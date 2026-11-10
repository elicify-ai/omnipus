# Adversarial review — Steering commands: no person question

**Verdict: REVISE.** Six MAJOR design/handoff gaps, two MINOR findings, and one OBSERVATION remain. The seven founder decisions are locked; the correction must make their integration unambiguous, not reconsider them.

**Correction:** the preliminary skill result treated A1 and A4 as pending against the design-branch baseline. That was wrong for the assigned work-branch pin `ac7848fb8`: A1's already-stopped preservation and A4's rollback on sender-record save failure are already implemented there. Correct is to mark those adjustments implemented at that pin, with execution evidence not assessed here. Impact: neither is an open implementation request in this report; MAJ-004 concerns the separate abrupt-process-exit commit boundary, not redoing A4's rollback.

| Review metadata | Value |
|---|---|
| Document | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question.md` |
| Source amended | **The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume**, read-only snapshot `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md` |
| Mode / round | ADR mode; exactly one fixed grill. This report verifies and assembles the skill's returned findings, not a second grill. |
| Date | 2026-10-04 |
| Design checkout | `work/a-steering-design-20261004` at `f1672ea90796e9cbc4ff1b83f25058816c784292` |
| Newer implementation comparison | `work/a-lane-combine-notice-epoch-20261004`, pinned to `ac7848fb8`; read through Git objects without switching or modifying either checkout |
| Certainty standard | Document/code facts: **Verified, high confidence** where cited. Failure scenarios: **Inferred, high confidence** unless qualified; no runtime reproduction claimed. |
| Instruments | GitNexus MCP tools/resources unavailable; direct reads and scoped caller searches used. No graph-impact result claimed. No production symbol edited. |
| Execution | No tests, builds, browser campaign, or CI pass claimed. |

“Amendment” and “source ADR” below refer to the two absolute paths above. A code citation qualified `@ac7848fb8` is a read of that pinned Git object, not a claim that the file in this design checkout contains the newer implementation.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 2 |
| OBSERVATION | 1 |
| **Total** | **9** |

## Findings

### MAJ-001 — Ordinary-message resumption is not reconciled with the preserved state/routing rules

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR** — Ambiguity; inconsistency with the source ADR; reachability |
| Affected sections | Locked decision 5; replacement table D3 row; Precedence; Not changed |
| Failure scenario | In root → C → D, C is stopped while D keeps working. D sends an ordinary question to C. Decision 5 says messages both ways are steering and resume a stopped helper; preserved D6 says descendants' messages stay stored until C resumes. One implementer starts C, another leaves it stopped. Likewise, a parent message to a done/failed C can be rejected under preserved D3 even though the new decision says an ended turn resumes. A replay of an old message can also be mistaken for a new resuming submission. |
| Evidence | Amendment decision 5 and its stopped-only D3 replacement; source ADR::D3 refuses done/failed steer, ::D5 retains the old ordering table, ::D6 stores upward messages until resume, and ::D8.5 prohibits recovery dispatch from old controls. These are explicit text conflicts, not inferred missing features. |
| Certainty | **Verified, high** for the conflicting text; **Inferred, high** for divergent implementations. |
| Recommendation | Add one normative ordinary-message/state table covering both directions, working/stopped/done/failed recipients, the named tool/inbound entry paths, and fresh submission versus retry/replay. Explicitly supersede the affected D3/D5/D6 rules while retaining D8's no-boot-dispatch guarantee. State how a fresh ordinary message obtains the preserved execution identity and admission boundary. Distinguish ordinary conversation messages from runtime notices and telemetry: a stop-notice ring must not itself undo Stop. Do not recreate person-question routing. |

This is a reconciliation task under decision 5, not a question about whether messages should resume helpers.

### MAJ-002 — The removal boundary does not identify the retained message contract or all question producers

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR** — Incompleteness; contract-first gaps; reachability; overcomplexity |
| Affected sections | Locked decision 6; Withdrawn behavior; A3; Not changed |
| Failure scenario | An implementer deletes owner escalation/expiry but keeps the existing “ordinary question” call, `message_parent(kind="question", wait=true)`. Its omitted authority still defaults to owner-required, it still parks the helper, and parent `respond` still refuses the answer. The old special wait survives without its expiry. Deleting the whole question family instead, without mapping its producers/consumers, leaves delegated clarification unable to send a valid message. |
| Evidence | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/message_parent.go::MessageParentTool.Parameters`, ::messageParentToolExecute.encodeMessage, ::messageParentToolExecute.finishDelivery: `wait` and `authority`, owner-required default, park and `ParksTurn`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/delegate_park.go::delegateToolExecuteRespond.verifyQuestionAuthority`: refuses owner-required. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/askuser/registry.go::Registry.CreatePending`, ::relaySteeredQuestions: another delegated producer emits a typed `question`, `self_ok`, `wait=true`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/contracts/components/schemas/SessionMessageQuestion.yaml::wait`, ::authority and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/contracts/components/schemas/ControlReceipt.yaml::verb` retain the old question/action surfaces. Source ADR::Vocabulary, ::D4, ::D6b still describe waiting-for-answer states and blockers. |
| Certainty | **Verified, high** for retained surfaces; **Inferred, high** for an incomplete removal. |
| Recommendation | Specify the retained ordinary parent/child message shape and a disposition for `question`, `decision_request`, `wait`, `authority`, `respond`, `needs_input`, pending-question records, and every delegated question producer. Explain ordinary turn completion and subsequent-message resumption, including how completion-frontier checks treat the resulting state. Make the scope of any retained self-answerable structured question explicit without restoring a person-only mechanism. Distinguish this helper change from the root user's existing clarification cards; do not remove those by assumption. Backend-lead changes boundary schemas and regenerates before Go/SPA consumers; prompts/tool descriptions and schema mirrors must agree. No replacement expiry. |

