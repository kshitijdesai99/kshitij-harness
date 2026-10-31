package test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestProjectConfigMCPProcess is an actual stdio child launched by CLI startup.
func TestProjectConfigMCPProcess(t *testing.T) {
	if os.Getenv("KH_PROJECT_MCP_CHILD") != "1" {
		return
	}
	marker := os.Getenv("KH_PROJECT_MCP_MARKER")
	if err := os.WriteFile(marker, []byte("started\n"), 0600); err != nil {
		os.Exit(2)
	}
	scanner := bufio.NewScanner(os.Stdin)
	initialized := false
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		log, err := os.OpenFile(marker, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(4)
		}
		fmt.Fprintln(log, request.Method)
		log.Close()
		var result any
		switch request.Method {
		case "initialize":
			capabilities := map[string]any{"tools": map[string]any{}}
			if os.Getenv("KH_PROJECT_MCP_CONTENT") == "true" {
				capabilities["resources"] = map[string]any{}
				capabilities["prompts"] = map[string]any{}
			}
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": capabilities, "serverInfo": map[string]any{"name": "cli-fixture", "version": "1"}}
		case "notifications/initialized":
			initialized = true
			continue
		case "tools/list":
			if !initialized {
				os.Exit(5)
			}
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "CLI fixture echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "enum": []string{"roundtrip"}}}, "required": []string{"value"}, "additionalProperties": false}}}}
		case "tools/call":
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Value string `json:"value"`
				} `json:"arguments"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Name != "echo" || params.Arguments.Value != "roundtrip" {
				os.Exit(6)
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "MCP-CLI-roundtrip"}}}
		case "resources/list":
			result = map[string]any{"resources": []any{map[string]any{"uri": "fixture://guide", "name": "fixture-guide", "description": "CLI resource metadata"}}}
		case "resources/templates/list":
			result = map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "fixture://{name}", "name": "fixture-template"}}}
		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.URI != "fixture://guide" {
				os.Exit(8)
			}
			result = map[string]any{"contents": []any{map[string]any{"uri": params.URI, "mimeType": "text/plain", "text": "MCP-CLI-RESOURCE-BODY"}}}
		case "prompts/list":
			result = map[string]any{"prompts": []any{map[string]any{"name": "greet", "description": "CLI prompt metadata", "arguments": []any{map[string]any{"name": "who", "required": true}}}}}
		case "prompts/get":
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Name != "greet" || params.Arguments["who"] != "reader" {
				os.Exit(9)
			}
			result = map[string]any{"messages": []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "MCP-CLI-PROMPT-reader"}}}}
		default:
			continue
		}
		if json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
			os.Exit(7)
		}
	}
	os.Exit(0)
}

type projectCLIRequest struct {
	Instructions string           `json:"instructions"`
	Input        []map[string]any `json:"input"`
	Tools        []map[string]any `json:"tools"`
}

// projectCLIProxy terminates only the fixture's chatgpt.com HTTPS requests.
// A private CA and synthetic tokens keep these tests offline and credential-free.
func projectCLIProxy(t *testing.T, home string, invokeTool bool) (string, <-chan projectCLIRequest) {
	t.Helper()
	return projectCLIProxyScript(t, home, func(step int) *projectCLICall {
		if invokeTool && step == 0 {
			return &projectCLICall{Name: "mcp__fixture__echo", Arguments: `{"value":"roundtrip"}`}
		}
		return nil
	})
}

// projectCLICall drives an actual CLI tool loop using synthetic provider replies.
type projectCLICall struct {
	Name, Arguments string
}

func projectCLIProxyScript(t *testing.T, home string, call func(int) *projectCLICall) (string, <-chan projectCLIRequest) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		var major, minor int
		if _, err := fmt.Sscanf(runtime.Version(), "go%d.%d", &major, &minor); err != nil || major < 1 || major == 1 && minor < 27 {
			t.Skip("provider HTTPS fixture needs Go 1.27 SSL_CERT_FILE override on Darwin; no system trust changes")
		}
	}
	requests := make(chan projectCLIRequest, 32)
	step := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[]}`)
			return
		}
		var body projectCLIRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if next := call(step); next != nil {
			event, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "call_id": fmt.Sprintf("cli-call-%d", step), "name": next.Name, "arguments": next.Arguments}})
			fmt.Fprintf(w, "data: %s\n", event)
		} else {
			fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"PROJECT-CLI-DONE"}`)
			fmt.Fprintln(w, `data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"PROJECT-CLI-DONE"}]}}`)
		}
		step++
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{}}`)
	}))
	// Reuse a disposable test key, but issue a certificate for the real hostname.
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	key := seed.TLS.Certificates[0].PrivateKey
	seed.Close()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"chatgpt.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, seed.Certificate().PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	projectCLIWrite(t, filepath.Join(home, "fixture-ca.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "chatgpt.com:443" {
			http.Error(w, "fixture blocks external requests", 403)
			return
		}
		upstream, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture unavailable", 502)
			return
		}
		defer upstream.Close()
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go io.Copy(upstream, client)
		io.Copy(client, upstream)
	}))
	t.Cleanup(proxy.Close)
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"https://api.openai.com/auth":{"chatgpt_account_id":"fixture"}}`, time.Now().Add(time.Hour).Unix())))
	projectCLIWrite(t, filepath.Join(home, ".kh", "codex.json"), `{"access_token":"fixture.`+claims+`.signature","refresh_token":"fixture-not-a-secret"}`)
	return proxy.URL, requests
}

func projectCLIWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectConfigCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	fixture := func(t *testing.T, auto bool) (string, string) {
		t.Helper()
		home, cwd := t.TempDir(), filepath.Join(t.TempDir(), "nested")
		if err := os.MkdirAll(cwd, 0700); err != nil {
			t.Fatal(err)
		}
		// macOS temporary paths may be aliases under /var rather than /private/var.
		cwd, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			t.Fatal(err)
		}
		projectCLIWrite(t, filepath.Join(home, ".kh", "config.json"), fmt.Sprintf(`{"auto":%t,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`, auto))
		return home, cwd
	}
	run := func(t *testing.T, home, cwd, proxy, input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=", "HTTPS_PROXY="+proxy, "HTTP_PROXY="+proxy, "ALL_PROXY=", "NO_PROXY=", "SSL_CERT_FILE="+filepath.Join(home, "fixture-ca.pem"), "SSL_CERT_DIR="+home, "GODEBUG=x509sslcertoverrideplatform=1")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %s %v (context: %v)", args, out, err, ctx.Err())
		}
		return string(out)
	}
	mcpConfig := func(t *testing.T, cwd string, content ...bool) string {
		t.Helper()
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(cwd, "mcp-started")
		data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]any{"command": exe, "args": []string{"-test.run=^TestProjectConfigMCPProcess$"}, "env": map[string]string{"KH_PROJECT_MCP_CHILD": "1", "KH_PROJECT_MCP_MARKER": marker, "KH_PROJECT_MCP_CONTENT": fmt.Sprint(len(content) > 0 && content[0])}}}})
		if err != nil {
			t.Fatal(err)
		}
		projectCLIWrite(t, filepath.Join(cwd, ".agent", "settings.json"), string(data))
		return marker
	}
	rootFixtures := func(t *testing.T, cwd string) {
		t.Helper()
		for _, dir := range []string{".agents", ".claude"} {
			// Copy the actual repository fixtures, not a second synthetic definition.
			if err := os.CopyFS(filepath.Join(cwd, dir), os.DirFS(filepath.Join("../../..", dir))); err != nil {
				t.Fatalf("copy root fixture %s: %v", dir, err)
			}
		}
	}
	toolOutput := func(t *testing.T, body projectCLIRequest, step int) string {
		t.Helper()
		for _, item := range body.Input {
			if item["type"] == "function_call_output" && item["call_id"] == fmt.Sprintf("cli-call-%d", step) {
				output, _ := item["output"].(string)
				return output
			}
		}
		t.Fatalf("step %d has no tool output: %v", step, body.Input)
		return ""
	}
	t.Run("root fixtures lazy discovery and explicit loading", func(t *testing.T) {
		home, cwd := fixture(t, true)
		rootFixtures(t, cwd)
		calls := []projectCLICall{
			{Name: "project_capability", Arguments: `{"action":"search","query":"fixture"}`},
			{Name: "project_capability", Arguments: `{"action":"load","name":"fixture-guide"}`},
			{Name: "project_capability", Arguments: fmt.Sprintf(`{"action":"read","path":%q}`, filepath.Join(cwd, ".agents", "scripts", "fixture-demo.sh"))},
			{Name: "project_capability", Arguments: `{"action":"load","name":"fixture-demo:hello"}`},
			{Name: "project_capability", Arguments: `{"action":"load","name":"fixture-demo:check"}`},
			{Name: "project_capability", Arguments: `{"action":"load","name":"fixture-fallback"}`},
		}
		proxy, requests := projectCLIProxyScript(t, home, func(step int) *projectCLICall {
			if step < len(calls) {
				return &calls[step]
			}
			return nil
		})
		out := run(t, home, cwd, proxy, "", "-p", "Explain the fixture capabilities on request")
		if !strings.Contains(out, "PROJECT-CLI-DONE") || len(requests) != len(calls)+1 {
			t.Fatalf("tool loop incomplete (%d requests): %s", len(requests), out)
		}
		first := <-requests
		encoded, _ := json.Marshal(first)
		for _, marker := range []string{"LAZY-FIXTURE-SKILL-BODY", "CLAUDE-FIXTURE-SKILL-BODY", "PLUGIN-FIXTURE-SKILL-BODY", "ROOT-FIXTURE-SCRIPT-BODY", "PLUGIN-FIXTURE-SCRIPT-BODY", "AGENTS-FIXTURE-COMMAND", "CLAUDE-FIXTURE-FALLBACK", "PLUGIN-FIXTURE-COMMAND"} {
			if strings.Contains(string(encoded), marker) {
				t.Errorf("startup eagerly includes %s", marker)
			}
		}
		registered := false
		for _, tool := range first.Tools {
			registered = registered || tool["name"] == "project_capability"
		}
		if !registered {
			t.Error("project_capability absent from actual provider tool schema")
		}
		for step, markers := range [][]string{
			{"fixture-guide", "Explain the small inert", "fixture-demo:hello", "Explain the fixture-demo plugin greeting", "fixture-demo:check", "Describe the local fixture plugin", "fixture-fallback"},
			{"LAZY-FIXTURE-SKILL-BODY"},
			{"ROOT-FIXTURE-SCRIPT-BODY"},
			{"PLUGIN-FIXTURE-SKILL-BODY"},
			{"PLUGIN-FIXTURE-COMMAND"},
			{"CLAUDE-FIXTURE-SKILL-BODY"},
		} {
			output := toolOutput(t, <-requests, step)
			for _, marker := range markers {
				if !strings.Contains(output, marker) {
					t.Errorf("step %d missing %q: %s", step, marker, output)
				}
			}
			if step == 0 && strings.Contains(output, "LAZY-FIXTURE-SKILL-BODY") {
				t.Error("metadata search returned skill body")
			}
		}
	})
	t.Run("root fixtures command alias uses agents precedence", func(t *testing.T) {
		home, cwd := fixture(t, true)
		rootFixtures(t, cwd)
		proxy, requests := projectCLIProxy(t, home, false)
		out := run(t, home, cwd, proxy, "", "-p", "/fixture-check", "alpha", "beta")
		for _, marker := range []string{"ROOT-FIXTURE-SCRIPT-BODY", "PLUGIN-FIXTURE-SCRIPT-BODY"} {
			if strings.Contains(out, marker) {
				t.Errorf("discovery executed inert script: %s", out)
			}
		}
		if len(requests) != 1 {
			t.Fatalf("alias provider requests=%d, want 1", len(requests))
		}
		body := <-requests
		encoded, _ := json.Marshal(body.Input)
		if !strings.Contains(string(encoded), "AGENTS-FIXTURE-COMMAND alpha beta first=alpha") || strings.Contains(string(encoded), "CLAUDE-FIXTURE-FALLBACK") {
			t.Fatalf("wrong alias expansion: %s", encoded)
		}
	})
	t.Run("root fixtures plugin command expands local plugin directory", func(t *testing.T) {
		home, cwd := fixture(t, true)
		rootFixtures(t, cwd)
		proxy, requests := projectCLIProxy(t, home, false)
		run(t, home, cwd, proxy, "", "-p", "/fixture-demo:check", "alpha")
		if len(requests) != 1 {
			t.Fatalf("plugin alias provider requests=%d, want 1", len(requests))
		}
		body := <-requests
		encoded, _ := json.Marshal(body.Input)
		want := "PLUGIN-FIXTURE-COMMAND alpha root=" + filepath.Join(cwd, ".agents", "plugins", "fixture-demo")
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("plugin expansion missing %q: %s", want, encoded)
		}
	})
	t.Run("MCP resource and prompt content roundtrip", func(t *testing.T) {
		home, cwd := fixture(t, true)
		marker := mcpConfig(t, cwd, true)
		calls := []projectCLICall{
			{Name: "mcp__fixture__resources_list", Arguments: `{}`},
			{Name: "mcp__fixture__resources_templates_list", Arguments: `{}`},
			{Name: "mcp__fixture__resources_read", Arguments: `{"uri":"fixture://guide"}`},
			{Name: "mcp__fixture__prompts_list", Arguments: `{}`},
			{Name: "mcp__fixture__prompts_get", Arguments: `{"name":"greet","arguments":{"who":"reader"}}`},
		}
		proxy, requests := projectCLIProxyScript(t, home, func(step int) *projectCLICall {
			if step < len(calls) {
				return &calls[step]
			}
			return nil
		})
		out := run(t, home, cwd, proxy, "", "-p", "Inspect MCP resources and prompts")
		if !strings.Contains(out, "PROJECT-CLI-DONE") || len(requests) != len(calls)+1 {
			t.Fatalf("content tool loop incomplete (%d requests): %s", len(requests), out)
		}
		first := <-requests
		registered := map[string]bool{}
		for _, tool := range first.Tools {
			name, _ := tool["name"].(string)
			registered[name] = true
		}
		for step, want := range []string{"CLI resource metadata", "fixture://{name}", "MCP-CLI-RESOURCE-BODY", "CLI prompt metadata", "MCP-CLI-PROMPT-reader"} {
			if !registered[calls[step].Name] {
				t.Errorf("content tool missing from provider schema: %s", calls[step].Name)
			}
			if output := toolOutput(t, <-requests, step); !strings.Contains(output, want) {
				t.Errorf("content step %d missing %q: %s", step, want, output)
			}
		}
		log, err := os.ReadFile(marker)
		want := "started\ninitialize\nnotifications/initialized\ntools/list\nresources/list\nresources/templates/list\nresources/read\nprompts/list\nprompts/get\n"
		if err != nil || string(log) != want {
			t.Fatalf("content protocol: %q %v", log, err)
		}
	})
	t.Run("startup summary and opt-in diagnostics", func(t *testing.T) {
		home, cwd := fixture(t, false)
		projectCLIWrite(t, filepath.Join(cwd, ".agent", "settings.json"), `{"hooks":{},"theme":"dark"}`)
		projectCLIWrite(t, filepath.Join(cwd, ".agent", "commands", "project.md"), "SHADOW-DIAGNOSTICS")
		out := run(t, home, cwd, "", "/help\n")
		if strings.Count(out, "(project config)") != 1 || !strings.Contains(out, "2 diagnostics (/project diagnostics)") || strings.Contains(out, "unsupported setting") {
			t.Fatalf("noisy or missing startup summary: %s", out)
		}
		out = run(t, home, cwd, "", "/project diagnostics\n")
		for _, want := range []string{"unsupported setting hooks", "unsupported setting theme"} {
			if !strings.Contains(out, want) {
				t.Errorf("diagnostics missing %q: %s", want, out)
			}
		}
		if strings.Contains(out, "SHADOW-DIAGNOSTICS") {
			t.Fatal("diagnostics invoked project command")
		}
	})
	t.Run("help lists commands and skills without invocation", func(t *testing.T) {
		home, cwd := fixture(t, false)
		projectCLIWrite(t, filepath.Join(cwd, ".agent", "commands", "inspect.md"), "INSPECT-BODY")
		projectCLIWrite(t, filepath.Join(cwd, ".claude", "skills", "explain", "SKILL.md"), "EXPLAIN-BODY")
		projectCLIWrite(t, filepath.Join(cwd, ".agent", "commands", "help.md"), "SHADOW-NATIVE-HELP")
		out := run(t, home, cwd, "", "/help\n")
		if !strings.Contains(out, "Project commands/skills: /explain, /inspect") || !strings.Contains(out, "/compact") || strings.Contains(out, "SHADOW-NATIVE-HELP") || strings.Contains(out, "not logged in") {
			t.Fatalf("help: %s", out)
		}
	})
	for _, kind := range []string{"command", "skill"} {
		t.Run("headless expands "+kind+" in provider request", func(t *testing.T) {
			home, cwd := fixture(t, true)
			projectCLIWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "HOME-CONTEXT")
			projectCLIWrite(t, filepath.Join(filepath.Dir(cwd), ".agent", "rules", "parent.md"), "ANCESTOR-CONTEXT")
			projectCLIWrite(t, filepath.Join(cwd, ".claude", "rules", "local.md"), "LOCAL-CONTEXT")
			path := filepath.Join(cwd, ".agent", "commands", "inspect.md")
			if kind == "skill" {
				path = filepath.Join(cwd, ".claude", "skills", "inspect", "SKILL.md")
			}
			projectCLIWrite(t, path, "REFERENCE $ARGUMENTS first=$1 dir=${CLAUDE_SKILL_DIR}")
			proxy, requests := projectCLIProxy(t, home, false)
			out := run(t, home, cwd, proxy, "", "-p", "/inspect", "alpha", "beta")
			if !strings.Contains(out, "PROJECT-CLI-DONE") || strings.Contains(out, "kh chat, session") {
				t.Fatalf("headless: %s", out)
			}
			select {
			case body := <-requests:
				for _, marker := range []string{"HOME-CONTEXT", "ANCESTOR-CONTEXT", "LOCAL-CONTEXT"} {
					if !strings.Contains(body.Instructions, marker) {
						t.Errorf("provider instructions missing %q", marker)
					}
				}
				encoded, _ := json.Marshal(body.Input)
				for _, text := range []string{"/inspect alpha beta", "Project command reference (not authorization", "REFERENCE alpha beta first=alpha dir=" + filepath.Dir(path)} {
					if !strings.Contains(string(encoded), text) {
						t.Errorf("provider input missing %q: %s", text, encoded)
					}
				}
			default:
				t.Fatal("provider received no request")
			}
		})
	}
	t.Run("MCP startup denial never launches child", func(t *testing.T) {
		home, cwd := fixture(t, false)
		marker := mcpConfig(t, cwd)
		out := run(t, home, cwd, "", "n\n/help\n")
		if !strings.Contains(out, `MCP server "fixture" not connected`) || !strings.Contains(out, "run it? [y/N]") || !strings.Contains(out, "/compact") {
			t.Fatalf("denial: %s", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("unapproved MCP child ran: %v", err)
		}
	})
	t.Run("MCP auto startup launches child and discovers tools", func(t *testing.T) {
		home, cwd := fixture(t, true)
		marker := mcpConfig(t, cwd)
		out := run(t, home, cwd, "", "/help\n")
		if !strings.Contains(out, "loaded 1 MCP tools") || strings.Contains(out, "run it? [y/N]") {
			t.Fatalf("auto discovery: %s", out)
		}
		log, err := os.ReadFile(marker)
		if err != nil || string(log) != "started\ninitialize\nnotifications/initialized\ntools/list\n" {
			t.Fatalf("child discovery protocol: %q %v", log, err)
		}
	})
	t.Run("MCP auto startup discovers schema and completes tool roundtrip", func(t *testing.T) {
		home, cwd := fixture(t, true)
		marker := mcpConfig(t, cwd)
		proxy, requests := projectCLIProxy(t, home, true)
		out := run(t, home, cwd, proxy, "", "-p", "exercise MCP")
		if !strings.Contains(out, "loaded 1 MCP tools") || !strings.Contains(out, "PROJECT-CLI-DONE") || strings.Contains(out, "run it? [y/N]") {
			t.Fatalf("auto: %s", out)
		}
		if len(requests) != 2 {
			t.Fatalf("provider requests=%d, want 2", len(requests))
		}
		first, second := <-requests, <-requests
		found := false
		for _, tool := range first.Tools {
			if tool["name"] != "mcp__fixture__echo" {
				continue
			}
			found = true
			params, _ := tool["parameters"].(map[string]any)
			encoded, _ := json.Marshal(params)
			if tool["description"] != "CLI fixture echo" || params["additionalProperties"] != false || !strings.Contains(string(encoded), `"enum":["roundtrip"]`) || !strings.Contains(string(encoded), `"required":["value"]`) {
				t.Errorf("schema lost: %v", tool)
			}
		}
		if !found {
			t.Error("MCP schema missing from provider tools")
		}
		found = false
		for _, item := range second.Input {
			output, _ := item["output"].(string)
			if item["type"] == "function_call_output" && item["call_id"] == "cli-call-0" && strings.HasPrefix(output, "MCP-CLI-roundtrip\n\n[ecosystem snapshot] ") {
				found = true
			}
		}
		if !found {
			t.Errorf("MCP result missing from provider follow-up: %v", second.Input)
		}
		log, err := os.ReadFile(marker)
		if err != nil || string(log) != "started\ninitialize\nnotifications/initialized\ntools/list\ntools/call\n" {
			t.Fatalf("child protocol: %q %v", log, err)
		}
	})
}
