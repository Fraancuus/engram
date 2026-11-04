# M6 Stats and Demo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Implementers `sonnet`, task reviewers `opus`, scoped re-reviews `haiku`, explicit model on every dispatch (repo `CLAUDE.md`, "Model tiers"). Main checkout, no worktree: Franco watches in VS Code.

**Goal:** `memory_stats` exists, a real agent has used all four tools over stdio in a kept transcript, and Franco has a script to record the demo from.

**Architecture:** No new package. One two-method interface at its consumer, three small root-package types, one pure histogram function. Sweep stats reach `mcp` through a function-valued option, so `mcp` does not import `sweep`.

**Tech Stack:** Go 1.26, `mcp`, `mock`, `neo4j`, `sweep`, `cmd/engramd`, go-sdk v1.6.1 (`mcpsdk.CommandTransport`, `NewInMemoryTransports`), Neo4j 5.26 Community, Claude Code headless.

**Spec:** `openspec/changes/m6-stats-demo/design.md` (D1 to D15, and "Decisions at plan review", confirmed by Franco on 2026-11-04) and `openspec/changes/m6-stats-demo/specs/`.

**How this changes A/B/C:** It does not. `Recall`, `Remember` and `SweepOnce` are untouched, and `MemStore` gains read-only methods. **How this changes the CI gate:** It does not. Task 9 proves it: `go run ./cmd/eval --ci` passes and `eval/baseline.json` is unchanged. No re-baseline.

## Global Constraints

- TDD: failing test first, showing a behaviour assertion failing, not a compile error. Stub a new signature, then show the failing assertion.
- Run `graphify query "<question>"` before the first search or source read; run `graphify update .` after a task that changes Go code.
- Run tests as `go test -race ./<pkg>/...`, scoped to the task's packages. Full runs happen at Task 9.
- Never call `time.Now()` in decay or retrievability logic. Parameterized Cypher only. Wrap errors with `%w`.
- Add no test to `./eval`; its wall time has little headroom under the 10-minute limit.
- Values are fixed: `HistogramBuckets` 10; `defaultStatsRowCap` 10000; tool name `memory_stats`; JSON keys as in design D4; description text verbatim from design D9; `sweepInterval` unchanged.
- Do not add a namespace whitelist, persist sweep stats, or change when the sweep runs. If one of these seems needed, stop and report.
- Confirm the branch with `git branch --show-current`, stage files by name, commit per task. End commit messages with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push, rebase, amend or switch branch.
- Stop and report if live code contradicts this plan, or a test fails twice after a reasonable fix. Literal code here is a proposal: if a probe shows it cannot work, return BLOCKED once with the evidence.

## Review Focus

1. A namespace holding Cypher syntax, such as `x' OR 1=1 //`: it matches nothing and returns empty counts. Pinned by `TestStoreCountsHostileNamespace` in Task 3.
2. A pre-migration row with no `superseded`, `pinned` or `forgotten` property: it counts as live and maps cleanly. Pinned by `TestStorePreMigrationRowCountsLive` in Task 3.
3. A write landing between `Counts` and `LiveMemories`: `live` and the bucket sum disagree by a row, with no error and `truncated` false. Pinned by `TestDoMemoryStatsCountsAndRowsDisagree` in Task 5.
4. `ENGRAM_DECAY=none`: every live row in the top bucket, `decay` reads `none`, `below_recall_threshold` 0. Pinned by `TestDoMemoryStatsNullDecay` in Task 5.
5. The client disconnecting: `engramd` exits cleanly within the transport's window, so a session leaves no process behind. Pinned by asserting `session.Close()` returns nil in `TestStdioAllTools`, Task 6.

## Shared store fixture

Tasks 2 and 3 build the same fixture and assert the same values. That is the only parity check between the stores. In Task 3, `a`, `b`, `e` and `f` get unique per-run names, because entities are global and the dev database holds residue.

