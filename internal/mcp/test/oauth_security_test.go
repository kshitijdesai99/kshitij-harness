package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/mcp"
)

func oauthSecurityConnect(t *testing.T, f *oauthFixture, dir string, browser func(context.Context, string) error) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"security": {URL: f.resource()}}, mcp.Options{OAuth: &mcp.OAuthOptions{StoreDir: dir, OpenBrowser: browser}})
	defer cleanup()
	if len(ts) != 0 || len(warnings) != 1 {
		t.Fatalf("unsafe OAuth accepted: tools=%d warnings=%v", len(ts), warnings)
	}
	return warnings
}

func TestOAuthRejectsUnboundOrUnsupportedMetadata(t *testing.T) {
	for _, kind := range []string{"resource", "issuer", "missing-S256", "no-registration"} {
		t.Run(kind, func(t *testing.T) {
			f := newOAuthFixture(t)
			original := f.server.Config.Handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "oauth-protected-resource") && kind == "resource" {
					f.json(w, map[string]any{"resource": f.server.URL + "/different", "authorization_servers": []string{f.server.URL}})
					return
				}
				if r.URL.Path == "/.well-known/oauth-authorization-server" {
					metadata := map[string]any{"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register", "response_types_supported": []string{"code"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}}
					switch kind {
					case "issuer":
						metadata["issuer"] = f.server.URL + "/different"
					case "missing-S256":
						metadata["code_challenge_methods_supported"] = []string{"plain"}
					case "no-registration":
						delete(metadata, "registration_endpoint")
					}
					f.json(w, metadata)
					return
				}
				original.ServeHTTP(w, r)
			})
			var browsers atomic.Int32
			oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), func(context.Context, string) error { browsers.Add(1); return nil })
			if r, a, g, refresh := f.counts(); browsers.Load() != 0 || r != 0 || a != 0 || g != 0 || refresh != 0 {
				t.Fatalf("invalid metadata caused authorization: browser=%d counts=%d/%d/%d/%d", browsers.Load(), r, a, g, refresh)
			}
		})
	}
}

func TestOAuthInvalidCallbacksNeverReachTokenEndpoint(t *testing.T) {
	for _, kind := range []string{"wrong-state", "duplicate-code", "wrong-issuer", "duplicate-state", "error-and-code"} {
		t.Run(kind, func(t *testing.T) {
			f := newOAuthFixture(t)
			var tokens atomic.Int32
			original := f.server.Config.Handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokens.Add(1)
				}
				original.ServeHTTP(w, r)
			})
			browser := func(ctx context.Context, target string) error {
				authorize, err := url.Parse(target)
				if err != nil {
					return err
				}
				callback, err := url.Parse(authorize.Query().Get("redirect_uri"))
				if err != nil {
					return err
				}
				q := url.Values{"code": {"untrusted-code-secret"}, "state": {authorize.Query().Get("state")}}
				switch kind {
				case "wrong-state":
					q.Set("state", "untrusted-state-secret")
				case "duplicate-code":
					q.Add("code", "second-code-secret")
				case "wrong-issuer":
					q.Set("iss", f.server.URL+"/wrong")
				case "duplicate-state":
					q.Add("state", q.Get("state"))
				case "error-and-code":
					q.Set("error", "denied-secret")
				}
				callback.RawQuery = q.Encode()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, callback.String(), nil)
				if err != nil {
					return err
				}
				response, err := f.server.Client().Do(request)
				if err != nil {
					return err
				}
				defer response.Body.Close()
				_, _ = io.Copy(io.Discard, response.Body)
				return nil
			}
			warnings := oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), browser)
			if tokens.Load() != 0 {
				t.Fatalf("invalid callback reached token endpoint %d times", tokens.Load())
			}
			for _, secret := range []string{"untrusted-code-secret", "untrusted-state-secret", "second-code-secret", "denied-secret"} {
				if strings.Contains(strings.Join(warnings, "\n"), secret) {
					t.Fatal("callback secrets leaked in warnings")
				}
			}
		})
	}
}

