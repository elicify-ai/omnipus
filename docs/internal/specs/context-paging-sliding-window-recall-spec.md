# Spec: Context paging — sliding-window + recall (replaces reactive compaction)

- **Source ADR:** `docs/internal/architecture/ADR-028-context-paging-sliding-window-recall.md` (rev. 3, post re-grill)
- **Status:** Draft for `/grill-spec` → `/taskify` → implement
- **Priority:** P0 (hottest path — every agent turn)
- **Branch target:** `hotfix/v0.1.1` (operator-directed; v0.3-flavoured, noted)

---

## 1. Overview

**Actors.** The **agent loop** (assembles context each turn); the **LLM/agent** (reads the
window + breadcrumb, calls `recall_conversation`); the **operator/end-user** (whose long
conversations must not silently lose content); the **retention sweeper**.

**Problem.** Reactive compaction (`forceCompression`) drops the oldest ~50% of turns on
overflow and leaves only a `[dropped N]` marker — content is gone from the working context.
Long/heartbeat sessions grow then drop blindly.

**Solution (per ADR-028).** Context paging: the live window is RAM, the append-only
`context.jsonl` is disk, and a new `recall_conversation` tool is the page-fault handler.
Over-budget **evicts** the oldest whole Turn(s) from the live window (advancing `meta.Skip`,
**zero bytes deleted**); the full log persists on disk (once `Compact`-on-`Save` is stopped
from dropping skipped lines); a prominent heuristic **breadcrumb** tells the model what slid
out and that it can page it back **verbatim**.

**In scope:** `windowTrim` (Skip-based, Turn-aligned) replacing `forceCompression`; stop
`Compact` dropping skipped lines; sliding-window `BuildMessages` + breadcrumb; the heuristic
breadcrumb builder; `recall_conversation` (query/turn_range/time, session-scoped, bounded,
generalized BM25) + a **transient native recall span** (Design B: recalled Turns re-injected as
provider messages with rewritten IDs, non-persisted, dropped-first under pressure); model-switch
re-window; `.context/` retention (remove exemption); hard-delete `forceCompression` + `[dropped N]` path.

**Out of scope:** any LLM-based summarization; a persistent recall index; changing the pinned
core (system prompt/memory) content; the ledger/baton continuous-agent designs; `transcript.jsonl`
schema; cross-session recall.

---

## 2. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `AgentLoop.forceCompression` (`loop.go:7411`) | **delete** | Replaced by `windowTrim`; drop the `[dropped N]` summary write. |
| `isOverContextBudget` (`context_budget.go:161`) | **call** | Over-budget check `msgTokens+toolTokens+maxTokens > contextWindow`; the `windowTrim` trigger. Reused unchanged. |
| `parseTurnBoundaries` (`context_budget.go:22`) | **reuse** | Returns Turn-start slice indices of a `[]providers.Message`. The canonical Turn-boundary detector `windowTrim` uses to compute the window-relative `keepLast`. (NB: `splitHistoryAtTurnMidpoint` at `:187` is the *midpoint* splitter — **retired**, not reused.) |
| forceCompression call sites `loop.go:5078/5851/5981` | **modify** | Call `windowTrim` instead. (Verified line numbers — MIN-01.) |
| `JSONLStore.TruncateHistory` (`jsonl.go:323`) | **call** | The Skip-advance primitive: keeps the last `keepLast` **lines** (`meta.Skip = meta.Count − keepLast`), zero bytes deleted. Its `keepLast < effective` guard (`:349-352`, `effective = Count − Skip`) already interprets `keepLast` **relative to the current live window** — so `windowTrim` passes a window-relative count (see FR-001). |
| `JSONLStore.GetHistory` (`jsonl.go:266`) | **call** | Reads `readMessages(path, meta.Skip)` — the live window (post-Skip). Used by `windowTrim` (window sizing) and `BuildMessages`. **NOT** used by recall (recall must see evicted turns → uses the new `ReadArchive`, FR-016). |
| `readMessages(path, skip)` (`jsonl.go:124`, **private**) | **wrap** | The line reader; `skip=0` reads the full archive. Exposed via new `ReadArchive` (FR-016). |
| `JSONLStore.addMsg` (`jsonl.go:214`, `O_APPEND`) | **modify** | Now stamps a per-line write timestamp via a flat wrapper `storedMessage{ providers.Message; TS int64 }` (FR-017). Still append-only. |
| `JSONLStore.Compact` (`jsonl.go:398`) | **remove from Save path** | Physically drops skipped lines. The **call** is removed from `UnifiedStore.Save` (`unified.go:757`) and `session/jsonl_backend.go:75`; the function is kept but **test-only, never auto-invoked** (FR-005, OBS-04). |
| `ContextBuilder.BuildMessages` (`context.go:717`) | **modify** | Assemble: pinned core → breadcrumb block (FR-007) → **transient recall span** (FR-019, native re-injected messages) → Skip-trimmed sliding window; stop appending the compaction summary. Gains a recall-span input from the agent-loop state. |
| `AgentLoop.handleModelSwitch` (`loop.go:7659`) | **modify** | Re-window via the **same** `windowTrim` (inherits FR-003 floor/termination on downsize); delete the `splitHistoryAtTurnMidpoint`+summary path. |
| `UnifiedStore.ReadTranscript` (`unified.go:640`) | **do NOT use for recall** | Lossy UI feed (no tool_call_id / tool results); retention-deleted. Its `TranscriptEntry.Timestamp` (`daypartition.go:147`) is the *only* pre-existing per-turn timestamp — but is NOT recall-readable; hence FR-017 stamps `context.jsonl` itself. |
| `providers.Message` (`protocoltypes/types.go:85`) | **read** | `Role/Content/Media/ReasoningContent/SystemParts/ToolCalls/ToolCallID` — **no timestamp, no agent-id**. → recall is session-scoped (FR-013) and needs FR-017's stamp for `time` mode/breadcrumb. |
| `RetentionSweep` (`retention_sweep.go:25`) | **modify** | Remove the `.context/` exemption (`:55,74`); ModTime-based (`:84`) → active sessions spared. |
| `rankRetrosBM25`/`retroTokenize` (`retro_bm25.go`) | **generalize** | Hard-typed to `Retro`; extract a BM25 core over a `(text, timestamp)` document so `recall_conversation` can rank `providers.Message` turns. |
| `tools.NewRecallMemoryTool` (`instance.go:210`) | **pattern** | Registration + description-cross-reference site for the new tool. |

