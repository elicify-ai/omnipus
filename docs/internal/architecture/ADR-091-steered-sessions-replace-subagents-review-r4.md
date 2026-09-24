VERDICT: BLOCK
CRITICAL: 2  MAJOR: 16  MINOR: 1

Reviewed as one delivery against `364290cb51cef7b8c900ac1c03cef68860799d20`, including the rev-2/rev-3 reports and three prior audits. No files changed. Rev 4 repairs several earlier defects, but the published contract still permits cancellation races, inconsistent audience classification, lost delivery, and independently incompatible implementations.

### [CRITICAL] R01 — Stop can miss new children and cancel revived work
- **Where:** ADR D8; landing order I-6; FR-A-015; FR-D-001/004.
- **Problem:** Locking individual record writes does not serialize the entire Stop operation.
- **Evidence:** I-6 stamps records and **“then cancels live turns.”** A permitted sequence is: stamp B in generation 1; revive and register B in generation 2; perform the delayed cancellation against B. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/cancel.go::RequestCancelForSession` accepts session identity, without an expected generation. Separately, a launch can read an unstopped parent, then publish its child after the cascade stamps and enumerates that parent. Checking an already-stamped parent does not cover this window.
- **Fix:** Carry the target generation or specific turn identity through cancellation. Make the launch’s parent check and child publication participate in the cascade’s synchronization. Add deterministic tests for both interleavings; define when `SkippedNewerGeneration` is populated.

### [CRITICAL] R02 — A present but damaged record can still acquire user audience
- **Where:** I-8 classification; I-5 audience; FR-A-004; FR-B-001.
- **Problem:** Metadata protects the **missing-record** case, but not the **present-record, missing-edge** case.
- **Evidence:** I-8 classifies a present record with nil `SteeredBy` and task origin as `ordinary_root`, without requiring `ParentSessionID` to be empty. A task-origin child whose edge disappears can therefore satisfy the root rule despite metadata identifying it as a child. I-5 grants that class `AudienceUser`. The record-level `Origin` used by this rule is itself undeclared in I-1.
- **Fix:** Define the record discriminator and require record/metadata consistency for every classification branch. A child marker with a missing edge must refuse execution and publication. Test deletion of the edge alone, deletion of the whole record, missing metadata, and a genuine root.

### [MAJOR] R03 — Audience resolution still cannot be consumed across package boundaries
- **Where:** I-5; CP-0; FR-B-001; boundaries 7–8.
- **Problem:** The upward-delivery import cycle was fixed, but the audience function recreates it.
- **Evidence:** `PublicationAudience` remains defined in the agent package, while every boundary must call it—including the tools and channels packages. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop.go` already imports those packages. No injected audience resolver or classification transport is published.
- **Fix:** Publish a consumer-owned resolver with concrete inputs, errors and missing-record semantics. Assign its wiring owner. CP-0 must compile the actual message-tool and external-stream consumers.

### [MAJOR] R04 — Named hooks still conceal required interfaces and ownership
- **Where:** I-1/I-3/I-6/I-7/I-9; landing order §3; CP-0; WP-G.
- **Problem:** Independent agents still need unpublished dependencies and ownership decisions.
- **Evidence:**
  - `ReserveDispatch(sessionID, gen)` must call `RegisterTurnIfAbsent(sessionKey, ts)`, but the contract supplies neither `ts` nor responsibility for constructing it.
  - I-7 receives only a launcher exposing `Launch` and `Dispatch`, yet promises Stop, revival, queued wakes, crash and boot operations.
  - A recorder wrapping downstream sinks cannot distinguish “boundary exercised and blocked” from “boundary never exercised.” No observation hook before the audience decision is published.
  - I-9 changes unexported `ensureWarm`, without defining how the agent-package consumer receives its report.
  - A owns the `subturn*.go` wildcard while B explicitly owns the matching `subturn_result.go`; the exclusion is unstated.
  - Ordinary-root launch requests lack explicit workspace/ownership inputs or a published task lookup contract.
