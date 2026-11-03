package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

// manySeeds builds n distinct RecallResults with descending scores.
func manySeeds(n int) []engram.RecallResult {
	out := make([]engram.RecallResult, n)
	for i := range out {
		out[i] = engram.RecallResult{
			Memory: engram.Memory{ID: engram.MemoryID(fmt.Sprintf("m%04d", i))},
			Score:  1.0 - float64(i)/float64(n),
		}
	}
	return out
}

func TestDoRecallMapsResults(t *testing.T) {
	t.Parallel()
	created := time.Unix(1000, 0).UTC()
	accessed := time.Unix(2000, 0).UTC()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{
			ID: "a", Content: "alpha", Type: engram.Semantic, Namespace: "ns",
			Source: "src", CreatedAt: created, LastAccessed: accessed, AccessCount: 3,
		}, Score: 0.91},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)

	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(out.Results))
	}
	r := out.Results[0]
	if r.ID != "a" || r.Content != "alpha" || r.Type != "semantic" || r.Namespace != "ns" || r.Score != 0.91 {
		t.Errorf("result = %+v, want mapped fields", r)
	}
	p := r.Provenance
	if p.Source != "src" || !p.CreatedAt.Equal(created) || !p.LastAccessed.Equal(accessed) || p.AccessCount != 3 {
		t.Errorf("provenance = %+v, want projected memory fields", p)
	}
}

func TestDoRecallClampsK(t *testing.T) {
	t.Parallel()
	k := func(n int) *int { return &n }
	tests := []struct {
		name string
		in   *int
		want int
	}{
		{"default", nil, 10},
		{"too big", k(500), 100},
		{"zero", k(0), 1},
		{"negative", k(-5), 1},
		{"normal", k(25), 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Search fetches a fixed seed set (seedN); the k clamp is applied after the filter, so
			// assert the result count.
			st := &mock.FakeStore{SearchResults: manySeeds(120)}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			out, err := h.doRecall(context.Background(), RecallInput{Query: "q", K: tt.in})
			if err != nil {
				t.Fatalf("doRecall: %v", err)
			}
			if len(out.Results) != tt.want {
				t.Errorf("results = %d, want %d (k clamp)", len(out.Results), tt.want)
			}
		})
	}
}

func TestDoRecallExpandsAndTagsProvenance(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{{Memory: engram.Memory{ID: "s1", Content: "seed"}, Score: 0.8}},
		NeighborsRes:  []engram.Neighbor{{Memory: engram.Memory{ID: "n1", Content: "neighbor"}, SourceID: "s1", Via: "link", Weight: 0.5}},
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q", Namespaces: []string{"nsA"}})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(st.LastNeighborSeeds) != 1 || st.LastNeighborSeeds[0] != "s1" {
		t.Errorf("Neighbors seeds = %v, want [s1]", st.LastNeighborSeeds)
	}
	if len(st.LastNeighborScope) != 1 || st.LastNeighborScope[0] != "nsA" {
		t.Errorf("Neighbors scope = %v, want [nsA]", st.LastNeighborScope)
	}
	via := map[engram.MemoryID]string{}
	for _, r := range out.Results {
		via[engram.MemoryID(r.ID)] = r.Provenance.RetrievedVia
	}
	if via["s1"] != "vector" {
		t.Errorf("s1 retrieved_via = %q, want vector", via["s1"])
	}
	if via["n1"] != "link" {
		t.Errorf("n1 retrieved_via = %q, want link", via["n1"])
	}
}

func TestDoRecallPassesNamespaces(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	if _, err := h.doRecall(context.Background(), RecallInput{Query: "q", Namespaces: []string{"a", "b"}}); err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(st.LastSearchNS) != 2 || st.LastSearchNS[0] != "a" || st.LastSearchNS[1] != "b" {
		t.Errorf("Search namespaces = %v, want [a b]", st.LastSearchNS)
	}
}

func TestDoRecallEmptyQuery(t *testing.T) {
	t.Parallel()
	emb := &mock.FakeEmbedder{Vec: engram.Vector{1}}
	h := testHandlers(emb, &mock.FakeStore{})
	if _, err := h.doRecall(context.Background(), RecallInput{Query: ""}); err == nil {
		t.Fatal("want error for empty query")
	}
	if emb.Last != "" {
		t.Error("must not embed on validation failure")
	}
}

func TestDoRecallEmbedErrorIsSanitized(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{Err: errors.New("tei-internal-detail")}, &mock.FakeStore{})
	_, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err == nil {
		t.Fatal("want error on embed failure")
	}
	if strings.Contains(err.Error(), "tei-internal-detail") {
		t.Errorf("leaks internal detail: %q", err.Error())
	}
}

