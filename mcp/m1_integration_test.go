//go:build integration

package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/inference"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// uniqueNS gives each test run an isolated universe in the shared dev DB.
func uniqueNS(base string) engram.Namespace {
	return engram.Namespace(fmt.Sprintf("%s-%d", base, time.Now().UnixNano()))
}

// liveHandlers wires the real TEI embedder and Neo4j store into the handlers, or skips.
func liveHandlers(t *testing.T) *handlers {
	t.Helper()
	store, err := eneo4j.New(context.Background(),
		envOr("NEO4J_TEST_URI", "neo4j://localhost:7687"),
		envOr("NEO4J_TEST_USER", "neo4j"),
		os.Getenv("NEO4J_TEST_PASSWORD"))
	if err != nil {
		t.Skipf("neo4j unavailable (%v); run: docker compose up -d --wait", err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	return &handlers{
		embedder:         inference.New(envOr("TEI_TEST_URL", "http://localhost:8080")),
		reranker:         inference.NewReranker(envOr("TEI_RERANK_TEST_URL", "http://localhost:8081")),
		decay:            engram.TypeAwareDecay{},
		store:            store,
		clock:            fixedClock{},
		dedupThreshold:   defaultDedupThreshold,
		seedN:            defaultSeedN,
		rerankCandidates: defaultRerankCandidates,
		maxTokens:        defaultMaxTokens,
		wSim:             defaultWSim,
		wImp:             defaultWImp,
		wRet:             defaultWRet,
		softThreshold:    defaultSoftThresh,
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		newID:            newMemoryID,
	}
}

func mustRemember(t *testing.T, h *handlers, in rememberInput) rememberOutput {
	t.Helper()
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("remember %q: %v", in.Content, err)
	}
	return out
}

// TestM1RecallRanks is the M1 store->search demo: distinct memories go in, and a semantic
// query ranks the relevant one first. It also exercises the entity-write path.
func TestM1RecallRanks(t *testing.T) {
	h := liveHandlers(t)
	ns := string(uniqueNS("m1-rank"))
	paris := mustRemember(t, h, rememberInput{
		Content: "The capital of France is Paris.", Type: "semantic", Namespace: ns,
		Entities: []string{"France", "Paris"},
	})
	mustRemember(t, h, rememberInput{
		Content: "Goroutines are Go's lightweight concurrency primitive.", Type: "semantic", Namespace: ns,
	})

	out, err := h.doRecall(context.Background(),
		recallInput{Query: "What is the capital of France?", Namespaces: []string{ns}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("recall returned no results")
	}
	if out.Results[0].ID != paris.MemoryID {
		t.Errorf("top result = %q, want the Paris memory %q", out.Results[0].ID, paris.MemoryID)
	}
}

// TestM1Dedup verifies remembering identical content reinforces rather than duplicates.
func TestM1Dedup(t *testing.T) {
	h := liveHandlers(t)
	ns := string(uniqueNS("m1-dedup"))
	in := rememberInput{Content: "Engram deduplicates near-identical memories.", Type: "episodic", Namespace: ns}
	first := mustRemember(t, h, in)
	if first.Deduped {
		t.Fatal("first remember must not be deduped")
	}
	second := mustRemember(t, h, in)
	if !second.Deduped {
		t.Error("second remember of identical content should be deduped")
	}
	if second.MemoryID != first.MemoryID {
		t.Errorf("dedup id = %q, want %q", second.MemoryID, first.MemoryID)
	}
}

// TestM1NamespaceScoping verifies recall restricted to a namespace excludes others.
func TestM1NamespaceScoping(t *testing.T) {
	h := liveHandlers(t)
	nsA := string(uniqueNS("m1-scope-a"))
	nsB := string(uniqueNS("m1-scope-b"))
	a := mustRemember(t, h, rememberInput{Content: "Project Orca ships next quarter.", Type: "semantic", Namespace: nsA})

	outB, err := h.doRecall(context.Background(),
		recallInput{Query: "When does Orca ship?", Namespaces: []string{nsB}})
	if err != nil {
		t.Fatalf("recall nsB: %v", err)
	}
	for _, r := range outB.Results {
		if r.ID == a.MemoryID {
			t.Errorf("nsB-scoped recall leaked the nsA memory %q", a.MemoryID)
		}
	}

	outA, err := h.doRecall(context.Background(),
		recallInput{Query: "When does Orca ship?", Namespaces: []string{nsA}})
	if err != nil {
		t.Fatalf("recall nsA: %v", err)
	}
	found := false
	for _, r := range outA.Results {
		if r.ID == a.MemoryID {
			found = true
		}
	}
	if !found {
		t.Errorf("nsA-scoped recall did not find the memory %q", a.MemoryID)
	}
}
