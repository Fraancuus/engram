// Command eval runs the Engram retrieval eval: arms none (no decay), uniform
// and type-aware over a generated, labelled corpus with virtualized time, and
// reports precision@k, recall@k, nDCG and per-class breakdowns.
//
// With --ci it runs the floor corpus with the committed baseline's seed, k and
// rerank mode and exits 1 if type-aware precision@k regressed beyond tolerance.
// With --update-baseline it rewrites that baseline.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Fraancuus/engram/eval"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// seedsFor returns flagSeeds consecutive seeds from flagSeed; the gate and the
// baseline use exactly one.
func seedsFor(flagSeed int64, flagSeeds int, one bool) []int64 {
	if one || flagSeeds < 1 {
		flagSeeds = 1
	}
	seeds := make([]int64, flagSeeds)
	for i := range seeds {
		seeds[i] = flagSeed + int64(i)
	}
	return seeds
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ci := fs.Bool("ci", false, "run the floor corpus and fail if type-aware precision@k regresses vs the baseline")
	update := fs.Bool("update-baseline", false, "run the floor corpus and rewrite the baseline file (amd64 only)")
	seed := fs.Int64("seed", 42, "first seed")
	nSeeds := fs.Int("seeds", 3, "number of consecutive seeds (1 under --ci/--update-baseline)")
	scale := fs.Int("scale", 1, "multiply corpus size (must be 1 under --ci/--update-baseline)")
	k := fs.Int("k", 5, "results per query")
	days := fs.Int("days", 180, "simulated days (must be the floor under --ci/--update-baseline)")
	rerank := fs.String("rerank", "none", "rerank mode: none or cosine")
	jsonPath := fs.String("json", "", "also write full results as JSON to this path")
	basePath := fs.String("baseline", "eval/baseline.json", "baseline file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mode, err := eval.ParseRerank(*rerank)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	p := eval.Floor().Scaled(*scale)
	p.Days = *days
	gated := *ci || *update
	if gated && (*scale != 1 || *days != eval.Floor().Days) {
		_, _ = fmt.Fprintln(stderr, "--ci and --update-baseline run the floor corpus: omit -scale and -days")
		return 2
	}
	if *ci && *update {
		_, _ = fmt.Fprintln(stderr, "--ci and --update-baseline are mutually exclusive")
		return 2
	}

	var base eval.Baseline
	if *ci {
		if base, err = eval.LoadBaseline(*basePath); err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\nrun `go run ./cmd/eval --update-baseline` to create it\n", err)
			return 1
		}
		if mode, err = eval.ParseRerank(base.Rerank); err != nil {
			_, _ = fmt.Fprintf(stderr, "baseline rerank: %v\n", err)
			return 1
		}
		*seed, *k = base.Seed, base.K
	}

	res, err := eval.Run(context.Background(), p, seedsFor(*seed, *nSeeds, gated), *k, mode)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	eval.WriteTable(stdout, res)

	if *jsonPath != "" {
		if err := writeJSON(*jsonPath, res); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	switch {
	case *update:
		if err := eval.WriteBaseline(*basePath, eval.NewBaseline(res)); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "baseline written to %s\n", *basePath)
	case *ci:
		if err := eval.CheckGate(res, base, eval.GateTolerance); err != nil {
			_, _ = fmt.Fprintf(stderr, "eval gate FAILED: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "eval gate passed")
	}
	return 0
}

func writeJSON(path string, res *eval.Results) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- operator-supplied path
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if err := eval.WriteJSON(f, res); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
