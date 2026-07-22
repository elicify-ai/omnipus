# ADR-051 Human UAT Plan: Media Handling and Provider-Error Translation

**Target:** ADR-051 Revision 3 and `docs/internal/specs/media-handling-error-translation-spec.md` Revision 3  
**Implementation under test:** `dfa5c980beae6619b6ceab8895f38a9abf67c250` on `sendfile-fix`  
**Release target:** v0.1.1 stability  
**Test type:** Human acceptance and regression testing against the embedded SPA and real gateway  
**Result rule:** A scenario passes only when all UI, provider-capture, log, transcript, console, and screenshot assertions listed for it pass. A partial result is a failure.

---

## 1. Overview

ADR-051 has two user-visible goals:

1. Media that a model cannot accept must be normalized or downgraded so the turn can continue, with no more than one media-rejection retry.
2. Provider failures must reach users as stable, meaningful messages rather than raw provider JSON, while retaining raw diagnostic detail only in approved places.

This plan exercises every BDD scenario in the Revision 3 spec, all named edge cases, the original xAI incident string, archive handling, live/replay behavior, Verbose Chat gating, rate-limit deduplication, and the existing kickoff/cancellation paths that share the SPA error reducer.

### 1.1 Acceptance principles

- Test the real embedded SPA served by the Go gateway, not the Vite development server.
- Use a programmable provider stub for deterministic request inspection and response sequencing.
- Repeat the real-model image and PDF fallback cases using the required text-only OpenRouter model.
- Treat an empty assistant reply, a stuck streaming state, more than one media retry, raw provider text in a forbidden surface, or a duplicate bubble as a failure.
- Save evidence while the test is running. Do not infer request normalization from the final assistant response; prove it from the stub request capture.
- Run each test case in a fresh chat session unless the procedure explicitly requires the same session.

---

## 2. Scope

### 2.1 In scope

- PNG normalization for JPEG, GIF, animated GIF first-frame behavior, WebP, BMP, and TIFF.
- Safe fallback for AVIF, HEIC, SVG, corrupt images, oversized images, and missing files.
- Format-rejection and capability-absence classification.
- Exactly one media downgrade retry and terminal handling after a second rejection.
- PDF downgrade for a model that cannot accept native PDF input.
- ZIP and TAR.GZ manifests; opaque binary metadata notes.
- OOXML detection when extension/MIME are wrong, including a valid OOXML document renamed to `.zip`.
- Archive entry caps, protected-archive note, and corrupt-archive handling.
- Translation of HTTP 400, 408, 413, 429, 5xx, content-policy, context-window, and unknown failures.
- The two production rate-limit message forms and rate-limit bubble deduplication.
- Live error delivery without navigation and live/replay deduplication by transcript entry id.
- Verbose Chat off, on/collapsed, and on/expanded behavior.
- Persistence rules for translated `message`, `error_code`, and `error_retryable`; non-persistence of raw text and `detail`.
- Existing workspace kickoff rejection and cancellation acknowledgment behavior.
- Cancellation of background work belonging to the canceled session without affecting another session.
- Real image and PDF fallback using `$OMNIPUS_E2E_NO_VISION_MODEL`.

### 2.2 Out of scope

The following are not acceptance requirements for ADR-051 and must not be filed as ADR-051 failures unless they prevent an in-scope scenario:

- RD3 declarative provider-capability registry; this is deferred to v0.3.
- Audio or video capability handling.
- Changes to the `send_file` tool.
- Channel-side upload, MIME, or media-size limits.
- Provider-specific quality of image understanding after successful delivery.
- Animation preservation; animated GIFs deliberately become a static PNG representing the first frame.
- Adding CGo codecs for AVIF or HEIC.
- Redesigning the existing PDF-capability registry/allow-list.

---

## 3. Role definitions

One person may perform multiple roles, but evidence must identify who acted in each role.

| Role | Responsibilities |
|---|---|
| **Operator UAT** | Owns environment isolation, target commit verification, gateway start/stop, provider credentials, feature toggles, final result ledger, and sign-off. |
| **Power user** | Performs UI actions, attaches fixtures, controls Verbose Chat, starts/cancels turns, observes user-visible behavior, and captures screenshots. |
| **Log auditor** | Tails `gateway.log`, inspects programmable-provider request captures, proves retry counts, and verifies raw detail is logged without entering forbidden stores. Must not paste secrets into evidence. |
| **Archive user** | Runs archive, opaque-binary, and OOXML scenarios; verifies the model receives manifests/extracted text rather than raw bytes. |
| **Replay user** | Performs reload/reopen/archive-and-reopen steps; verifies entry-id dedupe, transcript reconstruction, and live/replay parity. |

---

## 4. Environment setup

### 4.1 Required environment variables

Run in a clean shell. The no-vision model variable is mandatory and must cause readiness to fail when missing.

```bash
export OMNIPUS_E2E_NO_VISION_MODEL=deepseek/deepseek-chat
: "${OMNIPUS_E2E_NO_VISION_MODEL:?ADR-051 UAT requires a text-only, tool-capable model}"

export OMNIPUS_HOME=/tmp/omnipus-adr051-uat
export UAT_ROOT=/tmp/omnipus-adr051-uat-evidence
export UAT_ASSETS="$UAT_ROOT/assets"
export UAT_CAPTURE="$UAT_ROOT/provider-capture"
mkdir -p "$OMNIPUS_HOME" "$UAT_ASSETS" "$UAT_CAPTURE"

: "${DEVPOD_PREVIEW_URL:?DEVPOD_PREVIEW_URL must be supplied by the devpod}"
printf 'Preview URL: %s\n' "$DEVPOD_PREVIEW_URL"
```

**Fail readiness if:**

- `OMNIPUS_E2E_NO_VISION_MODEL` is unset, empty, or points to a model that accepts image/PDF input.
- `DEVPOD_PREVIEW_URL` is unset or was guessed rather than read from the environment.
- The gateway does not listen directly on `0.0.0.0:8080`.
- `OMNIPUS_HOME` contains data from an earlier UAT run.

### 4.2 Provider credentials

Provision these through the Credential Vault / Settings provider flow. Never place secret values in this document, screenshots, shell history, provider captures, or the evidence archive.

| Credential | Purpose | Required scenarios |
|---|---|---|
| OpenRouter API key | Real text-only model (`deepseek/deepseek-chat`) | UAT-024, UAT-025 |
| xAI key, if available | Optional holdout reproduction against real xAI | Optional holdout H-01 |
| Programmable stub provider credential, if the harness requires one | Deterministic rejection and capture profiles | UAT-001 through UAT-023 |

