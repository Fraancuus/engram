# Real-agent transcript

Claude Code, headless, choosing its own tool calls from plain-language prompts that name no tool. Each process below is a separate `claude -p` run, and each started its own `engramd` over stdio, against the real stack with the schema applied. Only assistant text, tool calls, tool results and the result status are kept; `e2e-transcript.jsonl` holds the same events.

## Process 1

**Prompt:** Please remember for later, in the memory namespace demo/m6-e2e, that our staging database is rotated every Tuesday at 03:00 UTC.

**Tool call:** `mcp__engram__remember`

```json
{
  "content": "Our staging database is rotated every Tuesday at 03:00 UTC.",
  "type": "semantic",
  "namespace": "demo/m6-e2e",
  "entities": [
    "staging database"
  ]
}
```

**Tool result:**

```json
{
  "deduped": false,
  "memory_id": "08414a374bd08f5125bdc033f5739435"
}
```

**Agent:** I saved it to the `demo/m6-e2e` namespace: the staging database is rotated every Tuesday at 03:00 UTC. It's a new memory, not a duplicate of an existing one.

*Process 1 ended: success, is_error false.*

## Process 2

**Prompt:** Looking only in the memory namespace demo/m6-e2e, when is the staging database rotated? Then tell me how many memories that namespace holds and how fresh they are.

**Tool call:** `mcp__engram__recall`

```json
{
  "query": "when is the staging database rotated",
  "namespaces": [
    "demo/m6-e2e"
  ]
}
```

**Tool call:** `mcp__engram__memory_stats`

```json
{
  "namespace": "demo/m6-e2e"
}
```

**Tool result:**

```json
{
  "results": [
    {
      "content": "Our staging database is rotated every Tuesday at 03:00 UTC.",
      "id": "08414a374bd08f5125bdc033f5739435",
      "namespace": "demo/m6-e2e",
      "provenance": {
        "access_count": 0,
        "created_at": "2026-11-04T21:27:34.4281968Z",
        "last_accessed": "2026-11-04T21:27:34.4281968Z",
        "retrieved_via": "vector",
        "source": ""
      },
      "score": 1.0793916251570073,
      "type": "semantic"
    }
  ]
}
```

**Tool result:**

```json
{
  "counts": {
    "by_namespace": {
      "demo/m6-e2e": {
        "episodic": 0,
        "procedural": 0,
        "semantic": 1
      }
    },
    "by_type": {
      "episodic": 0,
      "procedural": 0,
      "semantic": 1
    },
    "forgotten": 0,
    "live": 1,
    "pinned": 0,
    "superseded": 0,
    "total": 1
  },
  "retrievability": {
    "below_recall_threshold": 0,
    "buckets": [
      {
        "count": 0,
        "max": 0.1,
        "min": 0
      },
      {
        "count": 0,
        "max": 0.2,
        "min": 0.1
      },
      {
        "count": 0,
        "max": 0.3,
        "min": 0.2
      },
      {
        "count": 0,
        "max": 0.4,
        "min": 0.3
      },
      {
        "count": 0,
        "max": 0.5,
        "min": 0.4
      },
      {
        "count": 0,
        "max": 0.6,
        "min": 0.5
      },
      {
        "count": 0,
        "max": 0.7,
        "min": 0.6
      },
      {
        "count": 0,
        "max": 0.8,
        "min": 0.7
      },
      {
        "count": 0,
        "max": 0.9,
        "min": 0.8
      },
      {
        "count": 1,
        "max": 1,
        "min": 0.9
      }
    ],
    "decay": "type-aware",
    "truncated": false
  },
  "store_size": {
    "entity_bridges": 0,
    "link_edges": 0,
    "memories": 1
  },
  "sweep": {
    "last_pruned": 0,
    "last_run_at": null,
    "total_pruned": 0
  }
}
```

**Agent:** **Staging database rotation:** every Tuesday at 03:00 UTC. This comes from a single semantic memory in `demo/m6-e2e`.

**Namespace `demo/m6-e2e`:**
- **Count:** 1 memory in total. It is semantic and live. There are 0 episodic, 0 procedural, 0 forgotten, 0 superseded and 0 pinned.
- **Freshness:** The memory was created on 2026-11-04 at 21:27 UTC. Its retrievability is in the top bucket (0.9–1.0), and none are below the recall threshold.
- **Other details:** The memory has no links or entity bridges. The decay sweep hasn't run yet, so nothing has been pruned.

*Process 2 ended: success, is_error false.*

## Process 3

**Prompt:** In the memory namespace demo/m6-e2e, forget the memory about the staging database rotation, but keep it recoverable. Show me the namespace's stats afterwards. Then delete it permanently and show the stats once more.

**Tool call:** `mcp__engram__recall`

```json
{
  "query": "staging database rotation",
  "namespaces": [
    "demo/m6-e2e"
  ]
}
```

**Tool result:**

