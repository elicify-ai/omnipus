// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { startHeldStreamProvider } from './held-stream-provider';

const PROMPT = 'the scripted long-prose prompt for unit tests';

function streamRequestBody(): string {
  return JSON.stringify({
    model: 'fixture/model', stream: true,
    messages: [{ role: 'user', content: `Please answer at length: ${PROMPT}` }],
  });
}

/**
 * Unit tests for the #1661 held-stream provider fixture: hold-open on the
 * first matching streaming request, release() finishes it, a cancel (client
 * abort) closes it without a release, later matching requests complete, and
 * non-matching traffic is answered but flagged. The browser tests assert the
 * same evidence end-to-end; these pin the fixture's own protocol so a broken
 * fixture fails here first, in milliseconds, not in a 7-minute shard.
 */
describe('held-stream provider fixture', () => {
  it('serves the model list and the non-stream validation probe without holding', async () => {
    const provider = await startHeldStreamProvider({ promptIncludes: PROMPT, chunkIntervalMs: 20 });
    try {
      const models = await fetch(`${provider.url}/v1/models`);
      expect(models.status).toBe(200);
      expect(await models.json()).toMatchObject({ object: 'list' });

      const probe = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: 'fixture/model', stream: false, messages: [] }),
      });
      expect(probe.status).toBe(200);
      expect(await probe.json()).toMatchObject({ choices: [{ message: { content: 'probe ok' } }] });
      expect(provider.evidence.streamRequests).toBe(0);
    } finally {
      await provider.close();
    }
  });

  it('holds the first matching stream open with deltas and never finishes on its own', async () => {
    const provider = await startHeldStreamProvider({ promptIncludes: PROMPT, chunkIntervalMs: 20 });
    try {
      const res = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: streamRequestBody(),
      });
      expect(res.status).toBe(200);

      const reader = res.body!.getReader();
      const first = await reader.read();
      expect(first.done).toBe(false);
      const heldOpenBefore = provider.evidence.chunksSent;
      expect(heldOpenBefore).toBeGreaterThanOrEqual(1);
      expect(provider.evidence.streamRequests).toBe(1);
      expect(provider.evidence.streamsClosed).toBe(0);
      expect(provider.evidence.released).toBe(0);

      // Deltas keep flowing; still no finish and no [DONE] anywhere so far.
      await new Promise((resolve) => setTimeout(resolve, 120));
      expect(provider.evidence.chunksSent).toBeGreaterThan(heldOpenBefore);
      expect(provider.evidence.streamsClosed).toBe(0);
      reader.cancel();
    } finally {
      await provider.close();
    }
  });

  it('release() finishes the held stream and later matching requests complete instead of holding', async () => {
    const provider = await startHeldStreamProvider({ promptIncludes: PROMPT, chunkIntervalMs: 20 });
    try {
      const held = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: streamRequestBody(),
      });
      await held.body!.getReader().read();

      provider.release();
      expect(provider.evidence.released).toBe(1);
      expect(provider.evidence.streamsClosed).toBe(1);

      const retry = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: streamRequestBody(),
      });
      const retryBody = await retry.text();
      expect(retryBody).toContain('data: [DONE]');
      expect(retryBody).toContain('"finish_reason":"stop"');
      expect(provider.evidence.streamRequests).toBe(2);
      expect(provider.evidence.retried).toBe(1);
      expect(provider.evidence.released).toBe(1);

      // Idempotent: releasing again is a no-op, not a second finish.
      provider.release();
      expect(provider.evidence.released).toBe(1);
    } finally {
      await provider.close();
    }
  });

  it('a client abort (the real cancel path) closes the held stream without a release', async () => {
    const provider = await startHeldStreamProvider({ promptIncludes: PROMPT, chunkIntervalMs: 20 });
    try {
      const abort = new AbortController();
      const held = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: streamRequestBody(),
        signal: abort.signal,
      });
      await held.body!.getReader().read();
      expect(provider.evidence.streamsClosed).toBe(0);

      abort.abort();
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(provider.evidence.streamsClosed, 'the abort must tear the held stream down').toBe(1);
      expect(provider.evidence.released, 'no release() was involved').toBe(0);
    } finally {
      await provider.close();
    }
  });

  it('answers an unmatched streaming prompt completely and records it as fixture misuse', async () => {
    const provider = await startHeldStreamProvider({ promptIncludes: PROMPT, chunkIntervalMs: 20 });
    try {
      const res = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model: 'fixture/model', stream: true,
          messages: [{ role: 'user', content: 'an entirely different prompt' }],
        }),
      });
      const body = await res.text();
      expect(res.status).toBe(200);
      expect(body).toContain('FIXTURE_ERROR: unexpected streaming prompt');
      expect(body).toContain('data: [DONE]');
      expect(provider.evidence.unmatchedStreams).toBe(1);
      expect(provider.evidence.errors).toHaveLength(1);
      expect(provider.evidence.streamRequests).toBe(1);
    } finally {
      await provider.close();
    }
  });
});
