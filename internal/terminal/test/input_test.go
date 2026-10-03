package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"testing"

	"kh/internal/terminal"
)

// Run the public reader in a separate process so raw terminal state and
// interrupt handling cannot interfere with the parent test runner.
func TestInputProcess(t *testing.T) {
	if os.Getenv("KH_INPUT_HELPER") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); fmt.Println("CANCELLED") }()
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	console.Start()
	for line := range console.Lines {
		if os.Getenv("KH_INPUT_JSON") == "1" {
			b, _ := json.Marshal(line)
			fmt.Printf("INPUT_JSON:%s\n", b)
		} else {
			fmt.Printf("INPUT:%s\n", line)
		}
	}
}

func TestPipedInput(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestInputProcess$")
	cmd.Env = append(os.Environ(), "KH_INPUT_HELPER=1")
	cmd.Stdin = strings.NewReader("first\nsecond")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "INPUT:first\nINPUT:second\n") {
		t.Fatalf("piped input: %s: %v", out, err)
	}
}

func TestTerminalHistory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for a test pseudo-terminal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,fcntl,termios,struct
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,80,0,0))
p=subprocess.Popen([sys.argv[1],'-test.run=^TestInputProcess$'],stdin=slave,stdout=slave,stderr=slave,env=dict(os.environ,KH_INPUT_HELPER='1',TERM='xterm',NO_COLOR=''))
os.close(slave)
buf=b''
def expect(text):
    global buf
    end=time.monotonic()+3
    while text not in buf:
        if time.monotonic()>end: raise AssertionError((text,buf))
        if select.select([master],[],[],.1)[0]: buf+=os.read(master,65536)
    buf=buf.split(text,1)[1]
def send(data,result):
    os.write(master,data)
    expect(b'INPUT:'+result+b'\r\n')
try:
    expect(b'\x1b[36m> ')
    os.write(master,b'first\r')
    expect(b'\x1b[36mfirst\x1b[0m')
    expect(b'INPUT:first\r\n')
    send(b'second\r',b'second')
    send(b'\x1b[A\r',b'second')
    send(b'\x1b[A\x1b[A\r',b'first')
    send(b'draft\x1b[A\x1b[B\r',b'draft')
    send(b'yes\r',b'yes')
    send(b'\x1b[A!\r',b'draft!')
    send('café\r'.encode(), 'café'.encode())
    os.write(master,b'\x03')
    expect(b'CANCELLED')
    send(b'after interrupt\r',b'after interrupt')
    os.write(master,b'\x04')
    p.wait(timeout=3)
    assert p.returncode==0,p.returncode
finally:
    if p.poll() is None: p.kill(); p.wait()
    os.close(master)
`, os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal history: %s: %v", out, err)
	}
}
