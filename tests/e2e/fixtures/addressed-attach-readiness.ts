import type { Page, WebSocket } from '@playwright/test';
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas';

// not-wire-format: local observation proof referencing generated wire frames.
type AttachProof = {
  request: Extract<WsFrame, { type: 'attach_session' }>;
  completion: Extract<WsFrame, { type: 'catch_up_complete' }>;
};
// not-wire-format: one local waiter, armed before navigation to avoid missed events.
type Waiter = {
  after: number;
  sessionId?: string;
  proof?: AttachProof;
  error?: Error;
  resolve?: (proof: AttachProof) => void;
  reject?: (error: Error) => void;
};

/** Observe native chat sockets; never send frames, mock transport or read a store.
 * The existing whole-test deadline bounds waiting, not a new enlarged UI poll. */
export function observeAddressedAttachReadiness(page: Page) {
  let ordinal = 0;
  let failure: Error | undefined;
  let currentSocket: WebSocket | undefined;
  let boundSessionId: string | undefined;
  const completed = new Map<string, AttachProof>();
  const waiters = new Set<Waiter>();
  const cleanup = new Set<() => void>();
  const fail = (error: Error) => {
    failure ??= error;
    for (const waiter of waiters) {
      waiter.error = error;
      waiter.reject?.(error);
    }
  };
  const onSocket = (socket: WebSocket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return;
    currentSocket = socket;
    boundSessionId = undefined;
    completed.clear(); // A prior document/socket cannot prove the new connection ready.
    const requests = new Map<string, { ordinal: number; frame: AttachProof['request'] }[]>();
    const parse = (payload: string | Buffer) => {
      const parsed = WsFrame.safeParse(JSON.parse(payload.toString()));
      if (!parsed.success) throw new Error(`Addressed attach readiness: invalid native frame: ${parsed.error.message}`);
      return parsed.data;
    };
    const sent = ({ payload }: { payload: string | Buffer }) => {
      try {
        const frame = parse(payload);
        if (frame.type !== 'attach_session') return;
        if (socket === currentSocket) { boundSessionId = frame.session_id; completed.clear(); }
        const queue = requests.get(frame.session_id) ?? [];
        queue.push({ ordinal: ++ordinal, frame });
        requests.set(frame.session_id, queue);
      } catch (error) { fail(error instanceof Error ? error : new Error(String(error))); }
    };
    const received = ({ payload }: { payload: string | Buffer }) => {
      try {
        const frame = parse(payload);
        if (frame.type !== 'catch_up_complete') return; // replay done is not readiness.
        const request = requests.get(frame.session_id)?.shift();
        if (!request) return; // No observed request means no proof for this observer.
        if (socket !== currentSocket) return; // A superseded connection cannot prove current readiness.
        if (boundSessionId === frame.session_id) completed.set(frame.session_id, { request: request.frame, completion: frame });
        for (const waiter of waiters) {
          if (waiter.proof || request.ordinal <= waiter.after || (waiter.sessionId && waiter.sessionId !== frame.session_id)) continue;
          waiter.proof = { request: request.frame, completion: frame };
          waiter.resolve?.(waiter.proof);
        }
      } catch (error) { fail(error instanceof Error ? error : new Error(String(error))); }
    };
    const closed = () => { if (socket === currentSocket) { currentSocket = undefined; boundSessionId = undefined; completed.clear(); } };
    socket.on('framesent', sent);
    socket.on('framereceived', received);
    socket.on('close', closed);
    cleanup.add(() => { socket.off('framesent', sent); socket.off('framereceived', received); socket.off('close', closed); });
  };
  const dispose = () => {
    fail(new Error('Page closed before its addressed attach completed'));
    page.off('websocket', onSocket);
    page.off('close', dispose);
    for (const remove of cleanup) remove();
    cleanup.clear();
    completed.clear();
    currentSocket = undefined;
    boundSessionId = undefined;
    waiters.clear();
  };
  page.on('websocket', onSocket);
  page.once('close', dispose);
  return {
    completedOnCurrentSocket(sessionId: string) { return completed.get(sessionId); },
    arm(sessionId?: string) {
      const waiter: Waiter = { after: ordinal, sessionId, error: failure };
      waiters.add(waiter);
      return {
        async wait(): Promise<AttachProof> {
          if (waiter.error) throw waiter.error;
          if (waiter.proof) return waiter.proof;
          return new Promise<AttachProof>((resolve, reject) => { waiter.resolve = resolve; waiter.reject = reject; });
        },
        cancel() { waiters.delete(waiter); },
      };
    },
    dispose,
  };
}
