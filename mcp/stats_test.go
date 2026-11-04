package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

// statsTools builds Tools over st for the stats handler, with the clock fixed at T.
func statsTools(st statsStore, d engram.DecayModel, opts ...Option) *Tools {
	t := &Tools{
		decay:         d,
		stats:         st,
		statsRowCap:   defaultStatsRowCap,
		clock:         fixedClock{},
		softThreshold: defaultSoftThresh,
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func putAll(t *testing.T, s *mock.MemStore, ms ...engram.Memory) {
	t.Helper()
	for _, m := range ms {
		if err := s.Put(context.Background(), m); err != nil {
			t.Fatalf("Put(%s): %v", m.ID, err)
		}
	}
}

func bucketSum(o MemoryStatsOutput) int {
	n := 0
	for _, b := range o.Retrievability.Buckets {
		n += b.Count
	}
	return n
}

func liveMem(id string) engram.Memory {
	return engram.Memory{ID: engram.MemoryID(id), Namespace: "a", Type: engram.Semantic, Content: "c", LastAccessed: fixedClock{}.Now()}
}

func TestDoMemoryStatsValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ns      string
		wantErr bool
	}{
		{"blank", "   ", true},
		{"oversize", strings.Repeat("x", maxNamespaceBytes+1), true},
		{"at limit", strings.Repeat("x", maxNamespaceBytes), false},
		{"omitted", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{}
			_, err := statsTools(st, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{Namespace: tt.ns})
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "1..256") {
					t.Fatalf("err = %v, want one naming the bounds", err)
				}
				if st.LastCountsNS != "" || st.LastLiveLimit != 0 {
					t.Error("store was called on invalid input")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDoMemoryStatsFlagsOverlap(t *testing.T) {
	t.Parallel()
	s := mock.NewMemStore()
	both := liveMem("1")
	both.Superseded, both.Forgotten = true, true
	pinned := liveMem("2")
	pinned.Pinned = true
	putAll(t, s, both, pinned, liveMem("3"))

	out, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	c := out.Counts
	if c.Total != 3 || c.Live != 2 || c.Superseded != 1 || c.Forgotten != 1 || c.Pinned != 1 {
		t.Errorf("counts = %+v, want total 3 live 2 superseded 1 forgotten 1 pinned 1", c)
	}
	if out.StoreSize.Memories != 3 || out.StoreSize.Memories != c.Total {
		t.Errorf("store_size.memories = %d, counts.total = %d, want both 3", out.StoreSize.Memories, c.Total)
	}
}

func TestDoMemoryStatsScoped(t *testing.T) {
	t.Parallel()
	s := mock.NewMemStore()
	b := liveMem("2")
	b.Namespace = "b"
	putAll(t, s, liveMem("1"), b)

	out, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{Namespace: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Counts.ByNamespace) != 1 || out.Counts.ByNamespace["a"].Semantic != 1 {
		t.Errorf("by_namespace = %+v, want only a with 1 semantic", out.Counts.ByNamespace)
	}
	if out.Counts.Total != 1 {
		t.Errorf("total = %d, want 1", out.Counts.Total)
	}
}

func TestDoMemoryStatsCapBoundary(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		rows      int
		truncated bool
	}{{2, false}, {3, true}} {
		s := mock.NewMemStore()
		for _, id := range []string{"1", "2", "3"}[:tt.rows] {
			putAll(t, s, liveMem(id))
		}
		h := statsTools(s, engram.TypeAwareDecay{})
		h.statsRowCap = 2
		out, err := h.doMemoryStats(context.Background(), MemoryStatsInput{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Retrievability.Truncated != tt.truncated || bucketSum(out) != 2 || out.Counts.Live != tt.rows {
			t.Errorf("rows=%d: truncated=%v sum=%d live=%d, want %v 2 %d",
				tt.rows, out.Retrievability.Truncated, bucketSum(out), out.Counts.Live, tt.truncated, tt.rows)
		}
	}
}

func TestDoMemoryStatsCountsExactAboveCap(t *testing.T) {
	t.Parallel()
	s := mock.NewMemStore()
	putAll(t, s, liveMem("1"), liveMem("2"), liveMem("3"))
	h := statsTools(s, engram.TypeAwareDecay{})
	h.statsRowCap = 2
	out, err := h.doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Counts.Live != 3 || !out.Retrievability.Truncated {
		t.Errorf("live = %d truncated = %v, want 3 true", out.Counts.Live, out.Retrievability.Truncated)
	}
}

func TestDoMemoryStatsLiveOnlyHistogram(t *testing.T) {
	t.Parallel()
	s := mock.NewMemStore()
	sup, forg := liveMem("2"), liveMem("3")
	sup.Superseded, forg.Forgotten = true, true
	putAll(t, s, liveMem("1"), sup, forg)
	out, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if bucketSum(out) != 1 || out.Counts.Live != 1 {
		t.Errorf("bucket sum = %d live = %d, want 1 1", bucketSum(out), out.Counts.Live)
	}
}

func TestDoMemoryStatsBelowRecallThreshold(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		age  time.Duration
		want int
	}{
		{"6 days counts", 6 * 24 * time.Hour, 1},
		{"5.9 days does not", 5*24*time.Hour + 22*time.Hour + 24*time.Minute, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := mock.NewMemStore()
			m := liveMem("1")
			m.Type, m.LastAccessed = engram.Episodic, fixedClock{}.Now().Add(-tt.age)
			putAll(t, s, m)
			out, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
			if err != nil {
				t.Fatal(err)
			}
			if out.Retrievability.BelowRecallThreshold != tt.want || out.Retrievability.Buckets[0].Count != 1 {
				t.Errorf("below = %d bucket0 = %d, want %d 1", out.Retrievability.BelowRecallThreshold, out.Retrievability.Buckets[0].Count, tt.want)
			}
		})
	}
}

func TestDoMemoryStatsCountsAndRowsDisagree(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{
		CountsRes: engram.StoreCounts{Groups: []engram.CountGroup{{Namespace: "a", Type: engram.Semantic, Total: 5, Live: 5}}},
		LiveRes:   []engram.Memory{liveMem("1"), liveMem("2"), liveMem("3")},
	}
	out, err := statsTools(st, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Retrievability.Truncated || bucketSum(out) != 3 {
		t.Errorf("truncated = %v sum = %d, want false 3", out.Retrievability.Truncated, bucketSum(out))
	}
}

func TestDoMemoryStatsNullDecay(t *testing.T) {
	t.Parallel()
	s := mock.NewMemStore()
	putAll(t, s, liveMem("1"), liveMem("2"))
	out, err := statsTools(s, engram.NullDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Retrievability.Buckets[9].Count != 2 || out.Retrievability.Decay != "none" || out.Retrievability.BelowRecallThreshold != 0 {
		t.Errorf("retrievability = %+v, want all in bucket 9, decay none, below 0", out.Retrievability)
	}
}

func TestDoMemoryStatsReadOnly(t *testing.T) {
	t.Parallel()
	fs := &mock.FakeStore{LiveRes: []engram.Memory{liveMem("1")}}
	if _, err := statsTools(fs, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Reinforced) != 0 || fs.LastPropagateID != "" {
		t.Errorf("FakeStore saw writes: reinforced %v propagate %q", fs.Reinforced, fs.LastPropagateID)
	}

	s := mock.NewMemStore()
	m := liveMem("1")
	m.LastAccessed = fixedClock{}.Now().Add(-24 * time.Hour)
	m.Stability, m.AccessCount = 3, 2
	putAll(t, s, m)
	before, _ := s.Snapshot("1")
	if _, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Snapshot("1")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("MemStore row changed: %+v -> %+v", before, after)
	}
}

func TestDoMemoryStatsSweep(t *testing.T) {
	t.Parallel()
	at := fixedClock{}.Now().Add(time.Hour)
	set := WithSweepStats(func() engram.SweepStats {
		return engram.SweepStats{LastRunAt: at, LastPruned: 2, TotalPruned: 7}
	})
	tests := []struct {
		name string
		h    *Tools
		ns   string
		want SweepReport
	}{
		{"no option", statsTools(&mock.FakeStore{}, engram.TypeAwareDecay{}), "", SweepReport{}},
		{"nil option ignored", statsTools(&mock.FakeStore{}, engram.TypeAwareDecay{}, WithSweepStats(nil)), "", SweepReport{}},
		{"set", statsTools(&mock.FakeStore{}, engram.TypeAwareDecay{}, set), "", SweepReport{LastRunAt: &at, LastPruned: 2, TotalPruned: 7}},
		{"namespace does not change it", statsTools(&mock.FakeStore{}, engram.TypeAwareDecay{}, set), "a", SweepReport{LastRunAt: &at, LastPruned: 2, TotalPruned: 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := tt.h.doMemoryStats(context.Background(), MemoryStatsInput{Namespace: tt.ns})
			if err != nil {
				t.Fatal(err)
			}
			got := out.Sweep
			if (got.LastRunAt == nil) != (tt.want.LastRunAt == nil) ||
				(got.LastRunAt != nil && !got.LastRunAt.Equal(*tt.want.LastRunAt)) ||
				got.LastPruned != tt.want.LastPruned || got.TotalPruned != tt.want.TotalPruned {
				t.Errorf("sweep = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDoMemoryStatsStoreErrorSanitized(t *testing.T) {
	t.Parallel()
	leak := errors.New("Neo.ClientError: MATCH (m:Memory) RETURN m failed")
	for name, st := range map[string]*mock.FakeStore{
		"counts": {CountsErr: leak},
		"live":   {LiveErr: leak},
	} {
		_, err := statsTools(st, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
		if err == nil || err.Error() != "memory_stats: store unavailable" {
			t.Errorf("%s: err = %v, want exactly %q", name, err, "memory_stats: store unavailable")
		}
	}
}

type panicStore struct{ statsStore }

func (panicStore) Counts(context.Context, engram.Namespace) (engram.StoreCounts, error) {
	panic("boom")
}

func TestDoMemoryStatsPanicRecovered(t *testing.T) {
	t.Parallel()
	_, err := statsTools(panicStore{}, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err == nil || err.Error() != "memory_stats: internal error" {
		t.Errorf("err = %v, want exactly %q", err, "memory_stats: internal error")
	}
}

func TestDoMemoryStatsBucketBounds(t *testing.T) {
	t.Parallel()
	out, err := statsTools(&mock.FakeStore{}, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{})
	if err != nil {
		t.Fatal(err)
	}
	b := out.Retrievability.Buckets
	if len(b) != 10 || b[3].Min != 0.3 || b[9].Max != 1 {
		t.Errorf("buckets len %d, [3].Min %v, [9].Max %v, want 10, 0.3, 1", len(b), b[3].Min, b[9].Max)
	}
}

func TestMemoryStatsOverTransport(t *testing.T) {
	t.Parallel()
	call := func(t *testing.T, opts []Option, args map[string]any) map[string]any {
		t.Helper()
		ctx := context.Background()
		tools := NewTools(&mock.FakeEmbedder{}, nil, engram.TypeAwareDecay{}, mock.NewMemStore(), fixedClock{}, opts...)
		ct, st := mcpsdk.NewInMemoryTransports()
		ss, err := NewServer(tools).Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ss.Close() }()
		cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cs.Close() }()

		lt, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range lt.Tools {
			names = append(names, tl.Name)
			if tl.Name == "memory_stats" && (tl.Annotations == nil || !tl.Annotations.ReadOnlyHint) {
				t.Error("memory_stats lacks ReadOnlyHint")
			}
		}
		if len(names) != 4 {
			t.Errorf("tools = %v, want exactly remember, recall, forget, memory_stats", names)
		}

		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "memory_stats", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("CallTool: err %v result %+v", err, res)
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	check := func(t *testing.T, got map[string]any, wantRunAt bool) {
		t.Helper()
		counts := got["counts"].(map[string]any)
		if bn, ok := counts["by_namespace"].(map[string]any); !ok || len(bn) != 0 {
			t.Errorf("by_namespace = %#v, want {}", counts["by_namespace"])
		}
		if bt := counts["by_type"].(map[string]any); len(bt) != 3 {
			t.Errorf("by_type = %#v, want episodic, semantic, procedural", bt)
		}
		if b := got["retrievability"].(map[string]any)["buckets"].([]any); len(b) != 10 {
			t.Errorf("buckets = %d, want 10", len(b))
		}
		sweep := got["sweep"].(map[string]any)
		runAt, present := sweep["last_run_at"]
		if !present {
			t.Fatal("last_run_at key missing")
		}
		if s, isStr := runAt.(string); wantRunAt != isStr || (isStr && s == "") {
			t.Errorf("last_run_at = %#v, want string=%v", runAt, wantRunAt)
		}
	}

	t.Run("unknown namespace", func(t *testing.T) {
		t.Parallel()
		check(t, call(t, nil, map[string]any{"namespace": "nope"}), false)
	})
	t.Run("empty store", func(t *testing.T) {
		t.Parallel()
		check(t, call(t, nil, nil), false)
	})
	t.Run("with sweep stats", func(t *testing.T) {
		t.Parallel()
		opt := WithSweepStats(func() engram.SweepStats { return engram.SweepStats{LastRunAt: fixedClock{}.Now()} })
		check(t, call(t, []Option{opt}, nil), true)
	})
}
