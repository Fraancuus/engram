# M5 — Eval harness design

> Status: brainstormed 2026-11-01. Goal (PRD §7, §9 M5): a mixed-type, graph-structured
> dataset; A/B/C conditions; precision/recall/nDCG/MRR plus type-stratified and graph
> metrics; a CI gate; a strategy recommendation surfaced to the operator. Builds on M4.

## Context

M0–M4 are merged. `cmd/eval/main.go` is a stub that parses `--ci` and exits 0; the CI job
already calls it. `eval/` holds only `.gitkeep`. The three `DecayModel` implementations the
arms need (`NullDecay`, `UniformDecay`, `TypeAwareDecay`) exist in `decay.go`. The retrieval
pipeline (`embed → Search → Neighbors → blend → scoreAndFilter → rerank → packBudget →
reinforce`) lives on the unexported `handlers` type in package `mcp`, with the clock and the
decay model already injected. `sweep.SweepOnce(ctx, now)` takes virtual time. The fakes in
`mock/` are canned stubs (programmed return values), not functional doubles.

Ports consumed by this change (all in `engram.go`): `Embedder`, `Reranker`, `DecayModel`,
`Clock`. Storage interfaces consumed: `mcp.Store`, `mcp.forgetStore`, `sweep.Pruner` (defined
at their consumers). This change adds one functional implementation of each in `mock/`, one
exported in-process entry to the `mcp` handlers, and the `eval` package + binary. It adds no
new port and no new infrastructure package.

Franco (owner) reads the result table in the README (M6) and decides the shipped strategy;
the eval recommends, it never switches at runtime.

## Goals / Non-Goals

**Goals:**
- Answer PRD §7 Q1 and Q2 with numbers: does decay+reinforcement beat a never-forgetting
  baseline from a leaner store, and does type-aware decay beat uniform decay?
- Show how each strategy affects graph expansion (link and entity-bridge reach) as nodes
  are pruned.
- Make `go run ./cmd/eval --ci` a real gate: deterministic, offline, seconds, fails on a
  precision@k regression of the shipped strategy.
- Print a recommendation and make the strategy an `engramd` option (`ENGRAM_DECAY`).
- Keep the thing measured equal to the thing shipped: the eval calls the same handler code
  the MCP server wraps.

**Non-Goals:**
- A public benchmark (LongMemEval/LoCoMo), real sidecar embeddings, or a Neo4j-backed eval
  run. All out of v1; the design leaves room for a later `-backend` flag.
- Runtime auto-tuning or per-namespace strategy selection (v2 list).
- `memory_stats`, the README architecture/eval write-up, demo recording (M6).
- Ablation arms (type-aware without supersession, reinforcement off). Not v1.
- Tuning decay constants; the eval reports, it does not search parameters.

## Decisions

### D1. Fully synthetic, vector-level dataset (over benchmark or sidecar-embedded text)
Memories are generated directly as unit vectors with structure, not as text that is later
embedded. Rationale: the variable under test is the `DecayModel`; all arms share identical
vectors, so embedding realism cannot change the A/B/C ordering, while ground-truth control
(types, supersession, access patterns, bridge-only relevance) is exactly what the thesis
needs. It also makes the CI gate offline and fast with no data files. Alternatives and why
not: see `brainstorm.md`. Cost: the README must state the generator's assumptions.

Content strings still exist (e.g. `topic-17/fact/v3`) because `remember` requires content,
the reranker receives content, and `packBudget` counts bytes; they double as the lookup key.

### D2. Drive the real handlers in process via an exported entry in `mcp`
Export the handler surface: `mcp.NewTools(embedder, reranker, decay, store, clock, opts
...Option) *Tools` with `Remember`, `Recall`, `Forget` methods and exported input/output
structs (`RememberInput`, `RecallInput`, `RecallOutput`, `ForgetInput`, …). `NewServer(t
*Tools)` becomes the thin MCP registration over it. One option exists: `WithIDGenerator(func()
(engram.MemoryID, error))`; production keeps `crypto/rand`, the eval injects a sequential
generator (`m000001`, …) so ids, and every id-ordered tie-break downstream, are
deterministic across runs. This is a rename/export of what exists; no new
package, no second pipeline. Rationale: "the reranker is part of the pipeline under test"
only holds if the eval runs the shipped pipeline. Alternative rejected: driving the MCP
server through an in-process SDK client (adds JSON round-trips and SDK plumbing to every
eval call for no measurement gain). Alternative deferred: moving the pipeline to its own
package (a larger refactor the architect agent may still recommend; not needed for M5).

