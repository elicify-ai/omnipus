// held-stream-provider.ts — the deterministic mid-turn signal for the #1661
// rewritten tests (issue #1661, planning-20261106/LLM-AGENTS-RED-TRIAGE.md).
//
// Nine e2e tests used to establish "a turn is genuinely mid-flight" by waiting
// for >80 characters of answer text to become visible while the turn is still
// running. On the CI primary model (see tests/e2e/e2e-model.json) that
// window never opens: the model thinks invisibly for seconds and then
// delivers the whole answer inside ~430ms, so visible text coincides with
// completion and the mid-turn gate fails every attempt. The gateway streams
// correctly; the assumption about the MODEL's emission profile is what broke.
//
// This fixture removes that dependency: it scripts ONLY the external
// OpenAI-compatible provider behind an isolated GatewayProcess (the same
// apiBase seam as agent-grep, T25's stop-label provider and parent-wake) and
// holds the scripted stream open — writing a small text delta every
// chunkIntervalMs, never finishing on its own — until the test commands
// release(). The turn's mid-flight state is then provable from evidence that
// cannot coincide with completion:
//
//   streamRequests === 1   the real turn reached the scripted provider;
//   chunksSent    >= 2     real deltas flowed through the real gateway to the UI;
//   streamsClosed === 0    the stream is STILL OPEN — the turn cannot be done;
//   released     === 0     the fixture did not end it either.
//
// Whatever the test does next (Stop, /cancel, Escape, freeze+reload, tab
// switch, SIGKILL, offline blip) happens against a turn that is provably
// still running, and the original assertions afterwards still decide the
// verdict. A cancel that does nothing leaves the stream open forever (the
// stop button never clears); a catch-up that replays nothing fails the
// prefix/completeness assertions; a reconnect that drops the live tail
// strands the turn. The tests keep their original teeth.
//
// Browser, gateway, turn engine, persistence, catch-up and cancellation stay
// real; only the external model is scripted. Not-wire-format: the SSE bodies
// below are the external OpenAI-compatible chat protocol, not an Omnipus
// contract type.

import http from 'node:http';
import { expect, type TestInfo } from '@playwright/test';
import { E2E_FALLBACK } from './e2e-model';

/** Evidence a spec asserts on — the model-independent mid-turn signal. */
export interface HeldStreamEvidence {
  /** Matching streaming requests received (the held one + any scripted retry). */
  streamRequests: number;
  /** Streaming requests whose prompt did NOT match promptIncludes. */
  unmatchedStreams: number;
  /** Scripted text deltas written onto held streams. */
  chunksSent: number;
  /** Held streams that stopped streaming (release(), a real cancel, a crash). */
  streamsClosed: number;
  /** Held streams finished by release() — must stay 0 where only a real
   * cancel may close the stream. */
  released: number;
  /** Matching streaming requests answered complete instead of held (e.g. the
   * "Generate again" retry after catchup scenario h's restart). */
  retried: number;
  /** Unexpected or failed scripted-provider traffic. */
  errors: string[];
}

// not-wire-format: a test fixture handle, not an Omnipus wire type.
export interface HeldStreamProvider {
  readonly url: string;
  readonly evidence: HeldStreamEvidence;
  /** Finish the held stream deterministically (finish chunk + [DONE]). A
   * no-op when nothing is held (already cancelled/crashed/released). */
  release(): void;
  /** The exact answer the SPA must render for the held turn: the numbered
   * delta sequence plus the release text once released — whitespace-
   * normalized the same way fixtures/assistant-text.ts normalizes. Lets a
   * test pin byte-continuity of catch-up (no dropped or duplicated delta)
   * despite the prose being repetitive. */
  expectedHeldAnswer(): string;
  /** Stop the HTTP server and tear down remaining connections. */
  close(): Promise<void>;
}

export interface HeldStreamOptions {
  /** Substring identifying the user prompt of the turn to hold (the same
   * matching rule T25's stop-label provider uses: any user message that
   * includes it, position-independent — the gateway may append its own
   * system-time user message after the person's). */
  promptIncludes: string;
  /** Milliseconds between scripted deltas while held. Default 150. */
  chunkIntervalMs?: number;
  /** Text of each scripted delta. Default: one steady sentence. */
  chunkText?: string;
  /** Text of the final delta written by release(). */
  releaseText?: string;
}

interface HeldStream {
  res: http.ServerResponse;
  timer: NodeJS.Timeout;
  finished: boolean;
}

/**
 * Start a scripted OpenAI-compatible provider that holds the matching turn's
 * stream open until release(). One provider instance serves one test; the
 * FIRST matching streaming request is held, later matching ones (a retry
 * turn, e.g. catchup h's "Generate again" after the gateway restart) are
 * answered complete, and anything else is answered complete but recorded in
 * errors/unmatchedStreams so evidence exposes it instead of hanging a turn.
 */
