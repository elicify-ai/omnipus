# Wave 1 Review — Round 1 — Silent Failure Hunter

**Scope:** `d0e7374a..fba0acbf` (`sendfile-fix`, Wave 1 slices B1 + C + F + E)
**Reviewer role:** `silent-failure-hunter` (pr-review-toolkit)
**Focus:** swallowed errors, masking fallbacks, corrupt-data handling, pixel-bomb behavior, last-known-good behavior, and Wave 0 SFH-05 carry-forward.

## Verdict

**BLOCK — 1 CRIT / 4 MAJOR.**

The four requested focal paths are not uniformly safe:

- **Slice E / literal Gemini 400:** the literal `400 Unsupported MIME type: image/svg+xml` case does fire the fallback by static trace, but only because the gate treats every residual `CodeProviderRejected` 4xx as “inconclusive.” That is substantially broader than FR-017 and can mask unrelated non-media 4xx failures.
- **Slice B1 / SHA-256-on-read:** hard-fails correctly. Hash or size mismatch logs expected/actual hashes, returns `ErrIntegrityCheckFailed`, and returns `nil` bytes; corrupt bytes do not escape `Read`.
- **Slice F / pixel bomb:** the preflight is correctly before `image.Decode`; an over-16-MP declared raster returns early and logs. `pkg/media/resize` never receives the bomb because it accepts an already-decoded `image.Image`. The failure reason is collapsed to the generic caller marker, however.
- **Slice C / corrupt pull:** a present mismatching sidecar hard-fails and `Catalog.Refresh` retains last-known-good. Missing, unreadable, malformed, or unreachable sidecars are silently treated as success, so valid-JSON corruption/tampering can replace last-known-good.

## Findings

### SFH-W1-01 — CRITICAL — Slice E emits contract-invalid error codes; the SPA drops the entire live and replay error frame

**Locations:**

- `pkg/agent/translate_error.go:56-69`
- `contracts/asyncapi.yaml:1343-1352`
- `contracts/asyncapi.yaml:1373-1382`
- `pkg/gateway/websocket.go:3367-3386`
- `pkg/gateway/replay.go:1033-1040`
- `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts:110-125`
- `src/workers/ws-parser.worker.ts:42-68`

Slice E adds runtime codes `tool_args` and `schema`, but neither code was added contract-first to `LLMError` / `LLMErrorReplay`. The generated Go type uses `string`, so the backend compiles and serializes either value. The generated SPA Zod schemas still allow only the old seven-code enum and reject the whole known-type frame.

**Full causal chain:**

1. A provider 400 containing `invalid tool arguments` or `schema validation` is classified as `CodeToolArgs` / `CodeSchema`.
2. The gateway serializes that string into generated `LLMError.Code` at `pkg/gateway/websocket.go:3378-3385`; replay serializes the persisted code at `pkg/gateway/replay.go:1033-1040`.
3. The generated SPA schema rejects the code at `_asyncapi-zod-schemas.generated.ts:112,121`.
4. The worker returns `frame: null` for the known `error` / `replay_error` frame. A single dropped error does not reach the chat reducer; the next valid frame resets the consecutive-drop counter.
5. The turn can therefore end with no user-visible error bubble live or after reload.

This is an immediate instance of the unrecognized-code failure Wave 0 warned about, not merely a future compatibility concern. It also violates the repository’s contract-first boundary rule.

**Required fix:** add both codes to the canonical `LLMError.yaml` and `LLMErrorReplay.yaml` schemas and AsyncAPI mirrors before runtime use; regenerate Go/TS/Zod; add display copy; add contract tests that marshal the actual runtime code set and validate both live and replay frames.

### SFH-W1-02 — MAJOR (SFH-05 carry-forward) — unrecognized-code fallback remains unobservable on both sides

**Locations:**

- `pkg/agent/translate_error.go:112-128`
- `src/lib/llm-error.ts:63-72`

Wave 0 SFH-05 is **not addressed**. Go still maps an unrecognized code to the generic `unknown` message with no WARN/metric. The SPA still maps it to `codeToDisplay.unknown` with no warning or telemetry carrying the actual code. The correct forward-compatible behavior—never throw and never render blank—is preserved, but the identity of the unrecognized value remains silent.

SFH-W1-01 makes this residual concrete: Slice E introduced two backend codes without updating the wire/SPA enum. Even after the immediate contract drift is fixed, future unknown values would still be silently collapsed by these fallback functions.

