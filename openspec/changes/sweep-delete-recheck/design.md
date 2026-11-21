## Context

The decay sweep hard-deletes memories the decay model has written off. Since `sweep-on-start` it
runs one minute into every `engramd` session and then hourly. `SweepOnce` works in two steps:

```go
cands, err := s.pruner.PruneCandidates(ctx, now.Add(-s.grace), s.batch)
...
for _, m := range cands {
	if m.Pinned { continue }
	if s.decay.Retrievability(m, now) < s.hardFloor {
		if err := s.pruner.Delete(ctx, m.ID); err != nil { ... }
		pruned++
	}
}
```

The candidates are a snapshot. Between the read and a row's delete, another caller can change
that row. Facts, each read in the code or measured on the live Neo4j 5.26:

- `Delete` (`neo4j/store.go:412`) is `MATCH (m:Memory {id: $id}) WITH m, count(m) AS c DETACH DELETE m RETURN c`. It does not look at the row.
- Neo4j evaluates `MATCH` and `WHERE` before it takes the row's write lock. A conditional delete
  written as `MATCH ... WHERE ... DETACH DELETE` still deleted the row in about 95% of
  overlapping rounds, after the writer had been told it succeeded.
- `Reinforce` (`neo4j/store.go:180`) and `Pin` (`:384`) are `MATCH ... SET ... RETURN count(m)`.
  When the row is deleted while they wait for its lock, the `SET` is a silent no-op and the
  count is still 1. They return nil for a row that is gone.
- Writers that can turn "delete" into "keep": `Pin` (retrievability becomes 1) and `Reinforce`
  and `PropagateReinforce` (`last_accessed` becomes now). `Supersede` only lowers stability and
  `SetForgotten` does not feed decay, so neither can. No handler overwrites a row with `Put`.
  So "unpinned and `last_accessed` before the cutoff" is a complete re-check.
- `remember` dedup (`mcp/remember.go:169-183`): on any `Reinforce` error it logs and returns
  `remember: store unavailable`. The content is not stored.
- `recall` (`mcp/recall.go:276`): a failed `Reinforce` is logged at error level and skipped.
- `forget` with mode pin or soft returns `forget "<id>": ...: memory not found` for a missing row.
- `Link` skips a target that was deleted before its statement starts, in both stores
  (`neo4j/store.go:249`, `mock/memstore.go:146`). A target deleted while the statement runs is
  different: `Link` returns `Neo.ClientError.Statement.EntityNotFound`. Measured over 100
  overlapping rounds, twice each: 65 and 66 against the unconditional `Delete` that is on `main`
  today, 62 and 58 against the lock-first delete (81 in an earlier probe with other timing). In
  the other rounds the target was deleted and no edge was made. `remember` answers a failed
  `Link` with `remember: store unavailable`, after `Put` has stored the content.
- The eval calls `sweep.New` and `SweepOnce` on a `mock.MemStore`, from one goroutine, with
  virtual time (`eval/arm.go:215-217`).

**Ports.** `sweep.Pruner` is the consumer-side interface in package `sweep`; `*neo4j.Store`,
`*mock.MemStore` and `*mock.FakeStore` implement it. `mcp` declares its own store interfaces
(`ServerStore`, `forgetStore`), which keep the unconditional `Delete`. This change adds no
package and no interface. It swaps one method on `Pruner`, so the interface stays at two.

## Goals / Non-Goals

**Goals:**

- A row pinned or reinforced during a pass is not deleted by that pass.
- `Reinforce`, `Pin` and `SetForgotten` never report success on a row that is gone.
- `remember` never loses its content because its dedup target was deleted.
- The eval result does not change.

**Non-Goals:**

- Coordinating passes between processes (`sweep-daily-claim`).
- Changing which rows are candidates, the grace period, the floor or the batch size.
- Consolidation: trying another dedup candidate when the first is gone.
- Recovering rows lost before the fix. There is no record of them.
- The other writes that can meet a delete. They are known and left as they are: a `Link` whose
  target is deleted mid-statement fails (see Risks); `Supersede` and `PropagateReinforce` never
  claim that one given row was written; a hard `forget` deletes whatever it names, including a
  row another session has just reinforced or pinned, because the agent asked for exactly that.

## Decisions

### D1. A sweep-only delete that locks, then re-checks

