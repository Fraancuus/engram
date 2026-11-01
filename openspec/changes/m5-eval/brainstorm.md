## Design Summary

M5 is the eval harness (PRD §7, §9): one binary, `go run ./cmd/eval`, that runs the shipped
recall pipeline under three decay strategies (A `NullDecay`, B `UniformDecay`, C
`TypeAwareDecay` + supersession) over a synthetic, graph-structured, labelled dataset with
virtual time, then prints a comparison table, names a recommended strategy, and in `--ci`
mode fails the build when arm C's precision@k regresses against a committed baseline.
The recommended strategy becomes a runtime option on `engramd` (`ENGRAM_DECAY`), never a
runtime auto-switch. Full design: `design.md`.

Brainstormed in chat on 2026-11-01 (Franco + Claude). The conversation settled the headline
table, the recommendation semantics, the dataset source, a minimum dataset size, and a
deliberate interconnection strategy so graph expansion is measured per arm.

## Alternatives Considered

### Alternative A: Public benchmark (LongMemEval / LoCoMo) + real sidecar
- **Approach**: load a public long-term-memory benchmark, embed with the TEI sidecar, run
  the three arms against Neo4j, judge against the benchmark's labels.
- **Pros**: external credibility; real embeddings; exercises the real adapters.
- **Cons**: CI cannot run it (needs Neo4j + sidecar + model download); no control over
  types, access patterns, supersession, or graph structure, which is what the thesis is
  about; large data files in the repo; label semantics do not include "superseded".
- **Why not chosen**: the thesis is about *time and type*, not embedding quality, and the
  CI gate must run offline in seconds. Kept as a possible later flag, not v1.

### Alternative B: Synthetic text + real sidecar locally, canned vectors in CI
- **Approach**: generate text memories, embed them once with the sidecar, commit the
  vectors, run the eval over committed vectors.
- **Pros**: realistic embedding geometry; still deterministic in CI.
- **Cons**: committed vector files (MBs) that drift from the generator; two code paths
  (fresh vs. cached); anyone changing the generator needs the sidecar.
- **Why not chosen**: the extra moving part buys realism the thesis does not depend on;
  all three arms share identical vectors either way.

### Alternative C: Fully synthetic vectors with topic structure (chosen)
- **Approach**: a seeded generator produces memories as vectors (topic centroid + kind
  offset + noise) with types, namespaces, entities, explicit links, supersession chains,
  an event schedule over ~180 virtual days, and ground-truth labels per query. The embedder
  is a lookup table and the reranker scores by cosine, so neither can judge itself.
- **Pros**: full ground-truth control (incl. bridge-only relevance, supersession, type
  mix); offline; seconds in CI; no data files; multiple seeds for mean ± std.
- **Cons**: synthetic geometry; the README must state generator assumptions plainly, and
  the effect size depends on generator parameters. Mitigation: fixed defaults, all metrics
  reported including the ones where C loses (episodic recall), null result allowed.
- **Why chosen**: isolates the variable under test (DecayModel) and makes the CI gate real.

## Agreed Approach

Alternative C. The eval drives the real `remember`/`recall`/`forget` handlers in process
(exported entry in package `mcp`), over an in-memory functional store in `mock/` that
mirrors the Neo4j adapter's search, neighbor, and prune semantics, with an injected
virtual clock and the real `sweep.SweepOnce`. Only the `DecayModel` differs between arms.

## Key Decisions

1. **Headline table** (per arm): precision@k, recall@k, nDCG@k, MRR, type-stratified
   recall (procedural / semantic / episodic), bridge-only recall (link / entity), share
   of results by `retrieved_via`, final store size, surviving edges + entity bridges,
   recall latency p50/p95. Mean ± std over seeds. k = 5.
2. **Recommendation, not auto-tuning**: the report names the arm with the highest mean
   nDCG@k (tie within 0.005 → smaller final store) and prints the `ENGRAM_DECAY` value to
   set. `engramd` reads `ENGRAM_DECAY` = `type-aware` (default) | `uniform` | `none`.
3. **Arm semantics**: A = `NullDecay`, sweep prunes nothing. B = `UniformDecay`, sweep on.
   C = `TypeAwareDecay`, sweep on. Supersession (a new procedural standard version replaces
   the previous one, the only path `remember` supersedes on) runs in all three arms, so the
   arms differ by the decay curve only (Franco, 2026-11-01).
4. **Ground truth**: only the current version of a topic's procedural standard is relevant to
   its topic query; a write-once semantic fact is relevant to its own query at any age; an episodic event is relevant to a query
   about it at any age (forgetting it is a measured cost, not hidden); bridge-only
   relevant items are reachable only through one hop (link or entity).
5. **Minimum size** (CI runs exactly this; `-scale` multiplies for local runs): 3
   namespaces, 50 topics, 5,000 memories, 200 entities, 500 queries, 180 virtual days,
   vector dim 128 (raised from 64 on 2026-11-01, design D3b).
6. **Interconnection**: explicit links (standard version chains, fact↔standard, sparse cross-topic),
   auto-links triggered on purpose (twin episodes above the 0.85 threshold, below dedup),
   hub entities spanning topics and namespaces, and queries whose relevant set includes a
   bridge-only memory whose hop node is often episodic.
7. **CI gate**: fixed seed, `eval/baseline.json`, arm C precision@k must not drop more than
   0.02 absolute; `--update-baseline` rewrites it. The gate never compares arms (a null
   result does not fail the build).
8. **Anti-circularity**: labels come from the generator; the reranker is a cosine scorer
   over the same lookup vectors and identical across arms.

## Open Questions

None open. Settled 2026-11-01: supersession in all arms; floor as in decision 5; sweep
cadence one `SweepOnce` per virtual day with batch = store size (engramd runs hourly with
batch 500). An ablation arm (type-aware without supersession) stays out of v1.
