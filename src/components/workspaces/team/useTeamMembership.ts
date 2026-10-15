// useTeamMembership — the Team list's explicit membership writes
// (omnipus-system-agent FR-102/FR-103/FR-104, spec §6.3 rows "Team list" and
// "Add agent menu / picker"; F:D20, F:D21).
//
// Add and Remove each write the workspace's core_team through the one
// revision-guarded workspace update (delegation omitted: the server keeps the
// still-valid edges and applies its new-member seed rule). The outcome is
// truthful: success → toast (an add offers Undo) and every cache that carries
// the workspace is refreshed; a refusal → its message; an UNKNOWN outcome →
// "not confirmed" with Retry. A retry re-reads the workspace first, so it
// writes against the current revision and is idempotent (adding a member that
// already landed changes nothing).
import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api'
import { fetchWorkspace, updateWorkspace, workspacesQueryKeys } from '@/lib/api'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'
import { useUiStore } from '@/store/ui'

export type MembershipAction = 'add' | 'remove' // not-wire-format: UI intent of a Team membership write

export type MembershipStatus = // not-wire-format: in-browser state of the Team list's membership write
  | { kind: 'idle' }
  | { kind: 'saving'; action: MembershipAction; agentId: string }
  | { kind: 'uncertain'; action: MembershipAction; agentId: string; agentName: string }

function nextTeam(current: readonly string[], action: MembershipAction, agentId: string): string[] {
  if (action === 'add') return current.includes(agentId) ? [...current] : [...current, agentId]
  return current.filter((id) => id !== agentId)
}

export function useTeamMembership(workspace: Workspace) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [status, setStatus] = useState<MembershipStatus>({ kind: 'idle' })

  const publish = useCallback(
    (updated: Workspace) => {
      queryClient.setQueryData<Workspace>(workspacesQueryKeys.detail(updated.id), updated)
      for (const listStatus of ['active', 'archived'] as const) {
        queryClient.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: listStatus }), (prev) =>
          prev?.map((w) => (w.id === updated.id ? updated : w)),
        )
      }
      void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.delegation(updated.id) })
    },
    [queryClient],
  )

  const write = useCallback(
    async (action: MembershipAction, agentId: string, agentName: string, base: Workspace): Promise<void> => {
      setStatus({ kind: 'saving', action, agentId })
      try {
        const updated = await updateWorkspace(base.id, {
          revision: base.revision,
          core_team: nextTeam(base.core_team ?? [], action, agentId),
        })
        publish(updated)
        setStatus({ kind: 'idle' })
        if (action === 'add') {
          addToast({
            variant: 'success',
            message: `Added ${agentName} to ${base.name}`,
            action: { label: 'Undo', onClick: () => void write('remove', agentId, agentName, updated) },
          })
        } else {
          addToast({ variant: 'success', message: `Removed ${agentName} from ${base.name}` })
        }
      } catch (error) {
        if (classifyMutationFailure(error) === 'uncertain') {
          setStatus({ kind: 'uncertain', action, agentId, agentName })
          void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.detail(base.id) })
          return
        }
        setStatus({ kind: 'idle' })
        addToast({
          variant: 'error',
          message: `${action === 'add' ? 'Could not add' : 'Could not remove'} ${agentName}: ${mutationFailureMessage(error)}`,
        })
      }
    },
    [addToast, publish, queryClient],
  )

  const add = useCallback(
    (agentId: string, agentName: string) => write('add', agentId, agentName, workspace),
    [write, workspace],
  )
  const remove = useCallback(
    (agentId: string, agentName: string) => write('remove', agentId, agentName, workspace),
    [write, workspace],
  )

  /** Retry an unconfirmed change against a fresh read of the workspace. */
  const retry = useCallback(async () => {
    if (status.kind !== 'uncertain') return
    const { action, agentId, agentName } = status
    setStatus({ kind: 'saving', action, agentId })
    let fresh: Workspace
    try {
      fresh = await fetchWorkspace(workspace.id)
    } catch {
      setStatus({ kind: 'uncertain', action, agentId, agentName })
      return
    }
    await write(action, agentId, agentName, fresh)
  }, [status, workspace.id, write])

  return { status, add, remove, retry }
}
