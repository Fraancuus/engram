## Why

`Sweeper.Run` waits one full tick before its first pass, and `engramd` sets the tick to one hour. An agent host starts `engramd` for one session and stops it when the session ends, usually inside the hour, so the hard-prune that M4 shipped rarely runs in real use. The store keeps rows the decay model has already written off, and `memory_stats` shows an empty `sweep` block in every short session. A first pass one minute after start makes the sweep run in ordinary sessions.

## What Changes

**Milestone.** No new milestone. This corrects the scheduled sweep of M4 (PRD section 9, "M4 — Type-aware decay") as post-M6 cleanup.

**First pass of the sweep**
- From: `Run(ctx)` runs its first pass one interval after start (one hour in `engramd`).
- To: `Run(ctx, firstDelay)` runs its first pass when `firstDelay` has elapsed, then one pass per interval. `engramd` passes one minute. Zero means a pass at start.
- Reason: sessions end before the hour.
- Impact: non-breaking for agents. `Run` has one caller. `sweep.New` and `SweepOnce` keep their signatures.

**A row that is gone by the time a pass reaches it**
- From: `SweepOnce` logs `sweep: delete failed` at error level when `Delete` returns `engram.ErrNotFound`.
- To: it skips the row: not counted, no error logged.
- Reason: with real sweeps, another process's pass or a hard `forget` can remove a row between the candidate read and the delete.
- Impact: non-breaking. `SweepOnce` returns the same count and error as before for every input.

**What `memory_stats` says about the sweep**
- From: the tool description says "Sweep stats are store-wide since process start, and read null and 0 until the first hourly sweep."
- To: "Sweep stats span all namespaces but only this server process's passes; null and 0 say nothing about the store."
- Reason: the old sentence becomes false, and it never said the stats are per process.
- Impact: the stats fields and their meaning do not change. `sweep.last_run_at` is still null until a pass completes.

**Integration fixtures a real pass would delete**
- From: three `neo4j` integration tests write rows 100 days old, and the `mcp` integration tests write rows with a clock fixed in 2023. A real pass deletes those rows while the tests are reading them back.
- To: 48 hours, and a clock fixed at the real start time. Five lines in three integration test files; no production code.
- Reason: after this change a developer's own session sweeps the store the suite uses.
- Impact: tests only. Each test still proves what it did.

**Tests and docs that assume no early pass**
- `TestStdioAllTools` stops asserting `sweep.last_run_at` is null on the real binary and checks the shape of the block.
- `docs/demo.md`, `docs/engram-prd-v1.md` and `README.md` stop saying the first sweep is an hour in. `CONTRIBUTING.md` gains one line on integration fixtures.

**Consequence to accept.** Hard-prune now runs in ordinary sessions for the first time. The first pass permanently deletes the backlog: unpinned memories idle more than 30 days whose retrievability is below 0.02, in every namespace, up to 500 candidates per pass. Soft-forgotten and superseded rows are deleted by the same rule, and after that `include_forgotten` cannot bring them back. On the dev store on 2026-11-15 that is 163 of 164 candidates, all in namespaces the integration tests create. The pass happens one minute into the first session started with a rebuilt `engramd`, not at merge. Reverting restores no row.

**Out of scope**
- A flag or environment variable for the first delay or the interval.
- Persisting the time of the last sweep, or coordinating passes between processes. Franco wants at most one pass a day for the whole store; that is the next change, `sweep-daily-claim`.
- Per-namespace sweep behaviour (v2).
- Ordering or paging `PruneCandidates`. Today it has no `ORDER BY` and a limit of 500, so with more than 500 idle rows above the floor, rows below the floor can go unexamined. That predates this change and is listed for Franco as a follow-up.
- Stopping a pass early when its context is cancelled.
- Changing the grace period, the floor, the batch size or the decay model.
- A sweep at shutdown.
- Making `TestStoreSearch` robust. It broke on the dev volume for a reason unrelated to the sweep and was cleared by removing leftover test rows (see design.md, Risks).

## Capabilities

### New Capabilities
- `sweep-schedule`: when the decay sweep runs its passes (first delay, then the interval), where each pass gets its time, and what a pass does with a row that is already gone.

### Modified Capabilities
- `memory-stats`: "Sweep stats" gains the tool-description wording and a scenario for the real binary, where a pass may or may not have completed. "Sweep stats" and "The eval does not move" now say what must not change about `SweepOnce`: the count and error it returns.
- `agent-acceptance`: the "Demo script" requirement no longer requires `docs/demo.md` to say sweep stats read empty in a short session; it requires the doc to say when they are populated.

## Impact

- `sweep/sweep.go`: `Run` gains `firstDelay`; `SweepOnce` gains the `ErrNotFound` branch. `sweep/sweep_test.go`: schedule tests on virtual time (`testing/synctest`, standard library since Go 1.25; `go.mod` says 1.26).
- `cmd/engramd/main.go`: `sweepFirstDelay`, the `Run` call, the startup log line. `cmd/engramd/stdio_integration_test.go`: the `sweep` assertion and the startup-log assertion.
- `mcp/server.go`: the `memory_stats` description string. No field, schema or handler change.
- `neo4j/decay_integration_test.go`, `mcp/m1_integration_test.go`, `mcp/m4_integration_test.go`: fixture timestamps.
- `docs/demo.md`, `docs/engram-prd-v1.md`, `README.md`, `CONTRIBUTING.md`.
- No change to the domain package, `mock/`, the `neo4j` adapter code, `inference/`, the schema or dependencies.
- Eval: no change expected. The eval calls `sweep.New` and `SweepOnce` with virtual time and never calls `Run` (`eval/arm.go:215-217`). `go run ./cmd/eval --ci` is run as a check, and any difference from the baseline stops the work.
