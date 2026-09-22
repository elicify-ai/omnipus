#!/usr/bin/env node
// spacing/value-rules.mjs
//
// CSS spacing-value math (calc()/var() evaluation, Tailwind
// spacing-utility decoding, the constitutional scale check) and the
// class/style analysis entry points that only need value math, not AST
// class-builder resolution. Depends on ast-utils.mjs only.

'use strict'

import ts from 'typescript'
import {
  RULE,
  formatPx,
  locFromTs,
  pushFinding,
} from './ast-utils.mjs'

// D10 closed spacing scale. Hairlines are 1px borders, not padding/gap/margin.
export const CONSTITUTIONAL_SCALE = [0, 4, 8, 16, 24, 32, 40, 48, 64]

export const SCALE_EPSILON = 1e-6

export const SCALE_TEXT = '0, 4, 8, 16, 24, 32, 40, 48, and 64px'

// D10's own words: "Hairlines and 1px borders stay 1px." The registered
// border-width token below is the one sanctioned 1px exception a spacing
// position may hold -- used verbatim (not through calc arithmetic, and not
// as a stand-in for any other border-width token) so a 1px gap/padding/
// margin/inline-style value can be expressed with a real token instead of
// a raw literal, without inventing a spacing-scale rung the scale
// deliberately does not have. Resolved defensively against the live token
// pixel value, not just its name, so the exception self-revokes if the
// token registry ever redefines it away from 1px.
export const HAIRLINE_SPACING_TOKEN = '--border-width-hairline'

export const HAIRLINE_SPACING_PX = 1

export const SPACING_PROPERTIES = new Set([
  'margin', 'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  'margin-block', 'margin-block-start', 'margin-block-end',
  'margin-inline', 'margin-inline-start', 'margin-inline-end',
  'padding', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
  'padding-block', 'padding-block-start', 'padding-block-end',
  'padding-inline', 'padding-inline-start', 'padding-inline-end',
  'gap', 'row-gap', 'column-gap', 'grid-gap', 'grid-row-gap', 'grid-column-gap',
  'scroll-margin', 'scroll-margin-top', 'scroll-margin-right', 'scroll-margin-bottom', 'scroll-margin-left',
  'scroll-margin-block', 'scroll-margin-block-start', 'scroll-margin-block-end',
  'scroll-margin-inline', 'scroll-margin-inline-start', 'scroll-margin-inline-end',
  'scroll-padding', 'scroll-padding-top', 'scroll-padding-right', 'scroll-padding-bottom', 'scroll-padding-left',
  'scroll-padding-block', 'scroll-padding-block-start', 'scroll-padding-block-end',
  'scroll-padding-inline', 'scroll-padding-inline-start', 'scroll-padding-inline-end',
  'border-spacing',
])

export const TW_PREFIXES = [
  'scroll-ms', 'scroll-me', 'scroll-mx', 'scroll-my', 'scroll-mt', 'scroll-mr', 'scroll-mb', 'scroll-ml',
  'scroll-ps', 'scroll-pe', 'scroll-px', 'scroll-py', 'scroll-pt', 'scroll-pr', 'scroll-pb', 'scroll-pl',
  'scroll-m', 'scroll-p',
  'space-x', 'space-y',
  'gap-x', 'gap-y',
  'ms', 'me', 'mx', 'my', 'mt', 'mr', 'mb', 'ml',
  'ps', 'pe', 'px', 'py', 'pt', 'pr', 'pb', 'pl',
  'gap', 'm', 'p',
]

export const IGNORE_KEYWORDS = new Set(['auto', 'normal', 'none'])

export const CSS_WIDE_KEYWORDS = new Set(['inherit', 'initial', 'unset', 'revert', 'revert-layer'])

export const SAFE_AREA_ENV_NAMES = new Set([
  'safe-area-inset-top',
  'safe-area-inset-right',
  'safe-area-inset-bottom',
  'safe-area-inset-left',
])

export const ABSOLUTE_UNIT_TO_PX = {
  px: 1,
  in: 96,
  cm: 96 / 2.54,
  mm: 96 / 25.4,
  pt: 96 / 72,
  pc: 16,
  q: 96 / 2.54 / 40,
}

