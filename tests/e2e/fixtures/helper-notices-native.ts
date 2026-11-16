// T14: passive evidence for real browser-native WebSockets, no routeWebSocket.
import { expect, type Page } from '@playwright/test';
import type { NoticeTraffic } from './helper-notices-harness';
import { test } from './console-errors';

export function directNoticeLink(page: Page, traffic: NoticeTraffic) {
  const events: Array<{ kind: string; at: number }> = [];
  page.on('websocket', socket => {
    if (socket.url().includes('/api/v1/chat/ws')) events.push({ kind: 'native-no-proxy', at: Date.now() });
  });
  const refuseFault = (): never => { throw new Error('T14 native measurement cannot hold, disconnect or rewrite gateway frames'); };
  return {
    frames: traffic.frames, forwarded: traffic.frames, events,
    setRoot(id: string) { expect(id, 'native delivery instrument: actual root session exists').not.toBe(''); },
    disconnect: refuseFault, reconnect: refuseFault, holdEnded: refuseFault, heldEnded: refuseFault, deliverHeldEnd: refuseFault,
  };
}

// Anonymous scopes preserve the original full titles. Capture is disabled at
// file scope (trace/video are worker options); assert actual resolved fixtures.
export function countdownReloadCases(cases: () => void) {
  test.describe(() => {
    test.beforeEach(async ({ trace, video, page }, info) => {
      expect(trace, 'T14 measurement policy: tracing is off').toBe('off');
      expect(video, 'T14 measurement policy: video is off').toBe('off');
      expect(page.video(), 'T14 measurement policy: no recording object').toBeNull();
      await info.attach('helper-notice-reload-capture-policy', { body: JSON.stringify({ trace, video, clock: 'navigation/reload performance.timeOrigin',
        reloads: 'serial', transport: 'native-no-proxy during measured reload', proxyDelaySubtractedMs: 0 }), contentType: 'application/json' });
    });
    cases();
  });
}
