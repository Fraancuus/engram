# Verification Report

**Change**: `sweep-delete-recheck`
**Verified at**: `2026-11-22 01:00`
**Iteration**: `1`
**Verifier**: session model (Claude Fable 5.2), with the project agents `reviewer` and `security-auditor`, an adversarial correctness reviewer and a spec-conformance reviewer, each finding attacked by a refuter. The session model recorded the gates, the integration run and the eval. A subagent (Claude Opus 5.5) wrote this report from that record and ran the read-only checks in sections 1 to 5 and the mapping itself.

---

## Summary

| Dimension | Status |
|---|---|
| Completeness | 14 of 14 tasks. 9 of 9 added or modified requirements implemented (5 added, 4 modified). |
| Correctness | 9 of 9 requirements mapped to code. Of 32 scenarios: 30 are covered by automated tests, 2 by the eval commands in this report. No scenario lacks a test or a command that asserts its THEN. |
| Coherence | Design followed. The Cypher of `DeleteIfStale`, `Reinforce` and `Pin`, the `Pruner` interface, the `remember` switch, the `recall` debug record and the fake's fields match design.md. One wording drift in D7 and stale text in plan.md, both non-blocking. |

**Decision: pass with warnings.** No critical issue. Six warnings, listed under Overall Decision.

---

## 1. Structural Validation (`openspec validate --all --json`)

- [x] All items `"valid": true`

**Result**:

```text
items: 13, passed: 13, failed: 0 (1 change, 12 specs)
```

The change has no issue. The validator also prints 19 warning-level notes on 7 synced specs (`agent-acceptance`, `eval-dataset`, `eval-harness`, `memory-stats`, `memory-store-fake`, `memory-supersession`, `tools-entry`), all "Requirement text is very long (>500 characters)". They are on `main` already and do not fail validation.

---

## 2. Task Completion (`tasks.md`)

- [x] All `- [ ]` have been changed to `- [x]`

14 of 14. No task is incomplete.

---

## 3. Delta Spec Sync State

| Capability | Sync status | Notes |
|---|---|---|
| `write-race-safety` | ✗ pending sync | New capability, 3 requirements, 9 scenarios. `openspec/specs/write-race-safety/` does not exist yet; `/opsx:archive` creates it. |
| `sweep-schedule` | ✗ pending sync | 2 added ("A pass deletes only rows that are still stale", "The stale delete re-checks under the row lock"), 2 modified ("A row already deleted is not an error", "The eval does not move"). The main spec does not mention `DeleteIfStale` yet. |
| `memory-store-fake` | ✗ pending sync | 2 modified ("MemStore satisfies the consumer interfaces", "Lifecycle and prune semantics"). The main spec does not mention `DeleteIfStale` yet. |

Not blocking: the sync happens at `/opsx:archive`, which runs on this branch right after the PR is opened.

---

## 4. Design / Specs Coherence Spot Check

| Sample item | design description | specs counterpart | Gap |
|---|---|---|---|
| D1 | Lock-first `DeleteIfStale`: dummy `SET`/`REMOVE`, then the stale `WHERE`, then `DETACH DELETE` | `sweep-schedule`, "The stale delete re-checks under the row lock" | None. `neo4j/store.go:472-479` is D1's Cypher line for line; the doc comment at `:457-461` is D1's; the comment at `:463-471` cites the manual section. |
| D2 | `Pruner` swaps `Delete` for `DeleteIfStale`; one `before` per pass | `sweep-schedule`, "A pass deletes only rows that are still stale" and "A row already deleted is not an error" | None. `sweep/sweep.go:20-23` is D2's interface; `:73-90` is D2's loop. |
| D3 | `Pin` and `SetForgotten` re-match after the `SET`; `Reinforce` writes `last_accessed`, re-matches, then increments | `write-race-safety`, "A writer reports a row that is gone" | None. `neo4j/store.go:408-413` (`Pin`), `:394-399` (`SetForgotten`, with `m.forgotten`) and `:190-196` (`Reinforce`) are D3's statements verbatim. |
| D4 | `remember` switch: nil, `ErrNotFound` falls through to the insert path, other errors fail | `write-race-safety`, "remember keeps the content when its dedup target is gone" | None. `mcp/remember.go:172-193` is D4's switch, plus the debug record at `:187` that D5 names. |
| D5 | `recall` logs `ErrNotFound` at debug, skips propagation, keeps the hit | `write-race-safety`, "recall tolerates a hit that was deleted" | None. `mcp/recall.go:278-285`. |
| D6 | `MemStore.DeleteIfStale` under the mutex, sharing `deleteLocked`; `FakeStore` fields `KeptIDs`, `ReinforceErrs`, `Propagated`, `StaleBefore`, `PruneBefore` | `memory-store-fake`, "Lifecycle and prune semantics" (`MemStore` only; `FakeStore` has no requirement, by design) | None. `mock/memstore.go:312-340`; `mock/fakes.go:69-73`; the return at `mock/fakes.go:159` is D6's expression verbatim. |
| D8 | The eval is unaffected | `sweep-schedule`, "The eval does not move" | None. See Eval. |

