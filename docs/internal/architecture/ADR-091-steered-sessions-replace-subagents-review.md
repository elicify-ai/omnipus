VERDICT: BLOCK
CRITICAL: 3  MAJOR: 17  MINOR: 2

Reviewed against checkout `5d38291f39d28216b91368c8bfd1f7969c21c405`. This was a read-only source review; no files changed and no runtime tests executed.

### [CRITICAL] The authorization gates are not equivalent

- **Where:** §2.1, D5, §5
- **Problem:** Reusing task authorization can permit work that delegation currently forbids. “Same gate” conceals different operation modes and different self-delegation rules.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_delegation.go::buildDelegationDenyChecker` immediately permits self-targets when `selfAssignmentExempt` is true. Task wiring enables that exemption; delegate wiring does not. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_wire.go::registerSharedTools` also selects `DelegationModeTask` versus `DelegationModeBackground`. These modes can receive different graph permissions.
- **Fix:** Replace the equivalence claim with the actual differences. Specify the authorization mode and self-target rule for the unified operation. Distinguish creation authorization from subsequent steering authority, and decide what policy revocation does to existing sessions. Require negative tests for prohibited modes, self-targets, and revoked edges.

### [CRITICAL] D10 deletes the external runner already shared by both paths

- **Where:** §2.2, D10, WP-C
- **Problem:** The proposed deletion target is not an independent delegation-only implementation. Tasks already call it.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor_run.go::processTaskDirectExternalCLI` calls `runExternalCLISubTurn`. The delegation setup calls the same runner. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/external_dispatch.go::runExternalCLISubTurn` supplies workspace enforcement, runner invocation, cancellation wiring, and transcript/event handling.
- **Fix:** State “two setup wrappers, one shared runner.” Retain or rename the runner and consolidate its callers. Replace the file-deletion instruction with an explicit preservation requirement for workspace isolation, cancellation, deadlines, and transcript handling.

### [CRITICAL] Neither existing upward-wake path establishes the claimed parent identity

- **Where:** §1, §2.2, D3
- **Problem:** The ADR treats existing notification code as proof that completion and questions already wake the correct parent session. That proof is false in two places.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor_judge.go::notifyParentIfAllSiblingsDone` constructs `parentChatID := "task:" + parent.ID`; it never reads the real `parent.SessionID`. It also requires all siblings to be terminal and the parent task to remain `in_progress`. Separately, `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::messageParentToolExecute.finishDelivery` wakes using the **child’s** `rec.AgentID` and `childSessionID`. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go::WakeParent` forwards those identities unchanged. The inbox address is correct; the wake identity is not.
- **Fix:** Correct both factual claims. Define one upward-delivery operation that resolves the validated steering session and its agent, distinguishes producer from recipient, and specifies per-child versus all-siblings completion. Test which actual agent/session executes the wake; an inbox-write assertion is insufficient.

### [MAJOR] D5 cannot call StartTaskNow with the object D1 creates

