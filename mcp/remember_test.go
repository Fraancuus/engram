package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func testHandlers(emb *mock.FakeEmbedder, st *mock.FakeStore) *Tools {
	return &Tools{
		embedder:         emb,
		reranker:         &mock.FakeReranker{},
		decay:            mock.FakeDecay{R: 1}, // nothing soft-forgotten by default
		store:            st,
		forget:           st,
		clock:            fixedClock{},
		dedupThreshold:   defaultDedupThreshold,
		seedN:            defaultSeedN,
		rerankCandidates: defaultRerankCandidates,
		maxTokens:        defaultMaxTokens,
		wSim:             1, // wImp/wRet 0 so unit-test scores stay the raw similarity
		wImp:             0,
		wRet:             0,
		softThreshold:    defaultSoftThresh,
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		newID:            func() (engram.MemoryID, error) { return "test-id", nil },
	}
}

func validRemember() RememberInput {
	return RememberInput{Content: "hello world", Type: "semantic", Namespace: "work/eng"}
}

func TestDoRememberSupersedesProcedural(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	in.Supersedes = []string{"old1"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if len(st.Superseded) != 1 || len(st.Superseded[0]) != 1 || st.Superseded[0][0] != "old1" {
		t.Errorf("Supersede not called with [old1]: %v", st.Superseded)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "old1" {
		t.Errorf("output.Superseded = %v, want [old1]", out.Superseded)
	}
}

func TestDoRememberSupersedesSemantic(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember() // semantic
	in.Supersedes = []string{"old1"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if len(st.Superseded) != 1 || len(st.Superseded[0]) != 1 || st.Superseded[0][0] != "old1" {
		t.Errorf("Supersede not called with [old1]: %v", st.Superseded)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "old1" {
		t.Errorf("output.Superseded = %v, want [old1]", out.Superseded)
	}
}

func TestDoRememberSupersedeIgnoredForEpisodic(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "episodic"
	in.Supersedes = []string{"old1"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if len(st.Superseded) != 0 {
		t.Errorf("Supersede should not run for an episodic memory: %v", st.Superseded)
	}
	if out.Superseded != nil {
		t.Errorf("output.Superseded = %v, want nil", out.Superseded)
	}
}

func cand(id string, score float64, superseded bool) engram.RecallResult {
	return engram.RecallResult{Memory: engram.Memory{ID: engram.MemoryID(id), Type: engram.Semantic, Superseded: superseded}, Score: score}
}

func TestDoRememberDedupSkipsNamedSupersede(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("old", 0.97, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped || len(st.Puts) != 1 {
		t.Errorf("Deduped=%v puts=%d, want false/1", out.Deduped, len(st.Puts))
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "old" {
		t.Errorf("Superseded = %v, want [old]", out.Superseded)
	}
	if slices.Contains(st.Reinforced, "old") {
		t.Errorf("old was reinforced: %v", st.Reinforced)
	}
}

func TestDoRememberDedupSkipsSuperseded(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("dead", 0.97, true)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped || len(st.Puts) != 1 || len(st.Reinforced) != 0 {
		t.Errorf("Deduped=%v puts=%d reinforced=%v, want false/1/none", out.Deduped, len(st.Puts), st.Reinforced)
	}
}

func fallThroughStore() *mock.FakeStore {
	return &mock.FakeStore{SearchResults: []engram.RecallResult{cand("old", 0.98, false), cand("live", 0.96, false)}}
}

func TestDoRememberDedupFallsToNextLive(t *testing.T) {
	t.Parallel()
	st := fallThroughStore()
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "live" || len(st.Puts) != 0 {
		t.Errorf("out=%+v puts=%d, want deduped into live with no Put", out, len(st.Puts))
	}
	if !slices.Contains(st.Reinforced, "live") || slices.Contains(st.Reinforced, "old") {
		t.Errorf("Reinforced = %v, want only live", st.Reinforced)
	}
}

func TestDoRememberDedupStillSupersedes(t *testing.T) {
	t.Parallel()
	st := fallThroughStore()
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if len(st.Superseded) != 1 || st.Superseded[0][0] != "old" {
		t.Errorf("Supersede not called with [old]: %v", st.Superseded)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "old" {
		t.Errorf("output.Superseded = %v, want [old]", out.Superseded)
	}
}

func TestDoRememberDedupBoundaryUnchanged(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("live", defaultDedupThreshold, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped {
		t.Errorf("Deduped = false at exactly the threshold")
	}
}

func TestDoRememberEpisodicSupersedesKeepsDedup(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("old", 0.97, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "episodic"
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || !slices.Contains(st.Reinforced, "old") || len(st.Superseded) != 0 {
		t.Errorf("out=%+v reinforced=%v superseded=%v, want deduped, old reinforced, nothing superseded", out, st.Reinforced, st.Superseded)
	}
}

func TestDoRememberRetriedCorrectionIdempotent(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("prior", 0.99, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "prior" {
		t.Errorf("out=%+v, want deduped into prior", out)
	}
	if len(st.Superseded) != 1 || st.Superseded[0][0] != "old" || len(out.Superseded) != 1 || out.Superseded[0] != "old" {
		t.Errorf("superseded=%v out=%v, want [old]", st.Superseded, out.Superseded)
	}
}

func TestDoRememberSupersedesValidation(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	in.Supersedes = []string{"  "}
	if _, err := h.doRemember(context.Background(), in); err == nil {
		t.Error("want validation error for a blank supersedes id")
	}
}

func TestDoRememberTooManySupersedes(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	in.Supersedes = make([]string, maxEntities+1)
	for i := range in.Supersedes {
		in.Supersedes[i] = "x"
	}
	if _, err := h.doRemember(context.Background(), in); err == nil {
		t.Error("want error for too many supersedes")
	}
}

func TestDoRememberSupersedeFailureDoesNotFailInsert(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SupersedeErr: errors.New("db-down")}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	in.Supersedes = []string{"old1"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("remember must succeed after a successful insert even if supersede fails: %v", err)
	}
	if out.MemoryID == "" || len(st.Puts) != 1 {
		t.Error("the memory should still have been stored")
	}
	if out.Superseded != nil {
		t.Errorf("Superseded = %v, want nil (supersession failed)", out.Superseded)
	}
}

func TestDoRememberSetsInitialStability(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	h.decay = mock.FakeDecay{R: 1, S: 42}
	if _, err := h.doRemember(context.Background(), validRemember()); err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if len(st.Puts) != 1 {
		t.Fatalf("Puts = %d, want 1", len(st.Puts))
	}
	if st.Puts[0].Stability != 42 {
		t.Errorf("inserted stability = %v, want 42 (from decay.Stability)", st.Puts[0].Stability)
	}
}

func TestDoRememberInserts(t *testing.T) {
	t.Parallel()
	emb := &mock.FakeEmbedder{Vec: engram.Vector{0.1, 0.2}}
	st := &mock.FakeStore{}
	h := testHandlers(emb, st)
	in := validRemember()
	in.Entities = []string{"PortIQ"}

	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped {
		t.Error("Deduped = true, want false")
	}
	if out.MemoryID != "test-id" {
		t.Errorf("MemoryID = %q, want test-id", out.MemoryID)
	}
	if len(st.Puts) != 1 {
		t.Fatalf("Puts = %d, want 1", len(st.Puts))
	}
	got := st.Puts[0]
	if got.Content != in.Content || got.Type != engram.Semantic || got.Namespace != engram.Namespace(in.Namespace) {
		t.Errorf("stored memory = %+v, want content/type/namespace from input", got)
	}
	if got.Importance != 0.5 {
		t.Errorf("Importance = %v, want default 0.5", got.Importance)
	}
	if !got.CreatedAt.Equal(fixedClock{}.Now()) {
		t.Errorf("CreatedAt = %v, want injected clock time", got.CreatedAt)
	}
	if names := st.Linked["test-id"]; len(names) != 1 || names[0] != "PortIQ" {
		t.Errorf("linked entities = %v, want [PortIQ]", names)
	}
	if emb.Last != in.Content {
		t.Errorf("embedded %q, want %q", emb.Last, in.Content)
	}
}

func TestDoRememberDedups(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "existing"}, Score: 0.97},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)

	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "existing" {
		t.Errorf("got {id:%q deduped:%v}, want {existing true}", out.MemoryID, out.Deduped)
	}
	if len(st.Puts) != 0 {
		t.Errorf("Puts = %d, want 0 (dedup must not insert)", len(st.Puts))
	}
	if len(st.Reinforced) != 1 || st.Reinforced[0] != "existing" {
		t.Errorf("Reinforced = %v, want [existing]", st.Reinforced)
	}
}

func TestDoRememberBelowThresholdInserts(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "existing"}, Score: 0.90},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped {
		t.Error("Deduped = true, want false (below threshold)")
	}
	if len(st.Puts) != 1 {
		t.Errorf("Puts = %d, want 1", len(st.Puts))
	}
}

func TestDoRememberValidation(t *testing.T) {
	t.Parallel()
	imp := func(f float64) *float64 { return &f }
	tests := []struct {
		name string
		mut  func(*RememberInput)
	}{
		{"invalid type", func(in *RememberInput) { in.Type = "reference" }},
		{"empty content", func(in *RememberInput) { in.Content = "" }},
		{"empty namespace", func(in *RememberInput) { in.Namespace = "" }},
		{"importance too high", func(in *RememberInput) { in.Importance = imp(1.5) }},
		{"importance negative", func(in *RememberInput) { in.Importance = imp(-0.1) }},
		{"whitespace content", func(in *RememberInput) { in.Content = "   " }},
		{"whitespace entity", func(in *RememberInput) { in.Entities = []string{"  "} }},
		{"source too long", func(in *RememberInput) { in.Source = strings.Repeat("s", 257) }},
		{"whitespace link", func(in *RememberInput) { in.Links = []string{"  "} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{}
			emb := &mock.FakeEmbedder{Vec: engram.Vector{1}}
			h := testHandlers(emb, st)
			in := validRemember()
			tt.mut(&in)
			if _, err := h.doRemember(context.Background(), in); err == nil {
				t.Fatal("want validation error, got nil")
			}
			if len(st.Puts) != 0 || emb.Last != "" {
				t.Errorf("validation failure still touched embedder/store (puts=%d, embedded=%q)", len(st.Puts), emb.Last)
			}
		})
	}
}

func TestDoRememberImportanceProvided(t *testing.T) {
	t.Parallel()
	imp := 0.8
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Importance = &imp
	if _, err := h.doRemember(context.Background(), in); err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if st.Puts[0].Importance != 0.8 {
		t.Errorf("Importance = %v, want 0.8", st.Puts[0].Importance)
	}
}

func TestDoRememberEmbedErrorIsSanitized(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{Err: errors.New("sidecar-internal-detail")}, st)
	_, err := h.doRemember(context.Background(), validRemember())
	if err == nil {
		t.Fatal("want error on embed failure")
	}
	if strings.Contains(err.Error(), "sidecar-internal-detail") {
		t.Errorf("error leaks internal detail: %q", err.Error())
	}
	if len(st.Puts) != 0 {
		t.Error("must not Put when embed fails")
	}
}

func TestDoRememberAutoLinks(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "n1"}, Score: 0.93}, // >= linkThreshold
		{Memory: engram.Memory{ID: "n2"}, Score: 0.7},  // below linkThreshold
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped {
		t.Fatal("should insert, not dedup")
	}
	edges := st.LinkedEdges["test-id"]
	if len(edges) != 1 || edges[0].To != "n1" || edges[0].Weight != 0.93 {
		t.Errorf("auto-links = %+v, want [{n1 0.93}] (n2 below threshold filtered)", edges)
	}
}

func TestDoRememberExplicitLinks(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{} // no auto-link candidates
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Links = []string{"x", "y"}
	if _, err := h.doRemember(context.Background(), in); err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	edges := st.LinkedEdges["test-id"]
	if len(edges) != 2 || edges[0].To != "x" || edges[0].Weight != 1.0 || edges[1].To != "y" {
		t.Errorf("explicit links = %+v, want [{x 1} {y 1}]", edges)
	}
}

func TestDoRememberDedupSkipsAutoLink(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "existing"}, Score: 0.97},
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped {
		t.Fatal("want dedup")
	}
	if len(st.Puts) != 0 {
		t.Error("dedup must not Put")
	}
	if len(st.LinkedEdges) != 0 {
		t.Error("dedup must not create link edges")
	}
}

