# Engram M3 Design — "Rerank + token-budget assembly"

> Status: approved 2026-07-02 (brainstorm). Goal (PRD §9 M3): cross-encoder rerank +
> token-budget assembly. Builds on M2 (graph expansion). Reranker per
> [ADR-0002](adr/0002-embedding-model-and-amd-acceleration.md).

## Decision: rerank-only M3, blend at M4
The PRD (§6.2) lists a weighted blend (`w_sim·sim + w_rec·recency + w_imp·importance +
w_ret·retrievability`) at step 3, before rerank. **Retrievability** — the load-bearing
term and the type-aware-decay thesis — only exists at M4. So M3 ships the cross-encoder
rerank (the real quality lever) and token-budget assembly; the full weighted blend lands
cohesively at M4 when retrievability exists. Candidate *selection* at M3 uses M2's
propagated similarity; the reranker decides the final order.

## Scope
**In:** `Reranker` port implementation (TEI `/rerank`); a `tei-rerank` sidecar
(`bge-reranker-v2-m3`); rerank + token-budget wired into `recall`; the deferred M2 perf
fix (recall paths stop hydrating embeddings they don't use).
**Deferred → M4:** weighted blend (recency + importance + retrievability),
reinforce-on-access, decay sweep, supersession. **→ M6:** `memory_stats`.

## Recall pipeline (M3)
1. Embed query → vector kNN **seeds** (`seedN`, namespace-filtered) — M1.
2. **Expand:** `Neighbors` (1-hop `[:LINKS]` + entity bridges) — M2.
3. `blend()` (M2 propagated scoring) → **take top-M candidates** (`M = rerankCandidates`).
4. **Rerank:** `Reranker.Rerank(ctx, query, [candidate.Content…])` → scores aligned to
   candidates; reorder by rerank score descending. **Rerank is the final authority.**
5. **Take k**, then **token-budget assembly:** greedily pack results under a max-token
   ceiling (`maxTokens`; token ≈ `len(content)/4`), stopping before the ceiling is
   exceeded (may return fewer than `k`).
6. Map to DTO; `score` = rerank score; `retrieved_via` unchanged.

**Graceful degradation:** if the reranker errors or is unreachable, `recall` logs it and
falls back to the blend order — rerank is a quality enhancement, not a correctness
dependency. Recall never fails solely because the reranker is down.

## Components
- **`engram`:** `Reranker` port already exists (`Rerank(ctx, query string, docs []string)
  ([]float64, error)`, scores aligned to docs by index). No domain change beyond
  possibly a small helper.
- **`inference`:** add a reranker client implementing `engram.Reranker` against TEI
  `/rerank` (request `{query, texts:[…]}` → `[{index, score}…]`, mapped back to input
  order). Context-first, error-sanitized, bounded response — mirrors the embedder client.
- **`docker-compose.yml`:** add `tei-rerank` (`ghcr.io/huggingface/text-embeddings-inference:cpu-1.6`,
  `--model-id BAAI/bge-reranker-v2-m3`, `127.0.0.1:8081:80`, `/health` healthcheck).
- **`mcp`:** `handlers` gains `reranker engram.Reranker`, `rerankCandidates int`,
  `maxTokens int`. `recall` inserts steps 4–5. A pure `packBudget(results, maxTokens)`
  helper does the token-budget trim (unit-testable).
- **`cmd/engramd`:** wire `inference.NewReranker(env("TEI_RERANK_URL",
  "http://localhost:8081"))` into `NewServer`.
- **Perf fix (M2 follow-up):** `Search` and `Neighbors` project only the properties recall
  needs (`content`, `type`, `namespace`, provenance fields) — **not** the embedding — via
  a lighter node mapping. Rerank needs `content`, never the vector.

## Defaults (configurable at M5 tuning)
`rerankCandidates = 20`, `maxTokens = 2048`, reranker = `bge-reranker-v2-m3`
(`gte-reranker-modernbert-base` benchmarked at M5 per ADR-0002).

## Policy gate
Unchanged. The candidate contents sent to the reranker are already-stored memory text
(trusted at that point); the reranker URL is operator config.

## Testing
- **Unit (fakes):** a `FakeReranker` (programmable scores) — rerank reorders candidates;
  graceful fallback when `Rerank` errors (results still returned in blend order, logged);
  `packBudget` trims under the ceiling and preserves order; `score` reflects rerank.
- **Integration (`//go:build integration`, live with `tei-rerank`):** a query where the
  cross-encoder reorders results away from pure vector similarity; token-budget caps the
  returned count.

## Deferred carry-forward
Weighted blend (recency + importance + retrievability) + reinforce-on-access + decay
sweep + supersession → M4; `memory_stats` → M6.
