package eval

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mcp"
	"github.com/Fraancuus/engram/mock"
	"github.com/Fraancuus/engram/sweep"
)

const (
	sweepGrace     = 30 * 24 * time.Hour
	sweepHardFloor = 0.02
	accessK        = 1
	seriesEvery    = 10
)

// Point is one size/graph sample of an arm's store.
type Point struct{ Day, Size, Edges, Bridges int }

// ArmResult is one arm's aggregates over a dataset.
type ArmResult struct {
	Name         string
	Seed         int64
	Precision    float64
	Recall       float64
	NDCG         float64
	MRR          float64
	RecallByType map[engram.MemoryType]float64
	BridgeRecall map[string]float64 // "link", "entity"
	ViaShare     map[string]float64 // "vector", "link", "entity"
	LatencyP50   time.Duration
	LatencyP95   time.Duration
	FinalSize    int
	FinalEdges   int
	FinalBridges int
	Series       []Point
}

// dedupError reports that Remember collapsed a generated memory into an
// existing one, which the dataset's construction forbids.
type dedupError struct {
	GID, Existing string // generator id and the store id it collapsed into
	Cosine        float64
}

func (e *dedupError) Error() string {
	return fmt.Sprintf("memory %s deduplicated into %s (cosine %.6f): dataset vectors are too close", e.GID, e.Existing, e.Cosine)
}

// sequentialIDs yields m000001, m000002 and so on.
func sequentialIDs() func() (engram.MemoryID, error) {
	n := 0
	return func() (engram.MemoryID, error) {
		n++
		return engram.MemoryID(fmt.Sprintf("m%06d", n)), nil
	}
}

// armState is one arm's live pipeline: the shipped mcp.Tools over a MemStore.
type armState struct {
	decay engram.DecayModel
	store *mock.MemStore
	tools *mcp.Tools
	clock *Clock
	ids   map[string]string // GID -> store id
	byID  map[string]string // store id -> GID
}

// armAcc accumulates per-query scores across days.
type armAcc struct {
	prec, rec, ndcg, mrr []float64
	byType               map[engram.MemoryType][]float64
	bridge               map[string][]float64
	via                  map[string]int
	lat                  []time.Duration
	series               []Point
}

func newAcc() *armAcc {
	return &armAcc{byType: map[engram.MemoryType][]float64{}, bridge: map[string][]float64{}, via: map[string]int{}}
}

// newArm builds an arm. Every memory and query content is registered with its
// vector so the pipeline's embedder can resolve it.
func newArm(decay engram.DecayModel, ds *Dataset, mode rerankMode) *armState {
	return buildArm(decay, ds, mode, nil)
}

// rerankMode selects whether an arm reranks recall results.
type rerankMode int

const (
	// rerankNone keeps the blend order (default).
	rerankNone rerankMode = iota
	// rerankCosine reorders by cosine to the query embedding.
	rerankCosine
)

// String is the mode's name as printed in the report.
func (m rerankMode) String() string {
	if m == rerankCosine {
		return "cosine"
	}
	return "none"
}

// buildArm is newArm with an optional reranker override (tests count calls).
func buildArm(decay engram.DecayModel, ds *Dataset, mode rerankMode, rr engram.Reranker) *armState {
	emb := mock.NewLookupEmbedder()
	for _, m := range ds.Memories {
		emb.Register(m.Content, m.Vec)
	}
	for _, q := range ds.Queries {
		emb.Register(q.Content, q.Vec)
	}
	store := mock.NewMemStore()
	clock := &Clock{}
	if rr == nil {
		rr = mock.CosineReranker{Embedder: emb}
	}
	opts := []mcp.Option{mcp.WithIDGenerator(sequentialIDs())}
	if mode == rerankNone {
		opts = append(opts, mcp.WithoutRerank())
	}
	tools := mcp.NewTools(emb, rr, decay, store, clock, opts...)
	return &armState{decay: decay, store: store, tools: tools, clock: clock,
		ids: map[string]string{}, byID: map[string]string{}}
}

// translate maps GIDs to store ids; an unwritten GID is a dataset bug.
func (a *armState) translate(gids []string) ([]string, error) {
	out := make([]string, len(gids))
	for i, g := range gids {
		id, ok := a.ids[g]
		if !ok {
			return nil, fmt.Errorf("gid %s referenced before it was written", g)
		}
		out[i] = id
	}
	return out, nil
}