A5/A6 name a contract-first rename, but do not settle this broader message/state contract. An ADR need not contain the implementation plan; it does need the shape and deletion boundary.

### MAJ-003 — Fence-less notice retries have no durable historical identity after resume

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR** — Incompleteness; infeasibility of the current identity scheme; testability |
| Affected sections | Locked decision 1; A2; Positive consequences |
| Failure scenario | A fence-less stop lands, its notice append fails, and a fresh message resumes the helper. Resume clears the active stop note; a later delivery pass/restart cannot discover the lost transition. Separately, two fence-less stops within one generation use the same generation-derived stop sequence. After the parent takes the first notice, the second actual stop can be suppressed as the already-taken notice. |
| Evidence | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/stopped_notice.go::stoppedTransitionFromLandedNote` expressly uses the generation stand-in and writes no history; ::stoppedChildNoticeID keys by parent/child/generation/sequence; ::deliverLandedStopNotices discovers only ledger history. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/steer_completion_commit.go::landSteeredStopLocked` synthesizes `Seq: uint64(cur.Generation)`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/session/lifecycle_control_ledger_writer.go::LifecycleStore.RecordLandedStopLocked` requires accepted control identity; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/session/lifecycle_control_ledger.go::LifecycleStore.ListStoppedTransitions` reads that ledger. Source ADR::D2 clears the active note on same-generation resume; amendment A2 recognizes the one-shot gap but supplies no replacement discovery/identity rule. |
| Certainty | **Verified, high** for identity/discovery; **Inferred, high** for loss/collision. |
| Recommendation | Require a durable historical descriptor for every actual stop transition, including fence-less stops, with distinct transition identities in the existing notice key space. Name the owning store, durable creation boundary, discovery after resume, and retirement/ack rule; never fabricate an accepted control. Retain original actor/cause/time. Reuse the existing notice format and publisher, not a second notification system. Name event-driven delivery-pass triggers and the visible pending-error surface, so “until taken” does not depend on an unspecified future caller. No periodic timer. |

The production caller sweep found landing and boot paths for the notice publisher. That verifies the inspected wiring, not a proof that no indirect mechanism could ever be added. The live retry handoff must be explicit in the correction/spec.

