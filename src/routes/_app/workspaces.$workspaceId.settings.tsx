import { createFileRoute } from '@tanstack/react-router'
import { useActiveWorkspace } from '@/components/workspaces/WorkspaceTabContainer'
import { WorkspaceSettingsTab } from '@/components/workspaces/WorkspaceSettingsTab'

// Settings tab — workspace properties (name/description/owner/
// archive/team) with the S1 auto-save pattern.
function WorkspaceSettingsRoute() {
  const workspace = useActiveWorkspace()
  return <WorkspaceSettingsTab workspace={workspace} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/settings')({
  component: WorkspaceSettingsRoute,
})
