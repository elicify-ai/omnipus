/**
 * CalendarEventSlideOver — the calendar-specific create/edit Sheet for
 * recurring tasks and calendar-created one-time tasks (US-1, US-2, US-5).
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 *   User Story 1 (create without cron), User Story 2 (edit a recurring
 *   series), User Story 5 (legacy replace); FR-001/005/006/012/013/019/020/
 *   021/022/024; Timezone Semantics §1.
 *
 * Open/routing contract (owned by CalendarScreen, see that file):
 *   - `task === null` → CREATE mode, pre-filled from `initialDate`.
 *   - `task !== null` → EDIT mode (series edit). Reachable only from a
 *     recurring occurrence/aggregated chip (`task-occurrence` /
 *     `task-occurrence-agg`) or a legacy (`cron_expr`/`every_ms`) task's
 *     chip — due chips and `once` fire chips keep opening the existing
 *     TaskDetailSlideOver, unchanged (US-2 Acceptance Scenario 6).
 *
 * FR-024 anchor semantics (edit mode, already-RRULE trigger):
 *   - Title/Agent-only edit (Repeat + Date&time untouched) → the saved
 *     trigger is the ORIGINAL trigger object, byte-identical (never
 *     re-serialized) — `buildTriggerForSave` returns `task.trigger` verbatim.
 *   - The Date & time field touched → the rule re-anchors at exactly the
 *     date/time the operator picked.
 *   - Only the Repeat section touched (Date & time left alone) → the rule
 *     re-anchors at "now" (DTSTART = save instant), which — once expanded
 *     server-side — lands on the new rule's next occurrence ≥ now without
 *     this component computing any RRULE occurrence math itself (Explicit
 *     Non-Behaviors: the server is the sole occurrence authority).
 *   - Either touch restarts COUNT (a fresh DTSTART restarts RRULE's
 *     occurrence-position counting) and is announced inline before saving
 *     (`data-testid="reanchor-notice"`).
 *   Legacy (`cron_expr`/`every_ms`) tasks are exempt from byte-identical
 *   preservation — US-5/D8: every legacy save replaces outright with a
 *   fresh RRULE anchored like any new rule.
 *
 * FR-013/US-5 (legacy): the Repeat section ALWAYS starts fresh
 * (`{kind:'none'}`) for a legacy trigger — never a translated/parsed
 * cron_expr or every_ms. No cron string is ever rendered anywhere in this
 * component (D8/D9).
 *
 * FR-020 (optional, implemented): a small "Upcoming" preview list for an
 * already-RRULE series, sourced from the SAME `useOccurrences` endpoint the
 * calendar grid uses (never client-computed) over a short forward window.
 * The same query also backs the legacy note's "next run" time.
 */

import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { FormError } from '@/components/ui/FormError'
import { SmartSelect } from '@/components/ui/smart-select'
import { DateTimePicker } from '@/components/ui/date-time-picker'
import {
  createTask,
  updateTask,
  fetchAgents,
  buildTaskAssigneeItems,
  tasksQueryKeys,
  getErrorMessage,
} from '@/lib/api'
import type { Task, TaskTrigger, TaskCreateRequest, TaskUpdateRequest } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'
import {
  RecurrenceEditor,
  compileRecurrence,
  parseRruleString,
  validateRecurrenceState,
  type RecurrenceValue,
} from '@/components/calendar/RecurrenceEditor'
import { useOccurrences } from '@/lib/calendar/useOccurrences'
import type { TaskOccurrenceSet } from '@/lib/api/generated/openapi-types'

export interface CalendarEventSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
  /** null = create mode; non-null = series-edit mode. */
  task: Task | null
  /** Create-mode only: pre-fills the Date & time field (e.g. from a day-cell click). */
  initialDate?: Date
}

// ── Trigger classification (local — no client cron/RRULE math) ─────────────

function isRruleTrigger(trigger?: TaskTrigger | null): trigger is TaskTrigger {
  return (
    trigger?.type === 'recurring' &&
    typeof trigger.config?.rrule === 'string' &&
    typeof trigger.config?.dtstart_ms === 'number'
  )
}

