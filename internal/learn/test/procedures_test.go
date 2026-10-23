package test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

func procedureSteps() []learn.Step {
	return []learn.Step{{Name: "bash", Input: "build fixture", Output: "fixture ready"}, {Name: "bash", Input: "check fixture", Output: "fixture check passed"}}
}

func procedureAnswer(body string) string {
	b, _ := json.Marshal(map[string]any{"procedures": []any{map[string]any{"key": "fixture-check", "description": "Build and verify a reusable fixture.", "procedure": body, "context_summary": "A fixture workflow was demonstrated and verified.", "evidence": []any{map[string]any{"step": 0, "quote": "fixture ready"}, map[string]any{"step": 1, "quote": "fixture check passed"}}}}})
	return string(b)
}

func runProcedure(t *testing.T, s *memory.Store, steps []learn.Step, msg, body string, hook func()) time.Time {
	t.Helper()
	at := time.Now().UTC().Add(time.Minute)
	l := learn.New(s, repo, "owner", model(&calls{}, func(system, prompt string) string {
		if strings.Contains(system, "reusable procedures") {
			if hook != nil {
				hook()
			}
			return procedureAnswer(body)
		}
		if strings.Contains(system, "procedure replacement") {
			return `{"update":true,"evidence":"fixture check passed"}`
		}
		if strings.Contains(system, "always-loaded") {
			return `{"overview":"Fixture workflow.","keys":[]}`
		}
		return `{"save":[]}`
	}), 10000)
	l.FinishedAt(msg, "Fixture discussion", steps, "Verified.", at)
	l.Close(5 * time.Second)
	return at
}

func TestProcedureRejectsInvalidProposalsAndUnsafeUpdates(t *testing.T) {
	for _, name := range []string{"bad-key", "empty-context", "one-proof", "wrong-index", "kind-collision", "review-denied", "review-unproven", "review-failed", "stale-update"} {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := context.Background()
			var prior memory.Reference
			if strings.HasPrefix(name, "review-") || name == "stale-update" {
				var err error
				prior, err = s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "fixture-check", Text: "Fixture procedure", Source: "you"}, Procedure: "Build with --safe and verify the fixture."}, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "kind-collision" {
				if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: "fixture-check", Text: "Keep the fixture safe.", Source: "you"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			l := learn.New(s, repo, "owner", model(&calls{}, func(system, prompt string) string {
				if strings.Contains(system, "procedure replacement") {
					if !strings.Contains(prompt, prior.Procedure) {
						t.Error("update review omitted full existing procedure")
					}
					switch name {
					case "review-denied":
						return `{"update":false}`
					case "review-unproven":
						return `{"update":true,"evidence":"invented success"}`
					case "review-failed":
						return "not JSON"
					case "stale-update":
						s.Forget(ctx, repo, "fixture-check", "owner", "you")
						return `{"update":true,"evidence":"fixture check passed"}`
					}
				}
				if strings.Contains(system, "reusable procedures") {
					answer := procedureAnswer("A replacement that omits the safe flag.")
					switch name {
					case "bad-key":
						answer = strings.ReplaceAll(answer, "fixture-check", "unsafe\\nnotice")
					case "empty-context":
						answer = strings.ReplaceAll(answer, "A fixture workflow was demonstrated and verified.", "")
					case "one-proof":
						answer = strings.ReplaceAll(answer, `"step":1`, `"step":0`)
					case "wrong-index":
						answer = strings.ReplaceAll(answer, `"step":1`, `"step":99`)
					}
					return answer
				}
				if strings.Contains(system, "always-loaded") {
					return `{"overview":"Fixture.","keys":[]}`
				}
				return `{"save":[]}`
			}), 10000)
			l.FinishedAt("Verify fixture workflow.", "", procedureSteps(), "Verified.", time.Now().Add(time.Minute))
			l.Close(5 * time.Second)
			if prior.Note.ID != 0 {
				latest, _, _ := s.Latest(ctx, repo, "fixture-check")
				if name == "stale-update" {
					if !latest.Forgotten {
						t.Fatal("resurrected during review")
					}
				} else if latest.ID != prior.Note.ID {
					t.Fatalf("unsafe update saved: %+v", latest)
				}
			} else {
				refs, err := s.Current(ctx, repo, "reference")
				if err != nil || len(refs) != 0 {
					t.Fatalf("invalid proposal saved: %+v %v", refs, err)
				}
			}
		})
	}
}

