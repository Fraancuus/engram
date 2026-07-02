//go:build integration

package neo4j_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// rawCount runs a single-value count query against the dev DB to verify graph writes the
// M1 Store API does not yet read back.
func rawCount(t *testing.T, cypher string, params map[string]any) int64 {
	t.Helper()
	ctx := context.Background()
	d, err := neo4jdriver.NewDriverWithContext(
		envOr("NEO4J_TEST_URI", "neo4j://localhost:7687"), neo4jdriver.NoAuth())
	if err != nil {
		t.Fatalf("raw driver: %v", err)
	}
	defer func() { _ = d.Close(ctx) }()
	res, err := neo4jdriver.ExecuteQuery(ctx, d, cypher, params, neo4jdriver.EagerResultTransformer)
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}
	v, _ := res.Records[0].Get("c")
	n, _ := v.(int64)
	return n
}

func TestStoreReinforce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := uniqueID("itest-reinforce")
	m := engram.Memory{
		ID: id, Namespace: "itest", Type: engram.Episodic, Content: "reinforce me",
		Embedding: embedding(384), AccessCount: 0, CreatedAt: now, LastAccessed: now,
	}
	if err := s.Put(ctx, m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	later := now.Add(time.Hour)
	if err := s.Reinforce(ctx, id, later); err != nil {
		t.Fatalf("Reinforce 1: %v", err)
	}
	if err := s.Reinforce(ctx, id, later); err != nil {
		t.Fatalf("Reinforce 2: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessCount != 2 {
		t.Errorf("AccessCount = %d, want 2", got.AccessCount)
	}
	if !got.LastAccessed.Equal(later) {
		t.Errorf("LastAccessed = %v, want %v", got.LastAccessed, later)
	}
}

func TestStoreReinforceNotFound(t *testing.T) {
	s := testStore(t)
	err := s.Reinforce(context.Background(), uniqueID("missing"), time.Now())
	if !errors.Is(err, engram.ErrNotFound) {
		t.Fatalf("Reinforce(missing): want engram.ErrNotFound, got %v", err)
	}
}

func TestStoreLinkEntities(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	id := uniqueID("itest-entities")
	m := engram.Memory{
		ID: id, Namespace: "itest", Type: engram.Semantic, Content: "mentions things",
		Embedding: embedding(384), CreatedAt: now, LastAccessed: now,
	}
	if err := s.Put(ctx, m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	const countMentions = `MATCH (m:Memory {id:$id})-[:MENTIONS]->(e:Entity) RETURN count(e) AS c`
	if err := s.LinkEntities(ctx, id, []string{"PortIQ", "Neo4j"}); err != nil {
		t.Fatalf("LinkEntities: %v", err)
	}
	if n := rawCount(t, countMentions, map[string]any{"id": string(id)}); n != 2 {
		t.Errorf("MENTIONS edges = %d, want 2", n)
	}
	// Idempotent: re-linking the same names must not duplicate edges.
	if err := s.LinkEntities(ctx, id, []string{"PortIQ", "Neo4j"}); err != nil {
		t.Fatalf("LinkEntities (re-run): %v", err)
	}
	if n := rawCount(t, countMentions, map[string]any{"id": string(id)}); n != 2 {
		t.Errorf("MENTIONS edges after re-link = %d, want 2 (idempotent)", n)
	}
}

func TestStoreLinkEntitiesNotFound(t *testing.T) {
	s := testStore(t)
	err := s.LinkEntities(context.Background(), uniqueID("missing-for-link"), []string{"X"})
	if !errors.Is(err, engram.ErrNotFound) {
		t.Fatalf("LinkEntities(missing): want engram.ErrNotFound, got %v", err)
	}
}