- **Where:** D1, D5, WP-A/C
- **Problem:** The proposed sequence creates a session and then calls a function that requires an existing task and creates its own session. The ADR never decides whether every delegation also becomes a board task.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::StartTaskNow` accepts a **task ID**, loads the task, creates its session, and returns without dispatch when `Task.SessionID` is already populated. Existing task creation also tolerates metadata and lifecycle-write failures, contrary to Appendix A’s stronger creation guarantee.
- **Fix:** Choose either a shared session-launch primitive beneath both tools or an explicitly defined task-backed delegation. Specify who allocates each ID, creates the record, dispatches, and compensates for partial writes. Require no execution until mandatory identity, ownership, workspace, and edge writes succeed.

### [MAJOR] The audience function lacks identity at several delivery boundaries

- **Where:** D3, WP-B, AC-3
- **Problem:** D3’s universal rule cannot be implemented solely at the listed turn-loop sites. Some senders have no producing-session identity; streaming bypasses those publication methods altogether.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message.go::MessageTool.Execute` accepts explicit destinations, but `SendOrigin` carries agent/workspace information rather than the producing session. Its callback in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_wire.go::registerSharedTools` publishes outside the originating turn context. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor_judge.go::notifySourceChannel` independently sends task results. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/channels/manager.go::finalizeHookStreamer` suppresses delegated streaming through `parentSpawnCallID`, outside the proposed publication-function list.
- **Fix:** Require producing-session identity through direct messaging, task notifications, and stream creation/update/finalization. Explicitly include Telegram and WeCom streaming. Decide whether D3 prohibits agent-requested `send_message` as its wording currently implies. Add real delivery-boundary tests, including external-channel captures.

### [MAJOR] Moving the edge leaves ownership, hierarchy, and question guards behind

- **Where:** D1–D3, D5, D7, AC-1/3
- **Problem:** “One edge on LifecycleRecord” does not explain the existing second parent field or ownership stamp. Copying task creation loses both, potentially disabling the human-question prohibition.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::createChildSession` stamps `UnifiedMeta.ParentSessionID`. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/unified_api.go::CreateSessionWithID` copies the parent’s `Owner`. Task creation does neither. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/askuser/registry.go::Registry.CreatePending` rejects children using `ParentSessionID`; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/ask_user_question.go::AskUserQuestionTool.Execute` separately relies on positive delegation depth. Question cards broadcast through a separate gateway path.
- **Fix:** Name the canonical parent relationship and specify how metadata indexes derive from it. Preserve the human ownership stamp explicitly. Make question admission read the durable audience authority, including after re-entry. Specify parent relay for owner-required questions and test card emission, answer/timeout resume, and cancellation.

### [MAJOR] A parent-child relationship does not authorize a projected viewer

- **Where:** D7, AC-6/7
- **Problem:** The ADR defines steering authority but not human viewing authority. Automatically subscribing a parent’s viewer to children can cross principal or workspace boundaries.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_replay.go::loadReplay` validates the ID, resolves the store, and reads the transcript without checking the connection’s user against session ownership. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_streamer.go::resolveSessionConnsLocked` selects connections by session mapping. Neither supplies the authorization guarantee D7 needs.
- **Fix:** State whether all authenticated operators may view all sessions or whether access is scoped. For scoped access, authorize every child subscription, replay, and media read on the server. Parent visibility must not imply child visibility. Add denied-child and revoked-access tests.

### [MAJOR] Projection requires a transport and replay decision, not just client bucketing

- **Where:** D7, Q3, WP-E, AC-6/7
- **Problem:** Existing attachment and replay behavior does not provide the proposed multi-session projection. Also, “no stream is shadowed” accidentally includes a separate protection against concurrent narration.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_replay.go::bindConnection` replaces the connection’s attached session mapping. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/replay.go::emitNestedToolCalls` rebuilds nesting from the attached transcript, not independently subscribed child histories. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_streamer.go::wsStreamer.Update` sets `isShadowStream` both for delegation and when another turn already owns the same session’s stream.
- **Fix:** Choose bounded multiple subscriptions or multiple connections; specify cursors, reconnect, unsubscribe, ordering, and deduplication. Preserve same-session stream ownership while removing delegation-specific suppression. Extend acceptance to reload, late attachment, completed children, grandchild replay, and competing turns within one session.

### [MAJOR] A derived root ID does not preserve the pre-arm latch

