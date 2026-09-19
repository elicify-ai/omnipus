import assert from 'node:assert/strict'
import { after, test } from 'node:test'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'

import { checkCoverage } from '../../scripts/design-system-locks/coverage.mjs'

// Contract sources:
// - design-system/enforcement/contract.json promises
//   checkCoverage({root,indexPath,evidencePaths}) => {errors:string[]}.
// - An explicit root makes each composition call a project-scoped operation;
//   concurrent calls must not resolve one project's declarations in another.
// No timestamp freshness heuristic is asserted here because the checked-in
// contract does not currently define one.

const fixtureRoots = []
const scratchRoot = resolve('dist/design-system-baseline/coverage-adversarial')

after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function write(root, path, content) {
  const destination = resolve(root, path)
  mkdirSync(dirname(destination), { recursive: true })
  writeFileSync(destination, content)
}

function writeJson(root, path, value) {
  write(root, path, `${JSON.stringify(value, null, 2)}\n`)
}

function projectFixture(suffix) {
  mkdirSync(scratchRoot, { recursive: true })
  const root = mkdtempSync(resolve(scratchRoot, `${suffix}-`))
  fixtureRoots.push(root)

  const component = `Toggle${suffix}`
  const stem = `toggle-${suffix.toLowerCase()}`
  const source = `src/components/ui/${stem}.tsx`
  const testFile = `src/components/ui/${stem}.test.tsx`
  const storyFile = `src/components/ui/${stem}.stories.tsx`
  const story = `${component}Story`
  const storyTitle = `${component} Story`
  const unitTitle = `${component} unit contract`

  write(root, source, `export function ${component}() { return null }\n`)
  write(root, testFile, 'export {}\n')
  write(root, storyFile, `export const ${story} = {}\n`)
  write(root, `docs/components/${stem}.md`, `# ${component}\n`)
  write(root, 'src/index.ts', `export { ${component} } from './components/ui/${stem}'\n`)
  write(root, 'src/routes/.keep', '')

  writeJson(root, 'design-system/catalog.json', {
    version: 1,
    classifications: ['foundations', 'primitive', 'composite', 'domain', 'application'],
    entries: [{
      source,
      classification: 'primitive',
      exports: [component],
      publicExports: [component],
      publicTypes: [],
    }],
  })
  writeJson(root, 'design-system/surfaces.json', { version: 1, surfaces: [], sourceOwners: [] })

  const falseCheck = (kind) => ({
    id: `${stem}-${kind}`,
    kind,
    applicable: false,
    reason: `The isolated composition fixture does not exercise ${kind}.`,
  })
  writeJson(root, `design-system/manifests/${stem}.json`, {
    version: 1,
    component,
    exports: [component],
    category: 'primitive',
    source,
    documentation: `docs/components/${stem}.md`,
    owner: 'design-system',
    themes: ['dark'],
    variants: ['default'],
    sizes: ['default'],
    states: ['idle'],
    stories: [{ file: storyFile, exports: [story] }],
    checks: [
      { id: `${stem}-unit`, kind: 'unit', file: testFile, test: unitTitle, applicable: true },
      ...['interaction', 'axe', 'keyboard', 'browser', 'pointer', 'reduced-motion', 'forced-colors', 'root-size', 'zoom', 'reflow'].map(falseCheck),
    ],
  })

  writeJson(root, 'dist/storybook/index.json', {
    entries: {
      [`${stem}--story`]: {
        type: 'story',
        id: `${stem}--story`,
        title: `Fixture/${component}`,
        name: storyTitle,
        importPath: storyFile,
        exportName: story,
      },
    },
  })
  writeJson(root, 'evidence/unit.json', {
    testResults: [{
      name: testFile,
      assertionResults: [{ fullName: unitTitle, title: unitTitle, status: 'passed', failureMessages: [] }],
    }],
  })

  return {
    root,
    arguments: {
      root,
      indexPath: 'dist/storybook/index.json',
      evidencePaths: ['evidence/unit.json'],
    },
  }
}

test('concurrent checkCoverage calls keep path resolution isolated to each explicit root', async () => {
  const first = projectFixture('Alpha')
  const second = projectFixture('Beta')
  const cwdBefore = process.cwd()

  // Several overlapping pairs make the interleaving observable without
  // changing production code or depending on timing sleeps.
  let results
  let cwdAfter
  try {
    results = await Promise.all([
      checkCoverage(first.arguments),
      checkCoverage(second.arguments),
      checkCoverage(first.arguments),
      checkCoverage(second.arguments),
    ])
    cwdAfter = process.cwd()
  } finally {
    // Keep this deliberately failing reproduction isolated from later tests.
    // Production remains responsible for restoring cwd; the test restores the
    // runner only after it has captured the leaked value for the assertion.
    process.chdir(cwdBefore)
  }

  for (const [index, result] of results.entries()) {
    assert.deepEqual(result, { errors: [] }, `concurrent result ${index} crossed project roots: ${JSON.stringify(result)}`)
  }
  assert.equal(cwdAfter, cwdBefore, 'concurrent composition must restore the caller working directory')
})

test('an invalid root resolves to the stable errors result instead of rejecting', async () => {
  mkdirSync(scratchRoot, { recursive: true })
  const missingRoot = resolve(scratchRoot, 'does-not-exist')
  rmSync(missingRoot, { recursive: true, force: true })
  const cwdBefore = process.cwd()

  let result
  await assert.doesNotReject(async () => {
    result = await checkCoverage({
      root: missingRoot,
      indexPath: 'dist/storybook/index.json',
      evidencePaths: ['evidence/unit.json'],
    })
  })

  assert.equal(process.cwd(), cwdBefore, 'invalid-root handling must not change the caller working directory')
  assert.deepEqual(Object.keys(result).sort(), ['errors'])
  assert.ok(Array.isArray(result.errors) && result.errors.length > 0, 'invalid root must return at least one actionable error')
  assert.ok(result.errors.every((error) => typeof error === 'string' && error.length > 0))
})
