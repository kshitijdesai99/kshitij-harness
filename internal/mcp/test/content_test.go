// Content tools exercise public Connect behavior through real HTTP and stdio servers.
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/mcp"
	"kh/internal/tools"
)

// contentFixture records all methods so startup and capability gating are observable.
func contentFixture(t *testing.T, capabilities map[string]any, handle func(context.Context, string, json.RawMessage) (any, bool)) ([]tools.Tool, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var req httpRPC
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		methods = append(methods, req.Method)
		mu.Unlock()
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var value any
		if req.Method == "initialize" {
			value = map[string]any{"protocolVersion": "2025-03-26", "capabilities": capabilities}
		} else if handle != nil {
			var ok bool
			value, ok = handle(r.Context(), req.Method, req.Params)
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "secret-remote-error"}})
				return
			}
		} else {
			t.Errorf("unexpected request %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": value})
	}))
	t.Cleanup(server.Close)
	ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"content": {URL: server.URL}})
	t.Cleanup(cleanup)
	if len(warnings) != 0 {
		t.Fatalf("connect warnings: %v", warnings)
	}
	return ts, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), methods...) }
}

func contentTool(t *testing.T, ts []tools.Tool, method string) tools.Tool {
	t.Helper()
	name := "mcp__content__" + strings.ReplaceAll(method, "/", "_")
	for _, tool := range ts {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing %s among %+v", name, ts)
	return tools.Tool{}
}

func TestContentCapabilitiesAreLazyAndIndependent(t *testing.T) {
	for _, capability := range []string{"resources", "prompts", "none"} {
		t.Run(capability, func(t *testing.T) {
			caps := map[string]any{}
			expected := 0
			if capability != "none" {
				caps[capability] = map[string]any{}
				expected = 2
			}
			if capability == "resources" {
				expected = 3
			}
			ts, methods := contentFixture(t, caps, nil)
			if len(ts) != expected || strings.Join(methods(), ",") != "initialize,notifications/initialized" {
				t.Fatalf("tools=%d methods=%v", len(ts), methods())
			}
		})
	}
}

func TestContentRejectsInvalidCapabilities(t *testing.T) {
	for _, value := range []string{`null`, `[]`, `"resources"`, `{"resources":null}`, `{"prompts":true}`, `{"tools":[]}`} {
		t.Run(value, func(t *testing.T) {
			f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "initialize" {
					t.Errorf("unexpected %s", msg.Method)
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-03-26","capabilities":%s}}`, msg.ID, value)
				return true
			})
			ts, cleanup, warnings := mcp.Connect(context.Background(), map[string]mcp.Server{"bad": {URL: f.server.URL}})
			defer cleanup()
			if len(ts) != 0 || len(warnings) != 1 {
				t.Fatalf("tools=%d warnings=%v", len(ts), warnings)
			}
		})
	}
}

func TestContentOperationsHTTP(t *testing.T) {
	ts, methods := contentFixture(t, map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}, func(_ context.Context, method string, raw json.RawMessage) (any, bool) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		switch method {
		case "resources/list":
			if p["cursor"] == nil {
				return map[string]any{"resources": []any{map[string]any{"uri": "test://one", "name": "one"}}, "nextCursor": "next"}, true
			}
			if p["cursor"] != "next" {
				t.Errorf("cursor=%v", p)
			}
			return map[string]any{"resources": []any{map[string]any{"uri": "test://two", "name": "two"}}}, true
		case "resources/templates/list":
			return map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "test://{id}", "name": "template"}}}, true
		case "resources/read":
			if p["uri"] != "test://one" {
				t.Errorf("uri=%v", p)
			}
			return map[string]any{"contents": []any{map[string]any{"uri": "test://one", "mimeType": "text/plain", "text": "hello"}, map[string]any{"uri": "test://image", "mimeType": "image/png", "blob": "YWJj"}}}, true
		case "prompts/list":
			return map[string]any{"prompts": []any{map[string]any{"name": "review", "arguments": []any{map[string]any{"name": "code", "required": true}}}}}, true
		case "prompts/get":
			if p["name"] != "review" || p["arguments"].(map[string]any)["code"] != "print(1)" {
				t.Errorf("prompt params=%v", p)
			}
			return map[string]any{"description": "review code", "messages": []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "Review this code"}}, map[string]any{"role": "assistant", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "test://one", "text": "example"}}}}}, true
		}
		return nil, false
	})
	cases := []struct{ method, args, want string }{
		{"resources/list", `{}`, `test://two`}, {"resources/templates/list", `{}`, `test://{id}`},
		{"resources/read", `{"uri":"test://one"}`, `YWJj`}, {"prompts/list", `{}`, `"required":true`},
		{"prompts/get", `{"name":"review","arguments":{"code":"print(1)"}}`, `"role":"assistant"`},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			tool := contentTool(t, ts, tc.method)
			if tool.InputSchema == nil || tool.RunResult == nil {
				t.Fatalf("tool metadata missing: %+v", tool)
			}
			out, err := tool.Run(context.Background(), json.RawMessage(tc.args))
			if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, tc.want) {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
	}
	if len(methods()) != 8 {
		t.Fatalf("methods=%v", methods())
	}
}

