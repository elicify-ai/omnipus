// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import type { Page } from '@playwright/test';
import type { Session, SessionDetail, Workspace } from '../../../src/lib/api/generated/openapi-types';
import { createFreshAgentSession } from './fresh-agent-chat';

// Unit boundary: public HTTP client and cookie jar only. The real API helper
// and all ownership/history checks run; E2E covers the real deep link and UI.
const origin = 'http://fixture.local';
const at = '2026-11-11T00:00:00Z';
const workspace: Workspace = { revision: 'a'.repeat(64), id: 'workspace-1769', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0, task_count: 0, is_default: true, created_at: at, updated_at: at };
const session: Session = { id: 'fresh-1769', agent_id: 'jim', type: 'chat', title: '', status: 'active', created_at: at, updated_at: at, channel: 'webchat', partitions: [], workspace_id: workspace.id,
  stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 } };
const detail: SessionDetail = { session, history_state: { status: 'available' }, messages: [], has_earlier: false, has_later: false };
function httpEdge(created: Session = session, history: SessionDetail = detail, createStatus = 201) {
  const response = (body: unknown, status = 200) => ({ status: () => status, json: async () => body, text: async () => JSON.stringify(body) });
  const request = { get: vi.fn(async (url: string) => {
    if (url === `${origin}/api/v1/workspaces`) return response([workspace]);
    if (url === `${origin}/api/v1/sessions/${created.id}`) return response(history);
    throw new Error(`unexpected HTTP URL: ${url}`);
  }), post: vi.fn(async () => response(created, createStatus)) };
  const page = { request, context: () => ({ cookies: async () => [{ name: 'csrf', value: 'fixture-csrf' }] }) } as unknown as Page;
  return { page, request };
}
describe('#1769 fresh chat setup guards', () => {
  it('uses the exact create body and independently validates empty stored history before returning the exact deep-link identity', async () => {
    const edge = httpEdge();
    expect(await createFreshAgentSession(edge.page, 'jim', origin)).toEqual({ sessionId: session.id, agentId: 'jim', workspaceId: workspace.id });
    expect(edge.request.post).toHaveBeenCalledExactlyOnceWith(`${origin}/api/v1/sessions`, {
      headers: { 'X-CSRF-Token': 'fixture-csrf' }, data: { agent_id: 'jim', type: 'chat', workspace_id: workspace.id },
    });
    expect(edge.request.get.mock.calls).toEqual([[`${origin}/api/v1/workspaces`], [`${origin}/api/v1/sessions/${session.id}`]]);
  });
  it('rejects the shared main session even if its history happens to be empty', async () => {
    const edge = httpEdge({ ...session, type: 'main' });
    await expect(createFreshAgentSession(edge.page, 'jim', origin)).rejects.toThrow('fresh chat is not the shared main session');
    expect(edge.request.get).toHaveBeenCalledExactlyOnceWith(`${origin}/api/v1/workspaces`);
  });
  it('rejects a create response belonging to the wrong agent', async () => {
    const edge = httpEdge({ ...session, agent_id: 'mia' });
    await expect(createFreshAgentSession(edge.page, 'jim', origin)).rejects.toThrow('fresh chat belongs to the intended agent');
    expect(edge.request.get).toHaveBeenCalledExactlyOnceWith(`${origin}/api/v1/workspaces`);
  });
  it('rejects unavailable history instead of accepting omitted messages as empty', async () => {
    const edge = httpEdge(session, { session, history_state: { status: 'unavailable', reason: 'storage_error' } });
    await expect(createFreshAgentSession(edge.page, 'jim', origin)).rejects.toThrow('freshness cannot be inferred from unavailable history');
    expect(edge.request.get).toHaveBeenCalledTimes(2);
  });
  it('rejects a mismatched stored owner even when the create response claimed Jim', async () => {
    const edge = httpEdge(session, { ...detail, session: { ...session, agent_id: 'mia' } });
    await expect(createFreshAgentSession(edge.page, 'jim', origin)).rejects.toThrow('fresh chat: stored owner is the intended agent');
    expect(edge.request.get).toHaveBeenCalledTimes(2);
  });
  it('rejects a failed create status rather than trusting its response id', async () => {
    const edge = httpEdge(session, detail, 409);
    await expect(createFreshAgentSession(edge.page, 'jim', origin)).rejects.toThrow('fresh chat: public create endpoint');
    expect(edge.request.get).toHaveBeenCalledExactlyOnceWith(`${origin}/api/v1/workspaces`);
  });
});
