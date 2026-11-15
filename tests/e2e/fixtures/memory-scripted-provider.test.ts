// @vitest-environment node
import { describe, expect, it } from 'vitest';
import {
  MEMORY_SCRIPT_IDS, REMEMBER_SHARED_SUCCESS, startMemoryScriptedProvider,
} from './memory-scripted-provider';

const NONCE = 'MEMSCRIPT-abc123';
const QUERY = 'abc123-MEMSCRIPT';
const CONTROL = 'UNSTORED-never9';
const P1 = 'store the nonce please';
const P2 = 'recall the nonce please';
const P3 = 'recall the control please';
const start = () => startMemoryScriptedProvider({
  nonce: NONCE, recallQuery: QUERY, controlNonce: CONTROL, rememberPrompt: P1, recallPrompt: P2, controlPrompt: P3,
});
type Provider = Awaited<ReturnType<typeof start>>;
type Msg = Record<string, unknown>;

const TOOLS = [{ function: { name: 'remember' } }, { function: { name: 'recall_memory' } }];
const REMEMBER_ARGS = { content: NONCE, category: 'reference', room: 'shared' };

function post(provider: Provider, messages: Msg[], tools: unknown[] = TOOLS) {
  return fetch(`${provider.url}/v1/chat/completions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream: true, messages, tools }),
  });
}

// External provider SSE, not an Omnipus wire type.
interface Frame { choices: Array<{ delta: { content?: string; tool_calls?: Array<{ id: string; function: { name: string; arguments: string } }> } }> }
function frames(sse: string): Frame[] {
  expect(sse.endsWith('data: [DONE]\n\n')).toBe(true);
  return sse.split('\n').filter((line) => line.startsWith('data: ') && line !== 'data: [DONE]')
    .map((line) => JSON.parse(line.slice(6)) as Frame);
}
const textOf = (sse: string) => frames(sse).map((f) => f.choices[0].delta.content ?? '').join('');
const callOf = (sse: string) => frames(sse).flatMap((f) => f.choices[0].delta.tool_calls ?? [])[0];

const toolCall = (id: string, name: string, args: object): Msg => ({
  role: 'assistant', content: null, tool_calls: [{ id, type: 'function', function: { name, arguments: JSON.stringify(args) } }],
});
const toolResult = (id: string, content: string): Msg => ({ role: 'tool', tool_call_id: id, content });

/** Turn 1 (remember). Returns the history up to and including the STORED-OK reply. */
async function turnOne(provider: Provider): Promise<Msg[]> {
  const r1 = await post(provider, [{ role: 'user', content: P1 }]);
  expect(r1.status).toBe(200);
  const call = callOf(await r1.text());
  expect(call.function.name).toBe('remember');
  expect(JSON.parse(call.function.arguments)).toEqual(REMEMBER_ARGS);
  const history: Msg[] = [
    { role: 'user', content: P1 }, toolCall(call.id, 'remember', REMEMBER_ARGS),
    toolResult(call.id, REMEMBER_SHARED_SUCCESS),
  ];
  const r2 = await post(provider, history);
  expect(r2.status).toBe(200);
  expect(textOf(await r2.text())).toBe('STORED-OK');
  return [...history, { role: 'assistant', content: 'STORED-OK' }];
}

describe('memory scripted provider', () => {
  it('walks remember, recall (echoing the nonce found in the tool result), then the never-stored control', async () => {
    const provider = await start();
    try {
      const h1 = await turnOne(provider);
      const h2 = [...h1, { role: 'user', content: P2 }];
      const r3 = await post(provider, h2);
      const recall = callOf(await r3.text());
      expect(recall.function.name).toBe('recall_memory');
      expect(JSON.parse(recall.function.arguments)).toEqual({ query: QUERY });
      const h3 = [...h2, toolCall(recall.id, 'recall_memory', { query: QUERY }),
        toolResult(recall.id, `[id:X | t | reference]\n${NONCE}`)];
      const r4 = await post(provider, h3);
      expect(textOf(await r4.text())).toBe(`RECALLED:${NONCE}`);
      const h4 = [...h3, { role: 'assistant', content: `RECALLED:${NONCE}` }, { role: 'user', content: P3 }];
      const r5 = await post(provider, h4);
      const control = callOf(await r5.text());
      expect(JSON.parse(control.function.arguments)).toEqual({ query: CONTROL });
      const r6 = await post(provider, [...h4, toolCall(control.id, 'recall_memory', { query: CONTROL }),
        toolResult(control.id, 'no matching entries')]);
      expect(textOf(await r6.text())).toBe('NOTHING-FOUND');
      expect(provider.evidence.streamRequests).toBe(6);
      expect(provider.evidence.toolResults.map((r) => r.name)).toEqual(['remember', 'recall_memory', 'recall_memory']);
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });

  it('says NOTHING-FOUND when the recall result lacks the nonce, so an empty recall cannot pass', async () => {
    const provider = await start();
    try {
      const h2 = [...await turnOne(provider), { role: 'user', content: P2 }];
      const recall = callOf(await (await post(provider, h2)).text());
      const r4 = await post(provider, [...h2, toolCall(recall.id, 'recall_memory', { query: QUERY }),
        toolResult(recall.id, 'no matching entries')]);
      expect(textOf(await r4.text())).toBe('NOTHING-FOUND');
    } finally { await provider.close(); }
  });

  it.each([
    'write failed',
    'permission denied',
    'nothing was saved',
    'false',
    'ok — saved as a PRIVATE note: this turn has no workspace, so there is no team room to share into',
    'ok — saved as a private note',
  ])('refuses a remember result that is not the shared-room success: %s', async (result) => {
    const provider = await start();
    try {
      const r1 = await post(provider, [{ role: 'user', content: P1 }]);
      const call = callOf(await r1.text());
      const r2 = await post(provider, [{ role: 'user', content: P1 },
        toolCall(call.id, 'remember', REMEMBER_ARGS), toolResult(call.id, result)]);
      expect(r2.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 2: the remember tool result is not a success']);
    } finally { await provider.close(); }
  });

  it('refuses a remember result whose tool_call_id does not match the call', async () => {
    const provider = await start();
    try {
      const call = callOf(await (await post(provider, [{ role: 'user', content: P1 }])).text());
      const r2 = await post(provider, [{ role: 'user', content: P1 },
        toolCall(call.id, 'remember', REMEMBER_ARGS), toolResult('some-other-id', REMEMBER_SHARED_SUCCESS)]);
      expect(r2.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 2: expected the correlated remember tool result after its call']);
    } finally { await provider.close(); }
  });

  it.each([
    ['function name', 'recall_memory', REMEMBER_ARGS],
    ['arguments', 'remember', { ...REMEMBER_ARGS, content: 'other' }],
  ])('refuses a result correlated to a call with a changed %s (fresh provider, request 2)', async (_what, name, args) => {
    const provider = await start();
    try {
      const call = callOf(await (await post(provider, [{ role: 'user', content: P1 }])).text());
      const r2 = await post(provider, [{ role: 'user', content: P1 },
        toolCall(call.id, name, args), toolResult(call.id, REMEMBER_SHARED_SUCCESS)]);
      expect(r2.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 2: expected the correlated remember tool result after its call']);
    } finally { await provider.close(); }
  });

  it('accepts the same arguments in a different key order (the gateway re-serializes them)', async () => {
    const provider = await start();
    try {
      const call = callOf(await (await post(provider, [{ role: 'user', content: P1 }])).text());
      const reordered = { room: 'shared', category: 'reference', content: NONCE };
      const r2 = await post(provider, [{ role: 'user', content: P1 },
        toolCall(call.id, 'remember', reordered), toolResult(call.id, REMEMBER_SHARED_SUCCESS)]);
      expect(r2.status).toBe(200);
    } finally { await provider.close(); }
  });

  it('refuses a recall result that is missing', async () => {
    const provider = await start();
    try {
      const h2 = [...await turnOne(provider), { role: 'user', content: P2 }];
      const recall = callOf(await (await post(provider, h2)).text());
      const r4 = await post(provider, [...h2, toolCall(recall.id, 'recall_memory', { query: QUERY })]);
      expect(r4.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 4: expected the correlated recall_memory tool result after its call']);
    } finally { await provider.close(); }
  });

  it('refuses any call-producing request that does not offer both memory tools', async () => {
    const provider = await start();
    try {
      const r1 = await post(provider, [{ role: 'user', content: P1 }], [{ function: { name: 'remember' } }]);
      expect(r1.status).toBe(400);
      expect(provider.evidence.errors).toEqual(['request 1: remember and recall_memory must both be offered']);
      const provider2 = await start();
      try {
        const h2 = [...await turnOne(provider2), { role: 'user', content: P2 }];
        const r3 = await post(provider2, h2, [{ function: { name: 'remember' } }]);
        expect(r3.status).toBe(400);
        expect(provider2.evidence.errors).toEqual(['request 3: remember and recall_memory must both be offered']);
      } finally { await provider2.close(); }
    } finally { await provider.close(); }
  });

  it('refuses a request whose prompt is missing, or whose newest scripted prompt is an older turn\'s', async () => {
    const provider = await start();
    try {
      const bad = await post(provider, [{ role: 'user', content: 'other' }]);
      expect(bad.status).toBe(400);
      expect(provider.evidence.errors[0]).toBe('request 1: no user message contains its scripted prompt');
      const stale = await start();
      try {
        const h1 = await turnOne(stale);
        // Turn 2's prompt is present, but a later scripted prompt (turn 3's) is the newest: stale for request 3.
        const r3 = await post(stale, [...h1, { role: 'user', content: P2 }, { role: 'user', content: P3 }]);
        expect(r3.status).toBe(400);
        expect(stale.evidence.errors).toEqual(["request 3: the newest scripted user prompt is not this turn's"]);
      } finally { await stale.close(); }
    } finally { await provider.close(); }
  });

  it('does not consume the script on onboarding probes and exposes stable call ids', async () => {
    const provider = await start();
    try {
      const probe = await fetch(`${provider.url}/v1/chat/completions`, {
        method: 'POST', body: JSON.stringify({ stream: false, messages: [] }),
      });
      expect(probe.status).toBe(200);
      expect(provider.evidence.streamRequests).toBe(0);
      expect(new Set(Object.values(MEMORY_SCRIPT_IDS)).size).toBe(3);
    } finally { await provider.close(); }
  });
});
