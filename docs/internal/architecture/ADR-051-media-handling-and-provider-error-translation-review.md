# Adversarial Review: ADR-051 (Provider-Capability-Aware Media Handling and User-Facing Error Translation)

**Spec reviewed**: `docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md` (Revision 2)
**Review date**: 2026-07-21
**Verdict**: BLOCK

## Executive Summary

The ADR's Revision 2 redesign — translating at two boundary choke points instead of one in-loop seam — correctly closes the prior grill's CRIT-001 (the false "single seam" claim) at the *coverage* level. However, the redesign introduces a new, more fundamental defect that the prior spec-level grill could not have caught because it is specific to the ADR's new architecture: **neither choke point has access to the raw error that `translateLLMError(rawErr error)` needs.** The bus event carries `ErrorPayload{Stage, Message}` — a pre-formatted string — and `appendErrorTranscript(kind, stage, message string)` takes only strings. The structured `*ProviderError` the ADR adds to `HandleErrorResponse` to "make classification reliable" (MAJ-001) is **lost in the formatting chain** before it reaches either choke point. The ADR's headline design is not implementable as described; an implementer following it literally will either fall back to the substring parsing MAJ-001 was meant to eliminate, or will have to invent an `ErrorPayload` extension the ADR does not specify. A second critical defect: the ADR's "~10 emit sites" count (the factual basis for the "covers all by construction" claim) is wrong — the real total is **29** across 7 files, and the ADR conflates two distinct event channels (the in-memory bus vs the runner channel), listing runner-channel sites as bus sites.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 8 |
| MINOR | 6 |
| OBSERVATION | 3 |
| **Total** | **19** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] `translateLLMError(rawErr error)` is infeasible at both choke points — the raw error does not exist there

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: RD5 (lines 82-96); "Integration Contract" error invariants #4-#5 (lines 200-204); "Affected Components" (line 166); pseudocode in the spec at `media-handling-error-translation-spec.md:291-299`
- **Description**: The ADR's entire Rev 2 architecture rests on "Both call one shared pure function `translateLLMError(rawErr) → LLMError{...}" at the two choke points. Neither choke point has a raw `error` to pass:

  1. **Write choke point** — `appendErrorTranscript(kind, stage, message string)` (`turn.go:1152`) takes three strings. At every call site the raw `err` has already been formatted away:
     - `loop.go:7289`: `fmt.Sprintf("LLM call failed after retries: %s", err.Error())`
     - `loop.go:8532`: `err.Error()` directly
     - `loop.go:6199`: `fmt.Sprintf("Could not switch to model %q: %s. This reply used %q instead.", ...)`
     - `loop.go:1082`: `fmt.Sprintf("rate limit: %s (%s)", payload.PolicyRule, retryHint)`

  2. **Live choke point** — the bus event carries `Payload any` which for errors is `ErrorPayload{Stage string; Message string}` (verified at `events.go:477`). There is no `RawErr`, `ProviderError`, or HTTP status field on the event. The WS forwarder receives `Event{Kind, Meta, Payload: ErrorPayload{Stage, Message}}` — period.

  The ADR's RD5 further claims: "To make classification reliable, `HandleErrorResponse` additionally returns a structured `*ProviderError{Status, Body, Err}` ... so the classifier can read HTTP status instead of parsing a flat string (MAJ-001)." This is the ADR's stated fix for MAJ-001. **But the structured error never reaches the choke points.** The chain is: `HandleErrorResponse` → `*ProviderError` → provider `Chat()` returns it → `runTurn()` catches it → calls `err.Error()` to format `ErrorPayload.Message` → emits event / calls `appendErrorTranscript`. The structured type is collapsed to a string at the emit site, one layer above the choke point. MAJ-001 is **not fixed** by this design — the classifier at the choke point still sees only the flat string, exactly the situation MAJ-001 flagged.

- **Impact**: The ADR's central architectural decision is not implementable as described. An engineer following the ADR literally will discover that `translateLLMError(rawErr error)` has no `rawErr` to receive at either choke point, and will be forced into one of three unplanned paths: (a) parse the pre-formatted string via regex/substring (the very approach MAJ-001 rejected), (b) extend `ErrorPayload` with a `RawErr`/`ProviderError` field and thread it through every emit site (an `ErrorPayload` schema change the ADR never mentions, with downstream contract/zod implications), or (c) move classification back to emit sites (the design the ADR explicitly rejected as "high regression risk"). Each path contradicts a stated ADR decision.
- **Recommendation**: Pick one explicitly before ratification:
  1. **(Recommended) Thread the structured error through the event.** Add `ProviderError *ProviderError` (or `RawErr error`) to `ErrorPayload`; have each emit site pass the raw error (most emit sites already have it in scope — `loop.go:7280` has `err`, `loop.go:6195` has `switchErr`, `loop.go:1058` has the `RateLimitPayload`). The WS forwarder and `appendErrorTranscript` then read `payload.ProviderError.Status/Body` directly. This is the only path that actually fixes MAJ-001. State the `ErrorPayload` extension in "Affected Components" and in the Integration Contract.
  2. *(Alternative)* Accept substring classification on the pre-formatted message. Then drop the `*ProviderError` change from `HandleErrorResponse` entirely (it serves no purpose at the choke points), explicitly mark MAJ-001 as "accepted, not fixed," and document the regex the classifier uses against `"Status: %d\n  Body:   %s"`.

  Either way, the pseudocode `translateLLMError(rawErr)` must be replaced with the actual signature the chosen path implies.

