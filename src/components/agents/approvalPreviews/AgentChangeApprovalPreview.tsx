// Readable summary for `create_agent` / `update_agent` tool-approval requests
// (tool-parity r17 FR-160, scenarios 17.1-17.3 and 17.7).
//
// Registered as an ADDITIVE registry entry (registry.ts): the summary renders
// above the generic Arguments JSON dump, never instead of it, so any argument
// this summary does not explain (an unknown key, the agent's name, its soul)
// stays visible in the raw block — request content is never silently dropped
// (17.3, the fidelity guard).
//
// Secrets: the gateway already masks every env_overrides value in the
// approval frame (r17 SR-1). The card still never reads or renders a value:
// the summary shows names only, with the fixed mask and the readable text
// "value hidden" (17.7), and redactAgentChangeArgs replaces every value
// before the args reach the summary or the raw JSON dump, including an
// env_overrides container nested under an unknown argument. A malformed
// env_overrides (not an object of names) is masked as a whole.

import type { ToolApprovalPreviewContext } from './types'

/** The fixed mask the gateway's read model uses for env values (r17 FR-063). */
export const ENV_VALUE_MASK = '••••••'

const ENV_OVERRIDES = 'env_overrides'

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isEnvOverridesKey(key: string): boolean {
  return key.toLowerCase() === ENV_OVERRIDES
}

function maskEnvContainer(env: unknown): unknown {
  if (env === null) return null // an explicit null carries no value
  return isPlainObject(env) ? Object.fromEntries(Object.keys(env).map((name) => [name, ENV_VALUE_MASK])) : ENV_VALUE_MASK
}

function redactEnvDeep(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactEnvDeep)
  if (!isPlainObject(value)) return value
  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [key, isEnvOverridesKey(key) ? maskEnvContainer(item) : redactEnvDeep(item)]),
  )
}

/**
 * Returns a copy of args in which every env_overrides container (any letter
 * case, at any depth, inside objects and arrays, CK-11) has all its values
 * replaced by ENV_VALUE_MASK. Names stay; an explicit null stays null (it
 * carries no value); a container that is not an object is masked whole.
 * Every other key, value and the structure stay visible (17.3).
 */
export function redactAgentChangeArgs(args: Record<string, unknown>): Record<string, unknown> {
  return redactEnvDeep(args) as Record<string, unknown>
}

// tokenizeCliArgs mirrors pkg/agent/runner/executor_opts.go::tokenizeArgs
// (the tokenizer the run itself uses for cli_args): whitespace separates
// tokens, single and double quotes group a token (quotes stripped), and a
// backslash escapes the next character except inside single quotes.
export function tokenizeCliArgs(raw: string): string[] {
  const tokens: string[] = []
  let cur = ''
  let inToken = false
  let quote: '' | "'" | '"' = ''
  let escaped = false
  for (const ch of raw) {
    if (escaped) {
      cur += ch
      inToken = true
      escaped = false
    } else if (ch === '\\' && quote !== "'") {
      escaped = true
      inToken = true
    } else if (quote) {
      if (ch === quote) quote = ''
      else cur += ch
      inToken = true
    } else if (ch === "'" || ch === '"') {
      quote = ch
      inToken = true
    } else if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      if (inToken) {
        tokens.push(cur)
        cur = ''
        inToken = false
      }
    } else {
      cur += ch
      inToken = true
    }
  }
  if (inToken) tokens.push(cur)
  return tokens
}

/**
 * Groups cli_args into one line per flag: a token starting with "-" opens a
 * new line and the following non-flag tokens are its values
 * ("--model x --verbose" -> ["--model x", "--verbose"]). A leading value with
 * no flag before it gets its own line.
 */
export function cliArgLines(raw: string): string[] {
  const lines: string[] = []
  for (const token of tokenizeCliArgs(raw)) {
    if (token.startsWith('-') || lines.length === 0) lines.push(token)
    else lines[lines.length - 1] = `${lines[lines.length - 1]} ${token}`
  }
  return lines
}

const labelClass = 'text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mb-[var(--space-1)]'
const blockClass =
  'font-mono text-[length:var(--type-utility-xs-size)] bg-[var(--color-surface-2)] rounded-lg px-[var(--space-2-5)] py-[var(--space-2)] text-[var(--color-secondary)]'

export function AgentChangeApprovalPreview({ args }: ToolApprovalPreviewContext) {
  const env = args[ENV_OVERRIDES]
  const envNames = isPlainObject(env) ? Object.keys(env) : []
  const flags = typeof args.cli_args === 'string' ? cliArgLines(args.cli_args) : []
  if (envNames.length === 0 && flags.length === 0) return null

  return (
    <div className="space-y-[var(--space-2-5)]">
      {envNames.length > 0 && (
        <div>
          <p className={labelClass}>Environment variables</p>
          <dl className={`${blockClass} space-y-[var(--space-1)]`}>
            {envNames.map((name) => (
              <div key={name} className="flex flex-wrap items-baseline gap-x-[var(--space-2)]">
                <dt className="break-all">{name}</dt>
                <dd className="text-[var(--color-muted)]">
                  <span aria-hidden="true">{ENV_VALUE_MASK} </span>value hidden
                </dd>
              </div>
            ))}
          </dl>
        </div>
      )}
      {flags.length > 0 && (
        <div>
          <p className={labelClass}>Extra command-line flags</p>
          <ol className={`${blockClass} space-y-[var(--space-1)]`}>
            {flags.map((line, index) => (
              <li key={`${index}-${line}`} className="break-all">
                {line}
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  )
}
