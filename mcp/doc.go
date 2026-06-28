// Package mcp adapts the Engram domain to the Model Context Protocol, exposing the
// remember, recall, forget, and memory_stats tools over the stdio transport.
//
// Tool inputs arrive from an untrusted caller: this boundary validates types and
// bounds (k within limits, namespace against a known set, ids well-formed) before
// anything reaches the domain, and keeps errors crossing back to the agent legible
// — actionable messages, never internal stack or query dumps. It depends on engram,
// never the reverse.
package mcp
