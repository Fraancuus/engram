# Engram M0 Implementation Plan — "Skeleton + Embed-a-String End-to-End"

> Generated 2026-06-28 by the `engram-m0-design` ultracode workflow (parallel PRD/rules/scaffold
> readers → architect port design + scoping-ux scope guard → synthesized plan). This is the working
> reference for the M0 milestone; the task list in §H is executed roughly one step per loop iteration.

> Milestone goal (PRD §9): stand up the runtime substrate (Neo4j + TEI sidecar), define the domain core (types + 5 ports), and prove the wiring with a single end-to-end flow: **embed a string through the `Embedder` port against the live TEI sidecar, persist the resulting `:Memory` node to Neo4j, and read it back.** Nothing more. No tool handlers, no decay sweep, no recall pipeline.

## Locked assumptions (stated up front)
- **Docker Desktop** available locally (v28, Compose v2).
- **Store** = Neo4j (DR-1 is revisited at M2, not now).
- **Inference sidecar** = HF **Text Embeddings Inference (TEI)**.
- **Embedding model** = **BGE-small-en-v1.5, 384-dim** (recorded in the schema, swappable).
- **Module** `github.com/Fraancuus/engram`, **Go 1.26**.
- **MCP framework** = official `modelcontextprotocol/go-sdk` (already wired in `mcp/server.go`; serves **zero tools** at M0 — this is correct, not a bug).

## Repo state this plan builds on (verified)
- `engram.go` is a **package anchor only** — no types or ports declared yet.
- `go.mod` lists only the MCP SDK; the **Neo4j driver is not yet a dependency**.
- `inference/`, `neo4j/`, `mock/` are `doc.go` stubs; `cmd/engramd/main.go` and `cmd/eval/main.go` log stubs.
- `.golangci.yml` depguard binds the dependency rule to filenames **`engram.go`, `memory.go`, `decay.go`** (plus `**/` globs). **Consequence:** any domain declaration placed in a file named `types.go`/`ports.go` would escape the boundary check. **M0 keeps all domain code in `engram.go`** (covered); if it is ever split, the new filenames must be added to the depguard `files:` list in the same commit.
- CI jobs: `lint`, `test` (`go test -race ./...`), `build`, `vuln`, `eval` (stub, exits 0). **There is no service-backed integration job.** The M0 acceptance flow needs a live Neo4j + TEI, so all tests that require services are placed behind a `//go:build integration` tag and excluded from the default `go test -race ./...` — the CI `test` job stays green without containers. (Adding a compose-backed integration job is M1 housekeeping, noted in §H.)

---

## (A) M0 goal + acceptance test

**Acceptance criterion (PRD §9, made executable):** with `docker compose up` healthy and the schema applied, running `engramd` once:
1. calls `Embedder.Embed(ctx, "<fixed test string>")` against the **live TEI sidecar** and receives a 384-dim `Vector`;
2. constructs a `Memory` (type `semantic`, namespace `work/engineering`, injected `CreatedAt`/`LastAccessed` from the `Clock`);
3. `MemoryStore.Put`s it to Neo4j (parameterized Cypher);
4. `MemoryStore.Get`s it back and confirms id + embedding length round-trip;
5. logs success and exits 0.

**Encoded as a test:** `e2e_embed_test.go` (build tag `integration`) drives the same path through the real `inference.Client` and `neo4j.Store`. It is the single source of truth for "M0 done." It runs locally and in a future CI integration job, never in the default unit run.

**Out of scope for M0 (deferred, do not build):** `remember`/`recall`/`forget`/`memory_stats` handler bodies, dedup, supersession, auto-linking, MENTIONS edges, vector kNN/expansion, score blend, rerank wiring, the decay sweep, `DecayModel` *implementation*, `mock` fakes, and any per-namespace behavior. Interfaces for these land as shapes only where noted.

---

## (B) Finalized domain core (all in `engram.go`)