func TestContentBadArgumentsNeverReachServer(t *testing.T) {
	ts, methods := contentFixture(t, map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}, nil)
	cases := map[string][]string{
		"resources/list":           {`[]`, `null`, `{"cursor":"x"}`, `{"uri":"x"}`},
		"resources/templates/list": {`{"extra":true}`},
		"resources/read":           {`{}`, `{"uri":null}`, `{"uri":""}`, `{"uri":42}`, `{"uri":"x","extra":true}`, `{"uri":"x"} {}`},
		"prompts/get":              {`{}`, `{"name":null}`, `{"name":""}`, `{"name":"x","arguments":{"x":1}}`, `{"name":"x","arguments":null}`, `{"name":"x","arguments":[]}`, `{"name":"x","arguments":{"x":null}}`},
	}
	for method, inputs := range cases {
		for _, input := range inputs {
			if _, err := contentTool(t, ts, method).Run(context.Background(), json.RawMessage(input)); err == nil {
				t.Errorf("%s accepted %s", method, input)
			}
		}
	}
	if len(methods()) != 2 {
		t.Fatalf("invalid args sent: %v", methods())
	}
}

func TestContentPaginationAndResponseGuards(t *testing.T) {
	for _, method := range []string{"resources/list", "resources/templates/list", "prompts/list"} {
		for _, mode := range []string{"cycle", "page-limit", "entry-limit", "bytes-limit", "invalid", "remote"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				calls := 0
				key := map[string]string{"resources/list": "resources", "resources/templates/list": "resourceTemplates", "prompts/list": "prompts"}[method]
				ts, _ := contentFixture(t, map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}, func(_ context.Context, _ string, _ json.RawMessage) (any, bool) {
					calls++
					list := []any{map[string]any{"name": "entry", "uri": "test://one", "uriTemplate": "test://{id}"}}
					result := map[string]any{key: list}
					switch mode {
					case "cycle":
						result["nextCursor"] = "same"
					case "page-limit":
						result["nextCursor"] = fmt.Sprint(calls)
					case "entry-limit":
						for len(list) < 4097 {
							list = append(list, list[0])
						}
						result[key] = list
					case "bytes-limit":
						result["padding"] = strings.Repeat("x", 5<<20)
						result["nextCursor"] = fmt.Sprint(calls)
					case "invalid":
						result[key] = nil
					case "remote":
						return nil, false
					}
					return result, true
				})
				_, err := contentTool(t, ts, method).Run(context.Background(), json.RawMessage(`{}`))
				if err == nil || strings.Contains(err.Error(), "secret-remote-error") {
					t.Fatalf("guard err=%v", err)
				}
				wantCalls := 1
				switch mode {
				case "cycle", "bytes-limit":
					wantCalls = 2
				case "page-limit":
					wantCalls = 1000
				}
				if calls != wantCalls {
					t.Fatalf("calls=%d want=%d err=%v", calls, wantCalls, err)
				}
			})
		}
	}
}

func TestContentCancellationAndBoundedOutput(t *testing.T) {
	ts, _ := contentFixture(t, map[string]any{"resources": map[string]any{}}, func(ctx context.Context, _ string, raw json.RawMessage) (any, bool) {
		var p map[string]string
		_ = json.Unmarshal(raw, &p)
		if p["uri"] == "test://wait" {
			<-ctx.Done()
			return nil, false
		}
		if p["uri"] == "test://error" {
			return nil, false
		}
		if p["uri"] == "test://invalid" {
			return map[string]any{"contents": nil}, true
		}
		return map[string]any{"contents": []any{map[string]any{"uri": p["uri"], "text": strings.Repeat("界", 100000)}}}, true
	})
	tool := contentTool(t, ts, "resources/read")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := tool.Run(ctx, json.RawMessage(`{"uri":"test://wait"}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation=%v", err)
	}
	for _, uri := range []string{"test://error", "test://invalid"} {
		if _, err := tool.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri))); err == nil {
			t.Fatalf("accepted %s", uri)
		}
	}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"uri":"test://large"}`))
	if err != nil || len(out) > (256<<10)+100 || !strings.Contains(out, "truncated") {
		t.Fatalf("bytes=%d err=%v", len(out), err)
	}
}