export function analyzeStyleValue(propName, valueExpr, ctx, loc) {
  if (ts.isNumericLiteral(valueExpr)) {
    const syntax = `${propName}: ${valueExpr.text}px`
    analyzeSpacingValue(`${valueExpr.text}px`, ctx, syntax, loc, { unitlessIsPx: true })
    return
  }
  if (ts.isPrefixUnaryExpression(valueExpr) && valueExpr.operator === ts.SyntaxKind.MinusToken && ts.isNumericLiteral(valueExpr.operand)) {
    const syntax = `${propName}: -${valueExpr.operand.text}px`
    analyzeSpacingValue(`-${valueExpr.operand.text}px`, ctx, syntax, loc, { unitlessIsPx: true })
    return
  }
  if (ts.isStringLiteral(valueExpr) || ts.isNoSubstitutionTemplateLiteral(valueExpr)) {
    analyzeSpacingValue(valueExpr.text, ctx, canonicalDecl(propName, valueExpr.text), loc, { unitlessIsPx: false })
    return
  }
  if (ts.isTemplateExpression(valueExpr) && analyzeTemplateSpacingValue(propName, valueExpr, ctx, loc)) return
  pushFinding(ctx, RULE.unsupported, `${propName}: {expr}`, 'Unsupported spacing expression; dynamic spacing values cannot be verified against the D10 scale.', loc)
}

// FileTreeView.tsx-style shape: `` `calc(var(--space-2-5) * ${entry.indent})` ``
// -- a JS template literal, not a CSS var() reference, so the ordinary
// runtime-multiplier proof (evalNamedCalcFn/combineCalc, C1 Gap 1) never
// sees it; this rebuilds the SAME calc-string pipeline for it structurally.
// Every `${...}` substitution is replaced by a synthetic, guaranteed-
// unregistered `var(--__rt-multiplier-N__)` placeholder -- syntactically
// identical to an unregistered runtime depth var, so the existing
// evalNamedCalcFn/combineCalc multiplier proof classifies it exactly the
// same way, with NO duplicated arithmetic logic. The substitution is only
// trusted when every placeholder sits in a genuine value position (its
// immediate neighbours are not identifier/unit characters) -- a fused shape
// like `` `${n}px` `` (a raw interpolated dimension, not a calc() term) fails
// this check and falls through to the caller's existing generic "dynamic
// style value" unsupported finding. Returns true only when it produced a
// finding itself (any classification outcome), so the caller never
// double-reports.
export function analyzeTemplateSpacingValue(propName, expr, ctx, loc) {
  let text = expr.head.text
  for (const [index, span] of expr.templateSpans.entries()) {
    const boundaryBefore = text.length === 0 || !/[\w-]$/.test(text)
    const literal = span.literal.text
    const boundaryAfter = literal.length === 0 || !/^[\w-]/.test(literal)
    if (!boundaryBefore || !boundaryAfter) return false
    text += `var(--__rt-multiplier-${index}__)`
    text += literal
  }
  const before = ctx.findings.length
  analyzeSpacingValue(text, ctx, canonicalPropTemplateSyntax(propName, expr), loc, { unitlessIsPx: false })
  return ctx.findings.length > before
}

export function canonicalPropTemplateSyntax(propName, expr) {
  let text = expr.head.text
  for (const span of expr.templateSpans) text += `\${...}${span.literal.text}`
  return `${propName}: \`${text}\``
}

export function analyzeClassTemplate(expr, ctx, sourceFile, useLoc = null) {
  const loc = useLoc ?? locFromTs(expr, sourceFile)
  const syntax = canonicalTemplate(expr)
  analyzeClassList(completeClasses(expr.head.text, 'end'), ctx, loc)
  flagIncompleteSpacing(expr.head.text, 'end', ctx, loc, syntax)
  for (const [index, span] of expr.templateSpans.entries()) {
    const edge = index === expr.templateSpans.length - 1 ? 'start' : 'both'
    analyzeClassList(completeClasses(span.literal.text, edge === 'start' ? 'start' : 'both'), ctx, loc)
    flagIncompleteSpacing(span.literal.text, edge, ctx, loc, syntax)
  }
}

export function canonicalTemplate(expr) {
  let text = expr.head.text
  for (const span of expr.templateSpans) text += `\${...}${span.literal.text}`
  return `\`${text}\``
}

