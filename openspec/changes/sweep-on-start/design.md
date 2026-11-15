## Context

The decay sweep hard-prunes memories that the decay model has written off. `sweep.Sweeper.Run`
drives it:

```go
func (s *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}
```

`cmd/engramd` builds one `Sweeper` with `sweepInterval = time.Hour` and runs `Run` in a goroutine
next to the MCP server. A stdio host starts `engramd` per session, and most sessions end inside
the hour, so the first tick is rarely reached.

Facts the design rests on, each read in the code:

- `Run` has one caller, `cmd/engramd/main.go:81`. No test calls `Run`; the tests call `tick` and
  `SweepOnce` directly.
- `tick` (`sweep/sweep.go:101`) recovers from a panic, reads `now := s.clock.Now()`, calls
  `SweepOnce(ctx, now)`, and records the pass under a mutex. A pass that returns an error is
  logged and not recorded.
- `SweepOnce` (`sweep/sweep.go:66`) calls `PruneCandidates(ctx, now.Add(-grace), batch)` and
  deletes each unpinned candidate whose retrievability is below the hard floor.
- `PruneCandidates` (`neo4j/store.go:432`) is store-wide: every namespace, unpinned,
  `last_accessed` before the cutoff, `LIMIT $limit`, no `ORDER BY`.
- `Delete` (`neo4j/store.go:412`) returns `engram.ErrNotFound` when no memory has the id.
- `memory_stats` reports `sweep.last_run_at` as JSON null until a pass has completed
  (`mcp/stats.go:170-182`). Its description (`mcp/server.go:149`) says the values "read null and
  0 until the first hourly sweep".
- The eval calls `sweep.New(...)` and `sw.SweepOnce(ctx, now)` with virtual time, once per
  simulated day (`eval/arm.go:215-217`). Nothing under `eval/` or `cmd/eval/` calls `Run`.
- `engramd` defaults to type-aware decay (`cmd/engramd/main.go:97-101`): episodic stability
  2 days, semantic 60 days, live procedural rows never decay (`decay.go:20-24, 52-64`).

**Ports.** `sweep` consumes two domain ports from `engram.go`, `engram.DecayModel` and
`engram.Clock`, and its own consumer-side interface `sweep.Pruner`, which `*neo4j.Store` and the
`mock` stores implement. This change adds no package, no interface and no method to a port.

## Goals / Non-Goals

**Goals:**

- A session of ordinary length runs at least one sweep pass.
- A process that lives for seconds does no sweep work.
- The integration suite does not depend on whether a real sweep is running against the store.
- The clock stays injected: no `time.Now` call in `sweep/sweep.go`.
- The eval result does not change.

**Non-Goals:**

- Making the schedule configurable at run time.
- Coordinating passes between processes, or persisting when the last pass ran.
- Changing what a pass deletes: grace period, floor, batch size and decay model are untouched.
- Fixing the candidate cap (see Risks).

## Decisions

### D1. `Run` takes a first delay

```go
// Run drives SweepOnce until ctx is cancelled: one pass once firstDelay has elapsed, then one
// per interval. A firstDelay of zero runs the first pass immediately. Each pass recovers from
// a panic so one bad sweep cannot take the process down.
func (s *Sweeper) Run(ctx context.Context, firstDelay time.Duration) {
	first := time.NewTimer(firstDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
		s.tick(ctx)
	}

	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}
```

The interval ticker starts after the first pass, so the second pass is one interval after the
first pass ends.

Why an argument to `Run` and not a field set by `New`: the delay matters only to `Run`, `Run`
has one caller, and `New` already takes eight positional arguments and has three callers. One
of them is the eval, which this change must not touch.

### D2. `engramd` waits one minute

`cmd/engramd/main.go` gains `sweepFirstDelay = time.Minute` next to `sweepInterval`, passes it
to `Run`, and adds it to the startup log line as `sweep_first_delay`.

Three schedules were weighed. The table shows what each one does for the kinds of process that
exist.

| Process | At start (delay 0) | One minute, then hourly (recommended) | Every ten minutes, no code change |
|---|---|---|---|
| Tool-listing probe, lives under a second | Starts a pass and abandons it | Nothing | Nothing |
| One-shot `claude -p` run, seconds to a minute | Prunes | Usually nothing | Nothing |
| Integration suite's `engramd` (5.4 s measured) | Sweeps the shared store mid-suite | Nothing | Nothing |
| Working session, ten minutes or more | Prunes | Prunes | Prunes |
| Session of several hours | 1 pass, then hourly | 1 pass, then hourly | A pass every ten minutes |
| New code | 1 line | A parameter and its tests | None |

