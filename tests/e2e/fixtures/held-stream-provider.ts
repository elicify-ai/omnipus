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
  /** Per-script state when `scripts` is used (empty otherwise): the
   * multi-prompt branch (#1661 Bug-3-a) asserts on these — one tab's prompt
   * held while another's is answered, per prompt, not as one global count. */
  promptRecords: HeldStreamPromptRecord[];
}

/** Per-script record for the `scripts` branch (see HeldStreamOptions.scripts). */
export interface HeldStreamPromptRecord {
  /** The script's prompt substring (same matching rule as promptIncludes). */
  promptIncludes: string;
  /** What this script does: keep the stream open, or answer it complete. */
  behavior: 'hold' | 'answer';
  /** Matching streaming requests seen for this prompt. */
  requests: number;
  /** True while this prompt's stream is currently held open. */
  held: boolean;
  /** Completed answers served for an 'answer' script. */
  answered: number;
  /** Times the client aborted while this prompt's stream was held — the
   * wire-level cancel signal (the gateway cancelling the provider call). */
  clientClosed: number;
  /** Times release() finished this prompt's held stream. */
  released: number;
  /** Deltas and closes for this prompt alone, including concurrent holds. */
  chunksSent: number;
  streamsClosed: number;
}

// not-wire-format: a test fixture handle, not an Omnipus wire type.
export interface HeldStreamProvider {
  readonly url: string;
  readonly evidence: HeldStreamEvidence;
  /** Finish held streams deterministically (finish chunk + [DONE]). With a
   * prompt, finish only that script; otherwise finish every held stream.
   * A no-op when nothing is held (already cancelled/crashed/released). */
  release(promptIncludes?: string): void;
  /** The exact answer the SPA must render for the held turn: the numbered
   * delta sequence plus the release text once released — whitespace-
   * normalized the same way fixtures/assistant-text.ts normalizes. Lets a
   * test pin byte-continuity of catch-up (no dropped or duplicated delta)
   * despite the prose being repetitive. */
  expectedHeldAnswer(promptIncludes?: string): string;
  /** Stop the HTTP server and tear down remaining connections. */
  close(): Promise<void>;
}

/** One branch of the multi-prompt script (see HeldStreamOptions.scripts). */
export interface HeldStreamScript {
  /** Substring identifying this prompt (same matching rule as promptIncludes). */
  promptIncludes: string;
  /** 'hold' keeps this prompt's stream open until release() or a real cancel;
   * 'answer' completes it with answerText (streamed at the fixture's cadence
   * so the answer stays observable mid-flight, like the retry path). */
  behavior: 'hold' | 'answer';
  /** Reply text for behavior 'answer' (required there). */
  answerText?: string;
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
  /** Multi-prompt branch (#1661 Bug-3-a and the replay tests): branch the
   * reply per prompt instead of the single held prompt — hold one tab's
   * message, answer another's. When set, matching walks these scripts and
   * each gets a record in evidence.promptRecords. Matching scans the LAST
   * user message backwards so a later turn's request matches its own script
   * rather than an earlier turn's marker still present in the sent history
   * (the gateway sends the whole conversation). Single-hold behaviour
   * (promptIncludes) is unchanged and still used when scripts is omitted. */
  scripts?: HeldStreamScript[];
}

interface HeldStream {
  res: http.ServerResponse;
  timer: NodeJS.Timeout;
  finished: boolean;
  /** The script record this stream is held for (scripts branch only). */
  record: HeldStreamPromptRecord | null;
}

/**
 * The text of a chat message's content — a plain string or an OpenAI-style
 * content-parts array (module scope: pure, instance-independent).
 */
const userText = (content: unknown): string => typeof content === 'string' ? content : Array.isArray(content) ?
  content.map((part: Record<string, unknown>) => part.type === 'text' ? part.text : '').join('') : '';

// External OpenAI-compatible protocol, not a gateway/SPA wire format.
const chunk = (delta: Record<string, unknown>, finish: string | null) =>
  'data: ' + JSON.stringify({
    id: 'held-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000),
    choices: [{ index: 0, delta, finish_reason: finish }],
  }) + '\n\n';

/**
 * Mutable per-instance state the module-scope handlers operate on — the
 * former closure variables of startHeldStreamProvider, in one object. One
 * runtime per provider instance; nothing is shared across instances.
 */
