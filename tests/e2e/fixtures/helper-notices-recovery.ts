import { expect } from '@playwright/test';
import { expectNamed, visibleNamed, type NoticeScene } from './helper-notices-harness';

// Opening a fake HTTP answer gate does not advance the real Retry-After clock.
// Keep the real scheduled wait and producer publication separate from the
// spec's 2 s copy-arrival -> painted-row deadline.
export async function recoverHelper(scene: NoticeScene, index = 0, name = 'Researcher', displayed = true) {
  const retry = scene.traffic.frames.filter(f => f.type === 'provider_retry' &&
    f.session_id === scene.root.id && f.child_session_id === scene.children[index].id).at(-1);
  expect(retry?.retry_at, 'positive recovery instrument: target helper, not the parent retry').toEqual(expect.any(String));
  const remaining = Math.max(0, Date.parse(String(retry!.retry_at)) - Date.now());
  expect(Number.isFinite(remaining), 'real helper retry deadline is parseable').toBe(true);
  scene.provider.answer[index].resolve();
  await expect.poll(() => scene.provider.helpers[index].success, {
    message: 'real retried HTTP model request has answered, not merely an opened gate', timeout: remaining + 15_000,
  }).toBe(true);
  expect(scene.provider.evidence.requests.filter(r => r.role === `helper-${index}` && r.call > 1 && r.status === 200).length,
    'positive control: actual successful retried fake-model request').toBeGreaterThan(0);
  const recoveryIndex = () => scene.traffic.frames.findIndex(f => f.type === 'provider_status' && f.state === 'recovered' &&
    f.session_id === scene.root.id && f.child_session_id === scene.children[index].id && f.turn_id === retry!.turn_id);
  await expect.poll(recoveryIndex, { message: 'FR-002: actual recovered main-chat copy for this helper turn',
    timeout: 15_000, intervals: [10, 20, 50] }).toBeGreaterThanOrEqual(0);
  const arrived = scene.traffic.receivedAt[recoveryIndex()];
  if (displayed) await expectNamed(scene, name, 'after 1 retry', arrived);
  else {
    // FR-009: fourth recovered helper remains in the summary, not a fourth row.
    await expect.poll(() => visibleNamed(scene.page, name, 'after 1 retry'), {
      timeout: Math.max(1, arrived + 2_000 - performance.now()),
    }).toEqual([]);
  }
}