### MAJ-004 — The all-or-nothing promise lacks an abrupt-exit acceptance/recovery boundary

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR** — Incompleteness; infeasibility; testability and false-green risk |
| Affected sections | Locked decision 7; A4; Negative consequences |
| Failure scenario | The transcript append finishes and syncs; the process exits abruptly before the sender-record append. The error-return rollback never runs. A write-error test can be green while reopening the store still exposes a transcript message with no sender record. A rollback failure is another explicitly returned error that does not, by itself, remove the surviving line. |
| Evidence | **A4's returned-error rollback is already implemented** at `ac7848fb8`, via `775250d9f`: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/session/transcript_indexed.go::UnifiedStore.appendTranscript @ac7848fb8` captures pre-state, appends transcript, then provenance, and invokes rollback only on a returned provenance error; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/session/message_provenance.go::rollbackTranscriptAppend @ac7848fb8` restores/removes the file and returns rollback failures. The read regression `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/session/message_provenance_torn_write_test.go::TestAppendTranscriptWithProvenance_FailureRollsBackTranscriptLine @ac7848fb8` exercises a real sender-path error and byte restoration, not process exit between the two saves. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/fileutil/file.go::AppendJSONL` syncs each append separately. Amendment decision 7/A4 select no combined commit or recovery protocol. |
| Certainty | **Verified, high** for implemented A4 and sequential writes; **Inferred, high** for the crash window. Crash acceptance is not tested here. |
| Recommendation | Mark A4's handled save-error rollback implemented, then select the durability scope and one authoritative acceptance boundary for the stronger “together, or neither survives” promise. A combined internal record or a recoverable transaction protocol are technical alternatives, not new product decisions. Define which durable event accepts the pair, how incomplete writes are hidden/reconciled before transcript/replay/model readers see them, and when echo/ack/turn admission may follow. Keep sender identity server-internal. Require real-store process-reopen tests at the actual write cuts and a rollback-failure case; a same-lock assertion or successful compensation test is not crash-atomicity evidence. |

This finding does **not** reopen the implemented A4 code or ask the founder to weaken decision 7. It asks the architect to specify its full persistence meaning; “web intake takes on an atomicity duty” is a requirement, not the missing mechanism.

### MAJ-005 — Replaying an old stop notice can skip recovery of a newer interrupted run

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR** — Inconsistency with preserved D8; failure/recovery completeness |
| Affected sections | A2; Precedence; Not changed |
| Failure scenario | A child stops, its parent has not taken the notice, and a fresh action resumes it. That new run crashes. At boot, successful re-ringing of the historical unacknowledged notice returns “handled” for the current running/queued record; its caller exits before stopping the interrupted current run. The persisted state can remain working with no live process. A historical replay error can cause the same skip. |
| Evidence | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/stopped_notice.go::SteerBootRecovery.recoverStoppedChildNotice @ac7848fb8` returns `pending || replayErr != nil` for running/queued records without an unacknowledged final; ::deliverLandedStopNotice reports work for an untaken ring. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` returns immediately on that boolean before current-run recovery. The `0b071bdb7` A1 change adds preservation for an **already-stopped** record; it does not change this historical-replay return. Source ADR::D8.1/5/9 requires stopping current uncommitted working runs independently of historical delivery. |
| Certainty | **Verified, high** for control flow, also checked at the newer pin; **Inferred, high** for the stopped → resumed → crash scenario. |
| Recommendation | State that historical notice delivery and current-run recovery are independent obligations. An old pending notice, successful ring, or replay failure must never exempt current queued/running work from preserved D8. Recover the current lifecycle before publication can wake a parent against half-recovered state. Add the stop → untaken notice → resume → crash → boot regression, asserting both historical notice identity and current stopped state with no automatic run. |

**A1 is already implemented at `ac7848fb8` through `0b071bdb7`; this is a separate current-running-record case, not an A1 rework.**

### MAJ-006 — User-document handoff still inherits instructions for the removed behavior

