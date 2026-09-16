# Memory system redesign — 2026-05

## Status

Draft, decisions logged 2026-05-03. Greenfield design to replace the current monolithic `MEMORY.md` system. Sequenced after the sandbox redesign (`sandbox-redesign-2026-05.md`); this document reuses the two-room (private agent + shared project) workspace topology established there.

## Problem

The current memory system is patchwork:

- One monolithic `MEMORY.md` per agent at `<agent_workspace>/memory/MEMORY.md`. Append-only, `<!-- next -->`-separated blocks with `<!-- ts=… cat=… -->` headers. Implementation: `pkg/agent/memory.go:148-209`.
- Three fixed categories: `key_decision | reference | lesson_learned`.
- Search is case-insensitive substring across `MEMORY.md`, `LAST_SESSION.md`, and retrospectives from the last 30 days. Newest-first, hard-capped at 50. No regex, no tokenization, no ranking. Implementation: `pkg/agent/memory.go:350+`.
- No revision: corrections live next to errors. No relationships between entries. No deduplication. No cross-agent sharing. Single mutex on the whole file becomes a write bottleneck.
- `LAST_SESSION.md` and `retrospectives/` exist as parallel artifacts with no clean lineage to long-term memory.
- Triggers are unreliable: only `remember()` (volitional, often skipped) and session-end recap (mechanical) write durable memory. No event-driven path captures failures, decisions, or working patterns.

Replacement requirements (user, verbatim):

1. Self-improving and learning.
2. No embedding model.
3. Simple architecture.
4. Secure.
5. Nice-to-have: graph support like Obsidian.

## Decisions log

Captured 2026-05-03 over an iterative design conversation.

| # | Decision | Rationale |
|---|---|---|
| D1 | Atomic markdown files per memory, not a monolithic file | Removes write contention, enables Obsidian browsing, makes git diffs reviewable |
| D2 | Drop `MEMORY.md` and the legacy `<!-- next -->` block format wholesale; no migration | Fresh-build redesign — legacy storage code is removed; existing data in old format is ignored, not preserved |
| D3 | Three storage tiers: `memories`, `learnings`, `sessions` (firehose) | Maps to semantic / reflective / episodic memory; non-overlapping responsibilities |
| D4 | Keep `last-session.md` as a fourth artifact, auto-injected into next session's prompt | Continuity bridge; a single file is cheap to load, expensive to recompute |
| D5 | `learnings/` differentiates `auto_recap` vs `joint` source via frontmatter | User-attended retros are higher-signal than transcript-only auto-recaps |
| D6 | Three agent tools: `remember`, `recall`, `retrospective` | Continuity with current names; one verb per writable destination + one read verb |
| D7 | The boundary rule: only `remember()` writes memories; only `retrospective()` (or session-end recap) writes learnings; everything else is `sessions/` | Single deterministic entry point per tier; no classifier, no scoring threshold |
| D8 | No separate journal directory — session transcripts ARE the firehose | The journal as previously sketched was ~95% duplicate of session writes |
| D9 | Six fixed edge types in the graph: `links_to`, `tagged`, `authored_by`, `supersedes`, `born_in`, `cited_in` | Each has a deterministic creation rule; no inferred or similarity edges |
| D10 | Graph edges stored as JSONL in `.index/edges.jsonl` (one edge per line, append-only); in-memory map built at boot | Pure JSONL, multi-process safe via atomic `O_APPEND`; auditable history; rebuildable from `.md` source |
| D11 | Wikilinks `[[id]]` are the human-narrative edges; operational edges (`born_in`, `cited_in`) live in `edges.jsonl` only | Obsidian renders the narrative graph faithfully; ops edges stay machine-only and don't clutter the visual graph |
| D12 | MOCs (Maps of Content) auto-maintained per project to solve cold-start | New memories of a given type/tag get auto-linked from the corresponding MOC |
| D13 | User-imperative regex auto-extracts `remember()` calls without LLM in the loop | Same input → same memory; deterministic capture of explicit user instructions |
| D14 | Citation detection (id or title literal in agent text after a `recall` hit) drives access counters and confidence drift | Cheap, deterministic, conservative — biases toward under-counting, never over |
| D15 | Confidence and access counts written as append-only events to `.index/counters.jsonl`; in-memory state computed at boot, refreshed via periodic compaction | Pure JSONL, multi-process safe (atomic appends), auditable forever (until compaction); no row-locking needed |
| D16 | Full-text search via **bleve v2** (Apache 2.0, pure Go) — gives BM25 ranking, MoreLikeThis similarity, multi-field markdown indexing | Mature library (11k stars, used by Caddy/Couchbase/Sourcegraph); pure Go; no CGo; isolated derived index in `.index/bleve/`; replaces SQLite FTS5 entirely |
| D20 | **No embedding model.** Cross-vocabulary semantic similarity recovered through (a) LLM-driven query rephrase loop, (b) graph traversal via wikilinks/tags, (c) MOC navigation hubs | Determinism > semantic fuzziness for our use case; LLM rephrase is free in an agent system; if needed later, bleve v2.6+ already supports vector + RRF hybrid — upgrade is one schema change away |
| D21 | Near-duplicate detection on `remember()` uses MinHash signatures stored in `.index/minhash.jsonl` (algorithm: ekzhu/minhash-lsh; ~128 B/memory); fallback Jaccard via `adrg/strutil` for the 60%/50% conflict threshold | Pure Go, deterministic; cheap signature compute; sharper than substring-overlap heuristic |
| D22 | No SQLite anywhere in Omnipus's own data — explicitly forbidden by `CLAUDE.md` storage principle | Whatsmeow's SQLite is isolated to channel-state and unrelated to memory |
| D17 | Project memory is a peer "room" alongside private agent memory; same format, separate directory tree | Reuses the sandbox-redesign two-room model; one mental model, two scopes |
| D18 | Default scope of `remember`/`retrospective` follows session context (project session → project room; agent session → private room); explicit override available | Removes a per-call decision burden; correct default for the common case |
| D19 | `last-session.md` is per-agent only; project rooms have no last-session continuity | Sessions are conversational and per-agent; project rooms accumulate knowledge, not continuity |

