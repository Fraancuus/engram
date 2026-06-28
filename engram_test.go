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

func TestMemoryTypeValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mt   engram.MemoryType
		want bool
	}{
		{"episodic", engram.Episodic, true},
		{"semantic", engram.Semantic, true},
		{"procedural", engram.Procedural, true},
		{"empty", engram.MemoryType(""), false},
		{"unknown", engram.MemoryType("reference"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.mt.Valid(); got != tt.want {
				t.Errorf("MemoryType(%q).Valid() = %v, want %v", tt.mt, got, tt.want)
			}
		})
	}
}

func TestRecallResultEmbedsMemory(t *testing.T) {
	t.Parallel()
	r := engram.RecallResult{Memory: engram.Memory{ID: "m1", Content: "hi"}, Score: 0.87}
	if r.ID != "m1" || r.Content != "hi" {
		t.Errorf("embedded Memory access: got ID=%q Content=%q", r.ID, r.Content)
	}
	if r.Score != 0.87 {
		t.Errorf("Score = %v, want 0.87", r.Score)
	}
}
