// Package tools holds everything the model can call.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
)

// Lines is every line typed at the terminal, from one background reader.
// Whoever is waiting gets the next line: a y/n question, the chat prompt, or
// agent.Run between steps (to steer a running task). Closed on Ctrl-D.
var Lines = make(chan string, 16)

func init() {
	go func() {
		r := bufio.NewReader(os.Stdin)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				close(Lines)
				return
			}
			Lines <- strings.TrimSpace(line)
		}
	}()
}

// Tool is one plugin. Add a tool = one file + one line in the list in main.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any // JSON Schema "properties"
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}