Types and the 5 ports are declared in `engram.go` so depguard's boundary applies. Interfaces are **defined here because they are the shared contracts between the adapters and the eval** — a deliberate, bounded exception to "define interfaces where consumed." Role interfaces invented later (Recaller, Forgetter, Sweeper, StatsReporter) live at *their* consumers, never here.

### Types
```go
type MemoryType string

const (
    Episodic   MemoryType = "episodic"
    Semantic   MemoryType = "semantic"
    Procedural MemoryType = "procedural"
)

type MemoryID string
type Namespace string
type Vector []float32 // float32 matches TEI output and Neo4j's native vector index

type Memory struct {
    ID           MemoryID
    Namespace    Namespace
    Type         MemoryType
    Content      string
    Embedding    Vector
    Importance   float64 // 0–1; default 0.5 applied at the remember boundary (M1), NOT via zero value
    Stability    float64 // S; type-keyed S0 at insert, bumped on reinforce
    AccessCount  int
    CreatedAt    time.Time
    LastAccessed time.Time
    Source       string
    Superseded   bool
}

type Entity struct { // cross-namespace bridge node; intentionally NOT namespaced
    ID   string
    Name string
}
```
`Memory` is exactly the persisted `:Memory` node (PRD §6.1). **Retrievability is derived, never stored** (`DecayModel.Retrievability(m, now)`). **Score and provenance are recall outputs**, landing on a future `RecallResult` at M2 — they are not `Memory` fields. No `Pinned` field at M0 (resolved on the forget path at M4).

### Ports (5)
```go
type Clock interface {
    Now() time.Time
}

type Embedder interface {
    Embed(ctx context.Context, text string) (Vector, error)
}

type Reranker interface {
    Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

type DecayModel interface {
    Retrievability(m Memory, now time.Time) float64
    Stability(t MemoryType, accessCount int, importance float64) float64
}

type MemoryStore interface {
    Put(ctx context.Context, m Memory) error
    Get(ctx context.Context, id MemoryID) (Memory, error)
}

var ErrNotFound = errors.New("memory not found")
```
**M0 implements only `Embedder` (TEI) and `MemoryStore` (Neo4j).** `Clock` gets a 3-line production impl wired in `main()`. `Reranker` and `DecayModel` are **interface shapes only** at M0 — no concrete implementation (Reranker client is M3; DecayModel math + sweep are M4). `MemoryStore` stays a 2-method persistence port and **must not grow into a god-interface**; kNN/reinforce/prune/stats arrive as their own small interfaces at their consumers, all satisfied by the one `*neo4j.Store`.

**Clock injection (the single most important rule):** `DecayModel` takes `now time.Time` and holds no `Clock`; the `Clock` is injected one layer up and `Now()` is read once at the orchestration edge. The decay math stays pure and table-testable. Honored structurally, not by convention.

---

## (C) docker-compose shape (describe, not the full file)

`docker-compose.yml` at repo root, two services + named volumes; both expose healthchecks so `engramd` and the integration tests can wait on readiness.

- **`neo4j`**
  - image: `neo4j:5.26-community` (LTS; native vector index is core, no plugin required).
  - ports: `7474:7474` (HTTP browser), `7687:7687` (Bolt).
  - env: `NEO4J_AUTH=neo4j/<dev-password>`, memory pagecache/heap left default.
  - volumes: `neo4j-data:/data` (persist graph), optional `./schema:/schema:ro` for the init step.
  - healthcheck: poll Bolt readiness, e.g. `cypher-shell -u neo4j -p <pw> "RETURN 1"` (or wget `http://localhost:7474`), `interval 10s`, `retries 12`.
- **`tei`** (embeddings)
  - image: `ghcr.io/huggingface/text-embeddings-inference:cpu-1.6` (CPU build; GPU is out of v1 scope).
  - command/env: `--model-id BAAI/bge-small-en-v1.5` (384-dim).
  - ports: `8080:80` (TEI serves on container port 80).
  - volumes: `tei-cache:/data` (cache the model so restarts are fast).
  - healthcheck: GET `/health` on the container port, `interval 10s`, `retries 12`.
