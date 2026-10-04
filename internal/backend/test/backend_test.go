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

	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/tools"
)

func TestFactoryRejectsUnknownBackend(t *testing.T) {
	for _, model := range []string{config.Defaults.Model, "future-model-name"} {
		c := config.Defaults
		c.Provider, c.Model = "not-installed", model
		if _, err := backend.New(c, "", nil, nil); err == nil {
			t.Fatalf("unknown backend accepted with model %q", model)
		}
	}
	if err := backend.Login(context.Background(), "not-installed"); err == nil {
		t.Fatal("unknown login backend accepted")
	}
}

func TestFactoryDoesNotInferBackendFromModelName(t *testing.T) {
	c := config.Defaults
	c.Model = "future-model-name"
	p, err := backend.New(c, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model, _ := p.Use("", ""); model != c.Model {
		t.Fatal(model)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Inspect the final adapter request rather than a factory implementation detail.
func factoryInstructions(t *testing.T, c config.Config, ts []tools.Tool) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	claims, err := json.Marshal(map[string]any{
		"exp":                         time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := json.Marshal(map[string]string{
		"access_token":  "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test",
		"refresh_token": "test-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "codex.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	var instructions string
	seen := false
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var body struct{ Instructions string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		instructions, seen = body.Instructions, true
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	p, err := backend.New(c, "", ts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Step(context.Background(), "question", nil); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no adapter request observed")
	}
	return instructions
}

func TestFactoryParentInstructionsRequireAgentsCapability(t *testing.T) {
	for _, tc := range []struct {
		name, parent string
		tools        []tools.Tool
		want         bool
	}{
		{name: "tool-less", parent: "kh:main"},
		{name: "other tools", parent: "kh:main", tools: []tools.Tool{{Name: "bash"}, {Name: "edit"}}},
		{name: "agents", parent: "kh:main", tools: []tools.Tool{{Name: "agents"}}, want: true},
		{name: "not exact capability", parent: "kh:main", tools: []tools.Tool{{Name: "custom-agents"}}},
		{name: "no parent", tools: []tools.Tool{{Name: "agents"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KH_PARENT", tc.parent)
			t.Setenv("KH_AGENT", "kh:worker")
			c := config.Defaults
			c.System, c.WebSearch = "FACTORY_SYSTEM_SENTINEL", false
			got := factoryInstructions(t, c, tc.tools)
			if strings.Contains(got, "Use the native agents tool") != tc.want {
				t.Fatalf("agents instructions want=%v got=%q", tc.want, got)
			}
			if tc.want && (!strings.Contains(got, "kh:main") || !strings.Contains(got, "kh:worker")) {
				t.Fatalf("agent addresses missing: %q", got)
			}
			if !strings.Contains(got, c.System) {
				t.Fatalf("configured system prompt lost: %q", got)
			}
		})
	}
}

func TestFactoryPreservesToollessHelpInstructions(t *testing.T) {
	t.Setenv("KH_PARENT", "kh:main")
	c := config.Defaults
	c.System, c.WebSearch = "Explain how the agents and memory tools work; show bash and curl examples without executing them.", false
	if got := factoryInstructions(t, c, nil); got != c.System {
		t.Fatalf("tool-less help instructions changed: got %q want %q", got, c.System)
	}
}
