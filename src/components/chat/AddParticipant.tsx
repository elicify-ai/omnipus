// AddParticipant — "Add participant" for a chat
// (omnipus-system-agent FR-207, FR-208; K24; BDD-17.24/25; test row 138).
//
// Offers agents only: the conversation agents of the chat's workspace, never
// channel people, workers, hidden engine agents or Omnipus.
//   - In an agent's main chat, choosing an agent creates a NEW ad-hoc group
//     with both agents and opens it empty, with a link back. The main chat
//     itself stays unchanged and one-to-one (FR-207).
//   - In a squad or ad-hoc group, it adds the chosen agent (FR-208).
//   - In the team room it is disabled with the explanation (FR-208).
//   - It is absent in Omnipus's chat (MainChatAddParticipant below).
//
// #1449 D13 (founder 2026-10-28): in the chat header line the main-chat
// control is an icon-only ghost button with a tooltip (the 'icon'
// presentation); the room header keeps the labelled button until the group
// chat lands. The picker behind both is the same.

import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { UserPlus } from '@phosphor-icons/react'
import type { Room, RoomCreateRequest } from '@/lib/api/generated/openapi-types'
import type { Agent } from '@/lib/api'
import { fetchAgents, fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
import { createRoom, fetchWorkspaceRooms, roomsQueryKeys, updateRoomMembers } from '@/lib/api/rooms'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'
import { isOmnipusAgentId } from '@/lib/omnipus'
import { ROOMS_ENABLED } from '@/lib/featureSwitches'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip } from '@/components/ui/tooltip'
import { Command, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { workspaceConversationAgents } from '@/components/rooms/roomModel'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

export type AddParticipantTarget = // not-wire-format: React prop union over a main chat or a generated Room
  | { kind: 'main'; workspaceId: string; agentId: string; sessionId: string }
  | { kind: 'room'; room: Room }

export const TEAM_ROOM_ADD_EXPLANATION = "Team room members are the workspace's agents — add the agent to the workspace"

type AddOutcome = { status: 'idle' | 'working' } | { status: 'uncertain'; agentId: string } | { status: 'refused'; message: string } // not-wire-format: in-browser write outcome

function targetWorkspaceId(target: AddParticipantTarget): string {
  return target.kind === 'main' ? target.workspaceId : target.room.workspace_id
}

function excludedIds(target: AddParticipantTarget): Set<string> {
  return new Set(target.kind === 'main' ? [target.agentId] : target.room.members)
}

/** An ad-hoc group with exactly these members started from this main (uncertain-create recovery). */
function sameAdhoc(room: Room, request: RoomCreateRequest): boolean {
  return room.kind === 'adhoc'
    && room.origin_session_id === request.origin_session_id
    && room.members.length === request.agent_ids.length
    && request.agent_ids.every((id) => room.members.includes(id))
}

export function AddParticipant({ target, presentation = 'label' }: { target: AddParticipantTarget; presentation?: 'label' | 'icon' }) {
  const [open, setOpen] = useState(false)
  if (target.kind === 'room' && target.room.kind === 'team') {
    return (
      <Button
        variant="ghost"
        size="sm"
        data-testid="add-participant"
        disabled
        aria-description={TEAM_ROOM_ADD_EXPLANATION}
        title={TEAM_ROOM_ADD_EXPLANATION}
      >
        <UserPlus aria-hidden="true" size={14} />
        Add participant
      </Button>
    )
  }
  if (presentation === 'icon') {
    // #1449 D13 (founder 2026-10-28): in the chat header line the control is
    // an icon-only ghost button with a tooltip — no label, no border, no
    // background of its own. The picker behind it is unchanged.
    return (
      <>
        <Tooltip content="Add participant" side="bottom" className="w-max" interactive>
          <IconButton
            aria-label="Add participant"
            data-testid="add-participant"
            size="sm"
            variant="ghost"
            onClick={() => setOpen(true)}
            className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)] active:text-[var(--color-accent)]"
          >
            <UserPlus aria-hidden="true" size={16} />
          </IconButton>
        </Tooltip>
        {open && <AddParticipantDialog target={target} onClose={() => setOpen(false)} />}
      </>
    )
  }
  return (
    <>
      <Button variant="ghost" size="sm" data-testid="add-participant" onClick={() => setOpen(true)}>
        <UserPlus aria-hidden="true" size={14} />
        Add participant
      </Button>
      {open && <AddParticipantDialog target={target} onClose={() => setOpen(false)} />}
    </>
  )
}

