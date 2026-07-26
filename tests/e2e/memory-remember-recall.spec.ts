/**
 * memory-remember-recall.spec.ts — E2E round-trip for the remember→recall_memory tool chain.
 *
 * Regression class caught:
 *   This test exercises the full tool→adapter→room→bleve pipeline end-to-end:
 *
 *   1. The `remember` tool call (pkg/tools/memory.go: Remember handler) must
 *      actually write a .md file to disk.  When the session is workspace-scoped
 *      (which the e2e chat always is — the SPA opens at /#/workspaces/<id>/chat),
 *      `remember` with no explicit `room` defaults to the SHARED workspace room:
 *        <OMNIPUS_HOME>/workspaces/<workspaceId>/.omnipus/memories/<ulid>.md
 *      (pkg/memrooms/rooms.go: DefaultRoomScope returns "shared" when a shared room
 *      is available; "private" otherwise.)
 *      If no workspace is associated with the session, the file lands in the PRIVATE
 *      room:
 *        <OMNIPUS_HOME>/agents/<agentId>/.omnipus/memories/<ulid>.md
 *      The disk assertion searches BOTH locations so the test is correct in either case.
 *
 *   2. The MemoryStoreAdapter (pkg/agent/memory_adapter.go) must flush that write
 *      so the bleve index (and the disk-mtime staleness check) picks it up before
 *      the next `recall_memory` search.
 *
 *   3. The `recall_memory` tool call must search the bleve index
 *      (pkg/agent/memory.go: SearchEntries), find the nonce-bearing memory, and
 *      surface it back in the LLM's context window so the model can reproduce the
 *      nonce in its reply.
 *
 *   4. The SPA must render the assistant's reply containing the nonce.
 *
 *   Unit tests cover each subsystem in isolation.  This test is the only gate
 *   that validates the full chain under a real LLM.  If it turns red it means
 *   one of the joints between units is broken — NOT a test infrastructure
 *   problem.  Do NOT add softSkip() here without a tracked GitHub issue.
 *
 * Notes:
 *   - Both turns are driven against the same default agent (Mia) and the same
 *     chat session so the agent's private room is consistent.
 *   - The LLM is glm-class: it needs FORCEFUL, single-purpose imperative prompts
 *     (see memory: "glm needs forceful small-batch prompts"). Prompts deliberately
 *     avoid hedging language.
 *   - Disk assertions use a retry loop because the MemoryStoreAdapter's index
 *     refresh is asynchronous (it checks mtime diff since last sync on the next
 *     search, not on write completion). The retry guard is capped at 30 s with
 *     clear failure messages so a legitimate indexing delay doesn't false-fail.
 */

import { test, expect, type Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { startNewChat, waitForConnected } from './fixtures/selectors';

// ── Constants ─────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// Unique nonce for this test run.  Using Date.now() rather than Math.random()
// (no seeding issues in Playwright's Node environment).  Prefixed with a
// human-readable tag so grep/logs are clear.
const NONCE = `MEMTEST-${Date.now()}`;

// ── Auth helpers (same pattern as retention.spec.ts and cancel-cross-channel.spec.ts) ──

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(new URL(import.meta.url).pathname),
      'fixtures/.auth/admin.json',
    );

function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) return null;
  try {
    const raw = fs.readFileSync(AUTH_FILE, 'utf-8');
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string;
        localStorage?: Array<{ name: string; value: string }>;
      }>;
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

// ── DOM helpers ───────────────────────────────────────────────────────────────

/** Chat input — same selector as fixtures/selectors.ts chatInput(). */
const chatInput = (page: Page) =>
  page.locator('textarea[aria-label="Message input"]');

/**
 * Completed assistant messages — excludes the in-progress streaming placeholder.
 * Mirrors fixtures/selectors.ts assistantMessages().
 */
const assistantMessages = (page: Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])');

// ── Turn completion helpers ───────────────────────────────────────────────────

/**
 * Wait for any follow-up LLM call that the agent may start after a tool call.
 *
 * When an agent calls remember() as its first action the stream ends briefly
 * (isStreaming=false), making the assistantMessages count increment too early.
 * A second LLM call then starts to produce the text reply.  This helper watches
 * for the stop button to reappear within gapMs and, if it does, waits for it to
 * vanish again.
 *
 * Mirrors waitForTurnFullyDone() from chat.spec.ts (shared harness pattern).
 */
