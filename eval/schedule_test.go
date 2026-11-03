package eval

import (
	"reflect"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func TestNoAccessBeforeWrite(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for _, a := range d.Accesses {
		m, ok := d.Lookup(a.GID)
		if !ok || a.Day < m.WriteDay || a.Day < 0 || a.Day >= d.Params.Days {
			t.Fatalf("access %+v invalid (write day %d)", a, m.WriteDay)
		}
	}
	for _, q := range d.Queries {
		if q.Day < 0 || q.Day >= d.Params.Days {
			t.Fatalf("%s: day %d", q.ID, q.Day)
		}
		for _, g := range q.Relevant {
			if m, _ := d.Lookup(g); q.Day < m.WriteDay {
				t.Fatalf("%s day %d before %s write day %d", q.ID, q.Day, g, m.WriteDay)
			}
		}
	}
	for _, m := range d.Memories {
		for _, l := range m.Links {
			y, ok := d.Lookup(l)
			if !ok || y.WriteDay > m.WriteDay {
				t.Fatalf("%s links %s written later", m.GID, l)
			}
		}
		if m.WriteDay < 0 || m.WriteDay >= d.Params.Days {
			t.Fatalf("%s: write day %d", m.GID, m.WriteDay)
		}
	}
	for i := 1; i < len(d.Queries); i++ {
		if d.Queries[i].Day < d.Queries[i-1].Day {
			t.Fatal("queries not ordered by day")
		}
	}
	for i := 1; i < len(d.Accesses); i++ {
		if d.Accesses[i].Day < d.Accesses[i-1].Day {
			t.Fatal("accesses not ordered by day")
		}
	}
}

func TestScheduleWindows(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for _, m := range d.Memories {
		switch {
		case m.Kind == KindStandard && m.Version == 1:
			if m.WriteDay < 0 || m.WriteDay > 10 {
				t.Fatalf("%s: v1 day %d", m.GID, m.WriteDay)
			}
		case m.Kind == KindStandard:
			if m.WriteDay < 30 || m.WriteDay > 90 {
				t.Fatalf("%s: later version day %d", m.GID, m.WriteDay)
			}
		case m.Kind == KindFact:
			if m.WriteDay < 0 || m.WriteDay > 120 {
				t.Fatalf("%s: fact day %d", m.GID, m.WriteDay)
			}
		}
	}
	for _, a := range d.Accesses {
		if m, _ := d.Lookup(a.GID); m.Kind == KindStandard && a.Day > 60 {
			t.Fatalf("standard %s accessed on day %d", a.GID, a.Day)
		}
	}
}

func classCounts(d *Dataset) map[QueryClass]int {
	got := map[QueryClass]int{}
	for _, q := range d.Queries {
		got[q.Class]++
	}
	return got
}

func near(n, want int) bool { return n >= want-2 && n <= want+2 }

func TestQueryMixAgent(t *testing.T) {
	t.Parallel()
	got := classCounts(floorDS(t))
	if !near(got[QTopicCurrent], 175) || !near(got[QFact], 100) || !near(got[QEpisodeRecent], 150) ||
		!near(got[QEpisodeOld], 25) || !near(got[QBridgeLink]+got[QBridgeEntity], 50) {
		t.Fatalf("agent query mix %v", got)
	}
	if !near(got[QBridgeLink], 25) || !near(got[QBridgeEntity], 25) {
		t.Fatalf("bridge split %v", got)
	}
}

func stressDS(t *testing.T) *Dataset {
	t.Helper()
	p := Floor()
	p.Seed, p.Mix = 42, MixStress
	d, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestQueryMixStress(t *testing.T) {
	t.Parallel()
	got := classCounts(stressDS(t))
	if !near(got[QTopicCurrent], 125) || !near(got[QFact], 75) || !near(got[QEpisodeRecent], 100) ||
		!near(got[QEpisodeOld], 75) || !near(got[QBridgeLink]+got[QBridgeEntity], 125) {
		t.Fatalf("stress query mix %v", got)
	}
}

func TestMixKeepsCorpus(t *testing.T) {
	t.Parallel()
	a, b := floorDS(t), stressDS(t)
	if !reflect.DeepEqual(a.Memories, b.Memories) || !reflect.DeepEqual(a.Entities, b.Entities) ||
		!reflect.DeepEqual(a.Accesses, b.Accesses) {
		t.Fatal("mix changed the corpus")
	}
}

func TestTopicCurrentPicksCurrentVersion(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	n := 0
	for _, q := range d.Queries {
		if q.Class != QTopicCurrent {
			continue
		}
		n++
		if q.Day < 150 || q.Day > 179 {
			t.Fatalf("%s: day %d", q.ID, q.Day)
		}
		first, _ := d.Lookup(q.Relevant[0])
		var want Memory
		for _, s := range d.Memories {
			if s.Kind == KindStandard && s.Topic == first.Topic && s.WriteDay <= q.Day && s.WriteDay >= want.WriteDay {
				want = s
			}
		}
		if len(q.Relevant) != 1 || q.Relevant[0] != want.GID {
			t.Fatalf("%s day %d: relevant %v, want [%s]", q.ID, q.Day, q.Relevant, want.GID)
		}
		if len(q.Namespaces) != 1 || q.Namespaces[0] != first.Namespace {
			t.Fatalf("%s: namespaces %v", q.ID, q.Namespaces)
		}
	}
	if n == 0 {
		t.Fatal("no topic-current queries")
	}
}

func TestLatestAt(t *testing.T) {
	t.Parallel()
	d := &Dataset{Memories: []Memory{{WriteDay: 10}, {WriteDay: 70}, {WriteDay: 115}}}
	chain := []int{0, 1, 2}
	for _, c := range []struct{ day, want int }{{5, 0}, {10, 0}, {70, 1}, {100, 1}, {115, 2}, {179, 2}} {
		if got := d.latestAt(chain, c.day); got != c.want {
			t.Errorf("day %d: latest version index %d, want %d", c.day, got, c.want)
		}
	}
}

func TestFactQueryPicksLatestVersion(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	n := 0
	for _, q := range d.Queries {
		if q.Class != QFact {
			continue
		}
		n++
		if len(q.Relevant) != 1 {
			t.Fatalf("%s: relevant %v", q.ID, q.Relevant)
		}
		rel, _ := d.Lookup(q.Relevant[0])
		var want Memory
		for _, m := range d.Memories {
			if m.Kind == KindFact && m.Topic == rel.Topic && m.Chain == rel.Chain && m.WriteDay <= q.Day && m.Version > want.Version {
				want = m
			}
		}
		if rel.GID != want.GID {
			t.Fatalf("%s day %d: relevant %s, want latest written %s", q.ID, q.Day, rel.GID, want.GID)
		}
		for _, m := range d.Memories {
			if m.Kind != KindFact || m.Topic != rel.Topic || m.Chain != rel.Chain || m.GID == rel.GID {
				continue
			}
			if c := mock.Cosine(q.Vec, m.Vec); c < 0.80 {
				t.Fatalf("%s: cos(query, %s) = %.4f < 0.80", q.ID, m.GID, c)
			}
		}
	}
	if n == 0 {
		t.Fatal("no fact queries")
	}
}

func TestEntityBridgeScope(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	crossNS := 0
	for _, q := range d.Queries {
		switch q.Class {
		case QBridgeEntity:
			x, _ := d.Lookup(q.Relevant[0])
			y, _ := d.Lookup(q.Relevant[1])
			want := map[engram.Namespace]bool{x.Namespace: true, y.Namespace: true}
			got := map[engram.Namespace]bool{}
			for _, ns := range q.Namespaces {
				got[ns] = true
			}
			if len(got) != len(want) || len(q.Namespaces) != len(want) {
				t.Fatalf("%s: namespaces %v, want %v", q.ID, q.Namespaces, want)
			}
			for ns := range want {
				if !got[ns] {
					t.Fatalf("%s: namespaces %v, want %v", q.ID, q.Namespaces, want)
				}
			}
			if len(want) == 2 {
				crossNS++
			}
		case QBridgeLink:
			x, _ := d.Lookup(q.Relevant[0])
			if len(q.Namespaces) != 1 || q.Namespaces[0] != x.Namespace {
				t.Fatalf("%s: namespaces %v", q.ID, q.Namespaces)
			}
		}
	}
	if crossNS == 0 {
		t.Fatal("no entity bridge crosses namespaces")
	}
}

func TestQueryTargetsAndLabels(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for _, q := range d.Queries {
		if q.Content != q.ID || len(q.Vec) != d.Params.Dim || len(q.Relevant) == 0 {
			t.Fatalf("%s: malformed query", q.ID)
		}
		first, _ := d.Lookup(q.Relevant[0])
		switch q.Class {
		case QFact:
			if first.Kind != KindFact || len(q.Relevant) != 1 {
				t.Fatalf("%s: fact query relevant %v", q.ID, q.Relevant)
			}
			if q.Day < first.WriteDay+60 && q.Day != d.Params.Days-1 {
				t.Fatalf("%s: day %d", q.ID, q.Day)
			}
		case QEpisodeRecent, QEpisodeOld:
			if first.Kind != KindEpisode || len(q.Relevant) != 1 {
				t.Fatalf("%s: episode query relevant %v", q.ID, q.Relevant)
			}
		}
	}
}
