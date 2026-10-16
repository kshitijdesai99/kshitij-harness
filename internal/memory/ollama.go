package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewOllamaEmbedder uses an explicitly configured /api/embed endpoint. It never
// downloads a model, reads chat credentials, or follows redirects with note text.
func NewOllamaEmbedder(endpoint, model string) (Embedder, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("memory: embedding endpoint must be an HTTP(S) URL without credentials, query or fragment")
	}
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 200 || Secret(model) || strings.ContainsAny(model, "\r\n\t ") {
		return nil, fmt.Errorf("memory: an explicit embedding model name is required")
	}
	return &ollamaEmbedder{endpoint: u.String(), model: model, client: &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type ollamaEmbedder struct {
	endpoint, model string
	client          *http.Client
}

func (e *ollamaEmbedder) Model() string {
	return fmt.Sprintf("ollama:%x", sha256.Sum256([]byte(e.endpoint+"\n"+e.model)))
}

func (e *ollamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	for _, text := range texts {
		if Secret(text) {
			return nil, fmt.Errorf("memory: refusing to embed what looks like a secret")
		}
	}
	body, err := json.Marshal(struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Truncate bool     `json:"truncate"`
	}{e.model, texts, false})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("memory: invalid embedding request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("memory: embedding request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memory: embedding service returned HTTP %d", resp.StatusCode)
	}
	const capBytes = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, capBytes+1))
	if err != nil || len(data) > capBytes {
		return nil, fmt.Errorf("memory: embedding response unreadable or too large")
	}
	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("memory: invalid embedding response")
	}
	dim := 0
	for _, v := range result.Embeddings {
		if !validVector(v, dim) {
			return nil, fmt.Errorf("memory: invalid embedding vector")
		}
		dim = len(v)
	}
	return result.Embeddings, nil
}