## Storage tiers

Four destinations. Each has exactly one writer and exactly one purpose.

| Tier | Storage | Written by | Auto-injected into prompt? |
|---|---|---|---|
| Sessions (firehose) | `sessions/<id>/<date>.jsonl` | every conversation turn | only the active session's transcript |
| Last session summary | `last-session.md` (overwritten) | session-end recap pipeline (system) | **yes** — every new session loads it |
| Memories | `memories/<id>.md` | `remember()` tool | no — `recall()` only |
| Learnings | `learnings/<date>-<sid>-{auto\|joint}.md` | session-end recap (auto) OR `retrospective()` tool (joint) | no — `recall()` only |

Plus two hidden artifacts:

- `.system.jsonl` — housekeeping events (auto-archive, confidence drift, Dreamcatcher runs). Tiny.
- `.index/` — derived: `bleve/` (FTS+BM25+MoreLikeThis), `edges.jsonl` (graph), `counters.jsonl` (access/drift event log), `tags.json`, `minhash.jsonl` (near-dup signatures). All rebuildable from `.md` source.

## Layout — single room

```
.omnipus/
├── sessions/<id>/<date>.jsonl       # firehose (default 90d retention)
├── last-session.md                  # auto-injected continuity (overwritten each session-end)
├── memories/<id>.md                 # written by remember() — durable concepts
├── learnings/<date>-<sid>-auto.md   # session-end recap, source=auto_recap
├── learnings/<date>-<sid>-joint.md  # retrospective() call, source=joint
├── .system.jsonl                    # housekeeping (small)
├── .index/                          # derived, rebuildable
│   ├── bleve/                       # bleve v2 FTS index (BM25, MoreLikeThis, multi-field)
│   ├── edges.jsonl                  # graph edges, one JSON line per edge, append-only
│   ├── counters.jsonl               # access/drift event log, append-only
│   ├── tags.json                    # tag → memory_ids map, atomic rewrite
│   └── minhash.jsonl                # MinHash signatures for near-duplicate detection
└── .gitignore                       # ships ignoring .index/, .system.jsonl
```

Memory file shape:

```markdown
---
id: lndlk-vs-sccmp-7f3a
title: Landlock vs seccomp-bpf for path enforcement
created: 2026-05-03T10:14:22Z
updated: 2026-05-03T11:02:11Z
author: jim
type: decision         # decision | fact | reference | lesson | person | project | moc | note
tags: [security, kernel, sandbox]
confidence: 0.85       # denormalized cache; counters.jsonl event log is authoritative
status: active         # active | superseded | archived
supersedes: lndlk-only-2026-04
access_count: 7        # denormalized cache
last_accessed: 2026-05-03T10:14:22Z
born_in: sess-abc123   # provenance — session in which this was remembered
---

Supersedes: [[lndlk-only-2026-04]]

We chose Landlock over seccomp-bpf because path-based rules
match our threat model better. See [[hardened-exec-design]]
and [[pentest-2026-05]].
```

