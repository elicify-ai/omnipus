import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  removeStyleFromMock,
  hasStyleAssertion,
  isAmbiguousStyleForwarding,
  planTestDoubleStyleEdit,
  processFile,
  runCodemod,
  TARGET_FILES,
} from './codemod-test-double-style.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // CI runs `node --test scripts/design-system/codemod-*.test.mjs` on a
  // fresh checkout with no dist/ at all — create the parent explicitly
  // rather than relying on another test file having created it first.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-test-double-style-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

const FULL_DESTRUCTURE_MOCK = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} style={style} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),
  },
}))
`

const FULL_DESTRUCTURE_EXPECTED = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),
  },
}))
`

const SHORT_DESTRUCTURE_MOCK = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, style, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} style={style} {...rest}>{children}</aside>
    ),
  },
}))
`

const SHORT_DESTRUCTURE_EXPECTED = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} {...rest}>{children}</aside>
    ),
  },
}))
`

// ── removeStyleFromMock: pure transform ──────────────────────────────────

test('MATCH: full destructuring with named aria props — removes the style param and the JSX forwarding', () => {
  const result = removeStyleFromMock(FULL_DESTRUCTURE_MOCK)
  assert.equal(result, FULL_DESTRUCTURE_EXPECTED)
  assert.doesNotMatch(result, /style/)
})

test('MATCH: short destructuring — removes the style param and the JSX forwarding', () => {
  const result = removeStyleFromMock(SHORT_DESTRUCTURE_MOCK)
  assert.equal(result, SHORT_DESTRUCTURE_EXPECTED)
  assert.doesNotMatch(result, /style/)
})

test('NO-MATCH: style destructured/forwarded outside a framer-motion mock shape is left untouched', () => {
  const before = `function myComponent({ children, className, style }: Props) {
  return <div className={className} style={style}>{children}</div>;
}
`
  const result = removeStyleFromMock(before)
  assert.equal(result, before)
})

test('NO-MATCH: a differently named prop (customStyle) is left untouched', () => {
  const before = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, customStyle, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} customStyle={customStyle} {...rest}>{children}</aside>
    ),
  },
}))
`
  const result = removeStyleFromMock(before)
  assert.equal(result, before)
})

test('IDEMPOTENT: applying removeStyleFromMock to an already-fixed mock produces no further change', () => {
  const once = removeStyleFromMock(FULL_DESTRUCTURE_MOCK)
  const twice = removeStyleFromMock(once)
  assert.equal(twice, once)
})

// ── hasStyleAssertion ─────────────────────────────────────────────────────

test('hasStyleAssertion is false for a file with no style-observing assertions', () => {
  assert.equal(hasStyleAssertion(FULL_DESTRUCTURE_MOCK), false)
})

test('hasStyleAssertion is true when a test uses toHaveStyle', () => {
  const content = `${FULL_DESTRUCTURE_MOCK}\nexpect(screen.getByRole('complementary')).toHaveStyle({ width: '240px' })\n`
  assert.equal(hasStyleAssertion(content), true)
})

test('hasStyleAssertion is true when a test reads .style. directly', () => {
  const content = `${FULL_DESTRUCTURE_MOCK}\nexpect(aside.style.width).toBe('240px')\n`
  assert.equal(hasStyleAssertion(content), true)
})

// ── isAmbiguousStyleForwarding ─────────────────────────────────────────────

test('isAmbiguousStyleForwarding is false for a clean full-destructure match', () => {
  assert.equal(isAmbiguousStyleForwarding(FULL_DESTRUCTURE_MOCK), false)
})

test('isAmbiguousStyleForwarding is true when a `style={style}` JSX site has no recognized destructuring counterpart', () => {
  // `style` is destructured in a shape the codemod does not recognize (style
  // listed first), so DESTRUCTURE_FULL_RE/DESTRUCTURE_SHORT_RE both miss it,
  // yet the JSX side still forwards `style={style}` — stripping only the JSX
  // side here would leave the destructured `style` param unused.
  const before = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ style, children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} style={style} {...rest}>{children}</aside>
    ),
  },
}))
`
  assert.equal(isAmbiguousStyleForwarding(before), true)
})

// ── planTestDoubleStyleEdit: refusal guards ─────────────────────────────────

test('REFUSAL: a file whose test asserts on the forwarded style is refused, unchanged', () => {
  const content = `${FULL_DESTRUCTURE_MOCK}\nexpect(screen.getByRole('complementary')).toHaveStyle({ width: '240px' })\n`
  const plan = planTestDoubleStyleEdit(content)
  assert.equal(plan.refused, true)
  assert.equal(plan.changed, false)
  assert.match(plan.reason, /style-observing assertion/)
  assert.equal(plan.content, content, 'refused content is byte-identical to the input')
})

