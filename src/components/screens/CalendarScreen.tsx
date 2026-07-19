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
  fetchAgents,
  buildTaskAssigneeItems,
  updateTask,
  updateMilestone,
  tasksQueryKeys,
  milestonesQueryKeys,
  type Task,
  type Milestone,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'
import { mapToCalendarEvents, formatLocalDate } from '@/lib/calendar/eventMapping'
import { useOccurrences } from '@/lib/calendar/useOccurrences'
import { FullCalendarView } from '@/components/calendar/FullCalendarView'
import { CalendarToolbar } from '@/components/calendar/CalendarToolbar'
import { MilestoneDatePopover } from '@/components/calendar/MilestoneDatePopover'
import { AGENT_FILTER_ALL, filterEventsByAgent } from '@/components/calendar/calendarAgentFilter'
import type {
  CalendarViewName,
  CalendarEventExtProps,
  MilestoneTarget,
} from '@/components/calendar/types'
import { CalendarEventSlideOver } from '@/components/calendar/CalendarEventSlideOver'
import { TaskDetailSlideOver } from '@/components/workspaces/TaskDetailSlideOver'

interface CalendarScreenProps {
  workspaceId: string
}

/** Short human label for a reschedule toast ("Jun 23"). */
function formatShort(d: Date): string {
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

/** Same calendar day as `d`, with the time-of-day set to `hour`:00 local. Used
 *  to give an all-day day-cell click a sensible default event time (US-1). */
function withDefaultHour(d: Date, hour: number): Date {
  const nd = new Date(d)
  nd.setHours(hour, 0, 0, 0)
  return nd
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

  // ── Recurring-task occurrences (Calendar Recurrence Redesign, US-2, FR-008) ─
  // Keyed to FullCalendar's own visible range, reported via onDatesSet below.
  // `activeRange` starts null (no range known yet — before FullCalendar's
  // first datesSet fires) so the query stays disabled until a real range
  // exists; the placeholder Dates below are inert (enabled:false skips them).
  const [activeRange, setActiveRange] = useState<{ start: Date; end: Date } | null>(null)
  const {
    data: occurrenceSets = [],
    isError: occurrencesError,
  } = useOccurrences({
    workspaceId,
    activeStart: activeRange?.start ?? new Date(0),
    activeEnd: activeRange?.end ?? new Date(1),
    enabled: !!activeRange && !!workspaceId,
  })

  // Degrade on occurrences failure (FR-017/Behavioral Contract "Error flows")
  // — non-blocking toast, due/fire chips still render (occurrenceSets simply
  // stays whatever it last successfully was, or [] on first-load failure).
  useEffect(() => {
    if (occurrencesError) {
      addToast({ message: "Couldn't load recurring occurrences", variant: 'error' })
    }
  }, [occurrencesError, addToast])

  const events = useMemo(
    () => mapToCalendarEvents(tasks, milestones, occurrenceSets),
    [tasks, milestones, occurrenceSets],
  )

  // ── Agent filter (FR-015 / US-4) ─────────────────────────────────────────
  // Client-side only — SC-004 requires ZERO additional network requests for
  // the filter itself, so `filterEventsByAgent` is a pure in-memory pass over
  // `events` + the already-fetched `tasks` (no new query). The roster reuses
  // the identical workspace-team-scoping + degrade convention as the task
  // assignee picker (CreateTaskSlideOver/TaskDetailPanel): on a failed
  // team-set fetch, `useWorkspaceTeamIds` returns `teamIds: undefined`,
  // `buildTaskAssigneeItems` falls back to the FULL unscoped agent list (never
  // an empty/broken dropdown), and `teamError` drives the toolbar's "Team
  // list unavailable — showing all agents" notice (Edge Cases).
  const [agentFilter, setAgentFilter] = useState<string>(AGENT_FILTER_ALL)
  const { data: allAgents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })
  const { teamIds, isError: teamError } = useWorkspaceTeamIds(workspaceId)
  const agentOptions = useMemo(
    () =>
      buildTaskAssigneeItems(allAgents, {
        teamScope: teamIds ? { kind: 'scoped', ids: teamIds } : { kind: 'unscoped' },
      }),
    [allAgents, teamIds],
  )
  // Keys off each event's underlying TASK `agent_id` (via `tasks`, already
  // fetched above) rather than per-chip data — see calendarAgentFilter.ts for
  // why this transparently covers Wave-2's recurring occurrence/aggregated
  // chips too, with milestones exempt (US-4.3).
  const filteredEvents = useMemo(
    () => filterEventsByAgent(events, tasks, agentFilter),
    [events, tasks, agentFilter],
  )

  const isLoading = tasksLoading || milestonesLoading
  const isEmpty = !isLoading && filteredEvents.length === 0

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

  const handleDatesSet = useCallback(
    (nextTitle: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => {
      setTitle(nextTitle)
      setCurrentView(view)
      setActiveRange({ start: activeStart, end: activeEnd })
    },
    [],
  )

  const handleViewChange = useCallback((view: CalendarViewName) => setCurrentView(view), [])

  // ── Slide-over / popover state ───────────────────────────────────────────────
  // CalendarEventSlideOver is a single component covering BOTH create (US-1)
  // and recurring/legacy series edit (US-2/US-5) — `eventSlideOverTask` null
  // means create mode, non-null means edit mode. It fully replaces the
  // generic CreateTaskSlideOver on this screen: per the Behavioral Contract,
  // a day/slot click now always opens the calendar-specific event panel.
  const [eventSlideOverOpen, setEventSlideOverOpen] = useState(false)
  const [eventSlideOverTask, setEventSlideOverTask] = useState<Task | null>(null)
  const [eventSlideOverInitialDate, setEventSlideOverInitialDate] = useState<Date | undefined>(undefined)
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [milestoneTarget, setMilestoneTarget] = useState<MilestoneTarget | null>(null)

  const invalidate = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    })
    void queryClient.invalidateQueries({ queryKey: milestonesQueryKeys.list(workspaceId) })
    void queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
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

  // Chip-click routing (Calendar Recurrence Redesign, FR-001/FR-012, US-2
  // Acceptance Scenarios 5–6): occurrence/aggregated chips (recurring series,
  // legacy chips included — they render as 'task-occurrence'/'-agg' too, see
  // eventMapping) open the calendar event slide-over in edit mode; due/fire
  // chips keep opening TODAY'S existing task detail panel, unchanged. The
  // truncation marker chip is non-interactive (no click action).
  const handleEventClick = useCallback(
    (arg: EventClickArg) => {
      const raw = arg.jsEvent?.target as HTMLElement | null
      triggerElRef.current =
        (raw?.closest('[tabindex]') as HTMLElement | null) ?? raw ?? null
      const ext = arg.event.extendedProps as CalendarEventExtProps
      switch (ext.kind) {
        case 'milestone': {
          const m = milestones.find((x) => x.id === ext.milestoneId)
          if (m) setMilestoneTarget({ id: m.id, name: m.name, due_date: m.due_date ?? null })
          else console.warn('[calendar] milestone event has no backing milestone', ext)
          return
        }
        case 'task-occurrence':
        case 'task-occurrence-agg': {
          const t = tasks.find((x) => x.id === ext.taskId)
          if (t) {
            setEventSlideOverTask(t)
            setEventSlideOverOpen(true)
          } else {
            console.warn('[calendar] occurrence event has no backing task', ext)
          }
          return
        }
        case 'task-occurrence-more':
          // Non-interactive truncation marker — no click action.
          return
        case 'task-due':
        case 'task-fire': {
          const t = tasks.find((x) => x.id === ext.taskId)
          if (t) setSelectedTask(t)
          else console.warn('[calendar] task event has no backing task', ext)
          return
        }
      }
    },
    [tasks, milestones],
  )

  // Open the calendar event slide-over in create mode, prefilled with a date
  // (all-day day-cell click → 9am default; timed slot click → the exact
  // slot). Store the nearest focusable ancestor of the trigger so
  // restoreFocus() can actually focus it (WCAG 2.4.3 — raw chip divs have no
  // tabindex, their FC harness does).
  const openCreateAt = useCallback((target: HTMLElement | null, date: Date, allDay: boolean) => {
    triggerElRef.current =
      (target?.closest('[tabindex]') as HTMLElement | null) ?? target ?? null
    setEventSlideOverTask(null)
    setEventSlideOverInitialDate(allDay ? withDefaultHour(date, 9) : date)
    setEventSlideOverOpen(true)
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
    setEventSlideOverTask(null)
    setEventSlideOverInitialDate(date ? withDefaultHour(date, 9) : undefined)
    setEventSlideOverOpen(true)
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
          agentFilter={agentFilter}
          onAgentFilterChange={setAgentFilter}
          agentOptions={agentOptions}
          agentRosterError={teamError}
        />
      </div>

      <div className="flex-1 min-h-0 min-w-0 p-3" data-testid="calendar-grid">
        <FullCalendarView
          events={filteredEvents}
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

      <CalendarEventSlideOver
        open={eventSlideOverOpen}
        onOpenChange={(open) => {
          setEventSlideOverOpen(open)
          if (!open) {
            setEventSlideOverTask(null)
            invalidate()
            restoreFocus()
          }
        }}
        workspaceId={workspaceId}
        task={eventSlideOverTask}
        initialDate={eventSlideOverInitialDate}
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