Note ids: `[a-z0-9-]{4,32}`, generated as `<slug>-<4-char-random>`. Globally unique by construction; titles are display-only and renameable.

## Project memory — the second dimension

Memory is scoped to a **room**. Two rooms exist:

- **Private** (per-agent): `<omnipus_home>/agents/<agent_id>/.omnipus/`
- **Project** (per-project, shared across agents): `<project_root>/.omnipus/`

Both use the same layout, same memory file format, same tools. The difference is who can read and write, and how the rooms compose at recall time.

### Layout — both rooms

```
<omnipus_home>/agents/jim/.omnipus/
├── sessions/...                  # private to Jim — conversations
├── last-session.md               # private to Jim — continuity
├── memories/                     # private to Jim — author=jim always
├── learnings/                    # private to Jim
├── .system.jsonl
└── .index/

<project_root>/.omnipus/          # checked into the project, optionally git-tracked
├── memories/                     # shared — author=<any agent who wrote it>
├── learnings/                    # shared
├── .system.jsonl
└── .index/
```

Project rooms have **no `sessions/`** and **no `last-session.md`** — those are inherently per-agent (D19). A project room accumulates knowledge across agents over time; continuity is a personal thing.

### Default scope follows session context (D18)

When the agent calls `remember(content)`:

- If the session is bound to a project context (via the sandbox/project-room mechanism) → default `scope=project`, writes to the project room.
- Otherwise → default `scope=private`, writes to the agent's room.
- Explicit `remember(content, scope=private)` or `scope=project` overrides.

Same default rule for `retrospective()` and the session-end auto-recap. Auto-recap learnings respect the room: if the session was project-scoped, the auto-recap learning lands in the project room with `author=<agent_id>` so contributions are attributable.

`recall(query)` defaults to searching **both rooms** when one is in project context. Filterable via `room=private|project|both`.

### Cross-room links

A memory in either room can wikilink to a memory in the other room. The indexer resolves `[[id]]` by looking up the id in:

1. The same room first (most common).
2. The other room as a fallback.
3. If found in the other room, the edge is recorded with a `cross_room` flag.

Cross-room links are visible in Obsidian if both rooms are opened as one vault, or rendered as broken links if only one is opened. The SPA's memory view shows them as linked regardless.

### Multi-agent writes to a project room

Two agents may try to update the same memory's frontmatter simultaneously (e.g., both citing it in different sessions causing parallel access-count bumps). Mitigations:

- **Counter writes go to `counters.jsonl` as append-only events, not frontmatter.** Each access bump or drift event is one line: `{ts, memory_id, op, by, amount?}`. Appends are atomic on POSIX for writes under `PIPE_BUF` (4 KB), which any single event line fits. In-memory aggregate state is computed at boot and updated on each append. Periodic sweep (weekly) compacts the log to `{snapshot}\n{recent tail}` and flushes denormalized values back to frontmatter for human readability.
- **New memory creation is always a new file** (atomic temp + rename). No id collision because the random suffix ensures uniqueness.
- **Frontmatter mutations** (status, supersedes, body edits) use per-file advisory `flock` plus an `updated_at` etag precondition on `update`. Mismatch → caller re-reads.
- **Author preserved**: the `author` field is set on creation and never mutated. Updates can be made by any agent in the project but they don't rewrite the author field; ownership is informational.

### Promotion: private → project

A private memory can be promoted to the project room via an operator action or an explicit `remember(content, scope=project, supersedes=<private-id>)` call. The new file has `author=<original_agent>`, `born_in=<original_session>`, and supersedes the private original. The private one's status flips to `superseded`. Promotion is opt-in; defaults are private.

### Git semantics

The project room directory is **inside the project's working tree**. Whether memories travel with the project repo is an operator choice:

- **Default**: `<project_root>/.omnipus/.gitignore` ignores `.index/` and `.system.jsonl` only. `memories/` and `learnings/` are committable.
- **Operator opt-out**: add `memories/` or `learnings/` (or all of `.omnipus/`) to the project's main `.gitignore`. Memory stays local.
- **`git diff` per concept**: because each memory is its own file, project memory diffs are reviewable line-by-line. Unlike a monolithic `MEMORY.md`, this is a real PR-able artifact.

### What multi-agent collaboration on a project actually looks like

Three agents (Jim, Mia, Ava) work on the same project. Each has their own private room; the project has one shared room.

