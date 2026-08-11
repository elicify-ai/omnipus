# ADR-052 Phase 3 — macOS + Audio: Goal & Acceptance Criteria

- **Status:** Proposed (goal/charter — precursor to the C3 ADR + the Phase 3 plan-spec).
- **Parent:** [ADR-052](ADR-052-native-cross-platform-browser-bundled-distribution.md) §5, row 3.
- **Drives grill items:** C3 (macOS `.app` Chrome-embedding / notarization sub-decision), M3 (macOS stays not-capable for video until audio is proven), M4 (macOS launchd slice of the cross-cutting service-registration thread).
- **Amends / relates to:** [ADR-047](ADR-047-live-browser-webrtc.md) §13.4 (the macOS audio spike), §13 (platform support); [ADR-048](ADR-048-live-browser-capture-default-context.md) condition 3 ("must not advertise Capable when capture cannot succeed"); [ADR-044](ADR-044-live-browser-video-streaming.md) §6.0.1 item 4 (the Linux-only rationale this retires per-OS).

---

## 1. Goal (one line)

Deliver native **darwin/arm64** Omnipus with a working **bundled Chrome**, and decide — empirically and per an ADR — whether macOS gets **live video+audio** or ships as **browsing-only (video deferred)**, plus the macOS kernel-sandbox and launchd service plumbing that ADR-052's cross-cutting threads require.

## 2. Why (context)

ADR-052 Phases 1–2 proved on Linux that a packaged install runs the agent on a **bundled, integrity-verified Chrome with zero downloads**. Phase 3 extends that to macOS and resolves the one genuinely open empirical question ADR-047 §13.4 left dangling: **does `chrome.tabCapture` yield real audio under `--headless` on darwin?** The linux-only video gate (M3/M6) may **only** relax for an OS after that OS proves audio-with-video — so the spike is the gate for everything video on macOS.

Two macOS-specific blockers make this its own phase (not a config flip):

- **C3 — Apple notarization.** Chrome-for-Testing is signed by Google; repackaging it inside an Omnipus `.app` breaks that signature, and Apple notarization rejects unsigned / non-hardened-runtime binaries. The existing `scripts/build-macos-app.sh` does **no** `codesign`/`xcrun notarytool` and builds the *Launcher* `.app`, not the gateway — so the macOS delivery is from-scratch.
- **No darwin sandbox backend.** ADR-052 §5 cross-cutting thread: *"Seatbelt/sandbox-exec needs real implementation + testing — no kernel-sandbox backend exists for darwin today beyond app-level."*

## 3. The gating empirical question

> **On darwin/arm64, under `--headless`, does a managed full-Chrome `chrome.tabCapture` produce a non-silent audio track?**

