# ADR-010 — Removal of GHSA-pv8c-p6jf-3fpp Channel Block on `exec`

**Status:** Accepted
**Date:** 2026-04-29
**Deciders:** architect, security-lead, backend-lead

---

## Context

The GHSA-pv8c-p6jf-3fpp advisory described a class of attack in which a
remote chat channel (Telegram, Discord, Slack, etc.) sends a message that
the LLM interprets as a request to run `exec` against the host. The original
mitigation, implemented in `pkg/tools/shell.go`, was a hard channel-layer
block: if the originating `Channel` was tagged "remote", the `exec` tool
returned an error before policy or sandbox even saw the call.

That block was the right answer in a world where:

- There was no per-agent `ToolPolicyCfg` to allow/deny `exec` declaratively.
- There was no per-agent sandbox profile to bound what `exec` could
  actually do.
- Custom-agent `system.*: deny` defaults did not exist.

After the central tool registry redesign
(`docs/internal/specs/tool-registry-redesign-spec.md`) and the per-agent sandbox
profile work (ADR-009), all three of those gaps are filled. Continuing to
hard-block `exec` at the channel layer now produces incorrect behaviour:

- Operators who deliberately want a Jim-style ops agent on Telegram cannot
  enable it even with `workspace.shell: allow` and `profile=workspace`.
- The block fires before policy, so the operator has no way to reason about
  it from `ToolPolicyCfg` alone.
- The "channel is remote" predicate is brittle — it conflates trust ("did
  the user authenticate?") with transport ("is this Slack or stdio?").

---

## Decision

The channel-layer block on `exec` in `pkg/tools/shell.go` is **removed**.

The policy plane — per-agent `ToolPolicyCfg` — becomes the sole string-level
authority over whether `workspace.shell` / `exec` is callable. The sandbox
plane (ADR-009) bounds what the resulting child process can do.

Defaults remain conservative:

- New custom agents are seeded with `system.*: deny` and
  `workspace.shell: deny` in their policy. An operator must opt in
  explicitly to enable shell execution, mirroring CLAUDE.md
  hard-constraint #6 (deny-by-default for security).
- Jim, the workspace agent, ships with `workspace.shell: allow` and
  `profile=workspace`, plus the experimental gate (ADR-011) flipped on by
  the seed.

### Migration WARN at boot

`backend Fix-6` adds a startup audit pass that scans the loaded config and
logs a `WARN` for every agent that simultaneously satisfies:

1. Has `workspace.shell` (or any `exec`-family tool) at `allow` or `ask` in
   its `ToolPolicyCfg`.
2. Is reachable on at least one channel that the operator has flagged as
   "remote" (Telegram / Discord / Slack / Matrix / etc., per the channel's
   declared trust class).

The WARN names each affected agent, the channels involved, and links to the
release notes. It does not block boot — operators may have legitimate
reasons (the Jim-on-Telegram case above) — but it is loud enough to be
noticed in systemd journal, Docker logs, or the gateway log file.

---

## Consequences

### Positive

- Policy is the single string-level gate. Operators reason about agent
  capability from one config surface, not two.
- The Jim-on-Telegram (and equivalent) ops use cases become possible
  without code changes.
- Trust decisions move out of `pkg/tools/shell.go` and into the policy
  plane, which is the correct architectural home for them.

### Negative

- **Operators who upgrade without reading the release notes could expose
  `exec` on a remote channel that was previously blocked at the channel
  layer.** The WARN is the mitigation, not a guarantee. Operators who run
  the gateway as a daemon and never read its stderr are at risk.
- The "remote channel" trust predicate is no longer free. Operators who
  want the old behaviour must encode it themselves via per-agent policy
  (deny `workspace.shell` on the agents that handle remote channels).

### Neutral

- The advisory itself remains documented. The mitigation simply moves from
  a hard-coded block to operator-managed policy plus the sandbox boundary
  from ADR-009.

---

## Alternatives Considered

### A. Keep the block, add policy on top

- Pros: No upgrade risk. Operators see no behaviour change.
- Cons: Operators cannot enable Jim-on-Telegram even with explicit policy +
  sandbox. The block becomes a hidden, unqueriable rule.
- **Rejected** as the wrong layer for this decision.

### B. Replace the block with a runtime confirmation prompt

- Pros: User-in-the-loop on the actual call.
- Cons: Already covered by `ToolPolicyCfg: ask`. A separate prompt path
  would duplicate the confirmation infrastructure.
- **Rejected** as redundant.

### C. Keep the block but make it overridable via a config flag

- Pros: Backwards-compatible default, opt-in unlock.
- Cons: A third gate in addition to policy and sandbox. Operators would
  have to set the unlock flag *and* set policy *and* set sandbox to enable
  the legitimate case — three places to get wrong.
- **Rejected** in favour of fewer, clearer gates.

---

## Affected Components

- Backend:
  - `pkg/tools/shell.go` — channel-layer block removed.
  - Boot WARN scanner (introduced by backend Fix-6) — emits the migration
    advisory.
- Frontend:
  - Release notes / migration guide must be surfaced in the operator UI.
- Variants:
  - Open Source: WARN appears in gateway log on first boot after upgrade.
  - Desktop: WARN appears in stderr captured by the Electron wrapper.
  - SaaS: WARN routed to the operator notification channel.

---

## References

- `pkg/tools/shell.go` — site of the removed block.
- `docs/internal/specs/tool-registry-redesign-spec.md` — policy plane.
- ADR-009 — per-agent sandbox profile as the execution-level boundary.
- ADR-011 — `experimental.workspace_shell_enabled` config-level gate.
- GHSA-pv8c-p6jf-3fpp — original advisory.
- CLAUDE.md hard-constraint #6 — deny-by-default for security.