- **`schema`** (optional one-shot, recommended for reproducibility)
  - image: `neo4j:5.26-community` (reuse for `cypher-shell`).
  - `depends_on: neo4j (condition: service_healthy)`.
  - command: `cypher-shell -a neo4j://neo4j:7687 -u neo4j -p <pw> -f /schema/001_init.cypher`; runs once and exits 0.
  - Alternative if the one-shot is skipped: document `make schema` / a manual `cypher-shell -f schema/001_init.cypher`.

Volumes: `neo4j-data`, `tei-cache`. Document the env (`NEO4J_URI=neo4j://localhost:7687`, `NEO4J_USER`, `NEO4J_PASSWORD`, `TEI_URL=http://localhost:8080`) in the README and read them in `main()`.

---

## (D) Cypher schema (`schema/001_init.cypher`)

Idempotent (`IF NOT EXISTS`), records the embedding dimension and similarity function so the model choice is versioned and swappable.

```cypher
// Identity / uniqueness
CREATE CONSTRAINT memory_id IF NOT EXISTS
  FOR (m:Memory) REQUIRE m.id IS UNIQUE;
CREATE CONSTRAINT entity_id IF NOT EXISTS
  FOR (e:Entity) REQUIRE e.id IS UNIQUE;

// Native vector index — dim + similarity recorded here (BGE-small = 384, cosine)
CREATE VECTOR INDEX memory_embedding IF NOT EXISTS
  FOR (m:Memory) ON (m.embedding)
  OPTIONS { indexConfig: {
    `vector.dimensions`: 384,
    `vector.similarity_function`: 'cosine'
  }};

// Cheap scalar indexes for the namespace/type filtering recall will need (M1)
CREATE INDEX memory_namespace IF NOT EXISTS FOR (m:Memory) ON (m.namespace);
CREATE INDEX memory_type      IF NOT EXISTS FOR (m:Memory) ON (m.type);
```

`:Memory` key properties written at M0: `id`, `namespace`, `type`, `content`, `embedding` (LIST<FLOAT>), `importance`, `stability`, `access_count`, `created_at`, `last_accessed`, `source`, `superseded`. `:Entity` (`id`, `name`) and `[:MENTIONS]`/`[:LINKS]` edges are defined in the constraint set but **not written** by any M0 code — the entity/link write path is M1.

---

## (E) neo4j `MemoryStore` adapter sketch

