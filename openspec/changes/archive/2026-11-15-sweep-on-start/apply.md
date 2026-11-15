# Apply Receipt

**Change**: `sweep-on-start`
**Iteration**: `1`
**Applied at**: `2026-11-15 20:35`
**Executor**: `subagent-driven-development`

---

## Workspace

- **Worktree**: none. The main checkout, because Franco asked for it: "Work in the main checkout, no worktree."
- **Branch**: `feat/sweep-on-start`

---

## Commits

- **Range**: `971c76d..1299620`
- **Count**: `7` (six tasks and one fix wave after the whole-branch review)

---

## Tasks

- **Completed**: `21 of 21` checkboxes in tasks.md flipped to `- [x]`
- **Remaining**: none

---

## How it was run

Sonnet implemented each task from its brief, test first. Opus reviewed each task for spec compliance and quality. No task review found a Critical or Important issue, so no fix round ran inside a task.

The whole-branch review ran on Opus (`reviewer` on the Go and docs diff, `scoping-ux` on the tool wording). It found nothing Critical or Important. One fix wave followed, checked by a scoped re-review:

- `TestRunFirstPassAfterFirstDelay` now asserts one pass a nanosecond before the second is due. The new assertion was shown to fail when the ticker is created before the first pass.
- The `SweepOnce` comment says a row that is already gone is "skipped and not logged".
- `CONTRIBUTING.md`: fixtures must be pinned, last accessed within 30 days, or procedural and not superseded. The planned wording said "live procedural, or younger than 30 days", which holds only under type-aware decay and confused age with last access.
- `docs/engram-prd-v1.md:173`, `docs/demo.md` and `README.md:177` no longer call the sweep stats store-wide. They say all namespaces, this process's passes.

## Deviations from the plan

- The fixture rule in `CONTRIBUTING.md` and the three doc sentences above differ from the text in plan.md Task 6. design.md, tasks.md and the `agent-acceptance` delta were updated to match.
- Task 4 was edited with `sed`, not the Edit tool. The diff is the five planned lines.
- Review left these as they are, each with a reason in the review: a zero delay on an already-cancelled context may start one pass; no test mixes an already-gone row with a deleted one; a skipped row logs nothing at any level; the stdio test proves the logged constant, not the argument `Run` receives.

## Evidence gathered during apply

- `go build ./...` and `go test -race -count=1 -coverprofile=cover.out ./...` on the final tree: 9 packages ok. `sweep` coverage 100%.
- `golangci-lint run ./...`: 0 issues (local 2.15.0; CI pins v2.12.2).
- Integration, uncached, `-p 2`, every package except the body of `./eval`: 331 passed, 0 failed, 0 skipped. `TestStoreSearch` passed three times beforehand.
- `go run ./cmd/eval --ci` passed. Type-aware precision@5 0.179 and ndcg@5 0.875 against a baseline of 0.1788 and 0.8747. The full output was compared with `main`'s: every figure is identical except the two wall-clock latency columns (p50, p95), and those also differ between two runs of the same tree.
- Acceptance on the real binary: `sweep.last_run_at` was null two seconds after start and `2026-11-15T19:30:49Z` at 80 seconds, one minute after the start logged at 19:29:49Z. The pass pruned 181 rows, all test leftovers. The startup line names `sweep_first_delay=1m0s` and `sweep_interval=1h0m0s`.

---

## Next step

Run `/opsx:verify`, then `/opsx:continue` to push and open the PR, then `/opsx:archive` on this branch.
