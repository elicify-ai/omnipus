import { createFileRoute, redirect } from '@tanstack/react-router'

// /workspaces (index) redirects to /tasks which in turn redirects to the Inbox
// workspace. This keeps the URL meaningful when the user navigates to /workspaces
// without a workspace ID — it is the same redirect logic as the /tasks route.
export const Route = createFileRoute('/_app/workspaces/')({
  beforeLoad: () => {
    throw redirect({ to: '/tasks', replace: true })
  },
})
