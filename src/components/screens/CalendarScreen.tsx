// Workspace Calendar — Outlook-style FullCalendar v6 surface.
// Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2);
// SP-39 (side-panel wave 3): docks on Week by default; Day/Week/Month are
// three equal in-panel choices; Month renders the compact CalendarMonthGrid
// in place — it never routes away to a wider surface. Agenda is dropped.
//
// The grid ALWAYS renders (the empty-state bug is gone); supports Month/Week/Day,
// drag-to-reschedule + a keyboard path, and click/slot-select to create. This
// file is the integration hub: it maps tasks → events (pure fn), hosts the
// wrapper + toolbar, and owns the handlers/mutations (optimistic move handled
// by FullCalendar; revert + undo + toast here). It also owns the optimistic
// query-cache patch + per-item rollback layer that keeps the TanStack Query
// cache in sync with FullCalendar's DOM move — cancelling in-flight queries
// before each patch and rolling back only the changed row on failure so
// concurrent updates to other rows are never lost.
//
// Month engine note: FullCalendar stays mounted as the navigation/date engine
// in EVERY view — while Month is active its DOM is hidden (visibility + inert)
// and CalendarMonthGrid renders from FullCalendar's own reported range
// (datesSet). The toolbar therefore keeps driving one calendar API unchanged.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type FullCalendar from '@fullcalendar/react'
import type { EventClickArg, EventDropArg, DateSelectArg } from '@fullcalendar/core'
import type { DateClickArg } from '@fullcalendar/interaction'
import {
  fetchTasks,
  fetchAgents,
  buildTaskAssigneeItems,
  updateTask,
  tasksQueryKeys,
  type Task,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'
import { mapToCalendarEvents } from '@/lib/calendar/eventMapping'
import { useOccurrences } from '@/lib/calendar/useOccurrences'
import { FullCalendarView } from '@/components/calendar/FullCalendarView'
import { CalendarToolbar } from '@/components/calendar/CalendarToolbar'
import { CalendarMonthGrid } from '@/components/calendar/CalendarMonthGrid'
import { AGENT_FILTER_ALL, filterEventsByAgent } from '@/components/calendar/calendarAgentFilter'
import type {
  CalendarViewName,
  CalendarEventExtProps,
} from '@/components/calendar/types'
import { CalendarEventSlideOver } from '@/components/calendar/CalendarEventSlideOver'
import { TaskDetailSlideOver } from '@/components/workspaces/TaskDetailSlideOver'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { cn } from '@/lib/utils'

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
    refetch: refetchTasks,
  } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId }),
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
    () => mapToCalendarEvents(tasks, occurrenceSets),
    [tasks, occurrenceSets],
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
  // chips too.
  const filteredEvents = useMemo(
    () => filterEventsByAgent(events, tasks, agentFilter),
    [events, tasks, agentFilter],
  )

  const isLoading = tasksLoading
  // D9 fix: FR-016/I-2's "degrade gracefully" contract is still honored for a
  // PARTIAL failure (e.g. occurrences failed but tasks loaded — there is real
  // data worth showing, so the grid renders it and a toast is enough). What
  // it never intended is a TOTAL failure silently reading as "no scheduled
  // items" — the UAT-reported bug. `isBlockingError` is true only when there
  // is genuinely nothing to render AND at least one query is the reason why
  // (as opposed to a genuinely empty, healthy workspace) — that is exactly
  // the case the Graph/Team/Board tabs already treat as a hard error state.
  const hasQueryError = tasksError
  const isTrulyEmpty = !isLoading && filteredEvents.length === 0
  const isEmpty = isTrulyEmpty && !hasQueryError
  const isBlockingError = isTrulyEmpty && hasQueryError

  // Degrade on query failure (FR-016, I-2) — non-blocking toast in the
  // PARTIAL case; see isBlockingError above for the total-failure case, which
  // additionally gets a real blocking state in place of the grid below.
  useEffect(() => {
    if (tasksError) addToast({ message: "Couldn't load tasks", variant: 'error' })
  }, [tasksError, addToast])

  // ── Toolbar state (driven by FullCalendar's datesSet) ─────────────────────────
  // SP-39: Calendar docks on Week by default — the initial view is Week, both
  // here (switcher state) and in FullCalendarView's initialView below.
  const [currentView, setCurrentView] = useState<CalendarViewName>('timeGridWeek')
  const [title, setTitle] = useState('')
  // The visible range WHILE Month is active (null in Week/Day). CalendarMonthGrid
  // renders only from this — never from a stale Week/Day range during the brief
  // window between the switcher click and FullCalendar's next datesSet.
  const [monthRange, setMonthRange] = useState<{ start: Date; end: Date } | null>(null)

  const handleDatesSet = useCallback(
    (nextTitle: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => {
      setTitle(nextTitle)
      setCurrentView(view)
      setActiveRange({ start: activeStart, end: activeEnd })
      setMonthRange(view === 'dayGridMonth' ? { start: activeStart, end: activeEnd } : null)
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
  // The clicked occurrence's join key (ADR-050 RD8 / task-run-history-spec.md
  // §4.1/§4.3), threaded to CalendarEventSlideOver's `selectedOccurrenceMs` so
  // it can re-point the Run status/Result/Open-in-Chat sections at THAT
  // occurrence's own run instead of the task-level mirror. Only ever set from
  // an individual `task-occurrence` chip click — see handleEventClick: a
  // `task-occurrence-agg` (bucket) click intentionally leaves this unset
  // (undefined), since selectedOccurrenceMs is matched by the slide-over as
  // an EXACT `run.occurrence_ms`, and a bucket's `day_start_ms` would never
  // match a real run's occurrence_ms — passing it here would silently hide
  // the Result/Open-in-Chat sections instead of the day's run mini-list.
  const [selectedOccurrenceMs, setSelectedOccurrenceMs] = useState<number | undefined>(undefined)
  // The clicked bucket's own day span (ADR-050 RD8 / task-run-history-spec.md
  // §4.1/§4.3), threaded to CalendarEventSlideOver's `selectedBucketDayRange`
  // so it can day-scope the slide-over's run mini-list to just that bucket's
  // day instead of the task's entire run history. Only ever set from a
  // `task-occurrence-agg` (bucket) chip click — an individual `task-occurrence`
  // click leaves this `null`, the mirror image of how `selectedOccurrenceMs`
  // is only set from an individual instant click (see above). `endMs` comes
  // straight off the wire (`DayBucket.day_end_ms` → `ext.dayEndMs`) — the
  // server's own DST-aware civil-next-midnight boundary for this bucket
  // (`civilDayNext`, pkg/gateway/task_occurrences.go), the SAME window
  // `populateBucketRunCounts` uses to tally `run_counts`. Delta-review HIGH
  // fix: a client-recomputed fixed dayStartMs+24h span diverges from that
  // DST-aware window on a transition day, disagreeing with the aggregate
  // `run_counts` and the drilled-in run list — carrying the boundary on the
  // wire means the client never recomputes it (Explicit Non-Behaviors: no
  // client-side RRULE/day math).
  const [selectedBucketDayRange, setSelectedBucketDayRange] = useState<{
    startMs: number
    endMs: number
  } | null>(null)
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)

  const invalidate = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
    })
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
  // `ext` is the discriminated CalendarEventExtProps union; the switch narrows
  // taskId with no defensive checks. NOT exhaustive over `kind` (a
  // pre-existing gap, not introduced here): task-occurrence/-agg/-more chips are
  // never draggable (eventMapping.ts sets `editable: false` on all three), so
  // `eventDrop` can never fire for any of them — an implicit
  // fall-through-and-return is safe today.
  const persistReschedule = useCallback(
    async (ext: CalendarEventExtProps, start: Date): Promise<void> => {
      switch (ext.kind) {
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
      await queryClient.cancelQueries({
        queryKey: tasksQueryKeys.list({ workspace_id: workspaceId }),
      })
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
        case 'task-occurrence':
        case 'task-occurrence-agg': {
          const t = tasks.find((x) => x.id === ext.taskId)
          if (t) {
            setEventSlideOverTask(t)
            // Individual instant → its exact occurrence_ms (matches a run's
            // occurrence_ms 1:1). Bucket → leave unset; day_start_ms is not
            // a real occurrence instant and would never match a run (see the
            // state declaration above for why passing it here would be wrong).
            setSelectedOccurrenceMs(ext.kind === 'task-occurrence' ? ext.occurrenceMs : undefined)
            // Mirror image: a bucket click threads its day span so the
            // slide-over can day-scope its run mini-list; an individual
            // instant click leaves this null (it re-points at ONE run, not a
            // day's worth).
            setSelectedBucketDayRange(
              ext.kind === 'task-occurrence-agg'
                ? { startMs: ext.dayStartMs, endMs: ext.dayEndMs }
                : null,
            )
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
    [tasks],
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
    setSelectedOccurrenceMs(undefined)
    setSelectedBucketDayRange(null)
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
    setSelectedOccurrenceMs(undefined)
    setSelectedBucketDayRange(null)
    setEventSlideOverInitialDate(date ? withDefaultHour(date, 9) : undefined)
    setEventSlideOverOpen(true)
  }, [])

  // Month grid (SP-39) — a compact-grid day-cell click is the pointer-parity of
  // an all-day cell click: create pre-filled with that day at the same 9am
  // default `openCreateAt` uses. Keyboard parity stays the toolbar's "New task"
  // button (see CalendarToolbar's WCAG 2.1.1 note) — the same stance the
  // FC-rendered views already take.
  const handleMonthDayClick = useCallback(
    (date: Date) => openCreateAt(null, date, true),
    [openCreateAt],
  )

  // SP-39 — Month renders the compact grid in place; FullCalendar stays mounted
  // as the navigation/date engine in every view (hidden while Month is active).
  const isMonth = currentView === 'dayGridMonth'

  return (
    <div className="relative flex h-full min-h-0 min-w-0 flex-col overflow-x-hidden bg-[var(--color-surface-0)] text-[var(--color-secondary)]">
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

      <div
        className="relative flex-1 min-h-0 min-w-0 p-[var(--space-2-5)]"
        data-testid="calendar-grid"
      >
        {isBlockingError ? (
          <QueryErrorState
            layout="fill"
            message="Couldn't load your calendar. Check your connection and try again."
            onRetry={() => {
              void refetchTasks()
            }}
            testId="calendar-error"
          />
        ) : (
          <>
            {/* FullCalendar keeps rendering (single navigation engine — the
                toolbar drives this one API in all three views). While Month is
                active its DOM is hidden (`visibility`, NOT display:none, so
                its layout measurements stay valid for the switch back to
                Week/Day) and inert, and CalendarMonthGrid renders in its
                place from FullCalendar's own reported range. */}
            <div
              className={cn('h-full min-h-0', isMonth && 'invisible pointer-events-none')}
              aria-hidden={isMonth || undefined}
              inert={isMonth || undefined}
            >
              <FullCalendarView
                events={filteredEvents}
                calendarRef={calendarRef}
                initialView="timeGridWeek"
                isLoading={isLoading}
                isEmpty={isEmpty}
                onEventDrop={handleEventDrop}
                onEventClick={handleEventClick}
                onDateClick={handleDateClick}
                onDateSelect={handleDateSelect}
                onDatesSet={handleDatesSet}
              />
            </div>
            {isMonth && monthRange && (
              <div className="absolute inset-[var(--space-2-5)] flex min-h-0 flex-col">
                <CalendarMonthGrid
                  rangeStart={monthRange.start}
                  rangeEnd={monthRange.end}
                  events={filteredEvents}
                  onDayClick={handleMonthDayClick}
                  isLoading={isLoading}
                  isEmpty={isEmpty}
                />
              </div>
            )}
          </>
        )}
      </div>

      <CalendarEventSlideOver
        open={eventSlideOverOpen}
        onOpenChange={(open) => {
          setEventSlideOverOpen(open)
          if (!open) {
            setEventSlideOverTask(null)
            setSelectedOccurrenceMs(undefined)
            setSelectedBucketDayRange(null)
            invalidate()
            restoreFocus()
          }
        }}
        workspaceId={workspaceId}
        task={eventSlideOverTask}
        initialDate={eventSlideOverInitialDate}
        selectedOccurrenceMs={selectedOccurrenceMs}
        selectedBucketDayRange={selectedBucketDayRange}
      />

      <TaskDetailSlideOver
        task={selectedTask}
        onClose={() => {
          setSelectedTask(null)
          restoreFocus()
        }}
      />
    </div>
  )
}
