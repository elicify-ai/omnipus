# Adversarial Review: Thinking & Reasoning Effort — Round 2

**Document reviewed**: `docs/internal/specs/thinking-reasoning-spec.md` (1531 lines; commit `61062a09c` on `feat/thinking-reasoning`, "fix round 1")
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-09-27
**Verdict**: BLOCK

**Reviewer notes**: independent review; the reviewer wrote neither the spec, ADR-095 nor the round-1 review. GitNexus was not available; every code claim below was checked with Grep/Read on the worktree at `61062a09c`. Round 1's 36 findings were diffed against the current text (see "Round-1 disposition" below). ADR-095 (Accepted) is treated as settled, except where the spec now departs from it or where the ADR itself never addressed a code fact this review found.

## Executive Summary

Round 2 found 24 findings: **2 CRITICAL, 14 MAJOR, 6 MINOR, 2 OBSERVATION. Verdict: BLOCK.**

The fix round did real work: the effort field placement, the thinking storage unit, the live-text callback, the hold-back rule and the toggle endpoint are all much better specified. But two new blocking defects surfaced, and neither round 1 nor ADR-095 caught them:

- **CRIT-001 — the default user's chat breaks.** The browser app treats any gap in frame sequence numbers as lost frames. When it sees one, it drops frames and asks the server to resend. The spec makes such gaps deliberate: every thinking frame hidden from a toggle-off user leaves one. Toggle-off is the default for every login. So every thinking-model turn would make every default user's browser drop answer frames and fire a re-attach request.
- **CRIT-002 — hidden thinking can leak into the model's own input.** The history-rebuild code that feeds past conversation back to the model picks entries by their `role` field, not their `type`. The spec does not say what `role` a thinking entry carries, and it states "no change" to that code. If an implementer gives thinking entries the natural `role: "assistant"`, the redacted thinking text goes back to the model as if it were a past answer. That breaks ADR-095 D8.3 and FR-015.

Beyond the two CRITICALs:

- **The revision log overstates what was fixed.** Several round-1 fixes it records as done are not in the text: the D16 scenario, the rebuild and sweep test oracles, the `thinking_level` zero-trace check, one file path, and the "docs included" line.
- **The spec is internally inconsistent.** The old §9.2 and §9.3 sections were left in below the new ones, and they bring back two errors round 1 already fixed. The traceability table has no rows for FR-029 to FR-036.

Frontend coverage (lenses 5, 8, 9 and 10) got the same depth as backend. That is where the CRITICAL gap-handling defect and the ChatSection state and copy gap (MAJ-012) were found.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 14 |
| MINOR | 6 |
| OBSERVATION | 2 |
| **Total** | **24** |

---

## Round-1 disposition (diff of round-1 findings vs the current text)

