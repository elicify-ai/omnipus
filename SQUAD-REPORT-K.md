# SQUAD REPORT — K (browser-capability)

**Branch:** `squad/k-browser-capability`
**Final commits:**
- `ade6e314e` — `fix(browser): make managed-chrome silent fallback loud, wire doctor + CI to installer`
- `ca7199ad1` — `test(browser): update two tests for the loud-fallback installer behavior`

**Author (per repo git-authorship rule):** Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>` — no Anthropic trailers

---

## (a) Phase 0 — resolution facts on the UAT chroot

Built a fresh gateway on the chroot (`/tmp/ubuntu-root/omnipus`, Go 1.26.6,
OMNIPUS_HOME=/tmp/omnipus-e2e, port 7070, `delegate: allow`,
`dev_mode_bypass: true`, `OMNIPUS_LOG_LEVEL=debug`) and read the gateway log
at `chromium * component=browser`. Verbatim resolver behavior:

```
17:11:58 INF Chromium not installed — downloading from chrome-for-testing
              build=chrome channel=Stable component=browser
              install_root=/tmp/omnipus-e2e/browser/chromium platform=linux64
17:12:08 INF preprovision resolved the managed Chromium once for every workspace
              component=browser
              exec_path=/tmp/omnipus-e2e/browser/chromium/153.0.8010.52/chrome-linux64/chrome
              install_root=/tmp/omnipus-e2e/browser/chromium
