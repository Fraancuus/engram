# Engram — agent guidance

## What this is
A local-first, long-term memory service for AI agents. A single-binary Go service
over Neo4j (native vector index + property graph), with **type-aware forgetting**,
namespaced "universes", and a local **inference sidecar** (embeddings + reranker).
The differentiator is the **eval** that measures what type-aware decay buys and what it
costs. Protect it: a null result is a valid outcome, so never tune the dataset or scoring
toward a preferred one.

Full spec: `docs/engram-prd-v1.md`. Go conventions: `docs/engram-go-rules.md`
(imported below). Repo/DevEx plan: `docs/engram-repo-setup-prd.md`.

## Repo map
- `engram.go` — domain core: types (Memory, Entity) + ports (MemoryStore, Embedder,
  Reranker, DecayModel, Clock). `decay.go` holds the decay math. Imports NO infrastructure.
- `neo4j/` — MemoryStore adapter (parameterized Cypher only).
- `inference/` — Embedder/Reranker adapter (sidecar HTTP client).
- `mcp/` — MCP adapter over stdio. Tools: remember, recall, forget, memory_stats.
- `sweep/` — the decay sweep goroutine; hard-prunes below the retrievability floor.
- `schema/` — `001_init.cypher`: constraints + the vector index. Applied by hand; no Go
  code applies it.
- `mock/` — fakes implementing the domain ports, for tests + the eval.
- `internal/` — not importable by consumers.
- `cmd/engramd/` — the service; wires deps by hand in `main()`.
- `cmd/eval/` — the eval harness binary. `--ci` is the live gate: it fails when arm
  `type-aware` precision@5 drops more than 0.02 below `eval/baseline.json`.
- `eval/` — generator, arms, metrics, report (anti-circularity: ground-truth labels,
  never the reranker judging itself).
- `openspec/` — per-artifact rules (`config.yaml`), the superspec schema, synced `specs/`,
  and `changes/` with its `archive/`.
- `plans/<slug>/` — visual plans for review.

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

**Git.** Never push to `main`. Cut branches with
`git switch -c feat/<slug> --no-track origin/main`, because VS Code Sync pushes to the
tracked upstream, so a branch tracking `origin/main` lands on `main`. Set upstream at first
push: `git push -u origin feat/<slug>`.

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
   `/opsx:verify`, then `/opsx:continue` to push and open the PR, then
   `/opsx:archive` on the same branch and push, so one PR carries code, archive and synced
   specs. Never a second PR for the archive.

**Model tiers** (set 2026-11-01; the session model orchestrates, decides, and writes
dispatch briefs; it does not implement or review by itself):
- Session model: `opus` for the execution loop (`/opsx:apply`, reviews, verify, ship).
  `fable` is optional for brainstorm, design, and spec rulings, where first-try judgment
  on open-ended questions matters most.
- Implementers: `sonnet`; `haiku` only for mechanical one-file edits or docs.
- `architect` and `security-auditor` agents: `fable` (pinned in their frontmatter); they
  run rarely and their verdicts gate structure and releases.
- `reviewer`, `scoping-ux`, task reviewers, and the final whole-branch reviewer: `opus`.
  When dispatching a generic reviewer, pass `model: opus` explicitly.
- Scoped re-reviews of a small fix diff: `haiku`.
- Fix-loop escalation (round 4+): one tier above the stuck implementer.
Never dispatch a subagent without an explicit model; an omitted model inherits the session's.

**Subagent rules** (subagents load this file, except built-in Explore and Plan, so briefs
need not repeat these):
- Implementers confirm the branch with `git branch --show-current`, stage files by name, and
  never push, rebase, amend, switch branch or dispatch subagents.
- Any scratch copy of the repo, and any probe that would dirty the checkout, works in its
  own `mktemp -d`, never a shared scratch path, and deletes only what it created. Reviewers
  never edit tracked files.
- `architect` and `scoping-ux` have no Bash, so they cannot run `graphify query`. Run it
  yourself and put the result in their brief.
- Literal code in a brief is a proposal, not a spec. An implementer who shows by probe that
  it cannot work returns BLOCKED once, with the evidence, and the orchestrator rules. A
  reviewer does not reopen a deviation that is disclosed, evidenced and keeps the brief's
  intent.

Tooling installed by hand, outside Claude Code plugins: graphify
(`uv tool install graphifyy`, then `graphify install --project`) and the OpenSpec CLI
(`npm i -g @fission-ai/openspec`). The `superspec` schema lives in
`openspec/schemas/superspec/`.

## Verification
Before claiming work done, run what CI runs:
```bash
go build ./...
go test -race -coverprofile=cover.out ./...
golangci-lint run ./...
```
CI also runs `govulncheck`, CodeQL, gitleaks and the eval gate.

- **Eval gate:** a change to decay, recall or the eval also runs `go run ./cmd/eval --ci`
  and reports the A/B/C table.
- **Slow package:** `./eval` takes 5 to 6.5 minutes under `-race` on CI (288 s and 384 s on
  the same M6 code), and `go test` kills a package at 10 minutes, as it did twice during M6.
  Check its wall time before adding tests, and share datasets rather than generating new
  floor-size ones.
- **Baseline:** regenerate only on amd64, with
  `go run ./cmd/eval --update-baseline -seed 42`, then run `--ci` twice. Commit it only for
  an intended change and say why in the PR. Never regenerate to pass a regression.
- **Integration tests:** CI does not run them. A change to `neo4j/`, `mcp/`, `sweep/`,
  `schema/` or `cmd/engramd/` runs them against the real stack, from Git Bash (PowerShell has no `<`):
  ```bash
  docker compose up -d --wait
  docker compose exec -T neo4j cypher-shell < schema/001_init.cypher  # once per fresh volume
  go test -race -tags integration -v ./...
  ```
  With Neo4j down these tests skip rather than fail, and `go test` still exits 0. A `SKIP`
  reading `neo4j unavailable` or `tei unavailable` means the run proved nothing. Other skips,
  such as the two `ENGRAM_EVAL_SMOKE` tests in `./eval`, have nothing to do with the stack;
  do not set that variable to clear them. With Neo4j up but no schema, tests that search
  fail: `TestStoreSearch` names the missing vector index, and the `mcp` tests report only
  `store unavailable`.
- **Local hook:** `.git/hooks/pre-commit` exits 0 when lefthook is not on PATH, so commits
  skip format, lint and gitleaks, with only the line `Can't find lefthook in PATH` to show
  for it. If you see it, run the gates by hand or follow Dev setup in `CONTRIBUTING.md`.

@docs/engram-go-rules.md

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- When graphify-out/graph.json exists, run `graphify query "<question>"` before your first grep, search or source-file read for a codebase question, and put this rule in every subagent prompt that explores code. Read raw files only for lines the graph or an earlier read already located. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
- A committed graphify-out/graph.json is rewritten whole on every build, so a large diff to it in a pull request is expected. Do not flag it as churn or suggest skipping the graph rebuild or its hooks.
