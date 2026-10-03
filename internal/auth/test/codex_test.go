package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"kh/internal/auth"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func useTransport(t *testing.T, f roundTripFunc) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: f}
	t.Cleanup(func() { http.DefaultClient = old })
}

func accessToken(exp time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"exp":                         exp.Unix(),
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "account"},
	})
	return "header." + base64.RawURLEncoding.EncodeToString(b) + ".signature"
}

func tokenFile(t *testing.T, access string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kh", "codex.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTokens(t, path, access, "single-use")
	return path
}

func writeTokens(t *testing.T, path, access, refresh string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"access_token": access, "refresh_token": refresh})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func holdLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTokenLockCancellation(t *testing.T) {
	path := tokenFile(t, accessToken(time.Now().Add(time.Hour)))
	holdLock(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := auth.Token(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Token error = %v, want deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Token blocked on a held lock despite cancellation")
	}
}

func TestTokenAlreadyCanceled(t *testing.T) {
	tokenFile(t, accessToken(time.Now().Add(time.Hour)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := auth.Token(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Token error = %v, want canceled", err)
	}
}

func TestTokenReadsAfterAcquiringLock(t *testing.T) {
	path := tokenFile(t, accessToken(time.Now().Add(-time.Hour)))
	f := holdLock(t, path)
	useTransport(t, func(*http.Request) (*http.Response, error) {
		t.Error("Token refreshed instead of reading updated tokens after acquiring lock")
		return nil, errors.New("unexpected refresh")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		access, account string
		err             error
	}
	done := make(chan result, 1)
	go func() {
		a, id, err := auth.Token(ctx)
		done <- result{a, id, err}
	}()
	// Leave time for Token to encounter the held lock before rotating tokens.
	time.Sleep(100 * time.Millisecond)
	fresh := accessToken(time.Now().Add(time.Hour))
	writeTokens(t, path, fresh, "rotated")
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.access != fresh || got.account != "account" {
			t.Fatalf("Token = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Token did not acquire released lock")
	}
}

func TestRefreshDeadlineAndSave(t *testing.T) {
	path := tokenFile(t, accessToken(time.Now().Add(-time.Hour)))
	fresh := accessToken(time.Now().Add(time.Hour))
	calls := 0
	useTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 29*time.Second || remaining > 30*time.Second {
			t.Errorf("refresh deadline remaining = %v (present %v), want 30s", remaining, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Method != "POST" || r.URL.Path != "/oauth/token" || r.Form.Get("refresh_token") != "single-use" || r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("unexpected refresh request: %s %s %v", r.Method, r.URL, r.Form)
		}
		b, _ := json.Marshal(map[string]string{"access_token": fresh, "refresh_token": "rotated"})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: make(http.Header)}, nil
	})
	access, account, err := auth.Token(context.Background())
	if err != nil || access != fresh || account != "account" {
		t.Fatalf("Token = %q, %q, %v", access, account, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]string
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["refresh_token"] != "rotated" || stored["access_token"] != fresh || calls != 1 {
		t.Fatalf("stored = %v, refresh calls = %d", stored, calls)
	}
	if _, _, err := auth.Token(context.Background()); err != nil || calls != 1 {
		t.Fatalf("cached Token error = %v, refresh calls = %d", err, calls)
	}
}

func TestRefreshParentCancellationReleasesLockWithoutRetry(t *testing.T) {
	path := tokenFile(t, accessToken(time.Now().Add(-time.Hour)))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	useTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("refresh did not preserve parent's earlier deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err = auth.Token(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("Token error = %v, refresh calls = %d", err, calls)
	}
	// Nonblocking acquisition proves the canceled refresh released its lock.
	holdLock(t, path)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed refresh changed saved tokens")
	}
}
