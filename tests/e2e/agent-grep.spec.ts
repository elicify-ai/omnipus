/**
 * agent-grep.spec.ts — E2E proof for the agent-facing `grep` tool
 * (docs/internal/specs/unified-search-and-grep-spec.md, US-3 "Best-in-class
 * agent grep tool" — spec test 35: "e2e agent-grep flow ... real turn with
 * context lines").
 *
 * Regression class caught:
 *   The context acceptance drives a real turn, tool execution, seeded file and
 *   browser using the suite's existing isolated provider endpoint seam. Only
 *   the external model response is scripted, so context_lines: 2 is a given,
 *   not a model-choice lottery. The provider echoes ONLY the actual tool result.
 *   A separate real-model smoke must choose grep, execute it on the seed and
 *   render the match. Neither a mock grep nor transcript replay can pass this
 *   pack; neither the smoke alone nor a lucky retry proves context correctness.
 *
 * Harness pattern: closely mirrors memory-remember-recall.spec.ts (same
 * auth/API helpers, same disk-seeding-before-prompting approach, same
 * "resolve the default agent, resolve the active workspace, seed BOTH
 * candidate roots since e2e chat may or may not be workspace-scoped"
 * defensive strategy, same waitForTurnFullyDone follow-up-turn handling).
 *
 * Verification separates model input, tool output, and browser rendering:
 *   1. Observe the real tool_call_start in the fresh session: the model must
 *      pass the exact pattern and numeric context_lines: 2. Missing context
 *      is a model-input failure, not evidence of a grep engine defect.
 *   2. Correlate tool_call_result by session AND call id. Its successful result
 *      must contain all five seeded lines in order, including all four context
 *      lines. No transcript replay or fabricated tool result proves this.
 *   3. Preserve the visible grep badge and the original reply-fidelity checks:
 *      the nonce and at least one context line must reach the browser.
 */

import { test, expect, type BrowserContext, type Page, type TestInfo } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import http from 'node:http';
import { waitForConnected } from './fixtures/selectors';
import { freshChat } from './fixtures/session-setup';
import { GatewayProcess } from './fixtures/gateway-process';
import { E2E_MODEL } from './fixtures/e2e-model';
import { WsFrame } from '../../src/lib/api/generated/ws-schemas';

// A retry must not conceal model-input omission in this acceptance test.
test.describe.configure({ retries: 0 });

// ── Constants ─────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6062';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// Unique nonce for this test run (Date.now(), matching memory-remember-recall's
// own reasoning: no seeding issues in Playwright's Node environment).
const NONCE = `GREPTEST-${Date.now()}`;
const NEEDLE_LINE = `NEEDLE-${NONCE}`;
const ABOVE1_LINE = `ABOVE1-${NONCE}`;
const ABOVE2_LINE = `ABOVE2-${NONCE}`;
const BELOW1_LINE = `BELOW1-${NONCE}`;
const BELOW2_LINE = `BELOW2-${NONCE}`;
const SEED_FILE_NAME = 'e2e-grep-haystack.txt';

// ── Auth helpers (same pattern as memory-remember-recall.spec.ts / retention.spec.ts) ──

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(path.dirname(new URL(import.meta.url).pathname), 'fixtures/.auth/admin.json');

function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) return null;
  try {
    const raw = fs.readFileSync(AUTH_FILE, 'utf-8');
    const state = JSON.parse(raw) as {
      origins?: Array<{ origin: string; localStorage?: Array<{ name: string; value: string }> }>;
    };
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value;
      }
    }
  } catch {
    // Auth file may not exist on first run
  }
  return null;
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const token = getStoredAuthToken();
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null;
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  };
}

// ── DOM helpers (same selectors as memory-remember-recall.spec.ts) ────────────

const chatInput = (page: Page) => page.locator('textarea[aria-label="Message input"]');

const assistantMessages = (page: Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])');

/**
 * Wait for any follow-up LLM call the agent starts after a tool call — the
 * stop button reappearing within gapMs signals a second in-flight LLM call
 * (mirrors memory-remember-recall.spec.ts's identically-named helper).
 */
