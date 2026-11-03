# M6 polish: eval correction design

> Status: drafted 2026-11-03, before any M6 eval run. Inputs: the vault note `M5 eval - results
> and correction plan` and `openspec/changes/archive/2026-11-03-m5-eval/design.md` section
> "Result at the floor". Decision numbers continue from M5 (D1 to D10) to keep references unique.

## Context

The M5 floor run (seed 42, k=5, rerank none, dim 128):

| arm | nDCG@5 | precision@5 | store | topic-current | fact | episode-recent | episode-old | bridge-link | bridge-entity |
|---|---|---|---|---|---|---|---|---|---|
| none | 0.901 | 0.225 | 5000 | 1.000 | 0.673 | 1.000 | 0.990 | 1.000 | 0.613 |
| uniform | 0.668 | 0.176 | 4016 | 0.864 | 0.648 | 1.000 | 0.243 | 0.688 | 0.258 |
| type-aware | 0.514 | 0.127 | 1199 | 1.000 | 0.722 | 0.661 | 0.000 | 0.169 | 0.017 |

Episode access hit rate under type-aware was 0.412 against 0.989 under none.

**Ports consumed:** `Embedder`, `Reranker`, `DecayModel`, `Clock` from `engram.go`, unchanged.
**Implementers:** `neo4j` and `mock` satisfy `mcp.Store`; `inference` is untouched. This change
adds no package, port or interface, and changes no signature on `mcp.Store`.

## Goals / Non-Goals

**Goals:**
- Stop forgetting from penalising live memories, and make stale facts clear.
- Put the mix and the store cost in plain view, so the recommendation says what it rests on.
- Re-baseline, and let the README say only what the numbers support.

**Non-Goals:**
- Making type-aware win. A null result is a credible finding (PRD section 7).
- `memory_stats`, the demo, and changing the `ENGRAM_DECAY` default (see proposal).
- Tuning constants against the outcome. See D16.

## Decisions

### D11. Retrievability: pre-pool filter plus a bounded tiebreak

`defaultWRet` becomes 0.01. Score = 1.0 sim + 0.3 importance + 0.01 R. `R` is in [0, 1], so it can
reorder two memories only when their other terms are already within 0.01. That is the note's "R
only breaks ties within 0.01", kept as a plain scalar so the sort stays a total order.

Rejected: a "within 0.01" comparator, because it is not transitive and breaks `sort.Slice`. Also
rejected: a bucketed lexicographic key, because it adds code and an edge artefact for the same
guarantee. Under `NullDecay` R is constant, so arm A's order equals sim + 0.3 importance exactly.
That property gets a test.

The tiebreak ranks by blend score. In production the cross-encoder remains the final authority on
order; it receives a better pool, and `rerankResults` still sorts stably.

**Filter before truncation.** Today `blend` cuts to the pool size, then `scoreAndFilter` drops dead
candidates, so dead memories take slots that live ones need. `blend` loses its `k` parameter and
truncation moves after `scoreAndFilter` in `doRecall`, which is a deletion plus one slice. Seeds
stay capped at `seedN` (50) before the filter: R cannot run inside the vector index, so a topic
full of dead memories can still crowd the seeds until the sweep prunes them. That limit is accepted.

`include_forgotten` keeps its meaning: nothing is filtered, and dead memories rank on relevance.

**A/B/C:** A's order is unchanged except where the pool cut moved. B and C stop losing fresh
episodes to permanent standards. **Gate:** arm C's precision rises, so the gate is re-baselined.

### D12. Semantic supersession, explicit only

`remember` applies `Supersedes` when the type is procedural or semantic. An episodic write keeps
today's behaviour: the field is ignored and `superseded` is absent from the reply. The decay models
already handle a superseded semantic memory (S = 2 d), so `decay.go` does not change.

