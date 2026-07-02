//go:build integration

package engram_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/inference"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestEmbedEndToEnd is the M0 acceptance criterion (PRD §9), encoded: embed a string
// through the real TEI Embedder, persist it via the real Neo4j MemoryStore, and read it
// back. It requires the dev stack (docker compose up -d --wait) and skips otherwise.
func TestEmbedEndToEnd(t *testing.T) {
	ctx := context.Background()

	store, err := eneo4j.New(ctx,
		env("NEO4J_TEST_URI", "neo4j://localhost:7687"),
		env("NEO4J_TEST_USER", "neo4j"),
		os.Getenv("NEO4J_TEST_PASSWORD"),
	)
	if err != nil {
		t.Skipf("neo4j unavailable (%v); run: docker compose up -d --wait", err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	embedder := inference.New(env("TEI_TEST_URL", "http://localhost:8080"))
	const content = "engram m0 end-to-end check"
	vec, err := embedder.Embed(ctx, content)
	if err != nil {
		t.Skipf("tei unavailable (%v); run: docker compose up -d --wait", err)
	}
	if len(vec) != 384 {
		t.Fatalf("embedding dim = %d, want 384", len(vec))
	}

	now := time.Now().UTC()
	m := engram.Memory{
		ID:           engram.MemoryID(fmt.Sprintf("m0-e2e-%d", now.UnixNano())),
		Namespace:    "work/engineering",
		Type:         engram.Semantic,
		Content:      content,
		Embedding:    vec,
		Importance:   0.5,
		CreatedAt:    now,
		LastAccessed: now,
		Source:       "e2e_embed_test",
	}
	if err := store.Put(ctx, m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != m.ID || len(got.Embedding) != 384 {
		t.Errorf("round-trip mismatch: id=%q dim=%d, want id=%q dim=384", got.ID, len(got.Embedding), m.ID)
	}
}
