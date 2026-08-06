# ADR-051: Provider-Capability-Aware Media Handling and User-Facing Error Translation

**Status:** Proposed (Revision 3 — operator decisions locked 2026-07-21: GIF→static PNG; `detail`→Verbose-Chat-gated; `x/image` IN v0.1.1; e2e env-var fail-if-unset preseeded `deepseek/deepseek-chat`. ADR-grill round-2 wiring findings CRIT-001/002 + 8 MAJ corrected in Rev 3.)
**Date:** 2026-07-21
**Deciders:** architect (+ backend-lead, frontend-lead, qa-lead for implementation/review); operator Daniel Piatkowski
**Target release:** `release/v0.1.1` (stability) for RD1, RD2, RD4–RD7; `v0.3` for RD3
**Defect origin:** operator UAT, 2026-07-21 — `send_file` of an image to an xAI/Grok provider returned `400 invalid-argument: "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image."`, and the full raw provider JSON body was surfaced verbatim to the chat user.
**Implements spec:** `docs/internal/specs/media-handling-error-translation-spec.md` (Rev 3)

> **Revision 3 (2026-07-21) — operator decisions locked.** Q1 animated GIF → transcode to static PNG. Q2 raw `detail` → ships on the wire but the SPA renders it **only under Verbose Chat** (`verboseChatEnabled`, `src/store/chatPreferences.ts`), not DEV mode — runtime-toggleable by any operator. Q3 → **`x/image` is IN v0.1.1**: WebP/BMP/TIFF decode added (one new pure-Go dep). Q4 e2e model → `$OMNIPUS_E2E_NO_VISION_MODEL` env var, **fails if unset**, preseeded with `deepseek/deepseek-chat` (verified `input_modalities:["text"]`, tools:true, 131072 ctx on OpenRouter).
>
> **Revision 2 (2026-07-21).** Spec `/grill-spec` BLOCK found the original "single translation seam at `pkg/agent/loop.go:7277-7300`" false — `EventKindError` is emitted from many sites. RD5/RD6 revised to translate at **two boundary choke points** (`appendErrorTranscript` write + WS-forwarder `EventKindError` case), covering every emit site by construction. ADR grill (round 2) endorsed this architecture; its 2 CRITICAL + 8 MAJOR (wiring-level) are **corrected in Rev 3** (see "Grill findings → resolution (round 2)").
>
> **Scope split (load-bearing).** Two inseparable halves: (1) stop sending media the provider cannot accept, and (2) stop showing raw provider errors when something still goes wrong. One decision — the incident had both failure modes at once, and the *reliability anchor* for (1) makes (2) largely unnecessary, but (2) is still mandatory because providers reject for reasons no client can predict.

---

## Context

### The incident [USER-INPUT]

The operator tested `send_file` in chat against an **xAI/Grok** provider. The agent attached an image; Omnipus forwarded it as a base64 data URL; xAI replied `400 invalid-argument` (bytes not a supported format — xAI accepts only JPG/PNG/WebP/ICO). The turn died and the user was shown the raw provider JSON verbatim. Two defects: **D-A** (no media-capability awareness/normalization) and **D-B** (raw provider errors reach the user).

### What the media path does today [FACT — code-cited]

`pkg/agent/loop_media.go` reduces every attachment to one of three buckets:

1. **Images** (`loop_media.go:98`) → base64 data URL, **always sent**, ungated.
2. **PDFs** (`loop_media.go:114`) → native PDF block **only if** `pdfCapableModel` (`loop_media.go:166`, a hardcoded substring allow-list); else text.
3. **Everything else** → `buildDocumentInjection` → `docextract.Extract` (`pkg/docextract/extract.go:45`), which recognizes only text-like, OOXML, and PDF. **ZIP/EXE/archives/binaries/encrypted hit `default` → `ok=false`** (`extract.go:74`) → a metadata text note.

**Consequence (D-A scope): only images and PDFs can 400 from the media path.** Every other type is already text/metadata before the provider call. ZIP/EXE are *already safe* — the incident was an image. The entire capability system today is `pdfCapableModel`; no image-format awareness.

