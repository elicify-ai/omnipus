// TaskToolUI — issue #1683. A readable card for create_task / update_task /
// run_task / delete_task, live (makeAssistantToolUI) and on history replay:
// TaskToolCardBlock is rendered by the real saved-history dispatcher
// (HistoricalToolCallBlock.tsx::renderSavedToolCall, reached from
// ChatScreen.tsx::VirtualAssistantMessageRow) and by ToolCallBadge. Raw rule
// text is never shown.
//
// The trigger is parsed with the generated TaskTrigger Zod schema. A trigger
// that fails that parse falls back to the generic chip — no invented schedule.
// A trigger that parses but carries no concrete time (manual, or a recurring
// rule with nothing to say) renders the card with no schedule line.
//
// Time: a one-time trigger uses the viewer's local clock, the same helper the
// task detail panel uses (taskFormFields.ts::scheduledTriggerSummary, called
// from TaskDetailPanel.tsx::TaskDetailPanel; founder C1 B, 2026-11-07 11:16Z).
// A repeating trigger keeps the trigger's own zone: the clock time via
// formatInstantInTriggerZone, and the calendar dates the rule helpers print
// (the start words and the end date) via triggerZoneCalendarAnchor.
//
// Wording: every string below follows the Omnipus voice (founder rule
// 2026-11-08): in a chat the agent speaks as "I", says what happened, and an
// error or warning ends with one thing the person can do.

import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { CalendarBlank, Warning } from '@phosphor-icons/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import { makeAssistantToolUI } from '@/lib/assistant-tool-status'
import { Card } from '@/components/ui/card'
import { ErrorState } from '@/components/ui/error-state'
import type { TaskTrigger } from '@/lib/api'
import { TaskTrigger as TaskTriggerSchema } from '@/lib/api/generated/schemas'
import { parseRruleString, summarizeRecurrence, type RecurrenceEndCondition } from '@/lib/calendar/recurrence'
import { scheduledTriggerSummary } from '@/components/workspaces/taskFormFields'
import { GenericToolCall } from './GenericToolCall'

export const TASK_TOOL_NAMES = ['create_task', 'update_task', 'run_task', 'delete_task'] as const
export type TaskToolName = (typeof TASK_TOOL_NAMES)[number]

/**
 * The card heading, in the agent's voice. create_task and run_task each have a
 * second heading, picked in `headingFor` from the call and its result: a create
 * with no schedule must not claim one (CHECK F3), and a run_task that found the
 * task already running started nothing.
 */
const HEADINGS = {
  scheduled: 'I scheduled the task',
  created: 'I created the task',
  updated: 'I updated the task',
  started: 'I started the task',
  alreadyRunning: 'The task is already running',
  deleted: 'I deleted the task',
} as const

/**
 * The `note` pkg/tools/run_task.go::TaskRunTool.Execute returns, next to the
 * task_id / status / session_id, when the task is already in progress.
 */
const ALREADY_RUNNING_NOTE = 'already running'

/**
 * Plain sentence for a result that carries `schedule_sync_warning`.
 * Keeps the meaning of tool-parity spec r17 §7.11 (the REST sentence is
 * `task "<id>" was saved but its schedule could not be re-armed. The saved
 * trigger stays authoritative; re-save the schedule or restart the gateway.`):
 * the task was saved, the schedule is not active, the saved schedule is still
 * the one to use, and there is one thing to do from here — save it again in
 * the Calendar. Wording is team-lead decision D4 (2026-11-08).
 */
const SCHEDULE_NOT_ACTIVE =
  "I saved the task, but its schedule isn't active. The saved schedule is still the one to use. Open Calendar and save it again."

/** The verb of the no-reason failure sentence, per tool (team-lead decision D5). */
const FAILED_ACTION: Record<TaskToolName, string> = {
  create_task: 'create',
  update_task: 'update',
  run_task: 'start',
  delete_task: 'delete',
}

type PartStatus = { type: string; reason?: string }

