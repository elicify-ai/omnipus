// mail-panel-observer-frames.ts: the two server -> client Mail panel presence
// frames, `mail_panel_observer_ack` and `mail_panel_observer_error`
// (contracts/asyncapi.yaml). Own pre-switch handler (provider-frames.ts
// pattern): the main switch in slices/frames.ts::handleFrame is a
// grandfathered budget entry, so new frame families do not grow it.
//
// The ack is recognised and consumed (presence is best-effort; nothing
// tracks acknowledgement). Each recognised error calls the shared diagnostic
// sink once with the frame's closed-class `code`. Actual emission is
// production-only and rate-limited; dev/test emit nothing. No toast, banner
// or correctness gate (Mail keeps working through its request-scoped fallback).

import { logDiagnostic } from '@/lib/telemetry'
import type { ChatStore } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

/** Returns true when the frame was a Mail panel observer ack/error and has been consumed. */
export function handleMailPanelObserverFrame(frame: Frame): boolean {
  switch (frame.type) {
    case 'mail_panel_observer_ack':
      return true
    case 'mail_panel_observer_error':
      logDiagnostic('mailPanelObserverRefused', {
        code: frame.code,
        observerId: frame.observer_id,
        workspaceId: frame.workspace_id,
      })
      return true
    default:
      return false
  }
}
