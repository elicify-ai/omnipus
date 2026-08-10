# Spec — ADR-059 work items W1–W5 (delegation observability)

- **Status:** Draft — amended after grill #1 (A1) and grill #2 (A2). Body edits applied, not merely described.
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

**A1-4 (C-04) — SCOPE: progress reaches only streaming-capable providers. Named and accepted.**
*Corrected by grill #2: there are **three** implementers, not two — `HTTPProvider`
(`pkg/providers/http_provider.go:51`) is the third, as ADR-059 W1 already said. The original A1-4
enumeration walked only `pkg/providers/*/` subdirectories and missed the top-level file.*
**`anthropic`, `openai_compat` and `HTTPProvider` implement `ChatStream`;** `azure`,
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

## 1.0b Amendment A2 — post re-grill #2 (BINDING; supersedes A1 where they conflict)

Grill #2 returned **BLOCK** (5 CRITICAL). Report:
[adr-059-delegation-observability-spec-review-pass2.md](adr-059-delegation-observability-spec-review-pass2.md).

**A2-1 — A1 described edits that were never applied. THIS IS THE FINDING THAT MATTERS.** Six of A1's
statements were falsifiable claims about this document's own contents (§6 retargeted, §2.2 corrected,
§4 scoped, SC-001 reclassified, §9 mapped, sections added) and only §2.3 had actually been rewritten.
Intent was written in the past tense. **All six edits are now applied to the body** — §2.2, §4, §6,
SC-001/SC-002, FR-009, §9.1, §10.5, §10.6, §10.7 — and A1 should be read as the rationale for them,
not as a claim that they exist.

**A2-2 — A1-4 was wrong on a number. Corrected in place.** There are **three** `ChatStream`
implementers, not two: `HTTPProvider` is the third, exactly as ADR-059 W1 states. A1 contradicted the
ADR it is bound to. The scope conclusion — that Azure, Bedrock and `anthropic_messages` report no
progress — is unaffected and stands.

**A2-3 — chasing A2-2 found a production bug, now fixed.** `anthropicprovider.NewProvider` has one
non-test caller: `ClaudeProvider`, which held its delegate as an unexported non-embedded field and so
did **not** satisfy `StreamingProvider`. Proven by compile check. Every Anthropic install silently
took the non-streaming path, making the native emitter — and the stale-accessor fix applied to it —
unreachable. `streaming_compliance_test.go` asserted the *inner* type, which is why it stayed green.
Fixed: `ClaudeProvider` forwards `ChatStream`, and the assertions now name the factory-returned type
first. **Rule recorded: assert the type the factory returns, never the delegate it wraps.**

**A2-4 — TDD #2 cannot fail after W1.** Once the callback is a parameter, "a hook replacing the
options map cannot silence progress" is true by construction. It is kept as documentation and
labelled as such in §10.7; it must not be counted as coverage.

**Still open, deliberately:** the W5 gate has one genuinely open question (§6 item 3) and still lacks a
pre-agreed alternative shape if the answer is bad. That is the next decision, not a defect to paper
over.

## 1.0c Amendment A3 — post grill #3 (BINDING; supersedes A1/A2 where they conflict)

Grill #3 **verified A2's claim**: all nine body edits physically exist, so pass #2's central finding
(an amendment describing edits that were never made) is closed and did not recur. It then found four
new blockers.

**A3-1 — the implementer count has been wrong three passes running: 2 → 3 → 4. It is FOUR.**
`openai_compat.Provider`, `anthropicprovider.Provider`, `HTTPProvider`, and — added by `45b01b14`,
one commit before this spec was amended — **`ClaudeProvider`**, the wrapper the factory actually
returns for Anthropic. Each previous count was published as "verified by enumeration" and each was
wrong: the first walked only `pkg/providers/*/` and missed the top-level file; the second was correct
when written and went stale when *this branch's own fix* added a fourth. **Rule: re-derive the count
at the commit being described, never carry it forward.** §2.1, §2.2 and §10.6 are corrected.
ADR-059 §3/§4/W6 still say three and must be amended to match.

