package mock

import (
	"context"
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

// Reinforce records the id and returns the configured ReinforceErr.
func (f *FakeStore) Reinforce(_ context.Context, id engram.MemoryID, _ time.Time) error {
	f.Reinforced = append(f.Reinforced, id)
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
