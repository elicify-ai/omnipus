import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import Ajv2020 from 'ajv/dist/2020.js'

const catalogSchema = JSON.parse(readFileSync(new URL('../../design-system/catalog.schema.json', import.meta.url), 'utf8'))
const surfacesSchema = JSON.parse(readFileSync(new URL('../../design-system/surfaces.schema.json', import.meta.url), 'utf8'))
const ajv = new Ajv2020({ allErrors: true })
const validateCatalogSchema = ajv.compile(catalogSchema)
const validateSurfacesSchema = ajv.compile(surfacesSchema)

const allowedClassifications = new Set(['foundations', 'primitive', 'composite', 'domain', 'application'])
const allowedCatalogKeys = new Set(['version', 'classifications', 'entries'])
const allowedInventoryKeys = new Set(['version', 'surfaces', 'sourceOwners'])
const allowedEntryKeys = new Set(['source', 'classification', 'exports', 'publicExports', 'publicTypes'])
const allowedSurfaceKeys = new Set(['id', 'kind', 'source', 'symbol', 'lane', 'entry', 'verification'])
const allowedCheckKeys = new Set(['checkId', 'kind', 'file', 'test', 'status'])
const allowedOwnerKeys = new Set(['source', 'lane'])
const requiredManifestKinds = ['unit', 'interaction', 'axe', 'keyboard', 'browser', 'pointer', 'reduced-motion', 'forced-colors', 'root-size', 'zoom', 'reflow']
const requiredSurfaceKinds = {
  route: ['unit', 'browser', 'reflow'],
  redirect: ['unit', 'browser', 'reflow'],
  screen: ['unit', 'browser', 'reflow'],
  tab: ['unit', 'interaction', 'browser'],
  modal: ['unit', 'interaction', 'browser'],
}

function readJson(file, errors) {
  try { return JSON.parse(readFileSync(file, 'utf8')) }
  catch (error) { errors.push(`invalid JSON ${file}: ${error.message}`); return {} }
}

function walk(directory) {
  return readdirSync(directory).flatMap((name) => {
    const path = resolve(directory, name)
    return statSync(path).isDirectory() ? walk(path) : [path]
  })
}

function unknownKeys(value, allowed, label, errors) {
  for (const key of Object.keys(value ?? {})) if (!allowed.has(key)) errors.push(`unknown ${label} key: ${key}`)
}

function moduleExports(file) {
  const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true)
  if (source.parseDiagnostics.length) throw new Error(`TypeScript parse failure in ${file}: ${source.parseDiagnostics[0].messageText}`)
  const names = []
  for (const statement of source.statements) {
    const exported = statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)
    if (ts.isExportDeclaration(statement) && statement.exportClause && ts.isNamedExports(statement.exportClause)) {
      for (const element of statement.exportClause.elements) names.push(element.name.text)
    } else if (exported && 'name' in statement && statement.name && ts.isIdentifier(statement.name)) names.push(statement.name.text)
    else if (exported && ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) names.push(declaration.name.text)
    }
  }
  return [...new Set(names)]
}

