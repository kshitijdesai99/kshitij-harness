package test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestActivityLiveDescendantsTTY(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for PTY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,re,fcntl,termios,struct
m,s=pty.openpty()
fcntl.ioctl(s,termios.TIOCSWINSZ,struct.pack('HHHH',24,100,0,0))
p=subprocess.Popen([sys.argv[1],'-test.run=^TestActivityProcess$'],stdin=s,stdout=s,stderr=s,env=dict(os.environ,KH_ACTIVITY_HELPER='1',TERM='xterm'))
os.close(s)
buf=b''
def pump(seconds):
 global buf
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  if select.select([m],[],[],.01)[0]:buf+=os.read(m,65536)
def expect(text):
 end=time.monotonic()+4
 while text not in buf:
  if time.monotonic()>end:raise AssertionError((text,buf[-2000:]))
  pump(.02)
def titles():return re.findall(rb'\x1b\]2;([^\x07]*)\x07',buf)
def command(text,ack):
 global buf
 buf=b''
 os.write(m,text+b'\r');expect(ack+b'\r\n');pump(.25)
try:
 expect(b'> ')
 command(b'stop',b'STOPPED')
 command(b'count-three',b'COUNT_THREE')
 assert titles()==[b'test-agent (3 - M)'],titles() # Count changes render while idle.
 command(b'count-zero',b'COUNT_ZERO')
 assert titles()==[b'test-agent (0 - M)'],titles()
 command(b'count-leaf',b'COUNT_LEAF')
 assert titles()==[b'test-agent (0 - W)'],titles()
 command(b'count-zero',b'COUNT_ZERO')
 assert titles()==[b'test-agent (0 - M)'],titles()
 command(b'start',b'STARTED');pump(1.1)
 command(b'count-three',b'COUNT_THREE')
 # An old spinner tick may arrive before the command is read; inspect the settled title.
 assert titles() and re.match(rb'test-agent \(3 - M\) [|/\\-] working \([1-9][0-9]*s\)$',titles()[-1]),titles()
 command(b'phase',b'PHASE')
 assert titles() and b'test-agent (3 - M) ' in titles()[-1] and b'running bash' in titles()[-1],titles()
 command(b'count-concurrent',b'COUNT_CONCURRENT')
 assert titles() and re.match(rb'test-agent \(7 - M\) [|/\\-] running bash \([1-9][0-9]*s\)$',titles()[-1]),titles()[-5:]
 command(b'stop',b'STOPPED')
 assert titles()==[b'test-agent (7 - M)'],titles()
 command(b'count-zero',b'COUNT_ZERO')
 assert titles()==[b'test-agent (0 - M)'],titles()
 command(b'count-clear',b'COUNT_CLEAR')
 assert titles()==[b'test-agent'],titles()
 command(b'close',b'CLOSED')
 command(b'count-three',b'COUNT_THREE')
 assert not titles(),titles() # Late polling updates cannot write after Close.
 os.write(m,b'\x04')
 end=time.monotonic()+3
 while p.poll() is None and time.monotonic()<end:
  try:pump(.02)
  except OSError:break
 p.wait(timeout=1);assert p.returncode==0,p.returncode
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
`, os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("live descendant TTY titles: %s: %v", out, err)
	}
}
