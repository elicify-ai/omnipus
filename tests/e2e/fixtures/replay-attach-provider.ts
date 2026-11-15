import http from 'node:http';
import type { ServerResponse } from 'node:http';
import { setTimeout as delay } from 'node:timers/promises';
import { E2E_FALLBACK } from './e2e-model';

// Controlled stimulus for Scenario 9 / TDD row 25, not text a live model must obey.
// A single plain paragraph permits exact DOM text comparison without Markdown normalization.
export const REPLAY_ATTACH_PREFIX = 'REPLAY-1613-BEGIN ';
export const REPLAY_ATTACH_CHUNKS = [
  ...Array.from({ length: 24 }, (_, index) => `segment-${String(index + 1).padStart(2, '0')} `),
  'REPLAY-1613-END.',
];
export const REPLAY_ATTACH_TEXT = REPLAY_ATTACH_PREFIX + REPLAY_ATTACH_CHUNKS.join('');
// 25 delayed chunks (1.25 s total) exercise actual streaming. Gates, not this
// interval or an elapsed-time assertion, establish the active-attach ordering.
const CHUNK_DELAY_MS = 50;

// not-wire-format: local fixture controls, not a gateway/SPA protocol.
export type ReplayAttachProvider = {
  url: string;
  releaseChunks(): void;
  finish(): void;
  state(): { calls: number; emittedChunks: number; finished: boolean };
  failures(): string[];
  close(): Promise<void>;
};

/** Fake only the external provider; the real engine creates all gateway frames. */
export async function startReplayAttachProvider(): Promise<ReplayAttachProvider> {
  let calls = 0;
  let emittedChunks = 0;
  let finished = false;
  const failures: string[] = [];
  let releaseChunks!: () => void;
  let finish!: () => void;
  const chunksHeld = new Promise<void>(resolve => { releaseChunks = resolve; });
  const finishHeld = new Promise<void>(resolve => { finish = resolve; });

  async function stream(response: ServerResponse) {
    response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
    response.write(chunk({ role: 'assistant', content: REPLAY_ATTACH_PREFIX }, null));
    emittedChunks++;
    await chunksHeld;
    for (const content of REPLAY_ATTACH_CHUNKS) {
      await delay(CHUNK_DELAY_MS);
      if (response.destroyed) return;
      response.write(chunk({ content }, null));
      emittedChunks++;
    }
    await finishHeld;
    if (response.destroyed) return;
    finished = true;
    response.end(chunk({}, 'stop') + 'data: [DONE]\n\n');
  }

  const server = http.createServer((request, response) => {
    const buffers: Buffer[] = [];
    request.on('data', (buffer: Buffer) => buffers.push(buffer));
    request.on('error', error => failures.push(`provider request: ${error.message}`));
    request.on('end', () => {
      const reject = (status: number, message: string) => {
        failures.push(message);
        response.writeHead(status, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ error: { message } }));
      };
      if (request.method === 'GET' && /^\/(v1\/)?models$/.test(request.url ?? '')) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (request.method !== 'POST' || !/^\/(v1\/)?chat\/completions$/.test(request.url ?? '')) {
        reject(404, `unexpected provider route: ${request.method} ${request.url}`);
        return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(buffers).toString('utf8')) as Record<string, unknown>; }
      catch { reject(400, 'invalid provider request JSON'); return; }
      if (body.stream !== true) {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ id: 'replay-attach-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      if (calls !== 0) { reject(409, 'unexpected second streaming request'); return; }
      calls++;
      void stream(response).catch(error => {
        failures.push(`provider stream: ${String(error)}`);
        response.destroy(error instanceof Error ? error : new Error(String(error)));
      });
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') { reject(new Error('replay attach provider has no port')); return; }
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return {
    url, releaseChunks, finish,
    state: () => ({ calls, emittedChunks, finished }),
    failures: () => [...failures],
    close: async () => {
      releaseChunks(); finish();
      await new Promise<void>((resolve, reject) => {
        server.close(error => error ? reject(error) : resolve());
        server.closeAllConnections();
      });
    },
  };
}

function chunk(delta: Record<string, unknown>, finishReason: string | null): string {
  return 'data: ' + JSON.stringify({ id: 'replay-attach-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    choices: [{ index: 0, delta, finish_reason: finishReason }] }) + '\n\n';
}
