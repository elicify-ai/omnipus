import { useMemo, useState } from 'react'
import { MagnifyingGlass, Plus, Star, Warning } from '@phosphor-icons/react'
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
 *
 * ADR-075 FR-047 — the elevation-of-privilege disclosure lives HERE, above the
 * candidate list, and not anywhere else.
 *
 * Adding an agent to a workspace team is not only an organisational act. Under
 * ADR-075 a browser belongs to the workspace: one Chrome, one profile, one
 * cookie jar, shared by everyone on the team. So the moment an agent joins, it
 * can drive every site that workspace is already signed in to — the operator's
 * own live sessions, not a fresh logged-out browser. Since D1.10 that reach
 * extends to unattended work: a scheduled or heartbeat turn inherits the same
 * logins with nobody watching.
 *
 * Placement is the requirement, not decoration. This flow has no confirmation
 * step — clicking a candidate calls onAdd immediately — so the click on a
 * candidate IS the confirm action, and the disclosure has to be on screen
 * before it. That rules out the three cheaper places it could have gone:
 *
 *   - a tooltip on the [+ Add agent] button (needs a hover the operator has no
 *     reason to perform, and does not exist at all on touch),
 *   - a toast after the add (the privilege is already granted by then),
 *   - release notes (explicitly not sufficient — FR-047).
 *
 * Rendering it inside the picker rather than in WorkspaceTeamTab is also what
 * makes it survive: the tab renders AddAgentPicker in TWO places (the header
 * and the empty-team state), and a disclosure attached to only one of them
 * would be a hole in exactly the flow a first-time operator takes.
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
        {/* FR-047. First thing in the popover, above the search and the
            candidates — see the component doc comment for why it is here and
            not in a tooltip, a toast, or the release notes. */}
        <div
          role="note"
          data-testid="team-add-agent-disclosure"
          className="flex items-start gap-2 border-b border-[var(--color-border)] bg-[var(--color-warning)]/10 px-2.5 py-2 text-[11px] leading-snug text-[var(--color-warning)]"
        >
          <Warning size={13} weight="fill" className="mt-px shrink-0" aria-hidden="true" />
          <span>
            An agent you add here can use this workspace&rsquo;s browser, which stays signed in to
            every site you have logged into on it. It can act as whoever this workspace is signed
            in as &mdash; including on scheduled and background turns nobody is watching.
          </span>
        </div>
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
