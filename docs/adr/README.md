# Architecture Decision Records

Significant architecture decisions for Engram, recorded as lightweight ADRs
(Michael Nygard / MADR style) — one file per decision, numbered and append-only. A
decision that changes an earlier one gets a **new** ADR that marks the old one
`Superseded by NNNN`.

## Format
Each record: **Status** (`Proposed` · `Accepted` · `Superseded by NNNN`), **Context**,
**Decision**, **Consequences**, and the **Options considered**.

## Index
- [0001](0001-record-architecture-decisions.md) — Record architecture decisions in ADRs — *Accepted*
- [0002](0002-embedding-model-and-amd-acceleration.md) — Embedding model & AMD RDNA / CPU acceleration — *Accepted*

## Relationship to the PRD decision records
The PRD (`docs/engram-prd-v1.md` §3) holds the founding decision records DR-1..DR-3. Those
remain valid; new and revisited decisions are captured here. DR-1 (Neo4j vs Postgres) was
resolved at the M2 checkpoint — keep Neo4j.