**Dedup bypass (new finding).** Dedup returns before supersession runs, so a correction that embeds
within raw cosine 0.90 of the old fact is discarded and the replace never happens. Dedup now skips
a candidate that is already superseded or is named in `in.Supersedes`, and the first remaining
candidate decides. Skipping an already-superseded candidate also stops a fact flipping back to an
old value from reinforcing a memory recall will never return. Supersession also applies when the
write dedups into a different live memory, because the caller still named what it replaces; that also
makes a retried correction idempotent after a failed supersede. An episodic write's `Supersedes` is
treated as absent throughout, so it cannot bypass dedup. Search returns at most `autoLinkK` (5)
candidates, so five skipped candidates can hide a live duplicate at rank 6; this is accepted and noted
in a `shortcut:` comment. The new memory still auto-links to a skipped candidate, which is harmless:
recall filters it and propagation skips it.

**Stop refreshing superseded versions.** `PropagateReinforce` skips superseded targets in both
stores, and a skipped target does not consume the cap of 20. Neo4j gets one `WHERE` predicate on a
property it already writes, so no new parameter and no string-built Cypher. Rejected: lowering the
chain-link weight (brainstorm, Alternative C).

**Generator.** Each fact version after the first carries `Supersedes` = its predecessor, on every
chain. A pruned predecessor is already a no-op in both stores. D10 still holds: supersession runs
in every arm.

**A/B/C:** this removes A's last handicap. Stale versions are excluded in all arms, so `none` and
`type-aware` converge near 1.0 on facts. Uniform stays lower because its latest versions can prune.
After this, C cannot beat A on nDCG in any class. **Gate:** arm C's precision on facts rises.
Superseded standards also prune faster under B and C, because they no longer get refreshed.

### D13. Query mix as a named preset

`Params` gains `Mix string`. Presets (percent topic-current / fact / episode-recent / episode-old /
bridge, with bridge split evenly between link and entity):

| preset | topic-current | fact | episode-recent | episode-old | bridge |
|---|---|---|---|---|---|
| `agent` (primary, default) | 35 | 20 | 30 | 5 | 10 |
| `stress` (the M5 mix) | 25 | 15 | 20 | 15 | 25 |

`agent` is an assumption, not a measurement: no real agent traces exist yet. An agent mostly asks
for the current way of doing something and for what just happened, sometimes for a fact, and rarely
for old chatter or a multi-hop association. `stress` keeps the per-class cost of forgetting visible.

`Floor()` sets `Mix: "agent"`, so `Params` still compares with `!=` and the existing guard treats a
mismatch as stale with no new code. `Validate` rejects an unknown mix. `buildQueries` runs last in
`generateUnchecked`, so every mix sees the same corpus for a seed, and a test pins that.

`-mix` selects one mix per invocation. Each mix is a separate simulation, because queries reinforce
what they return. The README shows both tables from two invocations (brainstorm, Alternative D).

**A/B/C:** the mix can change the order of the arms, which is why both tables ship. **Gate:** it
runs the baseline's mix, and `--update-baseline` refuses anything but the primary mix.

### D14. Recommendation score

```
score(arm) = nDCG(arm) - lambda * store(arm) / store(none)
```

`Lambda` defaults to `DefaultLambda = 0.10`, read as: holding a store as large as never-forgetting
costs 0.10 nDCG. `-lambda` overrides it; a negative, NaN or infinite value exits 2. `store(none)` is
the mean final size of arm `none` in the same run, or the largest arm present when a partial result
has no `none`. The tie rule stays: within 0.005 of the top score, the smallest store wins. `Recommend`
with `Lambda` 0 reduces to the M5 rule, so `-lambda 0` shows the retrieval-only answer.

**Why 0.10.** Store cost is a value judgement. The in-memory fake has flat latency, so nothing here
measures it. 0.10 is a round figure that treats a 10x larger store as worth about 0.09 nDCG. It is
written here before any M6 run and will not be changed to reach a result.