**Drift warnings** (non-blocking):

- design.md D7 (lines 295-296) says "Each call has its own 30 s context as a hang guard". The test gives each round one 30 s context that both calls of the round share (`neo4j/stale_integration_test.go:266`), and its own comment says "Each round has its own 30 s context" (`:231-232`). Wording only; the intent, a hang guard and no wall-time assertion, holds.
- plan.md still carries text from before the fix round: one 2-minute deadline (plan.md:25, :1063), the claim that a run "in which the two calls never overlapped cannot pass" (plan.md:1057), and the `remember` fall-through without its debug line. design.md carries the final wording (`966056c`); plan.md is left as the record of what was planned.

---

## 5. Implementation Signal

- [x] No unstaged files in the worktree. `git status --short` shows only the two new files `apply.md` and `verify.md` of this change, not yet committed.
- [ ] All related commits have been pushed. Not yet: finalize pushes the branch and opens the PR.

**Commit range**: `9c59ff3..5793fea` on `feat/sweep-delete-recheck`. 14 commits: 3 planning, 9 code and docs (`bba1966..5d80a7f`), then 2 openspec records. apply.md lists them.

---

## Requirement to implementation

| Requirement | Where |
|---|---|
| `write-race-safety`: A writer reports a row that is gone | `neo4j/store.go:181-213` (`Reinforce`, query `:190-196`), `:391-401` (`SetForgotten`), `:405-415` (`Pin`); `flagOne` maps a count of 0 to `engram.ErrNotFound` at `:432-433` |
| `write-race-safety`: remember keeps the content when its dedup target is gone | `mcp/remember.go:171-194` (switch at `:173`; `ErrNotFound` falls through at `:186-189`; other errors at `:190-192`); insert path `:196-243`, `applySupersedes` at `:243` |
| `write-race-safety`: recall tolerates a hit that was deleted | `mcp/recall.go:275-293` (`ErrNotFound` at `:278-285`; other errors at `:286`) |
| `sweep-schedule`: A pass deletes only rows that are still stale | `sweep/sweep.go:20-23` (`Pruner`), `:72-94` (`SweepOnce`: one `before` at `:73`, used at `:74` and `:83`, counted only when deleted at `:88-90`); unconditional `Delete` kept at `neo4j/store.go:440`, reached by `forget` at `mcp/forget.go:53` |
| `sweep-schedule`: The stale delete re-checks under the row lock | `neo4j/store.go:462-494` (query `:472-479`, `before` bound as UTC at `:481`) |
| `sweep-schedule`: A row already deleted is not an error | `sweep/sweep.go:83-90` (error logged and skipped at `:84-86`; `(false, nil)` not counted) |
| `sweep-schedule`: The eval does not move | `sweep.New` signature unchanged (`sweep/sweep.go:44`); `eval/arm.go:215-217` calls `sweep.New` and `SweepOnce(ctx, now)`; `MemStore.DeleteIfStale` shares the cascade `deleteLocked` (`mock/memstore.go:326`) |
| `memory-store-fake`: MemStore satisfies the consumer interfaces | `mock/memstore.go:312` (`DeleteIfStale`); assertions at `mock/memstore_conformance_test.go:9-12` |
| `memory-store-fake`: Lifecycle and prune semantics | `mock/memstore.go:96` (`Reinforce`), `:247` (`PropagateReinforce`), `:265` (`Supersede`), `:277` and `:282` (`SetForgotten`, `Pin`), `:299` (`Delete`), `:312-321` (`DeleteIfStale`, check and delete under one lock), `:326-340` (`deleteLocked`), `:344` (`PruneCandidates`) |

## Scenario to proof

