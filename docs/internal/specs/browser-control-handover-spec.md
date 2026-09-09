# Feature Specification: Browser control handover without turn cancellation

**Created**: 2026-09-09
**Status**: Draft
**Source of truth**: [`docs/internal/architecture/ADR-085-browser-control-handover.md`](../architecture/ADR-085-browser-control-handover.md) **revision 2**
**Branch / base commit**: `feat/adr-081-work-first-goal` @ `fa737b4f`
**Written non-interactively** — every point where the `plan-spec` skill would have stopped for
operator confirmation is recorded in [Assumptions & Ambiguity Warnings](#assumptions--ambiguity-warnings)
instead. Nothing below has been ratified by the operator.

---

## 0. Corrections to ADR-085 revision 2 — READ FIRST

Revision 2 fixed the two false premises revision 1 carried. Grounding its decisions against the
code at `fa737b4f` surfaces seven more places where the ADR is either wrong about the current
system or silent on something an implementer cannot proceed without. **Two of them (C1, C2) are
blockers: an implementer who follows the ADR literally will produce a build that either fails
three structural tests or ships an invisible feature.**

### C1 — BLOCKER. D5 collides with the §14 rule 3 biconditional and three structural tests

D5 says the three read-only capture tools "join the deferral gate". In this codebase, calling
`controlledResult` is not a free-standing property — it is one half of a **biconditional that
three tests enforce**:

- `pkg/tools/browser/control_gate_membership_test.go::declaredControlGateExemptions` lists
  `browser_screenshot`, `browser_get_text` and `browser_snapshot` as declared, reasoned exemptions,
  and the same test asserts that this roster and `pkg/tools/browser/audit.go::readOnlyBrowserTools`
  are "the same set stated twice — 'not gated' and 'not audited per call' are one classification
  under §14 rule 3".
- `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet` asserts
  `writeClassBrowserTools` equals the set of tools that call `controlledResult`.
- `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased` asserts,
  per §14's biconditional, that a tool is write-leased **iff** it is `controlledResult`-gated.

Followed literally, D5 therefore makes `browser_screenshot` / `browser_get_text` /
`browser_snapshot` write-class, per-call audited, **and write-leased** — so a screenshot would
serialize behind an in-flight `browser_click` on the same tab set
(`pkg/tools/browser/lease.go::acquireWrite`). The ADR intends none of that, and a leased
screenshot is a behaviour regression, not a safety property.

**This spec's resolution:** replace the two-way classification with a **three-way** one —
**action** (gated + leased + per-call audited), **capture** (gated, NOT leased, NOT per-call
audited), **exempt** (none of the three). §14 rule 3's biconditional is narrowed to
`leased ⟺ action-class`, and a second biconditional is added: `control-deferred ⟺ action ∪ capture`.
See FR-030 – FR-035 and W1 in the wave plan. **This is an amendment to ADR-075 §14 that ADR-085
does not currently record and should.**

### C2 — BLOCKER. D6's waiting surface has no wire path; the cited precedent is transcript-only

D6 cites `pkg/agent/goal_loop.go`'s system-transcript writer as precedent. That writer
(`writeGoalSystemTranscript`) does exactly one thing: `AppendTranscriptStrict` of a
`session.EntryTypeSystem` entry. It emits **no frame**. A transcript entry is visible on replay
(`pkg/gateway/replay.go` handles `EntryTypeSystem`), i.e. after the operator reloads or re-attaches
the session — *not* live in the open thread.

The chat channel has no frame that could carry it: `contracts/asyncapi.yaml`'s chat-channel message
list contains no system/notice frame, and `NotificationFrame`'s `notification_type` enum is
`[schedule_failed]` with the header notification center as its destination, not the thread.

So as written, D6 ships a "visible waiting surface" that is invisible in the session the operator
is looking at. **Resolution:** a new contract-first wire type is required (Constraint #8, the
5-step process), paired with the transcript entry so live and replay agree. See FR-036 – FR-039
and W4.

### C3 — CORRECTION. D2.3's coverage fix has an existing carrier, and using it amends a closed set

The ADR describes the delegated-child gap correctly but names no carrier for "the chat whose wheel
was taken". One exists and is exactly right: `pkg/agent/turn.go::turnState.routingSessionID` is
ADR-057 FR-011's routing identity, "inherited VERBATIM through an entire delegation subtree — for a
grandchild it equals the ROOT's own session id". That is precisely the id the panel passed to
`BrowserManager.PanelTabSetID`.

**But** its doc comment declares a **CLOSED CONSUMER SET** (ADR-057 FR-014) policed by
`pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed`.
Adding the browser gate is a deliberate allowlist amendment with a named reader, not a free read.
The new read is squarely role-B (routing/scope), so the amendment is defensible — it just has to be
made explicitly, in the same commit, with the test updated.

### C4 — CORRECTION. D2.2's "per turn" cannot be counted in the tool layer

There is no turn identifier in the tool context: `pkg/tools/base.go` exposes `ToolCallID`,
`ToolTranscriptSessionID`, `ToolSessionKey`, `ToolAgentID` — no turn id. A counter kept in
`pkg/tools/browser` keyed on the transcript session would leak across turns of the same session.

The correct precedent is one layer up and already exists: `pkg/agent/tool_denial.go`'s
`turnDenialLedger` on `turnState`, with `turnState.recordToolDenial` — an aggregate per-turn count
plus a per-tool "already permanently denied, short-circuit for the rest of the turn" map. **The
bounded-attempt counter belongs in the engine, on `turnState`, modelled on that ledger**, keyed off
the structural `{"deferred": true}` marker `controlledResult` already emits. See FR-013 – FR-017.

### C5 — CORRECTION. D7's "passes the lock to the operator" is undefined with no viewer attached

`LiveView.controller` is a **`viewerID`** (`pkg/tools/browser/live.go`). When the agent calls
`browser_handover` there may be no attached viewer at all, so there is no viewer id to grant. Worse,
a holder that is not in `lv.viewers` is precisely the **ghost** that
`LiveViewRegistry::EnsureControlForInput` is designed to steal from and that D4 declares **void** —
so an agent-initiated handover cannot be expressed as a control-lock holder without contradicting D4.

**Resolution:** model agent-initiated handover as a distinct, explicitly-set state on the live view
(a handover-pending flag), separate from the viewer control lock. It defers tools like a held lock
does, is cleared by the same next-prompt release path, and is *not* subject to the ghost rule. See
FR-040 – FR-045. **The ADR should record this; today it under-specifies the mechanism.**

### C6 — CORRECTION. The ungated set is six tools, not four

`controlledResult`'s own doc comment names three (`browser_screenshot`, `browser_get_text`,
`browser_wait`) and the task brief named four. The authoritative roster
(`declaredControlGateExemptions`) is **six**: `browser_list_tabs`, `browser_screenshot`,
`browser_get_text`, `browser_wait`, `browser_snapshot`, `browser_handle_dialog`. After D5 it is
three (`browser_list_tabs`, `browser_wait`, `browser_handle_dialog`), with the other three moving
to the new capture class. `browser_handle_dialog` is exempt for a *different* reason (recovery verb;
gating it would deadlock a wedged tab) and must stay exempt.

### C7 — CORRECTION. D4's "accepting a chat turn" is more than one entry point

Webchat prompts converge at `pkg/gateway/websocket.go`'s message handler
(`bus.InboundMessage{Channel: "webchat", …}` → `PublishInbound`). SSE has its own publish site
(`pkg/gateway/sse.go`), and every non-web channel publishes independently. A release wired only into
the webchat handler leaves a Telegram or Slack prompt on the same session **not** releasing, which
reproduces exactly the stale-lock class D4 exists to close. This spec requires the release at the
single point every channel converges on. See FR-029 and Ambiguity A3.

---

## Existing Codebase Context

> GitNexus was not queried for this spec; the analysis below is from direct `Read`/`Grep` at
> `fa737b4f`. That fallback is sanctioned by the skill when the graph does not cover the question —
> here the questions were exact-text ones (which call sites, which roster members), not structural.

### Symbols Involved

| Symbol | Role | Note |
|---|---|---|
| `pkg/tools/browser/tools.go::controlledResult` | modifies | The deferral gate. Reason text, and the class split (C1). |
| `pkg/tools/browser/live.go::LiveViewRegistry.IsControlled` / `.Controller` | calls | Lock read. Process memory only, no persistence, no boot rehydration. |
| `pkg/tools/browser/live.go::LiveView.takeControl` / `.releaseControl` / `.ensureControlForInput` / `.detach` | extends | Lock lifecycle; `detach` clears on clean close only. Handover-pending flag lands here (C5). |
| `pkg/tools/browser/register.go::resolveTurn` / `resolveTurnScope` / `resolveTurnTabSet` | modifies | The one path every browser tool starts with; where the root-chat key must also be resolved. |
| `pkg/tools/browser/manager.go::PanelTabSetID` / `OperatorSessionID` / `focusedTabSet` | calls | Panel-vs-agent tab-set mirror. The D2.3 gap lives in the seam between the first two. |
| `pkg/tools/browser/audit.go::writeClassBrowserTools` / `readOnlyBrowserTools` / `recordBrowserAction` | modifies | Classification (C1). |
| `pkg/tools/browser/lease.go::acquireWrite` | unchanged | Must NOT gain capture-class members (C1). |
| `pkg/gateway/browser_ws.go::handleControl` / `auditControl` / `auditRelease` / `detach` | modifies | Take/release wire path; `TakeControlEnabled` refusal. |
| `pkg/gateway/websocket.go` message handler (`PublishInbound` site) | extends | Candidate release trigger (C7). |
| `pkg/agent/turn.go::turnState.routingSessionID` | calls | Root-chat identity for D2.3 (C3). Closed consumer set. |
| `pkg/agent/tool_denial.go::turnDenialLedger`, `turnState.recordToolDenial` | pattern | Model for the bounded-attempt ledger (C4). |
| `pkg/agent/goal_loop.go::writeGoalSystemTranscript` | pattern | System-entry writer; transcript only (C2). |
| `pkg/coreagent/core.go::allStaticToolNames` + per-agent seeds (`IDJim`, `IDRay`, `IDExplorer`, `IDResearcher`) | modifies | Constraint #6 site (a) + (c) for the new tool. |
| `pkg/config/defaults.go` `Sandbox.ToolPolicies` browser block | modifies | Constraint #6 site (b) — the ceiling. |
| `src/components/browser/BrowserLiveView.tsx` — `takeWheelIfNeeded`, `agentPausedByUserRef`, `effectiveAgentWorking`, `computeDriveMode`, `canDispatchInput`, `releaseWheel`, the auto-release effect | modifies | The D8 rebuild. The regression risk. |

### Impact Assessment