| Memory | Namespace | Type | Flags |
|---|---|---|---|
| m1 | a | episodic | superseded and forgotten |
| m2 | a | episodic | none |
| m3 | a | semantic | pinned |
| m4 | b | procedural | none |

Entities: e mentioned by m2, m3, m4; f mentioned by m2, m3. Links: m2 to m3, m3 to m2, m3 to m4.

| Scope | Groups (Total, Live, Superseded, Forgotten, Pinned) | LinkEdges | EntityBridges |
|---|---|---|---|
| a | (a, episodic: 2, 1, 1, 1, 0), (a, semantic: 1, 1, 0, 0, 1) | 2 | 3 |
| b | (b, procedural: 1, 1, 0, 0, 0) | 1 | 2 |
| all (mock only) | the three groups above | 2 | 3 |
| unknown | none | 0 | 0 |

---

### Task 1: Histogram and domain types

**Files:**
- Modify: `decay.go`, `engram.go`
- Test: `decay_test.go`

**Interfaces:**
- Produces: `const HistogramBuckets = 10`; `func RetrievabilityHistogram(ms []Memory, d DecayModel, now time.Time) [HistogramBuckets]int`; `type StoreCounts struct { Groups []CountGroup; LinkEdges, EntityBridges int }`; `type CountGroup struct { Namespace Namespace; Type MemoryType; Total, Live, Superseded, Forgotten, Pinned int }`; `type SweepStats struct { LastRunAt time.Time; LastPruned, TotalPruned int }`. Doc comments per design D2.

- [ ] **Step 1: Write the failing tests** in `decay_test.go`, with a local `DecayModel` stub that reads R from `m.Importance`:
  - `TestRetrievabilityHistogramBoundaries`: R of 0, 0.0999, 0.1, 0.5, 0.9, 0.9999, 1.0 land in buckets 0, 0, 1, 5, 9, 9, 9.
  - `TestRetrievabilityHistogramClamps`: R of -0.5 and 1.5 land in 0 and 9, with no panic. Also an empty slice returns all zeros.
  - `TestRetrievabilityHistogramVirtualTime`: `TypeAwareDecay` at a fixed `now`. Episodic, importance 0, access count 0: last accessed 6 d before lands in bucket 0 (R about 0.0498), 5.9 d before lands in bucket 0 (R about 0.0523), 0 d before lands in bucket 9. Procedural a year before lands in 9. Pinned episodic a year before lands in 9.
- [ ] **Step 2: Stub the function returning a zero array and run** `go test -race -run TestRetrievabilityHistogram .`. Expected: FAIL on the bucket assertions.
- [ ] **Step 3: Implement** in `decay.go` per design D3, and add the three types to `engram.go`. Clamp the computed index, not R.
- [ ] **Step 4: Run** `go test -race -run TestRetrievabilityHistogram .`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(decay): pure retrievability histogram and stats domain types`.

### Task 2: MemStore and FakeStore

**Files:**
- Modify: `mock/memstore.go`, `mock/fakes.go`
- Test: `mock/memstore_test.go`

**Interfaces:**
- Consumes: the Task 1 types.
- Produces: `func (s *MemStore) Counts(ctx context.Context, ns engram.Namespace) (engram.StoreCounts, error)`; `func (s *MemStore) LiveMemories(ctx context.Context, ns engram.Namespace, limit int) ([]engram.Memory, error)`, the same on `FakeStore`. `FakeStore` gains `CountsRes engram.StoreCounts`, `CountsErr error`, `LiveRes []engram.Memory`, `LiveErr error`, `LastCountsNS engram.Namespace`, `LastLiveNS engram.Namespace`, `LastLiveLimit int`. `ns == ""` means all namespaces.

- [ ] **Step 1: Write the failing tests**, using the shared fixture:
  - `TestMemStoreCountsFlagsOverlap` and `TestMemStoreCountsScopeEitherEndpoint`: the fixture table for a, b and all, comparing `Groups` directly, since both stores sort by namespace then type.
  - `TestMemStoreCountsUnknownNamespace`: no groups, 0, 0, nil error.
  - `TestMemStoreLiveMemoriesOrderProjectionLimit`: live ids m3, m1, m2 plus superseded m0, limit 2 returns m1 then m2, with nil `Embedding`, empty `Content` and `Source`, and `LastAccessed` kept.
  - `TestMemStoreStatsReadOnly`: fixed T, `Snapshot` before and after is deep-equal.
  - `TestMemStoreStatsConcurrent`: `Counts`, `LiveMemories` and `Delete` in goroutines under `-race`.
- [ ] **Step 2: Stub both methods returning zero values and run** `go test -race -run 'TestMemStore(Counts|LiveMemories|Stats)' ./mock/`. Expected: FAIL on the assertions.
- [ ] **Step 3: Implement.** Move the pair loops from `EdgeCount` and `BridgeCount` into unexported lock-free helpers taking `ns`; a pair counts when `ns == ""` or either endpoint is in `ns`. The exported counters call them with `""` under the read lock. Sort `Groups` by namespace then type, and live ids ascending.
- [ ] **Step 4: Add the `FakeStore` fields and methods.**
- [ ] **Step 5: Run** `go test -race ./mock/...`. Expected: PASS, with the existing `EdgeCount` and `BridgeCount` tests unchanged.
- [ ] **Step 6: Commit** `feat(mock): stats methods on MemStore and FakeStore`.

### Task 3: Neo4j stats queries

**Files:**
- Modify: `neo4j/store.go`
- Test: create `neo4j/stats_integration_test.go`, `//go:build integration`

