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

func TestTerminalPeerDraftIsolation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for a test pseudo-terminal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,base64,json,fcntl,termios,struct
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,100,0,0))
p=subprocess.Popen([sys.argv[1],'-test.run=^TestInputProcess$'],stdin=slave,stdout=slave,stderr=slave,env=dict(os.environ,KH_INPUT_HELPER='1',KH_INPUT_JSON='1',KH_AUTO_REBUILD='0',TERM='xterm',NO_COLOR=''))
os.close(slave)
buf=b''
def pump(seconds):
    global buf
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        if select.select([master],[],[],.02)[0]: buf+=os.read(master,65536)
def expect(text):
    global buf
    end=time.monotonic()+3
    while text not in buf:
        if time.monotonic()>end: raise AssertionError((text,buf[-2000:]))
        pump(.02)
    buf=buf.split(text,1)[1]
def result(text):
    return b'INPUT_JSON:'+json.dumps(text,ensure_ascii=False).encode()+b'\r\n'
def peer(text):
    frame=b'\x1b]kh-peer;'+base64.b64encode(text.encode()).rstrip(b'=')+b'\x07'
    # Fragment every byte to exercise framing across terminal reads.
    for byte in frame: os.write(master,bytes([byte]));time.sleep(.001)
    expect(result(text))
def unsent():
    pump(.1)
    assert b'INPUT_JSON:' not in buf,buf
try:
    expect(b'\x1b[?2004h')
    expect(b'> ')
    # Preserve Unicode, the cursor position, and insertion at that position.
    os.write(master,'café draft'.encode()+b'\x1b[D\x1b[D')
    peer('[from kh:worker] yes')
    peer('[from kh:other] [paste 0123456789abcdef: 1 lines]')
    unsent()
    os.write(master,b'!\r')
    expect(result('café dra!ft'))
    # The peer must not be saved to editor history.
    os.write(master,b'\x1b[A\r')
    expect(result('café dra!ft'))
    # Preserve already continued lines as well as the current line.
    os.write(master,'first 日本語'.encode()+b'\x1b[13;2u')
    expect(b'... ')
    os.write(master,b'second')
    peer('[from kh:worker] multiline interruption')
    unsent()
    os.write(master,b' end\r')
    expect(result('first 日本語\nsecond end'))
    # A peer arriving inside an unfinished paste is not part of its payload.
    os.write(master,b'prefix \x1b[200~pasted\n')
    peer('[from kh:worker] active paste')
    unsent()
    os.write(master,b'text\x1b[201~ suffix')
    pump(.1)
    unsent()
    os.write(master,b'\r')
    expect(result('prefix pasted\ntext suffix'))
    # Cancellation and EOF still operate on the human editor only.
    os.write(master,b'discard')
    peer('[from kh:worker] before cancel')
    os.write(master,b'\x03')
    expect(b'CANCELLED')
    os.write(master,b'fresh\r')
    expect(result('fresh'))
    os.write(master,b'\x04')
    p.wait(timeout=3)
    assert p.returncode==0,p.returncode
finally:
    if p.poll() is None:p.kill();p.wait()
    os.close(master)
`, os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal peer isolation: %s: %v", out, err)
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
transcript=b''
ready_count=0
def pump():
    global buf,transcript
    if select.select([master],[],[],.02)[0]:
        data=os.read(master,65536)
        buf+=data;transcript+=data
def expect(text):
    global buf
    end=time.monotonic()+3
    while text not in buf:
        if time.monotonic()>end: raise AssertionError((text,buf))
        pump()
    buf=buf.split(text,1)[1]
def ready():
    global ready_count
    # Results and the next prompt come from different goroutines. Observe the
    # empty next prompt even if it printed before the result, then check raw
    # mode so an early CR cannot be line-discipline translated into Ctrl-J.
    prompt=b'\x1b[36m> \x1b[0m\x1b[36m\x1b[0m \x08'
    end=time.monotonic()+3
    while transcript.count(prompt)<=ready_count or termios.tcgetattr(master)[3]&termios.ICANON:
        if time.monotonic()>end: raise AssertionError(('editor not ready',buf))
        pump()
    ready_count=transcript.count(prompt)
def send(data,result):
    os.write(master,data)
    expect(b'INPUT:'+result+b'\r\n')
    ready()
try:
    expect(b'\x1b[36m> ')
    ready()
    os.write(master,b'first\r')
    expect(b'\x1b[36mfirst\x1b[0m')
    expect(b'INPUT:first\r\n')
    ready()
    os.write(master,b'[from kh:worker] peer coordination\r')
    expect(b'\x1b[38;5;214m[from kh:worker] peer coordination\x1b[0m')
    expect(b'INPUT:[from kh:worker] peer coordination\r\n')
    ready()
    os.write(master,b'second\r')
    expect(b'\x1b[36msecond\x1b[0m')
    expect(b'INPUT:second\r\n')
    ready()
    send(b'\x1b[A\r',b'second')
    send(b'\x1b[A\x1b[A\x1b[A\r',b'first')
    send(b'draft\x1b[A\x1b[B\r',b'draft')
    send(b'yes\r',b'yes')
    send(b'\x1b[A!\r',b'draft!')
    send('café\r'.encode(), 'café'.encode())
    os.write(master,b'\x03')
    expect(b'CANCELLED')
    ready()
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
