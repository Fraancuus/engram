All tasks run under `go test -race`, scoped to the package named. The eval gate is expected to be
red from task 3 until task 10, which re-baselines it; do not run `--ci` as a pass/fail signal before
then. No eval run may be used to choose lambda, the mix or `sigmaTwin`.

## 1. Domain (root package)

- [x] 1.1 Pin the superseded-semantic curve already in `decay.go`: table cases in `decay_test.go` for `TypeAwareDecay` and `UniformDecay` at fixed virtual times, semantic superseded S = 2 d, R just above and below 0.05. Test: `TestRetrievabilitySupersededSemantic`. No production change expected; if one is needed, stop and report.

## 2. Mock store

- [x] 2.1 `mock.MemStore.PropagateReinforce` skips superseded targets without consuming the cap. Test: `TestMemStorePropagateSkipsSuperseded`, including 25 edges with the 5 heaviest superseded.

## 3. Adapters: recall ranking and supersession

- [x] 3.1 `neo4j.Store.PropagateReinforce` adds the superseded predicate to its parameterized Cypher, before `LIMIT` so skipped targets do not consume the cap. Test: extend `TestStorePropagateReinforce` under the `integration` tag.
- [x] 3.2 `mcp/recall.go`: `defaultWRet` 0.01, `blend` loses `k`, truncate to the pool after `scoreAndFilter`. Tests: `TestRecallBoundedTiebreak` (0.009 and 0.011 gaps), `TestRecallFreshEpisodeBeatsStandard`, `TestRecallDeadCandidatesDoNotCrowdPool`, `TestRecallSoftThresholdBoundary`, `TestRecallNullDecayOrder`, `TestRecallKAboveDefaultPool`, `TestRecallAllDeadIsEmpty`, `TestDoRecallIncludeForgottenRanksDead`, updated `mcp/blend_test.go`.
- [x] 3.3 `mcp/remember.go`: `Supersedes` applies to semantic writes; dedup skips candidates superseded or named in `Supersedes`; field description updated. Tests: `TestDoRememberSupersedesSemantic`, `TestDoRememberSupersedeIgnoredForEpisodic` (replaces the non-procedural test), `TestDoRememberDedupSkipsNamedSupersede`, `TestDoRememberDedupSkipsSuperseded`, `TestDoRememberDedupFallsToNextLive`, `TestDoRememberDedupStillSupersedes`, `TestDoRememberDedupBoundaryUnchanged`, `TestDoRememberEpisodicSupersedesKeepsDedup`, `TestDoRememberRetriedCorrectionIdempotent`.
- [x] 3.4 `linkThreshold` and `propagateThreshold` become 0.925, comments state the raw-cosine equivalents, and the twin band moves in the same task so the tree stays green: `minTwinCos` 0.86, `maxTwinCos` 0.89, `sigmaTwin` retuned against the band test only, `bandScore` uses the twin band, `eval/vectors.go` comment rewritten. Tests: updated `mcp/remember_test.go` boundary cases at 0.925 and 0.9249, `TestPropagateThresholdBoundary`, updated `TestTwinRange` in `eval/dataset_test.go`. Check `go vet -tags=integration ./...` and fix any integration vectors that assumed 0.85.

## 4. Eval: generator

- [ ] 4.1 `Params.Mix`, `MixAgent`, `MixStress`, the preset table, `Floor` sets agent, `Validate` rejects an unknown mix, `buildQueries` reads the preset. Tests: `TestQueryMixAgent`, `TestQueryMixStress`, `TestParamsRejectUnknownMix`, `TestParseMix`, `TestScaledKeepsMix`, `TestMixKeepsCorpus` (same memories, vectors, links, write days and accesses across mixes).
- [ ] 4.2 Fact versions after the first carry `Supersedes`; update the `Memory.Supersedes` comment. Tests: updated topic composition test, `TestWriteSupersedesPrunedPredecessor` (an arm where a predecessor is gone at write time). Run `eval/dataset_test.go` and `eval/arm_test.go`; arm tests pinned to the old stale-fact behaviour change with a reason in the commit message.

## 5. Eval: report and recommendation

- [ ] 5.1 `Results.Lambda`, `DefaultLambda = 0.10`, `Recommend` scores nDCG minus store cost, keeps the tie rule (rename `TieNDCG` to `TieScore`); store baseline is `none`, or the largest arm when `none` is absent. Tests: `TestRecommendStoreCostFlips` (0.98/5000 vs 0.90/1500 at 0.20), `TestRecommendLambdaZero`, `TestRecommendTieBreak` unchanged at lambda 0, `TestRecommendNoNoneArm`, `TestRecommendZeroStoreBaseline`.
- [ ] 5.2 `WriteTable` prints mix and lambda in the header, a `score` column, the break-even line with `n/a` when no store is saved, and the new recommendation line. Tests: `TestBreakEven` (0.114 case and the not-applicable case), `TestReportBreakEvenLine`, `TestReportTableColumns` updated, `TestReportNamesMix`. Pin the access hit-rate table by extending `TestAccessHitSummaryAndTable`.

## 6. Eval: gate

- [ ] 6.1 `CheckGate`'s message names seed, params including mix, rerank and k. Tests: table `TestCheckGateStale` covering each field, `TestCheckGateNoMixIsStale` (a baseline with no mix), `TestCheckGateStaleBeatsRegression`, and `TestGateIgnoresLambda`.

## 7. Command

- [ ] 7.1 `cmd/eval`: `-mix` (default agent; unknown exits 2), `-lambda` (default `DefaultLambda`; negative, NaN or infinite exits 2), `--ci` runs the baseline's mix with a note if `-mix` was passed, `--update-baseline` refuses a non-primary mix. Tests in `cmd/eval/main_test.go`: `TestRunRejectsBadLambda`, `TestRunRejectsUnknownMix`, `TestUpdateBaselineRefusesStress`.

## 8. Review before the numbers

- [ ] 8.1 `reviewer` (opus) over the Go diff so far; `security-auditor` (fable) over the Cypher predicate and the `supersedes` handling at the MCP boundary. Resolve every blocking finding before task 9.

## 9. Integration check

- [ ] 9.1 Bring Neo4j up with `docker compose up -d`, run `go test -race -tags=integration ./neo4j/... ./mcp/... ./sweep/...`. If Neo4j cannot start, report it and do not claim the Cypher predicate was exercised.

## 10. Re-baseline and results

- [ ] 10.1 `go run ./cmd/eval --update-baseline -seed 42` on amd64; `--ci` twice with a byte-identical check on arm C's precision and nDCG.
- [ ] 10.2 Capture the tables: `-seed 42 -seeds 1` on `agent`, on `stress`, and on `agent -lambda 0`. Write them to `design.md` under "Result at the floor" next to the D16 predictions, reporting any miss as it stands.

## 11. Docs

- [ ] 11.1 `README.md`: claim from D17, both tables, lambda and break-even, recall description, flags, archived M5 path, status and roadmap lines. PRD M6 status line, naming what remains open.
- [ ] 11.2 `graphify update .`, confirm nothing in `graphify-out/` is staged.

## 12. Verify, ship, archive

- [ ] 12.1 `go build ./...`, `go test -race ./...`, `golangci-lint run ./...`, `go run ./cmd/eval --ci`. Final whole-branch `reviewer` (opus). Then `/opsx:verify`.
- [ ] 12.2 `/opsx:continue` through finalize: push `feat/m6-polish` with its own upstream, open the one PR.
- [ ] 12.3 `/opsx:archive` on the same branch, sync specs, push. Only then tell Franco it is ready to merge.
