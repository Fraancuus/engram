## ADDED Requirements

### Requirement: Tool input and validation
The `memory_stats` tool MUST take one optional string, `namespace`. Omitted or empty MUST mean
every namespace. A non-empty value MUST be non-blank and at most `maxNamespaceBytes`, enforced by
the handler with the same check as `recall` and `remember`, and MUST NOT be checked against a known
set. A namespace holding no memories MUST NOT be an error.

#### Scenario: Blank namespace rejected
- **WHEN** `namespace` is three spaces
- **THEN** the call returns an error naming the bounds, and the store is not called

#### Scenario: Oversized namespace rejected
- **WHEN** `namespace` is `maxNamespaceBytes` + 1 bytes
- **THEN** the call returns an error naming the bounds, and the store is not called

#### Scenario: Namespace at its limit accepted
- **WHEN** `namespace` is exactly `maxNamespaceBytes` bytes
- **THEN** the call succeeds

#### Scenario: Unknown namespace over the wire
- **WHEN** a client calls the tool through an MCP transport with a namespace that holds no memories
- **THEN** the call succeeds with `by_namespace` an empty object, zero counts, ten zero buckets and `store_size` all zero

#### Scenario: Empty store
- **WHEN** the store holds no memories and `namespace` is omitted
- **THEN** the call succeeds with the same empty shape

### Requirement: Output is structured and always complete
The tool MUST return `counts`, `retrievability`, `store_size` and `sweep`, with every key present
and no JSON `null` except `sweep.last_run_at`. `counts.by_namespace` MUST always be an object, never
null. The tool MUST be registered with `ReadOnlyHint: true`.

#### Scenario: Keys present when empty
- **WHEN** the output for an empty store is marshalled
- **THEN** `by_namespace` is `{}`, `buckets` has ten entries, and `by_type` has `episodic`, `semantic` and `procedural`, each 0

### Requirement: Counts cover all rows
`counts.total` and `store_size.memories` MUST both be the number of rows in scope, taken from one
fold so they cannot differ. `counts.by_type` and `counts.by_namespace` MUST count all rows in scope.
`counts.live` MUST count rows neither superseded nor forgotten. `counts.superseded`,
`counts.forgotten` and `counts.pinned` MUST count their flags, which overlap. Counts MUST NOT be
affected by the row cap.

#### Scenario: Flags overlap
- **WHEN** the store holds one row superseded and forgotten, one pinned and live, and one plain live
- **THEN** `total` is 3, `live` is 2, `superseded` is 1, `forgotten` is 1 and `pinned` is 1

#### Scenario: Scoped to a namespace
- **WHEN** rows exist in namespaces a and b and `namespace` is a
- **THEN** `by_namespace` has only the key a, and `total` counts only a's rows

#### Scenario: Counts exact above the cap
- **WHEN** the row cap is 2 and three live rows exist
- **THEN** `live` is 3 and `truncated` is true

### Requirement: Retrievability histogram
`engram.RetrievabilityHistogram(ms, d, now)` MUST be a pure function that counts `ms` into ten
fixed-width buckets over [0,1] using `d.Retrievability(m, now)`, half-open except the last, which
MUST be closed so R = 1.0 lands in it. An out-of-range R MUST clamp into the first or last bucket.
The tool's histogram MUST cover live rows only, computed at the injected clock's now, and
`retrievability.decay` MUST name the active decay model. `retrievability.below_recall_threshold`
MUST count the fetched live rows with R below the recall soft threshold.

#### Scenario: Bucket boundaries
- **WHEN** rows have R of 0, 0.0999, 0.1, 0.5, 0.9, 0.9999 and 1.0
- **THEN** the buckets holding them are 0, 0, 1, 5, 9, 9 and 9

#### Scenario: Out of range clamps
- **WHEN** a `DecayModel` returns -0.5 and 1.5
- **THEN** they land in buckets 0 and 9, and nothing panics

#### Scenario: Virtual time fixes the curve
- **WHEN** at virtual time T an episodic memory of importance 0 and access count 0 was last accessed 6 days before T under `TypeAwareDecay` (S = 2 days, so R = e^-3, about 0.0498)
- **THEN** it lands in bucket 0 and counts in `below_recall_threshold`

#### Scenario: Just above the soft threshold
- **WHEN** at virtual time T the same memory was last accessed 5.9 days before T (R about 0.0523)
- **THEN** it lands in bucket 0 and does not count in `below_recall_threshold`

#### Scenario: Permanent memory in the top bucket
- **WHEN** at virtual time T a procedural memory last accessed a year before T is counted under `TypeAwareDecay`
- **THEN** R is 1 and it lands in bucket 9

