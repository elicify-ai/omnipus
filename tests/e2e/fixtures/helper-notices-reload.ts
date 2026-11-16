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
  // Both documents have the init recorder BEFORE reload. Both branches finish
  // and attach their timestamps even if one fails; rethrow, never swallow it.
  const results = await Promise.allSettled([
    (async () => {
      await scene.page.reload(); await waitForConnected(scene.page);
      await expectRestoredCountdowns(scene.page, info, `${label}-main`, [...mainOwn, ...named]);
    })(),
    (async () => {
      await scene.own.reload(); await waitForConnected(scene.own);
      await expectRestoredCountdowns(scene.own, info, `${label}-helper`, [helper]);
    })(),
  ]);
  for (const result of results) if (result.status === 'rejected') throw result.reason;
}