function AddParticipantDialog({ target, onClose }: { target: AddParticipantTarget; onClose: () => void }) {
  const navigate = useNavigate()
  const client = useQueryClient()
  const workspaceId = targetWorkspaceId(target)
  const [outcome, setOutcome] = useState<AddOutcome>({ status: 'idle' })
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const workspaceQuery = useQuery({ queryKey: workspacesQueryKeys.detail(workspaceId), queryFn: () => fetchWorkspace(workspaceId) })
  const excluded = excludedIds(target)
  const candidates = workspaceConversationAgents(workspaceQuery.data, agentsQuery.data ?? [])
    .filter((agent) => !excluded.has(agent.id))

  async function createAdhoc(agentId: string, recovering: boolean) {
    if (target.kind !== 'main') return
    const request: RoomCreateRequest = { kind: 'adhoc', agent_ids: [target.agentId, agentId], origin_session_id: target.sessionId }
    if (recovering) {
      // A create whose outcome is unknown may already exist: open it instead of making a second one.
      const existing = (await fetchWorkspaceRooms(workspaceId)).find((room) => sameAdhoc(room, request))
      if (existing) return openRoom(existing.id)
    }
    openRoom((await createRoom(workspaceId, request)).id)
  }

  function openRoom(roomId: string) {
    void client.invalidateQueries({ queryKey: roomsQueryKeys.list(workspaceId) })
    onClose()
    void navigate({ to: '/workspaces/$workspaceId/rooms/$roomId', params: { workspaceId, roomId } })
  }

  async function addToRoom(agentId: string) {
    if (target.kind !== 'room') return
    await updateRoomMembers(target.room.id, { action: 'add', agent_ids: [agentId] })
    void client.invalidateQueries({ queryKey: roomsQueryKeys.detail(target.room.id) })
    void client.invalidateQueries({ queryKey: roomsQueryKeys.list(workspaceId) })
    onClose()
  }

  async function choose(agent: Agent, recovering = false) {
    setOutcome({ status: 'working' })
    try {
      if (target.kind === 'main') await createAdhoc(agent.id, recovering)
      else await addToRoom(agent.id)
    } catch (error) {
      setOutcome(classifyMutationFailure(error) === 'uncertain'
        ? { status: 'uncertain', agentId: agent.id }
        : { status: 'refused', message: mutationFailureMessage(error) })
    }
  }

  const retryAgent = outcome.status === 'uncertain' ? candidates.find((agent) => agent.id === outcome.agentId) : undefined

  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent data-testid="add-participant-dialog">
        <DialogHeader>
          <DialogTitle>Add participant</DialogTitle>
          <DialogDescription>
            {target.kind === 'main'
              ? 'Start a group chat with this agent and another agent of the workspace. This chat stays one-to-one.'
              : 'Add an agent of this workspace to the group.'}
          </DialogDescription>
        </DialogHeader>
        {agentsQuery.isError || workspaceQuery.isError ? (
          <QueryErrorState
            layout="fill"
            message="Could not load this workspace's agents."
            onRetry={() => { void agentsQuery.refetch(); void workspaceQuery.refetch() }}
          />
        ) : agentsQuery.isPending || workspaceQuery.isPending ? (
          <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loading agents…</p>
        ) : candidates.length === 0 ? (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No other agents in this workspace</p>
        ) : (
          <Command label="Agents" className="rounded-lg">
            <CommandInput autoFocus placeholder="Search agents" />
            <CommandList>
              {candidates.map((agent) => (
                <CommandItem key={agent.id} value={`${agent.name} ${agent.id}`} disabled={outcome.status === 'working'} onSelect={() => void choose(agent)}>
                  {agent.name}
                </CommandItem>
              ))}
            </CommandList>
          </Command>
        )}
        {outcome.status === 'uncertain' && (
          <div role="alert" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            <span className="flex-1">Not confirmed — the change may not have been saved.</span>
            {retryAgent && <Button variant="outline" size="sm" onClick={() => void choose(retryAgent, true)}>Retry</Button>}
          </div>
        )}
        {outcome.status === 'refused' && (
          <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{outcome.message}</p>
        )}
      </DialogContent>
    </Dialog>
  )
}

/**
 * The main-chat placement (FR-207): the open chat must be a member's main
 * chat in a workspace. Absent in Omnipus's chat (FR-208) and in any chat that
 * is not a main.
 *
 * #1449 D13 (founder 2026-10-28): it renders in the workspace header line's
 * right-end slot (ChatHeaderSlot) as an icon-only ghost button — its own row
 * above the chat is gone. The visibility rules are unchanged.
 */
export function MainChatAddParticipant() {
  const sessionId = useSessionStore((state) => state.activeSessionId)
  const agentId = useSessionStore((state) => state.activeAgentId)
  const isMain = useSessionStore((state) => state.attachedSessionType === 'main')
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  if (!ROOMS_ENABLED) return null
  if (!sessionId || sessionId === '__pending' || !agentId || !workspaceId || !isMain || isOmnipusAgentId(agentId)) return null
  return (
    <AddParticipant target={{ kind: 'main', workspaceId, agentId, sessionId }} presentation="icon" />
  )
}
