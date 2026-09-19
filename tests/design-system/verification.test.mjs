import assert from 'node:assert/strict'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import test, { after } from 'node:test'
import { verifyCoverage } from '../../scripts/design-system/verification.mjs'

const roots = []
after(async () => { await Promise.all(roots.map((root) => rm(root, { recursive: true, force: true }))) })

async function setup(checks) {
  const parent = resolve('dist/design-system-baseline')
  await mkdir(parent, { recursive: true })
  const root = await mkdtemp(join(parent, 'verification-'))
  roots.push(root)
  for (const directory of ['design-system/manifests', 'src', 'docs', 'tests/design-system']) await mkdir(join(root, directory), { recursive: true })
  await Promise.all([
    writeFile(join(root, 'src/button.tsx'), 'export const Button = () => null'),
    writeFile(join(root, 'src/button.stories.tsx'), 'export const Primary = { play: async () => {} }'),
    writeFile(join(root, 'src/button.test.tsx'), 'test("Button unit", () => {})'),
    writeFile(join(root, 'docs/button.md'), '# Button'),
    writeFile(join(root, 'tests/design-system/browser.spec.ts'), 'test("Button browser", () => {})'),
  ])
  await writeFile(join(root, 'design-system/manifests/button.json'), JSON.stringify({ version: 1, component: 'Button', exports: ['Button'], category: 'primitive', owner: 'design-system', themes: ['dark'], variants: ['default'], sizes: ['default'], states: ['idle'], source: 'src/button.tsx', documentation: 'docs/button.md', stories: [{ file: 'src/button.stories.tsx', exports: ['Primary'] }], checks }))
  await writeFile(join(root, 'index.json'), JSON.stringify({ entries: { primary: { type: 'story', exportName: 'Primary', name: 'Button interaction', importPath: './src/button.stories.tsx' } } }))
  return root
}

function playwrightSpec({ file = 'tests/design-system/browser.spec.ts', rootDir, title = 'Button browser', projects = ['chromium', 'firefox', 'webkit', 'chromium-coarse-pointer'], failedFirst = false }) {
  return { ...(rootDir ? { config: { rootDir } } : {}), suites: [{ title: 'browser.spec.ts', file, specs: [{ title, file, tests: projects.map((projectName) => ({ projectName, status: 'expected', results: failedFirst && projectName === 'chromium' ? [{ status: 'failed' }, { status: 'passed' }] : [{ status: 'passed' }] })) }] }] }
}

function vitestReport({ file, title }) {
  return { testResults: [{ name: file, assertionResults: [{ fullName: title, status: 'passed', failureMessages: [] }] }] }
}

async function run(root, evidenceFiles) {
  const previous = process.cwd(); process.chdir(root)
  try { return await verifyCoverage({ manifestDir: join(root, 'design-system/manifests'), storybookIndex: join(root, 'index.json'), evidenceFiles }) }
  finally { process.chdir(previous) }
}

test('requires exact file, exact test, every browser project, and no failed retry', async () => {
  const root = await setup([{ id: 'button-browser', kind: 'browser', applicable: true, file: 'tests/design-system/browser.spec.ts', test: 'Button browser', story: 'Primary' }])
  const evidence = join(root, 'playwright.json')
  await writeFile(evidence, JSON.stringify(playwrightSpec({ file: 'tests/wrong.spec.ts' })))
  assert.match((await run(root, [evidence])).errors.join('\n'), /no exact executed evidence/)
  await writeFile(evidence, JSON.stringify(playwrightSpec({ file: join(root, 'another-package/tests/design-system/browser.spec.ts') })))
  assert.match((await run(root, [evidence])).errors.join('\n'), /no exact executed evidence/, 'a different package sharing the suffix must not satisfy this manifest')
  await writeFile(evidence, JSON.stringify(playwrightSpec({ title: 'Button browser extra' })))
  assert.match((await run(root, [evidence])).errors.join('\n'), /no exact executed evidence/)
  await writeFile(evidence, JSON.stringify(playwrightSpec({ projects: ['chromium', 'firefox', 'webkit'] })))
  assert.match((await run(root, [evidence])).errors.join('\n'), /missing required project chromium-coarse-pointer/)
  await writeFile(evidence, JSON.stringify(playwrightSpec({ failedFirst: true })))
  assert.match((await run(root, [evidence])).errors.join('\n'), /failed or retried-failure/)
  await writeFile(evidence, JSON.stringify(playwrightSpec({ file: 'browser.spec.ts', rootDir: join(root, 'tests/design-system') })))
  assert.equal((await run(root, [evidence])).pass, true, 'Playwright resolves basename files relative to its reported rootDir')
  await writeFile(evidence, JSON.stringify(playwrightSpec({})))
  assert.equal((await run(root, [evidence])).pass, true)
})

test('accepts exact Vitest unit and three-engine Storybook story evidence', async () => {
  const root = await setup([
    { id: 'button-unit', kind: 'unit', applicable: true, file: 'src/button.test.tsx', test: 'Button unit' },
    { id: 'button-interaction', kind: 'interaction', applicable: true, file: 'src/button.stories.tsx', test: 'Button interaction', story: 'Primary' },
  ])
  const unit = join(root, 'design-system-unit.json')
  const stories = ['chromium', 'firefox', 'webkit'].map((engine) => join(root, `design-system-storybook-${engine}.json`))
  await writeFile(unit, JSON.stringify(vitestReport({ file: join(root, 'src/button.test.tsx'), title: 'Button unit' })))
  await Promise.all(stories.map((story) => writeFile(
    story,
    JSON.stringify(vitestReport({ file: join(root, 'src/button.stories.tsx'), title: 'Button interaction' })),
  )))
  assert.equal((await run(root, [unit, ...stories])).pass, true)
})

