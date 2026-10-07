import { useEffect, useMemo, useState } from 'react'
import { CaretDown, ArrowUp, ArrowDown, Check } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import { PriorityBadge } from './PriorityBadge'
import { TaskActionButton } from './TaskActionButton'
import { RunningIndicator } from '@/components/ui/RunningIndicator'
import { Button } from '@/components/ui/button'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'
import { cn } from '@/lib/utils'
// 6-state unified vocabulary + colour — single source of truth.
import { STATUS_ORDER, statusLabel, taskDisplayColor, taskDisplayLabel } from '@/lib/statusColors'
import { isScheduledTrigger } from './taskFormFields'
import type { Task, Agent, Plan } from '@/lib/api'
import { TaskDetailsPopover } from './WorkItemDetails'

type SortKey = 'priority' | 'title' | 'status' | 'agent' | 'updated'
type SortDir = 'asc' | 'desc'

// The slice of an Agent the list needs to resolve a task's assignee name —
// tied to the generated wire type so it can't drift, and assignable straight
// from the caller's Agent[].
type AgentRef = Pick<Agent, 'id' | 'name'>

// Sentinels for the "no value" bucket in the Tags / Agent column filters. The
// leading NUL makes them un-collidable with any real tag (server-normalized to
// lowercase + trimmed) or agent name/id — a real value can never contain a NUL byte.
const UNTAGGED = '\u0000untagged'
const UNASSIGNED = '\u0000unassigned'

// Priority domain: PRIORITY_BADGE's own keys (PriorityBadge.tsx), kept in the
// exact insertion/ascending order that record declares (1 through 5) --
// spelled out as a literal rather than Object.keys(PRIORITY_BADGE) because
// the design-system spacing scanner's record-safety proof treats
// Object.keys/values/entries on a scanned record as an escape (it could hide
// a value derived from a dynamically enumerated key elsewhere), which blocks
// every reader of PRIORITY_BADGE across every importing file, not just this
// one. Order-identical to PRIORITY_BADGE's own declaration; behaviour
// unchanged (Object.keys on an object with only integer-like keys already
// enumerates in ascending numeric order, exactly this list).
const PRIORITY_ORDER: string[] = ['1', '2', '3', '4', '5']

/** T11 keeps every column present. The table reserves its fixed-column budget
 * plus a readable Title floor (two --space-8 plus --space-3); a narrow panel
 * scrolls the table viewport only instead of hiding or squeezing columns.
 */

interface ListViewProps {
  /**
   * Shared plan/agent/tag-scoped task set. List's own column sorting and
   * filters further narrow that visible set; toolbar scope stays applied.
   */
  tasks: Task[]
  agents: AgentRef[]
  plans?: Plan[]
  onTaskClick: (task: Task) => void
}

// Status sort rank derived from the canonical lifecycle order. Keyed by the
// Task status union so a typo'd literal is a compile error; the runtime `?? 99`
// still guards an additively-widened wire enum (STATUS_ORDER can't prove
// completeness as a non-tuple array).
const STATUS_RANK = Object.fromEntries(STATUS_ORDER.map((s, i) => [s, i])) as Record<Task['status'], number>

/** Assignee name shown in the Agent column: server-set name, then a lookup by
 * id, then the raw id, then null when the task has no agent. Shared by the
 * Agent column filter, the Agent sort, and the row render so they never drift. */
function resolveAgentName(task: Task, agents: AgentRef[]): string | null {
  return task.agent_name ?? (task.agent_id ? (agents.find((a) => a.id === task.agent_id)?.name ?? task.agent_id) : null)
}

/**
 * Flat, minimalist task table with Excel-style column headers. Every column
 * header is a dropdown: sortable columns (Pri / Title / Status / Agent /
 * Updated) offer sort ascending/descending; columns with discrete values
 * (Pri / Status / Tags / Agent) offer a checkbox value-filter — so Tags is
 * filter-only and Title/Updated are sort-only. Filtering is column-local (AND
 * across columns, OR within a column's checklist) over the plan-scoped `tasks`
 * prop. Borderless: no filled header slab, no row rules — rows separate by
 * padding + hover only.
 */