**Break-even line.** A rough estimate from the M5
per-class numbers on the agent mix puts `none` near 0.98 and `type-aware` near 0.87 at about 30% of
the store, a break-even near 0.16. That is close enough to any round lambda that the constant alone
could look like the verdict. The report therefore prints the lambda at which each decaying arm ties
`none`: (nDCG(none) - nDCG(arm)) / (1 - store(arm) / store(none)). It costs one function and one line.

Report shape:

```
rerank: none, mix: agent, lambda: 0.1
arm  ...  ndcg@5  ...  store  score  ...
...
break-even lambda vs none: uniform <x>, type-aware <y>
recommended: <arm> (ENGRAM_DECAY=<arm>) on mix agent, lambda 0.1
```

On that estimate lambda 0.10 still recommends `none`, and a lambda above about 0.16 would recommend
`type-aware`. So the value is the decision: Franco confirmed 0.10 at the plan review on 2026-11-03,
before the first run and with that estimate in front of him.

**A/B/C:** this can change the recommendation without changing any retrieval number, and the report
says so openly. **Gate:** none. The gate compares precision only.

### D15. Thresholds on the normalised scale

The index returns (1 + cos) / 2. Mapping a raw-cosine intent c to the scale is (1 + c) / 2.

| constant | M5 | raw cosine | M6 | raw cosine |
|---|---|---|---|---|
| `defaultDedupThreshold` | 0.95 | 0.90 | 0.95 | 0.90 |
| `linkThreshold` | 0.85 | 0.70 | 0.925 | 0.85 |
| `propagateThreshold` | 0.85 | 0.70 | 0.925 | 0.85 |

M4 set `propagateThreshold` equal to `linkThreshold`, so every auto-link counted as a strong edge.
M6 keeps that relation. Explicit links stay at weight 1.0 and always propagate. Existing edges keep
their stored weights: those below 0.925 still expand and no longer propagate, and no migration runs.

**Generator consequence.** The auto-link point moves to raw 0.85, while twins and chain versions sit
in [0.74, 0.88]. Without a change, twins stop auto-linking. Twin pairs get their own band
`[minTwinCos, maxTwinCos]` = [0.86, 0.89], which is above the link point and below the dedup point.
`sigmaTwin` drops to roughly 0.95 (estimate: the pair then lands near 0.875), tuned against the band
test only. Chain versions keep [0.74, 0.88]; they have explicit links, and those above 0.85 also
auto-link, which is harmless. D3b's comment in `eval/vectors.go` is rewritten.

**A/B/C:** fewer edges in every arm (1,048 in M5), so less propagation. Expansion reach through
auto-links drops, while explicit links are untouched. **Gate:** part of the re-baseline.

### D16. Preregistration and re-baseline

This design is committed before any M6 eval run, so its git history shows that lambda and the mix
were not chosen from results. After apply: `go run ./cmd/eval --update-baseline -seed 42` on amd64,
then `--ci` twice with a byte-identical check on the arm C numbers, then a `-mix stress` run. The
tables go into this file under "Result at the floor". If a number contradicts a prediction below,
the prediction stays and the result is reported next to it.

Predictions for seed 42, k=5, rerank none:

| metric | M5 | predicted |
|---|---|---|
| type-aware episode-recent nDCG | 0.661 | near 1.0 |
| type-aware episode access hit rate | 0.412 | near 0.99 |
| none fact nDCG | 0.673 | near 1.0 |
| type-aware fact nDCG | 0.722 | near 1.0, tied with none |
| uniform fact nDCG | 0.648 | below none and type-aware |
| none episode-old, stress mix | 0.990 | unchanged within 0.02 |
| type-aware episode-old | 0.000 | still 0.000 |
| type-aware store | 1199 | higher: reinforcement reaches episodes, offset by pruned versions |
| retrieval-only winner (`-lambda 0`), both mixes | none | none |
| recommendation at lambda 0.10, agent mix | none (nDCG only) | none, break-even near 0.16 |

