import { useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspaceDelegation, workspacesQueryKeys } from '@/lib/api'
import { logError } from '@/lib/telemetry'

/**
 * useWorkspaceTeamIds — resolves the workspace-team-scoping set consumed by
 * `buildTaskAssigneeItems` (Fix B: the task assignee picker must be scoped to
 * the task's workspace team, not the global agent roster).
 *
 * The team set is core_team ∪ every delegation edge's from/to agent id —
 * EXACTLY the set the backend enforces at `validateTaskAgentID`
 * (`pkg/gateway/rest_tasks.go`) via `workspaceTeamSet`
 * (`pkg/gateway/rest_workspace_delegation.go`), which is itself a thin
 * wrapper over the canonical `workspace.TeamSet` (`pkg/workspace/delegation.go`).
 * Rather than re-deriving that union client-side from a separately-fetched
 * Workspace record, this reuses the existing per-workspace delegation-graph
 * endpoint (`GET /workspaces/{id}/delegation`, already consumed by the Team
 * tab — see `WorkspaceTeamTab.tsx`) whose `team[]` field is computed
 * server-side with that identical union (`workspaceDelegationToWire`) — one
 * fewer place the two derivations could drift apart.
 *
 * Returns:
 *   - `teamIds`: `Set<string> | undefined`.
 *       - `undefined` while the query has not yet resolved (`isLoading`) OR
 *         when it errors (`isError`) — callers pass this straight through to
 *         `buildTaskAssigneeItems` as an explicit `{ kind: 'unscoped' }`
 *         `AssigneeTeamScope` (an intentional degrade: on load, paired with
 *         a disabled/loading control at the call site so nothing is
 *         selectable yet; on error, the picker falls back to the full
 *         unscoped agent list — same behaviour as before this scoping was
 *         added — rather than presenting an empty, unusable picker. The
 *         backend still enforces team membership server-side either way).
 *       - a `Set` (possibly empty, e.g. a workspace with no core_team and no
 *         delegation edges) once the delegation graph has loaded
 *         successfully.
 *   - `isLoading`: true only while genuinely fetching for a real workspace id
 *     (never true when `workspaceId` is falsy — there is nothing to load).
 *   - `isError`: true when the fetch failed. This is not a silent state: the
 *     hook itself logs a rate-limited `workspaceTeamIdsFetchFailed`
 *     telemetry event once per error transition (`logError`, see
 *     src/lib/telemetry.ts), and both call sites (`CreateTaskSlideOver`,
 *     `TaskDetailPanel`) read `isError` to render a muted inline hint next
 *     to the assignee picker ("Team list unavailable — showing all
 *     agents") — so the unscoped-fallback degrade is visible to the user
 *     and durably logged for an operator, not indistinguishable from a
 *     healthy, unrestricted workspace.
 */
export interface UseWorkspaceTeamIdsResult {
  teamIds: Set<string> | undefined
  isLoading: boolean
  isError: boolean
}

export function useWorkspaceTeamIds(workspaceId: string | null | undefined): UseWorkspaceTeamIdsResult {
  const enabled = !!workspaceId
  const { data, isLoading, isError } = useQuery({
    queryKey: workspacesQueryKeys.delegation(workspaceId ?? ''),
    queryFn: () => fetchWorkspaceDelegation(workspaceId as string),
    enabled,
    staleTime: 15_000,
  })

  const scopedIsError = enabled && isError

  // Log once per error transition (mirrors ConnectorsScreen's
  // workspacesFetchFailed/agentsFetchFailed `useEffect` pattern) — without
  // this, a failed fetch silently rendered the same unscoped roster as a
  // healthy, team-unrestricted workspace, with no durable signal anywhere.
  useEffect(() => {
    if (scopedIsError) {
      logError({ event: 'workspaceTeamIdsFetchFailed', workspaceId: workspaceId ?? null })
    }
  }, [scopedIsError, workspaceId])

  const teamIds = useMemo(() => {
    if (!enabled || isError || !data) return undefined
    return new Set(data.team ?? [])
  }, [enabled, isError, data])

  return {
    teamIds,
    isLoading: enabled && isLoading,
    isError: scopedIsError,
  }
}
