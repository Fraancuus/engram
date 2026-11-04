package eval

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/Fraancuus/engram/mock"
)

const (
	// entityFanoutCap keeps every entity's mention count well under the
	// pipeline's neighbor cap of 20 per seed.
	entityFanoutCap = 15
	// crossLinkFraction is the share of episodes that link into another topic.
	crossLinkFraction = 0.02
	// hubTopicCount and hubMentionsPerTopic size a hub: 4 topics x 2 mentions.
	hubTopicCount       = 4
	hubMentionsPerTopic = 2
	// bridgeMaxCos is the |cosine| ceiling between a bridge pair's X and Y.
	bridgeMaxCos = 0.20
)

// graphInfo carries what query building needs from the graph.
type graphInfo struct {
	crossLinks [][2]int         // (episode X, linked memory Y) in other topics
	hubs       []string         // hub entity names, sorted
	members    map[string][]int // hub name -> memories mentioning it
	superseded map[string]bool  // GIDs some memory supersedes: never a bridge label
}

// buildGraph adds links and entity mentions.
func (d *Dataset) buildGraph(r *rand.Rand) *graphInfo {
	g := &graphInfo{members: map[string][]int{}, superseded: map[string]bool{}}
	for _, m := range d.Memories {
		if m.Supersedes != "" {
			g.superseded[m.Supersedes] = true
		}
	}
	d.linkChains()
	d.linkCross(r, g)
	d.buildEntities(r, g)
	return g
}

// linkChains links each standard version to its predecessor, each fact version
// to its chain predecessor (which it also supersedes, see Memory.Supersedes),
// and each fact to the standard current at its write time. A link is stored on
// the later-written end, since a remember can only reference what already exists.
func (d *Dataset) linkChains() {
	for _, ts := range d.topics {
		for i := 1; i < len(ts.std); i++ {
			m := &d.Memories[ts.std[i]]
			m.Links = append(m.Links, d.Memories[ts.std[i-1]].GID)
		}
		for _, chain := range ts.chains {
			for i := 1; i < len(chain); i++ {
				m := &d.Memories[chain[i]]
				m.Links = append(m.Links, d.Memories[chain[i-1]].GID)
			}
		}
		for _, f := range ts.facts {
			cur := ts.std[0]
			for _, s := range ts.std {
				if d.Memories[s].WriteDay <= d.Memories[f].WriteDay {
					cur = s
				}
			}
			d.Memories[f].Links = append(d.Memories[f].Links, d.Memories[cur].GID)
		}
	}
}

// before reports whether memory a is written before memory b.
func (d *Dataset) before(a, b int) bool {
	wa, wb := d.Memories[a].WriteDay, d.Memories[b].WriteDay
	return wa < wb || (wa == wb && a < b)
}

// linkCross links 2% of episodes to an earlier non-standard memory in another
// topic of the same namespace, far from the episode in vector space. The target
// is never superseded: recall drops a memory with a successor, so it could not
// be a reachable label.
func (d *Dataset) linkCross(r *rand.Rand, g *graphInfo) {
	var eps []int
	for _, ts := range d.topics {
		eps = append(eps, ts.eps...)
	}
	want := max(1, int(math.Ceil(crossLinkFraction*float64(len(eps)))))
	for _, pi := range r.Perm(len(eps)) {
		if len(g.crossLinks) >= want {
			return
		}
		x := eps[pi]
		for try := 0; try < 300; try++ {
			y := r.IntN(len(d.Memories))
			X, Y := &d.Memories[x], d.Memories[y]
			if Y.Topic == X.Topic || Y.Namespace != X.Namespace || Y.Kind == KindStandard || g.superseded[Y.GID] || !d.before(y, x) ||
				math.Abs(mock.Cosine(X.Vec, Y.Vec)) >= bridgeMaxCos {
				continue
			}
			X.Links = append(X.Links, Y.GID)
			g.crossLinks = append(g.crossLinks, [2]int{x, y})
			break
		}
	}
}

