# Spec — Channel ownership per (workspace, agent), enforced on destination selection

- **Status:** Draft, revision 1 (2026-08-14). Implements [ADR-065](../architecture/ADR-065-channel-ownership-per-agent-workspace.md).
- **Round-1 review:** [channel-agent-ownership-spec-review.md](channel-agent-ownership-spec-review.md) — verdict BLOCK, 31 findings. This revision resolves all four CRITICALs and the eight MAJORs; §8 records what remains open.
- **Scope:** the OUTBOUND half. Inbound is ADR-029's and is only regression-pinned here.
- **Model followed:** the M11 email tools. Where a rule has an email equivalent, it is named.

---

## 1. The rule, and the one distinction that makes it work

A channel instance is owned by one **(workspace, agent)** pair — already stored as
`ChannelInstanceConfig.WorkspaceID` + `Identity{kind:"agent", id:…}`, already selected in the
Configure panel. No new storage, no new UI, no schema change.

**Ownership governs DESTINATION SELECTION. It does not govern the turn's own conversation.**

That sentence is the whole revision. Round 1 established that the codebase has at least three
routine ways an agent legitimately transacts on a conversation it does not own, and a rule that
ignored them would have muted delegation and hand-off across every workspace-bound channel:

- **Replying where the turn already is** is always allowed. The conversation was established by
  the inbound path, which ADR-029 and the hand-off pin have already adjudicated. Re-deciding it
  at send time would override those decisions from the wrong end.
- **Choosing a destination when the turn has none** — proactive sends, heartbeats, scheduled
  jobs — is where ownership binds. Nothing has adjudicated anything yet, and this is the only
  path on which an agent could reach a channel that was never part of its turn.

`send_message` losing its `channel`/`chat_id` parameters (FR-1) is what makes the distinction
enforceable rather than advisory: with no way to name a destination, an agent can only reach the
conversation it is in, or the one ownership resolves for it.

---

## 2. Functional requirements — the destination

### FR-1 — `send_message` takes no destination

`tools.MessageTool.Parameters` MUST expose **only** `content`. `channel` and `chat_id` are removed.

> **Given** any agent turn
> **When** the model calls `send_message` with a `channel` or `chat_id` argument
> **Then** no such parameter exists in the schema, and any supplied value is ignored
> **And** the destination comes solely from the turn or from FR-3.

*Email equivalent: the five email tools expose no mailbox parameter; `EmailTransports.resolve`
reads `ToolWorkspaceID(ctx)`.*

This single FR is the load-bearing one. Everything below either follows from it or covers a path
it cannot reach.

### FR-2 — A turn always replies where it is

**Given** a turn with `ToolChannel(ctx)` and `ToolChatID(ctx)` set
**When** `send_message` executes
**Then** it sends to exactly that channel and chat
**And** ownership is NOT consulted.

Ownership is not consulted here **by design**, and this holds even when the sending agent does
not own the instance. Three cases reach this deliberately:

- **Hand-off.** `hand_off` pins another agent to a live conversation. The pin is consulted
  *before* `ResolveRoute` and returns early (`pkg/agent/loop.go`, the `sessionActiveAgent` lookup
  keyed `"chat:"+channel+":"+chatID`), so it bypasses ADR-029's Priority-0 path. A user on Mia's
  bound instance asking for Ava gets Ava — and Ava must be able to answer.
- **Delegation.** See FR-2a.
- **A workspace-bound instance whose owning agent changed** after the conversation started.

### FR-2a — A delegated sub-turn sends into the parent's conversation, as itself

**Given** a sub-turn spawned by `spawnSubTurn`, which inherits `Channel`, `ChatID` and
`WorkspaceID` from the parent while running under the delegate's own agent identity
**When** the delegate calls `send_message`
**Then** the message goes to the parent's conversation
**And** it is attributed to the delegate (`ToolAgentID(ctx)`), not to the parent
**And** ownership is not consulted (FR-2).

Without this, every delegated worker on a workspace-bound channel is silently mute. `delegate` is
in the always-visible manifest (`pkg/tools/manifest.go`, `fullManifestToolNames`) — this is a
high-traffic path, not an edge case.

### FR-3 — A proactive send resolves the agent's OWN instance for the turn's workspace

For a turn with no inbound conversation — heartbeat, scheduled job, background work:

**Given** an agent `A` and a turn whose workspace is `W`, where `W` is
`tools.ToolWorkspaceID(ctx)` — the same signal the email tools use, and the only one threaded
through every turn type
**When** `send_message` executes with no turn channel/chat
**Then** the target is the instance owned by exactly `(W, A)`
**And** if `A` owns no instance in `W`, the call fails naming that fact
**And** if `W` is empty, the call fails naming that fact — it does NOT fall back to a
single instance, because a turn with no workspace has not established which workspace's channel
it is entitled to
**And** if `A` owns more than one instance in `W`, see FR-3a.

*Email equivalent: `EmailTransports.resolve` errors naming the candidates rather than choosing.
It differs on the empty case — email falls back to the single mailbox when there is exactly one.
Channels deliberately do not, because a mailbox is addressed to a person while a channel reaches
a room.*

