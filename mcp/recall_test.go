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

	out, err := h.doRecall(context.Background(), recallInput{Query: "q"})
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
			// Search fetches a fixed seed set (seedN); the k clamp is applied at blend, so
			// assert the result count.
			st := &mock.FakeStore{SearchResults: manySeeds(120)}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			out, err := h.doRecall(context.Background(), recallInput{Query: "q", K: tt.in})
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
	out, err := h.doRecall(context.Background(), recallInput{Query: "q", Namespaces: []string{"nsA"}})
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
	if _, err := h.doRecall(context.Background(), recallInput{Query: "q", Namespaces: []string{"a", "b"}}); err != nil {
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
	if _, err := h.doRecall(context.Background(), recallInput{Query: ""}); err == nil {
		t.Fatal("want error for empty query")
	}
	if emb.Last != "" {
		t.Error("must not embed on validation failure")
	}
}

func TestDoRecallEmbedErrorIsSanitized(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{Err: errors.New("tei-internal-detail")}, &mock.FakeStore{})
	_, err := h.doRecall(context.Background(), recallInput{Query: "q"})
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
	if _, err := h.doRecall(context.Background(), recallInput{Query: "   "}); err == nil {
		t.Fatal("want error for whitespace-only query")
	}
	if emb.Last != "" {
		t.Error("must not embed on a blank query")
	}
}

func TestDoRecallRejectsBlankNamespace(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, &mock.FakeStore{})
	if _, err := h.doRecall(context.Background(), recallInput{Query: "q", Namespaces: []string{"  "}}); err == nil {
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
	out, err := h.doRecall(context.Background(), recallInput{Query: "q"})
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
	out, err := h.doRecall(context.Background(), recallInput{Query: "q"})
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
	out, err := h.doRecall(context.Background(), recallInput{Query: "q"})
	if err != nil {
		t.Fatalf("doRecall must not fail on rerank count mismatch: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "s1" {
		t.Errorf("fallback order = %v, want blend order [s1 ...]", got)
	}
}