Why one minute and not zero:

1. **Processes that do no real work pay nothing.** A host that starts the server to list its
   tools does no sweep work, and no sub-minute process starts a pass it will abandon. A pass cut
   off by a cancelled context logs an error for each remaining candidate.
2. **The suite's own processes stay out of the shared store.** `TestStdioAllTools` starts two
   real `engramd` processes while other packages' integration tests use the same Neo4j. With D7
   the fixtures of today survive a real pass, but a pass at start would make the suite itself
   delete any future fixture that is old and decayed, at random. With a delay those processes
   exit before they sweep.
3. **Startup.** The pass does not compete with the session's first tool calls.

What the delay costs: a session shorter than a minute never prunes, and no integration test can
wait for a populated `sweep` block. Pruning is housekeeping with no deadline. A row only becomes
prunable after 30 idle days, so the next longer session is soon enough.

Why not ten minutes with no code change: it is the cheapest fix and it would work for long
sessions. It moves the first pass by changing the cadence, so the two cannot be set apart: ten
minutes still misses sessions under ten minutes, and a first pass at one minute would mean a
store scan every minute for the life of every process.

One minute is a judgement, not a measured optimum. It is long against the integration suite's
5.4 seconds and short against a working session. The choice is Open Question 1.

### D3. Decay time stays on the injected clock; the schedule stays on Go timers

Two kinds of time are involved:

- **Decay time** is the `now` passed to `SweepOnce`. `tick` reads it from `s.clock.Now()`, and
  that does not change. `sweep/sweep.go` contains no `time.Now` call before or after.
- **Schedule time** is when the goroutine wakes. It is a `time.Ticker` today and becomes a
  `time.Timer` followed by a `time.Ticker`.

The schedule is not moved onto `engram.Clock`. That would add a timer method to a domain port
that the decay math and the eval use only for `Now()`, to serve one goroutine.

Schedule tests run on virtual time with `testing/synctest` (standard library since Go 1.25;
`go.mod` says 1.26, and CI reads the Go version from `go.mod`). Inside `synctest.Test`, timers
use a fake clock that advances only when every goroutine in the bubble is blocked, so a test can
assert "no pass one nanosecond before the delay, one pass at the delay" without sleeping. The
fake clock moves schedule time only. Decay time in those tests still comes from the test's own
`engram.Clock`, which shows the two are separate.

The plan's schedule tests were dry-run in a scratch copy of `main`: the first one fails on the
old behaviour with `at the first delay: 0 passes, want 1`, all of them pass after D1, and three
consecutive `-race` runs are green. `go vet` and `golangci-lint` report nothing.

### D4. A row that is already gone is not an error

```go
if err := s.pruner.Delete(ctx, m.ID); err != nil {
	if errors.Is(err, engram.ErrNotFound) {
		continue // deleted since the candidate read
	}
	s.log.Error("sweep: delete failed", "id", m.ID, "err", err)
	continue
}
pruned++
```

A pass reads its candidates, then deletes them one by one. A row can vanish in between: another
process's pass got to it first, or the agent hard-forgot it. `Delete` then returns
`engram.ErrNotFound`, and today that is an error-level line per row in the host's MCP log.
Before this change two processes sweep the same rows only if both live past the hour. After it,
any two sessions that start close together do.

A probe against the live Neo4j shows what happens when two deletes of one row overlap exactly:
in 200 rounds through one driver and 200 through two, both callers got success, no call returned
an error, and no row was left behind. So overlapping passes are safe, and `ErrNotFound` is the
outcome only for a pass that arrives after the other has committed.

Two consequences, both accepted:

- The skipped row is not counted. `LastPruned` and `TotalPruned` stay per process.
- When two deletes overlap exactly, both processes count the row. The counts say what each
  process asked the store to delete; across processes they can add up to more than the rows
  removed.

`SweepOnce` returns the same count and error as before on every input. A delete that failed was
already skipped and not counted; the only difference is that this one error is no longer logged.
The `memory-stats` spec says `tick` records a pass "with `SweepOnce` unchanged", and its "The
eval does not move" requirement says that capability must not change `SweepOnce`. Both protect
the results of a pass, and the delta rewords both to say so: the count and the error.

### D5. The stats contract does not change; its description does

`sweep.last_run_at` is null until a pass completes, and the counts are 0. That is still true.
What changes is how soon a pass completes.

