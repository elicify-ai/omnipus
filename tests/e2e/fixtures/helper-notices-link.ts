// Fault only delivery of REAL gateway frames. Never synthesize a provider frame.
import { expect, type Page, type WebSocketRoute } from '@playwright/test';

export async function installNoticeLink(page: Page) {
  let offline = false;
  let forceSnapshot = false;
  let holdEnd = false;
  let root = '';
  const held: Array<{ raw: string | Buffer; socket: WebSocketRoute; frame: Record<string, unknown> }> = [];
  const blocked = new Set<WebSocketRoute>();
  const sockets: Array<{ client: WebSocketRoute; server: WebSocketRoute }> = [];
  const frames: Record<string, unknown>[] = [];
  const events: Array<{ kind: string; at: number }> = [];
  await page.routeWebSocket('**/api/v1/chat/ws*', socket => {
    events.push({ kind: offline ? 'rejected-offline' : 'connect-to-server', at: Date.now() });
    if (offline) { socket.close({ code: 1001, reason: 'scripted network outage' }); return; }
    const server = socket.connectToServer();
    sockets.push({ client: socket, server });
    socket.onMessage(raw => {
      const request = JSON.parse(raw.toString()) as Record<string, unknown>;
      if (forceSnapshot && request.type === 'attach_session' && request.session_id === root) {
        // Keep the client's real boot id; only age its requested cursor.
        // The real hub must select retention_exceeded (or live_only if its
        // actual history is unavailable), never a fabricated server reply.
        server.send(JSON.stringify({ ...request, since_seq: 0 }));
      } else server.send(raw);
    });
    server.onMessage(raw => {
      const frame = JSON.parse(raw.toString()) as Record<string, unknown>;
      frames.push(frame);
      if (holdEnd && frame.type === 'provider_status' && frame.state === 'ended' && frame.session_id === root) blocked.add(socket);
      // T8 split B: a transport can stall an ordered suffix, never let newer
      // frames overtake a held sequenced end (including an attach replay).
      if (blocked.has(socket)) { held.push({ raw, socket, frame }); return; }
      if (!offline) socket.send(raw);
    });
  });
  return {
    frames, events,
    setRoot(id: string) { root = id; },
    disconnect() {
      expect(sockets.length, 'network instrument owns a real connected socket').toBeGreaterThan(0);
      events.push({ kind: 'disconnect', at: Date.now() });
      offline = true;
      for (const link of sockets) { link.client.close({ code: 1001, reason: 'scripted network outage' }); link.server.close(); }
    },
    async reconnect(snapshot = false) {
      events.push({ kind: 'reconnect', at: Date.now() });
      forceSnapshot = snapshot; offline = false;
      // Restore the network edge, including its ordinary browser signal.
      // After a real 30 s tool, the offline retry at31s may just have failed;
      // a 15 s test wait cannot cover the next documented 30 s backoff. Do not
      // widen it or reach into connection-manager state to force a socket.
      await page.evaluate(() => window.dispatchEvent(new Event('online')));
      events.push({ kind: 'online-dispatched', at: Date.now() });
    },
    holdEnded() { holdEnd = true; },
    heldEnded() { return held.filter(item => item.frame.type === 'provider_status' && item.frame.state === 'ended').map(item => item.frame); },
    deliverHeldEnd() {
      expect(held.some(item => item.frame.type === 'provider_status' && item.frame.state === 'ended'),
        'positive instrument: a real ended frame starts the ordered held suffix').toBe(true);
      holdEnd = false;
      const delivered = held.splice(0);
      for (const item of delivered) item.socket.send(item.raw);
      blocked.clear();
      return delivered.map(item => item.frame);
    },
  };
}
