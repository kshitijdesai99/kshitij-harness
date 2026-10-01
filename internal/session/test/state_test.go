package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kh/internal/session"
)

func TestProviderEnvelopeIsOpaqueAndLegacyChatsStillLoad(t *testing.T) {
	if _, err := session.Encode("future-backend", []byte(" null ")); err == nil {
		t.Fatal("null state accepted")
	}
	state := []byte(`{"future_private_format":[1,2,3]}`)
	b, err := session.Encode("future-backend", state)
	if err != nil {
		t.Fatal(err)
	}
	r, err := session.Decode(b)
	if err != nil || r.Provider != "future-backend" || !bytes.Equal(r.State, state) {
		t.Fatalf("record=%+v err=%v", r, err)
	}
	legacy := []byte(`{"session":"old","map":"","input":[]}`)
	r, err = session.Decode(legacy)
	if err != nil || r.Provider != "codex" || !bytes.Equal(r.State, legacy) {
		t.Fatalf("legacy=%+v err=%v", r, err)
	}
	for _, invalid := range []string{`null`, `{}`, `{"version":2,"provider":"future","state":{}}`, `{"version":1,"provider":"future","state":null}`} {
		if _, err := session.Decode([]byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func TestAtomicSaveNeverExposesPartialJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	initial := []byte(`{"initial":true}`)
	if err := session.Save("chat", initial); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	errors := make(chan error, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			b, err := session.Load("chat")
			if err != nil {
				errors <- err
				return
			}
			if !json.Valid(b) {
				errors <- fmt.Errorf("partial JSON: %d bytes", len(b))
				return
			}
		}
	}()
	for i := 0; i < 30; i++ {
		b, _ := json.Marshal(map[string]string{"text": strings.Repeat(fmt.Sprint(i), 4096)})
		if err := session.Save("chat", b); err != nil {
			t.Error(err)
			break
		}
	}
	close(done)
	readers.Wait()
	select {
	case err := <-errors:
		t.Fatal(err)
	default:
	}
}

func TestSessionIDsCannotTraverseAndTemporaryFilesAreIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	for _, id := range []string{"", "../escape", "/absolute", ".hidden"} {
		if err := session.Save(id, nil); err == nil {
			t.Fatalf("accepted %q", id)
		}
		if _, err := session.Load(id); err == nil {
			t.Fatalf("loaded %q", id)
		}
	}
	if err := session.Save("chat", []byte("old")); err != nil {
		t.Fatal(err)
	}
	var folder string
	if err := filepath.WalkDir(filepath.Join(home, ".kh", "sessions"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "chat.json" {
			folder = filepath.Dir(path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if folder == "" {
		t.Fatal("session not saved")
	}
	if err := os.WriteFile(filepath.Join(folder, ".save-abandoned"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, "blocked.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := session.Save("blocked", []byte("new")); err == nil {
		t.Fatal("rename failure ignored")
	}
	if got := session.List(); len(got) != 1 || got[0] != "chat" {
		t.Fatal(got)
	}
	b, err := session.Load("chat")
	if err != nil || string(b) != "old" {
		t.Fatalf("previous save lost: %q %v", b, err)
	}
	info, err := os.Stat(filepath.Join(folder, "chat.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}
