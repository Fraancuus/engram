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
// the language conventions.
package engram

import (
	"context"
	"errors"
	"time"
)

// MemoryType is the kind of a memory and the single key the DecayModel switches on.
// It is a string (not an int enum) so it stays readable and stable across the
// Neo4j/JSON boundary; the constants below are the only valid values.
type MemoryType string

// The three memory types, each with its own decay regime (PRD §6.3). The string
// values are persisted verbatim, so they must not drift — see TestMemoryTypeStrings.
const (
	Episodic   MemoryType = "episodic"
	Semantic   MemoryType = "semantic"
	Procedural MemoryType = "procedural"
)

// Valid reports whether t is one of the known memory types. The remember handler uses
// it to reject unknown types from untrusted input.
func (t MemoryType) Valid() bool {
	switch t {
	case Episodic, Semantic, Procedural:
		return true
	default:
		return false
	}
}

// MemoryID is the unique identifier of a stored memory. The named type stops call
// sites from mixing it up with content, a namespace, or an entity id.
type MemoryID string

// Namespace is the soft scope ("universe") a memory belongs to, e.g.
// "work/engineering". It filters recall and scopes writes; it does not change decay
// behavior (per-namespace behavior is out of v1 scope).
type Namespace string

// Vector is an embedding. It is []float32 to match the TEI sidecar output and Neo4j's
// native vector index; cosine similarity loses nothing at float32.
type Vector []float32

// Memory is a single stored memory — exactly the persisted :Memory node (PRD §6.1)
// and nothing more. Retrievability is derived per-call by a DecayModel and is never
// stored here; recall-time outputs (score, provenance) live on a separate result type,
// not on Memory.
type Memory struct {
	ID           MemoryID
	Namespace    Namespace
	Type         MemoryType
	Content      string
	Embedding    Vector
	Importance   float64 // 0–1; the 0.5 default is applied at the remember boundary, not via the zero value
	Stability    float64 // S: type-keyed initial value at insert, raised on reinforcement
	AccessCount  int
	CreatedAt    time.Time
	LastAccessed time.Time
	Source       string
	Superseded   bool // replaced by a newer memory; begins a fast decay toward archival
	Pinned       bool // explicitly protected from decay (distinct from importance)
	Forgotten    bool // soft-forgotten; excluded from default recall, recoverable
}

// RecallResult is a memory returned by a recall, paired with its similarity Score
// (higher is more relevant). The MCP layer projects the response provenance from the
// embedded Memory's own fields.
type RecallResult struct {
	Memory
	Score float64
	// RetrievedVia records how this result surfaced during recall: "vector", "link", or
	// "entity:<name>". Empty until recall sets it (M2 graph expansion).
	RetrievedVia string
}

// Link is a weighted association from one memory to another, persisted as a [:LINKS]
// edge. Weight is the cosine similarity for auto-links, or 1.0 for an explicit link.
type Link struct {
	To     MemoryID
	Weight float64
}

// Neighbor is a memory reached during recall's associative expansion, tagged with the
// seed it was reached from (SourceID) and how (Via is "link" or "entity:<name>"). Weight
// is the [:LINKS] edge weight and is meaningless for entity bridges.
type Neighbor struct {
	Memory   Memory
	SourceID MemoryID
	Via      string
	Weight   float64
}

// Entity is a cross-namespace bridge node reached from memories via MENTIONS. It is
// intentionally minimal and NOT namespaced — pinning it to one namespace would model
// the bridge wrong.
type Entity struct {
	ID   string
	Name string
}

// Clock is the single source of time, injected so the eval can virtualize it.
// Production wires a wall-clock implementation in main(); decay and retrievability
// logic never call time.Now directly.
type Clock interface {
	Now() time.Time
}

// Embedder turns text into a Vector via the inference sidecar.
type Embedder interface {
	Embed(ctx context.Context, text string) (Vector, error)
}

// Reranker scores how well each doc answers query, returning scores aligned to docs
// by index (not a sorted list) so the domain keeps ownership of the final ordering.
// Implemented by a cross-encoder client at M3.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// DecayModel is the pure, type-keyed decay math. Its methods take time explicitly
// (they never read a Clock) and do no I/O, so they are deterministic and
// table-testable; uniform vs. type-aware decay is a swap of implementation, not a
// rewrite. Implemented at M4.
type DecayModel interface {
	// Retrievability returns the probability that m is recallable at now, in [0,1].
	Retrievability(m Memory, now time.Time) float64
	// Stability returns the decay time-constant S for a memory of type t with the given
	// access count and importance.
	Stability(t MemoryType, accessCount int, importance float64) float64
}

// MemoryStore is the persistence port. It is deliberately minimal — recall, forget,
// stats, and the decay sweep get their own small interfaces at their consumers, all
// satisfied by the one concrete store; this must not grow into a god-interface.
type MemoryStore interface {
	Put(ctx context.Context, m Memory) error
	Get(ctx context.Context, id MemoryID) (Memory, error)
}

// ErrNotFound is returned by MemoryStore.Get when no memory has the given id. Callers
// branch on it with errors.Is.
var ErrNotFound = errors.New("memory not found")