export function isTaskToolName(name: string): name is TaskToolName {
  return (TASK_TOOL_NAMES as readonly string[]).includes(name)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseJSONQuietly(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

/** The three shapes a tool result arrives in (live string, replay `{text}` envelope, parsed object). */
function unwrapPayload(result: unknown): Record<string, unknown> | undefined {
  if (result == null) return undefined
  let value: unknown = result
  if (typeof value === 'string') value = parseJSONQuietly(value)
  if (!isRecord(value)) return undefined
  if ('text' in value && !('task_id' in value) && !('deleted' in value) && !('schedule_sync_warning' in value)) {
    const inner = value.text
    if (typeof inner === 'string') {
      const parsed = parseJSONQuietly(inner)
      return isRecord(parsed) ? parsed : undefined
    }
    return isRecord(inner) ? inner : undefined
  }
  return value
}

function readString(args: unknown, key: string): string {
  if (!isRecord(args)) return ''
  const value = args[key]
  return typeof value === 'string' ? value.trim() : ''
}

type TriggerRead =
  | { kind: 'absent' }
  | { kind: 'invalid' }
  | { kind: 'ok'; trigger: TaskTrigger }

function readTrigger(args: unknown): TriggerRead {
  if (!isRecord(args) || !('trigger' in args) || args.trigger === undefined) return { kind: 'absent' }
  const parsed = TaskTriggerSchema.safeParse(args.trigger)
  if (!parsed.success) return { kind: 'invalid' }
  return { kind: 'ok', trigger: parsed.data }
}

export interface TriggerZoneInstant {
  date: string
  time: string
  zoneLabel: string
}

/**
 * Date, clock time and a short zone name for one repeating-schedule instant,
 * in the trigger's own IANA zone (not the viewer's). Once triggers do not use
 * this — they go through scheduledTriggerSummary, which formats in the
 * viewer's zone. Returns null when `timeZone` is empty or not a real zone
 * name; the caller then shows no repeating schedule, rather than inventing one.
 */
export function formatInstantInTriggerZone(epochMs: number, timeZone: string): TriggerZoneInstant | null {
  if (!Number.isFinite(epochMs) || timeZone.trim() === '') return null
  let parts: Intl.DateTimeFormatPart[]
  try {
    parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date(epochMs))
  } catch {
    return null
  }
  const pick = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value ?? ''
  const year = pick('year')
  const month = pick('month')
  const day = pick('day')
  const hour = pick('hour')
  const minute = pick('minute')
  if (!year || !month || !day || !hour || !minute) return null
  const city = (timeZone.split('/').pop() ?? timeZone).replace(/_/g, ' ')
  return { date: `${month} ${day}, ${year}`, time: `${hour}:${minute}`, zoneLabel: `${city} time` }
}

/**
 * The instant's calendar date in the trigger's own zone, as a Date at local
 * noon. The recurrence helpers read the weekday, day-of-month and month of the
 * anchor they are given (a yearly rule carries no month or day of its own), so
 * `new Date(dtstart)` would read them in the VIEWER's zone, which can be the
 * previous or next day. Noon sidesteps every daylight-saving gap. Returns null
 * for an unusable instant or zone; the caller then shows no repeating schedule.
 */
function triggerZoneCalendarAnchor(epochMs: number, timeZone: string): Date | null {
  let parts: Intl.DateTimeFormatPart[]
  try {
    parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
    }).formatToParts(new Date(epochMs))
  } catch {
    return null
  }
  const pick = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((part) => part.type === type)?.value)
  const year = pick('year')
  const month = pick('month')
  const day = pick('day')
  if (!Number.isInteger(year) || !Number.isInteger(month) || !Number.isInteger(day)) return null
  return new Date(year, month - 1, day, 12, 0, 0, 0)
}

/**
 * An "ends on" date the recurrence helpers print as the RULE's calendar date.
 * `summarizeRecurrence` reads `end.date` through local getters — the editor
 * builds it from a picked day and `endOfDayUtc` reads it back the same way —
 * but `parseRruleString` returns the raw UNTIL instant. Printed as-is, a
 * Los Angeles schedule that ends at the end of 10 November (UNTIL
 * 20261111T075959Z) reads "November 11" to a UTC viewer. FR-006a: "Ends on
 * date D" is D 23:59:59 in the rule's own zone, so the date to show is the
 * instant's date there; the same noon carrier as the start anchor delivers it.
 * Done here, not in recurrence.ts: the calendar editor and the task detail
 * panel call the same helper and read the viewer's calendar. Returns null for
 * an unusable instant or zone; the caller then shows no repeating schedule.
 */
