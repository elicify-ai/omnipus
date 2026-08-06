# ADR-052-C3 — macOS Chrome embedding & Apple notarization (ADR-052 Phase 3 sub-decision)

- **Status:** **Proposed — 2026-07-22.** Recommendation recorded; ratifies ADR-052 §2's deferral of the C3 choice to Phase 3. Awaits operator sign-off + the AC-1 audio-spike outcome (which adjusts, but does not block, the recommendation).
- **Deciders:** Daniel Piatkowski (operator); architect (this record).
- **Parent:** [ADR-052](ADR-052-native-cross-platform-browser-bundled-distribution.md) §2 (C3) + §5 Phase 3.
- **Evidence level:** 2 — codebase facts + Apple's published notarization/hardened-runtime requirements + the Chrome-for-Testing signing reality; tagged where empirical (audio spike pending).
- **Amends / relates to:** [ADR-047](ADR-047-live-browser-webrtc.md) §13.4 (macOS audio spike); [ADR-048](ADR-048-live-browser-capture-default-context.md) condition 3; the operator directive ("the browser must always be packed in").

---

## 1. Context

ADR-052 D2 promises a **bundled, integrity-verified Chrome as the guaranteed floor** on every OS. On macOS that promise collides with Apple's notarization model:

- **Chrome-for-Testing (CfT) is signed by Google** with Google's Developer ID. It is *not* notarized by Apple (CfT is a developer tool, not MAS-distributed), but it *is* ad-hoc/Developer-ID signed and runs under the hardened runtime.
- **Repackaging CfT inside an Omnipus `.app`** — copying the binary into our bundle — **breaks the signature** (the seal covers the original bundle layout). Apple's notarization then rejects the resulting `.app` because it contains an unsigned / non-hardened-runtime binary, and Gatekeeper will refuse to launch it on a default-configured Mac.
- The **existing `scripts/build-macos-app.sh`** builds the *Launcher* `.app`, performs **no** `codesign` / `xcrun notarytool`, and does not wrap the gateway or Chrome. So macOS delivery is from-scratch, not an extension.

ADR-052 §2 therefore **deferred** the macOS embedding choice to Phase 3 (C3), naming three options and explicitly accepting that "macOS may end up the one OS where Chrome is runtime-downloaded" (§8, downgraded confidence). This record makes the choice.

