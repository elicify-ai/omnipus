import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, UsersThree } from '@phosphor-icons/react'
import {
  fetchAgents,
  fetchWorkspaceDelegation,
  updateWorkspace,
  updateWorkspaceDelegation,
  workspacesQueryKeys,
  isWorker,
  ApiError,
  isApiError,
  getCsrfCookie,
  CSRF_HEADER_NAME,
  type Workspace,
  type WorkspaceDelegation,
  type WorkspaceUpdateRequest,
  type WorkspaceDelegationUpdateRequest,
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
  DEFAULT_DEPTH_FALLBACK,
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
// Every save writes core_team (updateWorkspace) BEFORE edges
// (updateWorkspaceDelegation) — the delegation PUT validates edge endpoints
// against the STORED core_team, so a newly-added member must land there first
// or drawing an edge to it 400s. Backed by the Sprint-3 per-workspace
// delegation contract.
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceTeamTabProps {
  workspaceId: string
}

// workspaceId is accepted for API symmetry with the route wrapper
// (workspaces.$workspaceId.team.tsx passes it through), but this component
// resolves the active workspace from WorkspaceTabContainer's context instead.
export function WorkspaceTeamTab(props: WorkspaceTeamTabProps) {
  void props
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

  // D3 residual (2nd site) fix: reactive counterpart to `hydratedRef`,
  // mirroring AgentProfile's `formHydrated` / WorkspaceSettingsTab's
  // `instructionsHydrated`. useAutoSave's baseline-capture / re-arm logic
  // (see useAutoSave.ts's `wasDisabledRef`) is keyed off its `disabled`
  // OPTION actually transitioning true→false across a real committed
  // render — a `useRef` alone is invisible to that machinery (reading
  // `hydratedRef.current` at render time does not make React re-run
  // useAutoSave's internal effect). Before this fix, `hydratedRef` was set
  // once on the FIRST-ever hydration and never reset, so on every
  // subsequent workspace switch (this component persists across switches —
  // no `key` prop at the route) `disabled` stayed permanently `false`:
  // useAutoSave treated the newly-adopted workspace's own fresh
  // core_team/edges as a "genuine edit" relative to the stale previous
  // workspace's baseline, and fired a spurious `updateWorkspace` +
  // `updateWorkspaceDelegation` PUT echoing that data straight back.
  //
  // Reset via the "adjusting state during render" pattern (React docs:
  // you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes)
  // rather than a plain `useEffect` keyed on `workspaceId` — this is the
  // SAME fix WorkspaceSettingsTab's `instructionsHydrated` already uses
  // (see its own doc comment for the full story) and for the same reason: a
  // plain effect-based reset runs in the SAME batched effect-flush as the
  // hydration effect below whenever `delegation` for the new workspace is
  // already cache-resolved on the very first render of the switch (no
  // network gap) — React would coalesce `setHydrated(false)` immediately
  // followed by `setHydrated(true)` into one update with no OBSERVABLE
  // commit in between, so useAutoSave's `disabled` prop would never
  // actually be SEEN as `true` and the re-arm branch would never fire,
  // reproducing the exact spurious-PUT bug this fix exists to close. Doing
  // the reset mid-render instead guarantees the very FIRST committed render
  // after a workspace switch already carries `hydrated = false`, regardless
  // of whether the new workspace's delegation resolves synchronously
  // (cache hit) or asynchronously (network fetch).
  const [hydrated, setHydrated] = useState(false)
  const [prevWorkspaceIdForHydration, setPrevWorkspaceIdForHydration] = useState(workspaceId)
  if (workspaceId !== prevWorkspaceIdForHydration) {
    setPrevWorkspaceIdForHydration(workspaceId)
    hydratedRef.current = false
    setHydrated(false)
  }

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
      // D3 fix: flip the reactive readiness flag in the SAME commit the
      // freshly-hydrated data lands (see `hydrated`'s doc comment above) —
      // batched with `setEditState` above into one render, so there is no
      // intermediate frame where `disabled` is already false but the data
      // is still stale.
      setHydrated(true)
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
  // The auto-save `data` MUST include `members`, not just `edges` — D7 fix.
  // useAutoSave's change-detection JSON-compares consecutive `data` snapshots
  // and only debounce-fires `saveFn` when that JSON actually differs (see
  // useAutoSave.ts's "skip if data hasn't changed" guard). Adding a member
  // with no incident edge does not change `buildSaveEdges(editState)` at all
  // — the edges array is byte-identical — so an edges-only body silently
  // starves the debounce timer AND (for the same reason) the unmount flush /
  // pagehide beacon, both of which gate on `hasPendingChanges()` doing the
  // same JSON comparison. The result: an edgeless member added to the team
  // never triggers ANY save path, not even the emergency ones, and just
  // vanishes on next refetch with no autosave ever having been attempted.
  // Including `members` here doesn't change what's PERSISTED (saveFn always
  // read `editState.members` for the core_team PUT; that part worked) — it
  // fixes WHEN the save fires at all.
  const saveBody = useMemo(
    () => ({
      members: editState?.members ?? [],
      edges: editState ? buildSaveEdges(editState) : [],
    }),
    [editState],
  )

  const saveFn = useCallback(
    async (body: { members: string[]; edges: ReturnType<typeof buildSaveEdges> }) => {
      if (!hydratedRef.current) return

      // P0 fix: persist team MEMBERSHIP before the edges PUT. The backend's
      // delegation PUT (handleWorkspaceDelegationPut) validates every edge
      // endpoint against the STORED core_team ∪ stored edges — computed BEFORE
      // applying the incoming edge. An agent added to the team in this same
      // edit session was never written to core_team, so drawing a delegation
      // edge to it 400s ("not a member of the workspace team") unless
      // core_team lands first. This ordering is load-bearing: if the core_team
      // write throws, we must NOT proceed to the edges PUT — the throw
      // propagates to useAutoSave's catch, which surfaces the error via
      // AutoSaveIndicator instead of silently dropping the edit.
      const members = body.members
      const updatedWorkspace = await updateWorkspace(workspaceId, { core_team: members })

      // Land the fresh core_team on every cache a consumer might read it from.
      // NOTE: `invalidateQueries({ queryKey: workspacesQueryKeys.list() })` (no
      // params) does NOT partial-match the `{status:'active'}` / `{status:
      // 'archived'}` keyed list queries actually in use (TanStack's
      // partialMatchKey requires the filter's key segments to structurally
      // match the stored key's; `undefined` vs `{status:'active'}` mismatches)
      // — so we write the exact keys directly instead of relying on that
      // pattern (see also WorkspaceHeader.tsx / WorkspaceSettingsTab.tsx, which
      // use the same no-op invalidate; out of scope to fix here).
      queryClient.setQueryData<Workspace>(workspacesQueryKeys.detail(workspaceId), updatedWorkspace)
      for (const status of ['active', 'archived'] as const) {
        queryClient.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status }), (prev) =>
          prev?.map((w) => (w.id === workspaceId ? updatedWorkspace : w)),
        )
      }

      // F4: core_team is durably saved as of this point. If the edges PUT
      // below throws, a bare rethrow would surface via AutoSaveIndicator as
      // an undifferentiated "save failed" message — indistinguishable from
      // "nothing was saved at all" (e.g. if updateWorkspace itself had
      // thrown, above). Wrap the error so the message tells the operator
      // team membership DID land and only the delegation graph didn't. We
      // still rethrow (never swallow) so useAutoSave's catch marks the save
      // 'error' and keeps the edit pending/retryable.
      try {
        const resp = await updateWorkspaceDelegation(workspaceId, body.edges)
        // Adopt the server's computed team + edges as the new baseline so the
        // next refetch reconciles cleanly (no spurious dirty state).
        queryClient.setQueryData<WorkspaceDelegation>(
          workspacesQueryKeys.delegation(workspaceId),
          resp,
        )
        baselineRef.current = stateKey(buildTeamEditState(resp, updatedWorkspace.core_team ?? []))
      } catch (err) {
        const detail = isApiError(err)
          ? err.userMessage
          : err instanceof Error
            ? err.message
            : String(err)
        const message = `Team membership saved, but delegation edges failed: ${detail}`
        throw isApiError(err)
          ? new ApiError(err.status, message, { code: err.code, body: err.body, cause: err })
          : new Error(message, { cause: err })
      }
    },
    [workspaceId, queryClient, stateKey],
  )

  // ── F3 — emergency-flush ordering ────────────────────────────────────────
  // The page-hide/unload beacon (useAutoSave's `flushBeacon`) must honor the
  // SAME core_team-before-edges ordering as `saveFn` above, or it reintroduces
  // the exact P0 bug on the one path the P0 fix didn't cover: add a brand-new
  // member, draw an edge to it, then switch tabs/close within the 500ms
  // debounce — the built-in single-URL beacon would PUT the edges endpoint
  // alone, the backend validates the new edge's endpoint against the STORED
  // core_team (which never got the new member), and the PUT 400s. The
  // fire-and-forget `void fetch(...)` in the default beacon has no `.catch`,
  // so that 400 is entirely silent and the edit vanishes.
  //
  // Fix: fire the SAME two PUTs `saveFn` would, in the same order, as
  // `keepalive` requests. Note the honesty caveat: `keepalive: true` lets each
  // individual fetch survive page teardown, but chaining the second fetch off
  // the first's promise via `.then()` does NOT — if the page is torn down
  // between the two requests, only core_team lands. That is still a strict
  // improvement over the pre-fix behavior (edges PUT alone, silently 400ing):
  // firing core_team first makes the 400 essentially impossible in practice,
  // and the worst case leaves core_team saved (self-heals on next visit — the
  // "not connected yet" banner already surfaces this state) rather than a
  // total silent loss of the edit.
  //
  // FOLLOW-UP: the definitive fix is an atomic single-PUT delegation endpoint
  // that accepts `{core_team, edges}` together — a Constraint #8 contract
  // change (new/extended wire schema), deferred.
  const beaconFlush = useCallback(() => {
    if (!hydratedRef.current || !editState) return
    // Auth is the omnipus-session HttpOnly cookie (US-5 / FR-010) — sent
    // automatically via `credentials: 'include'`. A state-changing PUT still
    // requires the CSRF double-submit header alongside it (read fresh, never
    // cached — see getCsrfCookie's own doc comment). Mirrors the pattern in
    // useAutoSave.ts's own built-in flushBeacon path.
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    const csrf = getCsrfCookie()
    if (csrf) headers[CSRF_HEADER_NAME] = csrf

    const putKeepalive = (url: string, payload: unknown, label: string): Promise<void> =>
      fetch(url, {
        method: 'PUT',
        keepalive: true,
        credentials: 'include',
        headers,
        body: JSON.stringify(payload),
      })
        .then((res) => {
          if (!res.ok) {
            // Match the useAutoSave.ts unmount-flush precedent: a failed
            // best-effort flush is at least discoverable in the console
            // rather than silently dropped.
            console.error(
              `[WorkspaceTeamTab] beacon flush (${label}) failed:`,
              res.status,
              res.statusText,
            )
          }
        })
        .catch((err) => {
          console.error(`[WorkspaceTeamTab] beacon flush (${label}) failed:`, err)
        })

    const coreTeamUrl = `/api/v1/workspaces/${encodeURIComponent(workspaceId)}`
    const edgesUrl = `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/delegation`
    const coreTeamBody: WorkspaceUpdateRequest = { core_team: editState.members }
    const edgesBody: WorkspaceDelegationUpdateRequest = { edges: buildSaveEdges(editState) }

    // Ordered: edges only fires once the core_team fetch has settled.
    void putKeepalive(coreTeamUrl, coreTeamBody, 'core_team').then(() =>
      putKeepalive(edgesUrl, edgesBody, 'edges'),
    )
  }, [workspaceId, editState])

  const {
    status: saveStatus,
    error: saveError,
    lastSavedAt,
  } = useAutoSave(saveBody, saveFn, {
    // D3 residual (2nd site) fix: `hydrated` (reactive state), not
    // `hydratedRef.current` — see `hydrated`'s doc comment above for why a
    // ref alone cannot correctly gate useAutoSave's re-arm logic.
    disabled: !hydrated || editState === null,
    // F3: use the component-supplied ordered flush (core_team, then edges)
    // instead of the built-in single-URL beacon — see the comment above
    // `beaconFlush`'s definition for why.
    beaconFlush,
  })

  // ── Derived ───────────────────────────────────────────────────────────────
  const workerIds = useMemo(() => {
    const s = new Set<string>()
    for (const a of agents) if (isWorker(a)) s.add(a.id)
    return s
  }, [agents])

  const memberIds = useMemo(() => new Set(editState?.members ?? []), [editState])

  const graph = useMemo(() => {
    if (!editState) return { nodes: [], edges: [], defaultDepth: DEFAULT_DEPTH_FALLBACK }
    return buildTeamGraphModel(editState, agents)
  }, [editState, agents])

  // Edgeless, non-core members are queued for the next autosave rather than
  // permanently unpersisted (D7 fix): saveFn's core_team PUT writes the FULL
  // current member list unconditionally (see the ordering comment above
  // `saveFn`), regardless of whether a member has an incident edge — it's
  // only the edges-derived team computed by the delegation PUT that has an
  // edges-only view. Surface these here so the transient "not saved yet"
  // window between adding a member and the debounced autosave landing is
  // visible, not silent — it self-clears once `workspace.core_team` (below)
  // reflects the just-saved membership.
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
      // FR-018 / A5: pass the workspace id so AgentProfile renders the
      // conditional Heartbeat tab for this (workspace, agent) pair.
      openEditAgentSlideOver(agentId, workspaceId)
      if (agent) {
        addToast({
          variant: 'default',
          message: `Editing ${agent.name} — applies everywhere it's used.`,
        })
      }
    },
    [agents, openEditAgentSlideOver, addToast, workspaceId],
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
        <button tabIndex={0}
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
        {/* ADR-075 FR-047: the "this agent gets the workspace's live logins"
            disclosure travels INSIDE AddAgentPicker, so both of this tab's two
            render sites carry it. Do not replace either with a bare Button. */}
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
            {unsavedNames.join(', ')} {unsavedNames.length === 1 ? 'is' : 'are'} not saved yet —{' '}
            {unsavedNames.length === 1 ? 'it' : 'they'} will join the team on the next autosave.
            Draw a delegation edge to{' '}
            {unsavedNames.length === 1 ? 'it' : 'them'} to also enable delegation.
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
            {/* ADR-075 FR-047: second render site. The disclosure comes with
                the picker — see the header site above. */}
            <AddAgentPicker agents={agents} memberIds={memberIds} onAdd={handleAddMember} />
          </div>
        ) : (
          <WorkspaceTeamGraph
            nodes={graph.nodes}
            edges={graph.edges}
            workerIds={workerIds}
            editState={editState as TeamEditState}
            defaultDepth={graph.defaultDepth}
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
