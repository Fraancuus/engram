## MODIFIED Requirements

### Requirement: Typed memory mix per topic
Each topic MUST belong to exactly one namespace and MUST contain: one procedural standard
with 1–3 versions where each version after the first carries `Supersedes` = the previous
version's generator id; 1–2 semantic fact chains of 2–4 versions each, where each version after the
first carries `Supersedes` = its chain predecessor's generator id; and episodic events that fill the
topic's share of the memory budget. Importance MUST be 0.7 for standards, 0.5 for facts,
and in [0.3, 0.5] for episodes.

#### Scenario: Topic composition
- **WHEN** a dataset is generated at the floor
- **THEN** every topic has exactly one standard chain of length 1–3, 1–2 fact chains of 2–4 versions where only each first version has `Supersedes == ""`, and at least one episode, all in the topic's namespace

#### Scenario: Superseding a pruned predecessor
- **WHEN** under arm B a fact version's predecessor was pruned before the version's write day
- **THEN** the write succeeds and the run continues

### Requirement: Vector geometry
Memory vectors MUST be unit vectors of the configured dimension built as
normalize(topic centroid + 0.35·kind offset + σ·noise) with frozen named σ constants.
Topic centroids MUST be mutually orthonormal whenever `Topics ≤ Dim`. All bounds below are
RAW cosines; the store reports `(1 + cos) / 2`, so the pipeline's dedup threshold 0.95 is
raw cos 0.90 and its auto-link threshold 0.925 is raw cos 0.85. On the CI seed at the floor:
ordinary in-topic pairs (not twins, not chain mates) have cosine in [0.40, 0.66], below the
auto-link point with margin, so general chatter does not link; cross-topic cosine has absolute value
below 0.30 over a seeded sample of 2,000 pairs (the all-pairs maximum is reported in the
design, not bounded); designated "twin" episode pairs have pairwise cosine in [0.86, 0.89] (they
auto-link, they never dedup); versions of one fact chain have pairwise cosine in [0.74, 0.88]; and
no two memories in the same namespace have cosine ≥ 0.90 (the dedup point). The dedup bound MUST hold
on every seed by construction; the other ranges are asserted on the CI seed.

#### Scenario: No dedup collisions
- **WHEN** a dataset is generated at the floor
- **THEN** the maximum pairwise cosine among memories sharing a namespace is below 0.90

#### Scenario: Orthonormal centroids at the floor
- **WHEN** a dataset is generated with Topics ≤ Dim
- **THEN** every pair of distinct topic centroids has |cosine| < 1e-6 and every centroid has unit norm

#### Scenario: Twins cross the auto-link threshold
- **WHEN** a twin pair is generated
- **THEN** its cosine is ≥ 0.86 and ≤ 0.89

### Requirement: Event schedule over virtual days
The schedule MUST place events on integer days in [0, days): standard v1 writes on days
0–10, later versions on days 30–90, standard accesses every ~7 days until day 60 and none
after; fact-chain versions written in order on days 0–120 (each later than the previous)
with 1–3 accesses in the 30 days after each version's write; episode writes
uniformly over the run with 0–2 accesses within 3 days; queries by class as topic-current (days
150–179), fact (t+60..120 after the chain's latest version, clamped to the run), episode-recent
(t+1..3), episode-old (t+60..120), bridge (half link, half entity, any day after both X and Y
exist). `Params.Mix` MUST name a preset that sets the percentage of each class: `agent` 35 / 20 / 30
/ 5 / 10 and `stress` 25 / 15 / 20 / 15 / 25, in the order topic-current, fact, episode-recent,
episode-old, bridge. `Floor` MUST use `agent`, and `Validate` MUST reject an unknown mix. The mix MUST
NOT change memories, vectors, links, write days or accesses for a given seed. An event MUST never
reference a memory before its write day.

#### Scenario: No access before write
- **WHEN** the schedule is generated
- **THEN** every access and query day is ≥ the write day of every memory it references

#### Scenario: Agent mix
- **WHEN** 500 queries are generated with mix `agent`
- **THEN** the counts per class are within ±2 of 175/100/150/25/50

#### Scenario: Query mix
- **WHEN** 500 queries are generated with mix `stress`
- **THEN** the counts per class are within ±2 of 125/75/100/75/125

#### Scenario: Unknown mix
- **WHEN** `Validate` runs on `Params` with mix `weekend`
- **THEN** it returns an error naming the mix

#### Scenario: Same corpus across mixes
- **WHEN** one seed generates datasets with mixes `agent` and `stress`
- **THEN** their memories, vectors, links, write days and accesses are identical

### Requirement: Generator-owned ground truth
Each query MUST carry its relevant set from the generator: topic-current → {standard
version current on the query day}; fact → {the chain's latest version written on or before
the query day}, with the query vector built from the chain centroid (version-agnostic), so older
versions match it closely and only their superseded flag keeps them out; episode → {that episode} at
any age; bridge → {X, Y}. Queries MUST be scoped to the topic's namespace, except entity-bridge
queries which pass both namespaces involved. Labels MUST NOT depend on any pipeline output.

#### Scenario: Topic-current picks the current version
- **WHEN** a topic has versions v1 (day 5) and v2 (day 40) and a topic-current query on day 160
- **THEN** the relevant set is {v2}

#### Scenario: Fact query picks the latest version
- **WHEN** the relevance rule is applied to a chain with v1 (day 10), v2 (day 70), v3 (day 115) for a query day of 100 (a unit-level case: generated fact queries always fall after the last version, so this exercises the rule, not the schedule)
- **THEN** the relevant set is {v2}; and for every generated fact query cos(query, each other version of its chain) ≥ 0.80

#### Scenario: Entity-bridge query scope
- **WHEN** a bridge query's X and Y are in namespaces n1 and n2
- **THEN** the query's namespace list is exactly {n1, n2}

#### Scenario: Pruned hop node
- **WHEN** a bridge query's hop node X was pruned before the query day
- **THEN** the labels still say {X, Y} and the query scores what the pipeline returns
