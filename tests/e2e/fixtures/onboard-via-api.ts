import { type APIRequestContext, request } from '@playwright/test';

const DEFAULT_PROVIDER_ID = 'openrouter';
// z-ai/glm-5.2 — the project's standard model for all e2e tests (mirrored in the
// Fly CI runner's runci.sh e2e config). The previous google/gemini-2.5-flash
// pick degraded on OpenRouter (empty responses + "http2: response body closed"
// stream drops → turns never completed → Bug-3/Bug-5/T24/media all failed), so
// it was swapped for glm-5.2, a live, reliable, tool-capable model. Determinism
// for "exactly N tool calls" subagent assertions is enforced via the
// temperature=0 + seed=42 plumbing the suite already passes through to
// OpenRouter, not by the model alone.
//
// NOTE: the nightly evals (.github/workflows/evals-nightly.yml +
// evals/cmd/eval-runner/main.go) intentionally stay on z-ai/glm-5-turbo (a LIVE
// model whose eval baselines depend on it) — the E2E-vs-evals difference is
// deliberate.
const DEFAULT_MODEL = 'z-ai/glm-5.2';
const DEFAULT_USERNAME = 'admin';
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
 *   - The backend now PROBES the key against the real provider before
 *     completing onboarding (see the "Provider API-key validation" block in
 *     rest_onboarding.go) and returns 400 when the provider confirms the key
 *     is wrong (`providers.OutcomeInvalidKey`) — no_credit / unreachable /
 *     restricted still proceed with a warning. Onboarding is NO LONGER a pure
 *     "store whatever was typed" write; a fake key is now rejected, not
 *     silently accepted.
 *
 * The API key is sourced from OPENROUTER_API_KEY_CI (or falls back to
 * OPENROUTER_API_KEY for local runs). If NEITHER is set, this throws
 * immediately, before making any request — a placeholder key would either get
 * a real 400 from the provider probe (a confusing failure deep inside
 * onboarding) or, worse, if OpenRouter ever starts probe-accepting garbage
 * keys, would silently seed a broken install that fails every subsequent spec
 * one at a time with no indication why. Failing fast, at fixture-build time,
 * with a message that names the two env vars to set, is strictly better than
 * either.
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

  const apiKey = opts.apiKey ?? process.env.OPENROUTER_API_KEY_CI ?? process.env.OPENROUTER_API_KEY;
  if (!apiKey) {
    // No placeholder fallback (see the doc comment above): onboarding now
    // validates the key for real, so a fake key cannot silently seed a
    // broken install — but running the whole suite against one is still a
    // waste of a CI shard's worth of time before the same 400 surfaces at
    // spec #1 instead of here. Fail immediately with the fix.
    throw new Error(
      'onboard-via-api: no OpenRouter API key available. Set OPENROUTER_API_KEY_CI ' +
        '(CI) or OPENROUTER_API_KEY (local), or pass { apiKey } explicitly. Onboarding ' +
        'now probes the key against the real provider before completing (see ' +
        'pkg/gateway/rest_onboarding.go), so the suite cannot run against a placeholder.'
    );
  }

  const ctx: APIRequestContext = await request.newContext({ baseURL: opts.baseURL });
  try {
    const res = await ctx.post('/api/v1/onboarding/complete', {
      data: {
        provider: {
          auth_method: 'api_key',
          id: opts.providerID ?? DEFAULT_PROVIDER_ID,
          api_key: apiKey,
          model: opts.model ?? DEFAULT_MODEL,
        },
        admin: {
          username: opts.username ?? DEFAULT_USERNAME,
          password,
        },
      },
    });

    // 200 = fresh onboard.
    if (res.status() === 200) {
      return;
    }

    // 409 = already complete on this $OMNIPUS_HOME (e.g. second test shard
    // hitting the same instance). Accept ONLY when the body confirms the known
    // sentinel — any other 409 is an unexpected error and must be surfaced.
    // Parse the body to distinguish expected "already complete" from
    // an unexpected 409 (e.g., partial state, schema mismatch).
    if (res.status() === 409) {
      let body: string;
      try {
        body = await res.text();
      } catch {
        throw new Error('onboard-via-api: 409 response body could not be read');
      }
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
    // any other 400 (malformed body, bad username, etc.): the key that
    // OPENROUTER_API_KEY_CI/OPENROUTER_API_KEY points at is wrong or revoked.
    // Surface that plainly instead of letting the raw provider-rejection
    // message (from providers.BuildMessage) read like a suite bug.
    if (res.status() === 400) {
      const body = await res.text();
      if (body.toLowerCase().includes('rejected')) {
        throw new Error(
          `onboard-via-api: the configured OpenRouter key was rejected by the provider — ` +
            `check OPENROUTER_API_KEY_CI/OPENROUTER_API_KEY is a valid, active key. ` +
            `Server said: ${body}`,
        );
      }
      throw new Error(
        `onboard-via-api: POST /api/v1/onboarding/complete returned 400: ${body}`,
      );
    }

    const body = await res.text();
    throw new Error(
      `onboard-via-api: POST /api/v1/onboarding/complete returned ${res.status()}: ${body}`,
    );
  } finally {
    await ctx.dispose();
  }
}
