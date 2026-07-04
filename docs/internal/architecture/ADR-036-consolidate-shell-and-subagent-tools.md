# ADR-036 — Consolidate Shell and Subagent Tools; Generalize the Async Wake Mechanism

**Status:** Accepted
**Date:** 2026-07-04
**Deciders:** Daniel Piatkowski (product owner)
**Supersedes:** [ADR-011 — `experimental.workspace_shell_enabled` Defaults to `false`](./ADR-011-experimental-workspace-shell-default-false.md)
**Extends:** [ADR-035 — Remove the Per-Agent Sandbox Profile](./ADR-035-remove-per-agent-sandbox-profile.md), [ADR-009](./ADR-009-per-agent-sandbox-as-security-boundary.md) (already superseded), [ADR-010 — Removal of GHSA-pv8c-p6jf-3fpp Channel Block on `exec`](./ADR-010-remove-ghsa-channel-block-on-exec.md) (still valid, tool renamed)

---

## 1. Context

Following ADR-035's removal of the fictitious per-agent `SandboxProfile`, two more instances of the same underlying problem — capability fragmented across multiple overlapping tool identities, with inconsistent defaults between them — were identified in the same review:

1. **Shell execution, three tools**: `exec` (`pkg/tools/shell.go`), `workspace_shell`, and `workspace_shell_bg` (both `pkg/tools/workspace_shell*.go`) all give an agent the same core capability — run an arbitrary shell command. They are registered simultaneously (nothing coordinates between them), with genuinely *different* defaults for the one thing that matters most: deny-pattern enforcement is **on** by default for `exec` (including literal `master.key`/`credentials.json` blocks) and **off** by default for `workspace_shell`/`workspace_shell_bg`. An agent blocked by one can simply call the other.
2. **Subagent delegation, three tools**: `spawn` (async), `run_subagent` (sync), and `check_spawn_status` (poll) all wrap the same underlying primitive, `SubTurnSpawner.SpawnSubTurn(ctx, cfg)`, differing only in `cfg.Async`. Investigating this surfaced a live bug: `check_spawn_status` reads from `SubagentManager.tasks`, but the current `spawn` tool never writes to that map — it calls `SpawnSubTurn` directly in a goroutine. **Checking on a subagent spawned via `spawn` today always reports "no subagents have been spawned yet."**

Separately, investigating `spawn`'s async completion path surfaced something worth generalizing rather than just fixing: `spawn`'s `AsyncCallback` closure (`pkg/agent/loop.go`) already republishes a completed background subagent's result as a **synthetic inbound bus message** (`bus.PublishInbound`, sender `"async:spawn"`, same channel/chat), which triggers a brand-new turn exactly as if a real message had arrived. This is a small, already-working prototype of "wake the agent up when background work finishes" — currently wired ad-hoc inside one tool's callback, invisible to anything else. `workspace_shell`/`workspace_shell_bg`'s background mode has no equivalent — an agent must proactively poll to learn a backgrounded command finished.

## 2. Market context

Anthropic's own Claude Code uses one execution surface per capability, with synchronous-vs-background as a parameter, not a different tool:

- **Bash**: one tool, `run_in_background: bool` folds sync/async into a single identity. A separate **Monitor** tool exists only for continuous, real-time line-by-line streaming (`tail -f`-style) — a genuinely different delivery model (push vs. poll), not a different execution surface.
- **Task** (subagent spawning): one tool. Historically blocking by default, with a human able to background a running one (`Ctrl+B`). As of a December 2025 update, Claude Code added fully async subagents that **"move to the background and continue working independently... and wake up your main agent when they're done"** — the same behavior `spawn`'s existing callback already approximates for Omnipus, just not generalized.

## 3. Decision

### 3.1 One shell tool: `bash`

