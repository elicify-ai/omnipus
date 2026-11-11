// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { E2E_FALLBACK } from './e2e-model';
import { ASK_USER_PROMPT, ASK_USER_QUESTION, FRESH_CHAT_PROMPT, FRESH_CHAT_REPLY, startAskUserProvider } from './ask-user-provider';

function post(url: string, body: unknown) {
  return fetch(`${url}/chat/completions`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
}
function streamBody(prompt: string, tools: string[]) {
  return { stream: true, model: E2E_FALLBACK.model, messages: [{ role: 'user', content: prompt }],
    tools: tools.map(name => ({ type: 'function', function: { name } })) };
}
async function deltas(response: Response) {
  return (await response.text()).split('\n').filter(line => line.startsWith('data: ') && line !== 'data: [DONE]')
    .map(line => JSON.parse(line.slice(6)) as { choices: Array<{ delta: Record<string, unknown>; finish_reason: string | null }> });
}

describe('#1769 scripted question provider', () => {
  it('serves model discovery and validation without consuming a question turn', async () => {
    const provider = await startAskUserProvider();
    try {
      const models = await fetch(`${provider.url}/models`);
      expect(models.status).toBe(200);
      expect(await models.json()).toEqual({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] });
      const probe = await post(provider.url, { stream: false });
      expect(probe.status).toBe(200);
      expect(await probe.json()).toMatchObject({ choices: [{ message: { content: 'probe ok' }, finish_reason: 'stop' }] });
      expect(provider.evidence).toEqual({ streamRequests: 0, questionCalls: 0, searches: 0, replies: 0, errors: [] });
    } finally { await provider.close(); }
  });
  it('discovers the real tool, issues the specified question and answers a distinct fresh chat', async () => {
    const provider = await startAskUserProvider();
    try {
      const discovery = await post(provider.url, streamBody(ASK_USER_PROMPT, ['ToolSearch']));
      expect(discovery.status).toBe(200);
      expect((await deltas(discovery))[0].choices[0].delta).toEqual({ tool_calls: [{ index: 0, id: 'ask-1769-discover', type: 'function', function: { name: 'ToolSearch', arguments: JSON.stringify({ names: ['AskUserQuestion'] }) } }] });
      const question = await post(provider.url, streamBody(ASK_USER_PROMPT, ['AskUserQuestion']));
      expect(question.status).toBe(200);
      const chunks = await deltas(question);
      expect(chunks[0].choices[0].delta).toEqual({ tool_calls: [{ index: 0, id: 'ask-1769-1', type: 'function', function: { name: 'AskUserQuestion', arguments: JSON.stringify({ questions: [{ header: 'Colour', question: ASK_USER_QUESTION, options: [{ label: 'Blue' }, { label: 'Green' }] }] }) } }] });
      expect(chunks[1].choices[0].finish_reason).toBe('tool_calls');
      const fresh = await post(provider.url, streamBody(FRESH_CHAT_PROMPT, []));
      expect(fresh.status).toBe(200);
      expect((await deltas(fresh))[0].choices[0].delta).toEqual({ role: 'assistant', content: FRESH_CHAT_REPLY });
      const resume = await post(provider.url, streamBody('{"status":"cancelled"}', []));
      expect(resume.status).toBe(200);
      expect((await deltas(resume))[0].choices[0].delta).toEqual({ role: 'assistant', content: 'Question cancelled.' });
      expect(provider.evidence).toEqual({ streamRequests: 4, questionCalls: 1, searches: 1, replies: 2, errors: [] });
    } finally { await provider.close(); }
  });
  it('recognises the real provider text-parts encoding instead of treating a question as a resume', async () => {
    const provider = await startAskUserProvider();
    try {
      const response = await post(provider.url, { ...streamBody('', ['AskUserQuestion']),
        messages: [{ role: 'user', content: [{ type: 'text', text: ASK_USER_PROMPT }] }] });
      expect(response.status).toBe(200);
      const chunks = await deltas(response);
      expect(chunks[0].choices[0].delta).toMatchObject({ tool_calls: [{ function: { name: 'AskUserQuestion' } }] });
      expect(provider.evidence).toEqual({ streamRequests: 1, questionCalls: 1, searches: 0, replies: 0, errors: [] });
      const fresh = await post(provider.url, { ...streamBody('', []),
        messages: [{ role: 'user', content: [{ type: 'text', text: FRESH_CHAT_PROMPT }] },
          { role: 'user', content: 'additional agent-loop context' }] });
      expect(fresh.status).toBe(200);
      expect((await deltas(fresh))[0].choices[0].delta).toEqual({ role: 'assistant', content: FRESH_CHAT_REPLY });
      expect(provider.evidence).toEqual({ streamRequests: 2, questionCalls: 1, searches: 0, replies: 1, errors: [] });
    } finally { await provider.close(); }
  });
  it('re-asks the identical question exactly once after Cancel, then finishes after the second Cancel', async () => {
    const provider = await startAskUserProvider({ reaskAfterCancel: true });
    try {
      const first = await post(provider.url, streamBody(ASK_USER_PROMPT, ['AskUserQuestion']));
      expect(first.status).toBe(200);
      const firstChunks = await deltas(first);
      const questionArgs = JSON.stringify({ questions: [{ header: 'Colour', question: ASK_USER_QUESTION,
        options: [{ label: 'Blue' }, { label: 'Green' }] }] });
      expect(firstChunks[0].choices[0].delta).toEqual({ tool_calls: [{ index: 0, id: 'ask-1769-1', type: 'function',
        function: { name: 'AskUserQuestion', arguments: questionArgs } }] });
      const cancelled = streamBody('{"status":"cancelled"}', ['AskUserQuestion']);
      const second = await post(provider.url, cancelled);
      expect(second.status).toBe(200);
      const secondChunks = await deltas(second);
      expect(secondChunks[0].choices[0].delta).toEqual({ tool_calls: [{ index: 0, id: 'ask-1769-2', type: 'function',
        function: { name: 'AskUserQuestion', arguments: questionArgs } }] });
      expect(secondChunks[1].choices[0].finish_reason).toBe('tool_calls');
      const finish = await post(provider.url, cancelled);
      expect(finish.status).toBe(200);
      const finalChunks = await deltas(finish);
      expect(finalChunks[0].choices[0].delta).toEqual({ role: 'assistant', content: 'Question cancelled.' });
      expect(finalChunks[1].choices[0].finish_reason).toBe('stop');
      expect(provider.evidence).toEqual({ streamRequests: 3, questionCalls: 2, searches: 0, replies: 1, errors: [] });
    } finally { await provider.close(); }
  });
  it('does not re-ask on an unrelated fresh-chat request when the re-ask script is enabled', async () => {
    const provider = await startAskUserProvider({ reaskAfterCancel: true });
    try {
      const question = await post(provider.url, streamBody(ASK_USER_PROMPT, ['AskUserQuestion']));
      expect(question.status).toBe(200);
      await question.text();
      const fresh = await post(provider.url, streamBody(FRESH_CHAT_PROMPT, ['AskUserQuestion']));
      expect(fresh.status).toBe(200);
      expect((await deltas(fresh))[0].choices[0].delta).toEqual({ role: 'assistant', content: FRESH_CHAT_REPLY });
      expect(provider.evidence).toEqual({ streamRequests: 2, questionCalls: 1, searches: 0, replies: 1, errors: [] });
    } finally { await provider.close(); }
  });
  it('rejects a malformed request with a specific error instead of inventing a question', async () => {
    const provider = await startAskUserProvider();
    try {
      const response = await fetch(`${provider.url}/chat/completions`, { method: 'POST', body: '{' });
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({ error: { message: 'invalid provider JSON' } });
      expect(provider.evidence).toEqual({ streamRequests: 0, questionCalls: 0, searches: 0, replies: 0, errors: ['invalid provider JSON'] });
    } finally { await provider.close(); }
  });
  it('rejects a question turn with no offered question or discovery tool', async () => {
    const provider = await startAskUserProvider();
    try {
      const response = await post(provider.url, streamBody(ASK_USER_PROMPT, []));
      expect(response.status).toBe(422);
      expect(await response.json()).toEqual({ error: { message: 'neither AskUserQuestion nor ToolSearch was offered' } });
      expect(provider.evidence.questionCalls).toBe(0);
      expect(provider.evidence.errors).toEqual(['neither AskUserQuestion nor ToolSearch was offered']);
    } finally { await provider.close(); }
  });
  it('rejects repeated discovery when the gateway never offers the requested tool', async () => {
    const provider = await startAskUserProvider();
    try {
      const first = await post(provider.url, streamBody(ASK_USER_PROMPT, ['ToolSearch']));
      expect(first.status).toBe(200);
      await first.text();
      const second = await post(provider.url, streamBody(ASK_USER_PROMPT, ['ToolSearch']));
      expect(second.status).toBe(422);
      expect(await second.json()).toEqual({ error: { message: 'AskUserQuestion was not offered after discovery' } });
      expect(provider.evidence.errors).toEqual(['AskUserQuestion was not offered after discovery']);
    } finally { await provider.close(); }
  });
});
