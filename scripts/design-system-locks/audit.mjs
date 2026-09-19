#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname, extname, relative, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import Ajv2020 from 'ajv/dist/2020.js'

export const CHECKPOINTS = Object.freeze(['A', 'B', 'C1', 'C2', 'C3', 'C4', 'C5', 'C6'])
export const SCANNER_FILES = Object.freeze([
  'css-colors.mjs',
  'ts-colors.mjs',
  'typography.mjs',
  'spacing.mjs',
  'controls.mjs',
  'status.mjs',
])
export const SOURCE_EXTENSIONS = Object.freeze(['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'])
export const COVERAGE_FILE = 'coverage.mjs'
export const CANONICAL_GENERATED_TOKEN_PATHS = Object.freeze([
  'src/styles/tokens.generated.css',
  'src/styles/tokens.theme.generated.css',
  'src/design-system/tokens.ts',
])

const SKIP_DIRS = new Set(['node_modules', 'dist', '.git', 'coverage'])
const DEFAULT_SRC_ROOTS = Object.freeze(['src', 'packages/ui/src'])
// Each E1 lock must retain its own applicability; a different lock scanning
// the same extension cannot replace the missing checks.
const REQUIRED_SCANNER_EXTENSIONS = Object.freeze({
  'css-colors.mjs': ['.css'],
  'ts-colors.mjs': ['.js', '.jsx', '.ts', '.tsx', '.svg'],
  'typography.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'],
  'spacing.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx'],
  'controls.mjs': ['.js', '.jsx', '.ts', '.tsx'],
  'status.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'],
})
const POSIX_FILE = /^(?!\/)(?!\.\.)(?!.*\/\.\.)[A-Za-z0-9._$ -]+(?:\/[A-Za-z0-9._$ -]+)*$/
const TEST_BOUNDARY_SUFFIX = /\.(?:test|spec)\.(?:js|jsx|ts|tsx)$/
const STORY_BOUNDARY_SUFFIX = /\.stories\.(?:js|jsx|ts|tsx)$/
const FATAL_SETUP = new Set([
  'schema',
  'missing-argument',
  'invalid-checkpoint',
  'deleted-policy',
  'invalid-policy',
  'invalid-catalog',
  'scanner-not-ready',
])

const scriptDir = fileURLToPath(new URL('.', import.meta.url))
const baselineSchema = JSON.parse(readFileSync(new URL('../../design-system/enforcement/baseline.schema.json', import.meta.url), 'utf8'))
const ledgerSchema = JSON.parse(readFileSync(new URL('../../design-system/enforcement/ledger.schema.json', import.meta.url), 'utf8'))
const ajv = new Ajv2020({ allErrors: true })
const validateBaselineSchema = ajv.compile(baselineSchema)
const validateLedgerSchema = ajv.compile(ledgerSchema)

export function fingerprintOf(ruleId, path, syntax) {
  return createHash('sha256').update(JSON.stringify([ruleId, path, syntax])).digest('hex')
}

function addError(errors, code, message, extra = {}) {
  const rest = { ...extra }
  delete rest.code
  delete rest.message
  errors.push({ code, message, ...rest })
}

function schemaMessages(validate, label) {
  return (validate.errors ?? []).map((error) => `${label} schema ${error.instancePath || '/'} ${error.message}`)
}

function toPosix(root, absolute) {
  return relative(root, absolute).split(sep).join('/')
}

function checkpointIndex(name) {
  return CHECKPOINTS.indexOf(name)
}

function isExpired(expiry, current) {
  return checkpointIndex(expiry) !== -1 && checkpointIndex(current) !== -1 && checkpointIndex(expiry) <= checkpointIndex(current)
}

function infrastructureKind(ruleId, message = '') {
  const id = String(ruleId ?? '')
  if (/(^|[.:/_-])unsupported([.:/_-]|$)/i.test(id)) return 'unsupported'
  if (/(^|[.:/_-])parse-failure([.:/_-]|$)/i.test(id) || /(^|[.:/_-])parse([.:/_-]|$)/i.test(id)) return 'parse-failure'
  if (/(unclassified\s+parse|parse\s+failure|failed\s+to\s+parse)/i.test(message) && !/parse/i.test(id)) return 'unclassified-parse'
  return null
}

