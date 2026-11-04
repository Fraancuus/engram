## Context

Milestone M6 has two things left: `memory_stats` and the agent acceptance. The eval correction and write-up landed in `m6-polish`.

Observed at `059396e`:

- The sweep only hard-prunes. `SweepOnce` returns a count, `tick` logs it, nothing keeps it. `Run` waits one full `sweepInterval` (1 h) before its first tick.
- `Forgotten` is set only by the `forget` tool. `recall` also drops rows below the soft threshold at read time and stores nothing. Supersede happens at write time and through `forget`. So PRD 6.3's sweep tiers and PRD 6.5's "soft-forgotten/pruned/superseded since last run" describe nothing in the code.
- `recall` excludes `Superseded || Forgotten || R < softThreshold` (`mcp/recall.go:259`).
- `MemoryStore` has `Put` and `Get` by design; further capabilities are narrow interfaces at their consumers.
- The eval calls `SweepOnce` directly (`eval/arm.go:217`), never `tick` or `Run`.

Probes run while writing this design, in throwaway directories and a scratch Neo4j, none of them reading eval output:

| Probe | Result |
|---|---|
| Headless `claude -p`, `--strict-mcp-config`, `--mcp-config`, `--setting-sources ""`, `--tools ""`, `--allowedTools mcp__engram__*`, from a temp directory, against today's `engramd` and the real stack | Works. The agent chose `remember` in one process, then `recall` and a hard `forget` in a second. Run from inside a Claude Code session, with the session's `CLAUDE*` variables unset |
| go-sdk v1.6.1, nil map in output | Fails: `validating tool output: ... has type "null", want "object"` |
| go-sdk v1.6.1, nil slice in output | Passes silently as `null`; the schema type is `["null","array"]` |
| go-sdk v1.6.1, nil `*time.Time` | Passes as `null`; the schema type is `["null","string"]` |
| go-sdk v1.6.1, `namespace` argument | Omitted and `""` both reach the handler as `""`. JSON `null` is rejected by the type gate |
| `docker compose exec -T neo4j cypher-shell < schema/001_init.cypher` from Git Bash | Exit 0. Indexes online |
| `ExecuteQueryWithReadersRouting` with a `SET`, on Neo4j 5.26 Community | Rejected: `Writing in read access mode not allowed` |
| `SHOW PROCEDURES` for size | Only `db.stats.*` (query collection). No byte size, as expected |
| Dev database | 212 memories in 62 namespaces, test residue from `uniqueNS` integration runs |

## Goals / Non-Goals

**Goals:**
- `memory_stats`, exact in its counts, bounded in its fetch, read-only at the server.
- A real agent, in a kept transcript, using all four tools over stdio across processes.
- A stdio smoke test that fails when the wire contract breaks.
- A demo script Franco can follow to record, and docs that match the code.

**Non-Goals:**
- Recording the demo. Moving any eval number. Persisting sweep stats. Changing when the sweep runs. A namespace whitelist. Anything on the v2 list.

## Decisions

### D1. Two read methods at the consumer

`mcp/stats.go` defines the capability, following `forgetStore`, and `ServerStore` embeds its methods:

```go
// statsStore is the read-only capability the memory_stats handler consumes.
type statsStore interface {
	Counts(ctx context.Context, ns engram.Namespace) (engram.StoreCounts, error)
	LiveMemories(ctx context.Context, ns engram.Namespace, limit int) ([]engram.Memory, error)
}
```

`ns == ""` means every namespace. Ports consumed: `DecayModel` and `Clock`, unchanged. `neo4j.Store`, `mock.MemStore` and `mock.FakeStore` implement the two methods.

**Justification for the new interface:** the handler needs a capability no existing interface has, and the repo's rule is a narrow interface at the consumer rather than growing `MemoryStore`. Two methods, not one, because they have different costs and only one is capped. No new package.

Alternative rejected: deriving counts from capped rows. Above the cap every count would silently be wrong.

### D2. Domain types live in the root package