---

#### [CRIT-002] The "~10 emit sites" count is wrong (actual: 29 across 7 files); the ADR conflates the in-memory bus with the runner channel

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: Context §"What the error path does today" (lines 36-38); RD5 (line 87: "Covers every emit site (loop.go ×8, subturn.go, driver_codex.go ×5)"); Grill-finding table MAJ-004 ("Covered by choke points by construction")
- **Description**: The ADR enumerates `EventKindError` emit sites as "~10" = 8 in `loop.go` + 1 in `subturn.go` + 5 in `driver_codex.go`. The actual non-test, non-smoketest count is **29** across 7 files:

  | File | Sites | ADR mentions? |
  |---|---|---|
  | `pkg/agent/loop.go` | 8 (1059, 3494, 6195, 7280, 7479, 8403, 8500, 8525) | yes |
  | `pkg/agent/subturn.go` | 1 (847) | yes |
  | `pkg/agent/runner/driver_codex.go` | **6** (225, 233, 310, 371, 387, 398) | yes, but says **5** and wrong path |
  | `pkg/agent/runner/driver_opencode.go` | **5** (192, 200, 330, 351, 373) | **no** |
  | `pkg/agent/runner/driver_claude.go` | **6** (259, 267, 359, 407, 528, 547) | **no** |
  | `pkg/agent/runner/external_event.go` | **2** (205, 238) | **no** |
  | `pkg/agent/runner/driver_stream.go` | **1** (161) | **no** |

  More importantly, the ADR lists `driver_codex.go ×5` as sites "on the in-memory bus." They are **not** on the bus. The runner drivers (`pkg/agent/runner/*`) emit `runner.RunEvent` values onto a per-run **channel** (`chan runner.RunEvent`), consumed by an intermediate (`spawnSubTurn` or a task executor). Those events never reach `al.eventBus.Emit` directly. The bus-level `EventKindError` sites are the 8 in `loop.go` + 1 in `subturn.go` = 9 (the runner driver events are consumed and *re-emitted* by the loop if they bubble up). The ADR conflates two distinct event mechanisms.

- **Impact**: The "covers all emit sites by construction" claim (RD5, MAJ-004 resolution) is currently *true by accident* for the bus-level forwarder case, because the runner channel sites funnel through `spawnSubTurn` which re-emits on the bus. But the ADR's reasoning is unsound — it cites runner-channel sites as bus sites to argue coverage, when in fact those sites are covered by a different mechanism (intermediate re-emission) that the ADR never names. An implementer who trusts the ADR's accounting and later adds a new runner driver that emits `EventKindError` directly to the bus (bypassing `spawnSubTurn`) would believe the choke point covers it. The miscount also undermines the ADR's credibility on its other code-cited claims.
- **Recommendation**:
  1. Correct the enumeration: state clearly that there are **9 bus-level** `EventKindError` emit sites (8 `loop.go` + 1 `subturn.go`) covered by the forwarder case, plus **20 runner-channel** `EventKindError` sites across 5 driver files (`driver_codex.go`, `driver_opencode.go`, `driver_claude.go`, `driver_stream.go`, `external_event.go`) covered by the *intermediate* `spawnSubTurn`/executor re-emission path — not by the forwarder directly.
  2. Cite the runner driver files at their correct paths (`pkg/agent/runner/driver_codex.go`, not `driver_codex.go`).
  3. Add an invariant test: "every new runner driver that emits `EventKindError` MUST either route through `spawnSubTurn` or be explicitly listed as a bus-level site." Otherwise the coverage claim is unauditable.

---

### MAJOR Findings

