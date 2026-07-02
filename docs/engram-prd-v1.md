# Engram — Product Requirements Document (v1 · Go edition)

> Working codename. *Engram* = the physical trace a memory leaves behind; signals the neuroscience-inspired decay angle. Rename freely.

**Status:** Draft for build
**Owner:** Franco
**Type:** Open-source portfolio + passion project
**One-liner:** A local-first, long-term memory service for AI agents — a clean concurrent **Go** service over a graph+vector store, with **type-aware forgetting**, **namespaced "universes"**, and a rigorous eval proving the forgetting actually helps.

---

## 1. Why this exists

Most "agent memory" projects are a vector store with `save()` and `search()`. They never forget, never distinguish *kinds* of memory, and — critically — **never measure whether the memory layer makes retrieval better.** The store grows unbounded, recall degrades, and there's no evidence either way.

Engram's thesis: *forgetting is a feature* — but **what** and **how fast** you forget depends on the *type* of memory. An event fades; a code standard shouldn't. v1 builds that distinction and **proves its effect with an eval.** The proof is the differentiator, not the storage.

### Signal goals (this is a portfolio piece)
- Systems engineering in **Go**: a single-binary concurrent service (goroutines for the decay sweep, `context` for timeouts), orchestrating ML inference + a graph+vector store.
- LLMOps rigor: an eval harness with a baseline, real metrics, and a CI regression gate.
- Ship a *finished*, defensible v1 with a clear architecture writeup — not a 60%-done repo.
- Learn Go properly (a stated personal goal) on a project where Go is genuinely the right tool.

---

## 2. Scope discipline

### In scope (v1)
1. Write path: store a memory with **type** + **namespace** + **entities** (embed, dedup, link).
2. Read path: hybrid retrieval (vector + 1-hop graph expansion + entity bridges) → rerank → token-budget assembly, **scopable to a namespace or spanning all**.
3. **Type-aware decay & reinforcement**: decay rate keyed by memory type; access reinforces.
4. **MCP server**: `remember`, `recall`, `forget`, `memory_stats`.
5. **Eval harness**: type-aware vs. uniform decay, decay vs. no-decay, on labeled data, in CI.

### Explicitly NOT in v1 (deferred)
- Consolidation / summarization (the "sleep" pass that merges duplicates + promotes episodic→semantic).
- **Per-namespace behavior** (custom decay rates / policies per universe). v1 stores the namespace and lets `recall` filter by it — nothing more.
- Web UI / graph visualization (beyond `memory_stats`, which feeds it later).
- LLM-based extraction of memories from raw conversation (v1 stores what the caller hands it; extraction is a flagged stretch — §9).
- GPU / NPU acceleration (CPU sidecar only in v1; iGPU/NPU is a later optimization writeup).
- Multi-tenant / auth / distributed. Single-user, single-node.

> If it isn't on the *in-scope* list, it isn't in v1. This list is the contract.

---

## 3. Stack

| Layer | Choice | Notes |
|---|---|---|
| Language | **Go** | Deliberate: ship clean + fast, learn the language. Trade vs. Rust: inference is a **sidecar**, not in-process (DR-3). |
| Concurrency | goroutines + `context` + `time.Ticker` | Decay sweep, timeouts on recall, batch workers — Go's home turf. |
| Store | **Neo4j** (native vector index + graph) | Embeddings + relationships in one store. |
| Neo4j driver | **neo4j-go-driver/v5** (official) | First-class and maintained — *removes* the driver-maturity risk Rust had. |
| Inference | **Sidecar** (HF Text Embeddings Inference *or* llama.cpp server) | Serves embeddings + cross-encoder rerank over HTTP/gRPC, CPU. See DR-3. |
| MCP | official **`github.com/modelcontextprotocol/go-sdk`** v1.x (Anthropic, with Google) — DR-4 | Typed `mcpsdk.AddTool` infers + validates each tool's JSON Schema from its Go input struct. stdio transport for v1. Imported under the `mcpsdk` alias — the SDK's own package is also named `mcp`. |
| Eval | Go `testing` + a small harness binary | Runs in CI. |

### Idiomatic-Go guardrail
Wire dependencies by hand in `main()` (no DI container), define interfaces where they're *consumed* (`accept interfaces, return structs`), keep packages small and flat. Don't rebuild .NET in Go.

### Decision records

**DR-1 — Neo4j vs. Postgres+pgvector.** Neo4j is kept *because the graph is load-bearing in v1*: entity bridges (§5) and 1-hop associative expansion are core, not decorative. Under Go the official driver removes the old risk leg, so this is now a clean choice. Fallback (if the graph proves decorative at the M2 checkpoint): **Postgres + `pgx` + pgvector + an `edges`/`entities` table.** Cheap to switch — the store sits behind a `MemoryStore` interface.

