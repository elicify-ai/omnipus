import http from 'node:http';
import { E2E_MODEL } from '../e2e-model';

// D-U2 frozen provider totals. These are authored inputs, not sampled counters.
export const EARLIER_TOKENS = 1_200;
export const CURRENT_TOKENS = 45_123;
export const SESSION_TOKENS = 46_323;
export const NEXT_TOKENS = 700;
export const NEXT_SESSION_TOKENS = 47_023;
export const BASELINE_INPUT = 'D16 baseline: execute the completed harmless command.';
export const USAGE_INPUT = 'D16 usage: account the completed 45123-token reply.';
export const NEXT_INPUT = 'D16 advance: account the completed 700-token reply.';
export const CANCEL_INPUT = 'D16 cancel: execute the blocking harmless command and wait for Stop.';
export const COMPLETED_ID = 'd16-completed-native-call';
export const CANCELLED_ID = 'd16-cancelled-native-call';
export const COMPLETED_MARKER = 'd16-completed-executions.log';
export const CANCELLED_MARKER = 'd16-cancelled-executions.log';
export const RUNNING_MARKER = 'd16-running-process.json';
export const COMPLETED_COMMAND = `printf 'once\\n' >> ${COMPLETED_MARKER}; printf D16_COMPLETED_TOOL_OUTPUT`;
const blockedPython = [
  'from pathlib import Path',
  'import json, os, threading',
  `with Path("${CANCELLED_MARKER}").open("a", encoding="utf8") as receipt:`,
  '    receipt.write("once\\n")',
  `Path("${RUNNING_MARKER}.tmp").write_text(json.dumps({"pid": os.getpid(), "cwd": os.getcwd()}), encoding="utf8")`,
  `os.replace("${RUNNING_MARKER}.tmp", "${RUNNING_MARKER}")`, // Publish a complete receipt atomically.
  'threading.Event().wait()', // Event barrier, not a sleep or elapsed-time oracle.
].join('\n');
export const CANCELLED_COMMAND = `python3 -I -u -c '${blockedPython.replaceAll("'", "'\\''")}'`;

// not-wire-format: external provider receipts and fixture controls only.
export type ReloadProvider = {
  url: string;
  receipts: { stage: string; tokens: number; callId?: string; tool?: string }[];
  failures: string[];
  close(): Promise<void>;
};

/** The ONLY scripted boundary is external model HTTP. Native tools, Stop,
 * gateway frames, canonical persistence and the complete UI stay real. */
export async function startReloadProvider(): Promise<ReloadProvider> {
  const receipts: ReloadProvider['receipts'] = [];
  const failures: string[] = [];
  let searched = false;
  let completed = false;
  let blocked = false;
  const server = http.createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on('data', (chunk: Buffer) => chunks.push(chunk));
    request.on('error', error => failures.push(`provider request: ${error.message}`));
    request.on('end', () => {
      if (request.method === 'GET' && request.url?.includes('/models')) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ data: [{ id: E2E_MODEL, object: 'model' }] }));
        return;
      }
      if (request.method !== 'POST' || !request.url?.includes('/chat/completions')) {
        failures.push(`unexpected provider route: ${request.method} ${request.url}`);
        response.writeHead(404); response.end(); return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>; }
      catch (error) { failures.push(String(error)); response.writeHead(400); response.end(); return; }
      if (body.stream !== true) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ id: 'd16-probe', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 } }));
        return;
      }
      const messages = Array.isArray(body.messages) ? body.messages as Record<string, unknown>[] : [];
      const user = [...messages].reverse().find(message => message.role === 'user');
      const input = typeof user?.content === 'string' ? user.content : Array.isArray(user?.content)
        ? (user.content as { text?: string }[]).map(part => part.text ?? '').join('') : '';
      const names = new Set((Array.isArray(body.tools) ? body.tools as { function?: { name?: string } }[] : [])
        .map(tool => tool.function?.name));
      response.writeHead(200, { 'Content-Type': 'text/event-stream' });
      const finish = (stage: string, tokens: number, delta: Record<string, unknown>, callId?: string, tool?: string) => {
        receipts.push({ stage, tokens, ...(callId ? { callId, tool } : {}) });
        response.end(chunk(delta) + chunk({}, callId ? 'tool_calls' : 'stop') + usage(tokens) + 'data: [DONE]\n\n');
      };
      if (input === BASELINE_INPUT || input === CANCEL_INPUT) {
        if (!names.has('bash')) {
          if (searched || !names.has('ToolSearch')) {
            failures.push('PREREQUISITE: real registry did not expose bash after ToolSearch');
            finish('missing-native-tool', 0, { content: 'Native tool unavailable.' }); return;
          }
          searched = true;
          finish('discover', 0, call('ToolSearch', { names: ['bash'] }, 'd16-discover-native'), 'd16-discover-native', 'ToolSearch');
          return;
        }
        if (input === BASELINE_INPUT) {
          if (!completed) {
            completed = true;
            finish('completed-native', 0, call('bash', { command: COMPLETED_COMMAND }, COMPLETED_ID), COMPLETED_ID, 'bash');
          } else finish('baseline', EARLIER_TOKENS, { role: 'assistant', content: 'D16 completed native command and recorded baseline.' });
          return;
        }
        if (!blocked) {
          blocked = true;
          finish('blocked-native', 0, call('bash', { command: CANCELLED_COMMAND, timeout_seconds: 300 }, CANCELLED_ID), CANCELLED_ID, 'bash');
          return;
        }
        failures.push('unexpected model resume after the blocked native call; Stop must not rerun or finish it');
        finish('unexpected-resume', 0, { content: 'Unexpected continuation.' }); return;
      }
      if (input === USAGE_INPUT) {
        finish('usage', CURRENT_TOKENS, { role: 'assistant', content: 'D16 recorded the completed current-turn usage.' }); return;
      }
      if (input === NEXT_INPUT) {
        finish('next', NEXT_TOKENS, { role: 'assistant', content: 'D16 recorded the next completed turn once.' }); return;
      }
      failures.push(`unexpected streaming request, last user input=${JSON.stringify(input)}`);
      finish('unexpected-input', 0, { content: 'Unexpected input.' });
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') return reject(new Error('provider has no ephemeral port'));
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return { url, receipts, failures, close: async () => {
    await new Promise<void>((resolve, reject) => { server.close(error => error ? reject(error) : resolve()); server.closeAllConnections(); });
  } };
}
function call(name: string, args: unknown, id: string) {
  return { role: 'assistant', tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] };
}
function chunk(delta: Record<string, unknown>, finishReason: string | null = null) {
  return 'data: ' + JSON.stringify({ id: 'd16-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    choices: [{ index: 0, delta, finish_reason: finishReason }] }) + '\n\n';
}
function usage(tokens: number) {
  return 'data: ' + JSON.stringify({ id: 'd16-stream', object: 'chat.completion.chunk', model: E2E_MODEL, choices: [],
    usage: { prompt_tokens: Math.max(0, tokens - 1), completion_tokens: tokens ? 1 : 0, total_tokens: tokens } }) + '\n\n';
}
