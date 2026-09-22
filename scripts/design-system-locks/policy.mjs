import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { validateTokenSources } from '../design-system/tokens.mjs'

export function createPolicy(sources) {
  const { tokens, resolved } = validateTokenSources(sources)
  return {
    tokenCssNames: tokens.map((token) => token.css).sort(),
    resolvedTokens: Object.fromEntries(Object.entries(resolved).sort(([a], [b]) => a.localeCompare(b))),
    resolvedCssTokens: Object.fromEntries(tokens.map((token) => [token.css, resolved[token.id]])
      .sort(([a], [b]) => a.localeCompare(b))),
  }
}

export function loadPolicy(root = fileURLToPath(new URL('../..', import.meta.url))) {
  const sources = ['colors.json', 'foundations.json'].map((name) =>
    JSON.parse(readFileSync(resolve(root, 'design-system/tokens', name), 'utf8')))
  return createPolicy(sources)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.stdout.write(`${JSON.stringify(loadPolicy(), null, 2)}\n`)
}
