import { expect, type TestInfo } from '@playwright/test';
import type { NoticeScene } from './helper-notices-harness';
import { expectRestoredCountdowns, type CountdownTarget } from './helper-notices-paint';
import { openSessionByDeepLink } from './session-setup';
import { waitForConnected } from './selectors';

// Existing server frames are the deadline oracle; never infer retry_at from a
// node-side "five seconds elapsed" guess or from a displayed countdown.
function targetFor(scene: NoticeScene, index: number, kind: 'own' | 'named', ordinal = 0): CountdownTarget {
  const child = scene.children[index];
  const retry = scene.traffic.frames.filter(f => f.type === 'provider_retry' &&
    f.session_id === scene.root.id && f.child_session_id === child.id).at(-1);
  expect(retry?.retry_at, 'paint oracle: the chosen real helper turn has an actual server retry_at').toEqual(expect.any(String));
  return { sessionId: kind === 'own' ? child.id : scene.root.id, kind, ordinal, retryAt: String(retry!.retry_at),
    ...(kind === 'named' ? { name: scene.agents.find(a => a.id === child.agent_id)!.name } : {}) };
}

export async function reloadCountdownPair(scene: NoticeScene, info: TestInfo, label: string,
  waitingIndices: number[] = [0], ownIndex = waitingIndices[0], mainOwn: CountdownTarget[] = []) {
  expect(waitingIndices.length, 'paint oracle: this reload really expects at least one helper countdown').toBeGreaterThan(0);
  const named = waitingIndices.map((index, position) => {
    const name = scene.agents.find(a => a.id === scene.children[index].agent_id)!.name;
    const ordinal = waitingIndices.slice(0, position).filter(i => scene.children[i].agent_id === scene.children[index].agent_id).length;
    return { ...targetFor(scene, index, 'named', ordinal), name };
  });
  const helper = targetFor(scene, ownIndex, 'own');
  if (!scene.own.url().endsWith(`/sessions/${scene.children[ownIndex].id}`)) {
    await openSessionByDeepLink(scene.own, scene.children[ownIndex].id, scene.gateway.baseURL);
  }
  const main = await scene.nativeCountdownPage();
  // T14: no routed socket in the measured path, no proxy delay subtraction.
  // Only ONE page reloads at a time; trace/video are off in these case scopes.
  // The init recorder's navigation origin/deadline and both assertions are
  // unchanged: waiting for the server is charged to the SAME strict2s budget.
  const failures: unknown[] = [];
  for (const [measuredPage, audience, targets, traffic] of [
    [main, 'main', [...mainOwn, ...named], scene.traffic],
    [scene.own, 'helper', [helper], scene.ownTraffic],
  ] as const) {
    try {
      expect(measuredPage.video(), 'T14 measured reload has no video recording').toBeNull();
      const beforeReload = traffic.frames.length;
      await measuredPage.reload(); await waitForConnected(measuredPage);
      await info.attach(`helper-notice-reload-environment-${label}-${audience}`, { body: JSON.stringify({ audience,
        serial: true, transport: 'native-no-proxy', proxyDelaySubtractedMs: 0, video: 'off',
        reloadFrames: traffic.frames.slice(beforeReload).filter(f => ['session_state', 'catch_up_complete'].includes(String(f.type))) }), contentType: 'application/json' });
      await expectRestoredCountdowns(measuredPage, info, `${label}-${audience}`, [...targets]);
      expect(traffic.frames.slice(beforeReload).some(f => f.type === 'catch_up_complete' && f.session_id === targets[0].sessionId),
        'T14 positive control: THIS native measured reload completed its actual attach').toBe(true);
    } catch (error) { failures.push(error); }
  }
  // Collect both serial branches' evidence, but never convert a failure to pass.
  for (const error of failures) throw error;
}