func TestDoRecallRejectsBlankQuery(t *testing.T) {
	t.Parallel()
	emb := &mock.FakeEmbedder{Vec: engram.Vector{1}}
	h := testHandlers(emb, &mock.FakeStore{})
	if _, err := h.doRecall(context.Background(), RecallInput{Query: "   "}); err == nil {
		t.Fatal("want error for whitespace-only query")
	}
	if emb.Last != "" {
		t.Error("must not embed on a blank query")
	}
}

func TestDoRecallRejectsBlankNamespace(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, &mock.FakeStore{})
	if _, err := h.doRecall(context.Background(), RecallInput{Query: "q", Namespaces: []string{"  "}}); err == nil {
		t.Fatal("want error for whitespace-only namespace")
	}
}

func TestDoRecallReranks(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "first"}, Score: 0.9},
		{Memory: engram.Memory{ID: "s2", Content: "second"}, Score: 0.5},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.reranker = &mock.FakeReranker{Scores: []float64{0.1, 0.8}} // s1->0.1, s2->0.8 => s2 first
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "s2" || got[1] != "s1" {
		t.Errorf("rerank order = %v, want [s2 s1]", got)
	}
	if out.Results[0].Score != 0.8 {
		t.Errorf("top score = %v, want rerank score 0.8", out.Results[0].Score)
	}
	fr := h.reranker.(*mock.FakeReranker)
	if fr.LastQuery != "q" || len(fr.LastDocs) != 2 || fr.LastDocs[0] != "first" {
		t.Errorf("reranker got query=%q docs=%v", fr.LastQuery, fr.LastDocs)
	}
}

func TestDoRecallRerankFallback(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "first"}, Score: 0.9},
		{Memory: engram.Memory{ID: "s2", Content: "second"}, Score: 0.5},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.reranker = &mock.FakeReranker{Err: errors.New("rerank-down")}
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall should not fail when rerank errors: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Errorf("fallback order = %v, want blend order [s1 s2]", got)
	}
}

// countMismatchReranker returns fewer scores than docs with no error — the mismatch
// fallback path that the validating FakeReranker can no longer produce.
type countMismatchReranker struct{}

func (countMismatchReranker) Rerank(_ context.Context, _ string, _ []string) ([]float64, error) {
	return []float64{0.1}, nil
}

func TestDoRecallRerankCountMismatchFallback(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "first"}, Score: 0.9},
		{Memory: engram.Memory{ID: "s2", Content: "second"}, Score: 0.5},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.reranker = countMismatchReranker{}
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall must not fail on rerank count mismatch: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "s1" {
		t.Errorf("fallback order = %v, want blend order [s1 ...]", got)
	}
}

func TestDoRecallSoftForget(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "a"}, Score: 0.9},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.decay = mock.FakeDecay{R: 0} // below softThreshold -> soft-forgotten

	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(out.Results) != 0 {
		t.Errorf("soft-forgotten memory returned: %v", resultIDs(out))
	}

	out, err = h.doRecall(context.Background(), RecallInput{Query: "q", IncludeForgotten: true})
	if err != nil {
		t.Fatalf("doRecall(include_forgotten): %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "s1" {
		t.Errorf("include_forgotten did not return it: %v", resultIDs(out))
	}
}

func TestDoRecallExcludesSupersededAndForgotten(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "sup", Content: "a", Superseded: true}, Score: 0.9},
		{Memory: engram.Memory{ID: "forg", Content: "b", Forgotten: true}, Score: 0.9},
		{Memory: engram.Memory{ID: "ok", Content: "c"}, Score: 0.9},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.decay = mock.FakeDecay{R: 1}
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if got := resultIDs(out); len(got) != 1 || got[0] != "ok" {
		t.Errorf("results = %v, want [ok] (superseded/forgotten excluded)", got)
	}
}

func TestDoRecallWeightedScore(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "a", Importance: 0.25}, Score: 0.5},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.decay = mock.FakeDecay{R: 0.125}
	h.wSim, h.wImp, h.wRet = 2, 4, 8
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	// 2*0.5 + 4*0.25 + 8*0.125 = 1 + 1 + 1 = 3 (all binary-exact).
	if len(out.Results) != 1 || out.Results[0].Score != 3.0 {
		t.Errorf("weighted score = %v, want 3.0", out.Results[0].Score)
	}
}

