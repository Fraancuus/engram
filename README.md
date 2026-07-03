# Engram

> A local-first, long-term memory service for AI agents — a clean concurrent **Go**
> service over a graph + vector store, with **type-aware forgetting**, namespaced
> "universes", and a rigorous eval proving the forgetting actually helps.

**Status:** v1 in progress — **M3 rerank landed** (cross-encoder rerank of the expanded candidates + token-budget assembly). Next: M4 type-aware decay.
**Module:** `github.com/Fraancuus/engram` · **Go:** 1.26

Most "agent memory" projects are a vector store with `save()` and `search()`. They
never forget, never distinguish *kinds* of memory, and never measure whether the
memory layer makes retrieval better. Engram's thesis: **forgetting is a feature** —
but *what* and *how fast* you forget depends on the *type* of memory. An event fades;
a code standard shouldn't. v1 builds that distinction and **proves its effect with an
eval.** The proof is the differentiator, not the storage.

## Architecture

```
  MCP client  →  Engram — Go service (single binary)
  (any agent)    MCP: remember · recall · forget · stats
                       │                         │ HTTP
       ┌───────────────┼───────────────┐         ▼
       ▼               ▼               ▼    Inference sidecar
  Write path       Read path     Decay scheduler   (TEI / llama.cpp:
 (embed·dedup·  (vector→graph→   (goroutine +        embeddings +
  link·entity)   rerank→assemble) time.Ticker)       reranker, CPU)
                       ▼
             MemoryStore interface  →  Neo4j (vector index + graph)
```

Everything talks to storage through a **`MemoryStore`** port; inference sits behind
**`Embedder` / `Reranker`** ports. The domain core (`engram` package) imports no
infrastructure — `neo4j/`, `inference/`, and `mcp/` implement the ports. The
dependency rule is enforced as a build failure by depguard.

## Stack

| Layer | Choice |
|---|---|
| Language | Go (goroutines + `context` + `time.Ticker`) |
| Store | Neo4j (native vector index + graph) |
| Inference | Local sidecar (HF Text Embeddings Inference or llama.cpp), CPU |
| MCP | stdio transport (v1) |
| Eval | Go `testing` + a harness binary (CI job wired; the gate lands at M5) |

## The memory model

Every memory carries a **type** (governs decay) and a **namespace** (a soft "universe"):

- **episodic** — fast decay with disuse, reinforced on access.
- **semantic** — slow decay; distilled, stable state.
- **procedural / reference** — near-permanent; "forgets" only on *supersession*.

Namespaces are soft scopes; **entities** are first-class nodes that bridge memories
across universes for associative recall.

## The eval (the point)

Three conditions over a labelled dataset with virtualized time: **A** baseline (no
decay), **B** uniform decay, **C** type-aware decay + supersession. Metrics:
precision@k, recall@k, nDCG/MRR, latency, store size over time, and **type-stratified
recall**. Relevance is judged against held-out ground-truth labels, never the reranker
scoring itself. Once M5 lands, a precision@k regression fails CI — the job is wired
today against a scaffold stub that exits 0.

## Developing

```bash
# Toolchain (one-time): Go 1.26+, plus the gate tools
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install github.com/evilmartians/lefthook@latest
go install github.com/zricethezav/gitleaks/v8@latest   # module path is still the legacy org
lefthook install     # wires the pre-commit gate (format · lint · secrets)

go build ./...
go test -race ./...
golangci-lint run ./...
```

Ensure your Go bin dir (`go env GOPATH`/bin) is on `PATH` so the git hook resolves
the tools. See [CONTRIBUTING.md](CONTRIBUTING.md) for the gates-vs-agents workflow and
[docs/engram-go-rules.md](docs/engram-go-rules.md) for Go conventions.

## Run Engram

Bring up the stack, apply the schema, and serve the MCP tools over stdio:

```bash
docker compose up -d --wait                                        # Neo4j + TEI embed + TEI rerank (first run pulls images + models)
docker compose exec -T neo4j cypher-shell < schema/001_init.cypher # apply schema (idempotent)
go run ./cmd/engramd                                               # serves the MCP tools over stdio
```

`engramd` speaks the Model Context Protocol over stdio, so point any MCP client at the
command (`go run ./cmd/engramd`, or a built binary). It exposes two tools:

- **`remember`** — `{content, type, namespace, importance?, source?, entities?, links?}` → `{memory_id, deduped}`. Embeds the content, deduplicates within the namespace (reinforcing a near-identical memory instead of inserting), writes `:Entity` nodes + `[:MENTIONS]` edges, and **auto-links** to sufficiently-similar neighbors (plus any explicit `links`) via weighted `[:LINKS]` edges.
- **`recall`** — `{query, namespaces?, k?}` → ranked `[{id, content, score, type, namespace, provenance}]`. Vector kNN seeds are expanded via 1-hop `[:LINKS]` and entity bridges (cross-namespace unless scoped); the expanded candidates are **reranked by a cross-encoder** (`score` is the cross-encoder score when reranking applies, or the similarity/blend score when it is skipped — a lone candidate — or the reranker is unavailable) and the result is packed under a **token budget**. `provenance.retrieved_via` reports how each result surfaced (`vector` / `link` / `entity:<name>`).

Service-backed tests are tagged `integration` and excluded from the default unit run:

```bash
go test ./...                      # unit tests — no services needed
go test -tags integration ./...    # store + end-to-end — needs the stack up
```

Config (env vars, with defaults): `NEO4J_URI` (`neo4j://localhost:7687`), `NEO4J_USER`
(`neo4j`), `NEO4J_PASSWORD` (empty → the no-auth dev stack), `TEI_URL`
(`http://localhost:8080`), `TEI_RERANK_URL` (`http://localhost:8081`). The local stack
runs Neo4j with `NEO4J_AUTH=none` bound to loopback — auth is out of v1 scope.

> A compose-backed CI job to run the `integration` tests is still a TODO.

## Milestones

`M0` skeleton → `M1` core remember/recall → `M2` graph + entity bridges →
`M3` rerank → `M4` type-aware decay → `M5` eval (the credibility milestone) →
`M6` polish. See [docs/engram-prd-v1.md](docs/engram-prd-v1.md) §9.

## License

TBD.