async function waitForTurnFullyDone(page: Page, gapMs = 10_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — turn is fully done.
  }
}

// ── Default agent / workspace resolvers (same pattern as memory-remember-recall.spec.ts) ──

async function resolveDefaultAgentId(page: Page, baseURL = BASE_URL): Promise<string | null> {
  const resp = await page.request.get(`${baseURL}/api/v1/agents`, {
    headers: await apiHeaders(page),
    failOnStatusCode: false,
  });
  if (!resp.ok()) return null;
  const agents = (await resp.json()) as Array<{ id: string; default?: boolean; name?: string }>;
  const def = agents.find((a) => a.default === true);
  if (def) return def.id;
  const mia = agents.find((a) => /mia/i.test(a.name ?? ''));
  return mia?.id ?? null;
}

async function resolveWorkspaceId(page: Page, baseURL = BASE_URL): Promise<string | null> {
  try {
    const resp = await page.request.get(`${baseURL}/api/v1/workspaces`, {
      headers: await apiHeaders(page),
      failOnStatusCode: false,
    });
    if (resp.ok()) {
      const workspaces = (await resp.json()) as Array<{
        id: string;
        is_default?: boolean;
        status?: string;
      }>;
      if (Array.isArray(workspaces) && workspaces.length > 0) {
        const def = workspaces.find((w) => w.is_default === true);
        if (def?.id) return def.id;
        const first = workspaces.find((w) => !w.status || w.status === 'active') ?? workspaces[0];
        if (first?.id) return first.id;
      }
    }
  } catch {
    // Network error — fall through to null; the disk seed still covers the
    // agent-private root.
  }
  return null;
}

/**
 * Seed the haystack file into EVERY location grep might actually search,
 * mirroring resolveWorkspaceId's own justification: an e2e chat session MAY
 * be workspace-scoped (TurnWorkspaceDir re-roots grep into
 * $OMNIPUS_HOME/workspaces/<id>/work/) or not (grep falls back to the
 * agent's own fixed home, $OMNIPUS_HOME/agents/<id>/) — this test does not
 * assume which, it covers both so the assertion is about grep's real
 * behavior, not this harness's guess at routing.
 */
function seedHaystack(agentId: string, workspaceId: string | null, home = OMNIPUS_HOME): string[] {
  const content =
    [ABOVE2_LINE, ABOVE1_LINE, NEEDLE_LINE, BELOW1_LINE, BELOW2_LINE].join('\n') + '\n';

  const dirs = [path.join(home, 'agents', agentId)];
  if (workspaceId) {
    dirs.push(path.join(home, 'workspaces', workspaceId, 'work'));
  }

  const written: string[] = [];
  for (const dir of dirs) {
    fs.mkdirSync(dir, { recursive: true });
    const filePath = path.join(dir, SEED_FILE_NAME);
    fs.writeFileSync(filePath, content, 'utf-8');
    written.push(filePath);
  }
  return written;
}

// ── Real wire evidence (generated contract, no routing or stubbing) ────────────

type GrepStart = Extract<WsFrame, { type: 'tool_call_start' }>;
type GrepResult = Extract<WsFrame, { type: 'tool_call_result' }>;

function recordGrepTraffic(page: Page) {
  const starts: GrepStart[] = [];
  const results: GrepResult[] = [];
  const errors: string[] = [];
  page.on('websocket', (socket) => {
    socket.on('framereceived', ({ payload }) => {
      try {
        const frame = WsFrame.parse(JSON.parse(payload.toString()));
        if (frame.type === 'tool_call_start' && frame.tool === 'grep') starts.push(frame);
        if (frame.type === 'tool_call_result' && frame.tool === 'grep') results.push(frame);
      } catch (error) {
        errors.push(`received frame decode failed: ${String(error)}`);
      }
    });
  });
  return { starts, results, errors };
}

function assertGrepInput(start: GrepStart): void {
  expect(
    start.params.context_lines,
    `model did not pass context_lines: 2 to grep; actual arguments: ${JSON.stringify(start.params)}`,
  ).toBe(2); // US-3 AS-4: two lines before AND after, not the omitted/zero default.
  expect(start.params.pattern, 'grep must search for the exact seeded needle').toBe(NEEDLE_LINE);
}

