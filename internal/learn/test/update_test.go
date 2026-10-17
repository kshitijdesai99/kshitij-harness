package test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
	"kh/internal/provider"
)

func updateJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func updateReview(system string) bool {
	return strings.HasPrefix(system, "You review a proposed durable-memory replacement")
}
func updateSave(kind, key, text string) string {
	return updateJSON(map[string]any{"save": []any{map[string]string{"kind": kind, "key": key, "text": text}}})
}
func updateAnswer(text, quote string) string {
	return updateJSON(map[string]any{"update": true, "text": text, "evidence": quote})
}
func seedUpdate(t *testing.T, s *memory.Store, kind, text string) memory.Note {
	t.Helper()
	_, _, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: kind, Key: "review-rule", Text: text, Source: "you"}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	n, ok, err := s.Latest(context.Background(), repo, "review-rule")
	if err != nil || !ok {
		t.Fatalf("seed: %+v %v", n, err)
	}
	return n
}
func checkUpdate(t *testing.T, s *memory.Store, text string, versions int) {
	t.Helper()
	ns, err := s.History(context.Background(), repo, "review-rule")
	if err != nil || len(ns) != versions {
		t.Fatalf("history: %+v, %v; want %d versions", ns, err, versions)
	}
	n, ok, err := s.Latest(context.Background(), repo, "review-rule")
	if err != nil || !ok || n.Text != text {
		t.Fatalf("latest: %+v, %v; want %q", n, err, text)
	}
}

func TestUpdateParaphrasesKeepOneVersion(t *testing.T) {
	s, c := open(t), &calls{}
	original := "Always run independent read-only reviews before fixing concrete bugs."
	seedUpdate(t, s, "instruction", original)
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			return `{"update":false}`
		}
		if is(system, "lasting user instructions") || is(system, "gotchas") {
			return updateSave("instruction", "review-rule", "Review independently and read-only before fixing verified bugs.")
		}
		return `{"overview":"Review workflow.","keys":["review-rule"]}`
	}), 10000)
	l.Message("Remember to review independently before fixing verified bugs.")
	l.FinishedAt("Remember to review independently before fixing verified bugs.", "", nil, "I will review independently before fixing verified bugs.", time.Now())
	notices := strings.Join(l.Close(5*time.Second), "\n")
	checkUpdate(t, s, original, 1)
	reviews := 0
	for _, p := range c.prompts {
		if updateReview(p) {
			reviews++
		}
	}
	if reviews != 2 || strings.Contains(notices, "Replaced") {
		t.Fatalf("reviews=%d notices=%s", reviews, notices)
	}
}

func TestUpdateRecentContextCannotAuthorizeRoutineRequest(t *testing.T) {
	s, c := open(t), &calls{}
	original := "Run independent Claude and Codex reviews."
	seedUpdate(t, s, "instruction", original)
	quote := "Also exchange findings and verify acknowledgments."
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			return updateAnswer(original+" "+quote, quote)
		}
		if is(system, "lasting user instructions") {
			return updateSave("instruction", "review-rule", original+" "+quote)
		}
		return `{"overview":"Review workflow.","keys":[]}`
	}), 10000)
	l.MessageAt("c and p", quote, time.Now())
	notices := strings.Join(l.Close(5*time.Second), "\n")
	checkUpdate(t, s, original, 1)
	if !strings.Contains(notices, "existing note retained") {
		t.Fatalf("notices: %s", notices)
	}
}

