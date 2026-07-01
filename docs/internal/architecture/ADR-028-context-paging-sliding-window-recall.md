# ADR-028: Context paging — sliding-window + recall replaces reactive compaction

- **Status:** Proposed (rev. 3 — post re-grill)
- **Date:** 2026-07-01
- **Deciders:** Daniel (operator) + Albert (architecture)
- **Evidence level (highest used):** 1 (user-provided direction) + 3 (documented pattern: MemGPT/Letta context paging), grounded in the codebase (Rule 7)

> **Revision note (rev. 2).** The first draft named `transcript.jsonl` as the recall
> archive. `/grill-spec` (review: `ADR-028-…-review.md`) correctly BLOCKED that: the
> replayable, provider-format history is **`context.jsonl`** (in `.context/`), not the
> lossy UI feed `transcript.jsonl`. This revision corrects the archive to append-only
> `context.jsonl`, records the two operator decisions that followed (bounded `.context/`
> retention; hard-replace confirmed), and folds the grill's MAJOR findings (M-1 BM25
> typing, M-2 agent-scoping, M-3 combined budget, M-4 silent-loss) into the decisions.
>
> **Revision note (rev. 3, post re-grill).** The re-grill cleared the BLOCK (0 CRITICAL) and
> raised 6 MAJOR, all now addressed & code-verified: the append-only archive already exists via
> `Skip`-based eviction (MAJ-001 → FR-2 shrunk); the only destroyer is `Compact`-on-`Save`
> (MAJ-002 → disable it); per-agent recall is infeasible so recall is session-scoped (MAJ-003 →
> NFR-6); ModTime retention spares active sessions (MAJ-004 → FR-9); measurable NFR thresholds
> added (MAJ-005 → NFR-2/NFR-5); Turn-boundary `Skip` advance required for provider-valid replay
> (MAJ-006 → D10); `handleModelSwitch` citation fixed to `loop.go:7659`.

> **Mode:** Ratifying ADR. D1–D6 (direction) were decided in the design doc + conversation.
> The genuinely designed decisions here are **D7 (recall tool shape)**, **D11 (which log
> is the archive)**, and **D14 (archive is append-only + bounded)**, analysed in §5–§6.

---

## 1. Problem Understanding

**Problem.** Omnipus manages per-turn context with **reactive compaction**. On overflow,
`forceCompression` (`pkg/agent/loop.go:7411`, triggered at `loop.go:5075/5846/5981` under
`isOverContextBudget`, `context_budget.go:161`) drops the oldest ~50% of turns
(`splitHistoryAtTurnMidpoint`, `context_budget.go:187`), writes a lossy
`"[Emergency compression dropped N oldest messages]"` marker into the session summary, and
**rewrites the working history** via `agent.Sessions.SetHistory`+`Save`
(`session/manager.go:297`, `session/unified.go:742/756`). `[FACT]` The dropped content is
gone from the working context — only a count marker remains. `[FACT]`

**Direction (ratified).** Retire compaction; adopt **context paging** (MemGPT/Letta pattern
`[documented pattern]`): the live window is RAM, an append-only session log is disk, and a
recall tool is the page-fault handler. Old turns are *evicted* from the live window (not
summarized, not deleted) and paged back **verbatim** on demand. `[FACT: user-provided]`

**The archive — corrected (see D11, §5–§6).** There are two per-session logs `[FACT, unified.go:83]`:
- **`context.jsonl`** (in `.context/`) — the **agent-loop provider-message history**
  (`GetHistory`/`SetHistory`). Provider format: roles `user`/`assistant`/`tool`, with
  `tool_call_id` and tool-**result** entries. This is the **only** log from which
  provider-valid messages can be replayed. Retention currently **skips `.context/`**
  (`retention_sweep.go:62–91`). `[FACT]`
- **`transcript.jsonl`** — the **UI feed** (`TranscriptEntry`, `daypartition.go:141`).
  It has **no `tool_call_id` and no tool-result entry type**, so it is **lossy for replay**,
  and retention **deletes** it at `session_days` (default 90). `[FACT]`

