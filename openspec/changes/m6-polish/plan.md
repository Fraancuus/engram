# M6 Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Implementers `sonnet`, task reviewers `opus`, scoped re-reviews `haiku`, explicit model on every dispatch (repo `CLAUDE.md`, "Model tiers").

**Goal:** Retrievability stops penalising live memories, stale facts clear through explicit supersession, and the eval reports a named mix and a store cost, so the README can state what the numbers support.

**Architecture:** No new package, port or interface. `mcp` changes one constant, moves the pool cut and widens supersession; both stores gain one predicate; `eval` gains `Params.Mix` and `Results.Lambda`.

**Tech Stack:** Go 1.26, `mcp`, `mock`, `neo4j`, `eval`, `cmd/eval`.

**Spec:** `openspec/changes/m6-polish/design.md` (D11 to D17) and `openspec/changes/m6-polish/specs/`.

**How this changes A/B/C (expected before the first run; `design.md`, "Result at the floor", has what happened):** A's order stays sim + 0.3 importance. B and C stop losing fresh episodes to permanent standards. Stale facts leave every arm, so `none` and `type-aware` converge on facts and no class remains where forgetting beats `none`. The store weight then decides A versus C.

**How this changes the CI gate:** Logic unchanged (arm C against its own past, tolerance 0.02). Baseline moves to the agent mix. Red from Task 2 to Task 11 by design.

## Global Constraints

- Follow TDD: failing test first, then the code. Run `graphify query "<question>"` before the first search or source read; run `graphify update .` after a task that changes Go code.
- Run tests as `go test -race ./<pkg>/...`, scoped to the task's packages. Full `go test -race ./...` runs at Task 13.
- Never call `time.Now()` in decay or retrievability logic. Parameterized Cypher only. Wrap errors with `%w`.
- Do not run the eval to choose or adjust lambda, a mix percentage or `sigmaTwin`. `sigmaTwin` is tuned against `TestTwinRange` only.
- Values are fixed: `defaultWRet` 0.01; `linkThreshold` and `propagateThreshold` 0.925; `defaultDedupThreshold` 0.95; `DefaultLambda` 0.10; tie margin 0.005; `agent` 35/20/30/5/10; `stress` 25/15/20/15/25; twin band [0.86, 0.89].
- Commit per task. End commit messages with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- Stop and report if live code contradicts this plan, or a test fails twice after a reasonable fix.

## Review Focus

1. A write with `supersedes` that dedups into a different live memory: the caller still expects the named memory to be replaced. Pinned by `TestDoRememberDedupStillSupersedes` in Task 3.
2. Every candidate dead: recall returns an empty list with no error and reinforces nothing. Pinned by `TestRecallAllDeadIsEmpty` in Task 2.
3. A store baseline of 0 in the report: score and break-even must never be NaN or infinite. Pinned by `TestRecommendZeroStoreBaseline` in Task 7.
4. `-lambda` under `--ci`: it changes printed scores and never the gate's verdict. Pinned by `TestGateIgnoresLambda` in Task 8.
5. `-scale 2` with a named mix: the mix survives `Scaled` and class counts sum to the query total. Pinned by `TestScaledKeepsMix` in Task 5.

---

### Task 1: Stop refreshing superseded memories in both stores

**Files:**
- Modify: `mock/memstore.go` (`PropagateReinforce`), `neo4j/store.go` (`PropagateReinforce`)
- Test: `decay_test.go`, `mock/memstore_test.go`, `neo4j/decay_integration_test.go`

**Interfaces:**
- Produces: `PropagateReinforce` skips superseded targets, signature unchanged.