func TestDoRememberLinkErrorIsSanitized(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{{Memory: engram.Memory{ID: "n1"}, Score: 0.93}},
		LinkEdgesErr:  errors.New("db-internal-detail"),
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	_, err := h.doRemember(context.Background(), validRemember())
	if err == nil {
		t.Fatal("want error when Link fails")
	}
	if strings.Contains(err.Error(), "db-internal-detail") {
		t.Errorf("leaks internal detail: %q", err.Error())
	}
}

func TestDoRememberAutoLinkAtThreshold(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{
		{Memory: engram.Memory{ID: "n1"}, Score: 0.925},  // exactly the link point -> links (>=)
		{Memory: engram.Memory{ID: "n2"}, Score: 0.9249}, // just under -> no link
	}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	if _, err := h.doRemember(context.Background(), validRemember()); err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	edges := st.LinkedEdges["test-id"]
	if len(edges) != 1 || edges[0].To != "n1" || edges[0].Weight != 0.925 {
		t.Errorf("auto-links = %+v, want only [{n1 0.925}]", edges)
	}
}

// vecEmbedder maps content to a fixed vector, so a real MemStore sees controlled similarities.
type vecEmbedder map[string]engram.Vector

func (e vecEmbedder) Embed(_ context.Context, text string) (engram.Vector, error) {
	return e[text], nil
}