### Impact Assessment
| Symbol Modified | Risk | Direct Dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `BuildMessages` | **CRITICAL** | every turn of every agent/channel | all chat/heartbeat/e2e |
| `forceCompression`→`windowTrim` + 3 call sites | **CRITICAL** | the turn loop over-budget path | long-session behavior, model-switch |
| `Compact`/`Save` (stop dropping skipped) | **HIGH** | session persistence; on-disk size | recall correctness; retention |
| `handleModelSwitch` | **MEDIUM** | model-switch turns | multi-model sessions |
| `RetentionSweep` (`.context/`) | **MEDIUM** | nightly sweep | disk usage; recall reach |
| BM25 `bm25Score` core | **LOW-MED** | `rankTurnsBM25` (new); `rankRetrosBM25` (must keep passing) | retro recall |
| `addMsg` +`TS` (FR-017) | **LOW** | every session write; all readers of `context.jsonl` | backward-compat parse of legacy lines |
| new `ReadArchive` (FR-016) | **LOW** | `recall_conversation`, breadcrumb builder | none (additive read) |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| Turn assembly (`BuildMessages`→provider) | Where the window + breadcrumb are composed. |
| Over-budget guard (`isOverContextBudget`→`windowTrim`) | Where eviction happens. |
| Tool call (`recall_conversation.Execute`) | Where paging happens. |
| Model switch (`handleModelSwitch`) | Re-window on model change. |
| Retention sweep | `.context/` bounding. |

**Available Reference Patterns:** None in `docs/reference/` for context paging; the BM25 pattern is the just-added `retro_bm25.go`. Pattern reference: **MemGPT/Letta context paging** (external).

---

## 3. User Stories & Acceptance Criteria

### US-1 (P0) — Lossless eviction on overflow
As the agent loop, when a turn would exceed the model window, I evict the **oldest whole
Turn(s)** from the live window until it fits, **without deleting anything from disk**, so no
content is ever lost.
**Why P0:** it is the core of the change and the fix for the lossy status quo.
**Independent Test:** overflow a session; assert the live window shrank at a Turn boundary,
`meta.Skip` advanced, the on-disk `context.jsonl` line count is **unchanged**, and no
`[dropped N]` marker was written.
1. **Given** a session whose history + tools + reserve exceed `contextWindow`, **When** the turn is assembled, **Then** `windowTrim` advances `meta.Skip` to keep the largest suffix of whole Turns that fits, and deletes zero bytes on disk.
2. **Given** eviction runs, **When** it picks the cut point, **Then** the cut lands on a **Turn boundary** — never mid tool-call/tool-result.
3. **Given** a single Turn alone exceeds the window (e.g. one massive tool result), **When** eviction runs, **Then** it keeps only the most recent user Turn (last-resort), and does not loop forever.
4. **Given** eviction runs, **Then** no `[Emergency compression dropped N…]` marker is written to the summary.

### US-2 (P0) — Sliding-window assembly
As the agent loop, `BuildMessages` replays only the current sliding window (already Skip-trimmed)
+ the pinned core + the breadcrumb, so context stays under budget without full-history replay.
**Why P0:** the assembly change that realizes the window.
**Independent Test:** with an evicted session, assert the provider messages contain only the
window turns (not the evicted ones) + a system message + the breadcrumb.
1. **Given** an evicted session, **When** `BuildMessages` runs, **Then** the returned messages contain the pinned system prompt, the breadcrumb, and only the windowed (post-Skip) turns — not the evicted turns.
2. **Given** no eviction has occurred, **When** `BuildMessages` runs, **Then** the breadcrumb is absent (or empty) and all turns are present (behavior identical to a short session).

### US-3 (P0) — Prominent boundary breadcrumb
As the LLM, I see a prominent, LLM-free breadcrumb naming what slid out and that
`recall_conversation` can page it back, so I know to recall when needed.
**Why P0:** the sole mitigation for silent loss (ADR M-4).
**Independent Test:** after eviction, assert `BuildMessages` includes a breadcrumb block listing
turn-ranges + timestamps + first-line snippets + entities, and naming `recall_conversation`.
1. **Given** turns 1–20 were evicted, **When** `BuildMessages` runs, **Then** the breadcrumb includes `turns 1–20`, a relative timestamp, a ≤80-char verbatim snippet of the chunk's first user line, and any cheap entities.
2. **Given** the breadcrumb is built, **Then** it explicitly names the `recall_conversation` tool as the way to retrieve the evicted turns.
3. **Given** the breadcrumb building runs, **Then** it makes **no LLM call**.
4. **Given** many evictions, **When** the breadcrumb would exceed its cap (~1000 tokens), **Then** it keeps the most-recent-first pointers up to the cap and notes "+K earlier ranges".