func TestProcedureUpdateDoesNotDuplicateAliasMetadata(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	prior, err := s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "fixture-check", Text: "Fixture check.", Source: "you"}, Procedure: "Build fixture, then check.", Aliases: []string{"fixture"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	l := learn.New(s, repo, "owner", model(&calls{}, func(system, prompt string) string {
		switch {
		case strings.Contains(system, "reusable procedures"):
			return strings.Replace(procedureAnswer("Build fixture, then check the verified result."), strconv.Quote("Build and verify a reusable fixture."), strconv.Quote(prior.Note.Text), 1)
		case strings.Contains(system, "procedure replacement"):
			return `{"update":true,"evidence":"fixture check passed"}`
		case strings.Contains(system, "always-loaded"):
			return `{"overview":"Fixture.","keys":[]}`
		default:
			return `{"save":[]}`
		}
	}), 10000)
	l.FinishedAt("Verify fixture workflow.", "", procedureSteps(), "Verified.", time.Now().Add(time.Minute))
	l.Close(5 * time.Second)
	next, ok, err := s.LatestReference(ctx, repo, prior.Note.Key)
	if err != nil || !ok || next.Version != 2 || next.Note.Text != prior.Note.Text {
		t.Fatalf("alias metadata duplicated on update: %+v %v", next, err)
	}
}

func TestProcedureCreationAndProvenance(t *testing.T) {
	s := open(t)
	at := runProcedure(t, s, procedureSteps(), "Verify our reusable fixture workflow.", "1. Build fixture.\n2. Check fixture.", nil)
	r, ok, err := s.LatestReference(context.Background(), repo, "fixture-check")
	if err != nil || !ok {
		t.Fatalf("reference: %+v %v %v", r, ok, err)
	}
	if r.Procedure != "1. Build fixture.\n2. Check fixture." || r.Note.Source != "post-hook" || r.Note.Owner != "owner" || r.Note.ContextSummary == "" || !r.Note.StatedAt.Equal(at) {
		t.Fatalf("provenance/payload: %+v", r)
	}
	if r.Version != 1 || len(r.Assets) != 0 {
		t.Fatalf("unexpected version/assets: %+v", r)
	}
}

func TestProcedureRejectsUnsupportedOriginsAndEvidence(t *testing.T) {
	for _, name := range []string{"peer", "empty-query", "failure", "exit-status", "memory", "unsupported-quote"} {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			steps := procedureSteps()
			msg := "Verify the fixture workflow."
			switch name {
			case "peer":
				msg = "[from kh:worker] Verify fixture"
			case "empty-query":
				msg = ""
			case "failure":
				steps[0].Failed = true
			case "exit-status":
				steps[0].Output += "\nexit status 1"
			case "memory":
				steps[0].Name = "functions.memory"
			case "unsupported-quote":
				steps[0].Output = "unrelated output"
			}
			runProcedure(t, s, steps, msg, "Run a fixture check.", nil)
			ns, err := s.Current(context.Background(), repo, "reference")
			if err != nil || len(ns) != 0 {
				t.Fatalf("unsupported procedure saved: %+v %v", ns, err)
			}
		})
	}
}

func TestProcedureCASNoResurrectionAndVersions(t *testing.T) {
	for _, action := range []string{"update", "duplicate", "forgotten", "race"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			ctx := context.Background()
			r, err := s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "fixture-check", Text: "Fixture check.", Source: "you"}, Procedure: "Build fixture, then check.", Aliases: []string{"fixture"}, Assets: []memory.Asset{{Name: "fixture.sh", Interpreter: "sh", Source: "echo fixture"}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			body := "Build fixture, then check the verified result."
			if action == "duplicate" {
				body = r.Procedure
			}
			if action == "forgotten" {
				if _, err := s.Forget(ctx, repo, r.Note.Key, "owner", "you"); err != nil {
					t.Fatal(err)
				}
			}
			var hook func()
			if action == "race" {
				hook = func() { s.Forget(ctx, repo, r.Note.Key, "owner", "you") }
			}
			runProcedure(t, s, procedureSteps(), "Verify the fixture workflow.", body, hook)
			n, _, err := s.Latest(ctx, repo, r.Note.Key)
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "forgotten", "race":
				if !n.Forgotten {
					t.Fatalf("resurrected: %+v", n)
				}
			case "duplicate":
				if n.ID != r.Note.ID {
					t.Fatalf("rewritten identical procedure: %+v", n)
				}
			case "update":
				next, ok, err := s.Reference(ctx, repo, n.ID)
				if err != nil || !ok || next.Version != 2 || n.Overridden != r.Note.ID || next.ReferenceID != r.ReferenceID || len(next.Assets) != 1 || len(next.Aliases) != 1 {
					t.Fatalf("update: %+v %v", next, err)
				}
			}
		})
	}
}
