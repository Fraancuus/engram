# Engram M4 Design — Type-aware decay (the thesis milestone)

> Status: brainstormed 2026-07-03. Goal (PRD §9 M4): per-type retrievability,
> reinforcement, supersession, the decay sweep, soft-forget/hard-prune, and the `forget`
> tool. This is the signature feature — the eval (M5) proves it. Builds on M3.

## Core decision: decay/no-decay is the primary axis
The fundamental question for any memory is binary — **does it fade, or does it persist?**
The three types (episodic/semantic/procedural) encode that *plus a rate*. So the decay
model branches first on **permanence**, then applies a type-keyed rate to the decaying
ones. `procedural` (and any pinned memory) is the non-decaying class; episodic/semantic
decay fast/slow. No new type fields; the three wire types are unchanged.

## Scope
**In:** `DecayModel` implementations (type-aware + uniform + null, for the eval arms);
retrievability in `recall` (soft-forget filter + the completed weighted blend);
reinforce-on-access (+ 1-hop propagation); the decay sweep goroutine (hard-prune);
supersession; the `forget` tool (soft/hard/pin/supersede). **Out (→ M6):** `memory_stats`.

**Resolved design choices:** (1) recency is **folded into retrievability** — one time-decay
term, not two; (2) decay bites via the **soft-forget filter + candidate-selection weight**,
with rerank still the final authority; (3) **no `superseded_at` schema change** — a
superseded memory decays fast from `last_accessed`.

---

## 1. Decay model — `decay.go` (domain, pure)
Implements the existing `DecayModel` port. No `Clock`, no I/O — `now` is always a parameter,
so it is deterministic, table-testable, and the eval can virtualize time. Time unit: **days**.

Two implementations behind the port encode the eval's C-vs-B contrast; a third is arm A:
- **`TypeAwareDecay`** (Engram, arm C):
  ```
  Retrievability(m, now):
    if m.Pinned:                                      return 1   # explicit pin, any type
    if m.Type == procedural && !m.Superseded:         return 1   # permanent by type
    S := Stability(m.Type, m.AccessCount, m.Importance)
    if m.Superseded: S = supersededStability                     # fast archival curve
    Δt := max(0, now - m.LastAccessed) in days
    return retrievability(S, Δt)                                  # shared pure helper exp(-Δt/S)

  Stability(type, accessCount, importance):
    return S0[type] * (1 + reinforceGain*accessCount) * (1 + importance)
    # S0: episodic 2d · semantic 60d · procedural 3650d (the branch above owns permanence)
  ```
- **`UniformDecay`** (arm B): `pinned → 1`; else `exp(-Δt / Sᵤ)` with **one** `Sᵤ` for all
  types — it *wrongly decays procedural*, which is the whole point of the contrast.
- **`NullDecay`** (arm A): always returns `1` (never forgets — the "everyone else" baseline).

Stability is **derived** from `accessCount` (the stored source of truth for reinforcement);
`Memory.Stability` holds `S₀` at insert for reference/stats. Boundary tests: `R≈1` at
`Δt=0`; `R=1/e` at `Δt=S`; procedural permanent; pinned permanent; superseded decays fast;
uniform decays procedural; reinforcement (higher `accessCount`) raises `R`.

## 2. Recall — soft-forget + weighted blend
`doRecall` gains a `DecayModel` dependency (the clock is already injected).
- **Weighted blend (completes the M3-deferred blend):** the candidate-selection score
  becomes `w_sim·sim + w_imp·importance + w_ret·R(m, now)` (recency folded into `R`).
  Weights configurable; defaults `w_sim=1.0, w_imp=0.3, w_ret=0.5`. Computed with the
  injected clock's `now`.
- **Soft-forget filter (the load-bearing eval mechanism):** a candidate is excluded when
  `m.Superseded`, `m.Forgotten`, **or** `R(m, now) < SOFT_THRESHOLD` (0.05) — unless the
  call sets `include_forgotten: true`. Episodic fades out; procedural (`R≈1`) stays.
- **Rerank stays final authority** (M3): decay gates + weights *selection*; the
  cross-encoder orders the survivors. Order of operations: expand → blend(+R) →
  **soft-forget filter** → rerank pool → rerank → take k → token-budget.
- `recallInput` gains `include_forgotten bool` (default false).

## 3. Reinforce-on-access
After assembling the returned results, `recall` reinforces them (synchronously, before
returning — correctness over latency for v1): `access_count++`, `last_accessed = now`
(the existing `Reinforce`), which raises derived stability. A **spreading-activation**
bump refreshes 1-hop `[:LINKS]` neighbors reachable by a strong edge
(`weight ≥ propagateThreshold`): their `last_accessed` is set to `now` (freshness only, no
`access_count` bump). Parameterized Cypher; clock injected. Reinforcement writes on the
read path touch only Neo4j (no shared mutable Go state), so `-race` is unaffected.