```go
// StoreCounts is the aggregate state of a store or one namespace of it.
type StoreCounts struct {
	Groups        []CountGroup // one per (namespace, type) present, sorted by namespace then type
	LinkEdges     int
	EntityBridges int
}

// CountGroup holds the counts of one (namespace, type) pair. Superseded, Forgotten and
// Pinned are flags and overlap; Live is neither superseded nor forgotten.
type CountGroup struct {
	Namespace                                Namespace
	Type                                     MemoryType
	Total, Live, Superseded, Forgotten, Pinned int
}

// SweepStats reports the decay sweep since process start. A zero LastRunAt means no pass
// has completed.
type SweepStats struct {
	LastRunAt   time.Time
	LastPruned  int
	TotalPruned int
}
```

They live in the root package because `neo4j`, `mock` and `sweep` all produce them and adapters must not import each other. The zero `SweepStats` is the honest "never ran" value.

`Live` is not derived as `Total - Superseded - Forgotten`, because a row can carry both flags. `Groups` is sorted in both stores (`ORDER BY` in Cypher, a sort in the mock), so per-store tests compare plain values and output is deterministic. The handler's fold does not depend on it.

### D3. Histogram is a pure root-package function

In `decay.go`, next to the curves:

```go
// HistogramBuckets is the number of fixed-width retrievability buckets over [0,1].
const HistogramBuckets = 10

// RetrievabilityHistogram counts ms by retrievability at now. Buckets are half-open except
// the last, which is closed so R = 1.0 lands in it.
func RetrievabilityHistogram(ms []Memory, d DecayModel, now time.Time) [HistogramBuckets]int
```

`now` is a parameter, per the clock rule. An out-of-range R clamps into the first or last bucket, so a bad `DecayModel` cannot index out of bounds. An array rather than a slice: the probe showed a nil slice passes the SDK silently as `null`, and an array cannot be nil.

### D4. What is counted

Output, all keys always present:

```json
{
  "counts": {
    "total": 12, "live": 9, "superseded": 2, "forgotten": 1, "pinned": 3,
    "by_type": {"episodic": 5, "semantic": 4, "procedural": 3},
    "by_namespace": {"demo/m6-e2e": {"episodic": 5, "semantic": 4, "procedural": 3}}
  },
  "retrievability": {
    "decay": "type-aware",
    "buckets": [{"min": 0, "max": 0.1, "count": 0}, "... ten in all ..."],
    "below_recall_threshold": 1,
    "truncated": false
  },
  "store_size": {"memories": 12, "link_edges": 4, "entity_bridges": 1},
  "sweep": {"last_run_at": null, "last_pruned": 0, "total_pruned": 0}
}
```

- **Counted set:** all rows. `counts.total` and `store_size.memories` come from one fold, so they cannot drift. `by_type` and the type counts across namespaces each sum to it. `total` stays in `counts` because `by_type` counts all rows: after a soft forget the block would otherwise read `live: 0, episodic: 1` with no anchor (scoping-ux).
- **`live`:** not superseded and not forgotten. The flags overlap, so `live + superseded + forgotten` can exceed `memories`.
- **`by_type` and `by_namespace[ns]`** are `TypeCounts` structs, not maps: the schema names the three keys, and no inner nil map can exist. The one map is `by_namespace`, initialised before folding.
- **Bucket bounds** are computed as `float64(i)/10`, so the JSON never shows `0.30000000000000004`.
- **`retrievability.decay`** is `engram.DecayName(t.decay)`. Under `ENGRAM_DECAY=none` every row lands in the top bucket, and a caller has no other way to see that setting. *Added on scoping-ux's advice, beyond the brief; Franco kept it at plan review.*
- **`retrievability.below_recall_threshold`** counts the fetched live rows with `R < softThreshold`, the same test recall applies at `mcp/recall.go:259`. With sweep stats usually empty, this is the one exact measure of what decay hides from recall, and it is what replaces the PRD's "soft-forgotten". Sampled when `truncated`. *Same status.*
- **Left out:** `as_of`, because the call is synchronous and the caller can stamp the response.

