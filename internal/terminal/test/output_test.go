package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
)

// Keep synchronization off the terminal: even a test's status marker would
// otherwise repaint or contaminate the draft whose preservation we are testing.
func TestOutputProcess(t *testing.T) {
	dir := os.Getenv("KH_OUTPUT_HELPER")
	if dir == "" {
		return
	}
	mark := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	r := terminal.NewRenderer(console.Stdout(), console.Stderr())
	console.Start()
	go func() {
		for i := 0; ; i++ {
			line, ok := <-console.Lines
			if !ok {
				return
			}
			data, _ := json.Marshal(line)
			mark(fmt.Sprintf("line-%d", i), data)
		}
	}()
	steps := []func(){
		func() {
			source := "**Reply begins** " + strings.Repeat("readable words αβγ ", 14) + "\n\n1. one\n\n1. two\n"
			// Deliberately split both Markdown and UTF-8 across calls.
			for i := 0; i < len(source); i++ {
				r.Reply(source[i : i+1])
			}
		},
		r.FlushReply,
		func() { r.Reply("**partial**") },
		func() { r.Notice("STOPPED") }, // must flush stdout before stderr
		func() { r.Action("checking files") },
	}
	for i, step := range steps {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("go-%d", i))); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for output command")
			}
			time.Sleep(5 * time.Millisecond)
		}
		step()
		mark(fmt.Sprintf("done-%d", i), nil)
	}
	// The parent submits the still-active draft, then a second steering line.
	for {
		if _, err := os.Stat(filepath.Join(dir, "line-1")); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOutputPreservesActiveDraft(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for PTY")
	}
	for _, c := range []struct {
		name  string
		width int
	}{
		{"initial", 110},
		{"continuation", 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", outputDraftPTY, os.Args[0], t.TempDir(), fmt.Sprint(c.width), c.name)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PTY: %v\n%s", err, out)
			}
		})
	}
}

const outputDraftPTY = `import os,pty,select,subprocess,sys,time,fcntl,termios,struct,re,json
binary,root,width,scenario=sys.argv[1:]
width=int(width)
m,s=pty.openpty()
fcntl.ioctl(s,termios.TIOCSWINSZ,struct.pack('HHHH',24,width,0,0))
p=subprocess.Popen([binary,'-test.run=^TestOutputProcess$'],stdin=s,stdout=s,stderr=s,env=dict(os.environ,KH_OUTPUT_HELPER=root,KH_AUTO_REBUILD='0',TERM='xterm-256color',NO_COLOR='1'))
os.close(s)
buf=b''
def pump(seconds=.1):
 global buf
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  if select.select([m],[],[],.01)[0]:
   try: data=os.read(m,65536)
   except OSError: break
   buf+=data
def wait(predicate):
 end=time.monotonic()+4
 while not predicate():
  assert time.monotonic()<end,repr(buf[-4000:])
  pump(.02)
 pump()
def command(i):
 before=len(buf)
 open(root+'/go-'+str(i),'w').close()
 wait(lambda:os.path.exists(root+'/done-'+str(i)))
 return buf[before:]
def text(raw):
 return re.sub(r'\x1b\[[0-9;:]*m','',raw.decode()).replace('\r\n','\n')
def refresh(raw,output,prompt,draft):
 # A refresh must clear the current draft BEFORE output, then redraw it AFTER.
 # Matching the raw control protocol avoids depending on an external emulator.
 prefix,body=raw.split(output,1)
 assert b'\r' in prefix and re.search(rb'\x1b\[[0-9;]*[JK]',prefix),(prefix,raw)
 assert prompt+draft in body,repr(raw)
 assert b'first human paste' not in raw and b'second human paste' not in raw,repr(raw)
 return body
try:
 wait(lambda:b'\x1b[?2004h' in buf and b'> ' in buf)
 prompt=b'> '
 prefix=''
 if scenario=='continuation':
  os.write(m,b'first line\x1b[13;2u')
  wait(lambda:b'... ' in buf)
  prompt=b'... '
  prefix='first line\n'
 os.write(m,b'\x1b[200~first human paste\x1b[201~\x1b[200~second human paste\x1b[201~ tail')
 wait(lambda:len(re.findall(rb'\[paste [0-9a-f]{16}: 1 lines\]',buf))>=2 and b' tail' in buf)
 # Capture the stored placeholders, not their unpredictable IDs.
 placeholders=re.findall(rb'\[paste [0-9a-f]{16}: 1 lines\]',buf)
 draft=placeholders[-2]+placeholders[-1]+b' tail'
 os.write(m,b'\x1b[D\x1b[D')
 pump()
 assert not os.path.exists(root+'/line-0'),'draft submitted before Enter'
 assert command(0)==b'','Reply must remain silent until FlushReply'
 flushed=command(1)
 refresh(flushed,b'Reply begins',prompt,draft)
 body=text(flushed).split('Reply begins',1)[1].split(prompt.decode(),1)[0]
 assert '**' not in body and '2. two' in body,repr(flushed)
 assert 'αβγ' in body and all(len(line)<=width for line in ('Reply begins'+body).splitlines()),repr(body)
 assert command(2)==b'','partial Reply must remain buffered'
 stopped=command(3)
 refresh(stopped,b'partial',prompt,draft)
 refresh(stopped.split(b'partial',1)[1],b'STOPPED',prompt,draft)
 assert stopped.index(b'partial')<stopped.index(b'STOPPED'),repr(stopped)
 action=command(4)
 refresh(action,b'checking files',prompt,draft)
 assert b'    checking files' in action,'stderr terminal metadata lost through wrapper'
 assert not os.path.exists(root+'/line-0'),'output accidentally submitted draft'
 # Cursor position must survive every stdout/stderr refresh, not just the text.
 os.write(m,b'!\r')
 wait(lambda:os.path.exists(root+'/line-0'))
 with open(root+'/line-0') as f: assert json.load(f)==prefix+'first human pastesecond human paste ta!il'
 os.write(m,b'next steering\r')
 wait(lambda:os.path.exists(root+'/line-1'))
 with open(root+'/line-1') as f: assert json.load(f)=='next steering'
 p.wait(timeout=3)
 assert p.returncode==0,(p.returncode,buf)
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
`