function reviewedBoundaryKindMatches(path, kind) {
  if (kind === 'test') return TEST_BOUNDARY_SUFFIX.test(path)
  if (kind === 'story') return STORY_BOUNDARY_SUFFIX.test(path)
  if (kind === 'generated-tokens') return CANONICAL_GENERATED_TOKEN_PATHS.includes(path)
  return false
}

function isAuthorizedReviewedBoundary(root, boundary) {
  if (!boundary || typeof boundary.path !== 'string' || typeof boundary.kind !== 'string') return false
  if (isBlanketPath(root, boundary.path)) return false
  return reviewedBoundaryKindMatches(boundary.path, boundary.kind)
}

function isReviewedBoundary(posixPath, extraPaths) {
  return extraPaths.has(posixPath)
}

function isBlanketPath(root, posixPath) {
  if (!POSIX_FILE.test(posixPath) || posixPath.endsWith('/') || /[*?]/.test(posixPath)) return true
  const absolute = resolve(root, posixPath)
  return existsSync(absolute) && statSync(absolute).isDirectory()
}

function readJson(file, label, errors) {
  if (!file) return null
  if (!existsSync(file)) {
    addError(errors, 'schema', `${label} not found: ${file}`)
    return null
  }
  try {
    return JSON.parse(readFileSync(file, 'utf8'))
  } catch (error) {
    addError(errors, 'schema', `${label} invalid JSON: ${error.message}`)
    return null
  }
}

export function parseArgs(argv) {
  const result = {}
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index]
    if (token === '--coverage') {
      result.coverage = true
      continue
    }
    if (!token?.startsWith('--')) continue
    const body = token.slice(2)
    const equals = body.indexOf('=')
    if (equals !== -1) {
      result[body.slice(0, equals)] = body.slice(equals + 1)
      continue
    }
    result[body] = argv[index + 1]
    index += 1
  }
  return result
}

function validatePolicy(policy, errors) {
  if (policy == null) {
    addError(errors, 'invalid-policy', 'a live token policy is required')
    return null
  }
  if (policy.deleted === true) {
    addError(errors, 'deleted-policy', 'policy was deleted; scanners cannot run without a live token policy')
    return null
  }
  if (!Array.isArray(policy.tokenCssNames) || typeof policy.resolvedTokens !== 'object' || policy.resolvedTokens === null || Array.isArray(policy.resolvedTokens)) {
    addError(errors, 'invalid-policy', 'policy must provide tokenCssNames[] and resolvedTokens{}')
    return null
  }
  const names = policy.tokenCssNames
  const validNames = names.every((name) => typeof name === 'string' && /^--[A-Za-z0-9_-]+$/.test(name))
  const uniqueNames = new Set(names)
  if (!validNames || uniqueNames.size !== names.length) {
    addError(errors, 'invalid-policy', 'tokenCssNames must contain unique CSS custom property names')
    return null
  }
  const cssValues = policy.resolvedCssTokens
  if (cssValues !== undefined && (
    cssValues === null || typeof cssValues !== 'object' || Array.isArray(cssValues) ||
    Object.keys(cssValues).length !== uniqueNames.size ||
    [...uniqueNames].some((name) => !Object.hasOwn(cssValues, name)) ||
    Object.values(cssValues).some((value) => !(typeof value === 'string' && value.trim().length > 0) &&
      !(typeof value === 'number' && Number.isFinite(value)))
  )) {
    addError(errors, 'invalid-policy', 'resolvedCssTokens must map every registered CSS name to a nonempty string or finite number')
    return null
  }
  return Object.freeze({
    tokenCssNames: Object.freeze([...policy.tokenCssNames]),
    resolvedTokens: Object.freeze({ ...policy.resolvedTokens }),
    ...(cssValues === undefined ? {} : { resolvedCssTokens: Object.freeze({ ...cssValues }) }),
  })
}

