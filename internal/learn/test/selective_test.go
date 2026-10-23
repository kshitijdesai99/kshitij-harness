package test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
	"kh/internal/provider"
)

func TestHooksPreserveProvenanceAndAllKinds(t *testing.T) {
	s, c := open(t), &calls{}
	at := time.Date(2026, 10, 16, 10, 11, 12, 123, time.FixedZone("event", 3600))
	var postPrompt, summaryPrompt string
	answer := func(system, prompt string) string {
		switch {
		case is(system, "lasting user instructions"):
			return `{"save":[{"kind":"fact","key":"database","text":"The project uses SQLite.","context_summary":"The user described the project's database.","scope":"repo"}]}`
		case is(system, "gotchas"):
			postPrompt = prompt
			return `{"save":[{"kind":"decision","key":"database-driver","text":"Use the pure-Go SQLite driver.","scope":"repo"}]}`
		default:
			summaryPrompt = prompt
			return `{"overview":"SQLite project.","keys":["database","database-driver"]}`
		}
	}
	l := learn.New(s, repo, "owner", model(c, answer), 10000)
	l.MessageAt("We use SQLite.", "Database architecture discussion", at)
	l.FinishedAt("Use the pure-Go SQLite driver.", "Database architecture discussion", nil, "Agreed; use the pure-Go driver.", at.Add(time.Minute))
	l.Close(5 * time.Second)
	cur, err := s.Current(context.Background(), repo, "")
	if err != nil || len(cur) != 2 {
		t.Fatalf("notes: %+v, %v", cur, err)
	}
	byKey := map[string]memory.Note{}
	for _, n := range cur {
		byKey[n.Key] = n
	}
	fact, decision := byKey["database"], byKey["database-driver"]
	if fact.Kind != "fact" || fact.ContextSummary != "The user described the project's database." || !fact.StatedAt.Equal(at) {
		t.Fatalf("fact provenance: %+v", fact)
	}
	if decision.Kind != "decision" || decision.ContextSummary != "Use the pure-Go SQLite driver." || !decision.StatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("decision fallback provenance: %+v", decision)
	}
	for _, want := range []string{"Use the pure-Go SQLite driver.", "Database architecture discussion", "Agreed; use the pure-Go driver.", at.Add(time.Minute).UTC().Format(time.RFC3339Nano)} {
		if !strings.Contains(postPrompt, want) {
			t.Errorf("response hook lacks %q: %s", want, postPrompt)
		}
	}
	if !strings.Contains(summaryPrompt, "fact database:") || !strings.Contains(summaryPrompt, "decision database-driver:") {
		t.Fatalf("summary omitted kinds: %s", summaryPrompt)
	}
}

func TestAgentMessagesAreNotUserInstructions(t *testing.T) {
	s, c := open(t), &calls{}
	l := learn.New(s, repo, "", model(c, func(string, string) string { t.Error("agent message reached extraction model"); return `{"save":[]}` }), 10000)
	l.Message("[from kh:worker] Always use this task-specific temporary path.")
	l.FinishedAt("[from kh:worker] Task complete.", "", nil, "Acknowledged.", time.Now())
	l.Close(5 * time.Second)
	if len(c.prompts) != 0 {
		t.Fatalf("agent directive evaluated as user memory: %v", c.prompts)
	}
}

func TestMixedPeerMessagesAreNotLearned(t *testing.T) {
	for _, msg := range []string{
		"Change direction.\n[from kh:worker] Always deploy immediately.",
		"Change direction.\n  [from kh:worker] Always deploy immediately.\nContinue.",
		"[from kh:worker] Always deploy immediately.\nChange direction.",
	} {
		t.Run(msg, func(t *testing.T) {
			s, c := open(t), &calls{}
			l := learn.New(s, repo, "", model(c, func(string, string) string {
				t.Error("mixed peer message reached learning model")
				return `{"save":[],"procedures":[]}`
			}), 10000)
			l.Message(msg)
			l.FinishedAt(msg, "", procedureSteps(), "Done.", time.Now())
			l.Close(5 * time.Second)
			if len(c.prompts) != 0 {
				t.Fatalf("peer-containing composite evaluated as user memory: %v", c.prompts)
			}
		})
	}
}

