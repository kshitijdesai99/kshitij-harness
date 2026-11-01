package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/mcp"
)

func TestOAuthDiscoveryUnprotectedServerDoesNotDiscover(t *testing.T) {
	f := newHTTPFixture(t, false, nil)
	original := f.server.Config.Handler
	var discovery atomic.Int32
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			discovery.Add(1)
			http.NotFound(w, r)
			return
		}
		original.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"public": {URL: f.server.URL}}, mcp.Options{OAuth: &mcp.OAuthOptions{
		StoreDir: filepath.Join(t.TempDir(), "oauth"),
		OpenBrowser: func(context.Context, string) error {
			t.Error("public server opened browser")
			return fmt.Errorf("unexpected browser")
		},
	}})
	defer cleanup()
	if len(warnings) != 0 || len(ts) != 1 {
		t.Fatalf("public connect tools=%d warnings=%v", len(ts), warnings)
	}
	if err := oauthCall(ctx, ts[0], "public"); err != nil {
		t.Fatal(err)
	}
	if n := discovery.Load(); n != 0 {
		t.Fatalf("public server caused %d discovery requests", n)
	}
}

func TestOAuthDiscoveryResourceMetadataLocations(t *testing.T) {
	for _, challenge := range []bool{true, false} {
		t.Run(fmt.Sprintf("challenge=%t", challenge), func(t *testing.T) {
			f := newOAuthFixture(t)
			original := f.server.Config.Handler
			var mu sync.Mutex
			var locations []string
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") || r.URL.Path == "/custom-resource-metadata" {
					mu.Lock()
					locations = append(locations, r.URL.Path)
					mu.Unlock()
					if challenge && r.URL.Path == "/custom-resource-metadata" || !challenge && r.URL.Path == "/.well-known/oauth-protected-resource" {
						f.json(w, map[string]any{"resource": f.resource(), "authorization_servers": []string{f.server.URL}})
					} else {
						http.NotFound(w, r)
					}
					return
				}
				if r.URL.Path == "/mcp" && r.Header.Get("Authorization") == "" {
					w.Header().Add("WWW-Authenticate", `Basic realm="first, realm", resource_metadata="https://invalid.example/not-bearer"`)
					value := `Digest realm="second, realm", nonce="n", Bearer realm="tools, public"`
					if challenge {
						value += fmt.Sprintf(`, resource_metadata="%s/custom-resource-metadata"`, f.server.URL)
					}
					w.Header().Add("WWW-Authenticate", value)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				original.ServeHTTP(w, r)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ts, _ := connectOAuth(t, ctx, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
			if err := oauthCall(ctx, ts[0], "discovered resource"); err != nil {
				t.Fatal(err)
			}
			want := []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"}
			if challenge {
				want = []string{"/custom-resource-metadata"}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(locations, want) {
				t.Fatalf("resource metadata locations = %v, want %v", locations, want)
			}
		})
	}
}

func TestOAuthDiscoveryOIDCPathFallbackAndHeaderIsolation(t *testing.T) {
	for _, appendPath := range []bool{false, true} {
		t.Run(fmt.Sprintf("append=%t", appendPath), func(t *testing.T) {
			f := newOAuthFixture(t)
			original := f.server.Config.Handler
			issuer := f.server.URL + "/tenant/"
			want := []string{"/.well-known/oauth-authorization-server/tenant", "/.well-known/openid-configuration/tenant"}
			if appendPath {
				want = append(want, "/tenant/.well-known/openid-configuration")
			}
			var mu sync.Mutex
			var locations []string
			var resourceRequests atomic.Int32
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/mcp" {
					resourceRequests.Add(1)
					if r.Header.Get("X-API-Key") != "resource-only" {
						t.Error("MCP request lost static resource header")
					}
				} else if r.URL.Path != "/authorize" && r.Header.Get("X-API-Key") != "" {
					t.Errorf("resource header leaked to %s", r.URL.Path)
				}
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					f.json(w, map[string]any{"resource": f.resource(), "authorization_servers": []string{issuer}})
					return
				case "/.well-known/oauth-authorization-server/tenant", "/.well-known/openid-configuration/tenant", "/tenant/.well-known/openid-configuration":
					mu.Lock()
					locations = append(locations, r.URL.Path)
					mu.Unlock()
					if r.URL.Path != want[len(want)-1] {
						http.NotFound(w, r)
						return
					}
					f.json(w, map[string]any{"issuer": issuer, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register", "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
					return
				case "/authorize":
					// Require the discovered issuer's exact trailing slash at callback,
					// while the fixture validates PKCE, token and resource binding.
					recorder := httptest.NewRecorder()
					original.ServeHTTP(recorder, r)
					target, err := url.Parse(recorder.Header().Get("Location"))
					if err != nil || target.Host == "" {
						t.Error("missing authorization redirect")
						w.WriteHeader(500)
						return
					}
					q := target.Query()
					q.Set("iss", issuer)
					target.RawQuery = q.Encode()
					http.Redirect(w, r, target.String(), http.StatusFound)
					return
				}
				original.ServeHTTP(w, r)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ts, cleanup, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"oauth": {URL: f.resource(), Headers: map[string]string{"X-API-Key": "resource-only"}}}, mcp.Options{OAuth: &mcp.OAuthOptions{StoreDir: filepath.Join(t.TempDir(), "oauth"), OpenBrowser: f.browser, Notice: func(string) {}}})
			defer cleanup()
			if len(warnings) != 0 || len(ts) != 1 {
				t.Fatalf("OIDC connect tools=%d warnings=%v", len(ts), warnings)
			}
			if err := oauthCall(ctx, ts[0], "OIDC"); err != nil {
				t.Fatal(err)
			}
			if resourceRequests.Load() == 0 {
				t.Error("no MCP requests checked")
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(locations, want) {
				t.Fatalf("authorization metadata locations = %v, want %v", locations, want)
			}
		})
	}
}

func TestOAuthDiscoveryUnauthorizedReplayBounds(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(fmt.Sprintf("cold=%t", cold), func(t *testing.T) {
			f := newOAuthFixture(t)
			original := f.server.Config.Handler
			var reject atomic.Bool
			var rejected atomic.Int32
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/mcp" && r.Method == http.MethodPost && reject.Load() {
					rejected.Add(1)
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, f.server.URL))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				original.ServeHTTP(w, r)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dir := filepath.Join(t.TempDir(), "oauth")
			ts, cleanup := connectOAuth(t, ctx, f, dir, f.browser)
			if cold {
				cleanup()
			}
			reject.Store(true)
			want := int32(2) // Original request and one runtime refresh/replay.
			if cold {
				want = 3 // Anonymous request, cached token, refreshed token.
				got, close, warnings := mcp.ConnectWithOptions(ctx, map[string]mcp.Server{"oauth": {URL: f.resource()}}, mcp.Options{OAuth: &mcp.OAuthOptions{
					StoreDir: dir,
					OpenBrowser: func(context.Context, string) error {
						t.Error("rejected cached grant reopened browser")
						return fmt.Errorf("unexpected browser")
					},
				}})
				close()
				if len(got) != 0 || len(warnings) == 0 {
					t.Fatalf("persistent 401 tools=%d warnings=%v", len(got), warnings)
				}
			} else if err := oauthCall(ctx, ts[0], "persistent 401"); err == nil {
				t.Error("persistent 401 call succeeded")
			}
			if n := rejected.Load(); n != want {
				t.Fatalf("rejected MCP requests = %d, want %d", n, want)
			}
			if _, a, g, r := f.counts(); a != 1 || g != 1 || r != 1 {
				t.Fatalf("authorization/code/refresh = %d/%d/%d, want 1/1/1", a, g, r)
			}
		})
	}
}

func TestOAuthDiscoveryProtectedListDoesNotRepeatInitialize(t *testing.T) {
	f := newOAuthFixture(t)
	original := f.server.Config.Handler
	var initializes atomic.Int32
	f.mcp.hook = func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
		if msg.Method == "initialize" && initializes.Add(1) > 1 {
			t.Error("OAuth recovery repeated successful initialize")
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		return false
	}
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" && !f.mcp.initialized.Load() {
			f.mcp.serve(w, r)
			return
		}
		original.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, _ := connectOAuth(t, ctx, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
	if err := oauthCall(ctx, ts[0], "authorized list"); err != nil {
		t.Fatal(err)
	}
	if n := initializes.Load(); n != 1 {
		t.Fatalf("initialize requests = %d, want 1", n)
	}
}
