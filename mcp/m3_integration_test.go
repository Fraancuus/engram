//go:build integration

package mcp

import (
	"context"
	"testing"
)

// TestM3RerankSurfacesDirectAnswer exercises the full live recall path through the real
// bge-reranker-v2-m3 sidecar: the cross-encoder should rank the memory that directly
// answers the query first, even beside a topically-similar distractor.
func TestM3RerankSurfacesDirectAnswer(t *testing.T) {
	h := liveHandlers(t)
	ns := string(uniqueNS("m3-rerank"))
	mustRemember(t, h, rememberInput{
		Content:   "France is a country in Western Europe known for its cuisine, wine, and art museums.",
		Type:      "semantic",
		Namespace: ns,
	})
	answer := mustRemember(t, h, rememberInput{
		Content:   "Paris is the capital of France.",
		Type:      "semantic",
		Namespace: ns,
	})

	out, err := h.doRecall(context.Background(),
		recallInput{Query: "What is the capital of France?", Namespaces: []string{ns}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("recall returned no results")
	}
	if out.Results[0].ID != answer.MemoryID {
		t.Errorf("top result = %q, want direct-answer memory %q (reranker should surface it)",
			out.Results[0].ID, answer.MemoryID)
	}
}

// TestM3TokenBudgetTrims verifies packBudget caps the assembled output under maxTokens
// even when the caller asks for a large k, against the live stack.
func TestM3TokenBudgetTrims(t *testing.T) {
	h := liveHandlers(t)
	h.maxTokens = 20 // room for ~1 short memory
	ns := string(uniqueNS("m3-budget"))
	contents := []string{
		"The Raft consensus algorithm elects a leader to manage log replication.",
		"Paxos is a family of protocols for solving consensus among unreliable nodes.",
		"Vector clocks capture causality between events in a distributed system.",
		"Consistent hashing distributes keys across nodes to minimize reshuffling.",
		"A write-ahead log records mutations before applying them for durability.",
	}
	for _, c := range contents {
		mustRemember(t, h, rememberInput{Content: c, Type: "semantic", Namespace: ns})
	}

	big := 100
	out, err := h.doRecall(context.Background(),
		recallInput{Query: "distributed systems consensus", Namespaces: []string{ns}, K: &big})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("recall returned no results")
	}
	if len(out.Results) >= len(contents) {
		t.Errorf("token budget did not trim: got %d results for maxTokens=%d, want < %d",
			len(out.Results), h.maxTokens, len(contents))
	}
}
