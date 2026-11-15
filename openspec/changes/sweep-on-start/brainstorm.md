## Design Summary

**What Franco asked for.** `Sweeper.Run` (`sweep/sweep.go:88`) waits one full tick before its
first `SweepOnce`, and `cmd/engramd` sets the tick to one hour (`cmd/engramd/main.go:35`). An
agent host starts `engramd` for one session and stops it when the session ends, usually well
inside the hour. So the hard-prune that M4 shipped rarely runs in real use. The change makes a
first pass happen early in the life of the process.

**What the code does today.**

- `Run` creates a `time.Ticker` at the interval and calls `tick` on each tick. Nothing runs
  before the first tick. `Run` has one caller, `cmd/engramd/main.go:81`. No test calls it.
- `tick` reads `now` from the injected `engram.Clock`, calls `SweepOnce(ctx, now)`, and records
  the pass in memory. A failed or panicking pass is logged and not recorded.
- `SweepOnce` is store-wide. It takes up to 500 unpinned memories whose `last_accessed` is more
  than 30 days old (`neo4j/store.go:432`, no `ORDER BY`), and hard-deletes those whose
  retrievability is below 0.02.
- `memory_stats` reports the last pass. `sweep.last_run_at` is JSON null until a pass completes.
- The eval builds its own `Sweeper` and calls only `SweepOnce` with virtual time
  (`eval/arm.go:215-217`). It never calls `Run`.

**The design.** `Run` takes a first delay: `Run(ctx, firstDelay)`. It runs one pass when the
first delay has elapsed, then one per interval as today. `cmd/engramd` passes one minute and
sets the interval to 24 hours. Each pass still reads its `now` from the injected clock. The schedule itself stays on Go timers, and
its tests run on virtual time with `testing/synctest`. `New` keeps its signature, so the eval's
call site does not change.

Three things follow from sweeping for real, and the change covers them:

- **Integration fixtures a real pass would delete.** Two groups of integration tests write rows
  a real pass removes, then read them back: three tests in `neo4j/decay_integration_test.go`
  (rows 100 days old), and every `mcp` integration test built on `liveHandlers`, which writes
  with a clock fixed in 2023. A five-line edit to those test files makes the rows fresh, so no
  real pass touches them. Probed against the live stack: the same tests pass before and after,
  and none of the rows written after the edit is a sweep candidate.
- **A row that is gone by the time a pass reaches it.** `Delete` returns `engram.ErrNotFound`,
  which `SweepOnce` logs today as `sweep: delete failed` at error level. That happens when
  another pass got there first, or when the agent hard-forgot the row in between. `SweepOnce`
  will skip it quietly.
- **The stdio test's empty-block assertion.** `TestStdioAllTools` asserts `sweep.last_run_at` is
  null on the real binary (`cmd/engramd/stdio_integration_test.go:186`). That holds only while
  no pass has run. It becomes a shape check: null, or an RFC 3339 timestamp.

**What sweeping for real means.** This change is the first time hard-prune runs in ordinary
sessions. The first pass after it ships deletes the backlog, permanently. On the dev store
(read-only count, 2026-11-15): 301 rows are candidates, and 290 of them would be deleted (183
episodic, 107 semantic). Eleven survive: 6 live procedural rows, which never decay, and 5
semantic rows still above the floor. All 301 sit in namespaces the integration tests create
(`itest-*`, `m1-*` to `m4-*`, `sweep-*`). No row outside a test namespace is a candidate today.

## Alternatives Considered

### Alternative A: Sweep when `Run` starts

- **Approach**: `Run` calls `tick` once before it enters the ticker loop.
- **Pros**:
  - One line, no new parameter.
  - Every process prunes, including a one-shot `claude -p` run that lasts seconds.
  - The stdio integration test could wait for the first pass and assert a populated block, which
    would prove the `WithSweepStats` wiring end to end.
