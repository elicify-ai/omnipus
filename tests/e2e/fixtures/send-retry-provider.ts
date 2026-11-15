import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// External OpenAI-compatible protocol, NOT a gateway/SPA wire format.
interface ProviderMessage {
  role?: string;
  content?: unknown;
  tool_calls?: unknown[];
}

export const SEND_RETRY_FIRST_REPLY = 'FIRST-REPLY-OK';
export const SEND_RETRY_RESEND_REPLY = 'RESEND-REPLY-OK';

/** Script only the paid model edge for open-in-chat (b) (#1792). Two text-only
 * answers, in order: request ONE must carry `first` in a user message
 * and gets FIRST-REPLY-OK; request TWO must carry `second` (the resent text) in one
 * and gets RESEND-REPLY-OK. Anything else is refused with a 400 and recorded.
 * No tools are offered or called, so a live model's exploring turn (memory
 * recall, bash, ...) cannot stand in for the short reply. The one-shot failed
 * send never reaches the provider, so exactly two stream requests prove it was
 * dropped once and resent once. */
export async function startSendRetryProvider(options: { first: string; second: string }) {
  const evidence = {
    streamRequests: 0,
    lastUserTexts: [] as string[],
    errors: [] as string[],
  };
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    const reject = (status: number, message: string) => {
      evidence.errors.push(message);
      res.writeHead(status, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message } }));
    };
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('error', (error) => evidence.errors.push(`provider input: ${error.message}`));
    req.on('end', () => {
      if (req.method === 'GET' && /\/(?:v1\/)?models$/.test(req.url ?? '')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !/\/(?:v1\/)?chat\/completions$/.test(req.url ?? '')) {
        reject(404, `unexpected provider route: ${req.method} ${req.url}`);
        return;
      }
      let body: { stream?: boolean; messages?: ProviderMessage[] };
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as typeof body; }
      catch { reject(400, 'invalid provider JSON'); return; }
      if (body.stream !== true) {
        // Onboarding probes are not agent-loop requests and consume no script.
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'send-retry-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      evidence.streamRequests += 1;
      const users = (body.messages ?? []).filter((message) => message.role === 'user').map((message) => textOf(message.content));
      if (evidence.streamRequests > 2) {
        reject(400, 'expected exactly two agent-loop requests');
        return;
      }
      const expected = evidence.streamRequests === 1 ? options.first : options.second;
      // The gateway may add its own user-role context around the prompt, so the
      // prompt is looked for in the LAST user message that carries it.
      const carrying = users.filter((text) => text.includes(expected));
      evidence.lastUserTexts.push(carrying[carrying.length - 1] ?? '');
      if (carrying.length === 0) {
        reject(400, `request ${evidence.streamRequests}: no user message contains its scripted prompt`);
        return;
      }
      const reply = evidence.streamRequests === 1 ? SEND_RETRY_FIRST_REPLY : SEND_RETRY_RESEND_REPLY;
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ role: 'assistant', content: reply }, 'stop') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('send-retry provider: no ephemeral port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return {
    url, evidence,
    close: () => new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}

function textOf(content: unknown): string {
  if (typeof content === 'string') return content;
  return Array.isArray(content)
    ? content.map((part: { type?: string; text?: string }) => part.type === 'text' ? part.text ?? '' : '').join('')
    : '';
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({
    id: 'send-retry-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
