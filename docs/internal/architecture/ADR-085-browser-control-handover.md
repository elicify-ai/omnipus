# ADR-085 — The operator takes the browser wheel without the turn being cancelled

- **Status:** Proposed (revision 5, after the second spec grill — see §8) — 2026-09-09
- **Relates to:** ADR-038 D6 / ADR-075 (the control-deferral gate), ADR-039/040/041 (live view, implicit control), ADR-061 (WebRTC is the only video path), ADR-057 FR-011 (a delegated child has its own transcript session), ADR-077 / Constraint #6 (per-tool policy entries)
- **Spec:** `docs/internal/specs/browser-control-handover-spec.md`

## 1. Operator direction (verbatim, 2026-09-09)

> the cancel is not good, we need a more elegant solution, like the browser tool signals the agent that the user took control, but does not cancel the turn

> we do not need the hand back button, the user has to prompt again

> [continue other work] yes it should continue other work

## 2. Evidence

A turn ended with "This turn was stopped before it finished." The operator had not clicked Stop; they took control of the live browser while the agent was driving it. The transcript records it as a deliberate user cancel:

```
type: turn_canceled, canceled_by_user: admin, canceled_by_channel: web,
cancel_method: graceful, descendants_canceled: [jim-turn-6]
```

That is today's design, stated at the top of `src/components/browser/BrowserLiveView.tsx`: the first interactive action "pauses the agent (reuses the chat store's existing `cancelStream` — the same action the chat Stop button calls)". **"Pause" was implemented as "cancel".** The turn is destroyed, the message misattributes it to the operator aborting, and ADR-040 D1 removed the explicit control toggle (asserted by `BrowserLiveView.controlToggle.test.tsx`), so there is no button to hand it back.

### 2.1 Corrections to revision 1 (established by review, verified in code)

Revision 1 asserted two things about the current system that are false. They are corrected here because the design depends on them:

- **The browser tools DO already consult the control lock.** `pkg/tools/browser/tools.go::controlledResult` calls `IsControlled` and returns a non-error deferral (`{"deferred": true, "reason": "a human is currently controlling this browser …"}`) from eleven call sites across `tools.go`, `tools_interact.go` and `tabs.go`. `live.go::IsControlled`'s own doc comment names it "the turn-coordination gate (ADR-038 D6)". The signalling this ADR asked for largely ships today.
- **Escape already releases the wheel.** `BrowserLiveView.tsx::handleKeyDown`'s Escape branch calls `releaseWheel()` → `sendControl('release')`. Revision 1 quoted the pre-fix behaviour (a local no-op) as if it were current; that was the WCAG 2.1.2 defect this branch fixed.

### 2.2 Why revision 1's mechanism was abandoned

Revision 1 proposed parking the turn via `tools.ToolResult.ParksTurn`. Review established that this cannot deliver the operator's decisions:

| # | Finding |
|---|---|
| F1 | `ParksTurn` is read by `pkg/agent/loop.go` only when a **tool call returns**. A WS `browser_control{action:"take"}` frame (`pkg/gateway/browser_ws.go::handleControl`) runs on another goroutine and has no path into the running turn's tool loop, so a click cannot park anything at click time. |
| F2 | Both existing producers (`pkg/tools/message_parent.go`, `pkg/tools/ask_user_question.go`) set the flag **only after a durable record is persisted**, rolling back if the persist fails (`askuser.Registry::CreatePending`: "a set that is not durably persisted must not park a turn"). The browser control lock is process memory — `LiveView.controller string` under `lv.mu` — with no on-disk record and no boot re-hydration. Revision 1's "the pending state is durable" was false. |
| F3 | A parked turn **ends**. `pkg/agent/goal_loop.go::checkGoalLoopAfterTurn` returns immediately on `TurnEndStatusParked` and never re-dispatches. So parking forecloses the operator's settled decision that the agent continues non-browser work. |

**Decision: do not park the turn.** The operator's three decisions are satisfied natively by the deferral gate that already exists. This dissolves F1, F2 and F3, the one-park-per-chat collision, and the goal-round question.

