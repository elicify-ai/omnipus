import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'

// Deep-link → open the slide-over and replace history to /agents so the back
// button doesn't surface this transient URL.
function AgentProfileRoute() {
  const { agentId } = Route.useParams()
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)
  const navigate = useNavigate()

  useEffect(() => {
    openEditAgentSlideOver(agentId)
    navigate({ to: '/agents', replace: true })
  }, [agentId, openEditAgentSlideOver, navigate])

  return null
}

export const Route = createFileRoute('/_app/agents/$agentId')({
  component: AgentProfileRoute,
})
