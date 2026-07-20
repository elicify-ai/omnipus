import { useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus, SquaresFour, ListBullets, Graph as GraphIcon } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TagFilterBar } from './TagFilterBar'
import { AltitudeToggle } from './AltitudeToggle'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { PlansFilterBand } from './PlansFilterBand'
import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { WorkspaceGraphTab } from './WorkspaceGraphTab'
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
import { filterByTag } from '@/lib/planFilter'
import { filterTasks } from '@/lib/taskFilters'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'

interface WorkspaceTasksTabProps {
  workspaceId: string
}

type TasksView = 'board' | 'list' | 'graph'

const OWNER_ALL = '__all__'

/**
 * The combined "Tasks" screen (ADR-051 D1/D2/D4/D6) — Board, List, and Graph
 * collapse into ONE screen behind a segmented view switcher, over a single
 * workspace-wide, tasks-only board. A plans-as-filter band sits above it:
 * clicking a plan tile filters the task set below to that plan (it does not
 * navigate/drill — the board stays one flat kanban). An Owner filter ANDs
 * with the plan filter. Both combine with the existing tag filter to produce
 * the one filtered task set every view (Board/List) renders; Graph reads the
 * shared `activePlanId` store field directly (see WorkspaceGraphTab) so it
 * stays in scope-sync with the band without needing its own copy of the
 * filtered list.
 */
