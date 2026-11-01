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

// Memory is one generated memory. Content equals GID and doubles as the embedder
// lookup key.
type Memory struct {
	GID        string
	Topic      int
	Namespace  engram.Namespace
	Type       engram.MemoryType
	Kind       Kind
	Version    int // standards only, 1-based
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
	std, facts, eps []int
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
		nStd, nFact := 1+r.IntN(3), 2+r.IntN(3)
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
		for f := 0; f < nFact; f++ {
			ts.facts = append(ts.facts, add(Memory{GID: fmt.Sprintf("t%03d/fact/%d", t, f), Type: engram.Semantic, Kind: KindFact, Importance: 0.5}))
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
// noise overlap; a twin shares most of its sibling's noise.
func (d *Dataset) assignVectors(r *rand.Rand) error {
	dim := d.Params.Dim
	cents := centroids(r, d.Params.Topics, dim)
	noise := make([]engram.Vector, len(d.Memories))
	for t, ts := range d.topics {
		basis := []engram.Vector{cents[t]}
		for k := 0; k < 3; k++ {
			basis = append(basis, unitOrtho(r, dim, basis...))
		}
		offs := basis[1:]
		for _, group := range [][]int{ts.std, ts.facts, ts.eps} {
			for _, i := range group {
				m := &d.Memories[i]
				ok := false
				for try := 0; try < maxNoiseTries && !ok; try++ {
					if m.Twin == "" {
						noise[i] = unitOrtho(r, dim, basis...)
					} else {
						sib := noise[d.byGID[m.Twin]]
						noise[i] = mix(sib, nil, 0, unitOrtho(r, dim, append(basis[:4:4], sib)...), sigmaTwin)
					}
					m.Vec = mix(cents[t], offs[m.Kind], kindOffsetScale, noise[i], sigmaInTopic)
					ok = noiseIsClean(noise[i], cents, t) && d.farFromTopicMates(i, ts)
				}
				if !ok {
					return fmt.Errorf("assign vector for %s (topic %d): no noise draw satisfies the cosine bounds in %d tries", m.GID, t, maxNoiseTries)
				}
			}
		}
	}
	return nil
}

// farFromTopicMates reports whether memory i, already holding a candidate
// vector, stays under maxInTopicCos with every earlier-assigned memory of its
// topic. This enforces the dedup bound (0.95) by construction.
func (d *Dataset) farFromTopicMates(i int, ts topicSpan) bool {
	for _, group := range [][]int{ts.std, ts.facts, ts.eps} {
		for _, j := range group {
			if j == i {
				return true // memories are assigned in std, facts, eps order
			}
			if mock.Cosine(d.Memories[i].Vec, d.Memories[j].Vec) >= maxInTopicCos {
				return false
			}
		}
	}
	return true
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
