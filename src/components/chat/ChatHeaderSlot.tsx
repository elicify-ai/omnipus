/**
 * ChatHeaderSlot — the ONE right-end slot of the workspace header line
 * (#1449, requirements D13; founder 2026-10-28: "I do not want to see header
 * borders or anything; stick to our minimal design … the existing header
 * should stay on the left and not change location").
 *
 * The slot sits in the SAME line as the workspace strip (the banner row of
 * WorkspaceTabContainer) — no new row, no bar, no border, no background of
 * its own. It carries the chat's own chrome:
 *
 *   - the chat label (FeedKindLabel): a main chat's title, a helper/task
 *     chat's "‹ Parent / Child" breadcrumb, or a task transcript's task
 *     label. This is the grey, bottom-bordered bar that used to sit above
 *     the feed — now un-bordered small muted text in the header line.
 *   - the chat controls: "Add participant" as an icon-only ghost button.
 *
 * The later group chat (frames 3/4 of the approved wireframe: the avatar
 * stack + count as the Participants button) plugs into this same slot; it is
 * deliberately NOT implemented here.
 *
 * TWO rules the slot must keep:
 *
 * 1. It belongs to the SESSION chat. A room page is a different
 *    conversation — the room header owns its own controls until the group
 *    Participants button lands — so the slot stays EMPTY there. Without
 *    this, opening a group after a main chat would show the main chat's
 *    title and an "Add participant" action that creates yet another group
 *    from the remembered main instead of adding to the group on screen.
 *
 * 2. The workspace strip always wins the width. The slot's share of the
 *    header line is capped (max-w) so it can never squeeze the workspace
 *    navigation out of the line; the label inside truncates, and the whole
 *    slot shrinks under pressure. The slot keeps its automatic minimum
 *    width so the icon control is never clipped.
 */
import { useLocation } from '@tanstack/react-router'
import { FeedKindLabel } from './FeedKindLabel'
import { MainChatAddParticipant } from './AddParticipant'

export function ChatHeaderSlot({ workspaceId }: { workspaceId: string }) {
  const { pathname } = useLocation()
  const base = `/workspaces/${workspaceId}`
  const isSessionChat = pathname === base || pathname === `${base}/chat`

  return (
    <div
      data-testid="chat-header-slot"
      className="flex max-w-[25%] shrink items-center justify-end gap-[var(--space-1)] pr-[var(--space-2-5)]"
    >
      {isSessionChat ? (
        <>
          <FeedKindLabel />
          <MainChatAddParticipant />
        </>
      ) : null}
    </div>
  )
}