```go
// DeleteIfStale deletes the memory and its relationships only if it is still stale: unpinned,
// with last_accessed before `before`, the predicate PruneCandidates(before) uses. It reports
// whether it deleted. A row that changed since the sweep read it, or that is already gone, is
// (false, nil). The re-check is complete only while every write that raises retrievability
// sets last_accessed or pinned.
func (s *Store) DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (bool, error)
```

```cypher
MATCH (m:Memory {id: $id})
SET m.sweep_check = true
REMOVE m.sweep_check
WITH m
WHERE m.last_accessed < $before AND coalesce(m.pinned, false) = false
DETACH DELETE m
RETURN count(*) AS deleted
```

The `SET` is there for its lock. Neo4j takes a node's write lock when a statement first writes
to it, not when it matches it. Writing a dummy property first makes the statement wait for any
writer that got there earlier, and the `WHERE` after it reads committed values. The property is
removed in the same statement, so no row ever carries it. Both parameters are bound; `before`
is passed as UTC.

This is the form the Neo4j Operations Manual gives for the problem: "Concurrent data access",
"Lost updates", example "Dummy property to acquire a write lock"
(`https://neo4j.com/docs/operations-manual/current/database-internals/concurrent-data-access/`).
A code comment explains the pair and cites that section. Without it the `SET`/`REMOVE` reads as
dead code and the next cleanup removes it.

An earlier draft computed the predicate between the `SET` and the `REMOVE`. Both forms gave the
same result in every probe cell (stale deleted, fresh kept, pinned kept, missing id 0, no
property left behind, no lost write). The shorter one is the documented one, so it is kept.

The alternative `SET m.id = m.id` also took the lock in the probe. It is rejected: it writes the
property the uniqueness constraint covers, and a no-op `SET` taking the lock is observed
behaviour, not documented.

Cost: 7 to 9 ms median per call in the probe, against 9 ms for the unconditional `Delete`. No
measurable difference.

### D2. `sweep.Pruner` swaps `Delete` for `DeleteIfStale`

```go
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (deleted bool, err error)
}
```

```go
before := now.Add(-s.grace)
cands, err := s.pruner.PruneCandidates(ctx, before, s.batch)
...
deleted, err := s.pruner.DeleteIfStale(ctx, m.ID, before)
if err != nil {
	s.log.Error("sweep: delete failed", "id", m.ID, "err", err)
	continue
}
if deleted {
	pruned++
}
```

One cutoff feeds the candidate read and every delete of the pass. The branch that skipped
`engram.ErrNotFound` from `Delete` goes away: a missing row is now `(false, nil)`.

Why swap and not add: the sweep has no use for an unconditional delete, and a third method
would be dead surface on every implementation. Why not make `Delete` itself conditional:
`forget` with mode hard reaches the same method and must delete whatever the agent names.

`(false, nil)` covers both "kept because it changed" and "already gone". The sweep does the
same thing for both: no count, no error-level log, next candidate. Nothing consumes the
difference, so there is no third state and no counter of kept rows.

### D3. Writers match the row again after their write

```cypher
// Pin; SetForgotten is the same with m.forgotten
MATCH (m:Memory {id: $id})
SET m.pinned = true
WITH m
MATCH (x:Memory {id: $id})
RETURN count(x) AS c
```

```cypher
// Reinforce
MATCH (m:Memory {id: $id})
SET m.last_accessed = $now
WITH m
MATCH (x:Memory {id: $id})
SET x.access_count = x.access_count + 1
RETURN count(x) AS c
```

Three literal queries, no shared Cypher fragment. When the row was deleted while the writer
waited, the second `MATCH` finds nothing, the count is 0, and the existing code already maps 0
to `engram.ErrNotFound`.

`Reinforce` writes `last_accessed` first and increments `access_count` on the re-matched row,
for a measured reason. The obvious form, `SET m.access_count = m.access_count + 1,
m.last_accessed = $now` followed by the re-match, reads a property before it takes the lock.
Against the lock-first delete that deadlocks: `Neo.TransientError.Transaction.DeadlockDetected`
on 6 to 12 of 60 overlapping calls. The driver retries, so no call failed and no write was
lost, but each retried call took about 2.1 s instead of about 8 ms. The form above writes
before it reads: 0 deadlocks, 0 retries and 0 lost writes in 120 overlapping rounds (60 against
each of the two delete forms of D1), slowest call 46 ms. A comment on the query says why the increment is on the second line. The increment
now reads `access_count` after the lock is held, which is the form the manual gives against a
lost update between two concurrent `Reinforce` calls. That pairing was not probed and is not
claimed.

