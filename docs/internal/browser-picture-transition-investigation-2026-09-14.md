# Latest user picture-transition failure

## Timeline

Amsterdam, deployed runtime source `7767f749933455c85fc0bee10f8bb963ca2a1e20`; investigation on `browser-improvements`. All times UTC on 2026-09-14.

- 10:34:41: Mia attached chat session `session_01M2F8FXJ61CGWEYMXAED5VF2H`.
- 10:34:48: Chromium launched for the live browser.
- 10:34:50–53: initial video transitions recovered.
- 10:35:16: page transition began; latest recorded key release completed at 10:35:17.
- 10:35:31: `live view document refresh failed`, error `new document did not provide a confirmed picture in time`.
- 10:37:31: another transition began.
- 10:37:46: the same fifteen-second watchdog error recurred.

## Confirmed cause of input being blocked

The page-transition check did not finish, so input remained disabled. The wording suggests missing video, but this exact watchdog runs before completion of the document-transition token, which in turn precedes requesting a refreshed capture. These logs do not establish a WebRTC input failure or delayed encoding as the cause.

All 100 detailed input records in the captured session window completed. Chrome dispatch duration had median 1.4668 ms and maximum 17.4578 ms. These records are below the 100 ms pressure threshold and do not support blaming the new video adaptation. They do not measure the user's total input-to-picture latency.

## Root cause still unproven

The current logs do not record the navigation identities or the paint-check stage needed to choose between: a missing/rejected document commit; a paint/geometry check that does not complete; or a canceled/same-document navigation without the expected completion event. Existing recovery handles certain failures after a commit has been accepted, but cannot repair an unaccepted commit through that path. The watchdog reports the timeout without completing recovery.

The preceding successful bounded stress tests did not reproduce this transition. Read-only coverage review found stale-commit, cancellation and accepted-commit recovery tests, but no explicit redirect or same-document navigation cases. This is a verification gap, not evidence that the user's error is harmless.

## Evidence and next reproduction

Raw evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/user-picture-1037-persistent.log` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/user-picture-1037-fly.log`.

Implementation reviewed: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/live_document_frame.go` (`watchDeadline`, `onEvent`, `paintAndCaptureDocument`, `reconcileDocument`).

The user was asked for the website and immediately preceding action. Reproduce that transition and capture its frame/document/request identity sequence and paint stage before choosing a fix. Do not merely extend the timeout or remove the input guard. No runtime code, deployment, restart, or live-page navigation was changed during this investigation.

## Landing-page reproduction after user clarification

The user identified `http://localhost:5000/browser-start` as the visible page. It is static HTML with no page-navigation JavaScript. Its autofocus search field submits a native GET form to DuckDuckGo when Enter or Search is activated. This can start a navigation without replacing the visible landing page.

A separate Chromium process on Amsterdam exercised the actual landing page, without navigating the user's production tab:

- 11:09:23 UTC: landing-page HTTP 200; start, request and committed document identities matched.
- 11:09:25–42: typing-only phase retained the landing page, complete ready state, and exact typed value for seventeen seconds. No navigation event occurred.
- 11:09:42.831: the diagnostic submitted Enter.
- 11:09:42.839–49: Chrome emitted navigation-start and document-request events for the DuckDuckGo search with matching identities.
- No response, commit or cancellation event arrived before the trace ended. After eighteen seconds, a page-state query was attempted and itself reached its ten-second deadline. The final URL is therefore unverified; do not invent a successful page-state read.

The independent connectivity check from the same machine found DuckDuckGo IPv4 TCP connection timeout after five seconds, an unsuccessful IPv6 connection, and successful HTTPS HTTP 200 from `https://example.com/` in about 96 ms. DNS completed quickly in the first DuckDuckGo probe; TLS was never reached. This localizes that reproduction to destination-specific connectivity, not general loss of internet access, and does not prove which network operator caused it.

The native trace is a characterization, not a passing Omnipus regression test. It demonstrates a pending search request consistent with the user's watchdog failure, without a document-identity mismatch. The user's exact triggering key remains unlogged and has not yet been confirmed. Pure typing did not reproduce the error.

The application issue is now concrete: the fifteen-second picture watchdog starts during provisional navigation, before any new document has committed. A pending network request is incorrectly labeled as failure to produce a picture. The correct direction is to distinguish navigation waiting from failed painting and preserve an explicit cancellation/recovery path; restoring input must still require proof of the current visible page. Do not bypass frame identity, replay old actions, or merely increase the picture timeout. Search-provider reachability is a separate issue.

Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/start-page-native-trace.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/start-page-search-network.log`, and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/start-page-network-comparison.log`.

No production code or configuration was changed by these diagnostics. The isolated test Chrome was stopped after the trace; the user's tab was left untouched.

## Approved correction and verification plan

The user requested immediate correction. Keep pending navigation distinct from failed painting: a slow request can continue, but Stop loading must remain available. Starting a paint check after commit receives its own fifteen-second bound. A previous phase timer must not fail the new phase. Stopping first cancels loading, then verifies the actual current document, geometry and refreshed media; it does not authorize a stale picture or replay input. Newer navigation wins over an older cancellation result.

The correction adds `stop_loading` to the existing navigation-command path, with no change to gesture transport. It interrupts older queued navigation and uses ordinary control admission and held-input release. The landing search and omnibox both use Google, whose actual search endpoint responded HTTP 200 from Amsterdam in89ms; this is an explicit provider change, not automatic query fallback.

Expected behavior derives from the user report and the safety requirements above. Focused tests cover provisional waiting without a false picture error; late commit with a full paint budget; stale timers; Stop cancellation before paint/media readiness; superseding navigation; immediate queue cancellation; command errors; refused frontend control; exact Unicode search encoding and preserved address parsing. Mock only Chrome protocol/network and clock edges; the navigation state machine, command queue and frontend routing remain real. Deliberate faults must be caught and restored before final focused tests.

Live verification must exercise the actual start-page search and an intentionally unreachable destination, using Stop to return to usable input. Retain failed attempts and compare post-fix behavior with the reproduced pending-request trace. Full CI is not required for each fix; main, tool-policy runtime and Amsterdam placement remain outside this change.

## Implementation and review

The pending-navigation timer now logs the wait without emitting a persistent failure. The existing waiting-picture banner explains Stop loading. Once Chrome commits a document, the separate bounded paint phase begins; phase cancellation prevents an obsolete timer from failing that phase. Review caught and removed a proposed loading notice sent through the persistent error channel, because it would survive a later successful recovery.

Stop is a navigation command with immediate queue interruption. Its successful Chrome cancellation is followed by a fresh frame-tree read, current-document checks, paint and geometry confirmation, and recapture. Input remains blocked until the refreshed media boundary is confirmed. Both contract schema copies and their generated consumers include the new command; unknown commands remain invalid.

Frontend validation passed 87 focused tests and lint, with three deliberate faults independently caught. URL resolution passed 25 tests and lint; wrong provider, removed escaping and whitespace corruption were caught by mutations. The first backend focused run passed. Final backend mutation/gateway checks, candidate build and live results are recorded below when completed.