Therefore recall MUST page from **`context.jsonl`** — and the change is **smaller than it
looks** (grill MAJ-001). `context.jsonl` is *already* byte-level append-only: writes go
through `addMsg` with `O_APPEND` (`pkg/memory/jsonl.go:214,228`), and eviction is already
**`Skip`-based** — `TruncateHistory` advances `meta.Skip` (`jsonl.go:323,347,351`) and
`GetHistory` reads `readMessages(path, meta.Skip)` (`jsonl.go:266,280`), deleting **zero
bytes**. So `windowTrim` = "advance `meta.Skip` to a Turn boundary," not a new persistence
layer. The **one** thing that destroys the archive is `Compact`-on-`Save`: `UnifiedStore.Save`
→ `backend.Compact` (`unified.go:756-757`) physically rewrites the file dropping skipped
lines and resets `Skip=0` (`jsonl.go:398,415,425`). The load-bearing change is therefore:
**evict via `Skip` (already exists) and stop `Compact` from dropping skipped lines** (so the
full log persists as the archive), plus bounded `.context/` retention (D14).

**Blast radius.** `BuildMessages` (`context.go:717`) runs on **every agent turn**. This is
the hottest path in the system. `[FACT]`

---

## 2. Extracted Requirements

### Functional
- **FR-1 (D1):** Replace `forceCompression` with `windowTrim`: on over-budget, evict the
  oldest **whole Turn(s)** from the **in-memory live window** until it fits. Delete the
  `[dropped N]` marker path and the summary-write. `[FACT]`
- **FR-2 (D14) — corrected (grill MAJ-001/002):** Evict via `meta.Skip` (advance it, as
  `TruncateHistory` already does — zero bytes deleted; `jsonl.go:347`). `Save` MUST NOT drop
  skipped lines: `Compact` (`unified.go:757`→`jsonl.go:398`) must be disabled/guarded on the
  agent-loop archive so the full log persists. `windowTrim` advances the live-window `Skip`
  independent of the on-disk archive tail that recall reads. `[FACT — corrected]`
- **FR-3 (D3):** `BuildMessages` replays only the sliding window (recent turns to a token
  target) + pinned core (existing system prompt) + boundary breadcrumb; it stops replaying
  full history. `[FACT]`
- **FR-4 (D2):** Three-zone live context: pinned core (identity/goal/memory — unchanged),
  sliding window (recent turns verbatim), boundary breadcrumb (pointers to what slid out).
  Append-only `context.jsonl` is the on-disk archive. `[FACT]`
- **FR-5 (D4):** Breadcrumb = heuristic, LLM-free pointer: turn-range + timestamp + verbatim
  snippet of the chunk's first user line + cheap entities (quoted text, paths, Capitalized
  names). Pointers, not a summary. It MUST be **prominent** (mitigates M-4). `[FACT]`
- **FR-6 (D5, D11):** `recall_conversation` retrieves verbatim provider-valid turns from
  **this session's `context.jsonl`** by (a) BM25 query, (b) turn-range, (c) time window.
  Output is **session-scoped** (not per-agent — see NFR-6) and **bounded** (D13). `[FACT — corrected]`
- **FR-7 (D7):** Recall is a **new `recall_conversation` tool** (§5–§6), distinct from
  `recall_memory`, steered by the breadcrumb + a cross-reference in `recall_memory`'s
  description.