- **Cons**:
  - The integration suite's own `engramd` processes (two in `TestStdioAllTools`) sweep the shared
    store while other packages' tests run. That is safe only as long as no integration fixture
    is ever more than 30 days old and decayed. The fixture edit above makes it safe today; every
    future fixture has to keep to the rule, and the suite itself would punish a slip at random.
  - A host that starts the server only to list its tools pays for a pass. A process that exits
    mid-pass logs one error per remaining candidate.
  - The pass competes with the session's first tool calls for Neo4j.
- **Why not chosen**: it makes the integration suite depend on a fixture rule that nothing
  enforces, for the sake of sessions shorter than a minute.

### Alternative B: First pass after a short first delay (chosen)

- **Approach**: `Run(ctx, firstDelay)` waits `firstDelay`, runs a pass, then ticks at the
  interval. `cmd/engramd` passes one minute.
- **Pros**:
  - Processes that live for seconds do nothing: tool-listing probes, and the `engramd` processes
    the integration suite starts (the whole test took 5.4 s in the last run).
  - No sub-minute process starts a pass it cannot finish, and startup does not compete with the
    first tool calls.
  - The cadence is set apart from the first pass, so it can be chosen on its own.
  - Alternative A is this alternative with a delay of zero, so the choice is one constant.
- **Cons**:
  - A session shorter than the delay never prunes.
  - No automated test sees a populated `sweep` block through the real binary, because that
    would mean waiting out the delay.
  - One more argument on `Run`, and about a hundred lines of schedule tests.
- **Why chosen**: it fixes exactly what was reported (the first pass waits a full tick)
  without tying the first pass to the cadence.

### Alternative C: Sweep at start, rate-limited by a persisted last-sweep time

- **Approach**: store the time of the last pass in Neo4j. A starting process sweeps only if the
  last pass is older than the interval.
- **Pros**: many short sessions cost one pass per hour in total, however they overlap.
- **Cons**: a new node in the schema, a new method on the store port, a read-then-write race
  between processes, and persisted state for a stats block that is in-memory by decision (M6).
- **Why not chosen**: the cost it removes is small (see below), and it is far larger than the
  problem.

### Alternative D: Shorten the interval, change no code

- **Approach**: set `sweepInterval` to ten minutes. `Run` is untouched: the first pass comes
  after ten minutes and every ten minutes after.
- **Pros**:
  - One constant. No new parameter, no schedule tests, no new spec capability.
  - Rows become prunable only after 30 idle days, so a pass in most working sessions is enough.
  - The integration suite's short-lived processes still never sweep.
- **Cons**:
  - It ties the first pass to the cadence. Ten minutes still misses sessions under ten minutes,
    and getting the first pass down to a minute would mean a store scan every minute for the
    life of every process.
  - A long session sweeps six times as often. Each pass is cheap, but each is also another
    moment at which two processes can overlap.
- **Why not chosen**: it is the cheapest fix and a legitimate one. It loses on precision: the
  reported problem is the wait before the first pass, and this changes the cadence to move it.
  Franco chose Alternative B on 2026-11-15.

### Also dropped

- **Sweep at shutdown.** One pass per session whatever its length, but the host may kill the
  process before the pass ends, `ctx` is already cancelled, and no one can read the stats.
- **Random jitter on the first delay.** It spreads simultaneous starts, but it puts randomness
  in the schedule, and overlapping passes are already safe.

## Agreed Approach

Alternative B, with a first delay of one minute and an interval of 24 hours in `cmd/engramd`.
Franco chose it in the plan review on 2026-11-15: the first pass when the server starts, then
one a day. He also confirmed the `ErrNotFound` handling, the fixture edits, and the first real
pass on the dev store.

**Daily, not hourly.** A row becomes prunable only after 30 idle days, so a pass an hour after
the first finds nothing new. One pass a day is enough, and it holds per process: every `engramd`
process sweeps once, a minute after it starts.

