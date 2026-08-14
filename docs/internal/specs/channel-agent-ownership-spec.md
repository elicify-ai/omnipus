# Spec — A channel belongs to one (workspace, agent) pair

- **Status:** Draft, revision 3 (2026-08-14). Implements [ADR-065](../architecture/ADR-065-channel-ownership-per-agent-workspace.md).
- **Reviews:** [round 1](channel-agent-ownership-spec-review.md) (BLOCK, 31) · [round 2](channel-agent-ownership-spec-review-round2.md) (BLOCK, 20). Revision 3 additionally applies three operator decisions that overturned parts of revision 2 — see §7.
- **Scope:** OUTBOUND. Inbound is ADR-029's and is only regression-pinned here.
- **Model followed:** the M11 email tools, with one deliberate divergence (§2.1).

---

## 1. The rule

**A channel instance is owned by one (workspace, agent) pair. A message leaves through a channel
the ACTING AGENT owns in the turn's workspace. There are no exemptions.**

Ownership is already stored as `ChannelInstanceConfig.WorkspaceID` + `Identity{kind:"agent", id}`
and already set in the Configure panel. No new storage, no new UI, no schema change.

### 1.1 Session, turn, acting agent

- A **session** is the ongoing conversation. It sits on a channel instance.
- A **turn** is one unit of work in it. Its only role here is to say **which agent is acting**.
- **Ownership never consults the turn's conversation.** Revisions 0 and 1 made "the turn's own
  conversation" an exemption. That was wrong twice over: it put the turn inside the ownership
  rule, and its justification — "the inbound path already adjudicated this conversation" — is
  false for several turn types. A scheduled run's channel and chat come straight from
  `job.Payload` with **no validation at schedule-create: not ownership, not bindings, not even
  existence** (`pkg/gateway/schedules.go::scheduledRunner.run`). Nothing adjudicated anything.

### 1.2 Sending is first-class

An agent **initiates**; it is not only a receiver. One rule covers replies and proactive sends
alike. There is no primary path and no exceptional one.

### 1.3 Mechanics — [VERIFIED] at `b3e802e7`

Neither case revision 1 built exemptions for actually needs one.

**Hand-off produces a NEW TURN in the SAME SESSION.** `hand_off` stores the target under
`"session:"+sessionID` and `"chat:"+channel+":"+chatID` (`pkg/agent/loop.go`, the
`sessionActiveAgent` store block), and the pin routes the **next inbound message**. The handed-to
agent is not sending inside someone else's turn — it is the acting agent of its own turn.
Ownership is checked against it, which is the right question.

**A delegated sub-turn does not send.** `pkg/agent/subturn.go` contains **zero** outbound
publishes; the delegate's text returns to its parent and the parent sends. A delegate sends only
when it explicitly calls `send_message` — an ordinary send, checked like any other.

---

## 2. Functional requirements — the destination

### FR-1 — `send_message` keeps its destination parameters, restricted to what the agent owns

`send_message` addresses **every** channel type through one tool, so naming the target is
inherent to its design, not a flaw. The agent must be able to choose — only it knows, in context,
whether something belongs on Slack or on Telegram.

**Given** acting agent `A`, turn workspace `W` = `tools.ToolWorkspaceID(ctx)`
**When** `send_message` is called with a `channel`
**Then** the send proceeds only if that channel is owned by `(W, A)`
**And** otherwise it is refused, naming the channels `A` does own in `W`.

### 2.1 Why this differs from the email model, and what it costs

The email tools expose **no** target parameter at all, so a wrong target is unrepresentable.
This spec cannot do that: one tool serves every channel, and an agent holding two must pick.

So the safety property moves from **unrepresentable** to **validated** — strictly the weaker
mechanism. It holds only while every path that turns a `send_message` call into an outbound
message passes through the same check. **This is the load-bearing sentence of the spec:** if a
future change adds a second route to the bus that skips validation, the guarantee is gone with no
test failing. FR-6 exists to make that visible; do not remove the check as "redundant" without
reading this paragraph.

### FR-2 — Unspecified destination resolves to the agent's own

**Given** `send_message` called with no `channel`
**When** the turn has a conversation
**Then** it replies there, provided that conversation's instance is owned by `(W, A)` — see FR-3
**And when** the turn has no conversation
**Then** the target is `A`'s instance in `W`; if `A` owns exactly one, it is used
**And** if `A` owns several, the call is refused, naming them, so the agent chooses explicitly
**And** if `A` owns none, or `W` is empty, the call is refused naming that fact.

