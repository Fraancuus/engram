# Sweep On Start Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task, with superpowers:test-driven-development inside each task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `engramd` runs its first decay sweep one minute after start instead of one hour, then one a day, so hard-prune runs in ordinary sessions.

**Architecture:** `sweep.Sweeper.Run` gains a `firstDelay` argument: one pass when it elapses, then one per interval. `engramd` sets the interval to 24 hours. Each pass still reads `now` from the injected `engram.Clock`; only the schedule uses Go timers, and its tests run on virtual time with `testing/synctest`. `SweepOnce` stops logging an error for a row that is already gone. Integration fixtures stop writing rows a real pass would delete.

**Tech Stack:** Go 1.26, standard library only (`time`, `testing/synctest`, `log/slog`). No new dependency.

**Spec:** `openspec/changes/sweep-on-start/design.md` and the delta specs in `openspec/changes/sweep-on-start/specs/` (`sweep-schedule`, `memory-stats`, `agent-acceptance`). Tasks: `openspec/changes/sweep-on-start/tasks.md`.

## Global Constraints

- Branch `feat/sweep-on-start`. Confirm with `git branch --show-current` before the first edit. Stage files by name. Never push, rebase, amend or switch branch.
- No `time.Now` call in `sweep/sweep.go` or `sweep/sweep_test.go`. Pass time comes from `engram.Clock`. (`sweep/sweep_integration_test.go:44` already has one and is not touched.)
- `sweep.New` keeps its signature. Nothing under `eval/`, `cmd/eval/`, `mock/`, `schema/` or the root package is edited, and no production file under `neo4j/` or `inference/`. If a step seems to need that, stop and report.
- `SweepOnce` returns the same count and the same error as today for every input.
- First delay in `cmd/engramd`: exactly `time.Minute`. Interval in `cmd/engramd`: exactly `24 * time.Hour` (it is `time.Hour` today, and changes in Task 5, not before). The `time.Hour` default inside `sweep.New` and the `time.Hour` the eval passes are not touched.
- Inside a `synctest.Test` bubble do not call `t.Run`, `t.Parallel` or `t.Deadline` (`t.Parallel` panics there). Put `t.Run` outside the bubble and open one bubble per case. Every goroutine started in a bubble must have returned before the bubble function returns.
- RED means an assertion fails against the old behaviour, not a compile error. Task 1 stubs the signature first for that reason.
- Every test passes under `go test -race`. Memory on the dev machine is short: run only the packages a step names, one `go test` at a time, and never `./eval` except where a step says so.
- After any import edit run `gofmt -l` on the file; it must print nothing. Edit Go files with the Edit tool, not a script that may rewrite line endings.
- Run `graphify update .` after each task that changes Go code.
- Commit messages end with a blank line and `Co-Authored-By:` for the model that wrote the commit.
- Do not edit `plans/m6-stats-demo/`, `docs/engram-m4-plan.md` or anything under `openspec/changes/archive/`.

## Review Focus

Conditions the specs imply that are most likely to bite, each pinned by a test in the task that owns the code:

1. A first delay of zero must sweep at once, without waiting for schedule time. Task 1, `TestRunZeroFirstDelaySweepsAtStart`.
2. `ctx` cancelled while `Run` is still inside the first delay must return without a pass. Task 1, `TestRunCancelledDuringFirstDelay`.
3. A pass must take its time from the injected clock even though a timer woke it. Task 1, `TestRunPassTimeFromInjectedClock`.
4. A row deleted between the candidate read and the delete must not be reported as an error or counted. Task 2, `TestSweepOnceAlreadyDeletedIsNotAnError`.
5. The real binary may or may not have completed a pass when `memory_stats` is called: the test must accept both. Task 5, the `sweep` shape check in `TestStdioAllTools`.

---

### Task 1: `Run` takes a first delay

**Files:**
- Modify: `sweep/sweep.go` (`Run`, lines 86-99, and the package comment on lines 1-2)
- Modify: `cmd/engramd/main.go` (the `sweeper.Run(ctx)` call, line 81)
- Test: `sweep/sweep_test.go`

**Interfaces:**
- Consumes: `New(p Pruner, d engram.DecayModel, c engram.Clock, interval, grace time.Duration, hardFloor float64, batch int, log *slog.Logger) *Sweeper` (unchanged), `(*Sweeper).Stats() engram.SweepStats`, and the test helpers already in `sweep/sweep_test.go`: `fixedClock`, `stepClock`, `statsT0`, `mock.FakeStore{PruneCands}`, `mock.FakeDecay{R}`.
- Produces: `func (s *Sweeper) Run(ctx context.Context, firstDelay time.Duration)`. Task 5 calls it with `sweepFirstDelay`.

