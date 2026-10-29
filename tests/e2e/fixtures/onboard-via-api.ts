import { E2E_FALLBACK, E2E_PRIMARY } from './e2e-model.js';
import { keyEnvNamesFor, lookupModelKey } from './e2e-model-keys.js';

// The models all e2e tests run on are the ONE central setting,
// tests/e2e/e2e-model.json (a primary and a fallback pair), threaded through
// fixtures/e2e-model.ts and, for the shell surfaces (pr.yml, sandbox-uat.yml,
// the Fly CI runner's runci.sh), scripts/e2e-model.sh.
//
// NOTE: the nightly evals (.github/workflows/evals-nightly.yml +
// evals/cmd/eval-runner) follow the SAME central setting: the evals agent
// and the judge default to its primary, with its fallback (founder decision
// 2026-09-27), so tests and evals cannot drift apart on which model they run.
// Credentials never travel through Playwright's request client: the Playwright
// test runner records every request a context sends — JSON bodies included —
// into the trace, and CI uploads retained traces as artifacts (artifacts are not
// secret-masked). This file therefore talks to the gateway with Node's own
// fetch, which no trace observes. tests/e2e/trace-secret-canary.spec.ts pins
// that a canary key sent through here never appears in a retained trace.
const DEFAULT_USERNAME = 'admin';

interface PlainResponse {
  status: number;
  ok: boolean;
  text: string;
}

