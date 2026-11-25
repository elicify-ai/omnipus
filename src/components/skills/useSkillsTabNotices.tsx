// useSkillsTabNotices — the two warning callouts of the Skills tab:
//   - the unfinished-upgrade notice (C-19 "The notice", S-131), from
//     PermissionSettings.upgrade_unfinished, served by GET and every PATCH;
//   - the Full-size warning (C-14 "Page warning", S-130, D18), from the
//     figures GET /agents serves.
// GET /agents is read on mount, once more after each saved skill permission
// or level, and on window focus (C-14 Freshness). When it fails the tab shows
// no warning and no error: the rest of the tab is unaffected (D18 row 13).

import { useEffect, useRef, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents } from '@/lib/api'
import type { PermissionSaveQueue } from '@/lib/permissionSaveQueue'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { agentsOverHalf, fullSizeWarning, upgradeNotice, type AgentContextFigures } from './skillsToolsModel'

export function useSkillsTabNotices(queue: PermissionSaveQueue): ReactNode {
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, refetchOnWindowFocus: true })

  // One more read after each saved skill change (not on the first render).
  const seenSaves = useRef(queue.skillSaves)
  const { refetch } = agentsQuery
  useEffect(() => {
    if (queue.skillSaves === seenSaves.current) return
    seenSaves.current = queue.skillSaves
    void refetch()
  }, [queue.skillSaves, refetch])

  const agents = (agentsQuery.isSuccess ? agentsQuery.data : []) as readonly AgentContextFigures[]
  const overHalf = agentsOverHalf(agents)
  const unfinished = queue.stored?.upgrade_unfinished ?? []
  const nameOf = (agentId: string) => (agentsQuery.data ?? []).find((agent) => agent.id === agentId)?.name

  return (
    <>
      {unfinished.length > 0 && (
        <Callout variant="warning">
          <CalloutDescription>{upgradeNotice(unfinished, nameOf)}</CalloutDescription>
        </Callout>
      )}
      {overHalf.length > 0 && (
        <Callout variant="warning">
          <CalloutDescription>{fullSizeWarning(overHalf)}</CalloutDescription>
        </Callout>
      )}
    </>
  )
}
