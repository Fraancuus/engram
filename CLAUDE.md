# Engram — agent guidance

## What this is
A local-first, long-term memory service for AI agents. A single-binary Go service
over Neo4j (native vector index + property graph), with **type-aware forgetting**,
namespaced "universes", and a local **inference sidecar** (embeddings + reranker).
The differentiator is the **eval** that proves type-aware decay helps — protect it.

Full spec: `docs/engram-prd-v1.md`. Go conventions: `docs/engram-go-rules.md`
(imported below). Repo/DevEx plan: `docs/engram-repo-setup-prd.md`.

## Repo map
- `engram.go` — domain core anchor; will hold types (Memory, Entity) + ports
  (MemoryStore, Embedder, Reranker, DecayModel, Clock). `memory.go`/`decay.go` join it
  in M0–M1. Imports NO infrastructure.
- `neo4j/` — MemoryStore adapter (parameterized Cypher only).
- `inference/` — Embedder/Reranker adapter (sidecar HTTP client).
- `mcp/` — MCP adapter: remember · recall · forget · memory_stats (stdio).
- `mock/` — fakes implementing the domain ports, for tests + the eval.
- `internal/` — not importable by consumers.
- `cmd/engramd/` — the service; wires deps by hand in `main()`.
- `cmd/eval/` — the eval harness binary. The CI job is wired now, but the real
  precision@k regression gate lands at M5 (today it's a stub that exits 0).
- `eval/` — labelled datasets + scenarios (anti-circularity: ground-truth labels,
  never the reranker judging itself).

## Architecture invariants (non-negotiable)
- **Dependency rule:** the root `engram` package imports NO infrastructure. Ports
  live in the domain; `neo4j`/`inference`/`mcp` implement them. depguard enforces it.
- **Package-by-dependency, not by layer.** No `usecases/`/`interactors/`/`entities/`
  /`services/` folders.
- **No DI container.** Wire dependencies by hand in `main()`.
- **Accept interfaces, return structs.** Define interfaces where they are *consumed*.
- **Inject the clock.** Never call `time.Now()` inside decay/retrievability logic —
  take `now time.Time` or a `Clock`. The eval virtualizes time; a buried clock makes
  simulation impossible. This is the single most important rule.
- **Decay is pure functions** behind `DecayModel`, keyed by memory type. Uniform vs.
  type-aware must be a strategy swap, not a rewrite.
- `context.Context` is the **first parameter** on anything doing I/O or that can be
  cancelled. Never store a Context in a struct.
- Errors are wrapped with `%w` and add what you were doing. Never swallow an error;
  never log *and* return the same one.
- Every goroutine has a known lifecycle and exits on `ctx.Done()`. Code must pass
  `go test -race`.

## Go, not .NET
The most common failure mode here. No `Manager`/`Factory`/`Helper`/`AbstractBase`
/`*Service` god-types. No middleware framework. No service locator. If you are
reaching for a .NET pattern, stop and write the flatter Go version.

## Security
- Every Neo4j query is parameterized. No string-built Cypher with input.
- MCP tool inputs are untrusted: validate types and bounds at the handler boundary.
- No secrets in code or logs. Errors crossing the MCP boundary never leak internals.

## Scope (the contract)
v1 scope is fixed in `docs/engram-prd-v1.md` §2. Do **not** add v2 features early:
consolidation/summarization, per-namespace behavior, web UI, GPU/NPU, or LLM
extraction. If a change isn't on the in-scope list, it isn't in v1. Bias toward
shipping the smaller thing.

## Gates vs. agents (never duplicate)
**Deterministic gates do deterministic rules; agents do judgment.**
`gofmt` + `golangci-lint` (errcheck, depguard, staticcheck, gosec, …) own formatting,
unused vars, unhandled errors, and the import boundary — never re-flag those by hand.
The four read-only subagents in `.claude/agents/` exist for what a linter can't decide:
- **architect** — before adding a package/interface or changing dependency direction;
  is an abstraction justified, is the design sound.
- **reviewer** — after writing/modifying Go, before committing; idiomatic shape,
  error handling, the missing test case, concurrency safety.
- **scoping-ux** — when a feature is proposed or scope is drifting; in-scope vs.
  defer-to-v2, plus MCP/API DevEx.
- **security-auditor** — for DB queries, MCP inputs, the sidecar boundary, deps, or
  before a release; Cypher injection, input validation, info leakage.

## How work moves
One change per milestone (PRD §9), one PR per change, Franco merges.

1. **Orient with the graph, not grep.** `graphify-out/graph.json` is the map of this
   repo. Before the first search or file read for a codebase question run
   `graphify query "<question>"`; `graphify path "<A>" "<B>"` for how two things relate,
   `graphify explain "<concept>"` for one thing. Read raw files only for lines the graph
   already located. After changing code run `graphify update .` (AST only, free).
   `graphify-out/` is gitignored; rebuild locally with `/graphify .`.
2. **Specify before coding: superspec.** `/opsx:ff <milestone-slug>` runs brainstorm
   (superpowers:brainstorming) → proposal → design → delta specs → tasks → plan in
   `openspec/changes/<slug>/`. The proposal names the milestone and what is out of scope;
   anything from the v2 list stops here. Rules per artifact: `openspec/config.yaml`.
3. **Review the plan visually.** After `plan.md`, run `/visual-plan` on it into
   `plans/<same-slug>/` (Agent-Native Plans MDX, local bridge). Franco reviews there;
   every decision goes back into `design.md`/`plan.md` before apply.
4. **Build TDD first.** `/opsx:apply` works the plan with
   superpowers:test-driven-development and subagent-driven-development. Layer order:
   domain → `mock/` → adapters → `cmd/`. Small diffs; delete before you add.
5. **Review, verify, ship.** Run the `.claude/agents/` reviewers on what changed, then
   `/opsx:verify`, then `/opsx:continue` to push and open the PR. After merge,
   `/opsx:archive`.

Tooling installed by hand, outside Claude Code plugins: graphify
(`uv tool install graphifyy`, then `graphify install --project`) and the OpenSpec CLI
(`npm i -g @fission-ai/openspec`). The `superspec` schema lives in
`openspec/schemas/superspec/`.

## Verification
Before claiming work done: `go build ./...`, `go test -race ./...`,
`golangci-lint run ./...`. CI runs the same plus `govulncheck`, CodeQL, gitleaks,
and the eval gate.

@docs/engram-go-rules.md

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- When graphify-out/graph.json exists, run `graphify query "<question>"` before your first grep, search or source-file read for a codebase question, and put this rule in every subagent prompt that explores code. Read raw files only for lines the graph or an earlier read already located. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
- A committed graphify-out/graph.json is rewritten whole on every build, so a large diff to it in a pull request is expected. Do not flag it as churn or suggest skipping the graph rebuild or its hooks.
