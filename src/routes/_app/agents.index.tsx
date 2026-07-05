import { createFileRoute } from '@tanstack/react-router'
import { AgentListScreen } from '@/components/screens/AgentListScreen'

// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
// AgentListScreen is intentionally NOT re-exported from agents.tsx — an eager
// re-export would pull the screen back into the layout chunk and defeat the
// split.
export const Route = createFileRoute('/_app/agents/')({
  component: AgentListScreen,
})
