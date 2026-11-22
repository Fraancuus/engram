## Design Summary

**The defect.** The decay sweep reads its candidates, then deletes each one by id, and the delete
does not look at the row again. A row that is pinned or reinforced between the read and its
delete is deleted anyway. Franco merged `sweep-on-start` (PR #38) knowing this and chose to fix
it next. The race is as old as M4's sweep; since #38 a pass runs one minute into every session,
so short sessions now reach it.

**What the scout found: the defect has two halves.**

1. **Sweep side.** `SweepOnce` calls the store's unconditional `Delete`
   (`neo4j/store.go:412`). Adding a `WHERE` to that delete does not help. Neo4j evaluates
   `MATCH` and `WHERE` before it takes the row's write lock, so the check reads the value from
   before the writer's change. In a probe, about 95% of overlapping rounds still deleted a row
   whose writer had just been told it succeeded.
2. **Writer side.** `Reinforce` and `Pin` have the same flaw. Each is
   `MATCH ... SET ... RETURN count(m)`. It matches, waits behind the sweep's delete, applies
   its `SET` to the deleted row as a silent no-op, and returns a count of 1. The caller is told
   the write happened. With only the sweep side fixed, 85 of 200 overlapping rounds still ended
   that way.

**What that means for an agent today.**

- `forget` with mode pin can answer OK for a memory the sweep then deletes. The PRD says pinned
  memories are exempt.
- `remember` can dedup onto a row, answer `deduped: true` with its id, and the row is deleted.
  The incoming content is stored nowhere.
- Nothing records which rows were lost. A successful delete logs no id.

It needs a row that is past the 30-day grace and below the floor, and a write to that row
inside the length of one pass. On a single-user store that is rare. When it happens it is
silent and permanent.

**The fix, both halves.**

- **A sweep-only delete that locks, then re-checks.** `DeleteIfStale(ctx, id, before)` writes a
  dummy property to take the row's lock, re-reads `last_accessed` and `pinned`, removes the
  dummy property, and deletes only if the row is still a candidate. It returns whether it
  deleted. A row that changed, or that is already gone, is `false` with no error.
- **`sweep.Pruner` swaps `Delete` for `DeleteIfStale`.** `SweepOnce` computes one cutoff, uses it
  for the candidate read and for every delete, and counts a row only when it was deleted.
- **Writers re-check after their write.** `Reinforce`, `Pin` and `SetForgotten` match the row
  again after the `SET` and return `engram.ErrNotFound` when it is gone.
- **`remember` keeps the content.** When the dedup target's `Reinforce` returns `ErrNotFound`,
  the handler stores the content as a new memory. Today it answers
  `remember: store unavailable`, which is false when the store is healthy.
- **`recall` stays quiet.** A hit whose row vanished is skipped without an error-level log.
- **The fakes follow**, so the eval and the unit tests see the same semantics.

**Proved on the live Neo4j, on real `:Memory` rows.** 200 rounds per cell, writer and sweeper
released together:

| Cell | Row kept, writer's change present | Row deleted, writer told not found | Writer told success, row gone |
|---|---|---|---|
| New `Reinforce` against `DeleteIfStale` | 107 | 93 | 0 |
| New `Pin` against `DeleteIfStale` | 106 | 94 | 0 |
| Old `Reinforce` against `DeleteIfStale` (control) | 104 | 11 | 85 |

Both orders occurred in every cell, and the same cells through two separate drivers also gave
0. The race detector was clean. One `DeleteIfStale` took 8.7 ms on average against 10.5 ms for
the old `Delete`, so the lock costs nothing measurable.

## Alternatives Considered

### Alternative A: Fix the sweep side only

- **Approach**: `DeleteIfStale` and the `Pruner` swap. Leave the writers alone.
- **Pros**: one store method, no change to `remember`.
- **Cons**: the control cell shows it. A writer that arrives second is still told it succeeded
  on a row that is gone: 85 of 200.
- **Why not chosen**: it does not close the defect.

### Alternative B: Fix the writers only

- **Approach**: writers re-check and report `ErrNotFound`. The sweep keeps its unconditional
  delete.
- **Pros**: no interface change.
- **Cons**: a writer that arrives first commits its pin, and the sweep then deletes the pinned
  row.
- **Why not chosen**: it still breaks "pinned memories are exempt".

### Alternative C: Both sides, and `remember` fails loudly when its target is gone

- **Approach**: both halves, but `remember` keeps answering `remember: store unavailable`.
- **Pros**: no behaviour change in `remember`.
- **Cons**: the message is false, the tool description gives no retry rule for it, and an agent
  that reads it as an outage does not retry. The content is lost.
- **Why not chosen**: storing the content is the only outcome that never loses it.

### Alternative D: Both sides, and `remember` stores the content (chosen)

- **Approach**: as in the summary. On `ErrNotFound` from the dedup target, fall through to the
  normal insert.
- **Pros**: no path loses content or a pin silently. The reply is the ordinary insert reply.
- **Cons**: one more branch in `remember`; a swap of one method on `sweep.Pruner`, which both
  fakes must follow.
- **Why chosen**: it is the smallest change that closes both halves.

### Also dropped

- **Optimistic concurrency on the snapshot** (delete only if `last_accessed` still equals the
  value the sweep read). It needs timestamp equality through the driver and still needs the
  pinned check. The candidate predicate already exists in both stores.
- **Taking the lock with `SET m.id = m.id`.** It worked in the probe, but it writes the key the
  uniqueness constraint covers, and a no-op `SET` taking the lock is observed, not documented.
- **A mutex inside `engramd`.** Each session is its own process, so a mutex cannot arbitrate
  between them.
- **Trying the next dedup candidate when the first is gone.** That moves toward consolidation,
  which the PRD defers to v2.

## Agreed Approach

Alternative D. Franco chose the direction when he merged #38 and asked for this fix before the
next piece of work. He has not reviewed this design; the open questions go into its visual plan.

**One change, one PR.** Either half alone still loses data, so they ship together.

**The eval does not move.** `SweepOnce` will call `DeleteIfStale` on `mock.MemStore`. Inside the
eval one goroutine drives the store at virtual time, the candidates are unique, and one cutoff
feeds both calls, so the re-check is true for every candidate. The risk is elsewhere: a second
copy of the fake's delete cascade could drift and move the edge counts. `DeleteIfStale` and
`Delete` on the fake therefore share one cascade. The eval gate and a full-output comparison
with `main` check it, and any figure that moves stops the work.

**The clock stays injected.** `SweepOnce` derives the cutoff from the `now` it is given. Nothing
reads the wall clock.

## Key Decisions

1. `sweep.Pruner` keeps two methods: `PruneCandidates` and
   `DeleteIfStale(ctx, id, before) (deleted bool, err error)`. The unconditional `Delete` stays
   on the store for `forget` mode hard, through the `mcp` interfaces that already declare it.
2. "Stale" means "still returned by `PruneCandidates(before)`": unpinned, and `last_accessed`
   before the cutoff. The doc comment says so, because `neo4j/store.go` already uses the word
   for superseded rows.
3. `false` with no error covers both "kept because it changed" and "already gone". The sweep
   treats them the same. The branch added in #38 that skips `ErrNotFound` from `Delete` goes
   away.
4. The lock is taken by writing a dummy property and removing it in the same statement. A code
   comment explains it and cites the Neo4j manual; without the comment the pair reads as dead
   code.
5. `Reinforce`, `Pin` and `SetForgotten` re-match after their `SET`. Three literal queries, no
   shared Cypher fragment. `PropagateReinforce` and `Supersede` are left alone: neither tells a
   caller that one given row was written.
6. `SetForgotten` is included although a soft forget of a deleted row harms no one. It shares
   `flagOne` with `Pin`, and `mcp/forget.go` promises a legible not-found for it.
7. `remember` falls through to its existing insert path. No second insert block, no retry, no
   next candidate. `Link` already skips a target that no longer exists, in both stores.
8. `recall` skips `ErrNotFound` from a hit's `Reinforce` without an error-level log.
9. `mock.MemStore.DeleteIfStale` and `Delete` share one unexported cascade. `mock.FakeStore`
   gets a scripted `DeleteIfStale`.
10. The concurrent probe becomes an integration test in `neo4j/`. It passes its own cutoff of an
    hour, so its rows are never stale for a real sweep, as `CONTRIBUTING.md` requires.

## Open Questions

1. **Should `forget` with mode pin say more than "memory not found"?** The scope review suggests
   adding a hint that the memory may have decayed and been pruned. Recommended: leave the
   message as it is in this change.
2. **Lock-first depends on the query plan, and it is proven on Neo4j 5.26 only.** A read of
   `last_accessed` before the `SET` would reuse the value from before the lock. The integration
   test is the guard, and CI does not run integration tests. Recommended: accept, and rerun the
   test whenever the Neo4j image changes.
3. **An auto-link whose target is deleted mid-statement is not probed.** `remember` links a new
   memory to similar candidates, and the sweep can delete one of them at that moment. The
   content is already stored by then, so the worst case is an error after a successful insert.
   Recommended: probe it during the plan dry-run and report it, without widening this change.
