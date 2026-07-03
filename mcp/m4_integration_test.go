//go:build integration

package mcp

import (
	"context"
	"testing"
	"time"
)

// clockAt is a fixed clock at an arbitrary instant, used to advance recall's "now" past a
// memory's insertion so decay can be observed with virtual time.
type clockAt time.Time

func (c clockAt) Now() time.Time { return time.Time(c) }

// TestM4SoftForgetAndInclude inserts an episodic memory, advances the recall clock ~1 year,
// and checks it is decayed out of default recall but recoverable with include_forgotten.
func TestM4SoftForgetAndInclude(t *testing.T) {
	h := liveHandlers(t)
	ns := string(uniqueNS("m4-soft"))
	m := mustRemember(t, h, rememberInput{
		Content: "An old episodic event that should fade with disuse.", Type: "episodic", Namespace: ns,
	})

	t0 := (fixedClock{}).Now()
	h.clock = clockAt(t0.Add(365 * 24 * time.Hour)) // a year later; episodic S0 is 2 days

	def, err := h.doRecall(context.Background(),
		recallInput{Query: "old episodic event that fades", Namespaces: []string{ns}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, r := range def.Results {
		if r.ID == m.MemoryID {
			t.Errorf("decayed memory returned in default recall: %q", r.ID)
		}
	}

	inc, err := h.doRecall(context.Background(),
		recallInput{Query: "old episodic event that fades", Namespaces: []string{ns}, IncludeForgotten: true})
	if err != nil {
		t.Fatalf("recall(include_forgotten): %v", err)
	}
	found := false
	for _, r := range inc.Results {
		if r.ID == m.MemoryID {
			found = true
		}
	}
	if !found {
		t.Error("include_forgotten did not surface the decayed memory")
	}
}

// TestM4ReinforceOnAccess checks a recall bumps the returned memory's access_count, visible
// on the next recall's provenance.
func TestM4ReinforceOnAccess(t *testing.T) {
	h := liveHandlers(t)
	ns := string(uniqueNS("m4-reinforce"))
	m := mustRemember(t, h, rememberInput{
		Content: "A durable fact about distributed consensus algorithms.", Type: "semantic", Namespace: ns,
	})

	if _, err := h.doRecall(context.Background(),
		recallInput{Query: "distributed consensus algorithms", Namespaces: []string{ns}}); err != nil {
		t.Fatalf("recall #1: %v", err)
	}
	out, err := h.doRecall(context.Background(),
		recallInput{Query: "distributed consensus algorithms", Namespaces: []string{ns}})
	if err != nil {
		t.Fatalf("recall #2: %v", err)
	}
	for _, r := range out.Results {
		if r.ID == m.MemoryID {
			if r.Provenance.AccessCount < 1 {
				t.Errorf("access_count = %d, want >= 1 after a prior recall reinforced it", r.Provenance.AccessCount)
			}
			return
		}
	}
	t.Fatal("memory not found on the second recall")
}