Subtest names are given as `go test -run` spells them. Line numbers are those of `5d80a7f`. A later commit on this branch, `2fbe39d`, moved the fixture and the row count of `TestMemStoreDeleteIfStale` into two helpers after DeepSource flagged the test's complexity. It asserts the same things; in `mock/memstore_test.go` the test now starts at line 441 and the tests after it sit 15 lines lower.

**`write-race-safety`** (9)

| Scenario | Proof |
|---|---|
| Unknown id | `TestStoreWritersNotFound/{Reinforce,Pin,SetForgotten}` (`neo4j/stale_integration_test.go:316`, cases `:323-325`) |
| No acknowledged write is lost, over many rounds | `TestStoreWriterNeverAcknowledgesADeletedRow/Reinforce` and `/Pin` (`neo4j/stale_integration_test.go:234`, cases `:247` and `:250`, end states and "both occur" at `:296-310`); the mutation check |
| Target gone, content stored | `TestDoRememberStoresContentWhenDedupTargetIsGone/target_gone` (`mcp/remember_test.go:210`) for the stored content and the reply; `TestRememberDedupTargetGoneIsRecallable` (`mcp/remember_test.go:318`) for recall |
| Target gone on a correction | `TestDoRememberStoresContentWhenDedupTargetIsGone/target_gone_on_a_correction` (`mcp/remember_test.go:215`) |
| Target gone beside a second live candidate | `TestDoRememberTargetGoneBesideSecondCandidate` (`mcp/remember_test.go:258`) |
| Target present | `TestDoRememberDedupBoundaryUnchanged` (`mcp/remember_test.go:182`), at `defaultDedupThreshold` 0.95 |
| Reinforce fails for another reason | `TestDoRememberReinforceFailureStillFails` (`mcp/remember_test.go:282`) |
| Hit deleted before its reinforce, at fixed virtual time | `TestRecallToleratesDeletedHit/second_hit_gone` (`mcp/recall_test.go:603`) |
| Every hit deleted | `TestRecallToleratesDeletedHit/every_hit_gone` (`mcp/recall_test.go:604`) |

**`sweep-schedule`** (12)

| Scenario | Proof |
|---|---|
| One cutoff per pass, at fixed virtual time | `TestSweepOnceUsesOneCutoff` (`sweep/sweep_test.go:468`) |
| A kept candidate is not counted | `TestSweepOnceKeptRowIsNotCounted` (`sweep/sweep_test.go:445`) |
| Pinned after the candidate read, at fixed virtual time | `TestStoreDeleteIfStale/pinned_after_the_candidate_read` (`neo4j/stale_integration_test.go:45`) |
| Reinforced after the candidate read | `TestStoreDeleteIfStale/reinforced_after_the_candidate_read` (`neo4j/stale_integration_test.go:56`) |
| Cutoff boundary | `TestStoreDeleteIfStale/last_accessed_equal_to_the_cutoff` (`:67`) and `/one_millisecond_before_the_cutoff` (`:68`) |
| Nothing left on a kept memory | The `sweep_check` assertion at `neo4j/stale_integration_test.go:92`, run on every kept case (`fresh` at `:44`, pinned, reinforced, equal to the cutoff) |
| Relationships go with the memory | `TestStoreDeleteIfStale/relationships_go_with_the_memory` (`neo4j/stale_integration_test.go:101`; 3 edges asserted before at `:126`, none left after at `:135-142`) |
| Unknown id | `TestStoreDeleteIfStale/unknown_id` (`neo4j/stale_integration_test.go:145`) |
| Every candidate already gone, at fixed virtual time | `TestSweepOnceAlreadyDeletedIsNotAnError` (`sweep/sweep_test.go:400`) |
| Another delete error is still reported | `TestSweepOnceOtherDeleteErrorStillLogged` (`sweep/sweep_test.go:429`) |
| Eval sweeps on virtual time only | Command: `grep -rn 'SweepOnce\|\.Run(' eval/ cmd/eval/` on non-test files finds only `eval/arm.go:217`, `sw.SweepOnce(ctx, now)`, and no call to `Run` |
| Gate with zero drift | Command: `go run ./cmd/eval --ci` on the branch and on `main`, outputs compared without p50 and p95, and `git diff origin/main -- eval/ cmd/eval/`. See Eval. |

**`memory-store-fake`** (11)

