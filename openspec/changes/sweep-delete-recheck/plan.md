# Sweep Delete Recheck Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task, with superpowers:test-driven-development inside each task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A sweep pass deletes a candidate only while it is still stale, `Reinforce`, `Pin` and `SetForgotten` never report success on a row that is gone, and `remember` stores its content when its dedup target was deleted under it.

**Architecture:** `sweep.Pruner` swaps `Delete` for `DeleteIfStale(ctx, id, before) (bool, error)`. On Neo4j that is one statement that takes the row's write lock with a dummy `SET` and `REMOVE`, then re-checks the `PruneCandidates` predicate in a `WITH m WHERE`, and deletes only then. `Pin` and `SetForgotten` match the row again after their `SET`; `Reinforce` writes `last_accessed` first, matches the row again and increments `access_count` on the re-matched row. So a row deleted while a writer waited is `engram.ErrNotFound`. `remember` falls through to its existing insert path on that error, and `recall` logs it at debug level and skips its propagation. Both fakes follow: `mock.MemStore` shares one delete cascade between `Delete` and `DeleteIfStale`, so the eval cannot move, and `mock.FakeStore` gains per-id scripts (`KeptIDs`, `ReinforceErrs`, `Propagated`) so no test file needs its own wrapper.

**Tech Stack:** Go 1.26, standard library (`sync`, `log/slog`, `testing`), `github.com/neo4j/neo4j-go-driver/v5` v5.28.5 (already a dependency), Neo4j `5.26-community` from `docker-compose.yml`. No new dependency.

**Spec:** `openspec/changes/sweep-delete-recheck/design.md` (decisions D1 to D8) and the delta specs in `openspec/changes/sweep-delete-recheck/specs/` (`memory-store-fake`, `sweep-schedule`, `write-race-safety`). Tasks: `openspec/changes/sweep-delete-recheck/tasks.md`.

## Global Constraints

- Branch `feat/sweep-delete-recheck`. Confirm with `git branch --show-current` before the first edit. Stage files by name. Never push, rebase, amend or switch branch.
- Nothing under `eval/`, `cmd/eval/`, `cmd/engramd/`, `schema/` or `inference/` is edited, and nothing in the root package (`engram.go`, `decay.go` and their tests). If a step seems to need that, stop and report.
- The unconditional `Delete` stays on `*neo4j.Store`, `*mock.MemStore` and `*mock.FakeStore`, and on the `mcp` interfaces (`forgetStore`, `mcp/forget.go:14-19`). `forget` with mode hard keeps calling it.
- On a store nobody else writes to during the pass, `SweepOnce` returns the same count and the same error as it does today.
- `cmd/engramd/main.go:74` passes `*neo4j.Store` to `sweep.New`, so `*neo4j.Store` must have `DeleteIfStale` from the moment `Pruner` swaps its method, or `go build ./...` fails. Task 2 therefore adds a stub with the old unconditional behaviour to `neo4j/store.go`, and Task 3 replaces it. This is the only `neo4j/` production edit outside Task 3.
- No `time.Now` call in `sweep/sweep.go` or `sweep/sweep_test.go`; pass time comes from `now` or `engram.Clock`. Integration tests under `neo4j/` may call `time.Now()`.
- Integration fixtures (`CONTRIBUTING.md`, lines 71-73): the new store tests pass their own cutoff of one hour ago and use rows two hours old, never rows older than 30 days, because a running `engramd` sweeps the same store. Every new integration test removes the rows it creates in `t.Cleanup`, by exact id, and the entities it creates by id.
- Every Cypher statement is parameterized and appears verbatim from design.md D1 and D3: the lock-first `DeleteIfStale` (`SET`, `REMOVE`, then `WITH m WHERE ...`), the re-match for `Pin` and `SetForgotten`, and the write-first `Reinforce`.
- RED means an assertion fails against the old behaviour, not a compile error. Where a method, a field or an interface is new, a stub step first makes the code compile with the old behaviour.
- Per-id scripting of the fake lives in `mock.FakeStore` (`KeptIDs`, `ReinforceErrs`, `Propagated`). No test file declares its own wrapper around `FakeStore` to script results. The one wrapper allowed is `sweptStore`, which Task 4 adds to `mcp/remember_test.go`: it is not a script, it simulates the race on a real `MemStore`. `FakeStore` has no mutex and every test drives it from one goroutine; the new fields keep that.
- No test asserts wall time, and no Expected line sets a time limit. A deadlock retry costs about 2 s and is correct. The 2-minute context deadline in `TestStoreWriterNeverAcknowledgesADeletedRow` is a hang guard only.
- Each step that edits existing code names the function and the line range it replaces and shows the complete new block, comments in place. Line numbers are those of the file at the start of that step. Replace exactly that block; do not merge it by hand with the old text.
- Every test passes under `go test -race`. Memory on the dev machine is short: run one `go test` command at a time, only the packages a step names, and never `./eval` except in the eval gate of Task 2 and in the Verify task.
- After any import edit run `gofmt -l` on the file; it must print nothing. Edit Go files with the Edit tool, not a script that may rewrite line endings.
- Run `graphify update .` after each task that changes Go code.
- Commit messages end with a blank line and a `Co-Authored-By:` trailer for the model that wrote the commit: the second `-m` in each commit step. Replace `<model>` with that model's name.
- Do not edit anything under `plans/` or `openspec/changes/archive/`.
- Integration steps run from Git Bash with the stack up (`docker compose up -d --wait`) and the schema applied. A `SKIP` that names `unavailable` proves nothing: report it and stop.
- Existing integration tests leave rows in the shared dev store: about 10 per run of the `neo4j` and `sweep` packages, in namespaces `itest`, `itest-flags-<ts>` and `sweep-itest-<ts>`. Enough of them break `TestStoreSearch`. Before the first integration run of a task, record the ids (`MATCH (m:Memory) RETURN m.id`). After the last one, delete by exact id the rows your runs added and nothing else, and report the count before, after the runs and after the cleanup. Never delete by namespace prefix or by age.

## Review Focus

Conditions the specs imply that are most likely to bite, each pinned by a test in the task that owns the code:

1. A row whose `last_accessed` equals the cutoff is kept, in both stores. The comparison is strict `<`, the same as `PruneCandidates`. Task 1, `TestMemStoreDeleteIfStale/LastAccessed_equal_to_before_kept`; Task 3, `TestStoreDeleteIfStale/last_accessed_equal_to_the_cutoff` and `/one_millisecond_before_the_cutoff`.
2. The Neo4j re-check must read `last_accessed` and `pinned` after the lock, not at `MATCH` time: the dummy `SET` comes first and the `WHERE` follows the `WITH m`. If the statement read them first, every sequential test would still pass; only the race test sees it. Task 3, `TestStoreWriterNeverAcknowledgesADeletedRow` (item 6).
3. A candidate the store kept is neither counted nor logged at error level, and every delete of a pass gets the cutoff the candidate read used. Task 2, `TestSweepOnceKeptRowIsNotCounted` (two candidates, one in `KeptIDs`) and `TestSweepOnceUsesOneCutoff`.
4. `remember` falls through only on `engram.ErrNotFound`: any other `Reinforce` error still fails without a `Put` or a supersede, and the handler never tries the next candidate. Task 4, `TestDoRememberStoresContentWhenDedupTargetIsGone`, `TestDoRememberTargetGoneBesideSecondCandidate` (`Reinforced` is exactly `[gone]` while `other` at 0.96 would succeed) and `TestDoRememberReinforceFailureStillFails`.
5. `MemStore.DeleteIfStale` must remove edges and mentions exactly as `Delete` does, or the eval's edge and bridge columns move. Task 1, `TestMemStoreDeleteIfStale/stale_row_deleted_with_its_link_and_mention` with the unchanged `TestMemStoreDeleteCascades`; Task 2, the eval gate.
6. The two-state assertion. Every round of `TestStoreWriterNeverAcknowledgesADeletedRow` must end in exactly one of two states: writer nil, `DeleteIfStale` false, row present with the write applied (after `Reinforce`, `access_count` 1 higher and `last_accessed` the given `now`; after `Pin`, `pinned` true); or writer `engram.ErrNotFound`, `DeleteIfStale` true, row gone. Any other round fails with its round number and what was seen, and each pairing must show both states. A test that only counted "nil while gone" would pass a kept row whose write was lost. Task 3.
7. Write before read in `Reinforce`: `SET m.last_accessed = $now`, then the re-match, then `SET x.access_count = x.access_count + 1` on the re-matched row. A statement that reads `access_count` before its first write deadlocks against the lock-first delete; the driver retries, so no assertion fails and the call only takes about 2 s. Review the statement and its comment against design.md D3. Task 3, Step 8.
8. No test asserts wall time. Reject any `time.Since` check, any "under N s" expectation and any shortened context deadline: a deadlock retry is slow and correct. Task 3.

---

### Task 1: Fakes (`mock/`)

**Files:**
- Modify: `mock/memstore.go` (`Delete`, lines 297-319: its cascade moves into `deleteLocked`; add `DeleteIfStale` after `Delete`)
- Modify: `mock/fakes.go` (a new field block in `FakeStore` after the M4 block, which ends at line 65; `Reinforce`, lines 88-92; add `DeleteIfStale` after `Delete`, lines 136-140; `PropagateReinforce`, lines 142-146; `PruneCandidates`, lines 148-151)
- Test: `mock/memstore_test.go` (append `TestMemStoreDeleteIfStale`; replace `TestMemStoreConcurrentSearchDelete`, lines 360-390; the file already declares the package-level `ctx`, `t0` and `q`, lines 17-21, and the helpers `mem` and `mustPut`, lines 28-39)
- Create: `mock/fakes_test.go` (`package mock`; it reuses `ctx` and `t0` from `mock/memstore_test.go`, so it must not declare them again)

**Interfaces:**
- Consumes: `NewMemStore`, `(*MemStore).Put`, `Link`, `LinkEntities`, `Pin`, `Reinforce`, `Search`, `Neighbors`, `Snapshot`, `EdgeCount`, `BridgeCount`, `Len`, `Delete`; `engram.ErrNotFound`.
- Produces: `func (s *MemStore) DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (bool, error)`; `func (f *FakeStore) DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (bool, error)`; new `FakeStore` fields `KeptIDs map[engram.MemoryID]bool`, `ReinforceErrs map[engram.MemoryID]error`, `Propagated []engram.MemoryID`, `StaleBefore []time.Time`, `PruneBefore time.Time`; `FakeStore.Reinforce` checks `ReinforceErrs[id]` before `ReinforceErr`; `FakeStore.PropagateReinforce` appends to `Propagated`; `FakeStore.PruneCandidates` records `PruneBefore`. Task 2 uses `DeleteIfStale`, `KeptIDs`, `StaleBefore` and `PruneBefore`; Task 4 uses `ReinforceErrs` and `Propagated`.

- [ ] **Step 1: Stub `MemStore.DeleteIfStale` with the old behaviour**

In `mock/memstore.go`, insert after `Delete` (after line 319, with one blank line between). No import change: `MemStore.Delete` fails only with `engram.ErrNotFound`.