func TestDoRecallReinforcesResults(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "a"}, Score: 0.9},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	if _, err := h.doRecall(context.Background(), RecallInput{Query: "q"}); err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(st.Reinforced) != 1 || st.Reinforced[0] != "s1" {
		t.Errorf("Reinforced = %v, want [s1]", st.Reinforced)
	}
	if st.LastPropagateID != "s1" || st.LastPropagateThr != propagateThreshold {
		t.Errorf("propagate = id:%q thr:%v, want s1/%v", st.LastPropagateID, st.LastPropagateThr, propagateThreshold)
	}
}

func TestDoRecallReinforceFailureDoesNotFailRecall(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{{Memory: engram.Memory{ID: "s1", Content: "a"}, Score: 0.9}},
		ReinforceErr:  errors.New("db-down"),
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("recall must succeed even when reinforce fails: %v", err)
	}
	if len(out.Results) != 1 {
		t.Errorf("results = %d, want 1 (reinforce failure is non-fatal)", len(out.Results))
	}
}

// rByID is a DecayModel returning a fixed retrievability per memory id (0 if absent).
type rByID map[engram.MemoryID]float64

func (r rByID) Retrievability(m engram.Memory, _ time.Time) float64 { return r[m.ID] }

func (rByID) Stability(engram.MemoryType, int, float64) float64 { return 1 }

// blendHandlers returns handlers with the production blend weights and no reranker: the
// cross-encoder is the final authority on order, and these tests are about the blend.
func blendHandlers(st *mock.FakeStore, d engram.DecayModel) *Tools {
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.reranker = nil
	h.decay = d
	h.wImp, h.wRet = defaultWImp, defaultWRet
	return h
}

func TestRecallFreshEpisodeBeatsStandard(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "std", Content: "s", Importance: 0.7}, Score: 0.80},
		{Memory: engram.Memory{ID: "ep", Content: "e", Importance: 0.4}, Score: 0.95},
	}}
	h := blendHandlers(st, rByID{"ep": 0.7, "std": 1.0})
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "ep" {
		t.Errorf("order = %v, want episode first", got)
	}
}

func TestRecallBoundedTiebreak(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		gap       float64 // how far the dead-ish candidate's raw score leads
		wantFirst string
	}{
		{"gap inside R weight reorders", 0.009, "fresh"},
		{"gap outside R weight keeps order", 0.011, "stale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{SearchResults: []engram.RecallResult{
				{Memory: engram.Memory{ID: "fresh", Content: "f"}, Score: 0.80},
				{Memory: engram.Memory{ID: "stale", Content: "s"}, Score: 0.80 + tt.gap},
			}}
			h := blendHandlers(st, rByID{"fresh": 1.0, "stale": 0.05})
			out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
			if err != nil {
				t.Fatalf("doRecall: %v", err)
			}
			if got := resultIDs(out); len(got) != 2 || got[0] != tt.wantFirst {
				t.Errorf("order = %v, want %s first", got, tt.wantFirst)
			}
		})
	}
}

func TestRecallDeadCandidatesDoNotCrowdPool(t *testing.T) {
	t.Parallel()
	var seeds []engram.RecallResult
	for i := 0; i < 20; i++ {
		seeds = append(seeds, engram.RecallResult{
			Memory: engram.Memory{ID: engram.MemoryID(fmt.Sprintf("dead%02d", i)), Content: "d", Superseded: true}, Score: 0.9})
	}
	for i := 0; i < 5; i++ {
		seeds = append(seeds, engram.RecallResult{
			Memory: engram.Memory{ID: engram.MemoryID(fmt.Sprintf("live%02d", i)), Content: "l"}, Score: 0.5})
	}
	h := blendHandlers(&mock.FakeStore{SearchResults: seeds}, mock.FakeDecay{R: 1})
	k := 5
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q", K: &k})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(out.Results) != 5 {
		t.Fatalf("results = %v, want the 5 live ones", resultIDs(out))
	}
	for _, id := range resultIDs(out) {
		if !strings.HasPrefix(id, "live") {
			t.Errorf("dead candidate %s crowded into results", id)
		}
	}
}

func TestRecallSoftThresholdBoundary(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "at", Content: "a"}, Score: 0.9},
		{Memory: engram.Memory{ID: "below", Content: "b"}, Score: 0.9},
	}}
	h := blendHandlers(st, rByID{"at": 0.05, "below": 0.0499})
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if got := resultIDs(out); len(got) != 1 || got[0] != "at" {
		t.Errorf("results = %v, want [at] (R 0.05 kept, 0.0499 dropped)", got)
	}
}

