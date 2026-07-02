package mcp

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

type panicEmbedder struct{}

func (panicEmbedder) Embed(context.Context, string) (engram.Vector, error) { panic("embed boom") }

type panicSearchStore struct{ *mock.FakeStore }

func (panicSearchStore) Search(context.Context, []engram.Namespace, engram.Vector, int) ([]engram.RecallResult, error) {
	panic("search boom")
}

func TestDoRememberDedupThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		score     float64
		wantDedup bool
	}{
		{"at threshold", 0.95, true},
		{"just below", 0.9499, false},
		{"well above", 0.99, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{SearchResults: []engram.RecallResult{
				{Memory: engram.Memory{ID: "existing"}, Score: tt.score},
			}}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			out, err := h.doRemember(context.Background(), validRemember())
			if err != nil {
				t.Fatalf("doRemember: %v", err)
			}
			if out.Deduped != tt.wantDedup {
				t.Errorf("Deduped = %v, want %v (score %v, threshold %v)", out.Deduped, tt.wantDedup, tt.score, defaultDedupThreshold)
			}
		})
	}
}

func TestDoRememberDedupLinksNewEntities(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "existing"}, Score: 0.97},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Entities = []string{"NewEntity"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped {
		t.Fatal("want deduped")
	}
	if names := st.Linked["existing"]; len(names) != 1 || names[0] != "NewEntity" {
		t.Errorf("dedup must still link supplied entities, got %v", names)
	}
}

func TestDoRememberReinforceError(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{{Memory: engram.Memory{ID: "existing"}, Score: 0.97}},
		ReinforceErr:  errors.New("db-down-internal"),
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	_, err := h.doRemember(context.Background(), validRemember())
	if err == nil {
		t.Fatal("want error when reinforce fails")
	}
	if strings.Contains(err.Error(), "db-down-internal") {
		t.Errorf("leaks internal detail: %q", err.Error())
	}
}

func TestDoRememberRecoversPanic(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{}, &mock.FakeStore{})
	h.embedder = panicEmbedder{}
	_, err := h.doRemember(context.Background(), validRemember())
	if err == nil {
		t.Fatal("want error from recovered panic, got nil")
	}
}

func TestDoRecallRecoversPanic(t *testing.T) {
	t.Parallel()
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, &mock.FakeStore{})
	h.store = panicSearchStore{&mock.FakeStore{}}
	_, err := h.doRecall(context.Background(), recallInput{Query: "q"})
	if err == nil {
		t.Fatal("want error from recovered panic, got nil")
	}
}

func TestDoRememberRejectsNonFiniteImportance(t *testing.T) {
	t.Parallel()
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		st := &mock.FakeStore{}
		h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
		in := validRemember()
		in.Importance = &bad
		if _, err := h.doRemember(context.Background(), in); err == nil {
			t.Errorf("importance %v: want error", bad)
		}
		if len(st.Puts) != 0 {
			t.Errorf("importance %v: must not insert", bad)
		}
	}
}

func TestDoRememberRejectsBlankNamespace(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Namespace = "   "
	if _, err := h.doRemember(context.Background(), in); err == nil {
		t.Fatal("want error for blank namespace")
	}
}