function assertGrepResult(result: GrepResult): void {
  expect(result.status, 'the correlated grep execution must succeed').toBe('success');
  if (typeof result.result !== 'string') {
    throw new Error('expected the correlated grep tool result to be text');
  }
  // Match data-line suffixes, not the header that quotes the search pattern.
  // The independent oracle is the five-line seed and US-3 AS-4, not model prose
  // or the implementation's line-number/separator formatting.
  const lines = result.result.match(
    new RegExp(`(?:ABOVE[12]|NEEDLE|BELOW[12])-${NONCE}\\r?$`, 'gm'),
  );
  expect(lines, 'expected all five seeded lines in order in the actual grep result').toEqual([
    ABOVE2_LINE, ABOVE1_LINE, NEEDLE_LINE, BELOW1_LINE, BELOW2_LINE,
  ]);
}

// These controls test the evidence assertions, NOT a mocked grep tool. A removed
// assertion must turn a negative control red rather than silently certify input.
test.describe('grep evidence controls', () => {
  const start: GrepStart = {
    type: 'tool_call_start', session_id: 'control-session', call_id: 'control-grep',
    tool: 'grep', params: { pattern: NEEDLE_LINE, context_lines: 2 },
  };
  const result: GrepResult = {
    type: 'tool_call_result', session_id: start.session_id, call_id: start.call_id,
    tool: 'grep', status: 'success',
    result: [ABOVE2_LINE, ABOVE1_LINE, NEEDLE_LINE, BELOW1_LINE, BELOW2_LINE].join('\n'),
  };

  test('accepts the requested input and complete context evidence', () => {
    assertGrepInput(start);
    assertGrepResult(result);
  });

  for (const [label, params] of [
    ['omitted', { pattern: NEEDLE_LINE }],
    ['zero', { pattern: NEEDLE_LINE, context_lines: 0 }],
    ['string', { pattern: NEEDLE_LINE, context_lines: '2' }],
  ] as const) {
    test(`rejects ${label} context_lines with an explicit model-input failure`, () => {
      expect(() => assertGrepInput({ ...start, params })).toThrow(/model did not pass context_lines: 2/);
    });
  }

  test('rejects a result missing even one requested context line', () => {
    expect(() => assertGrepResult({
      ...result, result: [ABOVE2_LINE, ABOVE1_LINE, NEEDLE_LINE, BELOW1_LINE].join('\n'),
    })).toThrow(/expected all five seeded lines in order in the actual grep result/);
  });
});

