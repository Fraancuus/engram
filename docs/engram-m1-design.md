# Engram M1 Design — "Core loop: remember + recall over MCP"

> Status: approved 2026-06-28 (brainstorm). Goal (PRD §9 M1): `remember` + `recall`
> (vector-only, namespace-filtered) over MCP, with dedup and type + namespace + entities
> written — a working store→search demo. Builds on the M0 skeleton (domain core, TEI
> `Embedder`, Neo4j `MemoryStore`, schema, wiring).

## Scope

**In:** the two write/read MCP tools served over stdio; vector-only recall; dedup on
write; `:Entity` nodes + `[:MENTIONS]` edges written from explicit input.

**Out (deferred):**
- Graph expansion / entity-bridge traversal in recall, auto-linking, explicit `links` → **M2**
- Cross-encoder rerank → **M3**
- Decay, supersession, reinforcement-by-decay, `forget`, `memory_stats`, `include_forgotten` → **M4 / M6**
- LLM entity extraction → **v2** (entities are explicit input only)

## MCP tool contracts (registered via the SDK's typed `mcp.AddTool`)

### `remember`
| Field | In/Out | Notes |
|---|---|---|
| `content` | in (required) | the memory text; non-empty, length-bounded |
| `type` | in (required) | one of `episodic` / `semantic` / `procedural` |
| `namespace` | in (required) | soft universe, e.g. `work/engineering`; non-empty, bounded |
| `importance` | in (optional) | 0–1, default **0.5** applied at this boundary |
| `source` | in (optional) | provenance hint, e.g. tool/agent name |
| `entities` | in (optional) | string names → `:Entity` nodes + `[:MENTIONS]` |
| `memory_id` | out | id of the stored (or deduped-onto) memory |
| `deduped` | out | true if it matched an existing memory and reinforced it |

`superseded` (M4) and `links` (M2) are intentionally omitted from the M1 surface.

### `recall`
| Field | In/Out | Notes |
|---|---|---|
| `query` | in (required) | text to embed and search |
| `namespaces` | in (optional) | restrict to these universes; empty = all |
| `k` | in (optional) | result count, default **10**, clamped to [1, 100] |
| results | out | ranked `[{id, content, score, type, namespace, provenance}]` |
| `provenance` | out (per result) | `{source, created_at, last_accessed, access_count}` — a projection of the memory's own fields |

`include_forgotten` is omitted — nothing is forgotten until M4.

## Behavior

**Write path (`remember`):**
1. Validate + default inputs (policy gate, below).
2. `Embed(content)` → `Vector`.
3. **Dedup:** vector kNN top-1 within the same `namespace`; if cosine ≥ `DEDUP_THRESHOLD`
   (default **0.95**, configurable), **reinforce** the existing node (`access_count++`,
   `last_accessed = now`), skip insert, return its id with `deduped: true`.
4. Else insert a new `:Memory` (`importance` defaulted, `access_count = 0`, `created_at =
   last_accessed = now` from the injected `Clock`, `stability` left as a placeholder — M4
   sets the type-keyed S₀), then MERGE `:Entity` nodes and `[:MENTIONS]` edges from `entities`.

**Read path (`recall`):**
1. Validate + clamp inputs.
2. `Embed(query)` → `Vector`.
3. Neo4j vector-index kNN (`db.index.vector.queryNodes('memory_embedding', …)`),
   **namespace-filtered**. Over-fetch (e.g. k × oversample) then filter to the requested
   namespaces and take top-k, so the namespace cut doesn't starve results.
4. Map to `RecallResult` and project provenance for the response.

## Code shape (respecting the M0 architecture)

- **Domain (`engram`):** add `RecallResult struct { Memory; Score float64 }`. `MemoryStore`
  stays minimal (`Put`/`Get`) — new capabilities do NOT bloat it.
- **New store capabilities** as small interfaces **defined at their consumer** (the mcp
  handlers), all satisfied by the one `*neo4j.Store`:
  - `Search(ctx, namespaces []Namespace, vec Vector, k int) ([]RecallResult, error)` — recall + dedup
  - `Reinforce(ctx, id MemoryID, now time.Time) error` — dedup hit
  - `LinkEntities(ctx, id MemoryID, names []string) error` — entity write
- **`mcp` package:** `NewServer` grows to accept the ports (`Embedder`, the store
  interface(s), `Clock`) and registers `remember` + `recall`. Handlers own the **policy
  gate** and keep errors agent-legible (never leak internals/driver errors).
- **`cmd/engramd`:** replace the M0 smoke flow with adapter wiring + `mcp.NewServer(...)` +
  `server.Run(ctx, &mcpsdk.StdioTransport{})` — the real service. The M0 embed→store proof
  remains as the integration test.
- **Fakes:** M1 handler unit tests are the first real consumer of domain-port fakes, so
  minimal `Embedder`/store fakes land now (in `mock/`), earlier than the M5 eval note.

## Policy gate (untrusted MCP input)

Validated at the handler boundary before anything touches the domain:
- `type` ∈ the known `MemoryType` set; reject otherwise.
- `namespace` non-empty and length-bounded (a soft universe in v1 — **not** a fixed
  whitelist; that would contradict agent-created universes).
- `content` non-empty, length-bounded.
- `k` clamped to [1, 100], default 10.
- `entities` count-capped and each name length-bounded.
- Cypher remains 100% parameterized; errors crossing back are legible, never internal.

## Decisions / defaults

- **provenance = rich** (`source, created_at, last_accessed, access_count`) — a projection
  of existing `Memory` fields, no new storage.
- **entities = explicit input** in M1; traversal/bridges are M2.
- `DEDUP_THRESHOLD = 0.95` (cosine), configurable.
- `default k = 10`, `max k = 100`.
- `Stability` is an inert placeholder at M1; M4 owns the type-keyed S₀.

## Testing

- **Unit (no services):** handler input validation (k clamp, type/namespace/content/entity
  bounds), the dedup decision, response mapping — via the `mock` fakes.
- **Integration (`//go:build integration`, live Neo4j + TEI):**
  - remember → recall round-trip (the PRD "store→search demo").
  - dedup: remembering near-identical content twice reinforces (no duplicate node).
  - entities: remember with `entities` creates `[:MENTIONS]` edges.
  - namespace filtering: recall scoped to a namespace excludes others.

## Deferred carry-forward

`links` + auto-linking + entity-bridge/1-hop recall (M2); rerank (M3); decay + supersession
+ `forget` + `memory_stats` + `include_forgotten` (M4/M6); LLM extraction (v2).
