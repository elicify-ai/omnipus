// TaskToolUI — issue #1683. A readable card for create_task / update_task /
// run_task / delete_task, live (makeAssistantToolUI) and on history replay
// (TaskToolCardBlock from ToolCallBadge). Raw rule text is never shown.
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
// formatInstantInTriggerZone, and the calendar date the rule helpers infer via
// triggerZoneCalendarAnchor.

import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { CalendarBlank, Warning } from '@phosphor-icons/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import { makeAssistantToolUI } from '@/lib/assistant-tool-status'
import { Card } from '@/components/ui/card'
import type { TaskTrigger } from '@/lib/api'
import { TaskTrigger as TaskTriggerSchema } from '@/lib/api/generated/schemas'
import { parseRruleString, summarizeRecurrence } from '@/lib/calendar/recurrence'
import { scheduledTriggerSummary } from '@/components/workspaces/taskFormFields'
import { GenericToolCall } from './GenericToolCall'

export const TASK_TOOL_NAMES = ['create_task', 'update_task', 'run_task', 'delete_task'] as const
export type TaskToolName = (typeof TASK_TOOL_NAMES)[number]

const VERBS: Record<TaskToolName, string> = {
  create_task: 'Task scheduled',
  update_task: 'Task changed',
  run_task: 'Task run started',
  delete_task: 'Task deleted',
}

/**
 * Plain sentence for a result that carries `schedule_sync_warning`.
 * Keeps the meaning of tool-parity spec r17 §7.11 (the REST sentence is
 * `task "<id>" was saved but its schedule could not be re-armed. The saved
 * trigger stays authoritative; re-save the schedule or restart the gateway.`):
 * the task was saved, the schedule is not active, and the saved trigger still
 * counts until someone re-saves it or restarts.
 */
const SCHEDULE_NOT_ARMED =
  'Saved, but the schedule is not active. The task was saved and the saved trigger still counts; re-save the schedule or restart to arm it.'

const FAILURE_FALLBACK = 'This task action did not succeed.'

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
  const state = parseRruleString(rrule, anchor.getTime())
  if (!state) return null
  const phrase = summarizeRecurrence({ kind: 'rrule', state }, anchor)
  return `${phrase} at ${when.time} (${when.zoneLabel})`
}

function hasSyncWarning(result: unknown): boolean {
  const payload = unwrapPayload(result)
  if (!payload) return false
  const warning = payload.schedule_sync_warning
  return typeof warning === 'string' && warning.trim() !== ''
}

function failureMessage(result: unknown, error?: string): string {
  if (typeof error === 'string' && error.trim() !== '') return error
  if (typeof result === 'string' && result.trim() !== '') return result
  return FAILURE_FALLBACK
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

function TaskToolFailure({ message }: { message: string }) {
  return (
    <p className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">
      {message}
    </p>
  )
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
        Open task
      </Link>
      <Link
        to="/workspaces/$workspaceId/calendar"
        params={{ workspaceId }}
        data-testid="task-tool-link-calendar"
        className={className}
      >
        <CalendarBlank size={12} aria-hidden="true" />
        Show in Calendar
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
    return <TaskToolFailure message={failureMessage(result, error)} />
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
        {VERBS[toolName]}
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
          <span>{SCHEDULE_NOT_ARMED}</span>
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
