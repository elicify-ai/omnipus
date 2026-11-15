// @vitest-environment node
import { request as httpRequest } from 'node:http';
import { describe, expect, it } from 'vitest';
import { startMemoryRoundTripProvider } from './memory-round-trip-provider';

// Expectations are the dispatch's script, not values read from the fixture.
const NONCE = 'memoryroundtrip0123456789abcdef';
const MISSING = 'unrecordedfedcba9876543210';
// Founder-adopted #1800 contract; independent of the fixture's implementation.
const SHARED_SUCCESS = "ok — saved to your workspace team's shared memory";
const PROMPTS = ['store this test memory', 'recall the stored test memory', 'recall the absent test memory'] as const;
const TOOLS = [{ function: { name: 'remember' } }, { function: { name: 'recall_memory' } }];
const start = () => startMemoryRoundTripProvider({ nonce: NONCE, missingNonce: MISSING, prompts: PROMPTS });
const INDEX_NONCE = 'MEMSCRIPT-abc123';
const INDEX_QUERY = 'abc123-MEMSCRIPT';
const startIndexed = () => startMemoryRoundTripProvider({ nonce: INDEX_NONCE, recallQuery: INDEX_QUERY, missingNonce: MISSING, prompts: PROMPTS });
type Provider = Awaited<ReturnType<typeof start>>;

// Native fetch leaves its client's idle timers alive after the test server
// closes. Use one real, fully consumed HTTP request per call, without a pool.
function providerFetch(url: string, init: { method?: string; headers?: Record<string, string>; body?: string } = {}): Promise<Response> {
  return new Promise((resolve, reject) => {
    const request = httpRequest(url, { method: init.method ?? 'GET', headers: init.headers, agent: false }, (response) => {
      const chunks: Buffer[] = [];
      response.on('data', (chunk: Buffer) => chunks.push(chunk));
      response.on('error', reject);
      response.on('end', () => resolve(new Response(Buffer.concat(chunks).toString('utf8'), { status: response.statusCode })));
    });
    request.on('error', reject);
    request.end(init.body);
  });
}