/** One JSON request with Node fetch (NOT Playwright's request client). */
async function send(
  baseURL: string,
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  body?: unknown,
  headers: Record<string, string> = {},
): Promise<PlainResponse> {
  const res = await fetch(new URL(path, baseURL), {
    method,
    headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  return { status: res.status, ok: res.ok, text };
}

function parseJSON(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return undefined;
  }
}
const DEFAULT_PASSWORD = 'admin123';

export interface OnboardingOptions {
  baseURL: string;
  providerID?: string;
  apiKey?: string;
  model?: string;
  username?: string;
  /** @minLength 8 — backend enforces ≥8 chars; W4-7 throws at fixture-build time if violated. */
  password?: string;
}

/**
 * Call POST /api/v1/onboarding/complete to seed an admin user + provider
 * without navigating the UI wizard. Bypasses the "Continue button stays
 * disabled because no model was auto-selected" trap in the UI flow.
 *
 * Contract from pkg/gateway/rest_onboarding.go:
 *   - Endpoint is CSRF-exempt (see rest_onboarding.go:310).
 *   - Body: { provider: {id, api_key, model}, admin: {username, password} }.
 *   - 200 on success, 409 if already complete — both are treated as success.
 *   - Password must be ≥8 characters.
 *   - The backend PROBES the key against the real provider before
 *     completing onboarding (see the "Provider API-key validation" block in
 *     rest_onboarding.go) and returns 400 when the provider confirms the key
 *     is wrong (`providers.OutcomeInvalidKey`) — no_credit / unreachable /
 *     restricted still proceed with a warning.
 *
 * Provider and model default to the central primary (E2E_PRIMARY). The key is
 * looked up by provider (fixtures/e2e-model-keys.ts): MINIMAX_API_KEY_CI then
 * MINIMAX_API_KEY for the MiniMax primary, OPENROUTER_API_KEY_CI then
 * OPENROUTER_API_KEY for OpenRouter. A missing key throws immediately, before
 * any request, naming both variables — a placeholder key would only fail
 * later and more confusingly.
 *
 * After a fresh onboard (200) it seeds the fallback, because the product falls
 * back per agent only (issue #1340):
 *   1. PUT /api/v1/providers/<fallback provider> with the fallback key and model;
 *   2. GET /api/v1/agents;
 *   3. PUT /api/v1/agents/<id> {fallback_models:[fallback]} for each agent.
 * A 409 (onboarded earlier by another caller, which seeded the fallback then)
 * skips that seeding. When the job already runs on the fallback (job-start
 * check), there is nothing to fall back to and the seeding is skipped too.
 */
export async function onboardViaAPI(opts: OnboardingOptions): Promise<void> {
  // W4-7: validate password length at fixture-build time, not at 400-response time.
  // The backend requires >= 8 characters; failing here gives a clear error message.
  const password = opts.password ?? DEFAULT_PASSWORD;
  if (password.length < 8) {
    throw new Error(
      `onboard-via-api: password must be at least 8 characters (got ${password.length})`
    );
  }

  const providerID = opts.providerID ?? E2E_PRIMARY.provider;
  const model = opts.model ?? E2E_PRIMARY.model;
  const apiKey = opts.apiKey ?? lookupModelKey(providerID);
  if (!apiKey) {
    const [ciName, localName] = keyEnvNamesFor(providerID);
    throw new Error(
      `onboard-via-api: no API key for the primary provider "${providerID}". Set ${ciName} ` +
        `(CI) or ${localName} (local), or pass { apiKey } explicitly. Onboarding ` +
        'probes the key against the real provider before completing (see ' +
        'pkg/gateway/rest_onboarding.go), so the suite cannot run against a placeholder.'
    );
  }

  const seedFallback =
    !(providerID === E2E_FALLBACK.provider && model === E2E_FALLBACK.model);
  let fallbackKey: string | undefined;
  if (seedFallback) {
    fallbackKey = lookupModelKey(E2E_FALLBACK.provider);
    if (!fallbackKey) {
      const [ciName, localName] = keyEnvNamesFor(E2E_FALLBACK.provider);
      throw new Error(
        `onboard-via-api: no API key for the fallback provider "${E2E_FALLBACK.provider}". ` +
          `Set ${ciName} (CI) or ${localName} (local). Every agent gets the fallback model, ` +
          'so the suite does not turn red when the primary is unavailable.'
      );
    }
  }

  {
    const res = await send(opts.baseURL, 'POST', '/api/v1/onboarding/complete', {
      provider: {
        auth_method: 'api_key',
        id: providerID,
        api_key: apiKey,
        model,
      },
      admin: {
        username: opts.username ?? DEFAULT_USERNAME,
        password,
      },
    });

    // 200 = fresh onboard: seed the fallback row and every agent's fallback.
    if (res.status === 200) {
      if (seedFallback && fallbackKey) {
        const body = (parseJSON(res.text) ?? {}) as { token?: string };
        await seedAgentFallback(opts.baseURL, body.token, fallbackKey, password);
      }
      return;
    }

    // 409 = already complete on this $OMNIPUS_HOME (e.g. second test shard
    // hitting the same instance). Accept ONLY when the body confirms the known
    // sentinel — any other 409 is an unexpected error and must be surfaced.
    if (res.status === 409) {
      const body = res.text;
      // Accept the 409 only when the body contains the expected sentinel.
      // The backend returns {"error":"onboarding_already_complete",...} or similar text.
      if (
        body.includes('onboarding_already_complete') ||
        body.toLowerCase().includes('already complete') ||
        body.toLowerCase().includes('already been completed')
      ) {
        return;
      }
      throw new Error(
        `onboard-via-api: POST /api/v1/onboarding/complete returned unexpected 409: ${body}`,
      );
    }

    // 400 with the key-rejected outcome is a distinct, actionable failure from
    // any other 400 (malformed body, bad username, etc.): the configured key
    // is wrong or revoked. Surface that plainly instead of letting the raw
    // provider-rejection message read like a suite bug.
    if (res.status === 400) {
      const body = res.text;
      if (body.toLowerCase().includes('rejected')) {
        throw new Error(
          `onboard-via-api: the configured key for "${providerID}" was rejected by the provider — ` +
            `check ${keyEnvNamesFor(providerID).join('/')} is a valid, active key. ` +
            `Server said: ${body}`,
        );
      }
      throw new Error(
        `onboard-via-api: POST /api/v1/onboarding/complete returned 400: ${body}`,
      );
    }

    throw new Error(
      `onboard-via-api: POST /api/v1/onboarding/complete returned ${res.status}: ${res.text}`,
    );
  }
}

/**
 * Add the fallback provider row, then give every agent the fallback in its
 * fallback_models. token is the onboarding response's bearer token (bearer
 * callers are exempt from the CSRF double-submit); without one the calls rely
 * on dev-mode bypass, as CI's seeded config sets.
 */
async function seedAgentFallback(
  baseURL: string,
  token: string | undefined,
  fallbackKey: string,
  password: string,
): Promise<void> {
  const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
  const providerPath = `/api/v1/providers/${encodeURIComponent(E2E_FALLBACK.provider)}`;
  // Saving a provider key is a sensitive change: the server wants a re-auth consent token
  // (POST /api/v1/auth/reauth, Spec-6 FR-12.2) in X-Reauth-Token, minted from the admin password.
  const reauth = await send(baseURL, 'POST', '/api/v1/auth/reauth', { password }, headers);
  const reauthToken = (parseJSON(reauth.text) as { token?: string } | undefined)?.token;
  if (!reauth.ok || !reauthToken) {
    throw new Error(
      `onboard-via-api: POST /api/v1/auth/reauth returned ${reauth.status}: ${reauth.text}`,
    );
  }
  const put = await send(
    baseURL,
    'PUT',
    providerPath,
    { auth_method: 'api_key', api_key: fallbackKey, model: E2E_FALLBACK.model },
    { ...headers, 'X-Reauth-Token': reauthToken },
  );
  if (!put.ok) {
    throw new Error(
      `onboard-via-api: PUT ${providerPath} (fallback provider) returned ${put.status}: ${put.text}`,
    );
  }

  const list = await send(baseURL, 'GET', '/api/v1/agents', undefined, headers);
  if (!list.ok) {
    throw new Error(`onboard-via-api: GET /api/v1/agents returned ${list.status}: ${list.text}`);
  }
  const agents = parseJSON(list.text);
  if (!Array.isArray(agents) || agents.length === 0) {
    throw new Error('onboard-via-api: GET /api/v1/agents returned no agents to give the fallback');
  }
  const fallbackModels = [{ provider: E2E_FALLBACK.provider, model: E2E_FALLBACK.model }];
  for (const agent of agents) {
    const { id, revision } = agent as { id?: unknown; revision?: unknown };
    if (typeof id !== 'string' || id === '') {
      throw new Error('onboard-via-api: GET /api/v1/agents returned an agent with no id');
    }
    // An agent update must carry the revision it read (409/400 otherwise, ADR-091 era).
    if (typeof revision !== 'string' || revision === '') {
      throw new Error(`onboard-via-api: GET /api/v1/agents returned no revision for agent ${id}`);
    }
    const path = `/api/v1/agents/${encodeURIComponent(id)}`;
    const res = await send(baseURL, 'PUT', path, { revision, fallback_models: fallbackModels }, headers);
    if (!res.ok) {
      throw new Error(`onboard-via-api: PUT ${path} (fallback_models) returned ${res.status}: ${res.text}`);
    }
  }
}
