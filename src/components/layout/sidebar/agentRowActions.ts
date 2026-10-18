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
 * A built-in (locked) member of the DEFAULT workspace is re-added at every
 * boot (ensureBuiltinRosterPresent), so a successful remove would be silently
 * undone on restart — the menu refuses it up front with that reason.
 * Built-ins on NON-default workspaces are removable; nothing re-adds them
 * there (ADR §Rules Q3).
 */
export function removeDisabledReason(agent: Agent | undefined, workspace: Workspace): string | null {
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
 *
 * A record carrying NO core_team at all is a genuinely EMPTY team, never a
 * missing list: every `record` reaching this function is a fresh
 * fetchWorkspace read (removeWorkspaceMember refetches when the cached record
 * has no team; retryOnceOnConflict refetches on 409), and the gateway omits
 * core_team on the wire ONLY when the stored team is empty
 * (rest_workspaces.go's toWire gates on len(CoreTeam) > 0). The roster can
 * still show a row for such an agent — workspaceRoster.ts::memberRoster falls
 * back to member_configs keys when core_team is absent — a stale config that
 * survived its member's removal. R2-S4-02 made the old refusal truthful but
 * left it a dead end (CR-04 = SFH-02): the same error every time, the row
 * forever. Remove therefore FINISHES the job through the existing contract —
 * `{ revision, member_configs: {} }`, an EMPTY merge map. On the server that
 * flips the member_configs-changed path (present-and-nil is the only "absent",
 * so `{}` is present) and runs its own GC — entries whose agent is off the
 * effective team are pruned (rest_workspaces.go applyUpdate, FR-022) — so the
 * stale config is cleared server-side and the row loses its fallback key.
 * core_team is deliberately NOT re-asserted: it is already empty, and a PUT
 * equal to the stored team sets no changed flag, so the write — and with it
 * the GC — would be skipped (persistAndRespond's no-op). Delegation is
 * untouched: no team membership means no edges are stripped here, and the
 * body carries no delegation field; the team path's retention rule stands —
 * a main chat is never released.
 */
async function removalBody(record: Workspace, agentId: string): Promise<WorkspaceUpdateRequest | null> {
  if (record.core_team === undefined) {
    return { revision: record.revision, member_configs: {} }
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
 * fetched first. A fresh record with no team even after that refetch is a
 * genuinely EMPTY team, not a missing list — there the removal completes by
 * clearing the stale member_configs entry through the server's own GC
 * (CR-04; see removalBody) instead of refusing.
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
