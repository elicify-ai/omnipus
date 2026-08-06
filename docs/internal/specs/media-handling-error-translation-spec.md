# Feature Specification: Media Handling & Provider-Error Translation (v0.1.1 Stability)

**Created**: 2026-07-21
**Status**: Draft (Revision 3 — operator decisions locked 2026-07-21; ADR-grill round-2 wiring findings corrected)
**Branch**: `release/v0.1.1`
**Input**: Operator UAT defect 2026-07-21 (xAI image-rejection 400 + raw error blob surfaced); ADR-051; `/grill-spec` reviews (spec round 1 BLOCK→corrected; ADR round 2 BLOCK→architecture endorsed, wiring pending).
**Implements**: ADR-051 (Provider-Capability-Aware Media Handling and User-Facing Error Translation)
**Scope release**: `release/v0.1.1` (RD1 incl. `x/image`, RD2, RD4, RD5, RD6, RD7). RD3 (capability registry) is a v0.3 non-goal.

> **Revision 3 (2026-07-21) — operator decisions locked:** Q1 animated GIF → transcode to static PNG. Q2 raw `detail` → ships on wire, rendered **only under Verbose Chat** (`verboseChatEnabled`, `src/store/chatPreferences.ts`), not DEV mode. Q3 → **`x/image` IN v0.1.1** (transcode WebP/BMP/TIFF; one new pure-Go dep). Q4 e2e → `$OMNIPUS_E2E_NO_VISION_MODEL` env var, **fail if unset**, preseeded `deepseek/deepseek-chat` (OpenRouter-verified `input_modalities:["text"]`, `tools:true`, 131072 ctx). Also folded in the ADR-grill round-2 wiring corrections: thread `*ProviderError` through `ErrorPayload` (CRIT-001); correct the emit-site coverage argument (CRIT-002); rate-limit skip at write (MAJ-001/004); model_switch sanitize (MAJ-003); REST-executor in-scope (MAJ-005); persist `code` (MAJ-006); rollback mixed-state accepted (MAJ-007); classify pre-truncation (MAJ-008).
>
> **Revision 2 (2026-07-21):** Spec grill BLOCK corrections — translation moved to **two boundary choke points** (`appendErrorTranscript` write + WS-forwarder live `EventKindError` case); incident xAI string pinned; `LLMError` AsyncAPI integration specified; MAJ-001…011 addressed.

---

## Decisions Locked (operator-confirmed via ADR-051)

