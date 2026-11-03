package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/provider"
	"kh/internal/session"
)

func TestArchiveAppendResumeAndSnapshotSeparation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := session.Save("chat", []byte(`{"keep":true}`)); err != nil {
		t.Fatal(err)
	}
	a, err := session.OpenArchive("chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.OpenArchive("chat"); err == nil {
		t.Fatal("concurrent writer accepted")
	}
	records := []session.ArchiveRecord{
		{Event: provider.Event{Type: "session_start"}, Origin: "new"},
		{Event: provider.Event{Type: "user_message", Text: "hello\nworld"}},
		{Event: provider.Event{Type: "tool_call", CallID: "c1", Input: json.RawMessage(`{"number":9007199254740993,"image_url":"data:image/png;base64,AAAA","nested":{"content_base64":"secret-binary","text":"data:image/png;base64,BBBB"}}`)}},
	}
	for _, r := range records {
		if err := a.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(session.Folder(), "chat.jsonl")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), "AAAA") || strings.Contains(string(original), "BBBB") || strings.Contains(string(original), "secret-binary") {
		t.Fatal("image/binary payload leaked")
	}
	if !strings.Contains(string(original), "9007199254740993") {
		t.Fatal("numeric argument changed")
	}
	info, _ := os.Stat(name)
	if info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	a, err = session.OpenArchive("chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Append(session.ArchiveRecord{Event: provider.Event{Type: "session_start"}, Origin: "resume"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(name)
	if !strings.HasPrefix(string(data), string(original)) {
		t.Fatal("append rewrote prior records")
	}
	var got []session.ArchiveRecord
	if err := session.ReadArchive(strings.NewReader(string(data)), func(r session.ArchiveRecord) error { got = append(got, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[1].Text != "hello\nworld" || got[3].Origin != "resume" {
		t.Fatalf("records: %+v", got)
	}
	ids := map[string]bool{}
	for _, r := range got {
		if r.ID == "" || ids[r.ID] || r.Timestamp.IsZero() || r.SessionID != "chat" || r.Version != 1 {
			t.Fatalf("identity: %+v", r)
		}
		ids[r.ID] = true
	}
	snapshot, _ := session.Load("chat")
	if string(snapshot) != `{"keep":true}` {
		t.Fatal("snapshot changed")
	}
	if list := session.List(); len(list) != 1 || list[0] != "chat" {
		t.Fatal(list)
	}
}

func TestArchiveTailRecoveryAndCorruption(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		wantError  bool
	}{
		{"partial", `{"unfinished":`, false}, {"valid-no-newline", `{"type":"old"}`, false}, {"corrupt-line", "broken\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			a, err := session.OpenArchive("s")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Append(session.ArchiveRecord{Event: provider.Event{Type: "user_message", Text: "keep"}}); err != nil {
				t.Fatal(err)
			}
			a.Close()
			name := filepath.Join(session.Folder(), "s.jsonl")
			prefix, _ := os.ReadFile(name)
			f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(tc.tail)
			f.Close()
			a, err = session.OpenArchive("s")
			if tc.wantError {
				if err == nil {
					a.Close()
					t.Fatal("corruption accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Append(session.ArchiveRecord{Event: provider.Event{Type: "user_message", Text: "next"}}); err != nil {
				t.Fatal(err)
			}
			a.Close()
			b, _ := os.ReadFile(name)
			if !strings.HasPrefix(string(b), string(prefix)) {
				t.Fatal("committed prefix changed")
			}
			if err := session.ReadArchive(strings.NewReader(string(b)), func(session.ArchiveRecord) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := session.ReadArchive(strings.NewReader("{\"type\":\"valid\"}\n{partial"), func(session.ArchiveRecord) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := session.ReadArchive(strings.NewReader("broken\n"), func(session.ArchiveRecord) error { return nil }); err == nil {
		t.Fatal("reader hid corruption")
	}
}

func TestArchiveLockDoesNotLeakIntoChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := session.OpenArchive("s")
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		a.Close()
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := session.OpenArchive("s")
	if err != nil {
		t.Fatalf("child retained archive lock: %v", err)
	}
	again.Close()
}

func TestArchiveMalformedInputFailureLatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := session.OpenArchive("s")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first := a.Append(session.ArchiveRecord{Event: provider.Event{Type: "tool_call", Input: json.RawMessage(`{}garbage`)}})
	if first == nil {
		t.Fatal("accepted malformed input")
	}
	if next := a.Append(session.ArchiveRecord{Event: provider.Event{Type: "user_message", Text: "next"}}); next != first || a.Err() != first {
		t.Fatal("write failure did not latch")
	}
}

func TestArchiveRejectsInvalidIDsAndSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := session.OpenArchive("../escape"); err == nil {
		t.Fatal("invalid id accepted")
	}
	os.MkdirAll(session.Folder(), 0o700)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("keep"), 0o600)
	if err := os.Symlink(outside, filepath.Join(session.Folder(), "s.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.OpenArchive("s"); err == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "keep" {
		t.Fatal("symlink target modified")
	}
}
