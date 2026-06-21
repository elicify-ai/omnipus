import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { GraphView } from './graph/GraphView'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import { fetchTasks, fetchAgents, tasksQueryKeys } from '@/lib/api'

// ── F2 COMPONENT BOUNDARY ────────────────────────────────────────────────────
// The Graph (Task DAG) tab. Tasks as nodes, `blocked_by` as dependency edges,
// laid out left→right with live per-node status colour, pan/zoom, minimap.
// Export name + the WorkspaceTasksTab data-fetching pattern are preserved.
// The DAG canvas itself lives in ./graph/GraphView (presentational, testable).
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceGraphTabProps {
  workspaceId: string
}

/**
 * The Graph tab body: fetches the workspace's user-surface tasks + the agents
 * cache (for avatar colour/icon), renders the DAG canvas, and opens the shared
 * task detail slide-over on node click — mirroring the Board's onTaskClick.
 */
export function WorkspaceGraphTab({ workspaceId }: WorkspaceGraphTabProps) {
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)

  const {
    data: tasks = [],
    isLoading: tasksLoading,
    isError: tasksError,
  } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId, surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId, surface: 'user' }),
    refetchInterval: 15_000,
    staleTime: 10_000,
    enabled: !!workspaceId,
  })

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  const selectedTask =
    selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  if (tasksLoading) {
    return <GraphSkeleton />
  }

  if (tasksError && tasks.length === 0) {
    return (
      <div className="absolute inset-0 flex items-center justify-center p-8 text-sm text-[var(--color-muted)]">
        Failed to load the task graph. Check your connection and try again.
      </div>
    )
  }

  return (
    <div className="absolute inset-0 overflow-hidden">
      <GraphView
        tasks={tasks}
        agents={agents}
        selectedTaskId={selectedTaskId}
        onTaskClick={(task) => setSelectedTaskId(task.id)}
      />

      <TaskDetailSlideOver task={selectedTask} onClose={() => setSelectedTaskId(null)} />
    </div>
  )
}

/** Dark shimmer placeholder while the first task fetch resolves. */
function GraphSkeleton() {
  return (
    <div className="absolute inset-0 bg-[var(--color-surface-0)] p-8">
      <div className="flex h-full items-center gap-12">
        {[0, 1, 2].map((rank) => (
          <div key={rank} className="flex flex-1 flex-col gap-6">
            {[0, 1].map((row) => (
              <div
                key={row}
                className="h-24 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
              />
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
