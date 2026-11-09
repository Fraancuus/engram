package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Fraancuus/engram"
)

// defaultStatsRowCap bounds the live rows memory_stats fetches for its histogram.
const defaultStatsRowCap = 10_000

// statsStore is the read-only capability the memory_stats handler consumes.
type statsStore interface {
	Counts(ctx context.Context, ns engram.Namespace) (engram.StoreCounts, error)
	LiveMemories(ctx context.Context, ns engram.Namespace, limit int) ([]engram.Memory, error)
}

// MemoryStatsInput is the argument set of the memory_stats tool.
type MemoryStatsInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"restrict to this universe; empty means all"`
}

// TypeCounts is a count per memory type; a struct so all three keys are always present.
type TypeCounts struct {
	Episodic   int `json:"episodic"`
	Semantic   int `json:"semantic"`
	Procedural int `json:"procedural"`
}

// MemoryCounts counts rows. Superseded, Forgotten and Pinned are flags and overlap.
type MemoryCounts struct {
	Total       int                   `json:"total"`
	Live        int                   `json:"live"`
	Superseded  int                   `json:"superseded"`
	Forgotten   int                   `json:"forgotten"`
	Pinned      int                   `json:"pinned"`
	ByType      TypeCounts            `json:"by_type"`
	ByNamespace map[string]TypeCounts `json:"by_namespace"`
}

// HistogramBucket is one retrievability bucket over [Min, Max), except the last, which also includes Max.
type HistogramBucket struct {
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Count int     `json:"count"`
}

// RetrievabilityStats is the live-row retrievability histogram.
type RetrievabilityStats struct {
	Decay                string            `json:"decay"`
	Buckets              []HistogramBucket `json:"buckets"`
	BelowRecallThreshold int               `json:"below_recall_threshold"`
	Truncated            bool              `json:"truncated"`
}

// StoreSize reports the graph size as counts. Scoped to a namespace, it is that namespace's
// size, not the whole store's, and the edge and bridge counts include those touching it.
type StoreSize struct {
	Memories      int `json:"memories"`
	LinkEdges     int `json:"link_edges"`
	EntityBridges int `json:"entity_bridges"`
}

// SweepReport is the decay sweep's state since process start. LastRunAt is nil until a
// pass completes.
type SweepReport struct {
	LastRunAt   *time.Time `json:"last_run_at"`
	LastPruned  int        `json:"last_pruned"`
	TotalPruned int        `json:"total_pruned"`
}

// MemoryStatsOutput is the result of the memory_stats tool.
type MemoryStatsOutput struct {
	Counts         MemoryCounts        `json:"counts"`
	Retrievability RetrievabilityStats `json:"retrievability"`
	StoreSize      StoreSize           `json:"store_size"`
	Sweep          SweepReport         `json:"sweep"`
}

// doMemoryStats validates the untrusted input and reports counts, a live-row
// retrievability histogram, graph size and sweep state. It never writes or reinforces.
// Store failures are logged and returned sanitized.
func (t *Tools) doMemoryStats(ctx context.Context, in MemoryStatsInput) (out MemoryStatsOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error("memory_stats: recovered panic", "panic", r)
			out, err = MemoryStatsOutput{}, errors.New("memory_stats: internal error")
		}
	}()

	if in.Namespace != "" && (strings.TrimSpace(in.Namespace) == "" || len(in.Namespace) > maxNamespaceBytes) {
		return MemoryStatsOutput{}, fmt.Errorf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
	}
	ns := engram.Namespace(in.Namespace)

	counts, err := t.stats.Counts(ctx, ns)
	if err != nil {
		t.log.Error("memory_stats: counts failed", "err", err)
		return MemoryStatsOutput{}, errors.New("memory_stats: store unavailable")
	}
	rows, err := t.stats.LiveMemories(ctx, ns, t.statsRowCap+1)
	if err != nil {
		t.log.Error("memory_stats: live rows failed", "err", err)
		return MemoryStatsOutput{}, errors.New("memory_stats: store unavailable")
	}

	out.Counts = foldCounts(counts.Groups)
	out.StoreSize = StoreSize{Memories: out.Counts.Total, LinkEdges: counts.LinkEdges, EntityBridges: counts.EntityBridges}
	out.Retrievability = t.retrievabilityStats(rows)
	out.Sweep = t.sweepReport()
	return out, nil
}

// foldCounts folds the per-(namespace, type) groups into totals, by-type and by-namespace counts.
func foldCounts(groups []engram.CountGroup) MemoryCounts {
	var out MemoryCounts
	out.ByNamespace = map[string]TypeCounts{}
	for _, g := range groups {
		c := &out
		c.Total += g.Total
		c.Live += g.Live
		c.Superseded += g.Superseded
		c.Forgotten += g.Forgotten
		c.Pinned += g.Pinned
		byNS := c.ByNamespace[string(g.Namespace)]
		for _, tc := range []*TypeCounts{&c.ByType, &byNS} {
			switch g.Type {
			case engram.Episodic:
				tc.Episodic += g.Total
			case engram.Semantic:
				tc.Semantic += g.Total
			case engram.Procedural:
				tc.Procedural += g.Total
			}
		}
		c.ByNamespace[string(g.Namespace)] = byNS
	}
	return out
}

// retrievabilityStats truncates rows to the cap and builds the retrievability histogram.
func (t *Tools) retrievabilityStats(rows []engram.Memory) RetrievabilityStats {
	var out RetrievabilityStats
	out.Truncated = len(rows) > t.statsRowCap
	if out.Truncated {
		rows = rows[:t.statsRowCap]
	}
	now := t.clock.Now()
	hist := engram.RetrievabilityHistogram(rows, t.decay, now)
	out.Decay = engram.DecayName(t.decay)
	out.Buckets = make([]HistogramBucket, engram.HistogramBuckets)
	for i, n := range hist {
		out.Buckets[i] = HistogramBucket{
			Min: float64(i) / engram.HistogramBuckets, Max: float64(i+1) / engram.HistogramBuckets, Count: n,
		}
	}
	for _, m := range rows {
		if t.decay.Retrievability(m, now) < t.softThreshold {
			out.BelowRecallThreshold++
		}
	}
	return out
}

// sweepReport reports the decay sweep's state, or the zero value without a sweep source.
func (t *Tools) sweepReport() SweepReport {
	var out SweepReport
	if t.sweepStats != nil {
		s := t.sweepStats()
		out.LastPruned, out.TotalPruned = s.LastPruned, s.TotalPruned
		if !s.LastRunAt.IsZero() {
			at := s.LastRunAt.UTC() // systemClock is local; Neo4j-sourced times are UTC
			out.LastRunAt = &at
		}
	}
	return out
}
