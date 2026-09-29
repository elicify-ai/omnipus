# Adversarial Review: Chat Steps and Agent Checklist (#991 + #1072)

**Document reviewed:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/specs/chat-steps-and-agent-checklist-spec.md`

**Mode:** Spec

**Round:** Round 1 of 2 — one grill only; no correction performed

**Review date:** 2026-09-29

**Reviewed branch / commit:** `spec/chat-steps-todo-991-1072` / `95714e0d992aef5808502415c1e0b2ad7c784d69`

**Verdict:** REVISE

## Executive Summary

**Do not implement this draft.** Seven major findings concern contradictory acceptance expectations, checklist retention, incompatible transport outcomes, delayed failure visibility, unchecked checklist data, missing loading/recovery journeys, and incomplete accessibility requirements. Four minor findings and two non-gating observations complete the review. The draft honestly identifies Q1–Q6 as open; their existence is not disguised as a newly discovered defect, and no recommendation below is founder approval.

The frontend received the same scrutiny as the data path: both renderers, disclosure behavior, panel reachability, recovery states, keyboard/focus, narrow layouts, catalog reuse, and actual acceptance oracles were examined. This is a source-grounded specification review, not a claim that the feature works or that runtime failures were reproduced.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 7 |
| MINOR | 4 |
| OBSERVATION | 2 |
| **Total** | **13** |

**Method and limits.** The `grill-spec` invocation returned one review; this saved report verifies and completes that same round against the checkout's current twelve-lens template. GitNexus MCP tools were unavailable, so direct source reads and searches were used. No production code, contracts, tests, or the draft spec were changed. No local test suite or feature test was run. The skill's returned diagnostic-probe claims have no saved receipts in this report and are **not relied upon**; the relevant mechanisms were independently verified by reading source.

The side-panel reference was read from a Git object in this checkout, not by entering or reading another worktree: branch `feat/resizable-side-panels` at `2448d93fc732f73b5974bfabed4e5c47d870f011`. Its written FR-022/SP-41 requirement is verified; its implementation and runtime acceptance are not certified here.

## Source register

Evidence keys below resolve to the exact source read in this task. All filesystem references are absolute. Issue bodies and comments were obtained with `gh issue view` on 2026-09-29; each request exited 0.

| Key | Source and verified fact |
|---|---|
| E01 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/specs/chat-steps-and-agent-checklist-spec.md` — complete draft, including FR-001–015, S-01–17, datasets, tests 01–16, Q1–Q6 and release prohibition. |
| E02 | [#991 — Chat tool-call Steps](https://github.com/elicify-ai/omnipus/issues/991)::Wanted / Scope / Acceptance — consecutive calls, first-sentence objective, live readable action, visible failures, unchanged verbose behavior and transcript/wire, reload parity. |
| E03 | [#1072 — Todo list in chat and side panel](https://github.com/elicify-ai/omnipus/issues/1072)::Behaviour / Acceptance — separate checklist, backend-state language, replacement, compact/collapsible presentation, unchanged wire and reload parity. |
| E04 | [#1021 — Plan and task visibility](https://github.com/elicify-ai/omnipus/issues/1021)::R1–R4; [#1049 — Task running indicator](https://github.com/elicify-ai/omnipus/issues/1049)::Acceptance — separate plan/task pills, links and drill-down; task-graph spinner and token counter. |
| E05 | Git object `2448d93fc732f73b5974bfabed4e5c47d870f011`, side-panel specification at repository location corresponding to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/specs/side-panel-shell-spec.md` (not a working-tree file here)::FR-022 / SP-41 / Wave 3 — one catalogued chat-style spinner-plus-token indicator on running tasks in Board/List/Graph/Plans; wave-3 test numbers are still marked TBD in that document. |
| E06 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/messages.ts::MAX_MESSAGES_PER_SESSION` = 500; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/session.ts::applyMessageArray` discards oldest messages and dependent call maps; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame` enforces the same cap. |
| E07 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/api/sessions.ts::rawToToolCall` — denied becomes cancelled; pending and unhandled values become running; parameters become params; error is assigned undefined. |
| E08 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/ToolCall.yaml::status / parameters / duration_ms`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/ToolCallResultFrame.yaml::status`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/gateway/replay.go::toolCallResultStatus` — persisted status domain is richer than success/error result frames; nonempty non-success statuses normalize to error when a replay result is emitted; parameters are an open object. |
| E09 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/tools/todos.go::SetTodosTool.Execute / parseTodosArg / findActiveGoalTask / archiveOtherScratchpadCards` — full replacement, status defaulting, session-scoped scratchpad lookup, nonfatal archival failure, formatted read-on-write result. |
| E10 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/task/store.go::validateTodos` — 1–500 Unicode code points and known tri-state status; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/Todo.yaml::properties`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/Task.yaml::todos / session_id` — task execution session, not scratchpad-origin identity. |
| E11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/toolVisibility.ts::shouldRenderToolCall` — ToolSearch/Skill are hidden on success but visible on error; delegate and background bash have different rules; set_goal's raw badge is distinct from its dedicated result presentation. |
| E12 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tsx::AssistantMessage / VirtualAssistantMessageRow`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/messageParts.ts::splitMessageParts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/omnipus-runtime.ts::pushHistoryParts / buildContentParts` — separate live/history render paths and offset/snapshot inputs; guards depend on visible content. |
| E13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityBar.tsx::ActivityBar` — returns null without Agents/Commands mount conditions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityPanel.tsx::ActivityPanel / ActivityRow` — existing Sheet, section scrolling, child status and Open control. |
| E14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/design-system/catalog.json::entries` — DisclosureRow, Accordion, Button, Sheet, EmptyState, QueryErrorState and Skeleton exist; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/ui/disclosure-row.tsx::DisclosureRow` — keyboard-capable Button and expanded state, not a complete feature focus/announcement policy. |
| E15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md::D7`, **A sub-agent is a session steered by another session** — child details stay in the child's own session; parent status/open surface remains. |
| E16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-049-planning-goals-system-agents.md::Functional FR-4`, **Planning & Goals — Plan entity, evidence-ladder judge, goal loops, System Agents** (partially superseded; only scratchpad exemption used here); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-051-tasks-screen-plans-as-filter.md::D1–D4`, **Tasks Screen: Plans-as-Filter over a Combined Task Board** — executable tasks/plans are not checklist items. |
| E17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/agent/loop_wire.go::NewSetTodosTool registration through RegisterReplacing`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/config/defaults.go::set_todos ceiling entry` — registered existing tool and shipped allow ceiling, not proof that every configured agent may execute it. |
| E18 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/slices/frames.ts::applyToolCallResultFrame` — result updates live calls or calls already baked into messages; display derivation must handle both. |
| E19 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/tools.md::What it is / Allow, ask and deny`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/security.md::Approve a request` — tool-inspection and approval promises affected by changing presentation, not permission semantics. |
| E20 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/.claude/skills/grill-spec/SKILL.md::Phase 1 / Phase 2 / Output`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/.claude/skills/grill-spec/report-template.md`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/.claude/skills/grill-spec/review-constitution.md` — current twelve-lens structure, severity rules and fixed interview/correction sequence. |

## Findings

### CRITICAL findings

None established. The draft explicitly prohibits implementation. Runtime security certification, feature correctness and delivery are not claimed.

### MAJOR findings

#### MAJ-001 — The all-hidden-run acceptance oracle contradicts the requirement it is meant to test

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified, high confidence** — conflicting text, not a runtime hypothesis. |
| Lens | Ambiguity; inconsistency; testability and false-green risk. |
| Affected section | FR-001; SC-001; D1 rows 7 and 11; Q2. |
| Failure scenario | The founder accepts Q2-A, suppressing an all-hidden successful run. FR-001 and D1 permit that choice, but SC-001 still requires exactly one Step for **100% of D1 runs**. Conversely, an implementation mounting an empty wrapper can satisfy SC-001 while defeating the hidden-success decision. A test author must silently choose which statement to disregard. |
| Evidence | E01::SC-001 says “exactly one Step per contiguous run”; E01::FR-001 and D1 row 11 explicitly leave this conditional. E11 confirms the all-hidden-success input is real, not hypothetical: successful ToolSearch/Skill calls have no raw detail in non-verbose chat. |
| Recommendation | After the interview, give each visibility fixture an exact wrapper count, summary count basis, failure inclusion and expansion expectation. Make SC-001 refer to that decision table instead of an unconditional one-Step rule. Do not manufacture a passing oracle while Q2 remains unresolved. |

Q1–Q6 remain separate product decisions in the founder section; this finding is the concrete contradictory test rule, not a second listing of those questions.

#### MAJ-002 — Active-checklist lifetime is incorrectly left dependent on transcript-view retention

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified mechanism, high confidence.** Feature disappearance is **Inferred, high confidence** if the proposed projection scans only retained messages. |
| Lens | Incompleteness; reachability; UI states and journey gaps. |
| Affected section | FR-009/014; “No recorded checklist or insufficient history”; Q4; tests 04/09/12/13. |
| Failure scenario | A successful checklist is early in a long session. No replacement or clear follows, but later messages push its source outside the 500-message buffer. Both the call-bearing message and dependent call entries can disappear from the client state. A message-derived current list then vanishes; Q4-A also anchors the active chat block to a message that is no longer rendered. A cleared-state record can be evicted too. Reloading cannot be assumed to repair this because replay also enforces the cap. |
| Evidence | E06::applyMessageArray removes the earliest messages and their calls; E06::handleReplayMessageFrame evicts during replay. E01 promises the latest successful list without an exception for a merely trimmed view. Its “insufficient history” row does not distinguish server evidence missing from client view eviction. |
| Recommendation | Specify the lifetime of current checklist state independently of the rendered transcript window, with reconstruction through existing history/replay and session-reset behavior. State where the active block remains accessible if its source row is not mounted. Do not solve it by raising/removing the message cap or adding unapproved persistent storage. If the available history genuinely cannot establish the answer, expose unavailable/last-known state rather than claiming a clear. Add 499/500/501-message boundaries, a later clear crossing eviction, reopen, both restore paths and session switching. Product choice: Q7. |

#### MAJ-003 — “Supported outcomes” are not defined against the actual transport domains

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified, high confidence** from contract and adapter branches. No runtime test claimed. |
| Lens | Infeasibility; contract-first gaps; inconsistency. |
| Affected section | FR-003/004/008/014; D1 row 10; S-15; Q3/Q6. |
| Failure scenario | A persisted interrupted/parked call is read through REST and becomes running because the adapter's default branch handles it. A replay result for that status is error. Denied/cancelled calls also differ between adapters. Thus a collapsed Step can show a continuing spinner or a different failure count for the same record depending on restore path. A test that seeds an internal “interrupted” state into every path would skip the actual loss of information. |
| Evidence | E07::rawToToolCall; E08::ToolCall.status / ToolCallResultFrame.status / toolCallResultStatus. The result-frame contract carries only success/error, while the persisted contract also permits pending/denied/running/cancelled/interrupted/parked. E18 applies the result-frame status to live or baked calls. |
| Recommendation | Add a table from actual persisted/live/replay inputs to Step display state and count classification, including a completed turn with unresolved call records. Explicitly include necessary frontend adapter work in scope. An unchanged-wire solution must use a truthful common outcome vocabulary or visible uncertainty; it cannot recover precise distinctions that a carrier discarded. If precise denial/cancellation/parking distinctions are required everywhere, reopen the contract/ADR boundary before implementation. Q8 refines Q3/Q6. |

This is also a discovered existing adapter discrepancy to route to team-lead/backend and frontend owners; it was not fixed on the side. Do not replace existing tool-detail status semantics globally merely to simplify the new Step header without specifying that impact.

#### MAJ-004 — Failure can remain hidden while the rest of the Step is still working

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified specification gap, high confidence.** |
| Lens | Inconsistency; UI states; testability. |
| Affected section | US-1; AS-1.3; S-03; FR-005; tests 03/07. |
| Failure scenario | X fails, Y remains running, and the Step stays collapsed. AS-1.3 and S-03 assert a failed count only **when the Step settles**. A header showing only Y's current action can therefore hide X's failure for the rest of a long-running operation while passing the stated scenario. Later successful calls must not erase the earlier failure either. |
| Evidence | E01::AS-1.3 / S-03 versus FR-005 and US-1's “see failure promptly”; E02::Failures says “Never hidden.” E18 shows that a failed result can update a call before the containing message settles. |
| Recommendation | Require the collapsed failure signal on receipt of a relevant failed result, simultaneously with any running status. Add a failed-plus-running fixture, then a later success, with assertions **before** settlement. Hidden-call inclusion still follows the founder's Q2 answer; immediate visibility of an included failure is already required by the source issue. |

#### MAJ-005 — A successful tool record is not a validated checklist-shaped payload

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified boundary gap, high confidence; Inferred UI failure, high confidence.** This is not a demonstrated exploit or a claim that normal validated calls produce every malformed fixture. |
| Lens | Security; contract-first gaps; incompleteness; UI error states. |
| Affected section | FR-009/010/014; Machine-checkable invariants; Conservative type design; D2; tests 04/09/13. |
| Failure scenario | A restored success record has absent/non-array todos, a non-object item, invalid status or unreadable outcome. The generic ToolCall contract can accept the outer record without validating the nested checklist. A new renderer that maps or trusts those fields can crash, falsely interpret unreadable data as `[]`, or keep an older list silently labeled current. Escaping text prevents markup execution, not invalid object access or false freshness. |
| Evidence | E08::ToolCall.parameters is an open object; E09::SetTodosTool.Execute / parseTodosArg supplies tool-specific parsing and normalization, not a typed checklist in the outer transport. The parser defaults an empty/non-string status to pending; Execute's unchecked todos-array assertion can hand nil to the parser if called with a non-array. E01 specifies only omitted-status normalization, escaping and successful-record selection. |
| Recommendation | Define checked decoding and the accepted normalization rules before deriving the internal render model. Match established backend semantics where the record is interpretable; do not invent a stricter backend input rule in frontend code. Distinguish valid empty-list clear, no record, incomplete reconstruction and an unreadable latest accepted record. State whether older data is hidden or explicitly last-known (Q9); never silently call it current. Add missing/non-array todos, mixed malformed items, invalid status/outcome, omitted and empty status, valid clear, failed write and subsequent valid recovery fixtures. Diagnostics must not log full checklist text. |

The permissive parser branches are a source-verified observation for the owning lead, not authorization for a backend hardening change in this render-only feature.

#### MAJ-006 — Loading, partial restore and recovery have no user-facing state contract

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified missing specification, high confidence.** |
| Lens | UI states and journey gaps; reachability; incompleteness. |
| Affected section | Prerequisites/runtime/integration table; Reachability checklist; US-2; FR-014/015; tests 09/12–15. |
| Failure scenario | The user opens a completed session and only an early checklist has replayed. The panel can show that list as current, show “no checklist” before history arrives, or remain blank after replay and REST fail. The spec has no rule separating those states or describing a recovery action. A subsequent session switch or reconnect can leave the user unable to tell whether a checklist is absent, cleared, stale or still loading. Component inventory and “do not fake it” do not specify what the user actually sees. |
| Evidence | E01's integration rows name REST/WS failure possibilities but not a loading/empty/error/partial state for each changed surface. There is no end-to-end journey covering initial open → restore → degraded state → recovery → switch/close. E13 provides a panel whose existing empty test is only about activity rows, not checklist reconstruction. |
| Recommendation | Add a per-surface state matrix for Step header/details, active chat checklist and checklist panel. Name labels/actions for loading, never-recorded, valid clear, updating, partial replay, unavailable history and recovered state. Specify when a reconstructed list may be called current and how session switching clears or replaces it. Describe the complete journey including narrow/touch layouts and the route back to chat; reuse current history recovery mechanisms rather than inventing a second retry service. Add reducer-plus-render tests for replay interrupted mid-history, REST failure, recovery and switch during loading. |

#### MAJ-007 — Keyboard toggling alone does not cover focus or live-status accessibility

| Field | Finding |
|---|---|
| Severity / certainty | **MAJOR; Verified specification gap, high confidence.** The concrete accessibility failure is **Inferred** until implemented. |
| Lens | Accessibility and keyboard; UI journey gaps. |
| Affected section | Machine-checkable invariants; FR-015; Q4/Q5; test 10. |
| Failure scenario | A keyboard user opens the checklist panel, then the checklist is replaced/cleared or the panel closes. The spec does not say where focus goes or how it returns if the originating control disappears. A screen-reader user can toggle a Step but receive no announcement when a failure appears or checklist progress changes, despite this feature's main purpose being live visibility. Announcing every streaming update instead would create unusable speech noise. |
| Evidence | E01 requires keyboard operability and expanded state only; its test 10 does not define focus restoration, accessible state labels or live announcements. E14::DisclosureRow implements a button and expansion state but cannot decide a feature's replacement/clear focus policy or summary announcement policy. E13::ActivityPanel has separate controls and changing sections. |
| Recommendation | Add an Accessibility and keyboard section covering disclosure names/state relationships, initial and return focus for the panel, Escape/close behavior through the existing panel, focus preservation across updates/remounts, and non-color tri-state labels. Define concise meaningful progress/failure announcements with no per-token narration and no forced focus stealing. Test keyboard-only open/close/clear, session switch, rerender while expanded and status announcements; carry this through the real narrow layout. These are implementation obligations, not permission to invent new brand styling. |

### MINOR findings

#### MIN-001 — “Compact” has no observable long-list behavior

| Field | Finding |
|---|---|
| Severity / certainty / lens | **MINOR; Verified, high confidence; ambiguity / testability.** |
| Affected section | US-2; FR-009; D2; test 09. |
| Failure scenario | An always-expanded 100-item checklist can pass all short-list fixtures while occupying the whole conversation. The current “long” fixture is one long item, not a long list; #1072 explicitly calls for compact/collapsible presentation. |
| Evidence | E01::D2 and Machine-checkable invariants; E03::Not noisy. |
| Recommendation | Define default disclosure behavior, which summary remains visible, and how every item is accessible. Add a genuinely long list, narrow-panel fixture and keyboard expansion assertion. Do not introduce a backend data cap or silently truncate items. Q10 asks only for the product disclosure default. |

#### MIN-002 — The item-length note misstates an enforced backend limit

| Field | Finding |
|---|---|
| Severity / certainty / lens | **MINOR; Verified, high confidence; incorrectness / testability.** |
| Affected section | D2 row 7. |
| Failure scenario | QA treats “500-character” as informal guidance and never checks the actual boundary. A 501-code-point attempted write is rejected, whereas a multibyte string of 500 code points is valid; a JavaScript code-unit check could classify it differently. |
| Evidence | E01 calls length “tool-description guidance”; E10::validateTodos counts `len([]rune(td.Text))`, rejects fewer than 1 or greater than 500, and validates status. |
| Recommendation | Correct the factual note. Include 0/1/500/501-code-point cases, including a multibyte example, and assert that a rejected update does not replace the displayed accepted list. Reuse existing backend validation evidence; this is not a request to add a second UI write validator or run unrelated Go suites. |

#### MIN-003 — Traceability counts IDs, but not all promised assertions

| Field | Finding |
|---|---|
| Severity / certainty / lens | **MINOR; Verified, high confidence; testability / inconsistency.** |
| Affected section | Full requirement-to-test traceability; FR-004/015; S-02/03/10/13. |
| Failure scenario | FR-004 is marked covered by S-02/S-03, but those scenarios assert a changing live action and settled failure, not the final count/duration. FR-015 maps to S-10/S-13 without a keyboard/state-exposure assertion. A traceability completeness check can be green when those promises have no BDD oracle. |
| Evidence | E01::FR-004 / FR-015 / associated scenarios and trace rows. Structural inventory finds 15 FRs, 17 acceptance scenarios, 17 BDD scenarios and **16 planned tests**; these counts alone prove no behavior. |
| Recommendation | After Q2/Q3 and accessibility correction, add exact final-summary and disclosure/focus assertions to the scenarios and update the matrix. Report ID coverage separately from assertion coverage. Retain independent holdout scenarios rather than turning them into development fixtures. |

#### MIN-004 — Security/user promises and their documentation impact are not assessed explicitly

| Field | Finding |
|---|---|
| Severity / certainty / lens | **MINOR; Verified omission, high confidence; security/user promises / structural integrity.** |
| Affected section | Behavioral safeguards; Sources; Reachability checklist. |
| Failure scenario | The UI ships a scoped “last recorded agent checklist” and grouped calls, but the user guidance continues to imply a different inspection/current-state model. The author also has no explicit checklist confirming that regrouping leaves approval controls discoverable. Existing “no tool policy change” language is necessary but does not cover these presentation promises. |
| Evidence | E01 has safeguards but no security/user-promises assessment; E19::What it is promises collapsible tool inspection and ::Approve a request describes the approval controls. E02/E03 change how those tool records and checklist state are presented. |
| Recommendation | Add a short explicit section: existing authentication, policy and approvals remain unchanged; tool details and approval entry points remain accessible; render hiding is not authorization; checklist scope is labeled accurately; diagnostic logging excludes raw contents. Name the affected user-doc update/audit, or record a verified no-change assessment. Do not expand this into new authentication or tool-policy work. |

### Observations — non-gating

#### OBS-001 — Keep wave-3 integration evidence, but assign one owner

**Lens:** Overcomplexity. **Affected:** FR-013, SC-005, tests 11/16. **Certainty:** Verified overlap in written requirements; duplicate implementation/test cost is Inferred, medium confidence.

E04/E05 verify that task pills/links and the shared task-running indicator belong to neighboring work. E05 still marks its own wave-3 test numbering TBD. Keep the combined integration check, but name the owning suite, required integrated branch and handoff condition. Reuse its Board/List/Graph/Plans evidence instead of implementing another indicator or a second exhaustive standalone matrix here. A written neighboring requirement is not a claim that wave 3 has landed or passed.

#### OBS-002 — The catalog supports this feature without a new generic disclosure primitive

**Lens:** Design-system reuse and brand. **Affected:** Existing codebase context; frontend implementation plan. **Certainty:** Verified catalog entries, high confidence.

E14 contains DisclosureRow/Accordion/Button/Sheet and named loading/empty/error parts. The draft correctly calls for reuse and does not mandate off-brand controls. When the state matrix is added, map recurring states to those catalogued parts and retain token/brand rules. A domain Step/checklist component is not automatically a new public primitive; require the four-part publication contract only if a new reusable component is actually published. No new visual-design decision is made in this review.

## Structural integrity

| Check | Result | Notes |
|---|---|---|
| Status field | PASS | Draft, explicitly not implementation-authorized. |
| ADR linkage / decision gate | CONDITIONAL | Relevant titled ADRs are cited; the draft explains that a newly opened architectural decision triggers an ADR. The founder interview must resolve the authority/contract choice before finalization. No ADR was invented for this review. |
| Contract boundary stated before implementation | PASS for declared frontend-only boundary; feasibility unresolved | Existing schemas identified; unchanged-wire promise explicit. MAJ-003/005 and Q1/Q8 must determine whether it is actually sufficient. |
| API/data description | PARTIAL | Input seams and non-behaviors exist, but complete outcome/decoding/retention rules do not; MAJ-002/003/005. |
| UI loading/empty/error/partial states | FAIL | Not specified for all changed surfaces; MAJ-006. |
| End-to-end user journey | FAIL | Entry inventory is present; restore/recover/switch/close and narrow behavior are not complete; MAJ-006. |
| Accessibility and keyboard section | FAIL | Toggle/expanded-state invariant is not the full accessibility contract; MAJ-007. |
| Catalog-first design-system reuse | PASS with non-gating follow-up | Existing disclosure correctly identified; state components are available, OBS-002. |
| Security/user promises assessment | FAIL | Safeguards are present, explicit assessment/doc impact missing; MIN-004. |
| BDD scenarios and spec-owned oracles | PARTIAL | 17 scenarios; Q-dependent oracles unresolved, MAJ-001/004 and MIN-003. |
| Requirement → scenario → test matrix | PASS for ID inventory; PARTIAL behavior coverage | 15 requirements and 17 scenarios occur in traceability; tests are planned, not executed. MIN-003. |
| Reachability section | PASS as plan | Real chat, existing tool/policy, verbose setting and checklist-only panel entry are named. No delivery claim follows from this. |

## Twelve-lens coverage

A lens is not forced to invent a defect. Cross-cutting findings are reused where they cover multiple lenses.

| Lens | Disposition |
|---|---|
| 1. Ambiguity | MAJ-001; MIN-001; open Q1–Q6 carried to interview. |
| 2. Incompleteness | MAJ-002/005/006. |
| 3. Inconsistency / ADR / as-is | MAJ-001/003/004; MIN-002/003. Child-session and scratchpad/task distinctions match E15/E16. |
| 4. Infeasibility | MAJ-003; exact wall-clock time remains an unapproved scope change, Q3. No new runtime requirement proposed. |
| 5. Contract-first | MAJ-003/005; no invented wire type, existing contract domains verified. |
| 6. Security / user promises | MAJ-005; MIN-004; threat summary below. |
| 7. Reachability | MAJ-002/006; Q5 already recognizes the checklist-only mount gap. |
| 8. UI states / journeys | MAJ-004/006; MIN-001. |
| 9. Accessibility / keyboard | MAJ-007; MIN-003. |
| 10. Design system / brand | OBS-002; no specific brand violation established. |
| 11. Testability / false greens | MAJ-001/003/004; MIN-002/003. |
| 12. Overcomplexity | OBS-001; do not create new persistence, retry or indicator subsystems without necessity. |

## Test coverage assessment

| Missing or incomplete category | Required addition |
|---|---|
| Message retention | Checklist and subsequent clear crossing the 500-message cap; source unmounted; reopen and both reconstruction routes. |
| Actual transport domains | Feed valid persisted records and valid result frames through the real adapters. Do not put unsupported statuses on a success/error-only frame. |
| Failure during continuing work | Failed call plus still-running call; later success cannot erase the collapsed signal. |
| Invalid/normalized checklist data | Missing/non-array todos, malformed/mixed items, invalid outcome/status, omitted/empty status, valid clear and recovery. No crash, false clear or unsupported freshness claim. |
| Restore state transitions | Loading, partial replay, replay failure, REST failure, recovered state and session switching while loading. |
| Interaction/accessibility | Keyboard open/close, initial/return focus, source control disappearing, live announcements, expanded-state retention, narrow/touch access. |
| Long lists and text boundaries | A many-item list; 0/1/500/501 code points; all items remain obtainable; literal markup-like text stays inert. |
| Final summary | Count basis, missing/partial duration labels, lifecycle classification and first-sentence examples after interview. |
| Wave-3 integration | Retain the integrated check, using the neighboring suite's evidence and one shared indicator. |

Already planned and worth retaining: duplicate replay IDs, late results after a call is baked into a message, session/parent-child isolation, registered and fallback tool UIs, real workspace route, verbose toggle, forced REST fallback, CI group coverage and negative controls. These are **planned tests**, not executed verification.

## STRIDE threat summary

STRIDE means spoofing, tampering, repudiation, information disclosure, denial of service and elevation of privilege. “No new boundary” below is a scope assessment, not a security audit pass.

| Component / flow | Assessment |
|---|---|
| Tool/checklist decoding | Tampering/integrity and availability: generic nested input can be misread or crash new presentation; MAJ-005. Escaping is necessary but insufficient. |
| Session-local projection | Information disclosure/integrity: retaining a checklist outside message rows must still reset and scope by displayed session. Existing isolation requirements are correct; extend tests to retained state and switching, MAJ-002/006. |
| Step header | Integrity/user trust: failed work can be hidden by running status or reclassified by restore transport; MAJ-003/004. Detail hiding is not access control. |
| Render-only layer | No new write/authentication authority proposed. Do not add checklist editing, task execution, storage, policy grants or hidden backend polling under this scope. |
| Approval and detail controls | Preserve existing tool permissions and approval reachability; explicitly assess the user promise, MIN-004. |
| Diagnostics | Do not log raw checklist/outcome text to explain decoding failures. Use existing diagnostics with non-content metadata. |
| Repudiation/audit | No new authoritative write or audit stream proposed under Q1-A. A displayed agent-history projection must not masquerade as independent task-board state. |

## Reachability check

| Question | Answer | Evidence |
|---|---|---|
| Existing agent tool registered and policy-backed? | Yes, source verified; actual execution not tested. No new tool proposed. | E17; global ceiling allow plus per-agent tightening, not mandatory copied per-agent grants. |
| Named user-facing renderers? | Yes, live and historical chat, ActivityBar and ActivityPanel. | E01/E12/E13. |
| Checklist-only entry specified sufficiently to implement? | No — requirement present, exact control awaits Q5. | E01::FR-011; E13's mount conditions. |
| State remains accessible after source eviction/loading failure? | Not specified adequately. | MAJ-002/006. |
| Test plan demands execution and route proof? | Yes. Not performed by this review. | E01::Reachability checklist / tests 12–16 / RED-first method. |
| Child-session detail ownership retained? | Yes in the proposed requirements, not runtime certified. | E01::FR-012 and E15::D7. |

## UX assessment

**Verdict: Several issues to address.** This is a written-flow assessment, not a screenshot or visual acceptance check.

| Heuristic | Consequence |
|---|---|
| Visibility of system status | A current/last-known/loading distinction and immediate failures are missing; MAJ-003/004/006. |
| Consistency | Two restore paths must not give different outcomes; MAJ-003. Reuse catalog parts and the single neighboring task indicator. |
| Recognition rather than recall | A checklist whose source row is gone still needs a reachable current-state entry; MAJ-002. |
| Minimalist display | “Compact” must be falsifiable on a long list, MIN-001. |
| Error recognition and recovery | No silent clear or stale-current claim; no blank unresolved restore; MAJ-005/006. |

**Priority actions:** (1) settle data authority and retained/reconstructed state; (2) define truthful outcome/failure and recovery states; (3) complete panel/disclosure journeys including long lists, focus and announcements.

## Unasked questions for the spec author

These are technical correction obligations, not extra founder interviews unless implementation exposes a genuine new product trade-off.

| ID | Required answer in the correction |
|---|---|
| A1 | What exact transport-to-Step table applies, including terminal turns with incomplete calls? Which frontend adapter changes are in scope? |
| A2 | Which existing history/replay path rebuilds retained checklist state before view eviction, and how are unknown history and an explicit clear distinguished? |
| A3 | What call-order/dedup key defines the latest accepted update across live, baked and replay forms? Show that delayed/repeated delivery cannot resurrect a superseded list, as D2 already requires. |
| A4 | Which per-surface loading/error/partial states, recovery actions and accessible focus/announcement transitions are required? |
| A5 | Which existing wave-3 test suite and integrated ref own the running-indicator check, and how will this feature consume its evidence? |
| A6 | Which catalogued state components and relevant design-system/CI checks cover the final frontend surface? Preserve the complete publication contract if a reusable component is added. |

## Questions for the founder

**Interview context:** This is round 1 of the combined #991/#1072 spec review. The spec is still Draft and cannot be implemented. Q1–Q6 below carry forward its existing decisions, with the code constraints made explicit; Q7–Q10 add choices exposed by the grill. Recommendations are proposals, not approvals. Team-lead should obtain these answers before any correction. Answer by ID, for example `Q1 A, Q2 A, Q3 A`; record any exception in words.

### Q1 — Which state does “active checklist” promise to match? (A / B)

A recorded successful agent call tells us what the agent last submitted. It cannot independently prove that a person has not since changed the task-board checklist. The current task response does not provide the scratchpad-origin identity needed for a safe join; matching titles or agents can pick the wrong task/session.

| Option | Decision |
|---|---|
| **A — Recommended for unchanged-wire v1** | Match the last recorded successful agent update only. Label that scope honestly. Track independent task-board synchronization separately. |
| B | Also reflect independent task-board edits. Reopen the architecture/contract scope before approving the spec; do not implement an unsafe title/agent join. |

### Q2 — How should hidden calls affect a Step? (A / B / C)

Some successful calls are deliberately absent from normal chat; some hidden call classes also have a separate failure/status surface. A Step must not accidentally reveal raw hidden details or bury dedicated goal/delegation controls. This choice sets the wrapper, summary and failure rules together.

| Option | Decision |
|---|---|
| **A — Recommended** | Suppress an all-hidden successful run. For other runs, count all invocations and report every recorded failed call in the header as soon as known, including otherwise-hidden calls, without exposing hidden arguments. Keep existing dedicated goal/delegation surfaces separate and do not duplicate them in the disclosure. Expandable details still obey the current visibility policy. A hidden-only failed run gets a header even if it has no permitted details. |
| B | Count/report only calls represented by visible details or dedicated visible presentations. Explicitly accept this narrower exception to #991's “failures never hidden” wording. Preserve separate dedicated surfaces. |
| C | Use A's counting and dedicated-surface rules, but automatically expand permitted details on failure. Never reveal details that the existing visibility policy hides. |

### Q3 — What should the duration mean? (A / B / C)

The existing record gives each call's duration, not a measured Step start/end interval. Parallel durations added together are not elapsed wall-clock time. Some call durations are missing entirely.

| Option | Decision |
|---|---|
| **A — Recommended** | Show a sum labeled as recorded tool time, never Step elapsed time. If some durations are missing, label the sum partial; if none are known, show duration unavailable. A measured zero remains zero, not unavailable. |
| B | Omit duration when it cannot be exact, explicitly relaxing #991's final-duration requirement. |
| C | Require true Step elapsed time. Reopen timing-source and potentially contract scope before implementation. |

Count inclusion follows Q2. Exact terminal-state vocabulary follows Q8, rather than pretending the existing carriers preserve all states.

### Q4 — Where should the current checklist live in chat? (A / B)

A newer accepted call replaces the active list. Updating every historical checklist block would create several apparently active lists. Anchoring the only current view to an old transcript row also fails when that row leaves the view buffer.

| Option | Decision |
|---|---|
| A | Anchor one current block to the latest accepted call; older blocks are explicitly historical or available only as raw verbose detail. Require a separate visible locator/current-state affordance when its anchor is outside the rendered transcript. A successful empty list shows a labeled cleared state. |
| **B — Recommended after the retention finding** | Use one persistent current-checklist block in the chat shell, separate from transcript rows. Historical tool details stay historical. An empty success shows a labeled cleared state. |

For either option, the recommendation is to keep the independent current checklist available in verbose chat too; verbose affects tool detail, not the availability of the checklist. If that is not intended, state the exception with the answer.

### Q5 — How does checklist-only work open the side panel? (A / B)

The existing activity entry disappears when there are no Agents or Commands to show. Adding only a panel section would strand the checklist; counting its items as agents or tasks would lie about activity.

| Option | Decision |
|---|---|
| **A — Recommended** | Add a distinct checklist entry that opens the existing activity panel at a separate checklist section. It remains available for a retained checklist/cleared or degraded checklist state without changing Agents/Commands counts. |
| B | Use another persistent entry in the chat shell. The spec must name it and its target before implementation; preserve the existing counts. |

### Q6 — What counts as the first sentence of intent? (A / B)

Punctuation, abbreviations, Markdown and line breaks can produce different objective text. The fallback must not invent intent from tool arguments, and a legacy call whose placement is unknown must not claim text was definitely spoken before it.

| Option | Decision |
|---|---|
| **A — Recommended** | Use the first readable sentence from the immediately preceding, recorded text segment, with conservative handling of abbreviations and Unicode sentence endings. Treat a nonempty segment without a sentence terminator as the sentence. Use “Working…” only when no usable preceding text is established. The corrected spec must give exact examples. |
| B | Use a strict first punctuation terminator, accepting that abbreviations may yield short fragments. The corrected spec must still define Markdown, newline and no-known-preceding-text cases. |

Tool names without a human-readable action label use a generic working label while genuinely active; they must not gain a success label just because their name is unknown. Terminal outcome precision is Q8.

### Q7 — Must the current checklist survive a trimmed conversation view? (A / B)

The UI retains 500 messages. An old but still-current checklist can leave that window without being replaced or cleared. Replay enforces a cap too, so “just reload” is not a sufficient behavior contract.

| Option | Decision |
|---|---|
| **A — Recommended** | Preserve/rebuild current checklist state independently of rendered-message retention using existing history/replay. Keep it accessible when the original row is unmounted. When the source evidence genuinely cannot be recovered, show that limitation explicitly. No new server store or raised transcript cap is implied. |
| B | Limit checklist availability to the retained transcript view. Show an explicit unavailable state after evidence leaves it and amend the durability/reload promise accordingly. |

### Q8 — How precise must stopped-call outcomes be across live and reload? (A / B)

WebSocket tool-result frames have only success/error; saved records have more states, and the REST adapter currently maps some stopped states to running. Identical-looking internal test fixtures cannot solve information that a real transport discarded.

| Option | Decision |
|---|---|
| **A — Recommended for unchanged-wire v1** | Accept a common, truthful Step-summary vocabulary that groups recorded non-success outcomes where needed and visibly marks genuinely unknown/incomplete work. Do not promise separate denied/interrupted/parked labels everywhere. Author must define and test the transport table, including any necessary frontend adapter fixes. |
| B | Require those distinctions everywhere. Reopen contract/backend scope and the architectural decision before finalizing the spec. |

This answer refines Q3 and Q6; it does not approve relabeling all existing tool-detail components indiscriminately.

### Q9 — What should appear after an unreadable latest accepted checklist record? (A / B)

An earlier valid list may still be readable, but a later successful record may not decode. Showing the old one as current would claim knowledge we do not have; showing an empty list would falsely claim the agent cleared it.

| Option | Decision |
|---|---|
| **A — Recommended** | Keep the earlier list only as clearly labeled last-known data, with a visible unavailable-current-update notice until a valid update/reconstruction recovers it. |
| B | Hide the old items from the current surface and show an unavailable notice until recovery. |

Neither choice silently discards the problem or interprets unreadable data as an empty successful list.

### Q10 — What is the default compact checklist presentation? (A / B)

“Compact” currently has no measurable rule, so a 100-item always-open block could pass. This is a disclosure choice, not permission to discard items or create another task counter.

| Option | Decision |
|---|---|
| **A — Recommended** | A compact summary with outcome and checklist progress, collapsed by default, with every item available on expansion. Preserve the user's expansion choice during updates within that session. |
| B | Expand short lists by default and collapse long lists. Specify the exact item-count threshold in the answer so acceptance tests do not invent it. |

**End of founder interview section.**

## Verdict rationale and next action

**REVISE**, not BLOCK: no critical defect was established, but the major specification gaps can ship incorrect or inaccessible behavior. The draft's explicit implementation prohibition remains appropriate. Retention (MAJ-002), actual outcome domains (MAJ-003), failure visibility (MAJ-004) and recovery/decoding (MAJ-005/006) must not be dismissed as styling details. The findings do not authorize backend changes under a frontend-only choice.

This is grill round **1 of 2 (fixed)**. Next: team-lead interviews the founder using the section above, then relays the answers to the spec author. Only then does the author perform the round-1 correction to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/specs/chat-steps-and-agent-checklist-spec.md`. The separately dispatched second spec-mode grill runs on that corrected document regardless of this verdict. **No correction or second grill is part of this dispatch.**

**Code correct and tested:** Not established; specification review only, no feature tests executed.

**Reachable by a user/agent:** Not established; entry paths and the existing tool wiring were read, not exercised as a delivered feature.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Correct checkout, branch and review base | `pwd; git branch --show-current; git rev-parse HEAD` — exit 0; path `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072`, branch `spec/chat-steps-todo-991-1072`, SHA `95714e0d992aef5808502415c1e0b2ad7c784d69`. | Verified, high confidence |
| One round, correct report naming | File-existence inventory found round-1 and round-2 review absent before writing; E20::Mode and round detection. Sibling read: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/specs/channel-agent-ownership-spec-review.md::header`. One `grill-spec` invocation; no fix invocation. | Verified, high confidence |
| Source issues checked rather than assumed | `gh issue view 991/1072/1021/1049 --repo elicify-ai/omnipus --json number,title,body,state,comments` executed separately per ID — each exit 0; titles and complete bodies in E02–E04, each OPEN with no comments. | Verified, high confidence |
| Written wave-3 scope verified without accessing another worktree | `git show feat/resizable-side-panels:docs/internal/specs/side-panel-shell-spec.md` — exit 0; FR-022 requires one catalogued indicator across Board/List/Graph/Plans. `git rev-parse feat/resizable-side-panels` — exit 0, `2448d93fc732f73b5974bfabed4e5c47d870f011`. E05. | Verified requirement; implementation Unknown |
| All seven major findings grounded | E01/E06–E14/E18, with failure scenarios and source-vs-runtime certainty specified per finding. No unsupported probe result is used as a receipt. | Verified text/mechanisms; predicted feature failures Inferred |
| Checklist boundaries and normalization checked | E09::Execute / parseTodosArg; E10::validateTodos / Todo properties. | Verified, high confidence |
| Structural and catalog instruments had positive controls | Python read the whole spec, found its known BDD heading, then counted 15 FR / 17 AS / 17 BDD / 16 tests. Catalog scan checked its actual `entries[].exports` shape, positively found Accordion, then DisclosureRow/Button/Sheet/EmptyState/QueryErrorState/Skeleton. No conclusion relied on the earlier name-key scan that returned no catalog rows. | Verified, high confidence |
| Architecture and user-promise grounding checked | E15/E16/E19; relevant sections of `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/AS-IS-architecture.md::Memory & Session / Tools System` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/plugin-extensibility-assessment.md::Tools / MCP` read as background; present-day source wins over stale descriptions. | Verified sources read; no blanket architecture certification |
| Review limits explicit | No feature suite, browser acceptance, CI or local probe result is claimed. GitNexus was not exposed in this session's tools; no graph impact/detect-changes result is claimed. Documentation-only report changes no production symbol. | Verified scope; feature correctness Unknown |
| Self-check | Re-read the assembled report against E20 and the draft. Python artifact check — exit 0: 7 major / 4 minor / 2 observations; Q1–Q10; required sections and skills present; a removed founder heading is detected; 29 cited local files exist; spec byte-identical to the reviewed base; index empty before report-only staging. The first path check exited 1 because its regex shortened .tsx to .ts; the corrected parser passed a .tsx positive control and all path checks. Final commit/push and diff receipts belong to the delivery handoff. | Verified source/report and static artifact checks; runtime acceptance not claimed |

skills: grill-spec, omnipus-shared-rules, gitnexus-exploring, ux-heuristics-review