### What the error path does today [FACT — code-cited]

No backend user-facing error-translation system exists. Raw errors flow verbatim:

1. `pkg/providers/common/common.go:390` `HandleErrorResponse` builds `API request failed:\n  Status: %d\n  Body:   %s` (≤512 bytes raw body); returns a flat string error.
2. Emit sites call `ts.appendErrorTranscript(kind, stage, err.Error())` (`pkg/agent/turn.go:1152`) which writes raw to JSONL. **`appendErrorTranscript` is the single write choke point** (`replay.go:1001`); all persist call sites route through it.
3. `EventKindError` is emitted onto the bus from ~29 sites across 7 files (`loop.go`, `subturn.go`, and the runner drivers `pkg/agent/runner/driver_codex.go`, `driver_opencode.go`, `driver_claude.go`, `driver_stream.go`, `external_event.go`); the WS forwarder (`pkg/gateway/websocket.go:3068-3461`) has **no `case agent.EventKindError:`** — live errors are invisible. The SPA learns only "turn errored" via status-only `TurnEnd` (`events.go:206`, no `Message`). **Coverage at the two choke points holds by construction:** runner-driver sites emit on the runner channel and are re-emitted onto the main bus by `spawnSubTurn` (so they reach the forwarder once the case is added), and every persist call routes through `appendErrorTranscript` (the write choke point) — so no emit site needs individual translation.
4. On replay the raw JSONL entry becomes a `ReplayErrorFrame` (`asyncapi_types.gen.go:338`) whose `Message` is the raw blob; the SPA renders `frame.message` verbatim (`src/store/chat.ts:3262`).

**This is why the design translates at the two choke points, not at emit sites:** too many emit sites, but all funnel through `appendErrorTranscript` (write) and — once added — the WS forwarder `EventKindError` case (live).

**Precedents to build on (not invent):** `isImageInputRejection` (`loop.go:7254`) intercepts one image rejection but **returns false for the incident string** → replaced by a dataset-driven classifier, not "generalized" (CRIT-002). `downgradePDFMediaToText` (`loop_media.go:215`). `DelegationDeniedResult` (`result.go:202`, typed-wire template). `recordRateLimitDenial` (`loop.go:1028`, dual-emit; needs reconciliation — RD6). SPA: `ApiError.userMessage`/`defaultUserMessage`/`getErrorMessage` (`src/lib/api-error.ts:45,59,267`), `friendlyProbeError` (`onboarding.tsx:252`), the zod-drop/dev-toast edge (`src/lib/ws.ts:246`), and **Verbose Chat** (`verboseChatEnabled`, `src/store/chatPreferences.ts` + `src/lib/toolVisibility.ts`) — no error-code map exists (greenfield).

### Forces

