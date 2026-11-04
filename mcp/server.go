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

// ServerStore is the full persistence surface NewTools wires in: Store, the forget
// operations and the read-only stats queries; each handler field then narrows to just
// what it uses.
type ServerStore interface {
	Store
	statsStore
	SetForgotten(ctx context.Context, id engram.MemoryID) error
	Pin(ctx context.Context, id engram.MemoryID) error
	Delete(ctx context.Context, id engram.MemoryID) error
}

// Option customises a Tools built by NewTools.
type Option func(*Tools)

// WithIDGenerator overrides how new memory ids are minted (default: 128 random bits as
// hex). The eval uses it to get deterministic ids. A nil fn is ignored; fn must be safe
// for concurrent use because handlers may run in parallel.
func WithIDGenerator(fn func() (engram.MemoryID, error)) Option {
	return func(t *Tools) {
		if fn != nil {
			t.newID = fn
		}
	}
}

// WithoutRerank disables the rerank stage, so recall results keep the blend order. The
// eval uses it because no label-free stand-in for the cross-encoder exists.
func WithoutRerank() Option {
	return func(t *Tools) { t.reranker = nil }
}

// WithSweepStats supplies the decay sweep's stats to memory_stats (default: none, so the
// tool reports the empty values). A nil fn is ignored; fn must be safe for concurrent use.
func WithSweepStats(fn func() engram.SweepStats) Option {
	return func(t *Tools) {
		if fn != nil {
			t.sweepStats = fn
		}
	}
}

// NewTools wires the domain ports into the remember, recall, forget and memory_stats
// pipeline with the production defaults. Call its methods in process, or hand it to NewServer.
func NewTools(embedder engram.Embedder, reranker engram.Reranker, decay engram.DecayModel, store ServerStore, clock engram.Clock, opts ...Option) *Tools {
	t := &Tools{
		embedder:         embedder,
		reranker:         reranker,
		decay:            decay,
		store:            store,
		forget:           store,
		stats:            store,
		statsRowCap:      defaultStatsRowCap,
		clock:            clock,
		dedupThreshold:   defaultDedupThreshold,
		seedN:            defaultSeedN,
		rerankCandidates: defaultRerankCandidates,
		maxTokens:        defaultMaxTokens,
		wSim:             defaultWSim,
		wImp:             defaultWImp,
		wRet:             defaultWRet,
		softThreshold:    defaultSoftThresh,
		log:              slog.Default(),
		newID:            newMemoryID,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Remember stores a memory, deduplicating within its namespace.
func (t *Tools) Remember(ctx context.Context, in RememberInput) (RememberOutput, error) {
	return t.doRemember(ctx, in)
}

// Recall returns ranked memories for a query with provenance.
func (t *Tools) Recall(ctx context.Context, in RecallInput) (RecallOutput, error) {
	return t.doRecall(ctx, in)
}

// Forget soft-forgets, deletes, pins or supersedes a memory by id.
func (t *Tools) Forget(ctx context.Context, in ForgetInput) (ForgetOutput, error) {
	return t.doForget(ctx, in)
}

// MemoryStats reports counts, retrievability, store size and sweep state, read-only.
func (t *Tools) MemoryStats(ctx context.Context, in MemoryStatsInput) (MemoryStatsOutput, error) {
	return t.doMemoryStats(ctx, in)
}

// NewServer constructs the Engram MCP server on the official Model Context Protocol Go
// SDK, registering t's remember, recall, forget and memory_stats tools. Each tool is registered with
// the generic mcpsdk.AddTool, which infers and validates its JSON Schema from the Go
// input struct (the type gate); the handlers enforce the policy bounds.
//
// Serve it over stdio with Serve(ctx, srv).
func NewServer(t *Tools) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: serverName, Version: serverVersion}, nil)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "remember",
		Description: "Store a memory (content, type, namespace; optional importance, source, entities). Deduplicates within the namespace, reinforcing a near-identical existing memory instead of inserting. With supersedes on a procedural or semantic write, the superseded field echoes the ids it asked the store to replace; if it is absent after naming ids, the replace failed and the same write should be retried.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in RememberInput) (*mcpsdk.CallToolResult, RememberOutput, error) {
		out, err := t.Remember(ctx, in)
		return nil, out, err
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "recall",
		Description: "Search memories by semantic similarity to a query, optionally restricted to namespaces; returns ranked results with provenance.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in RecallInput) (*mcpsdk.CallToolResult, RecallOutput, error) {
		out, err := t.Recall(ctx, in)
		return nil, out, err
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "forget",
		Description: "Forget a memory by id: mode 'soft' (excluded from recall, recoverable), 'hard' (delete), 'pin' (protect from decay), or 'supersede' (mark replaced).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ForgetInput) (*mcpsdk.CallToolResult, ForgetOutput, error) {
		out, err := t.Forget(ctx, in)
		return nil, out, err
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "memory_stats",
		Description: "Report how many memories exist and how fresh they are, store-wide or for one namespace. Returns no content and, unlike recall, never reinforces. Counts cover all rows; live, superseded, forgotten and pinned overlap. The retrievability histogram and below_recall_threshold cover live rows, sampled when truncated is true. Scoped edge and bridge counts include those touching the namespace. Sweep stats are store-wide since process start, and read null and 0 until the first hourly sweep. An unknown namespace returns empty counts, not an error.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in MemoryStatsInput) (*mcpsdk.CallToolResult, MemoryStatsOutput, error) {
		out, err := t.MemoryStats(ctx, in)
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
