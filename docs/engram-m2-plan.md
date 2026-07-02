# Engram M2 Implementation Plan — Graph (auto-linking + associative expansion)

> **For agentic workers:** implement task-by-task, test-first; `- [ ]` steps. Spec:
> `docs/engram-m2-design.md`. Builds on M1.

**Goal:** Auto-link memories on write and expand `recall` with 1-hop links + entity bridges, ranked by propagated scoring, tagging each result's `retrieved_via`.

**Architecture:** Domain gains `RetrievedVia`, `Link`, `Neighbor`. `neo4j.Store` gains `Link` (MERGE `[:LINKS]`) and `Neighbors` (1-hop + entity-bridge fetch, scoped, capped) — parameterized Cypher. The `remember` handler reuses its dedup `Search` (now `k=K`) to also auto-link; the `recall` handler runs seeds → `Neighbors` → a **pure `blend()`** (propagated score + dedup + top-k) → DTOs with `retrieved_via`.

**Tech Stack:** Go 1.26 · neo4j-go-driver/v5 (aliased `neo4jdriver`) · modelcontextprotocol/go-sdk (aliased `mcpsdk`).

## Global Constraints
- Domain imports NO infra; consumer interfaces at their consumers; `context.Context` first; errors wrapped `%w`; no panics (handlers `recover`). All Cypher parameterized. Inject the clock. Timestamps UTC.
- Gates per task: `go build ./...`, `go test ./...`, `golangci-lint run ./...`; integration tasks also `go test -tags integration ./...` (stack up).
- Config consts (become configurable at M3): `seedN=50`, `autoLinkK=5`, `linkThreshold=0.85`, `bridgePenalty=0.5`.
- Commit per task (lefthook pre-commit gate runs). Do not push.

## File structure
- `engram.go` — add `RecallResult.RetrievedVia`; add `Link`, `Neighbor` types.
- `neo4j/store.go` — add `Link`, `Neighbors`.
- `neo4j/graph_integration_test.go` — integration tests for Link/Neighbors.
- `mock/fakes.go` — `FakeStore` gains `Link`/`Neighbors` + recorders.
- `mcp/remember.go` — `Store` += `Link`; `rememberInput` += `Links`; auto-link after insert (reuse the dedup `Search`).
- `mcp/recall.go` — `Store` += `Neighbors`; expansion + pure `blend()` + `retrieved_via`.
- `mcp/blend_test.go`, updates to `mcp/remember_test.go` / `mcp/recall_test.go`.
- `mcp/m1_integration_test.go` sibling `mcp/m2_integration_test.go` — live expansion tests.
- `README.md` — M2 status + `retrieved_via`.

---

### Task 1: Domain — `RetrievedVia`, `Link`, `Neighbor`

**Files:** Modify `engram.go`; Test `engram_test.go`.

**Produces:**
```go
// on RecallResult:
type RecallResult struct {
	Memory
	Score        float64
	RetrievedVia string // "vector" | "link" | "entity:<name>" (M2)
}
// new:
type Link struct {
	To     MemoryID
	Weight float64
}
type Neighbor struct {
	Memory   Memory
	SourceID MemoryID // the seed this was reached from
	Via      string   // "link" | "entity:<name>"
	Weight   float64  // edge weight for links; unused for entity bridges
}
```

- [ ] **Step 1 — failing test** (`engram_test.go`): assert `RecallResult{Memory:{ID:"x"},Score:1,RetrievedVia:"vector"}.RetrievedVia == "vector"`, and that `Link{To:"a",Weight:0.9}` / `Neighbor{Memory:{ID:"b"},SourceID:"a",Via:"link",Weight:0.9}` construct and read back.
- [ ] **Step 2 — run, expect FAIL** (`RetrievedVia`/`Link`/`Neighbor` undefined): `go test ./`
- [ ] **Step 3 — implement** in `engram.go` (add the field + two types, with doc comments).
- [ ] **Step 4 — run, expect PASS**; `golangci-lint run ./`
- [ ] **Step 5 — commit** `feat(domain): RetrievedVia, Link, Neighbor for graph expansion`

---

### Task 2: neo4j `Link`