Before UAT-024/025, confirm the selected model is exactly the value of `OMNIPUS_E2E_NO_VISION_MODEL`, is tool-capable, and is currently advertised as text-only. If its capabilities have changed, stop and select one of the documented backups after recording the evidence; do not silently continue with a vision-capable model.

### 4.3 Gateway configuration and launch

1. Check out and verify the target:

   ```bash
   git switch sendfile-fix
   test "$(git rev-parse HEAD)" = "dfa5c980beae6619b6ceab8895f38a9abf67c250"
   ```

2. Build the SPA, synchronize it into the embedded SPA directory, and build the Go binary using the repository's canonical build path/tags.
3. In the isolated `$OMNIPUS_HOME/config.json`, configure `gateway.host` as `0.0.0.0` and `gateway.port` as `8080`. Do not use a loopback bind and do not introduce a forwarding bridge.
4. Launch the embedded gateway with `$OMNIPUS_HOME` exported.
5. Verify the listener and public route:

   ```bash
   ss -ltnp | grep ':8080'
   curl -fsS "$DEVPOD_PREVIEW_URL/api/v1/state" >/dev/null
   ```

6. Complete onboarding with a dedicated UAT admin account. Select a stub-backed agent for deterministic scenarios and preserve Mia for the real-model cases.
7. Open browser developer tools:
   - Console: preserve log, clear before each case.
   - Network: preserve log; filter `ws` and `/api/v1/upload`.
   - Do not enable console filters that could hide errors.

### 4.4 Programmable provider stub contract

The stub is external test infrastructure, not an Omnipus implementation surface. It must:

- expose an OpenAI-compatible chat endpoint;
- capture every request as one JSON object per line in `$UAT_CAPTURE/provider-requests.jsonl`;
- include `test_case`, `attempt`, timestamp, request body, and selected response profile in each capture;
- support response sequences, so the first and second calls can differ;
- return deterministic assistant content `UAT_OK_<test-case>` for successful attempts;
- never redact the request body in the local capture, because media assertions depend on it;
- redact Authorization headers before writing capture evidence.

Required profiles:

| Profile | Attempt 1 | Attempt 2 | Intended use |
|---|---|---|---|
| `accept-capture` | 200, assistant `UAT_OK` | not expected | Normalization, archives, OOXML, opaque binary |
| `xai-format-then-ok` | 400 exact xAI incident body | 200 `UAT_OK` | Format rejection; exactly one retry |
| `no-image-then-ok` | 400 capability-absence body | 200 `UAT_OK` | Capability-absence downgrade |
| `reject-media-twice` | 400 media rejection | same 400 | Terminal second rejection |
| `pdf-reject-then-ok` | 400 PDF-not-supported body | 200 `UAT_OK` | PDF downgrade |
| `generic-400` | 400 raw JSON body | not expected | Provider-rejected translation |
| `payload-413` | 413 raw JSON body | not expected | 413 classification |
| `timeout-408` | 408 or controlled timeout | not expected after normal transport policy | Network translation |
| `server-503` | 503 raw body | not expected after configured provider retry policy is exhausted | Network translation |
| `content-policy-400` | 400 content-policy body | not expected | Content-policy translation |
| `provider-429` | 429 raw body | not expected | Provider rate-limit format |
| `context-long-400` | 400 body with signal after byte 512 | not expected | Full-body classification |
| `garbage-error` | malformed/unclassified failure | not expected | Unknown translation |

### 4.5 Standard evidence naming

Create one directory per case:

```text
$UAT_ROOT/results/UAT-NNN/
  01-before.png
  02-action.png
  03-result.png
  04-verbose-details.png       # only when required
  console.txt
  provider-capture.jsonl
  gateway-log.txt
  transcript.jsonl
  result.txt
```

`result.txt` must contain target commit, tester, UTC timestamp, session id, fixture hashes, profile, PASS/FAIL, and defect link if failed.

---

## 5. Exact test assets

### 5.1 Fixture inventory

Prepare the following assets under `$UAT_ASSETS`. Record `sha256sum "$UAT_ASSETS"/*` before execution and attach the manifest to every test run. Image fixtures must contain the stated pixels; a renamed PNG is not a valid AVIF/HEIC/WebP/BMP/TIFF fixture.

