package eval

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func tinyParams() Params {
	return Params{Namespaces: 2, Topics: 4, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 32, Seed: 2, Mix: MixAgent}
}

func TestTinyParamsGenerates(t *testing.T) {
	t.Parallel()
	if _, err := generateUnchecked(tinyParams()); err != nil {
		t.Fatalf("tiny fixture does not generate (try another seed): %v", err)
	}
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
	res, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, RerankNone)
	if err != nil {
		t.Fatalf("runArm: %v", err)
	}
	if res.FinalSize != len(ds.Memories) {
		t.Fatalf("FinalSize = %d, want %d", res.FinalSize, len(ds.Memories))
	}
}

func TestRunArmByClass(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	res, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, RerankNone)
	if err != nil {
		t.Fatalf("runArm: %v", err)
	}
	n, ndcg, mrr := 0, 0.0, 0.0
	for c, cs := range res.ByClass {
		if cs.N == 0 {
			t.Fatalf("class %v has N=0", c)
		}
		n += cs.N
		ndcg += cs.NDCG * float64(cs.N)
		mrr += cs.MRR * float64(cs.N)
	}
	if n != len(ds.Queries) {
		t.Fatalf("class counts sum to %d, want %d queries", n, len(ds.Queries))
	}
	if math.Abs(ndcg/float64(n)-res.NDCG) > 1e-9 || math.Abs(mrr/float64(n)-res.MRR) > 1e-9 {
		t.Fatalf("overall nDCG/MRR %v/%v is not the query-weighted class mean %v/%v", res.NDCG, res.MRR, ndcg/float64(n), mrr/float64(n))
	}
}

func TestReinforceThroughRecall(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	if len(ds.Accesses) == 0 {
		t.Fatal("dataset has no accesses")
	}
	first := ds.Accesses[0]
	a := newArm(engram.NullDecay{}, ds, RerankNone)
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
	a := newArm(engram.UniformDecay{}, ds, RerankNone)
	acc := newAcc()
	superseded := map[string]bool{}
	for _, m := range ds.Memories {
		if m.Supersedes != "" {
			superseded[m.Supersedes] = true
		}
	}
	// A recall (query or access) reinforces what it returns, so a stale
	// standard that was recalled after its own accesses stopped at day 60 may
	// legitimately survive. Detect that as AccessCount growing past its day-60
	// value (a write sets LastAccessed but leaves AccessCount alone).
	recalled := map[string]bool{}
	base := map[string]int{}
	day60 := map[string]engram.Memory{}
	for d := 0; d <= 150; d++ {
		if err := a.day(context.Background(), ds, d, 5, acc); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
		for gid := range superseded {
			snap, ok := a.store.Snapshot(engram.MemoryID(a.ids[gid]))
			switch {
			case d == 60 && ok:
				base[gid] = snap.AccessCount
				day60[gid] = snap
			case d > 60 && ok && snap.AccessCount > base[gid]:
				recalled[gid] = true
			}
		}
	}
	lastAccess := map[string]int{}
	for _, ac := range ds.Accesses {
		lastAccess[ac.GID] = max(lastAccess[ac.GID], ac.Day)
	}
	checked, exempted, wantPruned := 0, 0, 0
	for _, m := range ds.Memories {
		// A version written after day 60 has had under 90 days to decay by day
		// 150 and may still be inside its stability window; only older ones must
		// be gone.
		if m.Kind != KindStandard || !superseded[m.GID] || m.WriteDay > 60 || lastAccess[m.GID] > 60 {
			continue
		}
		if recalled[m.GID] {
			exempted++
			continue
		}
		snap, ok := day60[m.GID]
		if !ok || !snap.Superseded {
			continue // gone, unwritten or not yet superseded at day 60: the snapshot cannot predict day 150
		}
		checked++
		// The sweep's own rule: R below the hard floor at day 150 (grace is met).
		pruned := engram.UniformDecay{}.Retrievability(snap, dayTime(150)) < sweepHardFloor
		_, present := a.store.Snapshot(engram.MemoryID(a.ids[m.GID]))
		if pruned {
			wantPruned++
		}
		if pruned == present {
			t.Errorf("stale standard %s: want pruned=%v on day 150, still present=%v", m.GID, pruned, present)
		}
	}
	t.Logf("stale standards: %d checked (%d expected pruned), %d exempted as recalled after day 60", checked, wantPruned, exempted)
	if wantPruned == 0 {
		t.Fatal("no unrecalled stale standard expected to be pruned")
	}
}

func TestViaShareUnknownErrors(t *testing.T) {
	for via, want := range map[string]string{"vector": "vector", "link": "link", "entity:Redis": "entity"} {
		if got, err := viaKey(via); err != nil || got != want {
			t.Errorf("viaKey(%q) = %q, %v; want %q", via, got, err, want)
		}
	}
	if _, err := viaKey("telepathy"); err == nil || !strings.Contains(err.Error(), "unknown retrieved_via") {
		t.Errorf("want unknown retrieved_via error, got %v", err)
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
	_, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, RerankNone)
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
	res, err := runArm(context.Background(), "A", engram.NullDecay{}, ds, 5, RerankNone)
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
		mode       RerankMode
		wantCalled bool
	}{
		{"none never reranks", RerankNone, false},
		{"cosine reranks", RerankCosine, true},
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

func TestAccessHitRate(t *testing.T) {
	t.Parallel()
	ds := tinyDataset(t)
	// Under none (R=1 everywhere) the target at cos~1 wins unless a newer version
	// already superseded it: superseded memories are demoted, so that access
	// reinforces the successor. Everything else must be a hit.
	superseded := map[string]int{} // GID -> write day of its successor
	for _, m := range ds.Memories {
		if m.Supersedes != "" {
			superseded[m.Supersedes] = m.WriteDay
		}
	}
	wantN, wantHit := map[Kind]int{}, map[Kind]int{}
	for _, ac := range ds.Accesses {
		m, _ := ds.Lookup(ac.GID)
		wantN[m.Kind]++
		if day, ok := superseded[ac.GID]; !ok || day > ac.Day {
			wantHit[m.Kind]++
		}
	}
	for _, name := range armOrder {
		dm, err := engram.DecayByName(name)
		if err != nil {
			t.Fatal(err)
		}
		res, err := runArm(context.Background(), name, dm, ds, 5, RerankNone)
		if err != nil {
			t.Fatalf("runArm %s: %v", name, err)
		}
		for _, kd := range kinds {
			if res.AccessN[kd] != wantN[kd] {
				t.Errorf("%s %v: AccessN = %d, want %d", name, kd, res.AccessN[kd], wantN[kd])
			}
			if wantN[kd] == 0 {
				continue
			}
			h, ok := res.AccessHit[kd]
			if !ok || h < 0 || h > 1 {
				t.Errorf("%s %v: hit rate %v (present %v), want in [0,1]", name, kd, h, ok)
			}
			if want := float64(wantHit[kd]) / float64(wantN[kd]); name == engram.DecayNone && h != want {
				t.Errorf("none %v: hit rate %v, want %v (1.000 unless superseded)", kd, h, want)
			}
		}
	}
}