- **Where:** D8, AC-5
- **Problem:** The latch depends on evidence that dispatch is imminent, not merely the ID a future turn eventually receives. A child wake queued under its own worker is not automatically imminent under the root.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/cancel_prearm.go::armCancelOrFindActiveTurn` consults `turnImminentForIdentity`, which requires matching worker or pending-spawn evidence. `preArmKeysForTurn` checks the inherited routing ID and channel/chat identity. The latch is consumed once and expires after five seconds.
- **Fix:** Define reservation-before-dispatch and atomic ordering between Stop, child creation, and re-entry. Cover multiple pending descendants and distinguish canceled work from a later independent turn. AC-5 must block real registration after a real enqueue; test-only worker priming would conceal this failure.

### [MAJOR] The durable cascade lacks integrity, retention, and partial-failure rules

- **Where:** D2, D8, R5
- **Problem:** A durable edge does not automatically make a complete or safe cancellation graph. Missing ancestors can hide live descendants; inconsistent roots can widen cancellation; “persist-only” does not resolve missing execution authorization.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle_index.go::lifecycleParentIndex.ensureWarm` skips individual unreadable records and can still mark the index warm. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go::pruneTerminalOne` removes old terminal records without checking descendants. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/cancel.go::CollectDescendantSessionIDs` returns partial results plus errors when branches cannot be read. Existing lifecycle validation does not establish all proposed edge relationships.
- **Fix:** Specify immutable parentage, generation handling, cycle rejection, root consistency, retention of required ancestor links, and handling of unreadable branches. Distinguish complete from partial Stop in logs and UI. Refuse execution when required authority cannot be established; “no audience” alone is insufficient. Prefer deriving reporting/root values over storing independently mutable duplicates.

### [MAJOR] D11 restores the direct parent instead of the cascade root

- **Where:** D11
- **Problem:** The temporary fix still loses root cancellation below the first generation.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go::LifecycleRecord` defines `ParentDurableKey` as the **direct parent**. For root → A → B, restoring B’s routing ID from that field produces A. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/steering.go::collectDescendantTurnIDs` matches the root routing ID.
- **Fix:** Resolve the actual root using validated ancestry or an explicit verified root field. Define unreadable-ancestor behavior. Test re-entry of a grandchild and deeper descendants before calling this containment effective.

### [MAJOR] Direct-parent authority contradicts the retained ancestor authority

- **Where:** D5 versus §11
- **Problem:** D5 permits actions only when the edge names the caller. §11 retains ADR-057’s ancestor ownership walk. These grant different rights to a grandparent.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate.go::verifyCallerOwnsSession` permits an ancestor found by walking `ParentDurableKey`, not just the immediate parent.
- **Fix:** Specify authority separately for steer, respond, cancel, peek, inbox, and follow-up: immediate parent, ancestors, and human operator. Reconcile D5 with §11 and require sibling/unrelated-root rejection tests.

### [MAJOR] “Every steered session” promises unsupported external-CLI capabilities

- **Where:** D5, D10
- **Problem:** A shared session model does not supply live steering or question parking to external runners.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate_followup.go::executeSteer` rejects external-CLI sessions because they do not drain the steering queue. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::messageParentToolExecute.prepareMessage` also rejects them. Corrective follow-up preserves a native session ID but creates a new external session.
- **Fix:** Define capabilities independently of creation mechanism. State which external operations remain unavailable and how follow-up preserves parentage, limits, history, and cancellation scope. Do not advertise unsupported actions under “every session.”

### [MAJOR] D6 changes completion without changing all prerequisites for completion

- **Where:** D6, WP-C
- **Problem:** Skipping Judge after a turn does not make criteria-free delegation executable. “Final answer” also lacks a precise distinction from parking, empty output, interruption, and intermediate answers while descendants still run.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/task.go::taskCreateToolExecute.validateRequest` requires criteria; `prepareContract` requires DoD items. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_assignee_readiness.go::TaskAssigneeCannotFinish` can reject a native agent denied `goal_claim`, even with no judged criteria. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_run_loop.go::finishRunTurn` has distinct error, terminal, scratchpad, and claim paths.
- **Fix:** Define an explicit completion disposition and its relationship to parked questions and outstanding children. Update creation validation, readiness, prompts, and the run loop together. Test criteria-free work with `goal_claim` denied, empty output, parking, Stop, timeout, and judged work remaining judged.

### [MAJOR] Restart and historical-session behavior can strand the steering parent

