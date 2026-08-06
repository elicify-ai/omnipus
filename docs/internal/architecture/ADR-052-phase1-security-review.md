# ADR-052 Phase 1 — Security Design Review

- **Reviewer:** security-lead (opus)
- **Subject:** `docs/internal/architecture/ADR-052-native-cross-platform-browser-bundled-distribution.md`
- **Scope:** Phase 1 only — Linux bundle + delivery + doctor warnings (WARN-BROWSER-005, WARN-BROWSER-006). Phases 2/3/4 out of scope.
- **Date:** 2026-07-21
- **Method:** Read-only review of the ADR + the integrity-surface code (`pkg/tools/browser/exec_resolver.go`, `pkg/tools/browser/installer.go`, `cmd/omnipus/internal/doctor/command.go`, `scripts/install.sh`, `.goreleaser.yaml`). Code referenced by line where useful; **no Phase 1 implementation exists yet** — findings are design-level, addressed to the implementation agent who picks up Phase 1.

---

## Executive summary

ADR-052 M2's "package-bundled Chrome + `chrome.sha256`" is **in spec only today**: `pkg/tools/browser/installer.go` exposes the path computation (`chromiumBuild.sha256Path()`) but no `verifyChromeSHA256()` exists, no `findInstalledBuild()` integrity gate exists, and `.goreleaser.yaml` has no post-step that emits `chrome.sha256`. Likewise, `cmd/omnipus/internal/doctor/command.go` ends at WARN-BROWSER-004; WARN-BROWSER-005 (ldd) and WARN-BROWSER-006 (Chrome hash) are promised for Phase 1 but absent. The biggest risks are all in what the implementation has yet to add — chiefly (a) **failing open when `chrome.sha256` is missing**, (b) **trusting a `chrome.sha256` value via symlink/file-attacker**, and (c) **`$PATH` Chrome precedence over a verified package Chrome when `PreferPackaged=false` is default**, which means a malicious user-installed `google-chrome` earlier on `$PATH` rides the resolution path. SHA-256 parser hardening, ldd → in-process ELF parsing (HC #2), and the goreleaser post-step that emits the manifest close out the rest. Three Blockers, three Highs, two Mediums, one Low.

---

## Findings

### SEC-ADR052-001 — Blocker — `verifyChromeSHA256()` not yet implemented; "missing chrome.sha256 = accept" defaults to fail-open

- **Location:** `pkg/tools/browser/installer.go` (new function in Phase 1); expected to be called from `findInstalledBuild()` per ADR §5 Phase 1.
- **Issue:** The ADR §3 M2 says `findInstalledBuild` "verifies that file at first launch and refuses a mismatched bundled Chrome." It is silent on the case where `chrome.sha256` is **absent** (e.g. someone hand-extracted the tarball without our goreleaser post-step, or the file was lost during a package downgrade). A naive "missing manifest → log warn → use binary anyway" implementation would be a textbook fail-open. The `chromiumBuild.sha256Path()` path function (installer.go:110-115) is in place; the actual verifier is not.
- **Recommendation:** When `chrome.sha256` is unreadable (any cause: missing, permission denied, EIO), **refuse the bundled Chrome** at `findInstalledBuild()` and fall through to the next resolution tier (managed download) — exactly as `verifyGoogHashMD5` already does at installer.go:512-527 when the X-Goog-Hash header is missing. Emit a single `WARN-CFTSHA-001` to the audit log so operators can correlate. If a package install legitimately can ship without the integrity file (none does today), that's a goreleaser post-step bug, not a runtime accept. **HC #6 says no silent default fallback**; this path is exactly that. The `.goreleaser.yaml` has no `chrome.sha256` post-step — flag this as a Phase 1 implementation task.
- **Rationale:** An attacker who can write `<installRoot>/chrome` can also write `<installRoot>/chrome.sha256`. Accepting the binary when the manifest is "missing" is identical to accepting an unverifiable binary. The only legitimate causes of a missing manifest are pipeline failures (already a release blocker) and operator tampering (already a hard-stop).

