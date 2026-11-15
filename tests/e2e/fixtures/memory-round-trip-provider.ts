import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// External OpenAI-compatible model protocol, NOT a gateway/SPA wire format.
interface ProviderMessage {
  role?: string;
  content?: unknown;
  tool_call_id?: string;
  tool_calls?: Array<{ id?: string; function?: { name?: string; arguments?: string } }>;
}

// not-wire-format: test-only controls for the external model edge.
interface MemoryScriptOptions {
  nonce: string;
  // Reordered tokens exercise indexed matching rather than substring fallback.
  recallQuery?: string;
  missingNonce: string;
  prompts: readonly [string, string, string];
}

/** Replace only the paid model, never a tool or gateway response. The first
 * two turns are remember -> acknowledgement, recall_memory -> result echo.
 * A third turn recalls a never-stored nonce as the in-gateway negative control.
 * Every received tool message (including repeated history) is recorded in full.
 */
export async function startMemoryRoundTripProvider(options: MemoryScriptOptions) {
  const evidence = {
    streamRequests: 0, probeRequests: 0, issuedCalls: 0,
    toolResults: [] as Array<{ request: number; callId: string; name: string; content: string }>,
    errors: [] as string[],
  };
  // Actual parsed HTTP bodies for local diagnostic attachments, never the
  // Authorization header. This recorder cannot supply the recall answer.
  const requests: unknown[] = [];
  // Argument shapes from pkg/tools/memory.go::{RememberTool,RecallMemoryTool}.Parameters.
  // Explicit shared room is required for remember, not an inferred default.
  // Positive recall omits room to exercise the real tool's default both scope.
  const turns = [
    { id: 'memory-scripted-remember', name: 'remember', args: { content: options.nonce, category: 'reference', room: 'shared' } },
    { id: 'memory-scripted-recall', name: 'recall_memory', args: { query: options.recallQuery ?? options.nonce } },
    { id: 'memory-scripted-missing', name: 'recall_memory', args: { query: options.missingNonce, room: 'shared' } },
  ];
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
        evidence.probeRequests += 1;
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
        // Catalog/onboarding checks must not advance the agent-loop script.
        evidence.probeRequests += 1;
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'memory-scripted-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      evidence.streamRequests += 1;
      requests.push(body);
      const messages = body.messages ?? [];
      const callsById = new Map(messages.flatMap((message) => message.tool_calls ?? []).map((call) => [call.id, call]));
      for (const result of messages.filter((message) => message.role === 'tool')) {
        evidence.toolResults.push({ request: evidence.streamRequests, callId: result.tool_call_id ?? '',
          name: callsById.get(result.tool_call_id)?.function?.name ?? 'UNKNOWN', content: textOf(result.content) });
      }
      const turnIndex = Math.floor((evidence.streamRequests - 1) / 2);
      const turn = turns[turnIndex];
      if (!turn) { reject(400, 'expected exactly six agent-loop requests'); return; }
      // The product appends request-only notes (including the clock) as
      // user-role messages. Find the latest scripted UI-turn marker instead
      // of treating those runtime notes as a new human prompt.
      let userIndex = messages.length - 1;
      while (userIndex >= 0 && (messages[userIndex].role !== 'user' ||
          !options.prompts.some((prompt) => textOf(messages[userIndex].content).includes(prompt)))) userIndex -= 1;
      if (userIndex < 0 || !textOf(messages[userIndex].content).includes(options.prompts[turnIndex])) {
        reject(400, `expected turn ${turnIndex + 1} user prompt`);
        return;
      }
      const current = messages.slice(userIndex + 1);
      if (evidence.streamRequests % 2 === 1) {
        if (!body.tools?.some((tool) => tool.function?.name === turn.name)) {
          reject(400, `${turn.name} was not offered to the agent`);
          return;
        }
        if (!['remember', 'recall_memory'].every((name) => body.tools?.some((tool) => tool.function?.name === name))) {
          reject(400, 'remember and recall_memory must both be offered');
          return;
        }
        if (current.some((message) => message.role === 'tool' || message.tool_calls?.length)) {
          reject(400, `expected turn ${turnIndex + 1} before its tool call`);
          return;
        }
        evidence.issuedCalls += 1;
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.end(chunk({ role: 'assistant', tool_calls: [{ index: 0, id: turn.id, type: 'function',
          function: { name: turn.name, arguments: JSON.stringify(turn.args) } }] }, null) +
          chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
        return;
      }
      const calls = current.flatMap((message) => message.role === 'assistant' ? message.tool_calls ?? [] : []);
      const results = current.filter((message) => message.role === 'tool');
      const call = calls[0];
      const result = results[0];
      const callIndex = current.findIndex((message) => message.tool_calls?.includes(call));
      if (calls.length !== 1 || call?.id !== turn.id || call.function?.name !== turn.name ||
          !sameArgs(call.function?.arguments, turn.args) || results.length !== 1 ||
          result?.tool_call_id !== turn.id || current.indexOf(result) <= callIndex) {
        reject(400, `expected the correlated ${turn.name} call and result for turn ${turnIndex + 1}`);
        return;
      }
      // #1800's exact positive shared-room success contract, not generic `ok`.
      if (turnIndex === 0 && textOf(result.content).trim() !== "ok — saved to your workspace team's shared memory") {
        reject(400, 'the remember tool result is not the shared-room success');
        return;
      }
      // Critical anti-false-green rule: only CURRENT correlated tool content
      // feeds the recall answer. No nonce lookup in the script, user text,
      // earlier tool results or the original tool-call arguments is allowed.
      const reply = turnIndex === 0 ? 'Memory stored.' : `Memory recall result:\n\n\`\`\`text\n${textOf(result.content)}\n\`\`\``;
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ role: 'assistant', content: reply }, 'stop') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('memory provider: no ephemeral port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return { url, evidence, requests, close: () => new Promise<void>((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
    server.closeAllConnections();
  }) };
}

function sameArgs(raw: string | undefined, expected: object): boolean {
  let actual: unknown;
  try { actual = JSON.parse(raw ?? 'null'); }
  catch { return false; }
  return actual !== null && typeof actual === 'object' &&
    Object.keys(actual).length === Object.keys(expected).length &&
    Object.entries(expected).every(([key, value]) => (actual as Record<string, unknown>)[key] === value);
}

function textOf(content: unknown): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  return content.map((part: { type?: string; text?: string }) => part.type === 'text' ? part.text ?? '' : '').join('');
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({
    id: 'memory-scripted-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
