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
 *    width so the icon control is never clipped. That minimum leaves the
 *    label's identity out (its grid column can reach zero), so the identity
 *    is the width the line takes back first, and the strip counts it as
 *    room before it steps down from the full row (useWorkspaceHeaderMode).
 */
import { useLocation } from '@tanstack/react-router'
import { FeedKindLabel } from './FeedKindLabel'
import { MainChatAddParticipant } from './AddParticipant'

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

  return (
    <div
      data-testid="chat-header-slot"
      // The gap grows on a coarse pointer, and the slot RESERVES the room
      // its two touch targets need.
      //
      // Two controls CAN share this line: the failed-roster warning (an icon
      // button whose touch box is the touch minimum, 44px) and "Add
      // participant" (the same). The published package's [data-ds-action]
      // expander grows the participant's hit region 6px past its box; with
      // the ordinary 4px gap the two regions would overlap, and the LATER
      // sibling wins an overlap — a tap meant for the warning would open the
      // participant picker instead. 8px keeps them disjoint in the app and in
      // the package.
      //
      // The 25% ceiling alone cannot hold the two touch targets on a phone:
      // at a 320px line the cap is 80px, smaller than the targets need, so
      // the slot OVERFLOWS and the warning is painted over the participant
      // control (measured in Chromium: an 8px real box overlap at 320px, and
      // a tap meant for the warning opens the picker). A coarse-pointer
      // MINIMUM width — the two touch targets plus the two gaps and the
      // right padding — reserves the room instead: the ceiling yields only
      // where the touch minimum asks for it, the workspace strip keeps the
      // rest of the line, and the identity truncates (the founder's rule:
      // the chat slot shrinks and truncates first, the workspace navigation
      // keeps the header line).
      className="flex max-w-[25%] shrink items-center justify-end gap-[var(--space-1)] pointer-coarse:gap-[var(--space-2)] pointer-coarse:min-w-[calc(var(--target-touch-minimum)*2+var(--space-1)+var(--space-2)+var(--space-2-5))] pr-[var(--space-2-5)]"
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
