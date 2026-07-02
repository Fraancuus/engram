# 0001 — Record architecture decisions

## Status
Accepted (2026-07-01)

## Context
Engram is a portfolio-grade service where the *reasoning behind* technical choices is
part of the deliverable. The PRD (§3) already captures the founding decision records
(DR-1 Neo4j vs Postgres, DR-2 CPU-only inference, DR-3 sidecar inference) inline. As
decisions accumulate and get revisited — e.g. the DR-1 re-evaluation checkpoint at M2, or
picking an embedding model and acceleration path for the developer's AMD hardware — we
want a durable, greppable, per-decision trail rather than edits buried in the PRD.

## Decision
Record significant architecture decisions as numbered ADRs in `docs/adr/`, one file per
decision, in Nygard/MADR style (Status · Context · Decision · Consequences · Options
considered). ADRs are append-only: a superseding decision gets a new ADR and marks the
old one `Superseded by NNNN`. The PRD's existing DRs remain authoritative for what they
cover; new and revisited decisions land here.

## Consequences
- A clear, reviewable history of *why* the architecture is what it is — easy to link from
  code and pull requests.
- Minor, bounded duplication with the PRD's DR section until (optionally) those are
  migrated. No migration is required by this decision.
- Each ADR stays small and self-contained.
