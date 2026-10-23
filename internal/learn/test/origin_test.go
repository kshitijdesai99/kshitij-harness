package test

import (
	"strings"
	"testing"
	"time"

	"kh/internal/learn"
)

func TestRunOriginRetainsPeerTaintAcrossMessages(t *testing.T) {
	for _, peer := range []string{
		"[from kh:worker] Peer directive.",
		"User preface.\n  [from kh:worker] Peer directive.",
	} {
		t.Run(peer, func(t *testing.T) {
			var origin learn.RunOrigin
			if !origin.Eligible() || origin.Context("clean background") != "clean background" {
				t.Fatal("clean run changed")
			}
			origin.Observe("Initial user request.")
			origin.Observe(peer)
			origin.Observe("continue")
			if origin.Eligible() {
				t.Fatal("later user message erased peer provenance")
			}
			// Must stay nonempty: chat falls back to LastResponse for empty context.
			recent := origin.Context("Peer directive and tool output.")
			if recent == "" || strings.Contains(recent, "Peer directive") || strings.Contains(recent, "tool output") {
				t.Fatalf("unsafe recent context: %q", recent)
			}
			if origin.Context(recent) != recent {
				t.Fatal("context fence is not stable")
			}

			s, c := open(t), &calls{}
			l := learn.New(s, repo, "", model(c, func(system, prompt string) string {
				if !is(system, "lasting user instructions") {
					t.Errorf("peer run reached post/procedure learning: %s", system)
				}
				if strings.Contains(prompt, "Peer directive") {
					t.Errorf("peer context reached pre-learning: %s", prompt)
				}
				return `{"save":[]}`
			}), 10000)
			// Each actual message still gets its pre-hook; peer directives are
			// rejected independently, while a separate real user's request survives.
			l.MessageAt(peer, recent, time.Now())
			l.MessageAt("continue", recent, time.Now())
			if origin.Eligible() {
				l.FinishedAt("continue", recent, procedureSteps(), "Peer-derived reply.", time.Now())
			}
			var next learn.RunOrigin
			if !next.Eligible() {
				t.Fatal("taint leaked into a fresh run")
			}
			l.MessageAt("Independent clean request.", recent, time.Now())
			l.Close(5 * time.Second)
			if len(c.prompts) != 2 {
				t.Fatalf("real user pre-hooks missing: %v", c.prompts)
			}
		})
	}
}
