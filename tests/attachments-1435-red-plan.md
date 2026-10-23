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
