# Memory

Memory is what makes an Omnipus agent feel like it has a history. When a session ends, the agent doesn't just close a transcript — it writes a short recap, a structured retro, and (optionally) durable notes the next session will read on its first turn. The next time the user types, the agent recalls what happened yesterday, what went well, what didn't, and what the user told it to remember. Memory compounds: every session leaves a usable trace, not just a transcript.

This document describes the memory system as it ships today. The v0.3 "Rooms" redesign (`docs/internal/design/memory-redesign-2026-05.md`) replaces most of this with a four-tier model and a graph index. That work is tracked in [#156](https://github.com/elicify-ai/omnipus/issues/156) and is **not** described here as current behaviour.

## The three memory tools

All three tools are ordinary builtins registered on the central tool registry. They are scoped per-agent: one agent's `remember` writes go into that agent's workspace and only that agent's `recall_memory` can see them.

### `remember`

Persists a single fact, decision, reference, or lesson to `MEMORY.md`. Implementation: `pkg/tools/memory.go:117-154`.

**Args:** `content` (string, required, ≤ 4096 runes) and `category` (string, required, one of `key_decision | reference | lesson_learned`).

**What lands on disk:** the entry is appended to `<agent_workspace>/memory/MEMORY.md` as a structured block:

```
<!-- next -->

<!-- ts=2026-05-21T14:30:00.000Z cat=key_decision -->
<the content>
```

The leading `<!-- next -->` separator is only written when the file is non-empty; the first entry omits it. Writes go through `MemoryStore.AppendLongTerm` (`pkg/agent/memory.go:148-209`) which acquires an advisory `flock`, opens with `O_APPEND`, calls `f.Sync()`, and rejects content containing `<!--` (to prevent header forgery) or NUL bytes (silently stripped).

**Audit:** every call emits a `memory.remember` audit entry with the content's SHA-256 (never the raw content), byte count, category, and outcome (`ok` / `error_invalid` / `error_io`).

**Rate limits** (`pkg/tools/memory_rate_limit.go`): a sliding-window limiter gates writes per-agent and per-caller, both windows 1 minute, defaults **60 writes/min per agent** and **600 writes/min per caller** (`PerAgentLimit` / `PerCallerLimit` in `MemoryRateLimitConfig`, defaults applied at construction: `pkg/tools/memory_rate_limit.go:118-135`). Caller identity is `channel:chat_id` (e.g. `telegram:123456789`, `rest:user-x`); empty identities share a single `<anonymous>` bucket. A rejection emits a `memory.rate_limited` audit entry with `Decision=deny`, `Scope=agent|caller`, `retry_after_seconds`, and the caller key, then returns an `IsError: true` result with a parseable `error_kind="rate_limited"` suffix.

### `recall_memory`

Reads back from durable memory. Implementation: `pkg/tools/memory.go:262-322`. Backed by the narrower `MemorySearcher` interface — read tools have no path to write capability even via type coercion.

**Args:** `query` (string, required, literal substring; **no regex**) and `limit` (number, default 20, max 50).

**Scope:** searches three sources from the agent's own workspace and concatenates them newest-first: long-term entries from `memory/MEMORY.md` (parsed by `ReadLongTermEntries`); `memory/sessions/LAST_SESSION.md` (as one synthetic entry, category `last_session`, timestamp = file mtime); and retros from the last 30 days under `memory/sessions/<YYYY-MM-DD>/` (category `retro`).

**Match rule:** case-insensitive substring on either the entry content or the category name. Duplicates with the same timestamp are dropped.

**Result shape:** plain text, one entry per block, separated by `\n\n---\n\n`. Each entry is formatted `[<ISO8601> | <category>]\n<content>`. Empty results return the literal string `"no matching entries"`.

There is no audit on the read side — it is intentionally clean (FR-014).

### `retrospective`

Explicit retro from inside a turn — used when the agent wants to commit a retro before session-end auto-recap fires. Implementation: `pkg/tools/memory.go:384-426`.

**Args:** `went_well` (array of strings, required) and `needs_improvement` (array of strings, required). At least one of the two must be non-empty.

**What lands on disk:** the retro is appended to `memory/sessions/<YYYY-MM-DD>/<sessionID>_retro.md` via `MemoryStore.AppendRetro` (`pkg/agent/memory.go:485-536`). The `Trigger` field is hardcoded to `joined` for explicit tool calls (vs. `explicit` / `lazy` / `idle` / `bootstrap` for the auto-recap paths). `Recap` is left empty — only auto-recap fills that.

**Session ID fallback:** if the call happens outside a session context, the tool synthesizes `session-<unix_milli>` so the retro still lands somewhere.

**Rate limits:** shares the same per-agent / per-caller buckets as `remember` — an agent can't trivially work around the limit by alternating verbs.

**Audit:** `memory.retrospective` entries record `went_well_count` / `needs_improve_count` and outcome; rejections emit `memory.rate_limited` exactly as `remember` does.

The auto-recap that fires at session close (next section) writes the **same retro format** but via the agent loop, not the `retrospective` tool. The two paths converge at `MemoryStore.AppendRetro`.

## What happens at session close (auto-recap)

`AgentLoop.CloseSession(sessionID, trigger)` is the entry point. Triggers are `explicit` (the SPA's "End session" button), `lazy` (next-turn check after idle threshold), `idle` (idle-ticker fired), and `bootstrap` (post-restart sweep). Implementation: `pkg/agent/session_end.go:30-46`.

The flow below applies when `Agents.Defaults.AutoRecapEnabled` is true.

First, `claimedCloseSessions` (a `sync.Map`) is checked for idempotency — only the first caller for a given session ID proceeds. Duplicates emit `skipped_already_claimed` and return (`session_end.go:36-41`). A goroutine is then spawned with a top-level `recover()` so a panic in any subsystem (provider, JSON parse, file I/O) can't kill the gateway (`session_end.go:51-61`).

The owning agent is resolved via `AgentForSession`, which reads session meta and prefers `ActiveAgentID` (v2 multi-agent) over the legacy `AgentID`. If the agent is gone, a heuristic fallback retro is written and processing stops.

The session transcript is read from `sharedSessionStore.ReadTranscript(sessionID)`. User turns are filtered (FR-028): empty messages, `[SubTurn Result]`-prefixed lines, and the literal interrupt-hint string are dropped. Tool calls are counted in passing for the fallback retro. The filtered transcript is then truncated to ~2000 tokens (~8000 runes) keeping the **tail** of the conversation. When truncation fires, a `[history truncated for summarisation]\n\n` marker is prepended (`session_end.go:109-117`).

The recap model is resolved by preferring `Agents.Defaults.Routing.LightModel` and falling back to the agent's primary model. The light model is configured by the operator; on a typical setup it's something cheap like `anthropic/claude-3.5-haiku`. The LLM call uses cost-guard options (`session_end.go:136-148`):

```go
opts := map[string]any{
    "max_tokens":        250,
    "extended_thinking": false,
    "extra_body": map[string]any{
        "reasoning": map[string]any{"exclude": true},
    },
}
```

Context timeout: 60 s. The recap prompt asks for exactly `{"recap": "...", "went_well": [...], "needs_improvement": [...], "worth_remembering": [...]}`.

On success, `memory.WriteLastSession(parsed.Recap)` writes `memory/sessions/LAST_SESSION.md` (atomically via `fileutil.WriteFileAtomic`), and `memory.AppendRetro(sessionID, retro)` writes the day-bucketed retro with `Trigger = RecapTrigger(trigger)` and `Fallback = false`. The audit entry is `memory.auto_recap` with `outcome=success`.

On JSON parse failure or LLM error, `writeHeuristicFallbackRetroWithCount` builds a deterministic recap (`"Session <id> ended. Turns: N. Tool calls: M. Fallback reason: <reason>."`) and writes both `LAST_SESSION.md` and a fallback retro with `Fallback=true` and the `FallbackReason` set (`session_end.go:275-325`). Reasons include `json_parse_error`, `llm_timeout`, `llm_rate_limit`, `llm_unauthorized`, `llm_error`, `agent_deleted`, `no_session_store`, `no_memory_store`, `transcript_read_error: <err>`.

`BootstrapRecapPass` (`session_end.go:412-540`) runs on gateway start. It walks the gateway-wide sessions directory, skips anything younger than 30 minutes or already represented by a `<sessionID>_retro.md` in the owning agent's workspace (`agentSessionHasRetro`, `session_end.go:546-572`), and enqueues `CloseSession(id, "bootstrap")` for the rest. It is throttled to `GetBootstrapRecapMaxPerMinute` starts per minute (the only bound — there is no total cap; the former `bootstrap_recap_daily_budget_usd`, which measured a hardcoded `1e-5 USD/recap` guess rather than real spend, was removed). Recap + bootstrap recap are seeded ON on a fresh install; existing configs keep their stored value.

The `worth_remembering` array returned by the LLM is parsed but **not** auto-written to `MEMORY.md` today — only `recap`, `went_well`, and `needs_improvement` reach disk. Promoting `worth_remembering` to long-term memory is part of the v0.3 Dreamcatcher pass.

## On-disk layout

Per-agent, rooted at `<OMNIPUS_HOME>/agents/<agent_id>/`:

```
agents/<agent_id>/
├── memory/
│   ├── MEMORY.md                                  # long-term, append-only, <!-- next -->-separated
│   ├── <YYYYMM>/<YYYYMMDD>.md                     # daily notes (defined, currently unused — see note)
│   ├── daily/                                     # created by datamodel.InitAgentWorkspace, currently unused
│   └── sessions/
│       ├── LAST_SESSION.md                        # most recent recap, overwritten each session-end
│       └── <YYYY-MM-DD>/
│           └── <sessionID>_retro.md               # one file per session-day, structured blocks
├── sessions/                                      # day-partitioned JSONL transcripts (separate subsystem)
└── skills/
```

Permissions: `memory/` and subdirectories are created `0o700`; files inside are `0o600` (`memory.go:171, 461, 493` and `WriteFileAtomic` defaults).

**Note on `daily/` and `memory/<YYYYMM>/`.** `datamodel.InitAgentWorkspace` creates `memory/daily/` (`pkg/datamodel/init.go:155-167`), and `MemoryStore` defines `AppendToday` / `ReadToday` / `GetRecentDailyNotes` against `memory/<YYYYMM>/<YYYYMMDD>.md` (`pkg/agent/memory.go:121-126, 740-800`). Neither path has a live caller in `pkg/` or `cmd/` today — daily notes are scaffolding that nothing writes. Treat the directory as reserved.

## Sample retro file

`memory/sessions/2026-05-21/session_01HXYZ_retro.md`, exactly as written by `AppendRetro` (`pkg/agent/memory.go:506-516`):

```
<!-- ts=2026-05-21T14:30:00.000Z trigger=explicit fallback=false -->
## Session recap
The user added a new memory tool, fixed two flaky tests, and shipped a release note. Discussion focused on per-agent vs per-caller rate-limit semantics.
### Went well
- Caught the rate-limit edge case where both buckets exhaust simultaneously
- Tests pass on first run after the refactor
### Needs improvement
- Forgot to run `make verify-contracts` before pushing — caught in CI
- Initial prompt was too vague; needed three clarification turns
<!-- next -->
```

A fallback retro (LLM call failed) is identical except the header reads `fallback=true` and the body contains a single line under `## Session recap`:

```
<!-- ts=2026-05-21T14:30:00.000Z trigger=bootstrap fallback=true -->
## Session recap
Session session_01HXYZ ended. Turns: 12. Tool calls: 7. Fallback reason: json_parse_error.
### Went well
### Needs improvement
<!-- next -->
```

Multiple retro blocks per session are possible — `AppendRetro` opens with `O_APPEND` under `flock`, so a manual `retrospective` call followed by the auto-recap at close produces two blocks separated by `<!-- next -->` in the same file.

## Sample `LAST_SESSION.md`

`memory/sessions/LAST_SESSION.md` is plain text — exactly the `recap` field from the LLM JSON (or the heuristic fallback string), written atomically each time:

```
The user added a new memory tool, fixed two flaky tests, and shipped a release note. Discussion focused on per-agent vs per-caller rate-limit semantics.
```

It's overwritten — there is only ever one. `GetMemoryContext` injects this file at the top of the next session's prompt under a `## Last Session` header (`memory.go:805-837`), which is what gives the agent continuity on first turn.

## Concurrency and safety

### Sharded mutex pool

`JSONLStore` (the per-session transcript backend) keeps 64 mutexes in a fixed array and maps each session key to a shard via FNV-1a hash (`pkg/memory/jsonl.go:21-77`). Memory usage is O(1) regardless of total session count — important for a long-running daemon. Each shard mutex covers all reads and writes on its bucket of session files, which serializes appends, history reads, summary updates, and truncation against each other.

### Atomic writes

Every replace-the-whole-file write (`MEMORY.md` snapshots, `LAST_SESSION.md`, JSONL rewrites, meta.json) goes through `fileutil.WriteFileAtomic` — write to a temp file in the same directory, `fsync`, `rename` over the target. A crash mid-write leaves either the old or the new file intact, never a torn one. JSONL appends use `O_APPEND` with an explicit `f.Sync()` before close (`jsonl.go:233-256`) so an appended message is on disk before the call returns.

### Advisory flock on Linux/macOS

`fileutil.WithFlock` (`pkg/fileutil/flock_unix.go:35-58`) opens the target with `O_RDWR|O_CREATE`, takes an exclusive `unix.LOCK_EX` flock, runs the callback, and joins any unlock/close errors with the callback's. `AppendLongTerm` and `AppendRetro` both run inside `WithFlock` — the flock is defense-in-depth against a second process appending to the same file from outside the gateway (e.g. a sidecar tool, an editor).

### Windows

`WithFlock` on Windows is a pass-through that emits a one-time `slog.Warn` and calls the function directly (`pkg/fileutil/flock_windows.go:34-40`). The reason is that an open Windows handle on a file blocks `WriteFileAtomic` from renaming the temp over it — graceful degradation per hard constraint 4. Concurrency safety on Windows relies on the single-writer goroutine pattern in the store layer rather than OS-level locks.

## What's not implemented yet

These are all explicitly future work — don't rely on them, don't promise them in marketing copy.

| Capability | Status | Pointer |
|---|---|---|
| Cross-agent shared memory | Not built. Every memory store is rooted at `<agent_id>/memory/` — agents cannot see each other's memories. | v0.3 Rooms redesign |
| Semantic search / embeddings | Not built. `recall_memory` is case-insensitive literal substring; no tokenization, no BM25, no vectors. | v0.3 plans `bleve` (BM25, MoreLikeThis) — no embedding model |
| Dreamcatcher consolidation pass | Not built. `worth_remembering` from auto-recap is parsed but discarded — no promotion to `MEMORY.md`. | v0.3 design doc §D5 |
| Maps of Content (MOCs) | Not built. No auto-maintained index files; no graph traversal. | v0.3 design doc §D12 |
| Wikilinks / graph edges | Not built. `MEMORY.md` blocks are flat; no `[[id]]` resolution, no `.index/edges.jsonl`. | v0.3 design doc §D9–D11 |
| Near-duplicate detection on `remember` | Not built. The same fact can be written 60 times per minute (per the rate limiter ceiling). | v0.3 design doc §D21 (MinHash) |
| Daily notes (`AppendToday`, `ReadToday`, `GetRecentDailyNotes`) | Code exists in `MemoryStore`, no live caller. | Reserved for a future surface |

The full v0.3 redesign — atomic markdown files per memory, three-tier `memories/learnings/sessions` storage, `bleve` index, MinHash dedupe, MOCs, no embedding model — is specified in `docs/internal/design/memory-redesign-2026-05.md` and tracked in [#156](https://github.com/elicify-ai/omnipus/issues/156). It is a greenfield replacement: D2 of that decision log says "drop MEMORY.md and the legacy `<!-- next -->` block format wholesale; no migration."

## Summary

Today's memory is single-agent and retro-driven. It works on every install with no config, no extra services, no embedding model — just files under `<agent_workspace>/memory/`, atomic writes, advisory flock on Unix, and a 64-shard mutex pool for the transcript subsystem. The headline behaviour is the auto-recap: every session that closes leaves a `LAST_SESSION.md` and a structured retro the next session reads on its first turn. Volitional memory (`remember`, `retrospective`) is rate-limited at 60/min per agent and 600/min per caller, audited with content hashes only, and capped at 4096 runes per entry. The architectural redesign for v0.3 is locked but not yet shipping — track [#156](https://github.com/elicify-ai/omnipus/issues/156) for the rollout.
