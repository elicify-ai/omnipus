import { useCallback, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info } from '@phosphor-icons/react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { GraphView } from './graph/GraphView'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import {
  fetchTasks,
  fetchAgents,
  fetchPlans,
  setTaskDependencies,
  isApiError,
  tasksQueryKeys,
  plansQueryKeys,
} from '@/lib/api'
import { planDisplayColor, planDisplayLabel, planPhaseChip } from '@/lib/planStateColors'
import { cn } from '@/lib/utils'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { planDependencyReconnect } from './dependencyReconnect'

// ── F2 COMPONENT BOUNDARY ────────────────────────────────────────────────────
// The Graph (Task DAG) tab. Tasks as nodes, `blocked_by` as dependency edges,
// laid out left→right with live per-node status colour, pan/zoom, minimap.
// Export name + the WorkspaceTasksTab data-fetching pattern are preserved.
// The DAG canvas itself lives in ./graph/GraphView (presentational, testable).
// ─────────────────────────────────────────────────────────────────────────────

// Sentinel for the "All tasks" option in the plan switcher — Radix Select
// requires a non-empty string value, so this stands in for `activePlanId ===
// null` (the store field itself uses `null`, same as the "no tag filter"
// null sentinel in `planFilter.ts` — Select just can't carry `null` as an
// item value).
const GRAPH_PLAN_ALL = '__all__'

interface WorkspaceGraphTabProps {
  workspaceId: string
  /** Hide the in-graph "By plan" dropdown — set true when a parent screen
   * (the combined Tasks screen's PlansFilterBand) already exposes plan
   * selection, so the two controls don't duplicate each other. Defaults to
   * false so standalone use of this tab is unaffected. The plan info strip
   * (state/goal/progress) still renders regardless. */
  hidePlanSelector?: boolean
}

/**
 * The Graph tab body: fetches the workspace's user-surface tasks + the agents
 * cache (for avatar colour/icon), renders the DAG canvas, and opens the shared
 * task detail slide-over on node click — mirroring the Board's onTaskClick.
 */