**DR-2 — CPU-only inference in v1.** Small embedding + reranker models are single-digit-to-low-tens of ms on CPU. iGPU (ROCm) / NPU is deferred to keep v1 shippable and turn the hard part into content, not a blocker.

**DR-3 — Sidecar inference (the Go trade).** Go has no in-process equivalent of Rust's fastembed-rs. So embeddings/reranking run as a **local sidecar process** (TEI or llama.cpp's `/embedding` + rerank endpoint), and the Go service calls it behind `Embedder` / `Reranker` interfaces. Alternative: `onnxruntime_go` (CGo) for no separate process. Sidecar is the ship-fast default; the interface makes it swappable. Accepted cost: one more process to run locally.

**DR-4 — MCP framework: official `modelcontextprotocol/go-sdk`.** We commit to the official Go SDK (maintained by Anthropic in collaboration with Google) over `mark3labs/mcp-go`. It is GA: v1.0.0 froze the public API with a no-breaking-changes guarantee, and v1.x is current (v1.6.1 at time of writing). Its generic `mcpsdk.AddTool[In, Out]` infers each tool's JSON Schema from typed Go structs (via `google/jsonschema-go`) and validates incoming arguments against it — which lines up cleanly with our *MCP inputs are untrusted* invariant: the inferred schema is the **type** gate, while our handlers remain the **policy** gate (`k` within limits, `namespace` against the known set, well-formed ids). The MCP surface is a thin adapter sitting behind our own handlers, so the framework stays cheap to swap if it ever stops fitting. **Gotcha:** the SDK's package is named `mcp`, colliding with our `mcp/` package — import it aliased as `mcpsdk` (the same move the neo4j driver needs).

---

## 4. Architecture (high level)

```
                 ┌──────────────────────────────────────────────┐
  MCP client  →  │  Engram — Go service (single binary)          │
  (any agent)    │   MCP: remember · recall · forget · stats     │
                 └─────┬──────────────────────────────┬──────────┘
                       │                              │ HTTP/gRPC
       ┌───────────────┼───────────────┐              ▼
       ▼               ▼               ▼      Inference sidecar
  Write path       Read path     Decay scheduler   (TEI / llama.cpp:
 (embed·dedup·  (vector→graph→   (goroutine +        embeddings +
  link·entity)   rerank→assemble) time.Ticker:       reranker, CPU)
       │               │          type-aware R,
       └───────────────┼──────────┴ soft-forget/prune)
                       ▼
             MemoryStore interface
          (Neo4j impl | Postgres impl)
                       │
                       ▼
            Neo4j (vector index + graph)
   (:Memory)-[:LINKS {w}]->(:Memory)   ← intra-namespace association
   (:Memory)-[:MENTIONS]->(:Entity)    ← cross-namespace bridges
```

Everything talks to storage through a **`MemoryStore` interface** (DR-1 can flip without touching the rest). Inference sits behind **`Embedder` / `Reranker` interfaces** (DR-3 sidecar now, GPU later).

---

## 5. The memory model (two orthogonal axes)

Every memory carries **both** a *type* (how it behaves) and a *namespace* (which universe it lives in). These are independent — "CI/CD knowledge" is `(procedural, work/engineering)`; "finance" is `(semantic, personal:finance)`.

### Axis 1 — Type (governs decay)
| Type | Examples | Decay behavior |
|---|---|---|
| **episodic** | "Deploy broke Tuesday, rolled back"; a conversation turn | **Fast** — fades with disuse; reinforced on access |
| **semantic** | "Our gateway is Portkey"; "mortgage handover is 31 July" | **Slow** — distilled state, long stability |
| **procedural / reference** | "Code standard: accept interfaces, return structs"; "how to roll back CI"; project facts | **Near-permanent** — does *not* decay with time; only on **supersession** |

### Axis 2 — Namespace ("mini universes")
Soft scopes, **not** walled gardens — hard isolation would kill the associative recall that justifies the graph. A `namespace` property on each memory; edges are mostly intra-namespace; **entities** bridge across.

Examples: `work/engineering` · `code-standards` · `project:portiq` · `project:volcano` · `personal:finance` · `personal:life`.

`recall` can scope to one universe, a set, or span all. (This is your AEOS/Obsidian structure made queryable and self-pruning.)

### Bridges = entities
Entities are first-class nodes: `(:Memory)-[:MENTIONS]->(:Entity)`. A memory in `project:volcano` mentioning **Upstash Redis** links organically to anything else mentioning it — including in other namespaces. Entities are what make cross-universe associative recall happen without hand-wiring every connection.

