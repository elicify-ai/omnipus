# ADR-037 — Remove the Global Per-Agent Delegation Policy

**Status:** Accepted
**Date:** 2026-07-11
**Deciders:** Daniel Piatkowski (product owner); architect (ratified)
**Precedent:** [ADR-035 — Remove the Per-Agent Sandbox Profile](./ADR-035-remove-per-agent-sandbox-profile.md) — same shape of decision: delete a per-agent config surface, its UI, and its dead code paths outright (no back-compat) after a trace proved the surface did not drive the behavior it claimed to.
**Relates to:** [ADR-032 — External-Agent Workspace Execution / delegation identity](./ADR-032-external-agent-workspace-execution.md) (the sub-turn identity copy of `DelegationPolicy` this ADR retires), [ADR-029 — Channel-Instance ↔ Workspace Binding](./ADR-029-channel-instance-workspace-binding.md) (workspace-scoped delegation context).
**Implementation:** performed by a separate track ("Wave 2"), running in parallel with this ADR. This document is the decision record; the code removal is not part of it.

---

## 1. Context

Omnipus carried two independent representations of "who may delegate to whom":

1. **The global, per-agent `config.AgentConfig.DelegationPolicy`** (`pkg/config/config.go:794`, type at `:1033-1053`) — a per-agent `To []AgentRef` + `Modes` + `Depth` policy, plus a global default at `cfg.Agents.Defaults.DelegationPolicy` (`:1369`). Edited through a dedicated top-level **`/agents/trust`** screen (`src/components/screens/TrustGraphScreen.tsx`, route `src/routes/_app/agents.trust.tsx`) and summarized on each agent's edit profile (`AgentProfile.tsx`, the `delegation-policy-summary` block linking to `/agents/trust`).

2. **The per-workspace delegation graph** (`pkg/workspace/delegation.go`, `workspaces/<id>.json → Delegation[]` edges) — a directed `from_agent → to_agent` edge list with per-edge modes and depth, edited through the workspace **Team** tab.

Commit **`822202ad`** ("feat(delegation): per-workspace, graph-authoritative enforcement", 2026-06-27) made the per-workspace graph the **sole runtime authority** for every delegation decision. From that commit onward, every live gate — `delegate` (background and await modes), `create_task`/`update_task` (task mode), and the sysagent cross-workspace task tools — resolves through `buildDelegationDenyChecker` (`pkg/agent/loop.go:2105`), which reads `workspace.ReadDelegation(...)` and, in its own words, *"the per-agent `config.DelegationPolicy` is seed-only and is NOT read here"* (`loop.go:2077-2078`, `2102-2104`).

What that commit did **not** do was retire, or even relabel, the global policy's UI and API. The `/agents/trust` screen, the `PUT /api/v1/agents/{id}` `delegation_policy` field, the wizard fields, and the profile summary all kept working exactly as before — writing a value that no runtime gate reads. The two surfaces diverged silently: the graph became authoritative, the global policy became decorative, and nothing told the operator.

**A code trace (this investigation, 2026-07-11) confirmed the divergence is total for enforcement:**

- Every production delegation gate is wired unconditionally to the graph checker. `SetDelegationDenyCheckerBackground` / `SetDelegationDenyCheckerAwait` (`loop.go:1478`, `:1504`) and the task tools' `SetDelegationDenyChecker` (`:1536`, `:1576`) are all set to `buildDelegationDenyChecker(...)` on every agent-tool wiring, with no conditional that could leave them nil.
- `pkg/tools/delegate.go`'s gate consults `delegationDenyBackground`/`delegationDenyAwait` **first** and only reaches its legacy `else if` fallbacks (`allowlistCheck` → `registry.CanSpawnSubagent` → `DelegationPolicy`; `delegateChecker` → `ResolveDelegationTo`) when those checkers are nil (`delegate.go:310-333`). Since they are never nil in production, those `else if` branches — the only in-tool paths that read `DelegationPolicy` — are unreachable.
- `pkg/agent/registry.go`'s `CanSpawnSubagent` (`:130`, the sole reader of an `AgentInstance`'s copied `DelegationPolicy` field) is only reachable through that dead `allowlistCheck` fallback.
- The depth resolver (`buildDelegationDepthResolver`, `delegation_depth.go:80`) reads the matched **graph edge** and the global `SubTurn.MaxDepth`, not `DelegationPolicy`.