**Required fix:** retain the generic fallback, but emit a rate-limited structured warning/metric containing the unknown code in both `defaultUserMessage` and `codeToMessage`; add assertions for the signal, not only for returned copy.

### SFH-W1-03 — MAJOR — `outcomeFallbackEligible` masks unrelated residual 4xx failures as media failures

**Locations:**

- `pkg/agent/translate_error.go:369-401`
- `pkg/agent/media_downgrade.go:144-192`
- `pkg/agent/media_outcome_retry_test.go:449-487`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1080-1082`

The literal Gemini scenario does fire:

1. `400 + "Unsupported MIME type: image/svg+xml"` matches none of the pinned media/policy/context/tool/schema substrings.
2. `classifyByHTTPStatus` therefore returns `CodeProviderRejected`.
3. `outcomeFallbackEligible` explicitly accepts `CodeProviderRejected`, status 400 is not excluded, and the body matches no exclusion helper.
4. With a data-image block present, `TryMediaDowngrade` strips it and returns true.

However, the same path fires for **every** residual 400–499 response except 401/403/413 and the small substring exclusion lists. This conflicts with FR-017’s explicit “ONLY ... `CodeUnknown`” gate and FR-018’s “any specific classified code ... prevents the fallback.” It can hide malformed requests, provider validation errors with novel wording, billing/region failures expressed as 400/422, and content-policy phrases outside the short pinned list.

The committed regression test demonstrates the overbreadth rather than the literal BDD: it uses `invalid request: image of a horse is not allowed here` and asserts that stripping must fire. That body can plausibly be a policy or request-semantic rejection, yet the implementation relabels a successful retry as media unsupported.

**Required fix:** do not equate all `CodeProviderRejected` 4xx values with inconclusive classification. Introduce an explicit classifier result that distinguishes “specific provider rejection” from “residual/unclassified status fallback,” or narrow the media outcome signal structurally. Add the literal Gemini body as the positive test and generic 400/404/422 negative cases with media present.

### SFH-W1-04 — MAJOR — FR-017a relabel is a dead write; no log, transcript, audit, or frame reads it

**Locations:**

- `pkg/agent/loop.go:6915-6948`
- `pkg/agent/turn.go:261-271`
- `pkg/agent/turn.go:307-326`
- `pkg/agent/media_outcome_retry_test.go:489-550`

After a successful outcome retry, the loop stores `CodeMediaUnsupported` in `turnState.outcomeRelabel`. A repository-wide search found no production caller of `outcomeRelabelCode`; the only occurrence is its definition. The pre-retry warning is emitted before success and records the original `helperCode` (`provider_rejected`), while transcript and WS translation independently rerun the raw classifier.

The test named `TestStep4_RelabelOnSuccess_ViaLoopCallSite` does not exercise the production field or an emit site. Its test-only `recordedVerdictForTurn` returns `CodeMediaUnsupported` whenever either retry guard is set, so it passes even if `setOutcomeRelabel`, `outcomeRelabel`, and the production accessor are removed.

**Impact:** FR-017a’s promised recorded classification never exists outside dead turn-local state. Operators see the original code in the only retry log, and no durable surface records the claimed relabel.

**Required fix:** define the actual observable record required after a successful retry and wire the relabel into it. Replace the mirror helper with a production-path test that executes the retry call site and asserts the emitted log/audit/transcript/frame code.

### SFH-W1-05 — MAJOR — checksum verification silently fails open when the sidecar is unavailable or unreadable

**Locations:**

- `pkg/providers/capabilities/puller.go:227-277`
- `pkg/providers/capabilities/puller_test.go:221-256`
- `pkg/providers/capabilities/catalog.go:406-447`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1091`

A present, readable mismatching sidecar is handled correctly: `verify` returns `ErrChecksumMismatch`; `Pull` returns no bytes; `Catalog.Refresh` logs, returns the error, and leaves state untouched.

But `verify` returns success without a warning when:

- the checksum URL cannot be derived;
- request construction fails;
- checksum transport fails;
- the sidecar returns any non-200 status;
- reading it fails; or
- its body is empty.

`TestGHReleasePuller_Pull_NoSidecar` explicitly locks the fail-open behavior. This contradicts FR-025’s “checksummed” transport and the package comment claiming every successful pull is verified. A corrupted body that remains schema-valid can therefore be applied and persisted as the new last-known-good. The parsed GitHub release asset `digest` field is also not used as an alternative integrity source.

