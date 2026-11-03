package eval

import (
	"math/rand/v2"
	"os"
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
	p := Params{Seed: 5, Namespaces: 2, Topics: 6, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 32, Mix: MixAgent}
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
		ns       engram.Namespace
		std, eps []Memory
		chains   map[int][]Memory
	}
	topics := map[int]*topic{}
	for _, m := range d.Memories {
		tp := topics[m.Topic]
		if tp == nil {
			tp = &topic{ns: m.Namespace, chains: map[int][]Memory{}}
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
			prev := ""
			if ch := tp.chains[m.Chain]; len(ch) > 0 {
				prev = ch[len(ch)-1].GID
			}
			tp.chains[m.Chain] = append(tp.chains[m.Chain], m)
			if m.Type != engram.Semantic || m.Importance != 0.5 {
				t.Fatalf("%s: type/importance", m.GID)
			}
			if m.Supersedes != prev {
				t.Fatalf("%s: supersedes %q, want chain predecessor %q", m.GID, m.Supersedes, prev)
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
		if n := len(tp.chains); n < 1 || n > 2 {
			t.Fatalf("topic %d: %d fact chains", id, n)
		}
		for c, ch := range tp.chains {
			if c < 1 || len(ch) < 2 || len(ch) > 4 {
				t.Fatalf("topic %d chain %d: %d versions", id, c, len(ch))
			}
			for i, f := range ch {
				if f.Version != i+1 {
					t.Fatalf("%s: version %d, want %d", f.GID, f.Version, i+1)
				}
			}
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

func TestFactChainCosine(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	chains := factChains(d)
	if len(chains) == 0 {
		t.Fatal("no fact chains")
	}
	lo, hi := 2.0, -2.0
	for _, ch := range chains {
		for a := 0; a < len(ch); a++ {
			for b := a + 1; b < len(ch); b++ {
				c := mock.Cosine(ch[a].Vec, ch[b].Vec)
				lo, hi = min(lo, c), max(hi, c)
			}
		}
	}
	t.Logf("within-chain cosine range [%.4f, %.4f]", lo, hi)
	if lo < 0.74 || hi > 0.88 {
		t.Fatalf("within-chain cosine range [%.4f, %.4f] outside [0.74, 0.88]", lo, hi)
	}
}

// factChains groups fact memories by (topic, chain), in version order.
func factChains(d *Dataset) [][]Memory {
	idx := map[[2]int]int{}
	var out [][]Memory
	for _, m := range d.Memories {
		if m.Kind != KindFact {
			continue
		}
		k := [2]int{m.Topic, m.Chain}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], m)
	}
	return out
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
	if maxCos >= 0.90 {
		t.Fatalf("max in-namespace cosine %.4f >= 0.90", maxCos)
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
		if c < minTwinCos || c > maxTwinCos {
			t.Fatalf("%s/%s: twin cosine %.4f outside [%.2f, %.2f]", m.GID, sib.GID, c, minTwinCos, maxTwinCos)
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
				ma, mb := d.Memories[idx[a]], d.Memories[idx[b]]
				if ma.Twin == mb.GID || mb.Twin == ma.GID || (ma.Kind == KindFact && mb.Kind == KindFact && ma.Chain == mb.Chain) {
					continue // twins and chain mates have their own band
				}
				c := mock.Cosine(ma.Vec, mb.Vec)
				lo, hi = min(lo, c), max(hi, c)
			}
		}
	}
	t.Logf("ordinary in-topic cosine range [%.4f, %.4f]", lo, hi)
	if lo < 0.40 || hi > 0.66 {
		t.Fatalf("ordinary in-topic cosine range [%.4f, %.4f] outside [0.40, 0.66]", lo, hi)
	}
}

func TestNoDedupCollisionsSeeds(t *testing.T) {
	if os.Getenv("ENGRAM_EVAL_SMOKE") != "1" {
		t.Skip("slow (~160 s); set ENGRAM_EVAL_SMOKE=1 to run")
	}
	t.Parallel()
	for _, seed := range []int64{1, 2, 5, 9} {
		p := Floor()
		p.Seed = seed
		d, err := Generate(p)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		_, byNS := inTopicAndNamespace(d)
		mx := -1.0
		for _, idx := range byNS {
			for a := 0; a < len(idx); a++ {
				for b := a + 1; b < len(idx); b++ {
					mx = max(mx, mock.Cosine(d.Memories[idx[a]].Vec, d.Memories[idx[b]].Vec))
				}
			}
		}
		t.Logf("seed %d max in-namespace cosine %.4f", seed, mx)
		if mx >= 0.90 {
			t.Fatalf("seed %d: max %.4f >= 0.90", seed, mx)
		}
	}
}

func TestGenerateUncheckedRejectsTinyDim(t *testing.T) {
	t.Parallel()
	p := Params{Seed: 5, Namespaces: 2, Topics: 6, Memories: 120, Entities: 12, Queries: 40, Days: 180, Dim: 4, Mix: MixAgent}
	if _, err := generateUnchecked(p); err == nil {
		t.Fatal("expected error for dim 4")
	}
}