// buildEntities creates the entity pool, hubs first, then per-topic locals.
func (d *Dataset) buildEntities(r *rand.Rand, g *graphInfo) {
	p := d.Params
	// Locals per topic: enough that average load stays near 10 mentions, which
	// is why the pool exceeds Params.Entities at the floor (5,000 memories at
	// 1-3 mentions cannot fit 200 entities under a fan-out cap of 20).
	counts := make([]int, p.Topics)
	total := 0
	for t, ts := range d.topics {
		n := len(ts.std) + len(ts.facts) + len(ts.eps)
		counts[t] = max(3, (2*n+9)/10+2)
		total += counts[t]
	}
	for t := 0; total < p.Entities-int(math.Ceil(HubFraction*float64(p.Entities))); t = (t + 1) % p.Topics {
		counts[t]++
		total++
	}
	nHubs := max(1, int(math.Ceil(HubFraction*float64(total)/(1-HubFraction))))

	locals := make([][]string, p.Topics)
	for t := range locals {
		for e := 0; e < counts[t]; e++ {
			name := fmt.Sprintf("t%03d-e%d", t, e)
			locals[t] = append(locals[t], name)
			d.Entities = append(d.Entities, Entity{Name: name})
		}
	}
	for h := 0; h < nHubs; h++ {
		name := fmt.Sprintf("hub%03d", h)
		g.hubs = append(g.hubs, name)
		d.Entities = append(d.Entities, Entity{Name: name, Hub: true})
	}

	has := func(m *Memory, name string) bool {
		for _, e := range m.Entities {
			if e == name {
				return true
			}
		}
		return false
	}
	for _, hub := range g.hubs {
		for _, t := range d.hubTopics(r) {
			ts := d.topics[t]
			n := 0
			for _, pi := range r.Perm(len(ts.eps)) {
				m := &d.Memories[ts.eps[pi]]
				if n == hubMentionsPerTopic {
					break
				}
				if len(m.Entities) >= 3 || has(m, hub) {
					continue
				}
				m.Entities = append(m.Entities, hub)
				g.members[hub] = append(g.members[hub], ts.eps[pi])
				n++
			}
		}
	}

	load := map[string]int{}
	for i := range d.Memories {
		m := &d.Memories[i]
		pool := locals[m.Topic]
		for want := 1 + r.IntN(3); len(m.Entities) < want; {
			pick := func() string { return pool[r.IntN(len(pool))] }
			a, b := pick(), pick()
			if load[b] < load[a] {
				a = b
			}
			if load[a] >= entityFanoutCap || has(m, a) {
				// Rare: both picks full or taken. Scan for any free local.
				a = ""
				for _, name := range pool {
					if load[name] < entityFanoutCap && !has(m, name) {
						a = name
						break
					}
				}
				if a == "" {
					break
				}
			}
			m.Entities = append(m.Entities, a)
			load[a]++
		}
	}
}

// hubTopics picks the topics a hub spans, covering at least two namespaces when
// the dataset has them.
func (d *Dataset) hubTopics(r *rand.Rand) []int {
	k := min(hubTopicCount, len(d.topics))
	var pick []int
	for try := 0; try < 50; try++ {
		pick = r.Perm(len(d.topics))[:k]
		nss := map[string]bool{}
		for _, t := range pick {
			nss[string(d.topics[t].ns)] = true
		}
		if len(nss) >= 2 || d.Params.Namespaces < 2 {
			break
		}
	}
	return pick
}

// entityPair finds a hub-linked (X, Y) in different topics with Y far from X in
// vector space. X is episodic and the pair crosses namespaces when it can.
// Neither end is superseded, since both are labels.
func (d *Dataset) entityPair(r *rand.Rand, g *graphInfo) (x, y int, ok bool) {
	if len(g.hubs) == 0 {
		return 0, 0, false
	}
	for try := 0; try < 400; try++ {
		mem := g.members[g.hubs[r.IntN(len(g.hubs))]]
		if len(mem) < 2 {
			continue
		}
		x, y = mem[r.IntN(len(mem))], mem[r.IntN(len(mem))]
		X, Y := d.Memories[x], d.Memories[y]
		if X.Topic == Y.Topic || Y.Kind == KindStandard || g.superseded[X.GID] || g.superseded[Y.GID] ||
			(try < 300 && X.Kind != KindEpisode) || (try < 200 && X.Namespace == Y.Namespace) ||
			math.Abs(mock.Cosine(X.Vec, Y.Vec)) >= bridgeMaxCos {
			continue
		}
		return x, y, true
	}
	return 0, 0, false
}
