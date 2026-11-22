package sweep

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

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func testSweeper(p Pruner, d engram.DecayModel) *Sweeper {
	return New(p, d, fixedClock{}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSweepOncePrunesBelowFloor(t *testing.T) {
	t.Parallel()
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}}}
	s := testSweeper(p, mock.FakeDecay{R: 0}) // R=0 < hardFloor(0.02)
	n, err := s.SweepOnce(context.Background(), fixedClock{}.Now())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != 2 || len(p.Deleted) != 2 {
		t.Errorf("pruned %d (deleted %v), want 2", n, p.Deleted)
	}
}

func TestSweepOnceKeepsAboveFloor(t *testing.T) {
	t.Parallel()
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
	s := testSweeper(p, mock.FakeDecay{R: 1}) // R=1 >= hardFloor
	n, err := s.SweepOnce(context.Background(), fixedClock{}.Now())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != 0 || len(p.Deleted) != 0 {
		t.Errorf("pruned %d, want 0 (above floor)", n)
	}
}

func TestSweepOnceSkipsPinned(t *testing.T) {
	t.Parallel()
	// PruneCandidates already excludes pinned; this pins the defensive in-loop check too.
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "pinned", Pinned: true}}}
	s := testSweeper(p, mock.FakeDecay{R: 0})
	n, err := s.SweepOnce(context.Background(), fixedClock{}.Now())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != 0 || len(p.Deleted) != 0 {
		t.Errorf("pinned memory pruned: n=%d deleted=%v", n, p.Deleted)
	}
}

func TestSweepOnceErrorsPropagate(t *testing.T) {
	t.Parallel()
	p := &mock.FakeStore{PruneErr: errors.New("boom")}
	if _, err := testSweeper(p, mock.FakeDecay{R: 0}).SweepOnce(context.Background(), fixedClock{}.Now()); err == nil {
		t.Error("want error when PruneCandidates fails")
	}
	// A Delete failure is best-effort: logged and skipped, not fatal — the sweep continues.
	p2 := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}, DeleteErr: errors.New("boom")}
	n, err := testSweeper(p2, mock.FakeDecay{R: 0}).SweepOnce(context.Background(), fixedClock{}.Now())
	if err != nil {
		t.Errorf("Delete failure should not fail the sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned = %d, want 0 (delete failed)", n)
	}
}

func TestSweepOnceAtFloorKeeps(t *testing.T) {
	t.Parallel()
	// The prune condition is strictly < hardFloor(0.02), so a memory at exactly the floor
	// is kept.
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
	n, err := testSweeper(p, mock.FakeDecay{R: 0.02}).SweepOnce(context.Background(), fixedClock{}.Now())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != 0 || len(p.Deleted) != 0 {
		t.Errorf("memory at exactly the floor pruned: n=%d", n)
	}
}

func TestNewGuardsConstruction(t *testing.T) {
	t.Parallel()
	// nil log + interval/batch <= 0 are defaulted at construction; the delete-error log path
	// must not panic on a nil logger.
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}, DeleteErr: errors.New("boom")}
	sw := New(p, mock.FakeDecay{R: 0}, fixedClock{}, 0, time.Hour, 0.02, 0, nil)
	if _, err := sw.SweepOnce(context.Background(), fixedClock{}.Now()); err != nil {
		t.Fatalf("SweepOnce with defaulted config: %v", err)
	}
}

// A superseded semantic memory decays on the fast curve (S=2 d), so once past
// the grace period it is pruned while its live twin (S=60 d) survives.
func TestSweepPrunesSupersededSemantic(t *testing.T) {
	t.Parallel()
	day0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lastAccessed := day0.AddDate(0, 0, 10) // day 10 for both memories
	const grace = 30 * 24 * time.Hour
	const floor = 0.02
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		day      int
		wantGone bool
	}{
		{"day 40 is inside grace: last access is not before now-30d", 40, false},
		{"day 41 is past grace: R=exp(-31/2) is below floor", 41, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := mock.NewMemStore()
			for _, m := range []engram.Memory{
				{ID: "stale", Namespace: "ns", Type: engram.Semantic, LastAccessed: lastAccessed},
				{ID: "live", Namespace: "ns", Type: engram.Semantic, LastAccessed: lastAccessed},
			} {
				if err := store.Put(ctx, m); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Supersede(ctx, []engram.MemoryID{"stale"}); err != nil {
				t.Fatal(err)
			}
			s := New(store, engram.TypeAwareDecay{}, fixedClock{}, time.Hour, grace, floor, 100,
				slog.New(slog.NewTextHandler(io.Discard, nil)))
			n, err := s.SweepOnce(ctx, day0.AddDate(0, 0, tc.day))
			if err != nil {
				t.Fatalf("SweepOnce: %v", err)
			}
			_, staleKept := store.Snapshot("stale")
			_, liveKept := store.Snapshot("live")
			if staleKept == tc.wantGone || !liveKept {
				t.Errorf("pruned %d: stale kept=%v (want %v), live kept=%v (want true)", n, staleKept, !tc.wantGone, liveKept)
			}
		})
	}
}

