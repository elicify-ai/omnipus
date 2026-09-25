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

// Status-contract governed-record capability (FIX-CONTRACT lane, 2026-09-20)
// — see statusContractResolvedColorOutcome's doc comment for the full
// design. `src/design-system/status.ts`'s own export/builder names, and the
// canonical generated-token module paths its `generatedColor()` accessor
// ultimately reads from — mirrored here exactly, never derived or guessed,
// so a differently-named look-alike can never satisfy this capability.
const STATUS_CONTRACT_MODULE_PATH = 'src/design-system/status.ts'
const STATUS_CONTRACT_EXPORT_NAME = 'statusContract'
const STATUS_CONTRACT_BUILDER_NAME = 'status'
const GENERATED_TOKEN_MODULE_PATHS = new Set([
  'src/styles/tokens.generated.css',
  'src/styles/tokens.theme.generated.css',
  'src/design-system/tokens.ts',
])
const CASE_OR_TRIM_METHODS = new Set(['toUpperCase', 'toLowerCase', 'trim', 'trimStart', 'trimEnd'])

// C1 Gap 2 (STATUS_BADGE.<status> read through cn(), TaskDetailPanel.tsx):
// this project's one real transparent class-list joiner, mirrored here
// exactly like STATUS_CONTRACT_MODULE_PATH/EXPORT_NAME above -- verified by
// IMPORT (module path + exported name), never trusted by the bare local
// identifier text alone, so a same-named but unrelated local `cn` cannot
// satisfy it. See resolvesToClassJoinerExport / classJoinerCallOutcome.
const CLASS_JOINER_MODULE_PATH = 'src/lib/utils.ts'
const CLASS_JOINER_EXPORT_NAMES = new Set(['cn'])

