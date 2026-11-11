// Package codex implements Codex protocol, credentials, and session state.
package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"kh/internal/config"
	"kh/internal/provider"
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
	retryEvery     = time.Second // after a recoverable connection failure
	retryFor       = time.Minute // recovery budget; Ctrl-C stops sooner
)

type Client struct {
	out              provider.Output
	observe          func(provider.Event) error
	observeErr       error
	attemptID        string
	model            string
	effort           string
	rules            string // always today's config, so rule changes reach old sessions
	repoMap          string // frozen per session: it changes often and would miss the cache
	memory           string // memory message for the next user message; saved with it, so it is sent once
	context          string // non-user reference context for the next Step, including empty-user continuations
	lastStepResponse string // completed terminal text only, never restored from history
	image            string // data URL attached to the next user message; retained if the request fails
	tools            []map[string]any
	webAvailable     bool
	webActive        *atomic.Bool     // shared pointer keeps Compact's private copy lock-free
	toolChoice       string           // empty means auto; compaction uses none to prohibit new tools
	input            []map[string]any // full history; the server stores nothing (store: false)
	session          string
	stats            provider.Stats
	start            time.Time     // when the current turn's user message was sent
	window           int           // context limit of c.model: 0 = not looked up, -1 = unknown
	idleTimeout      time.Duration // bounds silent headers/streams, not total reasoning time
}

func New(c config.Config, repoMap string, ts []tools.Tool, out provider.Output) *Client {
	if out == nil {
		out = discardOutput{}
	}
	id := make([]byte, 16)
	rand.Read(id)
	idle := time.Duration(c.ModelIdleTimeoutSec) * time.Second
	if idle <= 0 {
		idle = 120 * time.Second
	}
	x := &Client{out: out, model: c.Model, effort: c.Effort, rules: c.System, repoMap: repoMap, session: hex.EncodeToString(id), idleTimeout: idle, webAvailable: c.WebSearch, webActive: new(atomic.Bool)}
	x.webActive.Store(c.WebSearch)
	for _, t := range ts {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": t.Params, "required": t.Required}
		}
		x.tools = append(x.tools, map[string]any{
			"type": "function", "name": t.Name, "description": t.Description, "strict": false,
			"parameters": schema,
		})
	}
	if c.WebSearch {
		x.rules += "\n\nWeb\n- For anything on the web, use the built-in web search: it can search, open pages and find text on them."
		for _, tool := range ts {
			if tool.Name == "bash" {
				x.rules += " Use curl only for APIs or raw data that search can't reach."
				break
			}
		}
	}
	return x
}

func (c *Client) Step(ctx context.Context, user string, results []provider.Result) (calls []provider.Call, err error) {
	c.lastStepResponse = ""
	c.observeErr = nil
	if c.observe != nil {
		defer func() {
			err = errors.Join(err, c.observeErr)
			if err != nil {
				calls = nil
			}
		}()
	}
	if results == nil {
		c.start = time.Now() // a new turn, not a step inside one
	}
	// Outputs must directly follow their calls; a steering message goes after.
	for _, r := range results {
		var output any = r.Output
		if !r.IsError && len(r.Images) > 0 {
			parts := []map[string]any{{"type": "input_text", "text": r.Output}}
			for _, image := range r.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": image})
			}
			output = parts
		}
		c.input = append(c.input, map[string]any{"type": "function_call_output", "call_id": r.ID, "output": output})
	}
	if c.context != "" {
		c.input = append(c.input, map[string]any{"type": "message", "role": "developer",
			"content": []map[string]any{{"type": "input_text", "text": c.context}}})
		c.context = ""
	}
	if user != "" {
		// Memory goes just before the message it was retrieved for, so it
		// cannot look like a newer request, and stays in history: the caller
		// skips notes already there, and the provider caches them.
		if c.memory != "" {
			c.input = append(c.input, map[string]any{"type": "message", "role": "user", memoryOriginKey: true,
				"content": []map[string]any{{"type": "input_text", "text": c.memory}}})
			c.memory = ""
		}
		content := []map[string]any{{"type": "input_text", "text": user}}
		if c.image != "" {
			content = append(content, map[string]any{"type": "input_image", "image_url": c.image})
		}
		c.input = append(c.input, map[string]any{
			"type": "message", "role": "user", "content": content,
		})
	}
	// On failure, drop this reply's partial items: a function_call with no
	// output would make every later request in the session fail.
	n := len(c.input)
	defer func() {
		if err != nil {
			c.input = c.input[:n]
		} else {
			if user != "" {
				c.image = ""
			}
			if ctx.Err() == nil && c.observeErr == nil && len(calls) == 0 {
				c.lastStepResponse = terminalResponse(c.input[n:])
			}
		}
	}()

	if !c.webAvailable && c.toolChoice != "none" && c.hasWebHistory() {
		return nil, fmt.Errorf("web search is disabled but saved history contains hosted searches; use /compact to clear that history before continuing")
	}
	requestTools := c.requestTools()
	choice := c.toolChoice
	if choice == "" {
		choice = "auto"
	}
	body, _ := json.Marshal(map[string]any{
		"model":               c.model,
		"instructions":        c.instructions(),
		"input":               c.wireInput(),
		"tools":               requestTools,
		"tool_choice":         choice,
		"parallel_tool_calls": true,
		"reasoning":           map[string]string{"effort": c.effort},
		"store":               false,
		"stream":              true,
		// Lets us replay reasoning next turn without the server storing it.
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": c.cacheKey(requestTools),
	})
	access, account, err := Token(ctx)
	if err != nil {
		return nil, err
	}
	return c.requestWithRetry(ctx, body, access, account, n)
}