**Discovery.** The gap surfaced in live UAT: an operator added a delegation edge on the global `/agents/trust` screen, received a "Saved" confirmation, then found that a real, chat-driven delegation using that exact agent pair was still **denied** — because the runtime consulted only the workspace's own Team-tab edge list, which did not contain the edge. The global screen looked fully functional and was entirely decorative. A config surface that returns "Saved" but changes no behavior is a trust gap between what the UI claims and what the system does — the same class of defect ADR-035 removed for the sandbox profile.

## 2. Architectural context: delegation trust is workspace-scoped, not a global agent property

Beyond "the global policy is dead code," the global policy is **structurally wrong** for what delegation trust is:

- The same agent can be a member of multiple workspaces with completely different rosters and different trust relationships in each. `pkg/workspace/workspace.go`'s `CoreTeam` and `Delegation` fields are already fully per-workspace-file, with no cross-workspace uniqueness constraint. "Jim can delegate to Worker in Workspace A but not in Workspace B" is therefore already directly representable in the graph model.
- A **global, agent-level** policy cannot express that. A single `AgentConfig.DelegationPolicy.To` list is one value shared across every workspace the agent belongs to; it would either wrongly grant the trust in the workspace that should deny it, or wrongly restrict it in the workspace that should allow it. There is no correct global value.
- This makes the divergence in §1 not a bug to reconcile by re-connecting the global policy to enforcement, but a design that was superseded on purpose: the graph is authoritative **because** it is the only representation that can be correct.

The one legitimate remaining role for the per-agent policy is as a **seed template**. `defaultWorkspaceDelegationEdges` (`pkg/gateway/rest_workspace_delegation.go:170-205`) reads each agent's `DelegationPolicy.To/Modes/Depth` at **new-workspace creation** to bootstrap that workspace's graph (Jim→Ava/Ray/worker, the specialist edges, etc.), so a fresh workspace works out of the box. That seed data originates in `pkg/coreagent/core.go`'s `coreAgentDelegation` (`:755-820`), applied by `SeedConfig`. The seed is real and must be preserved — but as bootstrap data for the graph, not as a user-facing per-agent policy concept.

## 3. Decision

Remove the global per-agent delegation policy entirely — no deprecation shim, no backward compatibility — and make the per-workspace delegation graph the sole, exclusively-documented delegation mechanism:

