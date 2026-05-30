# Feature Specification: Wave 2 — Security Layer

**Created**: 2026-03-28
**Status**: Draft
**Input**: Omnipus BRD v1.0 sections 5.1-5.7.1, Appendix E (data model), Wave 2 discovery findings

---

## User Stories & Acceptance Criteria

### User Story 1 — Landlock Filesystem Sandboxing (Priority: P0)

An operator deploying Omnipus on a Linux 5.13+ server wants agent processes restricted to their workspace directories at the kernel level, so that even if an agent or its child processes attempt to escape the workspace via path traversal or symlink exploitation, the kernel blocks the access before it reaches the filesystem.

**Why this priority**: Kernel-level filesystem isolation is the foundation of the entire security layer. Without it, all other policy enforcement is application-level only and bypassable by child processes.

**Independent Test**: Deploy Omnipus on a Linux 5.13+ kernel, configure an agent with a workspace path, and verify that attempts to read/write outside the workspace are blocked with EACCES at the syscall level.

**Acceptance Scenarios**:

1. **Given** a Linux 5.13+ kernel with Landlock enabled, **When** Omnipus starts with `agents.defaults.restrict_to_workspace: true`, **Then** each agent's file operations are restricted to its workspace directory and configured allowed paths via Landlock ABI.
2. **Given** a Linux kernel older than 5.13, **When** Omnipus starts, **Then** it falls back to application-level path checks, logs a warning including the detected kernel version, and continues operating.
3. **Given** Landlock ABI v1 (no file truncation restriction), **When** Omnipus detects the ABI version at startup, **Then** it uses the maximum available ABI features and logs which ABI version is active and which restrictions are not enforceable.

---

### User Story 2 — Seccomp Syscall Filtering (Priority: P0)

An operator wants to prevent agent-spawned processes from performing privilege escalation, loading kernel modules, or creating raw sockets, so that even compromised or malicious tool output cannot elevate privileges or open unauthorized network connections.

**Why this priority**: Seccomp is the second kernel-level enforcement mechanism. Combined with Landlock, it provides defense-in-depth against process escape.

**Independent Test**: Start an agent, have it execute a tool that attempts a blocked syscall (e.g., `socket(AF_PACKET, ...)`), and verify the syscall is blocked and an audit event is logged.

**Acceptance Scenarios**:

1. **Given** a Linux 3.17+ kernel, **When** the exec tool spawns a child process, **Then** a seccomp-BPF filter is applied blocking dangerous syscalls (ptrace, mount, init_module, finit_module, create_module, socket with AF_PACKET/AF_NETLINK, reboot, swapon, swapoff, pivot_root, kexec_load, bpf).
2. **Given** a non-Linux platform (macOS, Windows), **When** Omnipus starts, **Then** seccomp is silently skipped (no-op), with an info-level log message noting the platform does not support seccomp.
3. **Given** a process that triggers a blocked syscall, **When** seccomp intercepts the call, **Then** the process receives EPERM (not SIGKILL) and an audit log entry is written with the blocked syscall name.

---

### User Story 3 — Child Process Sandbox Inheritance (Priority: P1)

An operator wants assurance that when an agent's exec tool runs a shell command that itself spawns grandchild processes (e.g., `npm install` spawning `node-gyp`), those grandchildren inherit the same Landlock and seccomp restrictions as the parent.

**Why this priority**: Without inheritance, the entire sandbox is trivially bypassable by spawning a child process.

**Independent Test**: Execute a script via the exec tool that forks a child, have the child attempt to access a path outside the sandbox, and verify it is blocked.

**Acceptance Scenarios**:

1. **Given** Landlock is active, **When** an exec tool child process forks, **Then** the grandchild inherits the same Landlock filesystem restrictions (Landlock provides this natively).
2. **Given** seccomp is active, **When** an exec tool child process is created, **Then** the seccomp filter is applied with `SECCOMP_FILTER_FLAG_TSYNC` so all threads in the child process are covered.
3. **Given** both Landlock and seccomp are active, **When** a multi-stage build tool runs (e.g., `make` calling `gcc` calling `as`), **Then** every process in the chain is sandboxed.

---

### User Story 4 — Tool Allow/Deny Lists Per Agent (Priority: P0)

An operator configuring multiple agents wants to grant each agent only the tools it needs — e.g., a researcher agent gets `web_search` and `web_fetch` but not `exec` or `file.write` — so that the blast radius of any single compromised agent is minimized.

**Why this priority**: Tool permissions are the most direct, easiest-to-understand security control and the foundation of the policy engine.

**Independent Test**: Configure an agent with `tools.allow: ["web_search"]`, invoke `file.write` via that agent, and verify it is denied with an explainable reason.

**Acceptance Scenarios**:

1. **Given** an agent with `tools.allow: ["web_search", "web_fetch"]`, **When** the agent attempts to invoke `exec`, **Then** the call is denied and an audit event logged with `policy_rule: "tool 'exec' not in tools.allow"`.
2. **Given** an agent with `tools.deny: ["exec", "file.write"]`, **When** the agent invokes `web_search`, **Then** the call is allowed.
3. **Given** an agent with both `tools.allow` and `tools.deny` set, **When** a tool appears in both lists, **Then** deny takes precedence and the tool is blocked.

---

### User Story 5 — Per-Binary Execution Control (Priority: P0)

An operator wants to restrict which binaries the exec tool can run — allowing `git`, `npm`, and `python3` but blocking `curl`, `wget`, and arbitrary scripts — so that agents cannot download or execute untrusted code.

**Why this priority**: The exec tool is the highest-risk tool in the system. Binary allowlisting is the primary control for limiting its attack surface.

**Independent Test**: Configure `tools.exec.allowed_binaries: ["git *", "npm *"]`, attempt to run `curl http://evil.com`, and verify it is blocked.

**Acceptance Scenarios**:

1. **Given** `tools.exec.allowed_binaries: ["git *", "npm run *", "python3 *.py"]`, **When** the agent calls exec with `git status`, **Then** the command is allowed.
2. **Given** the same allowlist, **When** the agent calls exec with `curl http://example.com`, **Then** the command is denied with `policy_rule: "binary 'curl' not in exec allowlist"`.
3. **Given** a glob pattern `npm *`, **When** the agent calls exec with `npm install --save lodash`, **Then** the command matches the glob and is allowed.
4. **Given** an empty or missing `allowed_binaries` list and `security.default_policy: "deny"`, **When** any exec command is attempted, **Then** all exec commands are denied.

---

### User Story 6 — Deny-by-Default Policy Model (Priority: P0)

An operator in a regulated environment wants to start with zero permissions and explicitly grant only what each agent needs, so that accidental over-permissioning is structurally impossible.

**Why this priority**: Deny-by-default is a fundamental security posture that changes the semantics of every other permission check. It must be implemented early so all other features build on it.

**Independent Test**: Set `security.default_policy: "deny"`, create an agent with no `tools.allow` list, and verify all tool invocations are denied.

**Acceptance Scenarios**:

1. **Given** `security.default_policy: "deny"` and an agent with no `tools` section, **When** the agent invokes any tool, **Then** the tool is denied.
2. **Given** `security.default_policy: "deny"` and an agent with `tools.allow: ["web_search"]`, **When** the agent invokes `web_search`, **Then** the tool is allowed.
3. **Given** `security.default_policy: "allow"` (default/backward-compatible), **When** an agent with no `tools` section invokes any tool, **Then** all tools are allowed (Omnipus-compatible behavior).

---

### User Story 7 — Exec Approval Prompt (Priority: P1)

An operator running Omnipus interactively wants to review and approve each exec command before it runs, with the option to "Always Allow" a pattern so recurring commands do not require repeated approval.

**Why this priority**: Interactive approval is the human-in-the-loop control for the highest-risk tool. Needed before exec is safe for interactive use.

**Independent Test**: Run Omnipus in CLI mode, trigger an exec tool call, verify the approval prompt appears, approve it, then verify "Always Allow" persists the pattern.

**Acceptance Scenarios**:

1. **Given** `tools.exec.approval: "ask"` and CLI mode, **When** the agent requests exec of `git status`, **Then** a prompt displays the full command and waits for Allow/Deny/Always Allow input.
2. **Given** the user selects "Always Allow" for `git *`, **When** the agent later requests `git diff`, **Then** the command is auto-approved without prompting.
3. **Given** `tools.exec.approval: "off"`, **When** the agent requests any exec, **Then** no prompt is shown and the command proceeds (subject to allowlist checks).
4. **Given** a headless/automated deployment, **When** `tools.exec.approval` is not explicitly set, **Then** it defaults to `"ask"` (safe default).

---

### User Story 8 — Declarative JSON Policy Files (Priority: P0)

An operator wants to define security policies in a structured, version-controllable JSON format within `config.json`, so that policies can be reviewed in pull requests, audited, and consistently applied across deployments.

**Why this priority**: The policy file format is the interface between operator intent and the policy engine. Every other security feature reads from it.

**Independent Test**: Write a `security.policy` section in `config.json`, start Omnipus, and verify policies are loaded and enforced.

**Acceptance Scenarios**:

