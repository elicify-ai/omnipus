import type { Page, WebSocket } from '@playwright/test';
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas';

/** Passive native transport evidence only: never send frames or read a client store. */
export function observeReplayAttach(page: Page, sessionId: string, onAttach?: () => void) {
  const received: WsFrame[] = [];
  const failures: string[] = [];
  // not-wire-format: local observation positions alongside generated frames.
  const attaches: { frame: Extract<WsFrame, { type: 'attach_session' }>; receivedBefore: number }[] = [];
  const cleanup: (() => void)[] = [];
  let currentSocket: WebSocket | undefined;
  let socketGeneration = 0;
  let experimentGeneration: number | undefined;
  let reconnectError: Error | undefined;
  const assertNoReconnect = () => { if (reconnectError) throw reconnectError; };
  const tokens = (after = 0) => received.slice(after).filter((frame): frame is Extract<WsFrame, { type: 'token' }> =>
    frame.type === 'token' && frame.replace !== true);
  const parse = (payload: string | Buffer) => {
    const parsed = WsFrame.safeParse(JSON.parse(payload.toString()));
    if (!parsed.success) throw new Error(`invalid native replay frame: ${parsed.error.message}`);
    return parsed.data;
  };
  const onSocket = (socket: WebSocket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return;
    currentSocket = socket;
    socketGeneration++;
    if (experimentGeneration !== undefined) {
      if (!reconnectError) {
        reconnectError = new Error(`reconnect during experiment: session ${sessionId}, socket generation ${experimentGeneration} -> ${socketGeneration}`);
        failures.push(reconnectError.message);
      }
      // Freeze the original evidence: a recovery socket must not repair a lost
      // delta or concatenate a second copy into the single-socket experiment.
      return;
    }
    // Setup may reconnect. Only the current socket can establish the baseline.
    received.length = 0;
    attaches.length = 0;
    const sent = ({ payload }: { payload: string | Buffer }) => {
      if (socket !== currentSocket) return;
      try {
        const frame = parse(payload);
        if (frame.type !== 'attach_session' || frame.session_id !== sessionId) return;
        attaches.push({ frame, receivedBefore: received.length });
        onAttach?.();
      } catch (error) { failures.push(String(error)); }
    };
    const incoming = ({ payload }: { payload: string | Buffer }) => {
      if (socket !== currentSocket) return;
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
    received, attaches, failures, assertNoReconnect,
    startExperiment() {
      assertNoReconnect();
      if (!currentSocket) throw new Error('replay observer has no native socket when experiment starts');
      experimentGeneration = socketGeneration;
    },
    liveTokens: (after = 0) => { assertNoReconnect(); return tokens(after); },
    liveDone: (turnId: string) => {
      assertNoReconnect();
      return received.filter((frame): frame is Extract<WsFrame, { type: 'done' }> =>
        frame.type === 'done' && frame.turn_id === turnId);
    },
    // Evidence remains readable even when a measurement deliberately rejects a reconnect.
    snapshot: (after = 0) => ({ tokens: tokens(after), attaches: [...attaches], failures: [...failures], socketGeneration, experimentGeneration }),
    dispose() { page.off('websocket', onSocket); for (const remove of cleanup) remove(); },
  };
}