| ID / filename | Exact content contract | Primary use |
|---|---|---|
| `IMG-01-quadrants-2x2.jpg` | Valid JPEG, 2×2 pixels: top-left red `#ff0000`, top-right green `#00ff00`, bottom-left blue `#0000ff`, bottom-right white `#ffffff`. | JPEG→PNG |
| `IMG-02-static-red-2x2.gif` | Valid single-frame GIF89a, 2×2 solid red. | Static GIF→PNG |
| `IMG-03-animated-rgb-3f.gif` | Valid looping GIF89a, 2×2, three frames in order: red, green, blue; 500 ms per frame. | Animated GIF first-frame behavior |
| `IMG-04-quadrants-2x2.webp` | Lossless WebP rendering the same 2×2 quadrant pixels as IMG-01. | WebP→PNG |
| `IMG-05-quadrants-2x2.bmp` | 24-bit BMP rendering the same 2×2 quadrant pixels as IMG-01. | BMP→PNG |
| `IMG-06-quadrants-2x2.tiff` | Little-endian, uncompressed RGB TIFF rendering the same 2×2 quadrant pixels as IMG-01. | TIFF→PNG |
| `IMG-07-red-2x2.avif` | Valid AVIF, 2×2 solid red. | AVIF fallback |
| `IMG-08-red-2x2.heic` | Valid HEIC, 2×2 solid red. | HEIC fallback |
| `IMG-09-red-circle.svg` | UTF-8 exact text: `<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><circle cx="1" cy="1" r="1" fill="#ff0000"/></svg>` followed by LF. | SVG fallback |
| `IMG-10-corrupt.png` | Exact 12 bytes: PNG signature `89 50 4e 47 0d 0a 1a 0a` followed by ASCII `BAD!`. | Corrupt image |
| `IMG-11-over-limit.png` | A valid PNG followed by zero bytes until total file size equals the configured attachment `maxSize + 1` byte. Record configured limit and actual byte count in `result.txt`. | Oversize image |
| `IMG-12-delete-after-upload.png` | Same bytes as the known-valid 67-byte tiny PNG used by `tests/e2e/media.spec.ts`, then deleted from the media store after upload but before send. | Missing file |
| `DOC-01-one-page.pdf` | Valid one-page PDF with visible text `ADR051 PDF FALLBACK`. Use this text, not a blank scan, so extraction can be proven. | PDF fallback |
| `ARC-01-two-entry.zip` | ZIP entries: `alpha.txt` = `alpha ADR051\n`; `dir/beta.csv` = `name,value\nbeta,51\n`; no other entries. | ZIP manifest |
| `ARC-02-two-entry.tar.gz` | TAR.GZ with the same two paths and byte contents as ARC-01; no other entries. | TAR.GZ manifest |
| `ARC-03-cap-1000.zip` | 1,000 entries named `entry-0000.txt` through `entry-0999.txt`, each containing exactly `x`; no compression nesting. | Manifest cap / zip-bomb defense |
| `ARC-04-protected.zip` | Traditional password-protected ZIP; password `adr051`; one entry `secret.txt` = `classified ADR051\n`. Password is test-only and may appear in the local fixture manifest, but need not be given to Omnipus. | Protected note |
| `ARC-05-corrupt.zip` | Exact bytes: ASCII `PK\x03\x04ADR051-CORRUPT` with no central directory. | Corrupt archive |
| `BIN-01-minimal.exe` | Exact 1,026 bytes: ASCII `MZ`, followed by 1,024 zero bytes. It need not be executable. | Opaque binary note |
| `OOX-01-magic.bin` | Valid minimal DOCX package with paragraph text `ADR051 OOXML MAGIC`; filename `.bin`, MIME forced to `application/octet-stream`. | Wrong extension/MIME magic detection |
| `OOX-02-renamed.zip` | Byte-for-byte copy of OOX-01 but named `ooxml-renamed.zip`; still contains `[Content_Types].xml`, `_rels/.rels`, and `word/document.xml` with text `ADR051 OOXML MAGIC`. | Wave 1 pinned OOXML-as-ZIP case |
| `ERR-01-xai.json` | `{"error":{"code":"invalid-argument","message":"Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image."}}` | Original incident |
| `ERR-02-no-image.json` | `{"error":{"message":"The selected model does not support image input."}}` | Capability absence |
| `ERR-03-generic400.json` | `{"error":{"message":"Provider returned error","request_id":"uat-secret-request-400"}}` | Generic 400 leak check |
| `ERR-04-413.json` | `{"error":{"message":"payload exceeds provider request limit","request_id":"uat-secret-request-413"}}` | 413 |
| `ERR-05-policy.json` | `{"error":{"message":"blocked by content policy","request_id":"uat-secret-policy"}}` | Content policy |
| `ERR-06-429.json` | `{"error":{"message":"Too many requests","request_id":"uat-secret-429"}}` | Provider rate limit |
| `ERR-07-long-context.json` | 700 ASCII `x` characters, then `context length exceeded`, then LF. Signal begins after byte 512. | Pre-truncation classifier |
| `ERR-08-garbage.txt` | Exact text `@@@ ADR051 unparseable upstream failure @@@\n`. | Unknown error |

### 5.2 Fixture validity checks

Before UAT, the archive user and log auditor jointly verify:

- All hashes are recorded.
- Every image decoder reports the intended real format.
- `IMG-03` has exactly three frames in red/green/blue order.
- ARC-01 and ARC-02 have exactly the two named entries and exact content.
- ARC-03 has exactly 1,000 entries.
- ARC-04 cannot be extracted without a password and can be extracted with `adr051`.
- OOX-01 and OOX-02 are valid OOXML ZIP packages and contain the exact phrase.
- No fixture contains a real credential or customer data.

---

## 6. Test matrix

### 6.1 BDD-to-UAT traceability

| Spec BDD scenario | User story | UAT case(s) |
|---|---:|---|
| JPEG is normalized to PNG and accepted | US-1 | UAT-001 |
| WebP/BMP/TIFF normalized via x/image | US-1 | UAT-004 |
| xAI-style format rejection downgrades and retries | US-1 | UAT-008 |
| Capability-absence downgrades all images | US-1 | UAT-009 |
| AVIF/HEIC/SVG downgrade | US-1 | UAT-005 |
| Second media rejection terminates with translated error | US-1 | UAT-011 |
| ZIP and TAR.GZ yield manifests | US-2 | UAT-012 |
| EXE yields a metadata note | US-2 | UAT-013 |
| OOXML with wrong extension extracts via magic bytes | US-3 | UAT-014 |
| Provider 400 surfaces translated, not raw | US-4 | UAT-017 |
| Rate limit deduped against RateLimitFrame | US-4 | UAT-021, UAT-022 |
| Detail visible only under Verbose Chat | US-4 | UAT-023 |
| Live error dedupes on reload | US-4 | UAT-027 |
| Live error renders without reload | US-5 | UAT-026 |
| Real vision-less model — image fallback | US-6 | UAT-024 |
| Real vision-less model — PDF fallback | US-6 | UAT-025 |
| Error classification outline | US-4 | UAT-017 through UAT-022, UAT-028 |

### 6.2 Edge-case coverage

| Edge case | UAT case |
|---|---|
| Static GIF | UAT-002 |
| Animated GIF first frame | UAT-003 |
| Corrupt image | UAT-006A |
| Oversize image | UAT-006B |
| Missing media file | UAT-006C |
| One of N images rejected / image-only downgrade | UAT-010 |
| PDF rejected by non-capable model | UAT-015 and UAT-025 |
| OOXML renamed `.zip` pinned by Wave 1 | UAT-014B |
| Zip-bomb cap | UAT-016A |
| Password-protected ZIP | UAT-016B |
| Corrupt archive | UAT-016C |
| HTTP 413 is not context-too-long | UAT-018 |
| Body signal after byte 512 | UAT-028A |
| Content policy | UAT-020 |
| 408/5xx network | UAT-019 |
| Unknown garbage | UAT-028B |
| Kickoff reject, duplicate vs real failure | UAT-029 |
| Cancel acknowledgment | UAT-030A |
| Cancel background work, session isolation | UAT-030B |

---

## 7. Detailed test cases

### UAT-001 — JPEG normalizes to PNG and succeeds

**Prerequisites:** Stub profile `accept-capture`; fresh session; Verbose Chat off.  
**Fixtures:** `IMG-01-quadrants-2x2.jpg`.  
**Persona:** Power user + log auditor.

**Steps:**

1. Clear console, network, provider capture, and relevant log tail.
2. Open a fresh session using the stub-backed agent.
3. Attach `IMG-01-quadrants-2x2.jpg` and send: `UAT-001: acknowledge this attachment in one sentence.`
4. Wait until Stop disappears and the assistant bubble is non-empty and not streaming.
5. Inspect the first provider request capture.