function collectSourceFiles(root, srcRoots, errors) {
  const files = []
  for (const rel of srcRoots) {
    const absRoot = resolve(root, rel)
    let stats
    try {
      stats = lstatSync(absRoot)
    } catch (error) {
      if (error?.code === 'ENOENT') continue
      addError(errors, 'source-filesystem', `cannot inspect source root ${rel}: ${error.message}`, { path: rel })
      continue
    }
    if (stats.isSymbolicLink()) {
      addError(errors, 'source-filesystem', `source root must not be a symbolic link: ${rel}`, { path: rel })
      continue
    }
    if (!stats.isDirectory()) {
      addError(errors, 'source-filesystem', `source root must be a directory: ${rel}`, { path: rel })
      continue
    }
    walk(absRoot, root, files, errors)
  }
  files.sort((left, right) => left.path.localeCompare(right.path))
  return files
}

function walk(directory, root, files, errors) {
  let names
  try {
    names = readdirSync(directory)
  } catch (error) {
    const path = toPosix(root, directory)
    addError(errors, 'source-filesystem', `cannot read source directory ${path}: ${error.message}`, { path })
    return
  }
  for (const name of names) {
    const absolute = resolve(directory, name)
    const path = toPosix(root, absolute)
    let stats
    try {
      stats = lstatSync(absolute)
    } catch (error) {
      addError(errors, 'source-filesystem', `cannot inspect source path ${path}: ${error.message}`, { path })
      continue
    }
    if (stats.isSymbolicLink()) {
      addError(errors, 'source-filesystem', `source path must not be a symbolic link: ${path}`, { path })
      continue
    }
    if (stats.isDirectory()) {
      if (!SKIP_DIRS.has(name)) walk(absolute, root, files, errors)
      continue
    }
    if (!stats.isFile()) {
      addError(errors, 'source-filesystem', `source path must be a regular file: ${path}`, { path })
      continue
    }
    const extension = extname(name).toLowerCase()
    if (!SOURCE_EXTENSIONS.includes(extension)) continue
    if (!POSIX_FILE.test(path)) {
      addError(errors, 'invalid-path', `applicable source path is not a safe repository-relative POSIX file: ${path}`, { path })
      continue
    }
    try {
      files.push({ absolute, path, extension, source: readFileSync(absolute, 'utf8') })
    } catch (error) {
      addError(errors, 'source-filesystem', `cannot read source file ${path}: ${error.message}`, { path })
    }
  }
}

function validateDocuments(baseline, ledger, checkpoint, root, errors) {
  if (!CHECKPOINTS.includes(checkpoint)) {
    addError(errors, 'invalid-checkpoint', `checkpoint must be one of ${CHECKPOINTS.join(', ')}`)
  }
  if (baseline && !validateBaselineSchema(baseline)) {
    for (const message of schemaMessages(validateBaselineSchema, 'baseline')) addError(errors, 'schema', message)
  }
  if (ledger && !validateLedgerSchema(ledger)) {
    for (const message of schemaMessages(validateLedgerSchema, 'ledger')) addError(errors, 'schema', message)
  }
  if (!baseline || !ledger || !Array.isArray(baseline.fingerprints) || !Array.isArray(ledger.entries) || !Array.isArray(ledger.exceptions)) return
  validateExactBoundaries(root, ledger, errors)
  validateFingerprintIdentity(baseline, ledger, checkpoint, errors)
}

function validateExactBoundaries(root, ledger, errors) {
  for (const entry of ledger.entries) {
    if (entry?.path && isBlanketPath(root, entry.path)) {
      addError(errors, 'blanket-directory', `ledger entry path is not an exact file: ${entry.path}`)
    }
  }
  for (const exception of ledger.exceptions) {
    if (exception?.path && isBlanketPath(root, exception.path)) {
      addError(errors, 'blanket-directory', `exception path is not an exact file: ${exception.path}`)
    }
  }
  for (const boundary of ledger.reviewedBoundaries ?? []) {
    if (boundary?.path && isBlanketPath(root, boundary.path)) {
      addError(errors, 'blanket-directory', `reviewed boundary path is not an exact file: ${boundary.path}`)
      continue
    }
    if (boundary?.path && boundary?.kind && !reviewedBoundaryKindMatches(boundary.path, boundary.kind)) {
      addError(errors, 'invalid-reviewed-boundary', `reviewed boundary kind ${boundary.kind} does not describe ${boundary.path}`, {
        path: boundary.path,
        kind: boundary.kind,
      })
    }
  }
}

