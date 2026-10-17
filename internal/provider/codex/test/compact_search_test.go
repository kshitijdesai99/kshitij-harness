package test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/tools"
)

func TestCompactHostedSearchHistoryKeepsSchemaButDisablesTools(t *testing.T) {
	mockCodex(t, "")
	cfg := config.Defaults
	cfg.WebSearch = false
	p := codex.New(cfg, "", []tools.Tool{tools.Edit, {Name: "agents"}, {Name: "bash"}}, nil)
	before := compactState(t, p)
	var state map[string]any
	if err := json.Unmarshal(before, &state); err != nil {
		t.Fatal(err)
	}
	state["input"] = append(state["input"].([]any), map[string]any{"type": "web_search_call", "id": "old-search-id", "status": "completed", "action": map[string]any{"type": "search", "query": "prior research"}})
	data, _ := json.Marshal(state)
	if err := p.Load(data); err != nil {
		t.Fatal(err)
	}
	requests := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		var body struct {
			Tools      []map[string]any
			Input      []map[string]any
			ToolChoice string `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return retryResponse(400, "invalid request"), nil
		}
		if requests == 1 {
			if body.ToolChoice != "none" || len(body.Tools) != 1 || body.Tools[0]["type"] != "web_search" {
				t.Errorf("unsafe/incompatible compaction tools: %+v choice=%s", body.Tools, body.ToolChoice)
			}
			found := false
			for _, item := range body.Input {
				if item["type"] == "web_search_call" && item["id"] == "old-search-id" {
					found = true
				}
			}
			if !found {
				t.Error("hosted history was discarded rather than made compatible")
			}
			return retryResponse(200, summaryStream("Research decisions and pending work.")), nil
		}
		if body.ToolChoice != "auto" || len(body.Tools) != 3 {
			t.Errorf("normal tool configuration mutated: %+v choice=%s", body.Tools, body.ToolChoice)
		}
		for _, item := range body.Input {
			if item["type"] == "web_search_call" {
				t.Error("compacted history still has hosted calls")
			}
		}
		return retryResponse(200, completeEvent), nil
	})
	if err := p.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Step(context.Background(), "continue", nil); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
}
