package test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
)

const pasteStart = "\x1b[200~"
const pasteFinish = "\x1b[201~"

func collectInput(in io.Reader) ([]string, string) {
	var diagnostics bytes.Buffer
	c := terminal.NewConsole(in, io.Discard, &diagnostics)
	defer c.Close()
	c.Start()
	var lines []string
	for line := range c.Lines {
		lines = append(lines, line)
	}
	return lines, diagnostics.String()
}

type fragmentedInput struct{ io.Reader }

func (r fragmentedInput) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestPastePreservesOneMessage(t *testing.T) {
	input := "prefix " + pasteStart + "\r\nfirst\r\nsecond\rthird\n" + pasteFinish + " suffix\nnext\n"
	for _, in := range []io.Reader{strings.NewReader(input), fragmentedInput{strings.NewReader(input)}} {
		got, diagnostics := collectInput(in)
		want := []string{"prefix \nfirst\nsecond\nthird\n suffix", "next"}
		if !reflect.DeepEqual(got, want) || diagnostics != "" {
			t.Fatalf("got=%q diagnostics=%q", got, diagnostics)
		}
	}
}

func TestPasteMultipleBlocksAndLiteralControls(t *testing.T) {
	payload := "yes\n/model other\n/image something\x03\x04\x1b[A"
	input := pasteStart + payload + pasteFinish + " middle " + pasteStart + "café 日本語" + pasteFinish + "\n"
	got, diagnostics := collectInput(strings.NewReader(input))
	if !reflect.DeepEqual(got, []string{payload + " middle café 日本語"}) || diagnostics != "" {
		t.Fatalf("got=%q diagnostics=%q", got, diagnostics)
	}
}

func TestPasteDoesNotSubmitBeforeEnter(t *testing.T) {
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	c := terminal.NewConsole(in, io.Discard, io.Discard)
	defer c.Close()
	c.Start()
	payload := "first\nsecond\nthird\n"
	if _, err := io.WriteString(out, pasteStart+payload+pasteFinish); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-c.Lines:
		t.Fatalf("paste submitted without Enter: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-c.Lines:
		if got != payload {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter did not submit paste")
	}
}

func TestPasteSizeLimitAndIncompletePaste(t *testing.T) {
	got, diagnostics := collectInput(strings.NewReader(pasteStart + strings.Repeat("x", (4<<20)+1) + pasteFinish + "\nnext\n"))
	if !reflect.DeepEqual(got, []string{"next"}) || !strings.Contains(diagnostics, "4 MiB") {
		t.Fatalf("got=%q diagnostics=%q", got, diagnostics)
	}
	got, diagnostics = collectInput(strings.NewReader("prior\ndraft " + pasteStart + "first\nsecond"))
	if !reflect.DeepEqual(got, []string{"prior"}) || !strings.Contains(diagnostics, "incomplete bracketed paste") {
		t.Fatalf("partial paste escaped: got=%q diagnostics=%q", got, diagnostics)
	}
}

func TestPasteEscapeSequencePreservation(t *testing.T) {
	// A false end-marker prefix and adjacent ESC must remain payload data.
	payload := "a\x1b[20x\x1b\x1b[Aend"
	got, diagnostics := collectInput(fragmentedInput{strings.NewReader(pasteStart + payload + pasteFinish + "\n")})
	if !reflect.DeepEqual(got, []string{payload}) || diagnostics != "" {
		t.Fatalf("got=%q diagnostics=%q", got, diagnostics)
	}
}

func TestTerminalBracketedPaste(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for pseudo-terminal integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,fcntl,termios,struct,json
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,100,0,0))
p=subprocess.Popen([sys.argv[1],'-test.run=^TestInputProcess$'],stdin=slave,stdout=slave,stderr=slave,env=dict(os.environ,KH_INPUT_HELPER='1',KH_INPUT_JSON='1',TERM='xterm',NO_COLOR=''))
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
        if time.monotonic()>end: raise AssertionError((text[:100],buf[-1000:]))
        pump(.02)
    buf=buf.split(text,1)[1]
def submit(text):
    os.write(master,b'\r')
    expect(b'INPUT_JSON:'+json.dumps(text,ensure_ascii=False).encode()+b'\r\n')
try:
    expect(b'\x1b[?2004h')
    expect(b'\x1b[36m> ')
    payload='first\r\nsecond\rthird\nyes\n/model other\ncafé 日本語\x03\x04'
    frame=b'\x1b[200~'+payload.encode()+b'\x1b[201~'
    for i in range(0,len(frame),3): os.write(master,frame[i:i+3]);time.sleep(.001)
    pump(.15)
    assert b'INPUT_JSON:' not in buf,buf
    assert b'CANCELLED' not in buf,buf
    assert b'[paste ' in buf,buf
    normalized=payload.replace('\r\n','\n').replace('\r','\n')
    submit(normalized)
    # Recalled paste remains one editable placeholder and resubmits one block.
    os.write(master,b'\x1b[A')
    submit(normalized)
    os.write(master,b'prefix \x1b[200~line one\nline two\n\x1b[201~ suffix')
    pump(.1)
    assert b'INPUT_JSON:' not in buf,buf
    submit('prefix line one\nline two\n suffix')
    # A genuinely large paste also emits one record, with no per-line turns.
    large='\n'.join('line '+str(i)+' café' for i in range(1000))+'\n'
    frame=b'\x1b[200~'+large.encode()+b'\x1b[201~'
    for i in range(0,len(frame),127): os.write(master,frame[i:i+127])
    pump(.1)
    assert b'INPUT_JSON:' not in buf,buf[-1000:]
    submit(large)
    # Discarding a pasted draft must discard the entire block.
    os.write(master,b'\x1b[200~discard\nthis\x1b[201~\x15ordinary')
    submit('ordinary')
    # Normal typing and history are still available after a paste.
    os.write(master,b'ordinary')
    submit('ordinary')
    os.write(master,b'\x1b[A!')
    submit('ordinary!')
    os.write(master,b'\x04')
    expect(b'\x1b[?2004l')
    p.wait(timeout=3)
    assert p.returncode==0,p.returncode
finally:
    if p.poll() is None:p.kill();p.wait()
    os.close(master)
`, os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal paste: %s: %v", out, err)
	}
}
