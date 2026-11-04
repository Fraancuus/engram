package mock

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
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
	cosByID := map[engram.MemoryID]float64{"m1": 0.9, "m2": 0.95, "m3": 0.8}
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
			for _, r := range got {
				want := (1 + cosByID[r.ID]) / 2
				if math.Abs(r.Score-want) > 1e-6 {
					t.Fatalf("%s: score %v, want normalized %v", r.ID, r.Score, want)
				}
			}
		})
	}
}

// TestMemStoreSearchNormalizedScore pins the Neo4j cosine index scale (1+cos)/2.
func TestMemStoreSearchNormalizedScore(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s,
		engram.Memory{ID: "same", Namespace: "a", Embedding: engram.Vector{1, 0}, LastAccessed: t0},
		engram.Memory{ID: "ortho", Namespace: "a", Embedding: engram.Vector{0, 1}, LastAccessed: t0},
		engram.Memory{ID: "opposite", Namespace: "a", Embedding: engram.Vector{-1, 0}, LastAccessed: t0})
	got, err := s.Search(ctx, nil, q, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := map[engram.MemoryID]float64{"same": 1, "ortho": 0.5, "opposite": 0}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for _, r := range got {
		if math.Abs(r.Score-want[r.ID]) > 1e-6 {
			t.Fatalf("%s: score %v, want %v", r.ID, r.Score, want[r.ID])
		}
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

func TestMemStorePropagateSkipsSuperseded(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("a", "a", 0.9), mem("live", "a", 0.9), mem("old", "a", 0.9))
	_ = s.Link(ctx, "a", []engram.Link{{To: "live", Weight: 0.9}, {To: "old", Weight: 1.0}})
	_ = s.Supersede(ctx, []engram.MemoryID{"old"})
	t1 := t0.Add(time.Hour)
	if err := s.PropagateReinforce(ctx, "a", 0.85, t1); err != nil {
		t.Fatal(err)
	}
	live, _ := s.Snapshot("live")
	old, _ := s.Snapshot("old")
	if !live.LastAccessed.Equal(t1) || !old.LastAccessed.Equal(t0) {
		t.Fatalf("live=%v old=%v", live.LastAccessed, old.LastAccessed)
	}
}

func TestMemStorePropagateCapIgnoresSuperseded(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	mustPut(t, s, mem("a", "a", 0.9))
	var links []engram.Link
	var dead []engram.MemoryID
	for i := 0; i < 30; i++ {
		id := engram.MemoryID(fmt.Sprintf("n%02d", i))
		mustPut(t, s, mem(string(id), "a", 0.9))
		// 30 strong edges; the 5 heaviest (n25..n29) are superseded, so exactly the
		// 20 heaviest live neighbors (n05..n24) refresh and n00..n04 miss the cap
		w := 0.9 + float64(i)*0.001
		if i >= 25 {
			dead = append(dead, id)
		}
		links = append(links, engram.Link{To: id, Weight: w})
	}
	_ = s.Link(ctx, "a", links)
	_ = s.Supersede(ctx, dead)
	t1 := t0.Add(time.Hour)
	if err := s.PropagateReinforce(ctx, "a", 0.85, t1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		m, _ := s.Snapshot(engram.MemoryID(fmt.Sprintf("n%02d", i)))
		if want := i >= 5 && i < 25; m.LastAccessed.Equal(t1) != want {
			t.Errorf("n%02d refreshed=%v, want %v", i, m.LastAccessed.Equal(t1), want)
		}
	}
}

// statsFixture builds the shared store fixture (see the m6-stats-demo plan); the Neo4j
// integration test asserts the same numbers.
func statsFixture(t *testing.T) *MemStore {
	t.Helper()
	s := NewMemStore()
	m1 := mem("m1", "a", 0.9)
	m1.Type, m1.Superseded, m1.Forgotten = engram.Episodic, true, true
	m1.Content, m1.Source = "c1", "s1"
	m2 := mem("m2", "a", 0.9)
	m2.Type = engram.Episodic
	m3 := mem("m3", "a", 0.9)
	m3.Type, m3.Pinned = engram.Semantic, true
	m4 := mem("m4", "b", 0.9)
	m4.Type = engram.Procedural
	m2.Content, m2.Source, m3.Content, m3.Source, m4.Content, m4.Source = "c2", "s2", "c3", "s3", "c4", "s4"
	mustPut(t, s, m1, m2, m3, m4)
	for id, ents := range map[engram.MemoryID][]string{"m2": {"e", "f"}, "m3": {"e", "f"}, "m4": {"e"}} {
		if err := s.LinkEntities(ctx, id, ents); err != nil {
			t.Fatal(err)
		}
	}
	for from, to := range map[engram.MemoryID]engram.MemoryID{"m2": "m3", "m3": "m2"} {
		if err := s.Link(ctx, from, []engram.Link{{To: to, Weight: 0.9}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Link(ctx, "m3", []engram.Link{{To: "m4", Weight: 0.9}}); err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	groupAEpisodic = engram.CountGroup{Namespace: "a", Type: engram.Episodic, Total: 2, Live: 1, Superseded: 1, Forgotten: 1}
	groupASemantic = engram.CountGroup{Namespace: "a", Type: engram.Semantic, Total: 1, Live: 1, Pinned: 1}
	groupBProc     = engram.CountGroup{Namespace: "b", Type: engram.Procedural, Total: 1, Live: 1}
)

func TestMemStoreCountsFlagsOverlap(t *testing.T) {
	t.Parallel()
	s := statsFixture(t)
	for _, tc := range []struct {
		name          string
		ns            engram.Namespace
		groups        []engram.CountGroup
		edges, bridge int
	}{
		{"a", "a", []engram.CountGroup{groupAEpisodic, groupASemantic}, 2, 3},
		{"b", "b", []engram.CountGroup{groupBProc}, 1, 2},
		{"all", "", []engram.CountGroup{groupAEpisodic, groupASemantic, groupBProc}, 2, 3},
	} {
		got, err := s.Counts(ctx, tc.ns)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(got.Groups, tc.groups) {
			t.Errorf("%s: groups %+v, want %+v", tc.name, got.Groups, tc.groups)
		}
		if got.LinkEdges != tc.edges || got.EntityBridges != tc.bridge {
			t.Errorf("%s: edges %d bridges %d, want %d %d", tc.name, got.LinkEdges, got.EntityBridges, tc.edges, tc.bridge)
		}
	}
}

// A pair counts for a namespace when either endpoint is in it: m3(a)-m4(b) counts for b.
func TestMemStoreCountsScopeEitherEndpoint(t *testing.T) {
	t.Parallel()
	got, err := statsFixture(t).Counts(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got.LinkEdges != 1 || got.EntityBridges != 2 {
		t.Fatalf("edges %d bridges %d, want 1 2", got.LinkEdges, got.EntityBridges)
	}
}

func TestMemStoreCountsUnknownNamespace(t *testing.T) {
	t.Parallel()
	got, err := statsFixture(t).Counts(ctx, "nope")
	if err != nil || len(got.Groups) != 0 || got.LinkEdges != 0 || got.EntityBridges != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestMemStoreLiveMemoriesOrderProjectionLimit(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	for _, id := range []string{"m3", "m1", "m2", "m0"} {
		m := mem(id, "a", 0.9)
		m.Content, m.Source = "c", "src"
		m.LastAccessed = t0.Add(time.Hour)
		m.Superseded = id == "m0"
		mustPut(t, s, m)
	}
	fo := mem("m1a", "a", 0.9)
	fo.Forgotten = true
	other := mem("m0o", "b", 0.9)
	mustPut(t, s, fo, other)
	got, err := s.LiveMemories(ctx, "a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "m2" {
		t.Fatalf("got %+v", got)
	}
	for _, m := range got {
		if m.Embedding != nil || m.Content != "" || m.Source != "" || !m.LastAccessed.Equal(t0.Add(time.Hour)) {
			t.Errorf("bad projection %+v", m)
		}
	}
}

func TestMemStoreStatsReadOnly(t *testing.T) {
	t.Parallel()
	s := statsFixture(t)
	snap := func() []engram.Memory {
		var out []engram.Memory
		for _, id := range []engram.MemoryID{"m1", "m2", "m3", "m4"} {
			m, _ := s.Snapshot(id)
			out = append(out, m)
		}
		return out
	}
	before := snap()
	_, _ = s.Counts(ctx, "")
	_, _ = s.LiveMemories(ctx, "", 10)
	if !reflect.DeepEqual(before, snap()) {
		t.Fatal("stats methods mutated the store")
	}
}

func TestMemStoreStatsConcurrent(t *testing.T) {
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
				_, _ = s.Counts(ctx, "a")
				_, _ = s.LiveMemories(ctx, "a", 5)
			}
		}()
		go func(g int) {
			defer wg.Done()
			for i := g; i < 50; i += 4 {
				_ = s.Delete(ctx, engram.MemoryID(fmt.Sprintf("m%d", i)))
			}
		}(g)
	}
	wg.Wait()
}

func TestMemStoreCountsSingleFlags(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	sup, fog, pin, plain := mem("s", "a", 0.9), mem("f", "a", 0.9), mem("p", "a", 0.9), mem("n", "a", 0.9)
	sup.Superseded, fog.Forgotten, pin.Pinned = true, true, true
	for _, m := range []*engram.Memory{&sup, &fog, &pin, &plain} {
		m.Type = engram.Episodic
	}
	mustPut(t, s, sup, fog, pin, plain)
	got, err := s.Counts(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	want := []engram.CountGroup{{Namespace: "a", Type: engram.Episodic, Total: 4, Live: 2, Superseded: 1, Forgotten: 1, Pinned: 1}}
	if !slices.Equal(got.Groups, want) {
		t.Fatalf("got %+v, want %+v", got.Groups, want)
	}
}
