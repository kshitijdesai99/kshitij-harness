package test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
)

func TestRedirectedOutputIsPlain(t *testing.T) {
	var w strings.Builder
	if got := terminal.Color(&w, terminal.Query, "hello"); got != "hello" {
		t.Fatalf("redirected output contains color: %q", got)
	}
}

func TestCommentaryAndStatisticsRedirected(t *testing.T) {
	var out, err strings.Builder
	r := terminal.NewRenderer(&out, &err)
	r.Commentary("Checking **files**.\n")
	r.Reply("Final answer.\n")
	r.Statistics("(10 tokens)")
	if out.String() != "Checking **files**.\nFinal answer.\n" || err.String() != "(10 tokens)\n" {
		t.Fatalf("out=%q err=%q", out.String(), err.String())
	}
}

func TestNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := terminal.Color(os.Stdout, terminal.Response, "hello"); got != "hello" {
		t.Fatalf("NO_COLOR ignored: %q", got)
	}
}

func TestQueryColorProcess(t *testing.T) {
	if os.Getenv("KH_QUERY_COLOR_HELPER") != "1" {
		return
	}
	r := terminal.NewRenderer(os.Stdout, os.Stderr)
	r.Query("user query")
	r.Query("[from kh:worker] peer message")
	// Queued and approval-held messages are joined before entering history.
	r.Query("user steering\n[from kh:worker] queued peer\nmore user steering")
	r.Query("[from kh:worker] peer steering\nuser followup")
	r.Query("mention [from kh:worker] in a user query")
}

func TestQueryColorsTTY(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for PTY")
	}
	for _, mode := range []string{"color", "no-color", "dumb"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,subprocess,sys
m,s=pty.openpty()
env=dict(os.environ,KH_QUERY_COLOR_HELPER='1',TERM='dumb' if sys.argv[2]=='dumb' else 'xterm',NO_COLOR='1' if sys.argv[2]=='no-color' else '')
p=subprocess.Popen([sys.argv[1],'-test.run=^TestQueryColorProcess$'],stdin=s,stdout=s,stderr=s,env=env)
os.close(s)
buf=b''
try:
 while True:
  try: data=os.read(m,65536)
  except OSError: break
  if not data: break
  buf+=data
 p.wait(timeout=3)
 assert p.returncode==0,(p.returncode,buf)
 expected=[(b'36',b'> user query'),(b'38;5;214',b'> [from kh:worker] peer message'),(b'36',b'> user steering\r\n'),(b'38;5;214',b'[from kh:worker] queued peer\r\n'),(b'36',b'more user steering'),(b'38;5;214',b'> [from kh:worker] peer steering\r\n'),(b'36',b'user followup'),(b'36',b'> mention [from kh:worker] in a user query')]
 if sys.argv[2]=='color':
  for color,text in expected:
   assert b'\x1b['+color+b'm'+text+b'\x1b[0m' in buf,buf
 else:
  assert b'\x1b[' not in buf,buf
  for _,text in expected: assert text in buf,buf
finally:
 if p.poll() is None: p.kill(); p.wait()
 os.close(m)
`, os.Args[0], mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("query colors: %s: %v", out, err)
			}
		})
	}
}

func TestPeerQueryRedirectedUnchanged(t *testing.T) {
	var out strings.Builder
	r := terminal.NewRenderer(&out, &out)
	const text = "user\n[from kh:worker] peer\nuser again"
	r.Query(text)
	if got := out.String(); got != "> "+text+"\n" {
		t.Fatalf("redirected query=%q", got)
	}
}

func TestDumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if got := terminal.Color(os.Stdout, terminal.Query, "hello"); got != "hello" {
		t.Fatalf("dumb terminal contains color: %q", got)
	}
}