**A3-2 — §6's "known precedent" was inverted.** It claimed no SPA component special-cases ADR-058's
structured payload. `GenericToolCall.tsx` does, via a bespoke `DelegationFailureDisplay` whose comment
says it exists to avoid showing "a raw JSON blob". The house response to a structured payload is to
*write a renderer*, so W5 should assume one is required. Corrected in §6.

**A3-3 — the gate was assessing the wrong renderer, again, in the other direction.** `write_file` is a
REGISTERED tool UI: **live** chat renders `FileWriteConfirm` (which shows no reason at all);
`GenericToolCall` renders it only on **replay**. A1-3 corrected FileWriteConfirm→GenericToolCall and
over-corrected. Both must be assessed. Consequence: **US-5 AC-1 is already false live, today, before
W5** — and W5's real risk is live/replay divergence in a codebase that deliberately maintains parity.

**A3-4 — §9's wiring-test row said "must pass unchanged" for a third pass**, contradicting §2.3,
SC-002 and TDD #1. Corrected, and the struck FR-009's row is struck.

**Still open and NOT fixed here** (recorded rather than silently carried): the W5 gate still has no
pre-agreed alternative shape — pass #2 proposed putting the discriminator in `ToolCall.result`, which
is `additionalProperties: true` and needs no contract change; FR-007 does not yet carry A1-5's
prefix-positioning rule as a requirement; the compile-break site count is stale again; and the spec
references no issue numbers while §10.5 says two gaps are "tracked separately".

## 1.0d Amendment A4 — operator decisions (BINDING)

Asked and answered 2026-08-10, after three grill passes. These settle every question the grills left
open.

**A4-1 — W1/W2/W4 are implemented in this branch, now.** Not deferred to a follow-up.

**A4-2 — W5 takes SHAPE B, decided up front rather than after the SPA check.** The discriminator goes
in `ToolCall.result` (`additionalProperties: true`, so **no contract change**), and `ForLLM` keeps its
existing sentence unchanged. This is strictly better than shape A on the evidence: `GenericToolCall`
already looks in `result` for structured payloads, ADR-058's own payload lands there too, and nothing
a human reads changes — so the "JSON blob in chat" risk is avoided rather than tested for.
**§6's gate is therefore closed by decision, not by investigation.** A1-5's prefix-positioning rule
becomes moot for shape B (a structured field cannot be severed by text truncation) and is retained in
FR-007 only as a constraint on any future shape-A revival.

**A4-3 — the pre-existing live-UX gap is fixed as part of W5.** A failed write currently renders
`write_file · a.svg · 1.2 KB · Failed` with no reason at all (grill #3, C3-03). W5 therefore becomes a
frontend change as well as a backend one: `FileWriteConfirm` must surface the reason. This is a scope
increase over the ADR, taken deliberately — W5's purpose is making write failures legible, and
stopping at the backend would leave the human-facing half of that unfixed.

**A4-4 — AC-06 gets a real guard, not a waiver.** The progress handler runs synchronously inside the
provider's SSE read loop with no `recover()`, so a panicking handler unwinds through the parser and
kills the turn — strictly worse than the blindness being fixed. A deferred recover at the call site
plus a test that a panicking handler leaves the turn alive.

---

## 1.0e Amendment A5 — A4-2 REVERSED; W5 takes shape A (BINDING)

Raised and decided 2026-08-10, during W5 implementation. **This supersedes A4-2.**

**Shape B is not implementable as worded, and the evidence I gave for it was wrong.** A4-2 said the
discriminator goes in `ToolCall.result` while `ForLLM` keeps its existing sentence. Tracing the
plumbing shows there is no carrier for that:

- The model reads `ToolResult.ForLLM` and nothing else (`ContentForLLM`, `pkg/tools/result.go`).
- The wire `result` and `error` fields are both derived from that same string — live
  (`pkg/gateway/websocket.go`, `liveResult = p.Result`) and persisted (`pkg/agent/loop.go`,
  `tcRecord.Error = truncateRunes(contentForLLM, …)`).
- ADR-058's payload reaches `result` **precisely because its `ForLLM` IS the JSON**
  (`DelegationDeniedResult`). A4-2 cited that precedent as support for leaving `ForLLM` alone; the
  precedent says the opposite. That was a factual error in the option as it was put to the operator.

The only way to satisfy shape B literally is a new Go field on `ToolResult` — exactly what W3 deleted
for being unreachable by a language model. So shape B collapses into the defect it was meant to
replace.

**W5 therefore takes shape A**, ADR-058's actual mechanism: the refusal's `ForLLM` becomes a JSON
object carrying a fixed `error` discriminator plus the existing sentence as its `reason`, and the
gateway parses it into a structured `result` the same way it parses delegation denials.

Two facts that make this the cheap option rather than the expensive one:

- A4-3 already commits to changing `FileWriteConfirm`. The SPA work that made shape B look cheaper is
  happening either way, so B's sole advantage was never real.
- `FileWriteConfirm`'s renderer receives `{ args, status }` only and never sees the result
  (`src/components/chat/tools/FileWriteConfirm.tsx`) — confirming §6 item 3's correction. It shows a
  bare `Failed` with no reason today, before W5 touches anything.

**A1-5's prefix-positioning rule is live again**, not moot: under shape A the payload is text and
truncation can sever it. FR-007 keeps that clause as a binding constraint, and TDD #9 (survives
truncation at 2000 runes) is a required test rather than a shape-B leftover.

