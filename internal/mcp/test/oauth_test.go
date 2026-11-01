package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/mcp"
	"kh/internal/tools"
)

// oauthFixture exercises OAuth over HTTP, without depending on credential-file
// formats, transport internals, or a real browser or authorization server.
type oauthFixture struct {
	t      *testing.T
	server *httptest.Server
	mcp    *httpFixture

	mu             sync.Mutex
	redirect       string
	challenge      string
	code           string
	access         string
	refresh        string
	registrations  int
	authorizations int
	grants         int
	refreshes      int
	gate           chan struct{}
	gateRemaining  int
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{t: t, mcp: &httpFixture{t: t}}
	// httptest.NewServer listens on a literal loopback address, so no public
	// network or DNS exception is required for the HTTP-only test issuer.
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *oauthFixture) resource() string { return f.server.URL + "/mcp" }

func (f *oauthFixture) json(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		f.t.Errorf("encode OAuth response: %v", err)
	}
}

func (f *oauthFixture) reject(w http.ResponseWriter, problem string) {
	f.t.Error(problem)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, `{"error":"invalid_request"}`)
}

func (f *oauthFixture) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		f.json(w, map[string]any{"resource": f.resource(), "authorization_servers": []string{f.server.URL}, "scopes_supported": []string{"mcp:tools"}})
	case "/.well-known/oauth-authorization-server":
		f.json(w, map[string]any{
			"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
		})
	case "/register":
		var registration struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&registration) != nil || len(registration.RedirectURIs) != 1 || registration.AuthMethod != "none" {
			f.reject(w, "invalid public-client registration")
			return
		}
		redirect, err := url.Parse(registration.RedirectURIs[0])
		if err != nil || redirect.Scheme != "http" || !net.ParseIP(redirect.Hostname()).IsLoopback() || redirect.Port() == "" || redirect.Fragment != "" {
			f.reject(w, "registration redirect must be a literal HTTP loopback callback")
			return
		}
		f.mu.Lock()
		f.registrations++
		f.redirect = registration.RedirectURIs[0]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		f.json(w, map[string]any{"client_id": "public-fixture-client", "token_endpoint_auth_method": "none", "redirect_uris": registration.RedirectURIs})
	case "/authorize":
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("client_id") != "public-fixture-client" || q.Get("response_type") != "code" || q.Get("redirect_uri") != f.redirect || q.Get("state") == "" || q.Get("resource") != f.resource() || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			f.reject(w, "authorization request lost state, redirect, S256, or resource")
			return
		}
		f.authorizations++
		f.challenge = q.Get("code_challenge")
		f.code = fmt.Sprintf("fixture-code-%d", f.authorizations)
		callback, _ := url.Parse(f.redirect)
		values := callback.Query()
		values.Set("code", f.code)
		values.Set("state", q.Get("state"))
		callback.RawQuery = values.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	case "/token":
		f.token(w, r)
	case "/mcp":
		f.protected(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *oauthFixture) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("resource") != f.resource() || r.Form.Get("client_id") != "public-fixture-client" || r.Form.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
		f.reject(w, "token request must use public-client authentication and resource")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		verifier := r.Form.Get("code_verifier")
		digest := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(digest[:]) != f.challenge || f.code == "" || r.Form.Get("code") != f.code || r.Form.Get("redirect_uri") != f.redirect {
			f.reject(w, "code exchange did not prove PKCE or preserve redirect")
			return
		}
		f.code = "" // Authorization codes are single-use.
		f.grants++
	case "refresh_token":
		if f.refresh == "" || r.Form.Get("refresh_token") != f.refresh {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		f.refreshes++
	default:
		f.reject(w, "unexpected OAuth grant")
		return
	}
	generation := f.grants + f.refreshes
	f.access = fmt.Sprintf("fixture-access-%d", generation)
	f.refresh = fmt.Sprintf("fixture-refresh-%d", generation)
	f.json(w, map[string]any{"access_token": f.access, "token_type": "Bearer", "refresh_token": f.refresh, "expires_in": 3600, "scope": "mcp:tools"})
}

func (f *oauthFixture) protected(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	valid := f.access != "" && r.Header.Get("Authorization") == "Bearer "+f.access
	var gate chan struct{}
	if !valid && r.Method == http.MethodPost && r.Header.Get("Authorization") != "" && f.gateRemaining > 0 {
		gate = f.gate
		f.gateRemaining--
		if f.gateRemaining == 0 {
			close(gate)
		}
	}
	f.mu.Unlock()
	if !valid {
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, f.server.URL))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mcp.serve(w, r)
}

func (f *oauthFixture) browser(ctx context.Context, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := f.server.Client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("simulated browser returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (f *oauthFixture) invalidate(concurrent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = "revoked"
	if concurrent > 0 {
		f.gate = make(chan struct{})
		f.gateRemaining = concurrent
	}
}

func (f *oauthFixture) counts() (registrations, authorizations, grants, refreshes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registrations, f.authorizations, f.grants, f.refreshes
}

func connectOAuth(t *testing.T, ctx context.Context, f *oauthFixture, dir string, browser func(context.Context, string) error) ([]tools.Tool, func()) {
	t.Helper()
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"oauth": {URL: f.resource()}}, mcp.Options{OAuth: &mcp.OAuthOptions{StoreDir: dir, OpenBrowser: browser, Notice: func(string) {}}})
	t.Cleanup(cleanup)
	if len(warnings) != 0 || len(ts) != 1 {
		t.Fatalf("OAuth connect tools=%d warnings=%v", len(ts), warnings)
	}
	return ts, cleanup
}

