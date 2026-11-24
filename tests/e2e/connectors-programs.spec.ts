/**
 * connectors-programs.spec.ts — S-148: the program switch on the Connectors page.
 *
 * Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
 * Traces to: S-148 (spec lines 1499-1504), US-5 AS-9 (line 496), the
 * Connectors -> channel panel -> Programs row of the UI-state table (line 561),
 * FR-009 (line 2095).
 *
 * Every expected string below is copied from the spec, never observed from the
 * implementation: the switch's accessible name ("Let <agent> use connected
 * programs for messages from this channel"), the bound line ("Bound to
 * <workspace> · <agent>", read from GET /channels/{id}/routing), the hint, the
 * unbound-channel message, the failure state ("Not confirmed" + Retry). At RED
 * no channel panel has a Programs section, so the first section assertion fails
 * loudly — the body cannot pass without the feature.
 *
 * Deterministic seeding (CHECK R01): the API is stubbed with a small STATEFUL
 * backend implementing the S1 programs contract the gateway tests pin
 * (GET/PUT /channels/{id}/programs carrying `allowed`), so the Given is exact —
 * telegram bound to workspace Home and agent Dev, telegram.eu unbound, both
 * switches off. The UI assertions are the read-back of that seed. Reload
 * persistence runs against the same stateful backend, and the failure path
 * arms a fault INSIDE it, so the only source of failure is the armed fault.
 * No conditional or caught visibility probe stands in for an assertion.
 */

import { expect, test, type Locator, type Page, type Route } from '@playwright/test';
import type { Agent, Workspace, ChannelEntry, ChannelRouting } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { startProbeProvider } from './fixtures/programs-s1';

// The SPA and the person's sign-in come from this spec's own isolated gateway
// (onboarded against the S1 loopback probe provider, no paid model), never
// from the shared gateway: the shared global setup needs a running gateway in
// $HOME/.omnipus and live-model keys, which S-148 does not use. Every channel,
// routing, programs, agent and workspace read below is the stateful stub; only
// static assets, auth and unrelated app reads reach the real gateway.
test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });

let gateway: GatewayProcess | undefined;
let provider: Awaited<ReturnType<typeof startProbeProvider>> | undefined;

test.beforeAll(async () => {
  provider = await startProbeProvider();
  gateway = await GatewayProcess.start({ apiBase: provider.url });
});
test.afterAll(async () => {
  try { await gateway?.stop(); }
  finally { await provider?.close(); }
  expect(provider?.unexpected ?? [], 'the isolated gateway made no unexpected model call').toEqual([]);
});

function origin(): string {
  if (!gateway) throw new Error('BLOCKED: the isolated S-148 gateway did not start');
  return gateway.baseURL;
}

// ── Seeded state (the S-148 Given) ──────────────────────────────────────────

const BOUND_CHANNEL = 'telegram';
const UNBOUND_CHANNEL = 'telegram.eu';
const BOUND_WORKSPACE_ID = 'home';
const BOUND_WORKSPACE_NAME = 'Home';
const BOUND_AGENT_ID = 'dev';
const BOUND_AGENT_NAME = 'Dev';

// Spec line 561 — the exact UI strings of the Programs section.
const PROGRAMS_HEADING = 'Programs';
const SETTINGS_HINT = 'Programs themselves are connected in Settings → Programs.';
const switchName = (agent: string) => `Let ${agent} use connected programs for messages from this channel`;
const boundLine = (workspace: string, agent: string) => `Bound to ${workspace} · ${agent}`;
const UNBOUND_MESSAGE =
  'This channel is not bound to a workspace and agent, so no agent receives its messages. ' +
  'This switch takes effect once it is bound.';
const NOT_CONFIRMED = 'Not confirmed';

// The SPA's request() validates every response against the generated zod
// schemas and rejects a whole payload on any mismatch (ApiSchemaError), so the
// stubs carry every schema-required field — the same shapes the
// channel-routing.spec.ts stubs use (revision is REQUIRED on Agent and
// Workspace under ADR-090, ^[a-f0-9]{64}$).
const STUB_REVISION = 'a'.repeat(64);

