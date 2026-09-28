package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"kh/internal/auth"
	"kh/internal/tools"
)

const codexURL = "https://chatgpt.com/backend-api/codex/responses"

type Codex struct {
	model   string
	system  string
	tools   []map[string]any
	input   []map[string]any // full history; the server stores nothing (store: false)
	session string
}

func NewCodex(model, system string, ts []tools.Tool) *Codex {
	id := make([]byte, 16)
	rand.Read(id)
	c := &Codex{model: model, system: system, session: hex.EncodeToString(id)}
	for _, t := range ts {
		c.tools = append(c.tools, map[string]any{
			"type": "function", "name": t.Name, "description": t.Description,
			"parameters": map[string]any{"type": "object", "properties": t.Params, "required": t.Required},
		})
	}
	return c
}

func (c *Codex) Step(ctx context.Context, user string, results []Result) ([]Call, error) {
	if user != "" {
		c.input = append(c.input, map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": user}},
		})
	}
	for _, r := range results {
		c.input = append(c.input, map[string]any{"type": "function_call_output", "call_id": r.ID, "output": r.Output})
	}

	body, _ := json.Marshal(map[string]any{
		"model":               c.model,
		"instructions":        c.system,
		"input":               c.input,
		"tools":               c.tools,
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"store":               false,
		"stream":              true,
		// Lets us replay reasoning next turn without the server storing it.
		"include": []string{"reasoning.encrypted_content"},
		// Same key every turn so the server reuses its prompt cache.
		"prompt_cache_key": c.session,
	})
	access, account, err := auth.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", codexURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("ChatGPT-Account-ID", account)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// OpenAI asks third-party harnesses to name themselves.
	req.Header.Set("originator", "kh")
	req.Header.Set("User-Agent", "kh/0.1")
	req.Header.Set("session_id", c.session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("codex: status %d: %s", resp.StatusCode, b)
	}

	var calls []Call
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 16<<20) // reasoning items can be large
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type     string         `json:"type"`
			Delta    string         `json:"delta"`
			Item     map[string]any `json:"item"`
			Response struct {
				Error *struct{ Message string } `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			fmt.Print(ev.Delta)
		case "response.output_item.done":
			// Item ids point at server storage we turned off; replaying them 404s.
			delete(ev.Item, "id")
			c.input = append(c.input, ev.Item)
			if ev.Item["type"] == "function_call" {
				id, _ := ev.Item["call_id"].(string)
				name, _ := ev.Item["name"].(string)
				args, _ := ev.Item["arguments"].(string)
				calls = append(calls, Call{ID: id, Name: name, Input: json.RawMessage(args)})
			}
		case "response.failed":
			if ev.Response.Error != nil {
				return nil, fmt.Errorf("codex: %s", ev.Response.Error.Message)
			}
			return nil, fmt.Errorf("codex: response failed")
		case "response.incomplete":
			return nil, fmt.Errorf("codex: reply was cut off")
		case "error":
			return nil, fmt.Errorf("codex: %s", ev.Message)
		}
	}
	fmt.Println()
	return calls, sc.Err()
}