- Jim's private session captures his conversation log; auto-recap writes Jim's `last-session.md` and a project-scoped learning (his auto-recap insight goes to the project room with `author=jim`).
- Mia opens the project, calls `recall("api auth approach")` — sees Jim's project memory + his project learning + her own private memories on the topic.
- Mia calls `remember("we settled on JWT in transit, OAuth at the boundary", scope=project)` — written to project room, `author=mia`.
- Jim's next session pulls in Mia's contribution via `recall` because both default to including the project room.
- Operator commits `<project_root>/.omnipus/memories/*.md` to the repo. Pushing the repo distributes the agent collective memory to other clones of the project.

This is the multi-agent collaboration mechanism: **shared project rooms, attributable authorship, git as the sync layer.** No central server, no live sync protocol, no merge daemon. Conflict resolution is git's existing merge model on per-file markdown diffs.

## Tool surface — three agent tools

```
remember(content, [supersedes=id], [scope=private|project])
  → appends a memory to memories/<id>.md
  → system fills id, type=note (Dreamcatcher re-classifies later),
    tags from #hashtags in content, links from [[wikilinks]] in content,
    confidence=0.7 (or 0.9 if invoked via user-imperative auto-extraction),
    author from context, scope from session context (override permitted)

retrospective(went_well, needs_improvement, [narrative], [scope=...])
  → appends a learning to learnings/<date>-<sid>-joint.md, source=joint
  → triggered when user invites collaborative reflection
  → ranking +0.1 because user attention was on it

recall(query, [scope=memories|learnings|sessions|all], [room=private|project|both],
       [hops=0], [limit=20])
  → returns ranked hits with full body (≤4KB) or summary+id (>4KB)
  → graph traversal up to N hops via in-memory edges map (loaded from edges.jsonl)
  → searches default to room=both when in project context
```

Three tools, four destinations (memories, learnings, last-session.md, sessions). The agent never writes to `last-session.md` or `sessions/` directly — those are system-managed.

## Triggers — what causes memory entries

```
remember() called directly                    → memories/<id>.md
user-imperative regex match in user turn      → system synthesizes remember()  → memories/<id>.md
Dreamcatcher proposal accepted (operator/agent)→ remember() invoked              → memories/<id>.md

retrospective() called by agent (user-invited)→ learnings/<date>-<sid>-joint.md  (source=joint)
session_close (auto)                          → recap LLM call →
                                                ├── last-session.md (overwritten, prose)
                                                └── learnings/<date>-<sid>-auto.md (source=auto_recap)

every conversation turn                       → sessions/<id>/<date>.jsonl  (existing infra)
new_session_start                             → last-session.md prepended to system prompt
recall() + agent cites a returned id/title    → access_count++, confidence += 0.05
                                                  (counters.jsonl append; no .md file rewrite)
remember(supersedes=X)                        → X.status=superseded, X.confidence -= 0.20
```

The system **never writes to `memories/` or `learnings/` directly** — every path runs through one of the three tools. Determinism by construction.

## Self-improvement — five compounding loops

All cheap, all integer arithmetic, no ML.

1. **Refinement, not append.** `remember(supersedes=X)` marks X superseded; default search hides it. Agents revise beliefs instead of accumulating contradictions.
2. **Access counters.** Citation detection on recall hits appends an `access` event to `counters.jsonl`. Frequently-recalled memories rank higher. Atomic JSONL appends, no row-locking needed; multi-agent safe in project rooms.
3. **Confidence drift.** Successful action on a recalled memory → +0.05; contradicted via supersedes → −0.20. Below 0.2 + no access in 90d → auto-archive (sweep, never blocking).
4. **Conflict detection on `remember()`.** Pre-write substring + tag-overlap check against active memories. High overlap → `"looks similar to [[existing-id]] — update that instead?"`. Stops duplicate-memory drift.
5. **Distillation pass — the Dreamcatcher.** Nightly for project rooms (default 03:00, configurable), on-demand for private rooms. Reads recent session transcripts + learnings, asks LLM "what durable memories should I extract?", writes proposals to `<room>/.omnipus/proposals/<id>.md`. Operator or agent reviews via the SPA; acceptance moves the file to `memories/` via `remember()`. Sessions → memories. Also detects emerging clusters (3+ memories sharing a tag with no MOC) and proposes new topical MOCs.

## Graph layer

### Six edge types, all deterministic