- **Fix:** Publish the missing typed operations, injection points, lifecycle-event hooks and wiring owners. Make ownership sets explicitly disjoint. Compile consumers exercising every fixture hook and boundary control, rather than merely compiling permissive stubs.

### [MAJOR] R05 — Terminal follow-up contradicts the generation and persistence rules
- **Where:** ADR D2/D5; I-1/I-2/I-6; WP-C follow-up edge case; WP-D US-1.
- **Problem:** Generation may advance only when stopped work is revived, but follow-up remains supported for completed work. Stop must also modify already-completed descendants.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go::persistLocked` rejects same-generation writes after a terminal record. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate_followup.go::spawnCorrectiveFollowUp` currently increments generation. I-2 additionally makes terminal dispatch a no-op.
- **Fix:** Define terminal follow-up and administrative Stop updates together with the existing terminal-immutability rule. Assign the lifecycle changes to A. Test completed, failed and timed-out follow-up, external follow-up, and Stop over completed descendants.

### [MAJOR] R06 — The returned queue state can be wrong
- **Where:** I-2/I-3; FR-A-011; FR-C-012.
- **Problem:** `Launch` returns state and queue position before `Dispatch` decides admission. `Dispatch` returns only an error.
- **Evidence:** Two launches can both observe one free slot and return `running`. After dispatch, one must queue, but C has no updated state or queue position to return. Dispatch explicitly returns nil when queued.
- **Fix:** Return the authoritative admission result from Dispatch, or define an atomic capacity reservation during Launch. Test concurrent launches and capacity changes between the two calls.

### [MAJOR] R07 — Delayed tasks cannot recover their required originating call ID
- **Where:** I-1/I-2/I-4; FR-A-010; WP-C task creation; FR-B-006.
- **Problem:** Task creator-session provenance is now persisted, but the call ID required for status correlation is not.
- **Evidence:** I-4 requires the original **“`create_task` call id for task-origin children.”** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/task/task.go::Task` has `OriginSessionID`, but no originating tool-call ID. The assigned `buildTask` change stores only the session ID; the task-model extension has no owner.
- **Fix:** Persist the originating call ID, assign its model owner, and test task creation followed by restart before scheduled launch, then status replay and opening the child.

### [MAJOR] R08 — Existing message schemas cannot carry the prescribed Judge verdict
- **Where:** I-5; WP-B US-2/AS-12; FR-C-009; WP-E contract manifest.
- **Problem:** Reusing an existing kind name does not make its payload suitable for a Judge verdict.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/contracts/components/schemas/SessionMessageGoalStatus.yaml` permits only direction `session_to_ui`, conditions `met|waiting_on_user`, and no verdict/evidence fields. The specification requires a parent-directed verdict with evidence. Additionally, `SubagentMessageFrame.yaml` explicitly excludes `goal_status`, while I-4 requires a frame for every child message and E marks that schema unchanged.
- **Fix:** Define a valid mapping for every Judge outcome, including failed criteria and evidence. Assign any necessary changes to existing schemas to E; introduce no new kind or frame. Validate a real negative verdict through persistence, generated validators and replay.

### [MAJOR] R09 — Boot wakes progress despite the explicit prohibition
- **Where:** I-5; FR-B-010; FR-D-010; WP-B regression requirements.
- **Problem:** Initial delivery and recovery use contradictory wake rules.
- **Evidence:** FR-D-010 says **“Boot MUST re-wake every unacknowledged inbox entry without a consumed marker.”** Stored progress normally has neither marker nor acknowledgement, so boot wakes it. I-5 also says “no” for the progress/checkpoint/blocker row while its parenthesis says blocker wakes. WP-B preserves today’s wake-kind set, which excludes the newly required `goal_status` wake.
- **Fix:** Publish one exhaustive wake-eligibility table, including error fatality, and use it for initial delivery and recovery. Test mixed message kinds across restart, including progress and nonfatal iteration-limit notices.