| # | Decision |
|---|---|
| D1 | Normalize outbound images to **PNG** via pure-Go transcode — **stdlib** (PNG/JPEG/GIF) **+ `golang.org/x/image`** (WebP/BMP/TIFF) — before sending (operator Q3). AVIF/HEIC/SVG (non-decodable without CGo) fall to D2. Animated GIF → static PNG first frame (operator Q1). |
| D2 | On a **media-class** provider rejection (image/PDF block rejected), **downgrade the offending media and retry the turn exactly once**. Never retry content-policy or unclassified rejections more than once. |
| D3 | **No provider-capability registry in v0.1.1.** Optimize-only; deferred to v0.3. Reliability = D1+D2, not prediction. |
| D4 | **No file type other than normalized images (D1) and gated PDFs is ever sent as a raw binary content block.** Archives → manifest note; opaque binaries → metadata note + agent-tool inspection. |
| D5 | Raw provider errors **never persist and never cross the gateway→SPA boundary in production.** Translate at **two boundary choke points** — (a) `appendErrorTranscript` (`turn.go:1152`, single write choke point) and (b) the new `case agent.EventKindError:` in the WS forwarder (single live choke point). Both call one shared pure `translateLLMError`. **Wiring requirement (ADR-grill CRIT-001):** extend `ErrorPayload` (`pkg/agent/events.go`) with `*ProviderError` so the choke-point classifier sees real HTTP status/body instead of a stringified message. Replay reads already-translated text. Raw `err` only in `logger.ErrorCF` + wrapped `error`. |
| D6 | Add the missing `case agent.EventKindError:` to the WS forwarder so translated errors render **live**, using the `LLMError` shape, carrying the transcript entry id (SPA dedupes vs replay). Suppress the error frame when `code==rate_limited` (RateLimitFrame authoritative). |
| D7 | SPA renders the translated `message` always; `detail` **ships on the wire** and is rendered **only when Verbose Chat is enabled** (`verboseChatEnabled`, `src/store/chatPreferences.ts` — the persisted Settings → Chat toggle) behind a "Technical details" disclosure. Stripped otherwise. `detail` computed live at the forwarder, **never persisted.** (operator Q2) |
| D8 | Wire changes via the contract pipeline (`contracts/asyncapi.yaml` → `make gen-contracts`); no hand-written wire types (Constraint #8). |

**Operator decisions resolved (2026-07-21):** Q1 static PNG · Q2 Verbose-Chat-gated `detail` · Q3 `x/image` IN v0.1.1 · Q4 env-var fail-if-unset preseed `deepseek/deepseek-chat`.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `appendErrorTranscript` (`pkg/agent/turn.go:1152`) | **modifies (choke point #1)** | Translate `message` before JSONL. Single write site for ALL persisted errors. |
| WS forwarder switch (`pkg/gateway/websocket.go:3068-3461`) | **modifies (choke point #2)** | Add `case agent.EventKindError:` → translate + emit live `LLMError` frame. |
| `ErrorPayload` (`pkg/agent/events.go:476-520`) | **modifies (CRIT-001)** | Add `*ProviderError` field so the classifier sees status/body at both choke points. |
| `translateLLMError` (new, `pkg/agent`) | new | Shared pure classifier: `*ProviderError`/raw → `LLMError`. Called at both choke points. |
| `encodeImageToDataURL` (`pkg/agent/loop_media.go:445`) | modifies | PNG-normalization (stdlib + `x/image`). |
| `runTurn` error block (`pkg/agent/loop.go:7277-7300`) | modifies | D2 retry-on-downgrade. |
| `isImageInputRejection` (`pkg/agent/loop.go:7254`) | **replaces** | Returns false for the incident string (CRIT-002); replaced by the dataset-driven classifier. Removed. |
| `downgradePDFMediaToText` (`pkg/agent/loop_media.go:215`) | calls | Reused by D2. |
| `recordRateLimitDenial` (`pkg/agent/loop.go:1028`) | modifies | Reconcile dual-emit (D6 suppress). |
| `docextract.Extract` / `IsExtractable` (`pkg/docextract/extract.go:45,110`) | extends | D4 archive manifest + OOXML sniff. |
| `HandleErrorResponse` (`pkg/providers/common/common.go:390`) | modifies | Return structured `*ProviderError{Status, Body, Err}` (stringer preserved) — MAJ-001; do NOT truncate body to 512B before handing to the classifier (MAJ-008). |
| `ReplayErrorFrame` + `pkg/gateway/inboundschemas/ReplayErrorFrame.yaml` | extends | Add `payload.llm_error: LLMError`. |
| `case 'error'` reducer (`src/store/chat.ts:3086,3259-3266`) | modifies | Render translated `message`; dedupe live/replay by entry id; leave kickoff-reject/cancel-ack sub-paths untouched. |
| `src/lib/llm-error.ts` | new | `code`→display map; render `detail` only when `verboseChatEnabled`. |
| `src/store/chatPreferences.ts` (`verboseChatEnabled`) | reads | Gate for `detail` disclosure. |
| `go.mod` | modifies | Add `golang.org/x/image` (Q3). |
| `DelegationDeniedResult` (`pkg/tools/result.go:202`) | reference | Typed-wire template. |

### Impact Assessment

| Symbol Modified | Risk | Notes |
|---|---|---|
| `appendErrorTranscript` | **HIGH** | Single translation-at-write site for every persisted error. |
| `ErrorPayload` (+ `*ProviderError`) | MEDIUM | Bus payload change; thread structured error to choke points (CRIT-001). |
| WS forwarder `EventKindError` case | MEDIUM | New live choke point. |
| `HandleErrorResponse` (structured err) | MEDIUM | All provider HTTP error paths; stringer preserved. |
| `encodeImageToDataURL` (+x/image) | MEDIUM | New decode formats. |
| `ReplayErrorFrame` (contract) | MEDIUM | Regenerated Go + zod. |
| `case 'error'` reducer | MEDIUM | Render + dedupe. |

**HIGH-risk callout:** `appendErrorTranscript` becomes the single translation-at-write site. Mitigation: translation is an additive pure transform of the `message` arg; signature stays `(kind, stage, message)`; raw `err` still logged at each emit site; replay fixtures updated to translated text.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Any `EventKindError` (now carrying `*ProviderError`) → bus → new WS case → `translateLLMError` → live `LLMError` frame → SPA | D-B live; choke point #2. |
| Any error persist → `appendErrorTranscript` → `translateLLMError` → translated JSONL → replay → `ReplayErrorFrame`(+`llm_error`) → SPA | D-B replay; choke point #1. |
| Attachment → `resolveMessageMedia` → `encodeImageToDataURL` (D1 normalize, stdlib+x/image) → provider | D-A; D2 downgrades+retries. |

---

## User Stories & Acceptance Criteria

### User Story 1 — Unsupported/corrupt images no longer kill the turn (Priority: P1)

As a chat user, when an image the provider cannot accept is attached, the turn completes instead of dying.

**Acceptance Scenarios**:
1. JPEG → provider receives PNG data URL; turn succeeds.
2. Image the provider rejects (SVG/AVIF non-decodable, OR xAI "valid JPG, PNG, WebP, or ICO" format-rejection) → downgrade + retry once → succeeds.
3. Provider rejects after downgrade → turn ends with a **translated** error (no empty reply, no raw JSON).

### User Story 2 — Archives reveal structure; binaries never go raw (Priority: P2)

**Acceptance Scenarios**:
1. `.zip` → manifest; no zip bytes reach the provider.
2. `.tar.gz` → manifest (covers tar/gz net-new imports).
3. `.exe` → metadata note; no exe bytes reach the provider.

### User Story 3 — Office docs with wrong extensions still extract (Priority: P2)

1. OOXML with non-matching extension/MIME → `IsExtractable` true via magic bytes → `Extract` returns text.

### User Story 4 — Provider errors are meaningful, never raw (Priority: P1)

**Acceptance Scenarios**:
1. Provider 400 with JSON body → rendered message classified/generic; raw JSON **not** in DOM; only in `gateway.log`.
2. Provider 429 → retry advice, `retryable=true`; RateLimitFrame authoritative (no duplicate bubble).
3. **Verbose Chat on** + translated error with `detail` → "Technical details" disclosure shows `detail`; **Verbose Chat off** → disclosure/`detail` absent. (operator Q2)
4. Error live then reload → rendered exactly once (entry-id dedupe).

### User Story 5 — Errors appear live (Priority: P1)

1. Live WS + provider error → `EventKindError` → gateway forwards translated `LLMError` frame → renders without reload.

### User Story 6 — Real-LLM fallback with a vision-less, PDF-less model (Priority: P1)

As an operator/developer, I want an e2e test using a **real** LLM that supports tools but **not** images/PDF, proving the downgrade fallback against a live provider.

**Acceptance Scenarios**:
1. Agent on `$OMNIPUS_E2E_NO_VISION_MODEL` (preseed `deepseek/deepseek-chat`; **test fails if unset**) → user sends image → turn completes via downgrade; no empty reply; no raw provider error.
2. Same agent → user sends PDF → turn completes via PDF-to-text downgrade.
3. Any provider error during these turns → no raw provider JSON in DOM/console.

---

## Behavioral Contract

**Primary:** outbound decodable image (PNG/JPEG/GIF/WEBP/BMP/TIFF) → provider receives `data:image/png;base64,…`. Provider rejects image/PDF → downgrade → retry once. `docextract` meets zip/tar/gz/tgz → manifest; opaque binary → note. Error persist → `appendErrorTranscript` translates. Error live → WS forwarder `EventKindError` translates.

**Error flows:** media-unsupported/provider-rejected → code per taxonomy (media → downgrade+one retry); 429 → `rate_limited` (RateLimitFrame authoritative, error frame suppressed); 408/5xx/timeouts → `network`, retryable; unclassified → `unknown`.

**Boundary:** at most one media downgrade-retry per turn; image over `maxSize` → existing too-large note; AVIF/HEIC/SVG → D2 downgrade; `detail` never persisted, Verbose-Chat-rendered; live frame carries entry id.

---

## Media & Error Semantics (normative)

### Media send invariant
1. Raw-binary content blocks sent to any provider are limited to PNG-normalized images and gated PDFs.
2. Normalization is best-effort; downgrade is the guarantee.
3. One retry per media rejection.

### Error translation invariant
4. Two choke points, one function. `translateLLMError` at `appendErrorTranscript` (write) + WS forwarder `EventKindError` (live). Coverage by construction (incl. subturns + runner drivers via bus).
5. Raw never persists, never ships in prod. `detail` ships but renders only under Verbose Chat; never persisted.
6. Generic over specific. Provider identity/raw text never in `message`.
7. Live == replay, deduped by entry id.

### `LLMError` code taxonomy
| code | trigger | retryable | default user message |
|---|---|---|---|
| `media_unsupported` | provider rejected image/PDF block (capability-absence OR format-rejection) | false | "The AI service couldn't process an attachment. I've continued without it." |
| `provider_rejected` | non-media/non-policy rejection; incl. HTTP 413 | false | "The AI service rejected the request." |
| `rate_limited` | 429 / quota | true | "The AI service is busy. Please retry shortly." (RateLimitFrame authoritative) |
| `network` | 408/5xx/timeout/connection | true | "Couldn't reach the AI service. Please retry." |
| `content_policy` | provider flagged content | false | "The AI service declined this request." |
| `context_too_long` | body indicates window exceeded (substring; not HTTP 413) | false | "The conversation is too long for the model; trim and retry." |
| `unknown` | unclassified | false | "The AI service encountered an error." |

### Classifier: two pattern classes (CRIT-002)
- **Capability-absence** — no vision: `"does not support image input"`, `"image input not supported"`, etc.
- **Format-rejection** — has vision, rejects a format: incl. the **incident string** `"valid JPG, PNG, WebP, or ICO image"`, `"unsupported image format"`.
Both → `code=media_unsupported`. Incident string pinned as dataset row #3.

---

## Edge Cases

- Animated GIF → static PNG (Q1). AVIF/HEIC/SVG/WEBP-not-decodable → D2 downgrade.
- Password-protected ZIP → names + "protected" note. Zip-bomb → capped manifest + truncation note.
- Corrupt image decoding to nothing → non-decodable → downgrade.
- One of N images rejected → downgrade only that one.
- `detail` with Verbose Chat off → not rendered.
- Rate-limit as `EventKindError` → suppressed.
- Sub-turn/external-CLI error → covered by choke points.
- HTTP 413 → `provider_rejected` (not `context_too_long`).
- Body >512B → classify on structured `*ProviderError` before truncation (MAJ-008).

---

## Explicit Non-Behaviors

- Must not send any file type other than normalized images and gated PDFs as raw binary content blocks.
- Must not retry a media rejection more than once.
- Must not render raw provider text/identity/status body unless Verbose Chat is on (and only `detail`, behind a disclosure).
- Must not persist raw provider text or `detail`.
- Must not translate at individual emit sites (choke points only).
- Must not introduce a capability registry, audio/video, or CGo in v0.1.1.
- Must not modify `send_file` or channel-side format handling.
- Must not hand-write the `LLMError` wire type.

---

## Integration Boundaries

### `image` stdlib + `golang.org/x/image` (decode → PNG encode)
- **Data in**: sandbox-resolved image path. **Data out**: PNG bytes → base64 data URL.
- **Contract**: decode (png/jpeg/gif via stdlib; webp/bmp/tiff via x/image) → `image.Image` → `png.Encode`. Decode error → `""`.
- **On failure**: non-decodable (AVIF/HEIC/SVG) → no transcode → D2. **Development**: stdlib + `golang.org/x/image` (new pure-Go dep, Q3).

### `archive/zip`, `archive/tar`, `compress/gzip` (manifest)
- **Data in**: archive path. **Data out**: manifest text, capped. **On failure**: corrupt/encrypted → note. **Development**: stdlib.

### `contracts/asyncapi.yaml` → `LLMError` + live `error` message (CRIT-003)
- **Contract**: component `LLMError` (`code/message/retryable/detail`); `ReplayErrorFrame.payload.llm_error: LLMError`; new live message `error` (`WsFrameTypeError`, `payload: LLMError`). **Development**: 5-step add-a-wire-type; `make verify-contracts`.

### `HandleErrorResponse` structured error (MAJ-001/008)
- **Data in**: HTTP status + full body. **Data out**: `*ProviderError{Status, Body, Err}` (stringer preserved). **On failure**: non-HTTP → `Status=0` → substring match. Body handed to the classifier **before** any 512B log-truncation.

---

## Wire & Engine Design (normative)

### `LLMError` component schema
```yaml
LLMError:
  type: object
  required: [code, message, retryable]
  additionalProperties: false
  properties:
    code:       { type: string, enum: [media_unsupported, provider_rejected, rate_limited, network, content_policy, context_too_long, unknown] }
    message:    { type: string, minLength: 1 }
    retryable:  { type: boolean }
    detail:     { type: string, description: "Ships on wire; rendered only under Verbose Chat; never persisted" }
```

### Translation function (new, pure)
```go
// translateLLMError classifies a provider/LLM error into a user-facing LLMError.
// Called at both choke points. Input is the *ProviderError threaded via ErrorPayload
// (CRIT-001); falls back to substring matching when status is unavailable.
// detail is derived from the raw error; NEVER persisted; rendered only under Verbose Chat.
func translateLLMError(pe *ProviderError) (code, message string, retryable bool, detail string)
```

### Choke-point wiring
```
appendErrorTranscript(kind, stage, message, pe *ProviderError):     // CRIT-001: pe threaded through
    if code==rate_limited { msg = message /* already friendly */ } else {
        code, msg, _, _ := translateLLMError(pe)                     // write choke point
    }
    write JSONL entry { kind, stage, message: msg, code }            // raw never on disk; code persisted (MAJ-006)

WS forwarder, new case agent.EventKindError (ev carries *ProviderError + entryId):
    if ev.code==rate_limited { skip }                                // D6 dedup
    code, msg, retryable, detail := translateLLMError(ev.pe)         // live choke point
    send ErrorFrame { payload: LLMError{code,msg,retryable,detail}, entryId: ev.entryId }
```
The `runTurn` error block keeps D2 (downgrade+retry) and raw `err` in `logger.ErrorCF` + `fmt.Errorf(%w)`; it no longer translates. `model_switch` messages are sanitized by the same classifier (no model-name identity leak — MAJ-003). REST-executor (subagent_3p) CLI errors crossing to the SPA are routed through `translateLLMError` at the REST error-response seam (MAJ-005, in-scope).

---

## Operations & Rollback

- **Observability:** raw provider error always in `gateway.log`; translated `code`/`message` on the wire; `code` persisted for replay reconstruction (MAJ-006). Counter for media-downgrade events.
- **Feature flags:** D1, D2, D5(write), D6(live), D7 each independently flaggable; default on.
- **Rollback:** disabling returns to today's behavior for that layer; raw always logged. Mixed translated+raw transcript entries during/after rollback are acceptable and documented (MAJ-007).
- **Contract drift:** `make verify-contracts` is the gate.

---

## BDD Scenarios

### Feature: Media Handling & Provider-Error Translation

#### Background
- **Given** the gateway is running on `release/v0.1.1`
- **And** a stub provider programmable to reject media / return errors

#### Scenario: JPEG is normalized to PNG and accepted
- **Traces to**: US-1, Scenario 1 · **Category**: Happy Path
- **Given** an attached JPEG within size limit **When** the turn runs **Then** the provider receives `data:image/png;base64,…` **And** the turn succeeds

#### Scenario: WebP/BMP/TIFF normalized via x/image
- **Traces to**: US-1 · **Category**: Happy Path
- **Given** an attached WebP/BMP/TIFF **When** transcoded **Then** the provider receives PNG (x/image decode) **And** the turn succeeds

#### Scenario: xAI-style format rejection downgrades and retries
- **Traces to**: US-1, Scenario 2 · **Category**: Error Path
- **Given** a provider returning 400 body "valid JPG, PNG, WebP, or ICO image" **When** classified **Then** code is `media_unsupported` (format-rejection) **And** downgrade + retry once **And** retry succeeds

#### Scenario: Capability-absence downgrades all images
- **Traces to**: US-1, Scenario 2 · **Category**: Error Path
- **Given** "does not support image input" **When** classified **Then** code is `media_unsupported` (capability-absence) **And** images downgraded

#### Scenario: AVIF/HEIC/SVG downgrade (no x/image decode)
- **Traces to**: US-1 · **Category**: Edge Case
- **Given** an AVIF/HEIC/SVG **When** non-decodable **Then** D2 downgrade note (no transcode) **And** turn succeeds

#### Scenario: Second media rejection terminates with translated error
- **Traces to**: US-1, Scenario 3 · **Category**: Error Path
- **Given** rejection after downgrade **When** retry also rejects **Then** turn ends with `code=media_unsupported` **And** no raw JSON in output **And** raw only in `gateway.log`

#### Scenario: ZIP and TAR.GZ yield manifests
- **Traces to**: US-2 · **Category**: Alternate Path
- **Given** `.zip` and `.tar.gz` **When** processed **Then** each yields an entry manifest **And** no archive bytes reach the provider

#### Scenario: EXE yields a metadata note
- **Traces to**: US-2 · **Category**: Alternate Path
- **Given** `.exe` **When** processed **Then** metadata note; no exe bytes reach the provider

#### Scenario: OOXML with wrong extension extracts via magic bytes
- **Traces to**: US-3 · **Category**: Edge Case
- **Given** a `.docx` renamed `.zip` **When** `IsExtractable` evaluated **Then** true via magic bytes **And** `Extract` returns text

#### Scenario: Provider 400 surfaces translated, not raw
- **Traces to**: US-4, Scenario 1 · **Category**: Error Path
- **Given** provider 400 with `{"error":{"message":"Provider returned error",...}}` **When** it reaches the SPA **Then** rendered message is classified/generic **And** "Provider returned error" not in DOM **And** raw JSON only in `gateway.log`

#### Scenario: Rate limit deduped against RateLimitFrame
- **Traces to**: US-4, Scenario 2 · **Category**: Error Path
- **Given** 429 **When** it flows **Then** RateLimitFrame authoritative **And** no duplicate bubble

#### Scenario: detail visible only under Verbose Chat
- **Traces to**: US-4, Scenario 3 · **Category**: Alternate Path
- **Given** a translated error with `detail` **When** rendered with Verbose Chat ON **Then** "Technical details" disclosure shows `detail` **But** with Verbose Chat OFF the disclosure/`detail` are absent

#### Scenario: Live error dedupes on reload
- **Traces to**: US-4, Scenario 4 · **Category**: Edge Case
- **Given** a live error frame then reload **When** replay renders **Then** the error appears exactly once (entry-id dedupe)

#### Scenario: Live error renders without reload
- **Traces to**: US-5 · **Category**: Happy Path
- **Given** a live WS session **When** the loop emits `EventKindError` **Then** the gateway forwards a translated `LLMError` frame **And** the SPA renders it without reload

#### Scenario: Real vision-less model — image fallback (e2e)
- **Traces to**: US-6, Scenario 1 · **Category**: Happy Path
- **Given** an agent on `$OMNIPUS_E2E_NO_VISION_MODEL` (preseed `deepseek/deepseek-chat`; **fails if unset**) **When** the user sends an image **Then** the turn completes via downgrade **And** no empty reply **And** no raw provider error in DOM/console

#### Scenario: Real vision-less model — PDF fallback (e2e)
- **Traces to**: US-6, Scenario 2 · **Category**: Happy Path
- **Given** the same agent **When** the user sends a PDF **Then** the turn completes via PDF-to-text downgrade

#### Scenario Outline: Error classification by provider signal
- **Traces to**: US-4 · **Category**: Error Path
- **Given** a provider returning `<signal>` **When** translated **Then** code is `<code>` and retryable is `<retryable>`

**Examples**:
| signal | code | retryable |
|---|---|---|
| HTTP 429 | rate_limited | true |
| HTTP 408 / timeout | network | true |
| HTTP 503 | network | true |
| HTTP 413 | provider_rejected | false |
| body "context length exceeded" | context_too_long | false |
| body "valid JPG, PNG, WebP, or ICO image" | media_unsupported | false |
| HTTP 400 (other) | provider_rejected | false |
| unparseable garbage | unknown | false |

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope |
|---|---|
| Unit | transcode (incl. x/image formats), classifier (dataset-driven), manifest, OOXML sniff, SPA error map |
| Integration | `appendErrorTranscript` translation + `ErrorPayload` threading + WS forwarder case + downgrade retry |
| E2E (stub) | full chat turn with stubbed rejection |
| E2E (real LLM) | vision-less/PDF-less model fallback (fail-if-unset) |

### Test Implementation Order

| Order | Test Name | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestEncodeImageToDataURL_NormalizesJPEGToPNG` | Unit | JPEG normalized | JPEG → PNG |
| 1b | `TestEncodeImageToDataURL_NormalizesWebP/BMP/TIFF` | Unit | WebP/BMP/TIFF normalized | x/image decode → PNG |
| 2 | `TestEncodeImageToDataURL_NonDecodableReturnsEmpty` | Unit | AVIF/SVG downgrade | AVIF/HEIC/SVG → `""` |
| 3 | `TestTranslateLLMError_Dataset` | Unit | classification outline | table incl. incident string (#3), capability-absence, 413→provider_rejected, context substring, >512B body |
| 4 | `TestTranslateLLMError_MessageGeneric_NoRawBody` | Unit | Provider 400 translated | raw JSON absent |
| 5 | `TestAppendErrorTranscript_TranslatesAndNeverPersistsRaw` | Integration | Provider 400 translated | JSONL translated; code persisted; raw absent |
| 6 | `TestErrorPayload_ThreadsProviderError` | Integration | (CRIT-001) | `*ProviderError` reaches both choke points |
| 7 | `TestWebsocket_ForwardsEventKindError_Translated_Live` | Integration | Live error renders | translated frame, entry id set |
| 8 | `TestWebsocket_SuppressesErrorWhenRateLimitAuthoritative` | Integration | Rate limit deduped | no duplicate |
| 9 | `TestDowngrade_OnMediaRejection_RetriesOnce` | Integration | Second rejection | one retry |
| 10 | `TestDowngrade_NeverRetriesTwice` | Integration | Second rejection | no 2nd retry |
| 11 | `TestDocextract_ZipManifest` / `_TargzManifest` | Unit | ZIP/TAR.GZ manifest | zip + tar.gz |
| 12 | `TestDocextract_OpaqueBinaryMetadata` | Unit | EXE metadata | note, no bytes |
| 13 | `TestIsExtractable_OOXMLMagicBytes` | Unit | OOXML magic bytes | docx-as-zip |
| 14 | `TestHandleErrorResponse_ReturnsStructuredProviderError` | Unit | classification | status readable; body pre-truncation |
| 15 | `llm-error.test.ts` code→message + Verbose-Chat disclosure | Unit | detail Verbose-Chat | `verboseChatEnabled` gates disclosure |
| 16 | `chat-store.error.test.ts` renders translated; dedupes by entry id | Unit | Provider 400 translated | uses `message`; dedupes |
| 17 | e2e (stub): `media-rejection.spec.ts` full turn | E2E | xAI rejection | reproduce incident |
| 18 | e2e (real): extend `tests/e2e/media.spec.ts` — vision-less image fallback | E2E (real) | Real image fallback | `$OMNIPUS_E2E_NO_VISION_MODEL` (preseed `deepseek/deepseek-chat`); **fail if unset** |
| 19 | e2e (real): extend `tests/e2e/media.spec.ts` — vision-less PDF fallback | E2E (real) | Real PDF fallback | same model |

### Test Datasets

#### Dataset: error classification (`TestTranslateLLMError_Dataset`)
| # | Input | Boundary Type | Expected | Notes |
|---|---|---|---|---|
| 1 | HTTP 429 + body | happy | rate_limited/true | |
| 2 | HTTP 503 + body | error | network/true | |
| 3 | body "valid JPG, PNG, WebP, or ICO image" (400) | **regression (incident)** | media_unsupported/false | pinned |
| 4 | body "does not support image input" | regression | media_unsupported/false | capability-absence |
| 5 | body "context length exceeded" | boundary | context_too_long/false | substring, not 413 |
| 6 | HTTP 413 | boundary (max) | provider_rejected/false | NOT context_too_long |
| 7 | HTTP 400 generic body | error | provider_rejected/false | |
| 8 | HTTP 408 timeout | error | network/true | |
| 9 | unparseable garbage | adversarial | unknown/false | conservative |
| 10 | body >512B with signal near end | boundary | correct code | classify pre-truncation (MAJ-008) |

#### Dataset: archive manifest
| # | Input | Expected | Notes |
|---|---|---|---|
| 1 | 2-entry zip | 2 entries | |
| 2 | 2-entry tar.gz | 2 entries | net-new import |
| 3 | 1000-entry zip | truncated + note | zip-bomb cap |
| 4 | password-protected zip | names + "protected" | |
| 5 | corrupt zip | note + reason | |

### Regression Test Requirements

| Existing Behaviour | New Regression Test |
|---|---|
| `pdfCapableModel` PDF gating | `TestDowngrade_PDFNonCapableModel_TextFallback` |
| `isImageInputRejection` one-off | removed; covered by dataset #3/#4 (CRIT-002) |
| `recordRateLimitDenial` dual-emit | `TestWebsocket_SuppressesErrorWhenRateLimitAuthoritative` |
| Raw error in transcript | update fixtures to translated msg + `code` |
| `media.spec.ts` existing e2e | extend with vision-less-model fallback (#18/#19) |

---

## Functional Requirements

- **FR-001**: MUST normalize outbound images to PNG via stdlib (PNG/JPEG/GIF) **+ `golang.org/x/image`** (WebP/BMP/TIFF) when decodable (operator Q3).
- **FR-002**: MUST, on a media-class rejection, downgrade the offending media and retry exactly once.
- **FR-003**: MUST NOT retry a media rejection more than once.
- **FR-004**: SHOULD inject a manifest for `.zip/.tar/.gz/.tgz`; MUST cover tar/gz with tests.
- **FR-005**: SHOULD detect OOXML by magic bytes regardless of extension/MIME.
- **FR-006**: MUST NOT render raw provider text/identity/status body unless Verbose Chat is on (only `detail`, behind a disclosure).
- **FR-007**: MUST translate at the two choke points (`appendErrorTranscript` + WS-forwarder `EventKindError`) via shared `translateLLMError`; MUST NOT translate at emit sites.
- **FR-007a**: MUST thread `*ProviderError` through `ErrorPayload` so choke points see status/body (CRIT-001).
- **FR-008**: MUST persist only translated `message` + `code`; raw `err` only in logs; `detail` never persisted.
- **FR-009**: MUST forward `EventKindError` as a live `LLMError` frame with entry id; MUST suppress when `code==rate_limited`.
- **FR-010**: MUST render translated `message`; MUST gate `detail` on `verboseChatEnabled` (Verbose Chat); MUST dedupe live vs replay by entry id.
- **FR-011**: MUST define `LLMError` in `contracts/`, add `ReplayErrorFrame.payload.llm_error`, add live `error` message, regenerate.
- **FR-012**: MUST classify via a dataset-driven classifier covering both pattern classes with the incident string pinned.
- **FR-013**: MUST extend `tests/e2e/media.spec.ts` with a real-LLM fallback using `$OMNIPUS_E2E_NO_VISION_MODEL` (preseed `deepseek/deepseek-chat`); **MUST fail (not skip) if unset** (operator Q4).
- **FR-014**: NON-GOAL (v0.3): capability registry, audio/video.

---

## Success Criteria

- **SC-001**: Incident reproduction → successful turn (normalized or downgraded); no empty reply.
- **SC-002**: No test asserts raw provider JSON reaches the SPA or persists; raw only in `gateway.log`.
- **SC-003**: Live provider error renders without reload.
- **SC-004**: All gates green — gofmt(0), golangci(0), `go test`(0), vitest(0), `npm run typecheck`(0), `make verify-contracts`(0).
- **SC-005**: Only new dependency is `golang.org/x/image` (pure-Go).
- **SC-006**: Real vision-less/PDF-less model e2e passes (fails loudly if `$OMNIPUS_E2E_NO_VISION_MODEL` unset).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | JPEG normalized; WebP/BMP/TIFF normalized | #1, #1b |
| FR-002 | US-1 | xAI rejection; capability-absence; second rejection; AVIF/SVG downgrade | #9, #10 |
| FR-003 | US-1 | second rejection | #10 |
| FR-004 | US-2 | ZIP/TAR.GZ manifest | #11 |
| FR-005 | US-3 | OOXML magic bytes | #13 |
| FR-006 | US-4 | Provider 400 translated; detail Verbose-Chat | #4, #15, #16 |
| FR-007/7a | US-4 | (choke points) Provider 400 translated; live | #5, #6, #7 |
| FR-008 | US-4 | Provider 400 translated | #4, #5 |
| FR-009 | US-4, US-5 | Rate-limit deduped; live renders | #7, #8 |
| FR-010 | US-4 | detail Verbose-Chat; live dedupe | #15, #16 |
| FR-011 | US-4 | (contract) Provider 400 translated | #5 |
| FR-012 | US-1, US-4 | xAI rejection; capability-absence; classification | #3 |
| FR-013 | US-6 | Real image/PDF fallback | #18, #19 |
| FR-014 | — | — | (non-goal) |

**Completeness check**: every FR except FR-014 maps to ≥1 user story, ≥1 BDD scenario, and ≥1 test.

---

## PR Slicing & Sequencing

| Slice | Scope | Contract | Depends |
|---|---|---|---|
| 1 | Backend: `LLMError` contract + `translateLLMError` + `ErrorPayload`/`ProviderError` threading + **both choke points** (incl. rate-limit dedup) + media (D1 stdlib+x/image, D2 downgrade-retry, D4 manifest, OOXML sniff) | `asyncapi.yaml` + regenerated; `go.mod` +x/image | — |
| 2 | Frontend: `llm-error.ts` (Verbose-Chat `detail` gate), `case 'error'` reducer (translated + entry-id dedupe) | consumes Slice 1 | Slice 1 |
| 3 | E2E: stub `media-rejection.spec.ts` + extend `tests/e2e/media.spec.ts` (real vision-less model, fail-if-unset) | none | Slice 2 |

---

## Ambiguity Warnings

| # | Item | Resolution |
|---|---|---|
| 1 | Animated-GIF fidelity | **Resolved (Q1):** static PNG. |
| 2 | `detail` on wire | **Resolved (Q2):** ship; render only under Verbose Chat; never persisted. |
| 3 | `x/image` scope | **Resolved (Q3):** IN v0.1.1. |
| 4 | Per-media granularity | Default: downgrade only the rejected image; confirm in impl. |
| 5 | "Error processing message:" prefix origin | Replaced when `message` translated; confirm in impl. |
| 6 | e2e model slug | **Resolved (Q4):** `$OMNIPUS_E2E_NO_VISION_MODEL`, fail-if-unset, preseed `deepseek/deepseek-chat`. |
| 7 | Persist `code` for replay | **Resolved:** persist `code` alongside `message` (MAJ-006). |
| 8 | REST executor raw CLI errors | **Resolved:** in-scope — routed through `translateLLMError` at the REST error-response seam (MAJ-005). |

---

## Evaluation Scenarios (Holdout)

### Scenario: Real provider image rejection (xAI) — incident reproduction
- attach SVG/AVIF → turn completes via downgrade; friendly note; no raw JSON.

### Scenario: Real vision-less model fallback
- agent on `$OMNIPUS_E2E_NO_VISION_MODEL` → send image, then PDF → each completes via downgrade; no empty reply; no raw error.

### Scenario: Archive structural understanding
- attach multi-entry ZIP → agent answers from manifest; tool-calls to extract if asked.

---

## Assumptions

- `appendErrorTranscript` (`turn.go:1152`) is the single write choke point (`replay.go:1001`).
- The WS forwarder is the single live choke point once `EventKindError` is added.
- `HandleErrorResponse` can return structured `*ProviderError` without breaking non-chat callers (stringer preserved).
- Verbose Chat (`verboseChatEnabled`) is the correct runtime gate for `detail`.
- `deepseek/deepseek-chat` remains a text-only, tool-capable OpenRouter model (verified 2026-07-21); backups documented.
- Operators prefer logs (not the UI) for raw provider detail; `detail` is for Verbose-Chat debugging.

---

## Clarifications

### 2026-07-21 (operator goal directive)
- "Showing the raw provider error is not good — check meaningful-message handling elsewhere." → No backend error-translation system exists today; SPA has `ApiError.userMessage`/`defaultUserMessage`/`getErrorMessage` + dev-toast/zod-drop edge. D5/D6/D7 build the backend equivalent at two choke points.
- "Spec what you call v0.1 but we work on 0.1.1." → Target `release/v0.1.1`.

### 2026-07-21 (operator mid-turn directives)
- "Add e2e with actual LLM, none-image/none-pdf capable model, to test the fallback." + "Extend the existing e2e tests." → US-6, FR-013, tests #18/#19 extend `tests/e2e/media.spec.ts`.

### 2026-07-21 (operator open-question answers)
- Q1 GIF → static PNG. Q2 `detail` → "strip in client; visible in verbose chat" → render `detail` only under `verboseChatEnabled`. Q3 x/image → "Add in v0.1.1." Q4 e2e → "env variable, failure if not set, preseed based on a strong OpenRouter model with no pdf/vision — research" → `$OMNIPUS_E2E_NO_VISION_MODEL`, fail-if-unset, preseed `deepseek/deepseek-chat` (verified `input_modalities:["text"]`, tools:true, 131072 ctx).

### 2026-07-21 (grill reviews)
- Spec round 1 BLOCK → corrected Rev 2. ADR round 2 BLOCK → architecture endorsed; round-2 wiring findings (CRIT-001 thread `*ProviderError`, CRIT-002 emit-site argument, MAJ-001…008) **corrected in Rev 3** (see ADR-051 "Grill findings → resolution (round 2)").
