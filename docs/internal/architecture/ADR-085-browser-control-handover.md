# ADR-085 — The operator takes the browser wheel without the turn being cancelled

- **Status:** Proposed (revision 2, after adversarial review) — 2026-09-09
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