### US-4 (P0) — `recall_conversation` tool
As the LLM, I call `recall_conversation(query | turn_range | time)` to retrieve **verbatim,
provider-valid** turns from **this session**, bounded so the result never re-overflows the window.
**Why P0:** the page-fault handler; without it the design has no retrieval.
**Independent Test:** evict turns containing a nonce; call each mode; assert the nonce turn(s)
return with role/content/tool fields intact, session-scoped, within the bound.
1. **Given** an evicted turn containing "NONCE", **When** `recall_conversation(query:"NONCE")` runs, **Then** it returns the matching turn(s) BM25-ranked, verbatim, provider-valid.
2. **Given** `turn_range: 5-10`, **When** recall runs, **Then** it returns turns 5–10 verbatim in order.
3. **Given** `time: {from,to}`, **When** recall runs, **Then** it returns turns whose timestamps fall in the window.
4. **Given** a result that would exceed the output bound (≤ 8 turns AND ≤ 4000 tokens), **When** recall runs, **Then** it truncates and appends "N more — narrow the query or use turn_range".
5. **Given** a handoff session with multiple agents, **When** recall runs, **Then** it returns this session's turns regardless of which agent produced them (session-scoped), and never turns from another session.
6. **Given** an empty query, **Then** recall returns a clear error (not a full-transcript dump).

### US-5 (P1) — Append-only archive preserved
As the system, the on-disk `context.jsonl` retains evicted (skipped) turns — `Compact`-on-`Save`
no longer drops them — so recall can reach any turn within the retention window.
**Why P1:** enables US-4 durability + the ADR's "no data loss" safety.
**Independent Test:** evict, then `Save`; assert the on-disk line count is unchanged and a
recall of an evicted turn still succeeds after a save.
1. **Given** turns were evicted (Skip advanced), **When** `Save` runs, **Then** the on-disk `context.jsonl` still contains the skipped lines (no Compact drop).
2. **Given** a saved, evicted session is reloaded, **When** `recall_conversation` targets an evicted turn, **Then** it is still retrievable.

### US-6 (P1) — Model-switch re-window
As the agent loop, switching the model re-fits the sliding window to the new model's budget,
with no summary.
1. **Given** a switch to a smaller-window model, **When** the next turn assembles, **Then** `windowTrim` re-fits the window to the new budget; no summary is written.
2. **Given** a switch to a **larger**-window model, **Then** previously-evicted turns **stay evicted** — the `Skip` cursor only moves forward; the extra room is used by new turns, and the model pages older turns via `recall_conversation` if needed. `[CONFIRMED: leave evicted, Skip forward-only]`

### US-7 (P1) — Bounded `.context/` retention
As the retention sweeper, I sweep idle sessions' `.context/` at the retention window so append-only
archives don't grow unbounded, while never touching active sessions.
1. **Given** a session idle past `session_days`, **When** the sweep runs, **Then** its `.context/context.jsonl` is removed (exemption gone).
2. **Given** an active session (written this turn), **When** the sweep runs, **Then** its `.context/` is spared (fresh ModTime > cutoff).

### US-8 (P1) — Decommission compaction
As a maintainer, `forceCompression`, `splitHistoryAtTurnMidpoint`'s midpoint split, and the
`[dropped N]` marker path are deleted (hard replace, no flag).
1. **Given** the codebase after this change, **Then** `forceCompression` and the `[Emergency compression dropped…]` string do not exist; grep is clean.
2. **Given** an old session with a legacy `[dropped N]` marker in its persisted summary, **When** it loads, **Then** it still works (the marker is inert).

### Edge Cases
- Single Turn > window → keep last user Turn (US-1.3).
- Tool-call/tool-result pair at the boundary → never split (US-1.2).
- Session with zero evictions → no breadcrumb, full window (US-2.2).
- Recall on a session with no evictions → returns from the (small) log normally.
- Recall result larger than bound → truncate + hint (US-4.4).
- Model never calls recall → breadcrumb gist only (accepted risk; measured by holdout eval H-2).
- Concurrent turn + sweep on the same session → sweep spares active (US-7.2).
- `keepLast` computed as line-count vs Turn boundary mismatch → Turn-aligned (US-1.2).

---

## 4. Behavioral Contract / Non-Behaviors / Integration Boundaries

### Behavioral Contract
- When a turn exceeds the window, the system evicts oldest whole Turns from the live window (Skip-advance) until it fits, deleting nothing on disk.
- When turns are evicted, the system emits a prominent LLM-free breadcrumb naming `recall_conversation`.
- When the model calls `recall_conversation`, the system returns bounded, verbatim, provider-valid, session-scoped turns.
- When `Save` runs on an evicted session, the system keeps skipped lines on disk.
- When the model switches, the system re-windows for the new budget without summarizing.
- When a session is idle past retention, the sweep removes its `.context/`; active sessions are spared.

### Explicit Non-Behaviors
- The system MUST NOT summarize evicted content (no LLM call on eviction) — the whole point is losslessness.
- The system MUST NOT delete bytes from `context.jsonl` on eviction — only advance `Skip`.
- The system MUST NOT let `recall_conversation` return another **session's** turns — session isolation.
- The system MUST NOT return an unbounded recall result that re-overflows the window.
- The system MUST NOT tear a tool-call/tool-result pair at an eviction boundary (provider-invalid).
- The system MUST NOT read `transcript.jsonl` for recall replay (lossy; retention-deleted).
- The system MUST NOT add a `context.mode` flag or retain `forceCompression` (hard replace).
- The system MUST NOT move `meta.Skip` backward (no window re-expansion on model upsize) — recall is the only path back to evicted turns.
- The system MUST NOT auto-invoke the file-GC `Compact` from any `Save` path — evicted lines are retained on disk until the retention sweep.
- The system MUST NOT return a partial Turn from recall (no lone `tool` result / torn pair); recall re-injects **whole Turns as native messages with rewritten IDs** and MUST NOT re-execute the original tool calls (historical exchanges carry their recorded results).
- The system MUST NOT persist the recall span to `context.jsonl` (it is a transient in-memory overlay — no disk duplication, no re-eviction).
- The system MUST NOT let a recall span cause eviction of real window Turns — the span is dropped first under budget pressure.
- The system MUST NOT treat `context.jsonl` as a Constraint #8 wire type — the `TS` stamp is internal persistence, no contract regeneration.

