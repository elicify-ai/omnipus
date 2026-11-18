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

/**
 * Unit tests for the scripts branch (#1661 Bug-3-a / Bug-5 move): branch the
 * reply PER PROMPT — hold one tab's message, answer another's — with per-
 * prompt evidence records, instead of the single global held prompt. The
 * browser tests assert the same branch end-to-end; these pin the fixture's
 * own protocol (matching, exact answer text, the wire-level cancel counter,
 * later-turn-wins over sent history) in milliseconds.
 */
describe('held-stream provider scripts branch (per-prompt branch)', () => {
  const HOLD_PROMPT = 'scripts-hold-this-tab';
  const ANSWER_PROMPT = 'scripts-answer-this-tab';
  const ANSWER_TEXT = 'MOCK-REPLY-SCRIPTS: the answered tab gets exactly this text.';
  const TURN_ONE_PROMPT = 'scripts-turn-one';
  const TURN_TWO_PROMPT = 'scripts-turn-two';
  const TURN_ONE_TEXT = 'MOCK-REPLY-SCRIPTS-ONE: first turn scripted answer.';
  const TURN_TWO_TEXT = 'MOCK-REPLY-SCRIPTS-TWO: second turn scripted answer.';

  function scriptRequestBody(...userTexts: string[]): string {
    return JSON.stringify({
      model: 'fixture/model', stream: true,
      messages: userTexts.map((content) => ({ role: 'user', content })),
    });
  }

  /** Concatenate every scripted delta content from an SSE body — the exact
   * text the SPA must ultimately render, whitespace-normalized like the
   * browser-side oracle normalizes. */
  function streamedContent(sseBody: string): string {
    let text = '';
    for (const line of sseBody.split('\n')) {
      if (!line.startsWith('data: ') || line.includes('[DONE]')) continue;
      const parsed = JSON.parse(line.slice('data: '.length)) as {
        choices: { delta: { content?: string } }[];
      };
      text += parsed.choices[0].delta.content ?? '';
    }
    return text.replace(/\s+/g, ' ').trim();
  }

  function recordsOf(provider: Awaited<ReturnType<typeof startHeldStreamProvider>>, prompt: string) {
    const record = provider.evidence.promptRecords.find((entry) => entry.promptIncludes === prompt);
    expect(record, `a promptRecords entry must exist for ${prompt}`).toBeDefined();
    return record!;
  }

  it('holds two prompts concurrently and releases each without losing or mixing its numbered deltas (#1766)', async () => {
    const secondPrompt = 'scripts-second-held-chat';
    const provider = await startHeldStreamProvider({
      promptIncludes: HOLD_PROMPT,
      chunkIntervalMs: 20,
      chunkText: 'Catch-up fixture delta',
      releaseText: ' Finished.',
      scripts: [
        { promptIncludes: HOLD_PROMPT, behavior: 'hold' },
        { promptIncludes: secondPrompt, behavior: 'hold' },
      ],
    });
    try {
      const open = (prompt: string) => fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(prompt),
      });
      const first = await open(HOLD_PROMPT);
      const second = await open(secondPrompt);
      const a = recordsOf(provider, HOLD_PROMPT);
      const b = recordsOf(provider, secondPrompt);
      await expect.poll(() => Math.min(a.chunksSent, b.chunksSent)).toBeGreaterThanOrEqual(2);
      for (const record of [a, b]) {
        expect(record.requests).toBe(1);
        expect(record.held).toBe(true);
        expect(record.streamsClosed).toBe(0);
        expect(record.released).toBe(0);
      }
      expect(provider.evidence.streamRequests).toBe(2);
      expect(provider.evidence.errors).toEqual([]);
      expect(() => provider.expectedHeldAnswer()).toThrow('expectedHeldAnswer needs a prompt');

      provider.release(HOLD_PROMPT);
      expect(a.held).toBe(false);
      expect(a.released).toBe(1);
      expect(a.streamsClosed).toBe(1);
      expect(b.held, 'releasing A must not release B').toBe(true);
      expect(b.released).toBe(0);
      expect(b.streamsClosed).toBe(0);
      const expected = (count: number) => Array.from({ length: count }, (_, i) =>
        `Catch-up fixture delta (part ${i + 1})`).join('') + ' Finished.';
      expect(streamedContent(await first.text())).toBe(expected(a.chunksSent));
      expect(provider.expectedHeldAnswer(HOLD_PROMPT)).toBe(expected(a.chunksSent));

      provider.release();
      expect(streamedContent(await second.text())).toBe(expected(b.chunksSent));
      expect(provider.expectedHeldAnswer(secondPrompt)).toBe(expected(b.chunksSent));
      expect(b.held).toBe(false);
      expect(b.released).toBe(1);
      expect(b.streamsClosed).toBe(1);
      provider.release();
      expect(provider.evidence.released, 'release stays idempotent across both prompts').toBe(2);
      expect(provider.evidence.streamsClosed).toBe(2);
      expect(provider.evidence.errors).toEqual([]);
    } finally {
      await provider.close();
    }
  });

  it('holds one script while answering another with the EXACT scripted text', async () => {
    const provider = await startHeldStreamProvider({
      promptIncludes: HOLD_PROMPT,
      chunkIntervalMs: 20,
      scripts: [
        { promptIncludes: HOLD_PROMPT, behavior: 'hold' },
        { promptIncludes: ANSWER_PROMPT, behavior: 'answer', answerText: ANSWER_TEXT },
      ],
    });
    try {
      // The hold tab streams first and stays open.
      const held = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please hold: ${HOLD_PROMPT}`),
      });
      expect(held.status).toBe(200);
      const heldReader = held.body!.getReader();
      await heldReader.read();
      const holdRecord = recordsOf(provider, HOLD_PROMPT);
      expect(holdRecord.held, 'the hold script must be holding').toBe(true);
      expect(holdRecord.requests).toBe(1);

      // The answered tab, WHILE the hold is open: completes with the exact text.
      const answered = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please answer: ${ANSWER_PROMPT}`),
      });
      const sseBody = await answered.text();
      expect(answered.status).toBe(200);
      expect(sseBody).toContain('data: [DONE]');
      expect(sseBody).toContain('"finish_reason":"stop"');
      expect(streamedContent(sseBody)).toBe(ANSWER_TEXT);

      const answerRecord = recordsOf(provider, ANSWER_PROMPT);
      expect(answerRecord.answered).toBe(1);
      expect(answerRecord.requests).toBe(1);
      expect(answerRecord.held, 'an answer script never holds').toBe(false);

      // The hold is untouched by the answer, and nothing was flagged.
      expect(holdRecord.held).toBe(true);
      expect(provider.evidence.streamRequests).toBe(2);
      expect(provider.evidence.unmatchedStreams).toBe(0);
      expect(provider.evidence.errors).toHaveLength(0);
      heldReader.cancel();
    } finally {
      await provider.close();
    }
  });

  it('records a client abort on the held script as clientClosed without touching the answered script', async () => {
    const provider = await startHeldStreamProvider({
      promptIncludes: HOLD_PROMPT,
      chunkIntervalMs: 20,
      scripts: [
        { promptIncludes: HOLD_PROMPT, behavior: 'hold' },
        { promptIncludes: ANSWER_PROMPT, behavior: 'answer', answerText: ANSWER_TEXT },
      ],
    });
    try {
      const abort = new AbortController();
      const held = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please hold: ${HOLD_PROMPT}`),
        signal: abort.signal,
      });
      await held.body!.getReader().read();
      expect(recordsOf(provider, HOLD_PROMPT).held).toBe(true);

      const answered = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please answer: ${ANSWER_PROMPT}`),
      });
      await answered.text();
      expect(recordsOf(provider, ANSWER_PROMPT).answered).toBe(1);

      // The wire-level cancel: the client aborts the held stream.
      abort.abort();
      await new Promise((resolve) => setTimeout(resolve, 50));
      const holdRecord = recordsOf(provider, HOLD_PROMPT);
      expect(holdRecord.clientClosed, 'the abort must land on the held prompt’s own record').toBe(1);
      expect(holdRecord.held, 'the held prompt is no longer holding after the abort').toBe(false);
      expect(provider.evidence.released, 'no release() was involved').toBe(0);
      expect(recordsOf(provider, ANSWER_PROMPT).answered, 'the answered script is untouched by the cancel').toBe(1);
    } finally {
      await provider.close();
    }
  });

  it('answers an answer-script prompt that arrives BEFORE any hold exists', async () => {
    const provider = await startHeldStreamProvider({
      promptIncludes: HOLD_PROMPT,
      chunkIntervalMs: 20,
      scripts: [
        { promptIncludes: ANSWER_PROMPT, behavior: 'answer', answerText: ANSWER_TEXT },
        { promptIncludes: HOLD_PROMPT, behavior: 'hold' },
      ],
    });
    try {
      // Answered first — no hold slot is open yet; must still complete.
      const answered = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please answer: ${ANSWER_PROMPT}`),
      });
      expect(streamedContent(await answered.text())).toBe(ANSWER_TEXT);
      expect(recordsOf(provider, ANSWER_PROMPT).answered).toBe(1);
      expect(provider.evidence.retried).toBe(0);

      // Only then is the hold prompt seen and held.
      const held = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(`please hold: ${HOLD_PROMPT}`),
      });
      const heldReader = held.body!.getReader();
      await heldReader.read();
      expect(recordsOf(provider, HOLD_PROMPT).held).toBe(true);
      heldReader.cancel();
    } finally {
      await provider.close();
    }
  });

  it('matches a later turn’s own script, not an earlier turn’s marker still in the sent history', async () => {
    const provider = await startHeldStreamProvider({
      promptIncludes: TURN_ONE_PROMPT,
      chunkIntervalMs: 20,
      scripts: [
        { promptIncludes: TURN_ONE_PROMPT, behavior: 'answer', answerText: TURN_ONE_TEXT },
        { promptIncludes: TURN_TWO_PROMPT, behavior: 'answer', answerText: TURN_TWO_TEXT },
      ],
    });
    try {
      // Turn 2's request carries the whole conversation: turn 1's user message
      // (with its marker) is still in the history. LAST matching user message wins.
      const turnTwo = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: scriptRequestBody(
          `first message ${TURN_ONE_PROMPT}`,
          'assistant placeholder is not a user message',
          `second message ${TURN_TWO_PROMPT}`,
        ),
      });
      expect(streamedContent(await turnTwo.text())).toBe(TURN_TWO_TEXT);
      expect(recordsOf(provider, TURN_TWO_PROMPT).answered).toBe(1);
      expect(recordsOf(provider, TURN_ONE_PROMPT).answered, 'turn 1’s script must not answer turn 2’s request').toBe(0);
      expect(recordsOf(provider, TURN_ONE_PROMPT).requests).toBe(0);
    } finally {
      await provider.close();
    }
  });
});