function post(provider: Provider, messages: unknown[], tools = TOOLS) {
  return providerFetch(`${provider.url}/v1/chat/completions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream: true, messages, tools }),
  });
}

function call(name: string, id: string, args: Record<string, string>) {
  return { role: 'assistant', tool_calls: [{ id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] };
}
const REMEMBER = call('remember', 'memory-scripted-remember', { content: NONCE, category: 'reference', room: 'shared' });
const RECALL = call('recall_memory', 'memory-scripted-recall', { query: NONCE });
const ABSENT = call('recall_memory', 'memory-scripted-missing', { query: MISSING, room: 'shared' });
const result = (id: string, content: unknown) => ({ role: 'tool', tool_call_id: id, content });
const user = (turn: number): { role: string; content: string } => ({ role: 'user', content: PROMPTS[turn] });

// External model SSE, not a gateway/SPA wire type. Check every delta and stop
// reason so an extra tool call or premature acknowledgement cannot hide.
function choices(sse: string): unknown[] {
  expect(sse.endsWith('data: [DONE]\n\n'), 'every scripted response must terminate its stream').toBe(true);
  return sse.split('\n').filter((line) => line.startsWith('data: ') && line !== 'data: [DONE]')
    .map((line) => (JSON.parse(line.slice(6)) as { choices: unknown[] }).choices);
}
const toolChoices = (name: string, id: string, args: Record<string, string>) => [
  [{ index: 0, delta: { role: 'assistant', tool_calls: [{ index: 0, id, type: 'function',
    function: { name, arguments: JSON.stringify(args) } }] }, finish_reason: null }],
  [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
];
const textChoices = (content: string) => [[{ index: 0, delta: { role: 'assistant', content }, finish_reason: 'stop' }]];

async function firstTurn(provider: Provider) {
  expect((await post(provider, [user(0)])).status).toBe(200);
  const history = [user(0), REMEMBER, result('memory-scripted-remember', SHARED_SUCCESS)];
  expect((await post(provider, history)).status).toBe(200);
  return [...history, { role: 'assistant', content: 'Memory stored.' }];
}

async function rejected(response: Response, message: string) {
  expect(response.status, message).toBe(400);
  expect(await response.json(), message).toEqual({ error: { message } });
}

describe('scripted memory round-trip provider (#1661)', () => {
  it('serves model and onboarding probes without consuming agent-loop requests', async () => {
    const provider = await start();
    try {
      const models = await providerFetch(`${provider.url}/v1/models`);
      expect(models.status).toBe(200);
      expect(await models.json()).toMatchObject({ object: 'list', data: [{ object: 'model' }] });
      const probe = await providerFetch(`${provider.url}/v1/chat/completions`, { method: 'POST', body: '{"stream":false}' });
      expect(probe.status).toBe(200);
      expect(await probe.json()).toMatchObject({ choices: [{ message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }] });
      expect(provider.evidence).toEqual({ streamRequests: 0, probeRequests: 2, issuedCalls: 0, toolResults: [], errors: [] });
    } finally { await provider.close(); }
  });

  it('scripts remember, recall and the missing-memory control in exactly six requests', async () => {
    const provider = await start();
    try {
      const first = await post(provider, [user(0)]);
      expect(first.status).toBe(200);
      expect(choices(await first.text())).toEqual(toolChoices('remember', 'memory-scripted-remember',
        { content: NONCE, category: 'reference', room: 'shared' }));
      expect(provider.evidence).toEqual({ streamRequests: 1, probeRequests: 0, issuedCalls: 1, toolResults: [], errors: [] });
      const history = [user(0), REMEMBER, result('memory-scripted-remember', SHARED_SUCCESS)];
      const second = await post(provider, history);
      expect(second.status).toBe(200);
      expect(choices(await second.text())).toEqual(textChoices('Memory stored.'));
      history.push({ role: 'assistant', content: 'Memory stored.' }, user(1));
      const third = await post(provider, history);
      expect(third.status).toBe(200);
      expect(choices(await third.text())).toEqual(toolChoices('recall_memory', 'memory-scripted-recall', { query: NONCE }));
      const recalled = `found on disk\n${NONCE}\nend of memory`;
      history.push(RECALL, result('memory-scripted-recall', recalled));
      const fourth = await post(provider, history);
      expect(fourth.status).toBe(200);
      const echo = `Memory recall result:\n\n\`\`\`text\n${recalled}\n\`\`\``;
      expect(choices(await fourth.text())).toEqual(textChoices(echo));
      history.push({ role: 'assistant', content: echo }, user(2));
      const fifth = await post(provider, history);
      expect(fifth.status).toBe(200);
      expect(choices(await fifth.text())).toEqual(toolChoices('recall_memory', 'memory-scripted-missing', { query: MISSING, room: 'shared' }));
      history.push(ABSENT, result('memory-scripted-missing', 'no matching entries'));
      const sixth = await post(provider, history);
      expect(sixth.status).toBe(200);
      expect(choices(await sixth.text())).toEqual(textChoices('Memory recall result:\n\n```text\nno matching entries\n```'));
      expect(provider.evidence).toEqual({ streamRequests: 6, probeRequests: 0, issuedCalls: 3, errors: [], toolResults: [
        { request: 2, callId: 'memory-scripted-remember', name: 'remember', content: SHARED_SUCCESS },
        { request: 3, callId: 'memory-scripted-remember', name: 'remember', content: SHARED_SUCCESS },
        { request: 4, callId: 'memory-scripted-remember', name: 'remember', content: SHARED_SUCCESS },
        { request: 4, callId: 'memory-scripted-recall', name: 'recall_memory', content: recalled },
        { request: 5, callId: 'memory-scripted-remember', name: 'remember', content: SHARED_SUCCESS },
        { request: 5, callId: 'memory-scripted-recall', name: 'recall_memory', content: recalled },
        { request: 6, callId: 'memory-scripted-remember', name: 'remember', content: SHARED_SUCCESS },
        { request: 6, callId: 'memory-scripted-recall', name: 'recall_memory', content: recalled },
        { request: 6, callId: 'memory-scripted-missing', name: 'recall_memory', content: 'no matching entries' },
      ] });
    } finally { await provider.close(); }
  });

  for (const position of ['before', 'after'] as const) {
    it(`finds the scripted turn among user-role clock notes (${position} the prompt)`, async () => {
      const provider = await start();
      // The request-only clock is user-role at the external provider edge
      // (prompt_cache_tail.go::consolidateVolatileNotes), not a new UI turn.
      const clock = { role: 'user', content: '## Current Time\n2026-11-15 12:00 UTC (+00:00) Sunday\nUTC: 2026-11-15 12:00:00Z' };
      const withClock = (messages: unknown[]) => position === 'before' ? [clock, ...messages] : [...messages, clock];
      try {
        expect((await post(provider, withClock([user(0)]))).status, 'runtime notes must not hide the remember prompt').toBe(200);
        const history = [user(0), REMEMBER, result('memory-scripted-remember', SHARED_SUCCESS)];
        const stored = await post(provider, withClock(history));
        expect(stored.status, 'runtime notes must not split the correlated remember result').toBe(200);
        expect(choices(await stored.text())).toEqual(textChoices('Memory stored.'));
        history.push({ role: 'assistant', content: 'Memory stored.' }, user(1));
        const recall = await post(provider, withClock(history));
        expect(recall.status, 'turn marker selects recall, not an earlier prompt or the clock').toBe(200);
        expect(choices(await recall.text())).toEqual(toolChoices('recall_memory', 'memory-scripted-recall', { query: NONCE }));
        const echoed = await post(provider, withClock([...history, RECALL, result('memory-scripted-recall', 'ONLY THE CURRENT RESULT')]));
        expect(echoed.status).toBe(200);
        const output = await echoed.text();
        expect(choices(output)).toEqual(textChoices('Memory recall result:\n\n```text\nONLY THE CURRENT RESULT\n```'));
        expect(output, 'clock handling must not make the script echo its configured nonce').not.toContain(NONCE);
        expect(provider.evidence.streamRequests).toBe(4);
        expect(provider.evidence.issuedCalls).toBe(2);
        expect(provider.evidence.errors).toEqual([]);
        expect(provider.requests).toHaveLength(4);
        expect(provider.requests.at(-1), 'diagnostics record the actual current result and runtime note, not script state').toEqual({
          stream: true, tools: TOOLS,
          messages: withClock([...history, RECALL, result('memory-scripted-recall', 'ONLY THE CURRENT RESULT')]),
        });
      } finally { await provider.close(); }
    });
  }

  for (const received of ['', 'unrelated tool result', [{ type: 'text', text: 'ONLY-' }, { type: 'text', text: 'RECEIVED' }]]) {
    it(`echoes only the correlated recall result, never its script or earlier history (${JSON.stringify(received)})`, async () => {
      const provider = await start();
      try {
        const history = await firstTurn(provider);
        history.push(user(1));
        expect((await post(provider, history)).status).toBe(200);
        const response = await post(provider, [...history, RECALL, result('memory-scripted-recall', received)]);
        expect(response.status).toBe(200);
        const expected = typeof received === 'string' ? received : 'ONLY-RECEIVED';
        const output = await response.text();
        expect(choices(output)).toEqual(textChoices(`Memory recall result:\n\n\`\`\`text\n${expected}\n\`\`\``));
        expect(output, 'nonce must not leak from the query, user history or script').not.toContain(NONCE);
        expect(output, 'the previous remember result is not the recalled content').not.toContain('ok — saved');
        expect(provider.evidence.toolResults.at(-1)).toEqual({ request: 4, callId: 'memory-scripted-recall', name: 'recall_memory', content: expected });
      } finally { await provider.close(); }
    });
  }

  for (const misuse of [
    { label: 'wrong prompt', messages: [{ role: 'user', content: 'another task' }], tools: TOOLS, error: 'expected turn 1 user prompt' },
    { label: 'prompt only in system text', messages: [{ role: 'system', content: PROMPTS[0] }], tools: TOOLS, error: 'expected turn 1 user prompt' },
    { label: 'later different turn marker', messages: [user(0), user(1)], tools: TOOLS, error: 'expected turn 1 user prompt' },
    { label: 'missing remember tool', messages: [user(0)], tools: [], error: 'remember was not offered to the agent' },
    { label: 'inherited tool history', messages: [user(0), REMEMBER], tools: TOOLS, error: 'expected turn 1 before its tool call' },
  ]) {
    it(`rejects ${misuse.label} before issuing remember`, async () => {
      const provider = await start();
      try {
        await rejected(await post(provider, misuse.messages, misuse.tools), misuse.error);
        expect(provider.evidence.errors).toEqual([misuse.error]);
        expect(provider.evidence.issuedCalls).toBe(0);
      } finally { await provider.close(); }
    });
  }

  for (const misuse of [
    { label: 'missing result', messages: [user(0), REMEMBER] },
    { label: 'wrong result id', messages: [user(0), REMEMBER, result('wrong-id', 'ok')] },
    { label: 'result before its call', messages: [user(0), result('memory-scripted-remember', 'ok'), REMEMBER] },
    { label: 'duplicate result', messages: [user(0), REMEMBER, result('memory-scripted-remember', 'ok'), result('memory-scripted-remember', 'ok')] },
    { label: 'wrong tool name', messages: [user(0), call('recall_memory', 'memory-scripted-remember', { query: NONCE }), result('memory-scripted-remember', 'ok')] },
    { label: 'altered remember arguments', messages: [user(0), call('remember', 'memory-scripted-remember', { content: 'wrong', category: 'reference', room: 'shared' }), result('memory-scripted-remember', 'ok')] },
  ]) {
    it(`rejects ${misuse.label}, rather than manufacturing a successful follow-up`, async () => {
      const provider = await start();
      try {
        expect((await post(provider, [user(0)])).status).toBe(200);
        await rejected(await post(provider, misuse.messages), 'expected the correlated remember call and result for turn 1');
        expect(provider.evidence.errors).toEqual(['expected the correlated remember call and result for turn 1']);
        expect(provider.evidence.issuedCalls).toBe(1);
      } finally { await provider.close(); }
    });
  }

  it('rejects a stale remember result instead of treating it as recall content', async () => {
    const provider = await start();
    try {
      const history = await firstTurn(provider);
      history.push(user(1));
      expect((await post(provider, history)).status).toBe(200);
      await rejected(await post(provider, history), 'expected the correlated recall_memory call and result for turn 2');
      expect(provider.evidence.errors).toEqual(['expected the correlated recall_memory call and result for turn 2']);
    } finally { await provider.close(); }
  });

  it('rejects malformed JSON and unknown routes with explicit errors', async () => {
    const provider = await start();
    try {
      await rejected(await providerFetch(`${provider.url}/v1/chat/completions`, { method: 'POST', body: '{' }), 'invalid provider JSON');
      const unknown = await providerFetch(`${provider.url}/wrong-route`);
      expect(unknown.status).toBe(404);
      expect(await unknown.json()).toEqual({ error: { message: 'unexpected provider route: GET /wrong-route' } });
      expect(provider.evidence.errors).toEqual(['invalid provider JSON', 'unexpected provider route: GET /wrong-route']);
      expect(provider.evidence.streamRequests).toBe(0);
    } finally { await provider.close(); }
  });

  it('rejects an excess seventh request without issuing another tool', async () => {
    const provider = await start();
    try {
      for (const [index, toolCall] of [REMEMBER, RECALL, ABSENT].entries()) {
        expect((await post(provider, [user(index)])).status).toBe(200);
        const id = toolCall.tool_calls[0].id;
        expect((await post(provider, [user(index), toolCall, result(id, index === 0 ? SHARED_SUCCESS : 'received')])).status).toBe(200);
      }
      await rejected(await post(provider, [user(2)]), 'expected exactly six agent-loop requests');
      expect(provider.evidence.streamRequests).toBe(7);
      expect(provider.evidence.issuedCalls).toBe(3);
      expect(provider.evidence.errors).toEqual(['expected exactly six agent-loop requests']);
    } finally { await provider.close(); }
  });
});

