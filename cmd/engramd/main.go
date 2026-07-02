// Command engramd is the Engram memory service. It wires the domain to its Neo4j and
// inference adapters by hand (no DI container) in main and serves the MCP tools
// (remember, recall) over stdio.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/inference"
	"github.com/Fraancuus/engram/mcp"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

// systemClock is the production engram.Clock: the wall clock. It lives here at the wiring
// layer, never inside decay logic, so the "no time.Now() in decay" rule holds.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var _ engram.Clock = systemClock{}

func main() {
	if err := run(); err != nil {
		log.Fatalf("engramd: %v", err)
	}
}

// run wires the adapters by hand and serves the MCP tools over stdio until the client
// disconnects or the process is signalled.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := eneo4j.New(ctx,
		getenv("NEO4J_URI", "neo4j://localhost:7687"),
		getenv("NEO4J_USER", "neo4j"),
		os.Getenv("NEO4J_PASSWORD"), // empty -> NoAuth, matching the local dev stack
	)
	if err != nil {
		return fmt.Errorf("store init: %w", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	embedder := inference.New(getenv("TEI_URL", "http://localhost:8080"))
	reranker := inference.NewReranker(getenv("TEI_RERANK_URL", "http://localhost:8081"))
	srv := mcp.NewServer(embedder, reranker, store, systemClock{})

	// Logs go to stderr (slog default); stdout carries the MCP stdio protocol.
	slog.Info("engramd serving MCP over stdio", "tools", "remember,recall")
	if err := mcp.Serve(ctx, srv); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// getenv returns the value of key, or def when the variable is unset or empty.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
