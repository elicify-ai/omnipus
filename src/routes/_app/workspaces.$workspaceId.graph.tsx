import { createFileRoute, redirect } from '@tanstack/react-router'

// ADR-051 D1 — Graph is no longer a top-level tab: it's one of the view
// switcher options on the combined "Tasks" screen (the `board` route
// segment). This route survives only so old deep links to /graph don't
// 404 — it redirects straight to the Tasks screen (the Graph view can be
// picked back up from its own in-screen switcher).
export const Route = createFileRoute('/_app/workspaces/$workspaceId/graph')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/workspaces/$workspaceId/board',
      params: { workspaceId: params.workspaceId },
      replace: true,
    })
  },
})