- **Where:** D1, D4, D5, §6, R5
- **Problem:** Durable history does not guarantee durable notification. The ADR does not say how failure at boot reaches the parent, or how old sessions remain history-only despite existing resume paths.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/boot_sweep.go::bootSweep` fails unpreserved sessions as interrupted. `isNeedsInputReconstructable` requires a checkpoint reference; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::parkNeedsInput` does not itself create that checkpoint. The production failure hook in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/gateway_boot.go` logs rather than waking a parent.
- **Fix:** Specify recoverable completion/failure delivery, acknowledgment and duplicate suppression, including crashes between persistence and wake. Define parked-session recovery without assuming a checkpoint exists. Explicitly prevent old-format sessions from resuming through new-format rules, or specify their conversion. Expose stranded delivery and unreadable records to operators.

### [MAJOR] D9 does not define the surviving limits

- **Where:** §2.1, D9, AC-9
- **Problem:** Concurrency is shared only under default settings; depth currently has multiple distinct constraints. Removing the block changes behavior without deciding replacements.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/admission.go::ResolveRootDelegationCap` honors positive `SubTurn.MaxConcurrent` before falling back to performance configuration. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/delegation_depth.go::resolveEffectiveDelegationDepth` combines global and per-edge depth. Task tooling separately receives a hard recursion ceiling of 10. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::getSubTurnConfig` also supplies the default execution and concurrency-wait timeouts; external tasks consume it.
- **Fix:** Provide a before/after table for all four settings, defaults, units, invalid values, and precedence. Decide whether “one concurrency source” means one number or one aggregate counter. Define timeout scope—turn, generation, or session lifetime—and its behavior on follow-up. Test differing global/edge limits and explicit concurrency overrides.

### [MAJOR] Wait-inline deletion leaves executable policy and prompt advertisements

- **Where:** D4, D10, R4, AC-8
- **Problem:** Removing `async` and `executeSync` leaves runtime-generated instructions and policy vocabulary for the removed behavior.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/delegation_context.go::buildDelegationContext` advertises `async=false` and blocking. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/config/config.go::DelegationModeAwait` defines the mode; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/coreagent/seed.go::coreAgentDelegation` seeds it. Await-specific wiring and workspace mode conversion remain. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate.go::Parameters` contains another argument described as applicable only to wait/`async=false`.
- **Fix:** Add a deletion manifest covering enums, seeds, gate wiring, mode conversion, generated prompts, synchronous-only arguments, contracts, and tests. State whether legacy policy values are rejected or retained. Verify a real generated system prompt and actual tool argument validation, not merely absence of one function.

### [MAJOR] list_jobs still relies on the old delegate registry

- **Where:** D5, D10, §8
- **Problem:** A steered session can exist durably yet disappear from actionable job discovery, or appear twice as both a task and a subagent.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs_sources.go::collectSubagentRows` filters using `ParentAgentID` and determines actionability through the live delegate session index. Labels also depend on that index. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::mintTaskLifecycleRecord` does not populate the delegate parent fields.
- **Fix:** Assign the job-discovery conversion explicitly. Define attribution, labels, actionability after restart, and deduplication across task/session categories. Add acceptance for discovering and acting on a restarted steered session.

### [MAJOR] The acceptance criteria omit decisions and permit false greens

- **Where:** §10, Appendix A
- **Problem:** D5 and D6 have no direct behavioral acceptance; D10 is covered only partially; D11 has none. AC-10 literally contradicts AC-6/7 by requiring “nothing” in the parent chat. Several criteria can pass without exercising their claimed property.
- **Evidence:** The ADR’s own trace is:

  | Decision | Existing acceptance | Missing proof |
  |---|---|---|
  | D1 | AC-1/2 | Failed writes, restart continuity, preserved ownership |
  | D2 | AC-1/3/5 | All entry paths, exclusions, restored authority/depth |
  | D3 | AC-3/4 | Direct messaging, cards, streaming, task notifications |
  | D4 | AC-8 | Runtime rejection and usable completion wake |
  | D5 | None directly | Every action, authorization, park/respond/follow-up |
  | D6 | None directly | Criteria-free completion and judged-work preservation |
  | D7 | AC-6/7/10 | Authorization, replay, reconnect |
  | D8 | AC-5 | Isolation, multiple pending descendants, partial walks |
  | D9 | AC-9 | Defaults, boundaries, semantic equivalence/change |
  | D10 | AC-2/8 partially | Shared-runner preservation and complete retirement |
  | D11 | None | Containment at deeper nesting |

  AC-2’s `grep newEphemeralSession` can pass after a rename. AC-3 can pass if nothing executes. AC-9 does not define what counts as a configuration source.
