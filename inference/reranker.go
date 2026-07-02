package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Fraancuus/engram"
)

var _ engram.Reranker = (*Reranker)(nil)

// Reranker is an engram.Reranker backed by an HF Text Embeddings Inference (TEI) sidecar
// serving a cross-encoder over its /rerank endpoint. Construct it with NewReranker; it is
// safe for concurrent use.
type Reranker struct {
	httpClient *http.Client
	baseURL    string
}

// NewReranker returns a Reranker that talks to the TEI rerank sidecar at baseURL (e.g.
// "http://localhost:8081").
func NewReranker(baseURL string) *Reranker {
	return &Reranker{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    baseURL,
	}
}

type rerankRequest struct {
	Query string   `json:"query"`
	Texts []string `json:"texts"`
}

// Rerank returns a relevance score for each doc against query, aligned to the input order
// (TEI returns results by descending score, so the client realigns them). Errors are
// wrapped and never echo the sidecar's response body.
func (r *Reranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return []float64{}, nil
	}
	body, err := json.Marshal(rerankRequest{Query: query, Texts: docs})
	if err != nil {
		return nil, fmt.Errorf("rerank marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("rerank: tei returned status %d", resp.StatusCode)
	}

	var out []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("rerank decode response: %w", err)
	}
	if len(out) != len(docs) {
		return nil, fmt.Errorf("rerank: want %d scores, got %d", len(docs), len(out))
	}

	scores := make([]float64, len(docs))
	for _, item := range out {
		if item.Index < 0 || item.Index >= len(docs) {
			return nil, fmt.Errorf("rerank: result index %d out of range", item.Index)
		}
		scores[item.Index] = item.Score
	}
	return scores, nil
}
