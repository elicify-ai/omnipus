import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { checkCoverage } from '../../scripts/design-system-locks/coverage.mjs'

// Contract: design-system/enforcement/contract.json — coverageApi.
// Expected values derive from the Stage A validator specifications
// (scripts/design-system/catalog.mjs, scripts/design-system/verification.mjs)
// and the design-system schemas, never from observed output of coverage.mjs.

const UNIT_TEST_NAME = 'Toggle — action contract'
const STORY_TEST_NAME = 'Toggle Blocks Action'

const fixtureRoots = []
const scratchDirectory = resolve('dist/design-system-baseline/cli-lanes')

after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function write(root, file, content) {
  const path = resolve(root, file)
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, content)
}

function writeJson(root, file, value) {
  write(root, file, `${JSON.stringify(value, null, 2)}\n`)
}

function mutateJson(root, file, mutation) {
  const path = resolve(root, file)
  const value = JSON.parse(readFileSync(path, 'utf8'))
  mutation(value)
  writeFileSync(path, JSON.stringify(value, null, 2))
}

// A complete minimal project: one public primitive with a full manifest and
// executed evidence, one public foundations export, one route surface whose
// checks are explicitly planned. Every file below is load-bearing for at
// least one validator in the composed chain.
//
// The stem parameter names the component inside the fixture. Concurrent-root
// tests rely on different stems: with identical layouts a working-directory
// crossing is invisible, because the foreign root happens to contain the
// same relative files. The default stem reproduces the original fixture
// exactly, including the Storybook index entry key mutated below.
function fixture(name = 'coverage-', stem = 'toggle') {
  mkdirSync(scratchDirectory, { recursive: true })
  const root = mkdtempSync(resolve(scratchDirectory, name))
  fixtureRoots.push(root)

  const component = stem.split('-').map((part) => part[0].toUpperCase() + part.slice(1)).join('')
  const unitTestName = `${component} — action contract`
  const storyTestName = `${component} Blocks Action`
  const storyExport = `${component}BlocksAction`
  const storybookId = `ui-${stem}--${storyExport.replace(/([A-Z])/g, '-$1').toLowerCase().replace(/^-/, '')}`
  const source = `src/components/ui/${stem}.tsx`
  const testFile = `src/components/ui/${stem}.test.tsx`
  const storyFile = `src/components/ui/${stem}.stories.tsx`

  write(root, source, `export function ${component}() {\n  return null\n}\n`)
  write(root, testFile, '// Executed by the unit harness; the evidence report references this file.\nexport {}\n')
  write(root, storyFile, `export const ${storyExport} = {\n  play: async () => {\n    // interaction steps executed by the Storybook harness\n  },\n}\n`)
  write(root, 'src/design-system/tokens.ts', 'export const tokens = {} as const\n')
  write(root, 'src/index.ts', [
    `export { ${component} } from './components/ui/${stem}'`,
    "export { tokens } from './design-system/tokens'",
    '',
  ].join('\n'))
  write(root, 'src/routes/.keep', '')
  write(root, `docs/components/${stem}.md`, `# ${component} contract\n`)

  writeJson(root, 'design-system/catalog.json', {
    version: 1,
    classifications: ['foundations', 'primitive', 'composite', 'domain', 'application'],
    entries: [
      { source, classification: 'primitive', exports: [component], publicExports: [component], publicTypes: [] },
      { source: 'src/design-system/tokens.ts', classification: 'foundations', exports: ['tokens'], publicExports: ['tokens'], publicTypes: [] },
    ],
  })
  writeJson(root, 'design-system/surfaces.json', { version: 1, surfaces: [], sourceOwners: [] })
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
    stories: [{ file: storyFile, exports: [storyExport] }],
    checks: [
      { id: `${stem}-unit`, kind: 'unit', file: testFile, test: unitTestName, applicable: true },
      { id: `${stem}-pending-interaction`, kind: 'interaction', file: storyFile, test: storyTestName, story: storyExport, applicable: true },
      { id: `${stem}-axe`, kind: 'axe', applicable: false, reason: 'fixture: axe runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-keyboard`, kind: 'keyboard', applicable: false, reason: 'fixture: keyboard runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-browser`, kind: 'browser', applicable: false, reason: 'fixture: browser runs in the Playwright matrix, outside this minimal composition fixture' },
      { id: `${stem}-pointer`, kind: 'pointer', applicable: false, reason: 'fixture: pointer runs in the Playwright matrix, outside this minimal composition fixture' },
      { id: `${stem}-reduced-motion`, kind: 'reduced-motion', applicable: false, reason: 'fixture: reduced-motion runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-forced-colors`, kind: 'forced-colors', applicable: false, reason: 'fixture: forced-colors runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-root-size`, kind: 'root-size', applicable: false, reason: 'fixture: root-size runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-zoom`, kind: 'zoom', applicable: false, reason: 'fixture: zoom runs in the browser matrix, outside this minimal composition fixture' },
      { id: `${stem}-reflow`, kind: 'reflow', applicable: false, reason: 'fixture: reflow runs in the browser matrix, outside this minimal composition fixture' },
    ],
  })

  writeJson(root, 'dist/storybook/index.json', {
    entries: {
      [storybookId]: {
        type: 'story',
        id: storybookId,
        title: `UI/${component}`,
        name: storyTestName,
        importPath: storyFile,
        exportName: storyExport,
      },
    },
  })
  writeJson(root, 'evidence/evidence-unit.json', {
    testResults: [{
      name: testFile,
      assertionResults: [{ fullName: unitTestName, title: unitTestName, status: 'passed', failureMessages: [] }],
    }],
  })
  for (const project of ['chromium', 'firefox', 'webkit']) {
    writeJson(root, `evidence/design-system-storybook-${project}.json`, {
      testResults: [{
        name: storyFile,
        assertionResults: [{ fullName: storyTestName, title: storyTestName, status: 'passed', failureMessages: [] }],
      }],
    })
  }
  return root
}

// A route surface whose three required checks are explicitly planned — the
// application surface state that awaits the C batches.
function writePlannedRouteSurface(root, statuses = { unit: 'planned', browser: 'planned', reflow: 'planned' }) {
  write(root, 'src/routes/__root.tsx', 'export const Route = null\n')
  writeJson(root, 'design-system/surfaces.json', {
    version: 1,
    surfaces: [{
      id: '__root',
      kind: 'route',
      source: 'src/routes/__root.tsx',
      symbol: 'Route',
      lane: 1,
      verification: [
        { checkId: 'surface:root:unit', kind: 'unit', file: 'tests/design-system/surfaces/root.test.tsx', test: '__root surface contract', status: statuses.unit },
        { checkId: 'surface:root:browser', kind: 'browser', file: 'tests/design-system/surface-inventory.spec.ts', test: '__root surface browser contract', status: statuses.browser },
        { checkId: 'surface:root:reflow', kind: 'reflow', file: 'tests/design-system/surface-inventory.spec.ts', test: '__root surface reflow contract', status: statuses.reflow },
      ],
      entry: { type: 'route', path: '/' },
    }],
    sourceOwners: [{ source: 'src/routes/__root.tsx', lane: 1 }],
  })
}

function run(root, overrides = {}) {
  const defaults = {
    indexPath: resolve(root, 'dist/storybook/index.json'),
    evidencePaths: [
      resolve(root, 'evidence/evidence-unit.json'),
      ...['chromium', 'firefox', 'webkit'].map((project) => resolve(root, `evidence/design-system-storybook-${project}.json`)),
    ],
  }
  const arguments_ = { root }
  for (const key of ['indexPath', 'evidencePaths']) arguments_[key] = key in overrides ? overrides[key] : defaults[key]
  return checkCoverage(arguments_)
}

function runWithChildPreload(root, source) {
  const preload = resolve(root, 'coverage-child-preload.cjs')
  write(root, 'coverage-child-preload.cjs', source)
  const previous = process.env.NODE_OPTIONS
  process.env.NODE_OPTIONS = [previous, `--require=${preload}`].filter(Boolean).join(' ')
  try {
    return run(root)
  } finally {
    if (previous === undefined) delete process.env.NODE_OPTIONS
    else process.env.NODE_OPTIONS = previous
  }
}

test('passes with exactly zero errors on a fully covered minimal project', async () => {
  const root = fixture()
  const result = await run(root)
  assert.deepEqual(Object.keys(result).sort(), ['errors'])
  assert.deepEqual(result.errors, [])
})

test('resolves indexPath and evidencePaths against root and restores the working directory', async () => {
  const root = fixture()
  const before = process.cwd()
  const result = await run(root, {
    indexPath: 'dist/storybook/index.json',
    evidencePaths: [
      'evidence/evidence-unit.json',
      ...['chromium', 'firefox', 'webkit'].map((project) => `evidence/design-system-storybook-${project}.json`),
    ],
  })
  assert.deepEqual(result.errors, [])
  assert.equal(process.cwd(), before, 'checkCoverage must not leak its working-directory change')
})

test('tolerates explicitly planned application surface checks awaiting C batches', async () => {
  const root = fixture()
  writePlannedRouteSurface(root)
  const result = await run(root)
  assert.deepEqual(result.errors, [])
})

test('keeps passing when a planned surface check becomes executed with its file present', async () => {
  const root = fixture()
  writePlannedRouteSurface(root, { unit: 'executed', browser: 'executed', reflow: 'executed' })
  write(root, 'tests/design-system/surfaces/root.test.tsx', 'export {}\n')
  write(root, 'tests/design-system/surface-inventory.spec.ts', 'export {}\n')
  const result = await run(root)
  assert.deepEqual(result.errors, [])
})

test('rejects a surface check whose status is neither executed nor planned', async () => {
  const root = fixture()
  writePlannedRouteSurface(root, { unit: 'pending', browser: 'planned', reflow: 'planned' })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /invalid verification status: __root/)
})

test('rejects a planned surface check with an incomplete mapping', async () => {
  const root = fixture()
  writePlannedRouteSurface(root)
  mutateJson(root, 'design-system/surfaces.json', (inventory) => { inventory.surfaces[0].verification[0].file = '' })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /invalid verification mapping: __root/)
})

test('rejects a surface missing a required verification kind', async () => {
  const root = fixture()
  writePlannedRouteSurface(root)
  mutateJson(root, 'design-system/surfaces.json', (inventory) => {
    inventory.surfaces[0].verification = inventory.surfaces[0].verification.filter((check) => check.kind !== 'reflow')
  })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /missing reflow verification: __root/)
})

test('blocks a public primitive export whose manifest was removed, and fails closed when no manifests remain', async () => {
  const root = fixture()
  rmSync(resolve(root, 'design-system/manifests/toggle.json'))
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /missing manifest coverage: src\/components\/ui\/toggle\.tsx#Toggle/)
  assert.match(errors, /no component manifests found/)
})

test('blocks a manifest that silently omits a required check kind', async () => {
  const root = fixture()
  mutateJson(root, 'design-system/manifests/toggle.json', (manifest) => {
    manifest.checks = manifest.checks.filter((check) => check.kind !== 'axe')
  })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /manifest Toggle omits required check kind: axe/)
})

test('blocks a story missing from the Storybook index', async () => {
  const root = fixture()
  mutateJson(root, 'dist/storybook/index.json', (index) => { index.entries = {} })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /Storybook index missing src\/components\/ui\/toggle\.stories\.tsx#ToggleBlocksAction/)
})

test('blocks an interaction story without an explicit play function', async () => {
  const root = fixture()
  write(root, 'src/components/ui/toggle.stories.tsx', 'export const ToggleBlocksAction = {}\n')
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /interaction story ToggleBlocksAction must declare an explicit play function/)
})

test('blocks an applicable check without exact executed evidence', async () => {
  const root = fixture()
  mutateJson(root, 'evidence/evidence-unit.json', (report) => { report.testResults = [] })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, new RegExp(`no exact executed evidence for src/components/ui/toggle\\.test\\.tsx::${UNIT_TEST_NAME}`))
})

test('fails closed when an evidence report file is missing', async () => {
  const root = fixture()
  const result = await run(root, { evidencePaths: [resolve(root, 'evidence/missing-evidence.json')] })
  assert.equal(result.errors.length, 1)
  assert.match(result.errors[0], /missing-evidence\.json/)
  assert.match(result.errors[0], /ENOENT/)
})

test('fails closed when the Storybook index file is missing', async () => {
  const root = fixture()
  const result = await run(root, { indexPath: resolve(root, 'dist/storybook/missing-index.json') })
  assert.ok(result.errors.length >= 1)
  assert.match(result.errors.join('\n'), /missing-index\.json/)
})

test('blocks evidence that contains a failed result', async () => {
  const root = fixture()
  mutateJson(root, 'evidence/evidence-unit.json', (report) => { report.testResults[0].assertionResults[0].status = 'failed' })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /evidence contains a failed or retried-failure result/)
  assert.doesNotMatch(errors, /no exact executed evidence for src\/components\/ui\/toggle\.test\.tsx/)
})

test('blocks an extra module export outside the catalog', async () => {
  const root = fixture()
  write(root, 'src/components/ui/toggle.tsx', 'export function Toggle() {\n  return null\n}\n\nexport const Surprise = 1\n')
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /uncataloged module export: src\/components\/ui\/toggle\.tsx#Surprise/)
})

test('blocks an extra public re-export outside the catalog', async () => {
  const root = fixture()
  write(root, 'src/components/ui/toggle.tsx', 'export function Toggle() {\n  return null\n}\n\nexport const Surprise = 1\n')
  write(root, 'src/index.ts', "export { Toggle, Surprise } from './components/ui/toggle'\nexport { tokens } from './design-system/tokens'\n")
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /uncataloged public export: src\/components\/ui\/toggle\.tsx#Surprise/)
})

test('blocks a public export whose classification is not foundations, primitive, or composite', async () => {
  const root = fixture()
  mutateJson(root, 'design-system/catalog.json', (catalog) => {
    catalog.entries.find((entry) => entry.source === 'src/design-system/tokens.ts').classification = 'domain'
  })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /invalid public classification: src\/design-system\/tokens\.ts#tokens/)
})

test('blocks a malformed catalog shape without throwing', async () => {
  const root = fixture()
  writeJson(root, 'design-system/catalog.json', { version: 1, classifications: 'primitive', entries: 'wrong' })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /classifications and entries arrays/)
})

test('fails closed on unparseable manifest JSON', async () => {
  const root = fixture()
  write(root, 'design-system/manifests/toggle.json', '{not json')
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /invalid JSON .*manifests\/toggle\.json/)
})

test('blocks a manifest violating its schema', async () => {
  const root = fixture()
  mutateJson(root, 'design-system/manifests/toggle.json', (manifest) => { delete manifest.stories })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /toggle\.json: schema .*must have required property 'stories'/)
})

test('fails closed on an unsupported evidence report shape', async () => {
  const root = fixture()
  writeJson(root, 'evidence/evidence-unit.json', { nonsense: true })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /unsupported evidence report shape/)
})

test('blocks a public entry that does not parse as TypeScript', async () => {
  const root = fixture()
  write(root, 'src/index.ts', 'export {')
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, /TypeScript parse failure in src\/index\.ts/)
})

test('never satisfies a check from a suffix-named file or a superstring test title', async () => {
  const root = fixture()
  mutateJson(root, 'evidence/evidence-unit.json', (report) => {
    report.testResults[0].name = 'src/components/ui/toggle-extra.test.tsx'
    report.testResults[0].assertionResults[0].fullName = `${UNIT_TEST_NAME} extra`
    report.testResults[0].assertionResults[0].title = `${UNIT_TEST_NAME} extra`
  })
  mutateJson(root, 'dist/storybook/index.json', (index) => {
    const entry = index.entries['ui-toggle--toggle-blocks-action']
    entry.exportName = 'ToggleBlocksActionExtra'
    entry.name = `${STORY_TEST_NAME} Extra`
  })
  const errors = (await run(root)).errors.join('\n')
  assert.match(errors, new RegExp(`no exact executed evidence for src/components/ui/toggle\\.test\\.tsx::${UNIT_TEST_NAME}`))
  assert.match(errors, /Storybook index missing src\/components\/ui\/toggle\.stories\.tsx#ToggleBlocksAction/)
})

test('blocks every applicable check when evidencePaths is omitted', async () => {
  const root = fixture()
  const errors = (await run(root, { evidencePaths: undefined })).errors.join('\n')
  assert.match(errors, new RegExp(`no exact executed evidence for src/components/ui/toggle\\.test\\.tsx::${UNIT_TEST_NAME}`))
  assert.match(errors, /no exact executed evidence for src\/components\/ui\/toggle\.stories\.tsx::Toggle Blocks Action/)
})

// Review follow-up (design-system/enforcement/review-coverage.json findings
// async-process-cwd-race and invalid-root-breaks-return-contract). The
// composition API is root-pure: concurrent calls with different roots return
// each root its own deterministic result, an invalid root resolves through
// the stable {errors} shape, and the caller working directory never moves.
// Expected values derive from the shared coverageApi contract, not from
// observed output of coverage.mjs.

test('concurrent calls with different roots return each root its own deterministic result', async () => {
  const first = fixture('coverage-first-', 'toggle-first')
  const second = fixture('coverage-second-', 'toggle-second')
  rmSync(resolve(second, 'design-system/manifests/toggle-second.json'))
  const before = process.cwd()

  const results = await Promise.all([run(first), run(second), run(first), run(second)])

  assert.deepEqual(results[0].errors, [], 'first root must stay clean while the second root composes concurrently')
  assert.deepEqual(results[2].errors, [], 'first root must stay clean on its second concurrent call')
  assert.match(results[1].errors.join('\n'), /missing manifest coverage: src\/components\/ui\/toggle-second\.tsx#ToggleSecond/)
  assert.match(results[3].errors.join('\n'), /no component manifests found/)
  assert.equal(process.cwd(), before, 'concurrent composition must not touch the caller working directory')
})

test('an invalid root that does not exist resolves to actionable errors without rejecting', async () => {
  mkdirSync(scratchDirectory, { recursive: true })
  const missingRoot = resolve(scratchDirectory, 'coverage-missing-root')
  rmSync(missingRoot, { recursive: true, force: true })
  const before = process.cwd()

  const result = await checkCoverage({ root: missingRoot, indexPath: 'dist/storybook/index.json', evidencePaths: ['evidence/evidence-unit.json'] })

  assert.deepEqual(Object.keys(result).sort(), ['errors'])
  assert.ok(result.errors.length >= 1, 'invalid root must return at least one actionable error')
  assert.ok(result.errors.every((error) => typeof error === 'string' && error.length > 0))
  assert.match(result.errors.join('\n'), /invalid root/)
  assert.match(result.errors.join('\n'), /coverage-missing-root/)
  assert.equal(process.cwd(), before, 'invalid-root handling must not change the caller working directory')
})

test('an invalid root that is a regular file resolves to actionable errors without rejecting', async () => {
  mkdirSync(scratchDirectory, { recursive: true })
  const fileRoot = resolve(scratchDirectory, 'coverage-file-root.txt')
  writeFileSync(fileRoot, 'not a project root\n')
  const before = process.cwd()

  const result = await checkCoverage({ root: fileRoot, indexPath: 'dist/storybook/index.json', evidencePaths: ['evidence/evidence-unit.json'] })

  assert.deepEqual(Object.keys(result).sort(), ['errors'])
  assert.ok(result.errors.length >= 1, 'file root must return at least one actionable error')
  assert.ok(result.errors.every((error) => typeof error === 'string' && error.length > 0))
  assert.match(result.errors.join('\n'), /invalid root/)
  assert.match(result.errors.join('\n'), /not a directory/)
  assert.equal(process.cwd(), before, 'invalid-root handling must not change the caller working directory')
})

test('a malformed root does not contaminate a concurrently composing valid root', async () => {
  const valid = fixture('coverage-valid-', 'toggle-valid')
  mkdirSync(scratchDirectory, { recursive: true })
  const missingRoot = resolve(scratchDirectory, 'coverage-missing-concurrent')
  rmSync(missingRoot, { recursive: true, force: true })
  const before = process.cwd()

  const results = await Promise.all([
    run(valid),
    checkCoverage({ root: missingRoot, indexPath: 'dist/storybook/index.json', evidencePaths: ['evidence/evidence-unit.json'] }),
  ])

  assert.deepEqual(results[0].errors, [], 'valid root must be unaffected by the concurrently rejected root')
  assert.match(results[1].errors.join('\n'), /invalid root/)
  assert.equal(process.cwd(), before, 'mixed concurrent composition must not touch the caller working directory')
})

test('fails closed when the child prints a clean report and then exits nonzero', async () => {
  const root = fixture('coverage-child-nonzero-', 'toggle-child-nonzero')
  const result = await runWithChildPreload(root, "process.on('beforeExit', () => { process.exitCode = 7 })\n")

  assert.equal(result.errors.length, 1, JSON.stringify(result))
  assert.match(result.errors[0], /isolated composition failed/)
  assert.match(result.errors[0], /exit=7/)
})

test('fails closed when the child prints a clean report and is then signaled', async () => {
  const root = fixture('coverage-child-signal-', 'toggle-child-signal')
  const result = await runWithChildPreload(root, "process.on('beforeExit', () => { process.kill(process.pid, 'SIGTERM') })\n")

  assert.equal(result.errors.length, 1, JSON.stringify(result))
  assert.match(result.errors[0], /isolated composition failed/)
  assert.match(result.errors[0], /signal=SIGTERM/)
})
