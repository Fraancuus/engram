package neo4j

import (
	"context"
	"fmt"
	"time"

	"github.com/Fraancuus/engram"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var _ engram.MemoryStore = (*Store)(nil)

// Store is a Neo4j-backed engram.MemoryStore. Construct it with New; the zero value is
// not usable. It is safe for concurrent use — the driver manages its own pool.
type Store struct {
	driver neo4jdriver.DriverWithContext
}

// New connects to Neo4j at uri (e.g. "neo4j://localhost:7687") and verifies
// connectivity. If password is empty it connects with no authentication — the local dev
// stack runs NEO4J_AUTH=none (auth is out of v1 scope) — otherwise it uses basic auth.
func New(ctx context.Context, uri, user, password string) (*Store, error) {
	auth := neo4jdriver.NoAuth()
	if password != "" {
		auth = neo4jdriver.BasicAuth(user, password, "")
	}
	driver, err := neo4jdriver.NewDriverWithContext(uri, auth)
	if err != nil {
		return nil, fmt.Errorf("neo4j driver %q: %w", uri, err)
	}
	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return nil, fmt.Errorf("neo4j connect %q: %w", uri, err)
	}
	return &Store{driver: driver}, nil
}

// Close releases the driver's connection pool.
func (s *Store) Close(ctx context.Context) error {
	if err := s.driver.Close(ctx); err != nil {
		return fmt.Errorf("neo4j close: %w", err)
	}
	return nil
}

// Put upserts m as a :Memory node. It MERGEs on id so re-running is idempotent, and
// writes the embedding through db.create.setNodeVectorProperty so it lands in the native
// vector index. Every value is passed as a query parameter — no Cypher is built from
// input.
func (s *Store) Put(ctx context.Context, m engram.Memory) error {
	const q = `
MERGE (m:Memory {id: $id})
SET m.namespace = $namespace,
    m.type = $type,
    m.content = $content,
    m.importance = $importance,
    m.stability = $stability,
    m.access_count = $access_count,
    m.created_at = $created_at,
    m.last_accessed = $last_accessed,
    m.source = $source,
    m.superseded = $superseded
WITH m
CALL db.create.setNodeVectorProperty(m, 'embedding', $embedding)`
	params := map[string]any{
		"id":           string(m.ID),
		"namespace":    string(m.Namespace),
		"type":         string(m.Type),
		"content":      m.Content,
		"importance":   m.Importance,
		"stability":    m.Stability,
		"access_count": m.AccessCount,
		// Normalize to UTC: Neo4j rejects Go's Local zone (its name "Local" is not a
		// valid IANA zone id). We persist instants, so UTC is the canonical form.
		"created_at":    m.CreatedAt.UTC(),
		"last_accessed": m.LastAccessed.UTC(),
		"source":        m.Source,
		"superseded":    m.Superseded,
		"embedding":     toFloat64(m.Embedding),
	}
	if _, err := neo4jdriver.ExecuteQuery(ctx, s.driver, q, params, neo4jdriver.EagerResultTransformer); err != nil {
		return fmt.Errorf("put memory %q: %w", m.ID, err)
	}
	return nil
}

// Get returns the memory with the given id, or engram.ErrNotFound if none exists.
func (s *Store) Get(ctx context.Context, id engram.MemoryID) (engram.Memory, error) {
	const q = `MATCH (m:Memory {id: $id}) RETURN m`
	res, err := neo4jdriver.ExecuteQuery(ctx, s.driver, q,
		map[string]any{"id": string(id)}, neo4jdriver.EagerResultTransformer)
	if err != nil {
		return engram.Memory{}, fmt.Errorf("get memory %q: %w", id, err)
	}
	if len(res.Records) == 0 {
		return engram.Memory{}, fmt.Errorf("get memory %q: %w", id, engram.ErrNotFound)
	}
	raw, ok := res.Records[0].Get("m")
	if !ok {
		return engram.Memory{}, fmt.Errorf("get memory %q: result missing node", id)
	}
	node, ok := raw.(neo4jdriver.Node)
	if !ok {
		return engram.Memory{}, fmt.Errorf("get memory %q: result is %T, want node", id, raw)
	}
	m, err := nodeToMemory(node)
	if err != nil {
		return engram.Memory{}, fmt.Errorf("get memory %q: %w", id, err)
	}
	return m, nil
}

