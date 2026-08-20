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
import { Textarea } from '@/components/ui/textarea'
import { FormError } from '@/components/ui/FormError'
import { SmartSelect } from '@/components/ui/smart-select'
import { DateTimePicker } from '@/components/ui/date-time-picker'
import {
  createTask,
  updateTask,
  fetchAgents,
  fetchTaskRuns,
  buildTaskAssigneeItems,
  tasksQueryKeys,
  getErrorMessage,
} from '@/lib/api'
import type { Task, TaskTrigger, TaskCreateRequest, TaskUpdateRequest, TaskRun, Todo } from '@/lib/api'
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
import { resolveRunForOccurrence } from '@/lib/calendar/eventMapping'
import { formatDateTime } from '@/lib/dateFormat'
import type { TaskRunOccurrenceContext } from '@/lib/taskRuns'
import { TaskRunStatusField } from '@/components/workspaces/TaskRunStatusField'
import { TaskChecklistField } from '@/components/workspaces/TaskChecklistField'
import { TaskResultField } from '@/components/workspaces/TaskResultField'
import { OpenInChatButton } from '@/components/workspaces/OpenInChatButton'
import { TaskRunsList } from '@/components/workspaces/TaskRunsList'

export interface CalendarEventSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
  /** null = create mode; non-null = series-edit mode. */
  task: Task | null
  /** Create-mode only: pre-fills the Date & time field (e.g. from a day-cell click). */
  initialDate?: Date
  /**
   * The occurrence instant the operator clicked to open this slide-over
   * (ADR-050 RD8 / task-run-history-spec §4.3) — e.g. from an individual
   * `task-occurrence` calendar chip. Re-points the Run status / Result /
   * Open-in-Chat sections at THAT SPECIFIC occurrence's run instead of the
   * task-level aggregate. `null`/omitted for a series-config edit or a
   * normal (non-recurring) task — those keep the existing task-level
   * behaviour.
   */
  selectedOccurrenceMs?: number | null
  /**
   * The day-window `[startMs, endMs)` of a clicked aggregated bucket chip
   * (`task-occurrence-agg`), when this slide-over was opened from a bucket
   * instead of an individual occurrence (H2 fix, task-run-history-spec
   * §4.3 — "a bucket click opens a mini-list of that day's runs"). When
   * set, the task-level status/result/chat sections are SUPPRESSED — left
   * alone they would silently fall back to task.status/task.result/
   * task.session_id, i.e. the most recent fire OVERALL, which may be from a
   * different day than the bucket clicked — and a day-scoped run list
   * (`TaskRunsList`'s `dayRange`) renders in their place. `null`/omitted
   * for the individual-occurrence and series-config-edit cases, which keep
   * their existing behaviour. Mutually exclusive with `selectedOccurrenceMs`
   * in practice (a click resolves to one or the other); if both were
   * somehow set, the bucket view takes precedence.
   */
  selectedBucketDayRange?: { startMs: number; endMs: number } | null
}

