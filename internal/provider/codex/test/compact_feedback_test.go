package test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider/codex"
)

func TestCompactStreamErrorsAreDiagnosticAndPreserveHistory(t *testing.T) {
	for _, tc := range []struct {
		name, event, want string
	}{
		{"nested", `{"type":"error","error":{"message":"Request rejected","code":"bad_request","type":"invalid_request_error"}}`, "codex: Request rejected; bad_request; invalid_request_error"},
		{"nested code", `{"type":"error","error":{"code":"context_length_exceeded"}}`, "codex: context_length_exceeded"},
		{"nested type", `{"type":"error","error":{"type":"server_error"}}`, "codex: server_error"},
		{"root", `{"type":"error","message":"Request rejected","code":"bad_request"}`, "codex: Request rejected; bad_request"},
		{"root message", `{"type":"error","message":"Request rejected"}`, "codex: Request rejected"},
		{"root code", `{"type":"error","code":"bad_request"}`, "codex: bad_request"},
		{"empty", `{"type":"error"}`, "codex: stream error"},
		{"empty nested", `{"type":"error","error":{}}`, "codex: stream error"},
		{"blank", `{"type":"error","message":"  ","code":" "}`, "codex: stream error"},
		{"empty nested root fallback", `{"type":"error","message":"Root reason","error":{}}`, "codex: Root reason"},
		{"failed", `{"type":"response.failed","response":{"error":{"message":"Request failed","code":"server_error","type":"internal_error"}}}`, "codex: Request failed; server_error; internal_error"},
		{"failed code", `{"type":"response.failed","response":{"error":{"code":"server_error"}}}`, "codex: server_error"},
		{"failed type", `{"type":"response.failed","response":{"error":{"type":"internal_error"}}}`, "codex: internal_error"},
		{"failed top-level fallback", `{"type":"response.failed","error":{"message":"Top-level failure","code":"server_error"},"response":{"error":null}}`, "codex: Top-level failure; server_error"},
		{"failed empty", `{"type":"response.failed","response":{"error":{}}}`, "codex: response failed"},
		{"failed null", `{"type":"response.failed","response":{"error":null}}`, "codex: response failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mockCodex(t, "")
			before := compactState(t, p)
			attempts := 0
			http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				attempts++
				return retryResponse(200, toolItem+"data: "+tc.event+"\n\n"), nil
			})
			err := p.Compact(retryContext(t))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if attempts != 1 {
				t.Fatalf("explicit error retried %d times", attempts)
			}
			after, err := p.Save()
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("history changed: %s (save error %v)", after, err)
			}
			// Also exercise Step's own rollback rather than just Compact's copy.
			attempts = 0
			calls, err := p.Step(retryContext(t), "", nil)
			if err == nil || err.Error() != tc.want || len(calls) != 0 || attempts != 1 {
				t.Fatalf("Step: calls=%v error=%v attempts=%d", calls, err, attempts)
			}
			after, err = p.Save()
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("Step retained partial output: %s (save error %v)", after, err)
			}
		})
	}
}

func TestCompactForwardsRetryNoticeWithoutSummaryLeak(t *testing.T) {
	mockCodex(t, "")
	out := &compactProgressOutput{retryOutput: &retryOutput{}}
	p := codex.New(config.Defaults, "", nil, out)
	compactState(t, p)
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return retryResponse(503, "temporarily unavailable"), nil
		}
		// Commentary must be captured as summary text too, not routed to chat.
		return retryResponse(200, `data: {"type":"response.output_item.added","item":{"type":"message","phase":"commentary"}}`+"\n\n"+summaryStream("Private handoff text.")), nil
	})
	if err := p.Compact(retryContext(t)); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(out.notices) != 1 || !strings.Contains(out.notices[0], "status 503: temporarily unavailable; retry 1 in") {
		t.Fatalf("attempts=%d notices=%q", attempts, out.notices)
	}
	if len(out.phases) != 2 || out.phases[0] != "compacting context" || out.phases[1] != "compacting context" {
		t.Fatalf("private summary progress not reported: %v", out.phases)
	}
	if out.replies.Len() != 0 || len(out.actions) != 0 {
		t.Fatalf("summary leaked: replies=%q actions=%q", out.replies.String(), out.actions)
	}
	after, err := p.Save()
	if err != nil || !bytes.Contains(after, []byte("Private handoff text.")) || bytes.Contains(after, []byte("temporarily unavailable")) || bytes.Contains(after, []byte("retry 1")) {
		t.Fatalf("summary/notice mixed: %s (save error %v)", after, err)
	}
}

func TestCompactRetryNoticeBeforeCancellationPreservesHistory(t *testing.T) {
	mockCodex(t, "")
	out := &retryOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := codex.New(config.Defaults, "", nil, &cancelNoticeOutput{retryOutput: out, cancel: cancel})
	before := compactState(t, p)
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return retryResponse(200, ""), nil // recoverable disconnect before any text
	})
	if err := p.Compact(ctx); err != context.Canceled {
		t.Fatalf("error=%v, want cancellation", err)
	}
	after, err := p.Save()
	if err != nil || !bytes.Equal(before, after) || len(out.notices) != 1 || out.replies.Len() != 0 {
		t.Fatalf("history=%s notices=%q replies=%q save error=%v", after, out.notices, out.replies.String(), err)
	}
}

type compactProgressOutput struct {
	*retryOutput
	phases []string
}

func (o *compactProgressOutput) Progress(phase string) { o.phases = append(o.phases, phase) }

type cancelNoticeOutput struct {
	*retryOutput
	cancel context.CancelFunc
}

func (o *cancelNoticeOutput) Notice(s string) {
	o.retryOutput.Notice(s)
	o.cancel()
}
