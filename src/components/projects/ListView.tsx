import { useState } from 'react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { PRIORITY_BADGE } from './TaskCard'
import { cn } from '@/lib/utils'
import type { BoardTask } from '@/lib/api'
import type { MilestoneWithProgress } from '@/lib/api'

type SortDir = 'desc' | 'asc'

const STATUS_LABELS: Record<string, string> = {
  inbox:   'Inbox',
  next:    'Next',
  active:  'Active',
  waiting: 'Waiting',
  done:    'Done',
  failed:  'Failed',
}

const STATUS_COLORS: Record<string, string> = {
  inbox:   'text-[var(--color-muted)]',
  next:    'text-blue-400',
  active:  'text-yellow-400',
  waiting: 'text-purple-400',
  done:    'text-green-400',
  failed:  'text-red-400',
}

interface ListViewProps {
  tasks: BoardTask[]
  milestones: MilestoneWithProgress[]
  agents: { id: string; name: string }[]
  onTaskClick: (task: BoardTask) => void
}

export function ListView({ tasks, milestones, agents, onTaskClick }: ListViewProps) {
  const [filterStatus, setFilterStatus] = useState<string>('__all__')
  const [filterPriority, setFilterPriority] = useState<string>('__all__')
  const [filterMilestone, setFilterMilestone] = useState<string>('__all__')
  const [filterAgent, setFilterAgent] = useState<string>('__all__')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const filtered = tasks
    .filter((t) => filterStatus === '__all__' || t.status === filterStatus)
    .filter((t) => filterPriority === '__all__' || String(t.priority ?? 3) === filterPriority)
    .filter((t) => filterMilestone === '__all__' || (t.milestone_id ?? '') === filterMilestone)
    .filter((t) => filterAgent === '__all__' || (t.agent_id ?? '') === filterAgent)
    .sort((a, b) => {
      const diff = new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
      return sortDir === 'desc' ? diff : -diff
    })

  function toggleSort() {
    setSortDir((d) => (d === 'desc' ? 'asc' : 'desc'))
  }

  return (
    <div className="flex flex-col flex-1 overflow-hidden">
      {/* Filters */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-[var(--color-border)] flex-wrap flex-shrink-0">
        <FilterSelect
          label="Status"
          value={filterStatus}
          onValueChange={setFilterStatus}
          options={[
            { value: '__all__', label: 'All statuses' },
            ...Object.entries(STATUS_LABELS).map(([v, l]) => ({ value: v, label: l })),
          ]}
        />
        <FilterSelect
          label="Priority"
          value={filterPriority}
          onValueChange={setFilterPriority}
          options={[
            { value: '__all__', label: 'All priorities' },
            { value: '1', label: 'P1 — Critical' },
            { value: '2', label: 'P2 — High' },
            { value: '3', label: 'P3 — Medium' },
            { value: '4', label: 'P4 — Low' },
            { value: '5', label: 'P5 — Minimal' },
          ]}
        />
        {milestones.length > 0 && (
          <FilterSelect
            label="Milestone"
            value={filterMilestone}
            onValueChange={setFilterMilestone}
            options={[
              { value: '__all__', label: 'All milestones' },
              { value: '', label: 'Unscheduled' },
              ...milestones.map((m) => ({ value: m.id, label: m.name })),
            ]}
          />
        )}
        {agents.length > 0 && (
          <FilterSelect
            label="Agent"
            value={filterAgent}
            onValueChange={setFilterAgent}
            options={[
              { value: '__all__', label: 'All agents' },
              { value: '', label: 'Unassigned' },
              ...agents.map((a) => ({ value: a.id, label: a.name })),
            ]}
          />
        )}
      </div>

      {/* Table */}
      <div className="flex-1 overflow-y-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
            <tr>
              <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] w-12">
                Pri
              </th>
              <th className="px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                Name
              </th>
              <th className="px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] w-20">
                Status
              </th>
              <th className="px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] w-28">
                Milestone
              </th>
              <th className="px-2 py-2 text-left text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] w-24">
                Agent
              </th>
              <th className="px-4 py-2 text-right text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] w-28">
                <button
                  type="button"
                  onClick={toggleSort}
                  className="hover:text-[var(--color-secondary)] transition-colors"
                  aria-label={`Sort by updated ${sortDir === 'desc' ? 'ascending' : 'descending'}`}
                >
                  Updated {sortDir === 'desc' ? '↓' : '↑'}
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-xs text-[var(--color-muted)]">
                  No tasks match the current filters
                </td>
              </tr>
            ) : (
              filtered.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  milestones={milestones}
                  onClick={() => onTaskClick(task)}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function TaskRow({
  task,
  milestones,
  onClick,
}: {
  task: BoardTask
  milestones: MilestoneWithProgress[]
  onClick: () => void
}) {
  const priority = task.priority ?? 3
  const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
  const milestone = task.milestone_id ? milestones.find((m) => m.id === task.milestone_id) : null

  return (
    <tr
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick()
        }
      }}
      className="border-b border-[var(--color-border)]/50 hover:bg-[var(--color-surface-2)]/40 cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-accent)]"
    >
      <td className="px-4 py-2.5">
        <span className={cn('rounded border px-1.5 py-0.5 text-[10px] font-bold', badge.className)}>
          {badge.label}
        </span>
      </td>
      <td className="px-2 py-2.5">
        <span className="text-sm text-[var(--color-secondary)] line-clamp-1">{task.name}</span>
      </td>
      <td className="px-2 py-2.5">
        <span className={cn('text-xs font-medium', STATUS_COLORS[task.status] ?? 'text-[var(--color-muted)]')}>
          {STATUS_LABELS[task.status] ?? task.status}
        </span>
      </td>
      <td className="px-2 py-2.5">
        {milestone ? (
          <span className="text-xs text-[var(--color-accent)] truncate max-w-[6rem] block">
            {milestone.name}
          </span>
        ) : (
          <span className="text-xs text-[var(--color-muted)]">—</span>
        )}
      </td>
      <td className="px-2 py-2.5">
        {task.agent_id ? (
          <span className="text-xs text-[var(--color-secondary)] truncate max-w-[5rem] block">
            {task.agent_id}
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

function FilterSelect({
  label,
  value,
  onValueChange,
  options,
}: {
  label: string
  value: string
  onValueChange: (v: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <Select value={value} onValueChange={onValueChange}>
      <SelectTrigger
        className="h-7 text-xs bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)] w-auto min-w-[7rem]"
        aria-label={`Filter by ${label}`}
      >
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value} className="text-xs">
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
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
