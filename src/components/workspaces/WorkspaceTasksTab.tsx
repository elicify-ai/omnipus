import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus, SquaresFour, ListBullets, Graph as GraphIcon, CaretDown, UsersThree, Tag } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { IconRenderer } from '@/components/shared/IconRenderer'
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
import type { Agent, Plan, Task } from '@/lib/api'
import { filterByTags, distinctTags, PLAN_FILTER_UNTAGGED } from '@/lib/planFilter'
import { filterTasks } from '@/lib/taskFilters'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { cn, initialOf } from '@/lib/utils'

interface WorkspaceTasksTabProps {
  workspaceId: string
}

type TasksView = 'board' | 'list' | 'graph'

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
  const { activeTags, setActiveTags, activePlanId, setActivePlanId } =
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

  // At most one plan action is ever in flight — collapse the three
  // mutations' pending state into the one discriminated value PlansFilterBand
  // takes, instead of three independently-nullable "which plan is pending"
  // props.
  const pendingAction: { planId: string; action: 'approve' | 'stop' | 'clear' } | null =
    approvePlanMutation.isPending && approvePlanMutation.variables
      ? { planId: approvePlanMutation.variables, action: 'approve' }
      : stopPlanMutation.isPending && stopPlanMutation.variables
        ? { planId: stopPlanMutation.variables, action: 'stop' }
        : clearPlanMutation.isPending && clearPlanMutation.variables
          ? { planId: clearPlanMutation.variables, action: 'clear' }
          : null

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

  const selectedPlan = useMemo(
    () => (activePlanId != null ? (plans.find((p) => p.id === activePlanId) ?? null) : null),
    [activePlanId, plans],
  )
  const ownerAgent = useMemo(
    () => (ownerAgentId != null ? (agents.find((a) => a.id === ownerAgentId) ?? null) : null),
    [ownerAgentId, agents],
  )

  // Defensively reset a stale filter once its source list has actually
  // loaded — e.g. right after a plan tile's ⋯ → Clear deletes the plan the
  // board is currently scoped to, `activePlanId` still points at it for one
  // render. An empty `plans`/`agents` array before the first fetch resolves
  // must never be read as "this id doesn't exist", hence the `.length` guard.
  useEffect(() => {
    if (activePlanId && plans.length && !plans.some((p) => p.id === activePlanId)) {
      setActivePlanId(null)
    }
  }, [activePlanId, plans, setActivePlanId])
  useEffect(() => {
    if (ownerAgentId && agents.length && !agents.some((a) => a.id === ownerAgentId)) {
      setOwnerAgentId(null)
    }
  }, [ownerAgentId, agents])

  // Plan + owner + tag filters AND together (ADR-051 D2/D6) — the one
  // filtered task set every view below renders (Graph excepted; it scopes
  // itself off the shared activePlanId store field, see the header comment).
  // Only scope by plan once `activePlanId` actually resolves against the
  // loaded `plans` list (`selectedPlan`) — mirrors the Graph tab's own guard
  // (WorkspaceGraphTab passes `planId={activePlan ? activePlanId : null}`),
  // so a stale id (e.g. mid-Clear, or before `plans` has loaded) never blanks
  // the board to zero matches with no active tile and no signal why.
  const filteredTasks = useMemo(() => {
    const tagFiltered = filterByTags(tasks, activeTags)
    return filterTasks(tagFiltered, { planId: selectedPlan ? activePlanId : null, ownerAgentId })
  }, [tasks, activeTags, activePlanId, selectedPlan, ownerAgentId])

  // Drives BoardView's empty-state copy ("no tasks match" vs "no tasks yet").
  const hasActiveFilter = selectedPlan != null || ownerAgentId != null || activeTags.length > 0

  const selectedTask =
    selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  const heading = selectedPlan ? `${selectedPlan.title} — tasks` : 'Team Task Backlog'

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* ── Plans section ── bold heading + a minimalist "+ New Plan" text
          link, over a thin separator. (Matches the operator mockup: section
          labels + hairline separators + link-style create actions, generous
          spacing.) */}
      <div className="flex items-center justify-between px-6 pt-5 pb-3 flex-shrink-0">
        <h2 className="font-headline text-base font-bold text-[var(--color-secondary)]">Plans</h2>
        <button tabIndex={0}
          type="button"
          onClick={() => setPlanSlideOver({ open: true, plan: null })}
          className="flex items-center gap-1 text-sm text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
        >
          <Plus size={14} />
          New Plan
        </button>
      </div>
      <div className="mx-6 border-t border-[var(--color-border)]/60 flex-shrink-0" aria-hidden="true" />

      {/* Plans-as-filter band (ADR-051 D2/D3) — overview + single-select
          filter, not navigation. Its own dashed "New plan" tile is hidden;
          the section header above owns that action. */}
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
        pendingAction={pendingAction}
        showNewPlanTile={false}
      />

      {/* ── Team Task Backlog section ── dynamic heading (the plan name
          replaces "Team Task Backlog" when a plan filter is active — ADR-051
          D2, Visibility of System Status) + the Board/List/Graph switcher +
          filters + a minimalist "+ New Task" link, over a thin separator. */}
      <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3 px-6 pt-6 pb-3 flex-shrink-0">
        {/* Left: dynamic heading + the flat Board/List/Graph view switcher. */}
        <div className="flex min-w-0 items-center gap-5">
          <div className="flex min-w-0 items-center gap-1" data-testid="tasks-heading">
            <h2 className="font-headline text-base font-bold text-[var(--color-secondary)] truncate">
              {heading}
            </h2>
            {view !== 'graph' && ownerAgent && (
              <span className="whitespace-nowrap text-xs text-[var(--color-muted)]">· Agent: {ownerAgent.name}</span>
            )}
          </div>
          <ViewSwitcher value={view} onChange={setView} />
        </div>

        {/* Center: Agent + Tag filters, centered over the board. Both flat
            (borderless, chat-composer style). Board/List only — the Graph view
            honors the plan filter alone, so these don't render for a filter it
            doesn't actually apply. */}
        <div className="flex items-center justify-center gap-2">
          {view !== 'graph' && (
            <AgentFilterDropdown agents={agents} value={ownerAgentId} onChange={setOwnerAgentId} />
          )}
          {view !== 'graph' && (
            <TagFilterMultiSelect tasks={tasks} value={activeTags} onChange={setActiveTags} />
          )}
        </div>

        {/* Right: New Task on its own. */}
        <div className="flex items-center justify-end">
          <button tabIndex={0}
            type="button"
            onClick={() => setCreateTaskOpen(true)}
            className="flex items-center gap-1 text-sm text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
          >
            <Plus size={14} />
            New Task
          </button>
        </div>
      </div>
      <div className="mx-6 border-t border-[var(--color-border)]/60 flex-shrink-0" aria-hidden="true" />

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
      {tasksError && tasks.length > 0 && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)] flex-shrink-0">
          <Info size={12} weight="fill" className="shrink-0" />
          Couldn't refresh — showing last-known tasks.
        </div>
      )}

      {/* Content */}
      <div className="relative flex-1 min-h-0 flex flex-col overflow-hidden">
        {view === 'graph' ? (
          // The Graph tab manages its own loading/error state and reads the
          // shared activePlanId store field directly, so it bypasses the
          // tasksLoading/tasksError gate below (which only guards Board/List,
          // both fed from THIS screen's own filtered task query).
          <WorkspaceGraphTab workspaceId={workspaceId} hidePlanSelector />
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
            altitude="top-level"
            hasActiveFilter={hasActiveFilter}
            onTaskClick={(task) => setSelectedTaskId(task.id)}
            onTaskMove={(task, status) => moveMutation.mutate({ task, status })}
            onMoveRejected={(reason) => addToast({ message: reason, variant: 'error' })}
          />
        ) : (
          <ListView
            tasks={filteredTasks}
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
      className="flex items-center gap-4 flex-shrink-0"
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
            // Flat like the workspace header tabs — no border, background or
            // shadow; just an icon + label on the header, gold when active.
            className={cn(
              'flex items-center gap-1.5 text-sm font-medium transition-colors',
              checked
                ? 'text-[var(--color-accent)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <opt.Icon size={15} weight={checked ? 'fill' : 'regular'} />
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}

// ── Agent filter dropdown ───────────────────────────────────────────────────

interface AgentFilterDropdownProps {
  agents: Agent[]
  value: string | null
  onChange: (agentId: string | null) => void
}

/** Small round agent avatar — the agent's icon (or name initial) on its colour
 * dot. Mirrors the chat composer's AgentPicker so the roster reads identically. */
function AgentAvatar({ agent }: { agent: Agent }) {
  return (
    <div
      aria-hidden="true"
      className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[9px] font-bold"
      style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
    >
      {agent.icon ? <IconRenderer icon={agent.icon} size={11} /> : initialOf(agent.name)}
    </div>
  )
}

/**
 * Agent filter — filters the board by each task's ASSIGNED agent (`Task.agent_id`),
 * ANDed with the plan filter. Reuses the chat composer's AgentPicker visual
 * pattern (a `DropdownMenu` with an `IconRenderer` avatar dot in the agent's
 * colour + name), so it reads like the agent selector used elsewhere — with
 * icons — rather than a plain text dropdown. Default "All agents" clears it.
 */
function AgentFilterDropdown({ agents, value, onChange }: AgentFilterDropdownProps) {
  const selected = value ? (agents.find((a) => a.id === value) ?? null) : null
  return (
    <div data-testid="tasks-agent-filter">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Filter by agent (current: ${selected?.name ?? 'all agents'})`}
            className="flex h-8 min-w-0 max-w-[200px] items-center gap-1.5 px-2 text-xs font-medium"
          >
            {selected ? (
              <AgentAvatar agent={selected} />
            ) : (
              <div
                aria-hidden="true"
                className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[var(--color-surface-3)] text-[var(--color-muted)]"
              >
                <UsersThree size={11} />
              </div>
            )}
            <span className="truncate">{selected ? selected.name : 'Agent'}</span>
            <CaretDown size={11} className="shrink-0 opacity-60" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuItem onClick={() => onChange(null)} className="flex items-center gap-2">
            <div
              aria-hidden="true"
              className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[var(--color-surface-3)] text-[var(--color-muted)]"
            >
              <UsersThree size={11} />
            </div>
            <span className="truncate">All agents</span>
            {value === null && (
              <span className="ml-auto shrink-0 text-[10px] text-[var(--color-success)]">active</span>
            )}
          </DropdownMenuItem>
          {agents.map((agent) => (
            <DropdownMenuItem
              key={agent.id}
              onClick={() => onChange(agent.id)}
              className="flex items-center gap-2"
              title={agent.name}
            >
              <AgentAvatar agent={agent} />
              <span className="truncate">{agent.name}</span>
              {agent.id === value && (
                <span className="ml-auto shrink-0 text-[10px] text-[var(--color-success)]">active</span>
              )}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

// ── Tag filter (flat multiselect) ────────────────────────────────────────────

interface TagFilterMultiSelectProps {
  tasks: Task[]
  value: string[]
  onChange: (tags: string[]) => void
}

/**
 * Tag filter — a flat, borderless multiselect (chat-composer style): a ghost
 * `DropdownMenu` trigger over checkable tag items. Selecting multiple tags is a
 * union (a task matches ANY selected tag; see `filterByTags`). Includes an
 * "Untagged" option (`PLAN_FILTER_UNTAGGED`). Menu stays open while toggling
 * (DropdownMenuCheckboxItem), with a "Clear tags" action once any is set.
 */
function TagFilterMultiSelect({ tasks, value, onChange }: TagFilterMultiSelectProps) {
  const tags = distinctTags(tasks)
  const toggle = (tag: string) =>
    onChange(value.includes(tag) ? value.filter((t) => t !== tag) : [...value, tag])
  const label = value.length === 0 ? 'Tags' : `${value.length} tag${value.length === 1 ? '' : 's'}`

  return (
    <div data-testid="tasks-tag-filter">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            aria-label="Filter by tags"
            className="flex h-8 min-w-0 max-w-[200px] items-center gap-1.5 px-2 text-xs font-medium"
          >
            <Tag size={13} className="shrink-0 opacity-70" />
            <span className="truncate">{label}</span>
            <CaretDown size={11} className="shrink-0 opacity-60" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center" className="w-52">
          {/* onSelect preventDefault keeps the menu OPEN while toggling multiple
              tags — Radix closes a checkbox item's menu on select by default,
              which would break multiselect. */}
          <DropdownMenuCheckboxItem
            checked={value.includes(PLAN_FILTER_UNTAGGED)}
            onCheckedChange={() => toggle(PLAN_FILTER_UNTAGGED)}
            onSelect={(e) => e.preventDefault()}
            className="text-xs"
          >
            Untagged
          </DropdownMenuCheckboxItem>
          {tags.length > 0 && <DropdownMenuSeparator />}
          {tags.map((tag) => (
            <DropdownMenuCheckboxItem
              key={tag}
              checked={value.includes(tag)}
              onCheckedChange={() => toggle(tag)}
              onSelect={(e) => e.preventDefault()}
              className="text-xs"
            >
              {tag}
            </DropdownMenuCheckboxItem>
          ))}
          {value.length > 0 && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => onChange([])} className="text-xs text-[var(--color-muted)]">
                Clear tags
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
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
