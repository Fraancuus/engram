# Contributing to Engram

Thanks for looking. This is an open-source portfolio + passion project; the bar is a
*finished, defensible* v1, not a 60%-done repo. The scope contract lives in
[docs/engram-prd-v1.md](docs/engram-prd-v1.md) §2 — if a change isn't on the in-scope
list, it isn't in v1.

## The governing principle: gates vs. agents

> **Deterministic gates do deterministic rules; agents do judgment. Never duplicate.**

`gofmt` + `golangci-lint` (errcheck, depguard, staticcheck, gosec, …) already catch
formatting, unused vars, unhandled errors, and import-boundary violations — so no
reviewer (human or agent) re-flags those. The four read-only subagents in
`.claude/agents/` exist for what a linter *can't* decide:

| Agent | Use it when | Decides |
|---|---|---|
| **architect** | adding a package/interface, changing dependency direction | is the abstraction justified, is the design sound |
| **reviewer** | after writing/modifying Go, before committing | idiomatic shape, error handling, the missing test case, races |
| **scoping-ux** | proposing a feature; scope feels like it's growing | in-scope vs. defer-to-v2; MCP/API DevEx |
| **security-auditor** | DB queries, MCP inputs, sidecar boundary, deps, releases | Cypher injection, input validation, info leakage |

The agents are scoped read-only — they advise; you implement.

## Dev setup

Requires **Go 1.26+**. Install the gate tools and wire the hook:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install github.com/evilmartians/lefthook@latest
go install github.com/zricethezav/gitleaks/v8@latest # module path is still the legacy org
lefthook install
```

Make sure `$(go env GOPATH)/bin` is on your `PATH` so the git hook resolves the tools.
The pre-commit hook runs: **format** (`golangci-lint fmt`, auto-fixes + re-stages) →
**lint** (`golangci-lint run ./...`) → **secrets** (`gitleaks`, staged content).

## Layout & invariants

Package-by-dependency, not by layer. The root `engram` package is the domain core
(types + ports) and imports **no** infrastructure; `neo4j/`, `inference/`, and `mcp/`
implement the ports. depguard enforces this as a build failure.

Non-negotiables (full list in [CLAUDE.md](CLAUDE.md) and
[docs/engram-go-rules.md](docs/engram-go-rules.md)):

- Accept interfaces, return structs; define interfaces where consumed.
- **Inject the clock** — never `time.Now()` in decay logic (the eval virtualizes time).
- Decay is pure functions behind `DecayModel`, keyed by memory type.
- `context.Context` is the first parameter on anything doing I/O.
- Wrap errors with `%w`; never swallow one. No `panic` in request/decay paths.
- No DI container, no `Manager`/`Factory`/`*Service` god-types. This is Go, not .NET.

## Tests

Table-driven by default; cover the boundary (decay at `R ≈ threshold`, dedup at the
similarity cutoff, recall during an active sweep), not just the happy path. Everything
must pass under the race detector:

```bash
go test -race ./...
```

Fakes live in `mock/` and implement the same domain ports as the real adapters; the
eval harness reuses them.

## Before you push

```bash
go build ./...
go test -race ./...
golangci-lint run ./...
```

CI runs the same, plus `govulncheck`, CodeQL, gitleaks (full history), and the eval
regression gate (`go run ./cmd/eval --ci`). All are required status checks.

## Commits & PRs

Keep commits focused. Reference the milestone (`M0`…`M6`) where it helps. PRs should
state which in-scope item they advance and note any DR-1 (Neo4j vs. Postgres)
implications if they touch the store.
