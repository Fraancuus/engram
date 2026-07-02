//go:build integration

package mcp

import (
	"context"
	"strings"
	"testing"
)

// provs renders each result's id:retrieved_via for failure messages.
func provs(out recallOutput) []string {
	s := make([]string, len(out.Results))
	for i, r := range out.Results {
		s[i] = r.ID + ":" + r.Provenance.RetrievedVia
	}
	return s
}

// TestM2ExplicitLinkExpansion: an unrelated memory that explicitly links to the query's
// seed surfaces via the link. seedN=1 keeps it out of the vector seed set so it can only
// arrive through the graph.
func TestM2ExplicitLinkExpansion(t *testing.T) {
	h := liveHandlers(t)
	h.seedN = 1
	ns := string(uniqueNS("m2-link"))
	seed := mustRemember(t, h, rememberInput{
		Content: "Portkey routes all of our LLM API calls.", Type: "semantic", Namespace: ns,
	})
	mustRemember(t, h, rememberInput{
		Content:   "The finance team reconciles vendor invoices every month.",
		Type:      "episodic",
		Namespace: ns,
		Links:     []string{seed.MemoryID},
	})

	out, err := h.doRecall(context.Background(),
		recallInput{Query: "How do we route our LLM API calls?", Namespaces: []string{ns}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	linked := false
	for _, r := range out.Results {
		if r.Provenance.RetrievedVia == "link" {
			linked = true
		}
	}
	if !linked {
		t.Errorf("expected a result retrieved_via=link, got %v", provs(out))
	}
}

// TestM2EntityBridgeExpansion: two memories sharing an entity bridge across namespaces;
// the non-matching one surfaces via the entity when unscoped, and is excluded when the
// query is scoped to the other namespace.
func TestM2EntityBridgeExpansion(t *testing.T) {
	h := liveHandlers(t)
	h.seedN = 1
	nsX := string(uniqueNS("m2-ent-x"))
	nsY := string(uniqueNS("m2-ent-y"))
	mustRemember(t, h, rememberInput{
		Content: "Kafka handles our streaming data ingestion.", Type: "semantic", Namespace: nsX,
		Entities: []string{"Kafka"},
	})
	mustRemember(t, h, rememberInput{
		Content: "The quarterly board meeting is scheduled for October.", Type: "episodic", Namespace: nsY,
		Entities: []string{"Kafka"},
	})

	// Unscoped: the nsY memory bridges in via the shared Kafka entity.
	out, err := h.doRecall(context.Background(),
		recallInput{Query: "How does Kafka handle our streaming ingestion?"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	bridged := false
	for _, r := range out.Results {
		if strings.HasPrefix(r.Provenance.RetrievedVia, "entity:") {
			bridged = true
		}
	}
	if !bridged {
		t.Errorf("expected a result retrieved_via=entity:*, got %v", provs(out))
	}

	// Scoped to nsX: the cross-namespace bridge is excluded.
	scoped, err := h.doRecall(context.Background(),
		recallInput{Query: "How does Kafka handle our streaming ingestion?", Namespaces: []string{nsX}})
	if err != nil {
		t.Fatalf("recall scoped: %v", err)
	}
	for _, r := range scoped.Results {
		if strings.HasPrefix(r.Provenance.RetrievedVia, "entity:") {
			t.Errorf("scoped recall leaked a cross-namespace bridge: %v", provs(scoped))
		}
	}
}

// TestM2ScopedRecallExcludesCrossNamespaceLink is a regression test: an agent can plant
// an explicit link to a memory in another namespace, but a namespace-scoped recall must
// not surface it through the link arm.
func TestM2ScopedRecallExcludesCrossNamespaceLink(t *testing.T) {
	h := liveHandlers(t)
	h.seedN = 1
	nsA := string(uniqueNS("m2-xlink-a"))
	nsB := string(uniqueNS("m2-xlink-b"))
	foreign := mustRemember(t, h, rememberInput{
		Content: "Universe A note about the quarterly revenue plan.", Type: "semantic", Namespace: nsA,
	})
	// A memory in nsB that explicitly links across into nsA.
	mustRemember(t, h, rememberInput{
		Content: "Universe B memo that references the other universe.", Type: "episodic", Namespace: nsB,
		Links: []string{foreign.MemoryID},
	})

	scoped, err := h.doRecall(context.Background(),
		recallInput{Query: "Universe B memo referencing the other universe.", Namespaces: []string{nsB}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, r := range scoped.Results {
		if r.ID == foreign.MemoryID {
			t.Errorf("scoped recall leaked cross-namespace linked memory %q via %q", r.ID, r.Provenance.RetrievedVia)
		}
	}
}
