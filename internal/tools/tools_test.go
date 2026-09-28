package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestIsSafe(t *testing.T) {
	for c, want := range map[string]bool{
		"rg foo | head":     true,
		"sed -n 1,20p a.go": true,
		"git diff":          true,
		"sed -i s/a/b/ f":   false,
		"cat a > b":         false,
		"ls; rm -rf x":      false,
		"find . -delete":    false,
		"git push":          false,
		"rm x":              false,
	} {
		if isSafe(c) != want {
			t.Errorf("isSafe(%q) = %v", c, !want)
		}
	}
}

func TestEdit(t *testing.T) {
	os.Chdir(t.TempDir())
	ctx := context.Background()
	if _, err := Edit.Run(ctx, []byte(`{"path":"a.txt","old":"","new":"hi there"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Edit.Run(ctx, []byte(`{"path":"a.txt","old":"","new":"x"}`)); err == nil {
		t.Error("create over existing file should fail")
	}
	if _, err := Edit.Run(ctx, []byte(`{"path":"a.txt","old":"there","new":"kh"}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "hi kh" {
		t.Errorf("got %q", b)
	}
	if _, err := Edit.Run(ctx, []byte(`{"path":"../x","old":"","new":"x"}`)); err == nil {
		t.Error("path outside project should fail")
	}
}

func TestBashTimeoutAndCap(t *testing.T) {
	out, _ := run(context.Background(), "yes | head -c 50000")
	if len(out) > 21000 || !strings.Contains(out, "[cut]") {
		t.Errorf("output not capped: %d bytes", len(out))
	}
}
