# Demo script

How to record Engram working end to end: a Claude Code agent stores a memory, finds it again in
a new process, reads the store's stats, then forgets it. Run every command from Git Bash.

## Prerequisites

Docker, Go 1.26+, and the `claude` CLI. The Go bin directory (`$(go env GOPATH)/bin`) must be on
`PATH`, so that `engramd` resolves.

## 1. Stack, schema and binary

From the repo root:

```bash
docker compose up -d --wait                                         # Neo4j + TEI embed + TEI rerank
docker compose exec -T neo4j cypher-shell < schema/001_init.cypher  # once per fresh volume
go install ./cmd/engramd                                            # puts engramd on PATH
```

`go install` copies the binary once. It goes stale until you run `go install ./cmd/engramd`
again, so rerun it after any Go change.

To wire Claude Code for everyday use, not for the recording:

```bash
claude mcp add --scope local engram -- engramd   # per person, tracks nothing in the repo
```

The repo has no `.mcp.json` on purpose. A tracked file would start Engram in every session here,
including ones with the stack down.

## 2. Start a clean session

Each prompt below runs in its own `claude` process. Start every one from a temp directory, so no
`CLAUDE.md` and no repo path enters the session:

```bash
cd "$(mktemp -d)"
claude --strict-mcp-config --setting-sources "" --tools "" \
  --mcp-config '{"mcpServers":{"engram":{"command":"engramd"}}}' \
  --allowedTools 'mcp__engram__*'
```

- `--tools ""` leaves only the Engram tools.
- `--strict-mcp-config` keeps every other MCP server out of the session.
- `--setting-sources ""` keeps your user hooks and settings out of the recording.
- `--allowedTools 'mcp__engram__*'` lets the agent call Engram without a permission prompt, including on a hard delete.

The config holds no local path, needs no file and leaves no state. If Claude Code asks whether to
trust the temp folder, accept: it is a fresh directory each time.

## 3. The three prompts

Type `/exit` between them and start a new `claude` with the command above. A new process is the
point: the viewer sees a memory survive a restart of both the client and `engramd`.

**Prompt 1.**

> Please remember for later, in the memory namespace demo/recording, that our staging database is rotated every Tuesday at 03:00 UTC.

The viewer should see a `remember` call with that namespace, and a result holding a `memory_id`
and `deduped: false`.

**Prompt 2.**

> Looking only in the memory namespace demo/recording, when is the staging database rotated? Then tell me how many memories that namespace holds and how fresh they are.

The viewer should see a `recall` call that returns the memory from prompt 1, and a `memory_stats`
call. In the stats, `counts.by_namespace` holds one namespace, `counts.live` counts the memory,
and the retrievability buckets put it near the top.

**Prompt 3.**

> In the memory namespace demo/recording, forget the memory about the staging database rotation, but keep it recoverable. Show me the namespace's stats afterwards. Then delete it permanently and show the stats once more.

The viewer should see a `recall` call to find the id, then a `forget` call in `soft` mode, then `memory_stats`: `counts.forgotten`
counts the memory and `counts.live` no longer does, but `counts.total` still does. Then a `forget`
call in `hard` mode, then `memory_stats` again with `counts.total` back at zero.

## What to watch for

**Keep `memory_stats` scoped.** Called with no `namespace`, it lists every namespace in the
database, including any left by earlier runs and tests. The prompts above name the namespace, so
the output stays scoped. If an unscoped call appears on screen, stop the take and run it again.

**The `sweep` section reads empty.** `sweep.last_run_at` is `null` and `last_pruned` and
`total_pruned` are 0. The stats are kept in memory from process start and are not persisted. A
stdio client starts `engramd` for its own session, and the first sweep runs an hour after start,
so a short session never reaches one. They are also store-wide, not per namespace.

**Scoped `store_size` is the namespace's size.** If the agent reports a scoped call's `store_size`
as the whole store's size, stop the take and run it again.

## Troubleshooting

**`calling "initialize": EOF` or `Connection closed`.** `engramd` exits at start when Neo4j is
down. A Go SDK client then reports `calling "initialize": EOF`, and Claude Code marks the server
failed with `Connection closed`. From the repo root, run `docker compose up -d --wait`, then start
the session again.

**`embedding failed`.** With only the TEI sidecars down, `engramd` starts, but `remember` and
`recall` report `embedding failed`. The same fix applies: from the repo root, run
`docker compose up -d --wait`.

**Behaviour that does not match the code.** The installed binary is stale. From the repo root, run
`go install ./cmd/engramd` again.

## Cleanup

From the repo root:

To stop the stack and keep the data volumes:

```bash
docker compose down
```

To also delete all data, every memory and both TEI model caches, run the next command instead.
It is irreversible, and the next `up` downloads the models again:

```bash
docker compose down -v
```

Prompt 3 already deletes the demo memory. If a take stops early, delete what is left with
`forget` in `hard` mode, using the id from the `remember` result.
