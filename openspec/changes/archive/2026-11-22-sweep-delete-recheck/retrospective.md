# Retrospective: sweep-delete-recheck

Evidence first. Dates are 2026-11-21 and 2026-11-22.

## Wins

- **The plan was dry-run twice before review, against the live stack.** The first run followed Tasks 1 to 4 in a scratch copy. It found one step that did not compile and a race test that took 14 to 25 s where about 2 s was expected. That timing led to a probe, and the probe found the cause: the first draft of `Reinforce` read `access_count` before its first write and deadlocked against the lock-first delete on 6 to 12 of 60 calls, at about 2.1 s per retried call. The second run followed the revised plan and every step behaved as written. During apply no task review raised a Critical or Important finding.
- **The guard was proven by breaking the code.** The race test was run against a delete without its dummy write, and against one that computes the predicate before the dummy write. It failed in three runs of three for each form, while the sequential test passed on the same broken delete. The check ran again in Task 3 and again after the fix round changed the test.
- **A claim about `main` was measured, not argued.** design.md said a `Link` whose target is deleted mid-statement already fails on `main`. A probe ran the same 100 overlapping rounds against both deletes: 65 and 66 failures with the old one, 62 and 58 with the new one.
- **Two read-only critics ran before the plan went to review.** They found nine places where the documents disagreed and three scenarios with a weak proof. One was a spec sentence, "every A/B/C figure equals the baseline exactly", that no command could check, because `eval/baseline.json` holds two figures.
- **The eval was compared with `main` in full, twice.** After Task 2 and on the final tree. Both outputs are equal without the two latency columns.

## Misses

- **The first design carried a statement that deadlocks.** Nobody saw it by reading. Only the dry run's wall time showed it.
- **"The same happens on `main` today" was written into design.md before it was measured.** It was hedged in chat and stated as fact in the file. The probe that followed confirmed it.
- **The race test's comment claimed more than the test proves.** It said a run in which the calls never overlapped cannot pass. Rounds that alternate without overlapping also give both end states. The plan wrote the sentence, the task review passed it, and two lenses of the whole-branch review caught it.
- **One new test could not fail for the line it was written for.** The recall test logged through a handler at Info, so the new debug record was never checked. The task review filed it as minor; the fix round made the test fail without the log line.
- **One verification command of mine was wrong.** A `git rev-parse` call with two revisions failed and cut the chain short, so the first eval comparison ran only `main`'s side. Reading the output showed it; the branch side was then run.
- **Existing integration tests still leave rows in the dev store.** 65 during Task 3 and 77 in the final run. They were deleted by exact id each time. The matching lesson from the last retrospective is still not applied.

## Plan deviations

See apply.md. In short: the fix round after the whole-branch review changed the race test's comment, failure message and hang guard, added one debug log line to `remember`, and strengthened four tests. plan.md is left as the record of what was planned; design.md has the final wording.

## Skill and workflow compliance

- No git worktree: Franco asked for the main checkout for this series of changes.
- The subagent-driven loop ran through Workflow scripts, as in the last change.
- Model tiers held: Sonnet implemented, Haiku wrote the docs task and re-reviewed the fix round, Opus reviewed each task and the branch, Fable ran `security-auditor`. `architect` and `scoping-ux` ruled at design time. The session model was Fable, where CLAUDE.md names Opus for the execution loop.
- The Haiku implementer of the docs task did not write its report file. The task reviewer checked the deliverable directly.
- `finishing-a-development-branch` was not invoked. CLAUDE.md fixes the outcome: push, one PR, Franco merges.
- The local bridge that served the visual plan stopped at its two-hour limit, after the plan was approved.

## Surprises

- A statement that reads a property of a row before its first write to that row can deadlock against a statement that locks the row first. The driver retries, so nothing fails; the call just takes two seconds.
- A race test whose rounds alternate who starts first ends in both states even when the two calls never overlap. Only a failing run against broken code shows overlap.
- `Link` fails with `EntityNotFound` in about six of ten overlapping rounds when its target is deleted mid-statement, on `main` as well.
- A pinned row can be gone: a hard `forget` from another session deletes it.
- `engramd` has no way to show a debug record. It logs at Info and has no switch.

## Promote candidates

Listed for Franco in the PR body, not applied here.

1. **`openspec/config.yaml`, plan and verify rules.** A test that is named as the guard of a concurrency property is run once against the broken code and must fail, and the record says so. A passing race test shows that both orders happened, not that the calls overlapped.
2. **`docs/engram-go-rules.md`, section 6.** A Cypher statement that must hold a row's lock writes to the row before it reads it. Say why (a lost update, and a deadlock against any other lock-first statement) and cite the Neo4j manual section.
3. **`openspec/config.yaml`, plan rule.** A plan with literal code is dry-run in a scratch copy, against the live stack where it has integration steps, before the visual plan. It has paid for itself in two changes running.
4. **CLAUDE.md, Verification** (carried over from the last retrospective). Integration tests leave rows in the dev store, 65 to 77 per full run. Either the tests clean up, or the doc says to record the ids before a run and delete the added ones by exact id after it.
5. **Follow-up changes**, in this order: `sweep-daily-claim`; the `Link` race (a lock-first `Link` or a best-effort link step in `remember`); a log-level switch for `engramd`; the candidate cap of `PruneCandidates` and its `content` projection.
