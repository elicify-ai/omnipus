import { createFileRoute, redirect } from '@tanstack/react-router'

// The bare /workspaces/$workspaceId path redirects to the Chat tab — Chat is
// the default landing view for a workspace (chat-first IA).
export const Route = createFileRoute('/_app/workspaces/$workspaceId/')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: params.workspaceId },
      replace: true,
    })
  },
})