export function completeClasses(fragment, edge) {
  const tokens = splitClassList(fragment)
  if (tokens.length === 0) return ''
  let start = 0
  let end = tokens.length
  if ((edge === 'end' || edge === 'both') && !/\s$/.test(fragment)) end -= 1
  if ((edge === 'start' || edge === 'both') && !/^\s/.test(fragment) && start < end) start += 1
  return tokens.slice(start, end).join(' ')
}

export function flagIncompleteSpacing(fragment, edge, ctx, loc, syntax) {
  const tokens = splitClassList(fragment)
  if (tokens.length === 0) return
  if ((edge === 'end' || edge === 'both') && !/\s$/.test(fragment) && looksLikeSpacingFragment(tokens[tokens.length - 1])) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; interpolated Tailwind spacing cannot be verified against the D10 scale.', loc)
    return
  }
  if ((edge === 'start' || edge === 'both') && !/^\s/.test(fragment) && looksLikeSpacingFragment(tokens[0])) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; interpolated Tailwind spacing cannot be verified against the D10 scale.', loc)
  }
}

export function looksLikeSpacingFragment(token) {
  const stripped = token.replace(/^!/, '').replace(/!$/, '')
  const utility = splitVariants(stripped).utility
  const core = utility.startsWith('-') ? utility.slice(1) : utility
  if (!core) return false
  if (parseSpacingUtility(core)) return true
  return TW_PREFIXES.some((prefix) => prefix.startsWith(core) || core.startsWith(`${prefix}-`) || core === prefix)
}

export function analyzeClassList(value, ctx, loc) {
  for (const cls of splitClassList(value)) analyzeUtilityClass(cls, ctx, loc)
}

export function analyzeUtilityClass(cls, ctx, loc) {
  const { utility } = splitVariants(cls)
  const importantFree = utility.replace(/^!/, '').replace(/!$/, '')
  const negative = importantFree.startsWith('-')
  const core = negative ? importantFree.slice(1) : importantFree
  const parsed = parseSpacingUtility(core)
  if (!parsed) return
  const suffix = parsed.suffix
  if (!suffix || suffix === 'reverse') return
  if (suffix === 'auto') return
  if (suffix === 'full' || suffix.includes('/')) {
    pushFinding(ctx, RULE.unsupported, cls, `Unsupported spacing expression; ${cls} is percentage spacing, not a closed D10 pixel step.`, loc)
    return
  }
  if (suffix.startsWith('[') && suffix.endsWith(']')) {
    const inner = decodeArbitrary(suffix.slice(1, -1))
    analyzeSpacingValue(inner, ctx, cls, loc, { unitlessIsPx: false })
    return
  }
  if (suffix.startsWith('(') && suffix.endsWith(')')) {
    analyzeSpacingValue(`var(${suffix.slice(1, -1).trim()})`, ctx, cls, loc, { unitlessIsPx: false })
    return
  }
  if (suffix === 'px') {
    reportOffScale(ctx, cls, 1, loc, '1px')
    return
  }
  if (suffix === '0') return
  if (/^\d+(?:\.\d+)?$/.test(suffix)) {
    if (Number(suffix) === 0) return
    pushFinding(ctx, RULE.rootDependent, cls, `Root-dependent spacing; Tailwind rem utility ${cls} changes with the user root (D1/D10 require pixel-stable spacing).`, loc)
    return
  }
  pushFinding(ctx, RULE.unsupported, cls, `Unsupported spacing expression; named utility ${cls} is not a closed-scale token or static length.`, loc)
}

export function decodeArbitrary(inner) {
  const decoded = inner.replaceAll('_', ' ')
  return decoded.replace(/^(?:length|spacing):/, '')
}

export function parseSpacingUtility(core) {
  for (const prefix of TW_PREFIXES) {
    if (core.startsWith(`${prefix}-`)) return { prefix, suffix: core.slice(prefix.length + 1) }
  }
  return null
}

export function splitClassList(value) {
  const out = []
  let current = ''
  let square = 0
  let paren = 0
  for (const char of String(value)) {
    if (char === '[') square += 1
    else if (char === ']' && square > 0) square -= 1
    else if (char === '(') paren += 1
    else if (char === ')' && paren > 0) paren -= 1
    if (/\s/.test(char) && square === 0 && paren === 0) {
      if (current) out.push(current)
      current = ''
    } else {
      current += char
    }
  }
  if (current) out.push(current)
  return out
}

