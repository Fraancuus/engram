## Design Summary

M5's first floor run recommended `none`. The vault note `Projects/Engram/M5 eval - results and
correction plan.md` traced that to three product defects and an instrument that was loaded
against forgetting. M6 fixes both, re-baselines, and rewrites the README claim to whatever the
new numbers support. Franco's launch prompt (2026-11-03) fixes the scope and its order:

1. Retrievability is a candidate filter plus a tiebreak, not a 0.5 additive blend term.
2. Explicit semantic supersession. The caller passes `Supersedes` on a semantic `remember`, the
   generator sets it on fact chains, and chain links stop refreshing superseded versions.
3. Query mix is a named `-mix` preset. The agent-realistic mix is the primary table, the M5 mix
   stays as `stress`, and the recommendation names its mix.
4. Recommendation score = nDCG - lambda * (store / baseline store), with lambda a documented
   constant, printed in the table and overridable by flag.
5. `linkThreshold` and `propagateThreshold` restated on the normalised (1+cos)/2 scale, then
   re-baseline with `--update-baseline -seed 42` on amd64 and `--ci` twice.
6. README claim reframed to what the numbers support.
7. Spec requirements for the access hit-rate table and the stale-baseline guard.

Decided, not reopened: a store cost weight exists; semantic supersession is explicit only
(detection is v2).

Reading the code surfaced four things the note does not say. They shape the design:

- **Supersession removes A's last handicap.** D10 applies supersession in every arm, and
  `scoreAndFilter` drops superseded memories in every arm. Once fact chains are superseded, arm
  `none` loses nothing to stale facts: its fact nDCG climbs from 0.673 toward 1.0. The note's
  "fact class widens type-aware's lead" holds against uniform, not against `none`. After M6 no
  query class exists where forgetting can buy retrieval quality over `none`; at best C ties it.
  So the store weight, not nDCG, decides A versus C. This is a finding to report, not a defect.
- **The lambda value can decide the winner.** A rough estimate puts the break-even near 0.16
  (design D14). A chosen constant near that point is a verdict in disguise, so the value is fixed
  in the design before any M6 run, and the report prints the break-even next to it.
- **Dedup defeats explicit supersession.** `doRemember` dedups and returns before it applies
  `Supersedes`. A corrected fact usually embeds very close to the old one (for example "deploys
  on Tuesday" then "deploys on Thursday"). Above raw cosine 0.90 the new content is dropped, the
  old memory is reinforced, and the caller's replace is silently ignored. The generator's chain
  band stays under 0.90, so the eval would never show this. It is a data-loss bug on the new
  path, so it is in scope.
- **Dead candidates crowd the pool.** `blend` truncates to the pool size before `scoreAndFilter`
  drops dead memories, so a filter that is meant to remove them still lets them take pool slots.
  A filter has to run before the truncation.

## Alternatives Considered

### Alternative A: Pure lexicographic tiebreak (bucket the score, then R)
- **Approach**: sort key (floor(score/0.01), R desc, score desc, id).
- **Pros**: R has no arithmetic effect on the score.
- **Cons**: more code; a boundary artefact at bucket edges; within a bucket a tiny R difference
  overrides a score gap of up to 0.01. A plain "within 0.01" comparator is worse: it is not
  transitive, so it breaks `sort.Slice`.
- **Why not chosen**: a small bounded additive term gives the same guarantee with one constant.

### Alternative B: Re-weight one run per class instead of simulating each mix
- **Approach**: run once, then compute each mix as a weighted mean of the per-class nDCG.
- **Pros**: one run per table, so half the CI time for two tables.
- **Cons**: wrong. A query reinforces what it returns, so the mix changes which memories survive.
- **Why not chosen**: a mix is a simulation parameter; only a real run reports its store size.

### Alternative C: Lower the chain-link weight instead of skipping superseded targets
- **Approach**: write version links below `propagateThreshold`, so they never propagate.
- **Pros**: no store change.
- **Cons**: the link weight also scales the expansion score, so bridges through a chain weaken.
  It also would not stop an auto-link at a high weight refreshing a superseded version.
- **Why not chosen**: skipping superseded targets states the intent and costs one predicate.

### Alternative D: Print both mixes from every run
- **Approach**: `go run ./cmd/eval` runs primary and stress in one invocation.
- **Pros**: nobody can skip the stress table.
- **Cons**: doubles every local run, adds orchestration code to `cmd/eval`.
- **Why not chosen**: two invocations give the same tables with no code; the README carries both.

## Agreed Approach

Smallest change that makes the seven items true:

- `defaultWRet` goes from 0.5 to 0.01. R can then reorder only memories already within 0.01 of
  each other. The soft-forget filter stays and moves ahead of pool truncation.
- `remember` applies `Supersedes` to semantic writes too. Dedup ignores a candidate that is
  already superseded or is named in `Supersedes`. `PropagateReinforce` skips superseded targets
  in both stores.
- `Params` gains `Mix`, so the baseline records it and the existing stale guard catches a
  mismatch with no new code path.
- `Results` gains `Lambda`. `Recommend` scores nDCG minus the store cost, keeping the tie rule.
- `linkThreshold` and `propagateThreshold` become 0.925 (raw cosine 0.85). The twin band moves up
  so twins still auto-link, which retunes `sigmaTwin`.

No new package, no new port, no new interface.

## Key Decisions

- Franco's: scope, order, store weight, explicit-only supersession, generator sets it on chains.
- My calls, for review: lambda = 0.10; agent mix 35/20/30/5/10; printing the break-even lambda;
  the bounded additive tiebreak; both thresholds at 0.925; the dedup bypass; `-mix` running one
  mix per invocation; arm A staying the store baseline.

## Open Questions

For Franco at the visual-plan review:

1. PRD section 9 lists `memory_stats` and the demo recording under M6; the launch scope does not.
   This change leaves them out, and v1 is not done without the demo. Confirm a follow-up change.
2. `ENGRAM_DECAY` stays `type-aware` by default in this change whatever the table recommends. The
   README says choosing the default is an M6 decision, and it stays Franco's after the numbers.