---

## 6. Functional requirements

### 6.1 Write path — `remember`
Input: `content`, `type` (`episodic`|`semantic`|`procedural`), `namespace`, optional `importance` (0–1, default 0.5), `source`, `entities[]`, explicit `links[]`.
Behavior:
1. Embed `content` (sidecar).
2. **Dedup**: cosine sim ≥ `DEDUP_THRESHOLD` to an existing memory *in the same namespace* → reinforce existing, return its id.
3. **Supersession (procedural):** if a new procedural memory shares namespace + a key entity with an existing one and is flagged a replacement, mark the old `superseded` (begins its decay/archival). This is how reference memory "forgets" — by being replaced, not by time.
4. Insert `:Memory` with: `created_at`, `last_accessed`, `access_count=0`, `importance`, `stability` (type-keyed `S₀`), `type`, `namespace`, embedding.
5. **Link**: explicit links; auto-link to top-K nearest neighbors above `LINK_THRESHOLD` (intra-namespace, edge weight = similarity); `MENTIONS` edges to each entity (creating `:Entity` nodes as needed).

### 6.2 Read path — `recall`
Input: `query`, optional `namespaces[]` (default: all), `k` (default 8), `include_forgotten` (default false).
Pipeline:
1. Embed query → **vector kNN** (Neo4j vector index), filtered to `namespaces` if scoped; top `N` (~50).
2. **Associative expansion:** 1-hop `LINKS` neighbors **+ entity bridges** (memories sharing a `MENTIONS` entity). Entity bridges may cross namespaces unless the query is scoped. *This is why the graph exists.*
3. **Score blend:** `score = w_sim·sim + w_rec·recency + w_imp·importance + w_ret·retrievability`. Weights configurable.
4. **Cross-encoder rerank** the blended top-M (sidecar); take final `k`.
5. **Token-budget assembly:** pack under a max-token ceiling.
6. **Reinforce on access:** returned memories get `last_accessed=now`, `access_count++`, `stability` bumped; small edge-weighted bump propagates to 1-hop neighbors.
Output: ranked memories with scores, type, namespace, provenance.

### 6.3 Type-aware decay & forgetting (the signature)
Retrievability, spaced-repetition inspired, **keyed by type**:

```
R(Δt) = exp( -Δt / S(type, access_count, importance) )
```
- `episodic`: small `S₀`, decays with disuse; reinforcement raises `S`.
- `semantic`: large `S₀`, slow decay.
- `procedural`: `S → ∞` w.r.t. time (no disuse decay); decays only when `superseded` (then drops to a fast curve toward archival).

A scheduled sweep (goroutine + `time.Ticker`, interval configurable) recomputes `R`. Two tiers:
- **Soft-forget:** `R < SOFT_THRESHOLD` → excluded from default `recall`, recoverable via `include_forgotten=true`.
- **Hard prune:** `R < HARD_FLOOR` for `> GRACE_PERIOD` → archived/deleted. Pinned (`importance=1.0`/explicit) exempt.

> The type-keying is the honest fix to one-size-fits-all decay: you stop losing code standards while still forgetting stale chatter. It is also a sharp eval question (§7).

### 6.4 `forget`
Explicit soft/hard forget by id; explicit **pin** (protect from decay); explicit **supersede** (mark replaced).

### 6.5 `memory_stats`
Counts by type × namespace, retrievability histogram, store size, sweep stats (soft-forgotten/pruned/superseded since last run), recent access patterns. Feeds the future viz; operational sanity check now.

---

## 7. The eval (this is the point)

**Questions**
1. Does decay+reinforcement retrieve *more relevant* memories from a *leaner* store than a never-forgetting baseline?
2. **Does type-aware decay beat uniform decay?** (The v1-specific claim.)

**Conditions**
- **A — Baseline:** flat retrievability, unbounded store. The "everyone else" system.
- **B — Uniform decay:** one decay curve for all memories.
- **C — Engram (type-aware decay):** per-type curves + supersession.

**Dataset:** labeled `(query, relevant_memory_ids)` over a store with **simulated time + access patterns** and a **mix of types** (frequently-hit procedural standards, write-once episodic chatter, slowly-aging semantic facts). Bootstrap from a public long-term-memory benchmark (LongMemEval / LoCoMo-style) or synthesize for full ground-truth control. Time is virtualized so a "month" runs in seconds.

**Metrics:** precision@k, recall@k, nDCG/MRR (vs. ground-truth labels); retrieval latency; store size over time; **type-stratified recall** (does C retain procedural memory that B wrongly forgets?).

