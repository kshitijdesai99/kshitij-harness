package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"kh/internal/provider"
)

// readStream is adapter-specific: the loop and renderer never see SSE events.
func (c *Client) readStream(r io.Reader) ([]provider.Call, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 16<<20)
	var calls []provider.Call
	displayed, completed := false, false
	defer func() {
		if displayed || completed {
			c.out.Reply("\n")
		}
		provider.FlushReply(c.out)
	}()
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Type     string         `json:"type"`
			Delta    string         `json:"delta"`
			Item     map[string]any `json:"item"`
			Message  string         `json:"message"`
			Response struct {
				Error *struct{ Message string } `json:"error"`
				Usage struct {
					In      int `json:"input_tokens"`
					Out     int `json:"output_tokens"`
					Details struct {
						Cached int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
					OutDetails struct {
						Think int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("codex: invalid stream event: %w", err)
		}
		switch ev.Type {
		case "response.output_item.added":
			if c.stats.TTFT == 0 {
				c.stats.TTFT = time.Since(c.start)
			}
		case "response.output_text.delta":
			if ev.Delta != "" {
				displayed = true
			}
			c.out.Reply(ev.Delta)
		case "response.output_item.done":
			if ev.Item == nil {
				return nil, fmt.Errorf("codex: missing output item")
			}
			delete(ev.Item, "id")
			c.input = append(c.input, ev.Item)
			if ev.Item["type"] == "web_search_call" {
				action, _ := ev.Item["action"].(map[string]any)
				if s := webAction(action); s != "" {
					displayed = true
					c.out.Action(s)
				}
			}
			if ev.Item["type"] == "function_call" {
				id, _ := ev.Item["call_id"].(string)
				name, _ := ev.Item["name"].(string)
				args, _ := ev.Item["arguments"].(string)
				if id == "" || name == "" || !json.Valid([]byte(args)) {
					return nil, fmt.Errorf("codex: invalid tool call")
				}
				calls = append(calls, provider.Call{ID: id, Name: name, Input: json.RawMessage(args)})
			}
		case "response.completed":
			completed = true
			u := ev.Response.Usage
			c.stats.In += u.In
			c.stats.Cached += u.Details.Cached
			c.stats.Out += u.Out
			c.stats.Think += u.OutDetails.Think
			c.stats.Context = u.In + u.Out
			return calls, nil
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
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return nil, fmt.Errorf("codex: stream: %w", err)
		}
		return nil, &streamDisconnect{err: fmt.Errorf("codex: stream: %w", err), displayed: displayed}
	}
	return nil, &streamDisconnect{err: fmt.Errorf("codex: stream ended before response.completed"), displayed: displayed}
}
