// ParticipantsPanel — a group chat's members on demand (#1449 US-7, US-8
// AS 8.3; D6, D10, D20; FR-033..FR-037, FR-045, FR-079; ADR-20261028 A10,
// §11 C-I8; wireframe frame 4).
//
// A registered side panel (`participants`), opened only from the group's
// header button. It takes over what the old room header showed, plus:
//   - per member: status (the existing delivery labels, "idle" with no
//     request), the attention halo while it waits on the person outside this
//     group, a link to its own main chat, Ask again on an interrupted request
//     (fills the message box, sends nothing), and the mute/unmute/remove menu;
//   - Add participant (disabled with its explanation in the team room);
//   - Delete group (squads and ad-hoc groups);
//   - "Cost so far", marked incomplete when a model call was unpriced;
//   - "Invitation context": generating, the summary text, or
//     "Invitation context: unavailable" (D20). The text is read only through
//     the group record (getRoom): the panel refetches it when the live status
//     turns ready and after every re-attach while the text may be stale; a
//     failed fetch shows an error with Retry.

import { useEffect, useRef, useState } from 'react'
import type { PanelContentProps } from '@/components/panel-shell/types'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import { AgentMark } from '@/components/agents/AgentMark'
import { AttentionHalo } from '@/components/agents/AttentionHalo'
import { useAttentionMotion } from '@/components/layout/sidebar/attentionCue'
import { AddParticipant } from '@/components/chat/AddParticipant'
import { HistoricalMessageMarkdown } from '@/components/chat/historical-markdown'
import { Button } from '@/components/ui/button'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { useUiStore } from '@/store/ui'
import { ROOM_ASK_AGAIN_EVENT, deliveryLabel, formatAllocatedCost, type RoomAskAgainDetail } from './roomModel'
import { DeleteGroup, MainChatLink, MemberMenu, useMembership } from './roomMembership'
import { needsAttention, useRoomCardRequestIds, useRoomView, type RoomView } from './useRoomView'

type InvitationStatus = 'generating' | 'ready' | 'unavailable' // not-wire-format: mirrors the generated enum for local branching

