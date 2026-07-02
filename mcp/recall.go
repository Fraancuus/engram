package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Fraancuus/engram"
)

// Recall result-count bounds.
const (
	defaultK      = 10
	maxK          = 100
	maxNamespaces = 64

	// defaultSeedN is how many vector hits seed the associative expansion; bridgePenalty
	// discounts entity-bridge neighbors relative to the seed that reached them.
	defaultSeedN  = 50
	bridgePenalty = 0.5
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
	RetrievedVia string    `json:"retrieved_via"`
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
func (h *handlers) doRecall(ctx context.Context, in recallInput) (out recallOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("recall: recovered panic", "panic", r)
			out, err = recallOutput{}, errors.New("recall: internal error")
		}
	}()

	if strings.TrimSpace(in.Query) == "" || len(in.Query) > maxContentBytes {
		return recallOutput{}, fmt.Errorf("query must be 1..%d bytes and not blank", maxContentBytes)
	}
	if len(in.Namespaces) > maxNamespaces {
		return recallOutput{}, fmt.Errorf("at most %d namespaces allowed", maxNamespaces)
	}
	namespaces := make([]engram.Namespace, 0, len(in.Namespaces))
	for _, n := range in.Namespaces {
		if strings.TrimSpace(n) == "" || len(n) > maxNamespaceBytes {
			return recallOutput{}, fmt.Errorf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
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
	seeds, err := h.store.Search(ctx, namespaces, vec, h.seedN)
	if err != nil {
		h.log.Error("recall: search failed", "err", err)
		return recallOutput{}, errors.New("recall: store unavailable")
	}
	seedIDs := make([]engram.MemoryID, len(seeds))
	for i, s := range seeds {
		seedIDs[i] = s.ID
	}
	neighbors, err := h.store.Neighbors(ctx, seedIDs, namespaces)
	if err != nil {
		h.log.Error("recall: neighbors failed", "err", err)
		return recallOutput{}, errors.New("recall: store unavailable")
	}
	results := blend(seeds, neighbors, k, bridgePenalty)

	out = recallOutput{Results: make([]recallResultDTO, len(results))}
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
				RetrievedVia: r.RetrievedVia,
			},
		}
	}
	return out, nil
}

// blend merges vector seeds and expansion neighbors into one ranked list by propagated
// scoring: a link neighbor scores sim_source*weight, an entity bridge scores
// sim_source*bridgePenalty, and a memory reached multiple ways keeps its highest score.
// Results are sorted by score (id-tiebroken) and truncated to k.
func blend(seeds []engram.RecallResult, neighbors []engram.Neighbor, k int, bridgePenalty float64) []engram.RecallResult {
	seedSim := make(map[engram.MemoryID]float64, len(seeds))
	best := make(map[engram.MemoryID]engram.RecallResult, len(seeds))
	for _, s := range seeds {
		seedSim[s.ID] = s.Score
		best[s.ID] = engram.RecallResult{Memory: s.Memory, Score: s.Score, RetrievedVia: "vector"}
	}
	for _, n := range neighbors {
		score := seedSim[n.SourceID] * n.Weight
		if strings.HasPrefix(n.Via, "entity:") {
			score = seedSim[n.SourceID] * bridgePenalty
		}
		if cur, ok := best[n.Memory.ID]; !ok || score > cur.Score {
			best[n.Memory.ID] = engram.RecallResult{Memory: n.Memory, Score: score, RetrievedVia: n.Via}
		}
	}
	out := make([]engram.RecallResult, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}
