import ts from 'typescript'
import postcss from 'postcss'
import { posix } from 'node:path'

export const extensions = ['.css', '.js', '.jsx', '.mjs', '.cjs', '.ts', '.tsx', '.svg']

export const RULE = {
  mismatch: 'design-system/status-mismatch',
  literal: 'design-system/status-literal',
  unsupported: 'design-system/status-unsupported',
  parseError: 'design-system/status-parse-error',
}

const D4_HEX = {
  inbox: '#9CA3AF',
  next: '#3B82F6',
  'in-progress': '#D4AF37',
  blocked: '#F97316',
  done: '#10B981',
  failed: '#EF4444',
  cancelled: '#EAB308',
}

const TOKEN_FOR = {
  inbox: '--color-status-inbox',
  next: '--color-status-next',
  'in-progress': '--color-status-in-progress',
  blocked: '--color-status-blocked',
  done: '--color-status-done',
  failed: '--color-status-failed',
  cancelled: '--color-status-cancelled',
}

const SEMANTIC_CSS = {
  '--color-muted': D4_HEX.inbox,
  '--color-info': D4_HEX.next,
  '--color-accent': D4_HEX['in-progress'],
  '--color-blocked': D4_HEX.blocked,
  '--color-success': D4_HEX.done,
  '--color-error': D4_HEX.failed,
  '--color-warning': D4_HEX.cancelled,
  '--color-cancelled': D4_HEX.cancelled,
}

