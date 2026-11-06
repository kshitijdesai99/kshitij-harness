package ecosystem_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"kh/internal/ecosystem"
)

func TestResultFeedReusePaginationAndIsolation(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 10)
	a, old := warmTask(t, s, r, "result-worker")
	other := root(t, s, 5)
	foreign := claim(t, s, reserve(t, s, other, "foreign"))
	if err := s.PublishResult(ctx, foreign.ID, "foreign-turn", "not visible"); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResult(ctx, r.ID, "root-turn", "root excluded"); err != nil {
		t.Fatal(err)
	}
	// Reserve the next generation before the parent has polled the old result.
	next, err := s.ReserveTask(ctx, r.ID, a.ID, "next task\nPRIVATE_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, identity(t), next); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, identity(t), old, "stale", ecosystem.TaskTimings{}); !errors.Is(err, ecosystem.ErrState) {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, identity(t), next, "initial result", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, identity(t), next, "duplicate", ecosystem.TaskTimings{}); !errors.Is(err, ecosystem.ErrState) {
		t.Fatal(err)
	}
	first, err := s.ResultsAfter(ctx, r.ID, 0, 1)
	if err != nil || len(first) != 1 || first[0].TaskID != old.ID {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := s.ResultsAfter(ctx, r.ID, first[0].Seq, 1)
	if err != nil || len(second) != 1 || second[0].TaskID != next.ID || second[0].Generation != 2 || second[0].Conclusion != first[0].Conclusion {
		t.Fatalf("second %+v %v", second, err)
	}
	watermark, err := s.ResultWatermark(ctx, r.ID)
	if err != nil || watermark != second[0].Seq {
		t.Fatalf("watermark %d %v", watermark, err)
	}
	empty, err := s.ResultsAfter(ctx, r.ID, watermark, 100)
	if err != nil || len(empty) != 0 {
		t.Fatalf("after %+v %v", empty, err)
	}
	body, _ := json.Marshal(append(first, second...))
	if strings.Contains(string(body), "PRIVATE_SENTINEL") || strings.Contains(string(body), "start_token") {
		t.Fatalf("private data %s", body)
	}
	isolated, err := s.ResultsAfter(ctx, other.ID, 0, 100)
	if err != nil || len(isolated) != 1 || isolated[0].AgentID != foreign.ID {
		t.Fatalf("isolation %+v %v", isolated, err)
	}
}

func TestOrdinaryResultTurnsAndUnicodeBounds(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 5)
	a := claim(t, s, reserve(t, s, r, "ordinary"))
	for _, turn := range []string{"turn-1", "turn-1", "turn-2"} {
		if err := s.PublishResult(ctx, a.ID, turn, ""); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Repeat("世", 6000)
	if err := s.PublishResult(ctx, a.ID, "turn-3", text); err != nil {
		t.Fatal(err)
	}
	// Retrying an earlier turn cannot overwrite inspect's latest conclusion.
	if err := s.PublishResult(ctx, a.ID, "turn-1", "overwrite"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ResultsAfter(ctx, r.ID, 0, 100)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	for _, v := range rows[:2] {
		if v.Conclusion != "" || v.Truncated || v.Generation != 1 {
			t.Fatalf("empty %+v", v)
		}
	}
	bounded := rows[2]
	if !bounded.Truncated || len(bounded.Conclusion) > 16000 || !utf8.ValidString(bounded.Conclusion) || len(bounded.Conclusion) < 15997 {
		t.Fatalf("bound %+v", bounded)
	}
	if get(t, s, a.ID).Conclusion != bounded.Conclusion {
		t.Fatal("inspect conclusion lost")
	}
}