**Interfaces:**
- Consumes: the Task 1 types.
- Produces: `func (s *Store) Counts(ctx context.Context, ns engram.Namespace) (engram.StoreCounts, error)`; `func (s *Store) LiveMemories(ctx context.Context, ns engram.Namespace, limit int) ([]engram.Memory, error)`.

The stack is up with the schema applied. Every result below must be a real pass, not a `neo4j unavailable` skip.

- [ ] **Step 1: Write the failing tests**, using the shared fixture with unique names, cleaning up fixture rows with `Delete` in `t.Cleanup`:
  - `TestStoreCountsFlagsOverlap`, `TestStoreCountsScopeEitherEndpoint`: the fixture table for a and b.
  - `TestStoreCountsUnknownNamespace`: no groups, 0, 0.
  - `TestStoreCountsHostileNamespace`: `x' OR 1=1 //` returns no groups, 0, 0, nil error.
  - `TestStoreLiveMemoriesOrderProjectionLimit`: as in Task 2, with ids prefixed per run.
  - `TestStoreStatsReadOnly`: `Get` before and after `Counts` and `LiveMemories`; `LastAccessed`, `AccessCount` and `Stability` equal.
  - `TestStoreStatsRoutingRejectsWrite`: a `SET` through `ExecuteQueryWithReadersRouting()`, on a test-opened driver, fails with an error containing `AccessMode`.
  - `TestStorePreMigrationRowCountsLive`: on a test-opened driver `REMOVE m.superseded, m.pinned, m.forgotten` on one row; it counts in `Live` and `LiveMemories` returns it with all three false.
- [ ] **Step 2: Stub both methods and run** `go test -race -tags integration -run 'TestStore(Counts|LiveMemories|Stats|PreMigration)' -v ./neo4j/`. Expected: FAIL on the assertions, with no SKIP lines.
- [ ] **Step 3: Implement** the four queries verbatim from design D7, each with `neo4jdriver.ExecuteQueryWithReadersRouting()`. Reuse `mapToMemory` and `recordValue`. Add the one-line comment on the `content: ''` projection, and on the bridge query: `// shortcut: quadratic in a hub entity's mentions, swap for the per-entity sum if stats get slow`.
- [ ] **Step 4: Run** the same command. Expected: PASS, no SKIP.
- [ ] **Step 5: Run** `go vet -tags=integration ./neo4j/`. Expected: clean.
- [ ] **Step 6: Commit** `feat(neo4j): read-only stats queries with readers routing`.

### Task 4: Sweep stats

