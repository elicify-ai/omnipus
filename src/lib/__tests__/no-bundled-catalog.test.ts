// no-bundled-catalog.test.ts — ADR-068 FR-037 / SC-010 (task T068-05).
//
// The SPA reads the providers catalog from GET /api/v1/providers/catalog,
// never from a bundled TS emission. This guard fails the build if the
// bundled file, its importers, or the retired `model-capabilities` /
// `refresh-models` client wrappers ever come back.
//
// SC-010: `grep -rn "generated/providerCatalog" src pkg` is empty and
// `ls src/lib/generated/providerCatalog.ts` fails.

import { describe, it, expect } from 'vitest'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'

const REPO_ROOT = resolve(__dirname, '..', '..', '..')
const THIS_FILE = resolve(__filename)

const SOURCE_EXT = /\.(ts|tsx|go|js|mjs|cjs)$/

function walk(dir: string, out: string[]): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const full = join(dir, name)
    const st = statSync(full)
    if (st.isDirectory()) walk(full, out)
    else if (SOURCE_EXT.test(name)) out.push(full)
  }
  return out
}

function filesReferencing(dirs: string[], needle: string | RegExp): string[] {
  const hits: string[] = []
  for (const d of dirs) {
    const root = join(REPO_ROOT, d)
    if (!existsSync(root)) continue
    for (const f of walk(root, [])) {
      if (resolve(f) === THIS_FILE) continue
      const text = readFileSync(f, 'utf8')
      const matched = typeof needle === 'string' ? text.includes(needle) : needle.test(text)
      if (matched) hits.push(f.slice(REPO_ROOT.length + 1))
    }
  }
  return hits.sort()
}

describe('SC-010 — no bundled provider catalog (FR-037)', () => {
  it('src/lib/generated/providerCatalog.ts does not exist', () => {
    expect(existsSync(join(REPO_ROOT, 'src/lib/generated/providerCatalog.ts'))).toBe(false)
  })

  it('nothing under src/ or pkg/ references generated/providerCatalog', () => {
    expect(filesReferencing(['src', 'pkg'], 'generated/providerCatalog')).toEqual([])
  })

  it('no SPA file references PROVIDER_CATALOG', () => {
    expect(filesReferencing(['src'], /\bPROVIDER_CATALOG\b/)).toEqual([])
  })

  it('no SPA file references the retired model-capabilities / refresh-models wrappers', () => {
    expect(filesReferencing(['src'], /\b(fetchModelCapabilities|refreshProviderModels)\b/)).toEqual([])
    expect(filesReferencing(['src'], /providers\/(model-capabilities|\$\{[^}]*\}\/refresh-models)/)).toEqual([])
  })

  it('the Go TS emitter and its embed-vs-TS test are gone', () => {
    expect(existsSync(join(REPO_ROOT, 'pkg/providers/catalog/gen'))).toBe(false)
    expect(filesReferencing(['pkg'], 'TestCatalog_EmbedMatchesGeneratedTS')).toEqual([])
  })
})
