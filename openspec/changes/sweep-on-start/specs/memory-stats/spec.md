## MODIFIED Requirements

### Requirement: Sweep stats
`Sweeper.tick` MUST record each completed pass in memory without changing what `SweepOnce` returns:
`LastRunAt` is the `now` the pass ran at, `LastPruned` its count, `TotalPruned` the sum since process
start. A failed or panicking pass MUST leave them unchanged. `Stats()` MUST be safe to call while a
tick runs. The tool MUST report them as `sweep.last_run_at`, `sweep.last_pruned` and
`sweep.total_pruned`, with `last_run_at` null before the first completed pass, and they MUST NOT
depend on `namespace`. `WithSweepStats` MUST supply them to `Tools`; without it the tool MUST report
the empty values. The tool description MUST say that sweep stats cover only this server process's
passes and that null and 0 say nothing about the store, and MUST NOT name a duration.

#### Scenario: Before the first tick
- **WHEN** no pass has completed
- **THEN** `last_run_at` is JSON null and both counts are 0

#### Scenario: Accumulates at fixed virtual time
- **WHEN** at virtual time T a pass prunes 2, then at T plus one hour a pass prunes 0
- **THEN** `LastRunAt` is T plus one hour, `LastPruned` is 0 and `TotalPruned` is 2

#### Scenario: Prune boundary
- **WHEN** a candidate's R is exactly the hard floor in one pass and just below it in another
- **THEN** the first pass records `LastPruned` 0 with `LastRunAt` set, and the second records 1

#### Scenario: Failed pass leaves stats alone
- **WHEN** a completed pass is followed by one where `PruneCandidates` fails
- **THEN** the stats still describe the completed pass

#### Scenario: Concurrent read
- **WHEN** `Stats` and `tick` run concurrently under the race detector
- **THEN** no race is reported

#### Scenario: Real binary, a pass may or may not have completed
- **WHEN** `memory_stats` is called on a running `engramd` at any moment after start
- **THEN** `sweep.last_run_at` is JSON null or an RFC 3339 string, both counts are numbers zero or more, and if `last_run_at` is null both counts are 0

#### Scenario: Description says whose passes the stats cover
- **WHEN** a client lists the tools
- **THEN** the `memory_stats` description says the sweep stats cover this server process's passes, and does not contain "hourly"

### Requirement: The eval does not move
This capability MUST NOT change `Recall`, `Remember`, the count or error `SweepOnce` returns, or any
eval constant, and MUST NOT change `eval/baseline.json`. A/B/C and the CI gate are unaffected.

#### Scenario: Gate and baseline
- **WHEN** verify runs `go run ./cmd/eval --ci` and `git diff origin/main -- eval/baseline.json`
- **THEN** the gate passes and the diff is empty
