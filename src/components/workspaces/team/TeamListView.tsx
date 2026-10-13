// TeamListView — the Team tab's first view (omnipus-system-agent FR-102,
// FR-004, FR-105, FR-109, FR-112; BDD-12.3, BDD-12.5, BDD-12.10, BDD-12.13,
// BDD-5.2; spec §6.3 row "Team list"; F:D20).
//
// One card per member: avatar, name, kind (conversation agent or worker),
// model, the other workspaces it works in, and its actions — Edit, Remove
// from this workspace, Delete everywhere — behind a menu button that keyboard
// focus and touch reach (no hover-only control). Conversation agents first,
// workers (native and external program) in their own section. Omnipus is a
// hidden member of the default workspace and never appears here (its
// self-row lives in the graph view).
//
// States: first load → skeleton cards; first load failed → error + Retry
// (never the empty-team offer); refresh failed with cached data → the cached
// cards + "may be out of date"; true empty → "No agents yet" with Set up a
// team and Add agent. Actions are disabled while a membership write runs.
// Delete everywhere lists every workspace the agent is in before it can be
// confirmed, and stays disabled while that list cannot load. (The dependency
// list and "delete everything" of FR-243 arrive with WU-20.)
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { DotsThree, UsersThree } from '@phosphor-icons/react'
import type { Agent, Workspace } from '@/lib/api'
import { deleteAgent } from '@/lib/api'
import { teamKindOf } from '@/lib/omnipus'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { useUiStore } from '@/store/ui'
import { AgentMark } from '@/components/agents/AgentMark'
import { providerBindingCopy } from '@/components/agents/AgentCard'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { Skeleton } from '@/components/ui/skeleton'

export type TeamListLoad = 'loading' | 'error' | 'stale' | 'ready' // not-wire-format: UI load state of the Team list's agents source

export interface TeamListViewProps {
  workspace: Workspace
  agents: Agent[]
  load: TeamListLoad
  onRetry: () => void
  /** Every active workspace (other-workspace names, Delete everywhere impact); undefined until loaded. */
  workspaces: Workspace[] | undefined
  /** True while a membership write runs — card actions are disabled. */
  busy: boolean
  onRemove: (agentId: string, agentName: string) => void
  onSetUpTeam: () => void
  /** The shared Add agent control, repeated in the empty state. */
  addAgent: React.ReactNode
}

type Member = { id: string; agent: Agent | undefined } // not-wire-format: one core_team id with its loaded agent, UI only

export function TeamListView(props: TeamListViewProps) {
  const { workspace, agents, load, onRetry } = props
  if (load === 'loading') return <TeamListSkeleton />
  if (load === 'error') {
    return <ErrorState message="Could not load this team." onRetry={onRetry} className="h-full" />
  }

  const byId = new Map(agents.map((agent) => [agent.id, agent]))
  const members: Member[] = (workspace.core_team ?? []).map((id) => ({ id, agent: byId.get(id) }))
  // An unknown id (not in the agents list) cannot be classified: it shows as a
  // conversation-section card so it stays visible and removable.
  const conversation = members.filter((m) => (m.agent ? teamKindOf(m.agent) === 'conversation' : true))
  const workers = members.filter((m) => m.agent && teamKindOf(m.agent) === 'worker')

  return (
    <div className="h-full overflow-y-auto p-[var(--space-3)]" data-testid="team-list-view">
      {load === 'stale' && (
        <div role="status" className="mb-[var(--space-2-5)] flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]" data-testid="team-list-stale">
          <span className="flex-1">Could not refresh — this team may be out of date.</span>
          <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>
        </div>
      )}
      {conversation.length === 0 && workers.length === 0 ? (
        <TeamEmptyState onSetUpTeam={props.onSetUpTeam} addAgent={props.addAgent} />
      ) : (
        <>
          <TeamSection title="Agents you chat with" members={conversation} {...props} />
          {workers.length > 0 && <TeamSection title="Workers" members={workers} {...props} />}
        </>
      )}
    </div>
  )
}

function TeamSection({ title, members, ...props }: TeamListViewProps & { title: string; members: Member[] }) {
  if (members.length === 0) return null
  const headingId = `team-section-${title.replace(/\s+/g, '-').toLowerCase()}`
  return (
    <section aria-labelledby={headingId} className="mb-[var(--space-4)]">
      <h3 id={headingId} className="mb-[var(--space-2)] text-[length:var(--type-caption-size)] font-semibold uppercase tracking-widest text-[var(--color-muted)]">
        {title}
      </h3>
      <ul className="grid grid-cols-1 gap-[var(--space-2-5)] sm:grid-cols-2 xl:grid-cols-3">
        {members.map((member) => (
          <li key={member.id}>
            <TeamAgentCard member={member} {...props} />
          </li>
        ))}
      </ul>
    </section>
  )
}

function kindLabel(agent: Agent | undefined): string {
  if (!agent) return 'Unknown agent'
  if (agent.type === 'subagent_3p') return 'Worker · external program'
  return teamKindOf(agent) === 'worker' ? 'Worker' : 'Conversation agent'
}

function workspacesWith(agentId: string, workspaces: Workspace[] | undefined): Workspace[] | undefined {
  return workspaces?.filter((w) => (w.core_team ?? []).includes(agentId))
}

