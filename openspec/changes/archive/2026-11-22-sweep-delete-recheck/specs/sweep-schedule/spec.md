## ADDED Requirements

### Requirement: A pass deletes only rows that are still stale
`SweepOnce` MUST delete a candidate through `DeleteIfStale(ctx, id, before)`, and MUST count it as
pruned only when that call reports it deleted. `before` MUST be `now` minus the grace period, and one
value MUST be used for the candidate read and for every delete of the pass. The unconditional
`Delete` MUST remain for `forget` with mode hard.

#### Scenario: One cutoff per pass, at fixed virtual time
- **WHEN** `SweepOnce` runs at virtual time T with a grace period of 30 days
- **THEN** the `before` given to `PruneCandidates` and to every `DeleteIfStale` is T minus 30 days

#### Scenario: A kept candidate is not counted
- **WHEN** a pass has two candidates below the floor, and `DeleteIfStale` reports the first deleted and the second kept
- **THEN** `SweepOnce` returns 1

### Requirement: The stale delete re-checks under the row lock
On the Neo4j store, `DeleteIfStale` MUST take the write lock of the memory before it reads the
memory's `last_accessed` and `pinned`, and MUST delete only when the memory is unpinned and its
`last_accessed` is before `before`.
It MUST return false and no error for a memory that is no longer stale or that does not exist, and
MUST leave no property behind on a memory it keeps.

#### Scenario: Pinned after the candidate read, at fixed virtual time
- **WHEN** at virtual time T a pass has read a memory as a candidate, and the memory is pinned before the pass deletes it
- **THEN** `DeleteIfStale` returns false, and the memory exists and is pinned

#### Scenario: Reinforced after the candidate read
- **WHEN** a pass has read a memory as a candidate, and a `Reinforce` commits on it before the pass deletes it
- **THEN** `DeleteIfStale` returns false and the memory exists with the new `last_accessed`

#### Scenario: Cutoff boundary
- **WHEN** one memory has `last_accessed` exactly equal to `before`, and another has it one millisecond earlier
- **THEN** `DeleteIfStale` keeps the first and deletes the second

#### Scenario: Nothing left on a kept memory
- **WHEN** `DeleteIfStale` keeps a memory
- **THEN** the memory has no `sweep_check` property

#### Scenario: Relationships go with the memory
- **WHEN** `DeleteIfStale` deletes a memory that has link edges in both directions and an entity mention
- **THEN** no relationship to it remains

#### Scenario: Unknown id
- **WHEN** `DeleteIfStale` is called with an id no memory has
- **THEN** it returns false and no error

## MODIFIED Requirements

### Requirement: A row already deleted is not an error
When `DeleteIfStale` reports that it did not delete a candidate, because the memory is gone or is no
longer stale, `SweepOnce` MUST skip it: it MUST NOT count it as pruned, MUST NOT log it at error
level, and MUST continue with the next candidate. An error from `DeleteIfStale` MUST be logged at
error level and skipped. No delete error MUST be counted as pruned or returned from `SweepOnce`.

#### Scenario: Every candidate already gone, at fixed virtual time
- **WHEN** at virtual time T a pass has two candidates below the floor and `DeleteIfStale` returns false with no error for both
- **THEN** `SweepOnce` attempts both, returns 0 and no error, logs no error-level record, and the pass is recorded with `LastRunAt` T and `LastPruned` 0

#### Scenario: Another delete error is still reported
- **WHEN** `DeleteIfStale` returns an error for a candidate below the floor
- **THEN** `SweepOnce` returns 0 and no error, and one error-level record is logged

### Requirement: The eval does not move
This capability MUST NOT change `sweep.New`, any eval constant or `eval/baseline.json`, and MUST NOT
move any eval figure. On a store that no other caller writes to during the pass, `SweepOnce` MUST
return the same count and error as it did with the unconditional delete. The eval MUST keep driving
the sweep through `SweepOnce` with virtual time and MUST NOT call `Run`.

#### Scenario: Eval sweeps on virtual time only
- **WHEN** an eval arm advances one virtual day
- **THEN** it calls `SweepOnce` with that day's virtual `now`, and no code under `eval/` or `cmd/eval/` calls `Run`

#### Scenario: Gate with zero drift
- **WHEN** verify runs `go run ./cmd/eval --ci` and `git diff origin/main -- eval/ cmd/eval/`
- **THEN** the gate passes, the type-aware precision and nDCG equal `eval/baseline.json` at the three decimals the report prints and not merely within the 0.02 tolerance, every figure of every arm equals the output of `main` once the p50 and p95 columns are removed, and the diff is empty
