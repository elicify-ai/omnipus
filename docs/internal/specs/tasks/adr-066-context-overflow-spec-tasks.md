# Tasks — ADR-066 spec (context overflow: window resolution, per-result cap, empty-in-place, mid-turn window check)

- **Spec:** `docs/internal/specs/adr-066-context-overflow-spec.md` (revision 2, 2026-08-22). FR ids, BDD ids (B-nn), test numbers and dataset ids (DS-n) below are the spec's own.
- **ADR:** `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` (§17 exit proofs; §12 contract impact).
- **Plan:** `docs/internal/specs/implementation-plan-adr-066-067-068.md` — this list is **stream B4 (context backend)**. The SPA work for D9 (Settings → Models context section) and the chat rendering of `content_state` / the recall mark / the `user`-attribution notice is **stream B5** and is referenced as a cross-spec dependency at the end, not duplicated here.
- **Citation rule:** `pkg/agent/loop.go` and `pkg/agent/turn.go` are cited as `file::symbol` only. Every other path is a plain path.
- **Gate vocabulary:** `quick | lint | go-test | contracts | spa` = the Fly `runci.sh <ref> <gate>` gates. Never the full Go suite on the dev machine.

## Landing-order dependencies (cross-spec)

| Alias | What it is | Why this list needs it |
|---|---|---|
| **A-CONTRACT** | Wave A, the single coordinated contract commit owned by ADR-067 (plan §1): `ContextSettings` / `ContextSettingsUpdate`, `ContextWindowSource.yaml`, `Agent.yaml` window fields (`context_window_effective` / `_source` / `_clamped`), `AgentUpdateRequest.context_window_override`, `GET/PUT /settings/context`, and the **four** `LLMError` copies with `turn_canceled`, `turn_timed_out`, `context_unrecoverable`, `context_window_unknown` + the `user` attribution; regenerated artefacts committed atomically. | Every task that compiles against a generated type. |
| **T067-RESOLVER** | ADR-067's catalog task(s) delivering `pkg/providers/catalog.Resolve(provider, model).Window()`, the `locality` predicate and the `cli_driver` field (spec §3, X-14/X-16). *(The concrete `T067-xx` id is assigned by the ADR-067 task list; substitute it when that list lands — unverified at the time of writing because the 067 list was being produced in parallel.)* | T066-09 (catalog rung, locality, exempt-by-driver) and T066-17 (catalog `GET` projection fields). |
| **T067-CATALOG-GET** | ADR-067's providers-catalog `GET` route. | T066-17 adds `window_source` / `window_unknown` per model to it (X-08). |
| **B5-SPA** | Stream B5 (ADR-068 §4–5 + ADR-066 D9 UI): Settings → Models context section, `content_state` rendering under Verbose, projection-frame zod edge, `llm-error.test.ts` `user` attribution. | Delivers spec tests 46, 46b, 47, 48 — listed in the "delivered elsewhere" section, not as tasks here. |

Spec landing order: **S67 → S68 → S66 backend (this list) → S66 UI tail (B5)**. Within this list, every task's `depends-on` precedes it.

---

## Task list (landing order)

### T066-01 — S66-owned contract additions (everything A-CONTRACT does not carry) — **P0**
- **Files:** `contracts/components/schemas/ToolCall.yaml` (`content_state: full | capped | emptied`, default `full`; transcript `result` content bounded by D4), `contracts/asyncapi.yaml` (`tool_result_projection` frame `{tool_call_id, archive_line, content_state, mark}`; inline recall-mark and argument-refusal schemas in the ADR-060 family), `scripts/check-no-handwritten-wire-types.sh` (two new family register entries), `pkg/api/generated/`, `src/lib/api/generated/` (regenerated via `scripts/gen-contracts.sh`), `pkg/api/generated/contract_test.go` (additions). Verify — do not re-edit — that A-CONTRACT already carries `ContextSettings*.yaml`, `ContextWindowSource.yaml`, the `Agent.yaml`/`AgentUpdateRequest.yaml` fields and the four `LLMError` copies.
- **FRs:** FR-022 (schema half), FR-034 (four-copy verification only), FR-036 / FR-037 (schemas present), FR-016 / FR-018 (schema half of the two typed producers), FR-046 (transcript `result` field).
- **BDD:** B-26, B-41, B-44 (contract rows).
- **Tests first:** `contract_test.go` additions (test 44); `TestContract_LLMError_AllClassifierCodesRoundTrip` and `TestContract_LLMErrorCatalogue_AllFourCopiesAgree` must pass unchanged.
- **Gate:** `contracts`.
- **Depends-on:** A-CONTRACT.
- **Size:** S.
- **DoD:** `make verify-contracts` exits 0 with the new schemas, spec + generated diff in one commit, the two ADR-060 register entries lint-clean.

