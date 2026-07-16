# Grill-Spec Review — Live-Browser Video Streaming (Revision R4)

- **Spec under review:** `docs/internal/specs/live-browser-video-streaming-spec.md` (Revision **R4**, post Gate-0 CI run, 2026-07-16).
- **Mode:** `plan-spec` (BDD scenarios, FR-/SC- IDs, traceability matrix present).
- **Reviewer stance:** adversarial, read-only. Prior rounds: R1 review (`-review.md`), R2 review (`-review-round2.md`). This is the round-3 grill (of the R3→R4 spec).
- **Verification basis:** code-cited against the live tree (`pkg/tools/browser/`, `pkg/gateway/`, `src/lib/`, `contracts/`, `go.mod`, chromedp v0.15.1 module source) and ADR-044.

---

## Executive Summary

R4 is a mature spec that has already closed most of its early defects; the structure, traceability, and test plan are largely sound. However, adversarial code-verification surfaces **one CRITICAL infeasibility at the heart of the C-3 "closed" security item** and **five MAJOR issues**, several of which are *factual contradictions with the codebase the spec claims to have verified*. The most serious: the CDP-transport change that the whole EC-3 / SC-017 token-confidentiality posture rests on has **no implementable transport** as written (chromedp cannot speak `--remote-debugging-pipe`; the `--remote-debugging-port=0` fallback is kernel-blocked by the existing Landlock connect-port allow-list). Separately, the spec repeatedly asserts the `browser_screencast` wire frame is "dead / no runtime path consumed it," but it is in fact the **current, sole live-view transport** end-to-end.

- **Findings:** 1 CRITICAL, 5 MAJOR, 4 MINOR, 1 OBSERVATION.
- **Verdict: BLOCK** (one CRITICAL).

---

## Findings

