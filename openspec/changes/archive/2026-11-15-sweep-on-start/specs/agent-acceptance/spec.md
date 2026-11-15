## MODIFIED Requirements

### Requirement: Demo script
`docs/demo.md` MUST give the exact commands and agent prompts to record the demo from Git Bash,
state what the viewer should see at each step, and warn that unscoped `memory_stats` lists every
namespace in the database. It MUST state when sweep stats are populated: empty until the first pass,
one minute after `engramd` starts, kept in memory per process, and covering all namespaces.
The README MUST have a section linking it. The commands MUST have been run once from Git Bash, and
`verify.md` MUST disclose that the agent steps ran through `claude -p`, not the interactive UI.

#### Scenario: Reader follows the script
- **WHEN** Franco follows `docs/demo.md` from Git Bash with the stack down
- **THEN** every command is given in order, starting from `docker compose up -d --wait`

#### Scenario: Recording stays open
- **WHEN** the PR is opened without a recording
- **THEN** the PRD M6 status line says the demo recording remains open until Franco links it

#### Scenario: Reader sees a populated sweep block
- **WHEN** a demo session lasts longer than one minute and the viewer sees `sweep.last_run_at` set
- **THEN** `docs/demo.md` has told them to expect it, and to expect null in a shorter session
