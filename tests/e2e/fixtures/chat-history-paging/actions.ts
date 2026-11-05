import { expect, type Page } from '@playwright/test';
import { SessionHistoryResponse } from '../../../../src/lib/api/generated/schemas';
import { type HistoryHarness, geometry, historyScroller, scrollToDistance, settled } from './harness';

/** Browse through actual UI requests. IDs in this synthetic fixture have
 * authored ordinals; opaque production history positions are never sorted. */
export async function browseBack(history: HistoryHarness, targetOrdinal: number) {
  const opening = expectedReaderOrdinals(history);
  expect(opening.length, 'opening source identities are visible to the traversal instrument').toBeGreaterThan(0);
  let earliest = Math.min(...opening);
  let initialReaderIntent = true;
  while (earliest > targetOrdinal) {
    const before = history.traffic.pages.length;
    if (initialReaderIntent) {
      await historyScroller(history.page).focus();
      await expect(historyScroller(history.page), 'real keyboard reader intent targets the conversation scroller').toBeFocused();
      await history.page.keyboard.press('Home');
      initialReaderIntent = false;
    }
    await scrollToDistance(history.page, 0);
    await expect.poll(() => history.traffic.pages.length, { message: 'genuine UI traversal must produce a page' }).toBeGreaterThan(before);
    const replies = history.traffic.pages.slice(before);
    for (const reply of replies) {
      expect(reply.status, 'failed pages do not count as browser traversal').toBe(200);
      const page = SessionHistoryResponse.parse(reply.body);
      const first = page.entries[0];
      expect(first, 'no empty nonterminal/phantom traversal').toBeDefined();
      const match = first?.id.match(/-(\d{6})$/);
      expect(match, 'this helper only accepts its authored numeric fixture identities').not.toBeNull();
      const ordinal = Number(match![1]);
      const ordinals = page.entries.map((entry) => Number(entry.id.slice(-6)));
      expect(ordinals, 'a physical page contains consecutive authored groups, not a hidden middle gap')
        .toEqual(Array.from({ length: ordinals.length }, (_, n) => ordinal + n));
      // D3 ("Older"): "before=known history position, exclusive; nearest
      // retained predecessors, ascending output." The predecessor to compare
      // against is the row the reader HELD at the position it asked from -
      // not the smallest ordinal this helper has ever seen. The reader sends
      // `before=` its own oldest held row (src/store/chat/history.ts::older
      // sends `before: bucket.history.olderBoundary`, which is `positions[0]`
      // of the held window), so the request's own `before` position names
      // exactly the row whose immediate predecessor the page must resume at.
      //
      // The previous oracle compared against `earliest`, the smallest ordinal
      // in the union of every replay frame and every successful page. That
      // union is not the reader's held window, and it is recorded
      // asynchronously (the harness pushes a page only after
      // `await response.json()` resolves), so it could lag the reader by a
      // whole page: the page was correct for the position it was asked from,
      // yet the union still named an older row.
      const askedFrom = requestedBeforeOrdinal(history, reply.url);
      expect(askedFrom, 'the traversal request carries the position the reader held').toBeDefined();
      expect(ordinals.at(-1), 'exclusive before resumes at the immediate retained predecessor').toBe(askedFrom! - 1);
      expect(ordinal, 'strict progress; unchanged cursors cannot spin a loop').toBeLessThan(earliest);
      earliest = ordinal;
    }
    await settled(history.page);
  }
  // Stay OUTSIDE the2H automatic trigger after arriving. H would itself
  // immediately schedule another page and make the held interval ambiguous.
  await scrollToDistance(history.page, 3 * (await geometry(history.page)).H);
  return earliest;
}

/** Synthetic one-row/group backward traversal: D9 discards the newer far edge
 * once500 groups are held. This expected interval is derived from fixture IDs
 * and the policy, never a guessed order of opaque positions. */
export function expectedReaderOrdinals(history: HistoryHarness): number[] {
  const ids = new Set<string>();
  for (const frame of history.traffic.frames) if (frame.type === 'replay_message' && frame.session_id === history.sessionId && frame.id) ids.add(frame.id);
  for (const reply of history.traffic.pages.filter((r) => r.status === 200)) {
    for (const entry of SessionHistoryResponse.parse(reply.body).entries) ids.add(entry.id);
  }
  return [...ids].map((id) => Number(id.slice(-6))).sort((a, b) => a - b).slice(0, 500);
}

/**
 * The ordinal the reader's OWN request asked to resume from, read off the
 * `before=` parameter the browser actually sent.
 *
 * D3 ("Older"): "before=known history position, exclusive; nearest retained
 * predecessors, ascending output." The predecessor of the position the reader
 * ASKED for is the only thing an older page's last row can be compared to.
 *
 * The previous oracle compared against `earliest` - the smallest ordinal this
 * helper had EVER seen across replay frames and every successful page. That is
 * a UNION of the whole traversal history, not the reader's held window, and it
 * is recorded asynchronously: the harness pushes a page only after
 * `await response.json()` resolves, so the union can lag the reader by a whole
 * page. The reader meanwhile requests `before=` its own oldest HELD row
 * (src/store/chat/history.ts::older sends `before: bucket.history.olderBoundary`,
 * which is `positions[0]` of the held window), and the server answers the
 * nearest retained predecessors of exactly that position. Comparing the
 * response against the stale union therefore failed by one page's worth of
 * rows (expected 3339, received 3319) while the product was correct.
 *
 * The mapping is position -> ordinal, taken from the rows the reader actually
 * holds (replay frames and page entries), so it cannot drift from what the
 * server was asked.
 */
export function requestedBeforeOrdinal(history: HistoryHarness, pageUrl: string): number | undefined {
  const before = new URL(pageUrl).searchParams.get('before');
  if (!before) return undefined;
  for (const row of heldRows(history)) {
    if (row.position === before) return row.ordinal;
  }
  return undefined;
}

/** Every row the reader has been given, with the ordinal its fixture id
 * carries and the opaque position the wire uses. */
function heldRows(history: HistoryHarness): { position: string; ordinal: number }[] {
  const rows: { position: string; ordinal: number }[] = [];
  for (const frame of history.traffic.frames) {
    if (frame.type !== 'replay_message' || frame.session_id !== history.sessionId) continue;
    const position = frame.history_origin?.position;
    if (!position || !frame.id) continue;
    rows.push({ position, ordinal: Number(frame.id.slice(-6)) });
  }
  for (const reply of history.traffic.pages.filter((r) => r.status === 200)) {
    for (const entry of SessionHistoryResponse.parse(reply.body).entries) {
      if (!entry.history_position) continue;
      rows.push({ position: entry.history_position, ordinal: Number(entry.id.slice(-6)) });
    }
  }
  return rows;
}

/** Only close the real browser socket; do not replace constructor or frames. */
export async function dropSocket(page: Page) {
  await page.evaluate(() => {
    const sockets = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [];
    if (!sockets.some((socket) => socket.readyState === WebSocket.OPEN)) throw new Error('socket-loss instrument had no OPEN socket');
    for (const socket of sockets) if (socket.readyState === WebSocket.OPEN) socket.close(1000, 'history-reader-network-loss');
  });
}