- **Fix:** Add decision-linked positive and negative acceptance. Give AC-3 a control proving the child ran and its own history/view received output. Verify history across restart for AC-2. Rewrite AC-10 as “no unsolicited child publication,” explicitly allowing authorized projection and parent-authored replies.

### [MAJOR] The parallel work packages overlap and omit central files

- **Where:** §8
- **Problem:** “Disjoint file ownership” is not true as written, and major integration work has no owner.
- **Evidence:** WP-A’s `subturn*.go` and configuration directory include tests assigned to WP-G; WP-C’s `delegate*.go` and WP-E’s frontend directory globs overlap WP-G similarly. `processSystemMessage` is owned by B but must implement A’s identity restoration and D’s cancellation identity. The table does not explicitly allocate task creation/dispatch, generic turn reconstruction, asynchronous notification, question admission, job discovery, or replay conversion.
- **Fix:** Separate production and test ownership explicitly. Name one owner for each shared file and provide dependency interfaces before parallel implementation. Add the omitted files, especially task executor, turn construction, async notifier, question registry, job discovery, and replay. Treat F as a residual audit, not an undefined owner of necessary integration work.

### [MINOR] The causal summary overstates several current deficiencies

- **Where:** §1, §2.1, D6, §4
- **Problem:** Some blanket statements erase distinctions that matter to the redesign.
- **Evidence:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::prepareProcessOptions` already inherits execution `WorkspaceID`; the missing value is durable session metadata. Native follow-up already has a resume path, although it recreates ephemeral model history. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate_run.go::delegateToolExecuteRun.persistLifecycle` uses `human` ownership for top-level delegations, not universally `parent_session`. Existing cancellation tests cover live delegated descendants, though they do not establish the re-entry case. `finishRunTurn` has exceptions to “every task” reaching the no-claim prompt.
- **Fix:** Narrow the statements to missing durable metadata, lost model-history continuity, re-entered cancellation coverage, and ordinary nonscratchpad claim behavior. Keep the #775 explanation explicitly hypothetical until reproduced.

### [MINOR] The blast-radius figures do not justify wholesale test replacement

- **Where:** §2.4, §4, R6
- **Problem:** The counting method is absent, “schemas” includes top-level documents, and matching tests are treated as obsolete even when they protect retained behavior.
- **Evidence:** Reproducible current-tree searches give:

  | Selection | Result |
  |---|---:|
  | Non-test Go under the package tree; `SessionTypeDelegate\|subagent_start\|subagent_end\|parentSpawnCallID` | **24 files**, including 3 generated files |
  | Contract YAML containing case variants of `subagent` or `delegate` | **56 files:** 54 component schemas plus 2 top-level contracts |
  | SPA TypeScript with the broad words above | **200 files** |
  | SPA TypeScript with the narrow four-token pattern | **22 files** |
  | Go test filenames beginning `subturn` or `delegate` | **50 files / 17,683 lines** |

  These do not establish the ADR’s 60-SPA or 46-test selections.
- **Fix:** Include the exact selection commands and resulting inventory. Classify tests as retain, update, or delete. Preserve cancellation, isolation, and target-identity tests rather than ordering approximately 16,700 lines rewritten wholesale.

## STRIDE summary

Here, **risk** means an unresolved design risk; **ok** means this review found no additional issue in that category, not a security certification.

