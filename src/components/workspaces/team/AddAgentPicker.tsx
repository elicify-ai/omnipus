import { useMemo, useState } from 'react'
import { MagnifyingGlass, Plus, Star } from '@phosphor-icons/react'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent } from '@/lib/api'
import { isWorker } from '@/lib/api'
import { roleLabel } from './teamGraphModel'
import { cn, initialOf } from '@/lib/utils'

interface AddAgentPickerProps {
  /** Every global agent (the agents cache). */
  agents: Agent[]
  /** Agent ids already on the team — filtered out of the picker. */
  memberIds: ReadonlySet<string>
  onAdd: (agentId: string) => void
}

/**
 * [+ Add agent] — a popover picker of global agents not yet on the workspace
 * team. Selecting one adds a node (= team membership). Searchable; shows the
 * agent avatar, name, and role so the choice is unambiguous.
 */
export function AddAgentPicker({ agents, memberIds, onAdd }: AddAgentPickerProps) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')

  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase()
    return agents
      .filter((a) => !memberIds.has(a.id))
      .filter((a) => a.type !== 'system') // legacy/system agents aren't team-addable
      .filter(
        (a) =>
          q === '' ||
          a.name.toLowerCase().includes(q) ||
          (a.description ?? '').toLowerCase().includes(q),
      )
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [agents, memberIds, query])

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) setQuery('')
      }}
    >
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant="outline"
          data-testid="team-add-agent"
          className="gap-1.5 border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-secondary)] hover:border-[var(--color-accent)]/50 hover:bg-[var(--color-surface-2)]"
        >
          <Plus size={14} weight="bold" />
          Add agent
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="w-72 border-[var(--color-border)] bg-[var(--color-surface-1)] p-0"
      >
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-2">
          <MagnifyingGlass size={14} className="text-[var(--color-muted)]" />
          <input tabIndex={0}
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search agents…"
            data-testid="team-add-agent-search"
            className="w-full bg-transparent text-sm text-[var(--color-secondary)] placeholder:text-[var(--color-muted)] focus:outline-none"
          />
        </div>
        <div className="max-h-64 overflow-y-auto py-1">
          {candidates.length === 0 ? (
            <p className="px-3 py-4 text-center text-xs text-[var(--color-muted)]">
              {query.trim()
                ? 'No matching agents.'
                : 'Every agent is already on this team.'}
            </p>
          ) : (
            candidates.map((a) => (
              <button tabIndex={0}
                key={a.id}
                type="button"
                data-testid={`team-add-agent-option-${a.id}`}
                onClick={() => {
                  onAdd(a.id)
                  setOpen(false)
                  setQuery('')
                }}
                className={cn(
                  'flex w-full items-center gap-2.5 px-2.5 py-2 text-left transition-colors',
                  'hover:bg-[var(--color-surface-2)] focus-visible:bg-[var(--color-surface-2)]',
                )}
              >
                <div
                  className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-[11px] font-bold text-[var(--color-secondary)]"
                  style={{ backgroundColor: a.color ?? 'var(--color-surface-3)' }}
                  aria-hidden="true"
                >
                  {a.icon ? (
                    <IconRenderer icon={a.icon} size={13} className="text-[var(--color-secondary)]" />
                  ) : (
                    initialOf(a.name)
                  )}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1">
                    <span className="truncate text-sm font-medium text-[var(--color-secondary)]">
                      {a.name}
                    </span>
                    {a.default && (
                      <Star size={10} weight="fill" className="shrink-0 text-[var(--color-accent)]" />
                    )}
                  </div>
                  <span className="block truncate text-[11px] text-[var(--color-muted)]">
                    {roleLabel(a)}
                    {isWorker(a) ? ' · leaf' : ''}
                  </span>
                </div>
                <Plus size={13} className="shrink-0 text-[var(--color-muted)]" />
              </button>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
