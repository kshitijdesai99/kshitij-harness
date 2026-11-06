package pluginhooks_test

import (
	"kh/internal/pluginhooks"
	"kh/internal/session"
	"testing"
)

func TestTranscriptAcceptsNativeSessionID(t *testing.T) {
	id := session.New()
	if _, err := pluginhooks.OpenTranscript(t.TempDir(), t.TempDir(), id); err != nil {
		t.Fatalf("native session ID %q rejected: %v", id, err)
	}
	for _, bad := range []string{"../escape", ".", "..", "/absolute", "nested/name"} {
		if _, err := pluginhooks.OpenTranscript(t.TempDir(), t.TempDir(), bad); err == nil {
			t.Fatalf("unsafe ID accepted: %q", bad)
		}
	}
}
