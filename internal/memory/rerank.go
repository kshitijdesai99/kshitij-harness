package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Reranker scores each candidate against the query, in input order.
// Implementations must honor cancellation and return finite scores in [0, 1].
type Reranker interface {
	Rerank(context.Context, string, []Note) ([]float64, error)
}

type rerankState struct {
	model  Reranker
	notice func(string)
	mu     sync.Mutex
	warned bool
}

// ConfigureReranker enables the second stage. When configured, hybrid retrieval
// requires both embedding and reranking to succeed, otherwise it uses keywords.
// Passing nil disables reranking without deleting notes or cached vectors.
func (s *Store) ConfigureReranker(r Reranker, notice func(string)) error {
	s.rerankMu.Lock()
	defer s.rerankMu.Unlock()
	s.reranker = nil
	if r != nil {
		s.reranker = &rerankState{model: r, notice: notice}
	}
	return nil
}

func (s *Store) rerankConfig() *rerankState {
	s.rerankMu.Lock()
	defer s.rerankMu.Unlock()
	return s.reranker
}

func (r *rerankState) failed(stage string) {
	r.mu.Lock()
	notify := !r.warned && r.notice != nil
	r.warned = true
	r.mu.Unlock()
	if notify {
		r.notice("Hybrid retrieval unavailable (" + stage + " stage); using keyword search.")
	}
}

func (r *rerankState) succeeded() {
	r.mu.Lock()
	r.warned = false
	r.mu.Unlock()
}

// ProbeHybrid verifies both services without transmitting stored memories or
// requiring the repository to already have notes.
func (s *Store) ProbeHybrid(ctx context.Context) error {
	e, r := s.semanticConfig(), s.rerankConfig()
	if e == nil || r == nil {
		return fmt.Errorf("hybrid retrieval requires embeddings and a reranker")
	}
	const query = "Find relevant saved project instructions."
	vectors, err := e.embedder.Embed(ctx, []string{query})
	if err != nil || len(vectors) != 1 || !validVector(vectors[0], 0) {
		return fmt.Errorf("hybrid embedding probe failed")
	}
	scores, err := r.model.Rerank(ctx, query, []Note{{Kind: "instruction", Key: "testing", Text: "Keep project tests in test folders."}})
	if err != nil || !validScores(scores, 1) {
		return fmt.Errorf("hybrid reranker probe failed")
	}
	return nil
}

func validScores(scores []float64, count int) bool {
	if len(scores) != count {
		return false
	}
	for _, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return false
		}
	}
	return true
}

// These are conservative selection heuristics, not calibrated probabilities.
// Local Qwen evaluation retained task constraints near .021 while rejecting
// unrelated topics. The relative floor removes weak tails behind strong matches
// without forcing top-one selection for multi-topic requests.
const (
	minRerankRelevance = 0.02
	minRerankFraction  = 0.1
)

// Covers the managed service's 16s queue + 8s inference, plus HTTP overhead.
const rerankTimeout = 25 * time.Second

type httpReranker struct {
	endpoint, revision string
	client             *http.Client
}

// NewHTTPReranker uses an explicit endpoint without credentials or redirects.
// It never installs models or reads the chat provider's credentials.
func NewHTTPReranker(endpoint string) (Reranker, error) {
	return NewRevisionedHTTPReranker(endpoint, "")
}

// NewRevisionedHTTPReranker pins managed scoring to a helper revision. Empty
// revision preserves compatibility with externally managed reranker APIs.
func NewRevisionedHTTPReranker(endpoint, revision string) (Reranker, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("memory: reranker endpoint must be an HTTP(S) URL without credentials, query or fragment")
	}
	return &httpReranker{endpoint: u.String(), revision: revision, client: &http.Client{
		Timeout:       rerankTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (r *httpReranker) Rerank(ctx context.Context, query string, notes []Note) ([]float64, error) {
	if len(notes) == 0 {
		return nil, nil
	}
	if Secret(query) {
		return nil, fmt.Errorf("memory: refusing to rerank what looks like a secret")
	}
	documents := make([]string, len(notes))
	for i, n := range notes {
		if Secret(n.Key) || Secret(n.Text) || Secret(n.ContextSummary) {
			return nil, fmt.Errorf("memory: refusing to rerank what looks like a secret")
		}
		documents[i] = "Memory kind: " + n.Kind + "\nTopic: " + n.Key + "\nContent: " + n.Text + "\n" + n.ContextSummary
	}
	body, err := json.Marshal(struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}{query, documents})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("memory: invalid reranker request")
	}
	req.Header.Set("Content-Type", "application/json")
	if r.revision != "" {
		req.Header.Set("X-KH-Revision", r.revision)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("memory: reranking request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memory: reranking service returned HTTP %d", resp.StatusCode)
	}
	const max = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil || len(data) > max {
		return nil, fmt.Errorf("memory: invalid reranker response")
	}
	var result struct {
		Revision string `json:"revision"`
		Results  []struct {
			Index *int     `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Results) != len(notes) {
		return nil, fmt.Errorf("memory: invalid reranker results")
	}
	if r.revision != "" && result.Revision != r.revision {
		return nil, fmt.Errorf("memory: reranker helper revision changed; retry /memory hybrid on")
	}
	scores := make([]float64, len(notes))
	seen := make([]bool, len(notes))
	for _, item := range result.Results {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(notes) || seen[*item.Index] || item.Score == nil {
			return nil, fmt.Errorf("memory: invalid reranker results")
		}
		seen[*item.Index] = true
		scores[*item.Index] = *item.Score
	}
	if !validScores(scores, len(notes)) {
		return nil, fmt.Errorf("memory: invalid reranker scores")
	}
	return scores, nil
}

// rerankCandidates bounds expensive pair scoring. Relevant old notes still
// participate; recency is only a tie-breaker after the relevance score.
func rerankCandidates(ctx context.Context, r *rerankState, query string, notes []Note) ([]float64, error) {
	if strings.TrimSpace(query) == "" || Secret(query) {
		return nil, fmt.Errorf("memory: invalid reranker query")
	}
	scores, err := r.model.Rerank(ctx, query, notes)
	if err != nil || !validScores(scores, len(notes)) {
		return nil, fmt.Errorf("memory: reranking unavailable")
	}
	return scores, nil
}
