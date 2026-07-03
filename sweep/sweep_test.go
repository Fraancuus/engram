package sweep

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
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
	p2 := &mock.FakeStore{PruneCands: []engram.Memory{{ID: "a"}}, DeleteErr: errors.New("boom")}
	if _, err := testSweeper(p2, mock.FakeDecay{R: 0}).SweepOnce(context.Background(), fixedClock{}.Now()); err == nil {
		t.Error("want error when Delete fails")
	}
}