const ALIAS_TO_CANONICAL = buildAliasMap()
const COLOR_PROP = /^(bg|background|background-color|backgroundColor|color|fill|stroke|border|border-color|borderColor|outline-color|accent|hex|value|stop-color)$/i
const TAILWIND_HUE = /\b(?:bg|text|fill|stroke|border|ring|from|to|via|outline|decoration|accent|caret)-(?:slate|gray|grey|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)(?:-\d{2,3})?\b/g
const HEX_RE = /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b/g
const RGB_RE = /rgba?\(\s*[\d.]+\s*[,\s][\d.\s,/]+\)/gi
const VAR_RE = /var\(\s*(--[A-Za-z0-9-]+)\s*\)/g
const STATUS_ATTR_RE = /\[\s*(?:data-)?(?:task-)?status\s*=\s*["']([^"']+)["']/gi
const STATUS_CLASS_RE = /(?:^|[\s.#[])status[-_]([A-Za-z0-9_-]+)/g
const BINDING_RE = /status|palette|badge|visuals?|chipstyle|state[_-]?colors/i
const SINGLE_STATUS_RE = /cancelled|canceled|in[_-]?progress/i
const PAINT_BINDING_RE = /colou?r|palette|hex|fill|stroke|background|chipstyle/i
const PRINTER = ts.createPrinter({ removeComments: true, newLine: ts.NewLineKind.LineFeed })

export function scan({ path = '', source = '', policy, modules } = {}) {
  const filePath = String(path).replaceAll('\\', '/')
  const text = typeof source === 'string' ? source : String(source ?? '')
  const ctx = { path: filePath, source: text, findings: [], maps: buildPolicyMaps(policy), modules, moduleCache: new Map() }
  const ext = extensionOf(filePath)
  if (!extensions.includes(ext)) {
    return [makeFinding(ctx, RULE.parseError, ext ? `unparseable-extension:${ext}` : 'unparseable-extension', `Unsupported file extension ${ext || '(none)'}; status coverage cannot be proven.`, 1, 1)]
  }
  if (ext === '.css') return scanCss(ctx)
  if (ext === '.svg') return scanSvg(ctx)
  return scanTypeScript(ctx, scriptKindFor(ext))
}

function buildAliasMap() {
  const map = new Map()
  const add = (alias, canonical) => map.set(alias.toLowerCase(), canonical)
  for (const canonical of Object.keys(D4_HEX)) {
    add(canonical, canonical)
    add(canonical.replaceAll('-', '_'), canonical)
    add(canonical.replaceAll('-', ''), canonical)
  }
  add('in progress', 'in-progress')
  add('success', 'done')
  add('error', 'failed')
  add('danger', 'failed')
  add('canceled', 'cancelled')
  add('draft', 'inbox')
  add('approved', 'next')
  add('running', 'in-progress')
  add('planning', 'next')
  return map
}

function canonicalStatus(name) {
  if (typeof name !== 'string' || name.length === 0) return null
  return ALIAS_TO_CANONICAL.get(name.trim().toLowerCase().replaceAll(' ', '-')) ?? null
}

function extensionOf(filePath) {
  const base = filePath.split('/').pop() ?? ''
  const index = base.lastIndexOf('.')
  return index === -1 ? '' : base.slice(index).toLowerCase()
}

function scriptKindFor(ext) {
  if (ext === '.tsx') return ts.ScriptKind.TSX
  if (ext === '.jsx') return ts.ScriptKind.JSX
  if (ext === '.ts') return ts.ScriptKind.TS
  return ts.ScriptKind.JS
}

function buildPolicyMaps(policy) {
  const cssToHex = new Map(Object.entries(SEMANTIC_CSS))
  const tokenCssNames = new Set(policy?.tokenCssNames ?? [])
  for (const [id, value] of Object.entries(policy?.resolvedTokens ?? {})) {
    if (typeof value !== 'string') continue
    const hex = normalizeHex(value) ?? extractFirstHex(value)
    if (!hex) continue
    const css = `--${id.replaceAll('.', '-')}`
    cssToHex.set(css, hex)
    if (id.endsWith('.default')) cssToHex.set(`--${id.slice(0, -'.default'.length).replaceAll('.', '-')}`, hex)
  }
  for (const [canonical, hex] of Object.entries(D4_HEX)) {
    cssToHex.set(TOKEN_FOR[canonical], hex)
  }
  return { cssToHex, tokenCssNames }
}

function makeFinding(ctx, ruleId, syntax, message, line, column) {
  const finding = { ruleId, path: ctx.path, syntax: canonicalize(syntax), message }
  if (Number.isInteger(line) && line >= 1) finding.line = line
  if (Number.isInteger(column) && column >= 1) finding.column = column
  return finding
}

function pushFinding(ctx, ruleId, syntax, message, line, column) {
  if (!syntax) return
  ctx.findings.push(makeFinding(ctx, ruleId, syntax, message, line, column))
}

function canonicalize(text) {
  return String(text)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/.*$/gm, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/#([0-9a-fA-F]{3,8})\b/g, (_, hex) => normalizeHex(`#${hex}`) ?? `#${hex.toUpperCase()}`)
    .replace(/'/g, '"')
    .replace(/;+\s*$/g, '')
    .replace(/,\s*}/g, ' }')
}

function loc(sf, node) {
  const { line, character } = sf.getLineAndCharacterOfPosition(node.getStart(sf, false))
  return { line: line + 1, column: character + 1 }
}

function printTs(sf, node) {
  return canonicalize(PRINTER.printNode(ts.EmitHint.Unspecified, node, sf))
}

function normalizeHex(value) {
  if (typeof value !== 'string') return null
  const match = value.trim().match(/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/)
  if (!match) return null
  let hex = match[1].toUpperCase()
  if (hex.length === 3) hex = hex.split('').map((char) => char + char).join('')
  if (hex.length === 8) hex = hex.slice(0, 6)
  return `#${hex}`
}

function parseRgb(value) {
  const match = value.replace(/\s+/g, ' ').match(/^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/i)
  if (!match) return null
  const rgb = [match[1], match[2], match[3]].map((part) => Math.max(0, Math.min(255, Math.round(Number(part)))))
  if (rgb.some((part) => Number.isNaN(part))) return null
  return `#${rgb.map((part) => part.toString(16).padStart(2, '0')).join('').toUpperCase()}`
}

function extractFirstHex(value) {
  const hex = value.match(HEX_RE)
  return hex ? normalizeHex(hex[0]) : null
}

function extractColors(text) {
  if (typeof text !== 'string' || text.length === 0) return []
  const found = []
  for (const match of text.matchAll(HEX_RE)) found.push({ kind: 'hex', raw: match[0], hex: normalizeHex(match[0]) })
  for (const match of text.matchAll(RGB_RE)) found.push({ kind: 'hex', raw: match[0], hex: parseRgb(match[0]) })
  for (const match of text.matchAll(VAR_RE)) found.push({ kind: 'token', raw: match[0], css: match[1] })
  for (const match of text.matchAll(TAILWIND_HUE)) found.push({ kind: 'tailwind', raw: match[0] })
  return found.filter((color) => color.kind !== 'hex' || color.hex)
}

function isStatusBindingName(name) {
  if (typeof name !== 'string' || name.length === 0) return false
  return BINDING_RE.test(name) || SINGLE_STATUS_RE.test(name)
}

function isPaintBindingName(name) {
  return typeof name === 'string' && name.length > 0 && PAINT_BINDING_RE.test(name)
}

function isPaletteBindingName(name) {
  return isStatusBindingName(name) && isPaintBindingName(name)
}

function isStaticPaletteNode(node) {
  return Boolean(node && (ts.isObjectLiteralExpression(node) || ts.isArrayLiteralExpression(node)))
}

function statusKeyFromBindingName(name) {
  if (typeof name !== 'string') return null
  const parts = name.split(/[^A-Za-z0-9]+/).filter(Boolean)
  for (const part of parts) {
    const canonical = canonicalStatus(part)
    if (canonical) return canonical
  }
  if (/cancelled|canceled/i.test(name)) return 'cancelled'
  if (/in[_-]?progress/i.test(name)) return 'in-progress'
  return null
}

function statusOwnerOfCss(cssName) {
  const ordered = Object.keys(D4_HEX).sort((left, right) => right.length - left.length)
  for (const canonical of ordered) {
    if (cssName === TOKEN_FOR[canonical]) return canonical
    if (cssName.startsWith(`--status-${canonical}-`) || cssName.startsWith(`--color-status-${canonical}-`)) return canonical
    const alias = canonical.replaceAll('-', '_')
    if (cssName === `--color-status-${alias}` || cssName.startsWith(`--status-${alias}-`)) return canonical
  }
  return null
}

function statusKeyFromCustomProp(prop) {
  return statusOwnerOfCss(prop)
}

function statusKeysFromSelector(selector) {
  const keys = new Set()
  for (const match of selector.matchAll(STATUS_ATTR_RE)) {
    const canonical = canonicalStatus(match[1])
    if (canonical) keys.add(canonical)
  }
  for (const match of selector.matchAll(STATUS_CLASS_RE)) {
    const canonical = canonicalStatus(match[1])
    if (canonical) keys.add(canonical)
  }
  return [...keys]
}

function unwrap(node) {
  if (!node) return node
  if (ts.isAsExpression(node) || ts.isSatisfiesExpression(node) || ts.isParenthesizedExpression(node) || ts.isNonNullExpression(node)) {
    return unwrap(node.expression)
  }
  if (typeof ts.isTypeAssertionExpression === 'function' && ts.isTypeAssertionExpression(node)) return unwrap(node.expression)
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object') {
    const method = node.expression.name.text
    if (['freeze', 'seal', 'preventExtensions'].includes(method) && node.arguments[0]) return unwrap(node.arguments[0])
  }
  return node
}

function foldString(node, sf) {
  const core = unwrap(node)
  if (!core) return null
  if (ts.isStringLiteral(core) || ts.isNoSubstitutionTemplateLiteral(core)) return core.text
  if (ts.isIdentifier(core)) return resolveConstString(core, sf)
  if (ts.isBinaryExpression(core) && core.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = foldString(core.left, sf)
    const right = foldString(core.right, sf)
    if (left !== null && right !== null) return left + right
  }
  return null
}

function resolveConstInitializer(identifier, sf) {
  let found = null
  function visit(node) {
    if (found !== null) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === identifier.text && node.initializer) {
      found = unwrap(node.initializer)
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return found
}

function resolveConstString(identifier, sf) {
  const initializer = resolveConstInitializer(identifier, sf)
  if (!initializer) return null
  if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer)) return initializer.text
  return null
}

function resolveKey(nameNode, sf) {
  if (ts.isIdentifier(nameNode) || ts.isPrivateIdentifier(nameNode)) return nameNode.text
  if (ts.isStringLiteral(nameNode) || ts.isNoSubstitutionTemplateLiteral(nameNode) || ts.isNumericLiteral(nameNode)) return nameNode.text
  if (ts.isComputedPropertyName(nameNode)) return foldString(nameNode.expression, sf)
  return null
}

function isColorLikeNode(node) {
  const core = unwrap(node)
  if (!core) return false
  if (ts.isStringLiteral(core) || ts.isNoSubstitutionTemplateLiteral(core)) return extractColors(core.text).length > 0
  if (ts.isTemplateExpression(core)) return extractColors(core.getText()).length > 0
  return tokenIdFromNode(core) !== null
}

function isColorPropertyName(name) {
  return COLOR_PROP.test(name) || /(?:^|-)color$/i.test(name) || name === 'background' || name === 'bg'
}

function hasColorishProp(objectNode) {
  return objectNode.properties.some((prop) => ts.isPropertyAssignment(prop) && isColorPropertyName(resolveKey(prop.name, prop.getSourceFile()) ?? ''))
}

function tokenIdFromNode(node) {
  const core = unwrap(node)
  if (!core || !ts.isElementAccessExpression(core) || !ts.isIdentifier(core.expression)) return null
  if (core.expression.text !== 'tokens' && core.expression.text !== 'resolvedTokens') return null
  const arg = core.argumentExpression
  if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) return arg.text
  return null
}