**Required fix:** require a valid checksum for success. For release assets, validate the GitHub API `digest` when present and otherwise require the sidecar; for raw fallback, require the sidecar. Any fetch/read/format/absence problem must return an error so `Catalog.Refresh` retains last-known-good. Log the precise verification failure at the catalog boundary.

## Minor / non-blocking observations

### SFH-W1-m1 — pixel-budget reason is lost at the function boundary

`encodeImageToDataURL` detects the over-budget dimensions before `image.Decode`, logs `pixel-budget-exceeded`, and returns `""` (`pkg/agent/loop_media.go:447-474`). `resolveMediaRefs` maps every non-SVG empty result to `[attachment unavailable: <name> (too large or unreadable)]` (`pkg/agent/loop_media.go:106-124`). The Slice F test only asserts an empty string (`pkg/agent/loop_media_resize_test.go:68-87`), not the required `[... (pixel budget exceeded)]` marker. The guard prevents the OOM class, but the exact step-7 reason is not preserved.

### SFH-W1-m2 — the literal Gemini BDD text is not executed by the named regression

`TestStep4_ClassifierSubstringFalsePositive_OutcomeFires` refers to the Gemini row in comments but executes `invalid request: image of a horse is not allowed here`. The current static path covers the literal body, but the exact incident phrase is not regression-locked.

## Requested path audit

| Path | Result | Evidence |
|---|---|---|
| Slice E: Gemini 400 unsupported SVG MIME | **Fires, but through an overbroad gate** | `classifyByHTTPStatus` → `CodeProviderRejected`; `outcomeFallbackEligible` accepts it; media is stripped. |
| Slice B1: SHA mismatch | **Hard-fail; safe** | `Library.Read` returns `nil, entry, ErrIntegrityCheckFailed` at `pkg/media/library/library.go:267-275`; test asserts no unverified bytes at `library_test.go:201-210`. |
| Slice F: pixel bomb | **Guarded before decode; not swallowed** | `DecodeConfig` and product guard occur at `loop_media.go:452-474`; `image.Decode` is later at `:483`. `ResizeToFit` documents that it accepts already-decoded images. |
| Slice C: corrupt download | **Hard-fail only when checksum is present and mismatches; otherwise fail-open** | `puller.go:235-277`; `catalog.go:417-426`. |

## Investigation hypotheses

| Hypothesis | Evidence sought | Result |
|---|---|---|
| H1: the literal Gemini 400 does not pass the outcome gate | Classifier result and predicate branch | **Rejected.** Static trace shows `CodeProviderRejected` and eligible 400; fallback fires. Confidence: high. |
| H2: B1 returns corrupt bytes alongside an integrity error | `Read` return values and tamper test | **Rejected.** Data is `nil`; error is `ErrIntegrityCheckFailed`; warning includes expected/actual digest. Confidence: high. |
| H3: Slice F attempts full decode before checking declared pixels or swallows a pixel-bomb OOM | DecodeConfig/decode ordering and resize API boundary | **Rejected for declared >16-MP raster bombs.** Guard precedes `image.Decode`; resize receives decoded images only. Exact marker reason is lost. Confidence: high. |
| H4: every corrupt capability download hard-fails to last-known-good | Checksum error branches and Catalog mutation ordering | **Partially rejected.** Present mismatch hard-fails; missing/unreadable checksum silently succeeds and schema-valid corruption can be applied. Confidence: high. |
| H5: Wave 0 SFH-05 observability was added in Slice E | WARN/metric/telemetry on unknown code fallbacks | **Rejected.** Both Go and SPA fallback bodies are unchanged and silent. Confidence: high. |
| H6: FR-017a relabel reaches a durable/observable surface | Production reads of `outcomeRelabelCode` and end-to-end test | **Rejected.** No production reads; test uses a mirror helper. Confidence: high. |

## Reproduction and verification observed

Initial reproduction command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestStep4_ClassifierSubstringFalsePositive_OutcomeFires$' -p 1 ./pkg/agent
```

Observed result:

```text
ok  github.com/elicify-ai/omnipus/pkg/agent  0.021s
```

This confirms the committed residual-4xx surrogate fires. It does **not** execute the literal Gemini body; that gap is SFH-W1-m2. The literal case conclusion above is grounded in the complete classifier → eligibility → media-mutation static trace.

I did not run the full Go suite, per the repository’s devpod resource rule. No production files were modified.
