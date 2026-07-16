// Workspace Calendar — Outlook-style FullCalendar v6 surface.
// Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2).
//
// Replaces the former month-grouped list. The grid ALWAYS renders (the empty-state
// bug is gone); supports Month/Week/Day/Agenda, drag-to-reschedule + a keyboard
// path, and click/slot-select to create. This file is the integration hub: it maps
// tasks/milestones → events (pure fn), hosts the wrapper + toolbar, and owns the
// handlers/mutations (optimistic move handled by FullCalendar; revert + undo + toast
// here). It also owns the optimistic query-cache patch + per-item rollback layer that
// keeps the TanStack Query cache in sync with FullCalendar's DOM move — cancelling
// in-flight queries before each patch and rolling back only the changed row on failure
// so concurrent updates to other rows are never lost.

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
  type Milestone,
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

  // ── Reschedule persistence (whole-trigger + date-format rules — F-05/F-06/F-08) ─
  // `ext` is the discriminated CalendarEventExtProps union, so the switch narrows
  // taskId/milestoneId with no defensive checks and is exhaustive over `kind`.
  const persistReschedule = useCallback(
    async (ext: CalendarEventExtProps, start: Date): Promise<void> => {
      switch (ext.kind) {
        case 'milestone':
          // Milestone due_date is a plain ISO date string → local YYYY-MM-DD.
          await updateMilestone(workspaceId, ext.milestoneId, {
            due_date: formatLocalDate(start),
          })
          return
        case 'task-due':
          // Task `due` is RFC3339 date-time (contract: format date-time), so a
          // date-only string is rejected 400. Write the dropped day's local-midnight
          // instant as ISO; the read side places it by LOCAL date → no off-by-one.
          await updateTask(ext.taskId, { due: start.toISOString() })
          return
        case 'task-fire': {
          const trigger = tasks.find((t) => t.id === ext.taskId)?.trigger
          // Send the WHOLE trigger, preserving type + sibling config keys (F-05).
          await updateTask(ext.taskId, {
            trigger: {
              type: trigger?.type ?? 'once',
              config: { ...(trigger?.config ?? {}), at_ms: start.getTime() },
            },
          })
          return
        }
      }
    },
    [tasks, workspaceId],
  )

  // Optimistically patch the cached item's date so the `events` memo agrees with
  // FullCalendar's DOM move — prevents a stale-data flash before the refetch lands.
  // Returns a per-item rollback: re-reads the CURRENT cache on failure and restores
  // only the single changed row, so concurrent updates to other rows are never lost.
  const patchCacheDate = useCallback(
    (ext: CalendarEventExtProps, start: Date): (() => void) => {
      if (ext.kind === 'milestone') {
        const key = milestonesQueryKeys.list(workspaceId)
        // Capture only the single prior item for a targeted rollback.
        const prevItem = queryClient
          .getQueryData<Milestone[]>(key)
          ?.find((m) => m.id === ext.milestoneId)
        queryClient.setQueryData<Milestone[]>(key, (current) =>
          current?.map((m) =>
            m.id === ext.milestoneId ? { ...m, due_date: formatLocalDate(start) } : m,
          ),
        )
        return () => {
          if (prevItem) {
            queryClient.setQueryData<Milestone[]>(key, (current) =>
              current?.map((m) => (m.id === ext.milestoneId ? prevItem : m)),
            )
          }
        }
      }
      const key = tasksQueryKeys.list({ workspace_id: workspaceId })
      // Capture only the single prior task for a targeted rollback.
      const prevItem = queryClient
        .getQueryData<Task[]>(key)
        ?.find((t) => t.id === ext.taskId)
      queryClient.setQueryData<Task[]>(key, (current) =>
        current?.map((t) => {
          if (t.id !== ext.taskId) return t
          if (ext.kind === 'task-due') return { ...t, due: start.toISOString() }
          return t.trigger
            ? {
                ...t,
                trigger: {
                  ...t.trigger,
                  config: { ...(t.trigger.config ?? {}), at_ms: start.getTime() },
                },
              }
            : t
        }),
      )
      return () => {
        if (prevItem) {
          queryClient.setQueryData<Task[]>(key, (current) =>
            current?.map((t) => (t.id === ext.taskId ? prevItem : t)),
          )
        }
      }
    },
    [queryClient, workspaceId],
  )

  // Optimistic reschedule: cancel in-flight refetches → patch cache → persist →
  // invalidate; per-item rollback on failure.
  const runReschedule = useCallback(
    async (ext: CalendarEventExtProps, start: Date): Promise<void> => {
      // Cancel any in-flight query for the relevant key so a concurrent refetch
      // cannot overwrite the optimistic write we are about to make.
      if (ext.kind === 'milestone') {
        await queryClient.cancelQueries({
          queryKey: milestonesQueryKeys.list(workspaceId),
        })
      } else {
        await queryClient.cancelQueries({
          queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
        })
      }
      const rollback = patchCacheDate(ext, start)
      try {
        await persistReschedule(ext, start)
        invalidate()
      } catch (err) {
        rollback()
        throw err
      }
    },
    [queryClient, workspaceId, patchCacheDate, persistReschedule, invalidate],
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
      void runReschedule(ext, newStart).then(
        () => {
          addToast({
            message: `Rescheduled to ${formatShort(newStart)}`,
            variant: 'success',
            duration: 5000,
            action: oldStart
              ? {
                  label: 'Undo',
                  onClick: () => {
                    void runReschedule(ext, oldStart).catch((err) => {
                      console.error('[calendar] undo reschedule failed', { kind: ext.kind, err })
                      addToast({ message: "Couldn't undo — please try again", variant: 'error' })
                    })
                  },
                }
              : undefined,
          })
        },
        (err) => {
          arg.revert() // optimistic cache already rolled back inside runReschedule
          console.error('[calendar] reschedule failed', { kind: ext.kind, err })
          addToast({ message: "Couldn't reschedule — please try again", variant: 'error' })
        },
      )
    },
    [runReschedule, addToast],
  )

  const handleEventClick = useCallback(
    (arg: EventClickArg) => {
      const raw = arg.jsEvent?.target as HTMLElement | null
      triggerElRef.current =
        (raw?.closest('[tabindex]') as HTMLElement | null) ?? raw ?? null
      const ext = arg.event.extendedProps as CalendarEventExtProps
      if (ext.kind === 'milestone') {
        const m = milestones.find((x) => x.id === ext.milestoneId)
        if (m) setMilestoneTarget({ id: m.id, name: m.name, due_date: m.due_date ?? null })
        else console.warn('[calendar] milestone event has no backing milestone', ext)
        return
      }
      // task-due | task-fire → taskId is guaranteed by the discriminated union.
      const t = tasks.find((x) => x.id === ext.taskId)
      if (t) setSelectedTask(t)
      else console.warn('[calendar] task event has no backing task', ext)
    },
    [tasks, milestones],
  )

  // Open the create slide-over prefilled with a date (all-day → midnight; timed → slot).
  // Store the nearest focusable ancestor of the trigger so restoreFocus() can actually
  // focus it (WCAG 2.4.3 — raw chip divs have no tabindex, their FC harness does).
  const openCreateAt = useCallback((target: HTMLElement | null, date: Date, allDay: boolean) => {
    triggerElRef.current =
      (target?.closest('[tabindex]') as HTMLElement | null) ?? target ?? null
    setInitialDue(allDay ? `${formatLocalDate(date)}T00:00` : toDatetimeLocal(date))
    setCreateOpen(true)
  }, [])

  const handleDateClick = useCallback(
    (arg: DateClickArg) =>
      openCreateAt((arg.jsEvent?.target as HTMLElement) ?? null, arg.date, arg.allDay),
    [openCreateAt],
  )

  const handleDateSelect = useCallback(
    (arg: DateSelectArg) =>
      openCreateAt((arg.jsEvent?.target as HTMLElement) ?? null, arg.start, arg.allDay),
    [openCreateAt],
  )

  // WCAG 2.1.1 keyboard-equivalent path (see CalendarToolbar.handleNewTask):
  // the toolbar passes the calendar's currently-VIEWED date so a keyboard user
  // who cannot pointer-click a day cell still gets it pre-filled — matching
  // the all-day format `openCreateAt` uses for a dateClick on an all-day cell.
  const handleNewTask = useCallback((date?: Date) => {
    triggerElRef.current = null
    setInitialDue(date ? `${formatLocalDate(date)}T00:00` : undefined)
    setCreateOpen(true)
  }, [])

  return (
    <div className="flex flex-col h-full min-h-0 overflow-x-hidden bg-[var(--color-surface-0)] text-[var(--color-secondary)]">
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
