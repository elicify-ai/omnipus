import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve, relative } from 'node:path'
import test from 'node:test'
import ts from 'typescript'
import postcss from 'postcss'

// D8: the source entry selected by the library build is curated and app-independent.
// This is an import/export structure contract, not a substitute for UI tests.
const root = resolve(import.meta.dirname, '../..')
const entry = resolve(root, 'src/index.ts')
const options = ts.parseJsonConfigFileContent(
  ts.readConfigFile(resolve(root, 'tsconfig.app.json'), ts.sys.readFile).config,
  ts.sys,
  root,
).options

function source(file) {
  return ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true)
}

function exportsOf(file) {
  const names = []
  for (const statement of source(file).statements) {
    if (!ts.isExportDeclaration(statement)) {
      assert.equal(statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) ?? false, false, 'public entry must re-export named contracts, not declare implementations')
      continue
    }
    assert.ok(statement.exportClause && ts.isNamedExports(statement.exportClause), 'public entry requires explicit named exports')
    names.push(...statement.exportClause.elements.map((item) => item.name.text))
  }
  return names
}

function localGraph(file, visited = new Set()) {
  if (visited.has(file)) return visited
  visited.add(file)
  if (file.endsWith('.css')) {
    postcss.parse(readFileSync(file, 'utf8')).walkAtRules('import', (rule) => {
      const specifier = rule.params.match(/^['"]([^'"]+)['"]/)?.[1]
      assert.ok(specifier, `unanalyzable CSS import in ${relative(root, file)}`)
      if (specifier.startsWith('.')) localGraph(resolve(file, '..', specifier), visited)
      else assert.ok(specifier === 'tailwindcss' || specifier.startsWith('@fontsource/'), `unregistered library CSS dependency: ${specifier}`)
    })
    return visited
  }
  function visit(node) {
    let specifier
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) specifier = node.moduleSpecifier
    if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || node.expression.getText() === 'require')) {
      assert.ok(node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0]), `unanalyzable import in ${relative(root, file)}`)
      specifier = node.arguments[0]
    }
    if (specifier && ts.isStringLiteral(specifier)) {
      const name = specifier.text
      if (name.startsWith('.') || name.startsWith('@/')) {
        if (name.endsWith('.css')) {
          assert.ok(name.startsWith('.'), 'stylesheet imports must be relative')
          localGraph(resolve(file, '..', name), visited)
          return
        }
        const resolved = ts.resolveModuleName(name, file, options, ts.sys).resolvedModule
        assert.ok(resolved, `unresolved library dependency ${name} from ${relative(root, file)}`)
        const target = resolve(resolved.resolvedFileName)
        assert.ok(relative(root, target).replaceAll('\\', '/').startsWith('src/'), `library dependency escapes source boundary: ${target}`)
        localGraph(target, visited)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source(file))
  return visited
}

test('public package retains its core controls and excludes application exports', () => {
  const names = exportsOf(entry)
  for (const expected of ['Button', 'Card', 'Input', 'Dialog', 'cn']) assert.ok(names.includes(expected), `missing ${expected}`)
  for (const forbidden of ['AppShell', 'Sidebar', 'useSidebarStore', 'ModelSelector', 'AutoSaveIndicator', 'RestartConfirmDialog']) {
    assert.equal(names.includes(forbidden), false, `${forbidden} is application-owned`)
  }
  assert.equal(new Set(names).size, names.length, 'duplicate public export')
})

test('public library dependency graph cannot reach application state or domain code', () => {
  const paths = [...localGraph(entry)].map((file) => relative(root, file).replaceAll('\\', '/'))
  assert.ok(paths.includes('src/components/ui/button.tsx'), 'graph must actually traverse the public controls')
  // This finite source boundary is independent of what the entry happens to export.
  // New shared modules require a deliberate boundary update and catalog classification.
  const controls = new Set([
    'accordion', 'alert-dialog', 'avatar', 'badge', 'button', 'calendar', 'card', 'checkbox',
    'command', 'date-picker', 'date-time-picker', 'dialog', 'dropdown-menu',
    'input', 'label', 'popover', 'progress', 'select', 'separator', 'sheet',
    'slider', 'smart-select', 'switch', 'table', 'tabs', 'textarea',
    'icon-button', 'field', 'confirm-dialog', 'skeleton', 'collection-state',
    'job-status', 'empty-state', 'error-state', 'query-error-state',
  ].map((name) => `src/components/ui/${name}.tsx`))
  const foundations = new Set(['src/index.ts', 'src/lib/utils.ts', 'src/design-system/tokens.ts', 'src/design-system/status.ts', 'src/design-system/use-loading-visibility.ts', 'src/styles/library.css', 'src/styles/tokens.generated.css', 'src/styles/tokens.theme.generated.css'])
  const forbidden = paths.filter((file) => !controls.has(file) && !foundations.has(file))
  assert.deepEqual(forbidden, [], 'application dependencies must remain outside the package entry')
})


