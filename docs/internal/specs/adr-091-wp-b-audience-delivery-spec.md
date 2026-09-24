# ADR-091 WP-B — Audience and upward delivery

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D3, D7 (server side), §7
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — consumes I-1, I-2, I-8; publishes I-4, I-5; boundary inventory §6
- **Owner files:** landing order §3, row B
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)

## Summary

A steered session must never speak to a human directly. Today that rule is a switch each sending place has to remember, and the places that forgot it are the leaks of the last week — images, retry notices, a woken session's final reply, a timeout notice written into the root chat. This package replaces every switch with one question, asked of the session's classified record: *who is this session's audience?* The answer for a steered session is always "its steering session". It then makes the path a child already uses to talk to its parent — a durable inbox entry, then a wake — the **only** upward path, using only the message kinds that already exist, and it persists the four sub-agent frames into the parent's transcript so the side panel survives a reload.

## Existing codebase context

*Source inspection at `364290cb5` (#805 verified on origin).*

### The twelve boundaries (landing order §6; each becomes a caller of I-5)

| # | Boundary | Symbol | Today |
|---|---|---|---|
| 1 | Synchronous tool text | `pkg/agent/loop_run_turn_tools.go::deliverToolOutput` | gated by `SendResponse` + `SuppressToolFeedback` |
| 2 | Asynchronous tool feedback | same file, the async callback gate | gated by depth == 0 and `IsTaskRun` |
| 3 | Final reply | `pkg/agent/loop.go::runAgentLoop` | publishes `finalContent` when `SendResponse` |
| 4 | Media | `deliverToolOutput` → `SendMedia` / `PublishOutboundMedia` | **ungated** — sends whenever media is present |
| 5 | Retry notices | `pkg/agent/loop_run_turn_response.go::retryTimeout`, `retryContextOverflow` | own conditions |
| 6 | Webchat streaming | `pkg/gateway/websocket_streamer.go::wsStreamer.Update` | shadows on `parentSpawnCallID` |
| 7 | External-channel streaming | `pkg/channels/manager.go::finalizeHookStreamer` | suppresses by `parentSpawnCallID` (#781) |
| 8 | Agent-requested messages | `pkg/tools/message.go::denyUnownedTarget` | allows any **unbound** channel; webchat is unbound and shared by operator decision, so a child could name the root chat and reach the operator |
| 9 | Task result notification | `pkg/agent/task_executor_judge.go::notifySourceChannel` | independent send |
| 10 | Typed error frames | `LLMError` (`code: delegated_task_limit`, #805) | operator-facing |
| 11 | Delegate-lifecycle notice | `pkg/agent/subturn_result.go::emitSubTurnIterationLimitNotice` (#805; called from `subturn.go`) | writes to the **root** transcript |
| 12 | Question cards | `pkg/askuser/registry.go::CreatePending` | rejects children by `ParentSessionID` |

### The upward path — reused, not rebuilt

`Deliver` (I-5) is the path `pkg/tools/message_parent.go` already runs: `session/message_inbox.go::MessageInboxStore.Append` (durable, first) then `async_notifier.go::WakeParent`. Acknowledgement is the existing `delegate(action: inbox_ack)`; the inbox retains every unacknowledged entry (`DefaultInboxUnackedMax`) and already de-duplicates by `message_id`. The message kinds are the existing `SessionMessage` variants — `handback` (with `mode`, `result_so_far`, `artifacts`, `open_questions`), `question`, `error` (`text`, `fatal`), `progress`, `checkpoint`, `blocker`, `goal_status`. Nothing new is added to that vocabulary.

| Symbol | Today | Change |
|---|---|---|
| `pkg/tools/message_parent.go::MessageParentWaker` | the tools-side injected wake interface | replaced by `steer.UpwardDeliverer` (I-5); the tool keeps calling an injected interface |
| audience at every boundary | each site decides alone (`SendResponse`, depth, `parentSpawnCallID`) | each site calls the injected `steer.AudienceResolver`, then `steer.BoundaryObserver.Observe`, then acts; the resolver is wired into `pkg/agent`, `pkg/tools`, `pkg/channels`, `pkg/gateway`, `pkg/askuser` at boot (R03) |
| `pkg/agent/async_notifier.go::WakeParent`, `wakeableSessionMessageKinds` (`question`, `blocker`, `error`, `handback`) | wakes with the **child's** `AgentID` as sender identity | wakes with the **parent's** identity resolved from the edge; the one wake-eligibility table (I-5) = today's set plus `goal_status`, minus non-fatal `error`; used by initial delivery and by boot alike (R09) |
| `pkg/agent/async_notifier.go::allowWake` | 15-second debounce and hourly cap on every wake; suppression returns success | wake-eligible kinds bypass it entirely (`WakeParentAlways` never consults `allowWake`), so a completion can never be throttled away; the rest never reach a wake at all and are reported `stored_not_woken` |
| `pkg/session/message_inbox.go::Append` | unacknowledged cap and per-minute rate on every kind; ids assigned by the inbox | wake-eligible kinds always admitted; cap and rate apply to `progress` / `checkpoint` / non-fatal `error`; terminal entries carry deterministic ids (`<child>:<gen>:final`), so a recreated entry is the same entry (R10) |
| write order of a terminal outcome | — | inbox entry first, terminal lifecycle write second; boot repairs either half (WP-D) |
| `pkg/agent/task_executor_judge.go::notifyParentIfAllSiblingsDone` | wakes at `"task:" + parent.ID`; fires only when all siblings are terminal | **deleted**; completion goes through `Deliver`, per child |
| `pkg/bus/session_message.go::SessionMessageEvent` | carries no trusted publisher principal | gains `Principal`, set by the publisher after it verified authority (tools: `verifyCallerOwnsSession`; human: the gateway's authenticated identity) (R17) |
| `pkg/agent/session_messaging_wire.go::deliverParentToChild` | checks the target exists and has a non-empty parent key — does not establish who is sending | re-verifies the event's `Principal` against the target's edge: an ancestor of the target, or the human — a valid child naming another valid child is refused |
| recipient with a live turn | second wake starts a second turn | the wake is enqueued into the live turn as a steering message (`steering.go::enqueueSteeringMessage`); no second turn; the drain writes the consumed marker (I-3) |
| boot | nothing re-wakes an entry whose wake never happened | the boot hook (WP-D) re-wakes every **wake-eligible** unacknowledged entry without a consumed marker — progress and checkpoint are never woken, at boot or otherwise |

### Server side of D7 (frames, persistence, replay)

| Symbol | Today | Change |
|---|---|---|
| `websocket_streamer.go::wsStreamer.Update` / finalize | `isShadowStream` for `parentSpawnCallID` **and** for a second turn on the same session | delegation shadowing removed; same-session ownership kept |
| `websocket_replay.go::bindConnection`, `loadReplay` | one attached session; replays its transcript; no viewer check | **unchanged** — a child is opened like any session (viewer check: none, per founder Q4) |
| `replay.go::emitNestedToolCalls` | rebuilds nested child steps from the attached transcript | **deleted** — no child steps are in a parent transcript any more |
| `replay.go::buildSubagentStart` | derives the span from a `spawn` / `delegate` tool-call id | recognises `create_task` too; reads `child_session_id` from the persisted event |
| `pkg/agent/events.go` `ProducingSessionID` on `ToolExecStartPayload`, `ToolExecEndPayload`, `ToolResultProjectionPayload`; readers in `pkg/gateway/websocket_forward.go` (three sites) and `onSubTurnSpawn` | the workaround for relabelled frames | **deleted**; every frame carries its own `session_id` (I-4) |
| parent transcript | `subagent_start` / `subagent_end` are rebuilt from tool calls at replay; `subagent_message` / `subagent_state` are defined by ADR-053 and **never emitted** (no Go emitter, no SPA consumer) | all four are **persisted as events in the parent's transcript** at the moment they happen — keyed by `SteeredBy.Origin.CallID`, for both fronts — and replayed by the existing since-cursor path; `subagent_message` on every `SessionMessage` the child writes, `subagent_state` on every lifecycle transition |

### Impact assessment (manual)

| Symbol | Risk | Dependents |
|---|---|---|
| `runAgentLoop` final publish | **HIGH** — every turn | all channels |
| `wsStreamer` shadowing | **HIGH** — live chat rendering | SPA (WP-E) |
| `WakeParent` / `allowWake` | HIGH — every async completion | `processSystemMessage` (WP-A) |
| `message_inbox.go::Append` admission | MEDIUM | every `message_parent` caller |
| `deliverToolOutput` media | MEDIUM | channel adapters |

## User stories and acceptance criteria

### US-1 — A steered session never reaches a human directly (P0)

*As the operator, I want nothing a sub-agent produces to appear in my chat unless the agent I am talking to says it, so that I always know who is speaking.*

**Why P0:** this is the leak class.
**Independent test:** three-level delegation with a recording outbound; assert per boundary, and assert each boundary was actually exercised.

1. **Given** a steered session at any depth, **When** each of the twelve boundaries is exercised, **Then** nothing is delivered to a user-facing address, and `RecordingOutbound` — as the injected `BoundaryObserver` — confirms the boundary was reached before the audience decision was acted on.
2. **Given** the same session, **When** it produces those things, **Then** each is present in its own transcript and view (control: the session demonstrably ran).
3. **Given** a steered session re-entered after its child completed, **When** it produces its final reply, **Then** AS-1 still holds.
4. **Given** a steered session on an external channel chain (Telegram/WeCom origin), **When** it streams, **Then** nothing reaches the external user.
5. **Given** a steered session's tool call **actually fails** (a real failed tool result, not a typed lifecycle error), **When** the failure is produced, **Then** the failed result is in the session's own transcript and view, an `error` inbox entry (`fatal: false`) reaches its parent, and its row in the parent's side panel shows the line — never a bare bubble in the root chat.
6. **Given** a steered session calls the `message` tool, **When** the target is its own session's conversation, **Then** it is allowed; **When** the target is the root's webchat id, another agent's session, or any external channel, **Then** it is refused with `steered_session_own_chat_only` — checked with real channel ownership (founder decision, round 8).
7. **Given** a steered session's question (boundary 12), **When** it is raised, **Then** it is relayed to the steering session as a `question` entry, never broadcast as the parent's own card.

### US-2 — A steered session reports to its steering session, per child, reliably (P0)

*As a steering agent, I want to be told when each child finishes, pauses or fails — with the right identity, exactly once in effect — so that I can act.*

**Independent test:** complete a child; observe exactly one wake on the parent's own address, carrying the child as producer.

1. **Given** child C of B finishes with a non-empty answer and a quiet subtree, **When** delivered, **Then** a `handback` (`mode: final`, id `C:<gen>:final`) is in B's inbox for C, B is woken once, on B's own address, as agent B, with C as producer.
1b. **Given** C finishes with an **empty** answer, **When** delivered, **Then** C is persisted `failed` and B receives an `error` (`fatal: true`, `empty_answer:`) — never a `handback` (founder decision, round 10).
2. **Given** B has two children, **When** one finishes, **Then** B is woken for that child without waiting for the other.
3. **Given** C parks with a question, **When** delivered, **Then** B receives a `question` entry and C stays parked until B responds.
4. **Given** C fails, times out or is interrupted, **When** delivered, **Then** B receives an `error` entry with `fatal: true` whose text begins `failed:`, `timed_out:` or `interrupted:`.
5. **Given** the process dies after C's `handback` is appended but before C's record is marked terminal, **When** the process restarts, **Then** boot finishes C's record from the entry, B's entry is re-woken, and B's turn starts once; **Given** it dies after the terminal write but before the append (a lifecycle write without its entry), **Then** boot recreates the entry with the same deterministic id and wakes B once.
6. **Given** the process dies after B's turn consumed the wake but before it acknowledged, **When** the process restarts, **Then** the re-wake is acknowledged without a second turn (I-3 consumed marker).
6b. **Given** the wake was injected into B's live turn, **When** the drain dequeues it, **Then** the consumed marker is written then, and a later re-wake is acknowledged without a turn.
7. **Given** B already has a live turn when C finishes, **When** delivered, **Then** the wake is enqueued into that turn as a steering message and no second turn starts.
8. **Given** B's inbox is at its unacknowledged cap and over its per-minute rate from C's progress, **When** C completes, **Then** the `handback` is still admitted and still wakes; progress entries beyond the cap are dropped as today.
9. **Given** ten children complete within the same second, **When** delivered, **Then** ten `handback` entries exist and B is woken (or enqueued) for each — the debounce and hourly cap do not apply to terminal kinds.
10. **Given** C's progress or checkpoint via `message_parent`, **When** delivered, **Then** the entry is stored and shown, and B is **not** woken — not now and not at boot (founder decision, round 8); **Given** a `blocker`, **Then** B is woken, as today.
11. **Given** C is failed by the boot sweep, **When** delivered, **Then** B receives an `error` entry; a log line alone is not delivery.
12. **Given** C ran with a goal, **When** the Judge rules "not met" on 2 of 3 criteria, **Then** B receives a `goal_status` entry with direction `session_to_parent`, condition `not_met` and three `evidence` items — valid against the regenerated contract (founder decision, round 10).
13. **Given** B carries a Stop marker for its current generation, **When** C's interruption is delivered, **Then** the entry is stored and B is not woken; it is acknowledged when B is revived.
14. **Given** a bus event for `steer` on C whose `Principal` is sibling D (a valid child), **When** it reaches delivery, **Then** it is refused; **Given** the principal is B, A or the human, **Then** it is delivered.

### US-3 — A child is watched in its own session; its parent's side panel survives a reload (P1)

*As the operator, I want to open a sub-agent's session and see it work, and see in the parent's side panel what each child is doing — after a reload too — without child steps ever entering the parent's chat.*

**Independent test:** drive a child; assert frames per session; restart; assert replay.

1. **Given** child C is running, **When** a viewer opens C, **Then** C's frames arrive with C as `session_id` and render in C's view; nothing of C reaches B's bucket.
2. **Given** C is launched from `delegate` call `call-7`, reports progress, parks, is answered, and completes, **When** each happens, **Then** B's transcript gains, in order: `subagent_start` (`parent_call_id: call-7`, `child_session_id: C`), `subagent_state(running)`, `subagent_message(progress)`, `subagent_state(needs_input)`, `subagent_state(running)`, `subagent_state(completed)`, `subagent_end`.
3. **Given** C is launched from `create_task` call `call-9` and starts later, **When** it starts, **Then** B's transcript gains the same sequence keyed `call-9`.
4. **Given** C is launched at the cap, **When** launched, **Then** `subagent_start` and `subagent_state(queued)` are written; `subagent_state(running)` follows when it starts.
5. **Given** grandchild C of B, **When** C emits status, **Then** the frames land in B's transcript (its own parent's), not the root's.
6. **Given** the process restarts and a browser with an empty store opens B, **When** replay runs, **Then** `loadReplay` returns the sequence in AS-2 from B's transcript.
7. **Given** two turns compete for the same session's stream, **When** the second starts, **Then** stream ownership still prevents it from completing the first — that protection is kept.

### Edge cases

| Case | Expected |
|---|---|
| Steering session deleted when the child completes | entry persisted as undeliverable (deterministic id kept); surfaced to the operator; child marked completed |
| Producer classified anything but `steered` or `ordinary_root` (I-8), including a present record whose edge was lost | audience `AudienceNone`; nothing published; diagnostic |
| Resolver returns an error (store down) | `AudienceNone`; the boundary logs and drops; never defaults to the user |
| Event storm (child emits 1,000 progress entries) | inbox rate-limit applies to progress; no wakes; the terminal `handback` still lands |
| Media from a steered session | persisted to its transcript; never sent to a channel |
| `handback` larger than the inbox entry limit | the answer is truncated in `result_so_far` with a marker and the full text stays in the child's transcript; the entry is still admitted |

## Behavioral contract

- When a turn publishes anything, the system first asks the session's audience from its classified record; a steered session's audience is its steering session.
- When a steered session finishes, pauses, fails or is judged, the system writes one existing-kind inbox entry to its steering session and wakes it once in effect — or enqueues into its live turn.
- When a steered session reports progress, the system stores and shows it and does not wake the parent.
- When a wake cannot be delivered, the system records it as undeliverable and shows the operator.
- When a child's frames are produced, the system files them under the child; when a child's lifecycle changes, the system writes the corresponding event into the parent's transcript so replay carries it.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not decide audience at a publishing site, because per-site switches are the class of defect being removed.
- The system must not relabel a child's frame with the parent's id, because that is what made the child invisible in its own view.
- The system must not batch a child's completion behind its siblings, because a slow sibling would hide a finished result (founder decision Q5).
- The system must not deliver a wake with the child's identity as the actor, because the parent's turn must run as the parent.
- The system must not treat a log line as delivery, because #803 showed a parent left waiting.
- The system must not add a second message vocabulary, because the inbox, the SPA and the tools already speak `SessionMessage`.
- The system must not let a terminal outcome be suppressed by a rate limit or a debounce, because a lost completion strands the parent.
- The system must not let a steered session address any conversation but its own, because webchat is shared and ownership cannot stop it (founder decision, round 8).

### Machine-verifiable constraints

Conventions: landing order §5 item 6.

| Constraint | Exact check |
|---|---|
| Nothing to user addresses | `RecordingOutbound` shows 0 calls with the root's `Channel`/`ChatID` for every one of the twelve boundaries, for a 3-level fixture, first run and re-entry |
| Every boundary exercised | `AssertBoundaryInvoked(name)` true for all twelve in the same run — via `Observe`, called before the decision is acted on |
| Control | the child's transcript contains its tool output, final reply and a **real failed tool result**; its view received ≥ 1 frame per kind; the parent's inbox holds the `error` entry for that failure |
| Own chat only | `message` tool from C: own chat allowed; root webchat id, sibling session id, Telegram → `steered_session_own_chat_only`; ownership resolver is the real one |
| One handback | exactly 1 `handback` per child completion with id `C:<gen>:final`; a repeat wake for its id starts 0 turns |
| Empty answer | empty final answer → record `failed`, `error` entry `empty_answer:`; 0 `handback` |
| Write order and repair | crash after append / crash after terminal write → after boot exactly one entry with the deterministic id, one consumption |
| Bus authority | `Principal` = sibling → refused; = ancestor or human → delivered; event without a principal → refused |
| Resolver injected | `go list -deps ./pkg/tools ./pkg/channels` contains no `omnipus/pkg/agent`; the tools and channels boundaries call the injected resolver (compiled consumer test at CP-0) |
| Correct identity | the parent's woken turn has `agentID == parent.AgentID`, `transcriptSessionID == parent id` |
| Durable | restart between persist and wake → 1 turn; restart between consume and ack → 0 additional turns |
| Live turn | recipient turn active → `Delivery.Outcome == queued_into_live_turn`; `activeTurnStates` gains no entry |
| Terminal admission | inbox at cap and over rate → `handback` stored, `Delivery.Outcome == woke`; `progress` → dropped as today |
| Wake-eligible bypasses debounce | 10 completions in 1 s → 10 `woke`/`queued_into_live_turn`, 0 `stored_not_woken` |
| Progress no wake | `progress` / `checkpoint` / non-fatal `error` → `stored_not_woken`; at boot, none of them is woken; `blocker` → `woke` |
| Verdict encoding | a `not_met` verdict with evidence validates against the regenerated `SessionMessageGoalStatus` and appears as a `subagent_message` of kind `goal_status` |
| External channels | Telegram/WeCom stream adapter receives 0 chunks from a steered session |
| Frames primary | every session-scoped frame from C has `session_id == C`; none from C appears in B's bucket; `ProducingSessionID` absent from `pkg/agent/events.go` |
| Persisted events | B's transcript contains the AS-2 sequence for a delegate child and the AS-3 sequence for a task child, keyed by the originating call id |
| Replay | after restart, `loadReplay(B)` returns those events in order |
| Stream ownership kept | second turn on the same session cannot mark the first's stream done |

## Integration boundaries

| System | In / out | Contract | On failure |
|---|---|---|---|
| Lifecycle store + classification (WP-A) | reads the class for audience | I-1, I-8 | not runnable → `AudienceNone` |
| Message inbox | every upward entry | `MessageInboxStore.Append(ownerKey, generated.SessionMessage)` | terminal always admitted; others rate-limited and surfaced |
| Async notifier | the wake transport | `WakeParentAlways` — the terminal bypass, no debounce or hourly cap | a failed wake yields `stored_not_woken`, reported at ERROR by the caller |
| Steering queue | live-turn enqueue | `steering.go::enqueueSteeringMessage` | existing behaviour |
| Channel adapters | receive nothing from steered sessions | audience = none | n/a |
| Gateway WS | frames, replay | I-4 | reconnect replays per session |
| SPA (WP-E) | consumes I-4 | contract | n/a |
| Boot hook (WP-D) | re-wakes unacknowledged entries | I-5 | surfaced |

## BDD scenarios

```gherkin
Feature: Audience and upward delivery

  # Error Path — Traces to: US-1 / AS-1, AS-2
  Scenario Outline: A steered session exercises boundary <n> and nothing reaches the root chat
    Given a chain R -> A -> B -> C with a recording outbound
    When C exercises boundary "<boundary>"
    Then the recorder shows the boundary was invoked
    And no delivery carries R's channel and chat id
    And C's transcript contains the produced content
    Examples:
      | n  | boundary                    |
      | 1  | synchronous tool text       |
      | 2  | asynchronous tool feedback  |
      | 3  | final reply                 |
      | 4  | media                       |
      | 5  | retry notice                |
      | 6  | webchat stream              |
      | 7  | external-channel stream     |
      | 8  | agent-requested message     |
      | 9  | task result notification    |
      | 10 | typed error frame           |
      | 11 | delegate-lifecycle notice   |
      | 12 | question card               |

  # Error Path — Traces to: US-1 / AS-3
  Scenario: A re-entered session's final reply stays contained
    Given B has been re-entered after C completed
    When B writes its final reply
    Then it is delivered to A as a handback entry and not to R's chat

  # Error Path — Traces to: US-1 / AS-6
  Scenario Outline: The message tool from a steered session
    Given real channel ownership is wired
    When C calls the message tool targeting "<target>"
    Then the result is "<result>"
    Examples:
      | target                  | result                          |
      | C's own conversation    | allowed                         |
      | R's webchat chat id     | steered_session_own_chat_only   |
      | sibling D's session id  | steered_session_own_chat_only   |
      | a Telegram chat         | steered_session_own_chat_only   |

  # Happy Path — Traces to: US-1 / AS-7
  Scenario: A child's question is relayed, not broadcast
    When C asks a question
    Then B's inbox holds a question entry from C
    And no question card is broadcast under B's name

  # Happy Path — Traces to: US-2 / AS-1
  Scenario: Completion wakes the parent with the right identity
    When C completes with a final answer
    Then B's inbox holds one handback from C with mode final
    And A, the grandparent, receives no wake for C
    And B is woken once on B's own address
    And the woken turn runs as agent B with producer C

  # Alternate Path — Traces to: US-2 / AS-2
  Scenario: Per-child wake
    Given B launched C1 and C2
    When C1 completes
    Then B is woken for C1 while C2 is still running

  # Error Path — Traces to: US-2 / AS-4
  Scenario Outline: Failure kinds
    When C ends by "<outcome>"
    Then B's inbox holds an error entry with fatal true and text starting "<prefix>"
    Examples:
      | outcome     | prefix        |
      | failing     | failed:       |
      | timing out  | timed_out:    |
      | being stopped | interrupted: |

  # Error Path — Traces to: US-2 / AS-5, AS-6
  Scenario Outline: Wake survives a crash, exactly once in effect
    Given C's completion is persisted
    And the process dies "<when>"
    When the process restarts
    Then B's turn for C has started exactly "<turns>" time(s) in total
    Examples:
      | when                          | turns |
      | before B is woken             | 1     |
      | after B consumed, before ack  | 1     |

  # Alternate Path — Traces to: US-2 / AS-7
  Scenario: A live parent turn receives the wake as steering
    Given B's turn is executing
    When C completes
    Then B's steering queue holds C's handback notice
    And no second turn for B is registered

  # Error Path — Traces to: US-2 / AS-8, AS-9
  Scenario: Terminal outcomes are never suppressed
    Given B's inbox is at its unacknowledged cap from C's progress
    And nine other children of B completed in the last second
    When C completes
    Then C's handback is stored and B is woken or enqueued
    And every delivery outcome is "woke" or "queued_into_live_turn"

  # Alternate Path — Traces to: US-2 / AS-10
  Scenario: Progress is stored, not woken — not even at boot
    When C calls message_parent with progress "checking the checkout page"
    Then B's inbox holds the progress entry
    And B is not woken
    When the process restarts with that entry unacknowledged
    Then B is still not woken

  # Error Path — Traces to: US-2 / AS-1b
  Scenario: An empty answer is a failure
    When C ends its turn with an empty final answer
    Then C's record is failed
    And B's inbox holds an error entry with fatal true starting "empty_answer:"

  # Error Path — Traces to: US-2 / AS-5
  Scenario Outline: A half-written terminal outcome is repaired at boot
    Given C's completion crashed "<when>"
    When the process restarts
    Then B's inbox holds exactly one entry with id "C:1:final"
    And C's record is completed
    And B's turn for C starts once
    Examples:
      | when                                   |
      | after the inbox append, before the terminal write |
      | after the terminal write, before the inbox append |

  # Error Path — Traces to: US-2 / AS-14
  Scenario Outline: Bus authority
    When a steer event for C arrives with principal "<principal>"
    Then the result is "<result>"
    Examples:
      | principal   | result    |
      | B (parent)  | delivered |
      | A (grandparent) | delivered |
      | the human   | delivered |
      | D (sibling) | refused   |
      | none        | refused   |

  # Happy Path — Traces to: US-2 / AS-12
  Scenario: A negative Judge verdict reaches the parent, structured
    Given C ran with three criteria and the Judge finds two unmet
    When the verdict is delivered
    Then B's inbox holds a goal_status entry with condition not_met and three evidence items
    And the entry validates against the generated contract
    And B's side panel row reads "verdict: not met — 2 of 3"

  # Error Path — Traces to: US-1 / AS-5
  Scenario: A real tool failure is visible in both views
    When C's bash tool returns a failed result
    Then C's transcript and view show the failed result
    And B's inbox holds an error entry with fatal false for it
    And B's chat shows nothing

  # Error Path — Traces to: edge cases
  Scenario: An undeliverable completion is surfaced
    Given B was deleted while C was running
    When C completes
    Then the entry is persisted as undeliverable and the operator is notified
    And C is marked completed

  # Alternate Path — Traces to: US-2 / AS-13
  Scenario: A stopped parent is not woken
    Given B carries a Stop marker for its current generation
    When C's interruption is delivered
    Then the entry is stored and B is not woken
    When B is revived
    Then the entry is acknowledged

  # Happy Path — Traces to: US-3 / AS-1, AS-2
  Scenario: A child streams in its own session; the parent's transcript records its lifecycle
    Given the operator has opened C, launched from delegate call "call-7"
    When C runs a tool, reports progress, parks, is answered and completes
    Then C's view shows the tool step with session_id C
    And B's bucket receives none of C's steps
    And B's transcript contains, in order: subagent_start(call-7, child C), subagent_state(running), subagent_message(progress), subagent_state(needs_input), subagent_state(running), subagent_state(completed), subagent_end

  # Alternate Path — Traces to: US-3 / AS-3, AS-4
  Scenario: A task child and a queued child are recorded the same way
    Given C2 is created by create_task call "call-9" and starts later
    And C3 is launched at the cap
    Then B's transcript gains subagent_start(call-9) when C2 starts
    And B's transcript gains subagent_start and subagent_state(queued) for C3 at launch, then subagent_state(running) when it starts

  # Alternate Path — Traces to: US-3 / AS-6
  Scenario: Replay after restart rebuilds the side panel
    Given the process has restarted and a browser with an empty store opens B
    When replay runs
    Then loadReplay returns C's lifecycle events from B's transcript in order

  # Alternate Path — Traces to: US-3 / AS-7
  Scenario: Stream ownership is kept
    Given a turn on session S owns S's stream
    When a second turn on S starts
    Then it cannot mark the first turn's stream done
```

## TDD plan

Implementers load the `test-driven-development` skill first.

| Order | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestAudience_ByClass` | Unit | US-1 | I-8 class → audience table, all seven classes |
| 2 | `TestBoundary_<n>_Contained` ×12 | Integration | US-1/AS-1,2 | one per boundary; `RecordingOutbound` + `AssertBoundaryInvoked` + control |
| 3 | `TestBoundary_ReentryFinalReply` | Integration | US-1/AS-3 | after re-entry |
| 4 | `TestBoundary_ExternalStreamZero` | Integration | US-1/AS-4 | Telegram/WeCom capture |
| 5 | `TestMessageTool_SteeredSessionOwnChatOnly` | Integration | US-1/AS-6 | real ownership; four targets |
| 6 | `TestQuestionCard_RelayedNotBroadcast` | Integration | US-1/AS-7 | boundary 12 |
| 7 | `TestDeliver_HandbackOnePerChild_Identity` | Integration | US-2/AS-1,2 | count, kind, identity |
| 8 | `TestDeliver_Question_ParksUntilRespond` | Integration | US-2/AS-3 | relay |
| 9 | `TestDeliver_FailureKinds` | Unit | US-2/AS-4 | three prefixes |
| 10 | `TestDeliver_CrashBeforeWake_OneTurn` | Integration | US-2/AS-5 | persist, crash, restart |
| 11 | `TestDeliver_CrashAfterConsumeBeforeAck_NoSecondTurn` | Integration | US-2/AS-6 | with WP-A's consumed marker |
| 12 | `TestDeliver_LiveTurn_EnqueuedAsSteering` | Integration | US-2/AS-7 | no second turn |
| 13 | `TestInbox_TerminalAlwaysAdmitted` | Unit | US-2/AS-8 | at cap and over rate |
| 14 | `TestWake_TerminalBypassesDebounce` | Unit | US-2/AS-9 | 10 in 1 s |
| 15 | `TestDeliver_ProgressStoredNotWoken` | Unit | US-2/AS-10 | `stored_not_woken` |
| 16 | `TestBootSweep_FailureReachesParent` | Integration | US-2/AS-11 | with WP-D |
| 17 | `TestDeliver_GoalVerdict` | Integration | US-2/AS-12 | `goal_status` |
| 18 | `TestDeliver_StoppedRecipientNotWoken` | Integration | US-2/AS-13 | stored; acked on revive |
| 19 | `TestFrames_PrimaryIsProducer_NoProducingSessionID` | Unit | US-3/AS-1 | every payload type; field absent |
| 20 | `TestParentTranscript_LifecycleEvents_Delegate` | Integration | US-3/AS-2 | the seven-event sequence |
| 21 | `TestParentTranscript_LifecycleEvents_TaskAndQueued` | Integration | US-3/AS-3,4 | `create_task` key; queued state |
| 22 | `TestParentTranscript_GrandchildInOwnParent` | Integration | US-3/AS-5 | not the root's |
| 23 | `TestReplay_AfterRestart_ReturnsLifecycleEvents` | Integration | US-3/AS-6 | empty browser store; `loadReplay` |
| 24 | `TestStreamOwnership_Kept` | Unit | US-3/AS-7 | second turn |
| 25 | `TestParentChat_NoChildFrames` | Integration | US-3/AS-1 | B's bucket receives nothing from C |
| 26 | `TestDeliver_UndeliverableSurfaced` | Integration | edge | steering session deleted |
| 27 | `TestDeliver_EmptyAnswer_FailedWithError` | Integration | US-2/AS-1b | record `failed`; `error` `empty_answer:`; no `handback` |
| 28 | `TestBoot_RepairsHalfWrittenTerminal` | Integration | US-2/AS-5 | both crash orders; one entry, one consumption (boot hook is WP-D's; this test is WP-B's because it proves delivery) |
| 29 | `TestDrain_WritesConsumedMarker` | Unit | US-2/AS-6b | live-turn injection consumed once |
| 30 | `TestBus_PrincipalReverified` | Integration | US-2/AS-14 | sibling refused; ancestor and human delivered; missing principal refused |
| 31 | `TestDeliver_GoalStatus_NotMet_Validates` | Integration | US-2/AS-12 | generated validator; `subagent_message(goal_status)` emitted |
| 32 | `TestToolError_RealFailure_BothViews` | Integration | US-1/AS-5 | a real failed tool result, not a typed lifecycle error |
| 33 | `TestAudienceResolver_InjectedIntoToolsAndChannels` | Unit (compile + call) | CP-0 | the `message` tool and the external stream call the injected resolver |
| 34 | `TestBoot_ProgressNeverWoken` | Integration | US-2/AS-10 | unacknowledged progress at boot → no wake |

### Test datasets

| ID | Input | Expected | Traces to |
|---|---|---|---|
| B-1 | depth 1 / 2 / 3 exercising each of the 12 boundaries | 0 user deliveries; boundary invoked; transcript has it | US-1 |
| B-2 | re-entry after 1 / 2 child completions | contained | US-1/AS-3 |
| B-3 | children 1 / 2 / 5 / 10 completing in any order, within 1 s | one `handback` each; 0 `stored_not_woken` | US-2 |
| B-4 | crash points: before persist / after persist before wake / after wake before consume / after consume before ack | one turn in every case (the first re-runs the child's finish) | US-2/AS-5,6 |
| B-5 | 1,000 progress entries in 1 s, then completion | progress rate-limited, 0 wakes; `handback` admitted and woken | US-2/AS-8,10 |
| B-6 | steering session deleted | undeliverable recorded and surfaced | edge |
| B-7 | frames: tool start/end, projection, text, error, subagent start/end/message/state | `session_id` == producer for all; `producing_session_id` absent | US-3 |
| B-8 | origin ∈ {delegate call, create_task call, queued launch} | transcript sequence keyed by the call id | US-3/AS-2,3,4 |
| B-9 | `message` targets: own / root webchat / sibling / Telegram | allowed / refused ×3 | US-1/AS-6 |

### Regression requirements

Preserved: the assertions and controls of `pkg/agent/system_turn_tool_output_test.go` and `pkg/agent/async_child_publication_test.go` (#766/#783 containment) — the second file's setup constructs the deleted ring and is moved to the persisted-store fixture by WP-A, assertions unchanged (WP-G classification: update); #781 external suppression semantics (now via audience); #782 attributed error notices for system turns; same-session stream ownership; today's `wakeableSessionMessageKinds`. Updated: tests asserting shadowing by `parentSpawnCallID`; tests asserting the sibling notifier. Deleted: tests of `notifyParentIfAllSiblingsDone`.

## Functional requirements

| ID | Requirement |
|---|---|
| FR-B-001 | Every boundary in the inventory MUST resolve audience through the injected `steer.AudienceResolver` from the I-8 class and MUST NOT publish to a user address for a steered session. |
| FR-B-002 | The system MUST deliver one durable upward entry — an existing `SessionMessage` kind per the I-5 table, with the deterministic id `<child>:<gen>:final` for terminal outcomes — per child completion, parking, failure and goal verdict, on the steering session's own address, with producer and recipient identities distinct; the inbox entry MUST be appended before the terminal lifecycle write; the `message_id` MUST travel in the wake; a wake for an id the recipient's transcript already records as consumed MUST be acknowledged without a second consumption. |
| FR-B-003 | The system MUST wake per child; `task_executor_judge.go::notifyParentIfAllSiblingsDone` is deleted. |
| FR-B-004 | The system MUST surface undeliverable entries to the operator. |
| FR-B-005 | Every session-scoped frame MUST carry the producing session as `session_id`; `ProducingSessionID` MUST be deleted from `pkg/agent/events.go` and its readers in `websocket_forward.go`; no frame is ever re-labelled with an ancestor's id. |
| FR-B-006 | The system MUST persist `subagent_start`, `subagent_state`, `subagent_message` and `subagent_end` as events in the parent's transcript, keyed by `SteeredBy.Origin.CallID`, for both fronts and for queued launches, in the order I-4 states, so the existing since-cursor replay returns them. |
| FR-B-007 | The system MUST delete delegation-specific stream shadowing and `replay.go::emitNestedToolCalls`, and MUST keep same-session stream ownership. |
| FR-B-008 | Tool errors from a steered session MUST be visible in its own view and, as an `error` inbox entry, as a line in the parent's side panel. |
| FR-B-009 | A steered session's `message` tool MUST accept only its own session's conversation as target and MUST refuse every other target with `steered_session_own_chat_only`; proven with real channel ownership, never a nil ownership stub. |
| FR-B-010 | One wake-eligibility table MUST govern initial delivery and boot alike: `handback`, `question`, `blocker`, fatal `error` and `goal_status` wake, are always admitted and bypass `allowWake`; `progress`, `checkpoint` and non-fatal `error` never wake (not at boot either) and remain subject to the cap and the rate and MUST be reported `stored_not_woken`, never as woken. Because a wake-eligible kind bypasses `allowWake` outright, a wake the debounce or the hourly cap threw away is not a reachable outcome for it and there is no separate `suppressed` result: `DeliveryOutcome` has exactly three values — `woke`, `queued_into_live_turn`, `stored_not_woken`. |
| FR-B-011 | When the recipient has a live turn, the wake MUST be enqueued into it as a steering message and MUST NOT start a second turn; the drain MUST write the consumed marker when it dequeues it. |
| FR-B-012 | A steered session's question MUST be relayed as a `question` entry to its steering session and MUST NOT be broadcast as the parent's own card. |
| FR-B-013 | When the recipient carries a Stop marker for its current generation, a terminal entry MUST be stored and MUST NOT wake it; it is acknowledged at revival. |
| FR-B-014 | Every boundary MUST obtain its audience from the injected `steer.AudienceResolver` and MUST call `steer.BoundaryObserver.Observe` before acting; a resolver error MUST yield `AudienceNone`. |
| FR-B-015 | `SessionMessageEvent` MUST carry a `Principal` set by the verified publisher, and `deliverParentToChild` MUST re-verify it against the target's edge (ancestor or human); an event without a principal, or from a non-ancestor, MUST be refused. |
| FR-B-016 | An empty final answer MUST be persisted `failed` and delivered as an `error` (`fatal: true`, `empty_answer:`); it MUST NOT produce a `handback`. |
| FR-B-017 | A Judge verdict MUST be delivered as the existing `goal_status` kind extended by WP-E (direction `session_to_parent`, condition `not_met`, `evidence`), and MUST validate against the regenerated contract; it MUST be emitted as a `subagent_message` of kind `goal_status`. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-B-1 | 0 user-address deliveries across 12 boundaries × 3 depths × 2 entry kinds in the fixture; 12 of 12 boundaries invoked. |
| SC-B-2 | exactly 1 consumption (a new turn or one live-turn injection, never both, never neither) per completion across 100 randomized completion orders and 5 crash points; 0 `stored_not_woken` outcomes for wake-eligible kinds across 1,000 randomized bursts of an un-stopped recipient — burst rate alone never costs a wake (a `stored_not_woken` for a wake-eligible kind is only ever the Stop marker of FR-B-013, a missing recipient record or a failed wake, never throttling). |
| SC-B-3 | 100% of frames from a steered session carry it as `session_id`; `ProducingSessionID` absent from `pkg/agent/events.go`. |
| SC-B-4 | After restart, `loadReplay` returns 100% of persisted lifecycle events for a delegate child and a task child. |

## Traceability

| Requirement | Story | Scenarios | Tests |
|---|---|---|---|
| FR-B-001 | US-1 | outline ×12; re-entry | 1, 2, 3, 4 |
| FR-B-002 | US-2 | completion; failure kinds; crash outline; repair outline | 7, 9, 10, 11, 28 |
| FR-B-003 | US-2 | per-child | 7 |
| FR-B-004 | edge | undeliverable completion | 26 |
| FR-B-005 | US-3 | own session; no child frames | 19, 25 |
| FR-B-006 | US-3 | lifecycle sequence; task and queued; replay | 20, 21, 22, 23 |
| FR-B-007 | US-3 | stream ownership kept | 24 |
| FR-B-008 | US-1 | real tool failure in both views | 32 |
| FR-B-009 | US-1 | message tool outline | 5 |
| FR-B-010 | US-2 | never suppressed; progress stored, not even at boot | 13, 14, 15, 34 |
| FR-B-011 | US-2 | live turn; drain consumes | 12, 29 |
| FR-B-012 | US-1 | question relayed | 6 |
| FR-B-013 | US-2 | stopped parent | 18 |
| FR-B-014 | US-1 | boundary outline (Observe) | 2, 33 |
| FR-B-015 | US-2 | bus authority | 30 |
| FR-B-016 | US-2 | empty answer | 27 |
| FR-B-017 | US-2 | negative verdict | 31 |

ADR ACs covered: AC-3, AC-7 (server half), AC-11 (the media and `SendResponse` containments become audience), §7 recovery.

## Ambiguity warnings

| What was ambiguous | Resolution |
|---|---|
| Whether a steered session may `message` other sessions | **Founder decision (round 8): own session only.** Round 5's "same rule as a task session, unchanged" is superseded — `denyUnownedTarget` lets any unbound channel through, and webchat is unbound |
| Whether progress wakes the parent | **Founder decision (round 8): no** — stored and shown; today's `wakeableSessionMessageKinds` kept; the 500 ms coalescing window from round 5 is withdrawn |
| Multi-session subscription | **Moot (round 6):** a child streams only in its own opened session |

## Holdout evaluation scenarios (not for development)

1. Ask Jarvis to delegate a site build to Jim; watch Jarvis's chat for the whole run: nothing appears except Jarvis's own words.
2. Open the worker's session from the side panel mid-run: its tool calls and text stream there.
3. Keep Jim's chat open at the same time: it shows only the `delegate` line; the side panel row for the worker updates its one-line status as the worker reports progress.
4. Make a worker take a screenshot: it appears in the worker's session, never in Jarvis's.
5. Kill the process the instant a worker finishes; restart: Jim is told once.
6. Start a delegation from Telegram; the worker's narration never reaches Telegram; the final answer from the top agent does.
7. Force a tool error in a worker: visible in the worker's view and as an error line on its row in Jim's side panel, with attribution.
8. Have a worker try to message Jarvis's chat directly: refused; Jarvis sees nothing.
9. Reload the browser mid-run: the side panel rows come back with their status lines.

## Definition of done

1. *Code correct and tested:* the TDD plan passes per package; the #766/#783 tests' assertions and controls unchanged and green (setup migration by WP-A).
2. *Reachable:* holdout 1–4, 8 and 9 executed in the real UI and recorded.
