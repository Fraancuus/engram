## ADDED Requirements

### Requirement: Supersedes applies to procedural and semantic writes
`remember` MUST apply `supersedes` when the memory type is procedural or semantic, and MUST report
the applied ids in `superseded`. An episodic write MUST ignore the field and MUST NOT report it.
Supersession is explicit only: the system MUST NOT infer it from similarity.

#### Scenario: Semantic write supersedes
- **WHEN** a semantic `remember` passes `supersedes: [old]`
- **THEN** `old` is marked superseded and the reply's `superseded` is `[old]`

#### Scenario: Episodic write ignores it
- **WHEN** an episodic `remember` passes `supersedes: [old]`
- **THEN** `old` is unchanged and the reply has no `superseded`

#### Scenario: No detection
- **WHEN** a semantic `remember` passes no `supersedes` and a very similar older fact exists below the dedup point
- **THEN** the older fact is not marked superseded

#### Scenario: Absent target
- **WHEN** `supersedes` names an id that no longer exists
- **THEN** the write succeeds and the store treats the id as a no-op

### Requirement: Dedup never defeats a replacement
Dedup MUST skip a candidate that is already superseded or that a procedural or semantic write names in
`supersedes`. An episodic write's `supersedes` MUST NOT affect dedup. The first remaining candidate
decides dedup. A skipped candidate at or above the dedup point MUST NOT be reinforced and MUST NOT
absorb the write. Supersession MUST apply whether the write inserts or dedups.

#### Scenario: Near-identical correction
- **WHEN** a semantic `remember` with `supersedes: [old]` scores 0.97 against `old`, above the 0.95 dedup point
- **THEN** a new memory is stored, `old` is superseded, and the reply has `deduped: false`

#### Scenario: Flip back to an old value
- **WHEN** a `remember` with no `supersedes` scores 0.97 against an already-superseded memory and no other candidate reaches 0.95
- **THEN** a new live memory is stored and the superseded memory is not reinforced

#### Scenario: Live duplicate behind a skipped candidate
- **WHEN** the top candidate at 0.98 is named in `supersedes` and a second candidate at 0.96 is live
- **THEN** the write dedups into the second candidate, the top candidate is superseded, and the reply's `superseded` names it

#### Scenario: Episodic supersedes does not bypass dedup
- **WHEN** an episodic `remember` passes `supersedes: [old]` and scores 0.97 against live `old`
- **THEN** it dedups into `old`, and `old` is not superseded

#### Scenario: Retried correction is idempotent
- **WHEN** a correction was stored but its supersede failed, and the same write retries and scores 0.99 against its own earlier insert
- **THEN** it dedups into that insert and applies the supersession, and the reply's `superseded` names the old memory

#### Scenario: Ordinary dedup unchanged
- **WHEN** a `remember` with no `supersedes` scores exactly 0.95 against a live memory
- **THEN** it dedups into that memory

### Requirement: Freshness does not propagate to superseded memories
`PropagateReinforce` MUST NOT change `last_accessed` on a superseded memory. A skipped memory MUST NOT
count toward the cap of 20. This MUST hold in both `neo4j.Store` and `mock.MemStore`.

#### Scenario: Chain-mate recall leaves the old version alone
- **WHEN** at virtual day 50 the current fact version is recalled and its superseded predecessor has a weight 1.0 edge to it and `last_accessed` at day 10
- **THEN** the predecessor's `last_accessed` is still day 10

#### Scenario: Superseded version decays and is pruned
- **WHEN** a superseded fact last accessed at day 10 reaches the sweep at day 41, with grace 30 days and hard floor 0.02
- **THEN** its retrievability exp(-31/2) is below the floor and the sweep deletes it

#### Scenario: Cap ignores skipped targets
- **WHEN** a memory has 25 strong edges and the 5 heaviest lead to superseded memories
- **THEN** the 20 live neighbors are refreshed
