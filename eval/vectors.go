package eval

import (
	"math"
	"math/rand/v2"

	"github.com/Fraancuus/engram"
)

// dimDefault is the vector dimension at the floor.
const dimDefault = 64

// Geometry constants, tuned once (6 attempts) against TestNoDedupCollisions,
// TestTwinRange and TestCrossTopicLow on seeds 42, 1 and 7, and now frozen: change
// them and the eval's ground-truth geometry changes with them.
const (
	// sigmaInTopic scales the per-memory noise added to a topic centroid.
	sigmaInTopic = 0.42
	// sigmaTwin is the weight of fresh noise relative to the sibling's noise
	// (weight 1) in a twin's noise; it sets cosine(twin, sibling) = 0.90.
	sigmaTwin = 3.6
	// kindOffsetScale scales the per-kind offset added to a topic centroid.
	kindOffsetScale = 0.35
	// noiseLeakMax caps |noise . otherCentroid|; noise that leaks more is redrawn
	// so cross-topic cosine stays under 0.30.
	noiseLeakMax = 0.25
)

// newRNG returns a deterministic generator for seed.
func newRNG(seed int64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15)) //nolint:gosec // reproducible eval data, not security
}

// unit returns a random unit vector of the given dimension.
func unit(r *rand.Rand, dim int) engram.Vector {
	v := make([]float64, dim)
	for i := range v {
		v[i] = r.NormFloat64()
	}
	return normalize(v)
}

// unitOrtho returns a random unit vector orthogonal to every (orthonormal)
// vector in basis.
func unitOrtho(r *rand.Rand, dim int, basis ...engram.Vector) engram.Vector {
	v := make([]float64, dim)
	for i := range v {
		v[i] = r.NormFloat64()
	}
	for _, a := range basis {
		var dot float64
		for i := range v {
			dot += v[i] * float64(a[i])
		}
		for i := range v {
			v[i] -= dot * float64(a[i])
		}
	}
	return normalize(v)
}

// mix returns normalize(centroid + scale*offset + sigma*noise). offset and noise
// may be nil.
func mix(centroid, offset engram.Vector, scale float64, noise engram.Vector, sigma float64) engram.Vector {
	v := make([]float64, len(centroid))
	for i := range v {
		v[i] = float64(centroid[i])
		if offset != nil {
			v[i] += scale * float64(offset[i])
		}
		if noise != nil {
			v[i] += sigma * float64(noise[i])
		}
	}
	return normalize(v)
}

func normalize(v []float64) engram.Vector {
	var s float64
	for _, x := range v {
		s += x * x
	}
	s = math.Sqrt(s)
	out := make(engram.Vector, len(v))
	if s == 0 {
		return out
	}
	for i, x := range v {
		out[i] = float32(x / s)
	}
	return out
}

// centroids returns n topic centroids: an orthonormal set while n <= dim (so
// cross-topic cosine is driven only by the noise), random unit vectors beyond.
func centroids(r *rand.Rand, n, dim int) []engram.Vector {
	out := make([]engram.Vector, 0, n)
	for len(out) < n {
		if len(out) >= dim {
			out = append(out, unit(r, dim))
			continue
		}
		v := make([]float64, dim)
		for i := range v {
			v[i] = r.NormFloat64()
		}
		for _, c := range out {
			var dot float64
			for i := range v {
				dot += v[i] * float64(c[i])
			}
			for i := range v {
				v[i] -= dot * float64(c[i])
			}
		}
		out = append(out, normalize(v))
	}
	return out
}