**Files:**
- Modify: `sweep/sweep.go`
- Test: `sweep/sweep_test.go`

**Interfaces:**
- Consumes: `engram.SweepStats`.
- Produces: `func (s *Sweeper) Stats() engram.SweepStats`. `SweepOnce` keeps its signature and body.

- [ ] **Step 1: Write the failing tests:**
  - `TestStatsBeforeFirstTick`: zero `LastRunAt`, 0, 0.
  - `TestTickAccumulatesAtVirtualTime`: a settable clock. At T a tick prunes 2; at T plus 1 h a tick prunes 0. `LastRunAt` is T plus 1 h, `LastPruned` 0, `TotalPruned` 2.
  - `TestTickPruneBoundaryRecorded`: R exactly at the floor records `LastPruned` 0 with `LastRunAt` set; R just below records 1.
  - `TestTickFailureLeavesStats`: a completed tick, then `PruneErr` set; the stats still describe the first.
  - `TestTickPanicLeavesStats`: a local `Pruner` whose `PruneCandidates` panics; `tick` recovers and the stats are unchanged.
  - `TestStatsConcurrentWithTick`: `Stats` and `tick` in goroutines under `-race`.
- [ ] **Step 2: Stub `Stats()` returning zero and run** `go test -race -run 'TestStats|TestTick' ./sweep/`. Expected: FAIL on the assertions.
- [ ] **Step 3: Implement** per design D8: `mu sync.Mutex` and a `stats engram.SweepStats` field on `Sweeper`. `tick` reads the clock once and passes that `now` to `SweepOnce`, recording only on a nil error. `Stats()` returns a copy under the lock.
- [ ] **Step 4: Run** `go test -race ./sweep/...`. Expected: PASS, with existing tests unchanged.
- [ ] **Step 5: Commit** `feat(sweep): keep completed-pass stats in memory`.

### Task 5: The `memory_stats` tool

**Files:**
- Create: `mcp/stats.go`
- Modify: `mcp/server.go`, `mcp/remember.go` (the `Tools` struct and its comment), `mcp/m1_integration_test.go` (`liveHandlers` gains the new fields), `mock/memstore_conformance_test.go`
- Test: create `mcp/stats_test.go`

**Interfaces:**
- Consumes: Tasks 1, 2 and 3. `ServerStore` gains `Counts` and `LiveMemories`, so `*neo4j.Store`, `*mock.MemStore` and `*mock.FakeStore` must already have them.
- Produces, in `mcp/stats.go`:
  - `type statsStore interface { Counts(...); LiveMemories(...) }`
  - `type MemoryStatsInput struct { Namespace string }`, tag per design D9.
  - `type TypeCounts struct { Episodic, Semantic, Procedural int }`
  - `type MemoryCounts struct { Total, Live, Superseded, Forgotten, Pinned int; ByType TypeCounts; ByNamespace map[string]TypeCounts }`
  - `type HistogramBucket struct { Min, Max float64; Count int }`
  - `type RetrievabilityStats struct { Decay string; Buckets []HistogramBucket; BelowRecallThreshold int; Truncated bool }`
  - `type StoreSize struct { Memories, LinkEdges, EntityBridges int }`
  - `type SweepReport struct { LastRunAt *time.Time; LastPruned, TotalPruned int }`
  - `type MemoryStatsOutput struct { Counts MemoryCounts; Retrievability RetrievabilityStats; StoreSize StoreSize; Sweep SweepReport }`
  - JSON keys exactly as in design D4.
- Produces, in `mcp/server.go`: `func WithSweepStats(fn func() engram.SweepStats) Option` (nil ignored); `func (t *Tools) MemoryStats(ctx context.Context, in MemoryStatsInput) (MemoryStatsOutput, error)`. `Tools` gains `stats statsStore`, `sweepStats func() engram.SweepStats`, `statsRowCap int`.