function canonicalFromTokenId(id) {
  const match = id.match(/^(?:color|component)\.status\.([^.]+)/)
  return match ? canonicalStatus(match[1]) : null
}

function classifyOutcome(statusKey, colors, maps) {
  let mismatch = false
  let literal = false
  let unsupported = false
  for (const color of colors) {
    const outcome = classifyOne(statusKey, color, maps)
    if (outcome === 'mismatch') mismatch = true
    else if (outcome === 'literal') literal = true
    else if (outcome === 'unsupported') unsupported = true
  }
  if (mismatch) return 'mismatch'
  if (literal) return 'literal'
  if (unsupported) return 'unsupported'
  return null
}

function classifyOne(statusKey, color, maps) {
  if (color.kind === 'tailwind') return 'mismatch'
  if (color.kind === 'token') {
    const statusPaint = statusOwnerOfCss(color.css) !== null || Object.hasOwn(SEMANTIC_CSS, color.css)
    if (!statusPaint) return isRegisteredToken(color.css, maps) ? null : 'unsupported'
    return classifyTokenCss(statusKey, color.css, maps)
  }
  if (color.kind === 'hex' && color.hex) return color.hex === D4_HEX[statusKey] ? 'literal' : 'mismatch'
  return 'unsupported'
}

function isRegisteredToken(cssName, maps) {
  return maps.tokenCssNames.has(cssName) || Object.hasOwn(SEMANTIC_CSS, cssName) || statusOwnerOfCss(cssName) !== null
}

