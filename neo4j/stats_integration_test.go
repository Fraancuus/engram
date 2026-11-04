//go:build integration

package neo4j_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
	eneo4j "github.com/Fraancuus/engram/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// statsRow is one fixture memory.
type statsRow struct {
	id        engram.MemoryID
	ns        engram.Namespace
	typ       engram.MemoryType
	sup, forg bool
	pinned    bool
}

func putRows(t *testing.T, s *eneo4j.Store, rows []statsRow) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, r := range rows {
		m := engram.Memory{
			ID: r.id, Namespace: r.ns, Type: r.typ, Content: "secret content", Source: "secret source",
			Embedding: embedding(384), Importance: 0.5, Stability: 2, AccessCount: 3,
			CreatedAt: now, LastAccessed: now,
			Superseded: r.sup, Forgotten: r.forg, Pinned: r.pinned,
		}
		if err := s.Put(ctx, m); err != nil {
			t.Fatalf("Put %s: %v", r.id, err)
		}
		id := r.id
		t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	}
}

// statsFixture is the shared fixture of the m6 plan with per-run names.
func statsFixture(t *testing.T) (s *eneo4j.Store, a, b engram.Namespace, ids [4]engram.MemoryID) {
	t.Helper()
	s = testStore(t)
	ctx := context.Background()
	a = engram.Namespace(string(uniqueID("stats-a")))
	b = engram.Namespace(string(uniqueID("stats-b")))
	for i := range ids {
		ids[i] = uniqueID(fmt.Sprintf("stats-m%d", i+1))
	}
	putRows(t, s, []statsRow{
		{id: ids[0], ns: a, typ: engram.Episodic, sup: true, forg: true},
		{id: ids[1], ns: a, typ: engram.Episodic},
		{id: ids[2], ns: a, typ: engram.Semantic, pinned: true},
		{id: ids[3], ns: b, typ: engram.Procedural},
	})
	e, f := string(uniqueID("stats-e")), string(uniqueID("stats-f"))
	for id, names := range map[engram.MemoryID][]string{ids[1]: {e, f}, ids[2]: {e, f}, ids[3]: {e}} {
		if err := s.LinkEntities(ctx, id, names); err != nil {
			t.Fatalf("LinkEntities: %v", err)
		}
	}
	for from, to := range map[engram.MemoryID]engram.MemoryID{ids[1]: ids[2], ids[2]: ids[1]} {
		if err := s.Link(ctx, from, []engram.Link{{To: to, Weight: 0.5}}); err != nil {
			t.Fatalf("Link: %v", err)
		}
	}
	if err := s.Link(ctx, ids[2], []engram.Link{{To: ids[3], Weight: 0.5}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	t.Cleanup(func() {
		// Entities are global and Delete leaves them behind.
		rawCount(t, `MATCH (e:Entity) WHERE e.name IN $n DETACH DELETE e RETURN 0 AS c`,
			map[string]any{"n": []string{e, f}})
	})
	return s, a, b, ids
}

func TestStoreCountsScopeEitherEndpoint(t *testing.T) {
	s, a, b, _ := statsFixture(t)
	g := func(ns engram.Namespace, typ engram.MemoryType, tot, live, sup, forg, pin int) engram.CountGroup {
		return engram.CountGroup{Namespace: ns, Type: typ, Total: tot, Live: live, Superseded: sup, Forgotten: forg, Pinned: pin}
	}
	tests := []struct {
		name string
		ns   engram.Namespace
		want engram.StoreCounts
	}{
		{"a", a, engram.StoreCounts{
			Groups:    []engram.CountGroup{g(a, engram.Episodic, 2, 1, 1, 1, 0), g(a, engram.Semantic, 1, 1, 0, 0, 1)},
			LinkEdges: 2, EntityBridges: 3}},
		{"b", b, engram.StoreCounts{
			Groups:    []engram.CountGroup{g(b, engram.Procedural, 1, 1, 0, 0, 0)},
			LinkEdges: 1, EntityBridges: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Counts(context.Background(), tt.ns)
			if err != nil {
				t.Fatalf("Counts: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Counts = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStoreCountsFlagsOverlap(t *testing.T) {
	s, a, _, _ := statsFixture(t)
	got, err := s.Counts(context.Background(), a)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if len(got.Groups) == 0 {
		t.Fatal("no groups")
	}
	g := got.Groups[0]
	if g.Type != engram.Episodic || g.Total != 2 || g.Live != 1 || g.Superseded != 1 || g.Forgotten != 1 {
		t.Errorf("episodic group = %+v; one row both superseded and forgotten must count in both, Live=1", g)
	}
}

func TestStoreCountsSingleFlags(t *testing.T) {
	s := testStore(t)
	a := engram.Namespace(string(uniqueID("stats-flags")))
	putRows(t, s, []statsRow{
		{id: uniqueID("sf-sup"), ns: a, typ: engram.Episodic, sup: true},
		{id: uniqueID("sf-forg"), ns: a, typ: engram.Episodic, forg: true},
		{id: uniqueID("sf-pin"), ns: a, typ: engram.Episodic, pinned: true},
		{id: uniqueID("sf-plain"), ns: a, typ: engram.Episodic},
	})
	got, err := s.Counts(context.Background(), a)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	want := engram.CountGroup{Namespace: a, Type: engram.Episodic, Total: 4, Live: 2, Superseded: 1, Forgotten: 1, Pinned: 1}
	if len(got.Groups) != 1 || got.Groups[0] != want {
		t.Errorf("Groups = %+v, want [%+v]", got.Groups, want)
	}
}

func TestStoreCountsUnknownNamespace(t *testing.T) {
	s := testStore(t)
	got, err := s.Counts(context.Background(), engram.Namespace(string(uniqueID("stats-none"))))
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if len(got.Groups) != 0 || got.LinkEdges != 0 || got.EntityBridges != 0 {
		t.Errorf("Counts = %+v, want empty", got)
	}
}

func TestStoreCountsHostileNamespace(t *testing.T) {
	s := testStore(t)
	got, err := s.Counts(context.Background(), "x' OR 1=1 //")
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if len(got.Groups) != 0 || got.LinkEdges != 0 || got.EntityBridges != 0 {
		t.Errorf("Counts = %+v, want empty", got)
	}
}

func TestStoreLiveMemoriesOrderProjectionLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := engram.Namespace(string(uniqueID("stats-live")))
	other := engram.Namespace(string(uniqueID("stats-other")))
	p := string(uniqueID("lm"))
	// Dropped filters would change the first two results: the forgotten-only, other
	// namespace and superseded rows all sort ahead of the live ones.
	putRows(t, s, []statsRow{
		{id: engram.MemoryID(p + "-0-forg"), ns: a, typ: engram.Episodic, forg: true},
		{id: engram.MemoryID(p + "-1-other"), ns: other, typ: engram.Episodic},
		{id: engram.MemoryID(p + "-2-sup"), ns: a, typ: engram.Episodic, sup: true},
		{id: engram.MemoryID(p + "-4-live"), ns: a, typ: engram.Semantic, pinned: true},
		{id: engram.MemoryID(p + "-3-live"), ns: a, typ: engram.Episodic},
		{id: engram.MemoryID(p + "-5-live"), ns: a, typ: engram.Episodic},
	})
	got, err := s.LiveMemories(ctx, a, 2)
	if err != nil {
		t.Fatalf("LiveMemories: %v", err)
	}
	if len(got) != 2 || got[0].ID != engram.MemoryID(p+"-3-live") || got[1].ID != engram.MemoryID(p+"-4-live") {
		t.Fatalf("got %+v, want ids -3-live, -4-live", got)
	}
	m := got[1]
	if m.Content != "" || m.Source != "" || m.Embedding != nil {
		t.Errorf("projection leaked content/source/embedding: %+v", m)
	}
	if !m.Pinned || m.Importance != 0.5 || m.Stability != 2 || m.AccessCount != 3 || m.Type != engram.Semantic {
		t.Errorf("projection lost fields: %+v", m)
	}
}

func TestStoreStatsReadOnly(t *testing.T) {
	s, a, _, ids := statsFixture(t)
	ctx := context.Background()
	before, err := s.Get(ctx, ids[1])
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := s.Counts(ctx, a); err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if _, err := s.LiveMemories(ctx, a, 10); err != nil {
		t.Fatalf("LiveMemories: %v", err)
	}
	after, err := s.Get(ctx, ids[1])
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !after.LastAccessed.Equal(before.LastAccessed) || after.AccessCount != before.AccessCount || after.Stability != before.Stability {
		t.Errorf("stats mutated the row: before %+v after %+v", before, after)
	}
}

func testDriver(t *testing.T) neo4jdriver.DriverWithContext {
	t.Helper()
	d, err := neo4jdriver.NewDriverWithContext(
		envOr("NEO4J_TEST_URI", "neo4j://localhost:7687"), neo4jdriver.NoAuth())
	if err != nil {
		t.Skipf("neo4j unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	return d
}

func TestStoreStatsRoutingRejectsWrite(t *testing.T) {
	d := testDriver(t)
	_, err := neo4jdriver.ExecuteQuery(context.Background(), d,
		`CREATE (n:StatsRoutingProbe) RETURN n`, nil,
		neo4jdriver.EagerResultTransformer, neo4jdriver.ExecuteQueryWithReadersRouting())
	if err == nil || !strings.Contains(err.Error(), "AccessMode") {
		t.Fatalf("write through readers routing: err = %v, want one containing AccessMode", err)
	}
}

func TestStorePreMigrationRowCountsLive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := engram.Namespace(string(uniqueID("stats-premig")))
	id := uniqueID("premig")
	putRows(t, s, []statsRow{{id: id, ns: a, typ: engram.Episodic}})
	d := testDriver(t)
	if _, err := neo4jdriver.ExecuteQuery(ctx, d,
		`MATCH (m:Memory {id:$id}) REMOVE m.superseded, m.pinned, m.forgotten`,
		map[string]any{"id": string(id)}, neo4jdriver.EagerResultTransformer); err != nil {
		t.Fatalf("remove flags: %v", err)
	}
	c, err := s.Counts(ctx, a)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if len(c.Groups) != 1 || c.Groups[0].Total != 1 || c.Groups[0].Live != 1 {
		t.Errorf("Groups = %+v, want one group Total=1 Live=1", c.Groups)
	}
	got, err := s.LiveMemories(ctx, a, 10)
	if err != nil {
		t.Fatalf("LiveMemories: %v", err)
	}
	if len(got) != 1 || got[0].Superseded || got[0].Pinned || got[0].Forgotten {
		t.Errorf("LiveMemories = %+v, want one row with all flags false", got)
	}
}
