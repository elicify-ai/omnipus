# Adversarial Review: Context paging — sliding-window + recall (plan-spec)

**Spec reviewed**: `docs/internal/specs/context-paging-sliding-window-recall-spec.md`
**Source ADR**: `docs/internal/architecture/ADR-028-context-paging-sliding-window-recall.md` (rev. 3)
**Review date**: 2026-07-01
**Review mode**: `plan-spec` (has BDD-style ACs, FR-xxx IDs, SC-xxx, TDD plan, traceability matrix)
**Verdict**: **REVISE**

> Context: this spec descends from ADR-028, whose rev.2 review (`ADR-028-…-review.md`) raised 6
> MAJOR findings that rev.3 claims to have folded in. Several are genuinely closed by the spec
> (the `Skip`-based append-only mechanism, the `Compact`-at-the-sink fix, session-scoped recall).
> This review grades the spec fresh against the **code**, and finds one new load-bearing
> feasibility gap the ADR chain never surfaced (**no per-message timestamp in the archive** — the
> `time` mode and breadcrumb timestamps have no data source), plus unclosed ambiguities in the
> Turn→line-index mapping, the full-archive read path, and recall provider-validity.

---

## Executive Summary

The direction is sound and, unusually, the spec's most invasive claims check out against the
code: `context.jsonl` really is append-only via `meta.Skip`/`O_APPEND` (`pkg/memory/jsonl.go:226-263`),
`Compact` really is the only destroyer and is reachable from a single sink (`unified.go:757`,
`jsonl_backend.go:75`), and disabling it there neutralises all 7+ `Sessions.Save()` callers at
once. That is the load-bearing correctness argument and it holds.

The blocking gaps are: (1) **`recall_conversation`'s `time` mode and the breadcrumb's "relative
timestamp" have no data source** — the persisted line type `providers.Message`
(`protocoltypes/types.go:85-93`) carries **no timestamp field**, and `context.jsonl` stores raw
`json.Marshal(msg)` (`jsonl.go:220`); (2) **there is no exported way to read the full archive
ignoring `Skip`** — recall's entire purpose is to reach *evicted* turns, but every public read
(`GetHistory`) honours `Skip`, so an unspecified new read primitive is required; (3) **the
Turn→`keepLast`-line-count mapping is asserted but never given as a formula**, and `TruncateHistory`
counts raw messages, not Turns; (4) **recall provider-validity is under-specified for partial /
BM25-selected turns** — a lone `tool` result or an `assistant` message with an unresolved
`tool_call_id` is a 400. These are correctness/feasibility gaps, not style; each must be closed
before implementation because tests T12/T14 and FR-007/FR-008/FR-009 cannot be written against
missing data or missing read paths.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 7 |
| MINOR | 6 |
| OBSERVATION | 4 |
| **Total** | **17** |

**Verdict: REVISE** — no data-loss / security CRITICALs, but seven MAJOR gaps make FRs
unimplementable or untestable as written.

---

## Findings Table