Parameterized Cypher only; driver imported under the `neo4jdriver` alias (the driver's package is also named `neo4j`). Only `Put` and `Get` exist at M0.

```go
package neo4j

import (
    "context"
    "fmt"

    "github.com/Fraancuus/engram"
    neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var _ engram.MemoryStore = (*Store)(nil) // compile-time port satisfaction

type Store struct {
    driver neo4jdriver.DriverWithContext
    db     string // "" = default database
}

func New(ctx context.Context, uri, user, pass string) (*Store, error) {
    d, err := neo4jdriver.NewDriverWithContext(uri, neo4jdriver.BasicAuth(user, pass, ""))
    if err != nil {
        return nil, fmt.Errorf("neo4j connect %q: %w", uri, err)
    }
    if err := d.VerifyConnectivity(ctx); err != nil {
        return nil, fmt.Errorf("neo4j verify %q: %w", uri, err)
    }
    return &Store{driver: d}, nil
}

func (s *Store) Close(ctx context.Context) error { return s.driver.Close(ctx) }

func (s *Store) Put(ctx context.Context, m engram.Memory) error {
    // MERGE on id so re-running the M0 flow is idempotent. Embedding written via
    // db.create.setNodeVectorProperty so it lands in the native vector index.
    const q = `
        MERGE (m:Memory {id: $id})
        SET m.namespace = $namespace, m.type = $type, m.content = $content,
            m.importance = $importance, m.stability = $stability,
            m.access_count = $access_count, m.created_at = $created_at,
            m.last_accessed = $last_accessed, m.source = $source,
            m.superseded = $superseded
        WITH m CALL db.create.setNodeVectorProperty(m, 'embedding', $embedding)`
    params := map[string]any{
        "id": string(m.ID), "namespace": string(m.Namespace), "type": string(m.Type),
        "content": m.Content, "importance": m.Importance, "stability": m.Stability,
        "access_count": m.AccessCount, "created_at": m.CreatedAt,
        "last_accessed": m.LastAccessed, "source": m.Source, "superseded": m.Superseded,
        "embedding": toFloat64(m.Embedding), // sole float32->float64 conversion, at the store boundary
    }
    _, err := neo4jdriver.ExecuteQuery(ctx, s.driver, q, params,
        neo4jdriver.EagerResultTransformer, neo4jdriver.ExecuteQueryWithDatabase(s.db))
    if err != nil {
        return fmt.Errorf("put memory %q: %w", m.ID, err)
    }
    return nil
}

func (s *Store) Get(ctx context.Context, id engram.MemoryID) (engram.Memory, error) {
    // returns engram.ErrNotFound when no row; maps record -> engram.Memory.
    // ...parameterized MATCH (m:Memory {id:$id}) RETURN m...
}
```
- **All input is bound via `params`** — no string-built Cypher. (gosec/CodeQL + security-auditor cover this.)
- `Vector` (`[]float32`) → `[]float64` happens once, here, because the driver/Cypher temporal+list mapping is float64; the domain keeps float32 to match TEI and the index config.
- `Get` returns `engram.ErrNotFound` so callers branch with `errors.Is`.
- Constructor returns the concrete `*Store`; callers depend on the narrow `engram.MemoryStore`.

---

## (F) TEI `Embedder`/`Reranker` client sketch

HTTP client behind the `Embedder` port, context-first. **Only `Embed` is implemented at M0.** `Reranker` is interface-only until M3 (no `var _ engram.Reranker` assertion yet — adding one before the method exists would fail to compile, which is the correct signal that it is deferred).

```go
package inference

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/Fraancuus/engram"
)

var _ engram.Embedder = (*Client)(nil)

type Client struct {
    http    *http.Client
    baseURL string
    dim     int // expected embedding dimension; 384 for BGE-small, validated on response
}

func New(baseURL string, opts ...Option) *Client { /* default http.Client, dim=384 */ }

func (c *Client) Embed(ctx context.Context, text string) (engram.Vector, error) {
    body, _ := json.Marshal(map[string]any{"inputs": text})
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("embed build request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("embed call: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("embed: tei status %d", resp.StatusCode) // no body leak
    }
    var out [][]float32 // TEI returns an array of vectors, one per input
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, fmt.Errorf("embed decode: %w", err)
    }
    if len(out) != 1 || len(out[0]) != c.dim {
        return nil, fmt.Errorf("embed: want 1x%d vector, got %dx%d", c.dim, len(out), vecLen(out))
    }
    return engram.Vector(out[0]), nil
}
```
- `ctx` first; cancellation/deadline propagate through `http.NewRequestWithContext`.
- Dimension validated against `c.dim` (384) so a model/index mismatch fails loud, not silently corrupt.
- Errors are wrapped with what we were doing and **never echo the TEI response body** (no internal/info leak across a boundary).
- This is the **richest unit-testable surface at M0** — fully exercised with `httptest`, no live sidecar needed (see §H step 3).

---

## (G) `cmd/engramd` main() hand-wiring

No DI container. Read config from env, build the two adapters + a real `Clock`, run the one flow, exit. The `systemClock` lives here (the injection point, not decay logic — does not violate the no-`time.Now()` rule).

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/Fraancuus/engram"
    "github.com/Fraancuus/engram/inference"
    eneo4j "github.com/Fraancuus/engram/neo4j"
)

type systemClock struct{}
func (systemClock) Now() time.Time { return time.Now() }
var _ engram.Clock = systemClock{}