- [ ] **Step 1: Write failing tests**
  - `TestRetrievabilitySupersededSemantic` in `decay_test.go`: a table over `TypeAwareDecay` and `UniformDecay` with `Type: Semantic, Superseded: true`, `dt` 5.99 and 6.00 days; R equals `exp(-dt/2)` within 1e-9, so 5.99 d is above 0.05 and 6.00 d is below. This pins existing behaviour and passes at once. If it fails, stop and report.
  - `TestMemStorePropagateSkipsSuperseded` in `mock/memstore_test.go`: `a` has edges to `live` (0.9) and `old` (1.0), `old` is superseded; after `PropagateReinforce(ctx, a, 0.85, t1)`, `Snapshot(live).LastAccessed == t1` and `Snapshot(old).LastAccessed == t0`.
  - `TestMemStorePropagateCapIgnoresSuperseded`: 25 edges at threshold or above, the 5 heaviest superseded; exactly the 20 live neighbors are refreshed.
  - Extend `TestStorePropagateReinforce` in `neo4j/decay_integration_test.go`: add a strong superseded neighbor; assert its `LastAccessed` is unchanged.
- [ ] **Step 2: Run** `go test -race ./mock/... -run Propagate`. Expected: FAIL on both new mock tests.
- [ ] **Step 3: Implement.** In `MemStore.PropagateReinforce`, keep the `break` on cap or weight, then `continue` on a superseded target before counting. In Cypher: `WHERE r.weight >= $thr AND coalesce(nb.superseded, false) = false`, before `LIMIT`. Update both doc comments.
- [ ] **Step 4: Run** `go test -race . ./mock/...` and `go vet -tags=integration ./neo4j/...`. Expected: PASS and no vet error.
- [ ] **Step 5: Commit** `fix(store): propagation skips superseded memories`.

### Task 2: Retrievability as a pre-pool filter and bounded tiebreak

**Files:**
- Modify: `mcp/recall.go`
- Test: `mcp/recall_test.go`, `mcp/blend_test.go`

**Interfaces:**
- Produces: `blend(seeds []engram.RecallResult, neighbors []engram.Neighbor, bridgePenalty float64) []engram.RecallResult`. `scoreAndFilter` keeps its signature.

- [ ] **Step 1: Write failing tests.** Add a test-local `type rByID map[engram.MemoryID]float64` implementing `engram.DecayModel`. Each test sets `h.wImp, h.wRet = defaultWImp, defaultWRet`.
  - `TestRecallFreshEpisodeBeatsStandard`: episode sim 0.95, importance 0.4, R 0.7; standard sim 0.80, importance 0.7, R 1.0. The episode is first.
  - `TestRecallBoundedTiebreak`, table: gap 0.009, lower-scoring candidate R 1.0, other R 0.05: the R 1.0 candidate is first. Gap 0.011, same R values: the higher-scoring candidate is first.
  - `TestRecallDeadCandidatesDoNotCrowdPool`: 20 superseded seeds at sim 0.9 and 5 live seeds at sim 0.5; `k` 5 returns the 5 live ones.
  - `TestRecallSoftThresholdBoundary`: R exactly 0.05 is kept, R 0.0499 is dropped.
  - `TestRecallNullDecayOrder`: with `engram.NullDecay{}`, the order equals the order by sim + 0.3 importance.
  - `TestRecallKAboveDefaultPool`: 150 live seeds with `seedN` raised to 150, `k` 100: 100 results.
  - `TestRecallAllDeadIsEmpty`: every seed below the threshold: empty results, nil error, `st.Reinforced` empty.
  - `TestDoRecallIncludeForgottenRanksDead`: `IncludeForgotten: true`, R 0 and superseded: returned, ranked by score.
  - In `mcp/blend_test.go`, rename `TestBlendTopKAndTiebreak` to `TestBlendOrderAndTiebreak`; it asserts order and id tiebreak with no truncation. Drop the `k` argument everywhere.
- [ ] **Step 2: Run** `go test -race ./mcp/... -run 'Recall|Blend'`. Expected: compile error, then FAIL.
- [ ] **Step 3: Implement.** `defaultWRet = 0.01`, with a comment saying R can only reorder scores within 0.01. Remove `k` and the truncation from `blend`. In `doRecall`: `cands := t.scoreAndFilter(blend(seeds, neighbors, bridgePenalty), now, in.IncludeForgotten)`, then cut to `poolSize`, then rerank.
- [ ] **Step 4: Run** `go test -race ./mcp/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(mcp): retrievability filters before the pool and only breaks ties`.

