## 1. Domain: decay strategy names (`decay.go`)

- [ ] 1.1 Add `DecayByName(name) (DecayModel, error)` and `DecayName(DecayModel) string` for `type-aware`/`uniform`/`none`; table test `TestDecayByName` (valid, invalid lists names, round trip) in `decay_test.go`, run `go test -race .`

## 2. Mock: functional doubles (`mock/`)

- [ ] 2.1 `mock/embedder.go`: `LookupEmbedder` (register, Embed, unknown → error) and `CosineReranker`; tests `TestLookupEmbedderUnknown`, `TestCosineRerankerAlignment`
- [ ] 2.2 `mock/memstore.go`: `MemStore` struct, mutex, `Put`, `Search` (cosine, namespace filter, score desc + insertion tie-break, k<1 → 1); tests `TestMemStoreSearchOrderAndScope`, `TestMemStoreSearchIncludesForgotten`
- [ ] 2.3 `MemStore.Link`, `LinkEntities`, `Neighbors` mirroring `neo4j/store.go` (cap 20 per kind, scope filter, seeds excluded, link weight desc, bridges by id, `Via` strings); tests `TestMemStoreNeighborsScope`, `TestMemStoreNeighborsSeedsExcluded`, `TestMemStoreNeighborsLinkCap`
- [ ] 2.4 `MemStore.Reinforce`, `PropagateReinforce` (cap 20, weight ≥ thr), `Supersede` (idempotent), `SetForgotten`/`Pin`/`Delete` (`engram.ErrNotFound`, delete cascades edges + mentions), `PruneCandidates` (`LastAccessed < before && !Pinned`, limit); tests `TestMemStoreDeleteCascades`, `TestMemStorePruneBoundary`, `TestMemStorePrunePinned`
- [ ] 2.5 `MemStore.Len`, `EdgeCount`, `BridgeCount`, `Snapshot(id)`; test `TestMemStoreBridgeCount`; compile-time asserts `var _ mcp.Store`, `var _ sweep.Pruner` in a test file to avoid an import cycle in `mock`; race test `TestMemStoreConcurrentSearchDelete`

## 3. MCP: exported in-process entry (`mcp/`)

- [ ] 3.1 Rename `handlers` → `Tools`; export `RememberInput/Output`, `RecallInput/Output`, `RecallResultDTO`, `ProvenanceDTO`, `ForgetInput/Output`; methods `Remember`, `Recall`, `Forget` delegating to the existing `do*`; add `Option` + `WithIDGenerator`; `NewTools(embedder, reranker, decay, store serverStore, clock, opts...)`; `NewServer(t *Tools)`; update all `mcp` tests and `cmd/engramd/main.go`; `go build ./... && go test -race ./mcp/...`
- [ ] 3.2 Tests `TestWithIDGeneratorSequential`, `TestNewServerWrapsTools` (server recall == Tools.Recall for one input via in-process SDK client already used in tests, or skip if none exists and note it)
- [ ] 3.3 Run the `architect` agent on the export shape; resolve blocking findings

## 4. engramd: `ENGRAM_DECAY` (`cmd/engramd/main.go`)

- [ ] 4.1 Read `ENGRAM_DECAY` (default `type-aware`) through `DecayByName` before `eneo4j.New`; pass the model to `NewTools` and `sweep.New`; log `decay=<name>`; unit test `TestDecayFromEnv` on a small helper (`decayFromEnv(getenv) (DecayModel, string, error)`)

## 5. Eval package: dataset (`eval/`)