export function WorkspaceGraphTab({ workspaceId, hidePlanSelector = false }: WorkspaceGraphTabProps) {
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)

  // Plan scope (ADR-051 — plans-as-filter): the SAME store field the Board's
  // lane ⑂ button writes before navigating here, so the graph opens already
  // scoped to the plan that was clicked. The switcher below reads and writes
  // this same field, so Board ⇄ Graph stay in sync either way.
  const activePlanId = useWorkspacesStore((s) => s.activePlanId)
  const setActivePlanId = useWorkspacesStore((s) => s.setActivePlanId)

  const {
    data: tasks = [],
    isLoading: tasksLoading,
    isError: tasksError,
    refetch: refetchTasks,
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

  const { data: plans = [], isError: plansError } = useQuery({
    queryKey: plansQueryKeys.list(workspaceId),
    queryFn: () => fetchPlans(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const activePlan = activePlanId != null ? (plans.find((p) => p.id === activePlanId) ?? null) : null

  const selectedTask =
    selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  const queryClient = useQueryClient()
  const { addToast } = useUiStore()

  // Persist a dependency add/remove made by dragging (or deleting) an edge on
  // the canvas — PUT /tasks/{id}/dependencies replaces the blocked set
  // atomically. On BOTH outcomes we re-invalidate the tasks query so the canvas
  // re-seeds to server truth: a rejected add (backend rejects cycles; cross-plan
  // is guarded below) never lingers, and a failed delete is restored.
  const depMutation = useMutation({
    mutationFn: ({ taskId, blockedBy }: { taskId: string; blockedBy: string[] }) =>
      setTaskDependencies(taskId, blockedBy),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    },
    onError: (err) => {
      addToast({
        message: isApiError(err)
          ? err.userMessage
          : err instanceof Error
            ? err.message
            : 'Failed to update dependencies',
        variant: 'error',
      })
    },
  })

  // Drag connect: the source handle is the blocker, the target handle is the
  // blocked task → add `blockerId` to blocked.blocked_by. Guard self / duplicate
  // / cross-plan (a dependency edge must stay inside one plan's DAG) before the
  // PUT; the backend additionally rejects cycles.
  const handleConnectDependency = useCallback(
    (blockerId: string, blockedId: string) => {
      if (blockerId === blockedId) return
      const blocked = tasks.find((t) => t.id === blockedId)
      const blocker = tasks.find((t) => t.id === blockerId)
      if (!blocked || !blocker) return
      if ((blocker.plan_id || null) !== (blocked.plan_id || null)) {
        addToast({ message: 'Dependencies must be within the same plan.', variant: 'error' })
        return
      }
      const current = blocked.blocked_by ?? []
      if (current.includes(blockerId)) return
      depMutation.mutate({ taskId: blockedId, blockedBy: [...current, blockerId] })
    },
    [tasks, addToast, depMutation],
  )

  const handleRemoveDependency = useCallback(
    (blockerId: string, blockedId: string) => {
      const blocked = tasks.find((t) => t.id === blockedId)
      const current = blocked?.blocked_by ?? []
      if (!current.includes(blockerId)) return
      depMutation.mutate({ taskId: blockedId, blockedBy: current.filter((x) => x !== blockerId) })
    },
    [tasks, depMutation],
  )

  // Reconnect: drag an edge endpoint onto another task to MOVE the dependency.
  // GraphView optimistically reconnects the edge; the pure `planDependencyReconnect`
  // decides the exact PUT(s) (or rejects), and on any no-op/rejection we
  // re-invalidate so the canvas reverts to server truth.
  const handleReconnectDependency = useCallback(
    (oldDep: { blocker: string; blocked: string }, newDep: { blocker: string; blocked: string }) => {
      const plan = planDependencyReconnect(tasks, oldDep, newDep)
      if (plan.error === 'same-plan') {
        addToast({ message: 'Dependencies must be within the same plan.', variant: 'error' })
      }
      if (plan.puts.length === 0) {
        void queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
        return
      }
      for (const put of plan.puts) depMutation.mutate(put)
    },
    [tasks, addToast, depMutation, queryClient],
  )

  if (tasksLoading) {
    return <GraphSkeleton />
  }

  if (tasksError && tasks.length === 0) {
    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 p-8 text-center">
        <p className="text-sm text-[var(--color-muted)]">
          Failed to load the task graph. Check your connection and try again.
        </p>
        <button tabIndex={0}
          type="button"
          onClick={() => void refetchTasks()}
          className="text-xs text-[var(--color-accent)] underline underline-offset-2"
        >
          Retry
        </button>
      </div>
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {agentsError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)]">
          <Info size={12} weight="fill" className="shrink-0" />
          Agent details failed to load — task avatars may be missing.
        </div>
      )}
      {plansError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)]">
          <Info size={12} weight="fill" className="shrink-0" />
          Plans failed to load — the plan filter may be incomplete.
        </div>
      )}

      {/* Graph toolbar — flat, borderless (no top rule on the canvas). Skipped
          entirely when it would be empty: embedded in the Tasks screen the plan
          selector is hidden, so with no active plan there's nothing to show and
          an empty bordered strip read as a stray line above the canvas. */}
      {(!hidePlanSelector || activePlan) && (
      <div className="flex items-center gap-3 px-4 py-2 flex-shrink-0">
        {!hidePlanSelector && (
          <>
            <span className="text-xs font-medium text-[var(--color-muted)] flex-shrink-0">By plan</span>
            <Select
              value={activePlanId ?? GRAPH_PLAN_ALL}
              onValueChange={(v) => setActivePlanId(v === GRAPH_PLAN_ALL ? null : v)}
            >
              <SelectTrigger className="h-8 w-56 text-xs" aria-label="Filter the graph by plan">
                <SelectValue placeholder="All tasks" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={GRAPH_PLAN_ALL} className="text-xs">
                  All tasks
                </SelectItem>
                {plans.map((p) => (
                  <SelectItem key={p.id} value={p.id} className="text-xs">
                    {p.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </>
        )}

        {activePlan && (
          <div className="flex flex-1 min-w-0 items-center gap-2 pl-2">
            {/* ADR-052 FR-015/US-8 — a user-cancelled plan renders
                "Cancelled" (orange), distinct from a genuine "Failed" (red),
                via the shared planDisplayColor/planDisplayLabel helpers. */}
            <span
              className="flex-shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold"
              style={{
                color: planDisplayColor(activePlan),
                backgroundColor: `${planDisplayColor(activePlan)}1a`,
              }}
            >
              {planDisplayLabel(activePlan)}
            </span>

            {/* ADR-053 FE-2 §7 (D7) — the plan's runtime sub-phase. The
                re-planning hold (`plan_phase: awaiting_owner_correction`) is
                the durable, operator-actionable signal: the plan reached
                all-terminal-but-unmet and is waiting on an owner correction
                (tail / supersede / targeted-retry) to continue — rendered as
                a warning chip so it can't blend into the gold Running badge.
                The other non-idle sub-phases (dispatching/judging/synthesizing)
                render as a quieter info chip — a live-progress hint. */}
            {(() => {
              const chip = planPhaseChip(activePlan)
              if (!chip) return null
              return (
                <span
                  data-testid={`plan-phase-chip-${activePlan.id}`}
                  className={cn(
                    'flex-shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-semibold leading-none',
                    chip.tone === 'warning'
                      ? 'border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 text-[color:var(--color-warning)]'
                      : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)]',
                  )}
                  title={
                    chip.tone === 'warning'
                      ? 'This plan is waiting on an owner correction (append a tail, supersede a done member, or targeted-retry a frozen member) to continue.'
                      : undefined
                  }
                >
                  {chip.label}
                </span>
              )
            })()}
            {activePlan.goal && (
              <p
                className="flex-1 min-w-0 truncate text-xs text-[var(--color-muted)]"
                title={activePlan.goal}
              >
                {activePlan.goal}
              </p>
            )}
            {/* Selected plan's completion — the share of its tasks that are
                `done`. Labelled + tooltipped so it reads as plan progress, not
                some ambient metric. */}
            <div
              className="flex flex-shrink-0 items-center gap-1.5"
              role="img"
              aria-label={`Plan progress: ${Math.round((activePlan.progress ?? 0) * 100)}% of tasks done`}
              title={`Plan progress — ${Math.round((activePlan.progress ?? 0) * 100)}% of this plan's tasks are done`}
            >
              <div className="h-1.5 w-16 rounded-full bg-[var(--color-surface-2)] overflow-hidden">
                <div
                  className="h-full rounded-full bg-[var(--color-accent)]"
                  style={{ width: `${Math.round((activePlan.progress ?? 0) * 100)}%` }}
                />
              </div>
              <span className="flex-shrink-0 text-right text-[10px] text-[var(--color-muted)]">
                {Math.round((activePlan.progress ?? 0) * 100)}% done
              </span>
            </div>
          </div>
        )}
      </div>
      )}

      <div className="relative flex-1 min-h-0">
        <GraphView
          tasks={tasks}
          agents={agents}
          selectedTaskId={selectedTaskId}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          // Only scope the canvas to a plan once it's actually resolvable
          // against the loaded `plans` list (`activePlan` above) — while
          // plansError/loading/just-cleared leaves `activePlanId` pointing at
          // a plan that ISN'T in that list, scoping to it would silently
          // filter the canvas down to nothing and show the misleading "This
          // plan has no dependencies yet" empty state (review-gate fix #3).
          planId={activePlan ? activePlanId : null}
          collapseOrphans
          onConnectDependency={handleConnectDependency}
          onRemoveDependency={handleRemoveDependency}
          onReconnectDependency={handleReconnectDependency}
        />
      </div>

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
