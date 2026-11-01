# M5 Eval Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `go run ./cmd/eval` measure arms A/B/C of decay over the shipped recall pipeline on a synthetic graph-structured dataset with virtual time, print a comparison + recommendation, and fail CI on an arm-C precision@k regression.

**Architecture:** The eval drives the real `mcp` handlers in process (exported as `Tools`) over a functional in-memory store in `mock/` that mirrors the Neo4j adapter, with a settable clock and the real `sweep.SweepOnce`. Only the `DecayModel` differs per arm. A seeded generator owns the dataset and the ground truth; the reranker is a cosine scorer over the same vectors so nothing judges itself.

**Tech Stack:** Go 1.26 stdlib only (`math/rand/v2` seeded PCG, `encoding/json`, `sort`, `sync`), existing module deps. No new dependencies.

**Spec:** `openspec/changes/m5-eval/design.md` (decisions D1–D10) and `openspec/changes/m5-eval/specs/*/spec.md` (requirements + scenarios). Read both before any task.

## Decisions recorded 2026-11-01

- Execution: subagent-driven (one fresh implementer per task, a fresh reviewer before the next task, whole-branch review at the end).
- If the arm C vs B signal at the floor is noisy, raise the floor; never pick seeds or tune constants to the result.
- Headline k = 5.

## Global Constraints

- Root package `engram` imports no infrastructure; `eval` may import `engram`, `mcp`, `mock`, `sweep`; `mock` must not import `mcp` or `sweep` in non-test files (import cycle: `mcp` tests import `mock`).
- No `time.Now()` in `eval/`, `mock/`, `decay.go`, or `mcp` handlers; the only wall-clock read is latency timing in `eval/arm.go` via `time.Since`.
- Every goroutine-touching type passes `go test -race ./...`. Tests use `t.Parallel()` where there is no shared state.
- Floor: 3 namespaces, 50 topics, 5,000 memories, 200 entities, 500 queries, 180 days, dim 64. Thresholds: dedup 0.95, auto-link 0.85, twin cosine [0.86, 0.94], in-topic [0.70, 0.94], cross-topic |cos| < 0.30, bridge-only cos(q, Y) < 0.30, hub fan-out < 20, neighbor cap 20, propagate cap 20, sweep grace 30 d, hard floor 0.02, k = 5, gate tolerance 0.02, tie 0.005, 4-decimal rounding, amd64-only baseline.
- Decay names are exactly `type-aware`, `uniform`, `none`.
- Errors wrap with `%w` and say what was being done; sentinel `engram.ErrNotFound` for unknown ids in `MemStore`.
- Commit after each task with a conventional message; never commit `graphify-out/`.

## Review Focus

1. A twin pair whose cosine lands at exactly 0.95 after normalisation rounding would be deduplicated and abort the run; Task 7's `TestNoDedupCollisions` asserts strict `< 0.95` over every in-namespace pair, not just twins.
2. A bridge query whose Y is in the hop's namespace but the query scope lists only the topic namespace silently loses Y; Task 7's `TestEntityBridgeScope` asserts the scope equals the union of both namespaces for every entity-bridge query.
3. `SweepOnce` with batch = `store.Len()` evaluated before the day's writes under-counts candidates; Task 9's `runArm` reads `Len()` immediately before each `SweepOnce` call and `TestArmBPrunesStaleStandard` asserts a standard last accessed on day 60 is gone by day 150 under B.
4. `--ci` on a machine without `eval/baseline.json` must say how to create it, not panic on a nil baseline; Task 11's `TestMainMissingBaseline` asserts exit 1 and the `--update-baseline` hint.
5. `Neighbors` returning the same neighbor twice through two shared entities must yield two rows (as Cypher does) and `blend` keeps the max; Task 3's `TestMemStoreNeighborsTwoEntities` asserts two rows with distinct `Via`.

---

### Task 1: Decay strategy names

**Files:**
- Modify: `decay.go` (append)
- Test: `decay_test.go` (append)

**Interfaces:**
- Produces: `const DecayTypeAware = "type-aware"`, `DecayUniform = "uniform"`, `DecayNone = "none"`; `func DecayByName(name string) (DecayModel, error)`; `func DecayName(d DecayModel) string` (returns `""` for an unknown implementation).