### D17. README claim

Rewritten after the tables exist, using the sentence the numbers back. The likely shape, to be
confirmed by the run: type-aware forgetting does not retrieve better than never forgetting; it holds
retrieval close on what an agent mostly asks, protects procedural memory that uniform decay loses,
and keeps a much smaller store, at a known cost on old episodes and bridges. The headline at README
line 5, "proving the forgetting actually helps", goes. The README also gets both tables, lambda and
the break-even, the corrected recall description, and the archived path of the M5 design.

### D18. Rulings from the mid-branch review

A five-lens review of the Go diff, with three skeptics trying to refute each serious finding, confirmed
twelve. Each ruling below changed code or a spec, except where marked as left open. None moves lambda, a
mix percentage or a D16 prediction, and no eval number informed any of them.

**Fixed.** A correction near a soft-forgotten memory dedups into it, and because M6 applies supersession on
the dedup path, the old fact is superseded while the correction is lost: recall went from one stale result
to none. Dedup now also skips soft-forgotten candidates. A write naming `Supersedes` dedups only into its
own type, because a procedural correction absorbed by an episode would otherwise lose the standard and its
replacement to the sweep; without `Supersedes`, cross-type dedup stays as it was. A bridge label can no
longer be a memory with a successor: M6 gave fact versions `Supersedes`, so such a label capped every arm,
including `none`, below 1.0 on bridge-link. Rejecting those candidates changes random draws for a seed, so
it lands before the re-baseline. The remainder are tests pinning behaviour D14 calls the decision
(`DefaultLambda`, the score column at a non-zero lambda, the `-mix` and `-lambda` plumbing behind the README
tables), the sweep pruning a superseded fact, labels surviving a pruned hop, and literal twin bounds.

**Reworded.** `superseded` reports the ids `remember` asked the store to supersede. Reporting only matched
ids needs a `Supersede` signature change, which this change rules out. The tool description now says what an
absent `superseded` means.

**Left open, for Franco.** A typo in `supersedes` is reported as success, and the fix is a store signature
change. CI has no job running the integration tests, and those tests skip silently without Neo4j, so Task 10
fails on any skip by hand. `supersedes` can reach another namespace, as `forget` already can. A retried
correction that dedups drops its `Links`, as dedup always has.

## Risks / Trade-offs

- [Lambda or the mix reads as chosen to flatter C] → both fixed here before any run; break-even and the stress table printed; `-lambda 0` documented.
- [A 0.01 additive term is not literally a tiebreak] → it is bounded to the same 0.01; a table test pins both sides of the bound. The architect is asked about this directly.
- [Fewer auto-links weaken real recall] → raw 0.85 is the value the note specifies; explicit links are unaffected; M6 reports edge counts.
- [Dedup bypass lets a wrong `Supersedes` insert near-duplicates] → the caller named that memory explicitly, and a duplicate is cheaper than a lost correction.
- [Retuned `sigmaTwin` moves the frozen geometry] → only twin pairs move; tuned to a band test with no eval output read; ordinary and chain bands unchanged and still asserted.
- [Gate is red mid-branch] → expected from the first ranking change until the re-baseline task; implementers run package tests, not `--ci`.
- [Linux reproducibility of the new baseline] → the PR's CI eval job on ubuntu is the proof, as it was for M5.

## Migration Plan

No data migration. `go run ./cmd/eval --ci` keeps its CI line and starts from the new baseline in
the same PR. Rollback is reverting the PR.

## Open Questions

`ENGRAM_DECAY` default stays `type-aware` in this change. That decision is open for Franco, now that the
tables exist. The blend discontinuity under "Why type-aware beats none on facts" needs a design decision in
a follow-up change.

Decided by Franco at the plan review (2026-11-03, before any M6 run): lambda 0.10; agent mix
35/20/30/5/10; `Supersedes` on every fact chain; print the break-even lambda; `memory_stats` and the
demo recording go to a follow-up change after this PR.