const AGENT_REQUIRED = {
  figure: 'Omnipus' as const,
  role: 'general' as const,
  memory_enabled: true,
  status: 'idle' as const,
  soul: 'stub soul',
  timeout_seconds: 300,
  max_tool_iterations: 200,
  needs_model: false,
  max_tool_iterations_source: 'global' as const,
  max_tool_iterations_override_ignored: false,
  revision: STUB_REVISION,
};

const STUB_AGENTS = [
  { id: BOUND_AGENT_ID, name: BOUND_AGENT_NAME, type: 'core', locked: true, ...AGENT_REQUIRED },
] satisfies Agent[];

const WS_REQUIRED = {
  pinned: false,
  pin_order: 0,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
  revision: STUB_REVISION,
};

const STUB_WORKSPACES = [
  {
    id: BOUND_WORKSPACE_ID,
    name: BOUND_WORKSPACE_NAME,
    status: 'active',
    core_team: [BOUND_AGENT_ID],
    task_count: 0,
    ...WS_REQUIRED,
  },
] satisfies Workspace[];

// instance_id marks a CONFIGURED instance — the ConnectorsScreen renders
// channel-card rows (with Configure) only for configured instances.
const STUB_CHANNELS = [
  {
    id: BOUND_CHANNEL,
    instance_id: BOUND_CHANNEL,
    identity: { kind: 'agent', id: BOUND_AGENT_ID },
    name: 'Telegram',
    transport: 'webhook',
    enabled: true,
    description: 'Telegram Bot integration',
  },
  {
    id: UNBOUND_CHANNEL,
    instance_id: UNBOUND_CHANNEL,
    identity: { kind: 'agent', id: BOUND_AGENT_ID },
    name: 'Telegram EU',
    transport: 'webhook',
    enabled: true,
    description: 'Telegram EU integration',
  },
] satisfies ChannelEntry[];

// GET /channels/{id}/routing — the binding the Programs section displays
// (spec line 561: the bound line is read from the existing routing GET).
const BOUND_ROUTING: ChannelRouting = {
  workspace_id: BOUND_WORKSPACE_ID,
  default_agent_id: BOUND_AGENT_ID,
  bound: true,
};
const UNBOUND_ROUTING: ChannelRouting = { workspace_id: '', default_agent_id: '', bound: false };

// ── The stateful stub backend ───────────────────────────────────────────────

/**
 * The per-connection programs setting, per channel (FR-009: default off),
 * served over the S1 contract the gateway tests pin — GET/PUT
 * /api/v1/channels/{id}/programs with `allowed`. faultArmed makes the NEXT
 * telegram PUT fail (the S-148 And-clause: "when the PUT fails"), and
 * telegramPutAttempts counts every telegram write attempt so "working Retry"
 * is proven by the retry re-issuing the write, not by a label alone.
 */
function createBackend() {
  const allowed: Record<string, boolean> = {
    [BOUND_CHANNEL]: false,
    [UNBOUND_CHANNEL]: false,
  };
  const revisions: Record<string, string> = { [BOUND_CHANNEL]: STUB_REVISION, [UNBOUND_CHANNEL]: STUB_REVISION };
  let saves = 0;
  let faultArmed = false;
  let telegramPutAttempts = 0;
  return {
    allowed,
    revisions,
    saved: () => { saves += 1; return saves.toString(16).padStart(64, '0'); },
    noteTelegramPut: () => {
      telegramPutAttempts += 1;
    },
    get faultArmed() {
      return faultArmed;
    },
    set faultArmed(v: boolean) {
      faultArmed = v;
    },
    get telegramPutAttempts() {
      return telegramPutAttempts;
    },
  };
}

type Backend = ReturnType<typeof createBackend>;

