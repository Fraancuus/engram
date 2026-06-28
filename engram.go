// Package engram is the domain core of the Engram long-term memory service.
//
// It owns the domain types (Memory, Entity) and the ports that infrastructure
// adapters implement: MemoryStore, Embedder, Reranker, DecayModel, and Clock.
// Per the dependency rule, this package imports NO infrastructure — the neo4j,
// inference, and mcp packages depend on engram, never the reverse. depguard
// enforces that boundary as a build failure.
//
// Domain logic (decay and retrievability math) lives here as pure, clock-injected
// functions so the eval can virtualize time. Nothing in this package reads the
// wall clock directly; callers pass a Clock or an explicit now.
//
// See docs/engram-prd-v1.md for the product spec and docs/engram-go-rules.md for
// the language conventions. Domain types and ports land in M0–M1; this file is the
// package anchor for now.
package engram
