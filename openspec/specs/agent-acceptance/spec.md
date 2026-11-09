# agent-acceptance Specification

## Purpose
How Claude Code starts `engramd` over stdio, and the proof that a real agent can use Engram end to end: the stdio smoke test, the kept transcript and the demo script.

## Requirements

### Requirement: Claude Code can start engramd over stdio
The repo MUST document how Claude Code starts `engramd` over stdio without a tracked `.mcp.json`:
`go install ./cmd/engramd` followed by `claude mcp add --scope local engram -- engramd`. The docs
MUST state that the installed binary goes stale until `go install` is rerun, and what a client shows with the
stack down, and how to fix it: with Neo4j down `engramd` exits at start, so a Go SDK client reports
`calling "initialize": EOF` and Claude Code marks the server failed with `Connection closed`; with
only TEI down the server starts and `remember` and `recall` report `embedding failed`.

#### Scenario: No tracked client config
- **WHEN** the repo is checked out
- **THEN** there is no `.mcp.json`, and a session in the repo starts no Engram server unless its user added one

#### Scenario: Stack down is documented
- **WHEN** a reader sees `calling "initialize": EOF`, `Connection closed`, or `embedding failed`
- **THEN** `docs/demo.md` tells them to run `docker compose up -d --wait`

### Requirement: Stdio smoke test
An `integration`-tagged test in `cmd/engramd` MUST build `engramd` and drive all four tools through
an MCP client over `mcpsdk.CommandTransport`, against the real stack, in a unique namespace,
across two processes. It MUST pass `NEO4J_TEST_URI`, `NEO4J_TEST_USER`, `NEO4J_TEST_PASSWORD`,
`TEI_TEST_URL` and `TEI_RERANK_TEST_URL` to the child as `NEO4J_URI`, `NEO4J_USER`,
`NEO4J_PASSWORD`, `TEI_URL` and `TEI_RERANK_URL`, with the defaults the other integration tests
use. It MUST give the binary an `.exe` name on Windows. It MUST skip, naming `neo4j unavailable` or
`tei unavailable`, when a service is unreachable, rather than fail with `calling "initialize": EOF`.

#### Scenario: All four tools across two processes
- **WHEN** one process lists the tools and calls `remember`, and exits, and a second process calls `recall`, `memory_stats` and `forget`
- **THEN** the tool list is exactly the four names, `recall` returns the remembered id, and `memory_stats` reports it as live

#### Scenario: Forget is visible in stats
- **WHEN** the second process soft-forgets the memory and calls `recall` and `memory_stats` for the namespace
- **THEN** `recall` no longer returns it, `forgotten` is 1 and `live` is 0

#### Scenario: Unknown namespace over the wire
- **WHEN** the client calls `memory_stats` with a namespace that holds no memories
- **THEN** the call succeeds and `by_namespace` is an empty object, so the output schema's `object` type holds

#### Scenario: Test environment cannot reach the dev database
- **WHEN** `NEO4J_TEST_URI` points at another database
- **THEN** the child `engramd` connects to that database, even if the shell also sets `NEO4J_URI`

### Requirement: Real agent transcript
The change directory MUST hold a transcript of one real end-to-end run: Claude Code choosing the
tool calls itself from plain-language prompts that name no tool, against the real stack with the
schema applied, in the dedicated namespace `demo/m6-e2e`, using `--strict-mcp-config`. The
transcript MUST show, with inputs and results, `remember` in one process, `recall` of that memory
from a second process, `forget`, and `memory_stats`. It MUST NOT contain the init event, a session
id, a local path, or anything naming the user's accounts or other MCP servers.

#### Scenario: Two processes
- **WHEN** a reader inspects the transcript
- **THEN** `remember` appears under one process and `recall` of the same memory id under a later one

#### Scenario: Sanitized by whitelist
- **WHEN** the transcript is produced from the raw event stream
- **THEN** only assistant text, tool calls, tool results and the result status survive, and a negative search for the local user name, GitHub login, email, drive or home paths, `session_id` and `uuid` finds nothing

#### Scenario: Only the dedicated namespace
- **WHEN** a reader searches the transcript for namespaces
- **THEN** only `demo/m6-e2e` appears; a run where the agent listed other namespaces is discarded and repeated, not edited

### Requirement: Demo script
`docs/demo.md` MUST give the exact commands and agent prompts to record the demo from Git Bash,
state what the viewer should see at each step, and warn that unscoped `memory_stats` lists every
namespace in the database. It MUST state that sweep stats read empty in a short session and why.
The README MUST have a section linking it. The commands MUST have been run once from Git Bash, and
`verify.md` MUST disclose that the agent steps ran through `claude -p`, not the interactive UI.

#### Scenario: Reader follows the script
- **WHEN** Franco follows `docs/demo.md` from Git Bash with the stack down
- **THEN** every command is given in order, starting from `docker compose up -d --wait`

#### Scenario: Recording stays open
- **WHEN** the PR is opened without a recording
- **THEN** the PRD M6 status line says the demo recording remains open until Franco links it