| ID | Sev | Lens | Section | Finding | Recommended fix |
|----|-----|------|---------|---------|-----------------|
| **F-01** | CRITICAL | Infeasibility / Incompleteness | C-3, FR-013, EC-3, SC-017, `browserDebugPort` row | The C-3 fix (replace fixed port 9223 with `--remote-debugging-pipe` target / `--remote-debugging-port=0` fallback) has **no implementable transport** with the pinned stack. chromedp v0.15.1 has **no pipe transport** — its allocators read a `ws://` URL from Chrome's stdout (`allocate.go:236-250`, `readOutput`→`wsURL`; the only `pipe` token in the module is a "broken pipe" error string). The ephemeral-port **fallback is kernel-blocked**: `pkg/gateway/sandbox_apply.go:419` allow-lists **only** the fixed `browser.DebugPort` (9223) in `ConnectPortRules`, and the in-code comment (`sandbox_apply.go:411-419`, `manager.go:32-40`) documents the exact failure — a random port yields `dial tcp 127.0.0.1:<random>: connect: permission denied` (Landlock EACCES). EC-3 is marked "✅ PASS (by design)" without validating that either transport is buildable. So the P0 token-confidentiality requirement (SC-017) has an **infeasible enforcement mechanism**, and the CRIT the spec claims closed (C-3) is not actually closable as written. | Pick one and scope it explicitly: (a) keep 9223 fixed and close C-3 a different way (network-namespace the browser per ADR §6.6, or bind CDP to a UNIX socket if chromedp can be pointed at one); (b) if `--remote-debugging-pipe` is the target, **scope the custom CDP-over-pipe client** (chromedp cannot consume it — this is a real, unbudgeted work item that touches Constraint #1) and prove it in Gate 0, not "by design"; (c) if ephemeral port, **first** amend `sandbox_apply.go` to read the actual port from `DevToolsActivePort` and inject it into `ConnectPortRules`/`bindPorts` at policy-build time — and note this couples sandbox-policy build to the browser launch ordering. Re-open EC-3 as an *empirical* pre-build gate, not a design assertion. |
| **F-02** | MAJOR | Inconsistency / Incorrectness | M-10, O-2, Impact Assessment, Non-Behaviors, `BrowserScreencastFrame` row | The spec asserts in ≥5 places that `browser_screencast` is a **"dead JPEG wire frame… no runtime path selected/consumed it"** and rates its removal **LOW risk**. This is false. It is the **current, sole live-view transport**: Go emits it per frame in the attach callback (`pkg/gateway/browser_ws.go:546`, `wc.sendFrameGen(generated.BrowserScreencastFrame{…})`), the SPA consumes it (`src/lib/browserLiveWs.ts:147` → `onScreencast`), and the spec's own Overview says the feature **replaces "the live browser panel's CDP JPEG-per-frame screencast."** A frame cannot be both "what we are replacing" and "dead with no runtime path." Consequence: removing it is the cutover of the only live-view wire path, not a costless cleanup — and it **eliminates working live view on every non-video-capable install** (headless-shell / no-Xvfb / non-Linux), which today render fine via this JPEG screencast (screencast constants are "spike-proven on chrome-headless-shell", `live.go:17-23`). The M-1 prose admits the withheld-experience cost, but the "dead surface" framing directly contradicts it and mis-rates the blast radius. | Correct the characterization: `browser_screencast` is **live**, not dead. Re-rate the removal from LOW to at least MEDIUM and tie it to the SPA cutover. State explicitly that non-video-capable installs **lose today's working JPEG live view** and land on the unavailable state (this is the true M-1 cost, not a footnote). Add a regression note that the removal must not precede the video path being reachable on video-capable installs. |
| **F-03** | MAJOR | Incompleteness / Inconsistency | `managedExecAllocatorOpts` row, C-3, Impact Assessment | Removing the fixed port breaks the **ADR-043 shared-Chrome coordinator**, which the spec never mentions. `coordinator.go:52` `sharedChromeCDPURL()` hardcodes `ws://127.0.0.1:9223`; ownership detection (`coordinator.go:44-48`) proves "a 9223 holder is OUR Chrome" by the fixed-port holder + marker; `checkDebugPortAvailable` (`manager.go`) preflights the fixed port. A pipe/ephemeral transport invalidates the dial URL **and** the ownership heuristic. The spec's `managedExecAllocatorOpts` row calls this the "highest-risk edit" but scopes it to headful/display/capture flags — it omits the coordinator, `sharedChromeCDPURL`, and ownership-marker dependencies entirely. | Extend the C-3 impact analysis to the coordinator: enumerate `sharedChromeCDPURL()`, the ownership-marker detection, and `checkDebugPortAvailable` as symbols that must change together. Decide how ownership is proven when the port is no longer fixed (e.g. marker-file only, independent of port). This is a same-wave dependency, not a follow-up. |
| **F-04** | MAJOR | Incorrectness / Infeasibility | EC-1, SC-003, R4 Gate-0 table, Overview | EC-1's "**PASS — 30 fps**" was measured on `ci-omnipus` (**16 GB / 8-core**), but the deployment min-spec (SC-016) is **undocumented** and the actual target pods can be far smaller (this project's own devpods run 2–4 cores / 3.8–15 GB per CLAUDE.md). The pipeline is **SwiftShader software rendering** + per-frame `createImageBitmap` (JPEG decode) + `VideoFrame` + software `VideoEncoder` at 720p30 — CPU-bound work whose throughput is highly host-dependent. The gate that the spec says "sets SC-002/003/013 to real numbers" was therefore measured on **non-representative hardware**, so its authority over the *shipped* fps floor is unproven. EC-1 "PASS" and the min-spec (SC-016, still undocumented) are entangled but treated independently. | Re-run / additionally run EC-1 on hardware at the intended **min video-capable spec**, and publish SC-016's min-spec numbers *before* asserting EC-1 clears for the shipping configuration. If 30 fps holds only on an 8-core box, SC-016's min-spec must encode that, and installs below it must classify not-video-capable (which the spec already provides for — but the threshold is currently empty). |
| **F-05** | MAJOR | Incompleteness / Inconsistency (self) | Gate 0 / EC-4, FR-006, §Status, Handoff | EC-4 (iPad H.264-main/VP8 decode) is **deferred to a post-build ship gate**, yet it determines FR-006's codec priority ("the Gate-0 iPad result **may invert** this to VP8-first") **and** whether the feature ships at all (neither decodes ⇒ NFR-1 fails ⇒ re-open ADR-044). The codec choice ripples into the contract (`video_caps`), encoder config, GOP sizing, and the negotiation matrix (DS-1). Building the whole epic on an unverified primary-device decode **repeats the exact C-1 anti-pattern** the spec congratulates itself for fixing ("a hard gate sequenced *after* the architecture it justifies"). If EC-4 fails or inverts, contract + encoder + tests churn. | Either run EC-4 before `/taskify` (it is a device probe, cheap), or explicitly sequence the epic so the codec-dependent surfaces (contract `video_caps`, encoder config, DS-1) are the **last** built and gated on EC-4, and state the rework cost if EC-4 inverts. Do not let "does not block taskify" hide that a ship-blocking, design-shaping gate is unrun. |
| **F-06** | MAJOR | Ambiguity / Incorrectness | FR-013, DS-4, US-9/AC-2, M-5 | The ingest-token model ("**single active holder**; a reconnect with the same token **supersedes** the prior holder; a **concurrent** duplicate with the same token is **rejected**") gives no mechanism to *distinguish* a legitimate reconnect from a concurrent duplicate. Both are "a connection presenting a valid, correctly-scoped token while a holder may still exist." DS-4 lists them as two rows with opposite outcomes (accept-supersede vs reject) but no discriminator. Operationally this is a race: after a transient drop the gateway may not yet have observed the old holder's close, so a legitimate reconnect is indistinguishable from an attacker replaying the still-live token. "Supersede always" contradicts "reject concurrent duplicate"; "reject if a holder exists" breaks reconnect. | Specify the discriminator precisely. Options: a monotonic connection epoch/nonce minted per ingest connect (reconnect carries a higher epoch and supersedes; a lower/equal epoch is rejected), or a bounded takeover grace after the prior holder's observed close. Define the exact accept/reject decision when two live connections hold the same valid token simultaneously, and add it to DS-4 and Test 29. |
| **F-07** | MINOR | Incompleteness | AW-4, SC-001a, SC-002, SC-016, Handoff | The spec's own precondition — "replace 3 s / 150 ms placeholders with Gate-0 measurements **before `/taskify`**" (AW-4) and "publish the min video-capable spec **before `/taskify` completes**" (SC-016) — is **unmet**. Gate 0 measured only fps (30); SC-001a still says "≤ 3 s placeholder," SC-002 still "150 ms placeholder," SC-016's min-spec numbers are absent. Yet the Handoff routes straight to `/taskify`. | Before `/taskify`, either fill these from Gate-0 data (cold-start, glass-to-glass, footprint/min-spec) or explicitly re-scope them as first-wave measurement tasks with named owners. Don't hand off with the spec's own gate unsatisfied. |
| **F-08** | MINOR | Incompleteness | `EnsureChromium`/`cftDownloadID` row, FR-009, US-8 | "Make the full Chrome build the default… detect either binary" understates the installer change. `cftDownloadID` (`installer.go:23`) is a single const driving the manifest download key, zip path, extraction subdir, **and** binary name (`headlessShellBinaryName()`). Full Chrome-for-Testing uses a different download ID (`chrome`), a different binary name (`chrome`, not `chrome-headless-shell`), and a different layout. The installer must handle **both** builds simultaneously (full Chrome default + headless-shell fallback for non-capable installs), each with its own integrity check. | Spell out the dual-download design: two download IDs, two binary-name resolvers, two layouts, integrity-verify for each, and the classification logic that picks between them. Add a dataset row for "full-Chrome present but headless-shell also cached" if both can coexist. |
| **F-09** | MINOR | Ambiguity (doc consistency) | Overview, FR-001/016, Integration Boundaries, US-9/AC-3 | R4 renamed the "capture page (`getDisplayMedia`)" to an "encoder page" fed by `Page.startScreencast`, and the R4 note asks the reader to mentally patch older phrasing ("where older R3 phrasing says… read: mechanism (b)"). The propagation is incomplete: Integration Boundaries → Managed Chrome still says "capture-page load"; US-9/AC-3 and the BDD "Agent-browsed page cannot capture" still center `getDisplayMedia` (now only the **audio** surface). A spec that requires the reader to hold a patch table in their head will be mis-implemented. | Do a global pass replacing "capture page" with "encoder page" and scoping every `getDisplayMedia` reference to audio-only, so the shipped mechanism (b) reads consistently without the R4 patch note. |
| **F-10** | MINOR | Inoperability | FR-019, SC-012, O-4 | SC-012 / Test 28 verify metric **emission** only. FR-019's alert thresholds and runbook pointer are deferred ("documented when the metrics land") with **no acceptance criterion** — so the 3 AM runbook the observability lens exists for is unenforced. | Add an SC that the alert thresholds + one-line runbook are present and reviewed at metrics-land time, or fold them into SC-012's definition of done. |
| **F-11** | OBSERVATION | Overcomplexity | Overview, FR-001 | Mechanism (b) adds a full **Chrome → gateway → Chrome → gateway → viewer** loop: the gateway pulls JPEG via CDP `Page.startScreencast`, ships each frame over loopback WS to an encoder page **in the same Chrome**, receives encoded chunks back over loopback WS, then relays. The extra JPEG loopback hop (~2.4 MB/s) is acknowledged but alternatives (CDP target-to-target delivery; a co-located encoder that reads screencast without the double gateway hop; keeping Go purely as the viewer-side relay) are not weighed. Defensible under the pure-Go constraint, but the round-trip is under-justified. | Note it as an accepted-cost trade with a one-line rationale for why the double hop beats the alternatives, so implementers don't "optimize" it away and break isolation. |

