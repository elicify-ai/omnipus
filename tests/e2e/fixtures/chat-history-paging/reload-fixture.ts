import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { test as base, expect, type Page, type TestInfo } from '@playwright/test';
import { Agent, Workspace, Session, SessionDetail, SessionHistoryResponse } from '../../../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest, SessionCreateRequest } from '../../../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from '../gateway-process';
import { chatInput } from '../selectors';
import { E2E_MODEL } from '../e2e-model';
import { observeTraffic, settled, historyScroller } from './harness';
import { assertNativeBashPolicy } from './agent-tool-policy';
import { startReloadProvider, type ReloadProvider, BASELINE_INPUT, COMPLETED_ID, COMPLETED_MARKER,
  CANCELLED_MARKER, RUNNING_MARKER, EARLIER_TOKENS } from './reload-provider';

export class ReloadHarness {
  readonly traffic;
  readonly apiReads: { url: string; status: number; body: unknown }[] = [];
  phase = 'PREREQUISITE: fresh real gateway and session';
  constructor(readonly page: Page, readonly gateway: GatewayProcess, readonly provider: ReloadProvider,
    readonly sessionId: string, readonly otherSessionId: string, readonly info: TestInfo, readonly logPath: string) {
    this.traffic = observeTraffic(page);
  }
  assertion(stage: string) { this.phase = `ASSERTION: ${stage}`; }
  async authenticate() {
    await this.page.context().clearCookies();
    await this.page.context().addCookies((await this.gateway.browserStorageState()).cookies);
  }
  async ready(sessionId = this.sessionId, reload = false) {
    const start = this.traffic.frames.length;
    const attachStart = this.traffic.attaches.length;
    const alreadyActive = await this.page.locator(`[data-active-session-id="${sessionId}"]`).isVisible();
    const completedBefore = this.traffic.frames.some(frame => frame.type === 'catch_up_complete' && frame.session_id === sessionId);
    const navigation = reload ? await this.page.reload() : await this.page.goto(`${this.gateway.baseURL}/#/sessions/${sessionId}`);
    await expect(this.page.locator(`[data-active-session-id="${sessionId}"]`), this.phase).toBeVisible();
    await settled(this.page);
    const newAttach = this.traffic.attaches.slice(attachStart).some(frame => frame.session_id === sessionId);
    // Playwright returns null for a same-document hash navigation. Reuse only
    // this already-active session's actual prior completion, never on reload.
    if (!reload && navigation === null && alreadyActive && !newAttach) {
      expect(completedBefore, `${this.phase}: same-session no-op must have a proven prior native completion`).toBe(true);
    } else {
      await expect.poll(() => this.traffic.frames.slice(start).filter(frame =>
        frame.type === 'catch_up_complete' && frame.session_id === sessionId).length,
      { message: `${this.phase}: actual attach finishes, not replay done alone` }).toBeGreaterThan(0);
    }
    await expect(chatInput(this.page), this.phase).toBeEnabled();
    expect(this.traffic.failures, `${this.phase}: generated network shapes`).toEqual([]);
    await settled(this.page);
  }
  async send(input: string) {
    expect(this.provider.failures, this.phase).toEqual([]);
    await chatInput(this.page).fill(input);
    await chatInput(this.page).press('Enter');
    await expect.poll(() => this.traffic.sends.filter(frame => frame.session_id === this.sessionId && frame.content === input).length,
      { message: `${this.phase}: exactly one real outbound input` }).toBe(1);
  }
  async detail(sessionId = this.sessionId) {
    const url = `${this.gateway.baseURL}/api/v1/sessions/${sessionId}`;
    const response = await this.page.request.get(url);
    expect(response.status(), `${this.phase}: ${await response.text()}`).toBe(200);
    const body: unknown = await response.json();
    this.apiReads.push({ url, status: response.status(), body });
    return SessionDetail.parse(body);
  }
  async read(limit: number) {
    const url = `${this.gateway.baseURL}/api/v1/sessions/${this.sessionId}/messages?limit=${limit}&min_rows=20`;
    const response = await this.page.request.get(url);
    expect(response.status(), this.phase).toBe(200);
    const body: unknown = await response.json();
    this.apiReads.push({ url, status: response.status(), body });
    return SessionHistoryResponse.parse(body);
  }
  async completed(tokens: number) {
    await expect.poll(async () => {
      expect(this.provider.failures, this.phase).toEqual([]);
      const detail = await this.detail();
      return { lifecycle: detail.session.lifecycle_state, tokens: detail.session.stats?.tokens_total };
    }, { message: `${this.phase}: actual completed lifecycle AND saved provider-accounted aggregate` })
      .toEqual({ lifecycle: 'done', tokens });
  }
  async counter(tokens: number, stage: string) {
    await expect(this.page.getByTestId('session-token-counter'), `${stage}: raw accessible session-wide total`)
      .toHaveAttribute('aria-label', `${tokens} tokens used`);
    return { raw: await this.page.getByTestId('session-token-counter').getAttribute('aria-label'),
      text: await this.page.getByTestId('session-token-value').innerText() };
  }
  async baseline() {
    await this.send(BASELINE_INPUT);
    await this.completed(EARLIER_TOKENS);
    const result = this.traffic.frames.filter(frame => frame.type === 'tool_call_result' && frame.call_id === COMPLETED_ID);
    expect(result, 'PREREQUISITE: real native completed tool positive control').toHaveLength(1);
    expect(result[0]).toMatchObject({ type: 'tool_call_result', tool: 'bash', status: 'success' });
    expect(this.executionCount(COMPLETED_MARKER), 'PREREQUISITE: native completed command actually executed once').toBe(1);
    await this.counter(EARLIER_TOKENS, 'PREREQUISITE: known live baseline');
  }
  marker(name: string): string | undefined {
    function find(directory: string): string | undefined {
      for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
        if (entry.isSymbolicLink()) continue;
        const file = path.join(directory, entry.name);
        if (entry.isFile() && entry.name === name) return file;
        if (entry.isDirectory()) { const found = find(file); if (found) return found; }
      }
      return undefined;
    }
    return find(this.gateway.homeDir);
  }
  executionCount(name: string) {
    const file = this.marker(name);
    return file ? fs.readFileSync(file, 'utf8').trim().split('\n').filter(Boolean).length : 0;
  }
  nativeProcess() {
    const file = this.marker(RUNNING_MARKER);
    return file ? JSON.parse(fs.readFileSync(file, 'utf8')) as { pid: number; cwd: string } : undefined;
  }
  nativeRunning() {
    const receipt = this.nativeProcess();
    if (!receipt) return false;
    try {
      process.kill(receipt.pid, 0);
      if (process.platform === 'linux') {
        const status = fs.readFileSync(`/proc/${receipt.pid}/status`, 'utf8');
        if (/^State:\s+[ZX]/m.test(status)) return false;
        return fs.readFileSync(`/proc/${receipt.pid}/cmdline`, 'utf8').includes(RUNNING_MARKER);
      }
      return true;
    } catch (error) {
      if (['ESRCH', 'ENOENT'].includes((error as NodeJS.ErrnoException).code ?? '')) return false;
      throw error;
    }
  }
  canonical(sessionId = this.sessionId) {
    const directory = path.join(this.gateway.homeDir, 'sessions', sessionId);
    return fs.readdirSync(directory).filter(name => /\.(json|jsonl)$/.test(name)).map(name => {
      const file = path.join(directory, name);
      const text = fs.readFileSync(file, 'utf8');
      return { file, text }; // Real private canonical/accounting bytes, never a fabricated receipt.
    });
  }
  async record(stage: string, sessionId = this.sessionId) {
    const hasCounter = await this.page.getByTestId('session-token-counter').count();
    const evidence = { stage, phase: this.phase, session_id: sessionId,
      provider: this.provider.receipts, provider_failures: this.provider.failures,
      frames: this.traffic.frames, pages: this.traffic.pages, api_reads: this.apiReads, sends: this.traffic.sends,
      canonical: this.canonical(sessionId),
      native_executions: { completed: this.executionCount(COMPLETED_MARKER), cancelled: this.executionCount(CANCELLED_MARKER) },
      native_process_receipt: this.nativeProcess(), native_still_running: this.nativeRunning(),
      counter: hasCounter ? await this.page.getByTestId('session-token-counter').getAttribute('aria-label') : null,
      formatted: hasCounter ? await this.page.getByTestId('session-token-value').textContent() : null };
    const evidencePath = this.info.outputPath(`${stage}.json`);
    fs.writeFileSync(evidencePath, JSON.stringify(evidence, null, 2));
    await this.info.attach(stage, { contentType: 'application/json', path: evidencePath });
    await this.info.attach(`${stage}-screenshot`, { contentType: 'image/png', body: await this.page.screenshot({ fullPage: true }) });
  }
  async seedOrdinary(label: string, count: number) {
    const binary = process.env.OMNIPUS_CHAT_HISTORY_SEED_BINARY;
    if (!binary || !path.isAbsolute(binary) || !fs.existsSync(binary))
      throw new Error('PREREQUISITE: official prebuilt canonical seed missing; required by T40/T41 older-page cases');
    await this.gateway.kill9();
    this.gateway.assertStoppedForFixture();
    const output = execFileSync(binary, ['--sessions-root', path.join(this.gateway.homeDir, 'sessions'),
      '--session', this.sessionId, '--label', label, '--count', String(count), '--shape', 'text'], { encoding: 'utf8' });
    expect(JSON.parse(output), 'actual ordinary filler only, written through the existing product appender')
      .toMatchObject({ count, session_id: this.sessionId, canonical_writer: 'UnifiedStore.AppendTranscriptAddressed' });
    await this.gateway.restart();
    await this.authenticate();
  }
  async olderUntil(predicate: (body: ReturnType<typeof SessionHistoryResponse.parse>) => boolean) {
    //240 authored filler groups / minimum20-page groups, plus two boundaries.
    for (let boundary = 0; boundary < 14; boundary++) {
      const prior = this.traffic.pages.length;
      await historyScroller(this.page).evaluate(element => { element.scrollTop = 0; element.dispatchEvent(new Event('scroll')); });
      const earlier = this.page.getByRole('button', { name: 'Load earlier messages', exact: true });
      if (await earlier.isVisible()) await earlier.click();
      await expect.poll(() => this.traffic.pages.slice(prior).some(reply => reply.status === 200),
        { message: `${this.phase}: real UI older request must complete` }).toBe(true);
      for (const reply of this.traffic.pages.slice(prior)) {
        expect(reply.status, this.phase).toBe(200);
        const body = SessionHistoryResponse.parse(reply.body);
        if (predicate(body)) { await settled(this.page); return body; }
        expect(body.has_earlier, `${this.phase}: target is in the retained range, never silently skipped`).toBe(true);
      }
    }
    throw new Error(`${this.phase}: retained target was not reached through fourteen genuine older-page requests`);
  }
  async noReexecution(receipts: number, inputs: number, cancelled: number) {
    expect(this.provider.receipts, 'reload/paging cannot initiate another model call').toHaveLength(receipts);
    expect(this.provider.failures).toEqual([]);
    expect(this.traffic.sends, 'reload/paging cannot resend an input').toHaveLength(inputs);
    expect(this.executionCount(COMPLETED_MARKER), 'completed native call is not executed again').toBe(1);
    expect(this.executionCount(CANCELLED_MARKER), 'cancelled native call is not executed again').toBe(cancelled);
  }
}