// #1800's shared-room contract. Do not grow the original describe container.
describe('shared-room remember contract carried from #1800', () => {
  it.each([
    'write failed', 'permission denied', 'nothing was saved', 'false',
    'ok — saved as a PRIVATE note: this turn has no workspace, so there is no team room to share into',
    'ok — saved as a private note', 'ok — saved', '', '   ', `${SHARED_SUCCESS} but failed`,
  ])('rejects non-shared remember success: %j', async (content) => {
    const provider = await start();
    try {
      expect((await post(provider, [user(0)])).status).toBe(200);
      const response = await post(provider, [user(0), REMEMBER, result('memory-scripted-remember', content)]);
      await rejected(response, 'the remember tool result is not the shared-room success');
      expect(provider.evidence.errors).toEqual(['the remember tool result is not the shared-room success']);
      expect(provider.evidence.issuedCalls).toBe(1);
    } finally { await provider.close(); }
  });

  it('accepts exact shared success with reordered argument keys', async () => {
    const provider = await start();
    try {
      expect((await post(provider, [user(0)])).status).toBe(200);
      const reordered = call('remember', 'memory-scripted-remember', { room: 'shared', category: 'reference', content: NONCE });
      const response = await post(provider, [user(0), reordered, result('memory-scripted-remember', SHARED_SUCCESS)]);
      expect(response.status, 'key order must not change the remember contract').toBe(200);
      expect(choices(await response.text()), 'exact shared success receives the normal acknowledgement').toEqual(textChoices('Memory stored.'));
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });
});

async function indexedRecallHistory(provider: Provider) {
  expect((await post(provider, [user(0)])).status).toBe(200);
  const remember = call('remember', 'memory-scripted-remember', { content: INDEX_NONCE, category: 'reference', room: 'shared' });
  const history = [user(0), remember, result('memory-scripted-remember', SHARED_SUCCESS)];
  expect((await post(provider, history)).status).toBe(200);
  return [...history, { role: 'assistant', content: 'Memory stored.' }, user(1)];
}

describe('index-query and result contract carried from #1800', () => {
  it('issues the reordered index query and echoes the received result, not its configured nonce', async () => {
    const provider = await startIndexed();
    try {
      expect(INDEX_NONCE.toLowerCase(), 'index query must not be a case-insensitive substring').not.toContain(INDEX_QUERY.toLowerCase());
      const history = await indexedRecallHistory(provider);
      const response = await post(provider, history);
      expect(response.status).toBe(200);
      expect(choices(await response.text()), 'the reordered query reaches the real tool-call boundary').toEqual(
        toolChoices('recall_memory', 'memory-scripted-recall', { query: INDEX_QUERY }));
      const recall = call('recall_memory', 'memory-scripted-recall', { query: INDEX_QUERY });
      const echoed = await post(provider, [...history, recall, result('memory-scripted-recall', 'ONLY THE INDEX RESULT')]);
      expect(echoed.status).toBe(200);
      const output = await echoed.text();
      expect(choices(output), 'only the current result is echoed').toEqual(textChoices('Memory recall result:\n\n```text\nONLY THE INDEX RESULT\n```'));
      expect(output, 'reordered query support must not manufacture the configured nonce').not.toContain(INDEX_NONCE);
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });

  it('rejects a missing recall result after issuing the reordered query', async () => {
    const provider = await startIndexed();
    try {
      const history = await indexedRecallHistory(provider);
      expect((await post(provider, history)).status).toBe(200);
      const recall = call('recall_memory', 'memory-scripted-recall', { query: INDEX_QUERY });
      await rejected(await post(provider, [...history, recall]), 'expected the correlated recall_memory call and result for turn 2');
      expect(provider.evidence.errors).toEqual(['expected the correlated recall_memory call and result for turn 2']);
    } finally { await provider.close(); }
  });

  it('echoes an explicit no-match recall without leaking either nonce', async () => {
    const provider = await start();
    try {
      const history = [...await firstTurn(provider), user(1)];
      expect((await post(provider, history)).status).toBe(200);
      const response = await post(provider, [...history, RECALL, result('memory-scripted-recall', 'no matching entries')]);
      expect(response.status).toBe(200);
      const output = await response.text();
      expect(choices(output), 'the no-match result is echoed verbatim').toEqual(textChoices('Memory recall result:\n\n```text\nno matching entries\n```'));
      expect(output).not.toContain(NONCE);
      expect(output).not.toContain(MISSING);
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });
});

describe('default-scope recall and request recording (#1661 fix round 2)', () => {
  it('records the room-less reordered recall from the actual follow-up request', async () => {
    const provider = await startIndexed();
    try {
      const history = await indexedRecallHistory(provider);
      expect((await post(provider, history)).status).toBe(200);
      const recall = call('recall_memory', 'memory-scripted-recall', { query: INDEX_QUERY });
      const messages = [...history, recall, result('memory-scripted-recall', 'INDEX DEFAULT RESULT')];
      const response = await post(provider, messages);
      expect(response.status, 'the positive recall must omit room to use the real default scope').toBe(200);
      expect(choices(await response.text())).toEqual(textChoices('Memory recall result:\n\n```text\nINDEX DEFAULT RESULT\n```'));
      expect(provider.requests[3], 'request 4 records the actual room-less call and received result').toEqual({ stream: true, messages, tools: TOOLS });
      expect(provider.evidence.errors).toEqual([]);
    } finally { await provider.close(); }
  });

  it('rejects an added room key instead of accepting changed default-scope arguments', async () => {
    const provider = await start();
    try {
      const history = [...await firstTurn(provider), user(1)];
      expect((await post(provider, history)).status).toBe(200);
      const changed = call('recall_memory', 'memory-scripted-recall', { query: NONCE, room: 'shared' });
      await rejected(await post(provider, [...history, changed, result('memory-scripted-recall', 'received')]),
        'expected the correlated recall_memory call and result for turn 2');
      expect(provider.evidence.errors).toEqual(['expected the correlated recall_memory call and result for turn 2']);
      expect(provider.evidence.issuedCalls).toBe(2);
    } finally { await provider.close(); }
  });
});

describe('advertised tools and turn markers carried from #1800', () => {
  it.each([1, 2, 3])('requires both memory tools before call-producing turn %i', async (turn) => {
    const provider = await start();
    try {
      for (const index of Array.from({ length: turn - 1 }, (_, i) => i)) {
        expect((await post(provider, [user(index)])).status).toBe(200);
        const tool = [REMEMBER, RECALL][index];
        expect((await post(provider, [user(index), tool, result(tool.tool_calls[0].id, index === 0 ? SHARED_SUCCESS : 'received')])).status).toBe(200);
      }
      const onlyCurrentTool = [{ function: { name: turn === 1 ? 'remember' : 'recall_memory' } }];
      await rejected(await post(provider, [user(turn - 1)], onlyCurrentTool), 'remember and recall_memory must both be offered');
      expect(provider.evidence.errors).toEqual(['remember and recall_memory must both be offered']);
      expect(provider.evidence.issuedCalls).toBe(turn - 1);
    } finally { await provider.close(); }
  });

  it('rejects recall when recall_memory itself is not offered', async () => {
    const provider = await start();
    try {
      const history = [...await firstTurn(provider), user(1)];
      await rejected(await post(provider, history, [{ function: { name: 'remember' } }]), 'recall_memory was not offered to the agent');
      expect(provider.evidence.errors).toEqual(['recall_memory was not offered to the agent']);
    } finally { await provider.close(); }
  });

  it('rejects a later different scripted prompt during recall', async () => {
    const provider = await start();
    try {
      const history = [...await firstTurn(provider), user(1), user(2)];
      await rejected(await post(provider, history), 'expected turn 2 user prompt');
      expect(provider.evidence.errors).toEqual(['expected turn 2 user prompt']);
    } finally { await provider.close(); }
  });
});
