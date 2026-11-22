package mcp

import (
	"bytes"
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
	return engram.RecallResult{Memory: engram.Memory{ID: engram.MemoryID(id), Type: engram.Semantic, Content: validRemember().Content, Superseded: superseded}, Score: score}
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
	if !out.Deduped || out.MemoryID != "live" {
		t.Errorf("out = %+v, want deduped into live at exactly the threshold", out)
	}
	// The existing memory carries the content: it is reinforced and nothing new is stored.
	if !slices.Equal(st.Reinforced, []engram.MemoryID{"live"}) || len(st.Puts) != 0 {
		t.Errorf("reinforced=%v puts=%d, want [live] and no Put", st.Reinforced, len(st.Puts))
	}
}

func TestDoRememberStoresContentWhenDedupTargetIsGone(t *testing.T) {
	t.Parallel()
	gone := fmt.Errorf("reinforce %q: %w", "gone", engram.ErrNotFound) // wrapped, as both stores return it
	for _, tc := range []struct {
		name           string
		cands          []engram.RecallResult
		supersedes     []string
		wantSuperseded []string
		wantStoreSup   string // fmt.Sprint of FakeStore.Superseded
		entities       []string
	}{
		{name: "target gone", cands: []engram.RecallResult{cand("gone", 0.97, false)},
			wantStoreSup: "[]"},
		{name: "target gone with entities", cands: []engram.RecallResult{cand("gone", 0.97, false)},
			wantStoreSup: "[]", entities: []string{"Ada"}},
		// The correction dedups into its own earlier insert, which the sweep deleted.
		{name: "target gone on a correction", cands: []engram.RecallResult{cand("gone", 0.99, false)},
			supersedes: []string{"old"}, wantSuperseded: []string{"old"}, wantStoreSup: "[[old]]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{SearchResults: tc.cands, ReinforceErrs: map[engram.MemoryID]error{"gone": gone}}
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
			var logs bytes.Buffer
			h.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			in := validRemember()
			in.Supersedes = tc.supersedes
			in.Entities = tc.entities
			out, err := h.doRemember(context.Background(), in)
			if err != nil {
				t.Fatalf("doRemember: %v; want the content stored when the dedup target is gone", err)
			}
			if !slices.Equal(st.Reinforced, []engram.MemoryID{"gone"}) {
				t.Errorf("Reinforced = %v, want exactly [gone]", st.Reinforced)
			}
			if n := strings.Count(logs.String(), "level=DEBUG"); n != 1 {
				t.Errorf("%d debug records, want 1; log: %q", n, logs.String())
			}
			if len(tc.entities) > 0 {
				if !slices.Equal(st.Linked["test-id"], tc.entities) {
					t.Errorf("Linked[test-id] = %v, want %v", st.Linked["test-id"], tc.entities)
				}
				if _, ok := st.Linked["gone"]; ok || len(st.Linked) != 1 {
					t.Errorf("Linked = %v, want only the new id", st.Linked)
				}
			}
			if out.Deduped || out.MemoryID != "test-id" {
				t.Errorf("out = %+v, want the new id test-id and deduped false", out)
			}
			if len(st.Puts) != 1 || st.Puts[0].Content != in.Content {
				t.Errorf("Puts = %+v, want one memory holding %q", st.Puts, in.Content)
			}
			if !slices.Equal(out.Superseded, tc.wantSuperseded) || fmt.Sprint(st.Superseded) != tc.wantStoreSup {
				t.Errorf("superseded: reply %v, store %v; want %v, %s", out.Superseded, st.Superseded, tc.wantSuperseded, tc.wantStoreSup)
			}
		})
	}
}

func TestDoRememberTargetGoneBesideSecondCandidate(t *testing.T) {
	t.Parallel()
	// Only the first candidate is gone. A handler that walked on would dedup into other.
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{cand("gone", 0.97, false), cand("other", 0.96, false)},
		ReinforceErrs: map[engram.MemoryID]error{"gone": fmt.Errorf("reinforce %q: %w", "gone", engram.ErrNotFound)},
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v; want the content stored when the dedup target is gone", err)
	}
	if out.Deduped || out.MemoryID != "test-id" {
		t.Errorf("out = %+v, want the new id test-id and deduped false", out)
	}
	if len(st.Puts) != 1 || st.Puts[0].Content != in.Content {
		t.Errorf("Puts = %+v, want one memory holding %q", st.Puts, in.Content)
	}
	if !slices.Equal(st.Reinforced, []engram.MemoryID{"gone"}) {
		t.Errorf("Reinforced = %v, want [gone]: the second candidate is not reinforced", st.Reinforced)
	}
}

