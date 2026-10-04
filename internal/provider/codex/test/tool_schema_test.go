package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/tools"
)

func TestWebPromptMentionsCurlOnlyWithBashCapability(t *testing.T) {
	for _, tc := range []struct {
		name     string
		web      bool
		tools    []tools.Tool
		wantCurl bool
	}{
		{name: "web without tools", web: true},
		{name: "web with edit", web: true, tools: []tools.Tool{tools.Edit}},
		{name: "web with agents", web: true, tools: []tools.Tool{{Name: "agents"}}},
		{name: "web with bash", web: true, tools: []tools.Tool{{Name: "bash"}}, wantCurl: true},
		{name: "web with other command tool", web: true, tools: []tools.Tool{{Name: "custom-bash"}}},
		{name: "no web with bash", tools: []tools.Tool{{Name: "bash"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockCodex(t, completeEvent)
			var instructions string
			seen := false
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				var body struct{ Instructions string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				instructions, seen = body.Instructions, true
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(completeEvent))}, nil
			})
			cfg := config.Defaults
			cfg.System, cfg.WebSearch = "WEB_SYSTEM_SENTINEL", tc.web
			p := codex.New(cfg, "", tc.tools, nil)
			if _, err := p.Step(context.Background(), "question", nil); err != nil {
				t.Fatal(err)
			}
			if !seen || strings.Contains(strings.ToLower(instructions), "curl") != tc.wantCurl {
				t.Fatalf("curl instructions want=%v seen=%v got=%q", tc.wantCurl, seen, instructions)
			}
			if tc.web && !strings.Contains(instructions, "web search") {
				t.Fatalf("web capability instructions missing: %q", instructions)
			}
		})
	}
}

func TestFunctionSchemaKeepsAlternativeEditFormatsOptional(t *testing.T) {
	_ = mockCodex(t, completeEvent)
	saw := false
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			var body struct{ Tools []map[string]any }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, tool := range body.Tools {
				if tool["name"] == "edit" {
					saw = true
					strict, exists := tool["strict"]
					if !exists || strict != false {
						t.Errorf("alternative edit schema must explicitly disable strict coercion: %v", tool)
					}
					params := tool["parameters"].(map[string]any)
					if required, ok := params["required"].([]any); !ok || len(required) != 0 {
						t.Errorf("legacy fields made mandatory: %v", params)
					}
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(completeEvent)), Header: make(http.Header)}, nil
	})
	p := codex.New(config.Defaults, "", []tools.Tool{tools.Edit}, nil)
	if _, err := p.Step(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("edit function schema missing")
	}
}
