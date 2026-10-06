import { useEffect, useMemo, useRef, useState } from 'react'
import { CaretDown, ArrowUp, ArrowDown, Check, DotsThree } from '@phosphor-icons/react'
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
import { cn } from '@/lib/utils'
// 6-state unified vocabulary + colour — single source of truth.
import { STATUS_ORDER, statusLabel, taskDisplayColor, taskDisplayLabel } from '@/lib/statusColors'
import { isScheduledTrigger } from './taskFormFields'
import type { Task, Agent } from '@/lib/api'

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

/**
 * SP-35 narrow-list visibility, using SP-33's container queries: keep the
 * existing 648px boundary for automatically hiding Tags/Updated behind "⋯".
 * The earlier 504px fixed-column budget assumed a 16px root. The actual
 * widths — Pri `w-12`, Status/Agent `w-24`, Tags/Updated `w-28`, Actions
 * `w-10` — total 31.5rem: 441px at the app's default 14px root, not 504px.
 * The 648px boundary is unchanged; it is not the current fixed-column sum
 * plus the intended 144px Title floor. On deliberate reveal, the table's
 * minimum width reserves that floor (two `--space-8` plus `--space-3`) in
 * addition to the rem-based column budget, so Title cannot collapse while
 * the table scrolls sideways inside the panel (founder decision B).
 * `@max-[648px]` measures this component's own container, never the window.
 */