test('REFUSAL: an ambiguous style-forwarding count mismatch is refused, unchanged', () => {
  const before = `vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ style, children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} style={style} {...rest}>{children}</aside>
    ),
  },
}))
`
  const plan = planTestDoubleStyleEdit(before)
  assert.equal(plan.refused, true)
  assert.equal(plan.changed, false)
  assert.match(plan.reason, /ambiguous match/)
  assert.equal(plan.content, before)
})

test('MATCH via planTestDoubleStyleEdit: a clean mock with no assertions and no ambiguity is changed', () => {
  const plan = planTestDoubleStyleEdit(FULL_DESTRUCTURE_MOCK)
  assert.equal(plan.refused, false)
  assert.equal(plan.changed, true)
  assert.equal(plan.content, FULL_DESTRUCTURE_EXPECTED)
})

test('NO-MATCH via planTestDoubleStyleEdit: content with nothing to remove reports changed=false, not refused', () => {
  const before = `function myComponent({ children, className, style }: Props) {
  return <div className={className} style={style}>{children}</div>;
}
`
  const plan = planTestDoubleStyleEdit(before)
  assert.equal(plan.refused, false)
  assert.equal(plan.changed, false)
  assert.equal(plan.content, before)
})

// ── processFile / runCodemod: filesystem behaviour ──────────────────────────

test('processFile dry-run reports changed=true but does not write to disk', () => {
  const root = fixtureRepo()
  const relPath = 'src/components/layout/Fixture.dry-run.test.tsx'
  write(root, relPath, FULL_DESTRUCTURE_MOCK)

  const result = processFile(root, relPath, { apply: false })
  assert.equal(result.changed, true)
  assert.equal(result.applied, false)
  assert.equal(readFileSync(resolve(root, relPath), 'utf8'), FULL_DESTRUCTURE_MOCK, 'dry-run must not write')
})

test('processFile --apply writes the transformed content to disk', () => {
  const root = fixtureRepo()
  const relPath = 'src/components/layout/Fixture.apply.test.tsx'
  write(root, relPath, FULL_DESTRUCTURE_MOCK)

  const result = processFile(root, relPath, { apply: true })
  assert.equal(result.changed, true)
  assert.equal(result.applied, true)
  assert.equal(readFileSync(resolve(root, relPath), 'utf8'), FULL_DESTRUCTURE_EXPECTED)
})

test('processFile --apply on a refused file leaves the file untouched on disk', () => {
  const root = fixtureRepo()
  const relPath = 'src/components/layout/Fixture.refused.test.tsx'
  const content = `${FULL_DESTRUCTURE_MOCK}\nexpect(aside.style.width).toBe('240px')\n`
  write(root, relPath, content)

  const result = processFile(root, relPath, { apply: true })
  assert.equal(result.refused, true)
  assert.equal(result.changed, false)
  assert.equal(readFileSync(resolve(root, relPath), 'utf8'), content)
})

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  const relPath = 'src/components/layout/Fixture.idempotent.test.tsx'
  write(root, relPath, FULL_DESTRUCTURE_MOCK)

  const first = runCodemod({ repoRoot: root, apply: true, files: [relPath] })
  assert.equal(first[0].changed, true)
  const afterFirst = readFileSync(resolve(root, relPath), 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true, files: [relPath] })
  assert.equal(second[0].changed, false, 'idempotent: nothing left to remove')
  const afterSecond = readFileSync(resolve(root, relPath), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

test('runCodemod reports a filesystem error for a missing file rather than throwing', () => {
  const root = fixtureRepo()
  const results = runCodemod({ repoRoot: root, apply: false, files: ['src/does/not/exist.test.tsx'] })
  assert.equal(results.length, 1)
  assert.ok(results[0].error, 'missing file must surface as an error result')
})

test('TARGET_FILES lists exactly the five Sidebar test doubles the pattern was discovered in', () => {
  assert.deepEqual(
    [...TARGET_FILES].sort(),
    [
      'src/components/layout/Sidebar.focus-trap.test.tsx',
      'src/components/layout/Sidebar.kb5.test.tsx',
      'src/components/layout/Sidebar.m5.test.tsx',
      'src/components/layout/Sidebar.orphan.test.tsx',
      'src/components/layout/Sidebar.test.tsx',
    ].sort(),
  )
})
