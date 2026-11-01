package eval

import (
	"math"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

var _ engram.Clock = (*Clock)(nil)

func relSet(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestMetricsKnownValues(t *testing.T) {
	t.Parallel()
	ret, rel := []string{"a", "x", "b", "y", "z"}, relSet("a", "b", "c")
	if got := precisionAt(ret, rel, 5); math.Abs(got-0.4) > 1e-9 {
		t.Errorf("P = %v", got)
	}
	if got := recallAt(ret, rel, 5); math.Abs(got-0.6667) > 1e-4 {
		t.Errorf("R = %v", got)
	}
	if got := reciprocalRank(ret, rel, 5); got != 1 {
		t.Errorf("RR = %v", got)
	}
	want := (1 + 1/math.Log2(4)) / (1 + 1/math.Log2(3) + 1/math.Log2(4))
	if got := ndcgAt(ret, rel, 5); math.Abs(got-want) > 1e-9 {
		t.Errorf("nDCG = %v, want %v", got, want)
	}
	if got := reciprocalRank([]string{"x", "y", "a"}, rel, 5); math.Abs(got-1.0/3) > 1e-9 {
		t.Errorf("RR rank3 = %v", got)
	}
	if got := reciprocalRank([]string{"x", "y"}, rel, 5); got != 0 {
		t.Errorf("RR miss = %v", got)
	}
}

func TestMetricsEmpty(t *testing.T) {
	t.Parallel()
	rel := relSet("a")
	for name, f := range map[string]func([]string, map[string]bool, int) float64{
		"P": precisionAt, "R": recallAt, "nDCG": ndcgAt, "RR": reciprocalRank,
	} {
		if got := f(nil, rel, 5); got != 0 {
			t.Errorf("%s(empty) = %v", name, got)
		}
	}
}

func TestMetricsTruncateToK(t *testing.T) {
	t.Parallel()
	ret, rel := []string{"x", "y", "a", "b"}, relSet("a", "b")
	if got := precisionAt(ret, rel, 2); got != 0 {
		t.Errorf("P@2 = %v", got)
	}
	if got := recallAt(ret, rel, 2); got != 0 {
		t.Errorf("R@2 = %v", got)
	}
	if got := ndcgAt(ret, rel, 2); got != 0 {
		t.Errorf("nDCG@2 = %v", got)
	}
	if got := reciprocalRank(ret, rel, 2); got != 0 {
		t.Errorf("RR@2 = %v", got)
	}
}

func TestAggregateStd(t *testing.T) {
	t.Parallel()
	if s := aggregate([]float64{1, 2, 3}); s.Mean != 2 || math.Abs(s.Std-1) > 1e-9 {
		t.Errorf("got %+v", s)
	}
	if s := aggregate([]float64{5}); s.Mean != 5 || s.Std != 0 {
		t.Errorf("n=1 got %+v", s)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	t.Parallel()
	ds := make([]time.Duration, 0, 20)
	for i := 20; i >= 1; i-- { // unsorted input
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	for _, c := range []struct {
		p    float64
		want time.Duration
	}{{0.5, 10 * time.Millisecond}, {0.95, 19 * time.Millisecond}, {1, 20 * time.Millisecond}, {0, time.Millisecond}} {
		if got := percentile(ds, c.p); got != c.want {
			t.Errorf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
	if ds[0] != 20*time.Millisecond {
		t.Error("input mutated")
	}
	if percentile(nil, 0.5) != 0 {
		t.Error("empty != 0")
	}
}

func TestClockSetNow(t *testing.T) {
	t.Parallel()
	var c Clock
	c.Set(dayTime(3))
	if got, want := c.Now(), Day0.Add(72*time.Hour); !got.Equal(want) {
		t.Errorf("Now = %v, want %v", got, want)
	}
}
