package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"text/tabwriter"

	"github.com/Fraancuus/engram"
)

// TieNDCG is the mean-NDCG gap within which the smaller store wins.
const TieNDCG = 0.005

// Recommend returns the arm with the highest mean NDCG; every arm within
// TieNDCG of that maximum counts as tied and the smallest mean final store
// size wins among them (equal sizes go to the earlier arm in armOrder).
func Recommend(r *Results) string {
	type cand struct {
		name       string
		ndcg, size float64
	}
	var cs []cand
	top := math.Inf(-1)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		cs = append(cs, cand{name, s.NDCG.Mean, s.FinalSize.Mean})
		top = max(top, s.NDCG.Mean)
	}
	best := -1
	for i, c := range cs {
		// epsilon: a gap of exactly TieNDCG must tie despite float error.
		if top-c.ndcg <= TieNDCG+1e-9 && (best < 0 || c.size < cs[best].size) {
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
	_, _ = fmt.Fprintf(w, "rerank: %s\n", r.Rerank)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "arm\tprecision@%d\trecall@%d\tndcg@%d\tmrr\tprocedural\tsemantic\tepisodic\tbridge-link\tbridge-entity\tvia-vector\tvia-link\tvia-entity\tstore\tedges\tbridges\tp50\tp95\n", r.K, r.K, r.K)
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name,
			meanStd(s.Precision), meanStd(s.Recall), meanStd(s.NDCG), meanStd(s.MRR),
			meanStd(s.RecallByType[engram.Procedural]), meanStd(s.RecallByType[engram.Semantic]), meanStd(s.RecallByType[engram.Episodic]),
			meanStd(s.BridgeRecall["link"]), meanStd(s.BridgeRecall["entity"]),
			meanStd(s.ViaShare["vector"]), meanStd(s.ViaShare["link"]), meanStd(s.ViaShare["entity"]),
			count(s.FinalSize), count(s.FinalEdges), count(s.FinalBridges),
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

	rec := Recommend(r)
	_, _ = fmt.Fprintf(w, "\nrecommended: %s (ENGRAM_DECAY=%s)\n", rec, rec)
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