- [ ] **Step 1: Stub the signature, behaviour unchanged**

In `sweep/sweep.go` change the signature to `func (s *Sweeper) Run(ctx context.Context, firstDelay time.Duration)` and leave the body as it is; the argument is unused until Step 4, and neither `go vet` nor the linter objects. In `cmd/engramd/main.go` change the call to `sweeper.Run(ctx, sweepInterval)`.

Run: `go build ./... && go test -race ./sweep/ ./cmd/engramd/`
Expected: both `ok`.

- [ ] **Step 2: Write the failing schedule test**

Add to `sweep/sweep_test.go` (new import `testing/synctest`, placed after `testing`):

```go
// runSweeper builds a Sweeper whose every pass prunes one candidate at retrievability r, so
// Stats().TotalPruned counts completed passes and the test never touches the fake store from
// a second goroutine.
func runSweeper(c engram.Clock, r float64) *Sweeper {
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
	return New(p, mock.FakeDecay{R: r}, c, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// startRun runs s.Run inside the current synctest bubble. stop cancels it and waits for it to
// return, which the bubble requires before it can end.
func startRun(s *Sweeper, firstDelay time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx, firstDelay)
	}()
	return func() { cancel(); <-done }
}

func TestRunFirstPassAfterFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		defer stop()

		time.Sleep(time.Minute - time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 0 {
			t.Fatalf("one nanosecond before the first delay: %d passes, want 0", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("at the first delay: %d passes, want 1", got)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 2 {
			t.Fatalf("one interval after the first pass: %d passes, want 2", got)
		}
	})
}
```

- [ ] **Step 3: Run it and see the behaviour fail**

Run: `go test -race -run TestRunFirstPassAfterFirstDelay ./sweep/`
Expected: FAIL with `at the first delay: 0 passes, want 1` (the stub still waits a full interval).

- [ ] **Step 4: Implement the first delay**

Body of `Run`, as fixed by design.md D1: a `time.NewTimer(firstDelay)` selected against `ctx.Done()`; on fire, `s.tick(ctx)`; then the existing ticker loop, with the ticker created after the first pass. Stop the timer with `defer`. Update the doc comment to:

```go
// Run drives SweepOnce until ctx is cancelled: one pass once firstDelay has elapsed, then one
// per interval. A firstDelay of zero runs the first pass immediately. Each pass recovers from
// a panic so one bad sweep cannot take the process down.
```

Update the package comment so it no longer implies the first pass waits a tick: "…hard-prunes memories whose retrievability has fallen below a floor, first after a delay and then on a ticker, until context cancellation."

- [ ] **Step 5: Run it and see it pass**

Run: `go test -race -run TestRunFirstPassAfterFirstDelay ./sweep/`
Expected: PASS.

- [ ] **Step 6: Add the boundary and cancellation tests**

```go
func TestRunZeroFirstDelaySweepsAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, 0)
		defer stop()
		synctest.Wait() // no time.Sleep: the pass must not need schedule time to elapse
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("zero first delay: %d passes before any time elapsed, want 1", got)
		}
	})
}

func TestRunCancelledDuringFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		time.Sleep(30 * time.Second)
		stop() // returns only when Run has returned
		if got := s.Stats(); got != (engram.SweepStats{}) {
			t.Fatalf("Stats() = %+v after cancelling inside the first delay, want zero", got)
		}
	})
}

func TestRunCancelledBetweenPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()
		time.Sleep(30 * time.Minute)
		stop()
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("%d passes after cancelling between passes, want 1", got)
		}
	})
}
```

- [ ] **Step 7: Add the injected-clock tests**

`statsT0` is 2023-11-14. The bubble's own clock starts at 2000-01-01, so a pass that took its time from the schedule would record a year-2000 time.