function validateFingerprintIdentity(baseline, ledger, checkpoint, errors) {
  const baselineByFp = new Map()
  const ledgerByFp = new Map()
  const exceptionKeys = new Set()
  for (const item of baseline.fingerprints) {
    const expected = fingerprintOf(item.ruleId, item.path, item.syntax)
    if (item.fingerprint !== expected) {
      addError(errors, 'fingerprint-mismatch', `baseline fingerprint does not match [ruleId,path,syntax]: ${item.fingerprint}`)
    }
    const kind = infrastructureKind(item.ruleId, '')
    if (kind) addError(errors, kind, `${kind} cannot be stored in the baseline: ${item.ruleId} ${item.path}`)
    if (baselineByFp.has(item.fingerprint)) addError(errors, 'duplicate-baseline', `duplicate baseline fingerprint ${item.fingerprint}`)
    baselineByFp.set(item.fingerprint, item)
  }
  for (const entry of ledger.entries) {
    const expected = fingerprintOf(entry.ruleId, entry.path, entry.syntax)
    if (entry.fingerprint !== expected) {
      addError(errors, 'fingerprint-mismatch', `ledger fingerprint does not match [ruleId,path,syntax]: ${entry.fingerprint}`)
    }
    if (ledgerByFp.has(entry.fingerprint)) addError(errors, 'duplicate-ledger', `duplicate ledger fingerprint ${entry.fingerprint}`)
    ledgerByFp.set(entry.fingerprint, entry)
    if (isExpired(entry.expiryCheckpoint, checkpoint)) {
      addError(errors, 'expired-ledger', `ledger entry expired at ${entry.expiryCheckpoint} (checkpoint ${checkpoint})`, {
        fingerprint: entry.fingerprint,
        path: entry.path,
        ruleId: entry.ruleId,
      })
    }
  }
  for (const exception of ledger.exceptions) {
    const key = JSON.stringify([exception.ruleId, exception.path, exception.syntax])
    if (exceptionKeys.has(key)) addError(errors, 'duplicate-exception', `duplicate exception ${exception.ruleId} ${exception.path}`)
    exceptionKeys.add(key)
  }
  for (const fingerprint of baselineByFp.keys()) {
    if (!ledgerByFp.has(fingerprint)) addError(errors, 'missing-ledger', `baseline fingerprint has no owned ledger entry ${fingerprint}`)
  }
  for (const fingerprint of ledgerByFp.keys()) {
    if (!baselineByFp.has(fingerprint)) addError(errors, 'unknown-ledger', `ledger fingerprint is not in the baseline ${fingerprint}`)
  }
}

function findingShapeError(finding) {
  if (finding === null || typeof finding !== 'object' || Array.isArray(finding)) return 'finding is not an object'
  const { ruleId, path, syntax, message, line, column } = finding
  if (typeof ruleId !== 'string' || ruleId.length === 0) return 'finding.ruleId must be a nonempty string'
  if (typeof path !== 'string' || !POSIX_FILE.test(path)) return 'finding.path must be a repository-relative POSIX file path'
  if (typeof syntax !== 'string' || syntax.trim().length === 0) return 'finding.syntax must be nonempty canonical syntax'
  if (typeof message !== 'string' || message.length === 0) return 'finding.message must be a nonempty string'
  if (line !== undefined && !(Number.isInteger(line) && line > 0)) return 'finding.line must be a positive integer when present'
  if (column !== undefined && !(Number.isInteger(column) && column > 0)) return 'finding.column must be a positive integer when present'
  return null
}

