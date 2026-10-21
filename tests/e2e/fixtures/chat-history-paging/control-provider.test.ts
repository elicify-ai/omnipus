// Positive instrument control only, not feature-GREEN or a CHECK verdict.
// A real HTTP client's abort must be observable by the scripted MODEL edge,
// otherwise the E2E engine-Stop assertion could be a false instrument.
import { expect, it } from 'vitest';
import { CONTROL_RUNNING, startHistoryControlProvider } from './control-provider';

it('marks an actual provider response abort without classifying Stop as a fixture protocol failure', async () => {
  const provider = await startHistoryControlProvider('stream');
  const abort = new AbortController();
  try {
    const response = await fetch(`${provider.url}/chat/completions`, { method: 'POST',
      body: JSON.stringify({ stream: true, tools: [] }), signal: abort.signal });
    expect(response.status).toBe(200);
    if (!response.body) throw new Error('instrument has no real streaming HTTP body');
    const first = await response.body.getReader().read();
    expect(new TextDecoder().decode(first.value)).toContain(CONTROL_RUNNING);
    expect(provider.calls()).toBe(1);
    expect(provider.heldCount()).toBe(1);
    expect(provider.aborted()).toBe(0);
    abort.abort();
    await expect.poll(() => provider.aborted()).toBe(1);
    expect(provider.heldCount()).toBe(0);
    expect(provider.failures()).toEqual([]);
  } finally { abort.abort(); await provider.close(); }
});
