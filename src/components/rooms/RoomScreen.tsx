// RoomScreen — one group chat (omnipus-system-agent US-17; FR-164, FR-171,
// FR-175, FR-199, FR-201, FR-204, FR-205, FR-207; §6.3, §6.11; K23, K26, K45).
//
// A room is a conversation owned by the user, not an agent session (FR-199):
// this screen attaches the ROOM on the existing hub connection (again after
// every reconnect, resuming from the room's own cursor) and never creates,
// attaches or switches an agent session. The room record (REST) gives the
// header's starting point; live frames (src/store/rooms.ts) keep members,
// mutes, pause, cost, request states and the transcript current.
// Sending is blocked — never silently dropped — while the room cannot be
// read, the first snapshot after (re)attach has not arrived, the connection
// is down, or the server marked the room read-only (§6.3, FR-204; ruling Q2).
// The reserved complete flag and omitted older history never block sending.

import { useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { Room } from '@/lib/api/generated/openapi-types'
import type { Agent, Workspace } from '@/lib/api'
import { fetchAgents, fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
import { fetchRoom, roomsQueryKeys } from '@/lib/api/rooms'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { Button } from '@/components/ui/button'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { useConnectionStore } from '@/store/connection'
import { latestDeliveryByMember, useRoomStore, type RoomLiveState } from '@/store/rooms'
import { RoomComposer } from './RoomComposer'
import { RoomHeader } from './RoomHeader'
import { RoomTranscript } from './RoomTranscript'
import { isRefusedRoomCommand, memberMainSessionId, refusalLinkMembers } from './roomModel'
import { useRoomSend, type RoomSend } from './useRoomSend'

export interface RoomScreenProps {
  workspaceId: string
  roomId: string
}

/** The REST record with the live room frames applied over it. */
function currentRoom(room: Room, live: RoomLiveState | undefined): Room {
  if (!live || live.seq === 0) return room
  return {
    ...room,
    members: live.members, muted: live.muted, paused: live.paused, generation: live.generation,
    read_only_reason: live.readOnlyReason,
    allocated_cost_micro_usd: live.allocatedCostMicroUsd,
    allocated_cost_unpriced: live.allocatedCostUnpriced,
  }
}

function blockedReasonOf(room: Room, live: RoomLiveState | undefined, connected: boolean): string | undefined {
  if (!connected) return 'Reconnecting… messages can be sent again once the connection is back.'
  if (room.read_only_reason) return room.read_only_reason
  if (!live?.snapshotReceived) return 'Loading the conversation…'
  return undefined
}

function SendNotice({ send, members, workspace }: { send: RoomSend; members: Agent[]; workspace: Workspace | undefined }) {
  const { state } = send
  if (state.status === 'uncertain') {
    return (
      <div role="alert" className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <span className="flex-1">Not confirmed — your message may not have been recorded.</span>
        <Button variant="outline" size="sm" onClick={send.retry}>Retry</Button>
      </div>
    )
  }
  if (state.status !== 'refused') return null
  // FR-205: a refused command links to each member's main chat (or the @-named one's).
  const links = isRefusedRoomCommand(state.request.text) ? refusalLinkMembers(state.request.text, members) : []
  return (
    <div role="alert" className="flex flex-col gap-[var(--space-1)] px-[var(--space-3)] text-[length:var(--type-caption-size)]">
      <p className="text-[var(--color-error)]">{state.message}</p>
      {links.length > 0 && (
        <div className="flex flex-wrap gap-x-[var(--space-3)]">
          {links.map((agent) => {
            const sessionId = memberMainSessionId(workspace, agent.id)
            return sessionId ? (
              <Link key={agent.id} tabIndex={0} to="/sessions/$sessionId" params={{ sessionId }} className="text-[var(--color-accent)] underline-offset-2 hover:underline">
                Open {agent.name}&apos;s main chat
              </Link>
            ) : null
          })}
        </div>
      )}
    </div>
  )
}

export function RoomScreen({ workspaceId, roomId }: RoomScreenProps) {
  const roomQuery = useQuery({ queryKey: roomsQueryKeys.detail(roomId), queryFn: () => fetchRoom(roomId) })
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const workspaceQuery = useQuery({ queryKey: workspacesQueryKeys.detail(workspaceId), queryFn: () => fetchWorkspace(workspaceId) })
  const live = useRoomStore((state) => state.rooms[roomId])
  const connected = useConnectionStore((state) => state.isConnected)
  const connection = useConnectionStore((state) => state.connection)
  const send = useRoomSend(roomId)
  const [askAgain, setAskAgain] = useState<{ agentId: string; nonce: number }>()
  const [attachedRoomId, setAttachedRoomId] = useState<string>()

  // K45: attach on mount and after every reconnect (the store resumes after
  // the room's own cursor). Never an agent-session attach.
  useEffect(() => {
    if (connected && connection) {
      useRoomStore.getState().attachRoom(roomId)
      // Cached rows may still carry the previous opening's ready flag on
      // the first render. Only this opening's attach can make them ready.
      setAttachedRoomId(roomId)
    } else {
      // Offline navigation cannot reuse an attachment from an earlier visit.
      setAttachedRoomId(undefined)
    }
  }, [roomId, connected, connection])
  // #1332 D6: leaving the room invalidates its in-flight older-page request.
  useEffect(() => () => useRoomStore.getState().abandonOlder(roomId), [roomId])

  const agents = agentsQuery.data
  const agentName = useMemo(() => {
    const byId = new Map((agents ?? []).map((agent) => [agent.id, agent.name]))
    return (agentId: string) => byId.get(agentId) ?? agentId
  }, [agents])

  if (roomQuery.isError) {
    return (
      <QueryErrorState
        layout="fill"
        message={`Could not load this group chat — ${mutationFailureMessage(roomQuery.error)}`}
        onRetry={() => void roomQuery.refetch()}
      />
    )
  }
  if (!roomQuery.data) {
    return <p role="status" className="m-auto text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading group chat…</p>
  }

  const room = currentRoom(roomQuery.data, live)
  const members = room.members.flatMap((id) => agents?.find((agent) => agent.id === id) ?? [])
  return (
    <div className="flex h-full min-h-0 flex-col">
      <RoomHeader
        room={room}
        workspace={workspaceQuery.data}
        agentName={agentName}
        memberStates={latestDeliveryByMember(live)}
        onAskAgain={(agentId) => setAskAgain({ agentId, nonce: Date.now() })}
      />
      <RoomTranscript
        roomId={roomId}
        snapshotReceived={connected && attachedRoomId === roomId && live?.snapshotReceived === true}
        entries={live?.entries ?? []}
        agentName={agentName}
        hasOlder={live?.hasOlder}
        history={{
          status: live?.historyStatus ?? 'idle',
          failed: live?.historyFailed ?? false,
          loadOlder: () => void useRoomStore.getState().loadOlder(roomId),
        }}
      />
      <SendNotice send={send} members={members} workspace={workspaceQuery.data} />
      <RoomComposer
        members={members}
        blockedReason={blockedReasonOf(room, live, connected)}
        onSend={send.send}
        prefill={askAgain}
      />
    </div>
  )
}
