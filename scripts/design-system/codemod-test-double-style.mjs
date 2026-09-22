#!/usr/bin/env node
// Codemod: remove `style={style}` forwarding from framer-motion test doubles
// — Stage B closure (design-system-migration-plan.md §"Stage B closure —
// founder decisions, 2026-09-19"), pattern P5 (test double / mock component
// whole-style-object forwarding).
//
// Pattern: a `vi.mock('framer-motion', ...)` mock component destructures a
// `style` prop and forwards it verbatim (`style={style}`) onto the rendered
// element. No test assertion in any of the target files reads that forwarded
// style (no `toHaveStyle`, `.style.`, `getComputedStyle`, or snapshot
// matcher) — removing the destructured parameter and the forwarding prop is
// therefore behaviourally invisible to every test in the file.
//
// Files changed: five Sidebar test doubles
// - src/components/layout/Sidebar.focus-trap.test.tsx
// - src/components/layout/Sidebar.kb5.test.tsx
// - src/components/layout/Sidebar.m5.test.tsx
// - src/components/layout/Sidebar.orphan.test.tsx
// - src/components/layout/Sidebar.test.tsx
//
// Safety:
//   - refuses a file whole-file if it contains a style-observing assertion
//     (toHaveStyle / .style. / getComputedStyle / toMatchSnapshot /
//     toMatchInlineSnapshot) — the premise "no assertion reads the forwarded
//     style" must hold for THIS file, not just the five files audited when
//     the pattern was discovered;
//   - refuses a file whole-file when the count of JSX `style={style}`
//     forwarding sites does not equal the count of matched destructuring
//     removals — a mismatch means at least one occurrence doesn't fit either
//     known destructuring shape, and blindly stripping just the JSX side
//     would leave an unused `style` destructured parameter behind (not
//     invisible: an unused-variable lint failure, and a genuine ambiguity
//     about which destructuring shape is really in play).
import { resolve } from 'node:path'
import { parseCodemodArgs, readFile, writeFileAtomic } from './codemod-lib.mjs'

export const TARGET_FILES = [
  'src/components/layout/Sidebar.focus-trap.test.tsx',
  'src/components/layout/Sidebar.kb5.test.tsx',
  'src/components/layout/Sidebar.m5.test.tsx',
  'src/components/layout/Sidebar.orphan.test.tsx',
  'src/components/layout/Sidebar.test.tsx',
]