interface ListViewProps {
  /**
   * Plan-scoped task set (the plan band filter applies upstream); the List
   * owns everything else — per-column sort AND filter, Excel-style. Board's
   * toolbar Agent/Tags filters are NOT applied here, so the column filter
   * dropdowns always offer the full set of values in the current plan scope.
   */
  tasks: Task[]
  agents: AgentRef[]
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
export function ListView({ tasks, agents, onTaskClick }: ListViewProps) {
  const [sortKey, setSortKey] = useState<SortKey>('updated')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // SP-34 — Tags/Updated column visibility. `auto` (default) lets the
  // `@max-[648px]:hidden` container query decide (narrow container → hidden);
  // the "⋯" overflow control overrides it for the session until toggled
  // back. The direction of the FIRST toggle ("what am I looking at right
  // now?") is read from the narrow-probe span below — the container query
  // itself stays the single source of truth for the breakpoint, so JS never
  // re-derives it (no window width, no duplicated constant).
  const [columnOverflow, setColumnOverflow] = useState<'auto' | 'shown' | 'hidden'>('auto')
  const narrowProbeRef = useRef<HTMLSpanElement | null>(null)

  function handleToggleOverflowColumns(): void {
    const containerNarrow = narrowProbeRef.current
      ? getComputedStyle(narrowProbeRef.current).display !== 'none'
      : false
    setColumnOverflow((prev) => {
      if (prev !== 'auto') return 'auto'
      return containerNarrow ? 'shown' : 'hidden'
    })
  }

  let overflowControlLabel = 'Show or hide Tags and Updated columns'
  if (columnOverflow === 'hidden') {
    overflowControlLabel = 'Show Tags and Updated columns'
  } else if (columnOverflow === 'shown') {
    overflowControlLabel = 'Hide Tags and Updated columns'
  }

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
    // `@container` (SP-34): the Tags/Updated breakpoint is measured against
    // THIS element's own inline size — the panel's real content width —
    // never the window's.
    <div className="@container flex flex-1 flex-col overflow-hidden">
      {/* Narrow-probe (SP-34): purely a read-out of the container query below
          the breakpoint, so `handleToggleOverflowColumns` can know which way
          "toggle" means without JS re-deriving the breakpoint. Never visible;
          carries no semantics. */}
      <span ref={narrowProbeRef} aria-hidden="true" className="hidden @max-[648px]:block" />
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
            TaskRow below) has effect. Deliberate reveal reserves the Title
            floor and scrolls only this content area; auto/hidden keep their
            existing fit and visibility. */}
        <table
          className={cn(
            'w-full table-fixed text-[length:var(--type-body-compact-size)]',
            columnOverflow === 'shown' && 'min-w-[calc(31.5rem+var(--space-8)*2+var(--space-3))]',
          )}
        >
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
              {/* SP-34 — Tags and Updated are the hideable columns: narrow
                  container hides them (container query) unless the user has
                  forced them shown via the ⋯ control below. DOM nodes and
                  sort/filter state persist either way, but display:none removes
                  hidden columns from the accessibility tree. Revealing them
                  with the ⋯ control restores their accessibility exposure.
                  The visibility classes are spelled out per state as literals
                  so the design-system scanners can resolve every class (a
                  shared computed class-string variable is an unresolved
                  extension boundary for them). `auto` → only the container
                  query hides; `shown` → nothing hides; `hidden` → always
                  hidden — mutually exclusive, no cascade fights. */}
              <th
                className={cn(
                  'w-28 px-[var(--space-2)] py-[var(--space-2)] text-left',
                  columnOverflow === 'auto' && '@max-[648px]:hidden',
                  columnOverflow === 'hidden' && 'hidden',
                )}
              >
                <ColumnMenu label="Tags" filter={buildFilter(tagValues, tagFilter, setTagFilter)} />
              </th>
              <th className="w-24 px-[var(--space-2)] py-[var(--space-2)] text-left" aria-sort={ariaSort('agent')}>
                <ColumnMenu label="Agent" sort={sortCfg('agent')} filter={buildFilter(agentValues, agentFilter, setAgentFilter)} />
              </th>
              <th
                className={cn(
                  'w-28 px-[var(--space-3)] py-[var(--space-2)] text-right',
                  columnOverflow === 'auto' && '@max-[648px]:hidden',
                  columnOverflow === 'hidden' && 'hidden',
                )}
                aria-sort={ariaSort('updated')}
              >
                <ColumnMenu label="Updated" align="right" sort={sortCfg('updated')} />
              </th>
              {/* ADR-052 §6.8 — a row action column (▶ Play / ■ Stop per
                  task state). Not sortable/filterable, so it's a plain
                  header rather than a ColumnMenu trigger — but it does carry
                  the SP-34 "⋯" overflow control that toggles Tags/Updated
                  back into view when the narrow container has hidden them
                  (and hides them again from a wide one). */}
              <th className="w-10 px-[var(--space-2)] py-[var(--space-2)] text-right">
                <span className="sr-only">Actions</span>
                <Button
                  variant="ghost"
                  onClick={handleToggleOverflowColumns}
                  aria-label={overflowControlLabel}
                  title={overflowControlLabel}
                  className="h-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                >
                  <DotsThree size={14} weight="bold" />
                </Button>
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-[var(--space-3)] py-[var(--space-5)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                  {anyFilterActive ? 'No tasks match the column filters' : 'No tasks to show'}
                </td>
              </tr>
            ) : (
              sorted.map((task) => (
                <TaskRow key={task.id} task={task} agents={agents} onClick={() => onTaskClick(task)} columnOverflow={columnOverflow} />
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
            'h-auto gap-[var(--space-1)] p-0 text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider hover:bg-transparent',
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

function TaskRow({
  task,
  agents,
  onClick,
  columnOverflow,
}: {
  task: Task
  agents: AgentRef[]
  onClick: () => void
  /** SP-34 column-visibility state, threaded from ListView so the Tags and
   * Updated cells hide/show in lockstep with their headers. The visibility
   * classes themselves are inlined below as literals (scanner-resolvable). */
  columnOverflow?: 'auto' | 'shown' | 'hidden'
}) {
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
          className="rounded px-[var(--space-1)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-bold"
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
          aria-label={`${task.title}, status ${taskDisplayLabel(task)}`}
          // `title` gives a native tooltip with the full text (Finding 2 —
          // "truncate with ellipsis plus a title/tooltip"). `truncate`
          // (nowrap + overflow-hidden + ellipsis) — rather than the old
          // `line-clamp-1` (which clips with no ellipsis once the row can't
          // grow) — reads as a real single-line ellipsis, and now that the
          // table itself is `table-fixed` (see ListView's <table> above),
          // this button's `w-full` resolves against a WIDTH-STABLE column
          // instead of one that grows to fit the very content it's meant to
          // truncate.
          title={task.title}
          className="h-auto w-full min-w-0 justify-start truncate p-0 text-left text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent"
        >
          {task.title}
        </Button>
      </td>
      <td className="px-[var(--space-2)] py-[var(--space-2)]">
        {/* ADR-052 FR-015/US-8 — a user-cancelled task renders "Cancelled"
            (orange), distinct from a genuine "Failed" (red), via the shared
            taskDisplayColor/taskDisplayLabel helpers (statusColors.ts). */}
        <div className="flex items-center gap-[var(--space-1)]">
          {running && <RunningIndicator />}
          <span className="text-[length:var(--type-utility-xs-size)] font-medium" style={{ color: taskDisplayColor(task) }}>
            {taskDisplayLabel(task)}
          </span>
        </div>
      </td>
      <td
        className={cn(
          'px-[var(--space-2)] py-[var(--space-2)]',
          columnOverflow === 'auto' && '@max-[648px]:hidden',
          columnOverflow === 'hidden' && 'hidden',
        )}
      >
        {tags.length > 0 ? (
          <div className="flex max-w-[7rem] flex-wrap items-center gap-[var(--space-1)]">
            {tags.slice(0, 2).map((tag) => (
              <span
                key={tag}
                title={tag}
                className="max-w-[4rem] truncate rounded bg-[var(--color-accent)]/10 px-[var(--space-1)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] text-[var(--color-accent)]"
              >
                {tag}
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
        className={cn(
          'px-[var(--space-3)] py-[var(--space-2)] text-right',
          columnOverflow === 'auto' && '@max-[648px]:hidden',
          columnOverflow === 'hidden' && 'hidden',
        )}
      >
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{formatUpdated(task.updated_at)}</span>
      </td>
      {/* ADR-052 §6.8 row action (▶ Play / ■ Stop per task state) — always
          visible (not hover-gated) so it's reachable on touch devices, which
          can't hover a row to discover it. TaskActionButton itself already
          stops the click/pointerdown/keydown from bubbling into the row's
          own onClick (open task). */}
      <td className="px-[var(--space-2)] py-[var(--space-2)] text-right" onClick={(e) => e.stopPropagation()}>
        <TaskActionButton task={task} />
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
