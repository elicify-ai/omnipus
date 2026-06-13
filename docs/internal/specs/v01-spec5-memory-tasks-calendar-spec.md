# Spec-5 — Memory Rooms (structure + logs), Task Fields & Calendar Shell (v0.1.0 Foundation)

- **Spec:** 5 of 6 (v0.1.0 Foundation)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-7 (memory structure + logs) + FR-8 (tasks/calendar)
- **Status:** Draft → pending `/grill-spec` (GATE C)
- **Cross-spec (Phase 3.5):** rooms are keyed to Spec-1's `Workspace` (the shared room is `<workspace>/.omnipus/`); the Orchestrator (Spec-3) advances `blocked_by` DAGs via `SetOnComplete`; `task_status_changed` is the existing WS frame (Spec-3 grounding).
- **Scope guard:** v0.1.0 ships **structure + frozen log formats only** — the ranking/graph/Dreamcatcher/weights **behaviour is v0.2.0** (ADR NFR-6).
- **Lessons pre-applied:** ground hard; contract-first; CI-authority; new deps = ADR decision; freeze persisted formats (NFR-1/NFR-7); compiler/test gate.

## 1. Overview

Land the memory **structure** (not behaviour): **two rooms** — a **private** per-agent room (`agents/<id>/.omnipus/`, agent-global) + a **shared workspace room** (`<workspace>/.omnipus/`, keyed to Spec-1's Workspace); the **full per-memory file format** (frontmatter, every field present even if unused); **3 new tools** `remember`/`recall`/`retrospective` **replacing the monolithic `MEMORY.md`**; **bleve** full-text recall (a **new pure-Go dep**); **freeze the append-only LOG RECORD FORMATS** (`sessions/` firehose · `counters.jsonl` · `born_in`/`cited_in`) so v0.2 ranking never backfills; **no embeddings · MinHash dedup · no SQLite**. Add the **task fields** `start·due·recurrence·blocked_by` (additive; `blocked_by` with its cycle/orphan validator + delete/runtime semantics), and the per-workspace **Calendar/Automations shell** (greenfield).

**In scope:** the 2-room directory topology (workspace-keyed); the per-memory file format (frontmatter schema, fully pinned); the 3 tools (`remember`/`recall`/`retrospective`); bleve FTS recall (new dep); the frozen log-record formats + starting the logs; MinHash dedup; the additive task fields + the `blocked_by` validator (+ delete/runtime semantics); the Calendar/Automations shell.
**Out of scope (v0.2.0 behaviour):** confidence drift, recall ranking, the graph (6 edges/MOCs), the Dreamcatcher, weights/thresholds (ADR NFR-6); functional recurrence execution + the automations engine (shell only); embeddings (never).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `pkg/memory/*.go` + `pkg/agent/memory.go::MemoryStore` (`MEMORY.md` + day-partitioned `_retro.md` + literal-substring `SearchEntries`; audit hooks + rate limiter) | **REWRITE** (live subsystem, NOT greenfield — C-1/M-2) | restructured into the rooms + file format; existing MEMORY.md data MUST migrate (M-2) |
| the 3 tools — **EXIST** (`pkg/tools/memory.go`: `RememberTool`→`remember`, `RecallMemoryTool`→**`recall_memory`**, `RetrospectiveTool`→`retrospective`) | **re-point** to the rooms | NOT new (C-1); keep the registered tool names (`recall_memory`, M-1) or alias |
| **bleve** | **NEW dep** | `0` in go.mod — MUST use the **pure-Go `scorch` index** and **FORBID the CGo `leveldb`/`rocksdb` backends** (the tree already has CGo creep via `mattn/go-sqlite3` indirect — C-4); a CI check asserts no new CGo. ADR-019 FR-7 records the dep. |
| the append-only logs (`sessions/` · `counters.jsonl` · `born_in`/`cited_in`) | **freeze formats + start writing** | record schemas pinned in v0.1.0 (NFR-1) |
| `pkg/boardtask/boardtask.go` + `contracts/.../BoardTask*.yaml` | **add** `start·due·recurrence·blocked_by` | additive; `blocked_by` absent today (Spec-1 grounding) |
| `task_status_changed` WS frame + `SetOnComplete` (Spec-3) | the validator + Orchestrator hook | Spec-3 owns the coordinator; this spec owns the fields + validator |
| calendar | **NEW** | no `pkg/calendar`; greenfield shell |

### Impact Assessment
| Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| memory rooms + file format + 3 tools | **HIGH** | the memory read/write paths, the agent loop's memory adapter, the per-agent + workspace dirs | recall in prompts |
| bleve dep | MEDIUM | go.mod, the recall index | Constraint #1 (ADR) |
| frozen log formats | **HIGH** (NFR-1) | the log writers + v0.2 readers | ranking/graph (v0.2) |
| task fields (contract) | **HIGH** (contract) | generated types + the board UI + `blocked_by` validator | the Orchestrator (Spec-3) |
| calendar shell | LOW | a new surface | — |

## 3. User Stories

**US-1 — Two rooms, workspace-keyed (P0).** 1. **Given** an agent, **When** it writes a private memory, **Then** it lands in `agents/<id>/.omnipus/` (agent-global); **When** it writes in a workspace session, **Then** the shared memory lands in `<workspace>/.omnipus/` (Spec-1 Workspace key). 2. **Given** the workspace room, **Then** it has the 3 tiers + `last-session.md`, no per-session sessions dir (D19).

**US-2 — Full per-memory file format, pinned (P0, NFR-7).** 1. **Given** a memory, **When** written, **Then** it is a file with the **full frontmatter** (id, type, tags, links, confidence, status, supersedes, born_in, …) — every field present even if unused, so v0.2 enriches without migrating files.

**US-3 — 3 tools replace MEMORY.md (P0).** 1. **Given** `remember`/`recall`/`retrospective`, **When** an agent calls them, **Then** they write/read/summarize against the rooms; `MEMORY.md` is no longer the store. 2. **Given** the tool contracts, **Then** `verify-contracts` exits 0 (if they cross the boundary) / they register in the tool catalog.

**US-4 — bleve FTS recall (P0).** 1. **Given** bleve (new pure-Go dep), **When** `recall` runs, **Then** it returns BM25 full-text matches (no embeddings, no SQLite). 2. **Given** the index is derived, **Then** it is rebuildable from the `.md` sources.

**US-5 — Frozen log formats + start the logs (P0, NFR-1).** 1. **Given** v0.1.0, **When** sessions/access/citations happen, **Then** the append-only logs (`sessions/`, `counters.jsonl`, `born_in`/`cited_in`) are written in their **frozen v0.1.0 record formats** — so v0.2 ranking reads them with **no backfill**.

**US-6 — Task fields + blocked_by validator (P0).** 1. **Given** the additive fields `start·due·recurrence·blocked_by`, **When** `make verify-contracts` runs, **Then** exit 0. 2. **Given** `blocked_by`, **Then** a write-time validator rejects cycle-creating edges; deleting a task cascade-cleans its edges + surfaces dependents; orphan edges are dropped (carried from Spec-1's task semantics — operator Q1).

**US-7 — Calendar/Automations shell (P1).** 1. **Given** the per-workspace Calendar surface, **When** I view it, **Then** scheduled tasks/events/milestones render (shell — the automations *engine* is v0.2.0). 

### Edge Cases
- bleve index missing/corrupt → rebuilt from `.md` (derived). · A workspace with no shared room → created on first write. · MinHash near-duplicate memory → deduped on write. · `blocked_by` cycle → rejected (Spec-1 semantics). · Recurrence field set but engine absent → stored, not executed (shell). · Migrating off `MEMORY.md` → greenfield (no old MEMORY.md read).

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Contract:** 2 rooms keyed to workspace; full per-memory file format; 3 tools; bleve BM25 recall; frozen log formats written from v0.1.0; MinHash dedup; additive task fields with the `blocked_by` validator; calendar shell.

**Non-behaviors:** must **not** ship ranking/graph/Dreamcatcher/weights (v0.2.0); must **not** use embeddings or SQLite; must **not** leave `MEMORY.md` as the store; must **not** require backfilling logs in v0.2 (formats frozen now); must **not** execute recurrence/automations (shell only); must **not** run the full Go suite locally (CI authority); greenfield.

**Integration boundaries:** none external. Internal: the memory rooms are file-based under the workspace/agent dirs (atomic writes, the existing 64-shard mutex); bleve indexes the `.md` files; the `blocked_by` validator runs at task write + the Orchestrator (Spec-3) reads the DAG.

## 5. BDD Scenarios

```gherkin
Scenario: Private vs shared room routing by workspace
  Traces to: US-1 / AC-1
  Category: Happy Path
  Given agent ray
  When ray writes a private memory
  Then it lands under agents/ray/.omnipus/
  When ray writes during a session in workspace Acme
  Then the shared memory lands under <Acme>/.omnipus/

Scenario: Memory file carries the full frontmatter
  Traces to: US-2 / AC-1
  Category: Happy Path
  Given remember is called
  When the memory file is written
  Then it has the full frontmatter schema (all fields present, unused ones empty)

Scenario: recall uses bleve BM25 (no embeddings)
  Traces to: US-4 / AC-1
  Category: Happy Path
  Given memories indexed in bleve
  When recall("query") runs
  Then it returns BM25 matches
  And no embedding/vector store is used

Scenario: bleve index rebuilds from .md sources
  Traces to: US-4 / AC-2
  Category: Alternate Path
  Given a deleted bleve index
  When recall runs
  Then the index is rebuilt from the .md files and returns results

Scenario: Logs written in frozen v0.1.0 formats
  Traces to: US-5 / AC-1
  Category: Happy Path
  Given a session + a recall + a citation
  When they occur
  Then sessions/, counters.jsonl, born_in/cited_in are appended in their frozen record formats

Scenario: blocked_by cycle rejected at write
  Traces to: US-6 / AC-2
  Category: Error Path
  Given task B blocked_by A
  When I add A blocked_by B
  Then the validator rejects the cycle

Scenario: Task fields regenerate clean
  Traces to: US-6 / AC-1
  Category: Happy Path
  Given start·due·recurrence·blocked_by added
  When make verify-contracts runs
  Then exit 0
```

## 6. TDD Plan

| Order | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | `TestMemory_PrivateVsSharedRoom_ByWorkspace` | Unit | "room routing" | path routing |
| 2 | `TestMemory_FileFormat_FullFrontmatter` | Unit | "full frontmatter" | format pinned |
| 3 | `TestMemoryTools_RememberRecallRetrospective` | Integration | "3 tools" | tool behaviour |
| 4 | `TestRecall_BleveBM25_NoEmbeddings` | Integration | "bleve BM25" | recall |
| 5 | `TestBleveIndex_RebuildFromMd` | Integration | "index rebuilds" | derived |
| 6 | `TestLogs_FrozenRecordFormats` | Unit | "frozen formats" | NFR-1 |
| 7 | `TestMinHash_DedupOnWrite` | Unit | edge | dedup |
| 8 | `TestBlockedBy_CycleRejected` | Unit | "cycle rejected" | validator |
| 9 | `verify-contracts` (CI) | CI | "task fields regen" | drift = fail |
| 10 | `e2e: Calendar shell renders scheduled tasks/events` | E2E | US-7 | SPA |

**Test Datasets**: room {private→agents/<id>, shared→<workspace>}; frontmatter {all fields}; recall {match, no-vector}; index {deleted→rebuild}; minhash {near-dup→deduped}; blocked_by {cycle→reject, delete→cascade-clean}; recurrence {set→stored-not-run}.

**Regression:** modifies the memory store (MEMORY.md→rooms) + task model. (1) Greenfield — no old MEMORY.md migrated; (2) the existing `.jsonl` session history (jsonl.go) coexists/migrates to the rooms structure; (3) task CRUD (Spec-1 workspace-keyed) preserved + extended; (4) NEW: rooms, 3 tools, bleve, logs, blocked_by validator, calendar. **CI authority; local scoped only.**

## 7. Functional Requirements & Success Criteria

- **FR-7.1:** MUST create the 2-room topology — private `agents/<id>/.omnipus/` (agent-global) + shared `<workspace>/.omnipus/` (Spec-1 Workspace-keyed); the workspace room has 3 tiers + `last-session.md`, no sessions dir (D19).
- **FR-7.2 (C-3):** the **full per-memory frontmatter schema** (inlined from `memory-redesign-2026-05.md`): `id` · `title` · `type` ∈ {decision|fact|reference|lesson|person|project|moc|note} · `tags: []` · `confidence` (0–1, denormalized cache — `counters.jsonl` is authoritative) · `status` ∈ {active|superseded|archived} · `supersedes` (memory id) · `author` (agent) · `born_in` (session id, provenance); the body carries `[[id]]` wikilink narrative edges. **Every field present even if empty** (NFR-7 — so v0.2 enriches without migrating files).
- **FR-7.3 (C-1, M-1):** MUST **re-point the EXISTING tools** (`remember` / `recall_memory` / `retrospective`, `pkg/tools/memory.go`, backed by `MemoryStore`) to the rooms — keeping the registered names (`recall_memory`, not `recall`) or a documented alias; the backend is rewritten to the rooms/file-format.
- **FR-7.4 (C-4):** MUST add **bleve** pinned to the **pure-Go `scorch` index**, **forbidding** the CGo `leveldb`/`rocksdb` backends; a **CI check asserts no new CGo** (`CGO_ENABLED=0` build stays green); the index is **derived/rebuildable** from the `.md` sources; **no embeddings, no SQLite**. **Index lifecycle:** built incrementally on memory write, **rebuilt from `.md` on corruption/absence**; **concurrency** coordinated with the existing 64-shard memory mutex (bleve's own writer lock + the shard lock — no double-write races) (M-6).
- **FR-7.5 (C-2):** the frozen append-only **log record schemas** (inlined from the design doc): **`counters.jsonl`** = `{ts, memory_id, op, by, amount?}` where `op` ∈ {access|drift|cited} — append-only, **atomic** (one event line < `PIPE_BUF` 4 KB, POSIX-safe, multi-process-safe); **`sessions/<id>/<date>.jsonl`** = the existing firehose (one turn per line, 90d retention). **`born_in` is frontmatter** (not a log); **`cited_in` is a `counters.jsonl` `op:cited` event** (recall-hit AND the agent text references the memory id/title). The **`.index/`** (`bleve/`, `edges.jsonl`, `tags.json`, `minhash.jsonl`) is **DERIVED/rebuildable from the `.md` + `counters.jsonl`** — NOT frozen. **MinHash** dedup is **non-destructive** (near-dups linked via `minhash.jsonl`, not deleted — M-5).
- **FR-7.6 (M-2):** memory is **NOT greenfield** — there is live `MEMORY.md`/`_retro.md` data + an existing `MigrateFromJSON`. MUST **migrate existing memory into the rooms** on upgrade (no silent loss); migration is one-way + idempotent.
- **FR-8.1:** MUST add additive task fields `start·due·recurrence·blocked_by`; `verify-contracts` exits 0.
- **FR-8.2 (M-3, M-4):** MUST ship `blocked_by` with a **write-time validator** rejecting **self-edges, and both 2-node AND N-node cycles** (a full DAG cycle check, not pairwise-only), **dropping orphan edges on load**, with a **depth bound** (the Spec-3 Orchestrator traverses the DAG on every status change). **Delete semantics:** deleting a task **cascade-cleans its inbound+outbound `blocked_by` edges + surfaces dependents** — this is **THIS spec's edge cleanup**, distinct from Spec-1's project→task-file cascade (NOT attributed to Spec-1). Operator Q1.
- **FR-8.3 (M-7):** MUST add a per-workspace **Calendar/Automations shell** — a **defined SPA surface** (a Calendar view on the workspace) rendering scheduled tasks/events/milestones; the automations *engine* + recurrence *execution* are v0.2.0 (shell only).

**Success Criteria**
- **SC-1:** `verify-contracts` exits 0 (CI). · **SC-2:** build + typecheck exit 0 (CI authority; local scoped). · **SC-3:** private/shared memory route to the right dirs by workspace. · **SC-4:** memory files carry the full frontmatter. · **SC-5:** `recall` returns bleve BM25 (0 embedding/SQLite deps added). · **SC-6:** the 3 logs are written in their frozen formats; rebuilding bleve from `.md` works. · **SC-7:** a `blocked_by` cycle is rejected at write. · **SC-8:** the Calendar shell renders (no engine).

## 8. Traceability Matrix

| Req | US | BDD | Test |
|---|---|---|---|
| FR-7.1 | US-1 | "room routing" | #1 |
| FR-7.2 | US-2 | "full frontmatter" | #2 |
| FR-7.3 | US-3 | "3 tools" | #3 |
| FR-7.4 | US-4 | "bleve BM25" / "index rebuilds" | #4,#5 |
| FR-7.5 | US-5 | "frozen formats" | #6,#7 |
| FR-8.1 | US-6 | "task fields regen" | #9 |
| FR-8.2 | US-6 | "cycle rejected" | #8 |
| FR-8.3 | US-7 | (e2e) | #10 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | 3-tools contract vs tool-catalog only | catalog registration; contract if boundary | confirm at impl — they're agent tools (like system.*), catalog-registered |
| 2 | bleve dep / Constraint #1 | new pure-Go dep | RESOLVED — accepted, like emersion/go-imap; document in ADR FR-7 |
| 3 | existing .jsonl history vs rooms | coexist / migrate to rooms | greenfield install = rooms; existing-install .jsonl out of scope (greenfield) |
| 4 | exact frontmatter field set | from memory-redesign-2026-05 | pin the full set at impl from the design doc |
| 5 | calendar shell scope | render-only, no engine | RESOLVED — shell; engine v0.2.0 |

## 10. Holdout Evaluation Scenarios *(post-impl; NOT in traceability)*
- H1: an agent remembers a fact in a workspace; another session recalls it → bleve returns it.
- H2: delete the bleve index → recall still works (rebuilt).
- H3: inspect a memory file → full frontmatter present.
- H4: create A blocked_by B, then B blocked_by A → rejected.
- H5: grep go.mod → bleve present, 0 embedding/sqlite deps.
- H6: the Calendar shows a scheduled task; recurrence stored but not auto-run (shell).

## 11. Assumptions
- Memory is **NOT greenfield** — existing `MEMORY.md`/`_retro.md` MUST migrate into the rooms (M-2); the Workspace/install is greenfield, but **user memory is preserved**. `[C-1/M-2]`
- bleve is the approved new pure-Go FTS dep (no CGo/SQLite/embeddings). `[ADR FR-7]`
- Rooms are keyed to Spec-1's `Workspace`; the shared room is `<workspace>/.omnipus/`. `[cross-spec Spec-1]`
- The Orchestrator (Spec-3) consumes `blocked_by` via `SetOnComplete`; this spec owns the fields + validator. `[cross-spec Spec-3]`
- Ranking/graph/Dreamcatcher/weights are v0.2.0 behaviour over these frozen structures + logs. `[ADR NFR-6]`
