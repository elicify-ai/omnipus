import http, { type ServerResponse } from 'node:http';
import { E2E_MODEL } from './e2e-model';

export const HISTORY_XSS_PAYLOAD = '<script>window.__xss_fired=true;alert(1)</script><img src=x onerror="window.__xss_fired=true;alert(1)">';

/** Only the external model HTTP boundary is scripted; gateway, WS, persistence
 * and both markdown render paths stay real. The live payload is held in flight. */
export async function startXssHistoryProvider() {
  let calls = 0;
  const failures: string[] = [];
  const held = new Set<ServerResponse>();
  const chunk = (content: string, finish: string | null) => 'data: ' + JSON.stringify({
    id: 'xss-history-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    choices: [{ index: 0, delta: { content }, finish_reason: finish }],
  }) + '\n\n';
  const server = http.createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on('data', (part: Buffer) => chunks.push(part));
    request.on('error', error => failures.push(`external provider request failed: ${error.message}`));
    request.on('end', () => {
      if (request.method === 'GET' && request.url?.includes('/models')) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ object: 'list', data: [{ id: E2E_MODEL, object: 'model' }] }));
        return;
      }
      if (request.method !== 'POST' || !request.url?.includes('/chat/completions')) {
        failures.push(`unexpected external provider route: ${request.method} ${request.url}`);
        response.writeHead(404); response.end(); return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>; }
      catch (error) {
        failures.push(`invalid external provider JSON: ${String(error)}`);
        response.writeHead(400); response.end(); return;
      }
      if (body.stream !== true) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ id: 'xss-history-probe', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'ready' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      calls++;
      if (calls !== 1) {
        failures.push(`expected one real chat turn, received ${calls}`);
        response.writeHead(500); response.end(); return;
      }
      response.writeHead(200, { 'Content-Type': 'text/event-stream' });
      held.add(response);
      response.on('close', () => held.delete(response));
      response.write(chunk(HISTORY_XSS_PAYLOAD, null));
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('XSS provider has no real ephemeral port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  const finish = () => {
    for (const response of held) response.end(chunk('', 'stop') + 'data: [DONE]\n\n');
    held.clear();
  };
  return { url, calls: () => calls, failures: () => [...failures], heldCount: () => held.size, finish,
    async close() {
      finish();
      await new Promise<void>((resolve, reject) => {
        server.close(error => error ? reject(error) : resolve());
        server.closeAllConnections();
      });
    },
  };
}
