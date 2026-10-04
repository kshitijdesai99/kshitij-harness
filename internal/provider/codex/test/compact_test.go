package test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/terminal"
	"kh/internal/tools"
)

func compactState(t *testing.T, p *codex.Client) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"session": "compact-session", "map": "saved map", "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": strings.Repeat("old conversation details ", 100)}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Load(b); err != nil {
		t.Fatal(err)
	}
	before, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	return before
}

func summaryStream(text string) string {
	event, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": text})
	return "data: " + string(event) + "\n\n" + completeEvent
}

func TestCompactSummarizesWithoutToolsAndResumes(t *testing.T) {
	mockCodex(t, summaryStream("Goal: add /compact. Tests pending."))
	var output strings.Builder
	cfg := config.Defaults
	cfg.System += "\nUse the agents tool and curl for operational work."
	p := codex.New(cfg, "", []tools.Tool{tools.Edit, {Name: "agents"}, {Name: "bash"}}, terminal.Renderer{Out: &output, Err: &output})
	before := compactState(t, p)
	p.SetMemory("EPHEMERAL_MEMORY")
	p.AttachImage("STAGED_IMAGE")
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var request struct {
			Tools        []any
			Input        []map[string]any
			Instructions string
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if len(request.Tools) != 0 || !strings.Contains(request.Instructions, "Do not use tools") {
			t.Fatalf("unsafe compaction request: %s", body)
		}
		if strings.Contains(strings.ToLower(request.Instructions), "agents") || strings.Contains(strings.ToLower(request.Instructions), "curl") {
			t.Fatalf("operational rules retained in compaction: %s", body)
		}
		if bytes.Contains(body, []byte("EPHEMERAL_MEMORY")) || bytes.Contains(body, []byte("STAGED_IMAGE")) {
			t.Fatalf("ephemeral state included: %s", body)
		}
		return transport.RoundTrip(r)
	})
	if err := p.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) || !bytes.Contains(after, []byte("Tests pending")) || bytes.Contains(after, []byte("old conversation details")) {
		t.Fatalf("bad compacted state: %s", after)
	}
	if output.Len() != 0 {
		t.Fatalf("summary streamed into chat: %q", output.String())
	}
	resumed := codex.New(config.Defaults, "", nil, nil)
	if err := resumed.Load(after); err != nil {
		t.Fatal(err)
	}
	saved, _ := resumed.Save()
	if !bytes.Equal(after, saved) {
		t.Fatalf("resume changed state: %s", saved)
	}
	model, effort := p.Use("", "")
	if model != config.Defaults.Model || effort != config.Defaults.Effort {
		t.Fatal("selection changed")
	}
	// Staged images are preserved for the actual next user message.
	http.DefaultClient.Transport = transport
	if _, err := p.Step(context.Background(), "continue", nil); err != nil {
		t.Fatal(err)
	}
	next, _ := p.Save()
	if !bytes.Contains(next, []byte("STAGED_IMAGE")) {
		t.Fatal("staged image lost")
	}
}

func TestCompactFailurePreservesHistory(t *testing.T) {
	for name, stream := range map[string]string{
		"incomplete":  summaryStream("partial")[:len(summaryStream("partial"))-len(completeEvent)],
		"empty":       completeEvent,
		"tool call":   toolItem + completeEvent,
		"not smaller": summaryStream(strings.Repeat("long summary ", 1000)),
		"cancelled":   summaryStream("summary"),
	} {
		t.Run(name, func(t *testing.T) {
			p := mockCodex(t, stream)
			before := compactState(t, p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "incomplete" {
				var timeoutCancel context.CancelFunc
				ctx, timeoutCancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer timeoutCancel()
			}
			if name == "cancelled" {
				cancel()
			}
			if err := p.Compact(ctx); err == nil {
				t.Fatal("failed compaction accepted")
			}
			after, _ := p.Save()
			if !bytes.Equal(before, after) {
				t.Fatalf("history changed: %s", after)
			}
		})
	}
}

func TestCompactEmptyHistory(t *testing.T) {
	p := codex.New(config.Defaults, "", nil, nil)
	before, _ := p.Save()
	if err := p.Compact(context.Background()); err == nil {
		t.Fatal("empty history accepted")
	}
	after, _ := p.Save()
	if !bytes.Equal(before, after) {
		t.Fatal("empty history changed")
	}
}
