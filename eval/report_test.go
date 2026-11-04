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
	r := fakeResults(0.5, 0.6, 0.7, 100, 80, 60)
	r.Params.Mix = MixAgent
	WriteTable(&buf, r)
	out := buf.String()
	if !strings.HasPrefix(out, "rerank: none, mix: agent, lambda: 0\n") {
		t.Errorf("missing rerank line in:\n%s", out)
	}
	wantHeader := []string{"arm", "precision@5", "recall@5", "ndcg@5", "mrr", "procedural", "semantic", "episodic",
		"bridge-link", "bridge-entity", "via-vector", "via-link", "via-entity", "store", "score", "edges", "bridges", "p50", "p95"}
	if got, _ := lineFields(out, "arm "); !slices.Equal(got, wantHeader) {
		t.Errorf("header = %v want %v", got, wantHeader)
	}
	wantRow := []string{"none", "0.111±0.000", "0.222±0.000", "0.500±0.000", "0.444±0.000",
		"0.555±0.000", "0.666±0.000", "0.777±0.000", "0.888±0.000", "0.999±0.000",
		"0.121±0.000", "0.131±0.000", "0.141±0.000", "100±0", "0.500", "200±0", "10±0",
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

func TestAccessHitSummaryAndTable(t *testing.T) {
	t.Parallel()
	a1, a2 := fakeArm(engram.DecayNone, 0.5, 10), fakeArm(engram.DecayNone, 0.5, 10)
	a1.AccessHit = map[Kind]float64{KindStandard: 1, KindFact: 0.5, KindEpisode: 0.25}
	a2.AccessHit = map[Kind]float64{KindStandard: 0.5, KindFact: 0.5} // no episode accesses this seed
	r := &Results{K: 5, Rerank: "none", Seeds: []int64{1, 2}, Arms: map[string][]ArmResult{engram.DecayNone: {a1, a2}}}
	s := r.Summary(engram.DecayNone)
	if got := s.AccessHit[KindStandard].Mean; got != 0.75 {
		t.Errorf("standard mean = %v, want 0.75", got)
	}
	if got := s.AccessHit[KindEpisode]; got.Mean != 0.25 || got.Std != 0 {
		t.Errorf("episode with one seed missing = %+v, want mean 0.25 over the one seed", got)
	}
	var buf bytes.Buffer
	WriteTable(&buf, r)
	lines := strings.Split(buf.String(), "\n")
	_, title := lineFields(buf.String(), "access hit rate (target ranked first)")
	if title < 0 {
		t.Fatalf("missing access table in:\n%s", buf.String())
	}
	if got, want := strings.Fields(lines[title+1]), []string{"arm", "standard", "fact", "episode"}; !slices.Equal(got, want) {
		t.Errorf("access header = %v want %v", got, want)
	}
	if got := strings.Fields(lines[title+2]); len(got) != 4 || got[0] != "none" || got[1] != "0.750±0.354" || got[3] != "0.250±0.000" {
		t.Errorf("access row = %v", got)
	}
	// An access that returned nothing is a recorded miss (key present, rate 0): it
	// pulls the mean down, unlike a seed without any access of the kind.
	a3 := fakeArm(engram.DecayNone, 0.5, 10)
	a3.AccessHit = map[Kind]float64{KindEpisode: 0}
	r3 := &Results{K: 5, Rerank: "none", Seeds: []int64{1, 3}, Arms: map[string][]ArmResult{engram.DecayNone: {a1, a3}}}
	if got := r3.Summary(engram.DecayNone).AccessHit[KindEpisode].Mean; got != 0.125 {
		t.Errorf("episode mean with a miss seed = %v, want 0.125", got)
	}
}

func TestDefaultLambda(t *testing.T) {
	t.Parallel()
	if DefaultLambda != 0.10 {
		t.Fatalf("DefaultLambda = %v, want 0.10", DefaultLambda)
	}
}

func TestRecommendStoreCostFlips(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.98, 0.1, 0.90, 5000, 5000, 1500)
	r.Lambda = 0.20
	if got := Recommend(r); got != engram.DecayTypeAware {
		t.Fatalf("got %s want type-aware", got)
	}
	// The scores behind the flip: none 0.98 - 0.20*1 = 0.78, type-aware
	// 0.90 - 0.20*0.3 = 0.84.
	base := storeBaseline(r)
	for name, want := range map[string]float64{engram.DecayNone: 0.78, engram.DecayTypeAware: 0.84} {
		if got := armScore(r.Summary(name), base, r.Lambda); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s score = %v, want %v", name, got, want)
		}
	}
	var buf bytes.Buffer
	WriteTable(&buf, r)
	header, hl := lineFields(buf.String(), "arm")
	if hl < 0 {
		t.Fatalf("no header row in:\n%s", buf.String())
	}
	scoreCol := slices.Index(header, "score")
	if scoreCol < 0 {
		t.Fatalf("no score column in header %v", header)
	}
	for name, want := range map[string]string{engram.DecayNone: "0.780", engram.DecayTypeAware: "0.840"} {
		fields, line := lineFields(buf.String(), name)
		if line < 0 {
			t.Fatalf("no %s row in:\n%s", name, buf.String())
		}
		if got := fields[scoreCol]; got != want {
			t.Errorf("%s row score = %s, want %s (row %v)", name, got, want, fields)
		}
	}
}

