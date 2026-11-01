package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"kh/internal/mcp"
)

func TestConnectorStartupReports(t *testing.T) {
	for _, kind := range []string{"tools", "resources", "empty", "failure"} {
		t.Run(kind, func(t *testing.T) {
			var methods []string
			var mu sync.Mutex
			fixture := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				mu.Lock()
				methods = append(methods, msg.Method)
				mu.Unlock()
				if msg.Method == "initialize" {
					if kind == "failure" {
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": "secret-remote-error"}})
						return true
					}
					caps := map[string]any{}
					if kind != "empty" {
						caps[kind] = map[string]any{}
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Mcp-Session-Id", "session-test")
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": caps}})
					return true
				}
				if msg.Method == "tools/list" {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []any{map[string]any{"name": "getJiraIssue", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "slack.search", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "slack/search", "inputSchema": map[string]any{"type": "object"}}}}})
					return true
				}
				return false
			})
			var reports []mcp.ConnectionResult
			ts, close, warnings := mcp.ConnectWithOptions(context.Background(), map[string]mcp.Server{"z": {URL: fixture.server.URL}, "a": {Type: "unsupported"}}, mcp.Options{Observe: func(r mcp.ConnectionResult) { reports = append(reports, r) }})
			defer close()
			if len(reports) != 2 || reports[0].Name != "a" || reports[0].Status != "failed" || reports[1].Name != "z" {
				t.Fatalf("reports: %+v", reports)
			}
			r := reports[1]
			if kind == "failure" {
				if r.Status != "failed" || len(ts) != 0 {
					t.Fatalf("failed: %+v %d", r, len(ts))
				}
			} else if r.Status != "connected" {
				t.Fatalf("not connected: %+v", r)
			}
			if kind == "tools" && !reflect.DeepEqual(r.ToolNames, []string{"getJiraIssue", "slack.search", "slack/search"}) {
				t.Fatalf("raw names lost: %+v", r)
			}
			if kind == "tools" && (len(ts) != 3 || ts[1].Name == ts[2].Name) {
				t.Fatal("colliding raw names lost unique registered aliases", ts)
			}
			if kind == "resources" && (!r.Resources || r.Prompts || len(r.ToolNames) != 0 || len(ts) != 3) {
				t.Fatalf("resource-only report: %+v tools=%d", r, len(ts))
			}
			if kind == "empty" && (len(ts) != 0 || len(r.ToolNames) != 0) {
				t.Fatal(r)
			}
			encoded, _ := json.Marshal(reports)
			if strings.Contains(string(encoded), "secret-remote-error") || strings.Contains(string(encoded), fixture.server.URL) {
				t.Fatal("unsafe report", string(encoded))
			}
			for _, warning := range warnings {
				if strings.Contains(warning, "secret-remote-error") {
					t.Fatal(warning)
				}
			}
			want := []string{"initialize", "notifications/initialized"}
			if kind == "tools" {
				want = append(want, "tools/list")
			}
			if kind == "failure" {
				want = []string{"initialize"}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(methods, want) {
				t.Fatalf("unexpected startup traffic: %v", methods)
			}
		})
	}
}