```go
func TestRunPassTimeFromInjectedClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(&stepClock{t: statsT0}, 0)
		stop := startRun(s, time.Minute)
		defer stop()
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := s.Stats().LastRunAt; !got.Equal(statsT0) {
			t.Fatalf("LastRunAt = %v, want the injected clock's %v", got, statsT0)
		}
	})
}

func TestRunFirstPassFloorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    float64
		want int
	}{{"at floor", 0.02, 0}, {"just below", 0.0199, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := runSweeper(&stepClock{t: statsT0}, tc.r)
				stop := startRun(s, time.Minute)
				defer stop()
				time.Sleep(time.Minute)
				synctest.Wait()
				want := engram.SweepStats{LastRunAt: statsT0, LastPruned: tc.want, TotalPruned: tc.want}
				if got := s.Stats(); got != want {
					t.Fatalf("Stats() = %+v, want %+v", got, want)
				}
			})
		})
	}
}
```

- [ ] **Step 8: Run the package under the race detector**

Run: `go test -race -count=3 ./sweep/ && go test -race ./cmd/engramd/ && go build ./... && gofmt -l sweep/ && grep -n 'time\.Now()' sweep/sweep.go sweep/sweep_test.go`
Expected: both `ok`, build silent, `gofmt` and the grep print nothing.

- [ ] **Step 9: Commit**

```bash
graphify update .
git add sweep/sweep.go sweep/sweep_test.go cmd/engramd/main.go
git commit -m "feat(sweep): run the first pass after a first delay"
```

---

### Task 2: A row that is already gone is not an error

**Files:**
- Modify: `sweep/sweep.go` (`SweepOnce`, the delete branch on lines 76-79; add `errors` to the imports of `sweep.go`)
- Test: `sweep/sweep_test.go`

**Interfaces:**
- Consumes: `engram.ErrNotFound` (`engram.go:158`), `mock.FakeStore{PruneCands, DeleteErr, Deleted}`.
- Produces: no signature change. `SweepOnce(ctx, now) (pruned int, err error)` returns what it returned before.

- [ ] **Step 1: Write the failing test and its guard**

New imports in `sweep/sweep_test.go`: `bytes`, `fmt`, `strings`.

```go
func TestSweepOnceAlreadyDeletedIsNotAnError(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	p := &mock.FakeStore{
		PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}},
		DeleteErr:  fmt.Errorf("delete: %w", engram.ErrNotFound), // wrapped, as neo4j.Store returns it
	}
	s := New(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(&logs, nil)))

	n, err := s.SweepOnce(context.Background(), statsT0)
	if n != 0 || err != nil {
		t.Fatalf("SweepOnce = (%d, %v), want (0, nil)", n, err)
	}
	if len(p.Deleted) != 2 {
		t.Errorf("attempted %d deletes, want 2: the pass must continue past a missing row", len(p.Deleted))
	}
	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing for rows that were already gone", logs.String())
	}

	s.tick(context.Background())
	want := engram.SweepStats{LastRunAt: statsT0, LastPruned: 0, TotalPruned: 0}
	if got := s.Stats(); got != want {
		t.Errorf("Stats() = %+v, want %+v", got, want)
	}
}

func TestSweepOnceOtherDeleteErrorStillLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}, DeleteErr: errors.New("boom")}
	s := New(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(&logs, nil)))

	n, err := s.SweepOnce(context.Background(), statsT0)
	if n != 0 || err != nil {
		t.Fatalf("SweepOnce = (%d, %v), want (0, nil)", n, err)
	}
	if got := strings.Count(logs.String(), "level=ERROR"); got != 1 {
		t.Errorf("%d error-level records, want 1; log: %q", got, logs.String())
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -race -run 'TestSweepOnce(AlreadyDeleted|OtherDeleteError)' ./sweep/`
Expected: `TestSweepOnceAlreadyDeletedIsNotAnError` FAILS on the `logged "…level=ERROR msg=\"sweep: delete failed\"…"` assertion. `TestSweepOnceOtherDeleteErrorStillLogged` PASSES already; it is the guard that the fix does not swallow other errors.

- [ ] **Step 3: Implement the branch**

In the delete branch of `SweepOnce`, before the existing `s.log.Error`, skip with `continue` when `errors.Is(err, engram.ErrNotFound)`, with the one-line comment `// deleted since the candidate read`. Do not increment `pruned`. Add one sentence to the `SweepOnce` doc comment: "A candidate that is already gone is skipped without an error."

- [ ] **Step 4: Run the package**

Run: `go test -race ./sweep/ && gofmt -l sweep/`
Expected: `ok`, and `gofmt` prints nothing.

- [ ] **Step 5: Eval check — a gate, not a formality**

