import { request } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';
import { onboardViaAPI } from './fixtures/onboard-via-api.js';

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
    path.dirname(fileURLToPath(import.meta.url)),
    'fixtures/.auth/admin.json',
  );

// T0.4: Preflight — fail fast if required environment variables are missing.
// OPENROUTER_API_KEY_CI is required. Its absence is a CI configuration failure,
// not a per-test skip condition. Tests that need a real LLM will fail anyway;
// this preflight surfaces the root cause immediately instead of after 60s timeouts.
function preflightCheck(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error(
      '[E2E preflight] OPENROUTER_API_KEY_CI is not set.\n' +
      'This environment variable is REQUIRED for the E2E suite.\n' +
      'Tests that require a live LLM will fail without it, and soft-skipping\n' +
      'on its absence is no longer permitted (see tests/e2e/README.md).\n\n' +
      'To fix:\n' +
      '  export OPENROUTER_API_KEY_CI="<your OpenRouter key>"\n' +
      'Or in CI: add OPENROUTER_API_KEY_CI to your secrets and inject it\n' +
      'as an env var in the Playwright workflow step (see .github/workflows/pr.yml).',
    );
  }
}

/**
 * Validate or seed the gateway config.json in OMNIPUS_HOME before the test
 * suite runs.
 *
 * Two concerns that are restart-gated (only take effect when the gateway BOOTS):
 *
 * 1. sandbox.audit_log — must be true at boot for T26. The audit subsystem creates
 *    ~/.omnipus/system/audit.jsonl only when cfg.Sandbox.AuditLog=true at startup
 *    (pkg/agent/loop.go:346). Toggling via REST mid-run has no effect.
 *
 * 2. tools.delegate.enabled:false — migrateDeprecatedToolEnableFlags (pkg/config/migration.go)
 *    converts this into a GLOBAL sandbox.tool_policies["delegate"]="deny" at config-load
 *    time, denying delegate for ALL agents. This breaks T24 (cancel cascade) and the
 *    handoff/subagent E2E tests. The fix is to have sandbox.tool_policies.delegate="allow"
 *    explicitly, which is never downgraded by the migrator, and to ensure the legacy
 *    tools.delegate.enabled=false key is absent.
 *
 * Strategy:
 *   - If config.json is absent from OMNIPUS_HOME, write a minimal correct config.
 *     This is the fast path for local dev: developer sets OMNIPUS_HOME, runs playwright,
 *     and the config is seeded correctly the first time.
 *   - If config.json exists but has tools.delegate.enabled:false or lacks sandbox.audit_log,
 *     fail loudly with an actionable error instead of silently producing confusing T24/T26
 *     failures 10 minutes into the test run.
 *
 * Note: OMNIPUS_HOME must exist before this function runs. The CI workflow creates
 * it with `mkdir -p` before calling `cat > config.json`. For local dev the developer
 * must set OMNIPUS_HOME before running `npx playwright test`.
 */
