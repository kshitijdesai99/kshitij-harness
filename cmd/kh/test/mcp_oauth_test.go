package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/ecosystem"
)

// Exercise the built CLI, real terminal detection, consent, browser execution,
// and credential reuse without a provider login or a real browser.
func TestMCPOAuthCLI(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for PTY fixture")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	var base string
	var registrations, authorizations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).Encode(map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token", "registration_endpoint": base + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}})
		case "/register":
			registrations.Add(1)
			var registration map[string]any
			json.NewDecoder(r.Body).Decode(&registration)
			registration["client_id"] = "cli-fixture-client"
			json.NewEncoder(w).Encode(registration)
		case "/authorize":
			authorizations.Add(1)
			callback, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil || callback.Hostname() != "127.0.0.1" {
				http.Error(w, "invalid callback", 400)
				return
			}
			q := callback.Query()
			q.Set("code", "cli-fixture-code")
			q.Set("state", r.URL.Query().Get("state"))
			callback.RawQuery = q.Encode()
			http.Redirect(w, r, callback.String(), http.StatusFound)
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "cli-fixture-access-secret", "refresh_token": "cli-fixture-refresh-secret", "token_type": "Bearer", "expires_in": 3600})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer cli-fixture-access-secret" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, base+"/.well-known/oauth-protected-resource"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			json.NewDecoder(r.Body).Decode(&request)
			if len(request.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			var result any = map[string]any{}
			switch request.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "oauth-fixture", "version": "1"}}
			case "tools/list":
				result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "OAuth fixture", "inputSchema": map[string]any{"type": "object"}}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL

	home, cwd, bin := t.TempDir(), t.TempDir(), t.TempDir()
	projectCLIWrite(t, filepath.Join(home, ".kh", "config.json"), `{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`)
	projectCLIWrite(t, filepath.Join(cwd, ".mcp.json"), fmt.Sprintf(`{"mcpServers":{"oauth-fixture":{"url":%q}}}`, base+"/mcp"))
	marker := filepath.Join(home, "browser-opened")
	failMarker := filepath.Join(home, "browser-fail")
	// Both platform names are replaced, so this can never launch a real browser.
	for _, name := range []string{"open", "xdg-open"} {
		path := filepath.Join(bin, name)
		projectCLIWrite(t, path, "#!"+python+"\nimport os,sys,time,urllib.request\nopen("+fmt.Sprintf("%q", marker)+",'w').write('opened')\nif os.path.exists("+fmt.Sprintf("%q", failMarker)+"):\n    print(sys.argv[1]); print(sys.argv[1],file=sys.stderr); sys.exit(1)\nurllib.request.urlopen(sys.argv[1],timeout=5).read()\n# Some platform openers remain alive until the browser closes.\ntime.sleep(30)\n")
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Keep terminal startup's tmux bookkeeping isolated without creating or
	// contacting any real tmux server.
	projectCLIWrite(t, filepath.Join(bin, "tmux"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "tmux"), 0700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"), "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=oauth-fixture", "TMUX_PANE=%oauth-fixture", "TERM=xterm-256color", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=")
	run := func(t *testing.T, terminal, headless bool, answer string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		args := []string{}
		if headless {
			args = []string{"-p", "fixture task"}
		}
		var cmd *exec.Cmd
		if terminal {
			cmd = exec.CommandContext(ctx, python, append([]string{"-c", `import os,pty,select,signal,subprocess,sys,time
binary,answer,*args=sys.argv[1:]
m,s=pty.openpty()
p=subprocess.Popen([binary,*args],stdin=s,stdout=s,stderr=s,start_new_session=True)
os.close(s)
out=bytearray(); answered=False; exited=False
try:
    end=time.monotonic()+10
    while time.monotonic()<end:
        if select.select([m],[],[],.05)[0]:
            try: out.extend(os.read(m,65536))
            except OSError: break
        if not answered and b'run it? [y/N]' in out:
            if answer == 'interrupt': os.kill(p.pid,signal.SIGINT)
            else: os.write(m,(answer+'\r').encode())
            answered=True
        if not exited and b'kh chat, session' in out:
            os.write(m,b'\x04'); exited=True
        if p.poll() is not None: break
    else: raise AssertionError(bytes(out))
finally:
    if p.poll() is None: p.kill()
    p.wait(); os.close(m)
sys.stdout.buffer.write(out)
`, binary, answer}, args...)...)
		} else {
			cmd = exec.CommandContext(ctx, binary, args...)
			cmd.Stdin = strings.NewReader("")
		}
		cmd.Dir, cmd.Env = cwd, env
		out, err := cmd.CombinedOutput()
		// -p fails later because the fixture deliberately has no model login.
		if ctx.Err() != nil || (err != nil && !headless) {
			t.Fatalf("CLI: %s: %v (context %v)", out, err, ctx.Err())
		}
		text := string(out)
		if strings.Contains(text, "Trust MCP server") || strings.Contains(text, "loaded 1 MCP tools") {
			t.Fatalf("auto startup trust clutter: %s", text)
		}
		for _, secret := range []string{"code_challenge=", "state=", "cli-fixture-access-secret", "cli-fixture-refresh-secret", "cli-fixture-code"} {
			if strings.Contains(text, secret) {
				t.Fatalf("OAuth secret exposed in CLI output: %s", text)
			}
		}
		return text
	}
	for _, tc := range []struct {
		name               string
		terminal, headless bool
	}{
		{"redirected-chat", false, false}, {"redirected-headless", false, true}, {"terminal-headless", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, tc.terminal, tc.headless, "")
			if !strings.Contains(out, "authorization required") || strings.Contains(out, "Open browser") || registrations.Load() != 0 || authorizations.Load() != 0 {
				t.Fatalf("headless OAuth attempted interactive login: %s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("browser invoked: %v", err)
			}
		})
	}
	t.Run("delegated-terminal-no-browser", func(t *testing.T) {
		store, err := ecosystem.Open(filepath.Join(home, ".kh", "ecosystem.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		process, err := ecosystem.CurrentProcess()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "oauth-parent", MaxAgents: 4, Process: process})
		if err != nil {
			t.Fatal(err)
		}
		reservation, err := store.Reserve(ctx, root.ID, "oauth-child", "fixture child")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, process); err != nil {
			t.Fatal(err)
		}
		originalEnv := env
		env = append(append([]string{}, env...), "KH_ECOSYSTEM_ID="+root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID="+reservation.Agent.ID, "KH_ECOSYSTEM_TOKEN="+reservation.Token)
		defer func() { env = originalEnv }()
		out := run(t, true, false, "")
		if !strings.Contains(out, "authorization required") || strings.Contains(out, "Open browser") || authorizations.Load() != 0 {
			t.Fatalf("worker tried interactive OAuth: %s", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("worker browser invoked: %v", err)
		}
	})
	t.Run("terminal-consent-sigint", func(t *testing.T) {
		out := run(t, true, false, "interrupt")
		if !strings.Contains(out, "context canceled") || !strings.Contains(out, "(MCP: 0 connected, 1 failed)") || authorizations.Load() != 0 {
			t.Fatalf("SIGINT did not cancel login: %s", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("cancelled browser invoked: %v", err)
		}
	})
	t.Run("terminal-consent-declined-even-auto", func(t *testing.T) {
		out := run(t, true, false, "n")
		if !strings.Contains(out, "Open browser for MCP sign-in at "+base) || !strings.Contains(out, "run it? [y/N]") || authorizations.Load() != 0 {
			t.Fatalf("missing explicit consent: %s", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("declined browser invoked: %v", err)
		}
	})
	t.Run("opener-failure-redacts-url-and-output", func(t *testing.T) {
		projectCLIWrite(t, failMarker, "fail")
		defer os.Remove(failMarker)
		out := run(t, true, false, "y")
		if !strings.Contains(out, "(MCP: 0 connected, 1 failed)") || authorizations.Load() != 0 {
			t.Fatalf("failed opener unexpectedly logged in: %s", out)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("failing opener not exercised: %v", err)
		}
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("terminal-login", func(t *testing.T) {
		out := run(t, true, false, "y")
		if !strings.Contains(out, "(MCP: 1 connected)") || authorizations.Load() != 1 {
			t.Fatalf("OAuth login failed: %s", out)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("browser not invoked: %v", err)
		}
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	})
	for _, headless := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached-redirected-headless-%t", headless), func(t *testing.T) {
			out := run(t, false, headless, "")
			if !strings.Contains(out, "(MCP: 1 connected)") || strings.Contains(out, "Open browser") || authorizations.Load() != 1 {
				t.Fatalf("cached OAuth unavailable headless: %s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("cached browser invoked: %v", err)
			}
		})
	}
}
