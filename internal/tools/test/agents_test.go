package test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"kh/internal/tools"
)

func TestNativeAgentsDispatch(t *testing.T) {
	for _, request := range []tools.AgentRequest{
		{Action: "spawn", Name: "tests", Task: "run focused tests"},
		{Action: "send", Address: "kh:review", Message: "inspect edit.go"},
		{Action: "peek", Address: "kh:tests"},
		{Action: "list"},
	} {
		t.Run(request.Action, func(t *testing.T) {
			called := false
			tool := tools.Agents(func(c context.Context, got tools.AgentRequest) (string, error) {
				called = true
				if c != ctx || !reflect.DeepEqual(got, request) {
					t.Fatalf("request changed: %+v", got)
				}
				return "native result", nil
			})
			input, _ := json.Marshal(request)
			out, err := tool.Run(ctx, input)
			if !called || err != nil || out != "native result" {
				t.Fatalf("called=%v out=%q err=%v", called, out, err)
			}
			if tool.Name != "agents" || !reflect.DeepEqual(tool.Required, []string{"action"}) {
				t.Fatalf("invalid schema: %+v", tool)
			}
		})
	}
}

func TestNativeAgentsRejectInvalidRequests(t *testing.T) {
	tool := tools.Agents(func(context.Context, tools.AgentRequest) (string, error) {
		t.Fatal("invalid request dispatched")
		return "", nil
	})
	for _, input := range []string{
		"not json", `{}`, `{"action":"unknown"}`, `{"action":"spawn","name":"tests"}`,
		`{"action":"spawn","name":"","task":"run tests"}`, `{"action":"spawn","name":"tests","task":" "}`,
		`{"action":"send","message":"hello"}`, `{"action":"send","address":"kh:main","message":"a\nb"}`,
		`{"action":"peek"}`,
	} {
		if _, err := tool.Run(ctx, []byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tool.Run(c, []byte(`{"action":"list"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeAgentsTransportErrors(t *testing.T) {
	failure := errors.New("tmux unavailable")
	tool := tools.Agents(func(context.Context, tools.AgentRequest) (string, error) { return "", failure })
	if _, err := tool.Run(ctx, []byte(`{"action":"list"}`)); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := tools.Agents(nil).Run(ctx, []byte(`{"action":"list"}`)); err == nil {
		t.Fatal("nil transport accepted")
	}
}