test('Vite library configuration selects the guarded source entry', () => {
  const entries = []
  function visit(node) {
    if (ts.isPropertyAssignment(node) && node.name.getText() === 'entry') {
      const literals = []
      function collect(child) {
        if (ts.isStringLiteral(child)) literals.push(child.text)
        ts.forEachChild(child, collect)
      }
      collect(node.initializer)
      entries.push(literals)
    }
    ts.forEachChild(node, visit)
  }
  visit(source(resolve(root, 'vite.lib.config.ts')))
  assert.deepEqual(entries, [['src/index.ts']], 'library build entry changed; update the public boundary deliberately')
})

test('public entry permits only the reviewed stylesheet import and named re-exports', () => {
  const parsed = source(entry)
  assert.deepEqual(parsed.parseDiagnostics, [], 'public entry must parse without recovery')
  let stylesheetImports = 0
  for (const statement of parsed.statements) {
    if (ts.isImportDeclaration(statement)) {
      assert.equal(statement.importClause, undefined, 'public entry must not execute imported bindings')
      assert.ok(ts.isStringLiteral(statement.moduleSpecifier))
      assert.equal(statement.moduleSpecifier.text, './styles/library.css', 'unapproved side-effect import')
      stylesheetImports += 1
    } else {
      assert.ok(ts.isExportDeclaration(statement), 'public entry forbids executable statements and default exports')
      assert.ok(statement.exportClause && ts.isNamedExports(statement.exportClause))
      assert.ok(statement.moduleSpecifier && ts.isStringLiteral(statement.moduleSpecifier), 'exports must name a source module')
    }
  }
  assert.equal(stylesheetImports, 1)
})

test('library CSS discovers only the reviewed component catalog, never application or story sources', () => {
  const catalog = JSON.parse(readFileSync(resolve(root, 'design-system/catalog.json'), 'utf8'))
  const expected = catalog.entries
    .filter((item) => item.source.startsWith('src/components/ui/') && ['primitive', 'composite'].includes(item.classification))
    .map((item) => resolve(root, item.source)).sort()
  const cssFile = resolve(root, 'src/styles/library.css')
  const parsed = postcss.parse(readFileSync(cssFile, 'utf8'))
  const sources = []
  let tailwindImports = 0
  parsed.walkAtRules('import', (rule) => {
    if (/^['"]tailwindcss['"]/.test(rule.params)) {
      assert.match(rule.params, /^['"]tailwindcss['"]\s+source\(none\)$/, 'automatic source discovery must remain disabled')
      tailwindImports += 1
    }
  })
  assert.equal(tailwindImports, 1)
  parsed.walkAtRules('source', (rule) => {
    const literal = rule.params.match(/^['"]([^'"]+)['"]$/)?.[1]
    assert.ok(literal, 'source boundaries must be explicit file literals')
    sources.push(resolve(cssFile, '..', literal))
  })
  assert.deepEqual(sources.sort(), expected)
  for (const name of ['tokens.generated.css', 'tokens.theme.generated.css']) {
    postcss.parse(readFileSync(resolve(root, 'src/styles', name), 'utf8')).walkAtRules('source', () => {
      assert.fail('generated tokens must not broaden utility discovery')
    })
  }
})

test('public controls use only approved external foundation dependencies', () => {
  const approved = new Set([
    'react', 'react-dom', '@phosphor-icons/react', 'react-day-picker', 'cmdk',
    'class-variance-authority', 'clsx', 'tailwind-merge',
    ...['accordion', 'alert-dialog', 'avatar', 'checkbox', 'dialog', 'dropdown-menu',
      'label', 'popover', 'progress', 'select', 'separator', 'slider', 'slot', 'switch', 'tabs']
      .map((name) => `@radix-ui/react-${name}`),
  ])
  const metadata = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
  const declared = new Set(Object.keys({ ...metadata.dependencies, ...metadata.peerDependencies }))
  for (const file of localGraph(entry)) {
    if (file.endsWith('.css')) continue
    function visit(node) {
      const specifier = ts.isImportDeclaration(node) || ts.isExportDeclaration(node)
        ? node.moduleSpecifier
        : ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || node.expression.getText() === 'require')
          ? node.arguments[0]
          : undefined
      if (specifier && ts.isStringLiteral(specifier) && !specifier.text.startsWith('.') && !specifier.text.startsWith('@/')) {
        const name = specifier.text.split('/').slice(0, specifier.text.startsWith('@') ? 2 : 1).join('/')
        assert.ok(approved.has(name), `unapproved public foundation dependency: ${specifier.text}`)
        assert.ok(declared.has(name), `undeclared public foundation dependency: ${specifier.text}`)
      }
      ts.forEachChild(node, visit)
    }
    visit(source(file))
  }
})