- The `memory_stats` description (`mcp/server.go:149`) replaces

  > Sweep stats are store-wide since process start, and read null and 0 until the first hourly sweep.

  with

  > Sweep stats ignore namespace and cover only this server process's passes; null and 0 mean it has not swept yet.

  The stats are per process, so every new session starts at null even if another process swept
  a minute earlier. The new sentence says that, and it names no duration, because the delay
  belongs to `cmd/engramd`, not to `mcp`.
- The `memory-stats` spec scenario "Before the first tick" keeps its name and text. Its WHEN
  ("no pass has completed") never depended on the schedule, so it is not racy, and OpenSpec does
  not let a MODIFIED block drop or rename a scenario. The delta adds two scenarios next to it:
  the real binary, where a pass may or may not have completed, and the description wording.

### D6. `TestStdioAllTools` checks shape, not emptiness

The test asserts `sweep.last_run_at == nil` on the real binary. With a one-minute delay it would
still pass, by a margin of timing and nothing else. It becomes:

- `last_run_at` is null or a string that parses as RFC 3339.
- `last_pruned` and `total_pruned` are numbers, zero or more.
- If `last_run_at` is null, both counts are 0.

It also asserts that the startup log names the first delay and the interval. The test already
captures each process's stderr.

The exact null-and-zero wire shape stays pinned where it is deterministic: `mcp/stats_test.go`
injects the stats function and asserts the JSON.

Every other test that expects empty stats either never starts `Run` (`TestStatsBeforeFirstTick`,
the `tick` tests, `sweep_integration_test.go`) or injects a stats function
(`mcp/stats_test.go:291-294, 473, 485-531`). They are unaffected.

### D7. Integration fixtures a real pass cannot delete

A real pass is store-wide, and the integration suite shares its Neo4j with whatever else is
attached to it. Two groups of tests write rows a real pass deletes, and then read them back:

| Tests | Rows they write | Under a real pass |
|---|---|---|
| `neo4j/decay_integration_test.go:98,133,178` | Episodic, `last_accessed` 100 days ago | Retrievability about e^-50. Deleted. |
| Every `mcp` integration test using `liveHandlers` (`mcp/m1_integration_test.go:50`) | Written with a clock fixed at 2023-11-14 | Semantic rows at about e^-18, episodic lower. Deleted. |

`sweep/sweep_integration_test.go` is not in this list. Its year-2000 rows are a pinned row and a
live procedural row, which a real pass keeps, and an episodic row the test itself expects gone.

Today nothing sweeps, so these tests pass. After this change a developer's own session sweeps a
minute after it starts, and a test that loses a row mid-run fails in a way that reads as a
recall regression. The fix is in the fixtures, and it is test-only:

```
neo4j/decay_integration_test.go (lines 98, 133, 178)
-	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
+	old := time.Now().UTC().Add(-48 * time.Hour)

mcp/m1_integration_test.go:50
-		clock:            fixedClock{},
+		clock:            clockAt(time.Now().UTC()), // real "now": a 2023 clock makes rows sweep-eligible

mcp/m4_integration_test.go:26
-	t0 := (fixedClock{}).Now()
+	t0 := h.clock.Now()
```

`clockAt` already exists in `mcp/m4_integration_test.go`. Each test still proves what it did:
`TestStorePruneCandidates` uses its own 24-hour cutoff, so a 48-hour row is still a candidate
for the test; the two propagation tests only need `old` to be well before now; the `mcp` tests
only need one shared instant.

Probed against the live stack in a scratch copy: `./mcp` passes 117 tests before and after.
`./neo4j` gives the same results before and after: 27 pass, and `TestStoreSearch` fails both
times for a reason that has nothing to do with the sweep (see Risks). Of the rows the edited
tests wrote, none is a sweep candidate; of the rows the unedited tests wrote, 32 are.

### D8. Docs

- `docs/demo.md:87-90` says the `sweep` section reads empty because the first sweep is an hour
  after start. New text: empty until the first pass, one minute after start; in memory per
  process; store-wide.
- `docs/engram-prd-v1.md:158` says the sweep is hourly: add the first pass. Line 173 says
  "empty until the first sweep an hour in": correct it.
- `README.md:177` says the sweep figures "read empty in a short session": correct it.
- `plans/m6-stats-demo/`, `docs/engram-m4-plan.md` (its `Run` sketch at line 259) and archived
  OpenSpec changes are records and are not edited.

### D9. The eval is unaffected, and that is checked

By construction: the eval calls `sweep.New` and `SweepOnce` only. `New` keeps its signature and
its defaults. `SweepOnce` returns the same count and error for every input (D4), and the eval
passes a logger that discards its output (`eval/arm.go:216`).