Run: `go run ./cmd/eval --ci` and `git diff origin/main -- eval/ cmd/eval/`
Expected: the gate passes, the A/B/C table equals `eval/baseline.json` figure for figure, and the diff is empty.
**If any figure differs or the gate fails: stop. Do not start Task 3. Report the full table to the orchestrator, who reports to Franco.** Paste the table into the task report either way.

- [ ] **Step 6: Commit**

```bash
graphify update .
git add sweep/sweep.go sweep/sweep_test.go
git commit -m "fix(sweep): skip a row that is already gone"
```

---

### Task 3: `memory_stats` description

**Files:**
- Modify: `mcp/server.go` (the `memory_stats` `Description` string, line 149)
- Test: `mcp/stats_test.go` (the helper `statsOverTransport`, line 412; its `for _, tl := range lt.Tools` loop, lines 421-427)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing later tasks use. No field, schema or handler changes.

- [ ] **Step 1: Write the failing assertion**

Inside the loop in `statsOverTransport`, next to the `ReadOnlyHint` check (`strings` is already imported):

```go
if tl.Name == "memory_stats" {
	if !strings.Contains(tl.Description, "this server process's passes") {
		t.Errorf("memory_stats description does not say whose passes the sweep stats cover: %q", tl.Description)
	}
	if strings.Contains(tl.Description, "hourly") {
		t.Errorf("memory_stats description still names the hourly wait: %q", tl.Description)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test -race -run TestMemoryStatsOverTransport ./mcp/`
Expected: FAIL on both new assertions, from each subtest that goes through the helper.

- [ ] **Step 3: Change the sentence**

In the description, replace exactly
`Sweep stats are store-wide since process start, and read null and 0 until the first hourly sweep.`
with
`Sweep stats ignore namespace and cover only this server process's passes; null and 0 mean it has not swept yet.`
Change nothing else in the string; the sentence after it (`An unknown namespace returns empty counts, not an error.`) stays. If the old sentence is not there verbatim, stop and report what is.

- [ ] **Step 4: Run the package**

Run: `go test -race ./mcp/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
graphify update .
git add mcp/server.go mcp/stats_test.go
git commit -m "docs(mcp): say whose passes the sweep stats cover"
```

---

### Task 4: Integration fixtures a real pass cannot delete

**Files (integration-tagged tests only, no production code):**
- Modify: `neo4j/decay_integration_test.go` (lines 98, 133, 178)
- Modify: `mcp/m1_integration_test.go` (line 50, in `liveHandlers`)
- Modify: `mcp/m4_integration_test.go` (line 26)

**Interfaces:**
- Consumes: `clockAt` (already defined in `mcp/m4_integration_test.go:13`).
- Produces: nothing later tasks use.

**Precondition:** the stack is up (`docker compose ps` shows `neo4j`, `tei`, `tei-rerank` healthy). Run from Git Bash. A `SKIP` naming `unavailable` proves nothing. `TestStoreSearch` in `./neo4j` fails on the dev volume today for an unrelated reason (design.md, Risks); it must fail or pass the same way before and after this task.

- [ ] **Step 1: Record the baseline**

Run, one at a time: `go test -race -p 1 -tags integration -v ./neo4j/` then `go test -race -p 1 -tags integration -v ./mcp/`. Note the pass, fail and skip counts of each.

- [ ] **Step 2: Make the edits**

```
neo4j/decay_integration_test.go (three places)
-	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
+	old := time.Now().UTC().Add(-48 * time.Hour)

mcp/m1_integration_test.go:50
-		clock:            fixedClock{},
+		clock:            clockAt(time.Now().UTC()), // real "now": a 2023 clock makes rows sweep-eligible

mcp/m4_integration_test.go:26
-	t0 := (fixedClock{}).Now()
+	t0 := h.clock.Now()
```

Read each test you touch first and confirm it still proves the same thing: `TestStorePruneCandidates` uses its own 24-hour cutoff, the two propagation tests need only an `old` well before now, the `mcp` tests need only one shared instant. If one does not, stop and report which.

- [ ] **Step 3: Run both packages again**

Same two commands as Step 1.
Expected: the same pass, fail and skip counts as the baseline.

- [ ] **Step 4: Check the rows are not sweep candidates**

Read-only, with the unique namespace suffix your Step 3 run produced (the tests print or embed a `UnixNano` suffix; take the lower bound from the time you started Step 3):

