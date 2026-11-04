package engram

import (
	"math"
	"strings"
	"testing"
	"time"
)

var decayBase = time.Unix(1_700_000_000, 0).UTC()

func daysLater(d float64) time.Time {
	return decayBase.Add(time.Duration(d * 24 * float64(time.Hour)))
}

func mem(mt MemoryType, accessCount int, lastAccessed time.Time) Memory {
	return Memory{Type: mt, AccessCount: accessCount, LastAccessed: lastAccessed}
}

func TestRetrievabilityHelper(t *testing.T) {
	t.Parallel()
	if got := retrievability(2, 0); math.Abs(got-1) > 1e-9 {
		t.Errorf("R(s=2, dt=0) = %v, want 1", got)
	}
	if got := retrievability(2, 2); math.Abs(got-math.Exp(-1)) > 1e-9 {
		t.Errorf("R(s=2, dt=2) = %v, want 1/e", got)
	}
	if got := retrievability(0, 5); got != 0 {
		t.Errorf("R(s=0) = %v, want 0 (guard)", got)
	}
}

func TestTypeAwareRetrievability(t *testing.T) {
	t.Parallel()
	d := TypeAwareDecay{}

	if got := d.Retrievability(mem(Episodic, 0, decayBase), decayBase); math.Abs(got-1) > 1e-9 {
		t.Errorf("fresh episodic R = %v, want ~1", got)
	}
	if got := d.Retrievability(mem(Episodic, 0, decayBase), daysLater(2)); math.Abs(got-math.Exp(-1)) > 1e-6 {
		t.Errorf("episodic at dt=S0(2d) R = %v, want 1/e", got)
	}
	if got := d.Retrievability(mem(Procedural, 0, decayBase), daysLater(100)); got != 1 {
		t.Errorf("procedural R = %v, want 1 (permanent)", got)
	}

	pinned := mem(Episodic, 0, decayBase)
	pinned.Pinned = true
	if got := d.Retrievability(pinned, daysLater(1000)); got != 1 {
		t.Errorf("pinned R = %v, want 1", got)
	}

	sup := mem(Procedural, 0, decayBase)
	sup.Superseded = true
	if got := d.Retrievability(sup, daysLater(2)); math.Abs(got-math.Exp(-1)) > 1e-6 {
		t.Errorf("superseded procedural R = %v, want ~1/e (fast decay)", got)
	}

	low := d.Retrievability(mem(Episodic, 0, decayBase), daysLater(4))
	high := d.Retrievability(mem(Episodic, 5, decayBase), daysLater(4))
	if !(high > low) {
		t.Errorf("reinforcement did not raise R: low=%v high=%v", low, high)
	}
}

// TestTypeAwareVsUniformContrast is the thesis in one assertion: a long-idle procedural
// memory survives under type-aware decay but is wrongly forgotten under uniform decay.
func TestTypeAwareVsUniformContrast(t *testing.T) {
	t.Parallel()
	m := mem(Procedural, 0, decayBase)
	at := daysLater(100)
	if got := (TypeAwareDecay{}).Retrievability(m, at); got != 1 {
		t.Errorf("type-aware procedural R = %v, want 1 (kept)", got)
	}
	if got := (UniformDecay{}).Retrievability(m, at); got >= 0.01 {
		t.Errorf("uniform procedural R = %v, want <0.01 (wrongly decayed)", got)
	}
}

func TestNullDecay(t *testing.T) {
	t.Parallel()
	if got := (NullDecay{}).Retrievability(mem(Episodic, 0, decayBase), daysLater(1e6)); got != 1 {
		t.Errorf("NullDecay R = %v, want 1 always", got)
	}
}

func TestStabilityMonotonic(t *testing.T) {
	t.Parallel()
	d := TypeAwareDecay{}
	if !(d.Stability(Episodic, 3, 0) > d.Stability(Episodic, 0, 0)) {
		t.Error("Stability not increasing in accessCount")
	}
	if !(d.Stability(Episodic, 0, 1) > d.Stability(Episodic, 0, 0)) {
		t.Error("Stability not increasing in importance")
	}
	if !(d.Stability(Semantic, 0, 0) > d.Stability(Episodic, 0, 0)) {
		t.Error("semantic S0 should exceed episodic S0")
	}
}

func TestDaysSinceBoundaries(t *testing.T) {
	t.Parallel()
	if got := daysSince(decayBase, daysLater(1)); got != 0 {
		t.Errorf("daysSince(now, future) = %v, want 0 (clamped)", got)
	}
	if got := daysSince(daysLater(1), decayBase); got <= 0 {
		t.Errorf("daysSince(now, past) = %v, want > 0", got)
	}
	if got := daysSince(decayBase, decayBase); got != 0 {
		t.Errorf("daysSince(now, now) = %v, want 0", got)
	}
}

func TestTypeAwareSupersededDecaysFast(t *testing.T) {
	t.Parallel()
	// A superseded memory uses the fast archival curve regardless of type: a semantic
	// memory that would barely decay in 2 days drops to ~1/e once superseded.
	m := mem(Semantic, 0, decayBase)
	m.Superseded = true
	if got := (TypeAwareDecay{}).Retrievability(m, daysLater(2)); math.Abs(got-math.Exp(-1)) > 1e-6 {
		t.Errorf("superseded semantic R at 2d = %v, want ~1/e (fast archival)", got)
	}
}

