# Spec Review — Spec-5: Memory Rooms (structure + logs), Task Fields & Calendar Shell

> ## ROUND 2 (2026-06-13) — RE-REVIEW after round-1 BLOCK · **Verdict: PASS (GATE C)**
>
> All four round-1 CRITICAL (C-1..C-4) and all seven MAJOR (M-1..M-7) findings are
> **closed and verified against the codebase**. Remaining items are MINOR /
> OBSERVATION and do not block. Spec is ready for `/taskify`.
>
> | Severity | Round 1 | Round 2 (open) |
> |----------|---------|----------------|
> | CRITICAL | 4 | 0 |
> | MAJOR | 7 | 0 |
> | MINOR | 5 | 3 (new) |
> | OBSERVATION | 3 | 3 (new) |
>
> ### Round-1 closure verification (grounded in code)
>
> | ID | Round-1 defect | Status | Evidence |
> |----|----------------|--------|----------|
> | **C-1** | Falsely said 3 tools are NEW | **CLOSED** | §2 row 2 + FR-7.3 now say the tools **EXIST** and are **re-pointed**. Confirmed: `pkg/tools/memory.go` — `RememberTool.Name()="remember"` (L90), `RecallMemoryTool.Name()="recall_memory"` (L262), `RetrospectiveTool.Name()="retrospective"` (L353), all over `MemoryAccess`/`MemorySearcher`→`MemoryStore`. |
> | **C-2** | Frozen log schemas absent | **CLOSED** | FR-7.5 inlines `counters.jsonl={ts,memory_id,op∈{access\|drift\|cited},by,amount?}` atomic `<PIPE_BUF`; `sessions/<id>/<date>.jsonl` firehose; `born_in`=frontmatter; `cited_in`=`counters` `op:cited`; `.index/` DERIVED. Matches design-doc D15 (memory-redesign-2026-05.md L295-303). |
> | **C-3** | Frontmatter unpinned (`…`) | **CLOSED** | FR-7.2 inlines the closed set `id·title·type{8-enum}·tags[]·confidence·status{3-enum}·supersedes·author·born_in`+`[[id]]`, "every field present even if empty." Matches design-doc memory shape (L92-114, 8-type enum L99). |
> | **C-4** | bleve no-CGo asserted, not enforced | **CLOSED** | FR-7.4 pins pure-Go `scorch`, forbids CGo leveldb/rocksdb, mandates `CGO_ENABLED=0` CI gate; M-6 folds in index lifecycle + 64-shard-mutex concurrency. **CGo-creep premise verified real**: `go.mod` L90 `mattn/go-sqlite3 // indirect`. `bleve` confirmed absent from `go.mod`/`go.sum` — genuinely new. |
> | **M-1** | Wrong tool name `recall` | **CLOSED** | §2/FR-7.3 keep `recall_memory` (= `RecallMemoryTool.Name()`). (Design-doc D6 says `recall`; spec correctly follows code.) |
> | **M-2** | "Greenfield" hid live data | **CLOSED*** | FR-7.6+§11 mandate migrating live `MEMORY.md`/`_retro.md`; `pkg/agent/memory.go::MemoryStore` confirms real data. *Caveat MIN-001: spec still self-contradicts in Edge-Cases/Regression and silently overrides design-doc D2. |
> | **M-3** | Cascade misattributed to Spec-1 | **CLOSED** | FR-8.2 states the `blocked_by` edge cleanup is "THIS spec's … distinct from Spec-1's project→task-file cascade (NOT attributed to Spec-1)." |
> | **M-4** | Validator only handled 2-node cycle | **CLOSED** | FR-8.2: rejects self-edge + 2-node + N-node cycles (full DAG check), drops orphan edges on load, depth-bounded. |
> | **M-5** | MinHash dedup destructive/unspecified | **CLOSED** | FR-7.5+§1: near-dups linked via `minhash.jsonl`, **not deleted**. |
> | **M-6** | bleve lifecycle/concurrency | **CLOSED** | FR-7.4: incremental-on-write, rebuild-on-corruption, bleve writer-lock + shard-lock, "no double-write races." |
> | **M-7** | Calendar surface undefined | **CLOSED** | FR-8.3 defines a SPA Calendar view rendering scheduled tasks/events/milestones; engine deferred to v0.2. |
>
> **Cross-spec grounding (Spec-3):** verified `TaskUpdateTool.SetOnComplete` (`pkg/tools/task.go`) wired in `pkg/agent/loop.go` to `taskExecutor.onTaskComplete`, and the generated `task_status_changed` WS frame (`contracts/components/schemas/TaskStatusChangedFrame.yaml`, `pkg/api/generated/asyncapi_types.gen.go`). §6 attribution is correct.
>
> ### Remaining non-blocking findings (round 2)
>
> - **MIN-001 (Inconsistency, FR-7.6 vs Edge-Cases/Regression/design-doc D2)** — The spec now mandates migration (FR-7.6/§11/US-3) but the **Edge-Cases bullet still says "Migrating off `MEMORY.md` → greenfield (no old MEMORY.md read)"** and **Regression note (1) says "Greenfield — no old MEMORY.md migrated"** — a direct self-contradiction that would re-introduce the M-2 data-loss bug if an implementer follows the wrong line. It also silently overrides design-doc D2/D33 ("no migration"). **Fix:** rewrite the Edge-Cases bullet and Regression (1) to match FR-7.6, and add one line noting the deliberate deviation from D2.
> - **MIN-002 (Incompleteness, FR-7.3)** — `recall_memory` today exposes only `query`+`limit` (memory.go L270-285); the design doc adds `room=private|project|both`/`scope`/`hops`. Two-room reads (US-1) need a room selector, but FR-7.3 neither pins nor defers it. **Fix:** state whether v0.1.0 `recall_memory` gains a `room`/`scope` param (pin the enum) or recall is room-implicit with the selector deferred.
> - **MIN-003 (Testability, FR-7.4/SC-5)** — the "no new CGo" CI gate has no SC/TDD anchor. **Fix:** add SC-5b (`CGO_ENABLED=0 … ./...` green with bleve present; no CGo-requiring dep added) + a CI/TDD row.
> - **OBS-001 (FR-7.2/7.3)** — existing `remember` hard-caps content at 4096 runes (memory.go L132); design-doc Q7 sets 8 KB soft / 64 KB hard. Spec is silent on which survives. Wikilink narratives will exceed 4096. State the v0.1.0 cap.
> - **OBS-002 (FR-7.2/7.3)** — current `remember` input is `category∈{key_decision|reference|lesson_learned}` (L107-111), but FR-7.2 frontmatter wants `type{8-enum}`+`tags[]`+`supersedes`. The tool's input→frontmatter mapping is unspecified; frontmatter can't be populated without it. Pin the v0.1.0 `remember` param set.
> - **OBS-003 (FR-8.3)** — Calendar surface names no route/wire-type/data-source. State whether it reads `GET /api/v1/board/tasks` (filtered by `start`/`due`) or needs a new contract-first endpoint (Constraint #8).
>
> ### Next action
> ```
> Verdict: PASS — spec ready for task decomposition. Run:
>   /taskify docs/internal/specs/v01-spec5-memory-tasks-calendar-spec.md
> ```
> (Recommend fixing MIN-001 first — it is an internal self-contradiction that can resurrect the M-2 data-loss bug — but it does not gate.)
>
> ---
> *Round-1 review (BLOCK) preserved below for the record.*
> ---

# Round 1 — original review (BLOCK)

- **Spec reviewed:** `docs/internal/specs/v01-spec5-memory-tasks-calendar-spec.md`
- **Reviewer mode:** adversarial spec-review (read-only). Input classified as **plan-spec** (has BDD Given/When/Then, FR-x, SC-x, Traceability Matrix).
- **Grounding base:** `pkg/memory/`, `pkg/tools/memory.go`, `pkg/agent/memory.go`, `go.mod`, `contracts/components/schemas/BoardTask*.yaml`, `pkg/boardtask/`, `docs/internal/architecture/ADR-019-v01-workspaces-foundation.md`, Spec-1 (`v01-spec1-workspace-rename-spec.md`, `project-task-management-level1-spec.md`), Spec-3 (`v01-spec3-agents-delegation-orchestrator-spec.md`).
- **Date:** 2026-06-13

---

## 1. Executive Summary

This spec is compact and well-traced on paper, but it ships on a **materially false grounding claim** that distorts the entire memory half of the work. The spec's §2 grounding table asserts the three tools `remember`/`recall`/`retrospective` are **"NEW … not present in `pkg/memory`/`pkg/sysagent/tools` today."** They are **already implemented and registered** in `pkg/tools/memory.go` (`RememberTool` → `"remember"`, `RecallMemoryTool` → `"recall_memory"`, `RetrospectiveTool` → `"retrospective"`), backed by `pkg/agent/memory.go`'s `MemoryStore` (a real `MEMORY.md` + day-partitioned retrospectives + a literal substring `SearchEntries`). This is a **rewrite/restructure of an existing subsystem**, not a greenfield add — and the spec's regression analysis, tool-naming, and "greenfield" assumptions are all built on the wrong premise.

The ADR itself (FR-7) correctly frames these tools as "**replacing** monolithic `MEMORY.md`" — so the spec is *less* grounded than the ADR it consumes. That is the headline defect.

**Findings:** 4 CRITICAL · 7 MAJOR · 5 MINOR · 3 OBSERVATION.

**Verdict: BLOCK.** The grounding error (C-1), the unpinned frozen-log schema (C-2), the unpinned frozen frontmatter schema (C-3), and the unspecified bleve no-CGo guarantee (C-4) are each disqualifying for a spec whose entire stated value is "structure + frozen formats, pinned now so v0.2 never backfills." You cannot freeze a format the spec never writes down.

---

## 2. Findings

| ID | Sev | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| C-1 | CRITICAL | Incorrectness / Inconsistency | §2 grounding table, §11, US-3 | **False "NEW" grounding.** The 3 tools already exist: `pkg/tools/memory.go` defines `RememberTool` (`"remember"`), `RecallMemoryTool` (`"recall_memory"`), `RetrospectiveTool` (`"retrospective"`), backed by `pkg/agent/memory.go::MemoryStore` (real `MEMORY.md` append + `sessions/YYYY-MM-DD/<id>_retro.md` retros + literal substring `SearchEntries`, lines 344-435). The spec says they are "not present today." This is a **restructure of a live subsystem**, with existing audit hooks, rate limiting (`MemoryRateLimiter`), and a `MemoryAccess`/`MemorySearcher` interface boundary. | Re-ground §2: state these tools EXIST and are being re-backed (MEMORY.md → rooms + per-memory files). Enumerate what is *kept* (tool names, audit events `memory.remember`, rate limiter, interface) vs *changed* (storage, search engine). Add regression coverage for the existing tool contract. |
| C-2 | CRITICAL | Infeasibility / Incompleteness | FR-7.5, US-5, BDD "frozen formats", NFR-1 | **The "frozen" log-record formats are never actually written down.** The spec's whole thesis is "freeze the schema now so v0.2 never backfills," yet it gives **no field list** for the `sessions/` firehose record, **no field list** for `counters.jsonl`, and **no schema** for `born_in`/`cited_in`. "Freeze the format" is unspecifiable when the format is absent. ADR-019 FR-7/NFR-7 demands the *complete* schema be pinned; this spec defers it ("pin … at impl"). A format frozen at impl-time by a single dev is exactly the un-reviewed pin NFR-1 exists to prevent. | Inline the full record schemas (every field, type, semantics, example line) for all three logs into the spec, or `$ref` a versioned schema file. Add a `schema_version` field to each record so v0.2 can detect format drift. Add a golden-file test asserting byte-level record shape. |
| C-3 | CRITICAL | Infeasibility / Ambiguity | FR-7.2, US-2, AM-4 | **The "full per-memory frontmatter" is also unpinned.** US-2/FR-7.2 require "all fields present even if unused" but the spec lists only `id, type, tags, links, confidence, status, supersedes, born_in, …` — the `…` is fatal. NFR-7 (the real NFR-1 guard) requires the *complete* schema pinned now; AM-4 punts it ("pin the full set at impl from the design doc"). Two engineers will pin two different field sets. | Enumerate the **complete, closed** frontmatter field set with types and "inert/active in v0.1.0" per field, sourced from `memory-redesign-2026-05.md`, directly in the spec. Add `TestMemory_FileFormat_FullFrontmatter` asserting the exact closed key set (fail on extra/missing keys). |
| C-4 | CRITICAL | Insecurity / Infeasibility | FR-7.4, US-4, AM-2 | **bleve's no-CGo guarantee is asserted, not enforced.** Constraint #2 forbids CGo. bleve's default index (`scorch`) is pure-Go, but bleve *can* pull CGo via optional kv-store backends (leveldb/rocksdb/cznic) behind build tags; an unpinned `go get` can transitively widen the build. The spec asserts "no CGo" with zero enforcement mechanism. Note the tree already carries a CGo SQLite driver (`mattn/go-sqlite3`, go.sum:204) as an indirect dep — proof that "indirect CGo creep" is real here. | Pin: (a) exact bleve module path + version; (b) the scorch index backend explicitly; (c) a CI guard — `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` must stay green (already a gate) PLUS a test/`go list` assertion that no leveldb/rocksdb/icu bleve subpackage is imported. Make "0 CGo deps added" a measurable SC, not prose. |
| M-1 | MAJOR | Inconsistency | §2, US-3 vs FR-7.3 | **Tool name mismatch: `recall` vs `recall_memory`.** The spec, US-3, FR-7.3, and BDD all call the tool `recall`. The existing registered tool is named **`recall_memory`** (`pkg/tools/memory.go:262`). Either the spec renames it (a breaking change to every agent policy/prompt referencing `recall_memory`, plus `core.go` seeds) — which must be stated and traced — or it keeps `recall_memory` and the spec is wrong. | Decide and state explicitly: keep `recall_memory` (update all spec references) OR rename to `recall` and add a finding/test for the rename's blast radius (agent tool policies, `coreagent/core.go`, prompts, docs). |
| M-2 | MAJOR | Inconsistency / Incompleteness | Regression §6 item (2), AM-3, §11 | **"Greenfield, no migration" contradicts an existing migrator and existing data.** `pkg/memory/migration.go::MigrateFromJSON` already migrates legacy `sessions/*.json`, and `pkg/agent/memory.go` actively reads/writes `MEMORY.md` for every existing install. The spec waves this away as "greenfield … existing-install `.jsonl` out of scope," but Omnipus is shipping software with real users on `main`. Dropping `MEMORY.md` as the store with no migration = **silent memory loss on upgrade**. | State the upgrade contract explicitly. Either (a) v0.1.0 migrates `MEMORY.md` → per-memory files (specify it, test it), or (b) explicitly accept memory loss on upgrade with a release-note warning and operator decision recorded in the ADR. "Greenfield" is not a truthful description of an in-place subsystem rewrite. |
| M-3 | MAJOR | Inconsistency (cross-spec) | §6 cross-spec, FR-8.2 vs Spec-1 | **Two different "cascade" semantics collide.** Spec-1 cascade = delete a **project** → delete its **task files** (by `project_id`) + scrub `project_session_links.jsonl`. Spec-5 FR-8.2 cascade = delete a **task** → clean its `blocked_by` **edges** + surface dependents. These are different operations on different entities, but the spec calls both "cascade" and claims FR-8.2 is "carried from Spec-1's task semantics — operator Q1." Spec-1 explicitly defers "full cascade = Spec-5" (`v01-spec1-workspace-rename-spec.md:216`), so the handoff exists — but the **task-delete edge-cleanup semantics are nowhere in Spec-1** to be "carried." | Stop attributing FR-8.2 to Spec-1. Define task-delete `blocked_by` cleanup as **owned by Spec-5**, specify the exact algorithm (remove inbound + outbound edges referencing the deleted task; what status do now-unblocked dependents transition to?), and reconcile with Spec-1's project-cascade so a project-delete that removes tasks also cleans cross-task edges. |
| M-4 | MAJOR | Incompleteness | FR-8.2, BDD "cycle rejected", §3 US-6 | **`blocked_by` validator is underspecified beyond the 2-node cycle.** The only scenario is A↔B. Unspecified: (a) self-edge (`task blocked_by self`); (b) N-node cycles (A→B→C→A); (c) the **orphan drop "on load"** (FR-8.2(c)) vs reject "on write" — load-time mutation of persisted task files is a silent data rewrite with no test; (d) max DAG depth / fan-out limits (DoS via a 10k-edge chain the Orchestrator must traverse, Spec-3). (e) `blocked_by` referencing a task in a *different* workspace/project — allowed or rejected? | Add scenarios + tests for self-edge, N-cycle, orphan-on-load (and whether it rewrites the file), and a depth/breadth bound. Specify cross-workspace edge legality. The Orchestrator (Spec-3) traverses this DAG on every `task_status_changed` — an unbounded/cyclic DAG is a liveness bug there. |
| M-5 | MAJOR | Incompleteness | FR-7.5, edge cases | **MinHash dedup is named but not specified.** "MinHash near-duplicate memory → deduped on write" gives no threshold, no shingle size, no signature width, no tie-break (which copy wins — newer? higher-confidence?), and no behaviour when a dedup *collision* is a false positive (two genuinely-different memories deduped → silent data loss). Dedup-on-write that drops a memory is destructive and must be specified to the parameter. | Specify: shingle/token granularity, number of hash functions, similarity threshold, the keep/drop rule, and whether dedup is hard-drop or soft-link (`supersedes`). Add a false-positive guard or make it a recall-time merge instead of a write-time drop. Add `TestMinHash_DedupOnWrite` with both a true-dup and a near-miss-not-dup dataset. |
| M-6 | MAJOR | Incompleteness / Inoperability | §4 integration, FR-7.4 | **bleve index lifecycle/concurrency unspecified.** The existing memory subsystem uses a 64-shard mutex + advisory flock. bleve is a stateful on-disk index opened by a long-lived handle. Unspecified: who owns the index handle (one per process? per workspace? per room?); is it safe under the existing concurrent write model; what happens on concurrent `remember` (write `.md` + index) vs `recall` (read index); index corruption detection (the spec says "rebuild" but not how corruption is *detected*); rebuild cost/blocking on a large corpus. | Specify the index ownership model, concurrency contract (does index write join the shard mutex?), corruption-detection trigger, and whether rebuild is lazy/blocking/background. Add a concurrency test (`-race`) for concurrent remember+recall. |
| M-7 | MAJOR | Infeasibility | TDD #10, US-7, SC-8 | **Calendar E2E test depends on an undefined surface and undefined data source.** TDD #10 is an E2E test that "Calendar shell renders scheduled tasks/events/milestones," but the spec defines **no** calendar data contract (where do "events" and "milestones" come from — `tasks/` with a `due`? a new file? the existing `milestone` schema?), no route, no API. `pkg/calendar` confirmed absent (greenfield). An E2E test cannot be written against an unspecified surface. | Define the calendar's read model: which existing records it renders (tasks with `start`/`due`, milestones from the milestone schema), the route, and whether any new contract type is needed (if so → contract-first per Constraint #8). Then the E2E is writable. |
| m-1 | MINOR | Ambiguity | US-1 / D19 | "no per-session sessions dir (D19)" references decision **D19** with no link or inline statement. Reader can't verify the rule. | Inline D19's content or cite its source doc + section. |
| m-2 | MINOR | Ambiguity | FR-7.1 | "agent-global" for the private room conflicts conceptually with the workspace-keyed model — is a private memory visible across all workspaces for that agent? State the visibility rule explicitly. | Add one sentence: "Private room is agent-scoped and workspace-independent; shared room is workspace-scoped." |
| m-3 | MINOR | Incompleteness | FR-8.1 | Field types for `start·due·recurrence` are unspecified. `recurrence` especially — RFC-5545 RRULE string? a struct? free text? This is an *inert* field (NFR-7) so its schema must still be fully pinned now. | Pin the wire types in `BoardTask.yaml` (additive): `start`/`due` as `date-time`, `recurrence` as a defined string format (e.g. RRULE) or a pinned object; `blocked_by` as `array<string>` (task IDs). Contract-first. |
| m-4 | MINOR | Inconsistency | SC-5 / Holdout H5 | "0 embedding/SQLite deps added" — but `modernc.org/sqlite` (pure-Go) and `mattn/go-sqlite3` (CGo, indirect) are **already** in go.mod (WhatsApp/Matrix). The success criterion as worded ("0 sqlite deps") is already false at baseline. | Reword to "adds no NEW sqlite/embedding dep" and pin the baseline (diff against current go.mod), so the check is meaningful. |
| m-5 | MINOR | Ambiguity | FR-7.3 | "register in the tool catalog; contract if boundary-crossing" leaves it open whether these tools cross the gateway boundary. AM-1 says "confirm at impl." For a spec claiming contract-first discipline, leaving the contract question open is a gap. | Determine now: the tools are agent-internal (no REST/WS surface) → no contract; or they surface results to the SPA → contract required. State it. |
| O-1 | OBSERVATION | Overcomplexity | FR-7.5 | Three separate frozen log streams (`sessions/` firehose, `counters.jsonl`, `born_in`/`cited_in`) are introduced purely so a *future* (v0.2) ranking engine reads them with no backfill. None are read in v0.1.0. Consider whether all three are needed now, or whether one (`counters.jsonl`) suffices and the others can be derived. Each frozen format is permanent maintenance surface. | Justify each of the three logs against a v0.2 reader, or defer the ones a v0.2 reader can reconstruct from `.md` + sessions. |
| O-2 | OBSERVATION | Overcomplexity | FR-7.4 | bleve is a heavy dependency (its own index format, mergers, analyzers) for what the existing system does with a literal substring scan over (presumably) small per-agent corpora. Confirm the corpus size justifies a full FTS engine vs an in-memory inverted index. | Sanity-check expected memory-count scale; if small, document why bleve over a simpler scan. (ADR FR-7 already accepts bleve — this is a flag, not a block.) |
| O-3 | OBSERVATION | Inoperability | whole spec | No observability story for the new memory paths (metrics on index size, dedup-drop count, recall latency, log-write failures). For a "firehose" that v0.2 depends on, a silent log-write failure = corrupt v0.2 ranking. | Add a structured-log/counter for log-write failures and dedup drops so the v0.2 dependency isn't built on silently-lossy logs. |

---

## 3. Structural Integrity (plan-spec checks)

| Check | Result | Note |
|---|---|---|
| Every US has ≥1 acceptance scenario | PASS | US-1..7 each have ACs. |
| Every BDD scenario has `Traces to:` | PASS | All 7 trace. |
| Every BDD has a TDD test | PARTIAL | "frozen formats" BDD → test #6, but the *format* it tests is undefined (C-2). MinHash test #7 has no BDD scenario. |
| Every FR in traceability matrix | PASS | FR-7.1..FR-8.3 mapped. |
| Every BDD in traceability matrix | FAIL | The "memory file full frontmatter" and "minhash dedup" coverage is thin; MinHash (#7) appears in TDD but not as a BDD scenario nor a matrix row. |
| Test datasets cover boundary/edge/error | PARTIAL | `blocked_by` covers cycle+delete but not self-edge/N-cycle/orphan-load (M-4); MinHash has no near-miss dataset (M-5). |
| Regression impact addressed | FAIL | Regression section is built on the false "greenfield/NEW" premise (C-1, M-2); does not address the existing live tools, audit events, rate limiter, or `MemoryStore`. |
| Success criteria measurable, no subjective language | PARTIAL | SC-5 baseline-wrong (m-4); SC-8 "Calendar shell renders" is untestable without a defined surface (M-7). |

---

## 4. Test Coverage Assessment

- **Missing negative tests:** no test for dedup false-positive, no test for orphan-edge-on-load file rewrite, no test for index corruption *detection* (only rebuild).
- **Missing concurrency tests:** the subsystem is concurrent (64-shard mutex + flock) and adds a stateful bleve handle; no `-race` test for concurrent remember+recall (M-6).
- **Missing boundary tests:** `blocked_by` self-edge, N-node cycle, depth bound (M-4); MinHash near-miss (M-5); empty workspace / first-write room creation is in edge cases but has no TDD row.
- **Untestable-as-written:** TDD #6 (frozen formats) and #2 (frontmatter) cannot assert a schema the spec doesn't pin (C-2, C-3); TDD #10 (Calendar E2E) has no surface to drive (M-7).
- **Regression blind spot:** the existing `pkg/tools/memory_tools_test.go` and `pkg/agent/memory.go` parsing/search tests are not referenced as must-preserve-or-knowingly-replace.

---

## 5. STRIDE Threat Summary

| Component | Threat | Note |
|---|---|---|
| `remember` (write `.md` + index + dedup) | **Tampering / DoS** | Dedup-on-write can silently drop a real memory (M-5). No size cap stated on per-memory file or total corpus → disk-fill DoS. Existing tool has a 4096-char cap + rate limiter — confirm carried forward. |
| bleve index | **DoS / Info disclosure** | Corrupt/poisoned index; rebuild cost on large corpus blocks recall (M-6). Index file is derived but contains memory contents — confirm same 0600 perms as source. |
| frozen logs (`sessions/`, `counters.jsonl`, `born_in`/`cited_in`) | **Repudiation / Info disclosure** | A firehose log of every session/recall/citation is a sensitive access trail. Perms, retention, and whether it contains memory *content* vs IDs are unspecified. `project_session_links.jsonl` precedent = 0600 (session IDs sensitive). |
| `blocked_by` DAG | **DoS** | Unbounded/cyclic DAG → Orchestrator (Spec-3) liveness/CPU on every status change (M-4). |
| shared workspace room | **Elevation / Info disclosure** | Multi-agent shared room: can agent X read/overwrite agent Y's memory in the shared room? No isolation/authz statement. Spec-1 "removed all access control" — confirm shared-room writes are intended to be fully cross-agent. |
| Calendar shell | n/a | Render-only; read model undefined (M-7). |

---

## 6. Unasked Questions (for the author)

1. The 3 tools exist today — is this a **rename** (`recall_memory`→`recall`) or a **re-backing** keeping names? What is the blast radius on agent tool policies and `coreagent/core.go`?
2. On upgrade of an existing install with a populated `MEMORY.md`, what happens to that data — migrated, abandoned, or dual-read? (M-2)
3. What is the **complete, closed** frontmatter field set, with each field's type and inert/active status? (C-3)
4. What are the **exact** record schemas for `sessions/`, `counters.jsonl`, `born_in`, `cited_in` — every field, type, and a `schema_version`? (C-2)
5. Which bleve module/version and which index backend, and how is no-CGo *enforced* in CI rather than asserted? (C-4)
6. Does dedup hard-drop or soft-supersede, and what is the false-positive guard? (M-5)
7. Who owns the bleve handle and how does index write interact with the 64-shard mutex / flock? (M-6)
8. What is the Calendar's read model and route, and does it introduce a new contract type? (M-7)
9. In the shared workspace room, is memory cross-agent read/write by design (no isolation)?
10. Does orphan-edge "drop on load" rewrite the persisted task file, and is that idempotent/safe under concurrent access? (M-4)

---

## 7. Verdict

**BLOCK.**

Four CRITICAL findings, each independently disqualifying for a "freeze the formats now" foundation spec:
- **C-1** — the memory tools are not new; the grounding is false and the regression analysis is built on it.
- **C-2** — the frozen log-record formats are never written down (cannot freeze an absent schema).
- **C-3** — the "full" frontmatter schema is left open (`…` / "pin at impl").
- **C-4** — bleve's no-CGo guarantee is asserted, not enforced (and CGo creep is demonstrably real in this tree).

Plus 7 MAJOR findings (tool-name mismatch, false greenfield/migration, cascade-semantics collision, under-specified validator, unspecified MinHash, unspecified index lifecycle, untestable Calendar E2E).

### Next action

```
Verdict: BLOCK

Review written to: docs/internal/specs/v01-spec5-memory-tasks-calendar-spec-review.md

To address these findings, run:
  /plan-spec --revise docs/internal/specs/v01-spec5-memory-tasks-calendar-spec.md docs/internal/specs/v01-spec5-memory-tasks-calendar-spec-review.md
```

Priority order for the revision: C-1 (re-ground against the existing tools) first — it changes the framing of the whole memory half — then pin the two frozen schemas (C-2, C-3), then the bleve CGo enforcement (C-4), then the MAJORs.
