// room-frames.ts: group-chat frames (omnipus-system-agent K45; FR-199,
// FR-201, FR-217). A room is not a session: its frames carry a ROOM sequence
// and no session id, so they are consumed here before the chat store's
// session routing, sequence gate and reconnect cursor ever see them — a room
// frame must not advance, gap-check or create any agent-session bucket.
// The main switch in slices/frames.ts::handleFrame is a grandfathered budget
// entry, hence an own handler (secret-request-frames.ts pattern).

import { useRoomStore } from '@/store/rooms'
import type { ChatStore } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

/** Returns true when the frame was a room frame and has been consumed. */
export function handleRoomFrame(frame: Frame): boolean {
  switch (frame.type) {
    case 'room_snapshot':
    case 'room_entry':
    case 'room_member_state':
    case 'room_meta':
      useRoomStore.getState().applyRoomFrame(frame)
      return true
    default:
      return false
  }
}
