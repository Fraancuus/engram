# Real-agent transcript

Claude Code, headless, choosing its own tool calls from plain-language prompts that name no tool. Each process below is a separate `claude -p` run, and each started its own `engramd` over stdio, against the real stack with the schema applied. Only assistant text, tool calls, tool results and the result status are kept; `e2e-transcript.jsonl` holds the same events.

## Process 1

**Prompt:** Please remember for later, in the memory namespace demo/m6-e2e, that our staging database is rotated every Tuesday at 03:00 UTC.

**Tool call:** `mcp__engram__remember`

```json
{
  "content": "The staging database is rotated every Tuesday at 03:00 UTC.",
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
  "memory_id": "f0224a8bce0d4e808aaa3009f3a43146"
}
```

**Agent:** I've saved it to the `demo/m6-e2e` namespace: the staging database is rotated every Tuesday at 03:00 UTC. The memory ID is `f0224a8bce0d4e808aaa3009f3a43146`. It was a new entry, not a duplicate of an existing one.

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

**Tool result:**

```json
{
  "results": [
    {
      "content": "The staging database is rotated every Tuesday at 03:00 UTC.",
      "id": "f0224a8bce0d4e808aaa3009f3a43146",
      "namespace": "demo/m6-e2e",
      "provenance": {
        "access_count": 0,
        "created_at": "2026-11-04T22:04:19.7475089Z",
        "last_accessed": "2026-11-04T22:04:19.7475089Z",
        "retrieved_via": "vector",
        "source": ""
      },
      "score": 1.067399168877289,
      "type": "semantic"
    }
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

**Namespace stats for `demo/m6-e2e`:**
- **Total memories:** 1. It is semantic, and there are no episodic or procedural ones.
- **Live:** 1. None are forgotten, superseded, or pinned.
- **Freshness:** The memory is fully fresh. It sits in the top retrievability bucket (0.9–1.0), and none are below the recall threshold. It was created today (2026-11-04, 22:04 UTC).
- **Sweep:** The hourly sweep hasn't run yet, so nothing has been pruned.

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
      "content": "The staging database is rotated every Tuesday at 03:00 UTC.",
      "id": "f0224a8bce0d4e808aaa3009f3a43146",
      "namespace": "demo/m6-e2e",
      "provenance": {
        "access_count": 1,
        "created_at": "2026-11-04T22:04:19.7475089Z",
        "last_accessed": "2026-11-04T22:04:26.492399Z",
        "retrieved_via": "vector",
        "source": ""
      },
      "score": 1.0611764485036026,
      "type": "semantic"
    }
  ]
}
```

**Tool call:** `mcp__engram__forget`

```json
{
  "id": "f0224a8bce0d4e808aaa3009f3a43146",
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

**Agent:** Soft-forget worked. Now the permanent delete.

**Tool call:** `mcp__engram__forget`

```json
{
  "id": "f0224a8bce0d4e808aaa3009f3a43146",
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

**Agent:** I soft-forgot the memory, checked the stats, then hard-deleted it and checked the stats again. The memory said "The staging database is rotated every Tuesday at 03:00 UTC." Its id was `f0224a8b…3a43146`.

**After the soft forget (`demo/m6-e2e`):**
- The namespace held 1 memory (semantic), which was marked forgotten.
- Live memories: 0.
- Pinned: 0. Superseded: 0.

**After the hard delete:**
- Total memories: 0.
- Forgotten: 0. Live: 0.
- Store size: 0 memories, 0 link edges, 0 entity bridges.

The permanent delete can't be undone, so the memory can't be recovered.

*Process 3 ended: success, is_error false.*