### D5. The histogram covers live rows

Those are the rows `recall` can return by default, so the histogram answers "what would recall see, and how close is it to fading". Bucket 0 spans `[0, 0.1)` and the soft threshold of 0.05 sits inside it, which is why `below_recall_threshold` is a separate number. Superseded and forgotten rows are already out of recall; their flags are counted.

Invariant, untruncated and with no concurrent write: the buckets sum to `counts.live`.

### D6. Row cap

`defaultStatsRowCap = 10_000`, a `Tools` field like `maxTokens` so a test can lower it. `LiveMemories` returns rows ordered by id ascending; the handler asks for `cap + 1`, keeps `cap`, and sets `truncated` when it got more. Comparing against `counts.live` instead would flag falsely if a delete landed between the two queries.

Ordering by id makes a truncated histogram an unbiased sample in production: ids are 128 random bits, so the first N by id are uniform over age and type. Ordering by `last_accessed` would bias it toward fresh rows. Rows carry no content or embedding, so the fetch is bounded by the cap times about 13 small properties, not by stored text (up to 100 KB per memory).

### D7. Neo4j queries

All four are parameterized and run with `ExecuteQueryWithReadersRouting()`, so the server rejects a write (probed). That makes "read-only" a property of the query path as well as the Cypher text.

```cypher
// groups
MATCH (m:Memory) WHERE $ns = '' OR m.namespace = $ns
RETURN m.namespace AS namespace, m.type AS type, count(m) AS total,
  sum(CASE WHEN coalesce(m.superseded,false) OR coalesce(m.forgotten,false) THEN 0 ELSE 1 END) AS live,
  sum(CASE WHEN coalesce(m.superseded,false) THEN 1 ELSE 0 END) AS superseded,
  sum(CASE WHEN coalesce(m.forgotten,false) THEN 1 ELSE 0 END) AS forgotten,
  sum(CASE WHEN coalesce(m.pinned,false) THEN 1 ELSE 0 END) AS pinned
ORDER BY namespace, type

// link edges: distinct undirected pairs, because a->b and b->a can both exist
MATCH (a:Memory)-[:LINKS]-(b:Memory)
WHERE a.id < b.id AND ($ns = '' OR a.namespace = $ns OR b.namespace = $ns)
RETURN count(DISTINCT [a.id, b.id]) AS n

// entity bridges: distinct unordered pairs sharing at least one entity
MATCH (a:Memory)
CALL (a) {
  MATCH (a)-[:MENTIONS]->(:Entity)<-[:MENTIONS]-(b:Memory)
  WHERE a.id < b.id AND ($ns = '' OR a.namespace = $ns OR b.namespace = $ns)
  RETURN count(DISTINCT b) AS c
}
RETURN sum(c) AS n

// live rows
MATCH (m:Memory)
WHERE ($ns = '' OR m.namespace = $ns)
  AND coalesce(m.superseded,false) = false AND coalesce(m.forgotten,false) = false
RETURN m {.id, .namespace, .type, .importance, .stability, .superseded, .pinned, .forgotten,
          .access_count, .created_at, .last_accessed, content: '', source: ''} AS m
ORDER BY m.id LIMIT $limit
```

Projecting `content: ''` and `source: ''` keeps `mapToMemory` and its required-property checks with no new mapper. A one-line comment on the query says this is deliberate, so nobody makes those properties optional and lets a really missing `content` pass on other paths.

`mock.MemStore.Counts` shares its pair loops with `EdgeCount` and `BridgeCount`: the loops move into unexported helpers taking the namespace filter, and the exported counters call them with no filter, so the numbers the eval report reads do not change.

**Scope rule for edges and bridges:** an edge or bridge counts when either endpoint is in the namespace. That gives 0 for an unknown namespace. Per-namespace values do not sum to the global value, because a cross-namespace edge counts on both sides. The tool description says "touching".

**Bridge cost, measured on a scratch `neo4j:5.26-community` with default memory settings, 5,000 memories, net of 3 s `cypher-shell` startup:**