async function fulfillJson(route: Route, status: number, body: unknown): Promise<void> {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

async function registerStubRoutes(page: Page, backend: Backend): Promise<void> {
  // Channel list — both configured instances.
  await page.route('**/api/v1/channels', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, STUB_CHANNELS);
    } else {
      await route.continue();
    }
  });

  // Channel detail (the panel's own fetch; the routing spec stubs it the same way).
  for (const id of [BOUND_CHANNEL, UNBOUND_CHANNEL]) {
    await page.route(`**/api/v1/channels/${id}`, async (route: Route) => {
      if (route.request().method() === 'GET') {
        await fulfillJson(route, 200, {});
      } else {
        await route.continue();
      }
    });
  }

  // Routing — the binding the Programs section displays.
  await page.route(`**/api/v1/channels/${BOUND_CHANNEL}/routing`, async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, BOUND_ROUTING);
    } else {
      await route.continue();
    }
  });
  await page.route(`**/api/v1/channels/${UNBOUND_CHANNEL}/routing`, async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, UNBOUND_ROUTING);
    } else {
      await route.continue();
    }
  });

  // The per-connection programs setting (S1 contract; stateful). faultArmed
  // fails the telegram write in flight; the counter observes every attempt,
  // including the ones Retry re-issues.
  for (const id of [BOUND_CHANNEL, UNBOUND_CHANNEL]) {
    await page.route(`**/api/v1/channels/${id}/programs`, async (route: Route) => {
      const method = route.request().method();
      if (method === 'GET') {
        await fulfillJson(route, 200, { allowed: backend.allowed[id], revision: backend.revisions[id] });
        return;
      }
      if (method === 'PUT') {
        // S1 contract: save the submitted value, never toggle on occurrence.
        // A missing/wrong body must fail instead of receiving a fake success.
        const body: unknown = route.request().postDataJSON();
        expect(body).toEqual({ allowed: expect.any(Boolean), revision: backend.revisions[id] });
        const allowed = (body as Record<string, unknown>).allowed;
        expect(typeof allowed).toBe('boolean');
        if (backend.faultArmed && id === BOUND_CHANNEL) {
          backend.noteTelegramPut();
          await fulfillJson(route, 500, { error: 'store busy' });
          return;
        }
        backend.allowed[id] = allowed as boolean;
        backend.revisions[id] = backend.saved();
        if (id === BOUND_CHANNEL) backend.noteTelegramPut();
        await fulfillJson(route, 200, { allowed: backend.allowed[id], revision: backend.revisions[id] });
        return;
      }
      await route.continue();
    });
  }

  // The names the bound line resolves ("Bound to Home · Dev").
  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, STUB_AGENTS);
    } else {
      await route.continue();
    }
  });
  await page.route('**/api/v1/workspaces?**', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, STUB_WORKSPACES);
    } else {
      await route.continue();
    }
  });
  await page.route(`**/api/v1/workspaces/${BOUND_WORKSPACE_ID}`, async (route: Route) => {
    if (route.request().method() === 'GET') {
      await fulfillJson(route, 200, STUB_WORKSPACES[0]);
    } else {
      await route.continue();
    }
  });
}

// ── Panel helpers ───────────────────────────────────────────────────────────

async function openConnectors(page: Page): Promise<void> {
  // Reuse the isolated gateway's own signed-in cookie; never log in again.
  const base = origin();
  await page.context().clearCookies();
  await page.context().addCookies((await gateway!.browserStorageState()).cookies);
  await page.goto(`${base}/#/connectors`);
  await expect(page).toHaveURL(/connectors/, { timeout: 15_000 });
}

/** Open one channel's actual panel through its Configure control. */
async function openChannelPanel(page: Page, channelId: string) {
  const configure = page.getByRole('button', { name: `Configure ${channelId}`, exact: true });
  await expect(configure).toBeVisible({ timeout: 15_000 });
  await configure.click();
  const panel = page.getByRole('dialog');
  await expect(panel).toBeVisible({ timeout: 10_000 });
  return panel;
}

function programsSwitch(panel: Locator, agentName: string) {
  return panel.getByRole('switch', { name: switchName(agentName), exact: true });
}

// ── S-148 ───────────────────────────────────────────────────────────────────

