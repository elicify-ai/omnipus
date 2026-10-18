/**
 * recap-continuity.spec.ts — idle recap → pair carry-forward → fresh chat.
 *
 * Binding oracle ("amendment" in the assertion comments below):
 * /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md
 * §3.2 items 1–5, 8 (lines 280–284, 287); §3.3 (line 293);
 * §3.4 (line 297); reachability (line 501). WU-21 / #1197 overrides the
 * former agent-wide continuity oracle. Corroborated by the system-agent
 * spec FR-197, carry-forward part of FR-218 and §15 WU-21; ADR OSA-16.
 *
 * The real idle ticker still drives the recap; no retired session_close
 * frame or mocked gateway/model is used. The publication path is now
 * workspaces/<workspace>/.omnipus/carry-forward/<agent>.md. Polling that
 * file alone is not enough: fresh chats must prove its user-visible effect.
 *
 * Given a fresh chat with one agent in workspace A and auto recap enabled,
 * when the user states a unique, conversation-only fact and the chat idles,
 * then A's carry-forward for that agent contains the fact (amendment 281–282).
 * When a NEW, empty chat with the SAME agent in A asks about it, its completed
 * reply contains the fact (amendment 280, 283–284).
 * When a NEW, empty chat with that agent in workspace B asks the same question,
 * its completed reply does NOT contain A's fact (amendment 280, 283–284, 289).
 *
 * Deliberate private notes remain agent-wide (amendment 287–293). All turns
 * therefore request no tools and their real transcript tool lists must be
 * empty: neither remember nor recall can substitute for recap injection.
 * B uses existing workspace-create REST and freshChat helpers, not new fixtures.
 * The existing CSRF handling, settings restore and wait budgets stay intact.
 */

import { test, expect, type Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { chatInput, assistantMessages } from './fixtures/selectors';
import { freshChat, originOf } from './fixtures/session-setup';
import { SessionDetail, Workspace } from '../../src/lib/api/generated/schemas';
import type { WorkspaceCreateRequest, WorkspaceUpdateRequest } from '../../src/lib/api/generated/openapi-types';
import { DoneFrame, ErrorFrame } from '../../src/lib/api/generated/ws-schemas';

// Never default a filesystem-observing spec to the operator's real data home.
const OMNIPUS_HOME = process.env.OMNIPUS_HOME;

/** One idle timeout of margin on top of the ticker's own minute. */
const IDLE_TIMEOUT_MINUTES = 1;
const IDLE_WAIT_MS = (IDLE_TIMEOUT_MINUTES + 1) * 60_000;
/** The recap pipeline's own overall budget, plus margin (see pollCarryForward). */
const RECAP_BUDGET_MS = 120_000;

interface MemorySettingsWire {
  auto_recap_enabled?: boolean;
  idle_timeout_minutes?: number;
  bootstrap_recap_enabled?: boolean;
  bootstrap_recap_max_per_minute?: number;
  session_days?: number;
  memory_retros_days?: number;
}

/**
 * The CSRF token for this page's own session, from the page's cookie jar —
 * the same rule fixtures/session-setup.ts::pageCsrfHeaders applies, so a
 * mutating request from a spec is authorised exactly like one from the SPA.
 * Omitting it is what S5-02 caught: the PUT was rejected, and the swallowed
 * restore then leaked the settings it was meant to undo.
 */
async function pageCsrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf');
  return csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {};
}

async function putMemorySettings(page: Page, body: MemorySettingsWire): Promise<void> {
  const resp = await page.request.put(`${originOf(page)}/api/v1/settings/memory`, {
    headers: { 'Content-Type': 'application/json', ...(await pageCsrfHeaders(page)) },
    data: JSON.stringify(body),
  });
  expect(
    resp.ok(),
    `PUT /api/v1/settings/memory failed: ${resp.status()} ${await resp.text()}`,
  ).toBe(true);
}

