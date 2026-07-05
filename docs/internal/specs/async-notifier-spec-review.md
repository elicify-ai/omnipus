# Adversarial Review: Async Wake Mechanism (`AsyncNotifier`)

**Spec reviewed**: `docs/internal/specs/async-notifier-spec.md`
**Review date**: 2026-07-04
**Verdict**: REVISE

## Executive Summary

The extraction-preserves-behavior half of this spec (User Story 2) is careful and well-tested. But the truncation cap this spec's own dataset asserts as "matching `exec`'s existing convention" (50,000 bytes) does not match the actual existing convention in code (a 1MB marker in `pkg/tools/session.go`) — a concrete, verifiable factual error that will produce a real behavioral inconsistency (double-truncation or silent over-truncation) if implemented as written. The forward-looking observer mechanism (User Story 3) is speculative generality built for a feature with no spec of its own yet, and the spec never states who is authorized to call `Notify` or validate its `SourceKind`/`AgentID` fields — the one scenario addressing notification spoofing is explicitly excluded from the required test plan.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 4 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **11** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] Truncation cap (50,000 bytes) contradicts the actual existing convention it claims to match (1MB)

- **Lens**: Incorrectness
- **Affected section**: Dataset: Content truncation, row 3 ("cap (match `exec`'s existing background-output cap)"); FR-N8
- **Description**: The dataset table states the truncation cap is "50000 | matches `exec`'s existing background-output cap | matches existing convention, not a new number invented for this spec." Verified against the codebase: `pkg/tools/session.go:16` defines `outputTruncateMarker = "\n... [output truncated, exceeded 1MB]\n"` — the actual existing background-output truncation ceiling for exec/session-managed processes is **1MB (1,048,576 bytes)**, not 50,000. The `50000` constant that does exist in the codebase (`pkg/tools/web.go:34`, `defaultMaxChars = 50000`) belongs to an entirely different tool (web-fetch content truncation) with no relationship to background shell-session output.
- **Impact**: If `AsyncNotifier.Notify`'s truncation is implemented per this dataset (50,000 bytes), a `bash run_in_background` job whose output is, say, 200KB — well under `session.go`'s own 1MB truncation ceiling, and therefore NOT truncated when the agent polls it mid-run via `action: read` — would be silently truncated to 4% of its length the moment it's delivered via the completion notification. The agent (and, per the spec's own concern in User Story 1, the operator) would see a materially different, much-shorter view of the same job's output depending on whether they polled it live or received it via the wake notification — a real, observable inconsistency, not a cosmetic one. This directly undermines FR-N8's own goal ("truncate... matching the truncation convention already used by `exec`'s existing background-output handling") since the number chosen doesn't match that convention at all.
- **Recommendation**: Correct FR-N8 and the dataset to use the actual existing cap (verify the precise byte count backing `session.go`'s "1MB" marker text — likely `1024*1024` — and cite the exact constant name/location, mirroring this spec's otherwise-careful sourcing convention elsewhere), or, if a smaller cap is genuinely desired for conversational-message-length reasons (1MB is a lot to inject as a synthetic chat message), state that explicitly as a *new*, deliberately smaller number — not as "matching an existing convention" it doesn't match.

---

#### [MAJ-002] No requirement governs who may call `Notify` or validates its `SourceKind`/`AgentID` against the actual calling context