export async function startHeldStreamProvider(opts: HeldStreamOptions): Promise<HeldStreamProvider> {
  const chunkIntervalMs = opts.chunkIntervalMs ?? 150;
  const chunkText = opts.chunkText ?? 'The tide keeps its own steady rhythm in this scripted stream.';
  const releaseText = opts.releaseText ?? ' The scripted stream now finishes.';
  const retryAnswer =
    'The scripted retry answer completes without holding. The held turn was already finished before this request arrived.';

  // Each scripted delta is UNIQUELY numbered. A periodic delta stream cannot
  // prove catch-up byte-continuity — a dropped middle chunk is invisible in
  // repeated prose — so every delta carries its own sequence number and
  // expectedHeldAnswer() reconstructs the exact answer the SPA must render.
  const deltaText = (n: number): string => `${chunkText} (part ${n})`;

  const evidence: HeldStreamEvidence = {
    streamRequests: 0,
    unmatchedStreams: 0,
    chunksSent: 0,
    streamsClosed: 0,
    released: 0,
    retried: 0,
    errors: [],
  };
  let held: HeldStream | null = null;

  // External OpenAI-compatible protocol, not a gateway/SPA wire format.
  const chunk = (delta: Record<string, unknown>, finish: string | null) =>
    'data: ' + JSON.stringify({
      id: 'held-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
      created: Math.floor(Date.now() / 1000),
      choices: [{ index: 0, delta, finish_reason: finish }],
    }) + '\n\n';

  const completeNow = (res: http.ServerResponse, content: string) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(chunk({ role: 'assistant', content }, 'stop') + 'data: [DONE]\n\n');
  };

  // A scripted completion that still STREAMS at the fixture's cadence: the
  // tests pin the running row while a response renders (catchup h's
  // "Generate again" assertions observe the retry mid-stream), so a retry
  // must not arrive in one burst — that would make the running state
  // unobservable and the original assertion unsatisfiable.
  const completePaced = (res: http.ServerResponse, content: string) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const parts = content.match(/.{1,40}(\s|$)/g) ?? [content];
    let i = 0;
    const timer = setInterval(() => {
      if (i < parts.length - 1) {
        res.write(chunk({ content: parts[i] }, null));
        i += 1;
        return;
      }
      clearInterval(timer);
      res.end(chunk({ content: parts[i] }, 'stop') + 'data: [DONE]\n\n');
    }, chunkIntervalMs);
    res.once('close', () => clearInterval(timer));
  };

  const server = http.createServer((req, res) => {
    const bodyChunks: Buffer[] = [];
    req.on('data', (data: Buffer) => bodyChunks.push(data));
    req.on('error', (error) => evidence.errors.push(`provider request: ${error.message}`));
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        evidence.errors.push(`unexpected provider route: ${req.method} ${req.url}`);
        res.writeHead(404, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'unexpected provider route' } }));
        return;
      }
      let body: Record<string, unknown>;
      try {
        body = JSON.parse(Buffer.concat(bodyChunks).toString('utf8')) as Record<string, unknown>;
      } catch (error) {
        evidence.errors.push(`invalid provider request JSON: ${String(error)}`);
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'invalid provider request JSON' } }));
        return;
      }
      if (body.stream !== true) {
        // Onboarding/key validation does not start the held stream.
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'held-stream-validation', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      const messages = Array.isArray(body.messages) ? body.messages as Record<string, unknown>[] : [];
      const hasPrompt = messages.some((message) => {
        if (message.role !== 'user') return false;
        const content = message.content;
        const text = typeof content === 'string' ? content : Array.isArray(content) ?
          content.map((part: Record<string, unknown>) => part.type === 'text' ? part.text : '').join('') : '';
        return text.includes(opts.promptIncludes);
      });
      evidence.streamRequests += 1;
      if (!hasPrompt) {
        // Answer complete so no turn can hang on the fixture; evidence flags it.
        evidence.unmatchedStreams += 1;
        evidence.errors.push(`unexpected streaming prompt (only the scripted prompt is held)`);
        completeNow(res, 'FIXTURE_ERROR: unexpected streaming prompt');
        return;
      }
      if (held !== null || evidence.streamRequests > 1) {
        // A later scripted turn (retry/regenerate): answer complete, do not
        // hold — but stream it at cadence so the retry stays observable
        // mid-flight.
        evidence.retried += 1;
        completePaced(res, retryAnswer);
        return;
      }
      // THE HELD STREAM: deltas on a timer, no finish chunk, no [DONE] — the
      // turn cannot complete until release() or a real teardown (cancel/crash).
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      const stream: HeldStream = {
        res,
        timer: setInterval(() => {
          res.write(chunk({ content: deltaText(evidence.chunksSent + 1) }, null));
          evidence.chunksSent += 1;
        }, chunkIntervalMs),
        finished: false,
      };
      res.once('close', () => {
        clearInterval(stream.timer);
        if (!stream.finished) {
          stream.finished = true;
          evidence.streamsClosed += 1;
        }
        if (held === stream) held = null;
      });
      res.on('error', (error) => evidence.errors.push(`provider response: ${error.message}`));
      held = stream;
      res.write(chunk({ role: 'assistant', content: deltaText(1) }, null));
      evidence.chunksSent += 1;
    });
  });

  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') return reject(new Error('held-stream provider: no port'));
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });

  return {
    url,
    evidence,
    release(): void {
      const stream = held;
      held = null;
      if (!stream || stream.finished) return;
      stream.finished = true;
      evidence.released += 1;
      evidence.streamsClosed += 1;
      clearInterval(stream.timer);
      stream.res.write(chunk({ content: releaseText }, 'stop') + 'data: [DONE]\n\n');
      stream.res.end();
    },
    expectedHeldAnswer(): string {
      const parts: string[] = [];
      for (let n = 1; n <= evidence.chunksSent; n += 1) parts.push(deltaText(n));
      if (evidence.released > 0) parts.push(releaseText);
      return parts.join('').replace(/\s+/g, ' ').trim();
    },
    close: () => new Promise<void>((resolve, reject) => {
      const stream = held;
      held = null;
      if (stream && !stream.finished) {
        // Test teardown with the turn still held: stop the timer so Node can
        // exit; a teardown is neither a cancel nor a release, so evidence
        // deliberately does not count it.
        stream.finished = true;
        clearInterval(stream.timer);
      }
      server.close((error) => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}

/**
 * Attach the provider evidence to the Playwright report and print it — every
 * rewritten test surfaces the mid-turn signal it acted on (T25's evidence
 * pattern, shared by all nine #1661 tests).
 */
export async function attachHeldStreamEvidence(info: TestInfo, label: string, provider: HeldStreamProvider): Promise<void> {
  const body = JSON.stringify(provider.evidence, null, 2);
  await info.attach(`${label}-held-stream-evidence`, { body, contentType: 'application/json' });
  console.log(`[${label}] held-stream provider evidence: ${JSON.stringify(provider.evidence)}`);
}

/**
 * The model-independent mid-turn signal (#1661), shared by every rewritten
 * test: the turn is provably still streaming at the moment the test's action
 * (cancel, freeze, tab switch, SIGKILL, offline blip) fires.
 *
 *   streamRequests === 1 — the real turn reached the scripted provider;
 *   chunksSent    >= 2   — real deltas flowed through the real gateway;
 *   streamsClosed === 0  — the stream is STILL OPEN: the turn cannot have
 *                          completed (the >80-chars-visible wait this replaces
 *                          could coincide with completion on a burst-emitting
 *                          model, i.e. fail exactly when it mattered);
 *   released     === 0   — the fixture did not end the turn; only the test's
 *                          scripted release or a REAL teardown may.
 *
 * The deltas condition WAITS (bounded poll) instead of asserting instantly:
 * the provider writes its first delta immediately and the second one at
 * chunkIntervalMs, so a gate that reads the counter synchronously can run
 * between the two writes and fail a perfectly healthy turn (this exact race
 * burned catchup h's first keyless runs: UI text checks resolve on chunk 1,
 * the gate ran at ~150ms against a 150ms chunk cadence). Polling turns the
 * signal back into a WAIT for real mid-stream evidence — the same shape as
 * the ">80 chars visible" wait it replaces, minus the model dependence.
 * Callers MUST await this.
 */
export async function expectTurnHeldMidStream(provider: HeldStreamProvider, label: string): Promise<void> {
  await expect.poll(
    () => provider.evidence.chunksSent,
    {
      timeout: 15_000,
      message: `${label}: scripted deltas actually flowed through the gateway (bounded wait for the fixture's cadence — a burst-emitting live model never opens this window, the scripted stream always does)`,
    },
  ).toBeGreaterThan(1);
  expect(provider.evidence.streamRequests, `${label}: the real turn reached the scripted provider exactly once`).toBe(1);
  expect(provider.evidence.streamsClosed, `${label}: the provider stream is still open — the turn cannot have completed`).toBe(0);
  expect(provider.evidence.released, `${label}: the fixture did not end the turn — only the scripted release or a real teardown may`).toBe(0);
}