**Sweep at start versus a first delay.** Both fix the reported problem. With the fixtures made
fresh, a pass at start no longer breaks today's integration suite, so the choice rests on three
smaller things. A delay costs nothing for processes that never do real work, it never starts a
pass that a sub-minute process will abandon, and it keeps the suite's own processes from
sweeping the store other tests are using. A pass at start prunes in sessions under a minute and
lets one integration test prove the wiring. The delay is recommended; zero is one constant away.

**Cost when many short sessions start.** One pass is one query that scans `:Memory` for rows
idle more than 30 days (no index on `last_accessed`, limit 500), a retrievability computation
per candidate in Go, and one delete per row below the floor. On a personal store this is
milliseconds when there is no backlog, and at most 500 deletes when there is. With a one-minute
delay a process that lives for seconds costs nothing. A day of 50 real sessions costs 50 scans.
Sessions that start in the same second sweep in the same second, and that is safe: a probe ran
two simultaneous deletes of one row against the live Neo4j, 200 rounds through one driver and
200 through two. Every delete returned success, no round returned an error, and no row was left.

**The clock stays injected.** There are two kinds of time here. Decay time is the `now` given to
`SweepOnce`, and it still comes from `engram.Clock` in `tick`. Schedule time is when the
goroutine wakes, and it is a Go timer today and stays one. The schedule is tested on virtual
time with `testing/synctest`, so no test sleeps and the `Clock` port does not grow a timer
method. `sweep/sweep.go` gains no `time.Now` call.

**The eval does not move.** It calls `sweep.New` and `SweepOnce` only, with virtual time. `New`
keeps its signature. Inside `SweepOnce` the one change is that a delete returning `ErrNotFound`
is no longer logged. It was already skipped and not counted, so the count and the error that
`SweepOnce` returns are the same for every input. The eval's logger discards its output.

## Key Decisions

1. `Run(ctx, firstDelay)`: the delay is an argument to `Run`, not a field set in `New`. `Run`
   has one caller, and `New` has three (`cmd/engramd`, the eval, the tests).
2. A first delay of zero runs the first pass immediately.
3. `cmd/engramd` uses `sweepFirstDelay = time.Minute` and `sweepInterval = 24 * time.Hour`,
   both constants. No flag and no environment variable.
4. Scheduling uses `time.NewTimer` and `time.NewTicker`. `tick` keeps reading `s.clock.Now()`.
5. Schedule tests use `testing/synctest`. The plan's tests were dry-run in a scratch copy:
   they fail on the old behaviour with the predicted message, pass after, and stay green over
   three `-race` runs.
6. `SweepOnce` skips `engram.ErrNotFound` from `Delete` without an error-level log. The row is
   not counted.
7. Integration fixtures become sweep-proof with a test-only edit: 48 hours instead of 100 days
   in three `neo4j` tests, and a clock fixed at the real start time instead of 2023 in the `mcp`
   integration helper.
8. The stats contract does not change: `last_run_at` is null until a pass completes. The
   `memory_stats` description says whose passes the stats cover: "Sweep stats ignore namespace
   and cover only this server process's passes; null and 0 mean it has not swept yet."
9. `TestStdioAllTools` checks the shape of the `sweep` block, not that it is empty. The exact
   null-and-zero wire shape stays pinned by `mcp/stats_test.go`, which injects the stats.
10. Docs that state the one-hour wait are corrected: `docs/demo.md`, `docs/engram-prd-v1.md`
    lines 158 and 173, `README.md`. `plans/m6-stats-demo/`, `docs/engram-m4-plan.md` and
    archived changes are records and stay as they are.

## Open Questions

Answered by Franco on 2026-11-15: the first pass runs one minute after the server starts and
one pass a day follows; the `ErrNotFound` handling and the fixture edits are in this change; the
first real pass may run on the dev store.

Still open:

1. **Is one pass per `engramd` process enough?** Recommended: yes, with the sweeper goroutine as
   the worker that repeats it daily. One pass a day for the whole store would need a persisted
   last-sweep time (Alternative C) or a worker outside `engramd`.
2. **May the leftover integration-test rows be removed?** `TestStoreSearch` fails on the dev
   volume until they are. Recommended: yes, before the integration task.