### Task 3: Semantic supersession and the dedup bypass

**Files:**
- Modify: `mcp/remember.go`
- Test: `mcp/remember_test.go`

**Interfaces:**
- Produces, unexported in `mcp`: `dedupTarget(cands []engram.RecallResult, replaces []string) (engram.RecallResult, bool)` on `*Tools`, and `applySupersedes(ctx context.Context, ids []string) []string` on `*Tools`, returning the applied ids or nil.

- [ ] **Step 1: Write failing tests.**
  - `TestDoRememberSupersedesSemantic`: semantic write with `Supersedes: ["old1"]`: `st.Superseded` holds `old1`, `out.Superseded == ["old1"]`.
  - `TestDoRememberSupersedeIgnoredForEpisodic` replaces `TestDoRememberSupersedeIgnoredForNonProcedural`: `Type: "episodic"`: nothing superseded, `out.Superseded` nil.
  - `TestDoRememberDedupSkipsNamedSupersede`: candidate `old` at 0.97, `Supersedes: ["old"]`: `Deduped` false, one `Put`, `old` superseded, `old` not in `st.Reinforced`.
  - `TestDoRememberDedupSkipsSuperseded`: candidate at 0.97 with `Superseded: true`, no `Supersedes`: `Deduped` false, one `Put`, nothing reinforced.
  - `TestDoRememberDedupFallsToNextLive`: `old` at 0.98 named, `live` at 0.96: `Deduped` true, `MemoryID == "live"`, `live` reinforced, no `Put`.
  - `TestDoRememberDedupStillSupersedes`: same setup as above: `old` is superseded and `out.Superseded == ["old"]`.
  - `TestDoRememberDedupBoundaryUnchanged`: live candidate at exactly 0.95, no `Supersedes`: `Deduped` true.
  - `TestDoRememberEpisodicSupersedesKeepsDedup`: episodic write naming live `old` at 0.97: `Deduped` true, `old` reinforced, nothing superseded.
  - `TestDoRememberRetriedCorrectionIdempotent`: candidate `prior` at 0.99, `Supersedes: ["old"]`: `Deduped` true into `prior`, `old` superseded, `out.Superseded == ["old"]`.
