# Feature Specification: Sandbox Apply/Install Wiring at Gateway Boot

**Created**: 2026-04-20
**Status**: Draft (ambiguities resolved 2026-04-20)
**Input**: GitHub issue #76 — "Landlock Apply() and seccomp Install() are never called at runtime"
**Branch**: `sprint-j-security-hardening`
**Depends on**: `docs/internal/plan/wave2-security-layer-spec.md` (Sandbox backends, SEC-01/02/03)

---

## 1. Context

### Problem Statement

The Omnipus agentic core advertises kernel-level sandboxing (Landlock + seccomp, SEC-01/02/03) but the parent gateway process never actually calls `SandboxBackend.Apply()` or `SeccompProgram.Install()` at boot. Children spawned via `ApplyToCmd()` are sandboxed, but the gateway itself — the long-lived process that holds credentials, config, and agent state — runs unconfined.

The sandbox package itself is aware of the gap: `pkg/sandbox/sandbox.go:392-395` explicitly logs a status note when queried:

> "sandbox backend is capable of kernel-level enforcement but Apply() has not been called on the Omnipus process; child processes are not currently restricted by Landlock or seccomp"

### Intended Outcome

After this sprint:
1. On Linux ≥ 5.13 with `gateway.sandbox.mode = enforce`, the gateway calls `Apply()` and then `Install()` once during boot, BEFORE serving requests.
2. On unsupported kernels/OSes, the gateway selects `FallbackBackend`, logs the degradation, and continues (graceful fallback per CLAUDE.md Hard Constraint #4).
3. A CLI override (`--sandbox=enforce|permissive|off`) and config key (`gateway.sandbox.mode`) exist for dev debugging and audit-only rollouts.
4. A **permissive** mode ships in Sprint J: policy is computed and audit-logged but violations are not enforced (seccomp uses `SECCOMP_RET_LOG`; Landlock is either skipped or uses permissive semantics depending on kernel). Intended for production dry-run and audit before flipping to enforce.
5. The status endpoint reports `sandbox.applied = true` post-boot (instead of the current warning note).
6. Failures during apply/install on a kernel that claims support are **fatal** — the gateway fails closed rather than silently serving with no sandbox.
7. **Sandbox config is process-global and applied once at boot.** Changes to `sandbox.*` require a full gateway restart; there is no hot-reload path in Sprint J.
8. **Seccomp is strictly gated on LinuxBackend being selected.** If Landlock falls back, seccomp is NOT installed either — both-or-neither, never seccomp-alone.

---

## 2. Phase 1 — Discovery & Requirements (Condensed)

| Dimension | Summary |
|-----------|---------|
| **Primary actor** | Operator booting the Omnipus gateway (`./omnipus gateway`) |
| **Secondary actor** | Developer running the gateway locally with a debugger attached |
| **Problem scope** | Parent-process sandbox enforcement only. Child-process sandboxing (`ApplyToCmd`) already works and is out of scope. |
| **Constraints** | CLAUDE.md: pure Go, ≤10MB RAM, graceful degradation, deny-by-default for security. No new runtime dependencies. |
| **Integration surface** | `pkg/gateway/gateway.go` boot path, `pkg/agent/loop.go` backend selection, `pkg/sandbox/*` enforcement, `pkg/config/sandbox.go` config, gateway CLI flags. |
| **Non-scope** | Windows Job Objects (stays as FallbackBackend on Windows until a later sprint); rewriting existing `pkg/sandbox` tests; changing the `SandboxBackend` interface. |

---

## 3. Phase 1.5 — Existing Codebase Context

### Symbols Involved

| Symbol | File:Line | Role |
|--------|-----------|------|
| `SandboxBackend` interface | `pkg/sandbox/sandbox.go:40-46` | Defines `Apply()` (parent) and `ApplyToCmd()` (children). No change needed. |
| `SelectBackend()` | `pkg/sandbox/sandbox.go:402-404` | Returns highest-capability backend available. Never fails. |
| `LinuxBackend.Apply()` | `pkg/sandbox/sandbox_linux.go:126` | Kernel Landlock enforcement on parent. Already implemented and tested. |
| `LinuxBackend.PolicyApplied()` | `pkg/sandbox/sandbox_linux.go:121-123` | Reports whether Apply() ran. Reuse; no change. |
| `SeccompProgram.Install()` | `pkg/sandbox/seccomp_linux.go:56` | Loads BPF filter into kernel. Already implemented. Called in tests only. |
| `BuildSeccompProgram()` | `pkg/sandbox/seccomp_linux.go:118` (approx) | Assembles the BPF program. Reuse; no change. |
| `FallbackBackend` | `pkg/sandbox/sandbox_fallback.go` | No-op enforcement; `Apply()` returns nil. |
| `SandboxStatus{}.PolicyApplied` | `pkg/sandbox/sandbox.go:386-396` | Already surfaces the gap in status output. Should flip to true after wiring. |
| `NewAgentLoop()` | `pkg/agent/loop.go:327-331` | Selects backend but does not `Apply()` it. |
| `Gateway.Start()` (implicit) | `pkg/gateway/gateway.go:334` | Constructs `AgentLoop`; should also orchestrate apply/install. |
| `OmnipusSandboxConfig.Enabled` | `pkg/config/sandbox.go:28-31` | Existing feature toggle; repurpose for the "on/off" decision. |
| `computeWorkspacePolicy` | **NEW** — to be added | Builds a `SandboxPolicy` with `$OMNIPUS_HOME/**` writable, minimal system paths readable. |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents (notable) |
|-----------------|------------|----------------|--------------------------|
| Gateway boot path | HIGH | `cmd/omnipus/main.go`, integration tests in `pkg/gateway/rest_*_test.go` | All E2E flows (onboarding, chat, agents) |
| `NewAgentLoop` | LOW | 18 call sites (mostly `pkg/gateway/*_test.go`; see list below) | Test-only — production caller is `gateway.go:334` |
| `SandboxBackend` interface | NONE (unchanged) | All backends, all `ApplyToCmd` callers | n/a |
| `LinuxBackend.Apply()` | LOW | sprint-j integration test (new); existing subprocess test at `tests/security/sandbox_enforcement_linux_test.go:175` | n/a |
| `OmnipusSandboxConfig` | LOW | config load/save; doctor command | Any future sandbox config UI |

**d=1 callers of `NewAgentLoop`:** production call at `pkg/gateway/gateway.go:334`; 18 test call sites in `pkg/gateway/rest_test.go`, `rest_auth_test.go`, `reload_rollback_test.go`, `credential_redaction_test.go` — all unaffected if wiring happens at `Gateway.Start()` (outside `NewAgentLoop`).

### Relevant Execution Flows

| Process / Flow | Relevance |
|----------------|-----------|
| Gateway boot (`cmd/omnipus gateway` → `gateway.Start()`) | Primary integration point. Apply/install must run BEFORE the HTTP listener binds. |
| Subprocess spawn via `pkg/tools/shell.go:237` → `ApplyToCmd()` | Already sandboxes children. Parent-sandbox change must not double-apply on children. |
| Status endpoint `/api/system/sandbox/status` | Must flip `policy_applied: true` after wiring. Existing note string must disappear. |
| `omnipus doctor` | Should read the same status and no longer warn about the gap. |

### Cluster Placement

This feature sits in the **security-runtime** cluster (pkg/sandbox + pkg/gateway boot wiring). It spans `pkg/sandbox` (no code change, only new caller) and `pkg/gateway` (boot orchestration) — but the architectural surface area is narrow: one new call site, one new policy-builder helper, one CLI flag.

---

## 4. Phase 1.7 — Available Reference Patterns

**N/A** — nothing in `docs/reference/` specifically relates to gateway-boot-ordering for sandbox enforcement. The closest references are:

- Wave 2 security spec (`docs/internal/plan/wave2-security-layer-spec.md`) — defines *what* Apply/Install do, not *where* to call them.
- Credential boot contract (`docs/internal/architecture/ADR-004-credential-boot-contract.md`) — ordering-of-operations pattern for boot-time security. This spec follows the same "fail closed on error" discipline.

No external patterns reused. Implementation follows existing in-tree conventions.

---

## 5. Phase 2 — User Stories & Acceptance Criteria

### US-1 — Kernel sandbox applied to gateway at boot (Priority: P0)

As an **operator deploying Omnipus on a Linux 5.13+ server**, I want the kernel-level sandbox applied to the main gateway process at boot, so that the runtime actually matches the README's security claims and a compromised gateway cannot read `/etc/passwd` or write outside `$OMNIPUS_HOME`.

**Why this priority**: The current code ships a security feature in label only. SEC-01/02/03 are P0 requirements in the BRD. Shipping without wiring is a user-visible misrepresentation of the security posture.

**Independent Test**: Boot the gateway on Linux 6.x with `mode=enforce`, `curl` the status endpoint, assert `sandbox.policy_applied == true`; from within the gateway process, attempt to write `/etc/passwd` and observe `EACCES`.

**Acceptance Scenarios**:

1. **Given** a Linux 6.x host with `gateway.sandbox.mode = enforce`, **When** the gateway starts, **Then** `LinuxBackend.Apply()` and `SeccompProgram.Install()` are invoked exactly once before the HTTP listener binds, and a structured log line `sandbox.applied backend=linux mode=enforce landlock_abi=3 seccomp_syscalls=N` is emitted.
2. **Given** a running gateway with the sandbox applied, **When** a request for `/api/system/sandbox/status` arrives, **Then** the response has `policy_applied: true`, `seccomp_enabled: true`, `mode: "enforce"`, and the `notes` array does NOT include the "Apply() has not been called" string.
3. **Given** a running gateway with the sandbox applied, **When** any code path in the gateway process attempts to `open("/etc/passwd", O_WRONLY)`, **Then** the syscall returns `EACCES` (Landlock denial) and no write occurs.
4. **Given** a Linux 5.10 host (pre-Landlock), **When** the gateway starts with `gateway.sandbox.mode = enforce`, **Then** `SelectBackend()` returns `FallbackBackend`, Apply is a no-op, seccomp is NOT installed, and a `sandbox.degraded reason=kernel_too_old` log line is emitted at WARN level; the gateway continues to serve requests.
5. **Given** a Linux 6.x host where the Landlock `Apply()` call returns an unexpected error, **When** `gateway.sandbox.mode = enforce`, **Then** the gateway logs `sandbox.apply_failed` at ERROR, aborts boot with exit code 78 (EX_CONFIG), and does NOT bind the HTTP listener.
6. **Given** a Linux 6.x host during the boot window AFTER `selectBackend()` but BEFORE `net.Listen`, **When** an external client attempts `GET /health` on the configured port, **Then** the client receives a TCP-level "connection refused" (ECONNREFUSED), NOT an HTTP 503 — because the listener is not bound yet. (Assertion: the listener bind is strictly last in the boot sequence.)

### US-2 — Dev override to disable sandbox (Priority: P1)

As a **developer running the gateway locally**, I want to override sandboxing via `--sandbox=off` (CLI) or `gateway.sandbox.mode = off` (config), so I can attach `delve`, `strace`, or `perf` without fighting kernel enforcement, and so I can bisect whether a bug is sandbox-related.

**Why this priority**: Without this override, developers cannot attach a debugger (ptrace is blocked by seccomp; filesystem probes are blocked by Landlock). That friction would cause developers to patch-and-revert the sandbox, which is worse than having an explicit knob.

**Independent Test**: Boot gateway with `--sandbox=off`; `curl` status; assert `policy_applied == false` and `disabled_by: "cli_flag"` in notes. Attach `delve attach <pid>` and confirm it works.

**Acceptance Scenarios**:

1. **Given** a Linux 6.x host, **When** the gateway starts with `--sandbox=off`, **Then** Apply/Install are not called, the status endpoint shows `policy_applied: false, mode: "off"` with `disabled_by: cli_flag`, and a WARN log `sandbox.disabled reason=cli_flag` is emitted.
2. **Given** a Linux 6.x host with `gateway.sandbox.mode = off` in config, **When** the gateway starts with no CLI flag, **Then** the same "disabled" path is taken and `disabled_by: config`.
3. **Given** both `--sandbox=off` CLI and `mode = enforce` config, **When** the gateway starts, **Then** the CLI flag wins (CLI > config precedence) and sandbox is disabled.
4. **Given** `mode = off` AND the host is in production (ENV `OMNIPUS_ENV=production`), **When** the gateway starts, **Then** the WARN log includes `WARN: sandbox disabled in production environment` on stderr so operators notice the misconfiguration in journald/Docker logs. **There is no suppression flag** — operators must either enable the sandbox or accept the banner.

---

### US-3 — Permissive mode for pre-enforcement audit (Priority: P1)

As an **operator rolling out sandboxing to an existing deployment**, I want a `permissive` mode that computes the full sandbox policy and logs every violation without actually blocking the call, so I can review audit logs for unexpected denies before flipping to `enforce` and breaking the service.

**Why this priority**: Flipping from no-sandbox to enforce in production is high-risk — any syscall or path access we forgot to whitelist becomes an outage. Permissive mode is the standard audit-then-enforce rollout pattern for LSMs (SELinux calls this "permissive"; AppArmor calls it "complain").

**Independent Test**: Boot gateway with `mode=permissive`; trigger an operation that would normally violate policy (e.g., a tool that reads `/etc/hosts` if hosts is not whitelisted); verify audit log entry exists AND the read returned bytes (not EACCES).

**Acceptance Scenarios**:

1. **Given** a Linux 6.x host with `gateway.sandbox.mode = permissive`, **When** the gateway starts, **Then** a structured log line `sandbox.permissive backend=linux mode=permissive landlock_abi=3 seccomp_syscalls=N` is emitted AND a multi-line banner is printed on stderr: `"SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION."` (banner repeats every 60s like the production-off nag).
2. **Given** a running gateway in permissive mode, **When** the gateway process attempts `os.WriteFile("/etc/passwd", ...)`, **Then** the write succeeds at the filesystem level (ENOENT/EACCES from normal Linux DAC is still possible — permissive does not grant extra rights, it only lets policy-violating calls through) OR if permissions would otherwise allow it, the write goes through AND an audit-log entry `sandbox.violation path=/etc/passwd action=write mode=permissive enforced=false` is written.
3. **Given** a running gateway in permissive mode, **When** a request for `/api/system/sandbox/status` arrives, **Then** the response has `mode: "permissive"`, `policy_applied: true`, `seccomp_enabled: true`, AND `violations_last_hour: N` reflecting the audit-log count.
4. **Given** a kernel that lacks native permissive-Landlock support (currently all kernels ≤ 6.11; may change in 6.12+), **When** `mode=permissive` is selected, **Then** the implementation SKIPS `landlock_restrict_self` but still builds and logs the policy, AND seccomp is installed with `SECCOMP_RET_LOG` instead of `SECCOMP_RET_ERRNO`; the status endpoint reports `mode: "permissive", landlock_enforced: false, seccomp_enforced: false, audit_only: true`.

---

## 6. Phase 2.5 — Behavioral Contract, Non-Behaviors, Integration Boundaries

### Primary flow (When → Then contract)

- **When** the gateway boots on a capable Linux kernel AND mode=enforce, **Then** `SelectBackend()` → `computeWorkspacePolicy()` → `backend.Apply(policy)` → `seccompProgram.Install()` → status flips to `applied=true` → **then** HTTP listener binds (strict ordering — listener bind is LAST).
- **When** the gateway boots on a capable Linux kernel AND mode=permissive, **Then** same flow except: Landlock uses permissive semantics (or skips `restrict_self` on kernels < 6.12) and seccomp is built with `SECCOMP_RET_LOG` instead of `SECCOMP_RET_ERRNO`. HTTP listener still binds last.
- **When** `SelectBackend()` returns `FallbackBackend` (pre-5.13 kernel or non-Linux build), **Then** seccomp is **NOT** installed (binary gate: seccomp-without-Landlock is never a valid intermediate state). Boot continues; status reports `policy_applied=false`.
- **When** boot completes in enforce mode, **Then** the gateway process can read `$OMNIPUS_HOME/**`, `/proc/self/**`, `/sys/devices/system/cpu/**`, `/etc/ssl/**`, `/lib{,64}/**`, `/usr/lib{,64}/**`; can write ONLY `$OMNIPUS_HOME/**` and `/tmp/**`; cannot read `/proc/<other-pid>/**`, cannot write `/etc`, `/var`, `/root`, `/home/*/.ssh`, `/sys/firmware`.
- **When** a child process is spawned via `exec.Cmd + ApplyToCmd`, **Then** the child inherits the parent's Landlock restrictions (Landlock is inherited across fork/exec by kernel design) AND the explicit child policy from `ApplyToCmd` (both apply — the tighter of the two wins).
- **When** a client attempts `GET /health` during the ~10ms window AFTER `Apply()`/`Install()` start AND BEFORE `net.Listen` returns, **Then** the TCP connection is refused at the OS level (ECONNREFUSED) because no socket is listening. The gateway never serves an HTTP 503 for sandbox-in-progress — the listener is simply not bound yet.

### Error flows

- **When** `Apply()` returns an error on a kernel that reports Landlock availability, **Then** the gateway treats it as fatal: ERROR log, exit code 78 (EX_CONFIG), no HTTP listener. (`cmd/omnipus/main.go` currently uses only exit 1 generically; Sprint J introduces 78 as the sandbox-specific failure code and documents it in the CLI help.)
- **When** `Install()` returns an error after `Apply()` succeeded, **Then** the gateway treats it as fatal: ERROR log, exit code 78, no HTTP listener. (The gateway is already partially locked down by Landlock; continuing would leave a half-sandboxed process, which is worse than failing.)
- **When** the config file declares a path in `AllowedPaths` that doesn't exist, **Then** Apply logs a WARN for that rule (path skipped) but continues if at least one rule succeeds. Consistent with existing `LinuxBackend.Apply` ruleErrors handling.
- **When** all rule-adds fail, **Then** Apply returns an aggregate error → fatal boot abort (per above).
- **When** a user-declared `AllowedPaths` entry overlaps a system-restricted path (`/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root`, or any child), **Then** the rule is added with **read-only** access (user intent respected); write access to the overlap is unconditionally stripped by `computeWorkspacePolicy`; a WARN is logged: `"User sandbox policy allows read on restricted system path /etc/ca-certificates; write access is still denied."`

### Boundary conditions

- **When** `Apply()` is called a second time in the same process, **Then** the second call is a safe no-op (returns nil, does not re-invoke syscalls, does not error). Enforced via `LinuxBackend.policyApplied` flag guard.
- **When** boot occurs inside an unprivileged container without `CAP_SYS_ADMIN`, **Then** Landlock still works (it's designed for unprivileged use) but seccomp also works without caps; no behavioral change from the non-container case.
- **When** boot occurs on kernel exactly 5.13.0, **Then** Landlock ABI v1 is negotiated, only the v1 access-rights subset is applied, and a log note `landlock_abi=1 reduced_feature_set=true` is emitted.

### Explicit Non-Behaviors

- The system MUST NOT silently succeed with the sandbox effectively disabled on Linux ≥ 5.13 when the operator asked for enforcement (`mode = enforce`) — fail closed.
- The system MUST NOT crash on pre-5.13 kernels — fall back gracefully (Hard Constraint #4).
- The system MUST NOT re-apply `Apply()` within the same process — the second call is a no-op. (Landlock rulesets can be stacked, but that's a deliberate tightening. In boot flow it would be a bug.)
- The system MUST NOT emit the "Apply() has not been called" note in `/api/system/sandbox/status` after successful wiring.
- The system MUST NOT write the sandbox policy rules to disk as a side effect of boot — policy is computed in memory from config.
- The system MUST NOT require `root` to apply the sandbox (Landlock + seccomp both work unprivileged).
- The system MUST NOT apply the sandbox BEFORE credential unlock (ADR-004). Credential file reads (`$OMNIPUS_HOME/credentials.json`) and the Argon2id KDF must complete first. Ordering is strictly: unlock → load config → select backend → Apply → Install → `net.Listen` → accept connections.
- The system MUST NOT install seccomp when `FallbackBackend` is selected. Seccomp is gated on LinuxBackend.Apply() having been called successfully — both-or-neither, never seccomp-alone.
- The system MUST NOT accept hot-reload of sandbox config. `sandbox.*` keys are process-global and applied once at boot; any change requires a full restart. Attempts to mutate at runtime are logged and rejected.
- The system MUST NOT grant write access to system-restricted paths (`/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root`) via `AllowedPaths`. User-supplied rules that target these paths are coerced to read-only and WARN-logged.
- The system MUST NOT offer a suppress-the-production-nag-banner flag. Operators who run `mode=off` in production accept the banner noise or fix the config.
- The system MUST NOT serve HTTP 503 during the Apply→Install→bind window. The TCP listener is bound strictly last, so clients receive ECONNREFUSED until boot completes — no partially-initialised HTTP responses.

### Integration Boundaries

#### Linux Kernel (Landlock LSM)
- **Data in**: `SandboxPolicy{FilesystemRules, InheritToChildren}`
- **Data out**: errno on failure; no return value on success.
- **Contract**: `landlock_create_ruleset` → `landlock_add_rule × N` → `prctl(PR_SET_NO_NEW_PRIVS)` → `landlock_restrict_self`. Per-thread; TSYNC via seccomp later.
- **On failure**: Aggregate errors, return from `Apply()`. Gateway treats as fatal.
- **Development**: Real kernel (Linux 6.8 in dev env); FallbackBackend in unit tests.

#### Linux Kernel (Seccomp-BPF)
- **Data in**: BPF program array built by `BuildSeccompProgram()`.
- **Data out**: kernel returns errno on failure.
- **Contract**: `prctl(PR_SET_NO_NEW_PRIVS)` (already set by Landlock, idempotent) → `seccomp(SECCOMP_SET_MODE_FILTER, TSYNC, prog)`.
- **On failure**: Fatal (same as Apply).
- **Development**: Real kernel.

#### Config System (Wave 1)
- **Data in**: `config.Sandbox.Mode` (new enum: `enforce|permissive|off`), `config.Sandbox.AllowedPaths`. The legacy `config.Sandbox.Enabled` bool is preserved for backwards compatibility: when present and `Mode` is empty, `Enabled=true` maps to `Mode=enforce` and `Enabled=false` maps to `Mode=off`. New installs write `Mode` only.
- **Data out**: N/A.
- **Contract**: JSON under `sandbox` key. Missing = default (`enforce` on Linux ≥ 5.13, `off` elsewhere).
- **Hot-reload**: NOT supported. Sandbox config is read once at boot; runtime changes require restart.

#### CLI (`cmd/omnipus gateway`)
- **Data in**: `--sandbox=off|enforce|permissive` flag (new).
- **Data out**: N/A.
- **Contract**: CLI > config > default. Invalid value (e.g., `--sandbox=of`) exits with code 2 (usage error) before boot.

#### Exit Codes (`cmd/omnipus gateway`)
- **Existing convention**: `cmd/omnipus/main.go` uses `os.Exit(1)` generically; no richer exit-code scheme is established.
- **Sprint J addition**: exit code **78** (`EX_CONFIG` from `sysexits.h`) is introduced for the specific case of sandbox apply/install failure on a capable kernel. This is documented in CLI help output and in the README operator section. Other existing failure paths continue to use 1.

---

## 7. Phase 3 — BDD Scenarios

### Feature: Sandbox Apply at Gateway Boot

#### Scenario: Fresh boot applies Landlock and seccomp on Linux ≥ 5.13

**Traces to**: US-1, Acceptance Scenarios 1 & 2
**Category**: Happy Path

- **Given** a Linux host reporting kernel 6.8 with Landlock ABI 3 available
- **And** `$OMNIPUS_HOME/config.json` has `gateway.sandbox.mode = "enforce"`
- **And** no `--sandbox` CLI flag is passed
- **When** the operator runs `./omnipus gateway`
- **Then** the gateway emits a structured log entry `event=sandbox.applied backend=linux mode=enforce landlock_abi=3 seccomp_syscalls=N`
- **And** the HTTP listener binds after the log entry is flushed
- **And** `GET /api/system/sandbox/status` returns `{"policy_applied": true, "seccomp_enabled": true, "mode": "enforce", "notes": []}`
- **But** the response must NOT contain the string "Apply() has not been called"

---

#### Scenario: Seccomp Install runs after Landlock Apply, not before

**Traces to**: US-1, Acceptance Scenario 1
**Category**: Happy Path (ordering assertion)

- **Given** a Linux 6.x host with `gateway.sandbox.mode = "enforce"`
- **When** the gateway boots and traces are captured via strace-equivalent logging
- **Then** the `landlock_restrict_self` syscall appears in the trace BEFORE the `seccomp(SECCOMP_SET_MODE_FILTER, ...)` syscall
- **And** both appear BEFORE any `bind()` or `listen()` syscall on the HTTP socket
- **But** if the order is reversed (Install first, Apply second), the test fails — because seccomp can block the `landlock_*` syscalls needed to configure the policy

> **Ordering rationale**: Seccomp is more-aggressive than Landlock (it filters ALL syscalls including `landlock_*`). If Install runs first, Apply's Landlock syscalls may be blocked. Apply must run first.

---

#### Scenario: Pre-Landlock kernel falls back gracefully

**Traces to**: US-1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a Linux 5.10 host where Landlock is not compiled into the kernel
- **And** `gateway.sandbox.mode = "enforce"` in config
- **When** the gateway boots
- **Then** `SelectBackend()` returns `FallbackBackend`
- **And** the gateway emits `event=sandbox.degraded reason=kernel_too_old selected_backend=fallback level=WARN`
- **And** `seccomp(SECCOMP_SET_MODE_FILTER, ...)` is NOT invoked (verified by absence in syscall trace — seccomp is strictly gated on LinuxBackend selection)
- **And** the HTTP listener binds normally
- **And** `GET /api/system/sandbox/status` returns `{"policy_applied": false, "seccomp_enabled": false, "backend_name": "fallback", "notes": ["kernel does not support Landlock; falling back to application-level enforcement"]}`

---

#### Scenario: Non-Linux build selects fallback without attempting syscalls

**Traces to**: US-1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a Darwin or Windows build of the gateway binary
- **And** any sandbox config
- **When** the gateway boots
- **Then** `SelectBackend()` returns `FallbackBackend`
- **And** no Linux-specific syscalls are attempted (verified by absence of `unix.Syscall` calls on the platform)
- **And** `GET /api/system/sandbox/status` returns `{"policy_applied": false, "backend_name": "fallback"}`
- **But** the gateway must boot successfully — this is a hard-constraint graceful degradation path

---

#### Scenario: Workspace policy permits $OMNIPUS_HOME writes, denies /etc

**Traces to**: US-1, Acceptance Scenario 3
**Category**: Happy Path (post-apply write probe)

- **Given** a booted gateway with sandbox applied on Linux 6.x
- **When** the gateway process attempts `os.WriteFile("$OMNIPUS_HOME/scratch/probe.txt", "ok", 0o600)`
- **Then** the write succeeds
- **And** when it attempts `os.WriteFile("/etc/passwd", "...", 0o600)`, the call returns `syscall.EACCES`
- **And** when it attempts `os.ReadFile("/proc/1/environ")`, the call returns `syscall.EACCES` (pid 1 is another process)
- **And** when it attempts `os.ReadFile("/sys/firmware/dmi/tables/smbios_entry_point")`, the call returns `syscall.EACCES`

---

#### Scenario: Dev override via CLI flag disables sandbox

**Traces to**: US-2, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** a Linux 6.x host with `gateway.sandbox.mode = "enforce"` in config
- **When** the operator runs `./omnipus gateway --sandbox=off`
- **Then** neither `Apply()` nor `Install()` is invoked
- **And** a WARN log `event=sandbox.disabled reason=cli_flag` is emitted
- **And** `GET /api/system/sandbox/status` returns `{"policy_applied": false, "mode": "off", "disabled_by": "cli_flag"}`
- **And** a subsequent attempt to attach `delve attach <pid>` succeeds (ptrace not blocked)

---

#### Scenario: Repeated Apply() within the same process is a safe no-op

**Traces to**: US-1, Acceptance Scenario 1 (boundary)
**Category**: Edge Case

- **Given** a gateway process where `Apply()` has already returned nil
- **When** some code path (e.g., misconfigured hot-reload handler) invokes `Apply()` a second time
- **Then** the second call returns nil immediately without invoking `landlock_create_ruleset` again
- **And** an INFO log `event=sandbox.apply.skipped reason=already_applied` is emitted
- **And** the effective policy is unchanged (no tightening, no widening)

---

#### Scenario: Apply() kernel error fails the boot closed

**Traces to**: US-1, Acceptance Scenario 5
**Category**: Error Path

- **Given** a Linux 6.x host where `gateway.sandbox.mode = "enforce"`
- **And** a (simulated) kernel condition where `landlock_create_ruleset` returns `EINVAL`
- **When** the gateway boots
- **Then** `Apply()` returns a wrapped error
- **And** the gateway emits `event=sandbox.apply_failed error="landlock: create_ruleset failed: EINVAL" level=ERROR`
- **And** the process exits with code 78 (`EX_CONFIG`)
- **And** the HTTP listener is NEVER bound — no port is opened
- **But** the gateway must NOT silently fall through to the fallback backend when the operator explicitly asked for enforcement

---

#### Scenario: Production environment with sandbox disabled logs prominent warning

**Traces to**: US-2, Acceptance Scenario 4
**Category**: Error Path (misconfiguration)

- **Given** a host where `OMNIPUS_ENV=production` is set in the environment
- **And** `gateway.sandbox.mode = "off"` in config
- **When** the gateway boots
- **Then** a multi-line WARN banner is emitted to stderr containing the strings "SANDBOX DISABLED", "PRODUCTION", and "this is not the deny-by-default posture"
- **And** the banner is repeated once every 60 seconds while the gateway runs, tagged `event=sandbox.disabled.nag`
- **But** there is NO config-based or env-based suppression knob — the banner cannot be silenced without enabling the sandbox

---

#### Scenario: /health during Apply→Install window returns TCP connection-refused

**Traces to**: US-1, Acceptance Scenario 6
**Category**: Edge Case (boot-window behaviour)

- **Given** a Linux 6.x host with `gateway.sandbox.mode = "enforce"`
- **And** a test harness polls `GET http://localhost:<port>/health` every 1ms from process start
- **When** the polling harness sends a request during the window between `SelectBackend()` returning and `net.Listen()` returning on the HTTP port
- **Then** the TCP client receives `ECONNREFUSED` at the socket layer (no HTTP response body, no 503 status)
- **And** NO log entry is emitted by the gateway for these in-flight requests (the listener does not yet exist to receive them)
- **But** once `net.Listen()` returns and `Accept` begins, subsequent `/health` requests return HTTP 200 within the normal latency SLA

---

#### Scenario: Permissive mode logs policy violations without enforcing them

**Traces to**: US-3, Acceptance Scenarios 1 & 2
**Category**: Alternate Path

- **Given** a Linux 6.x host with `gateway.sandbox.mode = "permissive"`
- **And** the workspace policy would normally deny `/etc/passwd` write
- **When** the gateway boots
- **Then** the gateway emits `event=sandbox.permissive backend=linux mode=permissive landlock_abi=3 seccomp_syscalls=N`
- **And** a multi-line WARN banner `"SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION."` is emitted on stderr
- **When** a test probe inside the gateway process attempts `os.WriteFile("$OMNIPUS_HOME/violations/probe.txt", ...)` (allowed) and then `syscall.Open("/etc/passwd", O_WRONLY, 0)` (would be denied under enforce)
- **Then** the allowed write succeeds as normal
- **And** the `/etc/passwd` open call succeeds at the kernel level (permissive does not grant rights, but it does not deny either — the call proceeds to Linux DAC, which will reject for non-root reasons)
- **And** an audit-log entry `event=sandbox.violation path=/etc/passwd action=write mode=permissive enforced=false` is written to `$OMNIPUS_HOME/system/audit.jsonl`
- **And** the banner repeats every 60 seconds with `event=sandbox.permissive.nag`

---

#### Scenario: Permissive mode on pre-6.12 kernel downgrades to audit-only

**Traces to**: US-3, Acceptance Scenario 4
**Category**: Alternate Path (kernel-capability-dependent)

- **Given** a Linux 6.8 kernel (lacks native permissive-Landlock support introduced in hypothetical 6.12)
- **And** `gateway.sandbox.mode = "permissive"`
- **When** the gateway boots
- **Then** `landlock_restrict_self` is NOT invoked (because there is no permissive Landlock semantic on this kernel)
- **And** seccomp IS installed with `SECCOMP_RET_LOG` action instead of `SECCOMP_RET_ERRNO`
- **And** the status endpoint returns `{"mode": "permissive", "policy_applied": true, "landlock_enforced": false, "seccomp_enforced": false, "audit_only": true}`
- **And** a structured log `event=sandbox.permissive.downgraded reason=kernel_lacks_permissive_landlock kernel_version=6.8` is emitted once at boot

---

#### Scenario: AllowedPaths overlap with /etc allows read, denies write

**Traces to**: US-1, Acceptance Scenario 3 (policy computation)
**Category**: Edge Case (user intent vs system restriction)

- **Given** `$OMNIPUS_HOME/config.json` has `sandbox.allowed_paths = ["/etc/ca-certificates"]`
- **And** `gateway.sandbox.mode = "enforce"` on a Linux 6.x host
- **When** the gateway boots
- **Then** a WARN log is emitted: `"User sandbox policy allows read on restricted system path /etc/ca-certificates; write access is still denied."`
- **And** the computed `SandboxPolicy.FilesystemRules` contains exactly one rule for `/etc/ca-certificates` with `Access = AccessRead` (no write bit)
- **When** the gateway process attempts `os.ReadFile("/etc/ca-certificates/ca-bundle.crt")`
- **Then** the read succeeds (user rule grants read)
- **When** the gateway process attempts `os.WriteFile("/etc/ca-certificates/foo.pem", ...)`
- **Then** the call returns `syscall.EACCES` (write unconditionally stripped for system-restricted paths)

---

#### Scenario: Seccomp is NOT installed when FallbackBackend is selected

**Traces to**: US-1, Acceptance Scenario 4 (seccomp-gate assertion)
**Category**: Alternate Path

- **Given** a Linux 5.10 host where `SelectBackend()` returns `FallbackBackend`
- **And** `gateway.sandbox.mode = "enforce"` (operator asked for enforcement; kernel cannot provide it)
- **When** the gateway boots
- **Then** `BuildSeccompProgram()` is NOT invoked
- **And** `seccomp(SECCOMP_SET_MODE_FILTER, ...)` does NOT appear in the syscall trace
- **And** the status endpoint returns `{"seccomp_enabled": false, "backend_name": "fallback"}`
- **But** the gateway boots successfully (graceful fallback, Hard Constraint #4) — seccomp-alone is not a valid intermediate state; the binary rule is "Landlock+seccomp together or neither"

---

## 8. Phase 4 — TDD Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | `computeWorkspacePolicy`, CLI flag parsing, mode precedence | Isolated logic, no syscalls |
| Integration | `sandbox.Apply` called via a boot-harness against a real kernel | Verify syscalls fire in the right order |
| E2E | Full gateway boot + status endpoint + filesystem probe | End-to-end operator experience |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestComputeWorkspacePolicy_DefaultRules` | Unit | Workspace policy permits $OMNIPUS_HOME | Asserts the generated policy contains expected read/write/exec rights on canonical paths. |
| 2 | `TestComputeWorkspacePolicy_AppendsAllowedPaths` | Unit | Workspace policy permits $OMNIPUS_HOME | Given `AllowedPaths=["/opt/shared"]`, assert it appears in the rule set with Read access. |
| 3 | `TestComputeWorkspacePolicy_SystemPathOverlapReadOnly` | Unit | AllowedPaths overlap with /etc allows read, denies write | Given `AllowedPaths=["/etc/ca-certificates"]`, assert rule's Access bit is Read-only (no Write). WARN log emitted. |
| 4 | `TestSandboxMode_CLIBeatsConfig` | Unit | Dev override via CLI flag | `--sandbox=off` with `mode=enforce` config resolves to mode=off. |
| 5 | `TestSandboxMode_ConfigDefault` | Unit | Dev override via CLI flag | No CLI flag + `mode=off` config → mode=off with `disabled_by=config`. |
| 6 | `TestSandboxMode_LegacyEnabledFieldMapping` | Unit | (Behavioral contract — Config Integration) | Legacy `Enabled=true` with empty `Mode` maps to `Mode=enforce`; `Enabled=false` → `Mode=off`. |
| 7 | `TestSandboxMode_InvalidCLIValueExits2` | Unit | Holdout E2 (usage error) | `--sandbox=of` typo triggers exit code 2 before any boot logic. |
| 8 | `TestSandboxMode_ProductionNagBanner` | Unit | Production with sandbox disabled warns | `OMNIPUS_ENV=production` + mode=off emits the multi-line banner on stderr. |
| 9 | `TestSandboxMode_PermissiveNagBanner` | Unit | Permissive mode logs policy violations without enforcing them | `mode=permissive` emits the "NOT ENFORCED — DO NOT USE IN PRODUCTION" banner on stderr. |
| 10 | `TestApplyIsIdempotent` | Unit (Linux-only, build tag) | Repeated Apply() is no-op | Two consecutive `backend.Apply(policy)` calls return nil; `PolicyApplied()` remains true. |
| 11 | `TestFallbackApply_IsNoOp` | Unit | Non-Linux build selects fallback | `FallbackBackend.Apply()` returns nil without side effects. |
| 12 | `TestSeccompInstallOrderAfterLandlock` | Integration (Linux 5.13+) | Seccomp Install runs after Landlock Apply | Boot harness spawns subprocess that calls Apply then Install; verify via eBPF trace the syscall order. |
| 13 | `TestSeccompNotInstalledOnFallback` | Integration | Seccomp is NOT installed when FallbackBackend is selected | Pre-5.13 kernel (or forced fallback via env): assert `BuildSeccompProgram` never called, `SYS_SECCOMP` absent from trace. |
| 14 | `TestGatewayBoot_AppliesSandboxOnCapableKernel` | Integration | Fresh boot applies Landlock and seccomp | Spawn gateway as subprocess, poll `/api/system/sandbox/status`, assert `policy_applied=true, mode=enforce`. |
| 15 | `TestGatewayBoot_PermissiveModeLogsViolations` | Integration (Linux 5.13+) | Permissive mode logs policy violations without enforcing them | Boot with `mode=permissive`, probe a would-be-denied path, assert audit-log entry with `enforced=false`. |
| 16 | `TestGatewayBoot_PermissiveDowngradesOnOldKernel` | Integration (kernel-version-gated) | Permissive mode on pre-6.12 kernel downgrades to audit-only | On Linux 6.8, assert `landlock_restrict_self` not called, seccomp installed with RET_LOG. |
| 17 | `TestGatewayBoot_FailsClosedOnApplyError` | Integration | Apply() kernel error fails boot closed | Inject mock backend that returns error from Apply; assert gateway exits 78 and never binds port. |
| 18 | `TestGatewayBoot_DegradesOnPre5_13Kernel` | Integration (skipped unless `SANDBOX_TEST_KERNEL=5.10`) | Pre-Landlock kernel falls back | Simulated via build tag / env flag; asserts fallback backend selected. |
| 19 | `TestGatewayBoot_CLIFlagDisablesSandbox` | Integration | Dev override via CLI flag | Spawn gateway with `--sandbox=off`; assert `policy_applied=false` and `disabled_by=cli_flag`. |
| 20 | `TestGatewayBoot_HealthDuringWindowIsECONNREFUSED` | Integration (Linux) | /health during Apply→Install window returns TCP connection-refused | Start gateway in subprocess with controlled delay in Apply; concurrent poller asserts ECONNREFUSED (not HTTP 503) until `net.Listen` returns. |
| 21 | `TestGatewayBoot_NoHotReloadOfSandboxConfig` | Integration | (Non-Behavior: hot-reload rejected) | Start gateway, mutate `sandbox.mode` in config on disk, send SIGHUP (or reload endpoint); assert rejection log and no runtime change. |
| 22 | `TestGatewayBoot_WorkspaceWriteAllowed` | E2E (Linux) | Workspace policy permits $OMNIPUS_HOME | Post-boot, invoke a test-only tool that writes `$OMNIPUS_HOME/scratch/probe.txt`; assert success. |
| 23 | `TestGatewayBoot_EtcPasswdDenied` | E2E (Linux) | Workspace policy denies /etc/passwd | Post-boot, invoke test tool that attempts write to `/etc/passwd`; assert EACCES returned via tool output. |
| 24 | `TestGatewayBoot_EtcCaCertsReadOkWriteDenied` | E2E (Linux) | AllowedPaths overlap with /etc allows read, denies write | With config listing `/etc/ca-certificates`, read succeeds, write returns EACCES. |
| 25 | `TestGatewayBoot_ProcOtherPidDenied` | E2E (Linux) | Workspace policy denies /proc | Post-boot, read `/proc/1/environ`; assert EACCES. |
| 26 | `TestSandboxStatus_NoteRemovedAfterWiring` | E2E | Fresh boot applies Landlock and seccomp (AS-2) | Post-boot, `GET /api/system/sandbox/status`, assert `notes` does not contain "Apply() has not been called". |

### Test Datasets

#### Dataset A: SandboxPolicy Rule Computation

| # | Input (config) | Boundary Type | Expected Output | Traces to | Notes |
|---|---------------|---------------|-----------------|-----------|-------|
| 1 | `AllowedPaths=[]` | Empty | Policy contains default rules only (home, /tmp, /proc/self, /lib, /usr, /etc/ssl) | Workspace policy permits $OMNIPUS_HOME | Baseline |
| 2 | `AllowedPaths=["/opt/shared"]` | Single item (non-system) | Default rules + `{Path:"/opt/shared", Access:Read}`; no WARN | Workspace policy permits $OMNIPUS_HOME | User extension |
| 3 | `AllowedPaths=["/etc/ca-certificates"]` | Overlap with /etc | Rule added with `Access=Read` only (Write stripped); WARN logged: "read on restricted system path" | AllowedPaths overlap with /etc allows read, denies write | Decision #3 — user-read wins |
| 4 | `AllowedPaths=["/proc/cpuinfo"]` | Overlap with /proc | Rule added as Read-only; WARN | AllowedPaths overlap with /etc allows read, denies write | System-restricted list: /etc, /proc, /sys, /dev, /boot, /root |
| 5 | `AllowedPaths=["/sys/class/net"]` | Overlap with /sys | Rule added as Read-only; WARN | AllowedPaths overlap with /etc allows read, denies write | Same enforcement rule |
| 6 | `AllowedPaths=["/etc"]` (whole dir) | Overlap with entire /etc | Rule added as Read-only for /etc; WARN; all writes to /etc* denied | AllowedPaths overlap with /etc allows read, denies write | Coarse-grained user intent |
| 7 | `AllowedPaths=["/nonexistent/path"]` | Missing file | Rule add fails inside `Apply()`, aggregated into ruleErrors; boot continues if other rules succeed | Apply() kernel error (partial) | Consistent with existing ruleErrors semantics |
| 8 | `AllowedPaths=[""]` | Empty string | Rejected at config-validation layer (not reached) | N/A | Config validation |
| 9 | `AllowedPaths=["../../../etc"]` | Traversal | Cleaned via `filepath.Clean`; resolves to `/etc`; overlap-with-system warning emitted; Read-only rule | AllowedPaths overlap with /etc allows read, denies write | Path sanitisation + Decision #3 |
| 10 | `AllowedPaths=["/root/.ssh"]` | Overlap with /root | Rule added as Read-only; WARN; writes denied | AllowedPaths overlap with /etc allows read, denies write | /root treated identically |

#### Dataset B: Kernel & OS Axis × Fresh-Install vs Existing-Creds × Mode

| # | OS | Kernel | Install State | Sandbox Config | Expected Outcome | Expected Boot | Traces to |
|---|-----|--------|---------------|----------------|-----------------|---------------|-----------|
| 1 | Linux | 6.8 | fresh | `mode=enforce` | LinuxBackend enforce | `policy_applied=true, mode=enforce` | Fresh boot applies Landlock (HP) |
| 2 | Linux | 6.8 | existing credentials.json | `mode=enforce` | LinuxBackend enforce | `policy_applied=true`; unlock succeeds (sandbox allows $OMNIPUS_HOME) | Workspace policy permits $OMNIPUS_HOME |
| 3 | Linux | 5.13.0 | fresh | `mode=enforce` | LinuxBackend enforce (ABI v1) | `policy_applied=true, landlock_abi=1` | Fresh boot applies Landlock (HP) |
| 4 | Linux | 5.10 | fresh | `mode=enforce` | Fallback; seccomp NOT installed | `policy_applied=false, degraded, seccomp_enabled=false` | Pre-Landlock fallback; Seccomp NOT installed when FallbackBackend |
| 5 | Linux | 6.8 | fresh | `mode=off` | Disabled | `policy_applied=false, disabled_by=config` | Dev override via CLI flag (AS-2) |
| 6 | Linux | 6.8 | fresh | `mode=enforce` + `--sandbox=off` | Disabled (CLI wins) | `policy_applied=false, disabled_by=cli_flag` | Dev override via CLI flag (AS-1) |
| 7 | Linux | 6.8 | fresh | `mode=permissive` | LinuxBackend permissive (with kernel support) | `policy_applied=true, mode=permissive, audit_only=true` | Permissive mode logs policy violations |
| 8 | Linux | 6.8 | fresh | `mode=permissive` (pre-6.12 semantics) | Landlock SKIPPED; seccomp RET_LOG | `policy_applied=true, landlock_enforced=false, seccomp_enforced=false, audit_only=true` | Permissive mode on pre-6.12 kernel downgrades |
| 9 | Linux | 6.8 | legacy config `enabled=true`, `mode` unset | Legacy-to-new mapping | Mapped to `mode=enforce` | `policy_applied=true` | (Behavioral contract — legacy compatibility) |
| 10 | Linux | 6.8 | legacy config `enabled=false`, `mode` unset | Legacy-to-new mapping | Mapped to `mode=off` | `policy_applied=false` | (Behavioral contract — legacy compatibility) |
| 11 | Darwin | 23.x | fresh | `mode=enforce` | Fallback; seccomp NOT installed | `policy_applied=false, seccomp_enabled=false` | Non-Linux build selects fallback; Seccomp gated |
| 12 | Windows | 10.0.22621 | fresh | `mode=enforce` | Fallback; seccomp NOT installed | `policy_applied=false, seccomp_enabled=false` | Non-Linux build selects fallback; Seccomp gated |
| 13 | Linux (Termux) | 5.15 Android | fresh | `mode=enforce` | Fallback | `policy_applied=false, reason=termux_no_landlock` | Pre-Landlock fallback (Android) |
| 14 | Linux | 6.8 | fresh | `mode=enforce`, kernel returns EINVAL on create_ruleset | enforce→fatal | Boot exits 78, no listener | Apply() kernel error (EP) |
| 15 | Linux | 6.8 | fresh | `mode=off`, `OMNIPUS_ENV=production` | Disabled with production nag | `policy_applied=false`; nag banner every 60s | Production with sandbox disabled warns |
| 16 | Linux | 6.8 | fresh | `mode=enforce`; runtime SIGHUP with `mode=off` in config | No-op | Running gateway keeps `policy_applied=true`; rejection log emitted | Hot-reload rejected (non-behavior) |

### Regression Test Requirements

> New capability — no existing Apply-at-boot behaviour to preserve. Integration seams protected by:

| Existing Behaviour | Existing Test | Regression Risk |
|--------------------|---------------|-----------------|
| `ApplyToCmd()` for child processes works | `pkg/tools/shell_test.go`, `tests/security/sandbox_enforcement_linux_test.go` | MUST still pass — new code must not break child sandbox inheritance. |
| Gateway boots without sandbox wiring (current behaviour, to be deprecated) | All `pkg/gateway/rest_*_test.go` | These tests use `FallbackBackend` implicitly via `NewAgentLoop`; they must still pass because they operate below the `Gateway.Start()` layer. |
| `omnipus doctor` reports sandbox status | `pkg/cli/doctor_test.go` (if present) | After wiring, the doctor output changes — update snapshot/golden if applicable. |

---

## 9. Phase 5 — Functional Requirements & Success Criteria

### Functional Requirements

- **FR-J-001** — The gateway MUST invoke `sandboxBackend.Apply(policy)` exactly once during boot on Linux ≥ 5.13 when `gateway.sandbox.mode = enforce` (or `permissive`), before `net.Listen` is called.
- **FR-J-002** — The gateway MUST invoke `seccompProgram.Install()` exactly once, strictly AFTER `Apply()` returns successfully, and before `net.Listen`.
- **FR-J-003** — The gateway MUST compute the sandbox policy from `$OMNIPUS_HOME`, system-library paths (`/lib`, `/lib64`, `/usr`, `/etc/ssl`), temp (`/tmp`), self-proc (`/proc/self`), and the `Sandbox.AllowedPaths` config list.
- **FR-J-004** — The gateway MUST fail closed (exit code 78 / EX_CONFIG, no HTTP listener) when `Apply()` or `Install()` returns an error on a kernel that claims support. Exit code 78 is the Sprint-J-specific code for sandbox apply/install failure; other failure paths in `cmd/omnipus/main.go` continue to use their existing codes (currently generic 1).
- **FR-J-005** — The gateway MUST select `FallbackBackend` on kernels below 5.13 and on non-Linux builds, and MUST continue to boot successfully without Apply/Install.
- **FR-J-006** — The gateway MUST accept a `--sandbox=off|enforce|permissive` CLI flag that takes precedence over `gateway.sandbox.mode` in the config. Invalid values MUST cause exit code 2 (usage error) before any boot logic runs.
- **FR-J-007** — The gateway SHOULD emit structured log events `sandbox.applied`, `sandbox.permissive`, `sandbox.degraded`, `sandbox.disabled`, `sandbox.apply_failed`, `sandbox.apply.skipped`, `sandbox.violation`, `sandbox.disabled.nag`, `sandbox.permissive.nag` with consistent field sets (`backend`, `mode`, `landlock_abi`, `seccomp_syscalls`, `reason`, `disabled_by`, `enforced`).
- **FR-J-008** — `GET /api/system/sandbox/status` MUST return `policy_applied=true, seccomp_enabled=true, mode="enforce", notes=[]` (no "Apply() has not been called" note) after successful apply in enforce mode.
- **FR-J-009** — `LinuxBackend.Apply()` MUST be idempotent within a single process — a second invocation returns nil without invoking kernel syscalls.
- **FR-J-010** — The gateway MUST NOT invoke Apply before credential unlock or config load, and MUST invoke Apply before binding the HTTP listener (ordering: unlock → load config → select backend → Apply → Install → `net.Listen`).
- **FR-J-011** — When `OMNIPUS_ENV=production` AND mode=off, the gateway MUST emit a multi-line WARN banner on stderr at boot and every 60 seconds. No suppression mechanism exists.
- **FR-J-012** — The gateway MUST support a `permissive` mode in which the policy is computed and logged but violations are not enforced. Implementation: seccomp filter uses `SECCOMP_RET_LOG` in place of `SECCOMP_RET_ERRNO`; on kernels supporting permissive Landlock (≥ 6.12), Landlock uses permissive semantics; on kernels without, `landlock_restrict_self` is skipped and the mode degrades to audit-only. Permissive mode MUST emit a prominent stderr banner `"SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION."` at boot and every 60 seconds.
- **FR-J-013** — `computeWorkspacePolicy` MUST strip the Write access bit from any `AllowedPaths` entry that overlaps a system-restricted path (`/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root`, or any child). Read is preserved per user intent. A WARN log MUST be emitted for each stripped rule.
- **FR-J-014** — The gateway MUST NOT install seccomp when `FallbackBackend` is selected. Seccomp is gated on `LinuxBackend.Apply()` having been invoked successfully. Both-or-neither — never seccomp-alone.
- **FR-J-015** — The gateway MUST NOT support hot-reload of `sandbox.*` config keys. Runtime changes to the config file MUST be ignored for sandbox keys; a SIGHUP-driven reload or reload endpoint MUST log a rejection and leave the applied policy untouched. A restart is required to change sandbox settings.
- **FR-J-016** — During the boot window AFTER `SelectBackend()` and BEFORE `net.Listen` returns, the gateway MUST NOT bind any HTTP listener. External clients hitting the configured port during this window MUST receive a TCP-level connection refusal (ECONNREFUSED), not an HTTP 503.

### Success Criteria

- **SC-J-001** — On a Linux 6.x host after boot in enforce mode, attempting to write `/etc/passwd` from within the gateway process returns `EACCES` within 1ms (kernel-enforced, no tail).
- **SC-J-002** — On a Linux 6.x host after boot, `curl localhost:3000/api/system/sandbox/status` returns JSON with `policy_applied: true, mode: "enforce"` within 50ms of the first successful `/health` request.
- **SC-J-003** — On a Linux 5.10 host, the gateway completes boot (binds HTTP listener) within 2 seconds and `policy_applied: false, seccomp_enabled: false, backend_name: "fallback"`.
- **SC-J-004** — The sandbox wiring adds no more than 5ms to cold boot time on Linux 6.x (measured as the delta between `SelectBackend()` return and `Install()` return).
- **SC-J-005** — The total RAM overhead added by Apply + Install is ≤ 512KB (BPF program + ruleset fd are tiny; the CLAUDE.md 10MB budget has ample headroom).
- **SC-J-006** — The integration test suite (`go test ./tests/security/...`) passes on Linux with the new wiring enabled; no flake rate > 1% over 100 runs.
- **SC-J-007** — `/api/system/sandbox/status` never contains the string "Apply() has not been called" after a successful wired boot (test: `grep -c` on response body == 0).
- **SC-J-008** — When `--sandbox=off`, `ptrace(PTRACE_ATTACH, <gateway_pid>, ...)` from a test harness succeeds within 100ms (dev debugging unblocked).
- **SC-J-009** — In permissive mode on a Linux 6.x host, attempting a policy-violating file access writes an audit-log entry with `enforced=false` to `$OMNIPUS_HOME/system/audit.jsonl` within 10ms; the underlying syscall succeeds (or fails only due to normal Linux DAC).
- **SC-J-010** — During the boot window (after `SelectBackend()`, before `net.Listen` returns), a TCP probe to the configured port receives `ECONNREFUSED` at connect() — verified by a concurrent polling harness that records 0 HTTP responses (of any status code) until the first successful connection.
- **SC-J-011** — User `AllowedPaths` entries that overlap system-restricted paths produce a rule with `Access & AccessWrite == 0` in the computed `SandboxPolicy` (100% of cases in Dataset A rows 3-6 and 9-10) and a WARN log line is emitted for each.
- **SC-J-012** — On a Linux host where `SelectBackend()` returns `FallbackBackend`, `SYS_SECCOMP` does not appear in the process's syscall trace after boot (verified via bpftrace / /proc/PID/status SeccompFilter counter == 0).

### Traceability Matrix

| FR | User Story | BDD Scenario(s) | Test Name(s) |
|----|-----------|-----------------|--------------|
| FR-J-001 | US-1, US-3 | Fresh boot applies Landlock and seccomp; Seccomp Install runs after Landlock Apply; Permissive mode logs violations | TestGatewayBoot_AppliesSandboxOnCapableKernel; TestSeccompInstallOrderAfterLandlock; TestGatewayBoot_PermissiveModeLogsViolations |
| FR-J-002 | US-1, US-3 | Seccomp Install runs after Landlock Apply | TestSeccompInstallOrderAfterLandlock |
| FR-J-003 | US-1 | Workspace policy permits $OMNIPUS_HOME writes, denies /etc | TestComputeWorkspacePolicy_DefaultRules; TestComputeWorkspacePolicy_AppendsAllowedPaths; TestGatewayBoot_WorkspaceWriteAllowed; TestGatewayBoot_EtcPasswdDenied |
| FR-J-004 | US-1 | Apply() kernel error fails the boot closed | TestGatewayBoot_FailsClosedOnApplyError |
| FR-J-005 | US-1 | Pre-Landlock kernel falls back gracefully; Non-Linux build selects fallback; Seccomp NOT installed when FallbackBackend | TestGatewayBoot_DegradesOnPre5_13Kernel; TestFallbackApply_IsNoOp; TestSeccompNotInstalledOnFallback |
| FR-J-006 | US-2 | Dev override via CLI flag disables sandbox | TestSandboxMode_CLIBeatsConfig; TestSandboxMode_ConfigDefault; TestSandboxMode_LegacyEnabledFieldMapping; TestSandboxMode_InvalidCLIValueExits2; TestGatewayBoot_CLIFlagDisablesSandbox |
| FR-J-007 | US-1, US-2, US-3 | All scenarios assert on log strings | All integration tests assert log output |
| FR-J-008 | US-1 | Fresh boot applies Landlock and seccomp (AS-2) | TestSandboxStatus_NoteRemovedAfterWiring |
| FR-J-009 | US-1 | Repeated Apply() within the same process is a safe no-op | TestApplyIsIdempotent |
| FR-J-010 | US-1 | Fresh boot applies Landlock and seccomp (ordering); /health during window is ECONNREFUSED | TestSeccompInstallOrderAfterLandlock; TestGatewayBoot_HealthDuringWindowIsECONNREFUSED |
| FR-J-011 | US-2 | Production environment with sandbox disabled logs prominent warning | TestSandboxMode_ProductionNagBanner |
| FR-J-012 | US-3 | Permissive mode logs policy violations; Permissive mode on pre-6.12 kernel downgrades | TestSandboxMode_PermissiveNagBanner; TestGatewayBoot_PermissiveModeLogsViolations; TestGatewayBoot_PermissiveDowngradesOnOldKernel |
| FR-J-013 | US-1 | AllowedPaths overlap with /etc allows read, denies write | TestComputeWorkspacePolicy_SystemPathOverlapReadOnly; TestGatewayBoot_EtcCaCertsReadOkWriteDenied |
| FR-J-014 | US-1 | Seccomp is NOT installed when FallbackBackend is selected; Pre-Landlock kernel falls back | TestSeccompNotInstalledOnFallback |
| FR-J-015 | US-1 | (Non-Behavior — captured in TDD row 21) | TestGatewayBoot_NoHotReloadOfSandboxConfig |
| FR-J-016 | US-1 | /health during Apply→Install window returns TCP connection-refused | TestGatewayBoot_HealthDuringWindowIsECONNREFUSED |

**Completeness check**: Every FR-J-* row has ≥1 BDD scenario and ≥1 test. Every BDD scenario (9 original + 4 added = 13 total, not counting scenario outlines) appears in ≥1 row. ✓

---

## 10. Phase 5.5 — Ambiguity Self-Audit

All 7 original ambiguities were RESOLVED by user decision on 2026-04-20. Left here as a decision record.

| # | Original Ambiguity | Resolution | Reflected In |
|---|--------------------|------------|--------------|
| 1 | Behaviour of `/health` in the ~10ms window between `Apply()` and `net.Listen()` | **RESOLVED** — Listener is bound AFTER Install(). Sequence: selectBackend → Apply → Install → net.Listen. No pre-HTTP readiness probe in Sprint J. /health returns TCP ECONNREFUSED (not HTTP 503) during the window. | FR-J-016; US-1 AS-6; BDD "/health during Apply→Install window returns TCP connection-refused"; Test `TestGatewayBoot_HealthDuringWindowIsECONNREFUSED`; SC-J-010 |
| 2 | Should `permissive` mode ship in Sprint J or be deferred? | **RESOLVED — SHIP in Sprint J.** Seccomp uses `SECCOMP_RET_LOG` instead of `SECCOMP_RET_ERRNO`. Landlock uses permissive semantics where kernel supports it (hypothetical 6.12+); on current kernels (≤ 6.11) `landlock_restrict_self` is skipped and the mode degrades to audit-only. Prominent stderr banner "SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION." fires at boot and every 60s. | FR-J-012; US-3; BDD "Permissive mode logs policy violations"; BDD "Permissive mode on pre-6.12 kernel downgrades to audit-only"; Tests rows 9, 15, 16; SC-J-009 |
| 3 | AllowedPaths vs system-restricted paths — user wins or deny wins? | **RESOLVED — user read-only wins; write always denied.** System-restricted set: `/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root` (+ children). User `AllowedPaths` entries in this set are coerced to Read-only in `computeWorkspacePolicy`; WARN logged. | FR-J-013; Dataset A rows 3-6, 9-10; BDD "AllowedPaths overlap with /etc allows read, denies write"; Tests `TestComputeWorkspacePolicy_SystemPathOverlapReadOnly`, `TestGatewayBoot_EtcCaCertsReadOkWriteDenied`; SC-J-011 |
| 4 | Exit code on Apply/Install failure | **RESOLVED — exit 78 (EX_CONFIG).** Verified `cmd/omnipus/main.go` currently uses only `os.Exit(1)` generically (line 61), so there is no conflicting convention. Sprint J introduces 78 specifically for sandbox apply/install failure; other existing failures keep exit 1. Documented in CLI help. | FR-J-004; BDD "Apply() kernel error fails the boot closed" (exit 78 assertion); Integration Boundaries → Exit Codes section |
| 5 | Suppress the production nag banner? | **RESOLVED — NO suppression mechanism in Sprint J.** Banner fires unconditionally when `OMNIPUS_ENV=production` + mode=off. Operator must either enable sandbox or accept the banner. | FR-J-011 (final sentence); Non-Behaviors bullet; BDD "Production environment with sandbox disabled logs prominent warning" (added "NO config-based or env-based suppression knob" clause) |
| 6 | Hot-reload of sandbox config? | **RESOLVED — NO hot-reload in Sprint J.** Sandbox is process-global, applied once at boot. Changes to `sandbox.*` require full gateway restart. SIGHUP / reload endpoint rejects sandbox-key mutations with a log line and leaves policy untouched. | FR-J-015; Non-Behaviors bullet; Integration Boundaries → Config System (Hot-reload: NOT supported); Dataset B row 16; Test `TestGatewayBoot_NoHotReloadOfSandboxConfig` |
| 7 | Seccomp alone without Landlock? | **RESOLVED — NO.** Seccomp install is strictly gated on LinuxBackend selection. If Landlock falls back, seccomp is NOT installed. Both-or-neither — never seccomp-alone. | FR-J-014; Non-Behaviors bullet; BDD "Seccomp is NOT installed when FallbackBackend is selected"; Test `TestSeccompNotInstalledOnFallback`; SC-J-012; Dataset B rows 4, 11, 12 |

### Remaining (lower-priority) open questions

| # | What's Ambiguous | Assumed Behaviour | Question to Resolve |
|---|------------------|-------------------|---------------------|
| 8 | Canonical `SECCOMP_RET_LOG` availability check for permissive mode — do all Linux 5.13+ kernels expose it? | Assumed: yes, SECCOMP_RET_LOG has been in the kernel since 4.14 (well before our 5.13 floor). | Confirm during implementation. Fall back to "permissive = skip seccomp entirely" if not. |
| 9 | Does the existing `LinuxBackend` struct have an `applied` flag that makes FR-J-009 idempotency trivial, or does Sprint J need to add one? | `pkg/sandbox/sandbox_linux.go:121-123` shows `PolicyApplied()` method reads a `policyApplied` bool. Confirmed idempotency flag exists. | None — this is resolved by code inspection. |

---

## 11. Phase 5.7 — Holdout Evaluation Scenarios

> **Not referenced in TDD plan or traceability matrix.** For post-implementation verification only.

### Happy Path

- **H1 — Cold start under load**
  - **Setup**: Pre-warm 100 HTTP clients, then start gateway.
  - **Action**: Measure time-to-first-200 on `/health`.
  - **Expected**: Apply + Install + bind completes within 100ms; no 503s.

- **H2 — Sandbox applied, agent runs a tool that writes to workspace**
  - **Setup**: Boot with sandbox enabled; send chat message asking the agent to run `write_file $OMNIPUS_HOME/sessions/probe.txt`.
  - **Action**: Observe tool call result.
  - **Expected**: Tool succeeds (workspace is writable).

- **H3 — Sandbox applied, agent rejected when writing outside workspace**
  - **Setup**: Same as H2.
  - **Action**: Agent attempts `write_file /tmp/../etc/malicious`.
  - **Expected**: Tool returns EACCES; audit log records the denial.

### Error

- **E1 — Disk full during boot**
  - **Setup**: Mount `$OMNIPUS_HOME` on a filesystem with 0 bytes free.
  - **Action**: Boot gateway.
  - **Expected**: Boot fails BEFORE Apply (config write fails first); clean error message. Apply/Install should not have been attempted.

- **E2 — Sandbox flag typo**
  - **Setup**: Pass `--sandbox=of` (typo).
  - **Action**: Boot gateway.
  - **Expected**: Clear error `invalid sandbox mode "of"; must be one of: enforce, permissive, off`; exit code 2 (usage error); no boot.

### Edge

- **G1 — Very long `AllowedPaths` list (500 entries)**
  - **Setup**: Config with 500 valid paths.
  - **Action**: Boot gateway.
  - **Expected**: Apply succeeds; all 500 rules added; boot time within SC-J-004 budget.

- **G2 — `AllowedPaths` contains `$OMNIPUS_HOME` explicitly (duplicate)**
  - **Setup**: Config lists `$OMNIPUS_HOME` that's already in default rules.
  - **Action**: Boot gateway.
  - **Expected**: Duplicate deduplicated (or two rules with same path, kernel handles it); no error; boot succeeds.

---

## Assumptions

- The `SandboxBackend` interface at `pkg/sandbox/sandbox.go:40-46` is stable for Sprint J — no shape changes.
- `LinuxBackend.Apply()` already tracks its own `policyApplied` state correctly (`pkg/sandbox/sandbox_linux.go:121-123` — confirmed via `PolicyApplied()` method) and returns nil on repeat calls; FR-J-009 is satisfied by the existing code once the caller is wired in.
- The gateway's current boot order (credential unlock → config load → `NewAgentLoop` → services → listen) is correct up to the `NewAgentLoop` return; the new Apply/Install call slots in between `NewAgentLoop` and `http.ListenAndServe`.
- `OMNIPUS_ENV=production` is the agreed environment-identifier convention. If a different convention is used project-wide, implementation should align with the existing one and update this spec.
- Non-Linux builds route through `selectBackendPlatform` to return `FallbackBackend` via Go build tags; this is the standard pattern already in place.
- `SECCOMP_RET_LOG` is available on all supported kernels (≥ 5.13); confirmed present in the kernel since 4.14.

## Clarifications

### 2026-04-20 (initial drafting)

- **Q**: Should the sprint name be `sprint-j-sandbox-apply` (narrow) or `sprint-j-security-hardening` (broad)? **A**: Branch is already `sprint-j-security-hardening`; spec filename is `sprint-j-sandbox-apply-spec.md` to scope tightly to issue #76. Future hardening work can add sibling specs on the same branch.
- **Q**: Does `Apply()` need a `context.Context` parameter for cancellation? **A**: No — Apply is a one-shot prctl+syscall sequence measured in microseconds. Adding ctx would be API churn for zero benefit.

### 2026-04-20 (ambiguity resolution round)

- **Q1**: Behaviour of `/health` during Apply→Install window? **A**: Listener is bound last. Window returns TCP ECONNREFUSED, not HTTP 503. No pre-HTTP readiness probe in Sprint J.
- **Q2**: Ship permissive mode in Sprint J? **A**: YES. Uses `SECCOMP_RET_LOG` + kernel-dependent Landlock permissive semantics. Audit-only on kernels < 6.12. Prominent banner at boot + every 60s.
- **Q3**: AllowedPaths vs system-restricted paths? **A**: User read-only wins; write unconditionally denied for `/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root` (+ children). WARN logged.
- **Q4**: Exit code on Apply/Install failure? **A**: 78 (EX_CONFIG). Verified no conflicting convention in `cmd/omnipus/main.go` (currently uses only generic `os.Exit(1)`).
- **Q5**: Suppress production nag banner? **A**: No suppression mechanism in Sprint J.
- **Q6**: Hot-reload sandbox config? **A**: No. Sandbox is process-global; config changes require restart. Must be documented in README.
- **Q7**: Seccomp without Landlock? **A**: No. Both-or-neither. Seccomp is gated on LinuxBackend selection.
- **Q (new)**: How should the legacy `Enabled bool` config field coexist with the new `Mode` enum? **A**: Backwards-compatible mapping — when `Mode` is empty and `Enabled` is set, `true`→`enforce`, `false`→`off`. New installs write only `Mode`. Both fields remain in the struct; `Enabled` is deprecated (`// Deprecated: use Mode instead`) but not removed.
