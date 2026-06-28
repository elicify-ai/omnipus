// Workspace Calendar — Outlook-style FullCalendar v6 surface.
// Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2).
//
// Replaces the former month-grouped list. The grid ALWAYS renders (the empty-state
// bug is gone); supports Month/Week/Day/Agenda, drag-to-reschedule + a keyboard
// path, and click/slot-select to create. This file is the integration hub: it maps
// tasks/milestones → events (pure fn), hosts the wrapper + toolbar, and owns the
// handlers/mutations (optimistic move handled by FullCalendar; revert + undo + toast
// here).

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type FullCalendar from '@fullcalendar/react'
import type { EventClickArg, EventDropArg, DateSelectArg } from '@fullcalendar/core'
import type { DateClickArg } from '@fullcalendar/interaction'
import {
  fetchTasks,
  fetchMilestones,
  updateTask,
  updateMilestone,
  tasksQueryKeys,
  milestonesQueryKeys,
  type Task,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { mapToCalendarEvents, formatLocalDate } from '@/lib/calendar/eventMapping'
import { FullCalendarView } from '@/components/calendar/FullCalendarView'
import { CalendarToolbar } from '@/components/calendar/CalendarToolbar'
import { MilestoneDatePopover } from '@/components/calendar/MilestoneDatePopover'
import type {
  CalendarViewName,
  CalendarEventExtProps,
  MilestoneTarget,
} from '@/components/calendar/types'
import { CreateTaskSlideOver } from '@/components/workspaces/CreateTaskSlideOver'
import { TaskDetailSlideOver } from '@/components/workspaces/TaskDetailSlideOver'

interface CalendarScreenProps {
  workspaceId: string
}

/** Format a Date as a `datetime-local` value ("YYYY-MM-DDTHH:mm") in LOCAL time. */
function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}`
  )
}

/** Short human label for a reschedule toast ("Jun 23"). */
function formatShort(d: Date): string {
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

export function CalendarScreen({ workspaceId }: CalendarScreenProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const calendarRef = useRef<FullCalendar | null>(null)

  // The DOM node that triggered the last create/open gesture — for focus restore (C-4).
  const triggerElRef = useRef<HTMLElement | null>(null)

  // ── Data ────────────────────────────────────────────────────────────────────
  const {
    data: tasks = [],
    isLoading: tasksLoading,
    isError: tasksError,
  } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId }),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const {
    data: milestones = [],
    isLoading: milestonesLoading,
    isError: milestonesError,
  } = useQuery({
    queryKey: milestonesQueryKeys.list(workspaceId),
    queryFn: () => fetchMilestones(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const events = useMemo(() => mapToCalendarEvents(tasks, milestones), [tasks, milestones])
  const isLoading = tasksLoading || milestonesLoading
  const isEmpty = !isLoading && events.length === 0

  // Degrade on query failure (FR-016, I-2) — non-blocking toast, grid still renders.
  useEffect(() => {
    if (tasksError) addToast({ message: "Couldn't load tasks", variant: 'error' })
  }, [tasksError, addToast])
  useEffect(() => {
    if (milestonesError) addToast({ message: "Couldn't load milestones", variant: 'error' })
  }, [milestonesError, addToast])

  // ── Toolbar state (driven by FullCalendar's datesSet) ─────────────────────────
  const [currentView, setCurrentView] = useState<CalendarViewName>('dayGridMonth')
  const [title, setTitle] = useState('')

  const handleDatesSet = useCallback((nextTitle: string, view: CalendarViewName) => {
    setTitle(nextTitle)
    setCurrentView(view)
  }, [])

  const handleViewChange = useCallback((view: CalendarViewName) => setCurrentView(view), [])

  // ── Slide-over / popover state ───────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false)
  const [initialDue, setInitialDue] = useState<string | undefined>(undefined)
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [milestoneTarget, setMilestoneTarget] = useState<MilestoneTarget | null>(null)

  const invalidate = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    })
    void queryClient.invalidateQueries({ queryKey: milestonesQueryKeys.list(workspaceId) })
  }, [queryClient, workspaceId])

  // Restore focus to the chip/cell that opened a dialog (C-4 / FR-013).
  const restoreFocus = useCallback(() => {
    const el = triggerElRef.current
    triggerElRef.current = null
    // best-effort — event chips are focusable; day cells may not be.
    window.requestAnimationFrame(() => el?.focus?.())
  }, [])

  // ── Reschedule persistence (the whole-trigger + date-only rules — F-05/F-06) ──
  const persistReschedule = useCallback(
    async (ext: CalendarEventExtProps, start: Date): Promise<void> => {
      if (ext.kind === 'milestone' && ext.milestoneId) {
        await updateMilestone(workspaceId, ext.milestoneId, { due_date: formatLocalDate(start) })
        return
      }
      if (ext.kind === 'task-due' && ext.taskId) {
        // Task `due` is RFC3339 date-time (contract: format date-time), so a
        // date-only string is rejected 400. Write the dropped day's local-midnight
        // instant as ISO; the read side (mapToCalendarEvents) places it by LOCAL
        // date, so this round-trips with no off-by-one in any timezone.
        await updateTask(ext.taskId, { due: start.toISOString() })
        return
      }
      if (ext.kind === 'task-fire' && ext.taskId) {
        const task = tasks.find((t) => t.id === ext.taskId)
        const trigger = task?.trigger
        // Send the WHOLE trigger, preserving type + sibling config keys (F-05).
        await updateTask(ext.taskId, {
          trigger: {
            type: trigger?.type ?? 'once',
            config: { ...(trigger?.config ?? {}), at_ms: start.getTime() },
          },
        })
      }
    },
    [tasks, workspaceId],
  )

  const handleEventDrop = useCallback(
    (arg: EventDropArg) => {
      const ext = arg.event.extendedProps as CalendarEventExtProps
      const newStart = arg.event.start
      const oldStart = arg.oldEvent.start
      if (!newStart) {
        arg.revert()
        return
      }
      void (async () => {
        try {
          await persistReschedule(ext, newStart)
          invalidate()
          addToast({
            message: `Rescheduled to ${formatShort(newStart)}`,
            variant: 'success',
            duration: 5000,
            action: oldStart
              ? {
                  label: 'Undo',
                  onClick: () => {
                    void (async () => {
                      try {
                        await persistReschedule(ext, oldStart)
                        invalidate()
                      } catch {
                        addToast({ message: "Couldn't undo — please try again", variant: 'error' })
                      }
                    })()
                  },
                }
              : undefined,
          })
        } catch {
          arg.revert()
          addToast({ message: "Couldn't reschedule — please try again", variant: 'error' })
        }
      })()
    },
    [persistReschedule, invalidate, addToast],
  )

  const handleEventClick = useCallback(
    (arg: EventClickArg) => {
      triggerElRef.current = (arg.jsEvent?.target as HTMLElement) ?? null
      const ext = arg.event.extendedProps as CalendarEventExtProps
      if (ext.kind === 'milestone' && ext.milestoneId) {
        const m = milestones.find((x) => x.id === ext.milestoneId)
        if (m) setMilestoneTarget({ id: m.id, name: m.name, due_date: m.due_date ?? null })
        return
      }
      if (ext.taskId) {
        const t = tasks.find((x) => x.id === ext.taskId)
        if (t) setSelectedTask(t)
      }
    },
    [tasks, milestones],
  )

  const handleDateClick = useCallback((arg: DateClickArg) => {
    triggerElRef.current = (arg.jsEvent?.target as HTMLElement) ?? null
    setInitialDue(arg.allDay ? `${formatLocalDate(arg.date)}T00:00` : toDatetimeLocal(arg.date))
    setCreateOpen(true)
  }, [])

  const handleDateSelect = useCallback((arg: DateSelectArg) => {
    triggerElRef.current = (arg.jsEvent?.target as HTMLElement) ?? null
    setInitialDue(arg.allDay ? `${formatLocalDate(arg.start)}T00:00` : toDatetimeLocal(arg.start))
    setCreateOpen(true)
  }, [])

  const handleNewTask = useCallback(() => {
    triggerElRef.current = null
    setInitialDue(undefined)
    setCreateOpen(true)
  }, [])

  return (
    <div className="flex flex-col h-full min-h-0 overflow-x-hidden bg-[var(--color-bg)] text-[var(--color-secondary)]">
      <div className="@container flex-shrink-0 w-full min-w-0">
        <CalendarToolbar
          calendarRef={calendarRef}
          currentView={currentView}
          title={title}
          onViewChange={handleViewChange}
          onNewTask={handleNewTask}
        />
      </div>

      <div className="flex-1 min-h-0 min-w-0 p-3" data-testid="calendar-grid">
        <FullCalendarView
          events={events}
          calendarRef={calendarRef}
          isLoading={isLoading}
          isEmpty={isEmpty}
          onEventDrop={handleEventDrop}
          onEventClick={handleEventClick}
          onDateClick={handleDateClick}
          onDateSelect={handleDateSelect}
          onDatesSet={handleDatesSet}
        />
      </div>

      <CreateTaskSlideOver
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open)
          if (!open) {
            invalidate()
            restoreFocus()
          }
        }}
        workspaceId={workspaceId}
        initialDue={initialDue}
      />

      <TaskDetailSlideOver
        task={selectedTask}
        onClose={() => {
          setSelectedTask(null)
          restoreFocus()
        }}
      />

      <MilestoneDatePopover
        workspaceId={workspaceId}
        milestone={milestoneTarget}
        onClose={() => {
          setMilestoneTarget(null)
          restoreFocus()
        }}
        onRescheduled={invalidate}
      />
    </div>
  )
}
