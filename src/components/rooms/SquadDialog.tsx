// SquadDialog — create a group chat from the web app
// (omnipus-system-agent FR-161, FR-162, FR-175, FR-204; K24; BDD-17.2/20).
//
// Only the user creates group chats, here: a named squad (name up to 80
// characters) or a quick ad-hoc group. Members come from the workspace's
// conversation agents only (never workers, hidden engine agents or Omnipus);
// at least two are required. On success the new group opens.

import { useEffect, useId, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { RoomCreateRequest } from '@/lib/api/generated/openapi-types'
import { fetchAgents, fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
import { createRoom, roomsQueryKeys } from '@/lib/api/rooms'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { workspaceConversationAgents } from './roomModel'

export const SQUAD_NAME_MAX = 80

export interface SquadDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
  kind?: 'squad' | 'adhoc'
  initialAgentIds?: string[]
}

type CreateOutcome = { status: 'idle' | 'creating' } | { status: 'uncertain' } | { status: 'refused'; message: string } // not-wire-format: in-browser create outcome

export function SquadDialog({ open, onOpenChange, workspaceId, kind = 'squad', initialAgentIds = [] }: SquadDialogProps) {
  const navigate = useNavigate()
  const client = useQueryClient()
  const nameId = useId()
  const [name, setName] = useState('')
  const [selected, setSelected] = useState<string[]>(initialAgentIds)
  const [outcome, setOutcome] = useState<CreateOutcome>({ status: 'idle' })
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000, enabled: open })
  const workspaceQuery = useQuery({ queryKey: workspacesQueryKeys.detail(workspaceId), queryFn: () => fetchWorkspace(workspaceId), enabled: open })

  useEffect(() => {
    if (!open) return
    setName('')
    setSelected(initialAgentIds)
    setOutcome({ status: 'idle' })
    // Reset only when the dialog opens; the initial selection is read then.
  }, [open])

  const candidates = workspaceConversationAgents(workspaceQuery.data, agentsQuery.data ?? [])
  const agentIds = candidates.map((agent) => agent.id).filter((id) => selected.includes(id))
  const nameValid = kind === 'adhoc' || (name.trim().length > 0 && name.length <= SQUAD_NAME_MAX)
  const canCreate = agentIds.length >= 2 && nameValid && outcome.status !== 'creating'
  const loadFailed = agentsQuery.isError || workspaceQuery.isError

  async function create() {
    const request: RoomCreateRequest = kind === 'squad'
      ? { kind, name: name.trim(), agent_ids: agentIds }
      : { kind, agent_ids: agentIds }
    setOutcome({ status: 'creating' })
    try {
      const room = await createRoom(workspaceId, request)
      void client.invalidateQueries({ queryKey: roomsQueryKeys.list(workspaceId) })
      onOpenChange(false)
      void navigate({ to: '/workspaces/$workspaceId/rooms/$roomId', params: { workspaceId, roomId: room.id } })
    } catch (error) {
      if (classifyMutationFailure(error) === 'uncertain') {
        // The group may exist: refresh the list so it shows up if it does.
        void client.invalidateQueries({ queryKey: roomsQueryKeys.list(workspaceId) })
        setOutcome({ status: 'uncertain' })
      } else {
        setOutcome({ status: 'refused', message: mutationFailureMessage(error) })
      }
    }
  }

  function toggle(agentId: string, checked: boolean) {
    setSelected((current) => checked ? [...current, agentId] : current.filter((id) => id !== agentId))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{kind === 'squad' ? 'New squad' : 'New group chat'}</DialogTitle>
          <DialogDescription>
            {kind === 'squad'
              ? 'A named group chat with two or more agents of this workspace.'
              : 'A quick group chat with two or more agents of this workspace.'}
          </DialogDescription>
        </DialogHeader>
        {kind === 'squad' && (
          <Field label="Squad name">
            <Input id={nameId} value={name} maxLength={SQUAD_NAME_MAX} onChange={(event) => setName(event.target.value)} />
          </Field>
        )}
        {loadFailed ? (
          <QueryErrorState
            layout="fill"
            message="Could not load this workspace's agents."
            onRetry={() => { void agentsQuery.refetch(); void workspaceQuery.refetch() }}
          />
        ) : agentsQuery.isPending || workspaceQuery.isPending ? (
          <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loading agents…</p>
        ) : candidates.length === 0 ? (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No agents in this workspace</p>
        ) : (
          <fieldset className="flex flex-col gap-[var(--space-2)]">
            <legend className="mb-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Members (at least two)</legend>
            {candidates.map((agent) => (
              <div key={agent.id} className="flex items-center gap-[var(--space-2)]">
                <Checkbox
                  id={`${nameId}-${agent.id}`}
                  checked={selected.includes(agent.id)}
                  onCheckedChange={(checked) => toggle(agent.id, checked === true)}
                />
                <Label htmlFor={`${nameId}-${agent.id}`}>{agent.name}</Label>
              </div>
            ))}
          </fieldset>
        )}
        {outcome.status === 'uncertain' && (
          <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            Not confirmed — the group may have been created. Check the sidebar before creating it again.
          </p>
        )}
        {outcome.status === 'refused' && (
          <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{outcome.message}</p>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={() => void create()} disabled={!canCreate}>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
