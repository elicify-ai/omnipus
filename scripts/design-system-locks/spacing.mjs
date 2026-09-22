
import path from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'
import {
  RULE,
  pushFinding,
} from './spacing/ast-utils.mjs'
import {
  ABSOLUTE_UNIT_TO_PX,
  CONSTITUTIONAL_SCALE,
  SPACING_PROPERTIES,
  analyzeClassList,
  analyzeSpacingValue,
  canonicalDecl,
  parseDimension,
} from './spacing/value-rules.mjs'
import {
  visitNode,
} from './spacing/ast-walk.mjs'

export const extensions = ['.css', '.js', '.jsx', '.ts', '.tsx']

export function scan(input = {}) {
  const filePath = input.path
  const source = input.source
  if (typeof filePath !== 'string' || filePath.length === 0) {
    throw new Error('path must be a non-empty string')
  }
  if (typeof source !== 'string') {
    throw new Error('source must be a string')
  }
  if (source.length === 0) return []
  const ctx = createContext(filePath, input.policy, input.modules)
  const ext = path.posix.extname(filePath).toLowerCase()
  if (ext === '.css') return scanCss(source, ctx)
  if (ext === '.ts' || ext === '.tsx' || ext === '.js' || ext === '.jsx') {
    return scanScript(source, ctx, ext)
  }
  pushFinding(ctx, RULE.parseError, '<unparseable>', `Failed to parse source for spacing analysis; unsupported extension ${ext}.`, { line: 1, column: 1 })
  return ctx.findings
}

export function createContext(filePath, policy, modules) {
  const resolved = policy && typeof policy === 'object' && policy.resolvedTokens && typeof policy.resolvedTokens === 'object'
    ? policy.resolvedTokens
    : {}
  const names = new Set(Array.isArray(policy?.tokenCssNames) ? policy.tokenCssNames : [])
  return {
    path: filePath,
    tokenCssNames: names,
    scale: new Set(CONSTITUTIONAL_SCALE),
    tokenPx: tokenPxFromPolicy(policy, resolved),
    modules,
    moduleCache: new Map(),
    findings: [],
  }
}

// Contract (design-system/enforcement/contract.json, "policy.resolvedCssTokens"):
// "Do not derive CSS names from token IDs." resolvedCssTokens is the exact,
// contract-mandated CSS-custom-property-name to resolved-value map policy.mjs
// always supplies in production -- preferred whole, never merged with the id-
// derived fallback, since the contract requires it to cover every
// tokenCssNames entry exactly when present. The id-derived path
// (cssNameFromTokenId) below is kept ONLY for isolated test fixtures that
// still hand this scanner a bare `resolvedTokens` (id-keyed) policy shape
// with no resolvedCssTokens at all (contract: "Optional for fixture
// compatibility") -- e.g. this file's own "policy fallback" describe block.
// It is fundamentally unable to recover every registered token correctly: at
// least two D10 scale ids in foundations.json are named after their pixel
// VALUE rather than their scale index (space.scale.2px -> --space-0-5,
// space.scale.12px -> --space-2-5), which the id-only regex cannot invert.
// Fixing that in the fallback itself would require a real id->css lookup
// table this file does not have; preferring resolvedCssTokens is the actual
// fix; the fallback stays best-effort for its narrower legacy fixture role.
export function tokenPxFromPolicy(policy, resolved) {
  const cssTokens = policy && typeof policy === 'object' && policy.resolvedCssTokens && typeof policy.resolvedCssTokens === 'object'
    ? policy.resolvedCssTokens
    : null
  const map = new Map()
  if (cssTokens) {
    for (const [css, value] of Object.entries(cssTokens)) {
      const px = pixelTokenValue(value)
      if (px !== null) map.set(css, px)
    }
    return map
  }
  for (const [id, value] of Object.entries(resolved)) {
    const css = cssNameFromTokenId(id)
    const px = pixelTokenValue(value)
    if (css && px !== null) map.set(css, px)
  }
  return map
}

export function cssNameFromTokenId(id) {
  const scale = /^space\.scale\.(\d+)$/.exec(id)
  if (scale) return `--space-${scale[1]}`
  return `--${String(id).replace(/[A-Z]/g, (ch) => `-${ch.toLowerCase()}`).replace(/\./g, '-')}`
}

export function pixelTokenValue(value) {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'string') return null
  const dim = parseDimension(value.trim(), { unitlessIsPx: true })
  if (!dim) return null
  if (dim.number === 0) return 0
  if (dim.unit === 'rem') return null
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) return dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit]
  return null
}

export function scanCss(source, ctx) {
  let root
  try {
    root = postcss.parse(source, { from: ctx.path })
  } catch (error) {
    if (error.name === 'CssSyntaxError') {
      pushFinding(
        ctx,
        RULE.parseError,
        '<unparseable>',
        `Failed to parse source for spacing analysis${error.reason ? `: ${error.reason}` : '.'}`,
        { line: error.line, column: error.column },
      )
      return ctx.findings
    }
    throw error
  }
  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    if (!SPACING_PROPERTIES.has(prop)) return
    const syntax = canonicalDecl(prop, decl.value)
    const loc = locFromPostcss(decl)
    analyzeSpacingValue(decl.value, ctx, syntax, loc, { unitlessIsPx: false })
  })
  root.walkAtRules((rule) => {
    if (rule.name.toLowerCase() !== 'apply') return
    analyzeClassList(rule.params, ctx, locFromPostcss(rule))
  })
  return ctx.findings
}

export function locFromPostcss(node) {
  return { line: node.source?.start?.line, column: node.source?.start?.column }
}

export function scanScript(source, ctx, ext) {
  const scriptKind = ext === '.ts' ? ts.ScriptKind.TS
    : ext === '.tsx' ? ts.ScriptKind.TSX
      : ext === '.jsx' ? ts.ScriptKind.JSX
        : ts.ScriptKind.JS
  const file = ts.createSourceFile(ctx.path, source, ts.ScriptTarget.Latest, true, scriptKind)
  const diagnostics = file.parseDiagnostics ?? []
  for (const diagnostic of diagnostics) {
    if (diagnostic.category !== ts.DiagnosticCategory.Error) continue
    const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')
    const pos = diagnostic.start ?? 0
    const { line, character } = file.getLineAndCharacterOfPosition(pos)
    pushFinding(
      ctx,
      RULE.parseError,
      '<unparseable>',
      `Failed to parse source for spacing analysis: ${message}`,
      { line: line + 1, column: character + 1 },
    )
  }
  visitNode(file, ctx, file)
  return ctx.findings
}
