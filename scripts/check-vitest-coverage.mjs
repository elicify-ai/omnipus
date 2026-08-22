#!/usr/bin/env node
// Fails the build if the vitest CI matrix does not actually run every test file.
//
// ADR-067 FR-085 / FR-086 (spec tests 97 and 98).
//
// WHY THIS EXISTS
// ---------------
// The `vitest` job in .github/workflows/pr.yml shards the suite across a
// HARDCODED matrix of path patterns. Each group runs `npx vitest run $PATTERNS`,
// where every space-separated token is a plain substring filter on the file
// path. A test file outside every pattern is therefore never executed — and the
// job still reports green. The pipeline says "tests passed" while whole
// directories were never run.
//
// That is not hypothetical. When the first version of this guard was written,
// 116 of 422 test files (27%) never ran in CI, including all 57 under
// src/components/workspaces/ and all 11 under src/components/browser/, and two
// patterns pointed at directories that had been DELETED entirely. A deleted
// directory in the matrix is worse than a missing one: it reads as coverage.
//
// A hand-maintained allowlist drifts the moment someone adds a directory. This
// makes coverage an enforced property instead of a convention.
//
// WHAT IT ENFORCES
// ----------------
//   FR-085  Every test file vitest would collect matches EXACTLY ONE matrix
//           group. Zero matches => the file never runs => FAIL, naming the file.
//           Two or more matches => the file runs twice in different shards,
//           burning CI time and making the shard split a lie => FAIL, naming it.
//
//   FR-086  Every configured pattern resolves to an existing directory. A stale
//           pattern cannot masquerade as coverage => FAIL, naming the pattern.
//
// DESIGN NOTE — the file list comes from vitest's own config, not from a
// hardcoded 'src/' walk. An earlier version of this guard hardcoded src/, which
// silently exempted the whole `tests/**` half of vitest's `include`. Deriving
// the enumeration from the same globs vitest itself collects is the only way
// the guard cannot drift away from the thing it is guarding. If those globs
// cannot be parsed we FAIL rather than fall back to a narrower scan — a guard
// that quietly checks less than it claims is the exact failure mode above.
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, resolve, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const WORKFLOW = '.github/workflows/pr.yml'
const VITE_CONFIG = 'vite.config.ts'
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git', 'coverage', '.turbo', 'build'])

/** Convert a simple glob (**, *, {a,b}) to an anchored RegExp over a POSIX path. */
function globToRegExp(glob) {
  let out = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        // '**/' spans zero or more directories; a bare '**' spans anything.
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
      if (close === -1) throw new Error(`unbalanced { in glob: ${glob}`)
      const alts = glob.slice(i + 1, close).split(',')
      out += `(?:${alts.map((a) => a.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`
      i = close
    } else if (c === '?') {
      out += '[^/]'
    } else {
      out += c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    }
  }
  return new RegExp(`^${out}$`)
}

