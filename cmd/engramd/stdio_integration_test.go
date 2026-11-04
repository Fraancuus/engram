//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/inference"
	eneo4j "github.com/Fraancuus/engram/neo4j"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// stdioSession starts one engramd process and connects an MCP client to it over stdio.
type stdioSession struct {
	*mcpsdk.ClientSession
	stderr *bytes.Buffer
}

func startEngramd(ctx context.Context, t *testing.T, bin string, env []string) stdioSession {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = env
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "stdio-smoke", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to engramd: %v\nengramd stderr:\n%s", err, stderr)
	}
	t.Cleanup(func() { _ = sess.Close() }) // a failed run must not leave engramd holding the exe open
	return stdioSession{sess, stderr}
}

// call invokes a tool and returns its structured output as the JSON a real client sees.
func (s stdioSession) call(ctx context.Context, t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := s.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("%s %v: err=%v result=%+v\nengramd stderr:\n%s", name, args, err, res, s.stderr)
	}
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: structured content is %T, want object", name, res.StructuredContent)
	}
	return m
}

func (s stdioSession) close(t *testing.T) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Errorf("close engramd session: %v\nengramd stderr:\n%s", err, s.stderr)
	}
}

// path digs into nested JSON objects.
func path(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %T at %q is not an object", keys, cur, k)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("path %v: missing key %q in %v", keys, k, obj)
		}
	}
	return cur
}

func hitIDs(t *testing.T, recall map[string]any) []string {
	t.Helper()
	var ids []string
	for _, h := range path(t, recall, "results").([]any) {
		ids = append(ids, h.(map[string]any)["id"].(string))
	}
	return ids
}

// TestStdioAllTools builds the real engramd and drives all four tools over stdio, across
// two processes, in a unique namespace. It is the only test that speaks the wire to the binary.
func TestStdioAllTools(t *testing.T) {
	neo4jURI := envOr("NEO4J_TEST_URI", "neo4j://localhost:7687")
	neo4jUser := envOr("NEO4J_TEST_USER", "neo4j")
	neo4jPass := os.Getenv("NEO4J_TEST_PASSWORD")
	teiURL := envOr("TEI_TEST_URL", "http://localhost:8080")
	rerankURL := envOr("TEI_RERANK_TEST_URL", "http://localhost:8081")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	store, err := eneo4j.New(ctx, neo4jURI, neo4jUser, neo4jPass)
	if err != nil {
		t.Skipf("neo4j unavailable (%v); run: docker compose up -d --wait", err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	if _, err := inference.New(teiURL).Embed(ctx, "reachability"); err != nil {
		t.Skipf("tei unavailable (%v); run: docker compose up -d --wait", err)
	}

	bin := filepath.Join(t.TempDir(), "engramd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { //nolint:gosec // bin is a t.TempDir path
		t.Fatalf("go build engramd: %v\n%s", err, out)
	}
	env := append(os.Environ(),
		"NEO4J_URI="+neo4jURI, "NEO4J_USER="+neo4jUser, "NEO4J_PASSWORD="+neo4jPass,
		"TEI_URL="+teiURL, "TEI_RERANK_URL="+rerankURL,
		"ENGRAM_DECAY=type-aware",
	)

	ns := fmt.Sprintf("stdio-smoke-%d", time.Now().UnixNano())
	var id string
	t.Cleanup(func() {
		if id != "" { // no-op if the test already hard-forgot it
			_ = store.Delete(context.Background(), engram.MemoryID(id))
		}
	})
	const content = "The staging database is rotated every Tuesday at 03:00 UTC."
	const query = "when is the staging database rotated"

	// Process A: list the tools, remember, exit.
	a := startEngramd(ctx, t, bin, env)
	tools, err := a.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v\nengramd stderr:\n%s", err, a.stderr)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"forget", "memory_stats", "recall", "remember"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	rem := a.call(ctx, t, "remember", map[string]any{"content": content, "type": "semantic", "namespace": ns})
	id, _ = path(t, rem, "memory_id").(string)
	if id == "" {
		t.Fatalf("remember returned no memory_id: %v", rem)
	}
	a.close(t)

	// Process B: recall, stats, soft forget, stats, unscoped and unknown stats, hard forget.
	b := startEngramd(ctx, t, bin, env)
	recall := func() []string {
		return hitIDs(t, b.call(ctx, t, "recall", map[string]any{"query": query, "namespaces": []string{ns}}))
	}
	if got := recall(); !slices.Contains(got, id) {
		t.Fatalf("recall ids = %v, want %q", got, id)
	}

	stats := b.call(ctx, t, "memory_stats", map[string]any{"namespace": ns})
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"counts.live", path(t, stats, "counts", "live"), 1.0},
		{"counts.by_type.semantic", path(t, stats, "counts", "by_type", "semantic"), 1.0},
		{"retrievability.truncated", path(t, stats, "retrievability", "truncated"), false},
		{"sweep.last_run_at", path(t, stats, "sweep", "last_run_at"), nil},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("stats %s = %v, want %v", c.name, c.got, c.want)
		}
	}
	sum := 0.0
	for _, bk := range path(t, stats, "retrievability", "buckets").([]any) {
		sum += bk.(map[string]any)["count"].(float64)
	}
	if sum != 1 {
		t.Errorf("histogram buckets sum = %v, want 1", sum)
	}

	b.call(ctx, t, "forget", map[string]any{"id": id, "mode": "soft"})
	if got := recall(); slices.Contains(got, id) {
		t.Errorf("recall after soft forget still returns %q", id)
	}
	stats = b.call(ctx, t, "memory_stats", map[string]any{"namespace": ns})
	if got := path(t, stats, "counts", "forgotten"); got != 1.0 {
		t.Errorf("stats counts.forgotten = %v, want 1", got)
	}
	if got := path(t, stats, "counts", "live"); got != 0.0 {
		t.Errorf("stats counts.live = %v, want 0", got)
	}

	// An unknown namespace is the nil-map case: by_namespace must still be an object.
	unknown := b.call(ctx, t, "memory_stats", map[string]any{"namespace": ns + "-unknown"})
	if got, ok := path(t, unknown, "counts", "by_namespace").(map[string]any); !ok || len(got) != 0 {
		t.Errorf("unknown namespace by_namespace = %#v, want empty object", got)
	}
	b.call(ctx, t, "memory_stats", map[string]any{}) // unscoped must succeed on any dev database

	b.call(ctx, t, "forget", map[string]any{"id": id, "mode": "hard"})
	stats = b.call(ctx, t, "memory_stats", map[string]any{"namespace": ns})
	if got := path(t, stats, "store_size", "memories"); got != 0.0 {
		t.Errorf("stats store_size.memories after hard forget = %v, want 0", got)
	}
	b.close(t)
}