- [ ] **Step 1: Write the failing tests** in `mcp/stats_test.go`, with `mock.MemStore` unless noted:
  - `TestDoMemoryStatsValidation`: three spaces and `maxNamespaceBytes`+1 return an error naming the bounds, and `FakeStore` records no call. Exactly `maxNamespaceBytes` and omitted both succeed.
  - `TestDoMemoryStatsFlagsOverlap`: one row superseded and forgotten, one pinned and live, one plain: `total` 3, `live` 2, `superseded` 1, `forgotten` 1, `pinned` 1, and `store_size.memories` 3.
  - `TestDoMemoryStatsScoped`: `by_namespace` has only the asked key.
  - `TestDoMemoryStatsCapBoundary`: `statsRowCap` 2. Two live rows give `truncated` false and sum 2; three give `truncated` true, sum 2 and `live` 3.
  - `TestDoMemoryStatsLiveOnlyHistogram`: one live, one superseded, one forgotten: buckets sum to 1.
  - `TestDoMemoryStatsBelowRecallThreshold`: `TypeAwareDecay` at fixed T. Episodic at 6 d counts; at 5.9 d does not.
  - `TestDoMemoryStatsCountsAndRowsDisagree`: `FakeStore` with `live` 5 and 3 rows: no error, `truncated` false, sum 3.
  - `TestDoMemoryStatsNullDecay`: `NullDecay`: all in bucket 9, `decay` is `none`, `below_recall_threshold` 0.
  - `TestDoMemoryStatsReadOnly`: `FakeStore.Reinforced` stays empty and `LastPropagateID` stays empty; on `MemStore`, `Snapshot` is deep-equal before and after.
  - `TestDoMemoryStatsSweep`: no option gives nil `LastRunAt` and 0s; nil option is ignored; an option returning a set value gives a non-nil pointer equal to it. Also: `namespace` set does not change them.
  - `TestDoMemoryStatsStoreErrorSanitized`: `CountsErr` and `LiveErr` naming a Cypher statement both return exactly `memory_stats: store unavailable`.
  - `TestDoMemoryStatsPanicRecovered`: a local store whose `Counts` panics returns exactly `memory_stats: internal error`.
  - `TestDoMemoryStatsBucketBounds`: ten buckets, `Min` of bucket 3 is exactly `0.3`, `Max` of bucket 9 is `1`.
  - `TestMemoryStatsOverTransport`: `mcpsdk.NewInMemoryTransports`, `NewServer(NewTools(... MemStore ...))`. `ListTools` returns exactly the four names and `memory_stats` has `ReadOnlyHint` true. Unknown namespace and empty store both succeed with `by_namespace` `{}` in `StructuredContent`, ten buckets, and `last_run_at` null; with the option set, `last_run_at` is a timestamp string.
- [ ] **Step 2: Add stubs and run** `go test -race -run 'TestDoMemoryStats|TestMemoryStatsOverTransport' ./mcp/`. Expected: FAIL on the assertions.
- [ ] **Step 3: Implement `doMemoryStats`** per design D4 to D9, with the same `defer recover` shape as `doForget`. Fold `Groups` once. Initialise `ByNamespace` before folding; build all ten buckets with `float64(i)/10`. Call `LiveMemories(ctx, ns, t.statsRowCap+1)`, compute `now` from `t.clock`, and use `engram.RetrievabilityHistogram` and `engram.DecayName`. Guard `t.sweepStats != nil`.
- [ ] **Step 4: Register the tool** in `NewServer`, with the description verbatim from design D9 and `Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true}`. In `NewTools` set `stats: store` and `statsRowCap: defaultStatsRowCap`. Add `Tools.MemoryStats`.
- [ ] **Step 5: Update doc comments** on `ServerStore`, `NewTools` and `NewServer` in `mcp/server.go` and `Tools` in `mcp/remember.go` to name four tools. Change `mock/memstore_conformance_test.go` to assert `mcp.ServerStore`, deleting the now-redundant `forgetMethods` mirror. Add `stats: store, statsRowCap: defaultStatsRowCap` to `liveHandlers`.
- [ ] **Step 6: Run** `go test -race ./mcp/... ./mock/...` and `go vet -tags=integration ./...`. Expected: PASS, clean.
- [ ] **Step 7: Commit** `feat(mcp): memory_stats tool`.

