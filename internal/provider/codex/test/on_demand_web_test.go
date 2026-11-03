package test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/tools"
)

func TestOnDemandWebActivationSnapshotsCacheAndHistory(t *testing.T) {
	mockCodex(t, completeEvent)
	cfg := config.Defaults
	p := codex.New(cfg, "", []tools.Tool{tools.Edit}, nil)
	p.DeferWebSearch()
	type request struct {
		Tools []map[string]any
		Key   string `json:"prompt_cache_key"`
	}
	var requests []request
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var q request
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, q)
		return retryResponse(200, completeEvent), nil
	})
	step := func() {
		t.Helper()
		if _, err := p.Step(context.Background(), "hello", nil); err != nil {
			t.Fatal(err)
		}
	}
	step()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.EnableWebSearch(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(requests[0].Tools) != 1 {
		t.Fatal("activation mutated prior request")
	}
	step()
	step()
	if len(requests[1].Tools) != 2 || requests[1].Tools[1]["type"] != "web_search" || requests[0].Key == requests[1].Key || requests[1].Key != requests[2].Key {
		t.Fatalf("bad snapshots/cache: %+v", requests)
	}
	// Deferred resumes retain required schema whenever hosted history exists.
	state := []byte(`{"session":"historical","input":[{"type":"web_search_call","id":"old","status":"completed","action":{"type":"search","query":"prior"}}]}`)
	q := codex.New(cfg, "", nil, nil)
	q.DeferWebSearch()
	if err := q.Load(state); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Step(context.Background(), "continue", nil); err != nil {
		t.Fatal(err)
	}
	last := requests[len(requests)-1]
	if len(last.Tools) != 1 || last.Tools[0]["type"] != "web_search" {
		t.Fatal("missing historical hosted schema")
	}
	cfg.WebSearch = false
	disabled := codex.New(cfg, "", nil, nil)
	if err := disabled.EnableWebSearch(); err == nil {
		t.Fatal("enabled disabled setting")
	}
	if err := disabled.Load(state); err != nil {
		t.Fatal(err)
	}
	before := len(requests)
	if _, err := disabled.Step(context.Background(), "continue", nil); err == nil || !strings.Contains(err.Error(), "/compact") {
		t.Fatalf("disabled historical error=%v", err)
	}
	if len(requests) != before {
		t.Fatal("disabled historical request reached server")
	}
}

func TestOnDemandWebActivationChangesOnlyNextRequest(t *testing.T) {
	mockCodex(t, completeEvent)
	p := codex.New(config.Defaults, "", nil, nil)
	p.DeferWebSearch()
	count := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var body struct{ Tools []map[string]any }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			if len(body.Tools) != 0 {
				t.Fatal("web exposed before activation")
			}
			if err := p.EnableWebSearch(); err != nil {
				t.Fatal(err)
			}
			if len(body.Tools) != 0 {
				t.Fatal("in-flight request mutated")
			}
		} else if len(body.Tools) != 1 || body.Tools[0]["type"] != "web_search" {
			t.Fatal("web missing next request")
		}
		count++
		return retryResponse(200, completeEvent), nil
	})
	for i := 0; i < 2; i++ {
		if _, err := p.Step(context.Background(), "hello", nil); err != nil {
			t.Fatal(err)
		}
	}
}
