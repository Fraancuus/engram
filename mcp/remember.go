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

// Tools holds the dependencies shared by the remember, recall, forget and memory_stats tools. It is the
// in-process entry point to the retrieval pipeline; NewServer exposes it over MCP.
type Tools struct {
	embedder         engram.Embedder
	reranker         engram.Reranker
	decay            engram.DecayModel
	store            Store
	forget           forgetStore
	stats            statsStore
	sweepStats       func() engram.SweepStats
	statsRowCap      int
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
// memory with its entity links. A dedup target deleted between the search and its
// reinforce does not fail the write: the content is stored as a new memory. Internal
// failures are logged and returned as a generic error so nothing internal leaks to the
// caller.
func (t *Tools) doRemember(ctx context.Context, in RememberInput) (out RememberOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error("remember: recovered panic", "panic", r)
			out, err = RememberOutput{}, errors.New("remember: internal error")
		}
	}()

	mt, importance, err := validateRememberScalars(in)
	if err != nil {
		return RememberOutput{}, err
	}
	if err := validateIDList(in.Entities, "entities", "entity names"); err != nil {
		return RememberOutput{}, err
	}
	if err := validateIDList(in.Links, "links", "link ids"); err != nil {
		return RememberOutput{}, err
	}
	if err := validateIDList(in.Supersedes, "supersedes", "supersedes ids"); err != nil {
		return RememberOutput{}, err
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
	if target, ok := t.dedupTarget(candidates, replaces, mt, in.Content); ok {
		out, merged, err := t.mergeDuplicate(ctx, target, in.Entities, replaces)
		if err != nil || merged {
			return out, err
		}
	}
	return t.insertMemory(ctx, in, mt, importance, vec, candidates, replaces)
}

// validateRememberScalars checks the type, content, namespace, importance and source of a
// remember call, in that order, and returns the parsed memory type and the importance
// (defaultImportance when the caller gave none). The error text is safe to show the caller.
func validateRememberScalars(in RememberInput) (engram.MemoryType, float64, error) {
	mt := engram.MemoryType(in.Type)
	if !mt.Valid() {
		return "", 0, fmt.Errorf("invalid type %q: want episodic, semantic, or procedural", in.Type)
	}
	if strings.TrimSpace(in.Content) == "" || len(in.Content) > maxContentBytes {
		return "", 0, fmt.Errorf("content must be 1..%d bytes and not blank", maxContentBytes)
	}
	if strings.TrimSpace(in.Namespace) == "" || len(in.Namespace) > maxNamespaceBytes {
		return "", 0, fmt.Errorf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
	}
	importance, err := validateImportance(in.Importance)
	if err != nil {
		return "", 0, err
	}
	if len(in.Source) > maxSourceBytes {
		return "", 0, fmt.Errorf("source must be at most %d bytes", maxSourceBytes)
	}
	return mt, importance, nil
}

// validateImportance returns the importance to store: defaultImportance when imp is nil,
// otherwise *imp, which must be a finite number between 0 and 1.
func validateImportance(imp *float64) (float64, error) {
	if imp == nil {
		return defaultImportance, nil
	}
	importance := *imp
	if math.IsNaN(importance) || math.IsInf(importance, 0) || importance < 0 || importance > 1 {
		return 0, errors.New("importance must be a number between 0 and 1")
	}
	return importance, nil
}

// validateIDList checks one of the entities, links or supersedes lists: at most maxEntities
// items, each 1..maxEntityBytes bytes and not blank. plural names the field in the count
// error and what names the items in the item error.
func validateIDList(items []string, plural, what string) error {
	if len(items) > maxEntities {
		return fmt.Errorf("at most %d %s allowed", maxEntities, plural)
	}
	for _, s := range items {
		if strings.TrimSpace(s) == "" || len(s) > maxEntityBytes {
			return fmt.Errorf("%s must be 1..%d bytes and not blank", what, maxEntityBytes)
		}
	}
	return nil
}

// mergeDuplicate reinforces the dedup target and links the supplied entities onto it. It
// returns the deduped reply and true when the write merged. It returns false and no error
// when the target was deleted since the search, so the caller stores the content as a new
// memory. Other failures are logged here and returned as the generic store error.
func (t *Tools) mergeDuplicate(ctx context.Context, target engram.RecallResult, entities, replaces []string) (RememberOutput, bool, error) {
	err := t.store.Reinforce(ctx, target.ID, t.clock.Now())
	switch {
	case err == nil:
		// Reinforce has committed last_accessed = now, so the sweep's DeleteIfStale keeps
		// the row while the entities are linked. Merge any supplied entities onto the
		// existing memory so a retry after a prior LinkEntities failure (or genuinely new
		// mentions) still records them.
		if len(entities) > 0 {
			if err := t.store.LinkEntities(ctx, target.ID, entities); err != nil {
				t.log.Error("remember: link entities on dedup failed", "err", err)
				return RememberOutput{}, false, errors.New("remember: store unavailable")
			}
		}
		return RememberOutput{MemoryID: string(target.ID), Deduped: true, Superseded: t.applySupersedes(ctx, replaces)}, true, nil
	case errors.Is(err, engram.ErrNotFound):
		t.log.Debug("remember: dedup target deleted before its reinforce", "id", target.ID)
		// The target was deleted since the search. Fall through and store the content as a
		// new memory: failing here would lose it.
		return RememberOutput{}, false, nil
	default:
		t.log.Error("remember: reinforce failed", "err", err)
		return RememberOutput{}, false, errors.New("remember: store unavailable")
	}
}

// insertMemory stores in as a new memory, links its entities, auto-links it to the similar
// candidates plus any explicit links, and returns the reply with the superseded ids. Store
// failures are logged here and returned as the generic error.
func (t *Tools) insertMemory(ctx context.Context, in RememberInput, mt engram.MemoryType, importance float64, vec engram.Vector, candidates []engram.RecallResult, replaces []string) (RememberOutput, error) {
	id, err := t.newID()
	if err != nil {
		t.log.Error("remember: id generation failed", "err", err)
		return RememberOutput{}, errors.New("remember: internal error")
	}
	now := t.clock.Now()
	m := engram.Memory{
		ID:           id,
		Namespace:    engram.Namespace(in.Namespace),
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
		if c.Score >= linkThreshold && !c.Forgotten {
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
// score order, at or above the dedup threshold that is live (not superseded or soft-forgotten)
// and not being replaced. A replacement dedups only into a memory of its own type with the
// same content (a retried correction), so a correction never folds into a different live
// memory and loses its text.
// shortcut: Search returns autoLinkK=5 candidates, 5 skipped ones hide a live duplicate at rank 6, raise autoLinkK if that shows up
func (t *Tools) dedupTarget(cands []engram.RecallResult, replaces []string, mt engram.MemoryType, content string) (engram.RecallResult, bool) {
	for _, c := range cands {
		if c.Score < t.dedupThreshold {
			break
		}
		if c.Superseded || c.Forgotten || slices.Contains(replaces, string(c.ID)) || (len(replaces) > 0 && (c.Type != mt || c.Content != content)) {
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
		t.log.Error("remember: supersede failed (memory stored)", "err", err, "ids", ids)
		return nil
	}
	return ids
}
