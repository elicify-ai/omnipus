import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Graph, ListBullets, Plus, UsersThree } from '@phosphor-icons/react'
import { fetchAgents, fetchWorkspaceDelegation, workspacesQueryKeys } from '@/lib/api'
import { useOpenOmnipus } from '@/hooks/useOpenOmnipus'
import { useUiStore } from '@/store/ui'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ViewSwitch } from '@/components/ui/view-switch'
import { useActiveWorkspace } from './WorkspaceTabContainer'
import { AddAgentPicker, type PickerListState } from './team/AddAgentPicker'
import { TeamGraphView } from './team/TeamGraphView'
import { TeamListView, type TeamListLoad } from './team/TeamListView'
import { useTeamMembership } from './team/useTeamMembership'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'

// ── Team tab (omnipus-system-agent FR-102–FR-104, FR-109, FR-111, FR-112;
// F:D20, F:D21; ADR-20261011 OSA-10 U6/U7) ───────────────────────────────────
// The workspace's agent home: list first, delegation graph second (a
// catalogued ViewSwitch, arrow keys move between the two views). The one Add
// agent control offers "Create with Omnipus" (Omnipus's chat opens with W1
// for this workspace; the active workspace stays) and "Add an existing
// agent" (the eligibility picker). Membership writes from the list are
// explicit and truthful (useTeamMembership); the graph view keeps its own
// delegation autosave.
// ─────────────────────────────────────────────────────────────────────────────

type TeamView = 'list' | 'graph' // not-wire-format: which Team view is shown

interface WorkspaceTeamTabProps {
  workspaceId: string
}

// workspaceId is accepted for API symmetry with the route wrapper
// (workspaces.$workspaceId.team.tsx passes it through), but this component
// resolves the active workspace from WorkspaceTabContainer's context instead.
export function WorkspaceTeamTab(props: WorkspaceTeamTabProps) {
  void props
  const workspace = useActiveWorkspace()
  const [view, setView] = useState<TeamView>('list')
  const openOmnipus = useOpenOmnipus()
  const membership = useTeamMembership(workspace)

  // Close any open agent slide-over when leaving the Team tab so it doesn't
  // phantom-reopen on return.
  useEffect(() => () => useUiStore.getState().closeEditAgentSlideOver(), [])

  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 60_000 })
  // Warm the delegation graph even while the list is shown, so switching to
  // Graph opens the editor at once (same key and freshness as the graph view).
  useQuery({
    queryKey: workspacesQueryKeys.delegation(workspace.id),
    queryFn: () => fetchWorkspaceDelegation(workspace.id),
    staleTime: 15_000,
    enabled: !!workspace.id,
  })
  const workspacesQuery = useQuery({
    ...activeWorkspacesQueryOptions,
    staleTime: 30_000,
  })

  const load: TeamListLoad = agentsQuery.isPending
    ? 'loading'
    : agentsQuery.isError
      ? (agentsQuery.data ? 'stale' : 'error')
      : 'ready'
  const pickerState: PickerListState = load
  const agents = agentsQuery.data ?? []
  const memberIds = new Set(workspace.core_team ?? [])
  const busy = membership.status.kind === 'saving'

  const startWorkflow = (workflow: 'W1' | 'W18') =>
    openOmnipus({ workflow, workspaceId: workspace.id, workspaceName: workspace.name })

  const addAgent = (
    <TeamAddAgentMenu
      onCreateWithOmnipus={() => startWorkflow('W1')}
      picker={(controlled) => (
        // ADR-075 FR-047: the browser-access disclosure travels inside the picker.
        <AddAgentPicker
          agents={agents}
          memberIds={memberIds}
          workspaces={workspacesQuery.data}
          listState={pickerState}
          onRetry={() => void agentsQuery.refetch()}
          controlled={controlled}
          onAdd={(agentId) => {
            const name = agents.find((agent) => agent.id === agentId)?.name ?? agentId
            void membership.add(agentId, name)
          }}
        />
      )}
    />
  )

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden" data-testid="workspace-team-tab">
      <div className="flex flex-wrap items-center gap-[var(--space-2-5)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)]">
        <div className="flex min-w-0 flex-1 items-center gap-[var(--space-2)]">
          <UsersThree size={18} weight="duotone" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
          <h2 className="font-headline text-[length:var(--type-body-compact-size)] font-bold text-[var(--color-secondary)]">Team &amp; delegation</h2>
        </div>
        <ViewSwitch<TeamView>
          aria-label="Team view"
          value={view}
          onValueChange={setView}
          options={[
            { value: 'list', label: 'List', icon: <ListBullets size={14} aria-hidden="true" />, testId: 'team-view-list' },
            { value: 'graph', label: 'Graph', icon: <Graph size={14} aria-hidden="true" />, testId: 'team-view-graph' },
          ]}
        />
        {/* The list view's Add agent menu; the graph view carries its own picker. */}
        {view === 'list' && addAgent}
      </div>

      {membership.status.kind === 'uncertain' && (
        <div
          role="status"
          data-testid="team-membership-uncertain"
          className="flex items-center gap-[var(--space-2)] bg-[var(--color-surface-2)] px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"
        >
          <span className="flex-1">
            {membership.status.action === 'add' ? 'Adding' : 'Removing'} {membership.status.agentName} was not confirmed.
          </span>
          <Button variant="ghost" size="sm" onClick={() => void membership.retry()}>Retry</Button>
        </div>
      )}

      <div className="relative min-h-0 flex-1">
        {view === 'list' ? (
          <TeamListView
            workspace={workspace}
            agents={agents}
            load={load}
            onRetry={() => void agentsQuery.refetch()}
            workspaces={workspacesQuery.data}
            busy={busy}
            onRemove={(agentId, name) => void membership.remove(agentId, name)}
            onSetUpTeam={() => startWorkflow('W18')}
            addAgent={addAgent}
          />
        ) : (
          <TeamGraphView
            renderSaveStatus={(state) => <AutoSaveIndicator {...state} />}
            onCreateWithOmnipus={() => startWorkflow('W1')}
            workspaces={workspacesQuery.data}
          />
        )}
      </div>
      {/* The unified global-agent editor is NOT mounted here: it is
          store-driven (reads editAgentId) and hosted ONCE in the app shell. */}
    </div>
  )
}

