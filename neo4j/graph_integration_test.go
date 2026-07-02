//go:build integration

package neo4j_test

import (
	"context"
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