// stepClock is a settable virtual clock; the mutex keeps it race-clean.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *stepClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type panicPruner struct{}

func (panicPruner) PruneCandidates(context.Context, time.Time, int) ([]engram.Memory, error) {
	panic("boom")
}
func (panicPruner) DeleteIfStale(context.Context, engram.MemoryID, time.Time) (bool, error) {
	return false, nil
}

var statsT0 = time.Unix(1_700_000_000, 0).UTC()

func statsSweeper(p Pruner, d engram.DecayModel, c engram.Clock) *Sweeper {
	return New(p, d, c, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestStatsBeforeFirstTick(t *testing.T) {
	t.Parallel()
	got := testSweeper(&mock.FakeStore{}, mock.FakeDecay{}).Stats()
	if got != (engram.SweepStats{}) {
		t.Errorf("Stats() = %+v, want zero", got)
	}
}

func TestTickAccumulatesAtVirtualTime(t *testing.T) {
	t.Parallel()
	clk := &stepClock{t: statsT0}
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}}}
	s := statsSweeper(p, mock.FakeDecay{R: 0}, clk)
	s.tick(context.Background())
	clk.set(statsT0.Add(time.Hour))
	p.PruneCands = nil
	s.tick(context.Background())
	want := engram.SweepStats{LastRunAt: statsT0.Add(time.Hour), LastPruned: 0, TotalPruned: 2}
	if got := s.Stats(); got != want {
		t.Errorf("Stats() = %+v, want %+v", got, want)
	}
}

func TestTickPruneBoundaryRecorded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    float64
		want int
	}{{"at floor", 0.02, 0}, {"just below", 0.0199, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
			s := statsSweeper(p, mock.FakeDecay{R: tc.r}, &stepClock{t: statsT0})
			s.tick(context.Background())
			want := engram.SweepStats{LastRunAt: statsT0, LastPruned: tc.want, TotalPruned: tc.want}
			if got := s.Stats(); got != want {
				t.Errorf("Stats() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestTickFailureLeavesStats(t *testing.T) {
	t.Parallel()
	clk := &stepClock{t: statsT0}
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
	s := statsSweeper(p, mock.FakeDecay{R: 0}, clk)
	s.tick(context.Background())
	before := s.Stats()
	clk.set(statsT0.Add(time.Hour))
	p.PruneErr = errors.New("boom")
	s.tick(context.Background())
	if got := s.Stats(); got != before || before.TotalPruned != 1 {
		t.Errorf("Stats() = %+v, want unchanged %+v (TotalPruned 1)", got, before)
	}
}

func TestTickPanicLeavesStats(t *testing.T) {
	t.Parallel()
	clk := &stepClock{t: statsT0}
	s := statsSweeper(&mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}, mock.FakeDecay{R: 0}, clk)
	s.tick(context.Background())
	before := s.Stats()
	s.pruner = panicPruner{}
	clk.set(statsT0.Add(time.Hour))
	s.tick(context.Background())
	if got := s.Stats(); got != before || before.TotalPruned != 1 {
		t.Errorf("Stats() = %+v, want unchanged %+v (TotalPruned 1)", got, before)
	}
}

func TestStatsConcurrentWithTick(t *testing.T) {
	t.Parallel()
	s := statsSweeper(&mock.FakeStore{}, mock.FakeDecay{R: 0}, &stepClock{t: statsT0})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			s.tick(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_ = s.Stats()
		}
	}()
	wg.Wait()
	if s.Stats().LastRunAt.IsZero() {
		t.Error("LastRunAt zero after 100 ticks")
	}
}

// runSweeper builds a Sweeper whose every pass prunes one candidate at retrievability r, so
// Stats().TotalPruned counts completed passes and the test never touches the fake store from
// a second goroutine.
func runSweeper(c engram.Clock, r float64) *Sweeper {
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}}
	return New(p, mock.FakeDecay{R: r}, c, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// startRun runs s.Run inside the current synctest bubble. stop cancels it and waits for it to
// return, which the bubble requires before it can end.
func startRun(s *Sweeper, firstDelay time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx, firstDelay)
	}()
	return func() { cancel(); <-done }
}

func TestRunFirstPassAfterFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		defer stop()

		time.Sleep(time.Minute - time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 0 {
			t.Fatalf("one nanosecond before the first delay: %d passes, want 0", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("at the first delay: %d passes, want 1", got)
		}
		time.Sleep(time.Hour - time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("one nanosecond before the second pass is due: %d passes, want 1", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := s.Stats().TotalPruned; got != 2 {
			t.Fatalf("one interval after the first pass: %d passes, want 2", got)
		}
	})
}

func TestRunZeroFirstDelaySweepsAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, 0)
		defer stop()
		synctest.Wait() // no time.Sleep: the pass must not need schedule time to elapse
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("zero first delay: %d passes before any time elapsed, want 1", got)
		}
	})
}

func TestRunCancelledDuringFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		time.Sleep(30 * time.Second)
		stop() // returns only when Run has returned
		if got := s.Stats(); got != (engram.SweepStats{}) {
			t.Fatalf("Stats() = %+v after cancelling inside the first delay, want zero", got)
		}
	})
}

func TestRunCancelledBetweenPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(fixedClock{}, 0)
		stop := startRun(s, time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()
		time.Sleep(30 * time.Minute)
		stop()
		if got := s.Stats().TotalPruned; got != 1 {
			t.Fatalf("%d passes after cancelling between passes, want 1", got)
		}
	})
}

func TestRunPassTimeFromInjectedClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := runSweeper(&stepClock{t: statsT0}, 0)
		stop := startRun(s, time.Minute)
		defer stop()
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := s.Stats().LastRunAt; !got.Equal(statsT0) {
			t.Fatalf("LastRunAt = %v, want the injected clock's %v", got, statsT0)
		}
	})
}

func TestRunFirstPassFloorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    float64
		want int
	}{{"at floor", 0.02, 0}, {"just below", 0.0199, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := runSweeper(&stepClock{t: statsT0}, tc.r)
				stop := startRun(s, time.Minute)
				defer stop()
				time.Sleep(time.Minute)
				synctest.Wait()
				want := engram.SweepStats{LastRunAt: statsT0, LastPruned: tc.want, TotalPruned: tc.want}
				if got := s.Stats(); got != want {
					t.Fatalf("Stats() = %+v, want %+v", got, want)
				}
			})
		})
	}
}

func TestSweepOnceAlreadyDeletedIsNotAnError(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	// DeleteIfStale reports (false, nil) for a row that is already gone, as for one it kept.
	p := &mock.FakeStore{
		PruneCands: []engram.Memory{{ID: "a"}, {ID: "b"}},
		KeptIDs:    map[engram.MemoryID]bool{"a": true, "b": true},
	}
	s := New(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(&logs, nil)))

	n, err := s.SweepOnce(context.Background(), statsT0)
	if n != 0 || err != nil {
		t.Fatalf("SweepOnce = (%d, %v), want (0, nil)", n, err)
	}
	if len(p.Deleted) != 2 {
		t.Errorf("attempted %d deletes, want 2: the pass must continue past a missing row", len(p.Deleted))
	}
	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing for rows that were already gone", logs.String())
	}

	s.tick(context.Background())
	want := engram.SweepStats{LastRunAt: statsT0, LastPruned: 0, TotalPruned: 0}
	if got := s.Stats(); got != want {
		t.Errorf("Stats() = %+v, want %+v", got, want)
	}
}

func TestSweepOnceOtherDeleteErrorStillLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	p := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}, DeleteErr: errors.New("boom")}
	s := New(p, mock.FakeDecay{R: 0}, &stepClock{t: statsT0}, time.Hour, 30*24*time.Hour, 0.02, 100,
		slog.New(slog.NewTextHandler(&logs, nil)))

	n, err := s.SweepOnce(context.Background(), statsT0)
	if n != 0 || err != nil {
		t.Fatalf("SweepOnce = (%d, %v), want (0, nil)", n, err)
	}
	if got := strings.Count(logs.String(), "level=ERROR"); got != 1 {
		t.Errorf("%d error-level records, want 1; log: %q", got, logs.String())
	}
}

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