export function splitVariants(cls) {
  const parts = []
  let current = ''
  let square = 0
  let paren = 0
  for (const char of cls) {
    if (char === '[') square += 1
    else if (char === ']' && square > 0) square -= 1
    else if (char === '(') paren += 1
    else if (char === ')' && paren > 0) paren -= 1
    if (char === ':' && square === 0 && paren === 0) {
      parts.push(current)
      current = ''
    } else {
      current += char
    }
  }
  if (current) parts.push(current)
  const utility = parts.pop() || ''
  return { variants: parts, utility }
}

export function analyzeSpacingValue(rawValue, ctx, syntax, loc, options) {
  const value = String(rawValue).trim()
  if (!value) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; empty spacing values cannot be verified against the D10 scale.', loc)
    return
  }
  for (const part of splitSpaceSeparated(value)) {
    analyzeSpacingTerm(part, ctx, syntax, loc, options)
  }
}

export function analyzeSpacingTerm(term, ctx, syntax, loc, options) {
  const trimmed = term.trim()
  if (!trimmed) return
  const lower = trimmed.toLowerCase()
  if (IGNORE_KEYWORDS.has(lower)) return
  if (CSS_WIDE_KEYWORDS.has(lower)) {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; keyword ${trimmed} cannot be verified against the D10 scale.`, loc)
    return
  }
  if (options.allowUnitlessNumber && isUnitlessNumber(trimmed)) return
  const fn = parseFunctionCall(trimmed)
  if (fn) {
    classifyFunction(fn, ctx, syntax, loc, options)
    return
  }
  const dim = parseDimension(trimmed, options)
  if (dim) {
    classifyDimension(dim, ctx, syntax, loc)
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${trimmed} cannot be verified against the D10 scale.`, loc)
}

export function classifyDimension(dim, ctx, syntax, loc) {
  if (dim.unit === '%' ) {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; percentage ${dim.raw} is not a closed D10 pixel step.`, loc)
    return
  }
  if (dim.unit === 'fr') {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${dim.raw} is not closed-scale spacing (fr is grid-track geometry).`, loc)
    return
  }
  if (dim.unit === 'rem') {
    if (dim.number === 0) return
    pushFinding(ctx, RULE.rootDependent, syntax, `Root-dependent spacing; rem spacing ${dim.raw} changes with the user root (D1/D10 require pixel-stable spacing).`, loc)
    return
  }
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) {
    const px = dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit]
    if (isOnScale(Math.abs(px), ctx.scale)) return
    reportOffScale(ctx, syntax, px, loc, dim.raw)
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; unit ${dim.unit} cannot be normalized to the D10 pixel scale.`, loc)
}

export function classifyFunction(fn, ctx, syntax, loc, options) {
  if (fn.name === 'var') {
    analyzeVar(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'calc') {
    analyzeCalc(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'env') {
    analyzeEnvironmentSpacing(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'min' || fn.name === 'max' || fn.name === 'clamp') {
    for (const arg of splitCommaSeparated(fn.args)) {
      analyzeSpacingTerm(arg, ctx, syntax, loc, { ...options, allowUnitlessNumber: false })
    }
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${fn.name}() cannot be verified against the D10 scale.`, loc)
}

export function analyzeEnvironmentSpacing(args, ctx, syntax, loc) {
  const parts = splitCommaSeparated(args)
  const name = parts[0]?.trim().toLowerCase()
  if (SAFE_AREA_ENV_NAMES.has(name) && parts.length === 1) {
    pushFinding(ctx, RULE.missingSafeAreaFallback, syntax, `Safe-area spacing ${name} is missing the required explicit closed-scale fallback.`, loc)
    return
  }
  if (!SAFE_AREA_ENV_NAMES.has(name) || parts.length !== 2) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; safe-area env() spacing requires one explicit closed-scale fallback.', loc)
    return
  }
  const before = ctx.findings.length
  analyzeSpacingTerm(parts[1], ctx, syntax, loc, { unitlessIsPx: false })
  if (ctx.findings.length === before) {
    pushFinding(ctx, RULE.extensionBoundary, syntax, `Spacing extension boundary; runtime ${name} uses a validated closed-scale fallback and requires exact central review.`, loc)
  }
}

