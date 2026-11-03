package eval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"math/rand/v2"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

// Kind is the generator-side memory kind.
type Kind int

// Memory kinds.
const (
	KindStandard Kind = iota // procedural, versioned
	KindFact                 // semantic, write-once
	KindEpisode              // episodic
)

// String is the kind's name as printed in the report.
func (k Kind) String() string {
	switch k {
	case KindStandard:
		return "standard"
	case KindFact:
		return "fact"
	case KindEpisode:
		return "episode"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// kinds is every kind in report order.
var kinds = []Kind{KindStandard, KindFact, KindEpisode}

// QueryClass labels how a query's relevant set was built.
type QueryClass int

// Query classes.
const (
	QTopicCurrent QueryClass = iota
	QFact
	QEpisodeRecent
	QEpisodeOld
	QBridgeLink
	QBridgeEntity
)

// queryClasses is every class in report order.
var queryClasses = []QueryClass{QTopicCurrent, QFact, QEpisodeRecent, QEpisodeOld, QBridgeLink, QBridgeEntity}

// String is the class name as printed in the report.
func (c QueryClass) String() string {
	switch c {
	case QTopicCurrent:
		return "topic-current"
	case QFact:
		return "fact"
	case QEpisodeRecent:
		return "episode-recent"
	case QEpisodeOld:
		return "episode-old"
	case QBridgeLink:
		return "bridge-link"
	case QBridgeEntity:
		return "bridge-entity"
	}
	return fmt.Sprintf("class(%d)", int(c))
}

// Memory is one generated memory. Content equals GID and doubles as the embedder
// lookup key.
type Memory struct {
	GID        string
	Topic      int
	Namespace  engram.Namespace
	Type       engram.MemoryType
	Kind       Kind
	Version    int // standards and fact versions, 1-based
	Chain      int // fact chain within the topic, 1-based; 0 for non-facts
	Content    string
	Vec        engram.Vector
	Importance float64
	Entities   []string
	Links      []string // GIDs written no later than this memory
	Supersedes string   // GID of the previous standard version
	WriteDay   int
	Twin       string // GID of the sibling episode this one is a near-copy of
}

// Entity is a named entity; hubs are mentioned across topics and namespaces.
type Entity struct {
	Name string
	Hub  bool
}

// Access is a follow-up recall of a memory's own vector on a day.
type Access struct {
	GID string
	Day int
}

// Query is a scored recall with generator-owned ground truth.
type Query struct {
	ID         string
	Class      QueryClass
	Day        int
	Content    string // equals ID; the embedder lookup key for Vec
	Vec        engram.Vector
	Namespaces []engram.Namespace
	Relevant   []string
}

// Dataset is a fully generated eval corpus.
type Dataset struct {
	Params   Params
	Memories []Memory
	Entities []Entity
	Accesses []Access
	Queries  []Query

	byGID  map[string]int
	topics []topicSpan
}

// topicSpan indexes one topic's memories in Dataset.Memories.
type topicSpan struct {
	ns              engram.Namespace
	std, facts, eps []int // facts is every chain's versions, chain by chain
	chains          [][]int
	chainCents      []engram.Vector // chain centroids, parallel to chains
}

// Generate builds the dataset for p, rejecting params below the floor.
func Generate(p Params) (*Dataset, error) {
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("generate dataset: %w", err)
	}
	return generateUnchecked(p)
}

// generateUnchecked is Generate without the floor check, for small test datasets.
func generateUnchecked(p Params) (*Dataset, error) {
	if p.Namespaces < 1 || p.Topics < 1 || p.Memories < p.Topics || p.Dim < 5 || p.Days < 1 || p.Queries < 1 {
		return nil, errors.New("generate dataset: params must be positive and memories >= topics")
	}
	r := newRNG(p.Seed)
	d := &Dataset{Params: p}
	d.buildMemories(r)
	if err := d.assignVectors(r); err != nil {
		return nil, fmt.Errorf("generate dataset: %w", err)
	}
	d.assignWriteDays(r)
	g := d.buildGraph(r)
	d.buildAccesses(r)
	if err := d.buildQueries(r, g); err != nil {
		return nil, fmt.Errorf("generate dataset: %w", err)
	}
	return d, nil
}

// buildMemories lays out each topic's standards, facts and episodes.
func (d *Dataset) buildMemories(r *rand.Rand) {
	p := d.Params
	per := p.Memories / p.Topics
	d.byGID = make(map[string]int, p.Memories)
	for t := 0; t < p.Topics; t++ {
		share := per
		if t == p.Topics-1 {
			share = p.Memories - per*(p.Topics-1)
		}
		ns := engram.Namespace(fmt.Sprintf("ns%d", t%p.Namespaces))
		nStd := 1 + r.IntN(3)
		sizes := make([]int, 1+r.IntN(2))
		nFact := 0
		for c := range sizes {
			sizes[c] = 2 + r.IntN(3)
			nFact += sizes[c]
		}
		nEp := max(share-nStd-nFact, 1)
		ts := topicSpan{ns: ns}
		add := func(m Memory) int {
			m.Topic, m.Namespace, m.Content = t, ns, m.GID
			d.Memories = append(d.Memories, m)
			d.byGID[m.GID] = len(d.Memories) - 1
			return len(d.Memories) - 1
		}
		for v := 1; v <= nStd; v++ {
			m := Memory{GID: fmt.Sprintf("t%03d/std/v%d", t, v), Type: engram.Procedural, Kind: KindStandard, Version: v, Importance: 0.7}
			if v > 1 {
				m.Supersedes = d.Memories[ts.std[v-2]].GID
			}
			ts.std = append(ts.std, add(m))
		}
		for c, n := range sizes {
			var chain []int
			for v := 1; v <= n; v++ {
				m := Memory{GID: fmt.Sprintf("t%03d/fact/c%d/v%d", t, c+1, v), Type: engram.Semantic, Kind: KindFact, Version: v, Chain: c + 1, Importance: 0.5}
				chain = append(chain, add(m))
			}
			ts.facts = append(ts.facts, chain...)
			ts.chains = append(ts.chains, chain)
		}
		for e := 0; e < nEp; e++ {
			m := Memory{GID: fmt.Sprintf("t%03d/ep/%04d", t, e), Type: engram.Episodic, Kind: KindEpisode, Importance: 0.3 + 0.2*r.Float64()}
			if e%10 == 9 {
				m.Twin = d.Memories[ts.eps[e-1]].GID
			}
			ts.eps = append(ts.eps, add(m))
		}
		d.topics = append(d.topics, ts)
	}
}

// assignVectors gives every memory its vector. Offsets and noise are orthogonal
// to the centroid and to each other, so in-topic cosine depends only on the
// noise overlap; a twin shares most of its sibling's noise, and a fact version
// is its chain's centroid plus small orthogonal version noise.
func (d *Dataset) assignVectors(r *rand.Rand) error {
	dim := d.Params.Dim
	cents := centroids(r, d.Params.Topics, dim)
	noise := make([]engram.Vector, len(d.Memories))
	for t := range d.topics {
		ts := &d.topics[t]
		basis := []engram.Vector{cents[t]}
		for k := 0; k < 3; k++ {
			basis = append(basis, unitOrtho(r, dim, basis...))
		}
		offs := basis[1:]
		plain := func(group []int) error {
			for _, i := range group {
				m := &d.Memories[i]
				n, ok := d.bestDraw(i, *ts, cents, func() (engram.Vector, engram.Vector) {
					var n engram.Vector
					if m.Twin == "" {
						n = unitOrtho(r, dim, basis...)
					} else {
						sib := noise[d.byGID[m.Twin]]
						n = mix(sib, nil, 0, unitOrtho(r, dim, append(basis[:4:4], sib)...), sigmaTwin)
					}
					return n, mix(cents[t], offs[m.Kind], kindOffsetScale, n, sigmaInTopic)
				})
				noise[i] = n
				if !ok {
					return fmt.Errorf("assign vector for %s (topic %d): no noise draw satisfies the cosine bounds in %d tries", m.GID, t, maxNoiseTries)
				}
			}
			return nil
		}
		if err := plain(ts.std); err != nil {
			return err
		}
		for _, chain := range ts.chains {
			var cc engram.Vector
			var err error
			// A chain whose centroid sits badly among the topic's other members
			// gets a fresh centroid instead of failing the whole dataset.
			for try := 0; try < maxChainTries; try++ {
				if cc, err = d.assignChain(r, chain, basis, cents, t, *ts); err == nil {
					break
				}
			}
			if err != nil {
				return err
			}
			ts.chainCents = append(ts.chainCents, cc)
		}
		if err := plain(ts.eps); err != nil {
			return err
		}
	}
	return nil
}

// assignChain draws a chain centroid and gives every version of the chain a
// vector near it, returning the centroid. basis is the topic's orthonormal
// (centroid, offsets) frame.
func (d *Dataset) assignChain(r *rand.Rand, chain []int, basis []engram.Vector, cents []engram.Vector, t int, ts topicSpan) (engram.Vector, error) {
	dim := d.Params.Dim
	var cn engram.Vector
	clean := false
	for try := 0; try < maxNoiseTries && !clean; try++ {
		cn = unitOrtho(r, dim, basis...)
		clean = noiseIsClean(cn, cents, t)
	}
	if !clean {
		return nil, fmt.Errorf("assign chain centroid for topic %d chain %d: no clean noise draw in %d tries", t, d.Memories[chain[0]].Chain, maxNoiseTries)
	}
	cc := mix(cents[t], basis[1+int(KindFact)], kindOffsetScale, cn, sigmaChain)
	for _, i := range chain {
		m := &d.Memories[i]
		_, ok := d.bestDraw(i, ts, cents, func() (engram.Vector, engram.Vector) {
			vn := unitOrtho(r, dim, append(basis[:4:4], cn)...)
			return vn, mix(cc, nil, 0, vn, sigmaFactVersion)
		})
		if !ok {
			return nil, fmt.Errorf("assign vector for %s (topic %d): no version noise satisfies the cosine bounds in %d tries", m.GID, t, maxNoiseTries)
		}
	}
	return cc, nil
}

// bestDraw sets memory i's vector from draw, which returns a (noise, vector)
// candidate. It takes the cleanest candidate of each batch of drawBatch (clean
// noise, smallest bandScore) and accepts the first batch whose best candidate is
// inside its bands. Plain first-fit rejection cannot place the late members of a
// ~100-member topic in a 0.29-wide cosine band, so the draw is a best-of-batch
// pick. It reports false after maxNoiseTries candidates.
func (d *Dataset) bestDraw(i int, ts topicSpan, cents []engram.Vector, draw func() (noise, vec engram.Vector)) (engram.Vector, bool) {
	own := d.Memories[i].Topic
	for tried := 0; tried < maxNoiseTries; tried += drawBatch {
		best := math.Inf(1)
		var bestNoise, bestVec engram.Vector
		for b := 0; b < drawBatch; b++ {
			n, v := draw()
			if !noiseIsClean(n, cents, own) {
				continue
			}
			d.Memories[i].Vec = v
			if sc := d.bandScore(i, ts); sc < best {
				best, bestNoise, bestVec = sc, n, v
			}
		}
		if best <= 1 {
			d.Memories[i].Vec = bestVec
			return bestNoise, true
		}
	}
	return nil, false
}

// bandScore returns how far memory i, already holding a candidate vector, sits
// from the middle of its band against every earlier-assigned memory of its topic,
// as a fraction of the band's half-width: at most 1 means every pair is inside
// its band. The band is [minLinkedCos, maxLinkedCos] for a twin pair or two
// versions of one fact chain and [minOrdinaryCos, maxOrdinaryCos] for any other
// pair. This enforces the auto-link and dedup scales (see vectors.go) by
// construction.
func (d *Dataset) bandScore(i int, ts topicSpan) float64 {
	mi := d.Memories[i]
	worst := 0.0
	for _, group := range [][]int{ts.std, ts.facts, ts.eps} {
		for _, j := range group {
			if j == i {
				return worst // memories are assigned in std, facts, eps order
			}
			mj := d.Memories[j]
			lo, hi := minOrdinaryCos, maxOrdinaryCos
			if mi.Twin == mj.GID || mj.Twin == mi.GID || (mi.Kind == KindFact && mj.Kind == KindFact && mi.Chain == mj.Chain) {
				lo, hi = minLinkedCos, maxLinkedCos
			}
			worst = max(worst, math.Abs(mock.Cosine(mi.Vec, mj.Vec)-(lo+hi)/2)/((hi-lo)/2))
		}
	}
	return worst
}

// noiseIsClean reports whether noise correlates at most noiseLeakMax with every
// centroid but its own; leakier noise would push cross-topic cosine up.
func noiseIsClean(noise engram.Vector, cents []engram.Vector, own int) bool {
	for j, c := range cents {
		if j == own {
			continue
		}
		var dot float64
		for i := range noise {
			dot += float64(noise[i]) * float64(c[i])
		}
		if math.Abs(dot) > noiseLeakMax {
			return false
		}
	}
	return true
}

// Lookup returns the memory with the given GID.
func (d *Dataset) Lookup(gid string) (Memory, bool) {
	i, ok := d.byGID[gid]
	if !ok {
		return Memory{}, false
	}
	return d.Memories[i], true
}

// Hash returns a sha256 over a canonical encoding of the dataset.
func (d *Dataset) Hash() string {
	h := sha256.New()
	str := func(s string) {
		putInt(h, int64(len(s)))
		_, _ = h.Write([]byte(s))
	}
	strs := func(ss []string) {
		putInt(h, int64(len(ss)))
		for _, s := range ss {
			str(s)
		}
	}
	vec := func(v engram.Vector) {
		putInt(h, int64(len(v)))
		for _, x := range v {
			putInt(h, int64(math.Round(float64(x)*1e6)))
		}
	}
	for _, m := range d.Memories {
		str(m.GID)
		str(string(m.Type))
		str(string(m.Namespace))
		putInt(h, int64(m.WriteDay))
		putInt(h, int64(math.Round(m.Importance*1e6)))
		putInt(h, int64(m.Chain))
		str(m.Supersedes)
		str(m.Twin)
		strs(m.Entities)
		strs(m.Links)
		vec(m.Vec)
	}
	for _, e := range d.Entities {
		str(e.Name)
		if e.Hub {
			putInt(h, 1)
		} else {
			putInt(h, 0)
		}
	}
	for _, a := range d.Accesses {
		str(a.GID)
		putInt(h, int64(a.Day))
	}
	for _, q := range d.Queries {
		str(q.ID)
		putInt(h, int64(q.Class))
		putInt(h, int64(q.Day))
		strs(q.Relevant)
		nss := make([]string, len(q.Namespaces))
		for i, ns := range q.Namespaces {
			nss[i] = string(ns)
		}
		strs(nss)
		vec(q.Vec)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func putInt(h hash.Hash, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v)) //nolint:gosec // two's-complement bits, intended
	_, _ = h.Write(b[:])
}
