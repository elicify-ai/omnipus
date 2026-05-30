# Business Requirements Document — Appendix A

## Omnipus Windows Kernel-Level Security Implementation

**Version:** 1.0 DRAFT  
**Date:** March 19, 2026  
**Parent Document:** Omnipus BRD v1.0  
**Status:** For Review

-----

## A.1 Purpose

This appendix extends the Omnipus BRD to define requirements for Windows-native kernel-level security enforcement. The main BRD specifies Linux Landlock and seccomp as the primary sandboxing mechanism (SEC-01, SEC-02, SEC-03). This appendix defines the equivalent Windows implementation using Job Objects, Restricted Tokens, and Windows-native access control — ensuring Omnipus delivers OS-enforced security on both major server and desktop platforms.

-----

## A.2 Background — Windows Security Primitives

Windows provides several OS-level isolation mechanisms that can be used by unprivileged applications without requiring Administrator rights or system-wide policy changes. These are the primitives Omnipus will use:

### A.2.1 Windows Job Objects

A Job Object is a kernel object that allows a group of processes to be managed as a unit. When a process is assigned to a Job Object, the following restrictions can be enforced:

- **Process limits:** Maximum number of child processes, CPU time limits, memory limits (working set and commit).
- **UI restrictions:** Block clipboard access, block desktop switching, block display setting changes.
- **Security restrictions:** Prevent child processes from escaping the Job Object, prevent creation of processes outside the job, prevent use of user handles from other processes.
- **Network restrictions:** Not natively supported by Job Objects — requires supplementary approach (see A.2.4).

Job Objects are inheritable: child processes spawned within a Job Object automatically belong to the same job and inherit all restrictions. This maps directly to the Linux requirement SEC-03 (child process sandbox inheritance).

Available since: Windows Vista / Server 2008. Nested Job Objects available since Windows 8 / Server 2012.

Go access: `golang.org/x/sys/windows` provides `CreateJobObject`, `AssignProcessToJobObject`, `SetInformationJobObject`.

### A.2.2 Restricted Tokens

Windows allows a process to create a “restricted token” — a copy of its security token with privileges removed, SIDs disabled, or restricting SIDs added. A child process launched with a restricted token has reduced capabilities even though it runs under the same user account.

Capabilities:

- Remove specific privileges (e.g., `SeBackupPrivilege`, `SeDebugPrivilege`, `SeShutdownPrivilege`).
- Add deny-only SIDs that prevent access to resources granted to those SIDs.
- Add restricting SIDs that limit the effective access to only resources that also grant access to the restricting SID.
- When combined with Job Objects, this creates a defense-in-depth sandbox.

Available since: Windows 2000.

Go access: `CreateRestrictedToken` via `golang.org/x/sys/windows`, then `CreateProcessAsUser` or `CreateProcess` with the restricted token as the process token.

### A.2.3 Filesystem Access Control (DACL Manipulation)

Windows NTFS supports per-file and per-directory Discretionary Access Control Lists (DACLs). Omnipus can:

- Create a temporary restricted user or use a restricted token.
- Set DACLs on the workspace directory to grant access only to the restricted token’s SID.
- Deny access to all paths outside the workspace for processes running under the restricted token.

This is the Windows equivalent of Landlock filesystem restrictions. Unlike Landlock (which is self-applied by the process), DACLs are enforced by the NTFS filesystem driver at the kernel level.

Limitation: Only works on NTFS volumes. FAT32/exFAT volumes do not support DACLs.

### A.2.4 Windows Filtering Platform (WFP)

For network egress control (currently excluded from the main BRD scope but documented here for completeness), Windows provides the Windows Filtering Platform — a kernel-mode network filtering framework. However, WFP requires Administrator privileges to install filters, making it unsuitable for Omnipus’s unprivileged execution model.

Alternative approaches for future consideration:

- **Layered Service Provider (LSP):** Deprecated since Windows 8.
- **Local proxy with loopback binding:** Application-level, same limitations as on Linux.
- **Windows Defender Firewall rules:** Requires Administrator.

