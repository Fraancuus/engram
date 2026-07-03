// Package sweep runs Engram's periodic decay sweep: it hard-prunes memories whose
// retrievability has fallen below a floor, on a ticker that exits on context cancellation.
package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Fraancuus/engram"
)

// Pruner is the narrow store capability the sweep consumes — defined here at the consumer
// and satisfied by the one concrete *neo4j.Store.
type Pruner interface {
	PruneCandidates(ctx context.Context, before time.Time, limit int) ([]engram.Memory, error)
	Delete(ctx context.Context, id engram.MemoryID) error
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
}

// New constructs a Sweeper. interval is the tick period; grace is how long past its last
// access a memory must sit before it can be pruned; hardFloor is the retrievability below
// which a candidate is deleted; batch bounds candidates fetched per tick.
func New(p Pruner, d engram.DecayModel, c engram.Clock, interval, grace time.Duration, hardFloor float64, batch int, log *slog.Logger) *Sweeper {
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

// SweepOnce runs one prune pass as of now: it fetches candidates older than the grace
// period and deletes those whose retrievability is below the hard floor (pinned memories
// are skipped defensively). now is passed explicitly so the eval can virtualize time.
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
			if err := s.pruner.Delete(ctx, m.ID); err != nil {
				return pruned, fmt.Errorf("sweep delete %q: %w", m.ID, err)
			}
			pruned++
		}
	}
	return pruned, nil
}

// Run drives SweepOnce on a ticker until ctx is cancelled. Each tick recovers from a panic
// so one bad sweep cannot take the process down.
func (s *Sweeper) Run(ctx context.Context) {
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
	n, err := s.SweepOnce(ctx, s.clock.Now())
	if err != nil {
		s.log.Error("sweep failed", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("sweep pruned decayed memories", "count", n)
	}
}
