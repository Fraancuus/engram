package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"

	"github.com/Fraancuus/engram"
)

// DefaultLambda is the store-cost weight Run assigns to Results.Lambda.
const DefaultLambda = 0.10

// TieScore is the score gap within which the smaller store wins.
const TieScore = 0.005

// storeBaseline is the mean final store size the cost term is relative to:
// arm none's, or the largest arm's when none is absent.
func storeBaseline(r *Results) float64 {
	if len(r.Arms[engram.DecayNone]) > 0 {
		return r.Summary(engram.DecayNone).FinalSize.Mean
	}
	base := 0.0
	for _, name := range armOrder {
		if len(r.Arms[name]) > 0 {
			base = max(base, r.Summary(name).FinalSize.Mean)
		}
	}
	return base
}

// armScore is mean nDCG minus lambda times the arm's store size relative to
// base; the cost term is 0 when base is not positive.
func armScore(s Summary, base, lambda float64) float64 {
	if base <= 0 {
		return s.NDCG.Mean
	}
	return s.NDCG.Mean - lambda*s.FinalSize.Mean/base
}

// breakEven returns the lambda at which arm's score equals none's, or false
// when arm saves no store (or none is empty). An arm with higher nDCG gives 0.
func breakEven(none, arm Summary) (float64, bool) {
	if none.FinalSize.Mean <= 0 || arm.FinalSize.Mean >= none.FinalSize.Mean {
		return 0, false
	}
	return max(0, (none.NDCG.Mean-arm.NDCG.Mean)/(1-arm.FinalSize.Mean/none.FinalSize.Mean)), true
}

// Recommend returns the arm with the highest score (see armScore); every arm
// within TieScore of that maximum counts as tied and the smallest mean final
// store size wins among them (equal sizes go to the earlier arm in armOrder).
func Recommend(r *Results) string {
	type cand struct {
		name        string
		score, size float64
	}
	base := storeBaseline(r)
	var cs []cand
	top := math.Inf(-1)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		c := cand{name, armScore(s, base, r.Lambda), s.FinalSize.Mean}
		cs = append(cs, c)
		top = max(top, c.score)
	}
	best := -1
	for i, c := range cs {
		// epsilon: a gap of exactly TieScore must tie despite float error.
		if top-c.score <= TieScore+1e-9 && (best < 0 || c.size < cs[best].size) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return cs[best].name
}

func meanStd(s Stats) string { return fmt.Sprintf("%.3f±%.3f", s.Mean, s.Std) }
func count(s Stats) string   { return fmt.Sprintf("%.0f±%.0f", s.Mean, s.Std) }

func ms(s Stats) string { return fmt.Sprintf("%.1f±%.1f ms", s.Mean, s.Std) }

// WriteTable prints one row per arm (mean±std over seeds), the per-class
// nDCG/MRR table, and the recommendation line.
func WriteTable(w io.Writer, r *Results) {
	_, _ = fmt.Fprintf(w, "rerank: %s  mix: %s  lambda: %g\n", r.Rerank, r.Params.Mix, r.Lambda)
	base := storeBaseline(r)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "arm\tprecision@%d\trecall@%d\tndcg@%d\tmrr\tprocedural\tsemantic\tepisodic\tbridge-link\tbridge-entity\tvia-vector\tvia-link\tvia-entity\tstore\tscore\tedges\tbridges\tp50\tp95\n", r.K, r.K, r.K)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.3f\t%s\t%s\t%s\t%s\n", name,
			meanStd(s.Precision), meanStd(s.Recall), meanStd(s.NDCG), meanStd(s.MRR),
			meanStd(s.RecallByType[engram.Procedural]), meanStd(s.RecallByType[engram.Semantic]), meanStd(s.RecallByType[engram.Episodic]),
			meanStd(s.BridgeRecall["link"]), meanStd(s.BridgeRecall["entity"]),
			meanStd(s.ViaShare["vector"]), meanStd(s.ViaShare["link"]), meanStd(s.ViaShare["entity"]),
			count(s.FinalSize), armScore(s, base, r.Lambda), count(s.FinalEdges), count(s.FinalBridges),
			ms(s.LatencyP50), ms(s.LatencyP95))
	}
	_ = tw.Flush() // a failing w fails the next write too; WriteTable has no error return

	_, _ = fmt.Fprint(w, "\nby query class (ndcg / mrr)\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprint(tw, "arm")
	for _, c := range queryClasses {
		_, _ = fmt.Fprintf(tw, "\t%s", c)
	}
	_, _ = fmt.Fprintln(tw)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		_, _ = fmt.Fprint(tw, name)
		for _, c := range queryClasses {
			_, _ = fmt.Fprintf(tw, "\t%.3f/%.3f", s.ByClass[c].NDCG.Mean, s.ByClass[c].MRR.Mean)
		}
		_, _ = fmt.Fprintln(tw)
	}
	_ = tw.Flush()

	_, _ = fmt.Fprint(w, "\naccess hit rate (target ranked first)\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprint(tw, "arm")
	for _, kd := range kinds {
		_, _ = fmt.Fprintf(tw, "\t%s", kd)
	}
	_, _ = fmt.Fprintln(tw)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		_, _ = fmt.Fprint(tw, name)
		for _, kd := range kinds {
			_, _ = fmt.Fprintf(tw, "\t%s", meanStd(s.AccessHit[kd]))
		}
		_, _ = fmt.Fprintln(tw)
	}
	_ = tw.Flush()

	if len(r.Arms[engram.DecayNone]) > 0 {
		none := r.Summary(engram.DecayNone)
		var parts []string
		for _, name := range armOrder[1:] {
			if len(r.Arms[name]) == 0 {
				continue
			}
			if l, ok := breakEven(none, r.Summary(name)); ok {
				parts = append(parts, fmt.Sprintf("%s %.3f", name, l))
			} else {
				parts = append(parts, name+" n/a")
			}
		}
		_, _ = fmt.Fprintf(w, "\nbreak-even lambda vs none: %s\n", strings.Join(parts, ", "))
	}

	rec := Recommend(r)
	_, _ = fmt.Fprintf(w, "\nrecommended: %s (ENGRAM_DECAY=%s) on mix %s, lambda %g\n", rec, rec, r.Params.Mix, r.Lambda)
}

// WriteJSON writes r as indented JSON. Durations marshal as nanoseconds.
func WriteJSON(w io.Writer, r *Results) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode results: %w", err)
	}
	return nil
}
