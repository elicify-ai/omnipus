import { E2E_FALLBACK, E2E_PRIMARY, E2E_PRIMARY_IS_FALLBACK } from './e2e-model.js'

/**
 * e2e-agent-model.ts — the provider/model fields every spec and fixture puts
 * on an agent it creates (POST /api/v1/agents and friends).
 *
 * The product falls back per agent only (no install-wide fallback, issue
 * #1340): an agent without its own fallback_models fails its turn when the
 * primary model is unavailable. So a live-model agent always carries the
 * central fallback.
 *
 *   live → E2E_PRIMARY's provider/model plus fallback_models: [E2E_FALLBACK]
 *          (no fallback_models when the job already runs on the fallback);
 *   mock → E2E_FALLBACK's provider/model and NO fallback_models: mock
 *          gateways speak the OpenAI-compatible protocol, so they are never
 *          pointed at the primary provider.
 *
 * Values come from tests/e2e/e2e-model.json through e2e-model.ts — never
 * literals at the call site. The returned object carries no API key.
 */

export type E2EAgentModelKind = 'live' | 'mock'

export interface E2EAgentModelFields {
  provider: string
  model: string
  fallback_models?: { provider: string; model: string }[]
}

export function e2eAgentModelFields(kind: E2EAgentModelKind): E2EAgentModelFields {
  switch (kind) {
    case 'live': {
      const fields: E2EAgentModelFields = { provider: E2E_PRIMARY.provider, model: E2E_PRIMARY.model }
      if (!E2E_PRIMARY_IS_FALLBACK) {
        fields.fallback_models = [{ provider: E2E_FALLBACK.provider, model: E2E_FALLBACK.model }]
      }
      return fields
    }
    case 'mock':
      return { provider: E2E_FALLBACK.provider, model: E2E_FALLBACK.model }
    default:
      throw new Error(`e2eAgentModelFields: kind must be 'live' or 'mock', got ${JSON.stringify(kind)}`)
  }
}