// Pattern 1: full destructuring with named aria props.
// ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: ...)
//   => ({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }:
const DESTRUCTURE_FULL_RE =
  /\(\{\s*children,\s*className,\s*style,\s*role,\s*'aria-modal':\s*ariaModal,\s*'aria-label':\s*ariaLabel,\s*\.\.\.\s*rest\s*\}:/g
const DESTRUCTURE_FULL_REPLACEMENT =
  "({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }:"

// Pattern 2: short destructuring.
// ({ children, className, style, ...rest }: ...) => ({ children, className, ...rest }:
const DESTRUCTURE_SHORT_RE = /\(\{\s*children,\s*className,\s*style,\s*\.\.\.\s*rest\s*\}:/g
const DESTRUCTURE_SHORT_REPLACEMENT = '({ children, className, ...rest }:'

// `style={style}` forwarding onto the rendered mock element, with the
// surrounding whitespace collapsed to a single space.
const JSX_FORWARD_RE = /\s+style=\{style\}\s+/g
const JSX_FORWARD_REPLACEMENT = ' '

// A test in this file reads/asserts on the forwarded style — refuse the
// whole file rather than risk changing what such an assertion observes.
const STYLE_ASSERTION_PATTERNS = [/toHaveStyle/, /\.style\./, /getComputedStyle/, /toMatchSnapshot/, /toMatchInlineSnapshot/]

function countMatches(re, content) {
  const matches = content.match(re)
  return matches ? matches.length : 0
}

/** True when some test assertion in `content` observes the forwarded style. */
export function hasStyleAssertion(content) {
  return STYLE_ASSERTION_PATTERNS.some((re) => re.test(content))
}

/**
 * True when the number of JSX `style={style}` forwarding sites doesn't equal
 * the number of destructuring sites that would be rewritten — i.e. at least
 * one `style={style}` occurrence doesn't correspond to either known
 * destructuring shape, so stripping it would leave an unused `style`
 * destructured parameter behind.
 */
export function isAmbiguousStyleForwarding(content) {
  const jsxCount = countMatches(JSX_FORWARD_RE, content)
  const destructureCount = countMatches(DESTRUCTURE_FULL_RE, content) + countMatches(DESTRUCTURE_SHORT_RE, content)
  return jsxCount !== destructureCount
}

/** Pure transform: removes the `style` destructuring parameter and the
 * `style={style}` JSX forwarding from framer-motion mock components. Does
 * not check for ambiguity or style assertions — callers that need the
 * refusal guards should call `planTestDoubleStyleEdit` instead. */
export function removeStyleFromMock(content) {
  let updated = content.replace(DESTRUCTURE_FULL_RE, DESTRUCTURE_FULL_REPLACEMENT)
  updated = updated.replace(DESTRUCTURE_SHORT_RE, DESTRUCTURE_SHORT_REPLACEMENT)
  updated = updated.replace(JSX_FORWARD_RE, JSX_FORWARD_REPLACEMENT)
  return updated
}

/**
 * Plans the edit for one file's content — never touches disk. Returns
 * `{ changed, refused, reason?, content }`. `content` is the (possibly
 * unchanged) result; when `refused` is true it is byte-identical to the
 * input.
 */
export function planTestDoubleStyleEdit(content) {
  if (hasStyleAssertion(content)) {
    return {
      changed: false,
      refused: true,
      reason: 'file contains a style-observing assertion (toHaveStyle/.style./getComputedStyle/toMatchSnapshot/toMatchInlineSnapshot) — refusing to remove style forwarding',
      content,
    }
  }
  if (isAmbiguousStyleForwarding(content)) {
    return {
      changed: false,
      refused: true,
      reason: 'count of `style={style}` JSX forwarding sites does not match the count of recognized destructuring shapes — refusing an ambiguous match',
      content,
    }
  }
  const updated = removeStyleFromMock(content)
  return { changed: updated !== content, refused: false, content: updated }
}

/** Processes one file relative to `repoRoot`. Never mutates unless `apply`
 * is true. Returns a result record describing what happened (or would). */
export function processFile(repoRoot, relPath, { apply = false } = {}) {
  const filePath = resolve(repoRoot, relPath)
  try {
    const original = readFile(filePath)
    const plan = planTestDoubleStyleEdit(original)

    if (plan.refused) {
      return { file: relPath, changed: false, refused: true, reason: plan.reason }
    }
    if (!plan.changed) {
      return { file: relPath, changed: false }
    }
    if (apply) {
      writeFileAtomic(filePath, plan.content)
      return { file: relPath, changed: true, applied: true }
    }
    return { file: relPath, changed: true, applied: false }
  } catch (err) {
    return { file: relPath, error: err.message }
  }
}

/** Runs the codemod over `files` (default TARGET_FILES) rooted at `repoRoot`. */
export function runCodemod({ repoRoot, apply = false, files = TARGET_FILES } = {}) {
  return files.map((relPath) => processFile(repoRoot, relPath, { apply }))
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const results = runCodemod({ repoRoot: root, apply })

  console.log('\n=== Codemod Results ===\n')
  let totalChanged = 0
  let totalRefused = 0
  for (const result of results) {
    if (result.error) {
      console.log(`ERROR ${result.file}: ${result.error}`)
    } else if (result.refused) {
      console.log(`REFUSED ${result.file}: ${result.reason}`)
      totalRefused++
    } else if (result.changed) {
      console.log(`CHANGED ${result.file}: modified ${result.applied ? '(APPLIED)' : '(dry-run)'}`)
      totalChanged++
    } else {
      console.log(`OK ${result.file}: no changes`)
    }
  }

  console.log(`\nTotal changed: ${totalChanged}/${results.length}`)
  console.log(`Total refused: ${totalRefused}/${results.length}`)
  console.log(`Mode: ${apply ? 'APPLY' : 'DRY RUN'}`)

  process.exit(results.some((r) => r.error) ? 1 : 0)
}