func TestTypeAwareUnknownTypeFallsBack(t *testing.T) {
	t.Parallel()
	// An unregistered type must not silently yield S=0 (which would hard-prune a fresh
	// memory); it falls back to a sane stability.
	d := TypeAwareDecay{}
	if got := d.Stability(MemoryType("reference"), 0, 0); got <= 0 {
		t.Errorf("Stability(unknown) = %v, want > 0 (fallback)", got)
	}
	if got := d.Retrievability(mem(MemoryType("reference"), 0, decayBase), decayBase); got == 0 {
		t.Errorf("Retrievability(fresh unknown) = %v, want > 0", got)
	}
}

func TestDecayByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want DecayModel
	}{
		{DecayTypeAware, TypeAwareDecay{}},
		{DecayUniform, UniformDecay{}},
		{DecayNone, NullDecay{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := DecayByName(tc.name)
			if err != nil {
				t.Fatalf("DecayByName(%q) error: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("DecayByName(%q) = %#v, want %#v", tc.name, got, tc.want)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		got, err := DecayByName("fast")
		if err == nil {
			t.Fatalf("DecayByName(\"fast\") = %#v, want error", got)
		}
		for _, n := range []string{DecayTypeAware, DecayUniform, DecayNone} {
			if !strings.Contains(err.Error(), n) {
				t.Errorf("error %q does not mention %q", err, n)
			}
		}
	})
}

func TestDecayNameRoundTrip(t *testing.T) {
	t.Parallel()
	for _, name := range []string{DecayTypeAware, DecayUniform, DecayNone} {
		m, err := DecayByName(name)
		if err != nil {
			t.Fatalf("DecayByName(%q): %v", name, err)
		}
		if got := DecayName(m); got != name {
			t.Errorf("DecayName(DecayByName(%q)) = %q", name, got)
		}
	}
}

// TestRetrievabilitySupersededSemantic pins that a superseded semantic memory decays on the
// fast curve (S=2d) under both models, so it crosses the 0.05 threshold between 5.99 and 6.00 days.
func TestRetrievabilitySupersededSemantic(t *testing.T) {
	t.Parallel()
	models := map[string]DecayModel{"type-aware": TypeAwareDecay{}, "uniform": UniformDecay{}}
	for name, d := range models {
		for _, dt := range []float64{5.99, 6.00} {
			m := mem(Semantic, 0, decayBase)
			m.Superseded = true
			got := d.Retrievability(m, daysLater(dt))
			if want := math.Exp(-dt / 2); math.Abs(got-want) > 1e-9 {
				t.Errorf("%s dt=%v: R = %v, want %v", name, dt, got, want)
			}
		}
	}
}

// rStub reads R straight from Importance so tests control it exactly.
type rStub struct{ NullDecay }

func (rStub) Retrievability(m Memory, _ time.Time) float64 { return m.Importance }

func histOf(rs ...float64) [HistogramBuckets]int {
	ms := make([]Memory, len(rs))
	for i, r := range rs {
		ms[i] = Memory{Importance: r}
	}
	return RetrievabilityHistogram(ms, rStub{}, decayBase)
}

func TestRetrievabilityHistogramBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		r      float64
		bucket int
	}{{0, 0}, {0.0999, 0}, {0.1, 1}, {0.5, 5}, {0.9, 9}, {0.9999, 9}, {1.0, 9}}
	for _, tc := range tests {
		h := histOf(tc.r)
		for i, n := range h {
			want := 0
			if i == tc.bucket {
				want = 1
			}
			if n != want {
				t.Errorf("R=%v: bucket %d = %d, want %d (hist %v)", tc.r, i, n, want, h)
			}
		}
	}
}

func TestRetrievabilityHistogramClamps(t *testing.T) {
	t.Parallel()
	h := histOf(-0.5, 1.5)
	if h[0] != 1 || h[HistogramBuckets-1] != 1 {
		t.Errorf("clamp: got %v, want one in bucket 0 and one in bucket 9", h)
	}
	if got := RetrievabilityHistogram(nil, rStub{}, decayBase); got != [HistogramBuckets]int{} {
		t.Errorf("empty input: got %v, want all zeros", got)
	}
}

func TestRetrievabilityHistogramVirtualTime(t *testing.T) {
	t.Parallel()
	now := decayBase
	ago := func(d float64) time.Time { return now.Add(-time.Duration(d * 24 * float64(time.Hour))) }
	pinned := mem(Episodic, 0, ago(365))
	pinned.Pinned = true
	tests := []struct {
		name   string
		m      Memory
		bucket int
	}{
		{"episodic 6d", mem(Episodic, 0, ago(6)), 0},
		{"episodic 5.9d", mem(Episodic, 0, ago(5.9)), 0},
		{"episodic 0d", mem(Episodic, 0, ago(0)), 9},
		{"procedural 1y", mem(Procedural, 0, ago(365)), 9},
		{"pinned episodic 1y", pinned, 9},
	}
	for _, tc := range tests {
		h := RetrievabilityHistogram([]Memory{tc.m}, TypeAwareDecay{}, now)
		if h[tc.bucket] != 1 {
			t.Errorf("%s: want bucket %d, got %v", tc.name, tc.bucket, h)
		}
	}
}