/** The longest leading path segment run of a glob that contains no wildcard. */
function globRoot(glob) {
  const segs = glob.split('/')
  const stat = []
  for (const s of segs) {
    if (/[*?{]/.test(s)) break
    stat.push(s)
  }
  return stat.join('/') || '.'
}

function walk(dir, acc) {
  let entries
  try {
    entries = readdirSync(join(REPO_ROOT, dir), { withFileTypes: true })
  } catch {
    return acc
  }
  for (const e of entries) {
    if (e.isDirectory()) {
      if (SKIP_DIRS.has(e.name)) continue
      walk(`${dir}/${e.name}`, acc)
    } else if (e.isFile()) {
      acc.push(`${dir}/${e.name}`)
    }
  }
  return acc
}

function fail(msg) {
  console.error(`FAIL: ${msg}`)
  process.exit(1)
}

// ---------------------------------------------------------------------------
// 1. What does vitest actually collect?
// ---------------------------------------------------------------------------
function readVitestIncludeGlobs() {
  const cfgPath = join(REPO_ROOT, VITE_CONFIG)
  if (!existsSync(cfgPath)) {
    fail(`${VITE_CONFIG} not found — cannot determine which files vitest collects.`)
  }
  const cfg = readFileSync(cfgPath, 'utf8')
  // Match the `include:` array inside the `test:` block. A -1 here must not
  // become slice(-1) — that silently yields the last character and turns a
  // "config moved" bug into a confusing downstream parse error.
  const testAt = cfg.search(/(^|\{)\s*test:\s*\{/m)
  const testBlock = testAt === -1 ? '' : cfg.slice(testAt)
  const m = testBlock.match(/^\s*include:\s*\[([^\]]*)\]/m)
  if (!m) {
    fail(
      `could not find a \`test.include: [...]\` array in ${VITE_CONFIG}.\n` +
        '       This guard derives the file list from vitest\'s own config on purpose.\n' +
        '       If the config moved or changed shape, update this guard deliberately —\n' +
        '       do NOT let it fall back to a narrower scan.',
    )
  }
  const globs = [...m[1].matchAll(/['"`]([^'"`]+)['"`]/g)].map((x) => x[1])
  if (globs.length === 0) fail(`\`test.include\` in ${VITE_CONFIG} is empty.`)
  return globs
}

// ---------------------------------------------------------------------------
// 2. What does the CI matrix claim to run?
// ---------------------------------------------------------------------------
function readMatrixPatterns() {
  const wfPath = join(REPO_ROOT, WORKFLOW)
  if (!existsSync(wfPath)) fail(`${WORKFLOW} not found.`)
  const wf = readFileSync(wfPath, 'utf8')

  // Isolate the vitest job block so `patterns:` keys from unrelated jobs
  // cannot be counted as coverage.
  const jobStart = wf.indexOf('\n  vitest:')
  if (jobStart === -1) {
    fail(
      `no \`vitest:\` job found in ${WORKFLOW}.\n` +
        '       If the job was renamed or removed, update this guard deliberately.',
    )
  }
  const rest = wf.slice(jobStart + 1)
  const nextJob = rest.search(/\n {2}[a-zA-Z0-9_-]+:\n/)
  const block = nextJob === -1 ? rest : rest.slice(0, nextJob)

  const groups = []
  const lines = block.split('\n')
  let currentGroup = null
  for (const line of lines) {
    const g = line.match(/^\s*-?\s*group:\s*"?([^"\s]+)"?\s*$/)
    if (g) currentGroup = g[1]
    const p = line.match(/^\s*-?\s*patterns:\s*["']([^"']+)["']/)
    if (p) {
      for (const tok of p[1].trim().split(/\s+/).filter(Boolean)) {
        groups.push({ group: currentGroup ?? '(unnamed)', pattern: tok })
      }
    }
  }
  if (groups.length === 0) {
    fail(`the \`vitest:\` job in ${WORKFLOW} declares no \`patterns:\` — cannot verify coverage.`)
  }
  return groups
}

// ---------------------------------------------------------------------------
// Self-test: guard the guard. The matching logic below is the whole value of
// this script; if it silently stops matching, everything reports clean.
// ---------------------------------------------------------------------------
function selfTest() {
  const checks = []
  const t = (name, actual, expected) =>
    checks.push({ name, ok: actual === expected, actual, expected })

  const re = globToRegExp('src/**/*.test.{ts,tsx}')
  t('glob matches nested tsx', re.test('src/components/library/LibraryPanel.test.tsx'), true)
  t('glob matches top-level ts', re.test('src/utils.test.ts'), true)
  t('glob rejects non-test file', re.test('src/components/library/LibraryPanel.tsx'), false)
  t('glob rejects .spec', re.test('src/a/b.spec.ts'), false)
  t('glob rejects other root', re.test('tests/e2e/fixtures/selectors.test.ts'), false)
  t('glob rejects .js ext', re.test('src/a.test.js'), false)

  const re2 = globToRegExp('tests/**/*.test.{ts,tsx}')
  t('second glob matches tests/', re2.test('tests/e2e/fixtures/selectors.test.ts'), true)

  t('globRoot of src glob', globRoot('src/**/*.test.{ts,tsx}'), 'src')
  t('globRoot of tests glob', globRoot('tests/**/*.test.{ts,tsx}'), 'tests')

  // Substring semantics must mirror vitest's own path filter.
  const matches = (f, p) => f.includes(p)
  t('substring covers subdir', matches('src/components/chat/tools/X.test.tsx', 'src/components/chat/'), true)
  t('substring rejects sibling', matches('src/components/library/X.test.tsx', 'src/components/chat/'), false)

  const failed = checks.filter((c) => !c.ok)
  for (const c of checks) {
    console.log(`${c.ok ? 'ok  ' : 'FAIL'}  ${c.name}`)
  }
  if (failed.length > 0) {
    console.error(`\nFAIL: ${failed.length} of ${checks.length} self-tests failed.`)
    process.exit(1)
  }
  console.log(`\nOK: all ${checks.length} self-tests passed.`)
  process.exit(0)
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
if (process.argv.includes('--self-test')) selfTest()

const includeGlobs = readVitestIncludeGlobs()
const groups = readMatrixPatterns()

// Enumerate every file on disk that vitest would collect.
const regexps = includeGlobs.map(globToRegExp)
const roots = [...new Set(includeGlobs.map(globRoot))]
const candidates = new Set()
for (const r of roots) walk(r, []).forEach((f) => candidates.add(f))
const files = [...candidates].filter((f) => regexps.some((re) => re.test(f))).sort()

if (files.length === 0) {
  fail(
    'found no test files at all matching vitest\'s include globs ' +
      `(${includeGlobs.join(', ')}) — the globs are wrong, or the tests vanished.`,
  )
}

let failed = false

// --- FR-086: no stale patterns -------------------------------------------
// A pattern ending in '/' names a directory and MUST exist. A pattern without a
// trailing slash is a file-level filter and is valid if it is an existing
// directory OR it selects at least one real test file.
const stale = []
for (const { group, pattern } of groups) {
  const asDir = pattern.replace(/\/+$/, '')
  const abs = join(REPO_ROOT, asDir)
  const isDir = existsSync(abs) && statSync(abs).isDirectory()
  if (pattern.endsWith('/')) {
    if (!isDir) stale.push({ group, pattern, why: 'directory does not exist' })
  } else if (!isDir && !files.some((f) => f.includes(pattern))) {
    stale.push({ group, pattern, why: 'matches no existing directory or test file' })
  }
}

if (stale.length > 0) {
  failed = true
  console.error(
    `\nFAIL (FR-086): ${stale.length} vitest matrix pattern(s) are stale and ` +
      'cannot run anything:\n',
  )
  for (const s of stale) {
    console.error(`  ${s.pattern}   [group: ${s.group}]  — ${s.why}`)
  }
  console.error(
    `\nA pattern pointing at a deleted directory reads as coverage while running\n` +
      `nothing. Remove it from the vitest matrix in ${WORKFLOW}, or restore the path.`,
  )
}

// --- FR-085: every test file matches exactly one group --------------------
const uncovered = []
const multi = []
for (const f of files) {
  const hits = groups.filter((g) => f.includes(g.pattern))
  if (hits.length === 0) uncovered.push(f)
  else if (hits.length > 1) multi.push({ file: f, hits })
}

if (uncovered.length > 0) {
  failed = true
  console.error(
    `\nFAIL (FR-085): ${uncovered.length} of ${files.length} test files match NO ` +
      'vitest matrix group and would NEVER run in CI:\n',
  )
  for (const f of uncovered) console.error(`  ${f}`)

  const byDir = new Map()
  for (const f of uncovered) {
    const d = f.split('/').slice(0, -1).join('/')
    byDir.set(d, (byDir.get(d) ?? 0) + 1)
  }
  console.error('\n  Summary by directory:')
  for (const [d, c] of [...byDir].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))) {
    console.error(`  ${String(c).padStart(4)}  ${d}/`)
  }
  console.error(
    `\nAdd these paths to a \`patterns:\` group in the vitest matrix in ${WORKFLOW}\n` +
      'so they actually execute.',
  )
}

if (multi.length > 0) {
  failed = true
  console.error(
    `\nFAIL (FR-085): ${multi.length} test file(s) match MORE THAN ONE vitest matrix ` +
      'group and would run repeatedly in different shards:\n',
  )
  for (const m of multi) {
    console.error(`  ${m.file}`)
    for (const h of m.hits) console.error(`      matched by "${h.pattern}"  [group: ${h.group}]`)
  }
  console.error(
    '\nGroups must partition the suite, not overlap. Narrow the offending patterns\n' +
      `in ${WORKFLOW} so each file belongs to exactly one group.`,
  )
}

if (failed) process.exit(1)

console.log(
  `OK: all ${files.length} test files (from ${includeGlobs.join(', ')}) match exactly one ` +
    `of ${groups.length} vitest matrix pattern(s), and every pattern resolves to an existing directory.`,
)
