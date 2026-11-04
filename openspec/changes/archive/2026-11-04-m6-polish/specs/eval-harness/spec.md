## MODIFIED Requirements

### Requirement: Virtual time per day
For each day d the harness MUST: set the clock to day d; apply writes via
`Tools.Remember` (entities, links, supersedes); apply accesses via `Tools.Recall` with the
memory's own vector and k=1; run queries via `Tools.Recall` and score them; call
`SweepOnce(ctx, now)`; and every 10 days record store size, edge count and bridge count. No code path in the
harness or under test MAY read wall-clock time except to measure latency.

#### Scenario: Reinforcement goes through recall
- **WHEN** a memory receives an access event on day d
- **THEN** its `AccessCount` increases and `LastAccessed` equals day d's clock value afterwards

#### Scenario: Prune at the boundary
- **WHEN** under arm B a standard was last accessed on day 60 and the sweep runs on day 150
- **THEN** its retrievability exp(-90/S) is below 0.02 only if S < 23.0 days, and the harness deletes it exactly when the sweep's floor test says so, never by its own rule

### Requirement: Report and recommendation
The harness MUST print a plain-text table (arms as rows) to stdout and, with `-json <path>`, write
the full results. The report MUST state the rerank mode, the mix and lambda, and MUST print a
`score` column where score = nDCG@k - lambda × (arm's mean final store / arm `none`'s mean final
store), or the largest arm present when `none` is absent. Lambda MUST default to the documented
constant 0.10 and `-lambda` MUST override it; a negative, NaN or infinite value MUST exit 2. It MUST
print the break-even lambda of each decaying arm against `none`, and a line
`recommended: <name> (ENGRAM_DECAY=<name>) on mix <mix>, lambda <lambda>` where `<name>` is the arm
with the highest score; a tie within 0.005 goes to the smaller final store. The names MUST be the
`DecayByName` names.

#### Scenario: Tie-break by store size
- **WHEN** arms B and C have scores within 0.005 and C's final store is smaller
- **THEN** the recommendation names `type-aware`

#### Scenario: Null result still reports
- **WHEN** arm A has the highest score
- **THEN** the recommendation names `none` and the exit code is 0 unless `--ci` fails the gate

#### Scenario: Store cost changes the winner
- **WHEN** arm A has nDCG 0.98 at store 5000 and arm C has nDCG 0.90 at store 1500, with lambda 0.20
- **THEN** A scores 0.78, C scores 0.84, and the recommendation names `type-aware`

#### Scenario: Lambda zero is retrieval only
- **WHEN** `-lambda 0` runs with the same arms
- **THEN** the score column equals nDCG and the recommendation names `none`

#### Scenario: Break-even
- **WHEN** arm A has nDCG 0.98 at store 5000 and arm C has nDCG 0.90 at store 1500
- **THEN** the report prints break-even 0.114 for `type-aware` (0.08 / 0.70, 3 d.p.)

#### Scenario: Arm with no smaller store
- **WHEN** an arm's mean final store equals `none`'s
- **THEN** its break-even prints as `n/a` and nothing divides by zero

#### Scenario: Invalid lambda
- **WHEN** `-lambda -0.1` or `-lambda NaN` is passed
- **THEN** the command exits 2 before any run starts

#### Scenario: Recommendation names its mix
- **WHEN** a run uses `-mix stress`
- **THEN** the header and the recommendation line both name `stress`

## ADDED Requirements

### Requirement: Access hit-rate table
The harness MUST report, per arm and memory kind (standard, fact, episode), the share of accesses
whose top result was the accessed memory itself, where an access is `Tools.Recall` at k=1 on the
memory's own vector at the access's virtual day. A kind with no accesses in a seed MUST be left out
of that seed's mean rather than counted as 0. The table MUST print on every run.

#### Scenario: Target ranked first
- **WHEN** on virtual day 12 an episode's access returns that episode first in 9 of 10 accesses for its kind
- **THEN** that arm's episode hit rate is 0.900

#### Scenario: A superseded target misses
- **WHEN** a standard is accessed after a newer version superseded it
- **THEN** the access counts as a miss in every arm, because recall excludes superseded memories

#### Scenario: Empty result
- **WHEN** an access returns no results
- **THEN** it counts as a miss and the run continues

#### Scenario: No accesses for a kind
- **WHEN** a seed has no accesses of a kind
- **THEN** that seed does not contribute to the kind's mean
