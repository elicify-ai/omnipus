import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// Small sibling of replay-attach-provider.ts for tests that need one ordinary,
// immediate, deterministic reply and no live model call (#2010). The reply is a
// pure function of the user messages, so the test controls the exact text
// the real engine will stream and the browser will render.

// not-wire-format: local fixture controls, not a gateway/SPA protocol.
export type ScriptedReplyProvider = {
  url: string;
  state(): { calls: number; userTexts: string[] };
  failures(): string[];
  close(): Promise<void>;
};

/** Fake only the external provider; the real engine creates all gateway frames. */
export async function startScriptedReplyProvider(
  reply: (userText: string) => string,
): Promise<ScriptedReplyProvider> {
  let calls = 0;
  const userTexts: string[] = [];
  const failures: string[] = [];

  const server = http.createServer((request, response) => {
    const buffers: Buffer[] = [];
    request.on('data', (buffer: Buffer) => buffers.push(buffer));
    request.on('error', error => failures.push(`provider request: ${error.message}`));
    request.on('end', () => {
      const reject = (status: number, message: string) => {
        failures.push(message);
        response.writeHead(status, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ error: { message } }));
      };
      if (request.method === 'GET' && /^\/(v1\/)?models$/.test(request.url ?? '')) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (request.method !== 'POST' || !/^\/(v1\/)?chat\/completions$/.test(request.url ?? '')) {
        reject(404, `unexpected provider route: ${request.method} ${request.url}`);
        return;
      }
      let body: { stream?: unknown; messages?: { role?: string; content?: unknown }[] };
      try { body = JSON.parse(Buffer.concat(buffers).toString('utf8')); }
      catch { reject(400, 'invalid provider request JSON'); return; }
      if (body.stream !== true) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ id: 'scripted-reply-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      if (calls !== 0) { reject(409, 'unexpected second streaming request'); return; }
      calls++;
      // The engine appends its own user-role notes (for example the deferred-tool
      // list) after the person's prompt, so the prompt is not always the last
      // user message. The reply sees every user message, oldest first.
      const userText = (body.messages ?? []).filter(message => message.role === 'user').map(message => {
        const content = message.content
        return typeof content === 'string' ? content
          : Array.isArray(content) ? content.map(part => (part as { text?: string }).text ?? '').join('') : ''
      }).join('\n')
      userTexts.push(userText)
      let text: string
      try { text = reply(userText) }
      catch (error) { reject(500, `scripted reply failed: ${String(error)}`); return }
      response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      response.write(chunk({ role: 'assistant', content: text }, null));
      response.end(chunk({}, 'stop') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('scripted reply provider has no port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return {
    url,
    state: () => ({ calls, userTexts: [...userTexts] }),
    failures: () => [...failures],
    close: () => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({ id: 'scripted-reply-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    choices: [{ index: 0, delta, finish_reason: finishReason }] }) + '\n\n';
}