**Expected UI:** One user turn and one non-empty assistant response containing the deterministic stub success; no error bubble, raw JSON, or Technical details disclosure.  
**Expected provider/log:** Exactly one provider call. The attached content starts with `data:image/png;base64,`, not JPEG. Decoding the data URL produces a valid 2×2 PNG with the four expected pixels. No downgrade/retry entry.  
**Screenshot checklist:** before send with JPEG chip; completed assistant bubble; provider capture showing MIME prefix with base64 collapsed/redacted from the screenshot.  
**Pass evidence:** one request; PNG signature after decode; pixel assertion; no browser console errors; completed turn. Any `data:image/jpeg` request is FAIL.

### UAT-002 — Static GIF normalizes to static PNG

**Prerequisites:** Same as UAT-001.  
**Fixtures:** `IMG-02-static-red-2x2.gif`.

**Steps:** Repeat UAT-001 with the GIF.

**Expected:** One provider call with a PNG data URL; decoded image is 2×2 solid red; successful non-empty response. No GIF data URL reaches the provider.  
**Screenshots/evidence:** attachment chip, result bubble, capture prefix; decoded PNG properties in `result.txt`.

### UAT-003 — Animated GIF becomes first-frame static PNG

**Prerequisites:** Same as UAT-001.  
**Fixtures:** `IMG-03-animated-rgb-3f.gif`.

**Steps:** Attach the three-frame GIF and send `UAT-003: process the attachment.` Inspect captured outbound PNG.

**Expected UI:** Turn succeeds; no warning/error is required for accepted animation loss.  
**Expected provider/log:** Exactly one PNG image, one frame, 2×2 solid red. Green and blue frames are absent. No raw GIF reaches the provider.  
**Screenshots:** animated fixture preview/chip; completed result; capture and decoded first-frame proof.  
**Pass:** Static red PNG plus successful turn. Multiple outbound frames, GIF MIME, or a non-first frame is FAIL.

### UAT-004 — WebP, BMP, and TIFF normalize via pure-Go decoders

**Prerequisites:** Stub profile `accept-capture`.  
**Fixtures:** `IMG-04-quadrants-2x2.webp`, `IMG-05-quadrants-2x2.bmp`, `IMG-06-quadrants-2x2.tiff`.

**Steps:** Run three subcases in separate sessions. Attach one fixture, send `UAT-004-<format>: process this attachment.`, and inspect capture.

**Expected:** Each subcase produces exactly one provider call containing `data:image/png;base64,`. Decoded output preserves the exact quadrant pixels. No downgrade note or second call.  
**Screenshots:** one completed bubble and one capture proof per format.  
**Pass:** 3/3 format subcases pass. A single failed format fails UAT-004.

### UAT-005 — AVIF, HEIC, and SVG use safe fallback

**Prerequisites:** Stub profile capable of recording the request and accepting a text-only retry; fresh session per format.  
**Fixtures:** `IMG-07-red-2x2.avif`, `IMG-08-red-2x2.heic`, `IMG-09-red-circle.svg`.

**Steps:** For each format, attach the file and ask `UAT-005-<format>: continue even if this format is unavailable.`

**Expected UI:** The turn completes with non-empty text. No raw provider error appears.  
**Expected provider/log:** No raw AVIF/HEIC/SVG binary content block reaches the provider. The request contains an honest attachment-unavailable/downgrade note; if a provider rejection is involved, there is at most one retry.  
**Screenshots:** attachment, final bubble, capture proving absence of forbidden data-URL MIME.  
**Pass:** 3/3 safe fallbacks, no empty reply, no more than two total provider calls in a rejection subcase.

### UAT-006 — Corrupt, oversized, and missing image files

#### UAT-006A — Corrupt image

**Fixture:** `IMG-10-corrupt.png`.  
**Steps:** Attach and send `UAT-006A: continue if unreadable.`  
**Expected:** No raw image block reaches provider; UI completes with an unavailable/unreadable note or a successful response based on that note. `gateway.log` contains a decode/read warning. No raw provider JSON.

#### UAT-006B — Oversize image

**Fixture:** `IMG-11-over-limit.png`.  
**Steps:** Record configured `maxSize`, verify fixture is exactly `maxSize + 1`, attach, and send.  
**Expected:** No image data URL; note identifies too-large/unreadable condition; log records size and limit; no provider media rejection is required; one provider call at most.

#### UAT-006C — Missing file after upload

**Fixture:** `IMG-12-delete-after-upload.png`.  
**Steps:** Upload through the UI, have the operator delete only that uploaded fixture from the isolated UAT media store before Send, then send.  
**Expected:** Visible attachment-unavailable marker; gateway remains healthy; no panic or local filesystem path in UI; one successful text-only call if the turn proceeds.

**Screenshots:** one result per subcase; log excerpt for each.  
**Pass:** all three fail safely, no empty/stuck turn, no forbidden raw block, no local path leak.

### UAT-007 — Multiple images: downgrade is scoped and safe

**Prerequisites:** Stub sequence rejects the request because of one image and records both attempts.  
**Fixtures:** `IMG-01-quadrants-2x2.jpg` plus `IMG-09-red-circle.svg`.

**Steps:** Attach both in one message. Configure first call to return a media rejection and second to accept. Send `UAT-007: tell me which attachments remained available.`

**Expected:** Exactly two calls. The retry contains no provider-rejected media. The response is non-empty and does not claim unavailable content was seen. No third call.  
**Evidence:** request diff showing media before and after downgrade, final bubble, log count.  
**Pass:** bounded retry and honest note. This case records whether downgrade is per-offending-media or all-image fallback; any raw rejected media on attempt 2 or more than one retry is FAIL.

### UAT-008 — Exact xAI incident string triggers downgrade and one retry

**Prerequisites:** Profile `xai-format-then-ok`; Verbose Chat off.  
**Fixtures:** `IMG-01-quadrants-2x2.jpg`, `ERR-01-xai.json`.

**Steps:**

1. Attach the JPEG and send `UAT-008: continue if the provider rejects this image.`
2. Stub returns the exact HTTP 400 body on attempt 1 and `UAT_OK_UAT-008` on attempt 2.
3. Observe live UI without reloading.
4. Inspect request counts, log, transcript, DOM, and console.

**Expected UI:** One final successful assistant bubble; no raw xAI phrase; no empty reply.  
**Expected provider/log:** Exactly two calls. Attempt 2 contains the downgrade note and no rejected image block. `gateway.log` contains the exact incident phrase and `media_unsupported`; transcript does not contain the phrase.  
**Screenshots:** before send, successful bubble, no Technical details, provider sequence.  
**Pass:** exact one retry, success, raw phrase absent from DOM/console/transcript.