#### [MAJ-001] The write choke point double-translates the rate-limit message, destroying the carefully-formatted retry hint

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: RD5 choke point #1 (line 86); RD6 rate-limit dedup (line 101); `recordRateLimitDenial` (`loop.go:1028-1083`)
- **Description**: RD6's rate-limit dedup ("when `code==rate_limited`, the error frame is suppressed") is specified only for the **live** choke point. The **write** choke point (`appendErrorTranscript`) applies `translateLLMError` to *every* message unconditionally. But `recordRateLimitDenial` at `loop.go:1082` persists a carefully-formatted message: `fmt.Sprintf("rate limit: %s (%s)", payload.PolicyRule, retryHint)` where `retryHint` is `"retry after Ns"` or `"retry shortly"`. If `translateLLMError` classifies this as `code=rate_limited` and substitutes the generic message `"The AI service is busy. Please retry shortly."`, the replay path loses the specific `PolicyRule` and `RetryAfterSeconds` data that `recordRateLimitDenial` deliberately preserved. The existing `RateLimitFrame` (which carries `RetryAfterSeconds` structured) is only authoritative on the **live** path; on **replay**, the only signal is the JSONL transcript entry — which the write choke point will have genericized.
- **Impact**: After a page reload, a rate-limited user sees "The AI service is busy. Please retry shortly." instead of the specific "retry after 30s" hint that was live. This is a functional regression on the rate-limit UX path introduced by the translation layer itself.
- **Recommendation**: Either (a) gate the write-choke-point translation on `kind != "rate_limit"` (the `kind` argument already distinguishes them — `EventKindRateLimit.String()` vs `EventKindError.String()`), and document the gate; or (b) make `translateLLMError` a no-op when the input already matches the `"rate limit: ..."` shape. Option (a) is cleaner and uses the signal already on the call.

---

#### [MAJ-002] `detail` cannot be "computed live at the forwarder from the in-memory error" — the raw error is not at the forwarder

- **Lens**: Infeasibility
- **Affected section**: RD5 ("`detail` ... never persisted"); RD7 (line 106: "`detail` is computed live at the forwarder from the in-memory error and is **never persisted**"); Error invariant #5 (line 204)
- **Description**: RD7 states `detail` is "computed live at the forwarder from the in-memory error." But (per CRIT-001) the forwarder receives `Event{Payload: ErrorPayload{Stage, Message}}`. There is no "in-memory error" at the forwarder — only the pre-formatted `Message` string. `detail` is defined as the raw provider text (for DEV disclosure), which by definition is the raw error/body — neither of which is carried on the event. So `detail` cannot be computed at the forwarder at all.
- **Impact**: Either `detail` is always empty (DEV disclosure shows nothing useful — the feature ships broken), or the implementer threads raw text onto the event payload (which then risks persisting it unless the ADR explicitly says not to — and the very reason for computing `detail` "live" was to keep it off disk).
- **Recommendation**: State where `detail` actually comes from. If it comes from a `RawErr`/`ProviderError` field added to `ErrorPayload` (per CRIT-001 option 1), say so. If it comes from the pre-formatted `Message`, then it is *not* the raw provider text and the DEV disclosure shows the same pre-formatted string the user already sees — making the disclosure pointless. Resolve explicitly.

---

#### [MAJ-003] `model_switch` messages embed model names — direct conflict with the "no provider identity in `message`" invariant