// toFloat64 widens an embedding for storage; Neo4j list/vector properties are float64.
func toFloat64(v engram.Vector) []float64 {
	out := make([]float64, len(v))
	for i, f := range v {
		out[i] = float64(f)
	}
	return out
}

// nodeToMemory maps a :Memory node's properties back into the domain type, failing if a
// required property is missing or has an unexpected type rather than returning a
// half-populated memory.
func nodeToMemory(n neo4jdriver.Node) (engram.Memory, error) {
	p := n.Props
	var m engram.Memory
	var err error
	if m.ID, err = strProp[engram.MemoryID](p, "id"); err != nil {
		return engram.Memory{}, err
	}
	if m.Namespace, err = strProp[engram.Namespace](p, "namespace"); err != nil {
		return engram.Memory{}, err
	}
	if m.Type, err = strProp[engram.MemoryType](p, "type"); err != nil {
		return engram.Memory{}, err
	}
	if m.Content, err = strProp[string](p, "content"); err != nil {
		return engram.Memory{}, err
	}
	if m.Source, err = strProp[string](p, "source"); err != nil {
		return engram.Memory{}, err
	}
	if m.Importance, err = prop[float64](p, "importance"); err != nil {
		return engram.Memory{}, err
	}
	if m.Stability, err = prop[float64](p, "stability"); err != nil {
		return engram.Memory{}, err
	}
	if m.Superseded, err = prop[bool](p, "superseded"); err != nil {
		return engram.Memory{}, err
	}
	accessCount, err := prop[int64](p, "access_count")
	if err != nil {
		return engram.Memory{}, err
	}
	m.AccessCount = int(accessCount)
	if m.CreatedAt, err = prop[time.Time](p, "created_at"); err != nil {
		return engram.Memory{}, err
	}
	if m.LastAccessed, err = prop[time.Time](p, "last_accessed"); err != nil {
		return engram.Memory{}, err
	}
	if m.Embedding, err = vecProp(p, "embedding"); err != nil {
		return engram.Memory{}, err
	}
	return m, nil
}

// prop extracts a property whose stored Go type is exactly T (float64, bool, int64,
// time.Time as the driver returns them).
func prop[T any](p map[string]any, key string) (T, error) {
	var zero T
	v, ok := p[key]
	if !ok {
		return zero, fmt.Errorf("missing property %q", key)
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("property %q: want %T, got %T", key, zero, v)
	}
	return t, nil
}

// strProp extracts a string property and converts it to a string-kind type T
// (engram.MemoryID, Namespace, MemoryType, or plain string).
func strProp[T ~string](p map[string]any, key string) (T, error) {
	var zero T
	v, ok := p[key]
	if !ok {
		return zero, fmt.Errorf("missing property %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return zero, fmt.Errorf("property %q: want string, got %T", key, v)
	}
	return T(s), nil
}

// vecProp narrows a stored embedding list back to engram.Vector.
func vecProp(p map[string]any, key string) (engram.Vector, error) {
	v, ok := p[key]
	if !ok {
		return nil, fmt.Errorf("missing property %q", key)
	}
	switch xs := v.(type) {
	case []any:
		out := make(engram.Vector, len(xs))
		for i, e := range xs {
			f, ok := e.(float64)
			if !ok {
				return nil, fmt.Errorf("property %q[%d]: want float64, got %T", key, i, e)
			}
			out[i] = float32(f)
		}
		return out, nil
	case []float64:
		out := make(engram.Vector, len(xs))
		for i, f := range xs {
			out[i] = float32(f)
		}
		return out, nil
	case []float32:
		out := make(engram.Vector, len(xs))
		copy(out, xs)
		return out, nil
	default:
		return nil, fmt.Errorf("property %q: want list, got %T", key, v)
	}
}
