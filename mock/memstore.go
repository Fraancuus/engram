package mock

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Fraancuus/engram"
)

// These mirror neo4j.neighborCapPerSeed and neo4j.propagateReinforceCap so the eval sees
// the same fan-out bounds as production.
const (
	neighborCap  = 20
	propagateCap = 20
)

// MemStore is a functional in-memory store for the eval and tests. It mirrors the Neo4j
// adapter semantics method for method, so the real mcp handlers and sweep.SweepOnce can
// run against it. It is safe for concurrent use; construct it with NewMemStore.
type MemStore struct {
	mu       sync.RWMutex
	mems     map[engram.MemoryID]*engram.Memory
	order    []engram.MemoryID                               // insertion order, the tie-break
	links    map[engram.MemoryID]map[engram.MemoryID]float64 // kept symmetric
	mentions map[engram.MemoryID]map[string]struct{}
	byEntity map[string]map[engram.MemoryID]struct{}
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		mems:     map[engram.MemoryID]*engram.Memory{},
		links:    map[engram.MemoryID]map[engram.MemoryID]float64{},
		mentions: map[engram.MemoryID]map[string]struct{}{},
		byEntity: map[string]map[engram.MemoryID]struct{}{},
	}
}

// clone deep-copies m so callers and the store never share the embedding slice.
func clone(m engram.Memory) engram.Memory {
	m.Embedding = slices.Clone(m.Embedding)
	return m
}

// project copies m without its embedding, as the Neo4j recall projections do.
func project(m *engram.Memory) engram.Memory {
	c := *m
	c.Embedding = nil
	return c
}

// Put upserts a copy of m, keeping its original insertion position on overwrite.
func (s *MemStore) Put(_ context.Context, m engram.Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mems[m.ID]; !ok {
		s.order = append(s.order, m.ID)
	}
	c := clone(m)
	s.mems[m.ID] = &c
	return nil
}

// Search returns the top k memories by similarity (score descending, insertion order on
// ties), restricted to namespaces when non-empty. Score is (1 + cosine) / 2, the scale
// Neo4j's cosine vector index returns (orthogonal vectors score 0.5); the index's
// quantization noise is not modelled. Like Neo4j it does not filter forgotten or
// superseded memories; k < 1 is treated as 1.
func (s *MemStore) Search(_ context.Context, namespaces []engram.Namespace, vec engram.Vector, k int) ([]engram.RecallResult, error) {
	if k < 1 {
		k = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []engram.RecallResult
	for _, id := range s.order {
		m := s.mems[id]
		if len(namespaces) > 0 && !slices.Contains(namespaces, m.Namespace) {
			continue
		}
		out = append(out, engram.RecallResult{Memory: project(m), Score: (1 + Cosine(vec, m.Embedding)) / 2})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// Reinforce increments access_count and sets last_accessed to now, or returns
// engram.ErrNotFound.
func (s *MemStore) Reinforce(_ context.Context, id engram.MemoryID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mems[id]
	if !ok {
		return fmt.Errorf("reinforce %q: %w", id, engram.ErrNotFound)
	}
	m.AccessCount++
	m.LastAccessed = now
	return nil
}

// LinkEntities idempotently attaches the memory to the named entities; an empty list is a
// no-op and an unknown memory is engram.ErrNotFound.
func (s *MemStore) LinkEntities(_ context.Context, id engram.MemoryID, names []string) error {
	if len(names) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mems[id]; !ok {
		return fmt.Errorf("link entities %q: %w", id, engram.ErrNotFound)
	}
	if s.mentions[id] == nil {
		s.mentions[id] = map[string]struct{}{}
	}
	for _, n := range names {
		s.mentions[id][n] = struct{}{}
		if s.byEntity[n] == nil {
			s.byEntity[n] = map[engram.MemoryID]struct{}{}
		}
		s.byEntity[n][id] = struct{}{}
	}
	return nil
}

// Link idempotently upserts weighted edges from `from`. Unknown targets and self-links are
// skipped, an empty list is a no-op, and an unknown source is engram.ErrNotFound. Edges are
// stored symmetrically because every read treats them as undirected. The edge carries a
// single weight, whereas Neo4j keeps directed LINKS edges with independent weights.
func (s *MemStore) Link(_ context.Context, from engram.MemoryID, links []engram.Link) error {
	if len(links) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mems[from]; !ok {
		return fmt.Errorf("link %q: %w", from, engram.ErrNotFound)
	}
	for _, l := range links {
		if _, ok := s.mems[l.To]; !ok || l.To == from {
			continue
		}
		s.setEdge(from, l.To, l.Weight)
		s.setEdge(l.To, from, l.Weight)
	}
	return nil
}

func (s *MemStore) setEdge(a, b engram.MemoryID, w float64) {
	if s.links[a] == nil {
		s.links[a] = map[engram.MemoryID]float64{}
	}
	s.links[a][b] = w
}

type edge struct {
	id engram.MemoryID
	w  float64
}

// edgesLocked returns id link edges, heaviest first then by id. The caller holds a lock.
func (s *MemStore) edgesLocked(id engram.MemoryID) []edge {
	es := make([]edge, 0, len(s.links[id]))
	for nb, w := range s.links[id] {
		es = append(es, edge{nb, w})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].w != es[j].w {
			return es[i].w > es[j].w
		}
		return es[i].id < es[j].id
	})
	return es
}

// Neighbors returns, per seed, up to neighborCap link neighbors (heaviest first) then up to
// neighborCap entity-bridge rows (by neighbor id, then entity). Seeds are excluded from
// both kinds and scope filters both when non-empty.
func (s *MemStore) Neighbors(_ context.Context, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error) {
	if len(seedIDs) == 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	isSeed := make(map[engram.MemoryID]bool, len(seedIDs))
	for _, id := range seedIDs {
		isSeed[id] = true
	}
	inScope := func(id engram.MemoryID) bool {
		m, ok := s.mems[id]
		return ok && !isSeed[id] && (len(scope) == 0 || slices.Contains(scope, m.Namespace))
	}
	type row struct {
		id     engram.MemoryID
		entity string
	}
	var out []engram.Neighbor
	done := map[engram.MemoryID]bool{}
	for _, seed := range seedIDs {
		if _, ok := s.mems[seed]; !ok || done[seed] {
			continue
		}
		done[seed] = true
		n := 0
		for _, e := range s.edgesLocked(seed) {
			if n == neighborCap {
				break
			}
			if inScope(e.id) {
				out = append(out, engram.Neighbor{Memory: project(s.mems[e.id]), SourceID: seed, Via: "link", Weight: e.w})
				n++
			}
		}
		var rows []row
		for ent := range s.mentions[seed] {
			for nb := range s.byEntity[ent] {
				if inScope(nb) {
					rows = append(rows, row{nb, ent})
				}
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].id != rows[j].id {
				return rows[i].id < rows[j].id
			}
			return rows[i].entity < rows[j].entity
		})
		if len(rows) > neighborCap {
			rows = rows[:neighborCap]
		}
		for _, r := range rows {
			out = append(out, engram.Neighbor{Memory: project(s.mems[r.id]), SourceID: seed, Via: "entity:" + r.entity, Weight: 1.0})
		}
	}
	return out, nil
}