export function analyzeVar(args, ctx, syntax, loc) {
  const parts = splitCommaSeparated(args)
  const name = parts[0] ? parts[0].trim() : ''
  if (!name.startsWith('--')) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; var() is missing a custom property name.', loc)
    return
  }
  if (!ctx.tokenCssNames.has(name)) {
    pushFinding(ctx, RULE.invalidVar, syntax, `Unknown spacing custom property ${name}; it is not a registered token.`, loc)
    return
  }
  if (!isSpacingTokenName(name) && !isHairlineSpacingException(name, ctx)) {
    pushFinding(ctx, RULE.unsupported, syntax, `Custom property ${name} is registered but is not a D10 spacing token.`, loc)
  }
  if (parts[1]) analyzeSpacingTerm(parts[1], ctx, syntax, loc, { unitlessIsPx: false })
}

// C1 Gap 1 (runtime tree-indent depth multiplied by registered spacing
// tokens, e.g. Sidebar.tsx/SearchModal.tsx/KnowledgeOutline.tsx's
// `calc(var(--space-2-5) + var(--sidebar-indent-depth) * var(--space-3))`):
// a calc() whose only unregistered var() leaf is used SOLELY as a `*`
// multiplier directly against a registered spacing token, and whose every
// other term is itself a registered token, is a legitimate runtime scaling
// of the D10 scale by an integer row-depth count -- registrable and
// centrally reviewed, not an unresolvable parser failure. See
// evalNamedCalcFn/combineCalc for the 'runtime-multiplier' kind that proves
// this narrow shape structurally (isTrustedTokenPx gates every combination).
//
// Decision on the `+ 18px` alignment offset present at two Sidebar.tsx call
// sites: a raw pixel literal is NEVER a registered token, so it can never be
// isTrustedTokenPx, so combining it (by `+`) with the runtime-multiplier
// chain falls through to 'unsupported' in combineCalc, same as before this
// fix. This is deliberate, not a gap: D10 requires every dimensional term to
// be a registered, centrally-reviewed token; an un-reviewed magic-number
// pixel offset riding alongside the multiplier would be exactly the kind of
// debt this lock exists to catch, and widening the carve-out to swallow it
// would violate the "never quietly widen a rule" instruction. Those two
// call sites stay `spacing/unsupported` findings after this fix.
export function analyzeCalc(args, ctx, syntax, loc) {
  const result = evaluateCalcExpr(args, ctx)
  if (result.kind === 'invalid-var') {
    pushFinding(ctx, RULE.invalidVar, syntax, `Unknown spacing custom property ${result.name}; it is not a registered token.`, loc)
    return
  }
  if (result.kind === 'px') {
    if (isOnScale(Math.abs(result.value), ctx.scale)) return
    reportOffScale(ctx, syntax, result.value, loc, formatPx(result.value))
    return
  }
  if (result.kind === 'number' && result.value === 0) return
  if (result.kind === 'runtime-multiplier') {
    pushFinding(ctx, RULE.extensionBoundary, syntax, `Spacing extension boundary; runtime depth multiplier ${result.name} is scaled against a validated closed-scale token and requires exact central review.`, loc)
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; calc() result is not a closed D10 pixel length.', loc)
}

export function evaluateCalcExpr(input, ctx) {
  const parser = { s: String(input), i: 0, ctx }
  const result = parseCalcAdd(parser)
  skipCalcWs(parser)
  if (result.kind === 'invalid-var' || result.kind === 'unsupported') return result
  if (parser.i !== parser.s.length) return { kind: 'unsupported' }
  return result
}

export function skipCalcWs(parser) {
  while (parser.i < parser.s.length && /\s/.test(parser.s[parser.i])) parser.i += 1
}

export function parseCalcAdd(parser) {
  let left = parseCalcMul(parser)
  // Only a hard parse failure ('unsupported') short-circuits here without
  // even checking for an operator. An 'invalid-var' leaf flows into
  // combineCalc below instead, which recognizes the one narrow
  // "registered-token +/- (runtime-var * registered-token)" shape as
  // 'runtime-multiplier' and otherwise reproduces the same invalid-var
  // short-circuit (see combineCalc's own
  // `if (left.kind === 'invalid-var') return left` / right-hand mirror), so
  // every non-multiplier invalid-var shape classifies the same either way.
  if (left.kind === 'unsupported') return left
  for (;;) {
    skipCalcWs(parser)
    const op = parser.s[parser.i]
    if (op !== '+' && op !== '-') break
    parser.i += 1
    const right = parseCalcMul(parser)
    if (right.kind === 'unsupported') return right
    left = combineCalc(left, right, op)
    if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  }
  return left
}

export function parseCalcMul(parser) {
  let left = parseCalcUnary(parser)
  // See parseCalcAdd's comment: only 'unsupported' bails before the operator
  // check; 'invalid-var' continues so a directly-following `*`/`/` gets the
  // chance to combine it into a runtime-multiplier via combineCalc.
  if (left.kind === 'unsupported') return left
  for (;;) {
    skipCalcWs(parser)
    const op = parser.s[parser.i]
    if (op !== '*' && op !== '/') break
    parser.i += 1
    const right = parseCalcUnary(parser)
    if (right.kind === 'unsupported') return right
    left = combineCalc(left, right, op)
    if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  }
  return left
}

export function parseCalcUnary(parser) {
  skipCalcWs(parser)
  if (parser.s[parser.i] === '+' || parser.s[parser.i] === '-') {
    const op = parser.s[parser.i]
    parser.i += 1
    const inner = parseCalcUnary(parser)
    if (op === '-') return negateCalc(inner)
    return inner
  }
  return parseCalcPrimary(parser)
}

export function parseCalcPrimary(parser) {
  skipCalcWs(parser)
  if (parser.s[parser.i] === '(') {
    parser.i += 1
    const inner = parseCalcAdd(parser)
    skipCalcWs(parser)
    if (parser.s[parser.i] !== ')') return { kind: 'unsupported' }
    parser.i += 1
    return inner
  }
  if (parser.i < parser.s.length && /[A-Za-z_]/.test(parser.s[parser.i])) {
    const nameStart = parser.i
    while (parser.i < parser.s.length && /[\w-]/.test(parser.s[parser.i])) parser.i += 1
    const name = parser.s.slice(nameStart, parser.i)
    skipCalcWs(parser)
    const args = readBalancedArgs(parser)
    if (args === null) return { kind: 'unsupported' }
    return evalNamedCalcFn(name.toLowerCase(), args, parser.ctx)
  }
  const dim = readCalcDimension(parser)
  if (!dim) return { kind: 'unsupported' }
  if (dim.number === 0) return { kind: 'px', value: 0 }
  if (!dim.unit) return { kind: 'number', value: dim.number }
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) {
    // tokenPx is deliberately absent (untrusted): a literal dimension typed
    // directly in source, never a registered token, so it must never combine
    // into a runtime-multiplier chain (see combineCalc's isTrustedTokenPx
    // gate) -- a raw `+ 18px` alongside a depth multiplier stays a finding,
    // not a silent extension-boundary pass (C1 Gap 1 decision, see
    // analyzeCalc's doc comment above for the full reasoning).
    return { kind: 'px', value: dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit] }
  }
  return { kind: 'unsupported' }
}

