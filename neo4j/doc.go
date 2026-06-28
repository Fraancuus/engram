// Package neo4j implements the engram.MemoryStore port over Neo4j's native vector
// index and property graph. All Cypher is parameterized — never built by
// concatenating user or agent input. This package depends on engram, never the
// reverse.
//
// Note: the official driver's package is also named neo4j
// (github.com/neo4j/neo4j-go-driver/v5/neo4j). Import it under an alias here, e.g.
//
//	import neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
//
// to avoid the name clash with this package.
package neo4j