| Component / data flow | S: Spoofing | T: Tampering | R: Repudiation | I: Disclosure | D: Denial of service | E: Privilege elevation | Note |
|---|---|---|---|---|---|---|---|
| Creator → session authorization | risk | risk | risk | ok | risk | risk | Different mode/self-target gates; revocation semantics unspecified |
| Durable edge → turn reconstruction | risk | risk | risk | risk | risk | risk | Missing/corrupt authority and redundant root/reporting fields lack invariants |
| Child → parent inbox/wake | risk | ok | risk | risk | risk | risk | Inbox parent key is correct; wake recipient identity is not |
| Session → messaging/media/channel stream | risk | ok | risk | risk | ok | risk | Several boundaries lack session identity or use separate suppression |
| Child → projected human viewer | risk | ok | risk | risk | risk | risk | Independent viewer authorization and subscription bounds unspecified |
| Live frames ↔ replay | ok | risk | risk | risk | risk | ok | Multi-session ordering and deduplication undefined |
| Steered session → question card/resume | risk | ok | risk | risk | risk | risk | Existing gates depend on depth and separate metadata |
| Stop → descendants and pending dispatch | risk | risk | risk | ok | risk | risk | Cancellation scope, incomplete walks, and future-turn isolation need rules |
| Boot sweep → parent failure notification | ok | ok | risk | ok | risk | ok | Logging exists; durable parent notification is absent |
| External CLI → shared runner | ok | ok | risk | risk | risk | risk | Proposed deletion removes enforcement and execution shared with tasks |

## Unasked questions

The existing open questions need these dispositions:

| Question | Assessment |
|---|---|
| **Q1 — Steering on create_task sessions** | **Sound recommendation.** Making capability depend on the creation verb would recreate the special case. Define the steering owner and capability limits first. |
| **Q2 — No Judge without criteria/DoD** | **Sound recommendation, incomplete consequence analysis.** Creation gates, readiness, prompts, parking, and terminal outcomes must change consistently. |
| **Q3 — Keep nested projection** | **Reasonable product choice; “now cheap” is unsupported.** It requires transport, authorization, and replay work. Present that cost before founder approval. |
| **Q4 — Leave existing sessions untouched** | **Sound for immutable history.** It is incomplete for queued, parked, running-at-crash, or resumable historical sessions. |
| **Q5 — Keep delegate name** | **Sound.** Ensure its generated instructions and actual capabilities agree after removal of wait-inline. |

Additional decisions needed before implementation:

1. Does delegation create a board task, or do tasks and delegation call a lower-level session launcher?
2. Which authorization mode governs unified creation, and what are the self-target and policy-revocation rules?
3. May every authenticated operator view every child, or is visibility scoped by principal/workspace?
4. May ancestors steer/respond/resume grandchildren, or only the immediate parent? What human override exists?
5. Does completion wake the parent per child, after all children, or under a specified aggregation rule?
6. What exactly distinguishes successful completion from parking, empty output, intermediate answers, and failure?
7. Does Stop cancel only currently admitted work, or also future re-entry in that generation? When does its authority expire?
8. What defaults replace the removed depth and timeout settings, and does follow-up reset a deadline?
9. Can a human directly converse with an opened steered session, and does that change its audience or steering relationship?
10. Which external-CLI capabilities remain unavailable, and how are those limits advertised?
11. What happens to incomplete old-format sessions at upgrade, and how does their parent learn the outcome?
12. What authority governs creation when the creator has no workspace?

## Claims I verified as correct

These confirmations are source-level findings, not runtime acceptance.

