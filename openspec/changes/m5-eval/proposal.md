## Why

Engram's differentiator is a claim: type-aware forgetting retrieves more relevant memories
from a leaner store than never forgetting, and beats one-curve-for-all decay. M0–M4 built
the mechanism; nothing measures it. `cmd/eval` is a stub that exits 0, so the CI "eval gate"
enforces nothing and the README's eval section is a promise. M5 (PRD §9) turns the claim
into numbers, makes the gate real, and tells the operator which strategy to run.

## What Changes

- **Delivers milestone M5** (PRD §7, §9). Out of scope: `memory_stats`, the README
  architecture + eval write-up and demo (M6); a public benchmark or sidecar/Neo4j-backed
  run; runtime auto-tuning or per-namespace strategy (v2); ablation arms; tuning decay
  constants.
- **Eval binary becomes real.** `go run ./cmd/eval` generates a seeded synthetic,
  graph-structured dataset at or above a fixed floor (3 namespaces, 50 topics, 5,000
  memories, 200 entities, 500 queries, 180 virtual days), runs arms A/B/C over the shipped
  handlers with a virtual clock and the real sweep, and prints precision@k, recall@k,
  nDCG@k, MRR, type-stratified recall, bridge-only recall (link/entity), `retrieved_via`
  share, store size, surviving edges/bridges, latency, mean ± std over seeds, plus a
  recommendation line.
- **CI gate.** `--ci` compares arm C precision@k against `eval/baseline.json` and fails on
  a drop beyond 0.02 absolute. `--update-baseline` rewrites it.
- **Strategy becomes an option.**
  - From: `engramd` hard-wires `TypeAwareDecay`.
  - To: `engramd` reads `ENGRAM_DECAY` (`type-aware` default, `uniform`, `none`) through a
    new `engram.DecayByName`; an unknown value fails startup.
  - Reason: the eval recommends a strategy; the operator must be able to pick it.
  - Impact: non-breaking (default unchanged).
- **In-process entry to the pipeline.**
  - From: handlers are unexported; only the MCP server reaches them.
  - To: `mcp.NewTools(...)` returns an exported `*Tools` with `Remember`/`Recall`/`Forget`
    and exported input/output structs; `NewServer` wraps it; one option
    `WithIDGenerator` for deterministic ids.
  - Reason: the eval must measure the shipped pipeline, not a copy.
  - Impact: `NewServer` signature changes; callers are `cmd/engramd` and tests. Non-breaking
    over the wire.
- **Functional in-memory store in `mock/`** (`MemStore`) mirroring the Neo4j adapter's
  search, neighbor, prune, delete-cascade semantics, plus `LookupEmbedder` and
  `CosineReranker`. The programmable `FakeStore` stays.

## Capabilities

### New Capabilities
- `eval-dataset`: seeded synthetic dataset with a size floor, typed memories, procedural
  version chains with supersession, deliberate interconnection (explicit links, auto-link
  twins, hub entities, bridge-only pairs), an event schedule over virtual days, and
  generator-owned ground-truth labels.
- `eval-harness`: runs arms A/B/C over `mcp.Tools` + `mock.MemStore` + `sweep.SweepOnce`
  with a virtual clock; computes per-query and aggregate metrics; prints the table and the
  recommendation; writes JSON on request.
- `eval-ci-gate`: baseline file, `--ci` regression check, `--update-baseline`, exit codes.
- `memory-store-fake`: `mock.MemStore` semantics (one for one with `neo4j/store.go`),
  `mock.LookupEmbedder`, `mock.CosineReranker`.
- `tools-entry`: exported in-process handler API in package `mcp` and the id-generator
  option.
- `decay-strategy-option`: `engram.DecayByName` and `ENGRAM_DECAY` in `engramd`.

### Modified Capabilities
<!-- openspec/specs/ is empty: no prior capabilities exist to modify. -->

## Impact

- Code: new `eval/` package, `cmd/eval/main.go` rewritten, `mock/memstore.go`,
  `mock/embedder.go`, `mock/reranker.go`, `decay.go` (+`DecayByName`), `mcp/` exports
  (rename of `handlers` → `Tools`, DTOs exported, `NewServer(t *Tools)`),
  `cmd/engramd/main.go` (env var + new wiring), `eval/baseline.json`.
- CI: `.github/workflows/ci.yml` unchanged; the existing `go run ./cmd/eval --ci` line
  starts enforcing when the baseline lands.
- Dependencies: none added.
- Docs: `docs/engram-m5-design.md` is not created; the design lives in this change.
  README eval section update is M6.