export function scan({ path = '', source = '', policy, modules } = {}) {
  const filePath = String(path).replaceAll('\\', '/')
  const text = typeof source === 'string' ? source : String(source ?? '')
  const ctx = { path: filePath, source: text, findings: [], maps: buildPolicyMaps(policy), modules, moduleCache: new Map(), governedPalettes: new Set() }
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

// ── Status-contract governed-record capability (FIX-CONTRACT lane, 2026-09-20) ──
//
// Closes the design-system/status-unsupported regression a first C1 repair
// script hit the moment it replaced hand-written status hexes with reads of
// the design system's own governed record: `import { statusContract } from
// '@/design-system/status'; TASK_CANCELLED_COLOR = statusContract.cancelled
// .resolvedColor`. `statusContract` (src/design-system/status.ts) is
// `Object.freeze({ inbox: status('inbox', ...), ... })`, where `status()`'s
// own `resolvedColor` field is bound to `generatedColor(...)`, which reads
// this project's own generated token output. Unlike this file's EXISTING
// governed-module-paint recognizer (the `ts.isIdentifier(core.expression)`
// branch a few lines below, and its own PropertyAccessExpression case),
// which resolves a SINGLE cross-module hop into a plain object literal, a
// `statusContract.<key>.resolvedColor` read is TWO hops deep, through an
// `Object.freeze(...)`-wrapped record whose values are themselves function
// CALLS — a shape neither that branch nor a plain `extractColors(printTs(...))`
// text scan (which only ever matches a literal-looking colour string) can
// see through. This capability recognizes exactly that two-hop shape and
// proves it governed structurally, rather than trusting the property name
// "resolvedColor" by itself:
//   1. The read must be a plain, non-computed `<base>.<statusKey>.<finalProp>`
//      PropertyAccessExpression chain — never `statusContract['inbox']`.
//   2. `<base>` must resolve to a NAMED import of `statusContract`
//      (checked by IMPORT NAME, not module path alone) from
//      STATUS_CONTRACT_MODULE_PATH — never a same-named LOCAL object
//      literal, which has no import statement to resolve at all.
//   3. `<statusKey>` (any of statusContract's own camelCase property names —
//      `inProgress`, not `in-progress`) must be an actual property of
//      statusContract's own object-literal initializer (after unwrapping
//      the ONE Object.freeze wrapper this file's `unwrap` already strips),
//      whose value is a call to the module-local `status(...)` builder.
//   4. `<finalProp>` must resolve, inside status()'s own single return
//      object literal (again exactly one Object.freeze unwrap), to a value
//      that is a bare identifier bound — by a local `const` in status()'s
//      OWN body, never a function PARAMETER like `label`/`nonColorCue` — to
//      a call whose callee is itself a LOCAL function (in the SAME module)
//      whose own returns resolve, through the same const/case-method
//      pattern, to a `<tokens>[id]` element access on an identifier that
//      traces — via import or `const`-alias, any number of hops — to
//      GENERATED_TOKEN_MODULE_PATHS. `resolvedColor` (→ `generatedColor(...)`
//      → `generatedValues[id]` → `resolvedTokens` imported from `./tokens`)
//      satisfies this; `tokens`/`contrast` (bound to `compositeOver(...)`
//      calls that do real colour MATH, not a governed accessor) do not.
// A canonical-status BINDING key (`statusKey`, e.g. the 'inbox' key of the
// STATUS_COLORS entry this read is the VALUE of) that disagrees with the
// resolved statusKey is a genuine MISMATCH (a status wired to the wrong
// colour), reported exactly like any other cross-wired status colour — this
// capability never silently drops that check. Any other failure at any step
// returns null (not recognized), falling through UNCHANGED to this file's
// pre-existing governed-module-paint / extractColors handling.
function statusContractResolvedColorOutcome(core, statusKey, env, record) {
  if (!ts.isPropertyAccessExpression(core)) return null
  const finalKey = core.name.text
  const mid = unwrap(core.expression)
  if (!mid || !ts.isPropertyAccessExpression(mid)) return null
  const midKeyRaw = mid.name.text
  const midKey = canonicalStatus(midKeyRaw)
  if (!midKey) return null
  const base = unwrap(mid.expression)
  if (!base || !ts.isIdentifier(base)) return null
  const sourceFile = record ? record.sf : env.sf
  if (!resolvesToStatusContractExport(base, sourceFile, env)) return null
  if (!statusContractMemberIsGovernedColor(midKeyRaw, finalKey, env)) return null
  if (statusKey && midKey !== statusKey) return ['mismatch']
  return [null]
}

// Structural (never scope-based) import resolution: walks `sf`'s own import
// declarations directly, exactly like this file's existing
// importedDeclaration, but additionally requires the EXPORTED name (not
// just the local alias) to be literally `statusContract` — importedDeclaration
// alone resolves by local alias only, which would wrongly accept a renamed
// import of some OTHER export from that same module.
function resolvesToStatusContractExport(identifier, sf, env) {
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const clause = statement.importClause
    const binding = clause?.namedBindings
    if (!binding || !ts.isNamedImports(binding)) continue
    for (const element of binding.elements) {
      if (element.name.text !== identifier.text) continue
      const exportedName = element.propertyName?.text ?? element.name.text
      if (exportedName !== STATUS_CONTRACT_EXPORT_NAME) return false
      return modulePath(env.ctx.path, statement.moduleSpecifier.text, env.ctx.modules) === STATUS_CONTRACT_MODULE_PATH
    }
  }
  return false
}

// Same identity-by-import shape as resolvesToStatusContractExport, for the
// one real class-list joiner (CLASS_JOINER_MODULE_PATH/EXPORT_NAMES) — never
// trusted by the bare name `cn` alone.
function resolvesToClassJoinerExport(identifier, sf, env) {
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const clause = statement.importClause
    const binding = clause?.namedBindings
    if (!binding || !ts.isNamedImports(binding)) continue
    for (const element of binding.elements) {
      if (element.name.text !== identifier.text) continue
      const exportedName = element.propertyName?.text ?? element.name.text
      if (!CLASS_JOINER_EXPORT_NAMES.has(exportedName)) return false
      return modulePath(env.ctx.path, statement.moduleSpecifier.text, env.ctx.modules) === CLASS_JOINER_MODULE_PATH
    }
  }
  return false
}

