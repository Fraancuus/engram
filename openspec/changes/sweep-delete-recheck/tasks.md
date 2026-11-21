The domain package does not change. Every Go task runs under `go test -race`. The order is
`mock/`, then the adapters (`sweep`, `neo4j`, `mcp`), then docs; `cmd/` does not change.
`mock/` goes first because `sweep.Pruner` cannot swap its method until both fakes have the new one.

## 1. Fakes (`mock/`)

- [x] 1.1 RED then GREEN: `MemStore.DeleteIfStale(ctx, id, before) (bool, error)` deletes a memory only when it is unpinned and `LastAccessed` is before `before`, under the store's mutex, and shares one unexported cascade (`deleteLocked`) with `Delete`. Proof: `TestMemStoreDeleteIfStale` in `mock/memstore_test.go` (table: stale row deleted with its link and mention; `LastAccessed` equal to `before` kept; pinned kept; reinforced kept; unknown id returns false and nil), the existing `TestMemStoreDeleteCascades` still passes, and the existing race test `TestMemStoreConcurrentSearchDelete` also runs `Reinforce` and `DeleteIfStale` with no race reported.
- [x] 1.2 `FakeStore.DeleteIfStale` records the id in `Deleted` and the cutoff in `StaleBefore`, and returns `(!KeptIDs[id] && DeleteErr == nil, DeleteErr)`; `FakeStore.PruneCandidates` records its cutoff in `PruneBefore`; `FakeStore.Reinforce` returns `ReinforceErrs[id]` when that id has one, before the store-wide `ReinforceErr`; `FakeStore.PropagateReinforce` appends the id to `Propagated`. Proof: `TestFakeStoreDeleteIfStale` (zero value deletes; an id in `KeptIDs` is kept while another is deleted; `DeleteErr` returns false and the error; `StaleBefore` and `PruneBefore` hold the cutoffs given) and `TestFakeStorePerIDScripts` (one id fails `Reinforce` with its own error while another succeeds; `Propagated` lists every id in call order) in `mock/fakes_test.go`.

## 2. Adapter: sweep (`sweep/`)

- [x] 2.1 RED then GREEN: `Pruner` swaps `Delete` for `DeleteIfStale`; `SweepOnce` computes one `before`, passes it to `PruneCandidates` and to every `DeleteIfStale`, counts a row only when it was deleted, and drops the `ErrNotFound` branch. Proof: `TestSweepOnceKeptRowIsNotCounted` (two candidates, one in `KeptIDs`: 1 pruned, nothing logged at error level), `TestSweepOnceUsesOneCutoff` (both cutoffs equal `now` minus the grace), the rewritten `TestSweepOnceAlreadyDeletedIsNotAnError`, and `TestSweepOnceOtherDeleteErrorStillLogged`.
- [x] 2.2 Eval check, then stop or go on: run `go run ./cmd/eval --ci`, compare its full output with `main`'s with the p50 and p95 columns removed, and run `git diff origin/main -- eval/ cmd/eval/`. Proof: the gate passes, the outputs are equal, the type-aware precision and nDCG are the 0.179 and 0.875 of `eval/baseline.json`, the diff is empty. If anything differs, stop and report to Franco before any other task.

## 3. Adapter: store (`neo4j/`)

- [x] 3.1 RED then GREEN: `Store.DeleteIfStale` with the lock-first statement from design.md D1, parameterized, with the comment that explains the dummy write and cites the Neo4j manual section. Proof: `TestStoreDeleteIfStale` in a new `neo4j/stale_integration_test.go` (integration tag): stale row deleted and no relationship left; fresh row kept with no `sweep_check` property; pinned row kept; `last_accessed` equal to the cutoff kept and one millisecond earlier deleted; unknown id returns false and nil.
- [x] 3.2 RED then GREEN: `Reinforce`, `Pin` and `SetForgotten` match the row again after their write, with the statements from design.md D3; `Reinforce` writes `last_accessed` first and increments `access_count` on the re-matched row, with the comment that says why. Proof: `TestStoreWriterNeverAcknowledgesADeletedRow` (integration tag): `Reinforce` against `DeleteIfStale` and `Pin` against `DeleteIfStale`, 100 rounds each, released together with an alternating head start, with a one-hour cutoff on rows two hours old; every round ends either "writer nil, delete false, row present with the write applied" or "writer `ErrNotFound`, delete true, row gone", and both occur. The test asserts no wall time. With the `SET`/`REMOVE` pair taken out of `DeleteIfStale` the same test fails; that is run once after the commit and the file is restored. `TestStoreWritersNotFound` (`Reinforce`, `Pin` and `SetForgotten` on an unknown id each return `engram.ErrNotFound`); `SetForgotten` is not raced, since a soft forget does not keep a row from the sweep and the two outcomes cannot be told apart. `TestStoreDelete`, the existing `Reinforce` test (count and timestamp still correct) and the existing not-found tests still pass. The test removes its rows in `t.Cleanup`.

## 4. Adapter: handlers (`mcp/`)

- [x] 4.1 RED then GREEN: `remember` falls through to its insert path when the dedup target's `Reinforce` returns `engram.ErrNotFound`. Proof: `TestDoRememberStoresContentWhenDedupTargetIsGone` (`Put` called with the content, reply `deduped: false` with the new id, `supersedes` applied), `TestDoRememberTargetGoneBesideSecondCandidate` (a second candidate at 0.96: a new memory is stored and `Reinforce` is not called for the second), `TestRememberDedupTargetGoneIsRecallable` (on a `MemStore`: recall returns the new memory), `TestDoRememberReinforceFailureStillFails` (another error: `remember: store unavailable`, no `Put`), and the strengthened `TestDoRememberDedupBoundaryUnchanged` (a live target at exactly 0.95: reply names it, it is reinforced, no `Put`).
- [x] 4.2 RED then GREEN: `recall` logs `engram.ErrNotFound` from a hit's `Reinforce` at debug level, skips its propagation, and still returns the hit. Proof: `TestRecallToleratesDeletedHit` (three hits, the second in `ReinforceErrs`: three returned in order, no error-level record, `Propagated` holds the first and third only).

## 5. Docs

- [x] 5.1 `CONTRIBUTING.md`: one paragraph saying the concurrent store test guards the lock-first delete and the re-match in `Reinforce` and `Pin`, and is rerun whenever the Neo4j image changes, because CI does not run integration tests. Proof: `grep -n "DeleteIfStale" CONTRIBUTING.md` prints a line of it.
- [x] 5.2 Proof that no doc still describes the old behaviour: `grep -rn -i -E "store unavailable|unconditional" README.md docs/demo.md docs/engram-prd-v1.md` prints nothing that says a vanished dedup target is an error.

## 6. Verify

- [x] 6.1 `go build ./...`, `go test -race -coverprofile=cover.out ./...`, `golangci-lint run ./...`, and `graphify update .`.
- [x] 6.2 Integration with the stack up and the schema applied: `go test -race -count=1 -tags integration -p 2 -v` on every package except the body of `./eval`. No skip may name `unavailable`.
- [x] 6.3 `go run ./cmd/eval --ci` on the final tree, compared with `main`'s full output as in 2.2; report the A/B/C table.
- [x] 6.4 Project agents: `reviewer` on the Go diff, `security-auditor` on the store queries and the handlers, and a skeptic review of the whole branch that tries to refute each finding, because `remember` is a write path. Resolve every blocking finding.
