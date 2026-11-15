// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { SEND_RETRY_FIRST_REPLY, SEND_RETRY_RESEND_REPLY, startSendRetryProvider } from './send-retry-provider';

const FIRST = 'Reply with just the single word: ready.';
const SECOND = 'second message UNIT-TRIGGER';
const start = () => startSendRetryProvider({ first: FIRST, second: SECOND });
type Provider = Awaited<ReturnType<typeof start>>;

function post(provider: Provider, messages: unknown[]) {
  return fetch(`${provider.url}/v1/chat/completions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream: true, messages }),
  });
}

// External provider SSE, not an Omnipus wire type.
function text(sse: string): string {
  expect(sse.endsWith('data: [DONE]\n\n')).toBe(true);
  return sse.split('\n').filter((line) => line.startsWith('data: ') && line !== 'data: [DONE]')
    .map((line) => JSON.parse(line.slice(6)) as { choices: Array<{ delta: { content?: string } }> })
    .map((frame) => frame.choices[0].delta.content ?? '').join('');
}

describe('send-retry scripted provider (#1792)', () => {
  it('answers the first request, then the resend, each with its own short text and no tool call', async () => {
    const provider = await start();
    try {
      const one = await post(provider, [{ role: 'user', content: FIRST }]);
      expect(one.status).toBe(200);
      expect(text(await one.text())).toBe(SEND_RETRY_FIRST_REPLY);
      const two = await post(provider, [
        { role: 'user', content: FIRST }, { role: 'assistant', content: SEND_RETRY_FIRST_REPLY },
        { role: 'user', content: SECOND },
      ]);
      expect(two.status).toBe(200);
      const raw = await two.text();
      expect(text(raw)).toBe(SEND_RETRY_RESEND_REPLY);
      expect(raw).not.toContain('tool_calls');
      expect(provider.evidence.streamRequests).toBe(2);
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });

  it('refuses a request whose last user message is not the scripted prompt', async () => {
    const provider = await start();
    try {
      const response = await post(provider, [{ role: 'user', content: 'something else' }]);
      expect(response.status).toBe(400);
      expect(provider.evidence.errors).toHaveLength(1);
    } finally { await provider.close(); }
  });

  it('refuses a resend request that does not carry the second prompt', async () => {
    const provider = await start();
    try {
      await (await post(provider, [{ role: 'user', content: FIRST }])).text();
      const two = await post(provider, [{ role: 'user', content: FIRST }, { role: 'user', content: 'not the resend' }]);
      expect(two.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 2: no user message contains its scripted prompt']);
    } finally { await provider.close(); }
  });

  it('refuses a third agent-loop request: the failed send must not reach the provider', async () => {
    const provider = await start();
    try {
      await (await post(provider, [{ role: 'user', content: FIRST }])).text();
      await (await post(provider, [{ role: 'user', content: SECOND }])).text();
      const third = await post(provider, [{ role: 'user', content: SECOND }]);
      expect(third.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['expected exactly two agent-loop requests']);
    } finally { await provider.close(); }
  });

  it('serves onboarding probes without consuming the script', async () => {
    const provider = await start();
    try {
      const probe = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', body: JSON.stringify({ stream: false, messages: [] }),
      });
      expect(probe.status).toBe(200);
      expect(provider.evidence.streamRequests).toBe(0);
    } finally { await provider.close(); }
  });
});
