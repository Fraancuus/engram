package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

func fakeArm(name string, ndcg float64, size int) ArmResult {
	return ArmResult{
		Name: name, Seed: 1, Precision: 0.5, Recall: 0.6, NDCG: ndcg, MRR: 0.7,
		RecallByType: map[engram.MemoryType]float64{engram.Procedural: 0.9, engram.Semantic: 0.5, engram.Episodic: 0.4},
		BridgeRecall: map[string]float64{"link": 0.3, "entity": 0.2},
		ViaShare:     map[string]float64{"vector": 0.6, "link": 0.3, "entity": 0.1},
		LatencyP50:   time.Millisecond, LatencyP95: 3 * time.Millisecond,
		FinalSize: size, FinalEdges: size * 2, FinalBridges: size / 10,
	}
}

func fakeResults(a, b, c float64, sa, sb, sc int) *Results {
	return &Results{
		K: 5, Rerank: "none", Seeds: []int64{1},
		Arms: map[string][]ArmResult{
			engram.DecayNone:      {fakeArm(engram.DecayNone, a, sa)},
			engram.DecayUniform:   {fakeArm(engram.DecayUniform, b, sb)},
			engram.DecayTypeAware: {fakeArm(engram.DecayTypeAware, c, sc)},
		},
	}
}

func TestRecommendMaxNDCG(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		a, b, c float64
		want    string
	}{
		{"none wins", 0.9, 0.5, 0.6, "none"},
		{"uniform wins", 0.5, 0.9, 0.6, "uniform"},
		{"type-aware wins", 0.5, 0.6, 0.9, "type-aware"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Recommend(fakeResults(tt.a, tt.b, tt.c, 100, 100, 100)); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestRecommendTieBreak(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		a, b, c    float64
		sa, sb, sc int
		want       string
	}{
		{"tie, type-aware smaller", 0.1, 0.800, 0.803, 500, 300, 200, "type-aware"},
		{"tie, uniform smaller", 0.1, 0.800, 0.803, 500, 200, 300, "uniform"},
		{"outside tie, higher ndcg wins", 0.1, 0.800, 0.820, 500, 200, 300, "type-aware"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Recommend(fakeResults(tt.a, tt.b, tt.c, tt.sa, tt.sb, tt.sc)); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestReportTableColumns(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteTable(&buf, fakeResults(0.5, 0.6, 0.7, 100, 80, 60))
	out := buf.String()
	for _, h := range []string{"arm", "precision@5", "recall@5", "ndcg@5", "mrr", "procedural", "semantic", "episodic",
		"bridge-link", "bridge-entity", "via-vector", "via-link", "via-entity", "store", "edges", "bridges", "p50", "p95"} {
		if !strings.Contains(out, h) {
			t.Errorf("missing header %q in:\n%s", h, out)
		}
	}
	if !strings.HasPrefix(out, "rerank: none\n") {
		t.Errorf("missing rerank line in:\n%s", out)
	}
	if !strings.Contains(out, "recommended: type-aware (ENGRAM_DECAY=type-aware)") {
		t.Errorf("missing recommendation line in:\n%s", out)
	}
	i, j, k := strings.Index(out, "\nnone"), strings.Index(out, "\nuniform"), strings.Index(out, "\ntype-aware")
	if i < 0 || i >= j || j >= k {
		t.Errorf("arm order wrong in:\n%s", out)
	}
}

func TestSummaryAggregates(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.4, 0.5, 0.6, 100, 100, 100)
	r.Arms[engram.DecayNone] = append(r.Arms[engram.DecayNone], fakeArm(engram.DecayNone, 0.6, 200))
	s := r.Summary(engram.DecayNone)
	if s.NDCG.Mean != 0.5 || s.FinalSize.Mean != 150 || s.NDCG.Std == 0 {
		t.Fatalf("bad summary: %+v", s)
	}
	if s.RecallByType[engram.Procedural].Mean != 0.9 || s.LatencyP95 != 3*time.Millisecond {
		t.Fatalf("bad per-key summary: %+v", s)
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.5, 0.6, 0.7, 100, 80, 60)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var got Results
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, r) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", got, *r)
	}
}

func TestRunFloorSmoke(t *testing.T) {
	if os.Getenv("ENGRAM_EVAL_SMOKE") != "1" {
		t.Skip("set ENGRAM_EVAL_SMOKE=1 to run")
	}
	for _, mode := range []rerankMode{rerankNone, rerankCosine} {
		start := time.Now() // wall timing of the test only, not eval logic
		r, err := Run(context.Background(), Floor(), []int64{42}, 5, mode)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		WriteTable(&buf, r)
		t.Logf("wall %s\n%s", time.Since(start), buf.String())
	}
}
