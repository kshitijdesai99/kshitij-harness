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
	"kh/internal/help"
	"kh/internal/terminal"
)

type helpTransport func(*http.Request) (*http.Response, error)

func (f helpTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOneOffHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX", "must-not-use-tmux")
	t.Setenv("KH_PARENT", "kh:main")
	t.Setenv("KH_AGENT", "kh:worker")
	claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	token := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
	creds, _ := json.Marshal(map[string]string{"access_token": token, "refresh_token": "test-only"})
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "codex.json"), creds, 0600); err != nil {
		t.Fatal(err)
	}
	// A file at the sessions path makes any attempted load/save fail.
	sentinel := filepath.Join(config.Dir(), "sessions")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	for _, tc := range []struct{ name, stream, wantErr string }{
		{"answer", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"One-off answer\"}\n\ndata: {\"type\":\"response.completed\"}\n\n", ""},
		{"tool rejected", "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"x\",\"name\":\"bash\",\"arguments\":\"{}\"}}\n\ndata: {\"type\":\"response.completed\"}\n\n", "does not execute local tools"},
		{"failure", "data: {\"type\":\"response.failed\"}\n\n", "response failed"},
	} {
		for _, request := range []string{
			"how do i load a session", "resume my chat", "how do I switch models?",
			"where is configuration stored?", "explain the agents tool",
			"explain git rebase", "How do I load a session in another app?",
		} {
			t.Run(tc.name+"/"+request, func(t *testing.T) {
				requests := 0
				http.DefaultClient = &http.Client{Transport: helpTransport(func(r *http.Request) (*http.Response, error) {
					requests++
					var body struct {
						Input []struct {
							Role    string
							Content []struct{ Text string }
						}
						Tools        []struct{ Type string }
						Store        bool
						Instructions string
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body.Input) != 1 || body.Input[0].Role != "user" || body.Input[0].Content[0].Text != request || body.Store {
						t.Fatalf("unexpected request: %+v", body)
					}
					for _, tool := range body.Tools {
						if tool.Type != "web_search" {
							t.Errorf("local tool advertised: %+v", tool)
						}
					}
					for _, reference := range []string{"standalone help request", "help assistant for kh", "kh -s SESSION_ID", "kh -r", "kh --sessions", "working folder", "creation ID", "-example-live-flag"} {
						if !strings.Contains(body.Instructions, reference) {
							t.Errorf("missing help reference %q", reference)
						}
					}
					for _, forbidden := range []string{"autonomous coding assistant", "Use the native agents tool", "Use curl only"} {
						if strings.Contains(body.Instructions, forbidden) {
							t.Errorf("tool-less help inherited unavailable capability instructions %q", forbidden)
						}
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.stream))}, nil
				})}
				var out strings.Builder
				err := help.Run(context.Background(), config.Defaults, request, "-example-live-flag: generated CLI usage", terminal.Renderer{Out: &out, Err: &out})
				if tc.wantErr == "" {
					if err != nil || out.String() != "One-off answer\n" {
						t.Fatalf("output=%q err=%v", out.String(), err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v", err)
				}
				if requests != 1 {
					t.Errorf("requests=%d", requests)
				}
				b, err := os.ReadFile(sentinel)
				if err != nil || string(b) != "unchanged" {
					t.Fatalf("sessions changed: %q %v", b, err)
				}
				entries, err := os.ReadDir(config.Dir())
				if err != nil || len(entries) != 2 {
					t.Fatalf("unexpected persisted data: %v %v", entries, err)
				}
			})
		}
	}
	if err := help.Run(context.Background(), config.Defaults, "  ", "", nil); err == nil {
		t.Fatal("empty request accepted")
	}
}