// Stable empty scope for the Agent picker's items list while the
// workspace-team query is still loading — see the `SmartSelect items=` usage
// below for why this must not be the full unscoped roster.
const EMPTY_TEAM_SCOPE: Set<string> = new Set()

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
  selectedOccurrenceMs,
  selectedBucketDayRange,
}: CalendarEventSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const isEdit = task != null
  const isLegacy = isEdit && isLegacyTrigger(task.trigger)
  const isEditingExistingRrule = isEdit && isRruleTrigger(task.trigger)
  // H2 fix — see the prop doc comment. Bucket takes precedence if somehow
  // both were set.
  const hasBucketContext = isEdit && selectedBucketDayRange != null

  const [title, setTitle] = useState('')
  const [agentId, setAgentId] = useState('')
  const [prompt, setPrompt] = useState('')
  const [anchorDate, setAnchorDate] = useState<Date | null>(null)
  const [recurrence, setRecurrence] = useState<RecurrenceValue>({ kind: 'none' })
  const [recurrenceValid, setRecurrenceValid] = useState(true)
  const [anchorFieldTouched, setAnchorFieldTouched] = useState(false)
  const [recurrenceFieldTouched, setRecurrenceFieldTouched] = useState(false)
  const [titleError, setTitleError] = useState('')
  const [saveError, setSaveError] = useState<string | null>(null)
  // Buffered checklist for CREATE mode (no task exists yet, so items can't be
  // persisted per-edit like the EDIT slide-over does). Folded into the create
  // request on Save. Unused in EDIT mode, where TaskChecklistField is task-bound.
  const [draftTodos, setDraftTodos] = useState<Todo[]>([])

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
      setAgentId(task.agent_id || '')
      setPrompt(task.prompt || '')
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
      setAgentId('')
      setPrompt('')
      setAnchorDate(initialDate ?? new Date())
      setRecurrence({ kind: 'none' })
    }
    // Draft checklist only applies to CREATE mode; always start empty on (re)open.
    setDraftTodos([])
     
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

  // ── ADR-050 RD8 / spec §4.3 — resolve the clicked occurrence's own run ─────
  // A calendar occurrence chip carries selectedOccurrenceMs; re-point the
  // Run status / Result / Open-in-Chat sections at THAT occurrence's run
  // instead of the task-level mirror (which only ever reflects the LATEST
  // fire). Absent selectedOccurrenceMs → those sections keep their existing
  // task-level behaviour untouched.
  const hasOccurrenceContext = isEdit && selectedOccurrenceMs != null
  const runsForOccurrenceQuery = useQuery({
    queryKey: tasksQueryKeys.runs(task?.id ?? ''),
    // Guarded by `enabled` below — task is non-null whenever hasOccurrenceContext.
    queryFn: () => fetchTaskRuns(task!.id),
    enabled: open && hasOccurrenceContext,
  })
  // Resolution itself (newest-run tie-break, server-matching) lives in
  // `resolveRunForOccurrence` (eventMapping.ts) — imported, not
  // reimplemented, so the slide-over can never drift from the calendar
  // chip's own occurrence→run join logic.
  const resolvedRun = useMemo<TaskRun | undefined>(() => {
    if (!hasOccurrenceContext || !runsForOccurrenceQuery.data || selectedOccurrenceMs == null) return undefined
    return resolveRunForOccurrence(runsForOccurrenceQuery.data, selectedOccurrenceMs)
  }, [hasOccurrenceContext, runsForOccurrenceQuery.data, selectedOccurrenceMs])
  // A resolved-run section is shown only when there's no occurrence context
  // (task-level fallback) OR a run was actually found — a future occurrence
  // with no run yet shows ONLY TaskRunStatusField's "Run now" (spec §4.3).
  const showResultAndChat = !hasOccurrenceContext || !!resolvedRun
  // M7 fix — fold the occurrence instant + its resolved run into ONE object,
  // built here once and passed identically to TaskRunStatusField/
  // TaskResultField/OpenInChatButton so the three siblings can never
  // disagree about which occurrence a `run` belongs to.
  const occurrenceContext = useMemo<TaskRunOccurrenceContext | undefined>(
    () => (hasOccurrenceContext && selectedOccurrenceMs != null ? { ms: selectedOccurrenceMs, run: resolvedRun } : undefined),
    [hasOccurrenceContext, selectedOccurrenceMs, resolvedRun],
  )

  // ── Trigger construction (FR-024) ───────────────────────────────────────────
  function buildTriggerForSave(): TaskTrigger {
    if (isLegacy && recurrence.kind === 'none') {
      // FIX (grill-code, CRITICAL): the Repeat section ALWAYS starts fresh
      // ({kind:'none'}) for a legacy trigger (US-5/D8), so "the operator
      // never built a new rule" and "the operator explicitly chose 'Does not
      // repeat'" are indistinguishable from `recurrence` alone. Treating
      // this as create-mode's none→once conversion would silently destroy a
      // working cron/every_ms schedule on ANY edit — even a title-only one.
      // Only an actually-built RRULE (recurrence.kind==='rrule', US-5.3
      // replace) may replace a legacy trigger; otherwise preserve it as-is.
      return task!.trigger!
    }
    if (isEditingExistingRrule && !scheduleTouched) {
      // Byte-identical: never re-serialize an untouched rule.
      return task!.trigger!
    }
    if (recurrence.kind === 'none') {
      return { type: 'once', config: { at_ms: (anchorDate ?? new Date()).getTime() } }
    }
    // Fresh compile — anchors in the browser zone for a create or a legacy
    // replace (Timezone Semantics §1); an existing RRULE series being
    // re-anchored keeps its OWN stored zone instead (FR-024) — re-anchoring
    // dtstart must not silently change the series' timezone too.
    const anchor =
      isEditingExistingRrule && !anchorFieldTouched
        ? new Date() // recurrence-only touch on an existing series → re-anchor to now
        : (anchorDate ?? new Date())
    const tz = isEditingExistingRrule ? (task!.trigger!.config!.tz as string) : undefined
    return { type: 'recurring', config: { ...compileRecurrence(recurrence.state, anchor, tz) } }
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

  // A scheduled task's agent is its only executor and its prompt is its only
  // instruction (Task.Prompt — pkg/agent/task_executor.go:462) — a fired
  // task with neither gets nothing to do and no one to do it. Both are
  // therefore hard-required, gating Save reactively (like `recurrenceValid`)
  // rather than only on click — a fresh panel cannot be saved until both are
  // set. Title stays click-validated (existing behavior, unchanged).
  const agentSelected = agentId.trim().length > 0
  const trimmedPrompt = prompt.trim()
  const promptEmpty = trimmedPrompt.length === 0

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
    // Defense-in-depth (mirrors the recurrence check above): Save is already
    // disabled while these are unset, but don't rely on that alone.
    if (!agentSelected || promptEmpty) {
      setSaveError('Select an agent and add instructions before saving.')
      return
    }
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
        agent_id: agentId,
        prompt: trimmedPrompt,
        trigger,
      })
    } else {
      createMutation.mutate({
        title: trimmed,
        action: 'llm',
        workspace_id: workspaceId,
        surface: 'user',
        priority: 3,
        agent_id: agentId,
        prompt: trimmedPrompt,
        trigger,
        // Fold the buffered create-mode checklist into the new task; omit when
        // empty to keep the request minimal.
        ...(draftTodos.length > 0 ? { todos: draftTodos } : {}),
      })
    }
  }

  const saveDisabled = isPending || !recurrenceValid || !agentSelected || promptEmpty

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

          {/* Agent — required: a scheduled task's only executor. */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">
              Agent <span className="text-[var(--color-error)]">*</span>
            </Label>
            <SmartSelect
              value={agentId}
              onValueChange={setAgentId}
              placeholder={teamLoading ? 'Loading team…' : 'Select an agent'}
              disabled={teamLoading}
              triggerClassName="h-9 text-sm"
              ariaLabel="Agent"
              items={
                // While the team-set query is in flight, don't feed the full
                // unscoped global roster into the picker — SmartSelect swaps
                // its underlying implementation (and thus its accessible
                // role: implicit "button" vs. explicit "combobox") based on
                // item COUNT (SEARCHABLE_THRESHOLD=5 in smart-select.tsx). A
                // real install's global agent roster is commonly >5 while a
                // workspace's own team is commonly <=5, so feeding the
                // unscoped roster in here made the control's accessible
                // identity flip the instant the query resolved — breaking
                // role-based automation/assistive-tech interaction with a
                // control that was ALREADY disabled and offering nothing
                // selectable. Scoping to an empty team set (falling through
                // to just the current assignee, if any, via
                // `currentAssigneeId`) keeps the item count — and therefore
                // the rendered branch — stable across the loading→resolved
                // transition for the common (<=5-member team) case, while
                // still showing an edited task's already-assigned agent by
                // name instead of a blank placeholder.
                teamLoading
                  ? buildTaskAssigneeItems(agents, {
                      teamScope: { kind: 'scoped', ids: EMPTY_TEAM_SCOPE },
                      currentAssigneeId: task?.agent_id,
                    })
                  : buildTaskAssigneeItems(agents, {
                      teamScope: teamIds ? { kind: 'scoped', ids: teamIds } : { kind: 'unscoped' },
                      currentAssigneeId: task?.agent_id,
                    })
              }
            />
            {teamError && (
              <p className="text-xs text-[var(--color-muted)]">
                Team list unavailable — showing all agents
              </p>
            )}
            <FormError
              id="ces-agent-error"
              error={agentSelected ? null : 'Select an agent to run this task.'}
            />
          </div>

          {/* Instruction (prompt) — required: the agent's only execution
              instruction each time this task fires (Task.Prompt,
              pkg/agent/task_executor.go:462). Empty prompt → the agent gets
              only "# Task: <title>" and nothing to actually do. */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ces-prompt" className="text-[var(--color-secondary)]">
              Instruction <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Textarea
              id="ces-prompt"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Describe what the agent should do each time this runs…"
              rows={4}
              maxLength={10000}
              className="text-xs font-mono resize-none"
              aria-invalid={promptEmpty}
              aria-describedby={promptEmpty ? 'ces-prompt-error' : undefined}
            />
            <FormError
              id="ces-prompt-error"
              error={promptEmpty ? 'Instructions are required.' : null}
            />
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
                    ? `Next run: ${formatDateTime(nextRunMs)}`
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
              tz={isRruleTrigger(task?.trigger) ? (task!.trigger!.config!.tz as string) : undefined}
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
                  <li key={ms}>{formatDateTime(ms)}</li>
                ))}
              </ul>
            </div>
          )}

          {/* Task-lifecycle sections. Recurring tasks are excluded from
              Board/List (US-3), so this slide-over is a recurring task's ONLY
              surface — without these, its checklist, run status, result, and
              chat link would be reachable nowhere. Reused verbatim from
              TaskDetailPanel (single source of truth); in EDIT they run their
              OWN mutations, independent of the Save button (which governs only
              title/agent/instruction/recurrence). Run status / result / chat
              link require an executed task, so they are EDIT-only; the
              checklist is offered in CREATE too (buffered, persisted on Save). */}
          {isEdit && task ? (
            <div className="flex flex-col gap-5 pt-4 border-t border-[var(--color-border)]">
              {hasBucketContext ? (
                <>
                  {/* H2 fix — a bucket click shows THAT DAY's runs, never the
                      task-level status/result/chat mirror (which reflects the
                      most recent fire OVERALL and may be from a different
                      day). TaskChecklistField stays — it's task-scoped, not
                      occurrence/day-scoped. */}
                  <TaskChecklistField task={task} />
                  <TaskRunsList
                    taskId={task.id}
                    dayRange={selectedBucketDayRange!}
                    onNavigate={() => onOpenChange(false)}
                  />
                </>
              ) : (
                <>
                  {hasOccurrenceContext && runsForOccurrenceQuery.isError && (
                    <p
                      className="text-xs text-[color:var(--color-error)]"
                      data-testid="occurrence-run-resolve-error"
                    >
                      Couldn't load this occurrence's run status.
                    </p>
                  )}
                  <TaskRunStatusField task={task} occurrence={occurrenceContext} />
                  <TaskChecklistField task={task} />
                  {showResultAndChat && <TaskResultField task={task} occurrence={occurrenceContext} />}
                  {showResultAndChat && (
                    <OpenInChatButton task={task} occurrence={occurrenceContext} onNavigate={() => onOpenChange(false)} />
                  )}
                  {/* RD8 — always embed run history so it survives a schedule
                      edit, independent of whether the current trigger can
                      still project a given run's occurrence_ms (spec
                      §4.3/§3.6). */}
                  <TaskRunsList taskId={task.id} onNavigate={() => onOpenChange(false)} />
                </>
              )}
            </div>
          ) : (
            <div className="flex flex-col gap-5 pt-4 border-t border-[var(--color-border)]">
              <TaskChecklistField value={draftTodos} onChange={setDraftTodos} />
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