### SEC-ADR052-002 — Blocker — `PreferPackaged: false` default + `$PATH`-above-package precedence = trusted binary of unknown origin

- **Location:** `pkg/tools/browser/exec_resolver.go:326-344` (current `$PATH` probe loop) + ADR §3 D2 resolution order; `tools.browser.prefer_packaged` toggle per ADR §3 M1.
- **Issue:** The ADR resolution order (D2/M1) keeps `$PATH` Chrome **above** the package Chrome to preserve operator autonomy ("a deliberately newer/patched `$PATH` Chrome still wins") and the classifier fix from `decb01f0`. With `PreferPackaged: false` as default, the operator-install path is: (1) `cfg.ExecPath` if set → (2) **$PATH google-chrome/chromium if present** → (3) verified package Chrome. `exec_resolver.go` already probes 4 PATH candidates and accepts the first one that `exec.LookPath` finds AND survives a `--version` probe; there is no integrity check on the $PATH candidate. Threat model: a multi-tenant developer box, a CI runner, or a compromised developer machine can plant a `google-chrome` script earlier on `$PATH` and the runtime will execute it. The package Chrome SHA-256-verified under step 3 never runs.
- **Recommendation:** Three options, in order of preference:
  1. **Default `PreferPackaged: true`** and treat `$PATH` as an opt-in escape hatch — flip the precedence from the ADR. Documentation calls out "operator autonomy" but the threat model for a managed install is "the package's verified Chrome is the floor"; `$PATH` is the override, not the default. This is the only option that puts the integrity-verified build on the hot path by default.
  2. **Hold the ADR default but require explicit operator opt-in to trust a non-package binary.** Add `tools.browser.trust_path_chrome: false` (default false); when false, the resolver still *records* the `$PATH` resolution but logs `WARN-BROWSER-007` and skips the launch, falling through to the package Chrome. Operators who actually want a custom Chrome set `trust_path_chrome: true`. Document `OMNIPUS_BROWSER_NO_SANDBOX` (already exists at exec_resolver.go:138) as the second-axis toggle for inner-sandbox suppression; `trust_path_chrome` is the integrity axis.
  3. **Honor the ADR default but verify the `$PATH` binary too** — apply the same `chrome.sha256` check the package path gets. This is operationally fragile (a Homebrew Chrome does not ship a `chrome.sha256`), so option 1 or 2 is preferred.
- **Rationale:** Chromium is a remote-code-execution engine running on behalf of the agent. It loads arbitrary web pages, executes injected JS, and reads cookies/storage. Treating the path its binary comes from as "operator pick" is fine; treating the binary's integrity as optional because the operator might want a newer one is not. The compiled-in `--disable-blink-features=AutomationControlled` flag and the disable-extensions omission (exec_resolver.go:80-87) make the operator-installed Chrome a deliberate upgrade path, but `PreferPackaged: false` + `--use-mock-keychain` + no SHA check is the worst combination: stealth and un-verified.

### SEC-ADR052-003 — Blocker — goreleaser post-step to emit `chrome.sha256` is not specified, not stubbed

- **Location:** `.goreleaser.yaml` (missing); `scripts/install.sh` does not verify `chrome.sha256` for the Linux tarball.
- **Issue:** The integrity file is the entire point of M2, but the pipeline that creates it is unspecified. Without it, every install of a packaged Chrome lands without integrity metadata, and SEC-ADR052-001's "missing = refuse" recommendation would brick every release. The current goreleaser config (`.goreleaser.yaml:38-93`) builds the binary only; there is no `nfpms` `contents` section pinning `chromium/` artifacts and no `archives:` files list. CfT is downloaded at runtime today (`EnsureChromium`); if Phase 1 intends to ship CfT in the package, the goreleaser post-step is the only place that ships `chrome.sha256`.
- **Recommendation:** Specify the goreleaser post-step concretely in the ADR or in a Phase-1 implementation task:
  - A `before:` hook or a new `nfpms.contents`/`archives.files` entry that runs `sha256sum chromium/chrome > chromium/chrome.sha256` after the CfT fetch.
  - The script must verify CfT's own `X-Goog-Hash` before computing and shipping `chrome.sha256` (today that check is only at runtime in `downloadFile`).
  - Publish `checksums.txt` (referenced in `scripts/install.sh:111`) **per-OS-per-CfT-binary**, not only for the omnipus binary, so `install.sh` can verify both. That requires extending `install.sh` to verify a second artifact.
  - The implementation agent should phrase the goreleaser addition as a single, idempotent post-step (no in-place edits of `chrome.sha256` after a partial run).
