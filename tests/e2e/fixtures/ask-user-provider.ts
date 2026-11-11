import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

export const ASK_USER_PROMPT = 'ASK-1769: ask which colour I want before continuing.';
export const ASK_USER_QUESTION = 'Which colour should I use?';
export const FRESH_CHAT_PROMPT = 'FRESH-1769: reply in this separate chat.';
export const FRESH_CHAT_REPLY = 'Fresh chat accepted.';

/** Script only the external OpenAI-compatible HTTP edge. Discovery, tool
 * execution, the question registry, WebSocket delivery and Cancel stay real. */
export async function startAskUserProvider(options: { reaskAfterCancel?: boolean } = {}) {
  const evidence = { streamRequests: 0, questionCalls: 0, searches: 0, replies: 0, errors: [] as string[] };
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('error', error => evidence.errors.push(`provider request: ${error.message}`));
    req.on('end', () => {
      const fail = (status: number, message: string) => {
        evidence.errors.push(message);
        res.writeHead(status, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message } }));
      };
      if (req.method === 'GET' && req.url?.endsWith('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !req.url?.endsWith('/chat/completions')) {
        fail(404, `unexpected provider route: ${req.method} ${req.url}`); return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>; }
      catch { fail(400, 'invalid provider JSON'); return; }
      if (body.stream !== true) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ id: 'ask-1769-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }));
        return;
      }
      evidence.streamRequests++;
      const messages = (Array.isArray(body.messages) ? body.messages : []) as Array<{ role?: string; content?: unknown }>;
      // Script actual tool rounds rather than a guessed last user-role message:
      // the real loop appends additional context after the person's prompt.
      const text = JSON.stringify(messages);
      const freshPrompt = text.includes(FRESH_CHAT_PROMPT);
      const reask = options.reaskAfterCancel === true && evidence.questionCalls === 1 && text.includes('cancelled');
      const offered = new Set((Array.isArray(body.tools) ? body.tools : []).map(tool =>
        (tool as { function?: { name?: string } }).function?.name));
      let delta: Record<string, unknown>;
      let finish: string;
      if (!freshPrompt && (evidence.questionCalls === 0 || reask)) {
        if (offered.has('AskUserQuestion')) {
          evidence.questionCalls++;
          delta = call('AskUserQuestion', { questions: [{ header: 'Colour', question: ASK_USER_QUESTION,
            options: [{ label: 'Blue' }, { label: 'Green' }] }] }, `ask-1769-${evidence.questionCalls}`);
        } else if (offered.has('ToolSearch')) {
          evidence.searches++;
          if (evidence.searches > 1) { fail(422, 'AskUserQuestion was not offered after discovery'); return; }
          delta = call('ToolSearch', { names: ['AskUserQuestion'] }, 'ask-1769-discover');
        } else { fail(422, 'neither AskUserQuestion nor ToolSearch was offered'); return; }
        finish = 'tool_calls';
      } else {
        evidence.replies++;
        delta = { role: 'assistant', content: freshPrompt ? FRESH_CHAT_REPLY : 'Question cancelled.' };
        finish = 'stop';
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk(delta, null) + chunk({}, finish) + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('ask provider has no private TCP port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return { url, evidence, close: () => new Promise<void>((resolve, reject) => {
    server.close(error => error ? reject(error) : resolve()); server.closeAllConnections();
  }) };
}

function call(name: string, args: unknown, id: string) {
  return { tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] };
}
function chunk(delta: Record<string, unknown>, finish: string | null) {
  return 'data: ' + JSON.stringify({ id: 'ask-1769-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finish }] }) + '\n\n';
}
