# Engram M3 Implementation Plan — Rerank + token-budget

> **For agentic workers:** implement task-by-task, test-first; `- [ ]` steps. Spec:
> `docs/engram-m3-design.md`. Builds on M2. Reranker per ADR-0002.

**Goal:** Cross-encoder rerank the expanded recall candidates and pack the result under a token budget.

**Architecture:** A TEI `/rerank` client implements `engram.Reranker`. `recall` selects top-M candidates by the M2 propagated `blend`, reranks them (final authority; degrades to blend order on error), takes k, then trims under a token ceiling. A `tei-rerank` sidecar serves `bge-reranker-v2-m3`.

**Tech Stack:** Go 1.26 · TEI (embed `:8080`, rerank `:8081`) · neo4j-go-driver/v5 · modelcontextprotocol/go-sdk.

## Global Constraints
- Domain imports no infra; `context.Context` first; errors wrapped `%w`, logged-then-sanitized at the MCP boundary; no panics (handlers `recover`). Inject the clock.
- Gates per task: `go build ./...`, `go test ./...`, `golangci-lint run ./...`; integration tasks also `go test -tags integration ./...` (stack up incl. `tei-rerank`).
- Config consts (tunable at M5): `rerankCandidates = 20`, `maxTokens = 2048`. Reranker = `bge-reranker-v2-m3`.
- Commit per task (lefthook pre-commit). Do not push.

## File structure
- `inference/reranker.go` (+ `reranker_test.go`) — `Reranker` client over TEI `/rerank`.
- `mock/fakes.go` — `FakeReranker`.
- `mcp/recall.go` — rerank + `packBudget`; `handlers` gains `reranker`/`rerankCandidates`/`maxTokens`.
- `mcp/server.go` — `NewServer(embedder, reranker, store, clock)`.
- `mcp/budget_test.go`, updates to `mcp/recall_test.go`, `mcp/remember_test.go`/`m1_integration_test.go` (handler constructors).
- `neo4j/store.go` — `Search`/`Neighbors` project non-embedding props (perf fix).
- `cmd/engramd/main.go` — wire the reranker.
- `mcp/m3_integration_test.go`; `README.md`.

---

### Task 1: `inference` Reranker client (TEI `/rerank`)

**Files:** Create `inference/reranker.go`, `inference/reranker_test.go`.

**Produces:**
```go
var _ engram.Reranker = (*Reranker)(nil)
type Reranker struct { httpClient *http.Client; baseURL string }
func NewReranker(baseURL string) *Reranker
func (r *Reranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
```
TEI `/rerank`: POST `{"query": query, "texts": docs}` → `[{"index": i, "score": s}, …]` (order not guaranteed). Map back to input order: `out[i] = score` for each returned `{index:i, score:s}`. Empty `docs` → return `[]float64{}` with no HTTP call. Reuse the embedder client's hygiene: `defaultTimeout`, `maxResponseBytes` LimitReader, drain-on-non-200, errors never echo the body.

