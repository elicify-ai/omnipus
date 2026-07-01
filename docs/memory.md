# Memory

Memory is what makes an Omnipus agent feel like it has a history. When a session ends, the agent writes a short recap and a structured retrospective. On the next turn those are read back in, so the agent remembers what happened yesterday, what went well, what did not, and what the user told it to keep. Memory compounds: every session leaves a usable trace, not just a transcript.

This document describes the memory system as it ships today.

## The four memory tools

All four tools are builtins registered on each agent's `ToolRegistry`. Agents always operate inside a workspace, so memory is **shared with the workspace team by default** — a teammate agent can recall what you saved. Pass `room='private'` only for notes that are just for you.

### `remember`

Saves a durable fact, decision, reference, or lesson. Implementation: `pkg/tools/memory.go`.

**Args:** `content` (string, required, ≤ 4 096 runes), `category` (required: `key_decision` | `reference` | `lesson_learned`), `room` (optional: `shared` | `private`).

**Room default:** `shared` — the memory goes into the workspace team's shared room (`workspaces/<id>/.omnipus/memories/`) so every agent on the team can recall it. Pass `room='private'` to write to the agent's own private room (`agents/<id>/.omnipus/memories/`).

**What lands on disk:** a Markdown file with YAML-like frontmatter under `memories/<id>.md` inside the target room (one file per memory; bleve/scorch-indexed for BM25 recall). Each file carries: `id`, `title`, `type`, `tags`, `confidence`, `status`, `author`, `born_in`.

**Audit:** every call emits a `memory.remember` audit entry with the content's SHA-256 (never the raw content), byte count, category, and outcome.

**Rate limits** (`pkg/tools/memory_rate_limit.go`): sliding-window limiter, defaults 60 writes/min per agent and 600 writes/min per caller. A rejection emits `memory.rate_limited` with `error_kind="rate_limited"`.

### `recall_memory`

Searches durable cross-session memory — saved facts **and** past retrospectives. Implementation: `pkg/tools/memory.go`.

**Args:** `query` (string, required, literal substring), `limit` (default 20, max 50), `room` (optional: `private` | `shared` | `both`).

**Room default:** `both` — searches the workspace shared room and the agent's private room, deduplicating by ID. Pass `room='shared'` or `room='private'` to narrow the scope.

**How search works:** BM25 full-text search via a per-room bleve/scorch index (`pkg/memrooms/index/`), with a case-insensitive substring-scan fallback when the index is unavailable. Retrospectives are indexed alongside long-term memories and surface through the same query.

**Use this when:** the information comes from a previous conversation. For earlier turns of the current conversation, use `recall_conversation` instead.

### `recall_conversation`

Pages back through earlier turns of the **current** conversation that scrolled out of the live context window. Implementation: `pkg/agent/recall_conversation.go`.

**Args:** exactly one of `query` (BM25 keyword, returns ≤ 8 turns / ≤ 4 000 tokens), `turn_range` (e.g. `"5-10"`, ≤ 50 turns / ≤ 8 000 tokens), or `time` (`{from, to}` Unix seconds or RFC 3339).

**Not persisted:** reads only the current session's own archive (the sliding-window breadcrumb index). It does not cross session boundaries. To find information saved across different conversations, use `recall_memory`.

### `run_retrospective`

Records what went well and what to improve at the end of a productive session. Implementation: `pkg/tools/memory.go`.

**Args:** `went_well` (array of strings, required), `needs_improvement` (array of strings, required). At least one must be non-empty.

**When to call it:** after the user has reviewed the session summary, before signing off. Do not call it mid-session.

**What lands on disk:** a structured block appended to `retros/<YYYY-MM-DD>/<sessionID>_retro.md` in the agent's **private** room (retrospectives are agent-personal reflection, not shared facts). Retrospectives are returned by `recall_memory` — there is no separate `recall_retro` tool.

**Auto-recap:** the same retro format is written automatically at session close via `AgentLoop.CloseSession` (`pkg/agent/session_end.go`). The explicit `run_retrospective` tool fires the same `MemoryStore.AppendRetro` path.

## The two-room topology

Each workspace has two memory rooms:

| Room | Path | Visible to |
|---|---|---|
| Shared | `workspaces/<id>/.omnipus/memories/` | Every agent in the workspace |
| Private | `agents/<id>/.omnipus/memories/` | This agent only |

**Write default:** `remember` defaults to `shared` — the workspace team's collective memory.
**Read default:** `recall_memory` defaults to `both` — reads widely, returns results from whichever room has the best match.

Rule of thumb: shared for anything a teammate agent might need (project decisions, conventions, references); private for personal working notes and individual lessons that would be noise to the rest of the team.

