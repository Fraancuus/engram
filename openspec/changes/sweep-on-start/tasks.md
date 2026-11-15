The domain package and `mock/` do not change: `engram.Clock`, `engram.ErrNotFound`, `mock.FakeStore`
and `mock.FakeDecay` already give the tests what they need. Every Go task runs under
`go test -race`. Schedule tests run inside `testing/synctest` bubbles: `t.Run`, `t.Parallel` and
`t.Deadline` are not allowed inside a bubble, so `t.Run` goes outside it, one bubble per case.

## 1. Adapter: sweep schedule (`sweep/`)

- [ ] 1.1 Give `Run` the `firstDelay` parameter as a stub that ignores it, and update its one caller in `cmd/engramd/main.go` to pass `sweepInterval` so behaviour is unchanged. Proof: `go build ./...` and `go test -race ./sweep/ ./cmd/engramd/` pass as before.
- [ ] 1.2 RED then GREEN: `Run` runs its first pass when the first delay has elapsed, then one per interval. Proof: `TestRunFirstPassAfterFirstDelay` in `sweep/sweep_test.go` (virtual time via `testing/synctest`: no pass one nanosecond before the delay, one at the delay, two an interval later) fails on the stub with `at the first delay: 0 passes, want 1` and passes after.
- [ ] 1.3 A first delay of zero runs the first pass immediately. Proof: `TestRunZeroFirstDelaySweepsAtStart`.
- [ ] 1.4 `Run` returns on cancellation in both waits. Proof: `TestRunCancelledDuringFirstDelay` (no pass, `Stats()` zero) and `TestRunCancelledBetweenPasses` (exactly one pass).
- [ ] 1.5 Each pass takes `now` from the injected clock, not from the schedule. Proof: `TestRunPassTimeFromInjectedClock` (clock fixed at T while schedule time advances: `LastRunAt` is T) and `TestRunFirstPassFloorBoundary` (R at the floor: `LastPruned` 0; just below: 1).

## 2. Adapter: a row that is already gone (`sweep/`)

- [ ] 2.1 RED then GREEN: `SweepOnce` skips `engram.ErrNotFound` from `Delete` without an error-level log, and still logs any other delete error. Proof: `TestSweepOnceAlreadyDeletedIsNotAnError` (two candidates, wrapped `ErrNotFound`: both deletes attempted, returns 0 and nil, nothing logged, `tick` records `LastRunAt` T and `LastPruned` 0) and `TestSweepOnceOtherDeleteErrorStillLogged` (one error-level record).
- [ ] 2.2 Eval check, then stop or go on: run `go run ./cmd/eval --ci` and `git diff origin/main -- eval/ cmd/eval/`. Proof: the gate passes, the A/B/C table equals the baseline, the diff is empty. If anything differs, stop and report to Franco before any other task.

## 3. Adapter: tool description (`mcp/`)

- [ ] 3.1 RED then GREEN: the `memory_stats` description says the sweep stats cover only this server process's passes and that null and 0 mean it has not swept yet. Proof: the tools-list loop in the helper `statsOverTransport` (`mcp/stats_test.go`) asserts the description contains "this server process's passes" and does not contain "hourly"; `go test -race ./mcp/ -run TestMemoryStatsOverTransport` fails before and passes after.

## 4. Adapters: integration fixtures a real pass cannot delete (`neo4j/`, `mcp/`)

- [ ] 4.1 `neo4j/decay_integration_test.go`: the three `old` values become 48 hours. Proof: `go test -race -p 1 -tags integration -v ./neo4j/` gives the same pass and fail set as before the edit, with no skip naming `unavailable`.
- [ ] 4.2 `mcp/m1_integration_test.go` and `mcp/m4_integration_test.go`: `liveHandlers` uses a clock fixed at the real start time, and the M4 test derives its `t0` from the handler's clock. Proof: `go test -race -p 1 -tags integration -v ./mcp/` passes with no skip naming `unavailable`, and a read-only count of the rows that run wrote finds none that is unpinned and idle more than 30 days.

## 5. Command: `engramd` (`cmd/engramd/`)

- [ ] 5.1 Add `sweepFirstDelay = time.Minute`, set `sweepInterval` to `24 * time.Hour`, pass the delay to `Run`, and add `sweep_first_delay` to the startup log line. Proof: `TestStdioAllTools` (integration tag) asserts the captured stderr of process `a` contains `sweep_first_delay=1m0s` and `sweep_interval=24h0m0s`.
- [ ] 5.2 `TestStdioAllTools` checks the shape of the `sweep` block and no longer asserts it is empty: `last_run_at` null or RFC 3339, counts zero or more, and counts 0 when `last_run_at` is null. Proof: `go test -race -tags integration -run TestStdioAllTools -v ./cmd/engramd/` passes with the stack up, with no skip naming `unavailable`.

## 6. Docs

- [ ] 6.1 `docs/demo.md`: replace the paragraph that says the `sweep` section reads empty because the first sweep is an hour after start.
- [ ] 6.2 `docs/engram-prd-v1.md` lines 158 and 173, and `README.md:177`: state the first pass one minute after start, then one a day. `CONTRIBUTING.md`: one line on integration fixtures (pinned, live procedural, or younger than 30 days).
- [ ] 6.3 Proof for 6.1 and 6.2: `grep -rn -i -E "hourly|hour after start|an hour in|read empty in a short session" --exclude='*_test.go' README.md docs/demo.md docs/engram-prd-v1.md mcp/ cmd/ sweep/` prints nothing.

## 7. Verify

- [ ] 7.1 `go build ./...`, `go test -race -coverprofile=cover.out ./...`, `golangci-lint run ./...`, and `graphify update .`.
- [ ] 7.2 Precondition for integration: `TestStoreSearch` fails on the dev volume today because of leftover test rows (design.md, Risks). Clear them with Franco's agreement first, and say so in `verify.md`.
- [ ] 7.3 Integration with the stack up and the schema applied: `go test -race -tags integration -p 2 -v` on every package except the body of `./eval`. No skip may name `unavailable`.
- [ ] 7.4 `go run ./cmd/eval --ci` once more on the final tree; report the A/B/C table.
- [ ] 7.5 Acceptance on the real binary: run `engramd` for more than one minute, call `memory_stats`, and record in `verify.md` that `sweep.last_run_at` is a timestamp, with the pruned counts. This is the first real pass on the dev store: it deletes the backlog. Franco agreed to it on 2026-11-15.
- [ ] 7.6 Project agents: `reviewer` on the Go diff, `scoping-ux` on the tool description. Resolve every blocking finding.
