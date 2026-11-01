package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

func fakeArm(name string, ndcg float64, size int) ArmResult {
	return ArmResult{
		Name: name, Seed: 1, Precision: 0.111, Recall: 0.222, NDCG: ndcg, MRR: 0.444,
		RecallByType: map[engram.MemoryType]float64{engram.Procedural: 0.555, engram.Semantic: 0.666, engram.Episodic: 0.777},
		BridgeRecall: map[string]float64{"link": 0.888, "entity": 0.999},
		ViaShare:     map[string]float64{"vector": 0.121, "link": 0.131, "entity": 0.141},
		LatencyP50:   2 * time.Millisecond, LatencyP95: 7 * time.Millisecond,
		FinalSize: size, FinalEdges: size * 2, FinalBridges: size / 10,
		ByClass: map[QueryClass]ClassStats{
			QTopicCurrent: {NDCG: 0.101, MRR: 0.201, N: 10}, QFact: {NDCG: 0.102, MRR: 0.202, N: 10},
			QEpisodeRecent: {NDCG: 0.103, MRR: 0.203, N: 10}, QEpisodeOld: {NDCG: 0.104, MRR: 0.204, N: 10},
			QBridgeLink: {NDCG: 0.105, MRR: 0.205, N: 10}, QBridgeEntity: {NDCG: 0.106, MRR: 0.206, N: 10},
		},
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
		{"three-way tie, smallest of all", 0.805, 0.801, 0.803, 500, 200, 300, "uniform"},
		{"three-way tie, equal size goes to arm order", 0.805, 0.801, 0.803, 300, 300, 300, "none"},
		{"inside boundary 0.0049", 0.800, 0.8049, 0.1, 100, 200, 300, "none"},
		{"exactly at boundary 0.005", 0.800, 0.805, 0.1, 100, 200, 300, "none"},
		{"outside boundary 0.0051", 0.800, 0.8051, 0.1, 100, 200, 300, "uniform"},
		{"only the in-tie arms compete on size", 0.805, 0.1, 0.801, 500, 10, 300, "type-aware"},
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

// lineFields returns the whitespace-split fields and index of the first line
// of out that starts with prefix.
func lineFields(out, prefix string) ([]string, int) {
	for i, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.Fields(l), i
		}
	}
	return nil, -1
}