```
MATCH (m:Memory)
WITH m, toInteger(last(split(m.namespace, '-'))) AS ts
WHERE ts >= <your lower bound>
RETURN count(m) AS rows,
       sum(CASE WHEN coalesce(m.pinned,false) = false AND m.last_accessed < datetime() - duration({days: 30}) THEN 1 ELSE 0 END) AS sweepable
```

Run it with `docker compose exec -T neo4j cypher-shell`. Expected: `sweepable` is 0.

- [ ] **Step 5: Commit**

```bash
git add neo4j/decay_integration_test.go mcp/m1_integration_test.go mcp/m4_integration_test.go
git commit -m "test: integration fixtures a real sweep cannot delete"
```

---

### Task 5: `engramd` waits one minute, then sweeps once a day

**Files:**
- Modify: `cmd/engramd/main.go` (the sweep constants on lines 33-39, the `Run` call, the startup `slog.Info` line)
- Test: `cmd/engramd/stdio_integration_test.go` (build tag `integration`; the `checks` table on lines 178-187; `a.close(t)` on line 166)

**Interfaces:**
- Consumes: `(*Sweeper).Run(ctx context.Context, firstDelay time.Duration)` from Task 1.
- Produces: the startup log attributes `sweep_first_delay=1m0s` and `sweep_interval=24h0m0s`.

- [ ] **Step 1: Write the failing assertions in `TestStdioAllTools`**

Add `strings` to the imports (`time` is already there). Remove the row `{"sweep.last_run_at", path(t, stats, "sweep", "last_run_at"), nil}` (line 186) from the `checks` table and add, after the loop over `checks`:

```go
// The real binary may or may not have completed a sweep pass by now, so check the shape of
// the block, not that it is empty. mcp/stats_test.go pins the exact empty wire shape.
lastRun := path(t, stats, "sweep", "last_run_at")
lastPruned, okLP := path(t, stats, "sweep", "last_pruned").(float64)
totalPruned, okTP := path(t, stats, "sweep", "total_pruned").(float64)
if !okLP || !okTP || lastPruned < 0 || totalPruned < 0 {
	t.Errorf("sweep counts = %v, %v; want numbers zero or more",
		path(t, stats, "sweep", "last_pruned"), path(t, stats, "sweep", "total_pruned"))
}
switch v := lastRun.(type) {
case nil:
	if lastPruned != 0 || totalPruned != 0 {
		t.Errorf("sweep.last_run_at is null but counts are %v, %v", lastPruned, totalPruned)
	}
case string:
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		t.Errorf("sweep.last_run_at = %q, want RFC 3339: %v", v, err)
	}
default:
	t.Errorf("sweep.last_run_at is %T, want null or a string", lastRun)
}
```

Right after `a.close(t)` (process `a` has exited there, so its captured stderr is complete):

```go
for _, want := range []string{"sweep_first_delay=1m0s", "sweep_interval=24h0m0s"} {
	if !strings.Contains(a.stderr.String(), want) {
		t.Errorf("engramd startup log lacks %s:\n%s", want, a.stderr)
	}
}
```

- [ ] **Step 2: Run it against the stack**

Run from Git Bash, with the stack up:
`go test -race -tags integration -run TestStdioAllTools -v ./cmd/engramd/`
Expected: FAIL with `engramd startup log lacks sweep_first_delay=1m0s` and `engramd startup log lacks sweep_interval=24h0m0s`. A `SKIP` naming `unavailable` proves nothing: report it and stop.

- [ ] **Step 3: Implement**