func TestRecommendHugeLambda(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.98, 0.90, 0.90, 5000, 5000, 1500)
	r.Lambda = 1e308
	if got := Recommend(r); got != engram.DecayTypeAware {
		t.Fatalf("Recommend = %q for a huge lambda, want %s", got, engram.DecayTypeAware)
	}
}

func TestRecommendLambdaZero(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.98, 0.1, 0.90, 5000, 5000, 1500)
	if got := Recommend(r); got != engram.DecayNone {
		t.Fatalf("got %s want none", got)
	}
}

func TestRecommendNoNoneArm(t *testing.T) {
	t.Parallel()
	r := fakeResults(0, 0.90, 0.88, 0, 4000, 1000)
	delete(r.Arms, engram.DecayNone)
	if got := storeBaseline(r); got != 4000 {
		t.Fatalf("baseline = %v want 4000 (largest arm)", got)
	}
	r.Lambda = 0.20 // costs 0.20 vs 0.05: type-aware 0.83 beats uniform 0.70
	if got := Recommend(r); got != engram.DecayTypeAware {
		t.Fatalf("got %s want type-aware", got)
	}
}

func TestRecommendZeroStoreBaseline(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.5, 0.6, 0.7, 0, 0, 0)
	r.Lambda = 0.5
	s := r.Summary(engram.DecayTypeAware)
	got := armScore(s, storeBaseline(r), r.Lambda)
	if math.IsNaN(got) || got != 0.7 {
		t.Fatalf("score = %v want 0.7", got)
	}
	if got := Recommend(r); got != engram.DecayTypeAware {
		t.Fatalf("got %s want type-aware", got)
	}
}

func TestBreakEven(t *testing.T) {
	t.Parallel()
	sum := func(ndcg, size float64) Summary {
		return Summary{NDCG: Stats{Mean: ndcg}, FinalSize: Stats{Mean: size}}
	}
	if got, ok := breakEven(sum(0.98, 5000), sum(0.90, 1500)); !ok || math.Abs(got-0.114) > 5e-4 {
		t.Errorf("got %.3f, %v want 0.114, true", got, ok)
	}
	if _, ok := breakEven(sum(0.98, 5000), sum(0.90, 5000)); ok {
		t.Error("equal store must be not applicable")
	}
	if _, ok := breakEven(sum(0.98, 0), sum(0.90, 0)); ok {
		t.Error("empty none store must be not applicable")
	}
	if got, ok := breakEven(sum(0.90, 5000), sum(0.98, 1500)); !ok || got != 0 {
		t.Errorf("higher nDCG arm: got %v, %v want 0, true", got, ok)
	}
}

func TestReportNamesMix(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.5, 0.6, 0.7, 100, 80, 60)
	r.Params.Mix = MixStress
	r.Lambda = 0.1
	var buf bytes.Buffer
	WriteTable(&buf, r)
	out := buf.String()
	for _, want := range []string{"mix: stress", "recommended: type-aware (ENGRAM_DECAY=type-aware) on mix stress, lambda 0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestReportBreakEvenLine(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.98, 0.90, 0.90, 5000, 5000, 1500)
	var buf bytes.Buffer
	WriteTable(&buf, r)
	// The two summary lines sit together, one blank line after the last table.
	want := "\n\nbreak-even lambda vs none: uniform n/a, type-aware 0.114\nrecommended: "
	if !strings.Contains(buf.String(), want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
	delete(r.Arms, engram.DecayNone)
	buf.Reset()
	WriteTable(&buf, r)
	if strings.Contains(buf.String(), "break-even") {
		t.Errorf("break-even line without arm none in:\n%s", buf.String())
	}
}

func TestReportNoBreakEvenWithOnlyNone(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.98, 0.90, 0.90, 5000, 5000, 1500)
	delete(r.Arms, engram.DecayUniform)
	delete(r.Arms, engram.DecayTypeAware)
	var buf bytes.Buffer
	WriteTable(&buf, r)
	out := buf.String()
	if strings.Contains(out, "break-even") {
		t.Errorf("break-even line with only arm none in:\n%s", out)
	}
	if !strings.Contains(out, "\n\nrecommended: none ") {
		t.Errorf("recommended line not set off by one blank line in:\n%s", out)
	}
}

func TestResultsLambdaInJSON(t *testing.T) {
	t.Parallel()
	r := fakeResults(0.5, 0.6, 0.7, 100, 80, 60)
	r.Lambda = 0.1
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"Lambda": 0.1`) {
		t.Errorf("Lambda missing from JSON:\n%s", buf.String())
	}
}
