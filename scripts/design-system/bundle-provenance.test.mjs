import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import test from 'node:test'
import { build } from 'vite'
import { designSystemProductionProvenance } from './bundle-provenance.mjs'

// The delivery contract requires provenance hashes to describe final emitted bytes,
// including changes by later build plugins, and forbids story modules entirely.
async function fixture(t, { story = false } = {}) {
  const parent = resolve('dist/design-system-baseline')
  await mkdir(parent, { recursive: true })
  const root = await mkdtemp(resolve(parent, 'provenance-test-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const entry = story ? 'fixture.stories.js' : 'entry.js'
  await writeFile(resolve(root, entry), 'console.log("production fixture")\n')
  const evidencePath = resolve(root, 'provenance.json')
  const outDir = resolve(root, 'dist/spa')
  const run = () => build({
    configFile: false, root, publicDir: false, logLevel: 'silent',
    build: { outDir, minify: false, rolldownOptions: { input: resolve(root, entry) } },
    plugins: [designSystemProductionProvenance({ evidencePath }), {
      name: 'fixture-final-transform',
      buildStart() { this.emitFile({ type: 'asset', fileName: 'worker.js', source: 'self.onmessage=()=>{};' }) },
      generateBundle(_options, bundle) {
        for (const output of Object.values(bundle)) {
          if (output.type === 'chunk') output.code += '\nconsole.log("late chunk transform");\n'
          else if (output.fileName === 'worker.js') output.source += '\n/* late worker transform */\n'
        }
      },
    }],
  })
  return { run, outDir, evidencePath }
}

for (const [label, property] of [['chunks', 'chunks'], ['JavaScript assets', 'javascriptAssets']]) {
  test(`provenance hashes final ${label} after later plugin transformations`, async (t) => {
    const f = await fixture(t)
    await f.run()
    const report = JSON.parse(await readFile(f.evidencePath, 'utf8'))
    assert.equal(report.clean, true)
    assert.equal(report[property].length, 1)
    for (const output of report[property]) {
      const bytes = await readFile(resolve(f.outDir, output.file))
      assert.match(bytes.toString(), /late (?:chunk|worker) transform/)
      assert.equal(output.sha256, createHash('sha256').update(bytes).digest('hex'))
    }
  })
}

test('production build rejects a story module even when output filename is ordinary JavaScript', async (t) => {
  const f = await fixture(t, { story: true })
  await assert.rejects(f.run, /Storybook module entered production bundle/)
})
