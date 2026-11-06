// GroupHeaderControls — a group chat's chrome in the workspace header line's
// right-end slot (#1449 D6/D13, ADR-20261028 A9.2; wireframe frame 3/4).
//
// One ghost Button: up to three member avatars, "+N" for the rest, and the
// member count — the Participants button. No UsersThree icon (it would read
// as the Team toggle), no bar, no border, no surface fill. It toggles the
// registered `participants` side panel; `aria-pressed` reports the panel.
// The group's name is announced as the chat's name by the group page
// (RoomScreen: a visually hidden status node, the main chat title pattern, D13).
// While the group record is unknown the button is not shown.

import { useUiStore } from '@/store/ui'
import { AgentMark } from '@/components/agents/AgentMark'
import { AttentionHalo } from '@/components/agents/AttentionHalo'
import { useAttentionMotion } from '@/components/layout/sidebar/attentionCue'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { AvatarStack } from '@/components/ui/avatar-stack'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { needsAttention, useRoomCardRequestIds, useRoomView } from './useRoomView'

/** Toggle this room's Participants panel through the outgoing panel's leave gate. */
export function toggleParticipantsPanel(workspaceId: string, roomId: string): void {
  const outgoing = useUiStore.getState().activePanel?.id ?? null
  leaveGateThen(outgoing, () => {
    const state = useUiStore.getState()
    const open = state.activePanel?.id === 'participants' && state.activePanel.context.roomId === roomId
    if (open) state.closePanel()
    else state.openPanel('participants', { workspaceId, roomId })
  })
}

export function GroupHeaderControls({ workspaceId, roomId }: { workspaceId: string; roomId: string }) {
  const view = useRoomView(roomId, workspaceId)
  const groupCards = useRoomCardRequestIds(roomId)
  const motion = useAttentionMotion()
  const pressed = useUiStore((state) => state.activePanel?.id === 'participants' && state.activePanel.context.roomId === roomId)
  const room = view.room
  if (!room) return null
  const count = room.members.length
  const items = room.members.map((agentId) => {
    const agent = view.agents?.find((candidate) => candidate.id === agentId)
    return {
      key: agentId,
      node: (
        <AttentionHalo halo={needsAttention(view.memberStates[agentId], groupCards)} motion={motion}>
          <AgentMark agent={agent} name={view.agentName(agentId)} size={18} />
        </AttentionHalo>
      ),
    }
  })
  return (
    <>
      <Tooltip content="Participants" side="bottom" className="w-max" interactive>
        <Button
          variant="ghost"
          size="sm"
          data-testid="participants-button"
          data-panel-trigger="participants"
          aria-pressed={pressed}
          aria-label={`Participants, ${count} ${count === 1 ? 'member' : 'members'}`}
          onClick={() => toggleParticipantsPanel(workspaceId, roomId)}
          className="shrink-0 gap-[var(--space-1)] px-[var(--space-1)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] aria-pressed:text-[var(--color-accent)] pointer-coarse:h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
        >
          <AvatarStack items={items} max={3} total={count} />
          <span data-testid="participants-count" className="text-[length:var(--type-utility-xs-size)]">{count}</span>
        </Button>
      </Tooltip>
    </>
  )
}