### Task 6: `engramd` wiring and stdio smoke test

**Files:**
- Modify: `cmd/engramd/main.go`
- Test: create `cmd/engramd/stdio_integration_test.go`, `//go:build integration`

**Interfaces:**
- Consumes: `mcp.WithSweepStats`, `(*sweep.Sweeper).Stats`.

Do not import `mock` or `eval` here; depguard forbids it in this directory. The test also imports no `mcp` DTO: it decodes `StructuredContent` into `map[string]any`, so it asserts the JSON keys a real client sees rather than the Go tags, and it compiles against older code.

The wiring in Step 4 is not exercised by any test: the first tick comes after an hour and the interval is a constant this change must not touch. It is compile-checked, and the option is covered by `TestDoMemoryStatsSweep`. A reviewer reads the line.

- [ ] **Step 1: Write `TestStdioAllTools`** per design D11. Build to `filepath.Join(t.TempDir(), "engramd")`, adding `.exe` when `runtime.GOOS == "windows"`. The child env is `append(os.Environ(), ...)` with the five mapped variables and `ENGRAM_DECAY=type-aware`. Reachability checks with `eneo4j.New` and `inference.New(...).Embed` skip with `neo4j unavailable` or `tei unavailable`. Set `cmd.Stderr` to a buffer and log it on failure.
- [ ] **Step 2: Assert the sequence in design D11**, and that `session.Close()` returns nil for each process. `t.Cleanup` hard-deletes the memory through `eneo4j.Store` if it is still present.
- [ ] **Step 3: Show it fails against old code.** Task 5 already shipped the tool, so prove the test can fail on a scratch copy: `S=$(mktemp -d); git archive origin/main | tar -x -C "$S"`, copy the test file into `$S/cmd/engramd/`, run `go test -tags integration -run TestStdioAllTools -v ./cmd/engramd/` there, then delete only `$S`. Expected: FAIL on the assertion that the tool list is exactly the four names.
- [ ] **Step 4: Update `cmd/engramd/main.go`:** construct the sweeper before the tools, pass `mcp.WithSweepStats(sweeper.Stats)`, update the package comment to name four tools, and change the log line's `tools` value to `remember,recall,forget,memory_stats`.
- [ ] **Step 5: Run** `go test -race -tags integration -run TestStdioAllTools -v ./cmd/engramd/`. Expected: PASS, no SKIP.
- [ ] **Step 6: Prove the skip:** `docker compose stop neo4j`, run the test, expect `SKIP` naming `neo4j unavailable`, then `docker compose up -d --wait`. Report the skip line.
- [ ] **Step 7: Commit** `feat(engramd): wire sweep stats, add stdio smoke test over CommandTransport`.

### Task 7: Docs and demo script

**Files:**
- Create: `docs/demo.md`
- Modify: `README.md`, `docs/engram-prd-v1.md`, `CLAUDE.md`

Quote `design.md` rather than paraphrase it. State no eval number.

- [ ] **Step 1: Write `docs/demo.md`** per design D13: stack up, schema, `go install ./cmd/engramd`, the interactive command with the design D10 config, from `cd "$(mktemp -d)"`, with `--setting-sources "" --allowedTools 'mcp__engram__*'`; the three design D12 prompts, one `claude` process each, with `/exit` between; what the viewer should see at each step; the warning about unscoped `memory_stats`; why `sweep` reads empty; the `initialize: EOF` symptom and fix; the stale-binary note; cleanup.
- [ ] **Step 2: Update `README.md`:** "It exposes three tools" becomes four; add a `memory_stats` bullet with the input and the output's four sections; add the `go install` and `claude mcp add --scope local` wiring to "Run Engram", replacing the `go run` suggestion as the client command; add a short "Demo" section linking `docs/demo.md`; update the status lines at the top and under Milestones so they name the recording as the one thing open.
- [ ] **Step 3: Update the PRD** per design D14: sections 4, 6.3, 6.5, 8 and the M6 status line. Leave the "namespace whitelist" sentence in section 8 alone: Franco decided that at plan review.
- [ ] **Step 4: Update the `mcp/` line in `CLAUDE.md`:** "Tools: remember, recall, forget, memory_stats." Remove "`memory_stats` is still to come (M6)".
- [ ] **Step 5: Check for stale claims:** `git grep -nE "three tools|still to come" -- README.md CLAUDE.md docs/engram-prd-v1.md mcp cmd`. Expected: no match.
- [ ] **Step 6: Commit** `docs: memory_stats, agent wiring, demo script, PRD corrections`.

