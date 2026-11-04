package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"reflect"
	"sort"
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
		{"5.9 days does not", 5*24*time.Hour + 21*time.Hour + 36*time.Minute, 0},
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
			if len(out.Retrievability.Buckets) != 10 {
				t.Fatalf("buckets = %d, want 10", len(out.Retrievability.Buckets))
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
	if len(out.Retrievability.Buckets) != 10 {
		t.Fatalf("buckets = %d, want 10", len(out.Retrievability.Buckets))
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
	before, ok := s.Snapshot("1")
	if !ok {
		t.Fatal("seeded row missing before")
	}
	if _, err := statsTools(s, engram.TypeAwareDecay{}).doMemoryStats(context.Background(), MemoryStatsInput{}); err != nil {
		t.Fatal(err)
	}
	after, ok := s.Snapshot("1")
	if !ok {
		t.Fatal("seeded row missing after")
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("MemStore row changed: %+v -> %+v", before, after)
	}
}

func TestDoMemoryStatsSweep(t *testing.T) {
	t.Parallel()
	// A local-zone LastRunAt, as the system clock yields, must come out in UTC.
	at := fixedClock{}.Now().Add(time.Hour).In(time.FixedZone("local", 5*3600))
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
			if got.LastRunAt != nil && got.LastRunAt.Location() != time.UTC {
				t.Errorf("last_run_at zone = %v, want UTC", got.LastRunAt.Location())
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

func TestDoMemoryStatsFoldAndStoreArgs(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{CountsRes: engram.StoreCounts{
		Groups: []engram.CountGroup{
			{Namespace: "a", Type: engram.Episodic, Total: 1, Live: 1},
			{Namespace: "a", Type: engram.Semantic, Total: 2, Live: 1, Superseded: 1},
			{Namespace: "b", Type: engram.Procedural, Total: 4, Live: 3, Forgotten: 1, Pinned: 2},
			{Namespace: "b", Type: engram.Episodic, Total: 8, Live: 8},
		},
		LinkEdges: 5, EntityBridges: 6,
	}}
	h := statsTools(st, engram.TypeAwareDecay{})
	h.statsRowCap = 7
	out, err := h.doMemoryStats(context.Background(), MemoryStatsInput{Namespace: "a"})
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := MemoryCounts{
		Total: 15, Live: 13, Superseded: 1, Forgotten: 1, Pinned: 2,
		ByType: TypeCounts{Episodic: 9, Semantic: 2, Procedural: 4},
		ByNamespace: map[string]TypeCounts{
			"a": {Episodic: 1, Semantic: 2},
			"b": {Episodic: 8, Procedural: 4},
		},
	}
	if !reflect.DeepEqual(out.Counts, wantCounts) {
		t.Errorf("counts = %+v, want %+v", out.Counts, wantCounts)
	}
	if want := (StoreSize{Memories: 15, LinkEdges: 5, EntityBridges: 6}); out.StoreSize != want {
		t.Errorf("store_size = %+v, want %+v", out.StoreSize, want)
	}
	if st.LastCountsNS != "a" || st.LastLiveNS != "a" || st.LastLiveLimit != 8 {
		t.Errorf("store args: counts ns %q, live ns %q, limit %d; want a, a, 8", st.LastCountsNS, st.LastLiveNS, st.LastLiveLimit)
	}
}

func TestMemoryStatsOverTransport(t *testing.T) {
	t.Parallel()
	call := func(t *testing.T, opts []Option, args map[string]any) map[string]any {
		t.Helper()
		ctx := context.Background()
		ms := mock.NewMemStore()
		putAll(t, ms, liveMem("1")) // namespace "a": an unknown namespace must differ from an empty store
		tools := NewTools(&mock.FakeEmbedder{}, nil, engram.TypeAwareDecay{}, ms, fixedClock{}, opts...)
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
		sort.Strings(names)
		if want := []string{"forget", "memory_stats", "recall", "remember"}; !reflect.DeepEqual(names, want) {
			t.Errorf("tools = %v, want %v", names, want)
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

	// want builds the full wire shape for total live semantic rows in namespace a.
	want := func(total int, byNS map[string]any, buckets map[int]float64, lastRunAt any) map[string]any {
		bs := make([]any, 10)
		for i := range bs {
			bs[i] = map[string]any{"min": float64(i) / 10, "max": float64(i+1) / 10, "count": buckets[i]}
		}
		live := float64(total)
		return map[string]any{
			"counts": map[string]any{
				"total": live, "live": live, "superseded": 0.0, "forgotten": 0.0, "pinned": 0.0,
				"by_type":      map[string]any{"episodic": 0.0, "semantic": live, "procedural": 0.0},
				"by_namespace": byNS,
			},
			"retrievability": map[string]any{
				"decay": "type-aware", "buckets": bs, "below_recall_threshold": 0.0, "truncated": false,
			},
			"store_size": map[string]any{"memories": live, "link_edges": 0.0, "entity_bridges": 0.0},
			"sweep":      map[string]any{"last_run_at": lastRunAt, "last_pruned": 0.0, "total_pruned": 0.0},
		}
	}
	// liveMem is a semantic row last accessed at T, so R = 1 at T: top bucket.
	aOnly := map[string]any{"a": map[string]any{"episodic": 0.0, "semantic": 1.0, "procedural": 0.0}}

	t.Run("unknown namespace", func(t *testing.T) {
		t.Parallel()
		got := call(t, nil, map[string]any{"namespace": "nope"})
		if w := want(0, map[string]any{}, nil, nil); !reflect.DeepEqual(got, w) {
			t.Errorf("got %#v\nwant %#v", got, w)
		}
	})
	t.Run("blank namespace is a tool error", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		tools := NewTools(&mock.FakeEmbedder{}, nil, engram.TypeAwareDecay{}, mock.NewMemStore(), fixedClock{})
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
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "memory_stats", Arguments: map[string]any{"namespace": "  "}})
		if err != nil || !res.IsError {
			t.Fatalf("CallTool: err %v, IsError %v, want a tool error", err, res != nil && res.IsError)
		}
		var msg string
		for _, c := range res.Content {
			if tc, ok := c.(*mcpsdk.TextContent); ok {
				msg += tc.Text
			}
		}
		if !strings.Contains(msg, "1..256") {
			t.Errorf("message = %q, want one naming the bounds", msg)
		}
	})
	t.Run("seeded store", func(t *testing.T) {
		t.Parallel()
		got := call(t, nil, nil)
		if w := want(1, aOnly, map[int]float64{9: 1}, nil); !reflect.DeepEqual(got, w) {
			t.Errorf("got %#v\nwant %#v", got, w)
		}
	})
	t.Run("with sweep stats", func(t *testing.T) {
		t.Parallel()
		at := fixedClock{}.Now()
		opt := WithSweepStats(func() engram.SweepStats {
			return engram.SweepStats{LastRunAt: at, LastPruned: 2, TotalPruned: 7}
		})
		got := call(t, []Option{opt}, nil)
		w := want(1, aOnly, map[int]float64{9: 1}, at.Format(time.RFC3339Nano))
		w["sweep"].(map[string]any)["last_pruned"], w["sweep"].(map[string]any)["total_pruned"] = 2.0, 7.0
		if !reflect.DeepEqual(got, w) {
			t.Errorf("got %#v\nwant %#v", got, w)
		}
	})
}

// TestBelowRecallThresholdMatchesRecall pins the soft-threshold boundary: stats and recall
// must agree on whether R == threshold is kept and the next float below it is dropped.
func TestBelowRecallThresholdMatchesRecall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		r         float64
		wantBelow int
		wantKept  bool
	}{
		{"at threshold", defaultSoftThresh, 0, true},
		{"just below", math.Nextafter(defaultSoftThresh, 0), 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ms := mock.NewMemStore()
			putAll(t, ms, liveMem("1"))
			h := statsTools(ms, mock.FakeDecay{R: tt.r})
			out, err := h.doMemoryStats(context.Background(), MemoryStatsInput{})
			if err != nil {
				t.Fatal(err)
			}
			if got := out.Retrievability.BelowRecallThreshold; got != tt.wantBelow {
				t.Errorf("below_recall_threshold = %d, want %d", got, tt.wantBelow)
			}
			cands := []engram.RecallResult{{Memory: liveMem("1")}}
			if kept := len(h.scoreAndFilter(cands, fixedClock{}.Now(), false)) == 1; kept != tt.wantKept {
				t.Errorf("recall kept = %v, want %v", kept, tt.wantKept)
			}
		})
	}
}
