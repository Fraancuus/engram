# Verification Report

**Change**: `sweep-on-start`
**Verified at**: `2026-11-15 20:50`
**Iteration**: `1`
**Verifier**: session model (Claude Fable 5.2), with the project agents `reviewer`, `scoping-ux` and `security-auditor`

---

## Summary

| Dimension | Status |
|---|---|
| Completeness | 21 of 21 tasks. 7 of 7 added or modified requirements implemented (4 added, 3 modified). |
| Correctness | 7 of 7 requirements mapped to code. Of 23 scenarios: 16 are covered by automated tests, 1 by the acceptance run, 3 by the eval checks in this report, 1 by reading `docs/demo.md`. The other 2 are M6 obligations of `agent-acceptance` that this change did not run again. |
| Coherence | Design followed. Two wording deviations from plan.md, both written back into design.md and the delta specs. |

**Decision: pass with warnings.** No critical issue. Three warnings, listed under Overall Decision.

---

## 1. Structural Validation (`openspec validate --all --json`)

- [x] All items `"valid": true`

**Result**:

```text
items: 12, passed: 12, failed: 0 (1 change, 11 specs)
```

---

## 2. Task Completion (`tasks.md`)

- [x] All `- [ ]` have been changed to `- [x]`

21 of 21. No task is incomplete.

---

## 3. Delta Spec Sync State

| Capability | Sync status | Notes |
|---|---|---|
| `sweep-schedule` | ✗ pending sync | New capability. `/opsx:archive` creates `openspec/specs/sweep-schedule/spec.md`. |
| `memory-stats` | ✗ pending sync | "Sweep stats" and "The eval does not move" are modified. |
| `agent-acceptance` | ✗ pending sync | "Demo script" is modified. |

---

## 4. Design / Specs Coherence Spot Check

| Sample item | design description | specs counterpart | Gap |
|---|---|---|---|
| D1 | `Run(ctx, firstDelay)`: one pass at the delay, then one per interval, ticker created after the first pass | `sweep-schedule`, "First pass after the first delay" | None |
| D3 | Decay time from the injected clock, schedule time from Go timers | `sweep-schedule`, "Pass time comes from the injected clock" | None |
| D4 | `ErrNotFound` from `Delete` is skipped, not counted, not logged | `sweep-schedule`, "A row already deleted is not an error" | None |
| D5 | The description says whose passes the stats cover and names no duration | `memory-stats`, "Sweep stats" | None |
| D7 | Fixture timestamps a real pass cannot delete | No requirement: test-only | None, by design |
| D8 | Docs say when the sweep block fills in | `agent-acceptance`, "Demo script" | None |

**Drift warnings** (non-blocking):

- plan.md Task 6 still carries the first wording of the `CONTRIBUTING.md` fixture rule and of three doc sentences. The whole-branch review changed them (apply.md, Deviations). design.md, tasks.md and the `agent-acceptance` delta carry the final wording; plan.md is left as the record of what was planned.

---

## 5. Implementation Signal

- [x] No unstaged files in the worktree
- [ ] All related commits have been pushed. Not yet: finalize pushes the branch and opens the PR.

**Commit range**: `971c76d..b43080a` on `feat/sweep-on-start` (7 code and docs commits, then the apply receipt).

---

## Requirement to implementation

| Requirement | Where | Proof |
|---|---|---|
| `sweep-schedule`: First pass after the first delay | `sweep/sweep.go:95` (`Run`); `cmd/engramd/main.go:36-37, 83, 87` | `TestRunFirstPassAfterFirstDelay`, `TestRunZeroFirstDelaySweepsAtStart`, `TestRunCancelledDuringFirstDelay`, `TestRunCancelledBetweenPasses`, the stderr assertion in `TestStdioAllTools`, and the acceptance run |
| `sweep-schedule`: Pass time comes from the injected clock | `sweep/sweep.go` (`tick` reads `s.clock.Now()`; no `time.Now` call in the file) | `TestRunPassTimeFromInjectedClock`, `TestRunFirstPassFloorBoundary` |
| `sweep-schedule`: A row already deleted is not an error | `sweep/sweep.go:80` | `TestSweepOnceAlreadyDeletedIsNotAnError`, `TestSweepOnceOtherDeleteErrorStillLogged` |
| `sweep-schedule` and `memory-stats`: The eval does not move | No file under `eval/`, `cmd/eval/` or `mock/` changed; `eval/arm.go:217` still calls only `SweepOnce` | Eval gate and full-output comparison, below |
| `memory-stats`: Sweep stats | `mcp/server.go:149` | The assertion in `statsOverTransport` (`mcp/stats_test.go:428-431`), run by `TestMemoryStatsOverTransport`; the shape check in `TestStdioAllTools`; the existing `TestStatsBeforeFirstTick`, `TestTickAccumulatesAtVirtualTime`, `TestTickPruneBoundaryRecorded`, `TestTickFailureLeavesStats`, `TestStatsConcurrentWithTick` |
| `agent-acceptance`: Demo script | `docs/demo.md:87-92` | Read against the requirement: it says the block is empty until the first pass, one minute after start, kept in memory, all namespaces, this process's passes |

The scenario "A session longer than the first delay" has no automated test, because a test would have to wait more than a minute (design D2). The acceptance run covers it.

`agent-acceptance` keeps two M6 obligations in its text. This change edited one paragraph of `docs/demo.md` and did not run the demo again. The commands in it are unchanged since M6, where the agent steps ran through `claude -p`, not the interactive UI.

