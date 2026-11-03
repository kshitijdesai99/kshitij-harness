package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

func registryFacade(t *testing.T, ts ...tools.Tool) []tools.Tool {
	t.Helper()
	r, err := tools.NewRegistry(ts)
	if err != nil {
		t.Fatal(err)
	}
	return r.Tools()
}

func TestRegistryFixedSchemasAndCompleteContracts(t *testing.T) {
	schema := map[string]any{"type": "object", "oneOf": []any{map[string]any{"required": []string{"secret"}}}, "additionalProperties": false, "properties": map[string]any{"secret": map[string]any{"type": "string", "enum": []string{"a", "b"}}}, "$defs": map[string]any{"nested": map[string]any{"type": "integer"}}}
	params := map[string]any{"retained": map[string]any{"description": "original fallback"}}
	called := false
	tool := tools.Tool{Name: "mcp__server__exact", Description: strings.Repeat("Full description. ", 100), InputSchema: schema, Params: params, Required: []string{"secret"}, Run: func(context.Context, json.RawMessage) (string, error) { called = true; return "", nil }}
	facade := registryFacade(t, tool)
	other := registryFacade(t, tools.Tool{Name: "other", Description: strings.Repeat("huge ", 10000)})
	for i := range facade {
		if facade[i].Name != other[i].Name || !reflect.DeepEqual(facade[i].Params, other[i].Params) || facade[i].Description != other[i].Description {
			t.Fatal("facade scales with catalogue")
		}
	}
	data, err := facade[0].Run(context.Background(), json.RawMessage(`{"action":"describe","name":"mcp__server__exact"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data, "Untrusted tool reference data") {
		t.Fatal(data)
	}
	var contract struct {
		Name, Description string
		InputSchema       map[string]any `json:"input_schema"`
		Params            map[string]any
		Required          []string
	}
	if err := json.Unmarshal([]byte(strings.SplitN(data, "\n", 2)[1]), &contract); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(schema)
	got, _ := json.Marshal(contract.InputSchema)
	if string(got) != string(expected) || contract.Description != tool.Description || !reflect.DeepEqual(contract.Params, params) || !reflect.DeepEqual(contract.Required, tool.Required) {
		t.Fatalf("lost contract: %s", data)
	}
	if called {
		t.Fatal("description executed tool")
	}
	schema["changed"] = "after registration"
	data, _ = facade[0].Run(context.Background(), json.RawMessage(`{"action":"describe","name":"mcp__server__exact"}`))
	if strings.Contains(data, "after registration") {
		t.Fatal("metadata not snapshotted")
	}
}

func TestRegistrySearchBoundedRanking(t *testing.T) {
	ts := []tools.Tool{{Name: "mcp__jira__search", Description: "Search work items"}, {Name: "aaa", Description: "jira jira jira"}, {Name: "unrelated", Description: "nothing"}}
	for i := 0; i < 20; i++ {
		ts = append(ts, tools.Tool{Name: fmt.Sprintf("z%02d", i), Description: strings.Repeat("jira ", 200)})
	}
	f := registryFacade(t, ts...)[0]
	raw := json.RawMessage(`{"action":"search","query":"jira","limit":2}`)
	a, err := f.Run(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Run(context.Background(), raw)
	if a != b || len(a) > 1500 || !strings.Contains(a, `"more":true`) {
		t.Fatal(a)
	}
	var out struct {
		Matches []struct{ Name, Summary string }
		Total   int `json:"total_matches"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(a, "\n", 2)[1]), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 2 || out.Matches[0].Name != "mcp__jira__search" || out.Matches[1].Name != "aaa" || out.Total != 22 {
		t.Fatal(a)
	}
	empty, err := f.Run(context.Background(), json.RawMessage(`{"action":"search","query":"nonexistent"}`))
	if err != nil || !strings.Contains(empty, `"matches":[]`) {
		t.Fatalf("%s %v", empty, err)
	}
}

func TestRegistryValidationAndOversize(t *testing.T) {
	for _, ts := range [][]tools.Tool{{{Name: ""}}, {{Name: "bad\nname"}}, {{Name: tools.RegistryToolName}}, {{Name: tools.InvokeToolName}}, {{Name: "same"}, {Name: "same"}}, {{Name: "invalid", Params: map[string]any{"fn": func() {}}}}} {
		if _, err := tools.NewRegistry(ts); err == nil {
			t.Fatalf("accepted %+v", ts)
		}
	}
	var executions atomic.Int32
	fs := registryFacade(t, tools.Tool{Name: "ok", Run: func(context.Context, json.RawMessage) (string, error) { executions.Add(1); return "", nil }}, tools.Tool{Name: "huge", Description: strings.Repeat("X", tools.RegistryContractLimit)})
	for _, raw := range []string{`null`, `[]`, `{}`, `{"action":"other"}`, `{"action":"search","query":"!!!"}`, `{"action":"search","query":"ok","limit":13}`, `{"action":"search","query":"ok","limit":-1}`, `{"action":"describe","name":"absent"}`, `{"action":"describe","name":"ok","query":"extra"}`, `{"action":"search","query":"ok","unknown":true}`, `{"action":"describe","name":"ok"} {}`, `{"action":"search","query":"` + strings.Repeat("a", 257) + `"}`} {
		if _, err := fs[0].Run(context.Background(), json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	out, err := fs[0].Run(context.Background(), json.RawMessage(`{"action":"describe","name":"huge"}`))
	if err == nil || !strings.Contains(err.Error(), "no schema or description returned") || out != "" {
		t.Fatalf("%q %v", out, err)
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"name":"missing","arguments":{}}`, `{"name":"ok"}`, `{"name":"ok","arguments":null}`, `{"name":"ok","arguments":[]}`, `{"name":"ok","arguments":"secret"}`, `{"name":"ok","arguments":{},"endpoint":"https://example.invalid"}`, `{"name":"ok","arguments":{}} {}`} {
		if _, err := fs[1].RunResult(context.Background(), json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if executions.Load() != 0 {
		t.Fatal("invalid request executed")
	}
}

func TestRegistryInvokePreservesClosuresResultsAndCancellation(t *testing.T) {
	marker := errors.New("approval denied")
	ctx := context.WithValue(context.Background(), struct{}{}, "same context")
	var seen json.RawMessage
	result := provider.Result{ID: "inner", Output: "output", Images: []string{"data:image/png;base64,AA=="}, IsError: true}
	fs := registryFacade(t, tools.Tool{Name: "image", RunResult: func(got context.Context, input json.RawMessage) (provider.Result, error) {
		if got != ctx {
			t.Fatal("context replaced")
		}
		seen = append(json.RawMessage(nil), input...)
		return result, nil
	}, Run: func(context.Context, json.RawMessage) (string, error) {
		t.Fatal("Run preferred over RunResult")
		return "", nil
	}}, tools.Tool{Name: "approved", Run: func(context.Context, json.RawMessage) (string, error) { return "", marker }})
	got, err := fs[1].RunResult(ctx, json.RawMessage(`{"name":"image","arguments":{"large":9007199254740993}}`))
	if err != nil || !reflect.DeepEqual(got, result) || string(seen) != `{"large":9007199254740993}` {
		t.Fatalf("%+v %s %v", got, seen, err)
	}
	_, err = fs[1].RunResult(ctx, json.RawMessage(`{"name":"approved","arguments":{}}`))
	if !errors.Is(err, marker) {
		t.Fatal("wrapped approval bypassed")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, f := range fs {
		if f.Run != nil {
			_, err = f.Run(canceled, json.RawMessage(`{}`))
		} else {
			_, err = f.RunResult(canceled, json.RawMessage(`{}`))
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

type registryModel struct {
	t    *testing.T
	step int
}

func (m *registryModel) Step(_ context.Context, _ string, results []provider.Result) ([]provider.Call, error) {
	m.step++
	if m.step == 1 {
		return []provider.Call{{ID: "outer-image", Name: tools.InvokeToolName, Input: json.RawMessage(`{"name":"image","arguments":{}}`)}, {ID: "outer-error", Name: tools.InvokeToolName, Input: json.RawMessage(`{"name":"failure","arguments":{}}`)}}, nil
	}
	if len(results) != 2 || results[0].ID != "outer-image" || len(results[0].Images) != 1 || results[0].Output != "picture" || results[1].ID != "outer-error" || !results[1].IsError || len(results[1].Images) != 0 {
		m.t.Fatalf("runner results: %+v", results)
	}
	return nil, nil
}
func TestRegistryRunnerIDsAndImages(t *testing.T) {
	fs := registryFacade(t, tools.Tool{Name: "image", RunResult: func(context.Context, json.RawMessage) (provider.Result, error) {
		return provider.Result{ID: "wrong", Output: "picture", Images: []string{"data:image/png;base64,AA=="}}, nil
	}}, tools.Tool{Name: "failure", RunResult: func(context.Context, json.RawMessage) (provider.Result, error) {
		return provider.Result{IsError: true, Images: []string{"should be stripped"}}, nil
	}})
	if err := (agent.Runner{Model: &registryModel{t: t}, Tools: fs}).Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryInvokeRunAndInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	fs := registryFacade(t,
		tools.Tool{Name: "text", Run: func(context.Context, json.RawMessage) (string, error) { return "original output", nil }},
		tools.Tool{Name: "wait", Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		}},
	)
	got, err := fs[1].RunResult(context.Background(), json.RawMessage(`{"name":"text","arguments":{}}`))
	if err != nil || got.Output != "original output" || got.IsError {
		t.Fatalf("%+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := fs[1].RunResult(ctx, json.RawMessage(`{"name":"wait","arguments":{}}`)); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRegistryLogicalCallPreservesIdentityWithoutExecution(t *testing.T) {
	var executions atomic.Int32
	r, err := tools.NewRegistry([]tools.Tool{
		{Name: "memory", Run: func(context.Context, json.RawMessage) (string, error) { executions.Add(1); return "", nil }},
		{Name: "agents", RunResult: func(context.Context, json.RawMessage) (provider.Result, error) {
			executions.Add(1)
			return provider.Result{}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory", "agents"} {
		t.Run(name, func(t *testing.T) {
			arguments := json.RawMessage(`{ "action" : "inspect", "id":9007199254740993, "decimal":1.234567890123456789 }`)
			direct := provider.Call{ID: "legacy-" + name, Name: name, Input: arguments}
			got, ok := r.LogicalCall(direct)
			if !ok || !reflect.DeepEqual(got, direct) {
				t.Fatalf("direct call changed: %+v, %v", got, ok)
			}
			wrapped := provider.Call{ID: "outer-" + name, Name: tools.InvokeToolName, Input: json.RawMessage(`{"name":"` + name + `","arguments":` + string(arguments) + `}`)}
			original := append(json.RawMessage(nil), wrapped.Input...)
			got, ok = r.LogicalCall(wrapped)
			want := provider.Call{ID: wrapped.ID, Name: name, Input: arguments}
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("wrapped identity/precision lost: %+v, %v", got, ok)
			}
			if string(wrapped.Input) != string(original) || wrapped.Name != tools.InvokeToolName {
				t.Fatal("provider/archive outer call changed")
			}
		})
	}
	if executions.Load() != 0 {
		t.Fatal("evidence normalization executed tool")
	}
}

func TestRegistryLogicalCallRejectsInvalidAndDiscovery(t *testing.T) {
	var executions atomic.Int32
	r, err := tools.NewRegistry([]tools.Tool{{Name: "memory", Run: func(context.Context, json.RawMessage) (string, error) { executions.Add(1); return "", nil }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{tools.RegistryToolName, "missing", "Memory", "memory\n", ""} {
		if got, ok := r.LogicalCall(provider.Call{ID: "outer", Name: name, Input: json.RawMessage(`{"action":"search","query":"memory"}`)}); ok {
			t.Errorf("accepted discovery/unregistered %q: %+v", name, got)
		}
	}
	for _, raw := range []string{
		``, `null`, `[]`, `{}`, `{"name":"memory"}`, `{"arguments":{}}`,
		`{"name":null,"arguments":{}}`, `{"name":1,"arguments":{}}`,
		`{"name":"missing","arguments":{}}`, `{"name":"Memory","arguments":{}}`,
		`{"name":"memory\n","arguments":{}}`, `{"name":"tool_registry","arguments":{}}`,
		`{"name":"tool_invoke","arguments":{}}`, `{"name":"memory","arguments":null}`,
		`{"name":"memory","arguments":[]}`, `{"name":"memory","arguments":1}`,
		`{"name":"memory","arguments":"{}"}`, `{"name":"memory","arguments":true}`,
		`{"name":"memory","arguments":{},"extra":true}`, `{"name":"memory","arguments":{}} {}`,
		`{"name":"memory","arguments":{"broken":}}`,
	} {
		if got, ok := r.LogicalCall(provider.Call{ID: "outer", Name: tools.InvokeToolName, Input: json.RawMessage(raw)}); ok {
			t.Errorf("accepted invalid envelope %s: %+v", raw, got)
		}
		if _, err := r.Tools()[1].RunResult(context.Background(), json.RawMessage(raw)); err == nil {
			t.Errorf("invocation validation drifted for %s", raw)
		}
	}
	if executions.Load() != 0 {
		t.Fatal("invalid envelope executed tool")
	}
}

func TestRegistryDescribeCallHidesArguments(t *testing.T) {
	for _, name := range []string{"bash", "edit", "mcp__server__tool"} {
		raw := json.RawMessage(`{"name":"` + name + `","arguments":{"command":"secret","path":"secret","token":"secret"}}`)
		if got := tools.DescribeCall(tools.InvokeToolName, raw); !reflect.DeepEqual(got, []string{"tool " + name}) {
			t.Fatal(got)
		}
	}
	got := tools.DescribeCall(tools.InvokeToolName, json.RawMessage(`{"name":"bad\nsecret","arguments":{}}`))
	if !reflect.DeepEqual(got, []string{"tool tool_invoke"}) {
		t.Fatal(got)
	}
}
