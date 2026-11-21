// Package sweep runs Engram's periodic decay sweep: it hard-prunes memories whose
// retrievability has fallen below a floor, first after a delay and then on a ticker, until
// context cancellation.
package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Fraancuus/engram"
)

// Pruner is the narrow store capability the sweep consumes, defined here at the consumer and
// satisfied by *neo4j.Store and the fakes in mock. DeleteIfStale deletes a candidate only while
// it is still unpinned and last accessed before `before`, and reports whether it did.
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	DeleteIfStale(ctx context.Context, id engram.MemoryID, before time.Time) (deleted bool, err error)
}

// Sweeper periodically hard-prunes decayed memories. Construct it with New and drive it
// with Run; SweepOnce is the tested core.
type Sweeper struct {
	pruner    Pruner
	decay     engram.DecayModel
	clock     engram.Clock
	interval  time.Duration
	grace     time.Duration
	hardFloor float64
	batch     int
	log       *slog.Logger

	mu    sync.Mutex
	stats engram.SweepStats
}

// New constructs a Sweeper. interval is the tick period; grace is how long past its last
// access a memory must sit before it can be pruned; hardFloor is the retrievability below
// which a candidate is deleted; batch bounds candidates fetched per tick.
func New(p Pruner, d engram.DecayModel, c engram.Clock, interval, grace time.Duration, hardFloor float64, batch int, log *slog.Logger) *Sweeper {
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = time.Hour
	}
	if batch <= 0 {
		batch = 500
	}
	return &Sweeper{
		pruner:    p,
		decay:     d,
		clock:     c,
		interval:  interval,
		grace:     grace,
		hardFloor: hardFloor,
		batch:     batch,
		log:       log,
	}
}

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

// Run drives SweepOnce until ctx is cancelled: one pass once firstDelay has elapsed, then one
// per interval. A firstDelay of zero runs the first pass immediately. Each pass recovers from
// a panic so one bad sweep cannot take the process down.
func (s *Sweeper) Run(ctx context.Context, firstDelay time.Duration) {
	first := time.NewTimer(firstDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
		s.tick(ctx)
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Sweeper) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("sweep: recovered panic", "panic", r)
		}
	}()
	now := s.clock.Now()
	n, err := s.SweepOnce(ctx, now)
	if err != nil {
		s.log.Error("sweep failed", "err", err)
		return
	}
	s.record(now, n)
	if n > 0 {
		s.log.Info("sweep pruned decayed memories", "count", n)
	}
}

// record stores a completed pass. A pass that returns an error never reaches it; a pass
// whose deletes all failed still records, with count 0.
func (s *Sweeper) record(now time.Time, pruned int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = engram.SweepStats{LastRunAt: now, LastPruned: pruned, TotalPruned: s.stats.TotalPruned + pruned}
}

// Stats returns a copy of the stats for completed passes since process start.
func (s *Sweeper) Stats() engram.SweepStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}