| Symbol modified | Risk | Direct dependents that must be re-tested |
|---|---|---|
| `controlledResult` | **HIGH** | 11 call sites in `tools.go`, `tools_interact.go`, `tabs.go`; plus 3 structural tests via the biconditional (C1). |
| `readOnlyBrowserTools` / `writeClassBrowserTools` | **HIGH** | `audit_test.go` (3 tests), `control_gate_membership_test.go`, `lease_membership_test.go`. |
| `takeWheelIfNeeded` + auto-release effect | **HIGH** | `BrowserLiveView.takeTheWheel.test.tsx` (~25 assertions), `BrowserLiveView.controlToggle.test.tsx`, `BrowserLiveView.tabStrip.test.tsx`. Naive removal of `cancelStream` breaks click dispatch AND revokes the lock (D8). |
| `routingSessionID` reader set | MEDIUM | `routing_session_id_consumer_set_adr057_test.go` (allowlist). |
| `allStaticToolNames` | MEDIUM | `constructor_seed_test.go`, `override_keys_panic_test.go`, the `len(AllStaticToolNames()) == len(DefaultConfig().Sandbox.ToolPolicies)` assertion. |
| `handleControl` | LOW | `browser_ws` gateway tests. |

### Cluster placement

Spans three clusters: **browser tooling** (`pkg/tools/browser`), **gateway wire/control plane**
(`pkg/gateway`), **agent turn engine** (`pkg/agent`), plus the **SPA live panel** and **contracts**.
That five-surface span is why the wave plan below is file-disjoint rather than layer-ordered.

---

## User Stories & Acceptance Criteria

> Phases 1–2.5 guardrail observed: no symbol names, signatures or schemas below this line until the
> Functional Requirements section.

### US-1 — The operator grabs the browser without killing the agent's work (P0)

An operator watching an agent drive a live browser sees it about to do the wrong thing, or reaches a
step only a human can complete. They click into the frame and start driving. Today that click is
recorded as the operator aborting the turn: the work is destroyed and the transcript blames them for
it. They want the agent to simply stop touching the browser and carry on with everything else.

**Why this priority**: the current behaviour destroys work and misattributes the cause. It is the
whole reason the ADR exists.

**Independent test**: start a turn that drives the browser, take the wheel, observe the turn is
still running and nothing in the conversation says it was stopped.

**Acceptance scenarios**

1. **Given** an agent turn is in progress and driving the live browser, **When** the operator takes
   the wheel, **Then** the turn continues and no record of a cancellation is written.
2. **Given** the operator holds the wheel, **When** the agent attempts an action on the browser,
   **Then** the attempt does not reach the page and the agent is told a human is driving.
3. **Given** the operator holds the wheel, **When** the agent does work unrelated to the browser,
   **Then** that work proceeds normally.
4. **Given** a browser action was already underway at the instant the operator took the wheel,
   **When** that action completes, **Then** it completes normally and the next one defers.

### US-2 — The operator's clicks actually reach the page, in one click (P0)

The operator expects one click to both take the wheel and land on the page — that is the behaviour
they have today. The change must not cost them that, and must not silently hand the wheel back the
moment they take it.

**Why this priority**: this is the regression the change most likely introduces. Removing the cancel
removes the only thing that currently makes the take-over stick.

**Independent test**: with a turn in flight, click once in the frame; the click reaches the page and
the panel keeps saying the operator is driving for as long as they hold it.

**Acceptance scenarios**

1. **Given** the agent is working, **When** the operator clicks once in the frame, **Then** the same
   click reaches the page and the panel reports the operator is driving.
2. **Given** the operator has just taken the wheel while the agent is still working, **When** any
   time passes without the operator releasing, **Then** control is not handed back.
3. **Given** the operator holds the wheel while the agent works, **When** they type, scroll or use
   the address bar, **Then** all of it reaches the page.
4. **Given** the operator holds the wheel, **When** the agent's turn ends on its own, **Then** the
   operator still holds the wheel.

### US-3 — The agent says what it is waiting for and stops trying (P0)

An agent that keeps retrying the browser produces a wall of identical deferrals and no useful output.
The operator wants it to try a small number of times, then say plainly what it is blocked on and get
on with something else or finish.

**Why this priority**: without a bound this is a visible quality failure on every takeover.

**Independent test**: take the wheel and leave it held; count the agent's browser attempts and read
its closing message.

**Acceptance scenarios**

1. **Given** the operator holds the wheel, **When** the agent attempts browser work repeatedly,
   **Then** after a small fixed number of attempts it is told to stop attempting for the rest of
   the turn.
2. **Given** the attempt bound has been reached, **When** the agent attempts browser work again in
   the same turn, **Then** nothing reaches the browser and it receives the same terminal instruction.
3. **Given** the bound was reached in one turn, **When** a new turn starts, **Then** the count starts
   again from zero.
4. **Given** the agent has nothing non-browser left to do, **When** it ends its turn, **Then** its
   final message states it is waiting for the operator.

### US-4 — Taking the wheel in one chat stops every agent reachable from it (P0)

An operator takes the wheel on a chat where the agent has delegated part of the job to a sub-agent.
They expect the browser to be theirs — not for a delegated worker to keep clicking on it.

**Why this priority**: this is the correctness hole the ADR identifies as "the real gap". Without it
the feature silently does not work whenever delegation is involved.

**Independent test**: take the wheel on a chat whose delegated worker is driving its own tab set, and
observe the worker deferring.

**Acceptance scenarios**

1. **Given** an agent in a chat has delegated browser work to a sub-agent with its own tab set,
   **When** the operator takes the wheel on that chat, **Then** the sub-agent's browser actions defer.
2. **Given** two unrelated chats share one workspace browser, **When** the operator takes the wheel
   on one, **Then** the agent in the other is not affected.
3. **Given** a sub-agent's browser action deferred, **When** it reports back to its parent, **Then**
   the parent receives that outcome as an ordinary result and neither turn is stopped.

### US-5 — Nothing is captured off the operator's screen while they drive (P0)

While the operator is typing a password or reading their own mail in the shared tab, the agent must
not be screenshotting, reading the text, or snapshotting the page.

**Why this priority**: security. The ADR states this becomes worse under the new design, because the
agent now keeps running instead of being cancelled.

**Independent test**: hold the wheel, ask the agent to screenshot; verify no image reaches the
conversation, the working directory, or the media library.

**Acceptance scenarios**

1. **Given** the operator holds the wheel, **When** the agent attempts to capture the page (image,
   text, or structural snapshot), **Then** the attempt defers and produces no captured content
   anywhere.
2. **Given** the operator holds the wheel, **When** the agent waits for an element or lists tabs,
   **Then** those still work.
3. **Given** the agent captured the page before the operator took the wheel, **When** the operator
   takes the wheel, **Then** the earlier capture is left exactly as it is.

### US-6 — The operator can see that the agent has stood down (P1)

The operator needs to know, without reading the agent's prose, that the agent has stopped driving and
how to give it back.

**Why this priority**: without it the operator cannot distinguish "the agent stood down" from
"the agent is stuck", and the ADR's own resume model (send a message) is undiscoverable.

**Independent test**: take the wheel and read the conversation without reloading the page.

**Acceptance scenarios**

1. **Given** an agent turn is running, **When** the operator takes the wheel, **Then** a line appears
   in the conversation saying the agent has stopped driving the browser and that sending a message
   returns it.
2. **Given** that line has appeared, **When** the operator reloads the session, **Then** the same line
   is still there in the same place.
3. **Given** the operator takes the wheel twice without releasing in between, **When** the second take
   is processed, **Then** only one such line exists.

### US-7 — The operator's next message hands the browser back (P0)

There is no hand-back button and the operator should not need one. Sending the next message is what
resumes the agent, and the agent works out where the page ended up by looking at it.

**Why this priority**: it is the entire resume path. Without it the operator holds the wheel forever
and the agent is locked out of a browser nobody is driving.

**Independent test**: take the wheel, navigate somewhere by hand, close the panel, send a message,
and see the agent act on the page the operator left behind.

**Acceptance scenarios**

1. **Given** the operator holds the wheel, **When** they send any message on that session, **Then**
   control is released before the agent's turn begins.
2. **Given** the operator holds the wheel and has closed the live panel, **When** they send a message,
   **Then** control is still released.
3. **Given** control was released by a message, **When** the agent runs, **Then** it drives the browser
   normally and reports the page's current address and title from what it observes, not from memory.
4. **Given** a different signed-in user sends the message on that session, **When** the release happens,
   **Then** it is recorded against the user who sent the message.
5. **Given** the operator released the wheel by pressing Escape and then walked away without sending
   anything, **When** time passes, **Then** the agent does not silently resume driving and the waiting
   line stays visible.

### US-8 — A holder who vanished does not lock the agent out (P1)

A viewer whose connection died abruptly still owns the wheel as far as the server is concerned. The
agent must not be blocked forever by a person who is not there.

**Why this priority**: it is a pre-existing stale-lock class that the new design makes permanently
visible, because the client-side auto-release that accidentally papered over it is being disarmed.

**Independent test**: take the wheel, kill the connection without a clean close, and have the agent
attempt browser work.

**Acceptance scenarios**

1. **Given** the wheel is held by a viewer who is no longer connected, **When** the agent attempts
   browser work, **Then** it is not deferred.
2. **Given** the wheel is held by a viewer who *is* still connected, **When** the agent attempts
   browser work, **Then** it defers.

### US-9 — The agent can hand the browser over on purpose (P1)

An agent that reaches a sign-in page, a payment step or anything it should not do on the operator's
behalf should be able to say so and put the browser in the operator's hands.

**Why this priority**: it is the productive inverse of the operator grabbing the wheel, and the ADR
scopes it as part of this change.

**Independent test**: drive an agent to a sign-in page and observe it hand over rather than attempt.

**Acceptance scenarios**

1. **Given** an agent reaches a step it should not perform, **When** it hands the browser over,
   **Then** the same waiting line appears and the agent concludes its turn with an explanation.
2. **Given** the agent handed over, **When** it attempts browser work in the same turn, **Then** it
   defers.
3. **Given** the agent handed over, **When** the operator sends their next message, **Then** the
   handover state is cleared exactly as an operator-held wheel would be.
4. **Given** the agent handed over, **When** its turn ends, **Then** the turn ends normally — it is
   not suspended or parked.

### US-10 — Stop still means stop (P0)

Nothing about the deliberate cancel path changes.

**Why this priority**: it is the safety valve; conflating it with the take-over is what caused the
original defect.

**Independent test**: press Stop mid-turn and observe an ordinary cancellation.

**Acceptance scenarios**

1. **Given** a turn is in progress, **When** the operator presses Stop, **Then** the turn is cancelled
   exactly as before.
2. **Given** a turn is in progress, **When** the operator issues the cancel command, **Then** the turn
   is cancelled exactly as before.

### US-11 — An installation with take-control switched off is unaffected (P2)

**Why this priority**: an implementer must not build a second path that reaches this feature when the
operator has disabled remote control.

**Independent test**: disable take-control, attempt every entry point.

**Acceptance scenarios**