- **Unknowable capabilities.** No single source reliably enumerates provider/model support (docs drift; `/models` richness varies; OpenRouter wraps upstreams; new models weekly). Reliability cannot depend on prediction.
- **Hard constraints.** Single Go binary, no new runtime deps except pure-Go additions (C#1); pure Go, no CGo (C#2) → transcode limited to stdlib + `x/image`; explicit seeded data over branches (C#6) — the registry embodying that is deferred (RD3).
- **Asymmetric failure.** False "unsupported" → transcode/downgrade (less rich, always works). False "supported" → provider 400 → empty reply, dead turn. When in doubt, normalize/downgrade.

---

## Decision

Adopt a **layered, fail-safe** design. Reliability from *normalization* + a *reactive backstop*, not prediction. User-facing errors translated at **two boundary choke points** so raw internals never persist and never cross to the SPA.

### RD1 — Canonical image normalization (the reliability anchor) [v0.1.1]

Before sending an image, normalize to **PNG** via pure-Go transcode when the source is decodable. Collapses `N formats × M providers` to one dimension. v0.1.1 decode coverage: **stdlib** (`image/png`, `image/jpeg`, `image/gif`) **+ `golang.org/x/image`** (`webp`, `bmp`, `tiff`) — operator decision Q3. **AVIF/HEIC/SVG remain non-decodable without CGo → fall to RD2.** `encodeImageToDataURL` (`loop_media.go:445`) gains a normalize step (decode → `png.Encode` → data URL); decode failure returns `""` (existing path handles it). Animated GIF → static PNG first frame (Q1; animation loss accepted).

### RD2 — Empirical media-rejection downgrade + single retry (backstop) [v0.1.1]

On a media-class provider rejection, **downgrade the offending media and retry the turn exactly once.** Image → strip + text note; PDF → reuse `downgradePDFMediaToText` (`loop_media.go:215`). Catches everything RD1 can't transcode (AVIF/HEIC/SVG), registry gaps, OR routing surprises, new models. **One retry only.**

### RD3 — Declarative provider-capability registry [DEFERRED to v0.3]

Data, operator-editable: per-provider `{image_formats, pdf, audio, video, …}` seeded from OpenRouter `input_modalities` + docs. **Job: efficiency** (skip unneeded transcoding) and audio/video. Adds nothing to reliability → out of scope for v0.1.1; belongs in v0.3.

### RD4 — Opaque binaries stay non-raw: archive manifest + agentic-tool model [v0.1.1]

- **Archives (`.zip/.tar/.gz/.tgz`):** upgrade the metadata note to a **manifest** (entries + sizes), stdlib `archive/zip` (already imported) + `archive/tar` + `compress/gzip`.
- **Opaque binaries (`.exe/.dll/.bin/.db/.so/encrypted/unknown`):** concise metadata note; agent inspects via tools.

**Invariant: no file type other than normalized images (RD1) and gated PDFs is ever sent as a raw binary content block.**

### RD5 — User-facing error translation at TWO boundary choke points; typed wire contract [v0.1.1]

Raw provider errors **never persist and never cross to the SPA in production.** Translate at:

1. **Write choke point — `appendErrorTranscript` (`turn.go:1152`):** translate `message` before JSONL write. Raw never lands on disk.
2. **Live choke point — new `case agent.EventKindError:` in the WS forwarder (`websocket.go`):** translate before emitting the live frame. Covers every emit site.

Both call one shared pure function `translateLLMError`. Raw `err` preserved **only** in `logger.ErrorCF` + wrapped `fmt.Errorf(%w)`. The `LLMError` shape (mirrors `DelegationDeniedResult`): `{code, message, retryable, detail}`. Default copy generic over server text (same rationale as `api-error.ts:219-225`). Classifier = two pattern classes, both → `media_unsupported`: *capability-absence* (no vision) and *format-rejection* (has vision, rejects a format — incl. the **incident string** "valid JPG, PNG, WebP, or ICO image", pinned as a dataset row). `isImageInputRejection` removed.

**Translation threading (closes ADR-grill CRIT-001).** `HandleErrorResponse` returns `*ProviderError{Status, Body, Err}`; emit sites pass it to **both** choke points — `appendErrorTranscript(kind, stage, message, pe *ProviderError)` (nilable for non-provider errors) and `ErrorPayload` gains a `*ProviderError` field for the live path. `translateLLMError(pe *ProviderError, message string)` uses `pe.Status`/`pe.Body` when present, else substring-matches on `message` (nil-safe). Write-path rules: (a) **rate-limit skip (MAJ-001/004)** — when `kind==EventKindRateLimit` the message is already friendly (`recordRateLimitDenial`), so `appendErrorTranscript` does **not** re-translate (the classifier also recognizes both rate-limit message formats); (b) **model_switch sanitize (MAJ-003)** — `model_switch`-stage messages pass through the classifier so no model name leaks into `message`. **REST executor in-scope (MAJ-005)** — subagent_3p CLI/executor errors that cross to the SPA are routed through `translateLLMError` at the REST error-response seam. `detail` is derived from `pe.Err` at the forwarder (MAJ-002, feasible now that `*ProviderError` is threaded).

### RD6 — Live error forwarding + dedup [v0.1.1]

The new `case agent.EventKindError:` (RD5 #2) forwards a translated `LLMError` frame **live**. **Rate-limit dedup:** when `code==rate_limited`, the existing `RateLimitFrame` path is authoritative; the error frame is suppressed (reconciles `recordRateLimitDenial`'s dual-emit). **Live/replay dedup:** the live frame carries the transcript entry id so the SPA dedupes against the replayed entry on reload.

### RD7 — SPA renders the translated message; raw `detail` under Verbose Chat [v0.1.1] — operator Q2

At the chat reducer (`src/store/chat.ts:3086`), render the translated `message`. `detail` **ships on the wire** (so it's available without a dev build) and is rendered **only when Verbose Chat is enabled** (`verboseChatEnabled`, `src/store/chatPreferences.ts` — the persisted Settings → Chat toggle, already the gate for `src/lib/toolVisibility.ts`), behind a "Technical details" disclosure. **Stripped otherwise.** `detail` is computed live at the forwarder, **never persisted.** New `src/lib/llm-error.ts` maps `code` → display string. The reducer's kickoff-reject (`addToast`) and cancel-ack sub-paths are left untouched.

### RD8 — Release placement

- **`release/v0.1.1` (stability):** RD1 (incl. `x/image`), RD2, RD4, RD5, RD6, RD7, plus the **real-LLM e2e** extending `tests/e2e/media.spec.ts`, gated by `$OMNIPUS_E2E_NO_VISION_MODEL` (preseeded `deepseek/deepseek-chat`; **fails if unset** — not skip). Fixes the incident (D-A, D-B). One new pure-Go dep (`golang.org/x/image`).
- **`v0.3` (capability-aware redesign):** RD3 (registry), audio/video.

---

## Options Considered

| Option | Verdict | Why |
|---|---|---|
| Per-provider substring allow-lists (status quo `pdfCapableModel`) extended to images | Rejected | Brittle, stale; no image awareness exists today. |
| Translate at each `EventKindError` emit site | Rejected | Too many sites; incomplete by construction. |
| Translate at one in-loop seam (`loop.go:7277-7300`) | Rejected (Rev 1; grill CRIT-001) | False premise — misses other emit sites. |
| **Translate at two boundary choke points** (`appendErrorTranscript` + WS forwarder) | **Accepted (Rev 2)** | Covers all emit/persist sites by construction; raw never persists. |
| "Attach as normal file; let the provider figure it out" | Rejected | No such primitive — chat-completions wire accepts only image/PDF/audio inline. |
| Probe-and-learn only (no normalization) | Rejected as primary | Slow, flaky. Kept only as RD2 backstop. |
| Canonical normalize + downgrade backstop (RD1+RD2) | **Accepted** | Reliability from normalization; backstop for the unknowable tail. |
| Full capability registry now (RD3 in v0.1.1) | Rejected for v0.1.1 | Over-scoped; optimize-only; belongs with v0.3. |
| Surface raw provider errors (status quo) | Rejected | Leaks internals/provider identity; D-B. |

---

## Consequences

### Positive
- Chat stops dying on unsupported/corrupt images and PDFs-to-non-capable-models (D-A closed).
- Raw provider internals never reach users and never persist (D-B closed); errors classified and meaningful.
- Errors visible **live** (RD6).
- Provider-agnostic; covers subturn + external-CLI errors for free via choke points.
- Archives structurally useful; OOXML content recovered regardless of extension.
- WebP/BMP/TIFF normalized upfront (Q3 → x/image in v0.1.1).
- Real-LLM e2e proves the fallback against a live vision-less model.

### Negative
- **Transcode CPU** per outbound image; bounded by existing `maxSize`.
- **Fidelity loss:** animated GIF → static PNG; AVIF/HEIC/SVG → downgrade note.
- **One retry** adds latency on rare media rejection.
- **One new pure-Go dependency:** `golang.org/x/image` (Q3) — allowed (Constraint-OK), but breaks the prior "zero new deps" claim.
- New pure-Go code to maintain: transcoder (+x/image formats), dataset-driven classifier, archive-manifest builder, OOXML sniffer, structured `ProviderError`, WS forward case, SPA error helper.
- `appendErrorTranscript` becomes load-bearing for translation; existing replay fixtures must update.

### Neutral / out of scope
- RD3 (registry), audio/video — v0.3.
- No change to `send_file` or channel-side format limits (separate capability domain).

### Rollback
RD1/RD2/RD4/RD5/RD6/RD7 are additive and independently flaggable. Disabling returns to today's behavior; raw always logged.

### Single binary / deps
v0.1.1 uses stdlib (`image`, `image/png`, `image/jpeg`, `image/gif`, `archive/zip`, `archive/tar`, `compress/gzip`) **+ `golang.org/x/image`** (`webp`, `bmp`, `tiff`) — one new pure-Go dep. No CGo. Wire changes via the contract pipeline.

---

## Affected Components

- **Backend:**
  - `pkg/agent/turn.go:1152` `appendErrorTranscript` — **RD5 write choke point**.
  - `pkg/gateway/websocket.go:3068-3461` — **RD5/RD6 live choke point** (new `EventKindError` case + rate-limit dedup + entry id).
  - `pkg/agent/translate_error.go` (new) — `translateLLMError` pure classifier.
  - `pkg/agent/events.go` `ErrorPayload` — **extend with `*ProviderError`** (CRIT-001 wiring fix).
  - `pkg/agent/loop_media.go:445` — RD1 normalize (stdlib + x/image); `loop_media.go:215` reused by RD2.
  - `pkg/agent/loop.go:7277-7300` — RD2 downgrade-retry; `loop.go:7254` `isImageInputRejection` removed.
  - `pkg/docextract/extract.go` — RD4 archive manifest + OOXML sniff.
  - `pkg/providers/common/common.go:390` — structured `*ProviderError`.
  - `pkg/agent/loop.go:1028` — reconcile dual-emit (RD6).
  - `go.mod` — add `golang.org/x/image`.
  - `contracts/components/schemas/LLMError.yaml` + `contracts/asyncapi.yaml` + `pkg/gateway/inboundschemas/ReplayErrorFrame.yaml` + `pkg/api/generated/` — `LLMError` + `ReplayErrorFrame.payload.llm_error` + live `error` message.
- **Frontend:** `src/store/chat.ts:3086` (render translated + dedupe); `src/lib/llm-error.ts` (new, `code`→display + Verbose-Chat `detail` disclosure reading `verboseChatEnabled`); `src/components/chat/ChatScreen.tsx`.
- **Tests:** unit/integration per spec TDD; **e2e extend `tests/e2e/media.spec.ts`** (real vision-less/PDF-less model via `$OMNIPUS_E2E_NO_VISION_MODEL`, fail-if-unset, preseed `deepseek/deepseek-chat`) + stub `media-rejection.spec.ts`.
- **Variants:** all (provider-agnostic). Lite build unaffected.

---

## Integration Contract

**`LLMError` component** (new, `contracts/components/schemas/LLMError.yaml`):
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
- `ReplayErrorFrame.payload.llm_error: LLMError` (optional; `additionalProperties: false` requires declaration).
- New live message `error` (`WsFrameTypeError`) with `payload: LLMError`.

**Invariants:** (1) only PNG-normalized images + gated PDFs are raw-binary content blocks; (2) every media rejection → at most one downgrade-retry; (3) no raw provider text crosses to the SPA in production or persists; (4) translation only at `appendErrorTranscript` (write) + WS forwarder `EventKindError` (live); (5) `detail` computed live, never persisted, Verbose-Chat-rendered.

---

## Grill findings → resolution

### Round 1 (spec) — all closed in spec Rev 2
CRIT-001 (single seam false) → two choke points. CRIT-002 (incident string missed) → dataset-driven classifier, pinned. CRIT-003 (AsyncAPI integration) → `LLMError` + `payload.llm_error` + live `error`. MAJ-001…011 addressed.

### Round 2 (ADR) — architecture endorsed; wiring findings CLOSED in Rev 3
| Finding | Status | Resolution (applied) |
|---|---|---|
| CRIT-001 (choke points lack raw `error`) | **CLOSED** | `HandleErrorResponse` returns `*ProviderError`; threaded to both choke points via `appendErrorTranscript(..., pe)` + `ErrorPayload.*ProviderError`; `translateLLMError(pe, message)` reads status/body. |
| CRIT-002 (emit-site count/paths wrong) | **CLOSED** | Corrected: ~29 sites across 7 files; coverage holds by construction (runner-driver sites re-emit via `spawnSubTurn`; persists funnel through `appendErrorTranscript`). |
| MAJ-001 (rate-limit double-translate at write) | **CLOSED** | `appendErrorTranscript` skips translation when `kind==EventKindRateLimit` (message already friendly). |
| MAJ-002 (`detail` not computable at forwarder) | **CLOSED** | Feasible via CRIT-001 threading (`detail` from `pe.Err`). |
| MAJ-003 (`model_switch` embeds model names) | **CLOSED** | `model_switch` messages pass through the classifier; no model-name leak. |
| MAJ-004 (two rate-limit message formats) | **CLOSED** | Classifier recognizes both rate-limit message formats. |
| MAJ-005 (REST executor raw CLI errors) | **CLOSED** | Declared in-scope: routed through `translateLLMError` at the REST error-response seam. |
| MAJ-006 (replay can't reconstruct `code`) | **CLOSED** | `code` persisted alongside `message` (spec FR-008). |
| MAJ-007 (rollback mixed-state transcript) | **CLOSED** | Accepted/documented: translated+raw coexistence during rollback. |
| MAJ-008 (512-byte body truncation before classification) | **CLOSED** | Classify on full `*ProviderError.Body` before any 512B log-truncation. |

---

## Operator decisions (locked 2026-07-21)

- **Q1 (animated GIF):** transcode to static PNG; animation loss accepted.
- **Q2 (raw `detail`):** ship on the wire; render **only under Verbose Chat** (`verboseChatEnabled`); never persisted.
- **Q3 (`x/image`):** **IN v0.1.1** — transcode WebP/BMP/TIFF; AVIF/HEIC/SVG still downgrade.
- **Q4 (e2e model):** `$OMNIPUS_E2E_NO_VISION_MODEL` env var; **fail if unset**; preseed `deepseek/deepseek-chat`.

---

## References

- Defect origin: operator UAT, 2026-07-21 (xAI image-rejection 400; raw blob surfaced).
- Spec: `docs/internal/specs/media-handling-error-translation-spec.md` (Rev 3) + grill reviews `…-spec-review.md`, `ADR-051-…-review.md`.
- Precedent patterns: `isImageInputRejection` (`loop.go:7254`); `downgradePDFMediaToText` (`loop_media.go:215`); `pdfCapableModel` (`loop_media.go:166`); `DelegationDeniedResult` (`result.go:202`); `recordRateLimitDenial` (`loop.go:1028`); `appendErrorTranscript` (`turn.go:1152`); `replay.go:1001`; Verbose Chat (`src/store/chatPreferences.ts`, `src/lib/toolVisibility.ts`).
- e2e model evidence: OpenRouter `/api/v1/models` — `deepseek/deepseek-chat` `input_modalities:["text"]`, `tools:true`, `context_length:131072` (verified 2026-07-21). Backups: `meta-llama/llama-3.3-70b-instruct`, `qwen/qwen-2.5-72b-instruct`.
- Prior error spec: `docs/internal/specs/phase-1-chat-model-and-errors.md` (FR-001/002/014 — error *persistence*; this ADR adds *translation*).
- Constraints: #1 (single binary), #2 (pure Go / no CGo), #6 (explicit data over branches), #8 (contract-first wire formats).
- Release strategy: v0.1.1 = stability (RD1,2,4,5,6,7 + e2e); v0.3 = capability-aware redesign (RD3).