Resolved by the architect agent (fable, 2026-11-03): the bounded additive term stands with no
misbehaving input found; removing `k` from `blend` is the right cut, and the cut must precede
`rerankResults`; the `DecayModel` contract and clock injection are untouched; nothing makes the arms
differ by anything but `DecayModel`. One required change, folded into D12: an episodic write's
`Supersedes` must not bypass dedup.

## Result at the floor (2026-11-04)

Produced at `0466233` on amd64: `go run ./cmd/eval --update-baseline -seed 42`, then `--ci` twice with byte-identical arm C rows. Raw output is in `results/`. Baseline: mix agent, k 5, seed 42, rerank none, precision@5 0.1788, nDCG@5 0.8747.

Agent mix, seed 42, k=5, rerank none, lambda 0.1:

| arm | nDCG@5 | precision@5 | store | edges | score | topic-current | fact | episode-recent | episode-old | bridge-link | bridge-entity |
|---|---|---|---|---|---|---|---|---|---|---|---|
| none | 0.949 | 0.210 | 5000 | 955 | 0.849 | 1.000 | 0.852 | 0.998 | 0.970 | 1.000 | 0.613 |
| uniform | 0.937 | 0.204 | 3897 | 457 | 0.859 | 0.989 | 0.900 | 0.998 | 0.800 | 0.865 | 0.564 |
| type-aware | 0.875 | 0.179 | 1146 | 112 | 0.852 | 1.000 | 1.000 | 1.000 | 0.000 | 0.347 | 0.147 |

Break-even lambda against `none`: uniform 0.055, type-aware 0.096. Recommended: `uniform`.

Stress mix (the M5 mix), seed 42, lambda 0.1:

| arm | nDCG@5 | precision@5 | store | edges | score | topic-current | fact | episode-recent | episode-old | bridge-link | bridge-entity |
|---|---|---|---|---|---|---|---|---|---|---|---|
| none | 0.919 | 0.225 | 5000 | 955 | 0.819 | 1.000 | 0.813 | 0.989 | 0.990 | 1.000 | 0.606 |
| uniform | 0.851 | 0.203 | 3876 | 456 | 0.773 | 0.984 | 0.862 | 0.989 | 0.737 | 0.822 | 0.518 |
| type-aware | 0.671 | 0.144 | 1180 | 114 | 0.647 | 1.000 | 1.000 | 0.993 | 0.067 | 0.294 | 0.204 |

Break-even lambda against `none`: uniform 0.301, type-aware 0.325. Recommended: `none`.

Episode access hit rate at seed 42: none 0.989, uniform 0.991, type-aware 0.995. With `-lambda 0`, `none` is recommended on both mixes.

Agent mix across seeds 42 to 51, one run each at lambda 0.1:

| seed | none nDCG, score | uniform nDCG, score | type-aware nDCG, score | recommended |
|---|---|---|---|---|
| 42 | 0.949, 0.849 | 0.937, 0.859 | 0.875, 0.852 | uniform |
| 43 | 0.944, 0.844 | 0.914, 0.837 | 0.875, 0.853 | type-aware |
| 44 | 0.941, 0.841 | 0.919, 0.842 | 0.880, 0.857 | type-aware |
| 45 | 0.949, 0.849 | 0.926, 0.848 | 0.876, 0.853 | type-aware |
| 46 | 0.931, 0.831 | 0.917, 0.841 | 0.869, 0.846 | type-aware |
| 47 | 0.945, 0.845 | 0.931, 0.854 | 0.874, 0.850 | type-aware |
| 48 | 0.946, 0.846 | 0.922, 0.844 | 0.875, 0.852 | type-aware |
| 49 | 0.947, 0.847 | 0.922, 0.844 | 0.875, 0.851 | type-aware |
| 50 | 0.944, 0.844 | 0.926, 0.849 | 0.879, 0.857 | type-aware |
| 51 | 0.945, 0.845 | 0.919, 0.842 | 0.873, 0.850 | type-aware |