// C1 Gap 2: a transparent class-list joiner call (`cn(...)`, identity-
// verified above) is not itself a colour value — its own function body
// (twMerge(clsx(...))) is opaque to this scanner and was never provable, so
// the pre-existing generic CallExpression branch below (which tries to
// trace what a called function's body RETURNS) always failed it, regardless
// of what was actually passed in. The governed colour information lives in
// cn()'s own ARGUMENTS at the call site, not in cn's return value — this
// recurses paintOutcomes into each argument instead, which is what lets the
// existing PropertyAccessExpression branch above (already fully capable of
// resolving `STATUS_BADGE.blocked` as a governed imported record member —
// see its own comment) actually get reached for the first time. A plain
// string-literal argument with no colour-looking utility at all (ordinary
// structural classes like "h-8 rounded-md") contributes nothing — D4 status-
// colour governance only cares about the colour-bearing argument(s); an
// argument this scanner cannot structurally resolve at all (a spread, a
// ternary, an unresolved call) fails closed as 'unsupported', matching this
// file's "missing graph coverage is a finding, not success" posture
// everywhere else.
function classJoinerCallOutcome(call, statusKey, env, seen) {
  const outcomes = []
  for (const arg of call.arguments) {
    if (ts.isSpreadElement(arg)) {
      outcomes.push('unsupported')
      continue
    }
    const core = unwrap(arg)
    if (!core) {
      outcomes.push('unsupported')
      continue
    }
    if (ts.isStringLiteral(core) || ts.isNoSubstitutionTemplateLiteral(core)) {
      const colors = extractColors(core.text)
      if (!colors.length) continue
      outcomes.push(classifyOutcome(statusKey, colors, env.ctx.maps))
      continue
    }
    const nested = paintOutcomes(core, statusKey, env, null, seen)
    outcomes.push(...(nested ?? ['unsupported']))
  }
  return outcomes.length ? outcomes : null
}

// Purely structural cache of STATUS_CONTRACT_MODULE_PATH's OWN top-level
// declarations/imports (exported or not — this file's ordinary moduleRecord
// only tracks EXPORTED symbols, but `status()`/`generatedColor()` are
// module-private). Cached once per scan (env.ctx) — never touches
// env.ctx.moduleCache/exports, so this is a pure addition with no risk to
// the existing export-only cache's behaviour.
function statusContractModuleInfo(env) {
  if (env.ctx.__statusContractModuleInfo !== undefined) return env.ctx.__statusContractModuleInfo
  const source = env.ctx.modules?.[STATUS_CONTRACT_MODULE_PATH]
  if (typeof source !== 'string') {
    env.ctx.__statusContractModuleInfo = null
    return null
  }
  const sf = ts.createSourceFile(STATUS_CONTRACT_MODULE_PATH, source, ts.ScriptTarget.Latest, true, scriptKindFor(extensionOf(STATUS_CONTRACT_MODULE_PATH)))
  const declarations = new Map()
  const imports = new Map()
  for (const statement of sf.statements) {
    if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name)) declarations.set(declaration.name.text, declaration)
      }
    } else if (ts.isFunctionDeclaration(statement) && statement.name) {
      declarations.set(statement.name.text, statement)
    } else if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const bindings = statement.importClause?.namedBindings
      if (bindings && ts.isNamedImports(bindings)) {
        for (const element of bindings.elements) {
          imports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
        }
      }
    }
  }
  const info = { sf, declarations, imports }
  env.ctx.__statusContractModuleInfo = info
  return info
}

function propertyAssignmentValue(objectNode, key) {
  let result = null
  for (const prop of objectNode.properties) {
    if (ts.isShorthandPropertyAssignment(prop)) {
      if (prop.name.text === key) result = prop.name
      continue
    }
    if (!ts.isPropertyAssignment(prop)) continue
    if (resolveKey(prop.name, objectNode.getSourceFile()) === key) result = prop.initializer
  }
  return result
}

