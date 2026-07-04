# Feature Specification: Async Wake Mechanism (`AsyncNotifier`)

**Created**: 2026-07-04
**Status**: Draft
**Input**: Session design review (`tool-consolidation-design.html`, approved) + [ADR-036](../architecture/ADR-036-consolidate-shell-and-subagent-tools.md) §3.3, extended per operator direction: must support a future Goals/loop feature (Claude Code-style persistent condition + auto-continuation) without requiring a later refactor.

> Supersedes the informal draft of this same file written earlier in the same session — that draft is preserved in git history; this version adds full BDD/TDD structure and the goals-readiness requirement (FR-N4).

> **Implementation precondition (added 2026-07-04, 7-reviewer gate MAJ-002):** before starting, verify the working tree includes commit `d0f65482` (`git log --oneline | grep d0f65482`). At the time this spec was drafted, the local checkout was two commits behind `origin/hotfix/v0.1.1` and did not include it — if absent, fetch and check out `origin/hotfix/v0.1.1`'s actual tip rather than branching from local HEAD.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/agent/loop.go`'s inline `asyncCallback` closure | extends (extracted from) | Currently the ONLY place this behavior exists — built per tool-call, captures `ts.channel`/`ts.chatID`, publishes `ForUser` via `bus.PublishOutbound` and `ContentForLLM()` via `bus.PublishInbound` with sender `"async:<toolName>"`. This spec extracts and generalizes it. |
| `pkg/tools/registry.go`'s `ExecuteWithContext` | calls | Invokes `ExecuteAsync` (and passes the callback through) when a tool implements `AsyncExecutor` and a non-nil callback is supplied. Currently true only for `SpawnTool`. |
| `pkg/bus.MessageBus` (`PublishInbound`/`PublishOutbound`) | calls | Confirmed to be a fixed-channel pub/sub (Inbound/Outbound/OutboundMedia) with **no generic topic-subscribe API** — this is why the extensibility this spec requires (FR-N4) must live inside `AsyncNotifier` itself, not be delegated to the bus. |
| `pkg/agent/task_trigger.go`'s `TaskTriggerScheduler` | participates in a sibling flow | The existing SCHEDULE-based trigger half (cron-driven). `AsyncNotifier` is the EVENT-based trigger half a future Goals feature needs alongside it — the two are siblings, not one built on the other. |
| `pkg/tools.AsyncCallback` / `AsyncExecutor` | extends | The existing per-tool-call callback interface; unchanged by this spec — `AsyncNotifier` sits one layer up, called FROM inside a tool's `AsyncCallback` implementation (or, post-merge, from `bash`'s background-completion path directly). |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/agent/loop.go`'s `asyncCallback` closure | MEDIUM | `spawn` tool (today), `bash` background mode (new, per `bash-tool-spec.md`), unified subagent tool (per `agent-delegation-spec.md`) | Any WS frame consumer expecting an `EventKindFollowUpQueued` event shape — must not change |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Spawn-async completion → new turn | The exact flow being extracted and generalized; must remain byte-for-byte behaviorally identical after extraction. |
| `bash run_in_background` completion | New call site this spec adds (per `bash-tool-spec.md`'s FR-B1). |
| (Future, not built here) Goal condition check | The forward-compatibility target — see FR-N4. |

### Cluster Placement

This feature belongs to the **agent-loop / async-execution** cluster. It is a foundational primitive both `bash-tool-spec.md` and `agent-delegation-spec.md` depend on, and it is the one piece of this three-spec change explicitly designed to outlive them — a future Goals spec will depend on it too.

---

## User Stories & Acceptance Criteria

### User Story 1 — A backgrounded shell command wakes the agent when it finishes (Priority: P0)

An agent runs a long build or install in the background (`bash run_in_background=true`), continues other work, and — without polling — sees the result surface as soon as it's ready, the same way a background subagent's result already surfaces today. Currently only `spawn`'s completion path has this; `bash`'s background mode has none, forcing an agent to remember to poll.

**Why this priority**: This is the concrete, immediately-useful behavior this spec exists to deliver; everything else (extraction, extensibility) is in service of this and the identical need in `agent-delegation-spec.md`.

**Independent Test**: Start a `bash run_in_background=true` command that completes in under a second; assert a synthetic inbound message appears on the same channel/chat, without any other feature from the other two specs being implemented.

**Acceptance Scenarios**:

1. **Given** an agent has called `bash` with `run_in_background: true` and the command has completed, **When** the background process exits, **Then** a new turn is triggered on the same channel and chat as the originating call, containing the command's output.
2. **Given** a backgrounded `bash` command is killed via `action: kill`, **When** the kill completes, **Then** the same wake mechanism fires with content indicating the process was killed, not that it completed normally.

---

### User Story 2 — The extraction changes nothing about today's behavior (Priority: P0)

Refactoring `spawn`'s inline callback into a named, reusable `AsyncNotifier` must be invisible to anyone observing the system from outside — same message shape, same sender ID convention, same failure logging, same event emission.

**Why this priority**: A regression here would silently break the one async-completion behavior that already works in production; this is the safety net for the whole extraction.

**Independent Test**: Run the existing (pre-extraction) `spawn`-async-completion test suite, unmodified, against the post-extraction code. Every test must still pass without a single assertion changed.

**Acceptance Scenarios**:

1. **Given** the pre-extraction test suite for `spawn`'s async completion, **When** it is run against the refactored code with zero test changes, **Then** every test passes.
2. **Given** a `spawn` call completes with `Silent: false` and a non-empty `ForUser`, **When** the callback fires, **Then** the direct-to-user publish (`bus.PublishOutbound`) still happens exactly as before, independent of whether a new turn is also triggered.

---

### User Story 3 — A future Goals feature can hook into every wake event without changing today's producers (Priority: P0)

The operator has explicitly required this: when a future "Goals" feature (a persistent condition the agent works toward across turns, auto-continuing until satisfied — the same shape as the Claude Code Stop-hook mechanism this session itself ran under) is built, it must be able to observe every `AsyncNotifier` event (to check "does this make progress on an active goal?") by registering itself once, with zero changes required to `bash`, the unified subagent tool, or any other future producer that already calls `Notify`.

**Why this priority**: This is the one requirement in this spec explicitly about avoiding future rework — getting it wrong here means a real refactor later, which is exactly what this user story exists to prevent.

**Independent Test**: Register a no-op test observer against `AsyncNotifier` and assert it receives a copy of every event `Notify` is called with, using only test doubles — no real Goals feature needs to exist to verify this.

**Acceptance Scenarios**:

1. **Given** an observer has been registered on `AsyncNotifier`, **When** any producer calls `Notify`, **Then** the observer receives the full structured event (not just the subset the bus-publish step uses).
2. **Given** no observer is registered (today's state), **When** `Notify` is called, **Then** behavior is identical to before observers existed — the bus-publish (new-turn) behavior is not contingent on any observer being present.
3. **Given** a registered observer panics, **When** `Notify` is called, **Then** the panic is recovered and does not prevent the bus-publish step from completing — an observer's failure never breaks the core wake behavior.

---

## Behavioral Contract

Primary flows:
- When any background mechanism's work finishes, the system delivers the result as a new turn on the originating channel/chat, without the agent needing to poll.
- When `Notify` is called, the system also offers the event to every registered observer, independent of the bus-publish outcome.

Error flows:
- When the bus-publish fails, the system logs the failure at Error level with enough context to diagnose, and returns a non-nil error to the caller — it does not silently discard the result.
- When an observer panics, the system recovers and continues — one broken observer never blocks the core wake behavior or any other observer.

Boundary conditions:
- When `Notify` is called with an empty channel or chatID, the system rejects the call rather than publishing a message with an ambiguous destination.
- When no observers are registered, `Notify`'s behavior is unchanged from having zero observer-related code at all.

---

## Edge Cases

- What happens when two producers call `Notify` for the same channel/chatID at nearly the same instant (e.g., a background bash command and a background subagent both finish within milliseconds)? Expected: both notifications are delivered; ordering is not guaranteed relative to each other, but neither is dropped.
- What happens when `Notify` is called after the originating session has already ended (e.g., the conversation was closed while a background task was still running)? Expected: the publish is attempted and its failure (if the session/channel no longer accepts inbound messages) is logged, not silently swallowed; this is an existing pre-extraction behavior gap, not something this spec is scoped to fix, but it must not get worse.
- What happens when the `content` payload is very large (e.g., a build log with thousands of lines)? Expected: truncated to a bounded size before publishing, following the same convention `exec`'s existing background-output truncation already uses — no unbounded growth into the conversation.
- What happens when an observer is registered twice (the same observer instance)? Expected: it is called twice per event — registration is additive, not deduplicated; documented as the caller's responsibility to avoid double-registering.

---

## Explicit Non-Behaviors

- The system must not deliver notifications mid-turn (interrupting a turn already in progress) because that requires a materially different delivery model (true push, not "start a new turn") that this spec explicitly defers — see ADR-036 §3.3's "not delivered by this decision" note.
- The system must not let an observer's return value or error suppress or alter the core bus-publish step because that would make a future Goals observer capable of silently breaking the one behavior every existing producer already depends on.
- The system must not require `bash` or the unified subagent tool to know whether any observer is registered because that would recreate exactly the tight coupling this spec exists to avoid — producers call `Notify` and are otherwise unaware of what consumes it.
- The system must not persist notification history beyond what's needed for at-most-one delivery attempt because this is a wake mechanism, not an audit log or an event store — durable history, if ever needed, belongs to a different feature.

---

## Integration Boundaries

### `pkg/bus.MessageBus`

- **Data in**: `InboundMessage{Channel: "system", Sender: {CanonicalID: "async:<sourceKind>"}, ChatID, Content}`.
- **Data out**: none (fire-and-forget from `AsyncNotifier`'s perspective; the bus's own delivery guarantees are unchanged by this spec).
- **Contract**: unchanged from today's `PublishInbound`/`PublishOutbound` signatures — this spec is a caller, not a modifier, of the bus.
- **On failure**: logged at Error with tool/source name, channel, chatID, and the error; the error is also returned to `Notify`'s caller.
- **Development**: real service — the bus is in-process, not an external dependency; no mock/twin needed.

---

## BDD Scenarios

### Feature: `AsyncNotifier` — background work wakes the conversation

#### Scenario: Backgrounded bash command wakes the agent on completion

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent called `bash` with `run_in_background: true` on channel `"telegram"`, chat `"12345"`
- **And** the backgrounded command is `echo done`
- **When** the command exits
- **Then** a new turn is triggered on channel `"telegram"`, chat `"12345"`
- **And** the new turn's triggering message has sender `"async:bash"`
- **And** the message content includes `"done"`

---

#### Scenario: Killed background command reports termination, not success

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a backgrounded `bash` session is running
- **When** it is terminated via `action: kill`
- **Then** the wake mechanism fires with content indicating the process was killed
- **And** the content does not claim the command completed successfully

---

#### Scenario: Extraction preserves spawn's existing async-completion behavior exactly

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the full pre-extraction test suite for `spawn`'s async completion
- **When** the suite is run against the post-extraction code with zero test modifications
- **Then** every test in the suite passes

---

#### Scenario: ForUser content is still published directly regardless of new-turn triggering

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a `spawn` call completes with `Silent: false` and a non-empty `ForUser` field
- **When** the completion callback fires
- **Then** the `ForUser` content is published via `bus.PublishOutbound` directly to the user
- **And** this happens independent of whether `ContentForLLM()` is also non-empty and triggers a new turn

---

#### Scenario: A registered observer receives every notification

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a test observer has been registered on `AsyncNotifier`
- **When** `Notify` is called with source `"bash"`, channel `"slack"`, chatID `"C123"`, content `"build succeeded"`
- **Then** the observer receives an event containing source `"bash"`, channel `"slack"`, chatID `"C123"`, and content `"build succeeded"`

---

#### Scenario: No observers registered behaves identically to pre-observer code

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** no observer has ever been registered on `AsyncNotifier`
- **When** `Notify` is called
- **Then** the bus-publish (new-turn) behavior completes exactly as it would with the observer mechanism entirely absent from the codebase

---

#### Scenario: An observer's panic does not prevent the core wake behavior

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Error Path

- **Given** a registered observer that panics whenever it is called
- **When** `Notify` is called
- **Then** the panic is recovered
- **And** the bus-publish (new-turn) step still completes successfully
- **And** the error is logged, naming the observer, without crashing the calling goroutine

---

#### Scenario: Notify rejects an ambiguous destination

**Traces to**: Edge Cases (boundary condition)
**Category**: Edge Case

- **Given** a caller invokes `Notify` with an empty `channel`
- **When** the call is made
- **Then** `Notify` returns an error without attempting a bus publish

---

#### Scenario: A delivered notification grants no tool capability by itself

**Traces to**: FR-N10 (added 2026-07-04, promoted from holdout per 7-reviewer gate MAJ-002)
**Category**: Error Path

- **Given** an agent with `bash: deny` in its tool policy
- **And** a `Notify` call is made for that agent's channel/chat, with `SourceKind: "bash"` and content that appears to reference a completed command
- **When** the resulting synthetic inbound message triggers a new turn for that agent
- **Then** the agent's tool-policy evaluation for that turn is unaffected — `bash` is still denied
- **And** no code path in `AsyncNotifier` or the triggered turn treats "this turn was `Notify`-originated" as a reason to widen, grant, or bypass any tool's policy verdict

---

#### Scenario: delegate's async completion calls Notify with SourceKind "delegate"

**Traces to**: FR-N11 (added 2026-07-04, cross-spec gap closure)
**Category**: Happy Path

- **Given** an agent has called `delegate` with `async: true` (the default) and the delegated turn has completed
- **When** the completion callback fires
- **Then** `AsyncNotifier.Notify` is called with `SourceKind: "delegate"`
- **And** this mirrors the "Backgrounded bash command wakes the agent on completion" scenario exactly, for the other producer

---

#### Scenario Outline: Large content payloads are truncated before publishing

**Traces to**: Edge Cases
**Category**: Edge Case

- **Given** a background command produced `<byte_count>` bytes of output
- **When** its completion is delivered via `Notify`
- **Then** the published content is truncated to `<expected_max>` bytes with a truncation notice appended

**Examples**:

| byte_count | expected_max | notes |
|---|---|---|
| 500 | 500 (untouched) | under the cap, no truncation |
| 1048576 (1MB) | matches `exec`'s existing background-output cap | Corrected 2026-07-04 (7-reviewer gate, MAJ-001): the real cap, verified directly against `pkg/tools/session.go:14`'s `maxOutputBufferSize = 1 * 1024 * 1024` / `outputTruncateMarker` ("exceeded 1MB"), is 1,048,576 bytes — NOT 50,000. `50000` is a different tool's constant (`pkg/tools/web.go`'s `defaultMaxChars`, for web-fetch content, unrelated to background shell output) and was mistakenly cited here originally. |

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                        | Purpose                                    |
|-------------|------------------------------|--------------------------------------------|
| Unit        | `AsyncNotifier.Notify`, observer registration/invocation, truncation logic | Validates the primitive's logic in isolation from any real bus or tool. Since observer registration is unexported (Clarifications), tests exercising it (Order #4–6, #12) MUST live in `package agent` (internal/white-box), not `package agent_test` — noted 2026-07-04, 7-reviewer gate MIN-002. |
| Integration | `bash` background completion → `Notify` → bus.PublishInbound → new turn observed by the agent loop | Validates the first genuinely new call site works end to end |
| E2E         | A background bash command run through the real gateway surfaces its result without polling | Validates the user-observable behavior this whole spec exists to deliver |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestAsyncNotifier_Notify_PublishesInboundMessage` | Unit | Scenario: Backgrounded bash command wakes the agent on completion | Basic shape: sender ID, channel, chatID, content composed correctly |
| 2 | `TestAsyncNotifier_Notify_RejectsEmptyChannel` | Unit | Scenario: Notify rejects an ambiguous destination | Boundary validation |
| 3 | `TestAsyncNotifier_Notify_TruncatesLargeContent` | Unit | Scenario Outline: Large content payloads are truncated | Truncation matches existing convention |
| 4 | `TestAsyncNotifier_RegisterObserver_ReceivesEvent` | Unit | Scenario: A registered observer receives every notification | Core extensibility requirement (FR-N4) |
| 5 | `TestAsyncNotifier_NoObserver_BehaviorUnchanged` | Unit | Scenario: No observers registered behaves identically | Proves zero-cost when unused |
| 6 | `TestAsyncNotifier_ObserverPanic_Recovered` | Unit | Scenario: An observer's panic does not prevent the core wake behavior | Isolation guarantee |
| 7 | `TestSpawnAsyncCompletion_PreExtractionSuite` (existing, re-run unmodified) | Unit/Integration | Scenario: Extraction preserves spawn's existing async-completion behavior exactly | Regression gate — must pass with zero edits |
| 8 | `TestBashRunInBackground_CompletionTriggersNewTurn` | Integration | Scenario: Backgrounded bash command wakes the agent on completion | First new producer, proves genuine reusability |
| 9 | `TestBashRunInBackground_KillReportsTermination` | Integration | Scenario: Killed background command reports termination | Kill-path variant |
| 10 | `TestAsyncNotifier_NotificationGrantsNoCapability` **(NEW)** | Integration | Scenario: A delivered notification grants no tool capability by itself | Promoted from holdout, 7-reviewer gate MAJ-002 — a `bash: deny` agent stays denied on a `Notify`-triggered turn |
| 11 | `TestDelegateAsyncCompletion_CallsNotifyWithSourceKindDelegate` **(NEW)** | Integration | Scenario: delegate's async completion calls Notify with SourceKind "delegate" | Closes the FR-N11 cross-spec traceability gap |
| 12 | `TestAsyncNotifier_ConcurrentNotifyAndRegisterObserver_RaceFree` **(NEW)** | Unit | (concurrency safety, 7-reviewer gate MIN-001) | Run under `go test -race`; asserts `Notify` and observer registration are safe to call concurrently |
| 13 | E2E: background bash + real gateway + WS observation | E2E | Scenario: Backgrounded bash command wakes the agent on completion | Full-stack proof |

