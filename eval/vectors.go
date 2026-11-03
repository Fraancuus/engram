package eval

import (
	"math"
	"math/rand/v2"

	"github.com/Fraancuus/engram"
)

// dimDefault is the vector dimension at the floor.
const dimDefault = 128

// Geometry constants, tuned against the dataset tests (seed 42; dedup bound also on
// seeds 1, 2, 5 and 9) at dim 128 and frozen. Cross-topic cosine < 0.30 is not
// reachable at dim 64 with in-topic cosines this low (the noise leaks onto the
// other 49 centroids), hence the floor dimension of 128: change them and the eval's ground-truth geometry
// changes with them.
//
// All bounds are raw cosines. The vector index scores (1+cos)/2 and the pipeline
// thresholds (dedup 0.95, auto-link and propagate 0.925) apply to that scale, so in
// raw cosine the dedup point is 0.90 and the link point is 0.85. Hence: ordinary
// pairs stay below the link point, twins sit in [minTwinCos, maxTwinCos] between the
// link and dedup points so they auto-link, chain versions sit in [minLinkedCos,
// maxLinkedCos]; they carry explicit links, and any above the link point also
// auto-link, and nothing reaches the dedup point.
const (
	// sigmaInTopic scales the per-memory noise added to a topic centroid.
	sigmaInTopic = 0.97
	// sigmaTwin is the weight of fresh noise relative to the sibling's noise
	// (weight 1) in a twin's noise; it sets cosine(twin, sibling).
	sigmaTwin = 0.95
	// kindOffsetScale scales the per-kind offset added to a topic centroid.
	kindOffsetScale = 0.35
	// noiseLeakMax caps |noise . otherCentroid|; noise that leaks more is redrawn
	// so cross-topic cosine stays under 0.30.
	noiseLeakMax = 0.25
	// sigmaChain scales the noise on a fact chain's centroid. It is smaller than
	// sigmaInTopic because each version adds its own noise on top, which lowers its
	// cosine to the rest of the topic; the chain centroid compensates.
	sigmaChain = 0.57
	// sigmaFactVersion is the weight of per-version noise on a fact chain's
	// centroid; versions of one chain then sit near cosine 1/(1+sigma^2).
	sigmaFactVersion = 0.5
	// minOrdinaryCos and maxOrdinaryCos bound a same-topic pair that is neither a
	// twin pair nor two versions of one chain; the ceiling stays well under the
	// auto-link point (raw cosine 0.85), so general chatter does not link.
	minOrdinaryCos = 0.40
	maxOrdinaryCos = 0.66
	// minTwinCos and maxTwinCos bound a twin pair: above the auto-link point (raw
	// cosine 0.85) and a margin under the dedup point (raw cosine 0.90).
	minTwinCos = 0.86
	maxTwinCos = 0.89
	// minLinkedCos and maxLinkedCos bound two versions of one chain, with
	// maxLinkedCos a margin under the dedup point (raw cosine 0.90), so no two
	// memories of a namespace ever dedup.
	minLinkedCos = 0.74
	maxLinkedCos = 0.88
	// maxChainTries bounds fresh chain centroids per fact chain.
	maxChainTries = 20
	// maxNoiseTries bounds the candidates drawn per vector assignment, drawBatch
	// at a time.
	maxNoiseTries = 2048
	drawBatch     = 32
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
