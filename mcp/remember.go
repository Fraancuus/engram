package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
}

// handlers holds the dependencies shared by the MCP tool handlers.
type handlers struct {
	embedder       engram.Embedder
	store          Store
	clock          engram.Clock
	dedupThreshold float64
	log            *slog.Logger
	newID          func() (engram.MemoryID, error)
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
)

type rememberInput struct {
	Content    string   `json:"content" jsonschema:"the memory text to store"`
	Type       string   `json:"type" jsonschema:"memory type: episodic, semantic, or procedural"`
	Namespace  string   `json:"namespace" jsonschema:"the universe to store under, e.g. work/engineering"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"importance from 0 to 1; defaults to 0.5"`
	Source     string   `json:"source,omitempty" jsonschema:"where this memory came from"`
	Entities   []string `json:"entities,omitempty" jsonschema:"entity names this memory mentions"`
}

type rememberOutput struct {
	MemoryID string `json:"memory_id"`
	Deduped  bool   `json:"deduped"`
}

// doRemember validates the input, deduplicates within the namespace (reinforcing an
// existing near-identical memory instead of inserting), and otherwise stores a new
// memory with its entity links. Internal failures are logged and returned as a generic
// error so nothing internal leaks to the caller.
func (h *handlers) doRemember(ctx context.Context, in rememberInput) (rememberOutput, error) {
	mt := engram.MemoryType(in.Type)
	if !mt.Valid() {
		return rememberOutput{}, fmt.Errorf("invalid type %q: want episodic, semantic, or procedural", in.Type)
	}
	if in.Content == "" || len(in.Content) > maxContentBytes {
		return rememberOutput{}, fmt.Errorf("content must be 1..%d bytes", maxContentBytes)
	}
	if in.Namespace == "" || len(in.Namespace) > maxNamespaceBytes {
		return rememberOutput{}, fmt.Errorf("namespace must be 1..%d bytes", maxNamespaceBytes)
	}
	importance := defaultImportance
	if in.Importance != nil {
		importance = *in.Importance
		if importance < 0 || importance > 1 {
			return rememberOutput{}, errors.New("importance must be between 0 and 1")
		}
	}
	if len(in.Entities) > maxEntities {
		return rememberOutput{}, fmt.Errorf("at most %d entities allowed", maxEntities)
	}
	for _, e := range in.Entities {
		if e == "" || len(e) > maxEntityBytes {
			return rememberOutput{}, fmt.Errorf("entity names must be 1..%d bytes", maxEntityBytes)
		}
	}

	ns := engram.Namespace(in.Namespace)
	vec, err := h.embedder.Embed(ctx, in.Content)
	if err != nil {
		h.log.Error("remember: embed failed", "err", err)
		return rememberOutput{}, errors.New("remember: embedding failed")
	}

	// Dedup: reinforce the nearest existing memory if it is similar enough.
	hits, err := h.store.Search(ctx, []engram.Namespace{ns}, vec, 1)
	if err != nil {
		h.log.Error("remember: dedup search failed", "err", err)
		return rememberOutput{}, errors.New("remember: store unavailable")
	}
	if len(hits) > 0 && hits[0].Score >= h.dedupThreshold {
		if err := h.store.Reinforce(ctx, hits[0].ID, h.clock.Now()); err != nil {
			h.log.Error("remember: reinforce failed", "err", err)
			return rememberOutput{}, errors.New("remember: store unavailable")
		}
		return rememberOutput{MemoryID: string(hits[0].ID), Deduped: true}, nil
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
	return rememberOutput{MemoryID: string(id), Deduped: false}, nil
}
