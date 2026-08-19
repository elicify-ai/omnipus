# Spec review — channel-agent-ownership-spec.md (Round 1 of 2)

- **Spec under review:** `docs/internal/specs/channel-agent-ownership-spec.md` (Draft, 2026-08-14)
- **ADR:** `docs/internal/architecture/ADR-065-channel-ownership-per-agent-workspace.md`
- **Worktree / branch:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/wt-macos-seatbelt` — `feat/remove-main-sentinel-agent`
- **Mode:** structured-spec (FR-ids + Given/When/Then + a test-data table; no traceability matrix, no SC-ids, no TDD plan)
- **Method:** every claim below was checked against code in this worktree. Where a finding rests on
  a specific file/symbol, it is cited. Nothing here is inferred from the spec's own narrative.

---

## 1. Executive summary

Thirty-one findings: **4 CRITICAL, 8 MAJOR, 9 MINOR, 10 OBSERVATION**.

The spec's *diagnosis* is sound and independently confirmed — `send_message` really does take a
model-supplied destination, dispatch really does key by instance, and `OutboundMessage` really does
carry no origin. The *prescription* is where it breaks: the spec models the system as
"one agent, one turn, one channel", and the codebase has at least three routine ways an agent ends
up legitimately transacting on a conversation whose instance it does not own (hand-off, delegated
sub-turns, and the ~19 non-agent producers of outbound messages). FR-2 and FR-4 give opposite
answers in all three, and FR-7 as written would refuse almost every outbound message the product
sends today. FR-9 (webchat) cannot be implemented at all as written: webchat has no
`ChannelInstanceConfig`, so it has no ownership pair to check.

**Verdict: BLOCK.**

---

## 2. Findings

### CRITICAL

#### C1 — FR-2 and FR-4 contradict each other, and `hand_off` reaches the contradiction on the main path

**Sections:** FR-2, FR-4, test-data row 5.

`hand_off` pins a different agent to a live conversation, and that pin is consulted **before**
`ResolveRoute` and returns early — so it bypasses the ADR-029 Priority-0 bound-instance path
entirely (`pkg/agent/loop.go`, the `sessionScopeKey(msg)` / `sessionActiveAgent.Load(scopeKey)`
block, which `return`s a `routing.ResolvedRoute` directly). For a channel chat the pin key is
`"chat:"+channel+":"+chatID` (`pkg/agent/loop.go`, `onHandoffFrontend`), so the pin is per-chat and
survives across messages.

Concretely: instance `tg.acme` is bound to `(W1, Mia)`. A user in that chat says "let me talk to
Ava". Ava is now the turn's agent, on Mia's instance.

- FR-2 says: send to the turn's own conversation. Ava sends. Allowed.
- FR-4 says: "an agent cannot send through an instance it does not own", explicitly including
  "an agent on `W1`'s own team". Ava is refused.

The spec never names `hand_off` and gives no rule. Whichever way an implementer reads it is a
shipped product decision: if FR-4 wins, hand-off on every workspace-bound channel becomes a silent
mute (the new agent answers into the void); if FR-2 wins, FR-4 — "this is the core requirement" —
is unenforced on the ordinary path and only constrains proactive sends, which is not what the ADR
claims to be buying.

**Fix.** Decide and state it. The defensible rule is that FR-4 governs **destination selection**
(FR-3's proactive resolution) and never the **turn's own inbound conversation** (FR-2), because
inbound already established the conversation legitimately and hand-off/ADR-029 already decide who
may answer it. Say that in one sentence in §1, and add a BDD scenario for the handed-off agent
replying on a bound instance. If instead FR-4 is meant to override FR-2, the spec must say what
hand-off on a bound instance does now, and ADR-029's "inbound is strict [VERIFIED]" needs
amending — the hand-off pin makes it not strict.

#### C2 — FR-4 makes every delegated sub-agent mute, and test-data row 5 codifies that as the goal

**Sections:** FR-4, test-data row 5.

`spawnSubTurn` builds the child's `processOptions` with `Channel`, `ChatID`, `SenderID`,
`TranscriptSessionID` and `WorkspaceID` all sourced from `parentTS` — the child "is still answering
within the PARENT's conversation/room, just running as a different agent identity"
(`pkg/agent/subturn.go`, the `WorkspaceID: parentTS.opts.WorkspaceID` field and its 20-line doc
comment). The child's *agent identity*, and therefore `tools.ToolAgentID(ctx)`
(`pkg/agent/loop.go`, `turnCtx = tools.WithAgentID(turnCtx, ts.agent.ID)`), is the delegate's own.

So a delegated worker running under Mia's turn on `(W1, Mia)`'s instance has
`ToolAgentID == "ava"`, `ToolWorkspaceID == "W1"`, and the turn's channel is Mia's instance. It
owns nothing. FR-4 refuses its `send_message`. Test-data row 5 — "`B` on `W1`'s team, owns nothing
… refused on ownership (**FR-4**)" — is exactly this shape, presented as the desired outcome.

This is a product-behaviour change of a different order from "prompts that pass `channel` break",
and §5 (Migration and breakage) does not mention it. `delegate` is in the always-visible tool
manifest (`pkg/tools/manifest.go`, `fullManifestToolNames`) — delegation is a first-class,
high-traffic path, not an edge case.

**Fix.** Same structural resolution as C1: scope FR-4 to destination selection, not to the turn's
inherited conversation. Then add an explicit FR (and BDD scenario) stating that a delegated
sub-turn sends into the parent's conversation as itself, and that FR-3's proactive resolution for a
delegated turn resolves against **which** agent — the delegate or the root parent. Both are
defensible; neither is stated.

#### C3 — FR-7 refuses ~19 of the 20 existing outbound producers, or is a no-op; the spec picks neither

**Sections:** FR-6, FR-7, test-data row 10.

There are 20 non-test constructions of `bus.OutboundMessage` in the tree. Exactly one of them is
the `send_message` callback. The other 19 include:

| Producer | File |
|---|---|
| the agent's ordinary final reply | `pkg/agent/loop.go` (`opts.SendResponse && result.finalContent != ""`) |
| reasoning output | `pkg/agent/loop.go` `handleReasoning` |
| session-worker rejection notices | `pkg/agent/session_worker.go` |
| task-executor notifications | `pkg/agent/task_executor.go` |
| schedule `deliver=true` delivery, schedule-failure alerts | `pkg/gateway/schedules.go` (×2) |
| device pairing notifications | `pkg/devices/service.go` |
| backpressure drop notices | `pkg/channels/manager.go` `notifyDrop` |
| media-fallback notice, `SendToChannel` | `pkg/channels/manager.go` (×2) |
| cancel acknowledgements | `pkg/channels/cancelparse.go` |

None of them has an agent, and several have no meaningful workspace. FR-7 says dispatch "MUST
refuse a message whose `(AgentID, WorkspaceID)` does not own the resolved instance" with no
exemption rule. Read literally, the ordinary agent reply, every drop notice, every device
notification and every schedule delivery stops. Read as "empty means exempt", FR-7 becomes
trivially satisfiable by leaving the fields empty — which is what 19 of 20 producers already do,
making it a no-op that test-data row 10 cannot detect (row 10 says "forged", but no model can set
these fields; only Go code can, and Go code that "forges" would just leave them empty).

The `notifyDrop` case is the sharpest: it is the mechanism that tells a user their message was
dropped under backpressure. If FR-7 refuses it, backpressure becomes silent — the exact failure
mode `notifyDrop` exists to prevent (see its doc comment).

**Fix.** FR-7 must state (a) which messages are in scope — proposal: only messages that carry a
non-empty `AgentID`, i.e. agent-originated sends — and (b) what happens to the rest, explicitly, as
a listed exemption rather than as a consequence of empty fields. Then either row 10 changes to a
Go-level unit test that constructs a *populated but mismatched* triple, or FR-7 is honestly
labelled "audit-only, non-enforcing" and moved out of the enforcement section.

#### C4 — FR-9 (webchat) is not implementable as written and contradicts FR-8

**Sections:** FR-8, FR-9, test-data row 8.

`webchat` has no `ChannelInstanceConfig` and cannot have one:

- It is not in `knownChannelTypes` (`pkg/config/config.go`) — a `channels.webchat` entry would be
  dropped on load with a WARN.
- It is registered synthetically at boot: `runningServices.ChannelManager.RegisterChannel("webchat", wch)`
  (`pkg/gateway/gateway.go`), and the Manager itself documents webchat as the canonical example of
  "a synthetic/internal registration … which has no `config.Channels` entry"
  (`pkg/channels/manager.go`, `channelTypeForRateLimit`).

So webchat has no `WorkspaceID` and no `Identity` — the ownership pair FR-9 wants to apply does not
exist for it. And because it has no `WorkspaceID`, FR-8 ("an instance with no `WorkspaceID` … current
behaviour is unchanged") explicitly exempts it. FR-8 and FR-9 are in direct contradiction, and FR-9
loses on the code.

Test-data row 8 compounds it: "webchat session owned by another user" is about session
`meta.Owner`, an entirely different ownership concept from `(workspace, agent)`. The spec conflates
the two.

The underlying risk the ADR identifies is real and I reproduced the mechanism:
`webchatChannel.Send` resolves recipients from `msg.ChatID` alone
(`c.wsHandler.sessionIDs[msg.ChatID]` → `collectSessionConnsLocked`) with no owner check, so a
model-supplied `chat_id` does push frames into another user's live browser session. But that is
fixed **entirely by FR-1** — remove the parameter and the model can no longer name another
session. FR-9 as a separate ownership rule is either redundant or infeasible.

**Fix.** Delete FR-9's "same rule" framing. Replace with: "webchat carries no ownership pair
(no `config.Channels` entry, synthetic registration). FR-1 is what protects it: with no `chat_id`
parameter, a turn can only reach its own session." Then re-write row 8 to assert that — an agent
cannot reach a session other than the turn's — rather than asserting an ownership refusal that has
nothing to check.

---

### MAJOR

#### M1 — "the turn's workspace" is undefined, and the two candidate signals provably diverge

**Section:** FR-3 ("a turn whose workspace is `W`"), FR-4, FR-5.

Two different workspace signals exist on a turn, and the code comment that introduces the second
one says in terms that they diverge **in both directions**:

> "those are genuinely different signals that can diverge in both directions — a CoreTeam member
> responding via an unbound channel still has `ts.opts.WorkspaceID == ""`; a channel bound to a
> workspace can route to an agent stale-removed from that workspace's CoreTeam."
> — `pkg/agent/loop.go`, the "Filesystem re-rooting" block

- `ts.opts.WorkspaceID` → `tools.WithWorkspaceID` → `ToolWorkspaceID(ctx)`. Sourced from the
  **session's meta** (`transcriptStore.GetMeta(...).WorkspaceID`), falling back to the inbound
  `workspace_id` metadata key, else `""`.
- `workspace.FindForAgent` — CoreTeam membership, keyed on agent identity.

The spec does not say which one FR-3/FR-4/FR-5 mean. Picking the wrong one produces silent
misbehaviour in both directions.

Worse, **neither is guaranteed non-empty**. `WithWorkspaceID` is only applied `if ts.opts.WorkspaceID != ""`,
so `ToolWorkspaceID(ctx)` is `""` for every turn on an unbound channel and for `/loop` self-paced
runs (`pkg/agent/loop_scheduler.go` calls `ProcessScheduled(..., "", "")` — no channel, no chat).
FR-3 has no branch for `W == ""`. FR-8 covers "instance with no `WorkspaceID`", which is a
different thing.

Note the email model the spec cites **does** have that branch and it is not a refusal:
`EmailTransports.resolve` (`pkg/tools/email.go`) falls back to the single transport when no
workspace is bound and the agent has exactly one mailbox. FR-3 cites this sentence as its
"email equivalent" but adopts only its multi-candidate error, not its single-candidate fallback.

**Fix.** Name the signal explicitly (`tools.ToolWorkspaceID(ctx)` is the one that matches the email
precedent and is already threaded). Add an FR branch for `W == ""` and say whether it mirrors
email's single-instance fallback or refuses. Add a test-data row for it.

#### M2 — The enforcement choke point is unnamed, and three egress paths bypass the one the spec implies

**Section:** FR-7 ("`channels.Manager`'s dispatch").

`Manager` has at least three ways a message reaches a channel, and only one of them is the
dispatch loop:

1. `dispatchOutbound` → `dispatchLoop` → worker queue → `sendWithRetry`. This is presumably what
   FR-7 means.
2. `Manager.SendMessage` and `Manager.SendToChannel` (`pkg/channels/manager.go`) enqueue **directly**
   onto `w.queue` / call `sendWithRetry`, never touching the bus or `dispatchLoop`. Live caller:
   voice-transcription echo (`pkg/agent/loop.go`, `cm.SendMessage(...)`).
3. **Streaming bypasses the bus entirely.** `Manager.GetStreamer` → `sc.BeginStream(ctx, chatID)`
   returns a `Streamer` the agent loop writes tokens into directly. No `OutboundMessage` is ever
   constructed, so no FR-6 fields and no FR-7 check. For every `StreamingCapable` channel this is
   the *normal* delivery path for an agent's reply — the `OutboundMessage` path is the fallback.
   `pkg/channels/cancelparse.go` similarly calls `ch.Send(ctx, bus.OutboundMessage{...})` directly.

The spec asserts "FR-1 should make this unreachable", which is true only for the `send_message`
tool. It says nothing about streaming, which is the highest-volume egress in the product.

**Fix.** Name the exact function FR-7 lands in. Then either state that paths 2 and 3 are
out of scope and why (defensible: they are not model-addressable), or add coverage. At minimum the
spec must acknowledge that streaming exists and that FR-6's audit trail therefore does not cover
the majority of delivered bytes.

#### M3 — FR-3's ambiguity refusal has no operator escape hatch, and the config permits the ambiguity permanently

**Sections:** FR-3, §6 Open question, test-data row 4.

`config.ValidateChannels` (`pkg/config/config.go`) enforces the key format and the FR-029
half-bound guard. It does **not** enforce uniqueness of `(WorkspaceID, Identity.ID)`. So an
operator can legitimately bind both `telegram.acme` and `slack.acme` to `(W1, Mia)` — a normal
setup, one team reachable on two platforms.

Under FR-3 that agent's heartbeats and scheduled runs refuse forever. There is no "primary
instance" field, no UI control, and the spec adds none ("No new storage, no new UI, no schema
change", §1). The operator's only remedy is to unbind one channel, which turns off ADR-029 inbound
routing for it.

§6 raises the *zero-instance* case for operator decision but not the *multi-instance* case, which
is the one with no workaround at all.

**Fix.** Add the multi-instance case to §6 as a second operator question, and enumerate the
options: (a) a `primary: true` flag on the instance (contradicts "no schema change"); (b) refuse,
and accept that a two-channel agent has no proactive voice; (c) deterministic pick (e.g. lowest
instance key) with a WARN. Whichever is chosen must also cover what happens when a *scheduled job*
already names `payload.channel` — see M6.

#### M4 — FR-6's `InstanceID` duplicates a field that already carries the instance key

**Section:** FR-6.

`OutboundMessage.Channel` **is** the instance key today, not the channel type:

- `Manager.initChannel` keys `m.channels[instanceID]` and calls `ch.SetInstanceID(instanceID)`.
- `BaseChannel.SetInstanceID` sets `c.name = id` (`pkg/channels/base.go`).
- `HandleMessage` therefore emits `Channel: c.name` == the instance key, which becomes
  `ToolChannel(ctx)` and comes straight back as `OutboundMessage.Channel`.
- `dispatchLoop` looks up `m.channels[msg.Channel]`.

Populating `InstanceID` as well creates two fields for one key with no stated invariant: must they
agree? which does FR-7's "the resolved instance" mean? what does dispatch do when they disagree?
The spec is silent, and a disagreement is exactly the state a forged/regressed producer would be in.

(The tool's own parameter description still says "target channel (telegram, whatsapp, etc.)" —
i.e. the *type* — which is the terminology confusion this finding is downstream of. FR-1 deletes
that description, which conveniently removes the evidence but not the ambiguity.)

**Fix.** Either drop `InstanceID` from FR-6 and state that `Channel` is the instance key, or keep
it and state the invariant (`InstanceID == Channel`, enforced at dispatch, mismatch = refuse +
WARN). Do not ship both fields undefined.

#### M5 — FR-6/FR-7 promise audit detectability but specify no audit event and no counter

**Sections:** FR-6 ("makes a violation detectable in audit"), FR-7 ("logging at WARN").

Nothing audits an outbound send today. `pkg/audit/events.go` has no `channel.send.*` event, and the
existing channel events are `channel.pairing`, `channel.routing.drift_drop`,
`channel.routing.changed`, `channel.instance.deleted`, `channel.instance.configured`. The event set
is an **allowlist** (`pkg/audit/audit.go`; `pkg/audit/hardening_test.go` pins validity), so a new
event name is a required, enumerable change — not something an implementer can add ad hoc.

The project's own precedent for exactly this shape is one function away: the ADR-029 drift drop
emits `al.driftDropped.Add(1)` **and** an `audit.EventChannelRoutingDriftDrop` entry with
`instance_id` / `workspace_id` / `intended_agent_id` / `chat_id` / `reason`
(`pkg/agent/loop.go`, the `if route.Drop` block). FR-7 specifies a WARN log and nothing else.

A WARN at the default log level is visible, but it is not queryable, not counted, and not on the
audit chain — so "detectable in audit" is not delivered by anything the spec requires.

**Fix.** Add an FR: a new allowlisted audit event (proposal: `channel.send.ownership_denied`,
Decision `deny`) with the same field set as the drift-drop entry, plus an
`omnipus_channel_send_ownership_denied_total` counter, mirroring `driftDropped`. Add a test-data
row asserting both fire exactly once per refusal.

#### M6 — Regression impact is understated; at least three documented behaviours change and none is listed

**Section:** §5 Migration and breakage.

§5 claims the only breakage is "any prompt or skill that passes `channel`/`chat_id`". Found in the
tree:

1. **`deliver=false` schedules rely on this.** `pkg/gateway/schedules.go` documents it explicitly:
   "M6: for `deliver=false`, `ProcessScheduled` runs the turn with `SendResponse:false`, so the
   agent's final text reply is NOT auto-published to any channel. **If the agent wants to message a
   user, it does so via the message tool during its turn.**" Under FR-3 that only works if the
   agent owns exactly one instance in the resolved workspace. Under M1's unresolved `W == ""` it
   may not work at all.
2. **`send_message` is described in-tree as the inter-agent tool.** `pkg/agent/loop.go` registers it
   under the comment "Message tool — **outbound inter-agent message** via bus", and
   `docs/internal/uat/uat-plan-tools-2026-06.md` exercises it as *"Send a message to Jim: 'please
   review the docs'"*. If inter-agent messaging via `send_message` is real, FR-1 removes it and the
   spec should say so; if it is not real, the tool comment and the UAT plan are wrong and should be
   corrected in the same change.
3. **Schedules have a second, independent destination resolver the spec never reconciles.**
   `resolveAgentDefaultChannel` (`pkg/gateway/schedules.go`) resolves "the agent's channel" by
   scanning `cfg.Bindings[]` for `b.AgentID == agentID` — a *third* notion of agent↔channel
   association, unrelated to both `ChannelInstanceConfig.Identity` and CoreTeam membership. After
   this spec, a proactive `send_message` and a schedule-failure alert for the same agent can
   resolve to different channels.

**Fix.** Rewrite §5 as an enumerated impact list with a decision per row. Add an FR reconciling
`resolveAgentDefaultChannel` with FR-3, or declare it out of scope in §3 by name.

#### M7 — §4 is a case list, not a test plan; two of its ten rows are not testable behaviours

**Section:** §4 Test data.

- No test levels. Rows 1–5 are unit-testable against a fake; rows 6, 9, 10 are Go-level; row 8 needs
  a live WS harness. Nothing says which.
- **Row 6 is not a test.** "impossible — no parameter" asserts the absence of a schema property.
  That is a one-line assertion on `MessageTool.Parameters()`, not the cross-workspace scenario the
  row describes. The actual FR-5 property (an agent in `W1` cannot cause egress through `W2`'s
  instance) needs a test that exercises a real second instance.
- **Row 10 is not reachable as described.** "forged `(AgentID, WorkspaceID)`" — those fields are set
  by Go code, never by a model. The realistic regression is a *new producer that leaves them empty*,
  which row 10 does not cover (see C3).
- **The scope claims to regression-pin inbound** ("Inbound is already correct per ADR-029 and is
  only regression-pinned here") and then contains **zero** inbound rows.
- No boundary rows: instance deleted mid-turn; agent removed from the workspace's CoreTeam mid-turn;
  config reload (`Manager.Reload`) racing an in-flight send; `Identity.Kind` present but non-`agent`;
  instance disabled but still in `cfg.Channels`.
- No concurrency row, despite `Manager.Reload` rebuilding `m.channels` under `m.mu` while workers
  drain queues that already hold messages resolved against the *old* map.

**Fix.** Convert §4 into a table with a Level column, split row 6, replace row 10 per C3, add the
inbound regression pins the scope promises, and add the boundary rows above.

#### M8 — The ADR's exploitability assessment is narrower than stated, and the spec inherits it as severity framing

**Section:** ADR §3, which the spec's priority rests on.

The ADR states: "The one config-reading oracle (`get_config`) is **denied to all four base agents**
on a live instance — checked against the running gateway, not inferred. **[VERIFIED]**"

That is accurate but incomplete. `get_config: deny` is set in the **base-agent** seed
(`pkg/coreagent/core.go`, inside `tightenGlobalCeiling`). The **global ceiling** in
`pkg/config/defaults.go` is `"get_config": "allow"`. Per Constraint #6, every agent resolves from an
explicit policy entry, and an operator-created `Main`/`Subagent` agent seeded from the global
default therefore gets `get_config: allow`.

And `get_config "channels"` does return the instance map. Redaction
(`pkg/sysagent/tools/config.go`, `sensitiveConfigNameFragments` = `api_key`/`secret`/`token`/`password`)
masks credential values — but **not** instance keys, **not** `workspace_id`, and **not**
`identity.id`. Those are precisely the identifiers the ADR says an attacker would need.

This does not change the decision — it strengthens it — but it means the "not self-serving today"
framing understates the exposure for the normal case (a user-created agent), and the spec's
priority and rollout urgency should be re-derived from the corrected picture.

**Fix.** Amend ADR §3 to distinguish base agents from operator-created agents, and state that
`get_config "channels"` discloses instance keys and bindings. Re-state the exploitability
conclusion accordingly.

---

### MINOR

- **N1 (FR-1, silent ignore).** "the argument … is ignored if supplied" — silently discarding a
  model-supplied argument is the "green but broken" pattern this project has repeatedly been bitten
  by. An unknown property should produce a tool error naming the removal, so a stale prompt fails
  loudly instead of sending somewhere the author did not intend. State which.
- **N2 (FR-1, schema vs. runtime).** The Given/When/Then mixes two mechanisms in one Then ("not
  present in the schema" **and** "ignored if supplied"). Split into two scenarios; they have
  different implementations and different failure modes.
- **N3 (`send_file` asymmetry).** §3 calls `send_file` "the reference implementation" and refuses to
  touch it, but `SendFileTool.Execute` (`pkg/tools/send_file.go`) has **no** FR-3 proactive
  resolution — it returns `"no target channel/chat available"` when the turn has no channel. After
  this change, an agent on a heartbeat can send a message but not a file. State whether that
  divergence is intended.
- **N4 (`SendCallback` signature).** FR-6 requires `AgentID`/`WorkspaceID` on the bus message, but
  the callback is `func(channel, chatID, content string) error` (`pkg/tools/message.go`) and closes
  over nothing agent-scoped. The spec does not say how the tool obtains them
  (`ToolAgentID(ctx)` / `ToolWorkspaceID(ctx)` are the obvious answer, but the callback takes no ctx).
- **N5 (FR-2, "and no other").** `handleReasoning` (`pkg/agent/loop.go`) publishes to
  `ch.ReasoningChannelID()` — a *different* chat on the same channel, by design. Under a literal
  FR-2 that is a violation. Name it as in-scope or exempt.
- **N6 (media half).** `OutboundMediaMessage` already carries `WorkspaceID` but no `AgentID`/
  `InstanceID` (`pkg/bus/types.go`). FR-6 covers only the text message, leaving the two bus types
  asymmetric with no rationale.
- **N7 (terminology).** "channel", "channel instance", and "instance" are used interchangeably
  throughout (§1, FR-2, FR-6, FR-7, §4). Given M4, a two-line glossary is load-bearing, not cosmetic.
- **N8 (unbound + FR-3 interaction).** FR-8 preserves legacy behaviour for unbound *instances*, but
  FR-3's proactive resolution scans for instances owned by `(W, A)` — an agent whose only channel is
  unbound owns nothing under FR-3 and is refused, while FR-8 promises "unchanged". State the
  precedence.
- **N9 (internal channels).** `dispatchLoop` silently `continue`s for `cli`/`system`/`subagent`
  (`pkg/constants/channels.go`). FR-7 must state that internal channels are exempt before the
  ownership check, or the check will be written after a `continue` that already ate the message.

---

### OBSERVATION

- **O1.** No success criteria (SC-ids), no traceability matrix, no owner, no target release. Given
  the project's `/plan-spec` conventions and the sibling
  `channel-instance-workspace-binding-spec.md` (which has both), this spec is structurally thinner
  than its own precedent.
- **O2.** No rollout or rollback story. This is a behaviour change to the product's primary output
  path with no flag, no staged enablement, and no stated way to revert if FR-3 refusals turn out to
  be widespread in the field. The project's no-back-compat posture justifies the *parameter*
  removal; it does not by itself justify shipping the *refusal* behaviour with no observability
  ramp.
- **O3.** No metric for "how often does FR-3 refuse?". That number is the single thing an operator
  needs to know whether their heartbeats went quiet. See M5.
- **O4.** The spec asserts FR-6's `InstanceID` "is currently declared and never set by any non-test
  code" — **verified correct**: the only assignments are in `pkg/bus/instanceid_test.go`.
- **O5.** The ADR's core outbound claims are **verified correct**: `MessageTool.Parameters` exposes
  `channel`/`chat_id` as optionals and `Execute` prefers them over ctx; the loop's callback forwards
  straight to `PublishOutbound` with no check; `OutboundMessage` carries no `AgentID`/`WorkspaceID`;
  `dispatchLoop` keys `m.channels` by instance. The diagnosis is sound.
- **O6.** The ADR's claim that ADR-029 never addressed outbound is **verified**: the only outbound
  mention across ADR-029, its three review rounds and its spec is C-2's aside that "stamping inbound
  `InstanceID` is a distinct workstream from the outbound/activation factory plumbing" — which is
  about factories, not ownership.
- **O7.** FR-5's title, "Cross-workspace egress is impossible through this path", is doing a lot of
  work with "this path". Given M2 (three egress paths) it would read more honestly as
  "impossible through `send_message`".
- **O8.** Consider whether FR-6 and FR-7 belong in this spec at all. FR-1 is the fix; FR-6/FR-7 are
  an audit-and-defence-in-depth workstream that touches 20 call sites, a new audit event, a counter
  and a dispatch refusal path — larger than the fix itself, and the source of C3, M2, M4 and M5.
  Splitting them into a follow-up would let FR-1–FR-5 ship clean.
- **O9.** §6's Open question is well-posed and the reasoning is right. It would be stronger with the
  operator-visible consequence quantified: which of the shipped seeds actually own a channel
  instance on a fresh install? If the answer is "none by default", FR-3 refuses on every fresh
  install's first heartbeat.
- **O10.** The `hand_off` interaction (C1) and the delegation interaction (C2) are the same
  underlying question — "what is the identity of a turn that is transacting on someone else's
  conversation?" — and it is the same question ADR-032/ADR-057 already litigated for sub-turns.
  Reusing that vocabulary (agent-level settings vs. turn-level routing/transport) would resolve both
  cleanly: **ownership is an agent-level property; the turn's channel/chat is turn-level transport,
  legitimately inherited.**

---

## 3. Structural integrity (structured-spec mode)

| Check | Result |
|---|---|
| Every stated goal has acceptance criteria | **PARTIAL** — FR-1/2/3/4/5/8 have Given/When/Then; FR-6, FR-7, FR-9 have none |
| Cross-references consistent | **FAIL** — FR-8 ⊥ FR-9 (C4); FR-2 ⊥ FR-4 (C1) |
| Scope boundaries explicit | **PASS** — §3 Non-goals is clear and well-argued |
| Success criteria measurable | **FAIL** — no SC-ids, no thresholds, no metric |
| Requirements mutually consistent | **FAIL** — see C1, C4 |
| Error/failure scenarios addressed per requirement | **PARTIAL** — FR-3 yes; FR-6/FR-7/FR-9 no |
| Dependencies between requirements identified | **FAIL** — FR-7 depends entirely on FR-6, never stated; FR-9 depends on an ownership pair that does not exist |
| Terms defined | **FAIL** — "channel" vs "instance" vs "the turn's workspace" (M1, N7) |
| Regression impact addressed | **FAIL** — §5 covers one of at least four changes (M6) |

---

## 4. Test coverage assessment

**Testability.** FR-1, FR-2, FR-6 are cleanly testable. FR-3 is testable only after M1 resolves
which workspace signal it means. FR-4 is testable only after C1/C2 resolve what it applies to.
FR-7 is testable only after C3 defines its scope. FR-9 is not testable as written (C4).

**Missing coverage** (beyond M7): negative path for a refusal's *message text* (FR-3/FR-4 both
require the refusal to "name" something — that text is a contract and nothing pins it); idempotency
(does a refused send set `sentInRound`? currently `t.sentInRound.Store(true)` runs only on success,
and the loop suppresses its own reply based on `HasSentInRound()` — so a refusal changes whether the
user gets *any* output); concurrency (config `Reload` racing an in-flight resolution); and the
inbound regression pins the scope explicitly promises.

**Highest-value missing test.** A turn where the acting agent ≠ the instance owner, produced the way
production produces it — via `hand_off` on a bound channel, and via `spawnSubTurn`. Those two tests
would have surfaced C1 and C2 before implementation.

---

## 5. STRIDE summary

| Component | Threat | Status in spec |
|---|---|---|
| `send_message` parameters | **Elevation / Spoofing** — model names another workspace's instance | Addressed by FR-1. Correct and sufficient. |
| `webchat` `chat_id` | **Information disclosure** — frames into another user's browser | Addressed by FR-1 (destination from turn). FR-9's ownership framing is inoperable (C4). |
| Delegated / handed-off turn identity | **Elevation** — agent transacts on an instance it does not own | **Not modelled.** C1, C2. |
| Dispatch (`FR-7`) | **Spoofing** — origin fields settable by any Go caller | Fields are Go-set, not model-set, so unforgeable by a model; but "empty = exempt" defeats the check (C3). |
| Outbound audit | **Repudiation** — no record of who sent what through which instance | Claimed by FR-6, delivered by nothing (M5). |
| `get_config "channels"` | **Information disclosure** — instance keys, `workspace_id`, `identity.id` readable by operator-created agents | Not in scope, but it is the oracle the ADR's severity assessment says does not exist (M8). |
| FR-3 refusal | **DoS (self-inflicted)** — misconfigured or two-channel agent goes silently mute | Partially raised in §6 (zero case only); multi-instance case has no remedy (M3). |
| `notifyDrop` under FR-7 | **DoS (self-inflicted)** — backpressure becomes silent | Not considered (C3). |

---

## 6. Unasked questions

1. When a handed-off agent replies on a bound instance it does not own — does the message go out?
2. When a delegated sub-agent calls `send_message` inside the parent's conversation — whose
   ownership is checked, the delegate's or the root parent's?
3. Which workspace is "the turn's workspace" — `ToolWorkspaceID(ctx)` or `workspace.FindForAgent`?
   What happens when it is empty?
4. What happens to the 19 non-agent producers of `OutboundMessage` under FR-7?
5. Does FR-7 apply to the streaming path, which never constructs an `OutboundMessage` at all?
6. Does FR-7 apply to `Manager.SendMessage` / `SendToChannel`, which never reach `dispatchLoop`?
7. What does the operator do when their agent legitimately owns two instances in one workspace and
   its heartbeats now refuse?
8. Does a refused send count as "sent in round"? Does the user get the agent's text reply instead,
   or nothing?
9. Is `send_message` an inter-agent tool? `pkg/agent/loop.go`'s own registration comment and the
   UAT plan say yes; the tool's description says "Send a message to user".
10. What is the audit event name, and is it added to the `pkg/audit` allowlist in the same change?
11. Must `OutboundMessage.InstanceID` equal `OutboundMessage.Channel`? What does dispatch do if not?
12. Does `resolveAgentDefaultChannel` (schedules) get reconciled with FR-3, or do the two resolvers
    stay free to disagree?
13. What happens to an in-flight send when the instance is unbound, deleted, or the agent is removed
    from the CoreTeam mid-turn?
14. On a fresh install, does any seeded agent own a channel instance? If not, FR-3 refuses on the
    first heartbeat of every new installation.

---

**Verdict: BLOCK.**

Review written to:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/wt-macos-seatbelt/docs/internal/specs/channel-agent-ownership-spec-review.md`

Address the findings above, then re-run round 2:
`/grill-spec docs/internal/specs/channel-agent-ownership-spec.md`