---

## Structural Integrity Results (plan-spec mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | PASS | US-1..US-11 each have ACs. |
| Every acceptance scenario has ≥1 BDD scenario | PASS | 34 scenarios + 3 outlines cover the ACs. |
| Every BDD scenario has a `Traces to:` back-reference | PASS | All sampled scenarios carry `Traces to:`. |
| Every BDD scenario has a corresponding TDD test | PARTIAL | Mostly yes; FR-019/observability and audit are covered by Tests 28/32. Fine. |
| Every FR appears in the traceability matrix | PASS | FR-001..FR-024 all present. |
| Every BDD scenario appears in the matrix | PARTIAL | Matrix maps FR→scenario→test; some scenarios (e.g. kill-switch teardown) are reachable only via FR-020's row. Acceptable. |
| Test datasets cover boundary/edge/error | PASS | DS-1..DS-5 include max u32/u64, empty payload, oversize keyframe, platform matrix. |
| Regression impact explicitly addressed | PARTIAL | Strong regression section, **but** it inherits F-02's error: it treats `browser_screencast` removal as safe and does not flag the loss of live view on non-video-capable installs. |
| Success criteria measurable, no subjective language | PARTIAL | SC-001a/SC-002 remain **placeholders** (F-07); SC-016 min-spec numbers absent. |

