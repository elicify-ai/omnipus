import { createFileRoute, redirect } from '@tanstack/react-router'

// The Command Center screen has been superseded by the Tasks screen.
// Redirect all visitors to /tasks so existing bookmarks and links keep working.
export const Route = createFileRoute('/_app/command-center')({
  beforeLoad: () => {
    throw redirect({ to: '/tasks', replace: true })
  },
  component: () => null,
})
