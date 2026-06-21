// Calendar view — shows tasks with time triggers (once/every/recurring) and milestones by date.
//
// Data sources:
//   - Tasks:       GET /api/v1/tasks?workspace_id={id}  (Task[])
//   - Milestones:  GET /api/v1/workspaces/{id}/milestones (Milestone[])
//
// Only tasks with a time trigger (once/every/recurring) appear on the calendar.
// Tasks with no trigger (manual) or surface!=user are excluded.

import { useQuery } from '@tanstack/react-query'
import { CalendarBlank, Flag, ArrowClockwise } from '@phosphor-icons/react'
import {
  fetchTasks,
  fetchMilestones,
  tasksQueryKeys,
  milestonesQueryKeys,
  type Milestone,
  type Task,
} from '@/lib/api'
import { cn } from '@/lib/utils'

interface CalendarScreenProps {
  workspaceId: string
}

// CalendarEvent is a unified view model for tasks and milestones with a date.
// not-wire-format: internal view model only; never sent over the network.
type CalendarEvent =
  | { kind: 'task-trigger'; date: Date; task: Task; label: string }
  | { kind: 'task-due'; date: Date; task: Task }
  | { kind: 'milestone'; date: Date; milestone: Milestone }

/** Parse a date string (date or date-time, ISO 8601). Returns null on failure. */
function parseDate(s: string | null | undefined): Date | null {
  if (!s) return null
  const d = new Date(s)
  return isNaN(d.getTime()) ? null : d
}

/** Parse a unix timestamp (ms). Returns null on failure. */
function parseMs(ms: number | null | undefined): Date | null {
  if (!ms) return null
  const d = new Date(ms)
  return isNaN(d.getTime()) ? null : d
}

/** Format a Date as "Mon DD" (short month + day). */
function formatShort(d: Date): string {
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })
}

/** Format a Date as "MMMM YYYY" for section headers. */
function formatMonth(d: Date): string {
  return d.toLocaleDateString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' })
}

/** Group events by their calendar month (YYYY-MM key). */
function groupByMonth(events: CalendarEvent[]): Map<string, CalendarEvent[]> {
  const map = new Map<string, CalendarEvent[]>()
  for (const ev of events) {
    const key = `${ev.date.getUTCFullYear()}-${String(ev.date.getUTCMonth() + 1).padStart(2, '0')}`
    const group = map.get(key)
    if (group) {
      group.push(ev)
    } else {
      map.set(key, [ev])
    }
  }
  return map
}

/** Render a single calendar event row. */
function EventRow({ event }: { event: CalendarEvent }) {
  if (event.kind === 'task-trigger' || event.kind === 'task-due') {
    const { task } = event
    const label = event.kind === 'task-trigger' ? event.label : 'Due'
    const labelClass =
      event.kind === 'task-trigger'
        ? 'text-[#E2E8F0]/60 bg-white/5'
        : 'text-[#D4AF37] bg-[#D4AF37]/10'
    const isRecurring = task.trigger && (task.trigger.type === 'every' || task.trigger.type === 'recurring')

    return (
      <div className="flex items-center gap-3 px-4 py-2.5 rounded-lg hover:bg-white/5 transition-colors">
        <span className="w-14 shrink-0 text-right text-sm font-mono text-[#E2E8F0]/50">
          {formatShort(event.date)}
        </span>
        <span
          className={cn(
            'shrink-0 px-1.5 py-0.5 rounded text-xs font-medium uppercase tracking-wide',
            labelClass,
          )}
        >
          {label}
        </span>
        <span className="flex-1 text-sm text-[#E2E8F0] truncate">{task.title}</span>
        {isRecurring && (
          <ArrowClockwise
            size={14}
            weight="regular"
            className="shrink-0 text-[#E2E8F0]/40"
            aria-label="Recurring"
          />
        )}
        <span
          className={cn(
            'shrink-0 px-1.5 py-0.5 rounded text-xs capitalize',
            task.status === 'done' && 'text-emerald-400 bg-emerald-400/10',
            task.status === 'in_progress' && 'text-blue-400 bg-blue-400/10',
            task.status === 'blocked' && 'text-amber-400 bg-amber-400/10',
            task.status === 'failed' && 'text-red-400 bg-red-400/10',
            (task.status === 'inbox' || task.status === 'next' || task.status === 'planning') &&
              'text-[#E2E8F0]/60 bg-white/5',
          )}
        >
          {task.status === 'in_progress' ? 'in progress' : task.status}
        </span>
      </div>
    )
  }

  // milestone event
  const { milestone } = event
  return (
    <div className="flex items-center gap-3 px-4 py-2.5 rounded-lg hover:bg-white/5 transition-colors">
      <span className="w-14 shrink-0 text-right text-sm font-mono text-[#E2E8F0]/50">
        {formatShort(event.date)}
      </span>
      <span className="shrink-0 px-1.5 py-0.5 rounded text-xs font-medium uppercase tracking-wide text-[#D4AF37] bg-[#D4AF37]/10">
        Milestone
      </span>
      <Flag size={13} weight="fill" className="shrink-0 text-[#D4AF37]" />
      <span className="flex-1 text-sm text-[#E2E8F0] truncate">{milestone.name}</span>
    </div>
  )
}