| Scenario | Proof |
|---|---|
| Compile-time conformance | `mock/memstore_conformance_test.go:10-11`, compiled by `go vet ./...` and `go test` |
| Concurrent recall and sweep | `TestMemStoreConcurrentSearchDelete` (`mock/memstore_test.go:360`; `Reinforce` at `:389-394`, `DeleteIfStale` at `:395-400`), under `-race` |
| Concurrent stats and delete | `TestMemStoreStatsConcurrent` (`mock/memstore_test.go:657`), unchanged |
| Delete cascades | `TestMemStoreDeleteCascades` (`mock/memstore_test.go:231`), unchanged |
| Prune predicate at the boundary | `TestMemStorePruneBoundary` (`mock/memstore_test.go:263`), unchanged |
| Pinned never a candidate | `TestMemStorePrunePinned` (`mock/memstore_test.go:279`), unchanged |
| Propagation skips superseded neighbors | `TestMemStorePropagateSkipsSuperseded` (`mock/memstore_test.go:478`), unchanged |
| DeleteIfStale cascades like Delete | `TestMemStoreDeleteIfStale/stale_row_deleted_with_its_link_and_mention` (`mock/memstore_test.go:418`; checks at `:455-473`) |
| DeleteIfStale at the boundary, at fixed virtual time | `TestMemStoreDeleteIfStale/LastAccessed_equal_to_before_kept` (`mock/memstore_test.go:419`) |
| DeleteIfStale keeps a pinned or reinforced memory | `TestMemStoreDeleteIfStale/pinned_kept` (`:420`) and `/reinforced_kept` (`:425`) |
| DeleteIfStale on an unknown id | `TestMemStoreDeleteIfStale/unknown_id` (`mock/memstore_test.go:430`) |

The `FakeStore` behaviour that the sweep and handler tests rely on is proven by `TestFakeStoreDeleteIfStale` (`mock/fakes_test.go:12`) and `TestFakeStorePerIDScripts` (`mock/fakes_test.go:53`). No spec requirement covers `FakeStore` (design D6).

---

## Gates

Run on the final tree. Code state at `5d80a7f`; the commits after it touch only `openspec/`.

- `gofmt -l .`: nothing. `go build ./...`: ok. `go vet ./...` and `go vet -tags integration ./...`: ok.
- `golangci-lint run ./...`: 0 issues. `golangci-lint run --build-tags integration ./...`: 0 issues. The local binary is 2.15.0 and CI pins v2.12.2, so the CI `lint` job is the authority.
- `gitleaks git --log-opts="origin/main..HEAD"`: no leaks found.
- `go test -race -count=1 -coverprofile=cover.out ./...`: 9 packages ok. `./eval` took 273.9 s; no eval test was added. Coverage: root 93.5%, `cmd/engramd` 17.5%, `cmd/eval` 75.5%, `eval` 93.4%, `inference` 89.5%, `mcp` 90.8%, `mock` 86.0%, `neo4j` 2.0% (unit run; its tests are integration tests), `sweep` 100.0%.
- `govulncheck` is not installed here. CI runs it. `go.mod` and `go.sum` did not change.

Re-checked by the report writer on this tree: `gofmt -l .` prints nothing; `git diff origin/main -- eval/ cmd/eval/` is empty; `git diff origin/main -- go.mod go.sum` is empty; `git diff --name-only 5d80a7f..HEAD` lists only `design.md` and `tasks.md` of this change. No line of the recorded evidence was contradicted.

## Integration run

Stack up (neo4j, tei, tei-rerank healthy), schema applied. `go test -race -count=1 -tags integration -p 2 -v` on every package except `./eval` (`go list ./... | grep -v 'engram/eval$'`): 8 packages ok, 371 PASS lines (252 top-level tests), 0 FAIL, 0 SKIP.

`TestStoreWriterNeverAcknowledgesADeletedRow`: for `Reinforce`, 51 rounds kept the row with the write and 49 deleted it; for `Pin`, 49 and 51.

Mutation check: the race test must fail when the dummy write is taken out of `DeleteIfStale`. It ran twice during apply, in Task 3 and again after the fix round changed the test. Both times the test failed, with round lines for both pairings, and passed again after `git restore neo4j/store.go`.

Dev store: 384 `:Memory` rows before the integration run. The run added 77 rows, all left by existing tests (namespaces `itest-*`, `m1-*` to `m4-*`, `sweep-itest-*`, and three ids `m0-e2e-*`, `m0-itest-roundtrip-*`, `m0-itest-localtime-*` in `work/engineering`), all created inside the run's window. They were deleted by exact id. After: 384 rows, the id set identical to before, 0 rows with a `sweep_check` property.

