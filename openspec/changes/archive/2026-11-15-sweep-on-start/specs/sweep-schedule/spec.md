## ADDED Requirements

### Requirement: First pass after the first delay
`Sweeper.Run(ctx, firstDelay)` MUST run its first pass when `firstDelay` has elapsed since `Run`
began, and MUST NOT wait for the interval before it. After the first pass it MUST run one pass per
interval. A `firstDelay` of zero MUST run the first pass immediately. `Run` MUST return when `ctx` is
cancelled, whether it is waiting for the first delay or for a tick. `cmd/engramd` MUST pass a first
delay of one minute with an interval of one hour, and MUST name both in its startup log line.

#### Scenario: Delay boundary at fixed virtual time
- **WHEN** `Run` starts at virtual schedule time S with a first delay of one minute and an interval of one hour
- **THEN** no pass has run at S plus one minute minus one nanosecond, exactly one has run at S plus one minute, and exactly two at S plus one minute plus one hour

#### Scenario: Zero first delay
- **WHEN** `Run` starts with a first delay of zero
- **THEN** one pass completes before any schedule time elapses

#### Scenario: Cancelled during the first delay
- **WHEN** `ctx` is cancelled 30 seconds into a one-minute first delay
- **THEN** `Run` returns, no pass has run and `Stats()` is the zero value

#### Scenario: Cancelled between passes
- **WHEN** `ctx` is cancelled after the first pass and before the next tick
- **THEN** `Run` returns and exactly one pass has run

#### Scenario: engramd names its schedule
- **WHEN** `engramd` starts
- **THEN** its startup log line names the first delay of one minute and the interval of one hour

#### Scenario: A session longer than the first delay
- **WHEN** `engramd` has served for more than one minute, its first pass completed without error, and `memory_stats` is called
- **THEN** `sweep.last_run_at` is an RFC 3339 timestamp

### Requirement: Pass time comes from the injected clock
Each pass that `Run` starts MUST call `SweepOnce` with `now` read from the injected `engram.Clock`.
`sweep/sweep.go` MUST NOT call `time.Now`, and the time at which a timer fired MUST NOT be used as
`now`.

#### Scenario: Schedule time and decay time differ, at fixed virtual time
- **WHEN** the injected clock reads T throughout, and the first pass fires one minute of schedule time after `Run` starts
- **THEN** `Stats().LastRunAt` is T

#### Scenario: Floor boundary on the first pass
- **WHEN** the first pass runs at virtual time T on a candidate whose R is exactly the hard floor, and in another run on a candidate whose R is just below it
- **THEN** the first run records `LastPruned` 0 with `LastRunAt` T, and the second records `LastPruned` 1

### Requirement: A row already deleted is not an error
When `Delete` returns `engram.ErrNotFound` for a candidate, `SweepOnce` MUST skip the row: it MUST
NOT count it as pruned, MUST NOT log it at error level, and MUST continue with the next candidate.
Any other delete error MUST still be logged at error level and skipped. No delete error, of either
kind, MUST be counted as pruned or returned from `SweepOnce`.

#### Scenario: Every candidate already gone, at fixed virtual time
- **WHEN** at virtual time T a pass has two candidates below the floor and `Delete` returns `ErrNotFound` for both
- **THEN** `SweepOnce` attempts both deletes, returns 0 and no error, logs no error-level record, and the pass is recorded with `LastRunAt` T and `LastPruned` 0

#### Scenario: Another delete error is still reported
- **WHEN** `Delete` returns an error that is not `ErrNotFound` for a candidate below the floor
- **THEN** `SweepOnce` returns 0 and no error, and one error-level record is logged

### Requirement: The eval does not move
This capability MUST NOT change `sweep.New`, the count or error `SweepOnce` returns, any eval
constant or `eval/baseline.json`. The eval MUST keep driving the sweep through `SweepOnce` with
virtual time and MUST NOT call `Run`.

#### Scenario: Eval sweeps on virtual time only
- **WHEN** an eval arm advances one virtual day
- **THEN** it calls `SweepOnce` with that day's virtual `now`, and no code under `eval/` or `cmd/eval/` calls `Run`

#### Scenario: Gate with zero drift
- **WHEN** verify runs `go run ./cmd/eval --ci` and `git diff origin/main -- eval/ cmd/eval/`
- **THEN** the gate passes, every A/B/C figure equals the baseline exactly and not merely within the 0.02 tolerance, and the diff is empty
