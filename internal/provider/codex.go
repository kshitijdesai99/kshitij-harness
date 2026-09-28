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
	codexURL = "https://chatgpt.com/backend-api/codex/responses"
	// Model catalog with each model's context_window. A high client_version
	// avoids being served an older, gated list.
	codexModelsURL = "https://chatgpt.com/backend-api/codex/models?client_version=99.0.0"
	originator     = "kh" // OpenAI asks third-party harnesses to name themselves
	userAgent      = "kh/0.1"
	retryEvery     = 500 * time.Millisecond // when the connection drops
	retryFor       = time.Minute            // then give up; Ctrl-C stops sooner
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
	window  int       // context limit of c.model: 0 = not looked up, -1 = unknown
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
	if results == nil {
		c.start = time.Now() // a new turn, not a step inside one
	}
	// Outputs must directly follow their calls; a steering message goes after.
	for _, r := range results {
		c.input = append(c.input, map[string]any{"type": "function_call_output", "call_id": r.ID, "output": r.Output})
	}
	if user != "" {
		c.input = append(c.input, map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": user}},
		})
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
	var resp *http.Response
	giveUp := time.Now().Add(retryFor)
	for try := 0; ; try++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", codexURL, bytes.NewReader(body))
		setHeaders(req, access, account)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("session_id", c.session)
		resp, err = http.DefaultClient.Do(req)
		// A dropped connection (e.g. an HTTP/2 stream reset) before any reply
		// is usually gone on retry. Nothing was streamed, so retrying is safe.
		if err == nil || ctx.Err() != nil || time.Now().After(giveUp) {
			break
		}
		if try == 0 {
			fmt.Fprintln(os.Stderr, "(connection dropped, retrying...)")
		}
		select {
		case <-time.After(retryEvery):
		case <-ctx.Done():
		}
	}
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
					OutDetails struct {
						Think int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_item.added":
			// First output of any kind (thinking, text or a tool call). Timing
			// only text would count tool runs and y/n waits as model latency.
			if c.stats.TTFT == 0 {
				c.stats.TTFT = time.Since(c.start)
			}
		case "response.output_text.delta":
			fmt.Print(ev.Delta)
		case "response.output_item.done":
			// Item ids point at server storage we turned off; replaying them 404s.
			delete(ev.Item, "id")
			c.input = append(c.input, ev.Item)
			if ev.Item["type"] == "web_search_call" {
				action, _ := ev.Item["action"].(map[string]any)
				if s := webAction(action); s != "" {
					fmt.Fprintln(os.Stderr, s)
				}
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
			c.stats.Think += u.OutDetails.Think
			c.stats.Context = u.In + u.Out // what the next step starts from
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

func (c *Codex) Use(model, effort string) (string, string) {
	if model != "" && model != c.model {
		c.model, c.window = model, 0
	}
	if effort != "" {
		c.effort = effort
	}
	return c.model, c.effort
}

func (c *Codex) Stats() Stats {
	if c.window == 0 { // look up once per model, even if it fails
		c.window = c.fetchWindow()
		if c.window == 0 {
			c.window = -1
		}
	}
	s := c.stats
	s.Window = max(c.window, 0)
	c.stats = Stats{}
	return s
}

// fetchWindow looks up c.model's context limit in the Codex catalog. Short
// timeout, 0 on any failure: the meter just shows "used" without a limit.
func (c *Codex) fetchWindow() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	access, account, err := auth.Token(ctx)
	if err != nil {
		return 0
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", codexModelsURL, nil)
	setHeaders(req, access, account)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var cat struct {
		Models []struct {
			Slug   string `json:"slug"`
			Window int    `json:"context_window"`
		} `json:"models"`
	}
	json.NewDecoder(resp.Body).Decode(&cat)
	for _, m := range cat.Models {
		if m.Slug == c.model {
			return m.Window
		}
	}
	return 0
}

func setHeaders(req *http.Request, access, account string) {
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("ChatGPT-Account-ID", account) // without it the backend 401s or lists no models
	req.Header.Set("originator", originator)
	req.Header.Set("User-Agent", userAgent)
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
			Action                map[string]any
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
			if s := webAction(m.Action); s != "" {
				fmt.Fprintln(w, s)
			}
		}
	}
}

// webAction describes one web search step: a search, opening a page, or
// finding text on a page. "" if there is nothing useful to show.
func webAction(a map[string]any) string {
	str := func(k string) string { s, _ := a[k].(string); return s }
	switch {
	case str("type") == "search" && str("query") != "":
		return "search: " + str("query")
	case str("type") == "open_page" && str("url") != "":
		return "open: " + str("url")
	case str("type") == "find" && str("pattern") != "":
		return "find: " + str("pattern") + " in " + str("url")
	}
	return ""
}
