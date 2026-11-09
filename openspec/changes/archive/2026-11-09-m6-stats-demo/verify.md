# Verification Report

**Change**: `m6-stats-demo`
**Verified at**: `2026-11-09`
**Iteration**: `1`
**Verifier**: Claude Opus 5.5 orchestrating sonnet mapping agents, split by capability

---

## 1. Structural Validation (`openspec validate --all --json`)

- [x] All items `"valid": true`

**Result**:

```text
totals: 10 items, 10 passed, 0 failed (1 change, 9 specs); 0 errors
warnings: 18, all "requirement text over 500 characters", six of them on this change
```

---

## 2. Task Completion (`tasks.md`)

- [ ] All `- [ ]` have been changed to `- [x]`

**Incomplete tasks**:

| Task | Reason incomplete | Blocks archive? |
|---|---|---|
| 8.2 | Finalize, runs after this report | No |
| 8.3 | Archive, runs after finalize on this branch | No |

---

## 3. Delta Spec Sync State

| Capability | Sync status | Notes |
|---|---|---|
| memory-stats | ✗ pending sync | New, synced by archive |
| agent-acceptance | ✗ pending sync | New, synced by archive |
| memory-store-fake | ✗ pending sync | Modified, synced by archive |
| tools-entry | ✗ pending sync | Modified, synced by archive |

---

## 4. Design / Specs Coherence Spot Check

| Sample item | design description | specs counterpart | Gap |
|---|---|---|---|
| Top bucket, clamping | D3: top bucket closed, R clamped | memory-stats: Retrievability histogram | None |
| Read-only queries | D7: readers routing on every stats query | memory-stats: Stats mirror the Cypher | None |
| Tool description | D9: verbatim text, `store_size` follows namespace | memory-stats | None |
| Stack-down messages | D10: `calling "initialize": EOF`, `Connection closed`, `embedding failed` | agent-acceptance: Claude Code can start engramd | None |
| Stdio smoke test | D11: two processes, `*_TEST_*` env | agent-acceptance: Stdio smoke test | None |
| Real run | D12: `claude -p`, `--strict-mcp-config`, whitelist | agent-acceptance: Real agent transcript | None |

**Requirement mapping**: 18 of 18 requirements implemented, with no design contradiction in D1 to D15. 47 of 51 scenarios have a test or artifact that fails if they break.

**Drift warnings** (non-blocking):

- Scenario "Test environment cannot reach the dev database": the env mapping is at `cmd/engramd/stdio_integration_test.go:130-134`, and an override was proven once by probe during Task 6. No test fails if the child reaches another database.
- Scenario "Server wraps Tools": `TestNewServerWrapsTools` only checks non-nil. `memory_stats` itself is exercised through `NewServer` over the transport.
- Scenario "Same behaviour both ways": a claim about unchanged handler assertions, which the mapping could not verify.
- Scenario "No tracked client config": `git ls-files .mcp.json` is empty. No test pins it.
- Scenario "Server rejects a write on the stats path" is pinned weakly: `TestStoreStatsRoutingRejectsWrite` runs a write through the same routing option, and would still pass if `Counts` or `LiveMemories` dropped it. All four queries carry the option by reading. It is integration-only, which CI skips.
- Unscoped counts on Neo4j are asserted as a lower bound, because the database is shared. The exact numbers are asserted on `MemStore`.
- `docs/engram-prd-v1.md:223`, the M4 line, still says recall soft-forgets. It is outside D14's list and the brief's, so it was left.
- `docs/demo.md` uses namespace `demo/recording` and the transcript `demo/m6-e2e`, on purpose, so that recording the demo cannot touch the transcript's data.

**Namespace check mismatch**: `docs/engram-go-rules.md` section 6, `mcp/doc.go` and PRD section 8 say `namespace` is validated against a known set. The code checks non-blank plus length. `memory_stats` copies that existing check, adds no whitelist, and the mismatch stands for Franco, as the brief directed.

---

## 5. Implementation Signal

- [x] No unstaged files in the worktree
- [ ] All related commits have been pushed: finalize pushes next

**Commit range**: `059396e..d364c67`, plus the commit carrying this report.

**After archive**: `b8a6aa0` splits `doMemoryStats` and its transport test, because DeepSource flagged their cyclomatic complexity. It changes no behaviour or assertion. `reviewer` found it clean, and `./mcp` and `./cmd/engramd` passed under `-race` on the integration tag, with no skip.

