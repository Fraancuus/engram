package eval

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

// Schedule windows (design D6). Days are inclusive.
var (
	standardWriteDays     = [2]int{0, 10}
	laterVersionDays      = [2]int{30, 90}
	factWriteDays         = [2]int{0, 120}
	topicCurrentQueryDays = [2]int{150, 179}
	recentGapDays         = [2]int{1, 3}
	oldGapDays            = [2]int{60, 120}
)

const (
	standardAccessEvery = 7
	standardAccessUntil = 60
	factAccessWindow    = 30
	episodeAccessWindow = 3
	// queryNoise is the sigma of the noise mixed into a query's target vector.
	queryNoise = 0.15
	// bridgeQueryMaxCos is the cosine ceiling between a bridge query and Y.
	bridgeQueryMaxCos = 0.25
)

// dayIn returns a uniform day in [lo, hi].
func dayIn(r *rand.Rand, lo, hi int) int { return lo + r.IntN(hi-lo+1) }

// assignWriteDays sets WriteDay on every memory per D6.
func (d *Dataset) assignWriteDays(r *rand.Rand) {
	last := d.Params.Days - 1
	for _, ts := range d.topics {
		v1 := min(dayIn(r, standardWriteDays[0], standardWriteDays[1]), last)
		d.Memories[ts.std[0]].WriteDay = v1
		prev := laterVersionDays[0] - 1
		for i := 1; i < len(ts.std); i++ {
			hi := laterVersionDays[1] - (len(ts.std) - 1 - i) // leave room for later versions
			prev = dayIn(r, prev+1, hi)
			d.Memories[ts.std[i]].WriteDay = min(prev, last)
		}
		for _, chain := range ts.chains {
			// Versions get distinct sorted days from [v1, 120]: a fact links to the
			// standard current at its write time, so it cannot precede v1. Only a
			// run too short to hold the chain loses strictness (days clamp).
			lo := max(factWriteDays[0], v1)
			days := r.Perm(max(min(factWriteDays[1], last)-lo+1, len(chain)))[:len(chain)]
			sort.Ints(days)
			for k, i := range chain {
				d.Memories[i].WriteDay = min(lo+days[k], last)
			}
		}
		for _, i := range ts.eps {
			d.Memories[i].WriteDay = r.IntN(d.Params.Days)
		}
	}
}

// buildAccesses emits follow-up accesses, ordered by day.
func (d *Dataset) buildAccesses(r *rand.Rand) {
	last := d.Params.Days - 1
	for _, m := range d.Memories {
		switch m.Kind {
		case KindStandard:
			for day := m.WriteDay + standardAccessEvery; day <= standardAccessUntil && day <= last; day += standardAccessEvery {
				d.Accesses = append(d.Accesses, Access{GID: m.GID, Day: day})
			}
		case KindFact: // every version is accessed once written; a superseded one is demoted, not exempt
			for n := 1 + r.IntN(3); n > 0; n-- {
				d.Accesses = append(d.Accesses, Access{GID: m.GID, Day: min(m.WriteDay+dayIn(r, 1, factAccessWindow), last)})
			}
		case KindEpisode:
			for n := r.IntN(3); n > 0; n-- {
				d.Accesses = append(d.Accesses, Access{GID: m.GID, Day: min(m.WriteDay+dayIn(r, 1, episodeAccessWindow), last)})
			}
		}
	}
	sort.SliceStable(d.Accesses, func(a, b int) bool { return d.Accesses[a].Day < d.Accesses[b].Day })
}

// latestAt returns the position in chain of the latest version written on or
// before day, or 0 when none was.
func (d *Dataset) latestAt(chain []int, day int) int {
	latest := 0
	for k, i := range chain {
		if d.Memories[i].WriteDay <= day {
			latest = k
		}
	}
	return latest
}

