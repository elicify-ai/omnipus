import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * e2e-model.ts — reader for the ONE source of truth for the real-LLM model
 * id used by this suite: tests/e2e/e2e-model.json ({"model":"<provider/slug>"}).
 *
 * The JSON file is the PRIMARY source. It is read and validated on every
 * import, with the path resolved relative to THIS file (never cwd), and a
 * missing / unparseable / empty .model throws at fixture load — fail closed,
 * never a silent fallback. CI (.github/workflows/pr.yml and the Fly runner's
 * deploy/ci-worker/runci.sh) reads the same JSON and exports OMNIPUS_E2E_MODEL
 * from it; when that env var is set and non-empty it is an explicit override
 * of the file's value (same file, so they cannot disagree in CI).
 *
 * Centralize here; import E2E_MODEL everywhere else. Nowhere else in
 * tests/e2e/** may a model id appear as a literal — scripts/check-no-hardcoded-e2e-model.sh
 * enforces it, in code and in comments.
 */
const HERE = dirname(fileURLToPath(import.meta.url))
const MODEL_JSON_PATH = resolve(HERE, '../e2e-model.json')

function loadCentralE2EModel(): string {
  let raw: string
  try {
    raw = readFileSync(MODEL_JSON_PATH, 'utf8')
  } catch (err) {
    throw new Error(
      `tests/e2e/e2e-model.json is missing or unreadable (${(err as Error).message}). ` +
        'It is the single source of truth for the e2e model — the whole real-LLM suite ' +
        'fails closed without it. Restore it with content {"model":"<provider/slug>"}.',
      { cause: err },
    )
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch (err) {
    throw new Error(
      `tests/e2e/e2e-model.json is not valid JSON (${(err as Error).message}). ` +
        'Expected exactly {"model":"<provider/slug>"}.',
      { cause: err },
    )
  }

  const model =
    typeof parsed === 'object' && parsed !== null ? (parsed as { model?: unknown }).model : undefined
  if (typeof model !== 'string' || model.trim() === '') {
    throw new Error(
      'tests/e2e/e2e-model.json must carry a non-empty string ".model" ' +
        `(e.g. {"model":"<provider/slug>"}); got ${model === undefined ? 'no .model key' : typeof model}.`,
    )
  }
  return model
}

const FILE_MODEL = loadCentralE2EModel()

/** CI's explicit override (set from the same tests/e2e/e2e-model.json). */
const ENV_OVERRIDE = process.env.OMNIPUS_E2E_MODEL?.trim() ?? ''
if (ENV_OVERRIDE !== '' && ENV_OVERRIDE !== FILE_MODEL) console.error(`OMNIPUS_E2E_MODEL value "${ENV_OVERRIDE}" differs from tests/e2e/e2e-model.json value "${FILE_MODEL}"; the environment variable won.`)

export const E2E_MODEL = ENV_OVERRIDE !== '' ? ENV_OVERRIDE : FILE_MODEL
