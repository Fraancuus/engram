package mcp

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func newTestTools(opts ...Option) *Tools {
	return NewTools(&mock.FakeEmbedder{Vec: engram.Vector{1}}, &mock.FakeReranker{}, mock.FakeDecay{R: 1}, &mock.FakeStore{}, fixedClock{}, opts...)
}

func TestWithIDGeneratorSequential(t *testing.T) {
	t.Parallel()
	n := 0
	seq := func() (engram.MemoryID, error) {
		n++
		return engram.MemoryID(fmt.Sprintf("m%06d", n)), nil
	}
	tools := newTestTools(WithIDGenerator(seq))
	for _, want := range []string{"m000001", "m000002"} {
		out, err := tools.Remember(context.Background(), validRemember())
		if err != nil {
			t.Fatalf("Remember: %v", err)
		}
		if out.MemoryID != want {
			t.Errorf("MemoryID = %q, want %q", out.MemoryID, want)
		}
	}
}

func TestWithIDGeneratorNilKept(t *testing.T) {
	t.Parallel()
	out, err := newTestTools(WithIDGenerator(nil)).Remember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(out.MemoryID) {
		t.Errorf("MemoryID = %q, want default 32 hex chars", out.MemoryID)
	}
}

func TestNewToolsDefaultID(t *testing.T) {
	t.Parallel()
	out, err := newTestTools().Remember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(out.MemoryID) {
		t.Errorf("MemoryID = %q, want 32 hex chars", out.MemoryID)
	}
}

// TestNewServerWrapsTools checks that NewServer registers the tools without error
// (AddTool infers each tool's schema from its input struct, so this also smoke-tests
// that inference succeeds).
func TestNewServerWrapsTools(t *testing.T) {
	t.Parallel()
	if NewServer(newTestTools()) == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestWithoutRerankKeepsBlendOrder(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1", Content: "first"}, Score: 0.9},
		{Memory: engram.Memory{ID: "s2", Content: "second"}, Score: 0.8},
	}}
	rr := &mock.FakeReranker{Scores: []float64{0.1, 0.8}} // would reverse the order
	tools := NewTools(&mock.FakeEmbedder{Vec: engram.Vector{1}}, rr, mock.FakeDecay{R: 1}, st, fixedClock{}, WithoutRerank())
	out, err := tools.Recall(context.Background(), RecallInput{Query: "q"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if got := resultIDs(out); len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Errorf("order = %v, want [s1 s2]", got)
	}
	if rr.LastDocs != nil {
		t.Errorf("reranker was called with %v", rr.LastDocs)
	}
}