**Recommendation:** Kernel-level network egress control on Windows remains out of scope, consistent with the main BRD (see LIM-01). The exec tool HTTP proxy (SEC-28) provides best-effort partial coverage on all platforms. `omnipus doctor` warns about this limitation (SEC-29). For full network isolation on Windows, deploy Omnipus inside a container or VM with restricted network rules.

-----

## A.3 Architecture — Platform Abstraction Layer

The Omnipus security engine will use a platform abstraction interface so that the policy engine, audit logging, and configuration are identical across operating systems. Only the enforcement backend varies.

```
┌──────────────────────────────────────────────────┐
│              Omnipus Policy Engine                 │
│   (JSON config, deny-by-default, audit logging)  │
└──────────────────┬───────────────────────────────┘
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
┌──────────┐ ┌──────────┐ ┌──────────────┐
│  Linux   │ │ Windows  │ │   Fallback   │
│ Backend  │ │ Backend  │ │   Backend    │
├──────────┤ ├──────────┤ ├──────────────┤
│ Landlock │ │ Job Obj  │ │ App-level    │
│ seccomp  │ │ Restr.   │ │ path checks  │
│ (kernel) │ │ Token    │ │ cmd blocking │
│          │ │ DACL     │ │              │
└──────────┘ └──────────┘ └──────────────┘
```

The abstraction interface (Go):

```go
type SandboxBackend interface {
    // Capabilities reports what this backend can enforce
    Capabilities() SandboxCapabilities

    // ApplyFilesystemPolicy restricts file access to allowed paths
    ApplyFilesystemPolicy(policy FilesystemPolicy) error

    // ApplyProcessPolicy restricts child process capabilities
    ApplyProcessPolicy(policy ProcessPolicy) error

    // LaunchSandboxed starts a child process within the sandbox
    LaunchSandboxed(cmd string, args []string) (*exec.Cmd, error)

    // Name returns the backend identifier for audit logging
    Name() string
}

type SandboxCapabilities struct {
    KernelFilesystem  bool  // Kernel-enforced filesystem isolation
    KernelProcess     bool  // Kernel-enforced process restrictions
    KernelNetwork     bool  // Kernel-enforced network control
    ChildInheritance  bool  // Restrictions inherit to child processes
    Unprivileged      bool  // Works without root/Administrator
}
```

At startup, Omnipus detects the platform and selects the appropriate backend:

1. **Linux 5.13+** → Linux backend (Landlock + seccomp). Full kernel enforcement.
1. **Linux <5.13** → Fallback backend. Application-level checks only.
1. **Windows 8+ / Server 2012+** → Windows backend (Job Objects + Restricted Tokens + DACL). Near-full kernel enforcement.
1. **Windows <8** → Fallback backend. Application-level checks only.
1. **macOS / FreeBSD / Android (Termux)** → Fallback backend. Application-level checks only.

-----

## A.4 Requirements — Windows Kernel Security

### A.4.1 Process Sandboxing (Job Objects)

