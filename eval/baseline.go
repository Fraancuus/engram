package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"

	"github.com/Fraancuus/engram"
)

// GateTolerance is the absolute precision@k drop --ci tolerates.
const GateTolerance = 0.02

// ErrUnsupportedArch is returned by WriteBaseline off amd64: Go fuses
// multiply-adds on some other architectures, so their numbers would not
// reproduce on the amd64 CI runners.
var ErrUnsupportedArch = errors.New("baseline can only be written on amd64")

// maxBaselineK mirrors the recall pipeline's cap on k.
const maxBaselineK = 100

// goarch is runtime.GOARCH, replaceable by tests.
var goarch = runtime.GOARCH

// Baseline is the committed reference the --ci gate compares against: arm
// type-aware's metrics from the first seed of a floor run.
type Baseline struct {
	K            int     `json:"k"`
	Seed         int64   `json:"seed"`
	GOARCH       string  `json:"goarch"`
	Rerank       string  `json:"rerank"`
	Params       Params  `json:"params"`
	PrecisionAtK float64 `json:"precision_at_k"`
	NDCGAtK      float64 `json:"ndcg_at_k"`
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// typeAwareFirst returns the type-aware arm's first-seed result.
func typeAwareFirst(r *Results) (ArmResult, error) {
	rs := r.Arms[engram.DecayTypeAware]
	if len(rs) == 0 {
		return ArmResult{}, fmt.Errorf("no %s arm result", engram.DecayTypeAware)
	}
	return rs[0], nil
}

// NewBaseline captures arm type-aware's first-seed precision@k and nDCG@k from
// r, rounded to 4 decimals. It errors when r has no type-aware result.
func NewBaseline(r *Results) (Baseline, error) {
	a, err := typeAwareFirst(r)
	if err != nil {
		return Baseline{}, fmt.Errorf("new baseline: %w", err)
	}
	return Baseline{
		K: r.K, Seed: a.Seed, GOARCH: goarch, Rerank: r.Rerank, Params: r.Params,
		PrecisionAtK: round4(a.Precision), NDCGAtK: round4(a.NDCG),
	}, nil
}

// LoadBaseline reads a baseline file.
func LoadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path
	if err != nil {
		return Baseline{}, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	// A zero metric or k would make the gate vacuous.
	if b.K < 1 {
		return Baseline{}, fmt.Errorf("baseline %s: k must be at least 1, got %d", path, b.K)
	}
	if b.K > maxBaselineK {
		return Baseline{}, fmt.Errorf("baseline %s: k must be at most %d, got %d", path, maxBaselineK, b.K)
	}
	if b.PrecisionAtK <= 0 {
		return Baseline{}, fmt.Errorf("baseline %s: precision_at_k must be positive, got %v", path, b.PrecisionAtK)
	}
	return b, nil
}

// CheckBaselineArch returns ErrUnsupportedArch when not running on amd64, so
// callers can refuse before spending time on an eval run.
func CheckBaselineArch() error {
	if goarch != "amd64" {
		return fmt.Errorf("baseline on %s: %w", goarch, ErrUnsupportedArch)
	}
	return nil
}

// WriteBaseline writes b to path, or returns ErrUnsupportedArch (writing
// nothing) when not running on amd64.
func WriteBaseline(path string, b Baseline) error {
	if err := CheckBaselineArch(); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// CheckGate returns nil when r's type-aware precision@k (first seed, rounded to
// 4 decimals) is no lower than b.PrecisionAtK - tol and r.Params meets the
// floor and match b (seed, params, rerank, k). It never compares arms: a null thesis result is not a failure.
func CheckGate(r *Results, b Baseline, tol float64) error {
	if err := r.Params.Validate(); err != nil {
		return fmt.Errorf("eval params: %w", err)
	}
	if len(r.Seeds) == 0 || b.Seed != r.Seeds[0] || b.Params != r.Params || b.Rerank != r.Rerank || b.K != r.K {
		return errors.New("baseline stale: seed, params (including mix), rerank or k differ; re-run --update-baseline")
	}
	a, err := typeAwareFirst(r)
	if err != nil {
		return fmt.Errorf("check gate: %w", err)
	}
	got := round4(a.Precision)
	// epsilon: a drop of exactly tol must pass despite float error.
	if got < b.PrecisionAtK-tol-1e-9 {
		return fmt.Errorf("precision@%d regressed: baseline %.4f, got %.4f, tolerance %v", r.K, b.PrecisionAtK, got, tol)
	}
	return nil
}
