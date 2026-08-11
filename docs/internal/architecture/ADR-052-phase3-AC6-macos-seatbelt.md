# ADR-052 Phase 3 / AC-6 — macOS Seatbelt sandbox: implementation record

- **Status:** Implemented (executes the ratified AC-6 in [ADR-052 Phase 3 goal](ADR-052-phase3-macos-goal.md) §6; no new design decision is taken here).
- **Scope:** AC-6 only — the macOS kernel sandbox. AC-1 (audio spike), AC-2/C3 (notarization), AC-3 (darwin package), AC-7 (launchd), AC-8 (doctor Mach-O) are untouched and remain open.
- **Validated on:** macOS 26.5.2 (Darwin 25.5.0), **x86_64**. See "Architecture caveat" below.

---

## 1. What changed

macOS previously ran on `FallbackBackend` (application-level enforcement only). It now runs on a real kernel sandbox:

| Before | After |
|---|---|
| `selectBackendPlatform` (`sandbox_other.go`, `!linux`) always returned `FallbackBackend` on darwin | New `sandbox_darwin.go` prefers `SeatbeltBackend`, falling back only if `sandbox-exec` is missing or the operator opts out |
| `SeatbeltBackend.Available()` returned false unless `OMNIPUS_SANDBELT_ENABLE=1` | Real capability probe: `/usr/bin/sandbox-exec` must exist and be executable |
| Nothing in the production path applied the backend to a child | `applyPlatformHardening` (darwin) wraps every hardened-exec child, fail-closed |
| Profile written to a temp `.sb` file, cleanup deferred as a TODO | Profile passed inline via `sandbox-exec -p`; no file, no cleanup, no swap window |
| Renderer emitted policy paths verbatim | Paths are resolved through symlinks before emission |

The retired `OMNIPUS_SANDBELT_ENABLE` gate is deliberately **not** honoured any more; it is replaced by `OMNIPUS_SEATBELT_DISABLE=1`, a kill-switch. The polarity flip is the point: protection must not depend on remembering to set a variable.

## 2. Findings that cost real debugging time

The renderer's original comment said the system preamble "must be validated empirically on macOS … hardcoding them from documentation would be guessing at a security boundary." That was correct, and the measurements bore it out.

1. **`(allow file-read* (literal "/"))` is mandatory.** A child must stat the root directory; no `(subpath "/usr/lib")`-style allow covers `/` itself. Without it **every** child dies on SIGABRT (exit 134) with **completely empty stderr** — dyld is killed before it can report. The symptom is indistinguishable from "Seatbelt is fundamentally broken."

2. **The dyld shared cache needs no rule at all** — it is kernel-mapped, not read through the filesystem. The documented path `/System/Library/dyld` **does not exist** on macOS 13+; the cache lives under `/System/Volumes/Preboot/Cryptexes/OS/…`. The original draft named the stale path. Guessing from docs would have added a useless rule and still failed.

3. **Seatbelt matches RESOLVED paths.** `/tmp`, `/etc` and `/var` are symlinks into `/private`. `DefaultPolicy` grants RWX on `/tmp`, so rendering it verbatim yields `(subpath "/tmp")` — a filter matching **nothing**. The profile looks right, the apply logs clean, and every write the policy explicitly permits is denied. This is the project's "mechanism not property" defect class exactly; `resolveSeatbeltPath` now resolves paths and grants traversal reads on symlinked ancestors.

4. **DNS is not UDP/53.** macOS resolves via mDNSResponder over a Unix-domain socket. A profile that allow-lists ports but not that socket resolves nothing — so every port-scoped network allow appears broken while unscoped `(allow network*)` works, which reads like wrong port-filter syntax and sends you chasing the wrong bug. `(remote tcp "*:PORT")` was correct all along; the preamble now carries the two mDNSResponder allows.

5. **`/private/var/select`** is required by `/bin/sh` (shell selector), or `sh` fails to start.

## 3. Adversarial results

Executed against real children (`seatbelt_adversarial_darwin_test.go`):

| Attack | Result |
|---|---|
| Symlink inside workspace → outside file | **Denied** |
| Symlink created by the child at runtime (race variant) | **Denied** |
| `../` traversal out of the workspace | **Denied** |
| Child manufacturing a hardlink to an out-of-policy file | **Denied** (cannot reach the target to link it) |
| Profile injection via a quote-bearing path | **Rejected at render** |
| Metacharacter paths (`(`, `)`, `;`, spaces) | Enforced correctly, no widening |
| **Pre-placed hardlink inside the workspace** | **READS SUCCEED — known gap** |

### 3.1 Known gap: pre-placed hardlinks

Seatbelt confines by path, not inode, so a hardlink inside the workspace pointing at an out-of-policy file is readable. Bounds:

- The confined child **cannot create one** (proven by test) — it cannot reach the target.
- Exploitation requires an **unconfined** actor to pre-place the link inside the agent workspace; such an actor can already write into that workspace directly.
- Hardlinks cannot cross filesystems.

This is a property of path-based MAC generally, not a defect of this implementation. It is asserted in the test suite so a behaviour change surfaces as a failure.

### 3.2 Structural limitation: the gateway process is not confined

Landlock restricts the **current thread** and children inherit the domain, so the gateway confines itself. `sandbox-exec` can only launch a **fresh child** inside a profile, and the in-process API (`sandbox_init(3)`) is C-only, which hard constraint #2 forbids.

