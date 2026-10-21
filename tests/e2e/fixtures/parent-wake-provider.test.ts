// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { E2E_MODEL } from './e2e-model';
import { PARENT_WAKE_HELPER, PARENT_WAKE_PARTIAL, PARENT_WAKE_TASK, startParentWakeProvider } from './parent-wake-provider';

const wakeText = `A delegated session handed back (final): ${PARENT_WAKE_HELPER}`;

// The scripted provider must distinguish a genuine matching user message,
// the helper task (which also names HELLO_HELPER), and the actual parent wake.
// A broad prompt substring would let the browser test pass without a hand-back.
describe('parent-wake browser provider', () => {
  // Characterization control for the fixture's existing HTTP error protocol;
  // not a new defect or a claim of independent RED coverage.
  it('reports an unexpected provider route instead of acknowledging it', async () => {
    const provider = await startParentWakeProvider();
    try {
      const response = await fetch(`${provider.url}/unexpected`);
      expect(response.status).toBe(404);
      expect(await response.json()).toEqual({ error: { message: 'unexpected provider route: GET /unexpected' } });
      expect(provider.failures()).toEqual(['unexpected provider route: GET /unexpected']);
    } finally {
      await provider.close();
    }
  });

  it('reports missing delegation permission instead of holding a parent request as the helper', async () => {
    const provider = await startParentWakeProvider();
    try {
      provider.releaseHelper();
      const response = await fetch(`${provider.url}/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: E2E_MODEL, stream: true, tools: [],
          messages: [{ role: 'user', content: `Delegate: ${PARENT_WAKE_TASK}` }] }),
      });
      expect(response.status).toBe(200);
      expect(await response.text()).toContain('FIXTURE_ERROR: delegate tool was not offered');
      expect(provider.failures()).toEqual(['delegate tool was not offered']);
    } finally {
      await provider.close();
    }
  });

  it('acknowledges genuine matching user text without launching a helper', async () => {
    const provider = await startParentWakeProvider();
    try {
      provider.setAgentId('parent-agent');
      const res = await fetch(`${provider.url}/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: E2E_MODEL, stream: true,
          tools: [{ type: 'function', function: { name: 'delegate' } }],
          messages: [{ role: 'user', content: wakeText }] }),
      });
      expect(res.status).toBe(200);
      const text = await res.text();
      expect(text).toContain('Recorded your matching sentence.');
      expect(text).not.toContain('tool_calls');
      expect(provider.failures()).toEqual([]);
    } finally {
      await provider.close();
    }
  });

  it('returns the real helper result before the distinct held wake stream', async () => {
    const provider = await startParentWakeProvider();
    try {
      provider.setAgentId('parent-agent');
      const post = (messages: unknown[]) => fetch(`${provider.url}/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: E2E_MODEL, stream: true,
          tools: [{ type: 'function', function: { name: 'delegate' } }], messages }),
      });
      const control = await post([{ role: 'user', content: wakeText }]);
      expect(control.status).toBe(200);
      expect(await control.text()).toContain('Recorded your matching sentence.');
      const history = [{ role: 'user', content: wakeText }, { role: 'assistant', content: 'Recorded your matching sentence.' }];
      const delegated = await post([...history, { role: 'user', content: `Delegate: ${PARENT_WAKE_TASK}` }]);
      expect(delegated.status).toBe(200);
      expect(await delegated.text()).toContain('parent-wake-delegate');
      const assistantCall = { role: 'assistant', tool_calls: [{ id: 'parent-wake-delegate', type: 'function', function: { name: 'delegate', arguments: '{}' } }] };
      const launched = await post([...history, assistantCall, { role: 'tool', tool_call_id: 'parent-wake-delegate', content: 'started' }]);
      expect(launched.status).toBe(200);
      // Release response gates before reading complete bodies: this test pins
      // routing/content, not elapsed time or the internals of Promise scheduling.
      provider.releaseHelper();
      provider.releaseWake();
      expect(await launched.text()).toContain('Launched');
      const helper = await post([{ role: 'user', content: PARENT_WAKE_TASK }]);
      expect(helper.status).toBe(200);
      const helperText = await helper.text();
      expect(helperText).toContain(PARENT_WAKE_HELPER);
      expect(helperText).not.toContain(PARENT_WAKE_PARTIAL);
      const woken = await post([...history, assistantCall, { role: 'tool', tool_call_id: 'parent-wake-delegate', content: 'started' }, { role: 'user', content: wakeText }]);
      expect(woken.status).toBe(200);
      const wakeReply = await woken.text();
      expect(wakeReply).toContain(PARENT_WAKE_PARTIAL);
      expect(wakeReply).toContain(' and the rest');
      expect(provider.failures()).toEqual([]);
    } finally {
      await provider.close();
    }
  });
});
