package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
	"kh/internal/tools"
)

func TestMarkdownRedirectedUnchanged(t *testing.T) {
	t.Setenv("TERM", "xterm")
	const source = "# Heading\n**bold** $x^2$\n"
	var out, err strings.Builder
	r := terminal.NewRenderer(&out, &err)
	for _, part := range []string{source[:5], source[5:]} {
		r.Reply(part)
	}
	r.FlushReply()
	if out.String() != source {
		t.Fatalf("redirected=%q", out.String())
	}
	f, e := os.CreateTemp(t.TempDir(), "output")
	if e != nil {
		t.Fatal(e)
	}
	r = terminal.NewRenderer(f, f)
	r.Reply(source)
	r.FlushReply()
	f.Close()
	b, e := os.ReadFile(f.Name())
	if e != nil || string(b) != source {
		t.Fatalf("file=%q err=%v", b, e)
	}
}

func TestMarkdownProcess(t *testing.T) {
	if os.Getenv("KH_MARKDOWN_HELPER") != "1" {
		return
	}
	r := terminal.NewRenderer(os.Stdout, os.Stdout)
	source := "# Heading\n\nA **bold** word and *italic*.\n\n1. one\n\n1. two\n\n[Docs][ref]\n\n[ref]: https://example.com\n\nMath: $x^2 + \\alpha$ and $a*b*c$. Unsupported: \\(\\unknown{x}\\).\n\n```text\n$x^2$\n\ncode stays\n```\n\n| Name | Value |\n| --- | --- |\n| Ada | 42 |\n\n"
	switch os.Getenv("KH_MARKDOWN_SCENARIO") {
	case "security":
		source = "Safe &#27;[2J and &#27;]52;c;YWJj&#7; and &#27;]8;;javascript:alert(1)&#27;\\forged&#27;]8;;&#27;\\ and \\u001b.\n"
	case "forgery":
		source = "&#27;[777;0;104116116112115058047047101118105108046101120097109112108101mhttps://good.example&#27;[778m\n"
	case "links":
		source = "[Config](vscode://file/Users/example/.kh)\n\n[Web][ref]\n\n[ref]: https://example.com/a%20b?q=1&x=2\n\n<https://example.org>\n\n[Mail](mailto:dev@example.com)\n\n[Local](file:///tmp/example)\n\n[Bad](javascript:alert%281%29)\n\n[Command](vscode://command/workbench.action.terminal.new)\n\n[Relative](../folder)\n\n[Broken](https://)\n\n[Control](https://example.com/&#27;]52;c;YWJj&#7;)\n"
	case "negative":
		source = "$- x$\n\n$+ x$\n\n\\[1. x\\]\n"
	case "width":
		source = strings.Repeat("readable words αβγ ", 14) + "\n"
	}
	if os.Getenv("KH_MARKDOWN_SCENARIO") == "links" {
		longURL := "https://example.com/" + strings.Repeat("long-path/", 12) + "end?q=1"
		source += "\n[Long](" + longURL + ")\n\n> [Quoted](" + longURL + ")\n\n"
		source += "| Link | Value |\n| --- | --- |\n| [Table](" + longURL + ") | x |\n\n"
		source += "[Space](<file:///tmp/my file.txt>)\n\n[Unicode](vscode://file/tmp/日本語)\n"
		source += "\n| First | Second |\n| --- | --- |\n| [A](https://first.example/" + strings.Repeat("x", 80) + ") | [B](https://b.co) |\n"
	}
	// Arbitrary token boundaries, including bytes inside UTF-8 and Markdown.
	copy := r
	for i := 0; i < len(source); i++ {
		copy.Reply(source[i : i+1])
	}
	fmt.Println("BEFORE_FLUSH")
	r.FlushReply()
	fmt.Println("AFTER_FLUSH")
	if os.Getenv("KH_MARKDOWN_SCENARIO") == "commentary" {
		copy.Commentary("Checking **files**.\n\n")
		copy.Commentary("A second line.\n")
		r.Action("$ checking files\nsecond action line") // actions flush pending commentary
		console := terminal.NewConsole(strings.NewReader(""), os.Stdout, os.Stdout)
		if ok, err := console.Approve(context.Background(), "live command\ncontinued command", true); err != nil || !ok {
			t.Fatalf("approval: ok=%v err=%v", ok, err)
		}
		console.Close()
		t.Chdir(t.TempDir())
		if _, err := tools.EditWithOutput(r.Action).Run(context.Background(), []byte(`{"edits":[{"path":"first.txt","old":"","new":"one"},{"path":"second.txt","old":"","new":"two"}]}`)); err != nil {
			t.Fatal(err)
		}
		r.Reply("Final **answer**.\n")
		r.FlushReply()
		r.Statistics("(ttft 1s, total 2s, 10 in)")
		r.Notice("ordinary notice")
	}
	r.Reply("**partial")
	r.Notice("STOPPED") // status/cancellation ordering flushes incomplete replies
	r.Reply("Next response")
	r.FlushReply()
	fmt.Println("DONE")
}

