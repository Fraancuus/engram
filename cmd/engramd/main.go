// Command engramd is the Engram memory service. It wires the domain to its Neo4j,
// inference, and MCP adapters by hand (no DI container) in main and serves the MCP
// tools over stdio.
package main

import "log"

func main() {
	// TODO(M0): construct the neo4j MemoryStore, the inference Embedder/Reranker,
	// the DecayModel and a real Clock, wire them together here, and serve the MCP
	// server over stdio. Scaffold stub until M0 lands.
	log.Println("engramd: scaffold stub; not yet implemented")
}
