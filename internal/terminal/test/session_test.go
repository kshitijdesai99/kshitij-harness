package test

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"kh/internal/terminal"

	"github.com/chzyer/readline"
	"github.com/rivo/uniseg"
)

func TestSessionRowWidths(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 12, 34, 0, 0, time.UTC)
	id := "20260101-123400.123456789"
	for _, width := range []int{120, 80, 60, 43, 42, 30, 25, 24, 12, 3, 2, 1, 0, -1} {
		row := terminal.SessionRow(stamp, id, strings.Repeat("answer ", 30), width)
		if cells := uniseg.StringWidth(row); cells > max(0, width) {
			t.Errorf("width %d: %d cells in %q", width, cells, row)
		}
		if width >= len(id) && !strings.Contains(row, id) {
			t.Errorf("width %d lost exact ID: %q", width, row)
		}
		if width >= len(id) && width < 16+2+len(id) && strings.HasPrefix(row, "2026-01-01") {
			t.Errorf("width %d should omit saved time: %q", width, row)
		}
	}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, "2026-01-01 12:34  " + id + "  " + strings.Repeat("x", 32) + "..."},
		{42, id + "  " + strings.Repeat("x", 12) + "..."},
		{30, id + "  ..."},
		{25, id},
	} {
		if got := terminal.SessionRow(stamp, id, strings.Repeat("x", 100), tc.width); got != tc.want {
			t.Errorf("width %d: got %q want %q", tc.width, got, tc.want)
		}
	}
	row := terminal.SessionRow(stamp, "id", strings.Repeat("x", 100), 120)
	if want := "2026-01-01 12:34  id  " + strings.Repeat("x", 67) + "..."; row != want {
		t.Errorf("preview cap: got %q want %q", row, want)
	}
	if row := terminal.SessionRow(stamp, id, "text", 12); row != id[:9]+"..." {
		t.Errorf("extremely narrow ID strategy: %q", row)
	}
}

func TestSessionRowGraphemes(t *testing.T) {
	stamp := time.Time{}
	for _, cluster := range []string{"界", "e\u0301", "👩‍💻", "👨‍👩‍👧‍👦", "🇺🇸"} {
		for _, width := range []int{6, 7, 8, 9, 10, 30, 80, 120} {
			row := terminal.SessionRow(stamp, "id", strings.Repeat(cluster, 100), width)
			if !utf8.ValidString(row) || uniseg.StringWidth(row) > width {
				t.Errorf("cluster %q width %d: %q", cluster, width, row)
			}
			prefix := "id  "
			if strings.HasPrefix(row, "0001-01-01") {
				prefix = "0001-01-01 00:00  id  "
			}
			preview := strings.TrimSuffix(strings.TrimPrefix(row, prefix), "...")
			if strings.Trim(preview, ".") != "" && strings.ReplaceAll(preview, cluster, "") != "" {
				t.Errorf("split cluster %q width %d: %q", cluster, width, row)
			}
		}
	}
	// IDs also remain valid at a grapheme boundary when they must be truncated.
	if row := terminal.SessionRow(stamp, "界e\u0301👩‍💻XYZ", "", 6); row != "界e\u0301..." {
		t.Errorf("Unicode ID: %q", row)
	}
}

func TestSessionRowSanitizesControls(t *testing.T) {
	preview := "\x1b[31mred\x1b[0m\n\t" +
		"\x1b]0;hidden title\a" +
		"\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\ " +
		"\x1bPprivate payload\x1b\\" +
		"\u009b32mgreen\u009b0m " +
		"\u009dhidden\u009c" +
		"\b\x00safe\u202ereply \xffdone\x1b[31"
	row := terminal.SessionRow(time.Time{}, "\x1b[31mid\x1b[0m", preview, 120)
	if want := "0001-01-01 00:00  id  red link green safereply done"; row != want {
		t.Errorf("sanitized row: %q want %q", row, want)
	}
	if row := terminal.SessionRow(time.Time{}, "id", "[preview unavailable]", 120); !strings.HasSuffix(row, "[preview unavailable]") {
		t.Errorf("explicit unavailable preview: %q", row)
	}
}

func TestSessionWidthTTY(t *testing.T) {
	out, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no controlling terminal")
	}
	defer out.Close()
	width, _, err := readline.GetSize(int(out.Fd()))
	if err != nil || width <= 0 || !readline.IsTerminal(int(out.Fd())) {
		t.Skip("no usable TTY dimensions")
	}
	t.Setenv("COLUMNS", "1")
	if got := terminal.SessionWidth(out); got != width {
		t.Errorf("TTY width should override COLUMNS: got %d want %d", got, width)
	}
}

func TestSessionWidthFallback(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "redirected")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, tc := range []struct {
		columns string
		want    int
	}{{"", 120}, {"80", 80}, {"7", 7}, {"0", 120}, {"-10", 120}, {"garbage", 120}, {"999999999999999999999999", 120}, {"80x", 120}} {
		t.Setenv("COLUMNS", tc.columns)
		if got := terminal.SessionWidth(out); got != tc.want {
			t.Errorf("COLUMNS=%q: got %d want %d", tc.columns, got, tc.want)
		}
	}
}
