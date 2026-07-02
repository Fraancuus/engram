# Engram M1 Implementation Plan — Core loop (remember + recall over MCP)

> **For agentic workers:** implement task-by-task, test-first. Steps use `- [ ]` checkboxes.
> Spec: `docs/engram-m1-design.md`. Builds on the M0 skeleton.

**Goal:** Ship the `remember` and `recall` MCP tools (vector-only recall, dedup, entities written), served over stdio.

**Architecture:** Domain gains a `RecallResult` type. The `neo4j.Store` gains `Search`/`Reinforce`/`LinkEntities` (parameterized Cypher; vector-index kNN). The `mcp` package gains typed `remember`/`recall` handlers behind narrow consumer interfaces, registered via the SDK's generic `AddTool`; handlers own input validation. `engramd` wires the ports and serves the server over stdio.

**Tech Stack:** Go 1.26 · `modelcontextprotocol/go-sdk` v1.6.1 (aliased `mcpsdk`) · `neo4j-go-driver/v5` (aliased `neo4jdriver`) · TEI sidecar.

## Global Constraints
- Domain (`engram`) imports NO infrastructure; ports live in the domain, consumer interfaces at their consumers (depguard binds `engram.go`/`memory.go`/`decay.go`).
- `context.Context` first param on all I/O; errors wrapped with `%w`; no panics in handlers/paths.
- All Cypher parameterized. MCP inputs untrusted — validate at the handler boundary; errors crossing back are agent-legible, never leak internals.
- Inject the clock; no `time.Now()` in domain logic. Timestamps stored UTC.
- Gates per task: `go build ./...`, `go test ./...`, `golangci-lint run ./...` (and `go test -tags integration ./...` with the stack up for integration tasks). `-race` runs in CI (no local cgo).
- Defaults: `DEDUP_THRESHOLD = 0.95`, default `k = 10`, `max k = 100`.

## File structure
- `engram.go` — add `RecallResult`; add `MemoryType.Valid()`.
- `neo4j/store.go` — add `Search`, `Reinforce`, `LinkEntities` (+ helpers).
- `neo4j/search_integration_test.go` — integration tests for the new store methods.
- `mcp/remember.go` — `rememberInput/Output`, the remember handler + dedup.
- `mcp/recall.go` — `recallInput/Output` DTOs, the recall handler.
- `mcp/server.go` — `NewServer(embedder, store, clock)` registers both tools; `Serve` helper; the consumer `Store` interface.
- `mcp/remember_test.go`, `mcp/recall_test.go` — handler unit tests (fakes).
- `mock/fakes.go` — `Embedder` + store fakes for unit tests.
- `cmd/engramd/main.go` — wire ports, serve MCP over stdio.
- `README.md` — M1 usage.

---

### Task 1: Domain — `RecallResult` + `MemoryType.Valid()`

**Files:** Modify `engram.go`; Test `engram_test.go`.

**Produces:**
- `type RecallResult struct { Memory; Score float64 }`
- `func (t MemoryType) Valid() bool` — true for episodic/semantic/procedural.

- [ ] **Step 1 — failing test** (`engram_test.go`): table test for `MemoryType.Valid()` (each of the 3 constants → true; `""` and `"reference"` → false); and a compile-level check that `RecallResult{Memory: Memory{ID:"x"}, Score: 0.5}.ID == "x"`.
- [ ] **Step 2 — run, expect FAIL** (`Valid`/`RecallResult` undefined): `go test ./`
- [ ] **Step 3 — implement** in `engram.go`:
```go
type RecallResult struct {
	Memory
	Score float64
}

// Valid reports whether t is one of the known memory types.
func (t MemoryType) Valid() bool {
	switch t {
	case Episodic, Semantic, Procedural:
		return true
	default:
		return false
	}
}
```
- [ ] **Step 4 — run, expect PASS**; `golangci-lint run ./`
- [ ] **Step 5 — commit** `feat(domain): add RecallResult and MemoryType.Valid`

---

### Task 2: neo4j `Search` (vector kNN, namespace-filtered)