1. **Given** a `config.json` with `security.policy.filesystem.allowed_paths: ["/tmp", "~/.omnipus/agents/*/"]`, **When** Omnipus starts, **Then** the Landlock ruleset (or fallback) includes those paths.
2. **Given** a `config.json` with `security.policy.exec.allowed_binaries`, **When** Omnipus starts, **Then** the exec allowlist is populated from this config.
3. **Given** a malformed `security.policy` section (e.g., invalid JSON, unknown fields), **When** Omnipus starts, **Then** it refuses to start with a clear error message identifying the malformed field and line.

---

### User Story 9 — Structured Audit Logging (Priority: P0)

A compliance officer wants every security-relevant action (tool calls, exec commands, file operations, permission decisions) logged in structured JSON format, so that incidents can be investigated and compliance reports generated.

**Why this priority**: Audit logging is the observability foundation. Without it, no security event can be reviewed or investigated after the fact.

**Independent Test**: Perform various agent actions, then read `~/.omnipus/system/audit.jsonl` and verify each action produced a well-formed JSON line with the expected fields.

**Acceptance Scenarios**:

1. **Given** audit logging is enabled, **When** an agent invokes a tool, **Then** an audit entry is appended to `audit.jsonl` with fields: timestamp, event, decision, agent_id, session_id, tool, parameters (redacted), policy_rule.
2. **Given** audit output is configured for `"file"`, **When** the audit log file reaches 50MB, **Then** it is rotated to `audit-YYYY-MM-DD.jsonl` and a new file is started.
3. **Given** audit output is configured for `"stdout"`, **When** a security event occurs, **Then** the event is written to stdout in JSON format via Go's `slog` package.
4. **Given** rotated audit files older than 90 days (default), **When** the daily retention check runs, **Then** expired files are deleted and the deletion is logged.

---

### User Story 10 — Log Redaction (Priority: P0)

An operator wants sensitive data (API keys, tokens, email addresses, passwords) automatically scrubbed from audit logs, so that log files can be shared with security teams or stored in centralized log systems without leaking credentials.

**Why this priority**: Without redaction, audit logs themselves become a credential leak vector, undermining the security they are meant to provide.

**Independent Test**: Invoke a tool with an API key in the parameters, read the audit log, and verify the key is replaced with `[REDACTED]`.

**Acceptance Scenarios**:

1. **Given** default redaction patterns are active, **When** a tool call includes a parameter value matching `sk-[a-zA-Z0-9]{20,}`, **Then** the value is replaced with `[REDACTED]` in the audit log entry.
2. **Given** custom redaction patterns in `security.audit.redaction_patterns`, **When** a matching value appears in any log field, **Then** it is redacted.
3. **Given** redaction is disabled (`security.audit.redaction: false`), **When** sensitive values appear in log output, **Then** they are logged as-is (operator explicitly accepted this risk).

---

### User Story 11 — Explainable Policy Decisions (Priority: P1)

An operator debugging a permission denial wants to see exactly which policy rule caused the denial, so that they can adjust policies without guesswork.

**Why this priority**: Explainable decisions reduce operator frustration and make the policy engine usable in practice. Without them, deny-by-default becomes a "deny-everything-and-hope" model.

**Independent Test**: Configure a policy that denies a tool, attempt to invoke that tool, and verify the audit log and error response both include the matching rule.

**Acceptance Scenarios**:

1. **Given** a tool call is denied, **When** the denial is logged, **Then** the audit entry includes `policy_rule` explaining the match (e.g., `"tool 'exec' not in tools.allow for agent 'researcher'"` or `"binary 'curl' not in exec allowlist"`).
2. **Given** a tool call is allowed, **When** the allow is logged, **Then** the audit entry includes `policy_rule` explaining why (e.g., `"tools.allow matched 'web_search'"` or `"security.default_policy is 'allow', no deny rule matched"`).
3. **Given** multiple policy rules could apply, **When** a decision is made, **Then** the `policy_rule` field indicates the first matching rule in evaluation order.

---

### User Story 12 — SSRF Protection (Priority: P0)

An operator wants to prevent agents from making HTTP requests to internal infrastructure (private IPs, cloud metadata endpoints), so that a compromised agent cannot scan internal networks or steal cloud credentials from metadata services.

**Why this priority**: SSRF against cloud metadata (169.254.169.254) is a well-known, high-severity attack vector. Blocking it is a minimal baseline for any tool that makes HTTP requests.

**Independent Test**: Have an agent attempt `web_fetch` to `http://169.254.169.254/latest/meta-data/`, verify the request is blocked and an audit event is logged.

**Acceptance Scenarios**:

1. **Given** SSRF protection is enabled (default), **When** an agent's `web_fetch` targets `http://169.254.169.254/`, **Then** the request is blocked before any TCP connection is made, and an audit entry is logged with `decision: "deny"` and `policy_rule: "SSRF: blocked cloud metadata endpoint"`.
2. **Given** SSRF protection is enabled, **When** an agent targets `http://10.0.0.5/api`, **Then** the request is blocked with `policy_rule: "SSRF: blocked private IP range 10.0.0.0/8"`.
3. **Given** `security.ssrf.allow_internal: ["10.0.0.5"]`, **When** the agent targets `http://10.0.0.5/api`, **Then** the request is allowed (allowlisted).
4. **Given** an agent targets a hostname that resolves to a private IP, **When** DNS resolution completes, **Then** the resolved IP is checked against SSRF rules before the connection is established (DNS rebinding protection).

---

### User Story 13 — Rate Limiting (Priority: P0)

An operator wants to cap agent activity at three levels (per-agent, per-channel, global cost), so that a runaway agent cannot exhaust API quotas, violate platform rate limits, or accumulate unbounded costs.

**Why this priority**: Without rate limiting, a single misconfigured agent loop can drain an API budget in minutes.

**Independent Test**: Set a per-agent limit of 5 tool calls/minute, trigger 6 calls in rapid succession, and verify the 6th is rejected with a `retry_after_seconds` value.

**Acceptance Scenarios**:

1. **Given** per-agent rate limit of `10 llm_calls/hour`, **When** the agent makes the 11th LLM call in the current hour window, **Then** the call is rejected with `retry_after_seconds` indicating when the window slides enough for a new call.
2. **Given** per-channel rate limit of `30 messages/minute` for Telegram, **When** the agent attempts the 31st outbound Telegram message, **Then** the message is rejected (not queued).
3. **Given** a global daily cost cap of `$50`, **When** cumulative session cost across all agents reaches $50, **Then** all further LLM calls are rejected with `policy_rule: "global daily cost cap exceeded ($50.00)"`.
4. **Given** the system agent, **When** it performs operations, **Then** rate limits do not apply to it, but operations are still audit-logged.
5. **Given** Omnipus restarts, **When** per-agent and per-channel rate limit state was in-memory, **Then** those counters reset to zero (fresh sliding window). Global cost cap persists via session stats on disk.

---

### User Story 14 — Exec Tool HTTP Proxy (Priority: P1)

An operator wants child processes spawned by the exec tool to have their HTTP traffic routed through a local proxy that enforces SSRF rules, so that `curl`, `wget`, `pip install`, and similar tools cannot reach internal infrastructure.

**Why this priority**: Without proxy enforcement, the exec tool provides an easy SSRF bypass. Best-effort coverage is better than none.

**Independent Test**: Run `curl http://169.254.169.254/` via the exec tool, verify the proxy intercepts and blocks the request.

**Acceptance Scenarios**:

1. **Given** SEC-28 proxy is enabled, **When** the exec tool spawns a child process, **Then** `HTTP_PROXY` and `HTTPS_PROXY` environment variables are set to the local proxy address.
2. **Given** the proxy is running and a child process requests `http://10.0.0.1/`, **Then** the proxy blocks the request applying the same SSRF rules as SEC-24 and logs the blocked request to the audit log.
3. **Given** a child process ignores proxy environment variables, **When** it makes a direct connection to a private IP, **Then** the connection succeeds (known limitation LIM-02, best-effort only).
4. **Given** no exec tool processes are running, **When** checking for the proxy, **Then** the proxy is not active (it only runs while exec processes are active).

---

### User Story 15 — DM Policy Safety Checks (Priority: P1)

An operator configuring a Telegram or Discord channel wants to be warned if the configuration is overly permissive (e.g., accepting messages from anyone without an `allow_from` list), so that accidental exposure of the agent to untrusted users is detected before deployment.

**Why this priority**: Misconfigured DM channels are a common source of unauthorized agent access. Proactive detection during `omnipus doctor` catches this before production.

**Independent Test**: Configure a Telegram channel with no `allow_from` restriction, run `omnipus doctor`, and verify it warns about the open DM policy.

**Acceptance Scenarios**:

1. **Given** a Telegram channel with `policies.allow_from: []` (empty, meaning anyone), **When** `omnipus doctor` runs, **Then** it reports a warning: "Telegram channel accepts messages from anyone. Set policies.allow_from to restrict access."
2. **Given** a Discord channel in a public server without `allow_from`, **When** `omnipus doctor` runs, **Then** it reports a similar warning.
3. **Given** all channels have `allow_from` configured with at least one user/group, **When** `omnipus doctor` runs, **Then** no DM policy warnings are produced.

