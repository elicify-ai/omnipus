import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * e2e-model.ts — reader for the ONE source of truth for the real-LLM models
 * used by this suite: tests/e2e/e2e-model.json, a primary and a fallback pair:
 *
 *   { "primary":  { "provider": ..., "model": ..., "api_key_ref": ... },
 *     "fallback": { "provider": ..., "model": ..., "api_key_ref": ... } }
 *
 * The JSON file is read and validated on every import, with the path resolved
 * relative to THIS file (never cwd); a missing / unparseable / malformed
 * document throws at fixture load — fail closed, never a silent default. CI
 * (.github/workflows/pr.yml, sandbox-uat.yml and the Fly runner's
 * deploy/ci-worker/runci.sh) reads the same file through scripts/e2e-model.sh.
 *
 * When the job-start check (scripts/e2e-model.sh preflight) finds the primary
 * unavailable, the workflow runs the whole job on the fallback and exports
 * OMNIPUS_E2E_PRIMARY_PROVIDER / OMNIPUS_E2E_PRIMARY_MODEL; those replace the
 * primary here, and a difference from the file is logged.
 *
 * Which pair to use:
 *   - a gateway that talks to a REAL model: E2E_PRIMARY, and agents created
 *     with e2eAgentModelFields('live') (fixtures/e2e-agent-model.ts), which
 *     adds the fallback to the agent's fallback_models;
 *   - a gateway pointed at a local MOCK provider: E2E_FALLBACK. The mocks
 *     speak the OpenAI-compatible protocol, which the fallback provider uses;
 *     the primary may speak another protocol.
 *
 * Centralize here. Nowhere else in tests/e2e/** may a model or provider id
 * appear as a literal — scripts/check-no-hardcoded-e2e-model.sh enforces it,
 * in code and in comments.
 */

export interface E2EModelSide {
  provider: string
  model: string
  apiKeyRef: string
}

export interface E2EModelPair {
  primary: E2EModelSide
  fallback: E2EModelSide
}

const HERE = dirname(fileURLToPath(import.meta.url))
const MODEL_JSON_PATH = resolve(HERE, '../e2e-model.json')
const FILE_LABEL = 'tests/e2e/e2e-model.json'

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function readSide(doc: Record<string, unknown>, name: 'primary' | 'fallback'): E2EModelSide {
  const raw = doc[name]
  if (!isRecord(raw)) {
    throw new Error(`${FILE_LABEL}: .${name} object is missing`)
  }
  const field = (key: 'provider' | 'model' | 'api_key_ref'): string => {
    const v = raw[key]
    if (typeof v !== 'string' || v.trim() === '') {
      throw new Error(`${FILE_LABEL}: .${name}.${key} is missing, empty or whitespace-only`)
    }
    return v.trim()
  }
  return { provider: field('provider'), model: field('model'), apiKeyRef: field('api_key_ref') }
}

/** Parse a raw document. Does not touch the filesystem or the network. */
export function parseE2EModel(raw: string): E2EModelPair {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch (err) {
    throw new Error(`${FILE_LABEL} is not valid JSON (${(err as Error).message})`, { cause: err })
  }
  if (!isRecord(parsed)) {
    throw new Error(`${FILE_LABEL} must be a JSON object with .primary and .fallback`)
  }
  if (!('primary' in parsed) && 'model' in parsed) {
    throw new Error(
      `${FILE_LABEL} is the old shape {"model": ...}; it must carry .primary and .fallback, ` +
        'each with provider, model and api_key_ref',
    )
  }
  const primary = readSide(parsed, 'primary')
  const fallback = readSide(parsed, 'fallback')
  if (primary.provider === fallback.provider && primary.model === fallback.model) {
    throw new Error(
      `${FILE_LABEL}: primary and fallback are the same provider/model ` +
        `(${primary.provider}/${primary.model}); the fallback must differ`,
    )
  }
  return { primary, fallback }
}

const ENV_PROVIDER = 'OMNIPUS_E2E_PRIMARY_PROVIDER'
const ENV_MODEL = 'OMNIPUS_E2E_PRIMARY_MODEL'

/**
 * Apply OMNIPUS_E2E_PRIMARY_PROVIDER and OMNIPUS_E2E_PRIMARY_MODEL (set by the
 * job-start check when the primary is unavailable). Both set: they replace
 * primary.provider and primary.model (trimmed), logging each field that
 * differs from the file. Exactly one set: throws, naming both. Neither set:
 * returns the pair unchanged without logging.
 */
export function applyPrimaryEnvOverride(
  pair: E2EModelPair,
  env: NodeJS.ProcessEnv,
  log: (line: string) => void = (line) => console.error(line),
): E2EModelPair {
  const provider = env[ENV_PROVIDER]?.trim() ?? ''
  const model = env[ENV_MODEL]?.trim() ?? ''
  if (provider === '' && model === '') {
    return pair
  }
  if (provider === '' || model === '') {
    throw new Error(
      `${ENV_PROVIDER} and ${ENV_MODEL} must be set together (got ` +
        `${ENV_PROVIDER}=${provider === '' ? '<unset>' : `"${provider}"`}, ` +
        `${ENV_MODEL}=${model === '' ? '<unset>' : `"${model}"`})`,
    )
  }
  if (provider !== pair.primary.provider) {
    log(
      `${ENV_PROVIDER} value "${provider}" differs from ${FILE_LABEL} value ` +
        `"${pair.primary.provider}"; the environment variable won.`,
    )
  }
  if (model !== pair.primary.model) {
    log(
      `${ENV_MODEL} value "${model}" differs from ${FILE_LABEL} value ` +
        `"${pair.primary.model}"; the environment variable won.`,
    )
  }
  // A job moved onto the fallback provider reads that provider's credential.
  const apiKeyRef =
    provider !== pair.primary.provider && provider === pair.fallback.provider
      ? pair.fallback.apiKeyRef
      : pair.primary.apiKeyRef
  return { primary: { provider, model, apiKeyRef }, fallback: pair.fallback }
}

function loadCentralE2EModel(): E2EModelPair {
  let raw: string
  try {
    raw = readFileSync(MODEL_JSON_PATH, 'utf8')
  } catch (err) {
    throw new Error(
      `${FILE_LABEL} is missing or unreadable (${(err as Error).message}). ` +
        'It is the single source of truth for the e2e models — the whole real-LLM suite ' +
        'fails closed without it.',
      { cause: err },
    )
  }
  return applyPrimaryEnvOverride(parseE2EModel(raw), process.env)
}

const CENTRAL = loadCentralE2EModel()

/** The model real-LLM gateways and agents use (after the job-start override). */
export const E2E_PRIMARY: E2EModelSide = Object.freeze({ ...CENTRAL.primary })

/** The fallback model; also the plumbing pair for mock-provider gateways. */
export const E2E_FALLBACK: E2EModelSide = Object.freeze({ ...CENTRAL.fallback })

/**
 * True when the job runs on the fallback (the job-start check moved the
 * primary onto the fallback pair). Agents then get no fallback_models: they
 * would only name their own model again.
 */
export const E2E_PRIMARY_IS_FALLBACK =
  E2E_PRIMARY.provider === E2E_FALLBACK.provider && E2E_PRIMARY.model === E2E_FALLBACK.model

/**
 * @deprecated Alias of E2E_PRIMARY.model, kept for an existing mock-provider
 * unit test that only needs some model id. New code picks E2E_PRIMARY (live)
 * or E2E_FALLBACK (mock) explicitly, or e2eAgentModelFields().
 */
export const E2E_MODEL = E2E_PRIMARY.model