func TestUpdateLongOriginalVisibleAndMerged(t *testing.T) {
	s, c := open(t), &calls{}
	command := "claude --model claude-opus-5-5 --effort medium --permission-mode auto --disallowedTools Edit,Write,NotebookEdit"
	original := strings.Repeat("Keep reviews independent and read-only. ", 7) + "Run `" + command + "`."
	seedUpdate(t, s, "instruction", original)
	addition := "Verify acknowledgments both ways."
	merged := original + " " + addition
	extractionVisible, reviewVisible := false, false
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			reviewVisible = strings.Contains(prompt, command)
			return updateAnswer(merged, addition)
		}
		if is(system, "lasting user instructions") {
			extractionVisible = strings.Contains(prompt, original)
			return updateSave("instruction", "review-rule", addition)
		}
		return `{"overview":"Review workflow.","keys":[]}`
	}), 10000)
	l.Message(addition)
	l.Close(5 * time.Second)
	checkUpdate(t, s, merged, 2)
	if !extractionVisible || !reviewVisible {
		t.Fatalf("full original not visible: extraction=%v review=%v", extractionVisible, reviewVisible)
	}
}

func TestUpdateUserCorrectionAccepted(t *testing.T) {
	s, c := open(t), &calls{}
	seedUpdate(t, s, "instruction", "Use Tailwind.")
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			return updateAnswer("Use plain CSS.", "Actually use plain CSS instead.")
		}
		if is(system, "lasting user instructions") {
			return updateSave("instruction", "review-rule", "Use plain CSS.")
		}
		return `{"overview":"CSS.","keys":[]}`
	}), 10000)
	l.Message("Actually use plain CSS instead.")
	l.Close(5 * time.Second)
	checkUpdate(t, s, "Use plain CSS.", 2)
}

func TestUpdateCommandLiteralChangesAreReviewed(t *testing.T) {
	for _, tc := range []struct{ name, old, revised, request string }{
		{"case", "Run `deploy --target=Prod`.", "Run `deploy --target=prod`.", "Use lowercase prod as the deployment target."},
		{"quoted spaces", "Run `printf '%s' 'a  b'`.", "Run `printf '%s' 'a b'`.", "Use one space inside the quoted argument."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := open(t), &calls{}
			seedUpdate(t, s, "instruction", tc.old)
			reviews := 0
			l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
				if updateReview(system) {
					reviews++
					return updateAnswer(tc.revised, tc.request)
				}
				if is(system, "lasting user instructions") {
					return updateSave("instruction", "review-rule", tc.revised)
				}
				return `{"overview":"Commands.","keys":[]}`
			}), 10000)
			l.Message(tc.request)
			l.Close(5 * time.Second)
			checkUpdate(t, s, tc.revised, 2)
			if reviews != 1 {
				t.Fatalf("literal change incorrectly normalized away: %d reviews", reviews)
			}
		})
	}
}

func TestUpdateInvalidReviewsKeepOriginal(t *testing.T) {
	const original = "Use plain CSS."
	const quote = "Use Tailwind instead."
	for _, tc := range []struct {
		name, response string
		fail           bool
	}{
		{name: "missing", response: `{}`},
		{name: "malformed", response: `{"update":`},
		{name: "error", fail: true},
		{name: "unsupported", response: updateAnswer("Use Tailwind.", "A nonexistent current quote.")},
		{name: "assistant-only", response: updateAnswer("Use Tailwind.", "Assistant recommends Tailwind.")},
		{name: "overlength", response: updateAnswer(strings.Repeat("x", memory.MaxText+1), quote)},
		{name: "secret", response: updateAnswer("Use api_key=privatevalue123.", quote)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := open(t), &calls{}
			seedUpdate(t, s, "instruction", original)
			base := model(c, func(system, prompt string) string {
				if updateReview(system) {
					return tc.response
				}
				if is(system, "gotchas") {
					return updateSave("instruction", "review-rule", "Use Tailwind.")
				}
				return `{"overview":"CSS.","keys":[]}`
			})
			factory := func(system string, out provider.Output) (provider.Provider, error) {
				if tc.fail && updateReview(system) {
					return nil, errors.New("review unavailable")
				}
				return base(system, out)
			}
			l := learn.New(s, repo, "", factory, 10000)
			l.FinishedAt(quote, "", nil, "Assistant recommends Tailwind.", time.Now())
			notices := strings.Join(l.Close(5*time.Second), "\n")
			checkUpdate(t, s, original, 1)
			if !strings.Contains(notices, "retained") && !strings.Contains(notices, "rejected") {
				t.Fatalf("no rejection notice: %s", notices)
			}
		})
	}
}

func TestUpdateToolEvidenceAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		step       learn.Step
		accepted   bool
	}{
		{"instruction-tool-only", "instruction", learn.Step{Name: "bash", Output: "Use SQLite instead."}, false},
		{"fact-success", "fact", learn.Step{Name: "bash", Output: "Use SQLite instead."}, true},
		{"failed-tool", "fact", learn.Step{Name: "bash", Output: "Use SQLite instead.", Failed: true}, false},
		{"memory-tool", "fact", learn.Step{Name: "memory", Output: "Use SQLite instead."}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := open(t), &calls{}
			original, revised := "Use PostgreSQL.", "Use SQLite instead."
			seedUpdate(t, s, tc.kind, original)
			l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
				if updateReview(system) {
					return updateAnswer(revised, revised)
				}
				if is(system, "gotchas") {
					return updateSave(tc.kind, "review-rule", revised)
				}
				return `{"overview":"Database.","keys":[]}`
			}), 10000)
			l.FinishedAt("Inspect the database.", "", []learn.Step{tc.step}, "Inspection complete.", time.Now())
			l.Close(5 * time.Second)
			if tc.accepted {
				checkUpdate(t, s, revised, 2)
			} else {
				checkUpdate(t, s, original, 1)
			}
		})
	}
}

func TestUpdateConcurrentWriteDuringReviewRejected(t *testing.T) {
	s, c := open(t), &calls{}
	seedUpdate(t, s, "instruction", "Use Tailwind.")
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			seedUpdate(t, s, "instruction", "Use Bootstrap.")
			return updateAnswer("Use plain CSS.", "Use plain CSS.")
		}
		if is(system, "lasting user instructions") {
			return updateSave("instruction", "review-rule", "Use plain CSS.")
		}
		return `{"overview":"CSS.","keys":[]}`
	}), 10000)
	l.Message("Use plain CSS.")
	notices := strings.Join(l.Close(5*time.Second), "\n")
	checkUpdate(t, s, "Use Bootstrap.", 2)
	if !strings.Contains(notices, memory.ErrStale.Error()) {
		t.Fatalf("notices: %s", notices)
	}
}

func TestUpdateNoReviewForDuplicateOrNewTopic(t *testing.T) {
	s, c := open(t), &calls{}
	seedUpdate(t, s, "instruction", "Use plain CSS.")
	l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
		if updateReview(system) {
			t.Error("unnecessary update review")
			return `{"update":false}`
		}
		if is(system, "lasting user instructions") {
			if strings.Contains(prompt, "New topic please") {
				return updateSave("instruction", "new-topic", "Run tests before commits.")
			}
			return updateSave("instruction", "review-rule", "  Use plain CSS.  ")
		}
		return `{"overview":"Workflow.","keys":[]}`
	}), 10000)
	l.Message("Repeat the CSS rule.")
	l.Message("New topic please")
	l.Close(5 * time.Second)
	checkUpdate(t, s, "Use plain CSS.", 1)
	extracts, summaries, reviews := 0, 0, 0
	for _, p := range c.prompts {
		switch {
		case updateReview(p):
			reviews++
		case is(p, "lasting user instructions"):
			extracts++
		default:
			summaries++
		}
	}
	if extracts != 2 || summaries != 1 || reviews != 0 {
		t.Fatalf("calls extraction=%d summary=%d review=%d", extracts, summaries, reviews)
	}
	n, ok, err := s.Latest(context.Background(), repo, "new-topic")
	if err != nil || !ok || n.Text != "Run tests before commits." {
		t.Fatalf("new topic: %+v %v", n, err)
	}
}
