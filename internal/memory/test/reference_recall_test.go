package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestReferenceRecallMatchesNamesAndAliasesWithoutFullPayload(t *testing.T) {
	s, ctx := open(t), context.Background()
	r := memory.Reference{Note: memory.Note{Repo: repo, Kind: "reference", Key: "victory-chime", Text: "Play a celebration sound.", Source: "you"}, Aliases: []string{"cool sound"}, Procedure: strings.Repeat("FULL PRIVATE PROCEDURE\n", 100), Assets: []memory.Asset{{Name: "play.sh", Interpreter: "sh", Source: "printf SCRIPT_NOT_IN_RECALL"}}}
	saved, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := memory.Budget{TopInstructions: 1, TopKnowledge: 1, TotalChars: 2000}
	for _, q := range []string{"Please trigger VICTORY-CHIME now", "play a cool sound"} {
		got, err := s.Recall(ctx, repo, q, nil, b)
		if err != nil || !strings.Contains(got, fmt.Sprintf("[memory #%d]", saved.Note.ID)) || !strings.Contains(got, "load") || strings.Contains(got, "FULL PRIVATE") || strings.Contains(got, "SCRIPT_NOT_IN_RECALL") {
			t.Fatalf("compact recall %q: %s %v", q, got, err)
		}
		seen := memory.Seen(state(t, got))
		again, err := s.Recall(ctx, repo, q, seen, b)
		if err != nil || strings.Contains(again, fmt.Sprintf("[memory #%d]", saved.Note.ID)) {
			t.Fatalf("duplicate recall %q %v", again, err)
		}
	}
	if ok, err := s.Forget(ctx, repo, saved.Note.Key, "", "you"); err != nil || !ok {
		t.Fatal(err)
	}
	got, err := s.Recall(ctx, repo, "play cool sound", nil, b)
	if err != nil || strings.Contains(got, "victory-chime") {
		t.Fatalf("forgotten recall: %s %v", got, err)
	}
}

func TestReferenceRecallTopicSearchAndBudget(t *testing.T) {
	s, ctx := open(t), context.Background()
	r, err := s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "laundry-help", Text: "Diagnose washer drainage faults.", Source: "you"}, Procedure: "Disconnect power before inspecting."}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Recall(ctx, repo, "washer drainage", nil, memory.Budget{TopKnowledge: 1, TotalChars: 1200})
	if err != nil || !strings.Contains(got, fmt.Sprintf("[memory #%d]", r.Note.ID)) {
		t.Fatalf("topic recall: %s %v", got, err)
	}
	tiny, err := s.Recall(ctx, repo, "laundry-help", nil, memory.Budget{TopKnowledge: 1, TotalChars: 80})
	if err != nil || len(tiny) > 80 {
		t.Fatalf("budget: %q %v", tiny, err)
	}
	foreign, err := s.Recall(ctx, "foreign/repo", "laundry-help", nil, memory.Budget{TopKnowledge: 1, TotalChars: 1200})
	if err != nil || strings.Contains(foreign, "laundry-help") {
		t.Fatalf("foreign scope: %s %v", foreign, err)
	}
}

func TestReferenceMentionsUsePhraseBoundaries(t *testing.T) {
	s, ctx := open(t), context.Background()
	_, err := s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "washer", Text: "Generic appliance guide.", Source: "you"}, Procedure: "Full guide."}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// With ordinary retrieval disabled, explicit named mentions are still usable,
	// but a substring inside a different word must not trigger named loading.
	b := memory.Budget{TotalChars: 1000}
	got, err := s.Recall(ctx, repo, "washer", nil, b)
	if err != nil || !strings.Contains(got, "washer") {
		t.Fatalf("explicit name: %s %v", got, err)
	}
	got, err = s.Recall(ctx, repo, "dishwasher", nil, b)
	if err != nil || got != "" {
		t.Fatalf("substring trigger: %s %v", got, err)
	}
}
