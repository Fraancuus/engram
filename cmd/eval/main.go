// Command eval runs the Engram retrieval eval: conditions A (baseline, no decay),
// B (uniform decay), and C (type-aware decay), over a labelled dataset with
// virtualized time, and reports precision@k / recall@k / nDCG plus type-stratified
// recall. With -ci it fails the build when precision@k regresses beyond tolerance.
package main

import (
	"flag"
	"log"
)

func main() {
	ci := flag.Bool("ci", false, "fail the build if precision@k regresses beyond tolerance")
	flag.Parse()

	// TODO(M5): load the dataset, run the A/B/C conditions against the domain over
	// the mock ports with a virtual clock, compute the metrics, and (when ci) exit
	// non-zero on regression. Scaffold stub exits 0 so the CI gate is wired now.
	log.Printf("eval: scaffold stub (ci=%v); no conditions implemented yet", *ci)
}
