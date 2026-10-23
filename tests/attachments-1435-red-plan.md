# Issue #1435 — RED test plan

## Behaviour and independent oracles

Accepted conversation attachments remain readable after a gateway restart and for the conversation's lifetime. Images sent to a provider fit its model's limits. An unavailable attachment or an image-size failure is explained by its actual cause, not by the unsupported-format message.

| Expectation | Independent source |
|---|---|
| The exact accepted attachment bytes and metadata remain accessible through the ref saved in a real conversation, including after reopening the stores and crossing the ordinary cleanup age | Issue #1435, Expected, first item |
| The resized image's patch count is at most 30,000 | Issue #1435, Expected, second item and the recorded provider rejection |
| Patch accounting uses `ceil(width / 32) * ceil(height / 32)` | OpenAI image guide, https://developers.openai.com/api/docs/guides/images-vision, read 2026-10-23; its general patch formula, not a claim that the guide separately documents gpt-6.1-sol |
| User-visible error copy names unavailability and asks to re-attach, or names an image-size limit; neither cause becomes `media_unsupported` | Issue #1435, Expected, third item |
| A known image-size rejection must not be converted to an unsupported-format failure by removing the image | Same Expected item; preserving cause through the existing strip-retry boundary |

The issue supplies the ChatGPT 30,000-patch cap. The public image guide supplies only the independent patch-count formula. Expected output dimensions, a particular resizing algorithm, new catalog field names, new error-code names, and exact replacement prose are **not** specified. Tests therefore assert decodable image output and the numeric budget invariant, and the required meaning of error copy, without prescribing those implementation choices.

## Stack and real boundary

Go tests, matching the neighbouring packages' standard `testing` and `testify/require` style. The media-store test uses the real session archive, filesystem, registry and resolver; only the clock is controlled through the store's existing test clock. The image tests use real image encoding/decoding, the embedded catalog, the real resize pipeline and real media presentation. Error tests use the real classifier, user-message mapper and strip-retry gate. No production functions are mocked. No paid API calls or live credentials are used.

## Cases (planned before assertions)

| Group | Inputs and ordering | Expected | Class |
|---|---|---|---|
| Conversation lifetime / restart | Store an uploaded file using the live upload scope and cleanup policy; append its ref to an actual conversation; stop/close both stores; reopen both; clean at cleanup age minus 1 ns, exactly cleanup age, and plus 1 ns | Conversation still contains the identical ref; identical path, metadata and bytes resolve in every case | State / boundary / positive |
| Catalog resize | Real embedded `openai-chatgpt` / `gpt-6.1-sol` limits; decoded grayscale images: 32x32, 4192x7328 (29,999 patches), 4800x6400 (30,000), 5537x5537 (30,276), 6144x6400 (38,400) | Decodable output; independently counted patches <= 30,000; output also respects catalog bytes and long edge | Positive / boundary / oversized input |
| Provider-facing image | A valid 5120x7712 PNG, exactly 160x241 = 38,560 patches, supplied both as a registered media ref and a pre-resolved data URL | Exactly one image remains in the provider-facing message and fits <= 30,000 patches; no silent attachment loss | Oversized input / integration |
| Missing ref | Real resolver returns `ErrNotFound`, direct and wrapped before turn-error translation | Public message says attachment no longer available and asks to re-attach; not unsupported format or an unrelated generic cause | Negative |
| Provider size error | Issue's exact 38,560 > 30,000 rejection, structured 400 body, body without HTTP status, and HTTP 413 image-too-large | Public message names image/attachment size; not `media_unsupported`; no generic unsupported-format advice | Negative |
| Strip-retry error provenance | Image-bearing message plus the issue's exact size error | Generic format downgrade does not remove the image or replace the cause | Negative |

Every top-level regression is run separately. Passing boundary/control subcases will be reported as controls, not as RED. Error and oversized-input cases exceed the 30% negative-case target.

## Instruments and impact

GitNexus MCP is unavailable. CLI query and upstream impact were attempted and saved, but the CLI reports a stale index from a sibling clone (4,871 commits behind when queried from the main checkout; 5,271 from this feature worktree). Its counts are not authoritative. The required pre-commit `detect-changes --scope staged` also returned `No changes detected` despite four staged additions; manual diff verification is the instrument here, not that false empty result. Direct source/caller searches confirm the store is loaded at gateway boot/reload, upload registration uses `upload:<session>` with `forget_only`, the turn presentation path consumes the catalog budget, and error translation feeds live/persisted errors. Only new test symbols and this test plan are added; no existing production symbol is edited. Impact is **Inferred**, tests-only, limited to the media, resize and agent package test binaries.

## CHECK mutation handoff — not executed in RED

| Mutant | Test expected to kill it |
|---|---|
| Drop or expire the persisted conversation upload mapping | Conversation lifetime/restart test |
| Ignore the image patch budget while still respecting bytes and long edge | Catalog resize and provider-facing image tests |
| Let pre-resolved images bypass budget enforcement | Provider-facing data-URL case |
| Map missing media to generic unknown/unsupported copy | Missing-ref message test |
| Map a size failure to unsupported format or let strip-retry relabel it | Image-size message and downgrade tests |

