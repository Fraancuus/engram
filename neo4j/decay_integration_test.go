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

// putAt inserts a minimal episodic memory with an explicit last_accessed, so decay/prune
// tests can place a memory in the past.
func putAt(t *testing.T, s *eneo4j.Store, id engram.MemoryID, ns engram.Namespace, at time.Time) {
	t.Helper()
	emb := make(engram.Vector, 384)
	emb[0] = 1
	m := engram.Memory{
		ID: id, Namespace: ns, Type: engram.Episodic, Content: string(id),
		Embedding: emb, CreatedAt: at, LastAccessed: at,
	}
	if err := s.Put(context.Background(), m); err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
}

func TestStorePinAndForget(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-flags")))
	id := uniqueID("m")
	putBare(t, s, id, ns)

	if err := s.Pin(ctx, id); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := s.SetForgotten(ctx, id); err != nil {
		t.Fatalf("SetForgotten: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Pinned || !got.Forgotten {
		t.Errorf("flags = pinned:%v forgotten:%v, want both true", got.Pinned, got.Forgotten)
	}
	if err := s.Pin(ctx, uniqueID("missing")); !errors.Is(err, engram.ErrNotFound) {
		t.Errorf("Pin(missing) = %v, want ErrNotFound", err)
	}
}

func TestStoreDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-del")))
	id := uniqueID("m")
	putBare(t, s, id, ns)
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, engram.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, uniqueID("missing")); !errors.Is(err, engram.ErrNotFound) {
		t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestStoreSupersede(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-sup")))
	a, b := uniqueID("a"), uniqueID("b")
	putBare(t, s, a, ns)
	putBare(t, s, b, ns)
	if err := s.Supersede(ctx, []engram.MemoryID{a, b}); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	for _, id := range []engram.MemoryID{a, b} {
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if !got.Superseded {
			t.Errorf("%s superseded = false, want true", id)
		}
	}
}

func TestStorePruneCandidates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-prune")))
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	oldID, recentID, pinnedOldID := uniqueID("old"), uniqueID("recent"), uniqueID("pinnedold")
	putAt(t, s, oldID, ns, old)
	putAt(t, s, recentID, ns, time.Now().UTC())
	putAt(t, s, pinnedOldID, ns, old)
	if err := s.Pin(ctx, pinnedOldID); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	// Large limit: the shared dev DB accumulates memories across runs, so assert membership
	// over all matches rather than relying on a bounded page to contain this run's ids.
	cands, err := s.PruneCandidates(ctx, time.Now().UTC().Add(-24*time.Hour), 100000)
	if err != nil {
		t.Fatalf("PruneCandidates: %v", err)
	}
	got := map[engram.MemoryID]bool{}
	for _, m := range cands {
		got[m.ID] = true
	}
	if !got[oldID] {
		t.Error("old unpinned memory should be a prune candidate")
	}
	if got[recentID] {
		t.Error("recent memory should not be a candidate")
	}
	if got[pinnedOldID] {
		t.Error("pinned memory should be excluded from candidates")
	}
}

func TestStorePropagateReinforce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-prop")))
	src, strong, weak := uniqueID("src"), uniqueID("strong"), uniqueID("weak")
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	putBare(t, s, src, ns)
	putAt(t, s, strong, ns, old)
	putAt(t, s, weak, ns, old)
	if err := s.Link(ctx, src, []engram.Link{{To: strong, Weight: 0.9}, {To: weak, Weight: 0.5}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	now := time.Now().UTC()
	if err := s.PropagateReinforce(ctx, src, 0.85, now); err != nil {
		t.Fatalf("PropagateReinforce: %v", err)
	}
	gs, err := s.Get(ctx, strong)
	if err != nil {
		t.Fatalf("Get strong: %v", err)
	}
	gw, err := s.Get(ctx, weak)
	if err != nil {
		t.Fatalf("Get weak: %v", err)
	}
	if !gs.LastAccessed.After(old.Add(time.Hour)) {
		t.Errorf("strong-edge neighbor last_accessed not refreshed: %v", gs.LastAccessed)
	}
	if gw.LastAccessed.After(old.Add(time.Hour)) {
		t.Errorf("weak-edge neighbor should not be refreshed: %v", gw.LastAccessed)
	}
}
