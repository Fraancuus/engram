package eval

import (
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func mentions(d *Dataset) map[string][]Memory {
	out := map[string][]Memory{}
	for _, m := range d.Memories {
		for _, e := range m.Entities {
			out[e] = append(out[e], m)
		}
	}
	return out
}

func TestHubSpansNamespaces(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	ms := mentions(d)
	hubs := 0
	for _, e := range d.Entities {
		if !e.Hub {
			continue
		}
		hubs++
		topics, nss := map[int]bool{}, map[engram.Namespace]bool{}
		for _, m := range ms[e.Name] {
			topics[m.Topic], nss[m.Namespace] = true, true
		}
		if len(topics) < 3 || len(nss) < 2 {
			t.Fatalf("hub %s: %d topics, %d namespaces", e.Name, len(topics), len(nss))
		}
	}
	if hubs == 0 {
		t.Fatal("no hubs")
	}
}

func TestHubFanoutUnderCap(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for name, ms := range mentions(d) {
		if len(ms) >= 20 {
			t.Fatalf("entity %s mentioned by %d memories", name, len(ms))
		}
	}
	for _, m := range d.Memories {
		if n := len(m.Entities); n < 1 || n > 3 {
			t.Fatalf("%s: %d entities", m.GID, n)
		}
	}
	if len(d.Entities) < d.Params.Entities {
		t.Fatalf("%d entities, want >= %d", len(d.Entities), d.Params.Entities)
	}
}

func neighbors(x, y Memory) bool {
	for _, l := range x.Links {
		if l == y.GID {
			return true
		}
	}
	for _, l := range y.Links {
		if l == x.GID {
			return true
		}
	}
	for _, a := range x.Entities {
		for _, b := range y.Entities {
			if a == b {
				return true
			}
		}
	}
	return false
}

func bridgeQueries(d *Dataset) []Query {
	var out []Query
	for _, q := range d.Queries {
		if q.Class == QBridgeLink || q.Class == QBridgeEntity {
			out = append(out, q)
		}
	}
	return out
}

func TestBridgeOnlyUnreachableByVector(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	bq := bridgeQueries(d)
	if len(bq) == 0 {
		t.Fatal("no bridge queries")
	}
	for _, q := range bq {
		x, _ := d.Lookup(q.Relevant[0])
		y, _ := d.Lookup(q.Relevant[1])
		if c := mock.Cosine(q.Vec, y.Vec); c >= 0.30 {
			t.Fatalf("%s: cos(q, Y) = %.4f", q.ID, c)
		}
		if !neighbors(x, y) {
			t.Fatalf("%s: Y %s not a link/entity neighbor of X %s", q.ID, y.GID, x.GID)
		}
		if x.Topic == y.Topic {
			t.Fatalf("%s: X and Y share topic %d", q.ID, x.Topic)
		}
		if q.Class == QBridgeLink {
			linked := false
			for _, l := range x.Links {
				linked = linked || l == y.GID
			}
			if !linked {
				t.Fatalf("%s: link bridge without link", q.ID)
			}
		}
	}
}

func TestHalfBridgeHopsEpisodic(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	bq := bridgeQueries(d)
	n := 0
	for _, q := range bq {
		if x, _ := d.Lookup(q.Relevant[0]); x.Kind == KindEpisode {
			n++
		}
	}
	if 2*n < len(bq) {
		t.Fatalf("%d of %d hop nodes episodic", n, len(bq))
	}
}

func TestFactLinksCurrentStandard(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for _, f := range d.Memories {
		if f.Kind != KindFact {
			continue
		}
		var cur Memory
		for _, s := range d.Memories {
			if s.Kind == KindStandard && s.Topic == f.Topic && s.WriteDay <= f.WriteDay {
				cur = s // chain is in version order, so the last match is current
			}
		}
		if cur.GID == "" || f.Links[len(f.Links)-1] != cur.GID || len(f.Links) != min(f.Version, 2) {
			t.Fatalf("%s (day %d): links %v, want the current standard %s last", f.GID, f.WriteDay, f.Links, cur.GID)
		}
	}
	crossTopic := 0
	for _, m := range d.Memories {
		if m.Kind != KindEpisode {
			continue
		}
		for _, l := range m.Links {
			if y, _ := d.Lookup(l); y.Topic != m.Topic {
				crossTopic++
			}
		}
	}
	if crossTopic < 50 {
		t.Fatalf("only %d cross-topic episode links", crossTopic)
	}
}

func TestFactChainLinks(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	for _, ch := range factChains(d) {
		for i, f := range ch {
			if i == 0 {
				continue
			}
			if len(f.Links) != 2 || f.Links[0] != ch[i-1].GID {
				t.Fatalf("%s: links %v, want [%s, current standard]", f.GID, f.Links, ch[i-1].GID)
			}
			if f.WriteDay <= ch[i-1].WriteDay {
				t.Fatalf("%s: day %d not after predecessor day %d", f.GID, f.WriteDay, ch[i-1].WriteDay)
			}
		}
	}
}

func TestFloorEntityPool(t *testing.T) {
	t.Parallel()
	d := floorDS(t)
	if len(d.Entities) < 1200 {
		t.Fatalf("entity pool %d < 1200", len(d.Entities))
	}
	t.Logf("entity pool %d", len(d.Entities))
}