- [ ] **Step 1: Write the failing table test `TestDecayByName`** in `decay_test.go`: cases `type-aware`→`TypeAwareDecay{}`, `uniform`→`UniformDecay{}`, `none`→`NullDecay{}`, `fast`→error whose text contains all three names; plus `TestDecayNameRoundTrip` asserting `DecayName(m) == name` for the three valid names.
- [ ] **Step 2: Run** `go test -race -run 'TestDecayByName|TestDecayNameRoundTrip' .` Expected: FAIL, undefined `DecayByName`.
- [ ] **Step 3: Implement** the three consts, `DecayByName` (switch, error `fmt.Errorf("unknown decay %q: want %s, %s or %s", ...)`), and `DecayName` (type switch) in `decay.go`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(domain): DecayByName/DecayName for eval arms and ENGRAM_DECAY`.

### Task 2: Lookup embedder and cosine reranker

**Files:**
- Create: `mock/embedder.go`, `mock/embedder_test.go`

**Interfaces:**
- Produces: `func Cosine(a, b engram.Vector) float64` (0 when either norm is 0); `type LookupEmbedder struct{…}`; `func NewLookupEmbedder() *LookupEmbedder`; `func (e *LookupEmbedder) Register(content string, v engram.Vector)`; `func (e *LookupEmbedder) Embed(ctx context.Context, text string) (engram.Vector, error)` (error wraps the unknown text); `type CosineReranker struct{ Embedder *LookupEmbedder }`; `func (r CosineReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error)`.

- [ ] **Step 1: Write failing tests** `TestLookupEmbedderUnknown` (Embed of unregistered text returns error containing the text), `TestCosineRerankerAlignment` (register q, d1, d2 with known vectors; `Rerank(q, [d1, d2])` returns `[Cosine(q,d1), Cosine(q,d2)]`), `TestCosineZeroNorm` (returns 0).
- [ ] **Step 2: Run** `go test -race ./mock/ -run 'LookupEmbedder|CosineReranker|CosineZero'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** `mock/embedder.go`. Register copies the slice. `LookupEmbedder` needs no mutex (registered before use, read-only afterwards); say so in its doc comment.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat(mock): LookupEmbedder and CosineReranker for the eval`.

### Task 3: MemStore

**Files:**
- Create: `mock/memstore.go`, `mock/memstore_test.go`
- Create: `mock/memstore_conformance_test.go` with `package mock_test` (imports `mcp` and `sweep` to assert interface conformance without a cycle)

**Interfaces:**
- Produces: `type MemStore struct{…}` (RWMutex; `mems map[engram.MemoryID]*engram.Memory`; `order []engram.MemoryID`; `links map[engram.MemoryID]map[engram.MemoryID]float64` kept symmetric; `mentions map[engram.MemoryID]map[string]struct{}`; `byEntity map[string]map[engram.MemoryID]struct{}`); `func NewMemStore() *MemStore`; methods `Put`, `Search`, `Reinforce`, `LinkEntities`, `Link`, `Neighbors`, `PropagateReinforce`, `Supersede`, `SetForgotten`, `Pin`, `Delete`, `PruneCandidates` with the signatures of `mcp.Store`, the forget method set and `sweep.Pruner`; `func (s *MemStore) Len() int`, `EdgeCount() int`, `BridgeCount() int`, `Snapshot(id engram.MemoryID) (engram.Memory, bool)`. Constants `neighborCap = 20`, `propagateCap = 20` with a comment naming the Neo4j constants they mirror.
- Consumes: `Cosine` from Task 2.

- [ ] **Step 1: Write failing tests** (table-driven, `t.Parallel()`): `TestMemStoreSearchOrderAndScope` (m1 ns a 0.9, m2 ns b 0.95, m3 ns a 0.8; `Search([a], q, 10)` → `[m1, m3]`; `Search(nil, q, 1)` → `[m2]`; `Search([a], q, 0)` → 1 result), `TestMemStoreSearchIncludesForgotten`, `TestMemStoreNeighborsScope` (shared entity e, scope [a] → only n1 with `Via == "entity:e"`, `Weight == 1`), `TestMemStoreNeighborsSeedsExcluded`, `TestMemStoreNeighborsLinkCap` (25 links → 20 heaviest), `TestMemStoreNeighborsTwoEntities` (two rows, `Via` `entity:e1` and `entity:e2`), `TestMemStoreDeleteCascades` (EdgeCount −3, former neighbors lose m, BridgeCount drops), `TestMemStorePruneBoundary` (`LastAccessed == before` → not a candidate; one second earlier → candidate), `TestMemStorePrunePinned`, `TestMemStoreNotFound` (`SetForgotten`/`Pin`/`Delete` on unknown id → `errors.Is(err, engram.ErrNotFound)`), `TestMemStoreSupersedeIdempotent`, `TestMemStoreBridgeCount` (e mentioned by m1..m3 → 3), `TestMemStoreConcurrentSearchDelete` (goroutines under `-race`).
- [ ] **Step 2: Write** `memstore_conformance_test.go`: `var _ mcp.Store = (*mock.MemStore)(nil)`, `var _ sweep.Pruner = (*mock.MemStore)(nil)`, plus an interface literal with `SetForgotten/Pin/Delete/Supersede`.
- [ ] **Step 3: Run** `go test -race ./mock/`. Expected: FAIL to compile.
- [ ] **Step 4: Implement** `mock/memstore.go`. `Search`: compute cosine for each memory in scope, `sort.SliceStable` by score desc over a slice built in `order` sequence. `Neighbors`: for each seed, links sorted by weight desc then id, cap 20; bridges as (neighbor, entity) rows sorted by neighbor id then entity name, cap 20 rows; filter scope when `len(scope) > 0`. `Delete`: remove from `mems`, `order`, both sides of `links`, `mentions`, `byEntity`. `BridgeCount`: count unordered pairs sharing ≥ 1 entity (set of pair keys). `PruneCandidates`: iterate `order`, stop at `limit`.
- [ ] **Step 5: Run** `go test -race ./mock/`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(mock): MemStore, a functional in-memory store mirroring the Neo4j adapter`.

