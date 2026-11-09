## Why

PRD sections 8 and 9 make a real agent using Engram over MCP the v1 acceptance condition, and there is no evidence it has ever happened: the integration tests call handlers in process and nothing speaks stdio. `memory_stats`, the fourth tool in the contract, does not exist, so nobody can see what forgetting has done to a store. This change finishes milestone M6 apart from the recording, which Franco makes. No v2 feature is involved.

## What Changes

Delivers the remaining parts of milestone M6 (PRD section 9). The demo recording stays open until Franco links it.

**`memory_stats` MCP tool (new)**
- Input `namespace?`. Output `counts`, `retrievability`, `store_size` and `sweep`, as structured JSON.
- Strictly read-only: it never reinforces a memory or touches `last_accessed`, and the server rejects a write on its queries.
- An unknown namespace returns empty counts, not an error.

**Sweep bookkeeping**
- From: `tick` logs the prune count and nothing keeps it.
- To: `tick` records `last_run_at`, `last_pruned` and `total_pruned` in memory. `SweepOnce` is unchanged.
- Reason: the tool has nothing else to report about the sweep.
- Impact: non-breaking. The values reset on restart.

**Agent acceptance (new)**
- A documented way for Claude Code to start `engramd` over stdio, with no tracked `.mcp.json`.
- A stdio smoke test behind the `integration` tag that builds `engramd` and drives all four tools through `mcpsdk.CommandTransport`, across two processes.
- One real run of Claude Code choosing the tool calls itself, with a sanitized transcript in this change directory.
- `docs/demo.md`, and a README section that links it.

**Docs**
- From: "three tools" in the README, `mcp/server.go` and `cmd/engramd/main.go`; PRD 6.3 and 6.5 describe sweep tiers and sweep stats the code does not have.
- To: four tools; PRD wording matches what is built; the PRD M6 status line says the recording remains open until Franco links it.
- Reason: this change makes them stale, and the PRD was already wrong about the sweep.
- Impact: documentation only.

**Eval**
- This change does not move the eval. A/B/C and the CI gate are unaffected. `go run ./cmd/eval --ci` passes and `eval/baseline.json` is unchanged, and verify proves it. No re-baseline.

### Out of scope

- The demo recording itself (Franco's).
- Persisting sweep stats, or changing when the sweep runs.
- A namespace whitelist. The boundary keeps the existing non-blank plus length check; the mismatch with `docs/engram-go-rules.md` section 6, `mcp/doc.go` and PRD section 8 is reported to Franco and recorded in `verify.md`.
- "Recent access patterns" from PRD 6.5, deferred.
- The `blend` discontinuity, the `ENGRAM_DECAY` default, a CI integration job, the `supersedes` typo and cross-namespace gaps.
- Everything on the v2 list: consolidation, per-namespace behavior, web UI, GPU/NPU, LLM extraction.

## Capabilities

### New Capabilities
- `memory-stats`: the `memory_stats` tool's input, output, counted sets, histogram, read-only guarantee and sweep stats.
- `agent-acceptance`: the stdio smoke test, the kept real-agent transcript, the Claude Code wiring and the demo script.

### Modified Capabilities
- `tools-entry`: `NewServer` registers four tools, `Tools` gains `MemoryStats`, and the store argument covers forget and stats.
- `memory-store-fake`: `MemStore` satisfies the stats store method set and mirrors the Neo4j stats semantics.

## Impact

- **Domain:** `engram.go` (`StoreCounts`, `CountGroup`, `SweepStats`), `decay.go` (`RetrievabilityHistogram`).
- **Adapters:** `neo4j/store.go` and `mock/` gain `Counts` and `LiveMemories`. `sweep/sweep.go` gains `Stats`. `mcp/` gains `stats.go`, the fourth tool, and the `WithSweepStats` option.
- **Binary:** `cmd/engramd/main.go` wiring order, package comment and log line; a new `integration`-tagged stdio test.
- **Docs:** README, PRD sections 4, 6.3, 6.5, 8 and 9, `CLAUDE.md`, new `docs/demo.md`.
- **MCP surface:** one new tool. The three existing tools are unchanged.
- **Dependencies:** none added.
- **Eval and CI gate:** unaffected, as above.
