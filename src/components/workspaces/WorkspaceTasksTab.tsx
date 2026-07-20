import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus, Strategy } from '@phosphor-icons/react'
import { TagFilterBar } from './TagFilterBar'
import { AltitudeToggle } from './AltitudeToggle'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
import {
  fetchTasks,
  fetchPlans,
  fetchAgents,
  updateTask,
  approvePlan,
  stopPlan,
  deletePlan,
  isApiError,
  parsePlanApproveTaskErrors,
  tasksQueryKeys,
  plansQueryKeys,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Plan, Task } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

interface WorkspaceTasksTabProps {
  workspaceId: string
  /** 'board' = kanban by 7-state lifecycle, grouped into plan swimlanes; 'list' = flat filterable table. */
  mode: 'board' | 'list'
}

/**
 * Shared task-tab body for the Board and List views. Board mode groups tasks
 * into per-plan swimlane bands (Plan Swimlane redesign) via BoardView.tsx's
 * `PlanLaneHeader` rows; `TagFilterBar` provides the toolbar's tag filter.
 * Owns the create task/plan slide-overs; only the inner Board/List view
 * differs.
 */
export function WorkspaceTasksTab({ workspaceId, mode }: WorkspaceTasksTabProps) {
  const { activeTagFilter, setActiveTagFilter, boardAltitude, setBoardAltitude } = useWorkspacesStore()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [planSlideOver, setPlanSlideOver] = useState<{ open: boolean; plan: Plan | null }>({ open: false, plan: null })

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

  // Plan-lane header actions (⋯ menu): Approve (draft-only), Stop
  // (running/approved cap-waiting), Clear (delete). Edit reuses the same
  // CreatePlanSlideOver as "New plan", opened with `plan` set.
  const approvePlanMutation = useMutation({
    mutationFn: (planId: string) => approvePlan(planId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      addToast({ message: 'Plan approved', variant: 'success' })
    },
    onError: (err) => {
      // Mirror CreatePlanSlideOver's Edit-panel Approve: a 400 carries a
      // per-task "needs acceptance criteria" payload — surface those reasons
      // instead of the generic fallback (the lane ⋯ Approve is now the
      // primary path, so it must not regress to a less useful toast).
      if (isApiError(err) && err.status === 400) {
        const taskErrors = parsePlanApproveTaskErrors(err.body)
        if (taskErrors) {
          const lines = taskErrors.map((e) => `${e.title ?? e.task_id}: ${e.reason}`)
          addToast({
            message: `Cannot approve — the following tasks are missing criteria:\n${lines.join('\n')}`,
            variant: 'error',
          })
          return
        }
      }
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to approve plan'
      addToast({ message: msg, variant: 'error' })
    },
  })

  const stopPlanMutation = useMutation({
    mutationFn: (planId: string) => stopPlan(planId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      addToast({ message: 'Plan stopped', variant: 'success' })
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to stop plan'
      addToast({ message: msg, variant: 'error' })
    },
  })

  const clearPlanMutation = useMutation({
    mutationFn: (planId: string) => deletePlan(planId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      addToast({ message: 'Plan cleared', variant: 'success' })
    },
    onError: (err) => {
      // The backend rejects deleting a `running` plan (400) — surface that
      // real reason rather than a generic message (PlanLaneHeader already
      // disables Clear while running, but the state can flip mid-flight).
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to clear plan'
      addToast({ message: msg, variant: 'error' })
    },
  })

  const { data: plans = [], isError: plansError } = useQuery({
    queryKey: plansQueryKeys.list(workspaceId),
    queryFn: () => fetchPlans(workspaceId),
    // Polled (not WS-driven, out of this wave's scope) so a paused/blocked
    // owner-disabled state (FR-086) still surfaces without a page reload.
    refetchInterval: 15_000,
    staleTime: 10_000,
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
      {/* Toolbar: tag filter + new plan/task */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0 flex-wrap">
        <TagFilterBar tasks={tasks} activeTagFilter={activeTagFilter} onSelectTag={setActiveTagFilter} />

        <div className="flex items-center gap-2 ml-auto flex-shrink-0">
          {/* Altitude toggle (Top-level / Show all) — board-only view control,
              consolidated into this toolbar row instead of its own second row. */}
          {mode === 'board' && (
            <AltitudeToggle value={boardAltitude} onChange={setBoardAltitude} />
          )}
          <button tabIndex={0}
            type="button"
            onClick={() => setPlanSlideOver({ open: true, plan: null })}
            className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium text-[var(--color-secondary)] hover:border-[var(--color-accent)] hover:text-[var(--color-accent)] transition-colors flex-shrink-0"
          >
            <Strategy size={13} />
            New plan
          </button>
          <button tabIndex={0}
            type="button"
            onClick={() => setCreateTaskOpen(true)}
            className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90 transition-colors flex-shrink-0"
          >
            <Plus size={13} />
            New task
          </button>
        </div>
      </div>

      {agentsError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)] flex-shrink-0">
          <Info size={12} weight="fill" className="shrink-0" />
          Agent details failed to load — task avatars may be missing.
        </div>
      )}
      {plansError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)] flex-shrink-0">
          <Info size={12} weight="fill" className="shrink-0" />
          Plans failed to load — planned tasks are hidden from their plan lanes and shown under Loose tasks instead.
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
          plans={plans}
          agents={agents}
          workspaceId={workspaceId}
          activeTagFilter={activeTagFilter}
          altitude={boardAltitude}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          onNewTask={() => setCreateTaskOpen(true)}
          onTaskMove={(task, status) => moveMutation.mutate({ task, status })}
          onMoveRejected={(reason) => addToast({ message: reason, variant: 'error' })}
          onApprovePlan={(plan) => approvePlanMutation.mutate(plan.id)}
          onStopPlan={(plan) => stopPlanMutation.mutate(plan.id)}
          onEditPlan={(plan) => setPlanSlideOver({ open: true, plan })}
          onClearPlan={(plan) => clearPlanMutation.mutate(plan.id)}
          approvingPlanId={approvePlanMutation.isPending ? (approvePlanMutation.variables ?? null) : null}
          stoppingPlanId={stopPlanMutation.isPending ? (stopPlanMutation.variables ?? null) : null}
          clearingPlanId={clearPlanMutation.isPending ? (clearPlanMutation.variables ?? null) : null}
        />
      ) : (
        <ListView
          tasks={tasks}
          plans={plans}
          agents={agents}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
        />
      )}

      <TaskDetailSlideOver task={selectedTask} onClose={() => setSelectedTaskId(null)} />

      <CreateTaskSlideOver
        open={createTaskOpen}
        onOpenChange={setCreateTaskOpen}
        workspaceId={workspaceId}
        // Swimlane redesign: new tasks always land unplanned (the "Loose
        // tasks" band) — "Move to plan…" on the task detail panel is the
        // explicit, single reassignment path (no lane-scoped quick-create).
        planId={null}
      />

      <CreatePlanSlideOver
        open={planSlideOver.open}
        onOpenChange={(open) => setPlanSlideOver((s) => ({ ...s, open }))}
        workspaceId={workspaceId}
        plan={planSlideOver.plan}
      />
    </div>
  )
}

function BoardSkeleton() {
  return (
    <div className="flex gap-3 p-4 overflow-x-auto flex-1">
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