## Eval

`go run ./cmd/eval --ci` on the branch: exit 0, "eval gate passed". The same on a scratch copy of `origin/main`: exit 0. Rerank none, mix agent, lambda 0.1, seed 42; identical on `main`.

| Arm | precision@5 | recall@5 | ndcg@5 | mrr |
|---|---|---|---|---|
| none | 0.210±0.000 | 0.975±0.000 | 0.949±0.000 | 0.957±0.000 |
| uniform | 0.204±0.000 | 0.952±0.000 | 0.937±0.000 | 0.948±0.000 |
| type-aware | 0.179±0.000 | 0.872±0.000 | 0.875±0.000 | 0.884±0.000 |

The type-aware 0.179 and 0.875 are `eval/baseline.json`'s 0.1788 and 0.8747 at the report's three decimals. The two full outputs are equal once the p50 and p95 columns, which are wall-clock latencies, are removed. `git diff origin/main -- eval/ cmd/eval/` is empty. No code under `eval/` or `cmd/eval/` calls `Run`.

One orchestrator mistake, recorded plainly: the first attempt at the comparison had a shell error. A `git rev-parse` call with two revisions failed and short-circuited the chain, so only `main`'s side ran. It was not an eval result. The branch side was then run, and the comparison above is from that second run.

## Project agents

- `reviewer` (Opus), `security-auditor` (Fable), an adversarial correctness reviewer (Opus) and a spec-conformance reviewer (Opus) reviewed the whole branch. A separate Opus refuter attacked every finding filed as Critical or Important. Result: 0 Critical, 0 Important. Two filed as Important were the same point about the race test's comment and were downgraded to wording.
- The spec-conformance reviewer found a test asserting the full THEN for 30 of the 30 scenarios that tests prove. The mapping above agrees.
- Eleven minor findings were fixed in one round (`bb9fdee`, `5d80a7f`). A scoped re-review on Haiku found all eleven fixed and no regression. apply.md lists them.
- `architect`: not among the lenses run. The change adds no package and no interface; it swaps one method on `Pruner`.
- `scoping-ux`: not run. No MCP field or tool description changed.
- The write-path rule applies, because `remember` is a write path. The refuter pass above is that skeptic review.

---

## Overall Decision

- [ ] ✅ PASS — ready to proceed via /opsx:continue to the finalize artifact, then /opsx:archive
- [x] ⚠️ PASS WITH WARNINGS — can proceed but note: `six warnings, below; none is a failed check`
- [ ] ❌ FAIL — return to the failed artifact, fix, then re-run verify

**Warnings**

1. **CI has not run.** The branch is not pushed yet. Finalize pushes it and opens the PR.
2. **The race guard runs outside CI.** CI does not run integration tests. The lock-first delete and the writers' re-match are proven only by the local run on `neo4j:5.26-community` recorded here. `CONTRIBUTING.md` asks for the race test and the mutation check again whenever the Neo4j image changes.
3. **`govulncheck` was not run locally.** It is not installed here. CI runs it. `go.mod` and `go.sum` did not change.
4. **The new debug records cannot be seen in `engramd` today.** It logs at Info and has no switch. The `remember` fall-through and the `recall` vanished hit are proven by unit tests at debug level only.
5. **design.md D7 wording.** It said each call has its own 30 s context; the test has one per round, shared by both calls. See section 4. Corrected in design.md in the commit that adds this report.
6. **plan.md carries text from before the fix round.** See section 4. It is left as the record of what was planned.

**Follow-ups, not part of this change** (from design.md, Open Questions and Risks)

- A `remember` whose auto-link target is deleted mid-statement answers `remember: store unavailable` although the content is stored. It predates this change, at the same rate with the old delete and the new one (open question 3).
- Target gone and a second live candidate at or above 0.95: the content is stored as a near-duplicate of that candidate (open question 4, chosen).
- `forget` with mode pin on a pruned memory says only "memory not found" (open question 1).
- `engramd` has no switch to lower the log level, so the debug records stay hidden.
- Coordinating passes between processes is `sweep-daily-claim`.

**Next step**:

`/opsx:continue` to finalize: push `feat/sweep-delete-recheck` and open the PR. Then `/opsx:archive` on the same branch and push, so one PR carries code, archive and synced specs.
