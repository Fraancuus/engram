//go:build integration

package neo4j_test

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

		// The setup gives x three edges: x to y, y to x, and x to the entity.
		if n := rawCount(t, `MATCH (:Memory {id: $x})-[r]-() RETURN count(r) AS c`,
			map[string]any{"x": string(x)}); n != 3 {
			t.Fatalf("%d relationships on x before the delete, want 3", n)
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
// in each pairing, which shows that both orders happened. It does not show that the calls
// overlapped at the server: rounds that alternate without overlapping also give both states.
// Only a failing run against a delete without its dummy write, or against the old writers,
// shows overlap (design D7). The test asserts no wall time: a deadlock retry costs about 2 s
// and is correct, so a time limit would only make it flaky. Each round has its own 30 s
// context, there only to stop a hang, so many correct retries across the 200 rounds cannot
// fail the test on time.
func TestStoreWriterNeverAcknowledgesADeletedRow(t *testing.T) {
	s := testStore(t)
	d := testDriver(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	cutoff := now.Add(-time.Hour)
	const rounds = 100

	for _, tc := range []struct {
		name    string
		write   func(ctx context.Context, id engram.MemoryID) error
		applied func(r rowState) bool // the writer's change is on the kept row
		want    string                // what applied checks, for the failure message
	}{
		{"Reinforce", func(ctx context.Context, id engram.MemoryID) error { return s.Reinforce(ctx, id, now) },
			func(r rowState) bool { return r.accessCount == 1 && r.lastAccessed.Equal(now) }, // rows start at 0
			fmt.Sprintf("access_count 1 and last_accessed %v", now)},
		{"Pin", func(ctx context.Context, id engram.MemoryID) error { return s.Pin(ctx, id) },
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
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				gate := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-gate
					if !writerFirst {
						time.Sleep(lag)
					}
					outs[i].writeErr = tc.write(ctx, id)
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
				cancel()
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
				t.Errorf("%s: %d kept, %d deleted; both end states must occur, or one side always won",
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