*Refusing rather than guessing between several is the email precedent
(`EmailTransports.resolve`), and matches the operator's ruling that the agent picks.*

### FR-3 — The acting agent is the turn's agent

**Given** a turn whose acting agent is `A` — including one created by a hand-off pin, and
including a delegated sub-turn
**When** resolution runs
**Then** `A` is `tools.ToolAgentID(ctx)`, never the session's original agent and never a parent.

**Operator decision (Q1):** after a hand-off, replies leave through the **new** acting agent's
channel. If that agent owns none in the workspace, the session goes quiet and the refusal says
why. The hand-off itself is **not** blocked — accepting it and then failing audibly was chosen
over refusing it up front.

### FR-4 — An agent cannot reach a channel it does not own

**Given** instance `I` owned by `(W1, agentA)`
**When** any other agent acts — including one on `W1`'s own team, a handed-to agent, or a delegate
**Then** `I` is never its destination.

Team membership does not confer use of another agent's channel, exactly as it does not confer use
of another agent's inbox.

### FR-5 — The ownership record must not be agent-writable

`set_config` is `allow` in the global ceiling, `channels.` is a permitted prefix, and no
blocked-key rule covers it — so an agent with shipped-default permissions can rewrite
`channels.<id>.identity.id` and `channels.<id>.workspace_id`. **[VERIFIED, round 2]**

Both MUST be added to the blocked-key table for agent-initiated config writes. A constraint the
constrained party can edit is not a constraint.

---

## 3. Functional requirements — attribution and enforcement

### FR-6 — Agent-originated messages carry their origin, and dispatch re-checks them

`bus.OutboundMessage` MUST carry `AgentID` and `WorkspaceID` for agent-originated sends.
`InstanceID` is NOT added: `Channel` already **is** the instance key
(`channels.Manager.dispatchLoop` looks up `m.channels[msg.Channel]`). The existing
declared-but-never-set `InstanceID` should be removed or documented as unused.

**Scope of the check:** messages with a non-empty `AgentID`.

**Given** such a message
**When** dispatch resolves its instance
**Then** it is refused with a WARN if `(AgentID, WorkspaceID)` does not own that instance.

This is the second half of §2.1: because validation replaced impossibility, one check at the tool
layer is a single point of failure. This one sits at the last common point before the wire.

**Exempt by enumeration:** the ~19 system producers — streamed replies, `notifyDrop` backpressure,
schedule delivery, device notifications — carry no `AgentID`, are not model-addressable, and pass
unchecked.

A successful send MUST emit an audit event carrying agent, workspace and instance. A refusal MUST
increment a counter distinguishable from a transport failure.

### FR-7 — Streaming is out of scope, explicitly

Streamed token delivery constructs no `OutboundMessage` and bypasses the bus, so FR-6's audit
trail does **not** cover the majority of delivered bytes. Accepted: streaming carries the
session's own conversation and is not model-addressable. Recorded so nobody reads FR-6 as a
complete ledger.

### FR-8 — Remove the dormant scheduled-delivery plumbing

**Operator decision (Q3):** this is legacy to be deleted, in the same manner as the `main`
sentinel — not migrated, not validated, removed.

The retired Schedules UI left a whole delivery mode behind. `pkg/cron/service.go::CronPayload`
still carries `Channel`, `To` and `Deliver`, where **`Deliver: true` means "send straight to the
channel with no agent turn at all"**. `contracts/components/schemas/Schedule.yaml` and
`ScheduleCreate.yaml` expose `channel` and `chat_id`, so the server still accepts them; nothing in
the product sends them.

This is worse than an unused field: a path that emits to a channel **with no agent involved** is
one no ownership rule can ever govern. Scheduled work is tasks and heartbeats, and those carry no
channel.

Removal MUST follow the contract-first process — schema first, regenerate, commit atomically —
and MUST include an audit of any persisted job carrying these fields.

### FR-9 — Unbound instances keep today's behaviour

An instance with no `WorkspaceID` behaves as it does now. "No workspace (global default routing)"
remains a valid operator choice.

### FR-10 — webchat is protected by validation, not by ownership

