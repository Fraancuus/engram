## MODIFIED Requirements

### Requirement: MemStore satisfies the consumer interfaces
`mock.MemStore` MUST satisfy `mcp.ServerStore`, which covers `mcp.Store`, the forget store method
set (`SetForgotten`, `Pin`, `Delete`, `Supersede`) and the stats store method set (`Counts`,
`LiveMemories`), and MUST satisfy `sweep.Pruner` (`PruneCandidates`, `DeleteIfStale`), be safe for
concurrent use, and pass `go test -race`.

#### Scenario: Compile-time conformance
- **WHEN** the package compiles
- **THEN** `var _ mcp.ServerStore = (*mock.MemStore)(nil)` and `var _ sweep.Pruner = (*mock.MemStore)(nil)` hold

#### Scenario: Concurrent recall and sweep
- **WHEN** `Search`, `Reinforce` and `DeleteIfStale` run concurrently from separate goroutines under the race detector
- **THEN** no race is reported

#### Scenario: Concurrent stats and delete
- **WHEN** `Counts`, `LiveMemories` and `Delete` run concurrently from separate goroutines under the race detector
- **THEN** no race is reported

### Requirement: Lifecycle and prune semantics
`Reinforce` MUST increment `AccessCount` and set `LastAccessed = now`. `PropagateReinforce`
MUST set `LastAccessed = now` on up to 20 non-superseded link neighbors with weight ≥ threshold,
heaviest first; a superseded neighbor MUST be skipped and MUST NOT count toward the 20. `Supersede`
MUST set `Superseded` and be idempotent for unknown ids. `SetForgotten`,
`Pin` and `Delete` MUST return `engram.ErrNotFound` for an unknown id. `Delete` MUST remove
the memory, every link edge touching it, and its entity mentions. `PruneCandidates(before,
limit)` MUST return up to `limit` memories with `LastAccessed < before` and `Pinned ==
false`. `DeleteIfStale(id, before)` MUST delete a memory exactly as `Delete` does, but only when
`PruneCandidates(before)` would still return it; it MUST report whether it deleted, and MUST return
false and no error for an unknown id. The check and the delete MUST happen under one lock.

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

#### Scenario: DeleteIfStale cascades like Delete
- **WHEN** a stale memory X with one link edge and one entity mention is passed to `DeleteIfStale`
- **THEN** it returns true, `Neighbors` no longer returns X, the edge is gone, and the entity no longer bridges to X

#### Scenario: DeleteIfStale at the boundary, at fixed virtual time
- **WHEN** at virtual time T a memory has `LastAccessed` exactly equal to `before`
- **THEN** `DeleteIfStale(id, before)` returns false and the memory is still there

#### Scenario: DeleteIfStale keeps a pinned or reinforced memory
- **WHEN** a memory read as a candidate is pinned, or reinforced at a time not before `before`, and is then passed to `DeleteIfStale`
- **THEN** it returns false and the memory is still there

#### Scenario: DeleteIfStale on an unknown id
- **WHEN** `DeleteIfStale` is called with an id no memory has
- **THEN** it returns false and no error
