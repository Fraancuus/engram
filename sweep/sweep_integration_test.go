//go:build integration

package sweep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func liveStore(t *testing.T) *eneo4j.Store {
	t.Helper()
	s, err := eneo4j.New(context.Background(),
		envOr("NEO4J_TEST_URI", "neo4j://localhost:7687"),
		envOr("NEO4J_TEST_USER", "neo4j"),
		os.Getenv("NEO4J_TEST_PASSWORD"))
	if err != nil {
		t.Skipf("neo4j unavailable (%v); run: docker compose up -d --wait", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestSweepOncePrunesDecayedLive verifies the sweep, against the real store and type-aware
// decay: a long-idle episodic memory is pruned, while a procedural and a pinned one survive.
func TestSweepOncePrunesDecayedLive(t *testing.T) {
	s := liveStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ns := engram.Namespace("sweep-itest-" + suffix)
	t0 := (fixedClock{}).Now()
	emb := make(engram.Vector, 384)
	emb[0] = 1
	put := func(id engram.MemoryID, typ engram.MemoryType, pinned bool) {
		m := engram.Memory{
			ID: id, Namespace: ns, Type: typ, Content: string(id), Embedding: emb,
			CreatedAt: t0, LastAccessed: t0, Pinned: pinned,
		}
		if err := s.Put(ctx, m); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	epi := engram.MemoryID("sweep-epi-" + suffix)
	proc := engram.MemoryID("sweep-proc-" + suffix)
	pin := engram.MemoryID("sweep-pin-" + suffix)
	put(epi, engram.Episodic, false)
	put(proc, engram.Procedural, false)
	put(pin, engram.Episodic, true)

	sw := New(s, engram.TypeAwareDecay{}, fixedClock{}, time.Hour, 30*24*time.Hour, 0.02, 100000,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Sweep ~2 years after insertion: the episodic memory is deeply decayed. The 30-day
	// grace filter means only these old test memories are candidates, not newer data.
	future := t0.Add(2 * 365 * 24 * time.Hour)
	if _, err := sw.SweepOnce(ctx, future); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if _, err := s.Get(ctx, epi); !errors.Is(err, engram.ErrNotFound) {
		t.Errorf("decayed unpinned episodic should be pruned; Get = %v", err)
	}
	if _, err := s.Get(ctx, proc); err != nil {
		t.Errorf("procedural should survive the sweep: %v", err)
	}
	if _, err := s.Get(ctx, pin); err != nil {
		t.Errorf("pinned should survive the sweep: %v", err)
	}
}
