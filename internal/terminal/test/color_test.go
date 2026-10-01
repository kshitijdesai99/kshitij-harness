package test

import (
	"os"
	"strings"
	"testing"

	"kh/internal/terminal"
)

func TestRedirectedOutputIsPlain(t *testing.T) {
	var w strings.Builder
	if got := terminal.Color(&w, terminal.Query, "hello"); got != "hello" {
		t.Fatalf("redirected output contains color: %q", got)
	}
}

func TestNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := terminal.Color(os.Stdout, terminal.Response, "hello"); got != "hello" {
		t.Fatalf("NO_COLOR ignored: %q", got)
	}
}

func TestDumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if got := terminal.Color(os.Stdout, terminal.Query, "hello"); got != "hello" {
		t.Fatalf("dumb terminal contains color: %q", got)
	}
}
