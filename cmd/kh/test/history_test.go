package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Unlike the reader-only PTY test, this exercises the built CLI, startup and
// real arrow bytes through an attached tmux client in a non-kh session.
func TestChatTerminalHistory(t *testing.T) {
	for _, tool := range []string{"tmux", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " needed for terminal integration")
		}
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,fcntl,termios,struct
binary,home=sys.argv[1:]
socket='kh_history_'+str(os.getpid())
env=dict(os.environ,HOME=home,TERM='xterm-256color')
env.pop('TMUX',None)
env.pop('TMUX_PANE',None)
def tmux(*args):
    return subprocess.check_output(['tmux','-L',socket,*args],env=env,stderr=subprocess.STDOUT)
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',30,120,0,0))
client=None
try:
    tmux('-f','/dev/null','new-session','-d','-s','7','-x','120','-y','30',binary)
    pane=tmux('display-message','-p','-t','7:0.0','#{pane_id}').decode().strip()
    tty=tmux('display-message','-p','-t',pane,'#{pane_tty}').decode().strip()
    def screen():
        return tmux('capture-pane','-p','-t',pane)
    def wait(check):
        end=time.monotonic()+5
        while not check():
            if time.monotonic()>end: raise AssertionError(screen())
            if client is not None and select.select([master],[],[],.05)[0]: os.read(master,65536)
            else: time.sleep(.05)
    wait(lambda:b'kh chat, session' in screen())
    with open(tty,'rb',buffering=0) as terminal:
        flags=termios.tcgetattr(terminal)[3]
        assert not flags & (termios.ICANON|termios.ECHO),flags
    client=subprocess.Popen(['tmux','-L',socket,'attach-session','-t','7'],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
    time.sleep(.3)
    os.write(master,b'/help\r')
    wait(lambda:b'/model [id]' in screen())
    os.write(master,b'\x1b[A')
    wait(lambda:screen().rstrip().endswith(b'> /help'))
    assert b'^[[A' not in screen(),screen()
    os.write(master,b'\x1b[B')
    wait(lambda:screen().rstrip().endswith(b'>'))
    os.write(master,b'/model\r')
    wait(lambda:b'(model ' in screen())
    os.write(master,b'\x1b[A\x1b[A')
    wait(lambda:screen().rstrip().endswith(b'> /help'))
    # Editing the recalled slash command must reach the CLI command handler,
    # not become a model request containing a literal escape sequence.
    os.write(master,b'\x15/model\r')
    wait(lambda:screen().count(b'(model ')>=2)
    os.write(master,b'\x04')
    client.wait(timeout=5)
    assert client.returncode==0,client.returncode
finally:
    subprocess.run(['tmux','-L',socket,'kill-server'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    if client is not None and client.poll() is None: client.kill();client.wait()
    os.close(master);os.close(slave)
`, binary, t.TempDir())
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chat terminal history: %s: %v", out, err)
	}
}
