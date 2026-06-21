# Sprint 1 — Foundation & UAT fixes (additive, tree-green)

**Branch:** `feat/0.1.0-s1-foundation` (off `hotfix/v0.1.1`).
**Spec of record:** `remediation-decisions.md`. **Build plan:** `0.1.0-build-plan.md`.

This wave ships everything that does **not** require the no-back-compat unified-task big-bang
(D4/Detail #7 — deleting `pkg/boardtask`+`pkg/taskstore`). Each item leaves the build green and is
independently reviewable. The unified-task epic is a separate coordinated branch (speced next).

## Track A — Backend/security (owner: backend-lead, worktree)
Owns: `contracts/`, `pkg/`, `cmd/`, `pkg/api/generated/`, `src/lib/api/generated/` (regen only). Does NOT touch `src/` hand-written code.

1. **O7 — global tool-policy enforcement (SECURITY).** `agentToolsCfgToPolicy` (`pkg/agent/instance.go`)
   drops `sandbox.tool_policies` (GlobalPolicies) before runtime `FilterToolsByPolicy`, so an admin global
   deny shows enforced in REST but does not block. **Fix:** merge global + per-agent at call time with
   **most-restrictive-wins (`deny > ask > allow`)** — global deny always blocks; an agent may tighten,
   never loosen. Add table tests covering global-deny-vs-agent-allow and agent-tighten cases. No contract change.
2. **MAJ-5 — external-CLI executor wiring.** `RunOptions` (`pkg/agent/runner/runner.go`) and the external
   dispatch path (`pkg/agent/external_dispatch.go`) do not consume `executor.cli_path` / `cli_args` /
   `env_overrides` from the agent config. **Fix:** thread these into the spawned CLI invocation (execve, no
   shell interpolation; warn-not-reject on injection chars in cli_args, per `ExecutorConfig.yaml`). The wire
   fields already exist — backend wiring only. Test: a subagent_3p with a custom cli_path/args/env spawns with them.
3. **worker-PUT-400.** PUT `/agents/{id}` on a Subagent/subagent_3p rejects a valid patch (UAT). Identify the
   over-broad `field_not_applicable_to_type` rejection in the PUT handler (`pkg/gateway/rest_agent.go`) and
   allow the fields that ARE valid for workers (model, timeout, max_tool_iterations, delegation_policy, etc.);
   keep rejecting genuinely-N/A fields (heartbeat_*, voice on subagents). Test both directions.
4. **O4-backend — self-restart endpoint.** New UI-triggerable graceful restart (drain in-flight, then re-exec
   or clean exit for a supervisor). Contract: new endpoint (e.g. `POST /api/v1/gateway/restart`) + minimal
   response schema (follow the 5-step add-a-wire-type process; regen Go+TS). Reuses existing
   `pending-restart` plumbing (`pkg/gateway/rest_pending_restart.go`). Guard behind `withAuth` +
   `RequireNotBypass`. Must be safe: graceful drain, clear status the SPA can poll on the way down and back up.

**DoD (Track A):** `gofmt -l` 0 · `make verify-contracts` clean · scoped Go tests for each item pass on the
ci-omnipus worker (tags `goolm,stdjson`) · no full local suite.

## Track B — Frontend (owner: frontend-lead, worktree)
Owns: `src/` (hand-written). Consumes `src/lib/api/generated/` (Track A regenerates; for the self-restart call,
build the modal/reattach against the existing pending-restart API — the new endpoint is wired at integration).

1. **O2 — roster IA.** Rename "BASE AGENTS" → **"MAIN AGENTS"** (custom Main agents). Keep a separate
   **"BUILT-IN ROSTER"** accordion (Mia/Jim/Ava/Ray). **Adaptive expand:** expanded when there are no custom
   Main agents, collapsed once the user has custom Main agents. Empty-state copy: *"No custom Main agents yet.
   Create one, or use a built-in below."*
2. **O3-selector (early, frontend-only).** ModelSelector: provider-headed sections **even with one provider**,
   models **sorted within group**, groups in stable order. **Drop the "calls will fail / UNRESOLVED" copy**;
   only warn (softly) when the model genuinely isn't in the chosen provider's catalog. (The structural
   `{model,provider}` field split is deferred to the task-epic regen — do NOT change the contract here.)
3. **O11 — chat polish.** Slash-command autocomplete; fix the tool-call **icon misattribution**
   (wrong agent avatar on tool blocks). Scope per O11 in the decision log.
4. **O4-frontend — restart UX.** Replace the passive "restart required" banner with a **modal on save** of any
   restart-gated setting: "Gateway restart required" → **[Restart now] [Later]**. **Later** defers (pending
   state persists). Persistent **"Restart gateway"** control in Settings → Gateway (shows pending notice +
   changed keys; always available). **Honest status:** show running value vs saved value separately. On restart,
   use existing WS reconnect + pending-restart polling to detect down→up, clear the modal, success toast.
5. **O5 — autosave everywhere.** Build one shared `Saving… / Saved` autosave hook; apply to profiles/settings/
   forms (no explicit Save where feasible). Exceptions still confirm: restart-gated (autosave writes → O4 modal),
   destructive, multi-step wizards. **MAJ-4:** never fire on initial hydration; never send fields invalid for the
   entity (no heartbeat fields for a worker). **MIN-7** (silent task agent-assignment) resolved by the indicator.
6. **MIN-9** defer `cli-detect`/authed fetches until the auth token is present. **MIN-10** fix the malformed
   `//#/agents/worker` hash URL that throws on Worker-profile open.

**DoD (Track B):** `npm run typecheck` 0 · `npx vitest run` green · no console errors in the touched flows ·
Sovereign Deep tokens (accent #d4af37; surfaces #0a0a0b/#111113/#1a1a1e/#222228; Outfit/Inter/JetBrains Mono).

## Integration & gate
Merge both worktrees into `feat/0.1.0-s1-foundation`; wire the O4 "Restart now" button to the new endpoint at
the seam. Then the **7-reviewer quality gate** (incl. a security reviewer for O7), fix wave, CI on ci-omnipus.
No merge to `main` without human review.