| ID | Sev | Lens | Section | One-line |
|----|-----|------|---------|----------|
| MAJ-01 | MAJOR | Infeasibility | FR-007, FR-008, US-3.1, US-4.3, T14 | `time` mode + breadcrumb timestamp have no source: `providers.Message` has no timestamp field. |
| MAJ-02 | MAJOR | Incompleteness | FR-008, US-4, T12/T13/T16, §2 (`GetHistory` "Unchanged") | No exported full-archive (Skip=0) read; recall must reach evicted turns but every read honours Skip. |
| MAJ-03 | MAJOR | Ambiguity/Incorrectness | FR-001, FR-002, US-1.2, T1/T2 | Turn→`keepLast` line-count mapping asserted, never given; `TruncateHistory` counts messages, not Turns. |
| MAJ-04 | MAJOR | Incorrectness | FR-008, FR-009, NFR-3, US-4.1, T12/T15/T17 | Recall provider-validity for BM25/partial selection unspecified — orphaned `tool_call_id` → 400. |
| MAJ-05 | MAJOR | Ambiguity | FR-009, US-4.4, NFR-5, T15/T23 | Token accounting for the fit invariant undefined: whose estimator, what reserve value, at what point measured. |
| MAJ-06 | MAJOR | Incompleteness | FR-011, US-6.2, T19 | Downsize re-window has no floor/termination guarantee restated for `handleModelSwitch`; only upsize behaviour is confirmed. |
| MAJ-07 | MAJOR | Inoperability | whole spec | No observability requirement: eviction count, recall call/hit/empty rate (the M-4 detector), archive growth — none specified. |
| MIN-01 | MINOR | Incorrectness | §2 symbol table | Call-site line numbers wrong: `5075`→5078, `5846`→5851 (5981 ok). |
| MIN-02 | MINOR | Ambiguity | FR-007, US-3.1 | "cheap entities (Capitalized names)" heuristic undefined and locale/false-positive-prone. |
| MIN-03 | MINOR | Ambiguity | FR-007, US-3, "prominent" | "Prominent breadcrumb" placement/form still not pinned (carried from ADR MIN-002). |
| MIN-04 | MINOR | Incompleteness | US-8.2, FR-014, T22 | "Legacy `[dropped N]` marker inert" untested against a real persisted-summary fixture; SetSummary path still exists. |
| MIN-05 | MINOR | Inconsistency | SC-009, §7 regression | "All existing tests pass unchanged" is contradicted by decommissioning `forceCompression` (its tests must change/delete). |
| MIN-06 | MINOR | Incompleteness | OBS-003 (ADR) not carried | No statement that turns evicted by *legacy* `Compact` before this ships are unrecoverable. |
| OBS-01 | OBS | Overcomplexity | FR-008, OBS-002 (ADR) | Consider shipping `turn_range`+`time` first, BM25 as fast-follow — but `time` is itself blocked by MAJ-01. |
| OBS-02 | OBS | Overcomplexity | FR-010 | Prefer a `bm25Score(tokens, …)` core + thin typed callers over generics (keeps retro path monomorphic). |
| OBS-03 | OBS | Incompleteness | H-2 / M-4 | The M-4 model-paging eval has no PASS threshold (what page-rate = pass?). |
| OBS-04 | OBS | Ambiguity | FR-005, "retention sweep is sole deleter" | `JSONLStore.Compact` retained "for manual op" — no operator entrypoint named; risk of accidental reintroduction. |

---

## Detailed Findings

### MAJOR

#### [MAJ-01] `recall_conversation` `time` mode and the breadcrumb timestamp have no data source

- **Lens**: Infeasibility
- **Sections**: FR-007 ("a relative timestamp"), FR-008 (`time` (timestamp window)), US-3.1, US-4.3, T14 (`TestRecallConversation_TimeWindow`), §8 A-4.
- **Evidence**: The recall archive is `context.jsonl`, whose lines are raw `providers.Message`
  values (`pkg/memory/jsonl.go:220` `json.Marshal(msg)`). `providers.Message`
  (`pkg/providers/protocoltypes/types.go:85-93`) is exactly:
  `Role, Content, Media, ReasoningContent, SystemParts, ToolCalls, ToolCallID` — **no `Timestamp`,
  no `CreatedAt`**. The only per-turn timestamp in the system lives in `TranscriptEntry.Timestamp`
  (`session/daypartition.go:147`), i.e. in `transcript.jsonl` — which the spec **explicitly forbids
  recall from reading** (Non-Behavior: "MUST NOT read `transcript.jsonl` for recall replay";
  §2 `ReadTranscript` "do NOT use"). So:
  - FR-008 `time: {from,to}` (US-4.3 / T14) cannot filter on a timestamp the record does not have.
  - FR-007's breadcrumb "relative timestamp" (US-3.1 / T9) has nothing to render.
- **Impact**: One of three recall modes and one required breadcrumb field are unbuildable from the
  chosen corpus. An implementer will either (a) silently drop `time` mode, (b) illegally read
  `transcript.jsonl` (violating the Non-Behavior + retention-deleted), or (c) discover late that a
  persistence-format change is needed — a Constraint #8 wire/format change with a migration story
  for existing logs, none of which is scoped.
- **Recommendation**: Pick and state one explicitly:
  1. **Add a timestamp to the persisted context record** (`providers.Message` +`Timestamp`, or a
     wrapper line type in `context.jsonl`). This is a persistence-format change — enumerate the
     back-compat read for existing timestamp-less lines (treat as zero / unknown) and note whether
     Constraint #8 applies (it is a persisted JSON the recall tool reads, not a gateway/SPA wire
     type — likely internal-only, but say so). Add a dataset row + test.
  2. **Drop `time` mode from v1** and remove the "relative timestamp" from the breadcrumb (or
     replace it with turn-range only). Update FR-007/FR-008, US-3.1/US-4.3, T9/T14, DS-2, DS-3, A-4,
     and the traceability matrix accordingly.
  The spec cannot ship with both "recall reads `context.jsonl` only" and "recall filters by time".

