import { createFileRoute, redirect } from '@tanstack/react-router'

// /projects/:projectId redirects to /workspaces/:workspaceId for backward compat.
// The entity was renamed from Project to Workspace (Spec-1).
export const Route = createFileRoute('/_app/projects/$projectId')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/workspaces/$workspaceId',
      params: { workspaceId: params.projectId },
      replace: true,
    })
  },
})