function isLegacyTrigger(trigger?: TaskTrigger | null): boolean {
  if (!trigger) return false
  if (trigger.type === 'every') return true
  return trigger.type === 'recurring' && typeof trigger.config?.cron_expr === 'string' && !!trigger.config.cron_expr
}

// ── Occurrence preview helpers (server-sourced only, FR-020) ───────────────

function setInstants(set: TaskOccurrenceSet): number[] {
  return [...set.occurrences_ms, ...set.day_buckets.map((b) => b.first_ms)]
}

function earliestInstant(set: TaskOccurrenceSet): number | null {
  const all = setInstants(set)
  return all.length === 0 ? null : Math.min(...all)
}

function upcomingInstants(set: TaskOccurrenceSet, limit: number): number[] {
  return setInstants(set).sort((a, b) => a - b).slice(0, limit)
}

function formatFullDateTime(ms: number): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(ms))
}

const PREVIEW_WINDOW_MS = 30 * 24 * 60 * 60 * 1000 // 30 days — short enough to stay well within useOccurrences' own caps

/** Save button label — a small named helper instead of a nested ternary (F6). */
function saveButtonLabel(isPending: boolean, isEdit: boolean): string {
  if (isPending) return 'Saving…'
  return isEdit ? 'Save' : 'Create'
}

