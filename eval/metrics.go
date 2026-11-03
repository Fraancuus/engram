package eval

import (
	"math"
	"sort"
	"time"
)

// hits returns ret truncated to k and the number of relevant items in it.
func hits(ret []string, rel map[string]bool, k int) ([]string, int) {
	if k < len(ret) {
		ret = ret[:max(k, 0)]
	}
	n := 0
	for _, id := range ret {
		if rel[id] {
			n++
		}
	}
	return ret, n
}

// precisionAt is the fraction of the top k results that are relevant.
func precisionAt(ret []string, rel map[string]bool, k int) float64 {
	top, n := hits(ret, rel, k)
	if len(top) == 0 || k < 1 {
		return 0
	}
	// Divide by k, not len(top): a short result list must not inflate precision.
	return float64(n) / float64(k)
}

// recallAt is the fraction of relevant items found in the top k results.
func recallAt(ret []string, rel map[string]bool, k int) float64 {
	top, n := hits(ret, rel, k)
	if len(top) == 0 || len(rel) == 0 {
		return 0
	}
	return float64(n) / float64(len(rel))
}

// ndcgAt is binary-gain nDCG over the top k results.
func ndcgAt(ret []string, rel map[string]bool, k int) float64 {
	top, _ := hits(ret, rel, k)
	var dcg float64
	for i, id := range top {
		if rel[id] {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	var ideal float64
	for i := 0; i < min(len(rel), k); i++ {
		ideal += 1 / math.Log2(float64(i+2))
	}
	if len(top) == 0 || ideal == 0 {
		return 0
	}
	return dcg / ideal
}

// reciprocalRank is 1/rank of the first relevant result in the top k, or 0.
func reciprocalRank(ret []string, rel map[string]bool, k int) float64 {
	top, _ := hits(ret, rel, k)
	for i, id := range top {
		if rel[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// Stats is a mean and sample standard deviation.
type Stats struct{ Mean, Std float64 }

// aggregate returns the mean and sample (n-1) standard deviation of xs;
// Std is 0 for fewer than two samples.
func aggregate(xs []float64) Stats {
	if len(xs) == 0 {
		return Stats{}
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	s := Stats{Mean: sum / float64(len(xs))}
	if len(xs) < 2 {
		return s
	}
	var ss float64
	for _, x := range xs {
		ss += (x - s.Mean) * (x - s.Mean)
	}
	s.Std = math.Sqrt(ss / float64(len(xs)-1))
	return s
}

// percentile is the nearest-rank p-th percentile (p in [0,1]) of ds.
func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	r := int(math.Ceil(p*float64(len(s)))) - 1
	return s[min(max(r, 0), len(s)-1)]
}
