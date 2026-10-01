package test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

func TestStreamingOutputBounded(t *testing.T) {
	cfg := config.Defaults
	cfg.Auto, cfg.Sandbox, cfg.OutputCap = true, false, 101
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out, err := bash(t, cfg, "printf HEAD; head -c 33554432 /dev/zero; printf TAIL >&2")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "HEAD") || !strings.HasSuffix(out, "TAIL") || !strings.Contains(out, "[cut]") {
		t.Fatalf("head/tail missing: %q", out)
	}
	if len(out) != cfg.OutputCap+len("\n...[cut]...\n") {
		t.Fatalf("unexpected output length: %d", len(out))
	}
	// The command emits 32 MiB. CombinedOutput would allocate at least that
	// much; the streaming collector should allocate only small I/O buffers.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("output buffered excessively: allocated %d bytes", allocated)
	}
}

func TestOutputCapBoundaries(t *testing.T) {
	for _, cap := range []int{1, 2, 3, 4, 5} {
		for _, text := range []string{"", "a", "ab", "abc", "abcde", "abcdefghij"} {
			cfg := config.Defaults
			cfg.Auto, cfg.OutputCap = true, cap
			out, err := bash(t, cfg, "printf '%s' '"+text+"'")
			if err != nil {
				t.Fatal(err)
			}
			want := text
			if len(text) > cap {
				want = text[:cap-cap/2] + "\n...[cut]...\n" + text[len(text)-cap/2:]
			}
			if out != want {
				t.Errorf("cap=%d text=%q got=%q want=%q", cap, text, out, want)
			}
		}
	}
}

func TestInvalidBashLimits(t *testing.T) {
	for _, tc := range []struct{ cap, timeout int }{{0, 1}, {-1, 1}, {1, 0}, {1, -1}, {1, int(^uint(0) >> 1)}} {
		cfg := config.Defaults
		cfg.Auto, cfg.Sandbox, cfg.OutputCap, cfg.TimeoutSec = true, false, tc.cap, tc.timeout
		called := false
		approval := fakeApprover(func(context.Context, string, bool) (bool, error) { called = true; return true, nil })
		input, _ := json.Marshal(map[string]string{"command": "printf unexpected"})
		out, err := tools.Bash(cfg, approval).Run(ctx, input)
		if err == nil || out != "" || called {
			t.Errorf("cap=%d timeout=%d: out=%q err=%v approved=%v", tc.cap, tc.timeout, out, err, called)
		}
	}
}

func TestFailedCommandOutput(t *testing.T) {
	cfg := config.Defaults
	cfg.Auto = true
	out, err := bash(t, cfg, "printf failure >&2; exit 7")
	if err != nil || !strings.Contains(out, "failure") || !strings.Contains(out, "exit status 7") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