### Task 4: Export the handlers as `mcp.Tools`

**Files:**
- Modify: `mcp/remember.go`, `mcp/recall.go`, `mcp/forget.go`, `mcp/server.go`, every `mcp/*_test.go` that names `handlers`, `rememberInput`, `recallInput`, `forgetInput` or their outputs; `cmd/engramd/main.go:65-66`
- Test: `mcp/tools_test.go` (new)

**Interfaces:**
- Produces: `type Tools struct` (the former `handlers`); `type ServerStore interface { Store; forgetStore }` (the former `serverStore`, exported); `type Option func(*Tools)`; `func WithIDGenerator(fn func() (engram.MemoryID, error)) Option`; `func NewTools(embedder engram.Embedder, reranker engram.Reranker, decay engram.DecayModel, store ServerStore, clock engram.Clock, opts ...Option) *Tools` (defaults exactly as today's `NewServer` body); `func (t *Tools) Remember(ctx context.Context, in RememberInput) (RememberOutput, error)`, `Recall(ctx, RecallInput) (RecallOutput, error)`, `Forget(ctx, ForgetInput) (ForgetOutput, error)` delegating to the unchanged `doRemember/doRecall/doForget`; `func NewServer(t *Tools) *mcpsdk.Server`. Exported DTOs: `RememberInput`, `RememberOutput`, `RecallInput`, `RecallOutput`, `RecallHit` (was `recallResultDTO`), `Provenance` (was `provenanceDTO`), `ForgetInput`, `ForgetOutput`; JSON tags unchanged.

- [ ] **Step 1: Write failing tests** in `mcp/tools_test.go`: `TestWithIDGeneratorSequential` (`NewTools(..., WithIDGenerator(seq))` with a `FakeStore` whose `SearchResults` is empty; two `Remember` calls return `m000001`, `m000002`), `TestNewToolsDefaultID` (no option → 32 hex chars), `TestNewServerWrapsTools` (`NewServer(NewTools(...))` non-nil; keep the existing `TestNewServerWiresPorts` assertions).
- [ ] **Step 2: Run** `go test -race ./mcp/ -run 'WithIDGenerator|NewTools|NewServer'`. Expected: FAIL to compile.
- [ ] **Step 3: Rename** with `gofmt -r` or sed across `mcp/*.go`: `handlers`→`Tools`, `serverStore`→`ServerStore`, `rememberInput`→`RememberInput`, `rememberOutput`→`RememberOutput`, `recallInput`→`RecallInput`, `recallOutput`→`RecallOutput`, `recallResultDTO`→`RecallHit`, `provenanceDTO`→`Provenance`, `forgetInput`→`ForgetInput`, `forgetOutput`→`ForgetOutput`. Keep `do*` methods and `testHandlers` (now returning `*Tools`). Add `Option`, `WithIDGenerator`, `NewTools`, the three exported methods, and change `NewServer` to take `t *Tools` and register `t.Remember/Recall/Forget`. Add doc comments on every new exported symbol.
- [ ] **Step 4: Update** `cmd/engramd/main.go`: `tools := mcp.NewTools(embedder, reranker, decay, store, systemClock{})`; `srv := mcp.NewServer(tools)`.
- [ ] **Step 5: Run** `go build ./... && go test -race ./mcp/ ./cmd/...`. Expected: PASS; `go test -race -tags integration ./mcp/` compiles (`go vet -tags integration ./mcp/`).
- [ ] **Step 6: Dispatch the `architect` agent** on the diff of `mcp/server.go` + `mcp/remember.go` with the question "is `Tools` + `Option` the right export shape, or should the pipeline move out of `mcp`?"; apply blocking findings only.
- [ ] **Step 7: Commit** `refactor(mcp): export Tools (in-process entry) and WithIDGenerator; NewServer wraps Tools`.

### Task 5: `ENGRAM_DECAY` in engramd

**Files:**
- Modify: `cmd/engramd/main.go`
- Test: `cmd/engramd/main_test.go` (new)

**Interfaces:**
- Consumes: `engram.DecayByName`, `engram.DecayTypeAware` (Task 1).
- Produces: `func decayFromEnv(get func(string) string) (engram.DecayModel, string, error)` (empty/unset → `type-aware`).

- [ ] **Step 1: Write failing test** `TestDecayFromEnv`: cases unset→(`TypeAwareDecay{}`, `"type-aware"`, nil), `"uniform"`→`UniformDecay{}`, `"linear"`→error.
- [ ] **Step 2: Run** `go test -race ./cmd/engramd/`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** `decayFromEnv`; in `run()` call it before `eneo4j.New`, return `fmt.Errorf("decay config: %w", err)`, pass the model to `NewTools` and `sweep.New`, add `"decay", name` to the startup `slog.Info`.
- [ ] **Step 4: Run** `go test -race ./cmd/engramd/ && go build ./cmd/engramd`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(engramd): ENGRAM_DECAY selects the decay strategy (type-aware default)`.

### Task 6: Eval params and floor

**Files:**
- Create: `eval/doc.go`, `eval/params.go`, `eval/params_test.go`

**Interfaces:**
- Produces: `type Params struct { Seed int64; Namespaces, Topics, Memories, Entities, Queries, Days, Dim int }`; `func Floor() Params` (Seed 0, values from Global Constraints); `func (p Params) Scaled(n int) Params` (multiplies Topics, Memories, Entities, Queries; n < 1 treated as 1); `func (p Params) Validate() error` (first violated dimension named with its floor); `const HubFraction = 0.05`.

- [ ] **Step 1: Write failing tests** `TestParamsFloor` (Floor().Validate() nil; Memories 4,999 → error containing `memories` and `5000`), `TestParamsScale` (Scaled(2): Topics 100, Memories 10000, Entities 400, Queries 1000, Namespaces 3, Days 180, Dim 64).
- [ ] **Step 2: Run** `go test -race ./eval/`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** `eval/params.go` and a package doc in `eval/doc.go` stating the anti-circularity rule.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat(eval): Params with size floor and -scale`.

### Task 7: Dataset generator

**Files:**
- Create: `eval/dataset.go` (types + `Generate` orchestration + `Hash`), `eval/vectors.go`, `eval/graph.go`, `eval/schedule.go`, tests `eval/dataset_test.go`, `eval/vectors_test.go`, `eval/graph_test.go`, `eval/schedule_test.go`

**Interfaces:**
- Produces:
  - `type Kind int` (`KindStandard`, `KindFact`, `KindEpisode`); `type QueryClass int` (`QTopicCurrent`, `QFact`, `QEpisodeRecent`, `QEpisodeOld`, `QBridgeLink`, `QBridgeEntity`).
  - `type Memory struct { GID string; Topic int; Namespace engram.Namespace; Type engram.MemoryType; Kind Kind; Version int; Content string; Vec engram.Vector; Importance float64; Entities []string; Links []string; Supersedes string; WriteDay int; Twin string }` (`Content` = `GID`, e.g. `t017/std/v2`, `t017/fact/3`, `t017/ep/0412`).
  - `type Entity struct { Name string; Hub bool }`; `type Access struct { GID string; Day int }`; `type Query struct { ID string; Class QueryClass; Day int; Content string; Vec engram.Vector; Namespaces []engram.Namespace; Relevant []string }`.
  - `type Dataset struct { Params Params; Memories []Memory; Entities []Entity; Accesses []Access; Queries []Query; byGID map[string]int }`; `func Generate(p Params) (*Dataset, error)` (calls `Validate`); `func (d *Dataset) Hash() string` (sha256 over a canonical encoding); `func (d *Dataset) Lookup(gid string) (Memory, bool)`.
  - `vectors.go`: `const dimDefault = 64`, `sigmaInTopic`, `sigmaTwin`, `kindOffsetScale = 0.35` (frozen, builder tunes once); `func newRNG(seed int64) *rand.Rand` (`math/rand/v2` PCG); `func unit(r *rand.Rand, dim int) engram.Vector`; `func mix(centroid, offset engram.Vector, scale float64, noise engram.Vector, sigma float64) engram.Vector` (normalised).
  - Schedule constants from D6 (`standardWriteDays = [0,10]`, `laterVersionDays = [30,90]`, `standardAccessEvery = 7`, `standardAccessUntil = 60`, `topicCurrentQueryDays = [150,179]`, …).
- Consumes: `mock.Cosine`.

- [ ] **Step 1: Write failing tests** (all on `Floor()` with `Seed: 42` unless stated; share one generated dataset via `sync.Once` in the test file): `TestGenerateDeterministic` (two `Generate` calls → equal `Hash`; `Seed: 43` → different), `TestTopicComposition` (every topic: one chain of 1–3 standards, 2–4 facts, ≥1 episode, all one namespace; totals ≥ 5,000 memories, 500 queries), `TestNoDedupCollisions` (max in-namespace pairwise cosine < 0.95), `TestTwinRange` (every `Twin` pair in [0.86, 0.94]), `TestCrossTopicLow` (sample 2,000 cross-topic pairs, |cos| < 0.30), `TestHubSpansNamespaces` (each hub: ≥3 topics, ≥2 namespaces), `TestHubFanoutUnderCap` (no entity mentioned by ≥20 memories), `TestBridgeOnlyUnreachableByVector` (every bridge query: cos(q, Y) < 0.30 and Y is a link neighbor of X or shares an entity with X), `TestHalfBridgeHopsEpisodic`, `TestNoAccessBeforeWrite`, `TestQueryMix` (125/75/100/75/125 ± 2), `TestTopicCurrentPicksCurrentVersion` (construct expectation from the dataset: relevant == the version with the greatest `WriteDay <= query.Day`), `TestEntityBridgeScope` (Namespaces == {ns(X), ns(Y)}), `TestFactLinksCurrentStandard`.
- [ ] **Step 2: Run** `go test -race ./eval/`. Expected: FAIL to compile.
- [ ] **Step 3: Implement `vectors.go`**: per-topic centroid `unit`; per-kind offset `unit`; `mix`; twin = sibling episode's vector re-mixed with `sigmaTwin`. Start with `sigmaInTopic = 0.55`, `sigmaTwin = 0.20`; adjust until `TestNoDedupCollisions`, `TestTwinRange`, `TestCrossTopicLow` pass, then freeze.
- [ ] **Step 4: Implement `dataset.go`**: assign topics round-robin to namespaces; per topic choose chain length 1–3, fact count 2–4, episodes = remainder of the topic's share (`Memories / Topics`, last topic absorbs the remainder); GIDs as above; importance per kind; `Generate` order: memories → vectors → graph → schedule; `Hash` over GID, Type, Namespace, WriteDay, Relevant lists, and rounded vector bytes.
- [ ] **Step 5: Implement `graph.go`**: links per D5; entity pool `t%03d-e%d` names with 5% hubs; each memory 1–3 entities from its topic's local set, hubs added from a global set to ≥3 topics across ≥2 namespaces while tracking fan-out < 20; bridge pairs chosen so Y's topic ≠ X's topic (link bridges via the 2% cross-topic links, entity bridges via a hub), at least half of X episodic.
- [ ] **Step 6: Implement `schedule.go`**: day assignment per D6; accesses; queries with vectors built as `mix(target.Vec, nil, 0, noise, 0.15)`; relevant sets per D7; scope per class; shuffle queries by day with the seeded RNG.
- [ ] **Step 7: Run** `go test -race ./eval/`. Expected: PASS; generation of the floor completes in < 2 s (`go test -run TestGenerateDeterministic -v` prints elapsed).
- [ ] **Step 8: Commit** `feat(eval): seeded synthetic dataset with graph structure and ground truth`.

### Task 8: Clock and metrics

**Files:**
- Create: `eval/clock.go`, `eval/metrics.go`, `eval/metrics_test.go`

**Interfaces:**
- Produces: `type Clock struct{ mu sync.Mutex; t time.Time }`; `func (c *Clock) Set(t time.Time)`; `func (c *Clock) Now() time.Time`; `var Day0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)`; `func dayTime(d int) time.Time`.
  `func precisionAt(ret []string, rel map[string]bool, k int) float64`, `recallAt`, `ndcgAt`, `reciprocalRank` (same params); `type Stats struct{ Mean, Std float64 }`; `func aggregate(xs []float64) Stats` (sample std, 0 for n < 2); `func percentile(ds []time.Duration, p float64) time.Duration` (nearest-rank).

- [ ] **Step 1: Write failing tests** `TestMetricsKnownValues` (R=[a,x,b,y,z], G={a,b,c}, k=5 → P 0.4, R 0.6667 (±1e-4), RR 1, nDCG = (1+1/log2(4))/(1+1/log2(3)+1/log2(4)) ±1e-9), `TestMetricsEmpty` (all 0), `TestMetricsTruncateToK` (R longer than k ignores the tail), `TestAggregateStd` ([1,2,3] → mean 2, std 1), `TestPercentileNearestRank`, `TestClockSetNow`.
- [ ] **Step 2: Run** `go test -race ./eval/ -run 'Metrics|Aggregate|Percentile|Clock'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** both files.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat(eval): virtual clock and retrieval metrics`.

### Task 9: Arm runner

**Files:**
- Create: `eval/arm.go`, `eval/arm_test.go`

**Interfaces:**
- Consumes: `mcp.NewTools`, `mcp.WithIDGenerator`, `mcp.RememberInput{Content, Type, Namespace, Importance *float64, Entities, Links, Supersedes}`, `mcp.RecallInput{Query, Namespaces, K *int}`, `mcp.RecallOutput.Results[i].ID/Type/Provenance.RetrievedVia`; `mock.NewMemStore`, `mock.NewLookupEmbedder`, `mock.CosineReranker`; `sweep.New(store, decay, clock, interval, grace, floor, batch, log)`; Task 7 types; Task 8 metrics.
- Produces: `type Point struct{ Day, Size, Edges, Bridges int }`; `type ArmResult struct { Name string; Seed int64; Precision, Recall, NDCG, MRR float64; RecallByType map[engram.MemoryType]float64; BridgeRecall map[string]float64 /* "link","entity" */; ViaShare map[string]float64 /* "vector","link","entity" */; LatencyP50, LatencyP95 time.Duration; FinalSize, FinalEdges, FinalBridges int; Series []Point }`; `func runArm(ctx context.Context, name string, decay engram.DecayModel, ds *Dataset, k int) (ArmResult, error)`; `type dedupError struct{ GID, Existing string }` implementing `error`; `func sequentialIDs() func() (engram.MemoryID, error)` yielding `m000001`, ….

- [ ] **Step 1: Write failing tests** using an unexported `generateUnchecked(p Params)` (same as `Generate` without `Validate`; add it in Task 7's `dataset.go` if missing) with a tiny `Params{Namespaces: 2, Topics: 4, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 16, Seed: 7}`: `TestArmADoesNotPrune` (`FinalSize == len(ds.Memories)`), `TestReinforceThroughRecall` (after the first access day of a memory, `store.Snapshot(id).AccessCount >= 1` and `LastAccessed == dayTime(day)`; expose the store through an internal `armState` returned by an unexported `newArm`), `TestArmBPrunesStaleStandard` (under `UniformDecay` a standard with no access after day 60 is absent from the store on day 150), `TestDedupAborts` (register two identical vectors in a hand-built tiny dataset → `runArm` returns an error `errors.As(&dedupError{})`), `TestSeriesEveryTenDays` (18 points for 180 days).
- [ ] **Step 2: Run** `go test -race ./eval/ -run 'Arm|Reinforce|Dedup|Series'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement `arm.go`**: `newArm` builds store, embedder (register every memory `Content` → `Vec` and every query `Content` → `Vec`), reranker, clock, `mcp.NewTools(emb, rr, decay, store, clock, mcp.WithIDGenerator(sequentialIDs()))`, `sweep.New(store, decay, clock, time.Hour, 30*24*time.Hour, 0.02, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))` — rebuild the sweeper or pass batch via a one-line `SweepOnce` wrapper that calls `sweep.New` with `batch = store.Len()` each day (choose the wrapper). Day loop per D6; writes map GID→returned id; `Supersedes`/`Links` translated through that map; accesses call `Recall` with `K: &five`; queries call `Recall`, time with `time.Since`, score against `Relevant` translated to store ids, tally `RetrievedVia` prefix (`vector`, `link`, `entity:` → `entity`); after `SweepOnce`, if `day%10 == 9` append a `Point`.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat(eval): arm runner over mcp.Tools + MemStore with virtual time`.

### Task 10: Run, report, recommendation

**Files:**
- Create: `eval/run.go`, `eval/report.go`, `eval/report_test.go`

**Interfaces:**
- Produces: `type Results struct { K int; Params Params; Seeds []int64; Arms map[string][]ArmResult }` (keys `none`, `uniform`, `type-aware`, in that print order); `func Run(ctx context.Context, p Params, seeds []int64, k int) (*Results, error)` (sequential arms × seeds; each seed sets `p.Seed`); `type Summary struct { Precision, Recall, NDCG, MRR Stats; RecallByType map[engram.MemoryType]Stats; BridgeRecall, ViaShare map[string]Stats; LatencyP50, LatencyP95 time.Duration; FinalSize, FinalEdges, FinalBridges Stats }`; `func (r *Results) Summary(arm string) Summary`; `const TieNDCG = 0.005`; `func Recommend(r *Results) string`; `func WriteTable(w io.Writer, r *Results)`; `func WriteJSON(w io.Writer, r *Results) error`.
- Consumes: `engram.DecayByName`, Task 9.

- [ ] **Step 1: Write failing tests** `TestRecommendMaxNDCG`, `TestRecommendTieBreak` (B and C within 0.005, C smaller store → `type-aware`), `TestReportTableColumns` (table contains header cells `precision@5`, `recall@5`, `ndcg@5`, `mrr`, `procedural`, `semantic`, `episodic`, `bridge-link`, `bridge-entity`, `via-link`, `store`, `edges`, `bridges`, `p95` and a line starting `recommended: `), `TestWriteJSONRoundTrip`. Build `Results` by hand.
- [ ] **Step 2: Run** `go test -race ./eval/ -run 'Recommend|Report|WriteJSON'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** `run.go` and `report.go` (`text/tabwriter`; `mean±std` formatted `%.3f±%.3f`; recommendation line `recommended: %s (ENGRAM_DECAY=%s)`).
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Sanity run** `go run ./cmd/eval` is not wired yet; add a temporary `TestRunFloorSmoke` guarded by `testing.Short()` that runs `Run` at `Floor()` with one seed and logs the table; run `go test -race ./eval/ -run RunFloorSmoke -v`. Expected: completes < 60 s, C ≥ B on `procedural` recall and B/C store < A. If not, adjust `sigmaInTopic`/schedule constants once and record the final values in `design.md` D5/D6.
- [ ] **Step 6: Commit** `feat(eval): Run over arms x seeds, table report, recommendation`.

### Task 11: Baseline, CI gate, binary

**Files:**
- Create: `eval/baseline.go`, `eval/baseline_test.go`, `eval/baseline.json`
- Rewrite: `cmd/eval/main.go`; Create: `cmd/eval/main_test.go`

**Interfaces:**
- Produces: `type Baseline struct { K int; Seed int64; GOARCH string; Params Params; PrecisionAtK, NDCGAtK float64 }` (json tags snake_case); `const GateTolerance = 0.02`; `func NewBaseline(r *Results) Baseline` (arm `type-aware`, first seed, rounded 4 d.p.); `func LoadBaseline(path string) (Baseline, error)`; `func WriteBaseline(path string, b Baseline) error` (returns `ErrUnsupportedArch` unless `runtime.GOARCH == "amd64"`); `func CheckGate(r *Results, b Baseline, tol float64) error` (nil = pass; error text includes both numbers and tol); `var ErrUnsupportedArch`.
  `cmd/eval/main.go`: `func run(args []string, stdout, stderr io.Writer) int` with flags `--ci`, `--update-baseline`, `-seed` (default 42), `-seeds` (default 3; forced to 1 under `--ci`/`--update-baseline`), `-scale` (1), `-k` (5), `-days` (180), `-json path`, `-baseline path` (default `eval/baseline.json`); `main()` calls `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`.

- [ ] **Step 1: Write failing tests** `TestGateRegression` (0.55 vs 0.52 → error containing `0.5200`, `0.5500`, `0.02`), `TestGateWithinTolerance` (0.535 → nil), `TestGateNullThesisPasses` (B higher than C, C within tol → nil), `TestBaselineRound` (4 d.p.), `TestBaselineRefusesNonAmd64` (inject `goarch` via an unexported variable), `TestMainMissingBaseline` (`run([]string{"--ci","-baseline", tmp}, …)` → 1 and stderr contains `--update-baseline`), `TestMainCIForcesOneSeed`.
- [ ] **Step 2: Run** `go test -race ./eval/ ./cmd/eval/`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** `baseline.go` and `cmd/eval/main.go` (`--ci`: load → `Run` with baseline seed, `Floor().Scaled(scale)` must equal the floor or fail → table → `CheckGate` → exit 1 on error; `--update-baseline`: `Run` → `WriteBaseline`; plain: `Run` → table (+ JSON)).
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Generate the baseline** `go run ./cmd/eval --update-baseline -seed 42`, then `go run ./cmd/eval --ci > a.txt; go run ./cmd/eval --ci > b.txt; diff a.txt b.txt`. Expected: exit 0 both times, empty diff; delete `a.txt b.txt`.
- [ ] **Step 6: Commit** `feat(eval): baseline + --ci regression gate; cmd/eval flags`.

### Task 12: Verification, review, docs touch

**Files:**
- Modify: `README.md` (eval paragraph: gate is live, how to run, where the baseline lives; the full write-up stays M6), `openspec/changes/m5-eval/design.md` (record final σ/schedule constants if tuned)

- [ ] **Step 1: Run** `gofmt -l . ; go build ./... && go test -race ./... && golangci-lint run ./... && go run ./cmd/eval --ci`. Expected: no gofmt output, all pass, exit 0.
- [ ] **Step 2: Dispatch `reviewer`** on the whole diff vs `main` and `security-auditor` on `mcp/server.go`, `mcp/remember.go`, `cmd/engramd/main.go`, `cmd/eval/main.go`; fix blocking findings; re-run Step 1.
- [ ] **Step 3: Run** `graphify update .`; edit the README paragraph; commit `docs: eval gate live; README points at cmd/eval`.
- [ ] **Step 4: Fill** `openspec/changes/m5-eval/verify.md` via `/opsx:verify`.
