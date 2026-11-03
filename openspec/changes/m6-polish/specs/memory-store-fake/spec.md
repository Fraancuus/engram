## MODIFIED Requirements

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
