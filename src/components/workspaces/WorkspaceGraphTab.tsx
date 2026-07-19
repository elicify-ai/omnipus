import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
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
import { fetchTasks, fetchAgents, fetchPlans, tasksQueryKeys, plansQueryKeys } from '@/lib/api'
import { planStateColor, planStateLabel } from '@/lib/planStateColors'
import { useWorkspacesStore } from '@/store/workspacesStore'

// ── F2 COMPONENT BOUNDARY ────────────────────────────────────────────────────
// The Graph (Task DAG) tab. Tasks as nodes, `blocked_by` as dependency edges,
// laid out left→right with live per-node status colour, pan/zoom, minimap.
// Export name + the WorkspaceTasksTab data-fetching pattern are preserved.
// The DAG canvas itself lives in ./graph/GraphView (presentational, testable).
// ─────────────────────────────────────────────────────────────────────────────

// Sentinel for the "All tasks" option in the plan switcher — Radix Select
// requires a non-empty string value, so this stands in for `activePlanId ===
// null` (the `PLAN_FILTER_ALL` sentinel used elsewhere is `null` itself,
// which Select can't carry as an item value).
const GRAPH_PLAN_ALL = '__all__'

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

  // Plan scope (Plan Swimlane board redesign): the SAME store field the
  // Board's lane ⑂ button writes before navigating here, so the graph opens
  // already scoped to the plan that was clicked. The switcher below reads
  // and writes this same field, so Board ⇄ Graph stay in sync either way.
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

      <div className="flex items-center gap-3 border-b border-[var(--color-border)] px-4 py-2 flex-shrink-0">
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

        {activePlan && (
          <div className="flex flex-1 min-w-0 items-center gap-2 pl-2">
            <span
              className="flex-shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold"
              style={{
                color: planStateColor(activePlan.state),
                backgroundColor: `${planStateColor(activePlan.state)}1a`,
              }}
            >
              {planStateLabel(activePlan.state)}
            </span>
            {activePlan.goal && (
              <p
                className="flex-1 min-w-0 truncate text-xs text-[var(--color-muted)]"
                title={activePlan.goal}
              >
                {activePlan.goal}
              </p>
            )}
            <div className="flex flex-shrink-0 items-center gap-1.5">
              <div className="h-1.5 w-16 rounded-full bg-[var(--color-surface-2)] overflow-hidden">
                <div
                  className="h-full rounded-full bg-[var(--color-accent)]"
                  style={{ width: `${Math.round((activePlan.progress ?? 0) * 100)}%` }}
                />
              </div>
              <span className="w-8 flex-shrink-0 text-right text-[10px] text-[var(--color-muted)]">
                {Math.round((activePlan.progress ?? 0) * 100)}%
              </span>
            </div>
          </div>
        )}
      </div>

      <div className="relative flex-1 min-h-0">
        <GraphView
          tasks={tasks}
          agents={agents}
          selectedTaskId={selectedTaskId}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          planId={activePlanId}
          collapseOrphans
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