Step 4.1 (see RED for the right reason) is this task. Step 4.2 (GREEN), step 4.3 (mutations), and the skill's proof-of-failability checklist are **deferred to CHECK** by the RED role rules. The full local suite is prohibited; candidate CI remains authoritative.

## Deliberate gaps

| Gap | Reason / handoff |
|---|---|
| Abrupt process crash during the debounced registry save | This reproduces graceful restart plus deterministic conversation-lifetime loss, not kill/power-loss durability |
| Pixel-guard and accepted-image interaction | The registered 38,560-patch fixture currently reaches the separate 16,777,216-pixel guard and is dropped; the data-URL form bypasses it and remains oversized. The implementation needs bounded-resource downscaling, not a blind removal of decode-bomb protection |
| Exact 30,001-patch rectangle isolated from the embedded long-edge ceiling | 30,001 factors as 19x1,579, so its 32-pixel patch rectangle exceeds that separate ceiling; 29,999 / 30,000 and the square pixel-rounding overflow cover the meaningful boundary without inventing catalog fields |
| A 0-pixel image and unrelated malformed formats | Not an accepted valid attachment; existing decode tests own those cases |
| Other providers' numerical caps and token-based formulas | Not supplied by this issue; no guessed provider limits |
| HTTP provider transport and browser replay | The pack checks the real provider-facing message and error-mapping seams; exact-candidate hands-on acceptance and independent CHECK still required |
| Automatic recovery notice claiming a resize-and-retry occurred | These tests do not invent a retry receipt; implementation must not claim a retry it did not perform |
| User-facing documentation | Backend implementer must include the issue's attachment-limit documentation update; RED edits tests only |

## Execution receipts

Base: `303fc66bad3dc1b9b6c1d6f102e7e1fa09cfeef3` (fetched integration/l1-bundle3, local and origin equal).
Evidence directory: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/t4-1435
Runs: the installed shared slot runner, one anchored `-run`, one package at a time, with `CGO_ENABLED=0`, `goolm,stdjson`, `-p 1`, `-count=1`, verbose named results, and load <=20 before execution. No full-suite, GREEN or mutation claim belongs to this RED pack.

## Chief evidence amendment — planned before additional assertions

Source read: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/jarvis-attachments-20261023/REPORT.md (sections A/B/C/E), plus the chief's saved control message. The original six regression groups finished before the outage; their saved test hashes and raw exits were verified on resume, with no re-run. They are checkpointed in tests-only commit `b77a5c74d7f49c70e5d3f0a0a3ea6ab3c16223ce`.

The incident image is tool-generated, not user-uploaded. A full-page screenshot is **2560x15424**, so `ceil(2560/32) * ceil(15424/32) = 80 * 482 = 38,560`. This corroborates the original independent patch arithmetic. The original upload/cleanup regression is a separate conversation-lifetime counterexample; it is **not evidence that restart or cleanup caused the live same-second unknown ref**.

| New group | Real boundary and sequence | Expected from issue Expected and chief's A/B/C requirements |
|---|---|---|
| A: tool JPEG and PNG | Register valid 2560x15424 images; call the real `attachToolResultMedia` before history admission | One fully decodable image, <=30,000 patches, within the effective catalog's byte/edge ceilings; not silently dropped |
| B: old image in model history | Persist the old full-size tool image with a matching assistant tool call in the real session archive; fetch/present again in iterations 1 and 2 of one turn, drive the real downgrade feedback, then close/reopen the archive for iterations 1 and 2 of a fresh turn | Every provider-facing image fits; consuming a one-shot retry may never let the original oversized image escape again |
| C: relabel and replay | Seed the real per-turn successful-strip stamp; persist a later exact patch-limit error via the real error-transcript writer | Replay code is not `media_unsupported`; public persisted text names image size, never wrong format |

A and B use valid JPEG/PNG fixture bytes, not screenshots or private data copied from the live installation. B is a deterministic integration of real archive + history presentation + downgrade state; the full LLM/tool conductor and browser still need exact-candidate UAT. C exercises the actual persistence/relabel seam, not a test-side reimplementation of the classifier.

E remains **Unknown for the live same-second 404**. It was already within the original ref-availability scope, so the accepted-ref/restart and missing-ref user-message tests are retained. On this base, the thumbnail handler already reads the current store through `agentLoop.GetMediaStore()` before its legacy fallback; simply assuming a stale REST-store pointer would manufacture a defect. This pack makes no new causal or reproduction claim for E. The chief's report and that handler need separate investigation; no production or live-instance changes were made.

Additional CHECK mutants: restore the verbatim universal-image branch; reintroduce an unsafe old data URL when rebuilding iteration/turn history; let an earlier strip stamp override a later image-size verdict in the persisted transcript. GREEN and mutation execution remain **deferred to CHECK**.