**Files:** Modify `neo4j/store.go`; Test `neo4j/graph_integration_test.go` (`//go:build integration`).

**Produces:** `func (s *Store) Link(ctx context.Context, from engram.MemoryID, links []engram.Link) error`

Cypher (idempotent; skip targets that don't exist; no-op on empty):
```go
const q = `
MATCH (a:Memory {id: $from})
UNWIND $links AS lk
MATCH (b:Memory {id: lk.to})
WHERE b.id <> a.id
MERGE (a)-[r:LINKS]->(b)
SET r.weight = lk.weight
RETURN count(r) AS c`
```
- `$links` = `[]map{"to":string(l.To),"weight":l.Weight}`. Missing `b` → that UNWIND row drops (inner MATCH), so nonexistent targets are silently skipped. Return `engram.ErrNotFound` only if the source `a` is missing (guard: a separate `MATCH (a) RETURN count(a)` check, or detect zero source — simplest: `OPTIONAL MATCH` the source and check). Keep it simple: if `len(links)==0` return nil; else run and return wrapped errors.

- [ ] **Step 1 — failing integration test**: Put A, B, C (unique ids). `Link(A, [{B,0.9},{C,0.7}])`. Assert via `rawCount` `MATCH (:Memory{id:A})-[r:LINKS]->() RETURN count(r) AS c` == 2, and a weighted-check query returns weight 0.9 for A→B. Re-run `Link` → still 2 (idempotent). `Link(A, [{nonexistent,1}])` → no error, no new edge.
- [ ] **Step 2 — run, expect FAIL** (`Link` undefined): `go test -tags integration -run TestStoreLink ./neo4j/`
- [ ] **Step 3 — implement** `Link`.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(neo4j): Link creates weighted [:LINKS] edges`

---

### Task 3: neo4j `Neighbors`

**Files:** Modify `neo4j/store.go`; Test `neo4j/graph_integration_test.go`.

**Produces:** `func (s *Store) Neighbors(ctx context.Context, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error)`

One query returns both neighbor kinds, tagged; namespace scope applied to *bridge* targets; caps bound expansion:
```go
const q = `
MATCH (seed:Memory) WHERE seed.id IN $seeds
CALL {
  WITH seed
  MATCH (seed)-[r:LINKS]-(nb:Memory)
  WHERE NOT nb.id IN $seeds
  RETURN nb, seed.id AS src, 'link' AS via, r.weight AS weight
  ORDER BY r.weight DESC LIMIT $linkCap
  UNION
  WITH seed
  MATCH (seed)-[:MENTIONS]->(e:Entity)<-[:MENTIONS]-(nb:Memory)
  WHERE nb.id <> seed.id AND NOT nb.id IN $seeds
    AND (size($scope) = 0 OR nb.namespace IN $scope)
  RETURN nb, seed.id AS src, 'entity:' + e.name AS via, 1.0 AS weight
  ORDER BY nb.id LIMIT $bridgeCap
}
RETURN nb, src, via, weight`
```
- `$seeds` = `[]string`; `$scope` = `[]string`; `$linkCap`, `$bridgeCap` = per-seed caps (const `neighborCapPerSeed = 20`). Map each row → `engram.Neighbor{Memory: nodeToMemory(nb), SourceID: src, Via: via, Weight: weight}` (reuse `nodeToMemory`; read `weight` as float64, `via`/`src` as string). Links are traversed undirected (`-[r:LINKS]-`). Bridges cross namespaces unless `$scope` non-empty.

- [ ] **Step 1 — failing integration test**: seed graph in unique namespaces — A(nsX) links B(nsX, weight 0.9); A mentions "Redis"; C(nsY) mentions "Redis". `Neighbors([A], nil)` → contains B via "link" and C via "entity:Redis". `Neighbors([A], [nsX])` → contains B (link) but NOT C (bridge filtered by scope). Assert `SourceID==A`, weights/via correct.
- [ ] **Step 2 — run, expect FAIL** (`Neighbors` undefined).
- [ ] **Step 3 — implement** `Neighbors`.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(neo4j): Neighbors (1-hop links + entity bridges)`

---

### Task 4: `mock.FakeStore` gains `Link` + `Neighbors`

**Files:** Modify `mock/fakes.go`.

**Produces:** on `FakeStore`:
```go
// recorders + programmable returns
LinkedTo      map[engram.MemoryID][]engram.Link
LinkErr2      error // (rename existing LinkErr stays for LinkEntities)
NeighborsRes  []engram.Neighbor
NeighborsErr  error
LastNeighborSeeds []engram.MemoryID
LastNeighborScope []engram.Namespace

func (f *FakeStore) Link(_ context.Context, from engram.MemoryID, links []engram.Link) error {
	if f.LinkedTo == nil { f.LinkedTo = map[engram.MemoryID][]engram.Link{} }
	f.LinkedTo[from] = links
	return f.LinkErr2
}
func (f *FakeStore) Neighbors(_ context.Context, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error) {
	f.LastNeighborSeeds, f.LastNeighborScope = seedIDs, scope
	return f.NeighborsRes, f.NeighborsErr
}
```
(No standalone test — exercised by Tasks 5/6. Gate: build + lint.)

- [ ] **Step 1 — implement**; **Step 2 — build + lint clean**; **Step 3 — commit** `test(mock): FakeStore Link + Neighbors`

---

### Task 5: `mcp` remember auto-link + explicit `links[]`

**Files:** Modify `mcp/remember.go`, `mcp/remember_test.go`.

**Interfaces:** `Store` interface (in remember.go) gains `Link(ctx, from engram.MemoryID, links []engram.Link) error`. `rememberInput` gains `Links []string` (json `links,omitempty`). New consts `autoLinkK=5`, `linkThreshold=0.85` on `handlers` (or package consts). Handler now searches `k=autoLinkK` (reused for dedup + auto-link).

Handler change (after the M1 validation + embed):
```go
candidates, err := h.store.Search(ctx, []engram.Namespace{ns}, vec, autoLinkK) // was k=1
// dedup uses candidates[0]; on the insert path:
var links []engram.Link
for _, c := range candidates {
	if c.Score >= h.linkThreshold {
		links = append(links, engram.Link{To: c.ID, Weight: c.Score})
	}
}
for _, id := range in.Links { // explicit; validated (non-empty, bounded)
	links = append(links, engram.Link{To: engram.MemoryID(id), Weight: 1.0})
}
// after store.Put(m):
if len(links) > 0 {
	if err := h.store.Link(ctx, id, links); err != nil { h.log.Error(...); return ..., errors.New("remember: store unavailable") }
}
```
Validate `in.Links`: `len ≤ maxEntities`, each `1..maxEntityBytes`.

- [ ] **Step 1 — failing unit tests**: (a) insert with `SearchResults=[{n1,0.9},{n2,0.7}]` (threshold 0.85) → `st.LinkedTo[newID]` == `[{n1,0.9}]` (n2 filtered); (b) explicit `Links:["x"]` → LinkedTo includes `{x,1.0}`; (c) dedup path (candidates[0].Score 0.97) → no Put, no Link; (d) Link error → sanitized error.
- [ ] **Step 2 — run, expect FAIL** (auto-link/`Links` field/Store.Link undefined).
- [ ] **Step 3 — implement**.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(mcp): auto-link on write + explicit links`

---

### Task 6: `mcp` recall expansion + pure `blend()` + `retrieved_via`

**Files:** Modify `mcp/recall.go`; Create `mcp/blend_test.go`; update `mcp/recall_test.go`.

**Interfaces:** `Store` gains `Neighbors(ctx, seedIDs []engram.MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error)`. `provenanceDTO` gains `RetrievedVia string` (json `retrieved_via`). New const `seedN=50`, `bridgePenalty=0.5`.

Pure blend (unexported, in recall.go):
```go
func blend(seeds []engram.RecallResult, neighbors []engram.Neighbor, k int, bridgePenalty float64) []engram.RecallResult {
	seedSim := make(map[engram.MemoryID]float64, len(seeds))
	best := make(map[engram.MemoryID]engram.RecallResult, len(seeds))
	for _, s := range seeds {
		seedSim[s.ID] = s.Score
		best[s.ID] = engram.RecallResult{Memory: s.Memory, Score: s.Score, RetrievedVia: "vector"}
	}
	for _, n := range neighbors {
		score := seedSim[n.SourceID] * n.Weight
		if strings.HasPrefix(n.Via, "entity:") {
			score = seedSim[n.SourceID] * bridgePenalty
		}
		if cur, ok := best[n.Memory.ID]; !ok || score > cur.Score {
			best[n.Memory.ID] = engram.RecallResult{Memory: n.Memory, Score: score, RetrievedVia: n.Via}
		}
	}
	out := make([]engram.RecallResult, 0, len(best))
	for _, r := range best { out = append(out, r) }
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score { return out[i].Score > out[j].Score }
		return out[i].ID < out[j].ID // deterministic tiebreak
	})
	if len(out) > k { out = out[:k] }
	return out
}
```
Handler `doRecall`: `Search(namespaces, vec, seedN)` → seeds; `Neighbors(seedIDs, namespaces)` → neighbors; `blend(seeds, neighbors, k, bridgePenalty)` → results; map to DTO with `Provenance.RetrievedVia = r.RetrievedVia`. (`Search`/`Neighbors` errors logged + sanitized; both wrapped in the existing `recover`.)

- [ ] **Step 1 — failing tests** (`blend_test.go`): (a) seed sim 0.8 + link neighbor weight 0.5 → link score 0.40, via "link"; entity neighbor → 0.8×0.5=0.40 via "entity:X"; (b) memory reachable as both seed(0.6) and link(0.4×...) → keeps 0.6/"vector"; (c) dedup by id + top-k truncation + deterministic tiebreak. Plus (`recall_test.go`) handler passes seeds→Neighbors→blend and maps `retrieved_via`.
- [ ] **Step 2 — run, expect FAIL** (`blend`/`Neighbors`/`RetrievedVia` DTO undefined).
- [ ] **Step 3 — implement** blend + handler wiring + DTO field.
- [ ] **Step 4 — run, expect PASS**; build; lint
- [ ] **Step 5 — commit** `feat(mcp): recall expansion + propagated blend + retrieved_via`

---

### Task 7: M2 live integration

**Files:** Create `mcp/m2_integration_test.go` (`//go:build integration`).

