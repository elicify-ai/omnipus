import http from 'node:http';
import type { ServerResponse } from 'node:http';
import { E2E_MODEL } from '../e2e-model';

// Only this external model HTTP boundary is scripted. The engine, tool search,
// actual approval/question registries and UI are never replaced.
export type HistoryControlKind = 'approval' | 'question' | 'stream';
export const CONTROL_RUNNING = 'HISTORY_CONTROL_RUNNING';
export const CONTROL_QUESTION = 'Which saved output format should I use?';
export const CONTROL_COMMAND = 'printf HISTORY_APPROVAL_ALLOWED';
export type HistoryControlProvider = {
  kind: HistoryControlKind; url: string; calls(): number; failures(): string[];
  aborted(): number; heldCount(): number; finishToolRound(): void; close(): Promise<void>;
};

export async function startHistoryControlProvider(kind: HistoryControlKind): Promise<HistoryControlProvider> {
  let calls = 0;
  let searched = false;
  let issued = false;
  let aborted = 0;
  const failures: string[] = [];
  const held = new Set<ServerResponse>();
  const tool = kind === 'approval' ? 'bash' : 'AskUserQuestion';
  const parameters = kind === 'approval' ? { command: CONTROL_COMMAND } : {
    questions: [{ header: 'Format', question: CONTROL_QUESTION,
      options: [{ label: 'Plain text', description: 'Readable text' }, { label: 'JSON', description: 'Structured data' }] }],
  };
  const server = http.createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on('data', (chunk: Buffer) => chunks.push(chunk));
    request.on('error', (error) => failures.push(`provider request: ${error.message}`));
    request.on('end', () => {
      if (request.method === 'GET' && request.url?.includes('/models')) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ object: 'list', data: [{ id: E2E_MODEL, object: 'model' }] }));
        return;
      }
      if (request.method !== 'POST' || !request.url?.includes('/chat/completions')) {
        failures.push(`unexpected provider route: ${request.method} ${request.url}`);
        response.writeHead(404); response.end(); return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>; }
      catch (error) { failures.push(`invalid provider JSON: ${String(error)}`); response.writeHead(400); response.end(); return; }
      if (body.stream !== true) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ id: 'history-control-probe', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      calls++;
      const names = new Set((Array.isArray(body.tools) ? body.tools : []).flatMap((entry) => {
        const name = (entry as { function?: { name?: string } }).function?.name;
        return name ? [name] : [];
      }));
      response.writeHead(200, { 'Content-Type': 'text/event-stream' });
      if (kind !== 'stream' && !issued) {
        if (!names.has(tool)) {
          if (searched || !names.has('ToolSearch')) {
            failures.push(`BLOCKED: actual ${tool} tool was not exposed by the real registry after ToolSearch — required by D-N2/T35`);
            response.end(chunk({ role: 'assistant', content: 'Required control tool is unavailable.' }, 'stop') + 'data: [DONE]\n\n');
            return;
          }
          searched = true;
          response.end(call('ToolSearch', { names: [tool] }, 'history-control-search') + chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
          return;
        }
        issued = true;
        response.end(call(tool, parameters, `history-control-${kind}`) + chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
        return;
      }
      // After a REAL human action (or immediately in stream-only mode), hold a
      // real provider call open so Stop must reach the genuinely running engine.
      held.add(response);
      let released = false;
      response.on('close', () => {
        held.delete(response);
        if (!released) aborted++;
      });
      Object.assign(response, { historyRelease: () => { released = true; } });
      response.write(chunk({ role: 'assistant', content: CONTROL_RUNNING }, null));
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('history control provider has no port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  function finish() {
    for (const response of held) {
      (response as ServerResponse & { historyRelease(): void }).historyRelease();
      response.end(chunk({ content: ' completed' }, 'stop') + 'data: [DONE]\n\n');
    }
    held.clear();
  }
  return { kind, url, calls: () => calls, failures: () => [...failures], aborted: () => aborted, heldCount: () => held.size, finishToolRound: finish,
    close: async () => { finish(); await new Promise<void>((resolve, reject) => { server.close((error) => error ? reject(error) : resolve()); server.closeAllConnections(); }); } };
}
function call(name: string, parameters: unknown, id: string) {
  return chunk({ tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(parameters) } }] }, null);
}
function chunk(delta: Record<string, unknown>, finishReason: string | null) {
  return 'data: ' + JSON.stringify({ id: 'history-control-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }] }) + '\n\n';
}
