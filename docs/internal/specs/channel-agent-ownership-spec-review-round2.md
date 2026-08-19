# Spec review — channel-agent-ownership-spec.md (Round 2 of 2, revision 1)

- **Spec under review:** `docs/internal/specs/channel-agent-ownership-spec.md` — Draft, revision 1 (2026-08-14)
- **Round-1 review:** `docs/internal/specs/channel-agent-ownership-spec-review.md` (BLOCK; 4 CRITICAL, 8 MAJOR, 9 MINOR, 10 OBSERVATION)
- **ADR:** `docs/internal/architecture/ADR-065-channel-ownership-per-agent-workspace.md` (§2 and §3 amended this round)
- **Worktree / branch:** `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/wt-macos-seatbelt` — `feat/remove-main-sentinel-agent`
- **Mode:** structured-spec (FR ids + Given/When/Then + a levelled test table; still no SC ids, no traceability matrix)
- **Evidence:** every claim below was read from code at commit `b3e802e7` (the pushed tip of this branch; the one local commit on top of it, `2ef6c1c2`, is the docs-only spec revision). Symbols are cited as `file::symbol`.
- **Environment caveat:** partway through this review the worktree became unreadable to this process (`EPERM` on every read under `/Users/danielpiatkowski/Documents/`, while writes to the same paths still succeed — consistent with a Seatbelt profile applied by other work on this branch). Code verification was completed against an extracted copy of `b3e802e7`; the spec and ADR text were read from the worktree before the lockout.

---

## 1. Executive summary

**Twenty findings: 3 CRITICAL, 6 MAJOR, 8 MINOR, 3 OBSERVATION. Verdict: BLOCK.**

The revision is a real improvement, not a re-wording. The "ownership governs destination
selection, never the turn's own conversation" rule is the right cut, and four of the eight
round-1 MAJORs plus all four CRITICALs are genuinely fixed at the layer they were raised
(details and the two that only *look* fixed are in §2).

But the new rule has a load-bearing premise that the code contradicts, and one requirement that
quietly ignores the rule:

1. **FR-7 does not know about FR-2.** Dispatch refuses on `(AgentID, WorkspaceID) != owner`
   with no provenance exemption, so once FR-6 stamps `AgentID` on agent-originated sends, the
   handed-off and delegated replies FR-2 exists to protect are refused one layer down. Rows 2
   and 3 of the test plan and row 14 describe the same message and demand opposite outcomes.
2. **FR-2's justification is false for at least four turn types.** "The conversation was
   established by the inbound path, which ADR-029 and the hand-off pin have already
   adjudicated" is true only of genuinely inbound turns. A `deliver=false` schedule takes its
   channel and chat straight from the job payload with no ownership or binding validation
   anywhere; task, plan and async-notify wakes replay a destination persisted by an earlier
   turn. None of those went past `ResolveRoute` or the hand-off pin. FR-5's "cross-workspace
   egress is impossible through this path" is falsified by an existing supported path.
3. **The ownership record itself is agent-writable at the shipped default policy.** `set_config`
   is `allow` in the global ceiling and `channels.` is a permitted, non-blocked config prefix,
   so an operator-created agent can rewrite the very `(workspace, agent)` pair FR-3 and FR-4
   resolve against.

FR-3b and FR-3a, the two new discretionary choices, each carry a consequence the spec has not
noticed: FR-3b turns delegation into an ownership-crossing egress primitive while recording only
the delegate in the audit trail, and FR-3a's lowest-key rule silently re-points an agent's entire
proactive output the moment a lower-sorting instance is added.

---

## 2. Verification of round-1 findings

Checked against code, not against the spec's claims.