Secondary, `-rerank cosine`, agent mix, seed 42: nDCG none 0.961, uniform 0.934; fact 1.000 in every arm; break-even uniform 0.136, type-aware 0.121; recommended `none`. The cosine reranker is a best case, not the production cross-encoder (D3a).

### Predictions against results

The D16 predictions stand as written.

| metric | M5 | predicted | result (seed 42) | held |
|---|---|---|---|---|
| type-aware episode-recent nDCG | 0.661 | near 1.0 | 1.000 agent, 0.993 stress | yes |
| type-aware episode access hit rate | 0.412 | near 0.99 | 0.995 | yes |
| none fact nDCG | 0.673 | near 1.0 | 0.852 agent, 0.813 stress | no |
| type-aware fact nDCG | 0.722 | near 1.0, tied with none | 1.000, not tied | half |
| uniform fact nDCG | 0.648 | below none and type-aware | 0.900, above none | half |
| none episode-old, stress mix | 0.990 | unchanged within 0.02 | 0.990 | yes |
| type-aware episode-old | 0.000 | still 0.000 | 0.000 agent, 0.067 stress | half |
| type-aware store | 1199 | higher | 1146 agent, 1180 stress | no |
| retrieval-only winner, both mixes | none | none | none | yes |
| recommendation at lambda 0.10, agent mix | none (nDCG only) | none, break-even near 0.16 | uniform, break-even 0.096 | no |

### Why type-aware beats none on facts

It does not retrieve facts better. Three independent diagnoses agree, and a replay reproduces the fact nDCG in every arm on seeds 42, 43, 44 and on the stress mix. Neighbors excludes seeds, so a memory inside the 50 vector seeds keeps its own similarity, while the same memory just outside inherits `sim_source * weight` through a link. Every generated fact has an explicit weight 1.0 link to its topic's standard, which has the higher importance. So a standard scoring about 1.00 as a seed scores about 1.165 just outside the seeds and outranks the fact: query q0153 scores 1.0044 and 1.1651 for the same standard. `none` keeps about 92 live memories per topic against the cap of 50, so its standard is often outside; `type-aware` keeps about 23, so it never is. Every miss is the target at rank 2 behind that standard, and recall@5 on facts is 1.000 in all arms. The inversion reproduced on a real Neo4j on the `none` arm at seed 42: 41 misses against the mock's 40.

Consequences:
- D12's "C cannot beat A on nDCG in any class" holds in substance but not as run: without this discontinuity none reaches 1.000 on facts. The reason D12 gives for uniform is wrong: uniform's latest versions were pruned in none of its fact queries, and its gap is the same inversion.
- With links from facts to standards removed, or with a reranker stage, `none` reaches fact 1.000 and is recommended on seeds 42 to 44. So lambda 0.10 sits between a result that recommends `type-aware` and one that recommends `none`, and the discontinuity decides which. The real cross-encoder could not be run: eval content is generator ids, not text.
- Repair needs a design decision. Scoring seeds through links as well makes every arm far worse (fact 0.708 in `none` and `type-aware`). It is M2 blend behaviour and out of scope here, so no constant was changed after seeing these numbers.

### What the numbers support

Type-aware forgetting does not retrieve better than never forgetting. On the agent mix it gives up about 0.07 nDCG for a store at 23% of the size, and on the stress mix about 0.25. The cost falls on old episodes and bridges, and the gain is store size with procedural memory protected. On the agent mix at lambda 0.10 the three arms score within about 0.02. On the stress mix the arms spread wider and `none` is recommended. On the agent mix the winner depends on the seed and on a blend discontinuity, so the table recommends no arm with confidence. `ENGRAM_DECAY` stays `type-aware`, and that choice stays open for Franco.
