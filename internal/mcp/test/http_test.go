package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/mcp"
	"kh/internal/tools"
)

func TestHTTPContentCapabilitiesWithoutTools(t *testing.T) {
	fixture := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
		if msg.Method != "initialize" {
			return false
		}
		w.Header().Set("Mcp-Session-Id", "session-test")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}}})
		return true
	})
	ts, close, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"remote": {URL: fixture.server.URL}})
	defer close()
	if len(ts) != 5 || len(warnings) != 0 {
		t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
	}
}

type httpRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type httpFixture struct {
	t           *testing.T
	server      *httptest.Server
	sse         bool
	hook        func(http.ResponseWriter, *http.Request, httpRPC) bool
	initialized atomic.Bool
	deletes     atomic.Int32
}

func newHTTPFixture(t *testing.T, sse bool, hook func(http.ResponseWriter, *http.Request, httpRPC) bool) *httpFixture {
	t.Helper()
	f := &httpFixture{t: t, sse: sse, hook: hook}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *httpFixture) reply(w http.ResponseWriter, msg httpRPC, result any) {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	if err != nil {
		f.t.Error(err)
		return
	}
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		// Comments and notifications may precede the response on the POST stream.
		fmt.Fprint(w, ": keepalive\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
	}
}

func (f *httpFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Mcp-Session-Id") != "session-test" && r.Method == http.MethodDelete {
		f.t.Errorf("DELETE session = %q", r.Header.Get("Mcp-Session-Id"))
	}
	if r.Method == http.MethodDelete {
		f.deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		f.t.Errorf("unexpected method %s", r.Method)
		w.WriteHeader(405)
		return
	}
	var msg httpRPC
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		f.t.Errorf("decode request: %v", err)
		w.WriteHeader(400)
		return
	}
	if msg.JSONRPC != "2.0" {
		f.t.Errorf("JSON-RPC version = %q", msg.JSONRPC)
	}
	if r.Header.Get("Content-Type") != "application/json" || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		f.t.Errorf("missing content negotiation headers: %v", r.Header)
	}
	if msg.Method != "initialize" {
		if r.Header.Get("Mcp-Session-Id") != "session-test" || r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
			f.t.Errorf("session/protocol headers: %v", r.Header)
		}
	}
	if f.hook != nil && f.hook(w, r, msg) {
		return
	}
	switch msg.Method {
	case "initialize":
		if r.Header.Get("Mcp-Session-Id") != "" || r.Header.Get("MCP-Protocol-Version") != "" {
			f.t.Error("initialize carried negotiated headers")
		}
		var p struct {
			ProtocolVersion string         `json:"protocolVersion"`
			ClientInfo      map[string]any `json:"clientInfo"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil || p.ProtocolVersion == "" || p.ClientInfo["name"] != "kh" {
			f.t.Errorf("initialize params: %s", msg.Params)
		}
		w.Header().Set("Mcp-Session-Id", "session-test")
		f.reply(w, msg, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}})
	case "notifications/initialized":
		if len(msg.ID) != 0 {
			f.t.Error("initialized notification has ID")
		}
		f.initialized.Store(true)
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		if !f.initialized.Load() {
			f.t.Error("tools/list before initialized")
		}
		f.reply(w, msg, map[string]any{"tools": []any{httpTool("echo")}})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil || p.Name != "echo" {
			f.t.Errorf("call params: %s", msg.Params)
		}
		f.reply(w, msg, httpText(fmt.Sprint(p.Arguments["value"])))
	default:
		f.t.Errorf("unexpected RPC method %q", msg.Method)
		w.WriteHeader(400)
	}
}

func httpTool(name string) map[string]any {
	return map[string]any{"name": name, "description": "Echo a value", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}}
}
func httpText(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}
func connectHTTP(t *testing.T, ctx context.Context, f *httpFixture) ([]tools.Tool, func()) {
	t.Helper()
	ts, close, warnings := mcp.Connect(ctx, map[string]mcp.Server{"fixture": {URL: f.server.URL}})
	t.Cleanup(close)
	if len(warnings) != 0 || len(ts) == 0 {
		t.Fatalf("Connect tools=%d warnings=%v", len(ts), warnings)
	}
	return ts, close
}

func TestHTTPJSONAndSSEDiscovery(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("SSE=%t", sse), func(t *testing.T) {
			var pages atomic.Int32
			f := newHTTPFixture(t, sse, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/list" {
					return false
				}
				if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
					t.Error("missing negotiated protocol")
				}
				var p map[string]any
				_ = json.Unmarshal(msg.Params, &p)
				n := pages.Add(1)
				w.Header().Set("Content-Type", "application/json")
				result := map[string]any{"tools": []any{httpTool("echo")}, "nextCursor": "page-2"}
				if n == 1 {
					if _, ok := p["cursor"]; ok {
						t.Error("unexpected initial cursor")
					}
				} else {
					if n != 2 || p["cursor"] != "page-2" {
						t.Errorf("page %d params %s", n, msg.Params)
					}
					result = map[string]any{"tools": []any{httpTool("zebra")}}
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", body)
				} else {
					_, _ = w.Write(body)
				}
				return true
			})
			ts, close := connectHTTP(t, context.Background(), f)
			if len(ts) != 2 || pages.Load() != 2 || !f.initialized.Load() {
				t.Fatalf("tools=%d pages=%d initialized=%t", len(ts), pages.Load(), f.initialized.Load())
			}
			if ts[0].Name != "mcp__fixture__echo" || ts[0].Description != "Echo a value" {
				t.Errorf("tool = %+v", ts[0])
			}
			expected := httpTool("echo")["inputSchema"]
			encoded, _ := json.Marshal(expected)
			var schema map[string]any
			_ = json.Unmarshal(encoded, &schema)
			if !reflect.DeepEqual(ts[0].InputSchema, schema) || !reflect.DeepEqual(ts[0].Required, []string{"value"}) || !reflect.DeepEqual(ts[0].Params, schema["properties"]) {
				t.Errorf("schema lost: %+v", ts[0])
			}
			got, err := ts[0].Run(context.Background(), json.RawMessage(`{"value":"hello"}`))
			if err != nil || got != "hello" {
				t.Fatalf("call = %q, %v", got, err)
			}
			close()
			close()
			if f.deletes.Load() != 1 {
				t.Errorf("cleanup DELETE count = %d", f.deletes.Load())
			}
		})
	}
}

func TestHTTPConcurrentCallRouting(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("SSE=%t", sse), func(t *testing.T) {
			const count = 12
			var entered atomic.Int32
			release := make(chan struct{})
			f := newHTTPFixture(t, sse, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/call" {
					return false
				}
				if entered.Add(1) == count {
					close(release)
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return true
				}
				var p struct {
					Arguments map[string]int `json:"arguments"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": httpText(fmt.Sprint(p.Arguments["value"]))})
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", body)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}
				return true
			})
			ts, _ := connectHTTP(t, context.Background(), f)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			for i := 0; i < count; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					got, err := ts[0].Run(ctx, json.RawMessage(fmt.Sprintf(`{"value":%d}`, i)))
					if err != nil || got != fmt.Sprint(i) {
						t.Errorf("call %d = %q, %v", i, got, err)
					}
				}(i)
			}
			wg.Wait()
		})
	}
}

