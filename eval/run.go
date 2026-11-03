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
	// Lambda weighs relative store size in the recommendation score.
	Lambda float64
	Params Params
	Seeds  []int64
	Arms   map[string][]ArmResult
}

// Run generates one dataset per seed and runs all three arms over it,
// sequentially. p.Seed is overwritten per seed.
func Run(ctx context.Context, p Params, seeds []int64, k int, mode RerankMode) (*Results, error) {
	res := &Results{K: k, Rerank: mode.String(), Lambda: DefaultLambda, Params: p, Seeds: seeds, Arms: map[string][]ArmResult{}}
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

// ClassSummary is one query class's nDCG and MRR across seeds.
type ClassSummary struct{ NDCG, MRR Stats }

// Summary is one arm's metrics aggregated across seeds. Latencies are in
// milliseconds.
type Summary struct {
	Precision, Recall, NDCG, MRR Stats
	RecallByType                 map[engram.MemoryType]Stats
	BridgeRecall, ViaShare       map[string]Stats
	ByClass                      map[QueryClass]ClassSummary
	LatencyP50, LatencyP95       Stats
	FinalSize, FinalEdges        Stats
	FinalBridges                 Stats
	AccessHit                    map[Kind]Stats
}

// Summary aggregates the named arm across its seeds. RecallByType,
// BridgeRecall, ByClass and AccessHit average only the seeds that have the key (a seed
// without e.g. bridge-entity queries says nothing about its recall); ViaShare
// is a share of results, so an absent key is 0.
func (r *Results) Summary(arm string) Summary {
	rs := r.Arms[arm]
	col := func(f func(ArmResult) float64) Stats {
		xs := make([]float64, len(rs))
		for i, a := range rs {
			xs[i] = f(a)
		}
		return aggregate(xs)
	}
	present := func(f func(ArmResult) (float64, bool)) Stats {
		var xs []float64
		for _, a := range rs {
			if v, ok := f(a); ok {
				xs = append(xs, v)
			}
		}
		return aggregate(xs)
	}
	ms := func(f func(ArmResult) time.Duration) Stats {
		return col(func(a ArmResult) float64 { return float64(f(a)) / float64(time.Millisecond) })
	}
	s := Summary{
		Precision:    col(func(a ArmResult) float64 { return a.Precision }),
		Recall:       col(func(a ArmResult) float64 { return a.Recall }),
		NDCG:         col(func(a ArmResult) float64 { return a.NDCG }),
		MRR:          col(func(a ArmResult) float64 { return a.MRR }),
		RecallByType: map[engram.MemoryType]Stats{},
		BridgeRecall: map[string]Stats{},
		ViaShare:     map[string]Stats{},
		ByClass:      map[QueryClass]ClassSummary{},
		AccessHit:    map[Kind]Stats{},
		LatencyP50:   ms(func(a ArmResult) time.Duration { return a.LatencyP50 }),
		LatencyP95:   ms(func(a ArmResult) time.Duration { return a.LatencyP95 }),
		FinalSize:    col(func(a ArmResult) float64 { return float64(a.FinalSize) }),
		FinalEdges:   col(func(a ArmResult) float64 { return float64(a.FinalEdges) }),
		FinalBridges: col(func(a ArmResult) float64 { return float64(a.FinalBridges) }),
	}
	for _, t := range []engram.MemoryType{engram.Procedural, engram.Semantic, engram.Episodic} {
		s.RecallByType[t] = present(func(a ArmResult) (float64, bool) { v, ok := a.RecallByType[t]; return v, ok })
	}
	for _, key := range []string{"link", "entity"} {
		s.BridgeRecall[key] = present(func(a ArmResult) (float64, bool) { v, ok := a.BridgeRecall[key]; return v, ok })
	}
	for _, key := range []string{"vector", "link", "entity"} {
		s.ViaShare[key] = col(func(a ArmResult) float64 { return a.ViaShare[key] })
	}
	for _, kd := range kinds {
		s.AccessHit[kd] = present(func(a ArmResult) (float64, bool) { v, ok := a.AccessHit[kd]; return v, ok })
	}
	for _, c := range queryClasses {
		s.ByClass[c] = ClassSummary{
			NDCG: present(func(a ArmResult) (float64, bool) { v, ok := a.ByClass[c]; return v.NDCG, ok }),
			MRR:  present(func(a ArmResult) (float64, bool) { v, ok := a.ByClass[c]; return v.MRR, ok }),
		}
	}
	return s
}
