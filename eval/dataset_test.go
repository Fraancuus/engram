package eval

import (
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

var (
	floorOnce sync.Once
	floorData *Dataset
	floorErr  error
)

// floorDS returns one shared dataset generated at the floor with seed 42.
func floorDS(t *testing.T) *Dataset {
	t.Helper()
	floorOnce.Do(func() {
		p := Floor()
		p.Seed = 42
		floorData, floorErr = Generate(p)
	})
	if floorErr != nil {
		t.Fatalf("Generate(Floor): %v", floorErr)
	}
	return floorData
}

func TestGenerateDeterministic(t *testing.T) {
	t.Parallel()
	p := Floor()
	p.Seed = 42
	start := time.Now()
	a, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Generate(Floor) took %s", time.Since(start))
	b, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() != b.Hash() {
		t.Fatal("same seed produced different hashes")
	}
	p.Seed = 43
	c, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() == c.Hash() {
		t.Fatal("different seeds produced equal hashes")
	}
}

func TestGenerateRejectsBelowFloor(t *testing.T) {
	t.Parallel()
	p := Floor()
	p.Memories = 4999
	if _, err := Generate(p); err == nil {
		t.Fatal("expected error for 4,999 memories")
	}
}

func TestGenerateUncheckedSmall(t *testing.T) {
	t.Parallel()
	p := Params{Seed: 5, Namespaces: 2, Topics: 6, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 16}
	d, err := generateUnchecked(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Memories) < 120 || len(d.Queries) != 40 {
		t.Fatalf("got %d memories, %d queries", len(d.Memories), len(d.Queries))
	}
}

func TestTopicComposition(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	if len(d.Memories) < 5000 || len(d.Queries) < 500 {
		t.Fatalf("got %d memories, %d queries", len(d.Memories), len(d.Queries))
	}
	type topic struct {
		ns              engram.Namespace
		std, facts, eps []Memory
	}
	topics := map[int]*topic{}
	for _, m := range d.Memories {
		tp := topics[m.Topic]
		if tp == nil {
			tp = &topic{ns: m.Namespace}
			topics[m.Topic] = tp
		}
		if m.Namespace != tp.ns {
			t.Fatalf("%s: topic spans namespaces", m.GID)
		}
		switch m.Kind {
		case KindStandard:
			tp.std = append(tp.std, m)
			if m.Type != engram.Procedural || m.Importance != 0.7 {
				t.Fatalf("%s: type/importance", m.GID)
			}
		case KindFact:
			tp.facts = append(tp.facts, m)
			if m.Type != engram.Semantic || m.Importance != 0.5 {
				t.Fatalf("%s: type/importance", m.GID)
			}
		case KindEpisode:
			tp.eps = append(tp.eps, m)
			if m.Type != engram.Episodic || m.Importance < 0.3 || m.Importance > 0.5 {
				t.Fatalf("%s: type/importance %v", m.GID, m.Importance)
			}
		}
	}
	if len(topics) != d.Params.Topics {
		t.Fatalf("got %d topics", len(topics))
	}
	for id, tp := range topics {
		if n := len(tp.std); n < 1 || n > 3 {
			t.Fatalf("topic %d: %d standard versions", id, n)
		}
		if n := len(tp.facts); n < 2 || n > 4 {
			t.Fatalf("topic %d: %d facts", id, n)
		}
		if len(tp.eps) < 1 {
			t.Fatalf("topic %d: no episodes", id)
		}
		for i, s := range tp.std {
			want := ""
			if i > 0 {
				want = tp.std[i-1].GID
			}
			if s.Version != i+1 || s.Supersedes != want {
				t.Fatalf("%s: version %d supersedes %q, want %q", s.GID, s.Version, s.Supersedes, want)
			}
		}
	}
}

// inTopicAndNamespace groups memory indices by topic and by namespace.
func inTopicAndNamespace(d *Dataset) (byTopic [][]int, byNS [][]int) {
	topics := map[int][]int{}
	nss := map[engram.Namespace][]int{}
	for i, m := range d.Memories {
		topics[m.Topic] = append(topics[m.Topic], i)
		nss[m.Namespace] = append(nss[m.Namespace], i)
	}
	tk := make([]int, 0, len(topics))
	for k := range topics {
		tk = append(tk, k)
	}
	sort.Ints(tk)
	for _, k := range tk {
		byTopic = append(byTopic, topics[k])
	}
	nk := make([]string, 0, len(nss))
	for k := range nss {
		nk = append(nk, string(k))
	}
	sort.Strings(nk)
	for _, k := range nk {
		byNS = append(byNS, nss[engram.Namespace(k)])
	}
	return byTopic, byNS
}

func TestNoDedupCollisions(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	_, byNS := inTopicAndNamespace(d)
	maxCos := -1.0
	for _, idx := range byNS {
		for a := 0; a < len(idx); a++ {
			for b := a + 1; b < len(idx); b++ {
				maxCos = max(maxCos, mock.Cosine(d.Memories[idx[a]].Vec, d.Memories[idx[b]].Vec))
			}
		}
	}
	t.Logf("max in-namespace cosine %.4f", maxCos)
	if maxCos >= 0.95 {
		t.Fatalf("max in-namespace cosine %.4f >= 0.95", maxCos)
	}
}

func TestTwinRange(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	n := 0
	for _, m := range d.Memories {
		if m.Twin == "" {
			continue
		}
		sib, ok := d.Lookup(m.Twin)
		if !ok {
			t.Fatalf("%s: twin %s missing", m.GID, m.Twin)
		}
		c := mock.Cosine(m.Vec, sib.Vec)
		if c < 0.86 || c > 0.94 {
			t.Fatalf("%s/%s: twin cosine %.4f outside [0.86, 0.94]", m.GID, sib.GID, c)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no twins generated")
	}
}

func TestCrossTopicLow(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // reproducible sampling
	for n := 0; n < 2000; {
		a, b := d.Memories[r.IntN(len(d.Memories))], d.Memories[r.IntN(len(d.Memories))]
		if a.Topic == b.Topic {
			continue
		}
		n++
		if c := mock.Cosine(a.Vec, b.Vec); c >= 0.30 || c <= -0.30 {
			t.Fatalf("%s/%s: cross-topic cosine %.4f", a.GID, b.GID, c)
		}
	}
}

func TestInTopicRange(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	byTopic, _ := inTopicAndNamespace(d)
	lo, hi := 2.0, -2.0
	for _, idx := range byTopic {
		for a := 0; a < len(idx); a++ {
			for b := a + 1; b < len(idx); b++ {
				c := mock.Cosine(d.Memories[idx[a]].Vec, d.Memories[idx[b]].Vec)
				lo, hi = min(lo, c), max(hi, c)
			}
		}
	}
	t.Logf("in-topic cosine range [%.4f, %.4f]", lo, hi)
	if lo < 0.70 || hi > 0.94 {
		t.Fatalf("in-topic cosine range [%.4f, %.4f] outside [0.70, 0.94]", lo, hi)
	}
}
