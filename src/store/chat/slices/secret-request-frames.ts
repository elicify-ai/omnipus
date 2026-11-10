// secret-request-frames.ts: the secure secret card's frames
// (omnipus-system-agent K1, K3, K4; FR-074, FR-079). Own pre-switch handler
// (mail-panel-observer-frames.ts pattern): the main switch in
// slices/frames.ts::handleFrame is a grandfathered budget entry.
//
// `secret_request` and `secret_request_resolved` are consumed here. The two
// session-state carriers (`session_state`, `session_snapshot`) are handled by
// their own handlers, which call reconcileSecretRequests for their
// `secret_requests` list. A carrier WITHOUT the field says nothing about
// secret requests and changes nothing.

import type { SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import { useSecretRequestStore } from '@/store/secretRequests'
import type { ChatStore } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

/** Returns true when the frame was a secret-card frame and has been consumed. */
export function handleSecretRequestFrame(frame: Frame): boolean {
  switch (frame.type) {
    case 'secret_request':
      useSecretRequestStore.getState().upsertFromFrame(frame)
      return true
    case 'secret_request_resolved':
      useSecretRequestStore.getState().markResolved(frame)
      return true
    default:
      return false
  }
}

/** K4: apply a session-state carrier's `secret_requests` list, when present. */
export function reconcileSecretRequests(frame: {
  session_id?: string
  secret_requests?: SessionStateSecretRequest[]
}): void {
  if (!frame.secret_requests || !frame.session_id) return
  useSecretRequestStore.getState().reconcileSession(frame.session_id, frame.secret_requests)
}
