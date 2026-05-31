# ADR-011 — `experimental.workspace_shell_enabled` Defaults to `false`

**Status:** Accepted
**Date:** 2026-04-29
**Deciders:** architect, backend-lead

---

## Context

ADR-009 establishes the per-agent sandbox profile as the execution-level
security boundary. ADR-010 removes the GHSA-pv8c-p6jf-3fpp channel-layer
block on `exec` and consolidates string-level enforcement on
`ToolPolicyCfg`.

Together those two ADRs are sufficient to gate `workspace.shell` correctly
*for an operator who has read the migration documentation*. They are not,
on their own, sufficient for an operator who upgrades from an older
release without reading release notes — the channel block silently
vanishes, and any agent that already had a permissive policy (e.g. an
operator who configured `system.*: allow` years ago and forgot) would
immediately become capable of `workspace.shell` on a remote channel.

CLAUDE.md hard-constraint #6 requires deny-by-default for security
features. We need an additional, config-level off-switch that is
independent of the policy plane and forces operators to explicitly opt in
once per gateway, not once per agent.

---

## Decision

A new boolean config key, `experimental.workspace_shell_enabled`, gates
the entire `workspace.shell` tool family at the registration layer.

**Default value: `false`.**

When the gate is `false`:

- `workspace.shell` is not registered on the tool registry. No agent can
  call it regardless of `ToolPolicyCfg`.
- The REST handler refuses to set `workspace.shell` to `allow` or `ask`
  for any agent and returns an actionable error pointing the operator at
  the gate.

When the gate is `true`:

- `workspace.shell` is registered. Per-agent `ToolPolicyCfg` then governs
  which agents can call it (still deny-by-default for new agents).
- The sandbox plane (ADR-009) bounds what the spawned child can do.

### Jim's seed flips it on idempotently

Per backend Fix-4, the Jim provisioning seed checks
`experimental.workspace_shell_enabled` at provision time and flips it to
`true` if it is currently `false`. The flip is idempotent: rerunning Jim's
seed on a config where the gate is already `true` is a no-op. The flip is
also logged at `INFO` so the operator sees it happen.

This means the common path — operator runs the onboarding wizard, accepts
Jim — works with no extra steps. The gate is a barrier for *upgraded*
gateways where Jim already exists but the operator might not realise the
old channel block is gone.

### Custom agents are double-denied

Custom agents start with `workspace.shell: deny` in their policy. To enable
the tool on a custom agent, the operator must:

1. Ensure `experimental.workspace_shell_enabled = true` (gateway-level
   opt-in).
2. Set `workspace.shell: allow` in that agent's `ToolPolicyCfg`
   (per-agent opt-in).

Two opt-ins, two distinct config surfaces. This is intentional friction for
power users, and it is acceptable in the open-source variant's first
release — the cost of one extra config key dwarfs the cost of an
accidental remote-shell exposure.

---

## Consequences

### Positive

- Deny-by-default at the gateway level, in addition to deny-by-default at
  the per-agent level. Defence in depth.
- Operators who upgrade and never touch their config get a safe default
  even if they have permissive `ToolPolicyCfg` entries.
- Jim's onboarding seed makes the common path frictionless — no operator
  ever has to discover the gate manually for the supported use case.
- The gate is a single grep-able key. Auditors can answer "is shell
  execution on?" without reading per-agent policy.

### Negative

- Power users who write custom agents must flip the gate themselves and
  also configure per-agent policy — two places to get right.
- The `experimental.` namespace implies this gate may move or be renamed.
  Operators who script around it will need to track that change. We will
  publish a deprecation notice if the gate is renamed.
- One more thing to check during incident response. "Why can't this agent
  run shell?" now has three possible answers: gate, policy, sandbox.

### Neutral

- The gate is config-level, not build-time. SaaS deployments can enable it
  without a binary rebuild. Open-source distributions ship with it `false`.

---

## Alternatives Considered

### A. No gate; rely on per-agent policy alone

- Pros: One fewer config key. Fewer places for things to go wrong.
- Cons: An operator with a pre-existing permissive agent policy
  immediately gains shell capability on upgrade. Violates
  hard-constraint #6.
- **Rejected** as too risky for the upgrade path.

### B. Build-time gate (compile flag instead of config flag)

- Pros: Cannot be flipped at runtime by a compromised admin token.
- Cons: SaaS deployments and Desktop variant cannot enable Jim without
  shipping two binaries. Defeats the single-binary constraint
  (CLAUDE.md hard-constraint #1) for the workspace use case.
- **Rejected** in favour of runtime config.

### C. Default the gate to `true` and rely on the WARN from ADR-010

- Pros: Frictionless for power users.
- Cons: Reverses the deny-by-default posture. The WARN is loud but not
  blocking, and operators have demonstrably ignored loud warnings before
  (e.g. the master.key backup warning).
- **Rejected** as inconsistent with hard-constraint #6.

---

## Affected Components

- Backend:
  - `pkg/config/` — `Experimental.WorkspaceShellEnabled` field, default
    `false`.
  - Tool registration site for `workspace.shell` — checks the gate at
    registration time.
  - `pkg/gateway/rest.go` — refuses `allow` / `ask` policy on
    `workspace.shell` when the gate is `false`.
  - Jim provisioning seed — idempotent flip with `INFO` log.
- Frontend:
  - Agent policy editor — disables the `workspace.shell` row with a
    tooltip pointing at the gate when the gate is `false`.
- Variants:
  - Open Source: defaults to `false`. Operators flip it via Jim's seed or
    manually.
  - Desktop: same default; Electron wrapper surfaces a one-time prompt
    when Jim is provisioned.
  - SaaS: defaults to `false`; tenant admins may flip per-workspace.

---

## References

- `CLAUDE.md` hard-constraint #6 — deny-by-default for security, opt-in
  for features.
- `CLAUDE.md` hard-constraint #1 — single binary (rules out compile-time
  gates for runtime-toggleable features).
- ADR-009 — per-agent sandbox profile as the execution-level boundary.
- ADR-010 — removal of the GHSA-pv8c-p6jf-3fpp channel block.
- `docs/internal/specs/tool-registry-redesign-spec.md` — `ToolPolicyCfg` policy
  plane.
