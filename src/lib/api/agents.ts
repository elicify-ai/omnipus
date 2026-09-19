// agents.ts: Agent CRUD, assignee pickers, per-agent sessions

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  Agent as AgentSchema,
  AgentSession as AgentSessionSchema,
  ConfigurationMutationState as ConfigurationMutationStateSchema,
  // Spec-4 — external-CLI runner connection test (contract-first #8):
  RunnerTestResponse as RunnerTestResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  AgentSession,
  // Wire types migrated from hand-written interfaces to generated types:
  Agent,
  AgentUpdateRequest,
  AgentCreateRequest,
  ConfigurationMutationState,
  RunnerTestResponse,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'
import { requestConfiguration } from './configuration'

// ── Agents ────────────────────────────────────────────────────────────────────

export interface AgentShellPolicy { // not-wire-format: SPA-internal helper type — the shell_policy field on the generated Agent type is an inline anonymous object; this interface is never sent to or received from the gateway as a standalone value
  enable_deny_patterns?: boolean
  custom_deny_patterns?: string[]
}

// Agent — re-exported from generated openapi-types (contract-first #8).
// The generated type is the source of truth; see contracts/components/schemas/Agent.yaml.

// AgentKind — the agent's "kind" axis. Derived directly from the generated
// Agent['type'] so it is NOT a parallel wire type (Constraint #8): the generated
// Agent remains the single source of truth. Re-used everywhere the literal union
// 'core' | 'custom' | 'system' | 'worker' was previously repeated inline (session
// store, ToolsAndPermissions) so the literal isn't scattered.
export type AgentKind = NonNullable<Agent['type']>

// isWorker — a worker is a delegation-only labour agent: never a chat target,
// never a channel routing default, never a schedule owner. Used by the chat
// switcher, channel-routing picker, and schedule-owner picker to filter workers
// out of those selection sites. Accepts a loose shape so it works on partial
// agent objects too.
//
// W2 (agent-form-requirements): recognise both Subagent and subagent_3p (the new
// wire enum values for the user-creatable worker types). The legacy "worker"
// value is the build-time/seed config constant and is NOT emitted by the
// gateway; it is left here as a defensive fallback so callers don't break on
// stale payloads.
export function isWorker(a: { type?: string | null }): boolean {
  return a.type === 'Subagent' || a.type === 'subagent_3p' || a.type === 'worker'
}

// AssigneeTeamScope — F2: an explicit choice, not an absent optional. The
// prior signature took `teamIds?: Set<string>` as a bare positional, which
// meant a future `buildTaskAssigneeItems(agents)` call silently compiled and
// returned the unscoped roster (the omission is invisible at the call site).
// Forcing callers to name their intent (`scoped` vs `unscoped`) makes that
// mistake a type error instead of a silent behavior change. Mirrors the
// `{ kind: '...' }` discriminated-union idiom already used for hook state in
// this codebase (see `CliValidationState` in useCliPathValidation.ts).
// not-wire-format: UI-only helper type consumed by buildTaskAssigneeItems; never serialized across the gateway/SPA boundary
export type AssigneeTeamScope =
  | { kind: 'scoped'; ids: Set<string> }
  | { kind: 'unscoped' }