function endInTriggerZone(end: RecurrenceEndCondition, timeZone: string): RecurrenceEndCondition | null {
  if (end.kind !== 'on-date') return end
  const day = triggerZoneCalendarAnchor(end.date.getTime(), timeZone)
  return day ? { kind: 'on-date', date: day } : null
}

function plainSchedule(trigger: TaskTrigger): string | null {
  if (trigger.type === 'once') {
    // Founder C1 B, 2026-11-07 11:16Z: the same viewer-local string the task
    // detail panel renders (TaskDetailPanel.tsx::TaskDetailPanel calls
    // scheduledTriggerSummary). Its once branch is
    // `Runs once — ${new Date(at).toLocaleString()}` with no timeZone override
    // (taskFormFields.ts::scheduledTriggerSummary).
    return scheduledTriggerSummary(trigger)
  }
  if (trigger.type !== 'recurring') return null
  const rrule = trigger.config?.rrule
  const dtstart = trigger.config?.dtstart_ms
  const tz = trigger.config?.tz
  if (typeof rrule !== 'string' || typeof dtstart !== 'number' || typeof tz !== 'string') return null
  const when = formatInstantInTriggerZone(dtstart, tz)
  const anchor = triggerZoneCalendarAnchor(dtstart, tz)
  if (!when || !anchor) return null
  // The zone-anchored date, not `new Date(dtstart)`: the weekday / day / month
  // the helpers infer must be the trigger's, matching the clock time and zone
  // label printed beside them.
  const parsed = parseRruleString(rrule, anchor.getTime())
  if (!parsed) return null
  // The end date too: the trigger's calendar date, not the viewer's.
  const end = endInTriggerZone(parsed.end, tz)
  if (!end) return null
  const phrase = summarizeRecurrence({ kind: 'rrule', state: { ...parsed, end } }, anchor)
  return `${phrase} at ${when.time} (${when.zoneLabel})`
}

function hasSyncWarning(result: unknown): boolean {
  const payload = unwrapPayload(result)
  if (!payload) return false
  const warning = payload.schedule_sync_warning
  return typeof warning === 'string' && warning.trim() !== ''
}

function isAlreadyRunning(result: unknown): boolean {
  return unwrapPayload(result)?.note === ALREADY_RUNNING_NOTE
}

/**
 * The heading for a settled, successful call. `scheduled` is true when the
 * card shows a schedule — a create that carries none says "created", never
 * "scheduled".
 */
function headingFor(toolName: TaskToolName, scheduled: boolean, result: unknown): string {
  switch (toolName) {
    case 'create_task':
      return scheduled ? HEADINGS.scheduled : HEADINGS.created
    case 'update_task':
      return HEADINGS.updated
    case 'run_task':
      return isAlreadyRunning(result) ? HEADINGS.alreadyRunning : HEADINGS.started
    case 'delete_task':
      return HEADINGS.deleted
  }
}

/**
 * The reason a failed call came with: its error text, else a text result (live)
 * or the persisted `{ text }` envelope's text (saved history). Null when the
 * call came back with none.
 */
function failureReason(result: unknown, error?: string): string | null {
  if (typeof error === 'string' && error.trim() !== '') return error
  if (typeof result === 'string' && result.trim() !== '') return result
  if (isRecord(result) && typeof result.text === 'string' && result.text.trim() !== '') return result.text
  return null
}

/** A real reason is shown as it came; with none, the sentence says so and names the action (D5). */
function failureMessage(toolName: TaskToolName, result: unknown, error?: string): string {
  return (
    failureReason(result, error) ??
    `I couldn't ${FAILED_ACTION[toolName]} the task, and no reason was given. Try again.`
  )
}

function toMessagePartStatus(status: PartStatus): MessagePartStatus {
  if (status.type === 'running' || status.type === 'requires-action') return { type: 'running' }
  if (status.type === 'incomplete' && status.reason === 'cancelled') {
    return { type: 'incomplete', reason: 'cancelled' }
  }
  if (status.type === 'incomplete') return { type: 'incomplete', reason: 'error' }
  return { type: 'complete' }
}

function callFailed(status: PartStatus, isError?: boolean): boolean {
  if (isError) return true
  return status.type === 'incomplete' && status.reason !== 'cancelled'
}