---

## Behavioral Contract

### Primary flows:

- When Omnipus starts on Linux 5.13+, the system detects Landlock ABI version, initializes the sandbox backend with the highest available ABI, and logs the active enforcement level.
- When Omnipus starts on Linux 3.17+ (but <5.13), the system initializes seccomp-only enforcement and falls back to application-level filesystem checks.
- When Omnipus starts on non-Linux, the system uses the Fallback sandbox backend with application-level enforcement only.
- When an agent invokes a tool, the policy engine evaluates: (1) default policy, (2) agent-level tool allow/deny, (3) per-binary exec allowlist. The first matching deny wins. If default is "deny", explicit allow is required.
- When a tool call is permitted, the action executes and an audit entry with `decision: "allow"` is appended.
- When a tool call is denied, the agent receives an error with the policy rule explanation, and an audit entry with `decision: "deny"` is appended.
- When rate limits are hit, the operation is rejected with `retry_after_seconds` — no silent queueing, no silent dropping.
- When an HTTP request targets a private IP or cloud metadata endpoint, the SSRF filter blocks it before any TCP connection.

### Error flows:

- When `config.json` contains a malformed `security` section, Omnipus refuses to start and prints a diagnostic error message identifying the invalid field.
- When the audit log file cannot be opened or written, Omnipus logs the error to stderr and continues operating without audit logging (degraded mode), but logs a critical-severity alert.
- When the Landlock `landlock_create_ruleset` syscall fails unexpectedly (not due to missing support), Omnipus logs the error and falls back to application-level enforcement.
- When the seccomp `prctl(PR_SET_SECCOMP)` call fails, Omnipus logs the error and continues without seccomp (graceful degradation).
- When the HTTP proxy for exec fails to bind to a loopback port, Omnipus logs the error and spawns exec processes without proxy env vars.
- When audit log rotation fails (disk full, permissions), the system continues writing to the current file and logs the rotation failure to stderr.

### Boundary conditions:

- When the audit log file is exactly 50MB, the next write triggers rotation.
- When a rate limit window boundary is crossed mid-request, the request that crossed the boundary is counted in the new window.
- When `security.default_policy` is missing from config, it defaults to `"allow"` for backward compatibility with Omnipus configs.
- When an agent has both `tools.allow` and `tools.deny` with overlapping entries, `deny` takes precedence.
- When the global cost cap is set to 0, all LLM calls are denied (emergency stop).
- When the system agent performs operations, rate limits are bypassed but audit logging still applies.

---

## Edge Cases

- **Unsupported kernel (Linux < 3.17)**: Landlock and seccomp are both unavailable. System uses Fallback backend (application-level checks only). `omnipus doctor` reports "No kernel sandbox available" with risk score impact.
- **Partial Landlock ABI (v1 only, kernel 5.13-5.18)**: File truncation restriction (`LANDLOCK_ACCESS_FS_TRUNCATE`) is not available. System enforces available restrictions, logs which ABI features are missing. Not treated as an error.
- **Landlock + seccomp interaction**: Both are applied independently. A seccomp rule that blocks `open` could conflict with Landlock's filesystem rules. Resolution: seccomp filters block only privilege-escalation syscalls, not filesystem syscalls (those are Landlock's domain).
- **Policy file with valid JSON but semantically invalid values** (e.g., `"default_policy": "maybe"`): Detected at startup with a validation error listing acceptable values.
- **Rate limit with exactly one remaining call and two concurrent requests**: The sliding window uses atomic operations. One request succeeds, the other is rejected with `retry_after_seconds`.
- **Concurrent policy reads during startup**: Policy is loaded once at startup into an immutable in-memory structure. Concurrent reads are safe; no locking needed for reads.
- **SSRF with IPv6**: Private IPv6 ranges (::1, fe80::/10, fc00::/7, ::ffff:10.0.0.0/104 mapped) are blocked alongside IPv4 private ranges.
- **SSRF with DNS CNAME chains**: DNS resolution follows CNAME chains to the final A/AAAA record, then checks the resolved IP against SSRF rules. This prevents CNAME-based SSRF bypass.
- **Audit log corruption (partial write on crash)**: JSONL format means corruption affects at most the last line. On startup, the system validates the last line of `audit.jsonl` and truncates if it is malformed JSON.
- **Exec allowlist with overlapping globs**: Globs are evaluated in order. First match wins. `["git *", "git push *"]` — `git push` matches the first pattern.
- **Rate limit counter overflow**: Sliding window counters use `int64`. At 1 call per nanosecond, overflow would take ~292 years. Not a practical concern.
- **Empty `tools.allow` array with `default_policy: "allow"`**: Interpreted as "allow nothing" (explicit empty allow list means no tools permitted, not "use default"). This matches the principle that explicit config overrides defaults.
- **Symlink inside allowed Landlock path pointing outside**: Landlock operates on the resolved path. A symlink `~/workspace/escape -> /etc/passwd` would resolve to `/etc/passwd`, which is outside the allowed paths and blocked by Landlock.
- **SSRF with HTTP redirect to private IP**: After receiving a 3xx redirect, the new target URL is checked against SSRF rules before following the redirect.

---

## Explicit Non-Behaviors

- The system must not use CGo or external C libraries for Landlock or seccomp because the project requires pure Go using `golang.org/x/sys/unix`.
- The system must not kill processes with SIGKILL when seccomp blocks a syscall because returning EPERM allows the process to handle the error gracefully and provides better diagnostics.
- The system must not queue rate-limited operations for later execution because silent queueing masks the problem from the agent loop; explicit rejection with `retry_after_seconds` gives the agent actionable information.
- The system must not hot-reload security-critical policies (filesystem paths, seccomp filters, exec allowlists) at runtime because SEC-12 requires static policies loaded once at startup for security boundaries.
- The system must not store rate limit state on disk (except global cost cap) because per-agent and per-channel counters resetting on restart is acceptable and avoids disk I/O overhead.
- The system must not implement Windows Job Objects in Wave 2 because the BRD scopes Windows kernel sandboxing to Phase 2; Wave 2 uses Fallback (app-level) on Windows.
- The system must not implement RBAC (SEC-19), gateway authentication (SEC-20), device pairing (SEC-21), credential management (SEC-22/23), skill trust verification (SEC-09), two-layer policy enforcement (SEC-10), hot-reloadable policies (SEC-13), policy change approval (SEC-14), or tamper-evident log chains (SEC-18) in Wave 2 because these are either out-of-scope priorities or scheduled for later waves.
- The system must not apply rate limits to the system agent because Appendix D specifies system agent exemption; operations are still audit-logged.
- The system must not block network connections at the kernel level because LIM-01 establishes this is outside the unprivileged execution model; SSRF protection operates at the application HTTP layer only.

---

## Integration Boundaries

### Linux Kernel (Landlock LSM)

- **Data in**: Landlock ABI version query (`landlock_create_ruleset` with flags), ruleset file descriptors, path access rules
- **Data out**: File descriptor for ruleset, EACCES errors on blocked access
- **Contract**: Syscalls via `golang.org/x/sys/unix` — `SYS_LANDLOCK_CREATE_RULESET`, `SYS_LANDLOCK_ADD_RULE`, `SYS_LANDLOCK_RESTRICT_SELF`. ABI version negotiation per kernel docs.
- **On failure**: If `landlock_create_ruleset` returns ENOSYS or EOPNOTSUPP, fall back to Fallback backend. Log the failure.
- **Development**: Real kernel on Linux CI; mock/simulated for unit tests and non-Linux CI

### Linux Kernel (Seccomp-BPF)

- **Data in**: BPF program (sock_filter array), `prctl(PR_SET_NO_NEW_PRIVS)`, `prctl(PR_SET_SECCOMP)`
- **Data out**: EPERM on blocked syscalls
- **Contract**: BPF program assembled in Go using `golang.org/x/sys/unix` types. Applied via `prctl` after `PR_SET_NO_NEW_PRIVS`.
- **On failure**: If `prctl` fails, continue without seccomp. Log the error.
- **Development**: Real kernel on Linux CI; no-op stub for non-Linux

### Filesystem (Audit Log)

- **Data in**: Structured audit events (JSON objects)
- **Data out**: JSONL file at `~/.omnipus/system/audit.jsonl`
- **Contract**: Append-only writes. Atomic where possible (single write call per line). Rotation at 50MB or daily.
- **On failure**: Log to stderr, continue without file audit. Never crash due to audit write failure.
- **Development**: Real filesystem; tempdir in tests

### Config System (Wave 1)

- **Data in**: `config.json` read at startup
- **Data out**: Parsed security policy structures (immutable after startup)
- **Contract**: JSON format. `security` section defined in Appendix E §E.20. Unknown fields are ignored (forward-compatible). Required fields validated at startup.
- **On failure**: Malformed security config prevents startup with descriptive error.
- **Development**: In-memory config objects in tests

### Go `slog` Package

- **Data in**: Structured log records (audit events, diagnostic messages)
- **Data out**: JSON-formatted log lines to configured outputs (file, stdout)
- **Contract**: `slog.Handler` interface. Custom handler for audit log with redaction middleware.
- **On failure**: `slog` itself does not fail; output handler failures handled per output target.
- **Development**: `slog` test handler captures log records for assertions