- [ ] **Step 1 — tests** (live, unique namespaces, via `liveHandlers`): (a) remember two very similar memories → the second auto-links to the first (verify a `[:LINKS]` edge via a raw count); (b) remember A ("Portkey is our gateway") and B ("we route LLM calls through the gateway") so B links to A; a query matching only B still surfaces A **via link**; (c) two memories sharing an explicit entity, one matches the query → the other surfaces with `retrieved_via` starting `entity:`; (d) scoping: entity bridge excluded when recall is scoped to the querying namespace.
- [ ] **Step 2 — run** (stack up): `go test -tags integration ./...` PASS; default `go test ./...` still green.
- [ ] **Step 3 — commit** `test(mcp): M2 graph expansion integration`

---

### Task 8: docs

**Files:** Modify `README.md` (recall now returns `provenance.retrieved_via`; note auto-linking + associative expansion). Update `docs/engram-prd-v1.md` §9 M2 to reflect DR-1 resolved if not already.

- [ ] **Step 1 — edit docs**; **Step 2 — commit** `docs: M2 graph (auto-link + expansion)`

## Self-review
- **Spec coverage:** auto-link (Task 5 + 2), explicit links (5), 1-hop + entity-bridge expansion (3 + 6 + 7), propagated scoring (6), retrieved_via (1 + 6), bounded expansion caps (3), namespace scoping (3 + 7), DR-1 (docs) — covered.
- **Type consistency:** `Store` interface (remember.go) final = `Put, Search, Reinforce, LinkEntities, Link, Neighbors`, all on `*neo4j.Store` and `mock.FakeStore`; `Link`/`Neighbor`/`RetrievedVia` (Task 1) used by Tasks 2/3/5/6.
- **Deferred correctly:** no weighted recency/importance blend, no rerank, no decay/reinforce-on-access.