function classifyTokenCss(statusKey, cssName, maps) {
  const owner = statusOwnerOfCss(cssName)
  if (owner && owner !== statusKey) return 'mismatch'
  if (owner === statusKey) return null
  if (!isRegisteredToken(cssName, maps)) return 'unsupported'
  const hex = maps.cssToHex.get(cssName)
  if (!hex) return 'unsupported'
  return hex === D4_HEX[statusKey] ? null : 'mismatch'
}

function classifyTokenId(statusKey, id, maps) {
  const owner = canonicalFromTokenId(id)
  if (owner && owner !== statusKey) return 'mismatch'
  if (owner === statusKey) return null
  const css = `--${id.replaceAll('.', '-')}`
  return classifyTokenCss(statusKey, css, maps)
}

function outcomeMessage(ruleId, statusKey, detail) {
  const token = TOKEN_FOR[statusKey]
  const hex = D4_HEX[statusKey]
  if (ruleId === RULE.mismatch) return `D4 status "${statusKey}" must use ${hex} via ${token}; found ${detail}. This is a colour mismatch, not a second palette.`
  if (ruleId === RULE.literal) return `D4 status "${statusKey}" embeds palette literal ${hex}; use token ${token} instead of an independently copied hex.`
  if (ruleId === RULE.unsupported) return `Unsupported dynamic status palette coverage for "${statusKey}": ${detail}. Missing graph coverage is a finding, not success.`
  return detail
}

function reportOutcome(env, node, statusKey, outcome, detail) {
  if (!outcome) return
  const ruleId = RULE[outcome]
  const { line, column } = loc(env.sf, node)
  pushFinding(env.ctx, ruleId, printTs(env.sf, node), outcomeMessage(ruleId, statusKey, detail), line, column)
}

function modulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/')
    ? `src/${specifier.slice(2)}`
    : specifier.startsWith('.')
      ? posix.normalize(posix.join(posix.dirname(from), specifier))
      : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

function moduleRecord(path, env) {
  if (env.ctx.moduleCache.has(path)) return env.ctx.moduleCache.get(path)
  const source = env.ctx.modules?.[path]
  if (typeof source !== 'string') return null
  const sf = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, scriptKindFor(extensionOf(path)))
  const record = { path, sf, exports: new Map() }
  env.ctx.moduleCache.set(path, record)
  for (const statement of sf.statements) {
    if (ts.isVariableStatement(statement) && statement.modifiers?.some((item) => item.kind === ts.SyntaxKind.ExportKeyword)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name) && declaration.initializer) record.exports.set(declaration.name.text, declaration)
    } else if (ts.isFunctionDeclaration(statement) && statement.name && statement.modifiers?.some((item) => item.kind === ts.SyntaxKind.ExportKeyword)) {
      record.exports.set(statement.name.text, statement)
    }
  }
  return record
}

function importedDeclaration(identifier, sf, env) {
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const clause = statement.importClause
    const binding = clause?.namedBindings
    if (!binding || !ts.isNamedImports(binding)) continue
    for (const element of binding.elements) {
      if (element.name.text !== identifier.text) continue
      const path = modulePath(env.ctx.path, statement.moduleSpecifier.text, env.ctx.modules)
      if (!path) return { missing: statement.moduleSpecifier.text }
      const record = moduleRecord(path, env)
      return { record, declaration: record?.exports.get(element.propertyName?.text ?? element.name.text) ?? null }
    }
  }
  return null
}

function localDeclaration(identifier, record) {
  return record?.exports.get(identifier.text) ?? null
}