// DeferWebSearch is called during application setup, before the first request.
// Existing hosted-search history still requires its declaration on continuation.
func (c *Client) DeferWebSearch() { c.webActive.Store(false) }

// EnableWebSearch is safe for concurrent, repeated activation tool calls.
func (c *Client) EnableWebSearch() error {
	if !c.webAvailable {
		return fmt.Errorf("web search is disabled in configuration")
	}
	c.webActive.Store(true)
	return nil
}

func (c *Client) hasWebHistory() bool {
	for _, item := range c.input {
		if item["type"] == "web_search_call" {
			return true
		}
	}
	return false
}

func (c *Client) requestTools() []map[string]any {
	// Compact constructs its own minimal schema and prohibits tool execution.
	if c.toolChoice == "none" || (!c.webActive.Load() && !c.hasWebHistory()) {
		return c.tools
	}
	return append(append([]map[string]any(nil), c.tools...), map[string]any{"type": "web_search"})
}

// AttachImage stages a data URL for the next user message. The current session
// saves the image with that message so resuming preserves its context.
func (c *Client) AttachImage(dataURL string) { c.image = dataURL }

// SetMemory stages a memory message for the next user message.
func (c *Client) SetMemory(text string) { c.memory = text }

// SetContext stages reference context without inventing a user message.
func (c *Client) SetContext(text string) { c.context = text }

func (c *Client) instructions() string {
	if c.repoMap == "" {
		return c.rules
	}
	return c.rules + "\n\nRepo map (path: top-level symbols). Use it to go straight to the right file:\n" + c.repoMap
}

// cacheKey is a hash of everything before the history. Same prompt and tools
// = same key, so every session in a repo shares one warm cache.
func (c *Client) cacheKey(requestTools []map[string]any) string {
	b, _ := json.Marshal([]any{c.model, c.instructions(), requestTools})
	h := sha256.Sum256(b)
	return "kh_" + hex.EncodeToString(h[:12])
}

func (c *Client) Use(model, effort string) (string, string) {
	if model != "" && model != c.model {
		c.model, c.window = model, 0
	}
	if effort != "" {
		c.effort = effort
	}
	return c.model, c.effort
}

func (c *Client) Stats() provider.Stats {
	if c.window == 0 { // look up once per model, even if it fails
		c.window = c.fetchWindow()
		if c.window == 0 {
			c.window = -1
		}
	}
	s := c.stats
	s.Window = max(c.window, 0)
	c.stats = provider.Stats{}
	return s
}

// fetchWindow looks up c.model's context limit in the Codex catalog. Short
// timeout, 0 on any failure: the meter just shows "used" without a limit.
func (c *Client) fetchWindow() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	access, account, err := Token(ctx)
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

// memoryOriginKey is private saved-state metadata, never a Responses API field.
// Only SetMemory insertion can confer this origin; message text cannot do so.
const memoryOriginKey = "kh_memory"

func hostMemory(item map[string]any) bool {
	if item[memoryOriginKey] != true || item["type"] != "message" || item["role"] != "user" {
		return false
	}
	var parts []struct{ Type, Text string }
	b, err := json.Marshal(item["content"])
	if err != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 1 {
		return false
	}
	return parts[0].Type == "input_text" && strings.HasPrefix(parts[0].Text, provider.MemoryHeader+"\n")
}

