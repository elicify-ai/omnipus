# macOS deployment & runner plan (ADR-052 Phase 3)

This directory holds the macOS delivery artifacts and the plan for the
acceptance criteria that **cannot run on the Linux devpod** (AC-1 audio spike,
AC-6 Seatbelt integration, AC-7 launchd install, AC-10 end-to-end). Per the
[Phase 3 goal](../../docs/internal/architecture/ADR-052-phase3-macos-goal.md) §7,
those ACs require a macOS host.

## Artifacts

- `ai.omnipus.gateway.plist` — launchd service **template** (AC-7). `@VARIABLES@`
  are substituted at install time. See the header comment for install/bootout
  commands. **Not installable as-is; validated only on macOS.**

## C3 decision (signed-off separately)

Per [ADR-052-C3](../../docs/internal/architecture/ADR-052-C3-macos-chrome-notarization.md):
**option (ii)** — ship Chrome-for-Testing as a **Google-signed sibling** outside
the `.app`; sign/notarize **only** the gateway `.app`. Chrome's signature is
never touched.

## Notarization pipeline (release step — runs on macOS)

Requires Apple Developer ID + notarization creds provisioned as CI secrets:
`APPLE_DEV_ID` (Developer ID Application cert name), `APPLE_ID`,
`APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID`.

1. `make build` → `GOOS=darwin GOARCH=arm64` gateway binary (cross-verified on Linux).
2. `bash scripts/cft-bundle.sh arm64 darwin` → fetches CfT `mac-arm64` full Chrome
   + `chrome.sha256` beside the `.app` inner binary (the **sibling**, Google-signed, untouched).
3. Assemble the gateway `.app` (extend `scripts/build-macos-app.sh` — currently
   Launcher-only, unsigned): bundle the gateway binary + `Info.plist` + entitlements.
4. Sign the **gateway `.app` only**:
   ```sh
   codesign --deep --force --options runtime \
     --entitlements deploy/macos/Omnipus.entitlements \
     --sign "$APPLE_DEV_ID" "build/Omnipus.app"
   ```
   **Do not sign the Chrome sibling** (it stays Google-signed).
5. Notarize + staple:
   ```sh
   ditto -c -k --keepParent build/Omnipus.app build/Omnipus.app.zip
   xcrun notarytool submit build/Omnipus.app.zip \
     --apple-id "$APPLE_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" \
     --team-id "$APPLE_TEAM_ID" --wait
   xcrun stapler staple build/Omnipus.app
   ```
6. Package: the `.app` + the Chrome sibling (+ `chrome.sha256`) into the darwin
   archive/goreleaser layout per `packageChromeRootCandidates`' darwin slot.

Entitlements: minimal by default. Add `com.apple.security.device.audio-input`
(+ camera) **only if AC-1 proves audio** (AC-4 path); otherwise keep the set
minimal (AC-5 browsing-only path).

## AC-1 audio spike procedure (the gating empirical question)

Run on a macOS host (`macos-latest` runner or a Mac):

1. Build the darwin/arm64 binary; flip the spike seam (`darwinAudioVerified=true`
   in `capability.go`, or set the test seam) **for the spike only** — do not ship it flipped.
2. Boot the gateway with a tool-capable model; trigger a `chrome.tabCapture`
   session on the managed full-Chrome under `--headless`.
3. **Measure**: does the captured `MediaStream` carry a **non-silent audio track**?
   - Save a sample; confirm non-zero PCM levels.
   - ADR-047 §13.4 expects headless macOS has **no loopback audio device** → likely AUDIO-ABSENT.
4. Record the binary outcome (AUDIO-WORKS / AUDIO-ABSENT) with evidence.
   - AUDIO-WORKS → AC-4: leave `darwinAudioVerified=true` (gated behind review), relax the darwin video gate.
   - AUDIO-ABSENT → AC-5: keep the gate not-capable; document "macOS = browsing-only, video deferred".

## launchd install / test (AC-7)

```sh
PLIST=~/Library/LaunchAgents/ai.omnipus.gateway.plist
# substitute @PREFIX@ (e.g. /usr/local) and @OMNIPUS_HOME@ (e.g. ~/.omnipus)
install -m 0644 deploy/macos/ai.omnipus.gateway.plist "$PLIST"
launchctl bootstrap gui/$(id -u) "$PLIST"
launchctl kickstart -k service-target/$(id -u)/ai.omnipus.gateway
```

Verify: gateway boots, resolves the **bundled** Chrome sibling, `master.key`
created 0600 (ADR-004 boot contract), survives logout (KeepAlive). Bootout to uninstall.

## ACs that defer to this runner

| AC | What | Where |
|---|---|---|
| AC-1 | audio spike | macOS host |
| AC-6 | Seatbelt integration (`backend_darwin_seatbelt.go` `Available()` flip + adversarial review) | macOS host |
| AC-7 | launchd plist real install/test | macOS host |
| AC-10 | end-to-end macOS install→browse→(video\|JPEG) | macOS host |
| AC-3 (partial) | notarized gateway `.app` produced + stapled | macOS release step |

## What is Linux-buildable (done / in this phase, no Mac needed)

- C3 ADR (this decision), Phase 3 goal doc.
- darwin/arm64 **cross-build** of the binary (`GOOS=darwin go build` — verified on Linux).
- `cft-bundle.sh arm64 darwin` producer logic (bats-tested).
- `packageChromeRootCandidates` darwin slot + `findPackageChrome` darwin `.app` layout.
- Mach-O doctor check (`command_libs_darwin.go`) — cross-vet'd + parser unit-tested on Linux.
- Seatbelt **profile renderer** + draft backend — cross-vet'd + render-tested on Linux.
- Gate-relaxation logic behind the `darwinAudioVerified` spike flag (default off).
- launchd plist template (this dir).

The macOS runner only needs to: sign/notarize, install, run the spike, flip the
two flags (`darwinAudioVerified`, Seatbelt `Available()`), and validate AC-10.