**§6's gate is closed by this amendment.** Items 1–2: the payload rides in `result` (already a
permissive `oneOf`) and in `error` (a plain string) — the frame shape does not change. Item 3: both
renderers are addressed — a new contract variant gives the SPA a typed shape, and `FileWriteConfirm`
gains the reason. Item 4: yes, the 5-step contract pipeline is required, because a hand-written Go
struct for a wire payload is forbidden by Constraint #8.

---

## 1. Overview

ADR-059 fixes a defect where a delegated worker generating a large tool-call argument was
indistinguishable from a hung one, and an orchestrator killed healthy workers on that ambiguity. The
**behaviour** is already delivered and green: progress is produced, recorded and surfaced through
`delegate status`.

This spec covers the ADR's remaining work items, which are a **migration of working code plus one
behavioural change** — not a new feature. That framing drives the whole verification approach: the
dominant risk is regressing something that currently works, not failing to build something new.

| Item | What | Status |
|---|---|---|
| **W1** | Move the progress callback from the `options` map to a `ChatStream` parameter | **Done** — `86b10dc7`. Four implementers (`openai_compat`, `anthropic`, `HTTPProvider`, `ClaudeProvider`), one caller, ~13 test sites |
| **W2** | Delete the now-dead options-map plumbing | **Done** — `86b10dc7`. `OnToolCallProgressKey` and `ToolCallProgressFromOptions` are gone; one way to pass this, not two |
| **W3** | Remove `ToolResult.Reason` | **Done** (§1.2) |
| **W4** | Correct `ToolCallProgress.Index`'s doc comment | **Done** — `86b10dc7` |
| **W5** | Structured discriminator in `write_file`'s refusal, per ADR-058 | **Done** — `fb14c632`, shape A per A5. Includes the gateway live/replay parity fix and the A4-3 SPA change |
| **A4-4** | `recover()` guard on the progress handler | **Done** — `6411a342`, with a production-caller test in each provider |

**§6's gate is closed** — by A5, not by a separate investigation. **W6 no longer blocks anything.**

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
| `ClaudeProvider.ChatStream` | `pkg/providers/claude_provider.go` | W1: implementer — **the type the factory returns for Anthropic** |
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
| `ChatStream` signature | **MEDIUM** | **4** in-tree implementers + 1 caller, all compile-enforced. **Breaks out-of-tree providers — loudly, at build time.** That is the intended trade. |
| Delete options-map plumbing | LOW | Nothing outside `protocoltypes` and `loop.go` after W1 |
| `Index` doc comment | NONE | No production consumer exists |
| `write_file` result text | **MEDIUM–HIGH** | Every agent that reads a `write_file` refusal; the SPA's `GenericToolCall.tsx` (which renders result/error content — `FileWriteConfirm.tsx` never reads the result, see A1-3); the persisted `ToolCall.error`; the replay path |

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