- **Go**: delete `config.AgentConfig.DelegationPolicy`, the global `cfg.Agents.Defaults.DelegationPolicy`, the `DelegationPolicy` type and its helpers that exist only to serve them (`ResolveDelegationTo`, `ResolveDelegationPolicy`, `WarnIfInertFieldsSet`, `IsDelegationModeAllowed`/`ResolveDelegationDepth` where orphaned), and the now-dead enforcement fallbacks: `registry.go`'s `CanSpawnSubagent` `DelegationPolicy` branch and `delegate.go`'s `allowlistCheck`/`delegateChecker` legacy paths, along with the `AgentInstance.DelegationPolicy` identity field and the `subturn.go:644` copy of it. (The `DelegationMode` type and the workspace edge's own modes/depth stay — they are the graph's, not the global policy's.)
- **Wire contract**: remove the per-agent `delegation_policy` field from `Agent`, `AgentCreateRequestMain`, `AgentCreateRequestSubagent`, `AgentCreateRequestSubagent3p`, and `AgentUpdateRequest`; regenerate `pkg/api/generated/` and `src/lib/api/generated/`. The **workspace** graph schemas (`WorkspaceDelegation`, `WorkspaceDelegationEdge`) stay — they are the real mechanism — but `WorkspaceDelegation.yaml`'s description prose (`:7`, *"The per-agent delegation_policy remains as an enforcement cap"*) must be corrected, since that claim is false and is itself part of the divergence this ADR closes. `ExecutorConfig.yaml`'s reference (`:25`, listing `delegation_policy` among fields hidden for `subagent_3p`) must be dropped from that comment.
- **Frontend**: delete `TrustGraphScreen.tsx`, its route `src/routes/_app/agents.trust.tsx`, its test/model files (`delegation/graphModel.ts`, `TrustGraphScreen.test.tsx`), the `/agents/trust` nav entry in `AgentListScreen.tsx`, the `delegation-policy-summary` block and its `/agents/trust` link in `AgentProfile.tsx`, and the delegation-policy fields in the create wizard (`CreateAgentWizard.tsx`, `wizard/Advanced.tsx`, `wizard/types.ts`). Workspace Team-tab delegation editing is untouched.
- **Seed relocation, not preservation as a concept**: Wave 2 relocates the seed data currently sourced from `AgentConfig.DelegationPolicy` (via `coreAgentDelegation`) so `defaultWorkspaceDelegationEdges` keeps producing the same bootstrap graph without the field living on `AgentConfig`. The seed remains; the user-facing per-agent policy does not.

## 4. Consequences

### Positive
- Closes the UAT-confirmed trust gap: there is no longer a "Saved"-but-inert delegation surface. The only place an operator edits delegation is the workspace Team tab, which is exactly what enforcement reads.
- One representation of delegation trust, and it is the structurally correct one (workspace-scoped). Removes a global, agent-level model that could not express per-workspace trust correctly.
- Removes dead code: two unreachable enforcement fallbacks (`delegate.go` `else if` branches, `CanSpawnSubagent`'s `DelegationPolicy` branch), a write-only `AgentInstance.DelegationPolicy` identity field, and the `DelegationPolicy` type + resolver helpers that existed only to serve them.
- Corrects contract and code prose that actively mis-described the relationship (the `WorkspaceDelegation.yaml` "enforcement cap" claim), which had been reinforcing the wrong mental model.

### Negative / accepted
- A pre-upgrade `config.json` carrying `delegation_policy` on an agent (or `agents.defaults.delegation_policy`) is now an unknown field. Consistent with ADR-035's no-back-compat choice, the config-load path must tolerate it (ignore-and-load, not fail) — but note the ADR-035 §7 finding: the `PUT /api/v1/agents/{id}` decode path is non-strict by default, so a client still sending `delegation_policy` on a PUT would have the field **silently dropped** and get 200 OK. Wave 2 should decide explicitly whether to mirror ADR-035's fix (sniff the raw body for the retired key and return an actionable 400) or accept the silent drop; this ADR flags it rather than presuming.
- The `Agent*` wire schemas change (field removal), so `make verify-contracts` and the generated types must be regenerated in the same commit (Constraint #8). Roughly 11 Go test files and several TS test files that assert on `DelegationPolicy` / `delegation_policy` / the trust screen will need updating or deleting.

### What does NOT change
- **Workspace Team-tab delegation editing is completely unaffected.** It was already the sole real mechanism before this ADR; this change removes the decorative twin, not the working one.
- Runtime delegation enforcement is byte-for-byte the same — it already read only the graph. No delegation that is allowed today becomes denied, and none that is denied becomes allowed, purely from this removal. (The seed relocation must preserve the same bootstrap edges for a fresh install — that is Wave 2's correctness obligation, verified against the current `coreAgentDelegation` output.)

## 5. Affected components

Verified against the tree at `hotfix/v0.1.1` @ `4a2526a4` on 2026-07-11. Wave 2 may already have begun in parallel, so some entries may be partially removed by the time they are read — note discrepancies rather than treating them as errors.

- **Backend (delete / edit):**
  - `pkg/config/config.go` — `AgentConfig.DelegationPolicy` (`:794`), `DelegationPolicy` type (`:1033-1053`), `Defaults.DelegationPolicy` (`:1369`), helpers (`ResolveDelegationTo` `:1101`, `ResolveDelegationPolicy` `:1173`, `WarnIfInertFieldsSet` `:1063`, `IsDelegationModeAllowed` `:1188`, `ResolveDelegationDepth` `:1205`), and the `validatePolicy` block (`:1886-1911`).
  - `pkg/coreagent/core.go` — `coreAgentDelegation` (`:755-820`) and its `SeedConfig` call sites (`:932-934`, `:986`): the seed data is relocated, not deleted.
  - `pkg/agent/registry.go` — `CanSpawnSubagent`'s `DelegationPolicy` branch (`:137-152`).
  - `pkg/agent/instance.go` — `AgentInstance.DelegationPolicy` field (`:75-77`) and its population (`:170-178`, `:373`).
  - `pkg/agent/subturn.go` — the `DelegationPolicy: execSource.DelegationPolicy` copy (`:644`).
  - `pkg/agent/loop.go` — the `SetAllowlistChecker`/`SetDelegateChecker` closures that call `ResolveDelegationTo`/`CanSpawnSubagent` (`:1465-1497`); comments referencing the retired field (`:393`, `:1461-1463`, `:2078`, `:2103`).
  - `pkg/tools/delegate.go` — the `allowlistCheck`/`delegateChecker` legacy fields, their setters, and the `else if` fallback branches (`:107-166`, `:315-332`).
  - `pkg/gateway/rest_workspace_delegation.go` — `defaultWorkspaceDelegationEdges` (`:170-205`): repoint to the relocated seed source (keep the function; change where `dp` comes from).
  - `pkg/gateway/rest.go`, `pkg/gateway/rest_agent_delegation.go` — any create/update handler reads of `delegation_policy`.
  - `pkg/task/task.go`, `pkg/tools/result.go` — incidental `DelegationPolicy` references to audit.
  - **[Found during Wave 2 review, 2026-07-11 — missing from the original list above.]** `pkg/sysagent/tools/agent.go` — the `create_agent`/`update_agent` system-tool schema still advertises a `can_delegate_to` parameter that writes `config.AgentConfig.CanDelegateTo`, a field whose last reader this removal deleted. An LLM agent (e.g. Ava) can still set it, believing it grants delegation trust, but it is now completely inert. Flagged independently by silent-failure-hunter and comment-analyzer; the parameter and the `CanDelegateTo` field are being removed by the Wave 2 backend track (see §7).
- **Contracts (delete the per-agent field; keep the graph):**
  - Remove `delegation_policy` from `contracts/components/schemas/{Agent,AgentCreateRequestMain,AgentCreateRequestSubagent,AgentCreateRequestSubagent3p,AgentUpdateRequest}.yaml` and the mirrored `pkg/gateway/inboundschemas/` copies; regenerate `pkg/api/generated/{openapi_types.gen.go,contract_test.go,fixtures.go}` and `src/lib/api/generated/{openapi-types.ts,schemas.ts}`.
  - Correct prose in `contracts/components/schemas/WorkspaceDelegation.yaml` (`:7`) and `ExecutorConfig.yaml` (`:25`).
  - **Keep** `contracts/components/schemas/WorkspaceDelegation.yaml` / `WorkspaceDelegationEdge.yaml` structure — the graph is the surviving mechanism.
- **Frontend (delete):**
  - `src/components/screens/TrustGraphScreen.tsx`, `src/components/screens/TrustGraphScreen.test.tsx`, `src/routes/_app/agents.trust.tsx` (and its `routeTree.gen.ts` entry — regenerated), `src/components/agents/delegation/graphModel.ts` + `graphModel.test.ts`.
  - `src/components/screens/AgentListScreen.tsx` — `/agents/trust` nav entry (`:614`).
  - `src/components/agents/AgentProfile.tsx` — `delegation-policy-summary` / `delegation-policy-link` block (`:957-981`) and the `agent.delegation_policy` read; `AgentProfile.test.tsx` assertions.
  - `src/components/agents/CreateAgentWizard.tsx`, `wizard/Advanced.tsx`, `wizard/types.ts` — delegation-policy fields.
- **Tests (update or delete — ~11 Go + several TS):** `pkg/agent/{delegation_enforce_test.go,delegation_wiring_test.go,worker_delegation_test.go}`, `pkg/config/kind_validation_test.go`, `pkg/coreagent/{specialist_seed_test.go,worker_seed_test.go}`, `pkg/gateway/{rest_agent_delegation_test.go,rest_agent_executor_test.go,rest_agent_type_test.go,rest_workspace_delegation_test.go}`, `pkg/api/generated/contract_test.go`; TS: `AgentProfile.test.tsx`, `TrustGraphScreen.test.tsx`, `delegation/graphModel.test.ts`.

## 6. References

- **ADR-035** (precedent — outright removal of a per-agent config concept, its UI, and its dead paths after a trace disproved its claimed effect; see its §7 for the decode-strictness and config-load-tolerance findings this ADR should heed).
- **ADR-032** (delegation identity: the sub-turn sources agent-level fields from the target; `DelegationPolicy` was one such field and is retired here).
- **ADR-029** (workspace-scoped delegation context — the model that makes the per-workspace graph the correct home for trust).
- **Commit `822202ad`** (2026-06-27) — "feat(delegation): per-workspace, graph-authoritative enforcement": the change that silently demoted the global policy to seed-only without updating its UI/API.
- **UAT investigation (2026-07-11)** — live reproduction of the "Saved but inert" `/agents/trust` edit; code trace confirming the graph is the sole runtime authority and `DelegationPolicy` is read only as a seed template.
- Code anchors: `pkg/agent/loop.go:2074-2134` (`buildDelegationDenyChecker`), `pkg/agent/delegation_context.go:15-18`, `pkg/gateway/rest_workspace_delegation.go:162-205`, `pkg/coreagent/core.go:740-821`.

## 7. Post-decision review & fixes

The Wave 2 removal diff (backend + frontend) went through the mandatory 7-reviewer gate (architect + the six pr-review-toolkit reviewers). The removal itself was approved clean by most reviewers. Findings recorded 2026-07-11:

1. **`PUT`-decode-strictness question (§4) — RESOLVED.** Wave 2 implemented the ADR-035 §7 finding #2 fix: `PUT /api/v1/agents/{id}` now sniffs the raw request body for a `delegation_policy` key and returns an explicit 400 (mirroring the `sandbox_profile` precedent) rather than silently dropping the retired field. The config-load path tolerates a legacy persisted `delegation_policy` (ignore-and-load), so a pre-upgrade `config.json` still loads without error.

2. **Seed relocation done.** The `DelegationPolicy` shape survives only as an unexported seed DTO for new-workspace bootstrap (`coreagent.SeedDelegationEdges`); it is never persisted on `AgentConfig` and never crosses the wire. `defaultWorkspaceDelegationEdges` was repointed to it.

3. **Inert `can_delegate_to` system-tool parameter (silent-failure-hunter + comment-analyzer, converged).** `pkg/sysagent/tools/agent.go` still advertised a `can_delegate_to` parameter writing `AgentConfig.CanDelegateTo`, whose last reader this removal deleted — a settable-but-inert knob an LLM agent could believe grants trust. Recorded in §5 as a found-during-review addendum; the parameter and field are removed by the Wave 2 backend track.

4. **Stale-reference sweep (comment-analyzer, repo-wide).** Several living reference docs and one code comment still described the retired mechanism as active. Corrected in a follow-up commit (this ADR's own author, except where noted):
   - `pkg/workspace/workspace.go`'s `Delegation` field doc comment repeated the exact false *"the per-agent delegation_policy remains the enforcement cap"* claim this ADR was written to correct (also independently found by architect) — corrected by the Wave 2 backend track.
   - `CLAUDE.md`'s "Delegation identity" entry carried a stale `DelegationPolicy` field-list claim and had no ADR-037 entry — corrected and a "Delegation Graph removal (ADR-037)" entry added by the coordinator.
   - `docs/internal/architecture/ADR-032` (header amendment block) still listed `DelegationPolicy` among the fields `spawnSubTurn` sources from the target — a dated `2026-07-11` amendment was appended.
   - `docs/internal/architecture/agent-types-field-matrix.md`, `docs/internal/specs/agent-config-matrix-spec.md`, and `docs/internal/specs/agent-form-requirements.md` still documented `delegation_policy.*` and the `/agents/trust` editor as active fields/surfaces — struck and marked "removed by ADR-037" at each site.
   - `docs/internal/specs/v01-spec3-agents-delegation-orchestrator-spec.md` (the original planning spec for the retired design) — a "Superseded by ADR-037" banner was added at the top rather than rewriting the dated planning doc.