interface HeldStreamRuntime {
  opts: HeldStreamOptions;
  evidence: HeldStreamEvidence;
  chunkIntervalMs: number;
  releaseText: string;
  retryAnswer: string;
  // Each scripted delta is UNIQUELY numbered. A periodic delta stream cannot
  // prove catch-up byte-continuity — a dropped middle chunk is invisible in
  // repeated prose — so every delta carries its own sequence number and
  // expectedHeldAnswer() reconstructs the exact answer the SPA must render.
  deltaText: (n: number) => string;
  /** One held stream per prompt; catch-up f keeps two real turns in flight. */
  held: Map<string, HeldStream>;
}

function completeNow(res: http.ServerResponse, content: string): void {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  res.end(chunk({ role: 'assistant', content }, 'stop') + 'data: [DONE]\n\n');
}

// A scripted completion that still STREAMS at the fixture's cadence: the
// tests pin the running row while a response renders (catchup h's
// "Generate again" assertions observe the retry mid-stream), so a retry
// must not arrive in one burst — that would make the running state
// unobservable and the original assertion unsatisfiable.
function completePaced(rt: HeldStreamRuntime, res: http.ServerResponse, content: string): void {
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
  }, rt.chunkIntervalMs);
  res.once('close', () => clearInterval(timer));
}

function createEvidence(opts: HeldStreamOptions): HeldStreamEvidence {
  return {
    streamRequests: 0,
    unmatchedStreams: 0,
    chunksSent: 0,
    streamsClosed: 0,
    released: 0,
    retried: 0,
    errors: [],
    promptRecords: (opts.scripts ?? []).map((script) => ({
      promptIncludes: script.promptIncludes,
      behavior: script.behavior,
      requests: 0,
      held: false,
      answered: 0,
      clientClosed: 0,
      released: 0,
      chunksSent: 0,
      streamsClosed: 0,
    })),
  };
}

function serveModelList(res: http.ServerResponse): void {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
}

function serveUnexpectedRoute(rt: HeldStreamRuntime, req: http.IncomingMessage, res: http.ServerResponse): void {
  rt.evidence.errors.push(`unexpected provider route: ${req.method} ${req.url}`);
  res.writeHead(404, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ error: { message: 'unexpected provider route' } }));
}

// Onboarding/key validation does not start the held stream.
function serveValidationProbe(res: http.ServerResponse): void {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    id: 'held-stream-validation', object: 'chat.completion', model: E2E_FALLBACK.model,
    choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
  }));
}

// Answer complete so no turn can hang on the fixture; evidence flags it.
function answerUnmatched(rt: HeldStreamRuntime, res: http.ServerResponse, reason: string): void {
  rt.evidence.unmatchedStreams += 1;
  rt.evidence.errors.push(reason);
  completeNow(res, 'FIXTURE_ERROR: unexpected streaming prompt');
}

/** THE HELD STREAM for a script: deltas on a timer, no finish chunk, no
 * [DONE] — the turn cannot complete until release(), a real cancel or a
 * crash. The per-script record is the test's branch oracle. */
function holdScriptsStream(rt: HeldStreamRuntime, res: http.ServerResponse, record: HeldStreamPromptRecord): void {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const stream: HeldStream = {
    res,
    timer: setInterval(() => {
      res.write(chunk({ content: rt.deltaText(record.chunksSent + 1) }, null));
      record.chunksSent += 1;
      rt.evidence.chunksSent += 1;
    }, rt.chunkIntervalMs),
    finished: false,
    record,
  };
  res.once('close', () => {
    clearInterval(stream.timer);
    if (!stream.finished) {
      stream.finished = true;
      rt.evidence.streamsClosed += 1;
      // The client (the gateway cancelling the provider call on turn
      // Stop) closed a stream the fixture did not finish: the wire-level
      // cancel signal for this prompt.
      record.clientClosed += 1;
      record.streamsClosed += 1;
    }
    if (rt.held.get(record.promptIncludes) === stream) {
      rt.held.delete(record.promptIncludes);
      record.held = false;
    }
  });
  res.on('error', (error) => rt.evidence.errors.push(`provider response: ${error.message}`));
  rt.held.set(record.promptIncludes, stream);
  record.held = true;
  res.write(chunk({ role: 'assistant', content: rt.deltaText(1) }, null));
  record.chunksSent += 1;
  rt.evidence.chunksSent += 1;
}

