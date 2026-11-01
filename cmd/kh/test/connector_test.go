package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectorCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"connected", "mixed", "failed", "declined", "resource-only", "zero-tools", "empty"} {
		t.Run(kind, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			projectCLIWrite(t, filepath.Join(home, ".kh", "config.json"), fmt.Sprintf(`{"auto":%t,"memory_model":"off","web_search":false,"map_cap":0}`, kind != "declined"))
			servers := map[string]any{}
			markers := map[string]string{}
			if kind == "connected" || kind == "mixed" || kind == "declined" {
				for _, name := range []string{"z-tools", "demo:api", "custom"} {
					marker := filepath.Join(cwd, name+"-traffic")
					markers[name] = marker
					env := map[string]string{"KH_PROJECT_MCP_CHILD": "1", "KH_PROJECT_MCP_MARKER": marker}
					if name == "demo:api" {
						env["KH_PROJECT_MCP_RESOURCE_ONLY"] = "true"
					}
					if name == "z-tools" {
						env["KH_PROJECT_MCP_TOOL_NAMES"] = "getJiraIssue,getConfluencePage,slack.search,getLoomObjects,notjirathing"
					}
					if name == "custom" {
						env["KH_PROJECT_MCP_TOOL_NAMES"] = "discover,execute"
					}
					servers[name] = map[string]any{"command": exe, "args": []string{"-test.run=^TestProjectConfigMCPProcess$"}, "env": env}
				}
			}
			if kind == "resource-only" || kind == "zero-tools" {
				marker := filepath.Join(cwd, "only-traffic")
				markers["only"] = marker
				env := map[string]string{"KH_PROJECT_MCP_CHILD": "1", "KH_PROJECT_MCP_MARKER": marker}
				if kind == "resource-only" {
					env["KH_PROJECT_MCP_RESOURCE_ONLY"] = "true"
				} else {
					env["KH_PROJECT_MCP_ZERO_TOOLS"] = "true"
				}
				servers["only"] = map[string]any{"command": exe, "args": []string{"-test.run=^TestProjectConfigMCPProcess$"}, "env": env}
			}
			if kind == "mixed" || kind == "failed" {
				servers["a-failed"] = map[string]any{"command": "/missing/connector-secret-command", "args": []string{"secret-args"}, "env": map[string]string{"TOKEN": "secret-env"}}
				servers["https://secret-server-label/?token=private"] = map[string]any{"type": "unsupported", "url": "https://secret-url.invalid", "headers": map[string]string{"Authorization": "secret-header"}}
			}
			data, _ := json.Marshal(map[string]any{"mcpServers": servers})
			projectCLIWrite(t, filepath.Join(cwd, ".agents", "settings.json"), string(data))
			projectCLIWrite(t, filepath.Join(cwd, ".agents", "commands", "connector.md"), "SHADOW-CONNECTOR")
			var requests atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
			defer proxy.Close()
			input := "/connector\n/connector\n/connector unexpected\n/help\n"
			if kind == "declined" {
				input = "n\nn\nn\n" + input
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=", "HTTPS_PROXY="+proxy.URL, "HTTP_PROXY="+proxy.URL, "ALL_PROXY=", "NO_PROXY=")
			cmd.Stdin = strings.NewReader(input)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("CLI: %s %v", out, err)
			}
			text := string(out)
			start := strings.Index(text, "MCP connectors:")
			if start < 0 {
				t.Fatalf("missing summary: %s", out)
			}
			summary := text[start:]
			startup := text[:start]
			wantStartup := map[string]string{
				"connected": "(MCP: 3 connected)", "mixed": "(MCP: 3 connected, 2 failed)",
				"failed": "(MCP: 0 connected, 2 failed)", "resource-only": "(MCP: 1 connected)", "zero-tools": "(MCP: 1 connected)",
			}[kind]
			if wantStartup != "" {
				if strings.Count(startup, "(MCP:") != 1 || !strings.Contains(startup, wantStartup) {
					t.Fatalf("missing or redundant startup summary %q: %s", wantStartup, startup)
				}
			} else if strings.Contains(startup, "(MCP:") {
				t.Fatalf("unexpected auto startup summary: %s", startup)
			}
			if kind == "declined" {
				if strings.Count(startup, "Trust MCP server") != 3 || strings.Count(startup, "run it? [y/N]") != 3 {
					t.Fatalf("manual trust prompts missing: %s", startup)
				}
			} else if strings.Contains(startup, "Trust MCP server") || strings.Contains(startup, "loaded ") && strings.Contains(startup, " MCP tools") {
				t.Fatalf("auto startup clutter: %s", startup)
			}
			if requests.Load() != 0 {
				t.Fatalf("command made %d model/network requests", requests.Load())
			}
			for _, forbidden := range []string{"secret-command", "secret-args", "secret-env", "secret-header", "secret-url", "secret-server-label", "SHADOW-CONNECTOR"} {
				if strings.Contains(summary, forbidden) {
					t.Fatalf("unsafe output %s: %s", forbidden, summary)
				}
			}
			for _, want := range []string{"session startup snapshot", "not live health", "not verified product authentication or permissions", "usage: /connector", "/connector   list session MCP"} {
				if !strings.Contains(summary, want) {
					t.Errorf("missing %q: %s", want, summary)
				}
			}
			if strings.Count(summary, "MCP connectors:") != 2 {
				t.Fatalf("snapshot repeats: %s", summary)
			}
			blocks := strings.Split(summary, "MCP connectors:")
			end := "No live checks performed; restart to refresh startup discovery."
			first, _, _ := strings.Cut(blocks[1], end)
			second, _, _ := strings.Cut(blocks[2], end)
			if first != second {
				t.Fatal("startup snapshot changed between commands", summary)
			}
			switch kind {
			case "empty":
				if !strings.Contains(summary, "No MCP servers configured") {
					t.Fatal(summary)
				}
			case "declined":
				if strings.Count(summary, "not connected (startup trust not granted or interrupted)") != 6 {
					t.Fatal(summary)
				}
			case "resource-only", "zero-tools":
				want := "only: connected; 0 advertised tools"
				if kind == "resource-only" {
					want += "; resources available"
				}
				if !strings.Contains(summary, want) {
					t.Fatal(summary)
				}
			case "connected":
				if strings.Count(summary, ": connected;") != 6 || strings.Contains(summary, ": failed (") {
					t.Fatal(summary)
				}
			case "mixed":
				for _, want := range []string{"demo:api: connected; 0 advertised tools; resources available", "custom: connected; 2 advertised tools\n", "z-tools: connected; 5 advertised tools; exposed service hints: Confluence, Jira, Loom, Slack", "a-failed: failed", "[redacted server name]: failed"} {
					if !strings.Contains(summary, want) {
						t.Errorf("missing %q: %s", want, summary)
					}
				}
				if strings.Index(summary, "custom: connected") > strings.Index(summary, "demo:api: connected") || strings.Index(summary, "z-tools: connected") > strings.Index(summary, "a-failed: failed") {
					t.Fatal("wrong status/name sorting", summary)
				}
			case "failed":
				if strings.Contains(summary, ": connected;") || strings.Count(summary, ": failed (") != 4 {
					t.Fatal(summary)
				}
			}
			for name, marker := range markers {
				log, err := os.ReadFile(marker)
				if kind == "declined" {
					if !os.IsNotExist(err) {
						t.Fatal("declined server launched", name, err)
					}
					continue
				}
				want := "started\ninitialize\nnotifications/initialized\n"
				if name != "demo:api" && kind != "resource-only" {
					want += "tools/list\n"
				}
				if err != nil || string(log) != want {
					t.Fatalf("extra MCP requests for %s: %q %v", name, log, err)
				}
			}
		})
	}
}
