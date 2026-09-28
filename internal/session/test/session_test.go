package test

import (
	"os"
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

func TestList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session.Save("b", nil)
	session.Save("a", nil)
	if got := session.List(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("list = %v, want [a b]", got)
	}
}

func TestPerFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, b := t.TempDir(), t.TempDir()
	os.Chdir(a)
	session.Save("x", nil)
	os.Chdir(b)
	if got := session.List(); len(got) != 0 {
		t.Errorf("folder b sees folder a's sessions: %v", got)
	}
	os.Chdir(a)
	if got := session.List(); len(got) != 1 {
		t.Errorf("folder a lost its session: %v", got)
	}
}
