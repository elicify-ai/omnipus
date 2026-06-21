import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'

// Reserved type-name segments that are NOT valid agent IDs.
// MIN-10: a malformed URL like //#/agents/worker arrives when a legacy link
// or external notification uses the agent *type* name as the route param
// instead of the agent ID. Navigating to this route with agentId='worker'
// would call openEditAgentSlideOver('worker'), which sets editAgentId to
// the type name — then AgentProfile fetches GET /agents/worker and gets
// a 404, surfacing a confusing error modal. Detect this case early and
// redirect cleanly to /agents without opening any slide-over.
const RESERVED_AGENT_SEGMENTS = new Set(['worker', 'trust', 'index'])

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
