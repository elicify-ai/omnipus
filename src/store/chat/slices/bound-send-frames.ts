// bound-send-frames.ts: #1428 step 2 frames that are fully consumed BEFORE the
// chat reducer, so the reducer must recognise them instead of counting them
// as unknown (chatUnknownFrameType). Own pre-switch handler
// (mail-panel-observer-frames.ts pattern): the main switch in
// slices/frames.ts::handleFrame is a grandfathered budget entry.
//
// cancel_idle is deliberately NOT listed: nothing in the web client consumes
// it yet, so it stays visible through the unknown-frame diagnostic.

import type { ChatStore } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

/** Returns true when the frame was already handled outside the reducer. */
export function handleBoundSendFrame(frame: Frame): boolean {
  switch (frame.type) {
    // server_capabilities (spec 4.10): decides the socket's send capability in lib/ws.ts::WsConnection (onmessage sniff, _decideCapability).
    // send_refused (spec 4.8): withdraws the refused message in lib/bound-send.ts::observeBoundSendFrame (onSendRefused).
    // command_received (spec 4.7): confirms the command record in lib/bound-send.ts::observeBoundSendFrame (onCommandReceived).
    case 'server_capabilities':
    case 'send_refused':
    case 'command_received':
      return true
    default:
      return false
  }
}