func TestMarkdownTTY(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for PTY")
	}
	for _, c := range []struct{ name, term, noColor, scenario string }{
		{"color", "xterm", "", "normal"},
		{"commentary", "xterm", "", "commentary"},
		{"commentary-no-color", "xterm", "1", "commentary"},
		{"commentary-dumb", "dumb", "", "commentary"},
		{"no-color", "xterm", "1", "normal"},
		{"dumb", "dumb", "", "normal"},
		{"security", "xterm", "", "security"},
		{"links", "xterm", "", "links"},
		{"links-no-color", "xterm", "1", "links"},
		{"forgery", "xterm", "", "forgery"},
		{"negative", "xterm", "1", "negative"},
		{"width", "xterm", "1", "width"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,subprocess,sys,fcntl,termios,struct,re,select,time
m,s=pty.openpty()
fcntl.ioctl(s,termios.TIOCSWINSZ,struct.pack('HHHH',24,42,0,0))
env=dict(os.environ,KH_MARKDOWN_HELPER='1',TERM=sys.argv[2],NO_COLOR=sys.argv[3],KH_MARKDOWN_SCENARIO=sys.argv[4])
p=subprocess.Popen([sys.argv[1],'-test.run=^TestMarkdownProcess$'],stdin=s,stdout=s,stderr=s,env=env)
os.close(s)
buf=b''
try:
 end=time.monotonic()+15
 while time.monotonic()<end:
  if select.select([m],[],[],.05)[0]:
   try:
    chunk=os.read(m,65536)
   except OSError:break
   if not chunk:break
   buf+=chunk
  elif p.poll() is not None:break
 p.wait(timeout=1)
 assert p.returncode==0,(p.returncode,buf)
 raw=buf.decode('utf-8').replace('\r\n','\n')
 links=re.findall(r'\x1b\]8;;([^\x1b\x07]*)\x1b\\',raw)
 unlinked=re.sub(r'\x1b\]8;;[^\x1b\x07]*\x1b\\','',raw)
 text=re.sub(r'\x1b\[[0-9;:]*m','',unlinked)
 assert '\x1b' not in text and '\x07' not in text,repr(raw)
 if sys.argv[3] or sys.argv[2]=='dumb':assert '\x1b' not in unlinked,repr(raw)
 assert sum(bool(link) for link in links)==links.count(''),repr(links)
 assert 'partial' in text and text.index('partial')<text.index('STOPPED')<text.index('Next response'),repr(text)
 if sys.argv[2]!='dumb':
  assert text.startswith('BEFORE_FLUSH\n'),repr(text)
 if sys.argv[4]=='normal':
  if sys.argv[2]=='dumb':
   assert '# Heading' in text and '**bold**' in text and '$x^2$' in text,repr(text)
  else:
   assert 'Heading' in text and '# Heading' not in text and '**bold**' not in text,repr(text)
   assert '1. one' in text and '2. two' in text,repr(text)
   assert 'https://example.com' in text,repr(text)
   assert 'x² + α' in text and 'a*b*c' in text and r'\(\unknown{x}\)' in text,repr(text)
   assert '$x^2$' in text and 'code stays' in text and 'Ada' in text and '42' in text,repr(text)
 elif sys.argv[4]=='commentary':
  if sys.argv[2]=='dumb':
   assert 'Checking **files**.\n\nA second line.\n$ checking files\nsecond action line\n$ live command\ncontinued command\nedit first.txt\nedit second.txt\nFinal **answer**.' in text,repr(text)
  else:
   assert '    Checking files.' in text and '    A second line.' in text,repr(text)
   assert '\nFinal answer.' in text,repr(text)
   assert '\n    $ checking files\n    second action line\n' in text,repr(text)
   assert '\n    $ live command\n    continued command\n' in text,repr(text)
   assert '\n    edit first.txt\n    edit second.txt\n' in text,repr(text)
  assert text.index('A second line.')<text.index('$ checking files')<text.index('Final '),repr(text)
  if not sys.argv[3] and sys.argv[2]!='dumb':
   assert '\x1b[90m    Checking files.' in raw,repr(raw)
   assert '\x1b[90m    $ checking files\n    second action line\n\x1b[0m' in raw,repr(raw)
   assert '\x1b[90m    $ live command\n    continued command\n\x1b[0m' in raw,repr(raw)
   assert '\x1b[90m    edit first.txt\n\x1b[0m' in raw and '\x1b[90m    edit second.txt\n\x1b[0m' in raw,repr(raw)
   assert '\x1b[38;5;215m(ttft 1s, total 2s, 10 in)\x1b[0m' in raw,repr(raw)
   assert '\x1b[90mordinary notice\x1b[0m' in raw,repr(raw)
  assert text.index('Checking')<text.index('Final answer' if sys.argv[2]!='dumb' else 'Final **answer**')<text.index('(ttft'),repr(text)
 elif sys.argv[4]=='forgery':
  assert not links and 'https://good.example' in text,repr(raw)
 elif sys.argv[4]=='links':
  targets=[link for link in links if link]
  for target in ['vscode://file/Users/example/.kh','https://example.com/a%20b?q=1&x=2','https://example.org','mailto:dev@example.com','file:///tmp/example']:
   assert target in targets,(target,targets,raw)
  assert all(target.startswith(('https://example.com/','https://example.org','https://first.example/','https://b.co','vscode://file/','mailto:','file:///')) for target in targets),repr(targets)
  long_target='https://example.com/'+'long-path/'*12+'end?q=1'
  assert targets.count(long_target)>=3,(targets,raw)
  assert 'https://first.example/'+'x'*80 in targets and 'https://b.co' in targets,(targets,raw)
  spans=re.findall(r'\x1b\]8;;([^\x1b]+)\x1b\\(.*?)\x1b\]8;;\x1b\\',raw,re.S)
  for target,body in spans:
   visible=re.sub(r'\x1b\[[0-9;:]*m','',body)
   assert '│' not in visible,(target,visible)
   if 'https://b.co' in visible:assert target=='https://b.co',(target,visible)
  assert 'vscode://file/tmp/日本語' in targets,(targets,raw)
  assert 'file:///tmp/myfile.txt' not in targets and 'file:///tmp/my file.txt' not in targets,repr(targets)
  assert all('│' not in target and '…' not in target for target in targets),repr(targets)
  assert 'javascript:' in text and 'Relative /folder' in text,repr(text)
 elif sys.argv[4]=='negative':
  assert '- x' in text and '+ x' in text and '1. x' in text and '•' not in text,repr(text)
 elif sys.argv[4]=='width':
  body=text.split('BEFORE_FLUSH\n',1)[1].split('AFTER_FLUSH',1)[0]
  assert 'αβγ' in body,repr(body)
  assert all(len(line)<=42 for line in body.splitlines()),repr(body)
 print(text)
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
`, os.Args[0], c.term, c.noColor, c.scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PTY: %v\n%s", err, out)
			}
		})
	}
}
