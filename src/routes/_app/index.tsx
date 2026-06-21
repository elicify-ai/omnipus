import { createFileRoute } from '@tanstack/react-router'
import { DefaultWorkspaceRedirect } from '@/components/workspaces/DefaultWorkspaceRedirect'

// The global "/" front door now redirects into the default workspace's Chat
// tab — you are always inside a workspace (IA reframe). Chat is no longer a
// global route; it is the workspace's front view.
export const Route = createFileRoute('/_app/')({
  component: () => <DefaultWorkspaceRedirect tab="chat" />,
})