function handleScriptsBranch(rt: HeldStreamRuntime, res: http.ServerResponse, messages: Record<string, unknown>[]): void {
  const scripts = rt.opts.scripts!;
  // Multi-prompt branch: walk the user messages BACKWARDS so a later
  // turn's request matches its own script, not an earlier turn's marker
  // still present in the sent conversation history.
  let matched: HeldStreamScript | null = null;
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const message = messages[i];
    if (message.role !== 'user') continue;
    const text = userText(message.content);
    const hit = scripts.find((script) => text.includes(script.promptIncludes));
    if (hit) {
      matched = hit;
      break;
    }
  }
  const record = matched ?
    rt.evidence.promptRecords.find((entry) => entry.promptIncludes === matched!.promptIncludes) ?? null :
    null;
  rt.evidence.streamRequests += 1;
  if (!matched || !record) {
    answerUnmatched(rt, res, `unexpected streaming prompt (only scripted prompts are held or answered)`);
    return;
  }
  record.requests += 1;
  if (matched.behavior === 'answer') {
    record.answered += 1;
    completePaced(rt, res, matched.answerText ?? `FIXTURE_ERROR: answer script for "${matched.promptIncludes}" has no answerText`);
    return;
  }
  if (rt.held.has(matched.promptIncludes)) {
    // A second request for the SAME prompt is unexpected, but different
    // scripts may hold concurrently (catch-up f's two active chats).
    rt.evidence.retried += 1;
    rt.evidence.errors.push(`hold requested for "${matched.promptIncludes}" while that prompt is already held`);
    completePaced(rt, res, rt.retryAnswer);
    return;
  }
  holdScriptsStream(rt, res, record);
}

/** THE HELD STREAM (single-prompt path): deltas on a timer, no finish chunk,
 * no [DONE] — the turn cannot complete until release() or a real teardown
 * (cancel/crash). */
function holdSingleStream(rt: HeldStreamRuntime, res: http.ServerResponse): void {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const stream: HeldStream = {
    res,
    timer: setInterval(() => {
      res.write(chunk({ content: rt.deltaText(rt.evidence.chunksSent + 1) }, null));
      rt.evidence.chunksSent += 1;
    }, rt.chunkIntervalMs),
    finished: false,
    record: null,
  };
  res.once('close', () => {
    clearInterval(stream.timer);
    if (!stream.finished) {
      stream.finished = true;
      rt.evidence.streamsClosed += 1;
    }
    if (rt.held.get(rt.opts.promptIncludes) === stream) rt.held.delete(rt.opts.promptIncludes);
  });
  res.on('error', (error) => rt.evidence.errors.push(`provider response: ${error.message}`));
  rt.held.set(rt.opts.promptIncludes, stream);
  res.write(chunk({ role: 'assistant', content: rt.deltaText(1) }, null));
  rt.evidence.chunksSent += 1;
}

function handleSinglePromptBranch(rt: HeldStreamRuntime, res: http.ServerResponse, messages: Record<string, unknown>[]): void {
  const hasPrompt = messages.some((message) => {
    if (message.role !== 'user') return false;
    return userText(message.content).includes(rt.opts.promptIncludes);
  });
  rt.evidence.streamRequests += 1;
  if (!hasPrompt) {
    answerUnmatched(rt, res, `unexpected streaming prompt (only the scripted prompt is held)`);
    return;
  }
  if (rt.held.size > 0 || rt.evidence.streamRequests > 1) {
    // A later scripted turn (retry/regenerate): answer complete, do not
    // hold — but stream it at cadence so the retry stays observable
    // mid-flight.
    rt.evidence.retried += 1;
    completePaced(rt, res, rt.retryAnswer);
    return;
  }
  holdSingleStream(rt, res);
}

function handleHttpRequest(rt: HeldStreamRuntime, req: http.IncomingMessage, res: http.ServerResponse): void {
  const bodyChunks: Buffer[] = [];
  req.on('data', (data: Buffer) => bodyChunks.push(data));
  req.on('error', (error) => rt.evidence.errors.push(`provider request: ${error.message}`));
  req.on('end', () => {
    if (req.method === 'GET' && req.url?.includes('/models')) {
      serveModelList(res);
      return;
    }
    if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
      serveUnexpectedRoute(rt, req, res);
      return;
    }
    let body: Record<string, unknown>;
    try {
      body = JSON.parse(Buffer.concat(bodyChunks).toString('utf8')) as Record<string, unknown>;
    } catch (error) {
      rt.evidence.errors.push(`invalid provider request JSON: ${String(error)}`);
      res.writeHead(400, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message: 'invalid provider request JSON' } }));
      return;
    }
    if (body.stream !== true) {
      serveValidationProbe(res);
      return;
    }
    const messages = Array.isArray(body.messages) ? body.messages as Record<string, unknown>[] : [];
    if (rt.opts.scripts) {
      handleScriptsBranch(rt, res, messages);
      return;
    }
    handleSinglePromptBranch(rt, res, messages);
  });
}

