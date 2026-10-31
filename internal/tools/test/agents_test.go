package test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"kh/internal/tools"
)

func TestNativeAgentsDispatch(t *testing.T) {
	for _, request := range []tools.AgentRequest{
		{Action: "spawn", Name: "tests", Task: "run focused tests"},
		{Action: "spawn", Name: "review", Task: "review the fix", Keep: true},
		{Action: "send", Address: "kh:review", Message: "inspect edit.go"},
		{Action: "peek", Address: "kh:tests"},
		{Action: "list"},
		{Action: "stats"},
		{Action: "set_role", Role: "manager"},
		{Action: "set_role", Role: "worker"},
		{Action: "inspect", Address: "kh:review"},
		{Action: "stop", Address: "kh:review"},
		{Action: "stop", Address: "kh:review", Scope: "agent"},
		{Action: "stop", Address: "kh:review", Scope: "subtree"},
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
		`{"action":"peek"}`, `{"action":"inspect"}`, `{"action":"stop"}`,
		`{"action":"peek","address":" "}`, `{"action":"inspect","address":"kh:review\n"}`,
		`{"action":"stop","address":"kh:review","scope":"all"}`,
		`{"action":"stop","address":"kh:review","scope":" "}`,
		`{"action":"set_role"}`, `{"action":"set_role","role":"owner"}`,
		`{"action":"set_role","role":" manager "}`,
		`{"action":"set_role","role":"manager","address":"kh:other"}`,
		`{"action":"spawn","name":"review","task":"review","role":"manager"}`,
		`{"action":"spawn","name":"review","task":"review","role":"worker"}`,
		`{"action":"list","role":"manager"}`, `{"action":"stats","scope":"agent"}`,
		`{"action":"send","address":" ","message":"hello"}`,
		`{"action":"send","address":"kh:review\n","message":"hello"}`,
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

func TestNativeAgentsEcosystemSchema(t *testing.T) {
	tool := tools.Agents(nil)
	for field, want := range map[string][]string{
		"action": {"spawn", "send", "list", "set_role", "stats", "inspect", "peek", "stop"},
		"role":   {"manager", "worker"},
		"scope":  {"agent", "subtree"},
	} {
		param, ok := tool.Params[field].(map[string]any)
		if !ok || !reflect.DeepEqual(param["enum"], want) {
			t.Errorf("%s schema = %#v, want enum %v", field, tool.Params[field], want)
		}
	}
	for _, text := range []string{"configurable shared agent cap", "known agent directory", "Before starting a task", "Only managers can spawn", "Any worker or manager", "peek is a safe alias", "never full private contexts"} {
		if !strings.Contains(tool.Description, text) {
			t.Errorf("description missing %q", text)
		}
	}
}

func TestNativeAgentsValidationErrorsExplainRepair(t *testing.T) {
	tool := tools.Agents(nil)
	for _, tc := range []struct{ input, want string }{
		{`{"action":"set_role"}`, "role manager or worker"},
		{`{"action":"stop","address":"kh:review","scope":"all"}`, "scope must be agent or subtree"},
		{`{"action":"inspect"}`, "known agent address from list/spawn"},
		{`{"action":"spawn","name":"review","task":"review","role":"manager"}`, "spawned agents start as workers"},
	} {
		_, err := tool.Run(ctx, []byte(tc.input))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.input, err, tc.want)
		}
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