| Relation | From | To | Source |
|---|---|---|---|
| `links_to` | memory | memory | `[[id]]` in body, regex |
| `tagged` | memory | tag | frontmatter `tags:` |
| `authored_by` | memory | agent | frontmatter `author:` |
| `supersedes` | memory | memory | frontmatter `supersedes:` |
| `born_in` | memory | session | session id captured at `remember()` time |
| `cited_in` | memory | session | recall returned it AND agent text contains its id or title |

No similarity edges, no inferred edges, no embeddings.

### Storage — JSONL files + bleve index, no SQLite

The `.index/` directory holds five derived artifacts. All rebuildable from the `.md` and `.jsonl` source files.

```
.index/
├── bleve/                    bleve v2 FTS index (binary, isolated, rebuildable)
├── edges.jsonl               graph edges, append-only, one JSON line per edge
├── counters.jsonl            access/drift event log, append-only
├── tags.json                 tag → memory_ids map, atomic rewrite
└── minhash.jsonl             MinHash signatures for near-duplicate detection
```

**`edges.jsonl`** — one edge per line, append-only:

```jsonl
{"ts":"2026-05-03T10:14:00Z","src":"mem:lndlk-7f3a","rel":"links_to","dst":"mem:hardened-exec","cross_room":false}
{"ts":"2026-05-03T10:14:00Z","src":"mem:lndlk-7f3a","rel":"tagged","dst":"tag:security"}
{"ts":"2026-05-03T10:14:00Z","src":"mem:lndlk-7f3a","rel":"supersedes","dst":"mem:lndlk-2026-04"}
```

In-memory edge map built at boot via linear scan (~50 ms per 10k edges). Multi-process safe via atomic `O_APPEND` writes (single-line edges fit under POSIX `PIPE_BUF`). Tombstones for deletions. Periodic compaction (weekly) drops superseded entries.

**`counters.jsonl`** — append-only event log, not state:

```jsonl
{"ts":"2026-05-03T10:14:00Z","memory_id":"lndlk-7f3a","op":"access","by":"jim"}
{"ts":"2026-05-03T10:14:00Z","memory_id":"lndlk-7f3a","op":"drift","by":"jim","amount":0.05}
{"ts":"2026-05-04T03:00:00Z","memory_id":"old-7e44","op":"archive","by":"sweep"}
```

`access_count` = count of `access` events. `confidence` = base + Σ drift events. Built in-memory at boot. Multi-agent safe (atomic appends, no row contention). Compaction rewrites as `{snapshot}\n{tail}` weekly. Audit history preserved.

**`tags.json`** — small JSON map, atomic rewrite on memory create/update:

```json
{"security":["lndlk-7f3a","sccmp-2b1c"],"kernel":["lndlk-7f3a","ldlk-9c2b"]}
```

A few thousand tags × hundreds of memories per tag stays under 1 MB. Atomic temp+rename, no partial-state.

**`bleve/`** — bleve v2 full-text index. Pure Go. Apache 2.0. Provides:

- BM25 ranking on multi-field documents (frontmatter fields + body indexed separately)
- `MoreLikeThis` queries for "find memories similar to this one"
- Tokenization with markdown-aware analyzer
- Hybrid search via RRF (reserved for future embedding addition; not used in v1)

Bleve writes its own binary index files isolated to `.index/bleve/`. They are **derived artifacts**, never source of truth. Blow away and re-index from `memories/*.md` if corrupted.

**`minhash.jsonl`** — MinHash signatures for near-duplicate detection on `remember()`:

```jsonl
{"memory_id":"lndlk-7f3a","sig":[12345,67890,...],"computed_at":"2026-05-03T10:14:00Z"}
```

~128 bytes per memory. Algorithm: ekzhu/minhash-lsh. On new `remember()`, compute signature, query LSH, flag candidates above the conflict threshold. Pure Go, deterministic.

**Adding a new edge type** is a no-op — just start writing JSON lines with the new `rel` value. No schema migration.

### Recall ranking

```
score = tag_match * 4
      + bm25_score
      + log(access_count + 1)
      - days_since_last_access * 0.01
      + 0.1 if learning.source = joint
```

Pure arithmetic. Same inputs → same ranking.

### MOCs (D12)

A memory with `type: moc` is a navigation hub whose body is mostly wikilinks. Two sources of MOCs:

**Seeded at project creation — six universal files, all cross-domain:**

```
<project>/.omnipus/memories/
├── home.md                    ← LYT-style entry point; links to the 5 categorical MOCs
├── moc-decisions.md           ← "What did we choose, and why?"
├── moc-lessons.md             ← "What did we learn?"
├── moc-people.md              ← "Who's involved? Who do we cite?"
├── moc-references.md          ← "What external sources do we keep?"
└── moc-goals.md               ← "What are we trying to accomplish?"
```