func oauthCall(ctx context.Context, tool tools.Tool, value string) error {
	input, _ := json.Marshal(map[string]string{"value": value})
	out, err := tool.Run(ctx, input)
	if err != nil {
		return err
	}
	if out != value {
		return fmt.Errorf("echo = %q, want %q", out, value)
	}
	return nil
}

func TestOAuthAuthorizationCodePKCEAndUnauthorizedRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newOAuthFixture(t)
	ts, _ := connectOAuth(t, ctx, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
	if err := oauthCall(ctx, ts[0], "authorized"); err != nil {
		t.Fatal(err)
	}
	if r, a, g, refresh := f.counts(); r != 1 || a != 1 || g != 1 || refresh != 0 {
		t.Fatalf("initial registration/authorization/code/refresh = %d/%d/%d/%d", r, a, g, refresh)
	}
	// The token still has an hour of advertised lifetime. Only the resource's
	// 401 can reveal revocation, so this cannot pass via expiry-based refresh.
	f.invalidate(0)
	if err := oauthCall(ctx, ts[0], "refreshed"); err != nil {
		t.Fatal(err)
	}
	if r, a, g, refresh := f.counts(); r != 1 || a != 1 || g != 1 || refresh != 1 {
		t.Fatalf("after 401 registration/authorization/code/refresh = %d/%d/%d/%d", r, a, g, refresh)
	}
}

func TestOAuthCachedCredentialsReconnectWithoutBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newOAuthFixture(t)
	dir := filepath.Join(t.TempDir(), "oauth")
	_, cleanup := connectOAuth(t, ctx, f, dir, f.browser)
	cleanup()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Use separate processes, not merely new transports, so an accidental
	// process-global token cache cannot masquerade as persisted credentials.
	restart := func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestOAuthCachedCredentialProcess$")
		cmd.Env = append(os.Environ(), "KH_MCP_OAUTH_CACHE_CHILD=1", "KH_MCP_OAUTH_ENDPOINT="+f.resource(), "KH_MCP_OAUTH_STORE="+dir)
		cmd.Stdin = strings.NewReader("")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("headless restart: %v\n%s", err, output)
		}
	}
	restart()
	f.invalidate(0)
	restart()
	if r, a, g, refresh := f.counts(); r != 1 || a != 1 || g != 1 || refresh != 1 {
		t.Fatalf("reconnect registration/authorization/code/refresh = %d/%d/%d/%d", r, a, g, refresh)
	}
}

// TestOAuthCachedCredentialProcess is a test-binary helper for the cache restart
// test. Only the endpoint and private store directory cross the process boundary.
func TestOAuthCachedCredentialProcess(t *testing.T) {
	if os.Getenv("KH_MCP_OAUTH_CACHE_CHILD") != "1" {
		t.Skip("headless cache subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"oauth": {URL: os.Getenv("KH_MCP_OAUTH_ENDPOINT")}}, mcp.Options{OAuth: &mcp.OAuthOptions{
		StoreDir: os.Getenv("KH_MCP_OAUTH_STORE"), OpenBrowser: func(context.Context, string) error {
			t.Error("cached credentials attempted interactive authorization")
			return errors.New("headless fixture has no browser")
		}, Notice: func(string) {},
	}})
	defer cleanup()
	if len(warnings) != 0 || len(ts) != 1 {
		t.Fatalf("headless connect tools=%d warnings=%v", len(ts), warnings)
	}
	if err := oauthCall(ctx, ts[0], "headless-cache"); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthConcurrentUnauthorizedCallsRotateRefreshOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newOAuthFixture(t)
	dir := filepath.Join(t.TempDir(), "oauth")
	ts, _ := connectOAuth(t, ctx, f, dir, f.browser)
	other, _ := connectOAuth(t, ctx, f, dir, func(context.Context, string) error {
		return errors.New("second connection must reuse cached credentials")
	})
	clients := []tools.Tool{ts[0], other[0]}
	const calls = 8
	// Hold all stale-token requests until every caller has reached the server.
	// This forces a real simultaneous-401 race across independent connections
	// sharing a credential store, rather than relying on sleeps.
	f.invalidate(calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := oauthCall(ctx, clients[i%len(clients)], fmt.Sprintf("concurrent-%d", i)); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if r, a, g, refresh := f.counts(); r != 1 || a != 1 || g != 1 || refresh != 1 {
		t.Fatalf("concurrent registration/authorization/code/refresh = %d/%d/%d/%d", r, a, g, refresh)
	}
	// The first refresh token was consumed. A second revocation must use its
	// rotated replacement, not the initial credential retained in memory.
	f.invalidate(0)
	if err := oauthCall(ctx, ts[0], "rotated-refresh"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, refresh := f.counts(); refresh != 2 {
		t.Fatalf("refresh count after rotation = %d", refresh)
	}
}

func TestOAuthBrowserFailureIsBoundedAndRedacted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f := newOAuthFixture(t)
	const secret = "browser-secret-must-not-leak"
	var notices []string
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"oauth": {URL: f.resource()}}, mcp.Options{OAuth: &mcp.OAuthOptions{
		StoreDir: filepath.Join(t.TempDir(), "oauth"), OpenBrowser: func(context.Context, string) error { return errors.New(secret) }, Notice: func(message string) { notices = append(notices, message) },
	}})
	defer cleanup()
	if len(ts) != 0 || len(warnings) != 1 {
		t.Fatalf("failed browser tools=%d warnings=%v", len(ts), warnings)
	}
	if strings.Contains(strings.Join(warnings, "\n"), secret) || strings.Contains(strings.Join(notices, "\n"), secret) {
		t.Fatal("browser error leaked through OAuth diagnostics")
	}
}
