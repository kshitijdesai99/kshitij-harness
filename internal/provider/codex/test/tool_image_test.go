package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/provider/codex"
	"kh/internal/terminal"
)

const toolImageURL = "data:image/jpeg;base64,SECRET_IMAGE"

func toolImageCalls(ids ...string) string {
	var stream strings.Builder
	for _, id := range ids {
		stream.WriteString(strings.ReplaceAll(toolItem, "call-1", id))
	}
	return stream.String() + completeEvent
}

func toolImageInput(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var state struct{ Input []map[string]any }
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	return state.Input
}

func assertToolImageOutputs(t *testing.T, input []map[string]any, results []provider.Result) {
	t.Helper()
	// One user message, all parallel calls, then their paired outputs. Images
	// belong to the function output, never to a synthetic user message.
	if len(input) != 1+2*len(results) || input[0]["role"] != "user" {
		t.Fatalf("unexpected history: %v", input)
	}
	for i, result := range results {
		call, output := input[1+i], input[1+len(results)+i]
		if call["type"] != "function_call" || call["call_id"] != result.ID || output["type"] != "function_call_output" || output["call_id"] != result.ID {
			t.Fatalf("call/output pairing %d: %v / %v", i, call, output)
		}
		var want any = result.Output
		if !result.IsError && len(result.Images) > 0 {
			parts := []any{map[string]any{"type": "input_text", "text": result.Output}}
			for _, image := range result.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": image})
			}
			want = parts
		}
		if !reflect.DeepEqual(output["output"], want) {
			t.Fatalf("output %s: got %#v, want %#v", result.ID, output["output"], want)
		}
	}
}

func TestToolImageOutputs(t *testing.T) {
	results := []provider.Result{
		{ID: "image", Output: "image metadata", Images: []string{toolImageURL, "data:image/webp;base64,SECOND_IMAGE"}},
		{ID: "plain", Output: "ordinary output"},
		{ID: "error", Output: "failed output", Images: []string{toolImageURL}, IsError: true},
		{ID: "empty", Images: []string{toolImageURL}},
	}
	p := mockCodex(t, toolImageCalls("image", "plain", "error", "empty"))
	calls, err := p.Step(context.Background(), "inspect images", nil)
	if err != nil || len(calls) != len(results) {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		assertToolImageOutputs(t, toolImageInput(t, body), results)
		return retryResponse(200, completeEvent), nil
	})
	if _, err := p.Step(context.Background(), "", results); err != nil {
		t.Fatal(err)
	}
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	resumed := codex.New(config.Defaults, "", nil, nil)
	if err := resumed.Load(saved); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := resumed.Save()
	if err != nil || string(roundTrip) != string(saved) {
		t.Fatalf("Save/Load changed image history: %s (%v)", roundTrip, err)
	}
	assertToolImageOutputs(t, toolImageInput(t, roundTrip), results)
	for _, client := range []*codex.Client{p, resumed} {
		var replay strings.Builder
		client.Replay(terminal.Renderer{Out: &replay, Err: &replay})
		text := replay.String()
		if strings.Count(text, "[image loaded]") != 3 {
			t.Fatalf("missing image markers: %q", text)
		}
		for _, hidden := range []string{"base64", "SECRET_IMAGE", "SECOND_IMAGE", "image metadata", "ordinary output", "failed output"} {
			if strings.Contains(text, hidden) {
				t.Fatalf("Replay exposed %q: %q", hidden, text)
			}
		}
	}
	// A loaded session sends exactly the same images and paired outputs.
	if _, err := resumed.Step(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestToolImageOutputsSurviveFailedStep(t *testing.T) {
	for _, mode := range []string{"failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			results := []provider.Result{
				{ID: "first", Output: "loaded", Images: []string{toolImageURL}},
				{ID: "second", Output: "plain"},
			}
			p := mockCodex(t, toolImageCalls("first", "second"))
			if _, err := p.Step(context.Background(), "inspect", nil); err != nil {
				t.Fatal(err)
			}
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				return retryResponse(200, toolItem+`data: {"type":"response.failed"}`+"\n\n"), nil
			})
			ctx := context.Background()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := p.Step(ctx, "", results); err == nil {
				t.Fatal("expected failed step")
			}
			saved, err := p.Save()
			if err != nil {
				t.Fatal(err)
			}
			assertToolImageOutputs(t, toolImageInput(t, saved), results)
			resumed := codex.New(config.Defaults, "", nil, nil)
			if err := resumed.Load(saved); err != nil {
				t.Fatal(err)
			}
			requests := 0
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				requests++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				assertToolImageOutputs(t, toolImageInput(t, body), results)
				return retryResponse(200, completeEvent), nil
			})
			if _, err := resumed.Step(context.Background(), "", nil); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d", requests)
			}
		})
	}
}
