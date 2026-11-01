## ADDED Requirements

### Requirement: Exported in-process tools
Package `mcp` MUST export `NewTools(embedder engram.Embedder, reranker engram.Reranker,
decay engram.DecayModel, store Store, clock engram.Clock, opts ...Option) *Tools` and the
methods `Remember(ctx, RememberInput) (RememberOutput, error)`, `Recall(ctx, RecallInput)
(RecallOutput, error)`, `Forget(ctx, ForgetInput) (ForgetOutput, error)`, with the input and
output structs exported and their JSON tags unchanged. `NewServer(t *Tools) *mcpsdk.Server`
MUST register the same three tools over the same methods, so stdio and in-process callers
run identical code.

#### Scenario: Same behaviour both ways
- **WHEN** the existing handler unit tests are rewritten against `Tools`
- **THEN** they pass unchanged in assertions

#### Scenario: Server wraps Tools
- **WHEN** `NewServer(NewTools(...))` serves a `recall` call
- **THEN** the response equals `Tools.Recall` for the same input

### Requirement: Store argument covers forget
`NewTools` MUST accept one store value that satisfies both the remember/recall store
interface and the forget method set (the existing `serverStore` union), and `*mock.MemStore`
and `*neo4j.Store` MUST both satisfy it.

#### Scenario: Both stores wire
- **WHEN** the package compiles
- **THEN** `mcp.NewTools(e, r, d, &mock.MemStore{}, c)` and `mcp.NewTools(e, r, d, neo4jStore, c)` type-check

### Requirement: Id generator option
`WithIDGenerator(func() (engram.MemoryID, error)) Option` MUST replace the id source used
by `Remember`. The default MUST remain the existing `crypto/rand` generator.

#### Scenario: Sequential ids
- **WHEN** `NewTools(..., WithIDGenerator(seq))` where `seq` yields `m000001`, `m000002`, …
- **THEN** two `Remember` calls return `m000001` then `m000002`

#### Scenario: Default unchanged
- **WHEN** no option is passed
- **THEN** ids are 32 lowercase hex characters as today

### Requirement: Callers updated
`cmd/engramd/main.go` and every test constructing handlers MUST use `NewTools`/`NewServer`;
no unexported constructor path MAY remain for production wiring.

#### Scenario: Build and tests
- **WHEN** `go build ./... && go test -race ./...` run
- **THEN** both succeed with the old `NewServer(embedder, reranker, decay, store, clock)` signature removed
