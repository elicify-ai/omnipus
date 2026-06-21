import { createFileRoute } from '@tanstack/react-router'
import { DefaultWorkspaceRedirect } from '@/components/workspaces/DefaultWorkspaceRedirect'

// Automations is removed from the product (IA reframe): scheduled/recurring
// work now lives in the workspace Calendar tab and per-agent heartbeats.
// The route is retained only as a redirect so old links don't 404 — it lands
// on the default workspace's Calendar.
export const Route = createFileRoute('/_app/automations')({
  component: () => <DefaultWorkspaceRedirect tab="calendar" />,
})