### Integration Boundaries
- **LLM provider:** consumes the assembled window; a torn tool pair → 400. Failure behavior: eviction must keep the message set provider-valid every turn.
- **Session store (`JSONLStore`/`UnifiedStore`):** `TruncateHistory`/`GetHistory` (real; reused); `addMsg` (real; +`TS` stamp, FR-017); new `ReadArchive` (FR-016); `Save` (real; `Compact` call removed, FR-005). Dev: real store in integration tests.
- **Retention sweep:** real; `.context/` exemption removed.

---

## 5. Functional Requirements

- **FR-001 (US-1):** `windowTrim(agent, sessionKey)` MUST compute the cut **in live-window space** and pass a **window-relative** `keepLast` to `TruncateHistory`, per this exact algorithm (MAJ-03):
  1. `window := GetHistory(sessionKey)` — the current post-Skip live window (`[]providers.Message`).
  2. `boundaries := parseTurnBoundaries(window)` — Turn-start indices (`context_budget.go:22`).
  3. `budget := contextWindow − maxTokens − ⌈0.05·contextWindow⌉` — the **5%-headroom target** (of `contextWindow`) so a normal next turn doesn't immediately re-trim.
  4. If a recall span (FR-019) is active and the turn is over budget, **drop the whole span first** and re-check; only if still over budget proceed to evict window Turns. When sizing, subtract any still-active `recallSpanTokens` from `budget`. Then choose the **smallest** boundary index `b` such that `estimateMessageTokens(window[b:]) + estimateToolDefsTokens(toolDefs) + recallSpanTokens ≤ budget`. (Evict oldest Turns; equivalent to dropping one Turn per pass and re-checking.)
  5. `keepLast := len(window) − b`; call `TruncateHistory(ctx, sessionKey, keepLast)`.
  Because `TruncateHistory` interprets `keepLast` relative to the current live window (`effective = meta.Count − meta.Skip`, `:349-352`), and Turn boundaries are line boundaries, `Skip` advances **exactly** to a Turn start — never mid-Turn, even when `Skip > 0` already. `[CONFIRMED: 5% slack, one-Turn-per-pass]`
- **FR-002 (US-1.2):** The cut index `b` MUST be a value returned by `parseTurnBoundaries(window)` (never an arbitrary line), so the new window always begins with a `role:"user"` message and no orphaned `tool_call_id`. `windowTrim` MUST NOT use `splitHistoryAtTurnMidpoint` (retired).
- **FR-003 (US-1.3):** If a single Turn exceeds the window, `windowTrim` MUST keep only the most-recent user Turn and terminate (no infinite loop).
- **FR-004 (US-1.4/US-8):** `windowTrim` MUST NOT write a `[dropped N]` marker; `forceCompression` and that marker path MUST be deleted.
- **FR-005 (US-5):** The file-GC `Compact` call MUST be **removed from the `Save` path** (`unified.go:757` and `session/jsonl_backend.go:75`) so evicted (skipped) lines are never physically deleted from disk. The `JSONLStore.Compact` function itself is **retained but test-only** — it MUST have **no production caller and no operator entrypoint** (OBS-04); a `// context-paging: do not call from any Save path` guard comment MUST sit on the function, and no `omnipus` CLI command may invoke it. The **retention sweep** is the sole deleter of the `.context/` archive. `[CONFIRMED: remove Compact from Save path, keep the function test-only]`
- **FR-006 (US-2):** `BuildMessages` MUST replay the pinned core + breadcrumb + the Skip-trimmed window only; it MUST NOT append the compaction summary.
- **FR-007 (US-3):** A breadcrumb builder MUST produce, LLM-free, a **single prominent block** placed **immediately after the pinned core** (before the sliding window) in `BuildMessages`, using this literal template (MIN-03):
  ```
  ## Earlier in this conversation (evicted from the live window)
  Use the recall_conversation tool to read any of these verbatim.
  - turns {A}–{B} · {relTime} · "{snippet}" · {entities}
  - … (most-recent-first)
  {+K earlier ranges}
  ```
  Per evicted chunk: `turns A–B`; `relTime` = a relative timestamp derived from the chunk's **first line `TS`** (FR-017) — e.g. "2h ago"; `snippet` = the ≤80-char verbatim first-user-line of the chunk; `entities` = **quoted strings, file paths, and multi-word runs of ≥2 consecutive Capitalized tokens that are not sentence-initial** (single Capitalized words and sentence-initial words are excluded — MIN-02). It MUST name `recall_conversation`; MUST cap the whole block at **~1000 tokens** (most-recent-first, collapse the tail to `+K earlier ranges`). Lines whose `TS==0` (legacy, pre-FR-017) render `relTime` as `earlier`.