- [ ] **Step 2: Run** `go test -race ./mcp/... -run 'DoRemember'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Right after validation, compute `replaces := in.Supersedes`, set to nil for an episodic write; use `replaces` for both dedup and supersession, so both key off one condition. `dedupTarget` walks candidates in score order, stops below `t.dedupThreshold`, and skips superseded or named ones; give it the comment `shortcut: Search returns autoLinkK=5 candidates, 5 skipped ones hide a live duplicate at rank 6, raise autoLinkK if that shows up`. `applySupersedes` holds the existing best-effort block (log and return nil on failure). Call it from both the dedup and the insert path. Update the `Supersedes` jsonschema text to "(procedural or semantic)".
- [ ] **Step 4: Run** `go test -race ./mcp/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(mcp): semantic supersession; dedup never defeats a replacement`.

### Task 4: Thresholds on the normalised scale, twin band follows

**Files:**
- Modify: `mcp/remember.go`, `mcp/recall.go`, `eval/vectors.go`, `eval/dataset.go` (`bandScore`)
- Test: `mcp/remember_test.go`, `mcp/recall_test.go`, `eval/dataset_test.go`

**Interfaces:**
- Produces: constants `minTwinCos = 0.86`, `maxTwinCos = 0.89` in `eval/vectors.go`.

- [ ] **Step 1: Write failing tests.**
  - Update the auto-link tests at `mcp/remember_test.go:292` and `:364`: 0.93 links, 0.925 links with weight 0.925, 0.9249 does not.
  - `TestPropagateThresholdBoundary` in `mcp/recall_test.go`, over `mock.NewMemStore()`: edges at weight 0.925 and 0.9249: after a recall only the first neighbor's `LastAccessed` is `now`; an explicit weight 1.0 link is refreshed.
  - `TestTwinRange` in `eval/dataset_test.go`: bounds 0.86 and 0.89.
- [ ] **Step 2: Run** `go test -race ./mcp/... -run 'Link|Propagate'` and `go test -race ./eval/... -run TwinRange`. Expected: FAIL.
- [ ] **Step 3: Implement.** Set both thresholds to 0.925 and rewrite their comments, plus `defaultDedupThreshold`'s, to state the scale and raw cosine. `bandScore` uses the twin band for twin pairs and `[minLinkedCos, maxLinkedCos]` for chain versions. Retune `sigmaTwin` from 1.5 to about 0.95 until `TestTwinRange` passes on seed 42. Rewrite the `eval/vectors.go` geometry comment.
- [ ] **Step 4: Run** `go test -race ./mcp/... ./eval/... -run 'Dataset|Vector|Geometry|Link|Propagate'` and `go vet -tags=integration ./...`. Fix any integration vector that assumed the 0.85 link point. Expected: PASS.
- [ ] **Step 5: Commit** `fix(mcp,eval): restate link and propagate thresholds on the normalised scale`.

### Task 5: Named query mix

**Files:**
- Modify: `eval/params.go`, `eval/schedule.go`
- Test: `eval/params_test.go`, `eval/schedule_test.go`

**Interfaces:**
- Produces: `const MixAgent = "agent"`, `const MixStress = "stress"`; `Params.Mix string` with JSON key `mix`; `ParseMix(s string) (string, error)`, mirroring `ParseRerank`. `Floor()` sets `Mix: MixAgent`.

- [ ] **Step 1: Write failing tests.**
  - `TestQueryMixAgent`: 500 queries: class counts within 2 of 175/100/150/25/50, with bridge split evenly.
  - `TestQueryMixStress` adapts the existing mix test: within 2 of 125/75/100/75/125.
  - `TestParamsRejectUnknownMix`: `Validate` with `Mix: "weekend"` errors, naming it.
  - `TestMixKeepsCorpus`: same seed, both mixes: identical memories, vectors, links, write days and accesses.
  - `TestScaledKeepsMix`: `Floor().Scaled(2)` keeps `Mix`, and generated class counts sum to 1000.
  - `TestParseMix`: both presets accepted; `""` and an unknown name error.
- [ ] **Step 2: Run** `go test -race ./eval/... -run 'Mix'`. Expected: compile error, then FAIL.
- [ ] **Step 3: Implement.** Add an unexported preset table in `params.go`. `buildQueries` reads it and returns an error on an unknown mix; `Validate` rejects an unknown mix. Set `Mix` in test params built by hand. Keep the integer math `p.Queries*pct/100`, with bridge taking the remainder.
- [ ] **Step 4: Run** `go test -race ./eval/... -run 'Mix|Params|Query|Schedule'`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(eval): named query mix presets, agent is primary`.

### Task 6: Fact chains carry Supersedes

**Files:**
- Modify: `eval/dataset.go`
- Test: `eval/dataset_test.go`, `eval/arm_test.go`

**Interfaces:**
- Consumes: Task 3's semantic supersession. No new names.

- [ ] **Step 1: Write failing tests.**
  - Update the composition test at `eval/dataset_test.go:112`: `m.Supersedes` is empty only when `m.Version == 1`, otherwise it is the chain predecessor's GID.
  - `TestWriteSupersedesPrunedPredecessor` in `eval/arm_test.go`: an arm writes v1, deletes it from the store, writes v2 with `Supersedes` = v1: no error.
  - Check the assertions at `eval/arm_test.go:100` and `:261`. If they assume only standards supersede, narrow them and say why in the commit message.
- [ ] **Step 2: Run** `go test -race ./eval/... -run 'Composition|Topic|Supersede'`. Expected: FAIL.
- [ ] **Step 3: Implement.** In the fact loop set `m.Supersedes` to the previous chain entry's GID for `v > 1`. Update the `Memory.Supersedes` and `KindFact` comments.
- [ ] **Step 4: Run** `go test -race ./eval/...`. Expected: PASS. Tests against numeric floor output are out of date only if they compare to `baseline.json`; report any such test rather than editing its number.
- [ ] **Step 5: Commit** `feat(eval): fact chains carry explicit supersession`.

