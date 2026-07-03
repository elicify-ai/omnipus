import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'

// Reserved segments that are NOT agent IDs (sibling route names).
// MIN-10 originally also reserved 'worker' to catch legacy links that used
// the agent TYPE as the route param — but the seeded built-in roster ships
// an agent whose ID is literally 'worker', so that guard silently swallowed
// clicks on a REAL agent card (bug, 2026-07-03: the built-in worker's
// view/edit slide-over never opened). 'worker' is a valid agent ID; if a
// stale link ever points at a nonexistent agent, AgentProfile's own
// fetch-error handling covers it. Keep only true sibling-route collisions.
const RESERVED_AGENT_SEGMENTS = new Set(['trust', 'index'])

// Deep-link → open the slide-over and replace history to /agents so the back
// button doesn't surface this transient URL.
function AgentProfileRoute() {
  const { agentId } = Route.useParams()
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)
  const navigate = useNavigate()

  useEffect(() => {
    // MIN-10: guard against reserved type-name segments being used as agent IDs.
    // If agentId is a reserved segment (e.g. "worker"), skip the slide-over and
    // just navigate to the agents list cleanly.
    if (RESERVED_AGENT_SEGMENTS.has(agentId.toLowerCase())) {
      navigate({ to: '/agents', replace: true })
      return
    }
    openEditAgentSlideOver(agentId)
    navigate({ to: '/agents', replace: true })
  }, [agentId, openEditAgentSlideOver, navigate])

  return null
}

export const Route = createFileRoute('/_app/agents/$agentId')({
  component: AgentProfileRoute,
})
