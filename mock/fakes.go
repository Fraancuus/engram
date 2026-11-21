package mock

import (
	"context"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
)

// FakeEmbedder is a programmable engram.Embedder for tests: it returns Vec/Err and
// records the most recently embedded text.
type FakeEmbedder struct {
	Vec  engram.Vector
	Err  error
	Last string
}

// Embed records text and returns the configured vector or error.
func (f *FakeEmbedder) Embed(_ context.Context, text string) (engram.Vector, error) {
	f.Last = text
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Vec, nil
}

// FakeStore is a programmable store double for handler tests: Search returns the
// configured results, and mutating calls are recorded for assertions. It satisfies the
// storage interface the mcp handlers consume.
type FakeStore struct {
	SearchResults []engram.RecallResult
	SearchErr     error
	PutErr        error
	ReinforceErr  error
	LinkErr       error

	Puts          []engram.Memory
	Reinforced    []engram.MemoryID
	Linked        map[engram.MemoryID][]string
	LastSearchNS  []engram.Namespace
	LastSearchVec engram.Vector
	LastSearchK   int

	LinkedEdges       map[engram.MemoryID][]engram.Link
	LinkEdgesErr      error
	NeighborsRes      []engram.Neighbor
	NeighborsErr      error
	LastNeighborSeeds []engram.MemoryID
	LastNeighborScope []engram.Namespace

	// M4 lifecycle recorders + programmable errors/results.
	SupersedeErr     error
	SetForgottenErr  error
	PinErr           error
	DeleteErr        error
	PropagateErr     error
	PruneErr         error
	Superseded       [][]engram.MemoryID
	Forgot           []engram.MemoryID
	PinnedIDs        []engram.MemoryID
	Deleted          []engram.MemoryID
	PruneCands       []engram.Memory
	LastPropagateID  engram.MemoryID
	LastPropagateThr float64

	// Per-id scripts and the sweep's cutoffs. A nil map reads as empty, so the zero value
	// behaves as it did before these fields existed.
	KeptIDs       map[engram.MemoryID]bool  // ids DeleteIfStale reports kept, as for a row changed since the read
	ReinforceErrs map[engram.MemoryID]error // per-id Reinforce errors, checked before ReinforceErr
	Propagated    []engram.MemoryID         // every id given to PropagateReinforce, in call order
	StaleBefore   []time.Time               // the cutoff of every DeleteIfStale call
	PruneBefore   time.Time                 // the cutoff of the last PruneCandidates call

	CountsRes     engram.StoreCounts
	CountsErr     error
	LiveRes       []engram.Memory
	LiveErr       error
	LastCountsNS  engram.Namespace
	LastLiveNS    engram.Namespace
	LastLiveLimit int
}

// Put records m and returns the configured PutErr.
func (f *FakeStore) Put(_ context.Context, m engram.Memory) error {
	f.Puts = append(f.Puts, m)
	return f.PutErr
}

// Search records its arguments and returns the configured results/error.
func (f *FakeStore) Search(_ context.Context, namespaces []engram.Namespace, vec engram.Vector, k int) ([]engram.RecallResult, error) {
	f.LastSearchNS, f.LastSearchVec, f.LastSearchK = namespaces, vec, k
	return f.SearchResults, f.SearchErr
}

// Reinforce records the id and returns ReinforceErrs[id] when that id has one, otherwise
// the configured ReinforceErr.
func (f *FakeStore) Reinforce(_ context.Context, id engram.MemoryID, _ time.Time) error {
	f.Reinforced = append(f.Reinforced, id)
	if err, ok := f.ReinforceErrs[id]; ok {
		return err
	}
	return f.ReinforceErr
}

// LinkEntities records the names under id and returns the configured LinkErr.
func (f *FakeStore) LinkEntities(_ context.Context, id engram.MemoryID, names []string) error {
	if f.Linked == nil {
		f.Linked = make(map[engram.MemoryID][]string)
	}
	f.Linked[id] = names
	return f.LinkErr
}

// Link records the edges under from and returns the configured LinkEdgesErr.
func (f *FakeStore) Link(_ context.Context, from engram.MemoryID, links []engram.Link) error {
	if f.LinkedEdges == nil {
		f.LinkedEdges = make(map[engram.MemoryID][]engram.Link)
	}
	f.LinkedEdges[from] = append(f.LinkedEdges[from], links...)
	return f.LinkEdgesErr
}

