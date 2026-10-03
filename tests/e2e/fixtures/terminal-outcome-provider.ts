import http from 'node:http';
import { E2E_MODEL } from './e2e-model';

export const TERMINAL_NARRATION = 'I will load the acceptance probe tool.';

// not-wire-format: fixture controls, not a gateway request/response type.
export interface TerminalOutcomeProvider {
  url: string;
  finishToolRound: () => void;
  calls: () => number;
  failures: () => string[];
  close: () => Promise<void>;
}

/** Script only the external provider. Hold its first streaming tool round AFTER
 * narration until the browser has observed it (and, in the offline case, lost
 * its real socket). No turn-engine, gateway, transcript or rendering stubs.
 * ToolSearch is real infrastructure; loading read_file is harmless and does
 * not actually read a file. Assertions in the caller prove it executed.
 */
export async function startTerminalOutcomeProvider(): Promise<TerminalOutcomeProvider> {
  let calls = 0;
  const failures: string[] = [];
  let release!: () => void;
  const finish = new Promise<void>((resolve) => { release = resolve; });
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('error', (err) => failures.push(`provider request: ${err.message}`));
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_MODEL, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        failures.push(`unexpected provider route: ${req.method} ${req.url}`);
        res.writeHead(404, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }));
        return;
      }
      let body: Record<string, unknown>;
      try {
        // External OpenAI-compatible protocol, not an Omnipus wire type.
        body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>;
      } catch (err) {
        failures.push(`invalid provider request JSON: ${String(err)}`);
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }));
        return;
      }
      if (body.stream !== true) {
        // Real onboarding validation is not a chat turn. Answer it honestly.
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'terminal-outcome-validation', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      calls += 1;
      const offered = Array.isArray(body.tools) && body.tools.some((tool: Record<string, unknown>) =>
        (tool.function as Record<string, unknown> | undefined)?.name === 'ToolSearch');
      if (calls !== 1 || !offered) {
        failures.push(`expected one streaming call with ToolSearch offered; calls=${calls}, offered=${offered}`);
        res.writeHead(500, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }));
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.write(chunk({ role: 'assistant', content: TERMINAL_NARRATION }, null));
      void finish.then(() => {
        res.end(
          chunk({ tool_calls: [{ index: 0, id: 'terminal-acceptance-call', type: 'function',
            function: { name: 'ToolSearch', arguments: JSON.stringify({ names: ['read_file'] }) } }] }, null) +
          chunk({}, 'tool_calls') + 'data: [DONE]\n\n',
        );
      });
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        reject(new Error('terminal provider: no ephemeral port'));
        return;
      }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return {
    url,
    finishToolRound: release,
    calls: () => calls,
    failures: () => [...failures],
    close: async () => {
      release();
      await new Promise<void>((resolve, reject) => {
        server.close((err) => err ? reject(err) : resolve());
        server.closeAllConnections();
      });
    },
  };
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({
    id: 'terminal-outcome-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    created: Math.floor(Date.now() / 1000),
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
