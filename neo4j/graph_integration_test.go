//go:build integration

package neo4j_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

// putBare stores a minimal memory (384-dim embedding) for graph tests.
func putBare(t *testing.T, s *eneo4j.Store, id engram.MemoryID, ns engram.Namespace) {
	t.Helper()
	now := time.Now().UTC()
	if err := s.Put(context.Background(), engram.Memory{
		ID: id, Namespace: ns, Type: engram.Semantic, Content: string(id),
		Embedding: embedding(384), CreatedAt: now, LastAccessed: now,
	}); err != nil {
		t.Fatalf("Put %s: %v", id, err)
	}
}

func TestStoreLink(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-link")))
	a, b, c := uniqueID("A"), uniqueID("B"), uniqueID("C")
	for _, id := range []engram.MemoryID{a, b, c} {
		putBare(t, s, id, ns)
	}

	if err := s.Link(ctx, a, []engram.Link{{To: b, Weight: 0.9}, {To: c, Weight: 0.7}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	const cnt = `MATCH (:Memory {id:$id})-[r:LINKS]->() RETURN count(r) AS c`
	if n := rawCount(t, cnt, map[string]any{"id": string(a)}); n != 2 {
		t.Errorf("LINKS from A = %d, want 2", n)
	}
	const wcnt = `MATCH (:Memory {id:$id})-[r:LINKS {weight:0.9}]->() RETURN count(r) AS c`
	if n := rawCount(t, wcnt, map[string]any{"id": string(a)}); n != 1 {
		t.Errorf("LINKS from A with weight 0.9 = %d, want 1", n)
	}

	// Idempotent: re-linking the same edge must not duplicate it.
	if err := s.Link(ctx, a, []engram.Link{{To: b, Weight: 0.9}}); err != nil {
		t.Fatalf("Link (re-run): %v", err)
	}
	if n := rawCount(t, cnt, map[string]any{"id": string(a)}); n != 2 {
		t.Errorf("LINKS from A after re-link = %d, want 2 (idempotent)", n)
	}

	// A missing target is skipped silently, not an error.
	if err := s.Link(ctx, a, []engram.Link{{To: uniqueID("nonexistent"), Weight: 1}}); err != nil {
		t.Fatalf("Link (missing target): %v", err)
	}
	if n := rawCount(t, cnt, map[string]any{"id": string(a)}); n != 2 {
		t.Errorf("LINKS from A after missing-target link = %d, want 2", n)
	}
}

func TestStoreNeighbors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	nsX := engram.Namespace(string(uniqueID("itest-nb-x")))
	nsY := engram.Namespace(string(uniqueID("itest-nb-y")))
	a, b, c := uniqueID("A"), uniqueID("B"), uniqueID("C")
	putBare(t, s, a, nsX)
	putBare(t, s, b, nsX)
	putBare(t, s, c, nsY)
	if err := s.Link(ctx, a, []engram.Link{{To: b, Weight: 0.9}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := s.LinkEntities(ctx, a, []string{"Redis"}); err != nil {
		t.Fatalf("LinkEntities A: %v", err)
	}
	if err := s.LinkEntities(ctx, c, []string{"Redis"}); err != nil {
		t.Fatalf("LinkEntities C: %v", err)
	}

	// Unscoped: link neighbor B and entity-bridge neighbor C (shared "Redis").
	all, err := s.Neighbors(ctx, []engram.MemoryID{a}, nil)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	byID := map[engram.MemoryID]engram.Neighbor{}
	for _, n := range all {
		byID[n.Memory.ID] = n
	}
	if nb, ok := byID[b]; !ok || nb.Via != "link" || nb.SourceID != a || nb.Weight != 0.9 {
		t.Errorf("link neighbor B = %+v (ok=%v), want via=link src=A weight=0.9", nb, ok)
	}
	if nc, ok := byID[c]; !ok || nc.Via != "entity:Redis" || nc.SourceID != a {
		t.Errorf("bridge neighbor C = %+v (ok=%v), want via=entity:Redis src=A", nc, ok)
	}

	// Scoped to nsX: link B stays; entity bridge C (nsY) is excluded.
	scoped, err := s.Neighbors(ctx, []engram.MemoryID{a}, []engram.Namespace{nsX})
	if err != nil {
		t.Fatalf("Neighbors scoped: %v", err)
	}
	got := map[engram.MemoryID]bool{}
	for _, n := range scoped {
		got[n.Memory.ID] = true
	}
	if !got[b] {
		t.Error("scoped Neighbors dropped link neighbor B")
	}
	if got[c] {
		t.Error("scoped Neighbors leaked cross-namespace bridge C")
	}
}

func TestStoreLinkNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-link-nf")))
	target := uniqueID("target")
	putBare(t, s, target, ns)
	err := s.Link(ctx, uniqueID("missing-source"), []engram.Link{{To: target, Weight: 1}})
	if !errors.Is(err, engram.ErrNotFound) {
		t.Fatalf("Link(missing source): want engram.ErrNotFound, got %v", err)
	}
}