- **If YES** → macOS video is achievable; relax the per-OS gate for darwin (AC-4).
- **If NO** (ADR-047 §13.4's expectation: headless macOS has no loopback audio device) → ship **browsing-only, video deferred**; the gate correctly **stays not-capable** (AC-5).

**Both outcomes are acceptable "Done"** per ADR-052 §5. The phase does not bet on the spike succeeding.

## 4. Scope (workstreams)

| # | Workstream | Notes |
|---|---|---|
| **W1** | **Audio spike** | Empirical: capture audio via the managed Chrome on darwin `--headless`; record outcome. Drives AC-1 and the AC-4-vs-AC-5 branch. |
| **W2** | **C3 notarization decision** | Pick (i) re-sign Chrome + Hardened Runtime, (ii) sibling signed-and-notarized helper outside `.app`, or (iii) macOS = runtime-download primary, `.app` wraps only the gateway. Recorded as an ADR. |
| **W3** | **darwin/arm64 build + package** | `GOOS=darwin GOARCH=arm64` binary; `cft-bundle.sh` `mac-arm64`/`mac-x64`; goreleaser darwin archive per the C3 layout; `install.sh` macOS path. |
| **W4** | **macOS sandbox (Seatbelt/sandbox-exec)** | Real implementation + tests; graceful degradation preserved; bundled-Chrome path reachable. |
| **W5** | **launchd service + credentials** | launchd plist (M4 macOS slice); `master.key` 0600 enforced by the ADR-004 boot contract. |
| **W6** | **Doctor macOS dependency check** | WARN-BROWSER-005 is Linux-only today (in-process ELF parser, `//go:build linux`). Add a Mach-O equivalent (in-process or `otool -L`) for the bundled Chrome's missing dylibs. |

## 5. Out of scope

- **Windows** (named-pipe allocator, `.msi`, Windows Service) — Phase 4.
- Relaxing the video gate for **Windows** — only after Phase 4's own audio spike.
- A macOS **auto-update** story — ADR-052 §7 open item; `install.sh` remains re-runnable.
- Any change to the **Linux** path delivered in Phases 1–2 (must not regress).

## 6. Acceptance criteria

Each criterion is a verifiable pass/fail. Reviewer sign-off requires every applicable AC met or explicitly deferred with a tracked issue.

### AC-1 — Audio spike executed and outcome recorded
- [ ] A reproducible spike script/run captures `chrome.tabCapture` audio on darwin/arm64 under `--headless` full Chrome.
- [ ] Outcome is binary and recorded: **AUDIO-WORKS** or **AUDIO-ABSENT** (with evidence: a saved audio sample proving non-silence, or a documented null result).
- [ ] The result is referenced by the C3 ADR and the gate decision (AC-4 / AC-5).

### AC-2 — C3 notarization decision landed as an ADR
- [ ] Exactly one of (i) / (ii) / (iii) is chosen and justified (cost, maintenance, UX, signing-chain risk).
- [ ] The decision is consistent with the AC-1 outcome (e.g. option (iii) is favored if AUDIO-ABSENT, since a bundled full-Chrome `.app` buys less when video is deferred).
- [ ] The ADR cites Apple's current notarization + hardened-runtime requirements and the Chrome-for-Testing signing reality.

### AC-3 — darwin/arm64 native build + package produced
- [ ] `GOOS=darwin GOARCH=arm64` binary builds clean (`make build` cross-compile; canonical `goolm,stdjson` tags).
- [ ] `cft-bundle.sh` resolves the live CfT `mac-arm64` (and `mac-x64` if in scope) full-Chrome build + `chrome.sha256`, mirroring the Phase-1 producer.
- [ ] goreleaser produces a darwin archive laid out per the C3 decision.
- [ ] `install.sh` installs on macOS to the runtime-resolvable package-root location (the darwin row of `packageChromeRootCandidates`).

### AC-4 — IF AUDIO-WORKS: macOS video capability enabled
Only applicable when AC-1 = AUDIO-WORKS.
- [ ] `ClassifyVideoCapability` returns **Capable** on darwin when the package Chrome + verified audio are present.
- [ ] ADR-048 condition 3 is honored (not Capable when capture cannot succeed).
- [ ] The per-OS gate relaxation (M3/M6) is applied for darwin **only**, gated behind the spike result — not a blanket `goosForCapability` change.
- [ ] ADR-048 gains the one-line note confirming the per-OS-capable classifier still honors condition 3 (ADR-052 §7 open item).

### AC-5 — IF AUDIO-ABSENT: browsing-only, video deferred (documented)
Only applicable when AC-1 = AUDIO-ABSENT.
- [ ] The darwin video gate **stays not-capable** with a clear operator `Reason` (e.g. *"headless macOS has no loopback audio device; live-view video deferred to a future phase"*).
- [ ] **Browsing still works** on macOS via the bundled Chrome (the JPEG live-view fallback per ADR-047 D3 continues to function).
- [ ] The deferral is documented in the C3 ADR + README "Platform support".

### AC-6 — macOS sandbox (Seatbelt) implemented and tested
**Implemented on darwin/amd64** — full record, empirical findings and residual risk: [ADR-052 Phase 3 / AC-6 implementation record](ADR-052-phase3-AC6-macos-seatbelt.md).
- [x] A Seatbelt/sandbox-exec profile is implemented for darwin (not just app-level fallback) and is the active backend when the kernel supports it. (`sandbox_darwin.go`; `applyPlatformHardening` wraps every hardened-exec child.)
- [x] Graceful degradation is preserved (HC #4): falls back to app-level enforcement if Seatbelt is unavailable. (Real `sandbox-exec` probe + `OMNIPUS_SEATBELT_DISABLE` kill-switch, both covered by tests.)
- [ ] The bundled-Chrome path is reachable under the Seatbelt profile (no Landlock-style path block). — **NOT verified.** Depends on AC-3, which has not landed: there is no darwin package Chrome on this host to exercise. The preamble does not cover `/Applications` at all — a bundled-Chrome path there is reachable only if a policy rule names it, so this needs re-checking when AC-3 lands.
- [x] Unit tests for the darwin backend run where possible; integration verified on macOS. (Renderer tests run on Linux CI; `seatbelt_integration_darwin_test.go` and `seatbelt_adversarial_darwin_test.go` run real children on macOS.)

> ⚠️ **Not an arm64 sign-off.** Validated on darwin/**amd64**; this phase targets darwin/arm64 and CI's `macos-latest` is arm64. Profile semantics are architecture-independent, but the empirically derived constants should be re-confirmed on arm64 before AC-6 is closed for the target platform.

### AC-7 — launchd service + credentials
- [ ] A launchd plist starts/stops the gateway as a service on macOS.
- [ ] `master.key` is created 0600 and stays 0600 (enforced by the ADR-004 boot contract, not the installer).
- [ ] The service runs the **bundled** Chrome (resolution order holds on darwin).

### AC-8 — Doctor macOS dependency check (WARN-BROWSER-005 parity)
- [ ] A macOS dependency check (Mach-O `DT_NEEDED`/dylib equivalent) flags missing shared libraries for the bundled Chrome.
- [ ] WARN-BROWSER-006 (hash mismatch) works on darwin via the shared `chromeintegrity` package.
- [ ] Build-tagged so Linux behavior is untouched (mirrors the `command_libs_linux.go` / `command_libs_other.go` split).

### AC-9 — No regression on Linux / Phases 1–2
- [ ] All Phase 1–2 gates remain green on Linux: `gofmt`, `go vet`, `golangci-lint`, scoped `go test`, `bats`, `shellcheck`, `make verify-contracts`.
- [ ] The Linux package Chrome resolution + doctor behavior is unchanged.

### AC-10 — End-to-end macOS validation
- [ ] On a real macOS host: install → gateway boots → agent browses via the **bundled** Chrome (proven by binary path, as in Phase 2) → credential boot holds → doctor clean on a healthy install.
- [ ] If AC-4 applies: a live-view session streams **video+audio**; if AC-5 applies: it renders JPEG fallback.

## 7. Environment constraint (execution reality)

This work cannot be executed end-to-end on the current **Linux** devpod:

| Can start now (Linux/devpod) | Requires macOS |
|---|---|
| C3 notarization **design/ADR** (the 3-option decision, costed) | The **audio spike** itself (AC-1) |
| darwin/arm64 **cross-build** of the binary | Notarization **signing** (`codesign` + `xcrun notarytool`) |
| Seatbelt sandbox **code** + Linux-runnable unit tests | darwin **build smoke test** + Seatbelt integration (AC-6, AC-10) |
| goreleaser **darwin archive** + `cft-bundle.sh mac-arm64` plumbing | launchd plist **real install/test** (AC-7) |
| Gate-relaxation **logic** wired behind the spike flag | The actual **gate sign-off** (AC-4 vs AC-5) |

**Recommendation:** do the Linux-buildable column now; run AC-1 / AC-6 / AC-7 / AC-10 on a macOS CI runner (the repo's cross-platform workflow already uses `macos-latest` = arm64) or a Mac.

## 8. Risks & open questions

- **Audio spike likely fails** (ADR-047 §13.4) → outcome (b). Plan must make (b) a first-class ship, not a consolation.
- **C3 option (i) re-signing** may trip Chrome's own integrity/self-check and incur per-Chrome-bump maintenance — weigh against (ii)/(iii).
- **Seatbelt profile correctness** is security-sensitive; needs adversarial review (a too-permissive profile is worse than the app-level fallback it replaces).
- **Pinned Chrome version cadence on macOS** — same ADR-052 §7 open item as Linux.
- **`mac-x64` (Intel Mac)** — ADR-052 deferred darwin/amd64 to a later task; confirm whether Phase 3 ships arm64-only or both.

## 9. Definition of Done (Phase 3)

Phase 3 is complete when **all applicable AC are met** (AC-1, AC-2, AC-3, AC-6, AC-7, AC-8, AC-9, AC-10, and **exactly one** of AC-4 / AC-5 per the spike outcome), merged to `release/v0.1.1`, and full CI green on the macOS matrix.

---

### Suggested next steps (per the operator's workflow)
1. `/albert` — produce the **C3 notarization ADR** (options, trade-offs, per-decision confidence), then `/grill-spec` it.
2. `/plan-spec` — turn this goal into a BDD/TDD spec for the Linux-buildable workstreams (W3 cross-build, W4 Seatbelt code, W6 Mach-O doctor, gate logic).
3. Stand up a **macOS CI runner** path for AC-1 / AC-6 / AC-7 / AC-10.
4. `/taskify` the resulting spec into epics/issues (one per workstream).