| Query shape | One hub of 2,000 mentions (2.0M pairs) | One hub of 5,000 mentions (12.5M pairs) |
|---|---|---|
| Plain `count(DISTINCT [a.id, b.id])` | about 10 s | about 131 s |
| Per-memory subquery (chosen) | not measured | about 20 s |
| Per-entity `sum(n*(n-1)/2)` | not measured | under 0.1 s, but overcounts pairs sharing two entities |

The chosen query is exact, matches the eval's `bridges` column, and holds only one memory's distinct set at a time. It is still quadratic in the mentions of a hub entity, about 1.6 microseconds per pair. A single-user store reaches a 5,000-mention hub rarely, and the cost is 20 s on an operator call. Shortcut comment on the query: `shortcut: quadratic in a hub entity's mentions, swap for the per-entity sum if stats get slow`. No handler timeout: no other handler has one, and stats takes no unbounded input.

### D8. Sweep stats

`Sweeper` gains a `sync.Mutex`, an `engram.SweepStats` and `Stats()`. `tick` becomes:

```go
now := s.clock.Now()
n, err := s.SweepOnce(ctx, now)
if err != nil { /* log, return: stats untouched */ }
s.record(now, n)
```

`SweepOnce` is unchanged, so the eval is unaffected. A failed pass leaves the stats alone and logs, so `last_run_at` means the last completed pass. A pass where one delete failed still completes, as it does today.

`mcp.WithSweepStats(func() engram.SweepStats) Option` follows `WithIDGenerator`: nil ignored. `main` constructs the sweeper first and passes `sweeper.Stats`. A function value means no new interface and no `mcp` to `sweep` import. Without the option the handler reports the zero value, which the eval and tests rely on.

`last_run_at` is `*time.Time` in the DTO, because a zero `time.Time` serialises as `0001-01-01T00:00:00Z`.

**Limitation, stated in the tool description:** these are store-wide, not per namespace, and reset on restart. A stdio client starts `engramd` per session and the first tick comes after an hour, so they read `null` and 0 in most sessions and in the demo.

### D9. Input validation

`Namespace string` with `omitempty`. `""` and omitted both mean all namespaces. When non-empty, the existing check applies, copied inline as in `recall` and `remember`: non-blank, at most `maxNamespaceBytes`. An unknown namespace is valid and returns empty counts. No whitelist.

Errors follow the other handlers: a recover boundary, internals logged, `memory_stats: store unavailable` to the caller. The field tag reads `restrict to this universe; empty means all`, matching `recall`.