---

## Gates

Run on the final tree, commit `1299620`, one at a time.

- `go build ./...`: ok.
- `go test -race -count=1 -coverprofile=cover.out ./...`: 9 packages ok. `./eval` took 323 s. `sweep` coverage is 100%.
- `golangci-lint run ./...`: 0 issues. The local binary is 2.15.0 and CI pins v2.12.2, so the CI `lint` job is the authority.
- Integration, stack healthy and schema applied, uncached: `go test -race -count=1 -tags integration -p 2 -v` on every package except the body of `./eval`. 8 packages ok, 331 tests passed, 0 failed, 0 skipped. `TestStoreSearch` passed three times beforehand. `./eval` was compiled with the tag and its tests were not run again.
- `govulncheck` is not installed here. CI runs it. `go.mod` and `go.sum` did not change.

## Eval

`go run ./cmd/eval --ci` on the final tree: the gate passed. Mix agent, rerank none, lambda 0.1, seed 42, compared with `eval/baseline.json`.

| Arm | precision@5 | recall@5 | ndcg@5 | mrr |
|---|---|---|---|---|
| none | 0.210 | 0.975 | 0.949 | 0.957 |
| uniform | 0.204 | 0.952 | 0.937 | 0.948 |
| type-aware | 0.179 | 0.872 | 0.875 | 0.884 |

The baseline holds two figures, type-aware precision@5 0.1788 and ndcg@5 0.8747, and both match. To cover the rest of the table, the full output was compared with the output of the same command on `main`, twice each. Every figure is identical. The only columns that differ are p50 and p95, which are wall-clock latencies, and they differ just as much between two runs of the same tree. `git diff origin/main -- eval/ cmd/eval/` is empty.

## Acceptance on the real binary

`engramd` built from this branch, default decay, one MCP client connected for 83 seconds.

- Startup line: `sweep_first_delay=1m0s sweep_interval=1h0m0s decay=type-aware`, logged at 19:29:49Z.
- `memory_stats` two seconds after start: `"sweep": {"last_pruned": 0, "last_run_at": null, "total_pruned": 0}`.
- `memory_stats` 80 seconds after start: `"sweep": {"last_pruned": 181, "last_run_at": "2026-11-15T19:30:49.5677164Z", "total_pruned": 181}`.
- Log: `sweep pruned decayed memories count=181`.

This was the first real pass on the dev store, which Franco agreed to. The store went from 565 rows with 185 candidates to 384 rows with 4. The 181 deleted rows were leftovers of the `mcp` integration tests, written with the 2023 clock before the fixture edit. The 4 remaining candidates are above the floor or live procedural.

## Project agents

- `reviewer` (Opus), whole branch: no Critical, no Important. Two fixes before merge and one comment fix, all applied in the fix wave and confirmed by a scoped re-review.
- `scoping-ux` (Opus): the description sentence is clear now and stays true after the follow-up change. Three doc sentences still said "store-wide"; fixed in the same wave.
- `security-auditor` (Fable): no Critical, High or Medium. No production query changed, no input validation changed, no log line carries content or credentials, and the timer path cannot leak a goroutine. Four Low findings and one nit, none caused by a line of this change; they are under Warnings and Follow-ups.
- `architect`: not run. The change adds no package and no interface.
- The write-path rule (a skeptic review of each finding) does not apply: `remember`, dedup, auto-link and supersede are untouched.

---

## Overall Decision

- [ ] ✅ PASS
- [x] ⚠️ PASS WITH WARNINGS — can proceed, with the three warnings below
- [ ] ❌ FAIL

**Warnings**

1. **A row touched during a pass is still deleted.** The sweep reads its candidates and then deletes each by id, and the delete does not re-check (`neo4j/store.go:414-416`). If a candidate is pinned, or reinforced by a `remember` that dedups onto it, between the read and its delete, it is deleted anyway, and that `remember` has already answered `deduped: true`. The window is the length of one pass, about a second for a full batch. This exists on `main` today at the hourly tick. This change adds one such window a minute into every session. The fix is a sweep-only delete that re-checks `last_accessed` and `pinned` in the same statement. It needs a store method, so it is outside this change. Recommended as the next change, before or together with `sweep-daily-claim`.
2. **The eval output is not byte-identical to `main`.** The two latency columns differ. They are wall-clock measurements and differ between runs of identical code; every other figure is identical.
3. **The branch is not pushed yet.** Finalize pushes it and opens the PR.

**Follow-ups, not part of this change**

- `PruneCandidates` returns at most 500 rows in no set order, so rows that can never be deleted can crowd out rows that can, and it loads each candidate's `content`, up to 100 kB a row.
- A pass does not check its context between deletes, so a cancel mid-pass logs one error per remaining row.
- `remember.supersedes` is not scoped to the caller's namespace. With a real sweep, a superseded row is hard-deleted about 30 days later.
- `TestM4SoftForgetAndInclude` now leaves one row dated a year ahead on each run, which no sweep examines for about 13 months.
- `TestStoreSearch` breaks again once enough identical-vector test rows pile up; `Search` filters by namespace after taking the 50 nearest rows store-wide.
- `docs/demo.md`, `README.md` and the PRD promise a first pass one minute after start. `sweep-daily-claim` must revisit those three sentences.

**Next step**:

`/opsx:continue` to finalize: push `feat/sweep-on-start` and open the PR. Then `/opsx:archive` on the same branch and push, so one PR carries code, archive and synced specs.