### [MAJOR] R10 — Delivery still has unclosed crash and consumption windows
- **Where:** I-3/I-5; WP-B B-4, FR-B-002/011, SC-B-2; WP-D re-nudge scenario.
- **Problem:** Stable event identity and consumption are not defined across all delivery paths.
- **Evidence:** B-4 still promises **“one turn in every case (the first re-runs the child’s finish)”**, including a crash before persistence. No recovery operation recreates that finish with a stable message ID. A terminal lifecycle write followed by a crash before inbox append leaves nothing for boot to retry. Delivery into a live turn also bypasses reconstruction, where the consumed marker is written. Finally, SC-B-2 requires one turn per completion despite the live-turn branch explicitly starting no new turn.
- **Fix:** Define durable pending-delivery identity and recovery between terminal persistence and inbox append. Define consumption for messages entering live turns. Rewrite assertions around delivery into a new **or existing** turn, with the stated at-most-once crash guarantee.

### [MAJOR] R11 — Empty output is both forbidden from completing and persisted as completed
- **Where:** ADR D6/AC-6; I-5 outcome table; WP-C US-4/AS-2; FR-C-008.
- **Problem:** The central completion rule contradicts its executable mapping.
- **Evidence:** FR-C-008 requires completion **“only with a non-empty final answer.”** I-5 maps empty output with a quiet subtree to lifecycle state `completed` and a final `handback`. Saying this is “never as done” does not distinguish it for lifecycle consumers.
- **Fix:** Choose an existing non-success outcome for empty output, or explicitly amend the completion rule and define how consumers distinguish success. Align persistence, status presentation and generated-contract tests.

### [MAJOR] R12 — Job discovery retains contradictory instructions and an impossible oracle
- **Where:** ADR D2/D10; WP-C symbol table, FR-C-010, US-5, C-7.
- **Problem:** The exact “unchanged” instruction survives, while the test asks for two results under a one-result limit.
- **Evidence:** ADR D2 still says the collector **“needs no change.”** WP-C requires task-origin exclusion and deletion of its live-index dependency, yet its symbol table says **“nothing else changes.”** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs_sources.go::collectSubagentRows` still obtains actionability and custom labels from live resolvers. The BDD scenario calls `list_jobs` with limit 1 and requires both workers to appear; the real collector applies a final overall limit.
- **Fix:** State the durable replacements for actionability and labels, plus task-origin exclusion before limits. At limit 1 assert one correctly classified result; use limit 2 or separate filtered requests to prove both workers appear once.

### [MAJOR] R13 — Residual guards reject required code and do not prove containment
- **Where:** WP-F machine constraints; FR-F-001/003/006; AC-11.
- **Problem:** Two proposed guards fail a correct implementation.
- **Evidence:**
  - The zero-occurrence check for `ProducingSessionID` across production Go contradicts I-5’s required `UpwardEvent.ProducingSessionID`.
  - The media guard bans every `len(...Media) > 0` in the tool-output file. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_run_turn_tools.go::persistToolResult` and `recordToolCompletion` use such checks for media attachment and replay, independently of publication.
  - Finding `PublicationAudience` somewhere in that file does not prove the send is gated.
- **Fix:** Scope field deletion to the retired payloads and readers. Preserve media persistence checks. Prove containment behavior through exercised publication boundaries; keep static checks for narrowly defined deleted symbols.

### [MAJOR] R14 — The closure verifier still produces false success
- **Where:** WP-F closure script; FR-F-005/006; AC-13.
- **Problem:** API failures do not reliably propagate, and a section marker without a section passes.
- **Evidence:** The exact script was executed in memory with a stubbed `gh`, without creating files:
  - Comments containing `#999 ADR-091 §`, with no section identifier, passed.
  - Comment retrieval printing a qualifying comment and then exiting 2 still produced verifier exit 0.
  - All API calls failing produced exit 1 rather than the required exit 2.
  
  `exit 2` occurs inside command substitutions or pipeline subprocesses; their statuses are not checked by the caller.