// Same supported provider seam as terminal-outcome: script ONLY the external
// model response. Never synthesize a grep result or read the haystack here.
async function startGrepProvider() {
  const errors: string[] = [];
  const receivedResults: string[] = [];
  const toolMessages: Record<string, unknown>[][] = [];
  let discovered = false;
  let grepCalls = 0;
  const callId = 'context-acceptance-grep';
  const chunk = (delta: Record<string, unknown>, finish: string | null) =>
    'data: ' + JSON.stringify({
      id: 'grep-acceptance-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
      created: Math.floor(Date.now() / 1000),
      choices: [{ index: 0, delta, finish_reason: finish }],
    }) + '\n\n';
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (data: Buffer) => chunks.push(data));
    req.on('error', (error) => errors.push(`provider request: ${error.message}`));
    const fail = (message: string) => {
      errors.push(message);
      res.writeHead(500, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message } }));
    };
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_MODEL, object: 'model' }] }));
        return;
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        fail(`unexpected provider route: ${req.method} ${req.url}`);
        return;
      }
      // External OpenAI-compatible protocol, NOT a gateway/SPA wire type.
      let body: Record<string, unknown>;
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>;
      } catch (error) {
        fail(`invalid provider request JSON: ${String(error)}`);
        return;
      }
      if (body.stream !== true) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          id: 'grep-validation', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        return;
      }
      const names = Array.isArray(body.tools) ? body.tools.map((tool: Record<string, unknown>) =>
        (tool.function as Record<string, unknown> | undefined)?.name) : [];
      const messages = Array.isArray(body.messages) ? body.messages as Record<string, unknown>[] : [];
      toolMessages.push(messages.filter((message) => message.role === 'tool'));
      const actualResult = messages.find((message) =>
        message.role === 'tool' && message.tool_call_id === callId);
      // The provider transport may encode text in cache-tagged content blocks.
      // Preserve the actual text exactly; reject non-text payloads, never invent it.
      const content = actualResult?.content;
      const text = typeof content === 'string' ? content :
        Array.isArray(content) && content.every((part: Record<string, unknown>) =>
          part.type === 'text' && typeof part.text === 'string') ?
          content.map((part: Record<string, unknown>) => part.text).join('') : null;
      if (grepCalls === 1 && text !== null) {
        receivedResults.push(text);
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.end(chunk({ role: 'assistant', content: text }, 'stop') + 'data: [DONE]\n\n');
        return;
      }
      let name: string;
      let args: Record<string, unknown>;
      if (grepCalls === 0 && names.includes('grep')) {
        name = 'grep';
        args = { pattern: NEEDLE_LINE, context_lines: 2 };
        grepCalls += 1;
      } else if (!discovered && grepCalls === 0 && names.includes('ToolSearch')) {
        name = 'ToolSearch';
        args = { names: ['grep'] };
        discovered = true;
      } else {
        fail('grep was not offered, was repeated, or its actual tool result was missing');
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(chunk({ tool_calls: [{ index: 0, id: name === 'grep' ? callId : 'load-grep',
        type: 'function', function: { name, arguments: JSON.stringify(args) } }] }, null) +
        chunk({}, 'tool_calls') + 'data: [DONE]\n\n');
    });
  });
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') return reject(new Error('grep provider: no port'));
      resolve(`http://127.0.0.1:${address.port}`);
    });
  });
  return { url, errors, receivedResults, toolMessages, grepCalls: () => grepCalls,
    close: () => new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}

// ── Context acceptance: real tool execution, scripted external provider ───────