**Gate at `d364c67`**:

- `go build ./...` ok; `gofmt -l` clean; `golangci-lint run` 0 issues on `./mcp` and `./mock`, plain and with `--build-tags integration`, and on the whole repo both ways at `1e04d00`.
- `go run ./cmd/eval --ci` passes. Its table equals the two runs at `1e04d00` except latency, and `git diff origin/main -- eval/ cmd/eval/` is empty, so `eval/baseline.json` is unchanged. No re-baseline.
- `go test -race -coverprofile=cover.out ./...` passes in all 9 packages at `1e04d00`, with `./eval` at 331.9 s. The two later commits change only `mock/memstore_test.go` and `mcp/stats_test.go`, and both packages were re-run under `-race`.
- gitleaks over `origin/main..HEAD` clean. `govulncheck` is not installed locally; CI runs it. Lefthook is not on PATH, so these gates ran by hand.

**Integration, against the real stack with the schema applied**:

- The run at `1e04d00` was killed by the harness for low memory, before it produced output. Nothing from it is counted. It was re-run, at Franco's go-ahead.
- `go test -count=1 -race -p 2 -tags integration -v` on every package but `./eval`, at `7f59d8c`: exit 0, 59 s, 320 pass, 0 fail, 0 skip, and no line naming `unavailable`.
- `./mcp` re-run on the tag at `d364c67`: ok.
- `./eval` has no integration-tagged file, so the tag adds nothing there. Its two expected skips, confirmed by name on the tag: `TestRunFloorSmoke` and `TestNoDedupCollisionsSeeds`, both waiting on `ENGRAM_EVAL_SMOKE=1`.

**Agents**: `architect` and `scoping-ux` ran before apply, and the `memory_stats` description changed afterwards, on the evidence of the real run. `reviewer` per task, `security-auditor` on the MCP input and new Cypher (pass, two low notes: no transaction timeout on the bridge query, and `--allowedTools` skips the hard-delete prompt, now documented in `docs/demo.md`). Whole-branch review found two items, both closed. Set aside with no code change: a warning on unknown types, a flagged fixture row with pairs, and the transaction timeout. `memory_stats` writes nothing, so the skeptic round for write paths does not apply.

**Real-agent transcript** (`results/e2e-transcript.md`):

- In the first run the agent read the scoped `store_size` as the whole store. The tool description was changed, the run was repeated on the final text, and the first transcript was replaced.
- The kept transcript ends process 3 with "Store size: 0 memories, 0 link edges, 0 entity bridges" under a heading that names no namespace. The figures are scoped. It is kept verbatim.
- The entity "staging database" remains after the hard delete, because `Delete` leaves entities.

**Demo script disclosure**: the commands in `docs/demo.md` were run once from Git Bash. The agent steps ran through `claude -p`, not the interactive UI, with `--model sonnet`, `--no-session-persistence`, stream-json output, and the session's `CLAUDE*` variables unset. The interactive UI and `/exit` were not driven. `docker compose down` was not run, because verify needed the stack. `claude mcp add` was checked against an isolated `CLAUDE_CONFIG_DIR`, and reported Connected. The recording stays open until Franco links it.

**A/B/C table**, agent mix, seed 42, k=5, rerank none, lambda 0.1. Scored columns equal `openspec/changes/archive/2026-11-04-m6-polish/results/agent.txt`:

```text
arm         precision@5  recall@5  ndcg@5  mrr    store  score  edges  bridges
none        0.210        0.975     0.949   0.957  5000   0.849  955    36043
uniform     0.204        0.952     0.937   0.948  3897   0.859  457    21994
type-aware  0.179        0.872     0.875   0.884  1146   0.852  112    2007
recommended: uniform (ENGRAM_DECAY=uniform) on mix agent, lambda 0.1
```

---

## Overall Decision

- [ ] ✅ PASS
- [x] ⚠️ PASS WITH WARNINGS — can proceed; the warnings are the four scenarios and one weak pin listed above, all correct by reading. Stated limits: the interactive UI was not driven, and the integration run left out the body of `./eval`.
- [ ] ❌ FAIL

**Next step**: finalize on this branch (no worktree, so the manual path), then `/opsx:archive` on the same branch, then tell Franco it is ready to merge.
