// RoomScreen — one group chat (omnipus-system-agent US-17; FR-164, FR-175,
// FR-199, FR-201, FR-204, FR-205, FR-207; §6.3, §6.11; K23, K26, K45).
//
// #1449 (ADR-20261028, decision D1): the group is the normal chat with more
// than one agent in it. The page renders the SHARED chat stack — the message
// list with its rows, history boundary, Return to latest and opening cover
// (ChatMessageList), and the message box (ComposerBody, group profile) — in
// the normal chat's centred columns (D7). It reads the group through a room
// ConversationSource; the room keeps its own store (FR-199). The group's
// chrome is the Participants button in the workspace header line
// (GroupHeaderControls) and the Participants side panel; there is no bar,
// member list or Stop on this page (D13, D6, D21).
//
// A room is a conversation owned by the user, not an agent session (FR-199):
// this screen attaches the ROOM on the existing hub connection (again after
// every reconnect, resuming from the room's own cursor) and never creates,
// attaches or switches an agent session. Sending is blocked — never silently
// dropped — while the room cannot be read, the first snapshot after
// (re)attach has not arrived, the connection is down, or the server marked
// the room read-only (§6.3, FR-204; ruling Q2).

import { useEffect, useMemo, useRef, useState } from 'react'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { useConnectionStore } from '@/store/connection'
import { useRoomStore } from '@/store/rooms'
import { useUiStore } from '@/store/ui'
import { VirtualizedMessageList } from '@/components/chat/ChatMessageList'
import { ConversationKindContext, ConversationSourceContext } from '@/components/chat/conversationSource'
import { ToolApprovalThreadTail } from '@/components/agents/ToolApprovalPanel'
import { AskUserQuestionCard } from '@/components/chat/AskUserQuestionCard'
import { useRoomConversationSource } from './roomConversationSource'
import { ROOM_ASK_AGAIN_EVENT, type RoomAskAgainDetail } from './roomModel'
import { RoomMessageBox, roomBlockedReason } from './RoomMessageBox'
import { RoomWorkingLines } from './RoomWorkingLines'
import { useRoomRuntime, type RoomSendBridge } from './useRoomRuntime'
import { useRoomSend } from './useRoomSend'
import { useRoomCardRequestIds, useRoomQuestionCards, useRoomView } from './useRoomView'

export interface RoomScreenProps {
  workspaceId: string
  roomId: string
}

/** FR-048: this group's question cards, one at a time with "+N more" (as the normal chat). */
function RoomQuestionTail({ roomId }: { roomId: string }) {
  const cards = useRoomQuestionCards(roomId)
  const first = cards[0]
  if (!first) return null
  return (
    <div className="w-full max-w-3xl mx-auto px-[var(--space-3)]" data-testid="room-question-tail">
      <AskUserQuestionCard card={first} />
      {cards.length > 1 && (
        <p className="text-center text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-1)]">+{cards.length - 1} more</p>
      )}
    </div>
  )
}

export function RoomScreen({ workspaceId, roomId }: RoomScreenProps) {
  const view = useRoomView(roomId, workspaceId)
  const connected = useConnectionStore((state) => state.isConnected)
  const connection = useConnectionStore((state) => state.connection)
  const liteMode = useConnectionStore((state) => state.liteMode)
  const send = useRoomSend(roomId)
  const [askAgain, setAskAgain] = useState<{ agentId: string; nonce: number }>()
  const [attachedRoomId, setAttachedRoomId] = useState<string>()
  const groupCards = useRoomCardRequestIds(roomId)
  // ADR §11 C-I4: the box is locked only by a pending question of THIS
  // group's work (as the normal chat locks for its own question), never by
  // another chat's question (US-13 AS 13.4).
  const questionPending = useRoomQuestionCards(roomId).length > 0

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
  // FR-034 / S-58: the Participants panel belongs to this group; leaving it closes the panel.
  useEffect(() => () => {
    const panel = useUiStore.getState().activePanel
    if (panel?.id === 'participants' && panel.context.roomId === roomId) useUiStore.getState().closePanel()
  }, [roomId])
  // FR-036: Ask again from the Participants panel fills this box.
  useEffect(() => {
    const onAskAgain = (event: Event) => {
      const detail = (event as CustomEvent<RoomAskAgainDetail>).detail
      if (detail?.roomId === roomId) setAskAgain({ agentId: detail.agentId, nonce: Date.now() })
    }
    window.addEventListener(ROOM_ASK_AGAIN_EVENT, onAskAgain)
    return () => window.removeEventListener(ROOM_ASK_AGAIN_EVENT, onAskAgain)
  }, [roomId])

  const attachedHere = connected && attachedRoomId === roomId
  const conversation = useRoomConversationSource(roomId, attachedHere, view.roomQuery.isError)
  const workspaceRef = useRef<string | undefined>(undefined)
  workspaceRef.current = view.room?.workspace_id
  const bridge = useRef<RoomSendBridge | null>(null)
  const runtime = useRoomRuntime(send, () => workspaceRef.current, bridge)
  const opening = useMemo(() => ({
    ...conversation.opening,
    tail: (
      <RoomWorkingLines
        memberStates={view.memberStates}
        agents={view.agents}
        agentName={view.agentName}
        workspace={view.workspace}
        groupCards={groupCards}
      />
    ),
  }), [conversation.opening, view.memberStates, view.agents, view.agentName, view.workspace, groupCards])

  if (view.roomQuery.isError) {
    return (
      <QueryErrorState
        layout="fill"
        message={`Could not load this group chat — ${mutationFailureMessage(view.roomQuery.error)}`}
        onRetry={() => void view.roomQuery.refetch()}
      />
    )
  }
  if (!view.room) {
    return <p role="status" className="m-auto text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading group chat…</p>
  }

  const room = view.room
  const liveViewReceived = attachedHere && view.live?.snapshotReceived === true
  return (
    <ConversationSourceContext.Provider value={conversation.source}>
      <ConversationKindContext.Provider value="room">
        <AssistantRuntimeProvider runtime={runtime}>
          <div className="flex flex-col absolute inset-0 overflow-hidden" data-room-id={roomId}>
            {/* US-11 AS 11.5 / S-102: the group's name is the chat's name for
                assistive technology (no visible title bar, D13). */}
            <span role="status" className="sr-only" data-testid="room-header">{room.name}</span>
            <ThreadPrimitive.Root className="flex flex-col flex-1 min-h-0">
              <VirtualizedMessageList
                key={`room:${roomId}`}
                messages={conversation.messages}
                liteMode={liteMode}
                agentName={room.name}
                openFailed={view.roomQuery.isError}
                room={opening}
              />
              {/* FR-048, FR-051: only the cards of THIS group's work; secret
                  and helper cards stay in the agent's own chat. */}
              <RoomQuestionTail roomId={roomId} />
              <ToolApprovalThreadTail roomId={roomId} />
              <div className="relative w-full">
                <div className="w-full max-w-3xl mx-auto px-[var(--space-3)] pt-[var(--space-2)] pb-[var(--space-2)]">
                  <RoomMessageBox
                    room={room}
                    members={view.members}
                    workspace={view.workspace}
                    send={send}
                    blockedReason={roomBlockedReason(room, connected, liveViewReceived)
                      ?? (questionPending ? 'Answer the question card above, or cancel it, to write again.' : undefined)}
                    prefill={askAgain}
                    bridge={bridge}
                  />
                </div>
              </div>
            </ThreadPrimitive.Root>
          </div>
        </AssistantRuntimeProvider>
      </ConversationKindContext.Provider>
    </ConversationSourceContext.Provider>
  )
}