`PropagateReinforce` and `Supersede` are left alone. Neither tells a caller that one given row
was written: the first is best effort over neighbours, the second returns nil for absent ids.

`SetForgotten` goes beyond the defect, since a soft forget of a deleted row harms no one. It is
included because it shares `flagOne` with `Pin`, and `mcp/forget.go` promises a legible
not-found for soft, hard and pin alike.

### D4. `remember` stores the content when its dedup target is gone

```go
if target, ok := t.dedupTarget(candidates, replaces, mt, in.Content); ok {
	err := t.store.Reinforce(ctx, target.ID, t.clock.Now())
	switch {
	case err == nil:
		// existing: link entities, return deduped
	case errors.Is(err, engram.ErrNotFound):
		// The target was deleted since the search. Fall through and store the content as a
		// new memory: failing here would lose it.
	default:
		t.log.Error("remember: reinforce failed", "err", err)
		return RememberOutput{}, errors.New("remember: store unavailable")
	}
}
// existing insert path
```

It is a fall-through into the insert path that is already there, not a second insert block. The
gone target stays in `candidates`. Its delete has committed before `Reinforce` returned, so the
`Link` that follows starts after it and skips it; auto-linking needs no change.
`applySupersedes` runs once, on the insert path, as it does for any insert.

`LinkEntities` after a successful `Reinforce` is safe from the sweep, because `Reinforce` has
committed `last_accessed = now` and `DeleteIfStale` will then keep the row. A comment says so.

The handler does not walk on to the next candidate. If a second live candidate also scores at
or above 0.95, the content is still stored as a new memory, so the namespace then holds a
near-duplicate of that candidate, auto-linked to it. This needs the race and two candidates
above the dedup point at once. Walking on would be a loop of store writes inside `remember` and
is the first step of consolidation, which is v2. Open question 4 puts the choice to Franco.

Every branch of the dedup step, and what happens to the incoming content and to the rows it
touches. "New" marks what this change adds:

| Branch | Incoming content | Dedup target | Rows named in `supersedes` |
|---|---|---|---|
| No candidate qualifies: none at or above 0.95, or each one is skipped (superseded, soft-forgotten, named in `supersedes`, or of another type or content on a correcting write) | Stored as a new memory | None | Superseded |
| Target reinforced | Merged: not stored again, and the target carries it | `access_count` + 1, `last_accessed` = now; supplied entities linked | Superseded |
| Target reinforced, then `LinkEntities` fails | Not stored again; the reply is `remember: store unavailable`; the target already carries the content | Reinforced; entities not linked | Not applied; a retry applies them |
| **New.** Target gone (`ErrNotFound`) | Stored as a new memory; reply `deduped: false` | Already deleted; nothing to do | Superseded |
| **New.** Target gone, and a second live candidate is also at or above 0.95 | Stored as a new memory, a near-duplicate of that candidate and auto-linked to it | Second candidate is not reinforced | Superseded |
| **New.** Target gone, then `Put` fails | Not stored; the reply is `remember: store unavailable` | Already deleted | Not applied |
| Stored, then `LinkEntities` or `Link` fails (any insert) | Stored; the reply is still `remember: store unavailable`; a retry dedups onto the stored memory | None | Not applied by this call; the retry applies them |
| `Reinforce` fails for another reason | Not stored; the reply is `remember: store unavailable` | Unknown | Not applied |
| Target is in another namespace | Unchanged: the search is scoped to the write's namespace | Left alone | Superseded |
| Target is pinned | Merged, as for any target. The sweep never deletes a pinned row; a hard `forget` from another session can, and then this is "Target gone" | Reinforced | Superseded |

The three rows marked new turn a lost write into a stored one, or keep an honest error.

### D5. `recall` skips a vanished hit quietly

In `reinforce` (`mcp/recall.go:274-284`), an `ErrNotFound` from `Reinforce` is logged at debug
level and skipped, without the neighbour propagation. Any other error is logged at error level
as today. The hit stays in the reply: recall read it before it was deleted.

With D3, `Reinforce` reports this case reliably, so without D5 a benign race would write an
error line into the host's MCP log. Debug and not silence, so that a store that starts
returning not-found for live rows can still be seen with the log level turned down.

### D6. The fakes

- `mock.MemStore.DeleteIfStale` checks, under the store's mutex, that the row exists, is
  unpinned and has `LastAccessed` before `before`, then deletes. An unknown id is `(false, nil)`.
  It shares one unexported cascade, `deleteLocked`, with `Delete`, so the edge and entity
  bookkeeping cannot drift between the two.
