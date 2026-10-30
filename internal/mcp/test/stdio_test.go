package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"kh/internal/mcp"
	"kh/internal/tools"
)

// TestMCPProcess is a real newline-delimited MCP child, not an in-process mock.
func TestMCPProcess(t *testing.T) {
	if os.Getenv("KH_MCP_TEST_CHILD") != "1" {
		return
	}
	var writes sync.Mutex
	send := func(v any) { writes.Lock(); defer writes.Unlock(); _ = json.NewEncoder(os.Stdout).Encode(v) }
	var initialized atomic.Bool
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 10<<20)
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			if os.Getenv("KH_MCP_MODE") == "hang" {
				continue
			}
			caps := map[string]any{"tools": map[string]any{}}
			if os.Getenv("KH_MCP_MODE") == "content" {
				caps = map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2024-11-05", "capabilities": caps, "serverInfo": map[string]any{"name": "test", "version": "1"}}})
		case "notifications/initialized":
			if string(req.Params) == "null" {
				os.Exit(4)
			}
			initialized.Store(true)
		case "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get":
			if !initialized.Load() {
				os.Exit(3)
			}
			var p map[string]any
			_ = json.Unmarshal(req.Params, &p)
			var value any
			switch req.Method {
			case "resources/list":
				value = map[string]any{"resources": []any{map[string]any{"uri": "fixture://one", "name": "one"}}}
			case "resources/templates/list":
				value = map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "fixture://{id}", "name": "template"}}}
			case "resources/read":
				if p["uri"] == "fixture://error" {
					send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "secret-token"}})
					continue
				}
				value = map[string]any{"contents": []any{map[string]any{"uri": p["uri"], "text": "fixture content"}}}
			case "prompts/list":
				value = map[string]any{"prompts": []any{map[string]any{"name": "review"}}}
			case "prompts/get":
				value = map[string]any{"messages": []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "review fixture"}}}}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": value})
		case "tools/list":
			if os.Getenv("KH_MCP_MODE") == "content" {
				os.Exit(5)
			}
			if !initialized.Load() {
				os.Exit(3)
			}
			var p map[string]any
			_ = json.Unmarshal(req.Params, &p)
			schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}
			list := []any{map[string]any{"name": "echo", "description": "Echo test", "inputSchema": schema}}
			result := map[string]any{"tools": list}
			if p["cursor"] == nil {
				result["nextCursor"] = "page2"
			} else {
				result["tools"] = []any{map[string]any{"name": "inspect", "inputSchema": schema}}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Value string `json:"value"`
					Delay int    `json:"delay"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			go func(id json.RawMessage, name, value string, delay int) {
				time.Sleep(time.Duration(delay) * time.Millisecond)
				result := map[string]any{"content": []any{map[string]any{"type": "text", "text": value}}}
				if name == "inspect" {
					cwd, _ := os.Getwd()
					result["content"] = []any{map[string]any{"type": "text", "text": fmt.Sprintf("%d|%s|%s", os.Getpid(), cwd, os.Getenv("KH_MCP_VALUE"))}}
				}
				switch value {
				case "wait":
					return
				case "rpc":
					send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": "secret-token"}})
					return
				case "error":
					result["isError"] = true
				case "structured":
					result["structuredContent"] = map[string]any{"answer": 42}
				case "big":
					result["content"] = []any{map[string]any{"type": "text", "text": strings.Repeat("界", 100000)}}
				case "oversize":
					result["content"] = []any{map[string]any{"type": "text", "text": strings.Repeat("a", 9<<20)}}
				}
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
				if value == "exit" {
					os.Exit(0)
				}
			}(append(json.RawMessage(nil), req.ID...), p.Name, p.Arguments.Value, p.Arguments.Delay)
		}
	}
	os.Exit(0)
}

func childServer(t *testing.T) mcp.Server {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return mcp.Server{Command: exe, Args: []string{"-test.run=^TestMCPProcess$"}, Env: map[string]string{"KH_MCP_TEST_CHILD": "1"}, Type: "stdio"}
}
func stdioTools(t *testing.T, ctx context.Context, s mcp.Server) []tools.Tool {
	t.Helper()
	ts, close, warnings := mcp.Connect(ctx, map[string]mcp.Server{"local": s})
	t.Cleanup(close)
	if len(warnings) != 0 || len(ts) != 2 {
		t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
	}
	return ts
}
func invoke(t *testing.T, tool tools.Tool, value string) (string, error) {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"value": value})
	return tool.Run(context.Background(), input)
}

func TestStdioHandshakePaginationAndConcurrentRouting(t *testing.T) {
	ts := stdioTools(t, context.Background(), childServer(t))
	if ts[0].Name != "mcp__local__echo" || ts[0].Description != "Echo test" || ts[1].Name != "mcp__local__inspect" {
		t.Fatalf("tools=%+v", ts)
	}
	if ts[0].InputSchema["additionalProperties"] != false || len(ts[0].Required) != 1 {
		t.Fatalf("lost schema: %+v", ts[0])
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input, _ := json.Marshal(map[string]any{"value": fmt.Sprintf("value-%d", i), "delay": 40 - i})
			out, err := ts[0].Run(context.Background(), input)
			if err != nil || out != fmt.Sprintf("value-%d", i) {
				t.Errorf("call %d: %q %v", i, out, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestStdioEnvironmentWorkingDirectoryAndCleanup(t *testing.T) {
	t.Setenv("KH_MCP_SOURCE", "expanded-value")
	t.Setenv("KH_MCP_DIRECTORY", t.TempDir())
	s := childServer(t)
	s.Env["KH_MCP_VALUE"] = "${KH_MCP_SOURCE}"
	s.Dir = "$KH_MCP_DIRECTORY"
	ts, close, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"local": s})
	defer close()
	if len(warnings) != 0 {
		t.Fatal(warnings)
	}
	out, err := invoke(t, ts[1], "inspect")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(out, "|")
	if len(parts) != 3 || parts[2] != "expanded-value" {
		t.Fatalf("inspect=%q", out)
	}
	cwd, _ := filepath.EvalSymlinks(os.Getenv("KH_MCP_DIRECTORY"))
	actual, _ := filepath.EvalSymlinks(parts[1])
	if actual != cwd {
		t.Fatalf("cwd=%q want=%q", actual, cwd)
	}
	pid, _ := strconv.Atoi(parts[0])
	close()
	close()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d survived cleanup: %v", pid, err)
	}
	if _, err := invoke(t, ts[0], "after"); err == nil {
		t.Fatal("closed tool succeeded")
	}
}

func TestStdioCancellationKeepsSiblingCallsUsable(t *testing.T) {
	ts := stdioTools(t, context.Background(), childServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ts[0].Run(ctx, json.RawMessage(`{"value":"wait"}`))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation %v duration=%v", err, time.Since(start))
	}
	out, err := invoke(t, ts[0], "still-working")
	if err != nil || out != "still-working" {
		t.Fatalf("sibling call: %q %v", out, err)
	}
}

func TestStdioParentCancellationKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ts := stdioTools(t, ctx, childServer(t))
	out, err := invoke(t, ts[1], "inspect")
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.Split(out, "|")[0])
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d survived parent cancellation", pid)
}

func TestStdioStartupCancellationAndSecretRedaction(t *testing.T) {
	s := childServer(t)
	s.Env["KH_MCP_MODE"] = "hang"
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	ts, close, warnings := mcp.Connect(ctx, map[string]mcp.Server{"hang": s})
	defer close()
	if len(ts) != 0 || len(warnings) != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("tools=%v warnings=%v elapsed=%v", ts, warnings, time.Since(start))
	}
	s = childServer(t)
	s.Command = "/does-not-exist/secret-token"
	_, close2, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"broken": s})
	defer close2()
	if len(warnings) != 1 || strings.Contains(warnings[0], "secret-token") {
		t.Fatalf("warnings=%v", warnings)
	}
}

func TestStdioToolResultsAndBounds(t *testing.T) {
	ts := stdioTools(t, context.Background(), childServer(t))
	for _, value := range []string{"rpc", "error", "structured", "big"} {
		out, err := invoke(t, ts[0], value)
		switch value {
		case "rpc":
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("RPC error=%v", err)
			}
		case "error":
			if err == nil || out != "error" {
				t.Fatalf("isError=%q %v", out, err)
			}
		case "structured":
			if err != nil || !strings.Contains(out, `"answer":42`) {
				t.Fatalf("structured=%q %v", out, err)
			}
		case "big":
			if err != nil || len(out) > (256<<10)+100 || !strings.Contains(out, "truncated") || !json.Valid([]byte(strconv.Quote(out))) {
				t.Fatalf("bounded result bytes=%d err=%v", len(out), err)
			}
		}
	}
	if _, err := invoke(t, ts[0], "oversize"); err == nil {
		t.Fatal("oversized stdio response accepted")
	}
}

func TestStdioRunResultPreservesToolErrorContent(t *testing.T) {
	ts := stdioTools(t, context.Background(), childServer(t))
	if ts[0].RunResult == nil {
		t.Fatal("MCP tools need structured results to preserve error output")
	}
	result, err := ts[0].RunResult(context.Background(), json.RawMessage(`{"value":"error"}`))
	if err != nil || !result.IsError || result.Output != "error" {
		t.Fatalf("tool error output lost: %+v %v", result, err)
	}
	result, err = ts[0].RunResult(context.Background(), json.RawMessage(`{"value":"rpc"}`))
	if err == nil || result.Output != "" {
		t.Fatalf("transport RPC error must remain an error: %+v %v", result, err)
	}
}

func TestStdioCompletedReplyBeforeExit(t *testing.T) {
	for i := 0; i < 8; i++ {
		ts := stdioTools(t, context.Background(), childServer(t))
		out, err := invoke(t, ts[0], "exit")
		if err != nil || out != "exit" {
			t.Fatalf("reply before exit=%q %v", out, err)
		}
	}
}

func TestStdioContentOnlyServer(t *testing.T) {
	s := childServer(t)
	s.Env["KH_MCP_MODE"] = "content"
	ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"content": s})
	defer cleanup()
	if len(ts) != 5 || len(warnings) != 0 {
		t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
	}
	for _, tc := range []struct{ method, args, want string }{
		{"resources/list", `{}`, "fixture://one"},
		{"resources/templates/list", `{}`, "fixture://{id}"},
		{"resources/read", `{"uri":"fixture://one"}`, "fixture content"},
		{"prompts/list", `{}`, "review"},
		{"prompts/get", `{"name":"review"}`, "review fixture"},
	} {
		out, err := contentTool(t, ts, tc.method).Run(context.Background(), json.RawMessage(tc.args))
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: %q %v", tc.method, out, err)
		}
	}
	if _, err := contentTool(t, ts, "resources/read").Run(context.Background(), json.RawMessage(`{"uri":"fixture://error"}`)); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("remote error=%v", err)
	}
}

func TestStdioRejectsNonObjectArguments(t *testing.T) {
	ts := stdioTools(t, context.Background(), childServer(t))
	for _, input := range []string{"null", "[]", `"string"`, "{broken"} {
		if _, err := ts[0].Run(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
