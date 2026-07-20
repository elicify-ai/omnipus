import { useState } from 'react'
import { PRIORITY_BADGE } from './TaskCard'
import { cn } from '@/lib/utils'
// 6-state unified vocabulary + colour — single source of truth.
import { STATUS_ORDER, statusColor, statusLabel } from '@/lib/statusColors'
import type { Task } from '@/lib/api'

type SortKey = 'priority' | 'status' | 'updated'
type SortDir = 'asc' | 'desc'

interface ListViewProps {
  /** Already filtered by the Tasks screen (plan / agent / tag). List just sorts + renders. */
  tasks: Task[]
  agents: { id: string; name: string }[]
  onTaskClick: (task: Task) => void
}

// Status sort rank derived from the canonical lifecycle order.
const STATUS_RANK: Record<string, number> = Object.fromEntries(STATUS_ORDER.map((s, i) => [s, i]))

/**
 * Flat, minimalist task table. Filtering is owned by the Tasks screen toolbar
 * (plan band + Agent + Tags) — this view has NO filter bar of its own (it used
 * to duplicate them). It sorts by Priority / Status / Updated via clickable
 * column headers, over a borderless table: no filled header slab, no row rules
 * — rows separate by padding + hover only.
 */
export function ListView({ tasks, agents, onTaskClick }: ListViewProps) {
  const [sortKey, setSortKey] = useState<SortKey>('updated')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // Filter out heartbeat/non-user surface tasks from the general list view.
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)

  const sorted = [...userTasks].sort((a, b) => {
    let cmp = 0
    if (sortKey === 'priority') cmp = (a.priority ?? 3) - (b.priority ?? 3)
    else if (sortKey === 'status') cmp = (STATUS_RANK[a.status] ?? 99) - (STATUS_RANK[b.status] ?? 99)
    else cmp = new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime()
    return sortDir === 'asc' ? cmp : -cmp
  })

  function sortBy(key: SortKey) {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      // Natural default per column: newest-first for time, most-critical/earliest first otherwise.
      setSortDir(key === 'updated' ? 'desc' : 'asc')
    }
  }

  const arrow = (key: SortKey) => (sortKey === key ? (sortDir === 'asc' ? ' ↑' : ' ↓') : '')
  const ariaSort = (key: SortKey): 'ascending' | 'descending' | 'none' =>
    sortKey === key ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <div className="flex-1 overflow-y-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 border-b border-[var(--color-border)]/15 bg-[var(--color-surface-0)]">
            <tr>
              <th className="w-12 px-4 py-2 text-left" aria-sort={ariaSort('priority')}>
                <SortHeader label={`Pri${arrow('priority')}`} onClick={() => sortBy('priority')} />
              </th>
              <th className="px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                Title
              </th>
              <th className="w-24 px-2 py-2 text-left" aria-sort={ariaSort('status')}>
                <SortHeader label={`Status${arrow('status')}`} onClick={() => sortBy('status')} />
              </th>
              <th className="w-28 px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                Tags
              </th>
              <th className="w-24 px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                Agent
              </th>
              <th className="w-28 px-4 py-2 text-right" aria-sort={ariaSort('updated')}>
                <SortHeader label={`Updated${arrow('updated')}`} onClick={() => sortBy('updated')} align="right" />
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-xs text-[var(--color-muted)]">
                  No tasks to show
                </td>
              </tr>
            ) : (
              sorted.map((task) => (
                <TaskRow key={task.id} task={task} agents={agents} onClick={() => onTaskClick(task)} />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/** Clickable, muted column-header sort control (no chrome — text only). */
function SortHeader({
  label,
  onClick,
  align = 'left',
}: {
  label: string
  onClick: () => void
  align?: 'left' | 'right'
}) {
  return (
    <button
      tabIndex={0}
      type="button"
      onClick={onClick}
      aria-label={`Sort by ${label.replace(/[↑↓]/g, '').trim()}`}
      className={cn(
        'text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] transition-colors hover:text-[var(--color-secondary)]',
        align === 'right' && 'ml-auto block',
      )}
    >
      {label}
    </button>
  )
}

function TaskRow({
  task,
  agents,
  onClick,
}: {
  task: Task
  agents: { id: string; name: string }[]
  onClick: () => void
}) {
  const priority = task.priority ?? 3
  const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
  const tags = task.tags ?? []
  const agentName = task.agent_name ?? (task.agent_id ? (agents.find((a) => a.id === task.agent_id)?.name ?? task.agent_id) : null)

  return (
    // The row is mouse-clickable for whole-row convenience; the REAL keyboard/AT
    // entry point is the Title button below (one tab stop per row, announced as
    // actionable). Borderless — separation is padding + hover, not a rule.
    <tr
      onClick={onClick}
      className="cursor-pointer transition-colors hover:bg-[var(--color-surface-2)]/40"
    >
      <td className="px-4 py-2.5">
        <span className={cn('rounded px-1.5 py-0.5 text-[10px] font-bold', badge.className)}>
          {badge.label}
        </span>
      </td>
      <td className="px-2 py-2.5">
        <button tabIndex={0}
          type="button"
          onClick={(e) => {
            // The <tr> also has onClick — stop the button's click bubbling so it
            // doesn't fire onClick twice.
            e.stopPropagation()
            onClick()
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              onClick()
            }
          }}
          aria-label={`${task.title}, status ${statusLabel(task.status)}`}
          className="block w-full text-left text-sm text-[var(--color-secondary)] line-clamp-1"
        >
          {task.title}
        </button>
      </td>
      <td className="px-2 py-2.5">
        <span className="text-xs font-medium" style={{ color: statusColor(task.status) }}>
          {statusLabel(task.status)}
        </span>
      </td>
      <td className="px-2 py-2.5">
        {tags.length > 0 ? (
          <div className="flex max-w-[7rem] flex-wrap items-center gap-1">
            {tags.slice(0, 2).map((tag) => (
              <span
                key={tag}
                title={tag}
                className="max-w-[4rem] truncate rounded bg-[var(--color-accent)]/10 px-1 py-0.5 text-[10px] text-[var(--color-accent)]"
              >
                {tag}
              </span>
            ))}
            {tags.length > 2 && (
              <span className="text-[10px] text-[var(--color-muted)]">+{tags.length - 2}</span>
            )}
          </div>
        ) : (
          <span className="text-xs text-[var(--color-muted)]">—</span>
        )}
      </td>
      <td className="px-2 py-2.5">
        {agentName ? (
          <span className="block max-w-[5rem] truncate text-xs text-[var(--color-secondary)]">
            {agentName}
          </span>
        ) : (
          <span className="text-xs text-[var(--color-muted)]">—</span>
        )}
      </td>
      <td className="px-4 py-2.5 text-right">
        <span className="text-[10px] text-[var(--color-muted)]">
          {formatUpdated(task.updated_at)}
        </span>
      </td>
    </tr>
  )
}

// not-wire-format
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