async function waitForTurnFullyDone(page: Page, gapMs = 10_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    // Stop button reappeared — a follow-up LLM call is in progress.
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — turn is fully done.
  }
}

// ── Workspace ID resolver ─────────────────────────────────────────────────────

/**
 * Resolve the active workspace ID so we can check the shared memory room.
 *
 * Strategy (most-to-least robust):
 *   1. Query GET /api/v1/workspaces and return the default workspace ID
 *      (is_default: true) or, failing that, the first active workspace.
 *   2. Fall back to scanning $OMNIPUS_HOME/workspaces/<id>/ directories on disk —
 *      works even when the API is unavailable or returns an unexpected shape.
 *
 * Returns the workspace ID string, or null when neither strategy finds one.
 * A null return means memories may have gone to the private agent room — the
 * disk assertion should still check there.
 *
 * Per pkg/memrooms/rooms.go: when a session is workspace-scoped, `remember` with
 * no explicit `room` defaults to the SHARED workspace room:
 *   $OMNIPUS_HOME/workspaces/<workspaceId>/.omnipus/memories/
 */
async function resolveWorkspaceId(page: Page): Promise<string | null> {
  // Strategy 1: REST API
  try {
    const resp = await page.request.get(`${BASE_URL}/api/v1/workspaces`, {
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
        // Prefer the explicitly-flagged default workspace.
        const def = workspaces.find((w) => w.is_default === true);
        if (def?.id) return def.id;
        // Otherwise take the first active workspace.
        const first = workspaces.find((w) => !w.status || w.status === 'active') ?? workspaces[0];
        if (first?.id) return first.id;
      }
    }
  } catch {
    // Network error — fall through to disk scan.
  }

  // Strategy 2: Disk scan
  // $OMNIPUS_HOME/workspaces/ contains one directory per workspace (named by its ID)
  // plus a <id>.json file per workspace.  We want directory names that are not .json.
  const workspacesDir = path.join(OMNIPUS_HOME, 'workspaces');
  if (fs.existsSync(workspacesDir)) {
    try {
      const entries = fs.readdirSync(workspacesDir, { withFileTypes: true });
      const wsDirs = entries
        .filter((e) => e.isDirectory())
        .map((e) => e.name);
      if (wsDirs.length > 0) {
        // Return the first directory found — in practice there is exactly one
        // default workspace on a fresh e2e install.
        return wsDirs[0];
      }
    } catch {
      // Scan failed — return null.
    }
  }

  return null;
}

// ── Disk-assertion helpers ────────────────────────────────────────────────────

/**
 * Scan a single memories directory for a file containing `needle`.
 * Returns the matching file path or null if none is found.
 */
function findMemoryInDir(memoriesDir: string, needle: string): string | null {
  if (!fs.existsSync(memoriesDir)) return null;
  let entries: fs.Dirent[];
  try {
    entries = fs.readdirSync(memoriesDir, { withFileTypes: true });
  } catch {
    return null;
  }
  for (const entry of entries) {
    if (!entry.name.endsWith('.md')) continue;
    const fullPath = path.join(memoriesDir, entry.name);
    try {
      const content = fs.readFileSync(fullPath, 'utf-8');
      if (content.includes(needle)) return fullPath;
    } catch {
      // Skip unreadable files
    }
  }
  return null;
}

/**
 * Search for a memory file containing `needle` in ALL candidate room directories.
 *
 * Search order:
 *   1. Shared workspace room: $OMNIPUS_HOME/workspaces/<workspaceId>/.omnipus/memories/
 *      (primary target when session is workspace-scoped — pkg/memrooms/rooms.go
 *      DefaultRoomScope returns "shared" when a workspace room is available)
 *   2. Agent private room:    $OMNIPUS_HOME/agents/<agentId>/.omnipus/memories/
 *      (fallback when no workspace is associated or tool was called with room="private")
 *
 * Returns the first matching file path, or null if none found in any location.
 */
