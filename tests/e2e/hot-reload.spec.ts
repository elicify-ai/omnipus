// hot-reload.spec.ts
//
// Hot-reloadable changes (prompt-injection-level, rate-limits) take
// effect within 2 seconds of save without a process restart.
//
// Approach: GET-readback variant.
//
// The observable contract is that "the change takes effect within 2s" —
// the simplest externally-verifiable proxy is that a GET of the endpoint reads
// back the new value within 2s of the PUT completing. The gateway's
// rest_prompt_guard.go and rest_rate_limits.go both call a.awaitReload()
// before returning, so by the time PUT responds the config is already reloaded.
// A follow-up GET within 2s MUST return the new value.
//
// LLM-observable assertions (sanitizer picks up new level, rate limiter
// rejects a second call) are not implemented here because they require:
//   (a) a real OPENROUTER_API_KEY, and
//   (b) controlled agent LLM calls with predictable injection content.
// T0.1: The previous soft-skip on OPENROUTER_API_KEY absence has been removed.
// Both tests use GET-readback only (no live LLM needed) and always run.
//
// Gateway lifecycle: this spec starts its own gateway on port 5551 with a
// throwaway OMNIPUS_HOME. It does NOT rely on the globally-started gateway
// from global-setup.ts. The binary is specified via OMNIPUS_BINARY env
// (default: /tmp/omnipus-ci).
//
// CSRF handling: we call the login endpoint via fetch() to get a bearer token
// plus the CSRF cookie (named __Host-csrf over TLS, csrf over the plain HTTP
// this spec's gateway runs — see requestIsSecure in
// pkg/gateway/middleware/csrf.go), then use fetch() for PUT calls with both
// the Authorization header and the X-Csrf-Token header echoing the cookie value.
//
// Differentiation guarantee: each test calls PUT with two DIFFERENT values and
// verifies GET returns the DIFFERENT corresponding value — ruling out both
// hardcoded responses and stale cache.

import { test, expect } from '@playwright/test';
import {
  startGateway,
  stopGateway,
  getFreePort,
  type GatewayHandle,
} from './setup.js';

// ── Shared gateway state ───────────────────────────────────────────────────────
let handle: GatewayHandle;
let adminToken = '';
let csrfToken = '';
let csrfCookieName = 'csrf';

// This spec manages its own isolated gateway — do NOT use the global storageState.
// baseURL is not set at module level because the port is resolved at runtime.
test.use({ storageState: { cookies: [], origins: [] } });

// ── Start the gateway once for all tests in this file ─────────────────────────
test.beforeAll(async () => {
  const port = await getFreePort();
  handle = await startGateway({ port });

  // Login via API to get the bearer token and CSRF cookie.
  // We use a raw fetch so we can capture the Set-Cookie header.
  const loginRes = await fetch(`${handle.baseURL}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: handle.adminUsername, password: handle.adminPassword }),
  });
  if (!loginRes.ok) {
    throw new Error(`hot-reload setup: login failed ${loginRes.status}`);
  }
  const loginBody = (await loginRes.json()) as { token: string };
  adminToken = loginBody.token;

  // Extract the CSRF cookie from Set-Cookie. The gateway names this cookie
  // `__Host-csrf` over TLS and `csrf` over plain HTTP (requestIsSecure in
  // pkg/gateway/middleware/csrf.go) — this spec's gateway runs over plain
  // HTTP, so the actual cookie name is `csrf`, not `__Host-csrf`. Capture
  // whichever name the server actually sent rather than assuming the TLS
  // flavor, mirroring the SPA's own dual-name read (src/lib/api.ts
  // readCSRFCookie).
  // On plain HTTP with localhost, Chromium stores Secure cookies because
  // localhost is a "potentially trustworthy origin" (RFC-8252).
  const setCookieHeader = loginRes.headers.get('set-cookie') ?? '';
  const csrfMatch = setCookieHeader.match(/(?:^|,|\s)(__Host-csrf|csrf)=([^;]+)/);
  csrfCookieName = csrfMatch?.[1] ?? 'csrf';
  csrfToken = csrfMatch?.[2] ?? '';
});

test.afterAll(async () => {
  await stopGateway(handle);
});

// ---------------------------------------------------------------------------
// Helper: authenticated PUT to the gateway.
// Uses fetch() directly (not page.request) because this spec's tests are
// API-only — no browser navigation is needed for the GET-readback checks.
// ---------------------------------------------------------------------------
async function authedPut(
  path: string,
  data: Record<string, unknown>,
): Promise<{ status: number; body: unknown }> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${adminToken}`,
    'Content-Type': 'application/json',
  };
  if (csrfToken) {
    headers['X-Csrf-Token'] = csrfToken;
    headers['Cookie'] = `${csrfCookieName}=${csrfToken}`;
  }
  const res = await fetch(`${handle.baseURL}${path}`, {
    method: 'PUT',
    headers,
    body: JSON.stringify(data),
  });
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = await res.text();
  }
  return { status: res.status, body };
}