func TestOAuthRejectsDiscoveryAndResourceRedirects(t *testing.T) {
	for _, path := range []string{"/mcp", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server", "/register", "/token"} {
		t.Run(path, func(t *testing.T) {
			var redirected atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
				json.NewEncoder(w).Encode(map[string]any{})
			}))
			defer trap.Close()
			f := newOAuthFixture(t)
			var attempted atomic.Int32
			original := f.server.Config.Handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == path {
					attempted.Add(1)
					http.Redirect(w, r, trap.URL, http.StatusFound)
					return
				}
				// Prevent fallback metadata from bypassing the invalid response,
				// but allow valid PRM discovery before the AS redirect case.
				wellKnown := strings.HasPrefix(r.URL.Path, "/.well-known/")
				prm := strings.Contains(r.URL.Path, "oauth-protected-resource")
				if wellKnown && ((strings.Contains(path, "oauth-protected-resource") && r.URL.Path != path) || (strings.Contains(path, "oauth-authorization-server") && !prm)) {
					http.NotFound(w, r)
					return
				}
				original.ServeHTTP(w, r)
			})
			oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
			if attempted.Load() == 0 {
				t.Fatal("redirect case was not reached")
			}
			if redirected.Load() != 0 {
				t.Fatalf("followed untrusted redirect %d times", redirected.Load())
			}
		})
	}
}

func TestOAuthHeadlessDoesNotRegisterOrOpenBrowser(t *testing.T) {
	f := newOAuthFixture(t)
	warnings := oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), nil)
	if !strings.Contains(strings.Join(warnings, "\n"), "authorization required") {
		t.Fatalf("missing headless diagnostic: %v", warnings)
	}
	if r, a, g, refresh := f.counts(); r != 0 || a != 0 || g != 0 || refresh != 0 {
		t.Fatalf("headless started authorization: %d/%d/%d/%d", r, a, g, refresh)
	}
}

func TestOAuthExplicitAuthorizationIsUntouched(t *testing.T) {
	f := newOAuthFixture(t)
	f.mu.Lock()
	f.access = "configured"
	f.mu.Unlock()
	var discovery atomic.Int32
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			discovery.Add(1)
		}
		original.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	store := filepath.Join(t.TempDir(), "oauth")
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"explicit": {URL: f.resource(), Headers: map[string]string{"aUtHoRiZaTiOn": "Bearer configured"}}}, mcp.Options{OAuth: &mcp.OAuthOptions{StoreDir: store, OpenBrowser: func(context.Context, string) error {
		t.Error("explicit header invoked browser")
		return fmt.Errorf("unexpected browser")
	}}})
	defer cleanup()
	if len(warnings) != 0 || len(ts) != 1 || discovery.Load() != 0 {
		t.Fatalf("explicit Authorization changed: tools=%d discovery=%d warnings=%v", len(ts), discovery.Load(), warnings)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("explicit header touched OAuth store: %v", err)
	}
}

func TestOAuthRejectsUnsafeCredentialPaths(t *testing.T) {
	for _, kind := range []string{"directory-permissions", "directory-symlink", "credential-permissions", "credential-symlink", "lock-permissions"} {
		t.Run(kind, func(t *testing.T) {
			f := newOAuthFixture(t)
			dir := filepath.Join(t.TempDir(), "oauth")
			if strings.HasPrefix(kind, "directory-") {
				if kind == "directory-permissions" {
					if err := os.Mkdir(dir, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(dir, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					target := filepath.Join(t.TempDir(), "real")
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, dir); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				_, cleanup := connectOAuth(t, ctx, f, dir, f.browser)
				cleanup()
				cancel()
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				suffix := ".json"
				if kind == "lock-permissions" {
					suffix = ".lock"
				}
				var path string
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), suffix) {
						path = filepath.Join(dir, entry.Name())
						break
					}
				}
				if path == "" {
					t.Fatalf("missing persisted %s file", suffix)
				}
				if kind == "credential-symlink" {
					target := filepath.Join(t.TempDir(), "outside")
					if err := os.Rename(path, target); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			beforeR, beforeA, beforeG, beforeRefresh := f.counts()
			oauthSecurityConnect(t, f, dir, f.browser)
			if r, a, g, refresh := f.counts(); r != beforeR || a != beforeA || g != beforeG || refresh != beforeRefresh {
				t.Fatalf("unsafe store triggered authorization: before=%d/%d/%d/%d after=%d/%d/%d/%d", beforeR, beforeA, beforeG, beforeRefresh, r, a, g, refresh)
			}
		})
	}
}
