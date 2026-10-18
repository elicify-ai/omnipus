// useChatAgents — shared agent-list query + scoping. The composer agent
// picker is gone (FR-007); the `@` mention menu is BACK (founder decision
// 2026-10-11 Q1 B) and consumes this hook again, exactly as it did before the
// cutover. Production caller: the mention menu (src/hooks/useSlashMenu.ts). The query and the scoping stay in one
// definition: ONE `['agents']` react-query cache entry and IDENTICAL scoping
// (ready-to-chat status, worker exclusion, active workspace's core_team)
// instead of two hand-rolled copies of the same filter drifting apart.
//
// Query keys are deliberately unchanged from AgentPicker's original inline
// version — `['agents']` and `workspacesQueryKeys.list({ status: 'active' })`
// — so React Query dedupes both callers into a single network request
// instead of issuing it twice per render tree.
//
// Does not write the session store. The old picker auto-selected the first
// ready agent on mount; that effect is gone with the picker (the store lane
// owns what a blank composer does next). This hook still refreshes the team
// list when agentSelectorOpen flips on, which /agents still sets.

import { useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents, fetchWorkspaces, isWorker, workspacesQueryKeys } from '@/lib/api'
import type { Agent, Workspace } from '@/lib/api'

// Item I (bugfixes3 sign-off): module-level (not per-render) empty-array
// defaults. `data: agents = []` would otherwise construct a BRAND NEW `[]`
// on every render during the loading window (before the query resolves) —
// a fresh array literal every time, defeating the point of the useMemo
// below for exactly the render count that matters most (initial mount,
// before either query has data). Hoisting the empty defaults to module
// scope means every loading-window render reads the SAME `[]` reference,
// so `chatAgents` genuinely memoizes across those renders too, not just
// once both queries have resolved.
const EMPTY_AGENTS: Agent[] = []
const EMPTY_WORKSPACES: Workspace[] = []

// Moved to src/lib/agentKind.ts (2026-10-11) so the lib-level @-recipient
// derivation can reuse the ONE predicate without a hooks-module import;
// re-exported here so this module's existing callers are unchanged.
import { isChatEligibleAgent } from '@/lib/agentKind'
export { isChatEligibleAgent }

export interface UseChatAgentsResult {
  /** Unfiltered agent list straight from the `['agents']` query — needed by callers that must distinguish "no agents at all" (hard error) from "no chat-eligible agents" (all-draft), e.g. AgentPicker's error/draft branches. */
  agents: Agent[]
  /** Ready-to-chat (active/idle), non-worker agents, scoped to the active workspace's core_team when one is set. */
  chatAgents: Agent[]
  /** True when the latest fetch of the active workspace's team list failed. The last successfully fetched team stays in `workspaces` (React Query retains previous data on a failed refetch), so `chatAgents` may be scoped by a team that failed to refresh — surfaces of the open picker show a "could not be refreshed" line instead of letting that list read as current. */
  teamRefreshFailed: boolean
  isError: boolean
  refetch: () => void
}

export function useChatAgents(): UseChatAgentsResult {
  const { data: agents = EMPTY_AGENTS, dataUpdatedAt: agentsUpdatedAt, isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Scope to the active workspace's core_team — same query AgentPicker
  // always ran (moved here verbatim).
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const pickerOpen = useUiStore((s) => s.agentSelectorOpen)
  // The workspaces query's error state is part of this hook's contract: the
  // picker-open effect below refetches the team list, and on failure React
  // Query KEEPS the previous `data` — so `workspaces` alone makes a stale team
  // indistinguishable from a fresh one. `teamRefreshFailed` is the signal
  // AgentPicker renders so the last known team never reads as current.
  const { data: workspaces = EMPTY_WORKSPACES, isError: teamRefreshFailed } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    enabled: !!activeWorkspaceId,
  })
  const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId)
  const teamIds = activeWorkspace?.core_team

  // Focus recovery and agent_created both refresh ['agents'], but team
  // membership can change separately. Refresh the workspace query too, even
  // when structural sharing preserves the same agents array after a refetch.
  // That refresh is too early for a membership PUT that lands AFTER
  // agent_created: the team list is read again when the picker opens.
  useEffect(() => {
    if (activeWorkspaceId && agentsUpdatedAt > 0) {
      void queryClient.invalidateQueries({
        queryKey: workspacesQueryKeys.list({ status: 'active' }),
      })
    }
  }, [activeWorkspaceId, agentsUpdatedAt])

  useEffect(() => {
    if (!activeWorkspaceId || !pickerOpen) return
    // Creation can finish after the initial agents fetch. Re-read discovery
    // as well as membership instead of relying on a delivered WS frame.
    void queryClient.invalidateQueries({ queryKey: ['agents'] })
    void queryClient.invalidateQueries({
      queryKey: workspacesQueryKeys.list({ status: 'active' }),
    })
  }, [activeWorkspaceId, pickerOpen])

  // Fix 10: memoize the filter chain. This hook is shared (AgentPicker AND
  // the "@" mention menu both call it), and consumers put `chatAgents` in
  // effect dependency lists (AgentPicker's auto-select-first-agent effect
  // below its own call site) — a plain re-filter on every render would hand
  // that effect a brand-new array identity each time even when the
  // underlying agents/teamIds are unchanged, re-running the effect on every
  // unrelated render of either consumer. Item I correction (bugfixes3
  // sign-off): that guarantee only holds as long as this memo's OWN inputs
  // are themselves stable. `agents`/`workspaces` come from react-query's
  // `data`, which is `undefined` until each query resolves — without the
  // EMPTY_AGENTS/EMPTY_WORKSPACES module-level constants above (as opposed
  // to an inline `= []` default), `agents`/`workspaces` would get a fresh
  // identity every render during that loading window, and this useMemo
  // would recompute right along with them despite looking stable once data
  // has actually arrived.
  const chatAgents = useMemo(
    () =>
      agents
        // ADR-049 D3: type:system (the locked Judge) is never a chat target —
        // it must not appear in the AgentPicker dropdown or the "@" mention
        // menu, both of which consume this hook.
        .filter((a) =>
          (a.status === 'active' || a.status === 'idle')
          && !isWorker(a)
          && isChatEligibleAgent(a),
        )
        .filter((a) =>
          !teamIds
          || teamIds.length === 0
          || teamIds.includes(a.id),
        ),
    [agents, teamIds],
  )

  return { agents, chatAgents, teamRefreshFailed, isError, refetch }
}