### Test Datasets

#### Dataset: Notify destination validation

| # | Input (channel, chatID) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `("", "chat1")` | Empty (channel) | Error, no publish | BDD Scenario: Notify rejects an ambiguous destination | |
| 2 | `("slack", "")` | Empty (chatID) | Error, no publish | BDD Scenario: Notify rejects an ambiguous destination | |
| 3 | `("slack", "C123")` | Happy path | Publish succeeds | BDD Scenario: Backgrounded bash command wakes the agent | |

#### Dataset: Content truncation

| # | Input (byte length) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 0 | Zero | Empty content published, no notice | Scenario Outline: Large content payloads | Still a valid (if unusual) notification, e.g. a silent `kill` |
| 2 | 1 | Min | Content published untouched | Scenario Outline: Large content payloads | |
| 3 | cap (match `exec`'s existing background-output cap) | Max | Content published untouched | Scenario Outline: Large content payloads | |
| 4 | cap + 1 | Max + 1 | Truncated with notice | Scenario Outline: Large content payloads | |
| 5 | 10x cap | Very large | Truncated with notice | Scenario Outline: Large content payloads | |

### Regression Test Requirements

**If modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| `spawn`'s async completion publishes `ForUser` directly and `ContentForLLM()` as a new inbound message | Existing `pkg/agent` async-completion test(s) | No — run unmodified against extracted code | The whole point of User Story 2 is that these tests don't change |
| `EventKindFollowUpQueued` event shape | Existing event-emission test(s) | No — same assertion, same code path, just relocated | Confirm the extraction doesn't move this emission to before/after a different point in the sequence |

---

## Functional Requirements

- **FR-N1**: The system MUST provide an `AsyncNotifier` interface, extracted from `pkg/agent/loop.go`'s existing inline `asyncCallback` closure, behaviorally identical to that closure's `bus.PublishInbound` step (sender-ID composition, sensitive-data filtering, event emission, failure logging).
- **FR-N2**: The system MUST leave the `ForUser` → `bus.PublishOutbound` step as a separate concern at each call site, not folded into `AsyncNotifier` itself.
- **FR-N3**: The system MUST re-wire `spawn`'s (or, post-merge, the unified subagent tool's) existing callback to call `AsyncNotifier.Notify` with zero behavioral change, proven by the pre-extraction test suite passing unmodified.
- **FR-N4**: The system MUST accept a structured event (not only positional string arguments) carrying at minimum `Channel`, `ChatID`, `AgentID`, `SourceKind`, `Content`, and an open `Metadata map[string]any` field, so a future producer or consumer can extend what's carried without changing `Notify`'s signature. **Provisional (flagged 2026-07-04, 7-reviewer gate MAJ-003)**: this shape is built ahead of the Goals feature it's designed for, which has no spec of its own yet. It MUST be revisited — not merely reused as-is — once Goals is actually specced; "we already built the extension point" is not a reason to skip re-evaluating whether this shape fits Goals' real needs.
- **FR-N5**: The system MUST support registering one or more observers that receive a copy of every `Notify` event, independent of and without affecting the bus-publish outcome. Same provisional caveat as FR-N4.
- **FR-N6**: The system MUST recover from an observer panic without impacting the core bus-publish step or any other registered observer. Same provisional caveat as FR-N4.
- **FR-N7**: The system MUST reject a `Notify` call with an empty `Channel` or `ChatID` before attempting any publish.
- **FR-N8**: The system MUST truncate `Content` to 1,048,576 bytes (1MB) before publishing, matching `pkg/tools/session.go`'s existing `maxOutputBufferSize`/`outputTruncateMarker` convention exactly — not the unrelated 50,000-byte `defaultMaxChars` constant from `pkg/tools/web.go` (corrected 2026-07-04, MAJ-001).
- **FR-N9**: `bash`'s background-completion path (per `bash-tool-spec.md`) MUST call `AsyncNotifier.Notify` with `SourceKind: "bash"` on completion, failure, timeout, or kill.
- **FR-N10** (added 2026-07-04, 7-reviewer gate MAJ-002): `AsyncNotifier.Notify` performs NO authorization or capability-granting of its own — a delivered notification is just a conversation message. The receiving turn's normal tool-policy evaluation is the sole capability gate; nothing about how a turn was triggered (a genuine user message vs. an `AsyncNotifier`-originated one) grants, widens, or bypasses any tool's policy verdict. This division of responsibility MUST be proven by a dedicated test, not left as an implicit assumption.
- **FR-N11** (added 2026-07-04, cross-spec gap from `agent-delegation-spec.md`'s MIN-004): `delegate`'s async-completion path MUST call `AsyncNotifier.Notify` with `SourceKind: "delegate"` on completion or failure, mirroring FR-N9's treatment of `bash` exactly — closing the one-directional traceability gap where `agent-delegation-spec.md`'s FR-D7 referenced this spec but this spec had no matching requirement.

---

## Success Criteria

- **SC-001**: 100% of the pre-extraction `spawn`-async-completion tests pass unmodified against the post-extraction code.
- **SC-002**: A test observer registered on `AsyncNotifier` receives 100% of `Notify` calls made during a test run, with the full structured event, not a subset.
- **SC-003**: An observer that always panics does not cause any of the core `Notify` unit tests (FR-N1–N3, N7–N9) to fail or hang — verified by a dedicated adversarial test.
- **SC-004**: A `bash run_in_background=true` command that completes within 2 seconds surfaces its result as a new turn within 5 seconds of process exit, in a real (not mocked) integration test. This budget applies to small/typical payloads (well under the 1MB truncation cap, FR-N8); large payloads near the cap are not held to this same 5-second budget — no separate latency requirement is stated for them (clarified 2026-07-04, MIN-003).
- **SC-005**: A `bash: deny` agent's tool policy is unaffected by receiving a `Notify`-triggered turn — verified by `TestAsyncNotifier_NotificationGrantsNoCapability` (FR-N10).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-N1 | US-2 | Scenario: Extraction preserves spawn's existing async-completion behavior exactly | `TestSpawnAsyncCompletion_PreExtractionSuite` |
| FR-N2 | US-2 | Scenario: ForUser content is still published directly | (covered within `TestSpawnAsyncCompletion_PreExtractionSuite`) |
| FR-N3 | US-2 | Scenario: Extraction preserves spawn's existing async-completion behavior exactly | `TestSpawnAsyncCompletion_PreExtractionSuite` |
| FR-N4 | US-3 | Scenario: A registered observer receives every notification | `TestAsyncNotifier_RegisterObserver_ReceivesEvent` |
| FR-N5 | US-3 | Scenario: A registered observer receives every notification; Scenario: No observers registered behaves identically | `TestAsyncNotifier_RegisterObserver_ReceivesEvent`, `TestAsyncNotifier_NoObserver_BehaviorUnchanged` |
| FR-N6 | US-3 | Scenario: An observer's panic does not prevent the core wake behavior | `TestAsyncNotifier_ObserverPanic_Recovered` |
| FR-N7 | Edge Cases | Scenario: Notify rejects an ambiguous destination | `TestAsyncNotifier_Notify_RejectsEmptyChannel` |
| FR-N8 | Edge Cases | Scenario Outline: Large content payloads are truncated | `TestAsyncNotifier_Notify_TruncatesLargeContent` |
| FR-N9 | US-1 | Scenario: Backgrounded bash command wakes the agent on completion; Scenario: Killed background command reports termination | `TestBashRunInBackground_CompletionTriggersNewTurn`, `TestBashRunInBackground_KillReportsTermination` |
| FR-N10 | US-1 (security boundary) | Scenario: A delivered notification grants no tool capability by itself | `TestAsyncNotifier_NotificationGrantsNoCapability` |
| FR-N11 | (shared with `agent-delegation-spec.md`) | Scenario: delegate's async completion calls Notify with SourceKind "delegate" | `TestDelegateAsyncCompletion_CallsNotifyWithSourceKindDelegate` |

**Completeness check**: every FR-xxx has at least one BDD scenario and test; every BDD scenario appears in at least one row above.

---

## Ambiguity Warnings

All four ambiguities identified during drafting were resolved by the operator on 2026-07-04 — see Clarifications below. None remain open.

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix.

### Scenario: Operator starts a long-running background command and walks away
- **Setup**: Real gateway, real agent, a `bash run_in_background: true` command that takes ~30 seconds (e.g., a real `npm install`).
- **Action**: The operator sends the request, then does not interact further.
- **Expected outcome**: Within a few seconds of the command finishing, the operator sees the result appear in their chat client without having asked again.
- **Category**: Happy Path

### Scenario: Two background jobs finish close together
- **Setup**: Start a `bash` background command and a subagent delegation (`agent-delegation-spec.md`) at nearly the same time, both completing within a second of each other.
- **Action**: Observe the conversation.
- **Expected outcome**: Both results appear, neither is dropped, and it's clear from the content which result belongs to which originating action.
- **Category**: Edge Case

### Scenario: Background command fails with a non-zero exit code
- **Setup**: A `bash run_in_background: true` command that exits with status 1 and prints an error to stderr.
- **Action**: Wait for completion.
- **Expected outcome**: The surfaced notification clearly indicates failure (not silently reported as success), including the relevant error output.
- **Category**: Error

### Scenario: A hypothetical future consumer is added without touching `bash.go`
- **Setup**: (For internal engineering evaluation only.) Add a throwaway test-only observer to `AsyncNotifier` that logs every event to a file, without modifying `pkg/tools/shell.go`'s (or `bash.go`'s) source at all.
- **Action**: Run a `bash run_in_background` command through the system.
- **Expected outcome**: The throwaway observer's log file shows the event, proving the extensibility promise (US-3) holds for a genuinely new, previously-nonexistent consumer.
- **Category**: Edge Case