function findMemoryFileContaining(
  agentId: string,
  needle: string,
  workspaceId: string | null,
): string | null {
  // 1. Shared workspace room (primary — where workspace-scoped `remember` writes)
  if (workspaceId) {
    const sharedMemoriesDir = path.join(
      OMNIPUS_HOME,
      'workspaces',
      workspaceId,
      '.omnipus',
      'memories',
    );
    const found = findMemoryInDir(sharedMemoriesDir, needle);
    if (found) return found;
  }

  // 2. Agent private room (fallback)
  const privateMemoriesDir = path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus', 'memories');
  return findMemoryInDir(privateMemoriesDir, needle);
}

/**
 * Poll findMemoryFileContaining until it returns a path or `timeoutMs` elapses.
 * Returns the found path, or throws with an actionable failure message.
 */
async function waitForMemoryFileDisk(
  agentId: string,
  workspaceId: string | null,
  nonce: string,
  timeoutMs = 30_000,
  intervalMs = 500,
): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const found = findMemoryFileContaining(agentId, nonce, workspaceId);
    if (found) return found;
    await new Promise<void>((r) => setTimeout(r, intervalMs));
  }

  // Timeout — build a diagnostic failure message covering both candidate dirs.
  const sharedMemoriesDir = workspaceId
    ? path.join(OMNIPUS_HOME, 'workspaces', workspaceId, '.omnipus', 'memories')
    : null;
  const privateMemoriesDir = path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus', 'memories');

  function listDir(dir: string): string {
    if (!dir || !fs.existsSync(dir)) return `(dir does not exist: ${dir})`;
    try {
      const files = fs.readdirSync(dir).filter((f: string) => f.endsWith('.md'));
      return files.length === 0
        ? `(dir exists but contains no .md files)`
        : files.slice(0, 10).join(', ') + (files.length > 10 ? ` … (${files.length} total)` : '');
    } catch {
      return '(could not list dir)';
    }
  }

  throw new Error(
    [
      `BLOCKED or INCOMPLETE: No memory file containing nonce "${nonce}" appeared`,
      `  within ${timeoutMs / 1000}s after the remember tool call.`,
      `  Agent ID used: ${agentId}`,
      `  Workspace ID used: ${workspaceId ?? '(none resolved)'}`,
      '',
      '  Searched locations (in priority order):',
      sharedMemoriesDir
        ? `  [1] Shared workspace room: ${sharedMemoriesDir}`
        : '  [1] Shared workspace room: (skipped — no workspace ID)',
      sharedMemoriesDir ? `      .md files: ${listDir(sharedMemoriesDir)}` : '',
      `  [2] Agent private room: ${privateMemoriesDir}`,
      `      .md files: ${listDir(privateMemoriesDir)}`,
      '',
      'This failure means the remember tool call either:',
      '  (a) was never emitted by the LLM (check the chat transcript above),',
      '  (b) was emitted but the tool handler did not write the .md file',
      '      (pkg/tools/memory.go → pkg/memrooms/memory_file.go WriteMemoryFile),',
      '  (c) wrote to a different workspace ID or OMNIPUS_HOME than expected.',
      '',
      'Expected write path (workspace-scoped session):',
      '  $OMNIPUS_HOME/workspaces/<workspaceId>/.omnipus/memories/<ulid>.md',
      '  (pkg/memrooms/rooms.go DefaultRoomScope → "shared" when shared room is set)',
      '',
      'This is a REAL gap, not a test infrastructure problem.',
      'Traces to: pkg/tools/memory.go (remember handler) → pkg/memrooms/memory_file.go (WriteMemoryFile)',
    ].join('\n'),
  );
}

// ── Default-agent resolver ────────────────────────────────────────────────────

/**
 * Resolve the default agent's ID by querying GET /api/v1/agents.
 *
 * The default agent has `default: true` in the wire response
 * (pkg/config/config.go: AgentConfig.Default bool `json:"default,omitempty"`).
 * coreagent.SeedConfig seeds Mia as default on fresh install.
 *
 * Returns null when no agent is marked default (should never happen on a
 * properly seeded gateway; caller must handle gracefully).
 */
async function resolveDefaultAgentId(page: Page): Promise<string | null> {
  const resp = await page.request.get(`${BASE_URL}/api/v1/agents`, {
    headers: await apiHeaders(page),
    failOnStatusCode: false,
  });
  if (!resp.ok()) return null;
  const agents = (await resp.json()) as Array<{ id: string; default?: boolean; name?: string }>;
  const def = agents.find((a) => a.default === true);
  if (def) return def.id;
  // Fallback: try name "Mia" (case-insensitive) — the canonical default on fresh installs.
  const mia = agents.find((a) => /mia/i.test(a.name ?? ''));
  return mia?.id ?? null;
}

