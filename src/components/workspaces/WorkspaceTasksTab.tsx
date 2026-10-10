import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Plus, SquaresFour, ListBullets, Graph as GraphIcon, UsersThree, Tag } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { FilterMenu } from '@/components/ui/filter-menu'
import { ViewSwitch, type ViewSwitchOption } from '@/components/ui/view-switch'
import { AgentMark } from '@/components/agents/AgentMark'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { PlansFilterBand } from './PlansFilterBand'
import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { WorkspaceGraphTab } from './WorkspaceGraphTab'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
import { useTasksBoardLayout } from './useTasksBoardLayout'
import { fetchTasks, fetchPlans, fetchAgents, updateTask, deletePlan, isApiError, taskMoveErrorMessage, tasksQueryKeys, plansQueryKeys, workspacesQueryKeys } from '@/lib/api'
import type { Plan, Task } from '@/lib/api'
import { filterByTags, distinctTags, PLAN_FILTER_UNTAGGED } from '@/lib/planFilter'
import { filterTasks } from '@/lib/taskFilters'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

interface WorkspaceTasksTabProps { workspaceId: string }
type TasksView = 'board' | 'list' | 'graph'

const VIEW_OPTIONS: readonly ViewSwitchOption<TasksView>[] = [
  { value: 'board', label: 'Board', icon: <SquaresFour size={15} />, testId: 'tasks-view-board' },
  { value: 'list', label: 'List', icon: <ListBullets size={15} />, testId: 'tasks-view-list' },
  { value: 'graph', label: 'Graph', icon: <GraphIcon size={15} />, testId: 'tasks-view-graph' },
]

/** Combined Tasks screen: one plan/agent/tag scope shared by Board, List and Graph.
 * Board stays the user's choice while a too-small content frame temporarily shows List.
 */
