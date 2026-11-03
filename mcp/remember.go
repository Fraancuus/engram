package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
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
	PropagateReinforce(ctx context.Context, id engram.MemoryID, weightThreshold float64, now time.Time) error
	Supersede(ctx context.Context, ids []engram.MemoryID) error
}

// Tools holds the dependencies shared by the remember, recall and forget tools. It is the
// in-process entry point to the retrieval pipeline; NewServer exposes it over MCP.
type Tools struct {
	embedder         engram.Embedder
	reranker         engram.Reranker
	decay            engram.DecayModel
	store            Store
	forget           forgetStore
	clock            engram.Clock
	dedupThreshold   float64
	seedN            int
	rerankCandidates int
	maxTokens        int
	wSim             float64
	wImp             float64
	wRet             float64
	softThreshold    float64
	log              *slog.Logger
	newID            func() (engram.MemoryID, error)
}

// Bounds on untrusted MCP input, enforced at the handler boundary.
const (
	maxContentBytes   = 100_000
	maxNamespaceBytes = 256
	maxEntities       = 64
	maxEntityBytes    = 256
	maxSourceBytes    = 256
	defaultImportance = 0.5

	// defaultDedupThreshold is the index score at or above which a new memory is
	// treated as a duplicate of the nearest existing one in its namespace. The score is
	// the normalised (1+cos)/2 scale, so 0.95 is a raw cosine of 0.90.
	defaultDedupThreshold = 0.95

	// autoLinkK is how many nearest neighbors a new memory considers linking to;
	// linkThreshold is the minimum index score for an auto-link, on the normalised
	// (1+cos)/2 scale: 0.925 is a raw cosine of 0.85.
	autoLinkK     = 5
	linkThreshold = 0.925
)

// RememberInput is the argument set of the remember tool.
type RememberInput struct {
	Content    string   `json:"content" jsonschema:"the memory text to store"`
	Type       string   `json:"type" jsonschema:"memory type: episodic, semantic, or procedural"`
	Namespace  string   `json:"namespace" jsonschema:"the universe to store under, e.g. work/engineering"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"importance from 0 to 1; defaults to 0.5"`
	Source     string   `json:"source,omitempty" jsonschema:"where this memory came from"`
	Entities   []string `json:"entities,omitempty" jsonschema:"entity names this memory mentions"`
	Links      []string `json:"links,omitempty" jsonschema:"ids of existing memories to link this one to"`
	Supersedes []string `json:"supersedes,omitempty" jsonschema:"ids of existing memories this (procedural or semantic) memory replaces"`
}

// RememberOutput is the result of the remember tool.
type RememberOutput struct {
	MemoryID   string   `json:"memory_id"`
	Deduped    bool     `json:"deduped"`
	Superseded []string `json:"superseded,omitempty"`
}

