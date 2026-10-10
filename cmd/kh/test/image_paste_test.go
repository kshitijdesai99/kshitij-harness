package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Exercise real terminal Ctrl-V bytes through an attached tmux client, not
// send-keys (which writes directly to a pane and bypasses root bindings).
func TestImagePasteBinding(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	t.Setenv("KH_PROVIDER", "")
	t.Setenv("KH_MODEL", "")
	t.Setenv("KH_EFFORT", "")
	t.Setenv("KH_AGENT", "")
	t.Setenv("KH_PARENT", "")
	if runtime.GOOS != "darwin" {
		t.Skip("clipboard images require macOS")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for a test pseudo-terminal")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", b, err)
	}
	for _, session := range []string{"0", "kh"} {
		t.Run(session, func(t *testing.T) {
			name := fmt.Sprintf("kh_paste_%d_%s", os.Getpid(), session)
			home := t.TempDir()
			fake := t.TempDir()
			// A fixed PNG simulates pasteboard bytes without changing the user's clipboard.
			png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII="
			if err := os.WriteFile(filepath.Join(fake, "swift"), []byte("#!/bin/sh\nprintf '%s\\n' '"+png+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			tmux := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("tmux", append([]string{"-L", name}, args...)...)
				cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+fake+":"+os.Getenv("PATH"))
				b, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("tmux %v: %s: %v", args, b, err)
				}
				return string(b)
			}
			// A private server, with no user's tmux.conf or panes affected.
			tmux("-f", "/dev/null", "new-session", "-d", "-s", session, "-n", "main", binary)
			defer tmux("kill-server")
			tmux("split-window", "-h", "-t", session+":main.0", "sh -c 'stty raw -echo; cat -v'")
			tmux("new-session", "-d", "-s", "unrelated", "sh -c 'stty raw -echo; cat -v'")
			var binding string
			for i := 0; i < 100; i++ {
				binding = tmux("list-keys", "-T", "root")
				if strings.Contains(binding, "#{@kh_image_paste}") {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !strings.Contains(binding, "#{@kh_image_paste}") {
				t.Fatalf("kh pane did not install root binding; screen: %q messages: %q", tmux("capture-pane", "-p", "-t", session+":main.0"), tmux("show-messages"))
			}
			if s := tmux("display-message", "-p", "-t", session+":main.0", "#{@kh_image_paste}"); strings.TrimSpace(s) != "1" {
				t.Fatalf("chat pane not marked: %q", s)
			}
			if s := tmux("display-message", "-p", "-t", session+":main.1", "#{@kh_image_paste}"); strings.TrimSpace(s) != "" {
				t.Fatalf("shell pane incorrectly marked: %q", s)
			}
			// Attach through a pseudo-terminal; select the intended pane, then
			// send actual 0x16 so tmux resolves the root key table itself.
			client := exec.Command("python3", "-c", `import os,pty,subprocess,sys,time
name,session,fake = sys.argv[1:]
master,slave = pty.openpty()
p = subprocess.Popen(['tmux','-L',name,'attach-session','-t',session], stdin=slave,stdout=slave,stderr=slave, start_new_session=True, env=dict(os.environ,TERM='xterm'))
os.close(slave)
try:
    time.sleep(.3)
    subprocess.check_call(['tmux','-L',name,'select-pane','-t',session+':main.0'])
    os.write(master,b'\x16')
    for i in range(50):
        if b'[[kh:image:' in subprocess.check_output(['tmux','-L',name,'capture-pane','-p','-t',session+':main.0']):
            break
        time.sleep(.1)
    else:
        raise RuntimeError('Ctrl-V did not capture image before moving focus')
    with open(fake+'/swift','w') as f:
        f.write("#!/bin/sh\necho 'clipboard has no image (pasteboard types: public.utf8-plain-text)' >&2\nexit 1\n")
    os.chmod(fake+'/swift',0o700)
    os.write(master,b'\x16')
    time.sleep(.3)
    screen = subprocess.check_output(['tmux','-L',name,'capture-pane','-p','-t',session+':main.0'])
    if screen.count(b'[[kh:image:') != 1:
        raise RuntimeError('Ctrl-V inserted a marker with no image')
    subprocess.check_call(['tmux','-L',name,'select-pane','-t',session+':main.1'])
    os.write(master,b'\x16X')
    time.sleep(.2)
    subprocess.check_call(['tmux','-L',name,'switch-client','-t','unrelated'])
    time.sleep(.1)
    os.write(master,b'\x16Y')
    time.sleep(.2)
finally:
    p.kill()
    p.wait(timeout=3)
    os.close(master)
`, name, session, fake)
			client.Env = append(os.Environ(), "HOME="+home, "PATH="+fake+":"+os.Getenv("PATH"))
			if b, err := client.CombinedOutput(); err != nil {
				t.Fatalf("attached client: %s: %v", b, err)
			}
			chat := tmux("capture-pane", "-p", "-t", session+":main.0")
			shell := tmux("capture-pane", "-p", "-t", session+":main.1")
			other := tmux("capture-pane", "-p", "-t", "unrelated:0.0")
			if !strings.Contains(chat, "[[kh:image:") {
				t.Errorf("chat did not receive captured image marker: %q; messages: %q", chat, tmux("show-messages"))
			}
			if strings.Contains(shell, "[[kh:image:") || !strings.Contains(shell, "^VX") {
				t.Errorf("shell did not receive ordinary Ctrl-V: %q; messages: %q", shell, tmux("show-messages"))
			}
			if strings.Contains(other, "[[kh:image:") || !strings.Contains(other, "^VY") {
				t.Errorf("other session did not receive ordinary Ctrl-V: %q", other)
			}
			// A text-only pasteboard must not inject another marker; the
			// diagnosis must say what was available rather than report only exit 1.
			if err := os.WriteFile(filepath.Join(fake, "swift"), []byte("#!/bin/sh\necho 'clipboard has no image (pasteboard types: public.utf8-plain-text)' >&2\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			pane := strings.TrimSpace(tmux("display-message", "-p", "-t", session+":main.0", "#{pane_id}"))
			cmd := exec.Command(binary, "clipboard-paste", pane)
			cmd.Env = append(os.Environ(), "TMUX="+strings.TrimSpace(tmux("display-message", "-p", "#{socket_path}"))+",1,0", "HOME="+home, "PATH="+fake+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "public.utf8-plain-text") {
				t.Errorf("clipboard error did not diagnose text-only input: %s %v", out, err)
			}
			if got := tmux("capture-pane", "-p", "-t", session+":main.0"); strings.Count(got, "[[kh:image:") != 1 {
				t.Errorf("failed paste changed input: %q", got)
			}
		})
	}
}

func TestImagePastePreservesUserBinding(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	t.Setenv("KH_PROVIDER", "")
	t.Setenv("KH_MODEL", "")
	t.Setenv("KH_EFFORT", "")
	t.Setenv("KH_AGENT", "")
	t.Setenv("KH_PARENT", "")
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", b, err)
	}
	name := fmt.Sprintf("kh_binding_%d", os.Getpid())
	home := t.TempDir()
	tmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", name}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, b, err)
		}
		return string(b)
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "other", "sleep 30")
	defer tmux("kill-server")
	tmux("bind-key", "-r", "-T", "root", "C-v", "send-keys", "-l", "user-paste")
	tmux("new-window", "-t", "other:", "-n", "chat", binary)
	for i := 0; i < 40; i++ {
		if strings.TrimSpace(tmux("display-message", "-p", "-t", "other:chat", "#{@kh_image_paste}")) == "1" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if option := tmux("display-message", "-p", "-t", "other:chat", "#{@kh_image_paste}"); strings.TrimSpace(option) != "1" {
		t.Fatalf("chat pane did not start: %q", option)
	}
	if keys := tmux("list-keys", "-T", "root"); !strings.Contains(keys, "C-v") || !strings.Contains(keys, "user-paste") || strings.Contains(keys, "#{@kh_image_paste}") {
		t.Fatalf("user binding overwritten: %q", keys)
	}
}