- `mock.FakeStore.DeleteIfStale` appends the id to `Deleted`, appends `before` to a new
  `StaleBefore` slice, and returns `(!f.KeptIDs[id] && f.DeleteErr == nil, f.DeleteErr)`. The
  zero value deletes, so every existing sweep test keeps its meaning. `PruneCandidates` records
  its cutoff in a new `PruneBefore` field so a test can assert the two cutoffs are equal.
- `mock.FakeStore` scripts per id, because the new scenarios need one row kept beside one
  deleted, and one hit gone beside two live: `KeptIDs map[engram.MemoryID]bool` (ids
  `DeleteIfStale` reports kept), `ReinforceErrs map[engram.MemoryID]error` (checked before the
  store-wide `ReinforceErr`), and `Propagated []engram.MemoryID` (every id given to
  `PropagateReinforce`, beside the existing `LastPropagateID`). A nil map reads as empty, so the
  zero value is unchanged. The scripting lives in `mock/`, not in wrappers local to each test
  file.

### D7. Tests

- **Sweep, unit.** A kept row is not counted and logs nothing; an error is logged and not
  counted; the cutoff passed to `DeleteIfStale` equals the one passed to `PruneCandidates` and
  is `now - grace`.
- **Fake, unit.** `MemStore.DeleteIfStale`: stale row deleted with its links and mentions; a
  row accessed at exactly `before` kept (the boundary); pinned kept; reinforced kept; unknown id
  `(false, nil)`.
- **Neo4j, integration.** On real rows: stale row deleted with no relationship left; fresh row
  kept with no `sweep_check` property; pinned kept; `last_accessed` equal to the cutoff kept and
  one millisecond earlier deleted; unknown id `(false, nil)`.
- **Neo4j, integration, concurrent.** `Reinforce` against `DeleteIfStale`, and `Pin` against
  `DeleteIfStale`, 100 rounds each, released together with a head start of 0 to 2 ms that
  alternates. Every round must end in one of two states: writer nil, delete false, row present
  with the write applied; or writer `ErrNotFound`, delete true, row gone. Anything else fails
  the test, and so does a run in which one of the two states never occurred, so a run that never
  overlapped does not pass by accident. The test asserts no wall time: a deadlock retry costs 2 s
  and is correct, so a time limit would only make it flaky. Its context deadline is a hang
  guard. Measured in the plan's dry run under `-race`: six runs, every round in one of the two
  states, the writer first in 42 to 52 rounds of 100 and the delete first in the rest. The three
  timed runs took about 2 s for the 200 rounds. Run against the old writers the same test failed, with rounds in
  which the writer returned nil and the row was gone; that failure is the proof that the rounds
  overlap. Run against the final writers and a delete without its dummy write, or with the
  predicate computed before the dummy write, it failed in three runs of three, with 51 to 60
  bad rounds of 100 per pairing; the sequential test passed on the same broken delete. So the
  race test is what guards the lock, and the plan repeats that check once during apply.
  `SetForgotten` is not raced, because a race on it cannot be told from outside: a soft forget
  does not keep a row from the sweep, so "forgotten, then swept" and "acknowledged on a deleted
  row" end the same. It has the statement shape of `Pin` and is tested for an unknown id. The test
  passes its own cutoff of one hour,
  on rows two hours old, so they are stale for the test and never for a real sweep, as
  `CONTRIBUTING.md` requires. It removes its rows in `t.Cleanup`.
- **`remember`, unit.** Target gone: `Put` is called with the content, the reply has
  `deduped: false` and the new id, `supersedes` is applied. Target gone beside a second
  candidate at 0.96: a new memory is stored and the second candidate is not reinforced. Other
  `Reinforce` error: the reply is `remember: store unavailable` and `Put` is not called.
- **`recall`, unit.** A hit whose `Reinforce` returns `ErrNotFound` is returned, nothing is
  logged at error level, and no propagation runs for it.
- **Fake, unit, concurrent.** The existing race test of `Search` against `Delete` on `MemStore`
  also runs `Reinforce` and `DeleteIfStale`, under `-race`.

### D8. The eval is unaffected, and that is checked

`SweepOnce` will call `DeleteIfStale` on `mock.MemStore`. Inside an eval arm the re-check is
true for every candidate: one goroutine drives the store, `PruneCandidates` returns unique ids
in insertion order, nothing under `eval/` pins a row between the two calls, and one `before`
feeds both. So the same rows are deleted and the same count is returned.