// A member is opaque (voids the whole-object completeness proof) exactly
// like this file's own hasColorishProp standard: a non-PropertyAssignment
// member other than a shorthand (spread, method, getter/setter) is always
// opaque; a computed key that cannot be pinned to a static literal name is
// opaque too. A shorthand property names itself unambiguously and is never
// opaque.
function isOpaqueStatusMember(member) {
  if (ts.isShorthandPropertyAssignment(member)) return false
  if (!ts.isPropertyAssignment(member)) return true
  return resolveKey(member.name, member.getSourceFile()) === null
}

// Finds the `const <name> = …` VariableDeclaration textually inside `body`
// (a function Block), never descending into a nested function's own scope.
function findLocalConst(body, name) {
  if (!body || !ts.isBlock(body)) return null
  let found = null
  const visit = (node) => {
    if (found || ts.isFunctionLike(node)) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name
      && node.parent && ts.isVariableDeclarationList(node.parent) && (node.parent.flags & ts.NodeFlags.Const)) {
      found = node
      return
    }
    ts.forEachChild(node, visit)
  }
  ts.forEachChild(body, visit)
  return found
}

// Resolves `ident` to a canonical generated-token-module path when it is
// either (a) a named import, or (b) a `const` alias — through an `as`/type-
// assertion cast or plain reassignment-free binding, any number of hops,
// each hop still required to be `const` — of one. Mirrors
// ts-colors.mjs::resolveGeneratedTokenModulePath's exact reasoning; this
// file cannot import that one directly (independent scanner, no shared
// runtime), so the same narrow proof is reproduced here instead of trusted
// by name alone.
function resolvesToGeneratedTokenModule(ident, info, env, seen = new Set()) {
  const marker = `${info.sf.fileName}#${ident.text}`
  if (seen.has(marker)) return false
  seen.add(marker)
  const imported = info.imports.get(ident.text)
  if (imported) {
    const path = modulePath(STATUS_CONTRACT_MODULE_PATH, imported.specifier, env.ctx.modules)
    return path !== null && GENERATED_TOKEN_MODULE_PATHS.has(path)
  }
  const declaration = info.declarations.get(ident.text)
  if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer
    && declaration.parent && ts.isVariableDeclarationList(declaration.parent) && (declaration.parent.flags & ts.NodeFlags.Const)) {
    const target = unwrap(declaration.initializer)
    if (ts.isIdentifier(target)) return resolvesToGeneratedTokenModule(target, info, env, seen)
  }
  return false
}

// A governed accessor return is either the bare identifier bound to a
// governed element-access read, or that same identifier wrapped in exactly
// one content-preserving case/trim String method call — mirrors
// ts-colors.mjs's generatedTokenAccessorReturnIsGoverned exactly (same
// independent-scanner reproduction rationale as resolvesToGeneratedTokenModule).
function accessorReturnIsGovernedColor(returnExpr, accessorDeclaration, info, env) {
  let target = unwrap(returnExpr)
  if (ts.isCallExpression(target) && target.arguments.length === 0) {
    const callee = unwrap(target.expression)
    if (callee && ts.isPropertyAccessExpression(callee) && CASE_OR_TRIM_METHODS.has(callee.name.text)) {
      target = unwrap(callee.expression)
    }
  }
  if (!ts.isIdentifier(target)) return false
  const local = findLocalConst(accessorDeclaration.body, target.text)
  if (!local || !local.initializer) return false
  const initializer = unwrap(local.initializer)
  if (!ts.isElementAccessExpression(initializer)) return false
  const base = unwrap(initializer.expression)
  if (!base || !ts.isIdentifier(base)) return false
  return resolvesToGeneratedTokenModule(base, info, env)
}