| Round-1 | Verdict | Evidence |
|---|---|---|
| **C1** hand-off contradiction | **RESOLVED** | FR-2 names `hand_off`, cites the correct mechanism (`pkg/agent/loop.go::onHandoffFrontend`'s `"chat:"+channel+":"+chatID` pin, consulted before `routing.ResolveRoute`), and row 2 pins it. The ADR §2 amendment describes the bypass accurately. |
| **C2** delegated sub-agent mute | **RESOLVED at the tool layer** | FR-2a matches `pkg/agent/subturn.go::spawnSubTurn` exactly: `Channel`, `ChatID`, `WorkspaceID` from `parentTS.opts`, agent identity from the delegate. Row 3 pins it. *But see NEW-1 — FR-7 re-refuses this message at dispatch, and NEW-6 — FR-3b's consequence is unexamined.* |
| **C3** FR-7 scope | **PARTIALLY** | The scope statement ("non-empty `AgentID`") and the enumerated exemption are what round 1 asked for, and row 14 is now a test that can fail. But the exemption's *justification* — "these are system-originated … carry no `AgentID`" — is circular and factually wrong for several members (NEW-4), and the choke point named does not exist under that name and is shared with a message type that cannot carry the fields (M-8 below). |
| **C4** webchat | **RESOLVED — cleanest fix in the revision** | Verified: `webchat` is absent from `pkg/config/config.go::knownChannelTypes`; it is registered synthetically at `pkg/gateway/gateway.go` (`RegisterChannel("webchat", wch)`); it therefore has no `ChannelInstanceConfig` and no ownership pair. FR-9's replacement text is accurate. Row 12 tests the real property. |
| **M1** which workspace signal | **RESOLVED** | FR-3 names `tools.ToolWorkspaceID(ctx)` and adds the empty-`W` branch (refuse). Matches `pkg/tools/email.go::EmailTransports.resolve`, which is the precedent cited. One inaccuracy remains (N-10). |
| **M2** enforcement choke point | **NOT RESOLVED** | Streaming is now acknowledged (FR-7a — correct and well-stated). The other two paths are not: `pkg/channels/manager.go::Manager.SendMessage` still enqueues directly (live caller: voice-echo in `pkg/agent/loop.go`), and the named symbol `channels.Manager.dispatchLoop` does not exist — it is the package-level generic `pkg/channels/manager.go::dispatchLoop[M any]`, shared by text and media. See M-8. |
| **M3** multi-instance ambiguity | **RESOLVED** (decision made; see M-6 for its consequences) | Confirmed `pkg/config/config.go::ValidateChannels` still enforces no uniqueness on `(WorkspaceID, Identity.ID)`, so the ambiguity is permanent, as round 1 said. FR-3a decides it and §8 Q2 escalates it. |
| **M4** `InstanceID` duplication | **RESOLVED** | FR-6 drops it and states the invariant reason correctly (`Channel` *is* the instance key). |
| **M5** audit event + counter | **PARTIALLY** | FR-6a now requires both. It names neither, and does not mention that `pkg/audit` event names are an allowlist (`pkg/audit/events.go`, pinned by `pkg/audit/hardening_test.go`) — so this is not the enumerable change round 1 asked for. See N-3. |
| **M6** regression impact | **PARTIALLY** | §5 enumerates four items, including the schedule-payload one. Two round-1 items are still absent: the in-tree description of `send_message` as the inter-agent tool, and `pkg/gateway/schedules.go::resolveAgentDefaultChannel` — a third, still-unreconciled notion of "the agent's channel" (it scans `cfg.Bindings`). See M-9. |
| **M7** test plan | **PARTIALLY** | Levels added, 16 rows, and row 14 is now a test that can fail (correctly noted in the spec). Still missing: the inbound regression pins §2's scope explicitly promises (there is no ADR-029 Priority-0 / drift-drop row), boundary rows (instance deleted/disabled/reloaded mid-turn, `Identity.Kind` non-agent), any concurrency row, and any pin on the refusal text that FR-3/FR-4 make a contract. See M-10. |
| **M8** ADR §3 exploitability | **RESOLVED** | ADR §3 now distinguishes base agents from operator-created ones and states the `get_config "channels"` disclosure. Verified: `pkg/config/defaults.go` has `"get_config": "allow"` in the ceiling; `pkg/coreagent/core.go`'s `tightenGlobalCeiling` denies it for the base roster. *The write half is still missing — see C-3.* |

Round-1 MINORs still open: N1 (silent ignore, → N-5), N3 (`send_file` asymmetry, → N-6), N6 (media message, → M-8), N8 (FR-8/FR-3 precedence, → N-4), N9 (internal-channel ordering, → N-7). N2, N4, N5, N7 are addressed or moot.

---

## 3. New findings

### CRITICAL

#### C-1 — FR-7 has no FR-2 exemption, so it refuses at dispatch exactly what FR-2 permits at the tool

**Sections:** FR-6, FR-7, FR-6a, rows 2, 3, 14.

FR-6 requires `AgentID` and `WorkspaceID` "populated for every **agent-originated** send". FR-7
then refuses, at dispatch, any message whose `(AgentID, WorkspaceID)` does not own the resolved
instance. Dispatch has no way to know whether the destination came from FR-2 (the turn) or FR-3
(resolution) — and FR-7 states no exemption for the former.

Walk row 2 through it. `tg.acme` is owned by `(W1, Mia)`; `hand_off` pins Ava; Ava replies. The
message is agent-originated, so under FR-6 it carries `AgentID: "ava"`, `WorkspaceID: "W1"`. The
instance is owned by `(W1, mia)`. That is a mismatch. FR-7: *"the message is refused, with a WARN"*.
Row 2 says: *"delivered; ownership not consulted"*. Row 3 (delegation) has the identical shape.
Row 14 — "**populated but mismatched** `(AgentID, WorkspaceID)` vs instance → refused" — **is
row 2's message**. The spec's own test plan asserts both outcomes for one message.

This is round-1 C1/C2 reappearing one layer down. The spec knows provenance exists — FR-6a
requires the audit event to record "whether the destination came from the turn (FR-2) or from
resolution (FR-3)" — but FR-7's rule does not consult it.

Note also that "agent-originated" is doing undefined work. The agent's own final reply
(`pkg/agent/loop.go`, the `opts.SendResponse && result.finalContent != ""` publish) and
`handleReasoning`'s output are plainly agent-originated in ordinary English. If an implementer
populates `AgentID` on them — which FR-6 as written instructs — then every handed-off and every
delegated reply is refused at dispatch, product-wide, on the highest-traffic path.

**Fix.** Either (a) carry the provenance on the message (a `DestinationFromTurn bool`, which FR-6a
already needs for the audit event) and make FR-7 check ownership **only** when provenance is
FR-3; or (b) state plainly that FR-6 populates `AgentID` on **only** the `send_message` tool path
and that dispatch refuses only proactive-resolved sends — and then say what the fields are on the
final reply. Whichever is chosen, add a row asserting the FR-2 message with a mismatched pair is
**delivered**, and reconcile it with row 14 so the two cannot both be satisfied by the same test.

#### C-2 — A `deliver=false` schedule sets the turn's channel and chat from the job payload; FR-2 blesses it unchecked, and FR-5's impossibility claim is false

**Sections:** §1 (the justification sentence), FR-2, FR-5, §5.

FR-2's exemption rests on one sentence: *"The conversation was established by the inbound path,
which ADR-029 and the hand-off pin have already adjudicated."* That is true of inbound turns. It
is not true of scheduled turns, and the code path is short and unambiguous:

- `pkg/gateway/schedules.go::scheduledRunner.run` reads `channel := job.Payload.Channel` and
  `chatID := job.Payload.To` and passes them to `ProcessScheduled`.
- `pkg/agent/loop.go::AgentLoop.ProcessScheduled` puts them straight into `processOptions.Channel`
  / `.ChatID`, which become `ToolChannel(ctx)` / `ToolChatID(ctx)`.
- The create handler (`pkg/gateway/schedules.go`, the `AddJobFull(cron.JobSpec{… Channel:
  derefStr(req.Channel), To: derefStr(req.ChatId), AgentID: req.OwnerAgentId …})` call) validates
  the trigger, the timeout, the session mode and that the owner is not a worker. **It does not
  validate the channel at all** — not against ownership, not against `cfg.Bindings`, not even for
  existence.
- `pkg/gateway/schedules.go::resolveScheduleWorkspaceID` then stamps the session with **the named
  instance's** `WorkspaceID`, so the turn's `ToolWorkspaceID` becomes `W2` even though the owning
  agent may belong to `W1` only.

So: a schedule owned by `agentA` with `payload.channel = telegram.beta` (owned by `(W2, agentB)`)
produces a turn whose context points at `W2`'s instance. FR-2: ownership is not consulted. The
agent's own generated content leaves through `W2`'s bot token. Nothing in FR-1–FR-9 covers it,
and FR-5's heading — "Cross-workspace egress is impossible through this path" — is false as
stated, as is its body ("the message can only leave through the turn's own conversation (FR-2)
or `A`'s own instance in `W1` (FR-3)"): the turn's own conversation **can be** another
workspace's instance.

The same shape recurs on three more turn types, all with destinations persisted by an earlier
turn and replayed later: `pkg/agent/task_executor.go::notifySourceChannel` and
`::wakeOwnerAttemptsExhausted` (`t.SourceChannel`/`t.SourceChatID`), the plan wake in
`pkg/agent/plan_engine.go` (`p.SourceChannel`/`p.SourceChatID`), and the delegate-completion
reconstruction in `pkg/agent/loop.go` (origin parsed from the notify message). To the revision's
credit, the *write* side of those is safe — `pkg/tools/task.go` and `pkg/tools/plan.go` both stamp
the origin from `ToolChannel(ctx)` and never from args, and `plan.go`'s comment states the exact
threat ("a caller-supplied origin would let one principal redirect another's plan outcome"). The
gap is that the spec's two-category model (FR-2 turn context / FR-3 resolution) has no third
category for *a destination captured in one turn and replayed by a different principal later*,
and `create_task` accepts an `agent_id` for another agent (trust-set gated), so the replaying
principal is routinely not the capturing one.

**Fix.** Three things. (1) Replace §1's justification sentence with the truth: the turn's
destination is inherited transport, and it is adjudicated by the inbound path *only for inbound
turns*. (2) Add an FR for the schedule payload — the honest options are to validate
`payload.channel`/`chat_id` against the owner's ownership at schedule-create time (mirrors the
FR-029 half-bound guard's fail-at-write posture and is the cheapest), or to state explicitly that
an operator-supplied schedule destination outranks ownership and why. (3) Restate FR-5 as
"impossible through `send_message`" (round-1 O7 said the same) or list the paths through which it
remains possible.

#### C-3 — The ownership record is writable by an operator-created agent at the shipped default policy, so FR-4 is a boundary the governed party can move

**Sections:** FR-3, FR-4, ADR §3.

FR-3/FR-4 resolve against `config.Channels[key].WorkspaceID` + `.Identity`. That subtree is
writable through `set_config`:

- `pkg/config/defaults.go` global ceiling: `"set_config": "allow"` (also `"configure_channel":
  "allow"`).
- `pkg/coreagent/core.go::tightenGlobalCeiling` denies `set_config` and `get_config` for the base
  roster — so, exactly as ADR §3 now says of the read oracle, this bites the **operator-created**
  agent, which is the case the ADR itself identifies as the risk.
- `pkg/sysagent/tools/config.go::knownConfigPrefixes` includes `"channels."`, and the
  `blockedConfigKey` table blocks `agents.list`, `sandbox`, `security`, `tools.exec`,
  `tools.mcp`, `gateway.*` and more — **but nothing under `channels.`**.
- `pkg/sysagent/tools/config.go::dotSet` → `setDot` writes through the JSON map and even creates
  intermediate objects, so `channels.telegram.identity.id` and `channels.telegram.workspace_id`
  are writable. (Dotted instance keys such as `telegram.acme` are not addressable by this path
  because the key splits on `.`; bare keys — the single-instance default — are.)

`configure_channel` is not a vector (`pkg/sysagent/tools/channel.go::ChannelConfigureTool.Parameters`
exposes only id/token/phone_number/bot_id/app_id/app_secret/mode), so `set_config` is the whole
of it — which also means the fix is one row in an existing data table.

An enforcement boundary whose authority record is writable by the party being governed is not an
enforcement boundary; it is a speed bump. The ADR's §3, corrected this round for the read oracle,
still does not mention the write oracle, and the spec's §1 claim "No new storage, no new UI, no
schema change" quietly depends on that storage being trustworthy.

**Fix.** Add an FR: `channels.` (at minimum `identity` and `workspace_id` under it) joins the
`blockedConfigKey` table with a `Reason` naming ADR-065, and a test asserting a `set_config`
attempt on `channels.<id>.identity.id` is refused. Amend ADR §3 to state the write half alongside
the read half. Add a test-plan row.

---

### MAJOR

#### M-4 — FR-7's exemption list is justified by a property that is merely the current state of an unset field

**Section:** FR-7 ("Exempt, by enumeration"), FR-6.

FR-7 exempts the ~19 non-agent producers on the grounds that they "are system-originated, are not
model-addressable, and carry no `AgentID`". The third clause is true only because `AgentID` does
not exist on `bus.OutboundMessage` yet — it is a fact about today's struct, not a property of
those producers. The first clause is wrong for several of them: `notifySourceChannel` publishes
an agent's task result, the plan wake publishes an agent's synthesis, `handleReasoning` publishes
an agent's reasoning, and the loop's `SendResponse` branch publishes the agent's own reply. Those
are agent-originated by any reading except "the field happens to be empty".

This matters because FR-7's scope is defined by that field, so the exemption is self-fulfilling:
a future producer is exempt precisely by forgetting to populate it — the failure mode round-1 C3
named, now written into the spec as the rule rather than fixed. This is the "mechanism not
property" shape this project has been bitten by repeatedly.

**Fix.** Define the exempt set by *what the producer is*, not by what the field holds: enumerate
the producers by symbol (they are a closed set of ~19 and the spec has already counted them) and
require the check to be keyed on that enumeration, so a new producer is a compile-or-review-time
decision rather than a silent exemption. Or make FR-7 explicitly a *provenance* check per C-1,
where "no provenance stamp" is itself a refusal.

#### M-5 — FR-3 grants proactive egress to turn types that today cannot send at all; the spec presents the change as purely restrictive

**Sections:** FR-3, §5, §8 Q1.

Today, a turn with no channel cannot send unless the model names a destination:
`pkg/tools/message.go::MessageTool.Execute` returns `"No target channel/chat specified"` when
both the args and `ToolChannel(ctx)`/`ToolChatID(ctx)` are empty. FR-1 removes the args, and FR-3
replaces that refusal with an automatic destination.

The turn types that gain a voice they do not have today:

- **`/loop` runs.** `pkg/agent/loop_scheduler.go::LoopScheduler.RunScheduled` calls
  `ProcessScheduled(ctx, job.AgentID, sessionID, prompt, "", "")` — no channel, no chat — and the
  workspace comes from the session's meta, so `W` is typically non-empty. Under FR-3 every
  self-paced loop iteration can now push a message into the agent's owned channel.
- **Heartbeats** (`pkg/gateway/heartbeat_schedule.go`, jobs named `heartbeat:<ws>:<agent>` with no
  payload channel) — same.
- **Delegated sub-turns with no inherited conversation** — FR-3b makes this explicit.

§5 lists four regressions, all of them losses. The gain is not listed anywhere, and it is the
larger behavioural change: it is the difference between "an agent can reply" and "an agent can
initiate". There is no rate limit, no operator switch, and no metric for it (FR-6a counts
refusals, not proactive sends).

Related, and sharper than §8 Q1 admits: on a **fresh install no channel instance is configured at
all**, and `webchat` structurally cannot satisfy FR-3 (FR-9 — no `ChannelInstanceConfig`). So the
default state of a new installation is that every heartbeat's proactive send refuses. Q1 frames
this as "a misconfigured heartbeat"; it is the out-of-the-box configuration.

**Fix.** State the widening in §5 as its own row. Decide whether an operator can disable proactive
resolution (a config key, not a code branch, per Constraint #6). Re-frame §8 Q1 around the
fresh-install case and say what a webchat-only install's heartbeats do.

#### M-6 — FR-3a's lowest-key rule silently re-points an agent's entire proactive output, and its only signal is a WARN that fires on every send

**Sections:** FR-3a, §8 Q2.

Instance keys are `<type>.<slug>` (`pkg/config/config.go::ValidateChannels`, `ParseInstanceKey`),
so "lowest key" orders by **platform name**: `discord.*` < `google-chat.*` < `irc.*` < `line.*` <
`matrix.*` < `slack.*` < `telegram.*` < `whatsapp.*`. Consequences the spec does not state:

1. **Adding a channel re-points the old one's traffic.** An agent whose heartbeats have gone to
   `telegram.acme` for months moves silently to `discord.acme` the moment the operator connects
   Discord. Nothing fails; the old recipients simply stop hearing from the agent. The only signal
   is a WARN that was already firing.
2. **The WARN fires on every proactive send**, i.e. every heartbeat tick per agent. A warning that
   fires on the steady state is a warning operators filter out — the opposite of "recoverable
   because it is loud".
3. **The ambiguity is not recorded where FR-6a looks.** FR-6a's audit event carries the chosen
   instance but is not required to record that the choice was ambiguous, so the audit trail cannot
   distinguish "the agent's channel" from "one of the agent's channels, picked alphabetically".

§8 Q2 also presents a false trichotomy (lowest-key / `primary` flag / refuse). There is a fourth
option that needs no schema change and is the one the spec's own model implies: make the
ambiguity **unrepresentable** by adding a uniqueness rule for `(WorkspaceID, Identity.ID)` to
`ValidateChannels`. That is precisely what makes the email precedent deterministic —
`pkg/tools/email.go`'s `EmailTransports` is `map[workspaceID]Transport` for one agent, so "two
mailboxes in one workspace" cannot be expressed. The spec says it is following the email model
while adopting the tie-break the email model never needs. The cost is real and should be stated
with the option: it would reject the existing, legitimate "one team reachable on Slack and
Telegram" configuration at load, so it needs a migration story.

**Fix.** Add the uniqueness option to Q2 with its migration cost. If lowest-key survives, require
the audit event to record `ambiguous: true` and the full candidate list, and require the WARN to
be emitted once per resolution *change* rather than per send.

#### M-7 — FR-3b makes delegation an ownership-crossing egress primitive, and half-fixes the attribution it was chosen for

**Sections:** FR-3b, FR-4, §8 Q3.

FR-3b resolves a delegated proactive send against the delegate's own ownership. Combined with
`spawnSubTurn`'s inheritance (`WorkspaceID` from the parent — verified at
`pkg/agent/subturn.go::spawnSubTurn`), this means:

Mia's turn delegates to Ava with the instruction "send this text". Ava has no inherited
conversation, resolves FR-3 against `(W1, ava)`, and the text leaves through **Ava's** instance.
FR-4 says an agent cannot make an instance it does not own the destination of a proactive send;
delegation achieves exactly that effect one hop away, and FR-3b is the requirement that makes it
work. `delegate` is in the always-visible manifest (`pkg/tools/manifest.go::fullManifestToolNames`),
so this is the ordinary path, not an exotic one. The workspace delegation trust-set gates *who may
delegate*, not *whose channel may be used* — worth naming, because it is the only thing standing
in the way and it was designed for a different question.

The attribution argument for FR-3b is also only half-delivered. FR-6 carries `AgentID` and
`WorkspaceID` and nothing else, so the audit event says "Ava sent through Ava's channel" and
contains no link to the parent turn that caused it. The audit trail does not lie, but it cannot
answer "who caused this", which is the question an incident actually asks.

**Fix.** State the laundering property explicitly in §8 Q3 so the operator decides it knowingly.
Require FR-6/FR-6a to carry the delegation root (the parent agent id, or the `routingSessionID`
which is inherited verbatim through the whole delegation subtree per ADR-057 — see
`pkg/agent/turn.go::turnState.routingSessionID`) so a delegated send is traceable to its origin.

#### M-8 — FR-7 names a choke point that does not exist under that name, is shared with a message type that cannot carry the fields, and media egress is left wholly uncovered

**Sections:** FR-7, FR-6, §6.

- The symbol is `pkg/channels/manager.go::dispatchLoop[M any]` — a package-level **generic
  function**, not `channels.Manager.dispatchLoop`. It is invoked twice: from
  `Manager.dispatchOutbound` (`bus.OutboundMessage`) and from `Manager.dispatchOutboundMedia`
  (`bus.OutboundMediaMessage`).
- `OutboundMediaMessage` carries `WorkspaceID` but no `AgentID` (`pkg/bus/types.go`), and FR-6
  does not add one. So a check "at `dispatchLoop`" is not writable as one check: it must live in
  the per-type `enqueue` closures, or the generic must be widened.
- Consequently **all media egress is outside FR-6 and FR-7** — `send_file`, tool-produced media,
  the media-fallback notice — and the spec never says so. FR-7a carefully scopes streaming out;
  media gets no equivalent sentence, so a reader reasonably concludes it is covered.
- Round-1 M2's other bypass is also still unmentioned: `Manager.SendMessage` enqueues directly to
  `w.queue` / `sendWithRetry` without touching the bus or `dispatchLoop` (live caller: the
  voice-transcription echo in `pkg/agent/loop.go`). Not model-addressable, so "out of scope" is a
  fine answer — but it has to be written down, because an implementer reading FR-7 will believe
  dispatch is the single choke point.

**Fix.** Name `pkg/channels/manager.go::dispatchLoop` correctly and say which of the two
invocations the check lands in. Add one sentence scoping media out (with FR-7a's honesty) or add
`AgentID` to `OutboundMediaMessage`. Add one sentence scoping `Manager.SendMessage` out by name.

#### M-9 — The second destination resolver is still unreconciled and still disagrees

**Sections:** §5, §6.

`pkg/gateway/schedules.go::resolveAgentDefaultChannel` resolves "the agent's channel" by scanning
`cfg.Bindings` for `b.AgentID == agentID` — a different association from
`ChannelInstanceConfig.Identity` — and is used to deliver schedule-failure alerts. After this spec
ships, a proactive `send_message` (FR-3, ownership) and a schedule-failure alert for the same
agent can resolve to two different channels, with no rule saying which is right. Round 1 raised
this (M6.3) and asked for either reconciliation or an explicit by-name out-of-scope declaration;
the revision does neither — the symbol appears nowhere in the spec.

**Fix.** One line in §6 naming it out of scope, or an FR making it defer to FR-3's resolution.
Either is acceptable; silence is not, because the two resolvers are now visibly in competition.

#### M-10 — The test plan still does not pin what §2's scope promises, and omits every boundary and concurrency case

**Section:** §4.

The scope sentence says inbound "is only regression-pinned here". There is still **no** row for
the ADR-029 Priority-0 bound-instance route or its drift drop (`MatchedBy: "bound.drift.drop"`,
`pkg/agent/loop.go`'s `route.Drop` branch with `al.driftDropped.Add(1)`) — the behaviour most
likely to be disturbed by a change to the same config fields. Rows 1, 2, 11 and 12 exercise
outbound behaviour on inbound-established turns, which is not the same thing.

Also still missing (all raised in round-1 M7 and none added):

- Instance deleted, disabled, or `Reload`ed mid-turn (`Manager.Reload` rebuilds `m.channels` under
  `m.mu` while workers drain queues resolved against the old map).
- `Identity.Kind` present but not `agent` (`IsWorkspaceBound()` is false → the instance is not
  ownable; FR-3's candidate set must say so).
- The refusal **text**: FR-3 and FR-4 both require the refusal to "name" something. That text is
  the entire user-visible contract of this change and nothing pins it.
- Whether a refused send counts as sent-in-round. `MessageTool.Execute` sets
  `t.sentInRound.Store(true)` only on success, and the loop suppresses its own reply based on
  `HasSentInRound()` — so a refusal changes whether the user gets any output at all.
- A row for the FR-2 message with a mismatched `(AgentID, WorkspaceID)` being **delivered** (C-1).

---

### MINOR

- **N-1 (FR-3, config read at send time).** FR-3 resolves against config with no statement about
  reload semantics, a disabled-but-present instance, or an instance deleted between resolution and
  dispatch. The FR-3 refusal and the dispatch-time "unknown channel" WARN are different failures
  with different messages; say which the agent sees.
- **N-2 (FR-3, candidate-set definition).** "the instance owned by exactly `(W, A)`" does not say
  whether the candidate must satisfy `IsWorkspaceBound()`, whether `Enabled: false` instances are
  candidates, or whether the comparison is case/whitespace-normalised. All three are decided
  elsewhere in the codebase (`config.canonicalizeKind`, `IsWorkspaceBound`); reuse them by name.
- **N-3 (FR-6a, unnamed event and counter).** Round 1 asked for the name because `pkg/audit` event
  names are an allowlist (`pkg/audit/events.go`; `pkg/audit/hardening_test.go` pins validity) — a
  new one is a required, enumerable change, not something an implementer adds ad hoc. The
  precedent to mirror is `audit.EventChannelRoutingDriftDrop` plus the `driftDropped` counter.
- **N-4 (FR-8 vs FR-3 precedence — round-1 N8, still open).** FR-8 promises "behaviour is
  unchanged" for an unbound instance. For a proactive send it is not: an agent whose only channel
  is unbound owns no `(W, A)` instance and is refused under FR-3, where today it could send by
  naming the channel. FR-8 and FR-3 need an explicit precedence sentence.
- **N-5 (FR-1 silent ignore — round-1 N1, still open).** "any supplied value is ignored" was the
  behaviour round 1 asked the spec to *choose deliberately*; the revision chose it without
  argument. Given this project's record with silently-discarded inputs, an error naming the
  removal is the safer default. Either way, argue it.
- **N-6 (`send_file` asymmetry — round-1 N3, still open).** §6 still calls `send_file` "the
  reference implementation here", but `pkg/tools/send_file.go::SendFileTool.Execute` has no FR-3
  equivalent — it returns "no target channel/chat available". After this spec an agent on a
  heartbeat can send a message but not a file. State whether that is intended.
- **N-7 (internal-channel ordering — round-1 N9, still open).** `dispatchLoop` `continue`s on
  `constants.IsInternalChannel(channel)` **before** any per-message work. FR-7's check must be
  placed after it; say so, or the check will be written above a `continue` that already ate the
  message.
- **N-8 (structure).** Still no SC ids, no traceability matrix, no owner, no target release, no
  rollout/rollback note — thinner than the sibling `channel-instance-workspace-binding-spec.md`
  (round-1 O1/O2, unchanged).

---

### OBSERVATION

- **O-1.** FR-7a is the best-written requirement in the revision: it states a limitation, says why
  it is accepted, and pre-empts the misreading. FR-9 and FR-6's `InstanceID` rationale are the same
  quality. The revision's weakest sections are the ones that assert a property (FR-7's exemption,
  FR-5's "impossible", §1's "already adjudicated") instead of naming the mechanism that guarantees
  it.
- **O-2.** Round-1 O8 still stands and is now better evidenced: FR-1 is the fix; FR-6/FR-6a/FR-7
  are an audit-and-defence workstream that has produced C-1, M-4 and M-8 in this round after
  producing C3, M2, M4 and M5 in the last. FR-1–FR-5 could ship clean on their own.
- **O-3.** The `(workspace, agent)` pair is now load-bearing for inbound routing, outbound
  destination selection, memory-room routing, filesystem re-rooting and heartbeat identity — while
  three different structures still express "which channel is this agent's" (`Identity`,
  `cfg.Bindings`, and the schedule payload). The consolidation question is bigger than this spec
  and probably belongs in the v0.3 workspaces work, but it is worth recording here as the reason
  M-9 keeps recurring.

---

## 4. Structural integrity (structured-spec mode)

| Check | Result |
|---|---|
| Every stated goal has acceptance criteria | **PASS** — every FR now has a Given/When/Then or an explicit statement of scope |
| Cross-references consistent | **FAIL** — FR-2/FR-2a vs FR-7 (C-1); rows 2/3 vs row 14 |
| Scope boundaries explicit | **PARTIAL** — §6 is strong; media (M-8) and `Manager.SendMessage` are neither in nor out |
| Success criteria measurable | **FAIL** — still no SC ids, no thresholds, no metric target |
| Requirements mutually consistent | **FAIL** — C-1 |
| Error/failure scenarios addressed per requirement | **PARTIAL** — FR-3 excellent; FR-6a/FR-7 do not say what a refusal does to the turn |
| Dependencies between requirements identified | **PARTIAL** — FR-7's dependence on FR-6 is now visible; FR-3's dependence on config trustworthiness is not (C-3) |
| Terms defined | **PASS** — "instance" vs "channel" is now used consistently; FR-6 states the key invariant |
| Regression impact addressed | **PARTIAL** — four of six known changes enumerated (M-5, M-9 missing) |
| Claims match code | **FAIL** — `channels.Manager.dispatchLoop` (M-8); "the only [workspace signal] threaded through every turn type" (it is set only when `ts.opts.WorkspaceID != ""`, and diverges from the identity-keyed `workspace.FindForAgent` that governs the same turn's work dir) |

---

## 5. Test coverage assessment

FR-1, FR-2, FR-2a, FR-6, FR-9 are cleanly testable and now have rows that can fail. FR-3 and
FR-3a are testable. FR-7 is **not** testable until C-1 is resolved — rows 2/3 and row 14 cannot
both pass. FR-6a is not testable until the event and counter are named (N-3).

Highest-value tests still missing, in order: (1) a handed-off agent's reply with a populated,
mismatched `(AgentID, WorkspaceID)` reaching dispatch — the test that decides C-1; (2) a
`deliver=false` schedule whose payload names an instance the owner does not own (C-2); (3) a
`set_config` write to `channels.<id>.identity.id` (C-3); (4) the ADR-029 inbound drift-drop pin
the scope promises; (5) adding a lower-sorting instance and asserting what happens to the agent's
proactive destination (M-6).

---

## 6. STRIDE summary

| Component | Threat | Status in revision 1 |
|---|---|---|
| `send_message` parameters | Elevation / Spoofing | **Addressed** by FR-1. Correct and sufficient for the model-supplied-destination class. |
| `webchat` `chat_id` | Information disclosure | **Addressed** by FR-1; FR-9 now describes the real mechanism. |
| Handed-off / delegated turn identity | Elevation | **Addressed at the tool layer** (FR-2/FR-2a); **re-opened at dispatch** (C-1). |
| Schedule payload destination | Elevation / cross-workspace egress | **Not modelled** (C-2). Unvalidated at write, unchecked at run. |
| Config write path (`set_config` → `channels.*`) | Elevation — the governed party edits the ownership record | **Not modelled** (C-3). |
| Delegation as egress laundering | Elevation | **Not modelled** (M-7); FR-3b is the enabling requirement. |
| Persisted/replayed destinations (task, plan, async-notify) | Elevation / Repudiation | **Not modelled** (C-2, second half); exempt from FR-7 by an empty field (M-4). |
| Outbound audit | Repudiation | **Partially delivered** — FR-6a exists but names no event, no counter, and no delegation root (M-7, N-3). |
| Media egress | Elevation / Repudiation | **Uncovered and unstated** (M-8). |
| FR-3 refusal | Self-inflicted DoS | **Partially addressed** — Q1/Q2 raise it; the fresh-install case is understated (M-5). |
| FR-3 resolution | New unsolicited-egress capability | **Not modelled** (M-5). |

---

## 7. Unasked questions

1. When a handed-off or delegated agent's reply reaches dispatch carrying a mismatched
   `(AgentID, WorkspaceID)` — does it go out? (FR-2 and FR-7 answer differently.)
2. Is the agent's own final reply (`SendResponse`) an "agent-originated send" under FR-6?
3. What validates a schedule's `payload.channel` against the owner's ownership — and if nothing
   does, is an operator-supplied destination meant to outrank ownership?
4. Should `channels.*` be writable by `set_config` at all, now that it is an enforcement record?
5. If Mia delegates to Ava and Ava's proactive send leaves through Ava's channel — is that
   delegation working as intended, or FR-4 defeated by one hop?
6. What tells an operator that their agent's proactive destination changed because a
   lower-sorting instance was added?
7. Does a fresh install — no channel instances, webchat only — have any working proactive send at
   all? If not, is that acceptable for the first heartbeat of every new installation?
8. Does FR-6/FR-7 cover `OutboundMediaMessage`? If not, what stops a file leaving through an
   instance the agent does not own?
9. Which resolver wins when FR-3 and `resolveAgentDefaultChannel` disagree about "the agent's
   channel"?
10. Does a refused send suppress the agent's own reply for that round (`HasSentInRound`)?
11. What is the audit event name, and is it added to the `pkg/audit` allowlist in the same change?
12. Does the audit trail record the delegation root, so a delegated send can be traced to the
    turn that caused it?

---

**Verdict: BLOCK** — 3 CRITICAL (C-1 FR-7 vs FR-2; C-2 schedule/replayed destinations falsify
FR-2's premise and FR-5's claim; C-3 ownership record is agent-writable at default policy).

The structural rule introduced this round is the right one and should survive; what it needs is
(a) a provenance stamp so FR-7 can honour it, (b) a third category for destinations that were
captured in one turn and replayed in another, and (c) an ownership record the governed party
cannot edit.