test.describe('Connectors page — program switch (S-148)', () => {
  test('S148: bound panel switches on by keyboard, persists across reload; unbound panel explains; failed PUT returns OFF with "Not confirmed" and a working Retry', async ({ page }) => {
    const backend = createBackend();
    await registerStubRoutes(page, backend);
    await openConnectors(page);

    // Seed read-back: both channels render as configured rows with Configure.
    await expect(page.getByRole('button', { name: `Configure ${BOUND_CHANNEL}`, exact: true })).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('button', { name: `Configure ${UNBOUND_CHANNEL}`, exact: true })).toBeVisible({ timeout: 15_000 });

    // ── The telegram panel: the Programs section with the seeded OFF state ──
    const telegram = await openChannelPanel(page, BOUND_CHANNEL);
    await expect(telegram.getByRole('heading', { name: PROGRAMS_HEADING })).toBeVisible({ timeout: 10_000 });
    await expect(telegram.getByText(SETTINGS_HINT, { exact: true })).toBeVisible();
    await expect(telegram.getByText(boundLine(BOUND_WORKSPACE_NAME, BOUND_AGENT_NAME), { exact: true })).toBeVisible();

    const telegramSwitch = programsSwitch(telegram, BOUND_AGENT_NAME);
    await expect(telegramSwitch).toBeVisible({ timeout: 10_000 });
    await expect(telegramSwitch).not.toBeChecked(); // "the switch off before the change"

    // Keyboard-only switch-on; the state is announced through the switch's
    // accessible state (role="switch", aria-checked).
    await telegramSwitch.focus();
    await page.keyboard.press('Space');
    await expect(telegramSwitch).toBeChecked({ timeout: 10_000 });
    await expect(telegramSwitch).toHaveAttribute('aria-checked', 'true');

    expect(backend.allowed[BOUND_CHANNEL]).toBe(true);
    expect(backend.telegramPutAttempts).toBe(1);

    // A reload shows the same (the stub write persisted, not gateway disk).
    await page.reload();
    const reloaded = await openChannelPanel(page, BOUND_CHANNEL);
    await expect(programsSwitch(reloaded, BOUND_AGENT_NAME)).toBeChecked({ timeout: 15_000 });
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden({ timeout: 10_000 });

    // ── The telegram.eu panel: the exact unbound message ──
    const eu = await openChannelPanel(page, UNBOUND_CHANNEL);
    await expect(eu.getByRole('heading', { name: PROGRAMS_HEADING })).toBeVisible({ timeout: 10_000 });
    await expect(eu.getByText(UNBOUND_MESSAGE, { exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden({ timeout: 10_000 });

    // ── Failed switch-on, from a fresh OFF baseline ──
    // Establish OFF through the same keyboard: this PUT succeeds (no fault).
    const failure = await openChannelPanel(page, BOUND_CHANNEL);
    const failingSwitch = programsSwitch(failure, BOUND_AGENT_NAME);
    await expect(failingSwitch).toBeChecked({ timeout: 10_000 });
    await failingSwitch.focus();
    await page.keyboard.press('Space');
    await expect(failingSwitch).not.toBeChecked({ timeout: 10_000 });

    // Arm the fault inside the backend; the person switches on and the PUT fails.
    const attemptsAtArm = backend.telegramPutAttempts;
    backend.faultArmed = true;
    await failingSwitch.focus();
    await page.keyboard.press('Space');

    // The switch returns to OFF with the exact failure state and a Retry control.
    await expect(failingSwitch).not.toBeChecked({ timeout: 15_000 });
    await expect(failure.getByText(NOT_CONFIRMED, { exact: true })).toBeVisible({ timeout: 15_000 });
    const retry = failure.getByRole('button', { name: /retry/i });
    await expect(retry).toBeVisible();

    // The failed write really left the backend: the attempt was issued.
    await expect.poll(() => backend.telegramPutAttempts).toBe(attemptsAtArm + 1);
    expect(backend.allowed[BOUND_CHANNEL]).toBe(false);

    // Working Retry: clicking it re-issues the same failed write.
    const attemptsBeforeRetry = backend.telegramPutAttempts;
    await retry.click();
    await expect(failure.getByText(NOT_CONFIRMED, { exact: true })).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => backend.telegramPutAttempts).toBe(attemptsBeforeRetry + 1);
    expect(backend.allowed[BOUND_CHANNEL]).toBe(false);
    await expect(failingSwitch).not.toBeChecked();

    // With the fault cleared, Retry completes the save (the UI row's Saved
    // state) — the Retry control is a real path out of the failure, not a label.
    backend.faultArmed = false;
    await retry.click();
    await expect(failingSwitch).toBeChecked({ timeout: 15_000 });
    await expect(failure.getByText(NOT_CONFIRMED, { exact: true })).toBeHidden({ timeout: 10_000 });
    expect(backend.allowed[BOUND_CHANNEL]).toBe(true);
    await expect.poll(() => backend.telegramPutAttempts).toBe(attemptsBeforeRetry + 2);
  });
});
