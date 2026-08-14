# Spec — Channel ownership per (workspace, agent), enforced in both directions

- **Status:** Draft (2026-08-14). Implements [ADR-065](../architecture/ADR-065-channel-ownership-per-agent-workspace.md).
- **Scope:** the OUTBOUND half. Inbound is already correct per ADR-029 and is only regression-pinned here.
- **Model followed:** the M11 email tools. Where a rule below has an email equivalent, it is named.

---

## 1. The ownership pair

A channel instance is owned by one **(workspace, agent)** pair, already stored as
`ChannelInstanceConfig.WorkspaceID` + `Identity{kind:"agent", id:…}` and already selected by the
operator in the channel Configure panel. No new storage, no new UI, no schema change.

`ChannelInstanceConfig.IsWorkspaceBound` remains the single predicate for "is this instance
owned?". An unbound instance keeps today's legacy behaviour (FR-8).

---

## 2. Functional requirements

### FR-1 — `send_message` takes no destination

`tools.MessageTool.Parameters` MUST expose **only** `content`. The `channel` and `chat_id`
properties are removed.

> **Given** an agent turn on any channel
> **When** the model calls `send_message` with a `channel` or `chat_id` argument
> **Then** the argument is not present in the schema and is ignored if supplied
> **And** the destination is resolved solely from the turn.

*Email equivalent: the five email tools expose no mailbox parameter; `EmailTransports.resolve`
reads `ToolWorkspaceID(ctx)`.*

### FR-2 — The destination is the turn's own conversation

**Given** a turn with `ToolChannel(ctx)` and `ToolChatID(ctx)` set
**When** `send_message` executes
**Then** it sends to exactly that channel and chat, and no other.

### FR-3 — A proactive send resolves the agent's OWN instance for the current workspace

For a turn with no inbound conversation (heartbeat, scheduled job, delegated background work):

**Given** an agent `A` and a turn whose workspace is `W`
**When** `send_message` executes with no turn channel/chat
**Then** the target is the instance owned by exactly `(W, A)`
**And** if `A` owns no instance in `W`, the call fails with a message naming that fact
**And** if `A` owns more than one instance in `W`, the call fails and names them, rather than choosing.

*Email equivalent: `EmailTransports.resolve` returns the single transport when there is exactly
one, and errors naming the candidates when there are several.*

### FR-4 — An agent cannot send through an instance it does not own

**Given** instance `I` owned by `(W1, agentA)`
**When** `agentB` — including an agent on `W1`'s own team — attempts to send through `I`
**Then** the send does not occur
**And** the refusal names ownership as the reason.

This is the core requirement. Team membership on the same workspace does **not** confer use of
another agent's channel, exactly as it does not confer use of another agent's inbox.

### FR-5 — Cross-workspace egress is impossible through this path

**Given** agent `A` owning an instance in `W1`, and instance `I2` owned by `(W2, agentB)`
**When** `A` sends
**Then** the message can only leave through `A`'s own instance in the turn's workspace
**And** `I2`'s credentials are never used.

### FR-6 — Outbound messages carry their origin

`bus.OutboundMessage` MUST carry `AgentID`, `WorkspaceID` and a populated `InstanceID`.

Not the enforcement point — FR-1 is, by removing the parameter — but it makes a violation
detectable in audit rather than invisible, and `InstanceID` is currently declared and never set
by any non-test code.

### FR-7 — Dispatch verifies before it sends

`channels.Manager`'s dispatch MUST refuse a message whose `(AgentID, WorkspaceID)` does not own
the resolved instance, logging at WARN with all three ids.

Defence in depth. FR-1 should make this unreachable; if it ever fires, something upstream
regressed and the log says so.

### FR-8 — Unbound instances keep today's behaviour

**Given** an instance with no `WorkspaceID`
**When** an agent sends through it
**Then** current behaviour is unchanged.

The operator can still choose "No workspace (global default routing)". This spec does not force
binding; it makes binding mean something on the way out as well as in.

### FR-9 — `webchat` is subject to the same rule

`webchat` is a registered channel and `webchatChannel.Send` currently looks up a session by
`ChatID` with no ownership check. It MUST NOT be exempt.

---

## 3. Non-goals

- **Not** changing whether an agent may send at all. That stays governed solely by tool policy,
  per the operator's standing ruling. This spec changes only *where* a permitted send may go.
- **Not** changing inbound. ADR-029 is correct and stays.
- **Not** introducing per-workspace tool policy. Policy remains per-agent.
- **Not** building cross-workspace announcement. If wanted, it is a separate capability with its
  own tool and its own policy entry — never an emergent property of an unchecked argument.
- **Not** touching `send_file`, which already resolves from turn state and is the reference
  implementation here.

---

## 4. Test data — the cases that must be covered

| # | Setup | Action | Expected |
|---|---|---|---|
| 1 | `A` owns `tg.acme` in `W1`; turn is an inbound chat on it | reply | delivered to that chat |
| 2 | `A` owns `tg.acme` in `W1`; heartbeat turn, no inbound | proactive send | delivered via `tg.acme` |
| 3 | `A` owns nothing in `W1`; heartbeat turn | proactive send | refused, names the absence |
| 4 | `A` owns `tg.acme` **and** `slack.acme` in `W1`; no inbound | proactive send | refused, names both |
| 5 | `B` on `W1`'s team, owns nothing; `A` owns `tg.acme` | `B` sends | refused on ownership (**FR-4**) |
| 6 | `I2` owned by `(W2, B)`; `A` runs in `W1` | `A` targets `I2` | impossible — no parameter (**FR-1/FR-5**) |
| 7 | instance unbound | any send | unchanged (**FR-8**) |
| 8 | webchat session owned by another user | `A` targets it | refused (**FR-9**) |
| 9 | any successful send | inspect the bus message | carries agent, workspace, instance (**FR-6**) |
| 10 | forged `(AgentID, WorkspaceID)` at dispatch | dispatch | refused + WARN (**FR-7**) |

---

## 5. Migration and breakage

Deliberate break, consistent with the project's no-back-compat posture: any prompt or skill that
passes `channel`/`chat_id` to `send_message` stops working. Those arguments are what this spec
exists to remove.

No stored data changes. The ownership pair is already persisted and already set through the UI.

---

## 6. Open question for the operator

**A scheduled job or heartbeat whose agent owns no channel in the workspace.** FR-3 refuses and
says so. The alternative is to let it fall back to any conversation the agent has open, which is
how the current unchecked behaviour would resolve it. Refusing is proposed because a background
job silently choosing a recipient is how a message ends up somewhere nobody intended — but it
does mean a misconfigured heartbeat goes quiet rather than talking to someone.