```json
{
  "results": [
    {
      "content": "Our staging database is rotated every Tuesday at 03:00 UTC.",
      "id": "08414a374bd08f5125bdc033f5739435",
      "namespace": "demo/m6-e2e",
      "provenance": {
        "access_count": 1,
        "created_at": "2026-11-04T21:27:34.4281968Z",
        "last_accessed": "2026-11-04T21:27:39.8115785Z",
        "retrieved_via": "vector",
        "source": ""
      },
      "score": 1.0726396137282483,
      "type": "semantic"
    }
  ]
}
```

**Tool call:** `mcp__engram__forget`

```json
{
  "id": "08414a374bd08f5125bdc033f5739435",
  "mode": "soft"
}
```

**Tool result:**

```json
{
  "ok": true
}
```

**Tool call:** `mcp__engram__memory_stats`

```json
{
  "namespace": "demo/m6-e2e"
}
```

**Tool result:**

```json
{
  "counts": {
    "by_namespace": {
      "demo/m6-e2e": {
        "episodic": 0,
        "procedural": 0,
        "semantic": 1
      }
    },
    "by_type": {
      "episodic": 0,
      "procedural": 0,
      "semantic": 1
    },
    "forgotten": 1,
    "live": 0,
    "pinned": 0,
    "superseded": 0,
    "total": 1
  },
  "retrievability": {
    "below_recall_threshold": 0,
    "buckets": [
      {
        "count": 0,
        "max": 0.1,
        "min": 0
      },
      {
        "count": 0,
        "max": 0.2,
        "min": 0.1
      },
      {
        "count": 0,
        "max": 0.3,
        "min": 0.2
      },
      {
        "count": 0,
        "max": 0.4,
        "min": 0.3
      },
      {
        "count": 0,
        "max": 0.5,
        "min": 0.4
      },
      {
        "count": 0,
        "max": 0.6,
        "min": 0.5
      },
      {
        "count": 0,
        "max": 0.7,
        "min": 0.6
      },
      {
        "count": 0,
        "max": 0.8,
        "min": 0.7
      },
      {
        "count": 0,
        "max": 0.9,
        "min": 0.8
      },
      {
        "count": 0,
        "max": 1,
        "min": 0.9
      }
    ],
    "decay": "type-aware",
    "truncated": false
  },
  "store_size": {
    "entity_bridges": 0,
    "link_edges": 0,
    "memories": 1
  },
  "sweep": {
    "last_pruned": 0,
    "last_run_at": null,
    "total_pruned": 0
  }
}
```

**Tool call:** `mcp__engram__forget`

```json
{
  "id": "08414a374bd08f5125bdc033f5739435",
  "mode": "hard"
}
```

**Tool result:**

```json
{
  "ok": true
}
```

**Tool call:** `mcp__engram__memory_stats`

```json
{
  "namespace": "demo/m6-e2e"
}
```

**Tool result:**

```json
{
  "counts": {
    "by_namespace": {},
    "by_type": {
      "episodic": 0,
      "procedural": 0,
      "semantic": 0
    },
    "forgotten": 0,
    "live": 0,
    "pinned": 0,
    "superseded": 0,
    "total": 0
  },
  "retrievability": {
    "below_recall_threshold": 0,
    "buckets": [
      {
        "count": 0,
        "max": 0.1,
        "min": 0
      },
      {
        "count": 0,
        "max": 0.2,
        "min": 0.1
      },
      {
        "count": 0,
        "max": 0.3,
        "min": 0.2
      },
      {
        "count": 0,
        "max": 0.4,
        "min": 0.3
      },
      {
        "count": 0,
        "max": 0.5,
        "min": 0.4
      },
      {
        "count": 0,
        "max": 0.6,
        "min": 0.5
      },
      {
        "count": 0,
        "max": 0.7,
        "min": 0.6
      },
      {
        "count": 0,
        "max": 0.8,
        "min": 0.7
      },
      {
        "count": 0,
        "max": 0.9,
        "min": 0.8
      },
      {
        "count": 0,
        "max": 1,
        "min": 0.9
      }
    ],
    "decay": "type-aware",
    "truncated": false
  },
  "store_size": {
    "entity_bridges": 0,
    "link_edges": 0,
    "memories": 0
  },
  "sweep": {
    "last_pruned": 0,
    "last_run_at": null,
    "total_pruned": 0
  }
}
```

**Agent:** I've finished both steps. The memory was "Our staging database is rotated every Tuesday at 03:00 UTC." (id `08414a37…`).

**1. After the soft-forget (recoverable):**
- The namespace held 1 memory, a semantic one.
- Its state was forgotten: 1 forgotten, 0 live, 0 pinned.

**2. After the hard delete (permanent):**
- The namespace has 0 memories. The store has 0 memories, 0 link edges and 0 entity bridges.
- `demo/m6-e2e` no longer appears in the per-namespace counts.

The memory can't be recovered now.

*Process 3 ended: success, is_error false.*