function normalizeScanner(module, label, errors) {
  const scan = module?.scan
  const extensions = module?.extensions
  if (typeof scan !== 'function') {
    addError(errors, 'malformed-scanner', `${label} must export function scan`)
    return null
  }
  if (!Array.isArray(extensions) || extensions.length === 0 || extensions.some((ext) => typeof ext !== 'string' || !ext.startsWith('.'))) {
    addError(errors, 'malformed-scanner', `${label} must export extensions array of leading-dot extensions`)
    return null
  }
  return { scan, extensions: [...extensions], label }
}

export async function loadScannerModules(scannerDir, errors) {
  const scanners = []
  for (const file of SCANNER_FILES) {
    const absolute = resolve(scannerDir, file)
    if (!existsSync(absolute)) {
      addError(errors, 'scanner-not-ready', `scanner module not ready: ${file}`)
      continue
    }
    try {
      const module = await import(pathToFileURL(absolute).href)
      const scanner = normalizeScanner(module, file, errors)
      if (scanner) {
        const missingExtensions = REQUIRED_SCANNER_EXTENSIONS[file].filter((ext) => !scanner.extensions.includes(ext))
        if (missingExtensions.length > 0) {
          addError(errors, 'scanner-coverage-gap', `${file} does not handle required formats: ${missingExtensions.join(', ')}`, {
            scanner: file,
            missingExtensions,
          })
        }
        scanners.push(scanner)
      }
    } catch (error) {
      addError(errors, 'scanner-exception', `scanner import failed: ${file}: ${error.message}`)
    }
  }
  return scanners
}

function runScanners({ files, scanners, policy, catalog, errors }) {
  const findings = []
  const modules = Object.freeze(Object.fromEntries(files.map((file) => [file.path, file.source])))
  const claimed = new Set(scanners.flatMap((scanner) => scanner.extensions))
  for (const file of files) {
    if (!claimed.has(file.extension)) {
      addError(errors, 'unsupported', `unsupported coverage gap: no scanner handles ${file.extension} (${file.path})`, {
        path: file.path,
        ruleId: 'enforcement.unsupported-syntax',
        syntax: file.extension,
      })
    }
    const source = file.source
    for (const scanner of scanners) {
      if (!scanner.extensions.includes(file.extension)) continue
      collectScannerFindings({ scanner, file, source, policy, catalog, modules, findings, errors })
    }
  }
  return findings
}

function collectScannerFindings({ scanner, file, source, policy, catalog, modules, findings, errors }) {
  let raw
  try {
    raw = scanner.scan({ path: file.path, source, policy, catalog, modules })
  } catch (error) {
    addError(errors, 'scanner-exception', `${scanner.label} threw on ${file.path}: ${error.message}`, { path: file.path })
    return
  }
  if (!Array.isArray(raw)) {
    addError(errors, 'malformed-scanner', `${scanner.label} did not return Finding[] for ${file.path}`, { path: file.path })
    return
  }
  for (const finding of raw) {
    const shape = findingShapeError(finding)
    if (shape) {
      addError(errors, 'malformed-scanner', `${scanner.label} ${file.path}: ${shape}`, { path: file.path })
      continue
    }
    if (finding.path !== file.path) {
      addError(errors, 'malformed-scanner', `${scanner.label} path mismatch for ${file.path}: ${finding.path}`, { path: file.path })
      continue
    }
    findings.push({
      ruleId: finding.ruleId,
      path: finding.path,
      syntax: finding.syntax,
      message: finding.message,
      line: finding.line,
      column: finding.column,
    })
  }
}