#### [MAJ-02] No exported full-archive (`Skip`=0) read — recall cannot reach evicted turns as specified

- **Lens**: Incompleteness / Infeasibility
- **Sections**: FR-008 ("BM25 over the session's `context.jsonl` turns"), US-4 (all modes), §2 row
  `GetHistory` marked **"Unchanged"** and `TruncateHistory`/`GetHistory` reused as-is; T12/T13/T16.
- **Evidence**: `JSONLStore.GetHistory` (`jsonl.go:266-286`) reads `readMessages(path, meta.Skip)`
  — it returns the **post-Skip live window only**. `readMessages(path, skip)` (`jsonl.go:124`) is
  **private/unexported**. There is **no public method** that reads the full log from line 0. Recall's
  whole purpose (US-4, breadcrumb → "page it back") is to return **evicted** turns — precisely the
  lines `GetHistory` skips. The spec lists `GetHistory` as "Unchanged" and names no new read
  primitive, so as written recall would call the one read that *cannot see* what it must return.
- **Impact**: T12/T13/T14/T16 are unwritable — recall of an evicted turn returns nothing. The
  design's core "no data loss / page it back verbatim" property is not reachable through any API the
  spec authorises.
- **Recommendation**: Add an FR for a new exported read that ignores `Skip` (e.g.
  `JSONLStore.ReadAll(ctx, sessionKey) []providers.Message` calling `readMessages(path, 0)`, or a
  `ReadRange(from,to)`), and thread it through `UnifiedStore`. State that recall uses this
  Skip-ignoring read, never `GetHistory`. Add a symbol-table row and a test asserting recall returns
  a turn whose index `< meta.Skip`. (This mirrors ADR review MIN-005, which the spec left unclosed.)

#### [MAJ-03] Turn→`keepLast` line-count mapping is asserted but never specified; `TruncateHistory` counts messages, not Turns

