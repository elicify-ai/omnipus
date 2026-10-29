// RoomMessageBox — the group chat's message box: the normal chat's shared
// ComposerBody with the group profile and a room-scoped conversation
// (#1449 US-2, US-13; ADR-20261028 A2.2, §11 C-I4).
//
// The conversation is built from the room alone. It never reads, sends to,
// stops or is locked by a chat session (FR-015): no replay state, no pending
// question of another chat, no workspace-entry gate, no cancelStream and no
// document-level Escape listener (the stop machine is inert — there is no
// Stop in the group, D21). Sending is blocked, with the reason shown in the
// box's notice area, while the group is reconnecting, read-only or has not
// received its first live view (FR-013), and while an attachment is still
// uploading or failed to upload (the text is kept).

import { useCallback, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useComposerRuntime } from '@assistant-ui/react'
import type { Agent, Workspace } from '@/lib/api'
import type { Room } from '@/lib/api/generated/openapi-types'
import { Button } from '@/components/ui/button'
import { useConnectionStore } from '@/store/connection'
import { ComposerBody, INERT_CANCEL_STATE } from '@/components/chat/composer/OmnipusComposer'
import type { ComposerConversation, ComposerProfile } from '@/components/chat/composer/OmnipusComposer'
import { isRefusedRoomCommand, memberMainSessionId, refusalLinkMembers } from './roomModel'
import { useRoomComposerMenu } from './useRoomComposerMenu'
import type { RoomSend } from './useRoomSend'
import type { RoomSendBridge } from './useRoomRuntime'

/** S-19 / FR-012: the group box's placeholder (spec wording). */
export const GROUP_PLACEHOLDER = 'Message the group - type @ to address someone'

/** D4 as amended by D21: card, attach and send; nothing session-only. */
export const ROOM_COMPOSER_PROFILE: ComposerProfile = {
  contextRow: false,
  unavailableNotice: false,
  stop: false,
  activityBar: false,
  mediaLibrary: false,
}

/** FR-013: why sending is not possible right now; undefined when it is. */
export function roomBlockedReason(room: Room, connected: boolean, liveViewReceived: boolean): string | undefined {
  if (!connected) return 'Reconnecting… messages can be sent again once the connection is back.'
  if (room.read_only_reason) return room.read_only_reason
  if (!liveViewReceived) return 'Waiting for the group…'
  return undefined
}

function SendNotice({ send, members, workspace }: { send: RoomSend; members: Agent[]; workspace: Workspace | undefined }) {
  const { state } = send
  if (state.status === 'uncertain') {
    return (
      <div role="alert" className="flex items-center gap-[var(--space-2)] px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <span className="flex-1">Not confirmed — your message may not have been recorded.</span>
        <Button variant="outline" size="sm" onClick={send.retry}>Retry</Button>
      </div>
    )
  }
  if (state.status !== 'refused') return null
  // FR-205: a refused command links to each member's main chat (or the @-named one's).
  const links = isRefusedRoomCommand(state.request.text) ? refusalLinkMembers(state.request.text, members) : []
  return (
    <div role="alert" className="flex flex-col gap-[var(--space-1)] px-[var(--space-1)] text-[length:var(--type-caption-size)]">
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

export function RoomMessageBox({
  room,
  members,
  workspace,
  send,
  blockedReason,
  prefill,
  bridge,
}: {
  room: Room
  members: Agent[]
  workspace: Workspace | undefined
  send: RoomSend
  blockedReason: string | undefined
  prefill?: { agentId: string; nonce: number }
  bridge: { current: RoomSendBridge | null }
}) {
  const composerRuntime = useComposerRuntime()
  const isConnected = useConnectionStore((state) => state.isConnected)
  const menu = useRoomComposerMenu({ composerRuntime, members, muted: room.muted, prefill })
  // The runtime's send reads the explicit picks at send time.
  bridge.current = { mentionsFor: menu.mentionsFor, onAccepted: menu.clearPicks }
  const [uploadNotice, setUploadNotice] = useState<string | null>(null)

  const sendBlocked = useCallback(() => {
    if (blockedReason) return true
    const attachments = composerRuntime.getState().attachments
    if (attachments.some((attachment) => attachment.status.type === 'running')) {
      setUploadNotice('Wait until the files have uploaded, then send.')
      return true
    }
    if (attachments.some((attachment) => attachment.status.type === 'incomplete')) {
      setUploadNotice('A file could not be uploaded. Remove it, then send.')
      return true
    }
    setUploadNotice(null)
    return false
  }, [blockedReason, composerRuntime])

  const inputEnabled = blockedReason === undefined
  const conversation: ComposerConversation = {
    profile: ROOM_COMPOSER_PROFILE,
    isStreaming: false,
    isReplaying: false,
    isConnected,
    // The group never queues while disconnected: sending is blocked instead.
    reconnectPhase: null,
    inputEnabled,
    attachDisabled: !inputEnabled,
    agentRemoved: false,
    placeholder: GROUP_PLACEHOLDER,
    inputLabel: 'Message the group',
    attachLabel: 'Attach files',
    cancelState: INERT_CANCEL_STATE,
    goalRunning: false,
    slashMenu: menu,
    sendBlocked,
    notices: (
      <div data-testid="room-composer-notices" className="flex flex-col gap-[var(--space-1)] empty:hidden pb-[var(--space-1)]">
        {room.paused && (
          <p className="px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            Group paused — new messages are kept here and not sent to members until /resume.
          </p>
        )}
        {blockedReason && (
          <p role="status" className="px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{blockedReason}</p>
        )}
        {uploadNotice && (
          <p role="alert" className="px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">{uploadNotice}</p>
        )}
        <SendNotice send={send} members={members} workspace={workspace} />
      </div>
    ),
  }
  return <ComposerBody conversation={conversation} composerRuntime={composerRuntime} />
}