function paintOutcomes(node, statusKey, env, record = null, seen = new Set()) {
  const core = unwrap(node)
  if (!core) return null
  if (ts.isBinaryExpression(core) && core.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken) {
    const primary = paintOutcomes(core.left, statusKey, env, record, seen)
    return primary ?? paintOutcomes(core.right, statusKey, env, record, seen)
  }
  if ((ts.isElementAccessExpression(core) || ts.isPropertyAccessExpression(core)) && ts.isIdentifier(core.expression)) {
    const resolved = record ? { record, declaration: localDeclaration(core.expression, record) } : importedDeclaration(core.expression, env.sf, env)
    if (!resolved) return null
    if (resolved.missing || !resolved.declaration) return ['unsupported']
    const initializer = unwrap(resolved.declaration.initializer)
    if (!initializer || !ts.isObjectLiteralExpression(initializer)) return ['unsupported']
    const outcomes = []
    const selectedKey = ts.isPropertyAccessExpression(core) ? canonicalStatus(core.name.text) : null
    if (selectedKey && statusKey && selectedKey !== statusKey) return ['mismatch']
    for (const prop of initializer.properties) {
      if (!ts.isPropertyAssignment(prop)) continue
      const key = canonicalStatus(resolveKey(prop.name, resolved.record.sf) ?? '')
      if (!key || (selectedKey ? key !== selectedKey : statusKey && key !== statusKey)) continue
      const colors = extractColors(printTs(resolved.record.sf, prop.initializer))
      const outcome = colors.length ? classifyOutcome(key, colors, env.ctx.maps) : 'unsupported'
      outcomes.push(outcome === 'literal' ? null : outcome)
    }
    return outcomes.length ? outcomes : ['unsupported']
  }
  if (ts.isIdentifier(core)) {
    const resolved = record ? { record, declaration: localDeclaration(core, record) } : importedDeclaration(core, env.sf, env)
    if (!resolved) return null
    if (resolved.missing || !resolved.declaration || !resolved.declaration.initializer) return ['unsupported']
    const ownKey = statusKeyFromBindingName(core.text)
    return paintOutcomes(resolved.declaration.initializer, ownKey ?? statusKey, env, resolved.record, seen)
      ?? (() => {
        const colors = extractColors(printTs(resolved.record.sf, resolved.declaration.initializer))
        const outcome = colors.length ? classifyOutcome(ownKey ?? statusKey, colors, env.ctx.maps) : 'unsupported'
        return [outcome === 'literal' ? null : outcome]
      })()
  }
  if (ts.isCallExpression(core) && ts.isIdentifier(core.expression)) {
    const resolved = record ? { record, declaration: localDeclaration(core.expression, record) } : importedDeclaration(core.expression, env.sf, env)
    if (!resolved) return null
    if (resolved.missing || !resolved.declaration || !ts.isFunctionDeclaration(resolved.declaration) || !resolved.declaration.body) return ['unsupported']
    const marker = `${resolved.record.path}#${core.expression.text}`
    if (seen.has(marker)) return ['unsupported']
    const nextSeen = new Set(seen).add(marker)
    const returns = []
    const visit = (child) => {
      if (ts.isFunctionLike(child) && child !== resolved.declaration) return
      if (ts.isReturnStatement(child) && child.expression) returns.push(child.expression)
      else ts.forEachChild(child, visit)
    }
    visit(resolved.declaration.body)
    const outcomes = []
    for (const expression of returns) {
      const parts = ts.isConditionalExpression(unwrap(expression)) ? [unwrap(expression).whenTrue, unwrap(expression).whenFalse] : [expression]
      for (const part of parts) outcomes.push(...(paintOutcomes(part, statusKey, env, resolved.record, nextSeen) ?? ['unsupported']))
    }
    return outcomes.length ? outcomes : ['unsupported']
  }
  return null
}

function classifyGovernedModulePaint(node, statusKey, env, syntaxNode) {
  const outcomes = paintOutcomes(node, statusKey, env)
  if (!outcomes) return false
  const outcome = outcomes.includes('mismatch') ? 'mismatch' : outcomes.includes('literal') ? 'literal' : outcomes.includes('unsupported') ? 'unsupported' : null
  reportOutcome(env, syntaxNode, statusKey, outcome, 'governed imported status paint')
  return true
}

function classifyValue(node, statusKey, env, syntaxNode = node) {
  const core = unwrap(node)
  if (!core) return
  const tokenId = tokenIdFromNode(core)
  if (tokenId) {
    reportOutcome(env, syntaxNode, statusKey, classifyTokenId(statusKey, tokenId, env.ctx.maps), tokenId)
    return
  }
  if (classifyGovernedModulePaint(core, statusKey, env, syntaxNode)) return
  if (ts.isObjectLiteralExpression(core)) {
    classifyColorObject(core, statusKey, env, syntaxNode)
    return
  }
  if (ts.isStringLiteral(core) || ts.isNoSubstitutionTemplateLiteral(core)) {
    const colors = extractColors(core.text)
    if (colors.length === 0) return
    reportOutcome(env, syntaxNode, statusKey, classifyOutcome(statusKey, colors, env.ctx.maps), colors.map((color) => color.raw).join(', '))
    return
  }
  if (ts.isTemplateExpression(core)) {
    if (core.templateSpans.length > 0 && core.templateSpans.every((span) => classifyGovernedModulePaint(span.expression, statusKey, env, syntaxNode))) return
    reportOutcome(env, syntaxNode, statusKey, 'unsupported', 'template expression')
    return
  }
  if (ts.isIdentifier(core)) {
    const resolved = resolveConstInitializer(core, env.sf)
    if (resolved && resolved !== core) classifyValue(resolved, statusKey, env, syntaxNode)
    else if (env.inStatusMap) reportOutcome(env, syntaxNode, statusKey, 'unsupported', `unresolved identifier ${core.text}`)
  }
}

