// useNoticeClock — #1537 FR-008 (helper-model-notices spec, "Reload and
// reconnect" item 2): the provider notices' countdown and silence time are
// measured with the chat's server-clock estimate (store
// `providerServerClock`: the largest server-minus-client offset over the
// frames received, `session_state.emitted_at` included), not the single
// frame's `sent_at`. A freshly attached tab therefore counts a re-sent 30 s
// retry from the server's current time (s30 − s5 = 25 s), not from 30 s.
//
// The anchor is frozen when the notice changes, so a later clock sample can
// never restart or re-announce a notice already shown. The indicators take
// the anchor as their (sentAt, receivedAt) pair: their unchanged formula
// serverNow = sentAt + (clientNow − receivedAt) then yields the estimate.

import { useState } from 'react'
import type { ProviderServerClock } from '@/store/chat/types'

export interface NoticeClockAnchor {
  sentAt: string
  receivedAt: string
}

/**
 * The estimate as an indicator anchor; the notice's own pair when no estimate
 * exists or (#1537 S1) either value is not a valid Date — never a throw from
 * rendering, never a mixed pair.
 */
export function clockAnchor(clock: ProviderServerClock | null | undefined, own: NoticeClockAnchor): NoticeClockAnchor {
  if (!clock) return own
  const server = new Date(clock.serverMs)
  const client = new Date(clock.clientMs)
  if (Number.isNaN(server.getTime()) || Number.isNaN(client.getTime())) return own
  return { sentAt: server.toISOString(), receivedAt: client.toISOString() }
}

/** Freeze the chat's clock estimate per notice (identity). */
export function useNoticeClock(
  notice: object | null | undefined,
  clock: ProviderServerClock | null | undefined,
  own: NoticeClockAnchor | null,
): NoticeClockAnchor | null {
  const [frozen, setFrozen] = useState(() => ({ notice, clock }))
  let current = frozen
  if (frozen.notice !== notice) {
    // React's "adjust state while rendering" pattern: re-anchor on a new notice.
    current = { notice, clock }
    setFrozen(current)
  }
  return own ? clockAnchor(current.clock, own) : null
}
