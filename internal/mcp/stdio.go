package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type stdioTransport struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     io.ReadCloser
	writes  chan struct{}
	mu      sync.Mutex
	pending map[string]chan rpcMessage
	dead    bool
	done    chan struct{}
	waited  chan struct{}
	once    sync.Once
	next    atomic.Uint64
}

func newStdio(ctx context.Context, s Server) (transport, error) {
	if s.Command == "" || s.URL != "" {
		return nil, fmt.Errorf("stdio requires a command and no URL")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Dir = s.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	for key, value := range s.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cannot create MCP stdio pipe")
	}
	// Own the stdout pipe: exec.Wait must not close it before buffered replies
	// have been consumed by the reader.
	out, childOut, err := os.Pipe()
	if err != nil {
		in.Close()
		return nil, fmt.Errorf("cannot create MCP stdio pipe")
	}
	cmd.Stdout = childOut
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		childOut.Close()
		return nil, fmt.Errorf("cannot start MCP process")
	}
	childOut.Close()
	t := &stdioTransport{cmd: cmd, in: in, out: out, writes: make(chan struct{}, 1), pending: map[string]chan rpcMessage{}, done: make(chan struct{}), waited: make(chan struct{})}
	stop := context.AfterFunc(ctx, t.stop)
	go func() { <-t.done; stop() }()
	go t.read()
	go func() {
		cmd.Wait()
		// The process has exited; kill children holding stdout open, then let the
		// reader drain complete replies before EOF closes the connection.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		close(t.waited)
	}()
	return t, nil
}

func (t *stdioTransport) version(string) {}
func (t *stdioTransport) stop() {
	t.once.Do(func() {
		t.mu.Lock()
		t.dead = true
		close(t.done)
		t.mu.Unlock()
		// Kill the process group, including children retaining inherited pipes.
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
		_ = t.in.Close()
		_ = t.out.Close()
	})
}
func (t *stdioTransport) close() {
	t.stop()
	select {
	case <-t.waited:
	case <-time.After(2 * time.Second):
	}
}
func (t *stdioTransport) write(ctx context.Context, message any) error {
	data, err := json.Marshal(message)
	if err != nil || len(data) > maxResponse {
		return fmt.Errorf("MCP request exceeds size limit")
	}
	select {
	case t.writes <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return fmt.Errorf("MCP stdio connection closed")
	}
	defer func() { <-t.writes }()
	// A misbehaving server may stop reading stdin. Closing/killing it unblocks
	// the single writer; no goroutine or pending call survives cancellation.
	sent := make(chan error, 1)
	go func() { _, err := t.in.Write(append(data, '\n')); sent <- err }()
	select {
	case err := <-sent:
		if err != nil {
			t.stop()
			return fmt.Errorf("MCP stdio write failed")
		}
		return nil
	case <-ctx.Done():
		t.stop()
		return ctx.Err()
	case <-t.done:
		return fmt.Errorf("MCP stdio connection closed")
	}
}
func (t *stdioTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := strconv.FormatUint(t.next.Add(1), 10)
	response := make(chan rpcMessage, 1)
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return nil, fmt.Errorf("MCP stdio connection closed")
	}
	t.pending[id] = response
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.pending, id); t.mu.Unlock() }()
	if err := t.write(ctx, map[string]any{"jsonrpc": "2.0", "id": json.Number(id), "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case message := <-response:
		return result(message)
	case <-ctx.Done():
		// Best-effort protocol cancellation; a canceled individual call does not
		// tear down the shared process when the writer is still responsive.
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = t.notify(cancelCtx, "notifications/cancelled", map[string]any{"requestId": json.Number(id), "reason": "client canceled"})
		cancel()
		return nil, ctx.Err()
	case <-t.done:
		select {
		case message := <-response:
			return result(message)
		default:
		}
		return nil, fmt.Errorf("MCP stdio connection closed")
	}
}
func (t *stdioTransport) notify(ctx context.Context, method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	return t.write(ctx, message)
}
func (t *stdioTransport) read() {
	defer t.stop()
	scanner := bufio.NewScanner(t.out)
	scanner.Buffer(make([]byte, 4096), maxResponse+1)
	for scanner.Scan() {
		if len(scanner.Bytes()) > maxResponse {
			return
		}
		var m rpcMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil || m.JSONRPC != "2.0" {
			return
		}
		if m.Method != "" {
			if len(m.ID) != 0 {
				reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
				if m.Method == "ping" {
					reply["result"] = map[string]any{}
				} else {
					reply["error"] = map[string]any{"code": -32601, "message": "Client method not supported"}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				err := t.write(ctx, reply)
				cancel()
				if err != nil {
					return
				}
			}
			continue
		}
		t.mu.Lock()
		ch := t.pending[string(m.ID)]
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
		t.mu.Unlock()
	}
}
