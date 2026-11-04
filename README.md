# Engram

> A local-first, long-term memory service for AI agents — a clean concurrent **Go**
> service over a graph + vector store, with **type-aware forgetting**, namespaced
> "universes", and an eval that measures what forgetting buys and what it costs.

**Status:** v1 in progress — **M4 type-aware decay landed** (per-type retrievability, reinforce-on-access, the decay sweep, supersession, and the `forget` tool). M5 eval landed; M6 eval correction landed (the numbers below). Next: `memory_stats` and the demo.
**Module:** `github.com/Fraancuus/engram` · **Go:** 1.26

Most "agent memory" projects are a vector store with `save()` and `search()`. They
never forget, never distinguish *kinds* of memory, and never measure whether the
memory layer makes retrieval better. Engram's thesis: **forgetting is a feature** —
but *what* and *how fast* you forget depends on the *type* of memory. An event fades;
a code standard shouldn't. That is a thesis, and v1 puts it under test: it builds the
distinction and **measures its effect with an eval.** The measurement is the
differentiator, not the storage.

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
| Eval | Go `testing` + a harness binary (the CI gate is live) |

## The memory model

Every memory carries a **type** (governs decay) and a **namespace** (a soft "universe"):

- **episodic** — fast decay with disuse, reinforced on access.
- **semantic** — slow decay; distilled, stable state.
- **procedural / reference** — near-permanent; "forgets" only on *supersession*.

Namespaces are soft scopes; **entities** are first-class nodes that bridge memories
across universes for associative recall.

## The eval (the point)

Three arms over a labelled dataset with virtualized time, differing only in decay:
**none**, **uniform** and **type-aware**. Supersession runs in all three. Metrics:
precision@k, recall@k, nDCG/MRR, latency, store size, type-stratified recall, and nDCG per
query class. Relevance is judged against held-out ground-truth labels owned by the dataset
generator, never the reranker scoring itself. The dataset is synthetic; see
[M5](openspec/changes/archive/2026-11-03-m5-eval/design.md) and
[M6](openspec/changes/archive/2026-11-04-m6-polish/design.md) for the design.

The gate is live: `go run ./cmd/eval --ci` fails when arm `type-aware` precision@5
regresses more than 0.02 against `eval/baseline.json`. The tables below reproduce with
`go run ./cmd/eval -seeds 1` and `go run ./cmd/eval -seeds 1 -mix stress`; plain
`go run ./cmd/eval` averages seeds 42 to 44. Flags: `-seed`, `-seeds`, `-k`, `-scale`,
`-mix agent|stress`, `-lambda`, `-rerank none|cosine`, `-json`, `--update-baseline`.

**Recommendation score.** nDCG minus lambda times the store size over the store size of
`none`. Lambda 0.10 is a value judgement, fixed before the first run. `-lambda 0` gives
the retrieval-only answer. The eval also prints the break-even lambda against `none`;
it ignores the 0.005 tie margin the recommendation uses.

**Query mix.** The primary `agent` mix is an assumption, not a measurement, because no
real agent traces exist. It is 35% topic-current, 20% fact, 30% episode-recent, 5%
episode-old and 10% bridge (half link, half entity). The `stress` mix is the M5 mix.

Seed 42, k=5, rerank none, lambda 0.10. Agent mix (primary):

| arm | nDCG@5 | store | score | topic-current | fact | episode-recent | episode-old | bridge-link | bridge-entity |
|---|---|---|---|---|---|---|---|---|---|
| none | 0.949 | 5000 | 0.849 | 1.000 | 0.852 | 0.998 | 0.970 | 1.000 | 0.613 |
| uniform | 0.937 | 3897 | 0.859 | 0.989 | 0.900 | 0.998 | 0.800 | 0.865 | 0.564 |
| type-aware | 0.875 | 1146 | 0.852 | 1.000 | 1.000 | 1.000 | 0.000 | 0.347 | 0.147 |

Stress mix (second):

| arm | nDCG@5 | store | score | topic-current | fact | episode-recent | episode-old | bridge-link | bridge-entity |
|---|---|---|---|---|---|---|---|---|---|
| none | 0.919 | 5000 | 0.819 | 1.000 | 0.813 | 0.989 | 0.990 | 1.000 | 0.606 |
| uniform | 0.851 | 3876 | 0.773 | 0.984 | 0.862 | 0.989 | 0.737 | 0.822 | 0.518 |
| type-aware | 0.671 | 1180 | 0.647 | 1.000 | 1.000 | 0.993 | 0.067 | 0.294 | 0.204 |

Break-even lambda against `none`, seed 42: agent mix uniform 0.055, type-aware 0.096;
stress mix uniform 0.301, type-aware 0.325. Entity bridges surfaced no result in any arm,
mix or rerank mode, so `bridge-entity` measures only the half reachable by vector, and
0.613 is its ceiling.

