# ADR-009 — Per-Agent Sandbox Profile is the Security Boundary

**Status:** Accepted
**Date:** 2026-04-29
**Deciders:** architect, security-lead, backend-lead

---

## Context

Until this PR the security boundary for the `workspace.shell` / `exec` family of
tools was a hybrid of two layers, neither of which was authoritative on its
own:

1. A **per-tool channel allowlist** (`pkg/tools/shell.go`) that hard-rejected
   `exec` invocations whose originating channel was a "remote" channel
   (Telegram, Discord, Slack, etc.). This was tagged in code as the
   "GHSA-pv8c-p6jf-3fpp block".
2. **Application-level filesystem and command checks** scattered through the
   shell tool implementation (path canonicalisation, command argv
   inspection).

Both layers shared a fundamental weakness: they ran *inside* the same Go
process as the agent loop and made trust decisions on string-shaped inputs
that the LLM could phrase its way around. They also did not compose with the
new per-agent `ToolPolicyCfg` model from the central tool registry redesign
(`docs/internal/specs/tool-registry-redesign-spec.md`) — the channel block fired
*before* policy was consulted, so even an operator who explicitly granted
`workspace.shell` to a custom agent on a remote channel could not opt in.

We needed a single, kernel-anchored boundary that:

- Is enforced by Linux kernel primitives (Landlock + seccomp + the egress
  proxy) where available, and degrades gracefully on platforms that lack them
  (CLAUDE.md hard-constraint #4).
- Composes with `ToolPolicyCfg` so that policy is the only string-level gate
  and the kernel sandbox is the only execution-level gate.
- Is per-agent, so that Jim (the workspace agent) and Ava (the orchestrator)
  can run with different blast radii in the same gateway process.

---

## Decision

The per-agent **sandbox profile** is the authoritative security boundary for
any tool that spawns a child process. There are four profiles, defined in
`pkg/sandbox/profile.go` (`LimitsForProfile`) and surfaced as
`config.SandboxProfile`:

| Profile          | Filesystem                              | Network                              | Intended use                          |
|------------------|-----------------------------------------|--------------------------------------|---------------------------------------|
| `workspace`      | `WorkspaceDir` only (Landlock)          | None (no egress proxy)               | Default for agents that touch files   |
| `workspace+net`  | `WorkspaceDir` only                     | Egress proxy with allowlist          | Build/install steps that need fetch   |
| `host`           | Wider host paths (npm cache, etc.)      | Egress proxy                         | Power-user agents (Jim, ops)          |
| `off`            | Unrestricted (god mode)                 | Unrestricted                         | Operator-only, latched (see below)    |

The empty string and `none` are normalised to `workspace` — safe by default.

### Three latches for `off`

`off` disables Landlock, the seccomp filter, and the egress proxy. Because it
removes the kernel boundary entirely, it is gated by **three independent
latches** that must all be open simultaneously:

1. **Build tag** — the `godmode` build tag must be present at compile time
   (`pkg/sandbox/godmode_on.go` vs `godmode_off.go`). Distribution binaries
   ship without it.
2. **Boot flag** — even with the build tag, the gateway must be started with
   the explicit `--allow-godmode` (or equivalent config) flag.
3. **UI confirmation** — switching an agent to `off` from the UI requires a
   typed confirmation in the agent edit dialog; the REST handler returns 403
   if the in-memory `gateway.allowGodmode` is false (see `pkg/gateway/rest.go`
   in the agent-update path).

Any one latch blocks the others — there is no single kill-switch an attacker
can flip via prompt injection.

### Composition with `ToolPolicyCfg`

The two layers compose orthogonally:

- **Policy plane** (`ToolPolicyCfg` per agent): decides *whether* a tool name
  is callable at all (`allow` / `ask` / `deny`). String-level. Cheap to
  evaluate. Resolves per turn.
- **Sandbox plane** (`SandboxProfile` per agent): decides *what the tool's
  child process can actually do* once it is allowed. Kernel-level.
  Inherited via TSYNC into all descendant processes.

The agent loop's coercion path (`pkg/agent/loop.go`) reads the agent's
profile and threads it through to the spawn site. The REST handler's 403
check (`pkg/gateway/rest.go`) refuses requests that would set `profile=off`
on an agent without the operator-level latches open.

### Removal of the channel-layer block

The GHSA-pv8c-p6jf-3fpp channel allowlist in `pkg/tools/shell.go` is removed
in this PR. It is superseded by the policy plane (which is now the only
string-level gate) and the sandbox plane (which is the only execution-level
gate). See ADR-010 for the migration semantics.

---

## Consequences

### Positive

- One boundary, not two. Reviewers and operators have a single mental model
  for "what can this agent do" — read the profile, read the policy, done.
- Kernel-enforced where it matters. On Linux 5.13+ with the appropriate
  capabilities, the boundary survives a fully-compromised LLM prompt.
- Composable. New tools that spawn children (e.g. future MCP-launched
  helpers) inherit the boundary for free as long as they go through the
  shared spawn helper.
- Per-agent blast radius. Jim can run `workspace+net`; Ava can stay at
  `workspace`; neither change requires a binary rebuild.

### Negative

- **Non-Linux platforms have no kernel boundary.** macOS and Windows fall
  back to the application-level checks defined by the `Fallback` sandbox
  backend. The gateway logs a `WARN` at boot describing the degraded state,
  per CLAUDE.md hard-constraint #4. Operators on those platforms must treat
  `workspace.shell` as a trusted-agent-only tool until a platform-native
  backend ships.
- **Seccomp filter is process-wide, not per-profile.** The seccomp filter is
  installed once at process start and inherited into every child via
  TSYNC, so all profiles currently share the same syscall filter. A future
  refactor will install the filter per-spawn so that `workspace` can deny
  syscalls that `host` permits. Tracked as a follow-up; the current
  filter is the strict superset that all profiles need, which is safe but
  not minimal.
- The `off` profile exists. Three latches make it hard to enable
  accidentally, but operators who flip all three knowingly accept full
  responsibility — this is documented in the UI confirmation copy.

### Neutral

- Existing agents migrate to `workspace` by default (empty profile is
  normalised). No behavioural change for agents that previously did not
  use the `exec` tool.

---

## Alternatives Considered

### A. Keep the channel allowlist, add `ToolPolicyCfg` on top

- Pros: Smallest diff. No need to migrate operators.
- Cons: Two string-level gates with overlapping but non-identical rules —
  exactly the failure mode that motivated the redesign. Operators who
  granted policy could not understand why the channel block still fired.
- **Rejected** because it cements the layer the redesign was meant to remove.

### B. Single global sandbox profile (gateway-wide)

- Pros: Simpler config. One latch.
- Cons: Forces every agent to the most permissive profile any one agent
  needs. Defeats per-agent blast radius.
- **Rejected** as incompatible with the multi-agent model.

### C. Per-tool sandbox profile (not per-agent)

- Pros: Tool authors specify their own footprint.
- Cons: Encourages tool authors to over-request. Operators lose the ability
  to restrict a tool to a workspace context for one agent and a host context
  for another.
- **Rejected** in favour of per-agent because the deployment unit is the
  agent, not the tool.

---

## Affected Components

- Backend:
  - `pkg/sandbox/profile.go` — `LimitsForProfile`, profile semantics
  - `pkg/sandbox/godmode_on.go`, `godmode_off.go` — build-tag latch
  - `pkg/agent/loop.go` — coercion of agent profile into spawn limits
  - `pkg/gateway/rest.go` — 403 check on profile-update path
  - `pkg/config/` — `SandboxProfile` enum
- Frontend:
  - Agent edit dialog — typed confirmation for `off`
- Variants:
  - Open Source: ships without `godmode` build tag by default.
  - Desktop: same as open source.
  - SaaS: ships without `godmode` build tag and rejects any config attempt to
    set `profile=off`.

---

## References

- `docs/internal/specs/tool-registry-redesign-spec.md` — central tool registry +
  per-agent `ToolPolicyCfg` policy plane.
- `pkg/sandbox/profile.go` — profile-to-limits mapping.
- `pkg/agent/loop.go` — profile coercion.
- `pkg/gateway/rest.go` — REST 403 latch.
- `CLAUDE.md` hard-constraints #4 (graceful degradation) and #6
  (deny-by-default).
- ADR-010 — removal of the GHSA-pv8c-p6jf-3fpp channel block.
- ADR-011 — `experimental.workspace_shell_enabled` config gate.