## 4. Decay sweep — goroutine
A `Sweeper` (new top-level `sweep` package, consuming a narrow `Pruner` interface plus
`DecayModel` + `Clock`) runs a `time.Ticker` loop that **exits on `ctx.Done()`** and
`recover()`s at the goroutine boundary (known lifecycle, no fire-and-forget). Each tick
calls the testable `SweepOnce(ctx, now)`:
1. Fetch prune candidates cheaply (`last_accessed < now - GRACE_PERIOD`, not pinned), in
   bounded batches.
2. Compute `R` in Go via the `DecayModel` (exp isn't worth doing in Cypher).
3. **Hard-prune** (delete) those with `R < HARD_FLOOR` and past the grace period; pinned
   (`Pinned = true`) exempt. Emit counts to the log (feeds M6 stats).

**Soft-forget is never persisted by the sweep** — it is the live `recall` filter (§2),
consistent with the domain rule "retrievability is derived per-call, never stored." The
sweep only deletes. `SweepOnce(ctx, now)` takes `now` so the eval drives it with virtual
time; the ticker goroutine passes `clock.Now()`. Wired + started in `cmd/engramd`, stopped
on shutdown.

## 5. Supersession + the `forget` tool
- **Supersession:** `remember` gains an optional `supersedes: []memory_id`. When set and
  the new memory is `procedural`, the named memories are marked `superseded = true` (→ fast
  decay). `rememberOutput` gains `superseded: []memory_id` (what was replaced). This is how
  reference memory "forgets" — by replacement, not time.
- **`forget{ id, mode }` → `{ ok }`**, `mode ∈ {soft, hard, pin, supersede}`:
  - `soft` — set `Forgotten = true` (excluded from default recall, recoverable via
    `include_forgotten`).
  - `hard` — delete the memory.
  - `pin` — set `Pinned = true` (decay-exempt; a dedicated flag, not importance, so pinning
    does not skew the blend's importance term).
  - `supersede` — set `Superseded = true`.
  Untrusted input validated at the handler boundary (id well-formed, mode in the set).

`Forgotten` and `Pinned` are new boolean properties. Neo4j is schemaless for properties, so
no migration is needed — writes set them; reads treat a missing value as `false` (optional
read in `mapToMemory`).

## Ports / store surface (new)
- `engram.DecayModel` — implemented by `TypeAwareDecay`/`UniformDecay`/`NullDecay` in
  `decay.go`, sharing a package-level pure `retrievability(S, Δt)` helper (no base struct).
  `mock` gets a programmable `FakeDecay` for handler tests.
- `neo4j.Store` gains (all parameterized Cypher): `Supersede(ids)`, `SetForgotten(id)`,
  `Pin(id)`, `Delete(id)`, `PruneCandidates(before, limit)`, `PropagateReinforce(id, weightThreshold, now)`.
- **Interfaces split by capability** (not one god-Store): the new `forget` handler owns a
  narrow `{SetForgotten, Pin, Delete, Supersede}` interface (its own field on `handlers`);
  the `sweep` package owns `Pruner {PruneCandidates, Delete}`; `mcp.Store` gains only
  `PropagateReinforce` (the same recall write-back path as `Reinforce`). Overlap (e.g.
  `Delete`) is fine — no shared `Deleter`. The one `*neo4j.Store` satisfies them all.
- `recall` reads `Forgotten`/`Superseded`/`Pinned` via the projection (add the three to the
  `Search`/`Neighbors` map projection so the soft-forget filter can see them).

## Config (env / consts, tunable at M5)
`SOFT_THRESHOLD=0.05`, `HARD_FLOOR=0.02`, `GRACE_PERIOD=30d`, `SWEEP_INTERVAL=1h`,
`reinforceGain=1.0`, `propagateThreshold=0.85`, blend weights above, `S0` per type
(episodic 2d · semantic 60d · procedural 3650d), `Sᵤ=14d` (uniform), `supersededStability=2d`.

## Testing
- **Unit (table-driven, pure):** `decay.go` boundaries (above) for all three impls;
  `blend` with the retrievability term; soft-forget filter (in/at/below threshold,
  `include_forgotten`, superseded, forgotten); `forget` mode validation + dispatch (fakes);
  supersession path.
- **Integration (`//go:build integration`, live):** reinforce-on-access bumps
  `access_count`/`last_accessed` and propagates to a strong-edge neighbor; `SweepOnce`
  hard-prunes a decayed unpinned memory but not a pinned/procedural one; `forget` modes
  end-to-end; a recall excludes a soft-forgotten memory and returns it with
  `include_forgotten`.
- No wall-clock in tests — the injected clock / explicit `now` throughout. `-race` clean.

## Deferred
`memory_stats` (counts by type×namespace, retrievability histogram, sweep stats) → M6. The
eval that consumes all of this → M5.
