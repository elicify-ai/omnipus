// #1537: only the external OpenAI-compatible model edge is scripted.
// No WebSocket notice, store, delegate implementation or tool result is mocked.
import http from 'node:http';
import { setTimeout as delay } from 'node:timers/promises';
import { E2E_FALLBACK } from './e2e-model';

export type HelperScript = 'retry' | 'silence' | 'silence-retry' | 'empty' | 'timeout' | 'fallback' | 'repeat' | 'failure';
export function barrier() {
  let resolve!: () => void;
  const promise = new Promise<void>(yes => { resolve = yes; });
  return { promise, resolve };
}
// not-wire-format: fixture scenario controls; never sent to a gateway API.
export type NoticeScriptOptions = { script: HelperScript; helperScripts?: HelperScript[]; wait?: number; helperWaits?: number[]; count?: number; parentWriting?: boolean; parentRetry?: boolean; nested?: boolean };

type ModelRequest = { stream?: boolean; model?: string; messages?: Array<{ role?: string; content?: unknown; tool_call_id?: string }>;
  tools?: Array<{ function?: { name?: string } }> };

export async function startHelperNoticesProvider(options: NoticeScriptOptions) {
  let agents: string[] = [];
  const parentFinish = barrier();
  const fail = Array.from({ length: options.count ?? 1 }, barrier);
  const answer = fail.map(barrier);
  const finish = fail.map(barrier);
  const helpers = fail.map((_, i) => ({ calls: 0, success: false, toolSent: false, complete: false, firstAt: 0, retryWait: options.helperWaits?.[i] ?? options.wait ?? 10 }));
  const evidence = { requests: [] as Array<{ role: string; call: number; model: string; at: number; status?: number }>, errors: [] as string[],
    delegateResults: [] as unknown[], offeredTools: [] as string[][], parentCalls: 0, parentHeld: false, parentDone: false, toolResults: [] as unknown[] };

  function chunk(delta: Record<string, unknown>, reason: string | null = null) {
    return 'data: ' + JSON.stringify({ id: 'hm-script', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
      created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: reason }] }) + '\n\n';
  }
  function tools(res: http.ServerResponse, calls: Array<{ id: string; name: string; args: object }>) {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(chunk({ role: 'assistant', tool_calls: calls.map((c, index) => ({ index, id: c.id, type: 'function',
      function: { name: c.name, arguments: JSON.stringify(c.args) } })) }) + chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
  }
  function toolFor(body: ModelRequest, res: http.ServerResponse, index: number) {
    const state = helpers[index];
    const names = (body.tools ?? []).map(t => t.function?.name ?? '');
    evidence.offeredTools.push(names);
    if (!names.includes('bash')) {
      if (!names.includes('ToolSearch')) throw new Error('real ToolSearch or bash must be offered');
      tools(res, [{ id: `hm-search-${index}`, name: 'ToolSearch', args: { names: ['bash'] } }]);
      return;
    }
    state.toolSent = true;
    tools(res, [{ id: `hm-hold-${index}`, name: 'bash', args: { command: `sleep 30; printf 'HM_TOOL_${index}_OK'`, run_in_background: false } }]);
  }
  async function serve(req: http.IncomingMessage, res: http.ServerResponse, bytes: Buffer[]) {
    if (req.method === 'GET' && req.url?.startsWith('/held-tool/')) {
      throw new Error('unused HTTP tool path: the product must execute the real bash tool');
    }
    if (req.method === 'GET' && /\/models$/.test(req.url ?? '')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model }, { id: 'hm-fallback' }] })); return;
    }
    if (req.method !== 'POST' || !/\/chat\/completions$/.test(req.url ?? '')) throw new Error(`unexpected route ${req.method} ${req.url}`);
    const body = JSON.parse(Buffer.concat(bytes).toString()) as ModelRequest;
    if (body.stream !== true) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ id: 'hm-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
        choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }] })); return;
    }
    const users = (body.messages ?? []).filter(m => m.role === 'user').map(m => typeof m.content === 'string' ? m.content : JSON.stringify(m.content)).join('\n');
    const helperMatch = users.match(/HM_HELPER_(\d+)_TASK/);
    if (!helperMatch && !users.includes('HM_ROOT_TASK')) throw new Error('no exact test task marker in real model request');
    if (!helperMatch) { await parent(body, res); return; }
    const index = Number(helperMatch[1]);
    const state = helpers[index];
    if (!state) throw new Error(`unexpected helper index ${index}`);
    const call = ++state.calls;
    if (!state.firstAt) state.firstAt = Date.now();
    const row: (typeof evidence.requests)[number] = { role: `helper-${index}`, call, model: body.model ?? '', at: Date.now() };
    evidence.requests.push(row);
    if (options.nested && index === 0 && call === 1) {
      row.status = 200;
      tools(res, [{ id: 'hm-grandchild', name: 'delegate', args: { action: 'run', agent_id: agents[0], task: 'HM_HELPER_1_TASK. Return HM_CHILD_1_OK.', label: 'nested grandchild' } }]); return;
    }
    if (options.nested && index === 0) {
      row.status = 200;
      evidence.delegateResults.push(...(body.messages ?? []).filter(m => m.role === 'tool' && m.tool_call_id === 'hm-grandchild'));
      res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(chunk({ role: 'assistant', content: 'Middle is still working. ' }));
      await finish[0].promise;
      res.end(chunk({ content: 'HM_MIDDLE_OK' }, 'stop') + 'data: [DONE]\n\n'); return;
    }
    const script = options.helperScripts?.[index] ?? options.script;
    const primaryFailure = script === 'fallback' && body.model !== 'hm-fallback' && !state.success;
    if (call === 1 || primaryFailure || script === 'failure') {
      await fail[index].promise;
      if (res.destroyed) return; // A timed-out request cannot send a later fake success.
      if (script === 'timeout' && call === 1) {
        // No headers/bytes: the configured 3 s REQUEST timeout must fire in the real provider.
        await new Promise<void>(resolve => res.once('close', resolve)); return;
      }
      if (script === 'empty' && call === 1) {
        row.status = 200;
        res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.end('data: [DONE]\n\n'); return;
      }
      if (script === 'silence' || script === 'silence-retry') await answer[index].promise;
      if (script !== 'silence') {
        row.status = script === 'failure' ? 500 : 429;
        res.writeHead(row.status, { 'Content-Type': 'application/json', 'Retry-After': String(state.retryWait) });
        res.end(JSON.stringify({ error: { message: 'scripted helper unavailable', code: row.status === 429 ? 'rate_limit_exceeded' : 'server_error' } })); return;
      }
    }
    if (!state.success) {
      await answer[index].promise;
      if (res.destroyed) return;
      state.success = true; row.status = 200; toolFor(body, res, index); return;
    }
    if (!state.toolSent) { row.status = 200; toolFor(body, res, index); return; }
    const heldResult = (body.messages ?? []).find(m => m.role === 'tool' && m.tool_call_id === `hm-hold-${index}`);
    if (!heldResult) throw new Error('helper continuation has no correlated real 30 s tool result');
    evidence.toolResults.push(heldResult);
    if (script === 'repeat' && !state.complete) {
      state.complete = true;
      row.status = 429;
      res.writeHead(429, { 'Content-Type': 'application/json', 'Retry-After': String(state.retryWait) });
      res.end(JSON.stringify({ error: { message: 'second same-turn rate limit', code: 'rate_limit_exceeded' } })); return;
    }
    await finish[index].promise;
    if (res.destroyed) return;
    row.status = 200;
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(chunk({ role: 'assistant', content: `HM_CHILD_${index}_OK` }, 'stop') + 'data: [DONE]\n\n');
  }
  async function parent(body: ModelRequest, res: http.ServerResponse) {
    evidence.parentCalls++;
    const row: (typeof evidence.requests)[number] = { role: 'parent', call: evidence.parentCalls, model: body.model ?? '', at: Date.now() };
    evidence.requests.push(row);
    if (evidence.parentCalls === 1) {
      row.status = 200;
      if (!(body.tools ?? []).some(t => t.function?.name === 'delegate')) throw new Error('real delegate tool not offered');
      tools(res, Array.from({ length: options.nested ? 1 : helpers.length }, (_, i) => ({ id: `hm-delegate-${i}`, name: 'delegate',
        args: { action: 'run', agent_id: agents[i], task: `HM_HELPER_${i}_TASK. Return HM_CHILD_${i}_OK.`, label: `helper ${i}` } }))); return;
    }
    const results = (body.messages ?? []).filter(m => m.role === 'tool' && m.tool_call_id?.startsWith('hm-delegate-'));
    if (!results.length) throw new Error('parent continuation lacks correlated real delegate result');
    evidence.delegateResults.push(...results);
    if (options.parentRetry && evidence.parentCalls === 2) {
      row.status = 429;
      res.writeHead(429, { 'Content-Type': 'application/json', 'Retry-After': '20' });
      res.end(JSON.stringify({ error: { message: 'main model is temporarily rate limited' } })); return;
    }
    evidence.parentHeld = true;
    row.status = 200;
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(chunk({ role: 'assistant', content: 'HM_PARENT_IS_WORKING ' }));
    const heartbeat = setInterval(() => { if (!res.destroyed) res.write(': keepalive\n\n'); }, 1000);
    try { await parentFinish.promise; } finally { clearInterval(heartbeat); }
    evidence.parentDone = true;
    res.end(chunk({ content: 'HM_ROOT_OK' }, 'stop') + 'data: [DONE]\n\n');
  }
  const server = http.createServer((req, res) => {
    const bytes: Buffer[] = [];
    req.on('data', (b: Buffer) => bytes.push(b));
    req.on('error', error => evidence.errors.push(error.message));
    req.on('end', () => { void serve(req, res, bytes).catch(error => {
      evidence.errors.push(String(error));
      if (!res.headersSent) res.writeHead(400, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message: 'script error', detail: String(error) } }));
    }); });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject); server.listen(0, '127.0.0.1', () => {
      const a = server.address(); if (!a || typeof a === 'string') { reject(new Error('no provider port')); return; }
      resolve(`http://127.0.0.1:${a.port}`);
    });
  });
  return { url, evidence, helpers, fail, answer, finish, parentFinish,
    setAgents(ids: string[]) { agents = ids; },
    nextHelperTurn(index: number, retryWait = options.wait ?? 10) {
      // Only reset the external model script after the real stopped turn.
      helpers[index] = { calls: 0, success: false, toolSent: false, complete: false, firstAt: 0, retryWait };
      answer[index] = barrier(); finish[index] = barrier();
    },
    async close() {
      parentFinish.resolve(); for (const gate of [...fail, ...answer, ...finish]) gate.resolve();
      await delay(0);
      await new Promise<void>((resolve, reject) => { server.close(e => e ? reject(e) : resolve()); server.closeAllConnections(); });
    },
  };
}