`home.md` opens to a one-screen view of the project's brain with wikilinks to the five categorical MOCs and to the first ~5 memories until topical MOCs emerge. The five categorical MOCs are deliberately domain-neutral so the platform fits any kind of project, not just software.

**Auto-emerged via Dreamcatcher:**

- 3+ memories sharing a tag with no MOC for it → Dreamcatcher proposes `moc-<tag>.md` (operator/agent approves via the proposals queue).
- Orphan memory with no incoming links after 5 sessions → Dreamcatcher flags it for linking from `home.md` or an existing MOC.
- Empty seed MOC (e.g., `moc-people` after 30d with zero entries) → Dreamcatcher proposes archival.

The auto-emerge cluster threshold is configurable (`dreamcatcher.cluster.min_memories`, default 3) via the Dreamcatcher settings tab.

MOCs always exist (six seeded per project) so the graph is never empty. Solves the cold-start problem.

### Obsidian compatibility

Wikilinks render natively. Frontmatter is YAML which Obsidian shows. Tags are first-class. Supersedes is rendered as a body line `Supersedes: [[old-id]]` so the link appears in the graph view. Operational edges (`born_in`, `cited_in`) live only in `edges.jsonl` — they aren't narrative and shouldn't clutter the visual graph.

## Determinism scorecard

| Path | Trigger | Decision-maker | Deterministic? |
|---|---|---|---|
| Memory created | `remember()` call | agent or auto-extract regex | content reproducible; type=note default |
| Memory revised | `remember(supersedes=X)` | agent text | exact |
| Session firehose | every turn | system | exact |
| Last session summary | session close | system + LLM | LLM prose, deterministic file path |
| Auto learning | session close | system + LLM | LLM content, deterministic file path |
| Joint learning | `retrospective()` call | agent (user-invited) | content reproducible |
| Confidence ↑ | recall + citation regex | system | exact (regex caveat: misses paraphrases — acceptable, biases conservative) |
| Confidence ↓ | `supersedes=X` | system | exact |
| Auto-archive | `confidence < 0.2 AND no_access > 90d` | system sweep | exact |
| Type re-classified | Dreamcatcher pass | LLM, offline | non-deterministic but human-gated |

Hot path is regex + arithmetic + bleve BM25 + atomic JSONL appends. LLM only at session-close (one call) and the Dreamcatcher (nightly for project rooms, operator-gated for private).

## Auto-injection — what the agent sees by default

At session start, the agent's system prompt prepends:

```
## Last session
{contents of last-session.md, ~500-2000 tokens}
```

That is the only memory file loaded automatically. Memories, learnings, and historical sessions are all `recall()`-only. Token cost bounded; agent has continuity without paying for full memory injection.

## Security

- Memory bodies are plain markdown. No code path executes their content.
- Frontmatter is YAML parsed with strict allowlist of keys; unknown keys fail-closed; anchors and merge keys disabled.
- Memory id format enforced at write boundary: `[a-z0-9-]{4,32}`. No path separators, no shell metas.
- Atomic writes (temp + rename) via existing `fileutil.WriteFileAtomic`.
- Cross-agent project-room writes: per-file flock + etag precondition on update. Counters via append-only `counters.jsonl` (atomic POSIX appends) to avoid frontmatter contention.
- `.index/` and `.system.jsonl` in `.gitignore` by default.
- Project room committable by default (memories + learnings); operator can opt-out per project.

## Fresh install — no backward compatibility

This is a fresh-build redesign. The legacy `MEMORY.md`, `LAST_SESSION.md`, and `retrospectives/*.json` formats are **not preserved or migrated**. Existing data in those locations is ignored; the new memory subsystem starts empty.

At first boot of an agent, the gateway creates the agent's room layout with empty directories:

```
<OMNIPUS_HOME>/agents/<id>/.omnipus/
├── sessions/         (empty — fills as conversations happen)
├── memories/         (empty — fills as remember() is called)
├── learnings/        (empty — fills as session-end recaps run)
├── proposals/        (empty — fills if Dreamcatcher runs)
├── .system.jsonl     (empty)
└── .index/           (empty bleve index, empty JSONL files)
```

At project creation, the project room is seeded with the six universal MOCs (`home.md`, `moc-decisions`, `moc-lessons`, `moc-people`, `moc-references`, `moc-goals`) and otherwise empty.

