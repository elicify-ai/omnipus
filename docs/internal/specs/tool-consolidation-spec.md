# Tool Consolidation Spec — `bash` and Unified Subagent Delegation

**Status:** Draft — ready for implementation
**Date:** 2026-07-04
**Author:** Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>`
**Branch target:** `hotfix/v0.1.1`
**Governing ADR:** [ADR-036 — Consolidate Shell and Subagent Tools; Generalize the Async Wake Mechanism](../architecture/ADR-036-consolidate-shell-and-subagent-tools.md)
**Companion spec:** [`async-notifier-spec.md`](./async-notifier-spec.md) — the wake-mechanism infrastructure `bash`'s background mode and `delegate` both depend on
**Name correction (2026-07-04):** the unified subagent tool's name was resolved to `delegate` in [`agent-delegation-spec.md`](./agent-delegation-spec.md) the same day this document was drafted — this document originally said "name TBD... refers to it as `subagent`"; every such reference below is now `delegate`. If you're reading only this overview document, `delegate` is final — see `agent-delegation-spec.md`'s Clarifications for the resolution record.
**Reviewed design:** `tool-consolidation-design.html` (session design review, approved 2026-07-04)

---

## 1. Goals

- Replace `exec`, `workspace_shell`, `workspace_shell_bg` with one tool, `bash`.
- Replace `spawn`, `run_subagent`, `check_spawn_status` with one tool, named `delegate` (resolved 2026-07-04, see note above) — applied uniformly across schema, registry, docs, and tests.
- Retire the dedicated exec-only approval UI (`ExecApprovalBlock`/`ExecApprovalTool`); every tool's "ask" verdict goes through the generic `ToolApprovalModal`.
- Ship a migration so no operator's existing tool-policy intent (`deny`/`ask`/`allow` on any of the five retired tool names) is silently lost.
- Zero net reduction in the security guarantees any of the five tools currently provides, except the two explicitly-approved capability drops (§2).

## 2. Non-goals (explicitly out of scope)

- True real-time push notification (Claude Code's `Monitor`-tool equivalent) — see `async-notifier-spec.md` §6.
- The future "Goals" feature itself — this spec ships the primitive it will depend on, not the feature.
- PTY/interactive terminal sessions — dropped per ADR-036 §3.1, not replaced.
- `workspace_shell_bg`'s free-form background-command + arbitrary-port-expose capability — dropped per ADR-036 §3.1; the legitimate use case (serving a dev server) is `web_serve`'s job, unchanged by this spec.

## 3. `bash` — functional requirements

### FR-B1 — Schema

```json
{
  "command": "string, required",
  "description": "string, optional",
  "cwd": "string, optional — relative to the agent's resolved workspace root only",
  "timeout_seconds": "int, optional, default 300, max 3600",
  "run_in_background": "bool, optional, default false",
  "persistent": "bool, optional, default false — only meaningful with run_in_background=true",
  "action": "enum [run, poll, read, kill], optional, default 'run'",
  "session_id": "string, required when action is poll/read/kill"
}
```

**Acceptance criteria:**
- A call with only `command` set runs synchronously in the foreground, exactly matching `exec`'s current default behavior (minus the dropped capabilities in §2).
- `cwd` containing `..` or an absolute path is rejected with a clear error (`"path escapes workspace"` or equivalent) — **before** any subprocess is spawned. No `restrict_to_workspace=false`-style escape hatch survives.
- `timeout_seconds` is enforced identically for foreground and background modes — a command exceeding it is killed and the result reports a timeout, not silently truncated.
- `run_in_background=true` returns immediately with a `session_id` and any output produced before the call returns; the command continues running detached.
- `persistent=true` without `run_in_background=true` is a validation error (400-equivalent tool-result error), not silently ignored.
- `action=poll` with a `session_id` returns the session's current status (`running`/`completed`/`failed`/`killed`) without blocking.
- `action=read` with a `session_id` returns output accumulated since the last `read` call for that session (or since spawn, if never read) — not the full history each time.
- `action=kill` with a `session_id` terminates the process group and marks the session `killed`.
- Referencing a `session_id` that doesn't exist, or belongs to a different conversation/agent, returns a clear "not found" error — never another conversation's data (mirrors `check_spawn_status`'s existing channel/chatID scoping).

### FR-B2 — Deny-patterns

**Acceptance criteria:**
- The hardcoded baseline deny-pattern list (currently `exec`'s `defaultDenyPatterns`, including the literal `master.key`/`credentials.json` guards, `rm -rf`, fork-bomb patterns, `sudo`, curl-pipe-to-shell) applies to **every** `bash` call, foreground and background, by default — with no per-agent or global toggle to disable the baseline entirely (only the operator-extensible `CustomDenyPatterns`/global `ShellDenyPatterns` layer on top, matching `workspace_shell`'s existing extension mechanism).
- A command matching a deny pattern is rejected with the same class of error message `exec` produces today, before any subprocess is spawned.

### FR-B3 — Binary allowlist

**Acceptance criteria:**
- `EvaluateExec`'s glob-based `allowed_binaries` mechanism (`security.policy.exec.allowed_binaries`) is consulted for every `bash` call, foreground and background — today it only gates `exec`'s foreground path.
- When `allowed_binaries` is empty (the default), this check is a no-op (matches today's default-permissive behavior for operators who never configured it).

### FR-B4 — Hardening and god-mode

**Acceptance criteria:**
- Every non-god-mode `bash` invocation runs through `sandbox.ResolveLimits(godMode, workspaceDir, proxy, timeoutSeconds)` (the ADR-035 §7 primitive) — one hardening path, not `exec`'s current two parallel ones.
- `godMode := agent.GodModeActive(cfg)`, resolved once per turn exactly as `workspace_shell`/`workspace_shell_bg` already do — no new latch, no per-tool god-mode variant.
- Audit-write failure with `auditFailClosed=true` (the default) refuses execution — `workspace_shell`'s stricter behavior, not `exec`'s current unguarded one.

### FR-B5 — Registration and access

**Acceptance criteria:**
- `bash` is registered for every agent unconditionally, exactly as `exec` is today — no config flag gates whether the tool merely *exists* on an agent's registry.
- Whether a given agent can actually *call* `bash` is governed exclusively by that agent's `ToolPolicyCfg` entry for `"bash"` (or whatever final tool name is chosen) — deny-by-default for new custom agents, matching every other builtin tool's convention.

## 4. Unified `delegate` tool — functional requirements

### FR-S1 — Schema

```json
{
  "task": "string, required",
  "label": "string, optional",
  "agent_id": "string, optional — target agent from the delegation allowlist",
  "async": "bool, optional, default true",
  "action": "enum [run, status], optional, default 'run'",
  "task_id": "string, required when action is status"
}
```

**Acceptance criteria:**
- A call with only `task` set defaults to `async=true` — fire-and-forget, returns an immediate acknowledgment and a `task_id`, matching `spawn`'s current default shape but now the tool's only mode's default, not a separate tool.
- `async=false` blocks until the delegated turn completes and returns its result inline, matching `run_subagent`'s current behavior exactly.
- `action=status` with a `task_id` returns the task's current state (`running`/`completed`/`failed`/`canceled`) and, if completed, its result — scoped to the calling conversation's channel/chatID, matching `check_spawn_status`'s existing scoping.
- **The bug this merge fixes**: `action=status` reads from the exact same underlying state the `async=true` path writes to. There is no longer a data structure written by one code path and read by a different, disconnected one.
- The delegation-policy gate (trust set, modes, depth — FR-6.2) is consulted identically regardless of `async` value, exactly as `spawn`/`run_subagent` both already do — a rejected delegation surfaces the same structured denial reason either way.
- Any agent — including the main/orchestrating agent, not a role-restricted subset — can call this tool; access is governed entirely by the agent's own delegation policy, never by the tool's registration.

### FR-S2 — Async completion delivery

**Acceptance criteria:**
- When `async=true` and the delegated turn completes, the result is delivered via `AsyncNotifier.Notify` (see `async-notifier-spec.md`) — the exact mechanism `spawn`'s current callback already uses, unchanged in shape, just invoked from the unified tool.

## 5. Approval-flow consolidation

### FR-A1 — One approval UI

**Acceptance criteria:**
- `ExecApprovalBlock.tsx`, `ExecApprovalTool.tsx`, and the `exec_approval_request`/`exec_approval_response`/`exec_approval_expired` wire frames are deleted.
- A `bash` (or `delegate`) call resolved to `"ask"` by policy renders via the generic `ToolApprovalModal` — verify it and its wire frames (`tool_approval_required`, etc.) already carry enough information (the exact command, the tool name, the target agent) to make an informed approve/deny decision without the dedicated flow's bespoke copy. If the generic modal is missing something the dedicated flow showed (e.g., a live command preview specific to shell execution), that capability must be ported into the generic modal **before** the dedicated flow is deleted — do not delete first and discover the gap after.

## 6. Migration — functional requirements

### FR-M1 — Tool-policy key rename

**Acceptance criteria:**
- A new boot-time migration function (added to the existing `pkg/config` `migrate*` pipeline, following the same additive/idempotent/logged pattern as its siblings) scans every persisted `AgentBuiltinToolsCfg.Policies` map (per-agent) and the global `OmnipusSandboxConfig.ToolPolicies` map for keys `exec`, `workspace_shell`, or `workspace_shell_bg`.
- If exactly one of the three keys is present, its value is copied to a new `bash` key (or `delegate`, as applicable) and the old key is removed.
- If more than one of the three keys is present with different values, the **strictest** wins (`deny` > `ask` > `allow`) — never silently loosen an existing restriction.
- The migration is idempotent: running it twice against an already-migrated config produces no further change and no error.
- The migration is logged (INFO, one line per rewritten key) so an operator can see what happened on the first post-upgrade boot.
- The legacy `toolEnableToPolicy` table's `{"exec","exec"}` row is updated to `{"exec","bash"}` so a pre-existing `tools.exec.enabled=false` config still produces a `bash: deny` policy after migration, not a dropped/no-op translation.

### FR-M2 — Regression coverage for the fail-open case

**Acceptance criteria:**
- A test proves that a config with `tools_cfg: {"exec": "deny"}` on some agent, loaded through the full migration pipeline, results in that agent's *effective* policy for `bash` being `deny` — not the compositor's default-`allow` fallback. This is the single test that would have caught the regression this spec exists to prevent; it must fail if the migration is ever accidentally removed or short-circuited.

## 7. Traceability

| Requirement | ADR-036 §  | Primary files |
|---|---|---|
| FR-B1–B5 | §3.1 | `pkg/tools/shell.go` (rewritten in place, or a new `bash.go`), `pkg/tools/workspace_shell*.go` (deleted), `pkg/agent/loop.go` (wiring) |
| FR-S1–S2 | §3.2, §3.3 | `pkg/tools/spawn.go`, `pkg/tools/subagent.go`, `pkg/tools/spawn_status.go` (deleted), `pkg/agent/loop.go` |
| FR-A1 | §3.4 | `src/components/agents/ExecApprovalBlock.tsx` (deleted), `src/components/agents/ToolApprovalModal.tsx` |
| FR-M1–M2 | §3.6 | `pkg/config/migration.go`, a new migration function alongside `migrateDeprecatedToolEnableFlags` |

## 8. Test plan (high level — detailed cases follow the existing `tool-test-plan-2026-06.md` §7 structure)

- Unit: every FR above gets at least one positive and one negative test, following this repo's existing `pkg/tools/shell_test.go`/`workspace_shell_test.go` patterns (constructors, deny-pattern matrix, timeout enforcement, cwd-escape rejection).
- Migration: FR-M1/FR-M2's tests live in `pkg/config`, modeled on the existing `migrateDeprecatedToolEnableFlags` test suite — fixture configs with each of the three old keys, alone and in combination, asserting the rewritten result and idempotency on a second load.
- Integration: a `pkg/agent` test proving a `bash run_in_background=true` call's completion is delivered via the same code path `spawn`'s existing async completion uses (shared assertion helper, if one doesn't already exist, is worth adding).
- Frontend: `ToolCallBadge.tsx`, `humanizeToolName.ts`, `toolPolicyPresets.ts`, `ToolsAndPermissions.tsx` each get updated fixtures/tests for the new tool name(s); `ExecApprovalBlock.test.tsx` deleted; `ToolApprovalModal.test.tsx` gains a case proving it renders correctly for a `bash`-sourced "ask" verdict.
- Manual/E2E: an agent policy-denied on `bash` cannot reach shell execution via any other tool name (closes the exact gap FR-B2/B3 exist to prevent) — this is the one regression test worth a real end-to-end pass, not just unit coverage, given it's the security property motivating this whole spec.
