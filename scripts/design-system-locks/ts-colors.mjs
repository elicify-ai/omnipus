import ts from 'typescript'
import {
  MODULE_CACHES,
  indexModuleRecord,
  prepareSource,
  scriptKindFor,
  tokenSet,
  walk,
} from './ts-colors/scope-and-jsx.mjs'
import {
  emitParse,
  sortFindings,
} from './ts-colors/value-analysis.mjs'
export { CANONICAL_GENERATED_TOKEN_PATHS } from './ts-colors/scope-and-jsx.mjs'

export function scan(input = {}) {
  const path = input.path
  const source = input.source
  if (typeof path !== 'string' || path.length === 0) {
    throw new TypeError('path must be a non-empty string')
  }
  if (typeof source !== 'string') {
    throw new TypeError('source must be a string')
  }

  const prepared = prepareSource(path, source)
  const sourceFile = ts.createSourceFile(path, prepared, ts.ScriptTarget.Latest, true, scriptKindFor(path))
  const modules = input.modules
  let moduleCache = new Map()
  if (modules && typeof modules === 'object') {
    moduleCache = MODULE_CACHES.get(modules) ?? moduleCache
    if (!MODULE_CACHES.has(modules)) MODULE_CACHES.set(modules, moduleCache)
  }
  const ctx = {
    path,
    sourceFile,
    tokens: tokenSet(input.policy),
    scopes: [],
    callFrames: [],
    findings: [],
    seen: new Set(),
    modules,
    moduleCache,
    sourceRecords: new Map(),
    // scanHookStateLaundering's independent, silent scan of a useState
    // initializer/setter argument — never emit ts-colors/unsupported for
    // the opaque/runtime case there (the read-site boundary already covers
    // it); a raw literal still reports normally through analyzeCssValue,
    // which does not consult this flag at all.
    silentUnsupported: false,
  }
  ctx.sourceRecords.set(sourceFile, indexModuleRecord(path, sourceFile))

  for (const diagnostic of sourceFile.parseDiagnostics ?? []) {
    if (diagnostic.category === ts.DiagnosticCategory.Error) emitParse(ctx, diagnostic)
  }

  walk(sourceFile, ctx)
  sortFindings(ctx.findings)
  return ctx.findings
}

export const extensions = Object.freeze(['.js', '.jsx', '.ts', '.tsx', '.svg'])
