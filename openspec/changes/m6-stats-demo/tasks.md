All Go tasks run under `go test -race`, scoped to the package named. A/B/C and the CI gate are unaffected throughout; task 7.3 proves it.

## 1. Domain (root package)

- [ ] 1.1 `decay.go`: `HistogramBuckets` and pure `RetrievabilityHistogram(ms, d, now)`, top bucket closed, out-of-range R clamped. Tests in `decay_test.go`: `TestRetrievabilityHistogramBoundaries` (R of 0, 0.0999, 0.1, 0.5, 0.9, 0.9999, 1.0), `TestRetrievabilityHistogramClamps` (-0.5, 1.5), `TestRetrievabilityHistogramVirtualTime` (fixed T: episodic at 6 d and 5.9 d, procedural at a year, pinned).
- [ ] 1.2 `engram.go`: `StoreCounts`, `CountGroup`, `SweepStats`, with doc comments stating that flags overlap and that a zero `LastRunAt` means no completed pass. Types only; exercised by 2.1.

## 2. Mock

- [ ] 2.1 `mock/memstore.go`: `Counts` and `LiveMemories`. Move the pair loops out of `EdgeCount` and `BridgeCount` into unexported helpers taking a namespace filter; the exported counters call them unfiltered. Tests in `mock/memstore_test.go`: `TestMemStoreCountsFlagsOverlap`, `TestMemStoreCountsScopeEitherEndpoint`, `TestMemStoreCountsUnknownNamespace`, `TestMemStoreLiveMemoriesOrderProjectionLimit`, `TestMemStoreStatsReadOnly` (fixed T, `Snapshot` unchanged), `TestMemStoreStatsConcurrent`. Existing `EdgeCount` and `BridgeCount` tests stay green unchanged.
- [ ] 2.2 `mock/fakes.go`: `FakeStore` gains programmable `CountsRes`, `CountsErr`, `LiveRes`, `LiveErr` and records the namespace and limit. Exercised by 5.1.

## 3. Adapters: Neo4j

- [ ] 3.1 `neo4j/store.go`: `Counts` and `LiveMemories`, parameterized, each query with `ExecuteQueryWithReadersRouting()`, the bridge query per memory with its `shortcut:` comment, the `content: ''` projection commented. Tests in a new `neo4j/stats_integration_test.go` under the `integration` tag, using the same fixture and expected values as 2.1: `TestStoreCountsFlagsOverlap`, `TestStoreCountsScopeEitherEndpoint`, `TestStoreCountsUnknownNamespace`, `TestStoreLiveMemoriesOrderProjectionLimit`, `TestStoreStatsReadOnly` (`Get` before and after), `TestStoreStatsRoutingRejectsWrite`.

## 4. Adapters: sweep

- [ ] 4.1 `sweep/sweep.go`: mutex, `SweepStats`, `Stats()`, `tick` records completed passes. `SweepOnce` unchanged. Tests in `sweep/sweep_test.go`: `TestStatsBeforeFirstTick`, `TestTickAccumulatesAtVirtualTime`, `TestTickPruneBoundaryRecorded` (R at the floor and just below), `TestTickFailureLeavesStats`, `TestTickPanicLeavesStats`, `TestStatsConcurrentWithTick`.

## 5. Adapters: MCP

- [ ] 5.1 `mcp/stats.go`: `statsStore`, DTOs, `doMemoryStats`, `defaultStatsRowCap`. `mcp/server.go`: `ServerStore` gains the two methods, `Tools.MemoryStats`, `WithSweepStats`, the fourth `AddTool` with the design's description and `ReadOnlyHint`. `liveHandlers` in `mcp/m1_integration_test.go` gains the new fields. Tests in `mcp/stats_test.go`: `TestDoMemoryStatsValidation` (blank, oversized, exactly at the limit, omitted), `TestDoMemoryStatsFlagsOverlap`, `TestDoMemoryStatsScoped`, `TestDoMemoryStatsCountsExactAboveCap`, `TestDoMemoryStatsCapBoundary` (at the cap, one above), `TestDoMemoryStatsLiveOnlyHistogram`, `TestDoMemoryStatsBelowRecallThreshold` (fixed T, 6 d and 5.9 d), `TestDoMemoryStatsReadOnly`, `TestDoMemoryStatsSweep` (no option, option, nil option), `TestDoMemoryStatsStoreErrorSanitized`, `TestDoMemoryStatsPanicRecovered`, `TestMemoryStatsOverTransport` (in-memory transport: unknown namespace, empty store, `last_run_at` null then set, `ListTools` exactly four, `ReadOnlyHint` set). Also `mock/memstore_conformance_test.go` asserts `mcp.ServerStore`.
- [ ] 5.2 Doc comments: `ServerStore`, `NewTools`, `NewServer` in `mcp/server.go`; `Tools` in `mcp/remember.go`. No test; `go vet` and lint.

## 6. Cmd

- [ ] 6.1 `cmd/engramd/main.go`: build the sweeper before the tools, pass `mcp.WithSweepStats(sweeper.Stats)`, update the package comment and the tools log line. Test: `go build ./...` and task 6.2.
- [ ] 6.2 `cmd/engramd/stdio_integration_test.go`: `TestStdioAllTools` under the `integration` tag, per design D11. Run with `go test -race -tags integration -v ./cmd/engramd/`, and also with Neo4j stopped once, to prove it skips with `neo4j unavailable` rather than failing.

## 7. Acceptance and docs

- [ ] 7.1 Real run per design D12: `go install ./cmd/engramd`, three `claude -p` processes against the real stack, whitelist sanitize, negative search, keep `results/e2e-transcript.jsonl` and `results/e2e-transcript.md`. Proof: the transcript itself shows `remember`, then `recall` of the same id in a later process, `forget` and `memory_stats`.
- [ ] 7.2 Docs, quoting `design.md`: `docs/demo.md` (D13); README ("three tools", the fourth bullet, status lines at the top and under Milestones, a Demo section, the `claude mcp add` wiring); PRD sections 4, 6.3, 6.5, 8 and the M6 status line (D14); the `mcp/` line in `CLAUDE.md`. Run the commands in `docs/demo.md` once from Git Bash, with `claude -p` for the agent steps. Verify `claude mcp add` against an isolated `CLAUDE_CONFIG_DIR` with `engramd` installed, so the user's config is untouched.
- [ ] 7.3 Eval non-movement: `go run ./cmd/eval --ci` passes and `git diff origin/main -- eval/baseline.json` is empty. Recorded in `verify.md` with the A/B/C table.

## 8. Ship

- [ ] 8.1 Reviewers: `reviewer` on all Go; `security-auditor` on the MCP input and the new Cypher; whole-branch review. Then `/opsx:verify`, with the integration run, and the mismatch and expected skips recorded in `verify.md`.
- [ ] 8.2 Finalize by the manual path: push with `git push -u origin feat/m6-stats-demo` and open the PR.
- [ ] 8.3 `/opsx:archive` on this branch and push, then tell Franco it is ready to merge.