export function CalendarEventSlideOver({
  open,
  onOpenChange,
  workspaceId,
  task,
  initialDate,
}: CalendarEventSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const isEdit = task != null
  const isLegacy = isEdit && isLegacyTrigger(task.trigger)
  const isEditingExistingRrule = isEdit && isRruleTrigger(task.trigger)

  const [title, setTitle] = useState('')
  const [agentId, setAgentId] = useState('__none__')
  const [anchorDate, setAnchorDate] = useState<Date | null>(null)
  const [recurrence, setRecurrence] = useState<RecurrenceValue>({ kind: 'none' })
  const [recurrenceValid, setRecurrenceValid] = useState(true)
  const [anchorFieldTouched, setAnchorFieldTouched] = useState(false)
  const [recurrenceFieldTouched, setRecurrenceFieldTouched] = useState(false)
  const [titleError, setTitleError] = useState('')
  const [saveError, setSaveError] = useState<string | null>(null)

  const scheduleTouched = anchorFieldTouched || recurrenceFieldTouched

  // ── Load / reset form state whenever the panel opens or its target changes ─
  useEffect(() => {
    if (!open) return
    setTitleError('')
    setSaveError(null)
    setAnchorFieldTouched(false)
    setRecurrenceFieldTouched(false)

    if (task) {
      setTitle(task.title)
      setAgentId(task.agent_id || '__none__')
      if (isRruleTrigger(task.trigger)) {
        const dtstartMs = task.trigger.config.dtstart_ms as number
        const rruleBody = task.trigger.config.rrule as string
        setAnchorDate(new Date(dtstartMs))
        const parsed = parseRruleString(rruleBody, dtstartMs)
        setRecurrence(parsed ? { kind: 'rrule', state: parsed } : { kind: 'none' })
      } else {
        // Legacy (cron_expr / every_ms) — US-5/D8: FRESH picker, never a
        // translated rule. No stored anchor is meaningful here either, so
        // the Date & time field defaults to "now" like a brand-new rule.
        setAnchorDate(new Date())
        setRecurrence({ kind: 'none' })
      }
    } else {
      setTitle('')
      setAgentId('__none__')
      setAnchorDate(initialDate ?? new Date())
      setRecurrence({ kind: 'none' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, task?.id, initialDate])

  // ── Agent roster (mirrors CreateTaskSlideOver / TaskDetailPanel) ───────────
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })
  const { teamIds, isLoading: teamLoading, isError: teamError } = useWorkspaceTeamIds(workspaceId)

  // ── FR-020 / legacy "next run" preview — server-sourced, short window ──────
  const previewRange = useMemo(() => {
    const from = Date.now()
    return { from, to: from + PREVIEW_WINDOW_MS }
  }, [open, task?.id])
  const previewQuery = useOccurrences({
    workspaceId,
    activeStart: new Date(previewRange.from),
    activeEnd: new Date(previewRange.to),
    enabled: open && isEdit && !!workspaceId,
  })
  const { isError: previewError } = previewQuery
  const previewSet = useMemo(
    () => previewQuery.data?.find((s) => s.task_id === task?.id) ?? null,
    [previewQuery.data, task?.id],
  )
  const nextRunMs = previewSet ? earliestInstant(previewSet) : null
  const upcomingPreview = previewSet ? upcomingInstants(previewSet, 3) : []

  // ── Trigger construction (FR-024) ───────────────────────────────────────────
  function buildTriggerForSave(): TaskTrigger {
    if (isEditingExistingRrule && !scheduleTouched) {
      // Byte-identical: never re-serialize an untouched rule.
      return task!.trigger!
    }
    if (recurrence.kind === 'none') {
      return { type: 'once', config: { at_ms: (anchorDate ?? new Date()).getTime() } }
    }
    // Fresh compile — always anchors in the browser zone (Timezone Semantics §1),
    // whether this is a create, a legacy replace, or a re-anchor.
    const anchor =
      isEditingExistingRrule && !anchorFieldTouched
        ? new Date() // recurrence-only touch on an existing series → re-anchor to now
        : (anchorDate ?? new Date())
    return { type: 'recurring', config: { ...compileRecurrence(recurrence.state, anchor) } }
  }

  function invalidateCalendarQueries() {
    queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
  }

  const createMutation = useMutation({
    mutationFn: (body: TaskCreateRequest) => createTask(body),
    onSuccess: () => {
      invalidateCalendarQueries()
      addToast({ message: 'Event created', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err) => {
      setSaveError(getErrorMessage(err, 'Failed to create event'))
    },
  })

  const updateMutation = useMutation({
    mutationFn: (data: TaskUpdateRequest) => updateTask(task!.id, data),
    onSuccess: () => {
      invalidateCalendarQueries()
      addToast({ message: 'Event updated', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err) => {
      setSaveError(getErrorMessage(err, 'Failed to update event'))
    },
  })

  const isPending = createMutation.isPending || updateMutation.isPending

  function handleAnchorChange(d: Date | null) {
    setAnchorDate(d)
    setAnchorFieldTouched(true)
    setSaveError(null)
  }

  function handleRecurrenceChange(v: RecurrenceValue) {
    setRecurrence(v)
    setRecurrenceFieldTouched(true)
    setSaveError(null)
  }

  function handleSave() {
    // F4 defense-in-depth: the Save button is already disabled while the
    // Repeat section reports an invalid state, but don't rely on that alone
    // — re-check here too.
    if (recurrence.kind === 'rrule' && !validateRecurrenceState(recurrence.state).valid) {
      return
    }
    const trimmed = title.trim()
    if (!trimmed) {
      setTitleError('Title is required')
      return
    }
    setTitleError('')
    if (!anchorDate) {
      setSaveError('Pick a date and time for this event.')
      return
    }
    setSaveError(null)

    // F-SFH6: buildTriggerForSave reaches the rrule lib's optionsToString —
    // an exception there must not fire uncaught inside this click handler
    // (which would leave the button silently dead with no feedback).
    let trigger: TaskTrigger
    try {
      trigger = buildTriggerForSave()
    } catch (err) {
      setSaveError(getErrorMessage(err, 'Failed to build the schedule for this event'))
      return
    }

    if (task) {
      updateMutation.mutate({
        title: trimmed,
        agent_id: agentId === '__none__' ? '' : agentId,
        trigger,
      })
    } else {
      createMutation.mutate({
        title: trimmed,
        action: 'llm',
        workspace_id: workspaceId,
        surface: 'user',
        priority: 3,
        agent_id: agentId === '__none__' ? undefined : agentId,
        trigger,
      })
    }
  }

  const saveDisabled = isPending || !recurrenceValid

  return (
    <Sheet open={open} onOpenChange={(next) => { if (!next) onOpenChange(false) }}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col p-0">
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>{isEdit ? 'Edit event' : 'New event'}</SheetTitle>
        </SheetHeader>

        <div className="flex flex-col flex-1 gap-5 px-6 py-4 overflow-y-auto">
          {/* Title */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ces-title" className="text-[var(--color-secondary)]">
              Title <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="ces-title"
              value={title}
              onChange={(e) => { setTitle(e.target.value); setTitleError('') }}
              placeholder="Event title"
              autoFocus
              maxLength={200}
              aria-invalid={!!titleError}
              aria-describedby={titleError ? 'ces-title-error' : undefined}
            />
            <FormError id="ces-title-error" error={titleError || null} />
          </div>

          {/* Agent */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Agent</Label>
            <SmartSelect
              value={agentId}
              onValueChange={setAgentId}
              placeholder={teamLoading ? 'Loading team…' : 'Unassigned'}
              disabled={teamLoading}
              triggerClassName="h-9 text-sm"
              ariaLabel="Agent"
              items={[
                { value: '__none__', label: 'Unassigned', className: 'text-xs' },
                ...buildTaskAssigneeItems(agents, {
                  teamScope: teamIds ? { kind: 'scoped', ids: teamIds } : { kind: 'unscoped' },
                  currentAssigneeId: task?.agent_id,
                }),
              ]}
            />
            {teamError && (
              <p className="text-xs text-[var(--color-muted)]">
                Team list unavailable — showing all agents
              </p>
            )}
          </div>

          {/* Date & time (the recurrence anchor) */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ces-anchor" className="text-[var(--color-secondary)]">
              Date &amp; time
            </Label>
            <DateTimePicker
              id="ces-anchor"
              aria-label="Date and time"
              value={anchorDate}
              onChange={handleAnchorChange}
            />
          </div>

          {/* Legacy old-format note (US-5, FR-013) — no cron string, no pre-fill. */}
          {isLegacy && (
            <div
              data-testid="legacy-trigger-note"
              className="rounded-md border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/5 p-3 text-xs text-[var(--color-secondary)] space-y-1"
            >
              <p className="font-medium">This task uses an old schedule format.</p>
              <p>
                {previewError
                  ? "Couldn't load upcoming run times."
                  : nextRunMs != null
                    ? `Next run: ${formatFullDateTime(nextRunMs)}`
                    : 'Next run time unavailable.'}
              </p>
              <p className="text-[var(--color-muted)]">
                Set a new repeat rule below to replace it.
              </p>
            </div>
          )}

          {/* Repeat section */}
          <div className="flex flex-col gap-1.5">
            <RecurrenceEditor
              value={recurrence}
              onChange={handleRecurrenceChange}
              anchor={anchorDate ?? new Date()}
              disabled={isPending}
              onValidityChange={setRecurrenceValid}
            />
            {isEditingExistingRrule && scheduleTouched && (
              <p data-testid="reanchor-notice" className="text-xs text-[color:var(--color-warning)]">
                Changing the schedule restarts the occurrence count, starting from now.
              </p>
            )}
            <FormError id="ces-repeat-error" error={saveError} />
          </div>

          {/* FR-020: upcoming fires preview, server-sourced (edit mode, active RRULE only) */}
          {isEditingExistingRrule && previewError && (
            <p className="text-xs text-[var(--color-muted)]" data-testid="upcoming-preview-error">
              Couldn't load upcoming run times.
            </p>
          )}
          {isEditingExistingRrule && !previewError && upcomingPreview.length > 0 && (
            <div className="flex flex-col gap-1" data-testid="upcoming-occurrences-preview">
              <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                Upcoming
              </span>
              <ul className="text-xs text-[var(--color-secondary)] space-y-0.5">
                {upcomingPreview.map((ms) => (
                  <li key={ms}>{formatFullDateTime(ms)}</li>
                ))}
              </ul>
            </div>
          )}
        </div>

        <SheetFooter className="flex-row gap-2 px-6 py-4 flex-shrink-0">
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={isPending}
            className="flex-1"
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={handleSave}
            disabled={saveDisabled}
            className="flex-1 bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
          >
            {saveButtonLabel(isPending, isEdit)}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