export function readBalancedArgs(parser) {
  if (parser.s[parser.i] !== '(') return null
  let depth = 0
  const start = parser.i
  for (; parser.i < parser.s.length; parser.i += 1) {
    const char = parser.s[parser.i]
    if (char === '(') depth += 1
    else if (char === ')') {
      depth -= 1
      if (depth === 0) {
        const inner = parser.s.slice(start + 1, parser.i)
        parser.i += 1
        return inner
      }
    }
  }
  return null
}

export function readCalcDimension(parser) {
  skipCalcWs(parser)
  const start = parser.i
  if (parser.i >= parser.s.length || !/[\d.]/.test(parser.s[parser.i])) return null
  while (parser.i < parser.s.length && /[\d.]/.test(parser.s[parser.i])) parser.i += 1
  const number = Number(parser.s.slice(start, parser.i))
  if (!Number.isFinite(number)) {
    parser.i = start
    return null
  }
  const unitStart = parser.i
  while (parser.i < parser.s.length && /[A-Za-z%]/.test(parser.s[parser.i])) parser.i += 1
  return { number, unit: parser.s.slice(unitStart, parser.i).toLowerCase() }
}

export function evalNamedCalcFn(name, args, ctx) {
  if (name === 'calc') return evaluateCalcExpr(args, ctx)
  if (name !== 'var') return { kind: 'unsupported' }
  const parts = splitCommaSeparated(args)
  const token = parts[0] ? parts[0].trim() : ''
  if (!token.startsWith('--')) return { kind: 'unsupported' }
  if (!ctx.tokenCssNames.has(token)) return { kind: 'invalid-var', name: token }
  if (!isSpacingTokenName(token)) return { kind: 'unsupported' }
  if (!ctx.tokenPx.has(token)) return { kind: 'unsupported' }
  // tokenPx: true marks this px value as TRUSTED -- traced directly to a
  // registered D10 token, never a raw literal or an unvalidated arithmetic
  // combination -- the exact provenance combineCalc's isTrustedTokenPx gate
  // requires before it will treat an adjacent unregistered var() as a
  // legitimate runtime multiplier rather than ordinary invalid-var debt.
  return { kind: 'px', value: ctx.tokenPx.get(token), tokenPx: true }
}

