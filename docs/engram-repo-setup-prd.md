# Engram — Repo Setup & DevEx PRD (v1)

**Status:** Draft for build
**Owner:** Franco
**Companion to:** `engram-prd-v1.md` (product spec)
**Goal:** Stand up the public Engram repo so that (a) an agent-assisted dev loop is reliable, (b) the CI/CD + security posture is itself a portfolio showcase, and (c) you can't accidentally ship something insecure or off-scope.

---

## 0. Calibration — what to do *now* vs. what to gold-plate later

This whole document is a trap if you treat it as a pre-product to-do list. Calibrate:

- **Do now (the floor — ~half a day, hard to retrofit, real showcase value):** Go module + layout, `.golangci.yml`, `lefthook` pre-commit, the core CI workflows, secret + vuln scanning, `CLAUDE.md`, `SECURITY.md`. Public repos are judged on this on first glance and bolting it on later is annoying.
- **Define lean now (cheap — they're just markdown):** the four subagents. Write them tight, ship them, refine when one misfires. Do **not** spend two days crafting perfect agent prompts before any memory code exists. That is the building-instead-of-shipping pattern wearing a productivity costume.
- **Earned later (optional / nice-to-have):** path-scoped rules, the eval-integrity skill, OpenSSF Scorecard badge, SBOM, goreleaser. Add as the code grows and you hit a real, repeated mistake worth automating.

> The product is the point. The repo scaffolding exists to protect the product, not to become the project.

---

## 1. The config-loading model (so "frontmatter loading" is deliberate)

Claude Code assembles context from several sources with different load semantics. Know which is which:

| Source | Location | When it loads | Use it for |
|---|---|---|---|
| **Project memory** | `CLAUDE.md` (repo root) | Always, every session | Always-true invariants + anti-drift guardrails |
| **Nested memory** | `CLAUDE.md` in subdirs / `@path` imports | When working in that subtree | Path-scoped rules (e.g. Cypher rules under `neo4j/`) |
| **Subagents** | `.claude/agents/*.md` | At session start; matched by `description` | Specialized, isolated-context workers |
| **Skills** | `SKILL.md` (+ `skills:` field on an agent) | By `description` match, or preloaded into an agent | Judgment-heavy, reusable procedures |
| **Hooks** | `.claude/settings.json` (+ agent frontmatter `hooks:`) | On lifecycle events (PreToolUse, PostToolUse, Stop) | Deterministic gates wired into the agent loop |

Frontmatter mechanics that matter for this repo:
- `description` **is** the trigger. Write it keyword-rich and start with "Use this agent when…". A vague description = an agent that never gets invoked.
- `tools:` is an allowlist (comma-separated). **Omit = inherits everything.** For reviewers/auditors, *always* scope it down to `Read, Grep, Glob` so they physically cannot edit.
- `model:` — pin `opus` for high-stakes judgment (architect, security), `sonnet` for frequent cheaper work (reviewer, scoping). `inherit` follows the session.
- Subagents are **loaded at session start** — edit a file on disk, restart the session (or use `/agents`, which applies immediately).
- Skills are **not** inherited by subagents; declare them with the `skills:` field if an agent needs one.

---

## 2. The agent team

Four read-only specialists. The governing principle, and the thing to articulate in your `CONTRIBUTING.md`:

> **Deterministic gates do deterministic rules; agents do judgment. Never duplicate.**
> golangci-lint already catches formatting, unused vars, unhandled errors, and import-boundary violations — so no agent should re-flag those. Agents exist for what a linter *can't* decide: is this abstraction justified, is this in scope, is this Cypher actually injection-safe, is this test missing the case that matters.

All four are scoped read-only (`Read, Grep, Glob`, plus `Bash` only where they must run `git diff`/scanners). None can write — they advise; you and the main session implement.

### 2.1 `architect`
```yaml
---
name: architect
description: >
  Use this agent when making or reviewing architecture/structure decisions:
  adding a package or interface, changing dependency direction, designing a
  port/adapter boundary, or judging whether an abstraction is justified.
  Use proactively before introducing new packages.
tools: Read, Grep, Glob
model: opus
---
You are a senior Go architect for Engram, a long-term memory service.

Enforce, in order:
1. The dependency rule: the root `engram` domain package imports NO
   infrastructure (neo4j, inference, mcp). Ports (MemoryStore, Embedder,
   Reranker, DecayModel, Clock) are defined in the domain; adapters implement them.
2. Package-by-dependency, NOT by layer. No usecases/interactors/entities
   folders. No DI container — dependencies are wired by hand in main().
3. Idiomatic Go: accept interfaces, return structs; define interfaces where
   consumed; keep packages small and flat.
4. Engram invariants: the clock is injected (never time.Now() buried in
   decay logic); the decay model is pure functions behind DecayModel.

Resist ceremony. If a change adds a layer, a framework, or an abstraction
"for later," challenge it — name it as over-engineering and propose the
flatter version. This codebase punishes .NET habits.

Do NOT write code. Do NOT re-check the import boundary line-by-line — depguard
enforces that deterministically; you reason about whether the DESIGN is sound.
Output an ADR-style recommendation: Decision / Rationale / Alternative / Risk.
```

### 2.2 `reviewer`
```yaml
---
name: reviewer
description: >
  Use this agent after writing or modifying Go code, before committing.
  Reviews for idiomatic Go, error handling, test coverage, and design smells
  that linters miss.
tools: Read, Grep, Glob, Bash
model: sonnet
---
You are a senior Go reviewer for Engram.

When invoked: run `git diff HEAD`, focus on changed files.

Review for what golangci-lint CANNOT catch (do not re-flag fmt, unused vars,
or unhandled errors — the linter owns those):
- Errors wrapped with %w and context added; no swallowed errors.
- context.Context is the first arg and is threaded through I/O and the
  decay/recall paths.
- Idiomatic shape: accept interfaces / return structs; no leaky abstractions;
  no .NET-isms (no manager/factory/abstract-base structs, no DI container).
- Tests: table-driven; the MISSING case (decay edge at R≈threshold, dedup at
  the similarity boundary, concurrent recall during a sweep) is the point.
- Concurrency: anything the decay goroutine shares with recall is race-safe.

Output: Critical (must fix) / Warning (should fix) / Suggestion (nice), with
file:line and the minimal fix. Do not rewrite whole files.
```

### 2.3 `scoping-ux`
```yaml
---
name: scoping-ux
description: >
  Use this agent when adding or proposing a feature, designing the MCP tool
  surface, or whenever scope may be drifting beyond the v1 contract. Guards
  scope AND the developer experience of the API. Use proactively when a change
  feels like it's growing.
tools: Read, Grep, Glob
model: sonnet
---
You are the scope-and-DevEx guardian for Engram. Two jobs:

1. SCOPE GATEKEEPER. Hold every proposed change against the v1 in-scope list
   in engram-prd-v1.md §2. If it's a v2 feature (consolidation, per-namespace
   behavior, web UI, GPU, LLM extraction) sneaking in early, say so plainly
   and recommend deferral. Name the building-instead-of-shipping pattern when
   you see it. Bias hard toward "ship the smaller thing."

2. API / DEVEX UX. The "users" are agents calling the MCP tools and devs
   reading the repo. Check: are tool names/inputs/outputs intuitive? Are
   errors legible to a consuming agent (actionable, not stack-trace dumps)?
   Is the README's quickstart honestly runnable? Is the recall/remember
   contract self-explanatory?

Do NOT make architecture calls (that's `architect`). Output a verdict:
IN-SCOPE / DEFER-TO-V2 (with reason), plus concrete UX notes.
```

### 2.4 `security-auditor`
```yaml
---
name: security-auditor
description: >
  Use this agent for security-sensitive changes, before any release, and when
  touching database queries, MCP tool inputs, the inference sidecar boundary,
  auth, or dependencies. Complements the automated scanners with judgment.
tools: Read, Grep, Glob, Bash
model: opus
---
You are a senior application-security engineer reviewing Engram.

When invoked: run `git diff HEAD`; if available run `gosec ./...` and
`govulncheck ./...` and read their output.

You exist to catch what gitleaks/gosec/govulncheck CANNOT decide on their own:
- Cypher injection: every Neo4j query MUST use parameters, never string
  concatenation of user/agent input. Flag any interpolated query.
- MCP input validation: tool inputs (content, namespace, k, ids) are
  untrusted. Check bounds, types, and that namespace/id values can't be used
  to escape their scope.
- Reachability/context on govulncheck hits: is the vulnerable path actually
  called? Is the dep necessary?
- Info leakage: error messages and logs must not expose internals, secrets,
  or full queries.
- Defense-in-depth on secrets (gitleaks is the gate; you sanity-check config,
  .env handling, and that nothing sensitive is logged).
- The sidecar boundary: the inference process is local and trusted, but
  validate what crosses it.

Report CRITICAL / HIGH / MEDIUM / LOW with file:line and the MINIMAL fix.
Do not modify files. Do not duplicate the deterministic scanners' findings —
add the judgment layer on top.
```

---

## 3. Repo layout

```
engram/
├── engram.go              # domain types + ports (Memory, Entity, MemoryStore,
│                          #   Embedder, Reranker, DecayModel, Clock) — imports
│                          #   no infrastructure
├── memory.go, decay.go    # pure domain logic (decay = pure fns, clock injected)
├── neo4j/                 # MemoryStore impl (+ a CLAUDE.md: Cypher rules)
├── inference/             # Embedder/Reranker impl (sidecar client)
├── mcp/                   # MCP adapter (remember/recall/forget/stats)
├── mock/                  # fakes for tests + eval
├── internal/              # anything not meant for import
├── cmd/
│   ├── engramd/           # the service; wires deps in main()
│   └── eval/              # the eval harness binary
├── eval/                  # datasets + scenarios (+ a CLAUDE.md: anti-circularity)
├── .claude/
│   ├── agents/            # architect.md, reviewer.md, scoping-ux.md, security-auditor.md
│   └── settings.json      # hooks (optional)
├── .github/
│   ├── workflows/         # ci.yml, codeql.yml, secrets.yml, scorecard.yml(opt)
│   └── dependabot.yml
├── CLAUDE.md
├── lefthook.yml
├── .golangci.yml
├── .gitleaks.toml         # (optional tuning/allowlist)
├── SECURITY.md
├── CONTRIBUTING.md
└── README.md
```

---

## 4. `CLAUDE.md` (starter — the anti-drift guardrail)

```markdown
# Engram — agent guidance

## What this is
A local-first long-term memory service for AI agents. Go service over Neo4j
(vector + graph), type-aware forgetting, namespaced "universes", sidecar
inference. The differentiator is the EVAL — protect it.

## Architecture invariants (non-negotiable)
- Dependency rule: the root `engram` package imports NO infrastructure.
  Ports live in the domain; neo4j/inference/mcp implement them. (depguard enforces.)
- Package-by-dependency, not by layer. No usecases/interactors folders.
- No DI container. Wire dependencies by hand in main().
- Accept interfaces, return structs. Define interfaces where consumed.
- Inject the clock — never call time.Now() inside decay logic. The eval
  virtualizes time; buried clocks make that impossible.
- Decay is pure functions behind DecayModel, keyed by memory type.
- context.Context is the first parameter on anything doing I/O.
- Errors are wrapped with %w. Never swallow an error.

## Go, not .NET
This is the most common failure mode here. No manager/factory/abstract-base
types. No middleware framework. No service-locator. If you're reaching for a
.NET pattern, stop.

## Security
- Every Neo4j query is parameterized. No string-built Cypher with input.
- MCP tool inputs are untrusted: validate bounds and types.
- No secrets in code or logs.

## Scope
v1 scope is fixed in engram-prd-v1.md §2. Do not add v2 features
(consolidation, per-namespace behavior, web UI, GPU, LLM extraction) early.
```

---

## 5. Deterministic gate: `.golangci.yml`

The clever piece is **depguard turning the dependency rule into a build failure** — architecture-as-lint, not architecture-as-hope.

```yaml
# Schema differs slightly by golangci-lint major version:
# v1 uses `linters-settings:`; v2 uses `linters.settings:`. Adapt to installed version.
linters:
  enable:
    - errcheck      # FAILS build on unhandled errors — this is your "Go forces error handling"
    - govet
    - staticcheck
    - gosec         # SAST: Go security issues, in-line with the linter
    - depguard      # architecture boundary (below)
    - ineffassign
    - revive
    - gocritic
    - misspell

linters-settings:           # (v2: nest under linters.settings)
  depguard:
    rules:
      domain:
        files:
          - "engram.go"
          - "memory.go"
          - "decay.go"
        deny:
          - pkg: "github.com/<you>/engram/neo4j"
            desc: "domain must not import infrastructure (dependency rule)"
          - pkg: "github.com/<you>/engram/inference"
            desc: "domain must not import infrastructure"
          - pkg: "github.com/<you>/engram/mcp"
            desc: "domain must not import adapters"
```

---

## 6. Pre-commit: `lefthook.yml`

`lefthook` is Go-native and fast — the idiomatic choice for a Go showcase repo.

**On linting all files vs. changed-only:** lint the **whole module** (`golangci-lint run ./...`), not just staged lines. Go is package-compiled, so a change in one file can break another (rename an interface method and every implementer fails) — a changed-only lint misses exactly that. The cost is real but bounded: golangci-lint caches aggressively, so only the first run is slow; subsequent runs touch just the changed packages. If the repo ever grows enough that even the cached run drags, move the full lint to a `pre-push` stage and keep pre-commit to fmt/vet/gitleaks. Start with full-on-pre-commit; downgrade only if you feel it.

```yaml
pre-commit:
  parallel: true
  commands:
    fmt:
      glob: "*.go"
      run: gofmt -w {staged_files}
    imports:
      glob: "*.go"
      run: goimports -w {staged_files}
    vet:
      run: go vet ./...
    lint:
      run: golangci-lint run ./...    # ALL files; cached after first run. Blocks commit on any lint failure.
    secrets:
      run: gitleaks protect --staged --redact -v    # anti-leak, on staged content

# Fallback if the full lint ever feels slow: move `lint` here and trim pre-commit.
# pre-push:
#   commands:
#     lint-full:
#       run: golangci-lint run ./...

---

## 7. CI/CD (GitHub Actions)

Pin every action to a **commit SHA**, not a tag — it's the supply-chain best practice and OpenSSF Scorecard rewards it. Make all of these **required status checks** in branch protection.

**`.github/workflows/ci.yml`** — lint, race-tested coverage, build, vuln scan:
```yaml
name: ci
on: { push: { branches: [main] }, pull_request: {} }
permissions: { contents: read }
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-go@<sha>
        with: { go-version: 'stable' }
      - uses: golangci/golangci-lint-action@<sha>
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-go@<sha>
        with: { go-version: 'stable' }
      - run: go test -race -coverprofile=cover.out ./...
      - run: go tool cover -func=cover.out   # gate on a threshold here
  vuln:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-go@<sha>
        with: { go-version: 'stable' }
      - run: go install golang.org/x/vuln/cmd/govulncheck@latest
      - run: govulncheck ./...           # official Go vuln scanner; reachability-aware
  eval:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-go@<sha>
      - run: go run ./cmd/eval --ci      # FAILS if precision@k regresses (your signature gate)
```

**`.github/workflows/codeql.yml`** — GitHub's SAST, free for public repos, Go support; run on push/PR + a weekly schedule. Uses `github/codeql-action` (init → autobuild → analyze).

**`.github/workflows/secrets.yml`** — `gitleaks/gitleaks-action` scanning **full history** on PRs (pre-commit only sees staged content; CI catches anything that slipped in earlier).

**`.github/dependabot.yml`** — keep deps + Actions current:
```yaml
version: 2
updates:
  - { package-ecosystem: "gomod",          directory: "/", schedule: { interval: "weekly" } }
  - { package-ecosystem: "github-actions",  directory: "/", schedule: { interval: "weekly" } }
```

**Optional showcase:** `scorecard.yml` (OpenSSF Scorecard badge), `syft` SBOM on release, `goreleaser` for the single-binary release. All earned, none blocking v1.

---

## 8. Security & anti-leak matrix

The point to make explicit (in `SECURITY.md`): scanners cover the deterministic surface; the `security-auditor` agent covers the judgment surface. Together they leave few gaps — and saying so is itself a strong signal on a public repo.

| Concern | Deterministic tool | Where it runs | Judgment layer |
|---|---|---|---|
| Secrets / leaks | **gitleaks** | pre-commit (staged) + CI (full history) | security-auditor (config, logging) |
| Known dep vulns | **govulncheck** | CI | security-auditor (reachability) |
| Dependency freshness | **Dependabot** | GitHub | — |
| Go SAST (bugs/security) | **gosec** (via golangci-lint) + **CodeQL** | CI | security-auditor |
| Cypher injection | — (not reliably auto-detected) | — | **security-auditor** (parameterization) |
| MCP input validation | — | — | **security-auditor** + reviewer |
| Import-boundary / architecture | **depguard** | pre-commit + CI | architect (is the design sound) |
| Unhandled errors | **errcheck** | pre-commit + CI | reviewer (is the handling correct) |
| Supply-chain posture | **SHA-pinned actions** + OpenSSF Scorecard | CI / GitHub | — |
| SBOM (optional) | **syft** | CI (release) | — |

---

## 9. Bootstrap runbook (what to actually tell Claude Code)

Phased so you set the floor and **stop**, rather than gold-plating before the product exists.

**Phase 0 — the floor (do this first, ~half a day):**
> "Scaffold a Go module for Engram using the package-by-dependency layout in engram-repo-setup-prd.md §3. Add `lefthook.yml` (§6), `.golangci.yml` with errcheck + gosec + the depguard boundary (§5), and the CI workflows in §7 (ci with lint/test-race/build/govulncheck/eval, codeql, secrets/gitleaks, dependabot) with all Actions SHA-pinned. Add `CLAUDE.md` (§4), plus `SECURITY.md` and `CONTRIBUTING.md` documenting the gates-vs-agents split. Do not write any product code yet."

**Phase 1 — the agent team (cheap, do next):**
> "Create the four subagents in `.claude/agents/` exactly per the frontmatter in engram-repo-setup-prd.md §2: architect, reviewer, scoping-ux, security-auditor. Keep them read-only as specified."

**Phase 2 — earned, as code grows (NOT now):**
> Add path-scoped `CLAUDE.md` files (`neo4j/` Cypher rules, `eval/` anti-circularity rule) and an eval-integrity skill — only once you've written enough code to have a repeated mistake worth automating.

---

## 10. Explicitly NOT doing now
- Perfecting agent prompts before the product exists (iterate from real misfires instead).
- Agent Teams / multi-session orchestration (experimental; overkill for a solo repo).
- SBOM, goreleaser, Scorecard badge (nice, earned, optional).
- Any hook that duplicates a CI gate.
- A "pipeline framework." The retrieval pipeline is functions composed in order.