function resolveModule(root, specifier) {
  const base = resolve(root, 'src', specifier.replace(/^\.\//, ''))
  return [base + '.ts', base + '.tsx', resolve(base, 'index.ts')].find(existsSync)
}

function publicExports(root, errors) {
  const file = resolve(root, 'src/index.ts')
  const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true)
  if (source.parseDiagnostics.length) { errors.push(`TypeScript parse failure in src/index.ts`); return [] }
  const result = []
  for (const statement of source.statements) {
    if (statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) && !ts.isExportDeclaration(statement)) {
      errors.push('src/index.ts may contain only named re-exports; direct exported declarations are forbidden')
      continue
    }
    if (!ts.isExportDeclaration(statement) || !statement.moduleSpecifier || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const target = resolveModule(root, statement.moduleSpecifier.text)
    if (!target) { errors.push(`unresolved public module: ${statement.moduleSpecifier.text}`); continue }
    if (!statement.exportClause || !ts.isNamedExports(statement.exportClause)) { errors.push(`unsupported wildcard public export: ${statement.moduleSpecifier.text}`); continue }
    for (const element of statement.exportClause.elements) result.push({
      source: relative(root, target),
      name: element.name.text,
      type: statement.isTypeOnly || element.isTypeOnly,
    })
  }
  return result
}

function discoveredSurfaces(root) {
  const discovered = []
  for (const file of walk(resolve(root, 'src/routes'))) {
    const name = file.split('/').at(-1)
    if (!/\.(ts|tsx)$/.test(file) || /\.test\./.test(file) || name.startsWith('-') || name === 'authValidation.ts') continue
    discovered.push(relative(root, file).replace('src/routes/', '').replace(/\.(ts|tsx)$/, ''))
  }
  for (const file of walk(resolve(root, 'src/components')).filter((path) => path.endsWith('.tsx') && !/\.(test|stories)\.tsx$/.test(path) && !path.includes('/components/ui/'))) {
    const path = relative(root, file)
    const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    const overlayBindings = new Set(['Dialog', 'AlertDialog', 'Sheet'])
    for (const statement of source.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier) || !/(dialog|sheet)$/.test(statement.moduleSpecifier.text)) continue
      for (const element of statement.importClause?.namedBindings?.elements ?? []) {
        if (['Dialog', 'AlertDialog', 'Sheet'].includes(element.propertyName?.text ?? element.name.text)) overlayBindings.add(element.name.text)
      }
    }
    let overlayIndex = 0
    let hasDynamicTabs = false
    function visit(node) {
      const exported = node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)
      if (exported && node.name && ts.isIdentifier(node.name) && /(?:Tab|Tabs|Modal|Dialog|Screen)$/.test(node.name.text)) discovered.push(`${path}#${node.name.text}`)
      if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = node.tagName.getText(source).split('.').at(-1)
        if (overlayBindings.has(tag)) discovered.push(`${path}#overlay:${++overlayIndex}`)
        if (tag === 'TabsContent') {
          const attribute = node.attributes.properties.find((item) => ts.isJsxAttribute(item) && item.name.text === 'value')
          if (attribute?.initializer && ts.isStringLiteral(attribute.initializer)) discovered.push(`${path}#tab:${attribute.initializer.text}`)
        }
      }
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'map') {
        let rendersTab = false
        function inspect(child) {
          if (ts.isJsxOpeningElement(child) || ts.isJsxSelfClosingElement(child)) {
            const tag = child.tagName.getText(source).split('.').at(-1)
            const role = child.attributes.properties.find((item) => ts.isJsxAttribute(item) && item.name.text === 'role')
            if (tag === 'TabsTrigger' || (role?.initializer && ts.isStringLiteral(role.initializer) && role.initializer.text === 'tab')) rendersTab = true
          }
          ts.forEachChild(child, inspect)
        }
        inspect(node)
        hasDynamicTabs ||= rendersTab
      }
      ts.forEachChild(node, visit)
    }
    visit(source)
    if (hasDynamicTabs) discovered.push(`${path}#dynamic-tabs`)
  }
  return [...new Set(discovered)]
}

function applicationSources(root) {
  return [resolve(root, 'src/components'), resolve(root, 'src/routes')]
    .flatMap(walk)
    .concat(walk(resolve(root, 'src')).filter((file) => file.split('/').length === resolve(root, 'src').split('/').length + 1))
    .filter((file) => /\.tsx?$/.test(file))
    .filter((file) => !/\.(test|stories)\./.test(file) && !file.endsWith('.gen.ts') && !file.endsWith('.d.ts') && !file.endsWith('/src/index.ts') && !file.includes('/components/ui/'))
    .map((file) => relative(root, file))
}

