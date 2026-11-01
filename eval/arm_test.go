package eval

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func tinyParams() Params {
	return Params{Namespaces: 2, Topics: 4, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 16, Seed: 6}
}

func tinyDataset(t *testing.T) *Dataset {
	t.Helper()
	ds, err := generateUnchecked(tinyParams())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return ds
}

func TestArmADoesNotPrune(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	res, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, rerankNone)
	if err != nil {
		t.Fatalf("runArm: %v", err)
	}
	if res.FinalSize != len(ds.Memories) {
		t.Fatalf("FinalSize = %d, want %d", res.FinalSize, len(ds.Memories))
	}
}

func TestReinforceThroughRecall(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	if len(ds.Accesses) == 0 {
		t.Fatal("dataset has no accesses")
	}
	first := ds.Accesses[0]
	a := newArm(engram.NullDecay{}, ds, rerankNone)
	acc := newAcc()
	for d := 0; d <= first.Day; d++ {
		if err := a.day(context.Background(), ds, d, 5, acc); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
	}
	snap, ok := a.store.Snapshot(engram.MemoryID(a.ids[first.GID]))
	if !ok {
		t.Fatalf("%s missing from store", first.GID)
	}
	if snap.AccessCount < 1 || !snap.LastAccessed.Equal(dayTime(first.Day)) {
		t.Fatalf("AccessCount=%d LastAccessed=%v, want >=1 at %v", snap.AccessCount, snap.LastAccessed, dayTime(first.Day))
	}
}

func TestArmBPrunesStaleStandard(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	a := newArm(engram.UniformDecay{}, ds, rerankNone)
	acc := newAcc()
	for d := 0; d <= 150; d++ {
		if err := a.day(context.Background(), ds, d, 5, acc); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
	}
	lastAccess := map[string]int{}
	for _, ac := range ds.Accesses {
		lastAccess[ac.GID] = max(lastAccess[ac.GID], ac.Day)
	}
	// A non-current standard never gets recalled by queries, so it must be
	// gone by day 150 once its accesses stop at day 60.
	superseded := map[string]bool{}
	for _, m := range ds.Memories {
		if m.Supersedes != "" {
			superseded[m.Supersedes] = true
		}
	}
	checked := 0
	for _, m := range ds.Memories {
		if m.Kind != KindStandard || !superseded[m.GID] || lastAccess[m.GID] > 60 {
			continue
		}
		checked++
		if _, ok := a.store.Snapshot(engram.MemoryID(a.ids[m.GID])); ok {
			t.Errorf("stale standard %s still in store on day 150", m.GID)
		}
	}
	if checked == 0 {
		t.Fatal("no stale standard to check")
	}
}

func TestDedupAborts(t *testing.T) {
	t.Parallel()
	v := engram.Vector{1, 0, 0, 0, 0}
	ds := &Dataset{
		Params: Params{Days: 1, Dim: 5},
		Memories: []Memory{
			{GID: "g1", Namespace: "ns0", Type: engram.Semantic, Content: "g1", Vec: v, Importance: 0.5},
			{GID: "g2", Namespace: "ns0", Type: engram.Semantic, Content: "g2", Vec: v, Importance: 0.5},
		},
	}
	_, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, rerankNone)
	var de *dedupError
	if !errors.As(err, &de) || de.GID != "g2" {
		t.Fatalf("err = %v, want *dedupError for g2", err)
	}
	if de.Existing != "m000001" || math.Abs(de.Cosine-1) > 1e-6 {
		t.Fatalf("Existing=%q Cosine=%v, want m000001 and ~1", de.Existing, de.Cosine)
	}
}

func TestSeriesEveryTenDays(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	res, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, rerankNone)
	if err != nil {
		t.Fatalf("runArm: %v", err)
	}
	if len(res.Series) != 18 || res.Series[0].Day != 9 || res.Series[17].Day != 179 {
		t.Fatalf("series = %+v, want 18 points days 9..179", res.Series)
	}
}

// countingReranker wraps a Reranker and counts calls.
type countingReranker struct {
	inner engram.Reranker
	calls int
}

func (c *countingReranker) Rerank(ctx context.Context, q string, docs []string) ([]float64, error) {
	c.calls++
	return c.inner.Rerank(ctx, q, docs)
}

func TestArmRerankModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mode       rerankMode
		wantCalled bool
	}{
		{"none never reranks", rerankNone, false},
		{"cosine reranks", rerankCosine, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ds := tinyDataset(t)
			emb := mock.NewLookupEmbedder()
			for _, m := range ds.Memories {
				emb.Register(m.Content, m.Vec)
			}
			for _, q := range ds.Queries {
				emb.Register(q.Content, q.Vec)
			}
			cr := &countingReranker{inner: mock.CosineReranker{Embedder: emb}}
			a := buildArm(engram.NullDecay{}, ds, tt.mode, cr)
			acc := newAcc()
			for d := 0; d < ds.Params.Days; d++ {
				if err := a.day(context.Background(), ds, d, 5, acc); err != nil {
					t.Fatalf("day %d: %v", d, err)
				}
			}
			if (cr.calls > 0) != tt.wantCalled {
				t.Fatalf("rerank calls = %d, wantCalled %v", cr.calls, tt.wantCalled)
			}
		})
	}
}
