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
 * The group chat (frames 3/4 of the approved wireframe: the avatar stack +
 * count as the Participants button) plugs into this same slot
 * (GroupHeaderControls, #1449 US-11).
 *
 * TWO rules the slot must keep:
 *
 * 1. The session chrome belongs to the SESSION chat. A room page is a
 *    different conversation, so it shows the GROUP's controls and none of
 *    the remembered session's: without this, opening a group after a main
 *    chat would show the main chat's title and an "Add participant" action
 *    that creates yet another group from the remembered main instead of
 *    adding to the group on screen.
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
import { GroupHeaderControls } from '@/components/rooms/GroupHeaderControls'

/** The room id of a `/workspaces/<id>/rooms/<roomId>` address, or null. */
export function roomIdOfPath(pathname: string, workspaceId: string): string | null {
  const normalized = pathname.replace(/\/+$/, '')
  const prefix = `/workspaces/${workspaceId}/rooms/`
  if (!normalized.startsWith(prefix)) return null
  const rest = normalized.slice(prefix.length)
  return rest.length > 0 && !rest.includes('/') ? decodeURIComponent(rest) : null
}

export function ChatHeaderSlot({ workspaceId }: { workspaceId: string }) {
  const { pathname } = useLocation()
  // The router accepts a chat address with a trailing slash
  // ("/workspaces/<id>/chat/") and matches the SAME route, so the slot must
  // read the same route: normalise the trailing slashes before comparing —
  // the same normalisation usePanelDeepLink's isOwningChat does. Room paths
  // stay excluded: they are a different conversation.
  const normalized = pathname.replace(/\/+$/, '')
  const base = `/workspaces/${workspaceId}`
  const isSessionChat = normalized === base || normalized === `${base}/chat`
  const roomId = roomIdOfPath(pathname, workspaceId)

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
      ) : roomId ? (
        <GroupHeaderControls key={roomId} workspaceId={workspaceId} roomId={roomId} />
      ) : null}
    </div>
  )
}