### Task 7: Recommendation score and report

**Files:**
- Modify: `eval/report.go`, `eval/run.go`
- Test: `eval/report_test.go`

**Interfaces:**
- Produces: `const DefaultLambda = 0.10`; `const TieScore = 0.005` (renamed from `TieNDCG`); `Results.Lambda float64`, set to `DefaultLambda` by `Run` and overridable by the caller; unexported `storeBaseline(r *Results) float64`, `armScore(s Summary, base, lambda float64) float64`, `breakEven(none, arm Summary) (float64, bool)`.

- [ ] **Step 1: Write failing tests.**
  - `TestRecommendStoreCostFlips`: `fakeResults(0.98, 0.1, 0.90, 5000, 5000, 1500)` with `Lambda` 0.20 recommends `type-aware`.
  - `TestRecommendLambdaZero`: same arms with `Lambda` 0 recommends `none`. Existing `TestRecommendTieBreak` and `TestRecommendMaxNDCG` stay as they are; they run at `Lambda` 0.
  - `TestRecommendNoNoneArm`: without `none`, the largest arm is the store baseline.
  - `TestRecommendZeroStoreBaseline`: all final sizes 0: score equals nDCG, nothing is NaN.
  - `TestBreakEven`: 0.98/5000 versus 0.90/1500 gives 0.114 (3 d.p.); equal store gives `ok == false`; an arm with higher nDCG gives 0.
  - `TestReportTableColumns`: header has `score`; the `rerank:` line names mix and lambda.
  - `TestReportNamesMix`: with `Params.Mix` `stress` and `Lambda` 0.1, the output has `mix: stress` and `recommended: type-aware (ENGRAM_DECAY=type-aware) on mix stress, lambda 0.1`.
  - `TestReportBreakEvenLine`: prints `break-even lambda vs none:` with `n/a` for an arm with no store saved.
  - Extend `TestAccessHitSummaryAndTable`: an empty-result access is a miss, and a seed with no accesses of a kind stays out of that kind's mean.
- [ ] **Step 2: Run** `go test -race ./eval/... -run 'Recommend|Report|BreakEven|AccessHit'`. Expected: FAIL.
- [ ] **Step 3: Implement.** `score = nDCG - lambda*size/base`, with the cost term 0 when `base <= 0`. `breakEven` returns `max(0, (nDCGnone-nDCGarm)/(1-sizeArm/sizeNone))`, and false when `sizeNone <= 0` or `sizeArm >= sizeNone`. Print lambda with `%g`.
- [ ] **Step 4: Run** `go test -race ./eval/... -run 'Recommend|Report|BreakEven|AccessHit|JSON'`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(eval): recommendation weighs store cost, report names its mix`.

### Task 8: Gate message and command flags

**Files:**
- Modify: `eval/baseline.go`, `cmd/eval/main.go`
- Test: `eval/baseline_test.go`, `cmd/eval/main_test.go`

**Interfaces:**
- Consumes: `eval.ParseMix`, `eval.MixAgent`, `eval.DefaultLambda`, `Results.Lambda`.

- [ ] **Step 1: Write failing tests.**
  - `TestCheckGateStale`: a table over seed, `Params.Memories`, `Params.Mix`, rerank and k: each errors with `baseline stale` and `--update-baseline`. Keep `TestGateRejectsStaleParams`.
  - `TestCheckGateNoMixIsStale`: a baseline with `Mix: ""`: stale.
  - `TestCheckGateStaleBeatsRegression`: rerank differs and precision is below tolerance: the message is the stale one.
  - `TestGateIgnoresLambda`: two `Results` differing only in `Lambda` get the same verdict.
  - In `cmd/eval/main_test.go`: `TestRunRejectsBadLambda` (`-lambda -0.1` and `-lambda NaN` exit 2, no run), `TestRunRejectsUnknownMix` (`-mix weekend` exits 2), `TestUpdateBaselineRefusesStress` (exit non-zero, file unchanged).
- [ ] **Step 2: Run** `go test -race ./eval/... -run 'Gate' ` and `go test -race ./cmd/eval/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** The stale message names seed, params (including mix), rerank and k. Flags: `-mix` (default `eval.MixAgent`) through `ParseMix`, `-lambda` (default `eval.DefaultLambda`) rejecting negative, NaN and infinite values. Under `--ci`, set `p.Mix` to the baseline's mix when non-empty, and add `mix` to the ignored-flag note. A non-primary `-mix` with `--update-baseline` exits 1. Set `res.Lambda` after `eval.Run`. Update the package doc comment.
- [ ] **Step 4: Run** `go test -race ./eval/... -run 'Gate|Baseline'` and `go test -race ./cmd/eval/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(cmd/eval): -mix and -lambda flags, stale guard names every field`.

