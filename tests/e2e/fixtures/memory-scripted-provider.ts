import http from 'node:http';
import { E2E_FALLBACK } from './e2e-model';

// External OpenAI-compatible protocol, NOT a gateway/SPA wire format.
interface ProviderMessage {
  role?: string;
  content?: unknown;
  tool_call_id?: string;
  tool_calls?: Array<{ id?: string; function?: { name?: string; arguments?: string } }>;
}

export const MEMORY_SCRIPT_IDS = {
  remember: 'memscript-remember',
  recall: 'memscript-recall',
  control: 'memscript-control',
} as const;

const NONCE_PATTERN = /MEMSCRIPT-[0-9a-z]+/;
/** The exact success text of pkg/tools/memory.go::rememberOutcomeMessage for a
 * shared-room write inside a workspace. Success is POSITIVELY required: any other
 * text (an error, a private-room fallback, a refusal) is rejected. */
export const REMEMBER_SHARED_SUCCESS = "ok \u2014 saved to your workspace team's shared memory";

interface Options {
  nonce: string;
  /** What turn 2 passes to recall_memory; defaults to the nonce. A reordered-token
   * query that is NOT a substring of the stored text only matches through the
   * index, so it tells the index lookup apart from the substring-scan fallback. */
  recallQuery?: string;
  controlNonce: string;
  rememberPrompt: string;
  recallPrompt: string;
  controlPrompt: string;
}

/** Script only the paid model edge for the memory round trip (Team B spec,
 * 11 Nov 16:54Z). Six requests, three turns:
 *   1/2  remember(nonce) -> its tool result must be a success -> "STORED-OK"
 *   3/4  recall_memory(nonce) -> the reply echoes the nonce FOUND IN THE TOOL
 *        RESULT (so the SPA can only show it if the real recall returned it),
 *        or "NOTHING-FOUND" when the result carries no nonce
 *   5/6  recall_memory(controlNonce, never stored) -> must come back empty.
 * The real agent loop runs remember/recall_memory against the real memory store
 * and bleve index; nothing about storage, search or rendering is faked. */
export async function startMemoryScriptedProvider(options: Options) {
  const evidence = {
    streamRequests: 0,
    toolResults: [] as Array<{ request: number; callId: string; name: string; content: string }>,
    errors: [] as string[],
  };
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    const reject = (status: number, message: string) => {
      evidence.errors.push(message);
      res.writeHead(status, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message } }));
    };
    const toolCall = (id: string, name: string, args: object) => {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ role: 'assistant', tool_calls: [{ index: 0, id, type: 'function',
        function: { name, arguments: JSON.stringify(args) } }] }, null) +
        chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
    };
    const say = (text: string) => {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ role: 'assistant', content: text }, 'stop') + 'data: [DONE]\n\n');
    };
    req.on('data', (c: Buffer) => chunks.push(c));
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
          id: 'memscript-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      evidence.streamRequests += 1;
      const n = evidence.streamRequests;
      if (n > 6) { reject(400, 'expected exactly six agent-loop requests'); return; }
      const messages = body.messages ?? [];
      const offeredNames = new Set((body.tools ?? []).map((t) => t.function?.name));
      const prompts = [options.rememberPrompt, options.rememberPrompt, options.recallPrompt,
        options.recallPrompt, options.controlPrompt, options.controlPrompt];
      const users = messages.filter((m) => m.role === 'user').map((m) => textOf(m.content));
      if (!users.some((text) => text.includes(prompts[n - 1]))) {
        reject(400, `request ${n}: no user message contains its scripted prompt`);
        return;
      }
      // The newest scripted prompt in the history must be this turn's: an older
      // turn's prompt cannot stand in for it. (Gateway context messages carry no
      // scripted prompt, so they do not count.)
      const scriptedUsers = users.filter((text) => [...new Set(prompts)].some((p) => text.includes(p)));
      if (!scriptedUsers[scriptedUsers.length - 1]?.includes(prompts[n - 1])) {
        reject(400, `request ${n}: the newest scripted user prompt is not this turn's`);
        return;
      }
      if (n % 2 === 1) {
        // Odd requests open a turn: the call is produced here, so both tools must be offered.
        if (!offeredNames.has('remember') || !offeredNames.has('recall_memory')) {
          reject(400, `request ${n}: remember and recall_memory must both be offered`);
          return;
        }
        if (n === 1) {
          if (messages.some((m) => m.role === 'tool' || m.tool_calls?.length)) {
            reject(400, 'request 1: expected a fresh chat without tool history');
            return;
          }
        }
        if (n === 1) toolCall(MEMORY_SCRIPT_IDS.remember, 'remember', { content: options.nonce, category: 'reference', room: 'shared' });
        else if (n === 3) toolCall(MEMORY_SCRIPT_IDS.recall, 'recall_memory', { query: options.recallQuery ?? options.nonce });
        else toolCall(MEMORY_SCRIPT_IDS.control, 'recall_memory', { query: options.controlNonce });
        return;
      }
      const id = n === 2 ? MEMORY_SCRIPT_IDS.remember : n === 4 ? MEMORY_SCRIPT_IDS.recall : MEMORY_SCRIPT_IDS.control;
      const name = n === 2 ? 'remember' : 'recall_memory';
      const expectedArgs: Record<string, string> = n === 2 ? { content: options.nonce, category: 'reference', room: 'shared' }
        : { query: n === 4 ? options.recallQuery ?? options.nonce : options.controlNonce };
      const callIndex = messages.findIndex((m) => m.role === 'assistant' && m.tool_calls?.some((c) => c.id === id &&
        c.function?.name === name && sameJson(c.function?.arguments, expectedArgs)));
      const result = messages.find((m) => m.role === 'tool' && m.tool_call_id === id);
      const content = textOf(result?.content);
      if (callIndex < 0 || !result || messages.indexOf(result) <= callIndex || !content.trim()) {
        reject(400, `request ${n}: expected the correlated ${name} tool result after its call`);
        return;
      }
      evidence.toolResults.push({ request: n, callId: id, name, content });
      if (n === 2) {
        if (content.trim() !== REMEMBER_SHARED_SUCCESS) {
          reject(400, 'request 2: the remember tool result is not a success');
          return;
        }
        say('STORED-OK');
        return;
      }
      const found = NONCE_PATTERN.exec(content)?.[0];
      say(found ? `RECALLED:${found}` : 'NOTHING-FOUND');
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
  return {
    url, evidence,
    close: () => new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}

/** Same keys and values, in any key order (the gateway re-serializes arguments). */
function sameJson(raw: string | undefined, expected: Record<string, string>): boolean {
  try {
    const got = JSON.parse(raw ?? 'null') as Record<string, unknown> | null;
    if (!got || typeof got !== 'object') return false;
    const keys = Object.keys(expected);
    return Object.keys(got).length === keys.length && keys.every((key) => got[key] === expected[key]);
  } catch { return false; }
}

function textOf(content: unknown): string {
  if (typeof content === 'string') return content;
  return Array.isArray(content)
    ? content.map((part: { type?: string; text?: string }) => part.type === 'text' ? part.text ?? '' : '').join('')
    : '';
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({
    id: 'memscript-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }],
  }) + '\n\n';
}