func TestReportTableColumns(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteTable(&buf, fakeResults(0.5, 0.6, 0.7, 100, 80, 60))
	out := buf.String()
	if !strings.HasPrefix(out, "rerank: none\n") {
		t.Errorf("missing rerank line in:\n%s", out)
	}
	wantHeader := []string{"arm", "precision@5", "recall@5", "ndcg@5", "mrr", "procedural", "semantic", "episodic",
		"bridge-link", "bridge-entity", "via-vector", "via-link", "via-entity", "store", "edges", "bridges", "p50", "p95"}
	if got, _ := lineFields(out, "arm "); !slices.Equal(got, wantHeader) {
		t.Errorf("header = %v want %v", got, wantHeader)
	}
	wantRow := []string{"none", "0.111±0.000", "0.222±0.000", "0.500±0.000", "0.444±0.000",
		"0.555±0.000", "0.666±0.000", "0.777±0.000", "0.888±0.000", "0.999±0.000",
		"0.121±0.000", "0.131±0.000", "0.141±0.000", "100±0", "200±0", "10±0",
		"2.0±0.0", "ms", "7.0±0.0", "ms"}
	if got, _ := lineFields(out, "none "); !slices.Equal(got, wantRow) {
		t.Errorf("none row = %v want %v", got, wantRow)
	}
	if !strings.Contains(out, "recommended: type-aware (ENGRAM_DECAY=type-aware)") {
		t.Errorf("missing recommendation line in:\n%s", out)
	}
	i, j, k := strings.Index(out, "\nnone"), strings.Index(out, "\nuniform"), strings.Index(out, "\ntype-aware")
	if i < 0 || i >= j || j >= k {
		t.Errorf("arm order wrong in:\n%s", out)
	}

	_, title := lineFields(out, "by query class (ndcg / mrr)")
	if title < 0 {
		t.Fatalf("missing per-class title in:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	wantClassHeader := []string{"arm", "topic-current", "fact", "episode-recent", "episode-old", "bridge-link", "bridge-entity"}
	if got := strings.Fields(lines[title+1]); !slices.Equal(got, wantClassHeader) {
		t.Errorf("class header = %v want %v", got, wantClassHeader)
	}
	wantClassRow := []string{"none", "0.101/0.201", "0.102/0.202", "0.103/0.203", "0.104/0.204", "0.105/0.205", "0.106/0.206"}
	if got := strings.Fields(lines[title+2]); !slices.Equal(got, wantClassRow) {
		t.Errorf("class none row = %v want %v", got, wantClassRow)
	}
	if got := strings.Fields(lines[title+3])[0]; got != "uniform" {
		t.Errorf("class table second row = %q want uniform", got)
	}
	if strings.Index(out, "recommended:") < title {
		t.Errorf("recommendation must follow the per-class table in:\n%s", out)
	}
}

func TestSummaryAggregates(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.4, 0.5, 0.6, 100, 100, 100)
	second := fakeArm(engram.DecayNone, 0.6, 200)
	second.LatencyP50, second.LatencyP95 = 4*time.Millisecond, 9*time.Millisecond
	// this seed lacks some keys: RecallByType/BridgeRecall/ByClass average over
	// the seeds that have them; ViaShare is a share, so absent means 0.
	delete(second.RecallByType, engram.Episodic)
	delete(second.BridgeRecall, "entity")
	delete(second.ViaShare, "entity")
	delete(second.ByClass, QFact)
	r.Arms[engram.DecayNone] = append(r.Arms[engram.DecayNone], second)
	s := r.Summary(engram.DecayNone)
	if s.NDCG.Mean != 0.5 || s.FinalSize.Mean != 150 || s.NDCG.Std == 0 {
		t.Fatalf("bad summary: %+v", s)
	}
	if s.RecallByType[engram.Procedural].Mean != 0.555 {
		t.Fatalf("procedural = %+v", s.RecallByType[engram.Procedural])
	}
	if got := s.RecallByType[engram.Episodic]; got.Mean != 0.777 || got.Std != 0 {
		t.Fatalf("episodic with one seed missing = %+v, want mean 0.777 over the one seed", got)
	}
	if got := s.BridgeRecall["entity"]; got.Mean != 0.999 {
		t.Fatalf("bridge-entity with one seed missing = %+v, want mean 0.999", got)
	}
	if got := s.ViaShare["entity"].Mean; math.Abs(got-0.0705) > 1e-12 {
		t.Fatalf("via-entity with one seed missing = %v, want 0.0705 (absent counts as 0)", got)
	}
	if got := s.ByClass[QFact]; got.NDCG.Mean != 0.102 || got.MRR.Mean != 0.202 {
		t.Fatalf("fact class with one seed missing = %+v", got)
	}
	if got := s.LatencyP50; got.Mean != 3 || math.Abs(got.Std-math.Sqrt2) > 1e-9 {
		t.Fatalf("p50 = %+v, want mean 3ms std sqrt2", got)
	}
	if got := s.LatencyP95.Mean; got != 8 {
		t.Fatalf("p95 mean = %v, want 8", got)
	}
}

func TestByClassBreakdown(t *testing.T) {
	t.Parallel()
	// 75 fact queries at nDCG 0.5 and 125 topic-current at 1.0: the overall
	// nDCG (0.8125) is the query-weighted mean, not the mean of class means.
	arm := ArmResult{
		Name: engram.DecayNone, NDCG: 0.8125,
		ByClass: map[QueryClass]ClassStats{
			QFact:         {NDCG: 0.5, MRR: 0.25, N: 75},
			QTopicCurrent: {NDCG: 1.0, MRR: 1.0, N: 125},
		},
	}
	r := &Results{K: 5, Rerank: "none", Seeds: []int64{1}, Arms: map[string][]ArmResult{engram.DecayNone: {arm}}}
	s := r.Summary(engram.DecayNone)
	if s.ByClass[QFact].NDCG.Mean != 0.5 || s.ByClass[QTopicCurrent].NDCG.Mean != 1.0 || s.NDCG.Mean != 0.8125 {
		t.Fatalf("bad class summary: %+v", s)
	}
	var buf bytes.Buffer
	WriteTable(&buf, r)
	_, title := lineFields(buf.String(), "by query class (ndcg / mrr)")
	if title < 0 {
		t.Fatalf("missing per-class title in:\n%s", buf.String())
	}
	got := strings.Fields(strings.Split(buf.String(), "\n")[title+2])
	want := []string{"none", "1.000/1.000", "0.500/0.250", "0.000/0.000", "0.000/0.000", "0.000/0.000", "0.000/0.000"}
	if !slices.Equal(got, want) {
		t.Fatalf("class row = %v want %v", got, want)
	}
}

func TestParseRerank(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    RerankMode
		wantErr bool
	}{
		{"none", RerankNone, false},
		{"cosine", RerankCosine, false},
		{"", RerankNone, true},
		{"bge", RerankNone, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRerank(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "none") || !strings.Contains(err.Error(), "cosine") {
					t.Fatalf("error %q must list both modes", err)
				}
				return
			}
			if got != tt.want || got.String() != tt.in {
				t.Fatalf("got %v (%s) want %v", got, got, tt.want)
			}
		})
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
	for _, mode := range []RerankMode{RerankNone, RerankCosine} {
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
