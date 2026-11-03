package test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/session"
)

func assertCompactToolSchemas(t *testing.T, request projectCLIRequest) {
	t.Helper()
	names := map[string]bool{}
	for _, tool := range request.Tools {
		if tool["type"] == "web_search" {
			continue
		}
		name, _ := tool["name"].(string)
		names[name] = true
	}
	if len(names) != 2 || !names["tool_registry"] || !names["tool_invoke"] {
		t.Fatalf("provider must expose fixed registry facade only: %v", names)
	}
	encoded, err := json.Marshal(request.Tools)
	if err != nil || len(encoded) > 4000 {
		t.Fatalf("compact tool JSON bytes=%d: %v", len(encoded), err)
	}
}

func TestCompactBootstrapFreshResumeAndToollessCompaction(t *testing.T) {
	binary, requests := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
		switch step {
		case 0: // Pre-facade call names stay dispatchable for saved conversations.
			return &projectCLICall{Name: "bash", Arguments: `{"command":"printf HISTORICAL-TOOL"}`}
		case 2:
			return &projectCLICall{Name: "tool_registry", Arguments: `{"action":"describe","name":"bash"}`}
		case 3:
			return &projectCLICall{Name: "tool_invoke", Arguments: `{"name":"bash","arguments":{"command":"printf WRAPPED-TOOL"}}`}
		}
		return nil
	})
	run := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %s: %v", args, out, err)
		}
		return string(out)
	}
	out := run("", "-p", "fresh compact bootstrap")
	if !strings.Contains(out, "2 tool wrappers") || !strings.Contains(out, "capabilities on demand") {
		t.Fatal(out)
	}
	first, afterDirect := <-requests, <-requests
	assertCompactToolSchemas(t, first)
	encoded, _ := json.Marshal(first)
	toolJSON, _ := json.Marshal(first.Tools)
	t.Logf("fake provider fresh request projection: instructions=%d bytes tools=%d bytes request=%d bytes (not tokenizer counts)", len(first.Instructions), len(toolJSON), len(encoded))
	if len(first.Instructions)+len(toolJSON) > 20000 {
		t.Fatalf("clean fixture bootstrap exceeds bytes/4 ~5000 target: %d bytes", len(first.Instructions)+len(toolJSON))
	}
	if !strings.Contains(first.Instructions, "tool_registry") || !strings.Contains(first.Instructions, "tool_invoke") {
		t.Fatal("missing discovery instructions")
	}
	id, err := session.Latest()
	if err != nil {
		t.Fatal(err)
	}
	run("", "-s", id, "-p", "resume full history")
	resumed, described, invoked := <-requests, <-requests, <-requests
	assertCompactToolSchemas(t, resumed)
	before, _ := json.Marshal(afterDirect.Input)
	after, _ := json.Marshal(resumed.Input)
	if !strings.Contains(string(after), "HISTORICAL-TOOL") || !strings.Contains(string(after), "fresh compact bootstrap") || len(after) <= len(before) {
		t.Fatalf("resume lost historical call/results: %s", after)
	}
	description, _ := json.Marshal(described.Input)
	if !strings.Contains(string(description), "Run any shell command") {
		t.Fatalf("describe did not return original bash contract: %s", description)
	}
	invocation, _ := json.Marshal(invoked.Input)
	if !strings.Contains(string(invocation), "WRAPPED-TOOL") || !strings.Contains(string(invocation), "function_call_output") {
		t.Fatalf("wrapped invocation failed: %s", invocation)
	}
	out = run("/project diagnostics\n/compact\n", "-s", id)
	if !strings.Contains(out, "bytes, not provider tokens") || !strings.Contains(out, "provider-owned saved context") || !strings.Contains(out, "context compacted and session saved") {
		t.Fatal(out)
	}
	compaction := <-requests
	if len(compaction.Tools) != 0 || compaction.ToolChoice != "none" {
		t.Fatalf("compaction re-exposed operational tools: %+v", compaction)
	}
}