func main() {
    ctx := context.Background()
    var clock engram.Clock = systemClock{}

    var embedder engram.Embedder = inference.New(env("TEI_URL", "http://localhost:8080"))

    store, err := eneo4j.New(ctx,
        env("NEO4J_URI", "neo4j://localhost:7687"),
        env("NEO4J_USER", "neo4j"), os.Getenv("NEO4J_PASSWORD"))
    if err != nil {
        log.Fatalf("engramd: store init: %v", err)
    }
    defer store.Close(ctx)

    // M0 end-to-end proof: embed -> build Memory -> Put -> Get.
    vec, err := embedder.Embed(ctx, "engram m0 wiring check")
    if err != nil {
        log.Fatalf("engramd: embed: %v", err)
    }
    now := clock.Now()
    m := engram.Memory{
        ID: "m0-smoke", Namespace: "work/engineering", Type: engram.Semantic,
        Content: "engram m0 wiring check", Embedding: vec,
        Importance: 0.5, CreatedAt: now, LastAccessed: now, Source: "engramd-m0",
    }
    if err := store.Put(ctx, m); err != nil {
        log.Fatalf("engramd: put: %v", err)
    }
    got, err := store.Get(ctx, m.ID)
    if err != nil {
        log.Fatalf("engramd: get: %v", err)
    }
    log.Printf("engramd: M0 OK — stored %s, %d-dim embedding round-tripped", got.ID, len(got.Embedding))

    // MCP server still registers ZERO tools at M0 — handlers arrive at M1. Not served here yet.
}
```
The MCP server (`mcp.NewServer`) is intentionally **not** served at M0 (it has no tools); stdio serving is wired when the first tool lands at M1. The binary logs its milestone state clearly so a developer connecting an agent doesn't debug a non-bug.

---

## (H) Ordered, test-first task list

Each step is independently buildable and lint-clean on its own; roughly one step per coding-loop iteration. Default green gate everywhere: `go build ./...`, `go test -race ./...`, `golangci-lint run ./...`. Steps needing live services are tagged `integration` and excluded from the default unit run. Honesty note: pure type/interface declarations have no meaningful behavior to TDD — their gate is build + lint + compile-time `var _` assertions; the steps that *do* have logic (the TEI client, the store round-trip) lead with table-driven tests.

1. **Add the Neo4j driver dependency.**
   `go get github.com/neo4j/neo4j-go-driver/v5@latest`, `go mod tidy`.
   *Gate:* `go build ./...` clean; `go.mod`/`go.sum` updated. No code change.

2. **Domain core in `engram.go`** — `MemoryType` (+3 constants), `MemoryID`, `Namespace`, `Vector`, `Memory`, `Entity`, the 5 port interfaces, `ErrNotFound`.
   *Test first:* `engram_test.go` — a small table-driven test asserting the three `MemoryType` constants stringify to `"episodic"/"semantic"/"procedural"` (guards silent DB-string drift — the one honest unit test for this step).
   *Gate:* build + lint; depguard (engram.go is covered) confirms no infra import sneaks in. **Keep everything in `engram.go`** — do not create `types.go`/`ports.go` (depguard blind spot).

3. **TEI `Embedder` client (`inference/client.go`).**
   *Test first:* `client_test.go` with `httptest.Server`, table-driven cases — happy path (returns 384-dim vector), HTTP 503/429, malformed JSON, empty/zero-vector response, **dimension mismatch** (e.g. 768 vs 384), and **context cancellation** (cancel mid-request, assert wrapped error). No live sidecar.
   *Then implement:* `Client`, `New`/`Option`, `Embed`, and `var _ engram.Embedder = (*Client)(nil)`.
   *Gate:* `go test -race ./inference/...` green offline.

4. **docker-compose + schema.** Author `docker-compose.yml` (§C) and `schema/001_init.cypher` (§D), plus README env docs.
   *Gate (manual, not CI):* `docker compose up -d` → both services report healthy; schema one-shot exits 0; `cypher-shell "SHOW INDEXES"` lists `memory_embedding` with `vector.dimensions: 384`, `cosine`.

5. **neo4j `Store` adapter (`neo4j/store.go`)** — `New`, `Close`, `Put`, `Get`, `toFloat64`, `var _ engram.MemoryStore = (*Store)(nil)`.
   *Test first:* `store_integration_test.go` (`//go:build integration`) — table-driven round-trip: `Put` then `Get` returns the same id + embedding length; `Get` of a missing id returns `engram.ErrNotFound` (assert with `errors.Is`).
   *Gate:* default `go test -race ./...` still green (tag excluded); `go test -tags integration -race ./neo4j/...` green with compose up. Build + lint clean regardless.