The eval maps generator ids to the ids `Remember` returns. A `Deduped: true` reply is an
eval error (the generator guarantees no in-namespace pair at or above
`defaultDedupThreshold`), never silently remapped: dedup against different store contents
would make "the same memory" differ between arms.

### D3. One functional in-memory store in `mock/`, mirroring the Neo4j adapter
`mock.MemStore` implements `mcp.Store`, `mcp.forgetStore` (via the same method set), and
`sweep.Pruner`. Mutex-guarded (tests run with `-race`). Semantics mirror `neo4j/store.go`
one for one, because the eval's graph numbers are only meaningful if expansion behaves as
in production:
- `Search`: brute-force cosine over all memories in the namespaces (empty = all), top-k by
  score, no filtering of forgotten/superseded (the pipeline's `scoreAndFilter` does that,
  as with Neo4j).
- `Neighbors`: per seed, up to `neighborCapPerSeed` (20) link neighbors in either edge
  direction ordered by weight desc, plus up to 20 entity-bridge neighbors (shared entity,
  not self, not a seed) ordered by id; neighbors filtered by scope when scope is non-empty;
  `Via` is `"link"` or `"entity:<name>"`, bridge weight 1.0.
- `PruneCandidates(before, limit)`: `LastAccessed < before && !Pinned`, up to limit.
- `Delete`: removes the node, its link edges, and its entity mentions (so bridges and edges
  die with the node; that is the effect being measured).
- `Reinforce`, `PropagateReinforce` (cap 20, weight ≥ threshold, sets `LastAccessed`),
  `Link`, `LinkEntities`, `Supersede`, `SetForgotten`, `Pin` as in the adapter.
- Observability for the report: `Len()`, `EdgeCount()`, `BridgeCount()` (distinct
  memory–entity–memory pairs reachable), `Snapshot(id)`.

The existing programmable `FakeStore` stays for handler unit tests. `mock.LookupEmbedder`
(content → vector, error on unknown content) and `mock.CosineReranker` (cosine between the
looked-up query and doc vectors) complete the arm wiring. The reranker is identical across
arms and never sees labels.

### D4. Package layout
- `eval/` (package `eval`, importable): `dataset.go` (params, floor, generator),
  `schedule.go` (event stream), `run.go` (arm runner), `metrics.go`, `report.go`,
  `baseline.go`, `baseline.json`. Imports `engram`, `mcp`, `mock`, `sweep`. The root
  package still imports nothing; depguard is unaffected.
- `cmd/eval/main.go`: flags only (`--ci`, `--update-baseline`, `-seed`, `-seeds`, `-scale`,
  `-k`, `-days`, `-json <path>`), wires `eval.Run`, prints the report, sets the exit code.
- `decay.go`: add `DecayByName(name string) (DecayModel, error)` for `type-aware`,
  `uniform`, `none`. Used by `engramd` (`ENGRAM_DECAY`, default `type-aware`) and by the
  report's recommendation line. Domain-level because it is a pure mapping over domain types.
- `cmd/engramd/main.go`: read `ENGRAM_DECAY` through `DecayByName`; unknown value is a
  startup error, not a silent default.

### D5. Dataset shape and minimum size
Floor (CI runs exactly this; `eval.Params.Validate` rejects anything below it; `-scale n`
multiplies topics, memories, entities, queries by n):

| dimension | floor |
|---|---|
| namespaces | 3 |
| topics | 50 |
| memories | 5,000 |
| entities | 1,200 (5% hubs) |
| queries | 500 |
| virtual days | 180 |
| vector dim | 64 |

Per topic (assigned to one namespace): procedural standard** with 1–3 versions
(importance 0.7; each new version is written with `Supersedes: [previous]` in every arm,
which is the only path `remember` applies supersession on — it is procedural-only by
design, `mcp/remember.go`), 1–2 **semantic fact chains** of 2–4 versions (importance 0.5,
slowly aging: S₀ = 60 d under C, 14 d under B; versions are NOT superseded because `remember`
only supersedes procedural writes — implicit staleness is the realistic case and the only
place forgetting can buy precision: a never-forgetting store returns every version of a fact
while only the latest is right), and **episodic events** filling the remainder (importance
0.3–0.5). Vector of a
memory = normalize(topic centroid + 0.35·kind offset + σ·noise), with σ chosen so in-topic
cosine lands in roughly 0.75–0.93 and cross-topic cosine near 0. "Twin" episodes are
generated with a smaller σ so their cosine lands in [0.86, 0.94]: above `linkThreshold`
(0.85) to exercise auto-linking, below `defaultDedupThreshold` (0.95) so they are not
deduplicated. The builder tunes σ values once and freezes them as named constants.

