# Engram M2 Design — "Graph: auto-linking + associative expansion"

> Status: approved 2026-07-02 (brainstorm). Goal (PRD §9 M2): auto-linking + entity
> bridges on write; 1-hop + entity-bridge expansion in `recall`; **DR-1 resolved — keep
> Neo4j**. Builds on M1 (remember/recall over MCP, vector recall, dedup, entities written).

## Scope

**In:** auto-link on write; optional explicit `links[]`; recall associative expansion
(1-hop `[:LINKS]` + entity-bridge `[:MENTIONS]`) with **propagated scoring**;
`retrieved_via` provenance.

**Deferred:** weighted blend (recency/importance) + cross-encoder rerank + token-budget →
**M3** · retrievability, reinforce-on-access, edge-weight propagation, supersession → **M4**
· `memory_stats` → **M6**.

## Decisions (from the brainstorm)
- **Propagated scoring** (not a full weighted blend yet).
- **Keep Neo4j** — DR-1 resolved (cross-namespace entity traversal is native Cypher).
- **Add `retrieved_via`** to provenance.
- Defaults (become config at M3): `N=50` (seed set), `K=5` (auto-link neighbors),
  `LINK_THRESHOLD=0.85`, `BRIDGE_PENALTY=0.5`.

## Write path (`remember`) — after the M1 insert + entity links
1. **Auto-link:** find the new memory's top-K nearest neighbors *within its namespace*
   (reuse `Search` with `k=K+1`, drop self), keep those with `sim ≥ LINK_THRESHOLD`, and
   create `(:Memory)-[:LINKS {weight: sim}]->(:Memory)`. Directed storage, undirected
   traversal.
2. **Explicit `links[]`:** optional agent-supplied memory ids → `[:LINKS {weight: 1.0}]`
   (a target id that doesn't exist is skipped, not an error).
3. Dedup path unchanged from M1; on a dedup hit we do **not** re-auto-link (the existing
   memory already has its links).

## Read path (`recall`) — replaces M1's "return top-k"
1. Embed query → vector kNN **top-N seeds** (`N=50`, namespace-filtered), each with `sim`.
2. **Expand** the seeds via the store: 1-hop `[:LINKS]` neighbors (with edge weight) and
   entity-bridge neighbors (memories sharing a `[:MENTIONS]` entity). Bridges cross
   namespaces **unless** the query is namespace-scoped. Bounded — a per-seed link cap and
   a per-entity bridge cap keep a hub entity from exploding expansion.
3. **Propagated score** (pure function): `seed = sim`; `link = sim_source × weight`;
   `entity-bridge = sim_source × BRIDGE_PENALTY`. A memory reached multiple ways takes the
   **max** score.
4. Dedup by id, sort by score desc, take `k`; tag each result's `retrieved_via`
   (`vector` / `link` / `entity:<name>`).

## Code shape (respecting the M1 architecture)
- **Domain (`engram`):**
  - `RecallResult` gains `RetrievedVia string`.
  - `type Link struct { To MemoryID; Weight float64 }`.
  - `type Neighbor struct { Memory Memory; SourceID MemoryID; Via string; Weight float64 }`
    — an expansion result carrying which seed reached it and how.
- **`neo4j.Store`:** add
  - `Link(ctx, from MemoryID, links []engram.Link) error` — MERGE `[:LINKS]` edges.
  - `Neighbors(ctx, seedIDs []MemoryID, scope []engram.Namespace) ([]engram.Neighbor, error)`
    — one parameterized Cypher fetching link + entity-bridge neighbors with metadata; caps
    enforced in-query. `Search` is reused for seeds (recall) and neighbor-finding (auto-link).
- **`mcp`:**
  - `remember` handler auto-links after insert (Search → filter → `Link`).
  - `recall` handler: seeds (`Search`) → `Neighbors` → **pure `blend()`** (propagated
    scoring + dedup + top-k) → map to DTOs with `retrieved_via`.
  - The `Store` consumer interface grows `Link` and `Neighbors`.
- **Config:** `N/K/LINK_THRESHOLD/BRIDGE_PENALTY` as `mcp` consts now, with a note they
  become configurable when M3 tunes weights.

## Policy gate (unchanged + additions)
- Existing M1 validation stands. Explicit `links[]`: cap count (reuse `maxEntities`),
  each id well-formed/bounded. Auto-link and expansion caps are internal constants, not
  user input.

## Testing
- **Unit (fakes):** auto-link selection (top-K, threshold filter, self-exclusion); the
  pure `blend()` (link vs bridge scoring, max-of-paths, dedup, top-k); `retrieved_via`
  tagging; expansion respects namespace scope.
- **Integration (`//go:build integration`, live):**
  - auto-link creates a weighted `[:LINKS]` edge between two similar memories.
  - recall surfaces a **linked-but-not-directly-similar** memory (1-hop) and an
    **entity-bridged** memory (shared entity, one matches the query).
  - entity bridge crosses namespaces when unscoped, and is excluded when the query is
    scoped to one namespace.

## DR-1
Resolved: keep Neo4j — recorded in `docs/adr/README.md` and the PRD. The Postgres +
pgvector + edges fallback is retired.

## Deferred carry-forward
Weighted blend (recency/importance) + rerank + token-budget → M3; retrievability +
reinforce-on-access + edge-weight propagation + supersession → M4; `memory_stats` → M6.