- **Lens**: Insecurity (STRIDE: Spoofing, Elevation of Privilege)
- **Affected section**: FR-N1–N9 (no authorization/validation requirement anywhere); Evaluation Scenario "An agent explicitly denied `bash` cannot receive a spoofed wake notification granting it capability" (holdout only)
- **Description**: `AsyncNotifier` is explicitly designed as a shared, process-wide, open extension point ("a single `AsyncNotifier` instance held on `AgentLoop`") that any current or future in-process caller can invoke with an arbitrary `SourceKind`, `Channel`, `ChatID`, `AgentID`, and an entirely open `Metadata map[string]any` (FR-N4). Nothing in the Functional Requirements, BDD Scenarios, or TDD Plan states which callers are authorized to invoke `Notify`, or requires validating that a caller's claimed `SourceKind`/`AgentID` actually matches the tool/agent context it's being invoked from. The one scenario that even raises this concern — "An agent explicitly denied `bash` cannot receive a spoofed wake notification granting it capability" — is explicitly placed in the "Evaluation Scenarios (Holdout)" section, which the spec's own header states is "for post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix."
- **Impact**: This is a real, if narrow, elevation-of-privilege surface: the mitigation the holdout scenario describes ("the next turn's tool policy is still evaluated normally... the notification cannot itself grant capability") is a property of a *different* part of the system (the tool-policy compositor, not `AsyncNotifier`), asserted here but never actually required or tested as part of *this* spec's own deliverable. If a future producer (the very thing FR-N4/N5/N6 are built to accommodate without code changes) has a bug that lets it be invoked with an attacker-influenced `SourceKind`/`Content` (e.g., content reflecting user-controlled data from an MCP tool result), the synthetic inbound message this creates is trusted by the agent loop as a legitimate system-originated turn-trigger with no stated provenance check.
- **Recommendation**: Promote the spoofing scenario from the holdout section into the required BDD/TDD plan with a real test, OR, if the actual mitigating property genuinely lives entirely in the tool-policy layer (a legitimate design choice — defense doesn't have to live in every layer), add an explicit Functional Requirement stating this division of responsibility outright (e.g., "FR-N10: `AsyncNotifier` performs no authorization; the receiving turn's tool-policy evaluation is the sole capability gate, and this MUST be documented at the `Notify` call site") so the property is a stated, verified contract rather than an implicit assumption tested only optionally.

---

#### [MAJ-003] Observer/extensibility mechanism (FR-N4–N6) is speculative generality for a feature with no spec, built and tested now on faith that its shape will fit

- **Lens**: Overcomplexity
- **Affected section**: User Story 3; FR-N4, FR-N5, FR-N6
- **Description**: This spec's own Assumptions section concedes: "The Goals feature itself is out of scope; this spec is judged complete when a hypothetical, throwaway test observer can be attached with zero changes to any existing producer." The observer-registration pattern, the open `Metadata map[string]any` bag, and the panic-isolation guarantee are all built and given dedicated tests (`TestAsyncNotifier_RegisterObserver_ReceivesEvent`, `_NoObserver_BehaviorUnchanged`, `_ObserverPanic_Recovered`) for a consumer (Goals) that has no requirements document, no BDD scenarios, and no confirmed interface needs of its own yet. Per this review's Lens 8 test (CPX-04: "requirements must solve the current problem, not hypothetical future ones"): is it known, for instance, whether Goals needs synchronous or asynchronous observer invocation? Whether an observer needs to be able to veto/short-circuit the bus-publish (explicitly ruled out here, FR-N5: "independent of and without affecting the bus-publish outcome" — a decision made on Goals' behalf before Goals is designed)? Whether `Metadata`'s shape (a bag of `any`) will actually suit whatever Goals turns out to need, or whether it will need a stronger-typed extension when actually built?
- **Impact**: This is explicitly operator-directed scope ("operator direction: must support a future Goals/loop feature... without requiring a later refactor" — the spec header states this plainly, and the Clarifications confirm it was a deliberate, informed decision), so this finding does not carry the weight of an author inventing unwarranted complexity unprompted. But the adversarial obligation stands regardless of who ordered it: building and testing an abstraction for an undesigned consumer carries real risk that the extension point is *wrong* in a way that isn't discoverable until Goals is actually specced — at which point the "avoid a later refactor" premise this design exists to satisfy may fail anyway, while still having paid the complexity cost now (an unexported-for-now observer API, an open Metadata bag with no consumer, three dedicated tests for behavior nothing currently exercises).
- **Recommendation**: Given this is a deliberate operator call, not correcting it, but recommend explicitly time-boxing or flagging it: note in the spec that FR-N4–N6's shape is provisional and MUST be revisited (not merely reused as-is) once the Goals feature is actually specced, to avoid treating "we already built the extension point" as a reason to skip re-evaluating whether that shape actually fits Goals' real needs.

---

#### [MAJ-004] ADR-036 §3.3's committed interface signature contradicts this spec's FR-N4 — an implementer reading only the (authoritative, per project convention) ADR would build the wrong shape

- **Lens**: Inconsistency
- **Affected section**: FR-N4 (requires "a structured event... carrying at minimum `Channel`, `ChatID`, `AgentID`, `SourceKind`, `Content`, and an open `Metadata map[string]any` field"); cross-referenced against `ADR-036-consolidate-shell-and-subagent-tools.md` §3.3's `Decision` code block: `Notify(ctx context.Context, channel, chatID, sourceLabel, content string) error` (four positional string parameters, no `AgentID`, no `Metadata`, no struct)
- **Description**: This project's own conventions (per its CLAUDE.md) treat `docs/internal/architecture/ADR-*.md` as "accepted decisions" — i.e., the authoritative record of what was decided, with only code itself outranking it. ADR-036's §3.3 shows a concrete Go interface as "the Decision," using four positional string arguments. This spec (`async-notifier-spec.md`), written as a *companion* to that same ADR and explicitly building on it, requires a materially different, richer shape (a structured event struct, an `AgentID` field the ADR's signature has no room for, an open `Metadata` bag) to satisfy FR-N4/N5/N6 — the very requirements needed to support observers meaningfully. The ADR itself was never amended to reflect this evolution, even though this same repository has an established, active precedent for doing exactly that (a sibling ADR, ADR-035, was amended in-place the same day for a different but comparable reason — "amend ADR-035 with the 7-reviewer gate's findings and fixes").
- **Impact**: An implementer who reads ADR-036 as their primary reference (reasonable, since it's the higher-authority document per this project's own doc hierarchy, and it's shorter/more skimmable than the full spec) will build `Notify` with the ADR's literal four-positional-string signature — which cannot carry `AgentID` or `Metadata` at all, making FR-N4/N5's observer requirements unsatisfiable without a second, later signature change (exactly the "later refactor" this spec's whole User-Story-3 premise exists to avoid).
- **Recommendation**: Amend ADR-036 §3.3's code block to show the actual, final structured-event signature this spec requires, mirroring the precedent already set by the ADR-035 amendment. Do this before implementation, not as a documentation cleanup afterward — the ADR is the one artifact plausibly read in isolation from the spec.

---

### MINOR Findings

#### [MIN-001] "Two producers... at nearly the same instant" (Edge Cases) states no-drop but doesn't test concurrent-write safety of the observer list itself

- **Lens**: Incompleteness (concurrency)
- **Affected section**: Edge Cases; FR-N5
- **Description**: The spec requires observers receive every `Notify` call and that concurrent producer calls aren't dropped relative to each other, but doesn't require (or test) that concurrent `Notify` calls from two goroutines are safe against the *observer-registration* data structure itself (e.g., a data race between an in-flight `Notify` iterating the observer list and a hypothetical future `RegisterObserver` call happening concurrently — even though registration is stated to be process-wide/boot-time today, nothing prevents a later caller from registering at runtime).
- **Recommendation**: Add a concurrency test asserting `Notify` and observer registration are safe to call concurrently (e.g., via `go test -race`), even though today's only registration call site is at boot.

---

#### [MIN-002] Observer registration being "kept unexported... for now" (Assumptions) isn't reconciled with how its own tests will access it

- **Lens**: Ambiguity
- **Affected section**: Assumptions; Test Implementation Order #4–6
- **Description**: If the registration method is unexported (package-private to `pkg/agent`), the tests exercising it (`TestAsyncNotifier_RegisterObserver_ReceivesEvent`, etc.) must live inside `package agent` (not `agent_test` as a black-box external test package) to have access. Nothing in the Test-Driven Development Plan states this constraint, which matters for how these tests get organized alongside the rest of `pkg/agent`'s test files (some of which may use `package agent_test` per Go convention for black-box testing).
- **Recommendation**: State explicitly that these tests must be internal (`package agent`), not black-box, given the unexported API surface.

---

#### [MIN-003] "Content payload is very large" edge case addresses truncation but not delivery latency for a large payload

- **Lens**: Incompleteness
- **Affected section**: Edge Cases; SC-004 ("surfaces its result as a new turn within 5 seconds of process exit")
- **Description**: SC-004's 5-second latency budget is tested against "a `bash run_in_background=true` command that completes within 2 seconds" — a small/fast case. Given the truncation dataset (once MAJ-001 is corrected to a much larger real cap) discusses payloads potentially in the megabyte range, there's no test confirming the 5-second SC-004 budget still holds when `Notify` is processing/truncating a large payload, nor any stated allowance for it not holding in that case.
- **Recommendation**: Either state SC-004 applies regardless of payload size up to the truncation cap, or scope it explicitly to "small" payloads and add a separate, more lenient latency expectation for large ones.

---

#### [MIN-004] "Gateway restarts while a background command is still running" (holdout) correctly scopes non-persistence as accepted, but this isn't cross-referenced from the main FR list

- **Lens**: Inconsistency
- **Affected section**: Evaluation Scenarios (holdout); Assumptions ("No persistence/durability guarantee is required...")
- **Description**: The Assumptions section does state this is accepted (no new gap), which is good — but the corresponding holdout scenario's own text says "If this turns out to be a real operator expectation, it's a gap for a follow-up spec, not a silent failure to paper over now," which is slightly in tension with the Assumptions section's more definitive "this matches today's `spawn` behavior... not a new gap introduced here." One section treats it as settled; the other leaves the door open as a possible future gap. Not contradictory, but worth aligning the tone.
- **Recommendation**: Pick one framing consistently — either "settled, accepted limitation" throughout, or "acknowledged risk, deferred" throughout.

---

### Observations

#### [OBS-001] `SourceKind` values (`"bash"`, `"delegate"`/`"spawn"`) aren't enumerated as a closed set anywhere

- **Lens**: Ambiguity
- **Suggestion**: FR-N4 says `SourceKind` is part of the structured event but doesn't define whether it's a free-form string (any producer can invent any label) or a closed enum. Given `bash-tool-spec.md`'s FR-B9 and this spec's FR-N9 both hardcode `"bash"` as a literal, a shared constant/enum (rather than each producer independently stringifying its own name) would prevent drift (e.g., one producer using `"Bash"` or `"exec"` post-rename).

#### [OBS-002] The "no persistence" decision (Explicit Non-Behaviors) doesn't address what happens to an in-flight `Notify` call if the process crashes mid-publish

- **Lens**: Incompleteness
- **Suggestion**: Given `bus.PublishInbound`'s own delivery guarantees are stated as "unchanged by this spec," this is likely inherited, accepted behavior — worth a one-line confirmation rather than silence, for consistency with how thoroughly other edge cases are stated.

#### [OBS-003] FR-N7's "empty `Channel` or `ChatID`" validation doesn't address whitespace-only or otherwise-invalid-but-non-empty values

- **Lens**: Ambiguity
- **Suggestion**: A `Channel: " "` (whitespace) or a `ChatID` containing control characters would pass an empty-string check but could still be semantically invalid/ambiguous for a downstream bus consumer. Minor, but worth a trim-and-check note if this is meant to be a robust boundary guard rather than a literal empty-string check only.

---

## Structural Integrity (Variant A: Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1: 2, US-2: 2, US-3: 3 |
| Every acceptance scenario has BDD scenarios | PASS | |
| Every BDD scenario has `Traces to:` reference | PASS | |
| Every BDD scenario has a test in TDD plan | PASS | 10 tests enumerated, ordered |
| Every FR appears in traceability matrix | PASS | FR-N1–FR-N9 all present |
| Every BDD scenario in traceability matrix | PASS | |
| Test datasets cover boundaries/edges/errors | PARTIAL | Truncation cap itself is wrong (MAJ-001); spoofing/authorization untested (MAJ-002) |
| Regression impact addressed | PASS | Two existing behaviors explicitly carried forward unmodified |
| Success criteria are measurable | PASS | SC-001–SC-004 all quantified |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Notification authorization/spoofing | Only in holdout, not required TDD plan | User Story 1/3 (MAJ-002) |
| Concurrent observer registration + Notify | No race-safety test for the observer list itself | User Story 3 (MIN-001) |
| Large-payload latency | SC-004 only tested against a fast/small case | User Story 1 (MIN-003) |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| Content truncation | The stated cap (50000) doesn't match the real convention (1MB) | Correct per MAJ-001 |
| Notify destination validation | Whitespace-only / control-character values | Add per OBS-003 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `Notify` call site | **risk** | ok | ok | ok | ok | **risk** | No caller-authorization/`SourceKind` validation (MAJ-002) — spoofing scenario is holdout-only, not required |
| Observer registration (`RegisterObserver`) | ok | ok | ok | ok | ok | ok | Unexported today, low exposure; panic-isolation well-specified |
| `bus.PublishInbound` (as depended-upon) | ok | ok | ok | ok | ok | ok | Unchanged, out of scope for this spec correctly |
| Truncated content payload | ok | ok | ok | ok | ok | ok | Truncation itself well-motivated; only the specific number is wrong (MAJ-001, not a STRIDE category but flagged for completeness) |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. What is the actual byte constant behind `session.go`'s "1MB" truncation marker — is it exactly `1024*1024`, or an approximation? This needs to be pinned down precisely before FR-N8 can cite a correct number (MAJ-001).
2. Should `AsyncNotifier` itself perform any authorization, or is the tool-policy-evaluation-on-next-turn mitigation considered sufficient and worth stating as an explicit, tested contract rather than an implicit assumption (MAJ-002)?
3. Is FR-N4/N5/N6's shape considered final, or provisional pending the actual Goals spec — and if provisional, who is responsible for revisiting it later, given "avoid a later refactor" was the explicit goal (MAJ-003)?
4. Should ADR-036 §3.3 be amended now to show the structured-event signature, given this project's own precedent (ADR-035) for in-place ADR amendment (MAJ-004)?
5. Does `SourceKind` need to be a closed enum shared across `bash-tool-spec.md`, `agent-delegation-spec.md`, and this spec, to prevent three independently-authored producers from drifting on the exact string used for the same concept (OBS-001)?

---

## Verdict Rationale

No CRITICAL findings, but MAJ-001 is a concrete, easily-triggered factual error that will cause the shipped truncation behavior to diverge from the spec's own stated goal ("matching `exec`'s existing convention") by more than 20x — this alone forces a REVISE. MAJ-002's authorization gap is a real if narrow security concern given `AsyncNotifier`'s explicitly open, process-wide, multi-producer design, and it deserves a stated, tested contract rather than an implicit assumption relegated to an optional holdout scenario. MAJ-004's ADR/spec signature mismatch is a straightforward inconsistency that risks a wrong implementation if the ADR (this project's stated higher-authority document for decisions) is read on its own.

MAJ-003 (the speculative-generality observation) does not by itself justify blocking, since it reflects a deliberate, informed operator decision rather than an author's unchecked scope creep — but it's included per this review's obligation to always apply Lens 8, and the recommendation (flag it as provisional, revisit when Goals is actually specced) costs nothing to add now and reduces the risk of the abstraction calcifying incorrectly.

### Recommended Next Actions

- [ ] Correct the truncation cap to match the actual `session.go` convention (MAJ-001)
- [ ] Add an explicit authorization/provenance requirement for `Notify`, or promote the spoofing scenario out of the holdout section (MAJ-002)
- [ ] Note FR-N4–N6 as provisional, to be revisited once Goals is specced (MAJ-003)
- [ ] Amend ADR-036 §3.3 to show the structured-event signature (MAJ-004)