export function isTrustedTokenPx(value) {
  return value.kind === 'px' && value.tokenPx === true
}

export function isRuntimeMultiplier(value) {
  return value.kind === 'runtime-multiplier'
}

export function combineCalc(left, right, op) {
  // Runtime-multiplier detection is narrow by construction -- `*` only
  // turns an unregistered var() into 'runtime-multiplier' when multiplied
  // DIRECTLY against a trusted (registered-token) px value, and `+`/`-`
  // only ever propagate an ALREADY-recognized 'runtime-multiplier' when
  // combined with another trusted token px. Any other shape touching
  // either kind (double multiply, division, subtracting a multiplier,
  // combining with a raw literal or a second unregistered var) falls
  // through to the invalid-var/unsupported handling below.
  if (op === '*') {
    if (left.kind === 'invalid-var' && isTrustedTokenPx(right)) return { kind: 'runtime-multiplier', name: left.name }
    if (right.kind === 'invalid-var' && isTrustedTokenPx(left)) return { kind: 'runtime-multiplier', name: right.name }
  } else if (op === '+') {
    if (isRuntimeMultiplier(left) && isTrustedTokenPx(right)) return { kind: 'runtime-multiplier', name: left.name }
    if (isTrustedTokenPx(left) && isRuntimeMultiplier(right)) return { kind: 'runtime-multiplier', name: right.name }
  }
  // Pre-existing invalid-var short-circuit, reproduced here (moved out of
  // parseCalcAdd/parseCalcMul's pre-operator checks so the multiplier
  // detection above gets first refusal) -- an unregistered var() used in any
  // shape OTHER than the narrow multiplier case still propagates exactly as
  // it always did.
  if (left.kind === 'invalid-var') return left
  if (right.kind === 'invalid-var') return right
  if (left.kind === 'unsupported' || right.kind === 'unsupported') return { kind: 'unsupported' }
  if (op === '+' || op === '-') {
    const sign = op === '-' ? -1 : 1
    if (left.kind === 'px' && right.kind === 'px') return { kind: 'px', value: left.value + sign * right.value, tokenPx: Boolean(left.tokenPx && right.tokenPx) }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value + sign * right.value }
    return { kind: 'unsupported' }
  }
  if (op === '*') {
    if (left.kind === 'px' && right.kind === 'number') return { kind: 'px', value: left.value * right.value, tokenPx: left.tokenPx }
    if (left.kind === 'number' && right.kind === 'px') return { kind: 'px', value: left.value * right.value, tokenPx: right.tokenPx }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value * right.value }
    return { kind: 'unsupported' }
  }
  if (op === '/') {
    if (!right.value) return { kind: 'unsupported' }
    if (left.kind === 'px' && right.kind === 'number') return { kind: 'px', value: left.value / right.value, tokenPx: left.tokenPx }
    if (left.kind === 'px' && right.kind === 'px') return { kind: 'number', value: left.value / right.value }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value / right.value }
    return { kind: 'unsupported' }
  }
  return { kind: 'unsupported' }
}

export function negateCalc(value) {
  if (value.kind === 'px' || value.kind === 'number') return { kind: value.kind, value: -value.value }
  return value
}