function isolatedOmnipusHome(): string {
  if (!OMNIPUS_HOME) {
    throw new Error('recap-continuity requires an explicit, isolated OMNIPUS_HOME.');
  }
  const home = fs.realpathSync(OMNIPUS_HOME);
  const operatorHomes = [
    path.resolve(process.env.HOME || '', '.omnipus'),
    '/Users/danielpiatkowski/.omnipus',
  ];
  if (operatorHomes.some((operatorHome) => home === operatorHome || home.startsWith(`${operatorHome}${path.sep}`))) {
    throw new Error('recap-continuity refuses to read or write the operator data home.');
  }
  return home;
}

/** Amendment line 281 (§3.2 item 2): the pair file, never an agent-wide fallback. */
function carryForwardPath(agentId: string, workspaceId: string): string {
  return path.join(isolatedOmnipusHome(), 'workspaces', workspaceId, '.omnipus', 'carry-forward', `${agentId}.md`);
}

/**
 * Poll the file for THIS session's immutable pair (amendment 280–282).
 * The existing budget exceeds the recap pipeline's overall cap, unchanged.
 */
async function pollCarryForward(page: Page, agentId: string, workspaceId: string, needle: string): Promise<string> {
  const summaryPath = carryForwardPath(agentId, workspaceId);
  const deadline = Date.now() + RECAP_BUDGET_MS;
  let lastContents = '';
  while (Date.now() < deadline) {
    if (fs.existsSync(summaryPath)) {
      lastContents = fs.readFileSync(summaryPath, 'utf-8');
      if (lastContents.includes(needle)) return lastContents;
    }
    await page.waitForTimeout(2_000);
  }
  throw new Error(
    `recap-continuity: pair carry-forward did not carry ${needle} within ${RECAP_BUDGET_MS / 1000}s.\n` +
      `Agent: ${agentId}; workspace: ${workspaceId}; path: ${summaryPath}\n` +
      (fs.existsSync(summaryPath)
        ? `The file exists but does not contain the fact. Last contents:\n${lastContents.slice(0, 2000)}`
        : 'The pair file was never written — check the idle trigger and scoped recap publication.'),
  );
}

async function readSessionDetail(page: Page, sessionId: string): Promise<ReturnType<typeof SessionDetail.parse>> {
  const resp = await page.request.get(`${originOf(page)}/api/v1/sessions/${sessionId}`, {
    headers: await pageCsrfHeaders(page),
  });
  // Fixture oracle: contracts/openapi.yaml::getSession (200 + SessionDetail).
  expect(resp.status(), `GET session ${sessionId}: ${await resp.text()}`).toBe(200);
  return SessionDetail.parse(await resp.json());
}

/** Passive inbound-only capture, installed before navigation; never mocks a frame. */
function recordRecapCompletions(page: Page) {
  const done: ReturnType<typeof DoneFrame.parse>[] = [];
  const errors: ReturnType<typeof ErrorFrame.parse>[] = [];
  const malformed: string[] = [];
  page.on('websocket', (socket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return;
    socket.on('framereceived', ({ payload }) => {
      try {
        const frame: unknown = JSON.parse(payload.toString());
        if (typeof frame !== 'object' || frame === null || !('type' in frame)) {
          throw new Error('Chat WebSocket frame must be an object with a type');
        }
        if (frame.type === 'done') done.push(DoneFrame.parse(frame));
        if (frame.type === 'error') errors.push(ErrorFrame.parse(frame));
      } catch (error) {
        malformed.push(String(error)); // Asserted below, never logged-and-ignored.
      }
    });
  });
  return { done, errors, malformed, mark: () => ({ done: done.length, errors: errors.length }) };
}

type RecapTurnStart = ReturnType<ReturnType<typeof recordRecapCompletions>['mark']>;

