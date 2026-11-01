package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
)

func precResults(a, b, c float64) *Results {
	arm := func(name string, p float64) []ArmResult {
		return []ArmResult{{Name: name, Seed: 42, Precision: p, NDCG: p / 2}}
	}
	return &Results{
		K: 5, Rerank: "none", Params: Floor(), Seeds: []int64{42},
		Arms: map[string][]ArmResult{
			engram.DecayNone: arm(engram.DecayNone, a), engram.DecayUniform: arm(engram.DecayUniform, b),
			engram.DecayTypeAware: arm(engram.DecayTypeAware, c),
		},
	}
}

func TestGateRegression(t *testing.T) {
	err := CheckGate(precResults(0, 0, 0.52), Baseline{PrecisionAtK: 0.55}, GateTolerance)
	if err == nil {
		t.Fatal("want regression error")
	}
	for _, s := range []string{"0.5200", "0.5500", "0.02"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q missing %q", err, s)
		}
	}
}

func TestGateWithinTolerance(t *testing.T) {
	for _, c := range []float64{0.535, 0.53, 0.60} {
		if err := CheckGate(precResults(0, 0, c), Baseline{PrecisionAtK: 0.55}, GateTolerance); err != nil {
			t.Errorf("c=%v: %v", c, err)
		}
	}
}

func TestGateNullThesisPasses(t *testing.T) {
	if err := CheckGate(precResults(0.4, 0.9, 0.54), Baseline{PrecisionAtK: 0.55}, GateTolerance); err != nil {
		t.Fatalf("B above C must not fail the gate: %v", err)
	}
}

func TestGateBelowFloor(t *testing.T) {
	r := precResults(0, 0, 0.55)
	r.Params.Memories = 10
	if err := CheckGate(r, Baseline{PrecisionAtK: 0.55}, GateTolerance); err == nil {
		t.Fatal("want below-floor error")
	}
}

func TestBaselineRound(t *testing.T) {
	r := precResults(0, 0, 0.123456)
	r.Arms[engram.DecayTypeAware][0].NDCG = 0.98765
	b := NewBaseline(r)
	if b.PrecisionAtK != 0.1235 || b.NDCGAtK != 0.9877 || b.Seed != 42 || b.K != 5 || b.Rerank != "none" {
		t.Fatalf("got %+v", b)
	}
}

func TestBaselineRoundTripAndArch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	want := NewBaseline(precResults(0, 0, 0.5))
	if goarch != "amd64" {
		t.Skip("non-amd64 host")
	}
	if err := WriteBaseline(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(path)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	raw, _ := os.ReadFile(path) // #nosec G304 -- test temp path
	for _, k := range []string{`"precision_at_k"`, `"ndcg_at_k"`, `"goarch"`, `"params"`, `"rerank"`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("missing key %s", k)
		}
	}
}

func TestBaselineRefusesNonAmd64(t *testing.T) {
	old := goarch
	goarch = "arm64"
	t.Cleanup(func() { goarch = old })
	path := filepath.Join(t.TempDir(), "b.json")
	err := WriteBaseline(path, Baseline{})
	if !errors.Is(err, ErrUnsupportedArch) {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("file written")
	}
}
