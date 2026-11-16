import { expect } from '@playwright/test';
import { expectNamed, visibleNamed, type NoticeScene } from './helper-notices-harness';

// Corrected restart BDD (f900e29a72): stale rows must disappear, then an actual
// new Writer request earns a new-turn silence notice. No synthetic boot/frame.
export async function expectRestartedWriter(scene: NoticeScene, restartAt: number, callsBefore: number, oldTurn: unknown) {
  expect(oldTurn, 'positive control: old Writer had a real announced turn before restart').toEqual(expect.any(String));
  expect(await visibleNamed(scene.page, 'Writer', '.*'), 'restart BDD: no stale Writer row immediately after catch-up').toEqual([]);
  const resumed = () => scene.provider.evidence.requests.find(r => r.role === 'helper-1' && r.call > callsBefore && r.at >= restartAt);
  await expect.poll(resumed, { message: 'positive control: resumed Writer reaches the real fake-model HTTP edge', timeout: 32_000 }).toBeDefined();
  const request = resumed()!;
  expect(request.status, 'chosen silence script has not answered the resumed call').toBeUndefined();
  // Script holds the resumed HTTP call silent; the specified next notice is
  // therefore silence at30s. The full HTTP-start→painted-row bound is30+2s.
  const deadline = request.at + 32_000;
  const noticeIndex = () => scene.traffic.frames.findIndex(f => f.type === 'provider_status' && f.state === 'waiting' &&
    f.child_session_id === scene.children[1].id && f.turn_id !== oldTurn && Date.parse(String(f.silent_since)) >= restartAt);
  await expect.poll(noticeIndex, { message: 'restart BDD: resumed Writer earns its own new-turn silence notice within30s+2s',
    timeout: Math.max(1, deadline - Date.now()), intervals: [10, 20, 50] }).toBeGreaterThanOrEqual(0);
  const notice = scene.traffic.frames[noticeIndex()];
  expect(notice.turn_id, 'restart BDD: returned notice belongs to the resumed turn, never the interrupted one').not.toBe(oldTurn);
  await expectNamed(scene, 'Writer', "hasn't responded", scene.traffic.receivedAt[noticeIndex()]);
  expect(Date.now(), 'restart BDD: resumed HTTP call to painted row stays within the total30s+2s budget').toBeLessThanOrEqual(deadline);
  expect(await visibleNamed(scene.page, 'Researcher', '.*'), 'restart BDD: ended Researcher never returns').toEqual([]);
}
