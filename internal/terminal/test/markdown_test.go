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
		source = "Safe &#27;[2J and &#27;]52;c;YWJj&#7; and \\u001b.\n"
	case "negative":
		source = "$- x$\n\n$+ x$\n\n\\[1. x\\]\n"
	case "width":
		source = strings.Repeat("readable words αβγ ", 14) + "\n"
	}
	// Arbitrary token boundaries, including bytes inside UTF-8 and Markdown.
	copy := r
	for i := 0; i < len(source); i++ {
		copy.Reply(source[i : i+1])
	}
	fmt.Println("BEFORE_FLUSH")
	r.FlushReply()
	fmt.Println("AFTER_FLUSH")
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
		{"no-color", "xterm", "1", "normal"},
		{"dumb", "dumb", "", "normal"},
		{"security", "xterm", "", "security"},
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
 text=re.sub(r'\x1b\[[0-9;:]*m','',raw)
 assert '\x1b' not in text and '\x07' not in text,repr(raw)
 if sys.argv[3] or sys.argv[2]=='dumb':assert '\x1b' not in raw,repr(raw)
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
