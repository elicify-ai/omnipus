# ADR-091 — Landing order, interfaces and file ownership (rev 3, consolidated)

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) (rev 4)
- **Purpose:** the contract between the seven work packages so that several agents can build in parallel without editing each other's files or waiting on each other's internals. Each package **publishes** the interfaces below as compiled code before its dependents start; a dependent codes against the published interface, never against the publisher's internals.
- **Why rev 3:** three grills (rev 1 BLOCK 3/17/2, rev 2 BLOCK 2/16/2, rev 3 BLOCK 3/19/2) each found the previous patch had left an older sentence standing beside its replacement. This revision is a consolidation: every interface is restated once, in full, with the founder's decisions through round 9 applied, and the specs are rewritten against it. Nothing below is new mechanism unless the row says so; every sentence names the existing symbol it reuses.
- **Method note:** GitNexus MCP tools are not exposed in the authoring session and the worktree is unindexed (the same fallback ADR-090 recorded). Every `file::symbol` in these specs was verified by source inspection at release tip `364290cb5`.
- **Testing discipline (founder decision, round 4):** every implementing agent writes its own tests with its code using the `test-driven-development` skill. WP-G owns only the shared fixtures, the classification of existing tests and the cross-package suites.

## 0. The one-paragraph model

A sub-agent is a session steered by another session. It is created exactly as a task session is created today (real store, own address, workspace, title, lifecycle record) plus one durable **edge** naming who steers it. It never talks to a human: every place that could publish asks one function *who is this session's audience?* and a steered session's answer is always "its steering session". It reports upward through the path `message_parent` already runs — a durable inbox entry, then a wake — using the message kinds that already exist. Stop stamps a durable marker on every session it reaches, and no dispatch starts a turn on a stamped session. On screen it is a normal session: the parent's chat shows only the `delegate` line; the existing side panel lists children with a one-line status and an open control; the pill counts the open session's direct children; a child streams only in its own opened session.

## 1. Landing order

```
WP-A  Launcher, edge, turn reconstruction      ── publishes I-1, I-2, I-3, I-8, I-9
  │
  ├── WP-B  Audience & upward delivery          ── needs I-1, I-2, I-8; publishes I-4, I-5
  ├── WP-C  Steering surface & delegate front   ── needs I-1, I-2, I-3; consumes I-5
  ├── WP-D  Cancel cascade & boot recovery      ── needs I-1, I-3, I-8, I-9; publishes I-6
  └── WP-E  Contracts & SPA                     ── needs I-1 (edge on the wire), I-4 (frame shape)
        │
        └── WP-F  Residual audit                ── after A–E
WP-G  Fixtures, classification, cross-package suites ── publishes I-7 first; runs alongside all
```

Three things start on day one with no dependency: **WP-A**, **WP-G** (fixtures and the classification table), and the contract half of **WP-E** (schema changes drafted against I-1 and I-4 as soon as WP-A publishes their compiled shapes at CP-0).

## 2. Published interfaces

Names are binding. A package may add to an interface it publishes; it may not rename or remove after publication without a landing-order revision.

**One neutral package holds every published interface and type: `pkg/steer`.** It imports nothing from `pkg/agent`, `pkg/tools`, `pkg/channels` or `pkg/gateway` (only `pkg/session` types and `pkg/api/generated`). `pkg/agent` implements the interfaces; the gateway wires the implementations at boot; `pkg/tools`, `pkg/channels`, `pkg/gateway` and `pkg/agent/testutil` import `pkg/steer` and nothing else of the agent's. This is the existing pattern (`tools/delegate.go::SubTurnSpawner`, `tools/message_parent.go::MessageParentWaker`, `testutil/gateway_harness.go::RegisterGatewayRunner`) generalised to one place, so that audience resolution, cancellation, revival, classification and the fixture hooks are all consumable across package boundaries. Owner of `pkg/steer`: **WP-A** publishes it at CP-0 with every type below; each package fills in its own implementation behind it.

```go
package steer
type SessionLauncher   interface { Launch(ctx, LaunchRequest) (LaunchResult, error); Dispatch(ctx, sessionID string, gen int) (DispatchResult, error) }
type AudienceResolver  interface { Audience(ctx, sessionID string) (Audience, Class, error) }
type UpwardDeliverer   interface { Deliver(ctx, UpwardEvent) (Delivery, error) }
type Canceller         interface { CancelSubtree(ctx, sessionID string, by Principal) (CancelReport, error); Revive(ctx, sessionID string, by Principal) (int, error) }
type RecordClassifier  interface { Classify(ctx, sessionID string) (Class, error) }
type BoundaryObserver  interface { Observe(boundary Boundary, sessionID string, audience Audience) }   // called at every boundary BEFORE the audience decision is acted on
```

### I-1 — The steered-by edge (WP-A → everyone)

Persisted on `session/lifecycle.go::LifecycleRecord`: one new field `SteeredBy` (nil for a session nobody steers), one new field `Origin` (every record), one new field `Stop` (every record), and the **existing** `Generation` field with changed semantics.

