#!/usr/bin/env node
// Fails if any SPA test file is not matched by at least one vitest matrix
// group in .github/workflows/pr.yml.
//
// Why this exists: the vitest job shards the suite across a HARDCODED matrix of
// path patterns. Any test file outside every pattern is silently never run, and
// the job still reports green — the pipeline says "tests passed" while whole
// directories were never executed. When this guard was written, 116 of 422 test
// files (27%) never ran in CI, including all 57 under src/components/workspaces/
// (the v0.3 flagship) and all 11 under src/components/browser/. Two patterns
// pointed at directories that had been DELETED entirely.
//
// A hand-maintained allowlist drifts the moment someone adds a directory. This
// makes coverage an enforced property instead of a convention: add a new test
// directory without adding it to the matrix and CI fails with the exact list.
//
// The set of files this guard considers "a vitest test file" is READ FROM
// vite.config.ts's own `include`, never hardcoded here. It used to be
// hardcoded as `f.startsWith('src/')`, and that reproduced the very defect
// class the guard exists to catch: vite.config.ts's include is
// ['src/**/*.test.{ts,tsx}', 'tests/**/*.test.{ts,tsx}'], every vitest matrix
// pattern also begins with 'src/', so tests/e2e/fixtures/selectors.test.ts —
// a real vitest test file with 3 real tests — was executed by NO shard on any
// PR while this guard printed "OK: all 427 SPA test files are covered". A
// second hardcoded copy of the test-file definition is exactly the drift this
// script was written to make impossible.
import { readFileSync } from 'node:fs'
import { execSync } from 'node:child_process'

const wf = readFileSync('.github/workflows/pr.yml', 'utf8')

// Isolate the vitest job block, so patterns from unrelated jobs can't count.
const jobStart = wf.indexOf('\n  vitest:')
if (jobStart === -1) {
  console.error('FAIL: no `vitest:` job found in .github/workflows/pr.yml.')
  console.error('If the job was renamed or removed, update this guard deliberately.')
  process.exit(1)
}
const rest = wf.slice(jobStart + 1)
const nextJob = rest.search(/\n {2}[a-zA-Z0-9_-]+:\n/)
const block = nextJob === -1 ? rest : rest.slice(0, nextJob)

const patterns = [...block.matchAll(/^\s*-?\s*patterns:\s*"([^"]+)"/gm)]
  .flatMap((m) => m[1].trim().split(/\s+/))
  .filter(Boolean)

if (patterns.length === 0) {
  console.error('FAIL: vitest job declares no `patterns:` — cannot verify coverage.')
  process.exit(1)
}

// --- What counts as a vitest test file: read vite.config.ts's own `include` ---
//
// Single source of truth. If vite's include grows a directory, this guard
// starts policing it on the same commit, with no second list to remember.
const viteCfg = readFileSync('vite.config.ts', 'utf8')
const includeMatch = viteCfg.match(/^\s*include:\s*\[([^\]]*)\]/m)
if (!includeMatch) {
  console.error("FAIL: could not find vitest's `include:` array in vite.config.ts.")
  console.error('This guard mirrors that list; if it moved or was renamed, update this')
  console.error('guard deliberately rather than letting it fall back to a guess.')
  process.exit(1)
}
const includeGlobs = [...includeMatch[1].matchAll(/['"]([^'"]+)['"]/g)].map((m) => m[1])
if (includeGlobs.length === 0) {
  console.error('FAIL: vitest `include:` in vite.config.ts parsed to zero globs.')
  process.exit(1)
}

// Minimal glob → RegExp for the shapes vitest's include actually uses:
// `**` (any path segments), `*` (any run of non-slash chars), and `{a,b}`
// alternation. Anything else is escaped literally.
function globToRegExp(glob) {
  let out = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        // `**/` swallows the slash so it can also match zero segments.
        if (glob[i + 2] === '/') {
          out += '(?:[^/]+/)*'
          i += 2
        } else {
          out += '.*'
          i += 1
        }
      } else {
        out += '[^/]*'
      }
    } else if (c === '{') {
      const close = glob.indexOf('}', i)
      if (close === -1) {
        out += '\\{'
      } else {
        const alts = glob.slice(i + 1, close).split(',')
        out += `(?:${alts.map((a) => a.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`
        i = close
      }
    } else {
      out += c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    }
  }
  return new RegExp(`^${out}$`)
}

const includeRes = includeGlobs.map(globToRegExp)

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter((f) => includeRes.some((re) => re.test(f)))

if (files.length === 0) {
  console.error(
    `FAIL: vite.config.ts's include (${includeGlobs.join(', ')}) matched no tracked file — ` +
      'the glob translation is wrong, or the tests vanished.',
  )
  process.exit(1)
}

// Mirror vitest's own filter semantics: a plain substring match on the path.
const uncovered = files.filter((f) => !patterns.some((p) => f.includes(p)))

// A pattern matching nothing is dead weight that hides intent (it looks like
// coverage). Flag it, but do not fail on it alone.
const dead = patterns.filter((p) => !files.some((f) => f.includes(p)))

if (dead.length > 0) {
  console.warn(`WARN: ${dead.length} vitest pattern(s) match no test file:`)
  for (const p of dead) console.warn(`  ${p}`)
}

if (uncovered.length > 0) {
  console.error(
    `\nFAIL: ${uncovered.length} of ${files.length} SPA test files are not covered ` +
      `by any vitest matrix group and would NEVER run in CI:\n`,
  )
  const byDir = new Map()
  for (const f of uncovered) {
    const d = f.split('/').slice(0, 3).join('/')
    byDir.set(d, (byDir.get(d) ?? 0) + 1)
  }
  for (const [d, c] of [...byDir].sort((a, b) => b[1] - a[1])) {
    console.error(`  ${String(c).padStart(4)}  ${d}/`)
  }
  console.error(
    '\nAdd these paths to a `patterns:` group in the vitest matrix in ' +
      '.github/workflows/pr.yml so they actually execute.',
  )
  process.exit(1)
}

console.log(`OK: all ${files.length} SPA test files are covered by ${patterns.length} vitest patterns.`)