The legacy storage code in `pkg/agent/memory.go` (monolithic `MEMORY.md`, `<!-- next -->` block parser, etc.) is removed wholesale when the new subsystem lands. No coexistence period.

## What we cut from the current system

- Monolithic `MEMORY.md` → per-memory files. Removes the global mutex bottleneck.
- Three fixed categories → open `type` field + free-form `tags`.
- 4096-rune content cap → soft warn at 8 KB, hard cap at 64 KB.
- Hardcoded "search MEMORY.md + LAST_SESSION.md + retros from 30 days" combo → `recall(scope=memories|learnings|sessions|all)`.
- Parallel `LAST_SESSION.md` and `retrospectives/` artifacts → unified through the session-close pipeline (one LLM call → two outputs).

## Pros and cons

### Pros

1. Source-of-truth is `.md` files; indexes are derived and rebuildable. No schema migrations ever.
2. No embeddings, no vector store, no ML model dependency. Auditable, deterministic, replayable.
3. **No SQLite anywhere** — adheres to the `CLAUDE.md` storage principle (file-based JSON/JSONL only for Omnipus data).
4. Counter and edge writes are append-only JSONL with atomic POSIX semantics — multi-agent project rooms have zero row-locking concerns.
5. Counter audit history preserved indefinitely (until compaction) — operators can answer "who cited this memory and when?" by grepping the log.
6. Operator-debuggable in Obsidian — graph view, backlinks, tag pane all work natively.
7. Self-improvement is structural (revision, citation counters, drift), not statistical.
8. Atomic file semantics — one file per memory, atomic temp+rename writes.
9. Graph + tags + MOCs compensate for no semantic search.
10. Same format works in private and project rooms; only `author` differs.
11. Three composable tools, each maps to one destination.
12. Project memory is a real PR-able artifact via per-file git diffs.
13. Multi-agent collaboration uses git as the sync mechanism — no central server, no merge daemon.

### Cons

1. **Inode/file-count pressure.** 10k memories = 10k files; old/exotic filesystems struggle. Mitigation: 2-char-prefix sharding above 5k.
2. **No cross-vocabulary semantic similarity.** "kernel safety" doesn't hit "Landlock" without shared words. The fundamental trade-off vs an embedding system. Mitigated by (a) the LLM rephrasing queries on miss — agent retries with synonyms, effectively a query-expansion loop; (b) tags + wikilinks bridging vocabulary islands; (c) MOC navigation hubs clustering related concepts. For the personal-AI-agent use case, these compensations recover most of the practical recall an embedding model would give. If usage shows real gaps later, bleve v2.6+ already supports vector + RRF hybrid — adding embeddings is one schema change, not a re-architecture.
3. **Heuristic numbers.** Confidence drift weights, ranking weights, archive thresholds — initial guesses; need real-traffic tuning.
4. **Cold-start problem.** Empty graph has no edges. Mitigated by seeded MOCs.
5. **Distillation costs LLM tokens.** Mitigated: on-demand by default; scheduled opt-in.
6. **Wikilink fragility under rename.** Mitigated: canonicalize to id at write; titles display-only.
7. **YAML frontmatter foot-guns.** Mitigated: strict allowlist parser, anchors/merge keys disabled.
8. **Project-room write conflicts.** Mitigated: per-file flock, etag preconditions, atomic JSONL append for counters (no row-locking needed).
9. **Cross-room link rot.** A private memory linked from a project memory becomes broken if the agent leaves the project. Mitigated: keep cross-room edges marked; render as broken with a hint.
10. **Project room can leak private context.** Auto-recap writing project learnings with private session details requires careful prompt design at session-close. Mitigated: recap LLM is told "exclude private details"; operator review surface for new project entries during ramp-up.
11. **Wikilinks not rendered on github.com.** Operators viewing memory on GitHub see literal `[[id]]`. Acceptable — Obsidian + the SPA render correctly.
12. **Two-room ambiguity at the boundary.** Some memories are genuinely both private-and-project-relevant. Default "follow session context" + explicit override is the answer; some friction remains for edge cases.

## Sequencing

This redesign and the sandbox redesign (`sandbox-redesign-2026-05.md`) share the room topology but are independently shippable.

- **A. Sandbox first, memory second** *(recommended)* — sandbox lands per-agent + project room directories under `.omnipus/`. Memory then slots into the rooms with no new path work.
- **B. Memory first** — possible but requires migrating the directory layout twice. Avoid.
- **C. Parallel** — high merge-conflict risk on `pkg/agent/memory.go`, `pkg/agent/loop.go`, `pkg/coreagent/core.go`. Avoid.

