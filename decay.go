package engram

import (
	"math"
	"time"
)

// Decay tuning knobs (days), the parameters the M5 eval sweeps. Stability grows with
// reinforcement and importance; a superseded memory switches to a fast archival curve.
const (
	reinforceGain       = 1.0  // access multiplies stability by (1 + gain*accessCount)
	uniformS0           = 14.0 // UniformDecay's one-size-fits-all stability
	supersededStability = 2.0  // fast curve once a memory is superseded
)

// s0ByType is the type-keyed initial stability for the type-aware model. Procedural is
// realistic-but-large; its permanence is owned by the Retrievability branch below, not by
// this magnitude.
var s0ByType = map[MemoryType]float64{
	Episodic:   2,
	Semantic:   60,
	Procedural: 3650,
}

// retrievability is the shared decay curve: the probability a memory is recallable after
// dt days given stability s. R = exp(-dt/s); a non-positive s means fully decayed.
func retrievability(s, dt float64) float64 {
	if s <= 0 {
		return 0
	}
	return math.Exp(-dt / s)
}

// daysSince returns the non-negative age in days from then to now.
func daysSince(now, then time.Time) float64 {
	if d := now.Sub(then).Hours() / 24; d > 0 {
		return d
	}
	return 0
}

// TypeAwareDecay is Engram's decay model (eval arm C): permanent memories — pinned, or
// procedural until superseded — never decay; episodic and semantic decay on a type-keyed
// curve. This is the "type-aware forgetting" the eval tests.
type TypeAwareDecay struct{}

var _ DecayModel = TypeAwareDecay{}

// Retrievability implements DecayModel.
func (d TypeAwareDecay) Retrievability(m Memory, now time.Time) float64 {
	if m.Pinned {
		return 1
	}
	if m.Type == Procedural && !m.Superseded {
		return 1
	}
	s := d.Stability(m.Type, m.AccessCount, m.Importance)
	if m.Superseded {
		s = supersededStability
	}
	return retrievability(s, daysSince(now, m.LastAccessed))
}

// Stability implements DecayModel: type-keyed S0 raised by reinforcement and importance.
func (TypeAwareDecay) Stability(t MemoryType, accessCount int, importance float64) float64 {
	return s0ByType[t] * (1 + reinforceGain*float64(accessCount)) * (1 + importance)
}

// UniformDecay is the one-curve-for-all baseline (eval arm B): it ignores type, so it
// wrongly decays procedural memory — the contrast the eval measures. Pins still hold.
type UniformDecay struct{}

var _ DecayModel = UniformDecay{}

// Retrievability implements DecayModel.
func (d UniformDecay) Retrievability(m Memory, now time.Time) float64 {
	if m.Pinned {
		return 1
	}
	s := d.Stability(m.Type, m.AccessCount, m.Importance)
	if m.Superseded {
		s = supersededStability
	}
	return retrievability(s, daysSince(now, m.LastAccessed))
}

// Stability implements DecayModel with a single type-agnostic S0.
func (UniformDecay) Stability(_ MemoryType, accessCount int, importance float64) float64 {
	return uniformS0 * (1 + reinforceGain*float64(accessCount)) * (1 + importance)
}

// NullDecay never forgets (eval arm A: the never-forgetting "everyone else" baseline).
type NullDecay struct{}

var _ DecayModel = NullDecay{}

// Retrievability implements DecayModel: always recallable.
func (NullDecay) Retrievability(Memory, time.Time) float64 { return 1 }

// Stability implements DecayModel: infinite (never decays).
func (NullDecay) Stability(MemoryType, int, float64) float64 { return math.Inf(1) }
