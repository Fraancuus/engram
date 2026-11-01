## ADDED Requirements

### Requirement: Baseline file
`eval/baseline.json` MUST record `k`, `seed`, `goarch`, the floor `Params`, and arm C's
`precision_at_k` and `ndcg_at_k` rounded to 4 decimals. `--update-baseline` MUST run the
floor dataset with the given seed and rewrite the file; it MUST refuse to run on a
`GOARCH` other than `amd64`.

#### Scenario: Update writes rounded values
- **WHEN** `go run ./cmd/eval --update-baseline -seed 42` runs on amd64
- **THEN** `eval/baseline.json` contains `"seed": 42`, `"goarch": "amd64"`, and metric values with at most 4 decimals

#### Scenario: Refuse non-amd64 baseline
- **WHEN** `--update-baseline` runs with `GOARCH=arm64`
- **THEN** the command exits non-zero with a message naming the restriction and writes nothing

### Requirement: CI regression check
`--ci` MUST load the baseline, run the floor dataset with the baseline's seed, and exit 1
when arm C's precision@k (rounded to 4 decimals) is below `baseline.precision_at_k - 0.02`,
or when `Params.Validate` fails, or when any arm returns an error. It MUST exit 0
otherwise, print the table in both cases, and MUST NOT compare arms to each other.

#### Scenario: Regression fails the build
- **WHEN** the baseline says 0.5500 and the run yields 0.5200
- **THEN** the exit code is 1 and the output names both numbers and the tolerance

#### Scenario: Within tolerance passes
- **WHEN** the baseline says 0.5500 and the run yields 0.5350
- **THEN** the exit code is 0

#### Scenario: Null thesis result does not fail
- **WHEN** arm B outscores arm C but C is within tolerance of its own baseline
- **THEN** the exit code is 0

#### Scenario: Missing baseline
- **WHEN** `--ci` runs and `eval/baseline.json` does not exist
- **THEN** the exit code is 1 and the message says to run `--update-baseline`

### Requirement: Deterministic CI run
A `--ci` run MUST produce identical metrics on repeated invocations on the same `GOARCH`.

#### Scenario: Repeat run
- **WHEN** `--ci` runs twice in a row
- **THEN** the printed arm C precision@k and nDCG@k are byte-identical
