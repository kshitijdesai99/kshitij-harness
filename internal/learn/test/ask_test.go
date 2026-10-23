package test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/learn"
	"kh/internal/provider"
)

func TestAskRetriesMalformedResponsesWithoutPartialResults(t *testing.T) {
	for _, first := range []string{"", "not JSON", "null", `{"save":["partial"],"count":"wrong type"}`} {
		t.Run(first, func(t *testing.T) {
			n := 0
			m := model(&calls{}, func(string, string) string {
				n++
				if n == 1 {
					return first
				}
				return `{"count":2}`
			})
			var out struct {
				Save  []string
				Count int
			}
			if err := learn.Ask(context.Background(), m, "system", "prompt", &out); err != nil {
				t.Fatal(err)
			}
			if n != 2 || out.Count != 2 || len(out.Save) != 0 {
				t.Fatalf("calls=%d out=%+v", n, out)
			}
		})
	}
}

func TestAskFailureCategoriesAreSafeAndBounded(t *testing.T) {
	for _, response := range []string{"", "private secret response", "[]"} {
		n := 0
		m := model(&calls{}, func(string, string) string { n++; return response })
		var out struct{ Save []string }
		err := learn.Ask(context.Background(), m, "system", "prompt", &out)
		if err == nil || n != 2 || !strings.Contains(err.Error(), "after retry") || strings.Contains(err.Error(), "private") {
			t.Fatalf("calls=%d error=%v", n, err)
		}
	}
	setupCalls := 0
	secret := errors.New("private token=never-print-this")
	m := func(string, provider.Output) (provider.Provider, error) { setupCalls++; return nil, secret }
	var out struct{}
	err := learn.Ask(context.Background(), m, "", "", &out)
	if err == nil || err.Error() != "model setup failed" || !errors.Is(err, secret) || setupCalls != 1 {
		t.Fatalf("calls=%d err=%v", setupCalls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = learn.Ask(ctx, m, "", "", &out)
	if !errors.Is(err, context.Canceled) || err.Error() != "canceled" || setupCalls != 1 {
		t.Fatalf("cancellation: %v calls=%d", err, setupCalls)
	}
}

type hookStepFailure struct {
	provider.Provider
	err   error
	calls []provider.Call
}

func (p hookStepFailure) Step(context.Context, string, []provider.Result) ([]provider.Call, error) {
	return p.calls, p.err
}

func TestAskDoesNotRetryProviderFailureOrToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		calls []provider.Call
		want  string
	}{
		{name: "provider", err: errors.New("sensitive provider error"), want: "provider request failed"},
		{name: "timeout", err: context.DeadlineExceeded, want: "deadline exceeded"},
		{name: "permission", err: os.ErrPermission, want: "local permission denied"},
		{name: "tools", calls: []provider.Call{{Name: "bash"}}, want: "unexpected tool call"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			m := func(string, provider.Output) (provider.Provider, error) {
				n++
				return hookStepFailure{err: tc.err, calls: tc.calls}, nil
			}
			var out struct{}
			err := learn.Ask(context.Background(), m, "", "", &out)
			if err == nil || err.Error() != tc.want || n != 1 {
				t.Fatalf("calls=%d err=%v", n, err)
			}
		})
	}
}

// Opt-in uses the configured provider but no real chat history or memory writes.
func TestLocalMemoryHookExtraction(t *testing.T) {
	if os.Getenv("KH_TEST_LIVE_HOOK") != "1" {
		t.Skip("set KH_TEST_LIVE_HOOK=1 to test configured provider")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := func(system string, out provider.Output) (provider.Provider, error) {
		c := cfg
		c.System = system
		c.WebSearch = false
		c.Effort = "low"
		if c.MemoryModel != "" && c.MemoryModel != "off" {
			c.Model = c.MemoryModel
		}
		return backend.New(c, "", nil, out)
	}
	s := open(t)
	l := learn.New(s, repo, "test", m, 10000)
	l.MessageAt("Always put Go tests in test subdirectories.", "Testing memory extraction.", time.Now())
	notices := l.Close(150 * time.Second)
	for _, n := range notices {
		if strings.Contains(n, "failed") {
			t.Fatalf("hook diagnostic: %s", n)
		}
	}
	notes, err := s.Current(context.Background(), repo, "instruction")
	if err != nil || len(notes) == 0 {
		t.Fatalf("no extracted instruction: notes=%d err=%v notices=%v", len(notes), err, notices)
	}
	t.Logf("live extraction succeeded: %d instruction(s)", len(notes))
}
