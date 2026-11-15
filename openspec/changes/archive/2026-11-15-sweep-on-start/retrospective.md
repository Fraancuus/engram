# Retrospective: sweep-on-start

Evidence first. Dates are 2026-11-15 unless stated.

## Wins

- **The plan was dry-run before review.** An agent executed Tasks 1 to 3 of plan.md in a scratch copy and reported five defects: a test "function" that was a helper, a grep that matched a pre-existing line, a missing import, a wrong session variable, a wrong line reference. They were fixed before apply. During apply no task review raised a Critical or Important finding and no fix round ran inside a task.
- **Probes replaced two guesses with facts.** The design said the loser of a concurrent delete gets `ErrNotFound`. A probe on the live Neo4j showed both deletes succeed, in 400 of 400 rounds. A second probe showed the fixture edits keep every test passing and leave no sweepable row.
- **Review before design freeze changed the design.** The skeptic found that the list of tests a real sweep breaks was wrong: the `sweep` fixture is immune, and every `mcp` integration test was exposed. The scope reviewer found an alternative nobody had weighed (a shorter interval, no code).
- **The eval was compared with `main`, not only with the baseline file.** The baseline holds two figures. The full outputs were diffed, which is how the latency columns were found to be noise.

## Misses

- **A concurrency claim was written as fact with no evidence.** It reached the first visual plan before a reviewer called it unproven.
- **One decision was recorded from an ambiguous answer.** "One daily sweep" was first written into design.md as a 24-hour interval per process, then withdrawn a revision later when Franco chose a separate change for the once-a-day gate.
- **My own integration runs broke `TestStoreSearch`.** Test rows with one identical embedding had piled up to 512, and the runs made during planning tipped the test over. It was found by chance, in a probe report.
- **The fixture rule in plan.md was wrong twice.** It said "live procedural" (true only under type-aware decay) and "younger than 30 days" (the grace check uses `last_accessed`). A task reviewer and the final reviewer each caught one part.

## Plan deviations

See apply.md. In short: the `CONTRIBUTING.md` rule and three doc sentences were reworded after the whole-branch review, and one test gained an assertion that pins where the ticker starts.

## Skill and workflow compliance

- No git worktree: Franco asked for the main checkout.
- The subagent-driven loop ran through Workflow scripts, not single Agent calls, because a session hook rewrites Agent calls and each needed two attempts. A consequence: a fix round would have used a fresh agent, not the resumed implementer. None was needed.
- `finishing-a-development-branch` was not invoked. CLAUDE.md fixes the outcome: push, one PR, Franco merges.
- Model tiers held: Sonnet implemented, Opus reviewed, Haiku re-reviewed the fix wave, Fable ran `security-auditor`. `architect` was not needed here; it ran for the follow-up change. The session model was Fable, where CLAUDE.md names Opus for the execution loop.
- The local bridge that served the visual plan stopped at its two-hour limit, after the plan was approved.

## Surprises

- Neo4j evaluates `WHERE` before it takes the write lock. Two overlapping `DETACH DELETE`s of one node both report success, and a compare-and-set needs a dummy write first.
- The eval prints wall-clock latency (p50, p95) beside its deterministic figures, so its output is never byte-identical between runs.
- `Search` takes the 50 nearest rows store-wide and filters by namespace afterwards. With enough identical vectors it returns nothing for a namespace that has a perfect match.
- The security pass found that a sweep delete does not re-check the row. That is old code, but this change is what makes the window reachable in every session.

## Promote candidates

Listed for Franco in the PR body, not applied here.

1. **CLAUDE.md, Verification.** The integration command should exclude the eval body with `go list ./... | grep -v 'engram/eval$'`. The shorter `'/eval$'` also drops `cmd/eval`.
2. **`openspec/config.yaml`, design rule.** A claim about how the database behaves under concurrency is probed against the real stack before it is written into design.md.
3. **`openspec/config.yaml`, verify rule.** "The eval does not move" is checked by diffing the full output against `main`'s with the p50 and p95 columns removed, not against the two figures in `eval/baseline.json`.
4. **CLAUDE.md, Verification.** Integration tests leave rows in the dev store. Say so, and say that `TestStoreSearch` fails once enough of them share one embedding.
5. **Follow-up changes**, in this order: a sweep delete that re-checks the row; `sweep-daily-claim`; the candidate cap and its `content` projection.
