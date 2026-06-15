import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  ArrowLeft,
  ShareNetwork,
  FloppyDisk,
  ArrowCounterClockwise,
} from '@phosphor-icons/react'
import { DelegationGraph } from '@/components/agents/delegation/DelegationGraph'
import {
  addDelegationEdge,
  buildGraphModel,
  buildSavePayload,
  buildSourceEdits,
  changedSourceIds,
  reconcileEdits,
  removeDelegationEdge,
  setSourceDepth,
  toggleSourceMode,
  type DelegationMode,
  type SourcePolicyEdit,
} from '@/components/agents/delegation/graphModel'
import { Button } from '@/components/ui/button'
import { fetchAgents, isWorker, updateAgent, type Agent } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

// Editable delegation graph screen (Spec-3 FR-6.2 / US-3 / NFR-7).
//
// Renders the roster as a VISIBLE, EDITABLE directed delegation graph (the
// concept's intent): nodes are agents, edges are `delegation_policy.to`
// entries labelled with the source's modes (+ depth cap). Drag node → node to
// create an edge; click an edge to toggle modes / set depth / delete it.
//
// Editing is LOCAL until saved. Save issues one PUT per CHANGED source agent
// (each source's out-edges = that agent's delegation_policy). On success the
// ['agents'] query is invalidated so the graph reflects persisted state.
//
// HARD CONSTRAINT (NFR-7): only `to`, `modes`, `depth` are ever read or
// written. `accept_from` and `budget` are never touched.

function buildWorkerIds(agents: Agent[]): Set<string> {
  return new Set(agents.filter((a) => isWorker(a)).map((a) => a.id))
}

