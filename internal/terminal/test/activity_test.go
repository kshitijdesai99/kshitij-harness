package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"kh/internal/terminal"
	"os"
	"os/exec"
	"os/signal"
	"testing"
	"time"
)

func TestActivityRedirectedSilent(t *testing.T) {
	var out bytes.Buffer
	a := terminal.NewActivity(&out, "main")
	a.Start("working")
	a.Set("running bash")
	time.Sleep(450 * time.Millisecond)
	a.Stop()
	a.Close()
	a.Close()
	if out.Len() != 0 {
		t.Fatalf("redirected output: %q", out.String())
	}
	file, err := os.CreateTemp(t.TempDir(), "activity")
	if err != nil {
		t.Fatal(err)
	}
	a = terminal.NewActivity(file, "main")
	a.Start("working")
	time.Sleep(250 * time.Millisecond)
	a.Close()
	file.Close()
	if b, err := os.ReadFile(file.Name()); err != nil || len(b) != 0 {
		t.Fatalf("file output: %q %v", b, err)
	}
}
func TestActivityProcess(t *testing.T) {
	if os.Getenv("KH_ACTIVITY_HELPER") != "1" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	c := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer c.Close()
	c.Start()
	a := terminal.NewActivity(os.Stdout, "test-agent")
	defer a.Close()
	a.Start("working")
	go func() { <-ctx.Done(); a.Stop(); fmt.Println("CANCELLED") }()
	for line := range c.Lines {
		switch line {
		case "phase":
			a.Set("running bash")
			fmt.Println("PHASE")
		case "reply":
			(terminal.Renderer{Out: os.Stdout, Err: os.Stderr, Activity: a}).Reply("answer\n")
			fmt.Println("REPLIED")
		case "stop":
			a.Stop()
			fmt.Println("STOPPED")
		case "start":
			a.Start("working")
			fmt.Println("STARTED")
		case "close":
			a.Close()
			fmt.Println("CLOSED")
		case "exit":
			return
		default:
			b, _ := json.Marshal(line)
			fmt.Printf("INPUT_JSON:%s\n", b)
		}
	}
}
func TestActivityTTYLifecycle(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for PTY")
	}
	for _, term := range []string{"xterm", "dumb"} {
		t.Run(term, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,re,fcntl,termios,struct
m,s=pty.openpty()
fcntl.ioctl(s,termios.TIOCSWINSZ,struct.pack('HHHH',24,100,0,0))
p=subprocess.Popen([sys.argv[1],'-test.run=^TestActivityProcess$'],stdin=s,stdout=s,stderr=s,env=dict(os.environ,KH_ACTIVITY_HELPER='1',TERM=sys.argv[2]))
os.close(s)
buf=b''
def pump(seconds):
 global buf
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  if select.select([m],[],[],.02)[0]:buf+=os.read(m,65536)
def expect(text):
 global buf
 end=time.monotonic()+3
 while text not in buf:
  if time.monotonic()>end:raise AssertionError((text,buf[-1000:]))
  pump(.02)
 buf=buf.split(text,1)[1]
def titles():return re.findall(rb'\x1b\]2;([^\x07]*)\x07',buf)
def send(command,ack):
 os.write(m,command+b'\r');expect(ack+b'\r\n')
try:
 expect(b'> ')
 pump(.45)
 if sys.argv[2]=='dumb':assert not titles(),titles()
 else:
  assert len(titles())>=2,titles()
  assert any(b'working' in x for x in titles()),titles()
 # Spinning while a draft is typed must not alter or submit that draft.
 os.write(m,b'unfinished draft');pump(.45)
 assert b'INPUT_JSON:' not in buf,buf
 os.write(m,b'\r');expect(b'INPUT_JSON:"unfinished draft"\r\n')
 send(b'phase',b'PHASE');pump(.25)
 if sys.argv[2]!='dumb':assert any(b'running bash' in x for x in titles()),titles()
 send(b'reply',b'REPLIED');pump(.25)
 if sys.argv[2]!='dumb':assert any(b'responding' in x for x in titles()),titles()
 send(b'stop',b'STOPPED');buf=b'';pump(.45);assert not titles(),titles()
 send(b'start',b'STARTED');pump(.25)
 if sys.argv[2]!='dumb':assert titles(),buf
 os.write(m,b'\x03');expect(b'CANCELLED\r\n');buf=b'';pump(.45);assert not titles(),titles()
 send(b'start',b'STARTED');pump(.25)
 send(b'close',b'CLOSED');buf=b'';pump(.45);assert not titles(),titles()
 send(b'close',b'CLOSED')
 # Quit through the real editor EOF path, rather than closing readline
 # concurrently from an artificial helper command.
 os.write(m,b'\x04')
 # Drain terminal output while the helper exits; a full PTY output buffer
 # otherwise blocks the line editor's final redraw/terminal restoration.
 end=time.monotonic()+3
 while p.poll() is None and time.monotonic()<end:
  try:pump(.02)
  except OSError:break
 p.wait(timeout=1);assert p.returncode==0,p.returncode
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
`, os.Args[0], term)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("TTY activity: %s %v", out, err)
			}
		})
	}
}