function classifyColorObject(objectNode, statusKey, env, syntaxNode) {
  for (const prop of objectNode.properties) {
    if (!ts.isPropertyAssignment(prop)) {
      if (ts.isSpreadAssignment(prop)) reportUnsupported(env, prop, 'spread colour object')
      continue
    }
    const key = resolveKey(prop.name, env.sf)
    if (key && isColorPropertyName(key)) classifyValue(prop.initializer, statusKey, env, syntaxNode ?? prop)
  }
}

function reportUnsupported(env, node, detail) {
  const { line, column } = loc(env.sf, node)
  pushFinding(env.ctx, RULE.unsupported, printTs(env.sf, node), `Unsupported dynamic status palette coverage: ${detail}. Missing graph coverage is a finding, not success.`, line, column)
}

function scanTypeScript(ctx, scriptKind) {
  const sf = ts.createSourceFile(ctx.path || 'file.ts', ctx.source, ts.ScriptTarget.Latest, true, scriptKind)
  const diagnostics = sf.parseDiagnostics ?? []
  if (diagnostics.length > 0) {
    const diagnostic = diagnostics[0]
    const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')
    const { line, character } = sf.getLineAndCharacterOfPosition(diagnostic.start ?? 0)
    ctx.findings.push(makeFinding(ctx, RULE.parseError, 'unparseable-typescript', `Failed to parse TypeScript: ${message}`, line + 1, character + 1))
    return ctx.findings
  }
  walkTs(sf, { sf, ctx, statusKey: null, inStatusMap: false, bindingName: null })
  return ctx.findings
}

function walkTs(node, env) {
  if (ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node)) return
  if (ts.isVariableDeclaration(node)) {
    walkVariable(node, env)
    return
  }
  if (ts.isObjectLiteralExpression(node)) {
    walkObjectLiteral(node, env)
    return
  }
  if (ts.isSwitchStatement(node)) {
    walkSwitch(node, env)
    return
  }
  if (ts.isConditionalExpression(node)) {
    walkConditional(node, env)
    return
  }
  if (ts.isBinaryExpression(node)) {
    walkBinary(node, env)
    return
  }
  if (ts.isJsxElement(node)) {
    walkJsxElement(node, env)
    return
  }
  if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
    walkJsx(node, env)
    return
  }
  if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isMethodDeclaration(node) || ts.isArrowFunction(node)) {
    const name = node.name && ts.isIdentifier(node.name) ? node.name.text : env.bindingName
    const next = { ...env, bindingName: name ?? env.bindingName, inStatusMap: false }
    ts.forEachChild(node, (child) => walkTs(child, next))
    return
  }
  if (ts.isReturnStatement(node) && node.expression && env.statusKey && env.inStatusMap) {
    classifyValue(node.expression, env.statusKey, env)
  }
  ts.forEachChild(node, (child) => walkTs(child, env))
}

function walkVariable(node, env) {
  const name = ts.isIdentifier(node.name) ? node.name.text : env.bindingName
  const paletteBinding = isPaletteBindingName(name)
  const next = { ...env, bindingName: name ?? env.bindingName, inStatusMap: env.inStatusMap || paletteBinding }
  const initializer = node.initializer
  if (initializer) {
    const core = unwrap(initializer)
    const statusNamed = isStatusBindingName(name) || paletteBinding || next.inStatusMap
    if (isColorLikeNode(core)) {
      const key = statusKeyFromBindingName(name) ?? env.statusKey
      if (key && statusNamed) classifyValue(core, key, next, node)
      else if (!key && (paletteBinding || next.inStatusMap)) reportUnsupported(next, node, 'status-named colour binding without a D4 key')
    } else if (paletteBinding && !isStaticPaletteNode(core) && !isDomStyleRead(core) && !classifyGovernedModulePaint(core, statusKeyFromBindingName(name), next, node)) {
      reportUnsupported(next, node, 'status-named binding is not a static palette')
    }
    walkTs(initializer, next)
  }
}

