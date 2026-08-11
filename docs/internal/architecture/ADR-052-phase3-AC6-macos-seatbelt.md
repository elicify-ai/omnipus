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

`sandbox-exec -p <profile>` instead of `-f <file>`. A profile file is read by `sandbox-exec` *after* we write it; anyone able to replace that path in the window between write and exec chooses the sandbox policy — a total bypass. Inline passing closes the window and leaves no policy artifact on disk. The tradeoff is that the profile is visible in `ps`; it contains workspace paths and ports (no secrets), which already appear in the child's own argv. Profiles measure ~1 KB against a 1 MB `ARG_MAX`.

## 5. Architecture caveat (open)

AC-6 was validated on **darwin/amd64** (Intel). ADR-052 Phase 3 targets **darwin/arm64**, and CI's `macos-latest` is arm64. Seatbelt profile semantics are architecture-independent and nothing in the implementation is arch-specific, so the risk is low — but this is **not** an arm64 sign-off. The empirical constants (cryptex cache path, `/private/var/select`, the mDNSResponder socket) should be re-confirmed once an arm64 host or runner is available before AC-6 is called complete for the Phase 3 target platform.

## 6. Coverage

- `seatbelt_profile_test.go` — renderer, preamble invariants, symlink resolution (platform-independent, runs on Linux CI).
- `seatbelt_integration_darwin_test.go` — real children: workspace RW, out-of-policy denial, network default-deny **with an allow-listed-port control**, the `applyPlatformHardening` production seam.
- `seatbelt_adversarial_darwin_test.go` — the attack table in §3.

Darwin-gated files skip cleanly on Linux; the renderer tests run everywhere.
