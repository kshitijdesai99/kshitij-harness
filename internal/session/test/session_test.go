package test

import (
	"testing"
	"time"

	"kh/internal/session"
)

func TestSaveLoadLatest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := session.Latest(); err == nil {
		t.Error("no sessions yet, want an error")
	}
	a := session.New()
	time.Sleep(2 * time.Millisecond)
	b := session.New()
	session.Save(a, []byte("A"))
	session.Save(b, []byte("B"))

	if got, _ := session.Latest(); got != b {
		t.Errorf("latest = %q, want %q", got, b)
	}
	if got, _ := session.Load(a); string(got) != "A" {
		t.Errorf("load = %q", got)
	}
}
