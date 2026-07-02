package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func testHandlers(emb *mock.FakeEmbedder, st *mock.FakeStore) *handlers {
	return &handlers{
		embedder:       emb,
		store:          st,
		clock:          fixedClock{},
		dedupThreshold: defaultDedupThreshold,
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		newID:          func() (engram.MemoryID, error) { return "test-id", nil },
	}
}

func validRemember() rememberInput {
	return rememberInput{Content: "hello world", Type: "semantic", Namespace: "work/eng"}
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
		mut  func(*rememberInput)
	}{
		{"invalid type", func(in *rememberInput) { in.Type = "reference" }},
		{"empty content", func(in *rememberInput) { in.Content = "" }},
		{"empty namespace", func(in *rememberInput) { in.Namespace = "" }},
		{"importance too high", func(in *rememberInput) { in.Importance = imp(1.5) }},
		{"importance negative", func(in *rememberInput) { in.Importance = imp(-0.1) }},
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
		{Memory: engram.Memory{ID: "n1"}, Score: 0.9}, // >= linkThreshold
		{Memory: engram.Memory{ID: "n2"}, Score: 0.7}, // below linkThreshold
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
	if len(edges) != 1 || edges[0].To != "n1" || edges[0].Weight != 0.9 {
		t.Errorf("auto-links = %+v, want [{n1 0.9}] (n2 below threshold filtered)", edges)
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
		SearchResults: []engram.RecallResult{{Memory: engram.Memory{ID: "n1"}, Score: 0.9}},
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
