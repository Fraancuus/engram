# Retrospective: m5-eval

Evidence first, opinion second. Written at finalize, 2026-11-03.

## Wins

- The instrument works and is honest. Same handlers and defaults in all three arms, only the `DecayModel` swapped; labels come from the generator; the `--ci` gate is deterministic (run twice, identical output) and refuses stale baselines.
- It found three real product defects before any user did: the 0.5 retrievability weight in the blend, semantic versions that never clear, and dedup/auto-link thresholds set on the raw-cosine scale while Neo4j returns `(1+cos)/2`. The last one was verified against Neo4j 5.26 in Docker, not assumed.
- Subagent-driven development with per-task opus reviews held the line: 25 tasks, every fix loop closed, gates green at every commit on the branch.

## Misses

- The first floor run was unreadable for the thesis because the reranker nullified graph expansion and k=5 accesses kept everything alive. Two design rulings (D3a) were needed after code existed. Those belonged in brainstorm.
- The query mix was fixed at design time and never questioned until the result came in. The aggregate recommendation is a property of that mix, which design.md now says but the proposal did not anticipate.
- Entity floor 200 was impossible under the fan-out cap; cross-topic bounds were unreachable with random centroids. Both are arithmetic that a ten-minute check at design time would have caught.

## Plan deviations

- Rerank off by default and `-rerank cosine` as an opt-in comparison (spec said shipped defaults; design D3a records why).
- Accesses at k=1, not k=5.
- Semantic fact chains with implicit staleness added mid-apply (ruling 04e9bf9) because write-once facts gave the never-forgetting arm no distractor.
- Floor dim 64 to 128 after cross-topic noise leaked at 64.
- Entity floor 200 to 1,200.

## Skill/workflow compliance

- superspec: all nine artifacts produced in order; design rulings recorded with commit hashes.
- TDD: tests first on every task; two tests (`TestArmBPrunesStaleStandard`, dedup bound) were caught passing by seed luck and rewritten to derive expectations from the model.
- Model tiers: rule written mid-change after an architect dispatch inherited Fable; followed afterwards.
- Visual plan: produced, reviewed, decisions fed back into design.md and plan.md.

## Surprises

- Neo4j's cosine index score is normalised, so production thresholds mean something different from what the PRD says. Not an eval bug; a production constant to revisit.
- Weight-1.0 version-chain links sit above `propagateThreshold`, so recalling a current fact refreshes the stale one. Decay cannot clear what propagation keeps alive.
- Under type-aware, accessing a fresh episode reinforces the in-topic permanent standard 59% of the time. The blend turns forgetting into a ranking penalty on live memories.

## Promote candidates

- "Do the geometry arithmetic before the floor is frozen" as a design-artifact rule in `openspec/config.yaml`.
- Query mix as a named, reported parameter of any eval (goes into M6).
- The model-tier rule and the LF-only brief for implementers are already in `CLAUDE.md`.
