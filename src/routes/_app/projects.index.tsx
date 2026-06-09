import { createFileRoute, redirect } from '@tanstack/react-router'

// /projects (index) redirects to /tasks which in turn redirects to the Inbox
// project. This keeps the URL meaningful when the user navigates to /projects
// without a project ID — it is the same redirect logic as the /tasks route.
export const Route = createFileRoute('/_app/projects/')({
  beforeLoad: () => {
    throw redirect({ to: '/tasks', replace: true })
  },
})