// seqIDs mints "id1", "id2", ... for deterministic ids.
func seqIDs() func() (engram.MemoryID, error) {
	n := 0
	return func() (engram.MemoryID, error) {
		n++
		return engram.MemoryID(fmt.Sprintf("id%d", n)), nil
	}
}

func TestDoRememberDedupSkipsForgotten(t *testing.T) {
	t.Parallel()
	c := cand("gone", 0.97, false)
	c.Forgotten = true
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{c}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped || len(st.Puts) != 1 || len(st.Reinforced) != 0 {
		t.Errorf("Deduped=%v puts=%d reinforced=%v, want false/1/none", out.Deduped, len(st.Puts), st.Reinforced)
	}
}

func TestRememberCorrectionNearForgottenIsRecallable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := mock.NewMemStore()
	emb := vecEmbedder{
		"old":        {0, 0, 1},
		"F":          {1, 0, 0},
		"correction": {0.95, 0, 0.3122}, // cos 0.95 against F: index score ~0.975
	}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, fixedClock{}, WithIDGenerator(seqIDs()), WithoutRerank())
	for _, c := range []string{"old", "F"} {
		if _, err := h.Remember(ctx, RememberInput{Content: c, Type: "semantic", Namespace: "ns"}); err != nil {
			t.Fatalf("remember %s: %v", c, err)
		}
	}
	if _, err := h.Forget(ctx, ForgetInput{ID: "id2", Mode: "soft"}); err != nil {
		t.Fatalf("forget: %v", err)
	}
	out, err := h.Remember(ctx, RememberInput{Content: "correction", Type: "semantic", Namespace: "ns", Supersedes: []string{"id1"}})
	if err != nil {
		t.Fatalf("remember correction: %v", err)
	}
	if out.Deduped {
		t.Fatal("correction deduped into a soft-forgotten memory and was lost")
	}
	rec, err := h.Recall(ctx, RecallInput{Query: "correction", Namespaces: []string{"ns"}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(rec.Results) == 0 || rec.Results[0].ID != out.MemoryID {
		t.Errorf("recall = %+v, want the correction %s first", rec.Results, out.MemoryID)
	}
	if m, _ := st.Snapshot("id1"); !m.Superseded {
		t.Error("old was not superseded")
	}
	if m, _ := st.Snapshot("id2"); !m.Forgotten {
		t.Error("F lost its Forgotten flag")
	}
}

func TestDoRememberReplacementSkipsOtherType(t *testing.T) {
	t.Parallel()
	c := cand("ep", 1.0, false)
	c.Type = engram.Episodic
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{c}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	in.Supersedes = []string{"std"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped || len(st.Puts) != 1 || len(st.Reinforced) != 0 {
		t.Errorf("Deduped=%v puts=%d reinforced=%v, want false/1/none", out.Deduped, len(st.Puts), st.Reinforced)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "std" {
		t.Errorf("Superseded = %v, want [std]", out.Superseded)
	}
}

func TestDoRememberCrossTypeDedupWithoutReplaces(t *testing.T) {
	t.Parallel()
	c := cand("ep", 0.97, false)
	c.Type = engram.Episodic
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{c}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Type = "procedural"
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "ep" {
		t.Errorf("out = %+v, want deduped into ep", out)
	}
}

func TestDoRememberSupersedeFailureOnDedup(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SupersedeErr: errors.New("db-down"), SearchResults: []engram.RecallResult{cand("live", 0.99, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.Superseded != nil || !slices.Contains(st.Reinforced, "live") {
		t.Errorf("out = %+v reinforced=%v, want deduped, no Superseded, live reinforced", out, st.Reinforced)
	}
}

func TestDoRememberAbsentSupersedeTarget(t *testing.T) {
	t.Parallel()
	st := mock.NewMemStore()
	emb := vecEmbedder{"hello world": {1, 0}}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, fixedClock{}, WithIDGenerator(seqIDs()), WithoutRerank())
	in := validRemember()
	in.Supersedes = []string{"missing"}
	out, err := h.Remember(context.Background(), in)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, ok := st.Snapshot(engram.MemoryID(out.MemoryID)); !ok || out.Deduped {
		t.Error("memory was not stored")
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "missing" {
		t.Errorf("Superseded = %v, want [missing]", out.Superseded)
	}
}

func TestDoRememberDedupStopsAtThresholdAfterSkip(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("old", 0.98, false), cand("related", 0.9499, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Deduped || len(st.Puts) != 1 || slices.Contains(st.Reinforced, "related") {
		t.Errorf("Deduped=%v puts=%d reinforced=%v, want false/1/related untouched", out.Deduped, len(st.Puts), st.Reinforced)
	}
}

func TestDoRememberNoDetection(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{cand("older", 0.94, false)}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	out, err := h.doRemember(context.Background(), validRemember())
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if out.Superseded != nil || len(st.Superseded) != 0 {
		t.Errorf("Superseded = %v / %v, want none without an explicit supersedes", out.Superseded, st.Superseded)
	}
}

func TestAutoLinkSkipsForgotten(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := mock.NewMemStore()
	emb := vecEmbedder{"F": {1, 0, 0}, "near": {0.95, 0, 0.3122}} // cos 0.95: above the auto-link point
	now := time.Unix(1_700_000_000, 0).UTC()
	clk := &steppedClock{t: now}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, clk, WithIDGenerator(seqIDs()), WithoutRerank())
	if _, err := h.Remember(ctx, RememberInput{Content: "F", Type: "semantic", Namespace: "ns"}); err != nil {
		t.Fatalf("remember F: %v", err)
	}
	if _, err := h.Forget(ctx, ForgetInput{ID: "id1", Mode: "soft"}); err != nil {
		t.Fatalf("forget: %v", err)
	}
	before, ok := st.Snapshot("id1")
	if !ok {
		t.Fatal("snapshot before near write: id1 missing")
	}
	clk.advance(24 * time.Hour)
	if _, err := h.Remember(ctx, RememberInput{Content: "near", Type: "semantic", Namespace: "ns"}); err != nil {
		t.Fatalf("remember near: %v", err)
	}
	clk.advance(24 * time.Hour)
	got, err := h.Recall(ctx, RecallInput{Query: "near", Namespaces: []string{"ns"}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got.Results) == 0 || got.Results[0].Content != "near" {
		t.Fatalf("recall = %+v, want the near copy first", got.Results)
	}
	after, ok := st.Snapshot("id1")
	if !ok {
		t.Fatal("snapshot after recall: id1 missing")
	}
	if !after.LastAccessed.Equal(before.LastAccessed) {
		t.Errorf("forgotten memory LastAccessed %v -> %v, want unchanged", before.LastAccessed, after.LastAccessed)
	}
}

// steppedClock is a test clock whose time only moves when advance is called.
type steppedClock struct{ t time.Time }

func (c *steppedClock) Now() time.Time          { return c.t }
func (c *steppedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestDoRememberReplacementWalksPastOtherType(t *testing.T) {
	t.Parallel()
	ep := cand("ep", 1.0, false)
	ep.Type = engram.Episodic
	sem := cand("sem", 0.97, false)
	sem.Type = engram.Semantic
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{ep, sem}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"std"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "sem" {
		t.Errorf("out = %+v, want deduped into sem", out)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "std" {
		t.Errorf("Superseded = %v, want [std]", out.Superseded)
	}
	if len(st.Reinforced) != 1 || st.Reinforced[0] != "sem" {
		t.Errorf("Reinforced = %v, want exactly [sem]", st.Reinforced)
	}
	if len(st.Superseded) == 0 || !slices.Contains(st.Superseded[0], "std") {
		t.Errorf("store Superseded = %v, want a call containing std", st.Superseded)
	}
}
