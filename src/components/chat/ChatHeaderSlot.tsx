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
 * Narrow widths: the slot never forces a width. It shrinks with the header
 * line (flex shrink), the label truncates and carries its full text in the
 * title, and the controls stay icon-only — so the workspace strip keeps its
 * room and its own full/icons/narrow modes.
 */
import { FeedKindLabel } from './FeedKindLabel'
import { MainChatAddParticipant } from './AddParticipant'

export function ChatHeaderSlot() {
  return (
    <div
      data-testid="chat-header-slot"
      className="flex shrink items-center justify-end gap-[var(--space-1)] pr-[var(--space-2-5)]"
    >
      <FeedKindLabel />
      <MainChatAddParticipant />
    </div>
  )
}