6. **`cmd/engramd` hand-wiring (§G)** — `systemClock`, env reader, adapter construction, the embed→Put→Get flow, clear M0 log line. Replace the stub.
   *Gate:* build + lint; `go vet` clean. (Behavior validated in step 7.)

7. **End-to-end acceptance test (§A)** — `e2e_embed_test.go` (`//go:build integration`) exercising the real `inference.Client` + `neo4j.Store` against live compose: embed the fixed string, `Put`, `Get`, assert id + 384-dim embedding. This *is* the PRD §9 acceptance criterion encoded.
   *Gate:* `go test -tags integration -race ./...` green with compose up; equivalently, `go run ./cmd/engramd` logs `M0 OK`.

8. **M0 housekeeping / docs.** README "Run M0" section (compose up → schema → `go run ./cmd/engramd`), a one-line note that the MCP server serves zero tools until M1, and a TODO to add a compose-backed CI integration job (so `-tags integration` runs in CI later). If any domain file is ever split out of `engram.go`, add its name to the `.golangci.yml` depguard `files:` list in the same change.
   *Gate:* docs only; full suite (`build`, `test -race`, `lint`, `vuln`) green.

### Carried forward (explicitly deferred from M0)
- **provenance** is undefined in the PRD (§8 output field, no schema). Resolve and document its contents (origin tool-call? source-memory id chain?) **before M1** so the recall result type isn't designed under pressure.
- **`forget` mode=pin** reads as contradictory; decide before M1 tool registration whether pin is a `remember`/importance concern rather than a `forget` mode (changing the MCP surface after agents depend on it is costly).
- **`include_forgotten`** must be documented as "include *soft*-forgotten (below `SOFT_THRESHOLD`, recoverable)," not hard-forgotten, in its JSON-schema description.
- Reranker client (M3), DecayModel implementation + decay sweep (M4), `mock` fakes (M5 eval prep), recall/remember pipeline bodies (M1–M3).

---

## Appendix A — Design rationale (architect judgment calls)

