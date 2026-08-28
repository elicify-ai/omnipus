# Adversarial Review: Spec — ADR-066 Context overflow

**Spec reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/specs/adr-066-context-overflow-spec.md` (968 lines, Draft, 2026-08-22)
**Brief**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` (Proposed, restructured 2026-08-22; pass-2 findings resolved in §16a)
**Review date**: 2026-08-22
**Review mode**: plan-spec (BDD scenarios, FR-xxx, SC-xxx, traceability matrix all present)
**Branch verified against**: `feat/context-budget-and-tool-result-routing` (direct Read/Grep; GitNexus MCP tools were not exposed in this session either)
**Verdict**: **BLOCK**

> What this pass does not re-raise: the ADR-level findings the pass-2 review already closed (CRIT-001…004, MAJ-001…009, MAJ-016/017). Those are treated as settled brief. Everything below is about the **spec's own text** — places an implementer would have to guess, places the spec contradicts the ADR or itself, and places the spec's test plan would go green without proving the ADR's exit proof.

---

## Executive Summary

The spec is thorough in shape — every FR traces to a scenario and a test — but three of its load-bearing mechanisms are specified in ways that cannot both be implemented and satisfy their own success criteria. The mid-turn budget formula in §5 is not the ADR's trigger (`Wc = min(W, 0.9W)` is always `0.9W`, and the 0.9 is then stacked on top of `windowTrim`'s own 5 % headroom, so the pre-turn and mid-turn checks disagree — the exact "two formulas" defect §16a MAJ-001 was meant to close). The thrash guard is declared reachable "only by an injected fault" while the spec's own B-36 setup (three parallel results at the cap against a budget that admits two) reaches it through FR-028 + FR-030 with no fault at all — and on any small-window model a single capped result does the same. The restore-point requirement (FR-020) tells the implementer to both "refresh after every empty" and "restore to turn-start values", which are opposite instructions given how `restoreSession` actually works. Beneath those, the user-message bound is placed at the WebSocket handler although every channel (Telegram, Slack, Matrix, SSE, goal follow-ups) enters through `processMessage`, the "per-model override" the D3 message tells the operator to set has no contract type anywhere in the spec, and the SPA has no wire signal that a result was emptied, so B-26 cannot be built under Constraint #8.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 16 |
| MINOR | 12 |
| OBSERVATION | 5 |
| **Total** | **37** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The §5 D6 budget formula is not the ADR's trigger, is internally degenerate, and re-creates the two-formula defect