function accountFindings({ findings, ledger, extraReviewed, errors }) {
  const exceptions = new Set((ledger.exceptions ?? []).map((item) => JSON.stringify([item.ruleId, item.path, item.syntax])))
  const counts = new Map()
  const appliedExceptions = []
  const appliedBoundaries = []
  for (const finding of findings) {
    const kind = infrastructureKind(finding.ruleId, finding.message)
    if (kind) {
      addError(errors, kind, `${kind} cannot be baselined: ${finding.ruleId} ${finding.path} ${finding.syntax}`, {
        ruleId: finding.ruleId,
        path: finding.path,
        syntax: finding.syntax,
      })
      continue
    }
    if (isReviewedBoundary(finding.path, extraReviewed)) {
      appliedBoundaries.push({ path: finding.path, ruleId: finding.ruleId, syntax: finding.syntax })
      continue
    }
    const exceptionKey = JSON.stringify([finding.ruleId, finding.path, finding.syntax])
    if (exceptions.has(exceptionKey)) {
      appliedExceptions.push({ ruleId: finding.ruleId, path: finding.path, syntax: finding.syntax })
      continue
    }
    const fingerprint = fingerprintOf(finding.ruleId, finding.path, finding.syntax)
    const current = counts.get(fingerprint)
    if (current) {
      current.count += 1
      continue
    }
    counts.set(fingerprint, {
      fingerprint,
      ruleId: finding.ruleId,
      path: finding.path,
      syntax: finding.syntax,
      message: finding.message,
      count: 1,
    })
  }
  return { counts, appliedExceptions, appliedBoundaries }
}

function compareDebt(currentCounts, baseline, errors) {
  const baselineByFp = new Map((baseline.fingerprints ?? []).map((item) => [item.fingerprint, item]))
  for (const record of currentCounts.values()) {
    const base = baselineByFp.get(record.fingerprint)
    if (!base) {
      addError(errors, 'new-debt', `new debt ${record.ruleId} ${record.path} ${record.syntax}`, record)
      continue
    }
    if (record.count > base.count) {
      addError(errors, 'increased-count', `increased count ${record.fingerprint} ${base.count} -> ${record.count}`, {
        ...record,
        baselineCount: base.count,
      })
    }
  }
}

function summarizeDebt(currentCounts, baseline) {
  const baselineByFp = new Map((baseline?.fingerprints ?? []).map((item) => [item.fingerprint, item]))
  const fingerprints = [...currentCounts.values()].map((record) => {
    const base = baselineByFp.get(record.fingerprint)
    return {
      fingerprint: record.fingerprint,
      ruleId: record.ruleId,
      path: record.path,
      syntax: record.syntax,
      count: record.count,
      accepted: Boolean(base) && record.count <= base.count,
    }
  })
  const perRule = {}
  for (const record of fingerprints) perRule[record.ruleId] = (perRule[record.ruleId] ?? 0) + record.count
  return {
    fingerprints,
    perRule,
    acceptedCount: fingerprints.filter((item) => item.accepted).length,
  }
}

