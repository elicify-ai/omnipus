import { createFileRoute } from '@tanstack/react-router'
import { DefaultWorkspaceRedirect } from '@/components/workspaces/DefaultWorkspaceRedirect'

// /tasks is folded into the workspace Board tab — tasks now live inside a
// workspace. Redirect to the default workspace's Board so old links resolve.
export const Route = createFileRoute('/_app/tasks')({
  component: () => <DefaultWorkspaceRedirect tab="board" />,
})