|ID    |Requirement                                 |Priority|Effort  |Details                                                                                                                                                                                                                                                                                                                                                                    |
|------|--------------------------------------------|--------|--------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|WIN-01|Job Object creation for agent tool execution|P0      |Moderate|When an agent invokes the exec tool or spawns a child process, Omnipus creates a Windows Job Object with the following restrictions: `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (all children die when Omnipus exits), `JOB_OBJECT_LIMIT_ACTIVE_PROCESS` (configurable max child processes, default 10), `JOB_OBJECT_LIMIT_PROCESS_MEMORY` (configurable per-process memory limit).|
|WIN-02|Child process containment                   |P0      |Easy    |Set `JOB_OBJECT_LIMIT_BREAKAWAY_OK` to FALSE so child processes cannot escape the Job Object. All descendants of a sandboxed process remain within the same job. This is the Windows equivalent of SEC-03.                                                                                                                                                                 |
|WIN-03|UI restriction for headless agents          |P1      |Easy    |Apply `JOB_OBJECT_UILIMIT_DESKTOP`, `JOB_OBJECT_UILIMIT_DISPLAYSETTINGS`, `JOB_OBJECT_UILIMIT_EXITWINDOWS`, `JOB_OBJECT_UILIMIT_GLOBALATOMS`, `JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS` to prevent sandboxed processes from interacting with the Windows desktop, accessing the clipboard, or shutting down the system.                                                        |
|WIN-04|CPU and memory limits                       |P2      |Easy    |Configurable `JOB_OBJECT_LIMIT_JOB_MEMORY` (total memory for all processes in the job) and `JOB_OBJECT_LIMIT_JOB_TIME` (total CPU time). Prevents runaway agent tool execution from consuming system resources.                                                                                                                                                            |

### A.4.2 Privilege Reduction (Restricted Tokens)

|ID    |Requirement                             |Priority|Effort  |Details                                                                                                                                                                                                                                                                                                                                                                                                                                           |
|------|----------------------------------------|--------|--------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|WIN-05|Restricted token for sandboxed processes|P0      |Moderate|Before launching a sandboxed child process, Omnipus creates a restricted token that: removes `SeBackupPrivilege` (bypass file permission checks), removes `SeRestorePrivilege` (bypass file permission for writes), removes `SeDebugPrivilege` (attach debugger to other processes), removes `SeShutdownPrivilege`, removes `SeSystemtimePrivilege`, removes `SeTakeOwnershipPrivilege`. The restricted token is applied via `CreateProcessAsUser`.|
|WIN-06|Deny-only SIDs                          |P1      |Moderate|Add deny-only SIDs for the Administrators group and Power Users group to the restricted token. Even if the Omnipus user is in these groups, sandboxed child processes cannot exercise those group memberships.                                                                                                                                                                                                                                     |
|WIN-07|Integrity level reduction               |P1      |Moderate|Set the sandboxed process integrity level to Low or Untrusted using `SetTokenInformation` with `TokenIntegrityLevel`. Low-integrity processes cannot write to most filesystem locations or registry keys, even if DACL permissions would allow it. This provides a second layer of filesystem protection independent of DACLs.                                                                                                                    |

### A.4.3 Filesystem Isolation (DACL)

|ID    |Requirement                              |Priority|Effort  |Details                                                                                                                                                                                                                                                                                                                                                                                |
|------|-----------------------------------------|--------|--------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|WIN-08|Workspace-only filesystem access via DACL|P0      |Moderate|Before launching a sandboxed process, Omnipus sets DACLs on the workspace directory and its contents to grant full access to the restricted token’s SID. The restricted token’s restricting SIDs ensure that only paths with matching ACL entries are accessible. Combined with Low integrity level (WIN-07), this provides kernel-enforced filesystem isolation comparable to Landlock.|
|WIN-09|Temporary directory access               |P0      |Easy    |In addition to the workspace, grant the sandboxed process access to a dedicated temporary directory (`%LOCALAPPDATA%\Omnipus\sandbox-temp\<session-id>\`). This temp directory is created per-session and cleaned up after the sandboxed process exits.                                                                                                                                 |
|WIN-10|NTFS detection and fallback              |P1      |Easy    |At startup, check whether the workspace path is on an NTFS volume. If not (e.g., FAT32 USB drive, network share without ACL support), log a security warning and fall back to application-level path checks. The `omnipus security audit` command flags this as a risk.                                                                                                                 |

### A.4.4 Dangerous Operation Blocking

|ID    |Requirement                                |Priority|Effort|Details                                                                                                                                                                                                                                                                                                                                                                   |
|------|-------------------------------------------|--------|------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|WIN-11|Windows-specific dangerous command blocking|P0      |Easy  |Extend the existing dangerous command blocklist with Windows equivalents: `format`, `diskpart`, `del /s /q`, `rmdir /s /q`, `shutdown`, `taskkill`, `reg delete`, `bcdedit`, `cipher /w`, `sfc`, `dism`, PowerShell equivalents (`Remove-Item -Recurse -Force`, `Stop-Process`, `Remove-PSDrive`). Applied at the application level (before exec) on all Windows versions.|
|WIN-12|PowerShell execution policy                |P1      |Easy  |When executing PowerShell scripts via the exec tool, force `-ExecutionPolicy Restricted` or `-ExecutionPolicy AllSigned` to prevent unsigned script execution within the sandbox. Configurable per policy.                                                                                                                                                                |

-----

## A.5 Feature Mapping — Linux vs Windows

This table shows how each security requirement from the main BRD maps to a Windows implementation:

|Main BRD ID                      |Linux Implementation              |Windows Implementation                                                    |Parity Level                                                                                    |
|---------------------------------|----------------------------------|--------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
|SEC-01: Filesystem sandboxing    |Landlock LSM                      |DACL + Integrity Level + Restricted Token (WIN-05, WIN-07, WIN-08)        |High — both are kernel-enforced                                                                 |
|SEC-02: Syscall filtering        |seccomp-BPF                       |Restricted Token privilege removal + Job Object UI limits (WIN-05, WIN-03)|Medium — Windows doesn’t allow per-syscall filtering, but privilege removal covers the key cases|
|SEC-03: Child process inheritance|Landlock inherits; seccomp `TSYNC`|Job Object containment (WIN-02)                                           |High — both prevent child process escape                                                        |
|SEC-04: Tool allow/deny          |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-05: Per-binary control       |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-06: Per-method control       |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-07: Deny-by-default          |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-08: Exec approval            |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-11–14: Policy engine         |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-15–18: Audit logging         |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-19–21: RBAC                  |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-22–23e: Credentials          |Application-level (cross-platform)|Same + Windows Credential Manager as keyring backend for cached passphrase key (SEC-23b)|Full                                                                                            |
|SEC-24: SSRF protection          |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|SEC-26: Rate limiting            |Application-level (cross-platform)|Same                                                                      |Full                                                                                            |
|Network egress control           |Best-effort: HTTP proxy for exec tool (SEC-28). Full control needs netns/eBPF (privileged).|Best-effort: HTTP proxy for exec tool (SEC-28). Full control needs WFP (Admin).|Partial — see LIM-01, LIM-02 in main BRD §5.7.1|

**Summary:** Of the 27 security features in the main BRD, 22 are pure application-level and fully cross-platform. The remaining 5 (SEC-01, SEC-02, SEC-03 and their dependencies) require platform-specific implementation. The Windows equivalents provide **high parity** for filesystem isolation and child process containment, and **medium parity** for syscall/privilege filtering.

-----

## A.6 Implementation Considerations

### A.6.1 Go Support for Windows Security APIs

All required Windows APIs are accessible from Go via `golang.org/x/sys/windows`:

|API                       |Go Access                 |Notes                                                                                                               |
|--------------------------|--------------------------|--------------------------------------------------------------------------------------------------------------------|
|`CreateJobObjectW`        |`golang.org/x/sys/windows`|Direct syscall wrapper available                                                                                    |
|`AssignProcessToJobObject`|`golang.org/x/sys/windows`|Direct syscall wrapper available                                                                                    |
|`SetInformationJobObject` |`golang.org/x/sys/windows`|Requires manual struct marshaling for `JOBOBJECT_BASIC_LIMIT_INFORMATION` and `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`|
|`CreateRestrictedToken`   |`golang.org/x/sys/windows`|May require manual definition — verify availability in current package version                                      |
|`CreateProcessAsUserW`    |`golang.org/x/sys/windows`|Direct syscall wrapper available                                                                                    |
|`SetTokenInformation`     |`golang.org/x/sys/windows`|For integrity level changes                                                                                         |
|`SetNamedSecurityInfoW`   |`golang.org/x/sys/windows`|For DACL manipulation                                                                                               |
|`GetVolumeInformationW`   |`golang.org/x/sys/windows`|For NTFS detection                                                                                                  |

**Risk:** Some structs (e.g., `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`) may not be pre-defined in the Go package and will need manual definition. This is straightforward but requires careful alignment with the Windows SDK headers.

### A.6.2 Testing Strategy

|Test Category              |Approach                                                                                                                                                                  |
|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Unit tests                 |Mock the `SandboxBackend` interface; test policy logic cross-platform                                                                                                     |
|Integration tests (Windows)|GitHub Actions Windows runners (windows-latest) with automated Job Object + Restricted Token verification                                                                 |
|Integration tests (Linux)  |GitHub Actions Ubuntu runners with Landlock/seccomp verification                                                                                                          |
|Escape tests               |Automated tests that attempt to read/write outside the workspace, spawn breakaway processes, and escalate privileges from within the sandbox. Must fail on both platforms.|
|Parity tests               |Identical policy config applied on both Linux and Windows; verify that the same operations are allowed/denied on both platforms. Document any divergences.                |
|Backward compatibility     |Verify that Omnipus runs correctly on Windows 7 (no Job Object nesting, no integrity levels) with graceful fallback to application-level checks.                           |

### A.6.3 Known Limitations vs Linux

|Area                  |Linux (Landlock + seccomp)                  |Windows (Job + Token + DACL)                        |Gap                                                                                         |
|----------------------|--------------------------------------------|----------------------------------------------------|--------------------------------------------------------------------------------------------|
|Filesystem granularity|Per-path, per-operation (read/write/execute)|Per-path via DACL; per-operation via integrity level|Minor — Windows is slightly coarser                                                         |
|Syscall filtering     |Per-syscall BPF program                     |Per-privilege removal only                          |Moderate — Windows cannot block individual syscalls, only remove privileges that enable them|
|Self-application      |Process restricts itself (unprivileged)     |Process creates restricted child (unprivileged)     |Architectural difference, not a gap — both work without admin                               |
|Non-NTFS volumes      |N/A (all Linux filesystems support Landlock)|DACLs don’t work on FAT32/exFAT                     |Minor — rare in enterprise, logged as warning                                               |
|Network control       |Out of scope (needs root)                   |Out of scope (needs admin)                          |Parity — both excluded                                                                      |

-----

## A.7 Delivery Integration

The Windows kernel security features integrate into the main BRD delivery phases as follows:

|Phase   |Main BRD Deliverables                   |Windows Appendix Additions                                                                                                                                                                                         |
|--------|----------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Phase 1 |Landlock, seccomp, core policy engine   |WIN-01 (Job Objects), WIN-02 (child containment), WIN-05 (Restricted Tokens), WIN-08 (DACL filesystem), WIN-09 (temp dir), WIN-10 (NTFS detection), WIN-11 (dangerous cmd blocking), Platform abstraction interface|
|Phase 2 |RBAC, exec approvals, skill verification|WIN-03 (UI restrictions), WIN-06 (deny-only SIDs), WIN-07 (integrity level), WIN-12 (PowerShell policy)                                                                                                            |
|Phase 3 |Ecosystem, extensibility                |WIN-04 (CPU/memory limits), Windows Credential Manager integration (stretch)                                                                                                                                       |

**Note:** Team composition, timeline, and effort estimates to be determined after prioritization and detailed specification.

-----

## A.8 Decision Log

|Decision                                                         |Rationale                                                                                                                                                                                                                                          |
|-----------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Use Job Objects + Restricted Tokens instead of Windows containers|Windows containers require Hyper-V or process isolation mode, both needing Administrator and significant overhead. Job Objects + Restricted Tokens work unprivileged and add negligible overhead — consistent with Omnipus’s lightweight philosophy.|
|Require NTFS for full security enforcement                       |NTFS is the default and dominant filesystem on all Windows versions since XP. FAT32/exFAT deployments are edge cases (USB drives, SD cards). Graceful fallback with a clear warning is acceptable.                                                 |
|Exclude WFP network filtering                                    |Requires Administrator privileges. Inconsistent with Omnipus’s unprivileged execution model. Network egress control is out of scope on both Linux and Windows in the current BRD.                                                                   |
|Support Windows 8+ for full features, Windows 7 as fallback      |Nested Job Objects (needed for agents spawning sub-agents within jobs) require Windows 8. Windows 7 is EOL and represents <1% of enterprise deployments.                                                                                           |
|Platform abstraction interface in Go                             |Keeps the policy engine and audit logging completely cross-platform. Only the enforcement backend varies. Enables future macOS/FreeBSD backends without touching the core.                                                                         |

-----

*End of Appendix A*