---

## BDD Scenarios

### Feature: Landlock Filesystem Sandboxing

#### Scenario: Landlock restricts agent to workspace on supported kernel

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the host kernel is Linux 5.13+ with Landlock enabled
- **And** `config.json` has `agents.defaults.restrict_to_workspace: true`
- **And** agent "researcher" has workspace `~/.omnipus/agents/researcher/`
- **When** the agent attempts to read `/etc/passwd`
- **Then** the read fails with EACCES
- **And** an audit entry is logged with `event: "file_op"`, `decision: "deny"`, `policy_rule: "Landlock: path outside allowed ruleset"`

#### Scenario: Graceful fallback on unsupported kernel

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the host kernel is Linux 4.15 (no Landlock support)
- **When** Omnipus starts
- **Then** the sandbox backend is set to Fallback
- **And** a warning log is emitted: "Landlock not available (kernel 4.15 < 5.13). Using application-level filesystem enforcement."
- **And** the agent's file operations are restricted by application-level path checks

#### Scenario Outline: Landlock ABI version detection

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the host kernel supports Landlock ABI version `<abi_version>`
- **When** Omnipus detects the ABI at startup
- **Then** the system uses ABI `<abi_version>` features
- **And** logs "Landlock ABI v`<abi_version>` active. Restrictions: `<restrictions>`"

**Examples**:

| abi_version | restrictions |
|-------------|-------------|
| 1 | read, write, execute, readdir, remove, make_dir, make_reg, make_sock, make_fifo, make_block, make_sym, refer |
| 2 | v1 + truncate |
| 3 | v2 + ioctl_dev |

---

### Feature: Seccomp Syscall Filtering

#### Scenario: Dangerous syscall blocked in exec child

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the host kernel is Linux 3.17+
- **And** the exec tool spawns a child process
- **When** the child process attempts `ptrace(PTRACE_ATTACH, ...)`
- **Then** the syscall returns EPERM
- **And** an audit entry is logged with `event: "exec"`, `decision: "deny"`, `policy_rule: "seccomp: blocked syscall ptrace"`

#### Scenario: Seccomp is no-op on non-Linux

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the host platform is macOS
- **When** Omnipus starts
- **Then** the seccomp subsystem is not initialized
- **And** an info log is emitted: "Seccomp not available on darwin. Skipping syscall filtering."

---

### Feature: Child Process Sandbox Inheritance

#### Scenario: Grandchild inherits Landlock restrictions

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** Landlock is active with workspace `/tmp/agent-workspace/`
- **And** the exec tool runs `bash -c "bash -c 'cat /etc/shadow'"`
- **When** the innermost bash attempts to read `/etc/shadow`
- **Then** the read fails with EACCES

#### Scenario: Seccomp TSYNC covers all threads

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** seccomp is active
- **When** the exec tool spawns a multi-threaded child process
- **Then** all threads in the child are covered by the seccomp filter (SECCOMP_FILTER_FLAG_TSYNC)

---

### Feature: Tool Allow/Deny Lists

#### Scenario: Tool not in allow list is denied

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent "researcher" has `tools.allow: ["web_search", "web_fetch"]`
- **When** agent "researcher" invokes tool "exec"
- **Then** the invocation is denied
- **And** an audit entry is logged with `policy_rule: "tool 'exec' not in tools.allow for agent 'researcher'"`

#### Scenario: Tool in deny list is blocked even if default is allow

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `security.default_policy: "allow"`
- **And** agent "researcher" has `tools.deny: ["exec"]`
- **When** agent "researcher" invokes tool "exec"
- **Then** the invocation is denied with `policy_rule: "tool 'exec' in tools.deny for agent 'researcher'"`

#### Scenario: Deny takes precedence over allow

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** agent "researcher" has `tools.allow: ["exec"]` and `tools.deny: ["exec"]`
- **When** agent "researcher" invokes tool "exec"
- **Then** the invocation is denied with `policy_rule: "tool 'exec' in tools.deny (deny takes precedence over allow)"`

---

### Feature: Per-Binary Execution Control

#### Scenario: Allowed binary pattern matches

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `tools.exec.allowed_binaries: ["git *", "npm run *"]`
- **When** the agent calls exec with command `git status`
- **Then** the command is allowed

#### Scenario: Disallowed binary is blocked

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `tools.exec.allowed_binaries: ["git *", "npm run *"]`
- **When** the agent calls exec with command `curl http://example.com`
- **Then** the command is denied with `policy_rule: "binary 'curl' not in exec allowlist"`

#### Scenario: Empty allowlist with deny-by-default blocks all exec

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case

- **Given** `security.default_policy: "deny"` and `tools.exec.allowed_binaries: []`
- **When** the agent calls exec with any command
- **Then** the command is denied

---

### Feature: Deny-by-Default Policy Model

#### Scenario: No tools available without explicit allow

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `security.default_policy: "deny"`
- **And** agent "researcher" has no `tools` section in config
- **When** the agent invokes `web_search`
- **Then** the invocation is denied with `policy_rule: "default_policy is 'deny', no allow rule for tool 'web_search'"`

#### Scenario: Backward-compatible allow-by-default

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** `security.default_policy: "allow"` (or absent from config)
- **And** agent "researcher" has no `tools` section
- **When** the agent invokes `web_search`
- **Then** the invocation is allowed

---

### Feature: Exec Approval Prompt

#### Scenario: Interactive approval in CLI mode

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `tools.exec.approval: "ask"` and Omnipus is running in CLI mode
- **When** agent requests exec of `git status`
- **Then** a prompt is displayed showing the full command
- **And** the system waits for user input (Allow / Deny / Always Allow)

#### Scenario: Always Allow persists pattern

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the user selected "Always Allow" for pattern `git *`
- **When** agent later requests exec of `git diff`
- **Then** the command is auto-approved without prompting
- **And** an audit entry is logged with `policy_rule: "exec auto-approved: pattern 'git *' in persistent allowlist"`

---

### Feature: Structured Audit Logging

#### Scenario: Tool call produces audit entry

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** audit logging is enabled (default)
- **When** agent "general-assistant" invokes tool "web_search" with parameters `{"query": "AWS pricing"}`
- **Then** a line is appended to `~/.omnipus/system/audit.jsonl`
- **And** the line contains valid JSON with fields: timestamp (ISO 8601), event ("tool_call"), decision ("allow"), agent_id ("general-assistant"), tool ("web_search"), parameters, policy_rule

#### Scenario: Audit log rotation at 50MB

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** `audit.jsonl` is 49.9MB
- **When** a write pushes it past 50MB
- **Then** the current file is renamed to `audit-2026-03-28.jsonl`
- **And** a new `audit.jsonl` is created for subsequent writes

---

### Feature: Log Redaction

#### Scenario: API key pattern is redacted

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** default redaction patterns are active
- **When** a tool call includes parameter `{"api_key": "sk-ant-abc123def456ghi789jkl012mno345"}`
- **Then** the audit log entry contains `{"api_key": "[REDACTED]"}`

#### Scenario: Custom redaction pattern

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `security.audit.redaction_patterns: ["INTERNAL-[0-9]{6}"]`
- **When** a tool call includes `"ref": "INTERNAL-123456"`
- **Then** the audit log entry contains `"ref": "[REDACTED]"`

---

### Feature: Explainable Policy Decisions

#### Scenario: Denial includes matching rule

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent "researcher" has `tools.allow: ["web_search"]`
- **When** the agent invokes "exec"
- **Then** the error response includes `"policy_rule": "tool 'exec' not in tools.allow for agent 'researcher'"`
- **And** the audit log entry includes the same policy_rule

#### Scenario: Allow includes matching rule

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Happy Path

- **Given** agent "researcher" has `tools.allow: ["web_search"]`
- **When** the agent invokes "web_search"
- **Then** the audit log entry includes `"policy_rule": "tools.allow matched 'web_search' for agent 'researcher'"`

---

### Feature: SSRF Protection

#### Scenario: Cloud metadata endpoint blocked

**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path

- **Given** SSRF protection is enabled
- **When** agent calls `web_fetch` with URL `http://169.254.169.254/latest/meta-data/`
- **Then** the request is blocked before any TCP connection
- **And** audit entry logged with `policy_rule: "SSRF: blocked cloud metadata endpoint 169.254.169.254"`

#### Scenario: Private IP blocked

**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Happy Path

- **Given** SSRF protection is enabled
- **When** agent calls `web_fetch` with URL `http://192.168.1.100/admin`
- **Then** the request is blocked with `policy_rule: "SSRF: blocked private IP range 192.168.0.0/16"`

#### Scenario: Allowlisted internal IP is permitted

**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** `security.ssrf.allow_internal: ["10.0.0.5"]`
- **When** agent calls `web_fetch` with URL `http://10.0.0.5/api/data`
- **Then** the request is allowed

#### Scenario: DNS resolves to private IP (rebinding protection)

**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Edge Case