export function WorkspaceTasksTab({ workspaceId }: WorkspaceTasksTabProps) {
  const { activeTags, setActiveTags, activePlanId, setActivePlanId } = useWorkspacesStore()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [view, setView] = useState<TasksView>('board')
  const [ownerAgentId, setOwnerAgentId] = useState<string | null>(null)
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [planSlideOver, setPlanSlideOver] = useState<{ open: boolean; plan: Plan | null }>({ open: false, plan: null })
  const surfaceRef = useRef<HTMLDivElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const { boardFits, requestBoardWidth } = useTasksBoardLayout(surfaceRef, frameRef, view === 'board', workspaceId)

  const moveMutation = useMutation({
    mutationFn: ({ task, status }: { task: Task; status: Task['status'] }) => updateTask(task.id, { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
    },
    // Same message mapper as Board's confirmed drag-outcome live region.
    onError: (err) => addToast({ message: taskMoveErrorMessage(err, plans), variant: 'error' }),
  })
  const clearPlanMutation = useMutation({
    mutationFn: (planId: string) => deletePlan(planId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      addToast({ message: 'Plan cleared', variant: 'success' })
    },
    onError: (err) => {
      const message = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to clear plan'
      addToast({ message, variant: 'error' })
    },
  })

  // Selecting a real plan also opens Graph. Clearing the scope never forces a view change.
  // Edit/Clear/Execute controls remain isolated from this selection callback.
  const handleSelectPlan = useCallback((planId: string | null) => {
    setActivePlanId(planId)
    if (planId !== null) setView('graph')
  }, [setActivePlanId])
  const handleSelectView = (next: TasksView) => {
    if (next === 'board' && view === 'board') requestBoardWidth()
    setView(next)
  }
  const pendingAction: { planId: string; action: 'clear' } | null =
    clearPlanMutation.isPending && clearPlanMutation.variables
      ? { planId: clearPlanMutation.variables, action: 'clear' } : null

  const { data: plans = [], isError: plansError } = useQuery({
    queryKey: plansQueryKeys.list(workspaceId), queryFn: () => fetchPlans(workspaceId),
    refetchInterval: 15_000, staleTime: 10_000, enabled: !!workspaceId,
  })
  const { data: tasks = [], isLoading: tasksLoading, isError: tasksError, refetch: refetchTasks } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId, surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId, surface: 'user' }),
    refetchInterval: 15_000, staleTime: 10_000, enabled: !!workspaceId,
  })
  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'], queryFn: fetchAgents, staleTime: 60_000,
  })
  const selectedPlan = useMemo(() => activePlanId != null ? plans.find((p) => p.id === activePlanId) ?? null : null, [activePlanId, plans])
  const ownerAgent = useMemo(() => ownerAgentId != null ? agents.find((a) => a.id === ownerAgentId) ?? null : null, [ownerAgentId, agents])

  // Reset a stale source id only after a non-empty source list has loaded.
  useEffect(() => {
    if (activePlanId && plans.length && !plans.some((p) => p.id === activePlanId)) setActivePlanId(null)
  }, [activePlanId, plans, setActivePlanId])
  useEffect(() => {
    if (ownerAgentId && agents.length && !agents.some((a) => a.id === ownerAgentId)) setOwnerAgentId(null)
  }, [ownerAgentId, agents])

  // Only apply a plan id that resolves. Agent/Tags remain visible and effective across views.
  const filteredTasks = useMemo(() => filterTasks(filterByTags(tasks, activeTags), {
    planId: selectedPlan ? activePlanId : null, ownerAgentId,
  }), [tasks, activeTags, activePlanId, selectedPlan, ownerAgentId])
  const hasActiveFilter = selectedPlan != null || ownerAgentId != null || activeTags.length > 0
  const selectedTask = selectedTaskId != null ? tasks.find((t) => t.id === selectedTaskId) ?? null : null
  const heading = selectedPlan ? `${selectedPlan.title} — tasks` : 'Team Task Backlog'
  const effectiveView = view === 'board' && !boardFits ? 'list' : view
  const tagOptions = useMemo(() => [
    { value: PLAN_FILTER_UNTAGGED, label: 'Untagged' },
    ...distinctTags(tasks).map((tag) => ({ value: tag, label: tag })),
  ], [tasks])

  return (
    <div ref={surfaceRef} className="@container relative flex h-full min-h-0 min-w-0 flex-col overflow-hidden bg-[var(--color-surface-2)]">
      <PlansFilterBand key={workspaceId} plans={plans} tasks={tasks} agents={agents} selectedPlanId={activePlanId}
        onSelectPlan={handleSelectPlan} onNewPlan={() => setPlanSlideOver({ open: true, plan: null })}
        onEditPlan={(plan) => setPlanSlideOver({ open: true, plan })}
        onClearPlan={(plan) => clearPlanMutation.mutate(plan.id)} pendingAction={pendingAction} showNewPlanTile={false} />

      {/* Two toolbar rows at every width: heading/view, then filters/create. */}
      <div className="shrink-0 px-[var(--space-4)] pt-[var(--space-3)] pb-[var(--space-2-5)]">
        <div className="flex min-w-0 items-center justify-between gap-[var(--space-3)]">
          <div className="flex min-w-0 flex-1 items-center gap-[var(--space-1)]" data-testid="tasks-heading">
            <h2 title={heading} className="min-w-0 truncate font-headline text-base font-bold text-[var(--color-secondary)]">{heading}</h2>
            {ownerAgent && <span className="truncate text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">· Agent: {ownerAgent.name}</span>}
          </div>
          <ViewSwitch value={view} onValueChange={handleSelectView} options={VIEW_OPTIONS} aria-label="Task view" />
        </div>
        <div className="mt-[var(--space-2)] flex min-w-0 items-center justify-between gap-[var(--space-2)]">
          <div className="flex min-w-0 items-center gap-[var(--space-2)]">
            <FilterMenu mode="single" value={ownerAgentId} onChange={setOwnerAgentId} clearLabel="All agents"
              label={ownerAgent?.name ?? 'Agent'} aria-label={`Filter by agent (current: ${ownerAgent?.name ?? 'all agents'})`}
              data-testid="tasks-agent-filter" className="max-w-[200px]"
              icon={ownerAgent ? <AgentMark agent={ownerAgent} size={18} /> : <UsersThree size={13} className="shrink-0" aria-hidden="true" />}
              options={agents.map((agent) => ({ value: agent.id, label: agent.name, icon: <AgentMark agent={agent} size={18} /> }))} />
            <FilterMenu mode="multiple" value={activeTags} onChange={setActiveTags} clearLabel="Clear tags" options={tagOptions}
              label={activeTags.length === 0 ? 'Tags' : `${activeTags.length} tag${activeTags.length === 1 ? '' : 's'}`}
              aria-label={activeTags.length === 0 ? 'Filter by tags' : `Filter by tags (${activeTags.length} tag${activeTags.length === 1 ? '' : 's'})`} data-testid="tasks-tag-filter" icon={<Tag size={13} className="shrink-0" aria-hidden="true" />} />
          </div>
          <Button type="button" variant="ghost" onClick={() => setCreateTaskOpen(true)}
            className="h-auto shrink-0 gap-[var(--space-1)] p-0 text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent hover:text-[var(--color-accent)]">
            <Plus size={14} />New Task
          </Button>
        </div>
      </div>
      <div className="mx-[var(--space-4)] shrink-0 border-t border-[var(--color-border)]/60" aria-hidden="true" />
      {selectedPlan && (
        // Keep the existing quick-create warning visible, separate from the two toolbar rows.
        <p data-testid="new-task-unplanned-hint" className="shrink-0 px-[var(--space-4)] py-[var(--space-1)] wrap-anywhere text-[length:var(--type-caption-size)] leading-snug text-[var(--color-muted)]">
          Lands unplanned, not in "{selectedPlan.title}" — use "Move to plan…" after creating
        </p>
      )}
      {agentsError && <div className="flex shrink-0 items-center gap-[var(--space-1)] bg-[var(--color-warning)]/10 px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"><Info size={12} weight="fill" className="shrink-0" />Agent details failed to load — task avatars may be missing.</div>}
      {plansError && <div className="flex shrink-0 items-center gap-[var(--space-1)] bg-[var(--color-warning)]/10 px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"><Info size={12} weight="fill" className="shrink-0" />Plans failed to load — the plans filter band may be incomplete.</div>}
      {tasksError && tasks.length > 0 && <div className="flex shrink-0 items-center gap-[var(--space-1)] bg-[var(--color-warning)]/10 px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"><Info size={12} weight="fill" className="shrink-0" />Couldn't refresh — showing last-known tasks.</div>}

      <div ref={frameRef} className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <p role="status" data-testid="tasks-board-fallback" className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)] [&:not(:empty)]:px-[var(--space-3)] [&:not(:empty)]:py-[var(--space-1)]">
          {view === 'board' && effectiveView === 'list' ? 'Board needs more room — showing list' : ''}
        </p>
        <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          {effectiveView === 'graph' ? <WorkspaceGraphTab workspaceId={workspaceId} hidePlanSelector ownerAgentId={ownerAgentId} activeTags={activeTags} />
            : tasksLoading ? <BoardSkeleton />
            : tasksError && tasks.length === 0 ? <QueryErrorState layout="fill" message="Failed to load tasks. Check your connection and try again." onRetry={() => void refetchTasks()} testId="workspace-tasks-error" />
            : effectiveView === 'board' ? <BoardView tasks={filteredTasks} plans={plans} agents={agents} altitude="top-level" hasActiveFilter={hasActiveFilter}
              onTaskClick={(task) => setSelectedTaskId(task.id)} onTaskMove={(task, status) => moveMutation.mutateAsync({ task, status })}
              onMoveRejected={(reason) => addToast({ message: reason, variant: 'error' })} />
            : <ListView tasks={filteredTasks} agents={agents} plans={plans} onTaskClick={(task) => setSelectedTaskId(task.id)} />}
        </div>
      </div>
      <TaskDetailSlideOver task={selectedTask} onClose={() => setSelectedTaskId(null)} />
      {/* Quick-create always lands unplanned; task detail's Move to plan is the explicit reassignment path. */}
      <CreateTaskSlideOver open={createTaskOpen} onOpenChange={setCreateTaskOpen} workspaceId={workspaceId} planId={null} />
      <CreatePlanSlideOver open={planSlideOver.open} onOpenChange={(open) => setPlanSlideOver((s) => ({ ...s, open }))} workspaceId={workspaceId} plan={planSlideOver.plan} />
    </div>
  )
}

function BoardSkeleton() {
  return (
    <div className="flex min-w-0 flex-1 gap-[var(--space-2-5)] overflow-hidden p-[var(--space-3)]">
      {[1, 2, 3, 4, 5, 6].map((i) => (
        <div key={i} className="flex min-w-0 flex-1 flex-col rounded-xl border border-[var(--color-border)] animate-pulse">
          <div className="h-10 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]" />
          <div className="flex flex-col gap-[var(--space-2)] p-[var(--space-2)]">
            {[1, 2].map((j) => <div key={j} className="h-14 rounded-lg bg-[var(--color-surface-1)]" />)}
          </div>
        </div>
      ))}
    </div>
  )
}