### Task 9: Mid-branch review (orchestrator)

- [ ] **Step 1:** Dispatch `reviewer` (`model: opus`) over `git diff origin/main...HEAD -- '*.go'`.
- [ ] **Step 2:** Dispatch `security-auditor` (`model: fable`) on the Cypher predicate and the `supersedes` boundary, including whether a caller can supersede a memory in another namespace (the procedural path allows it today).
- [ ] **Step 3:** Resolve every blocking finding with a `sonnet` implementer; `haiku` re-reviews small fixes. Commit fixes.

### Task 10: Integration tests against Neo4j

- [ ] **Step 1:** `docker compose up -d`, wait for Neo4j to answer, run `go test -race -tags=integration ./neo4j/... ./mcp/... ./sweep/...`. Expected: PASS.
- [ ] **Step 2:** If Neo4j cannot start, record that the Cypher predicate was not exercised and tell Franco. Do not claim otherwise.

### Task 11: Re-baseline and capture results (orchestrator)

- [ ] **Step 1:** `go run ./cmd/eval --update-baseline -seed 42`. Expected: `baseline written to eval/baseline.json`, with `"mix": "agent"`.
- [ ] **Step 2:** `go run ./cmd/eval --ci` twice. Expected: both print `eval gate passed` with byte-identical arm C rows.
- [ ] **Step 3:** Capture tables: `-seed 42 -seeds 1`, the same with `-mix stress`, and the same with `-lambda 0`.
- [ ] **Step 4:** Add "Result at the floor" to `design.md`, placing the numbers next to the D16 predictions and leaving any miss as it stands. Commit `eval/baseline.json` and `design.md`.

### Task 12: README and PRD

**Files:**
- Modify: `README.md`, `docs/engram-prd-v1.md`

- [ ] **Step 1:** Rewrite per D17, using Task 11's numbers: headline line 5, thesis line 12 if the numbers require it, the eval section (claim, both tables, lambda and break-even, new flags), the recall description at line 111 (filter plus tiebreak, not a decay-weighted blend), line 114, status and roadmap lines, the archived M5 design path. No claim beyond the tables.
- [ ] **Step 2:** PRD section 9, M6: add a status line naming what landed and that `memory_stats` and the demo remain open.
- [ ] **Step 3:** Run `graphify update .`; confirm `git status` shows nothing from `graphify-out/`. Commit `docs: README claim follows the M6 numbers`.

### Task 13: Verify, ship, archive (orchestrator)

- [ ] **Step 1:** `go build ./...`, `go test -race ./...`, `golangci-lint run ./...`, `go run ./cmd/eval --ci`. Expected: all pass.
- [ ] **Step 2:** Final whole-branch `reviewer` (`model: opus`), then `/opsx:verify`, which reports the A/B/C table.
- [ ] **Step 3:** `/opsx:continue` through finalize: `git push -u origin feat/m6-polish` (the branch currently tracks `origin/main`), open the one PR.
- [ ] **Step 4:** `/opsx:archive` on the same branch, sync specs, push. Only now say it is ready to merge.
