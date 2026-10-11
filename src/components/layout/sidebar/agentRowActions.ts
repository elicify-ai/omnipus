/**
 * Row-menu actions for one sidebar agent row (DECISIONS.md D4/D5/D8,
 * ADR-hide-from-sidebar.md).
 *
 * Every mutation rides the existing revision-guarded workspace PUT. Remove
 * reuses the Team-panel path — no second removal mechanism (ADR §Decision):
 * the same shape `WorkspaceTeamTab.saveFn` saves, in one PUT, core_team
 * before/with edges. A main chat is NEVER deleted here: the server
 * garbage-collects the member's config and releases no session
 * (rest_workspaces.go::applyUpdate; founder ruling R2).
 */
import {
  fetchWorkspace,
  fetchWorkspaceDelegation,
  updateWorkspace,
  type Agent,
  type Workspace,
  type WorkspaceMemberConfig,
  type WorkspaceUpdateRequest,
} from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { isApiError } from '@/lib/api-error'
import { logDiagnostic } from '@/lib/telemetry'

/** The member ids carrying hidden_from_sidebar in this workspace's configs. */
export function hiddenAgentIds(workspace: Workspace): string[] {
  return Object.entries(workspace.member_configs ?? {})
    .filter(([, member]) => member.hidden_from_sidebar === true)
    .map(([id]) => id)
}

/**
 * D8: why the row's Remove is disabled, or null when removal is allowed.
 * (i) Admin is not a member and never can be (validateCoreTeamMembers), so
 * there is no membership to end; (ii) the workspace's DEFAULT agent — the
 * same refusal the Team panel already makes ("Cannot remove … — it is the
 * default agent", WorkspaceTeamTab::handleRemoveMember); (iii) a built-in
 * (locked) member of the DEFAULT workspace is re-added at every boot
 * (ensureBuiltinRosterPresent), so a successful remove would be silently
 * undone on restart — the menu refuses it up front with that reason.
 * Built-ins on NON-default workspaces are removable; nothing re-adds them
 * there (ADR §Rules Q3).
 */
export function removeDisabledReason(agent: Agent | undefined, workspace: Workspace): string | null {
  if (agent?.id === 'admin') return 'Admin is not a workspace member.'
  if (agent?.default === true) return `${agent.name} is the default agent.`
  if (workspace.is_default === true && agent?.locked === true) {
    return 'Built-in agents cannot leave the default workspace.'
  }
  return null
}

function isConflict(err: unknown): boolean {
  return isApiError(err) && err.status === 409
}

function memberConfigsBody(ids: string[], hidden: boolean): Record<string, WorkspaceMemberConfig> {
  return Object.fromEntries(ids.map((id) => [id, { hidden_from_sidebar: hidden }]))
}

/**
 * The ONE bounded 409 recovery shared by Hide and Remove (S4-07): refetch
 * the workspace and let `retry` rebuild the request body against the fresh
 * record. `retry` returns null when the fresh record already satisfies the
 * request (S4-08) — success, no second PUT. Runs only after a 409; a second
 * conflict propagates to the caller's visible error surface.
 */
async function retryOnceOnConflict(
  workspaceId: string,
  retry: (fresh: Workspace) => WorkspaceUpdateRequest | null | Promise<WorkspaceUpdateRequest | null>,
): Promise<void> {
  const fresh = await fetchWorkspace(workspaceId)
  const body = await retry(fresh)
  if (body === null) return // the fresh record already agrees
  await updateWorkspace(workspaceId, body)
}

/**
 * D5: set hidden_from_sidebar for the listed members in ONE minimal PUT —
 * `{ revision, member_configs: { <id>: { hidden_from_sidebar } } }`. Absent
 * members keep their stored heartbeat (contract: hidden_from_sidebar is the
 * one preserve-when-absent field; heartbeat keeps its own semantics).
 *
 * On 409 the workspace is refetched and the request retried ONCE with the
 * fresh revision, for the ids the fresh record still disagrees about; a
 * second conflict throws, and the caller surfaces the error visibly.
 */