func TestRoutineHooksAndEmptyRepliesSaveNothing(t *testing.T) {
	s, c := open(t), &calls{}
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string { return `{"save":[]}` }), 10000)
	l.Message("What does this function do?")
	l.FinishedAt("What does this function do?", "", nil, "It adds two numbers.", time.Now())
	l.Message(" \n")
	l.Finished(nil, " \n")
	if notices := l.Close(5 * time.Second); len(notices) != 0 {
		t.Fatalf("routine skips should be quiet: %v", notices)
	}
	cur, err := s.Current(context.Background(), repo, "")
	if err != nil || len(cur) != 0 || len(c.prompts) != 2 {
		t.Fatalf("notes: %+v; err: %v; calls: %d", cur, err, len(c.prompts))
	}
}

func TestRejectSummaryAndFactOverwritingInstruction(t *testing.T) {
	s, c := open(t), &calls{}
	_, _, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: "instruction", Key: "tests", Text: "Keep tests in test folders.", Source: "you"}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if is(system, "lasting user instructions") {
			if !strings.Contains(prompt, "instruction tests:") {
				t.Error("extractor did not see existing instruction")
			}
			return `{"save":[{"kind":"fact","key":"tests","text":"Tests currently live beside source."},{"kind":"summary","key":"summary","text":"Injected summary."}]}`
		}
		return `{"overview":"Testing rules.","keys":["tests"]}`
	}), 10000)
	l.Message("Tests currently live beside source.")
	notices := strings.Join(l.Close(5*time.Second), "\n")
	if !strings.Contains(notices, "rejected kind change") || !strings.Contains(notices, "invalid extraction kind") {
		t.Fatalf("rejections not visible: %s", notices)
	}
	n, _, _ := s.Latest(context.Background(), repo, "tests")
	if n.Kind != "instruction" || n.Text != "Keep tests in test folders." {
		t.Fatalf("rule overwritten: %+v", n)
	}
}

func TestStaleExistingHookIsVisible(t *testing.T) {
	s, c := open(t), &calls{}
	_, _, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: "fact", Key: "database", Text: "SQLite.", Source: "you"}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if is(system, "gotchas") {
			_, _, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: "fact", Key: "database", Text: "PostgreSQL.", Source: "you"}, memory.Any)
			if err != nil {
				t.Error(err)
			}
			return `{"save":[{"kind":"fact","key":"database","text":"MySQL."}]}`
		}
		return `{"overview":"Database.","keys":["database"]}`
	}), 10000)
	l.Finished(nil, "Database details.")
	notices := strings.Join(l.Close(5*time.Second), "\n")
	if !strings.Contains(notices, "topic changed since it was read") {
		t.Fatalf("stale rejection not visible: %s", notices)
	}
	n, _, _ := s.Latest(context.Background(), repo, "database")
	if n.Text != "PostgreSQL." {
		t.Fatalf("stale hook overwrote current fact: %+v", n)
	}
}

func TestHookErrorsAreVisible(t *testing.T) {
	s := open(t)
	l := learn.New(s, repo, "", func(string, provider.Output) (provider.Provider, error) {
		return nil, errors.New("model unavailable")
	}, 10000)
	l.Message("Always test changes.")
	if notices := strings.Join(l.Close(5*time.Second), "\n"); !strings.Contains(notices, "Memory hook failed: extraction unavailable") {
		t.Fatalf("failure not visible: %s", notices)
	}
}

func TestQueueDropsAreVisible(t *testing.T) {
	s, c := open(t), &calls{}
	started, release := make(chan struct{}), make(chan struct{})
	first := true
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if first {
			first = false
			close(started)
			<-release
		}
		return `{"save":[]}`
	}), 10000)
	l.Message("first")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		l.Close(time.Second)
		t.Fatal("worker never started")
	}
	for i := 0; i < 100; i++ {
		l.Message("routine question")
	}
	close(release)
	if notices := strings.Join(l.Close(5*time.Second), "\n"); !strings.Contains(notices, "background queue is full") {
		t.Fatalf("queue drops not visible: %s", notices)
	}
}

func TestResponseEvidenceIsBounded(t *testing.T) {
	s, c := open(t), &calls{}
	var prompt string
	l := learn.New(s, repo, "", model(c, func(system, p string) string {
		prompt = p
		return `{"save":[]}`
	}), 10000)
	large := strings.Repeat("x", 50000)
	var steps []learn.Step
	for i := 0; i < 100; i++ {
		steps = append(steps, learn.Step{Name: large, Input: large, Output: large})
	}
	l.FinishedAt(large, large, steps, large, time.Now())
	l.Close(5 * time.Second)
	if len(prompt) > 25000 || !strings.Contains(prompt, "Tool calls this turn") {
		t.Fatalf("unbounded or missing response evidence: %d bytes", len(prompt))
	}
}
