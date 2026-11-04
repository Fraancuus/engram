# Retrospective: m6-polish

## Wins

- Lambda, the mix and the predictions were committed before any M6 run, and none moved afterwards. When the first numbers surprised, the response was a diagnosis, not a retune.
- Independent diagnoses found that type-aware's 1.000 on facts comes from a blend discontinuity. Without that, the README would have claimed a retrieval benefit that does not exist.
- Whole-branch review found two data-loss paths on the new supersession code that per-task review passed: a correction lost near a soft-forgotten memory, and a correction lost near a different live memory. Both were reproduced end to end, the second on the real embedder.

## Misses

- Design wording failed twice on the same class. D12 applied supersession on the dedup path without asking what happens to the write's own text. It took two review rounds to reach "dedup only into identical content".
- Predictions were poor: 5 of 10 held outright. The claim that no class would favour forgetting was wrong as run, and the break-even estimate of 0.16 came in at 0.096.
- My briefs carried errors the reviewers caught: an unscoped claim under the wrong table, "link bridges" understating a cost, and a sweep sentence that lost its predicate.
- `git switch -c` from `origin/main` set the branch to track `main`, and `98012a9` reached `main` with no PR by a push this session did not run.
- A shared scratch path let one refute agent delete another's working copy. Briefs now require `mktemp -d`.

## Plan deviations

- No worktree: Franco asked to watch in VS Code, so apply ran in the main checkout, and finalize takes the manual path.
- Task 9 grew from one review into a fix wave (9a to 9d), and the final review added 13a to 13c. The spec gained scenarios for each ruling.
- Generator rule added: a bridge label cannot be a memory with a successor. It changes draws for a seed, so it landed before the re-baseline.
- `-rerank cosine` stayed out as a table, as scoped, but cosine runs became evidence for the claim.

## Skill and workflow compliance

- Followed: TDD per task with RED evidence, explicit model on every dispatch, opus task reviews, fable for architect and security-auditor, archive on the branch before saying ready.
- Departed: three one-line README fixes after 13c were applied directly rather than through another review round, because the text was exact and the reviewer had supplied it.
- Lefthook is not on PATH on this machine, so local pre-commit gates did not run. Implementers ran gofmt, vet and lint by hand, and CI runs gitleaks.

## Surprises

- Forgetting moved `none`'s score more than its own: the fact gap is a seed-cap effect where a smaller store happens to avoid an inversion.
- At lambda 0.10 three different recommendations are reachable from one codebase: `uniform` on seed 42, `type-aware` on nine other seeds, `none` with a reranker stage.
- Entity bridges surface nothing in any arm, mix or rerank mode. The class measures only the hop node.

## Promote candidates

- Whole-branch review with skeptics paid for itself. Keep it for changes that touch the write path.
- Preregister eval parameters in the design and commit them before the first run.
- Give every parallel agent its own scratch directory in the brief.
- Cut branches with `--no-track`.