- **`MemoryStore` stays a 2-method persistence port (Put/Get).** Recall, forget, stats, and the decay sweep each get their own small interface (Recaller, Forgetter, StatsReporter, Sweeper) defined AT their consumer (mcp handlers, the sweep loop) in M1+, all met by one `*neo4j.Store`. Rejected alternative: one fat MemoryStore with ~12 methods. Risk: five tiny interfaces look like .NET ceremony; mitigated because each is defined where used and there is exactly one implementation.
- **Retrievability (R) is DERIVED** via `DecayModel.Retrievability(m, now)`, never a `Memory` field. A stored R goes stale every tick and would let recall read a lie; an index snapshot, if needed, is an infra concern the sweep persists, not a domain field.
- **Score and provenance are recall OUTPUTS**, not `Memory` fields — they land on a separate `RecallResult` when recall is designed at M2. Keeps `Memory` from being empty everywhere except inside one pipeline.
- **`DecayModel` takes `now time.Time` and holds no Clock**; the Clock is injected one layer up, called once, and `now` passed in. The precise reading of "inject the clock" — math stays pure and deterministic.
- **Stability modeled as pure `S(type, accessCount, importance)`** — initial S0 is accessCount=0, reinforcement is a higher accessCount. Flagged: if reinforcement becomes path-dependent (spaced-repetition timing), the signature must also take the prior S.
- **Uniform vs type-aware decay = two implementations of `DecayModel` selected in `main()`**, never a bool flag inside one model. The eval's conditions B (uniform) and C (type-aware) are just different wired DecayModels — this protects the eval.
- **Thresholds (SOFT_THRESHOLD, HARD_FLOOR, GRACE_PERIOD, DEDUP/LINK) are policy/config consumed by the sweep**, NOT methods on DecayModel. The pure model only produces R.
- **`Embedder` is singular `Embed(ctx, text)` for M0** — narrowest seam. Batch `Embed(ctx, []string)` deferred; cheap to change later with ~no consumers.
- **`Reranker` returns `[]float64` aligned to input docs, takes `[]string` not `[]Memory`** — the domain owns the blend/ordering; the cross-encoder needs only text.
- **`Vector` is `[]float32`** — matches TEI and the Neo4j vector index; float64 precision is wasted on cosine and forces a lossy/expensive store-boundary conversion.
- **`Entity` is minimal (ID, Name) and intentionally NOT namespaced** — entities are the cross-namespace bridge; the PRD doesn't enumerate entity schema, so adding fields now is inventing scope.
- **Default importance 0.5 is applied at the remember (MCP) boundary, not via struct zero value** — defaulting and bounds-checking are the untrusted-input handler's job.
- **Omit a `Pinned` field at M0** — the PRD conflates pin with importance==1.0 OR an explicit flag; resolve at M4 on the forget path.
- **Accepted tension:** the five named ports are centralized in `engram` by deliberate architectural decree (CLAUDE.md repo map), slightly bending "define interfaces where consumed." Accept it for exactly these five contracts shared by adapters and the eval. All other role interfaces live at their consumers.
- **Rejected over-engineering, by name:** no `DecayConfig` struct yet; no DI/wiring framework; no generic `Repository[T]`; no Embedder model/dimension accessors; no `Storer` interface inside the neo4j package; no `MemoryType.Valid()` until the M1 remember handler actually validates untrusted input.

## Appendix B — Scope review (scoping-ux)

**Keep (in M0):** domain types in `engram.go`; all 5 ports as interface definitions; `docker-compose.yml` (Neo4j + TEI); Cypher schema (vector index dim=384 cosine + constraints); minimal `inference.Client.Embed` over TEI HTTP; minimal `neo4j` insert/`Put` + `Get`; `cmd/engramd` hand-wiring of the embed→store→read flow.

**Defer:** `remember` body (M1); `recall` body (M1 vector / M2 graph); `forget` + `memory_stats` bodies (M4/M6); cross-encoder rerank impl (M3); decay sweep goroutine (M4); supersession logic (M1–M4); `mock` fakes (M5 prep); per-namespace behavior (v2); LLM extraction (v2); consolidation/summarization (v2).

**Missing (must add in M0):** the `engram.go` types + 5 ports (file is currently an anchor only); `docker-compose.yml`; Cypher schema; `github.com/neo4j/neo4j-go-driver/v5` in `go.mod`; `inference.Client`; **provenance field definition** (PRD §8 references it but never defines it — resolve before M1).

**Risks:**
- `MemoryStore` over-specification — define only what the M0 proof exercises (`Put`/`Get`); grow milestone-by-milestone.
- `forget` conflates four ops under a mode enum; `mode=pin` reads as contradictory — reconsider before M1 tool registration.
- MCP server serves zero tools at M0 — README + binary log must say so, or it looks broken.
- docker-compose absence makes M0 unverifiable for non-authors; add a compose-backed CI integration job when the compose file lands.
- `include_forgotten` is ambiguous — its JSON-schema description must say "soft-forgotten, recoverable."
- depguard guards by filename (`engram.go`/`memory.go`/`decay.go`); domain logic in any other filename escapes the rule — keep domain code in those files or extend the `files:` list in the same commit.
