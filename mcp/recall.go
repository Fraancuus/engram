package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
)

// Recall result-count bounds.
const (
	defaultK      = 10
	maxK          = 100
	maxNamespaces = 64
)

type recallInput struct {
	Query      string   `json:"query" jsonschema:"text to search for"`
	Namespaces []string `json:"namespaces,omitempty" jsonschema:"restrict to these universes; empty means all"`
	K          *int     `json:"k,omitempty" jsonschema:"max results, default 10, capped at 100"`
}

type provenanceDTO struct {
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
	AccessCount  int       `json:"access_count"`
}

type recallResultDTO struct {
	ID         string        `json:"id"`
	Content    string        `json:"content"`
	Score      float64       `json:"score"`
	Type       string        `json:"type"`
	Namespace  string        `json:"namespace"`
	Provenance provenanceDTO `json:"provenance"`
}

type recallOutput struct {
	Results []recallResultDTO `json:"results"`
}

// doRecall validates the query, embeds it, runs a namespace-filtered vector search, and
// maps the results to the response DTO (provenance is projected from each memory's own
// fields). Internal failures are logged and returned sanitized.
func (h *handlers) doRecall(ctx context.Context, in recallInput) (recallOutput, error) {
	if in.Query == "" || len(in.Query) > maxContentBytes {
		return recallOutput{}, fmt.Errorf("query must be 1..%d bytes", maxContentBytes)
	}
	if len(in.Namespaces) > maxNamespaces {
		return recallOutput{}, fmt.Errorf("at most %d namespaces allowed", maxNamespaces)
	}
	namespaces := make([]engram.Namespace, 0, len(in.Namespaces))
	for _, n := range in.Namespaces {
		if n == "" || len(n) > maxNamespaceBytes {
			return recallOutput{}, fmt.Errorf("namespace must be 1..%d bytes", maxNamespaceBytes)
		}
		namespaces = append(namespaces, engram.Namespace(n))
	}
	k := defaultK
	if in.K != nil {
		k = *in.K
	}
	if k < 1 {
		k = 1
	}
	if k > maxK {
		k = maxK
	}

	vec, err := h.embedder.Embed(ctx, in.Query)
	if err != nil {
		h.log.Error("recall: embed failed", "err", err)
		return recallOutput{}, errors.New("recall: embedding failed")
	}
	results, err := h.store.Search(ctx, namespaces, vec, k)
	if err != nil {
		h.log.Error("recall: search failed", "err", err)
		return recallOutput{}, errors.New("recall: store unavailable")
	}

	out := recallOutput{Results: make([]recallResultDTO, len(results))}
	for i, r := range results {
		out.Results[i] = recallResultDTO{
			ID:        string(r.ID),
			Content:   r.Content,
			Score:     r.Score,
			Type:      string(r.Type),
			Namespace: string(r.Namespace),
			Provenance: provenanceDTO{
				Source:       r.Source,
				CreatedAt:    r.CreatedAt,
				LastAccessed: r.LastAccessed,
				AccessCount:  r.AccessCount,
			},
		}
	}
	return out, nil
}