### UAT-009 — Capability-absence rejection downgrades images

**Prerequisites:** Profile `no-image-then-ok`.  
**Fixtures:** `IMG-01-quadrants-2x2.jpg`, `ERR-02-no-image.json`.

**Steps:** Attach image, send, let stub reject once with `does not support image input`, then accept retry.

**Expected:** Exactly two calls; second has no image block and contains a continue-without-image note; non-empty final reply; no raw body in DOM/console/transcript; log contains raw body.  
**Screenshots/evidence:** same classes as UAT-008.  
**Pass:** classifier treats capability absence as media unsupported and turn succeeds after one retry.

### UAT-010 — PDF rejection by a non-capable stub model

**Prerequisites:** Profile `pdf-reject-then-ok`; model configuration that initially sends a native PDF block.  
**Fixtures:** `DOC-01-one-page.pdf`.

**Steps:** Attach the PDF and ask `UAT-010: quote the attached marker text.` First call rejects PDF; second accepts extracted text.

**Expected:** Exactly two calls. Attempt 1 may contain native PDF. Attempt 2 contains `ADR051 PDF FALLBACK` as text and no `data:application/pdf` block. Final response is non-empty.  
**Screenshots:** PDF attachment, final response, attempt diff.  
**Pass:** one downgrade retry only; extracted marker reaches second request; no raw rejection visible.

### UAT-011 — Second media rejection is terminal and translated

**Prerequisites:** Profile `reject-media-twice`; Verbose Chat off.  
**Fixtures:** `IMG-01-quadrants-2x2.jpg`, `ERR-01-xai.json`.

**Steps:** Send the attachment. Stub rejects both initial and downgraded attempt. Wait for turn terminal state.

**Expected UI:** Exactly one terminal error bubble with `The AI service couldn't process an attachment. I've continued without it.` or the canonical `media_unsupported` display copy; no empty invisible failure; no raw JSON or provider identity.  
**Expected provider/log:** Exactly two calls total, never three. Raw bodies exist in `gateway.log`. Transcript error entry includes `error_code:"media_unsupported"`, translated message, and no raw phrase/detail.  
**Screenshots:** terminal translated bubble; console clear; provider call count.  
**Pass:** two calls, one visible translated bubble, no forbidden raw text.

### UAT-012 — ZIP and TAR.GZ produce manifests

**Prerequisites:** Profile `accept-capture`.  
**Fixtures:** `ARC-01-two-entry.zip`, `ARC-02-two-entry.tar.gz`.

**Steps:** Run each archive in a fresh session. Ask `List every attached entry and its size; do not use shell or file tools.`

**Expected UI:** Response names `alpha.txt` and `dir/beta.csv`; no extraction error.  
**Expected provider/log:** Request contains a manifest with both entry names and sizes. It contains no archive data URL/raw ZIP/TAR bytes. One call.  
**Screenshots:** attached archive and resulting list for each; provider manifest excerpt.  
**Pass:** both manifests accurate and bounded, raw bytes absent.

### UAT-013 — Opaque EXE becomes a metadata note

**Prerequisites:** Profile `accept-capture`.  
**Fixture:** `BIN-01-minimal.exe`.

**Steps:** Attach and ask `Describe what metadata is available; do not inspect with tools.`

**Expected:** UI indicates file/format/size and inability to extract content. Provider request has a concise metadata note and no MZ payload/data URL. No local path in UI or request.  
**Screenshots:** attachment and response; capture note.  
**Pass:** one call, no raw EXE bytes, honest metadata only.

### UAT-014 — OOXML extraction with wrong extension and MIME

#### UAT-014A — `.bin` / octet-stream

**Fixture:** `OOX-01-magic.bin`.  
**Steps:** Upload with octet-stream MIME and ask `Return the exact marker text from the attachment.`  
**Expected:** Request contains `ADR051 OOXML MAGIC` as extracted text; no ZIP binary block.

#### UAT-014B — Valid OOXML renamed `.zip` (Wave 1 pin)

**Fixture:** `OOX-02-renamed.zip`.  
**Steps:** Attach and send the same prompt.  
**Expected:** The OOXML document is recognized by package magic/structure rather than treated as a generic archive; extracted text includes the exact marker. It must not merely return a manifest of XML part names.

**Screenshots:** result bubble for both; provider capture showing marker.  
**Pass:** both subcases extract exact text; raw package bytes absent.

### UAT-015 — PDF is downgraded before a known non-capable model call

**Prerequisites:** Stub/capture model configured as PDF-less so the conservative model gate chooses text fallback without a rejection round-trip.  
**Fixture:** `DOC-01-one-page.pdf`.

**Steps:** Attach PDF and ask for the exact marker.

**Expected:** One provider call containing extracted `ADR051 PDF FALLBACK` text and no PDF data URL. Final response non-empty.  
**Screenshots/evidence:** PDF chip, response, capture.  
**Pass:** one-call text fallback. A provider 400 due to a sent PDF is FAIL for this predictive gate scenario.

### UAT-016 — Archive safety edges

#### UAT-016A — 1,000-entry cap

**Fixture:** `ARC-03-cap-1000.zip`.  
**Expected:** Manifest is capped and includes an explicit truncation/cap note. UI remains responsive, gateway memory remains stable, and the provider request is bounded. No nested expansion or raw bytes.

#### UAT-016B — Password-protected ZIP

**Fixture:** `ARC-04-protected.zip`.  
**Expected:** Names that can be enumerated are shown and the note says protected/encrypted; secret contents are not fabricated and raw bytes are not sent.

#### UAT-016C — Corrupt ZIP

**Fixture:** `ARC-05-corrupt.zip`.  
**Expected:** Honest corrupt/unreadable note with a bounded reason; gateway stays healthy; no panic, local path, or raw archive data.

**Steps:** In three fresh sessions, attach one asset and ask for its manifest without using tools.  
**Screenshots:** result and provider capture per subcase; resource/log evidence for cap case.  
**Pass:** all three fail safely and remain bounded.

### UAT-017 — Generic provider 400 is translated, not raw

**Prerequisites:** Profile `generic-400`; Verbose Chat off.  
**Fixture:** `ERR-03-generic400.json`.

**Steps:** Send a plain text prompt. Observe the error live. Search the full DOM, console, transcript, and gateway log for `Provider returned error` and `uat-secret-request-400`.

