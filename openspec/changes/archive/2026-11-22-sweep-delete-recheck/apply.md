# Apply Receipt

**Change**: `sweep-delete-recheck`
**Iteration**: `1`
**Applied at**: `2026-11-22 01:00`
**Executor**: `subagent-driven-development`

---

## Workspace

- **Worktree**: none. The main checkout, because Franco asked for it for this series of changes.
- **Branch**: `feat/sweep-delete-recheck`, cut from `origin/main` at `9c59ff3`. Nothing is pushed yet.

---

## Commits

- **Range**: `bba1966..5d80a7f`
- **Count**: `9` (six task commits, one tasks.md tick, two commits of the fix round after the whole-branch review)

`git log --oneline origin/main..HEAD` lists 14 commits:

- Planning documents, before the range: `4bdd579` (brainstorm, proposal, design, specs, tasks, plan and the visual plan), `b612950` (change metadata), `bba1966` (open questions closed).
- Tasks: `b538084` and `74d2481` (Task 1; the second is a gofmt of the `FakeStore` field block), `a7e2756` (Task 2), `87c07e2` (Task 3), `9dd0a70` (Task 4), `e9d3313` (Task 5).
- `52ee604` ticks tasks 1 to 5 in tasks.md.
- Review fix round: `bb9fdee` (tests, one production log line, three doc comments) and `5d80a7f` (`CONTRIBUTING.md`).
- After the range, openspec only: `966056c` (design.md records what the fix round changed) and `5793fea` (ticks the verify tasks).

---

## Tasks

- **Completed**: `14 of 14` checkboxes in tasks.md flipped to `- [x]`
- **Remaining**: none

---

## How it was run

Sonnet implemented each of Tasks 1 to 4 from its brief, test first. Task 5 (docs) ran on Haiku. Opus reviewed each task for spec compliance and quality. All five task reviews approved with no Critical or Important finding, so no fix round ran inside a task.

The whole-branch review ran from four lenses: the project's `reviewer` agent (Opus), the project's `security-auditor` agent (Fable), an adversarial correctness reviewer (Opus) and a spec-conformance reviewer (Opus). A separate Opus refuter attacked every finding filed as Critical or Important. Result: 0 Critical, 0 Important. Two findings filed as Important were the same point about the race test's comment, and were downgraded to wording. The spec-conformance reviewer found a test asserting the full THEN for 30 of the 30 scenarios that tests prove; the other two are proven by commands.

Eleven minor findings were fixed in one round (`bb9fdee`, `5d80a7f`). A scoped re-review on Haiku found all eleven fixed and no regression:

- The recall test now logs at debug level and asserts the debug record. It was shown to fail with the log line deleted.
- The gone-target remember test asserts which row was reinforced, and has a case with entities.
- `remember` logs its fall-through at debug level. This is the one production line of the round.
- The relationships test asserts the edges existed before the delete.
- The race test's comment and failure message say a passing run shows both orders, not overlap.
- Each round of the race test has its own 30 s hang guard, in place of one 2-minute deadline for all rounds.
- Three doc comments corrected: `Reinforce`, `Pruner`, `SweepOnce`.
- The recall comment says `engramd` has no switch to show debug records.
- `CONTRIBUTING.md` asks for the mutation check when the Neo4j image changes.

## Deviations from the plan

- The fix round changed text that plan.md gives literally. plan.md:1057 still says a run "in which the two calls never overlapped cannot pass". plan.md:25 and :1063 still give one 2-minute deadline. plan.md Task 4 gives the `remember` fall-through without its debug log line. design.md was updated to match in `966056c`; plan.md is left as the record of what was planned.
- Task 1 landed in two commits. The second, `74d2481`, only runs gofmt on the `FakeStore` field block.
- The mutation check ran twice, not once: in Task 3, and again after the fix round changed the race test.
- Not a deviation: `a7e2756` (Task 2) adds a `DeleteIfStale` stub with the old unconditional behaviour to `neo4j/store.go`, so that `cmd/engramd` builds. plan.md:19 and :451 call for it, and Task 3 replaced it.

## Evidence gathered during apply

Code state at `5d80a7f`; the commits after it touch only `openspec/`.

