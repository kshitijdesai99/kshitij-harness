package test

import (
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestHierarchyDiagnosticBoundsCountsAndRetainsIDs(t *testing.T) {
	trace := memory.RecallTrace{
		CriticalConsideredIDs: []int64{1, 2, 3}, CriticalSelectedIDs: []int64{2, 3},
		CriticalPresentIDs: []int64{2}, CriticalOverflowIDs: []int64{1},
		ProcedureSelectedIDs: []int64{4}, ProcedureLoadedIDs: []int64{4}, ProcedureOverflowIDs: []int64{5},
	}
	compact, full := trace.Summary(nil), trace.Diagnostic(nil)
	for _, want := range []string{"critical: considered=3, selected=2, present=1, overflow=1", "procedure: selected=1, loaded=1, overflow=1"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact missing %q: %s", want, compact)
		}
	}
	for _, want := range []string{"critical: considered=[1 2 3], selected=[2 3], present=[2], overflow=[1]", "procedure: selected=[4], loaded=[4], overflow=[5]"} {
		if !strings.Contains(full, want) {
			t.Fatalf("detail missing %q: %s", want, full)
		}
	}
	if len(compact) > 1024 {
		t.Fatal("unbounded summary", compact)
	}
}
