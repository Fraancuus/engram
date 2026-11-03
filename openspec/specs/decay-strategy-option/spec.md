# decay-strategy-option Specification

## Purpose
Decay strategy selectable by name (engram.DecayByName) and through ENGRAM_DECAY in engramd, so uniform vs type-aware is a strategy swap at startup.

## Requirements

### Requirement: DecayByName
The root package MUST export `DecayByName(name string) (DecayModel, error)` mapping
`"type-aware"` → `TypeAwareDecay{}`, `"uniform"` → `UniformDecay{}`, `"none"` →
`NullDecay{}`, and returning an error that lists the valid names for anything else. It MUST
also export `DecayName(DecayModel) string` returning the inverse for those three types (used
by the eval report).

#### Scenario: Valid names
- **WHEN** `DecayByName("uniform")` is called
- **THEN** it returns `UniformDecay{}` and nil

#### Scenario: Invalid name
- **WHEN** `DecayByName("fast")` is called
- **THEN** it returns an error whose text contains `type-aware`, `uniform` and `none`

#### Scenario: Round trip
- **WHEN** `DecayName(TypeAwareDecay{})` is called
- **THEN** it returns `"type-aware"`

### Requirement: ENGRAM_DECAY in engramd
`engramd` MUST read `ENGRAM_DECAY`, default `type-aware` when unset or empty, resolve it
with `DecayByName`, and fail startup with a legible error on an unknown value. The chosen
name MUST appear in the startup log line. The same model MUST be passed to both
`mcp.NewTools` and `sweep.New`.

#### Scenario: Default
- **WHEN** `ENGRAM_DECAY` is unset
- **THEN** the service runs with `TypeAwareDecay` and logs `decay=type-aware`

#### Scenario: Unknown value
- **WHEN** `ENGRAM_DECAY=linear`
- **THEN** `run()` returns an error before connecting to Neo4j and the process exits non-zero

#### Scenario: Uniform selected
- **WHEN** `ENGRAM_DECAY=uniform`
- **THEN** recall and the sweep both use `UniformDecay`
