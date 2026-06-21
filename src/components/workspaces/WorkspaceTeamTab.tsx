import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, UsersThree } from '@phosphor-icons/react'
import {
  fetchAgents,
  fetchWorkspaceDelegation,
  updateWorkspaceDelegation,
  workspacesQueryKeys,
  isWorker,
  type WorkspaceDelegation,
} from '@/lib/api'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { AgentProfile } from '@/components/agents/AgentProfile'
import { useUiStore } from '@/store/ui'
import { useActiveWorkspace } from './WorkspaceTabContainer'
import { WorkspaceTeamGraph } from './team/WorkspaceTeamGraph'
import { AddAgentPicker } from './team/AddAgentPicker'
import {
  addEdge,
  addMember,
  buildSaveEdges,
  buildTeamEditState,
  buildTeamGraphModel,
  removeEdge,
  removeMember,
  setEdgeDepth,
  toggleEdgeMode,
  unsavedMembers,
  type DelegationMode,
  type TeamEditState,
} from './team/teamGraphModel'

// ── F2 — Team tab: the per-workspace DELEGATION GRAPH editor ─────────────────
// Nodes = the agents on this workspace's team; edges = delegation (drag node→
// node to connect; click an edge → modes + depth). [+ Add agent] adds a node
// (team membership); the node trash removes it (and its edges). Click a node →
// the existing AgentProfile slide-over edits the GLOBAL agent definition.
// Membership + edges both persist via updateWorkspaceDelegation (debounced
// auto-save). Backed by the Sprint-3 per-workspace delegation contract.
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceTeamTabProps {
  workspaceId: string
}

function readAuthToken(): string | undefined {
  // Same inline read the rest of the app uses (sessionStorage preferred,
  // localStorage fallback) — keeps the keepalive flush authorized.
  if (typeof sessionStorage === 'undefined') return undefined
  return (
    sessionStorage.getItem('omnipus_auth_token') ??
    localStorage.getItem('omnipus_auth_token') ??
    undefined
  )
}

