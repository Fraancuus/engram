package mock

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q   = engram.Vector{1, 0}
)

// withCos returns a unit vector whose cosine with q is c.
func withCos(c float64) engram.Vector {
	return engram.Vector{float32(c), float32(math.Sqrt(1 - c*c))}
}

func mem(id, ns string, c float64) engram.Memory {
	return engram.Memory{ID: engram.MemoryID(id), Namespace: engram.Namespace(ns), Embedding: withCos(c), LastAccessed: t0}
}

func mustPut(t *testing.T, s *MemStore, ms ...engram.Memory) {
	t.Helper()
	for _, m := range ms {
		if err := s.Put(ctx, m); err != nil {
			t.Fatalf("put %s: %v", m.ID, err)
		}
	}
}

func ids(ms []engram.RecallResult) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, string(m.ID))
	}
	return out
}

func TestMemStoreSearchOrderAndScope(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("m1", "a", 0.9), mem("m2", "b", 0.95), mem("m3", "a", 0.8))
	tests := []struct {
		name string
		ns   []engram.Namespace
		k    int
		want []string
	}{
		{"scoped", []engram.Namespace{"a"}, 10, []string{"m1", "m3"}},
		{"unscoped k1", nil, 1, []string{"m2"}},
		{"k zero acts as one", []engram.Namespace{"a"}, 0, []string{"m1"}},
		{"tie keeps insertion order", nil, 3, []string{"m2", "m1", "m3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.Search(ctx, tt.ns, q, tt.k)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(ids(got)) != fmt.Sprint(tt.want) {
				t.Fatalf("got %v want %v", ids(got), tt.want)
			}
		})
	}
}

func TestMemStoreSearchTieBreakInsertionOrder(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("z", "a", 0.5), mem("a", "a", 0.5))
	got, _ := s.Search(ctx, nil, q, 2)
	if fmt.Sprint(ids(got)) != "[z a]" {
		t.Fatalf("got %v", ids(got))
	}
}

func TestMemStorePutCopies(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	m := mem("m1", "a", 0.9)
	mustPut(t, s, m)
	m.Embedding[0] = 0
	snap, ok := s.Snapshot("m1")
	if !ok || snap.Embedding[0] == 0 {
		t.Fatalf("store aliased caller embedding: %v %v", snap, ok)
	}
	snap.Embedding[0] = 0
	again, _ := s.Snapshot("m1")
	if again.Embedding[0] == 0 {
		t.Fatal("snapshot aliased stored embedding")
	}
}

func TestMemStoreSearchIncludesForgotten(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	f, sup := mem("f", "a", 0.9), mem("s", "a", 0.8)
	f.Forgotten, sup.Superseded = true, true
	mustPut(t, s, f, sup)
	got, _ := s.Search(ctx, nil, q, 10)
	if fmt.Sprint(ids(got)) != "[f s]" {
		t.Fatalf("got %v", ids(got))
	}
}

func neighborIDs(ns []engram.Neighbor) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, string(n.Memory.ID)+"/"+n.Via)
	}
	return out
}

