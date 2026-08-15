# ADR-065 — A channel belongs to one (workspace, agent) pair, in both directions

- **Status:** Accepted (2026-08-14) — operator decision. Design only; no code has been written against it yet.
- **Date:** 2026-08-14
- **Deciders:** founder (decided the ownership model); lead (mechanism)
- **Related:** [ADR-029](ADR-029-channel-instance-workspace-binding.md) (bound a channel instance to one workspace + agent identity, **inbound only**); the M11 email tools (`pkg/agent/email_tools.go`, `pkg/tools/email.go`) whose model this follows; [ADR-064](ADR-064-remove-main-sentinel-agent.md) (same family of defect — an identity nobody could govern).
- **Evidence level:** claims marked **[VERIFIED]** were read from code at commit `f52f1988` by two independent reviewers, one of them tasked with refuting the other. Exploitability caveats are marked where they were not reproduced.
- **Spec:** [channel-agent-ownership-spec.md](../specs/channel-agent-ownership-spec.md)

---

## 1. Decision

A channel instance is owned by exactly one **(workspace, agent)** pair — the pair the operator
already selects in the channel Configure panel.

That pair is the sole authority in **both** directions:

- **Inbound**, a message on that instance is handled by that agent, in that workspace. *(Already
  true — ADR-029.)*
- **Outbound**, only that agent may send through that instance, and only from a turn running in
  that workspace. **Another agent may not use it, including an agent on the same workspace's
  team.** *(Not true today.)*

This is the email model applied to channels, with **one deliberate divergence**. An agent's
mailbox is keyed `(agent, workspace)` and the email tools take **no target parameter at all**, so
a wrong target is unrepresentable. `send_message` cannot do that: **one tool addresses every
channel type**, so naming the target is inherent to its design and an agent holding two channels
must choose between them — only it knows, in context, which one fits.

So channels get the same ownership property by **validation** rather than by impossibility: the
parameter stays, and a value outside what the acting agent owns is refused. That is strictly the
weaker mechanism, and the spec's §2.1 records what it costs and what must never be "simplified"
away.

## 2. Context — the binding is enforced in one direction only

**Inbound is strict on the ADR-029 path — with one documented bypass. [VERIFIED]** `ChannelInstanceConfig.WorkspaceID` + `Identity{kind:"agent"}`
form the pair; `ChannelInstanceConfig.IsWorkspaceBound` is the predicate;
`routing.ResolveRoute` treats a bound instance at **Priority 0**, overriding the whole binding
cascade. Its drift guard **drops** a message (`MatchedBy: "bound.drift.drop"`) rather than let a
bound instance fall through to the global default. The instance id is stamped at a single choke
point (`channels.BaseChannel.HandleMessage`) and explicitly never sourced from message content —
a STRIDE spoofing guard.

**The bypass:** `hand_off` pins another agent to a live conversation, and that pin is consulted
*before* `ResolveRoute` and returns early (`pkg/agent/loop.go`, the `sessionActiveAgent` lookup
keyed `"chat:"+channel+":"+chatID`). A handed-off agent therefore answers on an instance it does
not own, without the Priority-0 path running at all. This is correct behaviour — a user asking
for a different agent should get one — but it means "inbound is strict" is true of the routing
cascade, not of the system. The spec's FR-2 makes the rule explicit rather than leaving it to be
rediscovered.

**Outbound has no counterpart at all. [VERIFIED]**

- `tools.MessageTool.Parameters` exposes `channel` and `chat_id` as **model-supplied** optionals.
  `Execute` prefers the model's values and only falls back to `ToolChannel(ctx)` / `ToolChatID(ctx)`
  when they are empty.
- The callback wired in `pkg/agent/loop.go` (`messageTool.SetSendCallback`) forwards
  `channel, chatID, content` straight to `bus.PublishOutbound`. No ownership, workspace or
  instance check at any layer down to `w.ch.Send`.
- `bus.OutboundMessage` carries no `AgentID` and no `WorkspaceID`. Its `InstanceID` field is
  never set by any non-test code — dead on this path.
- `channels.Manager.dispatchLoop` keys `m.channels` by **instance**, so a model naming
  `telegram.beta` reaches *that exact instance*, precisely and repeatably.

**Consequence.** An agent can emit through another workspace's channel instance, using that
instance's own bot token. On the wire the message is indistinguishable from the other
workspace's bot. `webchat` is itself a registered channel, so the same primitive can push frames
into another user's live browser session.

**Two things already do this correctly. [VERIFIED]** `send_file` exposes only `path` and
`filename` and resolves the destination from turn state. The email tools refuse a target for the
same reason, and say so in their own doc comment. `send_message` is the lone outlier.

