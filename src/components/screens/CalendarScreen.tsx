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
import type { EventClickArg, EventDropArg, DateSelectArg, EventInput } from '@fullcalendar/core'
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
import { QueryErrorState } from '@/components/shared/QueryErrorState'

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

  const {
    data: milestones = [],
    isLoading: milestonesLoading,
    isError: milestonesError,
    refetch: refetchMilestones,
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
  // D9 fix: FR-016/I-2's "degrade gracefully" contract is still honored for a
  // PARTIAL failure (e.g. milestones failed but tasks loaded — there is real
  // data worth showing, so the grid renders it and a toast is enough). What
  // it never intended is a TOTAL failure silently reading as "no scheduled
  // items" — the UAT-reported bug. `isBlockingError` is true only when there
  // is genuinely nothing to render AND at least one query is the reason why
  // (as opposed to a genuinely empty, healthy workspace) — that is exactly
  // the case the Graph/Team/Board tabs already treat as a hard error state.
  const hasQueryError = tasksError || milestonesError
  const isTrulyEmpty = !isLoading && filteredEvents.length === 0
  const isEmpty = isTrulyEmpty && !hasQueryError
  const isBlockingError = isTrulyEmpty && hasQueryError

  // Degrade on query failure (FR-016, I-2) — non-blocking toast in the
  // PARTIAL case; see isBlockingError above for the total-failure case, which
  // additionally gets a real blocking state in place of the grid below.
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

  // ── Agenda "now" marker (live divider, listWeek ONLY) ─────────────────────────
  // `nowIndicator={true}` (FullCalendarView.tsx) already renders a correct, live
  // Google-Calendar-style "now" line in Week/Day via FullCalendar's own timeGrid
  // machinery — untouched here. `@fullcalendar/list` (Agenda) has no time axis and
  // structurally cannot render that built-in indicator, so a single synthetic
  // `kind: 'now-marker'` EventInput is injected instead, ONLY while Agenda is the
  // active view. `nowTick` only needs to advance roughly every 30-60s (no
  // per-second precision required), and the interval is scoped to Agenda being
  // open — no background ticking (and no timer leak) in Month/Week/Day.
  // `Date.now()`-driven; resynced the INSTANT Agenda becomes active (not left to
  // wait for the first 30s tick — switching in from a view that had been open for
  // minutes would otherwise show a stale "now" position until the first tick).
  const [nowTick, setNowTick] = useState<number>(() => Date.now())
  useEffect(() => {
    if (currentView !== 'listWeek') return
    setNowTick(Date.now())
    const id = window.setInterval(() => setNowTick(Date.now()), 30_000)
    return () => window.clearInterval(id)
  }, [currentView])

  // Time label pre-formatted here (not read from FullCalendar's arg.timeText at
  // render time — see the `now-marker` variant's doc comment in types.ts for why
  // that's always empty in list view). Matches FullCalendarView's own
  // eventTimeFormat/slotLabelFormat options so the label is visually consistent
  // with every other view's time formatting.
  const NOW_MARKER_TIME_FORMAT = useMemo(
    () => new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit', hour12: true }),
    [],
  )

  // Only materialize the marker when it would actually land inside FullCalendar's
  // OWN reported visible range (`activeRange`, half-open — `end` exclusive, same
  // convention as `onDatesSet`) — never in Month/Week/Day, never a stale marker
  // outside the currently-viewed Agenda week, and only when there's at least one
  // real item for it to sit among (a lone divider on an otherwise-empty Agenda
  // has nothing to divide and would render simultaneously with the "No scheduled
  // items" empty-state hint, which reads as contradictory/broken).
  const nowMarkerEvent = useMemo<EventInput | null>(() => {
    if (currentView !== 'listWeek' || !activeRange || filteredEvents.length === 0) return null
    if (nowTick < activeRange.start.getTime() || nowTick >= activeRange.end.getTime()) return null
    return {
      id: 'now-marker',
      start: new Date(nowTick),
      // A minimal (1s) but STRICTLY GREATER-than-start `end` — NOT equal.
      // WITHOUT any explicit `end`, FullCalendar assigns its own
      // `defaultTimedEventDuration` (1 hour) to a timed event, which late at
      // night pushed the marker's span past midnight; Agenda's list view then
      // renders a segment of any day-spanning event on EVERY day it touches
      // — the marker showed twice, once under today and once under
      // tomorrow. The first fix (end === start) was ALSO wrong: FullCalendar
      // itself treats `end <= start` as "no end provided" (@fullcalendar/
      // core's parseSingle: `if (startMarker && endMarker <= startMarker)
      // endMarker = null`) and falls through to the SAME 1-hour default —
      // confirmed live, still duplicated. `end` must be strictly after
      // `start`; 1 second is enough to satisfy that check while staying
      // visually/semantically instantaneous (NowMarkerLine never renders
      // anything duration-dependent).
      end: new Date(nowTick + 1000),
      allDay: false,
      title: '',
      editable: false,
      extendedProps: { kind: 'now-marker', timeLabel: NOW_MARKER_TIME_FORMAT.format(nowTick) },
    }
  }, [currentView, activeRange, nowTick, filteredEvents.length, NOW_MARKER_TIME_FORMAT])

  // Appended AFTER the agent-filter step (never before) — it is not a task and
  // must never be dropped by `filterEventsByAgent`. Kept as a SEPARATE array from
  // `filteredEvents` (rather than mutating it) so `isEmpty` above still reflects
  // only real data — the marker is a visual divider, not a schedulable item, and
  // must never mask a genuinely empty Agenda view. FullCalendar's list view sorts
  // by `start` automatically, so the marker lands in the right chronological slot
  // among the day's real events with no manual sorting needed.
  const calendarEvents = useMemo(
    () => (nowMarkerEvent ? [...filteredEvents, nowMarkerEvent] : filteredEvents),
    [filteredEvents, nowMarkerEvent],
  )

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
  // `ext` is the discriminated CalendarEventExtProps union; the switch narrows
  // taskId/milestoneId with no defensive checks. NOT exhaustive over `kind` (a
  // pre-existing gap, not introduced here): task-occurrence/-agg/-more chips are
  // never draggable (eventMapping.ts sets `editable: false` on all three), and
  // now-marker is likewise `editable: false` (below), so `eventDrop` can never
  // fire for any of them — an implicit fall-through-and-return is safe today.
  // The `now-marker` case is still spelled out explicitly (mirrors the same
  // defensive style already used for it in `patchCacheDate` and
  // `handleEventClick`, both below) rather than relying on that fall-through.
  const persistReschedule = useCallback(
    async (ext: CalendarEventExtProps, start: Date): Promise<void> => {
      switch (ext.kind) {
        case 'now-marker':
          return
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
      // The Agenda "now" divider is `editable: false` (see the marker event
      // built above) — FullCalendar never fires `eventDrop`/reschedule for
      // it, so this branch never runs in practice. Guarded here only so the
      // rest of this function can safely narrow `ext` to the taskId-bearing
      // members below (TypeScript exhaustiveness — `now-marker` has no
      // `taskId`).
      if (ext.kind === 'now-marker') return () => {}
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
        case 'now-marker':
          // Non-interactive Agenda "now" divider — no click action.
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

  return (
    <div className="absolute inset-0 flex flex-col overflow-x-hidden bg-[var(--color-surface-0)] text-[var(--color-secondary)]">
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
        {isBlockingError ? (
          <QueryErrorState
            layout="fill"
            message="Couldn't load your calendar. Check your connection and try again."
            onRetry={() => {
              void refetchTasks()
              void refetchMilestones()
            }}
            testId="calendar-error"
          />
        ) : (
          <FullCalendarView
            events={calendarEvents}
            calendarRef={calendarRef}
            isLoading={isLoading}
            isEmpty={isEmpty}
            onEventDrop={handleEventDrop}
            onEventClick={handleEventClick}
            onDateClick={handleDateClick}
            onDateSelect={handleDateSelect}
            onDatesSet={handleDatesSet}
          />
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
