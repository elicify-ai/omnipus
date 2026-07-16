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
import type { Agent } from '@/lib/api'

export interface UseChatAgentsResult {
  /** Unfiltered agent list straight from the `['agents']` query — needed by callers that must distinguish "no agents at all" (hard error) from "no chat-eligible agents" (all-draft), e.g. AgentPicker's error/draft branches. */
  agents: Agent[]
  /** Ready-to-chat (active/idle), non-worker agents, scoped to the active workspace's core_team when one is set. */
  chatAgents: Agent[]
  isError: boolean
  refetch: () => void
}

export function useChatAgents(): UseChatAgentsResult {
  const { data: agents = [], isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Scope to the active workspace's core_team — same query AgentPicker
  // always ran (moved here verbatim).
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const { data: workspaces = [] } = useQuery({
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
  // below its own call site) — a plain re-filter on every render hands that
  // effect a brand-new array identity each time even when the underlying
  // agents/teamIds are unchanged, re-running the effect on every unrelated
  // render of either consumer.
  const chatAgents = useMemo(
    () =>
      agents
        .filter((a) => (a.status === 'active' || a.status === 'idle') && !isWorker(a))
        .filter((a) => !teamIds || teamIds.length === 0 || teamIds.includes(a.id)),
    [agents, teamIds],
  )

  return { agents, chatAgents, isError, refetch }
}