export function TrustGraphScreen() {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const {
    data: agents = [],
    isLoading,
    isError,
    refetch,
  } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Pristine edit state derived from the server roster. `edits` is the live,
  // user-mutated copy; `baseline` is what was last loaded/saved (for diffing).
  const baseline = useMemo(() => buildSourceEdits(agents), [agents])
  const [edits, setEdits] = useState<Record<string, SourcePolicyEdit>>(baseline)

  // Track the baseline that `edits` was last reconciled against, so a refetch
  // can tell which agents the user had actually changed (still-dirty) from
  // those that were untouched (safe to adopt the new server value).
  const prevBaselineRef = useRef(baseline)

  // Re-seed local edits whenever the server roster changes (initial load,
  // post-save invalidation, background refetch). RECONCILE rather than blind
  // overwrite: untouched agents adopt the fresh server value; still-dirty
  // agents keep their pending edit. After a PARTIAL save this leaves only the
  // genuinely-failed sources dirty, and a Reset can never diverge from disk.
  useEffect(() => {
    const prevBaseline = prevBaselineRef.current
    prevBaselineRef.current = baseline
    setEdits((prev) => reconcileEdits(baseline, prevBaseline, prev))
  }, [baseline])

  const workerIds = useMemo(() => buildWorkerIds(agents), [agents])

  const { nodes, edges } = useMemo(
    () => buildGraphModel(agents, edits),
    [agents, edits],
  )

  const dirtyIds = useMemo(
    () => changedSourceIds(baseline, edits),
    [baseline, edits],
  )
  const isDirty = dirtyIds.length > 0

  const saveMutation = useMutation({
    // Returns the per-agent outcome. Run sequentially so a mid-flight failure
    // leaves a clear, reproducible state. We do NOT throw on partial failure:
    // the earlier PUTs ARE persisted, so the result must report exactly which
    // sources succeeded and which failed (the toast + invalidation depend on
    // it). Throwing would falsely imply nothing was saved.
    mutationFn: async (sourceIds: string[]) => {
      const attempted = sourceIds.length
      const failed: { id: string; message: string }[] = []
      for (const id of sourceIds) {
        const edit = edits[id]
        if (!edit) continue
        try {
          await updateAgent(id, { delegation_policy: buildSavePayload(edit) })
        } catch (err) {
          const message = isApiError(err)
            ? err.userMessage
            : err instanceof Error
              ? err.message
              : 'Save failed'
          failed.push({ id, message })
        }
      }
      return { attempted, failed }
    },
    onSuccess: ({ attempted, failed }) => {
      const saved = attempted - failed.length
      if (failed.length === 0) {
        addToast({ message: 'Delegation graph saved', variant: 'success' })
      } else {
        // PARTIAL FAILURE: some PUTs landed, some didn't. Name the failed
        // agents and their error; the succeeded ones re-seed from the refetch
        // below and drop out of the dirty set, leaving only the real failures.
        const failedNames = failed
          .map((f) => {
            const name = agents.find((a) => a.id === f.id)?.name ?? f.id
            return `${name}: ${f.message}`
          })
          .join('; ')
        addToast({
          message: `Saved ${saved} of ${attempted}; failed: ${failedNames}`,
          variant: 'error',
        })
      }
      // Always invalidate so the graph reflects PERSISTED state. Even on partial
      // failure this re-seeds baseline from the agents that DID save, so they
      // drop from the dirty set and a subsequent Reset can't diverge from disk —
      // only the genuinely-failed sources remain dirty.
      queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
    onError: (err) => {
      // Reached only on an unexpected throw (not a per-agent PUT failure, which
      // is captured above). Keep the edits; surface the message. No silent fail.
      // Still invalidate so local state can't drift from disk.
      addToast({
        message: err instanceof Error ? err.message : 'Failed to save delegation graph',
        variant: 'error',
      })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
  })

  // ── Edit handlers (all immutable, validated in the model) ──────────────────
  const handleConnect = (source: string, target: string) =>
    setEdits((prev) => addDelegationEdge(prev, source, target, workerIds))

  const handleDeleteEdge = (source: string, target: string) =>
    setEdits((prev) => removeDelegationEdge(prev, source, target))

  const handleToggleMode = (source: string, mode: DelegationMode) =>
    setEdits((prev) => toggleSourceMode(prev, source, mode))

  const handleSetDepth = (source: string, depth: number | undefined) =>
    setEdits((prev) => setSourceDepth(prev, source, depth))

  const handleReject = (message: string) =>
    addToast({ message, variant: 'warning' })

  const handleReset = () => setEdits(baseline)

  return (
    <div className="absolute inset-0 flex flex-col pb-[env(safe-area-inset-bottom)]">
      <div className="shrink-0 px-4 pt-6">
        <div className="mb-2">
          <Link
            to="/agents"
            className="inline-flex items-center gap-1 text-xs text-[var(--color-muted)] transition-colors hover:text-[var(--color-accent)]"
          >
            <ArrowLeft size={12} weight="bold" /> Agents
          </Link>
        </div>
        <div className="flex flex-wrap items-start gap-2">
          <ShareNetwork
            size={22}
            weight="bold"
            className="mt-0.5 text-[var(--color-accent)]"
          />
          <div className="min-w-0 flex-1">
            <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">
              Delegation Graph
            </h1>
            <p className="mt-0.5 text-sm text-[var(--color-muted)]">
              Drag a node onto another to let it delegate; click an edge to set
              modes (await / background / task) and depth. Workers are leaves —
              they receive work but never delegate onward.
            </p>
          </div>
          {/* Save / reset actions */}
          <div className="flex items-center gap-2">
            {isDirty && (
              <Button
                variant="ghost"
                size="sm"
                onClick={handleReset}
                disabled={saveMutation.isPending}
                data-testid="delegation-reset"
                className="gap-1.5"
              >
                <ArrowCounterClockwise size={14} weight="bold" /> Reset
              </Button>
            )}
            <Button
              variant="default"
              size="sm"
              onClick={() => saveMutation.mutate(dirtyIds)}
              disabled={!isDirty || saveMutation.isPending}
              data-testid="delegation-save"
              className="gap-1.5"
            >
              <FloppyDisk size={14} weight="bold" />
              {saveMutation.isPending
                ? 'Saving…'
                : isDirty
                  ? `Save (${dirtyIds.length})`
                  : 'Saved'}
            </Button>
          </div>
        </div>
      </div>

      {/* Graph canvas (fills remaining height; scrollable internally via pan) */}
      <div className="mt-4 min-h-0 flex-1 px-4 pb-4">
        {isLoading ? (
          <div
            data-testid="delegation-loading"
            className="h-full w-full animate-pulse rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)]"
          />
        ) : isError ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 rounded-xl border border-[var(--color-border)]">
            <p className="text-sm text-[var(--color-muted)]">
              Could not load the delegation graph.
            </p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </div>
        ) : agents.length === 0 ? (
          <div
            data-testid="delegation-empty"
            className="flex h-full flex-col items-center justify-center gap-3 rounded-xl border border-[var(--color-border)] text-center"
          >
            <ShareNetwork
              size={48}
              weight="thin"
              className="text-[var(--color-border)]"
            />
            <p className="text-sm text-[var(--color-muted)]">
              No agents to graph yet.
            </p>
          </div>
        ) : (
          <DelegationGraph
            nodes={nodes}
            edges={edges}
            workerIds={workerIds}
            validateEdits={edits}
            onConnect={handleConnect}
            onToggleMode={handleToggleMode}
            onSetDepth={handleSetDepth}
            onDeleteEdge={handleDeleteEdge}
            onRejectConnection={handleReject}
          />
        )}
      </div>
    </div>
  )
}
