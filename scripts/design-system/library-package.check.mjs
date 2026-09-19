import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import test from 'node:test'
import ts from 'typescript'
import { inspectPublishedPackage } from './package-boundary.mjs'

// D8 package contract: inspect real build output, independently of source checks.
// Run after build:lib. Asset boundary is an allowlist from the reviewed catalog graph.
const root = resolve(import.meta.dirname, '../..')
const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
const output = resolve(root, 'dist/lib')

test('every advertised package entry exists in the built distribution', () => {
  for (const field of ['main', 'module', 'types']) {
    assert.equal(existsSync(resolve(root, pkg[field])), true, `missing package ${field}: ${pkg[field]}`)
  }
})

test('the package publishes standalone component CSS without the application page lock', () => {
  const stylesheet = pkg.exports['./styles.css']
  assert.equal(typeof stylesheet, 'string', 'styles.css must be an explicit package export')
  assert.equal(existsSync(resolve(root, stylesheet)), true, 'exported stylesheet must exist')
  const css = readFileSync(resolve(root, stylesheet), 'utf8')
  assert.match(css, /--primitive-color-gold\s*:/, 'generated foundations must ship with controls')
  assert.doesNotMatch(css, /data-app-shell/, 'application shell rules must remain private')
  assert.doesNotMatch(css, /body\s*\{[^}]*position:\s*fixed/, 'library must not lock the host page')
})

test('library distribution only publishes the catalog declaration graph and advertised assets', () => {
  const catalog = JSON.parse(readFileSync(resolve(root, 'design-system/catalog.json'), 'utf8'))
  const result = inspectPublishedPackage({ root, catalog, pkg, output })
  assert.deepEqual(result.errors, [], result.errors.join('\n'))
  assert.deepEqual(result.leaks, [], 'public graph must not reach domain or application sources')
  assert.deepEqual(result.unexpected, [], 'app content must not ship inside the reusable package')
  assert.deepEqual(result.missingPublic, [], 'public catalog declarations must be published')
  assert.equal(result.published.includes('index.d.ts'), true, 'public declarations must be rooted at index.d.ts')
})

test('ESM and CommonJS expose reusable controls and exclude application entry points', async () => {
  const esm = await import(pathToFileURL(resolve(root, pkg.module)).href)
  const cjs = createRequire(import.meta.url)(resolve(root, pkg.main))
  for (const api of [esm, cjs]) {
    for (const name of ['Button', 'Card', 'Input', 'Dialog', 'tokens', 'resolvedTokens']) {
      assert.equal(Object.hasOwn(api, name), true, `missing runtime export ${name}`)
    }
    for (const name of ['AppShell', 'Sidebar', 'useSidebarStore', 'ModelSelector']) {
      assert.equal(Object.hasOwn(api, name), false, `application export ${name}`)
    }
  }
})

// The reviewed catalog is the delivery contract; a partial list of sample controls
// cannot prove that every approved public API survived bundling and declaration emit.
test('both runtime formats implement the complete catalog contract exactly', async () => {
  const catalog = JSON.parse(readFileSync(resolve(root, 'design-system/catalog.json'), 'utf8'))
  const expected = catalog.entries.flatMap((entry) => entry.publicExports).sort()
  const esm = await import(pathToFileURL(resolve(root, pkg.module)).href)
  const cjs = createRequire(import.meta.url)(resolve(root, pkg.main))
  assert.deepEqual(Object.keys(esm).sort(), expected, 'ESM export set differs from approved catalog')
  assert.deepEqual(Object.keys(cjs).sort(), expected, 'CommonJS export set differs from approved catalog')
})

test('emitted declarations expose every catalog value and type without extra exports', () => {
  const catalog = JSON.parse(readFileSync(resolve(root, 'design-system/catalog.json'), 'utf8'))
  const expected = catalog.entries.flatMap((entry) => [...entry.publicExports, ...entry.publicTypes]).sort()
  const declaration = resolve(root, pkg.types)
  const program = ts.createProgram([declaration], { skipLibCheck: true, moduleResolution: ts.ModuleResolutionKind.Bundler, module: ts.ModuleKind.ESNext })
  const checker = program.getTypeChecker()
  const source = program.getSourceFile(declaration)
  assert.ok(source, 'declaration entry must parse')
  const symbol = checker.getSymbolAtLocation(source)
  assert.ok(symbol, 'declaration entry must be a module')
  const exports = checker.getExportsOfModule(symbol)
  assert.deepEqual(exports.map((item) => item.name).sort(), expected)
  for (const item of exports) {
    const resolved = item.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(item) : item
    assert.ok(resolved.declarations?.length, `unresolved emitted declaration: ${item.name}`)
    for (const node of resolved.declarations) {
      assert.ok(node.getSourceFile().fileName.startsWith(output + '/'), `public declaration escapes distribution: ${item.name}`)
    }
  }
})

test('published declarations typecheck for a strict React consumer', () => {
  const program = ts.createProgram([resolve(root, pkg.types)], {
    strict: true,
    skipLibCheck: false,
    noEmit: true,
    target: ts.ScriptTarget.ES2022,
    jsx: ts.JsxEmit.ReactJSX,
    moduleResolution: ts.ModuleResolutionKind.Bundler,
    module: ts.ModuleKind.ESNext,
    types: ['react', 'react-dom'],
  })
  const diagnostics = ts.getPreEmitDiagnostics(program)
  assert.equal(diagnostics.length, 0, ts.formatDiagnosticsWithColorAndContext(diagnostics, {
    getCanonicalFileName: (name) => name,
    getCurrentDirectory: () => root,
    getNewLine: () => '\n',
  }))
})

test('every CSS asset reference is embedded or resolves inside the published distribution', () => {
  const stylesheet = resolve(root, pkg.exports['./styles.css'])
  const css = readFileSync(stylesheet, 'utf8')
  const urls = [...css.matchAll(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)/g)]
  assert.ok(urls.length > 0, 'font references must be inspected, not silently omitted')
  for (const match of urls) {
    const url = (match[1] ?? match[2] ?? match[3]).trim()
    if (url.startsWith('data:') || url.startsWith('#')) continue
    assert.doesNotMatch(url, /^(?:[a-z]+:|\/\/)/i, 'published CSS must not depend on remote assets')
    const target = resolve(stylesheet, '..', decodeURIComponent(url.split(/[?#]/)[0]))
    assert.ok(target.startsWith(output + '/'), `CSS asset escapes the package: ${url}`)
    assert.equal(existsSync(target), true, `missing published CSS asset: ${url}`)
  }
})
