## Why

The decay sweep reads its candidates and then deletes each by id without looking at the row again, and `Reinforce` and `Pin` report success for a row that was deleted while they waited for its lock. So a memory pinned or reinforced during a pass can be deleted while the agent is told OK, and a `remember` that dedups onto it answers `deduped: true` while its content is stored nowhere. The race is as old as M4's sweep. Since PR #38 a pass runs one minute into every session, so ordinary sessions now reach it. It is rare, silent and permanent.

## What Changes

**Milestone.** No new milestone. This corrects M4 (PRD section 9, "M4 — Type-aware decay": scheduled sweep, reinforcement, `forget`/pin) and restores PRD section 6.3, "Pinned memories are exempt". Post-M6 cleanup, after `sweep-on-start`.

**The sweep deletes only rows that are still stale**
- From: `SweepOnce` calls the store's unconditional `Delete(ctx, id)`.
- To: it calls `DeleteIfStale(ctx, id, before)`, which takes the row's write lock, re-checks that the row is unpinned and that `last_accessed` is before the cutoff, and deletes only then. It returns whether it deleted.
- Reason: a `WHERE` on the old delete is not enough. Neo4j evaluates it before it takes the lock.
- Impact: `sweep.Pruner` swaps `Delete` for `DeleteIfStale`. The unconditional `Delete` stays for `forget` mode hard.

**Writers report a row that is gone**
- From: `Reinforce`, `Pin` and `SetForgotten` return success when their `SET` landed on a row deleted while they waited.
- To: each matches the row again after its `SET` and returns `engram.ErrNotFound` when it is gone.
- Impact: `forget` with mode pin or soft answers "memory not found" for such a row. `SetForgotten` is included because it shares the helper with `Pin`; a soft forget of a deleted row harmed no one.

**`remember` keeps the content when its dedup target is gone**
- From: the handler answers `remember: store unavailable`, and the content is not stored.
- To: it stores the content as a new memory and answers the ordinary insert reply, `deduped: false` with the new id. Any other `Reinforce` error still answers `remember: store unavailable`.
- Impact: a behaviour change for the calling agent, in its favour. No tool description changes.

**`recall` does not log a vanished hit as an error**
- A hit whose row was deleted before its reinforce is logged at debug level and skipped. The hit is still returned.

**The fakes follow.** `mock.MemStore` gets `DeleteIfStale` with the same semantics, sharing one delete cascade with `Delete`. `mock.FakeStore` gets a scripted one.

**What cannot be recovered.** A row lost to this race before the fix is gone: `DETACH DELETE` leaves no tombstone, and the sweep logs no id on a successful delete, so the affected memories cannot be listed. Nothing suggests it has happened; the precondition is a write to a row that is past the 30-day grace and below the floor, inside the length of one pass.

**Out of scope**
- Trying the next dedup candidate, or merging, when the first is gone. That is consolidation, which the PRD defers to v2. The cost: when a second live candidate also scores at or above 0.95, a near-duplicate of it is stored.
- A `remember` whose auto-link target is deleted mid-statement. `Link` then fails, and the agent is told `remember: store unavailable` although the content is stored. It exists on `main` today and is a follow-up.
- A counter of rows the sweep kept, or a retry loop on `Reinforce`.
- Changing the message of `forget` for a pruned memory.
- `PropagateReinforce` and `Supersede`: neither tells a caller that one given row was written.
- Ordering or paging `PruneCandidates`, and stopping a pass when its context is cancelled. Both are listed follow-ups from `sweep-on-start`.
- `sweep-daily-claim`, the once-a-day gate. It follows this change.

## Capabilities

### New Capabilities
- `write-race-safety`: what a store writer and the MCP handlers do when the row they write was deleted under them, so that no write is lost or acknowledged falsely.

### Modified Capabilities
- `sweep-schedule`: a pass deletes only rows that are still stale; "A row already deleted is not an error" is restated for `DeleteIfStale`; "The eval does not move" no longer forbids the change to the delete call.
- `memory-store-fake`: "Lifecycle and prune semantics" gains `DeleteIfStale`, and the concurrency scenario of "MemStore satisfies the consumer interfaces" races it.

## Impact

- `sweep/sweep.go`: `Pruner`, `SweepOnce`. `sweep/sweep_test.go`: the already-deleted test, a cutoff test, the `panicPruner` double.
- `neo4j/store.go`: `DeleteIfStale`; the queries of `Reinforce`, `Pin`, `SetForgotten`. A new integration test that races a writer against the sweep delete on real rows.
- `mock/memstore.go`, `mock/fakes.go`: `DeleteIfStale`.
- `mcp/remember.go`: the fall-through. `mcp/recall.go`: the quiet skip.
- No change to the domain package, `cmd/`, `inference/`, the schema or dependencies. No file under `eval/` or `cmd/eval/` changes.
- Eval: no figure is expected to move. Inside the eval one goroutine drives the store, so the re-check is always true. `go run ./cmd/eval --ci` and a comparison of its full output with `main`'s are run, and any difference stops the work.
