package test

import (
	"context"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

func TestExpiredTextUpdateDoesNotInventRenewal(t *testing.T) {
	s := open(t)
	deadline := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
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
	n, _, _ := s.Latest(context.Background(), repo, prior.Key)
	if n.Text != text || !n.ExpiresAt.Equal(deadline) || n.Importance != prior.Importance {
		t.Fatalf("expiry lost: %+v", n)
	}
	current, err := s.Current(context.Background(), repo, "instruction")
	if err != nil || len(current) != 0 {
		t.Fatalf("omitted expiry revived note: %+v %v", current, err)
	}
	found, err := s.Search(context.Background(), repo, "instruction", "check", 10)
	if err != nil || len(found) != 0 {
		t.Fatalf("omitted expiry revived search: %+v %v", found, err)
	}
}
