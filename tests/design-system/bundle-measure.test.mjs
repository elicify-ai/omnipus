import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import test, { after } from 'node:test'
import { promisify } from 'node:util'
import { gzipSync } from 'node:zlib'

import { measureProductionBundle } from '../../scripts/design-system/bundle-measure.mjs'
import { compareProductionBundles } from '../../scripts/design-system/bundle-audit.mjs'
import { designSystemProductionProvenance } from '../../scripts/design-system/bundle-provenance.mjs'

const runMeasureCli = promisify(execFile)

async function invokeMeasureCli(args) {
  return runMeasureCli(process.execPath, [resolve('scripts/design-system/bundle-measure.mjs'), ...args])
}

const fixtureRoots = []
after(async () => { await Promise.all(fixtureRoots.map((root) => rm(root, { recursive: true, force: true }))) })

async function fixture(files) {
  const parent = resolve('dist/design-system-baseline')
  await mkdir(parent, { recursive: true })
  const root = await mkdtemp(join(parent, 'bundle-measure-'))
  fixtureRoots.push(root)
  for (const [path, contents] of Object.entries(files)) {
    const target = join(root, path)
    await mkdir(join(target, '..'), { recursive: true })
    await writeFile(target, contents)
  }
  return root
}

test('measures recursively imported initial JavaScript and linked CSS with gzip level 9', async () => {
  const files = {
    'index.html': '<link rel="stylesheet" href="/assets/app.css"><link rel="modulepreload" href="/assets/preloaded.js"><script type="module" src="/assets/app.js"></script>',
    'assets/app.css': '@import "./shared.css"; body { color: white; }',
    'assets/shared.css': 'button { color: inherit; }',
    'assets/app.js': 'import{shared}from"./shared.js";import("./lazy.js");const fake=`import "./fake.js"`;',
    'assets/shared.js': 'export const shared = true',
    'assets/preloaded.js': 'export const preloaded = true',
    'assets/lazy.js': 'export const lazy = true',
    'asset.bin': 'other embedded bytes',
  }
  const dist = await fixture(files)

  const report = await measureProductionBundle({ dist, revision: 'baseline' })

  assert.deepEqual(report.initial.files, ['assets/app.css', 'assets/app.js', 'assets/preloaded.js', 'assets/shared.css', 'assets/shared.js'])
  assert.equal(report.initial.javascript.rawBytes, Buffer.byteLength(files['assets/app.js']) + Buffer.byteLength(files['assets/preloaded.js']) + Buffer.byteLength(files['assets/shared.js']))
  assert.equal(report.initial.javascript.gzipBytes, gzipSync(files['assets/app.js'], { level: 9 }).byteLength + gzipSync(files['assets/preloaded.js'], { level: 9 }).byteLength + gzipSync(files['assets/shared.js'], { level: 9 }).byteLength)
  assert.equal(report.initial.css.rawBytes, Buffer.byteLength(files['assets/app.css']) + Buffer.byteLength(files['assets/shared.css']))
  assert.equal(report.total.rawBytes, Object.values(files).reduce((sum, value) => sum + Buffer.byteLength(value), 0))
  assert.ok(report.assets.every((asset) => /^[a-f0-9]{64}$/.test(asset.sha256)))
})

test('fails when an initial asset reference escapes or is missing', async () => {
  const escaped = await fixture({ 'index.html': '<script type="module" src="../outside.js"></script>' })
  await assert.rejects(() => measureProductionBundle({ dist: escaped }), /escapes distribution root/)

  const missing = await fixture({ 'index.html': '<script type="module" src="/assets/missing.js"></script>' })
  await assert.rejects(() => measureProductionBundle({ dist: missing }), /missing initial asset/)
})

test('reports Storybook-like output evidence without treating ordinary prose as module provenance', async () => {
  const dist = await fixture({
    'index.html': '<script type="module" src="/assets/app.js"></script>',
    'assets/app.js': 'console.log("storybook is useful prose")',
    'assets/preview.js': 'const moduleId = "@storybook/react-vite"',
    'Button.stories.js': 'export const Primary = {}',
  })

  const report = await measureProductionBundle({ dist })

  assert.deepEqual(report.storybook.outputPathMatches, ['Button.stories.js'])
  assert.deepEqual(report.storybook.contentMatches.map((match) => match.path), ['assets/preview.js'])
  assert.equal(report.storybook.clean, false)
})

