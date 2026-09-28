package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"kh/internal/auth"
	"kh/internal/config"
	"kh/internal/tools"
)

// OpenAI protocol values, not config.
const (
	codexURL   = "https://chatgpt.com/backend-api/codex/responses"
	originator = "kh" // OpenAI asks third-party harnesses to name themselves
	userAgent  = "kh/0.1"
)

type Codex struct {
	model   string
	effort  string
	rules   string // always today's config, so rule changes reach old sessions
	repoMap string // frozen per session: it changes often and would miss the cache
	tools   []map[string]any
	input   []map[string]any // full history; the server stores nothing (store: false)
	session string
	stats   Stats
	start   time.Time // when the current turn's user message was sent
}

func NewCodex(c config.Config, repoMap string, ts []tools.Tool) *Codex {
	id := make([]byte, 16)
	rand.Read(id)
	x := &Codex{model: c.Model, effort: c.Effort, rules: c.System, repoMap: repoMap, session: hex.EncodeToString(id)}
	for _, t := range ts {
		x.tools = append(x.tools, map[string]any{
			"type": "function", "name": t.Name, "description": t.Description,
			"parameters": map[string]any{"type": "object", "properties": t.Params, "required": t.Required},
		})
	}
	if c.WebSearch {
		// Server-side tool: OpenAI runs the search, we never see a call to execute.
		x.tools = append(x.tools, map[string]any{"type": "web_search"})
	}
	return x
}

func (c *Codex) Step(ctx context.Context, user string, results []Result) (calls []Call, err error) {
	if user != "" {
		c.start = time.Now()
		c.input = append(c.input, map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": user}},
		})
	}
	for _, r := range results {
		c.input = append(c.input, map[string]any{"type": "function_call_output", "call_id": r.ID, "output": r.Output})
	}
	// On failure, drop this reply's partial items: a function_call with no
	// output would make every later request in the session fail.
	n := len(c.input)
	defer func() {
		if err != nil {
			c.input = c.input[:n]
		}
	}()

	body, _ := json.Marshal(map[string]any{
		"model":               c.model,
		"instructions":        c.instructions(),
		"input":               c.input,
		"tools":               c.tools,
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"reasoning":           map[string]string{"effort": c.effort},
		"store":               false,
		"stream":              true,
		// Lets us replay reasoning next turn without the server storing it.
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": c.cacheKey(),
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
	req.Header.Set("originator", originator)
	req.Header.Set("User-Agent", userAgent)
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
				Usage struct {
					In      int `json:"input_tokens"`
					Out     int `json:"output_tokens"`
					Details struct {
						Cached int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if c.stats.TTFT == 0 {
				c.stats.TTFT = time.Since(c.start)
			}
			fmt.Print(ev.Delta)
		case "response.output_item.done":
			// Item ids point at server storage we turned off; replaying them 404s.
			delete(ev.Item, "id")
			c.input = append(c.input, ev.Item)
			if ev.Item["type"] == "web_search_call" {
				action, _ := ev.Item["action"].(map[string]any)
				fmt.Fprintln(os.Stderr, "search:", action["query"])
			}
			if ev.Item["type"] == "function_call" {
				id, _ := ev.Item["call_id"].(string)
				name, _ := ev.Item["name"].(string)
				args, _ := ev.Item["arguments"].(string)
				calls = append(calls, Call{ID: id, Name: name, Input: json.RawMessage(args)})
			}
		case "response.completed":
			u := ev.Response.Usage
			c.stats.In += u.In
			c.stats.Cached += u.Details.Cached
			c.stats.Out += u.Out
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

func (c *Codex) instructions() string {
	if c.repoMap == "" {
		return c.rules
	}
	return c.rules + "\n\nRepo map (path: top-level symbols). Use it to go straight to the right file:\n" + c.repoMap
}

// cacheKey is a hash of everything before the history. Same prompt and tools
// = same key, so every session in a repo shares one warm cache.
func (c *Codex) cacheKey() string {
	b, _ := json.Marshal([]any{c.model, c.instructions(), c.tools})
	h := sha256.Sum256(b)
	return "kh_" + hex.EncodeToString(h[:12])
}

func (c *Codex) Stats() Stats {
	s := c.stats
	c.stats = Stats{}
	return s
}

type codexState struct {
	Session string           `json:"session"`
	Map     string           `json:"map"`
	Input   []map[string]any `json:"input"`
}

func (c *Codex) Save() ([]byte, error) {
	return json.Marshal(codexState{c.session, c.repoMap, c.input})
}

// Load restores the session's repo map, so the prompt stays byte-identical
// and cached; rules still come from today's config (one cache miss after
// you change them).
func (c *Codex) Load(b []byte) error {
	var s codexState
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	c.session, c.input = s.Session, s.Input
	if s.Map != "" {
		c.repoMap = s.Map
	}
	return nil
}

func (c *Codex) Replay(w io.Writer) {
	for _, it := range c.input {
		// Round-trip through JSON: loaded items are generic maps, so this
		// is the shortest way to read their fields.
		var m struct {
			Type, Role, Arguments string
			Content               []struct{ Text string }
			Action                struct{ Query string }
		}
		b, _ := json.Marshal(it)
		json.Unmarshal(b, &m)
		switch m.Type {
		case "message":
			for _, part := range m.Content {
				if m.Role == "user" {
					fmt.Fprintln(w, ">", part.Text)
				} else {
					fmt.Fprintln(w, part.Text)
				}
			}
		case "function_call":
			var a struct{ Command, Path string }
			json.Unmarshal([]byte(m.Arguments), &a)
			if a.Command != "" {
				fmt.Fprintln(w, "$", a.Command)
			} else {
				fmt.Fprintln(w, "edit", a.Path)
			}
		case "web_search_call":
			fmt.Fprintln(w, "search:", m.Action.Query)
		}
	}
}