test('derives interaction browser engines from the contract rather than evidence filenames', async () => {
  const root = await setup([
    { id: 'button-interaction', kind: 'interaction', applicable: true, file: 'src/button.stories.tsx', test: 'Button interaction', story: 'Primary' },
  ])
  const projectless = join(root, 'renamed-vitest.json')
  await writeFile(projectless, JSON.stringify(vitestReport({ file: join(root, 'src/button.stories.tsx'), title: 'Button interaction' })))
  const report = await run(root, [projectless])
  assert.equal(report.pass, false)
  assert.deepEqual(report.errors, [
    'Button/button-interaction: missing required project chromium',
    'Button/button-interaction: missing required project firefox',
    'Button/button-interaction: missing required project webkit',
  ])
})

test('requires Playwright evidence and four projects for browser-family checks in an owner browser file', async () => {
  const root = await setup([
    { id: 'button-axe', kind: 'axe', applicable: true, file: 'tests/design-system/accessibility.spec.ts', test: 'Button axe', story: 'Primary' },
  ])
  const browserFile = join(root, 'tests/design-system/accessibility.spec.ts')
  await writeFile(browserFile, 'test("Button axe", () => {})')
  const wrongRunner = ['chromium', 'firefox', 'webkit'].map((engine) => join(root, `design-system-storybook-${engine}.json`))
  await Promise.all(wrongRunner.map((evidence) => writeFile(
    evidence,
    JSON.stringify(vitestReport({ file: browserFile, title: 'Button axe' })),
  )))
  const rejected = await run(root, wrongRunner)
  assert.equal(rejected.pass, false)
  assert.deepEqual(rejected.errors, [
    'Button/button-axe: evidence runner vitest is incompatible with browser-family check; expected playwright',
    'Button/button-axe: missing required project chromium',
    'Button/button-axe: missing required project firefox',
    'Button/button-axe: missing required project webkit',
    'Button/button-axe: missing required project chromium-coarse-pointer',
  ])

  const playwright = join(root, 'playwright-owner-browser.json')
  await writeFile(playwright, JSON.stringify(playwrightSpec({
    file: browserFile,
    title: 'Button axe',
  })))
  assert.equal((await run(root, [playwright])).pass, true, 'owner browser files retain exact path matching')
})

test('rejects malformed checks before considering evidence', async () => {
  const root = await setup([{ id: ' ', kind: 'unknown', test: '', applicable: undefined }])
  const report = await run(root, [])
  assert.equal(report.pass, false)
  assert.match(report.errors.join('\n'), /id must be non-empty/)
  assert.match(report.errors.join('\n'), /kind is invalid/)
  assert.match(report.errors.join('\n'), /applicable must be boolean/)
})

test('rejects an interaction claim backed only by a render-only story', async () => {
  const root = await setup([{ id: 'button-interaction', kind: 'interaction', applicable: true, file: 'src/button.stories.tsx', test: 'Button interaction', story: 'Primary' }])
  await writeFile(join(root, 'src/button.stories.tsx'), 'export const Primary = {}')
  const report = await run(root, [])
  assert.match(report.errors.join('\n'), /must declare an explicit play function/)
})

// The checked-in manifest schema is the oracle; passing evidence must never
// excuse absent ownership, unknown fields or duplicate public export names.
test('rejects every missing required manifest field despite passing execution evidence', async () => {
  const root = await setup([{ id: 'button-browser', kind: 'browser', applicable: true, file: 'tests/design-system/browser.spec.ts', test: 'Button browser', story: 'Primary' }])
  const file = join(root, 'design-system/manifests/button.json')
  const original = JSON.parse(await readFile(file, 'utf8'))
  const evidence = join(root, 'playwright.json')
  await writeFile(evidence, JSON.stringify(playwrightSpec({})))
  assert.equal((await run(root, [evidence])).pass, true)
  for (const field of ['exports', 'category', 'source', 'documentation', 'owner', 'themes', 'variants', 'sizes', 'states']) {
    const invalid = { ...original }; delete invalid[field]
    await writeFile(file, JSON.stringify(invalid))
    const report = await run(root, [evidence])
    assert.equal(report.pass, false, `missing ${field} must fail`)
    assert.match(report.errors.join('\n'), new RegExp(field))
  }
})

test('rejects unknown properties, duplicate arrays and malformed nested schema shapes', async () => {
  const root = await setup([{ id: 'button-browser', kind: 'browser', applicable: true, file: 'tests/design-system/browser.spec.ts', test: 'Button browser', story: 'Primary' }])
  const file = join(root, 'design-system/manifests/button.json')
  const original = JSON.parse(await readFile(file, 'utf8'))
  const evidence = join(root, 'playwright.json')
  await writeFile(evidence, JSON.stringify(playwrightSpec({})))
  for (const patch of [{ unexpected: true }, { exports: ['Button', 'Button'] }, { category: 'domain' }, { themes: [] }, { stories: {} }, { checks: null }, { checks: [{ ...original.checks[0], unknown: true }] }]) {
    await writeFile(file, JSON.stringify({ ...original, ...patch }))
    const report = await run(root, [evidence])
    assert.equal(report.pass, false, JSON.stringify(patch))
    assert.match(report.errors.join('\n'), /schema/)
  }
})