- [ ] 5.1 `eval/params.go`: `Params` (Seed, Namespaces, Topics, Memories, Entities, Queries, Days, Dim, Scale), floor constants, `Validate`, `Scaled`; tests `TestParamsFloor`, `TestParamsScale`
- [ ] 5.2 `eval/dataset.go`: types (`Memory`, `Entity`, `Query`, `Dataset`), `Generate(params)`: topics → namespaces, standard chains (1–3 versions, Supersedes), 2–4 facts, episodes to budget, importance per kind; `Hash()`; tests `TestGenerateDeterministic`, `TestTopicComposition`
- [ ] 5.3 `eval/vectors.go`: seeded `*rand.Rand`, centroids, kind offsets, frozen σ constants, `normalize`, `cosine`, twin generation; tests `TestNoDedupCollisions` (max in-namespace cosine < 0.95), `TestTwinRange` ([0.86, 0.94]), `TestCrossTopicLow` (|cos| < 0.30)
- [ ] 5.4 `eval/graph.go`: explicit links (version chain, fact→current standard, 2% cross-topic), entity mentions 1–3, hubs (5%, ≥3 topics, ≥2 namespaces, fan-out < 20), bridge-only pairs (cos(q,Y) < 0.30, ≥50% episodic hops); tests `TestHubSpansNamespaces`, `TestHubFanoutUnderCap`, `TestBridgeOnlyUnreachableByVector`
- [ ] 5.5 `eval/schedule.go`: day-indexed events (write/access/query) per D6 windows and query mix 125/75/100/75/125 ± 2; ground truth + namespace scope per query class; tests `TestNoAccessBeforeWrite`, `TestQueryMix`, `TestTopicCurrentPicksCurrentVersion`, `TestEntityBridgeScope`

## 6. Eval package: harness, metrics, report (`eval/`)

- [ ] 6.1 `eval/clock.go`: settable `Clock` (`Set`, `Now`); `eval/metrics.go`: `precisionAt`, `recallAt`, `ndcgAt`, `reciprocalRank`, `aggregate` (mean, sample std), `p50/p95`; tests `TestMetricsKnownValues`, `TestMetricsEmpty`, `TestAggregateStd`
- [ ] 6.2 `eval/arm.go`: `newArm(decay, dataset, k)` wiring MemStore + LookupEmbedder + CosineReranker + sequential ids + `sweep.New(store, decay, clock, 0, 30d, 0.02, store.Len(), logger)`; `runArm(ctx)` day loop (set clock, writes, accesses, queries, SweepOnce, series); dedup → error; tests `TestArmADoesNotPrune`, `TestReinforceThroughRecall`, `TestDedupAborts` (tiny params via unexported constructor bypassing the floor)
- [ ] 6.3 `eval/run.go`: `Run(ctx, Params, seeds, k) (Results, error)` over arms × seeds; `eval/report.go`: text table, `recommend(results)` (max nDCG, tie 0.005 → smaller store), JSON writer; tests `TestRecommendTieBreak`, `TestReportTableColumns`
- [ ] 6.4 Sanity run at the floor locally (`go run ./cmd/eval -seeds 3`); if arm C vs B shows no signal, tune σ/schedule constants once, record the change in `design.md` D5/D6; keep runtime under 60 s for 3 arms × 1 seed

## 7. CI gate and binary (`eval/baseline.go`, `cmd/eval/main.go`)

- [ ] 7.1 `eval/baseline.go`: `Baseline` struct, `LoadBaseline`, `WriteBaseline` (4-decimal rounding, `runtime.GOARCH`, amd64-only write), `CheckGate(results, baseline, tol)`; tests `TestGateRegression`, `TestGateWithinTolerance`, `TestGateNullThesisPasses`, `TestBaselineRefusesNonAmd64`
- [ ] 7.2 `cmd/eval/main.go`: flags `--ci`, `--update-baseline`, `-seed`, `-seeds`, `-scale`, `-k`, `-days`, `-json`; exit codes; missing baseline message; `go vet`-clean
- [ ] 7.3 Generate `eval/baseline.json` with `--update-baseline -seed 42` on amd64; run `--ci` twice and diff outputs (determinism)

## 8. Verification and review

- [ ] 8.1 `gofmt -l .` empty, `go build ./...`, `go test -race ./...`, `golangci-lint run ./...`, `go run ./cmd/eval --ci` exit 0
- [ ] 8.2 Run `reviewer` on all Go changes and `security-auditor` on `mcp/` export surface + `cmd/engramd` env parsing; fix blocking findings
- [ ] 8.3 `graphify update .`; update `README.md` eval paragraph only to say the gate is live (full write-up is M6); note any σ/schedule tuning in `design.md`