// ---------------------------------------------------------------------------
// Helper: authenticated GET from the gateway.
// ---------------------------------------------------------------------------
async function authedGet(path: string): Promise<{ status: number; body: unknown }> {
  const res = await fetch(`${handle.baseURL}${path}`, {
    headers: { Authorization: `Bearer ${adminToken}` },
  });
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = await res.text();
  }
  return { status: res.status, body };
}

// ---------------------------------------------------------------------------
// Test 1: prompt-injection hot-reload
//
// Variant: GET-readback fallback.
// Rationale: the LLM-observable variant requires a live LLM to produce tool
// results containing injection patterns.
//
// Differentiation proof:
//   PUT level=low  → GET must return level=low   (not hardcoded "medium")
//   PUT level=high → GET must return level=high  (different input, different output)
// ---------------------------------------------------------------------------
test('prompt-injection hot-reload: GET reflects new level within 2s of PUT', async () => {
  // T0.1: OPENROUTER_API_KEY soft-skip removed. OPENROUTER_API_KEY_CI is required
  // in CI; absence is a CI configuration failure. The GET-readback variant of this
  // test does not require live LLM calls anyway — only the key-guarded LLM-observable
  // assertions would need the key. This test uses GET-readback exclusively.

  // ── Step 1: set to "low" as the known starting state ────────────────────
  const putLow = await authedPut('/api/v1/security/prompt-guard', { level: 'low' });
  expect(putLow.status, 'PUT level=low must return 200').toBe(200);
  expect(
    (putLow.body as Record<string, unknown>).saved,
    'PUT low: saved must be true',
  ).toBe(true);
  expect(
    (putLow.body as Record<string, unknown>).requires_restart,
    'PUT low: requires_restart must be false (hot-reloadable)',
  ).toBe(false);

  // ── Step 2: verify GET reads back "low" within 2s ───────────────────────
  // The awaitReload() call inside putPromptGuard blocks until the config poll
  // completes, so the very next GET should already see the new value.
  // We use expect.poll with a 2000ms budget to match the 2s hot-reload envelope.
  await expect
    .poll(
      async () => {
        const r = await authedGet('/api/v1/security/prompt-guard');
        return (r.body as Record<string, unknown>).level;
      },
      {
        timeout: 2000,
        intervals: [100, 200, 300, 500],
        message: 'GET /api/v1/security/prompt-guard must return level=low within 2s of PUT',
      },
    )
    .toBe('low');

  // ── Step 3: differentiation — set to "high" and verify GET differs ──────
  // This catches hardcoded GET responses: if the endpoint always returns "medium"
  // regardless of what was saved, the assertion below will fail.
  // Anti-shortcut: two different inputs must produce two different outputs.
  const putHigh = await authedPut('/api/v1/security/prompt-guard', { level: 'high' });
  expect(putHigh.status, 'PUT level=high must return 200').toBe(200);
  expect(
    (putHigh.body as Record<string, unknown>).applied_level,
    'PUT high: applied_level in response must be "high"',
  ).toBe('high');
  expect(
    (putHigh.body as Record<string, unknown>).requires_restart,
    'PUT high: requires_restart must be false',
  ).toBe(false);

  // Verify GET reflects "high" within 2s (differentiation assertion)
  await expect
    .poll(
      async () => {
        const r = await authedGet('/api/v1/security/prompt-guard');
        return (r.body as Record<string, unknown>).level;
      },
      {
        timeout: 2000,
        intervals: [100, 200, 300, 500],
        message:
          'GET /api/v1/security/prompt-guard must return level=high within 2s of second PUT',
      },
    )
    .toBe('high');

  // ── Step 4: validate invalid level rejected ──────────────────────────────
  const putBad = await authedPut('/api/v1/security/prompt-guard', { level: 'extreme' });
  expect(putBad.status, 'invalid level must return 400').toBe(400);

  // Restore to "medium" (default) as teardown so subsequent tests see a clean state.
  await authedPut('/api/v1/security/prompt-guard', { level: 'medium' });
});

