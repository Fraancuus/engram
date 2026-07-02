package mcp

import (
	"testing"

	"github.com/Fraancuus/engram"
)

func findResult(rs []engram.RecallResult, id engram.MemoryID) (engram.RecallResult, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return engram.RecallResult{}, false
}

func TestBlendScoresLinkAndBridge(t *testing.T) {
	t.Parallel()
	seeds := []engram.RecallResult{{Memory: engram.Memory{ID: "s1"}, Score: 0.8}}
	neighbors := []engram.Neighbor{
		{Memory: engram.Memory{ID: "L"}, SourceID: "s1", Via: "link", Weight: 0.5},
		{Memory: engram.Memory{ID: "E"}, SourceID: "s1", Via: "entity:Redis", Weight: 1.0},
	}
	out := blend(seeds, neighbors, 10, 0.5)
	if r, ok := findResult(out, "s1"); !ok || r.Score != 0.8 || r.RetrievedVia != "vector" {
		t.Errorf("seed s1 = %+v (ok=%v), want score 0.8 via vector", r, ok)
	}
	if r, ok := findResult(out, "L"); !ok || r.Score != 0.4 || r.RetrievedVia != "link" {
		t.Errorf("link L = %+v (ok=%v), want score 0.40 via link", r, ok)
	}
	if r, ok := findResult(out, "E"); !ok || r.Score != 0.4 || r.RetrievedVia != "entity:Redis" {
		t.Errorf("bridge E = %+v (ok=%v), want score 0.40 via entity:Redis", r, ok)
	}
}

func TestBlendKeepsMaxScorePath(t *testing.T) {
	t.Parallel()
	seeds := []engram.RecallResult{
		{Memory: engram.Memory{ID: "s1"}, Score: 0.9},
		{Memory: engram.Memory{ID: "x"}, Score: 0.6}, // x is also a seed
	}
	// x is reachable as a link from s1 at 0.9*0.5 = 0.45, below its own seed score 0.6.
	neighbors := []engram.Neighbor{{Memory: engram.Memory{ID: "x"}, SourceID: "s1", Via: "link", Weight: 0.5}}
	out := blend(seeds, neighbors, 10, 0.5)
	if r, _ := findResult(out, "x"); r.Score != 0.6 || r.RetrievedVia != "vector" {
		t.Errorf("x = %+v, want score 0.6 via vector (max path wins)", r)
	}
}

func TestBlendTopKAndTiebreak(t *testing.T) {
	t.Parallel()
	seeds := []engram.RecallResult{
		{Memory: engram.Memory{ID: "b"}, Score: 0.5},
		{Memory: engram.Memory{ID: "a"}, Score: 0.5}, // ties with b; "a" sorts first
		{Memory: engram.Memory{ID: "c"}, Score: 0.9},
	}
	out := blend(seeds, nil, 2, 0.5)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (top-k)", len(out))
	}
	if out[0].ID != "c" || out[1].ID != "a" {
		t.Errorf("order = [%s %s], want [c a] (score desc, id tiebreak)", out[0].ID, out[1].ID)
	}
}
