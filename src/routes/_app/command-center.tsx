import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Plus } from '@phosphor-icons/react'
import { StatusBar } from '@/components/command-center/StatusBar'
import { AttentionSection } from '@/components/command-center/AttentionSection'
import { TaskList } from '@/components/command-center/TaskList'
import { TaskDetailPanel } from '@/components/command-center/TaskDetailPanel'
import { AgentSummarySection } from '@/components/command-center/AgentSummarySection'
import { ActivityFeed } from '@/components/command-center/ActivityFeed'
import { RateLimitStatusCard } from '@/components/command-center/RateLimitStatusCard'
import { SchedulesList } from '@/components/command-center/SchedulesList'
import { ScheduleFormSheet } from '@/components/command-center/ScheduleFormSheet'
import { fetchTasks } from '@/lib/api'
import type { Task } from '@/lib/api'
import { cn } from '@/lib/utils'

type StatusFilter = 'all' | Task['status']
type FeedView = 'tasks' | 'schedules'

const FILTER_TABS: { value: StatusFilter; label: string }[] = [
  { value: 'all',       label: 'All' },
  { value: 'queued',    label: 'Queued' },
  { value: 'assigned',  label: 'Assigned' },
  { value: 'running',   label: 'Running' },
  { value: 'completed', label: 'Completed' },
  { value: 'failed',    label: 'Failed' },
]

export function CommandCenterScreen() {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [feedView, setFeedView] = useState<FeedView>('tasks')
  const [creatingSchedule, setCreatingSchedule] = useState(false)

  const { data: tasks = [], isError: tasksError } = useQuery({
    queryKey: ['tasks'],
    queryFn: () => fetchTasks(),
    staleTime: 30_000,
    refetchInterval: 10_000,
  })
  const countFor = (filter: StatusFilter) =>
    filter === 'all' ? tasks.length : tasks.filter((t) => t.status === filter).length

  return (
    <div className="absolute inset-0 overflow-y-auto">
    <div className="flex flex-col">
      {/* 1. Status bar */}
      <StatusBar />

      {/* 2. Rate limit status */}
      <RateLimitStatusCard />

      {/* 3. Attention section */}
      <AttentionSection />

      {/* Tasks error */}
      {tasksError && (
        <div className="px-4 py-2 text-xs text-red-400 border-b border-[var(--color-border)]">
          Failed to load tasks. Please try again.
        </div>
      )}

      {/* 4. Agent summary — compact card row */}
      <AgentSummarySection />

      {/* 5a. Tasks | Schedules view toggle */}
      <div className="flex items-center justify-between gap-2 px-4 pt-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
        <div className="flex items-center gap-0.5">
          {(['tasks', 'schedules'] as FeedView[]).map((view) => {
            const active = feedView === view
            return (
              <button
                key={view}
                type="button"
                onClick={() => setFeedView(view)}
                className={cn(
                  'px-3 py-1.5 rounded-md text-xs font-medium transition-colors whitespace-nowrap capitalize',
                  active
                    ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {view}
              </button>
            )
          })}
        </div>
        {feedView === 'schedules' && (
          <button
            type="button"
            onClick={() => setCreatingSchedule(true)}
            className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
          >
            <Plus size={13} />
            New schedule
          </button>
        )}
      </div>

      {/* 5b. Task status filter tabs — only in Tasks view */}
      {feedView === 'tasks' && (
        <div className="flex items-center gap-0.5 px-4 py-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-x-auto">
          {FILTER_TABS.map((tab) => {
            const count = countFor(tab.value)
            const active = statusFilter === tab.value
            return (
              <button
                key={tab.value}
                type="button"
                onClick={() => setStatusFilter(tab.value)}
                className={cn(
                  'flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-colors whitespace-nowrap',
                  active
                    ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {tab.label}
                {count > 0 && (
                  <span
                    className={cn(
                      'rounded-full px-1.5 py-0.5 text-[10px] leading-none font-semibold',
                      active
                        ? 'bg-[var(--color-primary)]/30 text-[var(--color-primary)]'
                        : 'bg-[var(--color-surface-2)] text-[var(--color-muted)]',
                    )}
                  >
                    {count}
                  </span>
                )}
              </button>
            )
          })}
        </div>
      )}

      {/* 6. Feed — Tasks or Schedules */}
      {feedView === 'tasks' ? (
        <TaskList
          statusFilter={statusFilter}
          onTaskSelect={setSelectedTask}
        />
      ) : (
        <div className="px-4 py-3">
          <SchedulesList />
        </div>
      )}

      {/* 7. Activity feed — collapsed by default, at the bottom */}
      <div className="border-t border-[var(--color-border)]">
        <ActivityFeed />
      </div>

      {/* Task detail slide-over */}
      <TaskDetailPanel
        task={selectedTask}
        onClose={() => setSelectedTask(null)}
        onTaskSelect={setSelectedTask}
      />

      {/* New schedule slide-over */}
      {creatingSchedule && (
        <ScheduleFormSheet
          open={true}
          onOpenChange={(open) => {
            if (!open) setCreatingSchedule(false)
          }}
        />
      )}
    </div>
    </div>
  )
}

export const Route = createFileRoute('/_app/command-center')({
  component: CommandCenterScreen,
})