export function reportOffScale(ctx, syntax, px, loc) {
  const abs = Math.abs(px)
  pushFinding(
    ctx,
    RULE.offScale,
    syntax,
    `Off-scale spacing ${formatPx(abs)}; D10 allows ${SCALE_TEXT} (hairlines are 1px borders only).`,
    loc,
  )
}

export function parseFunctionCall(term) {
  const trimmed = term.trim()
  const open = trimmed.indexOf('(')
  if (open <= 0 || !trimmed.endsWith(')')) return null
  const name = trimmed.slice(0, open).trim()
  if (!/^[A-Za-z_][\w-]*$/.test(name)) return null
  const inner = trimmed.slice(open + 1, -1)
  if (!isBalanced(trimmed.slice(open))) return null
  return { name: name.toLowerCase(), args: inner }
}

export function parseDimension(term, options) {
  const trimmed = term.trim()
  if (trimmed === '0') return { number: 0, unit: 'px', raw: '0' }
  const match = /^([+-]?(?:\d*\.\d+|\d+))([A-Za-z%]+)?$/.exec(trimmed)
  if (!match) return null
  const number = Number(match[1])
  if (!Number.isFinite(number)) return null
  let unit = (match[2] || '').toLowerCase()
  if (!unit) {
    if (number === 0) unit = 'px'
    else if (options.unitlessIsPx) unit = 'px'
    else return null
  }
  return { number, unit, raw: trimmed }
}

export function isOnScale(px, scale) {
  for (const step of scale) {
    if (Math.abs(px - step) < SCALE_EPSILON) return true
  }
  return false
}

export function isUnitlessNumber(term) {
  return /^[+-]?(?:\d*\.\d+|\d+)$/.test(term.trim())
}

export function isSpacingTokenName(name) {
  return name.startsWith('--space-') || (name.startsWith('--density-') && name.includes('gap'))
}

// The D10 hairline exception: only the exact registered token, only when
// its resolved value is genuinely 1px. Does not cover the token appearing
// inside calc() -- evalNamedCalcFn intentionally has no equivalent check,
// so a hairline combined arithmetically with anything else stays
// unsupported, same as before this exception existed.
export function isHairlineSpacingException(name, ctx) {
  if (name !== HAIRLINE_SPACING_TOKEN) return false
  return ctx.tokenPx.get(name) === HAIRLINE_SPACING_PX
}

export function isBalanced(text) {
  let depth = 0
  for (const char of text) {
    if (char === '(') depth += 1
    if (char === ')') {
      depth -= 1
      if (depth < 0) return false
    }
  }
  return depth === 0
}

export function splitSpaceSeparated(value) {
  return splitTopLevel(value, ' ').filter(Boolean)
}

export function splitCommaSeparated(value) {
  return splitTopLevel(value, ',').filter(Boolean)
}

export function splitTopLevel(value, separator) {
  const out = []
  let current = ''
  let depth = 0
  for (const char of String(value)) {
    if (char === '(') depth += 1
    else if (char === ')') depth = Math.max(0, depth - 1)
    if (char === separator && depth === 0) {
      const piece = current.trim()
      if (piece) out.push(piece)
      current = ''
    } else {
      current += char
    }
  }
  const piece = current.trim()
  if (piece) out.push(piece)
  return out
}

export function canonicalDecl(prop, value) {
  return `${prop}: ${normalizeCssValue(value)}`
}

export function normalizeCssValue(value) {
  return String(value)
    .trim()
    .replace(/\s+/g, ' ')
    .replace(/\s*!\s*important$/i, '')
    .replace(/\(\s+/g, '(')
    .replace(/\s+\)/g, ')')
    .replace(/\s+,/g, ',')
    .replace(/,\s+/g, ', ')
    .trim()
}

export function kebab(name) {
  return name.replace(/^[A-Z]/, (char) => char.toLowerCase()).replace(/[A-Z]/g, (char) => `-${char.toLowerCase()}`)
}

export function scanInlineStyle(text, ctx, loc) {
  for (const chunk of splitTopLevel(text, ';')) {
    if (!chunk) continue
    const colon = chunk.indexOf(':')
    if (colon === -1) continue
    const prop = kebab(chunk.slice(0, colon).trim())
    if (!SPACING_PROPERTIES.has(prop)) continue
    const value = chunk.slice(colon + 1)
    analyzeSpacingValue(value, ctx, canonicalDecl(prop, value), loc, { unitlessIsPx: false })
  }
}
