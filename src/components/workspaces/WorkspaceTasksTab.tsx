import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus } from '@phosphor-icons/react'
import { MilestoneFilterPills, MILESTONE_FILTER_UNSCHEDULED } from './MilestoneFilterPills'
import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
import { CreateMilestoneSlideOver } from './CreateMilestoneSlideOver'
import {
  fetchTasks,
  fetchMilestones,
  fetchAgents,
  updateTask,
  isApiError,
  tasksQueryKeys,
  milestonesQueryKeys,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Task } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { BoardAltitude } from '@/store/workspacesStore'

interface WorkspaceTasksTabProps {
  workspaceId: string
  /** 'board' = kanban by 7-state lifecycle; 'list' = flat filterable table. */
  mode: 'board' | 'list'
}

/**
 * Shared task-tab body for the Board and List views. Both render the same
 * workspace-scoped task data with the milestone filter + create slide-overs;
 * only the inner view component differs. Board/List view internals are owned
 * by BoardView/ListView (F2 may extend them with delegation roll-ups etc.).
 */
export function WorkspaceTasksTab({ workspaceId, mode }: WorkspaceTasksTabProps) {
  const { activeMilestoneId, setActiveMilestoneId, boardAltitude, setBoardAltitude } = useWorkspacesStore()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [createMilestoneOpen, setCreateMilestoneOpen] = useState(false)

  // Kanban drag-to-column status change.
  const moveMutation = useMutation({
    mutationFn: ({ task, status }: { task: Task; status: Task['status'] }) =>
      updateTask(task.id, { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to move task'
      addToast({ message: msg, variant: 'error' })
    },
  })

  const { data: milestones = [], isError: milestonesError } = useQuery({
    queryKey: milestonesQueryKeys.list(workspaceId),
    queryFn: () => fetchMilestones(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

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

  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  const selectedTask =
    selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* Toolbar: milestone filter + new task */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
        <div className="flex-1 min-w-0">
          {milestones.length > 0 ? (
            <MilestoneFilterPills
              milestones={milestones}
              activeMilestoneId={activeMilestoneId}
              onSelect={setActiveMilestoneId}
              onNewMilestone={() => setCreateMilestoneOpen(true)}
            />
          ) : !milestonesError ? (
            <button tabIndex={0}
              type="button"
              onClick={() => setCreateMilestoneOpen(true)}
              className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
            >
              <Plus size={10} />
              Add milestone
            </button>
          ) : null}
        </div>

        <button tabIndex={0}
          type="button"
          onClick={() => setCreateTaskOpen(true)}
          className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90 transition-colors flex-shrink-0"
        >
          <Plus size={13} />
          New task
        </button>
      </div>

      {agentsError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)] flex-shrink-0">
          <Info size={12} weight="fill" className="shrink-0" />
          Agent details failed to load — task avatars may be missing.
        </div>
      )}

      {/* Content */}
      {tasksLoading ? (
        <BoardSkeleton />
      ) : tasksError && tasks.length === 0 ? (
        <div className="flex items-center justify-center flex-1 p-8 text-[var(--color-muted)] text-sm">
          Failed to load tasks. Check your connection and try again.
        </div>
      ) : mode === 'board' ? (
        <BoardView
          tasks={tasks}
          milestones={milestones}
          agents={agents}
          activeMilestoneId={activeMilestoneId}
          altitude={boardAltitude}
          onAltitudeChange={(next: BoardAltitude) => setBoardAltitude(next)}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          onNewTask={() => setCreateTaskOpen(true)}
          onTaskMove={(task, status) => moveMutation.mutate({ task, status })}
          onMoveRejected={(reason) => addToast({ message: reason, variant: 'error' })}
        />
      ) : (
        <ListView
          tasks={tasks}
          milestones={milestones}
          agents={agents}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
        />
      )}

      <TaskDetailSlideOver task={selectedTask} onClose={() => setSelectedTaskId(null)} />

      <CreateTaskSlideOver
        open={createTaskOpen}
        onOpenChange={setCreateTaskOpen}
        workspaceId={workspaceId}
        milestoneId={
          activeMilestoneId !== null && activeMilestoneId !== MILESTONE_FILTER_UNSCHEDULED
            ? activeMilestoneId
            : null
        }
      />

      <CreateMilestoneSlideOver
        open={createMilestoneOpen}
        onOpenChange={setCreateMilestoneOpen}
        workspaceId={workspaceId}
      />
    </div>
  )
}

function BoardSkeleton() {
  return (
    <div className="flex gap-3 p-4 overflow-x-auto overscroll-contain flex-1">
      {[1, 2, 3, 4, 5, 6, 7].map((i) => (
        <div
          key={i}
          className="flex flex-col min-w-[180px] flex-1 rounded-xl border border-[var(--color-border)] animate-pulse"
        >
          <div className="h-10 border-b border-[var(--color-border)] bg-[var(--color-surface-2)]" />
          <div className="flex flex-col gap-2 p-2">
            {[1, 2].map((j) => (
              <div key={j} className="h-14 rounded-lg bg-[var(--color-surface-2)]" />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
