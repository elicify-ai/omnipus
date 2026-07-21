# ADR-052: Native cross-platform builds + per-OS Chrome-bundled installer packages (the Ollama distribution model)

- **Status:** **Proposed — 2026-07-21** (revision 2 — addresses grill pass 1, verdict BLOCK; operator: Daniel Piatkowski — ratified the direction + the four-phase sequencing across the 2026-07-21 deliberation; awaits formal sign-off).
- **Deciders:** Daniel Piatkowski (operator); architect (this record).
- **Evidence level:** 1 — codebase facts + in-session measurements, with tagged OS-level facts where noted.
- **Amends / relates to:** **ADR-047 §13** (platform support — this ADR funds the work that makes §13's "untested, not impossible" macOS row actually tested, and fixes the Windows blocker §13 names); **ADR-044 §6.0.1 item 4** (the superseded "Linux-only" rationale this ADR retires for good); **ADR-053** (Bedrock lite-build decision — carried forward here as D5; in one line: AWS Bedrock compiles into the lite build via the `bedrock` tag without pulling heavy deps, so lite stays lean).
- **Supersedes for this decision:** the standing assumption that the browser is "downloaded on first use or found on `$PATH`" as the *only* guaranteed path — the package-bundled Chrome becomes the guaranteed-available floor.

> **Revision 2 changelog (grill pass 1):** C1 — Windows work reframed as **allocator**, not dialer. C2 — "no host prerequisite" corrected: a bundled Chrome needs host shared libs on minimal Linux (and MSVC runtime on Windows Server Core). C3 — macOS `.app` notarization promoted from open-item to a named decision. M1 — resolution order keeps `$PATH` above the package Chrome (operator autonomy + this session's classifier fix preserved); adds a `prefer_packaged` toggle. M2 — bundled-Chrome integrity verification added. M3/M6 — the linux-only video gate is relaxed **only after** per-OS audio verification, not before. M4 — service-registration & privileges subsection added. M5 — package root computed at runtime via `os.Executable()`, no ldflag/per-package variant.

---

## 1. Context

Omnipus ships as a single Go binary embedding the SPA, with a managed Chrome launched for
agent browsing. Two long-standing, related problems motivate this ADR:

1. **"Properly tested only on Linux."** The managed-Chrome CDP transport speaks over inherited
   file descriptors (`cmd.ExtraFiles = []*os.File{...}` at `pkg/tools/browser/cdppipe/allocator.go:232`),
   and the Go standard library states plainly: *"ExtraFiles is not supported on Windows."*
   `[FACT — go doc os/exec.Cmd.ExtraFiles]` So on Windows the managed browser cannot launch
   *at all* today. The macOS path is closer (ExtraFiles IS supported on darwin; `coordinator_lock_unix.go`
   is `//go:build unix`, which includes darwin; `chromeHardeningBaseFlags` at `exec_resolver.go:101`
   already ships `--use-mock-keychain` beside `--password-store=basic`), but it is unexercised in CI.
   The WebRTC-video capability classifier hard-returns not-capable for any non-Linux host
   (`capability.go:72`), a gate ADR-047 §13 established is **vestigial** — it rests on the Xvfb/
   PulseAudio design (ADR-044 §6.0.1 item 4) that was never built.

2. **"The browser must always be packed in" (operator directive).** Today Chrome is resolved at
   runtime as: operator `exec_path` → system Chrome on `$PATH` → managed download into a per-profile
   install root (`InstallRootForProfileDir`, `exec_resolver.go:49`) → remote CDP. Nothing *guarantees*
   a correct, video-capable, version-pinned Chrome is present on a given install. Hosts vary;
   downloads can fail; `$PATH` Chrome can be headless-shell.

The operator's requirements (2026-07-21 deliberation): the browser must always be present and
correct on every install; no *added* runtime prerequisite (no Docker, no VM install, no Node) —
"solve it, don't move the problem down"; one coherent delivery story across Linux, macOS, Windows;
and a confirmed four-phase implementation order (§5).

> **Scope note on "prerequisite" (C2):** "no added prerequisite" means no Docker/VM/Node. It does
> **not** mean zero host libraries. A bundled full Chrome on Linux dynamically links against
> `libnss3`, `libnspr4`, `libatk1.0-0`, `libatk-bridge2.0-0`, `libcups2`, `libdrm2`, `libgbm1`,
> `libxkbcommon0`, `libxcomposite1`, `libxdamage1`, `libxrandr2`, `libxshmfence1`, `libasound2`,
> `libpango-1.0-0`, `libcairo2` (Chrome's documented runtime deps) — present on a stock desktop,
> **absent on a minimal server** (Debian slim, RHEL minimal, Alpine-without-gcompat), where the
> binary would exit `error while loading shared libraries`. On Windows Server Core the MSVC runtime
> can be absent. Only the macOS `.app` is genuinely self-contained. This is acknowledged and handled
> by `install.sh` (documented `apt`/`dnf` set) + `doctor WARN-BROWSER-005` (`ldd` on the bundled
> Chrome) — not papered over.

---

## 2. Options considered

### Distribution / runtime model

| Option | Verdict | Why |
|---|---|---|
| **Embed Chrome in the binary** (`go:embed`) | ❌ Rejected | ~416 MB extracted / 185 MB zip `[FACT — measured]`. Violates Hard Constraint #1. |
| **Bundled VM / container runtime** (Lima, OrbStack, Firecracker, "Docker without Docker") | ❌ Rejected | **Hard OS wall on Windows:** a Linux binary on Windows/macOS requires host virtualization; on Windows that means enabling Hyper-V or WSL2 (admin, one-time) — every "Docker on Windows" product does this. Bundling a VM relocates the prerequisite onto the user. `[FACT — OS-level]` |
| **Sidecar with a bridge protocol** | ❌ Rejected | Contradicts the codebase: *"no `BridgeAdapter`, no stdio bridge protocol"* (CLAUDE.md). Chrome is launched directly via CDP. |
| **Native Go binary per OS + per-OS Chrome in the installer package** | ✅ **Chosen (D1 + D2)** | Binary stays lean; each OS package carries the correct Chrome; no VM/Docker/Node. |

### Browser delivery

| Option | Verdict | Why |
|---|---|---|
| Runtime download on first use as primary | ❌ Rejected as primary | Not deterministic; fails offline; version drift. Retained as a fallback. |
| Rely on system Chrome on `$PATH` as primary | ❌ Rejected as primary | Uncontrolled flavour/version. Retained in the order (operator autonomy, M1). |
| Pinned full Chrome bundled in the per-OS package, as the guaranteed floor | ✅ **Chosen (D2)** | Deterministic, version-pinned, offline-capable, integrity-verified (M2). |

### Install mechanism

| Option | Verdict | Why |
|---|---|---|
| **npm** | ❌ Rejected | Adds a Node prerequisite; cannot carry ~400 MB through `node_modules`. |
| **Docker image as primary** | ❌ Rejected | Requires Docker on the host — the prerequisite the operator refused. (Retained as a secondary artifact.) |
| **Ollama model: native packages + `curl\|sh` + OS package-manager entries** | ✅ **Chosen (D4)** | One command on any OS; carries the Chrome payload; reuses goreleaser (`archives`, `nfpms`, `dockers_v2` — `.goreleaser.yaml:65/81/93`). |

### macOS `.app` Chrome embedding (C3 — a named decision, not an open item)

Bundling Chrome inside an Omnipus `.app` is **gated by Apple notarization**, because Chrome-for-Testing
is signed by Google with Google's Developer ID; repackaging it breaks that signature and Apple
notarization rejects unsigned / non-hardened-runtime binaries. The existing `scripts/build-macos-app.sh`
does **no** `codesign`/`xcrun notarytool` and builds the *Launcher* `.app`, not the gateway — so this
is from-scratch, not an extension. Three options:

| Option | Verdict |
|---|---|
| (i) Re-sign Chrome with the Omnipus Developer ID + Hardened Runtime (+ `disable-library-validation`) | ⚠️ Strips Google's signature; may trip Chrome's self-check; ongoing maintenance per Chrome bump. |
| (ii) Ship Chrome as a sibling signed-and-notarized helper **outside** the `.app` | ⚠️ "Bundled" in the installer, not in the `.app` — weaker UX but cleanest signing. |
| (iii) macOS = runtime-download primary (today's behavior) for Chrome, `.app` wraps only the gateway | ⚠️ Honest, but macOS alone lacks the "always packed in" guarantee. |

**Decision: defer the choice to Phase 3 (macOS)**, where the empirical audio spike (§5) runs anyway.
Until then, D2's "guaranteed floor" claim is scoped to **Linux** (and Windows once Phase 4 lands);
macOS confidence is downgraded accordingly (§8). The choice is recorded here so it is not
re-discovered late.

---

## 3. Decision

### D1 — Distribution model: native Go binary per OS, no bundled VM/container

Native Go binary for each supported OS (linux, darwin, windows) from one codebase. No embedded
Chrome; no bundled VM/container; no sidecar bridge. Hard Constraints #1 (single lean binary) and
#2 (pure Go) preserved. `[grounded: binary ~102 MB via make; Constraints #1/#2 in CLAUDE.md]`

**Confidence: High.**

### D2 — Browser delivery: pinned full Chrome bundled in the per-OS package, as the guaranteed floor

Each OS package carries a version-pinned full Chrome-for-Testing build, extracted at package-build
time. **Package root is computed at runtime** from `filepath.Join(filepath.Dir(os.Executable()), "..", "chromium")`
(Linux/macOS) / resolved via `os.Executable()` (Windows) — **no `-X` ldflag, no per-package binary
variant** (M5; the build-variance memory `omnipus-build-variance-48mb.md` warns against exactly that).

**Resolution order (revised — M1): operator intent wins; the package Chrome is the guaranteed floor,
not an override:**

```
1. operator exec_path override        (explicit, trusted — always wins)
2. system Chrome on $PATH             (operator's deliberate choice — kept ABOVE package Chrome)
3. package-managed Chrome             ← NEW: guaranteed-correct floor when nothing better is present
4. managed download (first-use)       (fallback for bare-binary / no-package installs)
5. remote CDP                         (operator-managed Chromium)
```

Keeping `$PATH` above the package Chrome (not inverting it) preserves: (a) operator autonomy — a
deliberately newer/patched `$PATH` Chrome still wins; (b) the classifier fix landed this session
(`manager.go` `cachedPath()`, commit `decb01f0`), which credits a resolved `$PATH`/system Chrome —
it stays live rather than becoming dead code. A `tools.browser.prefer_packaged` toggle (default
**false**) is added for fleets that want the pinned package Chrome to win for reproducibility.

The package Chrome kills the "is a correct Chrome even present?" class of bug (the floor is now
on-disk, not a network fetch), and `ClassifyVideoCapability`/`findInstalledBuild` learn the package
root so **video capability is guaranteed wherever the package is installed AND the host OS is
supported** (C2/m1 — supported-OS list is explicit; macOS scoped by the C3 decision).

**Integrity (M2):** the package-build pipeline fetches CfT at release time and runs `verifyGoogHashMD5`
(`installer.go:414-461`) against the live response — the same check the runtime-download path uses —
then ships a `chrome.sha256` alongside the binary. `findInstalledBuild` verifies that file at first
launch and refuses a mismatched bundled Chrome (`doctor WARN-BROWSER-006`). This makes the package
path **at least as strong** as runtime-download, not weaker.

**Confidence: High on Linux; Medium on macOS (gated by C3); Medium on Windows (gated by D3).**

### D3 — Platform support: native Windows and macOS, via OS-specific allocator/transport work

Retire "only tested on Linux." The work is **allocator-level, not dialer-level (C1):**

- **The cdppipe layer is half transport-agnostic.** `pipeconn.go` (any `io.Reader`/`io.Writer`) and
  `frame.go` (pure NUL-delimited framing) are clean and OS-independent. The **dialer** (`dialer.go:81`)
  is already an in-memory `net.Pipe()` shim — OS-independent, nothing to do there.
- **The Windows work is in the allocator** (`allocator.go:192` `os.Pipe`, `:232` `ExtraFiles`,
  `:299-351` bridge loop): a `//go:build windows` allocator variant creates the named-pipe pair and
  passes handles to Chrome via `SysProcAttr`/`AdditionalInheritedHandles`, matching Chrome's Windows
  `--remote-debugging-pipe` handle convention (which differs from POSIX fd 3/4). Bounded allocator +
  handle-inheritance work, **not** "one new dialer."
- **macOS** needs no transport change (ExtraFiles works on darwin); it needs the §5 Phase 3 empirical
  audio spike + the C3 notarization decision.

**Retiring the linux-only video gate — sequenced, not immediate (M3/M6):** `ClassifyVideoCapability`
returns not-capable off-Linux (`capability.go:72`). Per ADR-048 condition 3 ("must not advertise
Capable when capture cannot succeed") and HC #6 (no silent default), the gate is relaxed **only after**
a real per-OS run proves audio-with-video: (1) build the per-OS transport, (2) run the empirical audio
verification on that OS, (3) **only then** relax `goosForCapability != "linux"` for that OS. macOS
stays not-capable until the Phase 3 spike proves audio (ADR-047 §13.4 — headless macOS has no loopback
audio device; video-only is not Capable under ADR-047's audio-with-video requirement).

**Confidence: Medium.** macOS is lower-risk for browsing; the Windows named-pipe allocator is the one
genuinely new code item; macOS headless audio is empirically open.

### D4 — Install mechanism: the Ollama model + per-OS service registration (M4)

Native packages + a one-line install script + OS package-manager entries:

- **One-line installer:** `curl -fsSL https://omnipus.ai/install.sh | sh` — OS-detects, downloads the
  correct native archive (Chrome bundled) from a **pinned release URL with a published SHA**, installs
  to the package-owned path. (The `curl|sh` trust model is named and accepted — it is re-runnable and
  SHA-pinned, matching the Homebrew/rustup/Ollama convention.)
- **Native packages (goreleaser bones exist):** tar.gz/zip archives, `.deb`/`.rpm` via nfpm, with
  Chrome + `chrome.sha256` added to the payload; add Windows `.msi`; the macOS gateway `.app` is new
  (the existing `build-macos-app.sh` builds the Launcher).
- **Package-manager manifests:** Homebrew tap (macOS/Linux), winget + Scoop (Windows).
- **Secondary Docker image retained** for users who already run containers.

**Service registration & privileges (M4) — per OS** (Omnipus is not Ollama: it runs AES-256-GCM/Argon2id
credential management per ADR-004 and kernel sandboxing):

| OS | Service model | Privilege / secret notes |
|---|---|---|
| Linux | systemd unit | `StateDirectory=~/.omnipus`, `UMask=0077`, **stable UID** (no `DynamicUser` — `master.key` needs a stable owner), Landlock ABI compat check at boot; post-install tested across Debian/RedHat/SUSE. |
| macOS | launchd plist | `master.key` 0600 enforced by the boot contract, not the installer. |
| Windows | Windows Service under a **dedicated account (NOT LocalSystem)** | ACL the install dir; the `pkg/sandbox` Windows backend (Job Objects + Restricted Tokens) is exercised in a non-developer service context for the first time — needs its own UAT in Phase 4. |

**Confidence: High** on the mechanism; the per-OS service/privilege work is part of the sandbox thread (§5).

### D5 — Lite build: binary stays lean, Chrome is package-level

The `lite` variant excludes compiled-in capabilities (whatsmeow, WebRTC per ADR-053; Bedrock stays in
lite per ADR-053). Chrome bundling is package-level, so the lite binary stays lean and lite-vs-full is
orthogonal to Chrome-bundled-package-vs-bare-binary. A bare `make build` binary (no package) falls
through to the runtime-download fallback (resolution step 4) — the lean-binary story is preserved.

**Confidence: High.**

---

## 4. Consequences

**Positive**
- One coherent story per OS: install one package → native binary + correct Chrome → works on
  Linux/macOS/Windows with no Docker/VM/Node.
- A correct, version-pinned, integrity-verified Chrome is guaranteed wherever the package is
  installed and the OS is supported — the "which Chrome did we get?" class of bug is structurally
  prevented (floor is on-disk, not a fetch), without sacrificing operator autonomy or this session's
  classifier fix.
- Constraints preserved (#1 lean binary, #2 pure Go, #4 graceful degradation: bare-binary installs
  fall back to download; non-video-capable hosts still browse).
- "Only tested on Linux" retired — CI matrix + the per-OS transports make the cross-platform claim real.

**Negative**
- **Package size:** each installer grows by the Chrome payload (~165–190 MB compressed / ~400 MB
  extracted, varying by OS — m2). Mitigation: the binary itself is unchanged; bare-binary users pay
  nothing.
- **Host shared libraries (C2):** on minimal/server Linux (and Windows Server Core) the bundled Chrome
  needs documented system libraries; `install.sh` installs them and `doctor WARN-BROWSER-005` checks.
- **macOS notarization (C3):** the macOS "always packed in" guarantee is gated by a Phase-3 signing
  decision; macOS may end up the one OS where Chrome is runtime-downloaded.
- **Windows named-pipe allocator (C1):** bounded but genuinely new code with its own test surface.
- **Per-OS service/privilege + sandbox work (M4):** the Windows sandbox backend running as a Service is
  a configuration it has never run in.

**Neutral**
- The runtime-download path is retained as a fallback, not deleted.

---

## 5. Sequencing (operator-confirmed, 2026-07-21) + cross-cutting sandbox thread

**The four phases (build the delivery machinery first, then validate/expand OS by OS):**

| Phase | Focus | "Done" | Lands grill items |
|---|---|---|---|
| **1 — Bundles + installer** | Delivery machinery **regardless of which OS builds are ready**: goreleaser carries pinned Chrome + `chrome.sha256` in archive/deb/rpm at the package root; `install.sh` (OS-detect, SHA-pinned, installs host libs); resolver + classifier learn the package root (`os.Executable()`); `doctor` gains `WARN-BROWSER-005` (ldd) + `WARN-BROWSER-006` (Chrome hash). | A Linux package installs and resolves the bundled Chrome with no first-use download. | C2, M2, M5 |
| **2 — Linux E2E** | Validate the whole stack through the new installer on the tested platform: browsing **and** WebRTC video, agent loop, channels, **sandbox (Landlock+seccomp)**, credentials. | Linux package install → `localhost` → full agent + live video, deterministically. | M1 (resolution order keeps `$PATH` above package Chrome; classifier fix stays live) |
| **3 — macOS + audio** | The empirical spike ADR-047 §13.4 flagged: does `chrome.tabCapture` yield **audio** under `--headless` on darwin? Plus the **C3 notarization** decision and the macOS sandbox (Seatbelt/sandbox_exec) + launchd service model. | darwin/arm64 build, real audio-verified capture — **or** a documented "macOS = browsing-only, video deferred" with the gate staying not-capable. | C3, M3 (macOS not-capable until audio proven) |
| **4 — Windows** | The **named-pipe allocator** (`//go:build windows`) replacing `os.Pipe`+`ExtraFiles`, matching Chrome's Windows `--remote-debugging-pipe` handle convention; the Windows sandbox backend (Job Objects + Restricted Tokens) exercised **as a Service** under a dedicated account; `.msi` packaging. | Native Windows install → browsing works; video follows only after its own audio verification. | C1 (allocator work), M4 (service/privilege) |

**Cross-cutting thread — sandbox & kernel isolation across OSes (M4):** this runs across **all** phases,
not inside one:
- **Linux (Phase 2):** Landlock+seccomp already works; harden/verify under the new bundle; confirm ABI
  compat check at boot.
- **macOS (Phase 3):** Seatbelt/sandbox-exec needs real implementation + testing (no kernel-sandbox
  backend exists for darwin today beyond app-level).
- **Windows (Phase 4):** Job Objects + Restricted Tokens, now running as a Windows Service under a
  dedicated account (not LocalSystem) — a configuration the backend has never run in; needs its own UAT.
- **Credentials thread (all phases):** `master.key` ownership/enforcement under systemd (stable UID) /
  launchd / a dedicated Windows service account.

---

## 6. Relationships to other ADRs

- **ADR-047 §13:** this ADR is its execution plan — converts "macOS untested" into "macOS tested"
  (Phase 3) and fixes the Windows blocker §13 names (Phase 4).
- **ADR-044 §6.0.1 item 4:** the superseded "Linux-only" rationale is retired for good once the gate is
  relaxed per-OS *after* verification (D3, Phase 3/4) — no vestige of the dead premise remains.
- **ADR-053:** D5 carries its lite-build decision (Bedrock stays in lite) forward, orthogonal to Chrome bundling.

---

## 7. Open items (genuine, not blockers)

- **Pinned Chrome version + cadence:** which CfT channel (Stable) and how often to bump at release
  time; whether the gateway refuses to start if the bundled Chrome is >N versions behind Stable.
- **macOS C3 sub-choice (i)/(ii)/(iii):** decided in Phase 3 alongside the audio spike.
- **Windows Service account shape:** dedicated account provisioning in the `.msi` (Phase 4).
- **Auto-update story:** out of scope here; `install.sh` is re-runnable and package managers handle
  updates for their users.
- **ADR-048 condition-3 update:** relaxing the linux-only gate per-OS may need a one-line note in
  ADR-048 confirming the per-OS-capable classifier still honors "must not advertise Capable when
  capture cannot succeed."

## 8. Confidence summary

| Decision | Confidence | Notes |
|---|---|---|
| D1 native binary, no VM/container | High | only no-added-prerequisite option on every OS |
| D2 per-OS bundled Chrome (floor) | **High on Linux; Medium on macOS (C3); Medium on Windows (D3)** | downgraded from rev1's blanket "High" |
| D3 native Windows + macOS | **Medium** | macOS audio empirically open; Windows allocator is the one new code item |
| D4 Ollama install model + per-OS service | High on mechanism; service/privilege work rides the sandbox thread | M4 |
| D5 lite stays lean, Chrome package-level | High | orthogonal by construction |
