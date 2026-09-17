# pkg/sandbox — kernel confinement seam

Landlock+seccomp on Linux, Seatbelt on macOS, application-level fallback
everywhere else. The security boundary of the whole binary lives here.

## Running tests here

Scope to one symbol; the package probes kernels and spawns real children
(`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestSandboxBackend_SelectBackend$' ./pkg/sandbox/`). CI is the authority
for full-suite results.

## Platform posture — what each OS actually gets

| Platform | Kernel sandbox | Gateway process | Spawned children |
|---|---|---|---|
| Linux 5.13+ | Landlock + seccomp (`sandbox_linux.go::LinuxBackend`) | confined (restrict_self at boot) | confined by inheritance |
| macOS | Seatbelt via `/usr/bin/sandbox-exec` (`backend_darwin_seatbelt.go`) | **NOT confined** | confined per-child |
| Windows (any `!linux && !darwin` build) | **none** — `sandbox_other.go::selectBackendPlatform` returns FallbackBackend | not confined | not confined |

- **Windows has NO sandbox backend.** An earlier root CLAUDE.md claimed
  "Windows (Job Objects+Restricted Tokens+DACL)" — that was wrong. Job
  Objects are per-child process hardening in `hardened_exec_windows.go`
  (memory cap + kill-on-parent-death only; its own header: "DACL,
  Restricted Token, and AppContainer are out of scope"), not a sandbox.
  The fallback is app-level path checks plus `OMNIPUS_SANDBOX_PATHS` env
  vars (`sandbox.go::FallbackBackend::ApplyToCmd`) that only COOPERATIVE
  children honour — arbitrary binaries are not contained.
- **macOS Seatbelt confines only CHILDREN.** `sandbox-exec` can only launch
  a fresh child inside a profile; pushing an already-running process into
  one needs `sandbox_init(3)`, which is CGo and forbidden (Hard Constraint
  #2). The gateway itself runs unconfined — `backend_darwin_seatbelt.go`'s
  header documents this as an accepted limitation. Kill-switch:
  `OMNIPUS_SEATBELT_DISABLE=1`; the backend is ON by default.
- **Linux is the only platform where the gateway confines itself.**

## Two confinement models — KernelChildConfiner

`kernel_confiner.go::KernelChildConfiner` marks a backend that enforces by
wrapping each spawned child (Seatbelt implements it). `LinuxBackend`
deliberately does NOT: Landlock restricts the calling thread and children
inherit — a different model with its own mode-aware entry point
(`ApplyWithMode`). `FallbackBackend` must never implement it; capability
checks on the interface, not on backend-name strings, are what keep the
gateway from silently booting unconfined.

## Landlock ratchet and the per-thread re-apply

- `sandbox_linux.go::processLandlockApplied`: restrict_self is a
  process-global one-shot ratchet. A second Apply would fail EINVAL from
  the already-restricted task; the latching flag is why sequential test
  gateways boot at all.
- Go's M:N scheduler can fork from a worker thread that never had
  restrict_self applied — the child silently escapes. `hardened_exec.go`'s
  `Run`/`StartLocked` lock a fresh OS thread and call
  `restrictCurrentThreadIfNeeded` BEFORE forking. Callers using
  `ApplyChildHardening` directly on their own `exec.Cmd` do NOT get that
  and must spawn from an already-restricted thread.

## Per-child hardening is not the sandbox backend

`hardened_exec.go`'s package comment is the cross-platform contract:
Linux = Setpgid + Pdeathsig=SIGTERM + prlimit RLIMIT_NPROC (always) and
RLIMIT_AS (when a memory limit is set); macOS = Setpgid ONLY (XNU has no
Pdeathsig and no usable address-space limit — `MemoryLimitBytes` is
ignored, `Result.MemoryLimitUnsupported=true`); Windows = Job Object
memory cap + KILL_ON_JOB_CLOSE. RLIMIT_CPU is deliberately never used
(cpu-time ≠ wall-clock; timeouts go through context cancellation).

## Per-turn policy seam

`turn_policy.go::RegisterTurnPolicyBase`: the gateway registers the boot
half once after an ENFORCING Apply; spawn sites supply the per-turn half;
`DeriveKernelPolicy` stays the single construction site. A nil registered
base means no per-turn overlay — spawn inherits the boot profile, including
when that profile is the degraded/off path. Do not treat nil as a switch
that unconfines an already-restricted process. The `nogodmode` build tag
compiles the sandbox "off" profile out of hosted builds
(`godmode_on.go::GodModeAvailable`).
