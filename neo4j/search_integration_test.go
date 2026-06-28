//go:build integration

package neo4j_test

import (
	"context"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

// oneHot returns a dim-length vector with 1 at idx and 0 elsewhere — gives orthogonal
// directions so cosine ranking in Search is deterministic.
func oneHot(dim, idx int) engram.Vector {
	v := make(engram.Vector, dim)
	v[idx] = 1
	return v
}

func TestStoreSearch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// Unique namespaces isolate this run from leftover nodes in the shared dev DB.
	nsA := engram.Namespace(string(uniqueID("itest-search-a")))
	nsB := engram.Namespace(string(uniqueID("itest-search-b")))
	now := time.Now().UTC()

	mk := func(id string, ns engram.Namespace, vec engram.Vector) engram.Memory {
		return engram.Memory{
			ID: engram.MemoryID(id), Namespace: ns, Type: engram.Semantic,
			Content: id, Embedding: vec, CreatedAt: now, LastAccessed: now,
		}
	}
	mA := mk(string(uniqueID("mA")), nsA, oneHot(384, 0)) // matches the query direction
	mB := mk(string(uniqueID("mB")), nsA, oneHot(384, 1)) // orthogonal to the query
	mC := mk(string(uniqueID("mC")), nsB, oneHot(384, 0)) // matches direction but different namespace
	for _, m := range []engram.Memory{mA, mB, mC} {
		if err := s.Put(ctx, m); err != nil {
			t.Fatalf("Put %s: %v", m.ID, err)
		}
	}

	// Namespace-filtered: the query points at mA's direction. mC shares the direction but
	// lives in nsB, so it must be filtered out.
	got, err := s.Search(ctx, []engram.Namespace{nsA}, oneHot(384, 0), 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Search returned no results")
	}
	if got[0].ID != mA.ID {
		t.Errorf("top result = %q, want %q (exact-direction match should rank first)", got[0].ID, mA.ID)
	}
	if got[0].Score <= got[len(got)-1].Score && len(got) > 1 {
		t.Errorf("results not ranked by descending score: %v", scores(got))
	}
	for _, r := range got {
		if r.Namespace != nsA {
			t.Errorf("result %q in namespace %q — namespace filter leaked (want %q)", r.ID, r.Namespace, nsA)
		}
	}

	// Empty namespaces = search across all universes: both mA and mC (same direction) appear.
	all, err := s.Search(ctx, nil, oneHot(384, 0), 50)
	if err != nil {
		t.Fatalf("Search(all): %v", err)
	}
	ids := map[engram.MemoryID]bool{}
	for _, r := range all {
		ids[r.ID] = true
	}
	if !ids[mA.ID] || !ids[mC.ID] {
		t.Errorf("cross-namespace search missing mA(%v)/mC(%v)", ids[mA.ID], ids[mC.ID])
	}
}

func scores(rs []engram.RecallResult) []float64 {
	out := make([]float64, len(rs))
	for i, r := range rs {
		out[i] = r.Score
	}
	return out
}