| Field | Type | Meaning |
|---|---|---|
| `Origin` (record level, new) | `{Kind, CallID, TaskID}` | `Kind` ∈ `delegate`, `task`, `chat`, `channel`, `scheduled`, `heartbeat`, `verifier`, `plan`, `human` — the record's discriminator; for `delegate` / `task`, `CallID` is the originating `delegate` or `create_task` tool-call id (the span key for I-4) and `TaskID` the task |
| `SteeredBy.SteeringSessionID` | session id | the direct parent; the inbox owner key |
| `SteeredBy.RootSessionID` | session id | the cascade root, **verified by walking the chain at launch**; equal to `SteeringSessionID` at depth 1 |
| `SteeredBy.ReportingTarget` | `{SessionID, Channel, ChatID}` | the steering session's own address; where completion wakes it |
| `SteeredBy.Authorization` | `{Mode, RemainingDepth}` | the gate verdict at launch (`Mode` ∈ `direct`, `task`) |
| `SteeredBy.Limits` | `{TimeoutSeconds}` | creator-set; `0` = the configured default (D9); scope is the session's lifetime across re-entries |
| `SteeredBy.ToolExclusions` | `[]string` | `switch_agent` today |
| `Generation` (record level, **existing field, changed semantics**) | int, starts at 1 | moves when a stopped session is revived by a newer instruction (I-6 `Revive`) **or** when a terminal session is given a follow-up (today's `delegate_followup.go::spawnCorrectiveFollowUp` already increments it — carried over); never on an ordinary re-entry. `lifecycle.go::persistLocked` keeps rejecting same-generation writes after a terminal record, which is why both cases bump it |
| `Stop` (record level, new) | `*{At, Generation, By}` or nil | the durable Stop marker on this session's own record (D8); written by the cascade on the stopped node and every reachable non-terminal descendant |

**Every session that takes part in steering has a record** (*founder decision, round 9*): when a session with no lifecycle record launches its first child, the launcher writes an `ordinary_root` record for it (`SteeredBy == nil`, `Origin.Kind` = the session's `UnifiedMeta.Type`, workspace and owner copied from its metadata). This is what makes a root's Stop durable and lets I-8 tell "a root" from "a child whose record was lost".

Invariants WP-A enforces and WP-G's fixtures assert: parentage immutable; no cycles; `RootSessionID` equals the walked root; an ancestor's record is never pruned (`lifecycle.go::pruneTerminalOne`) while a descendant is non-terminal; **record and metadata agree** — a record with `SteeredBy == nil` whose `UnifiedMeta.ParentSessionID` is set, or whose `UnifiedMeta.Type` is `delegate`, is not a root (I-8).

**Launch and the cascade share one critical section per parent:** the launcher reads the parent's record (for depth, authorization and a current-generation Stop marker) and publishes the child's record **under the parent's record lock**, so a cascade cannot enumerate the parent's children between the check and the publication (I-6 step 5 closes the other side).

Derived at creation and kept in step (existing readers depend on them): `UnifiedMeta.ParentSessionID` = `SteeringSessionID`; `UnifiedMeta.Owner` copied from the steering session; `UnifiedMeta.WorkspaceID` and `Title` stamped; `LifecycleRecord.ParentAgentID` = the steering session's agent (for both fronts — tasks left it empty).

### I-2 — The session launcher (WP-A → WP-C, task executor, WP-G)

`steer.SessionLauncher`, implemented in `pkg/agent`, injected at gateway boot into the delegate tool, the task executor and the I-7 fixture.

| `LaunchRequest` | |
|---|---|
| `SteeringSessionID` | caller's session; empty for a human-, schedule- or plan-created task (no edge; an `ordinary_root`) |
| `TargetAgentID` | the agent profile to run |
| `Label` | optional short title; `Task` text is the fallback; both empty → `ErrTitleRequired` (Q15) |
| `Task` | the first user message |
| `Origin` | `{Kind, CallID, TaskID}` as I-1 |
| `WorkspaceID`, `Owner` | **only** for an ordinary-root launch with no steering session (a scheduled or human-created task): taken from the task record; for a steered launch they are inherited from the steering session and these inputs must be empty |
| `Goal` | optional `*GoalSpec` — criteria + Definition of Done, the shape `create_task` already validates (`tools/task.go::validateRequest`); nil = no goal (founder decision, round 3) |
| `Limits`, `ToolExclusions` | as I-1 |

| `LaunchResult` | |
|---|---|
| `SessionID` | the new session |
| `Generation` | 1 at launch |

| `DispatchResult` (the **authoritative** admission decision — R06) | |
|---|---|
| `State` | `running` or `queued` — decided atomically under the admission lock inside `Dispatch`; `queued` when the effective concurrency cap is reached (*founder decision, round 8*): nothing blocks, nothing is refused; the admission loop starts queued sessions in order as slots free |
| `QueuePosition` | 1-based when `State == queued`, else 0 |
| `Generation` | the generation the turn was (or will be) registered in |

Errors (typed, exported from `pkg/steer`): `ErrTitleRequired`, `ErrDepthExceeded`, `ErrInvalidEdge` (cycle, unknown steering session, invalid ancestor), `ErrAgentUnknown`, `ErrStoreWrite`, `ErrDispatchCancelled` (a Stop marker for the current generation), `ErrStaleGeneration`, `ErrTerminal` (dispatch of a terminal session without a follow-up). A nil request panics.

Semantics. `Launch` is atomic for the mandatory writes (identity, `Owner`, workspace, title, edge, `Origin`, `GoalRef` when a goal is given, the steering session's own `ordinary_root` record if missing) and publishes the child under the parent's record lock (I-1): a failure leaves no session and no lifecycle record. It does not start a turn and does not decide admission. `Dispatch(sessionID, gen)` takes the admission lock, calls I-6's reservation, and either registers and runs the first turn (`running`) or leaves the record `queued` with its position; two concurrent dispatches observing one free slot cannot both return `running`. **Return timing** (*founder decision, round 9, superseding round 6*): the `delegate` tool returns as soon as `Launch` and `Dispatch` have returned, reporting `DispatchResult`; the child may already be running. There is no ordering hook between the parent's tool result and the child's start, and none is needed: a child never reaches the parent's chat.

**Reuse.** The launcher is `task_executor.go::createTaskSessionSync` + `mintTaskLifecycleRecord`, extracted and generalised with the edge; `subturn.go::createChildSession` is deleted in its favour. Both fronts call the one function: the task front through `task_executor.go::StartTaskNow`, which calls `Launch` then `Dispatch` when `Task.SessionID` is empty and `Dispatch` alone otherwise. Provenance a task needs at a delayed start is persisted on the task record, disk-only, by `tools/task.go::buildTask` (WP-C, which owns `pkg/task/task.go` for these two fields): the existing `OriginSessionID` and a new `OriginCallID` (the `create_task` tool-call id); the launcher reads them into `SteeringSessionID` and `Origin.CallID` — the steering session applies only if it still exists, otherwise the run is an `ordinary_root`. **Terminal sessions:** `Dispatch` on a terminal record returns `ErrTerminal`; a follow-up on a terminal session (`delegate_followup.go`, kept) bumps the generation first, exactly as today, and then dispatches; a Stop over a terminal descendant writes nothing (terminal records are immutable) and reports it as `SkippedTerminal`.

### I-3 — Turn reconstruction, dispatch and admission (WP-A → WP-B, WP-C, WP-D)

`pkg/agent`, package-internal: `reconstructSteeredTurn(rec *LifecycleRecord, wake *WakeInput) (processOptions, turnIdentity, error)`. Only `pkg/agent` builds turns.

`steer.WakeInput{MessageID, Kind, Generation}` is the inbox entry that woke the session (nil for a first run or a human message); `Generation` is the recipient's generation at the time the entry was written. Reconstruction:

1. classifies the record (I-8) and refuses anything but `steered` or `ordinary_root`;
2. refuses a wake whose `Generation` is older than the record's (a stale wake queued before a revival — `ErrStaleGeneration`);
3. **consumption rule:** before executing, the turn appends a `consumed <MessageID>` marker to the session's transcript; a later wake carrying an id already marked is acknowledged (`MessageInboxStore.Ack`) without a turn. When the recipient already has a live turn, the wake is enqueued into that turn (I-5) and the **steering-queue drain writes the same marker when it dequeues the message** — so consumption is defined identically for both paths. The guarantee is exactly this — *each upward event is consumed at most once: by one new turn or by one injection into a live turn, never both, never neither*; what the turn then does has the same guarantees as any interrupted turn today (a crash mid-turn is recovered by the boot sweep as an interrupted turn; it is not replayed from the event);
4. refuses, with a diagnostic, when the record or its edge cannot be read or fails I-1's invariants.

Every entry path — first run, wake after a child completes, follow-up, boot recovery — calls reconstruction and **never** builds `processOptions` for a steered session by hand. `processSystemMessage` (`loop_inbound.go`) is **owned by WP-A** for this reason; WP-B and WP-D consume its behaviour and do not edit it.

**Turn registration** is internal to `pkg/agent` (both WP-A and WP-D work there): `turn.go::registerActiveTurn` is a plain map store today; WP-A adds `registerTurnIfAbsent(ts) bool` (compare-and-set on `activeTurnStates`, keyed by `ts.sessionKey`, with `ts` built by reconstruction) and `Dispatch` calls I-6's `reserveDispatch(rec, gen)` and then `registerTurnIfAbsent(ts)` under the record lock, in that order. Two concurrent dispatches of the same session resolve to exactly one turn. Nothing outside `pkg/agent` constructs a turn.

**Admission** (D9, *founder decision, round 9*): the concurrency counter counts **turns executing right now** (`activeTurnStates` entries for steered sessions and task sessions), not sessions in a `running` state. A parent whose turn has ended and is waiting for children holds no slot. The cap is the **effective** value from the existing resolver `config_defaults_apply.go::EffectiveMaxParallelAgents` (environment override, then the configured positive value, then the existing safety backstop for zero/unset — carried over unchanged; a raw zero is never compared against). When a turn ends, the admission loop dispatches the oldest `queued` session (FIFO by launch time) if the counter is below the effective cap.

### I-4 — Frame shape (WP-B emits, WP-E owns the contract)

*Founder decision, round 6: reuse, do not reinvent.*

- Every session-scoped WebSocket frame a steered session produces carries **`session_id` = the producing session**, never an ancestor's. `producing_session_id` — the workaround — is deleted from every schema that carries it: `ToolResultProjectionFrame.yaml`, `ToolApprovalRequiredFrame.yaml`, `SubagentEndFrame.yaml`, `GoalStatusFrame.yaml`, `LoopStatusFrame.yaml`, `TaskStatusChangedFrame.yaml`, the inline copies in `asyncapi.yaml`, the Go payloads in `pkg/agent/events.go` and their readers in `pkg/gateway/websocket_forward.go` (three sites) — one inventory, WP-E for schemas, WP-B for Go.
- The parent's stream carries the parent's own lifecycle events about its child, **persisted as events in the parent's transcript and replayed by the existing since-cursor path** (`websocket_replay.go::loadReplay` reads the transcript; nothing reads the inbox or the lifecycle store at replay): `subagent_start` (gains optional `child_session_id`; its `parent_call_id` / `span_id` is `Origin.CallID` for **both** fronts — the `create_task` call id persisted on the task record for task-origin children, so `replay.go::buildSubagentStart` recognises `create_task` alongside `delegate`), `subagent_end`, and the two frames ADR-053 defined but nothing emits or consumes today: `subagent_message` (`kind`, `text`, `pct` — emitted from every `SessionMessage` the child writes; its `kind` enum gains `goal_status`, which it excludes today — WP-E, an enum value on an existing frame, not a new frame type) and `subagent_state` (emitted on every lifecycle transition, including `queued` → `running`). Ordering: `subagent_start` precedes the first `subagent_state`; `subagent_end` follows the terminal `subagent_state`. A queued launch emits `subagent_start` + `subagent_state(queued)` at launch. Revival and terminal follow-up emit `subagent_state(running)` again.
- These feed the side panel's status line and the pill. No new frame type; attach stays single-session; nothing nests a child's steps into a parent's chat.
- ADR-057 FR-047 (zero references to the two status frames in `src/`, guarded by `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts`) is superseded by ADR-091 D7; WP-E deletes the guard in the commit that adds the consumer.

### I-5 — Audience and upward delivery (WP-B → everyone)

**Audience.** `steer.AudienceResolver.Audience(ctx, sessionID) (Audience, Class, error)` — implemented in `pkg/agent` on top of I-8, injected into every package that hosts a boundary (`pkg/agent` itself, `pkg/tools` for boundary 8, `pkg/channels` for boundary 7, `pkg/gateway` for boundary 6, `pkg/askuser` for boundary 12) at gateway boot. `Audience` ∈ `AudienceUser`, `AudienceSteeringSession`, `AudienceNone`: `ordinary_root` → user; `steered` → steering session; every other class, a missing record for a session that should have one, or an error → none, and the boundary logs a diagnostic. Every boundary in the inventory (§6) calls the resolver, then calls `steer.BoundaryObserver.Observe(boundary, sessionID, audience)` (a no-op in production; the I-7 recorder in tests, which is how "boundary exercised and blocked" is told apart from "never exercised"), and only then acts.

**Upward delivery.** `steer.UpwardDeliverer.Deliver(ctx, UpwardEvent) (Delivery, error)`, implemented in `pkg/agent`, injected wherever `message_parent`'s `MessageParentWaker` is wired today (it replaces that interface).

`UpwardEvent{ChildSessionID, Outcome, Message generated.SessionMessage}` — the event **is** an existing `SessionMessage`, written with `session/message_inbox.go::MessageInboxStore.Append` first, then woken with `async_notifier.go::WakeParent`. There is **no second vocabulary**: every outcome maps onto a kind the inbox already stores and the SPA already renders:

| Turn outcome | Persisted `LifecycleState` | `SessionMessage` kind written | `message_id` | Wakes? | Side-panel line |
|---|---|---|---|---|---|
| non-empty final answer, subtree quiet | `completed` | `handback` (`mode: final`, `result_so_far` = the answer, `artifacts`, `open_questions` = questions of still-parked descendants) | `<child>:<gen>:final` | yes | "done" |
| empty final answer (*founder decision, round 10*) | `failed` | `error` (`fatal: true`, `text` begins `empty_answer:`) | `<child>:<gen>:final` | yes | "gave no answer" |
| parked on a question | `needs_input` | `question` (existing relay) | `<child>:<gen>:q:<n>` | yes | the question |
| interrupted by Stop | `cancelled` | `error` (`fatal: true`, `text` begins `interrupted:`) | `<child>:<gen>:final` | yes — unless the recipient itself carries a Stop marker, in which case the entry is stored and acknowledged at revival | "stopped" |
| timed out | `timed_out` | `error` (`fatal: true`, `text` begins `timed_out:`) | `<child>:<gen>:final` | yes | "timed out" |
| failed (incl. boot sweep) | `failed` | `error` (`fatal: true`, `text` begins `failed:`) | `<child>:<gen>:final` | yes | the error |
| answered, a descendant still `queued` or `running` | stays `running` | **nothing yet**; the parent's `handback` is written when its subtree becomes quiet, by the last such child's completion wake re-entering the parent | — | — | "waiting for N children" |
| progress / checkpoint (child's `message_parent`) | unchanged | `progress` / `checkpoint` (existing) | inbox-assigned | **no** | the text |
| blocker (child's `message_parent`) | unchanged | `blocker` (existing) | inbox-assigned | yes (as today) | the text |
| max-iterations lifecycle notice | unchanged | `error` (`fatal: false`) | inbox-assigned | no | the notice |
| Judge verdict on a goal (*founder decision, round 10*) | unchanged | `goal_status` — the existing kind, **extended minimally by WP-E**: direction `session_to_parent`, condition `not_met` added, an `evidence` list of `{criterion, met, note}` | `<child>:<gen>:verdict:<n>` | yes | "verdict: met" / "verdict: not met — 2 of 3" |

**One wake-eligibility table, used by initial delivery and by boot alike:** wakes = `handback`, `question`, `blocker`, `error` with `fatal: true`, `goal_status`; never wakes = `progress`, `checkpoint`, `error` with `fatal: false`. This is today's `wakeableSessionMessageKinds` (`question`, `blocker`, `error`, `handback`) plus `goal_status` and minus non-fatal errors.

"Subtree quiet" = no descendant in `queued` or `running`. A parked descendant does not block the parent's completion; its question travels in `open_questions`.

`Delivery{MessageID, Outcome ∈ woke \| queued_into_live_turn \| stored_not_woken \| suppressed}`. `MessageID` is the inbox entry's id and is the delivery identity that travels in the wake (`WakeInput`). **Terminal entries have deterministic ids** (`<child>:<gen>:final`), so the same outcome recreated after a crash is the same entry and the inbox's `message_id` de-duplication applies. **Write order for a terminal outcome:** the inbox entry is appended first, the terminal lifecycle write second; boot repairs either half — a non-terminal record whose parent inbox already holds its `:final` entry is finished from that entry, and a terminal record whose parent inbox lacks it gets the entry recreated (same id). If the recipient already has a live turn, the wake is enqueued as a steering message into that turn (`steering.go::enqueueSteeringMessage`, existing) — `queued_into_live_turn`, no second turn, consumed by the drain (I-3). **Admission and suppression:** wake-eligible kinds are always admitted to the inbox and bypass `async_notifier.go::allowWake` (its 15-second debounce and hourly cap); `progress` / `checkpoint` and non-fatal `error` remain subject to the inbox's unacknowledged cap and per-minute rate — `suppressed` is therefore possible only for those, never for a wake-eligible kind. **Boot:** every **wake-eligible** inbox entry whose recipient has no `consumed` marker and no `Ack` is re-woken once by the boot hook (`gateway_boot.go`, WP-D); non-eligible entries are never woken at boot either; the consumption rule (I-3) makes a duplicate harmless.

`Principal` (who acts on a steering action) is `{Kind ∈ agent\|human, ID}`; for a human it is the authenticated gateway identity of the WebSocket or REST caller, passed down by the gateway — never constructed by a tool. **Bus route authority (R17):** `bus/session_message.go::SessionMessageEvent` gains a `Principal` set by the publisher after it verified authority (`tools/delegate.go::verifyCallerOwnsSession` for tools; the gateway's authenticated identity for a human); `session_messaging_wire.go::deliverParentToChild` re-verifies, against the target's edge, that the principal is an ancestor of the target or the human — a valid child naming another valid child is refused.

**Reuse.** This is `message_parent.go`'s existing path (`Append` → `WakeParent`) made the only upward path; the completion wake and `task_executor_judge.go::notifyParentIfAllSiblingsDone` (deleted) are re-routed through it. Acknowledgement is the existing `delegate(action: inbox_ack)`; de-duplication is the existing `message_id` rule plus deterministic terminal ids plus I-3's consumed marker. New code: the terminal bypass in `allowWake`, the live-turn enqueue branch, deterministic ids, the boot repair and re-nudge, the principal on the bus event.

### I-6 — Cancel reservation, cascade and revival (WP-D → WP-A)

The unit is the **session's own record** (D8 "Terms used here"); with the round-9 rule every session that takes part in steering has one. Published as `steer.Canceller` (for the gateway, the tools and the fixture); the reservation is package-internal.

- `reserveDispatch(rec, gen) (ok bool, reason string)` (internal to `pkg/agent`, called by `Dispatch`) — false when the record's `Stop` marker names the record's current generation (`ErrDispatchCancelled`), when `gen` is older than the record's `Generation` (`ErrStaleGeneration`), or when the record is terminal without a follow-up (`ErrTerminal`); then `registerTurnIfAbsent(ts)` (I-3) decides concurrent duplicates. Reservation and registration happen under the record's lock, in that order; a refused reservation took nothing.
- `CancelSubtree(ctx, sessionID, by) (CancelReport, error)` is **one operation under the cascade lock of the stopped node** (R01), not a sequence of independent writes: (1) take the node's cascade lock; (2) enumerate descendants with `cancel.go::CollectDescendantSessionIDs` (its filter moves from `ParentDurableKey` to the edge's steering session); (3) stamp `Stop{At, Generation: rec.Generation, By}` on the node and on every reachable **non-terminal** descendant, one atomic write each, remembering the generation stamped; (4) cancel each live turn with `RequestCancelForSession(id, gen)` — **the cancel carries the stamped generation** and the turn registry refuses it when the registered turn's generation differs (a revival landed in between → reported in `SkippedNewerGeneration`); (5) enumerate once more and stamp any child published since step 2 (I-1's launch critical section guarantees a child is either visible to this pass or was stamped at launch because its parent already carried the marker); (6) release. Returns `CancelReport{Reached, Unreachable []{ID, Reason}, SkippedNewerGeneration, SkippedTerminal}`. A Stop on a middle node B stamps only B's subtree.
- `Revive(ctx, sessionID, by) (newGen int, error)` — called only by the "newer instruction" paths (a steering message or an answer arriving after the marker, or a follow-up on a terminal session): increments `Generation`, clears nothing (the marker stays as history but names an older generation), emits `subagent_state(running)`. Ordering with a concurrent Stop is by the record's write lock and the generation carried by the cancel: Stop then revive → the revived generation runs; revive then Stop → the new generation is stamped and cancelled. Both are what the operator asked for, in the order asked.
- Partial walks are reported, never silent: unreachable descendants in the report, `partial: true` on the Stop response frame (WP-E), one line on the originating channel (Q21).

### I-7 — Shared fixtures (WP-G → everyone)

`pkg/agent/testutil` (for `pkg/agent` tests) and, for tests in `pkg/tools`, **external-package tests only** (`package tools_test`) — an in-package tools test importing the fixture would recreate the import cycle. The fixture holds only `pkg/steer` interfaces, all injected at construction:

- `DelegationTree(t, deps steer.Deps, depth int) Tree` where `steer.Deps{Launcher, Canceller, Deliverer, Classifier, LifecycleStore, SessionStore, BootHook}` — a real store-backed root → A → B (→ C) chain built through I-2 (until WP-A publishes I-2 at CP-0, through the store directly with the same record shape); each session under a distinct agent profile; distinct workspaces on request. Hooks and the published operation each maps to: `Reenter(B)` → `Deliver` of a `handback` from B's child; `QueueWake(B, gen)` → `Deliver` with the recipient's turn held; `Stop(node)` → `Canceller.CancelSubtree`; `Revive(node)` → `Canceller.Revive`; `CorruptRecord(node, invariant)` / `DeleteRecord(node)` / `DeleteEdge(node)` → the lifecycle store directly; `Crash()` → close every store without flushing; `Reboot()` → reopen the stores and run `BootHook` (the same function `gateway_boot.go` runs).
- `RecordingOutbound(t)` — implements `steer.BoundaryObserver` and wraps every sink in the boundary inventory (§6): `PublishOutbound`, `PublishOutboundMedia`, `SendMedia`, the external stream `Write`, the WS `send`, the `message` tool's send, the task notification and the question-card broadcast. `AssertNothingTo(address)`, `AssertReceived(session, kind)` (the control), `AssertBoundaryInvoked(name)` (true only if `Observe` was called for that boundary — which distinguishes "exercised and blocked" from "never exercised").

Both exist before any WP asserts AC-3 or AC-8.

### I-8 — Record classification (WP-A → WP-B, WP-D)

`steer.RecordClassifier.Classify(ctx, sessionID) (Class, error)`, implemented in `pkg/agent`. Trusted inputs: the lifecycle store **and** the session's own metadata (`UnifiedMeta.Type`, `UnifiedMeta.ParentSessionID`) — and the two must **agree** on every branch: a lost record, or a lost edge on a present record, must never turn a child into a root (R02). Session types today: `chat`, `task`, `channel`, `scheduled`, `heartbeat`, `verifier`, `delegate` (`session/unified.go`).

| Situation | Class | Consequence |
|---|---|---|
| no record; meta has no `ParentSessionID` and `Type` ≠ `delegate` (a chat, channel, scheduled, heartbeat or verifier session that never delegated) | `ordinary_root` | normal audience; may launch (the launcher then writes its record) |
| record present, `SteeredBy == nil`, `Origin.Kind` a root kind, **and** meta has no `ParentSessionID` and `Type` ≠ `delegate` | `ordinary_root` | normal audience; durable Stop marker available |
| record present, valid `SteeredBy` (invariants hold), **and** meta `ParentSessionID == SteeredBy.SteeringSessionID` | `steered` | no user audience; upward delivery; cascade |
| no record, but meta has `ParentSessionID` or `Type == delegate` | `damaged_child` | refused: no dispatch, no publication, operator notice naming the session |
| record present, `SteeredBy == nil`, but meta has `ParentSessionID` or `Type == delegate`, and the record was written by ADR-091 code (`Origin` present) | `damaged_child` (edge lost) | refused, as above — never `ordinary_root` |
| record present, `SteeredBy == nil`, `Origin` absent, `Type == delegate` (written before ADR-091) | `legacy_delegate` | never resumed: set `failed`, reason `pre-adr-091-not-resumable`, one operator notice; never given normal audience |
| record unreadable | `unreadable` | refused, diagnostic, surfaced through I-9 |
| `SteeredBy` present but invalid (cycle, unknown ancestor, wrong root, or meta `ParentSessionID` disagrees) | `invalid_edge` | refused, diagnostic, operator notice; no dispatch |

### I-9 — Index report (WP-A → WP-D)

`pkg/session/lifecycle_index.go::ensureWarm` (unexported) records every unreadable record instead of silently skipping; WP-A publishes an exported accessor on the index, `LifecycleIndex.Report() steer.IndexReport` (`IndexReport{Unreadable []{ID, Err}}`), which WP-D's boot sweep reads through the store it already holds and surfaces to the operator (FR-D-008). Owner of the file: WP-A; owner of the consumer: WP-D.

## 3. File ownership (production code)

| File / area | Owner | Notes |
|---|---|---|
| `pkg/steer/**` (every published interface and type) | **A** | published at CP-0; other packages add nothing here without a landing-order revision |
| `pkg/agent/subturn*.go` **except** `subturn_result.go` (B) | **A** | becomes the launcher + delegate wrapper; ring, borrowed address and `SubTurnConfig.Async` deleted |
| `pkg/session/lifecycle*.go` (record, invariants, `pruneTerminalOne` guard, `ensureWarm` → I-9), `pkg/session/unified_api.go` (creation) | **A** | edge, invariants, derived meta |
| `pkg/agent/turn.go` (identity fields, `registerTurnIfAbsent`, generation-aware cancel refusal), `pkg/agent/loop_inbound.go::processSystemMessage` | **A** | reconstruction, registration |
| `pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents` (reused as is) | **A** (read only) | the effective cap |
| `pkg/agent/task_executor.go` (`StartTaskNow` → launcher), `pkg/agent/task_executor_run.go::processTaskDirectExternalCLI` (the one external wrapper) | **A** | task front, external path |
| `pkg/config` (SubTurn fold), `pkg/agent/delegation_depth.go`, `pkg/agent/admission.go` (turn-counting admission loop), `pkg/sysagent/tools/workspace.go`, `pkg/gateway/rest_workspace_delegation.go` (readers of `SubTurn.MaxDepth`, for that change only) | **A** | D9 |
| `pkg/agent/loop_run_turn_tools.go`, `loop_run_turn_response.go`, `loop.go` publish sites, `pkg/agent/subturn_result.go::emitSubTurnIterationLimitNotice`, `pkg/agent/events.go` (delete `ProducingSessionID`), `pkg/gateway/websocket_forward.go` (its readers, `onSubTurnSpawn`) | **B** | audience at each boundary; frame identity |
| `pkg/tools/message.go` (steered-session own-chat rule) | **B** | boundary 8 |
| `pkg/agent/async_notifier.go` (`allowWake` bypass for wake-eligible kinds, live-turn enqueue), `pkg/session/message_inbox.go::Append` (admission, deterministic ids), `pkg/agent/task_executor_judge.go` (wake, `notifySourceChannel`), `pkg/tools/message_parent.go`, `pkg/agent/session_messaging_wire.go` (principal re-verification), `pkg/bus/session_message.go` (`Principal` on the event), `pkg/askuser/registry.go`, `pkg/channels/manager.go::finalizeHookStreamer` (resolver injection) | **B** | upward delivery, question relay, bus authority |
| `pkg/gateway/websocket_streamer.go`, `websocket_replay.go`, `replay.go` (`buildSubagentStart` for both fronts; `emitNestedToolCalls` deleted) | **B** | server side of D7 |
| `pkg/gateway/gateway_boot.go` (wiring every `pkg/steer` implementation; the boot hook body is D's) | **A** wires, **D** owns the hook body | one file, two named sections |
| `pkg/tools/delegate*.go` (incl. setting `Principal` on published bus events after `verifyCallerOwnsSession`), `pkg/tools/run_task.go`, `pkg/tools/task.go` (`buildTask` sets `OriginSessionID` and `OriginCallID`; launcher call), `pkg/task/task.go` (the new disk-only `OriginCallID` field only) | **C** | fronts and steering actions |
| `pkg/agent/task_run_loop.go`, `pkg/agent/delegation_context.go`, `pkg/agent/loop_delegation.go`, `pkg/agent/loop_env.go`, `pkg/agent/loop_wire.go`, `pkg/coreagent/seed.go`, `pkg/tools/list_jobs_sources.go` | **C** | D4 manifest, D5, D6, D10 |
| `pkg/agent/steering.go` (incl. the drain's consumed marker), `cancel.go` (`CancelSubtree`, `RequestCancelForSession(id, gen)`), `cancel_prearm.go`, `plan_engine.go` (cancel), `boot_sweep.go` (classification, repair, re-nudge), the boot-hook body in `gateway_boot.go`, `pkg/gateway/websocket_cancel.go`, `pkg/gateway/rest_sessions.go` (thin callers of the descendant walk) | **D** | D8, §7 |
| `contracts/**`, generated clients, `src/store/chat/**`, `src/components/chat/**` (incl. `ActivityPanel.tsx`), `src/lib/subagentStatus.ts`, `src/lib/toolVisibility.ts`, `src/hooks/useRunningActivity.ts`, `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` (deleted) | **E** | I-4, D7 client |
| `pkg/constants/channels.go`, `pkg/agent/external_dispatch.go` (caller consolidation), docs, `scripts/adr091-issue-closure.sh` | **F** | D10 leftovers, closure |
| `pkg/agent/testutil/**`, cross-package suites under `tests/` | **G** | I-7 |

Rule: a package needing a change in a file it does not own **requests it from the owner** via the interface list above; it never edits the file. Test files: each package owns the tests beside its own production files; WP-G owns fixtures and cross-package suites only. Integration tests that span two packages are owned by the package whose acceptance criterion they prove.

## 4. Sequencing checkpoints

| Checkpoint | Gate |
|---|---|
| CP-0 | **Every interface is published as compiled code before any dependent starts:** WP-A publishes `pkg/steer` complete (every interface, type and error above), I-1's record fields, I-8's classifier, I-9's accessor; WP-B publishes `AudienceResolver`, `UpwardDeliverer` and `BoundaryObserver` implementations with compiled no-op bodies; WP-D publishes a compiled `Canceller` whose `CancelSubtree` is a no-op and an internal `reserveDispatch` that admits everything; WP-E publishes the contract schemas for I-1, I-4 and the `goal_status` extension; WP-G publishes I-7 and the complete 49-row test classification. **Proof:** `go build ./pkg/tools/... ./pkg/channels/... ./pkg/gateway/... ./pkg/agent/testutil/...` passes against the stubs, **and** one compiled consumer test per interface exercises it (the `message` tool calling the resolver; the external stream calling the resolver; the fixture calling every hook; a boundary calling `Observe`). Stubs are replaced by real bodies at CP-2 / CP-3; no alias period, no second path |
| CP-1 | WP-A's launcher creates a session that WP-G's `DelegationTree` reads back with a valid edge, **after a store close and reopen** (AC-1); every `ParentDurableKey` reader has moved to the edge (15 non-test files; owners in ADR-091 D2) and WP-A has removed the field — one integration, no alias period (founder decision, round 5) |
| CP-2 | WP-B's I-5 bodies land; a `RecordingOutbound` run of the three-level fixture shows nothing at the root address and every boundary was invoked (AC-3, control included) |
| CP-3 | WP-D's I-6 bodies land; a Stop on the fixture root reaches a re-entered child, a queued wake, and survives `Reboot()` (AC-8) |
| CP-4 | WP-E regenerates contracts (`child_session_id`; `producing_session_id` deleted from all seven schemas; `origin`, edge, `stop` on `SessionLifecycleRecord.yaml`; Stop report; run result `state` / `queue_position` / `generation`; `goal_status` extension; `goal_status` in the `subagent_message` kind enum); `make verify-contracts` green; generated validators accept a negative verdict and reject the removed fields; the existing side panel renders a child row with status line and open control from replayed fixture frames |
| CP-5 | WP-C's `delegate` uses the launcher; wait-inline manifest complete (AC-4); `delegate status` answers from the record (#614) |
| CP-6 | WP-F audit finds no leftover from D10; reachability check (AC-12) executed in the real UI |
| CP-7 (post-merge) | Issue closure (AC-13): after the delivery has merged, WP-F runs `scripts/adr091-issue-closure.sh` (named outside the `check-*.sh` guard pattern so CI never discovers it) with the merging PR number; #658, #614, #670, #755, #763, #764, #765 closed by hand with a comment citing that PR and the ADR section; #784 and #803 left open, each with one comment naming the ADR section and the reason |

## 5. What every implementer must do

1. Load the `test-driven-development` skill before writing code. Write the failing test first, from the spec's acceptance criteria — never from the implementation.
2. Cite `file::symbol`, never `file:line`.
3. Build tags `goolm,stdjson`, `CGO_ENABLED=0`; never run the full Go suite locally — one scoped package at a time, `-p 1`.
4. Any wire-format change follows Hard Constraint #8's five steps; generated files are never hand-edited.
5. Report delivery in two lines that are never merged: *code correct and tested*; *reachable by a user/agent*.
6. **Machine checks** (F19): every grep in a spec's machine-verifiable table scans production code only (`--exclude='*_test.go'` / `--exclude='*.test.*'`), states an exact expected count, and runs inside a script step that treats grep's "no match" exit (1) as the expected result when the count is 0 and any other non-zero exit as a failure of the check itself. Guards are proven to fail by reintroducing the banned symbol in a scratch branch.

## 6. Boundary inventory (the one list every spec references)

Twelve places can publish something a human would see. Each calls the injected `steer.AudienceResolver`, then `steer.BoundaryObserver.Observe`, and nothing else decides; each has one containment test and one control in WP-B; `RecordingOutbound` implements the observer and wraps each sink.

| # | Boundary | Site |
|---|---|---|
| 1 | Synchronous tool text | `loop_run_turn_tools.go::deliverToolOutput` |
| 2 | Asynchronous tool feedback | same file, the async callback gate |
| 3 | Final reply | `loop.go::runAgentLoop` |
| 4 | Media | `deliverToolOutput` → `SendMedia` / `PublishOutboundMedia` |
| 5 | Retry notices | `loop_run_turn_response.go::retryTimeout`, `retryContextOverflow` |
| 6 | Webchat streaming | `gateway/websocket_streamer.go` |
| 7 | External-channel streaming | `channels/manager.go::finalizeHookStreamer` |
| 8 | Agent-requested messages | `tools/message.go` — a steered session may target only its own session's conversation (founder decision, round 8) |
| 9 | Task result notification | `task_executor_judge.go::notifySourceChannel` |
| 10 | Typed error frames | the `LLMError` family (`code: delegated_task_limit`, #805) |
| 11 | Delegate-lifecycle notices | `subturn_result.go::emitSubTurnIterationLimitNotice` — becomes an `error` (`fatal: false`) inbox entry, never a root-transcript write |
| 12 | Question cards | `askuser/registry.go` — a steered session's question is relayed to its steering session, never broadcast as the parent's own |

## 7. Delivery lanes and model casting (founder decision, 2026-09-22)

The founder chose this mix of models for implementation instead of the default GLM-first ladder in the global operating rules. Casting follows risk: the lanes where a mistake leaks a message or loses a Stop get the strongest reasoning; mechanical lanes get the cheapest model that can follow an exact spec.

| Lane | Package | Model | Runs as |
|---|---|---|---|
| 1 | WP-A — launcher, edge, reconstruction, admission, and the CP-0 publication of `pkg/steer` | Sonnet | Claude subagent |
| 2 | WP-B — audience and upward delivery | Sonnet | Claude subagent |
| 3 | WP-D — cancel cascade, revival, boot recovery | Codex Sol (`gpt-5.6-sol`) | `codex exec`, workspace-write sandbox |
| 4 | WP-C — `delegate` front, steering, deletion manifest | Codex Sol | `codex exec`, workspace-write sandbox |
| 5a | WP-E — contracts half (schemas and regeneration) | Haiku | Claude subagent |
| 5b | WP-E — SPA half | Sonnet | Claude subagent, after WP-B's frames exist |
| 6a | WP-G — classification of the 49 existing test files | Haiku | Claude subagent |
| 6b | WP-G — fixtures and cross-package suites | Codex Sol | `codex exec`, workspace-write sandbox |
| 7 | WP-F — audit test and guard (Haiku); docs and the issue-closure script (MiniMax via `claudem`) | Haiku, MiniMax | Claude subagent; `claudem -p` |

```
 day 1               CP-0                 CP-1 … CP-5              CP-6      CP-7
 A ██ Sonnet ████████████████████████████████████████████████████▌
 G ██ Haiku: classify 49 ▌██ Codex Sol: fixtures + E2E ██████████▌
 E ██ Haiku: contracts ▌       ░░ Sonnet: SPA ░░░░░░░░░░░░░░░░░░░▌
 B                    ██ Sonnet ████████████████████████████████▌
 C                    ██ Codex Sol ████████████████████████████▌
 D                    ██ Codex Sol ████████████████████████████▌
 F                                                                ██ Haiku ▌ ▪ MiniMax (closure)
 running: 3 ──────────► 6 ─────────────────────────────────────► 1 ─────► 1
```

Rules that make the mix safe:

- **Cross-family review.** Each lane's work is reviewed by a different model family before it enters the integration branch: Sonnet lanes by Codex Sol, Codex lanes by Sonnet, Haiku and MiniMax output by Sonnet. This is in addition to the repository's seven-reviewer gate, not a replacement.
- **One lane, one worktree, one branch.** Every lane works in its own worktree on `adr091/wp-<letter>`, branched from the integration branch `feat/adr-091-steered-sessions`; the lead merges lanes into the integration branch in landing order and resolves conflicts there.
- **One Go compile at a time.** The repository forbids parallel Go test suites locally, so every lane runs Go through one shared lock; lanes queue for it rather than running Go concurrently.
- **CP-0 publication.** To keep the first checkpoint a single step, the WP-A lane writes the compiled no-op bodies for the interfaces WP-B and WP-D later implement, in files named for their owners; ownership of those files passes to WP-B and WP-D at CP-0.
