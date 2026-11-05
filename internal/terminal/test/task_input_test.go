package test

import (
	"encoding/json"
	"fmt"
	"io"
	"kh/internal/terminal"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTaskConsoleProcess(t *testing.T) {
	if os.Getenv("KH_TASK_CONSOLE_HELPER") != "1" {
		return
	}
	c := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer c.Close()
	c.BeginTask(1)
	c.Start()
	go func() {
		for range c.Assignments {
			c.BeginTask(2)
			fmt.Println("TASK-BOUNDARY")
		}
	}()
	for line := range c.Lines {
		fmt.Printf("TASK-INPUT:%q\n", line)
	}
}

func TestTaskBoundaryClearsTerminalDraft(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for PTY")
	}
	frame, err := terminal.TaskFrame(terminal.TaskControl{Kind: "assign", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", `import os,pty,select,subprocess,sys,time
master,slave=pty.openpty()
p=subprocess.Popen([sys.argv[1],'-test.run=^TestTaskConsoleProcess$'],stdin=slave,stdout=slave,stderr=slave,env=dict(os.environ,KH_TASK_CONSOLE_HELPER='1',TERM='xterm-256color'))
os.close(slave)
buf=b''
def pump(sec):
 global buf
 end=time.monotonic()+sec
 while time.monotonic()<end:
  if select.select([master],[],[],.02)[0]: buf+=os.read(master,65536)
def expect(text):
 global buf
 end=time.monotonic()+3
 while text not in buf:
  if time.monotonic()>end: raise AssertionError((text,buf[-2000:]))
  pump(.02)
 buf=buf.split(text,1)[1]
try:
 expect(b'> ')
 os.write(master,b'old multiline\x1b[13;2u')
 expect(b'... ')
 os.write(master,b'y')
 pump(.1)
 os.write(master,sys.argv[2].encode())
 expect(b'TASK-BOUNDARY')
 os.write(master,b'\r')
 pump(.3)
 assert b'TASK-INPUT:"y"' not in buf,buf
 assert b'old multiline' not in buf,buf
 os.write(master,b'old y')
 pump(.1)
 os.write(master,sys.argv[2].encode())
 expect(b'TASK-BOUNDARY')
 os.write(master,b'first\x1b[13;2u')
 expect(b'... ')
 pump(.1)
 assert b'TASK-INPUT:"first"' not in buf,buf
 os.write(master,b'second\r')
 expect(b'TASK-INPUT:"first\\nsecond"')
finally:
 p.terminate()
 try:p.wait(timeout=2)
 except: p.kill()
 os.close(master)
`, os.Args[0], frame)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("draft isolation: %s: %v", out, err)
	}
}

func TestTaskControlSeparateInput(t *testing.T) {
	payload := json.RawMessage(`{"lease":{"id":"task","generation":2}}`)
	frame, err := terminal.TaskFrame(terminal.TaskControl{Kind: "assign", Generation: 2, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	c := terminal.NewConsole(strings.NewReader(frame+"human\n"), io.Discard, io.Discard)
	defer c.Close()
	c.Start()
	select {
	case got := <-c.Assignments:
		if string(got) != string(payload) {
			t.Fatalf("%s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment not decoded")
	}
	if got := <-c.Lines; got != "human" {
		t.Fatalf("assignment entered human input: %q", got)
	}
}

func TestTaskGenerationFencesPeerInput(t *testing.T) {
	reader, writer := io.Pipe()
	c := terminal.NewConsole(reader, io.Discard, io.Discard)
	defer c.Close()
	defer reader.Close()
	c.BeginTask(2)
	c.Start()
	old, _ := terminal.TaskFrame(terminal.TaskControl{Kind: "message", Generation: 1, Message: "old"})
	current, _ := terminal.TaskFrame(terminal.TaskControl{Kind: "message", Generation: 2, Message: "current"})
	go func() { _, _ = io.WriteString(writer, old+current); _ = writer.Close() }()
	select {
	case got := <-c.Lines:
		if got != "current" {
			t.Fatalf("%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("current message missing")
	}
}

func TestTaskBoundaryDrainsFullQueue(t *testing.T) {
	reader, writer := io.Pipe()
	c := terminal.NewConsole(reader, io.Discard, io.Discard)
	defer c.Close()
	defer reader.Close()
	defer writer.Close()
	c.Start()
	go func() { _, _ = io.WriteString(writer, strings.Repeat("stale\n", 50)) }()
	time.Sleep(10 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.BeginTask(2); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("boundary deadlocked behind full input queue")
	}
}