func TestMemStoreNeighborsScope(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("seed", "a", 0.9), mem("n1", "a", 0.5), mem("n2", "b", 0.5))
	for _, id := range []engram.MemoryID{"seed", "n1", "n2"} {
		if err := s.LinkEntities(ctx, id, []string{"e"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Neighbors(ctx, []engram.MemoryID{"seed"}, []engram.Namespace{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Memory.ID != "n1" || got[0].Via != "entity:e" || got[0].Weight != 1 || got[0].SourceID != "seed" {
		t.Fatalf("got %+v", got)
	}
	all, _ := s.Neighbors(ctx, []engram.MemoryID{"seed"}, nil)
	if len(all) != 2 {
		t.Fatalf("unscoped got %v", neighborIDs(all))
	}
}

func TestMemStoreNeighborsSeedsExcluded(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("s1", "a", 0.9), mem("s2", "a", 0.9), mem("x", "a", 0.9))
	_ = s.Link(ctx, "s1", []engram.Link{{To: "s2", Weight: 0.9}, {To: "x", Weight: 0.5}})
	for _, id := range []engram.MemoryID{"s1", "s2", "x"} {
		_ = s.LinkEntities(ctx, id, []string{"e"})
	}
	got, _ := s.Neighbors(ctx, []engram.MemoryID{"s1", "s2"}, nil)
	for _, n := range got {
		if n.Memory.ID != "x" {
			t.Fatalf("seed leaked as neighbor: %v", neighborIDs(got))
		}
	}
	// s1: link x + bridge x; s2: bridge x only (its sole link is the seed s1)
	if len(got) != 3 {
		t.Fatalf("got %v", neighborIDs(got))
	}
}

func TestMemStoreNeighborsLinkCap(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("seed", "a", 0.9))
	var links []engram.Link
	for i := 0; i < 25; i++ {
		id := engram.MemoryID(fmt.Sprintf("n%02d", i))
		mustPut(t, s, mem(string(id), "a", 0.5))
		links = append(links, engram.Link{To: id, Weight: float64(i+1) / 100})
	}
	if err := s.Link(ctx, "seed", links); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Neighbors(ctx, []engram.MemoryID{"seed"}, nil)
	if len(got) != 20 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].Weight != 0.25 || got[19].Weight != 0.06 {
		t.Fatalf("not the 20 heaviest: first %v last %v", got[0].Weight, got[19].Weight)
	}
}

func TestMemStoreNeighborsTwoEntities(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("seed", "a", 0.9), mem("n1", "a", 0.5))
	_ = s.LinkEntities(ctx, "seed", []string{"e2", "e1"})
	_ = s.LinkEntities(ctx, "n1", []string{"e1", "e2"})
	got, _ := s.Neighbors(ctx, []engram.MemoryID{"seed"}, nil)
	if fmt.Sprint(neighborIDs(got)) != "[n1/entity:e1 n1/entity:e2]" {
		t.Fatalf("got %v", neighborIDs(got))
	}
}

func TestMemStoreDeleteCascades(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("m", "a", 0.9), mem("a", "a", 0.9), mem("b", "a", 0.9), mem("c", "a", 0.9))
	_ = s.Link(ctx, "m", []engram.Link{{To: "a", Weight: 0.9}, {To: "b", Weight: 0.9}})
	_ = s.Link(ctx, "c", []engram.Link{{To: "m", Weight: 0.9}})
	_ = s.Link(ctx, "a", []engram.Link{{To: "b", Weight: 0.9}})
	for _, id := range []engram.MemoryID{"m", "a", "b"} {
		_ = s.LinkEntities(ctx, id, []string{"e"})
	}
	if s.EdgeCount() != 4 || s.BridgeCount() != 3 {
		t.Fatalf("setup edges=%d bridges=%d", s.EdgeCount(), s.BridgeCount())
	}
	if err := s.Delete(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if s.EdgeCount() != 1 || s.BridgeCount() != 1 || s.Len() != 3 {
		t.Fatalf("edges=%d bridges=%d len=%d", s.EdgeCount(), s.BridgeCount(), s.Len())
	}
	for _, id := range []engram.MemoryID{"a", "b", "c"} {
		got, _ := s.Neighbors(ctx, []engram.MemoryID{id}, nil)
		for _, n := range got {
			if n.Memory.ID == "m" {
				t.Fatalf("%s still sees deleted m", id)
			}
		}
	}
	if got, _ := s.Search(ctx, nil, q, 10); len(got) != 3 {
		t.Fatalf("search still returns deleted: %v", ids(got))
	}
}

