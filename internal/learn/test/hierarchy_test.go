package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

// Gate model completion so a second writer deterministically changes a source
// after the summary snapshot, without sleeps, live models, or a live database.
func TestSummaryRejectsChangedSourceDuringModelCall(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "before", Text: "Before.", Source: "import"}, memory.Any); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	count := 0
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		count++
		if count == 1 {
			close(entered)
			<-release
			return `{"overview":"Stale context.","keys":[]}`
		}
		return `{"overview":"Fresh context.","keys":[]}`
	}), 1000)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("summary model never started")
	}
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "after", Text: "After.", Source: "import"}, memory.Any); err != nil {
		t.Fatal(err)
	}
	close(release)
	notices := strings.Join(l.Close(5*time.Second), "\n")
	sum, ok, err := s.Latest(ctx, repo, memory.SummaryKey)
	if err != nil || (ok && sum.Text == "Stale context.") {
		t.Fatalf("stale model output committed: %+v, %v; notices=%s", sum, err, notices)
	}
}
