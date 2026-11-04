# memory-store-fake Specification

## Purpose
In-memory MemoryStore fake for tests and the eval, one for one with the Neo4j adapter's semantics (score scale, neighbour caps, lifecycle and prune rules), plus a lookup embedder and a cosine reranker.

## Requirements

### Requirement: MemStore satisfies the consumer interfaces
`mock.MemStore` MUST satisfy `mcp.Store`, the `mcp` forget store method set
(`SetForgotten`, `Pin`, `Delete`, `Supersede`) and `sweep.Pruner`, be safe for concurrent
use, and pass `go test -race`.

#### Scenario: Compile-time conformance
- **WHEN** the package compiles
- **THEN** `var _ mcp.Store = (*mock.MemStore)(nil)` and `var _ sweep.Pruner = (*mock.MemStore)(nil)` hold

#### Scenario: Concurrent recall and sweep
- **WHEN** `Search` and `Delete` run concurrently from two goroutines under the race detector
- **THEN** no race is reported

### Requirement: Search mirrors the vector index query
`Search(ctx, namespaces, vec, k)` MUST return the top-k memories by cosine similarity among
those whose namespace is in `namespaces` (all memories when the list is empty), ordered by
score descending with insertion order as the tie-break, including forgotten and
superseded memories, with `Score` set to the NORMALIZED similarity `(1 + cos) / 2` — the
scale Neo4j's cosine vector index returns (measured on Neo4j 5.26: orthogonal vectors score
0.5), which is the scale every pipeline threshold (dedup 0.95, auto-link 0.925, blend) is
applied to. The mock does not model the index's quantization noise. `k < 1` MUST behave as `k = 1`.

#### Scenario: Namespace filter and order
- **WHEN** memories m1 (ns a, cos 0.9), m2 (ns b, cos 0.95), m3 (ns a, cos 0.8) exist and `Search([a], q, 10)` runs
- **THEN** the result is [m1, m3]

#### Scenario: Normalized score
- **WHEN** a memory with an embedding orthogonal to the query is searched
- **THEN** its `Score` is 0.5, and an identical embedding scores 1.0

#### Scenario: Forgotten still searchable
- **WHEN** a memory is `Forgotten`
- **THEN** `Search` still returns it (the pipeline filters it)

### Requirement: Neighbors mirror the Cypher
`Neighbors(ctx, seeds, scope)` MUST return, per seed: up to 20 link neighbors over edges in
either direction, excluding seeds, filtered to `scope` when non-empty, ordered by weight
descending, with `Via = "link"` and the edge weight; plus up to 20 entity-bridge
neighbors sharing at least one entity, excluding the seed itself and all seeds, filtered
to `scope`, ordered by id, one row per (neighbor, entity) with `Via = "entity:<name>"` and
weight 1.0. `SourceID` MUST be the seed.

#### Scenario: Scope filters bridges
- **WHEN** seed s (ns a) shares entity e with n1 (ns a) and n2 (ns b) and scope is [a]
- **THEN** only n1 is returned, with `Via = "entity:e"`

#### Scenario: Seeds excluded
- **WHEN** seeds s1 and s2 are linked to each other
- **THEN** neither appears as the other's neighbor

#### Scenario: Link cap per seed
- **WHEN** a seed has 25 link neighbors
- **THEN** the 20 heaviest are returned for that seed

### Requirement: Lifecycle and prune semantics
`Reinforce` MUST increment `AccessCount` and set `LastAccessed = now`. `PropagateReinforce`
MUST set `LastAccessed = now` on up to 20 non-superseded link neighbors with weight ≥ threshold,
heaviest first; a superseded neighbor MUST be skipped and MUST NOT count toward the 20. `Supersede`
MUST set `Superseded` and be idempotent for unknown ids. `SetForgotten`,
`Pin` and `Delete` MUST return `engram.ErrNotFound` for an unknown id. `Delete` MUST remove
the memory, every link edge touching it, and its entity mentions. `PruneCandidates(before,
limit)` MUST return up to `limit` memories with `LastAccessed < before` and `Pinned ==
false`.

#### Scenario: Delete cascades
- **WHEN** memory X with one link edge and one entity mention is deleted
- **THEN** `Neighbors` no longer returns X, the edge is gone, and the entity no longer bridges to X

#### Scenario: Prune predicate at the boundary
- **WHEN** a memory has `LastAccessed` exactly equal to `before`
- **THEN** `PruneCandidates(before, n)` does not return it

#### Scenario: Pinned never a candidate
- **WHEN** a pinned memory has `LastAccessed` long before `before`
- **THEN** `PruneCandidates` does not return it

#### Scenario: Propagation skips superseded neighbors
- **WHEN** at virtual time T a memory has one strong edge to a superseded neighbor and one to a live neighbor
- **THEN** only the live neighbor's `LastAccessed` becomes T

### Requirement: Observability counters
`MemStore` MUST expose `Len()`, `EdgeCount()` (distinct undirected link edges) and
`BridgeCount()` (distinct unordered memory pairs sharing at least one entity) for the
report.

#### Scenario: Bridge count
- **WHEN** entity e is mentioned by m1, m2, m3 and no other entity exists
- **THEN** `BridgeCount()` is 3

### Requirement: Lookup embedder and cosine reranker
`mock.LookupEmbedder` MUST return the registered vector for a content string and an error
for an unknown string. `mock.CosineReranker` MUST return, aligned to `docs`, the cosine
between the looked-up query vector and each looked-up doc vector, and an error if any
lookup fails. Neither MAY consult ground-truth labels.

#### Scenario: Unknown content errors
- **WHEN** `Embed` is called with unregistered content
- **THEN** it returns a non-nil error wrapping the content

#### Scenario: Reranker alignment
- **WHEN** `Rerank(q, [d1, d2])` runs
- **THEN** it returns [cos(q, d1), cos(q, d2)] in that order