## Recall routing at a glance

| You want to recall… | Tool | How |
|---|---|---|
| An earlier turn of the current chat that scrolled out of view | `recall_conversation` | `query:"…"` or `turn_range:"5-10"` or `time:{from,to}` |
| A past retrospective (went-well / needs-improvement) | `recall_memory` | `query:"…"` — retros are folded into recall_memory results |
| A saved fact/decision/reference/lesson | `recall_memory` | `query:"…"`, `room:'both'` (default) |

## What happens at session close (auto-recap)

`AgentLoop.CloseSession(sessionID, trigger)` is the entry point (`pkg/agent/session_end.go`). Triggers: `explicit` (SPA "End session"), `lazy` (next-turn check after idle threshold), `idle` (idle-ticker fired), `bootstrap` (post-restart sweep).

When `Agents.Defaults.AutoRecapEnabled` is true:

1. Idempotency check via `claimedCloseSessions` (a `sync.Map`).
2. Session transcript filtered (empty messages and sub-turn results dropped) and truncated to ~2 000 tokens keeping the tail.
3. LLM call (light model; 60 s timeout; max 250 tokens) requesting `{"recap", "went_well", "needs_improvement", "worth_remembering"}`.
4. On success: `memory.WriteLastSession(recap)` writes `last-session.md` (private room root), `memory.AppendRetro(sessionID, retro)` writes the day-bucketed retro (private room `retros/`).
5. On failure: `writeHeuristicFallbackRetro` writes a deterministic fallback retro with `fallback=true`.

`BootstrapRecapPass` runs on gateway start and enqueues `CloseSession` for any session that closed without a retro, throttled to `GetBootstrapRecapMaxPerMinute` starts/minute.

`worth_remembering` from the LLM is parsed but not auto-written to long-term memory today. Promoting it is the Dreamcatcher pass (v0.3).

## On-disk layout

```
workspaces/<ws_id>/
└── .omnipus/
    └── memories/
        ├── <uuid>.md          # one file per shared long-term memory
        └── .index/
            ├── scorch/        # bleve BM25 index
            ├── minhash.jsonl  # near-dup dedup links
            └── counters.jsonl # access frequency records

agents/<agent_id>/
└── .omnipus/
    ├── memories/
    │   ├── <uuid>.md          # one file per private long-term memory
    │   └── .index/            # bleve index, minhash, counters
    ├── retros/
    │   └── <YYYY-MM-DD>/
    │       └── <sessionID>_retro.md  # retrospective blocks
    └── last-session.md        # most recent recap (overwritten each session-end)
```

Permissions: directories are created `0o700`; files are `0o600`. Atomic writes via `fileutil.WriteFileAtomic`; retro appends via `flock` + `O_APPEND` + `fsync`.

## Near-duplicate detection

`MemoryStore.AppendLongTermToScope` runs a MinHash dedup check (`pkg/memrooms/minhash/`) after writing the `.md` file. If the new memory's MinHash signature exceeds the Jaccard threshold against any existing memory in the room, a `NearDupRecord` is appended to `.index/minhash.jsonl` (non-destructive — the `.md` file is kept). The sigCache is rebuilt from `.md` mtimes so externally written files are also considered.

## Concurrency and safety

- **Per-room bleve index** (`pkg/memrooms/index/`): lazily opened, protected by `indexMu` across both index writes and searches. `syncRoomToDiskLocked` detects mtime changes and rebuilds stale indexes.
- **Atomic writes:** long-term memory files, `last-session.md`, and JSONL rewrites use `fileutil.WriteFileAtomic` (temp + fsync + rename).
- **Advisory flock:** `AppendRetro` runs inside `fileutil.WithFlock` (Unix `LOCK_EX`). Windows falls back to a no-op flock with a one-time warn.
- **Shared room concurrency:** `MemoryStore.sharedRoom` is protected by a `sync.RWMutex`; `SetWorkspaceID` swaps the pointer atomically so per-turn workspace changes never race concurrent reads.
- **Session transcripts:** `JSONLStore` uses a 64-shard mutex pool keyed by FNV-1a hash (`pkg/memory/jsonl.go`) — O(1) memory regardless of session count.

## What is not implemented yet

| Capability | Status |
|---|---|
| Dreamcatcher: auto-promote `worth_remembering` to shared long-term memory | Not built; tracked for v0.3 |
| Maps of Content / graph edges / wikilinks | Not built; v0.3 |
| Semantic (embedding-based) search | Not built; no embedding model; v0.3 |
| Daily notes (`AppendToday`, `GetRecentDailyNotes`) | Code exists, no live caller; reserved |