**No accepted decision says it should be.** A full read of ADR-029, its three review rounds, its
spec and the spec review found the outbound direction is never addressed — not as in-scope,
out-of-scope, a non-goal, a risk, or a follow-up. This is an omission, not a trade-off someone
made.

## 3. Exploitability — stated honestly

Reaching another workspace's chat needs the instance key **and** a valid `chat_id`. There is no
chat-listing tool. **[VERIFIED]**

The config-reading oracle (`get_config`) is **denied to all four base agents** — checked against
a running gateway, not inferred. **[VERIFIED]** But the **global tool-policy ceiling for it is
`allow`**, so an operator-created agent gets it unless denied explicitly, and
`get_config "channels"` redacts secrets while leaving instance keys, `workspace_id` and
`identity.id` in the clear. **[VERIFIED — corrected in round-1 review; the first draft of this
section stated only the base-agent half and read as narrower than the truth.]**

So: not self-serving for the shipped roster, self-serving for a custom agent with default
permissions. It remains a **boundary that is not enforced** rather than an open door — an
attacker still needs a valid `chat_id`, and the fix is a design change rather than a hotfix — but
the severity is higher than the first draft implied, and it strengthens rather than weakens the
case.

## 4. Relationship to the operator's standing ruling on send tools

The operator has ruled that send tools must not be guarded: *"agents that have the send tools
can send and that must not be guarded — if we want to prevent that we set the tool permissions
accordingly."*

That ruling governs **whether** an agent may send, and names tool permissions as the mechanism.
It is not a ruling about **which instance** a permitted send may egress through. The two are
separable, and this ADR changes only the second: sending stays entirely ungated by policy, and
the destination becomes a property of the turn rather than a model-supplied argument — exactly
as `send_file` and the email tools already behave.

## 4a. A session must record the channel INSTANCE

Implementing this exposed a gap in the data model, not merely in the code.

A channel session recorded workspace, agent, bare channel type and peer — but **not the instance
key**. Since an install can hold a hundred WhatsApp numbers, each bound to its own
`(workspace, agent)` pair, every one of their sessions read `channel: "whatsapp"` and they were
indistinguishable once persisted. The in-memory dedup index had always keyed on the instance; the
stored record had not, so the distinction died at process restart.

That made the ownership model unrepresentable in stored data: anything acting on "the sessions of
this channel" acted on all of them. The first version of the re-stamp did exactly that, and would
have moved ninety-nine other conversations to the wrong workspace.

`UnifiedMeta.InstanceID` closes it. The field threads through the ADR-057 identity file, and the
rule for consumers is that an EMPTY instance means *unknown* and must never be treated as a match.

**Worth recording for the next person adding a session field:** the identifiers are enumerated by
hand in three separate places — the write projection, the read composition, and the post-write
cache refresh. Adding a field to two of the three compiles cleanly and silently half-works; that
is how this one first appeared to persist and then read back empty.

## 5. Consequences

- `send_message` loses `channel` and `chat_id` as model-supplied parameters. Any agent prompt or
  skill relying on them breaks; this is a deliberate break, consistent with the project's
  no-back-compat posture.
- An agent that owns no channel instance in the current workspace cannot send proactively. It
  gets a clear refusal naming the reason, not silence.
- Cross-workspace announcement — one agent messaging several workspaces' channels — is **no
  longer possible through this path**. If that is ever wanted it must be an explicit, separately
  designed capability with its own tool and its own policy entry, not an emergent property of an
  unchecked parameter.
- One channel connection still serves exactly one workspace (ADR-029 FR-1, unchanged). Two
  workspaces needing the same platform use two connections.

## 6. Alternatives rejected

**Remove `channel`/`chat_id` from `send_message` entirely, as the email tools do.** ~~Chosen~~ —
**rejected by the operator, correctly.** `send_message` is the single tool for every channel
type, so removing the target does not make it safer, it makes it unable to do its job: an agent
that owns both a Telegram and a Slack channel has no way to say which it means. The first two
drafts of this ADR treated the parameter as a defect to delete; it is a requirement of the
design.

What survives from that reasoning is the warning, not the remedy: with validation, every path
from a `send_message` call to the bus must pass the same check, and a missed one is silent. That
is why the decision carries a second check at dispatch (spec FR-6) rather than trusting a single
site.

**Carry `AgentID`/`WorkspaceID` on `OutboundMessage` and check at dispatch.** Useful for audit
and probably worth doing anyway, but insufficient alone: it validates late, after the message
has been composed and queued, and the failure surfaces far from the cause.

**Leave it, and rely on tool permissions.** Rejected because permissions are per-agent and
cannot express "may send, but only through its own channel". Denying `send_message` outright to
stop cross-workspace egress would also stop the agent replying in its own conversation.
