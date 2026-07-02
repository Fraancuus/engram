package mcp

import (
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
)

func rr(id, content string) engram.RecallResult {
	return engram.RecallResult{Memory: engram.Memory{ID: engram.MemoryID(id), Content: content}}
}

func resultIDs(out recallOutput) []string {
	s := make([]string, len(out.Results))
	for i, r := range out.Results {
		s[i] = r.ID
	}
	return s
}

func TestPackBudgetTrims(t *testing.T) {
	t.Parallel()
	// Each 40-char content ≈ 11 tokens; a 25-token ceiling fits 2, the third crosses.
	rs := []engram.RecallResult{
		rr("a", strings.Repeat("x", 40)),
		rr("b", strings.Repeat("x", 40)),
		rr("c", strings.Repeat("x", 40)),
	}
	out := packBudget(rs, 25)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "b" {
		t.Errorf("packBudget kept %d items, want 2 [a b]", len(out))
	}
}

func TestPackBudgetKeepsAtLeastOne(t *testing.T) {
	t.Parallel()
	rs := []engram.RecallResult{rr("big", strings.Repeat("x", 10000))}
	if out := packBudget(rs, 10); len(out) != 1 {
		t.Errorf("packBudget = %d, want 1 (always keep the first)", len(out))
	}
}

func TestPackBudgetAllFit(t *testing.T) {
	t.Parallel()
	rs := []engram.RecallResult{rr("a", "hi"), rr("b", "yo")}
	if out := packBudget(rs, 1000); len(out) != 2 {
		t.Errorf("packBudget = %d, want 2 (all fit)", len(out))
	}
}