export function CalendarScreen({ workspaceId }: CalendarScreenProps) {
  const { data: tasks = [], isLoading: tasksLoading, isError: tasksError } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId }),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const { data: milestones = [], isLoading: milestonesLoading, isError: milestonesError } = useQuery({
    queryKey: milestonesQueryKeys.list(workspaceId),
    queryFn: () => fetchMilestones(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const isLoading = tasksLoading || milestonesLoading
  const isError = tasksError || milestonesError

  // Build unified event list from tasks (trigger fire time + due) and milestones (due_date).
  const events: CalendarEvent[] = []

  for (const task of tasks) {
    // Skip non-user-surface tasks
    if (task.surface && task.surface !== 'user') continue

    if (task.trigger) {
      const { type, config } = task.trigger
      if (type === 'once' && config?.at_ms) {
        const d = parseMs(config.at_ms)
        if (d) events.push({ kind: 'task-trigger', date: d, task, label: 'Fires' })
      } else if (type === 'every' && config?.every_ms) {
        // Show next fire as "now + interval" approximation
        const d = new Date(Date.now() + config.every_ms)
        events.push({ kind: 'task-trigger', date: d, task, label: 'Repeats' })
      } else if (type === 'recurring' && config?.cron_expr) {
        // No local cron parser available; skip in-calendar until Sprint 4 adds server-computed next_fire_at
      }
    }

    const dueDate = parseDate(task.due)
    if (dueDate) {
      events.push({ kind: 'task-due', date: dueDate, task })
    }
  }

  for (const milestone of milestones) {
    const dueDate = parseDate(milestone.due_date)
    if (dueDate) {
      events.push({ kind: 'milestone', date: dueDate, milestone })
    }
  }

  // Sort chronologically.
  events.sort((a, b) => a.date.getTime() - b.date.getTime())

  const grouped = groupByMonth(events)
  const monthKeys = Array.from(grouped.keys()).sort()

  return (
    <div className="flex flex-col min-h-0 h-full bg-[#0A0A0B] text-[#E2E8F0]">
      {/* Header */}
      <div className="flex items-center gap-3 px-6 py-4 border-b border-white/[0.08] shrink-0">
        <CalendarBlank size={20} weight="regular" className="text-[#D4AF37]" />
        <h1 className="text-base font-semibold font-[Outfit]">Calendar</h1>
        <span className="ml-auto text-xs text-[#E2E8F0]/40">
          Recurring tasks with time triggers — set due or trigger on a task to see it here
        </span>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto px-4 py-6">
        {isLoading && (
          <div className="flex items-center justify-center h-40 text-sm text-[#E2E8F0]/40">
            Loading scheduled items...
          </div>
        )}

        {isError && !isLoading && (
          <div className="flex items-center justify-center h-40 text-sm text-red-400">
            Failed to load calendar data. Please try again.
          </div>
        )}

        {!isLoading && !isError && events.length === 0 && (
          <div className="flex flex-col items-center justify-center h-60 gap-3 text-[#E2E8F0]/40">
            <CalendarBlank size={40} weight="thin" />
            <p className="text-sm">No scheduled tasks or milestones yet.</p>
            <p className="text-xs">
              Set a{' '}
              <span className="font-mono text-[#E2E8F0]/60">trigger</span>
              {' or '}
              <span className="font-mono text-[#E2E8F0]/60">due</span>
              {' date on a task to see it here.'}
            </p>
          </div>
        )}

        {!isLoading && !isError && monthKeys.length > 0 && (
          <div className="flex flex-col gap-8 max-w-2xl mx-auto">
            {monthKeys.map((monthKey) => {
              const monthEvents = grouped.get(monthKey)!
              const firstEvent = monthEvents[0]
              return (
                <section key={monthKey} aria-label={formatMonth(firstEvent.date)}>
                  <h2 className="text-xs font-semibold uppercase tracking-widest text-[#E2E8F0]/40 mb-3 px-4">
                    {formatMonth(firstEvent.date)}
                  </h2>
                  <div className="flex flex-col gap-0.5 rounded-xl border border-white/[0.06] bg-white/[0.02] overflow-hidden">
                    {monthEvents.map((ev, idx) => (
                      <EventRow key={idx} event={ev} />
                    ))}
                  </div>
                </section>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