- **Rationale:** Without the producer, SEC-ADR052-001 fails closed on every install. Without `install.sh` updates, the install path is unverifiable at the user's machine even though the install script is SHA-256-aware for the omnipus binary.

### SEC-ADR052-004 — High — SHA-256 parser is not specified; whitespace/BOM/uppercase/prefix tolerance unspecified

- **Location:** New `verifyChromeSHA256(path string) error` function in Phase 1.
- **Issue:** `pkg/tools/browser/installer.go:512-555` shows how `verifyGoogHashMD5` handles format quirks (multiple `X-Goog-Hash` lines, comma-folded lines, base64-encoded digests). The SHA-256 path will be different — the on-disk format is hex, plaintext, with no enforced prefix — and the obvious parser pitfalls are: BOM at the start of the file (`\xEF\xBB\xBF`), CRLF line endings, trailing newline, upper-case hex (Linux `sha256sum` emits lowercase; macOS `shasum` defaults to lowercase too, but Windows-port tools vary), optional `sha256: ` prefix (some release managers do this), an algo-prefixed multi-line file (`# SHA256\n<hex> *chrome` like BusyBox's), or a stripped file containing only the binary name.
- **Recommendation:** Spell out the grammar in a doc comment and the test suite. The parser must:
  - Read with `bufio.Scanner` (handles `\r\n` correctly when token mode is lines, but verify against `\r\n` in tests).
  - Strip a leading BOM if present (`bytes.HasPrefix(data, "\xEF\xBB\xBF")`).
  - Accept lowercase hex only (reject uppercase with a clear error — it's a toolchain mismatch and worth flagging). Optionally: lower-case on read and compare with lowercase expected.
  - Strip a leading `sha256:` prefix if present.
  - Trim whitespace, ignore comment lines starting with `#`.
  - Validate length is exactly 64 hex chars; any other length is a parse error (NOT a "match by chance" — there is no such edge case for SHA-256).
  - Refuse CR-only separators or NUL bytes inside the digest.
  - Hash the binary on disk and compare in constant time (`crypto/subtle.ConstantTimeCompare`).
- **Rationale:** CfT-style release pipelines emit a single-line `<hex>  chrome` file (sha256sum format). Operators who hand-modify the install, or release managers who use different conventions, MUST not break the verifier silently. The MD5 path's test `TestVerifyGoogHashMD5_MultipleHeaderLines()` (graphify confirmed) sets the precedent for adversarial parsers; SHA-256 needs the same coverage.

### SEC-ADR052-005 — High — TOCTOU + symlink attacks on `chrome.sha256` and the binary

- **Location:** `findInstalledBuild()` (installer.go:323-356) and the new verifier.
- **Issue:** The function uses `os.Stat` then `info.Mode()&0o111`. `os.Open` follows symlinks (unlike `Openat`+`O_NOFOLLOW`). An attacker who can write to `<installRoot>/chromium/<version>/` (e.g. via a world-writable parent, a sudo misconfig, or a misconfigured systemd `StateDirectory`) can:
  - Symlink `chrome.sha256` → a known-good digest elsewhere on disk (`/dev/null` if the implementation mishandles empty files, but more usefully a real verifier-shaped file elsewhere).
  - Symlink `chrome` → an arbitrary binary. The `info.Mode()&0o111` check follows symlinks, so the bit is checked on the target; if the target is `+x` it passes.
  - Symlink a parent directory to redirect the write into attacker territory. The current code doesn't open with `O_NOFOLLOW` or `O_DIRECTORY`.
  - Replace the binary between the `os.Stat` and the `os.Open` for hashing (TOCTOU). The hash reads via `os.Open`+`io.Copy`, so the time-of-check/time-of-use is small but non-zero.
- **Recommendation:**
  - **`O_NOFOLLOW`** on every `os.OpenFile` and `os.Stat`-equivalent that targets `chrome` or `chrome.sha256`. Use `unix.Openat`+`O_NOFOLLOW`+`O_PATH` on Linux (`golang.org/x/sys/unix`) so a symlink at any path component is detected.
  - **`O_DIRECTORY`** + reject non-directory revalidation of install-root intermediates (defends against parent-dir hijinks).
  - **Stat the resolved path with `EvalSymlinks` first**, then `os.Lstat` on `chrome` to detect symlinks at the leaf (the resolved path's inode must match the lstat we just did; mismatches are a symlink).
  - **Lock the install root** for the duration of the verify+exec, OR re-stat and re-hash immediately before `exec.Cmd.Start()`. The simplest defense is: hash, exec, child inherits the file descriptor (open fd is immune to rename, but NOT to a hard-link swap on the same inode — Linux semantics depend on mount).
  - **Document the assumption** that `<installRoot>` is root-owned and 0755 on a system-wide install (per ADR §3 D4 systemd `StateDirectory`); the resolver should refuse to launch from a world-writable install root with `fmt.Errorf("install root %s has unsafe mode %v", root, info.Mode())` and a single audit entry `WARN-CFTSHA-002`.
- **Rationale:** ADR §1 (C2) acknowledges "minimal server" installs. A world-writable `~/.omnipus/` on a multi-user host, or a hand-extracted tarball by a non-root operator, both create this exposure. The CfT download path already lives in the same vulnerability class; Phase 1 doesn't add a new one, but neither does it close the existing one.

### SEC-ADR052-006 — High — `os.Executable()` symlink semantics + Windows installer-root computation are unspecified

- **Location:** `pkg/tools/browser/exec_resolver.go:49-51` (`InstallRootForProfileDir`, currently `ProfileDir`-relative); ADR §3 D2 specifies `filepath.Join(filepath.Dir(os.Executable()), "..", "chromium")` (Linux/macOS).
- **Issue:** The ADR says M5: "package root computed at runtime via `os.Executable()`" — eliminating the `-X` ldflag. Two open questions:
  - `os.Executable()` on Linux returns the **resolved** path of `/proc/self/exe` (a symlink to the actual binary). On macOS it returns the resolved path via `_NSGetExecutablePath`. On Windows, it returns the path as Windows computed it; `os.Executable` does NOT call `GetFinalPathNameByHandle` (per `os/exec.go` and Go source) — so a copy of `omnipus.exe` left via a hardlink or symlink on Windows yields the path of the link, not the original. The ADR claims "no behavior difference" between OSes for this computation; that is not the case.
  - Operator who installs via `cp omnipus /usr/local/bin/omnipus` gets `/usr/local/bin/omnipus`. Package install puts it at `/usr/bin/omnipus` (`.goreleaser.yaml:111` `bindir: /usr/bin`). Docker/Linux packagers differ. `os.Executable()` returns these correctly, but the **package's `chromium/` sibling** is only guaranteed to exist at the package install dir, not at `/usr/local/bin/`. The ADR's `filepath.Dir(os.Executable())` resolves to the install location iff `chromium/` is a sibling of the binary — which holds for `bindir: /usr/bin/...` but not for `cp` flows.
- **Recommendation:**
  - On Linux/macOS: keep `filepath.Dir(os.Executable())` as the primary path, then walk up one (`..`) for the `chromium/` sibling. Fall back to `InstallRootForProfileDir(cfg.ProfileDir)` (existing) if the sibling doesn't exist or is unwritable. This is exactly what the ADR says — but require the resolver to **probe both**, not blindly pick the new path. The existing `ProfileDir`-relative computation IS still the system-install default; the ADR's `os.Executable` is for "not installed via package" flows. Make that explicit in the doc.
  - On Windows: get the module path via `os.Executable()`, then resolve symlinks via `GetFinalPathNameByHandle` (Windows API; pulls in no CGo if we wrap a tiny `golang.org/x/sys/windows` call) before computing the sibling. If the resolved location doesn't contain `chromium/`, fall back to `%LOCALAPPDATA%\omnipus\chromium\`. Document the fallback in the ADR.
  - Validate the install root: `os.Lstat`, check `!info.IsDir()` returns false (must be a directory), check `info.Mode()&0o002 == 0` (not world-writable) — same recommendation as SEC-ADR052-005. Phase 1 must add this validation on every cold-start.
- **Rationale:** "Runtime resolution via `os.Executable()`" is brittle and platform-dependent. Phrasing it as "primary + ProfileDir fallback" makes the implementation handle `cp`, `make install`, package-manager, and `go build && ./omnipus` flows without manual operator configuration. Without the validation, the package-install story loses its integrity guarantee to whoever can write the chromium sibling.

### SEC-ADR052-007 — Medium — `doctor WARN-BROWSER-005` runs `ldd` via `os/exec`; fails HC #2

- **Location:** `cmd/omnipus/internal/doctor/command.go` (new check in Phase 1, code absent).
- **Issue:** ADR §1 names `ldd` as the WARN-BROWSER-005 mechanism for detecting missing host shared libs on minimal Linux. `ldd` is a shell script wrapper around the dynamic loader — running it from a Go program via `os/exec` requires `ldd` on `$PATH`, can produce false negatives on stripped or `LD_PRELOAD`-manipulated binaries, and is exactly the "shell out for security-related diagnostic" pattern that HC #2 ("Pure Go — no CGo, no external C libs, no shelling out for security-critical paths") rules out. Also:
  - `ldd` is not present on a minimal Alpine-without-gcompat, busybox, or Yocto host (it requires `glibc`'s dynamic loader, ironically the same library the binary depends on).
  - `ldd`'s output format changes between glibc versions and is locale-sensitive.
  - On a non-ELF binary (wrong arch, partial download), `ldd` returns "not a dynamic executable" — useful signal, but shelling out for it is overkill.
- **Recommendation:** Implement WARN-BROWSER-005 as **in-process ELF parsing**, not `ldd`:
  - Read the file header (`magic == "\x7fELF"`).
  - Walk the program headers for `PT_DYNAMIC` → `DT_NEEDED` entries.
  - For each soname, **canonicalize** via `os.Stat("/lib/<soname>")`, `os.Stat("/usr/lib/<soname>")`, then `os.Stat("/lib64/<soname>")`, then `os.Stat("/usr/lib64/<soname>")` (or use `golang.org/x/sys/unix` `dlopen`/the dynamic loader's own search path: which adds CGo — NOT acceptable; use the static list).
  - Flag missing libs in the warning, naming each `lib*.so`.
  - This implementation is pure Go, deterministic, and works even without `ldd` on the host.
  - Build tag the implementation: `//go:build linux`. On non-Linux, the check returns nil with a debug log "WARN-BROWSER-005 skipped: platform != linux" — same as `doctor`'s existing behavior on filtered checks.
- **Rationale:** Doctor's promise is "offline/read-only ... never launches a browser, probes $PATH, or hits the network" (`cmd/omnipus/internal/doctor/command.go:117-119`). Shelling out to `ldd` arguably violates that — even more so on a minimal server where `ldd` itself is missing. The in-process approach is strictly safer and addresses C2 (host shared libs "not papered over") on the same path the integrity verifier walks.

### SEC-ADR052-008 — Medium — Hard-coded CfT host-shared-libs list may be incomplete for current CfT builds

- **Location:** ADR §1 ("Scope note on 'prerequisite'") enumerates 15 libraries: `libnss3`, `libnspr4`, `libatk1.0-0`, `libatk-bridge2.0-0`, `libcups2`, `libdrm2`, `libgbm1`, `libxkbcommon0`, `libxcomposite1`, `libxdamage1`, `libxrandr2`, `libxshmfence1`, `libasound2`, `libpango-1.0-0`, `libcairo2`.
- **Issue:** These match the Chrome ≥~85+ "debian_depends" list but Chrome 130+ has added: `libxcb-dri3-0` (always-on since Cairo/DMABUF use it), `libwayland-client0` (for Ozone/Wayland support, optional), `libgles2` (for newer GL contexts in headless), `libxss1` (still required). `install.sh` needs to install all of them on minimal servers; the WARN-BROWSER-005 in-process ELF parser (SEC-ADR052-007) will catch missing ones dynamically regardless of the install list — so the lib list drives `install.sh` and `apt`/`dnf` mappings, not the verifier. The risk is that the lib list drifts as CfT bumps.
- **Recommendation:**
  - Generate the install list from CfT's own published `last-known-good-versions-with-downloads.json` if CfT ever adds a `runtime_deps` field; today it does not. Until then, **hard-code a "known-good for the last 4 CfT stable releases" set** plus a one-line comment "regenerate per Chrome release." Document the regeneration step in `scripts/install.sh`.
  - Pair the static list with `apt-get install -y --no-install-recommends $(cat debian-deps.txt)` and `dnf install -y $(cat fedora-deps.txt)`; tie these to the SHA-pinned package set so they're not silent drift.
  - **Primary defense is WARN-BROWSER-005 (in-process)**, not the install list — the verifier must catch any missing lib and bubble that up regardless of what `install.sh` promised. Make this explicit in the ADR.
- **Rationale:** If a future CfT version adds a `libxkbfile1` and the install list is stale, minimal-server installs silently degrade to a Chrome that crashes mid-launch with no doctor pre-flight. The in-process ELF check turns this from "silent failure on first launch" into "doctor surfaces the missing lib before first launch."

### SEC-ADR052-009 — Low — Chrome's own signed-binary self-check interactions unstated

- **Location:** ADR §2 (C3 macOS code-signing sub-choices); also relevant to Windows Phase 4.
- **Issue:** The ADR is silent on Chrome-for-Testing's own signing state. CfT binaries are signed by Google with Google's Developer ID; SHA-256 verification by us is **additional** to Google's signature, not a replacement. The two interact in two edge cases:
  - **Re-signing** (macOS C3 option (i)): stripping Google's Developer ID and re-signing with Omnipus's ID *can* trip Chrome's `--disable-breakpad`/Updater URL checks; embed `disable-signin-screens` etc. carefully. None of this is Chrome "rejecting" the binary; it's Chrome phoning home to Google's update servers (which is fine; the package doesn't carry an updater).
  - **Repackaging into `.deb`/`.rpm`**: Debian and RPM packaging tools do not modify the binary's content — they wrap and compress. SHA-256 round-trips cleanly. This is the easy path.
- **Recommendation:** Add a one-paragraph note in the ADR or in the Phase 1 README: "Chrome-for-Testing is signed by Google; our SHA-256 check verifies the binary matches the package-build-time copy, not Google's signature. Repackaging into .deb/.rpm does not modify the binary content; SHA-256 is preserved end-to-end." This preempts the obvious reviewer question and signals to the Windows Phase 4 (named-pipe) and macOS Phase 3 (notarization) implementers that Google's signing remains the upstream trust anchor.
- **Rationale:** Reviewers and operators WILL ask "what if you ship a Chrome Google didn't bless?" — the answer is "we DID get it from Google (CfT releases via storage.googleapis.com), we signed the *package*, and the binary's content is unchanged; our SHA-256 is a redundant inline check that catches in-transit corruption and tarball re-extraction mishaps." That answer needs to be in the ADR.

---

## Out-of-scope items (flagged for Phase 3/4)

1. **Darwin `otool -L` and Windows `dumpbin /dependents`** equivalents for WARN-BROWSER-005 on those platforms. Phase 1 is Linux-only; the in-process ELF parser (SEC-ADR052-007) leaves a clean seam for Mach-O and PE parsers in Phase 3/4.
2. **macOS notarization (C3)** — three sub-options still open; defer per ADR.
3. **Windows Service-account ACL on the install dir** — relevant to SEC-ADR052-005's recommendation but the user/permission model is `.msi`-specific.
4. **Auto-update story (ADR §7)** — `install.sh` is "re-runnable"; shadow updates via package manager are out of scope but the new chrome.sha256 should be re-checked on every install (the existing install.sh model handles this for the binary; Chrome inside the package carries its own verifier in `findInstalledBuild`).
5. **Hard Constraint #1 lean binary** — `go:embed` Chrome is correctly rejected (ADR §2). The chosen direction preserves this.

---

## Verification suggestions

Each finding ships with a test case the Phase 1 implementation agent can use as an objective definition-of-done:

### SEC-ADR052-001 — `verifyChromeSHA256` missing-file failure mode
- **Test:** `TestVerifyChromeSHA256_MissingManifest_Refuses` — `findInstalledBuild` returns the binary path with an empty `chrome.sha256`; expect `verifyChromeSHA256` to return `errManifestMissing` and `findInstalledBuild` itself to bubble that up, NOT return the path.
- **Test:** `TestVerifyChromeSHA256_PermissionDenied_Refuses` — `chrome.sha256` exists with mode 0000; expect refusal.
- **Test:** `TestVerifyChromeSHA256_PresentAndMatching_Accepts` — golden path.

### SEC-ADR052-002 — Resolution-order trust model
- **Test:** `TestResolve_ChromeOnPath_Wins_TriggersWARN` — Create a fake `google-chrome` script earlier on `$PATH`; with default config, expect the resolved path to be the $PATH one AND a `WARN-BROWSER-007` emitted (or refusal, depending on chosen option).
- **Test:** `TestResolve_PreferPackagedTrue_OverridesPath` — Same setup, with `prefer_packaged: true`; expect the package-bundled Chrome to win.

### SEC-ADR052-003 — goreleaser post-step
- **Test:** CI step that runs the goreleaser dry-run (`goreleaser release --snapshot --skip-publish --clean`) and asserts that `dist/omnipus_Linux_x86_64.tar.gz` contains a top-level `chromium/chrome` AND `chromium/chrome.sha256` with matching contents.

### SEC-ADR052-004 — SHA-256 parser hardening
- **Test:** table-driven `TestParseSHA256Manifest` with: BOM-prefixed file, CRLF line endings, leading `# comment` lines, `sha256: <hex>` prefix, uppercase hex (reject), wrong length (reject), trailing whitespace (accept), two-line format `<hex> *chrome` (accept), binary content after newline (reject), empty file (reject).
- **Test:** `TestVerifyChromeSHA256_ConstantTimeComparison` — confirm `crypto/subtle.ConstantTimeCompare` is used (or fail with a lint error).

### SEC-ADR052-005 — TOCTOU + symlinks
- **Test:** `TestFindInstalledBuild_SymlinkedBinary_Refused` — symlink the binary to `/bin/true`; expect refusal.
- **Test:** `TestFindInstalledBuild_SymlinkedManifest_Refused` — symlink `chrome.sha256` to a known-good digest in another location; expect refusal.
- **Test:** `TestFindInstalledBuild_WorldWritableInstallRoot_Refused` — chmod 0777 the install root; expect refusal.

### SEC-ADR052-006 — `os.Executable()` semantics
- **Test:** `TestResolvePkgChrome_BinaryInUsrBin` — symlink the test binary to `/tmp/fakebin/omnipus` with a sibling `chromium/chrome`; expect the sibling resolution to win.
- **Test:** `TestResolvePkgChrome_BinaryInLocalBin_FallsBack` — symlink to `/tmp/fakebin/omnipus` with no sibling; expect `InstallRootForProfileDir(cfg.ProfileDir)` to be used.

### SEC-ADR052-007 — In-process ELF parsing
- **Test:** `TestWARN_BROWSER_005_StubbedBinary_NoMissingLibs` — create a fake ELF with `DT_NEEDED` entries pointing to libraries on the host; expect no warning.
- **Test:** `TestWARN_BROWSER_005_LibNotFound_SurfacesName` — same setup but point one `DT_NEEDED` to a non-existent soname; expect the warning to name the missing library.
- **Test:** `TestWARN_BROWSER_005_NonELF_SurfacesDiagnostic` — point at a script or a Windows binary; expect a "not an ELF binary" message.
- **Test:** Run on `//go:build linux` only; on darwin/windows the test file should be empty.

### SEC-ADR052-008 — Host-shared-libs list coverage
- **Test:** Static check on the install list — diff against current CfT's published runtime deps (if ever published); OR include at minimum the libs in `chrome.sha256`'s metadata for the current release.

### SEC-ADR052-009 — Chrome self-check note
- **Test:** Documentation-only finding; the test is "the ADR or README explicitly addresses Google's signature + repackaging invariants."

---

## Summary

| ID | Severity | One-liner |
|---|---|---|
| SEC-ADR052-001 | Blocker | Missing `chrome.sha256` must refuse, not accept (fail-open) |
| SEC-ADR052-002 | Blocker | `PreferPackaged:false` default + no SHA on `$PATH` Chrome = trusted RCE-engine origin |
| SEC-ADR052-003 | Blocker | goreleaser post-step to emit `chrome.sha256` is not specified or stubbed |
| SEC-ADR052-004 | High | SHA-256 parser hardening needed for BOM/CRLF/uppercase/prefix/comment-line edge cases |
| SEC-ADR052-005 | High | Symlink + TOCTOU on `chrome.sha256` and the binary require `O_NOFOLLOW` + install-root ownership checks |
| SEC-ADR052-006 | High | `os.Executable()`-based install-root needs explicit Linux/macOS/Windows branching and unsafe-mode gate |
| SEC-ADR052-007 | Medium | `ldd` via `os/exec` violates HC #2; use in-process ELF parsing |
| SEC-ADR052-008 | Medium | CfT host-shared-libs list needs a regeneration hook + in-process verifier as primary defense |
| SEC-ADR052-009 | Low | ADR is silent on Chrome's own signing vs our SHA-256 — one paragraph closes it |

**Recommended blocking-criteria for Phase 1 sign-off (suggested, not mandated by this review):**
- `findInstalledBuild()` returns empty when `chrome.sha256` is missing or unreadable.
- WARN-BROWSER-005 uses in-process ELF parsing (no `os/exec`).
- WARN-BROWSER-006 surfaces SHA-256 verification failures clearly.
- `PreferPackaged: true` is the **default**, with `$PATH` Chrome as an opt-in override (or `$PATH` Chrome is gated by `trust_path_chrome: false` default).
- `.goreleaser.yaml` carries the post-step that emits `chromium/chrome.sha256`; `scripts/install.sh` extends `checksums.txt` verification to cover it.
- All `os.OpenFile` calls on the install root use `O_NOFOLLOW`; the resolver validates directory ownership/mode.

The integrity surface is the right **shape** — SHA-256, doctor warnings, fallback ladders, gate-by-gate — but four of the design points need a tweak before implementation: fail-open on missing manifest, the goreleaser post-step that produces it, the resolution-order security model, and in-process ELF parsing. The rest are hardening.

---

*End of review.*
