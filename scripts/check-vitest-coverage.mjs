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

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter((f) => f.startsWith('src/') && /\.(test|spec)\.[cm]?[jt]sx?$/.test(f))

if (files.length === 0) {
  console.error('FAIL: found no SPA test files at all — the glob is wrong, or tests vanished.')
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