- **Fix:** Check each API call’s status explicitly before processing its output. Validate a real section identifier and delivery citation. Add these precise negative fixtures to the self-test.

### [MAJOR] R15 — Several traceability claims still point to missing or insufficient tests
- **Where:** Appendix D F22; A/B/E/G traceability tables.
- **Problem:** A row naming a scenario or test does not establish the required behavior.
- **Evidence:** A’s pruning, registration, index-report and launch-during-cascade rows lack corresponding BDD scenarios. B names “undeliverable,” “ownership” and “error visible” scenarios absent from its BDD block. The tool-error requirement maps to typed lifecycle-error tests, which can pass without propagating a failed tool result. E-012 maps to test 1, whose listed assertions omit the edge/Stop/run-result fields.
- **Fix:** Add actual scenarios and explicit assertions. Include real tool-error propagation, generated validation of every new wire shape, and the race/crash cases identified above. Make boundary-invocation controls usable through I-7 as specified in R04.

### [MAJOR] R16 — Zero concurrency contradicts the reused configuration
- **Where:** ADR D9; I-3 admission; WP-A “Limit boundaries.”
- **Problem:** The scenario rejects an existing supported unset value, while the admission rule reads the raw field.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents` resolves environment overrides, positive configured values, and zero/unset to the existing safety backstop. WP-A instead says `max_parallel_agents = 0` is refused. Comparing active turns against raw zero would queue everything.
- **Fix:** Reuse the effective resolver and its unset semantics. Test zero/unset and environment overrides. An intentional breaking change requires an explicit decision.

### [MAJOR] R17 — The forged-target check still does not establish caller authority
- **Where:** D5/AC-5; I-5 Principal; WP-B messaging migration.
- **Problem:** Checking that a target has an edge does not prove the sender is its ancestor or the human.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/session_messaging_wire.go::deliverParentToChild` checks target existence and a nonempty parent key. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/bus/session_message.go::SessionMessageEvent` contains no trusted publisher principal. Replacing the key check with edge presence preserves this limitation.
- **Fix:** Bind trusted caller identity to this route, or explicitly restrict it to already-authorized publishers with enforceable wiring. Test a forged target naming another **valid child**. This is an internal authority-contract gap; an external exploit was not established.

### [MAJOR] R18 — The ADR still instructs implementers to leave the pill unchanged
- **Where:** ADR D7 pill row; WP-E FR-E-005.
- **Problem:** The consolidated documents still disagree about the required selector change.
- **Evidence:** ADR D7 describes the existing count as child spans and says **“none”** under Change. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/hooks/useRunningActivity.ts::useRunningActivity` combines agent spans and shell jobs in `runningCount`. WP-E correctly requires a separate direct-child selector excluding shell jobs.
- **Fix:** Correct D7’s current-state description and explicitly require E’s selector change.

