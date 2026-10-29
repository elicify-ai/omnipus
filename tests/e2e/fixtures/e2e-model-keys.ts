import { E2E_FALLBACK, E2E_PRIMARY, E2E_PRIMARY_IS_FALLBACK } from './e2e-model.js'

/**
 * e2e-model-keys.ts — which environment variables carry the API key for a
 * provider of the central e2e pair, and the one "are the live-model keys
 * present?" check the real-LLM specs share.
 *
 * Rule (mirrors scripts/e2e-model.sh preflight): for the provider's credential
 * name REF (the api_key_ref in tests/e2e/e2e-model.json), read REF_CI (CI
 * secret), then REF (local runs). First non-empty wins. For the MiniMax
 * primary that is MINIMAX_API_KEY_CI then MINIMAX_API_KEY; for the OpenRouter
 * fallback OPENROUTER_API_KEY_CI then OPENROUTER_API_KEY.
 */

function credentialRefFor(provider: string): string {
  if (provider === E2E_PRIMARY.provider) return E2E_PRIMARY.apiKeyRef
  if (provider === E2E_FALLBACK.provider) return E2E_FALLBACK.apiKeyRef
  // A provider outside the central pair (an explicit opts.providerID):
  // the catalog's convention, <PROVIDER>_API_KEY.
  return `${provider.toUpperCase().replace(/[^A-Z0-9]+/g, '_')}_API_KEY`
}

/** The env var names for provider's key, in lookup order. */
export function keyEnvNamesFor(provider: string): [string, string] {
  const ref = credentialRefFor(provider)
  return [`${ref}_CI`, ref]
}

/** The first non-empty key for provider, or undefined. Never logged. */
export function lookupModelKey(provider: string, env: NodeJS.ProcessEnv = process.env): string | undefined {
  for (const name of keyEnvNamesFor(provider)) {
    const v = env[name]?.trim()
    if (v) return v
  }
  return undefined
}

/**
 * Throw a BLOCKED error unless the keys for the live primary AND the fallback
 * are present. context names the spec or fixture for the message.
 */
export function requireLiveModelKeys(context: string, env: NodeJS.ProcessEnv = process.env): void {
  const sides = E2E_PRIMARY_IS_FALLBACK ? [E2E_PRIMARY] : [E2E_PRIMARY, E2E_FALLBACK]
  const missing = sides
    .filter((side) => lookupModelKey(side.provider, env) === undefined)
    .map((side) => keyEnvNamesFor(side.provider).join(' or '))
  if (missing.length > 0) {
    throw new Error(
      `BLOCKED: ${context} needs a live model key — set ${missing.join(', and ')} ` +
        '(the primary key and the fallback key; see tests/e2e/README.md).',
    )
  }
}
