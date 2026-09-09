import {
  request as apiRequest,
  type APIRequestContext,
  type Cookie,
  type Page,
} from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

/**
 * Admin-authenticated REST context for spec SETUP/TEARDOWN calls — built from
 * the shared storageState session, never from a fresh login.
 *
 * ── Why this exists (regression, 2026-08-28) ────────────────────────────────
 * `POST /api/v1/auth/login` does two things, and specs only ever wanted the
 * first: it returns a bearer token, AND it re-mints the **single-slot**
 * `session_token_hash` on the admin account (`HandleLogin`,
 * pkg/gateway/rest_auth.go — "Session-cookie token remains single-slot … 
 * overwrite as before"). That second effect invalidates the `omnipus-session`
 * cookie held in the shared storageState file, for every spec that runs later.
 *
 * calendar.spec.ts and calendar-recurrence.spec.ts each called login purely to
 * get a bearer token for REST setup. Playwright runs spec FILES alphabetically,
 * so both landed between `auth.spec.ts` (whose afterAll refreshes storageState)
 * and `create-agent.spec.ts` — which then ran with a dead cookie.
 *
 * That was invisible on every route but one. The e2e gateway runs with
 * `gateway.dev_mode_bypass: true`, and `checkBearerAuth` (pkg/gateway/auth.go)
 * short-circuits to the synthetic `_dev_bypass` identity BEFORE its cookie
 * branch for any request with no `Authorization: Bearer` header — which is
 * every SPA request since ADR-044. So all `withAuth` routes returned 200
 * without ever testing the cookie. `GET /api/v1/providers` is
 * `withOptionalAuth` + `requireAuthOutsideOnboarding`, which deliberately does
 * NOT honour bypass, so it alone 401'd — surfacing in the UI as the create-agent
 * wizard's model picker rendering its `role="alert"` error state instead of a
 * combobox. Proven by bcrypt-verifying the cookie the browser actually sent
 * against the shard's persisted `session_token_hash`: no match.
 *
 * ── The rule ────────────────────────────────────────────────────────────────
 * A spec that needs REST auth must use this helper. Only `global-setup.ts`,
 * `fixtures/login.ts` and `auth.spec.ts` (which tests the login flow itself,
 * and refreshes storageState afterwards) may call `POST /api/v1/auth/login`.
 * `scripts/check-e2e-login-crosstalk.sh` enforces that and names any offender.
 *
 * Authentication here is the session cookie, so the CSRF double-submit applies
 * on state-changing methods — hence the echoed `X-Csrf-Token` header. (Bearer
 * callers are exempt from that gate; cookie callers are not. See
 * pkg/gateway/middleware/csrf.go.)
 */

/** Mirrors playwright.config.ts:storageState, global-setup.ts and auth.spec.ts. */
export const ADMIN_AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(fileURLToPath(import.meta.url)),
      '.auth/admin.json',
    );

const SESSION_COOKIE_NAME = 'omnipus-session';
/** TLS-only name first, then the plain-HTTP fallback (csrf.go issues one or the other). */
const CSRF_COOKIE_NAMES = ['__Host-csrf', 'csrf'];

interface StoredCookie {
  name: string;
  value: string;
}

/**
 * Build an APIRequestContext authenticated as admin via the shared session
 * cookie. Caller owns disposal (`await ctx.dispose()`).
 *
 * Throws — loudly, naming the file — if the storageState is missing or carries
 * no session. A silent fall-through to an anonymous context would pass under
 * dev_mode_bypass and hide exactly the class of bug this helper exists to stop.
 */
export async function newAdminApiContext(): Promise<APIRequestContext> {
  const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';

  let raw: string;
  try {
    raw = fs.readFileSync(ADMIN_AUTH_FILE, 'utf8');
  } catch (err) {
    throw new Error(
      `newAdminApiContext: cannot read storageState at ${ADMIN_AUTH_FILE} ` +
        '(global-setup.ts writes it; OMNIPUS_AUTH_FILE overrides the path)',
      { cause: err },
    );
  }

  const cookies = (JSON.parse(raw) as { cookies?: StoredCookie[] }).cookies ?? [];
  const session = cookies.find((c) => c.name === SESSION_COOKIE_NAME);
  if (!session) {
    throw new Error(
      `newAdminApiContext: no ${SESSION_COOKIE_NAME} cookie in ${ADMIN_AUTH_FILE}. ` +
        'The shared session was never established or was rotated out by a stray login.',
    );
  }
  const csrf = cookies.find((c) => CSRF_COOKIE_NAMES.includes(c.name));
  if (!csrf) {
    throw new Error(
      `newAdminApiContext: no CSRF cookie (${CSRF_COOKIE_NAMES.join(' | ')}) in ` +
        `${ADMIN_AUTH_FILE}; state-changing REST calls would 403.`,
    );
  }

  return apiRequest.newContext({
    baseURL,
    storageState: ADMIN_AUTH_FILE,
    extraHTTPHeaders: { 'X-Csrf-Token': csrf.value },
  });
}

/**
 * Re-arm a live browser context with the SHARED admin session, minting nothing.
 *
 * ── The problem this replaces ───────────────────────────────────────────────
 * During a UAT with several people driving one gateway, any other run's login
 * rotates the single-slot `session_token_hash` and this run's cookie dies
 * mid-test; the SPA then says "Your session expired". The obvious repair —
 * `POST /api/v1/auth/login` — fixes the caller and breaks everyone else, which
 * is precisely the crosstalk `scripts/check-e2e-login-crosstalk.sh` forbids.
 *
 * ── What this does instead ──────────────────────────────────────────────────
 * It re-applies the cookies from the shared storageState file to the context.
 * That recovers the real case a spec can recover from: `auth.spec.ts`'s
 * afterAll (and global-setup) REWRITE that file, so a context created before
 * the rewrite is stale while the file on disk is current. Copying the file
 * forward costs one local read and rotates nobody's token.
 *
 * It deliberately cannot repair a session that is dead everywhere — no spec
 * should be able to. When the shared session itself is gone, the caller's own
 * eviction handling reports BLOCKED, which is the honest outcome: an
 * environment collision, reported as one, rather than a green run bought by
 * evicting the next tester.
 *
 * Returns the session cookie's value so a caller can tell whether the file
 * actually moved on.
 */
export async function restoreAdminSession(page: Page): Promise<string> {
  let raw: string;
  try {
    raw = fs.readFileSync(ADMIN_AUTH_FILE, 'utf8');
  } catch (err) {
    throw new Error(
      `restoreAdminSession: cannot read storageState at ${ADMIN_AUTH_FILE} ` +
        '(global-setup.ts writes it; OMNIPUS_AUTH_FILE overrides the path)',
      { cause: err },
    );
  }

  const cookies = (JSON.parse(raw) as { cookies?: Cookie[] }).cookies ?? [];
  const session = cookies.find((c) => c.name === SESSION_COOKIE_NAME);
  if (!session) {
    throw new Error(
      `restoreAdminSession: no ${SESSION_COOKIE_NAME} cookie in ${ADMIN_AUTH_FILE}. ` +
        'The shared session was never established or was rotated out by a stray login.',
    );
  }

  await page.context().addCookies(cookies);
  return session.value;
}