### Task 8: The real run (orchestrator)

The session model runs this one, because deciding what is safe to publish is judgment. **Files:** create `openspec/changes/m6-stats-demo/results/e2e-transcript.jsonl` and `results/e2e-transcript.md`.

- [ ] **Step 1: Install and check:** `go install ./cmd/engramd`, confirm `engramd` resolves on PATH and the stack is healthy with the schema applied. Verify `claude mcp add --scope local engram -- engramd`, then `claude mcp get engram` shows connected, against an isolated `CLAUDE_CONFIG_DIR`, so the user's config is untouched.
- [ ] **Step 2: Run the three prompts of design D12** as three `claude -p` processes from a `mktemp -d` directory, saving raw streams there. Confirm `--allowedTools 'mcp__engram__*'` matches; if it does not, list the four names and use that form in `docs/demo.md`.
- [ ] **Step 3: Check the run** by reading it. `remember` in process 1, `recall` of the same id in process 2, `forget` and `memory_stats` present, every `memory_stats` call scoped. If the agent called it unscoped, or a step is missing, discard and repeat. Nothing is hand-edited.
- [ ] **Step 4: Sanitize with the design D12 whitelist** into `results/`, from a script kept out of the repo.
- [ ] **Step 5: Negative search:** the local user name, GitHub login, email, drive and home paths, `session_id`, `uuid`, every other configured MCP server name, and any namespace other than `demo/m6-e2e`. Expected: no match. Read the whole transcript as well.
- [ ] **Step 6: Run `docs/demo.md` once from Git Bash,** with `claude -p` for the agent steps, in its own namespace, ending with its cleanup. Fix any command that fails. Record the disclosure for `verify.md`.
- [ ] **Step 7: Commit** `docs(openspec): real-agent transcript for M6 acceptance`.

### Task 9: Verify, finalize, archive

- [ ] **Step 1: Reviews:** `reviewer` (opus) on all Go; `security-auditor` (fable) on the MCP input and the new Cypher; whole-branch reviewer (opus). A reviewer flagging the whitelist mismatch is not a blocker: record it in `verify.md` and carry on.
- [ ] **Step 2: Gates:** `go build ./...`, `go test -race -coverprofile=cover.out ./...`, `golangci-lint run ./...`. Check `./eval`'s wall time against the 10-minute limit.
- [ ] **Step 3: Eval:** `go run ./cmd/eval --ci` passes and the A/B/C table goes in `verify.md`; `git diff origin/main -- eval/baseline.json` is empty.
- [ ] **Step 4: Integration:** `go test -race -tags integration -v ./...` with the stack up. No SKIP reading `neo4j unavailable` or `tei unavailable`. `TestRunFloorSmoke` and `TestNoDedupCollisionsSeeds` skip as expected; name them in `verify.md`.
- [ ] **Step 5: `/opsx:verify`** into `verify.md`, including the whitelist mismatch and the demo disclosure.
- [ ] **Step 6: Finalize by the manual path:** `git push -u origin feat/m6-stats-demo`, open the PR, write `finalize.md`, with no menu for Franco. Retrospective promote candidates are applied in this PR or listed in its body.
- [ ] **Step 7: `/opsx:archive`** on this branch and push, then tell Franco it is ready to merge.