Geometry notes settled during implementation (2026-11-01): topic centroids are an
orthonormal set whenever `Topics ≤ Dim` (true at the floor: 50 ≤ 64), so cross-topic cosine
is carried only by the kind-offset and noise components; with the frozen σ the all-pairs
maximum is ≈0.36–0.38 (seeds 42/1/7) and the sampled 2,000-pair bound `< 0.30` is what the
test asserts. The load-bearing bound is per bridge query, cos(query, Y) < 0.30, asserted
for every bridge query. Above the floor (large `-scale`) centroids are random unit vectors.
The entity floor is 1,200, not 200: 5,000 memories with 1–3 mentions each cannot fit under
a fan-out of 20 with fewer than ~1,160 entities, and the fan-out cap is what keeps
`neighborCapPerSeed` from truncating a bridge. `Params.Entities` is a minimum; the
generator grows the pool if the cap would otherwise be exceeded and the dataset reports the
actual count. A twin shares its sibling's noise so the pair's cosine is a fixed ≈0.90
(inside [0.86, 0.94]); the in-topic range [0.70, 0.94] and all geometric bounds are
asserted on the CI seed (42), with other seeds spot-checked, not guaranteed. Link-bridge Y
sits in X's namespace (those queries are scoped to one namespace); entity-bridge Y crosses
namespaces through a hub.

Interconnection, built on purpose:
- Explicit links on `remember`: standard vᵢ → vᵢ₊₁, every fact ↔ the current standard
  version, and 2% of episodes link to a random memory in another topic.
- Entities: every memory mentions 1–3 entities; hub entities (5%) are mentioned across
  topics and across namespaces, so entity bridges cross both.
- Bridge-only pairs: for bridge queries, a memory Y in another topic (often another
  namespace) is relevant but has cosine < 0.3 to the query; it is reachable only through a
  link or a shared entity from a hop node X that is near the query. Hop nodes are episodic
  in at least half the pairs, so decay can cut the path.

### D3b. Similarity scale (settled 2026-11-01, measured on Neo4j 5.26)
`db.index.vector.queryNodes` with `cosine` returns `(1 + cos) / 2`, not the raw cosine:
orthogonal vectors score 0.5. Every pipeline threshold is applied to that scale, so in raw
cosine the dedup point is 0.90 and the auto-link point is 0.70. `mock.MemStore.Search`
mirrors the normalized score; the generator's bounds are stated in raw cosine and mapped
through it: ordinary in-topic pairs [0.40, 0.69] (no auto-link), twins and chain versions
[0.72, 0.88] (auto-link, no dedup), dedup bound < 0.90 by construction, bridge-only
cos(query, Y) < 0.30. The quantized index also adds score noise (a few hundredths on dense
vectors; up to 0.5 on degenerate sparse ones), which the mock does not model. Product
finding for a follow-up change, not this one: the M1/M2 constants `defaultDedupThreshold`
0.95 and `linkThreshold` 0.85 were chosen as if they were raw cosines; in production they
mean cos 0.90 and cos 0.70.

### D3a. Reranker stage in the eval (settled 2026-11-01 after the first floor run)
With a cosine reranker, graph expansion can never surface a result: a bridge-only memory Y
has cos(query, Y) < 0.30 by construction, so the reranker sorts it below every in-topic
distractor and `via-link`/`via-entity` are 0 in all arms. A synthetic stand-in for the
cross-encoder would either leak labels or (cosine) nullify expansion. Ruling: the eval runs
the pipeline with the cross-encoder stage **disabled** by default (`mcp.WithoutRerank()`),
so the final order is the blend order (vector similarity + propagated link/bridge scores +
importance + retrievability), which is exactly the candidate selection that decay changes.
`-rerank cosine` re-enables `mock.CosineReranker` for comparison. The report states which
mode produced the table. Production still re-sorts the same candidate pool with the real
cross-encoder; the eval measures what that stage receives.

Accesses in the schedule call `Recall` with k = 1, not 5: an "access" models the agent
recalling that one memory. With k = 5 every access also reinforced four in-topic neighbours
and propagated to strong-edge neighbours, so ~5,000 accesses produced ~25,000
reinforcements and almost nothing ever decayed (store 4,831 of 5,000 under C at day 180).

Precision@5 has a low ceiling here because most queries have one relevant memory (max 0.2)
and bridge queries two (0.4); the table reports it because the PRD gate names it, but nDCG
and MRR carry the ranking signal. The smoke test at the floor is opt-in
(`ENGRAM_EVAL_SMOKE=1`) because it takes minutes under `-race`.

