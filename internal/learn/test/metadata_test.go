package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

func metadataSeed(t *testing.T, s *memory.Store, tier string, expiry time.Time) memory.Note {
	t.Helper()
	_, n, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: "instruction", Key: "metadata-rule", Text: "Preserve the exact command `check --all`.", Source: "you", Importance: tier, ExpiresAt: expiry}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMetadataOnlyCorrectionsReviewed(t *testing.T) {
	deadline := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, priorTier, tier, expiry, request string
		priorExpiry, wantExpiry                time.Time
	}{
		{"promote", "normal", "critical", "omitted", "Make this rule critical.", deadline, deadline},
		{"demote", "critical", "normal", "omitted", "This rule is normal priority now.", deadline, deadline},
		{"expire", "critical", "omitted", deadline.Format(time.RFC3339), "Expire this rule at 2099-01-01T00:00:00Z.", time.Time{}, deadline},
		{"clear expiry", "critical", "omitted", "", "Remove this rule's expiry.", deadline, time.Time{}},
		{"renew expired", "critical", "omitted", deadline.Format(time.RFC3339), "Renew this rule until 2099-01-01T00:00:00Z.", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), deadline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			prior := metadataSeed(t, s, tc.priorTier, tc.priorExpiry)
			proposal := map[string]any{"kind": "instruction", "key": prior.Key, "text": prior.Text, "metadata_evidence": tc.request}
			if tc.tier != "omitted" {
				proposal["importance"] = tc.tier
			}
			if tc.expiry != "omitted" {
				proposal["expires_at"] = tc.expiry
			}
			reviews := 0
			l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
				if updateReview(system) {
					reviews++
					if !strings.Contains(prompt, "Importance") || !strings.Contains(prompt, "ExpiresAt") {
						t.Error("review missing metadata")
					}
					return updateAnswer(prior.Text, tc.request)
				}
				if is(system, "lasting user instructions") {
					return updateJSON(map[string]any{"save": []any{proposal}})
				}
				return `{"overview":"Context only.","keys":[]}`
			}), 1000)
			l.Message(tc.request)
			l.Close(5 * time.Second)
			latest, ok, err := s.Latest(context.Background(), repo, prior.Key)
			wantTier := tc.tier
			if wantTier == "omitted" {
				wantTier = prior.Importance
			}
			if err != nil || !ok || latest.ID == prior.ID || latest.Text != prior.Text || latest.Importance != wantTier || !latest.ExpiresAt.Equal(tc.wantExpiry) || reviews != 1 {
				t.Fatalf("metadata correction: %+v err=%v reviews=%d", latest, err, reviews)
			}
			current, err := s.Current(context.Background(), repo, "instruction")
			if err != nil || len(current) != 1 || current[0].ID != latest.ID {
				t.Fatalf("renewed/corrected note not active: %+v err=%v", current, err)
			}
			found, err := s.Search(context.Background(), repo, "instruction", "check", 10)
			if err != nil || len(found) != 1 || found[0].ID != latest.ID {
				t.Fatalf("renewed/corrected note not searchable: %+v err=%v", found, err)
			}
		})
	}
}

func TestTextUpdatePreservesOmittedMetadata(t *testing.T) {
	s := open(t)
	deadline := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	prior := metadataSeed(t, s, memory.ImportanceCritical, deadline)
	request := "Also verify the exit code."
	text := prior.Text + " " + request
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if updateReview(system) {
			return updateAnswer(text, request)
		}
		if is(system, "lasting user instructions") {
			return updateSave("instruction", prior.Key, text)
		}
		return `{"overview":"Context.","keys":[]}`
	}), 1000)
	l.Message(request)
	l.Close(5 * time.Second)
	latest, _, _ := s.Latest(context.Background(), repo, prior.Key)
	if latest.Text != text || latest.Importance != prior.Importance || !latest.ExpiresAt.Equal(deadline) {
		t.Fatalf("metadata lost: %+v", latest)
	}
}

func TestUnsupportedMetadataDoesNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name, tier, expiry, quote, request, review string
		post                                       bool
	}{
		{name: "tool is not tier authority", tier: "critical", quote: "Make this critical.", request: "Inspect the build.", post: true},
		{name: "context is not authority", tier: "critical", quote: "Make this critical.", request: "Thanks."},
		{name: "review must support metadata", tier: "critical", quote: "Make this critical.", request: "Make this critical.", review: "not in this request"},
		{name: "invalid tier", tier: "urgent", quote: "Make this critical.", request: "Make this critical."},
		{name: "guessed expiry", expiry: "2099-01-01T00:00:00Z", quote: "Keep until tomorrow.", request: "Keep until tomorrow."},
		{name: "invalid expiry", expiry: "tomorrow", quote: "Keep until tomorrow.", request: "Keep until tomorrow."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			prior := metadataSeed(t, s, memory.ImportanceNormal, time.Time{})
			p := map[string]any{"kind": "instruction", "key": prior.Key, "text": prior.Text, "metadata_evidence": tc.quote}
			if tc.tier != "" {
				p["importance"] = tc.tier
			}
			if tc.expiry != "" {
				p["expires_at"] = tc.expiry
			}
			l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
				if updateReview(system) {
					quote := tc.review
					if quote == "" {
						quote = tc.quote
					}
					return updateAnswer(prior.Text, quote)
				}
				if is(system, "lasting user instructions") || is(system, "gotchas") {
					return updateJSON(map[string]any{"save": []any{p}})
				}
				return `{"overview":"Context.","keys":[]}`
			}), 1000)
			if tc.post {
				l.FinishedAt(tc.request, "", []learn.Step{{Name: "bash", Output: tc.quote}}, "Done.", time.Now())
			} else {
				l.MessageAt(tc.request, tc.quote, time.Now())
			}
			l.Close(5 * time.Second)
			latest, _, _ := s.Latest(context.Background(), repo, prior.Key)
			if latest.ID != prior.ID {
				t.Fatalf("unsupported metadata changed note: %+v", latest)
			}
		})
	}
}

func TestMetadataReviewCannotOverwriteConcurrentUserTier(t *testing.T) {
	s := open(t)
	prior := metadataSeed(t, s, memory.ImportanceNormal, time.Time{})
	request := "Make this rule critical."
	var concurrent memory.Note
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if updateReview(system) {
			var err error
			concurrent, err = s.SetImportance(context.Background(), prior.ID, memory.ImportanceNormal, false)
			if err != nil {
				t.Error(err)
			}
			return updateAnswer(prior.Text, request)
		}
		if is(system, "lasting user instructions") {
			return updateJSON(map[string]any{"save": []any{map[string]any{"kind": "instruction", "key": prior.Key, "text": prior.Text, "importance": "critical", "metadata_evidence": request}}})
		}
		return `{"overview":"Context.","keys":[]}`
	}), 1000)
	l.Message(request)
	l.Close(5 * time.Second)
	latest, _, _ := s.Latest(context.Background(), repo, prior.Key)
	if concurrent.ID == 0 || latest.ID != concurrent.ID || latest.Importance != memory.ImportanceNormal {
		t.Fatalf("concurrent user tier lost: %+v", latest)
	}
}

func TestNewCriticalRequiresCurrentUserMetadataEvidence(t *testing.T) {
	for _, supported := range []bool{false, true} {
		s := open(t)
		request := "Always verify signatures; this is critical."
		quote := "a tool said critical"
		if supported {
			quote = request
		}
		l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
			if is(system, "lasting user instructions") {
				return updateJSON(map[string]any{"save": []any{map[string]any{"kind": "instruction", "key": "signatures", "text": "Always verify signatures.", "importance": "critical", "metadata_evidence": quote}}})
			}
			return `{"overview":"Context.","keys":[]}`
		}), 1000)
		l.Message(request)
		l.Close(5 * time.Second)
		n, ok, err := s.Latest(context.Background(), repo, "signatures")
		if err != nil || ok != supported || (ok && n.Importance != memory.ImportanceCritical) {
			t.Fatalf("supported=%v note=%+v ok=%v err=%v", supported, n, ok, err)
		}
	}
}