| Field | Finding |
|---|---|
| Severity / lenses | **MAJOR (Important handoff gap)** — Incompleteness; UI journey/help and documentation |
| Affected sections | Consequences; adjustments; preservation of source ADR |
| Failure scenario | Implementation follows the inherited documentation unit, which still instructs signed-in owner-only answering and 24-hour expiry. Users wait for a removed card/code or expect a helper to fail after 24 hours. Existing guide text also says a single Stop ends descendants, leading users to expect work to be contained when the locked decision says descendants keep going. |
| Evidence | Amendment contains no named user-facing pages or specific documentation TODOs. Source ADR::Build plan unit 4 and ::Impact on existing code, user-facing guide row still require owner-only answer/expiry documentation. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/agents.md::Workers and delegation` says Stop stops everything below and names `follow_up`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/reference/built-in-tools.md::General` (`delegate`, `message_parent`, `AskUserQuestion` rows) describes old verbs and question waiting/relay. Root `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/CLAUDE.md::Definition of Done` requires affected pages and specific TODOs at design time. |
| Certainty | **Verified, high** for text/handoff gap; **Inferred, high** for user confusion. |
| Recommendation | Add named TODOs for `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/agents.md::Workers and delegation` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/reference/built-in-tools.md::General`: single Stop versus Stop all, stopped/finished message resumption, the final verb names, ordinary helper questions with no person-only wait/expiry, and the chosen delegated clarification mapping. Supersede the old guide-unit instructions explicitly. Implementing leads draft matching user-doc changes in the implementation diff; docs-verifier checks them against integrated behavior. This review does not demand changing user docs in the design-only commit. |

### MIN-001 — Required source acceptance cases have no retained/superseded/replaced disposition

| Field | Finding |
|---|---|
| Severity / lens | **MINOR** — Testability and false-green risk |
| Affected sections | Precedence; Withdrawn behavior |
| Failure scenario | qa-lead inherits source tests that require owner-only acceptance, question retention/expiry, or one wake, and either tests a removed product or deletes unrelated Stop/replay protection to get green. |
| Evidence | Source ADR::Required acceptance tests T1–T3, T8, T14, T25 and question clauses in T19/T22/T26 encode removed question behavior; T6/T15 encode notice/wake behavior needing reconciliation with decision 1. The amendment supplies no disposition. |
| Certainty | **Verified, high** for the test obligations; **Inferred, medium** for implementation/test-author drift. |
| Recommendation | Add a concise retained/superseded/replacement map. Preserve T11's final-versus-Stop/outbox proof, T27's same-generation stale-effect protection, non-cascading Stop and goal-preservation tests. Replacement expected values come from the seven decisions, not the current code. |

### MIN-002 — Adjustment status conflates the earlier checkout with the implemented work-branch pin

| Field | Finding |
|---|---|
| Severity / lens | **MINOR** — Inconsistency; evidence quality |
| Affected section | Code that already contradicts this amendment, especially A1/A4 |
| Failure scenario | A lead treats the undated-as-to-revision “adjustments to make” list as the integration backlog and dispatches already-completed A1/A4 work, potentially overwriting newer fixes. |
| Evidence | Amendment A1/A4 cite the older design checkout. `git merge-base --is-ancestor 0b071bdb7 ac7848fb8` and the corresponding `775250d9f` check both exited 0; scoped source reads verify preservation and rollback at the assigned pin. A1's old body is not proof that normal boot reached it: the design checkout's stopped-notice recovery ordinarily returns before that writer arm. |
| Certainty | **Verified, high** for pins and code; **Inferred, medium** for duplicate dispatch. |
| Recommendation | Pin the adjustment inventory and mark A1/A4 implemented at `ac7848fb8`, not integrated into this earlier design checkout and not acceptance-certified by this review. Separate MAJ-004/005's distinct obligations from those completed adjustments. Do not amend the read-only source snapshot. |

### OBS-001 — State which security boundaries survive removing owner-answer validation

| Field | Observation |
|---|---|
| Severity / lens | **OBSERVATION** — Security |
| Evidence | Amendment Negative says “nothing in the message path distinguishes an owner's text from an agent's text,” while decision 7 deliberately retains the server-side sender record. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/delegate_park.go::delegateToolExecuteRespond.validateAndLoad` checks steering authority; ::messageParentToolExecute.encodeMessage in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/message_parent.go` labels child content untrusted. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/tools.md::Allow, ask and deny` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/security.md::Approve a request` describe independent tool approvals. |
| Suggestion | Say “no owner-only question-answer authorization” rather than “no distinction.” Retain connection authentication, ordinary session/ancestor authorization, untrusted agent-content labeling, and tool approval policy. Persistence of sender identity neither authorizes an answer nor disappears with the special acceptor. |
| Certainty | **Verified, high** for text and existing boundaries. No new exploit established; this observation does not gate alone. |

## Structural integrity and twelve-lens coverage

This amendment records already-made founder choices; it is not required to invent another open product question. No missing “open decision” finding is raised. Alternatives needed for new persistence mechanisms belong in the correction, with rationale and ownership; rejected person-only designs are not to be reconsidered.

| ADR structure | Result |
|---|---|
| Status / date / deciders, Context, Decision, Consequences | Present |
| Source cited by title and precedence declared | Present; precedence reconciliation incomplete (MAJ-001/002) |
| Changed persistent-data ownership and recovery decided | Incomplete (MAJ-003/004/005) |
| Named user-document impact | Missing (MAJ-006) |
| Current adjustment inventory pinned | Stale for A1/A4 (MIN-002) |

| Lens | Assessment |
|---|---|
| 1. Ambiguity | MAJ-001: ordinary message entry paths/states versus retained routing rules |
| 2. Incompleteness | MAJ-002/003/004/005/006: deletion boundary, durable identity, commit/recovery, handoff |
| 3. ADR/as-is contradictions | MAJ-001/005 and MIN-002. Read **An open conversation must keep the ability to delegate** and **Mid-turn steering — assistant reply must not render above the message that triggered it**; no separate contrary product decision established. Historical as-is documentation does not override the inspected code/new founder decisions. |
| 4. Infeasibility | MAJ-003/004: generation identity and independent file saves cannot supply the stronger guarantees as currently represented. No new runtime dependency is proposed. |
| 5. Contract-first gaps | MAJ-002: retained/deleted question and receipt shapes are not settled. A5/A6 acknowledge generation order, but do not cover all changed producers/consumers. |
| 6. Security | MAJ-004 attribution integrity; OBS-001 retained authorization boundaries. Removal of the owner-only acceptor is intentional, not itself a vulnerability finding. |
| 7. Reachability | MAJ-001/002. Existing tools have shipped global policies (`delegate` and `message_parent`: allow); constructor/wiring searches find real runtime sites. This is not proof the amended actions are invokable or runtime-tested. |
| 8. UI states and journey | MAJ-006; web-save error/no-start versus accepted/no-response needs end-to-end acceptance under MAJ-004. Source D9's selected-session Stop/Stop-all journey stays binding. No new screen is specified here. |
| 9. Accessibility and keyboard | No independent new defect established. Preserve source D9's focused-conversation Esc priority and confirmation rules. Failed-save feedback must be perceivable without stealing composer focus; verify in the implementation spec/campaign. No browser accessibility PASS claimed. |
| 10. Design-system reuse and brand | No new primitive or visual system proposed. Read catalog/design constitution/brand/UI rules; existing FormError, Button, IconButton and ConfirmDialog entries provide relevant building blocks. No visual/brand finding invented. |
| 11. Testability and false-green risk | MIN-001; MAJ-003/004/005 require actual consumer/reopened-store tests, not source-string assertions or manual acknowledgement alone. |
| 12. Overcomplexity | MAJ-002: leaving deleted specialization behind ordinary messages maintains two mechanisms. Reject a second notice format, replacement expiry, or periodic notice timer as explicitly outside the locked choices. |

### Relevant usability heuristics

**Several issues to address:** the behavior handoff is incomplete; no new visual design is being adjudicated.

| Heuristic | Required consequence |
|---|---|
| H1 — Show system status | Distinguish stopped from failed; distinguish rejected web save from an accepted message whose answer has not started. |
| H3/H5 — User control and error prevention | Preserve Stop/Stop-all scope and replay-versus-new-message boundaries; a ring is not a user decision to resume. |
| H9 — Error recovery | Failed save visibly rejects the message and starts no turn; test Retry against the actual durable pair, not only a local error bubble. |
| H10 — Help/documentation | MAJ-006 names the guide/catalog updates so users do not follow the withdrawn answer/expiry recipe. |

Priority actions: reconcile message/removal contracts (MAJ-001/002); decide durable stop/intake recovery (MAJ-003/004/005); attach the test and user-document handoff (MAJ-006/MIN-001).

## Test coverage assessment

No execution success is reported. The newer A4 test was read, not run; its handled-error coverage is not crash certification.

| Category | Required acceptance / negative case |
|---|---|
| Message state matrix | Both directions into working, stopped, done and failed helpers; no mid-turn interruption; new-message resumption versus retry/replay; runtime notices do not resume stopped work |
| Question removal | Normal helper question does not enter the retired owner-only park, hit owner-only answer refusal, or expire at 24 hours; every delegated clarification producer uses the selected replacement; root cards remain within the declared scope |
| Notice identity/retry | Two fence-less stops in the same generation; take the first and deliver the second; notice survives resume and restart; failed append/wake stays visibly pending |
| Consumer deduplication | Repeat rings through the actual parent dequeue/context/ack path; assert no duplicate work/injection. One inbox row plus a test-issued ack is insufficient. |
| Current-run boot recovery | Untaken historical notice → resume → crash: replay remains owed and the current run becomes stopped without dispatch, even when replay fails |
| Intake persistence | Existing and first-message saves, sender-path failure (preserve implemented A4), rollback failure, process exit/reopen at real storage cuts, transcript/replay/context readers, no echo/ack/turn before the defined commit |
| Frontend and reachability | Real composer error/retry, no phantom replay message, stopped/failed labels; focused Esc versus higher-priority dialog handler; named tool actions exercised through production wiring |
| Regression preservation | Source T11/T27, final-before-Stop and Stop-before-final, same-generation execution identity, goal preservation, non-cascading Stop, downward Stop all, no boot auto-resume |

The documented security distinction also needs a negative case: agent-authored text that claims “operator approved” is ordinary untrusted content, not a tool approval or forged gateway principal.

## Threat assessment

STRIDE means spoofing, tampering, repudiation, disclosure, denial of service, and privilege escalation. This is a design review, not a security certification.

| Flow | Assessment |
|---|---|
| Web message + sender | MAJ-004: recoverable attribution/acceptance boundary missing. No disclosure exploit established. |
| Parent/helper messages | MAJ-001/002: start eligibility and removal mapping ambiguous; preserve authority and untrusted-origin boundaries (OBS-001). |
| Stop notices | MAJ-003: historical event loss/collision; duplicate-work guarantee needs real consumption tests. Event-driven retries must not add unbounded in-memory history or a new notifier. |
| Boot replay | MAJ-005: past delivery obligation must not suppress recovery of current work or authorize dispatch. |
| Stop/Redirect/Resume effects | Retain source execution-identity checks and tool permissions. No separately verified new privilege bypass. |

## Questions for the founder

**None that reopen the seven decisions.** The review found reconciliation and technical mechanism gaps, not grounds to ask again whether messages resume helpers, whether questions park for a person, whether expiry returns, or whether message/sender persistence is all-or-nothing.

Team-lead presents this separate list before the single correction. If the architect concludes an implementation choice cannot satisfy a locked decision, escalate that concrete conflict; do not silently weaken the decision or initiate another grill.

## Author-facing questions for the correction

| ID | Answer required from the architect |
|---|---|
| C1 | What normative state/path/replay table replaces the incompatible D3/D5/D6 message rules? |
| C2 | What ordinary helper message contract remains, and what happens to every structured-question producer, field, state and response action? |
| C3 | What durable historical identity and event-driven delivery hooks retain every fence-less stop across resume/restart? |
| C4 | What durable event accepts the message/sender pair, and how are incomplete writes hidden/recovered after abrupt exit or failed compensation? |
| C5 | How do current-run recovery and historical notice replay proceed independently? |
| C6 | What source test obligations and guide-unit instructions are superseded, and what named user-doc TODOs replace them? |

## Verdict rationale and next action

**REVISE** because MAJ-001 through MAJ-006 leave contradictory state behavior, incomplete deletion/data contracts, or a missing mandatory user-document handoff. No independently verified CRITICAL design finding remains, and this report makes no reproduced runtime failure/PASS claim. A1/A4 must not be dispatched as still-open fixes.

This is the ADR's one fixed grill round. Next: team-lead presents “Questions for the founder” before the architect's **one correction round** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question.md`. No second grill runs on this revision. Any blocking conflict still open after that correction goes to the founder.