- **Lens**: Inconsistency
- **Affected section**: ADR invariants (line 199: "Provider identity and raw response text never in `message`"); `loop.go:6191` (`switchFailMsg`)
- **Description**: The model-switch emit at `loop.go:6195` carries `ErrorPayload{Stage: "model_switch", Message: switchFailMsg}` where `switchFailMsg` is `"Could not switch to model %q: %s. This reply used %q instead."` — it embeds the requested model slug, the error, AND the fallback model slug. This message is valuable to the user (it tells them which model was used). Invariant ("Provider identity ... never in `message`") and the RD5 rule ("Default copy is generic over server-supplied text") demand it be replaced. The ADR provides no rule for this case. If `translateLLMError` classifies `model_switch` as `code=unknown` and substitutes generic copy, the user loses actionable information ("which model is actually answering me?"). If it preserves the message, the invariant is violated.
- **Impact**: Either a UX regression (user can't tell which model replied) or an invariant violation. The ADR's blanket "generic over server-supplied text" rule is too coarse for the `model_switch` case, which is agent-supplied, not server-supplied.
- **Recommendation**: Add an explicit carve-out for `stage=model_switch` (and any other agent-composed message): `translateLLMError` passes these through unmodified, because the "raw provider text" invariant targets provider HTTP bodies, not agent-composed informational messages. Document the distinction between "raw provider response" (must be translated) and "agent-composed contextual message" (may be preserved).

---

#### [MAJ-004] Rate-limit classifier must handle TWO incompatible message formats; the ADR specifies only one

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: RD6 rate-limit dedup (line 101); classifier two-class description (RD5 line 96); `recordRateLimitDenial` (`loop.go:1082`); `HandleErrorResponse` (`common.go:390`)
- **Description**: Rate-limit conditions reach the choke points via **two different message shapes**:
  1. **`recordRateLimitDenial` path** — message is `"rate limit: <rule> (retry after Ns)"`. Generated internally by Omnipus's own rate-limit policy. Already friendly.
  2. **Provider-native 429 path** — message is `"LLM call failed after retries: API request failed:\n  Status: 429\n  Body:   <provider's 429 body>"`. Generated by `HandleErrorResponse`. Raw provider text.

  The ADR's RD6 dedup ("suppress when `code==rate_limited`") requires the classifier to correctly classify BOTH shapes as `rate_limited`. The dataset in the spec (rows 1, 8) only exercises shape #2 (HTTP 429). Shape #1 (the internal rate-limit) is not in the classifier dataset. An implementer writing tests from the dataset will ship a classifier that matches `"Status: 429"` but not `"rate limit:"`, and the internal rate-limit path will dedup incorrectly (or not at all).
- **Impact**: Internal rate-limit denials either fail to dedup against `RateLimitFrame` (duplicate bubble — the exact regression RD6 exists to prevent) or fail to classify as `rate_limited` at all and surface as `code=unknown`.
- **Recommendation**: Add both message shapes to the classifier dataset. Specify the matcher for shape #1 as a `"rate limit:"` prefix (the format `recordRateLimitDenial` produces) and document that this is an Omnipus-internal format, not a provider format.

---

#### [MAJ-005] The universal invariant "no raw provider text crosses the gateway→SPA boundary" is violated by the REST executor path

- **Lens**: Insecurity / Incompleteness
- **Affected section**: "Integration Contract" media invariant #3 / error invariant #4 (lines 199-202); REST executor at `rest_executor_smoketest.go:616`
- **Description**: The ADR's normative invariant states: "No string crossing the gateway→SPA boundary in production contains raw provider text." The REST executor smoke-test path (`drainSmokeTestRun` at `rest_executor_smoketest.go:579-630`) surfaces runner errors directly: `case runner.EventKindError: msg = ev.Err.Message; return ..., false, msg`. The `ev.Err.Message` from an external CLI driver (`driver_claude.go`, `driver_codex.go`, `driver_opencode.go`) is the raw stderr/error text from `claude-code`/`codex`/`opencode` — which itself often contains the upstream LLM provider's error text verbatim. This `msg` is returned to the REST caller and surfaces in the task-run history and smoke-test UI. The ADR's two choke points (`appendErrorTranscript` + chat WS forwarder) do not touch this path at all — it has its own `case runner.EventKindError:` handler.
- **Impact**: Either (a) the invariant is universally false as stated (task-run smoke tests surface raw CLI/provider text today and the ADR doesn't change that), or (b) the REST executor path is implicitly out of scope. The ADR never says which. An operator reading the invariant as universal will believe the smoke-test UI is sanitized when it isn't.
- **Recommendation**: Scope the invariant explicitly. State that the two choke points cover the **chat WS** path only, and that the REST executor / task-run path is out of scope for v0.1.1 (its errors surface in task-run history, not chat). Or, if task-run errors ARE in scope, add a third choke point at `drainSmokeTestRun`'s `EventKindError` case. Pick one.

---

#### [MAJ-006] Replay cannot reconstruct `llm_error.code` unless `code` is persisted alongside the translated `message`

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: RD5 (line 89: "translate the `message` before JSONL write"); Integration Contract (line 193: "`ReplayErrorFrame.payload.llm_error: LLMError`"); spec Ambiguity Warning #7
- **Description**: The ADR says the JSONL stores the translated `message` only (RD5: "Raw text never lands on disk"). The Integration Contract adds `ReplayErrorFrame.payload.llm_error` with a `code` field. On replay, the decoder must populate `llm_error.code`. But the JSONL only has the translated `message` — there is no `code` stored. The replay decoder would have to **re-classify** the already-translated message to recover `code`, which is unreliable (the translated message is deliberately generic; classifying "The AI service couldn't process an attachment." back to `media_unsupported` requires another classifier running in reverse). The spec's own Ambiguity Warning #7 flags this ("Default: store `code` too"), but the ADR does not adopt that resolution — it says "translate the `message`" only.
- **Impact**: `ReplayErrorFrame.payload.llm_error.code` is always empty (or always `unknown`) on replay, making the structured field useless for post-mortem analysis. The SPA's `llm-error.ts` code→display map can't render the right icon/copy on replay because it has no code.
- **Recommendation**: Adopt Ambiguity Warning #7's default as a decision: persist `code` (and `retryable`) alongside `message` in the JSONL entry. This is two extra short string fields per error entry; the cost is negligible and it makes the replay path fully populate `llm_error`. `detail` remains unpersisted (correct).

---

#### [MAJ-007] Rollback produces a mixed-state transcript — replay will show a mix of translated and raw entries

- **Lens**: Inoperability
- **Affected section**: "Rollback" (line 154: "Disabling transcode/downgrade/translation returns to today's behavior"); spec Operations & Rollback
- **Description**: The ADR says each RD layer is "independently flaggable" and disabling translation "returns to today's behavior." But the JSONL transcript is append-only with 90-day retention. After a flag toggle mid-stream, the transcript contains a mix of pre-toggle (translated) and post-toggle (raw) entries. The replay decoder renders both with the same code path. The user sees friendly translated messages for old errors and raw provider JSON for new ones (or vice versa) in the same session. The ADR's "returns to today's behavior" claim is true for the live path only, not for replay of pre-toggle entries.
- **Impact**: Operators who toggle the translation flag (the ADR's stated rollback mechanism) create an inconsistent replay UX. There is no migration to re-translate or un-translate existing entries.
- **Recommendation**: Either (a) state explicitly that rollback creates a mixed-state transcript and is acceptable (translation is a render-time concern, not a storage migration), with the replay decoder handling both shapes; or (b) make the replay decoder detect whether an entry is translated (e.g., by the presence of a `code` field per MAJ-006) and render accordingly. Option (b) is more robust and falls out of adopting MAJ-006.

---

#### [MAJ-008] Body-substring classification is unreliable because `HandleErrorResponse` truncates the body to 512 bytes before the classifier ever sees it

- **Lens**: Infeasibility
- **Affected section**: RD5 classifier (`context_too_long` via body substring); `HandleErrorResponse` (`common.go:390`); `ResponsePreview` (`common.go:451`)
- **Description**: The ADR's classifier (and the spec's dataset) relies on body-substring matching for several codes: `context_too_long` (match `"context length exceeded"`), `media_unsupported` format-rejection (match `"valid JPG, PNG, WebP, or ICO image"`), `content_policy`, etc. But `HandleErrorResponse` builds its error string via `ResponsePreview(body, 512)`, which hard-truncates the body to 512 bytes. If the provider's error JSON places the classification substring beyond byte 512 (which is common — providers often wrap errors in `{"error":{"type":"...","code":"...","message":"...","details":[...]}}` where the message is deep), the substring never reaches the classifier. The ADR does not acknowledge this truncation or its impact on classification reliability.
- **Impact**: A non-trivial fraction of provider errors will classify as `code=unknown` even when their bodies contain a recognizable signal — because the signal was truncated away before the classifier runs. The `context_too_long` matcher is particularly at risk (the substring often lives in a nested `error.details[].message` field).
- **Recommendation**: Either (a) raise the `ResponsePreview` truncation for error bodies to a larger value (e.g., 4096) and document the cost (slightly larger log lines), or (b) acknowledge the truncation as a known classification-reliability ceiling and add a dataset row where the classification substring is at byte 500+ to pin the boundary. Option (a) is the simpler fix and the structured `*ProviderError.Body` (per CRIT-001) can carry the untruncated body for classification while the log line stays truncated.

---

### MINOR Findings

#### [MIN-001] Animated GIF transcoding is silent — no note to the agent about the lost animation

- **Lens**: Incorrectness (UX)
- **Affected section**: RD1 (line 65); Consequences Negative (line 145: "animated GIF → static PNG (Q1 accepted)")
- **Description**: The stdlib `image/gif` decoder returns only the first frame of an animated GIF. RD1 transcodes it to PNG. The agent receives a still image with no signal that the user sent an animation. Q1 is "resolved (default: static PNG)" but the resolution addresses only the fidelity loss, not the *signaling* loss. The agent may legitimately need to know "this was a 3-second animation" (e.g., to decide whether to extract frames via a tool).
- **Recommendation**: Either (a) detect multi-frame GIFs (`gif.DecodeAll`) and inject a text note `"[animated GIF, N frames, first frame only]"` alongside the PNG, mirroring RD4's manifest pattern; or (b) state explicitly that animation loss is silent and accepted. Option (a) is consistent with the ADR's own "media either complete or noted-omitted" philosophy.

---

#### [MIN-002] No streaming-error-path analysis — mid-stream provider errors may bypass both choke points

- **Lens**: Incompleteness
- **Affected section**: RD5/RD6; `ChatStream` at `openai_compat/provider.go:206`
- **Description**: Chat uses streaming (`ChatStream`) for LLM responses. A mid-stream provider error (200 OK, partial SSE, then an error event) returns via `fmt.Errorf("streaming read error: %w", err)` (`provider.go:376`) — a different format from `HandleErrorResponse`. The ADR never mentions streaming errors. If a streaming error reaches `runTurn`'s error block, it does flow through `appendErrorTranscript` and `emitEvent` (so the choke points would catch it), but its message format (`"streaming read error: ..."`) is not in the classifier dataset. It will classify as `code=unknown` even when the underlying cause is a mid-stream 429 or content policy block.
- **Recommendation**: Add a note that streaming errors flow through the same choke points but with a different message prefix (`"streaming read error:"`), and either add a dataset row or explicitly accept `code=unknown` for mid-stream errors.

---

#### [MIN-003] ADR cites `driver_codex.go` without the `pkg/agent/runner/` path prefix

- **Lens**: Inconsistency
- **Affected section**: RD5 (line 87); Context (line 38)
- **Description**: The ADR refers to "`driver_codex.go ×5`" but the file is at `pkg/agent/runner/driver_codex.go`. An implementer searching for `driver_codex.go` at the repo root or in `pkg/agent/` won't find it. (Also per CRIT-002, the count is 6, not 5.)
- **Recommendation**: Cite the full path `pkg/agent/runner/driver_codex.go`.

---

#### [MIN-004] "Existing replay fixtures must update" — unenumerated migration burden

- **Lens**: Inoperability
- **Affected section**: Consequences Negative (line 147: "existing replay fixtures must update to translated text")
- **Description**: The ADR acknowledges that `appendErrorTranscript` becoming load-bearing for translation requires updating existing replay fixtures, but does not enumerate them. The spec's regression table mentions `replay_test.go:1502`. An implementer doesn't know the blast radius.
- **Recommendation**: Run `rg -l "appendErrorTranscript|ReplayErrorFrame" pkg/gateway/*_test.go pkg/agent/*_test.go` and list the affected fixture files in the ADR's Affected Components section.

---

#### [MIN-005] The options table includes a strawman ("Attach as normal file; let the provider figure it out")

- **Lens**: Overcomplexity (evidence quality)
- **Affected section**: Options Considered table (line 123)
- **Description**: The rejected option "Attach as normal file; let the provider figure it out" is not a design anyone proposed — no chat-completions primitive accepts arbitrary files. Including it inflates the decision's apparent thoroughness without adding real discrimination among options.
- **Recommendation**: Drop the strawman row or replace it with a real alternative that was actually considered (e.g., "MCP-style file tool call instead of inline media" — a genuine design alternative).

---

#### [MIN-006] `pdfCapableModel` is a hardcoded substring allow-list (Constraint #6 tension) and the ADR doesn't reconcile

- **Lens**: Inconsistency (with project constraints)
- **Affected section**: Context (line 28: "a hardcoded substring allow-list"); Constraints (line 54: "explicit seeded data over branches (C#6)")
- **Description**: The ADR correctly identifies `pdfCapableModel` as "hardcoded" and uses it as the motivation for RD3 (deferred registry). But the ADR's own classifier (`translateLLMError`) is *also* a hardcoded substring matcher — the same pattern, for a different purpose. Constraint #6 is specifically about tool-policy decisions (not error classification), so this isn't a strict violation, but the ADR cites C#6 as a force ("explicit seeded data over branches") while shipping a branch-based classifier. The tension is worth naming.
- **Recommendation**: Add one sentence acknowledging that the classifier is substring-based (not seeded-data) and explaining why C#6 doesn't apply (it targets tool-policy resolution, not error UX translation).

---

### Observations

#### [OBS-001] The RD3 deferral is correct but leaves a measurable reliability gap for non-stdlib image formats

- **Lens**: Incompleteness
- **Suggestion**: RD1 normalizes stdlib-decodable formats (PNG/JPEG/GIF) only. WEBP/BMP/TIFF/AVIF/HEIC/SVG fall to RD2 downgrade. For a user who routinely sends WEBP screenshots (common on modern platforms), every image becomes a text note in v0.1.1 — a visible UX regression from "image just works" to "image omitted." The ADR accepts this (Q3 default: defer `x/image` to v0.3). Consider naming `x/image` as a *fast-follow* (v0.1.2) rather than v0.3, since it's a pure-Go additive import with no architectural change and closes the most-common non-stdlib format (WEBP).

---

#### [OBS-002] The two-choke-point design is architecturally sound for coverage; the defects are in its wiring details

- **Lens**: Overcomplexity
- **Suggestion**: The Rev 2 "two choke points" architecture is the right call — it is simpler than per-site translation and correctly addresses CRIT-001 from the prior grill. The findings here (CRIT-001, MAJ-001 through MAJ-004) are all wiring-level: how the raw error reaches the classifier, how rate-limit dedup interacts with the write path, how model-switch messages are handled. None of them challenge the two-choke-point decision itself. The ADR's instinct to "translate at the boundaries, not the emit sites" is correct; it just under-specified the boundary payloads. Fix the payload wiring and the design holds.

---

#### [OBS-003] Consider an audit-log entry for translation events (operability)

- **Lens**: Inoperability
- **Suggestion**: The ADR promises a "counter for media-downgrade events" but (per the spec grill's MIN-004) names neither the metric nor its labels. Beyond a counter, consider an `audit.EmitEntry` for translation-classification events (`code`, agent_id, stage) — this gives operators post-mortem triage without reading `gateway.log`, consistent with the existing `Emit*` precedent in `pkg/audit/events.go`.

---

## Structural Integrity (Variant C — Generic Markdown / ADR)

**Scope clarity**: Strong. The ADR clearly separates v0.1.1 scope (RD1/2/4/5/6/7) from v0.3 (RD3) and states the incident origin concretely. The "Scope split (load-bearing)" note correctly frames the two halves as inseparable.

**Actors identified**: Adequate. The ADR names the components (`appendErrorTranscript`, WS forwarder, `HandleErrorResponse`, `translateLLMError`, SPA reducer) but does NOT name the REST executor / task-run path as a distinct actor (MAJ-005).

**Success criteria**: Weak. The ADR references SC-001/002 from the spec but does not state its own measurable success criteria. "Fixes the incident (D-A, D-B)" is not measurable without the underlying test plan. Given CRIT-001 (classifier infeasibility), the success criteria are not achievable as designed.

**Failure modes**: Partial. The ADR addresses graceful degradation (RD2 backstop, rollback) but misses: streaming-error classification (MIN-002), body-truncation-induced misclassification (MAJ-008), mixed-state replay on rollback (MAJ-007), and the REST executor path (MAJ-005).

**Implementation detail**: Insufficient for an engineer to begin work without further design. The pseudocode `translateLLMError(rawErr)` does not match any available call signature (CRIT-001). The `ErrorPayload` extension needed to make the design work is not specified. An engineer would have to design the payload threading themselves.

**Assumptions & constraints**: Mixed. The forces section (Unknowable capabilities, Hard constraints, Asymmetric failure) is excellent. But the load-bearing assumption — that the choke points have access to the raw error — is false (CRIT-001), and the emit-site enumeration assumption is wrong (CRIT-002).

---

## Test Coverage Assessment

The ADR delegates test coverage to the underlying spec (`media-handling-error-translation-spec.md` Rev 2). The spec's TDD plan is thorough for the *media* path (RD1/RD2/RD4) but has gaps for the *error-translation* path that the ADR's Rev 2 redesign introduced:

| Gap | Affected area | Recommendation |
|---|---|---|
| No test for `ErrorPayload` carrying structured error to the choke point | CRIT-001 | Add `TestErrorPayload_PreservesProviderErrorForClassifier` once the payload extension is specified |
| No test for rate-limit write-path double-translation | MAJ-001 | Add `TestAppendErrorTranscript_RateLimitKind_PreservesRetryHint` |
| No test for `model_switch` message identity preservation | MAJ-003 | Add `TestTranslateLLMError_ModelSwitchStage_PassesThrough` |
| No dataset row for internal rate-limit format (`"rate limit:"` prefix) | MAJ-004 | Add to `TestTranslateLLMError_Dataset` |
| No dataset row for streaming-error format | MIN-002 | Add `"streaming read error:"` input → `code=network` or `unknown` |
| No test for body-truncation classification boundary | MAJ-008 | Add dataset row with classification substring at byte 500+ |
| No test for replay reconstructing `code` from JSONL | MAJ-006 | Add once `code` persistence is decided |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| `translateLLMError` classifier | ok | ok | ok | **risk** | ok | ok | If body/provider-name leaks into `message` (spec grill MIN-006); ADR doesn't add negative test |
| `appendErrorTranscript` write choke point | ok | ok | **risk** | ok | ok | ok | No audit entry for translation decisions; raw `err` in `logger.ErrorCF` is the only audit trail (acceptable but unstated) |
| WS forwarder `EventKindError` case (new) | ok | ok | **risk** | ok | **risk** | ok | No rate limit on error-frame flood — a churning provider emits repeated `EventKindError`, each forwarded live, DOSing the SPA render |
| `detail` field on wire | ok | ok | ok | **risk** | ok | ok | If `detail` is computed from raw text at the forwarder (per RD7) but the raw text reaches the forwarder via a new payload field, persistence must be explicitly gated — the ADR says "never persisted" but the mechanism is unspecified (MAJ-002) |
| REST executor `EventKindError` handler | ok | ok | **risk** | **risk** | ok | ok | Surfaces raw `ev.Err.Message` from external CLI drivers directly (MAJ-005); outside the ADR's two-choke-point coverage |
| `encodeImageToDataURL` transcoder | ok | ok | ok | ok | **risk** | ok | Decompression-bomb PNG/JPEG decode cost; mitigated by existing `maxSize` (value not pinned in ADR — spec grill MIN-010) |

**Legend**: risk = identified threat not mitigated in ADR, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **How does the raw error (or structured `*ProviderError`) physically reach `translateLLMError` at each choke point?** The ADR's pseudocode assumes it's there; the code says it isn't. (CRIT-001)
2. **Is the REST executor / task-run path in scope for the "no raw provider text" invariant or not?** (MAJ-005)
3. **What happens to `recordRateLimitDenial`'s carefully-formatted retry hint when the write choke point translates it?** (MAJ-001)
4. **Where does `detail` actually come from at the forwarder, given the forwarder only has `ErrorPayload{Stage, Message}`?** (MAJ-002)
5. **Does `model_switch` (which embeds model slugs in the message) get translated to generic copy (losing info) or passed through (violating the identity invariant)?** (MAJ-003)
6. **How does replay reconstruct `llm_error.code` when only the translated `message` is persisted?** (MAJ-006)
7. **What happens to replay UX when an operator toggles the translation flag mid-retention-window?** (MAJ-007)
8. **Has anyone audited whether 512-byte body truncation in `HandleErrorResponse` clips the classifier's substrings in practice?** (MAJ-008)
9. **Why are runner-channel `EventKindError` sites (20 of them across 5 driver files) listed as bus-level sites in the ADR's coverage argument?** (CRIT-002)

---

## Verdict Rationale

**Verdict: BLOCK.** Two critical defects each independently make the ADR's central Rev 2 design not implementable as described.

**CRIT-001** is the more fundamental: the ADR's pseudocode `translateLLMError(rawErr)` presupposes a raw `error` at the choke points that does not exist there. The bus event carries `ErrorPayload{Stage, Message}` — a pre-formatted string. The write choke point takes `(kind, stage, message string)`. The structured `*ProviderError` the ADR adds to `HandleErrorResponse` to fix MAJ-001 is collapsed to a string at the emit site, one layer above the choke point. The ADR's stated fix for MAJ-001 therefore does not work; an implementer will either fall back to the substring parsing MAJ-001 rejected, or will have to design an `ErrorPayload` extension the ADR does not mention. Either path contradicts a stated ADR decision. This must be resolved (pick option 1 or 2 from CRIT-001's recommendation) before ratification.

**CRIT-002** is a factual-reliability defect: the ADR's "~10 emit sites" count is wrong (actual: 29), the `driver_codex.go ×5` claim is wrong (6, at a different path), and four other driver files with 14 emit sites are unmentioned. The two-choke-point design still covers the bus-level sites correctly, but the ADR's *argument* for that coverage conflates the in-memory bus with the runner channel — and an implementer who trusts the ADR's accounting will not know to audit new runner drivers. Correct the enumeration and name the actual coverage mechanism (intermediate re-emission via `spawnSubTurn`).

The eight MAJOR findings are wiring-level: rate-limit double-translation at the write choke point, `detail` provenance, model-switch identity, dual rate-limit formats, REST executor scope, replay `code` reconstruction, rollback mixed-state, and body-truncation reliability. None challenges the two-choke-point architecture itself (OBS-002) — they collectively show the ADR under-specified the boundary payloads and their interactions. Several are one-sentence fixes; none requires re-architecture.

### Recommended Next Actions

- [ ] **CRIT-001**: Decide and document how the raw error reaches `translateLLMError` at each choke point. Recommended: extend `ErrorPayload` with a `*ProviderError` field and thread it through every emit site (most have `err` in scope). Update the pseudocode to match.
- [ ] **CRIT-002**: Correct the emit-site enumeration (9 bus-level + 20 runner-channel), cite correct paths, and name the intermediate-re-emission coverage mechanism.
- [ ] **MAJ-001**: Gate write-choke-point translation on `kind != "rate_limit"` to preserve the retry hint.
- [ ] **MAJ-002**: Specify where `detail` is sourced from (it cannot be "the in-memory error at the forwarder" — there isn't one).
- [ ] **MAJ-003**: Add a `stage=model_switch` carve-out (and any other agent-composed message) to the translation rules.
- [ ] **MAJ-004**: Add the internal `"rate limit:"` message format to the classifier dataset.
- [ ] **MAJ-005**: Explicitly scope the invariant to the chat WS path, or add a third choke point for REST executor.
- [ ] **MAJ-006**: Adopt spec Ambiguity Warning #7 — persist `code` alongside `message`.
- [ ] **MAJ-007/008**: Acknowledge mixed-state replay on rollback; either raise body truncation or accept the reliability ceiling.
- [ ] **MIN-001 through MIN-006**: Address during the revision pass.
- [ ] **Re-run `/grill-spec` on the revised ADR** before operator ratification; the ADR should not proceed to implementation until CRIT-001/002 are closed.
