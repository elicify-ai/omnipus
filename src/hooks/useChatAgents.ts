// useChatAgents — shared agent-list query + scoping used by BOTH the
// AgentPicker dropdown (composer/AgentPicker.tsx) and the `@` mention menu
// (useSlashMenu.ts's mention mode). Extracted so the two surfaces share ONE
// `['agents']` react-query cache entry and apply IDENTICAL scoping
// (ready-to-chat status, worker exclusion, active workspace's core_team)
// instead of two hand-rolled copies of the same filter drifting apart.
//
// Query keys are deliberately unchanged from AgentPicker's original inline
// version — `['agents']` and `workspacesQueryKeys.list({ status: 'active' })`
// — so React Query dedupes both callers into a single network request
// instead of issuing it twice per render tree.
//
// Deliberately does NOT own picker-only concerns: auto-select-first-agent,
// the agentSelectorOpen latch reset, and the error/all-draft branch UI all
// stay in AgentPicker, which is the sole side-effect writer to the session
// store on mount — a second writer here would race it.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
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

export interface UseChatAgentsResult {
  /** Unfiltered agent list straight from the `['agents']` query — needed by callers that must distinguish "no agents at all" (hard error) from "no chat-eligible agents" (all-draft), e.g. AgentPicker's error/draft branches. */
  agents: Agent[]
  /** Ready-to-chat (active/idle), non-worker agents, scoped to the active workspace's core_team when one is set. */
  chatAgents: Agent[]
  isError: boolean
  refetch: () => void
}

export function useChatAgents(): UseChatAgentsResult {
  const { data: agents = EMPTY_AGENTS, isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Scope to the active workspace's core_team — same query AgentPicker
  // always ran (moved here verbatim).
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const { data: workspaces = EMPTY_WORKSPACES } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    enabled: !!activeWorkspaceId,
  })
  const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId)
  const teamIds = activeWorkspace?.core_team

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
        .filter((a) => (a.status === 'active' || a.status === 'idle') && !isWorker(a) && a.type !== 'system')
        .filter((a) => !teamIds || teamIds.length === 0 || teamIds.includes(a.id)),
    [agents, teamIds],
  )

  return { agents, chatAgents, isError, refetch }
}