export function WorkspaceTasksTab({ workspaceId }: WorkspaceTasksTabProps) {
  const { activeTagFilter, setActiveTagFilter, boardAltitude, setBoardAltitude, activePlanId, setActivePlanId } =
    useWorkspacesStore()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [view, setView] = useState<TasksView>('board')
  const [ownerAgentId, setOwnerAgentId] = useState<string | null>(null)
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

  // Plan-tile actions (PlansFilterBand's edit pencil + ⋯ menu): Approve
  // (draft-only), Stop (running/approved cap-waiting), Clear (delete). Edit
  // reuses the same CreatePlanSlideOver as "New plan", opened with `plan` set.
  const approvePlanMutation = useMutation({
    mutationFn: (planId: string) => approvePlan(planId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      addToast({ message: 'Plan approved', variant: 'success' })
    },
    onError: (err) => {
      // A 400 carries a per-task "needs acceptance criteria" payload —
      // surface those reasons instead of the generic fallback.
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
      // real reason rather than a generic message.
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

  // Plan + owner + tag filters AND together (ADR-051 D2/D6) — the one
  // filtered task set every view below renders (Graph excepted; it scopes
  // itself off the shared activePlanId store field, see the header comment).
  const filteredTasks = useMemo(() => {
    const tagFiltered = filterByTag(tasks, activeTagFilter)
    return filterTasks(tagFiltered, { planId: activePlanId, ownerAgentId })
  }, [tasks, activeTagFilter, activePlanId, ownerAgentId])

  const selectedPlan = useMemo(
    () => (activePlanId != null ? (plans.find((p) => p.id === activePlanId) ?? null) : null),
    [activePlanId, plans],
  )
  const ownerAgent = useMemo(
    () => (ownerAgentId != null ? (agents.find((a) => a.id === ownerAgentId) ?? null) : null),
    [ownerAgentId, agents],
  )

  const selectedTask =
    selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  const heading = selectedPlan ? `${selectedPlan.title} — tasks` : 'Tasks'

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* Plans-as-filter band (ADR-051 D2/D3) — overview + single-select
          filter, not navigation. Selecting a tile scopes the board below;
          it never drills into a separate screen. */}
      <PlansFilterBand
        plans={plans}
        tasks={tasks}
        agents={agents}
        selectedPlanId={activePlanId}
        onSelectPlan={setActivePlanId}
        onNewPlan={() => setPlanSlideOver({ open: true, plan: null })}
        onEditPlan={(plan) => setPlanSlideOver({ open: true, plan })}
        onApprovePlan={(plan) => approvePlanMutation.mutate(plan.id)}
        onStopPlan={(plan) => stopPlanMutation.mutate(plan.id)}
        onClearPlan={(plan) => clearPlanMutation.mutate(plan.id)}
        approvingPlanId={approvePlanMutation.isPending ? (approvePlanMutation.variables ?? null) : null}
        stoppingPlanId={stopPlanMutation.isPending ? (stopPlanMutation.variables ?? null) : null}
        clearingPlanId={clearPlanMutation.isPending ? (clearPlanMutation.variables ?? null) : null}
      />

      {/* Toolbar: view switcher + tag filter + owner filter + new task */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0 flex-wrap">
        <ViewSwitcher value={view} onChange={setView} />
        <TagFilterBar tasks={tasks} activeTagFilter={activeTagFilter} onSelectTag={setActiveTagFilter} />

        <div className="flex items-center gap-2 ml-auto flex-shrink-0">
          {/* Altitude toggle (Top-level / Show all) — board-only view control. */}
          {view === 'board' && (
            <AltitudeToggle value={boardAltitude} onChange={setBoardAltitude} />
          )}
          <OwnerFilterDropdown agents={agents} value={ownerAgentId} onChange={setOwnerAgentId} />
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

      {/* Dynamic heading (ADR-051 D2 — Visibility of System Status) */}
      <div
        className="flex items-center gap-1 px-4 py-1.5 border-b border-[var(--color-border)]/50 flex-shrink-0"
        data-testid="tasks-heading"
      >
        <h2 className="text-sm font-headline font-semibold text-[var(--color-secondary)]">
          {heading}
        </h2>
        {ownerAgent && (
          <span className="text-xs text-[var(--color-muted)]">
            {' '}· Owner: {ownerAgent.name}
          </span>
        )}
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
          Plans failed to load — the plans filter band may be incomplete.
        </div>
      )}

      {/* Content */}
      <div className="relative flex-1 min-h-0 flex flex-col overflow-hidden">
        {view === 'graph' ? (
          // The Graph tab manages its own loading/error state and reads the
          // shared activePlanId store field directly, so it bypasses the
          // tasksLoading/tasksError gate below (which only guards Board/List,
          // both fed from THIS screen's own filtered task query).
          <WorkspaceGraphTab workspaceId={workspaceId} />
        ) : tasksLoading ? (
          <BoardSkeleton />
        ) : tasksError && tasks.length === 0 ? (
          <div className="flex items-center justify-center flex-1 p-8 text-[var(--color-muted)] text-sm">
            Failed to load tasks. Check your connection and try again.
          </div>
        ) : view === 'board' ? (
          <BoardView
            tasks={filteredTasks}
            plans={plans}
            agents={agents}
            altitude={boardAltitude}
            onTaskClick={(task) => setSelectedTaskId(task.id)}
            onTaskMove={(task, status) => moveMutation.mutate({ task, status })}
            onMoveRejected={(reason) => addToast({ message: reason, variant: 'error' })}
          />
        ) : (
          <ListView
            tasks={filteredTasks}
            plans={plans}
            agents={agents}
            onTaskClick={(task) => setSelectedTaskId(task.id)}
          />
        )}
      </div>

      <TaskDetailSlideOver task={selectedTask} onClose={() => setSelectedTaskId(null)} />

      <CreateTaskSlideOver
        open={createTaskOpen}
        onOpenChange={setCreateTaskOpen}
        workspaceId={workspaceId}
        // New tasks always land unplanned (the "no plan" bucket) — "Move to
        // plan…" on the task detail panel is the explicit, single
        // reassignment path (no filter-scoped quick-create).
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

// ── View switcher (Board / List / Graph segmented control) ─────────────────

const VIEW_OPTIONS: { value: TasksView; label: string; Icon: Icon }[] = [
  { value: 'board', label: 'Board', Icon: SquaresFour },
  { value: 'list', label: 'List', Icon: ListBullets },
  { value: 'graph', label: 'Graph', Icon: GraphIcon },
]

// WAI-ARIA radio group pattern (mirrors AltitudeToggle): exactly one option
// is in the tab sequence (the checked one — roving tabindex); arrow keys
// move AND immediately select the adjacent option.
function ViewSwitcher({ value, onChange }: { value: TasksView; onChange: (next: TasksView) => void }) {
  const optionRefs = useRef<Partial<Record<TasksView, HTMLButtonElement | null>>>({})

  function moveSelection(delta: 1 | -1) {
    const currentIndex = VIEW_OPTIONS.findIndex((opt) => opt.value === value)
    const next = VIEW_OPTIONS[(currentIndex + delta + VIEW_OPTIONS.length) % VIEW_OPTIONS.length]
    onChange(next.value)
    optionRefs.current[next.value]?.focus()
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLButtonElement>) {
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        e.preventDefault()
        moveSelection(1)
        break
      case 'ArrowLeft':
      case 'ArrowUp':
        e.preventDefault()
        moveSelection(-1)
        break
      default:
        break
    }
  }

  return (
    <div
      className="flex items-center rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-0.5 gap-0.5 flex-shrink-0"
      role="radiogroup"
      aria-label="Task view"
    >
      {VIEW_OPTIONS.map((opt) => {
        const checked = value === opt.value
        return (
          <button
            key={opt.value}
            ref={(el) => {
              optionRefs.current[opt.value] = el
            }}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={checked ? 0 : -1}
            data-testid={`tasks-view-${opt.value}`}
            onClick={() => onChange(opt.value)}
            onKeyDown={handleKeyDown}
            className={cn(
              'flex items-center gap-1.5 px-2.5 py-1 text-[11px] font-medium rounded-md transition-colors',
              checked
                ? 'bg-[var(--color-surface-1)] text-[var(--color-accent)] shadow-sm'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <opt.Icon size={13} weight={checked ? 'fill' : 'regular'} />
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}

// ── Owner filter dropdown ───────────────────────────────────────────────────

interface OwnerFilterDropdownProps {
  agents: { id: string; name: string }[]
  value: string | null
  onChange: (agentId: string | null) => void
}

/** Owner ANDs with the plan filter (ADR-051 D6) — filters the board by `Task.agent_id`. */
function OwnerFilterDropdown({ agents, value, onChange }: OwnerFilterDropdownProps) {
  return (
    <Select value={value ?? OWNER_ALL} onValueChange={(v) => onChange(v === OWNER_ALL ? null : v)}>
      <SelectTrigger
        className="h-7 w-auto min-w-[8rem] text-xs bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
        aria-label="Filter by owner"
        data-testid="tasks-owner-filter"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={OWNER_ALL} className="text-xs">
          Owner: All
        </SelectItem>
        {agents.map((a) => (
          <SelectItem key={a.id} value={a.id} className="text-xs">
            Owner: {a.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function BoardSkeleton() {
  return (
    <div className="flex gap-3 p-4 overflow-x-auto flex-1">
      {[1, 2, 3, 4, 5, 6].map((i) => (
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