func TestDoRememberReinforceFailureStillFails(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		SearchResults: []engram.RecallResult{cand("existing", 0.97, false)},
		ReinforceErr:  errors.New("db-down"),
	}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	_, err := h.doRemember(context.Background(), in)
	if err == nil || err.Error() != "remember: store unavailable" {
		t.Fatalf("err = %v, want remember: store unavailable", err)
	}
	if len(st.Puts) != 0 || len(st.Superseded) != 0 {
		t.Errorf("puts=%d superseded=%v, want neither: the store refused the write", len(st.Puts), st.Superseded)
	}
}

// sweptStore deletes victim just before Reinforce reaches it, as a sweep pass that wins the race
// between remember's search and its reinforce would. It simulates the race on a real MemStore;
// it does not script results.
type sweptStore struct {
	*mock.MemStore
	victim engram.MemoryID
}

func (s *sweptStore) Reinforce(ctx context.Context, id engram.MemoryID, now time.Time) error {
	if id == s.victim {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
		s.victim = ""
	}
	return s.MemStore.Reinforce(ctx, id, now)
}

func TestRememberDedupTargetGoneIsRecallable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &sweptStore{MemStore: mock.NewMemStore()}
	emb := vecEmbedder{"fact": {1, 0, 0}}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, fixedClock{}, WithIDGenerator(seqIDs()), WithoutRerank())
	first, err := h.Remember(ctx, RememberInput{Content: "fact", Type: "semantic", Namespace: "ns"})
	if err != nil {
		t.Fatalf("first remember: %v", err)
	}
	st.victim = engram.MemoryID(first.MemoryID)
	out, err := h.Remember(ctx, RememberInput{Content: "fact", Type: "semantic", Namespace: "ns"})
	if err != nil {
		t.Fatalf("remember after its dedup target was deleted: %v", err)
	}
	if out.Deduped || out.MemoryID == "" || out.MemoryID == first.MemoryID {
		t.Fatalf("out = %+v, want a new memory, not deduped into %s", out, first.MemoryID)
	}
	if m, ok := st.Snapshot(engram.MemoryID(out.MemoryID)); !ok || m.Content != "fact" {
		t.Errorf("stored %+v (found %v), want the content fact", m, ok)
	}
	rec, err := h.Recall(ctx, RecallInput{Query: "fact", Namespaces: []string{"ns"}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(rec.Results) != 1 || rec.Results[0].ID != out.MemoryID {
		t.Errorf("recall = %+v, want only the new memory %s", rec.Results, out.MemoryID)
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
	many := func() []string { return make([]string, maxEntities+1) }
	long := func() []string { return []string{strings.Repeat("x", maxEntityBytes+1)} }
	countMsg := func(field string) string { return fmt.Sprintf("at most %d %s allowed", maxEntities, field) }
	itemMsg := func(what string) string {
		return fmt.Sprintf("%s must be 1..%d bytes and not blank", what, maxEntityBytes)
	}
	contentMsg := fmt.Sprintf("content must be 1..%d bytes and not blank", maxContentBytes)
	nsMsg := fmt.Sprintf("namespace must be 1..%d bytes and not blank", maxNamespaceBytes)
	typeMsg := `invalid type "reference": want episodic, semantic, or procedural`
	tests := []struct {
		name string
		mut  func(*RememberInput)
		want string
	}{
		{"invalid type", func(in *RememberInput) { in.Type = "reference" }, typeMsg},
		{"empty content", func(in *RememberInput) { in.Content = "" }, contentMsg},
		{"empty namespace", func(in *RememberInput) { in.Namespace = "" }, nsMsg},
		{"importance too high", func(in *RememberInput) { in.Importance = imp(1.5) }, "importance must be a number between 0 and 1"},
		{"importance negative", func(in *RememberInput) { in.Importance = imp(-0.1) }, "importance must be a number between 0 and 1"},
		{"whitespace content", func(in *RememberInput) { in.Content = "   " }, contentMsg},
		{"whitespace entity", func(in *RememberInput) { in.Entities = []string{"  "} }, itemMsg("entity names")},
		{"source too long", func(in *RememberInput) { in.Source = strings.Repeat("s", maxSourceBytes+1) }, fmt.Sprintf("source must be at most %d bytes", maxSourceBytes)},
		{"whitespace link", func(in *RememberInput) { in.Links = []string{"  "} }, itemMsg("link ids")},
		{"too many entities", func(in *RememberInput) { in.Entities = many() }, countMsg("entities")},
		{"too many links", func(in *RememberInput) { in.Links = many() }, countMsg("links")},
		{"too many supersedes", func(in *RememberInput) { in.Supersedes = many() }, countMsg("supersedes")},
		{"entity too long", func(in *RememberInput) { in.Entities = long() }, itemMsg("entity names")},
		{"link too long", func(in *RememberInput) { in.Links = long() }, itemMsg("link ids")},
		{"supersedes too long", func(in *RememberInput) { in.Supersedes = long() }, itemMsg("supersedes ids")},
		{"blank entity", func(in *RememberInput) { in.Entities = []string{""} }, itemMsg("entity names")},
		{"blank link", func(in *RememberInput) { in.Links = []string{""} }, itemMsg("link ids")},
		{"blank supersedes", func(in *RememberInput) { in.Supersedes = []string{""} }, itemMsg("supersedes ids")},
		{"type is checked before content", func(in *RememberInput) { in.Type = "reference"; in.Content = " " }, typeMsg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{}
			emb := &mock.FakeEmbedder{Vec: engram.Vector{1}}
			h := testHandlers(emb, st)
			in := validRemember()
			tt.mut(&in)
			_, err := h.doRemember(context.Background(), in)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if len(st.Puts) != 0 || emb.Last != "" {
				t.Errorf("validation failure still touched embedder/store (puts=%d, embedded=%q)", len(st.Puts), emb.Last)
			}
		})
	}
}

// putPanicStore panics in Put, which FakeStore cannot do on demand.
type putPanicStore struct{ *mock.FakeStore }

func (putPanicStore) Put(context.Context, engram.Memory) error { panic("boom") }

// notRun fails the test when a store call that must not follow the failure was recorded.
func notRun(t *testing.T, call string, forbidden bool, n int) {
	t.Helper()
	if forbidden && n != 0 {
		t.Errorf("%s ran %d time(s) after the failure", call, n)
	}
}

func TestDoRememberFailurePaths(t *testing.T) {
	t.Parallel()
	const storeErr, internalErr = "remember: store unavailable", "remember: internal error"
	boom := errors.New("db-down")
	tests := []struct {
		name  string
		st    *mock.FakeStore
		wrap  func(*mock.FakeStore) Store // nil: use st itself
		newID func() (engram.MemoryID, error)
		in    func(*RememberInput)
		want  string
		// what must NOT have run after the failure
		noReinforce, noPut, noLinkEntities, noLink, noSupersede bool
	}{
		{
			name: "search fails", st: &mock.FakeStore{SearchErr: boom},
			want: storeErr, noReinforce: true, noPut: true,
		},
		{
			name: "put fails", st: &mock.FakeStore{PutErr: boom},
			in: func(in *RememberInput) {
				in.Entities = []string{"e"}
				in.Links = []string{"l"}
				in.Supersedes = []string{"old"}
			},
			want: storeErr, noLinkEntities: true, noLink: true, noSupersede: true,
		},
		{
			name: "link entities fails after put", st: &mock.FakeStore{LinkErr: boom},
			in: func(in *RememberInput) {
				in.Entities = []string{"e"}
				in.Links = []string{"l"}
				in.Supersedes = []string{"old"}
			},
			want: storeErr, noLink: true, noSupersede: true,
		},
		{
			name: "link fails after put", st: &mock.FakeStore{LinkEdgesErr: boom},
			in:   func(in *RememberInput) { in.Links = []string{"l"}; in.Supersedes = []string{"old"} },
			want: storeErr, noSupersede: true,
		},
		{
			name: "link entities fails after reinforce",
			st:   &mock.FakeStore{SearchResults: []engram.RecallResult{cand("existing", 0.97, false)}, LinkErr: boom},
			in:   func(in *RememberInput) { in.Entities = []string{"e"}; in.Supersedes = []string{"old"} },
			want: storeErr, noPut: true, noSupersede: true,
		},
		{
			name: "id generation fails", st: &mock.FakeStore{},
			newID: func() (engram.MemoryID, error) { return "", boom },
			want:  internalErr, noPut: true,
		},
		{
			name: "put panics", st: &mock.FakeStore{},
			wrap: func(f *mock.FakeStore) Store { return putPanicStore{f} },
			want: internalErr, noLinkEntities: true, noLink: true, noSupersede: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, tt.st)
			if tt.wrap != nil {
				h.store = tt.wrap(tt.st)
			}
			if tt.newID != nil {
				h.newID = tt.newID
			}
			in := validRemember()
			if tt.in != nil {
				tt.in(&in)
			}
			_, err := h.doRemember(context.Background(), in)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			st := tt.st
			notRun(t, "Reinforce", tt.noReinforce, len(st.Reinforced))
			notRun(t, "Put", tt.noPut, len(st.Puts))
			notRun(t, "LinkEntities", tt.noLinkEntities, len(st.Linked))
			notRun(t, "Link", tt.noLink, len(st.LinkedEdges))
			notRun(t, "Supersede", tt.noSupersede, len(st.Superseded))
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

func TestDoRememberCorrectionNotAbsorbedByOtherContent(t *testing.T) {
	t.Parallel()
	live := cand("live", 0.97, false)
	live.Content = "something else entirely"
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{live}}
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
	if slices.Contains(st.Reinforced, "live") {
		t.Errorf("live was reinforced: %v", st.Reinforced)
	}
}

func TestDoRememberRetryBehindOtherContent(t *testing.T) {
	t.Parallel()
	other := cand("other", 0.99, false)
	other.Content = "something else entirely"
	prior := cand("prior", 0.97, false)
	prior.Content = validRemember().Content
	st := &mock.FakeStore{SearchResults: []engram.RecallResult{other, prior}}
	h := testHandlers(&mock.FakeEmbedder{Vec: engram.Vector{1}}, st)
	in := validRemember()
	in.Supersedes = []string{"old"}
	out, err := h.doRemember(context.Background(), in)
	if err != nil {
		t.Fatalf("doRemember: %v", err)
	}
	if !out.Deduped || out.MemoryID != "prior" {
		t.Errorf("out = %+v, want deduped into prior", out)
	}
	if len(st.Reinforced) != 1 || st.Reinforced[0] != "prior" {
		t.Errorf("Reinforced = %v, want exactly [prior]", st.Reinforced)
	}
	if len(out.Superseded) != 1 || out.Superseded[0] != "old" {
		t.Errorf("Superseded = %v, want [old]", out.Superseded)
	}
}

func TestRememberCorrectionNearLiveMemoryIsRecallable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := mock.NewMemStore()
	emb := vecEmbedder{
		"old":        {0, 0, 1},
		"live":       {1, 0, 0},
		"correction": {0.95, 0, 0.3122}, // cos 0.95 against live: index score ~0.975
	}
	h := NewTools(emb, nil, engram.TypeAwareDecay{}, st, fixedClock{}, WithIDGenerator(seqIDs()), WithoutRerank())
	for _, c := range []string{"old", "live"} {
		if _, err := h.Remember(ctx, RememberInput{Content: c, Type: "semantic", Namespace: "ns"}); err != nil {
			t.Fatalf("remember %s: %v", c, err)
		}
	}
	before, ok := st.Snapshot("id2")
	if !ok {
		t.Fatal("snapshot before correction: id2 missing")
	}
	out, err := h.Remember(ctx, RememberInput{Content: "correction", Type: "semantic", Namespace: "ns", Supersedes: []string{"id1"}})
	if err != nil {
		t.Fatalf("remember correction: %v", err)
	}
	if out.Deduped {
		t.Fatal("correction deduped into a different live memory and was lost")
	}
	if m, ok := st.Snapshot("id1"); !ok || !m.Superseded {
		t.Errorf("old not superseded: %+v (found %v)", m, ok)
	}
	// Checked before Recall, which itself reinforces what it returns.
	after, ok := st.Snapshot("id2")
	if !ok {
		t.Fatal("snapshot after correction: id2 missing")
	}
	if after.AccessCount != before.AccessCount || after.Superseded {
		t.Errorf("live touched: before %+v after %+v", before, after)
	}
	rec, err := h.Recall(ctx, RecallInput{Query: "correction", Namespaces: []string{"ns"}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(rec.Results) == 0 || rec.Results[0].ID != out.MemoryID {
		t.Errorf("recall = %+v, want the correction %s first", rec.Results, out.MemoryID)
	}
}