- **Given** SSRF protection is enabled
- **And** hostname `evil.example.com` resolves to `169.254.169.254`
- **When** agent calls `web_fetch` with URL `http://evil.example.com/`
- **Then** the request is blocked after DNS resolution with `policy_rule: "SSRF: hostname resolved to blocked IP 169.254.169.254"`

---

### Feature: Rate Limiting

#### Scenario: Per-agent rate limit rejection

**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent "researcher" has rate limit `10 llm_calls/hour`
- **And** 10 calls have been made in the current window
- **When** the 11th call is attempted
- **Then** the call is rejected with error containing `retry_after_seconds`
- **And** audit entry logged with `decision: "deny"`, `policy_rule: "rate_limit: per-agent llm_calls 10/hour exceeded"`

#### Scenario: Global cost cap stops all agents

**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Happy Path

- **Given** global daily cost cap is `$50`
- **And** cumulative cost for today is `$49.98`
- **When** an LLM call that would cost `$0.05` is attempted
- **Then** the call is rejected with `policy_rule: "global daily cost cap exceeded ($50.00)"`

#### Scenario: System agent exempt from rate limits

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** rate limits are configured
- **When** the system agent (`omnipus-system`) performs an operation
- **Then** the operation proceeds regardless of rate limit counters
- **And** the operation is audit-logged

---

### Feature: Exec Tool HTTP Proxy

#### Scenario: Proxy blocks private IP from child process

**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the exec HTTP proxy is enabled
- **When** the exec tool spawns a child process that runs `curl http://10.0.0.1/secret`
- **Then** the proxy intercepts the request and returns an error
- **And** audit entry logged with `event: "exec"`, `policy_rule: "SSRF proxy: blocked private IP 10.0.0.1"`

#### Scenario: Proxy env vars set on child process

**Traces to**: User Story 14, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the exec HTTP proxy is active
- **When** a child process is spawned
- **Then** the process environment includes `HTTP_PROXY=http://127.0.0.1:<port>` and `HTTPS_PROXY=http://127.0.0.1:<port>`

---

### Feature: DM Policy Safety Checks

#### Scenario: Open Telegram channel flagged

**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Happy Path

- **Given** Telegram channel is enabled with `policies.allow_from: []`
- **When** `omnipus doctor` runs
- **Then** output includes warning: "Telegram channel accepts messages from anyone. Set policies.allow_from to restrict access."

#### Scenario: All channels properly restricted

**Traces to**: User Story 15, Acceptance Scenario 3
**Category**: Happy Path

- **Given** all enabled channels have non-empty `policies.allow_from`
- **When** `omnipus doctor` runs
- **Then** no DM policy warnings are produced

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | Individual functions: SSRF IP checker, glob matcher, policy evaluator, redaction engine, rate limiter, ABI detector, BPF assembler | Validates logic in isolation with no kernel or filesystem dependencies |
| Integration | Sandbox backend + policy engine + audit logger working together; config loading + policy initialization | Validates components compose correctly |
| E2E | Full agent tool invocation through policy engine, sandbox, audit pipeline | Validates complete security enforcement from user perspective |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestSSRFChecker_PrivateIPv4Ranges` | Unit | SSRF: Private IP blocked | Validates all RFC 1918 ranges, link-local, loopback, cloud metadata |
| 2 | `TestSSRFChecker_PrivateIPv6Ranges` | Unit | SSRF: Private IP blocked | Validates IPv6 private ranges (::1, fe80::/10, fc00::/7, mapped IPv4) |
| 3 | `TestSSRFChecker_Allowlist` | Unit | SSRF: Allowlisted internal IP | Validates allowlist overrides for specific IPs |
| 4 | `TestSSRFChecker_DNSRebinding` | Unit | SSRF: DNS rebinding protection | Validates resolved IP is checked after DNS resolution |
| 5 | `TestGlobMatcher_ExecAllowlist` | Unit | Per-Binary: Allowed binary matches | Validates glob pattern matching for exec commands |
| 6 | `TestGlobMatcher_NoMatch` | Unit | Per-Binary: Disallowed binary blocked | Validates non-matching commands are rejected |
| 7 | `TestGlobMatcher_EmptyList` | Unit | Per-Binary: Empty allowlist blocks all | Validates empty list denies everything |
| 8 | `TestPolicyEvaluator_DenyByDefault` | Unit | Deny-by-Default: No tools without allow | Validates deny-by-default with no allow list |
| 9 | `TestPolicyEvaluator_AllowByDefault` | Unit | Deny-by-Default: Backward-compatible | Validates allow-by-default backward compatibility |
| 10 | `TestPolicyEvaluator_ToolAllowList` | Unit | Tool Allow/Deny: Tool not in allow denied | Validates tool filtering by allow list |
| 11 | `TestPolicyEvaluator_ToolDenyList` | Unit | Tool Allow/Deny: Tool in deny blocked | Validates tool filtering by deny list |
| 12 | `TestPolicyEvaluator_DenyPrecedence` | Unit | Tool Allow/Deny: Deny takes precedence | Validates deny overrides allow when both set |
| 13 | `TestPolicyEvaluator_ExplainableDecision` | Unit | Explainable: Denial includes matching rule | Validates policy_rule string is populated on allow and deny |
| 14 | `TestRedactionEngine_DefaultPatterns` | Unit | Redaction: API key pattern redacted | Validates built-in patterns (API keys, tokens, emails) |
| 15 | `TestRedactionEngine_CustomPatterns` | Unit | Redaction: Custom redaction pattern | Validates user-defined regex patterns |
| 16 | `TestRedactionEngine_Disabled` | Unit | Redaction: disabled | Validates passthrough when redaction is off |
| 17 | `TestRateLimiter_SlidingWindow` | Unit | Rate Limiting: Per-agent rejection | Validates sliding window counts and rejection |
| 18 | `TestRateLimiter_RetryAfterSeconds` | Unit | Rate Limiting: Per-agent rejection | Validates retry_after_seconds calculation |
| 19 | `TestRateLimiter_GlobalCostCap` | Unit | Rate Limiting: Global cost cap | Validates daily cost accumulation and cutoff |
| 20 | `TestRateLimiter_SystemAgentExempt` | Unit | Rate Limiting: System agent exempt | Validates system agent bypasses rate limits |
| 21 | `TestRateLimiter_ConcurrentAccess` | Unit | Edge: concurrent rate limit requests | Validates thread-safety under concurrent calls |
| 22 | `TestLandlockDetector_ABIVersion` | Unit | Landlock: ABI version detection | Validates ABI version detection logic (mock syscalls) |
| 23 | `TestSeccompBPF_ProgramAssembly` | Unit | Seccomp: Dangerous syscall blocked | Validates BPF filter program construction |
| 24 | `TestDMSafetyChecker_OpenChannel` | Unit | DM Safety: Open Telegram flagged | Validates detection of open allow_from |
| 25 | `TestDMSafetyChecker_RestrictedChannel` | Unit | DM Safety: All channels restricted | Validates no warning for restricted channels |
| 26 | `TestConfigLoader_SecuritySection` | Integration | Policy Files: valid config | Validates security config parsing and validation |
| 27 | `TestConfigLoader_MalformedSecurity` | Integration | Policy Files: malformed config | Validates startup failure on invalid security config |
| 28 | `TestAuditLogger_WriteAndRotate` | Integration | Audit: rotation at 50MB | Validates JSONL append + file rotation |
| 29 | `TestAuditLogger_RedactionPipeline` | Integration | Audit + Redaction combined | Validates redaction is applied before writing |
| 30 | `TestPolicyEngine_FullToolInvocation` | Integration | Tool Allow/Deny + Explainable combined | Validates policy evaluation + audit logging end-to-end |
| 31 | `TestSandboxBackend_LinuxFull` | Integration | Landlock + Seccomp combined | Validates both backends active on Linux 5.13+ (requires Linux CI) |
| 32 | `TestSandboxBackend_Fallback` | Integration | Fallback on unsupported kernel | Validates Fallback backend on non-Linux |
| 33 | `TestExecProxy_SSRFBlock` | Integration | Proxy: blocks private IP | Validates HTTP proxy intercepts and blocks private IPs |
| 34 | `TestE2E_AgentToolDenied` | E2E | Full tool invocation denied | Agent invokes denied tool, receives error, audit entry written |
| 35 | `TestE2E_AgentExecApproved` | E2E | Full exec approval flow | Agent requests exec, approval granted, command runs, audit written |
| 36 | `TestE2E_RateLimitTriggered` | E2E | Full rate limit rejection | Agent hits rate limit, receives retry_after, audit written |
| 37 | `TestE2E_SSRFBlocked` | E2E | Full SSRF block | Agent web_fetch to private IP, blocked, audit written |

### Test Datasets

#### Dataset: SSRF IP Validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `10.0.0.1` | Private (RFC 1918 Class A) | Blocked | SSRF: Private IP blocked | 10.0.0.0/8 |
| 2 | `10.255.255.255` | Private boundary | Blocked | SSRF: Private IP blocked | End of 10.0.0.0/8 |
| 3 | `172.16.0.1` | Private (RFC 1918 Class B) | Blocked | SSRF: Private IP blocked | 172.16.0.0/12 |
| 4 | `172.31.255.255` | Private boundary | Blocked | SSRF: Private IP blocked | End of 172.16.0.0/12 |
| 5 | `172.32.0.1` | Just outside private | Allowed | SSRF: not private | First non-private in 172.x |
| 6 | `192.168.0.1` | Private (RFC 1918 Class C) | Blocked | SSRF: Private IP blocked | 192.168.0.0/16 |
| 7 | `192.168.255.255` | Private boundary | Blocked | SSRF: Private IP blocked | End of 192.168.0.0/16 |
| 8 | `169.254.169.254` | Cloud metadata | Blocked | SSRF: Cloud metadata blocked | AWS/GCP/Azure metadata |
| 9 | `169.254.0.1` | Link-local | Blocked | SSRF: link-local | 169.254.0.0/16 |
| 10 | `127.0.0.1` | Loopback | Blocked | SSRF: loopback | 127.0.0.0/8 |
| 11 | `127.0.0.2` | Loopback alternate | Blocked | SSRF: loopback | Non-standard loopback |
| 12 | `0.0.0.0` | Unspecified | Blocked | SSRF: unspecified | Can map to loopback |
| 13 | `8.8.8.8` | Public | Allowed | SSRF: public IP | Google DNS |
| 14 | `::1` | IPv6 loopback | Blocked | SSRF: IPv6 loopback | |
| 15 | `fe80::1` | IPv6 link-local | Blocked | SSRF: IPv6 link-local | fe80::/10 |
| 16 | `fc00::1` | IPv6 unique local | Blocked | SSRF: IPv6 private | fc00::/7 |
| 17 | `::ffff:10.0.0.1` | IPv4-mapped IPv6 | Blocked | SSRF: mapped IPv4 private | Must unwrap and check |
| 18 | `::ffff:169.254.169.254` | IPv4-mapped metadata | Blocked | SSRF: mapped IPv4 metadata | |
| 19 | `2001:4860:4860::8888` | IPv6 public | Allowed | SSRF: public IPv6 | Google DNS |

#### Dataset: Exec Allowlist Glob Matching

| # | Input Command | Pattern List | Expected | Traces to | Notes |
|---|---------------|--------------|----------|-----------|-------|
| 1 | `git status` | `["git *"]` | Allowed | Per-Binary: allowed | Simple glob |
| 2 | `git push origin main` | `["git *"]` | Allowed | Per-Binary: allowed | Multi-word after glob |
| 3 | `curl http://x.com` | `["git *"]` | Denied | Per-Binary: denied | No matching pattern |
| 4 | `npm run build` | `["npm run *"]` | Allowed | Per-Binary: allowed | Two-word prefix |
| 5 | `npm install lodash` | `["npm run *"]` | Denied | Per-Binary: denied | `install` != `run` |
| 6 | `python3 script.py` | `["python3 *.py"]` | Allowed | Per-Binary: allowed | Suffix glob |
| 7 | `python3 script.sh` | `["python3 *.py"]` | Denied | Per-Binary: denied | Wrong suffix |
| 8 | `git status` | `[]` | Denied | Per-Binary: empty list | Empty allowlist |
| 9 | `ls -la` | `["ls *"]` | Allowed | Per-Binary: allowed | Common command |
| 10 | `rm -rf /` | `["rm *"]` | Allowed (glob) | Per-Binary: allowed | Dangerous — glob doesn't assess safety, only pattern |

