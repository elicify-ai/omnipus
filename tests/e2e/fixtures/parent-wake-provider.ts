import http from 'node:http';
import { E2E_MODEL } from './e2e-model';

export const PARENT_WAKE_TASK = 'Reply with the single word HELLO_HELPER and then stop.';
export const PARENT_WAKE_HELPER = 'HELLO_HELPER';
export const PARENT_WAKE_PARTIAL = 'WOKEN_PARTIAL';

// not-wire-format: fixture controls for the scripted model, not an Omnipus wire type.
export interface ParentWakeProvider {
  url: string;
  setAgentId: (id: string) => void;
  releaseWake: () => void;
  failures: () => string[];
  close: () => Promise<void>;
}

/**
 * Scripted OpenAI-compatible provider for Test 26.
 * The first request that offers `delegate` launches one helper.
 * The helper's own request answers HELLO_HELPER.
 * The later parent request whose prompt already contains that word is the
 * wake turn: it emits one partial token and holds until releaseWake().
 */
export async function startParentWakeProvider(): Promise<ParentWakeProvider> {
  let agentId = '';
  let delegated = false;
  let helperDone = false;
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  const failures: string[] = [];

  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
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
        body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>;
      } catch (err) {
        failures.push(`invalid provider request JSON: ${String(err)}`);
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }));
        return;
      }
      if (body.stream !== true) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'parent-wake-validation', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
        }));
        return;
      }
      const names = toolNames(body.tools);
      const blob = JSON.stringify(body.messages ?? []);
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      if (!delegated && names.includes('delegate')) {
        if (agentId === '') {
          failures.push('delegate was offered before the test set the agent id');
        }
        delegated = true;
        res.end(
          chunk({ tool_calls: [{ index: 0, id: 'parent-wake-delegate', type: 'function', function: {
            name: 'delegate',
            arguments: JSON.stringify({
              action: 'run',
              agent_id: agentId,
              label: 'hello helper',
              task: PARENT_WAKE_TASK,
            }),
          } }] }, null) +
          chunk({}, 'tool_calls') + 'data: [DONE]\n\n',
        );
        return;
      }
      if (blob.includes(PARENT_WAKE_HELPER)) {
        res.write(chunk({ role: 'assistant', content: PARENT_WAKE_PARTIAL }, null));
        void held.then(() => {
          res.end(chunk({ content: ' and the rest' }, 'stop') + 'data: [DONE]\n\n');
        });
        return;
      }
      if (!helperDone) {
        helperDone = true;
        res.end(chunk({ role: 'assistant', content: PARENT_WAKE_HELPER }, 'stop') + 'data: [DONE]\n\n');
        return;
      }
      res.end(chunk({ role: 'assistant', content: 'ok' }, 'stop') + 'data: [DONE]\n\n');
    });
  });

  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        reject(new Error('parent-wake provider: no ephemeral port'));
        return;
      }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });

  return {
    url,
    setAgentId: (id: string) => { agentId = id; },
    releaseWake: release,
    failures: () => [...failures],
    close: async () => {
      release();
      await new Promise<void>((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
        server.closeAllConnections();
      });
    },
  };
}

function toolNames(tools: unknown): string[] {
  if (!Array.isArray(tools)) return [];
  return tools.flatMap((tool) => {
    const name = (tool as { function?: { name?: string } }).function?.name;
    return name ? [name] : [];
  });
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({
    id: 'parent-wake-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    created: Math.floor(Date.now() / 1000),
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