Replaces `exec`, `workspace_shell`, `workspace_shell_bg`. Schema: `command` (required), `description` (optional), `cwd` (optional, relative-to-workspace only — no absolute-path escape hatch), `timeout_seconds` (optional, actually enforced), `run_in_background` (bool, default `false`), `persistent` (bool, default `false`, meaningful only with `run_in_background` — skips the idle/hard timeout for `tail -f`-style watching), `action` (enum `run|poll|read|kill`, default `run` — reuses `exec`'s existing session manager for checking on or terminating a backgrounded call).

Resolved per-axis, taking the stronger/more-defensible option wherever the three disagreed:

| Axis | Decision |
|---|---|
| Registration | Universal, every agent, matching `exec`'s current default — the `experimental.workspace_shell_enabled` gate is retired entirely (§3.5) |
| `cwd` escapes | Relative-only, hard-rejected — drops `exec`'s absolute-path allowance |
| Timeout | The real, enforced mechanism (`workspace_shell`'s), not `exec`'s dead `timeout` parameter |
| Deny-patterns | **On** by default (the hardcoded baseline, including `master.key`/`credentials.json`), layered with the operator-extensible custom-pattern mechanism `workspace_shell` already has |
| Binary allowlist (`EvaluateExec`) | Kept, applied uniformly (foreground and background) |
| Audit | Fail-closed on audit-write failure (`workspace_shell`'s stricter behavior) |
| God-mode bypass | The single `godMode bool` + `sandbox.ResolveLimits` pattern from ADR-035 §7 — replaces `exec`'s older, separate hardening path entirely |
| PTY / interactive sessions | **Dropped.** Claude Code's Bash tool has no PTY either; this is real additional attack surface for a capability nothing currently depends on |
| Background + port-expose (dev servers) | **Dropped**, not ported. Genuinely redundant with the existing `web_serve` tool's dev-server mode, which does the same job more safely (a command allow-list, not a free-form command) |
| Plain background execution | Kept, reshaped as `run_in_background`/`action` on the one tool, reusing `exec`'s existing session manager rather than `workspace_shell_bg`'s narrower, Linux-only, non-shell-wrapped mechanism |
| Platform | Cross-platform (the Linux-only tool, `workspace_shell_bg`, is the one being dropped) |

### 3.2 One delegation tool: `delegate`

Replaces `spawn`, `run_subagent`, `check_spawn_status`. Name resolved 2026-07-04 (see `agent-delegation-spec.md`'s Clarifications) — this section originally left the name open ("one subagent tool"); `delegate` is final. Lower-risk than `bash`'s merge: the two execution modes already call the identical underlying function today. Schema: `task` (required), `label` (optional), `agent_id` (optional), `async` (bool, **default `true`** — the opposite default from `bash`, since delegation is typically the heavier, longer operation), `action` (enum `run|status`, default `run`). The main/orchestrating agent uses the exact same tool as any other agent — delegation capability is governed entirely by each agent's own delegation policy (trust set, modes, depth — FR-6.2, unchanged), not by a separate tool identity restricted to certain roles.

This merge is also the fix for the `check_spawn_status`/`spawn` disconnect (§1): one tool, one code path, one place status genuinely lives.

### 3.3 Generalize the async wake mechanism

Extract `spawn`'s existing `AsyncCallback` → `bus.PublishInbound` pattern into a named, reusable primitive:

```go
// AsyncNotifier is the one shared "wake the conversation" primitive. Any
// long-running background mechanism — a spawned subagent, a backgrounded
// bash command, a future goal's condition check — calls this exactly the
// way spawn's callback already does today, instead of inventing its own
// bus-publish plumbing per mechanism.
//
// Signature amended 2026-07-04 (7-reviewer gate, async-notifier-spec.md
// MAJ-004): the original sketch below used four positional strings; that
// contradicts async-notifier-spec.md's FR-N4, which requires AgentID and an
// open Metadata bag so a future observer (Goals) doesn't force a second
// signature change later. This structured-event shape is the actual Decision.
type AsyncNotifyEvent struct {
    Channel    string
    ChatID     string
    AgentID    string
    SourceKind string         // e.g. "bash", "delegate"
    Content    string
    Metadata   map[string]any // open bag, no fields populated yet (async-notifier-spec.md FR-N4)
}

type AsyncNotifier interface {
    Notify(ctx context.Context, event AsyncNotifyEvent) error
}
```

Backed by the same `bus.PublishInbound(...)` call `spawn`'s callback already makes today — a refactor-and-generalize, not new wire plumbing, no new bus frame type, no new agent-loop trigger path. `bash`'s `run_in_background` mode calls it on completion or kill, giving it the same "wakes the agent up" behavior `spawn` already has, via the identical mechanism. `delegate` (§3.2) requires zero new work here — it already goes through this path.

**Explicitly not delivered by this decision**: true real-time, line-by-line push (Claude Code's `Monitor` tool) — that requires injecting content *mid-turn*, a materially different delivery model. The pragmatic version above (notify on completion/kill; poll accumulated output via `action: read` in between) covers the same practical use cases (watch a log, wait for a build) without that added complexity. A dedicated design pass if genuine real-time streaming is wanted later.

**Forward-looking motivation**: a future "Goals" feature (a persistent condition the agent works toward across turns until satisfied) needs exactly this primitive as its event-driven trigger, alongside the schedule-based trigger `TaskExecutor`/`TaskTriggerScheduler` already provides. Not scoped into this change, but `AsyncNotifier` is designed with this consumer in mind so it isn't reworked later.

### 3.4 One approval flow, not two

The dedicated `ExecApprovalBlock`/`ExecApprovalTool` frontend flow (exec-only, hardcoded copy, no `toolName` prop, wire frames `exec_approval_request/response/expired`) is **retired entirely**. Every tool's "ask" verdict — `bash` included — goes through the existing generic `ToolApprovalModal`, which already handles any tool name. No separate approval tool or UI path survives this change.

### 3.5 Retire the `experimental.workspace_shell_enabled` gate

ADR-011's entire premise — a global, config-level opt-in flag defaulting off except for Jim's seed — is retired. Once `bash` is universally registered like `exec` already is, there is no second "experimental" tool needing separate rollout control; access is governed exclusively by each agent's own `ToolPolicyCfg`, exactly as `exec` access already is today. CLAUDE.md's Hard Constraint #6 (which described this gate) is rewritten accordingly, not just renamed.

### 3.6 Migrate persisted tool-policy keys (security-relevant, not cosmetic)

The compositor resolves tool policy by **exact string match** on the registered tool name, with unmatched keys falling back to `DefaultPolicy` (typically `allow`). Renaming `exec`/`workspace_shell`/`workspace_shell_bg` to `bash` with no migration means an operator's existing, persisted `tools_cfg: {exec: "deny"}` (or the global `sandbox.tool_policies` equivalent) would silently stop matching — and because the failure mode is fail-open, an explicit `deny` would silently become `allow` after upgrade. **This is a real security regression, unlike the `SandboxProfile` removal**, which discarded a distinction that carried no actual security value. A boot-time migration is added to the existing `pkg/config` `migrate*` pipeline: any persisted `Policies`/`ToolPolicies` entry keyed `exec`, `workspace_shell`, or `workspace_shell_bg` is rewritten to `bash`, taking the **strictest** value present (`deny` > `ask` > `allow`) if more than one of the three keys existed. The legacy `toolEnableToPolicy` migration table's `{"exec","exec"}` row is updated to `{"exec","bash"}` so old `tools.exec.enabled=false` configs keep producing a policy after upgrade.

**No permanent dual-key backward compatibility (operator decision, 2026-07-04):** once a legacy key is converted, it is **deleted** from the persisted config on that same boot, not merely superseded and left in place. Migration is a one-shot convert-and-clean operation; there is no ongoing code path that resolves `bash` and a legacy key against each other on every boot indefinitely. If an already-migrated config is ever found with a `bash` key sitting alongside a lingering legacy key (e.g., a hand-edited or partially-migrated file), the `bash` key's value is authoritative and the legacy key is deleted on that boot, not re-merged. See `bash-tool-spec.md` FR-M3.

### 3.7 Session cancel cascades to background bash sessions (new scope, operator decision 2026-07-04)

`bash`'s background mode (§3.1) is left running by default when its owning chat session goes idle or disconnects — matching `DevServerRegistry`'s existing independent lifecycle. But `pkg/agent/cancel.go`'s `RequestCancel` — the single canonical cancel entry point for all four surfaces (web SPA, Tier A `/cancel`, Tier B channels, CLI) — already cascades to every descendant **turn** via `collectDescendantTurnIDs`. It has no equivalent reach into detached background **processes** today, which is a real gap: an operator who explicitly cancels a session has no way to also stop a background build or long-running command it started.

This ADR adds that cascade:

- `pkg/tools/session.go`'s `ProcessSession` gains an `OwnerSessionID` field, set at creation time to the spawning turn's `transcriptSessionID`.
- `SessionManager` gains a `KillAllForSession(sessionID string) int` method.
- `CancelHooks` (`pkg/agent/cancel.go`) gains a new optional `KillBackgroundSessions func(sessionID string)` field, following the exact nil-skipped convention every other `CancelHooks` field already uses, invoked during `RequestCancel`'s existing "PHASE A: graceful cascade" step alongside `CancelPendingApprovals`.

A session with no background `bash` work sees no behavior change. See `bash-tool-spec.md` User Story 5, FR-B10/FR-B11.

## 4. Consequences

### Positive

- Closes a real, currently-exploitable inconsistency: an agent denied `exec` could already reach the same capability via `workspace_shell` with weaker deny-pattern enforcement. One tool, one policy surface, no bypass route.
- Fixes the confirmed `check_spawn_status`/`spawn` disconnect as a side effect of the merge, not a separate patch.
- `bash`'s background mode gains genuine "notify on completion" behavior for free, via a mechanism proven to already work for `spawn`.
- Establishes `AsyncNotifier` as the one place any future background-completion-driven feature (Goals, first among them) hooks in, rather than each new mechanism inventing its own bus-publish plumbing.
- Removes a meaningful amount of tool-identity, config, and UI surface that existed to service distinctions with no real behavioral or security value (three tools where the underlying primitive was already unified in two of the three cases).
- Closes a second, previously-undiscovered gap alongside the tool merge: an explicit session cancel (§3.7) now actually stops background work it started, instead of leaving it orphaned after the chat shows "canceled."
- No lingering legacy tool-policy keys survive an upgrade (§3.6) — a config inspected any time after the first post-upgrade boot shows only `bash`, never a stale `exec`/`workspace_shell`/`workspace_shell_bg` sitting unused alongside it.

### Negative / accepted

- Real capability reduction: PTY/interactive shell sessions and free-form background-command-with-port-exposure are gone. Neither is preserved elsewhere as a drop-in replacement — the PTY case has no replacement (matching Claude Code's own scope), and the port-exposure case is redirected to `web_serve`'s already-existing, safer dev-server mode.
- The policy-key migration (§3.6) is new logic, not a reuse of an existing pattern — the prior verb-first tool rename (`e72ce07f`) shipped with no equivalent migration and accepted silent breakage; that precedent is explicitly not followed here because this rename touches a tool (`exec`) with materially longer production exposure and a fail-open failure mode.
- Deleting the dedicated exec-approval flow (§3.4) means confirming first that it shows nothing materially more useful (e.g., a richer live command preview) than the generic modal — the implementation pass must verify this before deletion, not assume it.
- The cascade-cancel hook (§3.7) is new, untested-until-implemented surface on `RequestCancel` — a shared, security-sensitive code path used by all four cancel surfaces. It must be added carefully (nil-skipped like every other `CancelHooks` field) so a wiring mistake can't turn an optional hook into a hard dependency that breaks cancellation entirely when no background sessions exist.

## 5. Alternatives considered

**Keep `workspace_shell`/`workspace_shell_bg` as an “advanced” opt-in tier alongside a simplified `bash`.** Rejected — this reintroduces the exact bypass-via-inconsistent-defaults problem this ADR exists to close, just with fewer tools involved.

**Build true real-time push (`Monitor`-equivalent) now, alongside the poll-based mechanism.** Rejected for this change — it requires mid-turn content injection, a materially larger and more cross-cutting agent-loop change than a tool consolidation warrants. Deferred to its own design pass if genuinely needed.

## 6. Affected components

- Backend: `pkg/tools/shell.go`, `pkg/tools/workspace_shell.go` (deleted), `pkg/tools/workspace_shell_bg.go` (deleted), `pkg/tools/spawn.go`, `pkg/tools/spawn_status.go` (deleted), `pkg/tools/subagent.go`, `pkg/tools/session.go` (`ProcessSession.OwnerSessionID`, `SessionManager.KillAllForSession`), `pkg/agent/loop.go` (`AsyncCallback` extraction, tool wiring, `enforceEdgeModeAndDepth`/`buildDelegationDenyChecker` — see below), `pkg/agent/subturn.go` (`getSubTurnConfig`'s depth-cap resolution), `pkg/agent/loop_env.go` (`wireDelegationInjectors`), `pkg/agent/cancel.go` (`CancelHooks.KillBackgroundSessions`), `pkg/config/` (policy-key migration + cleanup, `toolEnableToPolicy` table update, retiring `Experimental.WorkspaceShellEnabled`), `pkg/coreagent/core.go` (seed policy maps).
- **Additional scope pulled in 2026-07-04** ([#477](https://github.com/elicify-ai/omnipus/issues/477), `agent-delegation-spec.md` User Story 3, FR-D9/FR-D10): the delegation graph's per-edge `Depth` authorization gate (`enforceEdgeModeAndDepth`) and the separate spawn-time depth backstop (`getSubTurnConfig`'s `defaultMaxSubTurnDepth`) read the same `SubTurn.MaxDepth` config field with contradictory zero-value semantics, and the backstop has no visibility into an edge's own explicit `Depth` — so an operator's explicit per-edge depth configuration can be silently overridden by the unrelated backstop. One shared effective-cap resolution function now backs both the delegation prompt and the spawn-time enforcement.
- Frontend: `ExecApprovalBlock.tsx`/`ExecApprovalTool.tsx` (deleted), `TerminalOutput.tsx`/`WorkspaceShellUI.tsx` (consolidated), `humanizeToolName.ts`, `toolPolicyPresets.ts`, `ToolCallBadge.tsx`, `ToolsAndPermissions.tsx`, `ExecAllowlistSection.tsx`/`ExecProxyStatusCard.tsx` (retained, re-pointed at `bash`).
- Contracts: no schema-level change (tool-policy maps are `additionalProperties`-shaped, no fixed enum) — illustrative `example:` strings updated across several schemas per Constraint #8.
- Docs: `CLAUDE.md` Hard Constraint #6 rewritten; ADR-011 marked Superseded (this document); ADR-010 gets a pointer note (tool renamed, its own decision — removing the channel block in favor of `ToolPolicyCfg` — still holds); ADR-035 gets a forward pointer.

## 7. References

- ADR-035 — the immediately preceding removal this continues in spirit and reuses `sandbox.ResolveLimits` from directly.
- ADR-009 (superseded by ADR-035), ADR-010 (still valid, tool renamed), ADR-011 (superseded by this document).
- Session design review (2026-07-04): `tool-consolidation-design.html`, reviewed and approved before this ADR was written.
- Market research: Anthropic Bash/Monitor tool documentation and the December 2025 async-subagent update ("wakes up your main agent when they're done"); Claude Code Task-tool community writeups on Task-vs-subagent execution models.
