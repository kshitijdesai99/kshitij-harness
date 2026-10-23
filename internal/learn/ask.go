package learn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
)

// hookError exposes only a fixed category. Provider errors and model responses
// can contain credentials or private conversation text and must not reach notices.
type hookError struct {
	reason string
	cause  error
}

func (e *hookError) Error() string { return e.reason }
func (e *hookError) Unwrap() error { return e.cause }

func requestError(ctx context.Context, stage string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		stage = "deadline exceeded"
	} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		stage = "canceled"
	} else if errors.Is(err, os.ErrPermission) {
		stage = "local permission denied"
	}
	return &hookError{reason: stage, cause: err}
}

// Ask runs a fresh tool-less model call. Retry an empty/malformed response once
// within the caller's existing deadline; transport retries belong to the provider.
func Ask(ctx context.Context, model Model, system, prompt string, v any) error {
	dst := reflect.ValueOf(v)
	if dst.Kind() != reflect.Pointer || dst.IsNil() {
		return &hookError{reason: "invalid response destination"}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return requestError(ctx, "canceled", err)
		}
		out := &capture{}
		p, err := model(system, out)
		if err != nil {
			return requestError(ctx, "model setup failed", err)
		}
		calls, err := p.Step(ctx, prompt, nil)
		if err != nil {
			return requestError(ctx, "provider request failed", err)
		}
		if len(calls) != 0 {
			return &hookError{reason: "unexpected tool call"}
		}
		text := strings.TrimSpace(out.text.String())
		reason := "invalid JSON response"
		if text == "" {
			reason = "empty model response"
		}
		if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
			text = text[i : j+1] // tolerate fences or a stray sentence around the JSON
		}
		// Decode into a fresh value first: failed parsing must never partially
		// modify the caller's destination, including across retry attempts.
		if strings.HasPrefix(text, "{") {
			decoded := reflect.New(dst.Elem().Type())
			if err := json.Unmarshal([]byte(text), decoded.Interface()); err == nil {
				dst.Elem().Set(decoded.Elem())
				return nil
			}
		}
		if attempt == 1 {
			return &hookError{reason: reason + " after retry"}
		}
	}
	panic("unreachable")
}