Code correct and tested: **Unknown for the amended behavior; not established by this design review.**

Reachable by a user/agent: **Existing messaging surfaces located; amended end-to-end behavior not verified.**

skills: grill-spec, omnipus-shared-rules, gitnexus-exploring, ux-heuristics-review, jev-use:jev-use, commit-messages

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Review baseline and source are pinned, not modified | `git rev-parse HEAD` → exit 0, `f1672ea90796e9cbc4ff1b83f25058816c784292`; initial `git status --short` → exit 0, empty. `git hash-object` of the reviewed ADR and source snapshot → exit 0, respectively `9b315c8ffd608c941238aabbec06b7101fd7a422` and `597d6afc92728a80fbb4adaf7483bda7dd132566`. Full amendment and source snapshot read. | Verified — high |
| A1/A4 implemented at the assigned newer pin, not open adjustments | `git merge-base --is-ancestor 0b071bdb7 ac7848fb8` → exit 0; corresponding `775250d9f` check → exit 0. Scoped `git show` reads → exit 0, A1 stopped guard/no failed rewrite and A4 `rollbackTranscriptAppend`. The implementation test source was read, no run claimed. | Verified — high for code; runtime/CI acceptance Unknown |
| MAJ-001: preserved rules conflict with ordinary-message resumption | Amendment::Decision 5/replacement table/Precedence; source ADR::D3, ::D5, ::D6, ::D8.5 read. | Verified — high for text; divergent outcome Inferred — high |
| MAJ-002: question contract has multiple retained producers and parks | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/message_parent.go::messageParentToolExecute.encodeMessage/finishDelivery`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/tools/delegate_park.go::delegateToolExecuteRespond.verifyQuestionAuthority`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/askuser/registry.go::relaySteeredQuestions`; question/receipt schemas and source D4/Vocabulary read. | Verified — high; incomplete-removal outcome Inferred — high |
| MAJ-003: fence-less identity/history is insufficient after resume | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/stopped_notice.go::stoppedTransitionFromLandedNote/stoppedChildNoticeID/deliverLandedStopNotices`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/steer_completion_commit.go::landSteeredStopLocked`; control-ledger writer/reader cited above read; source D2 read. | Verified — high; loss/collision Inferred — high |
| Production publisher search sees landing/boot, not a defined live retry handoff | Scoped `rg` over non-test Go files for `deliverLandedStopNotices`, `deliverLandedStopNotice`, `recoverStoppedChildNotice` → exit 0; finds definitions and completion/cancel/boot callers. Positive control finds the known publisher definition and landing call; no empty-search proof used. | Verified — high for sweep; exhaustive indirect-flow coverage Unknown |
| MAJ-004 is distinct from implemented A4 error rollback | Scoped newer-pin reads of `UnifiedStore.appendTranscript` and `rollbackTranscriptAppend` → exit 0; two sequential appends, error-return compensation. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/fileutil/file.go::AppendJSONL` calls `f.Sync`; assigned A4 test reads real error/byte-restore assertions, not crash cuts. | Verified — high for mechanism; abrupt-exit outcome Inferred — high |
| MAJ-005 survives the pinned A1 change | Scoped `git show` of newer recovery/publisher → exit 0; `git grep` at `ac7848fb8` → exit 0, `return pending || replayErr != nil`. `SteerBootRecovery.recoverSteered` read: immediate return precedes current-run stop. A1 diff read and ancestry verified separately. | Verified — high for control flow; resumed-crash outcome Inferred — high |
| MAJ-006 and MIN-001 are amendment handoff gaps | Amendment read completely; source ADR::Build plan unit 4, ::Impact, ::Required acceptance tests read; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/agents.md::Workers and delegation` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/reference/built-in-tools.md::General` read; root Definition of Done supplied/read in task. | Verified — high |
| Existing tool wiring/policies found, not amended reachability certified | Scoped `rg` → exit 0, `NewMessageParentTool` in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/session_messaging_wire.go`, `NewDelegateTool` in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/agent/loop_wire.go`; policy matches in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/pkg/config/defaults.go`. Initial combined lookup exited 2 because the historical catalog path was absent; it was not counted as reachability/absence evidence. | Verified — high for located sites; effective runtime behavior Unknown |
| No independent new visual/keyboard/security exploit established | Source D9; catalog FormError/Button/IconButton/ConfirmDialog entries; design constitution, brand and UI rules read; user tool/security approval sections read. No browser/penetration test invoked. | Verified — high for read scope; runtime safety/accessibility Unknown |
| One grill and read-only review discipline | `grill-spec` invoked once; its returned findings independently checked before assembly. No second grill, no production/contract/test/ADR edit, no local suite. `ListMcpResourcesTool` returned no resources and no GitNexus MCP tools were available; no graph result claimed. | Verified — high |
| **Self-check** | Final report/diff rechecked against the returned grill and task criteria: nine entries (6 MAJOR/2 MINOR/1 OBSERVATION), seven decisions locked, A1/A4 status corrected at `ac7848fb8`, inferred scenarios distinguished from runtime evidence, twelve lenses, separate founder list, one-correction handoff, absolute paths and skills. Inline document/scope checker rejected missing-field and extra-file negative controls; source-hash checks exited 0 with the original hashes. `git diff --cached --name-status` exited 0 with only this review added; `git diff --cached --check` exited 0; branch check exited 0 with `work/a-steering-design-20261004`. Initial checker exited 1 after incorrectly requiring a no-index diff of a new file to return 0; switched to the staged whitespace check, which exited 0. That was an instrument correction, not a code/test failure. Commit/push/authorship receipt is reported separately. | Verified — high for artifact/scope checks; no runtime delivery claim |