No eval constant, mix, weight, threshold or dataset size is touched, so there is nothing to
preregister. The prediction is that `go run ./cmd/eval --ci` passes with the A/B/C table equal
to the baseline. It is run once after the `sweep/` tasks. Any difference stops the work and goes
to Franco before anything else is changed.

### What a pass does to each kind of row

No branch changes. This is listed because the change makes these branches run in real use.

| Row | What a pass does |
|---|---|
| Pinned | Never a candidate. Kept. |
| Accessed in the last 30 days | Not a candidate. Kept. |
| Idle more than 30 days, retrievability at or above 0.02 | Examined, kept. |
| Idle more than 30 days, retrievability below 0.02 | Hard-deleted with its relationships. Not recoverable. |
| Procedural and not superseded, under type-aware decay | Retrievability stays 1. Kept. |
| Soft-forgotten | No special handling: deleted or kept by the rules above. Once deleted, `include_forgotten` cannot bring it back. |
| Superseded | Decays on the fast curve (stability 2 days), so it is deleted soon after the grace period. |
| In another namespace | Same rules. A pass is store-wide. |
| Gone by the time the pass reaches it | Skipped, not counted (D4). |

## Risks / Trade-offs

- **[The first real pass deletes the backlog.]** Hard-prune has effectively never run. Read-only
  count on the dev store, 2026-11-15: 301 candidates, of which 290 would be deleted (183
  episodic, 107 semantic) and 11 kept (6 live procedural, 5 semantic above the floor). All 301
  are in namespaces the integration tests create (`itest-*`, `m1-*` to `m4-*`, `sweep-*`); no row
  outside a test namespace is a candidate. Every integration run before D7 adds more rows of
  the same kind, so the count on the day will be higher. The pass runs in the first session
  started with a rebuilt `engramd`, one minute in, not at merge. → Franco confirms before apply. Anything that
  must survive is pinned, or the Neo4j volume is dumped, first. The PR body states the numbers.
- **[A session under a minute never prunes.]** → Accepted. See D2.
- **[No automated test sees a populated `sweep` block through the real binary.]** The unit tests
  cover the schedule, `tick`, `Stats` and the wire shape; the one untested link is
  `WithSweepStats(sweeper.Stats)` in `main.go`. → Verify runs `engramd` for more than a minute
  once, calls `memory_stats`, and records the output in `verify.md`.
- **[A future integration fixture is old and decayed.]** A developer's own session can delete it
  mid-test. → D7 fixes today's fixtures. The rule for new ones goes in `CONTRIBUTING.md` in one
  line: integration fixtures are pinned, live procedural, or younger than 30 days.
- **[`TestStoreSearch` fails on the dev volume today.]** It is unrelated to this change, but it
  blocks a green integration run. The `neo4j` and `sweep` integration tests write rows with the
  same one-hot embedding on every run and never remove them: 512 such rows on 2026-11-15, in
  `itest-*` and `sweep-*` namespaces. `Search` asks the vector index for the 50 nearest rows
  store-wide and filters by namespace afterwards. All 50 come back as exact ties, the test's new
  row is rarely among them, and the filter leaves nothing. It passed earlier the same day and
  has failed on every run since. → Before the integration task, the leftover rows in the
  integration-test namespaces are removed with Franco's agreement. Making those tests clean up
  after themselves, and the store-wide-then-filter search behind the failure, are listed for
  Franco as follow-ups.
- **[The candidate cap can starve the sweep.]** `PruneCandidates` returns at most 500 rows in no
  set order. If more than 500 rows are idle past the grace period and above the floor, rows
  below the floor may never be examined. This predates the change; today 301 rows are
  candidates. → Out of scope; listed for Franco as a follow-up.
- **[`testing/synctest` needs Go 1.25 or newer.]** → `go.mod` says 1.26 and CI reads the version
  from it.

## Migration Plan

No data migration and no schema change. Merging the PR changes nothing by itself: the first pass
happens one minute into the first session started with a rebuilt `engramd`. Rollback is
reverting the PR and rebuilding. It restores no row; rows a pass deleted are gone unless the
Neo4j volume was dumped first.

## Open Questions

1. When does the first pass run? Recommended: one minute after start, hourly after (D2). The
   others: at start, or every ten minutes with no code change.
2. Is D4 (the `ErrNotFound` branch) in this change? Recommended: yes.
3. Is D7 (the fixture edits) in this change? Recommended: yes.
4. Franco confirms the first real pass on his store, or pins rows or dumps the volume first.
