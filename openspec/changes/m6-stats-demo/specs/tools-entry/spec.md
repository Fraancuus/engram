## RENAMED Requirements

- FROM: `### Requirement: Store argument covers forget`
- TO: `### Requirement: Store argument covers forget and stats`

## MODIFIED Requirements

### Requirement: Exported in-process tools
Package `mcp` MUST export `NewTools(embedder engram.Embedder, reranker engram.Reranker,
decay engram.DecayModel, store ServerStore, clock engram.Clock, opts ...Option) *Tools` and the
methods `Remember(ctx, RememberInput) (RememberOutput, error)`, `Recall(ctx, RecallInput)
(RecallOutput, error)`, `Forget(ctx, ForgetInput) (ForgetOutput, error)`, `MemoryStats(ctx,
MemoryStatsInput) (MemoryStatsOutput, error)`, with the input and output structs exported and
the JSON tags of the first three unchanged. `NewServer(t *Tools) *mcpsdk.Server` MUST register
the same four tools over the same methods, so stdio and in-process callers run identical code.

#### Scenario: Same behaviour both ways
- **WHEN** the existing handler unit tests are rewritten against `Tools`
- **THEN** they pass unchanged in assertions

#### Scenario: Server wraps Tools
- **WHEN** `NewServer(NewTools(...))` serves a `recall` call
- **THEN** the response equals `Tools.Recall` for the same input

#### Scenario: Four tools registered
- **WHEN** a client lists the tools of `NewServer(NewTools(...))`
- **THEN** it sees exactly `remember`, `recall`, `forget` and `memory_stats`

### Requirement: Store argument covers forget and stats
`NewTools` MUST accept one store value that satisfies the remember/recall store interface, the
forget method set and the stats method set (`Counts`, `LiveMemories`), which together are
`ServerStore`, and `*mock.MemStore` and `*neo4j.Store` MUST both satisfy it.

#### Scenario: Both stores wire
- **WHEN** the package compiles
- **THEN** `mcp.NewTools(e, r, d, &mock.MemStore{}, c)` and `mcp.NewTools(e, r, d, neo4jStore, c)` type-check