## 3. Decisions

### D1 — Taking the wheel neither cancels nor parks; it defers

`takeWheelIfNeeded` stops calling `cancelStream`. Taking the wheel sets the control lock and nothing else. The running turn continues.

When the agent next calls a control-gated browser tool, it receives the existing non-error deferral saying a human holds the browser. It is free to do other work and to end its turn naturally when it has nothing left. **No `turn_canceled` entry, no interrupted status, no `TurnEndStatusParked`, no `RequestCancel`.**

Consequence, stated so nobody specs otherwise: between the operator's click and the agent's next gated browser call, the agent keeps running unchanged. That is correct — nothing was blocked until it tried to drive.

### D2 — The deferral tells the agent what to do, and the gate covers every reachable tab set

Three changes to the existing gate, and nothing else:

1. **Wording.** The reason says explicitly not to retry until control returns, and that the operator resumes by sending a message.
2. **Bounded attempts.** After N deferred attempts in one turn (N=3 unless the spec justifies otherwise), the agent is told to state plainly what it is waiting for and stop attempting browser work for the remainder of the turn.
3. **Coverage — the real gap.** The gate is evaluated per resolved `(BrowsingKey, TabOwner)` pair. The panel takes the lock on `manager.PanelTabSetID(chatSessionID)`, while a tool resolves its owner from `tools.ToolTranscriptSessionID(ctx)`. Under ADR-057 FR-011 a delegated child has its **own** transcript session, so a delegated agent driving its own tab set sees `IsControlled == false` and keeps clicking while the operator holds the parent's wheel. The gate must be evaluated against every tab set reachable from the chat whose wheel was taken.

### D3 — Resume is the operator's next prompt; the agent re-orients by looking

No hand-back control is added; ADR-040 D1 stands. The operator's next message is an **ordinary turn** — there is no resume dispatcher, no injected page state, and no memory of the handover beyond the conversation itself. Revision 1's "the resume dispatch carries the current page state" named an artefact with no writer and no reader; it is deleted.

The agent re-orients by calling `browser_screenshot`, whose description already states that it reports the tab's current URL and title "including a page the user navigated to themselves via the live browser panel (the tab is shared)" and instructs the model to read the URL from the tool rather than infer it.

**Releasing the lock is not a resume.** Escape, entering annotate mode, closing the panel and a WS disconnect all release the lock without resuming anything. The agent must not silently resume driving in that state; the waiting surface (D6) stays visible until a prompt arrives.

### D4 — Release is explicit, server-side, and ghost-proof

Today the lock happens to release on the next prompt through the client-side auto-release effect (`effectiveAgentWorking && isControlling → sendControl('release')`). D8 disarms that effect, so the release must be made explicit or the operator holds the wheel forever and the agent is locked out of a browser nobody is driving — the stale-lock class `EnsureControlForInput`'s doc comment already warns about.

- On accepting a chat turn for a session whose panel lock is held, the **gateway** releases it (`LiveViewRegistry` release for the holder). Server-side, because the operator may have closed the panel.
- A lock whose holder is no longer among the live viewers is **void**; the existing ghost-steal path must be reachable rather than leaving the agent deferred forever.
- Ownership is the `viewerID` holding `LiveView.controller`. Any authenticated user's prompt on that session releases it, audited with the acting user, not the holder.
- **Unchanged constraint:** input dispatch stays ungated (operator directive, 2026-08-03). The lock is authoritative for **tool deferral and presentation only**, never an authorization decision on `dispatchInput`. A second viewer driving without the lock neither defers the agent nor blocks it.

### D5 — Capture stops while the operator holds the wheel

The read-only capture tools — `browser_screenshot`, `browser_get_text`, `browser_snapshot` — are deliberately ungated today ("they don't inject input"). Under D1 the agent keeps running, so it can screenshot the operator's login form while they type into it. **D1 makes this worse, so D5 is required, not optional.**