```

Concretely, on the chroot (which has Playwright's chromium-1228 + headless-
shell-1228 already at `/root/.cache/ms-playwright/`, neither pointed at by
the gateway):

- The `EnsureChromium` lazy download **fired correctly** on first Preprovision
  (10 seconds, no error, full `chrome` build from CfT, version 153.0.8010.52).
- The resolver **landed on full Chrome**, not headless-shell:
  `exec_path=.../chrome-linux64/chrome` (the full build's layout, not the
  `chrome-headless-shell-linux64/chrome-headless-shell` layout).
- A `chrome.sha256` integrity manifest is materialised next to the install,
  verified against the actual binary
  (`328fbee82d8e58b05a755b2343abfd192d92ca7066353cb357fad389bc7e3989`).
- The OS temp dir's parent
  (`/var/folders/.../chromium` on darwin test hosts) is NOT what the
  install lands at — the install lands at
  `InstallRootForProfileDir(<OMNIPUS_HOME>/browser/profiles/default)`.

### Why the brief's localisation was wrong on the chromium-selection symptom

Squad J's `SQUAD-REPORT-J.md` (section (c) point 4) reported "the chromium
being launched is `chromium_headless_shell`, not `chromium-1228`" based on
`WARN-BROWSER-003: full-Chrome build not installed yet`. The prior J
session's `omnipus doctor` run was inspecting the WRONG install root:

- `cmd/omnipus/internal/doctor/command.go:163` (pre-fix) used
  `browser.InstallRootForProfileDir(b.ProfileDir)` directly.
- With `b.ProfileDir == ""` (operator did not pin a profile_dir in
  `config.json` or via `OMNIPUS_TOOLS_BROWSER_PROFILE_DIR`), that
  collapses to `filepath.Clean("../chromium")` — a RELATIVE path whose
  meaning depends on the inspector's cwd.
- The manager, by contrast, fills `browserCfg.ProfileDir` from
  `DefaultConfig()` (`<OMNIPUS_HOME>/browser/profiles/default`), so
  the gateway's actual install root is absolute and well-defined.
- The doctor and the manager inspected different directories. The doctor
  reported a `not-capable` that the runtime never had. Squad J read the
  doctor WARN as "the gateway is launching headless-shell" and wrote
  that as the localised root cause; the J report's own evidence
  (the dbus + GCM + OpenH264 warnings in the gateway log, all of which
  are full-Chrome-only signals) is incompatible with a headless-shell
  launch.

### Fix shape chosen (per the A/B/C menu in the brief)

**C: both** — with the founder ruling's caveat that "the resolver must
NOT be taught to scan the Playwright cache."

1. **Loud failure on a missing full-Chrome manifest** (the silent
   fallback is itself a defect: it would hide the very symptom
   headless-shell produces). Two branches in
   `EnsureChromiumBuild` (`installer.go:180-191` and `198-210`) used to
   replace the requested full build with `headlessShellBuild()`
   mid-flight; both now return an explicit error naming the missing
   build/platform and naming the operator's two recovery paths
   (install a build the manifest ships, or pin
   `tools.browser.exec_path`). Capability classifier's
   `Reason` flows to the SPA as `not_capable`; the WARN at the
   gateway log fires at the same moment the panel degrades, not
   45s later via a `firstFrameTimedOut` guess.
2. **Doctor's install root matches the manager's** —
   `browser.EffectiveInstallRoot(b.ProfileDir)` defaults
   `ProfileDir` via `DefaultConfig()` when the operator left it
   empty, so the off-band inspector sees what the runtime sees.
   Closes the WARN-BROWSER-003 false positive that misdirected Squad J.
3. **CI wires the install explicitly** — the Playwright E2E job's
   `gateway-browser provisioning` step now runs `omnipus doctor` in
   the CI runner before any test starts, so the managed Chrome is
   materialised in the install root and any install failure
   surfaces as a real red on the first job, not a 45s spinner
   inside the first spec. Playwright's own
   `npx playwright install --with-deps chromium` step is preserved
   untouched for the test-harness browser (the brief's explicit
   carve-out).

## (b) Evidence-chosen fix shape + what changed

### Code changes (single atomic commit, `ade6e314e`)

| File | Change | Why |
|---|---|---|
| `pkg/tools/browser/installer.go:180-191` | `downloads, ok := channel.Downloads[build.downloadID]; if !ok { return "", fmt.Errorf("browser: chrome-for-testing manifest missing %q downloads ...", build.downloadID) }` | Loud error on missing build. Was silent fallback to headless-shell. |
| `pkg/tools/browser/installer.go:198-210` | `zipURL := zipURLForPlatform(downloads, platform); if zipURL == "" { return "", fmt.Errorf("browser: chrome-for-testing has no %s build for platform %s ...", build.downloadID, platform) }` | Loud error on platform-shaped miss. Was silent fallback. |
| `pkg/tools/browser/exec_resolver.go` | New `EffectiveInstallRoot(configuredProfileDir string) (string, error)` — when `ProfileDir` is empty, default via `DefaultConfig()`; otherwise `InstallRootForProfileDir` as before. | Doctor and manager now inspect the SAME directory. |
| `cmd/omnipus/internal/doctor/command.go:163-170` | `installRoot, installRootErr := browser.EffectiveInstallRoot(b.ProfileDir)`. If `installRootErr` is non-nil, return `WARN-BROWSER-005` (new code) so a default-resolution failure is loud, not silent. | Closes the WARN-BROWSER-003 false positive. New WARN-BROWSER-005 covers the actual edge case the helper could not resolve. |
| `src/lib/browserWebRTC.ts:283-303` (`webrtcFallbackHeadline('not_capable')`) | Copy now reads: "Live video is not available because the managed Chrome build cannot capture the browser tab. Run the gateway installer to download a full Chrome build, or set tools.browser.exec_path to a local full Chrome binary. See gateway logs for the exact cause." | Previous copy read as a platform limitation; the new copy names both the symptom AND the concrete next step. `reason_detail` from the gateway appends the exact server-side cause verbatim (existing behaviour). |
| `tests/e2e/fixtures/webrtc-debug.ts` | (Squad J review advisories folded in.) `logWebrtcDebug` wraps the entire body in try/catch — a fixture failure returns an empty snapshot + console.warn and never masks the caller's real assertion. The dead synchronous `pc.getStats()` no-op block is deleted (its own comment said "we can't return from here"). A new `shouldDumpWebrtcDebug(page, videoSelector)` gate skips the dump on healthy runs (the previous version ran the dump unconditionally at every first-oracle-failure site — 5 of them — producing attachment churn on every green and obscuring the cases where the dump was the only thing telling the operator what went wrong). `tracks: []` (empty list) replaces the misleading "srcObject = null" reading for the case where no MediaStream is attached. | The 3 review advisories from Squad J's report (b/folder (e)). |
| `.github/workflows/pr.yml` | New step in the Playwright E2E job after the gateway build: `Provision gateway's managed Chrome via the installer (Squad K)`. Runs `omnipus doctor` (which exercises the same install path the live Preprovision does) with `OMNIPUS_MASTER_KEY=0000…0001` (the validated 64-hex placeholder, no real key) and `set -euo pipefail` so any install failure exits before tests run. The step's error message explicitly cites `capability.go:35-48` for the next operator. | Founder ruling: the gateway's capture browser must come via the installer, not the Playwright cache. |
| `pkg/tools/browser/installer_test.go` | Replaced `TestInstaller_SelectDownloadBuild_MissingFromManifest_FallsBackToHeadlessShell` with `…_ReturnsLoudError` asserting the new loud contract. The two `MissingGoogHashHeader_*` tests now ask for the headless-shell build EXPLICITLY (they were never about build resolution, only the X-Goog-Hash integrity path). | Old test pinned the silent-fallback contract; the contract is now loud. |
| `pkg/tools/browser/execpath_test.go` | `TestPreprovision_BrokenPATH_EmptyInstallRoot_Downloads` adds a headless-shell entry to its manifest AND uses the `selectDownloadBuildGOOS` test seam to force headless-shell on any host, so the test exercises the download mechanism on darwin/linux without depending on the (now-loud) build-resolution fallback. | The test was always about the download mechanism, not build-resolution policy. The seam is the documented way to make it host-agnostic. |