func TestHTTPCancellationAndCleanup(t *testing.T) {
	for _, mode := range []string{"call", "parent", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			var calls atomic.Int32
			f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/call" || calls.Add(1) != 1 {
					return false
				}
				close(started)
				<-r.Context().Done()
				close(stopped)
				return true
			})
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			ts, cleanup := connectHTTP(t, parent, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := ts[0].Run(ctx, json.RawMessage(`{}`)); done <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("call never started")
			}
			switch mode {
			case "parent":
				cancelParent()
			case "cleanup":
				cleanup()
			default:
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancel error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("call did not cancel")
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("request not canceled on server")
			}
			if mode == "call" {
				got, err := ts[0].Run(context.Background(), json.RawMessage(`{"value":"still alive"}`))
				if err != nil || got != "still alive" || f.deletes.Load() != 0 {
					t.Errorf("individual cancellation closed shared session: got=%q err=%v deletes=%d", got, err, f.deletes.Load())
				}
			}
			if mode == "parent" {
				deadline := time.Now().Add(3 * time.Second)
				for f.deletes.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if f.deletes.Load() != 1 {
					t.Error("parent cancellation did not delete the HTTP session")
				}
			}
			cleanup()
			cleanup()
			if f.deletes.Load() != 1 {
				t.Errorf("DELETE count = %d", f.deletes.Load())
			}
			if _, err := ts[0].Run(context.Background(), json.RawMessage(`{}`)); err == nil {
				t.Error("call succeeded after cleanup")
			}
		})
	}
}