### T066-02 — `pkg/memory` primitives: projection state, emptied-set rollback, `SetHistory` refusal — **P0**
- **Files:** `pkg/memory/jsonl.go` (`sessionMeta` gains projection state keyed `(tool_call_id, archive_line)` → `capped | emptied`, plus `hydrated bool`; `TruncateHistory` prunes entries with `archive_line < Skip`; `RollbackAppended(ctx, key, targetLines, targetSkip, emptiedSet)` restores all three atomically, dropping entries with `archive_line ≥ targetLines`; `SetHistory` refuses a non-empty file and never resets `Skip` on an existing archive), `pkg/memory/store.go` (interface), `pkg/agent/subturn.go` (`ephemeralSessionStore.RollbackAppended` — in-memory projection state, new parameter a no-op), every `Store` fake/adapter in `pkg/agent/*_test.go`, `pkg/gateway/*_test.go`, `pkg/session/` (compile), `pkg/agent/turn.go::restoreSession` (pass the turn-start set; the capture itself lands in T066-12).
- **FRs:** FR-019 (meta half), FR-020 (store half), FR-047.
- **BDD:** B-12, B-24, B-27, B-29b, B-53c.
- **Tests first:** `TestSessionMeta_ProjectionStateCompositeKey` (15), `TestRollbackAppended_RestoresTurnStartEmptiedSet` (16), `TestSetHistory_RefusesNonEmptyArchive` (57). Existing `SetHistory` callers in tests must be switched to empty stores (regression table).
- **Gate:** `quick`.
- **Depends-on:** none (pure Go, no wire type).
- **Size:** M.
- **DoD:** the three tests pass, every `Store` implementation compiles with the new signature, and `SetHistory` on a 1-line archive returns an error with `skip` untouched (DS-10 #8).

### T066-03 — `config.ContextSettings` + the one budget B — **P0**
- **Files:** `pkg/config/config.go` (new `ContextSettings` struct: `mcp_result_cap` 62,500 · `builtin_success_cap` 64,000 · `builtin_failure_cap` 10,000 · `warn_threshold` 25,000 · `absolute_trigger_chars` 400,000 · `ingest_bound_bytes` 8,000,000 · `default_context_window` unset · `model_overrides[]`; **delete** `AgentDefaults.SummarizeTokenPercent`), `pkg/config/defaults.go` (seed), `pkg/config/config_test.go`, `pkg/agent/context_budget.go::isOverContextBudget` (threshold becomes **B**), `pkg/agent/loop.go::windowTrim` (budget formula extracted into a shared helper — formula unchanged: `B = W − max_tokens − ceil(0.05·W) − pinnedCoreOverhead`), `pkg/agent/loop.go` timeout-recovery site (uses B; the `SummarizeTokenPercent` scaling goes), `pkg/agent/context_budget_test.go`.
- **FRs:** FR-028 (budget + threshold; pre-turn and timeout-recovery sites), FR-004 (the `summarize_token_percent` deletion), FR-010 (configured values live in one place), FR-036 (config half).
- **BDD:** B-06, B-38, B-44 (defaults).
- **Tests first:** `TestConfig_NoContextWindowDefaultKey` (22 — the `summarize_token_percent` half; the `context_window` half is asserted again in T066-09), `TestIsOverContextBudget*` updated to threshold B, `TestMidTurnBudget_SameBudgetAsWindowTrim` (19 — the "B only, `SummarizeTokenPercent` absent" half; its mid-turn half is re-asserted in T066-13).
- **Gate:** `quick`.
- **Depends-on:** A-CONTRACT (field names mirror the generated `ContextSettings`).
- **Size:** M.
- **DoD:** `grep -rn SummarizeTokenPercent pkg/` is empty, `isOverContextBudget` compares against B at the pre-turn and timeout-recovery sites, and a stale `summarize_token_percent` key in `config.json` is silently ignored (greenfield rule).

### T066-04 — Typed producers: cap mark, recall mark, argument refusal — **P0**
- **Files:** `pkg/tools/result.go` (three producers via `marshalWithinBudget`, ADR-060 family shape, `*Code` consts), `pkg/agent/recall_mark.go` (new: name and `tool_call_id` sanitised to ≤ 64 printable chars, archive line, size in chars, turn number = 1 + preceding `role: user` archive lines, recall hint), `pkg/agent/recall_mark_test.go`.
- **FRs:** FR-018, FR-016 (producer half — the refusal message shape; enforcement is T066-15), FR-011 (the cap mark whose length counts toward the cap).
- **BDD:** B-21 (mark contents), B-19 (refusal shape).
- **Tests first:** `TestRecallMark_SingleProducerSanitised` (13), `TestToolArgsCap_StructuredRefusal` (12 — family-shape half).
- **Gate:** `lint` (the `check-no-handwritten-wire-types.sh` register) then `quick`.
- **Depends-on:** T066-01.
- **Size:** S.
- **DoD:** no `fmt.Sprintf` assembles either mark or the refusal; a hostile MCP tool name (E6) is sanitised; lint register passes.

### T066-05 — The choke point: one function admits every tool result — **P0**
- **Files:** `pkg/agent/tool_result_admit.go` (new: filter → per-surface cap → clamp `min(configured, floor(0.5·B·2.5))` → `/N` for parallel calls when they would not fit → 50/50 head-and-tail with the mark counted, no rune split → encoded-line bound `len(json.Marshal(ArchivedMessage)) ≤ 0.8 × maxLineSize` → archive append of the full filtered content + `capped` state → warn-threshold WARN + `tool_result_large_total`; settings read per call; writes the bounded `result` into the transcript `tool_call` entry), `pkg/agent/projection.go` (new: the one pure projection function applying `(id, line) → capped | emptied` to a history slice, used by `loop.go::assembleMessages` and, from T066-13, mid-turn), `pkg/agent/loop.go` — the success-path `toolResultMsg`, the seven `deniedMsg` sites and the `skippedMsg` site all call the function; `pkg/agent/attach_hydrate.go` (hydrated-attachment surface), `pkg/agent/recall_conversation.go::buildRecallSpanMessages` (recall-page surface), `pkg/agent/repair.go` (exempt — bounded by construction, annotated), `pkg/agent/loop.go::assembleMessages` (applies the projection), `pkg/gateway/metrics.go::toolMetrics` (`tool_result_large_total`).
- **FRs:** FR-009, FR-010, FR-011, FR-012, FR-013, FR-019 (the `capped` state + pure projection function, capped view), FR-046 (the choke point writes the bounded transcript `result`).
- **BDD:** B-11, B-11b, B-11c, B-12, B-12b, B-13, B-16, B-16b.
- **Tests first:** `TestChokePoint_PerSurfaceCap` (7), `TestChokePoint_ClampToHalfBudget` (8), `TestChokePoint_EncodedLineBound` (9), `TestChokePoint_FilterThenCap_AtRealCuts` (10), `TestChokePoint_ProducerListByGrep` (11 — asserts all twelve `Role: "tool"` sites call the function, repair exempt), `TestProjection_PureFunction` (14 — capped view; extended with the emptied view in T066-12). Datasets DS-1 #1–#16.
- **Gate:** `go-test`.
- **Depends-on:** T066-02, T066-03, T066-04.
- **Size:** L.
- **DoD:** a 1,178,522-char MCP result enters at ≤ 62,500 chars with the archive line holding the full filtered content, reload renders the capped form byte-identical (B-12), and the producer-list grep test fails if any new `role: "tool"` site bypasses the function.

### T066-06 — D5.5: hydration fills only an empty archive; standalone `tool_call` reconstruction — **P0**
- **Files:** `pkg/gateway/websocket.go` (attach_session path: skip `HydrateAgentHistoryFromTranscript` when the agent archive has ≥ 1 line), `pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript` (reconstruct standalone `type: "tool_call"` transcript entries as `ToolCall`s on the preceding assistant message of the same `turn_id`/agent; synthetic assistant message + WARN when none exists; exactly one `role: "tool"` line per recorded call carrying the entry's bounded `result` via the choke point; set meta `hydrated: true`), `pkg/agent/loop.go` self-heal site (emptiness condition **unchanged**), `pkg/agent/attach_hydrate_test.go`, `pkg/gateway/attach_hydrate_test.go` (scoped), the ADR-028 append-only invariant test (`pkg/agent/window_trim_test.go` / `TestArchive_*` — gains an attach step).
- **FRs:** FR-045, FR-046, FR-048 (FR-047 landed in T066-02; the recall-by-id "not available — session was rebuilt from the transcript" answer that reads the `hydrated` flag lands in T066-14).
- **BDD:** B-52, B-53, B-53b, B-53d.
- **Tests first:** `TestAttach_TwiceArchiveByteIdentical` (55 — `pkg/gateway`, **one scoped run only**), `TestAttach_EmptyArchiveHydratesStandaloneToolCalls` (56 — fixture in the **real** transcript shape: assistant entries without `tool_calls`, standalone `tool_call` entries; DS-10 #6, #6b, #7), `TestArchive_AppendOnlyWithAttachStep` (58).
- **Gate:** `go-test`.
- **Depends-on:** T066-01, T066-02, T066-05.
- **Size:** M.
- **DoD:** attaching the same session twice leaves archive bytes and `meta.skip` identical; an empty archive hydrates once with one tool line per standalone entry attached to the right assistant message (SC-015).

### T066-07 — D5.4: recall content is spliced into the next request at the tool-result site — **P0**
- **Files:** `pkg/agent/loop.go` tool loop (recall tool-result site: after the recall result is appended to `messages`, run the fit check against B; if it fits, splice `span.Messages()` at the `BuildMessages` position — after the pinned core, before the window — and run `sanitizeHistoryForProvider` on the combined slice; on a second recall in the same turn remove the replaced span from the slice first, E20), `pkg/agent/turn.go` (`turnState.injectedRecallSpan`, compared by identity), `pkg/agent/loop.go::assembleMessages` (skips an already-injected span), `pkg/agent/context.go::BuildMessages` / `::sanitizeHistoryForProvider` (position + sanitisation reused, not duplicated), `pkg/agent/recall_conversation.go` (receipt *"Recalled N turn(s) (turns A–B); their text is now in your context"* only when injected; non-fit text *"N turn(s) found (X estimator tokens) but they do not fit the current window; narrow with turn_range or query"*; INFO line on set / injected / refused / dropped with sizes), `pkg/agent/recall_injection_test.go` (new), `pkg/agent/subturn_recall_limitation_test.go` (new), existing `TestRecallConversation_*` / `TestRecallSpan_*` string assertions updated to the new receipt.
- **FRs:** FR-041, FR-042, FR-043 (all but the per-page `tool_call_id` clause, which lands with the mode in T066-14; the D5/D6 subjection clause is asserted in T066-13), FR-044.
- **BDD:** B-48, B-49, B-50, B-50c, B-51.
- **Tests first:** `TestRunTurn_RecallInjected_NonceInSecondRequest` (49 — fake recording provider; nonce evicted past `Skip` in turn 1; provider call 1 returns `recall_conversation(turn_range:"1-1")`; provider request 2 contains the nonce and the marker), `TestRunTurn_RecallNonFit_ToolResultStatesIt` (50), `TestRunTurn_RecallNotDoubledOnReassembly` (51), `TestSubTurn_RecallReadsParentStore_KnownLimitation` (54 — pins the empty outcome and the INFO line; not fixed here). DS-10 #1–#4.
- **Gate:** `go-test`.
- **Depends-on:** T066-03 (fit check at B), T066-05 (the recall page is a builtin-success surface).
- **Size:** M.
- **DoD:** SC-014 — the nonce is in the provider's second request after recall; with a too-small window the request lacks it and the tool result states the non-fit; no receipt ever says "into context" unless the text is in the next request.

### T066-08 — Per-tool cap alignment (D4 figures) — P1
- **Files:** `pkg/tools/browser/tools.go` (`maxGetTextBytes` 100 KiB → 64,000 chars), `pkg/tools/shell.go` (`maxForegroundOutputLen` 10,000 stays for the failure path; success path 64,000), `pkg/tools/filesystem.go::MaxReadFileSize` (64 KB — unchanged, asserted), `pkg/tools/web.go::defaultMaxChars` (50,000 — unchanged under the cap), the tool tests asserting the old numbers.
- **FRs:** FR-014.
- **BDD:** B-15.
- **Tests first:** the B-15 rows of `TestChokePoint_PerSurfaceCap` (7) plus the updated `browser_get_text` / shell unit tests.
- **Gate:** `quick`.
- **Depends-on:** T066-03 (the cap constants come from `ContextSettings`).
- **Size:** S.
- **DoD:** `browser_get_text` returns ≤ 64,000 chars, shell success ≤ 64,000 / failure ≤ 10,000, `read_file` 64 KB unchanged; no per-tool opt-out added.

### T066-09 — D2/D3: `ResolveWindow` ladder, clamps, floor, locality, exempt-by-driver, greenfield deletions — **P0**
- **Files:** `pkg/agent/resolve_window.go` (new: `ResolveWindow(provider, model, agentID="")` — per-agent override (only with `agentID`) → `ContextSettings.model_overrides[]` → `default_context_window` → live cache (rung wired in T066-10; until then skipped) → `catalog.Resolve(provider, model).Window()` → floor; `effective = min(chosen, capability)`, `clamped`, source `operator | live | catalog | floor`; the single `cloudWindowFloor = 128000` constant; `locality: local` → no floor, refusal when no live value; subprocess `cli_driver` → `0`, no source; dead override for a deleted provider ignored), `pkg/agent/instance.go::NewAgentInstance` (calls the resolver; **delete** the `maxTokens * 4` fallback; `max_tokens` clamped to `floor(W/4)` with one WARN when B would be ≤ 0; `ContextWindow` read under the instance mutex), `pkg/agent/loop.go` model-switch re-window (`newContextWindow = 128000` **deleted**, consolidated onto the resolver), `pkg/agent/loop.go::runTurn` pre-turn gate (`context_window_unknown`, attribution `config`, **third** after `needs_provider` and `model_unassigned`; exempt providers skip pre-turn trim and every budget check), `pkg/config/config.go` (**delete** `AgentDefaults.ContextWindow` and `OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW`), `pkg/agent/translate_error.go` (`context_window_unknown` copy; `contextOverflowSubstrings` classifies only — D8 not adopted), `pkg/agent/window_trim_test.go::TestDecommission_NoForceCompressionSymbols` (extended with the SC-009 grep), `TestModelSwitch_*` updated to the ladder, `pkg/agent/subturn_target_identity_test.go` (additive: child window from the target's provider/model — after S67 re-keys the `mock` fixture, X-31).
- **FRs:** FR-001 (all rungs except the live fetch itself), FR-002, FR-004 (remaining deletions: `maxTokens * 4`, both `128000` fallbacks, `agents.defaults.context_window` + env var; one `cloudWindowFloor`), FR-005, FR-005b, FR-006, FR-007, FR-008 (refusal code, message and gate order — the projection and reload halves are T066-17), FR-035.
- **BDD:** B-01, B-02, B-02b, B-03, B-03b, B-04b, B-04c, B-05, B-05b, B-06, B-07, B-08, B-09 (refusal half), B-10b.
- **Tests first:** `TestResolveContextWindow_Ladder` (1 — catalog rows skipped, not faked, until S67 is merged; X-27), `TestResolveContextWindow_ClampAllRungs` (2), `TestResolveContextWindow_ExemptByCliDriver` (3 — by field, never by id; no deleted-provider literal), `TestNewAgentInstance_MaxTokensClampedWhenBudgetNonPositive` (3b), `TestResolveWindow_NoAgent` (3c), `TestResolveContextWindow_ByLocality` (4 — fixture rows ollama / lmstudio / custom loopback / custom public), `TestWindowAgreement_OneBudgetAllSites` (6 — source grep), `TestTranslateError_NoWindowLearning` (21), `TestConfig_NoContextWindowDefaultKey` (22 — `context_window` half). DS-4 #1–#6, #8–#16.
- **Gate:** `go-test`.
- **Depends-on:** A-CONTRACT, **T067-RESOLVER**, T066-03.
- **Size:** L.
- **DoD:** SC-009 grep is empty with exactly one `cloudWindowFloor`; the four consumers (pre-turn, mid-turn, timeout-recovery, model-switch) read one resolved window; an unknown local endpoint is refused with the exact Settings → Models → Model overrides message, never floored (SC-007 backend half).

### T066-10 — Live limits query, on demand, cached 24 h — **P0**
- **Files:** `pkg/agent/live_limits.go` (new: `$OMNIPUS_HOME/cache/model_limits.json`, key `(provider id, base URL, model)`, TTL 24 h, provider credential from the credential store, rung skipped without one; queried only at the first resolution reaching the rung — never at boot, never on a timer, never on the turn path; cold cache → next rung now, live value at the next reload; Ollama `/api/ps` for `locality: local`; the exact Ollama field name is **unverified** in the ADR §16 — confirm against a running daemon before pinning the fixture), `pkg/agent/resolve_window.go` (wire the rung), `pkg/agent/live_limits_test.go` (mocked HTTP).
- **FRs:** FR-003, FR-001 (live rung), FR-007 (the mandatory live query for local).
- **BDD:** B-04, B-08.
- **Tests first:** `TestLiveLimits_OnDemandCacheKeyTTLCredential` (5 — one fetch within 24 h; no-credential skip; **zero** fetches at boot and across a 25 h idle period), the B-08 row of test 1. DS-4 #7, #9.
- **Gate:** `quick`.
- **Depends-on:** T066-09.
- **Size:** M.
- **DoD:** resolving twice within 24 h performs one fetch; boot and idle perform none; a tampered cache can only lower the window (E17, clamp).

### T066-11 — D7: typed turn exits — P1
- **Files:** `pkg/agent/loop.go::runTurn` — the four silent `"turn canceled"` / `"turn timed out"` return sites (cancelled context → `turn_canceled`, attribution `user`; deadline → `turn_timed_out`, `provider`; each with a log line carrying the raw cause, an `EventKindTurnEnd` event and a transcript entry), `pkg/agent/translate_error.go` (`CodeTurnCanceled`, `CodeTurnTimedOut`, `CodeContextUnrecoverable` — the guard itself fires in T066-13 — and the attribution table with `user`), `pkg/agent/translate_error_test.go`, `pkg/gateway/replay.go` (existing `role:"turn_canceled"` replay frame retained; the `LLMError` is additional).
- **FRs:** FR-034 (backend; the SPA neutral-notice half is B5-SPA).
- **BDD:** B-40, B-41.
- **Tests first:** `TestTranslateError_TypedExitsAndAttributions` (20), `TestRunTurn_SilentExitsNowTyped` (36 — four sites: ≥ 1 log line, 1 turn-end event, 1 transcript entry, never `unknown`).
- **Gate:** `go-test`.
- **Depends-on:** A-CONTRACT.
- **Size:** M.
- **DoD:** SC-006 — each of the four silent returns produces the three artefacts with its typed code; `TestContract_LLMError_AllClassifierCodesRoundTrip` passes with the new classifier codes.

### T066-12 — D5: empty in place, persisted projection, turn-start restore point, projection frame — **P0**
- **Files:** `pkg/agent/empty_in_place.go` (new: eligible = any `role: "tool"` whose call is in the window, including earlier turns kept by the pre-turn floor, excluding the floor set = every result of the most recent assistant message; empty oldest-first by replacing content with the recall mark in the in-memory slice before `callMessages` is built; persist `(id, line) → emptied`; emit `tool_result_projection`; one INFO line with session key, count, share before/after; `context_empties_total`), `pkg/agent/projection.go` (emptied view), `pkg/agent/turn.go` (`turnState.initialEmptiedSet` captured once in `newTurnState` beside `initialArchiveLen` / `initialHistoryLength`; **delete** `turn.go::refreshRestorePointFromSession` and `turnState.restorePointHistory` — verified dead; `restoreSession` passes the turn-start triple to `RollbackAppended`), `pkg/agent/loop.go::windowTrim` (pre-turn: after the floor keeps an oversized last turn, its results are emptied oldest-first — register #3 / B-21b), `pkg/gateway/replay.go` (transcript read returns projected content + `content_state`; full content stays in `tool_results/` for Verbose chat), `pkg/gateway/metrics.go::toolMetrics` (`context_empties_total`), `pkg/gateway/websocket.go` (frame emission), `pkg/agent/window_trim_test.go` (`TestWindowTrim_SingleHugeTurn_KeepsLastUser` **updated**: Skip unchanged, marks present; `TestDecommission_*` grep extended with `refreshRestorePointFromSession` / `restorePointHistory`), `TestProjection_NeverOrphans` (new, beside `TestRecovery_*`).
- **FRs:** FR-017, FR-018 (applied), FR-019 (emptied view, live = reload), FR-020 (turn-side capture + deletions), FR-022 (backend: frame, transcript `content_state`, `tool_results/` retention), FR-023.
- **BDD:** B-21, B-21b, B-22, B-23, B-24, B-26 (backend), B-27, B-27b.
- **Tests first:** `TestProjection_PureFunction` (14 — emptied view), `TestRunTurn_LiveVsReloadBytesEqual` (32), `TestRunTurn_AbortRestoresTurnStartTriple` (33 — after two emptying passes), `TestGateway_ProjectionFrameAndContentState` (43), `TestWindowTrim_SingleHugeTurn_KeepsLastUser` (updated), `TestProjection_NeverOrphans`.
- **Gate:** `go-test`.
- **Depends-on:** T066-02, T066-04, T066-05.
- **Size:** L.
- **DoD:** SC-003 and SC-010 — an aborted turn restores archive length, `Skip` and the emptied-set to their turn-start values; the bytes sent to the provider for an emptied message equal the reload assembly; no archive line changes.

### T066-13 — D6: the mid-turn window check after every result; floor; guard — **P0**
- **Files:** `pkg/agent/loop.go::runTurn` tool loop (after each admitted result: `isOverContextBudget` at B and `share > absoluteShare` (= `absolute_trigger_chars ÷ 2.5`, 160,000 default); on fire → T066-12's emptying to 80 % of the fired condition or until no eligible result remains; **never** advances `Skip` mid-turn; floor = the whole last assistant step; guard → `context_unrecoverable` (attribution `product`), one ERROR line, no further provider call — reachable only by an injected fault; fixed order ingest bound → filter → cap/clamp + line bound → archive append + state → check → empty → assemble → call), `pkg/agent/context_budget.go` (share computation; `parseTurnBoundaries` unchanged), `pkg/agent/loop.go::windowTrim` (pre-turn site unchanged in operation: cut, then empty), `pkg/agent/midturn_budget_test.go` (new), `pkg/agent/runturn_context_test.go` (new integration tests).
- **FRs:** FR-021, FR-028 (mid-turn site), FR-029, FR-030, FR-031, FR-032, FR-033; FR-043's "an injected span is subject to D5/D6" clause.
- **BDD:** B-25, B-33, B-34, B-35, B-36, B-36b, B-37, B-38, B-39, B-50d.
- **Tests first:** `TestMidTurnBudget_OperationBySiteAndPosition` (17), `TestMidTurnBudget_TriggerTargetStop` (18), `TestMidTurnBudget_SameBudgetAsWindowTrim` (19 — mid-turn half), `TestRunTurn_GuardTest_2MBResultCompletes` (30), `TestRunTurn_LongTurn_50CallsAtCap_SmallWindow` (31 — also asserts archive bytes unchanged, B-23), `TestRunTurn_ThrashGuard_InjectedFaultOnly` (34 — provider call count after guard = 0; not reachable across DS-5 without the fault), `TestRunTurn_MidTurnNeverAdvancesSkip` (39), `TestRunTurn_SmallWindowClamp` (40 — 8,192 window), `TestRunTurn_InjectedSpanSubjectToD5` (53). DS-5 #1–#9.
- **Gate:** `go-test`.
- **Depends-on:** T066-05, T066-11 (typed-exit plumbing for the guard), T066-12.
- **Size:** L.
- **DoD:** SC-001, SC-002, SC-008, SC-011 — a 2 MB result and a 50-call turn at the cap complete with every request ≤ B, the last step intact, `Skip` never moving mid-turn, and the guard reached only under an injected fault.

### T066-14 — Recall by `tool_call_id`, in pages — **P0**
- **Files:** `pkg/agent/recall_conversation.go::RecallConversationTool` (`tool_call_id` mode with optional `archive_line ≥ 0`, `offset ≥ 0`, `length ≥ 1` in runes, clamped to the page; exactly one of `query | turn_range | time | tool_call_id`; payload = effective cap − mark framing; streaming archive scan via `pkg/memory/jsonl.go::ReadArchive` stopping at the match; most recent line wins on duplicate ids unless `archive_line` given; unknown / rolled-back id → tool error naming the id; exempt from the 4,000 / 8,000 span budgets, counted by D6, emptiable; each page injected via the T066-07 path; on a `hydrated: true` session answers *"not available — session was rebuilt from the transcript"*), the tool schema (new parameters, **no policy change**), `pkg/agent/recall_conversation_test.go`.
- **FRs:** FR-024, FR-025, FR-026, FR-027, FR-043 (per-page injection clause), FR-046 (the "not available" answer).
- **BDD:** B-28, B-29, B-29b, B-30, B-31, B-31b, B-50b, B-53b.
- **Tests first:** `TestRecallConversation_ToolCallID_PageFitsAfterFraming` (26), `TestRecallConversation_ToolCallID_PagingReachesLastByte` (27), `TestRecallConversation_ToolCallID_DuplicateIds` (28), `TestRecallConversation_ToolCallID_ExemptNotFoundExclusiveStreaming` (29), `TestRunTurn_RecallByIdPageInjected` (52). DS-6 #1–#8.
- **Gate:** `go-test`.
- **Depends-on:** T066-05, T066-06, T066-07, T066-12 (rolled-back ids must be absent — E7).
- **Size:** M.
- **DoD:** paging a 1,178,522-char archived result reaches the last byte, every page is ≤ the cap after framing and passes the choke point unmodified, and an id from an aborted turn is "not found".

### T066-15 — User-message bound in `processMessage`; tool-argument refusal — P1
- **Files:** `pkg/agent/loop.go::processMessage` (a message whose text — media refs excluded — exceeds the builtin-success cap is answered on the originating channel with N and the live limit, **before** turn registration and persistence; no transcript entry, no turn id, no error frame), `pkg/agent/loop.go::runTurn` tool dispatch (serialised arguments > 64,000 → the T066-04 structured refusal, tool not executed, the refusal admitted through the choke point, loop continues), `pkg/agent/processmessage_bound_test.go` (new; WS, SSE and a fake Slack channel intake), `pkg/agent/args_refusal_test.go` (new).
- **FRs:** FR-015, FR-016 (enforcement half).
- **BDD:** B-17, B-18, B-19, B-20.
- **Tests first:** `TestProcessMessage_UserMessageBound_AllIntakes` (38), `TestRunTurn_ArgsRefusal_TurnContinues` (35), the enforcement half of `TestToolArgsCap_StructuredRefusal` (12). DS-2 #1–#4, DS-3 #1–#2.
- **Gate:** `go-test`.
- **Depends-on:** T066-03, T066-04, T066-05.
- **Size:** M.
- **DoD:** SC-005 (first two clauses) — a 64,001-char message on WS, SSE and a channel leaves 0 transcript entries, 0 turns, 0 error frames; 64,001-char arguments yield a refusal followed by a further LLM call; changing the cap changes the quoted limit.

### T066-16 — D10: ingest bounds on the transport and the search providers — P1
- **Files:** `pkg/mcp/manager.go` (`sandboxedStdioConn` reader bounded at `ingest_bound_bytes` per JSON-RPC message — abort on the transport, never fully buffered; `http.MaxBytesReader` on the HTTP/SSE client `RoundTripper`; `Manager.CallTool` surfaces the failure naming the bound), `pkg/tools/web.go` (`BraveSearchProvider.Search`, `DuckDuckGoSearchProvider.Search`, `PerplexitySearchProvider.Search` — `io.ReadAll` replaced by a bounded reader; `GLMSearchProvider.Search`, `BaiduSearchProvider.Search` — the two 1 MiB `LimitReader`s raised to the bound; `fetch_url` fallback 10 MB → 8 MB), `pkg/mcp/ingest_bound_test.go` (new, fake server), `pkg/tools/web_ingest_test.go` (new).
- **FRs:** FR-038 (the `< 8,388,608` setting ceiling is validated in T066-17).
- **BDD:** B-46.
- **Tests first:** `TestIngestBound_MCPTransport` (23), `TestIngestBound_SearchProvidersAll5` (24). DS-7 #1–#5.
- **Gate:** `quick`.
- **Depends-on:** T066-03 (the `ingest_bound_bytes` setting).
- **Size:** M.
- **DoD:** 8,000,001 bytes from an MCP stdio server, an MCP HTTP server, or any of the five search providers is a tool failure naming the bound; 2 MB and 8,000,000 bytes are accepted; nothing is truncated at ingest.

### T066-17 — D9 backend: `/settings/context`, agent window fields, catalog projection, reload — P1
- **Files:** `pkg/gateway/rest_context_settings.go` (new: `GET/PUT /api/v1/settings/context` on the generated `ContextSettings` / `ContextSettingsUpdate` types; partial update, omitted = unchanged; 400 `ErrorResponse` naming field and limit on cap > 150,000 or < 1, `absolute_trigger_chars < 1`, `ingest_bound_bytes ≥ 8,388,608` or < 1, `model_overrides[].context_window < 1`; every 200 write → `loop.go::TriggerReload`; `withAuth`, **not** `RequireNotBypass` — the `/settings/memory` precedent in `pkg/gateway/rest_memory_settings.go`; prunes `model_overrides[]` entries whose provider no longer exists), `pkg/gateway/rest.go` (`listAgents` / `getAgent` / `updateAgent`: derived `context_window_effective`, `context_window_source`, `context_window_clamped`; `context_window_override` write → reload), the S67 providers-catalog `GET` handler (per-model `window_source`; `window_unknown: true` for a `locality: local` model whose live query failed — X-08; not a `Provider.status` value), `pkg/gateway/rest_context_settings_test.go` (new, `httptest`), `pkg/gateway/rest_agent_window_test.go` (new).
- **FRs:** FR-036, FR-037, FR-008 (projection + reload halves), FR-001 (dead-override prune), FR-010 (ceiling), FR-038 (setting ceiling).
- **BDD:** B-09 (projection half), B-10, B-14, B-44, B-45.
- **Tests first:** `TestGateway_ContextSettings_PartialUpdateCeilingReload` (41), `TestGateway_AgentWindowFieldsAndOverrideReload` (42), `TestIngestBound_SettingCeiling` (25), `TestRunTurn_LocalEndpointRefusedUntilOverrideReload` (37 — reload asserted; §17.6). DS-8 #1–#6.
- **Gate:** `go-test`.
- **Depends-on:** T066-01, T066-09, T066-10, **T067-CATALOG-GET**.
- **Size:** M.
- **DoD:** SC-007 and SC-013 — writing a `model_overrides` entry for a refused local model triggers a reload and the next turn runs with that window, no restart; the catalog `GET` shows `window_unknown: true` until then; `GET /settings/context` returns the spec defaults on a fresh install.

### T066-18 — Tier-1 bounding parameters (ADR §15 task 1) — P2
- **Files:** `pkg/tools/filesystem.go` (`list_directory` gains `offset` / `limit` in entries), `pkg/tools/inspect_session.go` (`offset` / `limit` in entries), `pkg/agent/recall_conversation.go` (`max_results` in turns on the `query` / `turn_range` modes), `pkg/tools/manifest.go` (schemas), the three tools' tests.
- **FRs:** FR-039, FR-040.
- **BDD:** B-47.
- **Tests first:** `TestTier1Tools_BoundingParams` (45). DS-9 #1–#3.
- **Gate:** `quick`.
- **Depends-on:** T066-14 (same file as the recall tool's new mode — avoids a conflicting edit).
- **Size:** S.
- **DoD:** `offset ≥ 0`, `limit` / `max_results ≥ 1` validated with a tool error otherwise, names consistent with `read_file`'s `offset` / `length`, documented in each tool schema.

---

## Delivered elsewhere (cross-spec, do not duplicate)

| Scope | Stream | Spec tests it carries | This list's hook |
|---|---|---|---|
| Settings → Models context section (caps, trigger, ingest bound, global default, model overrides, per-agent effective window + source + clamped badge) | **B5-SPA** (plan §2, ADR-066 D9 UI) | 46 `ContextSection.test.tsx`; 46b row/picker `window_unknown` state + default-model card window/source (after S68's components) | T066-17 is the backend it calls; `ResolveWindow(provider, model)` without an agent (T066-09) backs the default-model card. |
| Chat: recall mark rendered only under Verbose chat (`src/lib/toolVisibility.ts::shouldRenderToolCall`), `tool_result_projection` zod edge, `content_state` on the transcript | **B5-SPA** | 47 `toolVisibility.test.ts` + projection-frame zod test | T066-12 emits the frame and `content_state`. |
| `LLMError` attribution `user` rendered as a neutral notice, not an error toast | **B5-SPA** | 48 `llm-error.test.ts` | T066-11 emits the codes. |

---

## Dependency summary

**Parallel groups** (everything inside a group can run at the same time once its gate is met):

- **After A-CONTRACT, with no other prerequisite:** T066-01, T066-02, T066-03, T066-11. (T066-02 and T066-03 need no contract at all and can start immediately.)
- **After T066-01 + T066-03:** T066-04.
- **After T066-03:** T066-08, T066-16 (both independent of the agent loop).
- **After T066-05 (the choke point):** T066-06 and T066-07 together (the two P0 preconditions — first in line, per the spec), T066-12, T066-15. T066-09 can also run here; it only needs T066-03 plus **T067-RESOLVER**, so its real gate is the ADR-067 merge, not this list.
- **After T066-09:** T066-10.
- **After T066-11 + T066-12:** T066-13.
- **After T066-06 + T066-07 + T066-12:** T066-14, then T066-18.
- **After T066-09 + T066-10 + T066-01 + T067-CATALOG-GET:** T066-17.

**Serial spine (the critical path):** A-CONTRACT → T066-01 → T066-04 → T066-05 → T066-12 → T066-13 → T066-14 → T066-18. Everything else hangs off this spine; T066-02 and T066-03 must land before T066-05 but can be built in parallel with T066-01/T066-04. The three tasks that edit `loop.go::runTurn` (T066-05, T066-11, T066-13) plus T066-07 and T066-15 should be integrated **one at a time** by the integrator, each with its own Fly `go-test` run, because they touch the same CRITICAL symbol and will conflict textually.

**Riskiest single task: T066-13 (D6 mid-turn check).** The spec's impact table rates `loop.go::runTurn` / `::processMessage` **CRITICAL** (every turn, every channel, `subturn.go::spawnSubTurn`, the gateway frames and the ActivityPanel at d=2) and `windowTrim` + the `isOverContextBudget` threshold **HIGH** (pre-turn, timeout-recovery and model-switch sites; recall-span drop at d=2). T066-13 is the one task that sits on both at once, inside the tool loop that runs on every iteration, and it is the only task with a turn-fatal failure mode of its own (the guard → `context_unrecoverable`) and an "unbounded growth" failure mode if the check is skipped — which is the original incident. Its correctness also rests on two invariants owned by earlier tasks: the clamp in T066-05 (which is what makes the floor satisfiable, CRIT-002 / E3) and the emptied-set restore point in T066-12 (which is what makes an abort after emptying safe, FR-020). Run `impact({target: "runTurn", direction: "upstream"})` before editing and attach the report to the task's commit; integrate it alone with the Fly `go-test` gate, never bundled.

Runner-up: T066-05 (the choke point) — twelve producer sites routed through one function inside the same CRITICAL symbol, but its failure modes are mechanical (a missed producer is caught by the grep test 11).
