## Design Summary

Finish milestone M6 with three deliverables, in this order.

1. **`memory_stats`**, the fourth and last MCP tool in PRD section 8. It is strictly read-only and returns structured JSON: counts by type and namespace, a retrievability histogram, store size as counts, and in-memory sweep stats.
2. **A real agent using Engram end to end.** This is the v1 acceptance condition in PRD sections 8 and 9. It needs a way for Claude Code to start `engramd`, a stdio smoke test behind the `integration` tag, and one kept transcript of Claude Code choosing the tool calls itself from plain-language prompts.
3. **A demo script**, `docs/demo.md`, plus a README section that links it. Franco records the demo. The PR does not wait for the recording.

Franco set the shape of this change in his brief, so this brainstorm ran without one-at-a-time questions: every open question goes to the visual plan with a recommendation, and the visual plan is the single review stop. The facts below were checked against live code and by throwaway probes, recorded in `design.md`.

This change moves no eval number. A/B/C and the CI gate are unaffected, and verify proves it.

## Alternatives Considered

### Alternative A: Count everything from one capped row fetch
- **Approach**: One store method returns up to N rows; Go derives counts and the histogram from them.
- **Pros**: One Cypher query and one fold. No aggregate query to keep in parity with the mock.
- **Cons**: Above the cap every count silently becomes wrong, not only the histogram.
- **Why not chosen**: Counts must stay exact at any store size. Only the histogram needs rows, because retrievability lives in `DecayModel` and cannot run in Cypher.

### Alternative B: A dedicated lightweight row type for the histogram
- **Approach**: The store returns a new `engram.StatsRow` holding only the fields decay reads.
- **Pros**: The type states that content and embedding are absent.
- **Cons**: `DecayModel.Retrievability` takes `Memory`, so every row needs converting back. It adds a type and a mapper in each store.
- **Why not chosen**: `engram.Memory` with empty `Content`, `Source` and `Embedding` is already how `PruneCandidates` and the recall projections work. `mapToMemory` keeps its required-property checks by projecting `content: ''`.

### Alternative C: Agreed approach
- **Approach**: Exact aggregates from Cypher, plus capped live rows folded by a pure root-package function. See below.
- **Pros**: Exact counts at any size, a bounded fetch, and a pure table-testable histogram.
- **Cons**: Two read methods to keep in parity across two stores, with no shared behavioural test.
- **Why not chosen**: Chosen.

### Agent wiring alternatives
- **Tracked `.mcp.json`**: every session in this repo tries to start the server, including sessions doing unrelated work. With the stack down the client sees only `calling "initialize": EOF`. With `go run` as the command it also recompiles on each start. Not chosen.
- **Documented `claude mcp add --scope local`**: affects only the person who runs it, and tracks no file. Chosen for day-to-day use.
- **`--strict-mcp-config --mcp-config '<json>'`**: leaves no state, and hides every other MCP server from the session and from a recording. Chosen for the transcript and the demo.

## Agreed Approach

Alternative C. Two narrow read methods at the consumer, following `forgetStore` in `mcp/forget.go`, and a pure histogram in the root package. Sweep stats are recorded in `Sweeper.tick` with `SweepOnce` unchanged, and reach the handler through a `WithSweepStats` option holding a function value, so `mcp` never imports `sweep`.

The real run is possible here: a probe drove today's `engramd` with headless `claude -p`, `remember` in one process and `recall` plus a hard `forget` in a second. The kept transcript comes from a later run with all four tools.

## Key Decisions

Full reasoning is in `design.md`.

- Histogram covers live rows only, because that is what recall can return.
- Counts cover all rows, with overlapping superseded, forgotten and pinned flags.
- Row cap 10,000, ordered by id. Ids are 128 random bits, so a truncated histogram is an unbiased sample.
- Store size is counts: memories, link edges, entity bridges. The bridge query runs per memory, because plain `DISTINCT` took 131 s at 12.5M pairs against 20 s.
- Every stats query uses readers routing, so the server itself refuses a write.
- No tracked `.mcp.json`.
- `architect` approved the types and interfaces. `scoping-ux` found one blocker, the description implying sweep stats follow `namespace`, and added `counts.total`, `retrievability.decay` and `retrievability.below_recall_threshold`. The last two go beyond Franco's list; he kept both at plan review.

## Open Questions

None remain. Franco's answers are in "Decisions at plan review" in `design.md`.
