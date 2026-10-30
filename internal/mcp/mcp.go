// Package mcp connects explicitly trusted external MCP servers. Callers must
// obtain startup approval before passing servers and apply per-call approval.
package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"kh/internal/provider"
	"kh/internal/tools"
)

// Server is independent of project configuration. Dir is the stdio working directory.
type Server struct {
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	Type    string
	Dir     string
}

const (
	startupTimeout  = 15 * time.Second
	callTimeout     = 60 * time.Second
	maxResponse     = 8 << 20
	maxOutput       = 256 << 10
	maxPages        = 1000
	maxTools        = 4096
	protocolVersion = "2025-03-26"
)

type transport interface {
	request(context.Context, string, any) (json.RawMessage, error)
	notify(context.Context, string, any) error
	version(string)
	close()
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func result(m rpcMessage) (json.RawMessage, error) {
	if m.JSONRPC != "2.0" {
		return nil, fmt.Errorf("invalid MCP JSON-RPC version")
	}
	if m.Error != nil {
		return nil, fmt.Errorf("MCP RPC error %d", m.Error.Code)
	}
	if len(m.Result) == 0 {
		return nil, fmt.Errorf("missing MCP result")
	}
	return m.Result, nil
}

// Connect exposes tools and lazy resource/prompt operations from preapproved servers, retaining successful servers
// when others fail. Each startup has a 15s deadline; calls have a 60s deadline.
// Cleanup is idempotent and must be called. Canceling ctx also closes connections.
// Warnings deliberately omit commands, URLs, headers, stderr and remote messages.
func Connect(ctx context.Context, servers map[string]Server) ([]tools.Tool, func(), []string) {
	var ts []tools.Tool
	var warnings []string
	var connections []transport
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	used := map[string]bool{}
	for _, name := range names {
		s, err := expand(servers[name])
		var tr transport
		if err == nil {
			switch strings.ToLower(s.Type) {
			case "", "stdio", "http", "streamable-http":
				if s.Type == "" && s.URL != "" || strings.ToLower(s.Type) == "http" || strings.ToLower(s.Type) == "streamable-http" {
					tr, err = newHTTP(ctx, s)
				} else {
					tr, err = newStdio(ctx, s)
				}
			case "sse":
				err = fmt.Errorf("legacy SSE transport is unsupported; use streamable HTTP")
			default:
				err = fmt.Errorf("unsupported MCP transport")
			}
		}
		if err == nil {
			startup, cancel := context.WithTimeout(ctx, startupTimeout)
			var discovered []remoteTool
			var capabilities map[string]json.RawMessage
			capabilities, err = initialize(startup, tr)
			if _, ok := capabilities["tools"]; err == nil && ok {
				discovered, err = discover(startup, tr)
			}
			cancel()
			if err == nil {
				connections = append(connections, tr)
				ts = append(ts, contentTools(name, tr, capabilities, used)...)
				sort.SliceStable(discovered, func(i, j int) bool { return discovered[i].Name < discovered[j].Name })
				for _, remote := range discovered {
					remote := remote
					toolName := uniqueName(name, remote.Name, used)
					props, _ := remote.InputSchema["properties"].(map[string]any)
					var required []string
					if values, ok := remote.InputSchema["required"].([]any); ok {
						for _, value := range values {
							if v, ok := value.(string); ok {
								required = append(required, v)
							}
						}
					}
					run := func(ctx context.Context, input json.RawMessage) (provider.Result, error) {
						var args map[string]json.RawMessage
						if len(input) == 0 {
							input = json.RawMessage(`{}`)
						}
						if len(input) > maxResponse || json.Unmarshal(input, &args) != nil || args == nil {
							return provider.Result{}, fmt.Errorf("MCP arguments must be a JSON object within size limit")
						}
						callCtx, cancel := context.WithTimeout(ctx, callTimeout)
						defer cancel()
						raw, err := tr.request(callCtx, "tools/call", map[string]any{"name": remote.Name, "arguments": input})
						if err != nil {
							return provider.Result{}, err
						}
						return renderResult(raw)
					}
					ts = append(ts, tools.Tool{Name: toolName, Description: remote.Description, InputSchema: remote.InputSchema, Params: props, Required: required,
						RunResult: run,
						Run: func(ctx context.Context, input json.RawMessage) (string, error) {
							result, err := run(ctx, input)
							if err == nil && result.IsError {
								err = fmt.Errorf("MCP tool reported an error")
							}
							return result.Output, err
						}})
				}
			}
		}
		if err != nil {
			if tr != nil {
				tr.close()
			}
			warnings = append(warnings, fmt.Sprintf("MCP server %q: %s", name, err))
		}
	}
	return ts, func() {
		for _, tr := range connections {
			tr.close()
		}
	}, warnings
}

type remoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func initialize(ctx context.Context, tr transport) (map[string]json.RawMessage, error) {
	raw, err := tr.request(ctx, "initialize", map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "kh", "version": "0.1"}})
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &init) != nil || init.Capabilities == nil {
		return nil, fmt.Errorf("invalid MCP initialize response")
	}
	for _, feature := range []string{"tools", "resources", "prompts"} {
		if value, ok := init.Capabilities[feature]; ok {
			var capability map[string]json.RawMessage
			if json.Unmarshal(value, &capability) != nil || capability == nil {
				return nil, fmt.Errorf("invalid MCP server capability")
			}
		}
	}
	switch init.ProtocolVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
	default:
		return nil, fmt.Errorf("unsupported MCP protocol version")
	}
	tr.version(init.ProtocolVersion)
	if err := tr.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	return init.Capabilities, nil
}

