import { useMemo, useState } from 'react'
import { ChatCircleDots, MagnifyingGlass, Plus, Warning } from '@phosphor-icons/react'
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import { AgentMark } from '@/components/agents/AgentMark'
import type { Agent, Workspace } from '@/lib/api'
import { isWorker } from '@/lib/api'
import { isTeamEligible } from '@/lib/omnipus'
import { roleLabel } from './teamGraphModel'
import { cn } from '@/lib/utils'

/** The agents list behind the picker (spec §6.3 row "Add agent menu / picker"). */
export type PickerListState = 'loading' | 'ready' | 'stale' | 'error' // not-wire-format: UI load state of the agents list feeding the picker

interface AddAgentPickerProps {
  /** Every global agent (the agents cache). */
  agents: Agent[]
  /** Agent ids already on the team — filtered out of the picker. */
  memberIds: ReadonlySet<string>
  onAdd: (agentId: string) => void
  /**
   * omnipus-system-agent FR-103: when given (and not controlled), the popover
   * also offers "Create with Omnipus instead" below the candidate list.
   */
  onCreateWithOmnipus?: () => void
  /**
   * Every workspace, to mark candidates that are in no workspace at all
   * (F:D21, FR-104). Omitted or unknown → no marking (never a guessed mark).
   */
  workspaces?: readonly Workspace[]
  /** Load state of `agents`; default 'ready'. */
  listState?: PickerListState
  /** Retry loading the agents list (error and stale states). */
  onRetry?: () => void
  /**
   * Controlled mode for a host that owns the opening control (Team's Add
   * agent menu, FR-103): the picker opens on `open`, anchors to `anchor`
   * instead of rendering its own trigger, and shows the candidate list
   * directly.
   */
  controlled?: { open: boolean; onOpenChange: (open: boolean) => void; anchor: React.ReactNode }
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
export function AddAgentPicker({
  agents,
  memberIds,
  onAdd,
  onCreateWithOmnipus,
  workspaces,
  listState = 'ready',
  onRetry,
  controlled,
}: AddAgentPickerProps) {
  const [ownOpen, setOwnOpen] = useState(false)
  const open = controlled ? controlled.open : ownOpen
  const setOpen = controlled ? controlled.onOpenChange : setOwnOpen
  const [query, setQuery] = useState('')

  // F:D21: an agent no workspace's team lists. Unknown without the list.
  const inSomeWorkspace = useMemo(() => {
    if (!workspaces) return null
    const ids = new Set<string>()
    for (const w of workspaces) for (const id of w.core_team ?? []) ids.add(id)
    return ids
  }, [workspaces])

  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase()
    return agents
      .filter((a) => !memberIds.has(a.id))
      // FR-104: team-eligible only — never Omnipus, Judge, Plan Supervisor or
      // any other system agent, nor a retired built-in a stale agents cache
      // still carries (lib/omnipus.ts::isTeamEligible, FR-008).
      .filter((a) => isTeamEligible(a))
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
      {controlled ? (
        <PopoverAnchor asChild>{controlled.anchor}</PopoverAnchor>
      ) : (
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant="outline"
          data-testid="team-add-agent"
          className="gap-[var(--space-1)] border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-secondary)] hover:border-[var(--color-accent)]/50 hover:bg-[var(--color-surface-2)]"
        >
          <Plus size={14} weight="bold" />
          Add agent
        </Button>
      </PopoverTrigger>
      )}
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
          className="flex items-start gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-warning)]/10 px-[var(--space-2)] py-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]"
        >
          <Warning size={13} weight="fill" className="mt-[var(--border-width-hairline)] shrink-0" aria-hidden="true" />
          <span>
            An agent you add here can use this workspace&rsquo;s browser, which stays signed in to
            every site you have logged into on it. It can act as whoever this workspace is signed
            in as &mdash; including on scheduled and background turns nobody is watching.
          </span>
        </div>
        <div className="flex items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)]">
          <MagnifyingGlass size={14} className="text-[var(--color-muted)]" />
          <input tabIndex={0}
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search agents…"
            aria-label="Search agents"
            data-testid="team-add-agent-search"
            className="w-full bg-transparent text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] placeholder:text-[var(--color-muted)] focus:outline-none"
          />
        </div>
        {listState === 'stale' && (
          <div role="status" className="flex items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            <span className="flex-1">This list may be out of date.</span>
            {onRetry && <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>}
          </div>
        )}
        <div className="max-h-64 overflow-y-auto py-[var(--space-1)]" aria-busy={listState === 'loading'}>
          {listState === 'loading' ? (
            <p role="status" className="px-[var(--space-2-5)] py-[var(--space-3)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Loading agents…
            </p>
          ) : listState === 'error' ? (
            <div role="alert" className="flex flex-col items-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-3)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">
              <span>Could not load agents.</span>
              {onRetry && <Button variant="outline" size="sm" onClick={onRetry}>Retry</Button>}
            </div>
          ) : candidates.length === 0 ? (
            <p className="px-[var(--space-2-5)] py-[var(--space-3)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              {query.trim()
                ? 'No matching agents.'
                : 'No other agents'}
            </p>
          ) : (
            candidates.map((a) => (
              <Button
                key={a.id}
                variant="ghost"
                data-testid={`team-add-agent-option-${a.id}`}
                onClick={() => {
                  onAdd(a.id)
                  setOpen(false)
                  setQuery('')
                }}
                className={cn(
                  'flex h-auto w-full items-center justify-start gap-[var(--space-2)] rounded-none px-[var(--space-2)] py-[var(--space-2)] text-left transition-colors',
                  'hover:bg-[var(--color-surface-2)] focus-visible:bg-[var(--color-surface-2)]',
                )}
              >
                <AgentMark agent={a} size={26} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-[var(--space-1)]">
                    <span className="truncate text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">
                      {a.name}
                    </span>
                  </div>
                  <span className="block truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                    {roleLabel(a)}
                    {isWorker(a) ? ' · leaf' : ''}
                    {inSomeWorkspace && !inSomeWorkspace.has(a.id) ? ' · Not in any workspace' : ''}
                  </span>
                </div>
                <Plus size={13} className="shrink-0 text-[var(--color-muted)]" />
              </Button>
            ))
          )}
        </div>
        {/* FR-103: the other Add choice, available from the graph view's
            picker too (the list view offers both in its Add agent menu). */}
        {onCreateWithOmnipus && !controlled && (
          <div className="border-t border-[var(--color-border)] p-[var(--space-1)]">
            <Button
              variant="ghost"
              data-testid="team-add-agent-create-with-omnipus"
              onClick={() => {
                setOpen(false)
                onCreateWithOmnipus()
              }}
              className="h-auto w-full justify-start gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-body-compact-size)]"
            >
              <ChatCircleDots size={14} aria-hidden="true" />
              Create with Omnipus instead
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}
