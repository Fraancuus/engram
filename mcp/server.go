package mcp

// The official SDK's package is itself named mcp, so it is imported under the
// mcpsdk alias to avoid colliding with this package — the same aliasing the
// neo4j driver needs for the same reason.
import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Identity reported to MCP clients during the initialize handshake. Version is a
// dev placeholder until the build stamps a real one.
const (
	serverName    = "engram"
	serverVersion = "0.0.0-dev"
)

// NewServer constructs the Engram MCP server on the official Model Context
// Protocol Go SDK and returns it ready to be served over stdio by cmd/engramd
// (server.Run(ctx, &mcpsdk.StdioTransport{})).
//
// At M1 this signature grows to accept the domain ports the tool handlers need
// (MemoryStore, Embedder, Clock). Those handlers — and therefore the untrusted-
// input validation this package owns (see the package doc) — live HERE, in the
// mcp package, not in main: register the tools inside NewServer so the validation
// boundary stays at this one seam.
func NewServer() *mcpsdk.Server {
	// The remember/recall/forget/memory_stats tools (PRD §8) are registered here
	// at M1+ via the generic mcpsdk.AddTool, which infers and validates each
	// tool's JSON Schema from its Go input struct — the type gate; handlers still
	// enforce policy bounds (k limits, namespace whitelist, id shape) and keep
	// errors crossing back to the caller legible, never leaking internals.
	//
	// The second NewServer arg is the SDK's *ServerOptions; nil = defaults, no
	// capabilities beyond tools yet.
	return mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)
}