**Expected UI:** One terminal bubble: `The AI service rejected the request.` No Technical details. No raw JSON/request id.  
**Expected log/transcript:** Raw body and request id are present in `gateway.log`; transcript contains `error_code:"provider_rejected"`, translated message, and no raw text/detail.  
**Screenshots:** error bubble; DOM search zero result; log positive grep with secrets obscured in screenshot.  
**Pass:** raw only in gateway log; live translated bubble appears without reload.

### UAT-018 — HTTP 413 maps to provider_rejected, not context_too_long

**Prerequisites:** Profile `payload-413`.  
**Fixture:** `ERR-04-413.json`.

**Steps:** Send a plain text prompt and inspect live error plus transcript.

**Expected:** One bubble `The AI service rejected the request.`; `error_code:"provider_rejected"`; `error_retryable` absent/false. Must not show the context-too-long message. Raw 413 body only in gateway log.  
**Screenshots/evidence:** bubble, transcript field, log match.  
**Pass:** exact classification and privacy behavior.

### UAT-019 — HTTP 408 and exhausted 5xx map to retryable network errors

**Prerequisites:** Profiles `timeout-408` and `server-503`; fresh session for each.  
**Fixtures:** controlled 408 and 503 bodies.

**Steps:** Run both subcases and wait for any normal transport retry policy to exhaust. Count all provider calls separately from ADR-051 media retries.

**Expected UI:** One bubble `Couldn't reach the AI service. Please retry.` per subcase; no raw body.  
**Expected transcript:** `error_code:"network"`, `error_retryable:true`.  
**Screenshots:** each terminal bubble; log status.  
**Pass:** 2/2 classified network/retryable; no duplicate bubbles.

### UAT-020 — Content-policy rejection is not retried as media

**Prerequisites:** Profile `content-policy-400`.  
**Fixture:** `ERR-05-policy.json`.

**Steps:** Send a plain text prompt that causes the stub response; do not use actual disallowed material.

**Expected:** One terminal bubble `The AI service declined this request.`; one provider call under the profile; no media downgrade retry; `error_code:"content_policy"`, retryable false; raw policy JSON only in log.  
**Screenshots/evidence:** bubble, call count, transcript.  
**Pass:** no media retry and correct copy/code.

### UAT-021 — Provider HTTP 429 is deduped

**Prerequisites:** Profile `provider-429`.  
**Fixture:** `ERR-06-429.json`.

**Steps:** Send a prompt. Observe rate-limit UI and count visible bubbles/messages before and after ten seconds. Reload once and recount.

**Expected UI:** One rate-limit user signal with retry guidance, not both a RateLimitFrame UI and a generic LLM error bubble. No raw 429 JSON. Reload does not add a duplicate.  
**Expected transcript/log:** Rate-limit entry remains friendly; raw provider 429 in gateway log only; classification is `rate_limited`, retryable true where persisted.  
**Screenshots:** single live signal; single replayed signal.  
**Pass:** exactly one visible rate-limit signal live and after reload.

### UAT-022 — Internal rate limit uses authoritative RateLimitFrame

**Prerequisites:** Dedicated non-core/custom UAT agent configured with `max_llm_calls_per_hour=1`, retry hint 30 seconds, and a successful stub provider.  
**Fixture/message format:** `rate limit: max_llm_calls_per_hour (retry after 30s)`.

**Steps:**

1. In one session, send a first prompt and allow it to succeed.
2. Immediately send a second prompt to exceed the internal limit.
3. Observe live UI and count provider calls and bubbles.
4. Reload and inspect the persisted rate-limit entry.

**Expected:** Second prompt does not call provider. One authoritative rate-limit UI signal, no generic duplicate error bubble. Retry hint remains meaningful. Transcript does not contain provider raw text and does not create a duplicate error entry.  
**Screenshots:** successful first turn; single rate-limit state on second; post-reload state.  
**Pass:** second call blocked locally, one signal only, no duplicate live/replay bubble.

### UAT-023 — Verbose Chat gate: off, on/collapsed, on/expanded

**Prerequisites:** Profile `generic-400`; use the same raw token `uat-secret-request-400`; fresh session for each subcase.  
**Fixture:** `ERR-03-generic400.json`.

#### Off

1. Settings → Chat → ensure **Verbose Chat** is off.
2. Trigger the error.
3. Search `document.body.innerText` and DOM text content for the raw message/request id.

**Expected:** Generic translated bubble only. No Technical details element. Raw detail absent from DOM, console, and transcript; present only in `gateway.log`.

#### On, disclosure collapsed

1. Enable **Verbose Chat** in Settings → Chat and return to a new session.
2. Trigger the same error.

**Expected:** Generic translated bubble and a visible `Technical details` disclosure control. The raw detail is not visually exposed until the user opens it. It remains absent from console and transcript. Because disclosure implementations may keep collapsed content in the DOM, record both `innerText` and `textContent`; only visual exposure is forbidden while collapsed.

#### On, disclosure expanded

1. Click `Technical details`.
2. Capture the expanded disclosure.

**Expected:** Raw status/body detail becomes visible inside that error bubble only, capped/bounded; translated primary message remains unchanged. Raw detail remains absent from console and transcript and is still present in `gateway.log`.

**Screenshots:** off bubble; on/collapsed disclosure; on/expanded detail; Settings toggle.  
**Pass:** gate is fail-closed off, runtime-toggleable on, and detail never persists. Detail visible while Verbose Chat is off is an immediate FAIL.

### UAT-024 — Real vision-less model image fallback

**Prerequisites:** `OMNIPUS_E2E_NO_VISION_MODEL=deepseek/deepseek-chat`; live OpenRouter credential; fresh Mia session; console clear; no stub.  
**Fixture:** `IMG-12-delete-after-upload.png` bytes before deletion, but for this case do **not** delete it; filename `tiny.png`.

**Steps:**

1. Verify the env var is non-empty. As a readiness-negative check, launch the test harness once in a child shell with the variable unset and record that it fails rather than skips; then restore it.
2. Select the exact model from the env var for the turn.
3. Attach valid `tiny.png` and send `What colour is the image I sent? One short sentence.`
4. Wait up to six minutes for downgrade/retry and a settled non-empty response.
5. Inspect DOM, console, gateway log, and transcript.

**Expected:** Turn completes with non-empty text and non-error status. A vision-less model may answer from the downgrade note rather than image content; semantic color accuracy is not required. No raw provider JSON or capability string in DOM/console/transcript. At most one media downgrade retry.  
**Screenshots:** selected model; attached image; completed response; no error status.  
**Pass:** real live call completes, no empty reply/raw leak, fail-if-unset check proven.

