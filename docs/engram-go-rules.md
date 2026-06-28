# Engram — Go Language Rules

> **Loading:** this file lives at `docs/engram-go-rules.md` and is imported via `@docs/engram-go-rules.md` in `CLAUDE.md`, so it loads every session. It also serves as the human contributor reference linked from `CONTRIBUTING.md`.
>
> **Scope of this doc:** the *judgment* conventions a linter can't fully decide — naming intent, interface placement, error design, concurrency discipline. The *mechanical* subset (formatting, unused vars, unhandled errors, import boundary) is enforced deterministically by `gofmt` + `golangci-lint` (errcheck, depguard, staticcheck, gosec) and is **not** repeated here. Gates do determinism; this doc does judgment.
>
> Canonical upstream references: Effective Go, the Go Code Review Comments, and the Google Go Style Guide. When this doc is silent, defer to those.

---

## 1. Naming
- `MixedCaps` / `mixedCaps`, never underscores. Exported = capitalized.
- Package names: short, lowercase, no underscores, no plurals; the package name is part of the call site (`mcp.Server`, not `mcp.MCPServer`). Avoid stutter (`memory.Memory` is fine only if it reads naturally; prefer `memory.Store`).
- No `Get` prefix on getters: `m.Stability()`, not `m.GetStability()`.
- Receivers: 1–2 letters, consistent across all methods of a type (`func (s *Store)`, always `s`).
- Interface names: `-er` where it reads (`Embedder`, `Reranker`), or a noun for a role (`MemoryStore`, `DecayModel`).
- Error variables: `ErrNotFound`, `errFoo` (sentinel). Error *strings* are lowercase, no trailing punctuation: `"memory not found"`, not `"Memory not found."`.

## 2. Errors
- Wrap with context using `%w`: `fmt.Errorf("recall %q: %w", query, err)`. Always add what you were doing; never return a bare `err` from deep in a call stack without context.
- Compare with `errors.Is` / `errors.As`, never string matching.
- Define sentinel errors (`var ErrNamespaceUnknown = errors.New(...)`) for conditions callers branch on.
- **Never** `panic` in library code or request paths. `panic` is for truly unrecoverable init-time invariants only. The decay goroutine and MCP handlers must never panic the process — recover at the goroutine boundary if needed and log.
- Don't log *and* return the same error (double-reporting). Handle it once, at the layer that can decide.

## 3. Interfaces & types
- **Accept interfaces, return structs.** Functions take the narrow interface they need; constructors return concrete types.
- **Define interfaces where they're consumed, not where they're implemented.** `MemoryStore` lives in the domain (the consumer), `neo4j` implements it. Do not put a `Storer` interface inside the `neo4j` package.
- Keep interfaces small — 1–3 methods. A big interface is a design smell; split by capability.
- Avoid `any`/`interface{}` except at genuine serialization boundaries. Prefer concrete types and generics over empty-interface soup.
- Make the zero value useful where you can. A `DecayConfig{}` should mean "sane defaults," not "broken."
- Pointer vs value receiver: pointer if the method mutates or the struct is large or holds a lock; otherwise value. Be consistent per type — don't mix.

## 4. Concurrency (load-bearing for Engram)
- `context.Context` is the **first parameter** of every function that does I/O, blocks, or can be cancelled — recall, store calls, sidecar calls, the decay sweep. Never store a `Context` in a struct.
- Respect cancellation/deadlines: pass `ctx` down, select on `ctx.Done()` in loops.
- Every goroutine has a known lifecycle and a way to stop. The decay sweep runs on a `time.Ticker` inside a goroutine that exits on `ctx.Done()`. No fire-and-forget goroutines.
- Anything shared between the decay sweep and concurrent `recall` must be synchronized. **`go test -race` is mandatory** — assume the race detector will be run and design for it.
- Channels for ownership/coordination; mutexes for protecting state. Don't reach for channels where a mutex is simpler.

## 5. Structure
- **Package-by-dependency, not by layer.** No `usecases/`, `interactors/`, `entities/`, `services/` folders. Group by what depends on what.
- **No DI container.** Wire dependencies explicitly in `main()` (or `cmd/*/main.go`). If construction gets noisy, a hand-written `func newApp(...)` is the answer — not a framework.
- **No .NET-isms.** No `Manager`, `Factory`, `Helper`, `AbstractBase`, `*Service` god-types, no service locator, no middleware framework. If a name ends in `Manager`, rethink it.
- One purpose per package; keep them flat. `internal/` for anything that must not be imported by consumers.
- Functions over frameworks: the retrieval pipeline (vector → expand → blend → rerank → assemble) is functions composed in order, not a registered-stage engine.

## 6. Engram-specific invariants
- **Inject the clock.** No `time.Now()` inside decay or retrievability logic — take `now time.Time` or a `Clock` interface. The eval virtualizes time; a buried clock makes simulation impossible. This is the single most important rule in this file.
- **Decay is pure functions** behind `DecayModel`, keyed by memory type. No I/O, deterministic, table-testable. "Uniform vs. type-aware" must be a strategy swap, not a rewrite.
- **All Cypher is parameterized.** Never build a query by concatenating agent/user input. Parameters only.
- **MCP tool inputs are untrusted.** Validate types and bounds (`k` within limits, `namespace` against a known set, ids well-formed) at the handler boundary before anything touches them.
- Errors crossing the MCP boundary are legible to a calling agent — actionable messages, never internal stack/query dumps.

## 7. Testing
- **Table-driven tests** are the default. Name cases; cover the boundary, not just the happy path: decay at `R ≈ threshold`, dedup at the similarity cutoff, recall during an active sweep, empty/oversized inputs.
- Test behavior through public APIs; avoid testing unexported internals unless the logic is genuinely intricate (the decay math qualifies).
- No global mutable state in tests; no reliance on wall-clock time (use the injected clock) or ordering between tests. Tests must pass under `-race` and in parallel (`t.Parallel()` where safe).
- Fakes live in `mock/` and implement the domain ports — the same ones the real adapters implement. The eval harness reuses them.
- Don't chase a coverage number for its own sake; cover the logic that would silently corrupt memory or skew the eval.

## 8. Comments & docs
- Doc comments start with the name being documented (`// Recall returns ...`). Every exported symbol has one.
- Comment *why*, not *what*. The code says what.
- Keep `// TODO` rare and attributed; a TODO in a public repo is a visible promise.

---

## What enforces what (so this doc and the gates never conflict)
| Concern | Enforced by | Governed by this doc |
|---|---|---|
| Formatting, imports | `gofmt`, `goimports` | — |
| Unused vars/imports, ineffassign | compiler / `ineffassign` | — |
| Unhandled errors | `errcheck` | error *design* (wrapping, sentinels) |
| Import/dependency boundary | `depguard` | where interfaces live, package shape |
| Security (SAST) | `gosec`, CodeQL | Cypher parameterization, input validation intent |
| Naming, interface size, concurrency discipline, clock injection, test design | — | **this doc** + the reviewer/architect agents |