function collectReturnExpressions(fnDecl) {
  const returns = []
  const visit = (node) => {
    if (ts.isFunctionLike(node) && node !== fnDecl) return
    if (ts.isReturnStatement(node) && node.expression) returns.push(node.expression)
    else ts.forEachChild(node, visit)
  }
  if (fnDecl.body && ts.isBlock(fnDecl.body)) visit(fnDecl.body)
  else if (fnDecl.body) returns.push(fnDecl.body)
  return returns
}

// `value` is a member of status()'s own return object literal (e.g. the bare
// identifier `color` for `resolvedColor: color`). Governed only when it
// resolves — through a local `const` declared directly in status()'s OWN
// body (never a function PARAMETER like `label`/`nonColorCue`, which
// findLocalConst structurally cannot find) — to a call whose callee is a
// LOCAL function in the SAME module whose own returns are all governed.
function builderValueIsGovernedColor(value, builderDeclaration, info, env) {
  const target = unwrap(value)
  if (!ts.isIdentifier(target)) return false
  const local = findLocalConst(builderDeclaration.body, target.text)
  if (!local || !local.initializer) return false
  const initializer = unwrap(local.initializer)
  if (!ts.isCallExpression(initializer)) return false
  const callee = unwrap(initializer.expression)
  if (!callee || !ts.isIdentifier(callee)) return false
  const accessorDeclaration = info.declarations.get(callee.text)
  if (!accessorDeclaration || !ts.isFunctionDeclaration(accessorDeclaration) || !accessorDeclaration.body) return false
  const returns = collectReturnExpressions(accessorDeclaration)
  if (!returns.length) return false
  return returns.every((expr) => accessorReturnIsGovernedColor(expr, accessorDeclaration, info, env))
}

function statusBuilderReturnIsGovernedColor(returnExpr, finalKey, builderDeclaration, info, env) {
  const returnObject = unwrap(returnExpr)
  if (!returnObject || !ts.isObjectLiteralExpression(returnObject) || returnObject.properties.some(isOpaqueStatusMember)) return false
  const value = propertyAssignmentValue(returnObject, finalKey)
  if (!value) return false
  return builderValueIsGovernedColor(value, builderDeclaration, info, env)
}

// Bullets 3-4 of statusContractResolvedColorOutcome's doc comment: resolves
// STATUS_CONTRACT_MODULE_PATH's own `statusContract` and `status(...)`
// declarations directly from statusContractModuleInfo (never via a live
// scope stack — this file has none to begin with) and proves
// `<midKey>.<finalKey>` governed.
function statusContractMemberIsGovernedColor(midKeyRaw, finalKey, env) {
  const info = statusContractModuleInfo(env)
  if (!info) return false
  const contractDeclaration = info.declarations.get(STATUS_CONTRACT_EXPORT_NAME)
  if (!contractDeclaration || !ts.isVariableDeclaration(contractDeclaration) || !contractDeclaration.initializer) return false
  const contractObject = unwrap(contractDeclaration.initializer)
  if (!contractObject || !ts.isObjectLiteralExpression(contractObject) || contractObject.properties.some(isOpaqueStatusMember)) return false
  const entryValue = propertyAssignmentValue(contractObject, midKeyRaw)
  if (!entryValue) return false
  const call = unwrap(entryValue)
  if (!ts.isCallExpression(call)) return false
  const callee = unwrap(call.expression)
  if (!callee || !ts.isIdentifier(callee) || callee.text !== STATUS_CONTRACT_BUILDER_NAME) return false
  const builderDeclaration = info.declarations.get(STATUS_CONTRACT_BUILDER_NAME)
  if (!builderDeclaration || !ts.isFunctionDeclaration(builderDeclaration) || !builderDeclaration.body) return false
  const returns = collectReturnExpressions(builderDeclaration)
  if (!returns.length) return false
  return returns.every((expr) => statusBuilderReturnIsGovernedColor(expr, finalKey, builderDeclaration, info, env))
}