### UAT-025 — Real vision-less model PDF fallback

**Prerequisites:** Same as UAT-024.  
**Fixture:** `DOC-01-one-page.pdf`.

**Steps:** Select exact text-only model, attach PDF, send `Return the marker text from the attached PDF in one sentence.`

**Expected:** Turn completes through PDF text fallback; response contains or correctly references `ADR051 PDF FALLBACK`; no raw provider JSON; no error status.  
**Screenshots/evidence:** model selection, PDF chip, final response, console.  
**Pass:** non-empty successful reply based on extracted text.

### UAT-026 — Live error renders without reload

**Prerequisites:** Profile `generic-400`; navigation event recording enabled.  
**Fixture:** `ERR-03-generic400.json`.

**Steps:**

1. Record current URL, active session id, and navigation count.
2. Send prompt and do not refresh, navigate, change session, or reopen the page.
3. Wait for the translated bubble.
4. Re-record URL, session id, and navigation count.

**Expected:** Error appears live in the existing DOM. URL and active session id do not change; no main-frame navigation occurs. Composer leaves streaming state.  
**Screenshots:** before action and visible live error in same session.  
**Pass:** live visible bubble with zero navigation/reload events.

### UAT-027 — Live and replay show exactly one bubble by entry id

**Prerequisites:** Profile `generic-400`; replay user; know session id.  
**Fixture:** `ERR-03-generic400.json`.

**Steps:**

1. Trigger the error and count bubbles containing `The AI service rejected the request.`; count must be one.
2. Save the transcript error entry id.
3. Reload the page and wait for replay completion.
4. Count matching bubbles again.
5. Navigate away to another session, then reopen the original session and count a third time.

**Expected:** Count is exactly one live, after reload, and after reopen. Error code/message remain identical. No extra bubble is introduced by replay.  
**Screenshots:** live count; post-reload count; reopened session count.  
**Pass:** `1 → 1 → 1`, same entry id and message.

### UAT-028 — Remaining classification boundaries

#### UAT-028A — Context signal after byte 512

**Profile/fixture:** `context-long-400`, `ERR-07-long-context.json`.  
**Expected:** `context_too_long`, retryable false, message `The conversation is too long for the model; trim and retry.` The late signal is recognized even though it begins after byte 512. Raw long body only in log; transcript stores translated message/code.

#### UAT-028B — Unparseable failure

**Profile/fixture:** `garbage-error`, `ERR-08-garbage.txt`.  
**Expected:** `unknown`, retryable false, message `The AI service encountered an error.` Raw garbage absent from DOM/console/transcript and present in log.

**Screenshots:** one translated bubble per subcase; transcript field; log positive grep.  
**Pass:** both conservative classifications are exact.

### UAT-029 — Workspace kickoff rejection regression

**Prerequisites:** Isolated disposable workspace configured to launch the setup interview; programmable kickoff response; no existing user chat in the pending bucket.  
**Fixtures:** Two untagged error messages: `workspace setup already ran` and `workspace setup kickoff failed`.

#### Duplicate kickoff

1. Open the disposable workspace and cause a duplicate setup kickoff while its pending setup placeholder is foreground.
2. Deliver the untagged duplicate message containing `already`.

**Expected:** No LLM translated error bubble. Pending placeholder is cleaned up. Default toast says `Workspace setup already ran — find the interview in your sessions list.` Composer is not stuck. Console may contain the known diagnostic warning `chat.workspace_setup_kickoff_rejected`; this is expected for this regression case and must be captured, not treated as a provider leak.

#### Real failure

1. Reset workspace setup state and start again.
2. Deliver untagged `workspace setup kickoff failed`.

**Expected:** No LLM translated error bubble. Warning toast begins `Could not start the workspace setup interview:` and includes the actionable kickoff failure. Pending state clears so reopening can retry. The error must not be misattributed to another open session.

**Screenshots:** duplicate default toast; real-failure warning toast; usable composer after each.  
**Pass:** duplicate and real failure remain distinct, neither becomes an ADR-051 generic LLM bubble, and no `__pending` session remains active.

### UAT-030 — Cancellation acknowledgment and background-work regression

#### UAT-030A — Cancel acknowledgment preserves partial output

**Prerequisites:** Provider/stub capable of slow streaming; fresh session.  
**Steps:** Start a response that streams at least one visible token, click Stop, and wait for cancel acknowledgment.

**Expected:** Existing partial assistant content remains; bubble status becomes interrupted rather than error; no generic network/provider error copy; no Technical details; composer becomes usable. A `turn.cancel` acknowledgment must not produce a second bubble or connection-error banner.  
**Screenshots:** partial output before Stop; interrupted bubble after acknowledgment.  
**Pass:** partial content preserved, one interrupted bubble, no LLM translation.

#### UAT-030B — Cancel background work and preserve session isolation

**Prerequisites:** Two sessions A and B. In each, start a long background bash task that emits a unique heartbeat file/log line for at least 60 seconds. Ensure the initiating turns have already completed while background work remains active.  
**Steps:**

1. Confirm Activity Panel shows both background jobs running.
2. In session A, invoke the session cancellation/Stop control used to cancel background work.
3. Wait for activity status updates.
4. Confirm A stops producing heartbeat output and is labeled canceled.
5. Confirm B continues producing heartbeat output and remains running.
6. Stop B during cleanup.

**Expected:** A's background session is killed even though its foreground turn already ended; B is unaffected. Cancellation does not surface as a translated provider error. `gateway.log`/audit includes the killed count for A.  
**Screenshots:** both running; A canceled/B running; final cleanup.  
**Pass:** strict session isolation, observable canceled state, no orphaned A process.

---

## 8. Standard evidence and machine checks

### 8.1 Screenshot checklist

Every test case must include:

- browser URL and target workspace/session context;
- attachment filename(s) before Send for media cases;
- final assistant bubble and visible status;
- any expected toast, rate-limit surface, or disclosure;
- provider request count/capture for normalization, archive, and retry cases;
- a console view showing no unexpected errors, except the explicitly expected kickoff diagnostic;
- post-reload state for replay/dedupe cases.

Do not include provider keys, bearer tokens, cookies, CSRF tokens, raw Authorization headers, or unrelated user data.

### 8.2 Console expectations

Default expectation: zero unexpected `console.error` and zero raw provider body/request-id strings. WebSocket reconnect warnings are acceptable only when intentionally induced and must not coincide with a lost/duplicated bubble.

For each sensitive token, save a console export and prove zero matches:

