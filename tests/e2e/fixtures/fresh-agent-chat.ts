import { expect, type Page } from '@playwright/test';
import { Session, SessionDetail, Workspace } from '../../../src/lib/api/generated/schemas';
import type { SessionCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { assistantMessages, chatInput, userMessages } from './selectors';
import { openSessionByDeepLink, originOf } from './session-setup';

/** #1769: create before waiting on the old composer's readiness. The previously
 * active main chat may genuinely be locked by a question from another test. */
export async function createFreshAgentSession(page: Page, agentId: string, origin: string) {
  const cookies = await page.context().cookies(origin);
  const csrf = cookies.find(cookie => cookie.name === 'csrf' || cookie.name === '__Host-csrf');
  const headers: Record<string, string> = csrf ? { 'X-CSRF-Token': csrf.value } : {};
  const roster = await page.request.get(`${origin}/api/v1/workspaces`);
  expect(roster.status(), 'fresh chat: read the real workspace roster').toBe(200);
  const workspace = Workspace.array().parse(await roster.json()).find(item => item.is_default);
  if (!workspace) throw new Error('BLOCKED: default workspace missing — required by #1769 isolated chats');
  const response = await page.request.post(`${origin}/api/v1/sessions`, {
    headers, data: { type: 'chat', agent_id: agentId, workspace_id: workspace.id } satisfies SessionCreateRequest,
  });
  expect(response.status(), `fresh chat: public create endpoint: ${await response.text()}`).toBe(201);
  const created = Session.parse(await response.json());
  expect(created.agent_id, 'fresh chat belongs to the intended agent').toBe(agentId);
  expect(created.type, 'fresh chat is not the shared main session').toBe('chat');
  expect(created.workspace_id, 'fresh chat belongs to the intended workspace').toBe(workspace.id);
  const detailResponse = await page.request.get(`${origin}/api/v1/sessions/${created.id}`);
  expect(detailResponse.status(), 'fresh chat: independently read stored session').toBe(200);
  const detail = SessionDetail.parse(await detailResponse.json());
  expect(detail.session.id, 'fresh chat: stored identity matches the create response').toBe(created.id);
  expect(detail.session.agent_id, 'fresh chat: stored owner is the intended agent').toBe(agentId);
  expect(detail.session.type, 'fresh chat: stored type is chat, never main').toBe('chat');
  expect(detail.session.workspace_id, 'fresh chat: stored workspace agrees').toBe(workspace.id);
  expect(detail.history_state.status, 'freshness cannot be inferred from unavailable history').toBe('available');
  expect(detail.messages, 'fresh chat has no prior messages on the server').toEqual([]);
  expect(detail.has_earlier, 'fresh chat has no hidden earlier history').toBe(false);
  expect(detail.has_later, 'fresh chat has no hidden later history').toBe(false);
  return { sessionId: created.id, agentId, workspaceId: workspace.id };
}

export async function freshAgentChat(page: Page, agentId: string, origin = originOf(page)) {
  const created = await createFreshAgentSession(page, agentId, origin);
  await openSessionByDeepLink(page, created.sessionId, origin);
  await expect(userMessages(page), 'fresh chat has no prior user messages in the UI').toHaveCount(0);
  await expect(assistantMessages(page), 'fresh chat has no prior assistant messages in the UI').toHaveCount(0);
  await expect(chatInput(page), 'fresh chat composer is usable independently of the main chat').toBeEnabled();
  return created;
}