// doRemember validates the input, deduplicates within the namespace (reinforcing an
// existing near-identical memory instead of inserting), and otherwise stores a new
// memory with its entity links. Internal failures are logged and returned as a generic
// error so nothing internal leaks to the caller.
func (t *Tools) doRemember(ctx context.Context, in RememberInput) (out RememberOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error("remember: recovered panic", "panic", r)
			out, err = RememberOutput{}, errors.New("remember: internal error")
		}
	}()

	mt := engram.MemoryType(in.Type)
	if !mt.Valid() {
		return RememberOutput{}, fmt.Errorf("invalid type %q: want episodic, semantic, or procedural", in.Type)
	}
	if strings.TrimSpace(in.Content) == "" || len(in.Content) > maxContentBytes {
		return RememberOutput{}, fmt.Errorf("content must be 1..%d bytes and not blank", maxContentBytes)
	}
	if strings.TrimSpace(in.Namespace) == "" || len(in.Namespace) > maxNamespaceBytes {
		return RememberOutput{}, fmt.Errorf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
	}
	importance := defaultImportance
	if in.Importance != nil {
		importance = *in.Importance
		if math.IsNaN(importance) || math.IsInf(importance, 0) || importance < 0 || importance > 1 {
			return RememberOutput{}, errors.New("importance must be a number between 0 and 1")
		}
	}
	if len(in.Source) > maxSourceBytes {
		return RememberOutput{}, fmt.Errorf("source must be at most %d bytes", maxSourceBytes)
	}
	if len(in.Entities) > maxEntities {
		return RememberOutput{}, fmt.Errorf("at most %d entities allowed", maxEntities)
	}
	for _, e := range in.Entities {
		if strings.TrimSpace(e) == "" || len(e) > maxEntityBytes {
			return RememberOutput{}, fmt.Errorf("entity names must be 1..%d bytes and not blank", maxEntityBytes)
		}
	}
	if len(in.Links) > maxEntities {
		return RememberOutput{}, fmt.Errorf("at most %d links allowed", maxEntities)
	}
	for _, l := range in.Links {
		if strings.TrimSpace(l) == "" || len(l) > maxEntityBytes {
			return RememberOutput{}, fmt.Errorf("link ids must be 1..%d bytes and not blank", maxEntityBytes)
		}
	}
	if len(in.Supersedes) > maxEntities {
		return RememberOutput{}, fmt.Errorf("at most %d supersedes allowed", maxEntities)
	}
	for _, sid := range in.Supersedes {
		if strings.TrimSpace(sid) == "" || len(sid) > maxEntityBytes {
			return RememberOutput{}, fmt.Errorf("supersedes ids must be 1..%d bytes and not blank", maxEntityBytes)
		}
	}

	// Only procedural and semantic writes replace; one value drives both dedup and supersession.
	replaces := in.Supersedes
	if mt == engram.Episodic {
		replaces = nil
	}

	ns := engram.Namespace(in.Namespace)
	vec, err := t.embedder.Embed(ctx, in.Content)
	if err != nil {
		t.log.Error("remember: embed failed", "err", err)
		return RememberOutput{}, errors.New("remember: embedding failed")
	}

	// Search the namespace once: the top live candidate drives dedup; the rest feed auto-linking.
	candidates, err := t.store.Search(ctx, []engram.Namespace{ns}, vec, autoLinkK)
	if err != nil {
		t.log.Error("remember: search failed", "err", err)
		return RememberOutput{}, errors.New("remember: store unavailable")
	}
	if target, ok := t.dedupTarget(candidates, replaces); ok {
		if err := t.store.Reinforce(ctx, target.ID, t.clock.Now()); err != nil {
			t.log.Error("remember: reinforce failed", "err", err)
			return RememberOutput{}, errors.New("remember: store unavailable")
		}
		// Merge any supplied entities onto the existing memory so a retry after a prior
		// LinkEntities failure (or genuinely new mentions) still records them.
		if len(in.Entities) > 0 {
			if err := t.store.LinkEntities(ctx, target.ID, in.Entities); err != nil {
				t.log.Error("remember: link entities on dedup failed", "err", err)
				return RememberOutput{}, errors.New("remember: store unavailable")
			}
		}
		return RememberOutput{MemoryID: string(target.ID), Deduped: true, Superseded: t.applySupersedes(ctx, replaces)}, nil
	}

	id, err := t.newID()
	if err != nil {
		t.log.Error("remember: id generation failed", "err", err)
		return RememberOutput{}, errors.New("remember: internal error")
	}
	now := t.clock.Now()
	m := engram.Memory{
		ID:           id,
		Namespace:    ns,
		Type:         mt,
		Content:      in.Content,
		Embedding:    vec,
		Importance:   importance,
		Stability:    t.decay.Stability(mt, 0, importance),
		AccessCount:  0,
		CreatedAt:    now,
		LastAccessed: now,
		Source:       in.Source,
	}
	if err := t.store.Put(ctx, m); err != nil {
		t.log.Error("remember: put failed", "err", err)
		return RememberOutput{}, errors.New("remember: store unavailable")
	}
	if len(in.Entities) > 0 {
		if err := t.store.LinkEntities(ctx, id, in.Entities); err != nil {
			t.log.Error("remember: link entities failed", "err", err)
			return RememberOutput{}, errors.New("remember: store unavailable")
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
		if err := t.store.Link(ctx, id, links); err != nil {
			t.log.Error("remember: link failed", "err", err)
			return RememberOutput{}, errors.New("remember: store unavailable")
		}
	}

	return RememberOutput{MemoryID: string(id), Superseded: t.applySupersedes(ctx, replaces)}, nil
}

// dedupTarget returns the best candidate a new memory should be merged into: the first, in
// score order, at or above the dedup threshold that is neither superseded nor being replaced.
// shortcut: Search returns autoLinkK=5 candidates, 5 skipped ones hide a live duplicate at rank 6, raise autoLinkK if that shows up
func (t *Tools) dedupTarget(cands []engram.RecallResult, replaces []string) (engram.RecallResult, bool) {
	for _, c := range cands {
		if c.Score < t.dedupThreshold {
			break
		}
		if c.Superseded || slices.Contains(replaces, string(c.ID)) {
			continue
		}
		return c, true
	}
	return engram.RecallResult{}, false
}

// applySupersedes marks ids replaced and returns them, or nil when there are none or the
// store fails. Best-effort: the memory is already stored, so a failure is logged, not returned.
func (t *Tools) applySupersedes(ctx context.Context, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	mids := make([]engram.MemoryID, len(ids))
	for i, sid := range ids {
		mids[i] = engram.MemoryID(sid)
	}
	if err := t.store.Supersede(ctx, mids); err != nil {
		t.log.Error("remember: supersede failed (memory stored)", "err", err)
		return nil
	}
	return ids
}