- **FR-008 (US-4):** A new `recall_conversation` tool MUST support `query` (BM25), `turn_range` (explicit slice), and `time` (`{from,to}` over the per-line `TS`, FR-017); it MUST read via the **`ReadArchive` full-archive reader (FR-016), never `GetHistory`**, so it reaches evicted turns. Its **atomic unit is a whole Turn** (never a lone message): turns are grouped via `parseTurnBoundaries`; `query` ranks **Turns** (Turn score = aggregate BM25 over the Turn's messages); `turn_range`/`time` select whole Turns. Recall output is re-injected into the next context assembly as a **transient native recall span** of reconstructed provider messages (Design B, FR-019): the selected whole Turns are spliced back as their original `user`/`assistant`/`tool` messages, with `tool_call_id`s **rewritten** to fresh namespaced IDs (`recall_<archiveIdx>_<n>`) consistently across each `assistant` call and its matching `tool` result — so every re-injected pair is complete and no ID collides with the live window (MAJ-04). Re-injecting a historical tool exchange **executes nothing** (side effects only occur on the model's *new* output; the recorded `tool` result is present, so the provider sees a completed exchange). It MUST be **session-scoped** and registered like `recall_memory` (`instance.go`).
- **FR-009 (US-4.4/NFR-5):** `recall_conversation` output MUST be bounded — default ≤ **8 turns** AND ≤ **4000 tokens** (whichever first); `turn_range` ≤ **50 turns** / ≤ **8000 tokens**; overflow → drop **whole Turns from the tail** + "N more — narrow the query or use turn_range". Token counts use `estimateMessageTokens` over the selected Turns' messages, measured **before** returning. The fit invariant (MAJ-05) is the live budget check made whole: `estimateMessageTokens(pinned) + estimateMessageTokens(window) + estimateToolDefsTokens(toolDefs) + estimateMessageTokens(recallResult) + maxTokens ≤ contextWindow` — **`toolDefsTokens` and `maxTokens` (the output reserve) are both included** (matching `isOverContextBudget`). NB: the FR-001 **5% headroom is a separate, stricter `windowTrim` target**, not part of this hard invariant.
- **FR-010 (US-4/M-1):** A `bm25Score(queryTokens, docTokens, corpusStats)` **core** MUST be extracted and used by two thin typed callers — the existing `rankRetrosBM25` (over `Retro`) and a new `rankTurnsBM25` (over grouped Turns) — reusing `retroTokenize` (already `string`-generic, `retro_bm25.go:103`). No Go generics; the retro path stays monomorphic and its behavior byte-for-byte preserved (OBS-02).
- **FR-011 (US-6):** `handleModelSwitch` MUST re-window via the **same** `windowTrim` (FR-001) with no summary; its midpoint-split path MUST be deleted. **Downsize** thereby **inherits FR-003's last-user-Turn floor and termination guarantee** (a switch to a model whose window is smaller than the last Turn keeps only the last user Turn and does not loop — MAJ-06). **Upsize** leaves `Skip` put (forward-only; evicted turns are not re-expanded). `[CONFIRMED]`
- **FR-016 (US-4/MAJ-02):** A new exported reader `JSONLStore.ReadArchive(ctx, sessionKey) ([]storedMessage, error)` MUST read the **full log from line 0 ignoring `meta.Skip`** (via `readMessages(path, 0)`), threaded through `UnifiedStore`. `recall_conversation` and the breadcrumb builder MUST use it; it MUST return turns at indices `< meta.Skip` (the evicted ones).
- **FR-017 (US-3/US-4.3/MAJ-01):** `addMsg` MUST persist each line as a flat wrapper `storedMessage{ providers.Message ; TS int64 \`json:"ts,omitempty"\` }` stamping the write time. This is an **internal persistence format** (NOT a Constraint #8 gateway/SPA wire type — no contract regen). Readers MUST accept **legacy timestamp-less lines** (unmarshal → `TS==0`, treated as "unknown/earlier" by the breadcrumb and as session-start by `time` mode). `GetHistory`/`BuildMessages` strip `TS` and return plain `providers.Message`; `ReadArchive` exposes `TS`.
- **FR-018 (MAJ-07):** The eviction/recall hot path MUST emit structured counters (metric or log): `context_eviction_total`, `context_skip_advance_total`, `context_archive_bytes`, `recall_conversation_calls_total{result=hit|empty|error}`, and `recall_span_dropped_total{reason=replaced|pressure|aged}`. `windowTrim` MUST log at **WARN** when it evicts (session id + turns evicted + tokens trimmed) so the accepted M-4 silent-loss risk is visible in production.
- **FR-019 (US-4/Design B):** Recall re-injection MUST use a **transient in-memory recall span** with these invariants:
  - **Not persisted:** the span is NEVER written to `context.jsonl` (the archive stays the single source of truth); it is an assembly-time overlay held on the agent-loop/session state, merged by `BuildMessages` **after** the breadcrumb and **before** the sliding window, behind a demarcation marker message (e.g. a `system`/`user` note: *"Recalled earlier turns {A}–{B} (reference):"*). Chronology: recalled (old) precedes the window (recent).
  - **Not evictable by `Skip`:** the span lives outside the `meta.Skip` window mechanics; `windowTrim` never advances `Skip` into it.
  - **Dropped first under pressure:** when a turn is over budget, `windowTrim` MUST drop the **whole** recall span *before* evicting any real window Turn (kills the recall↔eviction feedback loop). Real conversation is always protected over transient recalled reference.
  - **Bounded + budgeted:** the span obeys the FR-009 caps and is counted as the `recallResultTokens` term of the fit invariant every turn it is active.
  - **Lifecycle:** the span persists until the **next `recall_conversation` call replaces it** or it is dropped under pressure. `[DEFAULT — see A-11; a fixed turn-age expiry is a tunable, not set]`
  - **ID namespace:** rewritten IDs use a `recall_` prefix so they are distinguishable from live IDs and guaranteed collision-free.
- **FR-012 (US-7):** `RetentionSweep` MUST remove the `.context/` exemption; ModTime-based sweeping MUST spare active sessions.
- **FR-013 (US-4.5):** `recall_conversation` MUST validate the session id and MUST NOT return another session's turns.
- **FR-014 (US-8.2):** Loading a legacy session with a `[dropped N]` summary marker MUST NOT error; the marker is inert.
- **FR-015 (US-3/D12):** `recall_memory`'s description MUST cross-reference `recall_conversation` ("for earlier turns of THIS conversation, use recall_conversation").

## 6. Success Criteria
- **SC-001:** After forced overflow, on-disk `context.jsonl` line count is **unchanged** (0 bytes deleted) while the live window fits under budget. (FR-001/005)
- **SC-002:** 100% of eviction cuts land on a Turn boundary — 0 torn tool pairs across the dataset. (FR-002)
- **SC-003:** A recalled tool-bearing turn replays **provider-valid** (no 400) in an integration send. (FR-008)
- **SC-004:** `grep -r forceCompression pkg/` and `grep -r "Emergency compression dropped"` return **0** matches. (FR-004)
- **SC-005:** `recall_conversation` never returns > 8 turns or > 4000 tokens (default mode) across the dataset. (FR-009)
- **SC-006:** Cross-session recall attempts return **0** foreign-session turns. (FR-013)
- **SC-007:** Breadcrumb building makes **0** LLM calls (asserted via a stub provider call counter). (FR-007)
- **SC-008:** p50 `BuildMessages` latency within **+10%** of the pre-change full-replay path on the 500-turn harness. (NFR-2)
- **SC-009:** All existing **memory / session / retro-recall** tests pass unchanged; the **compaction tests are removed/replaced** per T4/T21 (the `forceCompression` + `[dropped N]` tests cannot "pass unchanged" — they are retired). (MIN-05)
- **SC-010:** After `Save` on an evicted session, a recall of an evicted turn still succeeds. (FR-005)
- **SC-011:** `recall_conversation` / `ReadArchive` returns a turn at index `< meta.Skip` (an evicted turn) — proving the archive read reaches past `Skip`. (FR-016)
- **SC-012:** Eviction increments `context_eviction_total` and emits a WARN log; recall increments `recall_conversation_calls_total{result=…}` — both observable in a test harness. (FR-018)
- **SC-013:** A `time`-mode recall filters correctly on the per-line `TS`; a legacy `TS==0` line is treated as session-start and never crashes. (FR-017)
- **SC-014:** After any recall, `context.jsonl` line count is unchanged — the recall span is never persisted. (FR-019)
- **SC-015:** Across the recall dataset, the assembled provider request has **0** unresolved/duplicate `tool_call_id`s (span + window always ID-consistent). (FR-008/FR-019)
- **SC-016:** Under budget pressure with an active span, `Skip` is not advanced until the span is dropped — **0** real Turns evicted while a droppable span exists. (FR-019)

## 7. TDD Plan (tests before code)

| Order | Test | Level | Traces to (US.AC) | Description |
|---|---|---|---|---|
| T1 | `TestWindowTrim_KeepLastTurnAlignedFit` | Unit | US-1.1 | Over-budget → keeps largest whole-Turn suffix that fits; Skip advanced. |
| T2 | `TestWindowTrim_CutsOnTurnBoundary` | Unit | US-1.2 | Cut never splits a tool-call/result pair — run on a **fresh** AND an **already-evicted (Skip>0)** session; assert new window[0] is `role:"user"`, no orphaned `tool_call_id` (MAJ-03). |
| T3 | `TestWindowTrim_SingleHugeTurn_KeepsLastUser` | Unit | US-1.3 | One >window Turn → keep last user Turn, terminates. |
| T4 | `TestWindowTrim_NoDroppedMarker` | Unit | US-1.4 | No `[dropped N]` written. |
| T5 | `TestArchive_SkipEvictionDeletesZeroBytes` | Integration | US-1.1/US-5.1 | On-disk line count unchanged after eviction. |
| T6 | `TestSave_DoesNotCompactSkipped` | Integration | US-5.1 | `Save` keeps skipped lines on disk. |
| T7 | `TestBuildMessages_ReplaysWindowNotEvicted` | Integration | US-2.1 | Assembled messages exclude evicted turns; include window + system. |
| T8 | `TestBuildMessages_NoEviction_FullWindow` | Integration | US-2.2 | Short session unchanged; no breadcrumb. |
| T9 | `TestBreadcrumb_HeuristicPointerContents` | Unit | US-3.1/3.2 | turn-range + ts + snippet + entities + names recall_conversation. |
| T10 | `TestBreadcrumb_NoLLMCall` | Unit | US-3.3 | Stub provider call count == 0 during breadcrumb build. |
| T11 | `TestBreadcrumb_CapAndOverflow` | Unit | US-3.4 | Caps at ~1000 tokens, "+K earlier ranges". |
| T12 | `TestRecallConversation_QueryBM25` | Integration | US-4.1 | Nonce turn returned, BM25-ranked, verbatim. |
| T13 | `TestRecallConversation_TurnRange` | Integration | US-4.2 | Range slice verbatim, in order. |
| T14 | `TestRecallConversation_TimeWindow` | Integration | US-4.3 | `{from,to}` filters on per-line `TS` (FR-017); a `TS==0` legacy line → session-start, no crash. |
| T15 | `TestRecallConversation_OutputBounded` | Integration | US-4.4 | ≤8 turns/≤4000 tok + "N more" hint. |
| T16 | `TestRecallConversation_SessionScoped` | Integration | US-4.5/US-4.6 | This session only; empty query errors; no cross-session. |
| T17 | `TestRecallSpan_ReinjectedProviderValid` | Integration | US-4.1/MAJ-04/FR-019 | Recall of a tool-bearing whole Turn re-injects native `assistant`+`tool` messages with **rewritten `recall_*` IDs**; assembled request is provider-valid (every `tool_call_id` resolves, no collision with live window); the historical tool is **not** re-executed. |
| T18 | `TestBM25Core_SharedByTurnAndRetroCallers` | Unit | US-4/FR-010 | `bm25Score` core drives both `rankTurnsBM25` and `rankRetrosBM25`; retro output byte-for-byte preserved. |
| T19 | `TestModelSwitch_ReWindowsNoSummary` | Integration | US-6.1 | Re-fit to new budget; no summary. Includes a **downsize where the last Turn alone exceeds the new window** → keeps last user Turn, no loop (FR-011/FR-003, MAJ-06). |
| T24 | `TestModelSwitch_UpsizeKeepsSkipForward` | Integration | US-6.2 | Upsize does NOT move `Skip` backward; evicted turns stay evicted (structural gap: US-6.2 had no distinct test). |
| T25 | `TestReadArchive_ReachesEvictedTurn` | Integration | US-4/FR-016/SC-011 | `ReadArchive` returns a turn at index `< meta.Skip`; `GetHistory` does not. |
| T26 | `TestAddMsg_StampsTimestamp_LegacyLineZero` | Unit | FR-017 | New lines carry `TS>0`; a hand-written legacy line (no `ts`) unmarshals to `TS==0` without error. |
| T27 | `TestObservability_EvictionAndRecallCounters` | Unit | FR-018/SC-012 | Eviction bumps `context_eviction_total` + WARN log; recall bumps `recall_conversation_calls_total{result}`. |
| T28 | `TestRecallSpan_NotPersistedToArchive` | Integration | FR-019/SC-014 | After a recall, `context.jsonl` line count is unchanged (span is in-memory only). |
| T29 | `TestRecallSpan_DroppedFirstUnderPressure` | Integration | FR-019/US-1 | An over-budget turn with an active span drops the whole span **before** evicting any real window Turn; `Skip` unchanged if dropping the span suffices; `recall_span_dropped_total{reason=pressure}`++. |
| T30 | `TestRecallSpan_ReplacedOnNextRecall` | Integration | FR-019/A-11 | A second recall replaces the prior span (not accumulates); assembled request never carries two spans; `{reason=replaced}`++. |
| T31 | `TestBuildMessages_SpanPlacedAfterBreadcrumbBeforeWindow` | Integration | FR-019 | Assembly order = pinned core → breadcrumb → recall-span marker + span → sliding window; recalled turns precede window turns. |
| T20 | `TestRetention_SweepsIdleContext_SparesActive` | Integration | US-7.1/7.2 | Idle `.context/` swept; active spared (ModTime). |
| T21 | `TestDecommission_NoForceCompressionSymbols` | Unit | US-8.1 | grep-clean of removed symbols. |
| T22 | `TestLegacySummaryMarker_Inert` | Integration | US-8.2/MIN-04 | Load a **real persisted `.meta.json`** carrying a `[dropped N]` summary; assert it renders inertly as plain context (not re-parsed), no error. |
| T23 | `TestFitInvariantHolds_WindowPlusRecall` | Integration | US-4/NFR-5/MAJ-05 | `estimateMessageTokens(pinned+window+recall) + estimateToolDefsTokens(toolDefs) + maxTokens ≤ contextWindow`, incl. toolDefs + reserve. |

### Test Datasets
| Dataset | Rows exercise | Traces |
|---|---|---|
| DS-1 sessions | empty · 1-turn · exactly-at-budget · 1-over · single-huge-turn · 500-turn · tool-heavy turn · unicode content · **already-evicted (Skip>0)** | T1-T8, T23, T2 |
| DS-2 recall queries | nonce hit · no-hit · empty query · turn_range in/out of bounds · **time-window over per-line TS** · **legacy TS==0** · cross-session id · **>bound result that would tear a tool pair** | T12-T17, T25 |
| DS-3 breadcrumb | 0 evictions · 1 chunk · many chunks (>cap) · **fixed first line asserting exact entity extraction** (quoted/path/multi-word-Capitalized; single-word & sentence-initial excluded) · legacy `TS==0` → "earlier" | T9-T11 |

### Regression Requirements (feature MODIFIES existing behavior)
- MUST preserve: `GetHistory` semantics, `addMsg` append, `TruncateHistory` Skip math, retro recall (`SearchRetros`/`rankRetrosBM25` behavior via the generalized core), `BuildMessages` output for short sessions, model-switch correctness.
- Existing tests that MUST stay green: all `pkg/session`, `pkg/memory`, `pkg/agent` context/memory/switch tests; `pkg/tools` recall tests; the memory integration tests added this session.
- NEW regression tests: T6 (Compact-guard doesn't break normal save), T8 (short-session parity), T18 (retro recall preserved after BM25 generalization).

---

## 8. Resolved Decisions (operator-confirmed 2026-07-01 — no open ambiguities)

| # | Decision | Resolution |
|---|---|---|
| A-1 | Sliding-window sizing headroom | **5% slack** of `contextWindow` retained after the fit, so a normal next turn doesn't immediately re-trim (FR-001). |
| A-2 | File-GC `Compact` handling | **Remove `Compact` from the `Save` path** (`unified.go:757`, `jsonl_backend.go:75`); keep the `JSONLStore.Compact` function but never auto-invoke it; retention sweep is the sole deleter (FR-005). |
| A-3 | Model-switch to larger window | **Leave evicted turns evicted** — `Skip` is forward-only; extra room goes to new turns; page older turns via recall (FR-011). |
| A-4 | Breadcrumb entities | **Quoted text + file paths + multi-word runs of ≥2 Capitalized tokens (not sentence-initial)**; single Capitalized words excluded (MIN-02); no code-identifier/URL extraction (FR-007). |
| A-5 | `.context/` retention window | **= `session_days` (90)** — archive and session expire together; one retention concept (FR-012). |
| A-6 | Recall bounds | **Default 8 turns / 4000 tokens; `turn_range` 50 turns / 8000 tokens** (FR-009). |
| A-7 | Eviction granularity | **One whole Turn per pass**, re-check, repeat (FR-001). |
| A-8 | Breadcrumb caps | **~1000-token block cap; ≤80-char per-chunk snippet** (FR-007). |
| A-9 | Per-turn timestamp source (MAJ-01) | **Add a per-line `TS`** to `context.jsonl` via flat wrapper `storedMessage{providers.Message; TS int64}` — internal format, backward-compatible (`TS==0`=legacy), NOT a wire type. Powers breadcrumb time + `time`-mode (FR-017). |
| A-10 | Recall selection unit + output form (MAJ-04) | Atomic unit = **whole Turn**; output = **Design B: native transient recall span** (reconstructed `user`/`assistant`/`tool` messages, IDs rewritten `recall_*`, never re-executed, never persisted) — operator chose B over read-only text (FR-008/FR-019). |
| A-11 | Recall-span lifecycle | **Persists until the next recall replaces it, or dropped-first under budget pressure**; no fixed turn-age expiry (tunable, not set). Dropped-first protects real history from the recall↔eviction loop (FR-019). |

## 9. Holdout Evaluation Scenarios (NOT for TDD — post-impl verification)
- **H-1 (happy):** A real 300-turn chat with a fact stated at turn 5; at turn 250 ask about it — the agent pages it via `recall_conversation` and answers correctly.
- **H-2 (happy, the M-4 gate):** Over a fixed holdout set of long sessions whose answer lives **only** in an evicted turn, measure the fraction answered correctly via a `recall_conversation` call. **PASS threshold: ≥ 80%** (OBS-03). Below that, the breadcrumb steering or tool description needs tuning before ship — this is the silent-loss risk gate.
- **H-3 (happy):** A tool-heavy session (many tool calls) overflows; recall of an evicted tool turn replays and the next turn is provider-valid.
- **H-4 (error):** Kill the gateway mid-session, restart, continue — evicted turns still recallable (append-only archive survived).
- **H-5 (error):** Recall with a garbage query returns a clean empty/hint, never a full dump.
- **H-6 (edge):** A single 150k-token tool result arrives — the loop keeps the last user Turn and does not wedge.
- **H-7 (edge):** Two agents hand off in one session; recall surfaces both agents' relevant turns (session-scoped) and never another session's.

## 10. Traceability Matrix
| FR | US | BDD/AC | Tests |
|---|---|---|---|
| FR-001 | US-1.1 | US-1.1 | T1, T5 |
| FR-002 | US-1.2 | US-1.2 | T2 |
| FR-003 | US-1.3 | US-1.3 | T3 |
| FR-004 | US-1.4/US-8.1 | US-1.4/US-8.1 | T4, T21 |
| FR-005 | US-5 | US-5.1/5.2 | T6, T10*, SC-010 |
| FR-006 | US-2 | US-2.1/2.2 | T7, T8 |
| FR-007 | US-3 | US-3.1-3.4 | T9, T11 |
| FR-008 | US-4 | US-4.1-4.3 | T12-T14, T17 |
| FR-009 | US-4.4 | US-4.4 | T15, T23 |
| FR-010 | US-4 | US-4.1 | T18 |
| FR-011 | US-6 | US-6.1 | T19 |
| FR-012 | US-7 | US-7.1/7.2 | T20 |
| FR-013 | US-4.5/4.6 | US-4.5/4.6 | T16 |
| FR-014 | US-8.2 | US-8.2 | T22 |
| FR-015 | US-3/D12 | US-3.2 | T9 (asserts cross-ref) |
| FR-016 | US-4 | US-4.1/4.5 | T25, T12 |
| FR-017 | US-3.1/US-4.3 | US-3.1/US-4.3 | T26, T14, T9 |
| FR-018 | US-1/US-4 | (observability) | T27 |
| FR-019 | US-4 | US-4.1/MAJ-04 | T17, T28, T29, T30, T31 |

*(T10 = the no-LLM-call assertion, shared. T24 covers the US-6.2 upsize structural gap.)*

## 11. Assumptions
- `context.jsonl` (in `.context/`) is the sole provider-valid replay archive; `transcript.jsonl` is not used for recall. `[FACT, ADR-028 rev.3]`
- `TruncateHistory` is the Skip-advance primitive; `meta.Skip` is **forward-only**; no new persistence layer is built. `[FACT]`
- **Recall recovers only turns evicted *after* this ships.** Content already dropped by legacy `forceCompression`/`Compact` on existing sessions is physically gone and unrecoverable — US-1's "no content is ever lost" holds **going forward** only (MIN-06).
- The per-line `TS` (FR-017) is best-effort write-time, not a wall-clock guarantee across clock skew; `time`-mode is approximate and legacy `TS==0` lines sort as session-start.
- All tunable values are **operator-confirmed** (§8), not open: 5% window slack; recall 8/4000 default & 50/8000 range; breadcrumb 1000-token cap / 80-char snippet; `.context/` retention = 90 days; one-Turn-per-pass eviction; Compact removed from Save path (function test-only); entities = quoted/paths/multi-word-Capitalized; upsize leaves evicted; recall = whole-Turn **Design B native re-injection** (transient non-persisted span, IDs rewritten, dropped-first under pressure); per-line timestamp added.
