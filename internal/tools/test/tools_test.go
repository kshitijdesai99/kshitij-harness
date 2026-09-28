// Tests use only the public tools, exactly as the model calls them.
package test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

var ctx = context.Background()

func bash(t *testing.T, cfg config.Config, command string) (string, error) {
	in, _ := json.Marshal(map[string]string{"command": command})
	return tools.Bash(cfg).Run(ctx, in)
}

// Unsafe commands ask y/n; stdin is empty in tests, so they get "user said no".
func TestSafeList(t *testing.T) {
	os.Chdir(t.TempDir())
	os.WriteFile("a.go", []byte("x\n"), 0o644)
	for c, safe := range map[string]bool{
		"cat a.go | head":    true,
		"sed -n 1,20p a.go":  true,
		"sed -i s/a/b/ a.go": false,
		"cat a.go > b":       false,
		"ls; rm -rf x":       false,
		"find . -delete":     false,
		"git push":           false,
		"rm a.go":            false,
	} {
		_, err := bash(t, config.Defaults, c)
		if asked := err != nil && err.Error() == "user said no"; asked == safe {
			t.Errorf("%q: safe=%v but asked=%v", c, safe, asked)
		}
	}
}

func TestOutputCap(t *testing.T) {
	cfg := config.Defaults
	cfg.Yes = true
	out, _ := bash(t, cfg, "yes | head -c 50000")
	if len(out) > cfg.OutputCap+100 || !strings.Contains(out, "[cut]") {
		t.Errorf("output not capped: %d bytes", len(out))
	}
}

func TestEdit(t *testing.T) {
	os.Chdir(t.TempDir())
	edit := func(js string) error { _, err := tools.Edit.Run(ctx, []byte(js)); return err }

	if err := edit(`{"path":"a.txt","old":"","new":"hi there"}`); err != nil {
		t.Fatal(err)
	}
	if edit(`{"path":"a.txt","old":"","new":"x"}`) == nil {
		t.Error("create over existing file should fail")
	}
	if err := edit(`{"path":"a.txt","old":"there","new":"kh"}`); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "hi kh" {
		t.Errorf("got %q", b)
	}
	if edit(`{"path":"../x","old":"","new":"x"}`) == nil {
		t.Error("path outside project should fail")
	}
}