// Keep private metadata in history without leaking it to the API or mutating
// maps shared with Compact's private history copy.
func (c *Client) wireInput() []map[string]any {
	input := append([]map[string]any(nil), c.input...)
	for i, item := range input {
		if _, ok := item[memoryOriginKey]; ok {
			input[i] = maps.Clone(item)
			delete(input[i], memoryOriginKey)
		}
	}
	return input
}

type codexState struct {
	Session string           `json:"session"`
	Map     string           `json:"map"`
	Input   []map[string]any `json:"input"`
}

func (c *Client) Save() ([]byte, error) {
	return json.Marshal(codexState{c.session, c.repoMap, c.input})
}

// Load restores the session's repo map, so the prompt stays byte-identical
// and cached; rules still come from today's config (one cache miss after
// you change them).
func (c *Client) Load(b []byte) error {
	var s codexState
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	// Legacy untagged histories remain untrusted, so recall may safely re-inject
	// bounded memories. Invalid metadata must not grant trust to other items.
	for _, item := range s.Input {
		if !hostMemory(item) {
			delete(item, memoryOriginKey)
		}
	}
	c.session, c.input = s.Session, s.Input
	if s.Map != "" {
		c.repoMap = s.Map
	}
	return nil
}

func (c *Client) Replay(out provider.Output) {
	if out == nil {
		return
	}
	for _, it := range c.input {
		// Round-trip through JSON: loaded items are generic maps, so this
		// is the shortest way to read their fields.
		var m struct {
			Type, Role, Name, Arguments, Phase string
			Content                            []struct{ Type, Text string }
			Action                             map[string]any
			Output                             json.RawMessage
		}
		b, _ := json.Marshal(it)
		json.Unmarshal(b, &m)
		switch m.Type {
		case "message":
			// Reference context is neither a typed query nor an assistant reply.
			if m.Role == "developer" {
				continue
			}
			for _, part := range m.Content {
				if part.Type == "input_image" {
					out.Action("[image attached]")
				} else if hostMemory(it) {
					out.Action(fmt.Sprintf("[memory: %d notes]", strings.Count(part.Text, "\n[memory ")))
				} else if m.Role == "user" {
					out.Query(part.Text)
				} else {
					provider.Reply(out, part.Text+"\n", m.Phase)
				}
			}
			provider.FlushReply(out)
		case "function_call_output":
			var parts []struct{ Type string }
			if json.Unmarshal(m.Output, &parts) == nil {
				for _, part := range parts {
					if part.Type == "input_image" {
						out.Action("[image loaded]")
					}
				}
			}
		case "function_call":
			for _, action := range tools.DescribeCall(m.Name, json.RawMessage(m.Arguments)) {
				out.Action(action)
			}
		case "web_search_call":
			if s := webAction(m.Action); s != "" {
				out.Action(s)
			}
		}
	}
}

// LastResponse reads the most recent assistant message, preserving its text
// part boundaries. Presentation callbacks intentionally do not define messages.
func (c *Client) LastResponse() string {
	for i := len(c.input) - 1; i >= 0; i-- {
		var message struct {
			Type, Role string
			Content    []struct{ Type, Text string }
		}
		b, _ := json.Marshal(c.input[i])
		if json.Unmarshal(b, &message) != nil || message.Type != "message" || message.Role != "assistant" {
			continue
		}
		var parts []string
		for _, part := range message.Content {
			if part.Type == "output_text" || part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// LastStepResponse cannot fall back to an older message when a provider accepts
// an empty terminal completion (including a Stop continuation).
func (c *Client) LastStepResponse() string { return c.lastStepResponse }

func terminalResponse(input []map[string]any) string {
	for i := len(input) - 1; i >= 0; i-- {
		var message struct {
			Type, Role, Phase string
			Content           []struct{ Type, Text string }
		}
		b, _ := json.Marshal(input[i])
		if json.Unmarshal(b, &message) != nil || message.Type != "message" || message.Role != "assistant" || message.Phase == "commentary" {
			continue
		}
		var parts []string
		for _, part := range message.Content {
			if part.Type == "output_text" || part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Nil output is useful for session inspection and non-interactive consumers.
type discardOutput struct{}

func (discardOutput) Reply(string)  {}
func (discardOutput) Query(string)  {}
func (discardOutput) Action(string) {}
func (discardOutput) Notice(string) {}

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