// PropagateReinforce sets last_accessed to now on the id strongest link neighbors
// (weight >= weightThreshold, at most propagateCap) without bumping access_count. Superseded
// neighbors are skipped and do not count toward the cap, so a stale version is not kept alive.
func (s *MemStore) PropagateReinforce(_ context.Context, id engram.MemoryID, weightThreshold float64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.edgesLocked(id) {
		if n == propagateCap || e.w < weightThreshold {
			break // sorted by weight desc, so nothing later qualifies
		}
		if s.mems[e.id].Superseded {
			continue
		}
		s.mems[e.id].LastAccessed = now
		n++
	}
	return nil
}

// Supersede marks each id superseded; absent ids are skipped.
func (s *MemStore) Supersede(_ context.Context, ids []engram.MemoryID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if m, ok := s.mems[id]; ok {
			m.Superseded = true
		}
	}
	return nil
}

// SetForgotten soft-forgets the memory, or returns engram.ErrNotFound.
func (s *MemStore) SetForgotten(_ context.Context, id engram.MemoryID) error {
	return s.flag("set forgotten", id, func(m *engram.Memory) { m.Forgotten = true })
}

// Pin protects the memory from decay, or returns engram.ErrNotFound.
func (s *MemStore) Pin(_ context.Context, id engram.MemoryID) error {
	return s.flag("pin", id, func(m *engram.Memory) { m.Pinned = true })
}

func (s *MemStore) flag(op string, id engram.MemoryID, set func(*engram.Memory)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mems[id]
	if !ok {
		return fmt.Errorf("%s %q: %w", op, id, engram.ErrNotFound)
	}
	set(m)
	return nil
}

// Delete removes the memory with every link edge and entity mention, or returns
// engram.ErrNotFound.
func (s *MemStore) Delete(_ context.Context, id engram.MemoryID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mems[id]; !ok {
		return fmt.Errorf("delete %q: %w", id, engram.ErrNotFound)
	}
	delete(s.mems, id)
	s.order = slices.DeleteFunc(s.order, func(o engram.MemoryID) bool { return o == id })
	for nb := range s.links[id] {
		delete(s.links[nb], id)
	}
	delete(s.links, id)
	for ent := range s.mentions[id] {
		delete(s.byEntity[ent], id)
		if len(s.byEntity[ent]) == 0 {
			delete(s.byEntity, ent)
		}
	}
	delete(s.mentions, id)
	return nil
}

// PruneCandidates returns up to limit non-pinned memories with last_accessed strictly
// before `before`, in insertion order, without embeddings.
func (s *MemStore) PruneCandidates(_ context.Context, before time.Time, limit int) ([]engram.Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []engram.Memory
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		if m := s.mems[id]; m.LastAccessed.Before(before) && !m.Pinned {
			out = append(out, project(m))
		}
	}
	return out, nil
}

// Len reports the number of stored memories.
func (s *MemStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.mems)
}

// EdgeCount reports the number of distinct undirected link edges.
func (s *MemStore) EdgeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, nbs := range s.links {
		n += len(nbs)
	}
	return n / 2
}

// BridgeCount reports the number of distinct unordered memory pairs sharing at least one
// entity.
func (s *MemStore) BridgeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type pair struct{ a, b engram.MemoryID }
	pairs := map[pair]struct{}{}
	for _, ms := range s.byEntity {
		for a := range ms {
			for b := range ms {
				if a < b {
					pairs[pair{a, b}] = struct{}{}
				}
			}
		}
	}
	return len(pairs)
}

// Snapshot returns a copy of the stored memory, embedding included.
func (s *MemStore) Snapshot(id engram.MemoryID) (engram.Memory, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mems[id]
	if !ok {
		return engram.Memory{}, false
	}
	return clone(*m), true
}
