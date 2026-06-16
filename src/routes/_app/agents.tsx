import { useEffect } from 'react'
import { createFileRoute, Outlet } from '@tanstack/react-router'
import { AgentProfile } from '@/components/agents/AgentProfile'
import { useUiStore } from '@/store/ui'

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

function AgentsLayout() {
  // Close the agent edit slide-over when the user leaves the /agents/* subtree.
  // Without this, `editAgentId` survives the layout unmount, and re-entering
  // /agents re-opens the sheet for whatever agent the user last looked at — a
  // phantom-open that violates the "I closed it" mental model. The cleanup
  // fires on EVERY layout unmount, so this also covers the deep-link route
  // (which navigates to /agents on mount, not away from /agents).
  useEffect(() => () => useUiStore.getState().closeEditAgentSlideOver(), [])
  return (
    <>
      <Outlet />
      {/* Mounted once at the layout level so the edit slide-over is reachable
          from every sub-route. The component reads `editAgentId` from the store. */}
      <AgentProfile />
    </>
  )
}

export const Route = createFileRoute('/_app/agents')({
  component: AgentsLayout,
  errorComponent: AgentsError,
  notFoundComponent: AgentsNotFound,
})

// Note: the agent list screen body now lives in
// @/components/screens/AgentListScreen and is lazy-loaded by agents.index.tsx.
// It is intentionally NOT re-exported here — an eager re-export would pull the
// screen back into this (eager) layout chunk and defeat the code split.
// Consumers (tests) import AgentListScreen from the screen module directly.