export interface TaskToolCardBlockProps {
  toolName: TaskToolName
  args?: unknown
  result?: unknown
  status?: PartStatus
  isRunning?: boolean
  isError?: boolean
  error?: string
}

function TaskToolFallback(props: TaskToolCardBlockProps & { status: PartStatus }) {
  return (
    <GenericToolCall
      toolName={props.toolName}
      args={props.args}
      result={props.result}
      status={toMessagePartStatus(props.status)}
      isError={props.isError}
      error={props.error}
    />
  )
}

/**
 * The catalogued error presentation (role="alert", forced-colours text), with
 * the same inline layout the saved-history outcome lines use
 * (historyEntryOutcome.tsx): left-aligned, tight vertical padding.
 */
function TaskToolFailure({ message }: { message: string }) {
  return <ErrorState message={message} className="mt-[var(--space-2)] items-start py-[var(--space-1)]" />
}

function TaskToolLinks({ workspaceId }: { workspaceId: string }) {
  const className =
    'inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[color:var(--color-accent)] hover:underline'
  return (
    <div className="flex flex-wrap gap-[var(--space-3)]">
      <Link
        to="/workspaces/$workspaceId/chat"
        params={{ workspaceId }}
        search={{ panel: 'tasks' }}
        data-testid="task-tool-link-open"
        className={className}
      >
        Open tasks
      </Link>
      <Link
        to="/workspaces/$workspaceId/calendar"
        params={{ workspaceId }}
        data-testid="task-tool-link-calendar"
        className={className}
      >
        <CalendarBlank size={12} aria-hidden="true" />
        Open calendar
      </Link>
    </div>
  )
}

/** Presentational card shared by the live registration and the replay badge. */
export function TaskToolCardBlock({
  toolName,
  args,
  result,
  status,
  isRunning,
  isError,
  error,
}: TaskToolCardBlockProps): ReactNode {
  const part: PartStatus = status ?? { type: 'complete' }
  const running = isRunning ?? part.type === 'running'
  const fallback = (
    <TaskToolFallback
      toolName={toolName}
      args={args}
      result={result}
      status={part}
      isError={isError}
      error={error}
    />
  )
  if (callFailed(part, isError)) {
    return <TaskToolFailure message={failureMessage(toolName, result, error)} />
  }
  if (running || part.type === 'incomplete') return fallback

  const trigger = readTrigger(args)
  if (trigger.kind === 'invalid') return fallback

  const schedule = trigger.kind === 'ok' ? plainSchedule(trigger.trigger) : null
  const title = readString(args, 'title')
  const workspaceId = readString(args, 'workspace_id')
  const warning = hasSyncWarning(result)

  return (
    <Card data-testid="task-tool-card" className="mt-[var(--space-2)] flex flex-col gap-[var(--space-2)] p-[var(--space-3)]">
      <p
        data-testid="task-tool-verb"
        className="font-headline text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]"
      >
        {headingFor(toolName, schedule !== null, result)}
      </p>
      {title !== '' && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{title}</p>
      )}
      {schedule !== null && (
        <p data-testid="task-tool-schedule" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">
          {schedule}
        </p>
      )}
      {warning && (
        <p
          data-testid="task-tool-schedule-warning"
          className="flex items-start gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
        >
          <Warning size={12} weight="fill" className="mt-[var(--space-0-5)] shrink-0" aria-hidden="true" />
          <span>{SCHEDULE_NOT_ACTIVE}</span>
        </p>
      )}
      {workspaceId !== '' && <TaskToolLinks workspaceId={workspaceId} />}
    </Card>
  )
}

function makeTaskToolUI(toolName: TaskToolName) {
  return makeAssistantToolUI<Record<string, unknown>, unknown>({
    toolName,
    render: ({ args, result, status, isError }) => (
      <TaskToolCardBlock toolName={toolName} args={args} result={result} status={status} isError={isError} />
    ),
  })
}

export const CreateTaskToolUI = makeTaskToolUI('create_task')
export const UpdateTaskToolUI = makeTaskToolUI('update_task')
export const RunTaskToolUI = makeTaskToolUI('run_task')
export const DeleteTaskToolUI = makeTaskToolUI('delete_task')
