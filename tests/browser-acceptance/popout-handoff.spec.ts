import { browserTestWorkspacePath } from './test-workspace';
import fs from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';

// Oracle: user-required exclusive client ownership, not a backend detach ACK.
// Browser APIs remain real. Observe lifecycle only; never record URLs, SDP,
// credentials, text, binary input, handover identifiers or page content.
type Observation = { page: number; document: string; socket?: number; event: string; at: number };
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
const workspace = browserTestWorkspacePath;

test('popout transfers one client viewer and returns only to its owner after actual close', async ({ page: source, context }, info) => {
  const observations: Observation[] = [], pageIDs = new Map<Page, number>();
  const errors: Array<{ page: number; message: string }> = [];
  const marks: Array<{ label: string; at: string }> = [];
  let overflow = false, popup: Page | undefined, unrelated: Page | undefined;
  const identify = (page: Page) => {
    if (!pageIDs.has(page)) {
      pageIDs.set(page, pageIDs.size + 1);
      page.on('pageerror', error => errors.push({ page: pageIDs.get(page)!, message: error.name }));
    }
    return pageIDs.get(page)!;
  };
  const sourceID = identify(source);
  await context.exposeBinding('__recordBrowserHandoff', ({ page }, value: Omit<Observation, 'page'>) => {
    if (observations.length >= 4096) { overflow = true; return; }
    observations.push({ ...value, page: identify(page) });
  });
  await context.addInitScript(() => {
    const documentID = crypto.randomUUID();
    const record = (event: string, socket?: number) => {
      void (window as unknown as { __recordBrowserHandoff(value: { document: string; socket?: number; event: string; at: number }): Promise<void> })
        .__recordBrowserHandoff({ document: documentID, socket, event, at: performance.timeOrigin + performance.now() });
    };
    const NativeSocket = window.WebSocket;
    let serial = 0;
    class ObservedSocket extends NativeSocket {
      private observed: boolean;
      private identity: number;
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        this.observed = new URL(String(url), location.href).pathname === '/api/v1/browser/ws';
        this.identity = ++serial;
      }
      send(data: Parameters<WebSocket['send']>[0]) {
        super.send(data);
        if (!this.observed || typeof data !== 'string') return;
        let frame: { type?: string };
        try { frame = JSON.parse(data); } catch { return; }
        if (['browser_attach', 'browser_detach', 'browser_viewport'].includes(frame.type || '')) record(frame.type!, this.identity);
      }
      close(code?: number, reason?: string) {
        super.close(code, reason);
        if (this.observed) record('socket_close_requested', this.identity);
      }
    }
    window.WebSocket = ObservedSocket;
    const nativeClose = RTCPeerConnection.prototype.close;
    RTCPeerConnection.prototype.close = function () { nativeClose.call(this); record('peer_close_requested'); };
  });
  const ready = async (page: Page) => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLiveVideo(page)).toBeVisible();
    await expect.poll(() => browserLiveVideo(page).evaluate(node => (node as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
  };
  const forPage = (id: number, event: string) => observations.filter(row => row.page === id && row.event === event);
  try {
    // Load the unrelated same-origin app BEFORE opening source's browser, so
    // any later unsolicited re-dock is observed in an already-mounted app.
    unrelated = await context.newPage(); const unrelatedID = identify(unrelated);
    await unrelated.goto(workspace); await selectAgent(unrelated, 'Browser UAT Test');
    await expect(browserLivePanel(unrelated)).toHaveCount(0);
    await source.goto(workspace); await selectAgent(source, 'Browser UAT Test');
    await source.getByRole('button', { name: 'Open browser', exact: true }).click();
    await ready(source); await expect(browserLivePanel(source)).toBeVisible();
    await expect.poll(() => forPage(sourceID, 'browser_attach').length).toBe(1);
    const [opened] = await Promise.all([
      context.waitForEvent('page'),
      source.getByRole('button', { name: 'Pop out', exact: true }).click(),
    ]);
    popup = opened; const popupID = identify(popup);
    await popup.waitForURL(url => url.hash.split('?')[0] === '#/browser-live');
    await ready(popup);
    expect(await popup.evaluate(() => window.opener === null)).toBe(true);
    await expect(browserLivePanel(source)).toHaveCount(0);
    await expect(browserLivePanel(unrelated)).toHaveCount(0);
    await expect.poll(() => forPage(popupID, 'browser_attach').length).toBe(1);
    const childAttach = forPage(popupID, 'browser_attach')[0].at;
    expect(forPage(sourceID, 'browser_detach').filter(row => row.at < childAttach)).toHaveLength(1);
    expect(forPage(sourceID, 'socket_close_requested').some(row => row.at < childAttach)).toBe(true);
    expect(forPage(sourceID, 'peer_close_requested').filter(row => row.at < childAttach).length).toBeGreaterThanOrEqual(2);
    marks.push({ label: 'source-disconnected-popup-ready', at: new Date().toISOString() });
    const sourceAttachCount = forPage(sourceID, 'browser_attach').length;
    const sourceViewportCount = forPage(sourceID, 'browser_viewport').length;
    await source.setViewportSize({ width: 1100, height: 760 });
    // Longer than the existing 400ms resize debounce; old dock must be absent.
    await source.waitForTimeout(1200);
    expect(forPage(sourceID, 'browser_viewport')).toHaveLength(sourceViewportCount);
    await source.bringToFront();
    await source.getByRole('button', { name: 'Open browser', exact: true }).click();
    await expect(browserLivePanel(source)).toHaveCount(0);
    expect(context.pages().filter(page => !page.isClosed())).toHaveLength(3);
    await popup.reload(); await ready(popup);
    await expect.poll(() => forPage(popupID, 'browser_attach').length).toBe(2);
    await expect(browserLivePanel(source)).toHaveCount(0);
    await expect(browserLivePanel(unrelated)).toHaveCount(0);
    expect(forPage(sourceID, 'browser_attach')).toHaveLength(sourceAttachCount);
    expect(forPage(sourceID, 'browser_viewport')).toHaveLength(sourceViewportCount);
    marks.push({ label: 'popup-reload-did-not-redock', at: new Date().toISOString() });
    // A window.close() destroys the browsing context; it need not call the
    // JavaScript socket wrappers. Require actual close before accepting return.
    await Promise.all([popup.waitForEvent('close'), popup.getByRole('button', { name: 'Close live browser panel', exact: true }).click()]);
    expect(popup.isClosed()).toBe(true);
    await ready(source); await expect(browserLivePanel(source)).toBeVisible();
    await expect(browserLivePanel(unrelated)).toHaveCount(0);
    await expect.poll(() => forPage(sourceID, 'browser_attach').length).toBe(sourceAttachCount + 1);
    expect(forPage(unrelatedID, 'browser_attach')).toHaveLength(0);
    marks.push({ label: 'product-close-owner-only-return-ready', at: new Date().toISOString() });
    // Native tab close must work too, without depending on the product button.
    const sourceDetachCount = forPage(sourceID, 'browser_detach').length;
    const [reopened] = await Promise.all([
      context.waitForEvent('page'),
      source.getByRole('button', { name: 'Pop out', exact: true }).click(),
    ]);
    popup = reopened; const reopenedID = identify(popup);
    await popup.waitForURL(url => url.hash.split('?')[0] === '#/browser-live');
    await ready(popup);
    expect(await popup.evaluate(() => window.opener === null)).toBe(true);
    await expect(browserLivePanel(source)).toHaveCount(0);
    await expect.poll(() => forPage(reopenedID, 'browser_attach').length).toBe(1);
    const reopenedAttach = forPage(reopenedID, 'browser_attach')[0].at;
    const secondDetach = forPage(sourceID, 'browser_detach')[sourceDetachCount];
    expect(secondDetach).toBeDefined();
    expect(secondDetach.at).toBeLessThan(reopenedAttach);
    expect(forPage(sourceID, 'browser_attach')).toHaveLength(sourceAttachCount + 1);
    await popup.close();
    expect(popup.isClosed()).toBe(true);
    await ready(source); await expect(browserLivePanel(source)).toBeVisible();
    await expect(browserLivePanel(unrelated)).toHaveCount(0);
    await expect.poll(() => forPage(sourceID, 'browser_attach').length).toBe(sourceAttachCount + 2);
    expect(forPage(unrelatedID, 'browser_attach')).toHaveLength(0);
    expect(overflow, 'bounded observer overflowed').toBe(false);
    expect(errors).toEqual([]);
    marks.push({ label: 'native-close-owner-only-return-ready', at: new Date().toISOString() });
  } finally {
    const artifact = info.outputPath('popout-handoff-evidence.json');
    fs.writeFileSync(artifact, JSON.stringify({ provenance, testWorkspace: browserTestWorkspacePath, sourcePage: sourceID, marks, observations, overflow, errors }, null, 2));
    await info.attach('popout-handoff-evidence', { path: artifact, contentType: 'application/json' });
    if (popup && !popup.isClosed()) await popup.close().catch(() => {});
    if (!source.isClosed()) await source.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
    if (unrelated && !unrelated.isClosed()) await unrelated.close().catch(() => {});
  }
});