- When a **streaming-capable** provider streams tool-call arguments, the system reports forward
  progress to the caller. On a non-streaming provider (Azure, Bedrock, `anthropic_messages`) or an
  agent with multiple fallback candidates, **no progress is reported at all** — see A1-4.
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

## 6. W5 gate — contract impact — **CLOSED** (A5, 2026-08-10)

**This gate no longer blocks anything.** It is retained because the questions it asked were the right
ones and two of the answers are load-bearing. Answers, as delivered in `fb14c632`:

1. **No.** The payload rides in `result` (a permissive `oneOf`) and in `error` (a plain string, no
   `additionalProperties` constraint). Nothing invalidates. A new `FileExistsRefusal` variant was
   added to the `result` union so the SPA gets a typed shape rather than an anonymous object.
2. **No.** The frame keeps its shape; only the JSON *type* of `result` changes, from string to
   object, which the union already permits.
3. **Both renderers were changed, and the answer to the original question was the discovery that
   mattered.** `GenericToolCall` would indeed have shown a JSON blob, so the gateway now lifts the
   prose reason into `error` and hands the parsed object to `result` — on the LIVE path and, newly,
   on REPLAY. Replay was the real hazard: it reconstructs from a persisted raw JSON string, so
   without the same parse a page reload would replace a sentence with a blob. Separately,
   `FileWriteConfirm` was fixed under A4-3: it showed no reason at all, before W5 touched anything.
4. **Yes.** A hand-written Go struct for a wire payload is forbidden by Constraint #8, so the schema
   went through the 5-step pipeline and the generated artifacts are committed with it.

The original questions, as posed:

1. Does the changed refusal text flow into `ToolCall.error` (a field on a schema with
   `additionalProperties: false`), and does it still validate?
2. Does the live `tool_call_result` frame change shape?
3. **(A3-3 — corrected again.) `write_file` is a REGISTERED tool UI, so LIVE chat renders
   `FileWriteConfirm`, which shows `write_file · a.svg · 1.2 KB · Failed` and **no reason at all**;
   `GenericToolCall` renders `write_file` only on REPLAY. So US-5 AC-1 is already false live, today,
   before W5 — and the real W5 risk is LIVE/REPLAY DIVERGENCE in a codebase that deliberately
   maintains that parity. Both renderers must be assessed, not one.**
   Does `GenericToolCall.tsx` render the refusal as a sentence today, and
   would it show a JSON blob after the change?** (Not `FileWriteConfirm.tsx` — verified: it reads only
   `args` and `status`, never the result, so gating on it returns a false all-clear.) This is the user-visible question and the most likely reason to
   choose a different shape.
4. Is the 5-step contract pipeline (`scripts/gen-contracts.sh` + `make verify-contracts`) required?

**Correction (A3-2): the precedent argues the OTHER way.** An earlier draft of this gate claimed no
SPA component special-cases ADR-058's structured payload. It does: `GenericToolCall.tsx` detects
`error: "delegation_denied"` and renders it through a bespoke `DelegationFailureDisplay`, whose own
comment says it exists to show "a distinct, human-readable block instead of a raw JSON blob". So the
house response to a structured payload is *to write a renderer for it*, not to let it through as
text. Note also that it lands in `result` (an object), whereas W5's discriminator lands in `error`
(a string) which is rendered verbatim with no handling. **W5 should therefore assume a renderer is
required unless proven otherwise.**

---

## 7. Requirements

- **FR-001**: The streaming interface MUST carry the progress callback as a per-call parameter.
- **FR-002**: All in-tree streaming providers MUST accept it and emit progress on argument deltas.
- **FR-003**: Progress MUST remain observable after a hook replaces request options.
- **FR-004**: The options-map mechanism MUST be removed once FR-001 is satisfied.
- **FR-005**: Progress MUST NOT carry argument content.
- **FR-006**: `Index`'s documentation MUST state it is provider-scoped and not a tool-call ordinal.
- **FR-007**: A precondition refusal MUST be machine-distinguishable from an I/O failure in the
  result the calling agent reads, **and the discriminator MUST be prefix-positioned** — it has to
  begin the string, so neither the 2000-rune persisted truncation nor the live frame bound can sever
  or remove it (A1-5). A discriminator that survives only on short paths fails silently on long ones.