function walkObjectLiteral(node, env) {
  const entries = []
  for (const prop of node.properties) {
    if (ts.isSpreadAssignment(prop)) {
      entries.push({ kind: 'spread', node: prop })
      continue
    }
    if (ts.isShorthandPropertyAssignment(prop)) {
      entries.push({ kind: 'prop', node: prop, key: prop.name.text, value: prop.name, nameNode: prop.name })
      continue
    }
    if (!ts.isPropertyAssignment(prop)) continue
    entries.push({ kind: 'prop', node: prop, key: resolveKey(prop.name, env.sf), value: prop.initializer, nameNode: prop.name })
  }
  const d4Entries = entries.filter((entry) => entry.kind === 'prop' && canonicalStatus(entry.key))
  const nested = d4Entries.some((entry) => {
    const value = unwrap(entry.value)
    return value && ts.isObjectLiteralExpression(value) && hasColorishProp(value)
  })
  const isMap = env.inStatusMap || nested || (isStatusBindingName(env.bindingName) && d4Entries.length >= 1)
  if (isMap) {
    for (const entry of entries) {
      if (entry.kind === 'spread') reportUnsupported(env, entry.node, 'spread')
      else if (entry.key === null && ts.isComputedPropertyName(entry.nameNode)) reportUnsupported(env, entry.node, 'computed property')
    }
  }
  for (const entry of entries) {
    if (entry.kind !== 'prop') continue
    const canonical = canonicalStatus(entry.key)
    const childEnv = {
      ...env,
      inStatusMap: isMap || env.inStatusMap,
      statusKey: isMap ? (canonical ?? env.statusKey) : env.statusKey,
      bindingName: entry.key ?? env.bindingName,
    }
    if (isMap && canonical) classifyValue(entry.value, canonical, childEnv, entry.node)
    walkTs(entry.value, childEnv)
  }
}

function isDomStyleRead(node) {
  const core = unwrap(node)
  return Boolean(
    core
    && ts.isPropertyAccessExpression(core)
    && ts.isPropertyAccessExpression(core.expression)
    && core.expression.name.text === 'style',
  )
}

function walkSwitch(node, env) {
  walkTs(node.expression, env)
  for (const clause of node.caseBlock.clauses) {
    let statusKey = env.statusKey
    if (ts.isCaseClause(clause)) {
      const folded = foldString(clause.expression, env.sf)
      statusKey = (folded && canonicalStatus(folded)) || statusKey
    }
    const next = { ...env, statusKey, inStatusMap: env.inStatusMap || Boolean(statusKey) }
    for (const statement of clause.statements) walkTs(statement, next)
  }
}

function statusKeyFromCondition(node, sf) {
  const core = unwrap(node)
  if (!core || !ts.isBinaryExpression(core)) return null
  const kind = core.operatorToken.kind
  if (kind !== ts.SyntaxKind.EqualsEqualsEqualsToken && kind !== ts.SyntaxKind.EqualsEqualsToken) return null
  return canonicalStatus(foldString(core.left, sf) ?? '') ?? canonicalStatus(foldString(core.right, sf) ?? '')
}

function walkConditional(node, env) {
  walkTs(node.condition, env)
  const statusKey = statusKeyFromCondition(node.condition, env.sf) ?? env.statusKey
  const whenTrue = { ...env, statusKey }
  if (statusKey && isColorLikeNode(node.whenTrue)) classifyValue(node.whenTrue, statusKey, whenTrue)
  walkTs(node.whenTrue, whenTrue)
  walkTs(node.whenFalse, env)
}

function walkBinary(node, env) {
  const operator = node.operatorToken.kind
  if (operator === ts.SyntaxKind.AmpersandAmpersandToken || operator === ts.SyntaxKind.BarBarToken) {
    const statusKey = statusKeyFromCondition(node.left, env.sf) ?? env.statusKey
    walkTs(node.left, env)
    const next = { ...env, statusKey }
    walkTs(node.right, next)
    return
  }
  ts.forEachChild(node, (child) => walkTs(child, env))
}

function jsxNameText(node) {
  if (!node) return ''
  if (ts.isIdentifier(node)) return node.text
  if (typeof ts.isJsxIdentifier === 'function' && ts.isJsxIdentifier(node)) return node.text
  if (typeof ts.isJsxNamespacedName === 'function' && ts.isJsxNamespacedName(node)) {
    return `${jsxNameText(node.namespace)}:${jsxNameText(node.name)}`
  }
  if (ts.isPropertyAccessExpression(node)) return `${jsxNameText(node.expression)}.${jsxNameText(node.name)}`
  return typeof node.getText === 'function' ? node.getText() : ''
}

function jsxTagName(node) {
  return jsxNameText(node.tagName)
}

function jsxAttrName(attr) {
  return jsxNameText(attr.name)
}

function walkJsxElement(node, env) {
  const tag = jsxTagName(node.openingElement)
  if (tag === 'style') {
    const css = node.children.map((child) => (ts.isJsxText(child) ? child.text : '')).join('')
    scanCss({ path: env.ctx.path, source: css, findings: env.ctx.findings, maps: env.ctx.maps })
  }
  walkJsx(node.openingElement, env)
  for (const child of node.children) walkTs(child, env)
}

