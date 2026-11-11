// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { E2E_FALLBACK } from './e2e-model';
import { MEDIA_DOWNLOAD_CALL_ID, startMediaDownloadProvider } from './media-download-provider';

const RELATIVE_PATH = 'e2e-media-download/unit-report.txt';
const PROMPT = `Call send_file exactly once for ${RELATIVE_PATH}, then reply SENT.`;
const USER = { role: 'user', content: PROMPT };
const CALL = { role: 'assistant', tool_calls: [{ id: 'media-download-send-file', type: 'function',
  function: { name: 'send_file', arguments: JSON.stringify({ path: RELATIVE_PATH }) } }] };
const RESULT = { role: 'tool', tool_call_id: 'media-download-send-file', content: 'the real tool result' };
const start = () => startMediaDownloadProvider({ prompt: PROMPT, relativePath: RELATIVE_PATH });
type Provider = Awaited<ReturnType<typeof start>>;

function post(provider: Provider, messages: unknown[], tools = [{ function: { name: 'send_file' } }]) {
  return fetch(`${provider.url}/v1/chat/completions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream: true, messages, tools }),
  });
}

// External provider SSE, not an Omnipus wire type. Inspect every delta, so an
// extra tool call or an early SENT cannot hide inside an otherwise valid stream.
function frames(sse: string): Array<{ choices: Array<{ delta: unknown; finish_reason: string | null }> }> {
  expect(sse.endsWith('data: [DONE]\n\n')).toBe(true);
  return sse.split('\n').filter((line) => line.startsWith('data: ') && line !== 'data: [DONE]')
    .map((line) => JSON.parse(line.slice(6)));
}

async function expectRejected(response: Response, provider: Provider, message: string) {
  expect(response.status).toBe(400);
  expect(await response.json()).toEqual({ error: { message } });
  expect(provider.evidence.errors).toEqual([message]);
  expect(provider.evidence.toolResults).toEqual([]);
}

describe('media download scripted provider (#1764)', () => {
  it('serves onboarding probes without consuming the two-request script', async () => {
    const provider = await start();
    try {
      const models = await fetch(`${provider.url}/v1/models`);
      expect(models.status).toBe(200);
      expect(await models.json()).toEqual({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] });
      const probe = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', body: JSON.stringify({ stream: false, messages: [] }),
      });
      expect(probe.status).toBe(200);
      expect(await probe.json()).toEqual({
        id: 'media-download-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
        choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
        usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
      });
      expect(provider.evidence).toEqual({ streamRequests: 0, issuedCalls: 0, toolResults: [], errors: [] });
    } finally { await provider.close(); }
  });

  for (const parts of [false, true]) {
    it(`issues exactly one send_file and says SENT only after its tool result (content parts=${parts})`, async () => {
      const provider = await start();
      try {
        const user = { role: 'user', content: parts ? [{ type: 'text', text: PROMPT }] : PROMPT };
        const first = await post(provider, [user]);
        expect(first.status).toBe(200);
        const firstFrames = frames(await first.text());
        expect(firstFrames.map((frame) => frame.choices)).toEqual([
          [{ index: 0, delta: { role: 'assistant', tool_calls: [{ index: 0, id: 'media-download-send-file', type: 'function',
            function: { name: 'send_file', arguments: JSON.stringify({ path: RELATIVE_PATH }) } }] }, finish_reason: null }],
          [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
        ]);
        expect(MEDIA_DOWNLOAD_CALL_ID).toBe('media-download-send-file');
        expect(provider.evidence).toEqual({ streamRequests: 1, issuedCalls: 1, toolResults: [], errors: [] });
        const result = { ...RESULT, content: parts ? [{ type: 'text', text: RESULT.content }] : RESULT.content };
        const second = await post(provider, [user, CALL, result]);
        expect(second.status).toBe(200);
        expect(frames(await second.text()).map((frame) => frame.choices)).toEqual([
          [{ index: 0, delta: { role: 'assistant', content: 'SENT' }, finish_reason: 'stop' }],
        ]);
        expect(provider.evidence).toEqual({ streamRequests: 2, issuedCalls: 1,
          toolResults: [{ request: 2, callId: 'media-download-send-file', name: 'send_file', content: RESULT.content }], errors: [] });
      } finally { await provider.close(); }
    });
  }

  it('rejects malformed JSON instead of returning a completion', async () => {
    const provider = await start();
    try {
      const response = await fetch(`${provider.url}/v1/chat/completions`, { method: 'POST', body: '{' });
      await expectRejected(response, provider, 'invalid provider JSON');
      expect(provider.evidence.issuedCalls).toBe(0);
    } finally { await provider.close(); }
  });

  it('rejects an unexpected route', async () => {
    const provider = await start();
    try {
      const response = await fetch(`${provider.url}/wrong-route`);
      expect(response.status).toBe(404);
      expect(await response.json()).toEqual({ error: { message: 'unexpected provider route: GET /wrong-route' } });
      expect(provider.evidence.errors).toEqual(['unexpected provider route: GET /wrong-route']);
    } finally { await provider.close(); }
  });

  for (const misuse of [
    { name: 'wrong prompt', messages: [{ role: 'user', content: 'another task' }], error: 'expected the media download user prompt' },
    { name: 'prompt present only in system text', messages: [{ role: 'system', content: PROMPT }], error: 'expected the media download user prompt' },
    { name: 'shared tool history', messages: [USER, CALL, RESULT], error: 'expected a fresh chat without tool history' },
  ]) {
    it(`rejects ${misuse.name} before issuing send_file`, async () => {
      const provider = await start();
      try {
        await expectRejected(await post(provider, misuse.messages), provider, misuse.error);
        expect(provider.evidence.issuedCalls).toBe(0);
      } finally { await provider.close(); }
    });
  }

  it('rejects a request that does not offer the real send_file tool', async () => {
    const provider = await start();
    try {
      await expectRejected(await post(provider, [USER], []), provider, 'send_file was not offered to the agent');
      expect(provider.evidence.issuedCalls).toBe(0);
    } finally { await provider.close(); }
  });

  for (const misuse of [
    { name: 'missing result', messages: [USER, CALL] },
    { name: 'mismatched result id', messages: [USER, CALL, { ...RESULT, tool_call_id: 'wrong-id' }] },
    { name: 'empty result', messages: [USER, CALL, { ...RESULT, content: '' }] },
    { name: 'result before its call', messages: [USER, RESULT, CALL] },
    { name: 'duplicate results', messages: [USER, CALL, RESULT, RESULT] },
  ]) {
    it(`rejects ${misuse.name} rather than manufacturing SENT`, async () => {
      const provider = await start();
      try {
        await (await post(provider, [USER])).text();
        await expectRejected(await post(provider, misuse.messages), provider, 'expected the correlated send_file tool result after its call');
        expect(provider.evidence.issuedCalls).toBe(1);
      } finally { await provider.close(); }
    });
  }

  it('rejects altered send_file arguments on the follow-up request', async () => {
    const provider = await start();
    try {
      await (await post(provider, [USER])).text();
      const wrongCall = { ...CALL, tool_calls: [{ ...CALL.tool_calls[0], function: { name: 'send_file', arguments: '{"path":"wrong.txt"}' } }] };
      await expectRejected(await post(provider, [USER, wrongCall, RESULT]), provider, 'expected the original send_file call and path');
    } finally { await provider.close(); }
  });

  it('rejects a third request without issuing another send_file', async () => {
    const provider = await start();
    try {
      await (await post(provider, [USER])).text();
      await (await post(provider, [USER, CALL, RESULT])).text();
      const response = await post(provider, [USER, CALL, RESULT]);
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({ error: { message: 'expected exactly two agent-loop requests' } });
      expect(provider.evidence.errors).toEqual(['expected exactly two agent-loop requests']);
      expect(provider.evidence.streamRequests).toBe(3);
      expect(provider.evidence.issuedCalls).toBe(1);
      expect(provider.evidence.toolResults).toHaveLength(1);
    } finally { await provider.close(); }
  });
});
