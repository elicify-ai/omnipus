# Adversarial Review: Media Handling & Provider-Error Translation

**Spec reviewed**: `docs/internal/specs/media-handling-error-translation-spec.md`
**Review date**: 2026-07-21
**Verdict**: BLOCK

## Executive Summary

The spec is well-organised and its ADR-derivation is disciplined, but its central
architectural claim — that all user-facing error translation happens at a "single
seam" at `pkg/agent/loop.go:7277-7300` — is **factually false**. The agent loop
has at least **six** other `EventKindError` emission sites (plus one in
`subturn.go`) that will continue to surface raw provider errors after this spec
ships, so Success Criterion SC-002 ("no raw provider JSON reaches the SPA render
path") cannot be satisfied as written. A second critical defect: the spec
proposes to "generalize `isImageInputRejection`" into a media-rejection
classifier, but the existing function **does not match the incident's actual xAI
error string** — generalising it without acknowledging the gap risks shipping
"the fix" while the original bug still reproduces. A third critical defect: the
`LLMError` schema integration with the existing `ReplayErrorFrame` (which has
`additionalProperties: false` and a pre-existing `message` field) is
underspecified, so two implementers could produce incompatible shapes.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 11 |
| MINOR | 14 |
| OBSERVATION | 5 |
| **Total** | **33** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The "single translation seam" claim is false — raw errors will still leak from six other emit sites

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: "Wire & Engine Design → Single translation seam" (line 170); ADR-051 RD5 (line 98); FR-007 (line 460); SC-002 (line 472); "Decisions Locked" D5 (line 22); Impact Assessment "HIGH-risk callout" (line 64)
- **Description**: The spec asserts (Behavioural Invariant #4) that
  `pkg/agent/loop.go:7277-7300` is "the correct and only translation site" and
  that "the translated `message` feeds `appendErrorTranscript`, `ErrorPayload.Message`,
  and (via D6) the live WS frame." But the actual codebase emits `EventKindError`
  from **at least seven** distinct sites, each of which persists an un-translated
  `err.Error()` into the transcript and forwards it on replay:

  | Site | Stage | Persisted today |
  |---|---|---|
  | `loop.go:3494–3504` | `"hooks"` (AfterLLM hook failure) | raw `err.Error()` |
  | `loop.go:6186–6199` | `"model_switch"` (fallback model failed) | raw `switchFailMsg` built from `err` |
  | `loop.go:7279–7291` | `"runTurn"` (the spec's target) | `"LLM call failed after retries: %s"` |
  | `loop.go:7479–7485` | `"runTurn"` (second error block — empty-response exhaustion) | raw `err.Error()` |
  | `loop.go:8403–8413` | `"runTurn"` (third error block) | raw `err.Error()` |
  | `loop.go:8525–8532` | bottom-of-loop generic catch-all | raw `err.Error()` |
  | `subturn.go:847–855` | `"spawnSubTurn"` (delegated child failure) | raw `targetAgentFallbackWarning` |

  `subturn.go:847-855` is particularly load-bearing: a sub-turn that fails
  against its target provider emits `EventKindError` with raw text today, and
  nothing in this spec brings it under the translation seam.

- **Impact**: SC-002 ("No test asserts raw provider JSON/body reaches the SPA
  render path") will be violated by any provider error that flows through hooks,
  model-switch fallback, the empty-response retry-exhausted path, the bottom-of-loop
  generic path, or any sub-turn. The user will continue to see raw provider JSON
  in exactly the kind of incident this spec exists to close. The "single seam"
  claim is the spec's load-bearing reliability argument; it does not hold against
  the code.
- **Recommendation**: Either (a) **prove** the seam claim by enumerating every
  `EventKindError` and `appendErrorTranscript` call site in `pkg/agent/` and
  showing each is either translated or refactored to route through the seam
  (a 1-line `grep -rn "appendErrorTranscript\|EventKindError" pkg/agent/` will
  produce the list), or (b) move the translation one layer down — translate at
  the WS forwarder (`pkg/gateway/websocket.go`) and the replay frame
  decoder, so every emit site is covered by construction regardless of where the
  error originated. Option (b) is simpler and matches the "single seam" intent.
  Either way, add a BDD scenario + E2E test that drives each non-target emit site
  (sub-turn, hooks, model_switch) and asserts no raw text leaks.

---

#### [CRIT-002] Current `isImageInputRejection` does NOT match the incident's error string; "generalize" is underspecified

- **Lens**: Incorrectness
- **Affected section**: ADR-051 RD2 (line 81); spec FR-002 (line 455); spec "Decisions Locked" D2 (line 19); TDD Test #3/#4 (lines 415-416); "Regression Test Requirements" `TestIsImageRejectionRejection_GeneralizedClassifier` (line 446)
- **Description**: The spec describes `isImageInputRejection` as a "one-off"
  to be "generalized into the media-rejection classifier." The actual function
  (`pkg/agent/loop_media.go:286-322`) is narrower than the spec acknowledges. It:
  1. **Requires** the literal substring `"image"`.
  2. **Excludes** anything containing `"invalid image"`, `"corrupt"`, `"too large"`,
     `"moderation"`, `"content policy"`, `"safety"`, or `"unable to process"`.
  3. Matches only four *capability-absence* phrases: `"image input"`,
     `"no image support"`, `"not support image"`, `"image not supported"`.

  The incident's actual xAI error was:
  > `Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.`

  That string (a) contains `"image"` ✓, (b) matches none of the exclusions ✓,
  (c) matches **none** of the four capability phrases ✗. So
  `isImageInputRejection(incidentErr)` returns **false** today. The current
  synthesis path does not fire for the incident; the user gets the raw error.

  The spec's "generalize" therefore must invent a **new** pattern class —
  *format-rejection* ("does not contain a valid JPG/PNG/…") — distinct from the
  existing *capability-absence* class. The spec does not call this out; it talks
  about "generalizing" as if the existing patterns are a sound base.
- **Impact**: An implementer following the spec literally ("extend the existing
  function with more phrases") could ship a classifier that still fails to catch
  the original incident. The TDD test `TestIsImageRejectionRejection_GeneralizedClassifier`
  has no pinned incident-reproducer input, so it could pass without exercising
  the actual regression.
- **Recommendation**:
  1. Add an explicit test-dataset row whose input is **the exact incident
     string** (`Downloaded response does not contain a valid JPG, PNG, WebP, or
     ICO image.`), classified as `media_unsupported`.
  2. Add a second row for the dual class — `"image input not supported"` — to
     lock in the existing capability-absence behaviour under the new classifier.
  3. Rewrite FR-002 / D2 to acknowledge two distinct pattern classes
     (capability-absence vs format-rejection) and pin both.
  4. Add an invariant test: "any string classified by the OLD
     `isImageInputRejection` is also classified by the NEW classifier"
     (behaviour-preserving generalisation).

---

#### [CRIT-003] `LLMError` integration with the existing `ReplayErrorFrame` schema is underspecified

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: "Wire & Engine Design → `LLMError` schema" (lines 238-250); "Integration Boundaries → `contracts/asyncapi.yaml`" (lines 227-232); FR-011 (line 464); ADR-051 "Integration Contract" (lines 191-202)
- **Description**: The spec adds `LLMError` as a new schema file and says it is
  "referenced from `asyncapi.yaml`." It does **not** say **how**:
  - As a new top-level **live** message (e.g. `ErrorFrame`) distinct from
    `ReplayErrorFrame`?
  - As fields embedded **inside** `ReplayErrorFrame` (alongside the existing
    `message` / `kind` / `payload`)?
  - As a replacement for `ReplayErrorFrame.message`?
  - Nested inside `ReplayErrorFrame.payload` (which today is `type: object,
    additionalProperties: false`)?

  The existing `ReplayErrorFrame` (`contracts/asyncapi.yaml:1895-1935`) declares
  `additionalProperties: false` and already has a top-level `message` field, plus
  a `payload` object whose own `additionalProperties: false` constraint means
  adding `code` / `retryable` / `detail` requires a schema change either way.
  Three implementers reading this spec would produce three different shapes.

  Compounding this: D6 introduces a **live** `EventKindError` frame. The existing
  live forwarder only handles `ReplayErrorFrame` on the replay channel; a live
  error needs either a new message on the live `chat` channel or a new channel
  message altogether. The spec does not name the message.
- **Impact**: Contract drift on day one. The regenerated zod schema will not
  match what backend emits; SPA edge drops the frame; user sees nothing
  (worse than today). `make verify-contracts` may pass (each piece is internally
  valid) while the system is externally broken.
- **Recommendation**: Pin the integration explicitly. Recommended shape:
  1. Define `LLMError` as a component schema (the spec already does this).
  2. Extend `ReplayErrorFrame.payload` (not the top-level frame) with
     `llm_error: { $ref: '#/components/schemas/LLMError' }` — preserves the
     existing `message`/`kind` fields for backward compat and isolates the new
     typed shape.
  3. Add a **new** live message `ErrorFrame` on the existing `chat` channel
     whose payload **is** `LLMError` (so live and replay share a component
     schema but not a frame type, matching ADR-051's "live == replay" intent at
     the schema level).
  4. Make the existing top-level `ReplayErrorFrame.message` deprecated but still
     populated (= `llm_error.message`) for one release, so an older SPA still
     renders *something*.
  5. Specify all of the above as a single contract change in PR Slice 2.

---

### MAJOR Findings

#### [MAJ-001] `translateLLMError` cannot reliably read HTTP status from the wrapped `error`

- **Lens**: Infeasibility
- **Affected section**: "Translation function" (lines 254-261); BDD Scenario Outline "Error classification by provider status" (line 380); Test dataset rows 1-7 (lines 432-438)
- **Description**: The spec says classification uses "HTTP status (from
  `HandleErrorResponse`-style structured error where available) + body substring
  patterns." But `HandleErrorResponse` (`pkg/providers/common/common.go:390`)
  returns a **flat `error`** whose `.Error()` is the multi-line string
  `"API request failed:\n  Status: %d\n  Body:   %s"`. There is no structured
  status field exposed to callers; by the time the error reaches the loop seam,
  the status is embedded in prose. The classifier must therefore either
  regex-parse `"Status: (\d+)"` out of the string, or `HandleErrorResponse`
  must be changed to return a typed error (e.g. `*APIError{Status, Body}`).
- **Impact**: An implementer who reads "HTTP status … where available" and writes
  a switch on a status field will not compile — there is no field. The classifier
  silently degrades to body-substring-only matching, which the spec's own dataset
  (rows 1, 2, 5, 6) does not exercise — those rows have only a status and a bare
  `"+ body"` placeholder, so the tests pass against a stub that exposes status
  directly while production fails.
- **Recommendation**: Either (a) change `HandleErrorResponse` (and its callers)
  to return `*APIError{Status int; Body string; Err error}` for the loop path,
  with the existing flat string built only inside `.Error()`; or (b) write the
  classifier's status-extraction as a regex against the wrapped string and add
  a unit test that feeds the **real** `HandleErrorResponse` output (not a stub)
  for every status in the dataset.

---

#### [MAJ-002] `recordRateLimitDenial` already dual-emits; reconciliation with `code=rate_limited` is unspecified

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: ADR-051 references `recordRateLimitDenial` (line 57); spec LLMError taxonomy includes `rate_limited` (line 179); spec Error Flows (line 150); WS forwarder already has `case agent.EventKindRateLimit:` at `websocket.go:3322`
- **Description**: Today, `recordRateLimitDenial` (`pkg/agent/loop.go:1028-1083`)
  emits **both** `EventKindRateLimit` (forwarded live at `websocket.go:3322`) and
  `EventKindError` (not forwarded live). The spec adds `code=rate_limited` to
  `LLMError` but does not say whether:
  - The `EventKindRateLimit` path is subsumed under the new `EventKindError`
    with `code=rate_limited` (deprecating the existing case at L3322), or
  - Both continue to fire and the SPA receives two frames for one rate-limit
    event (duplicated rendering), or
  - The `EventKindError` half of the dual-emit is removed and rate-limit becomes
    `EventKindRateLimit`-only (no `LLMError` translation for it).
- **Impact**: Three plausible outcomes, three different UXs. The "live == replay"
  guarantee (Invariant #6) is at risk: if rate-limit fires both `EventKindRateLimit`
  and `EventKindError` live, the user may see two banners; if only one fires live
  but both are persisted, replay shows two.
- **Recommendation**: Pick one. Recommended: rate-limit becomes
  `code=rate_limited` inside `EventKindError`; the `EventKindRateLimit` case is
  removed; `RateLimitPayload` is preserved on the wire as additional fields on
  the new `LLMError` (or dropped if its fields are unused downstream). Document
  the migration in the spec's Operations section.

---

#### [MAJ-003] BDD Scenario Outline's `413 → context_too_long` mapping is technically wrong

- **Lens**: Incorrectness
- **Affected section**: BDD Scenario Outline "Error classification by provider status" examples table (lines 387-396); Test dataset row 5 (line 437)
- **Description**: The examples table maps HTTP 413 to `context_too_long`. HTTP
  413 is "Payload Too Large" — about overall HTTP request size (byte count of the
  HTTP body), not the model's context window. Providers signal context-window
  overflow inconsistently: OpenAI/OpenRouter typically return 400 with a body
  substring like `"context_length_exceeded"` or `"maximum context length"`;
  Anthropic returns 400 with `"prompt is too long"`; some return 413. Mapping
  status **alone** to `context_too_long` will misclassify every genuine 413
  (e.g., a 5MB JSON body the proxy rejects at the edge) as a context-length
  error and show the user "trim and retry" guidance that won't help.
- **Impact**: Users hitting an HTTP-layer 413 (proxy/CDN body limit) get a
  wrong "conversation is too long" message. Operators can't distinguish via the
  `code` metric.
- **Recommendation**: Replace the `413 → context_too_long` row with body-substring
  rules: `400 + ("context_length_exceeded" | "prompt is too long" | "maximum
  context length") → context_too_long`. Drop 413 from the table or remap to
  `provider_rejected`. Update test dataset row 5 accordingly.

---

#### [MAJ-004] Sub-turn error path (`subturn.go:847-855`) is silently out of scope

- **Lens**: Incompleteness
- **Affected section**: "Existing Codebase Context" (no `subturn.go` entry); "Explicit Non-Behaviors" (lines 200-207 — sub-turn not addressed); Symbol table (lines 36-50 — no sub-turn)
- **Description**: A delegated sub-turn (`spawnSubTurn`) that fails against its
  target provider emits `EventKindError` with raw `targetAgentFallbackWarning`
  text today. This is exactly the D-B failure mode (raw provider text reaches
  the user via replay). The spec never mentions `subturn.go`, never lists it
  under "Explicit Non-Behaviors," and never justifies its exclusion.
- **Impact**: Delegation is a core v0.1 feature. A sub-turn that hits a provider
  400 continues to leak raw text — the same defect class the spec claims to
  close. The "single seam" promise is silently broken for the entire delegation
  surface.
- **Recommendation**: Either (a) bring sub-turn under the seam (refactor
  `subturn.go:847-855` to call `translateLLMError`), or (b) add an Explicit
  Non-Behavior: "v0.1.1 does not translate sub-turn-local errors; sub-turns
  inherit the parent's translation via [mechanism]," with the mechanism named.
  Option (a) is small and strongly recommended.

---

#### [MAJ-005] PR Slice 1 ships D2 retry without D5 translation — the "second rejection terminates" path still surfaces raw errors

- **Lens**: Inconsistency
- **Affected section**: "PR Slicing & Sequencing" (lines 502-506); SC-001 vs SC-002 (lines 471-472); BDD "Second media rejection terminates with translated error" (lines 316-322)
- **Description**: Slice 1 = FR-001/002/004/005 (media), no contract change.
  Slice 2 = FR-006/007/008/009/011 (error translation, contract). But the BDD
  scenario "Second media rejection terminates with translated error" traces to
  **both** FR-002 (Slice 1) and FR-007 (Slice 2). After Slice 1 alone, a second
  rejection terminates the turn via the legacy raw-error path. SC-001 ("no empty
  reply") is satisfied but SC-002 ("no raw provider JSON reaches the SPA") is
  violated by Slice 1 in isolation.
- **Impact**: If Slice 1 and Slice 2 land in separate PRs and Slice 1 ships
  first (even to `release/v0.1.1`), users hitting the double-rejection path
  still see raw JSON — the exact incident, just one step later.
- **Recommendation**: Either (a) merge Slice 1 and Slice 2 into one PR so both
  halves of the incident close atomically; or (b) explicitly relax SC-002 for
  the Slice-1-only window and add a "Slice 1 known issue: second-rejection path
  still surfaces raw text until Slice 2 lands" callout in the spec.

---

#### [MAJ-006] `ErrorPayload` is internal-only (no JSON tags); wire-mapping step is unspecified

- **Lens**: Incompleteness / Ambiguity
- **Affected section**: Symbol table row "ErrorPayload" (line 41); "Translation function" (lines 254-261); "Downgrade-retry wiring" pseudocode (lines 265-277)
- **Description**: The spec says `ErrorPayload` "modifies — Carry translated
  fields for D6." But the actual struct (`pkg/agent/events.go:477`) is
  `type ErrorPayload struct { Stage string; Message string }` with **no JSON tags**
  — it's an internal event payload, never serialised directly to the wire. The
  wire shape comes from `contracts/asyncapi.yaml` → oapi-codegen. The spec never
  specifies the mapping step between the in-process `ErrorPayload` (extended to
  carry `Code`/`Retryable`/`Detail`) and the wire `LLMError`. The WS forwarder
  at `websocket.go:3068-3461` has to translate between these two shapes; the
  spec doesn't say where.
- **Impact**: The forwarder implementer has to invent the mapping. If they
  forget a field, the wire frame is missing data; if they hand-build a wire
  struct, they violate Constraint #8 (`check-no-handwritten-wire-types.sh` is
  enforced in CI).
- **Recommendation**: Add a "Wire mapping" subsection specifying: (a) `ErrorPayload`
  gains unexported-or-exported `Code`, `Retryable`, `Detail` fields (no JSON tags
  needed — internal); (b) the WS forwarder builds the wire `LLMError` from
  `ErrorPayload` via a small `toLLMError(ErrorPayload) generated.LLMError` helper;
  (c) the same helper is used by the replay frame builder.

---

#### [MAJ-007] Open Questions Q1/Q2/Q3 are simultaneously "block ratification" and "default-resolved"

- **Lens**: Inconsistency
- **Affected section**: "Decisions Locked" note (line 27); "Ambiguity Warnings" (lines 512-518)
- **Description**: The spec says "Open operator questions (block 'Locked'
  ratification until answered)" — but D1 (Normalize to PNG) is "Locked" with a
  default that **depends on** Q1 (animated-GIF fidelity), D5/D7 are "Locked"
  with a default that **depends on** Q2 (`detail` on wire), and the D1 format
  coverage **depends on** Q3 (`x/image` scope). The decisions are simultaneously
  locked and contingent on their own open questions.
- **Impact**: An implementer can't tell whether the defaults are shippable. If Q1
  later resolves to "downgrade animated GIFs to a note instead of transcode,"
  D1's "normalize to PNG" must be re-implemented. The spec encodes a default
  while claiming the question that determines the default is unresolved.
- **Recommendation**: Either (a) resolve Q1/Q2/Q3 before ratification (the
  spec's own wording says they block ratification), or (b) drop the "block
  ratification" language and explicitly mark D1/D5/D7 as "default-bound — may
  flip when Q1/Q2/Q3 resolve," with the cost of each flip noted.

---

#### [MAJ-008] "Live == replay" guarantee lacks an idempotency story for the live-then-reload sequence

- **Lens**: Incompleteness
- **Affected section**: Behavioural Contract Invariant #6 (line 172); BDD "Live error renders without a page reload" (lines 372-378)
- **Description**: Invariant #6 says the live frame and `ReplayErrorFrame` carry
  the same `LLMError` so the user "sees the same message before and after
  reload." But the spec doesn't specify what happens when: (1) the live frame
  renders the error in chat, (2) the user reloads, (3) replay re-emits the same
  error. Today's `case 'error'` reducer (`src/store/chat.ts:3259-3266`) creates
  a fresh `ChatMessage` for every error frame — no idempotency key, no dedup.
  Result: two error bubbles for one error.
- **Impact**: A user who reloads after an error sees the error twice. The
  "live == replay" invariant is technically preserved (both show the same text)
  but the UX is broken.
- **Recommendation**: Either (a) mark the live error with the same `entry_id` as
  the eventual replay entry and have the reducer dedup on `entry_id`, or
  (b) accept the duplication and add an Explicit Non-Behavior with rationale.
  (a) is cheaper than it sounds — replay entries already have stable IDs.

---

#### [MAJ-009] FR-004 manifest coverage: only ZIP is tested; tar/gz/tgz are not

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: FR-004 (line 457); BDD "ZIP attachment yields a manifest" (lines 323-329 — ZIP only); TDD Test #5 (line 417 — ZIP only)
- **Description**: FR-004 says "MUST inject a manifest … for `.zip/.tar/.gz/.tgz`
  attachments." The BDD scenario, test, and dataset only cover `.zip`. Tar, gz,
  and tgz (which is tar-with-gzip) are untested. `archive/tar` and `compress/gzip`
  are not currently imported by `docextract` (`extract.go:13` imports only
  `archive/zip`), so the work is net-new and error-prone (gzipped tar requires
  two-layer decompression, tar-with-non-gz is a separate path).
- **Impact**: An implementer could ship ZIP-only and pass every named test while
  violating FR-004 for 3 of 4 formats. tar/gz attachments would continue to hit
  the `default` branch and become bare metadata notes.
- **Recommendation**: Add three BDD scenarios (or a Scenario Outline with a
  `format` column) and three TDD tests covering `.tar`, `.gz` (single-file), and
  `.tgz`. Add a dataset row for each. Confirm `archive/tar` + `compress/gzip`
  imports are listed in the Integration Boundaries section (today only `archive/zip`
  is called out as already-imported).

---

#### [MAJ-010] `case 'error'` reducer has existing kickoff-reject + cancel-ack sub-paths the spec doesn't acknowledge

- **Lens**: Incompleteness
- **Affected section**: Symbol table row "case 'error' reducer" (line 47); FR-010 (line 463)
- **Description**: The existing reducer (`src/store/chat.ts:3086-3266` — ~180
  lines) has at least three sub-paths the spec doesn't mention: (1) **kickoff
  rejection** (`!frameSessionId` + `pendingKickoff`) — workspace-setup failure,
  renders a tailored duplicate-vs-failure message; (2) **cancel-ack** — renders
  empty content with `status: 'interrupted'`; (3) **generic error** — renders
  `frame.message` verbatim. The spec says "Render translated `message`" without
  saying which sub-path the new code lives in. If the helper is invoked in the
  generic sub-path only, the kickoff and cancel sub-paths still render raw text.
  If it's invoked on every frame, it has to no-op for cancel-ack (no `code`) and
  for kickoff (also no `code`).
- **Impact**: Either (a) missed coverage — kickoff-reject continues to surface
  raw server text, including provider errors during workspace bootstrap — or
  (b) over-coverage — the helper mangles a cancel-ack into a generic error message.
- **Recommendation**: Specify the integration point precisely. Recommended:
  `llm-error.ts` is invoked only when `frame.llm_error` (or `frame.code`) is
  present; cancel-ack and kickoff-reject are unchanged. Document the gating in
  the symbol-table row and in FR-010.

---

#### [MAJ-011] `detail` persistence story is unspecified — prod-replay may leak `detail` if persisted

- **Lens**: Insecurity
- **Affected section**: FR-008 (line 461); FR-010 (line 463); Edge case "`detail` present but app in prod → never rendered" (line 196)
- **Description**: The spec says `detail` is "DEV/gateway only; never rendered
  in prod" and FR-008 says "persist only the translated `message`." But the spec
  never states whether `detail` is **persisted to the JSONL transcript**. Two
  readings:
  - **Not persisted:** replay cannot reconstruct `detail`, so DEV mode after a
    reload shows no Technical Details. Acceptable but should be stated.
  - **Persisted:** the JSONL now contains raw provider text indefinitely; the
    SPA's "DEV only" gate is the only barrier. A future bug in the DEV detection
    (`import.meta.env.DEV`) or a third-party consumer of the transcript leaks it.
  The spec picks neither. The 90-day retention in CLAUDE.md means `detail`,
  if persisted, sits on disk for 90 days.
- **Impact**: If `detail` is persisted, the spec's D5 ("Raw provider errors
  never cross the gateway→SPA boundary in production") is true only at the
  render layer, false at the storage layer. A misconfigured proxy, a SPA bug,
  or a backup exfiltration all surface the raw text.
- **Recommendation**: State explicitly. Recommended: **do not persist `detail`**.
  Keep it on the in-memory `ErrorPayload` for the live WS frame only; persist
  only `code`, `message`, `retryable` to the JSONL. DEV mode after a reload
  loses Technical Details, which is the correct security/UX trade.

---

### MINOR Findings

#### [MIN-001] `isImageInputRejection` location cited as `loop.go:7254` — that's the call site, not the definition

- **Lens**: Inconsistency
- **Affected section**: "Existing Codebase Context" symbol table row (line 39): "`isImageInputRejection` (`pkg/agent/loop.go:7254`)"
- **Description**: The function is defined at `pkg/agent/loop_media.go:286`. The
  cited line `loop.go:7254` is its only call site. The "Symbol | Context" table
  reads as a definition location, sending an implementer to the wrong file.
- **Recommendation**: Cite both — definition at `loop_media.go:286`, called from
  `loop.go:7254` — or just cite the definition.

---

#### [MIN-002] `code` enum omits `auth_failure` and `provider_outage`; operators can't distinguish in metrics

- **Lens**: Inoperability / Incorrectness
- **Affected section**: `LLMError` code taxonomy (lines 174-184)
- **Description**: 401/403 (invalid key) and 5xx-other-than-503 (provider
  outage) both collapse to `unknown`. The incident root-causing workflow would
  benefit from at least `auth_failure` (operator action: rotate key) and
  `provider_outage` (operator action: wait or failover). Today both become "The
  AI service encountered an error" and the operator has to read `gateway.log`
  to tell them apart.
- **Recommendation**: Add `auth_failure` (401/403) and `provider_outage`
  (500/502/504 other than 503). The cost is two more enum values; the benefit
  is operator triage.

---

#### [MIN-003] `translateLLMError(rawErr, model)` — the `model` parameter's purpose is unspecified

- **Lens**: Overcomplexity
- **Affected section**: "Translation function" signature (line 257)
- **Description**: The function takes `model string` but the spec never says
  how it's used. If it's for inclusion in the translated message ("the model X
  could not…"), that violates the "no provider identity in `message`" rule
  (Invariant #5). If it's unused, drop it (CPX-02).
- **Recommendation**: Either drop the parameter or specify its use. Recommended:
  drop it; the translated message is provider-and-model-agnostic by design.

---

#### [MIN-004] "Counter for media-downgrade events" — no metric name, labels, or threshold

- **Lens**: Inoperability
- **Affected section**: "Operations & Rollback → Observability" (line 283)
- **Description**: The spec promises a counter but doesn't name it, doesn't list
  labels (agent_id? provider? model? trigger?), doesn't say where it's exported
  (the binary is file-based, no Prometheus), and doesn't specify an alert
  threshold. Today the project has no metrics surface — the counter may be
  structurally invisible.
- **Recommendation**: Either (a) define the counter as a structured-log entry
  (`logger.InfoCF("media_downgrade_total", …)`) that operators grep, with a
  label set; or (b) drop the counter and rely on the existing `logger.ErrorCF`
  raw-error log + a grep convention. Specify which.

---

#### [MIN-005] Feature flags D1/D2/D5/D6/D7 — names and config paths not specified

- **Lens**: Inoperability
- **Affected section**: "Operations & Rollback → Feature flags" (line 284)
- **Description**: "Each independently flaggable; default on." No flag names, no
  config keys, no `config.json` path. An operator reading the spec can't disable
  D2 without reading code.
- **Recommendation**: Add a row per flag: name, config path (e.g.
  `agent.media.normalize_images`), default, rollback behaviour. Align with the
  existing config-key conventions in `pkg/config/`.

---

#### [MIN-006] Test #9 negative assertions miss provider-name leakage

- **Lens**: Insecurity / Incorrectness
- **Affected section**: TDD Test #9 `TestTranslateLLMError_MessageIsGeneric_NoRawBody` (line 421); Incident description (ADR-051 line 23)
- **Description**: The incident leaked not only the raw body but also
  `"provider_name":"xAI"`. Test #9 asserts the raw body is absent from the
  translated `message`; it does not assert the provider name is absent. A
  translation rule like `"xAI rejected the image"` would pass test #9 while
  still leaking provider identity.
- **Recommendation**: Add a negative assertion to test #9: a fixed list of
  provider names (`xAI`, `OpenAI`, `Anthropic`, `Gemini`, `OpenRouter`,
  `grok`, `claude`, `gpt-`) must not appear in the translated `message`.

---

#### [MIN-007] No DEV-vs-PROD negative test — only positive "DEV renders detail"

- **Lens**: Incompleteness
- **Affected section**: BDD "DEV mode exposes raw detail behind a disclosure" (lines 364-370); TDD Test #12 (line 424)
- **Description**: The BDD scenario has a "But in production the disclosure and
  `detail` are absent" clause. No test in the TDD plan asserts the negative.
  Test #12 only covers the positive DEV path. A future change that renders
  `detail` unconditionally in prod would not be caught.
- **Recommendation**: Add a vitest: "in production (`import.meta.env.DEV ===
  false`), `detail` is not present in the rendered DOM even if supplied on the
  wire." Negative test, cheap to write.

---

#### [MIN-008] "Live error renders without a page reload" (US-5) has only a backend integration test

- **Lens**: Infeasibility / Test coverage
- **Affected section**: BDD "Live error renders without a page reload" (lines 372-378); TDD Test #11 (line 422)
- **Description**: The BDD asserts the SPA renders the error in the same
  session. Test #11 is `TestWebsocket_ForwardsEventKindError_Live` — a backend
  Go integration test verifying the WS frame is sent. The "renders without
  reload" UX assertion requires a Playwright E2E test; none is listed.
- **Recommendation**: Add an E2E test (e.g. `live-error.spec.ts`) that: connects
  a live WS session, triggers a stubbed provider error, asserts the error
  renders in the DOM without a navigation event.

---

#### [MIN-009] Password-protected ZIP manifest lists entry names verbatim — filenames may carry PII

- **Lens**: Insecurity
- **Affected section**: Edge Cases (line 191)
- **Description**: "Password-protected ZIP → manifest lists entries (names
  visible)." Entry names in a password-protected archive are often sensitive
  (e.g. `tax_return_2025.pdf`, `patient_records/q4.csv`). Listing them in the
  manifest sends them to the provider as plaintext context — exactly the
  "raw bytes don't cross" guarantee the spec is trying to uphold, just one
  layer up.
- **Recommendation**: Either (a) emit only `(count, total_size)` for
  password-protected archives, no entry names, or (b) state in the spec that
  entry names are treated as non-sensitive and let the operator decide via a
  flag. Recommended: (a) — matches the "no raw content" intent.

---

#### [MIN-010] `maxSize` referenced but neither defined nor valued

- **Lens**: Ambiguity
- **Affected section**: "Boundary conditions" (line 156); Integration Boundaries `image` stdlib (line 213); ADR-051 "Negative consequences"
- **Description**: The spec references `maxSize` as a pre-existing value ("Image
  over `maxSize` → existing too-large note"). The value is not stated, the
  config key is not stated, and an implementer new to the codebase has to hunt
  for it.
- **Recommendation**: Cite the value and the config key (or the Go constant
  name) in the Boundary Conditions section.

---

#### [MIN-011] "Entry count + per-entry size cap" values unspecified

- **Lens**: Ambiguity (AMB-07)
- **Affected section**: Edge Cases "Zip-bomb" (line 192); Integration Boundaries `archive/*` (line 222)
- **Description**: "Manifest bounded by entry count + per-entry size cap (deny
  listing beyond cap; note truncation)." Neither cap value is given. Different
  implementers will pick different values.
- **Recommendation**: Pin both values. Suggested defaults: max 1000 entries,
  max 50 entries listed (truncation note beyond), per-entry size from the
  archive's own header (no extra read). State them in the Integration Boundaries.

---

#### [MIN-012] Test dataset row #3 mislabeled "regression"

- **Lens**: Inconsistency
- **Affected section**: Test Dataset "error classification" row 3 (line 435)
- **Description**: Row 3 (`400 image-reject body → media_unsupported`) is
  labeled boundary type `regression`. A regression test prevents a bug from
  reoccurring; this row is a functional classification test. The label
  matters because the test plan organises by boundary type.
- **Recommendation**: Relabel to `happy` or `functional`. Reserve `regression`
  for tests that pin a previously-shipped bug.

---

#### [MIN-013] BDD Scenario Outline has 6 example rows; dataset has 7 — count mismatch

- **Lens**: Inconsistency (CON-02)
- **Affected section**: BDD Scenario Outline examples table (lines 388-396); Test Dataset "error classification" (lines 432-439)
- **Description**: The Scenario Outline lists 6 status/code/retryable triples
  (429, 408, 503, 413, 400-image, 400-other). The dataset has 7 rows
  (adds "unparseable/garbage → unknown"). The garbage-row classifier behaviour
  is described in the dataset but has no BDD examples-table row, so the BDD
  scenario under-covers the dataset.
- **Recommendation**: Add a 7th examples row (`-`/garbage → `unknown`/false),
  or remove the dataset row, or note explicitly that the garbage row is a
  unit-only negative test outside the BDD scenario's scope.

---

#### [MIN-014] No forward-compat story for unknown `code` values arriving at the SPA

- **Lens**: Inoperability
- **Affected section**: FR-010 (line 463); `LLMError` schema `code` enum (line 246)
- **Description**: The spec hardcodes the `code` enum. If a future backend adds
  a code (say `auth_failure` per MIN-002) before the SPA is updated, the SPA's
  zod schema drops the frame (per the established ws-edge discipline). The user
  sees no error at all — worse than today's raw text.
- **Recommendation**: Specify forward-compat: the SPA's `llm-error.ts` maps any
  unknown `code` to the `unknown` display string and renders normally. The zod
  schema should accept unknown enum values (or the SPA pre-passthrough layer
  should rewrite unknown codes to `unknown` before validation).

---

### Observations

#### [OBS-001] Four of the seven `LLMError` codes are not strictly needed to fix the incident

- **Lens**: Overcomplexity
- **Suggestion**: The incident is closed by `media_unsupported` (D2) +
  `rate_limited` (existing `recordRateLimitDenial`) + `unknown` (everything
  else). The other four codes (`provider_rejected`, `network`, `content_policy`,
  `context_too_long`) add classifier rules, tests, and message copy without
  fixing anything that's currently broken. Consider trimming the enum to three
  for v0.1.1 and adding the rest in v0.3 alongside RD3.

---

#### [OBS-002] Animated GIF → static PNG loses animation; consider downgrading instead

- **Lens**: Incorrectness (UX)
- **Suggestion**: Today's `image/gif` stdlib decoder already returns only the
  first frame, so "transcode to static PNG" is technically correct but
  surprising — the user sends a looping animation and the agent sees a still.
  Consider detecting multi-frame GIFs (`gif.DecodeAll`) and downgrading them to
  a note ("animated GIF, N frames, omitted") rather than silently dropping
  N-1 frames. Preserves the spec's "media either complete or noted-omitted"
  invariant.

---

#### [OBS-003] Consider an audit-log entry for media-downgrade events

- **Lens**: Inoperability
- **Suggestion**: A counter (MIN-004) tells you how often downgrades happen;
  an audit entry (`pkg/audit`) tells you which agent, which provider, which
  file, which reason. The latter is more useful for incident investigation.
  Consistent with the existing `Emit*` precedent.

---

#### [OBS-004] `Evaluation Scenarios (Holdout)` has only 2 scenarios; the spec's risk surface is larger

- **Lens**: Incompleteness
- **Suggestion**: Add holdouts for (a) "second provider rejection terminates"
  (the path SC-002 lives or dies on), (b) "live error in active WS session
  then reload" (MAJ-008), (c) "sub-turn error rendering" (MAJ-004). These are
  the scenarios most likely to regress silently.

---

#### [OBS-005] `LLMError` could carry `occurred_at` and `provider_class` for operator triage without leaking identity

- **Lens**: Inoperability
- **Suggestion**: If the spec wants operators to triage without reading
  `gateway.log`, add a `provider_class` field ("openrouter" / "direct" /
  "unknown") and an `occurred_at` epoch — neither leaks provider identity to
  the user but both support dashboards. Out of scope for v0.1.1 if MIN-002 is
  addressed; otherwise consider.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1 through US-5 each have ≥1 |
| Every acceptance scenario has BDD scenarios | PASS | All AS map to a BDD scenario |
| Every BDD scenario has `Traces to:` reference | PASS | All named scenarios carry the back-reference |
| Every BDD scenario has a test in TDD plan | PARTIAL | BDD "Live error renders without a page reload" has only a backend integration test, no E2E for the "renders without reload" half (MIN-008) |
| Every FR appears in traceability matrix | PASS | FR-001…FR-011 mapped; FR-012 is the explicit non-goal |
| Every BDD scenario in traceability matrix | PASS | |
| Test datasets cover boundaries/edges/errors | FAIL | No coverage for tar/gz/tgz (MAJ-009); no DEV-vs-PROD negative (MIN-007); no provider-name-leak negative (MIN-006); no incident-string classifier row (CRIT-002) |
| Regression impact addressed | PARTIAL | The `pdfCapableModel` and `isImageInputRejection` regression rows exist but the latter does not pin the incident input (CRIT-002) |
| Success criteria are measurable | FAIL | SC-002 is unmeasurable as written because the "single seam" claim it depends on is false (CRIT-001); SC-001 silently violated by Slice-1-only ships (MAJ-005) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|-----------------|--------------------|
| Non-target emit sites | No test drives a provider error through hooks/model_switch/subturn/bottom-of-loop paths and asserts no raw text leaks | CRIT-001, MAJ-004 |
| E2E live-render | "Live error renders without reload" has only a backend integration test | MIN-008 |
| DEV-vs-PROD negative | Only positive DEV-disclosure tested; no PROD-never-renders test | MIN-007 |
| Idempotency | No test for live-frame + reload dedup | MAJ-008 |
| tar/gz/tgz manifest | Only ZIP covered | MAJ-009 |
| Forward-compat | No test for unknown `code` arriving at the SPA | MIN-014 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Error classification | The exact incident string (`Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.`) | Add as row pinning CRIT-002 |
| Error classification | Capability-absence class (`image input not supported`) | Add to lock the old `isImageInputRejection` behaviour under the new classifier |
| Error classification | 401/403 and 500/502/504 | Add rows for `auth_failure` and `provider_outage` (MIN-002), or explicitly accept `unknown` |
| Error classification | Body-substring variants for context-too-long (not 413) | MAJ-003 |
| Archive inputs | `.tar`, `.gz` (single-file), `.tgz` | MAJ-009 |
| Archive inputs | Zip bomb (100k entries) and password-protected | MIN-009, MIN-011 |
| Translated message | Provider-name-leak negative assertion | MIN-006 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `encodeImageToDataURL` transcoder | ok | ok | ok | ok | risk | ok | CPU cost on crafted image (decode of a "decompression bomb" PNG/JPEG); `maxSize` cap mitigates partially — value not pinned (MIN-010) |
| Media-rejection classifier | ok | ok | risk | ok | ok | ok | No audit entry for downgrade decisions (OBS-003) |
| `translateLLMError` | ok | ok | ok | risk | ok | ok | Information disclosure if classifier copies body or provider name into `message` (MIN-006) |
| `detail` field on wire | ok | ok | ok | risk | ok | ok | DEV-only render is the sole barrier if persisted; persistence story unspecified (MAJ-011) |
| Archive manifest | ok | ok | ok | risk | ok | ok | Entry-name leakage for password-protected archives (MIN-009); zip-bomb resource use (MIN-011) |
| `EventKindError` live WS frame | ok | ok | risk | ok | risk | ok | No audit log for error events; no rate limit on error-frame flood (a churning provider can DOS the SPA render) |
| Replay JSONL transcript | ok | ok | ok | risk | ok | ok | If `detail` is persisted (MAJ-011), raw provider text sits on disk for 90-day retention |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. How are the **six non-target** `EventKindError` emit sites (hooks, model_switch,
   second runTurn block, third runTurn block, bottom-of-loop generic, `subturn.go`)
   brought under the translation seam? The spec never addresses them.
2. What is the persistence story for `detail` — persisted to JSONL (90-day
   retention) or in-memory only? (MAJ-011)
3. Does the `recordRateLimitDenial` dual-emit survive, or is `EventKindRateLimit`
   subsumed under `EventKindError{code: rate_limited}`? (MAJ-002)
4. After a live error renders and the user reloads, what prevents the replay
   path from rendering the same error a second time? (MAJ-008)
5. How does the classifier reliably read HTTP status when `HandleErrorResponse`
   returns a flat multi-line `error`, not a structured type? (MAJ-001)
6. Where does `LLMError` live in the AsyncAPI schema — new top-level message,
   nested in `ReplayErrorFrame.payload`, sibling of `message`? (CRIT-003)
7. What does an older SPA do when a new backend emits a code it doesn't know?
   (MIN-014)
8. Why is `subturn.go`'s `EventKindError` emit out of scope without an
   Explicit-Non-Behavior entry? (MAJ-004)
9. Is Slice 1 alone (D2 retry, no D5 translation) acceptable to ship to
   `release/v0.1.1` given it violates SC-002 on the second-rejection path?
   (MAJ-005)
10. What are the values of `maxSize`, the manifest entry-count cap, and the
    per-entry size cap? (MIN-010, MIN-011)
11. What are the names and config paths of the five feature flags? (MIN-005)
12. What does the `translateLLMError(rawErr, model)` `model` parameter do?
    (MIN-003)

---

## Verdict Rationale

**Verdict: BLOCK.** Three critical defects each independently make the spec
unimplementable-as-written.

CRIT-001 is the most damaging: the spec's load-bearing reliability argument is a
"single translation seam" at `loop.go:7277-7300`. The codebase has six other
`EventKindError` emit sites in the agent loop alone (plus one in `subturn.go`)
that will continue to surface raw provider text after this spec ships. SC-002
cannot be satisfied without either proving every emit site is migrated or
moving translation to the forwarder/replay decoder. The spec must pick one and
enumerate the sites.

CRIT-002 means the headline incident may not actually be fixed. The spec frames
`isImageInputRejection` as a "one-off to generalize," but the function today
returns **false** for the incident's exact error string. A naive generalisation
that adds more phrases without introducing a new *format-rejection* pattern
class ships a fix that still reproduces the bug. The test plan must pin the
incident string as a dataset row.

CRIT-003 means two implementers will produce incompatible wire shapes. The spec
adds an `LLMError` schema without saying whether it nests inside
`ReplayErrorFrame.payload`, replaces `message`, or defines a new live message.
The existing schema's `additionalProperties: false` constraint makes this
load-bearing.

The eleven MAJOR findings mostly cluster around the same theme: the spec
assumes a cleaner world than the code reveals. Multiple emit sites, dual
rate-limit emissions, in-flight streaming, schema integration, slice sequencing,
and the dev/prod `detail` boundary each have at least one plausible
implementation that violates the spec's own invariants.

The fourteen MINOR findings are quality issues — wrong file paths in the symbol
table, missing negative tests, unspecified cap values, forward-compat gaps.
None blocks implementation alone; all are cheap to fix during the CRIT/MAJOR
revision pass.

### Recommended Next Actions

- [ ] **CRIT-001**: Run `rg -n "EventKindError|appendErrorTranscript" pkg/agent/`,
  enumerate every emit site in the spec, and either migrate each to the seam or
  move translation to the WS forwarder + replay decoder. Add BDD/E2E coverage
  for at least the sub-turn, hooks, and model_switch paths.
- [ ] **CRIT-002**: Add the exact incident string as a classifier test-dataset
  row; rewrite D2/FR-002 to acknowledge two pattern classes (capability-absence
  and format-rejection).
- [ ] **CRIT-003**: Pin the `LLMError` AsyncAPI integration (recommended:
  nested in `ReplayErrorFrame.payload.llm_error` + a new live `ErrorFrame`
  message whose payload is the same component schema).
- [ ] **MAJ-001**: Decide whether `HandleErrorResponse` returns a typed error
  or the classifier regex-parses the wrapped string; add a test using real
  `HandleErrorResponse` output.
- [ ] **MAJ-002 through MAJ-011**: Address each — most are 1-2 sentences of
  spec text.
- [ ] **MIN-001 through MIN-014**: Address during the revision pass.
- [ ] **Re-run `/grill-spec`** after revision; the spec should not proceed to
  `/taskify` until at least CRIT-001/002/003 are closed.