func TestHTTPWarningsRedactSecretsAndLegacySSE(t *testing.T) {
	const secret = "sensitive-test-secret"
	for _, mode := range []string{"rpc", "status", "legacy404", "legacy405", "legacyType", "explicitSSE", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch mode {
				case "rpc":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":%q}}`, secret)
				case "status":
					w.WriteHeader(500)
					fmt.Fprint(w, secret)
				case "legacy404":
					w.WriteHeader(404)
				case "legacy405":
					w.WriteHeader(405)
				case "legacyType":
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, secret)
				case "redirect":
					http.Redirect(w, r, "https://"+secret+".invalid", http.StatusTemporaryRedirect)
				}
			}))
			defer server.Close()
			typ := "http"
			if mode == "explicitSSE" {
				typ = "sse"
			}
			ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"safe": {Type: typ, URL: server.URL + "/" + secret, Headers: map[string]string{"Authorization": "Bearer " + secret}}})
			defer cleanup()
			if len(ts) != 0 || len(warnings) != 1 {
				t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
			}
			warning := warnings[0]
			if strings.Contains(warning, secret) || strings.Contains(warning, server.URL) || strings.Contains(warning, "Bearer") {
				t.Errorf("secret in warning: %s", warning)
			}
			if strings.HasPrefix(mode, "legacy") || mode == "explicitSSE" {
				if !strings.Contains(strings.ToLower(warning), "sse") {
					t.Errorf("legacy SSE not explicit: %s", warning)
				}
			}
			if mode == "explicitSSE" && requests.Load() != 0 {
				t.Error("legacy SSE contacted server")
			}
		})
	}
}

func TestConnectKeepsHealthyServersAndReportsEachFailure(t *testing.T) {
	f := newHTTPFixture(t, false, nil)
	ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{
		"healthy":     {URL: f.server.URL},
		"bad-command": {Command: "/does-not-exist/secret-token"},
		"legacy":      {Type: "sse", URL: f.server.URL},
	})
	defer cleanup()
	if len(ts) != 1 || len(warnings) != 2 || ts[0].Name != "mcp__healthy__echo" {
		t.Fatalf("tools=%v warnings=%v", ts, warnings)
	}
	got, err := ts[0].Run(context.Background(), json.RawMessage(`{"value":"still usable"}`))
	if err != nil || got != "still usable" {
		t.Fatalf("healthy server failed after another startup failed: %q %v", got, err)
	}
}

func TestHTTPRejectsMismatchedResponses(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("SSE=%t", sse), func(t *testing.T) {
			f := newHTTPFixture(t, sse, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/call" {
					return false
				}
				body := `{"jsonrpc":"2.0","id":999999,"result":{"content":[{"type":"text","text":"wrong request"}]}}`
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", body)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, body)
				}
				return true
			})
			ts, _ := connectHTTP(t, context.Background(), f)
			got, err := ts[0].Run(context.Background(), json.RawMessage(`{}`))
			if err == nil || got != "" || !strings.Contains(err.Error(), "mismatch") {
				t.Errorf("mismatched response = %q, %v", got, err)
			}
		})
	}
}

func TestHTTPToolArgumentsPreserveExactNumbers(t *testing.T) {
	const arguments = `{"large":9007199254740993,"precise":1.234567890123456789}`
	f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
		if msg.Method == "tools/list" {
			tool := httpTool("echo")
			tool["inputSchema"].(map[string]any)["$defs"] = map[string]any{"large": map[string]any{"const": json.Number("9007199254740993")}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []any{tool}}})
			return true
		}
		if msg.Method != "tools/call" {
			return false
		}
		var params struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			t.Error(err)
		}
		if string(params.Arguments) != arguments {
			t.Errorf("arguments lost precision: %s", params.Arguments)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": httpText("ok")})
		return true
	})
	ts, _ := connectHTTP(t, context.Background(), f)
	schema, _ := json.Marshal(ts[0].InputSchema)
	if !strings.Contains(string(schema), `"const":9007199254740993`) {
		t.Fatalf("schema lost precision: %s", schema)
	}
	out, err := ts[0].Run(context.Background(), json.RawMessage(arguments))
	if err != nil || out != "ok" {
		t.Fatalf("tool result: %q %v", out, err)
	}
}

func TestHTTPEnvironmentExpansion(t *testing.T) {
	t.Setenv("KH_MCP_TEST_AUTH", "expanded-token")
	f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
		if r.Header.Get("Authorization") != "Bearer expanded-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		return false
	})
	t.Setenv("KH_MCP_TEST_URL", f.server.URL)
	ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"env": {URL: "${KH_MCP_TEST_URL}", Headers: map[string]string{"Authorization": "Bearer $KH_MCP_TEST_AUTH"}}})
	defer cleanup()
	if len(ts) != 1 || len(warnings) != 0 {
		t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
	}
	t.Setenv("KH_MCP_TEST_MISSING", "unused")
	// An invalid environment variable name cannot exist in a process environment.
	_, closeMissing, missing := mcp.Connect(context.Background(), map[string]mcp.Server{"missing": {URL: "${KH_MCP_TEST_URL}", Headers: map[string]string{"Authorization": "${KH_MCP_TEST_MISSING=SECRET}"}}})
	defer closeMissing()
	if len(missing) != 1 || !strings.Contains(missing[0], "environment variable") || strings.Contains(missing[0], "SECRET") {
		t.Errorf("missing variable warnings = %v", missing)
	}
}

