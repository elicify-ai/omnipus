# Generic environment setup — verification record

Date: 2026-09-18. Scope: the founder-approved generic Bash installation tool and replacement of the superseded document-specific installation route.

Status: implementation and required verification complete within the platform boundaries recorded below. No commit, push, CI or release is claimed.

## Delivered behavior

The agent supplies installation commands or scripts. Existing global and per-agent Ask permissions govern the existing approval dialog, whose installer summary shows the command, purpose, scope and destination. Workspace installation is the default; shared installation is explicit. Background sessions use existing run/poll/read/kill controls. Results report actual exit/publication outcomes and require the agent to verify installed dependencies in its ordinary environment.

No library catalogue, package-specific recipe engine, Admin dependency handoff, separate approval workflow or detached-process supervisor remains in this tool. Reusable rendering, image reading, sandbox, storage and session facilities remain. The global upfront tool set and intentionally non-deniable discovery behavior are unchanged.

## Requirement evidence

Evidence is retained in [the delivery artifact directory](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/.local/adr090/environment-setup-delivery).

| Requirement | Evidence and scope |
|---|---|
| Ask and deferred discovery | Registered S1/S2 and fresh F1/F2/F3 record discovery, approved execution and denial without installation. Browser R1/R2 exercise the existing authenticated dialog. |
| Custom agents without delegation edges | Fresh F1 installs and uses a real package; integrated custom run `3189892863` installs python-docx and generates, renders and reads a document. |
| Workspace/shared destinations and authority | Registered S4/S6, final Linux L1/L2/L3 and browser R3/R4 exercise selected destinations and rejected unauthorized access. |
| Failure and isolation | Real-child boundary checks, lifecycle tests and registered S5/S6 demonstrate denied side effects, cancellation, failure reporting and preserved shared installations. |
| Reuse and concurrency | Storage locking tests and S6 cover additive shared installations, stable absolute prefixes, earlier tools remaining usable and same-target locking. |
| Office workflow | Custom and Mia chains pass in `3189892863`; actual attended Mia-to-worker delegation passes in `967567453`, including recorded outgoing image data. Each installs real python-docx from staged wheels through Ask, then records ordinary execution importing it from the approved prefix. Prepared native rendering dependencies remain explicit prerequisites. |
| Honest limitations | Real read/tool-policy denial, provider image rejection, readiness-error tests, actual missing-rasterizer execution and live missing-glyph inspection demonstrate explicit failure/incomplete validation. |
| Two existing policy levels | Focused configuration, coreagent and tools tests cover the global/per-agent Ask/Deny matrix, custom defaults, saved overrides and existing policy resolution. No new permission resolver was added. |
| Background lifecycle | Registered S1/S5, fresh F1/F3 and focused lifecycle tests cover start/status/read/cancel, session ownership, no repeated installation and truthful terminal results. |
| Admin cross-workspace access | Registered S4 proves target-member use. Browser R3 `20260918-141714` uses the normal standalone Admin chat, displays the target workspace name and runs the installed executable there. |
| Unattended Ask denial | Registered S3 and worker scheduled run `946443508` reject installation. The successful worker case uses attended delegation instead. |
| Generic dependencies | Fresh single-line and multiline package-manager commands and custom scripts install dependencies unknown to the application catalogue. No package-specific source change was needed. |

The complete registered acceptance run is `2228212392`; fresh-package acceptance is `3415285064`, with the corrected Mia routing/label check in `1909144494`. Scripted providers and fixture-user approvals are not presented as live-model planning or founder approval.

The final integrated worker run records the actual next provider request's image data for `worker-edge-read`. The parent independently decoded its 10,334 PNG bytes (SHA-256 `3549f9a5fc2e4e76aeb5530fc3f2518aaa5ef5ad96784b315fbd6036585cf8d7`) and verified pixel equality with the final generated page. An earlier empty-media result was a recorder omission, not a production transport failure; the failed artifact is preserved.

## Visual and document checks

Real-model correction runs cover Word, Excel, PowerPoint and PDF for custom, Mia and General Purpose agents. The parent inspected all twelve final images. A clipped worker spreadsheet was rejected and corrected in a targeted follow-up; failed runs remain retained. The final-image delivery map compares decoded pixels to the images actually sent to the provider, accounting for PNG re-encoding. Models also had access to generation scripts, so this is not a pixels-only diagnosis claim.

Sandboxed edge fixtures cover two-page Word/PDF documents, two worksheets with a preserved formula and recalculated result 42, two slides, and Latin non-ASCII text. Parent inspection caught and corrected spreadsheet clipping and a PDF font problem. Japanese glyphs absent from one font fixture are retained as a negative case: the real model explicitly leaves their visual validation incomplete despite their presence in the PDF text layer.

## Verification quality and corrections

Three deliberate source mutations were each detected: bypassing Ask denial, bypassing cross-workspace authority and hiding a nonzero installer exit. Each source was restored byte-for-byte, its restored check passed, and the parent independently verified the original hashes.

The final installer test group passes after corrections to inherited write grants, source reads, runtime path reuse, nonzero/publication reporting and truncated output. Storage/runtime checks pass; the final non-root Linux Landlock run is `20260918-134520`. Independent installer review hashes still match current source.

Admin chat required a normal UI entry point and connection/replay corrections. The final focused UI suite passes 47 tests, typechecking passes, and browser R3 verifies actual usability. Independent review caught and corrected an intermediate duplicate-attach race.

## Boundaries and deferred work

- macOS sandbox installation and document workflows are executed; Linux installer confinement is executed. Native Windows execution and Linux Office rendering are not certified by these runs.
- A prepared native renderer is a declared prerequisite, not proof of fresh installation of every native dependency. Fresh authoring-package installation is separately demonstrated. Arbitrary libraries and fonts are not universally certified.
- Deliberately detached children retain Bash's cleanup limitations. No stronger process-supervision guarantee is claimed.
- Unrelated deferred issue #731 remains outside this work. A separately discovered channel-session restart-index mismatch is documented in the implementation status and was not changed.
- Merge/release review gates remain separate; no merge or release is performed by this delivery.
