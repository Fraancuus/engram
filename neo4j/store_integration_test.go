//go:build integration

package neo4j_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// testStore connects to the dev Neo4j (docker compose up -d --wait neo4j) or skips the
// test. The dev stack runs with auth disabled, so NEO4J_TEST_PASSWORD is empty by
// default and the store uses NoAuth.
func testStore(t *testing.T) *eneo4j.Store {
	t.Helper()
	s, err := eneo4j.New(context.Background(),
		envOr("NEO4J_TEST_URI", "neo4j://localhost:7687"),
		envOr("NEO4J_TEST_USER", "neo4j"),
		os.Getenv("NEO4J_TEST_PASSWORD"),
	)
	if err != nil {
		t.Skipf("neo4j unavailable (%v); start it with: docker compose up -d --wait neo4j", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// uniqueID keeps each run isolated against the shared dev database: Put uses MERGE, so a
// fixed id would let a stale node from a prior run mask a regression.
func uniqueID(base string) engram.MemoryID {
	return engram.MemoryID(fmt.Sprintf("%s-%d", base, time.Now().UnixNano()))
}

// embedding builds an n-length deterministic embedding fixture.
func embedding(n int) engram.Vector {
	v := make(engram.Vector, n)
	for i := range v {
		v[i] = float32(i) / float32(n)
	}
	return v
}

func TestStorePutGetRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	want := engram.Memory{
		ID:           uniqueID("m0-itest-roundtrip"),
		Namespace:    "work/engineering",
		Type:         engram.Semantic,
		Content:      "integration round-trip",
		Embedding:    embedding(384),
		Importance:   0.75,
		Stability:    2.5,
		AccessCount:  3,
		CreatedAt:    now,
		LastAccessed: now,
		Source:       "store_integration_test",
		Superseded:   true,
	}
	if err := s.Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Namespace != want.Namespace {
		t.Errorf("Namespace = %q, want %q", got.Namespace, want.Namespace)
	}
	if got.Type != want.Type {
		t.Errorf("Type = %q, want %q", got.Type, want.Type)
	}
	if got.Content != want.Content {
		t.Errorf("Content = %q, want %q", got.Content, want.Content)
	}
	if got.Importance != want.Importance {
		t.Errorf("Importance = %v, want %v", got.Importance, want.Importance)
	}
	if got.Stability != want.Stability {
		t.Errorf("Stability = %v, want %v", got.Stability, want.Stability)
	}
	if got.AccessCount != want.AccessCount {
		t.Errorf("AccessCount = %d, want %d", got.AccessCount, want.AccessCount)
	}
	if got.Source != want.Source {
		t.Errorf("Source = %q, want %q", got.Source, want.Source)
	}
	if got.Superseded != want.Superseded {
		t.Errorf("Superseded = %v, want %v", got.Superseded, want.Superseded)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.LastAccessed.Equal(want.LastAccessed) {
		t.Errorf("LastAccessed = %v, want %v", got.LastAccessed, want.LastAccessed)
	}
	if len(got.Embedding) != len(want.Embedding) {
		t.Fatalf("Embedding len = %d, want %d", len(got.Embedding), len(want.Embedding))
	}
	for i := range want.Embedding {
		if got.Embedding[i] != want.Embedding[i] {
			t.Errorf("Embedding[%d] = %v, want %v", i, got.Embedding[i], want.Embedding[i])
			break
		}
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.Get(context.Background(), uniqueID("does-not-exist"))
	if !errors.Is(err, engram.ErrNotFound) {
		t.Fatalf("Get(missing): want engram.ErrNotFound, got %v", err)
	}
}

// TestStorePutLocalZoneTime is a regression test: a Memory carrying timestamps in Go's
// Local zone (what systemClock.Now() and any plain time.Now() produce) must Put without
// error and read back as UTC. Neo4j's driver rejects the non-IANA zone name "Local", so
// the store normalizes timestamps to UTC.
func TestStorePutLocalZoneTime(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond) // Local zone — the engramd path that exposed the bug
	m := engram.Memory{
		ID:           uniqueID("m0-itest-localtime"),
		Namespace:    "work/engineering",
		Type:         engram.Episodic,
		Content:      "local zone timestamp",
		Embedding:    embedding(384),
		CreatedAt:    now,
		LastAccessed: now,
		Source:       "store_integration_test",
	}
	if err := s.Put(ctx, m); err != nil {
		t.Fatalf("Put with Local-zone timestamps: %v", err)
	}
	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loc := got.CreatedAt.Location(); loc != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", loc)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want same instant as %v", got.CreatedAt, now)
	}
}
