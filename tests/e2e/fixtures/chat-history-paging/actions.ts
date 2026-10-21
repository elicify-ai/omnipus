import { expect, type Page } from '@playwright/test';
import { SessionHistoryResponse } from '../../../../src/lib/api/generated/schemas';
import { type HistoryHarness, geometry, scrollToDistance, settled } from './harness';

/** Browse through actual UI requests. IDs in this synthetic fixture have
 * authored ordinals; opaque production history positions are never sorted. */
export async function browseBack(history: HistoryHarness, targetOrdinal: number) {
  const opening = expectedReaderOrdinals(history);
  expect(opening.length, 'opening source identities are visible to the traversal instrument').toBeGreaterThan(0);
  let earliest = Math.min(...opening);
  while (earliest > targetOrdinal) {
    const before = history.traffic.pages.length;
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
      expect(ordinals.at(-1), 'exclusive before resumes at the immediate retained predecessor').toBe(earliest - 1);
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

/** Only close the real browser socket; do not replace constructor or frames. */
export async function dropSocket(page: Page) {
  await page.evaluate(() => {
    const sockets = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [];
    if (!sockets.some((socket) => socket.readyState === WebSocket.OPEN)) throw new Error('socket-loss instrument had no OPEN socket');
    for (const socket of sockets) if (socket.readyState === WebSocket.OPEN) socket.close(1000, 'history-reader-network-loss');
  });
}
