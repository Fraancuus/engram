package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/eval"
)

func TestMainMissingBaseline(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--ci", "-baseline", filepath.Join(t.TempDir(), "nope.json")}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "--update-baseline") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestMainCIForcesOneSeed(t *testing.T) {
	if got := seedsFor(42, 3, true); !slices.Equal(got, []int64{42}) {
		t.Fatalf("ci: %v", got)
	}
	if got := seedsFor(42, 3, false); !slices.Equal(got, []int64{42, 43, 44}) {
		t.Fatalf("plain: %v", got)
	}
	if got := seedsFor(7, 0, false); !slices.Equal(got, []int64{7}) {
		t.Fatalf("zero seeds: %v", got)
	}
}

func TestMainRerankFlagInvalid(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-rerank", "bogus"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "bogus") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestMainCIRejectsScale(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"--ci", "-scale", "2"}, {"--ci", "-days", "30"}, {"--update-baseline", "-scale", "2"}} {
		out.Reset()
		errb.Reset()
		if code := run(append(args, "-baseline", filepath.Join(t.TempDir(), "x")), &out, &errb); code != 1 || out.Len() != 0 {
			t.Fatalf("%v: code %d stdout %q stderr %q", args, code, out.String(), errb.String())
		}
	}
}

// Not parallel: it swaps the package-level checkArch.
func TestMainUpdateRefusesNonAmd64(t *testing.T) {
	old := checkArch
	checkArch = func() error { return errors.New("baseline on arm64: unsupported") }
	t.Cleanup(func() { checkArch = old })
	path := filepath.Join(t.TempDir(), "b.json")
	jsonPath := filepath.Join(t.TempDir(), "r.json")
	var out, errb bytes.Buffer
	code := run([]string{"--update-baseline", "-baseline", path, "-json", jsonPath}, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "arm64") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	for _, f := range []string{path, jsonPath} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s written", f)
		}
	}
}

func TestMainBadBaselineNoUpdateHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"--ci", "-baseline", path}, &out, &errb); code != 1 || strings.Contains(errb.String(), "--update-baseline") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestRunRejectsBadLambda(t *testing.T) {
	for _, l := range []string{"-0.1", "NaN", "Inf"} {
		var out, errb bytes.Buffer
		if code := run([]string{"-lambda", l}, &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "lambda") {
			t.Errorf("%s: code %d stdout %q stderr %q", l, code, out.String(), errb.String())
		}
	}
}

func TestRunRejectsUnknownMix(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-mix", "weekend"}, &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "weekend") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
}

func TestUpdateBaselineRefusesStress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	var out, errb bytes.Buffer
	code := run([]string{"--update-baseline", "-mix", "stress", "-baseline", path}, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "agent") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("baseline written")
	}
}

// stubRunEval swaps runEval for one that records its arguments and returns a
// small type-aware result, so no floor run starts. Not parallel-safe.
func stubRunEval(t *testing.T) (gotP *eval.Params, gotSeeds *[]int64, gotK *int, gotMode *eval.RerankMode) {
	t.Helper()
	var (
		p     eval.Params
		seeds []int64
		k     int
		mode  eval.RerankMode
	)
	old := runEval
	runEval = func(_ context.Context, ps eval.Params, ss []int64, kk int, m eval.RerankMode) (*eval.Results, error) {
		p, seeds, k, mode = ps, ss, kk, m
		return &eval.Results{
			K: kk, Rerank: m.String(), Lambda: eval.DefaultLambda, Params: ps, Seeds: ss,
			Arms: map[string][]eval.ArmResult{engram.DecayTypeAware: {{Name: engram.DecayTypeAware, Seed: ss[0], Precision: 0.5, NDCG: 0.5}}},
		}, nil
	}
	t.Cleanup(func() { runEval = old })
	return &p, &seeds, &k, &mode
}

