import type { Page } from '@playwright/test';
import type { Session as SessionType } from '../../../src/lib/api/generated/openapi-types';
import { Agent, AppState, GodModeStatus, Room, SandboxStatus, Session, SessionDetail, SessionPage, Workspace } from '../../../src/lib/api/generated/schemas';
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas';
import { wireAvailableDetail, wireHistoryPage, wireHistorySnapshot } from '../../chat-history-paging/legacy-wire';

// Q81 layout fixture: only the HTTP/WS process edges are controlled. The
// production sidebar, dialogs, session selection, chat, motion and CSS are real.
const timestamp = '2026-10-24T00:00:00Z';
export const backdropAgent = Agent.parse({
  id: 'q81-agent', name: 'Q81 Reader', type: 'Main', locked: false,
  figure: 'Robot', role: 'general', status: 'active', soul: 'Layout fixture only.',
  revision: '0'.repeat(64), needs_model: false, max_tool_iterations: 8,
  max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false,
});
// The real roster requires the server-computed member main binding. A
// core_team entry alone intentionally renders Main chat unavailable, no menu.
export const backdropMain = Session.parse({
  id: 'q81-main', type: 'main', agent_id: backdropAgent.id, title: 'Q81 standing main',
  status: 'active', lifecycle_state: 'done', workspace_id: 'q81-workspace',
  protected: true, needs_attention: false,
  created_at: timestamp, updated_at: timestamp, channel: 'webchat', partitions: [],
  stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
});
export const backdropWorkspace = Workspace.parse({
  id: 'q81-workspace', name: 'Q81 Workspace', status: 'active', pinned: false,
  pin_order: 0, core_team: [backdropAgent.id], task_count: 0,
  member_configs: { [backdropAgent.id]: { main_session_id: backdropMain.id } },
  revision: '0'.repeat(64), created_at: timestamp, updated_at: timestamp,
});
export const backdropTarget = Session.parse({
  id: 'q81-target', type: 'chat', agent_id: backdropAgent.id, title: 'Q81 target chat',
  status: 'active', lifecycle_state: 'done', workspace_id: backdropWorkspace.id,
  created_at: timestamp, updated_at: timestamp, channel: 'webchat', partitions: [],
  stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
});
export const backdropInitial = Session.parse({
  ...backdropTarget, id: 'q81-initial', title: 'Q81 initial unfiled chat', workspace_id: undefined,
});
const state = AppState.parse({
  onboarding_complete: true, identity: { mode: 'local', edition: 'core', signed_in: true }, dev_mode_bypass: false,
});

export async function installSidebarBackdropEdges(page: Page, options: { extraSessions?: SessionType[]; agentName?: string } = {}) {
  const traffic = { requests: [] as string[], unknown: [] as string[], attaches: [] as string[], errors: [] as string[] };
  page.on('pageerror', error => traffic.errors.push(error.message));
  // `extraSessions` adds sessions (e.g. a helper chat with a parent) and
  // `agentName` renames the workspace's agent; the defaults keep this
  // fixture's own set unchanged.
  const sessions = [backdropInitial, backdropMain, backdropTarget, ...(options.extraSessions ?? [])];
  const agent = options.agentName ? Agent.parse({ ...backdropAgent, name: options.agentName }) : backdropAgent;
  const history = (id: string) => wireHistoryPage(id, [], 0);
  await page.routeWebSocket(/\/api\/v1\/chat\/ws/, socket => {
    let seq = 0;
    socket.onMessage(raw => {
      const frame = WsFrame.parse(JSON.parse(String(raw)));
      if (frame.type === 'ping') { socket.send(JSON.stringify(WsFrame.parse({ type: 'pong' }))); return; }
      if (frame.type !== 'attach_session') return;
      traffic.attaches.push(frame.session_id);
      const page = wireHistoryPage(frame.session_id, [], 0, {
        info: { limit: frame.history_limit ?? 40, min_rows: frame.history_min_rows ?? 20 },
      });
      const bootId = 'q81-layout-fixture';
      seq++;
      const frames = [
        wireHistorySnapshot(page, { seq, boot_id: bootId, reason: 'unknown_position' }),
        { type: 'session_state', session_id: frame.session_id, user_id: 'q81-api-fixture',
          pending_approvals: [], history_state: { status: 'available' }, emitted_at: timestamp },
        { type: 'done', session_id: frame.session_id, stats: { frames_emitted: 0 } },
        { type: 'catch_up_complete', session_id: frame.session_id, seq, boot_id: bootId, mode: 'snapshot' },
      ];
      for (const reply of frames) socket.send(JSON.stringify(WsFrame.parse(reply)));
    });
  });
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url());
    const endpoint = url.pathname.replace('/api/v1', '');
    traffic.requests.push(url.pathname + url.search);
    const json = (body: unknown) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
    if (endpoint === '/state') return json(state);
    if (endpoint === '/auth/validate') return json({ username: 'q81-api-fixture' });
    if (endpoint === '/workspaces') return json(url.searchParams.get('status') === 'archived' ? [] : [backdropWorkspace]);
    if (endpoint === `/workspaces/${backdropWorkspace.id}`) return json(backdropWorkspace);
    if (endpoint === `/workspaces/${backdropWorkspace.id}/rooms`) return json(Room.array().parse([]));
    if (endpoint === '/agents') return json([agent]);
    if (endpoint === `/agents/${agent.id}`) return json(agent);
    if (endpoint === '/sessions') return json(SessionPage.parse({ sessions }));
    for (const session of sessions) {
      if (endpoint === `/sessions/${session.id}`) return json(SessionDetail.parse(wireAvailableDetail(session, history(session.id))));
      if (endpoint === `/sessions/${session.id}/messages`) return json(history(session.id));
    }
    if (['/tasks', '/providers', '/skills', '/commands', '/tools', '/mcp-servers'].includes(endpoint)) return json([]);
    if (endpoint === `/agents/${backdropAgent.id}/activity-runs`) return json([]);
    if (endpoint === '/activity') return json({ events: [] });
    if (endpoint === '/skills/marketplace') return json({ enabled: false });
    if (endpoint === '/gateway/god-mode') return json(GodModeStatus.parse({ enabled: false, persisted: false, available: false, supported: false }));
    if (endpoint === '/security/sandbox-status') return json(SandboxStatus.parse({
      backend: 'fixture', available: false, kernel_level: false, policy_applied: false,
      seccomp_enabled: false, bind_ports_count: 0,
    }));
    if (endpoint === '/notifications') return json({ notifications: [], unread_count: 0 });
    if (endpoint === '/version') return json({ version: '0.1.1', build_sha: 'q81-layout-fixture' });
    if (endpoint === '/config') return json({ gateway: {}, sandbox: {}, agents: { defaults: {} } });
    traffic.unknown.push(endpoint);
    return route.fulfill({ status: 404, json: { error: `Undeclared Q81 fixture endpoint ${endpoint}` } });
  });
  return traffic;
}
