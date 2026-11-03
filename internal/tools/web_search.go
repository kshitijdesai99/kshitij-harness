package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

const EnableWebSearchToolName = "enable_web_search"

// EnableWebSearch activates a hosted capability, not a search or external call.
func EnableWebSearch(enable func() error) Tool {
	return Tool{
		Name:        EnableWebSearchToolName,
		Description: "Enable hosted web search, open and find for the next model request. This only enables the capability; it does not search or fetch anything. After success use the native hosted web search, not tool_invoke, for actual web operations.",
		Params:      map[string]any{}, Required: []string{},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var args map[string]json.RawMessage
			if err := json.Unmarshal(input, &args); err != nil || args == nil || len(args) != 0 {
				return "", fmt.Errorf("enable_web_search accepts only an empty object")
			}
			if err := enable(); err != nil {
				return "", err
			}
			return "Hosted web search is enabled for the next model request. Use native hosted search/open/find directly, not tool_invoke. No search or fetch has occurred.", nil
		},
	}
}