**Consequence:** on macOS the gateway process itself is **not** Seatbelt-confined — only the children it spawns are. A compromise of the gateway is not contained the way Landlock would contain it. `Apply()` emits this at WARN on every boot rather than leaving operators to assume parity.

## 4. Why the profile is passed inline

`sandbox-exec -p <profile>` instead of `-f <file>`. A profile file is read by `sandbox-exec` *after* we write it; anyone able to replace that path in the window between write and exec chooses the sandbox policy — a total bypass. Inline passing closes the window and leaves no policy artifact on disk. The tradeoff is that the profile is visible in `ps`; it contains workspace paths and ports (no secrets), which already appear in the child's own argv. A minimal profile is ~1 KB — but a realistic gateway policy carrying the
1000-port dev-server range renders to **~95 KB**, one line per port. Both are
well under the 1 MB `ARG_MAX`, and `TestSeatbelt_ProductionScaleProfile_Spawns`
proves a child actually starts at production scale. The ~1 KB figure alone
makes the limit look irrelevant; it is the 95 KB number that matters if the
port range ever grows by an order of magnitude.

## 4a. Operator-configurable exec paths (`sandbox.allowed_exec_paths`)

The kernel policy grants execute only on the system binary directories, and
`allowed_paths` grants read+**write** and never execute — so before this there
was no config key at all that could let an agent run a toolchain. On macOS that
meant Homebrew and every version manager were unreachable, which is not a
security posture so much as a broken product: the predictable operator response
is `OMNIPUS_SEATBELT_DISABLE=1`, losing the whole boundary.

`sandbox.allowed_exec_paths` is a separate read+execute-only list, seeded per
platform as editable install-time data. Properties, each enforced rather than
documented:

- **Never writable.** `buildExecPathRules` hard-codes the access bits and takes
  no access argument. Proven at the kernel level by
  `TestSeatbelt_RealChild_ExecPathIsNotWritable`, which uses a directory the
  test user owns — so it demonstrates Seatbelt overriding Unix ownership, which
  is why granting exec on a user-owned Homebrew prefix is safe.
- **Never writable AND executable.** An entry overlapping any writable grant —
  the operator's `allowed_paths` or the unconditional `$OMNIPUS_HOME` / `/tmp` /
  `$TMPDIR` grants — is dropped with a warning; the write grant wins.
  `TestSeatbeltAdversarial_CannotDropBinaryIntoExecPath` executes all five
  planting vectors (cp / mv / ln -s / ln / redirect) and each is denied.
- **Narrow.** Homebrew is enumerated at `bin`/`lib`/`Cellar`/… rather than by
  prefix, so `/usr/local/etc` — WireGuard keys, rustup config — stays closed.
- **Linux is untouched.** The seed returns nil there. The same gap exists on
  Linux and is real, but widening the Landlock posture of every existing install
  belongs in its own change with its own review and upgrade note, not as a
  ride-along on a macOS change. Tracked as follow-up.

**Residual cost, stated plainly:** the granted toolchain trees become readable.
That is a real, bounded confidentiality loss, mitigated by narrow enumeration
and by never granting `$HOME` or a bare prefix. It is not zero.

## 4b. Permissive mode is not available on macOS

Seatbelt has no audit-only equivalent: `sandbox-exec` either applies a profile
or it does not. Installing one under `mode=permissive` would give an operator
running the documented "watch what breaks before enabling" step full hard
enforcement, with no banner and no `audit_only` flag.

So `permissive` on darwin does **not** install a kernel profile. It degrades to
application-level enforcement, prints the permissive banner, sets
`ApplyState.AuditOnly`, and says why in the operator-facing notes. Regression
test: `TestApplySandbox_Darwin_PermissiveDoesNotInstallProfile`, with
`TestApplySandbox_Darwin_EnforceInstallsProfile` as the positive control.

## 4c. Network semantics differ from Landlock for the same policy

`renderSeatbeltProfile` emits only TCP port allows under `(deny default)`. That
denies **UDP entirely** and **all Unix-domain sockets** except the mDNSResponder
literal in the preamble. Landlock ABI v4 restricts TCP bind/connect only, so UDP
and Unix sockets pass through untouched there.

Consequence: an identical `SandboxPolicy` is materially stricter on macOS. A
child that talks to a local socket (`docker.sock`, a Postgres socket) or uses
QUIC/NTP works on Linux and fails on macOS. `SandboxPolicy.ConnectPortRules` is
therefore platform-dependent in a way the type does not express.

## 5. Architecture caveat (open)

AC-6 was validated on **darwin/amd64** (Intel). ADR-052 Phase 3 targets **darwin/arm64**, and CI's `macos-latest` is arm64. Seatbelt profile semantics are architecture-independent and nothing in the implementation is arch-specific, so the risk is low — but this is **not** an arm64 sign-off. The empirical constants (cryptex cache path, `/private/var/select`, the mDNSResponder socket) should be re-confirmed once an arm64 host or runner is available before AC-6 is called complete for the Phase 3 target platform.

## 6. Coverage

- `seatbelt_profile_test.go` — renderer, preamble invariants, symlink resolution (platform-independent, runs on Linux CI).
- `seatbelt_integration_darwin_test.go` — real children: workspace RW, out-of-policy denial, network default-deny **with an allow-listed-port control**, the `applyPlatformHardening` production seam.
- `seatbelt_adversarial_darwin_test.go` — the attack table in §3.

Darwin-gated files skip cleanly on Linux; the renderer tests run everywhere.
