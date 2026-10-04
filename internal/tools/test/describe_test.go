package test

import (
	"encoding/json"
	"reflect"
	"testing"

	"kh/internal/tools"
)

func TestDescribeCallUsesToolIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		{"bash", `{"command":"pwd"}`, []string{"$ pwd"}},
		{"edit", `{"path":"a.go"}`, []string{"edit a.go"}},
		{"edit", `{"edits":[{"path":"a.go"},{"path":"a.go"},{"path":"b.go"}]}`, []string{"edit a.go", "edit b.go"}},
		{"memory", `{"action":"search","query":"private","path":"not-edit"}`, []string{"memory search"}},
		{"agents", `{"action":"send","address":"kh:main","message":"private"}`, []string{"agents send kh:main"}},
		{"agents", `{"action":"spawn","name":"review"}`, []string{"agents spawn review"}},
		{"custom", `{"path":"not-edit","command":"not-bash","secret":"private"}`, []string{"tool custom"}},
		{"bash", `{"path":"not-edit"}`, []string{"tool bash"}},
		{"custom", `{broken`, []string{"tool custom"}},
		{"", `{}`, []string{"tool [unknown]"}},
	} {
		if got := tools.DescribeCall(tc.name, json.RawMessage(tc.args)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %s: got %v want %v", tc.name, tc.args, got, tc.want)
		}
	}
}