function writeReport(reportPath, report) {
  mkdirSync(dirname(reportPath), { recursive: true })
  writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`)
}

function extraReviewedPaths(root, ledger) {
  const paths = new Set()
  for (const item of ledger.reviewedBoundaries ?? []) {
    if (isAuthorizedReviewedBoundary(root, item)) paths.add(item.path)
  }
  return paths
}

export async function composeCoverage({ root, indexPath, evidencePaths, coverageModule, coverageDir } = {}) {
  if (coverageModule) {
    if (typeof coverageModule.checkCoverage !== 'function') return { errors: ['coverage module missing checkCoverage'] }
    return coverageModule.checkCoverage({ root, indexPath, evidencePaths })
  }
  const file = resolve(coverageDir ?? scriptDir, COVERAGE_FILE)
  if (!existsSync(file)) return { errors: [`coverage module not ready: ${COVERAGE_FILE}`] }
  const module = await import(pathToFileURL(file).href)
  if (typeof module.checkCoverage !== 'function') return { errors: ['coverage module missing checkCoverage'] }
  return module.checkCoverage({ root, indexPath, evidencePaths })
}

function freezeCatalog(value) {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    for (const child of Object.values(value)) freezeCatalog(child)
    Object.freeze(value)
  }
  return value
}

function loadCatalogContext(root, supplied, errors) {
  let catalog = supplied
  if (catalog === undefined) catalog = readJson(resolve(root, 'design-system/catalog.json'), 'catalog', errors)
  if (!validCatalog(catalog)) {
    addError(errors, 'invalid-catalog', 'catalog must provide entries with source, exports, publicExports and publicTypes')
    return null
  }
  return freezeCatalog(structuredClone(catalog))
}

const CATALOG_SOURCE_EXTENSION = /\.(?:tsx|ts|jsx|js|mjs|cjs)$/i

function validCatalog(catalog) {
  if (!catalog || !Array.isArray(catalog.entries)) return false
  const identities = new Set()
  for (const entry of catalog.entries) {
    if (!entry || !safeCatalogSource(entry.source)) return false
    if (!uniqueStringArray(entry.exports) || !uniqueStringArray(entry.publicExports) || !uniqueStringArray(entry.publicTypes)) return false
    const exported = new Set(entry.exports)
    if (entry.publicExports.some((name) => !exported.has(name)) || entry.publicTypes.some((name) => !exported.has(name))) return false
    const identity = entry.source.replace(CATALOG_SOURCE_EXTENSION, '')
    if (identities.has(identity)) return false
    identities.add(identity)
  }
  return true
}

function safeCatalogSource(source) {
  return typeof source === 'string'
    && source === source.trim()
    && !source.startsWith('./')
    && POSIX_FILE.test(source)
    && !source.split('/').includes('.')
    && CATALOG_SOURCE_EXTENSION.test(source)
}

function uniqueStringArray(values) {
  return Array.isArray(values)
    && values.every((value) => typeof value === 'string' && value.length > 0 && value === value.trim())
    && new Set(values).size === values.length
}

export async function audit(options) {
  const errors = []
  const root = resolve(options.root ?? process.cwd())
  const checkpoint = options.checkpoint
  const baseline = options.baseline
  const ledger = options.ledger ?? { version: 1, entries: [], exceptions: [] }
  const policy = validatePolicy(options.policy, errors)
  validateDocuments(baseline, ledger, checkpoint, root, errors)

  let scanners = []
  if (options.scanners) {
    scanners = options.scanners.map((module, index) => normalizeScanner(module, module.label ?? `injected-${index}`, errors)).filter(Boolean)
  } else if (!errors.some((error) => FATAL_SETUP.has(error.code))) {
    scanners = await loadScannerModules(options.scannerDir ?? scriptDir, errors)
  }
  const catalog = scanners.some((scanner) => scanner.label === 'controls.mjs')
    ? loadCatalogContext(root, options.catalog, errors)
    : options.catalog === undefined ? undefined : freezeCatalog(structuredClone(options.catalog))

  const usingDefaultRoots = !options.srcRoots
  const srcRoots = options.srcRoots ?? DEFAULT_SRC_ROOTS
  let sourceTreeMissing = false
  if (usingDefaultRoots) {
    const canonical = resolve(root, 'src')
    let canonicalStats
    try {
      canonicalStats = lstatSync(canonical)
    } catch (error) {
      if (error?.code !== 'ENOENT') addError(errors, 'source-filesystem', `cannot inspect source root src: ${error.message}`, { path: 'src' })
      addError(errors, 'missing-source-tree', 'canonical src tree is missing')
      sourceTreeMissing = true
    }
    if (!sourceTreeMissing && canonicalStats.isSymbolicLink()) {
      addError(errors, 'source-filesystem', 'source root must not be a symbolic link: src', { path: 'src' })
      sourceTreeMissing = true
    } else if (!sourceTreeMissing && !canonicalStats.isDirectory()) {
      addError(errors, 'missing-source-tree', 'canonical src tree is missing')
      sourceTreeMissing = true
    }
  } else if (srcRoots.every((rel) => !existsSync(resolve(root, rel)))) {
    addError(errors, 'missing-source-tree', 'no applicable src tree')
    sourceTreeMissing = true
  }

  let scannedFileCount = 0
  let currentCounts = new Map()
  let appliedExceptions = []
  let appliedBoundaries = []
  if (!errors.some((error) => FATAL_SETUP.has(error.code)) && !sourceTreeMissing) {
    const files = collectSourceFiles(root, srcRoots, errors)
    scannedFileCount = files.length
    const findings = runScanners({ files, scanners, policy: policy ?? { tokenCssNames: [], resolvedTokens: {} }, catalog, errors })
    const accounted = accountFindings({
      findings,
      ledger,
      extraReviewed: extraReviewedPaths(root, ledger),
      errors,
    })
    currentCounts = accounted.counts
    appliedExceptions = accounted.appliedExceptions
    appliedBoundaries = accounted.appliedBoundaries
    if (baseline && Array.isArray(baseline.fingerprints)) compareDebt(currentCounts, baseline, errors)
  }

  const report = {
    ok: errors.length === 0,
    mode: 'audit',
    checkpoint,
    scannedFileCount,
    errorCount: errors.length,
    errors,
    debt: summarizeDebt(currentCounts, baseline),
    appliedExceptions,
    appliedBoundaries,
  }
  if (options.reportPath) writeReport(options.reportPath, report)
  return report
}

export async function runCli(argv, extras = {}) {
  const args = parseArgs(argv)
  const errors = []
  for (const flag of ['baseline', 'ledger', 'checkpoint', 'report']) {
    if (!args[flag]) addError(errors, 'missing-argument', `missing --${flag}`)
  }
  const stderr = extras.stderr ?? process.stderr
  if (errors.length) {
    if (args.report) writeReport(args.report, { ok: false, mode: 'audit', checkpoint: args.checkpoint ?? null, scannedFileCount: 0, errorCount: errors.length, errors })
    stderr.write(`design-system-locks audit: FAIL (${errors.length} errors)\n`)
    return 1
  }

  const policyErrors = []
  let policy = extras.policy
  if (policy === undefined && args.policy) {
    const loaded = readJson(args.policy, 'policy', policyErrors)
    if (loaded && loaded.deleted === true) policy = { deleted: true }
    else if (loaded) policy = loaded
  }

  const baseline = extras.baseline ?? readJson(args.baseline, 'baseline', errors)
  const ledger = extras.ledger ?? readJson(args.ledger, 'ledger', errors)
  if (policyErrors.length) errors.push(...policyErrors)

  if (errors.length) {
    writeReport(args.report, { ok: false, mode: 'audit', checkpoint: args.checkpoint, scannedFileCount: 0, errorCount: errors.length, errors })
    stderr.write(`design-system-locks audit: FAIL (${errors.length} errors)\n`)
    return 1
  }

  const report = await audit({
    root: args.root ?? extras.root,
    baseline,
    ledger,
    checkpoint: args.checkpoint,
    policy,
    scanners: extras.scanners,
    scannerDir: args['scanner-dir'] ?? extras.scannerDir,
    srcRoots: extras.srcRoots,
    reportPath: args.report,
  })

  if (args.coverage || extras.coverage) {
    const evidence = (args.evidence ?? extras.evidencePaths ?? '').split(',').map((item) => item.trim()).filter(Boolean)
    const coverage = await composeCoverage({
      root: args.root ?? extras.root,
      indexPath: args.index ?? extras.indexPath,
      evidencePaths: evidence,
      coverageModule: extras.coverageModule,
      coverageDir: args['scanner-dir'] ?? extras.scannerDir,
    })
    for (const message of coverage.errors ?? []) addError(report.errors, 'coverage', String(message))
    report.errorCount = report.errors.length
    report.ok = report.errors.length === 0
    writeReport(args.report, report)
  }

  stderr.write(`design-system-locks audit: ${report.ok ? 'PASS' : 'FAIL'} (${report.errorCount} errors)\n`)
  return report.ok ? 0 : 1
}

function isDirectCli(argv1 = process.argv[1]) {
  if (!argv1) return false
  try {
    return fileURLToPath(import.meta.url) === resolve(argv1)
  } catch {
    return false
  }
}

if (isDirectCli()) {
  runCli(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (error) => {
      process.stderr.write(`${error.stack || error.message}\n`)
      process.exit(1)
    },
  )
}