func TestMemStorePruneBoundary(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	at, early := mem("at", "a", 0.9), mem("early", "a", 0.9)
	at.LastAccessed = t0
	early.LastAccessed = t0.Add(-time.Second)
	mustPut(t, s, at, early)
	got, _ := s.PruneCandidates(ctx, t0, 10)
	if len(got) != 1 || got[0].ID != "early" || got[0].Embedding != nil {
		t.Fatalf("got %+v", got)
	}
	if lim, _ := s.PruneCandidates(ctx, t0.Add(time.Hour), 1); len(lim) != 1 || lim[0].ID != "at" {
		t.Fatalf("limit/order: %+v", lim)
	}
}

func TestMemStorePrunePinned(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	p := mem("p", "a", 0.9)
	p.Pinned = true
	mustPut(t, s, p)
	if got, _ := s.PruneCandidates(ctx, t0.Add(time.Hour), 10); len(got) != 0 {
		t.Fatalf("pinned pruned: %+v", got)
	}
}

func TestMemStoreNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	tests := []struct {
		name string
		call func() error
	}{
		{"SetForgotten", func() error { return s.SetForgotten(ctx, "nope") }},
		{"Pin", func() error { return s.Pin(ctx, "nope") }},
		{"Delete", func() error { return s.Delete(ctx, "nope") }},
		{"Reinforce", func() error { return s.Reinforce(ctx, "nope", t0) }},
		{"LinkEntities", func() error { return s.LinkEntities(ctx, "nope", []string{"e"}) }},
		{"Link", func() error { return s.Link(ctx, "nope", []engram.Link{{To: "x", Weight: 1}}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.call(); !errors.Is(err, engram.ErrNotFound) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestMemStoreSupersedeIdempotent(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("m", "a", 0.9))
	for i := 0; i < 2; i++ {
		if err := s.Supersede(ctx, []engram.MemoryID{"m", "absent"}); err != nil {
			t.Fatal(err)
		}
	}
	if snap, _ := s.Snapshot("m"); !snap.Superseded {
		t.Fatal("not superseded")
	}
}

func TestMemStoreBridgeCount(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("m1", "a", 0.9), mem("m2", "a", 0.9), mem("m3", "a", 0.9))
	for _, id := range []engram.MemoryID{"m1", "m2", "m3"} {
		_ = s.LinkEntities(ctx, id, []string{"e", "f"}) // second shared entity must not double count
	}
	if s.BridgeCount() != 3 {
		t.Fatalf("got %d", s.BridgeCount())
	}
}

func TestMemStoreReinforceAndPropagate(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("a", "a", 0.9), mem("strong", "a", 0.9), mem("weak", "a", 0.9))
	_ = s.Link(ctx, "a", []engram.Link{{To: "strong", Weight: 0.9}, {To: "weak", Weight: 0.1}})
	now := t0.Add(time.Hour)
	if err := s.Reinforce(ctx, "a", now); err != nil {
		t.Fatal(err)
	}
	if err := s.PropagateReinforce(ctx, "a", 0.5, now); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Snapshot("a")
	st, _ := s.Snapshot("strong")
	w, _ := s.Snapshot("weak")
	if a.AccessCount != 1 || !a.LastAccessed.Equal(now) || !st.LastAccessed.Equal(now) || st.AccessCount != 0 || !w.LastAccessed.Equal(t0) {
		t.Fatalf("a=%+v strong=%+v weak=%+v", a, st, w)
	}
}

func TestMemStoreConcurrentSearchDelete(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	for i := 0; i < 50; i++ {
		mustPut(t, s, mem(fmt.Sprintf("m%d", i), "a", 0.9))
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.Search(ctx, nil, q, 5)
				_, _ = s.Neighbors(ctx, []engram.MemoryID{"m0"}, nil)
				_ = s.EdgeCount()
			}
		}()
		go func(g int) {
			defer wg.Done()
			for i := g; i < 50; i += 4 {
				id := engram.MemoryID(fmt.Sprintf("m%d", i))
				_ = s.Link(ctx, id, []engram.Link{{To: "m0", Weight: 0.9}})
				_ = s.Delete(ctx, id)
			}
		}(g)
	}
	wg.Wait()
	if s.Len() != 0 {
		t.Fatalf("len %d", s.Len())
	}
}
