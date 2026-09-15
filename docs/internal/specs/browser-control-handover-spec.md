# Feature Specification: Browser control handover without turn cancellation

**Created**: 2026-09-09
**Status**: Draft
**Source of truth**: [`docs/internal/architecture/ADR-085-browser-control-handover.md`](../architecture/ADR-085-browser-control-handover.md) — **revision 5 §8**, which records the corrections this spec's second grill produced (§6 = revision 3, §7 = revision 4 remain as written)
**Branch / base commit**: `feat/adr-081-work-first-goal` @ `5622756b`
**Written non-interactively** — every point where the `plan-spec` skill would have stopped for
operator confirmation is recorded in [Assumptions & Ambiguity Warnings](#assumptions--ambiguity-warnings)
instead. Nothing below has been ratified by the operator.

---

## 0. ADR-085 revision 3 §6, restated with implementation detail — READ FIRST

Revision 3's §6 already records most of what follows: **C1 → R3-a**, **C2 → R3-b**, **C5 and C7 →
R3-c**, **C3 and C4 → R3-d**. This section is therefore **not** a list of things the ADR still gets
wrong. It is the same corrections carried down to the level an implementer works at: the exact
rosters, the exact call sites, the exact tests that break. **C6** (the ungated set is six tools, not
four) has no §6 counterpart — it is roster detail an ADR should not carry.

Read C1 and C2 first regardless: an implementer who follows the ADR's *decisions* (D5, D6) without
reading §6 will produce a build that either fails three structural tests or ships an invisible
feature.

**Four corrections below were new after revision 3 and are now recorded on the ADR as §7 (revision
4):** C1's amendment target (revision 3 named "ADR-075 §14 rule 3"; there is no §14 in ADR-075 —
R4-c); the release must fire on operator-originated prompts only, and its audit actor is mostly not
`GatewayUserID` (C7 below — R4-a); a held wheel must expire (FR-031a — R4-b); and D10's audit field
set names a turn id the tool layer does not have (FR-061 — R4-d).

**A second grill produced five further corrections, recorded on the ADR as §8 (revision 5).** Three
of them are defects the *first* round of fixes introduced, so nothing in this document should be read
as settled because it survived one review: R5-a — the release site the first round specified
(`pkg/channels/base.go` calling a `pkg/gateway` helper) **cannot compile**, and the decision now
travels on a fail-closed field (C7 below, FR-029); R5-b — a question-card answer or cancel does
**not** release the wheel, reversing A13 (the predicate the first round prescribed returns `true` on
**Cancel**, so clicking Cancel would have handed the browser to the agent mid-drive); R5-c — no
server-initiated release ever reached the holder's own panel, so both parties believed they held the
wheel (FR-031b); R5-d — D3's "releasing the lock is not a resume" had no mechanism, and now has one
(FR-026a); R5-e — the idle timer's liveness signal counted the wrong thing and collided with the
existing tab reaper (FR-031a).

### C1 — BLOCKER. D5 collides with the §14.2 rule 3 biconditional and three structural tests

