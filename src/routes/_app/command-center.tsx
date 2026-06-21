import { createFileRoute } from '@tanstack/react-router'
import { DefaultWorkspaceRedirect } from '@/components/workspaces/DefaultWorkspaceRedirect'

// The Command Center has been folded into the workspace Board tab. Redirect old
// bookmarks/links to the default workspace's Board.
export const Route = createFileRoute('/_app/command-center')({
  component: () => <DefaultWorkspaceRedirect tab="board" />,
})