```go
// DeleteIfStale is a stub until Step 4: it deletes unconditionally, as Delete does.
func (s *MemStore) DeleteIfStale(ctx context.Context, id engram.MemoryID, _ time.Time) (bool, error) {
	return s.Delete(ctx, id) == nil, nil
}
```

Run: `go test -race ./mock/`
Expected: `ok`.

- [ ] **Step 2: Write the failing table test and extend the race test**

Append to `mock/memstore_test.go` (no new import):

```go
func TestMemStoreDeleteIfStale(t *testing.T) {
	t.Parallel()
	before := t0.Add(time.Hour) // x and y were last accessed at t0, so both are stale here
	for _, tc := range []struct {
		name        string
		write       func(t *testing.T, s *MemStore) // a write that lands after the candidate read
		id          engram.MemoryID
		before      time.Time
		wantDeleted bool
	}{
		{name: "stale row deleted with its link and mention", id: "x", before: before, wantDeleted: true},
		{name: "LastAccessed equal to before kept", id: "x", before: t0},
		{name: "pinned kept", id: "x", before: before, write: func(t *testing.T, s *MemStore) {
			if err := s.Pin(ctx, "x"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "reinforced kept", id: "x", before: before, write: func(t *testing.T, s *MemStore) {
			if err := s.Reinforce(ctx, "x", before); err != nil { // LastAccessed becomes before: not before it
				t.Fatal(err)
			}
		}},
		{name: "unknown id", id: "nope", before: before},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewMemStore()
			mustPut(t, s, mem("x", "a", 0.9), mem("y", "a", 0.9))
			if err := s.Link(ctx, "x", []engram.Link{{To: "y", Weight: 0.9}}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []engram.MemoryID{"x", "y"} {
				if err := s.LinkEntities(ctx, id, []string{"e"}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.write != nil {
				tc.write(t, s)
			}

			deleted, err := s.DeleteIfStale(ctx, tc.id, tc.before)
			if deleted != tc.wantDeleted || err != nil {
				t.Fatalf("DeleteIfStale(%s) = (%v, %v), want (%v, nil)", tc.id, deleted, err, tc.wantDeleted)
			}

			// While x lives, y sees it twice (one link, one entity bridge) and the store holds
			// one edge and one bridge. A delete must remove all of it, as Delete does.
			_, xKept := s.Snapshot("x")
			nbs, err := s.Neighbors(ctx, []engram.MemoryID{"y"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			xRows := 0
			for _, n := range nbs {
				if n.Memory.ID == "x" {
					xRows++
				}
			}
			wantKept, wantEdges, wantBridges, wantRows := true, 1, 1, 2
			if tc.wantDeleted {
				wantKept, wantEdges, wantBridges, wantRows = false, 0, 0, 0
			}
			if xKept != wantKept || s.EdgeCount() != wantEdges || s.BridgeCount() != wantBridges || xRows != wantRows {
				t.Errorf("x kept=%v edges=%d bridges=%d y-to-x rows=%d, want %v %d %d %d",
					xKept, s.EdgeCount(), s.BridgeCount(), xRows, wantKept, wantEdges, wantBridges, wantRows)
			}
		})
	}
}
```

Then replace `TestMemStoreConcurrentSearchDelete` (lines 360-390) with this block. It backs the spec scenario "Concurrent recall and sweep": `Reinforce` and `DeleteIfStale` now run from their own goroutines beside the existing readers and deleters.

```go
func TestMemStoreConcurrentSearchDelete(t *testing.T) {
	t.Parallel()
	s := NewMemStore()
	for i := 0; i < 50; i++ {
		mustPut(t, s, mem(fmt.Sprintf("m%d", i), "a", 0.9))
	}
	// Every row was last accessed at t0 and Reinforce below keeps it there, so DeleteIfStale
	// with this cutoff may delete any row the Delete goroutines have not reached yet.
	stale := t0.Add(time.Hour)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.Search(ctx, nil, q, 5)
				_, _ = s.Neighbors(ctx, []engram.MemoryID{"m0"}, nil)
				_ = s.EdgeCount()
			}
		}()
		go func(g int) {
			defer wg.Done()
			for i := g; i < 50; i += 4 {
				id := engram.MemoryID(fmt.Sprintf("m%d", i))
				_ = s.Link(ctx, id, []engram.Link{{To: "m0", Weight: 0.9}})
				_ = s.Delete(ctx, id)
			}
		}(g)
		// Recall's reinforce and the sweep's stale delete, each from its own goroutine.
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.Reinforce(ctx, engram.MemoryID(fmt.Sprintf("m%d", i)), t0)
			}
		}()
		go func(g int) {
			defer wg.Done()
			for i := (g + 2) % 4; i < 50; i += 4 {
				_, _ = s.DeleteIfStale(ctx, engram.MemoryID(fmt.Sprintf("m%d", i)), stale)
			}
		}(g)
	}
	wg.Wait()
	if s.Len() != 0 {
		t.Fatalf("len %d", s.Len())
	}
}
```

- [ ] **Step 3: Run them and see the behaviour fail**

Run: `go test -race -run 'TestMemStoreDeleteIfStale|TestMemStoreConcurrentSearchDelete' -v ./mock/`
Expected: FAIL in `TestMemStoreDeleteIfStale/LastAccessed_equal_to_before_kept`, `/pinned_kept` and `/reinforced_kept`, each with `DeleteIfStale(x) = (true, <nil>), want (false, nil)`. `/stale_row_deleted_with_its_link_and_mention` and `/unknown_id` pass. `TestMemStoreConcurrentSearchDelete` passes with no race reported: it is a guard that the real method in Step 4 still takes the store's lock.

- [ ] **Step 4: Implement `DeleteIfStale` over a shared cascade**

In `mock/memstore.go`, replace `Delete` and the Step 1 stub (lines 297-324) with this block. `Delete` keeps its signature, doc comment, lock and not-found check; its cascade moves unchanged into `deleteLocked`; `DeleteIfStale` uses the predicate `PruneCandidates` uses (`m.LastAccessed.Before(before) && !m.Pinned`, line 331 before this step).

```go
// Delete removes the memory with every link edge and entity mention, or returns
// engram.ErrNotFound.
func (s *MemStore) Delete(_ context.Context, id engram.MemoryID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mems[id]; !ok {
		return fmt.Errorf("delete %q: %w", id, engram.ErrNotFound)
	}
	s.deleteLocked(id)
	return nil
}

// DeleteIfStale deletes the memory exactly as Delete does, but only while PruneCandidates(before)
// would still return it: unpinned, with LastAccessed before `before`. The check and the delete
// hold one write lock. It reports whether it deleted; an unknown id is (false, nil).
func (s *MemStore) DeleteIfStale(_ context.Context, id engram.MemoryID, before time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mems[id]
	if !ok || m.Pinned || !m.LastAccessed.Before(before) {
		return false, nil
	}
	s.deleteLocked(id)
	return true, nil
}

// deleteLocked removes the memory with every link edge and entity mention. The caller holds
// the write lock and has checked that id exists. Delete and DeleteIfStale share it so their
// cascades cannot drift apart, which would move the eval's edge and bridge counts.
func (s *MemStore) deleteLocked(id engram.MemoryID) {
	delete(s.mems, id)
	s.order = slices.DeleteFunc(s.order, func(o engram.MemoryID) bool { return o == id })
	for nb := range s.links[id] {
		delete(s.links[nb], id)
	}
	delete(s.links, id)
	for ent := range s.mentions[id] {
		delete(s.byEntity[ent], id)
		if len(s.byEntity[ent]) == 0 {
			delete(s.byEntity, ent)
		}
	}
	delete(s.mentions, id)
}
```

- [ ] **Step 5: Run the store tests**

Run: `go test -race -run 'TestMemStore' ./mock/ && gofmt -l mock/`
Expected: `ok`, with `TestMemStoreDeleteIfStale`, `TestMemStoreDeleteCascades`, `TestMemStoreConcurrentSearchDelete` and `TestMemStoreStatsConcurrent` passing; `gofmt` prints nothing.

- [ ] **Step 6: Stub the new `FakeStore` fields and `DeleteIfStale` with the old behaviour**

`FakeStore` has no mutex; every test drives it from one goroutine, and the new fields keep that. In `mock/fakes.go`, insert this block inside `FakeStore`, after the M4 block (after line 65, `LastPropagateThr float64`) and before the blank line that precedes `CountsRes`. It is its own block, so `gofmt` leaves the M4 alignment alone.

```go

	// Per-id scripts and the sweep's cutoffs. A nil map reads as empty, so the zero value
	// behaves as it did before these fields existed.
	KeptIDs       map[engram.MemoryID]bool  // ids DeleteIfStale reports kept, as for a row changed since the read
	ReinforceErrs map[engram.MemoryID]error // per-id Reinforce errors, checked before ReinforceErr
	Propagated    []engram.MemoryID         // every id given to PropagateReinforce, in call order
	StaleBefore   []time.Time               // the cutoff of every DeleteIfStale call
	PruneBefore   time.Time                 // the cutoff of the last PruneCandidates call
```

Then insert after `Delete` (after line 148 once the fields are in; `Delete` was lines 136-140), with one blank line between:

```go
// DeleteIfStale is a stub until Step 9: it behaves as Delete does.
func (f *FakeStore) DeleteIfStale(_ context.Context, id engram.MemoryID, _ time.Time) (bool, error) {
	f.Deleted = append(f.Deleted, id)
	return f.DeleteErr == nil, f.DeleteErr
}
```

Run: `go test -race ./mock/ && gofmt -l mock/`
Expected: `ok`, and `gofmt` prints nothing.

- [ ] **Step 7: Write the failing fake tests**

Create `mock/fakes_test.go`:

```go
package mock

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

func TestFakeStoreDeleteIfStale(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		f       *FakeStore
		want    map[engram.MemoryID]bool // what DeleteIfStale reports, per id
		wantErr error
	}{
		{name: "zero value deletes", f: &FakeStore{},
			want: map[engram.MemoryID]bool{"a": true, "b": true}},
		{name: "an id in KeptIDs is kept while another is deleted", f: &FakeStore{KeptIDs: map[engram.MemoryID]bool{"b": true}},
			want: map[engram.MemoryID]bool{"a": true, "b": false}},
		{name: "DeleteErr returns false and the error", f: &FakeStore{DeleteErr: boom},
			want: map[engram.MemoryID]bool{"a": false, "b": false}, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// One sweep pass: the candidate read, then a delete per candidate, all with one cutoff.
			if _, err := tc.f.PruneCandidates(ctx, t0, 10); err != nil {
				t.Fatal(err)
			}
			for _, id := range []engram.MemoryID{"a", "b"} {
				deleted, err := tc.f.DeleteIfStale(ctx, id, t0)
				if deleted != tc.want[id] || !errors.Is(err, tc.wantErr) {
					t.Errorf("DeleteIfStale(%s) = (%v, %v), want (%v, %v)", id, deleted, err, tc.want[id], tc.wantErr)
				}
			}
			if !slices.Equal(tc.f.Deleted, []engram.MemoryID{"a", "b"}) {
				t.Errorf("Deleted = %v, want [a b]", tc.f.Deleted)
			}
			if !slices.EqualFunc(tc.f.StaleBefore, []time.Time{t0, t0}, time.Time.Equal) {
				t.Errorf("StaleBefore = %v, want [%v %v]", tc.f.StaleBefore, t0, t0)
			}
			if !tc.f.PruneBefore.Equal(t0) {
				t.Errorf("PruneBefore = %v, want %v", tc.f.PruneBefore, t0)
			}
		})
	}
}

func TestFakeStorePerIDScripts(t *testing.T) {
	t.Parallel()
	gone, boom := errors.New("gone"), errors.New("boom")
	f := &FakeStore{ReinforceErrs: map[engram.MemoryID]error{"b": gone}}
	if err := f.Reinforce(ctx, "a", t0); err != nil {
		t.Errorf("Reinforce(a) = %v, want nil", err)
	}
	if err := f.Reinforce(ctx, "b", t0); !errors.Is(err, gone) {
		t.Errorf("Reinforce(b) = %v, want its own error gone", err)
	}
	f.ReinforceErr = boom
	if err := f.Reinforce(ctx, "a", t0); !errors.Is(err, boom) {
		t.Errorf("Reinforce(a) with ReinforceErr set = %v, want boom", err)
	}
	if err := f.Reinforce(ctx, "b", t0); !errors.Is(err, gone) {
		t.Errorf("Reinforce(b) with ReinforceErr set = %v, want gone: the per-id error comes first", err)
	}
	if !slices.Equal(f.Reinforced, []engram.MemoryID{"a", "b", "a", "b"}) {
		t.Errorf("Reinforced = %v, want [a b a b]: a scripted error is still recorded", f.Reinforced)
	}

	for _, id := range []engram.MemoryID{"c", "a", "c"} {
		if err := f.PropagateReinforce(ctx, id, 0.5, t0); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(f.Propagated, []engram.MemoryID{"c", "a", "c"}) {
		t.Errorf("Propagated = %v, want [c a c] in call order", f.Propagated)
	}
	if f.LastPropagateID != "c" {
		t.Errorf("LastPropagateID = %q, want c", f.LastPropagateID)
	}
}
```

- [ ] **Step 8: Run them and see the behaviour fail**

Run: `go test -race -run 'TestFakeStore' -v ./mock/`
Expected: FAIL. Every subtest of `TestFakeStoreDeleteIfStale` reports `StaleBefore = [], want [2026-01-01 00:00:00 +0000 UTC 2026-01-01 00:00:00 +0000 UTC]` and `PruneBefore = 0001-01-01 00:00:00 +0000 UTC, want 2026-01-01 00:00:00 +0000 UTC`; `an_id_in_KeptIDs_is_kept_while_another_is_deleted` also reports `DeleteIfStale(b) = (true, <nil>), want (false, <nil>)`. `TestFakeStorePerIDScripts` reports `Reinforce(b) = <nil>, want its own error gone`, `Reinforce(b) with ReinforceErr set = boom, want gone: the per-id error comes first` and `Propagated = [], want [c a c] in call order`.

- [ ] **Step 9: Implement the fake per D6**

In `mock/fakes.go`, replace `Reinforce` (lines 96-100 after Step 6) with:

```go
// Reinforce records the id and returns ReinforceErrs[id] when that id has one, otherwise
// the configured ReinforceErr.
func (f *FakeStore) Reinforce(_ context.Context, id engram.MemoryID, _ time.Time) error {
	f.Reinforced = append(f.Reinforced, id)
	if err, ok := f.ReinforceErrs[id]; ok {
		return err
	}
	return f.ReinforceErr
}
```

Then replace the block from the Step 6 stub through `PruneCandidates` (lines 154-169 once `Reinforce` above has grown by four lines; lines 150-165 if you edit this block first) with:

```go
// DeleteIfStale records the id in Deleted and the cutoff in StaleBefore. It reports a delete
// unless the id is in KeptIDs or DeleteErr is set, so the zero value deletes as Delete does.
func (f *FakeStore) DeleteIfStale(_ context.Context, id engram.MemoryID, before time.Time) (bool, error) {
	f.Deleted = append(f.Deleted, id)
	f.StaleBefore = append(f.StaleBefore, before)
	return !f.KeptIDs[id] && f.DeleteErr == nil, f.DeleteErr
}

// PropagateReinforce records the id in Propagated, records the id and threshold of the last
// call, and returns the configured error.
func (f *FakeStore) PropagateReinforce(_ context.Context, id engram.MemoryID, threshold float64, _ time.Time) error {
	f.LastPropagateID, f.LastPropagateThr = id, threshold
	f.Propagated = append(f.Propagated, id)
	return f.PropagateErr
}

// PruneCandidates records the cutoff in PruneBefore and returns the configured candidates/error.
func (f *FakeStore) PruneCandidates(_ context.Context, before time.Time, _ int) ([]engram.Memory, error) {
	f.PruneBefore = before
	return f.PruneCands, f.PruneErr
}
```

- [ ] **Step 10: Run the fakes and their sweep consumer**

Run: `go test -race ./mock/ ./sweep/ && gofmt -l mock/`
Expected: both `ok` (`sweep` still calls `Delete`), and `gofmt` prints nothing.

- [ ] **Step 11: Commit**

```bash
graphify update .
git add mock/memstore.go mock/memstore_test.go mock/fakes.go mock/fakes_test.go
git commit -m "test(mock): add DeleteIfStale and per-id scripts to the fakes" -m "Co-Authored-By: <model> <noreply@anthropic.com>"
```

---

### Task 2: Adapter: sweep (`sweep/`)

**Files:**
- Modify: `sweep/sweep.go` (imports, lines 6-15, where `errors` is line 8; `Pruner` and its doc comment, lines 17-22; `SweepOnce` and its doc comment, lines 65-90)
- Modify: `neo4j/store.go` (a stub `DeleteIfStale` after `Delete`, which ends at line 427; add `errors` to the imports, lines 3-10). Needed only so `cmd/engramd` builds; Task 3 replaces it.
- Test: `sweep/sweep_test.go` (imports, lines 3-18, where `fmt` is line 7; `panicPruner.Delete`, line 172; the `p := ...` statement of `TestSweepOnceAlreadyDeletedIsNotAnError`, lines 402-405 before Step 1 and 404-407 after it; new tests appended)

**Interfaces:**
- Consumes: Task 1's `FakeStore.DeleteIfStale`, `KeptIDs`, `StaleBefore`, `PruneBefore` and `MemStore.DeleteIfStale`; the helpers already in `sweep/sweep_test.go`: `testSweeper`, `statsSweeper` (grace 30 days), `stepClock`, `statsT0`, `fixedClock`; `mock.FakeDecay{R}`.
- Produces: `Pruner` with `DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (deleted bool, err error)` in place of `Delete`. `(*neo4j.Store).DeleteIfStale`, a stub that Task 3 replaces. `New` and `SweepOnce` keep their signatures.

- [ ] **Step 1: Swap the method, behaviour unchanged**

In `sweep/sweep.go`, replace `Pruner` (lines 17-22) with this block. Its doc comment changes in Step 4.

```go
// Pruner is the narrow store capability the sweep consumes — defined here at the consumer
// and satisfied by the one concrete *neo4j.Store.
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (deleted bool, err error)
}
```

Replace the body of `SweepOnce` (lines 69-90; the doc comment, lines 65-68, stays until Step 4) with this block. Only the delete call changes. The bool is not used yet, so it is discarded with `_`: writing `deleted, err :=` here does not compile, because `deleted` would be unused. The `errors.Is(err, engram.ErrNotFound)` branch and `pruned++` stay for now, so the pass still counts every row the call did not fail on.

```go
func (s *Sweeper) SweepOnce(ctx context.Context, now time.Time) (pruned int, err error) {
	cands, err := s.pruner.PruneCandidates(ctx, now.Add(-s.grace), s.batch)
	if err != nil {
		return 0, fmt.Errorf("sweep candidates: %w", err)
	}
	for _, m := range cands {
		if m.Pinned {
			continue
		}
		if s.decay.Retrievability(m, now) < s.hardFloor {
			if _, err := s.pruner.DeleteIfStale(ctx, m.ID, now.Add(-s.grace)); err != nil {
				if errors.Is(err, engram.ErrNotFound) {
					continue // deleted since the candidate read
				}
				s.log.Error("sweep: delete failed", "id", m.ID, "err", err)
				continue
			}
			pruned++
		}
	}
	return pruned, nil
}
```

In `sweep/sweep_test.go`, replace `panicPruner`'s `Delete` (line 172) with:

```go
func (panicPruner) DeleteIfStale(context.Context, engram.MemoryID, time.Time) (bool, error) {
	return false, nil
}
```

In `neo4j/store.go`, replace the import block (lines 3-10) with:

```go
import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)
```

and insert after `Delete` (after line 428 once the import is in; `Delete` was lines 410-427), with one blank line between:

```go
// DeleteIfStale deletes the memory unconditionally, as Delete does, and reports whether it did;
// an unknown id is (false, nil).
// shortcut: no stale re-check yet, so a row changed since the candidate read is still deleted, as
// before; the lock-first statement replaces this body in the next commit to this file.
func (s *Store) DeleteIfStale(ctx context.Context, id engram.MemoryID, _ time.Time) (bool, error) {
	if err := s.Delete(ctx, id); err != nil {
		if errors.Is(err, engram.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
```

Run: `go build ./... && go vet -tags integration ./sweep/ ./neo4j/ && go test -race ./sweep/ ./mock/ && gofmt -l sweep/ neo4j/`
Expected: build and vet silent (the vet compiles `sweep/sweep_integration_test.go`, which passes `*eneo4j.Store` to `New`), both packages `ok`, `gofmt` prints nothing.

- [ ] **Step 2: Write the failing tests**

In `TestSweepOnceAlreadyDeletedIsNotAnError`, replace its `p := ...` statement (lines 404-407) with the block below and leave the rest of the test as it is. `FakeStore.DeleteIfStale` cannot tell a row that is gone from one it kept; both are `(false, nil)`, which is what the store returns for a gone row.

```go
	// DeleteIfStale reports (false, nil) for a row that is already gone, as for one it kept.
	p := &mock.FakeStore{
		PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}},
		KeptIDs:    map[engram.MemoryID]bool{"a": true, "b": true},
	}
```

Then replace the import block (lines 3-18) with this one, which drops `"fmt"`: nothing else in the file uses it.

```go
import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)
```

Append:

```go
func TestSweepOnceKeptRowIsNotCounted(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	// b was pinned or reinforced after the candidate read, so the store keeps it.
	p := &mock.FakeStore{
		PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}},
		KeptIDs:    map[engram.MemoryID]bool{"b": true},
	}
	s := New(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(&logs, nil)))

	n, err := s.SweepOnce(context.Background(), statsT0)
	if n != 1 || err != nil {
		t.Fatalf("SweepOnce = (%d, %v), want (1, nil): a kept row is not counted", n, err)
	}
	if len(p.Deleted) != 2 {
		t.Errorf("attempted %d deletes, want 2", len(p.Deleted))
	}
	if got := strings.Count(logs.String(), "level=ERROR"); got != 0 {
		t.Errorf("%d error-level records, want 0 for a kept row; log: %q", got, logs.String())
	}
}

func TestSweepOnceUsesOneCutoff(t *testing.T) {
	t.Parallel()
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}}}
	s := statsSweeper(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}) // grace 30 days
	if _, err := s.SweepOnce(context.Background(), statsT0); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	want := statsT0.Add(-30 * 24 * time.Hour)
	if !p.PruneBefore.Equal(want) {
		t.Errorf("PruneCandidates cutoff = %v, want %v", p.PruneBefore, want)
	}
	if len(p.StaleBefore) != 2 {
		t.Fatalf("DeleteIfStale called %d times, want 2", len(p.StaleBefore))
	}
	for i, b := range p.StaleBefore {
		if !b.Equal(want) {
			t.Errorf("DeleteIfStale %d cutoff = %v, want %v", i, b, want)
		}
	}
}
```