| Claim checked | Verified source and qualification |
|---|---|
| Native delegation and native tasks use the same turn engine | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::spawnSubTurn` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::processTaskDirect` reach `runAgentLoop`. External CLI uses a different execution path. |
| Delegation copies the originating address | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::prepareProcessOptions` copies parent channel, chat ID, sender ID, and sender display name. |
| Delegated sessions have their own persisted transcript identity | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::createChildSession` creates a separate delegate session and stamps its parent metadata. It does not stamp title/workspace. |
| Delegation has a separate bounded model-history store | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::buildDelegateAgent`, `newEphemeralSession`, `truncateLocked`, and `Save` establish a 50-message, front-truncated, nonpersistent store. The quoted “never pollute” rationale exists. |
| Emptying skips results without archive positions | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/empty_in_place.go::eligibleToolResults` skips `line < 0`. This supports the proposed mechanism, not the incident diagnosis by itself. |
| Tasks use their own supplied webchat session address and real store | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::processTaskDirect` sets webchat, task session addressing, real transcript store, and delegation depth. |
| Task creation attempts title/workspace persistence | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::createTaskSessionSync`; failures are logged rather than making creation fail. |
| Immediate task dispatch exists | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go::StartTaskNow`; it takes a task ID and dispatches asynchronously. |
| Native steering is shared engine machinery | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/steering.go::enqueueSteeringMessage` queues by scope; the native turn loop drains it. |
| Lifecycle storage already contains the named fields | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go::LifecycleRecord` contains parent key, origins, workspace, and `NeedsInput`. Task ownership distinguishes plan/human. |
| Parent inbox storage is durable and parent-keyed | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::ownerKeyFor` uses `ParentDurableKey`; message delivery uses `MessageInboxStore`. |
| Native question parking and response redispatch exist | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::parkNeedsInput` and the delegate park/respond implementation perform real lifecycle transitions and redispatch. |
| Task create/update install authorization checkers | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/task.go::SetDelegationDenyChecker` and production wiring exist. Their equivalence to delegate permissions is false, as reported above. |
| Task Stop uses the common cancellation entry | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/plan_engine.go::StopTask` reaches `cancelSessions` and `RequestCancelForSession`. |
| Re-entry restores neither parent nor cascade-root identity | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_inbound.go::processSystemMessage` sets `SendResponse: true`; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/turn.go::newTurnState` defaults routing identity to the session itself. |
| Re-entered final replies can publish | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop.go::runAgentLoop` publishes final content when `SendResponse` is enabled. |
| Media bypasses the text suppression condition | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_run_turn_tools.go::deliverToolOutput` sends media when present without the text path’s `SendResponse` guard. |
| Retry notices need audience coverage | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_run_turn_response.go::retryTimeoutError` and `retryContextOverflow` use their own publication conditions. |
| Wait-inline exists and async is the default | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate_run.go::executeSync` is blocking; `executeRun` defaults to asynchronous execution. |
| Delegate actions and switch exclusion exist | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate.go` exposes the named actions; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go::buildDelegateAgent` excludes `switch_agent`. |
| Frontend bucketing uses the frame’s primary session ID | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/store/chat/slices/frames.ts::handleFrame` computes `targetSid` from session identity, with the question-card-specific carrier where applicable. |
| ProducingSessionID exists on exactly three payloads | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/events.go`: `ToolExecStartPayload`, `ToolExecEndPayload`, and `ToolResultProjectionPayload`. |
| Delegated narration is shadowed | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_streamer.go::wsStreamer.Update` and finalization suppress nonempty `parentSpawnCallID`. |
| The four SubTurn configuration fields exist | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/config/config.go::SubTurnConfig`, including environment bindings. |
| The internal subagent channel entry currently has no production assignment | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/constants/channels.go`; other `subagent` literals include job-category values, which must not be deleted as channel uses. |
| Ordinary task runs enforce completion claims | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_run_loop.go::noClaimSteeringPrompt`; the universal wording requires the exceptions reported above. |
| Boot sweep itself does not publish to users | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/boot_sweep.go::sweepToFailedInterrupted` persists failure and invokes a hook; current production wiring logs it. |
| The narrow backend blast-radius count is reproducible | The four-token search described above returns 24 non-test Go files at the supplied checkout. |

## Claims I could not verify

- **The 80-of-80 live-session measurement and 37% context-budget incident:** production data and execution traces were not inspected.
- **#775’s actual cause:** the ring and archive-position mechanisms exist, but no incident trace or reproducer establishes causality.
- **#803 being blocked in every sense by the ring:** model-history continuity is lost, but native follow-up already exists. The stronger impossibility claim is unsupported.
- **The 60-SPA and 46-test selections:** the ADR provides no commands or inclusion lists that reproduce them.
- **“Never referenced since the foundation import”:** current-tree non-use was checked; the historical claim was not established.
- **Previous review verdicts, second-session reconfirmation, publication audit, live issue statuses, and all-ref ADR-number verification:** these are provenance/history claims beyond the inspected source.
- **Runtime effectiveness of the proposed changes:** this ADR is unimplemented. No tests were executed, so none of its acceptance criteria can be reported as passing.