While the operator holds the wheel, those three join the deferral gate and return the same non-error result. Gating the tool is the only point that stops all three of `browser_screenshot`'s sinks at once: the JPEG written under the turn's working directory, the MediaStore entry created from its returned data URL, and the `[file:…]` artifact tag.

Revision 1's "frame persistence" is deleted — nothing records live-view frames (ADR-061 removed the JPEG screencast; WebRTC is the only path and is never recorded). The operator's own view is unaffected, because WebRTC is independent of these tools. Captures taken **before** the take-over are not retracted; that is accepted and stated.

### D6 — The waiting state is visible, and the agent says it

Because the turn is not parked (D1), the agent can still speak — unlike a parked turn, which returns from inside the tool loop with no further model call. The deferral's wording (D2) drives the agent to say what it is waiting for.

That is not sufficient on its own, because the deferred tool call may be hidden from the thread. A system-authored line is emitted when the operator takes the wheel, stating that the agent has stopped driving the browser and that sending a message returns it. Precedent: `pkg/agent/goal_loop.go`'s system-transcript writer.

### D7 — Agent-initiated handover is a new tool with its own policy entry

`browser_handover(reason)`, used when the agent reaches a sign-in or anything it should not do on the operator's behalf. It passes the lock to the operator, emits the D6 surface, and returns a result instructing the agent to conclude its turn with an explanation. It does **not** park.

Revision 1 claimed this needed "no new policy entry". That was wrong: every browser verb in this codebase is a distinct `tools.Tool` with its own name and its own entry in `pkg/config/defaults.go`. Per Constraint #6 / ADR-077 the new tool requires an entry in `coreagent::allStaticToolNames`, a seeded default in `defaults.go` (`allow`, matching the family), and per-agent seeding — otherwise `ReconcileToolPolicyCeiling` has nothing to reconcile and `ValidateToolPolicyCoverage` fires as genuine internal drift.

### D8 — The client take-over state is rebuilt around the lock, not around cancel

`takeWheelIfNeeded` calls `cancelStream` first because `isStreaming` does not flip synchronously; `agentPausedByUserRef` is the local override that makes the take work in one click. Removing the cancel removes that trigger, and without a replacement the take-over silently reverts: `effectiveAgentWorking` stays true, `computeDriveMode` keeps `agent-working`, `canDispatchInput` stays false so the operator's clicks do nothing, and the auto-release effect fires and revokes the lock they just took.

Replacement: an explicit **operator-holds-wheel** state set the instant `sendControl('take')` is sent and confirmed by the server's `controlling` ack. `computeDriveMode` must let it beat `agentWorking`, and the auto-release effect must be gated on it so it can never revoke a lock the operator took while a turn is in flight.

### D9 — Stop still cancels

The Stop button and `/cancel` are unchanged. Only the take-the-wheel path changes.

### D10 — Audit

A take that causes the agent to defer is a materially different event from an ordinary take. It is audited with session id, turn id, viewer id, user, tab set, and the tool that was deferred, alongside the existing `auditControl` / `auditRelease` records.

### D11 — Delegated agents

`pkg/agent/subturn.go` propagates a child's park onto the parent, and `pkg/tools/delegate.go` honours it. Under D1 nothing parks, so there is no cascade: a delegated agent driving a taken browser simply defers (D2.3 makes that reachable), reports it to its parent through its normal result, and the parent decides. This is stated because revision 1's park would have cascaded a stop up the whole ancestry with no resume path.

## 4. Consequences

- A turn survives the operator taking the browser. Nothing is destroyed and nothing is recorded as an abort.
- The agent genuinely continues non-browser work, because the turn never ended.
- No durable handover record is introduced, and none is needed: the operator's next prompt is an ordinary turn. A gateway restart while the operator holds the wheel loses only the lock, which is already true today.
- Credentials typed during a takeover no longer reach the transcript.
- An agent with nothing non-browser to do will end its turn saying it is waiting. That is correct and visible.
- The `AskUserQuestion` composer lock and the browser lock no longer interact: with no browser park there is no second pending state, so the deadlock revision 1 would have created (card locks the composer, operator cannot prompt to resume) does not arise.

