package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

func TestSummaryListingCriticalFirstFairNewestAndReportsOmissions(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	// Dense older one-kind notes used to consume the entire oldest-first 12k.
	for i := 0; i < 45; i++ {
		if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: fmt.Sprintf("old-%02d", i), Text: strings.Repeat("dense context ", 45), Source: "import"}, memory.Any); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []memory.Note{
		{Repo: repo, Kind: "instruction", Key: "exact-critical", Text: "Run `check --target=Prod` exactly.", Source: "you", Importance: memory.ImportanceCritical},
		{Repo: memory.Global, Kind: "decision", Key: "global-newest", Text: "Global fresh context.", Source: "you"},
		{Repo: repo, Kind: "gotcha", Key: "repo-newest", Text: "Repo fresh context.", Source: "you"},
		{Repo: repo, Kind: "fact", Key: "fact-newest", Text: "Newest dense-kind context.", Source: "you"},
	} {
		if _, _, err := s.Add(ctx, n, memory.Any); err != nil {
			t.Fatal(err)
		}
	}
	// A later insertion/classification of an old statement is not fresh context.
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "old-statement-late-insert", Text: strings.Repeat("old context ", 50), Source: "import", StatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, memory.Any); err != nil {
		t.Fatal(err)
	}
	var listing string
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string { listing = prompt; return `{"overview":"Context.","keys":[]}` }), 1000)
	l.Close(5 * time.Second)
	for _, want := range []string{"instruction exact-critical: Run `check --target=Prod` exactly.", "decision global-newest:", "gotcha repo-newest:", "fact fact-newest:", "(omitted ", "of 50 canonical notes)"} {
		if !strings.Contains(listing, want) {
			t.Errorf("listing omitted %q: %s", want, listing)
		}
	}
	if len(listing) > 12050 {
		t.Fatalf("unbounded listing: %d", len(listing))
	}
	if strings.Index(listing, "instruction exact-critical:") > strings.Index(listing, "decision global-newest:") {
		t.Fatal("critical note not first")
	}
	if strings.Contains(listing, "(omitted 0 ") || strings.Contains(listing, "fact old-00:") || strings.Contains(listing, "fact old-statement-late-insert:") {
		t.Fatal("oldest context starved newer notes")
	}
}

func TestSourceLessLegacySummaryRebuiltFromCanonicalOnly(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, n := range []memory.Note{
		{Repo: repo, Kind: "fact", Key: "canonical", Text: "Canonical exact context.", Source: "import"},
		{Repo: repo, Kind: "summary", Key: memory.SummaryKey, Text: "Old generated gist never a source.", Source: "summary"},
	} {
		if _, _, err := s.Add(ctx, n, memory.Any); err != nil {
			t.Fatal(err)
		}
	}
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if strings.Contains(prompt, "Old generated gist") || !strings.Contains(prompt, "Canonical exact context.") {
			t.Errorf("summary sources are not canonical-only: %s", prompt)
		}
		if !strings.Contains(system, "nonauthoritative CONTEXT") {
			t.Error("gist prompt lost authority fence")
		}
		return `{"overview":"Fresh contextual gist.","keys":[]}`
	}), 1000)
	l.Close(5 * time.Second)
	n, ok, err := s.Summary(ctx, repo)
	rev, _ := s.SourceRevision(ctx, repo)
	if err != nil || !ok || n.Text != "Fresh contextual gist." || n.SourceRevision != rev {
		t.Fatalf("legacy summary not rebuilt: %+v %v", n, err)
	}
}

func TestStaleSummaryRequeuesOnceWithoutHotLoop(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "source", Text: "Source.", Source: "import"}, memory.Any); err != nil {
		t.Fatal(err)
	}
	second := make(chan struct{})
	count := 0
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		count++
		if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: fmt.Sprintf("change-%d", count), Text: "Changed source.", Source: "import"}, memory.Any); err != nil {
			t.Error(err)
		}
		if count == 2 {
			close(second)
		}
		return `{"overview":"Stale every time.","keys":[]}`
	}), 1000)
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("stale rebuild was not requeued")
	}
	notices := strings.Join(l.Close(5*time.Second), "\n")
	if count != 2 || strings.Count(notices, "notes changed during rebuild") != 2 {
		t.Fatalf("unbounded retry/missing stale guard: calls=%d notices=%s", count, notices)
	}
	if n, ok, _ := s.Latest(ctx, repo, memory.SummaryKey); ok {
		t.Fatalf("stale output committed: %+v", n)
	}
}

func TestSummaryConcurrentCloseAndStaleRetry(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := open(t)
		ctx := context.Background()
		s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "source", Text: "Before.", Source: "import"}, memory.Any)
		entered, release := make(chan struct{}), make(chan struct{})
		first := true
		l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
			if first {
				first = false
				close(entered)
				<-release
			}
			return `{"overview":"Context.","keys":[]}`
		}), 1000)
		<-entered
		s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "other", Text: "After.", Source: "import"}, memory.Any)
		done := make(chan struct{})
		go func() { l.Close(5 * time.Second); close(done) }()
		close(release)
		select {
		case <-done:
			l.Close(time.Second) // repeated close is safe after an in-job retry
		case <-time.After(5 * time.Second):
			t.Fatal("close/requeue deadlocked")
		}
	}
}