// buildQueries emits the query mix with generator-owned relevant sets.
func (d *Dataset) buildQueries(r *rand.Rand, g *graphInfo) error {
	p := d.Params
	last := p.Days - 1
	pct, ok := mixPcts[p.Mix]
	if !ok {
		return fmt.Errorf("build queries: unknown mix %q", p.Mix)
	}
	nTopic, nFact, nRecent, nOld := p.Queries*pct[0]/100, p.Queries*pct[1]/100, p.Queries*pct[2]/100, p.Queries*pct[3]/100
	nBridge := p.Queries - nTopic - nFact - nRecent - nOld
	nLink := nBridge / 2

	var eps []int
	for _, ts := range d.topics {
		eps = append(eps, ts.eps...)
	}
	eligible := func(maxWrite int) []int {
		var out []int
		for _, i := range eps {
			if d.Memories[i].WriteDay <= maxWrite {
				out = append(out, i)
			}
		}
		if len(out) == 0 {
			return eps
		}
		return out
	}
	recent, old := eligible(last-recentGapDays[1]), eligible(last-oldGapDays[0])

	qs := make([]Query, 0, p.Queries)
	add := func(class QueryClass, day int, target engram.Vector, ns []engram.Namespace, rel ...string) {
		qs = append(qs, Query{
			Class: class, Day: day, Vec: mix(target, nil, 0, unit(r, p.Dim), queryNoise),
			Namespaces: ns, Relevant: rel,
		})
	}
	for ; nTopic > 0; nTopic-- {
		ts := d.topics[r.IntN(len(d.topics))]
		day := min(dayIn(r, topicCurrentQueryDays[0], topicCurrentQueryDays[1]), last)
		cur := ts.std[0]
		for _, i := range ts.std {
			if d.Memories[i].WriteDay <= day {
				cur = i
			}
		}
		add(QTopicCurrent, day, d.Memories[cur].Vec, []engram.Namespace{ts.ns}, d.Memories[cur].GID)
	}
	for ; nFact > 0; nFact-- {
		ts := d.topics[r.IntN(len(d.topics))]
		c := r.IntN(len(ts.chains))
		chain := ts.chains[c]
		day := min(d.Memories[chain[len(chain)-1]].WriteDay+dayIn(r, oldGapDays[0], oldGapDays[1]), last)
		add(QFact, day, ts.chainCents[c], []engram.Namespace{ts.ns}, d.Memories[chain[d.latestAt(chain, day)]].GID)
	}
	for ; nRecent > 0; nRecent-- {
		m := d.Memories[recent[r.IntN(len(recent))]]
		add(QEpisodeRecent, min(m.WriteDay+dayIn(r, recentGapDays[0], recentGapDays[1]), last), m.Vec, []engram.Namespace{m.Namespace}, m.GID)
	}
	for ; nOld > 0; nOld-- {
		m := d.Memories[old[r.IntN(len(old))]]
		add(QEpisodeOld, min(m.WriteDay+dayIn(r, oldGapDays[0], oldGapDays[1]), last), m.Vec, []engram.Namespace{m.Namespace}, m.GID)
	}
	for i := 0; i < nBridge; i++ {
		class := QBridgeEntity
		var x, y int
		var ok bool
		if i < nLink {
			class = QBridgeLink
			if len(g.crossLinks) > 0 {
				pair := g.crossLinks[r.IntN(len(g.crossLinks))]
				x, y, ok = pair[0], pair[1], true
			}
		} else {
			x, y, ok = d.entityPair(r, g)
		}
		if !ok {
			return errors.New("no bridge candidates: dataset too small")
		}
		X, Y := d.Memories[x], d.Memories[y]
		ns := []engram.Namespace{X.Namespace}
		if class == QBridgeEntity && Y.Namespace != X.Namespace {
			ns = append(ns, Y.Namespace)
		}
		lo := min(max(X.WriteDay, Y.WriteDay)+1, last)
		add(class, dayIn(r, lo, last), X.Vec, ns, X.GID, Y.GID)
		// Resample the noise until the query cannot reach Y by vector alone.
		for try := 0; mock.Cosine(qs[len(qs)-1].Vec, Y.Vec) >= bridgeQueryMaxCos; try++ {
			if try > 100 {
				return fmt.Errorf("bridge query for %s cannot stay below cos %v of %s", X.GID, bridgeQueryMaxCos, Y.GID)
			}
			qs[len(qs)-1].Vec = mix(X.Vec, nil, 0, unit(r, p.Dim), queryNoise)
		}
	}

	r.Shuffle(len(qs), func(a, b int) { qs[a], qs[b] = qs[b], qs[a] })
	sort.SliceStable(qs, func(a, b int) bool { return qs[a].Day < qs[b].Day })
	for i := range qs {
		qs[i].ID = fmt.Sprintf("q%04d", i)
		qs[i].Content = qs[i].ID
	}
	d.Queries = qs
	return nil
}