#### Scenario: Superseded and forgotten rows excluded
- **WHEN** one live, one superseded and one forgotten row exist
- **THEN** the buckets sum to 1, and `counts.live` is 1

### Requirement: Row cap
The handler MUST fetch at most `cap + 1` live rows without content or embedding, ordered by id,
keep at most `cap`, and set `retrievability.truncated` when more than `cap` came back. The
production cap MUST be 10,000.

#### Scenario: Exactly at the cap
- **WHEN** the cap is 2 and two live rows exist
- **THEN** `truncated` is false and the buckets sum to 2

#### Scenario: One above the cap
- **WHEN** the cap is 2 and three live rows exist
- **THEN** `truncated` is true and the buckets sum to 2

### Requirement: Store size as counts
`store_size` MUST report `memories`, `link_edges` (distinct undirected edges) and `entity_bridges`
(distinct unordered memory pairs sharing at least one entity). With a namespace, an edge or bridge
MUST count when either endpoint is in that namespace.

#### Scenario: Pairs counted once
- **WHEN** entity e is mentioned by m1, m2 and m3, entity f is also mentioned by m1 and m2, and m1 links to m2 in both directions
- **THEN** `entity_bridges` is 3 and `link_edges` is 1

#### Scenario: Cross-namespace edge touches both
- **WHEN** m1 in namespace a links to m2 in namespace b
- **THEN** `link_edges` is 1 for a, for b and with no namespace

### Requirement: Strictly read-only
The tool MUST NOT change any stored value: no reinforcement, no `last_accessed` or `access_count`
change. The Neo4j queries behind it MUST run with readers routing, so the server rejects a write.

#### Scenario: A stats call changes nothing
- **WHEN** at virtual time T the tool runs over a memory last accessed at T minus one day, on either store
- **THEN** the memory's `LastAccessed`, `AccessCount` and `Stability` are unchanged, and `FakeStore` records no reinforce

#### Scenario: Server rejects a write on the stats path
- **WHEN** an integration test runs a write through the routing option the stats queries use
- **THEN** Neo4j rejects it with an access-mode error

### Requirement: Sweep stats
`Sweeper.tick` MUST record each completed pass in memory with `SweepOnce` unchanged: `LastRunAt` is
the `now` the pass ran at, `LastPruned` its count, `TotalPruned` the sum since process start. A
failed or panicking pass MUST leave them unchanged. `Stats()` MUST be safe to call while a tick runs.
The tool MUST report them as `sweep.last_run_at`, `sweep.last_pruned` and `sweep.total_pruned`, with
`last_run_at` null before the first completed pass, and they MUST NOT depend on `namespace`.
`WithSweepStats` MUST supply them to `Tools`; without it the tool MUST report the empty values.

#### Scenario: Before the first tick
- **WHEN** no pass has completed
- **THEN** `last_run_at` is JSON null and both counts are 0

#### Scenario: Accumulates at fixed virtual time
- **WHEN** at virtual time T a pass prunes 2, then at T plus one hour a pass prunes 0
- **THEN** `LastRunAt` is T plus one hour, `LastPruned` is 0 and `TotalPruned` is 2

#### Scenario: Prune boundary
- **WHEN** a candidate's R is exactly the hard floor in one pass and just below it in another
- **THEN** the first pass records `LastPruned` 0 with `LastRunAt` set, and the second records 1

#### Scenario: Failed pass leaves stats alone
- **WHEN** a completed pass is followed by one where `PruneCandidates` fails
- **THEN** the stats still describe the completed pass

#### Scenario: Concurrent read
- **WHEN** `Stats` and `tick` run concurrently under the race detector
- **THEN** no race is reported

### Requirement: Errors stay legible
A store failure MUST reach the caller as `memory_stats: store unavailable`, with the detail logged
and not returned. A panic in the handler MUST be recovered and returned as
`memory_stats: internal error`.

#### Scenario: Store failure sanitized
- **WHEN** `Counts` or `LiveMemories` returns an error naming a Cypher statement
- **THEN** the returned error is `memory_stats: store unavailable` and does not contain the statement

### Requirement: The eval does not move
This capability MUST NOT change `Recall`, `Remember`, `SweepOnce` or any eval constant, and MUST NOT
change `eval/baseline.json`. A/B/C and the CI gate are unaffected.

#### Scenario: Gate and baseline
- **WHEN** verify runs `go run ./cmd/eval --ci` and `git diff origin/main -- eval/baseline.json`
- **THEN** the gate passes and the diff is empty