#### Dataset: Rate Limit Configurations

| # | Scope | Limit | Window | State After N Calls | Expected on N+1 | Traces to |
|---|-------|-------|--------|---------------------|------------------|-----------|
| 1 | per-agent | 10 llm_calls | 1 hour | 10 calls in window | Reject, retry_after > 0 | Rate Limiting: per-agent |
| 2 | per-agent | 5 tool_calls | 1 minute | 5 calls in window | Reject, retry_after > 0 | Rate Limiting: per-agent |
| 3 | per-channel | 30 messages | 1 minute | 30 messages in window | Reject | Rate Limiting: per-channel |
| 4 | global cost | $50/day | 1 day | $49.99 cumulative | Reject | Rate Limiting: global cost |
| 5 | global cost | $0/day | 1 day | $0.00 cumulative | Reject (emergency stop) | Edge: zero cost cap |
| 6 | per-agent | 10 llm_calls | 1 hour | 0 calls (after restart) | Allow | Rate limit reset on restart |

#### Dataset: Redaction Patterns

| # | Input Value | Pattern | Expected Output | Traces to | Notes |
|---|-------------|---------|-----------------|-----------|-------|
| 1 | `sk-ant-abc123def456ghi789jkl012mno345` | `sk-[a-zA-Z0-9-]{20,}` | `[REDACTED]` | Redaction: API key | Anthropic key format |
| 2 | `sk-proj-abc123def456ghi789jkl` | `sk-[a-zA-Z0-9-]{20,}` | `[REDACTED]` | Redaction: API key | OpenAI key format |
| 3 | `ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx` | `ghp_[a-zA-Z0-9]{36}` | `[REDACTED]` | Redaction: API key | GitHub PAT |
| 4 | `user@example.com` | email pattern | `[REDACTED]` | Redaction: PII | Email address |
| 5 | `password123` | password field | Not redacted (no pattern match) | Redaction: value heuristic | Only pattern-based, not semantic |
| 6 | `Bearer eyJhbGciOi...` | `Bearer [a-zA-Z0-9._-]+` | `[REDACTED]` | Redaction: token | JWT bearer token |
| 7 | `INTERNAL-123456` | Custom: `INTERNAL-[0-9]{6}` | `[REDACTED]` | Redaction: custom | User-defined pattern |

#### Dataset: Landlock ABI Versions

| # | Kernel Version | ABI Version | Available Access Rights | Traces to |
|---|---------------|-------------|------------------------|-----------|
| 1 | < 5.13 | N/A (unsupported) | None (fallback) | Landlock: fallback |
| 2 | 5.13-5.18 | 1 | EXECUTE, WRITE_FILE, READ_FILE, READ_DIR, REMOVE_DIR, REMOVE_FILE, MAKE_CHAR, MAKE_DIR, MAKE_REG, MAKE_SOCK, MAKE_FIFO, MAKE_BLOCK, MAKE_SYM, REFER | Landlock: ABI v1 |
| 3 | 5.19-6.1 | 2 | v1 + TRUNCATE | Landlock: ABI v2 |
| 4 | 6.2+ | 3 | v2 + IOCTL_DEV | Landlock: ABI v3 |

#### Dataset: Seccomp Blocked Syscalls

| # | Syscall | Reason Blocked | Expected Action | Traces to |
|---|---------|---------------|-----------------|-----------|
| 1 | `ptrace` | Privilege escalation, debugging other processes | EPERM | Seccomp: blocked |
| 2 | `mount` | Filesystem manipulation | EPERM | Seccomp: blocked |
| 3 | `umount2` | Filesystem manipulation | EPERM | Seccomp: blocked |
| 4 | `init_module` | Kernel module loading | EPERM | Seccomp: blocked |
| 5 | `finit_module` | Kernel module loading | EPERM | Seccomp: blocked |
| 6 | `create_module` | Kernel module loading | EPERM | Seccomp: blocked |
| 7 | `delete_module` | Kernel module loading | EPERM | Seccomp: blocked |
| 8 | `reboot` | System reboot | EPERM | Seccomp: blocked |
| 9 | `swapon` | Swap manipulation | EPERM | Seccomp: blocked |
| 10 | `swapoff` | Swap manipulation | EPERM | Seccomp: blocked |
| 11 | `pivot_root` | Root filesystem change | EPERM | Seccomp: blocked |
| 12 | `kexec_load` | Kernel replacement | EPERM | Seccomp: blocked |
| 13 | `kexec_file_load` | Kernel replacement | EPERM | Seccomp: blocked |
| 14 | `bpf` | eBPF program loading | EPERM | Seccomp: blocked |
| 15 | `perf_event_open` | Performance monitoring (info leak) | EPERM | Seccomp: blocked |
| 16 | `socket(AF_PACKET)` | Raw packet access | EPERM | Seccomp: blocked |
| 17 | `socket(AF_NETLINK)` | Kernel communication | EPERM | Seccomp: blocked |
| 18 | `open` | Not blocked | Allowed (Landlock handles filesystem) | Seccomp: not blocked |
| 19 | `read` | Not blocked | Allowed | Seccomp: not blocked |
| 20 | `write` | Not blocked | Allowed | Seccomp: not blocked |

#### Dataset: Policy File Examples

**Valid minimal policy:**
```json
{
  "security": {
    "default_policy": "deny"
  }
}
```

**Valid full policy:**
```json
{
  "security": {
    "default_policy": "deny",
    "ssrf": {
      "enabled": true,
      "allow_internal": ["10.0.0.5", "10.0.0.6"]
    },
    "rate_limits": {
      "exec": "10/min",
      "web_search": "30/min",
      "web_fetch": "20/min",
      "global_cost_cap_usd": 50.0
    },
    "audit": {
      "output": "file",
      "redaction": true,
      "redaction_patterns": ["INTERNAL-[0-9]{6}"],
      "tamper_evident": false
    },
    "policy": {
      "filesystem": {
        "allowed_paths": ["/tmp", "~/.omnipus/agents/*/"]
      },
      "exec": {
        "allowed_binaries": ["git *", "npm *", "python3 *.py"]
      }
    }
  }
}
```