async function expectToolFreeTranscript(page: Page, sessionId: string, start: RecapTurnStart): Promise<void> {
  // DoneFrame is the terminal signal, not optional Message.status. Ignore
  // attach/replay completions captured before this send, and unrelated sessions.
  // A real streamed reply has the turn/message ids specified by DoneFrame.
  const completions = () => traffic.done.slice(start.done).filter((frame) =>
    frame.session_id === sessionId && frame.turn_id !== undefined && frame.message_id !== undefined);
  const errors = () => traffic.errors.slice(start.errors).filter((frame) =>
    frame.session_id === undefined || frame.session_id === sessionId);
  await expect.poll(() => completions().length > 0 || errors().length > 0 || traffic.malformed.length > 0, {
    timeout: 90_000, message: `Session ${sessionId} must emit a live terminal frame for this send`,
  }).toBe(true);
  expect(traffic.malformed, 'Completion observer must decode the real gateway frames').toEqual([]);
  expect(errors(), `Session ${sessionId} must not emit a turn/provider error`).toEqual([]);
  const terminal = completions();
  expect(terminal, `Session ${sessionId} must complete exactly one live reply`).toHaveLength(1);
  // DoneStats contract: absent/false is normal; true means failed/truncated/replay error.
  expect(terminal[0].stats?.turn_failed ?? false, `Session ${sessionId} must complete successfully`).toBe(false);
  expect(terminal[0].stats?.truncated ?? false, `Session ${sessionId} must not end mid-reply`).toBe(false);
  expect(terminal[0].stats?.replay_error ?? false, `Session ${sessionId} must not use a replay-error completion`).toBe(false);

  const { messages } = await readSessionDetail(page, sessionId);
  // Amendment 287–293: private notes still cross workspaces; rule out that
  // alternate path and tool-based recall, so only the prompt injection is tested.
  expect(
    messages.filter((message) => message.type === 'tool_call' || (message.tool_calls?.length ?? 0) > 0),
    `Session ${sessionId} must not use tools to store or retrieve the conversation-only fact`,
  ).toEqual([]);
  // Amendment 501 and DoneFrame::message_id/turn_id: a real completed reply
  // must be durable and must be the SAME reply as the successful live terminal.
  const replies = messages.filter((message) => message.role === 'assistant' && (message.type ?? 'message') === 'message');
  expect(replies, `Session ${sessionId} must persist exactly one assistant reply`).toHaveLength(1);
  expect(replies[0].id, 'Live completion must name this durable assistant reply').toBe(terminal[0].message_id);
  expect(replies[0].turn_id, 'Live completion must close this durable reply’s turn').toBe(terminal[0].turn_id);
  // Message.status is optional; explicit error/interrupted values still fail.
  expect(replies.filter((reply) => reply.status === 'error' || reply.status === 'interrupted'),
    `Session ${sessionId} must not persist a failed or interrupted reply`).toEqual([]);
  expect((replies[0].content ?? '').trim(), `Session ${sessionId} must not return a blank reply`).not.toBe('');
  expect(replies[0].truncated ?? false, `Session ${sessionId} must not return a truncated reply`).toBe(false);
}

let baseline: MemorySettingsWire | null = null;
let isolationWorkspaceId: string | null = null;
let traffic: ReturnType<typeof recordRecapCompletions>;

test.beforeEach(async ({ page }) => {
  traffic = recordRecapCompletions(page);
  isolatedOmnipusHome();
  // PR #1260 (E2E ui-heavy): the hook read the gateway settings BEFORE any
  // navigation, so the page was still on about:blank and originOf() refused —
  // every attempt failed at line 131 with a message that (misleadingly) named
  // freshChat. Playwright hands a test a blank page; open the app first, the
  // same way every spec that reads the page's own origin does.
  await page.goto('/');
  const resp = await page.request.get(`${originOf(page)}/api/v1/settings/memory`, {
    headers: await pageCsrfHeaders(page),
  });
  expect(resp.ok(), `GET /api/v1/settings/memory failed: ${resp.status()}`).toBe(true);
  baseline = (await resp.json()) as MemorySettingsWire;
});