- **Lens**: Incorrectness / Inconsistency (spec vs ADR)
- **Affected section**: §5 Machine-Verifiable Constraints "D6 budget formula (A-5)"; FR-027; B-34; test 18/19
- **Description**: The spec writes `Wc = min(W, 0.9 × W)`. For every positive `W` that is `0.9W` — the `min` does nothing. It then defines `budget = Wc − maxTokens − ceil(0.05 × W) − pinnedCoreOverhead`, i.e. the 10 % ceiling **stacked on** `windowTrim`'s existing 5 % headroom. The ADR (§7) says the trigger is `min(absoluteBudget, 0.9 × resolvedWindow)` — a ceiling on the *absolute character budget*, not a second haircut on the token budget — and §16a MAJ-001 says D6 "does **not** add a second formula: the mid-turn check calls the same `isOverContextBudget` with the same budget". Verified on the branch: `pkg/agent/context_budget.go::isOverContextBudget` tests `msgTokens + toolTokens + maxTokens > contextWindow` (no headroom, no pinned overhead), while `pkg/agent/loop.go::windowTrim` tests against `contextWindow − maxTokens − ceil(5 %) − pinnedCoreOverhead`. Those are already two formulas; the spec adds a third (`0.9W − maxTokens − 5 % − pinned`) for the mid-turn site and says nothing about which one the pre-turn `isOverContextBudget` site uses.
- **Impact**: The pre-turn trim leaves the window at a level (≤ `W − maxTokens − 5 % − pinned`) that the mid-turn check immediately considers over budget (its ceiling is ~`0.85W − maxTokens − pinned`), so emptying fires on the first tool result of any turn that starts near full, and the 80 % target drives it well below where the pre-turn site was content. Test 19 (`TestMidTurnBudget_SameBudgetAsWindowTrim`) as named would fail against the spec's own formula; an implementer will "fix" it by picking one — silently.
- **Recommendation**: Replace the block with one definition used by all four consumers: `budget(W) = W − maxTokens − ceil(0.05 × W) − pinnedCoreOverhead` (windowTrim's, unchanged). Define the D6 trigger exactly as the ADR: `triggerChars = min(absolute_trigger_chars, 0.9 × W × 2.5)` applied to the **tool-result share in chars**, and "over budget" as `isOverContextBudget(budget(W), …) OR toolResultShareChars > triggerChars`. State explicitly that `isOverContextBudget`'s threshold is changed to `budget(W)` so pre-turn, mid-turn, timeout-recovery and model-switch all compare against one number (that is what B-06 and SC-004 are actually asserting). Delete `Wc`.

---

#### [CRIT-002] The thrash guard is reachable without any fault — the spec's own B-36 proves it, and small-window models hit it on every large result

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: US-8 E3; B-36; DS-5 row 3; FR-028, FR-030; SC-005 ("the thrash guard is reached only under an injected fault"); test 34 ("unreachable without fault")
- **Description**: B-36 sets up "an assistant message with 3 parallel calls at the cap and a budget that admits two" and asserts "none of the three is emptied". FR-028 forbids emptying any of them (floor = whole last step). FR-030 then says: still over budget after every eligible result is emptied → typed error. That is the thrash guard, fired by a perfectly legal provider response with no injected fault. E3 even says so ("if still over budget → thrash guard") and then claims it is "reachable only by injected fault". Worse, the caps are window-independent (ADR §5 "Identical at 131,072 and 1,048,576"): on the 8,192-token Ollama model the spec itself uses in B-08, `budget ≈ 8,192 − maxTokens − 410 − pinned`, i.e. a few thousand tokens, while one builtin result at the 64,000-char cap is 25,600 estimator tokens. **Every** tool call returning more than ~5,000 chars on that model lands in the floor, cannot be emptied, and kills the turn with `context_unrecoverable`. Pass-2 MAJ-011 ("a window-independent cap on small-window models") was carried forward unresolved; the spec inherits it and then asserts the opposite in SC-005.
- **Impact**: On the operator's own local models the incident reproduces as a typed death instead of a silent one. SC-005 and test 34 will be written to pass against a fault injector and never exercise the real path.
- **Recommendation**: Decide one of: (a) the effective per-result cap is `min(configuredCap, floor(0.25 × budget(W) × 2.5))` so a floor set of ≤ 4 parallel results always fits — state the 0.25 and its rationale; or (b) when the floor set alone exceeds the budget, the floor shrinks to the **last** result only (ADR §7's original wording) and earlier results of the same step are emptied oldest-first, with a WARN; or (c) keep the typed death and delete "only by injected fault" from E3, SC-005, test 34 and US-9.AC5. Whichever is chosen, add a DS-5 row for "single result larger than budget on an 8,192 window" and a scenario that pins the outcome.

---

#### [CRIT-003] FR-020's "refresh the restore point after every empty" and "rollback restores to turn-start values" are opposite instructions, and the spec does not name which restore point it means

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: US-6.AC5, AC8; B-24, B-27; E13; FR-020; §3 Symbols table (`restoreSession`, `refreshRestorePointFromSession`, `initialArchiveLen/initialHistoryLength`)
- **Description**: Verified in `pkg/agent/turn.go`: `restoreSession` rolls back using `ts.initialArchiveLen` and `ts.initialHistoryLength`, which are captured once at turn start and **never refreshed**. `refreshRestorePointFromSession` writes a separate field, `ts.restorePointHistory`, which (grep) is written at `turn.go:1473` and read **nowhere** — it has no effect on rollback today. So "the restore point is refreshed after every trim" (US-6.AC8, FR-020) describes dead state, and the spec extends the dead state with the emptied-set. Meanwhile B-27 says an abort after two triggers "restores to the turn-start values, not to an intermediate state" and E13 says "rollback to the last refreshed restore point" — these are contradictory if refresh means anything, and vacuous if it means nothing.
- **Impact**: An implementer who wires the emptied-set into `refreshRestorePointFromSession` ships a no-op and test 33 passes by accident (the set is restored to turn-start because the turn's tool-result lines are truncated away anyway). An implementer who wires it into `initialArchiveLen`-style counters and refreshes them per empty breaks B-24 (rollback would stop at the last refresh). Neither knows which the spec wants.
- **Recommendation**: Specify the restore point as the three values `restoreSession` actually uses — `initialArchiveLen`, `initialHistoryLength`, and a new `initialEmptiedSet` — captured once at turn start and never moved. Delete "refreshed after every mid-turn empty" from US-6.AC8/FR-020/B-27/E13, or, if the intent is that a mid-turn **Skip advance** of earlier turns must survive an abort (today it does not — `restoreSession`'s comment says mid-turn evictions are undone), say so as a separate requirement with its own scenario. Note that the emptied-set entries an abort must remove are exactly the ids of the current turn (D5 only empties current-turn results, FR-016), so "restore the set" reduces to "drop ids whose archive lines were truncated" — state that, because it is simpler and testable.

---

#### [CRIT-004] The "per-model override" the D3 message instructs the operator to set does not exist as a contract type, and it is not the same thing as the per-agent override the ladder defines

- **Lens**: Inconsistency (spec vs ADR vs contracts) / Constraint #8
- **Affected section**: US-2.AC3/AC4; B-09, B-10; E10; FR-008; DS-4 rows 9; §5 "Agent view exposes … `context_window_override`"; FR-036
- **Description**: D2 rung 1 is "per-agent override". The D3 message (copied verbatim from the ADR) says *"Set it under Settings → Models → <provider> → <model> → Context length (per-model override, D2 rung 1)"* — a per-**model** setting under a **provider**, which is a different key space (one model, many agents). FR-036 contracts only per-agent fields on `Agent.yaml`/`AgentUpdateRequest.yaml` plus a global default in `ContextSettings.yaml`. No schema, route or config key for a per-(provider, model) override appears anywhere in the spec, yet B-10/DS-4 row 9/SC-007 are written as if it exists ("operator sets the per-model override to 32,768 → source `operator`"). Verified: `Agent.yaml` has no `context_window*` field today; `pkg/config/config.go` has only `agents.defaults.context_window`.
- **Impact**: Constraint #8 violation by omission: the first implementer to reach B-10 will invent either a per-agent field (and the UI message lies about where it lives) or an uncontracted per-model map in `config.json`. The ladder's rung order (per-agent → global default) would also need a fourth rung ("per-model") whose position is undefined — above or below the per-agent override?
- **Recommendation**: Pick one. Either (a) the D3 state is cleared by the **per-agent** override — change the message to *"Set a context length for this agent under Agents → <agent> → Context window"*, delete "per-model" everywhere, and keep FR-036 as is; or (b) add a per-(provider, model) override as a real rung — define its position in FR-001, its schema (`contracts/components/schemas/ModelContextOverride.yaml` or a field on the ADR-067 provider-model entry), its route, its clamp, and add it to DS-4 and the traceability matrix. The ADR has the same wording defect; flag it back rather than inheriting it.

---

### MAJOR Findings

#### [MAJ-001] The user-message bound is placed at the WebSocket handler, but user messages enter through `processMessage` from every channel

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: US-4; FR-014 ("at the gateway before a turn is registered"); §3 Symbols (`gateway/websocket.go::handleChatMessage`); test 38
- **Description**: Verified: every in-process channel builds `bus.InboundMessage` via `pkg/channels/base.go`, the webchat SSE path builds one in `pkg/gateway/sse.go:189`, goal follow-ups and the async notifier build them in `pkg/agent/goal_loop.go`, `goal_triggers.go`, `async_notifier.go`, and all are consumed by `pkg/agent/loop.go::processMessage` → `runTurn`. Slack (40,000 chars), Matrix, Google Chat, webhook-fed channels and a pasted message over SSE all bypass `handleChatMessage`. The spec's stated purpose — "so the thrash guard is never reachable through a user message" — is false for every intake except the SPA.
- **Impact**: A 200,000-char Slack paste reaches `runTurn` with nothing to empty and the thrash guard (or, per CRIT-002, a typed death) fires — the exact path D4 was extended to close.
- **Recommendation**: Move FR-014 to the single point where an inbound message becomes a turn (`processMessage`, before turn registration and before the user message is persisted), and define the refusal as a normal outbound reply on the originating channel. Keep the SPA-side check as a UX courtesy only, or drop it. Rename test 38 accordingly and add one channel-path scenario (Slack or SSE).

---

#### [MAJ-002] FR-009's "exactly one function / no other site constructs a `role: "tool"` message" is contradicted by three existing producers the spec does not mention

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-009; §3 Symbols ("One success-path `toolResultMsg` site plus seven `deniedMsg` sites")
- **Description**: Grep on the branch: `Role: "tool"` is built at **nine** sites in `pkg/agent/loop.go` (not eight), plus `pkg/agent/attach_hydrate.go:162` (attachment hydration), `pkg/agent/repair.go:265` (orphan repair), and `pkg/agent/recall_conversation.go::buildRecallSpanMessages` (re-injected span messages). The spec routes the recall page through the choke point (FR-024) but says nothing about hydrated attachments (which can be large — a hydrated file is a tool result by construction) or repair placeholders, and its site count is wrong.
- **Impact**: Either FR-009 is violated on day one and the lint/test the implementer writes for it ("no other site") fails on `attach_hydrate.go`, or attachments become a cap bypass.
- **Recommendation**: Enumerate all twelve producers in §3, state for each whether it goes through the choke point (attachments: yes, builtin-success surface; repair placeholders: exempt, bounded by construction; recall spans: yes), and add a test that asserts the producer list by grep the way `TestDecommission_NoForceCompressionSymbols` does.

---

#### [MAJ-003] "Advance Skip (cut)" mid-turn contradicts "mid-turn the system MUST NOT cut", and the in-memory effect of a mid-turn Skip advance is unspecified

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: FR-028 vs FR-029; B-35 row 1; DS-5 row 1; US-8.AC3
- **Description**: FR-028 row 1: mid-turn, oldest over-budget content is an earlier complete turn → "advance Skip (unchanged)". FR-029: "Mid-turn the system MUST NOT cut". Advancing Skip is a cut (at a user boundary — legal for providers, but a cut). The ADR has the same pair of sentences. More importantly, the ADR (§6.1) establishes that mid-turn requests are built from the in-memory `messages` slice and that `assembleMessages` cannot be re-run mid-turn. A mid-turn Skip advance must therefore also drop the corresponding prefix of `messages` — the spec never says so, and nothing in DS-5 row 1 checks the request bytes.
- **Impact**: An implementer who only advances `Skip` in meta sends the evicted turns anyway (the slice still holds them) — the check "succeeds" on disk and the provider request is unchanged. The next check fires again, advances Skip again, and so on: a thrash loop that FR-030 does not catch because the in-memory total never moves.
- **Recommendation**: Reword FR-029 to "MUST NOT cut **inside the current turn**". Add to FR-028 row 1: "and the same prefix is removed from the in-memory `messages` slice before `callMessages` is built; the restore point is not moved." Add a DS-5 row where the request bytes after a mid-turn Skip advance are asserted to exclude the evicted turn.

---

#### [MAJ-004] The D6 stop condition conflates "over trigger" with "over target", so the thrash guard fires when emptying reaches the trigger but not the target

- **Lens**: Ambiguity
- **Affected section**: §5 "target = 80 % of the binding trigger … emptying proceeds … until"; FR-027; FR-030 ("if still over budget after every eligible result is emptied")
- **Description**: Emptying runs "until `toolResultShare ≤ 0.8 × absoluteShare` **and** total ≤ `0.8 × budget`". Non-tool content (system prompt, user message, assistant reasoning) can hold the total above `0.8 × budget` even after every tool result is a mark. At that point the turn is under the trigger but over the target. FR-030 keys the typed death on "still over budget" — which of the two lines?
- **Impact**: With a 20 %-of-budget system prompt the thrash guard fires on every turn that trips the trigger once.
- **Recommendation**: State: emptying stops when the target is reached **or** no eligible result remains; the thrash guard fires only if, after that, the total still exceeds the **trigger** (not the target). Add a DS-5 row for "target unreachable, trigger satisfied → continue, no error".

---

#### [MAJ-005] No wire signal exists for "result X was emptied/capped", so the SPA cannot implement B-26 or show a capped form on reload — Constraint #8 gap

- **Lens**: Incompleteness / Contract-first
- **Affected section**: US-6.AC7; B-26; FR-022; §5 Integration Boundaries row "SPA ↔ gateway … recall-mark frame under Verbose chat"; FR-036
- **Description**: The live tool-result frame is streamed to the SPA when the result is produced — **before** D6/D5 run. Emptying then changes only the in-memory slice and meta. The SPA has no way to learn that R1 became a mark unless a new WS frame (or a field on the transcript REST read) carries it. The Integration Boundaries table names a "recall-mark frame" but no asyncapi schema, no `contracts/` file, no FR and no generated type is specified for it; the ADR §12 only covers the mark *inside* a tool-result message. Likewise, on reload the SPA reads the transcript — from the archive (full content) or the projected view? Unstated.
- **Impact**: Test 43 (`toolVisibility.test.ts`) will be written against a hand-rolled frame shape or against nothing; either the mark never reaches the thread, or a hand-written wire type lands in `src/lib/ws.ts` and trips the Constraint #8 lint.
- **Recommendation**: Add an FR: "Emptying emits a `tool_result_projection` WS frame `{tool_call_id, state: capped|emptied, mark}` defined in `contracts/asyncapi.yaml`; the transcript REST read returns the projected content plus `projection_state` per tool message." Add the schema file to the 5-step list and to the traceability matrix. Decide and state whether the SPA's `tool_results/` store (the 50 KiB `InlineToolResultMaxBytes` offload) keeps the full content — it does today, and that is where Verbose chat would read it from.

---

#### [MAJ-006] The "turn number" in the mark is defined as a turn index plus a line offset — two different units

- **Lens**: Incorrectness
- **Affected section**: FR-017; A-12 ("Index into `parseTurnBoundaries` of the current window + archive offset (stable across evictions)")
- **Description**: `parseTurnBoundaries` returns user-role **indices** into the window; `Skip` (the archive offset) is a count of **lines**. Adding them yields a number that is neither a turn ordinal nor a line number and is not stable across evictions (a trim that evicts one turn of 12 lines changes the sum by 12, not by 1).
- **Impact**: The mark states "turn 47" for the sixth turn; the model and the operator reading Verbose chat are misled; B-21's "turn" assertion has no ground truth.
- **Recommendation**: Define the turn number as "1 + the number of `role: user` lines in the archive preceding this result's line" (computable from the archive at append time, stable forever). If that costs a scan, record it on the `ArchivedMessage` when the user line is appended.

---

#### [MAJ-007] `tool_call_id` is not a unique key in the archive; recall-by-id and the emptied-set both assume it is

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: US-7.AC1 ("the archive line whose `ToolCallID` equals"); FR-018 (emptied-set keyed by id); FR-023; B-28
- **Description**: Ids are provider-generated. Local/OpenAI-compatible servers commonly emit `call_0`, `call_1` per response; `pkg/providers/antigravity_provider.go` synthesises `call_<name>_<nanos>`. Across a 90-day session archive, collisions are routine. The spec says "matches the archive line whose `ToolCallID` equals the argument" — singular — and keys the persisted emptied-set by the same id, so emptying `call_0` in turn 40 would, on reload, project **every** `call_0` in the window as a mark.
- **Impact**: Wrong result recalled; wrong results blanked on reload; B-22 (live = reload) fails in a way the test fixture (unique ids) never exercises.
- **Recommendation**: Key both the emptied-set and recall by `(archive line index)` or `(turn number, tool_call_id)`, and have the mark carry that composite. State the rule for duplicates ("most recent line wins" is acceptable if stated). Add a DS-6 row with two lines sharing an id.

---

#### [MAJ-008] The ingest ceiling does not guarantee a readable archive line; JSON escaping can inflate 8,000,000 bytes well past 10 MB

- **Lens**: Incorrectness
- **Affected section**: US-12.AC4 ("so every archived line produced from an admitted result remains readable"); FR-037; §5 "< `maxLineSize` × 0.8"; A-8
- **Description**: An admitted result of 8,000,000 bytes consisting of newlines, quotes, tabs or control characters serialises as `\n`, `\"`, `\t`, `\u00XX` — 2× to 6× inflation. 8,000,000 × 2 = 16,000,000 > `maxLineSize` (10,485,760). `pkg/memory/jsonl.go:158` reads with `bufio.Scanner` at that buffer size; a longer line is a scanner error that makes **the rest of the session unreadable**, not just that line. The 0.8 factor is asserted, not derived.
- **Impact**: A single adversarial or merely unlucky tool result (a log dump with CRLFs) permanently breaks `GetHistory` for that session — data loss by the ADR's own definition, and the incident's archive would not be recallable.
- **Recommendation**: Bound the **marshalled line**, not the raw bytes: after filtering and before append, if `len(json.Marshal(ArchivedMessage)) > maxLineSize − margin`, fail the tool call with the D10 error (the content is already held, so this is a post-parse check — say so). Keep the 8 MB raw bound as the transport pre-check. Replace US-12.AC4's claim with that rule and add a DS-7 row of 8,000,000 newline bytes.

---

#### [MAJ-009] For MCP, the "ingest bound" as specified is a post-parse size check, which is exactly what D10 says cannot protect the process

- **Lens**: Infeasibility / Honesty
- **Affected section**: US-12.AC1; FR-037; §3 Symbols (`mcp/manager.go::CallTool`); test 23
- **Description**: The ADR (§11) is explicit that by the time a result is measured "it has been received, held and parsed", and that the Go SDK offers no response cap. The spec nonetheless lists MCP first under "bounded at ingest" and tests it as `bound ± 1` on the serialised content. Measuring `len(content)` after `CallTool` returns is not an ingest bound; a 500 MB MCP response is still fully buffered by the SDK before the check runs.
- **Impact**: The process-protection property is claimed and tested but not delivered for the one surface that caused the incident. An operator reading US-12 believes a hostile MCP server cannot OOM the gateway.
- **Recommendation**: Either specify a transport-level limiter (wrap the stdio/HTTP transport's reader in an `io.LimitReader`-style bound that aborts the JSON-RPC read at 8 MB — name the SDK hook or the custom transport), or relabel the MCP row honestly as "post-parse size gate (process not protected)" and move the process-protection claim to the HTTP surfaces only. Either way, the two existing `LimitReader(…, 1<<20)` sites at `web.go:758/850` must be stated as raised to the bound or deliberately left at 1 MiB — "every read bounded at 8 MB by default" is currently false for them.

---

#### [MAJ-010] Provider classification ("cloud of a known vendor" / "local or self-hosted" / unknown) is undefined, and LM Studio has no provider id

- **Lens**: Ambiguity
- **Affected section**: US-2; FR-006 ("known vendor"), FR-007 (`ollama`, `vllm`, LM Studio, `custom`); DS-4 "Provider class"
- **Description**: Verified in `pkg/providers/factory_provider.go`: ids `ollama`, `vllm`, `custom`, `claude-cli`, `codex-cli` exist; no `lmstudio`. "Known vendor" is never defined — is it "present in the ADR-067 catalog as a provider", "has a live-limits endpoint", or the factory's `case` list? A `custom` provider pointed at a hosted OpenAI-compatible API (Together, Fireworks, a corporate proxy) is by FR-007 "self-hosted" → mandatory live query → `/v1/models` reports no window → **unusable**, although it is a cloud model the 128,000 floor was designed for.
- **Impact**: Every `custom`-wired cloud endpoint becomes unusable on upgrade; the operator sees the D3 message for a model that is not local.
- **Recommendation**: Define the class by provider id in a table in §5: `ollama`, `vllm` → local (mandatory live); `custom` → **ask**: live query first, then floor with WARN? or refuse? — decide and state; everything else in the factory list → cloud; LM Studio → state that it is reached via `custom` (OpenAI-compatible) and inherits that rule. Add a DS-4 row for `custom` + cloud URL.

---

#### [MAJ-011] "Usable immediately without restart" has no mechanism: the window is resolved when the instance is built

- **Lens**: Incompleteness
- **Affected section**: US-2.AC4; B-10; E10; FR-008; §5 Behavioral Contract ("When an agent instance is built, the system resolves…"); test 37
- **Description**: `NewAgentInstance` sets `ContextWindow` once. The spec's Behavioral Contract binds resolution to instance build, then requires that setting an override takes effect on the next turn with no restart. The default-agent precedent in CLAUDE.md shows this exact trap (`PUT … default:true` returned 200 and changed nothing until `TriggerReload` was wired). The spec does not say whether the override write triggers a registry reload, whether `runTurn` re-resolves per turn, or whether the instance's field is mutated under its mutex.
- **Impact**: The ADR-037 anti-pattern: the Settings write succeeds, the agent view shows the new number, the next turn uses the old one.
- **Recommendation**: State it: "Writing `context_window_override` or the global default, or clearing the D3 state, calls `TriggerReload` (same as the default-agent singleton); `runTurn` reads `ContextWindow` from the instance under `RLock`." Extend test 37 to assert the reload was triggered (or that the instance field changed) rather than only observing the next turn.

---

#### [MAJ-012] Recall pages of exactly 64,000 chars plus framing exceed the 64,000 cap; B-30 "passes the choke point unmodified" is off by the header

- **Lens**: Incorrectness (boundary)
- **Affected section**: US-7.AC1; B-28, B-30; FR-023; DS-6 row 1 ("chars 0–63,999, total stated")
- **Description**: The page "states the total size and the offset reached" inside the same `role: "tool"` message, and must "pass the D4 choke point". A 64,000-char payload plus even a 40-char header is 64,040 chars → capped head-and-tail with a mark → the page the model receives is no longer contiguous with the next page, and B-29's concatenation test fails at every page boundary.
- **Impact**: Paging is subtly lossy; the model re-reads overlapping or missing spans.
- **Recommendation**: Define page payload size = `builtinSuccessCap − len(header)` (or put total/offset in structured fields the choke point does not count), and pin it: DS-6 row 1 becomes "payload 0–63,9xx, message ≤ 64,000 after framing, unmodified by the choke point".

---

#### [MAJ-013] `turn_canceled` covers the user's own Stop button; the spec does not say whether a deliberate cancel is rendered as an error or which attribution the two new codes carry

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: US-9.AC1/AC2; FR-033 ("with copy and attribution" — values unspecified); B-40
- **Description**: The four returns (`loop.go` 9252/9255/9471/9474) fire on `ctx.Err()`, which is also what a user-initiated Stop and a gateway shutdown produce. The spec assigns `context_unrecoverable` attribution `product` but gives no attribution for `turn_canceled`/`turn_timed_out`. The `LLMError.yaml` copy rules (`product`/`config` must not say "switch models", no "contact support") need the attribution to be known before the copy can be written. Whether a user who pressed Stop should now see an `LLMError` frame at all is not decided — today the gateway has a `chatTurnCanceledNoMatch` path that treats cancel as a non-error.
- **Impact**: Either a Stop now toasts an error to the user who asked for it, or the implementer adds an undocumented "user-cancel is not an error" branch that contradicts "no silent exits".
- **Recommendation**: Split the cause: `turn_canceled` (attribution `product`? `ambiguous`?) for non-user cancellation with the raw cause logged; user-initiated Stop keeps its existing non-error `DoneFrame` path **but** still writes the log line, event and transcript entry (which is what §1.4 actually needs). State both attributions in FR-033 and add a B-40 row for "user Stop".

---

#### [MAJ-014] Two homes for the global default window: `agents.defaults.context_window` (config + env var) and the new `ContextSettings` field

- **Lens**: Inconsistency / Greenfield
- **Affected section**: US-11.AC1 ("global default window … unset"); FR-036; FR-001 rung 2; DS-4 row 4
- **Description**: Verified: `pkg/config/config.go:1550` already has `ContextWindow int \`json:"context_window"\` env:"OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW"` under agent defaults. The spec introduces "the global default window" in `ContextSettings.yaml` on `/settings/context` without saying whether it is the same field (and the route writes `agents.defaults.context_window`) or a second one (and which wins, and whether the env var still applies). The greenfield rule forbids aliasing; the spec does neither aliasing nor removal — it is silent.
- **Impact**: Two values, two writers, and a ladder rung whose input is undefined.
- **Recommendation**: State: the `ContextSettings.default_context_window` field **is** `agents.defaults.context_window` (single config key, single env var), surfaced through the new route; or delete the old key and env var outright (greenfield) and say so in SC-009's grep.

---

#### [MAJ-015] The ADR's implementation task 1 (bounding parameters for `list_directory`, `inspect_session`, `recall_conversation` search modes) is dropped without a decision

- **Lens**: Inconsistency (spec vs ADR) / Traceability
- **Affected section**: Spec §1 Scope; ADR §15 task 1 and §15.1
- **Description**: The ADR lists three Tier-1 gaps as implementation tasks ("hygiene rather than blockers"). The spec's scope line enumerates D2–D10 and §16a and never mentions §15 task 1 — it is neither in scope, nor explicitly deferred with a reason, nor given an issue.
- **Impact**: A reviewer comparing the ADR's task list to the delivered branch finds three items nobody owns.
- **Recommendation**: Add one line to §1: "ADR §15 task 1 is deferred to issue #NNN (not on the exit-proof path)" or include it with an FR and a scenario.

---

#### [MAJ-016] Which cap applies to an MCP *failure* result, a policy-*denied* result, or a delegated sub-turn's report is unspecified

- **Lens**: Ambiguity
- **Affected section**: US-3 surfaces ("builtins and MCP"; success/failure); FR-009 ("builtin success, builtin failure, denied, MCP"); FR-010; B-11
- **Description**: Four producer classes are named in FR-009 but only three caps exist (MCP 62,500 / builtin-success 64,000 / builtin-failure 10,000). An MCP result with `isError: true`: 62,500 or 10,000? A denied result (the seven `deniedMsg` sites): which surface? A `delegate` result is the child's whole final answer routed back as a builtin tool result — capped at 64,000 with a mark, which silently truncates long delegated reports; intended or not, it is unstated. "Failure" itself is undefined (tool `IsError`? non-zero exit? both?).
- **Impact**: Three implementers, three answers; B-11 does not cover any of the four cases.
- **Recommendation**: Add a surface table to §5: `{builtin, mcp} × {success, failure, denied}` → cap, with "failure" defined as `ToolResult.IsError == true` (MCP: `isError`), and a one-line decision on `delegate` results (exempt, or capped like any builtin).

---

### MINOR Findings

#### [MIN-001] SC-009's grep forbids `= 128000` in `pkg/agent`, but FR-006 requires a 128,000 floor constant to live there
- **Lens**: Inconsistency
- **Affected**: SC-009; FR-006; test 6
- **Description**: The floor will be written as `const cloudWindowFloor = 128000` — which SC-009's pattern matches. The gate is either unpassable or gamed with `128_000`.
- **Recommendation**: Change SC-009 to forbid the two *fallback* sites by symbol (`contextWindow = 128000` in `windowTrim`, `newContextWindow = 128000`) and require exactly one named constant.

#### [MIN-002] B-16 / DS-1 row 11 put the secret at 63,990–64,030, which is not near a 50/50 head-and-tail cut of a 100,000-char result
- **Lens**: Incorrectness (boundary)
- **Description**: With the mark counted and a 50/50 split, cuts fall near ~31,900 and ~68,100; position 64,000 is inside the removed middle, so the scenario does not test a secret straddling a cut.
- **Recommendation**: Place the secret across the actual head cut (compute from the mark length) and add a second row across the tail cut.

#### [MIN-003] `length > page size` is "clamped" in E8/DS-6 row 5 but the tool-interface constraint says `1 ≤ length ≤ page size` (implying an error)
- **Lens**: Inconsistency
- **Recommendation**: Pick "clamped" and rewrite the constraint as `length ≥ 1; values above the page size are clamped`.

#### [MIN-004] "Exempt from window resolution" (FR-005) vs E14 "D6 … do not apply" — does `runTurn` skip the mid-turn check entirely for `claude-cli`/`codex-cli`, and what window does `windowTrim` use for them?
- **Lens**: Ambiguity
- **Recommendation**: State: for exempt providers `ContextWindow = 0`, both checks are skipped, and the pre-turn trim is skipped too (today's `128000` fallback would otherwise fire).

#### [MIN-005] The live-limits cache key and the credential used for the query are unspecified
- **Lens**: Ambiguity / Insecurity
- **Description**: Anthropic `/v1/models` needs the API key; two Ollama hosts share a model name; a `custom` base URL is operator-controlled. Key `(provider id, model)` collides across base URLs.
- **Recommendation**: Key the cache by `(provider id, base URL, model)`; state that the query uses the provider's stored credential via the credential store and is skipped (falls to the next rung) when no credential is configured.

#### [MIN-006] Boot-time behaviour when the live cache is cold is unspecified (synchronous fetch per agent? timeout?)
- **Lens**: Inoperability
- **Recommendation**: State: on a cache miss at instance build, resolve from the next rung immediately and refresh the cache in the background; the live value takes effect on the next instance build/reload. (This also keeps "never on the turn path" true at first boot.)

#### [MIN-007] Emptied-set growth and pruning are unspecified
- **Lens**: Incompleteness
- **Description**: Ids stay in meta forever, including ids of turns long evicted past `Skip`.
- **Recommendation**: Prune entries whose line index < `Skip` on every `TruncateHistory`; state the rule.

#### [MIN-008] `ephemeralSessionStore` (delegated sub-turns) has no meta file; E15's "own emptied-set" has no home
- **Lens**: Incompleteness
- **Recommendation**: State that the ephemeral store keeps the projection state in memory and that `RollbackAppended`'s new parameter is a no-op there.

#### [MIN-009] Per-empty observability: no log line for an emptying pass (ids, count, share before/after)
- **Lens**: Inoperability / Repudiation
- **Recommendation**: Add one INFO line per trigger with session key, count emptied, share before/after, and a counter `context_empties_total`, next to the existing §5 logging rules. Also name where `tool_result_large_total` lives (`pkg/gateway/metrics.go::toolMetrics` is the only counter registry on the branch).

#### [MIN-010] "Settings changes take effect live" is unstated for caps, trigger and ingest bound
- **Lens**: Ambiguity
- **Recommendation**: State that the choke point and the D6 check read the settings per call (no restart), and whether an in-flight turn sees a mid-turn change.

#### [MIN-011] `ErrorResponse` is cited in §5 but `PUT /settings/context` partial-update semantics (omit a field = keep? = zero → 400?) are not
- **Lens**: Ambiguity
- **Recommendation**: Follow the `PerformanceSettingsUpdate.yaml` pattern (separate update schema, all fields optional, omitted = unchanged) and say so in FR-036.

#### [MIN-012] SC-005's last clause is unparseable ("produces a typed error with exactly one further LLM call count of 0")
- **Lens**: Ambiguity
- **Recommendation**: "…produces a typed error and the provider is not called again (call count after the guard = 0)".

---

### Observations

#### [OBS-001] "No new storage is introduced" (§1) vs the new `$OMNIPUS_HOME/cache/model_limits.json` (§9)
- **Lens**: Inconsistency. Harmless but say "one new cache file" in §1.

#### [OBS-002] `ReadArchive` for recall-by-id loads the whole archive into memory; with 8 MB lines × 50 results per turn × 90 days that is gigabytes per recall call
- **Lens**: Inoperability. Consider specifying a streaming scan that stops at the first matching line, and note that `GetHistory` (read on every turn) now carries full results too — the per-turn disk read grows with the archive, not the window.

#### [OBS-003] Greenfield: the spec does not state how pre-existing `config.json` files containing `summarize_token_percent` are treated
- **Lens**: Greenfield rule. `LoadConfig` has no `DisallowUnknownFields` (per `config_test.go`), so the key is silently ignored — that is "simply does not work" in the permitted sense, but state it in §5 so nobody adds a boot notice (forbidden) or a rejection (not required).

#### [OBS-004] The ADR says the recall page is "the same interface `read_file` uses"; `read_file`'s `offset`/`length` are **bytes**, the spec's are **chars (runes)**
- **Lens**: Inconsistency with the brief. Keep runes (consistent with the caps) and drop the "same interface" comparison, or say "same parameter names, rune-denominated".

#### [OBS-005] Delegated sub-turn results are themselves tool results of the parent and will be capped at 64,000 with a mark
- **Lens**: Incompleteness. See MAJ-016; worth a holdout scenario ("ask for a long delegated report") so the product decision is visible.

---

## Structural Integrity (plan-spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1…US-12 (US-10 withdrawn) |
| Every acceptance scenario has BDD scenarios | PASS | US-11.AC5 is covered only implicitly by B-45's "field set is generated from the contract" |
| Every BDD scenario has `Traces to:` reference | PASS | |
| Every BDD scenario has a test in TDD plan | **FAIL** | B-23 (archive untouched) maps to test 31 by the matrix but test 31's description does not assert archive bytes; B-26 relies on a frame that has no contract (MAJ-005); B-36's asserted outcome contradicts FR-030 (CRIT-002) |
| Every FR appears in traceability matrix | PASS | FR-035 withdrawn and marked |
| Every BDD scenario in traceability matrix | PASS | B-42/B-43 withdrawn and marked |
| Test datasets cover boundaries/edges/errors | **FAIL** | No row for a single result larger than the budget (small window); no row for duplicate `tool_call_id`; no row for escaped-JSON inflation; DS-1 row 11 at the wrong boundary (MIN-002); no row for `custom` + cloud URL; no channel-path user message |
| Regression impact addressed | PASS | Table present; note `restorePointHistory` is dead state today, so "restore point" regression rows test nothing until CRIT-003 is resolved |
| Success criteria are measurable | **FAIL** | SC-005 unparseable (MIN-012) and asserts an unreachability that is false (CRIT-002); SC-009 unpassable as written (MIN-001) |

---

## Test Coverage Assessment

- **Missing test levels**: B-22 (live = reload byte equality) is the CRIT-002 exit proof from pass 2 and is only an integration test with a fake provider; it should also run once through the embedded binary (holdout) because the real provider adapters serialise `providers.Message` differently.
- **Missing negative tests**: no test that a **channel-delivered** oversized user message is refused (MAJ-001); no test that a mid-turn Skip advance changes the request bytes (MAJ-003); no test for the "target unreachable, trigger satisfied" case (MAJ-004); no test that an MCP failure/denied result picks a defined cap (MAJ-016).
- **Missing boundary tests**: budget smaller than one capped result (CRIT-002); page + header at exactly the cap (MAJ-012); secret across the real cut positions (MIN-002); escaped-JSON line at `maxLineSize` (MAJ-008); duplicate ids (MAJ-007).
- **Missing concurrency tests**: none specified for a settings PUT racing an in-flight turn's choke-point read (MIN-010), or for two delegated children emptying concurrently against separate ephemeral stores (MIN-008). Low risk but unaddressed.
- **Idempotency**: B-25 (one pass to target) is the only re-fire test; add "second check immediately after a pass does not re-trigger" explicitly.
- **Regression blind spots**: `TestWindowTrim_SingleHugeTurn_KeepsLastUser` encodes today's "a single turn may stay over budget" behaviour — once D6 exists, decide whether a completed-but-oversized previous turn may be **emptied** pre-turn (the ADR §6 "When" allows it; FR-016 restricts emptying to the current turn) and pin it.
- **Test-plan naming trap**: test 19 `TestMidTurnBudget_SameBudgetAsWindowTrim` will pass against whichever formula the implementer chooses unless CRIT-001 fixes the formula first.

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Recall mark / arg refusal (model-visible text) | — | tool name sanitised (E6) but `tool_call_id` is provider-supplied and unsanitised | — | — | — | prompt-injection via id | Sanitise the id the same way as the name (MAJ-007's composite key helps) |
| Live provider limits query | — | TLS only; cache file unsigned | — | API key sent to `custom` base URLs (operator-controlled; acceptable) | cold-cache fan-out at boot (MIN-006) | — | Cache file is data-only; a tampered cache can only **lower** the window (clamp) — acceptable, state it |
| Archive (full results up to 8 MB each) | — | escaped-line overflow breaks reads (MAJ-008) | emptying unlogged (MIN-009) | full, filtered content on disk — as today | per-turn disk growth unbounded (OBS-002) | — | |
| `recall_conversation(tool_call_id)` | — | — | — | reads evicted turns — as today | whole-archive read per call (OBS-002) | — | |
| `PUT /settings/context`, agent override | withAuth | — | — | — | — | `RequireNotBypass`? not stated | Decide whether these are high-blast-radius admin routes (they change what reaches the provider) |
| User-message refusal | — | — | — | reply reveals the bound only | bypass via channels (MAJ-001) | — | |
| MCP ingest | — | — | — | — | post-parse check does not bound memory (MAJ-009) | — | |

---

## Unasked Questions

1. What happens on an 8,192-token local model when one capped builtin result is larger than the whole budget? (CRIT-002)
2. Which of `initialArchiveLen`/`initialHistoryLength`/`restorePointHistory` is "the restore point", given `restorePointHistory` is never read? (CRIT-003)
3. Is the override the D3 message points at per-agent or per-(provider, model), and where is its schema? (CRIT-004)
4. Does the pre-turn `isOverContextBudget` site change to `windowTrim`'s budget, or stay at `> contextWindow`? (CRIT-001)
5. When the oldest over-budget content mid-turn is a previous turn, what happens to the in-memory `messages` slice? (MAJ-003)
6. How does the SPA learn that a result it already rendered was emptied? (MAJ-005)
7. Does a user's own Stop button now produce an `LLMError`? (MAJ-013)
8. Is `custom` local or cloud? (MAJ-010)
9. What is the cap for an MCP `isError` result, a denied result, and a `delegate` result? (MAJ-016)
10. After a successful turn that emptied results, are the marks permanent for that session (yes, per meta) — and can the operator or the model ever un-empty one short of recall? (Observation; state it.)
11. Is `/settings/context` gated by `RequireNotBypass` like sandbox-config PUT? (STRIDE)

---

## Spec-vs-ADR contradiction register

| # | Spec | ADR | Resolution needed |
|---|---|---|---|
| 1 | §5 `Wc = min(W, 0.9W)`, 0.9 stacked on 5 % headroom | §7 `trigger = min(absoluteBudget, 0.9 × resolvedWindow)`; §16a "no second formula" | CRIT-001 |
| 2 | E3/SC-005 "thrash guard only by injected fault" | §7 "should be unreachable" — same claim, same defect (pass-2 MAJ-011 carried forward) | CRIT-002 — fix in both |
| 3 | FR-016 "tool result **of the current turn**" | §6 "When": any tool result whose call is still in the window | State whether a completed previous turn's results may be emptied pre-turn |
| 4 | FR-029 "MUST NOT cut" mid-turn | §7 table row 1 "advance Skip" mid-turn | MAJ-003 |
| 5 | "per-model override, D2 rung 1" (inherited) | D2 rung 1 is per-agent | CRIT-004 — defect originates in the ADR |
| 6 | Recall `offset`/`length` in chars | §6.3 "the same interface `read_file` uses" (bytes) | OBS-004 |
| 7 | §1 Scope omits ADR §15 task 1 | §15 lists it as an implementation task | MAJ-015 |
| 8 | §1 "no new storage" | §9 new cache file | OBS-001 |

---

## Greenfield / Constraint #8 audit

- **Greenfield**: no back-compat, migration or aliasing language found. Two silences to close: `agents.defaults.context_window` vs the new settings field (MAJ-014) and the treatment of stale `summarize_token_percent` keys (OBS-003). Existing session meta without projection state loading as an empty set is a zero value, not a compatibility path — say so in one sentence.
- **Constraint #8**: `ContextSettings.yaml`, the `Agent.yaml` fields, the two `LLMError` codes, and the ADR-060-style inline schemas for the mark and the refusal are all named. Missing: the emptied/capped projection **frame** and the transcript-read projection field (MAJ-005); the per-model override schema if CRIT-004 resolves to (b); the D3 "model unusable" turn refusal's `LLMError` code (B-09 names a message, not a code — `model_unavailable` exists; say whether it is reused).