function jsxAttrString(attr) {
  if (!attr.initializer) return ''
  if (ts.isStringLiteral(attr.initializer) || ts.isNoSubstitutionTemplateLiteral(attr.initializer)) return attr.initializer.text
  if (ts.isJsxExpression(attr.initializer) && attr.initializer.expression) return foldString(attr.initializer.expression, attr.getSourceFile()) ?? ''
  return ''
}

function walkJsx(node, env) {
  const attrs = []
  for (const attr of node.attributes.properties) {
    if (ts.isJsxAttribute(attr) && attr.name) attrs.push(attr)
  }
  const keys = []
  for (const attr of attrs) {
    const name = jsxAttrName(attr)
    if (name === 'data-status' || name === 'data-task-status' || name === 'status') {
      const canonical = canonicalStatus(jsxAttrString(attr))
      if (canonical) keys.push(canonical)
    }
    if (name === 'className' || name === 'class') keys.push(...statusKeysFromSelector(jsxAttrString(attr).split(/\s+/).map((part) => `.${part}`).join(' ')))
  }
  const statusKey = keys[0] ?? env.statusKey
  const next = { ...env, statusKey }
  for (const attr of attrs) {
    const name = jsxAttrName(attr)
    if (statusKey && (name === 'fill' || name === 'stroke' || name === 'color' || name === 'style' || name === 'className' || name === 'class') && attr.initializer) {
      const paintEnv = { ...next, inStatusMap: true }
      if (ts.isStringLiteral(attr.initializer) || ts.isNoSubstitutionTemplateLiteral(attr.initializer)) classifyValue(attr.initializer, statusKey, paintEnv)
      else if (ts.isJsxExpression(attr.initializer) && attr.initializer.expression) {
        classifyValue(attr.initializer.expression, statusKey, paintEnv)
        walkTs(attr.initializer.expression, paintEnv)
      }
    } else if (attr.initializer && ts.isJsxExpression(attr.initializer) && attr.initializer.expression) {
      walkTs(attr.initializer.expression, next)
    }
  }
  ts.forEachChild(node, (child) => {
    if (!ts.isJsxAttributes(child)) walkTs(child, next)
  })
}

function scanCss(ctx) {
  let root
  try {
    root = postcss.parse(ctx.source, { from: ctx.path })
  } catch (error) {
    const message = error.reason ?? error.message
    ctx.findings.push(makeFinding(ctx, RULE.parseError, 'unparseable-css', `Failed to parse CSS: ${message}`, error.line ?? 1, error.column ?? 1))
    return ctx.findings
  }
  root.walkDecls((decl) => {
    const rule = decl.parent && decl.parent.type === 'rule' ? decl.parent : null
    const selectorKeys = rule ? statusKeysFromSelector(rule.selector) : []
    const propKey = statusKeyFromCustomProp(decl.prop)
    const keys = propKey ? [propKey] : selectorKeys
    if (keys.length === 0) return
    if (!propKey && !isColorPropertyName(decl.prop) && !decl.prop.startsWith('--')) return
    const colors = extractColors(decl.value)
    if (colors.length === 0) {
      if (decl.value.trim() === '') {
        const syntax = `${canonicalize(rule?.selector ?? ':root').replace(/\s+/g, '')}{${canonicalize(`${decl.prop}:`).replace(/\s+/g, '')}}`
        pushFinding(ctx, RULE.unsupported, syntax, `Unsupported dynamic status palette coverage: empty colour declaration. Missing graph coverage is a finding, not success.`, decl.source?.start?.line, decl.source?.start?.column)
      }
      return
    }
    for (const statusKey of keys) {
      const outcome = classifyOutcome(statusKey, colors, ctx.maps)
      if (!outcome) continue
      const syntax = `${canonicalize(rule?.selector ?? ':root').replace(/\s+/g, '')}{${canonicalize(`${decl.prop}:${decl.value}`).replace(/\s+/g, '')}}`
      pushFinding(ctx, RULE[outcome], syntax, outcomeMessage(RULE[outcome], statusKey, decl.value), decl.source?.start?.line, decl.source?.start?.column)
    }
  })
  return ctx.findings
}

function scanSvg(ctx) {
  const styleBlocks = []
  const withoutStyleText = ctx.source.replace(/<style\b[^>]*>([\s\S]*?)<\/style>/gi, (_, css) => {
    styleBlocks.push(css)
    return '<style></style>'
  })
  for (const css of styleBlocks) scanCss({ ...ctx, source: css })
  const processed = withoutStyleText.replace(/<\?xml[^?]*\?>/g, '').replace(/(<[^>]*?)\sclass=/g, '$1 className=')
  return scanTypeScript({ ...ctx, source: `const __svg = (${processed})` }, ts.ScriptKind.TSX)
}
