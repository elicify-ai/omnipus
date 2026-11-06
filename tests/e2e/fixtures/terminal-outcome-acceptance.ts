import fs from 'node:fs';
import { expect, type Page, type TestInfo, type WebSocket as BrowserSocket } from '@playwright/test';
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas';
import { Agent, Workspace, Session, SessionDetail } from '../../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest, SessionCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './gateway-process';
import { chatInput, waitForConnected } from './selectors';
import { e2eAgentModelFields } from './e2e-agent-model';

// Independent oracle: tool-iteration-limit spec, Machine-Verifiable Constraints,
// Tool-limit message. Never import the production notice/translation helper.
export const TERMINAL_CAP_NOTICE = "I've reached this agent's limit of tool steps for one turn without a final response. An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), and each agent's own lower limit is on its profile's Advanced tab.";
export const TERMINAL_REASON = "I've reached this agent's limit of tool steps for one turn without a final response.";

export async function createTerminalSession(gw: GatewayProcess) {
  const agentResult = await gw.apiFetch('POST', '/api/v1/agents', {
    type: 'Main', name: 'Terminal acceptance', soul: 'Run the requested tool and report its result.',
    ...e2eAgentModelFields('mock'), max_tool_iterations: 1,
  } satisfies AgentCreateRequestMain);
  expect(agentResult.ok, agentResult.raw).toBe(true);
  const agent = Agent.parse(agentResult.body);
  expect(agent.max_tool_iterations, 'fixture: the real effective tool-round limit').toBe(1);
  const workspaceResult = await gw.apiFetch('POST', '/api/v1/workspaces', {
    name: 'Terminal acceptance', core_team: [agent.id],
  } satisfies WorkspaceCreateRequest);
  expect(workspaceResult.ok, workspaceResult.raw).toBe(true);
  const workspace = Workspace.parse(workspaceResult.body);
  const sessionResult = await gw.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: agent.id, workspace_id: workspace.id,
  } satisfies SessionCreateRequest);
  expect(sessionResult.ok, sessionResult.raw).toBe(true);
  // A second, untouched session of the same workspace. The SPA deliberately
  // sends no second attach_session for a session that is already active
  // (sessions.$sessionId.tsx: "An already-active session normally needs no
  // second attach"), so a user can only trigger a FRESH attach of the terminal
  // session by switching to another session and coming back via its link.
  const otherResult = await gw.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: agent.id, workspace_id: workspace.id,
  } satisfies SessionCreateRequest);
  expect(otherResult.ok, otherResult.raw).toBe(true);
  return { agent, session: Session.parse(sessionResult.body), otherSession: Session.parse(otherResult.body) };
}

/** Collect real browser traffic, without routing/stubbing any gateway frame.
 * Keep frame decode failures visible. Never record outbound auth credentials.
 */
export function recordTerminalTraffic(page: Page) {
  const frames: WsFrame[] = [];
  const attaches: Extract<WsFrame, { type: 'attach_session' }>[] = [];
  const sockets: BrowserSocket[] = [];
  const failures: string[] = [];
  page.on('websocket', (socket) => {
    sockets.push(socket);
    socket.on('framereceived', ({ payload }) => {
      try {
        frames.push(WsFrame.parse(JSON.parse(payload.toString())));
      } catch (error) {
        failures.push(`received frame decode: ${String(error)}`);
      }
    });
    socket.on('framesent', ({ payload }) => {
      try {
        const raw: unknown = JSON.parse(payload.toString());
        // Validate outgoing frames, but retain only non-secret reattach facts.
        const frame = WsFrame.parse(raw);
        if (frame.type === 'attach_session') attaches.push(frame);
      } catch (error) {
        failures.push(`sent frame decode: ${String(error)}`);
      }
    });
  });
  return { frames, attaches, sockets, failures };
}

export async function openTerminalSession(page: Page, baseURL: string, sessionId: string) {
  await page.goto(`${baseURL}/#/sessions/${sessionId}`);
  await expect(page.locator(`[data-active-session-id="${sessionId}"]`)).toBeVisible();
  await expect(chatInput(page)).toBeVisible();
  await expect(chatInput(page)).toBeEnabled();
  await waitForConnected(page);
}

export async function turnVerboseOff(page: Page, baseURL: string) {
  await page.goto(`${baseURL}/#/settings?tab=chat`);
  const toggle = page.getByRole('switch', { name: 'Verbose chat' });
  await expect(toggle).toBeVisible();
  // Drive the shipped setting, not a synthetic localStorage state. A deliberate
  // on->off roundtrip also proves the OFF value is persisted before reload.
  if (await toggle.getAttribute('aria-checked') !== 'true') {
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-checked', 'true');
  }
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
}

