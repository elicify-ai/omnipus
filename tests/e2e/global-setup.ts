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
 * Motivation for the API-only approach (vs. the original browser-based login):
 *
 * The original global-setup navigated a headless Chromium browser to `/`, waited
 * for the React SPA to load its ~5 MB JS bundle, parse it, bootstrap the router,
 * redirect to `/login`, and render the login form — then drove the form inputs.
 * On this server the SPA cold-start takes >5 s in headless mode, which exceeded
 * the 5 s `isVisible({ timeout: 5_000 })` guard in `loginAs()`, causing
 * consistent global-setup failures.
 *
 * The replacement strategy:
 *   1. `onboardViaAPI` — idempotent REST call to seed admin credentials
 *      (200 = fresh onboard, 409 = already complete, both succeed).
 *   2. `POST /api/v1/auth/login` — obtain a valid bearer token.
 *   3. Write a Playwright storageState JSON directly to the auth file with the
 *      token in the `localStorage` of the correct origin.  Playwright reads this
 *      file at test startup and injects the localStorage entries into every test
 *      page before navigation, so the SPA's `beforeLoad()` guard finds the
 *      token without a login redirect.
 *
 * TOKEN MIGRATION NOTE: The SPA reads omnipus_auth_token from sessionStorage
 * first, then localStorage (src/routes/_app.tsx:23). Playwright storageState
 * captures localStorage but NOT sessionStorage. Writing the token to localStorage
 * is sufficient — the SPA's `beforeLoad()` will pick it up.
 *
 * The auth store (src/store/auth.ts) replicates the token from localStorage to
 * sessionStorage on mount, so after the first render sessionStorage is also set.
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

  // Step 2: Obtain a valid bearer token + CSRF cookie via REST.
  // We keep the context alive until we extract cookies so the CSRF cookie set
  // by the login response can be included in the storageState. Without the
  // CSRF cookie, SPA tests that call state-changing API endpoints (like
  // createAgent) fail the client-side cookie-presence gate before fetch fires.
  const ctx = await request.newContext({ baseURL });
  let token: string;
  let csrfCookieValue: string | null = null;
  try {
    const res = await ctx.post('/api/v1/auth/login', {
      data: { username: 'admin', password: 'admin123' },
    });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`POST /api/v1/auth/login failed: ${res.status()} — ${body}`);
    }
    const json = (await res.json()) as { token: string };
    if (!json.token) throw new Error('Login response missing token field');
    token = json.token;

    // Extract the CSRF cookie from the context's cookie store.
    // The login endpoint issues either __Host-csrf (TLS) or csrf (HTTP).
    // We include it in storageState so browser page contexts have it on startup.
    const ctxState = await ctx.storageState();
    for (const cookie of ctxState.cookies) {
      if (cookie.name === '__Host-csrf' || cookie.name === 'csrf') {
        csrfCookieValue = cookie.value;
        break;
      }
    }
  } finally {
    await ctx.dispose();
  }

  // Step 3: Write the Playwright storageState file directly.
  // Format: https://playwright.dev/docs/auth#reuse-signed-in-state
  // The `origins` array contains one entry per origin.  Playwright injects
  // the `localStorage` items into the browser context for that origin before
  // each test's page navigation, so the SPA sees the token on first load.
  //
  // Cookies are also included so the browser context has the CSRF cookie that
  // was issued by the login endpoint. Without it, SPA mutations (createAgent,
  // etc.) fail the client-side CSRF-cookie-presence gate before fetch fires.
  const authDir = path.dirname(AUTH_FILE);
  if (!fs.existsSync(authDir)) {
    fs.mkdirSync(authDir, { recursive: true });
  }

  // Build the URL object so we can derive the correct cookie domain/path.
  const url = new URL(baseURL);
  const cookieDomain = url.hostname; // e.g. "localhost"
  const cookieSecure = url.protocol === 'https:';

  // Include both __Host-csrf (TLS) and csrf (HTTP) cookie names so the cookie
  // is present regardless of which flavor the gateway issued.
  const csrfCookies = csrfCookieValue
    ? [
        {
          name: cookieSecure ? '__Host-csrf' : 'csrf',
          value: csrfCookieValue,
          domain: cookieDomain,
          path: '/',
          expires: -1, // session cookie
          httpOnly: false,
          secure: cookieSecure,
          sameSite: 'Strict' as const,
        },
      ]
    : [];

  const storageState = {
    cookies: csrfCookies,
    origins: [
      {
        origin: baseURL,
        localStorage: [
          { name: 'omnipus_auth_token', value: token },
          { name: 'omnipus_auth_role', value: 'admin' },
          { name: 'omnipus_auth_username', value: 'admin' },
        ],
      },
    ],
  };

  fs.writeFileSync(AUTH_FILE, JSON.stringify(storageState, null, 2));
}

export default globalSetup;