- [ ] **Step 3: Run them and see the behaviour fail**

Run: `go test -race -run 'TestSweepOnce' -v ./sweep/`
Expected: FAIL in `TestSweepOnceKeptRowIsNotCounted` with `SweepOnce = (2, <nil>), want (1, nil): a kept row is not counted`, and in `TestSweepOnceAlreadyDeletedIsNotAnError` with `SweepOnce = (2, <nil>), want (0, nil)`. `TestSweepOnceUsesOneCutoff` and `TestSweepOnceOtherDeleteErrorStillLogged` pass already: they are guards that the next step keeps the cutoff and still logs a real error.

- [ ] **Step 4: Implement the pass**

In `sweep/sweep.go`, replace the import block (lines 6-15) with this one, which drops `"errors"`:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Fraancuus/engram"
)
```

Replace `Pruner` with its doc comment (lines 16-21 once the import has lost its line; lines 17-22 if you edit this block first) with:

```go
// Pruner is the narrow store capability the sweep consumes, defined here at the consumer and
// satisfied by *neo4j.Store and the fakes in mock. DeleteIfStale deletes a candidate only while
// it is still unpinned and last accessed before `before`, and reports whether it did.
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (deleted bool, err error)
}
```

Replace `SweepOnce` with its doc comment (lines 65-90 at the start of this step; recount after the two edits above) with the body fixed by design.md D2:

```go
// SweepOnce runs one prune pass as of now: it fetches candidates last accessed before now minus
// the grace period and deletes those whose retrievability is below the hard floor (pinned
// memories are skipped defensively). Each delete re-checks the same cutoff in the store, so a
// candidate pinned, reinforced or deleted since the read is kept, not counted and not logged.
// now is passed explicitly so the eval can virtualize time.
func (s *Sweeper) SweepOnce(ctx context.Context, now time.Time) (pruned int, err error) {
	before := now.Add(-s.grace)
	cands, err := s.pruner.PruneCandidates(ctx, before, s.batch)
	if err != nil {
		return 0, fmt.Errorf("sweep candidates: %w", err)
	}
	for _, m := range cands {
		if m.Pinned {
			continue
		}
		if s.decay.Retrievability(m, now) < s.hardFloor {
			deleted, err := s.pruner.DeleteIfStale(ctx, m.ID, before)
			if err != nil {
				s.log.Error("sweep: delete failed", "id", m.ID, "err", err)
				continue
			}
			if deleted {
				pruned++
			}
		}
	}
	return pruned, nil
}
```

- [ ] **Step 5: Run the package under the race detector**

Run: `go test -race -count=3 ./sweep/ && go build ./... && gofmt -l sweep/ neo4j/ && grep -n 'time\.Now()' sweep/sweep.go sweep/sweep_test.go`
Expected: `ok`, build silent, `gofmt` and the grep print nothing.

- [ ] **Step 6: Commit**

```bash
graphify update .
git add sweep/sweep.go sweep/sweep_test.go neo4j/store.go
git commit -m "fix(sweep): delete a candidate only while it is still stale" -m "Co-Authored-By: <model> <noreply@anthropic.com>"
```

- [ ] **Step 7: Eval gate: a stop rule, not a formality**

Run from Git Bash, at the repository root, one command at a time:

```bash
O=$(mktemp -d); echo "$O"
go run ./cmd/eval --ci > "$O/branch.txt"; echo "branch exit $?"
T=$(mktemp -d) && git archive origin/main | tar -x -C "$T" && (cd "$T" && go run ./cmd/eval --ci) > "$O/main.txt"; echo "main exit $?"; rm -rf "$T"
for f in branch main; do sed -E 's/[0-9.]+±[0-9.]+ ms//g; s/p50 +p95/p50 p95/; s/[[:space:]]+$//' "$O/$f.txt" > "$O/$f.cmp"; done
diff "$O/main.cmp" "$O/branch.cmp" && echo OUTPUTS-EQUAL
git diff origin/main -- eval/ cmd/eval/
grep -rn '\.Run(' eval/ cmd/eval/ --include=*.go | grep -vE '\bt\.Run\('
cat "$O/branch.txt"
```

The second `sed` expression is there because `WriteTable` aligns columns with a `tabwriter`: the spaces between the `p50` and `p95` headers depend on the width of the removed latency values.

Expected: both exits `0`, both outputs end with `eval gate passed`, `OUTPUTS-EQUAL` is printed, the `git diff` and the `grep` print nothing (no code under `eval/` or `cmd/eval/` calls `Run`; `eval/arm.go:215-217` still calls `SweepOnce` with the arm's virtual `now`). The second `grep` drops only calls on a receiver named exactly `t`, so a call such as `st.Run(` would still show. In `$O/branch.txt` the `type-aware` row has a mean `precision@5` of 0.179 and a mean `ndcg@5` of 0.875: those are the 0.1788 and 0.8747 of `eval/baseline.json` at the three decimals the report prints. Paste the A/B/C table from `$O/branch.txt` into the task report either way.

**If an exit is not 0, `diff` prints any line, the `git diff` or the `grep` prints anything, or the two type-aware figures are not 0.179 and 0.875: stop. Do not start Task 3. Report the full table of both runs and the `diff` output to the orchestrator, who reports to Franco.** Remove `$O` once the report is written.

---

### Task 3: Adapter: store (`neo4j/`)

**Files:**
- Modify: `neo4j/store.go` (the import block, lines 3-11, which loses the `errors` Task 2 added; the `DeleteIfStale` stub from Task 2, lines 430-442; `Reinforce`, lines 178-201 once the import is gone; `SetForgotten` and `Pin`, lines 376-387 once the import is gone)
- Create: `neo4j/stale_integration_test.go` (`//go:build integration`, `package neo4j_test`)

**Interfaces:**
- Consumes: the helpers of package `neo4j_test`: `testStore` (`neo4j/store_integration_test.go:27`), `uniqueID` (`:43`), `putAt` (`neo4j/decay_integration_test.go:18`, 384-dim embedding, explicit `last_accessed`), `putBare` (`neo4j/graph_integration_test.go:16`, `last_accessed` now), `rawCount` (`neo4j/mutate_integration_test.go:17`, reads the column `c`), `testDriver` (`neo4j/stats_integration_test.go:282`); `flagOne` and `recordValue` in `neo4j/store.go`. The prefixes `dis` and `race` are not used by any other test.
- Produces: the real `(*Store).DeleteIfStale`. `Reinforce`, `Pin` and `SetForgotten` keep their signatures and return `engram.ErrNotFound` for a row deleted while they waited. Task 4 relies on that for `Reinforce`.

**Precondition:** the stack is up (`docker compose ps` shows `neo4j` healthy) and `schema/001_init.cypher` is applied.

- [ ] **Step 1: Record the baseline**

Run: `go test -race -count=1 -tags integration -run 'TestStore(Reinforce|ReinforceNotFound|PinAndForget|Delete)$' -v ./neo4j/`
Expected: four `--- PASS`, no `SKIP`.

- [ ] **Step 2: Write the sequential test**

Create `neo4j/stale_integration_test.go` with this header and test. Step 6 adds `"fmt"` and `"sync"` to the imports and appends the rest of the file.

```go
//go:build integration

package neo4j_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// cleanupRow deletes the memory when the test ends; a row the test already deleted is fine.
func cleanupRow(t *testing.T, s *eneo4j.Store, id engram.MemoryID) {
	t.Helper()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
}

const sweepCheckQ = `MATCH (m:Memory {id: $id}) WHERE m.sweep_check IS NOT NULL RETURN count(m) AS c`

func TestStoreDeleteIfStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ns := engram.Namespace(string(uniqueID("itest-dis")))
	now := time.Now().UTC().Truncate(time.Millisecond)
	// The test's own cutoff, an hour ago. Its stale rows are at most two hours old: stale here,
	// never for a real sweep, whose grace is 30 days.
	cutoff := now.Add(-time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	for _, tc := range []struct {
		name        string
		lastAcc     time.Time
		write       func(t *testing.T, id engram.MemoryID) // lands after the candidate read
		wantDeleted bool
		check       func(t *testing.T, m engram.Memory) // on the kept row
	}{
		{name: "stale", lastAcc: twoHoursAgo, wantDeleted: true},
		{name: "fresh", lastAcc: now},
		{name: "pinned after the candidate read", lastAcc: twoHoursAgo,
			write: func(t *testing.T, id engram.MemoryID) {
				if err := s.Pin(ctx, id); err != nil {
					t.Fatalf("Pin: %v", err)
				}
			},
			check: func(t *testing.T, m engram.Memory) {
				if !m.Pinned {
					t.Error("kept row is not pinned")
				}
			}},
		{name: "reinforced after the candidate read", lastAcc: twoHoursAgo,
			write: func(t *testing.T, id engram.MemoryID) {
				if err := s.Reinforce(ctx, id, now); err != nil {
					t.Fatalf("Reinforce: %v", err)
				}
			},
			check: func(t *testing.T, m engram.Memory) {
				if !m.LastAccessed.Equal(now) {
					t.Errorf("last_accessed = %v, want the reinforce's %v", m.LastAccessed, now)
				}
			}},
		{name: "last_accessed equal to the cutoff", lastAcc: cutoff},
		{name: "one millisecond before the cutoff", lastAcc: cutoff.Add(-time.Millisecond), wantDeleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uniqueID("dis")
			putAt(t, s, id, ns, tc.lastAcc)
			cleanupRow(t, s, id)
			if tc.write != nil {
				tc.write(t, id)
			}

			deleted, err := s.DeleteIfStale(ctx, id, cutoff)
			if deleted != tc.wantDeleted || err != nil {
				t.Fatalf("DeleteIfStale = (%v, %v), want (%v, <nil>)", deleted, err, tc.wantDeleted)
			}
			m, err := s.Get(ctx, id)
			if tc.wantDeleted {
				if !errors.Is(err, engram.ErrNotFound) {
					t.Errorf("Get after the delete = %v, want ErrNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get kept row: %v", err)
			}
			if n := rawCount(t, sweepCheckQ, map[string]any{"id": string(id)}); n != 0 {
				t.Error("kept row carries a sweep_check property")
			}
			if tc.check != nil {
				tc.check(t, m)
			}
		})
	}

	t.Run("relationships go with the memory", func(t *testing.T) {
		x, y := uniqueID("dis-x"), uniqueID("dis-y")
		ent := string(uniqueID("dis-e"))
		d := testDriver(t)
		// Registered before the entity exists: Delete leaves entities behind.
		t.Cleanup(func() {
			_, _ = neo4jdriver.ExecuteQuery(context.Background(), d, `MATCH (e:Entity {id: $e}) DETACH DELETE e`,
				map[string]any{"e": ent}, neo4jdriver.EagerResultTransformer)
		})
		putAt(t, s, x, ns, twoHoursAgo)
		cleanupRow(t, s, x)
		putBare(t, s, y, ns)
		cleanupRow(t, s, y)
		for from, to := range map[engram.MemoryID]engram.MemoryID{x: y, y: x} { // both directions
			if err := s.Link(ctx, from, []engram.Link{{To: to, Weight: 0.9}}); err != nil {
				t.Fatalf("Link: %v", err)
			}
		}
		for _, id := range []engram.MemoryID{x, y} {
			if err := s.LinkEntities(ctx, id, []string{ent}); err != nil {
				t.Fatalf("LinkEntities: %v", err)
			}
		}

		deleted, err := s.DeleteIfStale(ctx, x, cutoff)
		if !deleted || err != nil {
			t.Fatalf("DeleteIfStale = (%v, %v), want (true, <nil>)", deleted, err)
		}
		if n := rawCount(t, `MATCH (:Memory {id: $y})-[r:LINKS]-() RETURN count(r) AS c`,
			map[string]any{"y": string(y)}); n != 0 {
			t.Errorf("%d LINKS edges left on the neighbour, want 0", n)
		}
		if n := rawCount(t, `MATCH (:Entity {id: $e})<-[r:MENTIONS]-() RETURN count(r) AS c`,
			map[string]any{"e": ent}); n != 1 {
			t.Errorf("%d MENTIONS edges on the entity, want 1 (the neighbour's)", n)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		deleted, err := s.DeleteIfStale(ctx, uniqueID("dis-missing"), cutoff)
		if deleted || err != nil {
			t.Fatalf("DeleteIfStale(unknown) = (%v, %v), want (false, <nil>)", deleted, err)
		}
	})
}
```

- [ ] **Step 3: Run it and see the behaviour fail**

Run: `go test -race -count=1 -tags integration -run 'TestStoreDeleteIfStale$' -v ./neo4j/`
Expected: FAIL in `fresh`, `pinned_after_the_candidate_read`, `reinforced_after_the_candidate_read` and `last_accessed_equal_to_the_cutoff`, each with `DeleteIfStale = (true, <nil>), want (false, <nil>)` (the stub deletes unconditionally). `stale`, `one_millisecond_before_the_cutoff`, `relationships_go_with_the_memory` and `unknown_id` pass.

- [ ] **Step 4: Implement the lock-first delete (D1)**

In `neo4j/store.go`, replace the import block (lines 3-11) with this one, which drops `"errors"`: nothing else in the file uses it once the stub is gone.

```go
import (
	"context"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)
```

Replace the Task 2 stub, comments included (lines 429-441 once the import has lost its line; lines 430-442 if you edit this block first), with the statement of design.md D1. Both parameters are bound; `before` is passed as UTC.

```go
// DeleteIfStale deletes the memory and its relationships only if it is still stale: unpinned,
// with last_accessed before `before`, the predicate PruneCandidates(before) uses. It reports
// whether it deleted. A row that changed since the sweep read it, or that is already gone, is
// (false, nil). The re-check is complete only while every write that raises retrievability
// sets last_accessed or pinned.
func (s *Store) DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (bool, error) {
	// The SET is there for its lock, and the REMOVE takes the property off again in the same
	// statement, so no row ever carries it. Neo4j takes a node's write lock when a statement
	// first writes to it, not when it matches it. Writing a dummy property first makes this
	// statement wait for any writer that got there earlier, and the WHERE after it then reads
	// committed values. Without the dummy write the WHERE reads the row before the lock, and
	// a row pinned or reinforced a moment ago is deleted anyway. The pair is not dead code: it
	// is the form the Neo4j Operations Manual gives in "Concurrent data access", "Lost
	// updates", example "Dummy property to acquire a write lock"
	// (https://neo4j.com/docs/operations-manual/current/database-internals/concurrent-data-access/).
	const q = `
MATCH (m:Memory {id: $id})
SET m.sweep_check = true
REMOVE m.sweep_check
WITH m
WHERE m.last_accessed < $before AND coalesce(m.pinned, false) = false
DETACH DELETE m
RETURN count(*) AS deleted`
	res, err := neo4jdriver.ExecuteQuery(ctx, s.driver, q,
		map[string]any{"id": string(id), "before": before.UTC()}, neo4jdriver.EagerResultTransformer)
	if err != nil {
		return false, fmt.Errorf("delete if stale %q: %w", id, err)
	}
	// count(*) always returns one row, so none is a malformed result, not a missing memory.
	if len(res.Records) == 0 {
		return false, fmt.Errorf("delete if stale %q: no result row", id)
	}
	n, err := recordValue[int64](res.Records[0], "deleted")
	if err != nil {
		return false, fmt.Errorf("delete if stale %q: %w", id, err)
	}
	return n > 0, nil
}
```

Run: `go build ./... && gofmt -l neo4j/`
Expected: build silent, `gofmt` prints nothing.

- [ ] **Step 5: Run the sequential test and the live sweep**

Run: `go test -race -count=1 -tags integration -run 'TestStoreDeleteIfStale$' -v ./neo4j/` then `go test -race -count=1 -tags integration -v ./sweep/`
Expected: `--- PASS: TestStoreDeleteIfStale` with every subtest passing; `--- PASS: TestSweepOncePrunesDecayedLive` (the real sweep now deletes through `DeleteIfStale`). No `SKIP`.

- [ ] **Step 6: Write the race test and the not-found guard**

Replace the import block of `neo4j/stale_integration_test.go` with this one, which adds `"fmt"` and `"sync"`:

```go
import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)
```

Then append:

```go
// staleRows creates n bare :Memory rows with access_count 0, last accessed at `at`, in one
// statement, and removes them by exact id when the test ends. They carry no embedding: this
// test never searches.
func staleRows(t *testing.T, d neo4jdriver.DriverWithContext, n int, at time.Time) []engram.MemoryID {
	t.Helper()
	base := string(uniqueID("race"))
	ids := make([]engram.MemoryID, n)
	strs := make([]string, n)
	for i := range ids {
		strs[i] = fmt.Sprintf("%s-%03d", base, i)
		ids[i] = engram.MemoryID(strs[i])
	}
	// Registered before the rows exist, so a failed create cannot leak them.
	t.Cleanup(func() {
		_, _ = neo4jdriver.ExecuteQuery(context.Background(), d, `MATCH (m:Memory) WHERE m.id IN $ids DETACH DELETE m`,
			map[string]any{"ids": strs}, neo4jdriver.EagerResultTransformer)
	})
	const q = `
UNWIND $ids AS id
CREATE (:Memory {id: id, namespace: $ns, type: 'episodic', content: id, source: '',
  importance: 0.5, stability: 1.0, access_count: 0, created_at: $at, last_accessed: $at,
  superseded: false, pinned: false, forgotten: false})`
	if _, err := neo4jdriver.ExecuteQuery(context.Background(), d, q,
		map[string]any{"ids": strs, "ns": base, "at": at.UTC()}, neo4jdriver.EagerResultTransformer); err != nil {
		t.Fatalf("create rows: %v", err)
	}
	return ids
}

// rowState is what a round left on its row.
type rowState struct {
	pinned       bool
	accessCount  int64
	lastAccessed time.Time
}

// rowStates reads, in one query, the rows among ids that still exist.
func rowStates(t *testing.T, d neo4jdriver.DriverWithContext, ids []engram.MemoryID) map[engram.MemoryID]rowState {
	t.Helper()
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = string(id)
	}
	const q = `
MATCH (m:Memory) WHERE m.id IN $ids
RETURN m.id AS id, coalesce(m.pinned, false) AS pinned, m.access_count AS access_count,
  m.last_accessed AS last_accessed`
	res, err := neo4jdriver.ExecuteQuery(context.Background(), d, q, map[string]any{"ids": strs}, neo4jdriver.EagerResultTransformer)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	out := make(map[engram.MemoryID]rowState, len(res.Records))
	for _, rec := range res.Records {
		rawID, _ := rec.Get("id")
		rawPinned, _ := rec.Get("pinned")
		rawAC, _ := rec.Get("access_count")
		rawAt, _ := rec.Get("last_accessed")
		id, ok1 := rawID.(string)
		pinned, ok2 := rawPinned.(bool)
		count, ok3 := rawAC.(int64)
		at, ok4 := rawAt.(time.Time)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			t.Fatalf("row %v: got %T, %T, %T, %T", rawID, rawID, rawPinned, rawAC, rawAt)
		}
		out[engram.MemoryID(id)] = rowState{pinned: pinned, accessCount: count, lastAccessed: at}
	}
	return out
}

// TestStoreWriterNeverAcknowledgesADeletedRow races a writer against the sweep's delete, 100
// rounds per pairing, on rows that are stale for this test only (two hours old, cutoff an hour
// ago). Every round must end in one of two states: the writer returned nil, DeleteIfStale
// returned false, and the row is there with the write applied; or the writer returned
// engram.ErrNotFound, DeleteIfStale returned true, and the row is gone. Both states must occur
// in each pairing, so a run in which the two calls never overlapped cannot pass. The test
// asserts no wall time: a deadlock retry costs about 2 s and is correct, so a time limit would
// only make it flaky. The context deadline is there only to stop a hang.
func TestStoreWriterNeverAcknowledgesADeletedRow(t *testing.T) {
	s := testStore(t)
	d := testDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	cutoff := now.Add(-time.Hour)
	const rounds = 100

	for _, tc := range []struct {
		name    string
		write   func(id engram.MemoryID) error
		applied func(r rowState) bool // the writer's change is on the kept row
		want    string                // what applied checks, for the failure message
	}{
		{"Reinforce", func(id engram.MemoryID) error { return s.Reinforce(ctx, id, now) },
			func(r rowState) bool { return r.accessCount == 1 && r.lastAccessed.Equal(now) }, // rows start at 0
			fmt.Sprintf("access_count 1 and last_accessed %v", now)},
		{"Pin", func(id engram.MemoryID) error { return s.Pin(ctx, id) },
			func(r rowState) bool { return r.pinned }, "pinned true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := staleRows(t, d, rounds, now.Add(-2*time.Hour))
			type outcome struct {
				writeErr error
				deleted  bool
				delErr   error
			}
			outs := make([]outcome, rounds)
			for i, id := range ids {
				// A 0 to 2 ms head start, given to the writer on even rounds and to the delete
				// on odd ones, so both orders occur as well as true overlaps.
				lag := time.Duration(i%3) * time.Millisecond
				writerFirst := i%2 == 0
				gate := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-gate
					if !writerFirst {
						time.Sleep(lag)
					}
					outs[i].writeErr = tc.write(id)
				}()
				go func() {
					defer wg.Done()
					<-gate
					if writerFirst {
						time.Sleep(lag)
					}
					outs[i].deleted, outs[i].delErr = s.DeleteIfStale(ctx, id, cutoff)
				}()
				close(gate)
				wg.Wait()
			}

			rows := rowStates(t, d, ids) // nothing touches a row after its round
			var kept, gone int
			for i, id := range ids {
				o := outs[i]
				row, exists := rows[id]
				switch {
				case o.writeErr == nil && o.delErr == nil && !o.deleted && exists && tc.applied(row):
					kept++
				case errors.Is(o.writeErr, engram.ErrNotFound) && o.delErr == nil && o.deleted && !exists:
					gone++
				default:
					t.Errorf("round %d: %s returned %v, DeleteIfStale returned (%v, %v), row exists=%v %+v; "+
						"want (nil, false, row with %s) or (ErrNotFound, true, no row)",
						i, tc.name, o.writeErr, o.deleted, o.delErr, exists, row, tc.want)
				}
			}
			t.Logf("%s: %d rounds kept the row with the write, %d deleted it, of %d", tc.name, kept, gone, rounds)
			if kept == 0 || gone == 0 {
				t.Errorf("%s: %d kept, %d deleted; both end states must occur, or the writer and the delete did not race",
					tc.name, kept, gone)
			}
		})
	}
}

func TestStoreWritersNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(id engram.MemoryID) error
	}{
		{"Reinforce", func(id engram.MemoryID) error { return s.Reinforce(ctx, id, time.Now().UTC()) }},
		{"Pin", func(id engram.MemoryID) error { return s.Pin(ctx, id) }},
		{"SetForgotten", func(id engram.MemoryID) error { return s.SetForgotten(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(uniqueID("dis-missing")); !errors.Is(err, engram.ErrNotFound) {
				t.Fatalf("%s(unknown id) = %v, want engram.ErrNotFound", tc.name, err)
			}
		})
	}
}
```

The local `rawAC` is named so because `rawCount` is the package helper from `neo4j/mutate_integration_test.go`.

- [ ] **Step 7: Run them and see the race**

Run: `go vet -tags integration ./neo4j/ && go test -race -count=1 -tags integration -run 'TestStore(WriterNeverAcknowledgesADeletedRow|WritersNotFound)$' -v ./neo4j/`
Expected: vet silent. `TestStoreWriterNeverAcknowledgesADeletedRow/Pin` FAILS with one or more lines of the form `round N: Pin returned <nil>, DeleteIfStale returned (true, <nil>), row exists=false {pinned:false accessCount:0 lastAccessed:0001-01-01 00:00:00 +0000 UTC}; want (nil, false, row with pinned true) or (ErrNotFound, true, no row)`: the old `MATCH ... SET ... RETURN count(m)` still counts a row deleted while it waited. `/Reinforce` is expected to fail the same way, with `round N: Reinforce returned <nil>, DeleteIfStale returned (true, <nil>), row exists=false ...`. Either pairing may also report `...: N kept, 0 deleted; both end states must occur, or the writer and the delete did not race`, because the old writers return `engram.ErrNotFound` only when the delete committed before their `MATCH`. The old `Reinforce` reads `access_count` before it writes, so some of its rounds may deadlock with the delete and take about 2 s while the driver retries; that is expected and is not judged. `TestStoreWritersNotFound` PASSES already: it guards that the new queries still map an absent row to `engram.ErrNotFound`. **If neither pairing reports a `round N:` line, stop and report the logged counts: the test does not reproduce the race and guards nothing.**

- [ ] **Step 8: Writers match the row again (D3)**

Three literal queries, no shared fragment. In `neo4j/store.go`, replace `Reinforce` with its doc comment (lines 178-201) with this block. The statement writes `last_accessed` first, matches the row again, and increments `access_count` on the re-matched row. The result mapping below the query does not change.

```go
// Reinforce records an access: it increments access_count and sets last_accessed to now.
// It returns engram.ErrNotFound if no memory has the given id, including one deleted while
// this call waited for its lock.
func (s *Store) Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error {
	// The statement writes before it reads. The SET of last_accessed takes the row's write
	// lock, so a row deleted while this call waited is gone at the second MATCH, the count is
	// 0, and the code below maps 0 to engram.ErrNotFound. The increment is on the re-matched
	// row, not in the first SET, because a statement that reads a property before it writes
	// deadlocks against the lock-first DeleteIfStale. The driver retries the deadlock, so the
	// call still succeeds, but it takes about 2 s instead of a few milliseconds. Any writer on
	// :Memory must write before it reads.
	const q = `
MATCH (m:Memory {id: $id})
SET m.last_accessed = $now
WITH m
MATCH (x:Memory {id: $id})
SET x.access_count = x.access_count + 1
RETURN count(x) AS c`
	res, err := neo4jdriver.ExecuteQuery(ctx, s.driver, q,
		map[string]any{"id": string(id), "now": now.UTC()}, neo4jdriver.EagerResultTransformer)
	if err != nil {
		return fmt.Errorf("reinforce %q: %w", id, err)
	}
	if len(res.Records) == 0 {
		return fmt.Errorf("reinforce %q: %w", id, engram.ErrNotFound)
	}
	n, err := recordValue[int64](res.Records[0], "c")
	if err != nil {
		return fmt.Errorf("reinforce %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("reinforce %q: %w", id, engram.ErrNotFound)
	}
	return nil
}
```

Replace `SetForgotten` and `Pin` with their doc comments (lines 376-387 at the start of this step if you edit them first; lines 387-398 once `Reinforce` above has grown by eleven lines) with this block. `flagOne` does not change.

```go
// SetForgotten soft-forgets the memory (excluded from default recall). Returns
// engram.ErrNotFound if no memory has the id, including one deleted while this call waited
// for its lock.
func (s *Store) SetForgotten(ctx context.Context, id engram.MemoryID) error {
	// The second MATCH runs once the SET holds the row's lock, so a row deleted while the SET
	// waited counts 0, which flagOne maps to engram.ErrNotFound.
	const q = `
MATCH (m:Memory {id: $id})
SET m.forgotten = true
WITH m
MATCH (x:Memory {id: $id})
RETURN count(x) AS c`
	return s.flagOne(ctx, "set forgotten", id, q)
}

// Pin protects the memory from decay. Returns engram.ErrNotFound if no memory has the id,
// including one deleted while this call waited for its lock.
func (s *Store) Pin(ctx context.Context, id engram.MemoryID) error {
	// The second MATCH runs once the SET holds the row's lock, so a row deleted while the SET
	// waited counts 0, which flagOne maps to engram.ErrNotFound.
	const q = `
MATCH (m:Memory {id: $id})
SET m.pinned = true
WITH m
MATCH (x:Memory {id: $id})
RETURN count(x) AS c`
	return s.flagOne(ctx, "pin", id, q)
}
```

- [ ] **Step 9: Run the store tests**

Run: `go test -race -count=3 -tags integration -run 'TestStore(WriterNeverAcknowledgesADeletedRow|WritersNotFound|DeleteIfStale|Reinforce|ReinforceNotFound|PinAndForget|Delete)$' -v ./neo4j/ && go build ./... && gofmt -l neo4j/`
Expected: every listed test passes in all three runs, no `SKIP`; no `round N:` line; for both pairings the race test logs `<name>: K rounds kept the row with the write, D deleted it, of 100` with K and D both above 0; `TestStoreReinforce` still sees the count and the timestamp it expects; build silent; `gofmt` prints nothing. Report the elapsed time `go test -v` prints for the race test; it is information, not a pass condition (the plan's dry run measured about 2 s for the 200 rounds).

- [ ] **Step 10: Check the store is clean**

Run (read-only):

```bash
printf '%s\n' \
  "MATCH (m:Memory) WHERE m.id STARTS WITH 'dis-' OR m.id STARTS WITH 'race-' OR m.sweep_check IS NOT NULL RETURN count(m) AS left;" \
  "MATCH (e:Entity) WHERE e.id STARTS WITH 'dis-e-' RETURN count(e) AS entities;" \
  | docker compose exec -T neo4j cypher-shell
```

Expected: `left` 0 and `entities` 0.

- [ ] **Step 11: Commit**

```bash
graphify update .
git add neo4j/store.go neo4j/stale_integration_test.go
git commit -m "fix(neo4j): lock before the stale re-check, and report a row deleted under a writer" -m "Co-Authored-By: <model> <noreply@anthropic.com>"
```

- [ ] **Step 12: Prove the race test guards the delete**

The sequential test cannot see a delete that reads before it locks; only the race test can (Review Focus, item 2). Prove on this machine that it does. This step comes after the commit so that the file can be restored exactly.

In `neo4j/store.go`, in the `DeleteIfStale` query only, delete the two lines `SET m.sweep_check = true` and `REMOVE m.sweep_check`. Change nothing else. Confirm with `git diff --stat neo4j/store.go` that the file lost exactly two lines.

Run: `go test -race -count=1 -tags integration -run 'TestStoreWriterNeverAcknowledgesADeletedRow$' -v ./neo4j/`
Expected: FAIL. Each pairing prints lines that name its own writer: `/Reinforce` prints `round N: Reinforce returned <nil>, DeleteIfStale returned (true, <nil>), row exists=false {...}; want (nil, false, row with access_count 1 and last_accessed ...) or (ErrNotFound, true, no row)`, and `/Pin` prints `round N: Pin returned <nil>, DeleteIfStale returned (true, <nil>), row exists=false {...}; want (nil, false, row with pinned true) or (ErrNotFound, true, no row)`. `/Pin` may also print `Pin: 0 kept, N deleted; both end states must occur...`. In the plan's dry run each pairing printed 51 to 60 `round N:` lines of 100, in three runs of three. **If the test passes with the two lines removed, stop and report: the test does not guard the lock-first delete, and design.md's mitigation for the query-plan risk does not hold.**

Then restore the file and prove it is the committed one:

```bash
git restore neo4j/store.go
git status --short neo4j/
go test -race -count=1 -tags integration -run 'TestStoreWriterNeverAcknowledgesADeletedRow$' -v ./neo4j/
```

Expected: `git status` prints nothing; the test passes. Then repeat Step 10; expected `left` 0 and `entities` 0. Nothing is committed in this step.

---

### Task 4: Adapter: handlers (`mcp/`)

**Files:**
- Modify: `mcp/remember.go` (the `doRemember` doc comment, lines 93-96; the dedup block inside `doRemember`, lines 169-183)
- Modify: `mcp/recall.go` (`reinforce` with its doc comment, lines 269-284)
- Test: `mcp/remember_test.go` (append; no new import)
- Test: `mcp/recall_test.go` (append; the import block, lines 3-14, gains `bytes`, `log/slog` and `slices`)

**Interfaces:**
- Consumes: `testHandlers` (`mcp/remember_test.go:22`), `validRemember` (`:43`), `cand` (`:103`, semantic, the content of `validRemember`), `vecEmbedder` (`:524`), `seqIDs` (`:531`), `fixedClock` (`:18`); `NewTools`, `WithIDGenerator`, `WithoutRerank` (`mcp/server.go:41-67`), `(*Tools).Remember` and `Recall` (`mcp/server.go:95`, `:100`); `mock.FakeStore{SearchResults, ReinforceErr, ReinforceErrs, Puts, Reinforced, Superseded, Propagated}` (Task 1), `mock.NewMemStore`; Task 3's guarantee that `Reinforce` returns `engram.ErrNotFound` for a row deleted under it.
- Produces: no signature change.

- [ ] **Step 1: Write the failing `remember` tests**

First replace `TestDoRememberDedupBoundaryUnchanged` (`mcp/remember_test.go`, lines 181-192) with this block. It is the guard for the spec scenario "Target present" and passes before and after this task. Until now it asserted only `Deduped`; the scenario also says which memory carries the content.

```go
func TestDoRememberDedupBoundaryUnchanged(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("live", defaultDedupThreshold, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "live" {
		t.Errorf("out = %+v, want deduped into live at exactly the threshold", out)
	}
	// The existing memory carries the content: it is reinforced and nothing new is stored.
	if !slices.Equal(st.Reinforced, []engram.MemoryID{"live"}) || len(st.Puts) != 0 {
		t.Errorf("reinforced=%v puts=%d, want [live] and no Put", st.Reinforced, len(st.Puts))
	}
}
```

Then append to `mcp/remember_test.go`:

```go
func TestDoRememberStoresContentWhenDedupTargetIsGone(t *testing.T) {
	t.Parallel()
	gone := fmt.Errorf("reinforce %q: %w", "gone", engram.ErrNotFound) // wrapped, as both stores return it
	for _, tc := range []struct {
		name           string
		cands          []engram.RecallResult
		supersedes     []string
		wantSuperseded []string
		wantStoreSup   string // fmt.Sprint of FakeStore.Superseded
	}{
		{name: "target gone", cands: []engram.RecallResult{cand("gone", 0.97, false)},
			wantStoreSup: "[]"},
		// The correction dedups into its own earlier insert, which the sweep deleted.
		{name: "target gone on a correction", cands: []engram.RecallResult{cand("gone", 0.99, false)},
			supersedes: []string{"old"}, wantSuperseded: []string{"old"}, wantStoreSup: "[[old]]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{SearchResults: tc.cands, ReinforceErrs: map[engram.MemoryID]error{"gone": gone}}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			in := validRemember()
			in.Supersedes = tc.supersedes
			out, err := h.doRemember(context.Background(), in)
			if err != nil {
				t.Fatalf("doRemember: %v; want the content stored when the dedup target is gone", err)
			}
			if out.Deduped || out.MemoryID != "test-id" {
				t.Errorf("out = %+v, want the new id test-id and deduped false", out)
			}
			if len(st.Puts) != 1 || st.Puts[0].Content != in.Content {
				t.Errorf("Puts = %+v, want one memory holding %q", st.Puts, in.Content)
			}
			if !slices.Equal(out.Superseded, tc.wantSuperseded) || fmt.Sprint(st.Superseded) != tc.wantStoreSup {
				t.Errorf("superseded: reply %v, store %v; want %v, %s", out.Superseded, st.Superseded, tc.wantSuperseded, tc.wantStoreSup)
			}
		})
	}
}

func TestDoRememberTargetGoneBesideSecondCandidate(t *testing.T) {
	t.Parallel()
	// Only the first candidate is gone. A handler that walked on would dedup into other.
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{cand("gone", 0.97, false), cand("other", 0.96, false)},
		ReinforceErrs: map[engram.MemoryID]error{"gone": fmt.Errorf("reinforce %q: %w", "gone", engram.ErrNotFound)},
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v; want the content stored when the dedup target is gone", err)
	}
	if out.Deduped || out.MemoryID != "test-id" {
		t.Errorf("out = %+v, want the new id test-id and deduped false", out)
	}
	if len(st.Puts) != 1 || st.Puts[0].Content != in.Content {
		t.Errorf("Puts = %+v, want one memory holding %q", st.Puts, in.Content)
	}
	if !slices.Equal(st.Reinforced, []engram.MemoryID{"gone"}) {
		t.Errorf("Reinforced = %v, want [gone]: the second candidate is not reinforced", st.Reinforced)
	}
}

func TestDoRememberReinforceFailureStillFails(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{cand("existing", 0.97, false)},
		ReinforceErr:  errors.New("db-down"),
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	_, err := h.doRemember(context.Background(), in)
	if err == nil || err.Error() != "remember: store unavailable" {
		t.Fatalf("err = %v, want remember: store unavailable", err)
	}
	if len(st.Puts) != 0 || len(st.Superseded) != 0 {
		t.Errorf("puts=%d superseded=%v, want neither: the store refused the write", len(st.Puts), st.Superseded)
	}
}

// sweptStore deletes victim just before Reinforce reaches it, as a sweep pass that wins the race
// between remember's search and its reinforce would. It simulates the race on a real MemStore;
// it does not script results.
type sweptStore struct {
	*mock.MemStore
	victim engram.MemoryID
}

func (s *sweptStore) Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error {
	if id == s.victim {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
		s.victim = ""
	}
	return s.MemStore.Reinforce(ctx, id, now)
}

func TestRememberDedupTargetGoneIsRecallable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &sweptStore{MemStore: mock.NewMemStore()}
	emb := vecEmbedder{"fact": {1, 0, 0}}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, fixedClock{}, WithIDGenerator(seqIDs()), WithoutRerank())
	first, err := h.Remember(ctx, RememberInput{Content: "fact", Type: "semantic", Namespace: "ns"})
	if err != nil {
		t.Fatalf("first remember: %v", err)
	}
	st.victim = engram.MemoryID(first.MemoryID)
	out, err := h.Remember(ctx, RememberInput{Content: "fact", Type: "semantic", Namespace: "ns"})
	if err != nil {
		t.Fatalf("remember after its dedup target was deleted: %v", err)
	}
	if out.Deduped || out.MemoryID == "" || out.MemoryID == first.MemoryID {
		t.Fatalf("out = %+v, want a new memory, not deduped into %s", out, first.MemoryID)
	}
	if m, ok := st.Snapshot(engram.MemoryID(out.MemoryID)); !ok || m.Content != "fact" {
		t.Errorf("stored %+v (found %v), want the content fact", m, ok)
	}
	rec, err := h.Recall(ctx, RecallInput{Query: "fact", Namespaces: []string{"ns"}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(rec.Results) != 1 || rec.Results[0].ID != out.MemoryID {
		t.Errorf("recall = %+v, want only the new memory %s", rec.Results, out.MemoryID)
	}
}
```

- [ ] **Step 2: Run them and see the behaviour fail**

Run: `go test -race -run 'TestDoRememberStoresContentWhenDedupTargetIsGone|TestDoRememberTargetGoneBesideSecondCandidate|TestDoRememberReinforceFailureStillFails|TestRememberDedupTargetGoneIsRecallable|TestDoRememberDedupBoundaryUnchanged' -v ./mcp/`
Expected: both subtests of `TestDoRememberStoresContentWhenDedupTargetIsGone` and `TestDoRememberTargetGoneBesideSecondCandidate` FAIL with `doRemember: remember: store unavailable; want the content stored when the dedup target is gone`, and `TestRememberDedupTargetGoneIsRecallable` FAILS with `remember after its dedup target was deleted: remember: store unavailable`. `TestDoRememberReinforceFailureStillFails` and `TestDoRememberDedupBoundaryUnchanged` (a target at exactly 0.95 that still exists dedups) PASS already: they are the guards. The `ERROR remember: reinforce failed` line on stderr comes from `NewTools`' default logger and is expected.

- [ ] **Step 3: Fall through to the insert path (D4)**

In `mcp/remember.go`, replace the `doRemember` doc comment (lines 93-96) with this one. It adds the third sentence; the other sentences are unchanged, only rewrapped.

```go
// doRemember validates the input, deduplicates within the namespace (reinforcing an
// existing near-identical memory instead of inserting), and otherwise stores a new
// memory with its entity links. A dedup target deleted between the search and its
// reinforce does not fail the write: the content is stored as a new memory. Internal
// failures are logged and returned as a generic error so nothing internal leaks to the
// caller.
```

Then, inside `doRemember`, replace the dedup block (lines 171-185 once the doc comment above has grown by two lines; lines 169-183 if you edit this block first), from `if target, ok := t.dedupTarget(` to its closing brace, with the switch the design fixes. The existing comment on the entity merge moves into the `err == nil` case and gains the sentence on why the sweep keeps the row. The insert path after the block does not change.

```go
	if target, ok := t.dedupTarget(candidates, replaces, mt, in.Content); ok {
		err := t.store.Reinforce(ctx, target.ID, t.clock.Now())
		switch {
		case err == nil:
			// Reinforce has committed last_accessed = now, so the sweep's DeleteIfStale keeps
			// the row while the entities are linked. Merge any supplied entities onto the
			// existing memory so a retry after a prior LinkEntities failure (or genuinely new
			// mentions) still records them.
			if len(in.Entities) > 0 {
				if err := t.store.LinkEntities(ctx, target.ID, in.Entities); err != nil {
					t.log.Error("remember: link entities on dedup failed", "err", err)
					return RememberOutput{}, errors.New("remember: store unavailable")
				}
			}
			return RememberOutput{MemoryID: string(target.ID), Deduped: true, Superseded: t.applySupersedes(ctx, replaces)}, nil
		case errors.Is(err, engram.ErrNotFound):
			// The target was deleted since the search. Fall through and store the content as a
			// new memory: failing here would lose it.
		default:
			t.log.Error("remember: reinforce failed", "err", err)
			return RememberOutput{}, errors.New("remember: store unavailable")
		}
	}
```

- [ ] **Step 4: Run the `remember` tests**

Run: `go test -race -run 'Remember' ./mcp/ && gofmt -l mcp/`
Expected: `ok`, including every existing `TestDoRemember*` and `TestRemember*` test and `TestDoRememberReinforceError` in `mcp/review_test.go`; `gofmt` prints nothing.

- [ ] **Step 5: Write the failing `recall` test**

Replace the import block of `mcp/recall_test.go` (lines 3-14) with this one, which adds `bytes`, `log/slog` and `slices`:

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)
```

Then append:

```go
func TestRecallToleratesDeletedHit(t *testing.T) {
	t.Parallel()
	gone := func(id string) error { return fmt.Errorf("reinforce %q: %w", id, engram.ErrNotFound) }
	for _, tc := range []struct {
		name           string
		errs           map[engram.MemoryID]error
		wantErrRecords int
		wantPropagated []engram.MemoryID
	}{
		{"second hit gone", map[engram.MemoryID]error{"s2": gone("s2")}, 0, []engram.MemoryID{"s1", "s3"}},
		{"every hit gone", map[engram.MemoryID]error{"s1": gone("s1"), "s2": gone("s2"), "s3": gone("s3")}, 0, nil},
		{"another error is still logged", map[engram.MemoryID]error{"s2": errors.New("db-down")}, 1, []engram.MemoryID{"s1", "s3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			st := &mock.FakeStore{
				SearchResults: []engram.RecallResult{
					{Memory: engram.Memory{ID: "s1", Content: "a"}, Score: 0.9},
					{Memory: engram.Memory{ID: "s2", Content: "b"}, Score: 0.8},
					{Memory: engram.Memory{ID: "s3", Content: "c"}, Score: 0.7},
				},
				ReinforceErrs: tc.errs,
			}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			h.reranker = nil // a FakeReranker without scores logs an error of its own
			h.log = slog.New(slog.NewTextHandler(&logs, nil))

			out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
			if err != nil {
				t.Fatalf("doRecall: %v", err)
			}
			var got []string
			for _, r := range out.Results {
				got = append(got, r.ID)
			}
			if !slices.Equal(got, []string{"s1", "s2", "s3"}) {
				t.Errorf("results = %v, want [s1 s2 s3] in that order", got)
			}
			if n := strings.Count(logs.String(), "level=ERROR"); n != tc.wantErrRecords {
				t.Errorf("%d error-level records, want %d; log: %q", n, tc.wantErrRecords, logs.String())
			}
			if !slices.Equal(st.Propagated, tc.wantPropagated) {
				t.Errorf("PropagateReinforce ids = %v, want %v", st.Propagated, tc.wantPropagated)
			}
		})
	}
}
```

- [ ] **Step 6: Run it and see the behaviour fail**

Run: `go test -race -run TestRecallToleratesDeletedHit -v ./mcp/`
Expected: `second_hit_gone` FAILS with `1 error-level records, want 0; log: "... level=ERROR msg=\"recall: reinforce failed\" id=s2 ..."`, and `every_hit_gone` with `3 error-level records, want 0; log: ...`. The `PropagateReinforce ids` and `results` assertions pass already (today's code also skips propagation after a failed reinforce). `another_error_is_still_logged` PASSES already: it guards that a real error keeps its error-level line.

- [ ] **Step 7: Log a vanished hit at debug level (D5)**

In `mcp/recall.go`, replace `reinforce` with its doc comment (lines 269-284) with this block. The doc comment keeps its text and gains the last sentence; the not-found case logs with `t.log.Debug` and skips the propagation; any other error keeps the existing error-level log. The hit stays in the reply.

```go
// reinforce records access on the returned memories (last_accessed/access_count) and
// spreads a freshness bump to their strong-edge neighbors. It runs synchronously before the
// response is sent — a deliberate v1 tradeoff (correctness over latency; a detached, batched
// write is a future optimization). A reinforce failure is logged and skipped so it never
// fails an otherwise-successful recall. A hit deleted since the search (engram.ErrNotFound)
// is logged at debug level only and gets no propagation: recall read it before it went.
func (t *Tools) reinforce(ctx context.Context, results []engram.RecallResult, now time.Time) {
	for _, r := range results {
		if err := t.store.Reinforce(ctx, r.ID, now); err != nil {
			if errors.Is(err, engram.ErrNotFound) {
				// The sweep or a hard forget removed the row after the search: a benign race.
				// Debug and not silence, so a store that starts returning not-found for live
				// rows still shows when the log level allows debug.
				t.log.Debug("recall: hit deleted before its reinforce", "id", r.ID)
				continue
			}
			t.log.Error("recall: reinforce failed", "id", r.ID, "err", err)
			continue
		}
		if err := t.store.PropagateReinforce(ctx, r.ID, propagateThreshold, now); err != nil {
			t.log.Error("recall: propagate failed", "id", r.ID, "err", err)
		}
	}
}
```

- [ ] **Step 8: Run the package**

Run: `go test -race ./mcp/ && gofmt -l mcp/`
Expected: `ok`, and `gofmt` prints nothing.

- [ ] **Step 9: Commit**

```bash
graphify update .
git add mcp/remember.go mcp/recall.go mcp/remember_test.go mcp/recall_test.go
git commit -m "fix(mcp): keep remember's content when its dedup target is gone" -m "Co-Authored-By: <model> <noreply@anthropic.com>"
```

---

### Task 5: Docs

**Files:**
- Modify: `CONTRIBUTING.md` (after the paragraph on integration fixtures, lines 71-73)

**Interfaces:** none.

- [ ] **Step 1: `CONTRIBUTING.md`**

After the paragraph that ends `...hard-deletes anything older that has decayed.` (line 73), add a blank line and this paragraph:

```markdown
`TestStoreWriterNeverAcknowledgesADeletedRow` in `neo4j/` is the only guard of the lock-first
`DeleteIfStale` and of the re-match in `Reinforce` and `Pin` (`SetForgotten` has the same
statement shape as `Pin` and is not raced: a soft forget does not keep a row from the sweep). CI
does not run integration tests, so rerun it whenever the Neo4j image in `docker-compose.yml`
changes.
```

- [ ] **Step 2: Check the line and the old behaviour**

Run: `grep -n "DeleteIfStale" CONTRIBUTING.md` then `grep -rn -i -E "store unavailable|unconditional" README.md docs/demo.md docs/engram-prd-v1.md`
Expected: the first prints the new line; the second prints nothing (it printed nothing on 2026-11-21, so no doc says a vanished dedup target is an error). If the second prints a line, read it and report whether it describes the old `remember` behaviour.

- [ ] **Step 3: Commit**

```bash
git add CONTRIBUTING.md
git commit -m "docs: name the test that guards the lock-first delete and the writers' re-match" -m "Co-Authored-By: <model> <noreply@anthropic.com>"
```

---

### Task 6: Verify

The controller runs this task. **Files:** none modified. Results go to `openspec/changes/sweep-delete-recheck/verify.md` in the verify phase.

- [ ] **Step 1: The CI gates**

Run, one at a time (memory is short):
`go build ./...`
`go test -race -coverprofile=cover.out ./...`
`golangci-lint run ./...`
`graphify update .`
Expected: build silent, every package `ok` (`./eval` takes five to six and a half minutes), `0 issues`.

- [ ] **Step 2: Integration**

From Git Bash with the stack up (`docker compose up -d --wait`) and the schema applied:
`go test -race -count=1 -tags integration -p 2 -v $(go list ./... | grep -v 'engram/eval$')`
Expected: every package `ok`, zero `--- FAIL`, no `SKIP` line naming `unavailable`. The two `ENGRAM_EVAL_SMOKE` skips do not apply here, since `./eval` is excluded; `grep -v '/eval$'` would also drop `cmd/eval`. Then repeat Task 3 Step 10; expected `left` 0 and `entities` 0.

- [ ] **Step 3: Eval gate on the final tree**

Repeat Task 2 Step 7 on `HEAD`. Expected: as there. Report the A/B/C table. Any difference: stop and report.

- [ ] **Step 4: Spec coverage**

Every scenario under `openspec/changes/sweep-delete-recheck/specs/` maps to a test above:

| Spec | Scenario | Test |
|---|---|---|
| memory-store-fake | Compile-time conformance | `mock/memstore_conformance_test.go` (unchanged; it now checks `DeleteIfStale` through `sweep.Pruner`) |
| memory-store-fake | Concurrent recall and sweep | `TestMemStoreConcurrentSearchDelete` (extended with `Reinforce` and `DeleteIfStale`) |
| memory-store-fake | Concurrent stats and delete | `TestMemStoreStatsConcurrent` (unchanged) |
| memory-store-fake | Delete cascades | `TestMemStoreDeleteCascades` (unchanged) |
| memory-store-fake | Prune predicate at the boundary | `TestMemStorePruneBoundary` (unchanged) |
| memory-store-fake | Pinned never a candidate | `TestMemStorePrunePinned` (unchanged) |
| memory-store-fake | Propagation skips superseded neighbors | `TestMemStorePropagateSkipsSuperseded` (unchanged) |
| memory-store-fake | DeleteIfStale cascades like Delete | `TestMemStoreDeleteIfStale/stale_row_deleted_with_its_link_and_mention` |
| memory-store-fake | DeleteIfStale at the boundary, at fixed virtual time | `TestMemStoreDeleteIfStale/LastAccessed_equal_to_before_kept` |
| memory-store-fake | DeleteIfStale keeps a pinned or reinforced memory | `TestMemStoreDeleteIfStale/pinned_kept`, `/reinforced_kept` |
| memory-store-fake | DeleteIfStale on an unknown id | `TestMemStoreDeleteIfStale/unknown_id` |
| sweep-schedule | One cutoff per pass, at fixed virtual time | `TestSweepOnceUsesOneCutoff` |
| sweep-schedule | A kept candidate is not counted | `TestSweepOnceKeptRowIsNotCounted` |
| sweep-schedule | Pinned after the candidate read, at fixed virtual time | `TestStoreDeleteIfStale/pinned_after_the_candidate_read` |
| sweep-schedule | Reinforced after the candidate read | `TestStoreDeleteIfStale/reinforced_after_the_candidate_read` |
| sweep-schedule | Cutoff boundary | `TestStoreDeleteIfStale/last_accessed_equal_to_the_cutoff`, `/one_millisecond_before_the_cutoff` |
| sweep-schedule | Nothing left on a kept memory | `TestStoreDeleteIfStale`, the `sweep_check` count on every kept row |
| sweep-schedule | Relationships go with the memory | `TestStoreDeleteIfStale/relationships_go_with_the_memory` |
| sweep-schedule | Unknown id | `TestStoreDeleteIfStale/unknown_id` |
| sweep-schedule | Every candidate already gone, at fixed virtual time | `TestSweepOnceAlreadyDeletedIsNotAnError` (rewritten) |
| sweep-schedule | Another delete error is still reported | `TestSweepOnceOtherDeleteErrorStillLogged` (unchanged) |
| sweep-schedule | Eval sweeps on virtual time only | Task 2 Step 7 and Step 3 here: the `git diff` and the `.Run(` grep |
| sweep-schedule | Gate with zero drift | Task 2 Step 7 and Step 3 here |
| write-race-safety | Unknown id | `TestStoreWritersNotFound`, `TestStoreReinforceNotFound` |
| write-race-safety | No acknowledged write is lost, over many rounds | `TestStoreWriterNeverAcknowledgesADeletedRow/Reinforce`, `/Pin`: every round in one of the two states, both states in each pairing |
| write-race-safety | Target gone, content stored | `TestDoRememberStoresContentWhenDedupTargetIsGone/target_gone`, `TestRememberDedupTargetGoneIsRecallable` |
| write-race-safety | Target gone on a correction | `TestDoRememberStoresContentWhenDedupTargetIsGone/target_gone_on_a_correction` |
| write-race-safety | Target gone beside a second live candidate | `TestDoRememberTargetGoneBesideSecondCandidate` |
| write-race-safety | Target present | `TestDoRememberDedupBoundaryUnchanged` (strengthened in Task 4 Step 1: id, no `Put`, `Reinforced`) |
| write-race-safety | Reinforce fails for another reason | `TestDoRememberReinforceFailureStillFails` |
| write-race-safety | Hit deleted before its reinforce, at fixed virtual time | `TestRecallToleratesDeletedHit/second_hit_gone` |
| write-race-safety | Every hit deleted | `TestRecallToleratesDeletedHit/every_hit_gone` |

Confirm each listed test ran and passed in Steps 1 and 2.

- [ ] **Step 5: Project agents**

`reviewer` (opus) on `git diff origin/main...feat/sweep-delete-recheck -- '*.go'`; `security-auditor` (fable, pinned in its frontmatter) on the queries in `neo4j/store.go` and on `mcp/remember.go` and `mcp/recall.go`; and a skeptic review of the whole branch (opus) that tries to refute each finding of the other two, because `remember` is a write path. Resolve every blocking finding before `/opsx:verify`.
