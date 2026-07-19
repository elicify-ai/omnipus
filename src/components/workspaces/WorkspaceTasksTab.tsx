import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus, CaretDown, CaretRight } from '@phosphor-icons/react'
import { PlanFilterBar } from './PlanFilterBar'
import { PlanCard } from './PlanCard'
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
  stopPlan,
  isApiError,
  tasksQueryKeys,
  plansQueryKeys,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Plan, Task } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { BoardAltitude } from '@/store/workspacesStore'
import { PLAN_FILTER_ALL, PLAN_FILTER_UNTAGGED } from '@/lib/planFilter'

interface WorkspaceTasksTabProps {
  workspaceId: string
  /** 'board' = kanban by 7-state lifecycle; 'list' = flat filterable table. */
  mode: 'board' | 'list'
}

/**
 * Shared task-tab body for the Board and List views. Owns the Plan filter +
 * Plans panel (ADR-049 SD-C1 — a collapsible section within this tab, not a
 * new route) alongside the create task/plan slide-overs; only the inner
 * Board/List view differs.
 */
export function WorkspaceTasksTab({ workspaceId, mode }: WorkspaceTasksTabProps) {
  const { activePlanId, setActivePlanId, activeTagFilter, setActiveTagFilter, boardAltitude, setBoardAltitude } = useWorkspacesStore()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [planSlideOver, setPlanSlideOver] = useState<{ open: boolean; plan: Plan | null }>({ open: false, plan: null })
  const [plansExpanded, setPlansExpanded] = useState(true)

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
      {/* Toolbar: plan/tag filter + new task */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
        <PlanFilterBar
          plans={plans}
          tasks={tasks}
          activePlanId={activePlanId}
          activeTagFilter={activeTagFilter}
          onSelectPlan={setActivePlanId}
          onSelectTag={setActiveTagFilter}
          onNewPlan={() => setPlanSlideOver({ open: true, plan: null })}
        />

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
      {plansError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)] flex-shrink-0">
          <Info size={12} weight="fill" className="shrink-0" />
          Plans failed to load — the Plan filter may be incomplete.
        </div>
      )}

      {/* Plans panel (SD-C1) — collapsible section within this tab. */}
      {plans.length > 0 && (
        <div className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
          <button tabIndex={0}
            type="button"
            onClick={() => setPlansExpanded((v) => !v)}
            aria-expanded={plansExpanded}
            className="flex items-center gap-1.5 px-4 py-1.5 text-xs font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
          >
            {plansExpanded ? <CaretDown size={11} /> : <CaretRight size={11} />}
            Plans ({plans.length})
          </button>
          {plansExpanded && (
            <div className="flex gap-2 px-4 pb-3 overflow-x-auto">
              {plans.map((plan) => (
                <div key={plan.id} className="min-w-[240px] max-w-[280px]">
                  <PlanCard
                    plan={plan}
                    agents={agents}
                    onEdit={() => setPlanSlideOver({ open: true, plan })}
                    onApprove={() => setPlanSlideOver({ open: true, plan })}
                    onStop={() => stopPlanMutation.mutate(plan.id)}
                    isStopping={stopPlanMutation.isPending && stopPlanMutation.variables === plan.id}
                  />
                </div>
              ))}
            </div>
          )}
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
          activePlanId={activePlanId}
          activeTagFilter={activeTagFilter}
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
        planId={
          activePlanId !== PLAN_FILTER_ALL && activePlanId !== PLAN_FILTER_UNTAGGED
            ? activePlanId
            : null
        }
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
