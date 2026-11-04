# similarity-thresholds Specification

## Purpose
The dedup, auto-link and propagation thresholds, stated on the store's normalised similarity scale with their raw-cosine equivalents.

## Requirements

### Requirement: Thresholds are stated on the normalised scale
Dedup, auto-link and propagation thresholds MUST compare against the store's normalised similarity
(1 + cos) / 2, and each constant's comment MUST state its raw-cosine equivalent. Their values MUST be:
dedup 0.95 (raw 0.90), auto-link 0.925 (raw 0.85), propagation 0.925 (raw 0.85).

#### Scenario: Auto-link at the boundary
- **WHEN** a new memory's candidate scores exactly 0.925
- **THEN** an auto-link is written with weight 0.925

#### Scenario: Below the auto-link point
- **WHEN** a new memory's candidate scores 0.9249
- **THEN** no auto-link is written

#### Scenario: Propagation at the boundary
- **WHEN** at virtual time T a recalled memory has one edge of weight 0.925 and one of weight 0.9249
- **THEN** only the first neighbor's `last_accessed` becomes T

#### Scenario: Explicit links still propagate
- **WHEN** a recalled memory has an explicit link, stored at weight 1.0, to a live memory
- **THEN** that neighbor's `last_accessed` is refreshed