- **Lens**: Ambiguity / Incorrectness
- **Sections**: FR-001 ("call `TruncateHistory` with the corresponding Turn-aligned `keepLast` line
  count"), FR-002 ("reuse `splitHistoryAtTurnMidpoint`'s boundary detection to map Turns→line
  indices"), US-1.1/US-1.2, T1/T2.
- **Evidence**: `TruncateHistory(keepLast)` (`jsonl.go:323-357`) treats `keepLast` as a **raw line /
  message count**: `meta.Skip = meta.Count - keepLast` (line 351). Turn detection lives in
  `parseTurnBoundaries(history []providers.Message) []int` (`context_budget.go:22`) which returns
  **slice indices** of an in-memory `[]providers.Message`, and `splitHistoryAtTurnMidpoint`
  (`context_budget.go:187`) is a *midpoint* splitter the spec retires. The spec says "map
  Turns→line indices" but gives **no formula**, and crucially the boundary indices are computed over
  the **Skip-trimmed window** returned by `GetHistory` (a slice starting at file-line `Skip+1`),
  while `keepLast` is interpreted against the **whole file** (`meta.Count`). Mixing the two spaces is
  exactly the off-by-`Skip` bug ADR review MAJ-006 warned about.
- **Impact**: An implementer can plausibly compute `keepLast` in window-slice space and pass it to a
  `meta.Count`-relative `TruncateHistory`, landing `Skip` mid-Turn on an already-evicted file →
  provider-invalid replay (400) on the hot path, hard-replace, no fallback. T2
  (`CutsOnTurnBoundary`) would pass on a fresh session and fail on an already-evicted one — a
  latent, load-dependent break.
- **Recommendation**: Give the exact arithmetic. E.g.: "let `window = GetHistory(...)`;
  `boundaries = parseTurnBoundaries(window)`; choose the smallest boundary index `b` such that
  `estimateMessageTokens(window[b:]) + toolDefs + maxTokens ≤ contextWindow·0.95`; then
  `keepLast = len(window) − b`; call `TruncateHistory(keepLast)`." State that `keepLast` is measured
  in *live-window messages* and that `TruncateHistory`'s `keepLast < effective` guard
  (`jsonl.go:349-352`) already interprets it relative to the current window — so the mapping is
  window-relative, not `meta.Count`-relative. Add a T2 dataset row that runs the cut on an
  **already-evicted** session (Skip>0) and asserts the new window's first message is `role:"user"`
  with no orphaned `tool_call_id`.

#### [MAJ-04] Recall provider-validity for BM25/partial selection is under-specified

- **Lens**: Incorrectness / Incompleteness
- **Sections**: FR-008, FR-009, NFR-3 (ADR), US-4.1, US-4.4, SC-003, T12/T15/T17.
- **Evidence**: `query` (BM25) and the `≤8 turns / ≤4000 tokens` bound (FR-009) select a *subset* of
  turns. A `providers.Message` with `Role:"tool"` carries a `ToolCallID` that must match a preceding
  `assistant` message's `ToolCalls[].ID`, or the provider 400s (Integration Boundary: "a torn tool
  pair → 400"). The spec asserts recall returns "provider-valid turns" (US-4.1) and tests replay
  (T17 / SC-003) but **never states the selection unit**: does BM25 rank/return whole Turns, or
  individual messages? If a BM25 hit is a lone `tool` result, or the 8-turn truncation cuts an
  `assistant`+`tool` pair, the returned set is provider-invalid — and it is unclear whether recall
  output is *replayed as messages* or *surfaced as read-only tool output text*.
- **Impact**: T17/SC-003 ("recalled tool-bearing turn replays provider-valid") can fail
  non-deterministically depending on which message BM25 ranks first; the truncation in FR-009 can
  itself tear a pair.
- **Recommendation**: State: (a) recall's atomic unit is a **whole Turn** (never a lone message);
  BM25 ranks Turns (score = max/aggregate over the Turn's messages), truncation drops whole Turns;
  and (b) whether recall output re-enters the provider window as replayable `assistant`/`tool`
  messages **or** is injected as read-only `user`-role quoted text. If the latter, SC-003's
  "provider-valid replay" means the quoted block is well-formed, not that tool pairs re-execute —
  clarify. (ADR review MIN-004, still open.)

#### [MAJ-05] The fit-invariant token accounting is undefined (estimator, reserve, measurement point)

- **Lens**: Ambiguity / Infeasibility (untestable)
- **Sections**: FR-009 ("`pinned + window + recallResult + reserve ≤ contextWindow`"), NFR-5, A-1
  ("5% headroom"), US-4.4, T15/T23.
- **Evidence**: The invariant names four terms but binds none to a computation. The codebase's
  budget check `isOverContextBudget` (`context_budget.go:161`) uses
  `estimateMessageTokens + estimateToolDefsTokens + maxTokens > contextWindow` — note it counts
  **toolDefs**, which the FR-009 invariant omits, and uses `maxTokens` as the reserve, which the
  invariant calls `reserve` without a value. FR-001 separately introduces a "5% headroom of
  `contextWindow`". So there are now two different slack concepts (`maxTokens` reserve vs 5%
  headroom) and toolDefs is dropped from the recall invariant. T23
  (`TestFitInvariantHolds_WindowPlusRecall`) cannot be written without knowing which estimator, whether
  toolDefs is included, and what `reserve` is.
- **Impact**: The anti-thrash safety property (the ADR's one-way-door argument) is unmeasurable;
  T15/T23 are unspecified; an implementer may omit toolDefs and re-overflow when tool defs are large.
- **Recommendation**: Bind every term: reuse `estimateMessageTokens`/`estimateToolDefsTokens`; write
  the invariant as `pinnedTokens + windowTokens + toolDefsTokens + recallResultTokens + maxTokens ≤
  contextWindow` (include toolDefs — it is in the live check), and reconcile the "5% headroom" with
  `maxTokens` (is 5% *in addition to* `maxTokens`, or is it the reserve?). State that recall measures
  `recallResultTokens` with the same estimator before returning and truncates to satisfy the
  inequality.

#### [MAJ-06] Model-switch **downsize** re-window has no restated floor / termination guarantee

- **Lens**: Incompleteness
- **Sections**: FR-011, US-6.1 (downsize), US-6.2 (upsize — confirmed), T19.
- **Evidence**: US-6.2 (upsize: Skip forward-only) is explicitly `[CONFIRMED]` and tested. But
  US-6.1/FR-011 downsize says only "`windowTrim` re-fits the window to the new budget" — it does
  **not** restate the single-huge-Turn floor (FR-003) or the termination guarantee for the switch
  path. `handleModelSwitch` (`loop.go:7659`) today has its own compress/summary/`fitWithinBudget`
  logic (lines ~7728-7803) that FR-011 says to delete; if `windowTrim` is substituted, the "single
  Turn > new (smaller) window" case (very likely on a downsize to a tiny model) needs the same
  last-user-Turn floor and no-infinite-loop guarantee as FR-003, but neither is referenced for the
  switch path. T19 only asserts "re-fit; no summary" — not the floor/termination.
- **Impact**: A downswitch to a small-window model where even the last Turn is over-budget could
  wedge or loop, exactly the FR-003 failure mode, on an untested path.
- **Recommendation**: State that downsize re-window invokes the *same* `windowTrim` (FR-001/FR-003),
  inheriting the last-user-Turn floor and termination. Add a T19 dataset row: downswitch to a model
  whose window is smaller than the last Turn → keeps last user Turn, no loop, no summary.

#### [MAJ-07] No observability for the new eviction/recall hot path

- **Lens**: Inoperability
- **Sections**: whole spec (no monitoring/metrics section; §9 covers offline evals only).
- **Evidence**: This is a hard-replace on the hottest path with an *accepted* silent-loss risk
  (M-4) whose only production signal would be "does the model page?". The spec specifies **no**
  runtime counters: eviction count/session, recall call rate, recall empty/hit rate (the M-4
  detector), archive size growth, `Skip` advancement. §9's H-2 is a pre-merge eval, not a production
  signal. ADR review OBS-004 raised this; the spec did not carry it.
- **Impact**: If M-4 materialises in production (glm-class models are noted reluctant tool-callers),
  on-call has no way to see it — silent context loss is invisible until a user complains.
- **Recommendation**: Add one FR + success criterion for structured counters (log or metric):
  `context_eviction_total`, `recall_conversation_calls_total{result=hit|empty|error}`,
  `context_archive_bytes`, `context_skip_advance_total`. This makes the accepted M-4 risk observable,
  not merely evaluated once.

### MINOR

#### [MIN-01] Wrong call-site line numbers
- **Lens**: Incorrectness — §2 symbol table: forceCompression call sites listed as `loop.go:5075/5846/5981`. Actual: **5078, 5851, 5981** (first two off by 3 and 5). Undermines the "code-cited" precision the spec otherwise earns. **Fix**: correct to 5078/5851/5981.

#### [MIN-02] "Capitalized names" entity heuristic undefined
- **Lens**: Ambiguity — FR-007/A-4 lists "Capitalized names" as a breadcrumb entity. Every sentence-initial word is Capitalized; this yields noise, is locale-dependent, and two engineers will build different extractors. **Fix**: define precisely (e.g. "multi-word runs of Capitalized tokens not at sentence start", or drop it and keep quoted-text + file-paths, which are unambiguous). Add a DS-3 row asserting the expected extraction on a fixed first line.

#### [MIN-03] "Prominent" breadcrumb still not operationalised
- **Lens**: Ambiguity — FR-007/US-3 require a "prominent" breadcrumb as the sole M-4 mitigation, but placement/form is unspecified (carried from ADR MIN-002). **Fix**: pin it — e.g. a dedicated `## Evicted context` block immediately after the pinned core, always naming `recall_conversation`; give plan-spec a literal template and assert it in T9.

#### [MIN-04] Legacy-marker inertness untested against a real fixture
- **Lens**: Incompleteness — FR-014/US-8.2/T22 claim a legacy `[dropped N]` summary "loads fine", but `SetSummary`/`GetSummary` and the summary-injection path in `BuildMessages` still exist (the spec removes the *write* on eviction, not the summary field). T22 must load an actual persisted `.meta.json` with a `[dropped N]` summary and assert it renders inertly (as plain context, not re-parsed). **Fix**: add the fixture to the dataset and make T22 use it.

#### [MIN-05] "All existing tests pass unchanged" contradicts decommissioning
- **Lens**: Inconsistency — SC-009 / §7 say existing tests "pass unchanged", but FR-004/US-8 delete `forceCompression` and its `[dropped N]` path. Any test named for/asserting `forceCompression` or the emergency-compression marker **must change or be deleted** — they cannot "pass unchanged". **Fix**: qualify SC-009 to "all *memory/session/retro* tests pass unchanged; compaction tests are removed/replaced per T4/T21", and enumerate the compaction tests being retired.

#### [MIN-06] Pre-existing-loss honesty missing
- **Lens**: Incompleteness — ADR review OBS-003 noted (correctly) that turns evicted by *legacy* `forceCompression`/`Compact` before this ships are already physically gone and unrecoverable by recall. The spec's "no content is ever lost" (US-1) is true only *going forward*. **Fix**: add an assumption: "recall recovers only turns evicted after this change; content previously dropped by legacy compaction is unrecoverable."

### OBSERVATIONS

- **[OBS-01]** Consider scoping v1 to `turn_range` + BM25 (the two deterministic-from-content modes) and deferring `time` — but note `time` is *blocked* by MAJ-01 regardless, so this may be forced.
- **[OBS-02]** For FR-010, a small `bm25Score(tokens []string, docFreq, …)` core with two thin typed callers (Retro, Message/Turn) is simpler than making `rankRetrosBM25` generic and keeps the hot retro path monomorphic (`retro_bm25.go:29` is `[]Retro`-typed; `retroTokenize` at `:103` is already `string`-generic and reusable as-is).
- **[OBS-03]** H-2 / M-4 eval has no PASS threshold. State the page-rate that constitutes success (e.g. "≥X% of holdout questions whose answer is only in an evicted turn are answered correctly via a recall call").
- **[OBS-04]** FR-005 retains `JSONLStore.Compact` "for tests / potential manual op" but names no operator entrypoint. Either name the command (`omnipus …`) or state it is test-only, to prevent accidental reintroduction into a Save path (a regression that would silently destroy the archive).

---

## Structural Integrity (plan-spec checks)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has ≥1 acceptance scenario | PASS | US-1…US-8 each have numbered ACs. |
| Every AC has a BDD-style Given/When/Then | PASS | ACs are in G/W/T form. |
| Every BDD scenario has a `Traces to:` back-ref | PARTIAL | Traceability is via the §10 matrix + per-US "Independent Test", not inline `Traces to:` on each scenario — acceptable for this format, but US-6.2/US-4.6 have no dedicated matrix row (folded into FR-011/FR-013). |
| Every BDD scenario has a TDD test | PARTIAL | US-3.3 (no-LLM) → T10; US-6.2 (upsize) has **no** distinct test (T19 tests downsize/no-summary only); US-4.6 (empty query) folded into T16. Add explicit coverage for US-6.2. |
| Every FR in the traceability matrix | PASS | FR-001…FR-015 all present in §10. |
| Every BDD scenario in the matrix | PARTIAL | Matrix rows are FR-keyed; some sub-ACs (US-6.2, US-1.3 termination) are implicit. |
| Datasets cover boundary/edge/error | PARTIAL | DS-1/2/3 are good, but **no dataset row for a timestamped `time`-mode query** (blocked by MAJ-01) and **no already-evicted-session row** for the Turn-boundary cut (MAJ-03). |
| Regression impact addressed | PARTIAL | §7 addresses it but SC-009 overclaims "unchanged" (MIN-05). |
| Success criteria measurable, no subjective language | FAIL | SC-008 depends on the fit invariant (MAJ-05, undefined terms); "prominent" (MIN-03) unmeasured; the M-4 eval (H-2) has no threshold (OBS-03). |

---

## Test Coverage Assessment

| Category | Gap | Requirement |
|----------|-----|-------------|
| `time` mode | T14 unbuildable — no timestamp in the archive record | FR-008, US-4.3, MAJ-01 |
| Full-archive read | No test that recall reads a turn at index `< meta.Skip` (evicted) via a Skip-ignoring read | FR-008, MAJ-02 |
| Turn-boundary cut on an **already-evicted** session | T2 only tests a fresh cut; the off-by-Skip case is untested | FR-002, MAJ-03 |
| Recall provider-validity of a BM25/truncated subset | T17 tests one tool turn; not a lone `tool` result or a truncation that tears a pair | FR-008/FR-009, MAJ-04 |
| Fit-invariant arithmetic | T23 asserts an inequality whose terms are undefined | FR-009, MAJ-05 |
| Downsize re-window floor/termination | T19 omits the single-huge-Turn downsize | FR-011, MAJ-06 |
| Observability counters | none | MAJ-07 |
| Model-paging (M-4) threshold | H-2 has no pass condition | OBS-03 |

Positives worth noting: the TDD plan correctly front-loads unit tests for `windowTrim` (T1-T4),
integration for durability (T5-T6) and recall (T12-T17), and a decommission grep test (T21) — the
*shape* of the plan is sound; the gaps are missing rows/undefined terms, not a missing strategy.

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| append-only `context.jsonl` (Skip eviction) | ok | ok* | ok | ok | risk | ok | T mitigated: `Compact` removed from the single Save sink (`unified.go:757`, `jsonl_backend.go:75`) neutralises all 7+ callers — **verified, MAJ-002 of the ADR review is genuinely closed**. D: unbounded growth on active heartbeat sessions until they idle (accepted, bounded by 90-day sweep). |
| `recall_conversation` tool | ok | ok | ok | ok | risk | ok | Session-scoped read of a local file — no new network/auth surface. I: cross-session leakage prevented by FR-013/session-id validation (good). D: unbounded output re-overflow *if* the fit invariant is left undefined (MAJ-05). No agent-scoping claim (correctly downgraded to session-scoped — resolves ADR MAJ-003). |
| `.context/` retention sweep | ok | ok | ok | ok | risk | ok | ModTime-based; an *active* session has a fresh mtime (written every turn) so is spared — the spec's US-7.2 is consistent with the code (`retention_sweep.go:84`). D residual: a genuinely-live-but-idle heartbeat session with no appends for 90d would be swept — acceptable per ADR, but worth a one-line note. |

Local, in-process, no new auth/network boundary → classic S/R/E network threats are N/A. Real weight
is on Tampering (archive integrity — **now well-handled**), DoS (recall re-overflow — **gated by the
still-undefined MAJ-05 invariant**), and the M-4 silent-loss product risk (observability gap MAJ-07).

---

## Unasked Questions

1. Where does the `time`-mode filter and the breadcrumb "relative timestamp" get a timestamp, given `providers.Message` has none and `transcript.jsonl` is forbidden for recall? (MAJ-01)
2. What is the exact read API recall uses to see *evicted* turns, since `GetHistory` honours `Skip` and no full-archive reader is exported? (MAJ-02)
3. What is the precise arithmetic converting a chosen Turn boundary into the `keepLast` value passed to `TruncateHistory`, and is it measured in window-slice space or `meta.Count` space? (MAJ-03)
4. Is recall's atomic selection/return unit a whole Turn, and does its output re-enter the provider window as replayable messages or as read-only quoted text? (MAJ-04)
5. In the fit invariant, which estimator, is `toolDefsTokens` included, what is `reserve`, and how does the 5% headroom relate to `maxTokens`? (MAJ-05)
6. On a downsize model switch where even the last Turn exceeds the new window, does `windowTrim` inherit FR-003's floor and termination? (MAJ-06)
7. What runtime signal tells on-call the model is *not* paging (the M-4 detector)? (MAJ-07)
8. What page-rate constitutes a PASS for the H-2 / M-4 eval? (OBS-03)

---

## Verdict

**REVISE.**

The direction is well-grounded and the spec's hardest structural claims are code-verified true —
notably that append-only `context.jsonl` + disabling `Compact` at its single sink genuinely closes
the ADR's data-loss finding. But seven MAJOR gaps make specific FRs unimplementable or untestable as
written: the `time` mode and breadcrumb timestamp have **no source in the chosen corpus** (MAJ-01);
recall has **no exported way to read the evicted turns it exists to return** (MAJ-02); the
Turn→line-count mapping — the exact thing ADR review MAJ-006 flagged — is **still asserted, not
specified**, over a store that counts messages not Turns (MAJ-03); and recall provider-validity,
the fit invariant, downsize termination, and observability are under-specified (MAJ-04…07). These
are gaps a revision can close cleanly; none contradicts the direction.

Review written to: `docs/internal/specs/context-paging-sliding-window-recall-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/context-paging-sliding-window-recall-spec.md docs/internal/specs/context-paging-sliding-window-recall-spec-review.md
```