export function ListView({ tasks, agents, plans = [], onTaskClick }: ListViewProps) {
  const [sortKey, setSortKey] = useState<SortKey>('updated')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // T11: no hidden columns or reveal toggle. One width-stable table scrolls
  // inside its own viewport; its sticky header shares that exact geometry.

  // Per-column value filters (empty set = no filter on that column). Priority
  // keys are stringified ('1'..'5') so all four filters share one Set<string>.
  const [priFilter, setPriFilter] = useState<Set<string>>(new Set())
  const [statusFilter, setStatusFilter] = useState<Set<string>>(new Set())
  const [tagFilter, setTagFilter] = useState<Set<string>>(new Set())
  const [agentFilter, setAgentFilter] = useState<Set<string>>(new Set())

  // Filter out heartbeat/non-user surface tasks from the general list view,
  // AND schedule-bearing tasks (trigger.type ∈ {once, every, recurring}) —
  // those are calendar-only (operator ruling 2026-08-07, superseding the
  // narrower every/recurring-only D3 boundary). Presentation-only: the task
  // store/REST API are untouched, the task still exists and runs — it just
  // never renders as a List row.
  const userTasks = useMemo(
    () =>
      tasks.filter(
        (t) => (t.surface === 'user' || t.surface === undefined) && !isScheduledTrigger(t.trigger),
      ),
    [tasks],
  )

  // Distinct values per filterable column, derived from the plan-scoped set so
  // the dropdowns always list what's actually present (in canonical order).
  const priValues = useMemo(() => {
    const present = new Set(userTasks.map((t) => String(t.priority ?? 3)))
    return PRIORITY_ORDER.filter((p) => present.has(p)).map((p) => ({ key: p, label: `P${p}` }))
  }, [userTasks])

  const statusValues = useMemo(() => {
    const present = new Set(userTasks.map((t) => t.status))
    return STATUS_ORDER.filter((s) => present.has(s)).map((s) => ({ key: s, label: statusLabel(s) }))
  }, [userTasks])

  const tagValues = useMemo(() => {
    const set = new Set<string>()
    let hasUntagged = false
    for (const t of userTasks) {
      const tags = t.tags ?? []
      if (tags.length === 0) hasUntagged = true
      else tags.forEach((tag) => set.add(tag))
    }
    const vals = [...set].sort().map((tag) => ({ key: tag, label: tag }))
    if (hasUntagged) vals.push({ key: UNTAGGED, label: 'Untagged' })
    return vals
  }, [userTasks])

  const agentValues = useMemo(() => {
    const set = new Set<string>()
    let hasUnassigned = false
    for (const t of userTasks) {
      const name = resolveAgentName(t, agents)
      if (name) set.add(name)
      else hasUnassigned = true
    }
    const vals = [...set].sort().map((name) => ({ key: name, label: name }))
    if (hasUnassigned) vals.push({ key: UNASSIGNED, label: 'Unassigned' })
    return vals
  }, [userTasks, agents])

  // Prune any checked filter value that has left the current plan scope (the
  // plan band switched, or a refetch dropped the last task carrying it). Without
  // this a checked-but-now-absent value keeps filtering while being invisible in
  // its own dropdown — a lit filter dot over an empty list with nothing checked.
  // Mirrors WorkspaceTasksTab's stale-activePlanId/owner reset effects.
  useEffect(() => {
    const prune = (
      setter: React.Dispatch<React.SetStateAction<Set<string>>>,
      values: { key: string }[],
    ) => {
      const present = new Set(values.map((v) => v.key))
      setter((prev) =>
        [...prev].every((k) => present.has(k)) ? prev : new Set([...prev].filter((k) => present.has(k))),
      )
    }
    prune(setPriFilter, priValues)
    prune(setStatusFilter, statusValues)
    prune(setTagFilter, tagValues)
    prune(setAgentFilter, agentValues)
  }, [priValues, statusValues, tagValues, agentValues])

  const anyFilterActive =
    priFilter.size > 0 || statusFilter.size > 0 || tagFilter.size > 0 || agentFilter.size > 0

  const filtered = useMemo(() => {
    return userTasks.filter((t) => {
      if (priFilter.size && !priFilter.has(String(t.priority ?? 3))) return false
      if (statusFilter.size && !statusFilter.has(t.status)) return false
      if (tagFilter.size) {
        const tags = t.tags ?? []
        const ok = tags.some((tag) => tagFilter.has(tag)) || (tags.length === 0 && tagFilter.has(UNTAGGED))
        if (!ok) return false
      }
      if (agentFilter.size) {
        const key = resolveAgentName(t, agents) ?? UNASSIGNED
        if (!agentFilter.has(key)) return false
      }
      return true
    })
  }, [userTasks, agents, priFilter, statusFilter, tagFilter, agentFilter])

  const sorted = useMemo(() => {
    const rows = [...filtered]
    rows.sort((a, b) => {
      let cmp = 0
      switch (sortKey) {
        case 'priority':
          cmp = (a.priority ?? 3) - (b.priority ?? 3)
          break
        case 'title':
          cmp = a.title.localeCompare(b.title)
          break
        case 'status':
          cmp = (STATUS_RANK[a.status] ?? 99) - (STATUS_RANK[b.status] ?? 99)
          break
        case 'agent':
          cmp = (resolveAgentName(a, agents) ?? '').localeCompare(resolveAgentName(b, agents) ?? '')
          break
        case 'updated': {
          // Guard NaN symmetrically with formatUpdated: an unparseable/missing
          // date must not make the comparator non-transitive (silent mis-sort).
          const at = new Date(a.updated_at).getTime()
          const bt = new Date(b.updated_at).getTime()
          cmp = (Number.isNaN(at) ? 0 : at) - (Number.isNaN(bt) ? 0 : bt)
          break
        }
        default: {
          // Exhaustiveness: a new SortKey must be handled above, not silently
          // fall through to updated-order.
          const _exhaustive: never = sortKey
          void _exhaustive
        }
      }
      return sortDir === 'asc' ? cmp : -cmp
    })
    return rows
  }, [filtered, agents, sortKey, sortDir])

  function applySort(key: SortKey, dir: SortDir) {
    setSortKey(key)
    setSortDir(dir)
  }

  const sortCfg = (key: SortKey): ColumnSortConfig => ({
    key,
    activeKey: sortKey,
    activeDir: sortDir,
    onSort: applySort,
  })

  const ariaSort = (key: SortKey): 'ascending' | 'descending' | 'none' =>
    sortKey === key ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'

  return (
    // The viewport is bounded by the actual panel, never the window's width.
    <div className="@container flex flex-1 flex-col overflow-hidden">
      {/* Native scrolling leaves room for the shared focus outline at either edge. */}
      <div className="flex-1 overflow-auto scroll-px-[var(--space-1)]">
        {/* UAT Finding 2 fix: `table-layout: auto` (the default) sizes
            columns from CONTENT's min-content width, ignoring the Title
            cell's own `truncate`/overflow-hidden entirely — a 200-char
            unbroken title's min-content IS its full rendered width, so the
            browser widened the Title column to fit it regardless, pushing
            Status/Tags/Agent/Updated/Actions off-screen with no scroll to
            reach them. `table-fixed` sizes every column from the header
            row's own explicit widths (w-12/w-24/w-28/w-10 on the other
            columns) instead — content can no longer drive column width, so
            Title takes the remaining width and its own `truncate` (see
            TaskRow below) has effect. T11 reserves a Title floor and scrolls
            only this content area; every column stays present. */}
        <table className="w-full min-w-[calc(37rem+var(--space-8)*2+var(--space-3))] table-fixed text-[length:var(--type-body-compact-size)]">
          <thead className="sticky top-0 border-b border-[var(--color-border)]/15 bg-[var(--color-surface-0)]">
            <tr>
              <th className="w-12 px-[var(--space-3)] py-[var(--space-2)] text-left" aria-sort={ariaSort('priority')}>
                <ColumnMenu label="Pri" sort={sortCfg('priority')} filter={buildFilter(priValues, priFilter, setPriFilter)} />
              </th>
              <th className="px-[var(--space-2)] py-[var(--space-2)] text-left" aria-sort={ariaSort('title')}>
                <ColumnMenu label="Title" sort={sortCfg('title')} />
              </th>
              <th className="w-24 px-[var(--space-2)] py-[var(--space-2)] text-left" aria-sort={ariaSort('status')}>
                <ColumnMenu label="Status" sort={sortCfg('status')} filter={buildFilter(statusValues, statusFilter, setStatusFilter)} />
              </th>
              <th className="w-12 px-[var(--space-2)] py-[var(--space-2)] text-left"><span className="sr-only">Details</span></th>
              {/* T15 inserts Details before Actions; T11 keeps every column present. */}
              <th className="w-20 px-[var(--space-2)] py-[var(--space-2)] text-left">
                <span className="text-[length:var(--type-caption-size)] font-semibold text-[var(--color-muted)]">Actions</span>
              </th>
              <th
                className="w-28 px-[var(--space-2)] py-[var(--space-2)] text-left"
              >
                <ColumnMenu label="Tags" filter={buildFilter(tagValues, tagFilter, setTagFilter)} />
              </th>
              <th className="w-24 px-[var(--space-2)] py-[var(--space-2)] text-left" aria-sort={ariaSort('agent')}>
                <ColumnMenu label="Agent" sort={sortCfg('agent')} filter={buildFilter(agentValues, agentFilter, setAgentFilter)} />
              </th>
              <th
                className="w-28 px-[var(--space-3)] py-[var(--space-2)] text-right"
                aria-sort={ariaSort('updated')}
              >
                <ColumnMenu label="Updated" align="right" sort={sortCfg('updated')} />
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.length === 0 ? (
              <tr>
                <td colSpan={8} className="px-[var(--space-3)] py-[var(--space-5)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                  {anyFilterActive ? 'No tasks match the column filters' : 'No tasks to show'}
                </td>
              </tr>
            ) : (
              sorted.map((task) => (
                <TaskRow key={task.id} task={task} agents={agents} plans={plans} onClick={() => onTaskClick(task)} />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

interface ColumnFilterConfig {
  values: { key: string; label: string }[]
  selected: Set<string>
  onToggle: (key: string) => void
  onClear: () => void
}

/** Build a column's filter config from its value list + backing Set state. One
 * home for the copy-on-write toggle and clear, shared by every filterable column. */
function buildFilter(
  values: { key: string; label: string }[],
  selected: Set<string>,
  setSelected: React.Dispatch<React.SetStateAction<Set<string>>>,
): ColumnFilterConfig {
  return {
    values,
    selected,
    onToggle: (key) =>
      setSelected((prev) => {
        const next = new Set(prev)
        if (next.has(key)) next.delete(key)
        else next.add(key)
        return next
      }),
    onClear: () => setSelected(new Set()),
  }
}

interface ColumnSortConfig {
  key: SortKey
  activeKey: SortKey
  activeDir: SortDir
  onSort: (key: SortKey, dir: SortDir) => void
}

// A column is sortable, filterable, or both — never neither. The union makes
// the "neither" state unrepresentable and keeps sort-only columns (Title/
// Updated) from carrying dead filter props (and vice-versa for Tags).
type ColumnMenuProps = {
  label: string
  align?: 'left' | 'right'
} & (
  | { sort: ColumnSortConfig; filter?: ColumnFilterConfig }
  | { sort?: ColumnSortConfig; filter: ColumnFilterConfig }
)

/**
 * Excel-style column header: a flat, borderless dropdown trigger (label + sort
 * arrow + a filter dot when the column is filtered) opening a menu with
 * Sort ascending/descending (when `sort` is set) and a checkbox value-filter
 * (when `filter` is set). Trigger keeps the repo tabIndex convention.
 */
function ColumnMenu({ label, align = 'left', sort, filter }: ColumnMenuProps) {
  const isSorted = sort != null && sort.activeKey === sort.key
  const isFiltered = filter != null && filter.selected.size > 0
  const arrow = isSorted ? (sort!.activeDir === 'asc' ? ' ↑' : ' ↓') : ''
  const affordance = sort != null && filter != null ? 'sort and filter' : sort != null ? 'sort' : 'filter'

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          aria-label={`${label} column — ${affordance}`}
          className={cn(
            'h-auto gap-[var(--space-1)] p-0 text-[length:var(--type-caption-size)] font-semibold hover:bg-transparent',
            isSorted || isFiltered
              ? 'text-[var(--color-secondary)]'
              : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            align === 'right' ? 'ml-auto' : undefined,
          )}
        >
          {`${label}${arrow}`}
          {isFiltered && <span className="h-1 w-1 rounded-full bg-[var(--color-accent)]" aria-hidden="true" />}
          {/* Caret inherits the header's text colour at full opacity so it stays
              legible (an opacity-50 caret on the muted header colour was almost
              invisible). */}
          <CaretDown size={10} weight="bold" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align === 'right' ? 'end' : 'start'} className="w-48">
        {sort != null && (
          <>
            <DropdownMenuItem onClick={() => sort.onSort(sort.key, 'asc')} className="text-[length:var(--type-utility-xs-size)]">
              <ArrowUp size={12} className="mr-[var(--space-2)] opacity-70" />
              Sort ascending
              {isSorted && sort.activeDir === 'asc' && <Check size={12} className="ml-auto" />}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => sort.onSort(sort.key, 'desc')} className="text-[length:var(--type-utility-xs-size)]">
              <ArrowDown size={12} className="mr-[var(--space-2)] opacity-70" />
              Sort descending
              {isSorted && sort.activeDir === 'desc' && <Check size={12} className="ml-auto" />}
            </DropdownMenuItem>
          </>
        )}
        {sort != null && filter != null && <DropdownMenuSeparator />}
        {filter != null && (
          <>
            <div className="max-h-56 overflow-y-auto">
              {filter.values.length === 0 ? (
                <div className="px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No values</div>
              ) : (
                filter.values.map((v) => (
                  <DropdownMenuCheckboxItem
                    key={v.key}
                    checked={filter.selected.has(v.key)}
                    onCheckedChange={() => filter.onToggle(v.key)}
                    // Keep the menu open while toggling several values (Radix
                    // closes a checkbox item's menu on select by default).
                    onSelect={(e) => e.preventDefault()}
                    className="text-[length:var(--type-utility-xs-size)]"
                  >
                    {v.label}
                  </DropdownMenuCheckboxItem>
                ))
              )}
            </div>
            {isFiltered && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={filter.onClear} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                  Clear filter
                </DropdownMenuItem>
              </>
            )}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function TaskRow({ task, agents, plans, onClick }: { task: Task; agents: AgentRef[]; plans: Plan[]; onClick: () => void }) {
  const priority = task.priority ?? 3
  const tags = task.tags ?? []
  const agentName = resolveAgentName(task, agents)
  // FR-022 (SP-41) + founder PI3: a running task's row shows THE catalogued
  // running indicator — animated spinner only, no token count.
  const running = task.status === 'in_progress'

  return (
    // The row is mouse-clickable for whole-row convenience; the REAL keyboard/AT
    // entry point is the Title button below (one tab stop per row, announced as
    // actionable). Borderless — separation is padding + hover, not a rule.
    <tr onClick={onClick} className="cursor-pointer transition-colors hover:bg-[var(--color-surface-2)]/40">
      <td className="px-[var(--space-3)] py-[var(--space-2)]">
        <PriorityBadge
          priority={priority}
          className="bg-transparent p-0 text-[length:var(--type-caption-size)] font-bold"
        />
      </td>
      <td className="px-[var(--space-2)] py-[var(--space-2)]">
        <Button
          variant="ghost"
          onClick={(e) => {
            // The <tr> also has onClick — stop the button's click bubbling so it
            // doesn't fire onClick twice. A native <button> already activates on
            // Enter (keydown) and Space (keyup), so no explicit onKeyDown is
            // needed — the row stays a single tab stop via this button.
            e.stopPropagation()
            onClick()
          }}
          data-task-open=""
          aria-label={`${task.title}, status ${taskDisplayLabel(task)}`}
          // T13: one-line ellipsis; T15 supplies full text from the info
          // button's click-triggered Popover; there is no native hover popup.
          className="block h-auto w-full min-w-0 truncate p-0 text-left text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent"
        >
          {task.title}
        </Button>
      </td>
      <td className="px-[var(--space-2)] py-[var(--space-2)]">
        {/* ADR-052 FR-015/US-8 — a user-cancelled task renders "Cancelled"
            (orange), distinct from a genuine "Failed" (red), via the shared
            taskDisplayColor/taskDisplayLabel helpers (statusColors.ts). */}
        <div className="flex items-center gap-[var(--space-1)]">
          {running ? <RunningIndicator /> : <span aria-hidden="true" style={{ color: taskDisplayColor(task) }}>●</span>}
          <span className="text-[length:var(--type-utility-xs-size)] font-medium" style={{ color: taskDisplayColor(task) }}>
            {taskDisplayLabel(task)}
          </span>
        </div>
      </td>
      <td className="px-[var(--space-2)] py-[var(--space-2)]"><TaskDetailsPopover task={task} plans={plans} agents={agents} onOpenTask={onClick} /></td>
      {/* Always visible, and isolated from the row's open-detail action. */}
      <td className="px-[var(--space-2)] py-[var(--space-2)]" onClick={(e) => e.stopPropagation()}>
        <TaskActionButton task={task} />
      </td>
      <td
        className="px-[var(--space-2)] py-[var(--space-2)]"
      >
        {tags.length > 0 ? (
          <div className="flex max-w-[7rem] flex-wrap items-center gap-[var(--space-1)]">
            {tags.slice(0, 2).map((tag) => (
              <span key={tag} data-task-tag="" className="inline-flex min-w-0 max-w-full items-center gap-[var(--space-1)] whitespace-normal break-normal wrap-break-word text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                {tags.indexOf(tag) > 0 && <span aria-hidden="true">·</span>}<WordBoundaryText text={tag} />
              </span>
            ))}
            {tags.length > 2 && <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">+{tags.length - 2}</span>}
          </div>
        ) : (
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">—</span>
        )}
      </td>
      <td className="px-[var(--space-2)] py-[var(--space-2)]">
        {agentName ? (
          <span className="block max-w-[5rem] truncate text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">{agentName}</span>
        ) : (
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">—</span>
        )}
      </td>
      <td
        className="px-[var(--space-3)] py-[var(--space-2)] text-right"
      >
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{formatUpdated(task.updated_at)}</span>
      </td>
    </tr>
  )
}

function formatUpdated(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMin = Math.floor(diffMs / 60_000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffH = Math.floor(diffMin / 60)
  if (diffH < 24) return `${diffH}h ago`
  const diffD = Math.floor(diffH / 24)
  return `${diffD}d ago`
}