- [ ] **Step 1 — failing test** (`reranker_test.go`, `httptest`): table — happy path (3 docs, server returns out-of-order `[{2,0.9},{0,0.1},{1,0.5}]` → mapped to `[0.1,0.5,0.9]` aligned to input); empty docs → `len==0`, no request; 503 status → error not containing body; malformed JSON → error; count mismatch (server returns 2 scores for 3 docs) → error. Assert request is POST `/rerank` with `{query, texts}`.
- [ ] **Step 2 — run, expect FAIL** (`NewReranker` undefined): `go test ./inference/`
- [ ] **Step 3 — implement** `reranker.go`.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(inference): TEI reranker client`

---

### Task 2: `mock.FakeReranker`

**Files:** Modify `mock/fakes.go`.

**Produces:**
```go
type FakeReranker struct {
	Scores    []float64
	Err       error
	LastQuery string
	LastDocs  []string
}
func (f *FakeReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	f.LastQuery, f.LastDocs = query, docs
	if f.Err != nil { return nil, f.Err }
	return f.Scores, nil
}
```
(No standalone test — used by Task 3. Gate: build + lint.)

- [ ] **Step 1 — implement**; **Step 2 — build+lint**; **Step 3 — commit** `test(mock): FakeReranker`

---

### Task 3: `mcp` recall rerank + token-budget

**Files:** Modify `mcp/recall.go`, `mcp/server.go`, `mcp/remember_test.go` (testHandlers), `mcp/m1_integration_test.go` (liveHandlers); Create `mcp/budget_test.go`; update `mcp/recall_test.go`, `mcp/server_test.go`.

**Interfaces:** `handlers` gains `reranker engram.Reranker`, `rerankCandidates int`, `maxTokens int`. `NewServer(embedder engram.Embedder, reranker engram.Reranker, store Store, clock engram.Clock)`. New consts `defaultRerankCandidates = 20`, `defaultMaxTokens = 2048`.

`doRecall` after `blend`:
```go
cands := blend(seeds, neighbors, h.rerankCandidates, bridgePenalty) // top-M
ranked := h.rerank(ctx, in.Query, cands)                            // reorder or fall back
if len(ranked) > k { ranked = ranked[:k] }
ranked = packBudget(ranked, h.maxTokens)
// map ranked -> DTO (unchanged)
```
Helpers (unexported, recall.go):
```go
func (h *handlers) rerank(ctx context.Context, query string, cands []engram.RecallResult) []engram.RecallResult {
	if len(cands) < 2 { return cands }
	docs := make([]string, len(cands))
	for i, c := range cands { docs[i] = c.Content }
	scores, err := h.reranker.Rerank(ctx, query, docs)
	if err != nil || len(scores) != len(cands) {
		if err != nil { h.log.Error("recall: rerank failed; using blend order", "err", err) }
		return cands // graceful fallback
	}
	out := make([]engram.RecallResult, len(cands))
	copy(out, cands)
	for i := range out { out[i].Score = scores[i] }
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// packBudget keeps results in order while their cumulative approx token count
// (len(content)/4) stays within maxTokens; always keeps at least the first.
func packBudget(rs []engram.RecallResult, maxTokens int) []engram.RecallResult {
	total := 0
	for i, r := range rs {
		total += len(r.Content)/4 + 1
		if total > maxTokens && i > 0 { return rs[:i] }
	}
	return rs
}
```

- [ ] **Step 1 — failing tests**: `budget_test.go` — `packBudget` trims when the ceiling is crossed, keeps ≥1, preserves order. `recall_test.go` — (a) rerank reorders: `FakeReranker.Scores` inverts the blend order → results come back in rerank order with `Score` = rerank score; (b) rerank error → results in blend order (fallback), no error; (c) `LastQuery`/`LastDocs` = the candidate contents. Update `server_test.go` (`NewServer` new arg with `&mock.FakeReranker{}`).
- [ ] **Step 2 — run, expect FAIL** (`packBudget`/`rerank`/NewServer arity).
- [ ] **Step 3 — implement**; update `testHandlers`/`liveHandlers` to set `reranker`/`rerankCandidates`/`maxTokens`.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(mcp): rerank + token-budget in recall`

---

### Task 4: neo4j perf — project non-embedding props in recall paths

**Files:** Modify `neo4j/store.go`.

Refactor `nodeToMemory(node)` to `mapToMemory(props map[string]any)` (same logic, from a map); `Get` calls `mapToMemory(node.Props)`. `Search` and `Neighbors` change their Cypher `RETURN` to project the needed props **without** `embedding`, e.g. `RETURN node {.id, .namespace, .type, .content, .importance, .stability, .access_count, .created_at, .last_accessed, .source, .superseded} AS m, score` and map via `mapToMemory` (leaving `Embedding` nil). Rerank/DTO need `content`, never the vector.

- [ ] **Step 1 — verify** the existing `//go:build integration` `Search`/`Neighbors` tests still pass (they assert id/type/namespace/score, not embedding) after the projection — run `go test -tags integration ./neo4j/`. (This is a behavior-preserving refactor; the guard is the existing suite. Add an assertion to `TestStoreSearch` that `got[0].Embedding == nil` to pin the projection.)
- [ ] **Step 2 — implement** the refactor + projections.
- [ ] **Step 3 — run** `go test -tags integration ./neo4j/` PASS; build; lint
- [ ] **Step 4 — commit** `perf(neo4j): project only needed props in Search/Neighbors`

---

### Task 5: `tei-rerank` sidecar + engramd wiring

**Files:** Modify `docker-compose.yml`, `cmd/engramd/main.go`.

Add to compose:
```yaml
  tei-rerank:
    image: ghcr.io/huggingface/text-embeddings-inference:cpu-1.6
    command: ["--model-id", "BAAI/bge-reranker-v2-m3"]
    ports:
      - "127.0.0.1:8081:80"
    volumes:
      - tei-rerank-cache:/data
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://localhost:80/health || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 30
      start_period: 60s
```
(+ `tei-rerank-cache` volume.) `engramd.run()`: `reranker := inference.NewReranker(getenv("TEI_RERANK_URL", "http://localhost:8081"))`, pass to `mcp.NewServer(embedder, reranker, store, systemClock{})`.

- [ ] **Step 1 — implement** compose + wiring; `docker compose config -q`.
- [ ] **Step 2 — gate:** `go build ./...`; `go vet ./...`; `golangci-lint run ./...`.
- [ ] **Step 3 — commit** `feat(engramd): serve reranker via tei-rerank sidecar`

---

### Task 6: M3 live integration

**Files:** Create `mcp/m3_integration_test.go` (`//go:build integration`). Requires `docker compose up -d --wait` incl. `tei-rerank`.

- [ ] **Step 1 — tests** (via `liveHandlers`, real reranker): (a) store several memories in a namespace; a query where the cross-encoder's top result differs from pure vector rank — assert `recall` returns it first (rerank reordered); (b) set `h.maxTokens` low → assert the returned count is trimmed by `packBudget`.
- [ ] **Step 2 — run** (stack up): `go test -tags integration ./...` PASS; default `go test ./...` green.
- [ ] **Step 3 — commit** `test(mcp): M3 rerank + token-budget integration`

---

### Task 7: docs

**Files:** Modify `README.md` (Status → M3; recall now reranks + token-budget; add `tei-rerank` + `TEI_RERANK_URL`); note M3 in `docs/engram-prd-v1.md` §9 if tracked.

- [ ] **Step 1 — edit**; **Step 2 — commit** `docs: M3 rerank + token-budget`

## Self-review
- **Spec coverage:** Reranker impl (1), sidecar (5), rerank in recall (3), token-budget (3), graceful degradation (3), perf fix (4), integration (6). Covered.
- **Type consistency:** `engram.Reranker.Rerank(ctx, query, []string) ([]float64, error)` used by inference (1), mock (2), handlers (3), engramd (5); `NewServer` new signature updated in server.go + engramd + tests.
- **Deferred correctly:** no recency/importance/retrievability blend, no decay.