- **FR-008**: A refusal MUST remain human-readable where it is displayed.
- ~~**FR-009**: No test listed in §2.3 may change to accommodate the migration.~~ **WITHDRAWN (A1-1)**
  — it was unsatisfiable: W1 breaks the wiring test's compile and W2 deletes the function it asserts
  through. Replaced by §2.3's FROZEN/REWRITTEN split.

## 8. Success criteria

- **SC-001** *(review obligation, not a runnable check — A1-6)*: removing the progress parameter from
  any in-tree provider must produce a build failure. Nothing in CI deletes a parameter, so this is
  asserted by `streaming_compliance_test.go` plus reviewer attention.
- **SC-002** *(restated — FR-009 is withdrawn, A1-1)*: every FROZEN test in §2.3 passes unchanged, and
  every REWRITTEN test carries the replacement assertion named there.
- **SC-003**: A tree-wide search for the options-map key returns no production matches.
- **SC-004**: `go-test` and `go-race` are green on the worker at the final commit.
- **SC-005**: W5 is not merged until §6 is answered in an appended amendment.

## 9. Traceability

| Requirement | Story | Scenario | Test |
|---|---|---|---|
| FR-001, FR-002 | US-1 | Progress survives the transport migration | provider progress tests (§2.3) + new per-provider parameter test |
| FR-003 | US-1 | Hook replacing options cannot silence progress | `toolcall_progress_wiring_test.go` — **REWRITTEN per §2.3**, not unchanged; and per A2-4 it cannot fail after W1, so it is documentation, not coverage |
| FR-001 | US-1 | Provider missing the parameter does not build | compile-time; `streaming_compliance_test.go` |
| FR-004 | US-2 | No superseded plumbing remains | new grep-assertion test |
| FR-005 | US-1 | — | existing progress tests assert byte counts only |
| FR-006 | US-3 | — | doc assertion test on the field comment |
| FR-007 | US-4 | A refusal is classifiable, a failure is not | new `write_file` discriminator test |
| FR-008 | US-5 | The refusal stays readable | SPA render check (§6 item 3) |
| ~~FR-009~~ | — | — | **struck — withdrawn by A1-1** |

### 9.1 ADR-059 acceptance criteria — mapping (A1-7)

The ADR's own §8 criteria, each mapped or explicitly marked as an open gap. Three were unmapped; they
are gaps in coverage, not gaps in the matrix.

| ADR AC | Covered by | Status |
|---|---|---|
| AC-01 loop supplies a non-nil handler | TDD #1 | mapped |
| AC-02 survives hook replacing options | TDD #2 | mapped, but see the note — after W1 it cannot fail |
| AC-03 progress fires before the call completes, **per implementing provider** | TDD #3 | **GAP — no Anthropic test exists** |
| AC-04 a mid-generation child reads as progressing | `delegate_toolcall_progress_test.go` (FROZEN) | mapped, **scoped to streaming-capable providers only (A1-4)** |
| AC-05 race-free under concurrent write/read | `toolcall_progress_race_test.go` (FROZEN) covers one turn; TDD #4 covers two | **PARTIAL — cross-turn half untested** |
| AC-06 a panicking handler does not kill the turn | TDD #5 | **GAP — no test, no waiver** |
| AC-07 W5 plumbing test (bar waived for D4) | TDD #8, #9 | mapped, W5 only |

## 10. Ambiguity audit

| Ambiguity | Likely agent assumption | Question to resolve |
|---|---|---|
| Does W1 also change the non-streaming `Chat` path? | No — only `ChatStream` | Confirm progress is streaming-only, so agents with fallback candidates (which take `Chat`) get no progress. **This is a real coverage hole flagged in review.** |
| Exact JSON shape for W5 | Mirror ADR-058's `{"error":…,"message":…}` | Confirm field names before implementing |
| Should the refusal keep `IsError: true`? | Yes — the content was not written | Confirm |
| Does W2 delete the `protocoltypes` file entirely? | No — `ToolCallProgress` stays | Confirm the type survives, only the map plumbing goes |

## 10.5 Out of scope