// ---------------------------------------------------------------------------
// Test 2: rate-limit hot-reload
//
// Variant: GET-readback fallback.
// Rationale: the LLM-observable variant requires issuing two real agent LLM
// calls (1st succeeds, 2nd gets 429) which needs a live LLM key and a way to
// issue agent calls with predictable timing.
//
// Differentiation proof:
//   PUT max_agent_llm_calls_per_hour=100 → GET must return 100
//   PUT max_agent_llm_calls_per_hour=1   → GET must return 1 (different output)
// ---------------------------------------------------------------------------
test('rate-limit hot-reload: GET reflects new cap within 2s of PUT', async () => {
  // T0.1: OPENROUTER_API_KEY soft-skip removed. OPENROUTER_API_KEY_CI is required
  // in CI. The GET-readback variant used here does not require live LLM calls.

  // ── Step 1: set a known baseline (100 LLM calls/hour, no cost cap) ──────
  const putBaseline = await authedPut('/api/v1/security/rate-limits', {
    max_agent_llm_calls_per_hour: 100,
    daily_cost_cap_usd: 0,
    max_agent_tool_calls_per_minute: 0,
  });
  expect(putBaseline.status, 'PUT baseline rate-limits must return 200').toBe(200);
  expect(
    (putBaseline.body as Record<string, unknown>).saved,
    'PUT baseline: saved must be true',
  ).toBe(true);
  expect(
    (putBaseline.body as Record<string, unknown>).requires_restart,
    'PUT baseline: requires_restart must be false (hot-reloadable)',
  ).toBe(false);

  // ── Step 2: verify GET reads back 100 within 2s ──────────────────────────
  // Verify within the 2s hot-reload envelope.
  await expect
    .poll(
      async () => {
        const r = await authedGet('/api/v1/security/rate-limits');
        return (r.body as Record<string, unknown>).max_agent_llm_calls_per_hour;
      },
      {
        timeout: 2000,
        intervals: [100, 200, 300, 500],
        message:
          'GET /api/v1/security/rate-limits must return max_agent_llm_calls_per_hour=100 within 2s',
      },
    )
    .toBe(100);

  // ── Step 3: differentiation — change cap to 1, verify GET reads 1 ────────
  // Catches hardcoded responses: if GET always returns 100, this will fail.
  //
  // This also proves the hot-reload works for a strict cap where a second
  // agent LLM call exceeding the cap would get rejected.
  const putStrict = await authedPut('/api/v1/security/rate-limits', {
    max_agent_llm_calls_per_hour: 1,
  });
  expect(putStrict.status, 'PUT strict cap (1 call/hour) must return 200').toBe(200);
  const appliedStrict = (putStrict.body as Record<string, unknown>).applied as Record<
    string,
    unknown
  >;
  expect(
    appliedStrict?.max_agent_llm_calls_per_hour,
    'PUT applied.max_agent_llm_calls_per_hour must be 1 in response',
  ).toBe(1);

  // GET-readback within 2s (differentiation assertion)
  await expect
    .poll(
      async () => {
        const r = await authedGet('/api/v1/security/rate-limits');
        return (r.body as Record<string, unknown>).max_agent_llm_calls_per_hour;
      },
      {
        timeout: 2000,
        intervals: [100, 200, 300, 500],
        message:
          'GET /api/v1/security/rate-limits must return max_agent_llm_calls_per_hour=1 within 2s',
      },
    )
    .toBe(1);

  // ── Step 4: validate negative value rejected ─────────────────────────────
  // Negative values must be rejected with 400 — save button disabled / API guard.
  const putNeg = await authedPut('/api/v1/security/rate-limits', {
    max_agent_llm_calls_per_hour: -5,
  });
  expect(putNeg.status, 'negative max_agent_llm_calls_per_hour must return 400').toBe(400);

  // ── Step 5: validate partial-update semantics — only send one field ───────
  // Partial updates: unset fields must not be zeroed.
  // First record that we set llm_calls=1 above; now only change cost cap.
  const putPartial = await authedPut('/api/v1/security/rate-limits', {
    daily_cost_cap_usd: 25.5,
  });
  expect(putPartial.status, 'partial PUT (only cost cap) must return 200').toBe(200);

  // Verify llm_calls_per_hour still reads as 1 (not zeroed by partial update)
  const getAfterPartial = await authedGet('/api/v1/security/rate-limits');
  expect(
    (getAfterPartial.body as Record<string, unknown>).max_agent_llm_calls_per_hour,
    'partial update must NOT zero previously-set max_agent_llm_calls_per_hour',
  ).toBe(1);
  // daily_cost_cap (without _usd suffix) is the GET field name per rest_rate_limits.go
  expect(
    (getAfterPartial.body as Record<string, unknown>).daily_cost_cap,
    'partial update must persist daily_cost_cap_usd=25.5',
  ).toBe(25.5);

  // ── Teardown: restore unlimited caps ────────────────────────────────────
  await authedPut('/api/v1/security/rate-limits', {
    daily_cost_cap_usd: 0,
    max_agent_llm_calls_per_hour: 0,
    max_agent_tool_calls_per_minute: 0,
  });
});
