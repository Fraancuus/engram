package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Fraancuus/engram"
)

// TieNDCG is the mean-NDCG gap within which the smaller store wins.
const TieNDCG = 0.005

// Recommend returns the arm with the highest mean NDCG; when the top two are
// within TieNDCG, the one with the smaller mean final store size wins.
func Recommend(r *Results) string {
	type cand struct {
		name       string
		ndcg, size float64
	}
	var cs []cand
	for _, name := range armOrder {
		if len(r.Arms[name]) == 0 {
			continue
		}
		s := r.Summary(name)
		cs = append(cs, cand{name, s.NDCG.Mean, s.FinalSize.Mean})
	}
	if len(cs) == 0 {
		return ""
	}
	best, second := -1, -1
	for i, c := range cs {
		switch {
		case best < 0 || c.ndcg > cs[best].ndcg:
			best, second = i, best
		case second < 0 || c.ndcg > cs[second].ndcg:
			second = i
		}
	}
	if second >= 0 && cs[best].ndcg-cs[second].ndcg <= TieNDCG && cs[second].size < cs[best].size {
		best = second
	}
	return cs[best].name
}

func meanStd(s Stats) string { return fmt.Sprintf("%.3f±%.3f", s.Mean, s.Std) }
func count(s Stats) string   { return fmt.Sprintf("%.0f±%.0f", s.Mean, s.Std) }

// WriteTable prints one row per arm (mean±std over seeds) followed by the
// recommendation line.
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
			s.LatencyP50.Round(time.Microsecond), s.LatencyP95.Round(time.Microsecond))
	}
	_ = tw.Flush() // a failing w fails the next write too; WriteTable has no error return
	rec := Recommend(r)
	_, _ = fmt.Fprintf(w, "recommended: %s (ENGRAM_DECAY=%s)\n", rec, rec)
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
