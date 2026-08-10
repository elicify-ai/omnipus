# Spec — ADR-059 work items W1–W5 (delegation observability)

- **Status:** Draft (pre-grill)
- **Date:** 2026-08-10
- **Authority:** [ADR-059](../architecture/ADR-059-delegation-observability.md) (Accepted 2026-08-10). Where this spec and the ADR disagree, the ADR wins.
- **Also binding:** [ADR-058](../architecture/ADR-058-tool-denial-semantics.md) §3/§7 (W5's convention and the contract-impact discipline it demands), Constraint #8 (contract-first), Constraint #7 (no "pre-existing" closure path).
- **Branch:** `fix/uat-delegation-rootcauses` (operator decision: continue here, not a follow-up branch).
- **Operator decisions already taken** (asked and answered before drafting):
  - **W1 shape** — change `StreamingProvider.ChatStream`'s signature directly. Not a sibling method, not an options struct.
  - **W5** — trace the contract impact first (§7/W6), then implement; do not implement blind.
  - **W3** — verify non-use, then remove immediately. **Already executed** — see §1.2.
  - **Branch** — continue in this branch.

---

## 1.0 Amendment A1 — post spec-grill #1 (BINDING; overrides any conflict below)

Grill #1 returned **BLOCK** (4 CRITICAL / 8 MAJOR). Review:
[adr-059-delegation-observability-spec-review.md](adr-059-delegation-observability-spec-review.md).
Each item below was verified against source before being accepted.

**A1-1 (C-01) — §2.3 and FR-009 were self-contradictory. FR-009 is WITHDRAWN.**
`pkg/agent/toolcall_progress_wiring_test.go` cannot "pass unchanged": it stubs `ChatStream` (W1
changes that signature) and asserts *through* `ToolCallProgressFromOptions` (W2 deletes it). Its whole
premise is the options map. `openai_compat/toolcall_progress_test.go` likewise contains
`TestToolCallProgressFromOptions`. §2.3 is replaced by two lists — **FROZEN** (behaviour that must not
change) and **REWRITTEN** (tests whose mechanism changes, each with its replacement assertion named).
A test may move from FROZEN to REWRITTEN only by amendment.

**A1-2 (C-02) — the compile break is ~13 sites, not 4**, two of them in `pkg/gateway`, which cannot be
validated locally per CLAUDE.md. W1 therefore requires a CI-worker round-trip, and the work plan must
budget for it rather than discovering it mid-migration.

**A1-3 (C-03) — the W5 gate pointed at the wrong component. Corrected.** Verified: 
`src/components/chat/tools/FileWriteConfirm.tsx` **never reads the tool result** — it derives only a
status ('error'/'success'). Gating on it would have produced a **false all-clear**. §6 item 3 now
targets `src/components/chat/tools/GenericToolCall.tsx` (which takes `result?: unknown` and renders
result content, including sentinels), the persisted `ToolCall.error`, and the replay path. §2.2's
dependent list is corrected likewise.

**A1-4 (C-04) — SCOPE: progress reaches only two providers. Named and accepted.** Verified by
enumeration: **only `anthropic` and `openai_compat` implement `ChatStream`.** `azure`,
`bedrock` and `anthropic_messages` do not, so those installs take the non-streaming `Chat` path and
report **no progress at all** — as does any agent with more than one fallback candidate
(`loop.go` routes those through `Chat`). Bedrock is first-class per ADR-053.
Consequences, binding:
  - §4's first behavioural bullet is scoped to streaming-capable providers only.
  - ADR-059's AC-04 is satisfiable only on those providers.
  - **`delegate status` must not let an orchestrator read silence as "hung" on a
    non-reporting install.** Until that is addressed, the gap is a named, accepted limitation and
    MUST be recorded in ADR-059 §4 (out of scope) rather than left implicit. Tracked, not fixed here.

**A1-5 (M-08) — W5 must prefix-position the discriminator.** Both the persisted path and the live
frame truncate at 2000 runes and append a marker. A discriminator that lands after the cut, or is
severed by it, is unparseable — so FR-007 would fail silently on long paths. The structured payload
MUST begin the string (as ADR-058's does), so truncation can never remove it.

**A1-6 — SC-001 is not a test.** "Delete the parameter and the build must fail" is a **review
obligation**, not something CI executes. Reclassified; the compile-time guarantee is asserted by
`streaming_compliance_test.go` plus reviewer attention, not by a runnable check.

**A1-7 — ADR-059's AC-01…AC-07 are now mapped in §9.** Three were unmapped and are genuine gaps, not
oversights in the matrix: **AC-03 has no Anthropic emission test at all** (the exact regression
`streaming_compliance_test.go`'s own comment warns about), **AC-05's cross-turn half is untested**
(the race test uses one `turnState`, not two concurrent sub-turns — the very hazard that rewrote D1),
and **AC-06 has neither test nor waiver**. These are recorded as required work, not deferred silently.

**A1-8 — missing house sections added**: out-of-scope, definition of done, and a TDD plan.
§6's four gate questions are also reduced to the one that is genuinely open: items 1 and 4 were
answerable from the tree (`ToolCall.error` is `type: string`, so JSON-inside-a-string always
validates, and `additionalProperties: false` only bites on *added* fields, which W5 does not add).

---

## 1. Overview

ADR-059 fixes a defect where a delegated worker generating a large tool-call argument was
indistinguishable from a hung one, and an orchestrator killed healthy workers on that ambiguity. The
**behaviour** is already delivered and green: progress is produced, recorded and surfaced through
`delegate status`.

This spec covers the ADR's remaining work items, which are a **migration of working code plus one
behavioural change** — not a new feature. That framing drives the whole verification approach: the
dominant risk is regressing something that currently works, not failing to build something new.

| Item | What | Risk |
|---|---|---|
| **W1** | Move the progress callback from the `options` map to a `ChatStream` parameter | Migration — must not regress |
| **W2** | Delete the now-dead options-map plumbing | Migration — must not regress |
| **W3** | Remove `ToolResult.Reason` | **Done** (§1.2) |
| **W4** | Correct `ToolCallProgress.Index`'s doc comment | Documentation only |
| **W5** | Structured discriminator in `write_file`'s refusal, per ADR-058 | **Behavioural** — gated on W6 |

### 1.1 Why W1 at all, when the delivered mechanism works

The options map is **correctly per-call scoped**, which is the property D1 actually cares about, so
the shipped code is not wrong — it is untyped. The cost of leaving it: a provider that never reads
the key is indistinguishable from one that does (no compile error, no runtime error, no signal), the
accessor must accept two type shapes and return `nil` on anything else, and a `BeforeLLM` hook can
replace the map wholesale and silently drop the callback — which the delivered code already has to
defend against explicitly.

A parameter removes all four at once and is compile-enforced for every implementer.

### 1.2 W3 — already executed

`ToolResult.Reason`, `ResultReason`, `ReasonAlreadyExists` and `WithReason` are removed, along with
the `write_file` call site and `write_file_reason_test.go`. Verified before removal: no production Go
code read the field.

One nuance found during that check and recorded here because it changes the *rule*, not this
instance: `*tools.ToolResult` is serialised into the **PostToolUse hook payload**
(`pkg/agent/hooks.go`), so the field was visible to user hook scripts. It was safe to delete only
because it shipped on this unreleased branch. **Any future removal of a `ToolResult` field after a
release is a hook-contract break requiring deprecation, not a deletion.**

---

## 2. Existing codebase context

### 2.1 Symbols involved

| Symbol | File | Role in this work |
|---|---|---|
| `StreamingProvider.ChatStream` | `pkg/providers/types.go` | **W1: signature changes** |
| `openai_compat.Provider.ChatStream` | `pkg/providers/openai_compat/provider.go` | W1: implementer |
| `anthropicprovider.Provider.ChatStream` | `pkg/providers/anthropic/provider.go` | W1: implementer |
| `HTTPProvider.ChatStream` | `pkg/providers/http_provider.go` | W1: implementer (delegates through) |
| `callLLM` closure | `pkg/agent/loop.go` | W1: sole caller; W2: injection removed here |
| `OnToolCallProgressKey`, `ToolCallProgressFromOptions` | `pkg/providers/protocoltypes/progress.go` | **W2: deleted** |
| `ToolCallProgress.Index` | `pkg/providers/protocoltypes/progress.go` | W4: doc corrected |
| `WriteFileTool.Execute` overwrite guard | `pkg/tools/filesystem.go` | W5: emits the discriminator |
| `turnState.recordToolCallProgress` / `clearToolCallProgress` | `pkg/agent/turn.go` | Unchanged — consumer side |
| `AgentLoop.ProgressForSession` | `pkg/agent/turn.go` | Unchanged |
| `DelegateTool.delegateStatusExtra` | `pkg/tools/delegate.go` | Unchanged |

### 2.2 Impact assessment

| Change | Risk | Direct dependents |
|---|---|---|
| `ChatStream` signature | **MEDIUM** | 3 in-tree implementers + 1 caller, all compile-enforced. **Breaks out-of-tree providers — loudly, at build time.** That is the intended trade. |
| Delete options-map plumbing | LOW | Nothing outside `protocoltypes` and `loop.go` after W1 |
| `Index` doc comment | NONE | No production consumer exists |
| `write_file` result text | **MEDIUM–HIGH** | Every agent that reads a `write_file` refusal; the SPA's `FileWriteConfirm.tsx`; the persisted `ToolCall.error` |

### 2.3 Test disposition (replaces the withdrawn "unchanged" list — see A1-1)

**FROZEN — behaviour must not change; these tests must pass without edits:**

| Test | Pins |
|---|---|
| `pkg/agent/toolcall_progress_race_test.go` | the record is race-free |
| `pkg/agent/toolcall_progress_lifetime_test.go` | cleared at round end; finished turns excluded |
| `pkg/tools/delegate_toolcall_progress_test.go` | `status` renders / omits the progress line |
| `pkg/providers/streaming_compliance_test.go` | every provider still satisfies the interface |

**REWRITTEN — mechanism changes, so the test must too. Each replacement assertion is named here so
the rewrite cannot quietly weaken it:**

| Test | Why it must change | Replacement assertion |
|---|---|---|
| `pkg/agent/toolcall_progress_wiring_test.go` | stubs `ChatStream` (W1) and asserts through `ToolCallProgressFromOptions` (W2 deletes it) | the stub receives a **non-nil `onProgress` parameter**, and still does so after a `BeforeLLM` hook replaces the options |
| `pkg/providers/openai_compat/toolcall_progress_test.go` | contains `TestToolCallProgressFromOptions`, testing a deleted function | delete that one case; the tool-args-only stream case stays FROZEN in substance |

**Anything not listed in either table must be discovered by compiling, not by assumption** — A1-2
puts that at roughly 13 sites, two in `pkg/gateway`.

---

## 3. User stories & acceptance criteria

### US-1 — Compile-enforced progress plumbing — **P0**

As a maintainer adding or modifying an LLM provider, I want the progress callback to be part of the
streaming call's signature, so that a provider which fails to handle it cannot compile — rather than
silently reporting nothing, which is the failure mode this whole ADR exists to eliminate.

**Why P0:** it is the ADR's primary decision, and it gets harder to change once any consumer treats
the map key as load-bearing.

**Independent test:** delete the `onProgress` parameter from one implementer; the build must fail.

1. **Given** a provider implementing the streaming interface, **When** the interface gains the
   progress parameter, **Then** that provider fails to build until it accepts the parameter.
2. **Given** a turn that streams tool-call arguments, **When** the provider emits argument deltas,
   **Then** the caller receives progress events — same observable behaviour as before the migration.
3. **Given** a `BeforeLLM` hook that replaces the request options wholesale, **When** the turn runs,
   **Then** progress reporting is unaffected — because it no longer travels in options.

### US-2 — No dead plumbing left behind — **P0**

As a maintainer, I want the superseded options-map mechanism removed once the parameter exists, so
nobody wires a second, untyped path to the same signal.

**Independent test:** searching the tree for the options key returns nothing.

1. **Given** W1 is complete, **When** the codebase is searched for the progress options key,
   **Then** no definition, producer, or accessor remains.
2. **Given** the removal, **When** the full suite runs, **Then** all progress behaviour tests listed
   in §2.3 still pass.

### US-3 — An honest `Index` contract — **P1**

As a consumer of progress events, I want `Index` documented as provider-scoped, so I do not write
code treating it as a tool-call ordinal and get wrong answers on one provider.

**Why P1:** no consumer exists yet, so nothing is broken today — but the comment actively invites the
mistake, and the two providers genuinely disagree.

1. **Given** the two providers number `Index` differently, **When** a maintainer reads its doc,
   **Then** the doc states it is provider-defined, stream-scoped, and not an ordinal over tool calls.

### US-4 — A refusal a worker can classify — **P1**

As a delegated worker, I want "this file already exists" to be distinguishable from "the write
failed" without matching on wording, so I can report *task already satisfied* to my parent instead of
reporting a failure.

**Why P1:** real, but the prose already names the condition, so the gain is stable classification
rather than new information — and it carries the only genuine behavioural risk in this spec.

**Independent test:** the refusal payload contains a machine-checkable marker; a genuine I/O failure
does not.

1. **Given** a file already exists and overwrite is not requested, **When** a worker calls the write
   tool, **Then** the result it reads contains a discriminator identifying a precondition refusal.
2. **Given** a genuine write failure (e.g. an unwritable location), **When** the tool runs,
   **Then** the result carries no precondition-refusal discriminator.
3. **Given** either outcome, **When** the transcript is later reopened, **Then** the same
   discriminator is present in the persisted record.

### US-5 — W5 does not degrade what a human sees — **P0 (gate on US-4)**

As an operator watching chat, I want a refusal to remain readable, so the fix for agents does not
turn a sentence into a JSON blob in the UI.

**Why P0:** ADR-058 §7 flagged exactly this class, and this is the one item that can visibly regress
the product.

1. **Given** the changed refusal text, **When** it renders in the chat surface, **Then** a human sees
   a readable message, not a raw structured payload.
2. **Given** the changed refusal text, **When** it crosses the wire, **Then** it validates against
   the existing schema with no contract drift.

### Edge cases

- A provider that implements streaming but never emits tool-call arguments (text-only response).
- A response containing multiple tool calls — `ArgsBytes` resets per call.
- A refusal whose path contains characters requiring escaping in a structured payload.
- A very long refusal (the persisted side truncates at 2000 runes).
- A hook that inspects the tool result payload (see §1.2).

---

## 4. Behavioral contract

- When a provider streams tool-call arguments, the system reports forward progress to the caller.
- When a provider is given no progress callback, the system reports nothing and does not fail.
- When request options are replaced by a hook, progress reporting is unaffected.
- When a write is refused because the file exists, the result identifies it as a precondition refusal.
- When a write fails for any other reason, the result does not claim a precondition refusal.
- When a refusal is displayed to a human, it remains readable prose.

### Explicit non-behaviours

- The system must **not** build a channel from the tool to the orchestrator — the worker reports
  upward through the existing delegation path (ADR-059 D4).
- Progress must **not** carry argument content, only byte counts — arguments are large and may be
  sensitive.
- W5 must **not** be implemented before its contract impact is traced (§6) — ADR-058 §7 warns this
  class causes a silent SPA-edge validation drop.
- The migration must **not** change any observable progress behaviour; it changes only the transport.

---

## 5. BDD scenarios

```gherkin
Scenario: Progress survives the transport migration
  Traces to: US-1, AC-2
  Given an agent whose provider streams a tool call with multi-part arguments
  When the turn runs
  Then the caller receives more than one progress event
  And each reports more accumulated bytes than the last
```

```gherkin
Scenario: A hook replacing request options cannot silence progress
  Traces to: US-1, AC-3
  Given a BeforeLLM hook that replaces the request options with an empty set
  When the turn runs
  Then progress events are still received
```

```gherkin
Scenario: A provider missing the progress parameter does not build
  Traces to: US-1, AC-1
  Given a provider type that implements the streaming interface
  When the progress parameter is removed from its method
  Then compilation fails
```

```gherkin
Scenario: No superseded plumbing remains
  Traces to: US-2, AC-1
  Given the migration is complete
  When the tree is searched for the options-map progress key
  Then there are no matches outside historical records
```

```gherkin
Scenario Outline: A refusal is classifiable, a failure is not
  Traces to: US-4, AC-1 and AC-2
  Given a target path in state <state>
  When a worker writes to it without requesting overwrite
  Then the result <expectation> a precondition-refusal discriminator

  Examples:
    | state                          | expectation   |
    | an existing file               | contains      |
    | a directory that cannot be written | does not contain |
```

```gherkin
Scenario: The refusal stays readable to a human
  Traces to: US-5, AC-1
  Given a write refused because the file exists
  When the result renders in the chat surface
  Then a human-readable message is shown
  And no raw structured payload is displayed
```

---

## 6. W5 gate — contract impact (must complete before W5 is implemented)

W5 is **blocked** until each of these is answered in writing and appended to this spec as an
amendment:

1. Does the changed refusal text flow into `ToolCall.error` (a field on a schema with
   `additionalProperties: false`), and does it still validate?
2. Does the live `tool_call_result` frame change shape?
3. **Does the SPA's `FileWriteConfirm.tsx` render the refusal as a sentence today, and would it show
   a JSON blob after the change?** This is the user-visible question and the most likely reason to
   choose a different shape.
4. Is the 5-step contract pipeline (`scripts/gen-contracts.sh` + `make verify-contracts`) required?

**Known precedent, to be confirmed rather than assumed:** ADR-058 already ships structured JSON
inside denial text today, and no SPA component appears to special-case it — suggesting such payloads
already render as text. That is a lead, not an answer.

---

## 7. Requirements

- **FR-001**: The streaming interface MUST carry the progress callback as a per-call parameter.
- **FR-002**: All in-tree streaming providers MUST accept it and emit progress on argument deltas.
- **FR-003**: Progress MUST remain observable after a hook replaces request options.
- **FR-004**: The options-map mechanism MUST be removed once FR-001 is satisfied.
- **FR-005**: Progress MUST NOT carry argument content.
- **FR-006**: `Index`'s documentation MUST state it is provider-scoped and not a tool-call ordinal.
- **FR-007**: A precondition refusal MUST be machine-distinguishable from an I/O failure in the
  result the calling agent reads.
- **FR-008**: A refusal MUST remain human-readable where it is displayed.
- **FR-009**: No test listed in §2.3 may change to accommodate the migration.

## 8. Success criteria

- **SC-001**: Removing the progress parameter from any in-tree provider produces a build failure.
- **SC-002**: Every test in §2.3 passes unchanged after W1 and W2.
- **SC-003**: A tree-wide search for the options-map key returns no production matches.
- **SC-004**: `go-test` and `go-race` are green on the worker at the final commit.
- **SC-005**: W5 is not merged until §6 is answered in an appended amendment.

## 9. Traceability

| Requirement | Story | Scenario | Test |
|---|---|---|---|
| FR-001, FR-002 | US-1 | Progress survives the transport migration | provider progress tests (§2.3) + new per-provider parameter test |
| FR-003 | US-1 | Hook replacing options cannot silence progress | `toolcall_progress_wiring_test.go` (existing, must pass unchanged) |
| FR-001 | US-1 | Provider missing the parameter does not build | compile-time; `streaming_compliance_test.go` |
| FR-004 | US-2 | No superseded plumbing remains | new grep-assertion test |
| FR-005 | US-1 | — | existing progress tests assert byte counts only |
| FR-006 | US-3 | — | doc assertion test on the field comment |
| FR-007 | US-4 | A refusal is classifiable, a failure is not | new `write_file` discriminator test |
| FR-008 | US-5 | The refusal stays readable | SPA render check (§6 item 3) |
| FR-009 | US-2 | — | SC-002 |

## 10. Ambiguity audit

| Ambiguity | Likely agent assumption | Question to resolve |
|---|---|---|
| Does W1 also change the non-streaming `Chat` path? | No — only `ChatStream` | Confirm progress is streaming-only, so agents with fallback candidates (which take `Chat`) get no progress. **This is a real coverage hole flagged in review.** |
| Exact JSON shape for W5 | Mirror ADR-058's `{"error":…,"message":…}` | Confirm field names before implementing |
| Should the refusal keep `IsError: true`? | Yes — the content was not written | Confirm |
| Does W2 delete the `protocoltypes` file entirely? | No — `ToolCallProgress` stays | Confirm the type survives, only the map plumbing goes |

## 11. Holdout evaluation scenarios *(not for development; excluded from §9)*

1. Delegate a task that writes a large file; confirm `delegate status` shows advancing progress mid-generation.
2. Delegate two tasks to the same agent concurrently; confirm each reports its own progress.
3. Ask a worker to write a file a sibling already wrote; confirm it reports "already done" rather than a failure.
4. Configure an agent with multiple fallback candidates; observe whether progress appears (expected: no — see §10).
5. Trigger a write refusal and read the chat surface as a human; confirm it is legible.
6. Reopen the session; confirm the refusal reads the same as it did live.
7. Point an agent at an unwritable location; confirm it is reported as a failure, not a refusal.
