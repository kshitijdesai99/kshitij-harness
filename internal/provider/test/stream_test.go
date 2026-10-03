package test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/terminal"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Fake credentials and an in-process transport exercise the public adapter
// without a real account or network. No other test in this package is parallel.
func mockCodex(t *testing.T, stream string) *provider.Codex {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	claims, _ := json.Marshal(map[string]any{
		"exp":                         time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "test"},
	})
	access := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
	b, _ := json.Marshal(map[string]string{"access_token": access, "refresh_token": "test-only"})
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "codex.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	return provider.NewCodex(config.Defaults, "", nil, nil)
}

const toolItem = `data: {"type":"response.output_item.done","item":{"type":"function_call","id":"server-id","call_id":"call-1","name":"bash","arguments":"{\"command\":\"pwd\"}"}}` + "\n\n"
const completeEvent = `data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n\n"

func TestStreamRequiresCompletionAndRollsBackPartialItems(t *testing.T) {
	for name, stream := range map[string]string{
		"empty": "", "partial tool": toolItem,
		"done without completion": toolItem + "data: [DONE]\n\n",
		"malformed":               toolItem + "data: {broken\n\n",
		"explicit failure":        toolItem + `data: {"type":"response.failed"}` + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := mockCodex(t, stream)
			ctx := context.Background()
			if name == "empty" || name == "partial tool" || name == "done without completion" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			calls, err := p.Step(ctx, "question", nil)
			if err == nil || len(calls) != 0 {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			b, err := p.Save()
			if err != nil {
				t.Fatal(err)
			}
			var state struct{ Input []map[string]any }
			if err := json.Unmarshal(b, &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Input) != 1 || state.Input[0]["role"] != "user" {
				t.Fatalf("partial items retained: %s", b)
			}
		})
	}
}

func TestCompletedStreamReturnsToolCalls(t *testing.T) {
	p := mockCodex(t, toolItem+completeEvent)
	calls, err := p.Step(context.Background(), "question", nil)
	if err != nil || len(calls) != 1 || calls[0].ID != "call-1" || calls[0].Name != "bash" {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	b, err := p.Save()
	if err != nil || strings.Contains(string(b), "server-id") {
		t.Fatalf("state=%s err=%v", b, err)
	}
}

func TestAdapterStreamsThroughInjectedRenderer(t *testing.T) {
	_ = mockCodex(t, `data: {"type":"response.output_text.delta","delta":"hello"}`+"\n\n"+completeEvent)
	var out strings.Builder
	p := provider.NewCodex(config.Defaults, "", nil, terminal.Renderer{Out: &out, Err: &out})
	if _, err := p.Step(context.Background(), "question", nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hello\n" {
		t.Fatalf("output=%q", got)
	}
}

func TestFactoryRejectsUnknownBackend(t *testing.T) {
	c := config.Defaults
	c.Provider = "not-installed"
	if _, err := provider.New(c, "", nil, nil); err == nil {
		t.Fatal("unknown backend accepted")
	}
	if err := provider.Login(context.Background(), c.Provider); err == nil {
		t.Fatal("unknown login backend accepted")
	}
}

func TestFactoryDoesNotInferBackendFromModelName(t *testing.T) {
	c := config.Defaults
	c.Model = "future-model-name"
	p, err := provider.New(c, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model, _ := p.Use("", ""); model != c.Model {
		t.Fatal(model)
	}
}
