package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type httpTransport struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	session  string
	protocol string
	next     atomic.Uint64
	once     sync.Once
}

func newHTTP(ctx context.Context, s Server) (transport, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || s.Command != "" {
		return nil, fmt.Errorf("streamable HTTP requires an http(s) URL without credentials or fragment and no command")
	}
	lifetime, cancel := context.WithCancel(ctx)
	t := &httpTransport{endpoint: s.URL, headers: s.Headers, ctx: lifetime, cancel: cancel, client: &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("MCP redirects are disabled") }}}
	go func() { <-lifetime.Done(); t.close() }()
	return t, nil
}
func (t *httpTransport) version(v string) { t.mu.Lock(); t.protocol = v; t.mu.Unlock() }
func (t *httpTransport) close() {
	t.once.Do(func() {
		t.cancel()
		t.mu.Lock()
		session := t.session
		t.mu.Unlock()
		if session != "" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			req, err := t.makeRequest(ctx, http.MethodDelete, nil)
			if err == nil {
				if resp, err := t.client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
			cancel()
		}
		t.client.CloseIdleConnections()
	})
}
func (t *httpTransport) makeRequest(ctx context.Context, method string, data []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid MCP HTTP request")
	}
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	if t.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", t.protocol)
	}
	t.mu.Unlock()
	return req, nil
}
func (t *httpTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := strconv.FormatUint(t.next.Add(1), 10)
	m, err := t.post(ctx, map[string]any{"jsonrpc": "2.0", "id": json.Number(id), "method": method, "params": params}, id, method == "initialize")
	if err != nil {
		return nil, err
	}
	return result(m)
}
func (t *httpTransport) notify(ctx context.Context, method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	_, err := t.post(ctx, message, "", false)
	return err
}
func (t *httpTransport) post(ctx context.Context, message any, id string, initialize bool) (rpcMessage, error) {
	var empty rpcMessage
	if err := t.ctx.Err(); err != nil {
		return empty, fmt.Errorf("MCP HTTP connection closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.ctx, cancel)
	defer func() { stop(); cancel() }()
	data, err := json.Marshal(message)
	if err != nil || len(data) > maxResponse {
		return empty, fmt.Errorf("MCP request exceeds size limit")
	}
	req, err := t.makeRequest(ctx, http.MethodPost, data)
	if err != nil {
		return empty, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, fmt.Errorf("MCP HTTP request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			return empty, fmt.Errorf("MCP HTTP status %d (legacy SSE endpoints are unsupported)", resp.StatusCode)
		}
		return empty, fmt.Errorf("MCP HTTP status %d", resp.StatusCode)
	}
	if initialize {
		session := resp.Header.Get("Mcp-Session-Id")
		if len(session) > 4096 || strings.IndexFunc(session, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
			return empty, fmt.Errorf("invalid MCP session ID")
		}
		t.mu.Lock()
		t.session = session
		t.mu.Unlock()
	}
	if id == "" {
		return empty, nil
	}
	if resp.StatusCode == http.StatusAccepted {
		return empty, fmt.Errorf("MCP request accepted without a result; standalone SSE is unsupported")
	}
	typ, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch typ {
	case "application/json":
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		if err != nil {
			if ctx.Err() != nil {
				return empty, ctx.Err()
			}
			return empty, fmt.Errorf("cannot read MCP HTTP response")
		}
		if len(body) > maxResponse {
			return empty, fmt.Errorf("MCP response exceeds size limit")
		}
		var m rpcMessage
		if json.Unmarshal(body, &m) != nil || m.Method != "" || string(m.ID) != id {
			return empty, fmt.Errorf("invalid or mismatched MCP HTTP response")
		}
		return m, nil
	case "text/event-stream":
		return t.readSSE(ctx, resp.Body, id)
	default:
		return empty, fmt.Errorf("unsupported MCP HTTP content type; legacy SSE discovery is unsupported")
	}
}
func (t *httpTransport) readSSE(ctx context.Context, reader io.Reader, id string) (rpcMessage, error) {
	limited := &io.LimitedReader{R: reader, N: maxResponse + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxResponse+1)
	var data strings.Builder
	var empty rpcMessage
	for scanner.Scan() {
		if limited.N == 0 {
			return empty, fmt.Errorf("MCP response exceeds size limit")
		}
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var m rpcMessage
			raw := strings.TrimSuffix(data.String(), "\n")
			data.Reset()
			if json.Unmarshal([]byte(raw), &m) != nil || m.JSONRPC != "2.0" {
				return empty, fmt.Errorf("invalid MCP SSE message")
			}
			if m.Method != "" {
				if len(m.ID) != 0 {
					reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
					if m.Method == "ping" {
						reply["result"] = map[string]any{}
					} else {
						reply["error"] = map[string]any{"code": -32601, "message": "Client method not supported"}
					}
					if _, err := t.post(ctx, reply, "", false); err != nil {
						return empty, err
					}
				}
				continue
			}
			if string(m.ID) == id {
				return m, nil
			}
			// Each POST owns its own response stream. A different ID cannot be
			// dispatched safely to a sibling POST; reject instead of misrouting.
			return empty, fmt.Errorf("mismatched MCP SSE response ID")
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if limited.N == 0 {
		return empty, fmt.Errorf("MCP response exceeds size limit")
	}
	return empty, fmt.Errorf("MCP SSE stream ended without a result")
}