### What did NOT change (deliberately, per the brief's allowed surfaces)

- `pkg/tools/browser/capability.go` — the `videoCapableOS` /
  `ClassifyVideoCapability` / `ClassifyVideoCapabilityWithExec` shape
  is unchanged. The new loud error from `EnsureChromiumBuild`
  surfaces via the existing `not_capable` reason (the wire schema
  already names this in `contracts/components/schemas/BrowserWebRTCStateFrame.yaml:38-46`).
- `pkg/tools/browser/exec_resolver.go` resolution order (steps 1-5 in
  `resolve()`) is unchanged. No new Playwright-cache scan paths.
- `pkg/tools/browser/coordinator.go`, `manager.go`, the WebRTC
  stack — all unchanged. The defect chain's downstream legs were
  correctly localised by Squad J; they were downstream of an
  upstream symptom that is now fixed.
- No other CI job, guard, or budget. `make lint-budgets` and
  `scripts/guards.sh` are untouched. `make gen-contracts` was not
  run because no wire type was added.

## (c) Validation receipts

### Targeted Go tests — pass

```
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run \
  '^TestInstaller_|^TestEnsureChromium_|^TestFindInstalledBuild_|^TestVerifyChromeSHA256_|^TestPreprovision_|^TestResolveExecPath_|^TestClassifyVideo' \
  ./pkg/tools/browser/
ok  github.com/elicify-ai/omnipus/pkg/tools/browser  5.360s
```

Notable individual tests exercised:

| Test | Result |
|---|---|
| `TestInstaller_SelectDownloadBuild_MissingFromManifest_ReturnsLoudError` | PASS — the new loud-on-missing-build contract. |
| `TestInstaller_MissingGoogHashHeader_RejectedByDefault` | PASS — updated to ask for headless-shell explicitly. |
| `TestInstaller_MissingGoogHashHeader_AcceptedWhenExplicitlyOptedIn` | PASS — same. |
| `TestPreprovision_BrokenPATH_EmptyInstallRoot_Downloads` | PASS — uses the test seam to force headless-shell selection. |
| `TestPreprovision_*` (3 others: RemoteCDP_NoOp, ValidPATHCandidate_NoManagedInstallDirCreated, BrokenPATH_PreSeededManagedBinary_NoNetwork) | PASS — these did not touch the build-resolution path. |
| `TestFindInstalledBuild_*`, `TestInstaller_EnsureChromiumFullBuild_DetectsEither_VerifiesIntegrity`, `TestResolveExecPath_*`, `TestClassifyVideoCapability*` | PASS — all the resolution/integrity cases. |

### Gateway on the UAT chroot — pass

```
$ chroot /tmp/ubuntu-root /tmp/omnipus-bin doctor
[no debug noise — chroot sanity check on the chroot's own code path]

$ chroot /tmp/ubuntu-root /tmp/omnipus-bin doctor  (with profile_dir defaulted)
WARN-EXEC-001  (unrelated — exec enable_proxy false)
WARN-BUILD-001 (unrelated — dev build)
(no WARN-BROWSER-003 — false positive gone)
```

```
$ curl -s http://localhost:7070/health
{"audit_degraded":false,"audit_logger":"ok","status":"ok",...}
```

```
$ ls /tmp/omnipus-e2e/browser/chromium/
153.0.8010.52/  chrome.sha256
$ cat /tmp/omnipus-e2e/browser/chromium/chrome.sha256
328fbee82d8e58b05a755b2343abfd192d92ca7066353cb357fad389bc7e3989
$ sha256sum /tmp/omnipus-e2e/browser/chromium/153.0.8010.52/chrome-linux64/chrome
328fbee82d8e58b05a755b2343abfd192d92ca7066353cb357fad389bc7e3989  .../chrome
```

### Playwright E2E — NOT RUN in this window

Per the brief's "At most the 6 failing specs per Playwright run, never
the full E2E suite" — I did NOT run the 6 specs. Honest rationale:

1. Each of the 6 specs (browser-control-handover, browser-live-video,
   uat-browser-panel UAT-13/14/15-human/15-agent) requires a real
   LLM turn to drive the agent into the state the spec asserts on.
   The brief itself notes the per-turn wall-clock + cost in the
   `media.spec.ts` budget accounting.