test('enforces inclusive initial gzip and total raw byte budgets', () => {
  const baseline = { initial: { gzipBytes: 100 }, total: { rawBytes: 1_000 }, storybook: { clean: true }, assets: [] }
  const atLimit = { initial: { gzipBytes: 25_700 }, total: { rawBytes: 257_000 }, storybook: { clean: true }, assets: [{ path: 'assets/app.js', sha256: 'candidate-hash' }] }
  const provenance = { clean: true, chunks: [{ file: 'assets/app.js', sha256: 'candidate-hash', modules: ['/repo/src/main.tsx'] }], javascriptAssets: [] }
  assert.equal(compareProductionBundles(baseline, atLimit, provenance).pass, true)

  const initialOver = structuredClone(atLimit)
  initialOver.initial.gzipBytes += 1
  assert.equal(compareProductionBundles(baseline, initialOver, provenance).pass, false)

  const totalOver = structuredClone(atLimit)
  totalOver.total.rawBytes += 1
  assert.equal(compareProductionBundles(baseline, totalOver, provenance).pass, false)

  const contaminated = structuredClone(baseline)
  contaminated.storybook.clean = false
  assert.equal(compareProductionBundles(baseline, contaminated, provenance).pass, false)
  assert.equal(compareProductionBundles(baseline, baseline, null).pass, false)
  assert.equal(compareProductionBundles(baseline, atLimit, { ...provenance, chunks: [{ ...provenance.chunks[0], sha256: 'stale-hash' }] }).pass, false)
  const unaccounted = structuredClone(atLimit)
  unaccounted.assets.push({ path: 'pdfjs/pdf.worker.min.mjs', sha256: 'worker-hash' })
  assert.equal(compareProductionBundles(baseline, unaccounted, provenance).pass, false)
  assert.equal(compareProductionBundles(baseline, unaccounted, { ...provenance, javascriptAssets: [{ file: 'pdfjs/pdf.worker.min.mjs', sha256: 'worker-hash' }] }).pass, true)
})

test('production provenance rejects Storybook module metadata at the output hook', () => {
  const plugin = designSystemProductionProvenance({ evidencePath: 'unused.json' })
  plugin.configResolved({ build: { outDir: '/repo/dist/spa' } })
  assert.throws(() => plugin.writeBundle.call({ error(message) { throw new Error(message) } }, {}, {
    'app.js': { type: 'chunk', fileName: 'app.js', code: 'production code', modules: { '/repo/src/main.tsx': {}, '/repo/node_modules/@storybook/react/index.js': {} } },
  }), /Storybook module entered production bundle/)
})

test('CLI writes reports exclusively, creates parent directories and requires --force to replace', async () => {
  const dist = await fixture({
    'index.html': '<script type="module" src="/app.js"></script>',
    'app.js': 'console.log("production")',
  })
  const output = join(dist, 'reports/nested/production-candidate.json')

  const first = await invokeMeasureCli(['--dist', dist, '--output', output, '--revision', 'first'])
  const summary = JSON.parse(first.stdout.trim())
  assert.equal(summary.output, output)
  assert.equal(typeof summary.initialGzipBytes, 'number')
  assert.equal(typeof summary.totalRawBytes, 'number')
  assert.equal(summary.storybookClean, true)
  assert.equal(JSON.parse(await readFile(output, 'utf8')).revision, 'first')

  await assert.rejects(
    () => invokeMeasureCli(['--dist', dist, '--output', output, '--revision', 'second']),
    (error) => error.code === 1 && /already exists/.test(error.stderr) && /--force/.test(error.stderr),
  )
  assert.equal(JSON.parse(await readFile(output, 'utf8')).revision, 'first')

  await invokeMeasureCli(['--dist', dist, '--output', output, '--revision', 'forced', '--force'])
  assert.equal(JSON.parse(await readFile(output, 'utf8')).revision, 'forced')
})