async function runGrepContextFlow(
  page: Page, info: TestInfo, baseURL: string, home: string,
): Promise<GrepResult> {
  const traffic = recordGrepTraffic(page);
  try {
    // The caller retains the original 180s test budget, including gateway setup.

    // ── Arrange ──────────────────────────────────────────────────────────────
    await page.goto(`${baseURL}/`);
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 });

    const agentId = await resolveDefaultAgentId(page, baseURL);
    if (!agentId) {
      throw new Error(
        'BLOCKED: Could not resolve the default agent ID from GET /api/v1/agents. ' +
          'The gateway may not be seeded correctly (coreagent.SeedConfig seeds Mia as default). ' +
          'Seeding the grep haystack file requires knowing which agent ID to write into.',
      );
    }
    const workspaceId = await resolveWorkspaceId(page, baseURL);

    console.log(`[agent-grep] agentId=${agentId} workspaceId=${workspaceId ?? '(none)'}`);

    const seededPaths = seedHaystack(agentId, workspaceId, home);

    console.log(`[agent-grep] Seeded haystack at: ${seededPaths.join(', ')}`);

    const { sessionId } = await freshChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    const input = chatInput(page);
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // ── The turn ─────────────────────────────────────────────────────────────
    //
    // BDD (US-3 AS-1, AS-4):
    //   Given a file containing a unique pattern
    //   When the user forcefully instructs the agent to grep for it with
    //     context_lines 2
    //   Then the assistant emits exactly one grep tool call
    //   And its reply contains the matched line and at least one context line
    //
    // Supply the actual argument object, not prose that leaves context optional.
    // Do not name the context tokens: the model must obtain them from real grep.
    const requestedArgs = { pattern: NEEDLE_LINE, context_lines: 2 };
    await input.fill(
      `Call the grep tool exactly once using this exact JSON argument object: ` +
        `${JSON.stringify(requestedArgs)}. ` +
        `context_lines must be the number 2; do not omit it or use its zero default. ` +
        `Do not call other tools or repeat grep. Then reply with ONLY the matched line ` +
        `and its four surrounding context lines, copied verbatim from the tool result, one per line.`,
    );
    await input.press('Enter');

    const calls = () => traffic.starts.filter((frame) => frame.session_id === sessionId);
    await expect.poll(() => calls().length, {
      timeout: 120_000, message: 'expected a real grep invocation in the fresh chat session',
    }).toBeGreaterThan(0).catch((error: unknown) => {
      throw new Error(
        `${error instanceof Error ? error.message : String(error)}\n` +
        `Grep observer decode errors:\n${traffic.errors.join('\n') || '(none recorded)'}`,
        { cause: error },
      );
    });
    const start = calls()[0];
    await info.attach('executed-grep-call', {
      body: JSON.stringify(start, null, 2), contentType: 'application/json',
    });
    assertGrepInput(start); // Fail here if the model omitted context, before blaming grep.

    const results = () => traffic.results.filter((frame) =>
      frame.session_id === sessionId && frame.call_id === start.call_id,
    );
    await expect.poll(() => results().length, {
      timeout: 15_000, message: 'expected the result of the SAME grep call in the SAME session',
    }).toBe(1).catch((error: unknown) => {
      throw new Error(
        `${error instanceof Error ? error.message : String(error)}\n` +
        `Grep observer decode errors:\n${traffic.errors.join('\n') || '(none recorded)'}`,
        { cause: error },
      );
    });
    await info.attach('executed-grep-result', {
      body: JSON.stringify(results()[0], null, 2), contentType: 'application/json',
    });
    assertGrepResult(results()[0]);

    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 120_000 });
    await waitForTurnFullyDone(page, 10_000);
    expect(traffic.errors, 'received WebSocket evidence must decode against the contract').toEqual([]);
    expect(calls(), 'the model must invoke grep exactly once, without a corrective retry').toHaveLength(1);

    // ── Assert (1): the grep TOOL actually ran — deterministic, independent
    // of anything the model chose to say afterward.
    await expect(
      page.locator('[data-testid="tool-call-badge"][data-tool="grep"]').first(),
      'expected a visible grep tool-call badge — the model must have actually ' +
        'invoked the grep tool, not merely described doing so',
    ).toBeVisible({ timeout: 5_000 });

    // ── Assert (2): content fidelity — the matched line's nonce reached the
    // rendered reply.
    const msgsWithNeedle = assistantMessages(page).filter({ hasText: NEEDLE_LINE });
    const needleCount = await msgsWithNeedle.count();
    if (needleCount === 0) {
      const allMsgs = assistantMessages(page);
      const msgCount = await allMsgs.count();
      const msgTexts: string[] = [];
      for (let i = 0; i < msgCount; i++) {
        try {
          msgTexts.push((await allMsgs.nth(i).textContent()) ?? '(empty)');
        } catch {
          msgTexts.push('(could not read)');
        }
      }
      throw new Error(
        [
          `BLOCKED or INCOMPLETE: matched line "${NEEDLE_LINE}" does NOT appear in any`,
          `  completed assistant message after grepping for it.`,
          '',
          `  Seeded haystack file(s): ${seededPaths.join(', ')}`,
          `  (a visible grep tool-call badge WAS asserted above — the tool did run;`,
          `   the gap, if any, is in the tool's real, or the model's echo of it)`,
          '',
          `  Assistant messages (${msgCount} total):`,
          ...msgTexts.map((t, i) => `    [${i}] ${t.slice(0, 300)}`),
          '',
          'Traces to: pkg/tools/grep.go (GrepTool.Execute / renderGrepResult).',
        ].join('\n'),
      );
    }

    // ── Assert (3): at least one context line made it into the reply too —
    // proof `context_lines` was actually honored, not merely accepted and
    // ignored. Soft in WHICH token (the model may echo any subset), hard in
    // that at least one must appear.
    const contextTokens = [ABOVE1_LINE, ABOVE2_LINE, BELOW1_LINE, BELOW2_LINE];
    let sawContext = false;
    for (const token of contextTokens) {
      const count = await assistantMessages(page).filter({ hasText: token }).count();
      if (count > 0) {
        sawContext = true;
        break;
      }
    }
    expect(
      sawContext,
      `expected at least one context-line token (${contextTokens.join(', ')}) in the ` +
        'reply — context_lines: 2 was requested; if none appear, either the tool did not ' +
        'honor context_lines or the model did not echo any context back',
    ).toBe(true);
    return results()[0];
  } finally {
    await info.attach('grep-wire-evidence', {
      body: JSON.stringify(traffic, null, 2), contentType: 'application/json',
    });
  }
}

