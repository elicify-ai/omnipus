import fs from 'node:fs';
import path from 'node:path';
import { test, expect } from '@playwright/test';
import { Agent, Session, Workspace } from '../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, AgentCreateRequestSubagent3p, AgentUpdateRequest, SessionCreateRequest, WorkspaceCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { observeTraffic } from './fixtures/chat-history-paging/harness';
import { e2eAgentModelFields } from './fixtures/e2e-agent-model';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { startT8AgentApprovalProvider, T8_ENV_VALUE } from './fixtures/t8-agent-approval-provider';

// Oracle: tool-parity r17 scenario 17.2, with 17.1 as the visible positive
// control. One private gateway/account/browser. Only the model HTTP edge is
// scripted. A setup/collection result is never reported as product PASS.
test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });
test.setTimeout(120_000);

test('17.2 restores the same masked agent-change approval after page reload', async ({ page }, info) => {
  const provider = await startT8AgentApprovalProvider();
  let gateway: GatewayProcess | undefined;
  try {
    gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 't8-browser-tester' });
    const made = await gateway.apiFetch('POST', '/api/v1/agents', {
      type: 'Main', name: 'T8 approval requester', soul: 'Carry out the requested agent change using update_agent.',
      ...e2eAgentModelFields('mock'), max_tool_iterations: 8,
    } satisfies AgentCreateRequestMain);
    expect(made.status, made.raw).toBe(201);
    let actor = Agent.parse(made.body);
    // This is a PERSON REST permission update, not a creation-time management
    // grant bypass and not a grant through update_agent itself.
    const grant = await gateway.apiFetch('PUT', `/api/v1/agents/${actor.id}`, {
      revision: actor.revision, tool_policy_changes: { set: { update_agent: 'ask' } },
    } satisfies AgentUpdateRequest);
    expect(grant.status, grant.raw).toBe(200);
    actor = Agent.parse(grant.body);
    const targetResult = await gateway.apiFetch('POST', '/api/v1/agents', {
      type: 'subagent_3p', name: 'T8 external target', description: 'T8 approval target', executor: { cli: 'codex' },
    } satisfies AgentCreateRequestSubagent3p);
    expect(targetResult.status, targetResult.raw).toBe(201);
    const target = Agent.parse(targetResult.body);
    provider.setTarget({ id: target.id, revision: target.revision });
    const workspaceResult = await gateway.apiFetch('POST', '/api/v1/workspaces', {
      name: 'T8 approval test', core_team: [actor.id],
    } satisfies WorkspaceCreateRequest);
    expect(workspaceResult.status, workspaceResult.raw).toBe(201);
    const workspace = Workspace.parse(workspaceResult.body);
    const sessionResult = await gateway.apiFetch('POST', '/api/v1/sessions', {
      type: 'chat', agent_id: actor.id, workspace_id: workspace.id,
    } satisfies SessionCreateRequest);
    expect(sessionResult.status, sessionResult.raw).toBe(201);
    const session = Session.parse(sessionResult.body);
    // Disable only the automatic approval switch in the isolated fixture.
    // Actual registry, strictest-wins policy and approval decision remain real.
    await gateway.kill9(); gateway.assertStoppedForFixture();
    const filename = path.join(gateway.homeDir, 'config.json');
    const cfg = JSON.parse(fs.readFileSync(filename, 'utf8')) as {
      sandbox?: { auto_approve?: boolean; tool_policies?: Record<string, string> };
    };
    cfg.sandbox = { ...cfg.sandbox, auto_approve: false, tool_policies: { ...cfg.sandbox?.tool_policies, update_agent: 'ask' } };
    fs.writeFileSync(filename, JSON.stringify(cfg), { mode: 0o600 });
    await gateway.restart();
    const auth = await gateway.browserStorageState();
    await page.context().clearCookies(); await page.context().addCookies(auth.cookies);
    const traffic = observeTraffic(page);
    await openSessionByDeepLink(page, session.id, gateway.baseURL);
    await chatInput(page).fill('Update the external worker environment and CLI arguments as requested.');
    await chatInput(page).press('Enter');
    await expect.poll(() => {
      expect(provider.failures()).toEqual([]);
      return traffic.frames.some((f) => f.type === 'tool_approval_required' && f.session_id === session.id && f.tool_name === 'update_agent');
    }, { message: 'the real loop must produce a real pending update_agent approval' }).toBe(true);
    const pending = traffic.frames.find((f) => f.type === 'tool_approval_required' && f.session_id === session.id && f.tool_name === 'update_agent');
    if (!pending || pending.type !== 'tool_approval_required') throw new Error('BLOCKED: genuine pending approval required by r17 17.2');
    expect(JSON.stringify(pending), 'secret cannot enter the browser approval frame').not.toContain(T8_ENV_VALUE);
    const card = page.getByRole('region', { name: 'Tool Approval Required', exact: true });
    const assertCard = async () => {
      await expect(card).toBeVisible();
      await expect(card.getByText('TOKEN', { exact: true })).toBeVisible();
      await expect(card.getByText(/value hidden/i)).toBeVisible();
      await expect(card.getByText('--model x', { exact: true })).toBeVisible();
      await expect(card.getByText('--verbose', { exact: true })).toBeVisible();
      expect(await page.locator('body').textContent()).not.toContain(T8_ENV_VALUE);
    };
    await assertCard();
    await info.attach('before-reload', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
    const start = traffic.frames.length;
    await page.reload(); await waitForConnected(page);
    await expect.poll(() => traffic.frames.slice(start).filter((f) => f.type === 'catch_up_complete' && f.session_id === session.id).length).toBe(1);
    const restored = traffic.frames.slice(start).find((f) => f.type === 'session_state' && f.session_id === session.id);
    if (!restored || restored.type !== 'session_state') throw new Error('authoritative session_state missing after reload');
    expect(restored.pending_approvals.map((p) => p.approval_id)).toEqual([pending.approval_id]);
    expect(restored.pending_approvals[0].args).toEqual(pending.args);
    expect(restored.pending_approvals[0].turn_id).toBe(pending.turn_id);
    expect(provider.issued(), 'reload must not issue a second tool call').toBe(1);
    expect(traffic.failures).toEqual([]);
    await assertCard();
    await info.attach('after-reload', { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
    await card.getByRole('button', { name: 'Deny', exact: true }).click();
    await expect(card).toHaveCount(0);
    const after = await gateway.apiFetch('GET', `/api/v1/agents/${target.id}`);
    expect(after.status, after.raw).toBe(200);
    expect(Agent.parse(after.body).revision, 'denying never mutates the target').toBe(target.revision);
  } finally {
    try { if (gateway) { await gateway.kill9(); await gateway.stop(); } }
    finally { await provider.close(); }
  }
});
