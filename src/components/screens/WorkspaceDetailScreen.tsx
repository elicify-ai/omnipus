import { useState, useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { List, SquaresFour, CalendarBlank, Plus } from '@phosphor-icons/react'
import { WorkspaceHeader } from '@/components/workspaces/WorkspaceHeader'
import { MilestoneFilterPills, MILESTONE_FILTER_UNSCHEDULED } from '@/components/workspaces/MilestoneFilterPills'
import { BoardView } from '@/components/workspaces/BoardView'
import { ListView } from '@/components/workspaces/ListView'
import { CalendarScreen } from '@/components/screens/CalendarScreen'
import { TaskDetailSlideOver } from '@/components/workspaces/TaskDetailSlideOver'
import { CreateTaskSlideOver } from '@/components/workspaces/CreateTaskSlideOver'
import { CreateMilestoneSlideOver } from '@/components/workspaces/CreateMilestoneSlideOver'
import {
  fetchTasks,
  fetchWorkspaces,
  fetchMilestones,
  fetchAgents,
  tasksQueryKeys,
  workspacesQueryKeys,
  milestonesQueryKeys,
} from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { BoardAltitude } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'

// Graph DAG view is Sprint 4 — placeholder only (do NOT build)
type ViewMode = 'board' | 'list' | 'calendar'

interface WorkspaceDetailScreenProps {
  workspaceId: string
}

export function WorkspaceDetailScreen({ workspaceId }: WorkspaceDetailScreenProps) {
  const navigate = useNavigate()
  const { activeMilestoneId, setActiveMilestoneId, boardAltitude, setBoardAltitude } = useWorkspacesStore()
  const [viewMode, setViewMode] = useState<ViewMode>('board')
  // Store task id only; derive the displayed task from live query data
  // so the detail panel reflects post-mutation state immediately.
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [createMilestoneOpen, setCreateMilestoneOpen] = useState(false)

  // Load workspaces to find this workspace's metadata
  const { data: workspaces = [], isError: workspacesError, isLoading: workspacesLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  // Redirect /workspaces/inbox to the real default workspace ID.
  useEffect(() => {
    if (workspaceId !== 'inbox') return
    if (workspaces.length === 0) return
    const defaultWorkspace = workspaces.find((p) => p.is_default)
    if (defaultWorkspace) {
      void navigate({ to: '/workspaces/$workspaceId', params: { workspaceId: defaultWorkspace.id }, replace: true })
    }
  }, [workspaceId, workspaces, navigate])

  // Reset milestone filter whenever the active workspace changes.
  useEffect(() => {
    setActiveMilestoneId(null)
  }, [workspaceId, setActiveMilestoneId])

  // Also try archived workspaces for direct URL access
  const { data: archivedWorkspaces = [], isLoading: archivedLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'archived' }),
    queryFn: () => fetchWorkspaces({ status: 'archived' }),
    staleTime: 60_000,
    enabled: workspaces.length > 0 && !workspaces.find((p) => p.id === workspaceId),
  })

  const workspace = workspaces.find((p) => p.id === workspaceId)
    ?? archivedWorkspaces.find((p) => p.id === workspaceId)

  // Milestones for this workspace
  const { data: milestones = [], isError: milestonesError } = useQuery({
    queryKey: milestonesQueryKeys.list(workspaceId),
    queryFn: () => fetchMilestones(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId && workspaceId !== 'inbox',
  })

  // Unified tasks filtered by workspace (user-surface only for general views)
  const {
    data: tasks = [],
    isLoading: tasksLoading,
    isError: tasksError,
  } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId, surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId, surface: 'user' }),
    refetchInterval: 15_000,
    staleTime: 10_000,
    enabled: !!workspaceId && workspaceId !== 'inbox',
  })

  // Agents for list view filter
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  // Derive the selected task from the live query array so the detail
  // panel always reflects post-mutation state.
  const selectedTask = selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  // 'inbox' is a redirect alias — suppress render while the useEffect navigates.
  if (workspaceId === 'inbox') return null

  if (workspacesError) {
    return (
      <div className="flex items-center justify-center h-full p-8 text-[var(--color-muted)] text-sm">
        Failed to load workspace. Check your connection and try again.
      </div>
    )
  }

  if (workspacesLoading) {
    return (
      <div className="flex flex-col h-full">
        <div className="h-16 bg-[var(--color-surface-1)] border-b border-[var(--color-border)] animate-pulse" />
        <div className="flex gap-3 p-4">
          {[1, 2, 3, 4, 5, 6, 7].map((i) => (
            <div key={i} className="flex-1 min-w-[180px] h-48 rounded-xl border border-[var(--color-border)] animate-pulse bg-[var(--color-surface-1)]" />
          ))}
        </div>
      </div>
    )
  }

  if (!workspace) {
    if (archivedLoading) {
      return (
        <div className="flex flex-col h-full">
          <div className="h-16 bg-[var(--color-surface-1)] border-b border-[var(--color-border)] animate-pulse" />
          <div className="flex gap-3 p-4">
            {[1, 2, 3, 4, 5, 6, 7].map((i) => (
              <div
                key={i}
                className="flex-1 min-w-[180px] h-48 rounded-xl border border-[var(--color-border)] animate-pulse bg-[var(--color-surface-1)]"
              />
            ))}
          </div>
        </div>
      )
    }
    return (
      <div className="flex items-center justify-center h-full p-8 text-[var(--color-muted)] text-sm">
        Workspace not found.
      </div>
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* Workspace header */}
      <WorkspaceHeader workspace={workspace} />

      {/* View toggle + milestone filter + new task button */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
        {/* View toggle */}
        <div className="flex items-center rounded-lg border border-[var(--color-border)] overflow-hidden">
          <ViewToggleButton
            active={viewMode === 'board'}
            onClick={() => setViewMode('board')}
            aria-label="Board view"
          >
            <SquaresFour size={14} />
            <span>Board</span>
          </ViewToggleButton>
          <ViewToggleButton
            active={viewMode === 'list'}
            onClick={() => setViewMode('list')}
            aria-label="List view"
          >
            <List size={14} />
            <span>List</span>
          </ViewToggleButton>
          <ViewToggleButton
            active={viewMode === 'calendar'}
            onClick={() => setViewMode('calendar')}
            aria-label="Calendar view"
          >
            <CalendarBlank size={14} />
            <span>Calendar</span>
          </ViewToggleButton>
          {/* Graph DAG view placeholder — Sprint 4 */}
          {/* <ViewToggleButton active={viewMode === 'graph'} onClick={() => setViewMode('graph')} aria-label="Graph view">
            <Graph size={14} />
            <span>Graph</span>
          </ViewToggleButton> */}
        </div>

        <div className="flex-1" />

        {/* New task button */}
        <button
          type="button"
          onClick={() => setCreateTaskOpen(true)}
          className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90 transition-colors"
        >
          <Plus size={13} />
          New task
        </button>
      </div>

      {/* Milestone filter pills — only shown for board/list views, not calendar */}
      {viewMode !== 'calendar' && milestones.length > 0 && (
        <div className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
          <MilestoneFilterPills
            milestones={milestones}
            activeMilestoneId={activeMilestoneId}
            onSelect={setActiveMilestoneId}
            onNewMilestone={() => setCreateMilestoneOpen(true)}
          />
        </div>
      )}

      {/* When no milestones and not calendar: show add milestone button */}
      {viewMode !== 'calendar' && milestones.length === 0 && !milestonesError && (
        <div className="px-4 py-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
          <button
            type="button"
            onClick={() => setCreateMilestoneOpen(true)}
            className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
          >
            <Plus size={10} />
            Add milestone
          </button>
        </div>
      )}

      {/* Main content area */}
      {viewMode === 'calendar' ? (
        <CalendarScreen workspaceId={workspaceId} />
      ) : tasksLoading ? (
        <BoardSkeleton />
      ) : tasksError && tasks.length === 0 ? (
        <div className="flex items-center justify-center flex-1 p-8 text-[var(--color-muted)] text-sm">
          Failed to load tasks. Check your connection and try again.
        </div>
      ) : viewMode === 'board' ? (
        <BoardView
          tasks={tasks}
          milestones={milestones}
          agents={agents}
          activeMilestoneId={activeMilestoneId}
          altitude={boardAltitude}
          onAltitudeChange={(next: BoardAltitude) => setBoardAltitude(next)}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          onNewTask={() => setCreateTaskOpen(true)}
        />
      ) : (
        <ListView
          tasks={tasks}
          milestones={milestones}
          agents={agents}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
        />
      )}

      {/* Task detail slide-over */}
      <TaskDetailSlideOver
        task={selectedTask}
        onClose={() => setSelectedTaskId(null)}
      />

      {/* Create task slide-over */}
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

      {/* Create milestone slide-over */}
      <CreateMilestoneSlideOver
        open={createMilestoneOpen}
        onOpenChange={setCreateMilestoneOpen}
        workspaceId={workspaceId}
      />
    </div>
  )
}

function ViewToggleButton({
  active,
  onClick,
  children,
  'aria-label': ariaLabel,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
  'aria-label': string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={ariaLabel}
      aria-pressed={active}
      className={cn(
        'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors',
        active
          ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)]'
          : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
      )}
    >
      {children}
    </button>
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