export function terminalLogRows(logPath: string): Record<string, unknown>[] {
  const raw = fs.readFileSync(logPath, 'utf8');
  // A file may be observed while its final JSONL row is still being written.
  // Read only complete rows; final outcome assertions require the complete row.
  const lastNewline = raw.lastIndexOf('\n');
  return raw.slice(0, lastNewline + 1).split('\n').filter(Boolean)
    .map((line) => JSON.parse(line) as Record<string, unknown>);
}

export async function terminalTranscript(page: Page, baseURL: string, sessionId: string) {
  // Browser login replaced this admin's API-fixture token. Use the browser's
  // current cookie jar, never GatewayProcess.apiFetch after that login.
  const response = await page.request.get(`${baseURL}/api/v1/sessions/${sessionId}`);
  expect(response.ok(), await response.text()).toBe(true);
  return SessionDetail.parse(await response.json());
}

export function terminalLiveEntries(frames: WsFrame[], sessionId: string, turnId: string) {
  const byId = new Map<string, string>();
  const tokensById = new Map<string, string>();
  let noticeFrames = 0;
  for (const frame of frames) {
    if (frame.type === 'token' && frame.session_id === sessionId) {
      expect.soft(frame.turn_id, 'live token is from the SAME observed turn').toBe(turnId);
      expect.soft(frame.message_id, 'live token has a stable durable-entry identity').toEqual(expect.any(String));
      if (!frame.message_id) continue; // The preceding soft assertion is a failure, not a skip.
      const content = frame.replace ? frame.content : (tokensById.get(frame.message_id) ?? '') + frame.content;
      tokensById.set(frame.message_id, content);
      byId.set(frame.message_id, content);
    } else if (frame.type === 'replay_message' && frame.session_id === sessionId && frame.content === TERMINAL_CAP_NOTICE) {
      noticeFrames++;
      expect.soft(frame.role, 'live cap notice must be system-authored').toBe('system');
      expect.soft(frame.turn_id, 'live notice is from the SAME observed turn').toBe(turnId);
      expect.soft(frame.id, 'live notice has a stable durable-entry identity').toEqual(expect.any(String));
      expect.soft(frame.model, 'live harness notice has no model attribution').toBeUndefined();
      if (!frame.id) continue; // The preceding assertion and exact live/durable pair still fail.
      byId.set(frame.id, frame.content);
    }
  }
  expect.soft(noticeFrames, 'exactly one live system cap-notice frame').toBe(1);
  expect.soft(Array.from(tokensById.values()).join(''), 'the cap notice is not a model-token answer')
    .not.toContain(TERMINAL_CAP_NOTICE);
  return Array.from(byId, ([id, content]) => ({ id, content }));
}

export async function assertTerminalRendering(
  page: Page, info: TestInfo, stage: string, terminalEntries: ReturnType<typeof SessionDetail.parse>['messages'],
) {
  if (terminalEntries === undefined) {
    throw new Error(`${stage}: terminal transcript messages are missing; cannot verify durable entry identities`);
  }
  const terminal = page.locator('[data-message-role="system"][data-message-id]').filter({ hasText: TERMINAL_CAP_NOTICE });
  // Soft product assertions preserve RED while allowing reconnect/reload to be
  // measured too. A missing notice NEVER bypasses a later observation.
  await expect.soft(terminal, `${stage}: exactly one completed terminal system row`).toHaveCount(1);
  await expect.soft(page.getByText(TERMINAL_CAP_NOTICE, { exact: true }), `${stage}: exact terminal sentence`).toHaveCount(1);
  expect.soft(await terminal.evaluateAll((elements) => elements.map((el) => el.getClientRects().length > 0)),
    `${stage}: the terminal system row is actually visible`).toEqual([true]);
  expect.soft(await terminal.evaluateAll((elements) => elements.map((el) => el.getAttribute('data-message-id'))),
    `${stage}: rendered identity equals the real durable terminal entry`).toEqual(terminalEntries.map((entry) => entry.id));
  expect.soft(await page.evaluate(() => {
    const raw = localStorage.getItem('omnipus-chat-preferences');
    return raw !== null && JSON.parse(raw).state.verboseChatEnabled === false;
  }), `${stage}: actual persisted Verbose preference stays OFF`).toBe(true);
  await info.attach(`${stage}-verbose-off`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
}