- **Making non-streaming providers report progress.** Azure, Bedrock and `anthropic_messages` have no
  `ChatStream`, and an agent with multiple fallback candidates takes the non-streaming path. Named,
  accepted, and to be recorded in ADR-059 §4 (A1-4). Not addressed here.
- **Teaching `delegate status` to distinguish "quiet" from "cannot report"** — tracked as **#614**. On a non-reporting
  install an orchestrator still sees silence, which the incident says it reads as "hung". Real gap.
- **`Is3P` (external-CLI) children**, which bypass the progress path entirely (covered by #614).
- **Wiring `tokens_in`** — unrelated, separately tracked.

## 10.6 Definition of done

W1/W2/W4 are done when **all** hold:

1. `ChatStream` carries the progress parameter and **all four** implementers accept it:
   `openai_compat.Provider`, `anthropicprovider.Provider`, `HTTPProvider`, **and
   `ClaudeProvider`** (added by `45b01b14` — see A3-1; the wrapper the factory actually
   returns for Anthropic).
2. Every FROZEN test in §2.3 passes unchanged; every REWRITTEN test carries its named replacement.
3. A tree-wide search for the options-map key returns no production matches.
4. `go-test` and `go-race` are green on the CI worker at the final commit, parsed from the log rather
   than the SSH exit code, with no `FLAKE (passed isolated)` lines.
5. ADR-059's AC-01…AC-07 are each mapped in §9 to a test, or carry an explicit written waiver.

W5 is done when, additionally, §6's open question is answered in an appended amendment **and** the
answer is acted on — including choosing a different shape if the SPA renders a blob.

## 10.7 TDD plan

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | Stub provider receives a non-nil `onProgress` **parameter** | Unit (pkg/agent) | US-1 / FR-001 | Rewrite of the wiring test; replaces the options-map assertion |
| 2 | Progress survives a `BeforeLLM` hook replacing options | Unit (pkg/agent) | US-1 / FR-003 | **After W1 this can no longer fail by construction** — keep it as documentation, or delete it and say why. Do not present it as coverage. |
| 3 | Tool-args-only stream emits monotonic progress — **per implementing provider** | Unit (providers) | US-1 / FR-002, ADR-059 AC-03 | **Anthropic has no such test today.** Required, not optional. |
| 4 | Two concurrent sub-turns on one agent record progress independently | Integration (pkg/agent) | ADR-059 AC-05 | The cross-turn half is currently untested — it is the exact hazard that rewrote D1 |
| 5 | A panicking progress handler does not kill the turn | Unit (providers) | ADR-059 AC-06 | Or an explicit written waiver |
| 6 | Options-map key absent from the tree | Unit | US-2 / FR-004 | Guards W2 |
| 7 | `Index` doc states the provider-scoped contract | Unit | US-3 / FR-006 | Cheap; guards W4 |
| 8 | Refusal carries a discriminator; I/O failure does not | Unit (pkg/tools) | US-4 / FR-007 | W5 only |
| 9 | Discriminator survives truncation at 2000 runes | Unit | US-4 / FR-007, A1-5 | Prefix-positioned, so the cut cannot remove it |

**Stub resistance.** Tests 1 and 3 must drive a real provider call path, not assert on a struct a test
built. ADR-059 §8's inherited bar applies verbatim: *a green test that does not exercise a production
caller does not satisfy this ADR.* Test 3 in particular must exercise the type the **factory returns**
— asserting an inner delegate is precisely how the Anthropic emitter shipped unreachable.

## 11. Holdout evaluation scenarios *(not for development; excluded from §9)*

1. Delegate a task that writes a large file; confirm `delegate status` shows advancing progress mid-generation.
2. Delegate two tasks to the same agent concurrently; confirm each reports its own progress.
3. Ask a worker to write a file a sibling already wrote; confirm it reports "already done" rather than a failure.
4. Configure an agent with multiple fallback candidates; observe whether progress appears (expected: no — see §10).
5. Trigger a write refusal and read the chat surface as a human; confirm it is legible.
6. Reopen the session; confirm the refusal reads the same as it did live.
7. Point an agent at an unwritable location; confirm it is reported as a failure, not a refusal.