- **FR-8 (D6):** `handleModelSwitch` (`loop.go:7659`) becomes a re-window (re-fit the sliding
  window to the new model's budget); its `splitHistoryAtTurnMidpoint`+summary path is removed.
- **FR-9 (D14) — corrected (grill MAJ-004):** Bound `.context/` growth by **removing its
  sweep exemption** (`retention_sweep.go:55,74`). Sweeping is **ModTime-based** (`:84`), so an
  active session — being written every turn — has a fresh mtime and is naturally spared; only
  sessions idle past the retention window are swept. No new "liveness" concept is needed.
  `[FACT — corrected]`

### Non-Functional
- **NFR-1 (cost):** No LLM call on the hot path (eviction + breadcrumb are LLM-free). `[FACT]`
- **NFR-2 (latency, grill MAJ-005):** p50 turn-assembly latency MUST stay within **+10%** of
  the current full-replay path (sliding replay is less work; BM25 runs only on tool call).
  Measured in the §9 benchmark. `[ASSUMPTION until measured]`
- **NFR-3 (fidelity) — corrected (grill C-2):** Recall returns **provider-valid** turns
  reconstructable from `context.jsonl` (tool_call_id + tool results present). "Byte-identical
  to `transcript.jsonl`" is dropped as infeasible; the target is provider-valid verbatim
  replay from `context.jsonl`. `[FACT — corrected]`
- **NFR-4 (compatibility):** Existing/in-flight sessions keep working; no migration; legacy
  `[dropped N]` markers in old summaries remain harmless. `[INFERENCE]`
- **NFR-5 (recall safety, grill M-3/MAJ-005):** The per-turn fit invariant MUST hold:
  `pinnedTokens + windowTokens + recallResultTokens + outputReserve ≤ contextWindow`. A recall
  result that would violate it is truncated with a "narrow the query / use turn_range" hint —
  never evicted-then-recalled in a loop. `[EXPERT REASONING]`
- **NFR-6 (scope, grill MAJ-003):** Recall is **session-scoped** (never cross-session). It is
  **not** per-agent: `providers.Message` (`protocoltypes/types.go:85`) carries no agent-id, and
  within one session a handoff's agents share one conversation — those turns are in-scope, not a
  bleed. `[FACT — corrected]`

### Constraints
- Single Go binary, pure Go, no new deps (Constraint #1/#2). BM25 reused/generalized from
  `retro_bm25.go` (M-1: it is hard-typed to `Retro` today → generalize to the message type;
  real work, not free reuse). `[FACT]`
- Hot-path change; Constraint #7 with force. Operator decision: **hard replace, no flag** (D1).

---

## 3. Gaps and Ambiguities (post-grill, mostly resolved)

| # | Item | Resolution |
|---|---|---|
| D7 | Recall tool shape | **§6 → new `recall_conversation` tool + breadcrumb steering** (Option C). |
| D11 | Which log is the archive | **RESOLVED: `context.jsonl` (.context/).** Already byte-level append-only (`addMsg` O_APPEND; `Skip`-based eviction, zero bytes deleted — grill MAJ-001). Not `transcript.jsonl`. |
| D14 | Archive lifecycle | **RESOLVED:** keep the log complete by disabling `Compact`-on-`Save` (grill MAJ-002); bound growth by removing the `.context/` sweep exemption — ModTime spares active sessions (grill MAJ-004). |
| MAJ-002 | `Save`→`Compact` drops skipped lines | **RESOLVED (FR-2):** disable/guard `Compact` on the agent-loop archive so evicted (skipped) lines persist for recall. |
| MAJ-003 | Per-agent recall infeasible | **RESOLVED (NFR-6):** recall is session-scoped; `providers.Message` has no agent-id; within-session cross-agent turns are the shared conversation. |
| D8 | Sliding-window sizing | Target = as many recent whole Turns as fit under `window − MaxTokens − pinned − breadcrumb`, floor = last user Turn. **Conservative** (evict late) to blunt M-4. `[ASSUMPTION → plan-spec numbers]` |
| D9 | Eviction granularity | Minimal: evict oldest whole Turn(s) one at a time until it fits (not drop-50%). |
| D10 | Turn atomicity (grill MAJ-006) | `meta.Skip` is a **line count**; `windowTrim` MUST advance it only to a line index that **begins a clean Turn** (never mid tool-call/tool-result), else replay is provider-invalid (400 on the hot path). Plan-spec maps Turn boundaries → line indices. |
| D12 | Discovery | Prominent breadcrumb names `recall_conversation`; `recall_memory` description cross-references it. |
| D13 | Recall output bound | Cap output (≤ ~8 turns or a token budget) + "N more — narrow query / use turn_range". |
| M-1 | `retro_bm25` typing | Generalize `rankRetrosBM25`/`retroTokenize` to operate on the message/turn type (or extract a shared BM25 core). Track as real implementation work. |
| M-4 | Silent loss if model never pages | **Accepted risk** (hard-replace confirmed). Mitigations: prominent breadcrumb (D5), conservative eviction (D8/D9 — evict late, keep a large window). Residual risk recorded (§7). |

---

## 4. Decision Criteria (D7 — recall tool shape)

| Criterion | Weight | Notes |
|---|---|---|
| Semantic clarity | High | Curated durable **facts** vs verbatim **this-session turns** — different corpora + return shapes. |
| Model-selection accuracy | High | Will a glm-class model pick the right recall path? |
| Contract cleanliness | Med | `recall_memory` = `query`/`room`/`limit`; transcript adds `turn-range`/`time` that don't fit. |
| Implementation coupling | Med | Enhancing `recall_memory` couples `MemorySearcher` to the session store. |
| Tool-surface cost | Low | Roster ~83 tools `[FACT]`; +1 marginal. |

---

## 5. Option Analysis (D7)

### Option A — New `recall_conversation` tool
| Dimension | Assessment |
|---|---|
| Strengths | Clean split (facts vs turns); native `turn-range`/`time`; own provider-valid return shape + own bounding; independent registration/testing; leaves `recall_memory`/`MemorySearcher` untouched. |
| Weaknesses | One more tool to disambiguate. |
| Risks | Model mis-selects between the two recall tools. |
| Complexity | Low-med: new tool + read `context.jsonl` + generalized BM25 + slices. |
| Cost | Build small; run: BM25 only on tool call. |
| Operational | Standard registration. |

### Option B — Enhance `recall_memory` with a `source`/`scope` param
| Dimension | Assessment |
|---|---|
| Strengths | One "recall" verb; fewer tools. |
| Weaknesses | Conflates two corpora/return-shapes; `turn-range`/`time` are meaningless for memory → bloated mode-dependent contract; `room` (private/shared) orthogonal to `source` → param combinatorics; couples `MemorySearcher` to the session store; risks the existing contract. |
| Risks | Swiss-army tool; wrong-scope defaults; back-compat on a shared tool. |
| Complexity | Med-high (contract redesign + coupling). |
| Cost | Build higher (shared tool). |
| Operational | Larger blast radius on an existing tool. |

### Option C — New `recall_conversation`, steered by the breadcrumb + cross-referencing descriptions `[CREATIVE CONTRIBUTION]`
| Dimension | Assessment |
|---|---|
| Strengths | Option A's clarity; selection risk mitigated where the design already builds — the **breadcrumb names `recall_conversation`**, and `recall_memory`'s description disambiguates. Zero new surface. |
| Weaknesses | Prompt-level steering, not a hard guarantee. |
| Risks | Residual mis-selection — bounded, recoverable (a wrong recall returns little; the model retries). |
| Complexity | Option A + two description edits. |
| Cost / Operational | Same as A. |

---

## 6. Recommended Architecture

**D7 → Option C:** a new **`recall_conversation(query | turn_range | time)`** tool, steered by
the breadcrumb (names it) + a disambiguating cross-reference in `recall_memory`'s description.
Rationale: maximises semantic clarity + contract cleanliness (highest-weighted); the second
tool's only real downside (selection accuracy) is mitigated inside the design already being
built; +1 tool is marginal. Option B rejected (bloated mode-dependent contract + memory/session
coupling).

**D11 → the archive is append-only `context.jsonl` (`.context/`).** It is the sole
provider-valid, replayable log (tool_call_id + tool results); it is retention-surviving
today (retention skips `.context/`). `recall_conversation` reads it. `transcript.jsonl`
rejected: lossy for replay + retention-deleted.

**D14 → append-only + bounded retention.** `windowTrim` stops rewriting `context.jsonl`;
it becomes append-only. Add `.context/` retention that sweeps **inactive** sessions' logs at
the retention window (never active sessions). This makes the "no data loss" safety real
*within the retention window* and prevents unbounded growth on long-lived/heartbeat sessions.

```
CONFIDENCE (D7 — new recall_conversation tool): Medium-High
  Basis         : corpora/return-shapes/modes genuinely differ; +1 tool marginal;
                  selection risk mitigated by the breadcrumb the design already adds.
  Evidence      : recall_memory contract; provider-message format in context.jsonl.
  Missing       : empirical two-recall-tool selection accuracy for glm-class models.
  Would improve : a small selection eval (one tool vs two).

CONFIDENCE (D11+D14 — context.jsonl archive via Skip + disable Compact + ModTime retention): High
  Basis         : [FACT, grill-verified] context.jsonl is the only replayable log; it is
                  already byte-level append-only (addMsg O_APPEND; Skip-based eviction deletes
                  zero bytes); the ONLY destroyer is Compact-on-Save. The change is small and
                  localized, not a new persistence layer.
  Evidence      : jsonl.go:214/228 (O_APPEND), :266/280 (readMessages+Skip), :323/347/351
                  (TruncateHistory advances Skip), :398/415/425 (Compact drops skipped, Skip=0);
                  unified.go:756-757 (Save→Compact); retention_sweep.go:55/74/84 (skip + ModTime).
  Missing       : the exact guard to stop Compact dropping the archive tail; the Turn→line-index
                  mapping for Skip-boundary eviction (MAJ-006).
  Would improve : plan-spec pins the Compact guard + the Turn-boundary Skip advance + a test that
                  a recalled tool-bearing turn replays provider-valid.

CONFIDENCE (D1–D6 ratified direction): High
  Basis         : operator direction + documented pattern; archive now correctly identified.

CONFIDENCE (D8–D10, D12, D13 sub-decisions): Medium
  Basis         : standard paging practice + existing Turn-boundary/budget code.
  Missing       : eviction-churn + latency benchmarks on a long session.
```

---

## 7. Risks and Caveats

- **M-4 — silent context loss if the model never pages (accepted).** With hard-replace and
  no summary marker, a model that ignores the breadcrumb silently loses fine detail of
  evicted turns. glm-class models are noted (project memory) as reluctant tool-callers.
  Mitigations: **prominent** breadcrumb naming `recall_conversation` (D5/D12) + **conservative
  eviction** (evict late, keep a large window; D8/D9). Residual risk: on a non-paging model,
  the user relies on the breadcrumb gist only. **This is the single biggest product risk of
  the design** — plan-spec should include an eval that a model actually pages when it needs to.
- **One-way door: hard replace, no flag, on the hot path (accepted).** No instant rollback.
  Mitigation now *real*: append-only `context.jsonl` means **no data is lost** within the
  retention window even under a `windowTrim` bug — recall recovers it. Requires a hard
  pre-merge test gate.
- **Provider-validity on eviction (D10).** A torn tool-call/tool-result pair fails provider
  validation. Mitigation: cut only at Turn boundaries.
- **Recall re-overflow (NFR-5/D13).** Bound `window+recall` together; cap recall output.
- **Cross-agent bleed (NFR-6/M-2).** Scope recall to the current agent.
- **Unbounded `.context/` growth (D14).** Addressed by the new inactive-session retention;
  active long-lived (heartbeat) sessions still grow while active — acceptable, bounded by the
  sliding window's need for only recent turns + on-close retention.
- **BM25 generalization (M-1).** `retro_bm25` is `Retro`-typed; extracting a shared BM25 core
  over the message type is real work, not a copy-paste reuse.
- **v0.3-flavoured change on a hotfix branch** — scope note per CLAUDE.md routing; operator-directed.

---

## 8. Confidence Assessment (roll-up)

| Decision | Confidence | Basis |
|---|---|---|
| D1–D6 (direction) | **High** | Operator + documented pattern; archive now correctly identified |
| D7 (new tool) | **Medium-High** | Corpora differ; +1 marginal; selection risk mitigated by breadcrumb |
| D11+D14 (append-only `context.jsonl` + bounded retention) | **High** | Grill-corrected [FACT]; makes verbatim paging + no-loss real |
| D8–D10, D12, D13 | **Medium** | Standard paging; grounded in budget/boundary code; lacks benchmarks |
| M-4 silent-loss (accepted) | **N/A (accepted risk)** | Operator hard-replace; mitigated by prominent breadcrumb + conservative eviction |

---

## 9. Validation / Next Steps

1. **Re-grill (recommended, prior verdict was BLOCK):** `/grill-spec docs/internal/architecture/ADR-028-context-paging-sliding-window-recall.md`
   — confirm the archive correction (D11/D14) clears C-1/C-2/C-3, and press M-4.
2. **Spec it:** `/plan-spec docs/internal/architecture/ADR-028-context-paging-sliding-window-recall.md`
   — user stories + BDD + TDD for: `windowTrim` (minimal, Turn-boundary eviction), **append-only
   `context.jsonl`** (the SetHistory/Save conversion), sliding-window `BuildMessages`, the prominent
   heuristic breadcrumb, `recall_conversation` (context.jsonl source, agent-scoped, bounded,
   generalized BM25), model-switch re-window, and `.context/` inactive-session retention. Resolve
   D8/D9/D13 into numbers.
3. **Spikes/benchmarks before commit:** a long-session harness (eviction count, p50 assembly
   latency sliding vs current, recall round-trip validity) + a **model-paging eval** (does the
   model actually call `recall_conversation` when the answer is only in an evicted turn? — the
   M-4 gate) + a two-recall-tool selection eval (D7).
4. **Verify in plan-spec:** the exact `context.jsonl` write path (`SetHistory`→`Save`) to make
   append-only, and that `recall_conversation` reconstructs provider-valid messages (tool_call_id
   + tool results) from it.