- `gofmt -l .`: nothing. `go build ./...`: ok. `go vet ./...` and `go vet -tags integration ./...`: ok.
- `golangci-lint run ./...`: 0 issues. `golangci-lint run --build-tags integration ./...`: 0 issues. Local golangci-lint is 2.15.0; CI pins v2.12.2.
- `gitleaks git --log-opts="origin/main..HEAD"`: no leaks found.
- `go test -race -count=1 -coverprofile=cover.out ./...`: 9 packages ok. `./eval` took 273.9 s; no eval test was added. Coverage: root 93.5%, `cmd/engramd` 17.5%, `cmd/eval` 75.5%, `eval` 93.4%, `inference` 89.5%, `mcp` 90.8%, `mock` 86.0%, `neo4j` 2.0% (unit run; its tests are integration tests), `sweep` 100.0%.
- Integration, stack up (neo4j, tei, tei-rerank healthy), schema applied: `go test -race -count=1 -tags integration -p 2 -v` on every package except `./eval` (`go list ./... | grep -v 'engram/eval$'`). 8 packages ok, 371 PASS lines (252 top-level tests), 0 FAIL, 0 SKIP. `TestStoreWriterNeverAcknowledgesADeletedRow`: for `Reinforce`, 51 rounds kept the row with the write and 49 deleted it; for `Pin`, 49 and 51.
- Mutation check: the race test must fail when the dummy write is taken out of `DeleteIfStale`. Run in Task 3 and again after the fix round. Both times the test failed, with round lines for both pairings, and passed again after `git restore neo4j/store.go`.
- Dev store: 384 `:Memory` rows before the integration run. The run added 77 rows, all left by existing tests (namespaces `itest-*`, `m1-*` to `m4-*`, `sweep-itest-*`, and three ids `m0-e2e-*`, `m0-itest-roundtrip-*`, `m0-itest-localtime-*` in `work/engineering`), all created inside the run's window. They were deleted by exact id. After: 384 rows, the id set identical to before, 0 rows with a `sweep_check` property.
- `go run ./cmd/eval --ci` on the branch: exit 0, "eval gate passed". The same on a scratch copy of `origin/main`: exit 0. The two full outputs are equal once the p50 and p95 columns are removed. `git diff origin/main -- eval/ cmd/eval/` is empty. Type-aware precision@5 0.179 and ndcg@5 0.875, which are `eval/baseline.json`'s 0.1788 and 0.8747 at the report's three decimals.
- One orchestrator mistake, recorded plainly: the first attempt at the eval comparison had a shell error. A `git rev-parse` call with two revisions failed and short-circuited the chain, so only `main`'s side ran. It was not an eval result. The branch side was then run, and the comparison above is from that second run.

---

## After the PR opened

DeepSource failed its Go check on PR #39. Three commits followed on this branch. None changes
behaviour.

- `2fbe39d`: `TestMemStoreDeleteIfStale` was over the complexity threshold. Its fixture and its
  row count moved into two helpers. It asserts the same things.
- `666cdd9`: `doRemember` had a cyclomatic complexity of 44, up from the value on `main` because
  this change adds one `switch` to it. Franco pointed at the failed check on 2026-11-22, so the
  function was split into `validateRememberScalars`, `validateImportance`, `validateIDList`,
  `mergeDuplicate` and `insertMemory`. `gocyclo` now gives 12 for `doRemember` and at most 11 for
  the others; the analyzer's threshold is 15. Only `mcp/remember.go` changed, no test was edited,
  and the same 112 tests passed before and after. `reviewer` (Opus) and `security-auditor`
  (Fable) both found the behaviour preserved; the auditor compared the old and the new handler
  over 36,204 inputs and store states and found no difference.
- `bf075b5`: tests that pin what the split made easier to break: the exact validation messages,
  which are now built from parameters, and the failure paths through the new functions (search,
  `Put`, `LinkEntities`, `Link`, id generation, a panic). Each new test was shown to fail when the
  thing it pins is broken. `mergeDuplicate` now takes the target's id only, and three comments
  were corrected.

Checked again on `bf075b5`: `gofmt`, build, `go vet` and `golangci-lint` with and without the
integration tag (0 issues), `gitleaks` (no leaks); `go test -race -count=1 ./...` (9 packages ok,
`mcp` coverage 94.6%); integration on every package except `./eval` against the live stack (8
packages ok, 389 passes, 0 failed, 0 skipped; the 77 rows existing tests left were deleted by
exact id and the store is back to its 384 rows); `go run ./cmd/eval --ci` passed and its full
output equals `main`'s with the p50 and p95 columns removed.

Two DeepSource dispositions are on record in the PR: 38 findings of `VET-V0010` are false
positives at `go 1.26` and are ignored with file-level rules, as two other test files already
were. The security auditor also noted, as older than this change, that `remember` echoes an
invalid `type` in its error with no length cap; capping it changes a message and is left for a
follow-up.

---

## Next step

Run `/opsx:verify`, then `/opsx:continue` to push and open the PR, then `/opsx:archive` on this branch.
