## ADDED Requirements

### Requirement: Three arms over the shipped pipeline
The harness MUST run arms A (`engram.NullDecay`), B (`engram.UniformDecay`) and C
(`engram.TypeAwareDecay`) over the same dataset and event stream, each on a fresh
`mock.MemStore`, `mock.LookupEmbedder`, `mock.CosineReranker`, a settable `engram.Clock`,
`mcp.NewTools` with a sequential id generator, and a `sweep.Sweeper` built with grace 30
days, hard floor 0.02, and batch equal to the current store size. Supersession, blend
weights, soft threshold, rerank candidates and token budget MUST be the shipped defaults
and identical in all arms; only the `DecayModel` differs.

#### Scenario: Arms differ only by decay model
- **WHEN** the harness constructs the three arms
- **THEN** the only constructor argument that differs between them is the `DecayModel`

#### Scenario: Arm A never prunes
- **WHEN** arm A completes 180 virtual days
- **THEN** its final store size equals the number of memories written

### Requirement: Virtual time per day
For each day d the harness MUST: set the clock to day d; apply writes via
`Tools.Remember` (entities, links, supersedes); apply accesses via `Tools.Recall` with the
memory's own vector and k=5; run queries via `Tools.Recall` and score them; call
`SweepOnce(ctx, now)`; record store size, edge count and bridge count. No code path in the
harness or under test MAY read wall-clock time except to measure latency.

#### Scenario: Reinforcement goes through recall
- **WHEN** a memory receives an access event on day d
- **THEN** its `AccessCount` increases and `LastAccessed` equals day d's clock value afterwards

#### Scenario: Prune at the boundary
- **WHEN** under arm B a standard was last accessed on day 60 and the sweep runs on day 150
- **THEN** its retrievability exp(-90/S) is below 0.02 only if S < 23.0 days, and the harness deletes it exactly when the sweep's floor test says so, never by its own rule

### Requirement: Error on dedup
`Tools.Remember` returning `Deduped: true` during a run MUST abort the arm with an error
naming the generator id, the existing id and the cosine.

#### Scenario: Dedup aborts
- **WHEN** a write is deduplicated
- **THEN** `Run` returns a non-nil error and no table is printed for that seed

### Requirement: Per-query metrics
For each query with returned ids R (in rank order, truncated to k) and relevant set G the
harness MUST compute precision@k = |R∩G|/k, recall@k = |R∩G|/|G|, nDCG@k with binary gain
and log2(rank+1) discount normalised by the ideal DCG for min(|G|, k), and reciprocal rank
of the first relevant id (0 if none). k defaults to 5 and is set by `-k`.

#### Scenario: Known values
- **WHEN** R = [a, x, b, y, z], G = {a, b, c}, k = 5
- **THEN** precision@5 = 0.4, recall@5 = 0.6667 (4 d.p.), RR = 1, nDCG@5 = (1 + 1/log2(4)) / (1 + 1/log2(3) + 1/log2(4))

#### Scenario: Empty result
- **WHEN** R is empty
- **THEN** all four metrics are 0

### Requirement: Aggregates per arm
Per arm and seed the harness MUST report: means of the four metrics; recall stratified by
the type of the relevant memory (procedural, semantic, episodic); bridge recall split by
link and entity; share of returned results by `retrieved_via` class (vector, link, entity);
latency p50 and p95 of `Recall`; final store size, edge count, bridge count; and a series
of (day, size, edges, bridges) every 10 days. Across seeds it MUST report mean and sample
standard deviation.

#### Scenario: Type-stratified recall
- **WHEN** a relevant procedural memory is returned in 9 of 10 queries whose relevant set contains it
- **THEN** procedural recall for that arm is 0.9

### Requirement: Report and recommendation
The harness MUST print a plain-text table (arms as rows) to stdout and, with
`-json <path>`, write the full results. It MUST print a line
`recommended: <name> (ENGRAM_DECAY=<name>)` where `<name>` is the arm with the highest mean
nDCG@k; a tie within 0.005 goes to the smaller final store. The names MUST be the
`DecayByName` names.

#### Scenario: Tie-break by store size
- **WHEN** arms B and C have mean nDCG within 0.005 and C's final store is smaller
- **THEN** the recommendation names `type-aware`

#### Scenario: Null result still reports
- **WHEN** arm A has the highest nDCG
- **THEN** the recommendation names `none` and the exit code is 0 unless `--ci` fails the gate