**Invalid policy (bad default_policy value):**
```json
{
  "security": {
    "default_policy": "maybe"
  }
}
```
Expected: Startup error: `"invalid security.default_policy value 'maybe': must be 'allow' or 'deny'"`

**Invalid policy (bad type for allow_internal):**
```json
{
  "security": {
    "ssrf": {
      "allow_internal": "10.0.0.5"
    }
  }
}
```
Expected: Startup error: `"security.ssrf.allow_internal must be an array of strings, got string"`

---

### Regression Test Requirements

> No regression impact — new capability (pre-implementation). Integration seams protected by: config loader validation tests, sandbox backend interface tests, and audit logger output tests.

---

## Functional Requirements

- **FR-001**: System MUST detect the Linux kernel version and Landlock ABI version at startup and select the appropriate SandboxBackend implementation (Linux, Fallback).
- **FR-002**: System MUST apply Landlock filesystem restrictions to agent processes on Linux 5.13+, limiting access to configured allowed paths and agent workspace directories.
- **FR-003**: System MUST apply seccomp-BPF filters to exec tool child processes on Linux 3.17+, blocking the syscalls listed in the seccomp blocked syscalls dataset.
- **FR-004**: System MUST ensure child processes spawned by the exec tool inherit Landlock restrictions (native) and seccomp filters (via SECCOMP_FILTER_FLAG_TSYNC).
- **FR-005**: System MUST fall back to application-level filesystem enforcement when Landlock is unavailable, logging a warning with the detected kernel version.
- **FR-006**: System MUST skip seccomp silently (info log only) on non-Linux platforms.
- **FR-007**: System MUST evaluate tool allow/deny lists per agent before every tool invocation, with deny taking precedence over allow when both are specified.
- **FR-008**: System MUST enforce per-binary execution control via glob pattern matching against `tools.exec.allowed_binaries` before running any exec command.
- **FR-009**: System MUST implement deny-by-default semantics when `security.default_policy` is `"deny"`, requiring explicit `tools.allow` entries for any tool to be available.
- **FR-010**: System MUST default `security.default_policy` to `"allow"` when the field is absent, maintaining backward compatibility with Omnipus configs.
- **FR-011**: System MUST present an interactive exec approval prompt in CLI mode when `tools.exec.approval` is `"ask"`, supporting Allow, Deny, and Always Allow responses.
- **FR-012**: System MUST persist "Always Allow" patterns from exec approval and auto-approve matching commands in subsequent invocations.
- **FR-013**: System MUST load all security policies from `config.json` at startup (static, load-once) and refuse to start if the security section is malformed.
- **FR-014**: System MUST append a structured JSON audit entry to `~/.omnipus/system/audit.jsonl` for every security-relevant event (tool_call, exec, file_op, auth, policy_change, channel_event, system_tool).
- **FR-015**: System MUST rotate audit log files at 50MB or daily (whichever comes first), naming rotated files `audit-YYYY-MM-DD.jsonl`.
- **FR-016**: System MUST delete rotated audit files older than `storage.retention.audit_days` (default 90 days).
- **FR-017**: System MUST apply configurable regex-based redaction to all audit log entries before writing, replacing matches with `[REDACTED]`.
- **FR-018**: System MUST include a `policy_rule` field in every audit entry explaining the matching rule for the allow/deny decision.
- **FR-019**: System MUST block outbound HTTP requests to private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 127.0.0.0/8), IPv6 private ranges (::1, fe80::/10, fc00::/7), and IPv4-mapped IPv6 equivalents.
- **FR-020**: System MUST block requests to cloud metadata endpoint 169.254.169.254 regardless of other SSRF configuration.
- **FR-021**: System MUST check resolved IPs (not just hostnames) against SSRF rules to prevent DNS rebinding attacks.
- **FR-022**: System MUST support a configurable allowlist (`security.ssrf.allow_internal`) that overrides SSRF blocking for specific IPs.
- **FR-023**: System MUST implement sliding-window rate limiting at per-agent, per-channel, and global cost scopes.
- **FR-024**: System MUST reject rate-limited operations with an error including `retry_after_seconds` (no silent queueing).
- **FR-025**: System MUST exempt the system agent (`omnipus-system`) from rate limits while still audit-logging its operations.
- **FR-026**: System MUST persist global cost cap state via session stats to survive restarts; per-agent and per-channel counters MAY reset on restart.
- **FR-027**: System MUST set `HTTP_PROXY` and `HTTPS_PROXY` environment variables on exec tool child processes pointing to a local SSRF-enforcing proxy.
- **FR-028**: System MUST only run the HTTP proxy while exec tool processes are active.
- **FR-029**: System MUST detect risky DM channel configurations (empty `allow_from`) and surface warnings in `omnipus doctor` output.
- **FR-030**: System MUST warn via `omnipus doctor` when the exec tool is enabled without full network egress control (SEC-29).
- **FR-031**: System SHOULD check HTTP redirect targets against SSRF rules before following redirects.
- **FR-032**: System MUST validate the last line of `audit.jsonl` on startup and truncate if malformed (crash recovery).
- **FR-033**: System MUST implement the `SandboxBackend` interface with three implementations: `LinuxSandboxBackend` (Landlock+seccomp), `FallbackSandboxBackend` (app-level), and a constructor that selects the appropriate backend at startup.

---

## Success Criteria

- **SC-001**: All 20 blocked seccomp syscalls return EPERM (not SIGKILL) within 1ms of invocation, verified on Linux CI with kernel 5.13+.
- **SC-002**: Landlock ABI detection correctly identifies v1, v2, and v3 on the corresponding kernel versions, verified by startup log output.
- **SC-003**: Policy evaluation (tool allow/deny check) completes in under 100 microseconds per invocation, measured with Go benchmarks.
- **SC-004**: Audit log write latency is under 1ms per entry (p99), measured with Go benchmarks, to avoid impacting tool execution performance.
- **SC-005**: SSRF checker correctly classifies all 19 IP test dataset entries with zero false positives and zero false negatives.
- **SC-006**: Rate limiter handles 10,000 concurrent increment operations without data races, verified by `go test -race`.
- **SC-007**: On a kernel without Landlock support, Omnipus starts successfully within the same time budget as with Landlock (< 1 second), with no error-level log messages (only warnings/info).
- **SC-008**: All audit log entries are valid JSON parseable by `encoding/json.Unmarshal`, verified by reading back every entry written during E2E tests.
- **SC-009**: Redaction engine correctly redacts all 7 test dataset patterns with no false negatives on known patterns.
- **SC-010**: Total memory overhead for the security subsystem (policy engine, rate limiter, audit logger, SSRF checker, sandbox state) is under 5MB, measured by Go runtime memory stats.
- **SC-011**: `omnipus doctor` produces correct warnings for all DM safety check scenarios in under 2 seconds.
- **SC-012**: The exec HTTP proxy starts in under 500ms and adds less than 50ms latency to proxied requests.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-001 | US-1 | Landlock: ABI version detection | `TestLandlockDetector_ABIVersion` |
| FR-002 | US-1 | Landlock: restricts agent to workspace | `TestSandboxBackend_LinuxFull` |
| FR-003 | US-2 | Seccomp: Dangerous syscall blocked | `TestSeccompBPF_ProgramAssembly`, `TestSandboxBackend_LinuxFull` |
| FR-004 | US-3 | Child: Grandchild inherits Landlock, Seccomp TSYNC | `TestSandboxBackend_LinuxFull` |
| FR-005 | US-1 | Landlock: Graceful fallback | `TestSandboxBackend_Fallback` |
| FR-006 | US-2 | Seccomp: no-op on non-Linux | `TestSandboxBackend_Fallback` |
| FR-007 | US-4 | Tool Allow/Deny: all scenarios | `TestPolicyEvaluator_ToolAllowList`, `TestPolicyEvaluator_ToolDenyList`, `TestPolicyEvaluator_DenyPrecedence` |
| FR-008 | US-5 | Per-Binary: all scenarios | `TestGlobMatcher_ExecAllowlist`, `TestGlobMatcher_NoMatch`, `TestGlobMatcher_EmptyList` |
| FR-009 | US-6 | Deny-by-Default: No tools without allow | `TestPolicyEvaluator_DenyByDefault` |
| FR-010 | US-6 | Deny-by-Default: Backward-compatible | `TestPolicyEvaluator_AllowByDefault` |
| FR-011 | US-7 | Exec Approval: Interactive approval | `TestE2E_AgentExecApproved` |
| FR-012 | US-7 | Exec Approval: Always Allow persists | `TestE2E_AgentExecApproved` |
| FR-013 | US-8 | Policy Files: valid config, malformed config | `TestConfigLoader_SecuritySection`, `TestConfigLoader_MalformedSecurity` |
| FR-014 | US-9 | Audit: Tool call produces entry | `TestAuditLogger_WriteAndRotate`, `TestE2E_AgentToolDenied` |
| FR-015 | US-9 | Audit: rotation at 50MB | `TestAuditLogger_WriteAndRotate` |
| FR-016 | US-9 | Audit: retention | `TestAuditLogger_WriteAndRotate` |
| FR-017 | US-10 | Redaction: API key, custom pattern | `TestRedactionEngine_DefaultPatterns`, `TestRedactionEngine_CustomPatterns` |
| FR-018 | US-11 | Explainable: Denial includes rule, Allow includes rule | `TestPolicyEvaluator_ExplainableDecision` |
| FR-019 | US-12 | SSRF: Private IP blocked | `TestSSRFChecker_PrivateIPv4Ranges`, `TestSSRFChecker_PrivateIPv6Ranges` |
| FR-020 | US-12 | SSRF: Cloud metadata blocked | `TestSSRFChecker_PrivateIPv4Ranges` |
| FR-021 | US-12 | SSRF: DNS rebinding | `TestSSRFChecker_DNSRebinding` |
| FR-022 | US-12 | SSRF: Allowlisted IP | `TestSSRFChecker_Allowlist` |
| FR-023 | US-13 | Rate Limiting: all scenarios | `TestRateLimiter_SlidingWindow`, `TestRateLimiter_GlobalCostCap` |
| FR-024 | US-13 | Rate Limiting: Per-agent rejection | `TestRateLimiter_RetryAfterSeconds` |
| FR-025 | US-13 | Rate Limiting: System agent exempt | `TestRateLimiter_SystemAgentExempt` |
| FR-026 | US-13 | Rate Limiting: restart behavior | `TestRateLimiter_GlobalCostCap` |
| FR-027 | US-14 | Proxy: env vars set, blocks private IP | `TestExecProxy_SSRFBlock` |
| FR-028 | US-14 | Proxy: only active during exec | `TestExecProxy_SSRFBlock` |
| FR-029 | US-15 | DM Safety: Open Telegram flagged | `TestDMSafetyChecker_OpenChannel` |
| FR-030 | US-15 | DM Safety: network egress warning | `TestDMSafetyChecker_OpenChannel` |
| FR-031 | US-12 | SSRF: redirect check | `TestSSRFChecker_DNSRebinding` |
| FR-032 | US-9 | Audit: crash recovery | `TestAuditLogger_WriteAndRotate` |
| FR-033 | US-1, US-2 | Landlock + Seccomp + Fallback backends | `TestSandboxBackend_LinuxFull`, `TestSandboxBackend_Fallback` |