type PickerControl = { open: boolean; onOpenChange: (open: boolean) => void; anchor: React.ReactNode } // not-wire-format: UI wiring between the Add menu and the picker

/**
 * FR-103: Team's Add agent is a menu with exactly two choices — "Create with
 * Omnipus" and "Add an existing agent" (the eligibility picker, anchored to
 * the same control). Opens on Enter/Space and on click, never on hover.
 */
function TeamAddAgentMenu({
  onCreateWithOmnipus,
  picker,
}: {
  onCreateWithOmnipus: () => void
  picker: (control: PickerControl) => React.ReactNode
}) {
  const [pickerOpen, setPickerOpen] = useState(false)
  // Set when "Add an existing agent" is chosen: the menu's own focus return
  // would otherwise dismiss the picker it is about to open.
  const openPickerOnClose = useRef(false)
  const menu = (
    <span className="inline-flex">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm" variant="outline" data-testid="team-add-agent-menu" className="gap-[var(--space-1)]">
            <Plus size={14} weight="bold" aria-hidden="true" />
            Add agent
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="min-w-[220px] border border-[var(--color-border)] bg-[var(--color-surface-1)]"
          onCloseAutoFocus={(event) => {
            if (!openPickerOnClose.current) return
            openPickerOnClose.current = false
            event.preventDefault()
            setPickerOpen(true)
          }}
        >
          <DropdownMenuItem data-testid="team-add-agent-create-with-omnipus" onSelect={onCreateWithOmnipus}>
            Create with Omnipus
          </DropdownMenuItem>
          <DropdownMenuItem
            data-testid="team-add-agent-existing"
            onSelect={() => { openPickerOnClose.current = true }}
          >
            Add an existing agent
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </span>
  )
  return <>{picker({ open: pickerOpen, onOpenChange: setPickerOpen, anchor: menu })}</>
}
