package engram

import (
	"math"
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