func TestHTTPNamesBoundedAndCollisionSafe(t *testing.T) {
	f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
		if msg.Method == "tools/call" {
			var params struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": httpText(params.Name)})
			return true
		}
		if msg.Method != "tools/list" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []any{httpTool("a.b"), httpTool("a/b"), httpTool(strings.Repeat("long", 50))}}})
		return true
	})
	servers := map[string]mcp.Server{"a.b": {URL: f.server.URL}, "a/b": {URL: f.server.URL}, strings.Repeat("server", 30): {URL: f.server.URL}}
	var first []string
	for round := 0; round < 2; round++ {
		ts, cleanup, warnings := mcp.Connect(context.Background(), servers)
		if len(ts) != 9 || len(warnings) != 0 {
			cleanup()
			t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
		}
		seen := map[string]bool{}
		var names []string
		for index, tool := range ts {
			wantRemote := []string{"a.b", "a/b", strings.Repeat("long", 50)}[index%3]
			out, err := tool.Run(context.Background(), json.RawMessage(`{}`))
			if err != nil || out != wantRemote {
				t.Errorf("sanitized tool routed incorrectly: got %q err=%v want %q", out, err, wantRemote)
			}
			if len(tool.Name) > 64 || seen[tool.Name] || !strings.HasPrefix(tool.Name, "mcp__") {
				t.Errorf("invalid/duplicate name %q", tool.Name)
			}
			for _, r := range tool.Name {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
					t.Errorf("unsafe name %q", tool.Name)
					break
				}
			}
			seen[tool.Name] = true
			names = append(names, tool.Name)
		}
		if round == 0 {
			first = names
		} else if !reflect.DeepEqual(first, names) {
			t.Errorf("unstable names: %v vs %v", first, names)
		}
		cleanup()
	}
}

func TestHTTPBoundedResponses(t *testing.T) {
	for _, mode := range []string{"json", "sse", "output"} {
		t.Run(mode, func(t *testing.T) {
			f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/call" {
					return false
				}
				n := (8 << 20) + 1
				if mode == "output" {
					n = (256 << 10) + 100
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": httpText(strings.Repeat("x", n))})
				if mode == "sse" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", body)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}
				return true
			})
			ts, _ := connectHTTP(t, context.Background(), f)
			got, err := ts[0].Run(context.Background(), json.RawMessage(`{}`))
			if mode == "output" {
				if err != nil || !strings.HasSuffix(got, "...[MCP output truncated]") || len(got) > (256<<10)+64 {
					t.Errorf("output len=%d err=%v", len(got), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Errorf("oversize response error = %v", err)
			}
		})
	}
}

func TestHTTPPaginationLoopsAndInvalidSchemas(t *testing.T) {
	for _, mode := range []string{"cursorLoop", "pageLimit", "duplicateTool", "invalidSchema", "toolLimit", "discoveryLimit"} {
		t.Run(mode, func(t *testing.T) {
			var pages atomic.Int32
			f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/list" {
					return false
				}
				page := pages.Add(1)
				list := map[string]any{"tools": []any{}}
				switch mode {
				case "cursorLoop":
					list["nextCursor"] = "same"
				case "pageLimit":
					list["nextCursor"] = fmt.Sprint(page)
				case "duplicateTool":
					list["tools"] = []any{httpTool("same"), httpTool("same")}
				case "invalidSchema":
					tool := httpTool("bad")
					tool["inputSchema"] = map[string]any{"type": "array"}
					list["tools"] = []any{tool}
				case "toolLimit":
					many := make([]any, 4097)
					for i := range many {
						many[i] = httpTool(fmt.Sprint(i))
					}
					list["tools"] = many
				case "discoveryLimit":
					tool := httpTool(fmt.Sprint(page))
					tool["description"] = strings.Repeat("x", 5<<20)
					list["tools"] = []any{tool}
					list["nextCursor"] = fmt.Sprint(page)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": list})
				return true
			})
			ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"fixture": {URL: f.server.URL}})
			defer cleanup()
			if len(ts) != 0 || len(warnings) != 1 {
				t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
			}
			expected := "schema"
			count := int32(1)
			if mode == "cursorLoop" {
				expected = "cursor repeated"
				count = 2
			}
			if mode == "pageLimit" {
				expected = "pagination exceeds limit"
				count = 1000
			}
			if mode == "toolLimit" {
				expected = "tool count exceeds limit"
			}
			if mode == "discoveryLimit" {
				expected = "discovery exceeds size limit"
				count = 2
			}
			if !strings.Contains(warnings[0], expected) || pages.Load() != count {
				t.Errorf("pages=%d warning=%s", pages.Load(), warnings[0])
			}
		})
	}
}