func writeTestBaseline(t *testing.T, mix string) string {
	t.Helper()
	b := eval.Baseline{K: 3, Seed: 7, GOARCH: "amd64", Rerank: "none", Params: eval.Floor(), PrecisionAtK: 0.5, NDCGAtK: 0.5}
	b.Params.Mix = mix
	path := filepath.Join(t.TempDir(), "base.json")
	data, err := json.Marshal(b) // not WriteBaseline: it refuses non-amd64 hosts
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Not parallel: it swaps the package-level runEval.
func TestRunPlumbsFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     func(t *testing.T) []string
		wantCode int
		wantOut  []string
		wantErr  []string
		wantMix  string
		wantSeed int64                             // 0 means unchecked
		wantK    int                               // 0 means unchecked
		after    func(t *testing.T, args []string) // optional; the baseline path is the last arg
	}{
		{"mix stress", func(*testing.T) []string { return []string{"-mix", "stress"} }, 0,
			[]string{"mix: stress"}, nil, eval.MixStress, 0, 0, nil},
		{"lambda zero", func(*testing.T) []string { return []string{"-lambda", "0"} }, 0,
			[]string{"lambda: 0\n", "lambda 0\n"}, nil, eval.MixAgent, 0, 0, nil},
		{"default lambda", func(*testing.T) []string { return nil }, 0,
			[]string{"lambda: 0.1\n", "lambda 0.1\n"}, nil, eval.MixAgent, 0, 0, nil},
		{"ci adopts baseline mix", func(t *testing.T) []string {
			return []string{"--ci", "-mix", "agent", "-rerank", "cosine", "-baseline", writeTestBaseline(t, eval.MixStress)}
		}, 0, []string{"eval gate passed"}, []string{"ignoring -mix", "ignoring -rerank"}, eval.MixStress, 7, 3, nil},
		{"ci no-mix baseline is stale", func(t *testing.T) []string {
			return []string{"--ci", "-baseline", writeTestBaseline(t, "")}
		}, 1, nil, []string{"baseline stale"}, eval.MixAgent, 0, 0, nil},
		{"ci no-mix baseline ignores -mix stress", func(t *testing.T) []string {
			return []string{"--ci", "-mix", "stress", "-baseline", writeTestBaseline(t, "")}
		}, 1, nil, []string{"baseline stale", "ignoring -mix"}, eval.MixAgent, 0, 0, nil},
		{"update-baseline plumbs seed and mix", func(t *testing.T) []string {
			if eval.CheckBaselineArch() != nil {
				t.Skip("non-amd64 host")
			}
			return []string{"--update-baseline", "-seed", "43", "-baseline", filepath.Join(t.TempDir(), "b.json")}
		}, 0, nil, nil, eval.MixAgent, 0, 0, func(t *testing.T, args []string) {
			data, err := os.ReadFile(args[len(args)-1]) // #nosec G304 -- test temp path
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Seed   int64 `json:"seed"`
				Params struct {
					Mix string `json:"mix"`
				} `json:"params"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Seed != 43 || got.Params.Mix != eval.MixAgent {
				t.Errorf("baseline seed %d mix %q, want 43 agent:\n%s", got.Seed, got.Params.Mix, data)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, seeds, k, _ := stubRunEval(t)
			var out, errb bytes.Buffer
			args := tt.args(t)
			code := run(args, &out, &errb)
			if code != tt.wantCode {
				t.Fatalf("code %d, want %d; stderr %q", code, tt.wantCode, errb.String())
			}
			if p.Mix != tt.wantMix {
				t.Errorf("Params.Mix = %q, want %q", p.Mix, tt.wantMix)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("stdout lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(errb.String(), w) {
					t.Errorf("stderr lacks %q: %q", w, errb.String())
				}
			}
			if tt.after != nil {
				tt.after(t, args)
			}
			if tt.wantSeed != 0 && (!slices.Equal(*seeds, []int64{tt.wantSeed}) || *k != tt.wantK) {
				t.Errorf("seeds %v k %d, want [%d] %d", *seeds, *k, tt.wantSeed, tt.wantK)
			}
		})
	}
}