// Folds a paintOutcomes-style outcomes array to a single classification —
// shared by classifyGovernedModulePaint and the property-enumeration loop
// below (paintOutcomes' own PropertyAccessExpression/ElementAccessExpression
// branch), so a nested governed-record read (see
// statusContractResolvedColorOutcome) is classified by the SAME precedence
// rule as the top-level call site.
function foldOutcomes(outcomes) {
  if (outcomes.includes('mismatch')) return 'mismatch'
  if (outcomes.includes('literal')) return 'literal'
  if (outcomes.includes('unsupported')) return 'unsupported'
  return null
}

function paintOutcomes(node, statusKey, env, record = null, seen = new Set()) {
  const core = unwrap(node)
  if (!core) return null
  const contractOutcome = statusContractResolvedColorOutcome(core, statusKey, env, record)
  if (contractOutcome) return contractOutcome
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
      let outcome
      if (colors.length) {
        outcome = classifyOutcome(key, colors, env.ctx.maps)
      } else {
        // A record entry's value is not always a literal-looking colour
        // string extractColors can find textually — it may itself be a
        // governed cross-module read (statusContractResolvedColorOutcome),
        // e.g. `inbox: statusContract.inbox.resolvedColor`. Recurse through
        // the SAME paintOutcomes this branch is already part of before
        // giving up as unsupported — this is a pure ADDITION: an entry that
        // was already 'unsupported' before (a genuinely unprovable value)
        // still is, since paintOutcomes returns null for anything it does
        // not recognize and the fallback below is unchanged.
        const nested = paintOutcomes(prop.initializer, key, env, resolved.record, seen)
        outcome = nested ? foldOutcomes(nested) : 'unsupported'
      }
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
  if (ts.isCallExpression(core) && ts.isIdentifier(core.expression)
    && resolvesToClassJoinerExport(core.expression, record ? record.sf : env.sf, env)) {
    return classJoinerCallOutcome(core, statusKey, env, seen)
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
  const outcome = foldOutcomes(outcomes)
  reportOutcome(env, syntaxNode, statusKey, outcome, 'governed imported status paint')
  return true
}

// True only for an interpolation that cannot carry paint: a plain local identifier
// whose resolved initializer (if it has one) carries no colour. Anything else — an
// element access like palette['cancelled'], a property access, a call — is treated
// as possibly paint-bearing and is NOT exempted, so the unresolved-palette-read
// finding is preserved. Deliberately narrow: a false "prose" verdict would silence
// a real status-colour violation, so only the shape we can prove inert qualifies.
function isProseInterpolation(node, sf) {
  const core = unwrap(node)
  if (!core || !ts.isIdentifier(core)) return false
  if (isColorLikeNode(core)) return false
  const resolved = resolveConstInitializer(core, sf)
  if (resolved && resolved !== core && isColorLikeNode(resolved)) return false
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
  if (ts.isBinaryExpression(core) && core.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    reportOutcome(env, syntaxNode, statusKey, 'unsupported', 'value built by string concatenation cannot be statically proven')
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
    // A template with no colour anywhere in it is not a status-COLOUR concern, the
    // same posture the StringLiteral branch above takes (`if (colors.length === 0)
    // return`). Without this, prose returned under a case label that happens to
    // fold to a D4 status word failed closed as `unsupported` — e.g.
    // `case 'cancelled': return \`Stopped ${agent}\`` in
    // src/lib/delegationEventLine.ts, which carries no hex, token or class — while
    // the byte-identical value written as a string literal, or in a ternary
    // (walkConditional gates on isColorLikeNode), passed. This check is placed
    // AFTER the governed-paint check above so a paint-resolving template is still
    // accepted, and before reportOutcome so a template carrying a real colour
    // (or an unresolvable palette read) is still a finding.
    if (extractColors(core.getText()).length === 0 && core.templateSpans.every((span) => isProseInterpolation(span.expression, env.sf))) return
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
  const resolvedCanonicalKeys = new Set(d4Entries.map((entry) => canonicalStatus(entry.key)))
  // Shape-based detection (D4): a map whose resolvable keys are the full D4 status
  // set (or a superset of it) is a governed status palette regardless of binding
  // name. A second D4-shaped palette under an innocuous name is still blocking.
  const shapeIsD4Palette = Object.keys(D4_HEX).every((status) => resolvedCanonicalKeys.has(status))
  const nested = d4Entries.some((entry) => {
    const value = unwrap(entry.value)
    return value && ts.isObjectLiteralExpression(value) && hasColorishProp(value)
  })
  const nameSuggestsStatus = isStatusBindingName(env.bindingName) || isPaletteBindingName(env.bindingName)
  const isMap = env.inStatusMap || nested || shapeIsD4Palette || (nameSuggestsStatus && d4Entries.length >= 1)
  if (isMap && node.parent && ts.isVariableDeclaration(node.parent) && ts.isIdentifier(node.parent.name)) {
    // Remember this binding so a later plain assignment into it (a mutation) can
    // still be re-classified even though the mutation site itself carries no
    // status-shaped literal to look at.
    env.ctx.governedPalettes.add(node.parent.name.text)
  }
  if (isMap) {
    for (const entry of entries) {
      if (entry.kind === 'spread') reportUnsupported(env, entry.node, 'spread')
      else if (entry.key === null && ts.isComputedPropertyName(entry.nameNode)) reportUnsupported(env, entry.node, 'computed property')
    }
  } else if (nameSuggestsStatus) {
    // Not (yet) recognised as a governed map, but the binding name still claims a
    // status/paint role and at least one entry resolves to a colour-looking value
    // behind a key we cannot statically prove. Fail closed instead of silently
    // dropping it — this is exactly the "all computed keys" shape a name-only gate
    // is blind to. A non-colour value (e.g. an ordinary state label) is left alone.
    for (const entry of entries) {
      if (entry.kind === 'prop' && entry.key === null && ts.isComputedPropertyName(entry.nameNode) && isColorLikeNode(entry.value)) {
        reportUnsupported(env, entry.node, 'computed property on a status-shaped binding resolves to a colour-like value that cannot be classified')
      }
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
  if (operator === ts.SyntaxKind.EqualsToken) {
    walkAssignment(node, env)
    return
  }
  ts.forEachChild(node, (child) => walkTs(child, env))
}

// A plain assignment into a property of a governed status palette (by name or by
// prior shape registration) must be re-classified against the mutated value — the
// initial, correct definition must never stand in for what the binding holds after
// a later write. An unresolvable property key on a governed binding fails closed.
function mutationTargetKey(node, env) {
  const core = unwrap(node)
  if (!core) return null
  const computed = ts.isElementAccessExpression(core)
  if (!ts.isPropertyAccessExpression(core) && !computed) return null
  const objectExpr = core.expression
  const keyNode = computed ? core.argumentExpression : core.name
  if (!ts.isIdentifier(objectExpr)) return null
  const name = objectExpr.text
  // A bare status-shaped name (e.g. kickoffAttemptStatus) is not enough on its
  // own — that also matches ordinary non-colour state records (src/store/chat/
  // store.ts::resolveKickoffAttempt). Require either a name that is BOTH
  // status- and paint-shaped (statusPalette, chipStyleColors, ...) or prior
  // shape registration as an actual D4 map; a plain status-named record must
  // stay silent on mutation exactly as it does at definition time.
  const governed = env.ctx.governedPalettes.has(name) || isPaletteBindingName(name)
  if (!governed) return null
  const keyText = computed
    ? foldString(keyNode, env.sf)
    : (ts.isIdentifier(keyNode) || ts.isPrivateIdentifier(keyNode) ? keyNode.text : null)
  return { canonical: keyText ? canonicalStatus(keyText) : null, resolvable: keyText !== null }
}

function walkAssignment(node, env) {
  const mutation = mutationTargetKey(node.left, env)
  if (mutation) {
    if (mutation.canonical) classifyValue(node.right, mutation.canonical, { ...env, inStatusMap: true }, node)
    else if (!mutation.resolvable) reportUnsupported(env, node, 'status palette mutation with an unresolved property key')
  }
  walkTs(node.left, env)
  walkTs(node.right, env)
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

// A paint attribute that clearly names a status colour helper/binding (fill,
// stroke, color, style) but that this element carries no status/data-status/
// className channel to key against must fail closed rather than be skipped for
// lack of a literal status key — e.g. style={{ background: getStatusHex(status) }}
// on an element with no data-status/status/className attribute at all.
function isStatusRelatedExpr(node) {
  const core = unwrap(node)
  if (!core) return false
  if (ts.isCallExpression(core)) {
    const callee = core.expression
    const calleeName = ts.isIdentifier(callee) ? callee.text : ts.isPropertyAccessExpression(callee) ? callee.name.text : ''
    if (isPaletteBindingName(calleeName) || isStatusBindingName(calleeName)) return true
    return core.arguments.some((arg) => isStatusRelatedExpr(arg))
  }
  if (ts.isIdentifier(core)) return isStatusBindingName(core.text) || core.text === 'status'
  if (ts.isPropertyAccessExpression(core)) return isStatusBindingName(core.name.text) || isStatusRelatedExpr(core.expression)
  if (ts.isElementAccessExpression(core)) return isStatusRelatedExpr(core.expression)
  return false
}

function reportRuntimePaintIfStatusRelated(env, expr) {
  const core = unwrap(expr)
  if (!core) return
  const targets = ts.isObjectLiteralExpression(core)
    ? core.properties
      .filter((prop) => ts.isPropertyAssignment(prop) && isColorPropertyName(resolveKey(prop.name, env.sf) ?? ''))
      .map((prop) => prop.initializer)
    : [core]
  for (const target of targets) {
    const value = unwrap(target)
    if (value && (ts.isCallExpression(value) || ts.isIdentifier(value)) && isStatusRelatedExpr(value)) {
      reportUnsupported(env, target, 'runtime status paint binding has no literal status key on this element to classify against')
    }
  }
}

function walkJsx(node, env) {
  const attrs = []
  for (const attr of node.attributes.properties) {
    if (ts.isJsxAttribute(attr) && attr.name) attrs.push(attr)
  }
  const keys = []
  let hasStatusChannel = false
  for (const attr of attrs) {
    const name = jsxAttrName(attr)
    if (name === 'data-status' || name === 'data-task-status' || name === 'status') {
      hasStatusChannel = true
      const canonical = canonicalStatus(jsxAttrString(attr))
      if (canonical) keys.push(canonical)
    }
    if (name === 'className' || name === 'class') {
      hasStatusChannel = true
      keys.push(...statusKeysFromSelector(jsxAttrString(attr).split(/\s+/).map((part) => `.${part}`).join(' ')))
    }
  }
  const statusKey = keys[0] ?? env.statusKey
  const next = { ...env, statusKey }
  for (const attr of attrs) {
    const name = jsxAttrName(attr)
    const isPaintAttr = name === 'fill' || name === 'stroke' || name === 'color' || name === 'style' || name === 'className' || name === 'class'
    if (statusKey && isPaintAttr && attr.initializer) {
      const paintEnv = { ...next, inStatusMap: true }
      if (ts.isStringLiteral(attr.initializer) || ts.isNoSubstitutionTemplateLiteral(attr.initializer)) classifyValue(attr.initializer, statusKey, paintEnv)
      else if (ts.isJsxExpression(attr.initializer) && attr.initializer.expression) {
        classifyValue(attr.initializer.expression, statusKey, paintEnv)
        walkTs(attr.initializer.expression, paintEnv)
      }
    } else if (!statusKey && !hasStatusChannel && isPaintAttr && attr.initializer && ts.isJsxExpression(attr.initializer) && attr.initializer.expression) {
      reportRuntimePaintIfStatusRelated(next, attr.initializer.expression)
      walkTs(attr.initializer.expression, next)
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