test.afterEach(async ({ page }) => {
  const b = baseline;
  baseline = null;
  try {
    // NOT swallowed (S5-02): a failed restore must fail loudly, never leak
    // a one-minute idle timeout to the later specs sharing this gateway.
    if (b) {
      await putMemorySettings(page, {
        auto_recap_enabled: b.auto_recap_enabled ?? false,
        idle_timeout_minutes: b.idle_timeout_minutes ?? 30,
        bootstrap_recap_enabled: b.bootstrap_recap_enabled ?? false,
        bootstrap_recap_max_per_minute: b.bootstrap_recap_max_per_minute ?? 5,
        session_days: b.session_days ?? 90,
        memory_retros_days: b.memory_retros_days ?? 180,
      });
    }
  } finally {
    // Cleanup still runs if an assertion or settings restoration failed.
    if (isolationWorkspaceId) {
      const workspaceId = isolationWorkspaceId;
      isolationWorkspaceId = null;
      const resp = await page.request.get(`${originOf(page)}/api/v1/workspaces/${workspaceId}`, {
        headers: await pageCsrfHeaders(page),
      });
      // Fixture oracle: contracts/openapi.yaml::getWorkspace and deleteWorkspace;
      // deleting requires the current revision, not a fabricated token.
      expect(resp.status(), `GET isolation workspace: ${await resp.text()}`).toBe(200);
      const workspace = Workspace.parse(await resp.json());
      const deleted = await page.request.delete(`${originOf(page)}/api/v1/workspaces/${workspaceId}`, {
        headers: await pageCsrfHeaders(page),
        params: { revision: workspace.revision },
      });
      expect(deleted.status(), `DELETE isolation workspace: ${await deleted.text()}`).toBe(200);
    }
  }
});

