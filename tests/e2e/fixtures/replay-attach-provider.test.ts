// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { E2E_FALLBACK } from './e2e-model';
import {
  REPLAY_ATTACH_CHUNKS, REPLAY_ATTACH_PREFIX, REPLAY_ATTACH_TEXT, startReplayAttachProvider,
} from './replay-attach-provider';

// R1/Scenario 9: these are controlled HTTP inputs, not captured product output.
const postStream = (url: string) => fetch(`${url}/chat/completions`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ stream: true, model: E2E_FALLBACK.model, messages: [{ role: 'user', content: 'Start the replay experiment.' }] }),
});

function deltas(text: string): string[] {
  return text.split('\n\n').filter(line => line.startsWith('data: {')).flatMap(line => {
    const frame = JSON.parse(line.slice(6)) as { choices: { delta: { content?: string } }[] };
    return frame.choices.flatMap(choice => choice.delta.content === undefined ? [] : [choice.delta.content]);
  });
}

describe('replay attach scripted provider', () => {
  it('holds an unfinished prefix, then emits every ordered delayed chunk and one finish', async () => {
    const provider = await startReplayAttachProvider();
    try {
      const response = await postStream(provider.url);
      expect(response.status).toBe(200);
      const reader = response.body!.getReader();
      const prefix = new TextDecoder().decode((await reader.read()).value);
      expect(deltas(prefix), 'the first HTTP read is only the held prefix, not an already-finished answer').toEqual([REPLAY_ATTACH_PREFIX]);
      expect(prefix).not.toContain('[DONE]');
      expect(provider.state()).toEqual({ calls: 1, emittedChunks: 1, finished: false });
      provider.releaseChunks();
      provider.finish();
      let rest = '';
      for (;;) {
        const next = await reader.read();
        if (next.done) break;
        rest += new TextDecoder().decode(next.value);
      }
      expect(deltas(rest)).toEqual(REPLAY_ATTACH_CHUNKS);
      expect(deltas(prefix + rest).join('')).toBe(REPLAY_ATTACH_TEXT);
      expect(rest.match(/"finish_reason":"stop"/g)).toHaveLength(1);
      expect(rest.match(/data: \[DONE\]/g)).toHaveLength(1);
      expect(provider.state()).toEqual({ calls: 1, emittedChunks: 1 + REPLAY_ATTACH_CHUNKS.length, finished: true });
      expect(provider.failures()).toEqual([]);
    } finally { await provider.close(); }
  });

  it('serves local model discovery and nonstream onboarding without starting a turn', async () => {
    const provider = await startReplayAttachProvider();
    try {
      const models = await fetch(`${provider.url}/models`);
      expect(models.status).toBe(200);
      expect(await models.json()).toEqual({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] });
      const probe = await fetch(`${provider.url}/chat/completions`, {
        method: 'POST', body: JSON.stringify({ stream: false }),
      });
      expect(probe.status).toBe(200);
      expect(await probe.json()).toMatchObject({ choices: [{ message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }] });
      expect(provider.state()).toEqual({ calls: 0, emittedChunks: 0, finished: false });
      expect(provider.failures()).toEqual([]);
    } finally { await provider.close(); }
  });

  it('rejects an unexpected route with a recorded diagnostic', async () => {
    const provider = await startReplayAttachProvider();
    try {
      const response = await fetch(`${provider.url}/unexpected`);
      expect(response.status).toBe(404);
      expect(await response.json()).toEqual({ error: { message: 'unexpected provider route: GET /unexpected' } });
      expect(provider.failures()).toEqual(['unexpected provider route: GET /unexpected']);
    } finally { await provider.close(); }
  });

  it('rejects malformed JSON without starting a stream', async () => {
    const provider = await startReplayAttachProvider();
    try {
      const response = await fetch(`${provider.url}/chat/completions`, { method: 'POST', body: '{' });
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({ error: { message: 'invalid provider request JSON' } });
      expect(provider.failures()).toEqual(['invalid provider request JSON']);
      expect(provider.state()).toEqual({ calls: 0, emittedChunks: 0, finished: false });
    } finally { await provider.close(); }
  });

  it('rejects a second streaming call instead of creating a duplicate answer', async () => {
    const provider = await startReplayAttachProvider();
    try {
      const first = await postStream(provider.url);
      expect(first.status).toBe(200);
      const duplicate = await postStream(provider.url);
      expect(duplicate.status).toBe(409);
      expect(await duplicate.json()).toEqual({ error: { message: 'unexpected second streaming request' } });
      expect(provider.failures()).toEqual(['unexpected second streaming request']);
      expect(provider.state().calls).toBe(1);
    } finally { await provider.close(); }
  });
});