// buildTaskAssigneeItems — shared task-assignee `SmartSelect` item list,
// deduped out of `TaskDetailPanel` and `CreateTaskSlideOver` (Simplify
// finding, Agent System P0 fix-wave). Scoped to the task's WORKSPACE TEAM
// (core_team ∪ every delegation edge endpoint — see `useWorkspaceTeamIds`)
// so the picker mirrors what the backend actually allows instead of the
// global agent roster: `validateTaskAgentID` (`pkg/gateway/rest_tasks.go`)
// 400s any assignee outside that set. subagent_3p (external-CLI) workers are
// NO LONGER unconditionally excluded here (Fix B) — team membership is the
// only gate task assignment goes through now that external-CLI task
// execution is being wired up alongside this change, so a 3p worker that IS
// on the team is a legitimate, non-dead-end assignee. A " · Worker" suffix
// keeps every delegation-only kind (Subagent / subagent_3p / legacy worker)
// visually distinguishable (mirrors AddAgentPicker's " · leaf" convention).
// Callers prepend their own "Unassigned" (`__none__`) item.
//
// `options.teamScope` (F2 — see `AssigneeTeamScope` above):
//   - `{ kind: 'scoped', ids }` → scope the list to `ids`' members (the
//     normal, team-scoped case).
//   - `{ kind: 'unscoped' }` → NO scoping — every known agent is offered.
//     Callers pass this explicitly for the deliberate fallback when the
//     team-set query ERRORS (or has no data yet) — the backend still
//     enforces team membership server-side, so an unscoped list here is a
//     graceful degrade (matches this picker's pre-scoping behaviour) rather
//     than an empty, unusable picker. Pair an error-driven `unscoped` choice
//     with an inline "team unavailable" hint at the call site (see
//     `useWorkspaceTeamIds`) so the degrade is visible, not silent.
//
// `options.currentAssigneeId`, when given, is always included even if it
// falls outside a `scoped` set — an existing task's already-assigned agent
// (e.g. one later dropped from the workspace team — legacy data) must still
// render as the selected value instead of silently vanishing from its own
// picker.
export function buildTaskAssigneeItems(
  agents: Agent[],
  options: { teamScope: AssigneeTeamScope; currentAssigneeId?: string | null },
): { value: string; label: string; className: string }[] {
  const { teamScope, currentAssigneeId } = options
  return agents
    .filter((a) => teamScope.kind === 'unscoped' || teamScope.ids.has(a.id) || a.id === currentAssigneeId)
    .map((a) => ({
      value: a.id,
      label: isWorker(a) ? `${a.name} · Worker` : a.name,
      className: 'text-xs',
    }))
}

export function fetchAgents(): Promise<Agent[]> {
  return request<Agent[]>('/agents', undefined, z.array(AgentSchema) as ZodType<Agent[]>)
}

export function fetchAgent(id: string): Promise<Agent> {
  return request<Agent>(`/agents/${encodeURIComponent(id)}`, undefined, AgentSchema as ZodType<Agent>)
}

export function createAgent(data: AgentCreateRequest): Promise<Agent> {
  return requestConfiguration<Agent>('/agents', { method: 'POST', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

export function updateAgent(id: string, data: AgentUpdateRequest): Promise<Agent> {
  return requestConfiguration<Agent>(`/agents/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

// Delete uses the revision the user reviewed. A stale revision conflicts;
// built-in identities remain protected from deletion.
export function deleteAgent(id: string, revision: string): Promise<ConfigurationMutationState> {
  return requestConfiguration<ConfigurationMutationState>(
    `/agents/${encodeURIComponent(id)}?${new URLSearchParams({ revision })}`,
    { method: 'DELETE' },
    ConfigurationMutationStateSchema as ZodType<ConfigurationMutationState>,
  )
}

// Spec-4 FR-4.2 — external-CLI runner connection test.
// POST /api/v1/agents/{id}/runner/test validates the agent's configured external
// CLI (claude-code / codex / opencode) without running any real agent work:
// binary present + version handshake + authenticated. The response carries a
// distinct `reason` (missing-binary | unauthenticated | handshake-failed |
// unknown-cli | not-external-cli) so the UI can show a precise remedy. The
// generated RunnerTestResponse type + Zod schema are the source of truth.
export function testAgentRunner(id: string): Promise<RunnerTestResponse> {
  return request<RunnerTestResponse>(
    `/agents/${encodeURIComponent(id)}/runner/test`,
    { method: 'POST' },
    RunnerTestResponseSchema as ZodType<RunnerTestResponse>,
  )
}

// AgentSession — re-exported from generated openapi-types (no local body needed).

export function fetchAgentSessions(agentId: string): Promise<AgentSession[]> {
  return request<AgentSession[]>(`/agents/${encodeURIComponent(agentId)}/sessions`, undefined, z.array(AgentSessionSchema))
}
