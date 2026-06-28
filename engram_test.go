package engram_test

import (
	"testing"

	"github.com/Fraancuus/engram"
)

// TestMemoryTypeStrings pins the wire/DB form of each MemoryType constant. These
// strings are persisted as Neo4j node properties and keyed on by the DecayModel,
// so silent drift (a rename, a casing change) would corrupt stored memories
// without a compile error — this table is the guard.
func TestMemoryTypeStrings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mt   engram.MemoryType
		want string
	}{
		{"episodic", engram.Episodic, "episodic"},
		{"semantic", engram.Semantic, "semantic"},
		{"procedural", engram.Procedural, "procedural"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tt.mt); got != tt.want {
				t.Errorf("MemoryType %s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