The real risk is the cascade. If `DeleteIfStale` deleted a row differently from `Delete`, the
edge and bridge counts in the table would move. D6 removes that risk by sharing the cascade.

No eval constant, mix, weight, threshold or dataset size is touched, so there is nothing to
preregister. The prediction: `go run ./cmd/eval --ci` passes, and its full output equals
`main`'s once the two wall-clock columns (p50, p95) are removed. It is run right after the
`mock` and `sweep` tasks. Any difference stops the work and goes to Franco.

## Risks / Trade-offs

- **[Lock-first depends on the query plan.]** If Neo4j read `last_accessed` or `pinned` before
  the `SET`, the comparison would use values from before the lock and the bug would return. The
  writers' second `MATCH` (D3) rests on the same thing: a read after the lock sees the committed
  state. The manual documents the pattern, and it is measured on 5.26 only. → The concurrent
  integration test is the guard for both. CI does not run integration tests, so it is rerun
  whenever the Neo4j image changes; `CONTRIBUTING.md` says so.
- **[A writer that reads before it writes deadlocks with the delete.]** Measured for the first
  draft of `Reinforce` (D3). The final `Reinforce` and `Pin` showed none in the probe (60
  rounds each against the final delete), and the three timed dry runs, at about 2 s for 200
  rounds, are too short to hold a 2.1 s retry. That does not prove none can happen. → A deadlock is a transient error; `ExecuteQuery` retries it,
  the call takes about 2 s and stays correct. A future writer on `:Memory` must write before it
  reads; the comment on `Reinforce` says so.
- **[The re-check rests on an invariant.]** Every write that raises retrievability must set
  `last_accessed` or `pinned`. That holds today. A future writer that changed only importance
  or stability would slip past. → Stated in the `DeleteIfStale` doc comment.
- **[Deadlock.]** `DeleteIfStale` holds the row's lock while `DETACH DELETE` locks its
  neighbours, and `PropagateReinforce` locks several rows. Neo4j detects a deadlock and fails
  one transaction with a transient error. → `ExecuteQuery` retries transient errors. If the
  sweep's statement still fails it is logged and the row is tried again next pass.
- **[Rows lost before the fix cannot be found.]** → Disclosed in the proposal and the PR body.
- **[`remember` now inserts where it used to fail.]** An agent that retried on the old error
  would have stored the content anyway; one that did not retry lost it. → The new reply is the
  ordinary insert reply. No description change is needed.
- **[An auto-link whose target is deleted mid-statement fails.]** Probed: `Link` returned
  `EntityNotFound` in 58 to 66 of 100 overlapping rounds in the two-run probe (81 in an earlier
  one), and left no edge. The content is
  stored before `Link` runs, so the agent sees `remember: store unavailable` after a successful
  insert, `supersedes` is not applied by that call, and a retry dedups onto the stored memory
  and applies it. It is not new: against the unconditional delete on `main` the same probe gave
  65 and 66 of 100, against the lock-first delete 62 and 58. This change neither adds nor
  removes it. → Reported. Not fixed here (open question 3).
- **[A near-duplicate when the target is gone and a second candidate is also above 0.95.]**
  See D4. → Stated in the spec; open question 4.

## Migration Plan

No data migration and no schema change. Rollback is reverting the PR. Reverting brings the race
back; it loses nothing by itself.

## Open Questions

Closed on 2026-11-21: Franco reviewed the visual plan and said to go ahead, without changing an
answer. The recommended option stands for each question below.

1. Should `forget` with mode pin say more than "memory not found" when the memory was pruned?
   Recommended: leave the message as it is in this change.
2. Lock-first is documented by Neo4j, measured on 5.26 only and guarded by a test CI does not
   run. Recommended: accept, and rerun the test when the Neo4j image changes.
3. A `remember` whose auto-link target is deleted mid-statement answers `remember: store
   unavailable` although the content is stored. It predates this change, and the probe shows
   the same rate with the old delete and the new one. Recommended: leave it
   out and open a follow-up; fixing it means a lock-first `Link` or a best-effort link step,
   and either is a behaviour change of its own.
4. Target gone and a second live candidate at or above 0.95: store a near-duplicate (the design)
   or walk on and dedup into the second candidate? Recommended: store. It is rare, loses
   nothing, and keeps `remember` to one reinforce attempt.