function releaseHeld(rt: HeldStreamRuntime, promptIncludes?: string): void {
  for (const [prompt, stream] of rt.held) {
    if (promptIncludes !== undefined && prompt !== promptIncludes) continue;
    rt.held.delete(prompt);
    if (stream.finished) continue;
    stream.finished = true;
    rt.evidence.released += 1;
    rt.evidence.streamsClosed += 1;
    if (stream.record) {
      stream.record.released += 1;
      stream.record.streamsClosed += 1;
      stream.record.held = false;
    }
    clearInterval(stream.timer);
    stream.res.write(chunk({ content: rt.releaseText }, 'stop') + 'data: [DONE]\n\n');
    stream.res.end();
  }
}

function expectedHeldAnswerText(rt: HeldStreamRuntime, promptIncludes?: string): string {
  const record = promptIncludes === undefined ? undefined :
    rt.evidence.promptRecords.find((entry) => entry.promptIncludes === promptIncludes);
  if (promptIncludes !== undefined && !record) throw new Error(`unknown held script: ${promptIncludes}`);
  if (promptIncludes === undefined && rt.evidence.promptRecords.filter((entry) => entry.behavior === 'hold').length > 1) {
    throw new Error('expectedHeldAnswer needs a prompt when multiple scripts hold');
  }
  const parts: string[] = [];
  for (let n = 1; n <= (record ?? rt.evidence).chunksSent; n += 1) parts.push(rt.deltaText(n));
  if ((record ?? rt.evidence).released > 0) parts.push(rt.releaseText);
  return parts.join('').replace(/\s+/g, ' ').trim();
}

function closeProvider(server: http.Server, rt: HeldStreamRuntime): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    for (const stream of rt.held.values()) {
      if (!stream.finished) {
        // Teardown stops timers but is neither a cancel nor a release.
        stream.finished = true;
        clearInterval(stream.timer);
      }
    }
    rt.held.clear();
    server.close((error) => error ? reject(error) : resolve());
    server.closeAllConnections();
  });
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
  const chunkText = opts.chunkText ?? 'The tide keeps its own steady rhythm in this scripted stream.';
  const rt: HeldStreamRuntime = {
    opts,
    evidence: createEvidence(opts),
    chunkIntervalMs: opts.chunkIntervalMs ?? 150,
    releaseText: opts.releaseText ?? ' The scripted stream now finishes.',
    retryAnswer:
      'The scripted retry answer completes without holding. The held turn was already finished before this request arrived.',
    deltaText: (n: number): string => `${chunkText} (part ${n})`,
    held: new Map(),
  };

  const server = http.createServer((req, res) => handleHttpRequest(rt, req, res));

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
    evidence: rt.evidence,
    release(promptIncludes?: string): void {
      releaseHeld(rt, promptIncludes);
    },
    expectedHeldAnswer(promptIncludes?: string): string {
      return expectedHeldAnswerText(rt, promptIncludes);
    },
    close: () => closeProvider(server, rt),
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
export async function expectTurnHeldMidStream(provider: HeldStreamProvider, label: string, promptIncludes?: string): Promise<void> {
  const record = promptIncludes === undefined ? undefined :
    provider.evidence.promptRecords.find((entry) => entry.promptIncludes === promptIncludes);
  if (promptIncludes !== undefined) expect(record, `${label}: per-prompt evidence exists`).toBeDefined();
  const evidence = record ?? provider.evidence;
  await expect.poll(
    () => evidence.chunksSent,
    {
      timeout: 15_000,
      message: `${label}: scripted deltas actually flowed through the gateway (bounded wait for the fixture's cadence — a burst-emitting live model never opens this window, the scripted stream always does)`,
    },
  ).toBeGreaterThan(1);
  expect(record?.requests ?? provider.evidence.streamRequests, `${label}: the real turn reached the scripted provider exactly once`).toBe(1);
  expect(evidence.streamsClosed, `${label}: the provider stream is still open — the turn cannot have completed`).toBe(0);
  expect(evidence.released, `${label}: the fixture did not end the turn — only the scripted release or a real teardown may`).toBe(0);
}
