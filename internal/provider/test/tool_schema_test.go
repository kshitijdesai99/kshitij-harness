package test

import (
	"context"
	"encoding/json"
	"io"
	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/tools"
	"net/http"
	"strings"
	"testing"
)

func TestFunctionSchemaKeepsAlternativeEditFormatsOptional(t *testing.T) {
	_ = mockCodex(t, completeEvent)
	saw := false
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			var body struct{ Tools []map[string]any }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, tool := range body.Tools {
				if tool["name"] == "edit" {
					saw = true
					strict, exists := tool["strict"]
					if !exists || strict != false {
						t.Errorf("alternative edit schema must explicitly disable strict coercion: %v", tool)
					}
					params := tool["parameters"].(map[string]any)
					if required, ok := params["required"].([]any); !ok || len(required) != 0 {
						t.Errorf("legacy fields made mandatory: %v", params)
					}
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(completeEvent)), Header: make(http.Header)}, nil
	})
	p := provider.NewCodex(config.Defaults, "", []tools.Tool{tools.Edit}, nil)
	if _, err := p.Step(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("edit function schema missing")
	}
}
