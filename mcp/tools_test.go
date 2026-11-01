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
