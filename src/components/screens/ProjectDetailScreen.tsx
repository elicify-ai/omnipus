import { useState, useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { List, SquaresFour, Lightning, Plus } from '@phosphor-icons/react'
import { ProjectHeader } from '@/components/projects/ProjectHeader'
import { MilestoneFilterPills, MILESTONE_FILTER_UNSCHEDULED } from '@/components/projects/MilestoneFilterPills'
import { BoardView } from '@/components/projects/BoardView'
import { ListView } from '@/components/projects/ListView'
import { ExecutionView } from '@/components/projects/ExecutionView'
import { TaskDetailSlideOver } from '@/components/projects/TaskDetailSlideOver'
import { CreateTaskSlideOver } from '@/components/projects/CreateTaskSlideOver'
import { CreateMilestoneSlideOver } from '@/components/projects/CreateMilestoneSlideOver'
import {
  fetchBoardTasks,
  fetchProjects,
  fetchMilestones,
  fetchAgents,
  boardTasksQueryKeys,
  projectsQueryKeys,
  milestonesQueryKeys,
} from '@/lib/api'
import { useProjectsStore } from '@/store/projectsStore'
import { cn } from '@/lib/utils'

type ViewMode = 'board' | 'list' | 'execution'

interface ProjectDetailScreenProps {
  projectId: string
}

export function ProjectDetailScreen({ projectId }: ProjectDetailScreenProps) {
  const navigate = useNavigate()
  const { activeMilestoneId, setActiveMilestoneId } = useProjectsStore()
  const [viewMode, setViewMode] = useState<ViewMode>('board')
  // F2 fix: store task id only; derive the displayed task from live query data
  // so the detail panel reflects post-mutation state immediately.
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [createMilestoneOpen, setCreateMilestoneOpen] = useState(false)

  // Load projects to find this project's metadata
  const { data: projects = [], isError: projectsError, isLoading: projectsLoading } = useQuery({
    queryKey: projectsQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchProjects({ status: 'active' }),
    staleTime: 30_000,
  })

  // Redirect /projects/inbox to the real default project ID.
  // "inbox" is a human-readable alias; the actual project has a real UUID.
  useEffect(() => {
    if (projectId !== 'inbox') return
    if (projects.length === 0) return
    const defaultProject = projects.find((p) => p.is_default)
    if (defaultProject) {
      void navigate({ to: '/projects/$projectId', params: { projectId: defaultProject.id }, replace: true })
    }
    // No default project — fall through so the "not found" state renders below.
  }, [projectId, projects, navigate])

  // Reset milestone filter whenever the active project changes.
  useEffect(() => {
    setActiveMilestoneId(null)
  }, [projectId, setActiveMilestoneId])

  // Also try archived projects for direct URL access
  const { data: archivedProjects = [], isLoading: archivedLoading } = useQuery({
    queryKey: projectsQueryKeys.list({ status: 'archived' }),
    queryFn: () => fetchProjects({ status: 'archived' }),
    staleTime: 60_000,
    enabled: projects.length > 0 && !projects.find((p) => p.id === projectId),
  })

  const project = projects.find((p) => p.id === projectId)
    ?? archivedProjects.find((p) => p.id === projectId)

  // Milestones for this project
  const { data: milestones = [], isError: milestonesError } = useQuery({
    queryKey: milestonesQueryKeys.list(projectId),
    queryFn: () => fetchMilestones(projectId),
    staleTime: 30_000,
    enabled: !!projectId && projectId !== 'inbox',
  })

  // Board tasks filtered by project
  const {
    data: tasks = [],
    isLoading: tasksLoading,
    isError: tasksError,
  } = useQuery({
    queryKey: boardTasksQueryKeys.list({ project_id: projectId }),
    queryFn: () => fetchBoardTasks({ project_id: projectId }),
    refetchInterval: 15_000,
    staleTime: 10_000,
    enabled: !!projectId && projectId !== 'inbox',
  })

  // Agents for list view filter
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  // F2 fix: derive the selected task from the live query array so the detail
  // panel always reflects post-mutation state (Start/Update/Retry refetches the
  // board-tasks query; the panel reads from the fresh array, not the snapshot).
  const selectedTask = selectedTaskId != null ? (tasks.find((t) => t.id === selectedTaskId) ?? null) : null

  // 'inbox' is a redirect alias — suppress render while the useEffect navigates to the real project ID.
  // Without this, archivedLoading can flip to false before navigate() completes, causing a crash
  // on any render path that accesses `project.name` when project is still undefined.
  if (projectId === 'inbox') return null

  if (projectsError) {
    return (
      <div className="flex items-center justify-center h-full p-8 text-[var(--color-muted)] text-sm">
        Failed to load project. Check your connection and try again.
      </div>
    )
  }

  // Show loading skeleton while projects list hasn't resolved yet
  if (projectsLoading) {
    return (
      <div className="flex flex-col h-full">
        <div className="h-16 bg-[var(--color-surface-1)] border-b border-[var(--color-border)] animate-pulse" />
        <div className="flex gap-3 p-4">
          {[1, 2, 3, 4, 5, 6].map((i) => (
            <div key={i} className="flex-1 min-w-[180px] h-48 rounded-xl border border-[var(--color-border)] animate-pulse bg-[var(--color-surface-1)]" />
          ))}
        </div>
      </div>
    )
  }

  if (!project) {
    if (archivedLoading) {
      return (
        <div className="flex flex-col h-full">
          <div className="h-16 bg-[var(--color-surface-1)] border-b border-[var(--color-border)] animate-pulse" />
          <div className="flex gap-3 p-4">
            {[1, 2, 3, 4, 5, 6].map((i) => (
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
        Project not found.
      </div>
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* Project header */}
      <ProjectHeader project={project} />

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
            active={viewMode === 'execution'}
            onClick={() => setViewMode('execution')}
            aria-label="Execution view"
          >
            <Lightning size={14} />
            <span>Execution</span>
          </ViewToggleButton>
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

      {/* Milestone filter pills — only shown when milestones exist */}
      {milestones.length > 0 && (
        <div className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
          <MilestoneFilterPills
            milestones={milestones}
            activeMilestoneId={activeMilestoneId}
            onSelect={setActiveMilestoneId}
            onNewMilestone={() => setCreateMilestoneOpen(true)}
          />
        </div>
      )}

      {/* When no milestones: show the new milestone button in a simpler form */}
      {milestones.length === 0 && !milestonesError && (
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
      {tasksLoading ? (
        <BoardSkeleton />
      ) : tasksError && tasks.length === 0 ? (
        <div className="flex items-center justify-center flex-1 p-8 text-[var(--color-muted)] text-sm">
          Failed to load tasks. Check your connection and try again.
        </div>
      ) : viewMode === 'board' ? (
        <BoardView
          tasks={tasks}
          milestones={milestones}
          activeMilestoneId={activeMilestoneId}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
          onNewTask={() => setCreateTaskOpen(true)}
        />
      ) : viewMode === 'execution' ? (
        <ExecutionView
          tasks={tasks}
          milestones={milestones}
          onTaskClick={(task) => setSelectedTaskId(task.id)}
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
        projectId={projectId}
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
        projectId={projectId}
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
      {[1, 2, 3, 4, 5, 6].map((i) => (
        <div
          key={i}
          className="flex flex-col min-w-[220px] flex-1 rounded-xl border border-[var(--color-border)] animate-pulse"
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
