// useRoomView — one group chat as every group surface sees it (#1449): the
// REST record with the live room frames applied over it, the member roster,
// each member's most recent request state and the workspace record. Shared by
// the group page (RoomScreen), the header's Participants button and the
// Participants panel, so the three never disagree about who is in the group.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { Room } from '@/lib/api/generated/openapi-types'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import type { Agent, Workspace } from '@/lib/api'
import { fetchAgents, fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
import { fetchRoom, roomsQueryKeys } from '@/lib/api/rooms'
import { latestDeliveryByMember, useRoomStore, type RoomLiveState } from '@/store/rooms'
import { useChatStore } from '@/store/chat'
import { useToolApprovalStore } from '@/store/toolApproval'

/** The REST record with the live room frames applied over it. */
export function currentRoom(room: Room, live: RoomLiveState | undefined): Room {
  if (!live || live.seq === 0) return room
  return {
    ...room,
    members: live.members, muted: live.muted, paused: live.paused, generation: live.generation,
    read_only_reason: live.readOnlyReason,
    allocated_cost_micro_usd: live.allocatedCostMicroUsd,
    allocated_cost_unpriced: live.allocatedCostUnpriced,
  }
}

export interface RoomView { // not-wire-format: React view model over the generated room types
  roomQuery: ReturnType<typeof useRoomQuery>
  /** The live room record; undefined until the REST read answered. */
  room: Room | undefined
  live: RoomLiveState | undefined
  agents: Agent[] | undefined
  /** Current members, in room order, that the loaded roster knows. */
  members: Agent[]
  memberStates: Record<string, RoomDeliveryState>
  workspace: Workspace | undefined
  agentName: (agentId: string) => string
}

function useRoomQuery(roomId: string) {
  return useQuery({ queryKey: roomsQueryKeys.detail(roomId), queryFn: () => fetchRoom(roomId) })
}

export function useRoomView(roomId: string, workspaceId: string): RoomView {
  const roomQuery = useRoomQuery(roomId)
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const workspaceQuery = useQuery({ queryKey: workspacesQueryKeys.detail(workspaceId), queryFn: () => fetchWorkspace(workspaceId) })
  const live = useRoomStore((state) => state.rooms[roomId])
  const agents = agentsQuery.data
  const agentName = useMemo(() => {
    const byId = new Map((agents ?? []).map((agent) => [agent.id, agent.name]))
    return (agentId: string) => byId.get(agentId) ?? agentId
  }, [agents])
  const room = roomQuery.data ? currentRoom(roomQuery.data, live) : undefined
  const members = room ? room.members.flatMap((id) => agents?.find((agent) => agent.id === id) ?? []) : []
  const memberStates = latestDeliveryByMember(live)
  return { roomQuery, room, live, agents, members, memberStates, workspace: workspaceQuery.data, agentName }
}

/**
 * The question cards that belong to THIS group's work (#1449 FR-048, D23):
 * a pending card whose immutable group reference names this room. Cards
 * live in their own agent chat's bucket; the group only shows them.
 */
export function useRoomQuestionCards(roomId: string) {
  const sessionsById = useChatStore((state) => state.sessionsById)
  return useMemo(() => Object.values(sessionsById)
    .flatMap((bucket) => (bucket.pendingAsk ? [bucket.pendingAsk] : []))
    .filter((card) => card.status === 'pending' && card.room_ref?.room_id === roomId), [sessionsById, roomId])
}

/**
 * The request ids of this group whose card is shown IN the group: a pending
 * tool approval or question carrying this room's reference. A member waiting
 * on the person for any other reason (a helper's card, unrelated work, a
 * step that also answers other input or another group — D15/D23/D28) has
 * its card in its own chat only, and the group shows the attention cue.
 */
export function useRoomCardRequestIds(roomId: string): ReadonlySet<string> {
  const queue = useToolApprovalStore((state) => state.queue)
  const questions = useRoomQuestionCards(roomId)
  return useMemo(() => new Set([
    ...queue.filter((approval) => approval.roomRef?.room_id === roomId).map((approval) => approval.roomRef!.request_id),
    ...questions.flatMap((card) => (card.room_ref ? [card.room_ref.request_id] : [])),
  ]), [queue, questions, roomId])
}

/** FR-028: the member waits on the person and the card is NOT in this group. */
export function needsAttention(delivery: RoomDeliveryState | undefined, groupCards: ReadonlySet<string>): boolean {
  return delivery?.state === 'waiting_on_user' && !groupCards.has(delivery.request_id)
}
