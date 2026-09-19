import assert from 'node:assert/strict'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import test from 'node:test'

test('Playwright artifact cleanup cannot delete sibling evidence reports', async (t) => {
  const source = await readFile(resolve('playwright.design-system.config.ts'), 'utf8')
  assert.match(source, /outputDir:\s*['"]test-results\/design-system-browser-artifacts['"]/)
  assert.match(source, /outputFile:\s*['"]test-results\/design-system-browser\.json['"]/)

  const parent = resolve('dist/design-system-baseline')
  await mkdir(parent, { recursive: true })
  const root = await mkdtemp(resolve(parent, 'playwright-config-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const evidence = resolve(root, 'test-results/design-system-browser.json')
  const artifactDirectory = resolve(root, 'test-results/design-system-browser-artifacts')
  await mkdir(artifactDirectory, { recursive: true })
  await writeFile(evidence, '{"preserved":true}')
  await writeFile(resolve(artifactDirectory, 'trace.zip'), 'temporary artifact')
  try {
    // Playwright empties only outputDir at run start. The evidence reporter is
    // deliberately its sibling and therefore survives that cleanup boundary.
    await rm(artifactDirectory, { recursive: true, force: true })
    assert.equal(await readFile(evidence, 'utf8'), '{"preserved":true}')
  } finally {
    await rm(evidence, { force: true })
    await rm(artifactDirectory, { recursive: true, force: true })
  }
})