export function WorkspaceTeamTab(_props: WorkspaceTeamTabProps) {
  const workspace = useActiveWorkspace()
  const workspaceId = workspace.id
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)

  // Close any open agent slide-over when leaving the Team tab so it doesn't
  // phantom-reopen on return (mirrors the /agents layout cleanup).
  useEffect(() => () => useUiStore.getState().closeEditAgentSlideOver(), [])

  const {
    data: delegation,
    isLoading: delegationLoading,
    isError: delegationError,
    refetch: refetchDelegation,
  } = useQuery({
    queryKey: workspacesQueryKeys.delegation(workspaceId),
    queryFn: () => fetchWorkspaceDelegation(workspaceId),
    staleTime: 15_000,
    enabled: !!workspaceId,
  })

  const {
    data: agents = [],
    isLoading: agentsLoading,
    isError: agentsError,
  } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  // ── Edit state ────────────────────────────────────────────────────────────
  // Seeded from the contract (team[] ∪ edges, falling back to core_team). The
  // server baseline is tracked so a background refetch can adopt server changes
  // without clobbering in-flight edits, and so dirtiness is computed honestly.
  const [editState, setEditState] = useState<TeamEditState | null>(null)
  const baselineRef = useRef<string>('')
  const hydratedRef = useRef(false)

  const stateKey = useCallback(
    (s: TeamEditState) =>
      JSON.stringify(buildSaveEdges(s)) + '|' + [...s.members].sort().join(','),
    [],
  )

  useEffect(() => {
    if (!delegation) return
    const seed = workspace.core_team ?? []
    const next = buildTeamEditState(delegation, seed)
    const nextKey = stateKey(next)
    if (!hydratedRef.current) {
      hydratedRef.current = true
      baselineRef.current = nextKey
      setEditState(next)
      return
    }
    // Background refetch: adopt the new server value only when the user has no
    // pending edits (local state still equals the previous baseline).
    setEditState((prev) => {
      if (!prev) {
        baselineRef.current = nextKey
        return next
      }
      const adopt = stateKey(prev) === baselineRef.current
      baselineRef.current = nextKey
      return adopt ? next : prev
    })
  }, [delegation, workspace.core_team, stateKey])

  // ── Auto-save ───────────────────────────────────────────────────────────────
  // The auto-save `data` is the request BODY shape ({ edges }) so the keepalive
  // page-hide flush (PUT delegation) sends exactly the right payload.
  const saveBody = useMemo(
    () => ({ edges: editState ? buildSaveEdges(editState) : [] }),
    [editState],
  )

  const saveFn = useCallback(
    async (body: { edges: ReturnType<typeof buildSaveEdges> }) => {
      if (!hydratedRef.current) return
      const resp = await updateWorkspaceDelegation(workspaceId, body.edges)
      // Adopt the server's computed team + edges as the new baseline so the next
      // refetch reconciles cleanly (no spurious dirty state).
      queryClient.setQueryData<WorkspaceDelegation>(
        workspacesQueryKeys.delegation(workspaceId),
        resp,
      )
      baselineRef.current = stateKey(buildTeamEditState(resp, workspace.core_team ?? []))
    },
    [workspaceId, queryClient, workspace.core_team, stateKey],
  )

  const {
    status: saveStatus,
    error: saveError,
    lastSavedAt,
  } = useAutoSave(saveBody, saveFn, {
    disabled: !hydratedRef.current || editState === null,
    flushUrl: `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/delegation`,
    flushAuthToken: readAuthToken(),
  })

  // ── Derived ───────────────────────────────────────────────────────────────
  const workerIds = useMemo(() => {
    const s = new Set<string>()
    for (const a of agents) if (isWorker(a)) s.add(a.id)
    return s
  }, [agents])

  const memberIds = useMemo(() => new Set(editState?.members ?? []), [editState])

  const graph = useMemo(() => {
    if (!editState) return { nodes: [], edges: [] }
    return buildTeamGraphModel(editState, agents)
  }, [editState, agents])

  // Edgeless, non-core members are NOT persisted (the PUT is edges-only; the
  // backend derives team = core_team ∪ edge endpoints). Surface them so an agent
  // the user added doesn't silently vanish on refetch — the fix is to connect it
  // with a delegation edge.
  const coreTeamSet = useMemo(
    () => new Set(workspace.core_team ?? []),
    [workspace.core_team],
  )
  const unsaved = useMemo(
    () => (editState ? unsavedMembers(editState, coreTeamSet) : []),
    [editState, coreTeamSet],
  )
  const unsavedNames = useMemo(
    () => unsaved.map((id) => agents.find((a) => a.id === id)?.name ?? id),
    [unsaved, agents],
  )

  // ── Mutations ─────────────────────────────────────────────────────────────
  const handleConnect = useCallback(
    (from: string, to: string) =>
      setEditState((prev) => (prev ? addEdge(prev, from, to, workerIds) : prev)),
    [workerIds],
  )
  const handleToggleMode = useCallback(
    (from: string, to: string, mode: DelegationMode) =>
      setEditState((prev) => (prev ? toggleEdgeMode(prev, from, to, mode) : prev)),
    [],
  )
  const handleSetDepth = useCallback(
    (from: string, to: string, depth: number | undefined) =>
      setEditState((prev) => (prev ? setEdgeDepth(prev, from, to, depth) : prev)),
    [],
  )
  const handleDeleteEdge = useCallback(
    (from: string, to: string) =>
      setEditState((prev) => (prev ? removeEdge(prev, from, to) : prev)),
    [],
  )
  const handleAddMember = useCallback(
    (agentId: string) =>
      setEditState((prev) => (prev ? addMember(prev, agentId) : prev)),
    [],
  )
  const handleRemoveMember = useCallback(
    (agentId: string) => {
      const agent = agents.find((a) => a.id === agentId)
      if (agent?.default) {
        addToast({
          variant: 'error',
          message: `Cannot remove ${agent.name} — it is the default agent.`,
        })
        return
      }
      setEditState((prev) => (prev ? removeMember(prev, agentId) : prev))
    },
    [agents, addToast],
  )

  const handleOpenAgent = useCallback(
    (agentId: string) => {
      const agent = agents.find((a) => a.id === agentId)
      openEditAgentSlideOver(agentId)
      if (agent) {
        addToast({
          variant: 'default',
          message: `Editing ${agent.name} — applies everywhere it's used.`,
        })
      }
    },
    [agents, openEditAgentSlideOver, addToast],
  )

  const handleRejectConnection = useCallback(
    (reason: string) => addToast({ variant: 'error', message: reason }),
    [addToast],
  )

  // ── States ──────────────────────────────────────────────────────────────────
  const loading = (delegationLoading || agentsLoading) && !editState
  if (loading) return <TeamGraphSkeleton />

  if (delegationError && !editState) {
    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 p-8 text-center">
        <p className="text-sm text-[var(--color-error)]">
          Failed to load this workspace's delegation graph.
        </p>
        <button
          type="button"
          onClick={() => void refetchDelegation()}
          className="text-xs text-[var(--color-accent)] underline underline-offset-2"
        >
          Retry
        </button>
      </div>
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col overflow-hidden">
      {/* Header: title + the one cue, the add-agent action, and save status. */}
      <div className="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-2.5">
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <UsersThree size={18} weight="duotone" className="shrink-0 text-[var(--color-accent)]" />
          <div className="min-w-0">
            <h2 className="font-headline text-sm font-bold text-[var(--color-secondary)]">
              Team &amp; delegation
            </h2>
            <p className="hidden truncate text-[11px] text-[var(--color-muted)] sm:block">
              Add an agent to drop a node · drag the gold dot to delegate · click an edge to tune
              modes &amp; depth
            </p>
          </div>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} lastSavedAt={lastSavedAt} />
        <AddAgentPicker agents={agents} memberIds={memberIds} onAdd={handleAddMember} />
      </div>

      {agentsError && (
        <div className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)]">
          <Info size={12} weight="fill" />
          Agent details failed to load — node names may show as ids.
        </div>
      )}

      {unsavedNames.length > 0 && (
        <div
          role="status"
          data-testid="team-unsaved-members"
          className="flex items-center gap-1.5 bg-[var(--color-warning)]/10 px-4 py-1.5 text-[11px] text-[var(--color-warning)]"
        >
          <Info size={12} weight="fill" className="shrink-0" />
          <span>
            {unsavedNames.join(', ')} {unsavedNames.length === 1 ? 'is' : 'are'} not connected yet —
            connect {unsavedNames.length === 1 ? 'it' : 'them'} with a delegation edge to keep{' '}
            {unsavedNames.length === 1 ? 'it' : 'them'} on the team (membership saves with edges).
          </span>
        </div>
      )}

      <div className="relative flex-1 min-h-0 p-3">
        {graph.nodes.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-[var(--color-border)] bg-[var(--color-surface-0)] p-8 text-center">
            <UsersThree size={32} weight="duotone" className="text-[var(--color-muted)]" />
            <div>
              <p className="font-headline text-sm font-bold text-[var(--color-secondary)]">
                No agents on this team yet
              </p>
              <p className="mt-1 text-xs text-[var(--color-muted)]">
                Add an agent to drop the first node, then draw delegation edges between them.
              </p>
            </div>
            <AddAgentPicker agents={agents} memberIds={memberIds} onAdd={handleAddMember} />
          </div>
        ) : (
          <WorkspaceTeamGraph
            nodes={graph.nodes}
            edges={graph.edges}
            workerIds={workerIds}
            editState={editState as TeamEditState}
            onConnect={handleConnect}
            onToggleMode={handleToggleMode}
            onSetDepth={handleSetDepth}
            onDeleteEdge={handleDeleteEdge}
            onRemoveMember={handleRemoveMember}
            onRejectConnection={handleRejectConnection}
            onOpenAgent={handleOpenAgent}
          />
        )}
      </div>

      {/* The shared global-agent editor. Store-driven (reads editAgentId), so
          opening it here edits the SAME global definition as the Agents screen. */}
      <AgentProfile />
    </div>
  )
}

function TeamGraphSkeleton() {
  return (
    <div className="absolute inset-0 flex flex-col">
      <div className="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-2.5">
        <div className="h-5 w-40 rounded bg-[var(--color-surface-2)] animate-pulse" />
        <div className="ml-auto h-8 w-24 rounded bg-[var(--color-surface-2)] animate-pulse" />
      </div>
      <div className="flex-1 p-3">
        <div className="relative h-full overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-0)]">
          <div className="flex h-full flex-col items-center justify-center gap-10">
            <div className="flex gap-16">
              <div className="h-[72px] w-[200px] rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse" />
            </div>
            <div className="flex gap-16">
              {[0, 1, 2].map((i) => (
                <div
                  key={i}
                  className="h-[72px] w-[200px] rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
                />
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