1. **Given** take-control is disabled, **When** the operator attempts to take the wheel, **Then** the
   attempt is refused, no waiting line appears, and no agent defers.
2. **Given** take-control is disabled, **When** the agent attempts to hand the browser over, **Then**
   the handover does not take effect and the agent is told so.

---

## Behavioral Contract

**Primary flows**

- When the operator takes the wheel during a running turn, the system leaves the turn running and
  records no cancellation.
- When the agent attempts an action or a capture on a browser whose wheel is held, the system declines
  the attempt without erroring and tells the agent a human is driving, not to retry, and that the
  operator resumes by sending a message.
- When the agent does non-browser work while the wheel is held, the system performs it normally.
- When the operator sends any message on that session, the system releases the wheel before the turn
  starts.
- When the agent hands the browser over deliberately, the system puts the browser in the operator's
  hands, shows the waiting line, and lets the agent conclude its turn.

**Error flows**

- When the agent has attempted browser work three times in one turn against a held wheel, the system
  tells it to state what it is waiting for and stop attempting for the remainder of that turn.
- When the agent attempts again after that point in the same turn, the system declines without
  contacting the browser and repeats the terminal instruction.
- When the wheel is held by someone who is no longer connected, the system treats it as unheld for
  the purpose of declining the agent.
- When take-control is disabled, the system refuses every take and no part of this behaviour engages.

**Boundary conditions**

- When a browser action was already in flight at the moment the wheel was taken, the system lets it
  finish; only the next attempt declines.
- When two chats share one workspace browser, the system declines only agents reachable from the chat
  whose wheel was taken.
- When a delegated worker drives its own tab set beneath a chat whose wheel was taken, the system
  declines it.
- When the wheel is taken twice with no release between, the system shows one waiting line, not two.
- When the operator releases the wheel and sends nothing, the system does not resume the agent.

---

## Edge Cases

- **Take-over mid-navigation.** A page load is in flight when the wheel is taken. Expected: the load
  completes; the operator ends up on whatever the navigation produced; the agent's *next* attempt
  declines.
- **The agent never touches the browser again.** Expected: nothing declines, the turn runs to a normal
  end, and the waiting line is the only trace of the takeover.
- **The wheel is taken while a question card is awaiting an answer.** Expected: no interaction — the
  composer lock and the wheel are independent, and no second suspended state is created.
- **Two viewers, one wheel.** Expected: only the holder's takeover declines the agent; the second
  viewer's input still reaches the page and neither declines the agent nor blocks the holder.
- **Abrupt disconnect of the holder.** Expected: the agent is not declined by the abandoned wheel.
- **Escape, then the operator walks away.** Expected: the agent does not resume driving on its own;
  the waiting line stays until a message arrives.
- **Handover with nobody watching.** Expected: the handover still takes effect and is cleared by the
  next message, whether or not a panel was ever open.
- **Gateway restart while the wheel is held.** Expected: the wheel is lost (it is process memory
  today and stays so); the agent is not declined after the restart; nothing needs recovering.
- **Take-control disabled.** Expected: the whole feature is unreachable.

---

## Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

- The system **must not** cancel, interrupt, suspend or park a turn because the operator took the
  browser, because the operator's intent is to redirect the browser, not to abort the work.
- The system **must not** add any hand-back affordance, because the operator has twice reconfirmed
  that the next message is the resume.
- The system **must not** inject page state, a page summary, or a synthetic re-orientation message
  into the resuming turn, because the resuming turn is an ordinary turn and the agent re-orients by
  looking.
- The system **must not** persist a handover record, because there is nothing to recover: the resume
  is an ordinary message.
- The system **must not** make the wheel an authorization decision on the operator's own input,
  because a standing operator directive says the panel is a real browser and the human's input always
  proceeds.
- The system **must not** retract, delete or redact captures taken before the takeover, because that
  is retroactive rewriting of a transcript for no security gain.
- The system **must not** make the capture tools take the browser's write lease or become per-call
  audited actions, because they act on nothing (see C1).
- The system **must not** let a declined attempt count as a tool failure, because it is coordination,
  not an error, and treating it as an error changes retry and denial behaviour.
- An implementing agent **must not** "helpfully" reinstate the control toggle, a resume dispatcher, a
  frame-persistence path, or a turn-park signal — all four are explicitly out of scope or explicitly
  abandoned.

### Machine-verifiable constraints

- A declined attempt is a **non-error** result whose body is JSON containing a `deferred` key set to
  true and a `reason` string. Callers must be able to detect it structurally, not by prose.
- The attempt bound is **3** per turn. The 3rd declined attempt carries the stop instruction; the 4th
  and later never contact the browser.
- A declined capture attempt produces **zero** of: a file under the turn's working directory, a media
  library entry, an artifact tag.
- Take-control disabled ⇒ **zero** takes succeed and **zero** waiting lines are emitted.
- The waiting line appears **at most once** per unbroken held period.
- Existing take/release audit records keep their current field set; the new decline record carries
  session id, turn id, viewer id, acting user, tab set, and tool name.

---

## Integration Boundaries

### Live browser panel (SPA ↔ gateway WebSocket)

- **Data in**: take/release requests, viewer input, viewport and tab actions.
- **Data out**: control-state acknowledgements, tab strip, error notices, WebRTC signalling.
- **Contract**: the existing browser WebSocket channel. This change adds no new frame here.
- **On failure**: a failed take leaves the operator not driving and tells them to retry; a failed
  release forces the local state back to released and tells them to retry. Both behaviours exist today
  and are preserved.
- **Development**: real gateway. The panel's behaviour under a stale/failed send is already covered by
  existing tests and must stay covered.

### Chat stream (SPA ↔ gateway WebSocket)

- **Data in**: the operator's messages (this is also the release trigger).
- **Data out**: the waiting line, live, in the open thread.
- **Contract**: a new frame is required (C2), defined contract-first before any code.
- **On failure**: if the frame cannot be delivered, the transcript entry still exists and the line
  appears on the next replay. The feature degrades to "visible after reload", never to "silently
  absent from both".
- **Development**: real gateway; the SPA edge validates the frame and drops-with-counter on a schema
  miss, per the standing contract rule.

### Headless Chromium (gateway ↔ CDP)

- **Data in / out**: unchanged. This change only decides *whether* a tool reaches CDP.
- **On failure**: unchanged.
- **Development**: real Chromium for integration and e2e; the existing package fakes for unit level.

---

## Functional Requirements

> Implementation detail is expected from here on. `file::symbol` citations only — `loop.go`,
> `turn.go` and `subturn.go` churn, so no line numbers.

### A. Take-over defers, never cancels (D1)

- **FR-001**: Taking the wheel MUST NOT invoke the chat cancel action.
  `src/components/browser/BrowserLiveView.tsx::takeWheelIfNeeded` MUST NOT call
  `useChatStore.getState().cancelStream(...)` on any path.
- **FR-002**: A take MUST NOT produce a `session.EntryTypeTurnCancelled` (`"turn_canceled"`)
  transcript entry, MUST NOT set an interrupted turn-end status, MUST NOT set
  `tools.ToolResult.ParksTurn`, and MUST NOT call any `RequestCancel` path.
