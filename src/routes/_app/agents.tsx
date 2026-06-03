import { createFileRoute, Outlet } from '@tanstack/react-router'

function AgentsError() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-4">
      <p className="text-sm text-[var(--color-error)]">Failed to load agents.</p>
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="text-xs text-[var(--color-accent)] underline underline-offset-2"
      >
        Reload page
      </button>
    </div>
  )
}

function AgentsNotFound() {
  return (
    <div className="flex items-center justify-center h-full">
      <p className="text-sm text-[var(--color-muted)]">Agent not found.</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/agents')({
  component: () => <Outlet />,
  errorComponent: AgentsError,
  notFoundComponent: AgentsNotFound,
})

// Note: the agent list screen body now lives in
// @/components/screens/AgentListScreen and is lazy-loaded by agents.index.tsx.
// It is intentionally NOT re-exported here — an eager re-export would pull the
// screen back into this (eager) layout chunk and defeat the code split.
// Consumers (tests) import AgentListScreen from the screen module directly.
