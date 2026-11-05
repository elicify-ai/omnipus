import type { Page, WebSocket } from '@playwright/test';
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas';

/** Passive native transport evidence only: never send frames or read a client store. */
export function observeReplayAttach(page: Page, sessionId: string, onAttach?: () => void) {
  const received: WsFrame[] = [];
  const failures: string[] = [];
  // not-wire-format: local observation positions alongside generated frames.
  const attaches: { frame: Extract<WsFrame, { type: 'attach_session' }>; receivedBefore: number }[] = [];
  const cleanup: (() => void)[] = [];
  const parse = (payload: string | Buffer) => {
    const parsed = WsFrame.safeParse(JSON.parse(payload.toString()));
    if (!parsed.success) throw new Error(`invalid native replay frame: ${parsed.error.message}`);
    return parsed.data;
  };
  const onSocket = (socket: WebSocket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return;
    const sent = ({ payload }: { payload: string | Buffer }) => {
      try {
        const frame = parse(payload);
        if (frame.type !== 'attach_session' || frame.session_id !== sessionId) return;
        attaches.push({ frame, receivedBefore: received.length });
        onAttach?.();
      } catch (error) { failures.push(String(error)); }
    };
    const incoming = ({ payload }: { payload: string | Buffer }) => {
      try {
        const frame = parse(payload);
        if ('session_id' in frame && frame.session_id === sessionId) received.push(frame);
      } catch (error) { failures.push(String(error)); }
    };
    socket.on('framesent', sent);
    socket.on('framereceived', incoming);
    cleanup.push(() => { socket.off('framesent', sent); socket.off('framereceived', incoming); });
  };
  page.on('websocket', onSocket);
  return {
    received, attaches, failures,
    liveTokens: (after = 0) => received.slice(after).filter((frame): frame is Extract<WsFrame, { type: 'token' }> =>
      frame.type === 'token' && frame.replace !== true),
    liveDone: (turnId: string) => received.filter((frame): frame is Extract<WsFrame, { type: 'done' }> =>
      frame.type === 'done' && frame.turn_id === turnId),
    dispose() { page.off('websocket', onSocket); for (const remove of cleanup) remove(); },
  };
}
