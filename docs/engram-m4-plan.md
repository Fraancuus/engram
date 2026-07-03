# Engram M4 Implementation Plan — Type-aware decay

> **For agentic workers:** implement task-by-task, test-first; `- [ ]` steps. Spec:
> `docs/engram-m4-design.md`. Architect review folded in (sweep package, split interfaces,
> `Pinned` flag, shared `retrievability` helper). Builds on M3.

**Goal:** Per-type retrievability with reinforcement, supersession, a decay sweep, and the `forget` tool — the signature feature the M5 eval proves.

**Architecture:** A pure `DecayModel` (three impls = the eval's A/B/C arms) lives in the domain. `recall` gains a soft-forget filter + the completed weighted blend + reinforce-on-access. A new `sweep` package hard-prunes on a ticker. A new `forget` tool + supersession manage lifecycle. Interfaces are split by capability; the one `*neo4j.Store` satisfies all.

**Tech Stack:** Go 1.26 · Neo4j · TEI (embed :8080, rerank :8081) · modelcontextprotocol/go-sdk.

## Global Constraints
- **Inject the clock** — no `time.Now()` in decay/recall/sweep logic; `now` is a parameter. `DecayModel` is pure (no I/O, no `Clock`). This is the single most important rule.
- Domain (`engram`) imports no infra. `context.Context` first; errors wrapped `%w`, logged-then-sanitized at the MCP boundary; no panics on request paths (handlers + sweep `recover()`). Every goroutine exits on `ctx.Done()`.
- All Cypher parameterized. MCP inputs validated at the boundary. Interfaces small, defined at consumers.
- Gates per task: `go build ./...`, `go test ./...`, `golangci-lint run ./...`; integration tasks also `go test -tags integration ./...` (stack up).
- Config consts (tunable at M5): `S0` episodic 2d · semantic 60d · procedural 3650d; `Sᵤ=14d`; `supersededStability=2d`; `reinforceGain=1.0`; `SOFT_THRESHOLD=0.05`; `HARD_FLOOR=0.02`; `GRACE_PERIOD=30d`; `SWEEP_INTERVAL=1h`; `propagateThreshold=0.85`; blend `wSim=1.0, wImp=0.3, wRet=0.5`.
- Commit per task (lefthook pre-commit). Do not push.

## File structure
- `engram.go` — `Memory` gains `Pinned bool`, `Forgotten bool`.
- `decay.go` (+ `decay_test.go`) — `retrievability` helper, `TypeAwareDecay`/`UniformDecay`/`NullDecay`, consts.
- `mock/fakes.go` — `FakeDecay`; `FakeStore` new methods.
- `neo4j/store.go` — `Supersede`/`SetForgotten`/`Pin`/`Delete`/`PruneCandidates`/`PropagateReinforce`; projection adds `superseded`/`forgotten`/`pinned`.
- `mcp/recall.go` — `DecayModel` dep; weighted blend + soft-forget filter; reinforce-on-access; `include_forgotten`.
- `mcp/forget.go` (+ tests) — `forget` handler + `forgetStore` interface.
- `mcp/remember.go` — `supersedes` param; `superseded` output; initial stability.
- `mcp/server.go` — register `forget`; `NewServer` gains `decay`.
- `sweep/sweep.go` (+ `sweep_test.go`) — `Sweeper`, `Pruner`, `SweepOnce`, `Run`.
- `cmd/engramd/main.go` — wire `decay` + start the sweeper.

---

### Task 1: domain fields — `Pinned`, `Forgotten`

**Files:** Modify `engram.go`.

**Produces:** `Memory.Pinned bool`, `Memory.Forgotten bool` (both default false; a pinned memory never decays, a forgotten one is excluded from default recall).

- [ ] **Step 1** — add the two bool fields to the `Memory` struct with doc comments, next to `Superseded`.
- [ ] **Step 2** — `go build ./...`; `go test ./...` (existing tests unaffected — new zero-value fields).
- [ ] **Step 3 — commit** `feat(domain): Memory.Pinned + Memory.Forgotten`

---

### Task 2: `decay.go` — the DecayModel impls (the heart)

**Files:** Create `decay.go`, `decay_test.go` (package `engram`).

**Interfaces / Produces:**
```go
var ( _ DecayModel = TypeAwareDecay{}; _ DecayModel = UniformDecay{}; _ DecayModel = NullDecay{} )

// retrievability is the shared curve: probability recallable after dt days with stability s.
func retrievability(s, dt float64) float64 { if s <= 0 { return 0 }; return math.Exp(-dt / s) }
func daysSince(now, then time.Time) float64 { d := now.Sub(then).Hours() / 24; if d < 0 { d = 0 }; return d }

type TypeAwareDecay struct{}
func (TypeAwareDecay) Retrievability(m Memory, now time.Time) float64
func (TypeAwareDecay) Stability(t MemoryType, accessCount int, importance float64) float64
// UniformDecay, NullDecay: same method set.
```
Logic (spec §1):
```go
func (d TypeAwareDecay) Retrievability(m Memory, now time.Time) float64 {
	if m.Pinned { return 1 }
	if m.Type == Procedural && !m.Superseded { return 1 }
	s := d.Stability(m.Type, m.AccessCount, m.Importance)
	if m.Superseded { s = supersededStability }
	return retrievability(s, daysSince(now, m.LastAccessed))
}
func (TypeAwareDecay) Stability(t MemoryType, accessCount int, importance float64) float64 {
	return s0ByType[t] * (1 + reinforceGain*float64(accessCount)) * (1 + importance)
}
// s0ByType = map[MemoryType]float64{Episodic: 2, Semantic: 60, Procedural: 3650}

func (d UniformDecay) Retrievability(m Memory, now time.Time) float64 {
	if m.Pinned { return 1 }              // no procedural branch: uniform decays procedural
	s := d.Stability(m.Type, m.AccessCount, m.Importance)
	if m.Superseded { s = supersededStability }
	return retrievability(s, daysSince(now, m.LastAccessed))
}
func (UniformDecay) Stability(_ MemoryType, accessCount int, importance float64) float64 {
	return uniformS0 * (1 + reinforceGain*float64(accessCount)) * (1 + importance)
}
func (NullDecay) Retrievability(Memory, time.Time) float64 { return 1 }
func (NullDecay) Stability(MemoryType, int, float64) float64 { return math.Inf(1) }
```
Consts: `supersededStability=2`, `reinforceGain=1.0`, `uniformS0=14`.

- [ ] **Step 1 — failing tests** (`decay_test.go`, table-driven, `t.Parallel`): fix a `base := time.Unix(...)`.
  - `R≈1` at `Δt=0` (last_accessed==now) for a plain episodic.
  - `R≈1/e` (±ε) at `Δt == S` (episodic accessCount=0 importance=0 → S=2d; now = last+2d).
  - **type-aware vs uniform contrast:** a procedural, unaccessed, 100d old → `TypeAwareDecay.R==1` but `UniformDecay.R < 0.01` (the whole thesis).
  - pinned → `R==1` in all three; `NullDecay.R==1` always.
  - superseded procedural decays fast (`R<1`, uses `supersededStability`).
  - reinforcement: higher `accessCount` raises `R` for the same `Δt` (S grows).
  - `Stability` monotonic in `accessCount` and `importance`.
- [ ] **Step 2 — run, expect FAIL**: `go test -run Decay ./` (undefined types).
- [ ] **Step 3 — implement** `decay.go`.
- [ ] **Step 4 — run, expect PASS**; build; lint.
- [ ] **Step 5 — commit** `feat(domain): type-aware/uniform/null DecayModel (decay.go)`

---

### Task 3: `mock` — FakeDecay + FakeStore lifecycle methods

**Files:** Modify `mock/fakes.go`.

**Produces:**
```go
type FakeDecay struct{ R float64; S float64 } // R returned by Retrievability, S by Stability
func (f FakeDecay) Retrievability(engram.Memory, time.Time) float64 { return f.R }
func (f FakeDecay) Stability(engram.MemoryType, int, float64) float64 { return f.S }
```
`FakeStore` gains (record args, return configured errs): `Supersede(ctx, ids) error` (append to `Superseded [][]engram.MemoryID`), `SetForgotten(ctx, id) error` (append `Forgot []engram.MemoryID`), `Pin(ctx, id) error` (append `Pinned []engram.MemoryID`), `Delete(ctx, id) error` (append `Deleted []engram.MemoryID`), `PropagateReinforce(ctx, id, thr, now) error` (record `LastPropagate`), `PruneCandidates(ctx, before, limit) ([]engram.Memory, error)` (return `PruneCands`). Add matching err fields.

- [ ] **Step 1 — implement**; **Step 2 — build + lint**; **Step 3 — commit** `test(mock): FakeDecay + FakeStore lifecycle ops`

---

### Task 4: `neo4j` store — lifecycle ops + projection

**Files:** Modify `neo4j/store.go`; tests in `neo4j/decay_integration_test.go` (new, `//go:build integration`).

**Produces (all parameterized Cypher):**
```go
func (s *Store) Supersede(ctx, ids []engram.MemoryID) error            // MATCH (m) WHERE m.id IN $ids SET m.superseded = true
func (s *Store) SetForgotten(ctx, id engram.MemoryID) error            // SET m.forgotten = true ; ErrNotFound if absent
func (s *Store) Pin(ctx, id engram.MemoryID) error                     // SET m.pinned = true ; ErrNotFound
func (s *Store) Delete(ctx, id engram.MemoryID) error                  // MATCH (m {id}) DETACH DELETE m ; ErrNotFound via count
func (s *Store) PruneCandidates(ctx, before time.Time, limit int) ([]engram.Memory, error)
//   MATCH (m:Memory) WHERE m.last_accessed < $before AND coalesce(m.pinned,false) = false
//   RETURN m {<scalar projection, no embedding>} AS m LIMIT $limit
func (s *Store) PropagateReinforce(ctx, id engram.MemoryID, weightThreshold float64, now time.Time) error
//   MATCH (a:Memory {id:$id})-[r:LINKS]-(nb:Memory) WHERE r.weight >= $thr SET nb.last_accessed = $now
```
Also: add `.superseded, .forgotten, .pinned` to the `Search` **and** `Neighbors` map projections, and read them in `mapToMemory` as **optional** bools (missing → false) via a `boolProp(p, key)` helper (default false). The single-id mutators use the `RETURN count(m) AS c` + `recordValue[int64]` + `ErrNotFound` pattern already in `Reinforce`.

- [ ] **Step 1 — failing integration tests** (`decay_integration_test.go`): `Pin`/`SetForgotten` set the flag (verify via `Get`); `Delete` removes (subsequent `Get` → `ErrNotFound`) and returns `ErrNotFound` for a missing id; `Supersede` marks a batch; `PruneCandidates` returns only old, non-pinned memories (respecting `before` + pinned filter); `PropagateReinforce` bumps a strong-edge neighbor's `last_accessed` but not a weak-edge one. Assert `Search` results now carry `Superseded/Forgotten/Pinned`.
- [ ] **Step 2 — run, expect FAIL**: `go test -tags integration -run 'Pin|Forgot|Delete|Supersede|Prune|Propagate' ./neo4j/`.
- [ ] **Step 3 — implement** methods + projection + `boolProp`.
- [ ] **Step 4 — run, expect PASS** (stack up); build; lint.
- [ ] **Step 5 — commit** `feat(neo4j): decay lifecycle ops + soft-forget projection`

---

### Task 5: `recall` — weighted blend + soft-forget + reinforce-on-access

**Files:** Modify `mcp/recall.go`, `mcp/remember.go` (handlers struct + Store), `mcp/server.go` (NewServer), `mcp/remember_test.go`/`mcp/m1_integration_test.go` (constructors), `mcp/recall_test.go`.

**Interfaces:** `handlers` gains `decay engram.DecayModel`, `wSim/wImp/wRet float64`, `softThreshold float64`. `mcp.Store` gains `PropagateReinforce(ctx, id, thr, now) error`. `NewServer(embedder, reranker, decay, store, clock)`. `recallInput` gains `IncludeForgotten bool \`json:"include_forgotten,omitempty"\``.

New unexported step in `doRecall` (capture `now := h.clock.Now()` once, up front):
```go
cands := blend(seeds, neighbors, poolSize, bridgePenalty)
cands = h.scoreAndFilter(cands, now, in.IncludeForgotten)   // NEW
ranked := h.rerankResults(ctx, in.Query, cands)
... take k, packBudget → results ...
h.reinforce(ctx, results, now)                              // NEW (after DTO built)
```
```go
func (h *handlers) scoreAndFilter(cands []engram.RecallResult, now time.Time, includeForgotten bool) []engram.RecallResult {
	out := make([]engram.RecallResult, 0, len(cands))
	for _, c := range cands {
		r := h.decay.Retrievability(c.Memory, now)
		if !includeForgotten && (c.Superseded || c.Forgotten || r < h.softThreshold) { continue }
		c.Score = h.wSim*c.Score + h.wImp*c.Importance + h.wRet*r
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
func (h *handlers) reinforce(ctx context.Context, results []recallResultDTO, now time.Time) {
	for _, r := range results {
		id := engram.MemoryID(r.ID)
		if err := h.store.Reinforce(ctx, id, now); err != nil { h.log.Error("recall: reinforce failed", "err", err); continue }
		if err := h.store.PropagateReinforce(ctx, id, propagateThreshold, now); err != nil { h.log.Error("recall: propagate failed", "err", err) }
	}
}
```
Reinforce failures never fail recall (results already assembled). `testHandlers`/`liveHandlers` set `decay` (`mock.FakeDecay{R:1}` for units so nothing is filtered; `engram.TypeAwareDecay{}` live), weights, `softThreshold`.

- [ ] **Step 1 — failing tests** (`recall_test.go`): (a) soft-forget — a candidate with `FakeDecay{R:0.0}` is excluded, and included when `IncludeForgotten:true`; a `Forgotten`/`Superseded` candidate excluded; (b) weighted score — with `FakeDecay{R:0.5}`, `wSim/wImp/wRet`, assert `Score == wSim*sim + wImp*imp + wRet*0.5`; (c) reinforce — after recall, `FakeStore.Reinforced` contains each returned id and `LastPropagate` is set. Update `server_test.go` (NewServer arity) + constructors.
- [ ] **Step 2 — run, expect FAIL**.
- [ ] **Step 3 — implement**; `gofmt -w mcp/`.
- [ ] **Step 4 — run, expect PASS**; build; lint.
- [ ] **Step 5 — commit** `feat(mcp): decay-weighted recall + soft-forget + reinforce-on-access`

---

### Task 6: `forget` tool

**Files:** Create `mcp/forget.go`, `mcp/forget_test.go`; modify `mcp/server.go` (register + `handlers.forget`), constructors.

**Produces:**
```go
type forgetStore interface {
	SetForgotten(ctx context.Context, id engram.MemoryID) error
	Pin(ctx context.Context, id engram.MemoryID) error
	Delete(ctx context.Context, id engram.MemoryID) error
	Supersede(ctx context.Context, ids []engram.MemoryID) error
}
type forgetInput struct { ID string `json:"id"`; Mode string `json:"mode"` } // soft|hard|pin|supersede
type forgetOutput struct { OK bool `json:"ok"` }
func (h *handlers) doForget(ctx context.Context, in forgetInput) (out forgetOutput, err error)
```
`handlers` gains `forget forgetStore` (its own field; in `NewServer` it points at the same `*neo4j.Store`). `doForget`: `recover`; validate `TrimSpace(id) != ""`/len, `mode ∈ {soft,hard,pin,supersede}` (else legible error); dispatch (`soft→SetForgotten`, `hard→Delete`, `pin→Pin`, `supersede→Supersede([id])`); map `ErrNotFound` to an actionable message; log-then-sanitize other errors. Register in `NewServer` via `mcpsdk.AddTool` with a description.

- [ ] **Step 1 — failing tests** (`forget_test.go`, fakes): each mode dispatches to the right `FakeStore` recorder; invalid mode → error, no store call; blank id → error; `ErrNotFound` → sanitized error (no internals). A `server_test`-style check that `forget` is registered.
- [ ] **Step 2 — run, expect FAIL**.
- [ ] **Step 3 — implement**.
- [ ] **Step 4 — run, expect PASS**; build; lint.
- [ ] **Step 5 — commit** `feat(mcp): forget tool (soft/hard/pin/supersede)`

---

### Task 7: `remember` — supersession + initial stability

**Files:** Modify `mcp/remember.go`, `mcp/remember_test.go`.

`rememberInput` gains `Supersedes []string \`json:"supersedes,omitempty"\``; `rememberOutput` gains `Superseded []string \`json:"superseded,omitempty"\``. In `doRemember`: validate each supersedes id (non-blank, len, ≤ maxEntities count) like `links`; set `m.Stability = h.decay.Stability(mt, 0, importance)` and `m.Pinned=false`, `m.Forgotten=false` at insert. After a successful insert, **iff `mt == engram.Procedural` and `len(Supersedes) > 0`**, call `h.store.Supersede(ctx, ids)` and set `out.Superseded = in.Supersedes`. (On the dedup path, no supersession.) `mcp.Store` gains `Supersede(ctx, ids)`.

- [ ] **Step 1 — failing tests**: a procedural remember with `Supersedes:["x"]` calls `FakeStore.Supersede` and returns `Superseded:["x"]`; a **semantic** remember with `Supersedes` does **not** supersede (wrong type); blank supersedes id → validation error; inserted memory has non-zero `Stability` (via `FakeDecay{S:42}`).
- [ ] **Step 2 — run, expect FAIL**; **Step 3 — implement**; **Step 4 — PASS**; build; lint.
- [ ] **Step 5 — commit** `feat(mcp): procedural supersession + initial stability`

---

### Task 8: `sweep` package

**Files:** Create `sweep/sweep.go`, `sweep/sweep_test.go`.

**Produces:**
```go
package sweep
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	Delete(ctx context.Context, id engram.MemoryID) error
}
type Sweeper struct { /* pruner, decay, clock, interval, grace, hardFloor, batch, log */ }
func New(p Pruner, d engram.DecayModel, c engram.Clock, interval, grace time.Duration, hardFloor float64, batch int, log *slog.Logger) *Sweeper
func (s *Sweeper) SweepOnce(ctx context.Context, now time.Time) (pruned int, err error)
func (s *Sweeper) Run(ctx context.Context)
```
`SweepOnce`: `cands, err := pruner.PruneCandidates(ctx, now.Add(-grace), batch)`; for each, skip if `m.Pinned`, else `if decay.Retrievability(m, now) < hardFloor { pruner.Delete(...); pruned++ }`; wrap errors. `Run`: `time.NewTicker(interval)`, `for { select { case <-ctx.Done(): return; case <-t.C: <recover-wrapped SweepOnce(ctx, clock.Now())> } }`.

- [ ] **Step 1 — failing tests** (fakes, fixed `now`): `SweepOnce` deletes a candidate whose `FakeDecay{R:0.0}` is below `hardFloor` but not one with `FakeDecay{R:1}`; a `Pinned` candidate is never deleted; `PruneCandidates` error propagates; `Delete` error propagates. (`Run` covered indirectly — keep it thin.)
- [ ] **Step 2 — run, expect FAIL**; **Step 3 — implement**; **Step 4 — PASS** (`go test ./sweep/`); build; lint.
- [ ] **Step 5 — commit** `feat(sweep): decay sweep (SweepOnce + ticker Run)`

---

### Task 9: `engramd` wiring

**Files:** Modify `cmd/engramd/main.go`.

Construct `decay := engram.TypeAwareDecay{}`; `srv := mcp.NewServer(embedder, reranker, decay, store, systemClock{})`; build `sweeper := sweep.New(store, decay, systemClock{}, sweepInterval, gracePeriod, hardFloor, sweepBatch, slog.Default())`; start it under a `sync.WaitGroup` with the app `ctx` (`go func(){ defer wg.Done(); sweeper.Run(ctx) }()`), and `wg.Wait()` after `Serve` returns so the goroutine drains on shutdown. Log that the sweep is running.

- [ ] **Step 1 — implement**; **Step 2 — gate:** `go build ./...`, `go vet ./...`, `golangci-lint run ./...`. **Step 3 — commit** `feat(engramd): wire type-aware decay + start the sweep`

---

### Task 10: M4 live integration

**Files:** Create `mcp/m4_integration_test.go` and `sweep/sweep_integration_test.go` (`//go:build integration`).

- [ ] **Step 1 — tests** (live stack): (a) **soft-forget** — a memory made stale (insert, then recall with a virtualized `now` far in the future via a fixed clock returning old `last_accessed`… or set the handler's decay to filter) is absent from `recall` but present with `include_forgotten:true`; (b) **reinforce-on-access** — `recall` bumps the returned memory's `access_count`; (c) **forget** — `forget{pin}` then the memory survives a `SweepOnce`; `forget{hard}` removes it; (d) **sweep** — a `sweep.Sweeper.SweepOnce(ctx, farFuture)` prunes a decayed unpinned memory but not a procedural/pinned one. Use the injected clock / explicit `now`; no wall-clock.
- [ ] **Step 2 — run** (stack up): `go test -tags integration ./...` PASS; default `go test ./...` green.
- [ ] **Step 3 — commit** `test: M4 decay + forget + sweep integration`

---

### Task 11: docs

**Files:** Modify `README.md` (Status → M4; recall decays/soft-forgets, `forget` tool, sweep; env: sweep config), `docs/engram-prd-v1.md` (§9 M4 landed marker).

- [ ] **Step 1 — edit**; **Step 2 — commit** `docs: M4 type-aware decay`

## Self-review
- **Spec coverage:** decay model (2), retrievability recall + soft-forget + reinforce (5), sweep (8), supersession (7) + forget (6), `Pinned`/`Forgotten` fields (1), store ops + projection (4), wiring (9), integration (10). Covered.
- **Interfaces split by capability:** `forgetStore` (6), `sweep.Pruner` (8), `mcp.Store` +`PropagateReinforce`/`Supersede` only (5,7). No god-Store. ✓ (architect Q2).
- **Pin ≠ importance:** `Pinned` bool everywhere; pin sets the flag (6), decay checks `m.Pinned` (2). ✓ (architect Q3 bug fix).
- **Clock injected:** `now` a parameter in decay (2), `scoreAndFilter`/`reinforce` (5), `SweepOnce` (8); only `Run` (8) + engramd (9) read `clock.Now()`. ✓
- **Type consistency:** `DecayModel` used by decay (2), mock (3), recall (5), remember (7), sweep (8), engramd (9); `NewServer(embedder, reranker, decay, store, clock)` updated in server + engramd + tests (5,9).
