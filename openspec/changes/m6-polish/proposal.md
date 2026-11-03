## Why

The first M5 floor run recommended `none`, and the diagnosis found faults on both sides.
Retrievability acts as a 0.5 ranking penalty on live memories, stale facts never clear, and link
thresholds sit on the wrong similarity scale. The eval also hides its query mix and gives store
size no weight. M6 must fix these before the README can tell the eval story, because that story
is the credibility of the project. Today the only supportable sentence is that type-aware
forgetting is store governance with a known per-class cost.

## What Changes

Delivers the eval-correction and README parts of milestone M6 (PRD section 9).

**Retrievability in recall**
- From: score = sim + 0.3 importance + 0.5 R, with dead candidates taking pool slots.
- To: R filters candidates before the pool is cut and adds at most 0.01 to the score (`wRet` 0.01).
- Reason: forgetting was never meant to penalise live memories.
- Impact: non-breaking; `score` values in recall output shift down by up to 0.49.

**Supersession**
- From: `Supersedes` applies to procedural writes only; dedup can swallow a replacement; recalling a chain-mate refreshes a superseded version.
- To: `Supersedes` applies to procedural and semantic writes; dedup ignores memories already superseded or named in `Supersedes`; `PropagateReinforce` skips superseded targets.
- Reason: stale facts never clear today, and a near-identical correction is silently lost.
- Impact: additive for callers; the `supersedes` field keeps its shape and widens its meaning.

**Similarity thresholds**
- From: `linkThreshold` and `propagateThreshold` 0.85 (raw cosine 0.70).
- To: both 0.925 on the normalised scale (raw cosine 0.85). `defaultDedupThreshold` stays 0.95 and its comment names the scale.
- Reason: M1 and M2 set them as if the index returned raw cosine.
- Impact: new writes create fewer auto-links; existing edges keep their stored weight.

**Eval instrument**
- From: one hidden query mix; recommendation by nDCG alone.
- To: `-mix agent|stress` (agent is primary); score = nDCG - lambda * (store / baseline store), lambda 0.10, overridable with `-lambda`; the report prints mix, lambda, a score column and the break-even lambda; the recommendation names its mix.
- Reason: the mix decided the M5 winner, and the benefit side of the thesis had no weight.
- Impact: `eval/baseline.json` gains `params.mix` and is regenerated.

**Generator**
- Fact versions carry `Supersedes`; the twin band moves to raw cosine [0.86, 0.89] so twins still auto-link.

**Specs and README**
- Requirements added for the access hit-rate table and the stale-baseline guard.
- README claim, results and recall description rewritten to what the new tables support.

**Effect on the A/B/C comparison.** Expected: episode-recent and episode access hit rate under
type-aware rise to near `none`; fact nDCG rises in every arm, and `none` and `type-aware` converge
near 1.0, so no class remains where forgetting beats `none` on retrieval; C's store grows from
reinforcement reaching episodes and shrinks from pruning superseded versions. The store weight
then decides A versus C, which is why lambda and the mix are fixed in `design.md` before any M6 run.

**Effect on the CI gate.** The gate logic is unchanged: arm C against its own past, never against
A or B, tolerance 0.02. The baseline moves to the agent mix, and the guard treats a mix mismatch as
stale. Numbers are expected to change, so the gate is red from the first ranking change until the
re-baseline task.

### Out of scope

- PRD M6 items left for a follow-up change: `memory_stats`, the demo recording.
- Changing the `ENGRAM_DECAY` default; Franco decides after reading the tables.
- From the correction note, not in the launch scope: the `-rerank cosine` secondary table and sweep cost as a report column.
- Semantic staleness detection by similarity (v2).
- v2 list: consolidation, per-namespace behaviour, web UI, GPU/NPU, LLM extraction.

## Capabilities

### New Capabilities
- `recall-ranking`: retrievability as a pre-pool candidate filter and a bounded tiebreak.
- `memory-supersession`: `Supersedes` on semantic writes, dedup bypass, no refresh of superseded targets.
- `similarity-thresholds`: dedup, auto-link and propagation thresholds stated on the normalised scale.

### Modified Capabilities
- `eval-dataset`: fact chains carry `Supersedes`, named query mixes, the twin band on the new link point.
- `eval-harness`: recommendation score with lambda, report contents, access hit-rate table, accesses at k=1.
- `eval-ci-gate`: the baseline records rerank and mix; stale-baseline guard requirement.
- `memory-store-fake`: `PropagateReinforce` skips superseded targets.

## Impact

- Code: `mcp/recall.go`, `mcp/remember.go`, `mock/memstore.go`, `neo4j/store.go`, `eval/` (params, dataset, schedule, vectors, report, run, baseline), `cmd/eval/main.go`, and their tests.
- Data: `eval/baseline.json` regenerated. No schema change and no Neo4j migration: edges already stored below 0.925 stay usable for expansion and stop propagating freshness.
- MCP surface: no new field. The `supersedes` description widens, and `score` magnitudes shift.
- Docs: `README.md`, PRD M6 status line.