**Files:** Modify `neo4j/store.go`; Test `neo4j/search_integration_test.go` (`//go:build integration`).

**Consumes:** `engram.RecallResult`, existing `nodeToMemory`/`toFloat64`.
**Produces:** `func (s *Store) Search(ctx context.Context, namespaces []engram.Namespace, vec engram.Vector, k int) ([]engram.RecallResult, error)`

Cypher (over-fetch, then namespace-filter, then limit):
```go
const q = `
CALL db.index.vector.queryNodes('memory_embedding', $fetch, $vec)
YIELD node, score
WHERE size($namespaces) = 0 OR node.namespace IN $namespaces
RETURN node, score
ORDER BY score DESC
LIMIT $k`
```
- `$vec` = `toFloat64(vec)`; `$namespaces` = `[]string` of the namespaces; `$fetch` = `max(k*5, 50)` (oversample so the namespace cut doesn't starve top-k); `$k` = k.
- Map each record: `node` → `nodeToMemory`, `score`→float64 → `RecallResult{Memory, Score}`.

- [ ] **Step 1 — failing integration test**: seed 3 memories across 2 namespaces (use `uniqueID`), `Search` with the query vector equal to one seeded embedding, k=2; assert the exact match ranks first (score highest) and namespace filtering returns only requested-namespace results.
- [ ] **Step 2 — run, expect FAIL** (`Search` undefined): `go test -tags integration -run TestStoreSearch ./neo4j/`
- [ ] **Step 3 — implement** `Search` per above (reuse `nodeToMemory`, `toFloat64`).
- [ ] **Step 4 — run, expect PASS** (stack up); `go build ./...`; `golangci-lint run ./...`
- [ ] **Step 5 — commit** `feat(neo4j): vector kNN Search with namespace filter`

---

### Task 3: neo4j `Reinforce` + `LinkEntities`

**Files:** Modify `neo4j/store.go`; Test `neo4j/search_integration_test.go`.

**Produces:**
- `func (s *Store) Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error` — `MATCH (m:Memory {id:$id}) SET m.access_count = m.access_count + 1, m.last_accessed = $now` (`$now = now.UTC()`); return `engram.ErrNotFound` if no node matched (check summary counters).
- `func (s *Store) LinkEntities(ctx context.Context, id engram.MemoryID, names []string) error` — `MATCH (m:Memory {id:$id}) UNWIND $names AS n MERGE (e:Entity {id:n}) SET e.name = n MERGE (m)-[:MENTIONS]->(e)` (no-op if `names` empty).

- [ ] **Step 1 — failing integration tests**: (a) Put a memory (access_count 0), `Reinforce` twice, `Get`, assert access_count==2 and last_accessed advanced; (b) Put a memory, `LinkEntities(id, ["PortIQ","Neo4j"])`, then a raw `MATCH (m{id})-[:MENTIONS]->(e) RETURN count(e)` (via a small query helper or `Search`-adjacent check) asserts 2 edges; (c) `Reinforce` of a missing id → `errors.Is(err, engram.ErrNotFound)`.
- [ ] **Step 2 — run, expect FAIL**: `go test -tags integration ./neo4j/`
- [ ] **Step 3 — implement** both methods (parameterized; `Reinforce` inspects `result.Summary().Counters().PropertiesSet()`/`ContainsUpdates()` or a `RETURN count(m)` to detect not-found).
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(neo4j): Reinforce and LinkEntities`

---

### Task 4: `mock` fakes for handler unit tests

**Files:** Create `mock/fakes.go`.

**Produces (in package `mock`):**
- `FakeEmbedder{ Vec engram.Vector; Err error }` implementing `engram.Embedder` (returns `Vec`/`Err`, records last text).
- `FakeStore` implementing the `mcp.Store` interface (Task 5): records `Put`/`Reinforce`/`LinkEntities` calls; `Search` returns a programmable `[]engram.RecallResult`/error. In-memory map keyed by id.

(No standalone test — fakes are exercised by Tasks 5/6. Gate: `go build ./...`, lint.)

- [ ] **Step 1 — implement** `mock/fakes.go` with the two fakes (exported fields/hooks for assertions).
- [ ] **Step 2 — build + lint** clean.
- [ ] **Step 3 — commit** `test(mock): Embedder and store fakes for M1 handlers`

---

### Task 5: `mcp` remember handler + dedup + `Store` interface

**Files:** Create `mcp/remember.go`, `mcp/remember_test.go`.

**Produces:**
```go
// consumer interface — satisfied by *neo4j.Store
type Store interface {
	Put(ctx context.Context, m engram.Memory) error
	Search(ctx context.Context, namespaces []engram.Namespace, vec engram.Vector, k int) ([]engram.RecallResult, error)
	Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error
	LinkEntities(ctx context.Context, id engram.MemoryID, names []string) error
}

type rememberInput struct {
	Content    string   `json:"content" jsonschema:"the memory text"`
	Type       string   `json:"type" jsonschema:"episodic | semantic | procedural"`
	Namespace  string   `json:"namespace" jsonschema:"universe, e.g. work/engineering"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"0..1, default 0.5"`
	Source     string   `json:"source,omitempty"`
	Entities   []string `json:"entities,omitempty"`
}
type rememberOutput struct {
	MemoryID string `json:"memory_id"`
	Deduped  bool   `json:"deduped"`
}
```
Handler logic: validate (`Type.Valid()`, non-empty bounded `Content`/`Namespace`, `len(Entities)` and each name bounded, `Importance` in [0,1] or default 0.5) → `Embed(content)` → dedup (`Search(ns,vec,1)`; if `hits[0].Score >= dedupThreshold` → `Reinforce`, return existing id + `deduped:true`) → else build `Memory` (`newMemoryID()` via crypto/rand hex; `CreatedAt=LastAccessed=clock.Now()`; `AccessCount=0`) → `Put` → `LinkEntities` if any → return new id + `deduped:false`. Validation failures return a legible `error`.

- [ ] **Step 1 — failing unit tests** (`mcp/remember_test.go`, fakes): (a) valid input inserts (FakeStore got a Put with the content/type/namespace; output `deduped:false`, non-empty id, entities linked); (b) dedup: FakeStore.Search returns a hit at score 0.97 → handler calls Reinforce, NOT Put, output `deduped:true` with the hit's id; (c) below-threshold hit (0.90) → inserts; (d) invalid type / empty content / empty namespace → error, no store calls; (e) importance defaulting (nil → 0.5; out-of-range → error).
- [ ] **Step 2 — run, expect FAIL**: `go test ./mcp/`
- [ ] **Step 3 — implement** `mcp/remember.go` (handler as a method on an unexported `handlers` struct holding `embedder/store/clock/dedupThreshold`).
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(mcp): remember handler with dedup + entities`

---

### Task 6: `mcp` recall handler

**Files:** Create `mcp/recall.go`, `mcp/recall_test.go`.

**Produces:**
```go
type recallInput struct {
	Query      string   `json:"query" jsonschema:"text to search for"`
	Namespaces []string `json:"namespaces,omitempty" jsonschema:"restrict to these; empty = all"`
	K          *int     `json:"k,omitempty" jsonschema:"max results, default 10, max 100"`
}
type provenanceDTO struct {
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
	AccessCount  int       `json:"access_count"`
}
type recallResultDTO struct {
	ID         string        `json:"id"`
	Content    string        `json:"content"`
	Score      float64       `json:"score"`
	Type       string        `json:"type"`
	Namespace  string        `json:"namespace"`
	Provenance provenanceDTO `json:"provenance"`
}
type recallOutput struct {
	Results []recallResultDTO `json:"results"`
}
```
Handler: validate (`Query` non-empty; clamp `K` to [1,100], default 10; bound namespaces) → `Embed(query)` → `Search(namespaces, vec, k)` → map each `RecallResult` to `recallResultDTO` (provenance projected from Memory fields).

- [ ] **Step 1 — failing unit tests**: (a) maps store results to DTOs incl. provenance projection + score; (b) K nil → 10, K=500 → clamped to 100, K=0 → 1; (c) empty query → error; (d) namespaces passed through to `Search`.
- [ ] **Step 2 — run, expect FAIL**: `go test ./mcp/`
- [ ] **Step 3 — implement** `mcp/recall.go`.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(mcp): recall handler (vector, namespace-filtered)`

---

### Task 7: `mcp.NewServer` registers tools + `Serve`

**Files:** Modify `mcp/server.go`.

**Produces:**
- `func NewServer(embedder engram.Embedder, store Store, clock engram.Clock) *mcpsdk.Server` — builds `handlers{...}`, registers `remember` + `recall` via `mcpsdk.AddTool`.
- `func Serve(ctx context.Context, srv *mcpsdk.Server) error { return srv.Run(ctx, &mcpsdk.StdioTransport{}) }` (keeps the sdk transport out of `engramd`).

- [ ] **Step 1 — failing test** (`mcp/server_test.go`): `NewServer(fakeEmbedder, fakeStore, fakeClock)` returns non-nil; (the existing zero-arg `NewServer` is replaced — update any references).
- [ ] **Step 2 — run, expect FAIL** (signature change): `go test ./mcp/`
- [ ] **Step 3 — implement**: rewrite `NewServer` to take ports and `AddTool` both handlers; add `Serve`.
- [ ] **Step 4 — run, expect PASS**; `go build ./...`; lint
- [ ] **Step 5 — commit** `feat(mcp): register remember/recall on the server`

---

### Task 8: `engramd` serves MCP over stdio

**Files:** Modify `cmd/engramd/main.go`.

**Consumes:** `mcp.NewServer`, `mcp.Serve`, `inference.New`, `eneo4j.New`, `systemClock`.

`run()`: build embedder + store + clock, `srv := mcp.NewServer(embedder, store, systemClock{})`, `log` that it's serving, `return mcp.Serve(ctx, srv)`. Use `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` for ctx (graceful stop). Drop the M0 smoke flow (it lives on as the e2e integration test).

- [ ] **Step 1 — implement** (no unit test — composition root; behavior covered by Task 9 integration).
- [ ] **Step 2 — gates**: `go build ./...`; `go vet ./...`; `golangci-lint run ./...`
- [ ] **Step 3 — commit** `feat(engramd): serve remember/recall over stdio`

---

### Task 9: end-to-end integration — the store→search demo

**Files:** Create `mcp/m1_integration_test.go` (`//go:build integration`) driving `NewServer`'s handlers against the live `*neo4j.Store` + real `inference.Client` (not fakes).

- [ ] **Step 1 — tests**: (a) remember two distinct memories in namespace A, recall a query close to one → it ranks first; (b) remember near-identical content twice → second returns `deduped:true`, recall returns one node; (c) remember with `entities` → recall the memory, and a direct `MENTIONS` count check; (d) namespace scoping → recall restricted to A excludes B.
- [ ] **Step 2 — run** (stack up): `go test -tags integration ./...` → PASS; default `go test ./...` still green (tag excluded).
- [ ] **Step 3 — commit** `test(mcp): M1 end-to-end remember/recall integration`

---

### Task 10: docs

**Files:** Modify `README.md` (Status → M1; add `remember`/`recall` tool summary + how an MCP client connects over stdio). Update `docs/engram-prd-v1.md` §9 M1 checkbox if tracked.

- [ ] **Step 1 — edit docs**; **Step 2 — commit** `docs: M1 remember/recall`

## Self-review
- **Spec coverage:** remember (Task 5), recall (Task 6), dedup (Task 5 + 9b), entities written (Task 3 + 5 + 9c), vector kNN + namespace filter (Task 2 + 9d), rich provenance (Task 6), served over stdio (Task 7/8), unit + integration tests (5/6 + 9) — all covered.
- **Type consistency:** `Store` interface (Task 5) matches `neo4j.Store` methods (Tasks 2–3) and `NewServer` params (Task 7); `RecallResult` (Task 1) used by Search (Task 2) + recall (Task 6).
- **Deferred correctly:** no links/graph/rerank/decay/forget.