## 5. Out of scope

- Reinstating an explicit control toggle (ADR-040 D1 stands; operator reconfirmed).
- Changing Escape-in-frame behaviour (it releases the wheel; correct today).
- Making the lock an authorization decision on input dispatch (operator directive, 2026-08-03).
- **`tools.browser.take_control_enabled: false`** — with take-control disabled no take ever succeeds (`handleControl` refuses and audits `take_control_disabled`), so none of this ADR is reachable on such an install. Stated so no implementer specs a dead path.
- The Judge's stance (ADR-084).

## 6. Revision 3 — corrections found while writing the spec

Spec authoring surfaced three further errors in revision 2. They are corrected here; the spec (`docs/internal/specs/browser-control-handover-spec.md`) implements the corrected form.

### R3-a — D5 cannot simply add the capture tools to the existing gate

Calling `controlledResult` is not free-standing. ADR-075 §14 rule 3 establishes a **biconditional** that three tests enforce: `pkg/tools/browser/control_gate_membership_test.go::declaredControlGateExemptions` lists the three capture tools as reasoned exemptions and asserts that roster equals `pkg/tools/browser/audit.go::readOnlyBrowserTools`; `audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet` and `lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased` close the loop as *leased ⟺ gated*.

Taken literally, D5 would make `browser_screenshot`, `browser_get_text` and `browser_snapshot` **write-class, per-call audited and write-leased**, so a screenshot would serialise behind an in-flight click through `lease.go::acquireWrite`. That is a real behaviour change nobody asked for.

**Correction.** The classification becomes three-way — **action**, **capture**, **exempt** — where capture tools are control-gated but neither write-leased nor audited as write-class. ADR-075 §14 rule 3's biconditional is amended accordingly, explicitly and in writing, rather than being silently broken by adding a call site.

### R3-b — D6's waiting surface has no wire path

Revision 2 cited `pkg/agent/goal_loop.go`'s system-transcript writer as precedent. That writer only calls `AppendTranscriptStrict`; it emits **no frame**. `contracts/asyncapi.yaml`'s chat channel has no system/notice frame, and `NotificationFrame`'s `notification_type` enum is `[schedule_failed]`, targeting the header notification centre rather than the thread.

So as written the "visible waiting surface" would be invisible until a reload — precisely the silence D6 exists to remove.

**Correction.** D6 requires a **new contract-first wire type** for an in-thread system notice, added through Constraint #8's five-step process (schema, spec reference, `scripts/gen-contracts.sh`, generated Go and TS committed together, then consumers). The transcript entry remains, so the line survives a reload; the frame is what makes it appear immediately.

### R3-c — D4's release is under-scoped, and D7's "passes the lock" is undefined

