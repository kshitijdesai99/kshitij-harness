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

func TestCommentaryAndStatisticsRedirected(t *testing.T) {
	var out, err strings.Builder
	r := terminal.NewRenderer(&out, &err)
	r.Commentary("Checking **files**.\n")
	r.Reply("Final answer.\n")
	r.Statistics("(10 tokens)")
	if out.String() != "Checking **files**.\nFinal answer.\n" || err.String() != "(10 tokens)\n" {
		t.Fatalf("out=%q err=%q", out.String(), err.String())
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