In `cmd/engramd/main.go`: add `sweepFirstDelay = time.Minute` to the sweep `const` block with the comment `// first pass: early enough for an ordinary session, late enough that a process which only lists tools does no sweep work`; change `sweepInterval` from `time.Hour` to `24 * time.Hour` with the comment `// a row becomes prunable only after sweepGrace, so one pass a day is enough`; change the call to `sweeper.Run(ctx, sweepFirstDelay)`; add `"sweep_first_delay", sweepFirstDelay.String()` to the startup `slog.Info` attributes, before `"sweep_interval"`.

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race -tags integration -run TestStdioAllTools -v ./cmd/engramd/ && go test -race ./cmd/engramd/ && gofmt -l cmd/engramd/`
Expected: `--- PASS: TestStdioAllTools`, no `SKIP`, the untagged package `ok`, and `gofmt` prints nothing.

- [ ] **Step 5: Commit**

```bash
graphify update .
git add cmd/engramd/main.go cmd/engramd/stdio_integration_test.go
git commit -m "feat(engramd): sweep one minute after start, then daily"
```

---

### Task 6: Docs

**Files:**
- Modify: `docs/demo.md` (the paragraph starting `**The `sweep` section reads empty.**`, lines 87-90)
- Modify: `docs/engram-prd-v1.md` (lines 158 and 173)
- Modify: `README.md` (the `memory_stats` entry, line 177)
- Modify: `CONTRIBUTING.md` (the section on integration tests; add one line)

**Interfaces:** none.

- [ ] **Step 1: `docs/demo.md`**

Replace the paragraph with:

```markdown
**The `sweep` section fills in after a minute.** `sweep.last_run_at` is `null` and `last_pruned`
and `total_pruned` are 0 until the first sweep pass completes. A stdio client starts `engramd` for
its own session, and the first pass runs one minute after start, then once a day. A take shorter than
a minute shows the empty block; a longer one shows a timestamp and the counts of that pass. The
stats are kept in memory from process start, are not persisted, and are store-wide, not per
namespace.
```

- [ ] **Step 2: `docs/engram-prd-v1.md`**

Line 158: replace `A scheduled sweep (goroutine + `time.Ticker`, hourly) hard-prunes:` with `A scheduled sweep (a goroutine; first pass one minute after start, then daily) hard-prunes:`.
Line 173: replace `and empty until the first sweep an hour in.` with `and empty until the first sweep, one minute after start.`

- [ ] **Step 3: `README.md`**

Replace `The sweep figures are in memory since process start, so they read empty in a short session.` with `The sweep figures are in memory since process start and read empty until the first sweep, one minute after start.`

- [ ] **Step 4: `CONTRIBUTING.md`**

Where it describes running the integration tests, add: `Integration fixtures must be pinned, live procedural, or younger than 30 days: a running engramd sweeps the same store and hard-deletes anything older that has decayed.`

- [ ] **Step 5: Check nothing still states the old wait**

Run: `grep -rn -i -E "hourly|hour after start|an hour in|read empty in a short session" --exclude='*_test.go' README.md docs/demo.md docs/engram-prd-v1.md mcp/ cmd/ sweep/`
Expected: no output. Test files are excluded because Task 3's assertion names the word. If `hourly` still appears somewhere this plan does not name, report the line; do not reword it on your own.

- [ ] **Step 6: Commit**

```bash
git add docs/demo.md docs/engram-prd-v1.md README.md CONTRIBUTING.md
git commit -m "docs: the first sweep runs one minute after start, then daily"
```

---

### Task 7: Verify

**Files:** none modified. Results go to `openspec/changes/sweep-on-start/verify.md` in the verify phase.

- [ ] **Step 1: The CI gates**

Run, one at a time (memory is short):
`go build ./...`
`go test -race -coverprofile=cover.out ./...`
`golangci-lint run ./...`
Expected: build silent, nine packages `ok` (`./eval` takes five to six minutes), `0 issues`.

- [ ] **Step 2: Clear the integration precondition**

`TestStoreSearch` fails on the dev volume because of leftover rows from earlier test runs (design.md, Risks). The orchestrator clears them with Franco's agreement and records what was removed in `verify.md`. Do not delete rows on your own.

- [ ] **Step 3: Integration**

From Git Bash with the stack up and the schema applied:
`go test -race -tags integration -p 2 -v $(go list ./... | grep -v 'engram/eval$')`
Expected: every package `ok`, zero `--- FAIL`, and no `SKIP` line naming `unavailable`. Note the filter: `grep -v '/eval$'` would also drop `cmd/eval`.

- [ ] **Step 4: Eval gate on the final tree**

Run: `go run ./cmd/eval --ci`
Expected: passes, table equal to the baseline. Report the A/B/C table. Any difference: stop and report.

- [ ] **Step 5: Acceptance on the real binary**

Build `engramd`, start it with an MCP client that stays connected for at least 70 seconds, call `memory_stats`, and record the `sweep` block and the startup log line in `verify.md`. Expected: `sweep.last_run_at` is an RFC 3339 timestamp. This is the first real pass on the dev store: it deletes the backlog (design.md, Risks). Record how many rows it pruned. Franco agreed to this pass on 2026-11-15.

- [ ] **Step 6: Project agents**

`reviewer` (opus) on `git diff origin/main...feat/sweep-on-start -- '*.go'`, `scoping-ux` (opus) on the new `memory_stats` description. Resolve every blocking finding before `/opsx:verify`.