`webchat` has no ownership pair to check: registered synthetically, absent from
`knownChannelTypes`, no `ChannelInstanceConfig`. Since FR-1 keeps the `chat_id` parameter, webchat
is protected by FR-1's validation and FR-6's re-check, **not** by an ownership record it does not
have. `webchatChannel.Send` resolving a recipient from `msg.ChatID` alone is safe only while those
checks hold — which is why §2.1 matters here most.

---

## 4. Test plan

| # | Lvl | Setup | Action | Expected | Traces |
|---|-----|-------|--------|----------|--------|
| 1 | I | `A` owns `tg.acme` in `W1`; inbound chat | reply, no channel named | leaves via `tg.acme` | FR-2 |
| 2 | I | same, heartbeat, no inbound | proactive send | leaves via `tg.acme` — same path | FR-2, §1.2 |
| 3 | U | `A` owns `tg.acme` + `slack.acme` in `W1` | send naming `slack.acme` | delivered via `slack.acme` | FR-1 (agent picks) |
| 4 | U | as 3 | send naming no channel | refused, names both | FR-2 |
| 5 | U | `A` owns `tg.acme` only | send naming `tg.beta` (another agent's) | refused, names what `A` owns | FR-1, FR-4 |
| 6 | I | `tg.acme` owned by `(W1,Mia)`; hand-off pins Ava; Ava owns `slack.acme` | Ava replies | leaves via **Ava's** channel | FR-3 |
| 7 | I | as 6, Ava owns nothing in `W1` | Ava replies | refused; session goes quiet with a reason | FR-3, Q1 |
| 8 | I | Mia's turn delegates to Ava | delegate returns text | parent sends via Mia's channel; no delegate send | §1.3 |
| 9 | U | delegate calls `send_message`, owns nothing in `W` | send | refused on ownership | FR-4 |
| 10 | U | empty `ToolWorkspaceID` | any send | refused, names the missing workspace | FR-2 |
| 11 | U | agent attempts `set_config channels.<id>.identity.id` | write | rejected as a blocked key | FR-5 |
| 12 | U | populated but mismatched `(AgentID, WorkspaceID)` vs instance | dispatch | refused + WARN | FR-6 |
| 13 | U | `notifyDrop` / device notification (empty `AgentID`) | dispatch | passes unchecked | FR-6 exemption |
| 14 | U | agent-originated send | inspect bus message | carries agent + workspace; no `InstanceID` | FR-6 |
| 15 | U | schedule create carrying `channel`/`chat_id` | request | rejected — field no longer exists | FR-8 |
| 16 | U | persisted job with `Deliver: true` | load | audited and removed; no agentless emission path remains | FR-8 |
| 17 | I | instance unbound | any send | unchanged | FR-9 |
| 18 | I | webchat turn | send | reaches only the turn's own session | FR-10 |
| 19 | I | **inbound routing regression pin** | inbound on bound instance | ADR-029 behaviour unchanged | regression |

---

## 5. Regression impact

**Losses.** An agent naming a channel it does not own is now refused where it previously
succeeded. An agent owning several must name one rather than having one chosen. Scheduled
delivery to a channel is removed outright (FR-8), including the agentless `Deliver: true` path.
Cross-workspace announcement through this route ends.

**Gains.** FR-2 gives heartbeats, `/loop` runs and delegated sub-turns a *defined* destination
they do not have today. This is a widening as well as a tightening, and the widening is
deliberate: an agent that owns a channel should be able to speak on it.

**Unchanged and pinned:** inbound routing (ADR-029, row 19), the hand-off pin mechanism,
`send_file`, the email tools, streaming.

---

## 6. Non-goals

Not changing **whether** an agent may send — that stays tool policy, per the operator's standing
ruling; this spec changes only **where**. Not changing inbound. Not per-workspace tool policy. Not
cross-workspace announcement. Not touching `send_file`. Not covering streamed bytes (FR-7).

---

## 7. Operator decisions applied in revision 3

**Q1 — hand-off to an agent owning no channel:** accept the hand-off, then fail audibly on the
reply (FR-3). Refusing the hand-off up front was rejected.

**Q2 — an agent owning two channels:** **the agent picks.** This overturned revision 2, which
removed the destination parameter entirely and had the system choose. `send_message` serves all
channels, so the parameter is inherent and the agent has to ground its choice properly. See §2.1
for what this costs in enforcement strength.

**Q3 — scheduled runs naming a channel:** the plumbing is dormant legacy and is **removed**
(FR-8), not migrated and not validated.

No open questions remain.