---

## Test Coverage Assessment

- **Negative/security coverage is strong**: Tests 1/2/27/29/30/32 cover unauth ingest, token scoping, pre-replay authz, reconnect, CDP-token, audit.
- **Gap (F-06):** Test 29 asserts "concurrent duplicate rejected" and "reconnect supersedes" but the spec never defines the discriminator, so the test cannot be written deterministically without inventing the epoch/nonce mechanism.
- **Gap (F-01):** Test 30 (`TokenNotRecoverableFromCDP`) presumes a pipe/ephemeral transport that is currently unbuildable with chromedp — the test's precondition is itself unproven.
- **Gap (F-04):** Test 23 (Gate-0 fps E2E) has no min-spec variant; it validates fps only on the 16 GB/8-core CI box.
- **Concurrency:** slow-viewer isolation (Test 6), aggregate LRU (Test 5) covered. The token-holder race (F-06) is the missing concurrency test.
- **Regression:** headful-equivalence corpus (Test 16) is concrete and reproducible — good. Missing: an explicit regression that non-video-capable installs *keep browsing* AND lose live view gracefully after `browser_screencast` removal (ties to F-02).

---

## STRIDE Threat Summary

| Component | Threats considered by spec | Residual gap |
|---|---|---|
| Ingest endpoint (loopback WS) | Spoofing (token), Tampering (bound+reject), DoS (per-viewer drop, max-message bound), Repudiation (audit FR-024) | **Token-holder race (F-06)** — replay window during reconnect; discriminator undefined. |
| CDP transport | Info-disclosure (token via `/json`) → pipe/ephemeral | **F-01: enforcement mechanism infeasible/kernel-blocked** — EC-3 "PASS by design" is unvalidated. |
| Encoder page consent | Elevation (agent page grabbing media) → CDP origin-scoped; global fake-ui forbidden | Structurally sound for video (mechanism b); audio grant still origin-scoped — OK, but doc drift (F-09) muddies which surface is guarded. |
| Managed Chrome (headful switch) | Info-disclosure/behavior drift → equivalence corpus | Coordinator/ownership dependency on fixed port unaddressed (F-03). |
| Viewer WS | AuthN (AuthFrame), pre-replay authz (FR-015/Test 27) | Covered. |

---

## Unasked Questions

1. **How does chromedp obtain the CDP endpoint once the port is not fixed?** The spec names `--remote-debugging-pipe` and `--remote-debugging-port=0` but never says how the ws-only chromedp allocator is fed the resulting endpoint (or replaced). This is the crux of F-01 and is simply absent.
2. **What proves "our Chrome" once 9223 is gone?** ADR-043 ownership detection keys on the fixed-port holder. The spec is silent (F-03).
3. **What is the min video-capable spec?** SC-016 is a "ship gate" with no number, and EC-1's 30 fps was measured on hardware larger than the likely target (F-04).
4. **What happens to live view on the millions-of-installs case of headless-shell / non-Linux** the day `browser_screencast` is removed? The spec calls the frame "dead"; it is the only thing rendering their live view today (F-02).
5. **Which connection wins when two hold the same valid ingest token at the same instant?** Undefined (F-06).
6. **Were cold-start and glass-to-glass actually measured at Gate 0?** The R4 note says fps was; SC-001a/SC-002 still read as placeholders (F-07).

---

## Verdict

**BLOCK** — 1 CRITICAL (F-01), 5 MAJOR (F-02..F-06).

The CRITICAL and the two codebase-contradiction MAJORs (F-02 `browser_screencast` is live, F-03 coordinator/ownership) are the priority: they are not judgment calls, they are verifiable facts the spec gets wrong about the system it modifies, and they survived an accepted ADR plus three grill rounds.

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/live-browser-video-streaming-spec.md docs/internal/specs/live-browser-video-streaming-spec-review-round3.md
```