test(
  'recap_continuity: a fact carries into the same workspace but not another workspace',
  async ({ page }) => {
    // The existing 900s budget covers the idle wait (120s), recap poll (120s),
    // three completed model turns (90s each), setup and cleanup. No widening.
    test.setTimeout(900_000);

    await putMemorySettings(page, {
      auto_recap_enabled: true,
      idle_timeout_minutes: IDLE_TIMEOUT_MINUTES,
    });

    // Readiness and the real idle trigger remain unchanged.
    await expect(chatInput(page)).toBeVisible({ timeout: 20_000 });
    const first = await freshChat(page);
    const firstDetail = await readSessionDetail(page, first.sessionId);
    // Amendment 280 (§3.2 item 1): scope comes from this session's metadata.
    expect(firstDetail.session).toMatchObject({ id: first.sessionId, agent_id: first.agentId });
    const workspaceId = firstDetail.session.workspace_id;
    if (!workspaceId) {
      throw new Error('BLOCKED: recap-continuity fixture has no workspace — WU-21 amendment line 280 requires a pair scope.');
    }

    const nonce = `recap-nonce-${Date.now()}`;
    const input = chatInput(page);
    // Amendment 287–293: ban deliberate private-note writes, not automatic
    // workspace continuity. "For this conversation only / do not save" asked
    // the model to forget the very fact the next chat requires (run 38760311753:
    // "You flagged that codename as conversation-only", then UNKNOWN).
    // The tool-free transcript assertions below still exclude private memory.
    await input.fill(
      `The launch codename for this workspace is ${nonce}. ` +
      'Do not use any tools or write a private memory note. Just acknowledge it.',
    );
    const firstTurn = traffic.mark();
    await input.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 });
    await expectToolFreeTranscript(page, first.sessionId, firstTurn);

    // Amendment 281–282 (§3.2 items 2–3): exact pair publication with the fact.
    await page.waitForTimeout(IDLE_WAIT_MS);
    await pollCarryForward(page, first.agentId, workspaceId, nonce);

    // Amendment 280, 283–284: fresh session, same agent AND same workspace.
    const second = await freshChat(page, first.agentName);
    expect(second.sessionId).not.toBe(first.sessionId);
    expect(second.agentId).toBe(first.agentId);
    const secondDetail = await readSessionDetail(page, second.sessionId);
    expect(secondDetail.session).toMatchObject({ agent_id: first.agentId, workspace_id: workspaceId });
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 15_000 });
    const question = 'What was the launch codename I told you about? Answer with just the codename if you know it; otherwise say UNKNOWN. Do not use any tools.';
    const fresh = chatInput(page);
    await fresh.fill(question);
    const secondTurn = traffic.mark();
    await fresh.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 });
    // Amendment 283 (§3.2 item 4): keep the original user-visible nonce oracle,
    // not just an existence check on the carry-forward file.
    await expect(assistantMessages(page).first()).toContainText(nonce, { timeout: 90_000 });
    await expectToolFreeTranscript(page, second.sessionId, secondTurn);

    // Existing REST createWorkspace + freshChat(workspace) provide isolation;
    // no new fixture system or shared-fixture change is needed.
    const workspaceName = `Recap isolation ${Date.now()}`;
    const created = await page.request.post(`${originOf(page)}/api/v1/workspaces`, {
      headers: await pageCsrfHeaders(page),
      data: { name: workspaceName } satisfies WorkspaceCreateRequest,
    });
    // Fixture oracle: contracts/openapi.yaml::createWorkspace (201 + Workspace).
    expect(created.status(), `POST isolation workspace: ${await created.text()}`).toBe(201);
    const otherWorkspace = Workspace.parse(await created.json());
    isolationWorkspaceId = otherWorkspace.id;
    // FR-048: a new workspace is empty. Explicitly join the existing agent
    // through the ordinary membership PUT, rather than assuming a seeded team.
    expect(otherWorkspace.core_team ?? [], 'FR-048: isolation workspace starts empty').toEqual([]);
    const current = await page.request.get(`${originOf(page)}/api/v1/workspaces/${otherWorkspace.id}`, {
      headers: await pageCsrfHeaders(page),
    });
    expect(current.status(), `GET isolation workspace before joining: ${await current.text()}`).toBe(200);
    const currentWorkspace = Workspace.parse(await current.json());
    const joined = await page.request.put(`${originOf(page)}/api/v1/workspaces/${otherWorkspace.id}`, {
      headers: await pageCsrfHeaders(page),
      data: { revision: currentWorkspace.revision, core_team: [first.agentId] } satisfies WorkspaceUpdateRequest,
    });
    // Fixture oracle: contracts/openapi.yaml::updateWorkspace (200 + Workspace).
    expect(joined.status(), `Join isolation workspace: ${await joined.text()}`).toBe(200);
    const joinedWorkspace = Workspace.parse(await joined.json());
    // Amendment 280, 289; FR-049: different workspace, SAME agent reused unchanged.
    expect(otherWorkspace.id).not.toBe(workspaceId);
    expect(joinedWorkspace.id).toBe(otherWorkspace.id);
    expect(joinedWorkspace.core_team).toEqual([first.agentId]);
    // Amendment 297 (§3.4): a first session in a workspace has no summary.
    expect(fs.existsSync(carryForwardPath(first.agentId, otherWorkspace.id))).toBe(false);

    await page.reload(); // Refresh the sidebar's roster after the real REST create.
    const third = await freshChat(page, first.agentName, workspaceName);
    // Amendment 280, 283–284, 289: rule out another agent/session as a false control.
    expect(third.agentId).toBe(first.agentId);
    expect([first.sessionId, second.sessionId]).not.toContain(third.sessionId);
    const thirdDetail = await readSessionDetail(page, third.sessionId);
    expect(thirdDetail.session).toMatchObject({ agent_id: first.agentId, workspace_id: otherWorkspace.id });
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 15_000 });
    const isolated = chatInput(page);
    await isolated.fill(question); // The question contains no nonce, in either workspace.
    const thirdTurn = traffic.mark();
    await isolated.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 });
    await expectToolFreeTranscript(page, third.sessionId, thirdTurn);
    // Amendment 280, 283–284, 289, 501: a real completed reply, not an empty
    // thread or a missing file, proves A's recap did not reach B's session.
    await expect(assistantMessages(page).first()).not.toContainText(nonce);
  },
);