### FR-3a — Multiple owned instances: deterministic, logged, overridable

**Given** `A` owns two or more instances in `W`
**When** a proactive send resolves
**Then** the instance whose key sorts lowest is chosen
**And** a WARN names every candidate and the one chosen.

Refusing outright (round-1's proposal) leaves a two-channel agent with no proactive voice at all
and no way for an operator to fix it, since the config permits the ambiguity permanently. A
deterministic pick with a loud log is recoverable; silence is not. **See §8 Q2** — the operator
may prefer an explicit primary flag instead.

### FR-3b — A delegated proactive send resolves against the delegate

**Given** a sub-turn with no inherited conversation
**When** the delegate sends proactively
**Then** resolution uses the **delegate's** own ownership, not the root parent's.

Chosen because the delegate is the acting identity (`ToolAgentID(ctx)`) and FR-6 attributes the
message to it; resolving against a different agent than the one attributed would make the audit
trail lie. **See §8 Q3** — the alternative (resolve against the root parent, on the grounds that
the work is the parent's) is defensible and unchosen.

### FR-4 — An agent cannot select an instance it does not own

**Given** instance `I` owned by `(W1, agentA)`
**When** `agentB` — including an agent on `W1`'s own team — attempts to make `I` the destination
of a proactive send
**Then** the send does not occur, and the refusal names ownership.

This is the core requirement, and it is now precisely bounded: it governs **selection**, so it
never fires on FR-2's inbound path. Team membership on the same workspace does not confer use of
another agent's channel, exactly as it does not confer use of another agent's inbox.

### FR-5 — Cross-workspace egress is impossible through this path

**Given** agent `A` in workspace `W1`, and instance `I2` owned by `(W2, agentB)`
**When** `A` sends
**Then** the message can only leave through the turn's own conversation (FR-2) or `A`'s own
instance in `W1` (FR-3)
**And** `I2`'s credentials are never used.

---

## 3. Functional requirements — attribution and audit

### FR-6 — Agent-originated outbound messages carry their origin

`bus.OutboundMessage` MUST carry `AgentID` and `WorkspaceID`, populated for every
**agent-originated** send.

`InstanceID` is NOT added: `Channel` already **is** the instance key — `channels.Manager` keys
`m.channels` by instance id, and `dispatchLoop` looks up `m.channels[msg.Channel]` directly. A
second field holding the same value is a divergence waiting to happen. The existing declared-but-
never-set `InstanceID` field should be removed or documented as unused.

### FR-6a — Attribution is observable

A successful agent-originated send MUST emit an audit event carrying agent, workspace, instance
and whether the destination came from the turn (FR-2) or from resolution (FR-3). A refusal under
FR-3/FR-4 MUST increment a counter distinguishable from a transport failure.

Without this, FR-6's origin fields are recorded and never read, and FR-7's WARN is the only
signal that anything was refused.

### FR-7 — Dispatch verifies agent-originated messages only

**Scope, stated explicitly rather than left to emerge from empty fields:** FR-7 applies to
messages with a non-empty `AgentID`.

**Given** a message with a non-empty `AgentID`
**When** `channels.Manager.dispatchLoop` resolves its instance
**Then** the message is refused, with a WARN naming agent, workspace and instance, if
`(AgentID, WorkspaceID)` does not own that instance.

**Exempt, by enumeration:** the ~19 non-agent producers — the agent's own streamed reply,
`notifyDrop` backpressure notices, schedule delivery, device notifications, and the rest. These
are system-originated, are not model-addressable, and carry no `AgentID`. They pass unchecked.

Defence in depth only: FR-1 should make this unreachable. If it fires, something upstream
regressed, and the WARN says which.

### FR-7a — Streaming is out of scope, and the spec says so

The highest-volume egress path does **not** construct an `OutboundMessage` at all — streamed
token delivery bypasses the bus. FR-6's audit trail therefore does **not** cover the majority of
delivered bytes, and FR-7 cannot see them.

This is accepted, not overlooked: streaming carries the turn's own conversation (FR-2 territory,
where ownership is not consulted anyway) and is not model-addressable. Recorded so nobody reads
FR-6 as a complete egress ledger.

### FR-8 — Unbound instances keep today's behaviour

**Given** an instance with no `WorkspaceID`
**When** an agent sends through it
**Then** behaviour is unchanged.

"No workspace (global default routing)" remains a valid operator choice. This spec does not force
binding; it makes binding mean something outbound as well as inbound.

### FR-9 — webchat is protected by FR-1, not by ownership

`webchat` has **no ownership pair to check**: it is registered synthetically, is absent from
`knownChannelTypes`, and has no `ChannelInstanceConfig`. An ownership rule there has nothing to
read, and FR-8 would exempt it in any case.

What protects it is FR-1: with no `chat_id` parameter, a turn can only reach its own session.
`webchatChannel.Send` resolving a recipient from `msg.ChatID` alone is safe once no model can
supply one.

---

## 4. Test plan

Every row is a behaviour with a pass/fail outcome. Level is Unit (U), Integration (I) or
End-to-end (E).

| # | Lvl | Setup | Action | Expected | Traces |
|---|-----|-------|--------|----------|--------|
| 1 | I | `A` owns `tg.acme` in `W1`; inbound chat on it | reply | delivered to that chat | FR-2 |
| 2 | I | `tg.acme` owned by `(W1, Mia)`; `hand_off` pins Ava to the chat | Ava replies | delivered; ownership not consulted | FR-2, C1 |
| 3 | I | Mia's turn on `tg.acme` delegates to Ava | Ava sends | delivered to parent's chat, attributed to Ava | FR-2a, C2 |
| 4 | I | `A` owns `tg.acme` in `W1`; heartbeat, no inbound | proactive send | delivered via `tg.acme` | FR-3 |
| 5 | U | `A` owns nothing in `W1`; heartbeat | proactive send | refused; message names the absence | FR-3 |
| 6 | U | turn has empty `ToolWorkspaceID` | proactive send | refused; names the missing workspace | FR-3 |
| 7 | U | `A` owns `tg.acme` + `slack.acme` in `W1` | proactive send | lowest key chosen; WARN lists both | FR-3a |
| 8 | U | sub-turn, no inherited conversation, delegate owns an instance | proactive send | resolves against the delegate | FR-3b |
| 9 | U | `B` on `W1`'s team owns nothing; `A` owns `tg.acme` | `B` proactive send | refused on ownership | FR-4 |
| 10 | U | — | inspect `send_message` schema | no `channel`, no `chat_id` property | FR-1, FR-5 |
| 11 | I | instance unbound | any send | unchanged | FR-8 |
| 12 | I | webchat turn | send | reaches only the turn's own session | FR-9 |
| 13 | U | agent-originated send | inspect bus message | carries agent + workspace; no `InstanceID` field | FR-6, M4 |
| 14 | U | **populated but mismatched** `(AgentID, WorkspaceID)` vs instance | dispatch | refused + WARN | FR-7 |
| 15 | U | `notifyDrop` / schedule / device notification (empty `AgentID`) | dispatch | passes unchecked | FR-7 exemption |
| 16 | U | successful send | inspect audit | event carries origin and FR-2-vs-FR-3 provenance | FR-6a |

Row 14 replaces round-1's row 10, which could not fail: an empty-field message is exempt under
FR-7's stated scope, so the old row tested nothing.

---

## 5. Regression impact — behaviours that change

Round 1 was right that this was understated. Enumerated:

1. **`send_message` loses two parameters.** Any prompt, skill or scheduled-job payload passing
   `channel`/`chat_id` stops steering the destination. Deliberate; it is the fix.
2. **Scheduled jobs naming a channel in their payload** no longer control delivery by that means;
   they resolve under FR-3 like any other proactive send. This needs an audit of existing job
   payloads before implementation.
3. **A proactive send from an agent owning nothing in the workspace now fails** where it
   previously went somewhere. Louder, and intended, but it is a behaviour change.
4. **Cross-workspace announcement through this path stops working.** If wanted, it becomes a
   separate capability with its own tool and its own policy entry — never an emergent property of
   an unchecked argument.

Unchanged and regression-pinned: inbound routing (ADR-029), hand-off, delegation, `send_file`,
the email tools, streaming.

---

## 6. Non-goals

- **Not** changing whether an agent may send. That stays governed solely by tool policy, per the
  operator's standing ruling. This spec changes only *where* a permitted send may go.
- **Not** changing inbound. ADR-029 stands.
- **Not** per-workspace tool policy. Policy remains per-agent.
- **Not** cross-workspace announcement.
- **Not** touching `send_file`, which already resolves from turn state and is the reference
  implementation here.
- **Not** covering streamed bytes (FR-7a).

---

## 7. Migration

No stored data changes. The ownership pair is already persisted and already set through the UI.
The breakage is behavioural and enumerated in §5, consistent with the project's no-back-compat
posture.

---

## 8. Open questions for the operator

**Q1 — A scheduled job whose agent owns no channel in the workspace.** FR-3 refuses and says so.
The alternative is falling back to any conversation the agent has open, which is how today's
unchecked behaviour resolves it. Refusing is proposed because a background job silently choosing a
recipient is how a message reaches someone nobody intended — but a misconfigured heartbeat then
goes quiet rather than talking.

**Q2 — Two owned instances in one workspace.** FR-3a picks the lowest key and logs loudly. The
alternatives are an explicit `primary: true` on the instance (clean, but contradicts "no schema
change") or refusing outright (leaves a two-channel agent with no proactive voice). If you expect
agents to hold two channels routinely, the primary flag is the better answer and the schema cost
is worth paying.

**Q3 — Proactive resolution inside a delegated turn.** FR-3b resolves against the delegate. The
alternative — resolve against the root parent, since the work is the parent's — is equally
defensible. FR-3b was chosen so that the agent attributed in the audit trail is the agent whose
ownership was checked.
