## MODIFIED Requirements

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
