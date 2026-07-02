package mcp

import (
	"testing"

	"github.com/Fraancuus/engram/mock"
)

// TestNewServerWiresPorts checks that NewServer accepts the domain ports and registers
// the tools without error (AddTool infers each tool's schema from its input struct, so
// this also smoke-tests that inference succeeds). The fakes' compile-time use here
// confirms they satisfy engram.Embedder and the Store interface.
func TestNewServerWiresPorts(t *testing.T) {
	t.Parallel()
	srv := NewServer(&mock.FakeEmbedder{}, &mock.FakeStore{}, fixedClock{})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}