A second input — the **AC-1 audio spike** (ADR-047 §13.4: does `chrome.tabCapture` yield audio under `--headless` on darwin?) — adjusts the *value* of bundling full Chrome but does not change the signing constraints. If audio is **absent** (§13.4's expectation: headless macOS has no loopback audio device), live video is deferred regardless of bundling, but a guaranteed-correct Chrome for **browsing** still has standalone value.

## 2. Options considered

| | Option | Signing posture | "Always packed in"? | Cost / risk |
|---|---|---|---|---|
| **(i)** | Re-sign CfT with the Omnipus Developer ID + Hardened Runtime (+ `disable-library-validation` for CfT's nested dylibs) | We **strip Google's signature** and re-sign Chrome ourselves; notarize the whole `.app`. | ✅ inside the `.app` | **High ongoing maintenance** per Chrome bump; Chrome's own self-check / Widevine / integrity paths may trip on a re-signed binary; must hold a Developer ID + notarization creds; `disable-library-validation` weakens the hardened runtime. |
| **(ii)** | Ship CfT as a **sibling helper outside the `.app`** (Google-signed, untouched); sign+notarize only the gateway `.app` | Chrome **stays Google-signed** in its own location; we sign/notarize only our own `.app`. | ✅ **in the package**, beside the `.app` (not inside it) | **Cleanest signing**; weaker UX (not a single double-click `.app` that contains Chrome); installer must place the sibling at the runtime's `packageChromeRootCandidates` darwin slot. |
| **(iii)** | macOS = **runtime-download primary**; `.app` wraps only the gateway; Chrome fetched on first use (today's behavior) | Only the gateway `.app` is signed/notarized; no Chrome handling at all. | ❌ **No floor on macOS** — Chrome is downloaded at runtime | **Lowest signing cost**; violates the operator's "always packed in" directive for macOS; reintroduces the download-on-first-use failure modes ADR-052 D2 was created to eliminate. |

## 3. Decision

**Adopt option (ii): ship Chrome-for-Testing as a Google-signed sibling helper outside the `.app`, and sign/notarize only the Omnipus gateway `.app`.**

The installer lays the sibling Chrome at the darwin row of `packageChromeRootCandidates` so the runtime's existing multi-root probe finds it exactly as it does on Linux (slot resolution, SHA-256 verification, and `findPackageChrome` are OS-agnostic). The gateway `.app` itself is signed with the Omnipus Developer ID, notarized via `xcrun notarytool`, and stapled — no Chrome binary is touched, so Google's signature and Chrome's own integrity/self-check paths remain intact.

**Why (ii) over (i):** re-signing Chrome (i) buys a single-`.app` UX at the cost of per-bump maintenance, a `disable-library-validation` runtime weakening, and real risk of tripping Chrome's self-check — for a UX nicety. The operator directive is *"the browser must always be packed in"*, not *"inside the `.app`"*; (ii) honours the directive without the signing risk.

**Why (ii) over (iii):** (iii) re-introduces the exact failure class (missing/wrong/download-failed Chrome) ADR-052 D2 exists to prevent. macOS should not be the one OS where the floor disappears unless signing makes bundling truly impossible — and (ii) shows it does not.

**Interaction with the audio spike (AC-1):** the choice is **robust either way.**
- If AUDIO-WORKS → bundled full Chrome + verified audio → AC-4 relaxes the darwin gate. (ii) already ships full Chrome; no change.
- If AUDIO-ABSENT → browsing-only, video deferred (AC-5). (ii) still delivers a guaranteed-correct Chrome for browsing + the JPEG live-view fallback; video was never on the table for macOS regardless of bundling.

So the C3 decision does **not** wait on the spike — only the AC-4-vs-AC-5 *video* outcome does.

**Confidence: Medium-High.** Signing constraints are well-grounded (Apple notarization + CfT signing are documented facts). Residual risk: Apple notarization occasionally rejects gateways that bundle certain helper layouts; the gateway `.app` (no Chrome inside) is the lowest-rejection shape, but final confirmation needs a real `xcrun notarytool` run on macOS (AC-6/AC-10).

## 4. Consequences

**Positive**
- Honours the operator's "always packed in" directive on macOS (floor preserved via the sibling).
- Cleanest signing path: Google's Chrome signature is never touched; Chrome's self-check / Widevine / integrity paths stay intact.
- Robust to the audio-spike outcome — no rework if video is deferred.
- Reuses the OS-agnostic Phase-1 machinery (`packageChromeRootCandidates`, `chromeintegrity`, `findPackageChrome`) unchanged.

**Negative**
- macOS install is not a single double-click `.app` that contains Chrome — the Chrome sibling sits beside the `.app` (weaker UX than Linux, parity with option (ii)'s documented trade-off).
- The operator must hold an Apple Developer ID + notarization credentials and run `codesign`/`notarytool` in the release pipeline (new infra vs Linux).
- `.app` notarization can still be rejected by Apple for reasons unrelated to Chrome (entitlements, bundled helpers) — needs a macOS release-step verification.

**Neutral**
- Option (i) remains a documented future upgrade if a single-`.app` UX later justifies the re-signing cost and a successful audio spike makes bundled full-Chrome video worth it.

## 5. Sequencing (within Phase 3)

1. **Now (Linux-buildable):** extend `cft-bundle.sh` for `mac-arm64`/`mac-x64`; extend `packageChromeRootCandidates` + `install.sh` for the darwin sibling layout; this ADR lands.
2. **macOS runner:** sign+notarize the gateway `.app`; place the Google-signed Chrome sibling; run the audio spike (AC-1).
3. **Gate:** flip AC-4 or AC-5 per the spike; final notarized-package validation (AC-10).

## 6. Open items

- **Developer ID + notarization credentials** must be provisioned for the release pipeline (operator/infra).
- **`mac-x64` (Intel Mac)** — confirm whether Phase 3 ships arm64-only or both (ADR-052 currently defers darwin/amd64).
- **Entitlements set** for the gateway `.app` (network, camera/mic only if AC-4 video lands; otherwise minimal) — to be pinned at signing time.
- **`/grill-spec` this ADR** before sign-off (per operator workflow) — particularly the (i)-vs-(ii) maintenance-cost claim and the "Chrome self-check trips on re-signing" risk.
