/**
 * calendarAgentFilter — client-side agent filter predicate for the workspace
 * calendar toolbar (FR-015, User Story 4).
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 *   FR-015: toolbar Agent filter (All agents default, roster entries,
 *           Unassigned) applied client-side with NO refetch; milestone chips
 *           are exempt.
 *   SC-004: selecting an agent changes the rendered event set with ZERO
 *           additional network requests.
 *
 * Pure, no React, no network — mirrors `eventMapping.ts`'s "pure module"
 * convention so it is trivially unit-testable and safely reusable from
 * CalendarScreen's render path on every keystroke/selection.
 *
 * Design note (forward-compatibility with Wave-2 occurrence/aggregated
 * chips): today `mapToCalendarEvents` only ever produces `task-due`,
 * `task-fire`, and `milestone` extendedProps (see `./types`'s
 * `CalendarEventExtProps`). PR 3 (US-1/US-2) adds recurring occurrence and
 * aggregated chips, which per the spec's US-4 resolution ("the predicate
 * must key off the underlying task's agent_id ... occurrence chips carry
 * their task_id") will ALSO carry a `taskId` in extendedProps. Rather than
 * switching on the exhaustive `kind` union (which would need editing again
 * the moment a new kind lands), this predicate duck-types on
 * `extendedProps.taskId` for every non-milestone kind — so it transparently
 * covers due/fire chips today and occurrence/aggregated chips the moment
 * they exist, with no further change here.
 */

import type { EventInput } from '@fullcalendar/core'
import type { Task } from '@/lib/api'

/** Sentinel value for "no filter" — mirrors ListView's FilterSelect convention. */
export const AGENT_FILTER_ALL = '__all__'
/** Sentinel value for "tasks with no agent_id" — mirrors ListView's FilterSelect convention. */
export const AGENT_FILTER_UNASSIGNED = '__unassigned__'

/**
 * The subset of a calendar event's `extendedProps` this predicate reads.
 * Deliberately looser than the full `CalendarEventExtProps` discriminated
 * union — see the forward-compatibility note above.
 */
interface FilterableExtProps {
  kind: string
  taskId?: string
}

function isFilterableExtProps(v: unknown): v is FilterableExtProps {
  return !!v && typeof v === 'object' && typeof (v as { kind?: unknown }).kind === 'string'
}

/**
 * True when `event` should remain visible under the given agent filter.
 *
 * - `AGENT_FILTER_ALL` → every event visible (filter is a no-op).
 * - `kind === 'milestone'` → always visible; milestones have no agent
 *   (US-4.3, Ambiguity Warning #2: "Resolved: exempt").
 * - Any other task-derived chip (due / fire / future occurrence / aggregated)
 *   → resolves the owning task via `extendedProps.taskId` against `tasks`
 *   and compares its `agent_id`; `AGENT_FILTER_UNASSIGNED` matches tasks
 *   with no `agent_id`.
 * - A task-kind chip whose task cannot be resolved (stale id / missing from
 *   the current `tasks` list) is hidden under an ACTIVE filter — fails
 *   closed rather than risk showing it under the wrong agent. It is never
 *   hidden when the filter itself is `AGENT_FILTER_ALL` (handled above).
 * - A chip with no recognizable `kind`/`taskId` shape at all is left visible
 *   (nothing to filter it by; never hide something this predicate doesn't
 *   understand).
 */
export function eventMatchesAgentFilter(
  event: EventInput,
  tasks: Task[],
  agentFilter: string,
): boolean {
  if (agentFilter === AGENT_FILTER_ALL) return true

  const ext: unknown = event.extendedProps
  if (!isFilterableExtProps(ext)) return true
  if (ext.kind === 'milestone') return true
  if (!ext.taskId) return true

  const task = tasks.find((t) => t.id === ext.taskId)
  if (!task) return false // stale/unresolvable task under an active filter — fail closed

  if (agentFilter === AGENT_FILTER_UNASSIGNED) return !task.agent_id
  return task.agent_id === agentFilter
}

/**
 * Filters a full FullCalendar `events` array against the agent filter.
 * This is the function CalendarScreen calls directly — client-side, no
 * refetch, applied to whatever `events` the (possibly Wave-2-extended)
 * mapping already produced.
 */
export function filterEventsByAgent(
  events: EventInput[],
  tasks: Task[],
  agentFilter: string,
): EventInput[] {
  if (agentFilter === AGENT_FILTER_ALL) return events
  return events.filter((event) => eventMatchesAgentFilter(event, tasks, agentFilter))
}