```text
valid JPG, PNG, WebP, or ICO image
Provider returned error
uat-secret-request-400
uat-secret-request-413
uat-secret-policy
uat-secret-429
```

UAT-029 is the only planned case where `chat.workspace_setup_kickoff_rejected` is an expected warning.

### 8.3 Gateway log checks

Locate the active log at `$OMNIPUS_HOME/logs/gateway.log`. Save only a bounded excerpt around the session/test-case id.

Positive checks for provider-error scenarios:

```bash
rg -n 'valid JPG, PNG, WebP, or ICO image|Provider returned error|uat-secret-request-400|Too many requests|context length exceeded' \
  "$OMNIPUS_HOME/logs/gateway.log"
```

Negative operational checks:

```bash
! rg -n 'panic|fatal error|out of memory' "$OMNIPUS_HOME/logs/gateway.log"
```

A provider-error scenario fails if raw diagnostic detail is absent from the gateway log, because the log is the approved diagnostic source. Redact credentials, not the classification signal, when copying excerpts.

### 8.4 Transcript JSONL checks

Find the partition by session id instead of assuming a fixed agent path:

```bash
SESSION_ID='<recorded-session-id>'
find "$OMNIPUS_HOME" -type f -path "*/sessions/$SESSION_ID/*.jsonl" -print
```

For translated provider errors, verify:

- an error entry contains `error_code` with the expected code;
- `error_retryable:true` exists only for retryable classes;
- `message`/content is translated;
- there is no `detail` or `error_detail` field;
- raw body, provider identity, request id, and incident phrase are absent.

Example negative check across the isolated UAT home:

```bash
! rg -n 'valid JPG, PNG, WebP, or ICO image|Provider returned error|uat-secret-request-400|uat-secret-request-413|uat-secret-policy|uat-secret-429' \
  "$OMNIPUS_HOME"/agents/*/sessions 2>/dev/null
```

Do not run the negative check against `gateway.log`; raw diagnostics are expected there.

### 8.5 Provider-capture checks

For each case, copy only its capture records into the case directory and use JSON-line counts:

```bash
jq -c 'select(.test_case == "UAT-008")' "$UAT_CAPTURE/provider-requests.jsonl" \
  > "$UAT_ROOT/results/UAT-008/provider-capture.jsonl"

test "$(wc -l < "$UAT_ROOT/results/UAT-008/provider-capture.jsonl")" -eq 2
```

Required assertions:

- Normalization cases: first/only media URL starts `data:image/png;base64,`.
- AVIF/HEIC/SVG/corrupt/oversize/missing: forbidden raw media is absent.
- Archive/binary/OOXML: no raw binary data URL; manifest, metadata note, or extracted marker text is present.
- One-retry cases: exactly two captured calls.
- Terminal second rejection: exactly two calls, never three.
- Internal rate-limit: zero captured provider calls for the rejected second prompt.

### 8.6 Pass/fail evidence minimum

A case is **PASS** only if its directory contains:

1. required screenshots;
2. console export;
3. provider capture or `not-applicable.txt`;
4. bounded gateway log excerpt;
5. transcript partition or `not-created.txt` with explanation;
6. `result.txt` with all assertions marked PASS;
7. fixture hash manifest.

Missing evidence is FAIL, not “not observed.”

---

## 9. Defect handling

When a case fails:

1. Stop the scenario without retrying it in the same session.
2. Preserve the session id, provider profile, attempt count, fixture hash, current URL, console, network export, log excerpt, and transcript.
3. Record the first observed divergence from expected behavior.
4. Re-run once in a fresh session only to determine reproducibility; do not overwrite first-failure evidence.
5. File one defect per root symptom unless multiple failures share the same captured request/entry id.
6. Severity guidance:
   - **Blocker:** raw provider data persists/crosses while Verbose Chat is off; unbounded media retries; gateway panic; archive resource exhaustion.
   - **High:** empty reply, stuck streaming state, wrong media sent, duplicate live/replay bubble, cancellation kills another session's work.
   - **Medium:** wrong translated class/copy/retryable flag, missing manifest entry, broken verbose disclosure.
   - **Low:** screenshot-only polish or non-actionable wording drift that preserves semantics.

---

## 10. Sign-off

### 10.1 Per-scenario result template

```text
Case: UAT-___
Spec trace: US-__ / BDD scenario ____________________
Tester / role:
UTC start / end:
Commit: dfa5c980beae6619b6ceab8895f38a9abf67c250
Session id:
Workspace / agent / model:
Provider profile:
Fixture names + SHA-256:
Provider call count:
Expected error code / retryable:
Observed UI result:
Observed log result:
Observed transcript result:
Observed console result:
Screenshot files:
PASS / FAIL:
Defect URL (required on FAIL):
Notes:
```

### 10.2 Aggregate result

| Metric | Count |
|---|---:|
| Total numbered cases | 30 |
| Total required subcases | 46 |
| Passed | ___ |
| Failed | ___ |
| Blocked by environment | ___ |
| Not run | ___ |

Release acceptance requires:

- **46/46 required subcases passed**;
- zero open Blocker or High defects;
- no raw-text privacy invariant failure;
- UAT-024 and UAT-025 passed against the real text-only model;
- UAT-029 and UAT-030 passed as regressions;
- all evidence directories complete.

An environment block is not a pass and must state the exact missing prerequisite and owner.

### 10.3 Ready for manual validation

Mark this section only after readiness review.

- [ ] Target commit is exactly `dfa5c980beae6619b6ceab8895f38a9abf67c250` on `sendfile-fix`.
- [ ] Embedded SPA and Go binary were rebuilt from that commit.
- [ ] Fresh, isolated `OMNIPUS_HOME` exists.
- [ ] Gateway listens directly on `0.0.0.0:8080`.
- [ ] `$DEVPOD_PREVIEW_URL` resolves and `/api/v1/state` responds.
- [ ] `OMNIPUS_E2E_NO_VISION_MODEL=deepseek/deepseek-chat` is set; unset-child-shell check fails loudly.
- [ ] Provider keys resolve without appearing in evidence.
- [ ] Programmable stub profiles and capture redaction are verified.
- [ ] All fixtures exist, validate as their real formats, and have recorded SHA-256 hashes.
- [ ] Browser console/network preservation is enabled.
- [ ] `gateway.log` is writable and tailing works.
- [ ] Evidence directory is empty and writable.
- [ ] Operator UAT, power user, log auditor, archive user, and replay user are assigned.

**Ready for manual validation:** YES / NO  
**Readiness reviewer:** ____________________  
**UTC timestamp:** ____________________  
**Blocking prerequisite or defect, if NO:** ____________________