> **Where the rule actually lives (corrected 2026-09-09).** ADR-085 revision 3 §6 R3-a, and this
> spec's earlier draft, both cite "ADR-075 §14 rule 3". **There is no §14 in ADR-075** — that ADR has
> no such section. The biconditional is a *spec* rule, not an ADR rule, and it lives in three places
> that all have to move together:
> - `docs/internal/specs/browser-workspace-ownership-spec.md` **§14.2 Rules, rule 3** (the normative
>   table, one row per tool) and its **FR-019a** / **AC5** (`leased ⟺ controlledResult-gated`);
> - the same document's **§12 A17**, which is where the biconditional was reasoned into existence and
>   where `browser_handle_dialog`'s double exemption is justified;
> - `docs/internal/specs/browser-agent-capability-spec.md`, which **restates** the rule for the six
>   D2 tools it registers (its §3 symbol table's `tools.go::controlledResult` row, and its §12 A-22).
>
> Amending the rule therefore means editing two spec documents, not an ADR section. See **FR-035a**,
> and the revision-4 note appended to ADR-085 §6.

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
audited), **exempt** (none of the three). §14.2 rule 3's biconditional is narrowed to
`leased ⟺ action-class`, and a second biconditional is added: `control-deferred ⟺ action ∪ capture`.
See FR-033 – FR-035, **FR-035a** (the cross-document amendment) and W1 in the wave plan.

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
5-step process), paired with the transcript entry so live and replay agree. See FR-041 – FR-044,
**FR-043a** (the replay half, which must emit the *same* frame type) and W4.

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
`LiveViewRegistry::EnsureControlForInput` was designed to steal from and that D4 declares **void** —
so an agent-initiated handover cannot be expressed as a control-lock holder without contradicting D4.
(That method is cited here for the *shape* of the ghost, not as a live path: FR-031 records that it
has no production caller today and that the gate's own ghost rule is implemented separately.)

**Resolution:** model agent-initiated handover as a distinct, explicitly-set state on the live view
(a handover-pending flag), separate from the viewer control lock. It defers tools like a held lock
does, is cleared by the same next-prompt release path, and is *not* subject to the ghost rule.
ADR-085 §6 R3-c records this correction; see FR-046 – FR-050 for the mechanism.

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
reproduces exactly the stale-lock class D4 exists to close.

**But `PublishInbound` is not a safe place to put the release either, and this is the single most
dangerous mistake available in this feature.** `MessageBus.PublishInbound` (`pkg/bus/bus.go`) is the
convergence point for **synthetic** traffic as well as human prompts. There are exactly **six**
non-test call sites (`grep -rn "PublishInbound(" --include="*.go" . | grep -v _test.go` returns
seven lines; the seventh is `pkg/bus/bus.go:86`, the **method declaration**, not a call site):

| Call site | Origin | Releases the wheel? |
|---|---|---|
| `pkg/gateway/websocket.go` (webchat `message` handler) | human | **yes** |
| `pkg/gateway/sse.go` (`POST` chat over SSE) | human | **yes** |
| `pkg/channels/base.go::HandleMessage` | human on Telegram / Slack / Discord / … | **yes** |
| `pkg/gateway/ws_ask_user.go::DispatchResume` | a question-card resume | **no** — see below and A13 |
| `pkg/agent/async_notifier.go` (`Channel: "system"`) | a background tool or delegate **finishing** | **no** |
| `pkg/agent/loop.go` (goal-loop follow-up re-injection, `Sender.CanonicalID == goalLoopFollowUpSenderID`) | the engine talking to itself | **no** |

A release fired on *any* inbound message means **a delegate finishing in the background hands the
browser back while the operator is mid-way through typing a password into it.** That is the exact
harm D5 exists to prevent, re-introduced through the release path. The release must therefore be
decided by the **origin of the prompt at its publish site**, never by the bus. See FR-029, FR-029a
and Ambiguity A3.

> **C7.1 — The question-card row flipped in revision 5, and the reason is a verified predicate bug.**
> The first round specified `ws_ask_user.go::DispatchResume` as a *conditional* release site, gated
> on that file's existing `resumeIsUserInitiated(set)`. That predicate's **first branch is
> `if set.Status == askuser.StatusCancelled { return true }`** (`pkg/gateway/ws_ask_user.go`) — it
> treats a cancel as user-initiated because, for its own purpose (goal/loop origin gating), a human
> clicking Cancel *is* a human acting. Wired to the browser release, that means **clicking Cancel on
> a question card hands the browser to the agent while the operator is driving it.** The operator's
> ratified decision is therefore that a question-card resume — answer *or* cancel — never releases
> the wheel. Full reasoning in A13; the release set is three sites, not four.

> **C7.2 — Where the release DECISION lives, and where the release ACTION lives. They are not the
> same place, and the first round put the action somewhere it cannot exist.**
> Round 1 required `pkg/channels/base.go::HandleMessage` to call a `pkg/gateway` helper. Two
> independent reasons that cannot be built:
> - **Import direction.** `pkg/gateway/gateway.go` imports `pkg/channels`; nothing under
>   `pkg/channels` imports `pkg/gateway` (`grep -rn "omnipus/pkg/gateway" pkg/channels/` returns
>   nothing). A call the other way is an import cycle.
> - **No session identity at that site.** The `bus.InboundMessage` built at `pkg/channels/base.go`
>   sets `Channel`, `InstanceID`, `Sender`, `ChatID`, `Content`, `Media`, `Peer`, `MessageID`,
>   `MediaScope`, `Metadata` and `UserInitiated` — and **not `SessionID`**. The lock is keyed on
>   `PanelTabSetID(chatSessionID)`; that id is resolved downstream in `pkg/agent`. A helper called
>   there would have nothing to release against.
>
> **Resolution (FR-029).** The *decision* stays at the publish site, carried by a new fail-closed
> field `bus.InboundMessage.OperatorPrompt bool` modelled exactly on `UserInitiated`'s doc contract:
> set `true` at the three operator sites and **nowhere else**, so a publish site added later fails
> closed (does not release) rather than open. The *action* is performed once the message has been
> resolved to a session — in `pkg/agent/loop.go::processMessage` — through a **gateway-registered
> hook**, because two things the release must do (push the FR-031b frame to the former holder's
> connection, and emit through `pkg/gateway/browser_ws.go`'s audit record shape) are only reachable
> from the gateway. `UserInitiated` itself **must not** be reused: `pkg/gateway/sse.go` builds its
> message without it, so keying on it silently reproduces this same stale-lock hole for SSE, and
> widening it would change goal/loop origin gating (ADR-049 Gap #8) as a side effect of a browser
> change.
>
> **`processMessage` is the convergence point for cron too, and that is exactly why the gate is the
> field and not the location.** `pkg/agent/loop.go::ProcessDirectWithChannel` builds a
> `bus.InboundMessage{… Sender: {CanonicalID: "cron"} …}` and hands it to `processMessage` — it
> simply never **publishes** it to the bus. Anyone who places the release at `processMessage` and
> gates it on anything other than `OperatorPrompt` releases the wheel on every cron fire.

---

## Existing Codebase Context

> GitNexus was not queried for this spec; the analysis below is from direct `Read`/`Grep` at
> `fa737b4f`. That fallback is sanctioned by the skill when the graph does not cover the question —
> here the questions were exact-text ones (which call sites, which roster members), not structural.

### Symbols Involved

| Symbol | Role | Note |
|---|---|---|
| `pkg/tools/browser/tools.go::controlledResult` | modifies | The deferral gate. Reason text, and the class split (C1). |
| `pkg/tools/browser/live.go::LiveViewRegistry.IsControlled` / `.Controller` | unchanged | Lock read for the **status/presentation** path. Process memory only, no persistence, no boot rehydration. The tool gate stops using them — see the next row (FR-031). |
| `pkg/tools/browser/live.go::LiveViewRegistry.IsControlledByLiveViewer` | **new** | The tool gate's read: `lv.controller != "" && lv.viewers[lv.controller]`, under `lv.mu`. `IsControlled` is `Controller(sessionID) != ""` and `Controller` returns `lv.controller` unconditionally, so the existing pair cannot express the ghost rule (FR-031). |
| `pkg/tools/browser/live.go::LiveView.takeControl` / `.releaseControl` / `.ensureControlForInput` / `.detach` | extends | Lock lifecycle; `detach` clears on clean close only. Handover-pending (C5) and the FR-026a stand-down latch land here. |
| `pkg/tools/browser/live.go::ControlSink` / `.snapshotControlSinksExceptLocked` / `broadcastControl` | modifies | The take/release fan-out. It **excludes the acting viewer by construction**, and `ControlSink`'s doc comment states that as an invariant ("the acting viewer … already gets an authoritative `browser_status` frame as the direct response to its own `browser_control` request"). That is only true for a release the holder itself requested — FR-031b amends both the behaviour and the comment. |
| `pkg/tools/browser/manager.go::ViewerHeartbeat` / `.ViewerAttached` / `.ViewerDetached` / `.ReapIdleSessions` | calls | Viewer liveness and the existing `idle_ttl` tab reaper. `ViewerHeartbeat` is stamped from the live panel's WS `PongHandler` (`pkg/gateway/browser_ws.go`) — the "somebody is still watching" proof FR-031a's liveness rule needs. `ReapIdleSessions` today does **not** touch any `LiveView`, which is the gap FR-031a closes. |
| `pkg/tools/browser/register.go::resolveTurn` / `resolveTurnScope` / `resolveTurnTabSet` | unchanged | The one path every browser tool starts with. **It is deliberately NOT the root-chat-id seam** — see the next row and FR-021. |
| `pkg/tools/base.go` (`ctxKeyTranscriptSessionID` and its `WithTool…` / `ToolTranscriptSessionID` pair) | extends | Gains the root-chat-session-id tool-context key (FR-021). Chosen over changing `ManagerResolver.ManagerFor`'s signature, which would break the interface and every fake in `pkg/tools/browser`'s tests, and would drag `register.go` into three waves at once. |
| `pkg/bus/types.go::InboundMessage` | modifies | Gains `OperatorPrompt bool` — the fail-closed release discriminator (C7.2, FR-029), modelled on the `UserInitiated` field directly above it. |
| `pkg/agent/loop.go::processMessage` | extends | Where the inbound message is resolved to a session, and therefore where the release **action** fires — gated on `OperatorPrompt`. Also the convergence point for `ProcessDirectWithChannel`'s cron message, which is why the gate is the field (C7.2). |
| `pkg/gateway/browser_ws.go::newBrowserWSHandler` | extends | Registers the release hook on the `*agent.AgentLoop` it is already handed. Precedent for a gateway-registered callback on the loop: `pkg/agent/loop.go::AgentLoop.SetReloadFunc`. No `pkg/gateway/gateway.go` edit is required. |
| `pkg/gateway/websocket.go::WSHandler.resolveSessionConnsLocked` | calls | The only path from a session id to that session's live chat WS connections. The waiting-line frame reaches the thread through it (FR-042); `BrowserWSHandler` holds only an `*agent.AgentLoop` today and must gain the handler reference. |
| `pkg/tools/browser/manager.go::PanelTabSetID` / `OperatorSessionID` / `focusedTabSet` | calls | Panel-vs-agent tab-set mirror. The D2.3 gap lives in the seam between the first two. |
| `pkg/tools/browser/audit.go::writeClassBrowserTools` / `readOnlyBrowserTools` / `recordBrowserAction` | modifies | Classification (C1). |
| `pkg/tools/browser/lease.go::acquireWrite` | unchanged | Must NOT gain capture-class members (C1). |
| `pkg/gateway/browser_ws.go::handleControl` / `auditControl` / `auditRelease` / `detach` | modifies | Take/release wire path; `TakeControlEnabled` refusal. |
| `pkg/gateway/websocket.go` / `sse.go` message handlers, `pkg/channels/base.go::HandleMessage` | extends | The three **operator-originated** `PublishInbound` sites; each sets `OperatorPrompt: true` and nothing else (C7.2, FR-029). |
| `pkg/gateway/ws_ask_user.go::DispatchResume`, `pkg/agent/async_notifier.go`, `pkg/agent/loop.go` (goal follow-up re-injection) | unchanged | The three **non-releasing** `PublishInbound` sites. They MUST leave `OperatorPrompt` false (C7.1, C7.2, FR-029). `ws_ask_user.go::resumeIsUserInitiated` stays exactly as it is — it is correct for its own caller and MUST NOT be reused here. |
| `pkg/tools/result.go::ToolResult` | modifies | Gains the structural deferral carrier (FR-012a). Today there is no non-prose channel from a tool to the engine. |
| `pkg/gateway/replay.go` (`EntryTypeSystem` branch) | modifies | Replay half of the waiting line; today it emits a generic `ReplayMessageFrame` (FR-043a). |
| `src/store/chat.ts` `case 'goal_status'` / `buildGoalAckInsertion` / `goalAckMessageId` | pattern | The exact precedent for a system-role line synthesized from a frame with a deterministic, idempotent id (FR-044). |
| `src/components/chat/ChatScreen.tsx::SystemMessage` / `VirtualSystemMessageRow` | extends | The two renderers a `role:'system'` `ChatMessage` reaches (closes A5). |
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
| `tools.ToolResult` (new field) | MEDIUM | Every package constructing a `ToolResult` compiles unchanged (the field is additive and `json:"-"`), but `pkg/tools/result_test.go` and `pkg/api/generated/contract_test.go` must both stay green — the field MUST NOT cross the wire. |
| `humanControlDeferralMarker` (the FR-011 reason rewrite) | **HIGH** | `lease_membership_test.go` (defines it), `implicit_acquisition_test.go` (6 assertions), `operator_takeover_test.go` (3 assertions). The constant is a *substring* match, so a reason rewrite that drops the phrase reddens three files at once (B8 / the Regression Requirements table). |

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
   anything, **When** the agent next attempts browser work, **Then** it still defers, the agent does
   not silently resume driving, and the waiting line stays visible.
6. **Given** the operator holds the wheel, **When** a background delegate or a scheduled follow-up
   the agent itself produced arrives on that session, **Then** the wheel is **not** released.
7. **Given** the operator has stood the agent down — holding the wheel, having released it without
   prompting, or having been handed the browser by the agent — **When** the idle window elapses with
   no proof that anyone is still there, **Then** the system stands the agent back up itself, records
   why, and tells the operator it did.
8. **Given** the operator answers or cancels a structured question card while holding the wheel,
   **When** the resumed turn attempts browser work, **Then** it still defers — answering a card is
   not a prompt for this purpose.

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
3. **Given** an operator holds the wheel on a running install, **When** take-control is switched off
   on that install, **Then** the hold is given up on its own and the agent can drive again — the
   feature cannot be left half-on with an agent locked out and no surface to unlock it.

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
- When the operator releases the wheel and sends nothing, the system does not resume the agent — it
  keeps declining browser work until a prompt arrives or the idle window elapses.
- When a background delegate, an engine follow-up, or a question-card answer or cancel produces an
  inbound message on the session while the agent is stood down, the system leaves the state exactly
  where it is.
- When the system itself gives the browser back — an idle expiry, a take-control switch-off, or a
  session being deleted — it tells the operator's still-open panel, so the panel and the server never
  disagree about who is driving.
- When nobody has proved they are still there for the idle window, the system stands the agent back
  up itself and says so.

---

## Edge Cases

- **Take-over mid-navigation.** A page load is in flight when the wheel is taken. Expected: the load
  completes; the operator ends up on whatever the navigation produced; the agent's *next* attempt
  declines.
- **The agent never touches the browser again.** Expected: nothing declines, the turn runs to a normal
  end, and the waiting line is the only trace of the takeover.
- **The wheel is taken while a question card is awaiting an answer.** Expected: no interaction while
  the card is pending — **the composer lock and the wheel are independent**, and no second suspended
  state is created. **Answering the card does not release the wheel, and neither does cancelling
  it** (FR-029, A13). The operator still holds the browser after the resumed turn begins, and the
  resumed turn's browser attempts defer like any other. The only thing that returns the browser is a
  message the operator composed, an explicit release, or the idle window.
- **A background delegate finishes while the operator drives.** Expected: nothing happens to the
  wheel. The result reaches the thread as usual and the operator keeps driving.
- **The operator takes the wheel, keeps the panel open and reads for twenty minutes.** Expected:
  nothing expires. An attached panel that is still answering the server's keep-alive is proof someone
  is there, and the idle window is not allowed to hand the agent a page a human is looking at
  (FR-031a). This is the case the first draft got wrong: it counted only *input*, so a reading
  operator was silently released after 900 s and the agent resumed capturing their screen.
- **The operator takes the wheel and walks away — laptop shut, panel gone.** Expected: no proof of
  life reaches the server, so after the idle window the hold and the stand-down are given up
  server-side, an operator-visible line says so, and the next scheduled or heartbeat turn on that
  session can drive the browser again (FR-031a).
- **Two viewers, one wheel.** Expected: only the holder's takeover declines the agent; the second
  viewer's input still reaches the page and neither declines the agent nor blocks the holder.
- **Abrupt disconnect of the holder.** Expected: the agent is not declined by the abandoned wheel,
  and the stand-down latch goes with it — a holder that vanished never deliberately stood anything
  down (FR-026a, FR-031).
- **Escape, then the operator walks away.** Expected: the agent does not resume driving on its own —
  the stand-down latch keeps declining browser work (FR-026a) — and the waiting line stays until a
  message arrives or the idle window elapses.
- **The server gives the browser back while the panel is still open.** Expected: the panel finds out.
  An idle expiry, a take-control switch-off or a session deletion pushes a `released` status to the
  former holder's own connection, not just to everyone else (FR-031b). Without this the panel keeps
  claiming the operator is driving, refuses to re-take, and the agent drives underneath it.
- **Handover with nobody watching.** Expected: the handover still takes effect and is cleared by the
  next message, whether or not a panel was ever open. With no viewer there is no liveness signal at
  all, so the idle window runs from the handover itself (FR-031a).
- **The tab set is reaped for inactivity while the wheel is held.** Expected: it is not. A tab set
  whose live view is controlled, handover-pending or stand-down-latched is exempt from `idle_ttl`
  reaping; if it is reaped anyway (last tab closed by the operator), the reap goes through the same
  audited release path rather than orphaning a lock on a live view whose tabs are gone (FR-031a).
- **The session is deleted, or its workspace unmounted, while the wheel is held.** Expected: the
  hold, the handover-pending state and the stand-down latch are cleared through the same audited
  path, the still-open panel is told (FR-031b), and the FR-043 transcript write is skipped rather
  than attempted against a directory that no longer exists (FR-043, FR-050).
- **Gateway restart while the wheel is held.** Expected: the wheel is lost (it is process memory
  today and stays so); the agent is not declined after the restart; nothing needs recovering. **Known
  and accepted:** the waiting line already written to the transcript is replayed on reload and reads
  as though the agent were still standing down. It is stale, not wrong — sending a message does
  return the browser — and no automated surface distinguishes the two states. What the restart must
  **not** do is suppress a genuine fresh takeover, which is why FR-044's notice id is derived from a
  timestamp rather than a counter that restarts at zero.
- **Take-control disabled on a fresh install.** Expected: the whole feature is unreachable.
- **Take-control switched off while a wheel is held.** Expected: the hold, the handover-pending state
  and the stand-down latch are given up within one sweeper tick and the panel is told — the feature
  cannot be left half-on with an agent locked out and the only release path disabled (FR-052).

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
  true and a `reason` string, **and** which carries a typed, non-string discriminator on the result
  value itself (FR-012a). The engine must be able to detect it without reading `ForLLM` at all — the
  JSON body is for the model, the typed field is for the engine.
- The attempt bound is **3** per turn. The 3rd declined attempt carries the stop instruction; the 4th
  and later never contact the browser.
- A declined capture attempt produces **zero** of: a file under the turn's working directory, a media
  library entry, an artifact tag.
- Take-control disabled ⇒ **zero** takes succeed and **zero** waiting lines are emitted.
- The waiting line appears **at most once** per unbroken held period.
- Existing take/release audit records keep their current field set; the new decline record carries
  session id, **root chat session id**, viewer id, acting user, tab set, and tool name. **Turn id is
  deliberately NOT in the set** — see FR-061 for why it cannot be, and what carries its role instead.
- The wheel is never held indefinitely: an idle hold expires server-side (FR-031a). "The operator's
  next prompt" is the *intended* release, not the *only* one.
- Releasing the lock does **not** re-open the gate. A stand-down latch (FR-026a) outlives the lock and
  is cleared by exactly two things: the FR-029 prompt release and the FR-031a idle expiry. It is
  **voided**, never merely cleared, by the FR-031 ghost condition.
- Every release the holder did not itself request produces exactly **one** unsolicited
  `browser_status{state:"released"}` on the former holder's own connection (FR-031b), in addition to
  the existing `control_only` broadcast to everyone else.
- The release discriminator is a **field**, not a call site: `bus.InboundMessage.OperatorPrompt` is
  `true` at exactly three non-test assignment sites and `false` everywhere else, including on a
  zero-valued `InboundMessage` (FR-029, FR-029a).

---

## Integration Boundaries

### Live browser panel (SPA ↔ gateway WebSocket)

- **Data in**: take/release requests, viewer input, viewport and tab actions.
- **Data out**: control-state acknowledgements, tab strip, error notices, WebRTC signalling, **and (new
  in this change) an unsolicited `browser_status{state:"released"}` to a former holder whose wheel the
  server took back** (FR-031b).
- **Contract**: the existing browser WebSocket channel and the existing `BrowserStatusFrame` schema.
  This change adds no new frame **type** here — but it does add a new *direction* of use: today every
  `state:"released"` frame is a direct reply to that connection's own `browser_control{release}`, and
  after FR-031b one can arrive unsolicited. The frame must **not** carry `control_only: true` — that
  flag makes the SPA apply only the control-ownership axis and return, so a `control_only` frame
  cannot clear the holder's own `isControlling`.
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
  **The rewritten reason MUST still contain the exact substring `human is currently controlling`.**
  That substring is `pkg/tools/browser/lease_membership_test.go::humanControlDeferralMarker`, and it
  is consumed as a `Contains` / `NotContains` assertion by three files — `lease_membership_test.go`
  itself, `implicit_acquisition_test.go` (6 assertions) and `operator_takeover_test.go` (3
  assertions). If an implementer prefers wording that drops the phrase, all three files MUST be
  amended in the **same commit**; the marker constant must never be left matching prose that no
  longer exists. See the Regression Requirements table.
- **FR-012**: The deferral's **model-facing body** MUST carry the existing `deferred: true` key plus
  a `gate` field naming the control gate (`"browser_control"`), so a future second deferral source is
  distinguishable in the transcript.
- **FR-012a**: The deferral MUST **also** carry a structural discriminator on the result *value*,
  because the engine cannot be asked to parse prose and today there is nothing else it could read.
  `pkg/tools/browser/tools.go::controlledResult` returns
  `tools.NewToolResult(fmt.Sprintf("%s: %s", toolName, string(body)))` — a **prose-prefixed** JSON
  string in `ForLLM`, and `pkg/tools/result.go::ToolResult` has no field that could carry the
  classification. Therefore:
  - `pkg/tools/result.go` gains `Deferred *ToolDeferral` with a `json:"-"` tag (it is engine-internal
    plumbing and MUST NOT cross the gateway/SPA boundary — Constraint #8), and a small
    `type ToolDeferral struct { Gate string; Reason string }` in the same file.
  - `controlledResult` populates it (`Gate: "browser_control"`) alongside the JSON body it already
    returns. The two are the same fact stated for two different readers.
  - The turn engine's ledger (FR-013) reads `result.Deferred != nil && result.Deferred.Gate ==
    "browser_control"`. It **MUST NOT** inspect `ForLLM`, `strings.Contains` it, or unmarshal it.
  - `Deferred` MUST be nil on every non-deferred result, so "is this a deferral" is a nil check and
    not a value comparison.
  - **The field is meaningful only on a SYNCHRONOUS result.** It rides the tool's own `*ToolResult`
    pointer back through `ToolRegistry.Execute` and `normalizeToolResult` (which mutates in place),
    exactly as the existing `Err` / `Messages` fields on the same struct do — but a result that
    reaches the engine by any other route (a background dispatch reconstituted from a stored payload,
    an async notifier round-trip) has crossed a serialisation boundary that `json:"-"` deliberately
    strips. No control-gated browser tool is dispatched that way today, and none may be without
    revisiting FR-013's ledger.
  - **Owning wave: W0** (see the wave plan; `pkg/tools/result.go` was previously unowned and the file
    blocks both W1 and W3).
- **FR-013**: The turn engine MUST maintain a per-turn count of control-gate deferrals, on
  `pkg/agent/turn.go::turnState`, modelled on `pkg/agent/tool_denial.go::turnDenialLedger`
  (C4). It MUST NOT be keyed on the transcript session id alone.
- **FR-014**: N MUST be **3**. *Justification, since the ADR invites one:* the deferral is a
  100%-reproducible condition, not a transient one — a retry cannot succeed until a human acts, so
  the only value of >1 attempt is letting the model discover the state on a tool it actually needed.
  Two attempts is enough for that; three leaves one attempt of slack for a multi-tool browser
  sequence (e.g. `browser_snapshot` then `browser_click`) to surface the state on the tool the model
  cares about. It is deliberately **much tighter** than the existing aggregate per-turn denial
  ceiling, `pkg/agent/tool_denial.go::turnDenialBudget = 10`: a denial can be recovered from within
  the turn (the user may approve the next one), a control deferral cannot. Larger values only add
  identical noise.
- **FR-015**: On the 3rd deferral in a turn, the result handed to the model MUST additionally instruct
  it to state plainly what it is waiting for and to stop attempting browser work for the remainder of
  the turn.
- **FR-016**: After the bound is reached, every later control-gated browser tool call in the same turn
  MUST short-circuit **in the engine, before dispatch** — no CDP contact, no lease acquisition, no
  audit action row, and no entry into `pkg/tools/browser` at all — and return the same terminal
  instruction.
- **FR-016a**: FR-016's short-circuit needs a classifier the engine can reach, and today there is
  none. Short-circuiting in `pkg/agent` means the browser gate never runs, so the engine must decide
  from the **tool name** alone — but `pkg/tools/browser/audit.go`'s `writeClassBrowserTools` and
  `readOnlyBrowserTools` are unexported, and `pkg/tools/browser` MUST NOT be imported by `pkg/agent`
  in the reverse direction either (that is a standing assumption of this spec). Therefore:
  - `pkg/tools/browser` MUST export `func ControlGatedToolNames() []string`, returning the
    **action ∪ capture** union — derived from the same three rosters FR-035 defines, never from a
    fourth hand-written list.
  - `pkg/agent/browser_deferral.go` consumes it once at construction and holds a set. (`pkg/agent`
    already imports `pkg/tools/browser` for the manager wiring, so this adds no new edge.)
  - `pkg/tools/browser/control_gate_membership_test.go` MUST assert that the exported view equals
    `action ∪ capture` element-for-element, so the two cannot drift: a tool added to a roster and
    forgotten in the exported function is a red test, not a silently un-short-circuited tool.
  - **Owning waves: W1 exports and asserts it; W3 consumes it.**
- **FR-017**: The counter MUST be per turn. A delegated child turn MUST have its own counter, and a
  new turn MUST start at zero.

### C. Gate coverage across every reachable tab set (D2.3)

- **FR-020**: The control gate MUST be evaluated against **both** the tab set the call resolved
  (`sessionKey(key, owner)` from `pkg/tools/browser/register.go::resolveTurnScope`) **and** the tab set
  the live panel would have taken the lock on for the ROOT chat this turn belongs to
  (`pkg/tools/browser/manager.go::PanelTabSetID(rootChatSessionID)`). A hold on either defers.
  - **Scope of the second check, stated because it looks wider than it is.** `PanelTabSetID` composes
    its result with the **resolving manager's own** `m.key` (`return sessionKey(m.key, owner)`), and
    `resolveTurnScope` gets `mgr` and `key` from the same `ManagerFor(ctx)` call — so the two checks
    are always on the *same* `BrowsingKey` by construction, and differ only in `TabOwner`. If the
    root chat's panel is attached under a **different** `BrowsingKey` (a different workspace's
    manager), this check does not see it and the agent **does not defer**. That is intended, not a
    hole: FR-024's "an unrelated chat on the same workspace browser must not defer" is the same rule
    seen from the other side, and a cross-workspace hold is further away still.
  - **A resolver that cannot supply a root chat id fails OPEN**: if `rootChatSessionID` is empty, the
    second check is **skipped entirely** and only the resolved-key check runs. This is required, not
    merely tolerated — `pkg/tools/browser/implicit_acquisition_test.go`'s "the lock is consulted
    against the resolved key" case asserts `NotContains(humanControlDeferralMarker)` while a lock is
    held on `TabOwnerSession("some-other-chat")`, and a second check that invented a root-chat id
    would turn that assertion red for the wrong reason. Failing closed here would also mean every
    non-chat turn (cron, heartbeat, task) defers forever.
- **FR-021**: The root chat id MUST be sourced from `pkg/agent/turn.go::turnState.routingSessionID`
  (ADR-057 FR-011) and MUST reach the browser tools as a **tool-context key**, not through the
  `ManagerResolver` interface. `pkg/tools/browser` MUST NOT import `pkg/agent`.
  - **The seam is `pkg/tools/base.go`**, which already carries `ctxKeyTranscriptSessionID` and its
    `ToolTranscriptSessionID(ctx)` reader. A sibling key and reader
    (`WithToolRootChatSessionID` / `ToolRootChatSessionID`) is added there; `pkg/agent` sets it where
    it already builds the tool context, and `controlledResult` reads it. `controlledResult`'s
    signature gains a `ctx context.Context` first parameter — it is
    `controlledResult(mgr *BrowserManager, key BrowsingKey, owner TabOwner, toolName string)` today
    and its eleven call sites all live in files W1 already owns.
  - **Why not `ManagerResolver.ManagerFor`, which the first draft named.** Three costs, none of them
    paid back: it changes an exported interface and therefore every fake implementing it in
    `pkg/tools/browser`'s test files; the interface is declared in
    `pkg/tools/browser/register.go`, which W6 must own outright for the `browser_handover`
    registration, so routing the id through it would put one file in three waves at once; and the
    root chat id is a property of the **turn**, exactly like `ToolTranscriptSessionID`, not of the
    manager resolution, so the context key is also the more honest home. This is the resolution of
    what was an open question in the first draft — the draft named the requirement and left the
    mechanism unstated, which is a gap, not a choice.
- **FR-022**: `pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed`
  MUST be amended in the same commit to name the new reader, with the role-B justification recorded
  (C3). Adding the reader without amending the allowlist is a build break, and amending the allowlist
  without the justification comment is a silent widening of ADR-057 FR-014.

  **This amendment is only meaningful if it is made in four specific places.** The test does not scan
  the package; it walks a **hardcoded file list** and asserts exact counts. An implementer who adds a
  reader in a new file and "amends the test" by touching nothing else gets a green test that proves
  nothing — the new file is never parsed. All four MUST be done together:
  1. The new reader lives in **`pkg/agent/browser_deferral.go`** (the same new file W3 owns for the
     ledger), not in `turn.go` or `loop.go`. Putting it in an already-scanned file would hide it
     inside an existing bucket's count.
  2. `"browser_deferral.go"` MUST be added to **`u19RoutingSessionIDScanFiles`** (today an exhaustive
     eight-entry list: `steering.go`, `turn.go`, `cancel_prearm.go`, `subturn.go`, `loop.go`,
     `cancel.go`, `events.go`, `session_messaging_wire.go`), and that list's own doc comment — which
     claims the list is what `grep -rl "routingSessionID"` returns — MUST be re-verified and updated
     to nine.
  3. `u19ClassifyRoutingSessionIDRead` MUST gain a **fifth bucket**,
     `u19BucketBrowserGate = "browser control-gate root-chat key"`, keyed on
     `(browser_deferral.go, <the reading function's name>)`, with the **role-B (routing/scope)**
     justification written into the classifier as a comment: the read supplies a *scope key* for a
     gate decision, exactly the role the existing role-B predicates play, and never a persistence or
     addressing identity.
  4. `const wantTotal` MUST be raised from **30** to `30 + <the number of new reads>` **and** the new
     bucket MUST get its own exact-count assertion, in the same style as the existing four
     (`role-B = 7`, `pre-arm = 3`, `WS-stamping = 19`, `inheritance = 1`). Raising only the total
     without a per-bucket count is how a read drifts into the wrong classification unnoticed.
- **FR-023**: A delegated child driving its **own** tab set MUST defer while the operator holds the
  wheel on its root chat.
- **FR-024**: An agent in an **unrelated** chat sharing the same workspace browser (same
  `BrowsingKey`, different root chat) MUST NOT defer. A gate that defers on "any tab set of this
  browser is controlled" is a defect, not a conservative choice.

### D. Resume is the next prompt (D3, D4)

- **FR-025**: The operator's next message MUST run as an ordinary turn. No resume dispatcher, no
  injected page state, no synthetic message, no persisted handover record.
- **FR-026**: Releasing the wheel (Escape, entering annotate mode, closing the panel) MUST NOT resume
  the agent and MUST NOT retract the waiting line. **The mechanism is FR-026a**, and without it this
  requirement has no implementation at all: `releaseControl` / `detach` clear `lv.controller`, so
  `controlledResult`'s gate re-opens on the very next call.
- **FR-026a**: A **stand-down latch** MUST exist, because ADR-085 D3's "releasing the lock is not a
  resume" is otherwise unimplemented and unobservable. Concretely:
  - **State**: a `standDownUntilPrompt bool` on `pkg/tools/browser/live.go::LiveView`, beside
    `controller` and the FR-047 handover-pending flag, under `lv.mu`.
  - **Set** on every transition **into** a stood-down state: a successful `takeControl` /
    `ensureControlForInput` grant to a human viewer, and an FR-047 handover. Setting it is
    idempotent within one unbroken stand-down (it shares FR-044's "transition into", not "every
    take").
  - **Consulted** by `controlledResult` alongside the lock and the handover-pending flag, under the
    **same FR-020 two-key evaluation** (the resolved tab set and the root chat's panel tab set). A
    latch on either defers.
  - **Cleared by exactly two things**: the FR-029 prompt release (across FR-050's whole reachability
    set) and the FR-031a idle expiry. Not by Escape, not by annotate mode, not by closing the panel,
    not by the end of a turn, not by a new turn starting.
  - **Voided — not cleared — by the FR-031 ghost condition.** When the gate finds a holder that has
    left `lv.viewers`, it voids the hold *and* the latch in the same evaluation. Without this
    exception FR-026a silently repeals US-8 and reddens
    `TestControlGate_GhostHolderDoesNotDeferTools`: a crashed panel would stand the agent down
    forever. The rule that makes this coherent, stated so nobody "simplifies" it away: **the latch
    records a deliberate stand-down, and a deliberate stand-down requires a deliberate release.** A
    viewer whose socket died released nothing.
  - **Scope note.** The grill called for a *session*-scoped latch. It is specified per **tab set**
    instead, on the `LiveView`, because that is the granularity the gate and the FR-050 clearing set
    already work at — a session-scoped latch would need its own second reachability model and would
    not clear correctly for a delegated child's own tab set.
  - **Why the latch and not the cheaper alternative.** The alternative is to amend D3 so that
    releasing the lock *does* re-open the gate and only the turn is not re-dispatched — cheaper, but
    it makes the waiting line lie: the line says "sending a message returns it" while, with attempts
    left under FR-014's N=3 or on any new turn (cron, heartbeat, a goal follow-up), the agent is
    already driving the page the operator walked away from. That is the D5 exposure reinstated
    silently. If that alternative is ever taken instead, US-7 AS-5, the "Escape, then the operator
    walks away" edge case, the matching BDD scenario and ADR-085 D3's paragraph MUST be deleted in
    the same pass — leaving a promise with no mechanism is how this defect arose.
  - **Owning wave: W2** (`live.go`) for the state and clearing; **W1** (`tools.go`) for the gate
    read.
- **FR-027**: Re-orientation MUST rely on the existing `pkg/tools/browser/tools.go::ScreenshotTool`
  description, which already reports the tab's current URL and title including a page the operator
  navigated to themselves. No new re-orientation surface MAY be added.
- **FR-028**: On accepting a chat turn for a session whose panel wheel is held, the **gateway** MUST
  release it server-side, before the turn begins. It MUST work with the panel closed and with the
  holder's connection gone.
- **FR-029**: The release MUST fire on an **operator-originated prompt only**, and MUST fire for such
  a prompt arriving on **any** channel — see C7, C7.1, C7.2 and Ambiguity A3. The decision is carried
  on the message; the action happens where the session is known. Concretely:
  - **The carrier.** `pkg/bus/types.go` gains `InboundMessage.OperatorPrompt bool`, documented in the
    same fail-closed form as the `UserInitiated` field directly above it: *true ONLY when this
    message is a prompt the operator composed on this session — the webchat WS `message` handler
    (`pkg/gateway/websocket.go`), the SSE chat POST (`pkg/gateway/sse.go`), or a channel adapter's
    inbound dispatch of a real platform sender (`pkg/channels/base.go::HandleMessage`). Every other
    producer MUST leave it false; it is never set automatically, so a producer added later fails
    closed (does not release) rather than open.* Like `UserInitiated`, it is internal agent↔channel
    plumbing — `pkg/bus` is not on the gateway/SPA wire boundary, so Constraint #8 does not apply.
  - **The three sites that set it**: `pkg/gateway/websocket.go`, `pkg/gateway/sse.go`,
    `pkg/channels/base.go::HandleMessage`. Nowhere else.
  - **The three that must not**: `pkg/gateway/ws_ask_user.go::DispatchResume` (see the next bullet),
    `pkg/agent/async_notifier.go` (background tool/delegate completions on the synthetic `"system"`
    channel) and `pkg/agent/loop.go`'s goal-loop follow-up re-injection
    (`Sender.CanonicalID == goalLoopFollowUpSenderID`). **A delegate finishing in the background must
    never hand the browser back while the operator is typing a password into it.**
  - **A question-card resume never releases the wheel — answer or cancel.** The first draft made
    `ws_ask_user.go` a conditional release site gated on its existing `resumeIsUserInitiated(set)`.
    That predicate begins `if set.Status == askuser.StatusCancelled { return true }`, so **clicking
    Cancel on a card would have handed the browser to the agent mid-drive.** Beyond the bug, the
    conflation is wrong on its own terms: `DispatchResume` injects a *synthesised* `resumeText` into
    a turn that is already parked — the operator composed no message — and the composer lock is a
    reason to keep the two pending states independent, not to fuse them. Harm asymmetry settles it:
    releasing mid-drive reinstates the whole D5 exposure, while not releasing costs one deferred
    round-trip that is visible to the operator and bounded by FR-014's N=3. `resumeIsUserInitiated`
    MUST be left exactly as it is — it is correct for its own caller (goal/loop origin gating) and
    MUST NOT be read by this feature. Operator-ratified; see A13.
  - **`bus.InboundMessage.UserInitiated` MUST NOT be reused as the discriminator**, tempting as it
    is. It is the right *idea* but it is **false on the SSE path** (`pkg/gateway/sse.go` constructs
    its `InboundMessage` without it), so keying on it would silently reproduce the C7 stale-lock hole
    for SSE clients; widening it to cover SSE would change goal/loop origin gating (ADR-049 Gap #8)
    as a side effect of a browser change; and it is **true** on the question-card path, which FR-029
    now needs to be false.
  - **The action, and where it can actually live.** A helper called from `pkg/channels/base.go` into
    `pkg/gateway` cannot be built (C7.2: import direction, and no `SessionID` on the message there).
    Instead:
    - `pkg/agent/browser_deferral.go` declares
      `func (al *AgentLoop) SetBrowserWheelReleaseHook(fn func(ctx context.Context, sessionID string, actor ReleaseActor))`,
      modelled on the existing `pkg/agent/loop.go::AgentLoop.SetReloadFunc` gateway-registered
      callback. `ReleaseActor` carries the FR-030 attribution fields.
    - `pkg/agent/loop.go::processMessage` invokes the hook **once**, before the turn begins, **if and
      only if** `msg.OperatorPrompt` is true and the hook is non-nil. A nil hook is a silent no-op
      (headless/test builds with no gateway).
    - `pkg/gateway/browser_ws.go` implements it as
      `(*BrowserWSHandler).ReleaseBrowserWheelForPrompt`, doing the release across FR-050's
      reachability set, clearing the FR-026a latch and the FR-047 handover-pending flag, emitting the
      FR-030 audit record and the FR-031b holder notification. It is registered on the loop inside
      `pkg/gateway/browser_ws.go::newBrowserWSHandler`, which is already handed the
      `*agent.AgentLoop` — **no `pkg/gateway/gateway.go` edit is required.**
    - The hook MUST NOT be invoked from `pkg/bus/bus.go::PublishInbound`.
  - **`processMessage` also carries cron, and the field is what saves it.**
    `pkg/agent/loop.go::ProcessDirectWithChannel` builds a `bus.InboundMessage` with
    `Sender.CanonicalID == "cron"` and calls `processMessage` directly — it never publishes to the
    bus. It leaves `OperatorPrompt` false, so it never releases. Do **not** re-gate this on "did it
    come from the bus", on the channel name, or on `UserInitiated`.
- **FR-029a**: FR-029's correctness is a property of an *assignment* set, so a structural test MUST
  pin **where `OperatorPrompt` is set**, not where a helper is called — a helper call site can be
  moved, but the field's truth value is the thing the gate reads. The test walks every non-test
  assignment to `bus.InboundMessage.OperatorPrompt` in the module and asserts the partition is
  exactly: sets-true = {`pkg/gateway/websocket.go`, `pkg/gateway/sse.go`, `pkg/channels/base.go`};
  never-sets = {`pkg/gateway/ws_ask_user.go`, `pkg/agent/async_notifier.go`, `pkg/agent/loop.go`}.
  It MUST additionally assert the six-site `PublishInbound` census itself (six non-test call sites;
  `pkg/bus/bus.go`'s own line is the method declaration, not a call), so a **new** publish site
  cannot appear without classifying itself — a site that sets nothing passes the assignment
  assertion vacuously. A new site or a new assignment in either half fails with the reason stated.
- **FR-030**: The release MUST be audited via `pkg/gateway/browser_ws.go::auditRelease`'s record
  shape, naming the **actor who sent the prompt**, never the holder. The actor is resolved in this
  order, and the spec is explicit because the obvious carrier does not exist on most paths:
  1. `bus.InboundMessage.GatewayUserID` when set — the WS-authenticated gateway principal. Its own
     doc comment states it is set **ONLY** by the webchat WS path (and `ws_ask_user.go`, from
     `set.Owner`); "channel/task/scheduled inbound messages never set this field", so it is empty on
     every channel prompt and under dev-mode bypass.
  2. Otherwise, for a channel-originated prompt, `Sender.CanonicalID` (e.g. `telegram:123`) is
     recorded as the **actor** and the audit **user** field is left **empty**. A platform handle is
     not a gateway principal and MUST NOT be stamped as one (the standing rule
     `bus.InboundMessage.GatewayUserID`'s doc comment states for `Sender.Username`).
  3. The holder is recorded separately, as the *prior* holder. It is **never** used as the actor —
     that is the specific defect this requirement exists to prevent.
- **FR-031**: A wheel whose holder is no longer in `pkg/tools/browser/live.go::LiveView.viewers` MUST
  be treated as **void** by the tool gate — the agent MUST NOT defer to a ghost. **The gate does not
  express this today and no existing accessor can be made to.** `IsControlled(sessionID)` is
  `Controller(sessionID) != ""`, and `Controller` returns `lv.controller` unconditionally; neither
  consults `lv.viewers`. Therefore:
  - `pkg/tools/browser/live.go` MUST gain
    `func (r *LiveViewRegistry) IsControlledByLiveViewer(sessionID string) bool`, returning
    `lv.controller != "" && lv.viewers[lv.controller] is present`, evaluated under `lv.mu` in one
    critical section (two separate reads can observe a detach between them).
  - `pkg/tools/browser/tools.go::controlledResult` MUST call it **instead of** `IsControlled`.
  - `IsControlled` and `Controller` MUST be left untouched. They are the **status/presentation**
    reads — `pkg/gateway/browser_ws.go::handleTabAction`'s F3 gate and the panel's own
    control-ownership display both depend on the current, viewer-blind semantics, and changing them
    would silently retarget consumers this change has no business retargeting.
  - The FR-026a latch is voided by the same condition, in the same evaluation.
  - **The "existing ghost-steal path must stay reachable" clause from the first draft is deleted.**
    `EnsureControlForInput` has **no production caller**: outside `live.go` it appears only in
    `pkg/tools/browser/live_notcontroller_test.go` and `shared_control_test.go`. The gateway's input
    path calls `mgr.Live().Input(...)` directly, and `dispatchInput`'s own comment states input is
    never gated on a control lock (operator directive, 2026-08-03). It is retained as-is and is
    **currently test-only**; nothing in this change may be specified as depending on it.
- **FR-031b**: **Any release the holder did not itself request MUST reach the holder's own panel.**
  Today it cannot. `releaseControl` broadcasts through
  `snapshotControlSinksExceptLocked(viewerID)`, which excludes the acting viewer **by construction**,
  and `ControlSink`'s doc comment states the exclusion as an invariant on the grounds that "the
  acting viewer … already gets an authoritative `browser_status` frame as the direct response to its
  own `browser_control` request". That frame is emitted **only** inside
  `pkg/gateway/browser_ws.go::handleControl`'s `case "release"` — i.e. only as a reply to the
  holder's own request. So after an FR-029 prompt release, an FR-031a idle expiry, an FR-050
  handover clear, an FR-052 switch-off or a session deletion, the panel keeps `isControlling === true`
  forever: FR-057 clears operator-holds-wheel only on a server `released` status that never arrives,
  and `takeWheelIfNeeded`'s first guard is `if (controllingRef.current) return`, so **the operator can
  never re-take while the agent drives.** Both parties believe they hold the wheel — the exact state
  ADR-039/040 exist to prevent. Therefore:
  - Every server-initiated release MUST push an unsolicited
    `generated.BrowserStatusFrame{Type: browser_status, State: "released", SessionId: &chatSessionID}`
    to the still-attached former holder's connection, in addition to the existing
    `control_only` broadcast to every other viewer.
  - The frame MUST NOT set `ControlOnly`. The SPA's `if (f.control_only) { setControlledByOther(…);
    return }` branch applies only the control-ownership axis and returns, so a `control_only` frame
    cannot clear the holder's own `isControlling` — it would look delivered and change nothing.
  - A former holder who is no longer attached is a no-op, not an error.
  - **`ControlSink`'s doc comment MUST be amended in the same commit**, because it currently asserts
    the opposite as an invariant. The corrected statement: the acting viewer is excluded from the
    broadcast because it gets its own direct reply *for a release it requested*; a release the server
    performed on the holder's behalf is delivered to the holder directly by FR-031b instead.
  - FR-057 MUST name this frame as a clearing trigger (it already names "a server `released` status";
    the point of this requirement is that such a status now actually arrives).
  - **Owning wave: W2** throughout (`live.go`, `browser_ws.go`).
- **FR-031a**: A held wheel MUST expire. Today the operator's next prompt is the **only** release,
  a cron or heartbeat turn is not a prompt (and under FR-029 must not become one), and FR-031's
  ghost rule only voids a holder who has left `lv.viewers` — an attached-but-idle holder is not a
  ghost. So an operator who takes the wheel and closes their laptop **permanently disables every
  scheduled browser turn on that session**, with no expiry, no operator-visible state and nothing to
  recover. Therefore:
  - **Liveness — what resets the timer.** The first draft said "no viewer input and no attach/detach
    activity", and that is the wrong signal: **a reading operator produces no input events.** Under
    it, an operator who takes the wheel and studies the page for 900 seconds is released server-side
    and the agent resumes screenshotting the page they are still looking at — the D5 exposure,
    reinstated silently (and, before FR-031b, invisibly). WebRTC is streaming to them the whole time
    and was not counted either. The timer is reset by **any** of, while a viewer is attached:
    viewer input; an attach or detach; a `pkg/tools/browser/manager.go::ViewerHeartbeat` stamp (the
    live panel's WS `PongHandler` calls it — this is the existing "somebody is still watching" proof,
    pinned by `pkg/tools/browser/viewer_heartbeat_test.go`); or an active WebRTC media track on that
    view. **At minimum an attached viewer with a live transport resets the timer**, so the window
    targets the abandoned-laptop case it was written for and nothing else.
  - **A stood-down state with no attached viewer has no liveness signal at all**, by construction:
    FR-047 handover-pending raised with nobody watching, and an FR-026a latch left behind after the
    operator released and closed the panel. For those the window runs unconditionally from the
    transition that set the state. This is the case that makes the timer load-bearing rather than a
    belt-and-braces measure, and it is the case H5 exercises.
  - **Delivery is EAGER, not lazy.** A `LiveViewRegistry`-level sweeper MUST run on a coarse tick
    (**30 s**; the window is 900 s, so tick precision is irrelevant and a cheap tick is the point),
    walking the views it owns and expiring any whose window has elapsed. A lazy check folded into
    `controlledResult` would satisfy the gate but would never fire when no agent is running — and the
    operator-visible expiry line below is exactly the surface that must appear when nobody is
    running. `LiveView` gains one `lastControlActivity time.Time` field for this; the registry gains
    the ticker, started and stopped with the registry's own lifecycle.
  - The threshold is `tools.browser.control_idle_release` (Go field `ControlIdleReleaseSec int`,
    matching the existing `LeaseWaitSec` / `IdleTTLSec` naming in `pkg/config/config.go`'s browser
    block; default **900** seconds). `0` disables expiry, for an operator who genuinely wants an
    indefinite hold. That is an opt-in, not the shipped default.
  - The release MUST be audited as a distinct outcome, `browser_control_idle_release`, with the
    former holder recorded and no acting user, and MUST notify the former holder per FR-031b.
  - **Handover-pending (FR-047) and the FR-026a latch expire on the same timer**, for the same
    reason: an agent that hands over to an operator who never arrives, or an operator who pressed
    Escape and left, must not park the browser for the session's lifetime.
  - An operator-visible line MUST be emitted on expiry, on the same surface as FR-041, saying the
    browser has been returned to the agent because it was idle.
  - **The sweeper is also FR-052's mechanism**: on each tick it MUST release any hold,
    handover-pending state or latch found while `tools.browser.take_control_enabled` is false, and it
    MUST run regardless of that flag. This is why FR-052 needs no config-reload hook.
  - **Interaction with the existing `idle_ttl` tab reaper, which the first draft did not address.**
    `pkg/tools/browser/manager.go::ReapIdleSessions` already tears down tabs and whole browsing
    contexts after `tools.browser.idle_ttl` and **does not touch any `LiveView`** — so today a reaped
    tab set would leave a control lock, a handover-pending flag and a latch alive on a view whose
    tabs no longer exist. Therefore: a tab set whose live view is **controlled, handover-pending or
    latched MUST be exempt from `idle_ttl` reaping**; and if such a context is torn down for any
    other reason, the teardown MUST go through the same audited release path (audit record, FR-041
    line, FR-031b notification) rather than dropping the state on the floor. Exemption is the primary
    rule because a held wheel is by definition a tab set a human is using; the FR-031a window, not
    `idle_ttl`, is what bounds it.
- **FR-032**: Input dispatch MUST stay ungated (operator directive, 2026-08-03).
  `LiveView::dispatchInput` MUST NOT gain a control-lock authorization check. A second viewer driving
  without the wheel MUST neither defer the agent nor be blocked.

### E. Capture gating and the three-way classification (D5, C1)

- **FR-033**: `browser_screenshot`, `browser_get_text` and `browser_snapshot` MUST consult the control
  gate and return the same non-error deferral while the wheel is held.
- **FR-033a**: The **descriptions** of those same three tools MUST gain the deferral clause the
  action tools already carry — `pkg/tools/browser/tools_interact.go::deferredIsNotAnError`, appended
  today by `browser_click`, `browser_type` and the other action verbs. A tool whose behaviour is
  gated but whose description never says so teaches the model to treat the deferral as a failure,
  which is exactly what that shared constant exists to prevent. Precedent for asserting description
  text structurally: `pkg/tools/browser/evaluate_description_test.go`.
- **FR-034**: `browser_wait`, `browser_list_tabs` and `browser_handle_dialog` MUST remain ungated.
  `browser_handle_dialog` in particular MUST stay ungated — gating a recovery verb behind the
  mechanism the fault disables is a deadlock (its existing exemption reason).
- **FR-035**: The classification MUST become three-way, replacing the current two-way roster:
  **action** (deferral-gated + `acquireWrite` + `recordBrowserAction`), **capture** (deferral-gated,
  NOT leased, NOT per-call audited), **exempt** (none). `pkg/tools/browser/audit.go` MUST carry a
  third explicit roster alongside `writeClassBrowserTools` and `readOnlyBrowserTools`.
  `readOnlyBrowserTools`' doc comment currently says "the four tools that observe without acting"
  while the map holds **six**; it MUST be corrected as part of this split, not left describing a
  count that was already wrong before this change.
  > **⚠️ The roster is 11 or 10 depending on which catalog you count, and one literal asserted
  > against both scopes will redden one test.** `BrowserBuiltinMetadata()` returns **17** tools;
  > `RegisterTools` registers **16** — `browser_upload_file` is deliberately in the metadata catalog
  > and deliberately **not** registered (`register.go`'s own comment: "held means unregistered, not
  > unseeded"; `interact_test.go::TestUploadFile_NotRegistered` pins the absence). The two structural
  > tests iterate different things: `control_gate_membership_test.go` walks
  > `BrowserBuiltinMetadata()`, `lease_membership_test.go` walks `registry.GetAll()`. So:
  > - **Metadata-scoped: 11 action / 3 capture / 3 exempt = 17.** The action roster is exactly
  >   `writeClassBrowserTools` as it stands today, `browser_upload_file` included.
  > - **Registry-scoped: 10 action / 3 capture / 3 exempt = 16.** These are FR-036's and FR-037's
  >   counts, and the ones deliberate-rewrite #3 changes from `10 leased / 6 exempt`.
  > - **`ControlGatedToolNames()` (FR-016a) returns the METADATA-scoped union — 14 names**, and its
  >   equality assertion MUST state both sides in that same scope. A name in it that is not
  >   registered is harmless (the engine short-circuits on a name the model can never call); a
  >   registered gated name missing from it is the drift the test exists to catch.
  >
  > Every count literal added or changed by this feature MUST name its scope in the assertion
  > message.
- **FR-035a**: The biconditional lives in two **spec documents**, not in an ADR (see C1's box), and
  both MUST be amended in the same commit as FR-035, or the structural tests and the normative prose
  disagree with no gate catching it:
  - `docs/internal/specs/browser-workspace-ownership-spec.md` — **§14.2 Rules, rule 3** (the
    per-tool table), **FR-019a** and **AC5**. The single rule `leased ⟺ controlledResult-gated`
    becomes **two**: `leased ⟺ action-class`, and `control-deferred ⟺ action ∪ capture`. Rule 3's
    table gains a class column; `browser_screenshot`, `browser_get_text` and `browser_snapshot` move
    from "exempt" to "capture" (gated, unleased, not per-call audited);
    **`browser_handle_dialog` stays exempt from both gates** and its §12 A17 reasoning is untouched
    — a JS modal blocks the panel's own input injection too, so gating the one verb that can clear it
    freezes the human as well.
  - `docs/internal/specs/browser-agent-capability-spec.md` — its restatement of the same rule (the
    `tools.go::controlledResult` row in the symbol table, which today says read-only tools "are
    deliberately ungated", and §12 A-22, which reasons from that premise). A17's cross-spec
    obligation is narrowed to `browser_handle_dialog` alone; `browser_snapshot` no longer "falls out
    correctly as read-only" and must be named as capture-class.
  - **Owning wave: W1** owns both documents (see the wave plan).
- **FR-036**: `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased`
  MUST be re-stated as `leased ⟺ action-class` and MUST fail if a capture-class tool acquires the
  write lease.
- **FR-037**: `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet` MUST
  be re-stated as `per-call-audited ⟺ action-class` and MUST fail if a capture-class tool emits a
  `browser_action` row.
- **FR-038**: `pkg/tools/browser/control_gate_membership_test.go` MUST assert the full three-way
  partition against the **metadata catalog** (`BrowserBuiltinMetadata()`, 17 tools — that is what the
  file already iterates, despite its local variable being named `registered`), with every member of
  each class named in a literal roster and a stated reason, and MUST fail on a catalog tool that
  belongs to no class. Counts: **11 / 3 / 3**. The registry-scoped counterpart is FR-036's.
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
  - **The emitter seam MUST be named, because it crosses two handlers and three producers.** The take
    happens in `pkg/gateway/browser_ws.go::handleControl` on a `*BrowserWSHandler` / `*browserWSConn`,
    while chat frames reach a thread through
    `pkg/gateway/websocket.go::WSHandler.resolveSessionConnsLocked(originChatID, sessionID)` on a
    different handler. `BrowserWSHandler` today holds only an `*agent.AgentLoop` (see its struct) and
    has no path to the chat connections. It MUST gain the `*WSHandler` reference, supplied at
    `newBrowserWSHandler` time, and deliver through `resolveSessionConnsLocked("", sessionID)`.
  - **One emitter, three producers.** The same line is raised from three places — the take
    (`browser_ws.go`, W2), `browser_handover` (`pkg/tools/browser`, W6) and the FR-031a idle sweeper
    (`live.go`, W2) — and two of them are in a package that MUST NOT import `pkg/gateway`. There MUST
    therefore be a single `WaitingSurfaceEmitter` seam: an interface declared in `pkg/tools/browser`
    (`Emit(ctx, tabSetID, notice) error`), implemented in `pkg/gateway/browser_ws.go`, and registered
    on the `LiveViewRegistry` at wiring time. Every producer emits through it; nobody formats the
    frame twice. Without this, FR-048 and FR-031a's operator-visible line are specified with no way
    to reach the thread — the same gap C2 found, one layer down.
  - **Degradation, stated so it is not discovered in production.** If the session has no attached chat
    WS connection, `resolveSessionConnsLocked` returns an empty set: the frame is **dropped**, no
    error is raised, and the FR-043 transcript entry is the only surface until the operator
    re-attaches. That is the same "visible after reload, never absent from both" contract the
    Integration Boundaries section already promises — it is restated here because the panel being
    open is not evidence that the *chat* socket is.
- **FR-043**: The line MUST also be persisted as a `session.EntryTypeSystem` transcript entry (pattern:
  `pkg/agent/goal_loop.go::writeGoalSystemTranscript`) so a replay renders the same line in the same
  place. A delivery failure on the live frame MUST NOT suppress the transcript write.
  - **The reverse also holds: a failed transcript write MUST NOT block or fail the release.** If the
    session has been deleted or its workspace unmounted while the wheel was held, the writer's
    `AppendTranscriptStrict` targets a directory that no longer exists. The writer MUST detect the
    missing session and skip, logging at warn, rather than erroring out of a release path whose whole
    job is to stop the agent being locked out. Giving the browser back is the load-bearing half; the
    line is the courtesy half.
- **FR-043a**: **Replay MUST emit the same frame type as live.** `pkg/gateway/replay.go` today turns
  an `EntryTypeSystem` entry into a generic `generated.ReplayMessageFrame`, with only two
  special cases: `entry.Type == session.EntryTypeSystem && entry.Status == "error"`, and an
  `entry.AgentID != "" && strings.HasPrefix(entry.Content, "Handoff:")` branch. Left alone, the
  waiting line would render as one wire type live and a different one on reload — two renderers, two
  chances to drift, and US-6 AS-2's "the same line in the same place" would be true only by
  coincidence of the copy. Therefore:
  - `replay.go` MUST emit the **FR-042 frame type** for the handover system entry.
  - It MUST discriminate on a **stamped field on the transcript entry** — a dedicated system-entry
    subtype written by FR-043's writer — **never** by prefix-matching `entry.Content`. The existing
    `"Handoff:"` prefix match is the anti-pattern to avoid, not the precedent to copy: it breaks the
    moment the copy is reworded or localised, and the copy here is operator-facing.
  - **Owning wave: W2** (which owns the gateway surface); `pkg/gateway/replay.go` was previously
    unowned by any wave.
- **FR-044**: Exactly **one** line MUST be emitted per unbroken held period. A second `take` with no
  intervening release MUST NOT emit a second line. **Idempotency is by construction, not by a
  server-side "have I sent this?" flag**, following the precedent that already ships:
  `src/store/chat.ts`'s `case 'goal_status'` reducer synthesizes a `role: 'system'` `ChatMessage`
  through `buildGoalAckInsertion`, whose id comes from `goalAckMessageId(goalId)`
  (`` `goal-ack-${goalId}` ``) and which returns `null` when `b.messagesById[ackId]` already exists.
  Applied here:
  - The notice's message id MUST derive deterministically from `(sessionID, holdStartedAtUnixNano)`,
    where `holdStartedAtUnixNano` is a wall-clock nanosecond timestamp minted at the **transition
    into** a stood-down state (take or handover), stored on the `LiveView` beside the state itself,
    and **reused verbatim** for every emission within that unbroken hold. Idempotency comes from
    reusing the stored value; it does not come from a counter.
    > **⚠️ A per-process counter — the first draft's `holdEpoch` — silently swallows the first take
    > after a gateway restart, and this is a real sequence, not a theoretical one.** The lock is
    > process memory with no persistence and no boot rehydration (`live.go`, stated in the Symbols
    > table). Take → id `…epoch-1` is written into the transcript → gateway restarts → the operator
    > reloads and replay puts `…epoch-1` into `messagesById` → the operator takes the wheel again →
    > the counter restarts at 1 → the frame carries the **same id** → the SPA reducer's
    > `if (b.messagesById[ackId]) return null` guard drops it → **no waiting line for a real
    > takeover.** The `goal_status` precedent does not have this failure because `goalId` is durable;
    > a hold epoch is not. A ULID minted at the transition and echoed into both the frame and the
    > transcript entry is an equally acceptable implementation — the requirement is only that the id
    > cannot repeat across a restart.
  - The SPA reducer MUST drop a notice whose id it already holds, exactly as `buildGoalAckInsertion`
    does — so a re-delivered frame after a reconnect, or a frame racing the replay entry, cannot
    produce a duplicate line.
  - The transcript entry MUST carry the same id, so live and replay converge on one message rather
    than two that merely read alike.
  - **This closes ambiguity A5**: the rendering path is `src/store/chat.ts` (reducer, synthesizes the
    `role: 'system'` message) → `src/components/chat/ChatScreen.tsx::VirtualSystemMessageRow` (the
    virtualised list) and `::SystemMessage` (the non-virtualised branch). Both are named in W8.
- **FR-045**: The agent's own narration is driven by the deferral wording (FR-011) and is **SHOULD,
  not MUST** — the system line is the guaranteed surface, because the deferred tool call may be
  hidden from the thread by `src/lib/toolVisibility.ts`. **FR-045 has no automated oracle and MUST
  NOT be given a fake one**: what a model chooses to say is not observable from a Go unit test in
  `pkg/tools/browser`. It is verified by holdout **H1** and by nothing else.

### G. `browser_handover` (D7, C5)

- **FR-046**: A new tool `browser_handover` MUST be added, taking a single `reason` string.
- **FR-047**: It MUST put the browser in the operator's hands by setting an explicit
  **handover-pending** state on the live view for the turn's resolved tab set — a state distinct from
  `LiveView.controller`, because there may be no viewer id to grant (C5). The handover-pending state
  MUST defer tools exactly as a held wheel does and MUST NOT be subject to the ghost rule of FR-031.
- **FR-048**: It MUST emit the FR-041 waiting surface.
- **FR-048a**: The `reason` argument MUST have a consumer. As specified in FR-046 it is accepted and
  then dropped — an argument the model is asked to compose and nothing ever reads is worse than no
  argument, because the model spends tokens on it and the operator never learns why the agent stood
  down. Therefore:
  - The FR-041 waiting surface, **when raised by `browser_handover`**, MUST include the reason in its
    body — as **plain text, truncated to 200 runes**, never rendered as HTML or Markdown (the string
    is model-authored and reaches an operator-facing surface; treating it as markup is an injection
    surface for no benefit).
  - The FR-062 audit record MUST carry the same reason, subject to the same truncation.
  - An empty or whitespace-only reason MUST NOT produce an empty parenthetical — the surface falls
    back to its agent-neutral copy.
- **FR-049**: It MUST return a non-error result instructing the agent to conclude its turn with an
  explanation. It MUST NOT set `ParksTurn`.
- **FR-050**: Handover-pending MUST be cleared by the same next-prompt release as FR-028, by an
  explicit operator release, by the FR-031a idle timer, by an FR-052 switch-off, and by the session
  being deleted or its workspace unmounted. **The session-lifecycle case is not optional**: the lock
  and the pending flag are keyed on the tab set, not on the session, so deleting a session releases
  nothing today and leaves an agent deferring against a chat that no longer exists. Session deletion
  MUST clear the hold, the handover-pending flag and the FR-026a latch across the FR-020 reachability
  set through the same audited path, notify any still-attached holder per FR-031b, and skip the
  FR-043 transcript write per FR-043's own missing-session rule.
  **Clearing MUST use the same reachability set the gate uses (FR-020), not a single key.** FR-047
  sets handover-pending on the **turn's resolved** tab set — for a delegated child that is the
  *child's own* tab set — while a next-prompt release arrives keyed on the **root chat**. Clearing
  only `PanelTabSetID(rootChatSessionID)` therefore never clears a child's pending state, and the
  browser stays deferred for that subtree until the gateway restarts. The release MUST clear
  handover-pending on **every tab set reachable from the root chat session**, which is exactly the
  set FR-020's second check is evaluated against.
- **FR-051**: Per Constraint #6 / ADR-077 the tool MUST be added at all three sites in one commit:
  - `pkg/coreagent/core.go::allStaticToolNames` — the static catalog;
  - the browser block of `pkg/config/defaults.go`'s `Sandbox.ToolPolicies`, with a shipped default of
    **`allow`**. *This is a decision, not an inheritance:* the browser family is **not** uniform —
    `browser_upload_file` is seeded `"ask"` in that same block. `allow` is chosen because handing the
    browser to the human is the conservative direction (the tool takes nothing and reaches no page),
    and an `ask` on it would put an approval card between the agent and its own stand-down.
  - the per-agent seeds for every browser-capable agent — **`IDJim`, `IDRay`, `IDExplorer`,
    `IDResearcher`** in `pkg/coreagent/core.go`. All four hold the full browser action set today
    (Jim's seed carries `browser_navigate`/`click`/`type`/`evaluate`/… at `allow`).
  - **Mia and Ava need no edit at all, and the test must assert that positively.** Every per-agent
    seed is built by `pkg/coreagent/core.go::denyAllThenOverride`, which enumerates
    *all* of `allStaticToolNames` at `deny` and then applies the agent's overrides — so adding the
    name to the catalog automatically gives Mia and Ava an explicit `deny` with no per-agent edit.
    The FR-051 test MUST assert they **resolve `deny`**, rather than asserting the absence of an
    entry: absence is not the observable, and an implementer "helpfully" adding them would produce a
    silently browser-capable Mia that no test notices.
- **FR-052**: With `tools.browser.take_control_enabled` false, `browser_handover` MUST NOT take effect
  and MUST return a result saying so. It MUST NOT become a bypass for the disabled feature.
  - **Flipping the flag to false while a wheel is held MUST give the wheel up.** The flag is checked
    only on `handleControl`'s `case "take"`; `case "release"` has no check, so an *attached* operator
    can still release by hand — but with the panel closed there is then no release path at all except
    the FR-031a timer, which is why that timer **MUST run regardless of the flag**. Concretely: on
    each tick the FR-031a sweeper releases any hold, clears any handover-pending state and voids any
    FR-026a latch it finds while the flag is false, audits each as a distinct outcome, and notifies
    the former holder per FR-031b. The bound is one sweeper tick, not instantaneous — stated rather
    than implied. Using the sweeper means **no config-reload hook and no `pkg/gateway/gateway.go`
    edit**; the flag is read live on each tick, following the `gateway.preview_enabled` precedent of
    a per-use read with no restart.

### H. Client take-over state rebuild (D8) — the regression surface

- **FR-053**: `BrowserLiveView.tsx` MUST introduce an explicit **operator-holds-wheel** state, set the
  instant `sendControl('take')` is sent and confirmed by the server's `controlling` status
  acknowledgement. It replaces `agentPausedByUserRef` / `agentPausedByUser`, which exist only to
  compensate for `cancelStream`'s asynchronous confirmation and have no meaning once FR-001 lands.
  - **"Replaces" MUST be asserted structurally, because the behavioural tests do not catch a
    half-done edit.** A minimal edit that deletes only the `cancelStream(sessionId)` line while
    keeping `setAgentPausedByUser(true)` beside it satisfies FR-001's test and leaves FR-054 and
    FR-055 green — only FR-057's second-turn regression catches it, and only because
    `if (!agentWorking) setAgentPausedByUser(false)` happens to survive with it. A test MUST assert
    that the identifiers `agentPausedByUser`, `agentPausedByUserRef` and `setAgentPausedByUser` **do
    not appear** in `src/components/browser/BrowserLiveView.tsx` at all. Both flags cannot coexist:
    two sources of truth for one state is the defect D8 exists to remove.
- **FR-054**: `computeDriveMode` MUST give operator-holds-wheel priority **over** `agentWorking`.
  Today the priority ladder is `annotating > agent-working > you-driving > …`, and `agentWorking` is
  softened only indirectly, via `effectiveAgentWorking = agentWorking && !agentPausedByUser`.
  **Regression guard:** without this, `driveMode` stays `agent-working`, so `canDispatchInput`
  (`return driveModeRef.current === 'you-driving' || implicitDriveActive`) returns false for every
  handler that passes `false` — keyboard, wheel and pointermove — and the operator's *continuing*
  input is inert while the panel's chip and cursor claim they are driving.
  **Note what this guard does NOT cover** — the very first `pointerdown` of the gesture. See FR-056.
- **FR-055**: The auto-release effect (`effectiveAgentWorking && isControlling → sendControl('release')`)
  MUST be gated on operator-holds-wheel so it can never revoke a wheel the operator took while a turn
  is in flight. **Regression guard:** without this, the take is granted and immediately released.
- **FR-056**: One click MUST still both acquire the wheel and dispatch the same `pointerdown` as page
  input, for click-to-drive, the omnibox, the tab strip and the explicit Take-over button.
  > **⚠️ This requirement is real but it is NOT a regression surface, and a test written against it
  > proves nothing.** `handlePointerDown` does not consult `canDispatchInput` at all: it reads
  > `driveModeRef` directly, explicitly allows `'agent-working'` (and `'idle'`, and `'other-driving'`)
  > through its bail-out, calls `takeWheelIfNeeded()`, sets `implicitDriveRef.current = true`, and
  > dispatches `mouse_down` over `{ forceWs: implicitDriveRef.current }`. **None of that path touches
  > `cancelStream` or `agentPausedByUser`**, so "the first click dispatches while the agent is
  > working" passes on today's unmodified code *and* after a naive deletion of `cancelStream`. The
  > surface that actually regresses is what happens **after `pointerup`** — see FR-054's guard and the
  > REGRESSION row in the test matrix. FR-056's own test is a **coverage** test (all four entry
  > points still take-and-act in one gesture), not a regression guard, and the matrix labels it so.
- **FR-057**: Operator-holds-wheel MUST be cleared **only** on a release — Escape, entering annotate
  mode, a failed take, **a server `browser_status{state:"released"}` frame (solicited or unsolicited
  — FR-031b is what makes the unsolicited one arrive; a `control_only` frame is NOT one of these)**,
  or disconnect. It MUST NOT be cleared by an `agentWorking` transition.
  **Regression guard, stated precisely.** The existing `agentPausedByUser` is cleared by
  `if (!agentWorking) setAgentPausedByUser(false)`. The harm is **not** at the moment the turn ends:
  the auto-release effect is `if (effectiveAgentWorking && isControlling && connectedRef.current)`,
  and when the turn ends `agentWorking` goes false, so `effectiveAgentWorking` is false and the
  effect **cannot** fire. The harm is one turn later. Carrying that clearing rule over **re-arms the
  auto-release for the NEXT agent turn**: the flag is already back to `false`, so the instant the
  agent starts speaking again `effectiveAgentWorking` becomes true with `isControlling` still true,
  and the wheel the operator never let go of is dropped out from under them mid-keystroke. The guard
  is therefore about the turn-end → next-turn-start **transition**, not about turn end alone.
- **FR-058**: Escape MUST still release the wheel and return focus to the address bar (WCAG 2.1.2).
- **FR-059**: No hand-back / control-toggle affordance MAY be added; ADR-040 D1 stands, asserted by
  `src/components/browser/BrowserLiveView.controlToggle.test.tsx`.

### I. Audit (D10)

- **FR-060**: `pkg/gateway/browser_ws.go::auditControl` and `::auditRelease` keep their current record
  shape and severities.
- **FR-061**: A take that causes an agent to defer MUST emit a distinct audit record carrying session
  id, **root chat session id**, viewer id, acting user, tab set, and the deferred tool's name.
  - **It MUST be emitted from the deferral path, not the take path**, by a new
    `pkg/tools/browser/audit.go::recordControlDeferral` called from `controlledResult` when it
    returns a deferral. The take handler (`pkg/gateway/browser_ws.go::handleControl`) **cannot** emit
    it: at take time no tool has deferred yet and the handler has no way to know which one later
    will. **Owning wave: W1**, not W2.
  - **Turn id is dropped from the field set.** `pkg/tools/base.go` exposes `ToolCallID`,
    `ToolTranscriptSessionID`, `ToolSessionKey` and `ToolAgentID` — there is **no turn id in the tool
    context** (this is the same absence C4 records for the attempt counter). The two candidates were:
    route a turn id through `ManagerResolver` alongside FR-021's root-chat id, or drop it.
    **Decision: drop it, and carry the root chat session id instead** — it is already being routed
    for FR-021, it is stable across a delegation subtree (ADR-057 FR-011), and it answers the
    question an auditor actually asks ("which conversation was blocked?") better than a turn id
    would. `ToolCallID` is recorded as well, which pins the individual call precisely.
- **FR-062**: A `browser_handover` MUST be audited with the same field set, with the agent as actor,
  plus the FR-048a reason. It is emitted from the same `recordControlDeferral` family, in W6's tool.

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
- **When** the agent attempts a browser action, in that turn or a later one
- **Then** the action still defers
- **And** the agent does not resume driving the browser on its own
- **And** the waiting line remains in the thread

#### Scenario: A holder who vanished does not leave the agent stood down
**Traces to**: US-8, AS-1 (and FR-026a) · **Category**: Error Path
- **Given** the operator took the wheel and their connection dropped without a clean close
- **When** the agent attempts a browser action
- **Then** the action executes normally
- **And** the stand-down is void, not merely the lock — a holder that vanished released nothing
  deliberately, so nothing deliberate is preserved

#### Scenario: A server-initiated release reaches the holder's own panel
**Traces to**: US-7, AS-7 (and FR-031b) · **Category**: Error Path
- **Given** the operator holds the wheel with the live panel still open
- **When** the server gives the browser back without the operator asking — an idle expiry, a
  take-control switch-off, or the session being deleted
- **Then** the panel stops reporting that the operator is driving
- **And** the operator can take the wheel again immediately, without reloading

#### Scenario: Answering a question card does not return the browser
**Traces to**: US-7, AS-8 · **Category**: Edge Case
- **Given** the operator holds the wheel and a question card is awaiting an answer
- **When** they answer the card
- **Then** the wheel is not released
- **And** the resumed turn's browser attempts still defer

#### Scenario: Cancelling a question card does not return the browser
**Traces to**: US-7, AS-8 · **Category**: Error Path
- **Given** the operator holds the wheel and a question card is awaiting an answer
- **When** they press Cancel on the card
- **Then** the wheel is not released
- **And** the resumed turn's browser attempts still defer

#### Scenario: Take-control is switched off while a wheel is held
**Traces to**: US-11, AS-3 · **Category**: Error Path
- **Given** the operator holds the wheel and has closed the panel
- **When** take-control is switched off on that installation
- **Then** within one sweeper interval the hold is given up and recorded
- **And** the agent's next browser action executes normally

#### Scenario: A held tab set is not reaped for inactivity
**Traces to**: US-7, AS-7 (and FR-031a) · **Category**: Edge Case
- **Given** the operator holds the wheel on a tab set that has seen no tab activity for longer than
  the tab idle window
- **When** the idle tab sweep runs
- **Then** that tab set is not torn down
- **And** the operator's page is still there when they look at it

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
- **And** the operator still holds the wheel for as long as the card is unanswered
- **And** when they answer or cancel it, the wheel is **not** released — the composer lock and the
  wheel are independent, and only a message the operator composed returns the browser

#### Scenario: A background delegate finishing does not hand the browser back
**Traces to**: US-7, AS-6 · **Category**: Error Path
- **Given** the operator holds the wheel on a session
- **And** a background delegate started earlier in that session is still running
- **When** that delegate finishes and its result is published on the session
- **Then** the wheel is not released
- **And** the agent's next browser attempt still defers

#### Scenario: An idle stand-down is given up by the server
**Traces to**: US-7, AS-7 · **Category**: Edge Case
- **Given** the operator has stood the agent down and nothing since then has proved anyone is still
  there — no input, no attach or detach, no panel keep-alive, no live video
- **When** the configured idle window elapses
- **Then** the hold, any pending handover and any stand-down are given up server-side and recorded as
  an idle release
- **And** a line appears saying the browser has been returned to the agent
- **And** a scheduled turn on that session can drive the browser again

#### Scenario: A watched hold does not expire
**Traces to**: US-7, AS-7 · **Category**: Error Path
- **Given** the operator holds the wheel with the panel open, reading the page and touching nothing
- **When** more than the idle window elapses
- **Then** the wheel is not released
- **And** the agent's browser attempts still defer — an operator who is visibly still there is not
  treated as an operator who left

#### Scenario: A handover set by a delegated child is cleared by the root chat's prompt
**Traces to**: US-9, AS-3 (edge) · **Category**: Edge Case
- **Given** a delegated sub-agent handed the browser over on its own tab set
- **When** the operator sends a message on the root chat
- **Then** the sub-agent's handover-pending state is cleared as well
- **And** a subsequent delegated browser action executes normally

#### Scenario: The operator keeps driving after the click that took the wheel
**Traces to**: US-2, AS-3 · **Category**: Happy Path
- **Given** the agent is working and the operator has completed one click in the frame, taking the wheel
- **When** they type a character, scroll, and move the pointer without releasing
- **Then** every one of those reaches the page as input

#### Scenario: A wheel held across the end of one turn survives the start of the next
**Traces to**: US-2, AS-4 · **Category**: Error Path
- **Given** the operator holds the wheel and the agent's turn ends on its own
- **When** a second agent turn starts on the same session
- **Then** no release is sent at any point across that transition
- **And** the operator still holds the wheel

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
| FR-001, FR-053 | `does not call cancelStream when taking the wheel, and DOES send one browser_control{take} frame in the same gesture` (paired negative + positive: a bare "not called" passes on a component that no longer takes the wheel at all) | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Operator takes the wheel… |
| FR-002 | `TestTakeover_WritesNoTurnCanceledEntry` — asserts zero `turn_canceled` entries after a take, **and**, on the same fixture, that pressing Stop DOES write one (the positive control; without it the test passes on a fixture whose transcript writer is broken) | Go integration | `pkg/gateway/browser_control_handover_test.go` | Operator takes the wheel…; Stop still cancels the turn |
| FR-002 | `TestTakeover_SetsNoParkAndNoCancel` | Go unit | `pkg/agent/browser_deferral_test.go` | Operator takes the wheel… |
| FR-003 | **COVERAGE, not a regression guard** `TestControlGate_InFlightCallCompletesAfterTake` — passes on today's unmodified code (no mid-tool preemption exists to remove); it exists so a future preemption cannot be added without a red test | Go unit | `pkg/tools/browser/tools_control_test.go` | Take-over mid-navigation… |
| FR-004 | `TestTakeover_NonBrowserToolStillExecutes` — narrowed: asserts the non-browser tool executes **while `IsControlled` is true AND the turn is still running**. Both conditions are the point; without them the test passes on a fixture where the take never landed or the turn already ended | Go integration | `pkg/gateway/browser_control_handover_test.go` | Non-browser work continues… |
| FR-010 | `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (existing, extended) | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-011 | `TestDeferralReason_StatesNoRetryAndPromptToResume` | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-012 | `TestDeferralPayload_CarriesGateDiscriminator` | Go unit | `pkg/tools/browser/tools_control_test.go` | The agent's next browser action defers… |
| FR-012a | `TestToolResult_DeferralIsStructuralNotProse` — the ledger counts a deferral whose `ForLLM` has been replaced with an unrelated string, and does **not** count a non-deferred result whose `ForLLM` contains the word "deferred". Proves the engine reads `Deferred`, not text | Go unit | `pkg/tools/result_test.go` + `pkg/agent/browser_deferral_test.go` | The agent's next browser action defers… |
| FR-012a | `TestToolResult_DeferredNeverCrossesTheWire` — `Deferred` is `json:"-"`; the generated-contract test stays green | Go unit | `pkg/tools/result_test.go` | — (structural) |
| FR-013, FR-017 | `TestBrowserDeferralLedger_CountsPerTurnNotPerSession` | Go unit | `pkg/agent/browser_deferral_test.go` | A new turn starts the attempt count at zero |
| FR-014, FR-015 | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` | Go unit | `pkg/agent/browser_deferral_test.go` | The agent is told to stop attempting… |
| FR-016 | `TestBrowserDeferralLedger_FourthAttemptShortCircuitsWithoutBrowser` | Go unit | `pkg/agent/browser_deferral_test.go` | A fourth attempt never reaches the browser |
| FR-016a | `TestBrowserTools_ControlGatedToolNamesEqualsActionPlusCapture` — the exported view equals the union of the action and capture rosters, element for element, **both sides metadata-scoped (14 names, `browser_upload_file` included)**, so the engine's classifier cannot drift from the gate's | Go unit | `pkg/tools/browser/control_gate_membership_test.go` | — (structural) |
| FR-016a | `TestShortCircuit_UsesExportedRosterNotAHardcodedList` — the engine short-circuits every name in `ControlGatedToolNames()` and no name outside it | Go unit | `pkg/agent/browser_deferral_test.go` | A fourth attempt never reaches the browser |
| FR-017 | `TestBrowserDeferralLedger_ChildTurnHasItsOwnCounter` | Go unit | `pkg/agent/browser_deferral_test.go` | A delegated worker driving its own tab set defers |
| FR-020 | `TestControlledResult_ChecksRootChatPanelTabSet` | Go unit | `pkg/tools/browser/tools_control_test.go` | A delegated worker driving its own tab set defers |
| FR-021 | `TestToolContext_CarriesRootChatSessionID` — `pkg/agent` stamps the new tool-context key from `turnState.routingSessionID` and `ToolRootChatSessionID` reads it back; asserts the `ManagerResolver` interface is **unchanged** (a signature change would drag `register.go` into three waves) | Go unit | `pkg/agent/browser_resolver_test.go` | A delegated worker driving its own tab set defers |
| FR-020 | `TestControlledResult_EmptyRootChatIDSkipsTheSecondCheck` — a resolver returning `""` skips the root-chat check entirely (fail-open), so the existing `implicit_acquisition_test.go` resolved-key case stays green for the right reason | Go unit | `pkg/tools/browser/tools_control_test.go` | An unrelated chat on the same workspace browser… |
| FR-022 | `TestRoutingSessionID_ConsumerSetIsClosed` (existing, amended in **four** places: `browser_deferral.go` added to `u19RoutingSessionIDScanFiles`; a fifth `u19BucketBrowserGate` in `u19ClassifyRoutingSessionIDRead` with its role-B justification; that bucket's own exact-count assertion; `wantTotal` raised from 30 by exactly the number of new reads) | Go unit | `pkg/agent/routing_session_id_consumer_set_adr057_test.go` | — (structural) |
| FR-023 | `TestDelegatedChild_DefersOnParentWheelHold` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A delegated worker driving its own tab set defers |
| FR-024 | `TestControlledResult_UnrelatedChatOnSameBrowserDoesNotDefer` | Go unit | `pkg/tools/browser/tools_control_test.go` | An unrelated chat on the same workspace browser… |
| FR-025, FR-027 | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-026 | `clears the wheel on Escape without resuming the agent` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Escape released the wheel… |
| FR-026 | **COVERAGE, not a regression guard** `TestRelease_DoesNotClearWaitingLine` — nothing retracts a transcript entry today, so this is a tautology on unmodified code; it exists so a future "tidy up the notice" change is caught | Go integration | `pkg/gateway/browser_control_handover_test.go` | Escape released the wheel… |
| FR-026a | **`TestStandDown_SurvivesEscapeAndStillDefers`** — take the wheel, release it (Escape / annotate / panel close), then call a gated browser tool: the call still defers. Runs entirely in `pkg/tools/browser`, so it observes the Go gate rather than the SPA. **This is the test the first draft did not have**: its named vitest ran in the SPA and could not see whether the Go gate deferred | Go unit | `pkg/tools/browser/tools_control_test.go` | Escape released the wheel… |
| FR-026a | **`TestStandDown_ClearedOnlyByPromptReleaseAndIdleExpiry`** — a new turn, a turn end, a re-take and a second viewer's input all leave the latch set; the FR-029 release and the FR-031a expiry each clear it | Go unit | `pkg/tools/browser/tools_control_test.go` | Escape released the wheel… |
| FR-026a, FR-031 | **`TestStandDown_VoidedByGhostHolder`** — a holder that left `lv.viewers` voids the latch as well as the lock, so `TestControlGate_GhostHolderDoesNotDeferTools` stays green for the right reason | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A holder who vanished does not leave the agent stood down |
| FR-028 | `TestChatTurnAccept_ReleasesHeldPanelLock` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-028 | `TestChatTurnAccept_ReleasesWithPanelClosed` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The wheel is released even with the panel closed |
| FR-029 | `TestChatTurnAccept_ReleasesForNonWebchatChannel` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-029 | `TestChatTurnAccept_ReleasesOnSSEPrompt` — the SSE publish site releases even though its `InboundMessage` leaves `UserInitiated` false | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message releases the wheel… |
| FR-029 | **`TestChatTurnAccept_SyntheticAsyncNotifyDoesNotRelease`** — an `AsyncNotifier.Notify` publish on the `"system"` channel for a finished background delegate leaves the wheel held, and the agent's next attempt still defers | Go integration | `pkg/gateway/browser_control_handover_test.go` | A background delegate finishing does not hand the browser back |
| FR-029 | `TestChatTurnAccept_GoalLoopFollowUpDoesNotRelease` — same for a `goalLoopFollowUpSenderID` re-injection | Go integration | `pkg/gateway/browser_control_handover_test.go` | A background delegate finishing does not hand the browser back |
| FR-029 | **`TestQuestionCardResume_NeverReleasesTheWheel`** — three cases on one fixture: a human answer, a **Cancel** (`set.Status == askuser.StatusCancelled`, the case that made `resumeIsUserInitiated` unusable here) and a timed-out auto-default. All three leave the wheel held and the next browser attempt deferring. Replaces `TestChatTurnAccept_AutoDefaultedQuestionCardDoesNotRelease`, whose human-answer half asserted the opposite | Go integration | `pkg/gateway/browser_control_handover_test.go` | Answering a question card…; Cancelling a question card… |
| FR-029 | `TestReleaseHook_FiresOnlyOnOperatorPrompt` — `processMessage` invokes the registered hook for an `OperatorPrompt: true` message and for **no** other, including the `Sender.CanonicalID == "cron"` message `ProcessDirectWithChannel` hands it; a nil hook is a silent no-op | Go unit | `pkg/agent/browser_deferral_test.go` | The next message releases the wheel… |
| FR-029a | **`TestOperatorPromptField_AssignmentPartitionIsPinned`** — walks every non-test assignment to `bus.InboundMessage.OperatorPrompt` and asserts sets-true = {`websocket.go`, `sse.go`, `channels/base.go`}, never-sets = {`ws_ask_user.go`, `async_notifier.go`, `loop.go`}; **and** asserts the `PublishInbound` census is six non-test call sites, so a new publish site cannot appear un-classified. Pins the field, not a helper call | Go unit | `pkg/gateway/browser_release_sites_test.go` | — (structural) |
| FR-030 | `TestChatTurnAccept_ReleaseAuditsActingUserNotHolder` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A different user's message releases the wheel… |
| FR-030 | **`TestChatTurnAccept_ChannelReleaseAuditsCanonicalSenderNotHolder`** — a Telegram prompt (no `GatewayUserID`) records `Sender.CanonicalID` as actor, leaves the audit user field empty, and records the holder only as the prior holder | Go integration | `pkg/gateway/browser_control_handover_test.go` | A different user's message releases the wheel… |
| FR-031 | `TestControlGate_GhostHolderDoesNotDeferTools` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A ghost holder does not lock the agent out |
| FR-031 | **COVERAGE, not a regression guard** `TestControlGate_LiveHolderDefersTools` — `IsControlled` already returns true for any holder, so this passes unmodified; it exists to pin the positive half of the new `IsControlledByLiveViewer` biconditional | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A live holder does lock the agent out |
| FR-031 | **`TestControlGate_UsesIsControlledByLiveViewerNotIsControlled`** — `IsControlled` / `Controller` keep their viewer-blind semantics (asserted directly, since `handleTabAction`'s F3 gate depends on them) while `controlledResult` no longer defers for a holder absent from `lv.viewers` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A ghost holder does not lock the agent out |
| FR-031b | **`TestServerInitiatedRelease_NotifiesTheFormerHolder`** — an idle expiry, an FR-052 switch-off and a prompt release each push one unsolicited `browser_status{state:"released"}` to the former holder's own connection, **without** `control_only`, in addition to the `control_only` broadcast to other viewers; a detached former holder is a no-op | Go integration | `pkg/gateway/browser_ws_test.go` | A server-initiated release reaches the holder's own panel |
| FR-031b, FR-057 | `clears operator-holds-wheel on an UNSOLICITED server released frame, and re-takes on the next click` — drives an unsolicited `browser_status{state:"released"}` into the panel with no local release and asserts `controllingRef` clears, so `takeWheelIfNeeded`'s `if (controllingRef.current) return` guard no longer blocks a re-take. A `control_only` frame must **not** clear it | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | A server-initiated release reaches the holder's own panel |
| FR-031a | **`TestControlGate_IdleHoldExpiresAndUngatesTheAgent`** — a hold with **no proof of life** (no input, no attach/detach, no `ViewerHeartbeat`, no live media track) for the idle window is released by the sweeper, audited `browser_control_idle_release`, and the agent's next attempt executes | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | An idle stand-down is given up by the server |
| FR-031a | **`TestControlGate_HeartbeatingHolderNeverExpires`** — an attached holder whose only activity is `ViewerHeartbeat` survives several idle windows and the agent keeps deferring. Without this, a reading operator is released and the D5 exposure returns | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | A watched hold does not expire |
| FR-031a | **`TestIdleSweeper_FiresWithNoAgentRunning`** — the registry sweeper expires a hold and emits the line on its own tick, with no tool call in flight. Pins EAGER over a lazy check inside `controlledResult`, which would never emit the line when nothing is running | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | An idle stand-down is given up by the server |
| FR-031a | `TestHandoverPending_ExpiresOnTheSameIdleTimer` — and the FR-026a latch with it; both run their window from the transition, since neither has an attached viewer to produce liveness | Go unit | `pkg/tools/browser/tools_handover_test.go` | An idle stand-down is given up by the server |
| FR-031a | **`TestReapIdleSessions_SkipsAControlledOrLatchedTabSet`** — `ReapIdleSessions` leaves a controlled, handover-pending or latched tab set alone; a context torn down for any other reason routes through the audited release path rather than orphaning the state | Go unit | `pkg/tools/browser/manager_reaper_test.go` | A held tab set is not reaped for inactivity |
| FR-031a | `TestIdleRelease_EmitsOperatorVisibleLine` | Go integration | `pkg/gateway/browser_control_handover_test.go` | An idle stand-down is given up by the server |
| FR-032 | `TestSharedControl_SecondViewerInputStillDispatches` (existing) | Go unit | `pkg/tools/browser/shared_control_test.go` | Two viewers, one wheel |
| FR-032 | **COVERAGE, not a regression guard** `TestSharedControl_SecondViewerDoesNotDeferTheAgent` — `dispatchInput` carries an explicit "NO CONTROL GATE" comment and never touches the lock, so this passes unmodified; it exists so the ungated-input directive cannot be quietly reversed | Go unit | `pkg/tools/browser/shared_control_test.go` | Two viewers, one wheel |
| FR-033 | `TestControlGate_CaptureToolsDeferWhileControlled` | Go unit | `pkg/tools/browser/tools_control_test.go` | Capture attempts defer… (outline) |
| FR-034 | `TestControlGate_WaitListTabsAndDialogStayUngated` | Go unit | `pkg/tools/browser/tools_control_test.go` | Wait and tab listing still work… |
| FR-033a | `TestCaptureTools_DescriptionsCarryTheDeferralClause` — all three capture descriptions contain `deferredIsNotAnError` (precedent: `evaluate_description_test.go`) | Go unit | `pkg/tools/browser/capture_description_test.go` | — (structural) |
| FR-035, FR-038 | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` — **metadata-scoped** (`BrowserBuiltinMetadata()`, 17), counts **11 action / 3 capture / 3 exempt**, each count literal naming its scope in the message | Go unit | `pkg/tools/browser/control_gate_membership_test.go` | — (structural) |
| FR-035a | `make verify-specs` is not a gate in this repo, so this is a **reviewer checklist row**: the same commit edits `browser-workspace-ownership-spec.md` §14.2 rule 3 / FR-019a / AC5 / §12 A17 and `browser-agent-capability-spec.md`'s restatement. The mechanical half is `TestBrowserTools_ThreeWayGateClassificationMatchesRosters`'s stated-reason strings, which MUST cite the amended rule | review gate | the two §14.2 spec documents | — (structural) |
| FR-036 | `TestWriteLease_LeasedIffActionClass` — **registry-scoped** (`registry.GetAll()`, 16), counts **10 action / 3 capture / 3 exempt** | Go unit | `pkg/tools/browser/lease_membership_test.go` | — (structural) |
| FR-037 | `TestAudit_PerCallAuditedIffActionClass` | Go unit | `pkg/tools/browser/audit_test.go` | — (structural) |
| FR-039 | `TestScreenshot_DeferredCallProducesNoFileNoMediaNoArtifactTag` | Go unit | `pkg/tools/browser/tools_control_test.go` | Capture attempts defer… (screenshot row) |
| FR-040 | **COVERAGE, not a regression guard** `TestCapture_PreTakeoverArtifactsAreNotRetracted` — nothing retracts anything today, so this passes unmodified; it exists so a future "clean up captures on takeover" idea is caught by a red test rather than a review | Go integration | `pkg/gateway/browser_control_handover_test.go` | An earlier capture is not retracted |
| FR-041, FR-042 | `TestTakeControl_EmitsWaitingSurfaceFrame` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The waiting line appears live… |
| FR-042 | **`TestWaitingSurface_ReachesTheChatConnsAndDegradesWhenNoneAreAttached`** — the frame is delivered through `WSHandler.resolveSessionConnsLocked("", sessionID)` from the browser-WS handler, and with **no** chat connection attached the frame is dropped without error while the FR-043 transcript entry is still written | Go integration | `pkg/gateway/browser_control_handover_test.go` | The waiting line appears live… |
| FR-042 | **`TestWaitingSurface_AllThreeProducersUseOneEmitter`** — the take, `browser_handover` and the idle sweeper all emit through the registered `WaitingSurfaceEmitter`; asserts `pkg/tools/browser` still does not import `pkg/gateway` | Go unit | `pkg/tools/browser/waiting_surface_test.go` | The waiting line appears live… |
| FR-043 | **`TestWaitingSurface_MissingSessionDoesNotFailTheRelease`** — with the session directory deleted, the transcript write is skipped with a warn and the release still completes and audits | Go integration | `pkg/gateway/browser_control_handover_test.go` | — (structural) |
| FR-042 | `renders the browser-handover waiting notice in the thread` | vitest | `src/components/chat/ChatScreen.browser-handover-notice.test.tsx` | The waiting line appears live… |
| FR-042 | `verify-contracts` gate (`make verify-contracts`) | CI gate | `contracts/` + generated dirs | — (structural) |
| FR-043 | `TestTakeControl_WritesWaitingSurfaceTranscriptEntry` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The waiting line survives a reload |
| FR-043a | **`TestReplay_HandoverNoticeReplaysAsTheSameFrameType`** — replaying a session containing the handover system entry yields the FR-042 frame type, not a generic `ReplayMessageFrame`, and the discrimination is on the stamped entry field (asserted by rewording the entry's content and seeing the frame type unchanged) | Go integration | `pkg/gateway/replay_test.go` | The waiting line survives a reload |
| FR-044 | `TestTakeControl_SecondTakeWithoutReleaseEmitsOneLine` | Go integration | `pkg/gateway/browser_control_handover_test.go` | Taking the wheel twice emits one waiting line |
| FR-044 | `TestWaitingNotice_MessageIDIsStableWithinAHoldAndUniqueAcrossRestarts` — the id is a pure function of `(sessionID, holdStartedAtUnixNano)`; a re-take within one hold reuses the stored value, a release-then-take produces a new one, and **an id minted for the same session after a simulated process restart differs from the one already in the transcript**, so a genuine fresh takeover is never dropped by the SPA's `messagesById` guard | Go unit | `pkg/gateway/browser_ws_test.go` | Taking the wheel twice emits one waiting line |
| FR-044 | `drops a duplicate browser-handover notice with an id already in the store` (mirrors `buildGoalAckInsertion`'s `messagesById[ackId]` guard) | vitest | `src/store/chat.browser-handover-notice.test.ts` | Taking the wheel twice emits one waiting line |
| FR-046, FR-049 | `TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark` | Go unit | `pkg/tools/browser/tools_handover_test.go` | The agent hands the browser over… |
| FR-047 | `TestHandoverTool_SetsHandoverPendingNotAViewerLock` | Go unit | `pkg/tools/browser/tools_handover_test.go` | After handing over, the agent's own browser attempts defer |
| FR-047 | `TestHandoverPending_IsNotSubjectToGhostVoiding` | Go unit | `pkg/tools/browser/live_notcontroller_test.go` | After handing over… |
| FR-048 | `TestHandoverTool_EmitsWaitingSurface` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The agent hands the browser over… |
| FR-048a | **`TestHandoverTool_ReasonAppearsInSurfaceAndAudit`** — the reason reaches both the waiting-surface body and the audit record, truncated at 200 runes, escaped as plain text; an empty reason produces no empty parenthetical | Go unit + Go integration | `pkg/tools/browser/tools_handover_test.go`, `pkg/gateway/browser_control_handover_test.go` | The agent hands the browser over… |
| FR-049 | `TestHandoverTool_TurnEndsCompletedNotParked` | Go unit | `pkg/agent/browser_deferral_test.go` | A handover ends the turn normally… |
| FR-050 | `TestHandoverPending_ClearedByNextPrompt` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The next message clears an agent-initiated handover |
| FR-050 | **`TestHandoverPending_SetByDelegatedChildIsClearedByRootPrompt`** — the child sets pending on its own tab set; the root chat's prompt clears it across the FR-020 reachability set, and a subsequent delegated action executes | Go integration | `pkg/gateway/browser_control_handover_test.go` | A handover set by a delegated child… |
| FR-050 | **`TestSessionDeleted_ClearsHoldHandoverAndLatch`** — deleting the session (or unmounting its workspace) while the wheel is held clears all three across the reachability set, audits it, notifies the holder per FR-031b, and does not attempt the transcript write | Go integration | `pkg/gateway/browser_control_handover_test.go` | — (structural) |
| FR-051 | `TestSeed_BrowserHandoverAtAllThreeConstraint6Sites` | Go unit | `pkg/coreagent/browser_handover_seed_test.go` | — (structural) |
| FR-051 | `TestSeed_MiaAndAvaResolveDenyForBrowserHandover` — positively asserts the `deny` `denyAllThenOverride` produces for them, rather than asserting an absent entry | Go unit | `pkg/coreagent/browser_handover_seed_test.go` | — (structural) |
| FR-051 | `TestConstructorSeed_PolicyMapMatchesCatalog` (existing) | Go unit | `pkg/coreagent/constructor_seed_test.go` | — (structural) |
| FR-051 | `TestDefaults_CeilingLengthMatchesStaticCatalog` (existing assertion) | Go unit | `pkg/config/defaults_test.go` | — (structural) |
| FR-052, FR-065 | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` — **extended** with the mid-hold flip: with a wheel already held and the panel closed, flipping the flag false makes the next sweeper tick release the hold, clear handover-pending, void the latch, audit each, notify per FR-031b, and let the agent's next attempt execute; and the FR-031a timer is asserted to run with the flag false | Go integration | `pkg/gateway/browser_control_handover_test.go` | With take-control disabled…; Take-control is switched off while a wheel is held |
| FR-053, FR-054 | **REGRESSION (headline)** `keeps dispatching keyboard, wheel and pointermove AFTER the pointerup that took the wheel, while the agent is still working`. Sequence: pointerdown → pointerup → keydown; assert an input frame is still sent. **Oracle**: `canDispatchInput` is `driveModeRef.current === 'you-driving' \|\| implicitDriveActive`; `handlePointerUp` sets `implicitDriveRef.current = false`; `computeDriveMode` returns `'agent-working'` before it ever reaches `'you-driving'`. So the post-gesture input survives **only** because `effectiveAgentWorking = agentWorking && !agentPausedByUser` is softened by the take — remove that softening without a replacement and this test goes red | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The operator keeps driving after the click that took the wheel |
| FR-054 | **REGRESSION** `keeps you-driving priority over agent-working for the chip and the cursor`. **Oracle**: `computeDriveMode`'s ladder puts `agent-working` above `you-driving`, so on the naive removal the chip reads "the agent is browsing" while the operator holds the wheel | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The operator's click reaches the page… |
| FR-055 | **REGRESSION** `does not auto-release the wheel after the take ack while the agent is still working`. **Oracle**: the effect `if (effectiveAgentWorking && isControlling && connectedRef.current) sendControl('release')` fires on the very next render once the softening is gone | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | The wheel is not handed back… |
| FR-056 | **COVERAGE, not a regression guard** (see FR-056's box) `acquires the wheel and dispatches in one click from the frame, the omnibox, the tab strip and Take over` — passes on today's code by design; it exists so a rebuild does not silently drop an entry point | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx`, `BrowserLiveView.tabStrip.test.tsx` | The operator's click reaches the page… |
| FR-057 | **REGRESSION** `still holds the wheel when a SECOND agent turn starts after the first ended` — drives `agentWorking` true → false → true and asserts **no** `release` frame is sent across either transition. **Oracle**: on the naive carry-over, `if (!agentWorking) setAgentPausedByUser(false)` re-arms the softening, so the second turn's first render satisfies `effectiveAgentWorking && isControlling` and the auto-release effect fires. (A test that stops at turn end proves nothing — the effect *cannot* fire while `agentWorking` is false) | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | A wheel held across the end of one turn survives the start of the next |
| FR-057 | `clears operator-holds-wheel on a server released status and on annotate mode` | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | Escape released the wheel… |
| FR-053 | **`does not contain agentPausedByUser anywhere`** — a structural source assertion that the identifiers `agentPausedByUser`, `agentPausedByUserRef` and `setAgentPausedByUser` are gone from `BrowserLiveView.tsx`. Without it, a minimal edit deleting only the `cancelStream` line while keeping `setAgentPausedByUser(true)` passes FR-001, FR-054 and FR-055 and leaves two sources of truth behind | vitest | `src/components/browser/BrowserLiveView.handover.test.tsx` | — (structural) |
| FR-058 | `reverts control state and stops accepting input when the socket disconnects mid-control` (existing) | vitest | `src/components/browser/BrowserLiveView.controlToggle.test.tsx` | Escape released the wheel… |
| FR-059 | `never renders a Take control / Release control / Hand to agent button` (existing) | vitest | `src/components/browser/BrowserLiveView.controlToggle.test.tsx` | — (structural) |
| FR-060 | existing `handleControl` audit assertions | Go integration | `pkg/gateway/browser_ws_test.go` | — (structural) |
| FR-061 | `TestDeferralAudit_RecordsSessionRootChatViewerUserTabSetAndTool` — emitted from `recordControlDeferral` on the deferral path (W1), carrying the deferred tool's own name and `ToolCallID`; asserts **no** turn id field | Go unit | `pkg/tools/browser/audit_test.go` | The agent's next browser action defers… |
| FR-062 | `TestHandoverAudit_RecordsAgentAsActor` | Go integration | `pkg/gateway/browser_control_handover_test.go` | The agent hands the browser over… |
| FR-063 | existing cancel-path tests (see Regression) | Go integration | `pkg/gateway/` cancel tests | Stop still cancels the turn |
| FR-064 | `TestDelegation_NoParkCascadeFromBrowserDeferral` | Go unit | `pkg/agent/browser_deferral_test.go` | A deferred sub-agent reports to its parent… |
| — (question-card edge) | `TestAskUserQuestionPending_UnaffectedByWheelTake` | Go integration | `pkg/gateway/browser_control_handover_test.go` | A question card is pending… |
| — (end-to-end) | `operator takes the wheel mid-turn and the turn is not cancelled`. **Positive observable**: `[data-testid="browser-handover-notice"]` becomes visible AND the composer's Stop button remains rendered (the turn is still running). Waiting for a `turn_canceled` entry *not* to appear is a timeout, not an assertion | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | Operator takes the wheel… |
| — (end-to-end) | `agent states what it is waiting for after the third deferral`. **Positive observable**: exactly 3 `browser_*` tool-call rows appear in the Activity panel for the turn, and the turn's final assistant message is non-empty | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | The agent ends its turn saying… |
| — (end-to-end) | `next prompt releases the wheel and the agent re-orients from the page`. **Positive observable**: a `browser_control{release}` frame is observed on the WS before the turn's first tool call, and the agent's reply contains the hostname the operator hand-navigated to | Playwright e2e | `tests/e2e/browser-control-handover.spec.ts` | The next message releases the wheel… |

**Count**: **104 rows, 104 distinct named tests or gates** — no test appears on two rows, though
eleven rows carry two FR ids because one test covers both. **11** rows reuse, amend, narrow, extend
or are a non-executable gate on existing material (each marked *existing* / *amended* / *narrowed* /
*extended* / *reviewer gate* / `verify-contracts` in the table); the remaining **93** are new.

**Six rows are explicitly labelled COVERAGE, not regression guards** — FR-003, FR-026, FR-031's live
holder, FR-032, FR-040 and FR-056. Each passes on today's unmodified code by construction (there is
no mid-tool preemption to break, nothing retracts a transcript entry, `IsControlled` already returns
true for any holder, `dispatchInput` carries an explicit "NO CONTROL GATE" comment, nothing retracts
captures, and `handlePointerDown` never touches `cancelStream`). They earn their place by pinning
properties a rebuild could silently drop; they must not be read as evidence that the change works.
The **four** genuine regression guards are the rows marked REGRESSION under FR-053/FR-054, FR-054,
FR-055 and FR-057.

**FR-045 is the one FR with no matrix row, deliberately.** What a model chooses to narrate has no
automated oracle in `pkg/tools/browser`, so it is traced to holdout **H1** and to nothing else. Every
other one of the **68** FRs in this spec appears in the matrix at least once, and no test in the
matrix is untraceable to an FR or to a named cross-cutting scenario.

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
| **The deferral reason's marker substring** | `pkg/tools/browser/lease_membership_test.go` defines `humanControlDeferralMarker = "human is currently controlling"`; it is asserted in that file, in `implicit_acquisition_test.go` (**6** assertions) and in `operator_takeover_test.go` (**3** assertions) | **Yes, and this is a hard requirement, not advice** — FR-011's rewritten reason MUST retain the substring verbatim, **or all three files are amended in the same commit**. There is no third option: a rewrite that drops the phrase and leaves the constant in place turns nine assertions red across two files this spec did not previously own. `implicit_acquisition_test.go` and `operator_takeover_test.go` are added to **W1** |
| Write lease membership | `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased` | **Yes** — re-stated as `TestWriteLease_LeasedIffActionClass` (C1) |
| Audit write-class membership | `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet`, `::TestAudit_ReadOnlyCallsAreNotRecorded`, `::TestAudit_EveryWriteClassCallIsRecorded` | **Yes** — `TestAudit_PerCallAuditedIffActionClass` (C1) |
| No control toggle / hand-back button exists | `BrowserLiveView.controlToggle.test.tsx::never renders a Take control / Release control / Hand to agent button` | **No** — must stay green unchanged |
| One-click take-over from every entry point | `BrowserLiveView.takeTheWheel.test.tsx` (the `UAT fix: one-click take-over…` and `watch-only while the agent is working` blocks) | **Yes** — these tests assert `cancelStream` IS called. They must be **rewritten**, not deleted; the one-click and no-instant-revert assertions must survive with the cancel assertion removed. |
| Tab-strip / omnibox take-then-act | `BrowserLiveView.takeTheWheel.test.tsx` tab-chip cases, `BrowserLiveView.tabStrip.test.tsx` | **Yes** — covered by FR-056's test |
| Tool-policy coverage invariant | `pkg/coreagent/constructor_seed_test.go`, `pkg/coreagent/override_keys_panic_test.go`, the `len(AllStaticToolNames()) == len(DefaultConfig().Sandbox.ToolPolicies)` assertion | **No** — must stay green after the FR-051 three-site edit |
| ADR-057 routing-session consumer set | `pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed` | **No** — amend the allowlist, keep the test |
| `IsControlled` / `Controller` keep their viewer-blind semantics for the status path | `pkg/gateway/browser_ws.go::handleTabAction`'s F3 gate (which calls `LiveViewRegistry.Controller`) and the panel's control-ownership display | **Yes** — `TestControlGate_UsesIsControlledByLiveViewerNotIsControlled`. FR-031 adds a new accessor rather than changing these, precisely so this row stays a no-op |
| `dispatchInput` is never gated on the control lock | its own "NO CONTROL GATE (operator directive, 2026-08-03)" comment block; `shared_control_test.go` | **No** — must stay green unchanged. FR-026a's latch is a **tool** gate; it MUST NOT be consulted by `dispatchInput` |
| The take/release fan-out excludes the acting viewer | `ControlSink`'s doc comment; `snapshotControlSinksExceptLocked` | **Yes** — `TestServerInitiatedRelease_NotifiesTheFormerHolder`. The exclusion stays; FR-031b adds a **direct** frame to the former holder for releases they did not request, and **amends the doc comment in the same commit** because it currently states the opposite as an invariant |

### Deliberate test rewrites (each is a risk of silently losing coverage)

Each entry names the **exact existing symbol** being replaced, because a rewrite that leaves the old
function in place beside the new one is how a suite ends up asserting two contradictory rules and
passing on the weaker.

1. `pkg/tools/browser/tools_control_test.go::TestExecute_ControlLock_ReadOnlyToolsAreNotGated` —
   **narrow** to the three remaining exempt tools (`browser_wait`, `browser_list_tabs`,
   `browser_handle_dialog`). Do not delete: deleting it is how the exemption silently becomes
   universal.
2. `pkg/tools/browser/control_gate_membership_test.go::TestBrowserTools_ControlGateMembershipMatchesExemptions`
   is **REPLACED** by `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` — the old function
   name must not survive. Concretely: its
   `assert.ElementsMatch(declaredReadOnly, exemptNames)` is the assertion D5 breaks, so
   `declaredControlGateExemptions` **splits into `declaredCaptureClassTools` (3 names, each with its
   own reason) + `declaredControlGateExemptions` (3 names)**, and the
   `readOnlyBrowserTools == exemptions` equality becomes
   `readOnlyBrowserTools == capture ∪ exempt`. **Scope: this file walks `BrowserBuiltinMetadata()`
   (17), despite its local variable being named `registered`** — so its partition is 11 / 3 / 3, not
   10 / 3 / 3. Rename the variable while you are in there; a misleading name is what made the two
   scopes look interchangeable.
3. `pkg/tools/browser/lease_membership_test.go::TestWriteLease_EveryActionToolIsLeased` is
   **REPLACED** by `TestWriteLease_LeasedIffActionClass`. Concretely: its
   `require.Equal(t, defersUnderLock, defersUnderLease, …)` — the current biconditional, "gated iff
   leased" — becomes `defersUnderLease ⟺ action-class`, so a capture tool that defers under the lock
   and **not** under the lease is the new expected shape rather than a failure. Its two count
   literals change from `require.Equal(t, 10, leasedCount)` / `require.Equal(t, 6, exemptCount)` to
   **10 action / 3 capture / 3 exempt**. **Scope: this file walks `registry.GetAll()` (16)** — a
   different catalog from rewrite #2's. The same literal roster asserted in both files with one count
   will redden one of them; every count message must name its scope (FR-035).
4. `pkg/tools/browser/audit_test.go::TestAudit_WriteClassSetIsTheControlledResultSet` is **REPLACED**
   by `TestAudit_PerCallAuditedIffActionClass`.
5. `pkg/tools/browser/audit.go::readOnlyBrowserTools`' doc comment ("the four tools that observe
   without acting") is corrected as part of the split — it already understates the map by two.
6. `BrowserLiveView.takeTheWheel.test.tsx`'s `cancelStream` assertions — invert (assert **not**
   called) while preserving every surrounding assertion about the one-click outcome. **The inverted
   assertion is not itself a regression guard** (see FR-056's box); the guards are the three
   REGRESSION rows in the matrix.

---

## Wave Plan (parallel worktree implementation)

File-disjoint by construction: **no two waves touch the same file.** Waves are independently
committable but not independently compilable — the coupling column names the wave whose symbols a
wave depends on. Recommended order: **W0 → W4 → (W1 ‖ W2 ‖ W3 ‖ W5 ‖ W7) → W6 → W8.**

**W0 has grown, and it lands before every other wave.** It is no longer just the deferral carrier: it
owns every **cross-cutting field or key** that a later wave sets in one package and reads in another.
Three separate wave-plan defects came from leaving those fields inside a feature wave — a file
assigned to W6 that W1 and W3 both needed, a config field W2 needed from a wave ordered after it, and
a bus field W2 sets and W3 reads. Putting all four in W0 fixes the class, not the instances.

| Wave | Owns (exclusive) | Delivers | Compile-coupled to |
|---|---|---|---|
| **W0 — Cross-cutting carriers** | `pkg/tools/result.go`, `pkg/tools/result_test.go` (FR-012a); `pkg/tools/base.go` + its test file (FR-021's root-chat tool-context key); `pkg/bus/types.go` (FR-029's `OperatorPrompt`); `pkg/config/config.go` (FR-031a's `ControlIdleReleaseSec`) | FR-012a, and the declarations FR-021 / FR-029 / FR-031a are built on | none — lands first, before every other wave |
| **W1 — Gate semantics & three-way classification** | `pkg/tools/browser/tools.go`, `tools_interact.go`, `tools_snapshot.go`, `tabs.go`, `audit.go`, `key.go`, and the tests `tools_control_test.go`, `control_gate_membership_test.go`, `audit_test.go`, `lease_membership_test.go`, `interact_test.go`, **`implicit_acquisition_test.go`**, **`operator_takeover_test.go`**, **`snapshot_test.go`**, **`snapshot_audit_redaction_test.go`**, **`capture_description_test.go`** (new), **`waiting_surface_test.go`** (new); plus the two rule-3 documents **`docs/internal/specs/browser-workspace-ownership-spec.md`** and **`docs/internal/specs/browser-agent-capability-spec.md`** | FR-003, FR-010–FR-012, FR-016a (exports), FR-020, FR-024, **FR-026a (the gate read)**, **FR-031 (the gate's switch to `IsControlledByLiveViewer`)**, FR-033–FR-040, **FR-033a**, **FR-035a**, **FR-061** | W0 (`ToolResult.Deferred`, `ToolRootChatSessionID`), W2 (`IsControlledByLiveViewer`, the latch and handover-pending accessors, the `WaitingSurfaceEmitter` seam) |
| **W2 — Lock lifecycle, release, handover-pending, stand-down, waiting surface** | `pkg/tools/browser/live.go`, `pkg/tools/browser/manager.go`, `pkg/gateway/browser_ws.go`, `pkg/gateway/websocket.go`, `pkg/gateway/sse.go`, `pkg/gateway/ws_ask_user.go`, `pkg/channels/base.go`, **`pkg/gateway/replay.go`**, and the tests `live_notcontroller_test.go`, `shared_control_test.go`, `browser_ws_test.go`, **`replay_test.go`**, **`manager_reaper_test.go`**, **`browser_release_sites_test.go`** (new) | FR-026a (state + clearing), FR-028–FR-032, **FR-029a**, **FR-031a**, **FR-031b**, FR-041 (emission), FR-042 (the emitter seam + `resolveSessionConnsLocked` delivery), FR-043, FR-043a, FR-044 (id + hold timestamp), FR-047 (state), FR-050, FR-052 (the sweeper's flag duty), FR-060, FR-062, FR-065 | W0 (`OperatorPrompt`, `ControlIdleReleaseSec`), W3 (the `SetBrowserWheelReleaseHook` seam and the `ControlIdleReleaseSec` → `BrowserConfig` translation), W4 (frame types) |
| **W3 — Turn engine: bounded attempts, root-chat identity, release hook, no-park** | `pkg/agent/browser_deferral.go` (new), `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/agent/routing_session_id_consumer_set_adr057_test.go`, `pkg/agent/browser_deferral_test.go` (new), `pkg/agent/browser_resolver_test.go` (new) | FR-002, FR-004, FR-013–FR-017, FR-016a (consumes), FR-021 (stamps the key), FR-022, **FR-029 (the hook declaration and its `processMessage` invocation)**, FR-064 | W0 (`ToolResult.Deferred`, the context key, `OperatorPrompt`, `ControlIdleReleaseSec`), W1 (`ControlGatedToolNames()`) |
| **W4 — Contracts & generated types** | `contracts/components/schemas/*.yaml` (new frame), `contracts/asyncapi.yaml`, `pkg/api/generated/**`, `src/lib/api/generated/**` | FR-042 (schema half) | none — lands first |
| **W5 — SPA live-panel take-over rebuild** | `src/components/browser/BrowserLiveView.tsx`, `BrowserLiveView.takeTheWheel.test.tsx`, `BrowserLiveView.handover.test.tsx` (new), `BrowserLiveView.controlToggle.test.tsx`, **`BrowserLiveView.tabStrip.test.tsx`**, **`BrowserLiveView.agentChip.test.tsx`** | FR-001, FR-053–FR-059 (including FR-031b's unsolicited-`released` handling) | none |
| **W6 — `browser_handover` tool + policy seeding** | `pkg/tools/browser/tools_handover.go` (new), `pkg/tools/browser/tools_handover_test.go` (new), **`pkg/tools/browser/register.go`**, `pkg/coreagent/core.go`, `pkg/coreagent/browser_handover_seed_test.go` (new), `pkg/config/defaults.go` | FR-046, FR-048, **FR-048a**, FR-049, FR-051, FR-052 (the tool's own refusal) | W1 (class rosters), W2 (handover-pending state, the emitter seam) |
| **W7 — Audit event vocabulary** | `pkg/audit/events.go` | the new deferral / handover / `browser_control_idle_release` / take-control-disabled-sweep event constants used by W1 and W2 | none |
| **W8 — Thread rendering + e2e** | **`src/store/chat.ts`**, **`src/components/chat/ChatScreen.tsx`**, `src/components/chat/ChatScreen.browser-handover-notice.test.tsx` (new), **`src/store/chat.browser-handover-notice.test.ts`** (new), `src/lib/ws.ts` consumer wiring, `tests/e2e/browser-control-handover.spec.ts` (new) | FR-042 (render half), FR-044 (SPA idempotency half), e2e | W4, W2 |

**Shared-nothing check**: `pkg/tools` is W0 (`result.go`, `base.go`) only. `pkg/tools/browser` is
split across W1 (tool bodies, audit, and every test file that asserts the deferral marker), W2
(`live.go`, `manager.go`) and W6 (`register.go`, the new tool) with no file in two waves. `pkg/bus`
is W0 only. `pkg/gateway` is entirely W2 — **including no edit to `pkg/gateway/gateway.go`**, because
the release hook is registered inside `browser_ws.go::newBrowserWSHandler`, which is already handed
the `*agent.AgentLoop`. `pkg/channels` is W2 (`base.go` only). `pkg/agent` is entirely W3.
`src/components/browser` is entirely W5; `src/store` and `src/components/chat` are entirely W8.
`pkg/config` is split by file: `config.go` is **W0**, `defaults.go` is **W6**.

**Two ordering notes that are not file collisions.**
1. **`register.go` stays wholly in W6, and that is a deliberate consequence of FR-021's ctx-key
   route.** The first draft routed the root-chat id through `ManagerResolver`, declared in
   `register.go` — a file W6 must own outright for the `browser_handover` registration — which would
   have put one file in W1, W3 and W6 at once while the wave table assigned it to W6 alone, ordered
   *after* both. The tool-context key removes the need for any `register.go` change outside W6.
2. **`ControlIdleReleaseSec` needs no `defaults.go` entry.** `LeaseWaitSec` and `IdleTTLSec` have
   none either — `pkg/config/defaults.go`'s browser block sets neither, and the "unset means the
   shipped default" translation happens at the **reader** (`pkg/agent/loop.go`, e.g.
   `if cfg.Tools.Browser.IdleTTLSec > 0 { browserCfg.IdleTTL = … }`). So the 900 s default lands in
   W3's `loop.go` translation into W2's `BrowserConfig`, and `defaults.go` stays untouched by
   FR-031a — leaving it free for W6's FR-051 tool-policy entry, which must land atomically with
   `coreagent/core.go` per Constraint #6. Until W3 lands, W2's sweeper sees a zero window, which
   means *expiry disabled* — a development-only state, and never a shippable one.

**Cross-wave coupling that is NOT a file collision and must still be respected.** W3's
`routing_session_id_consumer_set_adr057_test.go` reads **`pkg/gateway/websocket.go` live at test
time** — `u19CountClassAInWS5Artefact` opens `../gateway/websocket.go` and parses the comment block
anchored on the exact string `"ADR-057 FR-089 — W5 audit classification artefact"`. **W2 edits that
file.** W2 MUST NOT disturb that anchor, the comment block beneath it, or the class-(a) frame-type
list it enumerates; doing so reddens a test in a package W2 does not own, with a failure message
about ADR-057 that says nothing about the browser change that caused it.

---

## Success Criteria

- **SC-001**: Taking the wheel during an in-flight turn produces zero `turn_canceled` transcript
  entries across 10 consecutive takes.
- **SC-002**: With the wheel held, **at most 3 control-gated browser tool calls per turn reach
  `pkg/tools/browser`; every later call is short-circuited in the engine with zero CDP contact, zero
  lease acquisitions and zero `browser_action` audit rows.** *(Stated this way because the model
  chooses how many times to call, so "the agent makes at most 3 calls" is not a property the system
  can guarantee — and FR-016 explicitly says later calls DO happen. What the system guarantees is
  where they stop, and that is measurable.)*
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
- **SC-009**: A non-operator inbound message on a session with a held wheel — a background delegate
  completion, a goal-loop follow-up, a cron fire, **or a question-card answer or cancel** — produces
  **0** releases across 10 consecutive occurrences of each.
- **SC-010**: A stood-down state with **no proof of life** (no input, no attach/detach, no
  `ViewerHeartbeat`, no live media track) is given up within one idle window
  (`tools.browser.control_idle_release`, default 900s) in 100% of cases, and a scheduled browser turn
  on that session succeeds afterwards. Conversely, a hold whose panel is attached and heart-beating
  survives **3** consecutive idle windows with **0** releases.
- **SC-011**: After the operator releases the wheel without prompting (Escape, annotate, panel
  close), **0** of the next 10 control-gated browser tool calls reach the page — across a turn
  boundary and across a cron fire — until a prompt arrives or the idle window elapses.
- **SC-012**: Every server-initiated release delivers exactly **1** unsolicited
  `browser_status{state:"released"}` to a still-attached former holder, and the panel accepts a
  re-take on the very next click with **0** reloads.

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
| FR-012a | US-3 | The agent's next browser action defers… | `TestToolResult_DeferralIsStructuralNotProse`; `TestToolResult_DeferredNeverCrossesTheWire` |
| FR-013 | US-3 | A new turn starts the attempt count at zero | `TestBrowserDeferralLedger_CountsPerTurnNotPerSession` |
| FR-014 | US-3 | The agent is told to stop attempting… | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` |
| FR-015 | US-3 | The agent is told to stop attempting… | `TestBrowserDeferralLedger_ThirdAttemptCarriesStopInstruction` |
| FR-016 | US-3 | A fourth attempt never reaches the browser | `TestBrowserDeferralLedger_FourthAttemptShortCircuitsWithoutBrowser` |
| FR-016a | US-3 | A fourth attempt never reaches the browser | `TestBrowserTools_ControlGatedToolNamesEqualsActionPlusCapture`; `TestShortCircuit_UsesExportedRosterNotAHardcodedList` |
| FR-017 | US-3, US-4 | A new turn starts…; A delegated worker… | `TestBrowserDeferralLedger_ChildTurnHasItsOwnCounter` |
| FR-020 | US-4 | A delegated worker driving its own tab set defers; An unrelated chat… | `TestControlledResult_ChecksRootChatPanelTabSet`; `TestControlledResult_EmptyRootChatIDSkipsTheSecondCheck` |
| FR-021 | US-4 | A delegated worker driving its own tab set defers | `TestToolContext_CarriesRootChatSessionID` |
| FR-022 | US-4 | — (structural) | `TestRoutingSessionID_ConsumerSetIsClosed` |
| FR-023 | US-4 | A delegated worker driving its own tab set defers | `TestDelegatedChild_DefersOnParentWheelHold` |
| FR-024 | US-4 | An unrelated chat on the same workspace browser… | `TestControlledResult_UnrelatedChatOnSameBrowserDoesNotDefer` |
| FR-025 | US-7 | The next message releases the wheel… | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` |
| FR-026 | US-7 | Escape released the wheel… | `clears the wheel on Escape…`; `TestRelease_DoesNotClearWaitingLine` |
| FR-026a | US-7, US-8 | Escape released the wheel…; A holder who vanished does not leave the agent stood down | `TestStandDown_SurvivesEscapeAndStillDefers`; `TestStandDown_ClearedOnlyByPromptReleaseAndIdleExpiry`; `TestStandDown_VoidedByGhostHolder` |
| FR-027 | US-7 | The next message releases the wheel… | `TestResume_NextPromptIsAnOrdinaryTurnWithNoInjectedState` |
| FR-028 | US-7 | The next message…; The wheel is released even with the panel closed | `TestChatTurnAccept_ReleasesHeldPanelLock`; `TestChatTurnAccept_ReleasesWithPanelClosed` |
| FR-029 | US-7 | The next message releases the wheel…; A background delegate finishing…; Answering a question card…; Cancelling a question card… | `TestChatTurnAccept_ReleasesForNonWebchatChannel`; `TestChatTurnAccept_ReleasesOnSSEPrompt`; `TestChatTurnAccept_SyntheticAsyncNotifyDoesNotRelease`; `TestChatTurnAccept_GoalLoopFollowUpDoesNotRelease`; `TestQuestionCardResume_NeverReleasesTheWheel`; `TestReleaseHook_FiresOnlyOnOperatorPrompt` |
| FR-029a | US-7 | — (structural) | `TestOperatorPromptField_AssignmentPartitionIsPinned` |
| FR-030 | US-7 | A different user's message releases the wheel… | `TestChatTurnAccept_ReleaseAuditsActingUserNotHolder`; `TestChatTurnAccept_ChannelReleaseAuditsCanonicalSenderNotHolder` |
| FR-031 | US-8 | A ghost holder…; A live holder…; A holder who vanished… | `TestControlGate_GhostHolderDoesNotDeferTools`; `TestControlGate_LiveHolderDefersTools`; `TestControlGate_UsesIsControlledByLiveViewerNotIsControlled`; `TestStandDown_VoidedByGhostHolder` |
| FR-031a | US-7 | An idle stand-down is given up by the server; A watched hold does not expire; A held tab set is not reaped for inactivity | `TestControlGate_IdleHoldExpiresAndUngatesTheAgent`; `TestControlGate_HeartbeatingHolderNeverExpires`; `TestIdleSweeper_FiresWithNoAgentRunning`; `TestHandoverPending_ExpiresOnTheSameIdleTimer`; `TestReapIdleSessions_SkipsAControlledOrLatchedTabSet`; `TestIdleRelease_EmitsOperatorVisibleLine` |
| FR-031b | US-7 | A server-initiated release reaches the holder's own panel | `TestServerInitiatedRelease_NotifiesTheFormerHolder`; `clears operator-holds-wheel on an UNSOLICITED server released frame…` |
| FR-032 | US-8 | Two viewers, one wheel | `TestSharedControl_SecondViewerInputStillDispatches`; `TestSharedControl_SecondViewerDoesNotDeferTheAgent` |
| FR-033 | US-5 | Capture attempts defer… (outline) | `TestControlGate_CaptureToolsDeferWhileControlled` |
| FR-033a | US-5 | — (structural) | `TestCaptureTools_DescriptionsCarryTheDeferralClause` |
| FR-034 | US-5 | Wait and tab listing still work… | `TestControlGate_WaitListTabsAndDialogStayUngated` |
| FR-035 | US-5 | — (structural) | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` |
| FR-035a | US-5 | — (structural) | reviewer gate on the two §14.2 documents; mechanically anchored by `TestBrowserTools_ThreeWayGateClassificationMatchesRosters`'s reason strings |
| FR-036 | US-5 | — (structural) | `TestWriteLease_LeasedIffActionClass` |
| FR-037 | US-5 | — (structural) | `TestAudit_PerCallAuditedIffActionClass` |
| FR-038 | US-5 | — (structural) | `TestBrowserTools_ThreeWayGateClassificationMatchesRosters` |
| FR-039 | US-5 | Capture attempts defer… (screenshot row) | `TestScreenshot_DeferredCallProducesNoFileNoMediaNoArtifactTag` |
| FR-040 | US-5 | An earlier capture is not retracted | `TestCapture_PreTakeoverArtifactsAreNotRetracted` |
| FR-041 | US-6 | The waiting line appears live… | `TestTakeControl_EmitsWaitingSurfaceFrame` |
| FR-042 | US-6 | The waiting line appears live… | `TestTakeControl_EmitsWaitingSurfaceFrame`; `TestWaitingSurface_ReachesTheChatConnsAndDegradesWhenNoneAreAttached`; `TestWaitingSurface_AllThreeProducersUseOneEmitter`; `renders the browser-handover waiting notice…`; `make verify-contracts` |
| FR-043 | US-6 | The waiting line survives a reload | `TestTakeControl_WritesWaitingSurfaceTranscriptEntry`; `TestWaitingSurface_MissingSessionDoesNotFailTheRelease` |
| FR-043a | US-6 | The waiting line survives a reload | `TestReplay_HandoverNoticeReplaysAsTheSameFrameType` |
| FR-044 | US-6 | Taking the wheel twice emits one waiting line | `TestTakeControl_SecondTakeWithoutReleaseEmitsOneLine`; `TestWaitingNotice_MessageIDIsStableWithinAHoldAndUniqueAcrossRestarts`; `drops a duplicate browser-handover notice…` |
| FR-045 | US-3, US-6 | The agent ends its turn saying… | **SHOULD — verified by holdout H1, no automated oracle.** Deliberately no matrix row: what a model narrates is not observable from a Go unit test |
| FR-046 | US-9 | The agent hands the browser over… | `TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark` |
| FR-047 | US-9 | After handing over, the agent's own browser attempts defer | `TestHandoverTool_SetsHandoverPendingNotAViewerLock`; `TestHandoverPending_IsNotSubjectToGhostVoiding` |
| FR-048 | US-9 | The agent hands the browser over… | `TestHandoverTool_EmitsWaitingSurface` |
| FR-048a | US-9 | The agent hands the browser over… | `TestHandoverTool_ReasonAppearsInSurfaceAndAudit` |
| FR-049 | US-9 | A handover ends the turn normally… | `TestHandoverTool_TurnEndsCompletedNotParked` |
| FR-050 | US-9 | The next message clears an agent-initiated handover; A handover set by a delegated child… | `TestHandoverPending_ClearedByNextPrompt`; `TestHandoverPending_SetByDelegatedChildIsClearedByRootPrompt`; `TestSessionDeleted_ClearsHoldHandoverAndLatch` |
| FR-051 | US-9 | — (structural) | `TestSeed_BrowserHandoverAtAllThreeConstraint6Sites`; `TestSeed_MiaAndAvaResolveDenyForBrowserHandover`; `TestConstructorSeed_PolicyMapMatchesCatalog`; `TestDefaults_CeilingLengthMatchesStaticCatalog` |
| FR-052 | US-11 | With take-control disabled…; Take-control is switched off while a wheel is held | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` |
| FR-053 | US-2 | The operator's click reaches the page…; The operator keeps driving after the click… | `does not call cancelStream…, and DOES send one browser_control{take} frame`; `keeps dispatching keyboard, wheel and pointermove AFTER the pointerup…`; `does not contain agentPausedByUser anywhere` |
| FR-054 | US-2 | The operator keeps driving after the click…; The operator's click reaches the page… | `keeps dispatching keyboard, wheel and pointermove AFTER the pointerup…`; `keeps you-driving priority over agent-working for the chip and the cursor` |
| FR-055 | US-2 | The wheel is not handed back… | `does not auto-release the wheel after the take ack…` |
| FR-056 | US-2 | The operator's click reaches the page… | `acquires the wheel and dispatches in one click from the frame, the omnibox, the tab strip and Take over` (coverage, not a regression guard) |
| FR-057 | US-2 | A wheel held across the end of one turn survives the start of the next; Escape released the wheel…; A server-initiated release reaches the holder's own panel | `still holds the wheel when a SECOND agent turn starts after the first ended`; `clears operator-holds-wheel on a server released status…`; `clears operator-holds-wheel on an UNSOLICITED server released frame…` |
| FR-058 | US-7 | Escape released the wheel… | `reverts control state and stops accepting input when the socket disconnects mid-control` |
| FR-059 | US-7 | — (structural) | `never renders a Take control / Release control / Hand to agent button` |
| FR-060 | US-7 | — (structural) | existing `handleControl` audit assertions |
| FR-061 | US-1 | The agent's next browser action defers… | `TestDeferralAudit_RecordsSessionRootChatViewerUserTabSetAndTool` |
| FR-062 | US-9 | The agent hands the browser over… | `TestHandoverAudit_RecordsAgentAsActor` |
| FR-063 | US-10 | Stop still cancels the turn | existing cancel-path tests |
| FR-064 | US-4 | A deferred sub-agent reports to its parent… | `TestDelegation_NoParkCascadeFromBrowserDeferral` |
| FR-065 | US-11 | With take-control disabled… | `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` |

**Scenarios not tied to a single FR** (cross-cutting, still traced): *A question card is pending when
the operator takes the wheel* → `TestAskUserQuestionPending_UnaffectedByWheelTake`, asserting the
ADR §4 claim that the two pending states no longer interact. Its two sibling scenarios (*Answering a
question card…* and *Cancelling a question card…*) are traced to FR-029 above.

---

## Assumptions & Ambiguity Warnings

Every row is a decision made without the operator. **The lead must resolve or accept each before
implementation starts.**

| # | What's ambiguous | Assumption taken in this spec | Question to resolve |
|---|---|---|---|
| A1 | D5 vs. the §14.2 rule 3 biconditional (C1) — gating the capture tools makes them write-leased and per-call audited under the current rule | Split into a three-way classification; capture tools are gated but neither leased nor per-call audited. The rule is amended **in the two spec documents that actually carry it** (`browser-workspace-ownership-spec.md` §14.2 rule 3 / FR-019a / AC5 / §12 A17, and `browser-agent-capability-spec.md`'s restatement) — **not** in ADR-075, which has no §14 (FR-035a) | Do you accept amending §14.2 rule 3? ADR-085 records the amendment as §7 R4-c. |
| A2 | D6's waiting surface has no wire path (C2) | A new contract-first chat frame is added, plus the transcript entry, so live and replay agree | Approve the new wire type, or accept a transcript-only line that is invisible until reload? |
| A3 | D4 says "the gateway releases it", but prompts arrive from webchat WS, SSE, question-card resumes and every non-web channel (C7) — **and the one point they all converge on, `PublishInbound`, also carries synthetic traffic** | The **decision** is a fail-closed `bus.InboundMessage.OperatorPrompt` field set at the **three operator-originated publish sites only**; the **action** fires from a gateway-registered hook at `processMessage`, where the session id exists, never at the bus (FR-029, C7.2). The partition is pinned by a structural test on the field's assignment sites (FR-029a). **Revised:** the first draft's four-site helper-call model could not compile at one of those sites, and one of the four (the question-card resume) was the wrong site regardless — see A13 | Confirm the release must cover non-web channels **and** that a background delegate's completion must never release. If webchat-only is acceptable, say so — it leaves a known stale-lock path. |
| A4 | D7's "passes the lock to the operator" is undefined with no attached viewer (C5), and a never-attached holder is exactly D4's void ghost | A distinct handover-pending state on the live view, not a viewer lock; explicitly exempt from the ghost rule | Confirm handover-pending is a separate state, and confirm it should survive a viewer's absence. |
| A5 | ~~Which SPA component renders a system entry in the chat thread~~ | **CLOSED — identified, with a shipping precedent.** `src/store/chat.ts`'s `case 'goal_status'` already synthesizes a `role: 'system'` `ChatMessage` from a frame, via `buildGoalAckInsertion` with a deterministic id from `goalAckMessageId(goalId)` that is idempotent by construction (`if (b.messagesById[ackId]) return null`). It is rendered by `src/components/chat/ChatScreen.tsx::VirtualSystemMessageRow` (virtualised list) and `::SystemMessage` (non-virtualised branch). FR-044 follows that pattern with `(sessionID, holdStartedAtUnixNano)` — a timestamp rather than a process counter, so the id cannot repeat across a gateway restart and suppress a genuine fresh takeover; both files are named in W8 | — (no longer open) |
| A6 | N=3 for the attempt bound — the ADR invites justification | N=3, justified in FR-014 (a deferral is 100% reproducible; >1 attempt only helps the model discover the state on the tool it needed) | Accept N=3, or set a different value? |
| A7 | Whether the deferral audit record (D10) is a new event kind or an extension of `EventBrowserLiveControlTaken` | A distinct record is emitted, sharing the take's field set plus **root chat session id**, tab set and tool name. Turn id is **not** in the set (FR-061: there is no turn id in the tool context) | New `audit.Event` constant, or a `reason` variant on the existing one? |
| A8 | Whether the waiting line's copy should name the agent | Copy is agent-neutral ("the agent has stopped driving the browser…") | Should it name the agent, and if so which one for a delegated subtree? |
| A9 | Whether `browser_handover` should be seeded for the delegation-tier workers (Explorer, Researcher) as well as Jim and Ray | **CLOSED — seeded for all four, on a stated rule rather than a preference: an agent that holds the browser action set holds the verb that stands down from it.** Splitting them would leave Explorer able to drive the operator's browser but unable to hand it back, which is the worse half of the pair. Verified: all four (`IDJim`, `IDRay`, `IDExplorer`, `IDResearcher`) carry `browser_navigate`/`click`/`type` at `allow` today. Mia and Ava need **no edit** — `denyAllThenOverride` gives them an explicit `deny` automatically (FR-051) | — (no longer open; raise it only to overturn the rule) |
| A10 | Whether a take on a session with **no** running turn should emit the waiting line | Yes — the line is emitted on every take, because the operator cannot tell whether a turn is running and the message is true either way | Emit always, or only during a running turn? |
| A11 | Whether the bounded-attempt terminal instruction should also apply across a delegated child's *parent* (i.e. shared budget) | No — per FR-017 each turn has its own counter, so a parent and child each get 3 | Should the budget be shared across a delegation subtree? |
| A12 | The ADR is silent on what happens to an in-flight approval (`ask` policy) on a browser tool when the wheel is taken mid-approval | Untouched — the approval gate is orthogonal; the tool defers at the control gate if it is still held when approval lands. Exercised by holdout **H2**. **Corrected:** the first draft named `browser_upload_file` as the example, because it is the one browser tool seeded `ask` in `defaults.go`. It is also **deliberately unregistered** (`register.go`; `interact_test.go::TestUploadFile_NotRegistered`), so a model can never call it and it can never raise a card. H2 now uses a per-agent `ask` override on a **registered** action verb instead — a supported operator configuration under Constraint #6 layer 2, needing no test build | Confirm no interaction is intended. |
| A13 | Whether a **question-card answer or cancel** is a "prompt" for D4's release rule | **REVERSED IN REVISION 5 — operator-ratified: it does NOT release** (FR-029, C7.1). The first draft treated it as a prompt, gated on `ws_ask_user.go::resumeIsUserInitiated`. That predicate's first branch is `if set.Status == askuser.StatusCancelled { return true }`, so **clicking Cancel would have handed the browser to the agent mid-drive** — the exact D5 exposure, through the release path, on the most ordinary click available. Three reasons the reversal is right independent of the bug: (a) **harm asymmetry** — releasing mid-drive reinstates the D5 exposure, while not releasing costs one deferred round-trip, visible to the operator and bounded by FR-014's N=3; (b) **a card answer is not an ordinary turn** — `DispatchResume` injects a *synthesised* `resumeText` into a turn that is already parked, and the operator composed nothing; (c) **the composer lock is a reason to keep the two states independent, not to fuse them** — the whole point of the edge case is that the wheel and the card do not interact. `resumeIsUserInitiated` stays untouched; it is correct for goal/loop origin gating and MUST NOT be read here | — (ratified; raise only to overturn) |
| A14 | The idle-release window (FR-031a): whether it should be a config key at all, and **what counts as activity** | A config key `tools.browser.control_idle_release`, default **900** seconds, `0` disables. Matches the existing `lease_wait` / `idle_ttl` keys in the same block — and, like them, takes **no `defaults.go` entry**: the "unset means the shipped default" translation happens at the reader, per `IdleTTLSec`'s existing pattern in `pkg/agent/loop.go`. **Corrected in revision 5:** activity is no longer "input or attach/detach". A *reading* operator produces no input, so that definition released a live operator after 900 s and handed the agent the page they were looking at. Activity is now any of input, attach/detach, `ViewerHeartbeat` (the panel's WS pong) or a live media track; a stood-down state with no attached viewer at all runs its window from the transition | Accept 900s, or pick another default? Is an operator-disableable expiry acceptable, given it is now also the only path out of a held wheel when take-control is switched off (FR-052)? |

### Assumptions (non-ambiguous, recorded)

- The control lock, the FR-047 handover-pending flag and the FR-026a stand-down latch all stay process
  memory with no persistence and no boot rehydration; a gateway restart loses all three, which is
  already true of the lock today and which the ADR accepts. **Accepted consequence, stated rather than
  discovered:** a waiting line already written to the transcript is replayed after a restart and reads
  as though the agent were still stood down. It is stale, not false — a message does return the
  browser — and no automated surface distinguishes the two. What a restart must not do is *suppress* a
  genuine fresh takeover, which is why FR-044's notice id is a timestamp rather than a process counter.
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

### H2 — The wheel taken on top of an outstanding approval card (Edge Case)
> *Replaces the previous H2 ("one click, every time"), which was derivable from FR-056's test.*
> *Re-targeted in revision 5: the earlier version required the agent to call `browser_upload_file`,
> which is **deliberately not registered** (`register.go`; `interact_test.go::TestUploadFile_NotRegistered`).
> The model never sees it, so no approval card could ever be raised and the holdout could not be run
> at all — meaning A12 was untested and H2's "tested rather than asserted" claim was false. The point
> was never that particular verb; it is approval-gate × control-gate on the same call.*
- **Setup**: On the test agent, set a per-agent `ask` policy on a **registered** action verb —
  `browser_navigate` or `browser_click` (an ordinary Constraint #6 layer-2 tightening; no test build
  required). Give the agent a browsing task, so an approval card is outstanding and the tool has not
  run.
- **Action**: While the approval is still pending, take the wheel. Then approve the card.
- **Expected**: The approval lands normally — the wheel is not an authorization decision on it. The
  approved call then **defers at the control gate**, as an ordinary non-error deferral, and the agent
  continues. Neither the approval flow nor the composer is left in a stuck state, and exactly one
  waiting line exists. *(This is Ambiguity A12's assumption, tested rather than asserted.)*

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

### H5 — The wheel held while the session's schedule keeps firing (Error Path)
> *Replaces the previous H5 (ghost holder), which was `TestControlGate_GhostHolderDoesNotDeferTools`
> under a different name.*
- **Setup**: A session with a recurring heartbeat or scheduled trigger that drives the browser. Take
  the wheel on it, then **close the live panel and walk away** — no input, no message, and, once the
  panel is gone, no keep-alive. (Revision 5: the earlier version said "attached and alive, not a
  ghost — and then do nothing at all", which under FR-031a's corrected liveness rule now **never**
  expires, by design. An attached, heart-beating panel is proof somebody is watching; H5 is about the
  operator who genuinely left.)
- **Action**: Let at least two scheduled fires elapse **inside** the idle window, then let the idle
  window pass and let a third fire.
- **Expected**: The two fires inside the window defer cleanly and say why — the stand-down latch
  (FR-026a) is what defers them, since closing the panel already cleared the lock — and no scheduled
  run is recorded as failed or cancelled. After the window, the latch is given up server-side, the
  operator sees a line saying so, and the third fire drives the browser normally. **The pass
  condition is that the session is not permanently disabled by an operator who walked away.**

### H6 — Three levels deep (Edge Case)
> *Replaces the previous H6 (two people, one browser), which was FR-030 + FR-032 restated.*
- **Setup**: A chat where the primary agent delegates to a sub-agent which itself delegates one level
  deeper, and the **grandchild** is the one driving the browser on its own tab set.
- **Action**: Take the wheel on the root chat.
- **Expected**: The grandchild defers — its `routingSessionID` is the root's own id, inherited
  verbatim through the whole subtree — and the block propagates back up as ordinary results with no
  turn at any level stopped, parked or cancelled. Then send a message on the root chat: the wheel is
  released and the grandchild's handover-pending state (if any) is cleared too.

### H7 — One operator, two workspaces (Edge Case)
> *Replaces the previous H7 (the disabled installation), which was
> `TestTakeControlDisabled_NoTakeNoLineNoDeferralNoHandover` restated.*
- **Setup**: Two workspaces, each with its own browser and its own running agent, both open to the
  same signed-in operator.
- **Action**: Take the wheel in workspace A, then in workspace B, without releasing either. Drive
  both. Then send a message in workspace A only.
- **Expected**: Both agents defer, independently. Exactly one waiting line appears in each thread —
  not two in either, and not one shared. A's message releases **only** A's wheel; B's agent is still
  deferred and B's line is still there. Nothing in either workspace's audit trail names the other.

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
- **Deployment / runtime**: `tools.browser.take_control_enabled` is reused as the feature's kill
  switch — but it is no longer a *pure* switch: flipping it false on a running install now gives up
  any held wheel within one sweeper tick (FR-052), so operators disabling it mid-session will see the
  browser return to the agent rather than freeze. **One new config key**:
  `tools.browser.control_idle_release` (`ControlIdleReleaseSec int`, default 900, `0` disables) for
  FR-031a. It needs a doc comment on the field and **no entry in `pkg/config/defaults.go`** —
  `LeaseWaitSec` and `IdleTTLSec` have none either; the "unset means the shipped default" translation
  happens at the reader (`pkg/agent/loop.go`, following `IdleTTLSec`'s existing
  `if cfg.Tools.Browser.IdleTTLSec > 0 { … }` shape), never "unset means 0". *(The first draft said it
  needed a `defaults.go` default; that was wrong, and it is what put `defaults.go` in two waves.)*
- **Footprint**: the new state is, per live view, two flags (handover-pending, stand-down), one
  hold-start timestamp and one last-control-activity timestamp; per registry, one 30-second ticker;
  per turn, one small counter; per inbound message, one bool — well inside the <10 MB
  security-overhead budget (Constraint #3).
