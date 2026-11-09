## MODIFIED Requirements

### Requirement: MemStore satisfies the consumer interfaces
`mock.MemStore` MUST satisfy `mcp.ServerStore`, which covers `mcp.Store`, the forget store method
set (`SetForgotten`, `Pin`, `Delete`, `Supersede`) and the stats store method set (`Counts`,
`LiveMemories`), and MUST satisfy `sweep.Pruner`, be safe for concurrent use, and pass `go test -race`.

#### Scenario: Compile-time conformance
- **WHEN** the package compiles
- **THEN** `var _ mcp.ServerStore = (*mock.MemStore)(nil)` and `var _ sweep.Pruner = (*mock.MemStore)(nil)` hold

#### Scenario: Concurrent recall and sweep
- **WHEN** `Search` and `Delete` run concurrently from two goroutines under the race detector
- **THEN** no race is reported

#### Scenario: Concurrent stats and delete
- **WHEN** `Counts`, `LiveMemories` and `Delete` run concurrently from separate goroutines under the race detector
- **THEN** no race is reported

## ADDED Requirements

### Requirement: Stats mirror the Cypher
`MemStore.Counts(ctx, ns)` MUST return one `engram.CountGroup` per (namespace, type) present among
memories in `ns`, or among all memories when `ns` is empty, sorted by namespace then type, with `Total` counting every row, `Live`
counting rows neither superseded nor forgotten, and `Superseded`, `Forgotten` and `Pinned` counting
their flags, which overlap. `LinkEdges` MUST count distinct undirected link edges with either
endpoint in `ns`, and `EntityBridges` MUST count distinct unordered memory pairs sharing at least
one entity with either endpoint in `ns`. `MemStore.LiveMemories(ctx, ns, limit)` MUST return up to
`limit` memories in `ns` that are neither superseded nor forgotten, ordered by id ascending, with
`Embedding` nil and `Content` and `Source` empty. Neither method MAY change any stored value. The
`Len`, `EdgeCount` and `BridgeCount` counters the eval report reads MUST stay as they are.

#### Scenario: Flags overlap
- **WHEN** one episodic memory in ns a is both superseded and forgotten and one is neither
- **THEN** the group for (a, episodic) has `Total` 2, `Live` 1, `Superseded` 1 and `Forgotten` 1

#### Scenario: Scope counts either endpoint
- **WHEN** m1 in ns a and m2 in ns b share one link edge and one entity
- **THEN** `Counts` for a, for b and for all namespaces each report `LinkEdges` 1 and `EntityBridges` 1

#### Scenario: Unknown namespace
- **WHEN** `Counts` runs for a namespace with no memories
- **THEN** it returns no groups, `LinkEdges` 0 and `EntityBridges` 0, and no error

#### Scenario: Live rows order, projection and limit
- **WHEN** live memories with ids m3, m1 and m2 exist, one superseded memory m0 exists, and `LiveMemories` runs with limit 2
- **THEN** it returns m1 then m2, each with nil `Embedding` and empty `Content`

#### Scenario: Read-only at a fixed virtual time
- **WHEN** at virtual time T `Counts` and `LiveMemories` run over a memory last accessed at T minus one day
- **THEN** a `Snapshot` of that memory is unchanged, including `LastAccessed` and `AccessCount`
