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
design, `mcp/remember.go`), 2–4 write-once **semantic facts** (importance 0.5, slowly aging: S₀ =
60 d under C, 14 d under B), and **episodic events** filling the remainder (importance
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

### D6. Event schedule and virtual time
`eval.Clock` is a settable `engram.Clock`. One simulation day per loop iteration:
1. set clock to day start; 2. writes (`Remember` with entities/links/supersedes, identical in
all arms); 3. accesses (a `Recall` with the memory's own vector, k=5, which
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
Topic-current: {current standard version}. Fact: {that fact}. Episode queries: {that
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
