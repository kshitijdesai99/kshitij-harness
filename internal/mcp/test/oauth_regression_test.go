package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOAuthRefreshDoesNotBlockCanceledPeerOrCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := newOAuthFixture(t)
	ts, cleanup := connectOAuth(t, ctx, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
	defer cleanup()
	original := f.server.Config.Handler
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		original.ServeHTTP(w, r)
	})
	f.invalidate(0)
	first := make(chan error, 1)
	go func() { first <- oauthCall(ctx, ts[0], "refresh") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("refresh handler did not start")
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	peer := make(chan error, 1)
	go func() { peer <- oauthCall(short, ts[0], "canceled-peer") }()
	select {
	case err := <-peer:
		if err == nil {
			t.Error("canceled peer succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("peer blocked on refresh past its deadline")
	}
	closed := make(chan struct{})
	go func() { cleanup(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(1500 * time.Millisecond):
		t.Error("cleanup blocked on refresh mutex")
	}
	close(release)
	<-first
}

func TestOAuthRejectsSpecialUseAuthorizationDestinations(t *testing.T) {
	for _, host := range []string{"100.100.100.200", "198.18.0.1", "[64:ff9b::a00:1]", "[64:ff9b:1::1]", "[2001::1]", "[2001:db8::1]", "[2002::1]", "[fec0::1]", "[3fff::1]"} {
		t.Run(host, func(t *testing.T) {
			f := newOAuthFixture(t)
			original := f.server.Config.Handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/.well-known/oauth-authorization-server" {
					f.json(w, map[string]any{"issuer": f.server.URL, "authorization_endpoint": "https://" + host + "/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register", "code_challenge_methods_supported": []string{"S256"}})
					return
				}
				original.ServeHTTP(w, r)
			})
			warnings := oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), func(context.Context, string) error {
				t.Error("unsafe authorization URL reached browser")
				return fmt.Errorf("blocked")
			})
			if !strings.Contains(strings.Join(warnings, " "), "not public") {
				t.Fatalf("special-use destination not rejected before registration: %v", warnings)
			}
		})
	}
}

func TestOAuthDiscoveryFallbackDoesNotInheritPartialMetadata(t *testing.T) {
	f := newOAuthFixture(t)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			f.json(w, map[string]any{"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register", "code_challenge_methods_supported": false})
			return
		case "/.well-known/openid-configuration":
			f.json(w, map[string]any{"code_challenge_methods_supported": []string{"S256"}})
			return
		}
		original.ServeHTTP(w, r)
	})
	oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), func(context.Context, string) error {
		t.Error("partial fallback inherited issuer/endpoints")
		return fmt.Errorf("blocked")
	})
	if r, _, _, _ := f.counts(); r != 0 {
		t.Fatal("invalid fallback caused registration")
	}
}

func TestOAuthExpiryRefreshAndAuthenticatedCleanup(t *testing.T) {
	f := newOAuthFixture(t)
	original := f.server.Config.Handler
	var deletes int
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			recorder := httptest.NewRecorder()
			original.ServeHTTP(recorder, r)
			var token map[string]any
			if json.Unmarshal(recorder.Body.Bytes(), &token) == nil && token["access_token"] == "fixture-access-1" {
				token["expires_in"] = 1
				f.json(w, token)
				return
			}
			for k, v := range recorder.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		if r.URL.Path == "/mcp" && r.Method == http.MethodDelete {
			deletes++
			if r.Header.Get("Authorization") == "" {
				t.Error("cleanup omitted bearer token")
			}
		}
		original.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, cleanup := connectOAuth(t, ctx, f, filepath.Join(t.TempDir(), "oauth"), f.browser)
	if _, _, _, refresh := f.counts(); refresh != 1 {
		t.Fatalf("known-expiry refresh count=%d", refresh)
	}
	cleanup()
	if deletes != 1 {
		t.Fatalf("authenticated cleanup DELETEs=%d", deletes)
	}
}

func TestOAuthAuthorizationDestinationMustResolveBeforeRegistration(t *testing.T) {
	originalResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("fixture DNS unavailable")
	}}
	defer func() { net.DefaultResolver = originalResolver }()
	f := newOAuthFixture(t)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			f.json(w, map[string]any{"issuer": f.server.URL, "authorization_endpoint": "https://unresolvable.invalid/authorize", "token_endpoint": f.server.URL + "/token", "registration_endpoint": f.server.URL + "/register", "code_challenge_methods_supported": []string{"S256"}})
			return
		}
		original.ServeHTTP(w, r)
	})
	warnings := oauthSecurityConnect(t, f, filepath.Join(t.TempDir(), "oauth"), func(context.Context, string) error {
		t.Error("unresolved authorization destination reached browser")
		return fmt.Errorf("blocked")
	})
	if !strings.Contains(strings.Join(warnings, " "), "lookup failed") {
		t.Fatalf("authorization destination DNS was not checked: %v", warnings)
	}
	if registrations, _, _, _ := f.counts(); registrations != 0 {
		t.Fatal("unresolved authorization destination caused registration")
	}
}

func TestOAuthCachePermissionsArePrivate(t *testing.T) {
	f := newOAuthFixture(t)
	dir := filepath.Join(t.TempDir(), "oauth")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, cleanup := connectOAuth(t, ctx, f, dir, f.browser)
	cleanup()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("store permissions: %v %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("credential permissions: %v %v", info, err)
		}
	}
}
