import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { relative, resolve } from 'node:path'
import ts from 'typescript'

const ALLOWED_CLASSIFICATIONS = new Set(['primitive', 'composite', 'foundations'])
const REVIEWED_HELPER_SOURCES = new Set([
  'src/lib/utils.ts',
  'src/design-system/tokens.ts',
  'src/design-system/status.ts',
  'src/design-system/use-loading-visibility.ts',
])
const REVIEWED_LOCAL_ASSET = /^fonts\/[^/]+\.woff2$/

function posix(path) {
  return path.replaceAll('\\', '/')
}

export function listPublishedFiles(output) {
  if (!existsSync(output)) return []
  return readdirSync(output, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => posix(resolve(entry.parentPath, entry.name).slice(output.length + 1)))
    .sort()
}

export function localCssAssetPaths(css, stylesheet, output) {
  const assets = []
  const root = output.endsWith('/') ? output : `${output}/`
  for (const match of css.matchAll(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)/g)) {
    const url = (match[1] ?? match[2] ?? match[3]).trim()
    if (!url || url.startsWith('data:') || url.startsWith('#')) continue
    if (/^(?:[a-z]+:|\/\/)/i.test(url)) continue
    const target = resolve(stylesheet, '..', decodeURIComponent(url.split(/[?#]/)[0]))
    if (target !== output && !target.startsWith(root)) continue
    const relativeTarget = posix(relative(output, target))
    if (REVIEWED_LOCAL_ASSET.test(relativeTarget)) assets.push(relativeTarget)
  }
  return [...new Set(assets)].sort()
}

export function advertisedPackagePaths(root, pkg) {
  const canonical = resolve(root, 'dist/lib')
  const fields = [pkg.main, pkg.module, pkg.types, pkg.exports?.['./styles.css']]
  return fields
    .filter((value) => typeof value === 'string')
    .map((value) => posix(relative(canonical, resolve(root, value))))
}

function compilerOptions(root) {
  const configPath = resolve(root, 'tsconfig.app.json')
  if (existsSync(configPath)) {
    return ts.parseJsonConfigFileContent(ts.readConfigFile(configPath, ts.sys.readFile).config, ts.sys, root).options
  }
  return {
    moduleResolution: ts.ModuleResolutionKind.Bundler,
    module: ts.ModuleKind.ESNext,
    jsx: ts.JsxEmit.ReactJSX,
    baseUrl: root,
    paths: { '@/*': ['src/*'] },
  }
}

function walkLocalSource(file, options, visited, errors, root) {
  if (visited.has(file)) return
  visited.add(file)
  if (file.endsWith('.css')) return
  const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true)
  function visit(node) {
    let specifier
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) specifier = node.moduleSpecifier
    if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || node.expression.getText() === 'require')) {
      if (!(node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0]))) {
        errors.push(`unanalyzable import in ${posix(relative(root, file))}`)
        return
      }
      specifier = node.arguments[0]
    }
    if (specifier && ts.isStringLiteral(specifier)) {
      const name = specifier.text
      if (name.startsWith('.') || name.startsWith('@/')) {
        if (name.endsWith('.css')) {
          walkLocalSource(resolve(file, '..', name), options, visited, errors, root)
          return
        }
        const resolved = ts.resolveModuleName(name, file, options, ts.sys).resolvedModule
        if (!resolved) {
          errors.push(`unresolved library dependency ${name} from ${posix(relative(root, file))}`)
          return
        }
        walkLocalSource(resolve(resolved.resolvedFileName), options, visited, errors, root)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
}

export function sourceDeclarationPath(source) {
  const rel = posix(source)
  if (rel === 'src/index.ts') return 'index.d.ts'
  if (!/\.tsx?$/.test(rel)) return null
  return rel.replace(/^src\//, '').replace(/\.tsx?$/, '.d.ts')
}

export function catalogSourceGraph(root, catalog) {
  const options = compilerOptions(root)
  const visited = new Set()
  const errors = []
  const entry = resolve(root, 'src/index.ts')
  if (existsSync(entry)) walkLocalSource(entry, options, visited, errors, root)
  for (const item of catalog.entries ?? []) {
    if ((item.publicExports?.length || item.publicTypes?.length) && existsSync(resolve(root, item.source))) {
      walkLocalSource(resolve(root, item.source), options, visited, errors, root)
    }
  }
  return { files: [...visited].map((file) => posix(relative(root, file))), errors }
}

export function publishedPackageAllowlist({ root, catalog, pkg, output, stylesheet }) {
  const allowed = new Set(advertisedPackagePaths(root, pkg))
  const cssFile = stylesheet ?? (typeof pkg.exports?.['./styles.css'] === 'string' ? resolve(root, pkg.exports['./styles.css']) : null)
  if (cssFile && existsSync(cssFile)) {
    for (const asset of localCssAssetPaths(readFileSync(cssFile, 'utf8'), cssFile, output ?? resolve(root, 'dist/lib'))) {
      allowed.add(asset)
    }
  }
  const bySource = new Map((catalog.entries ?? []).map((entry) => [entry.source, entry]))
  const graph = catalogSourceGraph(root, catalog)
  const leaks = []
  for (const source of graph.files) {
    if (source.endsWith('.css') || source === 'src/index.ts') continue
    const entry = bySource.get(source)
    const reviewedHelper = REVIEWED_HELPER_SOURCES.has(source)
    if (entry ? !ALLOWED_CLASSIFICATIONS.has(entry.classification) : !reviewedHelper) {
      leaks.push(source)
      continue
    }
    const declaration = sourceDeclarationPath(source)
    if (declaration) allowed.add(declaration)
  }
  return { allowed, graph: graph.files, errors: graph.errors, leaks }
}

export function inspectPublishedPackage({ root, catalog, pkg, output, files, stylesheet }) {
  const published = files ?? listPublishedFiles(output)
  const allowlist = publishedPackageAllowlist({ root, catalog, pkg, output, stylesheet })
  const unexpected = published.filter((file) => !allowlist.allowed.has(file)).sort()
  const publicDeclarations = (catalog.entries ?? [])
    .filter((entry) => entry.publicExports?.length || entry.publicTypes?.length)
    .map((entry) => sourceDeclarationPath(entry.source))
    .filter(Boolean)
  const missingPublic = publicDeclarations.filter((file) => !published.includes(file)).sort()
  return { published, unexpected, missingPublic, ...allowlist }
}