- **FR-003**: A gated browser tool call already in flight when the wheel is taken MUST complete
  normally. There is no mid-tool preemption (`controlledResult`'s documented v1 limitation stands).
- **FR-004**: After a take, non-browser tool calls in the same turn MUST execute unchanged.

### B. Deferral wording and the bounded attempt (D2.1, D2.2)

- **FR-010**: `pkg/tools/browser/tools.go::controlledResult` MUST keep returning a non-error
  `tools.ToolResult` whose body is JSON with `deferred: true` and a `reason` string.
- **FR-011**: The `reason` string MUST state all three of: (a) a human is driving this browser;
  (b) do not retry the browser until control returns; (c) the operator resumes by sending a message.
- **FR-012**: The deferral result MUST carry a stable machine-readable discriminator the turn engine
  can count on without parsing prose — the existing `deferred` key, plus a `gate` field naming the
  control gate, so a future second deferral source is not miscounted.
- **FR-013**: The turn engine MUST maintain a per-turn count of control-gate deferrals, on
  `pkg/agent/turn.go::turnState`, modelled on `pkg/agent/tool_denial.go::turnDenialLedger`
  (C4). It MUST NOT be keyed on the transcript session id alone.
- **FR-014**: N MUST be **3**. *Justification, since the ADR invites one:* the deferral is a
  100%-reproducible condition, not a transient one — a retry cannot succeed until a human acts, so
  the only value of >1 attempt is letting the model discover the state on a tool it actually needed.
  Two attempts is enough for that; three leaves one attempt of slack for a multi-tool browser
  sequence (e.g. `browser_snapshot` then `browser_click`) to surface the state on the tool the model
  cares about. It also matches the existing per-turn denial budget's order of magnitude in
  `pkg/agent/tool_denial.go`. Larger values only add identical noise.
- **FR-015**: On the 3rd deferral in a turn, the result handed to the model MUST additionally instruct
  it to state plainly what it is waiting for and to stop attempting browser work for the remainder of
  the turn.
- **FR-016**: After the bound is reached, every later control-gated browser tool call in the same turn
  MUST short-circuit — no CDP contact, no lease acquisition, no audit action row — and return the same
  terminal instruction.
- **FR-017**: The counter MUST be per turn. A delegated child turn MUST have its own counter, and a
  new turn MUST start at zero.

### C. Gate coverage across every reachable tab set (D2.3)

- **FR-020**: The control gate MUST be evaluated against **both** the tab set the call resolved
  (`sessionKey(key, owner)` from `pkg/tools/browser/register.go::resolveTurnScope`) **and** the tab set
  the live panel would have taken the lock on for the ROOT chat this turn belongs to
  (`pkg/tools/browser/manager.go::PanelTabSetID(rootChatSessionID)`). A hold on either defers.
- **FR-021**: The root chat id MUST be sourced from `pkg/agent/turn.go::turnState.routingSessionID`
  (ADR-057 FR-011), surfaced to the browser tools through the existing
  `pkg/tools/browser/register.go::ManagerResolver` seam — `pkg/tools/browser` MUST NOT import
  `pkg/agent`.
- **FR-022**: `pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed`
  MUST be amended in the same commit to name the new reader, with the role-B justification recorded
  (C3). Adding the reader without amending the allowlist is a build break, and amending the allowlist
  without the justification comment is a silent widening of ADR-057 FR-014.
- **FR-023**: A delegated child driving its **own** tab set MUST defer while the operator holds the
  wheel on its root chat.
- **FR-024**: An agent in an **unrelated** chat sharing the same workspace browser (same
  `BrowsingKey`, different root chat) MUST NOT defer. A gate that defers on "any tab set of this
  browser is controlled" is a defect, not a conservative choice.

### D. Resume is the next prompt (D3, D4)

- **FR-025**: The operator's next message MUST run as an ordinary turn. No resume dispatcher, no
  injected page state, no synthetic message, no persisted handover record.
- **FR-026**: Releasing the wheel (Escape, entering annotate mode, closing the panel, a WS
  disconnect) MUST NOT resume the agent and MUST NOT retract the waiting line.
- **FR-027**: Re-orientation MUST rely on the existing `pkg/tools/browser/tools.go::ScreenshotTool`
  description, which already reports the tab's current URL and title including a page the operator
  navigated to themselves. No new re-orientation surface MAY be added.
- **FR-028**: On accepting a chat turn for a session whose panel wheel is held, the **gateway** MUST
  release it server-side, before the turn begins. It MUST work with the panel closed and with the
  holder's connection gone.
- **FR-029**: The release MUST be wired at the single point every channel's inbound message converges
  on, not only at the webchat WebSocket handler (`pkg/gateway/websocket.go`'s `PublishInbound` site)
  — see C7 and Ambiguity A3. A prompt arriving on the same session from any channel MUST release.
- **FR-030**: The release MUST be audited via `pkg/gateway/browser_ws.go::auditRelease`'s record shape
  with the **acting user** (the sender of the message), not the holder.
- **FR-031**: A wheel whose holder is no longer in `pkg/tools/browser/live.go::LiveView.viewers` MUST
  be treated as **void** by the tool gate — the agent MUST NOT defer to a ghost. The existing
  ghost-steal path (`LiveView::ensureControlForInput`) MUST stay reachable.
- **FR-032**: Input dispatch MUST stay ungated (operator directive, 2026-08-03).
  `LiveView::dispatchInput` MUST NOT gain a control-lock authorization check. A second viewer driving
  without the wheel MUST neither defer the agent nor be blocked.

### E. Capture gating and the three-way classification (D5, C1)

- **FR-033**: `browser_screenshot`, `browser_get_text` and `browser_snapshot` MUST consult the control
  gate and return the same non-error deferral while the wheel is held.
- **FR-034**: `browser_wait`, `browser_list_tabs` and `browser_handle_dialog` MUST remain ungated.
  `browser_handle_dialog` in particular MUST stay ungated — gating a recovery verb behind the
  mechanism the fault disables is a deadlock (its existing exemption reason).
- **FR-035**: The classification MUST become three-way, replacing the current two-way roster:
  **action** (deferral-gated + `acquireWrite` + `recordBrowserAction`), **capture** (deferral-gated,
  NOT leased, NOT per-call audited), **exempt** (none). `pkg/tools/browser/audit.go` MUST carry a
  third explicit roster alongside `writeClassBrowserTools` and `readOnlyBrowserTools`.
- **FR-036**: `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased`
  MUST be re-stated as `leased ⟺ action-class` and MUST fail if a capture-class tool acquires the
  write lease.
- **FR-037**: `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet` MUST
  be re-stated as `per-call-audited ⟺ action-class` and MUST fail if a capture-class tool emits a
  `browser_action` row.
- **FR-038**: `pkg/tools/browser/control_gate_membership_test.go` MUST assert the full three-way
  partition against the registered catalog, with every member of each class named in a literal roster
  and a stated reason, and MUST fail on a registered tool that belongs to no class.
- **FR-039**: A deferred `browser_screenshot` MUST produce **none** of its three sinks: no JPEG under
  the turn's working directory via `tools.ResolvePath`, no MediaStore entry created by
  `pkg/tools/normalization.go::normalizeToolResult` from a returned data URL, and no
  `[file:…]` `ArtifactTags` entry. The gate MUST run before the capture, not after.
- **FR-040**: Captures taken **before** the take MUST NOT be retracted, deleted or redacted.

### F. The visible waiting surface (D6, C2)

- **FR-041**: When the wheel is taken during a session, the system MUST emit a system-authored line
  stating that the agent has stopped driving the browser and that sending a message returns it.
- **FR-042**: The line MUST be delivered **live** to the open thread via a new chat-channel wire type,
  authored contract-first per Constraint #8: schema under `contracts/components/schemas/`, referenced
  from `contracts/asyncapi.yaml`, regenerated by `scripts/gen-contracts.sh`, generated artifacts
  committed in the same commit, and consumed only through the generated Go/TS types and Zod schema.
- **FR-043**: The line MUST also be persisted as a `session.EntryTypeSystem` transcript entry (pattern:
  `pkg/agent/goal_loop.go::writeGoalSystemTranscript`) so a replay renders the same line in the same
  place. A delivery failure on the live frame MUST NOT suppress the transcript write.
- **FR-044**: Exactly **one** line MUST be emitted per unbroken held period. A second `take` with no
  intervening release MUST NOT emit a second line.
- **FR-045**: The agent's own narration is driven by the deferral wording (FR-011) and is SHOULD, not
  MUST — the system line is the guaranteed surface, because the deferred tool call may be hidden from
  the thread by `src/lib/toolVisibility.ts`.

### G. `browser_handover` (D7, C5)

- **FR-046**: A new tool `browser_handover` MUST be added, taking a single `reason` string.
- **FR-047**: It MUST put the browser in the operator's hands by setting an explicit
  **handover-pending** state on the live view for the turn's resolved tab set — a state distinct from
  `LiveView.controller`, because there may be no viewer id to grant (C5). The handover-pending state
  MUST defer tools exactly as a held wheel does and MUST NOT be subject to the ghost rule of FR-031.
- **FR-048**: It MUST emit the FR-041 waiting surface.
- **FR-049**: It MUST return a non-error result instructing the agent to conclude its turn with an
  explanation. It MUST NOT set `ParksTurn`.
- **FR-050**: Handover-pending MUST be cleared by the same next-prompt release as FR-028, and by an
  explicit operator release.
- **FR-051**: Per Constraint #6 / ADR-077 the tool MUST be added at all three sites in one commit:
  `pkg/coreagent/core.go::allStaticToolNames`; the browser block of `pkg/config/defaults.go`'s
  `Sandbox.ToolPolicies` with a shipped default of `allow` (matching the family); and the per-agent
  seeds for every browser-capable agent — `IDJim`, `IDRay`, `IDExplorer`, `IDResearcher` in
  `pkg/coreagent/core.go`. Mia and Ava name no browser tool and MUST NOT be seeded it.
- **FR-052**: With `tools.browser.take_control_enabled` false, `browser_handover` MUST NOT take effect
  and MUST return a result saying so. It MUST NOT become a bypass for the disabled feature.

### H. Client take-over state rebuild (D8) — the regression surface

- **FR-053**: `BrowserLiveView.tsx` MUST introduce an explicit **operator-holds-wheel** state, set the
  instant `sendControl('take')` is sent and confirmed by the server's `controlling` status
  acknowledgement. It replaces `agentPausedByUserRef` / `agentPausedByUser`, which exist only to
  compensate for `cancelStream`'s asynchronous confirmation and have no meaning once FR-001 lands.
- **FR-054**: `computeDriveMode` MUST give operator-holds-wheel priority **over** `agentWorking`.
  **Regression guard:** without this, `driveMode` stays `agent-working`, `canDispatchInput` returns
  false, and the operator's clicks are inert while the panel claims they are driving.
- **FR-055**: The auto-release effect (`effectiveAgentWorking && isControlling → sendControl('release')`)
  MUST be gated on operator-holds-wheel so it can never revoke a wheel the operator took while a turn
  is in flight. **Regression guard:** without this, the take is granted and immediately released.
- **FR-056**: One click MUST still both acquire the wheel and dispatch the same `pointerdown` as page
  input, for click-to-drive, the omnibox, the tab strip and the explicit Take-over button.
- **FR-057**: Operator-holds-wheel MUST be cleared **only** on a release — Escape, entering annotate
  mode, a failed take, a server `released` status, or disconnect. It MUST NOT be cleared by an
  `agentWorking` transition. **Regression guard:** the existing `agentPausedByUser` is cleared when
  `agentWorking` goes false; carrying that clearing rule over re-arms the auto-release the moment the
  turn ends, silently dropping a wheel the operator still holds.
- **FR-058**: Escape MUST still release the wheel and return focus to the address bar (WCAG 2.1.2).
- **FR-059**: No hand-back / control-toggle affordance MAY be added; ADR-040 D1 stands, asserted by
  `src/components/browser/BrowserLiveView.controlToggle.test.tsx`.

### I. Audit (D10)

- **FR-060**: `pkg/gateway/browser_ws.go::auditControl` and `::auditRelease` keep their current record
  shape and severities.
- **FR-061**: A take that causes an agent to defer MUST emit a distinct audit record carrying session
  id, turn id, viewer id, acting user, tab set, and the deferred tool's name.
- **FR-062**: A `browser_handover` MUST be audited with the same field set, with the agent as actor.

### J. Unchanged surfaces (D9, D11, scope)

- **FR-063**: The Stop button and `/cancel` MUST be unchanged.
- **FR-064**: Nothing in this change MAY set `ParksTurn`; there is therefore no park cascade through
  `pkg/agent/subturn.go` or `pkg/tools/delegate.go`. A delegated child that defers MUST report the
  outcome to its parent as an ordinary tool result.
- **FR-065**: With `tools.browser.take_control_enabled` false, no take succeeds
  (`handleControl` refuses and audits `take_control_disabled`), no waiting line is emitted, no agent
  defers, and no new code path MAY reach any part of this feature.

---

## BDD Scenarios

### Feature: Browser control handover

#### Scenario: Operator takes the wheel and the turn keeps running
**Traces to**: US-1, AS-1 · **Category**: Happy Path
- **Given** an agent turn is in progress on a session and is driving the live browser
- **When** the operator takes the wheel
- **Then** the turn is still in progress
- **And** no cancellation record exists in the session transcript
- **But** no interrupted or stopped status is reported to the operator

#### Scenario: The agent's next browser action defers instead of driving
**Traces to**: US-1, AS-2 · **Category**: Happy Path
- **Given** the operator holds the wheel on a session with a running turn
- **When** the agent attempts to click an element on the page
- **Then** the click does not reach the page
- **And** the agent receives a non-error result stating a human is driving, not to retry, and that a
  message from the operator returns control

#### Scenario: Non-browser work continues while the operator drives
**Traces to**: US-1, AS-3 · **Category**: Happy Path
- **Given** the operator holds the wheel on a session with a running turn
- **When** the agent reads a file
- **Then** the file is read and returned normally

#### Scenario: Take-over mid-navigation lets the in-flight navigation finish
**Traces to**: US-1, AS-4 · **Category**: Edge Case
- **Given** the agent has started a page navigation that has not completed
- **When** the operator takes the wheel before the navigation returns
- **Then** the navigation completes and returns its normal result
- **And** the agent's next browser attempt defers

#### Scenario: The operator's click reaches the page in one click while the agent works
**Traces to**: US-2, AS-1 · **Category**: Happy Path
- **Given** the agent is working and the operator is watching the live panel
- **When** the operator clicks once inside the frame
- **Then** the same click is dispatched to the page as input
- **And** the panel reports that the operator is driving

#### Scenario: The wheel is not handed back while the agent is still working
**Traces to**: US-2, AS-2 · **Category**: Error Path
- **Given** the operator has just taken the wheel while an agent turn is in flight
- **When** the take is acknowledged by the server
- **Then** no release request is sent
- **And** the operator still holds the wheel

#### Scenario: The wheel survives the end of the agent's turn
**Traces to**: US-2, AS-4 · **Category**: Edge Case
- **Given** the operator holds the wheel while an agent turn is in flight
- **When** that turn ends on its own
- **Then** the operator still holds the wheel
- **And** their input still reaches the page

#### Scenario: The agent is told to stop attempting after the third deferral
**Traces to**: US-3, AS-1 · **Category**: Happy Path
- **Given** the operator holds the wheel on a session with a running turn
- **When** the agent attempts browser work for the third time in that turn
- **Then** the result instructs it to state what it is waiting for and stop attempting browser work
  for the remainder of the turn

#### Scenario: A fourth attempt never reaches the browser
**Traces to**: US-3, AS-2 · **Category**: Error Path
- **Given** the agent has reached the attempt bound in the current turn
- **When** it attempts browser work again in the same turn
- **Then** nothing is sent to the browser
- **And** it receives the same terminal instruction

#### Scenario: A new turn starts the attempt count at zero
**Traces to**: US-3, AS-3 · **Category**: Edge Case
- **Given** a previous turn reached the attempt bound
- **When** a new turn begins on the same session while the wheel is still held
- **Then** the first browser attempt of the new turn receives the ordinary deferral, not the terminal
  instruction

#### Scenario: The agent ends its turn saying what it is waiting for
**Traces to**: US-3, AS-4 · **Category**: Happy Path
- **Given** the agent has reached the attempt bound and has no non-browser work left
- **When** it ends its turn
- **Then** its final message states that it is waiting for the operator to return the browser

#### Scenario: A delegated worker driving its own tab set defers
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** an agent has delegated browser work to a sub-agent that is driving its own tab set
- **And** the operator holds the wheel on the root chat
- **When** the sub-agent attempts a browser action
- **Then** the action defers

#### Scenario: An unrelated chat on the same workspace browser is unaffected
**Traces to**: US-4, AS-2 · **Category**: Edge Case
- **Given** two chats on one workspace share a single browser
- **And** the operator holds the wheel on the first chat
- **When** an agent in the second chat performs a browser action
- **Then** the action executes normally

#### Scenario: A deferred sub-agent reports to its parent without stopping either turn
**Traces to**: US-4, AS-3 · **Category**: Alternate Path
- **Given** a delegated sub-agent's browser action deferred
- **When** it returns its result to the parent
- **Then** the parent receives it as an ordinary delegation result
- **And** neither the parent nor the child turn is stopped or suspended

#### Scenario Outline: Capture attempts defer while the operator drives
**Traces to**: US-5, AS-1 · **Category**: Happy Path
- **Given** the operator holds the wheel
- **When** the agent calls `<tool>`
- **Then** the result is the non-error deferral
- **And** `<sink assertion>`

**Examples**

| tool | sink assertion |
|---|---|
| `browser_screenshot` | no image file, no media library entry and no artifact tag are produced |
| `browser_get_text` | no page text is returned |
| `browser_snapshot` | no accessibility snapshot is returned |

#### Scenario: Wait and tab listing still work while the operator drives
**Traces to**: US-5, AS-2 · **Category**: Alternate Path
- **Given** the operator holds the wheel
- **When** the agent waits for an element and lists the open tabs
- **Then** both complete normally

#### Scenario: An earlier capture is not retracted
**Traces to**: US-5, AS-3 · **Category**: Edge Case
- **Given** the agent captured a screenshot before the operator took the wheel
- **When** the operator takes the wheel
- **Then** the earlier screenshot, its media entry and its artifact tag are all still present

#### Scenario: The waiting line appears live in the thread
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** an agent turn is running and the operator has the session open
- **When** the operator takes the wheel
- **Then** a system-authored line appears in the thread without a reload, stating the agent has
  stopped driving the browser and that sending a message returns it

#### Scenario: The waiting line survives a reload
**Traces to**: US-6, AS-2 · **Category**: Happy Path
- **Given** the waiting line has appeared in the thread
- **When** the operator reloads and re-attaches the session
- **Then** the same line is rendered in the same position

#### Scenario: Taking the wheel twice emits one waiting line
**Traces to**: US-6, AS-3 · **Category**: Edge Case
- **Given** the operator holds the wheel and a waiting line has been emitted
- **When** a second take is processed with no intervening release
- **Then** exactly one waiting line exists in the session

#### Scenario: The next message releases the wheel and the turn begins
**Traces to**: US-7, AS-1 · **Category**: Happy Path
- **Given** the operator holds the wheel
- **When** they send a message on that session
- **Then** the wheel is released before the agent's turn begins
- **And** the agent's browser actions in that turn execute normally

#### Scenario: The wheel is released even with the panel closed
**Traces to**: US-7, AS-2 · **Category**: Edge Case
- **Given** the operator holds the wheel and has closed the live panel
- **When** they send a message on that session
- **Then** the wheel is released server-side

#### Scenario: A different user's message releases the wheel and is audited as that user
**Traces to**: US-7, AS-4 · **Category**: Alternate Path
- **Given** one signed-in user holds the wheel on a session
- **When** a different signed-in user sends a message on that session
- **Then** the wheel is released
- **And** the release audit record names the user who sent the message, not the holder

#### Scenario: Escape released the wheel and the operator walks away
**Traces to**: US-7, AS-5 · **Category**: Edge Case
- **Given** the operator took the wheel during a running turn and then pressed Escape
- **And** no message is sent afterwards
- **When** time passes
- **Then** the agent does not resume driving the browser on its own
- **And** the waiting line remains in the thread

#### Scenario: A ghost holder does not lock the agent out
**Traces to**: US-8, AS-1 · **Category**: Error Path
- **Given** the wheel is held by a viewer whose connection dropped without a clean close
- **When** the agent attempts a browser action
- **Then** the action executes normally

#### Scenario: A live holder does lock the agent out
**Traces to**: US-8, AS-2 · **Category**: Happy Path
- **Given** the wheel is held by a viewer who is still connected
- **When** the agent attempts a browser action
- **Then** the action defers

#### Scenario: Two viewers, one wheel
**Traces to**: US-8, AS-2 (and the ungated-input directive) · **Category**: Edge Case
- **Given** two viewers are attached and the first holds the wheel
- **When** the second viewer sends pointer input
- **Then** that input reaches the page
- **And** the agent's deferral state is decided only by the first viewer's hold

#### Scenario: The agent hands the browser over at a sign-in page
**Traces to**: US-9, AS-1 · **Category**: Happy Path
- **Given** the agent has reached a sign-in page it must not complete
- **When** it hands the browser over with a reason
- **Then** the waiting line appears in the thread
- **And** the agent receives a non-error result instructing it to conclude its turn with an explanation

#### Scenario: After handing over, the agent's own browser attempts defer
**Traces to**: US-9, AS-2 · **Category**: Alternate Path
- **Given** the agent has handed the browser over in this turn
- **When** it attempts a browser action
- **Then** the action defers

#### Scenario: A handover ends the turn normally rather than suspending it
**Traces to**: US-9, AS-4 · **Category**: Happy Path
- **Given** the agent has handed the browser over
- **When** it finishes its remaining work
- **Then** the turn ends with an ordinary completed status, not a parked or suspended one

#### Scenario: The next message clears an agent-initiated handover
**Traces to**: US-9, AS-3 · **Category**: Happy Path
- **Given** the agent handed the browser over and no viewer ever attached
- **When** the operator sends a message on that session
- **Then** the handover state is cleared and the agent's browser actions execute normally

#### Scenario: A question card is pending when the operator takes the wheel
**Traces to**: US-1, AS-1 (edge) · **Category**: Edge Case
- **Given** the agent has asked a structured question and the composer is locked awaiting the answer
- **When** the operator takes the wheel on the live browser
- **Then** the turn remains parked on the question exactly as before
- **And** no second suspended state is created
- **And** answering the question resumes the turn normally, with the browser still held by the operator

#### Scenario: Stop still cancels the turn
**Traces to**: US-10, AS-1 · **Category**: Happy Path
- **Given** an agent turn is in progress
- **When** the operator presses Stop
- **Then** the turn is cancelled and recorded as a user cancellation, exactly as before this change

#### Scenario: With take-control disabled, the whole feature is unreachable
**Traces to**: US-11, AS-1 and AS-2 · **Category**: Error Path
- **Given** `tools.browser.take_control_enabled` is false
- **When** the operator attempts to take the wheel
- **Then** the take is refused and audited as disabled
- **And** no waiting line is emitted
- **And** the agent's browser actions continue to execute normally
- **And** an agent's handover attempt does not take effect and says so

---

## Test Matrix

Every FR maps to at least one named test at a named level, in a named file. **This machine cannot run
the full Go gateway suite (OOM — see CLAUDE.md "Testing & building").** Every Go test below is run
locally only as a single scoped invocation
(`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`); the suite verdict
comes from CI or the `ci-omnipus` Fly worker.

| FR | Test | Level | File | Scenario |
|---|---|---|---|---|
| FR-001, FR-053 | `does not call cancelStream when taking the wheel while the agent is working` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Operator takes the wheel… |
| FR-002 | `TestTakeover_WritesNoTurnCanceledEntry` | Go integration | `pkg/gateway/browser_control_handover_test.go` | Operator takes the wheel… |
| FR-002 | `TestTakeover_SetsNoParkAndNoCancel` | Go unit | `pkg/agent/browser_deferral_test.go` | Operator takes the wheel… |
| FR-003 | `TestControlGate_InFlightCallCompletesAfterTake` | Go unit | `pkg/tools/browser/tools_control_test.go` | Take-over mid-navigation… |
| FR-004 | `TestTakeover_NonBrowserToolStillExecutes` | Go integration | `pkg/gateway/browser_control_handover_test.go` | Non-browser work continues… |
| FR-010 | `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (existing, extended) | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-011 | `TestDeferralReason_StatesNoRetryAndPromptToResume` | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-012 | `TestDeferralPayload_CarriesGateDiscriminator` | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-013, FR-017 | `TestBrowserDeferralLedger_CountsPerTurnNotPerSession` | Go unit | `pkg/agent/browser_deferral_test.go` | A new turn starts the attempt count at zero |
| FR-014, FR-015 | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` | Go unit | `pkg/agent/browser_deferral_test.go` | The agent is told to stop attempting… |
| FR-016 | `TestBrowserDeferralLedger_FourthAttemptShortCircuitsWithoutBrowser` | Go unit | `pkg/agent/browser_deferral_test.go` | A fourth attempt never reaches the browser |
| FR-017 | `TestBrowserDeferralLedger_ChildTurnHasItsOwnCounter` | Go unit | `pkg/agent/browser_deferral_test.go` | A delegated worker driving its own tab set defers |
| FR-020 | `TestControlledResult_ChecksRootChatPanelTabSet` | Go unit | `pkg/tools/browser/tools_control_test.go` | A delegated worker driving its own tab set defers |
| FR-021 | `TestManagerResolver_SurfacesRootChatSessionID` | Go unit | `pkg/agent/browser_resolver_test.go` | A delegated worker driving its own tab set defers |
| FR-022 | `TestRoutingSessionID_ConsumerSetIsClosed` (existing, amended) | Go unit | `pkg/agent/routing_session_id_consumer_set_adr057_test.go` | — (structural) |
| FR-023 | `TestDelegatedChild_DefersOnParentWheelHold` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A delegated worker driving its own tab set defers |
| FR-024 | `TestControlledResult_UnrelatedChatOnSameBrowserDoesNotDefer` | Go unit | `pkg/tools/browser/tools_control_test.go` | An unrelated chat on the same workspace browser… |
| FR-025, FR-027 | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-026 | `clears the wheel on Escape without resuming the agent` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Escape released the wheel… |
| FR-026 | `TestRelease_DoesNotClearWaitingLine` | Go integration | `pkg/gateway/browser_control_handover_test.go` | Escape released the wheel… |
| FR-028 | `TestChatTurnAccept_ReleasesHeldPanelLock` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-028 | `TestChatTurnAccept_ReleasesWithPanelClosed` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The wheel is released even with the panel closed |
| FR-029 | `TestChatTurnAccept_ReleasesForNonWebchatChannel` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-030 | `TestChatTurnAccept_ReleaseAuditsActingUserNotHolder` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A different user's message releases the wheel… |
| FR-031 | `TestControlGate_GhostHolderDoesNotDeferTools` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A ghost holder does not lock the agent out |
| FR-031 | `TestControlGate_LiveHolderDefersTools` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A live holder does lock the agent out |
| FR-032 | `TestSharedControl_SecondViewerInputStillDispatches` (existing) | Go unit | `pkg/tools/browser/shared_control_test.go` | Two viewers, one wheel |
| FR-032 | `TestSharedControl_SecondViewerDoesNotDeferTheAgent` | Go unit | `pkg/tools/browser/shared_control_test.go` | Two viewers, one wheel |
| FR-033 | `TestControlGate_CaptureToolsDeferWhileControlled` | Go unit | `pkg/tools/browser/tools_control_test.go` | Capture attempts defer… (outline) |
| FR-034 | `TestControlGate_WaitListTabsAndDialogStayUngated` | Go unit | `pkg/tools/browser/tools_control_test.go` | Wait and tab listing still work… |
| FR-035, FR-038 | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` | Go unit | `pkg/tools/browser/control_gate_membership_test.go` | — (structural) |
| FR-036 | `TestWriteLease_LeasedIffActionClass` | Go unit | `pkg/tools/browser/lease_membership_test.go` | — (structural) |
| FR-037 | `TestAudit_PerCallAuditedIffActionClass` | Go unit | `pkg/tools/browser/audit_test.go` | — (structural) |
| FR-039 | `TestScreenshot_DeferredCallProducesNoFileNoMediaNoArtifactTag` | Go unit | `pkg/tools/browser/tools_control_test.go` | Capture attempts defer… (screenshot row) |
| FR-040 | `TestCapture_PreTakeoverArtifactsAreNotRetracted` | Go integration | `pkg/gateway/browser_control_handover_test.go` | An earlier capture is not retracted |
| FR-041, FR-042 | `TestTakeControl_EmitsWaitingSurfaceFrame` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The waiting line appears live… |
| FR-042 | `renders the browser-handover waiting notice in the thread` | vitest | `src/components/chat/ChatScreen.browser-handover-notice.test.tsx` | The waiting line appears live… |
| FR-042 | `verify-contracts` gate (`make verify-contracts`) | CI gate | `contracts/` + generated dirs | — (structural) |
| FR-043 | `TestTakeControl_WritesWaitingSurfaceTranscriptEntry` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The waiting line survives a reload |
| FR-044 | `TestTakeControl_SecondTakeWithoutReleaseEmitsOneLine` | Go integration | `pkg/gateway/browser_control_handover_test.go` | Taking the wheel twice emits one waiting line |
| FR-045 | `TestDeferralReason_DrivesAgentNarration` | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent ends its turn saying what it is waiting for |
| FR-046, FR-049 | `TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark` | Go unit | `pkg/tools/browser/tools_handover_test.go` | The agent hands the browser over… |
| FR-047 | `TestHandoverTool_SetsHandoverPendingNotAViewerLock` | Go unit | `pkg/tools/browser/tools_handover_test.go` | After handing over, the agent's own browser attempts defer |
| FR-047 | `TestHandoverPending_IsNotSubjectToGhostVoiding` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | After handing over… |
| FR-048 | `TestHandoverTool_EmitsWaitingSurface` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The agent hands the browser over… |
| FR-049 | `TestHandoverTool_TurnEndsCompletedNotParked` | Go unit | `pkg/agent/browser_deferral_test.go` | A handover ends the turn normally… |
| FR-050 | `TestHandoverPending_ClearedByNextPrompt` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message clears an agent-initiated handover |
| FR-051 | `TestSeed_BrowserHandoverAtAllThreeConstraint6Sites` | Go unit | `pkg/coreagent/browser_handover_seed_test.go` | — (structural) |
| FR-051 | `TestConstructorSeed_PolicyMapMatchesCatalog` (existing) | Go unit | `pkg/coreagent/constructor_seed_test.go` | — (structural) |
| FR-051 | `TestDefaults_CeilingLengthMatchesStaticCatalog` (existing assertion) | Go unit | `pkg/config/defaults_test.go` | — (structural) |
| FR-052, FR-065 | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` | Go integration | `pkg/gateway/browser_control_handover_test.go` | With take-control disabled… |
| FR-053, FR-054 | **REGRESSION** `dispatches the same pointerdown as input in ONE click while the agent is working` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The operator's click reaches the page… |
| FR-054 | **REGRESSION** `keeps you-driving priority over agent-working for the chip, cursor and dispatch gate` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The operator's click reaches the page… |
| FR-055 | **REGRESSION** `does not auto-release the wheel after the take ack while the agent is still working` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The wheel is not handed back… |
| FR-056 | `acquires the wheel and dispatches in one click from the omnibox, the tab strip and Take over` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The operator's click reaches the page… |
| FR-057 | **REGRESSION** `does not clear operator-holds-wheel when the agent turn ends` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The wheel survives the end of the agent's turn |
| FR-057 | `clears operator-holds-wheel on a server released status and on annotate mode` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Escape released the wheel… |
| FR-058 | `reverts control state and stops accepting input when the socket disconnects mid-control` (existing) | vitest | `src/components/browser/BrowserLiveView.controlToggle.test.tsx` | Escape released the wheel… |
| FR-059 | `never renders a Take control / Release control / Hand to agent button` (existing) | vitest | `src/components/browser/BrowserLiveView.controlToggle.test.tsx` | — (structural) |
| FR-060 | existing `handleControl` audit assertions | Go integration | `pkg/gateway/browser_ws_test.go` | — (structural) |
| FR-061 | `TestDeferralAudit_RecordsSessionTurnViewerUserTabSetAndTool` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The agent's next browser action defers… |
| FR-062 | `TestHandoverAudit_RecordsAgentAsActor` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The agent hands the browser over… |
| FR-063 | existing cancel-path tests (see Regression) | Go integration | `pkg/gateway/` cancel tests | Stop still cancels the turn |
| FR-064 | `TestDelegation_NoParkCascadeFromBrowserDeferral` | Go unit | `pkg/agent/browser_deferral_test.go` | A deferred sub-agent reports to its parent… |
| — (question-card edge) | `TestAskUserQuestionPending_UnaffectedByWheelTake` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A question card is pending… |
| — (end-to-end) | `operator takes the wheel mid-turn and the turn is not cancelled` | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | Operator takes the wheel… |
| — (end-to-end) | `agent states what it is waiting for after the third deferral` | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | The agent ends its turn saying… |
| — (end-to-end) | `next prompt releases the wheel and the agent re-orients from the page` | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | The next message releases the wheel… |

**Count**: 68 distinct named tests across 68 matrix rows. 10 of those rows reuse or amend an existing test or CI gate (marked as such in the table); the remaining 58 are new.

---

## Regression Requirements

### What must keep working

| Existing behaviour | Protecting test(s) today | New regression test needed |
|---|---|---|
| Stop cancels the turn, recorded as a user cancellation | the gateway cancel-path tests exercising `cancel` / `cancel_stage` frames; `pkg/agent` cancel/descendant tests reachable from `turn.go::Finish`'s `onCancelFinish` | **No** — assert unchanged; add no new coverage, but run them |
| `/cancel` cancels the turn | same as above | **No** |
| Escape releases the wheel and returns focus to the address bar (WCAG 2.1.2) | `BrowserLiveView.controlToggle.test.tsx` (`reverts control state and stops accepting input when the socket disconnects mid-control`); the Escape branch in `handleKeyDown` | **Yes** — `clears the wheel on Escape without resuming the agent` |
| Input dispatch is ungated by the control lock | `pkg/tools/browser/shared_control_test.go::TestSharedControl_SecondViewerInputStillDispatches`, `::TestSharedControl_ControllerRemainsPresentational`, `::TestSharedControl_NoViewerHoldsControl` | **Yes** — `TestSharedControl_SecondViewerDoesNotDeferTheAgent` |
| The eleven action tools defer while a human drives | `pkg/tools/browser/tools_control_test.go::TestExecute_ControlLock_InteractiveToolsDeferWhileControlled`, `::TestExecute_ControlLock_ReleaseUngatesInteractiveTools`, `::TestControlledResult_UsesResolvedKey`, `pkg/tools/browser/interact_test.go::TestActionTools_DeferWhenHumanControls_Table` | **No** — must stay green unchanged |
| Read-only tools that stay read-only (`browser_wait`, `browser_list_tabs`) are not gated | `pkg/tools/browser/tools_control_test.go::TestExecute_ControlLock_ReadOnlyToolsAreNotGated` | **Yes** — this test MUST be narrowed to the three remaining exempt tools, not deleted. Deleting it is how the exemption silently becomes universal. |
| Write lease membership | `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased` | **Yes** — re-stated as `TestWriteLease_LeasedIffActionClass` (C1) |
| Audit write-class membership | `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet`, `::TestAudit_ReadOnlyCallsAreNotRecorded`, `::TestAudit_EveryWriteClassCallIsRecorded` | **Yes** — `TestAudit_PerCallAuditedIffActionClass` (C1) |
| No control toggle / hand-back button exists | `BrowserLiveView.controlToggle.test.tsx::never renders a Take control / Release control / Hand to agent button` | **No** — must stay green unchanged |
| One-click take-over from every entry point | `BrowserLiveView.takeTheWheel.test.tsx` (the `UAT fix: one-click take-over…` and `watch-only while the agent is working` blocks) | **Yes** — these tests assert `cancelStream` IS called. They must be **rewritten**, not deleted; the one-click and no-instant-revert assertions must survive with the cancel assertion removed. |
| Tab-strip / omnibox take-then-act | `BrowserLiveView.takeTheWheel.test.tsx` tab-chip cases, `BrowserLiveView.tabStrip.test.tsx` | **Yes** — covered by FR-056's test |
| Tool-policy coverage invariant | `pkg/coreagent/constructor_seed_test.go`, `pkg/coreagent/override_keys_panic_test.go`, the `len(AllStaticToolNames()) == len(DefaultConfig().Sandbox.ToolPolicies)` assertion | **No** — must stay green after the FR-051 three-site edit |
| ADR-057 routing-session consumer set | `pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed` | **No** — amend the allowlist, keep the test |

### Deliberate test rewrites (each is a risk of silently losing coverage)

1. `TestExecute_ControlLock_ReadOnlyToolsAreNotGated` — narrow, do not delete.
2. `declaredControlGateExemptions` — three names move to a new capture roster; the "roster equals
   `readOnlyBrowserTools`" assertion becomes a three-way partition assertion.
3. `BrowserLiveView.takeTheWheel.test.tsx`'s `cancelStream` assertions — invert (assert **not**
   called) while preserving every surrounding assertion about the one-click outcome.

---

## Wave Plan (parallel worktree implementation)

File-disjoint by construction: **no two waves touch the same file.** Waves are independently
committable but not independently compilable — the coupling column names the wave whose symbols a
wave depends on. Recommended order: W4 → (W1 ‖ W2 ‖ W3 ‖ W5 ‖ W7) → W6 → W8.

| Wave | Owns (exclusive) | Delivers | Compile-coupled to |
|---|---|---|---|
| **W1 — Gate semantics & three-way classification** | `pkg/tools/browser/tools.go`, `tools_interact.go`, `tools_snapshot.go`, `tabs.go`, `audit.go`, `key.go`, and the tests `tools_control_test.go`, `control_gate_membership_test.go`, `audit_test.go`, `lease_membership_test.go`, `interact_test.go` | FR-003, FR-010–FR-012, FR-020, FR-024, FR-033–FR-040 | W3 (root-chat id via the resolver interface) |
| **W2 — Lock lifecycle, release, handover-pending state** | `pkg/tools/browser/live.go`, `pkg/tools/browser/manager.go`, `pkg/gateway/browser_ws.go`, `pkg/gateway/websocket.go`, `pkg/gateway/sse.go`, and the tests `live_notcontroller_test.go`, `shared_control_test.go`, `browser_ws_test.go` | FR-028–FR-032, FR-041 (emission), FR-044, FR-047 (state), FR-050, FR-060–FR-062, FR-065 | W4 (frame types) |
| **W3 — Turn engine: bounded attempts, root-chat identity, no-park** | `pkg/agent/browser_deferral.go` (new), `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/agent/routing_session_id_consumer_set_adr057_test.go`, `pkg/agent/browser_deferral_test.go` (new), `pkg/agent/browser_resolver_test.go` (new) | FR-002, FR-004, FR-013–FR-017, FR-021, FR-022, FR-064 | W1 (deferral discriminator) |
| **W4 — Contracts & generated types** | `contracts/components/schemas/*.yaml` (new frame), `contracts/asyncapi.yaml`, `pkg/api/generated/**`, `src/lib/api/generated/**` | FR-042 (schema half) | none — lands first |
| **W5 — SPA live-panel take-over rebuild** | `src/components/browser/BrowserLiveView.tsx`, `src/components/browser/BrowserLiveView.takeTheWheel.test.tsx`, `src/components/browser/BrowserLiveView.handover.test.tsx` (new), `src/components/browser/BrowserLiveView.controlToggle.test.tsx` | FR-001, FR-053–FR-059 | none |
| **W6 — `browser_handover` tool + policy seeding** | `pkg/tools/browser/tools_handover.go` (new), `pkg/tools/browser/tools_handover_test.go` (new), `pkg/tools/browser/register.go`, `pkg/coreagent/core.go`, `pkg/coreagent/browser_handover_seed_test.go` (new), `pkg/config/defaults.go` | FR-046, FR-048, FR-049, FR-051, FR-052 | W1 (class rosters), W2 (handover-pending state) |
| **W7 — Audit event vocabulary** | `pkg/audit/events.go` | the new deferral/handover event constants used by W2 | none |
| **W8 — Thread rendering + e2e** | `src/components/chat/ChatScreen.browser-handover-notice.test.tsx` (new), the chat thread renderer file that owns system entries (**to be confirmed — Ambiguity A5**), `src/lib/ws.ts` consumer wiring, `tests/e2e/browser-control-handover.spec.ts` (new) | FR-042 (render half), FR-043 (replay parity), e2e | W4, W2 |

**Shared-nothing check**: `pkg/tools/browser` is split across W1 (tool bodies + audit), W2
(`live.go`, `manager.go`) and W6 (`register.go`, the new tool) with no file in two waves.
`pkg/gateway` is entirely W2. `pkg/agent` is entirely W3. `src/components/browser` is entirely W5.

---

## Success Criteria

- **SC-001**: Taking the wheel during an in-flight turn produces zero `turn_canceled` transcript
  entries across 10 consecutive takes.
- **SC-002**: With the wheel held, the agent makes at most 3 control-gated browser tool calls per turn.
- **SC-003**: A deferred `browser_screenshot` produces 0 files under the turn's working directory, 0
  media library entries and 0 artifact tags.
- **SC-004**: A delegated child driving its own tab set defers in 100% of takes on its root chat, and
  an agent in an unrelated chat on the same browser defers in 0%.
- **SC-005**: The waiting line is present in the live thread within 1 second of the take, and present
  again byte-identically after a reload.
- **SC-006**: One click takes the wheel and dispatches page input, measured as 1 `browser_control`
  frame and ≥1 input event per gesture, with 0 `release` frames sent for the following 5 seconds.
- **SC-007**: `make verify-contracts`, `npm run typecheck`, `npx vitest run`, `gofmt -l . | wc -l`
  (=0), `golangci-lint run --build-tags=goolm,stdjson` and the CI Go suite all exit 0.
- **SC-008**: With `take_control_enabled: false`, 0 takes succeed, 0 waiting lines are emitted and 0
  agent deferrals occur.

---

## Traceability Matrix

| FR | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-2 | Operator takes the wheel…; The operator's click reaches the page… | `does not call cancelStream…` |
| FR-002 | US-1 | Operator takes the wheel… | `TestTakeover_WritesNoTurnCanceledEntry`; `TestTakeover_SetsNoParkAndNoCancel` |
| FR-003 | US-1 | Take-over mid-navigation… | `TestControlGate_InFlightCallCompletesAfterTake` |
| FR-004 | US-1 | Non-browser work continues… | `TestTakeover_NonBrowserToolStillExecutes` |
| FR-010 | US-1 | The agent's next browser action defers… | `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` |
| FR-011 | US-1, US-3 | The agent's next browser action defers… | `TestDeferralReason_StatesNoRetryAndPromptToResume` |
| FR-012 | US-3 | The agent's next browser action defers… | `TestDeferralPayload_CarriesGateDiscriminator` |
| FR-013 | US-3 | A new turn starts the attempt count at zero | `TestBrowserDeferralLedger_CountsPerTurnNotPerSession` |
| FR-014 | US-3 | The agent is told to stop attempting… | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` |
| FR-015 | US-3 | The agent is told to stop attempting… | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` |
| FR-016 | US-3 | A fourth attempt never reaches the browser | `TestBrowserDeferralLedger_FourthAttemptShortCircuitsWithoutBrowser` |
| FR-017 | US-3, US-4 | A new turn starts…; A delegated worker… | `TestBrowserDeferralLedger_ChildTurnHasItsOwnCounter` |
| FR-020 | US-4 | A delegated worker driving its own tab set defers | `TestControlledResult_ChecksRootChatPanelTabSet` |
| FR-021 | US-4 | A delegated worker driving its own tab set defers | `TestManagerResolver_SurfacesRootChatSessionID` |
| FR-022 | US-4 | — (structural) | `TestRoutingSessionID_ConsumerSetIsClosed` |
| FR-023 | US-4 | A delegated worker driving its own tab set defers | `TestDelegatedChild_DefersOnParentWheelHold` |
| FR-024 | US-4 | An unrelated chat on the same workspace browser… | `TestControlledResult_UnrelatedChatOnSameBrowserDoesNotDefer` |
| FR-025 | US-7 | The next message releases the wheel… | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` |
| FR-026 | US-7 | Escape released the wheel… | `clears the wheel on Escape…`; `TestRelease_DoesNotClearWaitingLine` |
| FR-027 | US-7 | The next message releases the wheel… | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` |
| FR-028 | US-7 | The next message…; The wheel is released even with the panel closed | `TestChatTurnAccept_ReleasesHeldPanelLock`; `TestChatTurnAccept_ReleasesWithPanelClosed` |
| FR-029 | US-7 | The next message releases the wheel… | `TestChatTurnAccept_ReleasesForNonWebchatChannel` |
| FR-030 | US-7 | A different user's message releases the wheel… | `TestChatTurnAccept_ReleaseAuditsActingUserNotHolder` |
| FR-031 | US-8 | A ghost holder…; A live holder… | `TestControlGate_GhostHolderDoesNotDeferTools`; `TestControlGate_LiveHolderDefersTools` |
| FR-032 | US-8 | Two viewers, one wheel | `TestSharedControl_SecondViewerInputStillDispatches`; `TestSharedControl_SecondViewerDoesNotDeferTheAgent` |
| FR-033 | US-5 | Capture attempts defer… (outline) | `TestControlGate_CaptureToolsDeferWhileControlled` |
| FR-034 | US-5 | Wait and tab listing still work… | `TestControlGate_WaitListTabsAndDialogStayUngated` |
| FR-035 | US-5 | — (structural) | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` |
| FR-036 | US-5 | — (structural) | `TestWriteLease_LeasedIffActionClass` |
| FR-037 | US-5 | — (structural) | `TestAudit_PerCallAuditedIffActionClass` |
| FR-038 | US-5 | — (structural) | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` |
| FR-039 | US-5 | Capture attempts defer… (screenshot row) | `TestScreenshot_DeferredCallProducesNoFileNoMediaNoArtifactTag` |
| FR-040 | US-5 | An earlier capture is not retracted | `TestCapture_PreTakeoverArtifactsAreNotRetracted` |
| FR-041 | US-6 | The waiting line appears live… | `TestTakeControl_EmitsWaitingSurfaceFrame` |
| FR-042 | US-6 | The waiting line appears live… | `TestTakeControl_EmitsWaitingSurfaceFrame`; `renders the browser-handover waiting notice…`; `make verify-contracts` |
| FR-043 | US-6 | The waiting line survives a reload | `TestTakeControl_WritesWaitingSurfaceTranscriptEntry` |
| FR-044 | US-6 | Taking the wheel twice emits one waiting line | `TestTakeControl_SecondTakeWithoutReleaseEmitsOneLine` |
| FR-045 | US-3, US-6 | The agent ends its turn saying… | `TestDeferralReason_DrivesAgentNarration` |
| FR-046 | US-9 | The agent hands the browser over… | `TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark` |
| FR-047 | US-9 | After handing over, the agent's own browser attempts defer | `TestHandoverTool_SetsHandoverPendingNotAViewerLock`; `TestHandoverPending_IsNotSubjectToGhostVoiding` |
| FR-048 | US-9 | The agent hands the browser over… | `TestHandoverTool_EmitsWaitingSurface` |
| FR-049 | US-9 | A handover ends the turn normally… | `TestHandoverTool_TurnEndsCompletedNotParked` |
| FR-050 | US-9 | The next message clears an agent-initiated handover | `TestHandoverPending_ClearedByNextPrompt` |
| FR-051 | US-9 | — (structural) | `TestSeed_BrowserHandoverAtAllThreeConstraint6Sites`; `TestConstructorSeed_PolicyMapMatchesCatalog`; `TestDefaults_CeilingLengthMatchesStaticCatalog` |
| FR-052 | US-11 | With take-control disabled… | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` |
| FR-053 | US-2 | The operator's click reaches the page… | `does not call cancelStream…`; `dispatches the same pointerdown…` |
| FR-054 | US-2 | The operator's click reaches the page… | `dispatches the same pointerdown…`; `keeps you-driving priority…` |
| FR-055 | US-2 | The wheel is not handed back… | `does not auto-release the wheel after the take ack…` |
| FR-056 | US-2 | The operator's click reaches the page… | `acquires the wheel and dispatches in one click from the omnibox, the tab strip and Take over` |
| FR-057 | US-2 | The wheel survives the end of the agent's turn; Escape released the wheel… | `does not clear operator-holds-wheel when the agent turn ends`; `clears operator-holds-wheel on a server released status…` |
| FR-058 | US-7 | Escape released the wheel… | `reverts control state and stops accepting input when the socket disconnects mid-control` |
| FR-059 | US-7 | — (structural) | `never renders a Take control / Release control / Hand to agent button` |
| FR-060 | US-7 | — (structural) | existing `handleControl` audit assertions |
| FR-061 | US-1 | The agent's next browser action defers… | `TestDeferralAudit_RecordsSessionTurnViewerUserTabSetAndTool` |
| FR-062 | US-9 | The agent hands the browser over… | `TestHandoverAudit_RecordsAgentAsActor` |
| FR-063 | US-10 | Stop still cancels the turn | existing cancel-path tests |
| FR-064 | US-4 | A deferred sub-agent reports to its parent… | `TestDelegation_NoParkCascadeFromBrowserDeferral` |
| FR-065 | US-11 | With take-control disabled… | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` |

**Scenarios not tied to a single FR** (cross-cutting, still traced): *A question card is pending when
the operator takes the wheel* → `TestAskUserQuestionPending_UnaffectedByWheelTake`, asserting the
ADR §4 claim that the two pending states no longer interact.

---

## Assumptions & Ambiguity Warnings

Every row is a decision made without the operator. **The lead must resolve or accept each before
implementation starts.**

| # | What's ambiguous | Assumption taken in this spec | Question to resolve |
|---|---|---|---|
| A1 | D5 vs. §14 rule 3's biconditional (C1) — gating the capture tools makes them write-leased and per-call audited under the current rule | Split into a three-way classification; capture tools are gated but neither leased nor per-call audited; the ADR-075 §14 rule is amended | Do you accept amending ADR-075 §14 rule 3, and should ADR-085 record that amendment explicitly? |
| A2 | D6's waiting surface has no wire path (C2) | A new contract-first chat frame is added, plus the transcript entry, so live and replay agree | Approve the new wire type, or accept a transcript-only line that is invisible until reload? |
| A3 | D4 says "the gateway releases it", but prompts arrive from webchat WS, SSE and every non-web channel (C7) | Wire the release at the single convergence point every channel's inbound message passes through, not only the webchat handler | Confirm the release must cover non-web channels. If webchat-only is acceptable, say so — it leaves a known stale-lock path. |
| A4 | D7's "passes the lock to the operator" is undefined with no attached viewer (C5), and a never-attached holder is exactly D4's void ghost | A distinct handover-pending state on the live view, not a viewer lock; explicitly exempt from the ghost rule | Confirm handover-pending is a separate state, and confirm it should survive a viewer's absence. |
| A5 | Which SPA component renders a system entry in the chat thread was not identified; only `ChatScreen.*` test-file conventions were confirmed | W8 names the file as "to be confirmed"; the vitest test is named against `ChatScreen.browser-handover-notice.test.tsx` | Name the owning component so W8's file ownership is genuinely exclusive. |
| A6 | N=3 for the attempt bound — the ADR invites justification | N=3, justified in FR-014 (a deferral is 100% reproducible; >1 attempt only helps the model discover the state on the tool it needed) | Accept N=3, or set a different value? |
| A7 | Whether the deferral audit record (D10) is a new event kind or an extension of `EventBrowserLiveControlTaken` | A distinct record is emitted, sharing the take's field set plus turn id, tab set and tool name | New `audit.Event` constant, or a `reason` variant on the existing one? |
| A8 | Whether the waiting line's copy should name the agent | Copy is agent-neutral ("the agent has stopped driving the browser…") | Should it name the agent, and if so which one for a delegated subtree? |
| A9 | Whether `browser_handover` should be seeded for the delegation-tier workers (Explorer, Researcher) as well as Jim and Ray | Seeded for all four browser-capable agents; not seeded for Mia or Ava (they hold no browser tool) | Confirm the roster. |
| A10 | Whether a take on a session with **no** running turn should emit the waiting line | Yes — the line is emitted on every take, because the operator cannot tell whether a turn is running and the message is true either way | Emit always, or only during a running turn? |
| A11 | Whether the bounded-attempt terminal instruction should also apply across a delegated child's *parent* (i.e. shared budget) | No — per FR-017 each turn has its own counter, so a parent and child each get 3 | Should the budget be shared across a delegation subtree? |
| A12 | The ADR is silent on what happens to an in-flight `browser_upload_file` approval (`ask` policy) when the wheel is taken mid-approval | Untouched — the approval gate is orthogonal; the tool defers at the control gate if it is still held when approval lands | Confirm no interaction is intended. |

### Assumptions (non-ambiguous, recorded)

- The control lock stays process memory with no persistence and no boot rehydration; a gateway restart
  loses it, which is already true today and which the ADR accepts.
- WebRTC remains the only live-video path; nothing in this change records frames (ADR-061).
- `pkg/tools/browser` continues not to import `pkg/agent`; all engine-side data reaches the tools
  through the existing `ManagerResolver` seam.
- No back-compat is owed to a pre-change client: the SPA and gateway ship together.

---

## Evaluation Scenarios (Holdout)

> **HOLDOUT — post-implementation verification only.** Not referenced by the test matrix or the
> traceability matrix, and deliberately not derivable from any test name above. Run these by hand (or
> by a separate evaluator) against a built binary with the embedded SPA.

### H1 — The turn genuinely survives, end to end (Happy Path)
- **Setup**: Ask an agent to research a topic that requires both browsing and file writing. Let it
  start browsing.
- **Action**: Take the wheel mid-browse and navigate the tab somewhere unrelated by hand. Do nothing
  else for two minutes.
- **Expected**: The agent finishes its turn. Its output contains the non-browser work it was able to
  do, and a closing statement that it is waiting for the browser. The transcript contains no
  cancellation and no "stopped before it finished" message.

### H2 — One click, every time (Happy Path)
- **Setup**: A turn actively driving the browser.
- **Action**: Click ten times in the frame at ten different moments during the turn, each time typing
  a character immediately after.
- **Expected**: All ten clicks and all ten characters land on the page. At no point does the panel
  revert to "the agent is browsing" without the operator releasing.

### H3 — The operator's credentials never reach the transcript (Happy Path)
- **Setup**: Ask an agent to reach a sign-in page and wait. Take the wheel.
- **Action**: Type a fake but distinctive password string into the form. Release nothing. Let the
  agent's turn finish. Then search the session directory, the media library and the turn's working
  directory for the distinctive string and for any image captured after the take.
- **Expected**: Zero hits, and zero images captured after the take timestamp.

### H4 — A delegated worker really does stand down (Error Path)
- **Setup**: A chat where the primary agent delegates a browsing sub-task.
- **Action**: While the sub-agent is browsing, take the wheel on the parent chat.
- **Expected**: The sub-agent stops driving within its next browser action, reports the block upward,
  and the parent finishes without either turn being recorded as stopped.

### H5 — Nobody is left holding a browser nobody is driving (Error Path)
- **Setup**: Take the wheel. Kill the browser tab hosting the panel (not a clean close) — or drop the
  network — so the viewer vanishes without a detach.
- **Action**: Send a new prompt asking the agent to browse.
- **Expected**: The agent browses successfully. It does not report being blocked by a human.

### H6 — Two people, one browser (Edge Case)
- **Setup**: Two signed-in users with the same session open in two browsers.
- **Action**: User A takes the wheel. User B clicks and types in their own panel. Then user B sends a
  chat message.
- **Expected**: Both users' input reaches the page throughout. The agent defers while A holds. B's
  message releases the wheel, and the audit trail names B as the releasing actor while A is named as
  the prior holder.

### H7 — The disabled installation (Edge Case)
- **Setup**: Set `tools.browser.take_control_enabled` to false and restart.
- **Action**: Try every route into this feature: click the frame, click Take over, use the omnibox,
  use a tab chip, and instruct an agent to hand the browser over.
- **Expected**: Every route is refused with the existing disabled message. No waiting line ever
  appears. No agent ever defers. The panel remains watch-only.

---

## Prerequisites, Setup, Stack, Deployment

- **Hardware / OS**: unchanged — the project's existing Linux/macOS dev targets. This machine (macOS
  arm64) **cannot run the full Go gateway suite**; scoped `-run` only.
- **Required runtimes**: Go per `go.mod`; Node for the SPA; Chromium provisioned by the existing
  browser installer.
- **Required services**: none new. No new runtime dependency (Constraint #1).
- **Network assumptions**: unchanged.
- **Development setup**: unchanged — `make build`, `make test`, `npm run build` + the SPA embed sync,
  `make gen-contracts` for W4.
- **Tech stack**: unchanged. No new library on either side.
- **Deployment / runtime**: unchanged. No new config key; `tools.browser.take_control_enabled` is
  reused as the feature's kill switch.
- **Footprint**: the new state is one flag plus one small per-turn counter — well inside the <10 MB
  security-overhead budget (Constraint #3).
