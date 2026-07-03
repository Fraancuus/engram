package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/Fraancuus/engram"
)

// Store is the storage capability the tool handlers consume. It is defined here, at the
// consumer, and satisfied by the one concrete *neo4j.Store.
type Store interface {
	Put(ctx context.Context, m engram.Memory) error
	Search(ctx context.Context, namespaces []engram.Namespace, vec engram.Vector, k int) ([]engram.RecallResult, error)
	Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error
	LinkEntities(ctx context.Context, id engram.MemoryID, names []string) error
	Link(ctx context.Context, from engram.MemoryID, links []engram.Link) error
	Neighbors(ctx context.Context, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error)
}

// handlers holds the dependencies shared by the MCP tool handlers.
type handlers struct {
	embedder         engram.Embedder
	reranker         engram.Reranker
	store            Store
	clock            engram.Clock
	dedupThreshold   float64
	seedN            int
	rerankCandidates int
	maxTokens        int
	log              *slog.Logger
	newID            func() (engram.MemoryID, error)
}

// Bounds on untrusted MCP input, enforced at the handler boundary.
const (
	maxContentBytes   = 100_000
	maxNamespaceBytes = 256
	maxEntities       = 64
	maxEntityBytes    = 256
	defaultImportance = 0.5

	// defaultDedupThreshold is the cosine similarity at or above which a new memory is
	// treated as a duplicate of the nearest existing one in its namespace.
	defaultDedupThreshold = 0.95

	// autoLinkK is how many nearest neighbors a new memory considers linking to;
	// linkThreshold is the minimum cosine similarity for an auto-link.
	autoLinkK     = 5
	linkThreshold = 0.85
)

type rememberInput struct {
	Content    string   `json:"content" jsonschema:"the memory text to store"`
	Type       string   `json:"type" jsonschema:"memory type: episodic, semantic, or procedural"`
	Namespace  string   `json:"namespace" jsonschema:"the universe to store under, e.g. work/engineering"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"importance from 0 to 1; defaults to 0.5"`
	Source     string   `json:"source,omitempty" jsonschema:"where this memory came from"`
	Entities   []string `json:"entities,omitempty" jsonschema:"entity names this memory mentions"`
	Links      []string `json:"links,omitempty" jsonschema:"ids of existing memories to link this one to"`
}

type rememberOutput struct {
	MemoryID string `json:"memory_id"`
	Deduped  bool   `json:"deduped"`
}

// doRemember validates the input, deduplicates within the namespace (reinforcing an
// existing near-identical memory instead of inserting), and otherwise stores a new
// memory with its entity links. Internal failures are logged and returned as a generic
// error so nothing internal leaks to the caller.
func (h *handlers) doRemember(ctx context.Context, in rememberInput) (out rememberOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("remember: recovered panic", "panic", r)
			out, err = rememberOutput{}, errors.New("remember: internal error")
		}
	}()

	mt := engram.MemoryType(in.Type)
	if !mt.Valid() {
		return rememberOutput{}, fmt.Errorf("invalid type %q: want episodic, semantic, or procedural", in.Type)
	}
	if strings.TrimSpace(in.Content) == "" || len(in.Content) > maxContentBytes {
		return rememberOutput{}, fmt.Errorf("content must be 1..%d bytes and not blank", maxContentBytes)
	}
	if strings.TrimSpace(in.Namespace) == "" || len(in.Namespace) > maxNamespaceBytes {
		return rememberOutput{}, fmt.Errorf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
	}
	importance := defaultImportance
	if in.Importance != nil {
		importance = *in.Importance
		if math.IsNaN(importance) || math.IsInf(importance, 0) || importance < 0 || importance > 1 {
			return rememberOutput{}, errors.New("importance must be a number between 0 and 1")
		}
	}
	if len(in.Entities) > maxEntities {
		return rememberOutput{}, fmt.Errorf("at most %d entities allowed", maxEntities)
	}
	for _, e := range in.Entities {
		if strings.TrimSpace(e) == "" || len(e) > maxEntityBytes {
			return rememberOutput{}, fmt.Errorf("entity names must be 1..%d bytes and not blank", maxEntityBytes)
		}
	}
	if len(in.Links) > maxEntities {
		return rememberOutput{}, fmt.Errorf("at most %d links allowed", maxEntities)
	}
	for _, l := range in.Links {
		if strings.TrimSpace(l) == "" || len(l) > maxEntityBytes {
			return rememberOutput{}, fmt.Errorf("link ids must be 1..%d bytes and not blank", maxEntityBytes)
		}
	}

	ns := engram.Namespace(in.Namespace)
	vec, err := h.embedder.Embed(ctx, in.Content)
	if err != nil {
		h.log.Error("remember: embed failed", "err", err)
		return rememberOutput{}, errors.New("remember: embedding failed")
	}

	// Search the namespace once: candidates[0] drives dedup; the rest feed auto-linking.
	candidates, err := h.store.Search(ctx, []engram.Namespace{ns}, vec, autoLinkK)
	if err != nil {
		h.log.Error("remember: search failed", "err", err)
		return rememberOutput{}, errors.New("remember: store unavailable")
	}
	if len(candidates) > 0 && candidates[0].Score >= h.dedupThreshold {
		if err := h.store.Reinforce(ctx, candidates[0].ID, h.clock.Now()); err != nil {
			h.log.Error("remember: reinforce failed", "err", err)
			return rememberOutput{}, errors.New("remember: store unavailable")
		}
		// Merge any supplied entities onto the existing memory so a retry after a prior
		// LinkEntities failure (or genuinely new mentions) still records them.
		if len(in.Entities) > 0 {
			if err := h.store.LinkEntities(ctx, candidates[0].ID, in.Entities); err != nil {
				h.log.Error("remember: link entities on dedup failed", "err", err)
				return rememberOutput{}, errors.New("remember: store unavailable")
			}
		}
		return rememberOutput{MemoryID: string(candidates[0].ID), Deduped: true}, nil
	}

	id, err := h.newID()
	if err != nil {
		h.log.Error("remember: id generation failed", "err", err)
		return rememberOutput{}, errors.New("remember: internal error")
	}
	now := h.clock.Now()
	m := engram.Memory{
		ID:           id,
		Namespace:    ns,
		Type:         mt,
		Content:      in.Content,
		Embedding:    vec,
		Importance:   importance,
		AccessCount:  0,
		CreatedAt:    now,
		LastAccessed: now,
		Source:       in.Source,
	}
	if err := h.store.Put(ctx, m); err != nil {
		h.log.Error("remember: put failed", "err", err)
		return rememberOutput{}, errors.New("remember: store unavailable")
	}
	if len(in.Entities) > 0 {
		if err := h.store.LinkEntities(ctx, id, in.Entities); err != nil {
			h.log.Error("remember: link entities failed", "err", err)
			return rememberOutput{}, errors.New("remember: store unavailable")
		}
	}

	// Auto-link to the sufficiently-similar candidates, plus any explicit links.
	var links []engram.Link
	for _, c := range candidates {
		if c.Score >= linkThreshold {
			links = append(links, engram.Link{To: c.ID, Weight: c.Score})
		}
	}
	for _, lid := range in.Links {
		links = append(links, engram.Link{To: engram.MemoryID(lid), Weight: 1.0})
	}
	if len(links) > 0 {
		if err := h.store.Link(ctx, id, links); err != nil {
			h.log.Error("remember: link failed", "err", err)
			return rememberOutput{}, errors.New("remember: store unavailable")
		}
	}
	return rememberOutput{MemoryID: string(id), Deduped: false}, nil
}