// TestCompactBootstrapHostedWebActivation exercises real CLI registration and
// provider requests, without performing a hosted search or any external fetch.
func TestCompactBootstrapHostedWebActivation(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			binary, requests := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
				switch step {
				case 0:
					return &projectCLICall{Name: "tool_registry", Arguments: `{"action":"search","query":"enable_web_search"}`}
				case 1:
					return &projectCLICall{Name: "tool_registry", Arguments: `{"action":"describe","name":"enable_web_search"}`}
				case 2:
					return &projectCLICall{Name: "tool_invoke", Arguments: `{"name":"enable_web_search","arguments":{}}`}
				case 3:
					if !enabled {
						// Historical direct names must not bypass disabled config either.
						return &projectCLICall{Name: "enable_web_search", Arguments: `{}`}
					}
					return &projectCLICall{Name: "tool_invoke", Arguments: `{"name":"enable_web_search","arguments":{}}`}
				}
				return nil
			})
			web := "false"
			if enabled {
				web = "true"
			}
			projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), `{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":`+web+`,"map_cap":0}`)
			run := func(args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Stdin = strings.NewReader("")
				cmd.Env = os.Environ()
				if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "PROJECT-CLI-DONE") {
					t.Fatalf("CLI %v: %s: %v", args, out, err)
				}
			}
			read := func(hosted bool) projectCLIRequest {
				t.Helper()
				select {
				case request := <-requests:
					assertCompactToolSchemas(t, request)
					want := 2
					if hosted {
						want++
					}
					hostedCount := 0
					for _, tool := range request.Tools {
						if tool["type"] == "web_search" {
							hostedCount++
							if len(tool) != 1 {
								t.Fatalf("hosted schema must remain native: %v", tool)
							}
						} else if tool["type"] != "function" {
							t.Fatalf("unexpected tool schema: %v", tool)
						}
					}
					if len(request.Tools) != want || hostedCount != want-2 || request.ToolChoice != "auto" {
						t.Fatalf("hosted=%t: tools=%v choice=%q", hosted, request.Tools, request.ToolChoice)
					}
					if request.PromptCacheKey == "" {
						t.Fatal("missing wire prompt_cache_key")
					}
					return request
				default:
					t.Fatal("missing provider request")
					return projectCLIRequest{}
				}
			}
			lastOutput := func(request projectCLIRequest) string {
				t.Helper()
				for i := len(request.Input) - 1; i >= 0; i-- {
					if request.Input[i]["type"] == "function_call_output" {
						output, _ := request.Input[i]["output"].(string)
						return output
					}
				}
				t.Fatal("missing function result")
				return ""
			}
			snapshot := func(request projectCLIRequest) string {
				encoded, err := json.Marshal([]any{request.Instructions, request.Tools})
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			}
			run("-p", "check lazy hosted web activation")
			fresh, searched, described := read(false), read(false), read(false)
			activated, repeated := read(enabled), read(enabled)
			if snapshot(fresh) != snapshot(searched) || snapshot(fresh) != snapshot(described) ||
				fresh.PromptCacheKey != searched.PromptCacheKey || fresh.PromptCacheKey != described.PromptCacheKey {
				t.Fatal("discovery changed effective snapshot or cache key")
			}
			if snapshot(activated) != snapshot(repeated) || activated.PromptCacheKey != repeated.PromptCacheKey {
				t.Fatal("repeated activation changed effective snapshot or cache key")
			}
			if enabled {
				if !strings.Contains(lastOutput(searched), `"name":"enable_web_search"`) ||
					!strings.Contains(lastOutput(described), "next model request") ||
					!strings.Contains(lastOutput(activated), "No search or fetch has occurred") ||
					!strings.Contains(lastOutput(repeated), "No search or fetch has occurred") {
					t.Fatalf("activation discovery/invocation failed: %s / %s / %s / %s", lastOutput(searched), lastOutput(described), lastOutput(activated), lastOutput(repeated))
				}
				if fresh.PromptCacheKey == activated.PromptCacheKey {
					t.Fatal("activation did not change effective snapshot cache key")
				}
			} else {
				if strings.Contains(lastOutput(searched), `"name":"enable_web_search"`) ||
					!strings.Contains(lastOutput(described), "unknown registered tool") ||
					!strings.Contains(lastOutput(activated), "unknown registered tool") ||
					!strings.Contains(lastOutput(repeated), "unknown tool") {
					t.Fatalf("disabled activation exposed or accepted: %s / %s / %s / %s", lastOutput(searched), lastOutput(described), lastOutput(activated), lastOutput(repeated))
				}
				if fresh.PromptCacheKey != activated.PromptCacheKey {
					t.Fatal("disabled activation changed effective snapshot cache key")
				}
			}
			id, err := session.Latest()
			if err != nil {
				t.Fatal(err)
			}
			run("-s", id, "-p", "resume activation-only history")
			resumed := read(false)
			history, _ := json.Marshal(resumed.Input)
			if !strings.Contains(string(history), "check lazy hosted web activation") || strings.Contains(string(history), `"type":"web_search_call"`) {
				t.Fatalf("unexpected activation-only resumed history: %s", history)
			}
			if enabled && !strings.Contains(string(history), "No search or fetch has occurred") {
				t.Fatal("resume lost successful activation history")
			}
		})
	}
}

func TestCompactBootstrapMandatoryFloorDiagnostic(t *testing.T) {
	binary, _ := newEcosystemProviderRequests(t, func(int) *projectCLICall { return nil })
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), `{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0,"memory_max_tokens":0}`)
	mandatory := "MANDATORY-GUIDANCE-" + strings.Repeat("binding ", 3000)
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), "AGENTS.md"), mandatory)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = strings.NewReader("/project diagnostics\n")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "mandatory instruction/map floor exceeds") || !strings.Contains(string(out), "preserved without dropping guidance") {
		t.Fatalf("floor diagnostic: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "Configured automatic memory budget: 0 bytes") || !strings.Contains(string(out), "legacy fallback: 16000 bytes; it does not disable recall") {
		t.Fatalf("explicit zero fallback diagnostic: %s", out)
	}
	// Old provider-owned maps may survive a resume even with map_cap=0 now.
	legacy, _ := json.Marshal(map[string]any{"session": "legacy-map", "map": strings.Repeat("saved-map ", 3000), "input": []any{}})
	if err := session.Save("legacy-map", legacy); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, binary, "-s", "legacy-map")
	cmd.Stdin = strings.NewReader("/project diagnostics\n")
	cmd.Env = os.Environ()
	out, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "repo_map=unavailable (provider-owned saved context)") || !strings.Contains(string(out), "its bytes and total floor are unavailable here, not zero") {
		t.Fatalf("saved map diagnostic misstates restored context: %s: %v", out, err)
	}
}