First floor result that forced this (2026-11-02, rerank none, seed 42): none nDCG .943 / store
5000; uniform .633 / 4873; type-aware .622 / 2967. With write-once facts the never-forgetting
arm had no distractor that could hurt it, so the PRD's Q1 premise was untestable; fact chains
with implicit staleness restore it. Entity bridges (`via-entity` = 0, bridge-entity recall
0.5 = hop node only) do not surface in the top 5 without the cross-encoder because the
blend scores them at sim × 0.5; that is reported as a finding, not tuned away.

### D6. Event schedule and virtual time
`eval.Clock` is a settable `engram.Clock`. One simulation day per loop iteration:
1. set clock to day start; 2. writes (`Remember` with entities/links/supersedes, identical in
all arms); 3. accesses (a `Recall` with the memory's own vector, k=1, which
reinforces through the real path); 4. queries (a `Recall` scored against truth, latency
timed); 5. `SweepOnce(now)` with grace 30 d, floor 0.02, batch = current store size (one
sweep per day stands in for engramd's 24 hourly ticks of 500); 6. record store size, edge
count, bridge count.

Schedule: standard v1 written days 0–10, accessed every ~7 days until day 60, then silent
until queried on days 150–180 (the window where uniform decay, S₀ = 14 d, forgets them);
later standard versions land at days 30–90 and inherit the access pattern. Facts are
written in days 0–60 and accessed 1–3 times in their first 30 days, then only queried. Episodes arrive uniformly; each gets 0–2
follow-up accesses within 3 days. Query mix: 25% topic-current (late), 15% fact (late, t+60..120),
20% episode-recent (t+1..3), 15% episode-old (t+60..120), 25% bridge (half link, half
entity). Queries are scoped to the topic's namespace, except entity-bridge queries which
pass both namespaces involved (the recall scoping rule requires it).

### D7. Ground truth
Topic-current: {current standard version}. Fact: {latest chain version written on or before
the query day}, queried from the chain centroid so all versions compete. Episode queries: {that
episode}, at any age. Bridge: {hop node X, bridge-only Y}. Superseded standard versions
are excluded from recall in every arm but only ever pruned by B and C; in-topic episodes
and older semantic facts are the live distractors. An old episode counts as relevant on
purpose: forgetting it is the price of a leaner store and the table must show it.

### D8. Metrics, aggregation, report
Per query: P@k, R@k, nDCG@k (binary gain), reciprocal rank; k = 5 by default (`-k`). Per
arm: means; recall stratified by relevant memory type; bridge recall split link/entity;
share of returned results by `retrieved_via`; latency p50/p95; final store size, edges,
bridges; a series every 10 days. Over seeds (`-seeds`, default 3 locally, 1 in CI): mean ±
std. Output: a plain table on stdout; `-json` writes everything.

Recommendation line: the arm with the highest mean nDCG@k; a tie within 0.005 goes to the
smaller final store. Printed as `recommended: type-aware (ENGRAM_DECAY=type-aware)`.

### D9. CI gate
`eval/baseline.json` records k, seed, params, and arm C's precision@k and nDCG@k.
`--ci`: run the floor size with the baseline's seed; fail (exit 1) if arm C precision@k is
below baseline − 0.02 absolute, or if the dataset is below the floor. Metrics are rounded to
4 decimals before comparison and the baseline is produced on amd64 (Go does not fuse
multiply-adds on amd64, so Windows and Linux CI agree bit for bit; arm64 is not a supported
baseline producer and the file records `GOARCH`). `--update-baseline`
rewrites the file; a PR that changes the baseline shows the number in the diff. The gate
compares C to its own past, never C to B or A: a null result is a finding, not a failure.

### D10. Arm semantics (fixed)
A: `NullDecay`, sweep runs but prunes nothing (R = 1). B: `UniformDecay`, sweep on. C:
`TypeAwareDecay`, sweep on. Supersession is applied identically in all three arms (Franco's
call, 2026-11-01: the arms differ by the decay curve and nothing else; the PRD's "C =
per-type curves + supersession" is read as C having both, not as the others lacking
supersession). Everything else (blend weights, soft threshold, rerank, token budget,
reinforcement) is the shipped default in all three. Under A a superseded standard version
is excluded by `scoreAndFilter` but never pruned, so A's store still grows without bound.

## Risks / Trade-offs

- [Synthetic geometry can be tuned to flatter C] → generator constants are frozen and
  named; every metric is reported, including episodic recall where B/C lose; multiple
  seeds; the README (M6) states the assumptions; the gate never asserts the thesis.
- [Floor too small to show the effect, or too slow for CI] → brute force at 5,000 × 64
  dims is ~15k searches per arm, seconds in Go; if the signal is noisy at the floor, raise
  the floor rather than add tricks.
- [MemStore drifts from the Neo4j adapter] → table tests pin the mirrored semantics
  (scoping, caps, ordering, prune predicate, delete cascades); the design lists them
  one for one; a future `-backend neo4j` flag can run the same harness against the real
  store under the `integration` build tag.
- [Exporting `mcp` handlers widens the package API] → it is the same surface the server
  already exposes over stdio; the architect agent reviews the shape before apply.
- [Dedup collapses intended memories] → generator keeps all in-topic cosines below 0.95
  except never; a generator test asserts the maximum pairwise cosine within a namespace.
- [Bridge recall conflates "pruned hop" with "cap reached"] → hub fan-out stays below
  `neighborCapPerSeed` in the generator so caps do not bind; the report notes it.

## Migration Plan

No data migration. `engramd` gains an env var with the current behaviour as default. The
CI job line is unchanged (`go run ./cmd/eval --ci`); it starts enforcing once
`eval/baseline.json` lands in the same PR. Rollback: revert the PR; the stub behaviour
returns.

## Open Questions

None. Franco settled the last two on 2026-11-01: supersession runs in all arms; the D5
floor stands.

Resolved after the builder check (2026-11-01): supersession chains are procedural, not
semantic, because `remember` only supersedes on procedural writes and that matches the PRD;
ids are injected and sequential in the eval; `Deduped: true` is an eval error; baseline
floats are rounded and amd64-only; sweep cadence stays daily with batch = store size.

## Result at the floor (2026-11-02)

Final frozen generator constants (`eval/vectors.go`, `eval/schedule.go`):

| constant | value |
|---|---|
| `sigmaInTopic` | 0.42 |
| `sigmaTwin` | 3.6 |
| `sigmaFactVersion` | 0.33 |
| `kindOffsetScale` | 0.35 |
| `maxInTopicCos` | 0.94 |
| `minInTopicCos` | 0.70 |
| `maxNoiseTries` | 2000 |
| `maxChainTries` | 20 |
| `queryNoise` | 0.15 |

Floor run (`go run ./cmd/eval --ci`, seed 42, k=5, rerank none):

```
rerank: none
arm         precision@5  recall@5     ndcg@5       mrr          procedural   semantic     episodic     bridge-link  bridge-entity  via-vector   via-link     via-entity   store   edges    bridges  p50         p95
none        0.225±0.000  0.937±0.000  0.897±0.000  0.927±0.000  1.000±0.000  1.000±0.000  0.895±0.000  1.000±0.000  0.500±0.000    0.771±0.000  0.229±0.000  0.000±0.000  5000±0  23081±0  36447±0  1.6±0.0 ms  6.9±0.0 ms
uniform     0.173±0.000  0.724±0.000  0.677±0.000  0.704±0.000  0.800±0.000  0.605±0.000  0.728±0.000  0.774±0.000  0.365±0.000    0.846±0.000  0.154±0.000  0.000±0.000  4889±0  22530±0  34744±0  1.7±0.0 ms  7.6±0.0 ms
type-aware  0.154±0.000  0.682±0.000  0.587±0.000  0.582±0.000  1.000±0.000  1.000±0.000  0.463±0.000  0.516±0.000  0.206±0.000    0.860±0.000  0.140±0.000  0.000±0.000  2991±0  10658±0  13371±0  0.0±0.0 ms  5.8±0.0 ms

by query class (ndcg / mrr)
arm         topic-current  fact         episode-recent  episode-old  bridge-link  bridge-entity
none        1.000/1.000    0.648/0.531  0.996/0.995     0.995/0.993  1.000/1.000  0.610/0.992
uniform     0.767/0.756    0.352/0.274  0.993/0.990     0.573/0.555  0.771/0.881  0.416/0.661
type-aware  1.000/1.000    0.662/0.548  0.600/0.540     0.199/0.186  0.499/0.609  0.206/0.310

recommended: none (ENGRAM_DECAY=none)
```

Baseline (`eval/baseline.json`): k 5, seed 42, goarch amd64, rerank none, precision_at_k 0.1544, ndcg_at_k 0.5868 (arm `type-aware`).

Findings:

- Under TypeAwareDecay, 1-3-day-old episodes drop out of the top 5 because the blend adds w_ret*R with w_ret 0.5. This is an M4 blend property, not tuned here.
- Semantic fact versions never decay within 180 d at the shipped constants, so implicit staleness is not cleared.
- Entity bridges never surface without the cross-encoder (blend sim x 0.5).