func discover(ctx context.Context, tr transport) ([]remoteTool, error) {
	var all []remoteTool
	seenCursors := map[string]bool{}
	seenTools := map[string]bool{}
	cursor := ""
	discoveryBytes := 0
	for page := 0; page < maxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := tr.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		discoveryBytes += len(raw)
		if discoveryBytes > maxResponse {
			return nil, fmt.Errorf("MCP discovery exceeds size limit")
		}
		var list struct {
			Tools      []remoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber() // schema integer/decimal constraints must not lose precision
		if decoder.Decode(&list) != nil || list.Tools == nil {
			return nil, fmt.Errorf("invalid MCP tools/list response")
		}
		for _, t := range list.Tools {
			if t.Name == "" || seenTools[t.Name] || t.InputSchema == nil || t.InputSchema["type"] != "object" {
				return nil, fmt.Errorf("invalid or duplicate MCP tool schema")
			}
			seenTools[t.Name] = true
			all = append(all, t)
			if len(all) > maxTools {
				return nil, fmt.Errorf("MCP tool count exceeds limit")
			}
		}
		cursor = list.NextCursor
		if cursor == "" {
			return all, nil
		}
		if seenCursors[cursor] {
			return nil, fmt.Errorf("MCP pagination cursor repeated")
		}
		seenCursors[cursor] = true
	}
	return nil, fmt.Errorf("MCP pagination exceeds limit")
}

func renderResult(raw json.RawMessage) (provider.Result, error) {
	var value struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if json.Unmarshal(raw, &value) != nil || (value.Content == nil && len(value.StructuredContent) == 0) {
		return provider.Result{}, fmt.Errorf("invalid MCP tools/call response")
	}
	var parts []string
	for _, content := range value.Content {
		var block struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &block) != nil {
			return provider.Result{}, fmt.Errorf("invalid MCP content")
		}
		if block.Type == "text" {
			parts = append(parts, block.Text)
		} else {
			parts = append(parts, string(content))
		}
	}
	if len(value.StructuredContent) != 0 {
		parts = append(parts, string(value.StructuredContent))
	}
	out := strings.Join(parts, "\n")
	if len(out) > maxOutput {
		out = strings.ToValidUTF8(out[:maxOutput], "") + "\n...[MCP output truncated]"
	}
	return provider.Result{Output: out, IsError: value.IsError}, nil
}

func expand(s Server) (Server, error) {
	missing := false
	expandValue := func(value string) string {
		return os.Expand(value, func(key string) string {
			v, ok := os.LookupEnv(key)
			if !ok {
				missing = true
			}
			return v
		})
	}
	s.Command, s.URL, s.Dir = expandValue(s.Command), expandValue(s.URL), expandValue(s.Dir)
	s.Args = append([]string(nil), s.Args...)
	for i, arg := range s.Args {
		s.Args[i] = expandValue(arg)
	}
	env := map[string]string{}
	for key, value := range s.Env {
		env[key] = expandValue(value)
	}
	s.Env = env
	headers := map[string]string{}
	for key, value := range s.Headers {
		headers[key] = expandValue(value)
	}
	s.Headers = headers
	if missing {
		return s, fmt.Errorf("required environment variable is missing")
	}
	return s, nil
}

func sanitize(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
func uniqueName(server, tool string, used map[string]bool) string {
	base := "mcp__" + sanitize(server) + "__" + sanitize(tool)
	name := base
	for i := 0; len(name) > 64 || used[name]; i++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", server, tool, i)))
		name = fmt.Sprintf("%s_%x", base[:min(len(base), 47)], hash[:8])
	}
	used[name] = true
	return name
}