export function validateCatalog(root = process.cwd()) {
  const errors = []
  const catalog = readJson(resolve(root, 'design-system/catalog.json'), errors)
  const inventory = readJson(resolve(root, 'design-system/surfaces.json'), errors)
  if (!validateCatalogSchema(catalog)) for (const error of validateCatalogSchema.errors ?? []) errors.push(`catalog schema ${error.instancePath || '/'} ${error.message}`)
  if (!validateSurfacesSchema(inventory)) for (const error of validateSurfacesSchema.errors ?? []) errors.push(`surfaces schema ${error.instancePath || '/'} ${error.message}`)
  unknownKeys(catalog, allowedCatalogKeys, 'catalog', errors)
  unknownKeys(inventory, allowedInventoryKeys, 'surface inventory', errors)
  if (catalog.version !== 1 || !Array.isArray(catalog.entries) || !Array.isArray(catalog.classifications)) errors.push('catalog must be version 1 with classifications and entries arrays')
  if (inventory.version !== 1 || !Array.isArray(inventory.surfaces) || !Array.isArray(inventory.sourceOwners)) errors.push('surfaces must be version 1 with surfaces and sourceOwners arrays')
  if (!Array.isArray(catalog.entries) || !Array.isArray(catalog.classifications) || !Array.isArray(inventory.surfaces) || !Array.isArray(inventory.sourceOwners)) return errors

  const bySource = new Map()
  for (const entry of catalog.entries ?? []) {
    unknownKeys(entry, allowedEntryKeys, 'catalog entry', errors)
    if (bySource.has(entry.source)) errors.push(`duplicate catalog source: ${entry.source}`)
    bySource.set(entry.source, entry)
    if (!allowedClassifications.has(entry.classification)) errors.push(`unclassified source: ${entry.source}`)
    const file = resolve(root, entry.source)
    if (!existsSync(file)) errors.push(`missing catalog source: ${entry.source}`)
    else try {
      const actual = moduleExports(file)
      for (const name of entry.exports ?? []) if (!actual.includes(name)) errors.push(`stale catalog export: ${entry.source}#${name}`)
      for (const name of actual) if (!(entry.exports ?? []).includes(name)) errors.push(`uncataloged module export: ${entry.source}#${name}`)
    }
    catch (error) { errors.push(error.message) }
  }

  const uiSources = walk(resolve(root, 'src/components/ui')).filter((file) => /\.tsx?$/.test(file) && !/\.(test|stories)\.tsx?$/.test(file)).map((file) => relative(root, file))
  for (const source of uiSources) if (!bySource.has(source)) errors.push(`uncataloged ui source: ${source}`)
  const indexedPublic = publicExports(root, errors)
  const indexedKeys = new Set(indexedPublic.map((item) => `${item.source}#${item.type ? 'type:' : ''}${item.name}`))
  for (const item of indexedPublic) {
    const entry = bySource.get(item.source)
    const collection = item.type ? entry?.publicTypes : entry?.publicExports
    if (!collection?.includes(item.name)) errors.push(`uncataloged public ${item.type ? 'type' : 'export'}: ${item.source}#${item.name}`)
    if (!['foundations', 'primitive', 'composite'].includes(entry?.classification)) errors.push(`invalid public classification: ${item.source}#${item.name}`)
  }
  for (const entry of catalog.entries) {
    for (const name of entry.publicExports ?? []) if (!indexedKeys.has(`${entry.source}#${name}`)) errors.push(`stale catalog public export: ${entry.source}#${name}`)
    for (const name of entry.publicTypes ?? []) if (!indexedKeys.has(`${entry.source}#type:${name}`)) errors.push(`stale catalog public type: ${entry.source}#${name}`)
  }

  const manifestDirectory = resolve(root, 'design-system/manifests')
  const manifests = existsSync(manifestDirectory)
    ? readdirSync(manifestDirectory).filter((name) => name.endsWith('.json')).map((name) => readJson(resolve(manifestDirectory, name), errors))
    : []
  const manifestedExports = new Set(manifests.flatMap((manifest) => manifest.exports ?? []))
  for (const item of indexedPublic) {
    if (!item.type && item.source.startsWith('src/components/ui/') && !manifestedExports.has(item.name)) errors.push(`missing manifest coverage: ${item.source}#${item.name}`)
  }
  for (const manifest of manifests) {
    const checksByKind = new Map((manifest.checks ?? []).map((check) => [check.kind, check]))
    for (const kind of requiredManifestKinds) {
      const check = checksByKind.get(kind)
      if (!check) errors.push(`manifest ${manifest.component} omits required check kind: ${kind}`)
      else if (check.applicable === false && !(typeof check.reason === 'string' && check.reason.trim())) errors.push(`manifest ${manifest.component} lacks supported reason for ${kind}`)
    }
  }

  const ids = new Set()
  const verificationIds = new Set()
  const ownerBySource = new Map()
  for (const owner of inventory.sourceOwners) {
    unknownKeys(owner, allowedOwnerKeys, 'source owner', errors)
    if (ownerBySource.has(owner.source)) errors.push(`duplicate source owner: ${owner.source}`)
    if (!Number.isInteger(owner.lane) || owner.lane < 1 || owner.lane > 8) errors.push(`invalid source owner lane: ${owner.source}`)
    if (!existsSync(resolve(root, owner.source))) errors.push(`missing source owner file: ${owner.source}`)
    ownerBySource.set(owner.source, owner.lane)
  }
  const expectedOwners = new Set(applicationSources(root))
  for (const source of expectedOwners) if (!ownerBySource.has(source)) errors.push(`missing application source owner: ${source}`)
  for (const source of ownerBySource.keys()) if (!expectedOwners.has(source)) errors.push(`stale application source owner: ${source}`)
  for (const surface of inventory.surfaces ?? []) {
    unknownKeys(surface, allowedSurfaceKeys, 'surface', errors)
    if (ids.has(surface.id)) errors.push(`duplicate surface id: ${surface.id}`)
    ids.add(surface.id)
    if (!existsSync(resolve(root, surface.source))) errors.push(`missing surface source: ${surface.source}`)
    if (!Object.hasOwn(requiredSurfaceKinds, surface.kind)) errors.push(`invalid surface kind: ${surface.id}`)
    if (!surface.entry || !['route', 'activation'].includes(surface.entry.type) || typeof surface.entry.path !== 'string') errors.push(`missing surface entry: ${surface.id}`)
    if (!Number.isInteger(surface.lane) || surface.lane < 1 || surface.lane > 8) errors.push(`invalid lane: ${surface.id}`)
    if (ownerBySource.get(surface.source) !== surface.lane) errors.push(`surface lane conflicts with source owner: ${surface.id}`)
    if (!Array.isArray(surface.verification) || surface.verification.length === 0) errors.push(`missing verification: ${surface.id}`)
    const verificationKinds = new Set()
    for (const check of surface.verification ?? []) {
      unknownKeys(check, allowedCheckKeys, 'verification', errors)
      if (![check.checkId, check.kind, check.file, check.test].every((value) => typeof value === 'string' && value.length)) errors.push(`invalid verification mapping: ${surface.id}`)
      if (verificationIds.has(check.checkId)) errors.push(`duplicate verification check id: ${check.checkId}`)
      verificationIds.add(check.checkId)
      if (verificationKinds.has(check.kind)) errors.push(`duplicate surface verification kind: ${surface.id}#${check.kind}`)
      verificationKinds.add(check.kind)
      if (!['executed', 'planned'].includes(check.status)) errors.push(`invalid verification status: ${surface.id}`)
      if (check.status === 'executed' && !existsSync(resolve(root, check.file))) errors.push(`missing executed verification file: ${surface.id}#${check.checkId}`)
      if (check.status === 'planned') errors.push(`planned verification gap: ${surface.id}#${check.checkId}`)
    }
    for (const kind of requiredSurfaceKinds[surface.kind] ?? []) {
      if (!verificationKinds.has(kind)) errors.push(`missing ${kind} verification: ${surface.id}`)
    }
  }
  for (const id of discoveredSurfaces(root)) if (!ids.has(id)) errors.push(`missing discovered surface: ${id}`)
  return errors
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const errors = validateCatalog()
  if (errors.length) { console.error(errors.join('\n')); process.exitCode = 1 }
  else console.log('design-system catalog and surface inventory valid')
}