**Anti-circularity rule:** the reranker is *part of the pipeline under test.* Relevance is judged against **held-out ground-truth labels**, never by the reranker scoring itself.

**CI gate:** fixed seed/dataset; build fails if precision@k regresses beyond tolerance. (Your evals-in-CI signature, on your own repo.)

> A rigorous **null result** (type-awareness doesn't help) is still a credible, publishable finding. Rigor is the deliverable, not a positive outcome.

---

## 8. MCP interface contract

| Tool | Input | Output |
|---|---|---|
| `remember` | `content`, `type`, `namespace`, `importance?`, `source?`, `entities?`, `links?` | `memory_id`, `deduped`, `superseded?` |
| `recall` | `query`, `namespaces?`, `k?`, `include_forgotten?` | ranked `[{id, content, score, type, namespace, provenance}]` |
| `forget` | `id`, `mode: soft\|hard\|pin\|supersede` | `ok` |
| `memory_stats` | `namespace?` | counts by type×namespace, retrievability histogram, sweep stats |

Transport: stdio (v1). A real agent using Engram end-to-end over MCP is the v1 acceptance demo.

Tools are registered with the official SDK's generic `mcpsdk.AddTool`, which derives each tool's input/output JSON Schema from its Go struct (`jsonschema` struct tags carry the field descriptions) and validates incoming arguments against it. That schema check is the **type** gate; the handler still enforces **policy** bounds (`k` limits, `namespace` whitelist, id shape) and returns agent-legible errors that never leak internals. Framework rationale and the package-name alias gotcha: DR-4.

---

## 9. Milestones (brutal v1 ordering)

- **M0 — Skeleton.** Go module, Neo4j up, schema (`:Memory`/`:Entity`, vector index, type+namespace props), inference sidecar standing up, `MemoryStore` + `Embedder`/`Reranker` interfaces, embed a string end-to-end.
- **M1 — Core loop.** `remember` + `recall` (vector-only, namespace-filtered) over MCP. Dedup. Type + namespace + entities written. Working store→search demo.
- **M2 — Graph.** Auto-linking + entity bridges on write; 1-hop + entity-bridge expansion in `recall`. **DR-1 re-evaluation checkpoint.**
- **M3 — Rerank.** Cross-encoder rerank + token-budget assembly. Reranker = `bge-reranker-v2-m3` on the TEI sidecar (`/rerank`), with `gte-reranker-modernbert-base` as the efficiency challenger; embedder stays `bge-small-en-v1.5` (384-dim). See [ADR-0002](adr/0002-embedding-model-and-amd-acceleration.md).
- **M4 — Type-aware decay.** Per-type retrievability, supersession, scheduled sweep, reinforcement (+ edge propagation), soft-forget/hard-prune, `forget`/pin/supersede.
- **M5 — Eval.** Mixed-type dataset; A/B/C conditions; metrics + type-stratified recall; CI gate. *The milestone that makes it credible.*
- **M6 — Polish.** `memory_stats`, README architecture + eval writeup, demo recording.

**v1 = done when:** M5 reports the measured effect of type-aware decay, a real agent uses the MCP server end-to-end, and the README tells the architecture + eval story.

---

## 10. Open questions / stretch
1. **DR-1** — proceed with Neo4j through M2, fall back to Postgres only if the graph proves decorative? (Default: Neo4j; entity bridges make it load-bearing.)
2. **Sidecar choice** — TEI vs. llama.cpp server vs. `onnxruntime_go` (CGo). Default: TEI for clean HTTP + reranker support; revisit if you want zero extra processes.
3. **Embedding model** — BGE-small (384-dim, fast) default; record dim in schema so it's swappable.
4. **Eval dataset** — synthetic (full control of decay ground truth) for v1, public benchmark as v1.1 external validation.
5. **Stretch (flagged, non-blocking):** thin LLM extraction so `remember` can take a raw turn and decide what's worth keeping + which type/namespace. Behind a feature flag.

---

## 11. Risks
| Risk | Mitigation |
|---|---|
| Sidecar = one more moving process | Single `docker-compose` (Neo4j + sidecar + Engram); `Embedder`/`Reranker` interface keeps it swappable. |
| Graph proves decorative | DR-1 fallback to Postgres+pgvector; honor the M2 checkpoint honestly. |
| Project stalls at 60% (real for a side project in a busy stretch) | Milestones independently shippable; M1 alone demos; Go's fast ramp + compile loop directly serves finishing. Minimum viable cut: M1 + M4 + M5. |
| Type-aware decay shows no gain | Valid, publishable result — rigor is the signal. |