func TestRecallNullDecayOrder(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "a", Content: "a", Importance: 0.0}, Score: 0.70}, // 0.70
		{Memory: engram.Memory{ID: "b", Content: "b", Importance: 1.0}, Score: 0.50}, // 0.80
		{Memory: engram.Memory{ID: "c", Content: "c", Importance: 0.5}, Score: 0.60}, // 0.75
		{Memory: engram.Memory{ID: "d", Content: "d", Importance: 0.2}, Score: 0.90}, // 0.96
	}}
	h := blendHandlers(st, engram.NullDecay{})
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	want := []string{"d", "b", "c", "a"}
	got := resultIDs(out)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v (sim + 0.3 importance)", got, want)
	}
}

func TestRecallKAboveDefaultPool(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: manySeeds(150)}
	h := blendHandlers(st, mock.FakeDecay{R: 1})
	h.seedN = 150
	k := 100
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q", K: &k})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(out.Results) != 100 {
		t.Errorf("results = %d, want 100", len(out.Results))
	}
}

func TestRecallAllDeadIsEmpty(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "a", Content: "a"}, Score: 0.9},
		{Memory: engram.Memory{ID: "b", Content: "b"}, Score: 0.8},
	}}
	h := blendHandlers(st, rByID{"a": 0.01, "b": 0.02})
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(out.Results) != 0 || len(st.Reinforced) != 0 {
		t.Errorf("results=%v reinforced=%v, want both empty", resultIDs(out), st.Reinforced)
	}
}

func TestDoRecallIncludeForgottenRanksDead(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "low", Content: "l", Superseded: true}, Score: 0.5},
		{Memory: engram.Memory{ID: "high", Content: "h", Superseded: true}, Score: 0.9},
	}}
	h := blendHandlers(st, rByID{}) // R 0 for both
	out, err := h.doRecall(context.Background(), RecallInput{Query: "q", IncludeForgotten: true})
	if err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if got := resultIDs(out); fmt.Sprint(got) != "[high low]" {
		t.Errorf("order = %v, want [high low] ranked by score", got)
	}
}

func TestRecallPoolCutsToRerankCandidates(t *testing.T) {
	t.Parallel()
	seeds := []engram.RecallResult{
		{Memory: engram.Memory{ID: "dead", Content: "dead", Superseded: true}, Score: 0.99},
	}
	for i := 0; i < 25; i++ {
		seeds = append(seeds, engram.RecallResult{
			Memory: engram.Memory{ID: engram.MemoryID(fmt.Sprintf("live%02d", i)), Content: fmt.Sprintf("live%02d", i)},
			Score:  0.5})
	}
	h := blendHandlers(&mock.FakeStore{SearchResults: seeds}, mock.FakeDecay{R: 1})
	fr := &mock.FakeReranker{}
	h.reranker = fr
	k := 5
	if _, err := h.doRecall(context.Background(), RecallInput{Query: "q", K: &k}); err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	if len(fr.LastDocs) != defaultRerankCandidates {
		t.Errorf("reranker got %d docs, want %d", len(fr.LastDocs), defaultRerankCandidates)
	}
	for _, d := range fr.LastDocs {
		if d == "dead" {
			t.Error("superseded candidate reached the reranker")
		}
	}
}

func TestPropagateThresholdBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := mock.NewMemStore()
	orth := func(i int) engram.Vector { v := make(engram.Vector, 4); v[i] = 1; return v }
	for i, id := range []engram.MemoryID{"s", "at", "under", "explicit"} {
		if err := st.Put(ctx, engram.Memory{ID: id, Namespace: "ns", Content: string(id), Embedding: orth(i)}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	err := st.Link(ctx, "s", []engram.Link{{To: "at", Weight: 0.925}, {To: "under", Weight: 0.9249}, {To: "explicit", Weight: 1.0}})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: orth(0)}, &mock.FakeStore{})
	h.store, h.forget = st, nil
	one := 1
	if _, err := h.doRecall(ctx, RecallInput{Query: "q", K: &one}); err != nil {
		t.Fatalf("doRecall: %v", err)
	}
	now := h.clock.Now()
	for id, want := range map[engram.MemoryID]bool{"at": true, "under": false, "explicit": true} {
		res, err := st.Search(ctx, nil, orth(map[engram.MemoryID]int{"at": 1, "under": 2, "explicit": 3}[id]), 1)
		if err != nil || len(res) == 0 {
			t.Fatalf("search %s: %v", id, err)
		}
		if got := res[0].LastAccessed.Equal(now); got != want {
			t.Errorf("%s refreshed = %v, want %v", id, got, want)
		}
	}
}
