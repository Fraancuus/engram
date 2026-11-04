# recall-ranking Specification

## Purpose
How recall treats retrievability: a filter applied before the rerank pool is cut, plus a tiebreak bounded at 0.01, so forgetting removes dead memories without penalising live ones.

## Requirements

### Requirement: Retrievability filters candidates before the pool is cut
Recall MUST drop superseded, forgotten and below-threshold-retrievability candidates before it
truncates the blended candidates to the rerank pool, unless `include_forgotten` is set. A dead
memory MUST NOT occupy a pool slot that a live memory could take.

#### Scenario: Dead candidates do not crowd the pool
- **WHEN** the pool size is 20 and the 20 most similar candidates are soft-forgotten or superseded while 5 live candidates rank lower
- **THEN** the 5 live candidates reach the pool

#### Scenario: R at the soft threshold
- **WHEN** a candidate's retrievability is exactly 0.05 at virtual time T
- **THEN** it is kept, and a candidate with retrievability 0.0499 at T is dropped

#### Scenario: include_forgotten keeps everything
- **WHEN** `include_forgotten` is true and a candidate is superseded with retrievability 0
- **THEN** the candidate stays in the pool and ranks by its blend score

#### Scenario: Empty candidates
- **WHEN** the vector search returns no seeds
- **THEN** recall returns an empty result and no error

### Requirement: Retrievability is a bounded tiebreak
The recall score MUST be 1.0·similarity + 0.3·importance + 0.01·retrievability. Retrievability MUST
therefore be unable to reorder two candidates whose similarity and importance terms differ by more
than 0.01. The sort MUST be deterministic for a given input.

#### Scenario: Fresh episode beats a permanent standard
- **WHEN** at virtual day 2 an episode with similarity 0.95, importance 0.4 and R 0.7 competes with a permanent standard with similarity 0.80, importance 0.7 and R 1.0
- **THEN** the episode ranks first

#### Scenario: Inside the bound
- **WHEN** two candidates' similarity and importance terms differ by 0.009, the lower one has retrievability 1.0 and the higher one 0.05
- **THEN** the candidate with retrievability 1.0 ranks first

#### Scenario: Outside the bound
- **WHEN** two candidates' similarity and importance terms differ by 0.011, the lower one has retrievability 1.0 and the higher one 0.05
- **THEN** the candidate with the higher similarity and importance terms ranks first

#### Scenario: Null decay order
- **WHEN** the decay model is `NullDecay`
- **THEN** the order equals the order by similarity plus 0.3·importance

#### Scenario: k at its limit
- **WHEN** k is 100, above the default pool size of 20
- **THEN** the pool holds up to 100 live candidates and at most 100 results return