Within the memory work itself, sequence:

1. Memory file format + tools (`remember`, `recall`, `retrospective`) on a single private room, no graph yet.
2. Index layer (bleve FTS + edges.jsonl + counters.jsonl + minhash.jsonl) and graph-aware recall.
3. Project-room dimension (D17–D19), promotion verb, multi-agent write tests.
4. Dreamcatcher distillation pass.

## Locked decisions (resolving prior open questions)

| Was | Decision | Source |
|---|---|---|
| Distillation cadence | Nightly for project rooms (default 03:00); on-demand for private rooms | Q4 (2026-05-03) |
| Naming of distillation pass | "Dreamcatcher" — replaces internal "librarian" | Q4 follow-up |
| Author enforcement on shared memories | Soft / collaborative — any project member can supersede; `author` field is informational only and never mutated | Q3 |
| Memory size limits | 8 KB soft warning, 64 KB hard cap | Q7 |
| Conflict-detection threshold for `remember()` | Flag if first-line similarity ≥ 60% OR (body similarity ≥ 50% AND ≥ 1 shared tag); skip if `supersedes=` already passed | Q9 |
| MOC seeding policy | Six universal files at project creation: `home.md`, `moc-decisions`, `moc-lessons`, `moc-people`, `moc-references`, `moc-goals`. Domain-neutral. Auto-emerge of topical MOCs via Dreamcatcher | Q5 |
| Proposal storage | Dedicated `<room>/.omnipus/proposals/<id>.md` directory; accept moves to `memories/`, reject deletes (or `proposals/_rejected/` if paper trail wanted); auto-expire 30 days | Q6 |
| `last-session.md` target detail | Adaptive: ~150 tok < 5 turns; ~400 tok 5–15; ~800 tok 15–50; ~1500 tok 50–100; ~2500 tok 100+; hard cap 4000 | Q12 |
| Cross-room recall ranking | Project memories +0.2 score boost when in project-bound sessions (no boost in private sessions) | Q8 |
| Project deletion | Cascade: deletes project room contents + sessions bound to project + tasks + last-project-session.md. Operator types project name to confirm (danger-zone pattern) | Q10 |
| Project archive feature | Not implemented. Lifecycle is binary — active or deleted | Q11 |
| Auto-recap learnings privacy | Stay in private room only; project room receives a sanitized `last-project-session.md` field from the same recap LLM call | Q1 |
| Project-bound session creation | Operator picks project + agent at session creation; binding is immutable | Q2 |
| Agent removal from project | Lose read AND write access. Authored content stays. Tasks assigned to removed agent become unassigned. Re-addable at any time, no special flow | Q15 |

## Remaining open questions

1. **Confidence drift weights** (`+0.05/−0.20`) — defer until real-traffic tuning.
2. **Cluster detection thresholds** for Dreamcatcher auto-emerged MOCs (3 memories, 0.6 tag overlap) — defer until tuning.
3. **Per-operator notification preferences** in multi-user (SaaS) deployments — out of scope for OSS / Desktop variants.
4. **Cross-project memory search** behavior — locked elsewhere (`projects-ui-2026-05.md` Q14).

## References

- `pkg/agent/memory.go` — current monolithic `MEMORY.md` implementation.
- `pkg/tools/memory.go` — current `remember`/`recall_memory`/`retrospective` tool surface.
- `pkg/agent/memory_adapter.go` — `MemoryStoreAdapter` bridging tools and storage.
- `pkg/agent/context.go:176-192,359` — how `MEMORY.md` is wired into the system prompt today.
- `pkg/agent/session_end.go` — current recap pipeline (`CloseSession → runRecap → WriteLastSession + AppendRetro`).
- `docs/internal/design/sandbox-redesign-2026-05.md` — companion design; defines the two-room topology this design plugs into.
- `github.com/blevesearch/bleve/v2` — Apache 2.0, pure-Go FTS + BM25 + MoreLikeThis; v2.6.0 released 2026-04-30.
- `github.com/adrg/strutil` — MIT, pure-Go string-similarity metrics (Jaccard, Levenshtein) for the conflict-detection quick check.
- `github.com/ekzhu/minhash-lsh` — MIT, pure-Go MinHash + LSH for near-duplicate detection. Algorithm mature; vendor a small port if upstream staleness becomes a concern.
- **No SQLite, no CGo, no embedding model** — by design.