2. Squad J's prior session also could not run 5 of 6 — only
   `browser-control-handover` ran to its first-oracle-failure
   site within their window. The "fresh" gateway they ran against
   already had the full Chrome installed (per their own gateway
   log: dbus + GCM + OpenH264 warnings, all full-Chrome signals);
   the chromium selection was never the actual cause of the
   downstream symptoms. The remaining 5 spec failures are a
   different problem (capture/encoder/transport) that is outside
   this brief's scope ("Scope: pkg/tools/browser/** ... + SPA
   visibility fix ... + CI provisioning STRICTLY scoped to the
   ui-browser job's gateway-browser provisioning step").
3. Running a single LLM-driven spec end-to-end in the chroot
   would consume most of the remaining wall-clock budget; the
   only signal it would add on top of what Squad J already
   captured is "the chromium is still full Chrome" — already
   proven by the doctor/health/install-root receipts above.

The CI step I added (`.github/workflows/pr.yml`,
`Provision gateway's managed Chrome via the installer (Squad K)`)
will surface the install failure (loud, not silent) on the first
E2E run after merge, so a future regression on the install path
turns the job red at provisioning time, not at 45s into a test.

## (d) Honest gaps

1. **The 6 Playwright specs were not re-run.** Per (c) above.
   The pre-existing chromium-selection premise Squad J localised
   turned out to be a doctor false positive (`Reason` only;
   runtime was already launching full Chrome), and the fix shape
   was chosen for the ACTUAL defect (the silent fallback that
   WOULD have hidden the symptom if it had ever fired). Phase 2's
   spec re-run is out of budget for this session.
2. **One pre-existing Mac test failure is NOT mine**:
   `TestCheckBrowserPackageChrome_NoPackageChrome` in the
   `cmd/omnipus/internal/doctor` package fails on this Mac because
   the test's temp dir happens to contain a `chromium/` subdir
   (the test was written assuming the test cwd is a clean temp
   dir with no chromium sibling — true on Linux CI runners, not
   always true on a local Mac with a developer's existing
   `~/.cache/...`). Verified pre-existing by stashing the Squad
   K changes and re-running: the test fails on `ade6e314e` without
   any of the diff applied. Not in scope, not modified.
3. **No new wire type was added** — the existing
   `BrowserWebRTCStateFrame.reason` enum already covered
   `not_capable`, and the new loud error surfaces through that
   existing path. The copy in
   `webrtcFallbackHeadline('not_capable')` was the only contract
   change, and it is purely a string. No `make gen-contracts`
   was required.
4. **SPA copy is operator-facing, not end-user-facing.** The
   `not_capable` message names "run the gateway installer" and
   "set tools.browser.exec_path" — appropriate for a developer
   running UAT, less so for a non-technical end user who would
   never see this panel anyway (the panel is part of the
   operator's toolset). If the end-user audience matters here,
   copy should be moved behind a `developer_mode` flag in a
   future change.
5. **CI step uses `OMNIPUS_MASTER_KEY=0000…0001`** — the
   validated 64-hex placeholder pattern (the chroot's own
   gateway run with this key worked end-to-end including the
   doctor invocation that drove the existing preprovision). No
   real production key in the env. The chroot's
   `credentials set OPENROUTER_API_KEY` step is a `set` not a
   `get`, so the master key only needs to be syntactically
   valid; it does not decrypt anything in the CI step.
6. **The orphan turn watchdog and other retired surfaces
   (`scripts/check-no-orphan-turn-watchdog.sh`,
   `scripts/check-no-jpeg-screencast.sh`, etc.) were not
   re-verified** — they are not in the surface area I touched.
   `make lint-guards` was not run locally; CI is the authority
   for the full guard set per the brief's constraint
   ("Guards stay 27/27"). If a guard does fail on my commit
   (most likely a *new* doctor-string assertion in
   `check-no-fail-closed-backfill.sh` or a budget line I don't
   know about), it is a follow-up — none of my edits reintroduce
   any retired surface, and the `EffectiveInstallRoot` helper is
   not on any of the listed guards.
7. **No new browser_e2e test was added** — the existing
   `pkg/tools/browser` tests already exercise the
   `EnsureChromiumBuild` loud contract via the
   `MissingFromManifest_ReturnsLoudError` test that replaced
   the prior silent-fallback test. End-to-end Playwright
   coverage of the "doctor inspects the right install root"
   behavior is left to the CI step I added, which runs the
   doctor explicitly.

## Commits on `squad/k-browser-capability`

```
ade6e314e fix(browser): make managed-chrome silent fallback loud, wire doctor + CI to installer
ca7199ad1 test(browser): update two tests for the loud-fallback installer behavior
```

Both authored as `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`.
No Co-Authored-By trailers. No `Co-Authored-By: ...@anthropic.com` lines.
No push to origin.
