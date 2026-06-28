// Command engramd is the Engram memory service. It wires the domain to its Neo4j and
// inference adapters by hand (no DI container) in main.
//
// At M0 it performs a single end-to-end proof — embed a string, persist it as a
// :Memory node, read it back — and exits. The MCP server registers zero tools until
// M1, so it is not served over stdio yet.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/inference"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

// systemClock is the production engram.Clock: the wall clock. It lives here at the
// wiring layer, never inside decay logic, so the "no time.Now() in decay" rule holds.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var _ engram.Clock = systemClock{}

func main() {
	if err := run(); err != nil {
		log.Fatalf("engramd: %v", err)
	}
}

// run wires the adapters by hand and performs the M0 end-to-end proof:
// embed a string -> build a Memory -> Put -> Get.
func run() error {
	ctx := context.Background()
	var clock engram.Clock = systemClock{}

	var embedder engram.Embedder = inference.New(getenv("TEI_URL", "http://localhost:8080"))

	store, err := eneo4j.New(ctx,
		getenv("NEO4J_URI", "neo4j://localhost:7687"),
		getenv("NEO4J_USER", "neo4j"),
		os.Getenv("NEO4J_PASSWORD"), // empty -> NoAuth, matching the local dev stack
	)
	if err != nil {
		return fmt.Errorf("store init: %w", err)
	}
	defer func() { _ = store.Close(ctx) }()

	const content = "engram m0 wiring check"
	vec, err := embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	now := clock.Now()
	m := engram.Memory{
		ID:           "m0-smoke",
		Namespace:    "work/engineering",
		Type:         engram.Semantic,
		Content:      content,
		Embedding:    vec,
		Importance:   0.5,
		CreatedAt:    now,
		LastAccessed: now,
		Source:       "engramd-m0",
	}
	if err := store.Put(ctx, m); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	got, err := store.Get(ctx, m.ID)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}

	log.Printf("engramd: M0 OK — stored %s, %d-dim embedding round-tripped", got.ID, len(got.Embedding))
	return nil
}

// getenv returns the value of key, or def when the variable is unset or empty.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