// ── The test ──────────────────────────────────────────────────────────────────

test(
  'remember→recall round-trip: nonce stored via remember tool and retrieved via recall_memory',
  async ({ page }) => {
    // Budget: app load(15) + agent resolution(5) + workspace resolution(5)
    //         + turn1 remember(120) + disk wait(30)
    //         + turn2 recall(120) + chat assertion(15) = 310s → 360s with margin.
    // glm-5.2 is slower than gemini-flash; the extra headroom matches T24b's budget.
    test.setTimeout(360_000);

    // ── Arrange ──────────────────────────────────────────────────────────────
    await page.goto('/');
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 });

    // Resolve the default agent's ID BEFORE sending any messages.  We need it to
    // locate the on-disk memories directory.  Must be called after page.goto('/')
    // so cookies are set and authHeaders can read the CSRF cookie.
    const agentId = await resolveDefaultAgentId(page);
    if (!agentId) {
      throw new Error(
        'BLOCKED: Could not resolve the default agent ID from GET /api/v1/agents. ' +
          'The gateway may not be seeded correctly (coreagent.SeedConfig seeds Mia as default). ' +
          'Disk assertions for the remember tool require knowing which agent ID to check.',
      );
    }

    // Resolve the workspace ID so we can check the shared memory room.
    // When the e2e chat is workspace-scoped (/#/workspaces/<id>/chat), the
    // `remember` tool with no explicit room defaults to the SHARED workspace room:
    //   $OMNIPUS_HOME/workspaces/<workspaceId>/.omnipus/memories/
    // (pkg/memrooms/rooms.go: DefaultRoomScope returns "shared" when Shared room is set)
    // A null here means we fall back to the private agent room in the disk assertion.
    const workspaceId = await resolveWorkspaceId(page);
    // eslint-disable-next-line no-console
    console.log(
      `[memory-remember-recall] agentId=${agentId} workspaceId=${workspaceId ?? '(none)'}`,
    );

    // Start a fresh session so we control the message count exactly.
    //
    // TEST BUG (fixed): this used to click a "New Chat" button scoped inside
    // `getByRole('banner')`. That button was removed from the header in the
    // workspace top-bar redesign (src/components/chat/ChatControls.tsx: "New
    // Chat was removed from the header — three paths for one action was
    // redundant... It lives where the user already is: the sidebar's
    // per-workspace 'New chat' row and the /new slash command."). The banner
    // no longer contains any element with that accessible name, so the old
    // `toBeVisible` assertion always failed — not LLM nondeterminism, not a
    // slash-command regression, just a stale selector. `startNewChat`
    // (tests/e2e/fixtures/selectors.ts) is the sanctioned replacement — it
    // drives the actual current mechanism ("/new" + Enter, intercepted
    // client-side by useSlashMenu's `runClientCommand('new')`) and is already
    // used by chat.spec.ts for the same purpose.
    await startNewChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    const input = chatInput(page);
    await expect(input).toBeEnabled({ timeout: 15_000 });
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 15_000 });

    // ── Turn 1: remember the nonce ────────────────────────────────────────────
    //
    // BDD:
    //   Given an empty chat session with the default agent
    //   When the user sends a forceful instruction to call the remember tool
    //   Then the assistant emits exactly one remember tool call
    //   And a .md memory file containing the nonce appears on disk
    //     (in the shared workspace room if session is workspace-scoped,
    //      or in the private agent room if not workspace-scoped)
    //
    // Prompt strategy (glm-class): single imperative sentence, tool name
    // spelled out exactly, no wiggle room for prose-only reply.
    //
    // SPEC BUG (fixed): the category instructed here used to be "test", which
    // is not one of the `remember` tool's three enum values (pkg/tools/memory.go:159
    // — `key_decision` | `reference` | `lesson_learned`). glm-5.2 correctly
    // refused to call the tool with an invalid category and asked a
    // clarifying question instead ("'test' isn't a valid option. Which of
    // those three would you like me to use?"), so no memory file was ever
    // written — not LLM flakiness, a genuinely invalid instruction. "reference"
    // is the closest semantic fit for an arbitrary stored fact/nonce.
    await input.fill(
      `Call the remember tool NOW. Store exactly this fact as the memory content: "${NONCE}". ` +
        `Use category "reference". Do not write anything else. Call remember immediately.`,
    );
    await input.press('Enter');

    // Wait for the first assistant message to complete.  The agent may do:
    //   (a) remember tool call → text reply (two streaming events)
    //   (b) remember tool call with inline acknowledgement
    // waitForTurnFullyDone handles both by detecting the stop button reappearing.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 120_000 });
    await waitForTurnFullyDone(page, 10_000);

    // ── Disk assertion: memory file must exist ────────────────────────────────
    //
    // The `remember` tool in a workspace-scoped session writes to:
    //   $OMNIPUS_HOME/workspaces/<workspaceId>/.omnipus/memories/<ulid>.md
    // (pkg/memrooms/rooms.go: DefaultRoomScope returns "shared" when Shared room is set)
    //
    // The assertion also checks the private room as a fallback:
    //   $OMNIPUS_HOME/agents/<agentId>/.omnipus/memories/<ulid>.md
    //
    // We poll for up to 30s to allow the async index flush to settle.
    // A failure here means the remember tool handler never wrote the file —
    // this is a REAL gap and must be investigated, not silently skipped.
    const memFilePath = await waitForMemoryFileDisk(agentId, workspaceId, NONCE, 30_000, 500);
    // memFilePath is a non-null resolved path — log it for CI diagnostics.
    // (Playwright's reporter captures console.log output.)
    // eslint-disable-next-line no-console
    console.log(`[memory-remember-recall] Memory file written: ${memFilePath}`);

    // ── Turn 2: recall the nonce ──────────────────────────────────────────────
    //
    // BDD:
    //   Given the nonce was stored in the previous turn
    //   When the user sends a forceful instruction to call recall_memory and echo the result
    //   Then the assistant's reply contains the nonce verbatim
    //
    // Prompt strategy: name the tool explicitly, use the nonce as the query
    // keyword so the bleve search finds it, demand the exact string echoed back.
    await input.fill(
      `Call the recall_memory tool NOW with query "${NONCE}". ` +
        `Then reply with the EXACT content you found — copy it verbatim. ` +
        `Call recall_memory immediately, then echo what it returns.`,
    );
    await input.press('Enter');

    await expect(assistantMessages(page)).toHaveCount(2, { timeout: 120_000 });
    await waitForTurnFullyDone(page, 10_000);

    // ── Assert: nonce appears in the rendered chat transcript ─────────────────
    //
    // We look across ALL completed assistant messages because the model may have
    // included the nonce in its turn-1 acknowledgement, the turn-2 recall reply,
    // or both.  Any occurrence suffices — the point is that the recall chain
    // surfaced the nonce from persistent storage.
    const msgsWithNonce = assistantMessages(page).filter({ hasText: NONCE });
    const count = await msgsWithNonce.count();

    if (count === 0) {
      // Collect the full text of all assistant messages for the failure report.
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
          `BLOCKED or INCOMPLETE: Nonce "${NONCE}" does NOT appear in any completed assistant message`,
          `  after calling recall_memory with it as the query.`,
          '',
          `  Memory file was written to: ${memFilePath}`,
          `  (remember tool DID write to disk — the gap is in recall_memory or the index)`,
          '',
          `  Assistant messages (${msgCount} total):`,
          ...msgTexts.map((t, i) => `    [${i}] ${t.slice(0, 200)}`),
          '',
          'This failure means one of:',
          '  (a) recall_memory was never called (LLM disobeyed; try a more forceful prompt)',
          '  (b) recall_memory was called but the bleve index did not find the memory',
          '      (pkg/agent/memory.go SearchEntries — check index flush timing)',
          '  (c) recall_memory found the memory but the LLM did not echo the nonce back',
          '',
          'Traces to: pkg/agent/memory.go (SearchEntries) → pkg/tools/memory.go (RecallMemory handler)',
        ].join('\n'),
      );
    }

    // Explicit assertion so Playwright reports it as a named check.
    expect(
      count,
      `Nonce "${NONCE}" must appear in at least one completed assistant message after recall_memory`,
    ).toBeGreaterThanOrEqual(1);
  },
);
