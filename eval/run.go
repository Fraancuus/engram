package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
)

// armOrder is the fixed arm key and print order.
var armOrder = []string{engram.DecayNone, engram.DecayUniform, engram.DecayTypeAware}

// Results holds every arm's per-seed results of one eval run.
type Results struct {
	K      int
	Rerank string
	Params Params
	Seeds  []int64
	Arms   map[string][]ArmResult
}

// Run generates one dataset per seed and runs all three arms over it,
// sequentially. p.Seed is overwritten per seed.
func Run(ctx context.Context, p Params, seeds []int64, k int, mode rerankMode) (*Results, error) {
	res := &Results{K: k, Rerank: mode.String(), Params: p, Seeds: seeds, Arms: map[string][]ArmResult{}}
	for _, seed := range seeds {
		ps := p
		ps.Seed = seed
		ds, err := Generate(ps)
		if err != nil {
			return nil, fmt.Errorf("generate dataset for seed %d: %w", seed, err)
		}
		for _, name := range armOrder {
			dm, err := engram.DecayByName(name)
			if err != nil {
				return nil, fmt.Errorf("decay model %q: %w", name, err)
			}
			ar, err := runArm(ctx, name, dm, ds, k, mode)
			if err != nil {
				return nil, fmt.Errorf("run arm %q seed %d: %w", name, seed, err)
			}
			ar.Seed = seed
			res.Arms[name] = append(res.Arms[name], ar)
		}
	}
	return res, nil
}

// Summary is one arm's metrics aggregated across seeds.
type Summary struct {
	Precision, Recall, NDCG, MRR Stats
	RecallByType                 map[engram.MemoryType]Stats
	BridgeRecall, ViaShare       map[string]Stats
	LatencyP50, LatencyP95       time.Duration
	FinalSize, FinalEdges        Stats
	FinalBridges                 Stats
}

// Summary aggregates the named arm across its seeds; latencies are the mean
// duration.
func (r *Results) Summary(arm string) Summary {
	rs := r.Arms[arm]
	col := func(f func(ArmResult) float64) Stats {
		xs := make([]float64, len(rs))
		for i, a := range rs {
			xs[i] = f(a)
		}
		return aggregate(xs)
	}
	dur := func(f func(ArmResult) time.Duration) time.Duration {
		if len(rs) == 0 {
			return 0
		}
		var sum time.Duration
		for _, a := range rs {
			sum += f(a)
		}
		return sum / time.Duration(len(rs))
	}
	s := Summary{
		Precision:    col(func(a ArmResult) float64 { return a.Precision }),
		Recall:       col(func(a ArmResult) float64 { return a.Recall }),
		NDCG:         col(func(a ArmResult) float64 { return a.NDCG }),
		MRR:          col(func(a ArmResult) float64 { return a.MRR }),
		RecallByType: map[engram.MemoryType]Stats{},
		BridgeRecall: map[string]Stats{},
		ViaShare:     map[string]Stats{},
		LatencyP50:   dur(func(a ArmResult) time.Duration { return a.LatencyP50 }),
		LatencyP95:   dur(func(a ArmResult) time.Duration { return a.LatencyP95 }),
		FinalSize:    col(func(a ArmResult) float64 { return float64(a.FinalSize) }),
		FinalEdges:   col(func(a ArmResult) float64 { return float64(a.FinalEdges) }),
		FinalBridges: col(func(a ArmResult) float64 { return float64(a.FinalBridges) }),
	}
	for _, t := range []engram.MemoryType{engram.Procedural, engram.Semantic, engram.Episodic} {
		s.RecallByType[t] = col(func(a ArmResult) float64 { return a.RecallByType[t] })
	}
	for _, key := range []string{"link", "entity"} {
		s.BridgeRecall[key] = col(func(a ArmResult) float64 { return a.BridgeRecall[key] })
	}
	for _, key := range []string{"vector", "link", "entity"} {
		s.ViaShare[key] = col(func(a ArmResult) float64 { return a.ViaShare[key] })
	}
	return s
}