function killOwnedNativeProcess(pid: number) {
  try { process.kill(pid, 'SIGKILL'); }
  catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw error; }
}

export const test = base.extend<{ reload: ReloadHarness }>({
  reload: async ({ page }, use, info) => {
    const provider = await startReloadProvider();
    let gateway: GatewayProcess | undefined;
    let fixture: ReloadHarness | undefined;
    const logPath = info.outputPath('reload-gateway.jsonl');
    fs.mkdirSync(path.dirname(logPath), { recursive: true });
    try {
      gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 'reload-parity',
        env: { OMNIPUS_LOG_FILE: logPath, OMNIPUS_LOG_LEVEL: 'debug' } });
      await gateway.kill9();
      gateway.assertStoppedForFixture();
      // not-wire-format: this isolated product test-home config is not runner
      // configuration or a gateway payload. Native bash still executes normally.
      const configPath = path.join(gateway.homeDir, 'config.json');
      const config = JSON.parse(fs.readFileSync(configPath, 'utf8')) as { sandbox?: { tool_policies?: Record<string, string> } };
      config.sandbox = { ...config.sandbox, tool_policies: { ...config.sandbox?.tool_policies, bash: 'allow' } };
      fs.writeFileSync(configPath, JSON.stringify(config), { mode: 0o600 });
      await gateway.restart();
      const agentResult = await gateway.apiFetch('POST', '/api/v1/agents', { type: 'Main', name: 'Reload parity',
        soul: 'Execute the requested harmless native command and answer the user.', provider: 'openrouter', model: E2E_MODEL,
        tool_policy_changes: { set: { bash: 'allow' } },
        max_tool_iterations: 8 } satisfies AgentCreateRequestMain);
      expect(agentResult.ok, agentResult.raw).toBe(true);
      const agent = Agent.parse(agentResult.body);
      // Agent creation need not echo its tool configuration. Verify the
      // same exact allow grant through the supported live tools endpoint,
      // including the effective ceiling-constrained policy and real override.
      await assertNativeBashPolicy(gateway, agent.id, 'allow', 'allow');
      const workspaceResult = await gateway.apiFetch('POST', '/api/v1/workspaces', { name: 'Reload parity', core_team: [agent.id] } satisfies WorkspaceCreateRequest);
      expect(workspaceResult.ok, workspaceResult.raw).toBe(true);
      const workspace = Workspace.parse(workspaceResult.body);
      const sessions: string[] = [];
      for (let i = 0; i < 2; i++) {
        const result = await gateway.apiFetch('POST', '/api/v1/sessions', { type: 'chat', agent_id: agent.id, workspace_id: workspace.id } satisfies SessionCreateRequest);
        expect(result.ok, result.raw).toBe(true);
        sessions.push(Session.parse(result.body).id);
      }
      fixture = new ReloadHarness(page, gateway, provider, sessions[0], sessions[1], info, logPath);
      await fixture.authenticate();
      await use(fixture);
    } finally {
      if (fixture && !page.isClosed()) {
        // Preserve the first assertion and collect live/canonical/network state
        // before process teardown; capture errors are separate loud failures.
        try { await fixture.record('final-reload-evidence'); }
        catch (error) { expect.soft(error, 'evidence capture must not fail silently').toBeUndefined(); }
      }
      try {
        if (fixture?.nativeRunning()) {
          const receipt = fixture.nativeProcess()!;
          await info.attach('owned-native-process-cleanup', { contentType: 'application/json',
            body: JSON.stringify({ receipt, reason: 'teardown only; never accepted as the tested UI Stop' }) });
          killOwnedNativeProcess(receipt.pid);
        }
        if (gateway) await gateway.stop();
      } finally { await provider.close(); }
    }
  },
});
