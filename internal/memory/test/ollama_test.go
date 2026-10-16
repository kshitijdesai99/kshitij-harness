package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestOllamaRequestAndIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL, r.Header)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		var model string
		var input []string
		var truncate bool
		if len(body) != 3 || json.Unmarshal(body["model"], &model) != nil || json.Unmarshal(body["input"], &input) != nil || json.Unmarshal(body["truncate"], &truncate) != nil || model != "test-model" || !reflect.DeepEqual(input, []string{"first", "second"}) || truncate {
			t.Errorf("unexpected payload: %s", body)
		}
		fmt.Fprint(w, `{"embeddings":[[1,0],[0,2]]}`)
	}))
	defer server.Close()
	endpoint := server.URL + "/api/embed"
	e, err := memory.NewOllamaEmbedder(endpoint, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil || !reflect.DeepEqual(got, [][]float32{{1, 0}, {0, 2}}) {
		t.Fatalf("vectors: %v %v", got, err)
	}
	if got, err := e.Embed(context.Background(), nil); err != nil || len(got) != 0 || calls.Load() != 1 {
		t.Fatalf("empty input made request: %v %v calls=%d", got, err, calls.Load())
	}
	same, _ := memory.NewOllamaEmbedder(endpoint, "test-model")
	otherModel, _ := memory.NewOllamaEmbedder(endpoint, "other-model")
	otherEndpoint, _ := memory.NewOllamaEmbedder(server.URL+"/different/api/embed", "test-model")
	if e.Model() == "" || e.Model() != same.Model() || e.Model() == otherModel.Model() || e.Model() == otherEndpoint.Model() {
		t.Fatal("model identity does not distinguish endpoint and model")
	}
}

func TestOllamaRejectsConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "file:///tmp/embed", "ftp://example.com/api/embed", "http://", "http://user:pass@example.com/api/embed", "http://example.com/api/embed?key=x", "http://example.com/api/embed?", "http://example.com/api/embed#fragment"} {
		if _, err := memory.NewOllamaEmbedder(endpoint, "model"); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
	for _, model := range []string{"", "  ", "two models", "model\nname", strings.Repeat("x", 201), "token=privatevalue"} {
		if _, err := memory.NewOllamaEmbedder("http://localhost/api/embed", model); err == nil {
			t.Errorf("accepted model %q", model)
		}
	}
}

func TestOllamaRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"json", `not json`, 200},
		{"missing", `{}`, 200},
		{"count", `{"embeddings":[[1,0],[0,1]]}`, 200},
		{"empty", `{"embeddings":[[]]}`, 200},
		{"zero", `{"embeddings":[[0,0]]}`, 200},
		{"overflow", `{"embeddings":[[1e100,0]]}`, 200},
		{"non-number", `{"embeddings":[["NaN",1]]}`, 200},
		{"http", `sensitive server diagnostic`, 503},
		{"oversize", strings.Repeat(" ", (8<<20)+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			e, err := memory.NewOllamaEmbedder(server.URL, "model")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Embed(context.Background(), []string{"hello"}); err == nil || strings.Contains(err.Error(), "sensitive server diagnostic") {
				t.Fatalf("expected safe error, got %v", err)
			}
		})
	}
	t.Run("dimensions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"embeddings":[[1,0],[1,0,0]]}`) }))
		defer server.Close()
		e, _ := memory.NewOllamaEmbedder(server.URL, "model")
		if _, err := e.Embed(context.Background(), []string{"one", "two"}); err == nil {
			t.Fatal("accepted mixed dimensions")
		}
	})
}

func TestOllamaSecretGuardAndRedirect(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); fmt.Fprint(w, `{"embeddings":[[1,0]]}`) }))
	defer target.Close()
	e, _ := memory.NewOllamaEmbedder(target.URL, "model")
	for _, secret := range []string{"password: privatevalue", "token=privatevalue", "-----BEGIN PRIVATE KEY-----"} {
		if _, err := e.Embed(context.Background(), []string{"innocent", secret}); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("expected redacted secret error: %v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("secret input reached HTTP server")
	}
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, status) }))
			defer redirect.Close()
			e, _ := memory.NewOllamaEmbedder(redirect.URL, "model")
			if _, err := e.Embed(context.Background(), []string{"hello"}); err == nil {
				t.Fatal("redirect accepted")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("redirect leaked request to target")
	}
}

func TestOllamaCancellationAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint("timeout=", timeout), func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			e, _ := memory.NewOllamaEmbedder(server.URL, "model")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			start := time.Now()
			go func() { _, err := e.Embed(ctx, []string{"hello"}); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second * 2):
				t.Fatal("no HTTP request")
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("request unexpectedly succeeded")
				}
				if timeout && time.Since(start) < 2*time.Second {
					t.Fatalf("provider timeout too early: %v", time.Since(start))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not respect cancellation/provider timeout")
			}
		})
	}
}