function MemberRow({ view, agentId, delivery, groupCards, update, roomId }: {
  view: RoomView
  agentId: string
  delivery: RoomDeliveryState | undefined
  groupCards: ReadonlySet<string>
  update: ReturnType<typeof useMembership>['update']
  roomId: string
}) {
  const motion = useAttentionMotion()
  const room = view.room!
  const name = view.agentName(agentId)
  const agent = view.agents?.find((candidate) => candidate.id === agentId)
  const member = room.members.includes(agentId)
  const muted = room.muted.includes(agentId)
  return (
    <li data-testid={`participant-row-${agentId}`} className="flex items-start gap-[var(--space-2)] py-[var(--space-1)]">
      <AttentionHalo halo={needsAttention(delivery, groupCards)} motion={motion}>
        <AgentMark agent={agent} name={name} size={26} />
      </AttentionHalo>
      <div className="flex min-w-0 flex-1 flex-col gap-[var(--space-0-5)]">
        <span className="flex items-baseline gap-[var(--space-1)]">
          <span className="truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{name}</span>
          {muted && <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">muted</span>}
          {!member && <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">left the group</span>}
        </span>
        <span data-testid={`participant-status-${agentId}`} className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {delivery ? deliveryLabel(delivery, name) : 'idle'}
        </span>
        <span className="flex flex-wrap items-center gap-x-[var(--space-2)]">
          <MainChatLink workspace={view.workspace} agentId={agentId} label={`Open ${name}'s main chat`} />
          {delivery?.state === 'interrupted' && (
            <Button
              variant="link"
              size="sm"
              className="h-auto p-0"
              onClick={() => window.dispatchEvent(new CustomEvent<RoomAskAgainDetail>(ROOM_ASK_AGAIN_EVENT, { detail: { roomId, agentId } }))}
            >
              Ask again
            </Button>
          )}
        </span>
      </div>
      {member && <MemberMenu room={room} agentId={agentId} name={name} muted={muted} update={update} />}
    </li>
  )
}

/** S-61, S-122, S-123: the invitation context disclosure. */
function InvitationContext({ view, status }: { view: RoomView; status: InvitationStatus | undefined }) {
  const [open, setOpen] = useState(false)
  const text = view.room?.invitation_context?.text
  const failed = view.roomQuery.isError
  if (!status) return null
  return (
    <section data-testid="invitation-context" data-status={status} className="flex flex-col gap-[var(--space-1)]">
      {status === 'generating' && (
        <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Invitation context: generating…</p>
      )}
      {status === 'unavailable' && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Invitation context: unavailable</p>
      )}
      {status === 'ready' && (
        <>
          <DisclosureRow expanded={open} onExpandedChange={setOpen} expandable>
            <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">Invitation context</span>
          </DisclosureRow>
          {open && (failed && !text ? (
            <ErrorState
              message={`Could not load the invitation context — ${mutationFailureMessage(view.roomQuery.error)}`}
              onRetry={() => void view.roomQuery.refetch()}
              className="items-start py-[var(--space-1)]"
            />
          ) : text ? (
            <div data-testid="invitation-context-text" className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
              <HistoricalMessageMarkdown content={text} />
            </div>
          ) : (
            <Skeleton className="h-4 w-full" />
          ))}
        </>
      )}
    </section>
  )
}

export function ParticipantsPanel({ shellProps }: { shellProps?: PanelContentProps }) {
  const activePanel = useUiStore((state) => state.activePanel)
  const context = shellProps?.context ?? (activePanel?.id === 'participants' ? activePanel.context : undefined)
  const roomId = context?.roomId
  const workspaceId = context?.workspaceId
  useEffect(() => {
    const register = shellProps?.registerExpandContext
    if (!register || !roomId || !workspaceId) return undefined
    register(() => ({ workspaceId, roomId }))
    return () => register(null)
  }, [shellProps?.registerExpandContext, roomId, workspaceId])
  if (!roomId || !workspaceId) {
    return (
      <div role="status" className="flex h-full items-center justify-center p-[var(--space-4)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
        Open this panel from a group chat to see its participants.
      </div>
    )
  }
  return <ParticipantsContent key={roomId} roomId={roomId} workspaceId={workspaceId} />
}

function ParticipantsContent({ roomId, workspaceId }: { roomId: string; workspaceId: string }) {
  const view = useRoomView(roomId, workspaceId)
  const groupCards = useRoomCardRequestIds(roomId)
  const room = view.room
  const membership = useMembership({ id: roomId, workspace_id: workspaceId })

  // FR-045 / FR-079: the live status wins over the record's; the text is
  // refetched through getRoom when the status turns ready, and after every
  // re-attach while it was generating or the text was never loaded.
  const status: InvitationStatus | undefined = view.live?.invitationContextStatus ?? room?.invitation_context_status
  const snapshots = view.live?.snapshotCount ?? 0
  const seen = useRef<{ status?: InvitationStatus; snapshots: number }>({ status, snapshots })
  const refetch = view.roomQuery.refetch
  const hasText = !!room?.invitation_context?.text
  useEffect(() => {
    const previous = seen.current
    seen.current = { status, snapshots }
    const turnedReady = status === 'ready' && previous.status !== 'ready'
    const reattached = snapshots !== previous.snapshots && (status === 'generating' || (status === 'ready' && !hasText))
    if ((turnedReady && !hasText) || reattached) void refetch()
  }, [status, snapshots, hasText, refetch])

  if (!room) {
    if (view.roomQuery.isError) {
      return (
        <ErrorState
          message={`Could not load this group — ${mutationFailureMessage(view.roomQuery.error)}`}
          onRetry={() => void view.roomQuery.refetch()}
        />
      )
    }
    return (
      <div data-testid="participants-loading" className="flex flex-col gap-[var(--space-2)] p-[var(--space-3)]">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-2/3" />
      </div>
    )
  }

  const formerMembers = Object.keys(view.memberStates).filter((agentId) => !room.members.includes(agentId))
  return (
    <div data-testid="participants-panel" className="flex h-full min-h-0 flex-col gap-[var(--space-3)] overflow-y-auto p-[var(--space-3)]">
      <ul aria-label="Members" className="flex flex-col">
        {[...room.members, ...formerMembers].map((agentId) => (
          <MemberRow
            key={agentId}
            view={view}
            agentId={agentId}
            delivery={view.memberStates[agentId]}
            groupCards={groupCards}
            update={membership.update}
            roomId={roomId}
          />
        ))}
      </ul>
      {membership.error && (
        <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{membership.error}</p>
      )}
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <AddParticipant target={{ kind: 'room', room }} />
        <DeleteGroup room={room} />
      </div>
      <div data-testid="participants-cost" className="flex items-baseline justify-between gap-[var(--space-2)]">
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Cost so far</span>
        <span className="flex items-baseline gap-[var(--space-1)]">
          <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{formatAllocatedCost(room.allocated_cost_micro_usd)}</span>
          {room.allocated_cost_unpriced && (
            <span className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">incomplete — includes unpriced model calls</span>
          )}
        </span>
      </div>
      <InvitationContext view={view} status={status} />
    </div>
  )
}