**Tool description** (scoping-ux's rewrite; the draft implied sweep stats follow `namespace`, which was the one blocking finding):

> Report how many memories exist and how fresh they are, store-wide or for one namespace. Returns no content and, unlike recall, never reinforces. Counts cover all rows; live, superseded, forgotten and pinned overlap. The retrievability histogram and below_recall_threshold cover live rows, sampled when truncated is true. Scoped edge and bridge counts include those touching the namespace. Sweep stats are store-wide since process start, and read null and 0 until the first hourly sweep. An unknown namespace returns empty counts, not an error.

The tool also sets `Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true}`.

### D10. Claude Code wiring: no tracked `.mcp.json`

A tracked file affects every session in this repo, including ones with the stack down, where a Go SDK client shows only `calling "initialize": EOF` and Claude Code shows the server failed with `Connection closed`. With `go run` it also recompiles on each start. Instead:

```bash
go install ./cmd/engramd                            # puts engramd on PATH via $(go env GOPATH)/bin
claude mcp add --scope local engram -- engramd      # per person, tracks nothing
```

The transcript and the demo use `--strict-mcp-config --mcp-config '{"mcpServers":{"engram":{"command":"engramd"}}}'`. It contains no local path, needs no file, leaves no state, and keeps every other MCP server out of the session. Known limit: `engramd` on PATH goes stale until `go install` is rerun. The docs say so, along with those symptoms and their fix.

### D11. Stdio smoke test

`cmd/engramd/stdio_integration_test.go`, `//go:build integration`, `TestStdioAllTools`. It imports no `mock` or `eval`, which depguard forbids in this directory.

- Builds with `go build -o <tmp>/engramd[.exe]`, appending `.exe` on Windows: `exec.Command` cannot start the extensionless file.
- Sets `NEO4J_URI`, `NEO4J_USER`, `NEO4J_PASSWORD`, `TEI_URL` and `TEI_RERANK_URL` from their `*_TEST_*` equivalents with the existing defaults, plus `ENGRAM_DECAY=type-aware`, so an override or a stray shell variable cannot send it to another database or change its decay. Beyond the three variables in the brief, user and password are included because the other integration tests honour them.
- Skips with `neo4j unavailable` or `tei unavailable` after a direct reachability check, because otherwise a down stack would surface as `calling "initialize": EOF`.
- Keeps `engramd`'s stderr for the failure message.

The sequence, in a `uniqueNS` namespace:

1. Process A: `ListTools` returns exactly the four names. `remember`. Close, which exits the process.
2. Process B: `recall` finds the id. `memory_stats{namespace}` reports `live` 1, the type count 1, buckets summing to 1, `truncated` false, `last_run_at` null. `forget{soft}`. `recall` no longer returns it. `memory_stats{namespace}` reports `forgotten` 1, `live` 0. `memory_stats{"unknown namespace"}` succeeds with `by_namespace` `{}`, the nil-map case. `memory_stats{}` succeeds. `forget{hard}`, then `memory_stats{namespace}` reports `memories` 0.

A `t.Cleanup` hard-deletes through a store connection if the test fails midway.

An in-memory transport test in `mcp/` (`mcpsdk.NewInMemoryTransports`, `MemStore`) also covers the wire shape, because CI does not run integration tests and would otherwise never see a nil-map regression.

### D12. The real run

Run by the orchestrator, because it needs judgment about what is safe to publish. Against the real stack, from a temp directory so no `CLAUDE.md` or repo path enters the session, with `--model sonnet --strict-mcp-config --setting-sources "" --tools "" --allowedTools mcp__engram__* --no-session-persistence --output-format stream-json --verbose`. Not `--bare`.

Three processes in namespace `demo/m6-e2e`, with plain-language prompts that name no tool:

1. "Please remember for later, in the memory namespace demo/m6-e2e, that our staging database is rotated every Tuesday at 03:00 UTC."
2. "Looking only in the memory namespace demo/m6-e2e, when is the staging database rotated? Then tell me how many memories that namespace holds and how fresh they are."
3. "In the memory namespace demo/m6-e2e, forget the memory about the staging database rotation, but keep it recoverable. Show me the namespace's stats afterwards. Then delete it permanently and show the stats once more."

**Sanitizing is a whitelist, not a denylist.** A scratch script (not committed) keeps only: `assistant` text and `tool_use` name and input; `user` `tool_result` content; `result` subtype and `is_error`. The init event, session ids, uuids, model usage, thinking and `rate_limit_event` never make it through. Then the output is checked to contain none of: the local user name, the GitHub login, the email, a drive or home path, `session_id`, `uuid`, or the name of any other configured MCP server. It is also checked to contain only namespace `demo/m6-e2e`; an unscoped `memory_stats` would list the 62 residue namespaces, and that run is discarded and repeated.

Kept in `openspec/changes/m6-stats-demo/results/`: `e2e-transcript.jsonl` (the whitelisted events, one process per file section) and `e2e-transcript.md`, generated from it for reading.

### D13. Demo script

`docs/demo.md`: prerequisites, stack up, schema, `go install`, then the interactive `claude` command with the D10 config and the D12 prompts, what the viewer should see at each step, and cleanup. It warns that unscoped `memory_stats` lists every namespace in the database, so the recording stays scoped. It states that `sweep` reads empty and why. The commands are run once from Git Bash, using `claude -p` for the agent steps because the interactive UI cannot be driven from here. That limit is disclosed in `verify.md`.

### D14. PRD corrections

- **4 (diagram):** "soft-forget/prune" becomes "hard-prune".
- **6.3:** the sweep hard-prunes when `last_accessed` is older than the grace period and R is below the floor; pinned rows are exempt. Soft-forgetting is not a sweep tier: recall drops rows below the threshold at read time and stores nothing, `include_forgotten` returns them, and the `Forgotten` flag is set only by `forget`. Supersede happens at write time and through `forget`.
- **6.5:** rewritten to D4, D5 and D8. "Recent access patterns" is deferred.
- **8:** the `memory_stats` row gains store size.
- **9:** the M6 status line says `memory_stats` and the agent acceptance landed, and that the demo recording remains open until Franco links it.

### D15. What this does to the eval

Nothing, by construction. No pipeline code changes: `Recall` and `Remember` are untouched, `SweepOnce` is untouched, `MemStore` gains read-only methods, and the existing counters the report reads stay as they are. No eval constant, mix, weight or dataset changes and none is preregistered. **A/B/C and the CI gate are unaffected.** Verify runs `go run ./cmd/eval --ci` and shows `git diff origin/main -- eval/baseline.json` empty. No test is added to `./eval`, whose wall time has less headroom than thought (288 s and 384 s on the same code).

**Write path:** this change touches none, so no write-path branch table applies.

## Risks / Trade-offs

- [`MemStore` and Neo4j stats drift apart, with no shared behavioural test] → Both stores get the same fixture and the same expected numbers in per-store tests.
- [Reader routing fails on a client routing table] → Probed on the same image. The integration test exercises it on every run.
- [Bridge query is quadratic in a hub entity] → Measured; shortcut comment; fallback named in D7.
- [A transcript leaks local detail into a public repo] → Whitelist, then a negative check, then Franco sees the file at review.
- [The agent makes unexpected calls during the real run, such as an unscoped `memory_stats`] → The prompts scope it; the check catches it; the run is repeated. Nothing is hand-edited.
- [`go install` leaves a stale `engramd`] → Documented; the demo script runs it first.
- [Sweep stats read empty in practice] → Stated in the tool description and the docs, and raised with Franco at plan review.

## Decisions at plan review

Confirmed by Franco at the plan review on 2026-11-04, before apply. Every recommendation stood, and he added no further constraint.

| Question | Decision |
|---|---|
| Histogram | Pure root-package function over capped live rows, with a `truncated` flag, reusing `engram.Memory` |
| Buckets | 10 fixed-width over [0,1], top closed, emitted as `{min, max, count}` objects |
| Sweep stats | In memory since process start, named `last_run_at`, `last_pruned`, `total_pruned` |
| Counted set | All rows, with overlapping flags and a live total; the histogram covers live rows only |
| Store size | Exact pair counts matching the eval, using the per-memory bridge query; edges and bridges count when either endpoint is in the namespace |
| Recent access patterns | Deferred |
| `retrievability.decay` and `retrievability.below_recall_threshold` | Kept, though beyond the brief |
| Unknown namespace | Empty counts, not an error |
| Wiring | Documented `claude mcp add`, no tracked `.mcp.json` |
| Recording | Stays open; the PR does not wait |
| PRD section 8 "namespace whitelist" sentence | Left as it is; the mismatch is recorded in `verify.md` |

Findings raised at the review, which need no decision here:

- **A. Forgetting may rarely run in normal use.** A stdio client owns the `engramd` process and the first sweep comes after an hour, so a session under an hour never hard-prunes. Read-time filtering still works, but the eval's leaner-store result assumes pruning that normal use may never reach. Changing it is out of scope here.
- **B. Whitelist mismatch.** `docs/engram-go-rules.md` section 6, `mcp/doc.go` and PRD section 8 say `namespace` is checked against a known set; the code checks non-blank plus length. This change copies the code and leaves those three sentences alone, and `verify.md` records the mismatch.
- **C. Dev database residue.** 212 memories in 62 namespaces from integration runs. Unscoped `memory_stats` shows them all, so the demo stays scoped.
