## ADDED Requirements

### Requirement: Seeded, deterministic generation
The generator MUST produce a byte-identical dataset (memories, vectors, entities, links,
schedule, queries, labels) for the same `Params` and seed, with no dependence on wall-clock
time, map iteration order, or goroutine scheduling.

#### Scenario: Same seed, same dataset
- **WHEN** `Generate(params)` is called twice with the same `Params` (including `Seed`)
- **THEN** a content hash over both datasets is equal

#### Scenario: Different seed, different dataset
- **WHEN** `Generate` is called with two seeds that differ
- **THEN** the content hashes differ

### Requirement: Size floor
`Params.Validate` MUST reject parameters below the floor: 3 namespaces, 50 topics, 5,000
memories, 200 entities, 500 queries, 180 virtual days, vector dimension 64. `-scale n`
MUST multiply topics, memories, entities and queries by `n` and leave the other dimensions
unchanged.

#### Scenario: Below the floor is rejected
- **WHEN** `Params` has 4,999 memories and otherwise valid values
- **THEN** `Validate` returns an error naming the dimension and the floor

#### Scenario: Floor is accepted
- **WHEN** `Params` equals the floor exactly
- **THEN** `Validate` returns nil and `Generate` produces at least 5,000 memories and 500 queries

#### Scenario: Scale multiplies the right dimensions
- **WHEN** `-scale 2` is applied to the floor
- **THEN** topics = 100, memories ≥ 10,000, entities = 400, queries = 1,000, namespaces = 3, days = 180, dim = 64

### Requirement: Typed memory mix per topic
Each topic MUST belong to exactly one namespace and MUST contain: one procedural standard
with 1–3 versions where each version after the first carries `Supersedes` = the previous
version's generator id; 2–4 write-once semantic facts; and episodic events that fill the
topic's share of the memory budget. Importance MUST be 0.7 for standards, 0.5 for facts,
and in [0.3, 0.5] for episodes.

#### Scenario: Topic composition
- **WHEN** a dataset is generated at the floor
- **THEN** every topic has exactly one standard chain of length 1–3, 2–4 facts, and at least one episode, all in the topic's namespace

### Requirement: Vector geometry
Memory vectors MUST be unit vectors of the configured dimension built as
normalize(topic centroid + 0.35·kind offset + σ·noise) with frozen named σ constants such
that: in-topic pairwise cosine lies in [0.70, 0.94]; cross-topic cosine has absolute value
below 0.30; designated "twin" episode pairs have cosine in [0.86, 0.94]; and no two
memories in the same namespace have cosine ≥ 0.95 (the dedup threshold).

#### Scenario: No dedup collisions
- **WHEN** a dataset is generated at the floor
- **THEN** the maximum pairwise cosine among memories sharing a namespace is below 0.95

#### Scenario: Twins cross the auto-link threshold
- **WHEN** a twin pair is generated
- **THEN** its cosine is ≥ 0.86 and ≤ 0.94

### Requirement: Deliberate interconnection
The generator MUST emit: explicit links from each standard version to the next, from every
fact to the standard version current at the fact's write time, and from 2% of episodes to
one memory in another topic; 1–3 entity mentions per memory; hub entities (5% of the pool)
mentioned from at least 3 topics spanning at least 2 namespaces; and bridge-only pairs (X,
Y) where cos(query, Y) < 0.30 and Y is reachable from X by exactly one link or one shared
entity. At least half of the hop nodes X MUST be episodic. Hub fan-out MUST stay below 20
memories per entity so `neighborCapPerSeed` never truncates a bridge.

#### Scenario: Bridge-only pair is unreachable by vector alone
- **WHEN** a bridge query is generated with relevant set {X, Y}
- **THEN** cos(query vector, Y) < 0.30 and Y is a 1-hop link or entity neighbor of X

#### Scenario: Hub entity spans namespaces
- **WHEN** an entity is marked as a hub
- **THEN** memories mentioning it belong to at least 2 namespaces and 3 topics

#### Scenario: Hub fan-out under the cap
- **WHEN** a dataset is generated at the floor
- **THEN** no entity is mentioned by 20 or more memories

### Requirement: Event schedule over virtual days
The schedule MUST place events on integer days in [0, days): standard v1 writes on days
0–10, later versions on days 30–90, standard accesses every ~7 days until day 60 and none
after; fact writes on days 0–60 with 1–3 accesses in the following 30 days; episode writes
uniformly over the run with 0–2 accesses within 3 days; queries as the mix 25%
topic-current (days 150–179), 15% fact (t+60..120), 20% episode-recent (t+1..3), 15%
episode-old (t+60..120), 25% bridge (half link, half entity, any day after both X and Y
exist). An event MUST never reference a memory before its write day.

#### Scenario: No access before write
- **WHEN** the schedule is generated
- **THEN** every access and query day is ≥ the write day of every memory it references

#### Scenario: Query mix
- **WHEN** 500 queries are generated
- **THEN** the counts per class are within ±2 of 125/75/100/75/125

### Requirement: Generator-owned ground truth
Each query MUST carry its relevant set from the generator: topic-current → {standard
version current on the query day}; fact → {that fact}; episode → {that episode} at any
age; bridge → {X, Y}. Queries MUST be scoped to the topic's namespace, except entity-bridge
queries which pass both namespaces involved. Labels MUST NOT depend on any pipeline output.

#### Scenario: Topic-current picks the current version
- **WHEN** a topic has versions v1 (day 5) and v2 (day 40) and a topic-current query on day 160
- **THEN** the relevant set is {v2}

#### Scenario: Entity-bridge query scope
- **WHEN** a bridge query's X and Y are in namespaces n1 and n2
- **THEN** the query's namespace list is exactly {n1, n2}