test('agent grep tool finds a seeded match with context lines', async ({ browser }, info) => {
  test.setTimeout(180_000); // Original acceptance budget, including isolated setup.
  const provider = await startGrepProvider();
  let gateway: GatewayProcess | undefined;
  let context: BrowserContext | undefined;
  try {
    gateway = await GatewayProcess.start({ apiBase: provider.url, model: E2E_MODEL });
    context = await browser.newContext({
      baseURL: gateway.baseURL, storageState: await gateway.browserStorageState(),
    });
    const page = await context.newPage();
    const result = await runGrepContextFlow(page, info, gateway.baseURL, gateway.homeDir);
    expect(provider.errors, 'scripted provider must not hide a failed request').toEqual([]);
    expect(provider.grepCalls(), 'exactly one scripted request for actual grep execution').toBe(1);
    expect(provider.receivedResults, 'the reply echoes the ACTUAL tool result, never canned context')
      .toEqual([result.result]);
  } finally {
    await info.attach('scripted-provider-evidence', {
      body: JSON.stringify({ errors: provider.errors, toolMessages: provider.toolMessages }, null, 2),
      contentType: 'application/json',
    });
    console.log(`[agent-grep] provider evidence: ${JSON.stringify({
      errors: provider.errors, toolMessages: provider.toolMessages,
    })}`);
    try {
      await context?.close();
    } finally {
      try { await gateway?.stop(); } finally { await provider.close(); }
    }
  }
});

// Keep the real-model value separate: it must choose and execute grep, then
// render its real match. Context correctness belongs to the deterministic test
// above; the smoke does not turn model omission into an engine-context verdict.
test('real-model grep smoke executes on the seed and renders the matched line', async ({ page }, info) => {
  test.setTimeout(180_000);
  const traffic = recordGrepTraffic(page);
  try {
    await page.goto('/');
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 });
    const agentId = await resolveDefaultAgentId(page);
    if (!agentId) throw new Error('BLOCKED: could not resolve the agent for the real-model grep smoke');
    seedHaystack(agentId, await resolveWorkspaceId(page));
    const { sessionId } = await freshChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });
    await chatInput(page).fill(
      `Call the grep tool exactly once with pattern "${NEEDLE_LINE}". ` +
      `Then copy the matched line from the actual tool result into your reply.`,
    );
    await chatInput(page).press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 120_000 });
    await waitForTurnFullyDone(page, 10_000);
    const calls = traffic.starts.filter((frame) => frame.session_id === sessionId);
    expect(traffic.errors, 'real-model wire evidence must decode').toEqual([]);
    expect(calls, 'the real model must invoke grep exactly once').toHaveLength(1);
    expect(calls[0].params.pattern, 'the real model must search the seeded needle').toBe(NEEDLE_LINE);
    const results = traffic.results.filter((frame) =>
      frame.session_id === sessionId && frame.call_id === calls[0].call_id);
    expect(results, 'observe the same real-model grep execution result').toHaveLength(1);
    expect(results[0].status, 'real-model grep execution succeeds').toBe('success');
    expect(String(results[0].result).split('\n').some((line) => line.endsWith(NEEDLE_LINE)),
      'the actual tool result contains the seeded data line, not just its quoted search pattern').toBe(true);
    await expect(page.locator('[data-testid="tool-call-badge"][data-tool="grep"]').first()).toBeVisible();
    await expect(assistantMessages(page).filter({ hasText: NEEDLE_LINE })).toHaveCount(1);
    await info.attach('real-model-grep-evidence', {
      body: JSON.stringify({ calls, results }, null, 2), contentType: 'application/json',
    });
  } finally {
    await info.attach('grep-wire-evidence', {
      body: JSON.stringify(traffic, null, 2), contentType: 'application/json',
    });
  }
});
