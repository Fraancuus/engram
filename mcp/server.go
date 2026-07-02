package mcp

// The official SDK's package is itself named mcp, so it is imported under the mcpsdk
// alias to avoid colliding with this package — the same aliasing the neo4j driver needs.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fraancuus/engram"
)

// Identity reported to MCP clients during the initialize handshake.
const (
	serverName    = "engram"
	serverVersion = "0.0.0-dev"
)

// NewServer constructs the Engram MCP server on the official Model Context Protocol Go
// SDK, wiring the domain ports into the remember and recall tool handlers. Each tool is
// registered with the generic mcpsdk.AddTool, which infers and validates its JSON Schema
// from the Go input struct (the type gate); the handlers enforce the policy bounds.
//
// Serve it over stdio with Serve(ctx, srv).
func NewServer(embedder engram.Embedder, store Store, clock engram.Clock) *mcpsdk.Server {
	h := &handlers{
		embedder:       embedder,
		store:          store,
		clock:          clock,
		dedupThreshold: defaultDedupThreshold,
		seedN:          defaultSeedN,
		log:            slog.Default(),
		newID:          newMemoryID,
	}

	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: serverName, Version: serverVersion}, nil)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "remember",
		Description: "Store a memory (content, type, namespace; optional importance, source, entities). Deduplicates within the namespace, reinforcing a near-identical existing memory instead of inserting.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in rememberInput) (*mcpsdk.CallToolResult, rememberOutput, error) {
		out, err := h.doRemember(ctx, in)
		return nil, out, err
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "recall",
		Description: "Search memories by semantic similarity to a query, optionally restricted to namespaces; returns ranked results with provenance.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in recallInput) (*mcpsdk.CallToolResult, recallOutput, error) {
		out, err := h.doRecall(ctx, in)
		return nil, out, err
	})

	return srv
}

// Serve runs srv over the stdio transport until the client disconnects or ctx is
// cancelled. It keeps the SDK transport type out of the caller (cmd/engramd).
func Serve(ctx context.Context, srv *mcpsdk.Server) error {
	if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp serve: %w", err)
	}
	return nil
}

// newMemoryID returns a random 128-bit hex identifier for a new memory.
func newMemoryID() (engram.MemoryID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return engram.MemoryID(hex.EncodeToString(b[:])), nil
}
