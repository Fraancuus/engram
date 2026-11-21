## ADDED Requirements

### Requirement: A writer reports a row that is gone
On the Neo4j store, `Reinforce`, `Pin` and `SetForgotten` MUST return `engram.ErrNotFound` when the
memory does not exist once their write has taken effect, including when it was deleted while they
waited for its lock. They MUST NOT return nil for a memory that is gone. Each query MUST stay
parameterized.

#### Scenario: Unknown id
- **WHEN** `Reinforce`, `Pin` or `SetForgotten` is called with an id no memory has
- **THEN** it returns an error that `errors.Is` matches to `engram.ErrNotFound`

#### Scenario: No acknowledged write is lost, over many rounds
- **WHEN** `Reinforce` races `DeleteIfStale`, and `Pin` races `DeleteIfStale`, each on fresh stale memories and released together, round after round
- **THEN** every round ends in one of two states and both occur: the writer returns nil, `DeleteIfStale` returns false and the memory exists with the write applied; or the writer returns `engram.ErrNotFound`, `DeleteIfStale` returns true and the memory is gone

### Requirement: remember keeps the content when its dedup target is gone
When `remember` selects a dedup target and `Reinforce` on it returns `engram.ErrNotFound`, the
handler MUST store the incoming content as a new memory through its normal insert path, MUST answer
with the new id and `deduped: false`, and MUST apply `supersedes` as for any insert. It MUST NOT try
another dedup candidate. Any other `Reinforce` error MUST still answer `remember: store unavailable`
without storing the content.

#### Scenario: Target gone, content stored
- **WHEN** a `remember` scores 0.97 against a live memory, and that memory is deleted before the reinforce
- **THEN** a new memory holding the content is stored, the reply has `deduped: false` and the new id, and recall returns the new memory

#### Scenario: Target gone on a correction
- **WHEN** a semantic `remember` with `supersedes: [old]` dedups into its own earlier insert, and that insert is deleted before the reinforce
- **THEN** a new memory holding the content is stored, `old` is superseded, and the reply's `superseded` names it

#### Scenario: Target gone beside a second live candidate
- **WHEN** a `remember` scores 0.97 against one memory and 0.96 against another, and the first is deleted before the reinforce
- **THEN** a new memory holding the content is stored with `deduped: false`, and the second memory is not reinforced

#### Scenario: Target present
- **WHEN** a `remember` scores exactly 0.95 against a live memory that still exists
- **THEN** it dedups into that memory as before, and the content is carried by that memory

#### Scenario: Reinforce fails for another reason
- **WHEN** `Reinforce` on the dedup target returns an error that is not `engram.ErrNotFound`
- **THEN** the reply is the error `remember: store unavailable`, no memory is stored, and the content is not retrievable because the store refused the write

### Requirement: recall tolerates a hit that was deleted
When `Reinforce` on a recalled hit returns `engram.ErrNotFound`, `recall` MUST skip that hit's
reinforcement and neighbour propagation without logging at error level, and MUST still return the
hit. Any other reinforce error MUST be logged at error level and skipped, as before. Result order
MUST NOT change.

#### Scenario: Hit deleted before its reinforce, at fixed virtual time
- **WHEN** at virtual time T recall returns three hits and `Reinforce` on the second returns `engram.ErrNotFound`
- **THEN** all three hits are returned in the same order, no error-level record is logged, and `PropagateReinforce` is not called for the second

#### Scenario: Every hit deleted
- **WHEN** `Reinforce` returns `engram.ErrNotFound` for every hit of a recall
- **THEN** the hits are returned, no error-level record is logged, and no propagation runs
