import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test, { after } from 'node:test'
import { inspectPublishedPackage, localCssAssetPaths, publishedPackageAllowlist } from './package-boundary.mjs'

const root = resolve(import.meta.dirname, '../..')
const catalog = JSON.parse(readFileSync(resolve(root, 'design-system/catalog.json'), 'utf8'))
const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
const output = resolve(root, 'dist/lib')
const fixtureRoots = []
// Temporary fixture folders live under dist/, which a fresh checkout lacks.
mkdirSync(resolve(root, 'dist/design-system-baseline'), { recursive: true })

after(() => {
  for (const directory of fixtureRoots) rmSync(directory, { recursive: true, force: true })
})

test('rejects a dummy screen declaration leaked into the published package', () => {
  const files = [
    'index.js',
    'index.cjs',
    'index.d.ts',
    'styles.css',
    'components/ui/button.d.ts',
    'components/ui/alert-dialog.d.ts',
    'components/screens/Board.d.ts',
  ]
  const result = inspectPublishedPackage({ root, catalog, pkg, output, files })
  assert.equal(result.allowed.has('components/screens/Board.d.ts'), false, 'allowlist must not include application screens')
  assert.ok(result.unexpected.includes('components/screens/Board.d.ts'), 'Board.d.ts must be rejected as unpublished package content')
})

test('does not bless the entire components/ui directory', () => {
  const files = [
    'index.d.ts',
    'components/ui/button.d.ts',
    'components/ui/AutoSaveIndicator.d.ts',
    'components/ui/brand-disclaimer.d.ts',
  ]
  const result = inspectPublishedPackage({ root, catalog, pkg, output, files })
  assert.equal(result.allowed.has('components/ui/button.d.ts'), true)
  assert.equal(result.allowed.has('components/ui/AutoSaveIndicator.d.ts'), false, 'domain widgets stay private')
  assert.equal(result.allowed.has('components/ui/brand-disclaimer.d.ts'), false, 'unexported foundations stay private')
  assert.ok(result.unexpected.includes('components/ui/AutoSaveIndicator.d.ts'))
  assert.ok(result.unexpected.includes('components/ui/brand-disclaimer.d.ts'))
})

test('allows unpublished primitives that the public graph actually depends on', () => {
  const result = publishedPackageAllowlist({ root, catalog, pkg, output })
  assert.equal(result.allowed.has('components/ui/alert-dialog.d.ts'), true, 'ConfirmDialog depends on alert-dialog')
  assert.equal(result.allowed.has('index.js'), true)
  assert.equal(result.allowed.has('index.cjs'), true)
  assert.equal(result.allowed.has('index.d.ts'), true)
  assert.equal(result.allowed.has('styles.css'), true)
})

test('CSS allowlist includes only referenced local fonts', () => {
  const directory = mkdtempSync(resolve(root, 'dist/design-system-baseline/package-boundary-css-'))
  fixtureRoots.push(directory)
  const dist = resolve(directory, 'dist/lib')
  mkdirSync(resolve(dist, 'fonts'), { recursive: true })
  const stylesheet = resolve(dist, 'styles.css')
  writeFileSync(stylesheet, '@font-face { src: url("./fonts/inter.woff2"); }')
  writeFileSync(resolve(dist, 'fonts/inter.woff2'), 'woff2')
  writeFileSync(resolve(dist, 'fonts/extra.woff2'), 'woff2')
  writeFileSync(resolve(dist, 'private-data.bin'), 'private')
  assert.deepEqual(localCssAssetPaths(readFileSync(stylesheet, 'utf8'), stylesheet, dist), ['fonts/inter.woff2'])
  assert.deepEqual(
    localCssAssetPaths('@font-face { src: url("data:font/woff2;base64,AA"); }', stylesheet, dist),
    [],
  )
  assert.deepEqual(
    localCssAssetPaths('body { background: url("./private-data.bin"); }', stylesheet, dist),
    [],
    'a CSS reference must not bless an arbitrary package file',
  )
  assert.deepEqual(
    localCssAssetPaths('@font-face { src: url("./fonts/legacy.woff"); }', stylesheet, dist),
    [],
    'only the reviewed WOFF2 package font format is publishable',
  )
  const result = inspectPublishedPackage({
    root,
    catalog,
    pkg,
    output: dist,
    files: ['index.js', 'index.cjs', 'index.d.ts', 'styles.css', 'fonts/inter.woff2', 'fonts/extra.woff2', 'private-data.bin'],
    stylesheet,
  })
  assert.equal(result.allowed.has('fonts/inter.woff2'), true)
  assert.equal(result.allowed.has('fonts/extra.woff2'), false)
  assert.ok(result.unexpected.includes('fonts/extra.woff2'))
  assert.ok(result.unexpected.includes('private-data.bin'))
})

test('rejects an unreviewed helper placed in a broadly named source directory', () => {
  const directory = mkdtempSync(resolve(root, 'dist/design-system-baseline/package-boundary-helper-'))
  fixtureRoots.push(directory)
  mkdirSync(resolve(directory, 'src/components/ui'), { recursive: true })
  mkdirSync(resolve(directory, 'src/lib'), { recursive: true })
  writeFileSync(resolve(directory, 'src/index.ts'), "export { Button } from './components/ui/button'\n")
  writeFileSync(resolve(directory, 'src/components/ui/button.ts'), "import { domainValue } from '../../lib/domain'\nexport const Button = domainValue\n")
  writeFileSync(resolve(directory, 'src/lib/domain.ts'), 'export const domainValue = 1\n')
  const fixtureCatalog = { entries: [{ source: 'src/components/ui/button.ts', classification: 'primitive', publicExports: ['Button'], publicTypes: [] }] }
  const fixturePackage = { main: './dist/lib/index.cjs', module: './dist/lib/index.js', types: './dist/lib/index.d.ts', exports: { './styles.css': './dist/lib/styles.css' } }
  const result = publishedPackageAllowlist({ root: directory, catalog: fixtureCatalog, pkg: fixturePackage, output: resolve(directory, 'dist/lib') })
  assert.ok(result.leaks.includes('src/lib/domain.ts'), 'directory naming alone must not classify a helper as publishable')
})

test('does not claim that entrypoint timestamps prove distribution freshness', () => {
  const result = inspectPublishedPackage({ root, catalog, pkg, output, files: [] })
  assert.equal(Object.hasOwn(result, 'stale'), false)
})