export async function setHiddenFromSidebar(
  workspace: Workspace,
  agentIds: string[],
  hidden: boolean,
): Promise<void> {
  if (agentIds.length === 0) return
  try {
    await updateWorkspace(workspace.id, {
      revision: workspace.revision,
      member_configs: memberConfigsBody(agentIds, hidden),
    })
    return
  } catch (err) {
    if (!isConflict(err)) throw err
  }
  await retryOnceOnConflict(workspace.id, (fresh) => {
    const stillDisagreeing = agentIds.filter((id) => {
      const member = fresh.member_configs?.[id]
      return hidden ? member !== undefined && member.hidden_from_sidebar !== true : member?.hidden_from_sidebar === true
    })
    // S4-08, explicit: a fresh record that already agrees — the flag was
    // applied by another writer, or a member's config has vanished (which
    // counts as agreeing for a hide) — is SUCCESS without a second PUT, not
    // a skipped retry. The null return is the helper's "nothing to send".
    if (stillDisagreeing.length === 0) return null
    return {
      revision: fresh.revision,
      member_configs: memberConfigsBody(stillDisagreeing, hidden),
    }
  })
}

/**
 * The Team-tab-shaped removal body against ONE record — the same mutation
 * `WorkspaceTeamTab.saveFn` saves: revision + core_team minus the agent +
 * the delegation graph minus the agent's edges, in one PUT. Returns null —
 * success without a PUT — when the record already satisfies the removal
 * (S4-08): the team does not list the agent, so another writer removed it.
 * Throws the visible refusal when the record carries NO core_team at all
 * (S4-01): the field is optional on the wire — the server omits it for an
 * empty team (pkg/workspace/workspace.go, `core_team,omitempty`) — and a
 * removal is never built from a list the record does not carry.
 */
async function removalBody(record: Workspace, agentId: string): Promise<WorkspaceUpdateRequest | null> {
  if (record.core_team === undefined) {
    throw new Error("this workspace's team list is unavailable — reopen the sidebar and try again")
  }
  if (!record.core_team.includes(agentId)) return null // already removed elsewhere (S4-08)
  const graph = await fetchWorkspaceDelegation(record.id)
  return {
    revision: record.revision,
    core_team: record.core_team.filter((id) => id !== agentId),
    delegation: graph.edges.filter((edge) => edge.from_agent !== agentId && edge.to_agent !== agentId),
  }
}

/**
 * D4: take the agent off THIS workspace's team only — the same mutation the
 * Team tab saves (`WorkspaceTeamTab.saveFn`): revision + core_team minus
 * the agent + the delegation graph minus the agent's edges, in one PUT,
 * with Hide's single bounded refetch-and-retry on 409 (S4-07, shared
 * helper).
 *
 * S4-01: core_team is a FULL-REPLACEMENT field on the server, so the
 * removal body is never built from a record that lacks the list. When the
 * cached record has no core_team (it is optional on the wire and the
 * sidebar cache can be up to one 30 s poll stale), the fresh record is
 * fetched first; a record with no team even after that refetch refuses
 * visibly. A removal therefore only ever drops the ONE agent.
 */
export async function removeWorkspaceMember(workspace: Workspace, agentId: string): Promise<void> {
  const base = workspace.core_team === undefined ? await fetchWorkspace(workspace.id) : workspace
  try {
    const body = await removalBody(base, agentId)
    if (body === null) return // already removed elsewhere (S4-08) — nothing to send
    await updateWorkspace(workspace.id, body)
    return
  } catch (err) {
    if (!isConflict(err)) throw err
  }
  await retryOnceOnConflict(workspace.id, (fresh) => removalBody(fresh, agentId))
}

/** Refresh every cache the sidebar roster and its consumers read. */
export function invalidateWorkspaceRoster(): void {
  void queryClient.invalidateQueries({ queryKey: ['workspaces'] })
  void queryClient.invalidateQueries({ queryKey: ['agents'] })
}

/**
 * Row-action failures are never silent: an error toast names what failed,
 * and a structured diagnostic record persists past the toast's few seconds
 * (S4-03) through the app's existing diagnostic path (telemetry logError in
 * production builds — logDiagnostic's own gate decides the write-through).
 */
export function surfaceActionError(action: string, err: unknown): void {
  const detail = err instanceof Error && err.message ? err.message : 'Unknown error'
  logDiagnostic('agentRowActionFailed', { action, detail })
  useUiStore.getState().addToast({ variant: 'error', message: `${action}: ${detail}` })
}