// Neighbors records its arguments and returns the configured result/error.
func (f *FakeStore) Neighbors(_ context.Context, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error) {
	f.LastNeighborSeeds, f.LastNeighborScope = seedIDs, scope
	return f.NeighborsRes, f.NeighborsErr
}

// Supersede records the batch of ids and returns the configured error.
func (f *FakeStore) Supersede(_ context.Context, ids []engram.MemoryID) error {
	f.Superseded = append(f.Superseded, ids)
	return f.SupersedeErr
}

// SetForgotten records the id and returns the configured error.
func (f *FakeStore) SetForgotten(_ context.Context, id engram.MemoryID) error {
	f.Forgot = append(f.Forgot, id)
	return f.SetForgottenErr
}

// Pin records the id and returns the configured error.
func (f *FakeStore) Pin(_ context.Context, id engram.MemoryID) error {
	f.PinnedIDs = append(f.PinnedIDs, id)
	return f.PinErr
}

// Delete records the id and returns the configured error.
func (f *FakeStore) Delete(_ context.Context, id engram.MemoryID) error {
	f.Deleted = append(f.Deleted, id)
	return f.DeleteErr
}

// DeleteIfStale records the id in Deleted and the cutoff in StaleBefore. It reports a delete
// unless the id is in KeptIDs or DeleteErr is set, so the zero value deletes as Delete does.
func (f *FakeStore) DeleteIfStale(_ context.Context, id engram.MemoryID, before time.Time) (bool, error) {
	f.Deleted = append(f.Deleted, id)
	f.StaleBefore = append(f.StaleBefore, before)
	return !f.KeptIDs[id] && f.DeleteErr == nil, f.DeleteErr
}

// PropagateReinforce records the id in Propagated, records the id and threshold of the last
// call, and returns the configured error.
func (f *FakeStore) PropagateReinforce(_ context.Context, id engram.MemoryID, threshold float64, _ time.Time) error {
	f.LastPropagateID, f.LastPropagateThr = id, threshold
	f.Propagated = append(f.Propagated, id)
	return f.PropagateErr
}

// PruneCandidates records the cutoff in PruneBefore and returns the configured candidates/error.
func (f *FakeStore) PruneCandidates(_ context.Context, before time.Time, _ int) ([]engram.Memory, error) {
	f.PruneBefore = before
	return f.PruneCands, f.PruneErr
}

// Counts records ns and returns the configured result/error.
func (f *FakeStore) Counts(_ context.Context, ns engram.Namespace) (engram.StoreCounts, error) {
	f.LastCountsNS = ns
	return f.CountsRes, f.CountsErr
}

// LiveMemories records its arguments and returns the configured result/error.
func (f *FakeStore) LiveMemories(_ context.Context, ns engram.Namespace, limit int) ([]engram.Memory, error) {
	f.LastLiveNS, f.LastLiveLimit = ns, limit
	return f.LiveRes, f.LiveErr
}

// FakeReranker is a programmable engram.Reranker for tests: it records the query/docs and
// returns the configured scores or error.
type FakeReranker struct {
	Scores    []float64
	Err       error
	LastQuery string
	LastDocs  []string
}

// Rerank records its arguments and returns the configured scores/error.
func (f *FakeReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	f.LastQuery, f.LastDocs = query, docs
	if f.Err != nil {
		return nil, f.Err
	}
	if len(f.Scores) != len(docs) {
		return nil, fmt.Errorf("FakeReranker misconfigured: %d docs, %d scores", len(docs), len(f.Scores))
	}
	return f.Scores, nil
}

// FakeDecay is a programmable engram.DecayModel for tests: Retrievability returns R and
// Stability returns S regardless of the memory, so handler tests control forgetting.
type FakeDecay struct {
	R float64
	S float64
}

// Retrievability returns the configured R.
func (f FakeDecay) Retrievability(engram.Memory, time.Time) float64 { return f.R }

// Stability returns the configured S.
func (f FakeDecay) Stability(engram.MemoryType, int, float64) float64 { return f.S }