**Completeness check**: All 33 FR-xxx rows have at least one BDD scenario and one test. All BDD scenarios appear in at least one row.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
All ambiguities resolved on 2026-03-28:

| # | What Was Ambiguous | Resolution |
|---|---|---|
| 1 | Filesystem policy paths scope | **Both** — global paths + per-agent workspace (always implicitly allowed). Merged at runtime. |
| 2 | Seccomp blocked syscall action | **SECCOMP_RET_ERRNO(EPERM).** Graceful error handling. Process can log and continue. More debuggable than SIGKILL. |
| 3 | Rate limit config format | **Structured format** — `{"count": 10, "window": "1m"}`. No string shorthand. |
| 4 | "Always Allow" exec patterns storage | **Separate file** — `~/.omnipus/system/exec-allowlist.json`. Don't mutate config.json with runtime-generated entries. |
| 5 | `tools.allow` vs `tools.overrides` | **`allow` is the permission gate, `overrides` is configuration of allowed tools.** If a tool isn't in `allow`, its overrides are irrelevant. |
| 6 | SSRF protection scope | **All outbound HTTP** — web_fetch, web_search backends, browser automation, and any other tool making HTTP requests. |
| 7 | Rate limit audit event type | **Reuse `tool_call`** with `decision: "deny"` and `policy_rule: "rate_limit:exec:10/1m"`. No new event type. |
| 8 | Per-channel rate limit defaults | **Define ourselves.** Hardcode sensible defaults per known channel, configurable via config. |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Full sandbox enforcement on fresh Linux 6.2 server
- **Setup**: Clean Linux 6.2 host, fresh Omnipus install, `security.default_policy: "deny"`, agent with `tools.allow: ["web_search", "file.read"]` and `tools.exec.allowed_binaries: ["git *"]`
- **Action**: Agent attempts to (1) web_search, (2) file.read inside workspace, (3) exec `git status`, (4) exec `curl http://169.254.169.254`, (5) file.write, (6) read /etc/passwd
- **Expected outcome**: (1) allowed, (2) allowed, (3) allowed, (4) denied by exec allowlist, (5) denied by tool allow list, (6) denied by Landlock. All 6 events in audit log with correct policy_rule.
- **Category**: Happy Path

### Scenario: Multi-agent rate limiting under load
- **Setup**: Two agents configured, per-agent limit 10 tool_calls/minute, global cost cap $5/day. Agent A has made 9 calls, Agent B has made 8 calls. Global cost at $4.95.
- **Action**: Agent A makes 2 rapid calls. Agent B makes 1 call that costs $0.10.
- **Expected outcome**: Agent A's first call succeeds (10th), second is rate-limited. Agent B's call is blocked by global cost cap ($4.95 + $0.10 > $5.00). Audit log shows correct rejection reasons for both.
- **Category**: Happy Path

### Scenario: Graceful startup on macOS with full config
- **Setup**: macOS host, config.json with all security features configured (Landlock paths, seccomp, SSRF, rate limits, audit logging)
- **Action**: Start Omnipus
- **Expected outcome**: Starts successfully in under 1 second. Logs info messages about Landlock/seccomp being unavailable. SSRF, rate limiting, audit logging, and policy engine all function correctly. Application-level filesystem enforcement active.
- **Category**: Happy Path

### Scenario: Corrupt audit log recovery
- **Setup**: `audit.jsonl` exists with 100 valid entries and one partial/corrupt last line (simulating crash during write)
- **Action**: Start Omnipus
- **Expected outcome**: Startup detects malformed last line, truncates it, logs a warning about audit log recovery, and continues appending new entries. The 100 valid entries are preserved.
- **Category**: Error

### Scenario: Invalid security config prevents startup
- **Setup**: `config.json` with `"security": {"default_policy": "yolo", "ssrf": {"allow_internal": 42}}`
- **Action**: Start Omnipus
- **Expected outcome**: Omnipus refuses to start. Error message lists both validation failures: invalid default_policy value and wrong type for allow_internal. Exit code non-zero.
- **Category**: Error

### Scenario: SSRF bypass attempt via DNS CNAME chain
- **Setup**: SSRF protection enabled. External DNS configured so `public.example.com` CNAME -> `internal.example.com` CNAME -> A record `169.254.169.254`
- **Action**: Agent calls web_fetch with `http://public.example.com/latest/meta-data/`
- **Expected outcome**: DNS resolution follows CNAME chain, resolves to 169.254.169.254, SSRF checker blocks the request. Audit entry includes the original hostname and the resolved IP.
- **Category**: Edge Case

### Scenario: Concurrent rate limit boundary
- **Setup**: Per-agent rate limit exactly 1 call remaining in current window. Two goroutines simultaneously attempt a tool call for the same agent.
- **Action**: Both goroutines call the rate limiter's `Allow()` method at the same time
- **Expected outcome**: Exactly one call succeeds and one is rejected. No data race (passes `-race` detector). The rejected call includes a valid `retry_after_seconds` value.
- **Category**: Edge Case

---

## Assumptions

- Go 1.21+ is the minimum Go version, providing `slog` in the standard library.
- `golang.org/x/sys/unix` provides all necessary syscall wrappers for Landlock (SYS_LANDLOCK_CREATE_RULESET, SYS_LANDLOCK_ADD_RULE, SYS_LANDLOCK_RESTRICT_SELF) and seccomp (PR_SET_NO_NEW_PRIVS, PR_SET_SECCOMP, SockFilter).
- Linux CI runners for integration tests have kernel 5.13+ with Landlock enabled in the kernel config.
- The config system (from Wave 1) provides a parsed `config.json` as a Go struct at startup. The security layer reads from this struct; it does not parse JSON directly.
- The agent loop (from Wave 1 or Omnipus) calls into the policy engine before executing each tool. The security layer provides a `CheckPolicy(agentID, toolName, params) -> (Decision, error)` function that the agent loop calls.
- The exec tool (from Omnipus) accepts a hook for environment variable injection and pre-execution checks. The security layer uses this hook to apply sandbox, proxy, and approval logic.
- `omnipus doctor` is a CLI command framework that accepts check functions. The security layer registers its checks (DM safety, network egress warning, sandbox status) with this framework.
- Windows Job Object sandboxing is explicitly out of scope for Wave 2. Windows uses the Fallback backend.
- RBAC (SEC-19), credential management (SEC-22/23), and tamper-evident logging (SEC-18) are out of scope for Wave 2.
- The HTTP proxy for exec (SEC-28) only needs to handle HTTP CONNECT (for HTTPS) and plain HTTP forwarding. It does not need to handle WebSocket, gRPC, or other protocols.
- Rate limit `retry_after_seconds` is calculated from the sliding window state: the time until the oldest event in the window expires, freeing one slot.
- The system agent's agent_id is `"omnipus-system"` as referenced in Appendix D.
