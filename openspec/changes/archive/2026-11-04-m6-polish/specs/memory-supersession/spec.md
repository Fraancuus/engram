## ADDED Requirements

### Requirement: Supersedes applies to procedural and semantic writes
`remember` MUST apply `supersedes` when the memory type is procedural or semantic, and MUST report
in `superseded` the ids it asked the store to supersede; an id that matches no memory is a no-op the
reply does not distinguish. A failed supersede MUST NOT fail the write and MUST leave `superseded`
absent, and the `remember` tool description MUST say that an absent `superseded` after a procedural or semantic write names ids means
the replace failed and the same write should be retried. An episodic write MUST ignore the field and
MUST NOT report it. Supersession is explicit only: the system MUST NOT infer it from similarity.

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
- **THEN** the write succeeds, the store treats the id as a no-op, and the reply's `superseded` still names it

#### Scenario: Failed supersede on either path
- **WHEN** the store's supersede fails on a write that inserts, or on a write that dedups into a live memory
- **THEN** the call returns no error, the memory stays stored or reinforced, and the reply has no `superseded`

### Requirement: Dedup never defeats a replacement
Dedup MUST skip a candidate that is already superseded, that is soft-forgotten, or that a procedural or
semantic write names in `supersedes`. A write that names `supersedes` MUST dedup only into a candidate
of its own memory type with identical content: the type decides how long the content lasts, and anything
else would discard the correction while superseding what it replaces. An episodic write's
`supersedes` MUST NOT affect dedup. The first remaining candidate decides dedup. A skipped candidate at
or above the dedup point MUST NOT be reinforced and MUST NOT absorb the write. Supersession MUST apply
whether the write inserts or dedups.

#### Scenario: Near-identical correction
- **WHEN** a semantic `remember` with `supersedes: [old]` scores 0.97 against `old`, above the 0.95 dedup point
- **THEN** a new memory is stored, `old` is superseded, and the reply has `deduped: false`

#### Scenario: Flip back to an old value
- **WHEN** a `remember` with no `supersedes` scores 0.97 against an already-superseded memory and no other candidate reaches 0.95
- **THEN** a new live memory is stored and the superseded memory is not reinforced

#### Scenario: Live duplicate behind a skipped candidate
- **WHEN** the top candidate at 0.98 is named in `supersedes` and a second candidate at 0.96 is live with identical content
- **THEN** the write dedups into the second candidate, the top candidate is superseded, and the reply's `superseded` names it

#### Scenario: Correction near a different live memory
- **WHEN** a write with `supersedes: [old]` scores 0.97 against a live memory of its own type with different content
- **THEN** a new memory is stored and recall returns it, `old` is superseded, and the live memory is not reinforced

#### Scenario: Soft-forgotten candidate
- **WHEN** a correction with `supersedes: [old]` scores 0.97 against a soft-forgotten memory, and no live candidate reaches 0.95
- **THEN** a new memory is stored and recall returns it, `old` is superseded, and the forgotten memory stays forgotten

#### Scenario: Re-remember after a soft forget
- **WHEN** a `remember` with no `supersedes` scores 0.97 against a soft-forgotten memory, and no live candidate reaches 0.95
- **THEN** a new live memory is stored and the forgotten memory is not reinforced

#### Scenario: No auto-link to a soft-forgotten memory
- **WHEN** a new memory scores at or above the auto-link point against a soft-forgotten memory
- **THEN** no link is written, so recalling the new memory does not refresh the forgotten one

#### Scenario: Replacement does not dedup across types
- **WHEN** a procedural `remember` with `supersedes: [std]` scores 1.0 against a live episode
- **THEN** a new procedural memory is stored and `std` is superseded, so the replacement outlives the episode

#### Scenario: Cross-type dedup unchanged without a replacement
- **WHEN** a procedural `remember` with no `supersedes` scores 0.97 against a live episode
- **THEN** it dedups into the episode, as it did before this change

#### Scenario: Threshold stops the walk after a skip
- **WHEN** a write with `supersedes: [old]` has candidates `old` at 0.98 and a live memory at 0.9499
- **THEN** a new memory is stored and the live memory is not reinforced

#### Scenario: Episodic supersedes does not bypass dedup
- **WHEN** an episodic `remember` passes `supersedes: [old]` and scores 0.97 against live `old`
- **THEN** it dedups into `old`, and `old` is not superseded

#### Scenario: Retried correction is idempotent
- **WHEN** a correction was stored but its supersede failed, and the same write retries with identical content against its own earlier insert
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