### [MINOR] R19 — Some asserted source facts remain inaccurate
- **Where:** I-8; WP-A lifecycle symbol row; WP-E existing-contract row.
- **Problem:** Field names and existence claims are presented as source-verified but are wrong.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/unified.go::UnifiedMeta` has `Type`, not `SessionType`. `LifecycleRecord.Generation` already exists rather than being newly added. E’s “zero non-generated references” claim includes tests, although the named FR-047 guard contains those references. I-8’s root-origin list also omits actual session types such as `channel` and `heartbeat`.
- **Fix:** Use the actual fields, distinguish changed semantics from new fields, qualify reference counts as production-only, and classify all supported root session types.

## Rev-3 dispositions not actually applied

All F01–F24 were checked. Fourteen remain incomplete; the ten verified corrections are listed below.

| Rev-3 finding | Exact remaining gap |
|---|---|
| F02 | Record writes are serialized, but actual cancellation and concurrent child publication are not. R01. |
| F03 | Missing-record classification is repaired; present nil-edge records lack metadata consistency checks. R02. |
| F04 | `Deliver` is injectable; audience resolution still creates forbidden dependencies. R03. |
| F05 | Existing kind names are used, but Judge verdict payload and frame contracts do not support them. R08. |
| F06 | Named files gained owners, but registration, index-report access, lifecycle hooks and overlapping ownership remain unresolved. R04. |
| F09 | Stable finish-event identity and live-turn consumption remain undefined; “one turn in every case” survives. R10. |
| F10 | Progress never wakes initially, but “every” pending entry wakes at boot; wake-kind instructions conflict. R09. |
| F12 | Call correlation is required but cannot survive delayed task start without additional persisted provenance. R07. |
| F13 | E’s count is consistent; ADR D7 still says no pill change. R18. |
| F15 | External tools-test packages fix the import cycle; runtime fixture hooks and boundary probes remain unpublished. R04. |
| F17 | The exact sentence “needs no change” survives in D2. R12. |
| F19 | Revised guards ban required delivery identity and legitimate media persistence checks. R13. |
| F21 | The closure script still mishandles API failures and accepts incomplete citations. R14. |
| F22 | Missing scenarios, insufficient assertions and unusable observation hooks remain. R04/R15. |

## Cross-spec consistency matrix

| Decision | Implementing requirements | Consistent? |
|---|---|---|
| D1 — shared launcher, one memory | A-001–003/009–011; C-001 | **No:** admission result and delayed-task correlation. |
| D2 — canonical edge and reconstruction | A-004–007/013–015; C-003 | **No:** classification, registration and terminal-generation rules. |
| D3 — audience and upward delivery | B-001–013; D-005/010 | **No:** interface, schema, wake and recovery contradictions. |
| D4 — remove wait-inline | C-002; E-007; F-001/003 | **Yes in requirement coverage.** Residual-check repairs remain separate. |
| D5 — steering and authority | C-003–007/011; B messaging migration | **No:** terminal follow-up and bus authority. |
| D6 — completion and optional goal | A-009/012; C-008/009/013 | **No:** empty completion and Judge verdict representation. |
| D7 — existing UI surfaces | B-005–008; E-002–012 | **No:** delayed correlation, verdict frames and pill instruction. |
| D8 — durable Stop | A-007/013/015; D-001–010 | **No:** cancellation races and incomplete runtime contracts. |
| D9 — one limit surface | A-008/011/012; C-012 | **No:** admission outcome and zero/unset concurrency. |
| D10 — delete replaced mechanisms | A/C/E deletion requirements; F-001–003 | **No:** job-index instructions and invalid deletion guards. |
| D11 — remove interim containment | A-004; B-001; D-001–004; F audit | **No:** baseline wording is fixed, but removal checks and permanent Stop proof remain deficient. |

## Traceability gaps

Every decision and ADR acceptance criterion has a nominal mapping. These mappings still lack sufficient proof:

| Requirement | Missing proof |
|---|---|
| AC-1/2 | Present-record/metadata disagreement; ordinary-root inputs; delayed task provenance across restart. |
| AC-3 | Published pre-decision boundary observation; real failed-tool-result propagation into both required views. |
| AC-5 | Authenticated authority on every steering entry, including the bus route and terminal follow-up. |
| AC-6 | Valid Judge verdict encoding; consistent empty-output outcome; live-turn consumption. |
| AC-7/12 | Task creation → restart before start → actual launcher → persisted status → replay/open. |
| AC-8 | Stop-stamp → revival → delayed cancellation; parent-check → cascade enumeration → child publication. |
| AC-9 | Existing zero/unset/environment concurrency behavior and concurrent launch admission. |
| AC-10/11 | Narrow deletion guards that preserve required media and delivery code. |
| AC-13 | Error-safe, delivery-specific closure verification. |
| A-006/013/014/015 | Corresponding BDD scenarios are missing. |
| B-004/007/008 | Named scenarios absent or mapped tests do not establish the claimed behavior. |
| E-012 | Explicit validation tests for lifecycle, Stop and run-result fields. |
| G-004 | No story/scenario mapping; only the CI tripwire is named. |

Issue closure remains **planned, not earned**:

| Issue | Decision → acceptance → named test | Remaining blocker |
|---|---|---|
| #658 | D7 → E-009 → `ActivityPanel.awaitingApprovalLine.test.tsx` | Must execute against actual approval delivery and final contracts. |
| #614 | D5 → C-011 → `TestDelegateStatus_FromRecordNotStreaming` | Mapping is present; implementation/test execution outstanding. |
| #670 | D8 → AC-8 → `TestCascade_StampsAndCancelsReenteredChild` | R01/R04/R05. |
| #755 | D7 → B-006/E-004 → `TestReplay_AfterRestart_ReturnsLifecycleEvents` | R07/R08 and real cross-package execution. |
| #763 | D1/D7 → AC-1/7/12 → launcher readback and reachability tests | R04/R07. |
| #764/#765 | D3 → AC-3 → twelve boundary tests and `TestE2E_ThreeLevelDelegation_NoLeak` | R02/R03/R04/R09/R10. |
| #784 | §12 → AC-13/F-005 | Correctly stays open: content-level quoting remains separate. |
| #803 | §12 → AC-13/F-005 | Correctly stays open: context-limit and resumption policy remain separate. |

The CP-0 classification table is explicitly founder-authorized work. Its unfinished state is **not** a new finding.

## Claims I verified as correct

| Rev-3 finding | Correction that holds |
|---|---|
| F01 | Q18 and the old message permission are explicitly superseded; own-session restriction has real-ownership controls. |
| F07 | Creator-session persistence through `OriginSessionID` and the basic Launch-then-Dispatch sequence are stated. The separate call-ID defect is F12/R07. |
| F08 | Return-before-start ordering is explicitly superseded; no ordering hook is required. |
| F11 | Quietness includes queued/running descendants; parked questions are carried upward; waiting parents hold no execution slot. |
| F14 | All seven contract files containing the obsolete producer field are inventoried. |
| F16 | Timeout zero/default and unset-depth precedence are corrected. R16 concerns a separate concurrency regression. |
| F18 | Ring-dependent test setup may migrate while assertions and controls remain unchanged. |
| F20 | The closure verifier is outside guard discovery and runs post-merge at CP-7. |
| F23 | Approval/question frames are correctly classified as session-scoped. |
| F24 | D11 correctly labels the containments proposed, not landed. |

Additional verified claims:

- The checkout matches the declared release tip.
- **133 unique extracted `file::symbol` pairs resolve textually.** This establishes symbol occurrence, not every behavioral claim.
- Inventories reproduce: **24** production Go files, **58** contract files, **18** narrow SPA files, **96** broad SPA files, **47** mechanism test files/**17,130** lines, and **15** production files referencing `ParentDurableKey`.
- Task-session creation, lifecycle storage, inbox append/acknowledgement, the shared external runner, sidebar nesting, side-panel spans, single-session replay and workspace-scoped approvals are real reusable mechanisms.
- FR-047 guard deletion has an explicit owner and landing condition.
- Self-target permission, ancestor/human steering, optional non-inherited goals, external-worker limitations, per-child completion and originating-channel partial-Stop notices are explicitly represented.
- D8’s explanations of generation, reservation, Stop marker and pre-arm latch address the earlier register concern. No separate register defect is raised.

## Claims I could not verify

- Proposed runtime behavior: no implementation, Go acceptance suite, UI acceptance test or mutation test was executed.
- Historical “80 of 80” live-session measurements, current GitHub issue states, and founder approvals beyond the supplied ledger.
- Atomic crash recovery, durable cancellation, notification consumption and replay ordering as delivered behavior.
- Full behavioral correctness from citation existence or test names alone.
- GitNexus analysis: tools were unavailable; review used direct source inspection.

The closure-script findings were reproduced with read-only, in-memory shell fixtures. No report or other file was created.