function TeamAgentCard({ member, workspace, workspaces, busy, onRemove }: TeamListViewProps & { member: Member }) {
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)
  const [confirm, setConfirm] = useState<'remove' | 'delete' | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const { agent, id } = member
  const name = agent?.name ?? id
  const memberOf = workspacesWith(id, workspaces)
  const others = memberOf?.filter((w) => w.id !== workspace.id)
  const binding = agent ? providerBindingCopy(agent) : null
  const deleteImpactKnown = memberOf !== undefined
  // Every listed agent offers Delete everywhere (F:D20; BDD-12.18 deletes
  // built-in Mia too) — the server decides and refuses visibly.
  const canDelete = !!agent

  return (
    <Card className="flex h-full items-start gap-[var(--space-2-5)] p-[var(--space-2-5)]" data-testid={`team-card-${id}`}>
      <AgentMark agent={agent} name={name} size={40} />
      <div className="min-w-0 flex-1">
        <p className="truncate font-headline text-[length:var(--type-body-compact-size)] font-bold text-[var(--color-secondary)]">{name}</p>
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid={`team-card-kind-${id}`}>{kindLabel(agent)}</p>
        <p className="truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid={`team-card-model-${id}`}>
          {agent?.model ? <>Model <span className="text-[var(--color-secondary)]">{agent.model}</span></> : 'No model set'}
          {binding ? ` · ${binding}` : ''}
        </p>
        <p className="truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid={`team-card-workspaces-${id}`}>
          {others === undefined
            ? 'Other workspaces unknown'
            : others.length === 0
              ? 'Only in this workspace'
              : `Also in ${others.map((w) => w.name).join(', ')}`}
        </p>
      </div>
      {/* §6.8 "Popover menu (actions)": a button opens plain buttons — every
          action is reachable by keyboard focus and touch, no hover (BDD-12.10). */}
      <Popover open={menuOpen} onOpenChange={setMenuOpen}>
        <PopoverTrigger asChild>
          <IconButton
            aria-label={`Actions for ${name}`}
            data-testid={`team-card-actions-${id}`}
            disabled={busy}
            className="h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
          >
            <DotsThree size={18} weight="bold" />
          </IconButton>
        </PopoverTrigger>
        <PopoverContent align="end" className="flex w-56 flex-col border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-1)]">
          <Button
            variant="ghost"
            className="justify-start"
            aria-label={`Edit ${name}`}
            disabled={!agent}
            onClick={() => { setMenuOpen(false); openEditAgentSlideOver(id, workspace.id) }}
          >
            Edit
          </Button>
          <Button variant="ghost" className="justify-start" onClick={() => { setMenuOpen(false); setConfirm('remove') }}>
            Remove from this workspace
          </Button>
          {canDelete && (
            <Button
              variant="ghost"
              className="justify-start text-[var(--color-error)]"
              disabled={!deleteImpactKnown}
              onClick={() => { setMenuOpen(false); setConfirm('delete') }}
            >
              {deleteImpactKnown ? 'Delete everywhere…' : 'Delete everywhere (workspaces not loaded)'}
            </Button>
          )}
        </PopoverContent>
      </Popover>
      <ConfirmDialog
        open={confirm === 'remove'}
        onOpenChange={(open) => { if (!open) setConfirm(null) }}
        title={`Remove ${name} from ${workspace.name}?`}
        description={`${name} stays available in its other workspaces and can be added back later.`}
        confirmLabel="Remove"
        destructive
        onConfirm={() => {
          setConfirm(null)
          onRemove(id, name)
        }}
      />
      {canDelete && memberOf && (
        <DeleteEverywhereDialog
          open={confirm === 'delete'}
          onOpenChange={(open) => { if (!open) setConfirm(null) }}
          agent={agent}
          memberOf={memberOf}
        />
      )}
    </Card>
  )
}

function DeleteEverywhereDialog({
  open,
  onOpenChange,
  agent,
  memberOf,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  agent: Agent
  memberOf: Workspace[]
}) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const mutation = useMutation({
    mutationFn: () => deleteAgent(agent.id, agent.revision),
    onSuccess: () => {
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ variant: 'success', message: `Deleted ${agent.name} everywhere` })
    },
    onError: (error: unknown) => {
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({ variant: 'error', message: `Could not delete ${agent.name}: ${mutationFailureMessage(error)}` })
    },
  })
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete ${agent.name} everywhere?`}
      description={
        <span data-testid="team-delete-impact">
          {memberOf.length === 0
            ? `${agent.name} is in no workspace. Deleting removes the agent for good.`
            : `${agent.name} is removed from every workspace it works in: ${memberOf.map((w) => w.name).join(', ')}. Deleting removes the agent for good.`}
        </span>
      }
      confirmLabel="Delete everywhere"
      destructive
      pending={mutation.isPending}
      onConfirm={() => mutation.mutate()}
    />
  )
}

function TeamEmptyState({ onSetUpTeam, addAgent }: { onSetUpTeam: () => void; addAgent: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-[var(--space-2-5)] rounded-xl border border-dashed border-[var(--color-border)] bg-[var(--color-surface-0)] p-[var(--space-5)] text-center" data-testid="team-empty">
      <UsersThree size={32} weight="duotone" className="text-[var(--color-muted)]" aria-hidden="true" />
      <div>
        <p className="font-headline text-[length:var(--type-body-compact-size)] font-bold text-[var(--color-secondary)]">No agents yet</p>
        <p className="mt-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          Let Omnipus set up a whole team for this workspace, or add one agent.
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-[var(--space-2)]">
        <Button data-testid="team-set-up-team" onClick={onSetUpTeam}>Set up a team</Button>
        {addAgent}
      </div>
    </div>
  )
}

function TeamListSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-[var(--space-2-5)] p-[var(--space-3)] sm:grid-cols-2 xl:grid-cols-3" data-testid="team-list-loading" aria-busy="true">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-[var(--space-8)] w-full rounded-xl" />
      ))}
    </div>
  )
}