**Release.** Prompts converge at three or more independent publish sites (`pkg/gateway/websocket.go`'s webchat handler, `pkg/gateway/sse.go`, and every non-web channel). A webchat-only release leaves a prompt arriving from Telegram on the same session not releasing the lock — reproducing exactly the stale-lock class D4 exists to close. The release must be applied where prompts converge on the session, not on one transport.

**Handover.** `LiveView.controller` holds a `viewerID`. When no panel is open there is no viewer to grant it to, and a holder that never attached is precisely the ghost D4 declares **void**. So `browser_handover` cannot be modelled as "pass the lock" without contradicting D4.

**Correction.** An agent-initiated handover is a distinct **handover-pending** state on the session, not a lock grant: the agent stops driving and the surface (D6) invites the operator in. The lock itself is taken by whoever actually attaches and clicks, through the ordinary take path. The pending state clears on the same triggers as D4's release.

### R3-d — Two carriers the spec had to route around (not ADR errors, but load-bearing)

- **D2.3's cross-tab-set coverage** has an exact existing carrier, `turnState.routingSessionID` (root-inherited per ADR-057 FR-011), but it sits behind a **closed consumer set** enforced by `routing_session_id_consumer_set_adr057_test.go`. Using it is a deliberate allowlist amendment, made in the open, not an incidental read.
- **D2.2's per-turn attempt bound** has no carrier in the tool layer (`pkg/tools/base.go` has no turn id). The counter lives on `turnState`, modelled on `pkg/agent/tool_denial.go::turnDenialLedger`.

## 7. Revision 4 — corrections found while grilling the spec

Four corrections, all verified in code at `0b7d4933`. Three amend decisions (D4, D5, D10); one is a
mis-citation in §6 itself. The spec (`docs/internal/specs/browser-control-handover-spec.md`)
implements the corrected form.

### R4-a — D4's release must fire on OPERATOR-ORIGINATED prompts only (amends D4)

R3-c correctly said the release "must be applied where prompts converge on the session, not on one
transport". Read literally that means `bus.MessageBus.PublishInbound`, and **that is not safe**: it is
the convergence point for synthetic traffic too. `pkg/agent/async_notifier.go` publishes a
`bus.InboundMessage` on the `"system"` channel for **every background tool or delegate completion**,
and `pkg/agent/loop.go` re-injects goal-loop follow-ups with
`Sender.CanonicalID == goalLoopFollowUpSenderID`. A release wired at the bus therefore means a
delegate finishing in the background **hands the browser back while the operator is typing a password
into it** — reintroducing, through the release path, exactly the exposure D5 exists to close.

**Correction.** The release fires from one shared helper invoked at the **four operator-originated
publish sites**: `pkg/gateway/websocket.go` (webchat), `pkg/gateway/sse.go`,
`pkg/gateway/ws_ask_user.go::DispatchResume` (for a genuine human answer only — an auto-defaulted,
timed-out card is not a prompt), and `pkg/channels/base.go::HandleMessage`. It is never invoked from
the bus, from the async notifier, or from the goal loop. The partition is pinned by a structural test
so a publish site added later must classify itself deliberately. Heartbeat, cron and task runs never
construct an `InboundMessage`, so they are excluded already. See spec FR-029 / FR-029a.

**Attribution, also under-specified in D4.** "audited with the acting user, not the holder" presumes
a carrier that mostly does not exist: `bus.InboundMessage.GatewayUserID` is set **only** on the
webchat WS path (and the question-card resume), and its own doc comment records that
"channel/task/scheduled inbound messages never set this field". For a channel-originated prompt the
audit records `Sender.CanonicalID` as the **actor** and leaves the user field **empty** — a platform
handle is not a gateway principal. The holder is recorded only as the *prior* holder, never as the
actor. See spec FR-030.

### R4-b — D4's release is not sufficient on its own: a held wheel must expire (amends D4)

D3 and D4 together make the operator's next prompt the **only** release. D4's ghost rule voids a
holder who has left `LiveView.viewers`; an **attached but idle** holder is not a ghost. Under R4-a a
cron or heartbeat turn is deliberately not a prompt. The consequence is that an operator who takes
the wheel and closes their laptop **permanently disables every scheduled browser turn on that
session**, with no expiry, no visible state and nothing to recover — a product-level hole rather than
an implementation detail.

**Correction.** A hold with no viewer input and no attach/detach activity for a configured idle
window (`tools.browser.control_idle_release`, shipped default 900 seconds; `0` disables) is released
server-side and audited as `browser_control_idle_release`, with an operator-visible line. D7's
handover-pending state expires on the same timer, for the same reason. See spec FR-031a.

### R4-c — R3-a cites a section that does not exist (corrects §6, not a decision)

R3-a says the biconditional is "ADR-075 §14 rule 3". **ADR-075 has no §14.** The rule is a *spec*
rule and lives in `docs/internal/specs/browser-workspace-ownership-spec.md` — **§14.2 Rules, rule 3**
(the per-tool table), with **FR-019a** and **AC5** carrying it and **§12 A17** carrying its reasoning
— and is **restated** in `docs/internal/specs/browser-agent-capability-spec.md`. D5's three-way
correction therefore amends **two spec documents in the same commit**, not an ADR section. Nothing
about the substance of R3-a changes; only where the edit lands. `browser_handle_dialog` stays exempt
from both gates on A17's original reasoning. See spec FR-035a.

### R4-d — D10's audit field set names a turn id that does not exist (amends D10)

D10 requires the deferral record to carry "session id, **turn id**, viewer id, user, tab set, and the
tool that was deferred". There is **no turn id in the tool context** — `pkg/tools/base.go` exposes
`ToolCallID`, `ToolTranscriptSessionID`, `ToolSessionKey` and `ToolAgentID` and nothing else. This is
the same absence R3-d records for the attempt counter, applied to the audit record.

**Correction.** Turn id is dropped from the field set and replaced by the **root chat session id**,
which is already being routed to the tool layer for D2.3's coverage fix, is stable across a whole
delegation subtree, and answers the question an auditor asks ("which conversation was blocked?").
`ToolCallID` is recorded alongside it to pin the individual call. **The record is also emitted from
the wrong place in D10 as written:** it must come from the deferral path in `pkg/tools/browser`, not
from the take handler in `pkg/gateway/browser_ws.go` — at take time no tool has deferred yet and the
handler cannot know which one later will. See spec FR-061.

## 8. Revision 5 — corrections found while grilling the spec a second time

Five corrections, all verified in code at `5622756b`. **Three of them are defects revision 4's own
fixes introduced** — which is the reason they are recorded here in full rather than folded quietly
into §6 or §7. Two amend decisions (D3, D4); one supplies a mechanism a decision promised and never
had; one corrects an attribution rule; one corrects a liveness definition. The spec
(`docs/internal/specs/browser-control-handover-spec.md`) implements the corrected form.

### R5-a — R4-a named a release site that cannot be built (amends D4, corrects §7)

R4-a placed the release helper at four publish sites, one of them
`pkg/channels/base.go::HandleMessage` calling into `pkg/gateway`. That call cannot exist, for two
independent reasons:

- **Import direction.** `pkg/gateway/gateway.go` imports `pkg/channels`; nothing under `pkg/channels`
  imports `pkg/gateway`. A call the other way is an import cycle.
- **No session identity at that site.** The `bus.InboundMessage` built there carries `Channel`,
  `InstanceID`, `Sender`, `ChatID`, `Content`, `Media`, `Peer`, `MessageID`, `MediaScope`, `Metadata`
  and `UserInitiated` — and **not `SessionID`**. The lock is keyed on `PanelTabSetID(chatSessionID)`,
  and that id is resolved downstream in `pkg/agent`. The helper would have nothing to release
  against.

**Correction.** Split the decision from the action. The **decision** stays at the publish site,
carried on a new fail-closed field `bus.InboundMessage.OperatorPrompt bool`, documented in the same
form as the `UserInitiated` field beside it: set `true` at the operator sites and nowhere else, so a
publish site added later fails closed. The **action** fires once the message has been resolved to a
session — `pkg/agent/loop.go::processMessage` — through a **gateway-registered hook**
(`AgentLoop.SetBrowserWheelReleaseHook`, modelled on the existing `AgentLoop.SetReloadFunc`),
registered inside `pkg/gateway/browser_ws.go::newBrowserWSHandler`, which is already handed the
`*agent.AgentLoop`. A gateway implementation is necessary, not merely convenient: the release must
also push R5-c's frame to the holder's WS connection and emit through `browser_ws.go`'s audit record
shape, neither of which `pkg/agent` can reach.

`UserInitiated` itself must **not** be reused as the discriminator. It is false on the SSE path
(`pkg/gateway/sse.go` builds its message without it), so keying on it silently reproduces the same
stale-lock hole for SSE clients; widening it would change goal/loop origin gating (ADR-049 Gap #8) as
a side effect of a browser change; and it is *true* on the question-card path, which R5-b now needs
to be false. Revision 4's refusal to reuse it was correct and stands.

**One more trap, stated because the obvious placement is the wrong one.** §7 asserted that
"heartbeat, cron and task runs never construct an `InboundMessage`". That is **false**:
`pkg/agent/loop.go::ProcessDirectWithChannel` builds one with `Sender.CanonicalID == "cron"` and
hands it straight to `processMessage`. It merely never **publishes** it. The conclusion survives —
those runs still do not release — but the *reason* does not, and the difference matters: an
implementer trusting the stated reason could place the release at `processMessage`, the real
convergence point, and gate it on anything other than `OperatorPrompt`, releasing the wheel on every
cron fire. The gate is the field, not the location. See spec C7.2 / FR-029 / FR-029a.

### R5-b — A question-card answer or cancel does NOT release the wheel (amends D4, reverses spec A13)

Revision 4 made `ws_ask_user.go::DispatchResume` a conditional release site, gated on that file's
existing `resumeIsUserInitiated(set)` predicate, so that a timed-out auto-default would not release
while a real answer would. **The predicate's first branch is
`if set.Status == askuser.StatusCancelled { return true }`.** It is correct for its own caller — for
goal/loop origin gating, a human pressing Cancel *is* a human acting — and completely wrong here:
under revision 4, **clicking Cancel on a question card would have handed the browser to the agent
while the operator was driving it.**

**Correction (operator-ratified).** A question-card resume never releases the wheel — answer or
cancel. The release set becomes three sites, not four: `websocket.go`, `sse.go`,
`channels/base.go`. The no-release set becomes `ws_ask_user.go`, `async_notifier.go`, `loop.go`.
`resumeIsUserInitiated` stays exactly as it is and MUST NOT be read by this feature.

The decision does not rest on the bug alone. Three independent reasons:

1. **Harm asymmetry.** Releasing mid-drive reinstates the whole D5 exposure — the agent resumes
   capturing a page a human is typing into. Not releasing costs one deferred round-trip, which is
   visible to the operator and bounded by the N=3 attempt ceiling.
2. **A card answer is not an ordinary turn.** `DispatchResume` injects a *synthesised* `resumeText`
   into a turn that is already parked. The operator composed nothing; D4's rule is about a prompt the
   operator wrote.
3. **The composer lock is a reason to keep the two states independent, not to fuse them.** §4 already
   claims the `AskUserQuestion` lock and the browser lock no longer interact. Making one release the
   other is the interaction, reintroduced.

The spec's Edge Cases line restoring "the composer lock and the wheel are independent" is part of
this correction, and the earlier test that asserted a human answer *does* release is replaced rather
than narrowed.

### R5-c — No server-initiated release ever reached the holder's own panel (amends D4)

Every release the server performs on the holder's behalf was invisible to that holder.
`LiveView.releaseControl` broadcasts through `snapshotControlSinksExceptLocked(viewerID)`, which
excludes the acting viewer **by construction**; `ControlSink`'s doc comment states that exclusion as
an invariant, on the grounds that the acting viewer "already gets an authoritative `browser_status`
frame as the direct response to its own `browser_control` request". That frame is emitted **only**
inside `pkg/gateway/browser_ws.go::handleControl`'s `case "release"` — i.e. only as a reply to the
holder's own request.

So after a prompt release, an idle expiry, a handover clear, a take-control switch-off or a session
deletion, the panel keeps `isControlling === true` indefinitely. The client clears that state only on
a server `released` status that never arrives, and `takeWheelIfNeeded`'s first guard is
`if (controllingRef.current) return` — so **the operator can never re-take while the agent drives.**
Both parties believe they hold the wheel: the exact state ADR-039/040 exist to prevent, and a
strictly worse version of it than the one those ADRs fixed, because here the disagreement is between
the operator and the server rather than between two viewers.

**Correction.** Any release **not** originated by the holder's own `browser_control` frame must
notify the still-attached former holder directly — an unsolicited
`browser_status{state:"released"}` pushed to that connection, in addition to the existing broadcast
to everyone else. It must **not** carry `control_only`: that flag makes the SPA apply only the
control-ownership axis and return, so a `control_only` frame would look delivered and change nothing.
`ControlSink`'s doc comment is amended in the same commit, since it currently asserts the opposite as
an invariant. See spec FR-031b.

### R5-d — D3's "releasing the lock is not a resume" had no mechanism (amends D3)

D3 states that Escape, annotate mode, closing the panel and a WS disconnect "all release the lock
without resuming anything", and that "the agent must not silently resume driving in that state". As
specified through revision 4, **nothing implemented that.** Those paths clear `lv.controller`, so the
gate re-opens on the very next call: with attempts left under the N=3 bound, or on any new turn (a
cron fire, a heartbeat, a goal follow-up), the agent drives the page the operator walked away from.
The waiting line meanwhile says "sending a message returns it", which is false the moment the gate
re-opens.

**Correction.** A **stand-down latch** — a flag on the live view alongside the control lock — is set
on every transition into a stood-down state (a human take, or an agent handover), consulted by the
tool gate alongside the lock under the same two-key reachability evaluation, and cleared by exactly
two things: the operator's next prompt (R5-a's release) and the idle expiry (R4-b's timer). Escape,
annotate mode, closing the panel and the end of a turn do not clear it.

**One exception, and it is load-bearing:** the latch is **voided** by D4's ghost rule. A holder that
left `LiveView.viewers` released nothing deliberately, so nothing deliberate is preserved — without
this, a crashed panel would stand the agent down forever and the ghost rule would be silently
repealed. The rule that keeps this coherent: *the latch records a deliberate stand-down, and a
deliberate stand-down requires a deliberate release.*

The alternative considered and rejected was to amend D3 the other way — releasing the lock *does*
re-open the gate, only the turn is not re-dispatched. Cheaper, but it makes the waiting line lie, and
the exposure it reopens is D5's. If it is ever taken instead, D3's paragraph, the spec's US-7 AS-5,
its matching edge case and its BDD scenario must all be deleted in the same pass. Leaving a promise
with no mechanism is precisely how this defect arose. See spec FR-026a.

### R5-e — R4-b's idle timer measured the wrong thing (amends D4 / corrects §7 R4-b)

R4-b defined the expiring hold as one with "no viewer input and no attach/detach activity" for the
idle window. **A reading operator produces no input events.** Under that definition, an operator who
takes the wheel to read a page is released after 900 seconds and the agent resumes driving — and,
before R5-c, resumes *silently*, with the panel still claiming they are in control. WebRTC is
streaming to them the whole time and was not counted at all.

**Correction, three parts.**

1. **Liveness.** While a viewer is attached, the window is reset by input, by an attach or detach, by
   a `BrowserManager.ViewerHeartbeat` stamp (the live panel's WS pong — the existing "somebody is
   still watching" proof), or by an active media track. A stood-down state with **no attached
   viewer** — a handover nobody came to, a latch left behind after the operator closed the panel —
   has no liveness signal by construction, and runs its window from the transition. That is the
   abandoned-laptop case R4-b was written for, and now the only case it fires on.
2. **Delivery is eager.** A registry-level sweeper on a coarse tick, not a lazy check inside the tool
   gate. R4-b requires an operator-visible line on expiry, and a lazy check would never emit it when
   no agent is running — which is exactly when it matters.
3. **The existing tab reaper.** `tools.browser.idle_ttl` already reaps idle tabs and whole browsing
   contexts, and `BrowserManager.ReapIdleSessions` **does not touch any `LiveView`** — so a reaped
   tab set would leave a live lock, handover flag and latch on a view whose tabs are gone. A tab set
   whose live view is controlled, handover-pending or latched is exempt from that reaping; a teardown
   for any other reason routes through the same audited release path.

The sweeper is also what makes `take_control_enabled: false` safe to flip on a running install. §5
scopes the disabled case as "no take ever succeeds", which is true of a *fresh* install; flipping the
flag while a wheel is already held leaves the hold in place, and with the panel closed there is then
no release path at all, because `handleControl`'s `case "release"` is the only other one and it needs
an attached operator. The sweeper releases such a hold on its next tick and runs regardless of the
flag — so no config-reload hook is needed. See spec FR-031a / FR-052.
