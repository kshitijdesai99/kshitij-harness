package test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPasteKeyCompletesBeforeImmediateEnter(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for a test pseudo-terminal")
	}
	name := fmt.Sprintf("kh_order_%d", os.Getpid())
	tmux := func(args ...string) string {
		t.Helper()
		b, err := exec.Command("tmux", append([]string{"-L", name}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, b, err)
		}
		return string(b)
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "order", "sh -c 'while IFS= read -r line; do printf \"LINE:%s\\n\" \"$line\"; done'")
	defer tmux("kill-server")
	// A slow clipboard reader must finish injecting the marker before tmux
	// delivers an Enter typed in the same terminal write as Ctrl-V.
	tmux("bind-key", "-T", "root", "C-v", "run-shell", "sleep .25; tmux -L "+name+" send-keys -l -t order:0.0 SNAPSHOT")
	client := exec.Command("python3", "-c", `import os,pty,subprocess,sys,time
name = sys.argv[1]
m,s = pty.openpty()
p = subprocess.Popen(['tmux','-L',name,'attach','-t','order'],stdin=s,stdout=s,stderr=s,start_new_session=True,env=dict(os.environ,TERM='xterm'))
os.close(s)
try:
    time.sleep(.2)
    os.write(m,b'\x16\r')
    time.sleep(.6)
finally:
    p.kill()
    p.wait(timeout=3)
    os.close(m)
`, name)
	if b, err := client.CombinedOutput(); err != nil {
		t.Fatalf("attached client: %s %v", b, err)
	}
	if screen := tmux("capture-pane", "-p", "-t", "order:0.0"); !strings.Contains(screen, "LINE:SNAPSHOT") {
		t.Fatalf("Enter overtook clipboard capture: %q", screen)
	}
}