// day plays virtual day d: writes, accesses, queries, sweep, series sample.
func (a *armState) day(ctx context.Context, ds *Dataset, d, k int, acc *armAcc) error {
	now := dayTime(d)
	a.clock.Set(now)
	for _, m := range ds.Memories {
		if m.WriteDay == d {
			if err := a.write(ctx, m); err != nil {
				return err
			}
		}
	}
	one := accessK
	for _, ac := range ds.Accesses {
		if ac.Day != d {
			continue
		}
		m, ok := ds.Lookup(ac.GID)
		if !ok {
			return fmt.Errorf("access of unknown gid %s", ac.GID)
		}
		in := mcp.RecallInput{Query: m.Content, Namespaces: []string{string(m.Namespace)}, K: &one}
		if _, err := a.tools.Recall(ctx, in); err != nil {
			return fmt.Errorf("day %d access %s: %w", d, ac.GID, err)
		}
	}
	for _, q := range ds.Queries {
		if q.Day == d {
			if err := a.query(ctx, ds, q, k, acc); err != nil {
				return err
			}
		}
	}
	// Fresh sweeper per day so batch covers the whole store.
	sw := sweep.New(a.store, a.decay, a.clock, time.Hour, sweepGrace, sweepHardFloor, max(a.store.Len(), 1),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := sw.SweepOnce(ctx, now); err != nil {
		return fmt.Errorf("day %d sweep: %w", d, err)
	}
	if d%seriesEvery == seriesEvery-1 {
		acc.series = append(acc.series, Point{Day: d, Size: a.store.Len(), Edges: a.store.EdgeCount(), Bridges: a.store.BridgeCount()})
	}
	return nil
}

func (a *armState) write(ctx context.Context, m Memory) error {
	links, err := a.translate(m.Links)
	if err != nil {
		return fmt.Errorf("write %s links: %w", m.GID, err)
	}
	var sup []string
	if m.Supersedes != "" {
		if sup, err = a.translate([]string{m.Supersedes}); err != nil {
			return fmt.Errorf("write %s supersedes: %w", m.GID, err)
		}
	}
	imp := m.Importance
	out, err := a.tools.Remember(ctx, mcp.RememberInput{
		Content: m.Content, Type: string(m.Type), Namespace: string(m.Namespace),
		Importance: &imp, Entities: m.Entities, Links: links, Supersedes: sup,
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", m.GID, err)
	}
	if out.Deduped {
		de := &dedupError{GID: m.GID, Existing: out.MemoryID}
		if ex, ok := a.store.Snapshot(engram.MemoryID(out.MemoryID)); ok {
			de.Cosine = mock.Cosine(m.Vec, ex.Embedding)
		}
		return de
	}
	a.ids[m.GID] = out.MemoryID
	a.byID[out.MemoryID] = m.GID
	return nil
}

func (a *armState) query(ctx context.Context, ds *Dataset, q Query, k int, acc *armAcc) error {
	nss := make([]string, len(q.Namespaces))
	for i, n := range q.Namespaces {
		nss[i] = string(n)
	}
	start := time.Now() // the only wall-clock read: latency
	out, err := a.tools.Recall(ctx, mcp.RecallInput{Query: q.Content, Namespaces: nss, K: &k})
	acc.lat = append(acc.lat, time.Since(start))
	if err != nil {
		return fmt.Errorf("query %s: %w", q.ID, err)
	}
	relIDs, err := a.translate(q.Relevant)
	if err != nil {
		return fmt.Errorf("query %s relevant: %w", q.ID, err)
	}
	rel := make(map[string]bool, len(relIDs))
	for _, id := range relIDs {
		rel[id] = true
	}
	ret := make([]string, len(out.Results))
	got := map[string]bool{}
	for i, h := range out.Results {
		ret[i] = h.ID
		got[h.ID] = true
		switch via := h.Provenance.RetrievedVia; {
		case via == "vector" || via == "link":
			acc.via[via]++
		case strings.HasPrefix(via, "entity:"):
			acc.via["entity"]++
		}
	}
	acc.prec = append(acc.prec, precisionAt(ret, rel, k))
	r := recallAt(ret, rel, k)
	acc.rec = append(acc.rec, r)
	acc.ndcg = append(acc.ndcg, ndcgAt(ret, rel, k))
	acc.mrr = append(acc.mrr, reciprocalRank(ret, rel, k))
	switch q.Class {
	case QBridgeLink:
		acc.bridge["link"] = append(acc.bridge["link"], r)
	case QBridgeEntity:
		acc.bridge["entity"] = append(acc.bridge["entity"], r)
	}
	total, hit := map[engram.MemoryType]int{}, map[engram.MemoryType]int{}
	for i, g := range q.Relevant {
		m, ok := ds.Lookup(g)
		if !ok {
			return fmt.Errorf("query %s: unknown relevant gid %s", q.ID, g)
		}
		total[m.Type]++
		if got[relIDs[i]] {
			hit[m.Type]++
		}
	}
	for t, n := range total {
		acc.byType[t] = append(acc.byType[t], float64(hit[t])/float64(n))
	}
	return nil
}

// runArm plays the whole dataset under decay and returns the arm's aggregates.
func runArm(ctx context.Context, name string, decay engram.DecayModel, ds *Dataset, k int, mode rerankMode) (ArmResult, error) {
	a := newArm(decay, ds, mode)
	acc := newAcc()
	for d := 0; d < ds.Params.Days; d++ {
		if err := a.day(ctx, ds, d, k, acc); err != nil {
			return ArmResult{}, fmt.Errorf("arm %s: %w", name, err)
		}
	}
	res := ArmResult{
		Name: name, Seed: ds.Params.Seed,
		Precision: aggregate(acc.prec).Mean, Recall: aggregate(acc.rec).Mean,
		NDCG: aggregate(acc.ndcg).Mean, MRR: aggregate(acc.mrr).Mean,
		RecallByType: map[engram.MemoryType]float64{}, BridgeRecall: map[string]float64{}, ViaShare: map[string]float64{},
		LatencyP50: percentile(acc.lat, 0.5), LatencyP95: percentile(acc.lat, 0.95),
		FinalSize: a.store.Len(), FinalEdges: a.store.EdgeCount(), FinalBridges: a.store.BridgeCount(),
		Series: acc.series,
	}
	for t, xs := range acc.byType {
		res.RecallByType[t] = aggregate(xs).Mean
	}
	for c, xs := range acc.bridge {
		res.BridgeRecall[c] = aggregate(xs).Mean
	}
	total := 0
	for _, n := range acc.via {
		total += n
	}
	for v, n := range acc.via {
		res.ViaShare[v] = float64(n) / float64(total)
	}
	return res, nil
}