**What the numbers support.** Type-aware forgetting does not retrieve better than never
forgetting. On the agent mix it gives up about 0.07 nDCG for a store at 23% of the size,
and on the stress mix about 0.25. The cost falls on old episodes and bridges: type-aware's
bridge-entity falls from 0.613 to 0.147 on the agent mix. The gain is store size, with
live procedural memory protected.

It does not beat uniform on retrieval either: uniform's nDCG is higher on both mixes and
on all ten agent-mix seeds. What type-aware holds over uniform is a store under a third of
the size, and standards at 1.000 where uniform gives 0.989 on the agent mix at seed 42.

On the agent mix at lambda 0.10 with rerank none, the three arms score within about 0.02.
Type-aware is recommended on 9 of 10 seeds and uniform on seed 42; with a cosine reranker
stage, `none` is recommended on seeds 42 to 44. On the stress mix the arms spread wider
and `none` is recommended. So the agent table recommends no arm with confidence.
`ENGRAM_DECAY` stays `type-aware`, and that default is an open decision.

**Caveat.** Type-aware's 1.000 on facts, against 0.852 for `none` on the agent mix at
seed 42, is not a retrieval benefit. It comes from a discontinuity in the score blend at
the 50-seed cap: a linked memory just outside the seeds inherits its source's similarity
and outranks the target. Uniform's fact gap over `none` has the same cause. Without it
the agent-mix cost of type-aware is about 0.10 nDCG, not 0.07, and the arms no longer sit
within 0.02. This is open for a follow-up change. The real cross-encoder could not be run
on this dataset, because its content is generator ids, not text.

**Thresholds.** On the normalised index scale, dedup is 0.95 (raw cosine 0.90) and
auto-link and propagation are 0.925 (raw cosine 0.85).

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
command (`go run ./cmd/engramd`, or a built binary). It exposes three tools:

- **`remember`** — `{content, type, namespace, importance?, source?, entities?, links?, supersedes?}` → `{memory_id, deduped, superseded?}`. Embeds the content, deduplicates within the namespace (reinforcing a near-identical memory instead of inserting), writes `:Entity` nodes + `[:MENTIONS]` edges, **auto-links** to sufficiently-similar neighbors (plus any explicit `links`). A procedural or semantic write naming `supersedes` replaces those memories; such a superseding write never dedups into what it replaces, and dedups only into identical content of its own type.
- **`recall`** — `{query, namespaces?, k?, include_forgotten?}` → ranked `[{id, content, score, type, namespace, provenance}]`. Vector kNN seeds are expanded via 1-hop `[:LINKS]` and entity bridges (cross-namespace unless scoped), scored as similarity + 0.3 importance + 0.01 retrievability. Retrievability filters candidates before the pool is cut (**decayed/soft-forgotten memories excluded** unless `include_forgotten`) and adds at most 0.01 to the score, so it only breaks near-ties. Results are then **reranked by a cross-encoder** and packed under a **token budget**; returned memories are **reinforced on access**. `provenance.retrieved_via` reports how each result surfaced (`vector` / `link` / `entity:<name>`).
- **`forget`** — `{id, mode}` → `{ok}`, where `mode` is `soft` (exclude from recall, recoverable), `hard` (delete), `pin` (protect from decay), or `supersede` (mark replaced).

A background **decay sweep** periodically hard-prunes memories whose retrievability has fallen below a floor (pinned memories always; under type-aware, also procedural memories that are not superseded). The eval measures the effect of this forgetting.

Service-backed tests are tagged `integration` and excluded from the default unit run:

```bash
go test ./...                      # unit tests — no services needed
go test -tags integration ./...    # store + end-to-end — needs the stack up
```

Config (env vars, with defaults): `NEO4J_URI` (`neo4j://localhost:7687`), `NEO4J_USER`
(`neo4j`), `NEO4J_PASSWORD` (empty → the no-auth dev stack), `TEI_URL`
(`http://localhost:8080`), `TEI_RERANK_URL` (`http://localhost:8081`), `ENGRAM_DECAY`
(`type-aware`, `uniform` or `none`; default `type-aware`). The local stack runs Neo4j with
`NEO4J_AUTH=none` bound to loopback — auth is out of v1 scope.

> A compose-backed CI job to run the `integration` tests is still a TODO.

## Milestones

`M0` skeleton → `M1` core remember/recall → `M2` graph + entity bridges →
`M3` rerank → `M4` type-aware decay → `M5` eval (the credibility milestone) →
`M6` polish (eval correction and write-up done; `memory_stats` and the demo remain). See [docs/engram-prd-v1.md](docs/engram-prd-v1.md) §9.

## License

TBD.