function validateOrSeedGatewayConfig(): void {
  const omnipusHome =
    process.env.OMNIPUS_HOME ||
    (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

  if (!fs.existsSync(omnipusHome)) {
    // OMNIPUS_HOME does not exist — the gateway cannot start without it.
    // Fail fast so the developer sees a clear error before a timeout spiral.
    throw new Error(
      `[E2E preflight] OMNIPUS_HOME directory does not exist: ${omnipusHome}\n` +
      'Create it and write a config.json before starting the gateway:\n\n' +
      `  mkdir -p ${omnipusHome}\n` +
      `  cat > ${omnipusHome}/config.json <<'EOF'\n` +
      '  {\n' +
      '    "version": 1,\n' +
      '    "gateway": { "port": 6060, "dev_mode_bypass": true },\n' +
      '    "sandbox": { "audit_log": true, "tool_policies": { "delegate": "allow" } }\n' +
      '  }\n' +
      '  EOF\n\n' +
      'Then start the gateway:\n' +
      '  OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &\n' +
      'See tests/e2e/README.md for the full local-run checklist.',
    );
  }

  const configPath = path.join(omnipusHome, 'config.json');

  if (!fs.existsSync(configPath)) {
    // Config absent — write a minimal correct config so the NEXT gateway start is correct.
    // (The current gateway run may already be in flight without these settings — warn.)
    const minimalConfig = {
      version: 1,
      gateway: { port: 6060, dev_mode_bypass: true },
      sandbox: {
        audit_log: true,
        tool_policies: { delegate: 'allow' },
      },
    };
    fs.writeFileSync(configPath, JSON.stringify(minimalConfig, null, 2), { mode: 0o600 });
    console.warn(
      `[E2E global-setup] config.json was absent — wrote minimal config to ${configPath}.\n` +
      'IMPORTANT: sandbox.audit_log and tool_policies.delegate are restart-gated. If the gateway\n' +
      'is already running without these settings, T24 and T26 will fail. Restart the gateway\n' +
      'and re-run the suite.',
    );
    return;
  }

  // Config exists — validate for the two known blockers.
  let cfg: Record<string, unknown>;
  try {
    cfg = JSON.parse(fs.readFileSync(configPath, 'utf-8')) as Record<string, unknown>;
  } catch {
    // Unparseable config — let the gateway handle it; don't block tests.
    return;
  }

  const errors: string[] = [];

  // Check 1: tools.delegate.enabled:false — the legacy deprecation trap.
  // migrateDeprecatedToolEnableFlags reads raw JSON to detect EXPLICIT false values,
  // so we check the parsed object here to give a user-friendly error.
  const tools = cfg.tools as Record<string, { enabled?: boolean }> | undefined;
  if (tools?.delegate?.enabled === false) {
    errors.push(
      'config.json has "tools": {"delegate": {"enabled": false}}.\n' +
      'migrateDeprecatedToolEnableFlags (pkg/config/migration.go) converts this into\n' +
      'sandbox.tool_policies["delegate"]="deny" at config-load time, breaking T24 (cancel\n' +
      'cascade) and handoff/subagent E2E tests.\n' +
      'FIX: remove the tools.delegate key entirely and add:\n' +
      '  "sandbox": { "tool_policies": { "delegate": "allow" } }',
    );
  }

  // Check 2: sandbox.audit_log must be true for T26.
  const sandbox = cfg.sandbox as { audit_log?: boolean; tool_policies?: Record<string, string> } | undefined;
  if (!sandbox?.audit_log) {
    errors.push(
      'config.json is missing sandbox.audit_log:true.\n' +
      'sandbox.audit_log is restart-gated — the audit subsystem (audit.jsonl) is only\n' +
      'created at boot when this flag is true. T26 will fail without it.\n' +
      'FIX: add "sandbox": { "audit_log": true } to config.json and restart the gateway.',
    );
  }

  if (errors.length > 0) {
    throw new Error(
      `[E2E preflight] Gateway config has ${errors.length} blocker(s) for T24/T26:\n\n` +
      errors.map((e, i) => `  [${i + 1}] ${e}`).join('\n\n') +
      '\n\n' +
      `Config path: ${configPath}\n` +
      'See .github/workflows/pr.yml "Seed gateway config" step for the canonical config.\n' +
      'See tests/e2e/README.md for the full local-run checklist.',
    );
  }
}

/**
 * Global setup: seed admin + provider via REST and write the auth storage
 * state file directly — no browser-rendered login flow required.
 *
 * Motivation for the API-only approach (vs. a full browser-driven login):
 *
 * The original global-setup navigated a headless Chromium browser to `/`, waited
 * for the React SPA to load its ~5 MB JS bundle, parse it, bootstrap the router,
 * redirect to `/login`, and render the login form — then drove the form inputs.
 * On this server the SPA cold-start takes >5 s in headless mode, which exceeded
 * the 5 s `isVisible({ timeout: 5_000 })` guard in `loginAs()`, causing
 * consistent global-setup failures. Driving `POST /api/v1/auth/login` directly
 * via Playwright's `APIRequestContext` avoids that cold-start cost while still
 * being a "real" login: it is the exact same `HandleLogin` handler
 * (`pkg/gateway/rest_auth.go`) the login FORM posts to, so the server mints and
 * persists the bcrypt `SessionTokenHash` exactly as it would for a UI-driven
 * login — this is not direct cookie fabrication (spec regression note #7 /
 * ADR-044 r1 CRIT-002: "direct cookie injection without a seeded hash fails
 * validation").
 *
 * ADR-044 / US-5 cookie-auth cutover (2026-07-15): the SPA no longer stores or
 * reads a JS-visible bearer token at all — `src/lib/api.ts` deleted
 * `getAuthHeaders()` and every request now goes out with `credentials:
 * 'include'`, authenticated solely by the browser-managed `omnipus-session`
 * HttpOnly cookie (see `pkg/gateway/middleware/session_cookie.go`). The route
 * guard (`src/routes/_app.tsx` `beforeLoad`) no longer inspects local/session
 * storage for a token — it always asks the server via `validateToken()`, which
 * rides the cookie automatically. That means a Playwright `storageState` that
 * only carries `localStorage.omnipus_auth_token` (the pre-ADR-044 shape) now
 * runs every "authenticated" spec LOGGED OUT: the cookie is invisible to JS, so
 * there is no way to inject it via localStorage — it can only be captured from
 * a real Set-Cookie response and replayed as an actual `storageState.cookies`
 * entry.
 *
 * The strategy:
 *   1. `onboardViaAPI` — idempotent REST call to seed admin credentials
 *      (200 = fresh onboard, 409 = already complete, both succeed).
 *   2. `POST /api/v1/auth/login` — a real login against `HandleLogin`. The
 *      response sets BOTH the `omnipus-session` HttpOnly cookie
 *      (`middleware.WriteSessionCookie`) and the CSRF cookie
 *      (`middleware.IssueCSRFCookie`). We read both back out of the
 *      `APIRequestContext`'s cookie jar (which already parsed the Set-Cookie
 *      headers into the exact `{name,value,domain,path,expires,httpOnly,
 *      secure,sameSite}` shape Playwright's `storageState.cookies` expects —
 *      no manual reconstruction needed).
 *   3. Write a Playwright storageState JSON directly to the auth file with
 *      those cookies. Playwright injects `storageState.cookies` into every
 *      test's browser context before the first navigation, so `beforeLoad()`'s
 *      `validateToken()` call rides an already-valid session cookie with no
 *      login redirect.
 *
 * The bearer `token` from the login response is still validated (proof the
 * login itself succeeded) but is deliberately NOT persisted to localStorage
 * anymore — the SPA never reads it, and writing it back would resurrect the
 * exact JS-readable-token surface ADR-044 removed.
 */
async function globalSetup(): Promise<void> {
  // T0.4: Run preflight checks before any browser/gateway interaction.
  preflightCheck();

  // Validate or seed the gateway config.json for T24 (delegate allow) and T26 (audit_log).
  // This must run before onboardViaAPI so a misconfigured gateway is caught early.
  validateOrSeedGatewayConfig();

  const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';

  // Step 1: Seed admin user + provider idempotently.
  // 200 = fresh onboard, 409 = already complete — both succeed.
  await onboardViaAPI({ baseURL });

  // Step 2: Perform a real login via REST and capture the cookies the
  // gateway issues. We keep the APIRequestContext alive until we extract
  // cookies — Playwright's APIRequestContext parses every Set-Cookie header
  // from the response into its own cookie jar (same as a browser would), so
  // `ctx.storageState()` after the login call already holds both:
  //   - `omnipus-session` (HttpOnly) — the primary auth credential (US-5 /
  //     FR-010). Without this, beforeLoad()'s validateToken() 401s and every
  //     "authenticated" spec gets redirected to /login.
  //   - `__Host-csrf` (TLS) or `csrf` (plain HTTP) — needed for state-changing
  //     API calls (createAgent, etc.) to pass the client-side CSRF-cookie-
  //     presence gate before fetch fires.
  const ctx = await request.newContext({ baseURL });
  // Element type of ctx.storageState().cookies — Playwright does not export a
  // standalone `Cookie` type from `@playwright/test`, so it's derived here
  // rather than hand-declared (which would drift from the real shape).
  type StorageStateCookie = Awaited<ReturnType<typeof ctx.storageState>>['cookies'][number];
  let sessionCookie: StorageStateCookie | null = null;
  let csrfCookie: StorageStateCookie | null = null;
  try {
    const res = await ctx.post('/api/v1/auth/login', {
      data: { username: 'admin', password: 'admin123' },
    });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`POST /api/v1/auth/login failed: ${res.status()} — ${body}`);
    }
    // Validate the response shape (proof the login itself succeeded and
    // returned the expected LoginResponse contract) — the bearer token
    // itself is deliberately not persisted anywhere; the SPA never reads one
    // anymore (ADR-044), so there is nothing to write it into.
    const json = (await res.json()) as { token: string };
    if (!json.token) throw new Error('Login response missing token field');

    // Extract the omnipus-session and CSRF cookies from the context's cookie
    // jar. Both are issued by HandleLogin (pkg/gateway/rest_auth.go):
    // middleware.WriteSessionCookie + middleware.IssueCSRFCookie. These
    // objects are already in Playwright's exact storageState.cookies shape
    // ({name,value,domain,path,expires,httpOnly,secure,sameSite}) — no manual
    // reconstruction needed, so we use them verbatim.
    const ctxState = await ctx.storageState();
    for (const cookie of ctxState.cookies) {
      if (cookie.name === 'omnipus-session') {
        sessionCookie = cookie;
      } else if (cookie.name === '__Host-csrf' || cookie.name === 'csrf') {
        csrfCookie = cookie;
      }
    }

    if (!sessionCookie) {
      throw new Error(
        '[E2E global-setup] POST /api/v1/auth/login did not set the ' +
        '`omnipus-session` cookie. Expected a Set-Cookie header from ' +
        'middleware.WriteSessionCookie (pkg/gateway/rest_auth.go HandleLogin). ' +
        'Without this cookie every "authenticated" e2e spec runs logged out ' +
        '(see ADR-044 / preview-on-main-listener-spec.md S8/S9).',
      );
    }
  } finally {
    await ctx.dispose();
  }

  // Step 3: Write the Playwright storageState file directly.
  // Format: https://playwright.dev/docs/auth#reuse-signed-in-state
  // `cookies` carries the real omnipus-session + CSRF cookies captured above
  // so every test's browser context starts already authenticated — the SPA
  // has no JS-readable token to inject via localStorage anymore (ADR-044).
  // `omnipus_auth_username` is still written to localStorage: it is NOT an
  // auth credential, just the display-only "logged in as X" value the auth
  // store reads directly (src/store/auth.ts) without a round trip.
  const authDir = path.dirname(AUTH_FILE);
  if (!fs.existsSync(authDir)) {
    fs.mkdirSync(authDir, { recursive: true });
  }

  const cookies = [sessionCookie, csrfCookie].filter(
    (c): c is NonNullable<typeof c> => c !== null,
  );

  const storageState = {
    cookies,
    origins: [
      {
        origin: baseURL,
        localStorage: [
          { name: 'omnipus_auth_username', value: 'admin' },
        ],
      },
    ],
  };

  fs.writeFileSync(AUTH_FILE, JSON.stringify(storageState, null, 2));
}

export default globalSetup;