### Scenario: Gateway restarts while a background command is still running
- **Setup**: Start a long-running `bash run_in_background` command, then restart the gateway process before it finishes.
- **Action**: Observe what happens on restart.
- **Expected outcome**: This is a settled, accepted limitation, not an open question (aligned 2026-07-04, MIN-004, to match the Assumptions section's framing) — no persistence/durability requirement was specified, matching today's `spawn` behavior exactly. The evaluation should confirm the system fails predictably (the notification is simply never delivered) rather than crashing or corrupting state on restart.
- **Category**: Edge Case

### Scenario: An agent explicitly denied `bash` cannot receive a spoofed wake notification granting it capability
- **Setup**: An agent with `bash: deny` in its policy.
- **Action**: Attempt to trigger a notification claiming to be from a `bash` background job for that agent's channel/chat (e.g., by directly invoking `Notify` in a way an attacker might if they could reach an internal API).
- **Expected outcome**: A delivered notification is just a conversation message — it does not itself grant any tool capability. The next turn's tool policy is still evaluated normally; the notification cannot be used to bypass policy enforcement. This property is now ALSO covered by a required test (`TestAsyncNotifier_NotificationGrantsNoCapability`, FR-N10, promoted from holdout 2026-07-04) — this holdout scenario remains as an additional exploratory/adversarial check beyond that unit test.
- **Category**: Error

---

## Assumptions

- `bash`'s and the unified subagent tool's exact final names/schemas are governed by their own specs (`bash-tool-spec.md`, `agent-delegation-spec.md`); this spec treats them only as call sites of `AsyncNotifier`.
- No persistence/durability guarantee is required for a notification that fails to deliver (e.g., session already closed) — this matches today's `spawn` behavior (log-and-drop on publish failure), not a new gap introduced here.
- The Goals feature itself is out of scope; this spec is judged complete when a hypothetical, throwaway test observer can be attached with zero changes to any existing producer (see the holdout scenario above).
- `AsyncNotifier` lives in `pkg/agent`, is a single process-wide instance held on `AgentLoop`, ships with an empty `Metadata` bag (no concrete fields pre-populated), and its observer-registration method stays unexported for now — all confirmed below.
- FR-N4–N6's observer/extensibility shape is deliberately built ahead of the Goals feature it targets (operator-directed, see Clarifications) — it is provisional, not final, and MUST be re-evaluated once Goals is actually specced (7-reviewer gate MAJ-003).
- `AsyncNotifier` performs no authorization of its own (FR-N10) — the sole capability gate is the receiving turn's normal tool-policy evaluation, unaffected by whether the turn was `Notify`-triggered or user-triggered.

## Clarifications

### 2026-07-04

- Q: Must the async wake mechanism be forward-compatible with a future Goals/loop feature without requiring a later refactor? -> A: Yes — this is FR-N4/FR-N5/FR-N6 (structured event payload + observer registration + panic isolation), added specifically in response to this requirement.
- Q: Where should the `AsyncNotifier` type live — `pkg/agent`, `pkg/bus`, or a new `pkg/notify`? -> A: `pkg/agent`, next to the `asyncCallback` logic it's extracted from.
- Q: Is observer registration process-wide (one instance at boot) or per-session? -> A: Process-wide — a single `AsyncNotifier` instance held on `AgentLoop`, matching how `d0f65482`'s `ApprovalGrantStore` is scoped to the loop rather than per-connection. A future Goals engine is expected to register itself once, the same way.
- Q: Does `Metadata` need any concrete fields populated now? -> A: No — it stays genuinely empty at this stage; `bash` and the unified subagent tool don't need anything beyond `Channel`/`ChatID`/`AgentID`/`SourceKind`/`Content` today. Fields get added only when a real producer needs them.
- Q: Should observer registration be a public API today? -> A: No — kept unexported/internal within `pkg/agent` for now. The mechanism exists and is fully tested (FR-N5/FR-N6), but nothing outside the package calls it yet; exporting it is deferred until the Goals feature is actually built.

### 2026-07-04 — 7-reviewer gate fixes (post-grill)

- Q: Is the 50,000-byte truncation cap actually correct? -> A: No — verified wrong. The real cap, per `pkg/tools/session.go:14`'s `maxOutputBufferSize`, is 1,048,576 bytes (1MB). Corrected in FR-N8 and the truncation dataset (MAJ-001).
- Q: Should `AsyncNotifier` itself perform authorization, or is next-turn tool-policy evaluation sufficient? -> A: The latter — stated explicitly now as FR-N10 with a required test, not left as an implicit assumption tested only in a holdout scenario (MAJ-002).
- Q: Does ADR-036 §3.3's `Notify` signature need amending to match FR-N4's structured event? -> A: Yes — amended in place (same precedent as the ADR-035 amendment) to show `AsyncNotifyEvent`/`Notify(ctx, event)` instead of four positional strings (MAJ-004).
- Q: Does `delegate`'s async completion need its own `SourceKind` requirement in this spec? -> A: Yes — added as FR-N11, closing the one-directional traceability gap `agent-delegation-spec.md`'s MIN-004 flagged.
