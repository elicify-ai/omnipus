import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { expect, type Page } from '@playwright/test';
import { SessionHistoryResponse } from '../../../../src/lib/api/generated/schemas';
import { type HistoryHarness, geometry, scrollToDistance, settled } from './harness';

/** Browse through actual UI requests. IDs in this synthetic fixture have
 * authored ordinals; opaque production history positions are never sorted. */
export async function browseBack(history: HistoryHarness, targetOrdinal: number) {
  let earliest = Number.POSITIVE_INFINITY;
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
      expect(ordinal, 'strict progress; unchanged cursors cannot spin a loop').toBeLessThan(earliest);
      earliest = ordinal;
    }
    await settled(history.page);
  }
  // Move off the loaded edge, so a later passive refresh has an unambiguous
  // old reader instead of a user-driven earlier-page action still in progress.
  await scrollToDistance(history.page, (await geometry(history.page)).H);
  return earliest;
}

/** Only close the real browser socket; do not replace constructor or frames. */
export async function dropSocket(page: Page) {
  await page.evaluate(() => {
    const sockets = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [];
    if (!sockets.some((socket) => socket.readyState === WebSocket.OPEN)) throw new Error('socket-loss instrument had no OPEN socket');
    for (const socket of sockets) if (socket.readyState === WebSocket.OPEN) socket.close(1000, 'history-reader-network-loss');
  });
}

/** Missing implementation is a loud RED, never a skip or a guessed filesystem
 * layout. Backend/architect must supply the actual derived-index artifact seam.
 * This is PRIVATE harness input, not a proposed product config key. */
export function removeDeclaredHistoryIndex(history: HistoryHarness) {
  const raw = process.env.OMNIPUS_E2E_HISTORY_INDEX_ARTIFACTS;
  if (!raw) throw new Error('BLOCKED: chat-index artifact seam not implemented/agreed — required by D1/D7a/D-N2; backend must name derived artifacts before this test may remove them');
  const artifacts: unknown = JSON.parse(raw);
  if (!Array.isArray(artifacts) || artifacts.length === 0 || !artifacts.every((p) => typeof p === 'string' && p.length > 0)) {
    throw new Error('Invalid private fixture OMNIPUS_E2E_HISTORY_INDEX_ARTIFACTS: expected nonempty JSON relative-path list');
  }
  const root = path.join(history.gateway.homeDir, 'sessions', history.sessionId);
  for (const artifact of artifacts as string[]) {
    const target = path.resolve(root, artifact);
    if (!target.startsWith(root + path.sep) || /(?:^|[/\\])(?:meta\.json|transcript[^/\\]*|archive[^/\\]*|model[^/\\]*|input_dispositions[^/\\]*)$/.test(artifact)) {
      throw new Error(`Refusing unsafe/non-derived index artifact ${artifact}`);
    }
    expect(fs.existsSync(target), 'positive removal control: actual indexed metadata must exist before removal').toBe(true);
    fs.rmSync(target, { force: false, recursive: true });
    expect(fs.existsSync(target)).toBe(false);
  }
}

/** An optional, separately supplied test-tree scenario builder must call the
 * real shared Go index/retention engine. No runtime endpoint or wire stub is
 * invented here. Its absence is explicit until GREEN supplies the seam. */
export function prepareRetentionScenario(history: HistoryHarness, survival: 'anchor-kept' | 'anchor-gone' | 'none' | 'budget-range', anchorId?: string) {
  const builder = process.env.OMNIPUS_E2E_HISTORY_SCENARIO_BUILDER;
  if (!builder || !path.isAbsolute(builder) || !fs.existsSync(builder)) {
    throw new Error('BLOCKED: real rolled-partition/shared-Go fixture builder not implemented — required by D14/D-X5/D-F1/D-F2; no fabricated expiry frames or index format allowed');
  }
  const args = ['--home', history.gateway.homeDir, '--session', history.sessionId, '--scenario', survival];
  if (anchorId) args.push('--anchor-id', anchorId);
  const stdout = execFileSync(builder, args, { encoding: 'utf8' });
  const result: unknown = JSON.parse(stdout);
  expect(result, 'scenario builder reports its actual canonical/index operation').toMatchObject({ session_id: history.sessionId, scenario: survival, canonical_rewrites: 0 });
  return result;
}
