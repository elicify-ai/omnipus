import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// External OpenAI-compatible protocol, NOT a gateway/SPA wire format.
interface ProviderMessage {
  role?: string;
  content?: unknown;
  tool_call_id?: string;
  tool_calls?: Array<{ id?: string; function?: { name?: string; arguments?: string } }>;
}

export const MEDIA_DOWNLOAD_CALL_ID = 'media-download-send-file';

/** Script only the paid model edge, following terminal-outcome-provider's SSE
 * pattern. The real agent must execute send_file and return its correlated tool
 * result on request TWO before this provider will say SENT. No file bytes,
 * gateway frames, tool execution, MediaStore or download responses are faked. */
export async function startMediaDownloadProvider(options: { prompt: string; relativePath: string }) {
  const evidence = {
    streamRequests: 0,
    issuedCalls: 0,
    toolResults: [] as Array<{ request: number; callId: string; name: string; content: string }>,
    errors: [] as string[],
  };
  const args = { path: options.relativePath };
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
      let body: { stream?: boolean; messages?: ProviderMessage[]; tools?: Array<{ function?: { name?: string } }> };
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as typeof body; }
      catch { reject(400, 'invalid provider JSON'); return; }
      if (body.stream !== true) {
        // Onboarding probes are not agent-loop requests and consume no script.
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'media-download-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      evidence.streamRequests += 1;
      const messages = body.messages ?? [];
      if (!messages.some((message) => message.role === 'user' && textOf(message.content).includes(options.prompt))) {
        reject(400, 'expected the media download user prompt');
        return;
      }
      if (evidence.streamRequests === 1) {
        if (!body.tools?.some((tool) => tool.function?.name === 'send_file')) {
          reject(400, 'send_file was not offered to the agent');
          return;
        }
        if (messages.some((message) => message.role === 'tool' || message.tool_calls?.length)) {
          reject(400, 'expected a fresh chat without tool history');
          return;
        }
        evidence.issuedCalls += 1;
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.end(chunk({ role: 'assistant', tool_calls: [{ index: 0, id: MEDIA_DOWNLOAD_CALL_ID, type: 'function',
          function: { name: 'send_file', arguments: JSON.stringify(args) } }] }, null) +
          chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
        return;
      }
      if (evidence.streamRequests !== 2 || evidence.issuedCalls !== 1) {
        reject(400, 'expected exactly two agent-loop requests');
        return;
      }
      const assistantIndex = messages.findIndex((message) => message.role === 'assistant' && message.tool_calls?.length);
      const calls = messages[assistantIndex]?.tool_calls ?? [];
      const call = calls[0];
      let returnedArgs: unknown;
      try { returnedArgs = JSON.parse(call?.function?.arguments ?? 'null'); }
      catch { reject(400, 'expected the original send_file call and path'); return; }
      if (calls.length !== 1 || call?.id !== MEDIA_DOWNLOAD_CALL_ID || call.function?.name !== 'send_file' ||
          JSON.stringify(returnedArgs) !== JSON.stringify(args)) {
        reject(400, 'expected the original send_file call and path');
        return;
      }
      const results = messages.filter((message) => message.role === 'tool');
      const result = results[0];
      const content = textOf(result?.content);
      if (results.length !== 1 || result?.tool_call_id !== MEDIA_DOWNLOAD_CALL_ID || !content.trim() ||
          messages.indexOf(result) <= assistantIndex) {
        reject(400, 'expected the correlated send_file tool result after its call');
        return;
      }
      evidence.toolResults.push({ request: evidence.streamRequests, callId: result.tool_call_id,
        name: call.function.name, content });
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ role: 'assistant', content: 'SENT' }, 'stop') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('media provider: no ephemeral port')); return; }
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
    id: 'media-download-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