| Round-1 ID | Status in round 2 | Note |
|---|---|---|
| CRIT-001 (effort surfaces) | Fixed, with a new gap | Per-surface table added. The resolution order conflicts with D10, and `/effort` on CLI and messengers writes agent config (MAJ-003, MAJ-004) |
| CRIT-002 (storage unit) | Fixed, with new gaps | One `type:"thinking"` entry per round is decided. Its role/hydration, write timing and field name are open (CRIT-002, MAJ-010) |
| CRIT-003 (live text path) | Fixed, with a new gap | The callback is specified. Live text comes from before the markup strip, stored text from after it, and the ADR is not amended (MAJ-005) |
| MAJ-001 | Mostly fixed | The C1 path still sits under the USER.md resource (MIN-001) |
| MAJ-002, MAJ-003, MAJ-004, MAJ-009, MAJ-013, MAJ-017, MAJ-018 | Fixed | Some residual detail in MIN-004 and MIN-005 |
| MAJ-005 (`thinking_level`) | Partially fixed | Deletion is stated, but the zero-trace check the revision log claims is not in US-9 AS2 or any test (MAJ-008) |
| MAJ-006 (hold-back) | Fixed | |
| MAJ-007 (journal pressure) | Not fixed, and worse | The journal is per session and global, not per connection. Whole-text frames make the pressure worse, the requested test is absent, and the risk is self-labelled "accepted" (MAJ-006, MAJ-007) |
| MAJ-008 (deletion scope) | Partially fixed | §6 still says "docs included"; the inventory still misses files (MIN-003) |
| MAJ-010 (#711 component) | Partially fixed | The stale duplicate §9.3 still carries the wrong claim, and "build inline" is a one-off (MAJ-009, MAJ-013) |
| MAJ-011 (reachability) | Fixed, with a new gap | `/effort` semantics on CLI and messengers (MAJ-004) |
| MAJ-012 (ADR risks) | Fixed | Test 28's description is inverted (MIN-006) |
| MAJ-014 (budgets) | Partially fixed | The chat store functions at their budget ceilings are not named (MAJ-014) |
| MAJ-015 (label, DisclosureRow) | Partially fixed | "Thinking…" survives in §4, §10, §11 and the stale §9.2 (MAJ-009) |
| MAJ-016 (oracles) | Mostly not fixed | The revision log claims otherwise (MAJ-008) |
| MIN-001 (paths) | Not fixed | The "corrected" path `pkg/channels/webchat_channel.go` does not exist (MAJ-008) |
| MIN-003 (dev-bypass identity) | Fixed wrongly | The CLI token is described as empty-identity; the code sets `"cli"` (MAJ-011) |
| MIN-011 (traceability) | Regressed | FR-029 to FR-036 have no rows (MAJ-008) |
| Others (MIN-002, 004–010, 012; OBS-001–003) | Fixed | |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Deliberate sequence gaps break the SPA's gap rule: toggle-off (default) clients drop answer frames and re-attach on every hidden thinking frame

- **Lens**: Inconsistency & ADR/AS-IS contradiction / Contract-first gaps / UI states & journey gaps
- **Affected section**: §4 Boundary conditions ("sees deliberate sequence gaps … never closed or broken"); §6 WebSocket delivery ("MUST show deliberate sequence gaps"); §7 Gateway delivery; §20 row 5; ADR-095 D2 Boundary 1
- **Failure scenario**:
  1. Bob (toggle off, the shipped default) is attached to a session while a reasoning model streams.
  2. The hub skips thinking frame N for his connection, so the next frame he receives is seq N+1 while his cursor sits at N-1.
  3. The SPA's gate classifies `seq > cursor + 1` as a gap. It **drops that frame** and sends `attach_session{since_seq: N-1}`. Every further frame is silently dropped until the re-attach resolves.
  4. The journal-tail replay again skips the thinking frame, so the replayed stream carries the same hole.
  5. At best, every hidden thinking frame (up to 4/s per C2's coalescing) costs every default user a dropped frame plus a re-attach round trip. At worst the answer stream stalls or loops on re-attach.

  The spec's only safeguard is server-side: "not closed, unbound, or marked overflowed". The client side of the guarantee is never addressed.
- **Evidence**:
  - `src/store/chat/cursor.ts::gateFrameBySeq`: its table has the row "`seq > cursor.seq + 1` → gap: re-attach".
  - `src/store/chat/slices/frames.ts::applySeqGate`: on `kind === 'gap'` it sends `attach_session{since_seq, boot_id}` and returns `'drop-or-gap'`, so the frame never reaches the reducers.
  - ADR-095 D2 item 3 and its "gated connection sees deliberate seq gaps" sentence. Neither the ADR nor the spec mentions the SPA cursor (`grep -i 'cursor\|re-attach' spec` → 0 hits).
- **Recommendation**: Make the gap non-deliberate from the client's point of view, as a contract change in §1:
  - **Option A (preferred).** Add a content-free `seq_skip` frame (`{type, session_id, seq}`) to `asyncapi.yaml`. The hub delivers it to a gated connection in place of each hidden thinking frame, live and in the journal tail. The SPA advances its cursor on it. It carries zero thinking bytes, so the gate promise holds.
  - **Option B.** Every sequenced frame carries `prev_seq`, and the SPA gate checks that instead.

  Amend ADR-095 D2 Boundary 1 (architect). Replace §6's "MUST show deliberate sequence gaps" with "MUST observe a contiguous sequence (skip markers) and zero thinking bytes". Add integration and E2E tests: a toggle-off client streaming a thinking turn fires zero `chatSeqGapReattach` diagnostics and renders the full answer. Name `src/store/chat/cursor.ts` and `slices/frames.ts` in §2.2 and WP-D. See founder Q1.

---

#### [CRIT-002] The thinking entry's role is unspecified, and attach hydration maps by role: the redacted thinking copy can enter provider-bound history

- **Lens**: Incompleteness / Security / Inconsistency with ADR-095
- **Affected section**: §1 C3; §8.1 "Storage unit" and the reader table row `pkg/agent/attach_hydrate.go` ("Skip — the Content-only mapping is locked by guard test"); §2.2 row `attach_hydrate.go` ("No change"); FR-015; FR-030
- **Failure scenario**:
  1. The implementer writes the thinking entry as the natural shape: `Type: "thinking"`, `Role: "assistant"`, `Content: <redacted text>`. §8.1 says the entry's `content` is the redacted text.
  2. Hydration only special-cases `tool_call` and handoff `system` entries, then does `switch e.Role`. The thinking entry falls into `case "assistant"` and becomes `providers.Message{Role: "assistant", Content: <redacted thinking>}`.
  3. After any attach or re-hydration, the model receives its own redacted reasoning as a prior answer. On Anthropic, this is an assistant turn with no thinking blocks, which can also trip the availability guard.

  This is exactly what ADR-095 D8.3, FR-015 and the §6 prohibition forbid. "No change" to `attach_hydrate.go` is safe only if the entry has an empty role, and the spec never says so. The other role-keyed readers carry the same risk: `goal_triggers.go` (`e.Role == "user"`), `behavior_scan.go`, `steer_*`, and `session_end.go` recap assembly. The §8.1 table labels those "naturally excluded".
- **Evidence**: `pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript`. The loop checks `e.Type == session.EntryTypeToolCall`, then `switch e.Role { case "user": … case "assistant": msg := providers.Message{Role: "assistant", Content: e.Content} …}`. There is no `Type` filter for other types. The spec's C3 names the field `text`, while §8.1 says "the entry's `content` is the redacted text".
- **Recommendation**:
  - State the invariant in C3 and FR-030: a thinking entry has **no `role`** and carries its text in a dedicated field, `thinking_text`, not `content`.
  - Change `attach_hydrate.go` from "No change" to "explicit `Type == thinking → continue` before the role switch, plus a guard test that a thinking entry with any role value is never hydrated".
  - Replace "naturally excluded" in the §8.1 table with an explicit type filter for every role-keyed reader.
  - Add a proving test: transcript = user, thinking (with role assistant planted), assistant. The hydrated messages must equal user and assistant only.

---

### MAJOR Findings

#### [MAJ-001] The no-answer notice becomes model context: persisted as assistant `content`, it is hydrated back to the model as its own prior answer

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: §1 C4 ("content **is the D26 notice text**"); US-8; FR-018
- **Failure scenario**: After a reasoning-only round, the assistant entry's `content` is "The model (Provider · Model) did not respond". Two paths then feed it to the model:
  - **Transcript rebuild.** The next attach or hydration maps it as an assistant message (`attach_hydrate.go` `case "assistant"`), so the model sees that sentence as something it said.
  - **Context file.** If the turn also appends it to the agent's LLM-context file, it rides every later request.

  The model then imitates it or reasons about it. D26 made the notice "the same everywhere" for *consumers*. It did not decide that the *model* reads it. The spec never states whether the notice enters the context file or hydration.
- **Evidence**: `pkg/agent/attach_hydrate.go` assistant mapping (above); C4's text; no sentence in §8.1 or §2.2 addresses the context file for the no-answer round.
- **Recommendation**: State that the notice is display- and delivery-only. Implement that in three places:
  - **Context file.** The LLM-context file records the round as the provider returned it: empty content, with the raw reasoning where D15 applies.
  - **Hydration.** Skip assistant entries with `outcome: "no_answer"`.
  - **Test.** Add one: the next provider request after a no-answer round contains no notice string.

  No founder question: the display-only reading is the obvious default (D26 scopes the notice to consumers; the model-context rule of ADR-095 D8.3 keeps display copy out of provider-bound requests). Fix directly in round 2.

#### [MAJ-002] Effort resolution order contradicts D10: a per-message effort overrides the serving fallback's own effort

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: §1 "C5 resolution order" step (1); US-6 AS5; US-6 AS9; BDD "Each fallback uses its own effort"; FR-021
- **Failure scenario**: The user picks "high" in the composer, so it rides the message. The primary fails and fallback Y, configured "low", serves. Step (1) says the per-message value applies "when present", which gives "high". D10, US-6 AS5 and FR-021 say "the fallback's own effort", which gives "low". Two engineers build two behaviours. Whichever is picked, one BDD scenario fails.

  Also undefined:
  - An agent with no `model` of its own rides the instance default model. Does the agent's `reasoning_effort` apply, or `DefaultModel.reasoning_effort`? Step (2) never mentions the instance default.
  - When the agent's model changes (UI or `/model` via `ApplyAgentModel`), is the stored `reasoning_effort` cleared? D23 says effort is a property of the model choice. Keeping it silently carries a level that may be stale for the new model.
- **Evidence**: §1 C5 resolution order; US-6 AS5; `pkg/agent/loop_slash.go` (`rt.SwitchModel` → `al.ApplyAgentModel`).
- **Recommendation**: Rewrite the order as:
  1. The per-message effort applies **only when the per-message model (or the agent primary, if no per-message model) is the one serving**.
  2. Otherwise, the serving candidate's own effort.
  3. For a primary inherited from the instance default, `DefaultModel.reasoning_effort`.
  4. Otherwise nothing.

  State that a model change clears the stored effort for that surface. Add BDD rows for the two conflicts. No founder question: D10 (binding) already settles it — the serving fallback uses its own effort.

#### [MAJ-003] `/effort` on CLI and messengers "mirrors `/model`", which today rewrites the agent's persisted config, contradicting D23's "no new server state"

- **Lens**: Inconsistency / Security (Elevation of Privilege) / Ambiguity
- **Affected section**: §9.5; US-6 AS7; FR-023; §14 row "`/effort` command (CLI + messengers)"
- **Failure scenario**: On a messenger, `/model <name>` calls `rt.SwitchModel` → `al.ApplyAgentModel(agent.ID, value)`, which is "same path as the PUT /api/v1/agents/{id} model change". It is an agent-wide, persisted write. `/effort high` "mirroring" it therefore does one of two things:
  - It writes the agent's `reasoning_effort` for every chat and every user of that agent, triggered by any messenger participant.
  - It writes nothing, because a messenger has no client that re-sends per-message metadata, so "sets a new level" does nothing.

  The spec says both "per-message, no per-chat server state" (D23) and "sets a new level" on CLI and messengers (D27), and defines neither mechanism.
- **Evidence**: `pkg/commands/cmd_model.go::modelHandler` (`rt.SwitchModel(arg)`); `pkg/agent/loop_slash.go` (`rt.SwitchModel = func… return al.ApplyAgentModel(agent.ID, value)`, commented "same path as the PUT /api/v1/agents/{id} model change").
- **Recommendation**: State exactly what `/effort <level>` writes on CLI and messengers. The likely answer is the serving agent's primary `reasoning_effort` through the same agent-update path, which makes it persistent and agent-wide. Add that to the agent-authorization note in §8.2 and to §13 as a user promise ("anyone who can message the agent can change its effort, as with `/model`"). Add a BDD scenario. Alternatively, restrict `/effort` set-level to web (revisiting D27). See Q2.

#### [MAJ-004] Adapter coverage is only half specified: no per-adapter effort request mapping, and azure, codex, bedrock and CLI adapters are not mentioned

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: §1 C5; §2.2; §7 LLM providers; US-4; US-6 AS4; WP-G
- **Failure scenario**:
  - **No adapter sends effort today.** `grep -i reasoning_effort pkg/providers` → 0 request-side hits. WP-G must invent the mapping for each adapter:
    - **Anthropic.** Adaptive thinking type plus `output_config.effort`, as round-1 MIN-005 recommended. The spec now only bans the budget field.
    - **OpenAI-compatible.** Top-level `reasoning_effort`.
    - **OpenRouter.** `reasoning: {effort}`.
  - **Unmentioned adapters.** The tree also has Responses-API adapters (`azure`, `codex_provider.go` via `openai_responses_common`, which already parses `"reasoning"` output items), `bedrock`, and CLI subprocess providers. For these:
    - A user can set "high" from catalog data and the adapter silently ignores it (T1's visible note does not fire, because the level *is* in the catalog).
    - Any `LLMResponse.Reasoning` they populate becomes a stored thinking row with no live frames. That is an unplanned surface.
- **Evidence**: `ls pkg/providers` (azure, bedrock, codex_provider.go, claude_provider.go, copilot_cli_provider.go, openai_responses_common); `pkg/providers/openai_responses_common/responses_common.go` (`case "reasoning":`); `pkg/providers/anthropic/provider.go` (the old `options["thinking_level"]` mapping being deleted).
- **Recommendation**: Add an adapter matrix to §2.2 with the columns: adapter, sends effort (field name), live reasoning callback, stored thinking row, v1 scope. For adapters out of scope, state that the effort control shows "Default" only, whatever the catalog says, and that stored `Reasoning` from them either does or does not become a thinking row. Add request-shape unit tests per in-scope adapter. See Q4.

#### [MAJ-005] Live text comes from the streaming callback, before the markup strip; stored text comes from the post-strip parse, so live and reload can differ. The ADR is not amended

- **Lens**: Inconsistency with ADR-095 / Incorrectness
- **Affected section**: §1 C2, C3 (MIN-006 text); §2.2 "provider streaming callback" row; US-3 AS1; FR-007; FR-031
- **Failure scenario**:
  - **Different sources.** The live row's text comes from the new accumulated-text callback during streaming. The stored row is captured "from `Reasoning`/`ReasoningContent` … only after … `stripOrphanToolCallMarkup`". A model that leaks orphan tool-call markup into reasoning shows it live, then loses it on reload. That violates US-3 AS1 and FR-007 ("identical").
  - **ADR not amended.** ADR-095 D7 says capture is `parseResponse`-only and fills "the display copy for the turn". The spec adds a second, streaming capture source for the display copy without an ADR amendment.
- **Evidence**: C3's MIN-006 sentence; §2.2's callback row; ADR-095 D7 paragraph 1; `pkg/agent/loop_truncation.go::stripOrphanToolCallMarkup` runs on the completed response.
- **Recommendation**: Specify that the `final: true` frame carries the **stored** text, redacted after the strip, and that the client replaces its row text with it. Either apply the same markup strip to the accumulated text before each live frame, or accept a mid-stream difference with a final reconciliation and say so in US-3. Architect records a one-line ADR-095 D7 amendment: streaming display source added; signatures remain parse-only.

#### [MAJ-006] Journal pressure: the spec misstates the caps (per session and global, not per connection), and whole-text frames evict answer frames for toggle-off users in the same and other sessions

- **Lens**: Security (DoS) / Inconsistency with code / Testability
- **Affected section**: §6 WebSocket delivery bullet 3 ("per-connection 2048 frames / 1 MiB, 32 MiB global"); C2 (whole-text replace, 64 KiB cap, 4 frames/s); §20 row 10
- **Failure scenario**: The journal is **one per session hub**, trimmed when it passes 2048 frames or 1 MiB, with a 32 MiB global cap that trims across all sessions. Frames carry the whole text so far, at up to 4/s and up to 64 KiB each, so a 60 KB thinking stream over 30 s writes about 120 frames averaging about 30 KB, roughly 3.6 MB. The session journal is trimmed several times over. Three groups are hit:
  - **Toggle-off users in the same session.** Their seq-cursor catch-up of the answer and tool frames is evicted, so they fall back to snapshot.
  - **Everyone else.** A few concurrent thinking sessions push the global cap, trimming *other* sessions' journals.
  - **The spec's own framing.** Its accepted degradation ("a gated toggle-on connection may fall back") names the wrong victim. Round 1's requested test (toggle-off catch-up intact) is absent.
- **Evidence**: `pkg/gateway/ws_session_hub.go`: `hubJournalMaxFrames = 2048`, `hubJournalMaxBytes = 1 << 20` enforced on `h.journal` (the session hub), `hubGlobalJournalMaxBytes = 32 << 20 // 32 MiB across all sessions`.
- **Recommendation**:
  - **Supersede in the journal.** When a thinking frame for `entry_id` X is journaled, drop the earlier journaled thinking frame for X. Only the latest whole-text frame is needed, because the client replaces. This bounds thinking's journal share to one frame per active round.
  - **Correct the §6 caps text.**
  - **Add the test.** A 64 KiB thinking stream leaves a toggle-off connection's catch-up of answer frames intact, and a second session's journal is not trimmed.

  See Q3.

#### [MAJ-007] §20 labels self-decided risks as founder-accepted

- **Lens**: Inconsistency / Testability (process false-green)
- **Affected section**: §20 heading ("residual risks the founder has already accepted"); rows 9 and 10
- **Failure scenario**: Row 9 (hold-back delay) and row 10 (journal-pressure degradation, 64 KiB live cutoff) are marked "**Accepted** (MAJ-006 rule)" and "**Accepted** (MAJ-007)". Neither appears in D1 to D28 or in ADR-095. The implementer and the 8-reviewer gate will treat them as founder decisions and dismiss findings against them. Row 10 is exactly where MAJ-006 above finds real cross-user impact.
- **Evidence**: §20 rows 9 and 10; the interview's decisions log (D22 to D28 cover Q1 to Q7 of round 1 only).
- **Recommendation**: Relabel rows 9 and 10 as "Proposed by spec author, pending founder", and put them to the founder (Q3). Keep "Accepted" only for rows that cite a D-number or an ADR clause.

#### [MAJ-008] The revision log claims fixes that are not in the text, and the traceability matrix is incomplete while claiming completeness

- **Lens**: Testability & false-green risk / Inconsistency
- **Affected section**: "Revision log — fix round 1"; §19 Traceability Matrix and its completeness paragraph; §15 Group A, C and F scenarios; §18 SC-004; §6 Scope; §2.2
- **Failure scenario**: The next reader trusts the log and the completeness line, and the unfixed items ship.

  | Revision log claim | Actual text |
  |---|---|
  | MAJ-016: "D16 scenario made deterministic (asserted present)" | §15 "Journal catch-up may re-deliver…" still reads "**may** be re-delivered" (unfalsifiable) |
  | MAJ-016: rebuild oracle restated | "…survives a mid-turn context rebuild" still has "without a provider rejection" as its oracle, and SC-004 says "without provider rejection". Neither can fail against a recorded mock |
  | MAJ-016: sentinel for the sweep | The debug-log sweep scenario and test 16 still search for "that turn's reasoning text", with no planted sentinel |
  | MAJ-005: "zero-trace check … US-9 AS2 extended" | US-9 AS2 names only the reasoning-channel key, env vars and publish path. `thinking_level` appears in no scenario or test |
  | MIN-001 / MAJ-008: path "`pkg/channels/webchat_channel.go`" | That file does not exist. The file is `pkg/gateway/webchat_channel.go` |
  | MAJ-008: sweep excludes specs and archive | §6 Scope still says "MUST return zero hits (docs included)" |
  | §19: "every FR-001…FR-027 and FR-029…FR-036 appears above" | The matrix ends at FR-028 (deleted), with **no rows** for FR-029 to FR-036 |
- **Evidence**: `ls pkg/channels/webchat_channel.go` → no such file; `pkg/gateway/webchat_channel.go::ReasoningChannelID` exists. The sections are as quoted.
- **Recommendation**:
  - Apply each claimed fix for real. The D16 row becomes "after an ON→OFF flip, reconnect inside retention → the journal-tail replay contains the on-period thinking frames (asserted present)". The rebuild and restart oracles assert byte-identical `ThinkingBlocks` on the outgoing request. The sweep plants a unique sentinel through a mock provider.
  - Add `thinking_level`/`ThinkingLevel`/`parseThinkingLevel` to US-9 AS2 and test 17.
  - Fix the path. Change §6 Scope to match the sweep exclusions.
  - Add rows for FR-029 to FR-036 and delete the FR-028 row.
  - Re-derive the completeness paragraph by listing, not by assertion.

#### [MAJ-009] Stale duplicate sections reintroduce two errors round 1 already fixed ("Thinking…" label, the #711 commit claim)

- **Lens**: Inconsistency / Design-system reuse & brand
- **Affected section**: §9 (two "### 9.2" and two "### 9.3" headings; two "### 9.6"); §4 Behavioral Contract bullet 1; §10 step 2; §11 "Row label"; §14 (the delivery statement is duplicated); §15 (the "Group E" heading is duplicated; a stray line in the Group F sweep scenario); §16 Gates (stray `"`); §2.2 (the `handleReasoning` row is duplicated); §23 ("Sequencing rules from the interviewSequencing rules…")
- **Failure scenario**: The second §9.2 says the row shows a "Thinking…" label and uses "the existing tool-call row collapse pattern". That is the MAJ-015 collision round 1 fixed. The second §9.3 says Option C was "landed on `feat/provider-messages`, commit `02eeba8c5`", the false claim round 1 disproved. §4, §10 and §11 also still say "Thinking…" (10 occurrences in the file). An implementer, or the RED author deriving oracles, reads whichever copy they land on.
- **Evidence**: `grep -c 'Thinking…' spec` → 10; the duplicated headings as listed.
- **Recommendation**: Delete the second §9.2, §9.3 and §9.6 blocks and the other duplicates. Replace every user-facing "Thinking…" row label with "Reasoning". Keep "Thinking…" only where the text refers to the existing `ThinkingIndicator`. Run a duplicate-heading check before round-2 fixes are declared done.

#### [MAJ-010] Thinking-entry write timing, uniqueness and field name are undefined

- **Lens**: Incompleteness / Contract-first gaps
- **Affected section**: §1 C3; §8.1 "Storage unit"; §9.2 Partial ("up to the last intermediate persist") and Error ("renders what was stored (redacted-so-far)"); FR-030
- **Failure scenario**: The transcript is append-only JSONL, and the ID is "minted at capture start". The spec never says **when** the entry is written. Each reading causes a defect:
  - **Written once at round end.** A mid-round attach or crash shows nothing for the current round. The §9.2 "last intermediate persist" and "redacted-so-far on failure" states then describe nothing.
  - **Written repeatedly.** The transcript holds several entries with one ID. Replay and REST need latest-wins de-duplication that no reader has.

  Cancellation (`turn_canceled`) mid-thinking is also unaddressed. Separately, C3 calls the field `text` while §8.1 calls it `content`, and the contract author has to guess.
- **Evidence**: `pkg/agent/turn_transcript.go` (append path); C3 vs §8.1 wording.
- **Recommendation**: State one rule. For example: the thinking entry is appended once, at round end or at cancel or error, with the final redacted text; mid-round state lives only in C2 frames and the journal. Then rewrite §9.2 Partial and Error to match. Name the field `thinking_text` (see CRIT-002). Add a scenario: "turn canceled mid-thinking → one thinking entry with the text so far".

#### [MAJ-011] US-1 AS5 and the gated-boundary dataset say CLI tokens have an empty identity; the code sets `userID = "cli"`, so a gate keyed on identity can leak to a real "cli" account

- **Lens**: Security (Spoofing / Information disclosure) / Inconsistency with code
- **Affected section**: US-1 AS5 ("CLI token, developer bypass, environment token — authentication paths that leave the caller's user identity empty"); dataset "gated boundary matrix" row 3; revision log MIN-003
- **Failure scenario**: The RED author derives the predicate from AS5: "empty identity → off; otherwise look up the row". A CLI-token connection carries `userID = "cli"`. If an account named "cli" exists with its toggle on, the lookup finds it and the CLI connection receives thinking. §5 says the opposite ("keys on how the caller authenticated"), so the BDD source and the edge-case list disagree.
- **Evidence**: `pkg/gateway/auth.go::resolveBearerIdentity` returns `&config.UserConfig{Username: "cli"}, true, true` for a CLI token. `pkg/gateway/websocket.go::authenticateWS` sets `wc.userID = user.Username` and `wc.isCLIToken = viaCLIToken`.
- **Recommendation**: State the predicate explicitly in US-1 AS5, §8.2 and FR-002: `show = !isCLIToken && userID != "" && row found && row.show_thinking && no read error`. The REST side uses the same `viaCLIToken`. Fix dataset row 3 ("CLI token: userID `cli`, isCLIToken true → hidden even when an account named `cli` has its toggle on"). Add that row to test 8 and test 10.

#### [MAJ-012] The Settings → Chat toggle: the spec relies on loading, error and auto-save states that ChatSection does not have, and places a synced setting under "local, per-device" copy

- **Lens**: UI states & journey gaps
- **Affected section**: §9.1 (Loading "rides the section's existing loading state"; Error "the section's standard error treatment"; Partial "the standard auto-save indicator covers it"); §12 "Show-thinking toggle" row
- **Failure scenario**:
  - **No server states exist.** ChatSection is purely local. It reads a Zustand store persisted to localStorage and makes no server calls, so it has no loading state, error treatment or `AutoSaveIndicator`. The implementer either ships a switch that flashes "off" before the GET resolves, with no error handling, or invents the states ad hoc.
  - **Contradictory copy.** The card's existing helper text reads "This is a local, per-device display preference — it is not synced across devices". A server-synced privacy toggle placed "directly below" it inherits a contradictory explanation.
- **Evidence**: `src/components/settings/ChatSection.tsx` (header comment: `useChatPreferencesStore, persisted to localStorage`; helper `<p>` text as quoted; no `useQuery` or `AutoSaveIndicator`).
- **Recommendation**:
  - **Own card.** Put the toggle in its own `Card` in ChatSection, with its own helper text.
  - **Loading.** Switch disabled with a skeleton until the C1 GET resolves.
  - **Error.** Revert, plus a catalogued inline error, plus a retry of the GET.
  - **Saving.** A catalogued `AutoSaveIndicator` for the PUT.
  - **Copy.** Scope the Verbose-chat helper text to its own card.
  - **Tests.** Add component tests for the loading, error and save states.

#### [MAJ-013] The no-answer notice is built "inline", a one-off UI that §12 still claims is catalogued, with conditional ownership

- **Lens**: Design-system reuse & brand
- **Affected section**: §9.3; §12 "No-answer notice" row and the lead sentence "Every control below is a catalogued component"; §22 bullet 2
- **Failure scenario**: This feature ships an inline strip copying Option-C classes from a Storybook demo. #711 later ships its own catalogued console strip, and the product has two builds of one visual language, which is the outcome D20 and D26 forbid. "Inline" also bypasses the design-system publication contract that skill rule 14 expects for a recurring UI job; provider messages and no-answer notices are two instances already.
- **Evidence**: §9.3 "builds the notice **inline** with the approved Option-C visual language"; §12's lead sentence; round-1 MAJ-010 evidence (`02eeba8c5` touches only `ProviderMessageVisualOptions.stories.tsx`).
- **Recommendation**: Decide ownership now. Either this feature builds a catalogued `ConsoleStrip` component with the four-part contract (export, `design-system/catalog.json`, `@source` in `src/styles/library.css`, manifest) and #711 reuses it, or WP-D declares a hard dependency on #711 landing it first. Correct §12's lead sentence.

#### [MAJ-014] The SPA frame consumers are unnamed, and they sit at grandfathered function-budget ceilings

- **Lens**: Reachability / Infeasibility (budget rule)
- **Affected section**: §2.2 frontend list; §23 WP-D and WP-H; SC-013; MAJ-014 (round 1) disposition
- **Failure scenario**: Thinking frames, thinking replay entries, the `outcome` marker and per-message `reasoning_effort` metadata all have to be handled in the chat store. The spec names only components and `ChatScreen.tsx`. The real reducers are grandfathered, shrink-only functions:
  - `handleFrame` (1773 lines)
  - `createFrameSlice` (1777)
  - `handleReplayAndStatusFrame` (580)
  - `sendMessage` (428), where the per-message `reasoning_effort` rides

  These are store functions, not React components, so the budget **fails** rather than warns. WP-D and WP-H go red on `make lint-budgets`, with no extract-first step planned. The row also cannot render at all if the store never accepts the new frame type.
- **Evidence**: `scripts/budgets/functions.txt` entries for `src/store/chat/slices/frames.ts handleFrame 1773`, `createFrameSlice 1777`, `replay-and-status-frames.ts handleReplayAndStatusFrame 580`, `outbound-lifecycle.ts sendMessage 428`; `src/store/chat/slices/frames.ts` comment "handleFrame is already at its grandfathered line-budget ceiling".
- **Recommendation**: Add these files to §2.2 and WP-D and WP-H. Specify extract-first: a new `slices/thinking-frames.ts` handler called from `handleFrame` with a net line change ≤ 0, and the metadata assembly extracted out of `sendMessage`. Add the store-level tests: a thinking frame replaces the row text, the replay entry maps to a row, and `outcome` maps to the notice.

---

### MINOR Findings

#### [MIN-001] C1 still nests the per-login preference under the USER.md workspace resource
- **Lens**: Contract-first gaps / Ambiguity
- **Affected section**: §1 C1 (`GET/PUT /user-context/thinking`)
- **Failure scenario**: `/user-context` is `getUserContext`, "USER.md content from the default workspace", tag Workspace. Nesting an account-scoped preference under it mixes scopes and authorization semantics, and misleads anyone reading the API. Round-1 MAJ-001 raised this; the path is unchanged.
- **Recommendation**: Host it under the auth/account area, for example `GET/PUT /auth/preferences/thinking`, next to `/auth/session` and `/auth/change-password`. Architect owns the final path.

#### [MIN-002] The reader-table row for `rest_tasks.go` is wrong
- **Lens**: Inconsistency with code
- **Affected section**: §8.1 reader table, `pkg/gateway/rest_tasks.go` "Gated strip"
- **Failure scenario**: An implementer adds gate plumbing to an endpoint that only ever emits `judge_verdict` entries (`if e.Type != session.EntryTypeJudgeVerdict { continue }`). That is wasted work, and it drifts from ADR-095's two-serializer Boundary 3.
- **Recommendation**: Change the row to "Naturally excluded — reads `judge_verdict` entries only".

#### [MIN-003] The WP-A deletion inventory is still incomplete
- **Lens**: Incompleteness
- **Affected section**: §2.2; §23 WP-A; Group F sweep
- **Failure scenario**: `pkg/config/config.go` and `pkg/agent/loop_run_turn_response.go` also reference the reasoning channel, and `pkg/channels/README.md` documents it. None is in WP-A. "13 channel-package implementations" is also inaccurate: the method is implemented once, by `BaseChannel` (plus `webchatChannel`), and 13 packages *use* it.
- **Recommendation**: List all 19 Go files from `grep -rln 'ReasoningChannelID\|reasoning_channel_id\|REASONING_CHANNEL_ID' pkg cmd`, plus the README and the AS-IS doc. Note that WP-A and WP-C both edit `loop_run_turn_response.go`.

#### [MIN-004] "Failed recap attempt (retry eligible)" is undefined against the recap's real retry logic
- **Lens**: Ambiguity
- **Affected section**: §1 C4; US-8 AS6; §2.2 `session_end.go` row
- **Failure scenario**: The recap retries only on transient stream errors (`maxTransientRetries`, `isTransientStreamError`) and otherwise moves to the next candidate. A no-answer result is not an error there, and the `outcome` marker lives on transcript entries, not on the recap response. "Retry eligible" could mean retrying the same candidate, moving to the next candidate, or waiting for the next flush point.
- **Recommendation**: State it as "a recap response with empty text after the strip is treated like a candidate failure: the next recap candidate is tried; if all fail, no recap is persisted". Add a unit test.

#### [MIN-005] "Session usage totals include thinking" has only a per-model carrier and no named UI surface
- **Lens**: Incompleteness / Reachability
- **Affected section**: §1 C7; US-7 AS1 and AS3; FR-024; FR-036; §14 (no usage row)
- **Failure scenario**: C7 adds `thinking` to `ModelTokens` only. `SessionStats` and `AgentTokenEntry` flat totals (`tokens_in/out/total`) stay without it. No screen is named that shows the figure, so US-7 cannot be demonstrated in UAT under SC-010.
- **Recommendation**: Either state that the thinking figure appears only in `by_model` breakdowns and name the component that renders them, or add `tokens_thinking` to `SessionStats` and `AgentTokenEntry`. Add a §14 row either way.

#### [MIN-006] Test 28's description is inverted; C4's "live done-frame" names no schema
- **Lens**: Testability / Contract-first gaps
- **Affected section**: §16 test 28; §1 C4
- **Failure scenario**:
  - **Test 28.** It plants the sentinel in a *tool result* and asserts it never reaches a gated surface. That tests tool-result gating, which is not a promise. The ADR's test is the reverse, thinking text never appearing in tool results, which test 24 already covers.
  - **C4 schema.** The `truncation_reason` precedent lives in `DoneStats.yaml` and inline in `asyncapi.yaml`, and C4 doesn't say which one gains `outcome`.
- **Recommendation**: Merge test 28 into test 24, or restate it as "thinking sentinel ∉ any tool-result frame for toggle-on or toggle-off". Name `DoneStats.yaml` (plus the asyncapi inline copy) in C4.

---

### Observations

#### [OBS-001] `reasoning_effort` on the thinking entry has no consumer
- **Lens**: Overcomplexity
- **Affected section**: §1 C3
- **Suggestion**: No user story, UI state or test reads the effort stored on a thinking row. Drop the field, or name the UI that shows it, such as the collapsed row summary.

#### [OBS-002] The spec is now 1531 lines, with a 45-row revision log embedded
- **Lens**: Overcomplexity
- **Affected section**: "Revision log — fix round 1"; finding-ID annotations throughout
- **Suggestion**: The inline "(MAJ-0xx)" tags and the embedded log helped this round but will confuse implementers, because round-1 and round-2 IDs collide. After fix round 2, move the logs to the review files and strip the finding tags from the normative text.

---

## Structural Integrity

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | "Draft — grill round 1 complete…" |
| ADR linked (or explicitly stated not needed) | PASS | ADR-095. Needs amendments for CRIT-001 and MAJ-005 |
| Contract changes stated first, citing `contracts/` | FAIL | Stated first. Missing the skip-marker frame (CRIT-001), the thinking-entry role and field (CRIT-002, MAJ-010), and the done-frame schema name (MIN-006) |
| API and data section | PASS (with gaps) | Write timing is undefined (MAJ-010) |
| UI screens and states (loading/empty/error/partial) | FAIL | ChatSection states assumed to exist (MAJ-012); stale duplicate §9.2/§9.3 (MAJ-009) |
| User journey section | PASS (with errors) | §10 still says "Thinking…" (MAJ-009) |
| Accessibility and keyboard section | PASS | Round-1 gaps closed; §11 label text is stale (MAJ-009) |
| Design-system components, catalogue-first | FAIL | Inline notice (MAJ-013) |
| Security and user promises section (when touched) | PASS (with gaps) | `/effort` authorization (MAJ-003); CLI predicate (MAJ-011) |
| BDD acceptance scenarios, oracle from spec | FAIL | Unfalsifiable oracles remain (MAJ-008); AS5 contradicts code (MAJ-011) |
| Traceability table: requirement -> scenario -> test | FAIL | FR-029 to FR-036 missing, false completeness claim (MAJ-008) |
| Reachability section (tool policy / screen wiring) | PASS (with gaps) | Store consumers unnamed (MAJ-014); no usage-total surface (MIN-005) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| E2E / integration (default user) | A toggle-off client streaming a thinking turn: zero re-attach diagnostics, full answer rendered | CRIT-001 |
| Unit (hydration guard) | A thinking entry with any role is never hydrated; a no-answer notice is never hydrated | CRIT-002, MAJ-001 |
| Integration (journal) | Toggle-off catch-up intact after a 64 KiB thinking stream; a second session's journal not trimmed | MAJ-006 |
| Unit (adapter request shape) | Per in-scope adapter: exact effort field; out-of-scope adapters send nothing | MAJ-004 |
| Integration (live vs stored) | Orphan-markup reasoning: the final frame text equals the stored text | MAJ-005 |
| Unit (predicate) | CLI token plus a real "cli" account with toggle on → hidden | MAJ-011 |
| Frontend component states | Toggle loading, error and saving states | MAJ-012 |
| Frontend store | Thinking frame replace, replay mapping, outcome → notice | MAJ-014 |
| Cancellation | Turn canceled mid-thinking → exactly one thinking entry | MAJ-010 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Gated boundary matrix | CLI token vs a real account named "cli" | Expect hidden (MAJ-011) |
| Effort value handling | Per-message "high" while fallback "low" serves | Expect "low" (MAJ-002, per D10) |
| Effort value handling | Agent without its own model, instance default carries effort | Expect the default model's effort (MAJ-002) |
| Effort value handling | An adapter with no effort support and a catalog listing levels | Expect Default-only control, nothing sent (MAJ-004) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Gate predicate | risk | ok | ok | risk | ok | ok | CLI identity `"cli"` collides with account lookup (MAJ-011) |
| Hub journal | ok | ok | ok | ok | risk | ok | Whole-text frames evict other users' and sessions' catch-up (MAJ-006) |
| SPA seq gate | ok | ok | ok | ok | risk | ok | Deliberate gaps → re-attach storm, dropped answer frames (CRIT-001) |
| Transcript → provider hydration | ok | risk | ok | risk | ok | ok | Role-keyed mapping ingests thinking and notice (CRIT-002, MAJ-001) |
| `/effort` on messengers | ok | risk | risk | ok | ok | risk | Any participant rewrites agent-wide effort; no audit stated (MAJ-003) |
| Toggle endpoint (C1) | ok | ok | ok | ok | ok | ok | GET plus idempotent PUT, CSRF, audit (fixed); path scope is MIN-001 |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | N/A | No new tool. `/effort` is a `pkg/commands` Definition (§9.5) |
| User-facing: named screen/component renders it? | Partially | Toggle: ChatSection (states missing, MAJ-012). Row: DisclosureRow, but the store consumers are unnamed (MAJ-014). Notice: inline one-off (MAJ-013). Usage totals: no surface (MIN-005) |
| Test plan describes execution, not just authorship? | Partially | SC-010 requires UAT evidence per §14 row; several oracles are still unfalsifiable (MAJ-008) |

---

## Unasked Questions

1. What `role` does a thinking entry carry, and in which field does its text live (CRIT-002)?
2. When exactly is the thinking entry appended: once, or repeatedly, and on cancel (MAJ-010)?
3. Does the `final` C2 frame carry the stored text (MAJ-005)?
4. Which adapters send effort, under which request field, and which show "Default" only (MAJ-004)?
5. Does a model change clear the stored effort for that surface (MAJ-002)?
6. What does `/effort <level>` persist on CLI and messengers, and who may run it (MAJ-003)?
7. What does a recap with empty text do: next candidate, or nothing (MIN-004)?

---

## Questions for the founder

(Capped at four by the dispatch brief. MAJ-002 and MAJ-001 were drafted as founder questions and dropped: D10 already decides MAJ-002, and MAJ-001 has an obvious default — both are direct round-2 fixes.)

1. **Q1 — Hidden thinking breaks the default user's live chat (CRIT-001).** The browser app treats a missing sequence number as "I lost frames" and asks the server to resend. Hiding thinking frames from toggle-off users creates exactly such gaps, several a second, for every default user. Options:
   - **(A)** The server sends a tiny, content-free "skip" frame in place of each hidden thinking frame, so numbers stay continuous. This is one new wire frame and an ADR-095 amendment.
   - **(B)** Every frame carries the previous sequence number, and the app checks that instead.
   - **(C)** Make thinking frames unnumbered. This contradicts ADR-095's "no unsequenced side-channel".

   **Recommendation: A.**
2. **Q2 — What does `/effort high` do in Telegram or the CLI (MAJ-003)?** Today `/model` there changes the agent's saved model for everyone who uses that agent. Options:
   - **(A)** `/effort` does the same: it saves the agent's effort for everyone, and anyone who can message the agent can change it, just like `/model`.
   - **(B)** On CLI and messengers, `/effort` only shows the current level; changing it is web-only (revises D27).

   **Recommendation: A**, stated plainly in the docs.
3. **Q3 — Long thinking crowds out everyone's reconnect buffer (MAJ-006, MAJ-007).** Each session keeps a 1 MiB buffer used to catch up after a reconnect. Long thinking fills it in seconds and pushes out other users' answer frames, and with many sessions it trims other sessions too. Options:
   - **(A)** Keep only the latest thinking frame per row in the buffer. It is small, and it fixes the problem.
   - **(B)** Accept the degradation, since reconnecting users fall back to a slower full reload.

   **Recommendation: A.** Either way, the spec should not call this "founder-accepted" until you decide.
4. **Q4 — Providers the spec does not cover (MAJ-004).** Effort and thinking are specified for Anthropic and OpenAI-compatible (incl. OpenRouter) only. Azure, Codex, Bedrock and the CLI-based providers are not mentioned. Options:
   - **(A)** v1 covers only the two specified families; others show "Default" only and never show thinking rows.
   - **(B)** Include the OpenAI Responses-API family (Azure, Codex) in v1.

   **Recommendation: A.**

---

## Verdict Rationale

**BLOCK.** CRIT-001 is a production defect in the default configuration. Every login starts with the toggle off, and the spec's own delivery design, deliberate sequence gaps, collides with the browser app's existing gap-recovery rule. Every thinking turn would drop answer frames and fire re-attach requests for every default user. CRIT-002 leaves open, and on the worst reading causes, the one leak ADR-095 guards hardest: redacted thinking entering the model's input through role-keyed hydration.

The MAJOR findings fall into four groups:

- **New design gaps.** Effort resolution vs D10 (MAJ-002), `/effort` on messengers (MAJ-003), adapter coverage (MAJ-004), the live vs stored text source (MAJ-005), journal pressure (MAJ-006), write timing (MAJ-010) and the CLI identity predicate (MAJ-011).
- **Process integrity.** Self-accepted risks (MAJ-007), and revision-log claims plus a traceability table that do not match the text (MAJ-008).
- **Document hygiene with real consequences.** Stale duplicates re-introducing fixed errors (MAJ-009).
- **Frontend gaps.** The toggle's UI states (MAJ-012), the inline notice (MAJ-013) and the store budget ceilings (MAJ-014).

### Escalation to the founder

This was the final grill round, so the blocking findings go to the founder, not to a third round:

| Finding ID | Why it's still open | Founder decision needed |
|---|---|---|
| CRIT-001 | New in round 2: neither ADR-095 nor round 1 considered the SPA's gap-recovery rule. The fix is a wire-contract addition and an ADR-095 D2 amendment | Q1: skip marker (A), `prev_seq` (B), or unsequenced (C) |
| CRIT-002 | New in round 2: the storage unit was decided in fix round 1 without specifying role or field, and the spec says "no change" to role-keyed hydration | No product decision needed. It must be closed in fix round 2 (role empty, `thinking_text` field, explicit type filter plus guard test). If it is not closed there, the founder decides whether implementation may start |
| MAJ-006 / MAJ-007 | Rated MAJOR, not blocking, but mislabelled "founder-accepted" | Q3 |

### Recommended Next Actions

- [ ] Add the skip-marker frame (or `prev_seq`) to §1, amend ADR-095 D2, and add the default-user no-reattach test (CRIT-001)
- [ ] Specify the thinking entry's empty role and `thinking_text` field, the explicit type filters, and the hydration guard test (CRIT-002)
- [ ] Keep the no-answer notice out of model context and add a test (MAJ-001)
- [ ] Rewrite the C5 resolution order and state effort clearing on model change (MAJ-002)
- [ ] Define what `/effort` writes on CLI and messengers, and its authorization (MAJ-003)
- [ ] Add the adapter coverage matrix and per-adapter request fields (MAJ-004)
- [ ] Make the final frame carry the stored text, and amend ADR-095 D7 (MAJ-005)
- [ ] Add journal supersede-by-`entry_id`, fix the §6 caps text, add the toggle-off catch-up test (MAJ-006)
- [ ] Relabel §20 rows 9 and 10 (MAJ-007)
- [ ] Apply every revision-log claim for real, add FR-029 to FR-036 to §19, fix the path and "docs included" (MAJ-008)
- [ ] Delete the duplicate sections and stale "Thinking…" labels (MAJ-009)
- [ ] Define thinking-entry write timing and the cancel behaviour (MAJ-010)
- [ ] State the gate predicate with `isCLIToken`, and fix AS5 and dataset row 3 (MAJ-011)
- [ ] Give the toggle its own card and real loading, error and save states (MAJ-012)
- [ ] Decide notice component ownership, catalogued (MAJ-013)
- [ ] Name the chat store slices and extract-first steps (MAJ-014)

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/thinking-reasoning-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
```
