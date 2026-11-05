import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// Process-edge fake model only. No gateway frame, approval registry, browser
// store or tool execution is mocked. The value is a harmless fixture secret.
export const T8_ENV_VALUE = 't8-approval-fixture-not-a-real-secret-0001';
export async function startT8AgentApprovalProvider() {
  let target: { id: string; revision: string } | undefined;
  let issued = 0;
  let searched = false;
  const failures: string[] = [];
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('error', (err) => failures.push(`provider input: ${err.message}`));
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        failures.push(`unexpected model route: ${req.method} ${req.url}`);
        res.writeHead(404); res.end(); return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString()) as Record<string, unknown>; }
      catch (err) { failures.push(`model JSON: ${String(err)}`); res.writeHead(400); res.end(); return; }
      if (body.stream !== true) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ id: 't8-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      const names = new Set((Array.isArray(body.tools) ? body.tools : []).flatMap((entry) => {
        const name = (entry as { function?: { name?: string } }).function?.name;
        return name ? [name] : [];
      }));
      if (issued > 0) { res.end(chunk({ content: 'T8 approval resolved.' }, 'stop') + 'data: [DONE]\n\n'); return; }
      if (!target) { failures.push('T8 target was not configured before the real turn'); res.end(chunk({}, 'stop') + 'data: [DONE]\n\n'); return; }
      if (!names.has('update_agent')) {
        if (searched || !names.has('ToolSearch')) {
          failures.push('BLOCKED: production update_agent is not discoverable — required by r17 17.2');
          res.end(chunk({}, 'stop') + 'data: [DONE]\n\n'); return;
        }
        searched = true;
        res.end(call('ToolSearch', { names: ['update_agent'] }, 't8-discover') + chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
        return;
      }
      issued++;
      res.end(call('update_agent', { ...target, env_overrides: { TOKEN: T8_ENV_VALUE }, cli_args: '--model x --verbose' }, 't8-change') + chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (!addr || typeof addr === 'string') { reject(new Error('provider did not bind a private port')); return; }
      resolve(`http://127.0.0.1:${addr.port}`);
    });
  });
  return { url, setTarget: (value: { id: string; revision: string }) => { target = value; },
    issued: () => issued, failures: () => [...failures],
    close: () => new Promise<void>((resolve, reject) => { server.close((err) => err ? reject(err) : resolve()); server.closeAllConnections(); }) };
}
function call(name: string, args: unknown, id: string) {
  return chunk({ tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] }, null);
}
function chunk(delta: Record<string, unknown>, finish: string | null) {
  return 'data: ' + JSON.stringify({ id: 't8-model', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finish }] }) + '\n\n';
}
