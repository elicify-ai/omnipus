#!/usr/bin/env node

// ---------------------------------------------------------------------------
// Stage B typography lock — D1/D2/D9 of docs/internal/design/design-system-definition.md.
//
// Contract: design-system/enforcement/contract.json — scan({ path, source, policy })
// returns Finding[] synchronously; `syntax` is the canonical offending syntax
// without position/trivia; scanners report raw findings and never apply
// policy/exceptions (the orchestrator owns that); parser failures and
// unsupported governed syntax become explicit findings, never empty success.
//
// Spec anchors (every constant below traces to one of these):
// - §D2  hard 12px floor for every UI text → FLOOR_PX = 12.
// - §D1  root is clamp(12px, var(--user-font-size, 14px), 20px) → rem sizes
//   are evaluated across ROOT_MIN_PX..ROOT_MAX_PX; foundations.json agrees
//   (font.root.minimum/default/maximum = 12/14/20px).
// - §D10 320px reflow floor → 1vw >= 3.2px, the only static bound for vw.
// - §E1  "computed UI text below 12px, or an arbitrary text-size utility" fail.
// - §D9  families are Outfit/Inter/JetBrains Mono through tokens; arbitrary
//   family values are rejected outside the exception registry.
// - Tailwind 4.3.3 defaults (node_modules/tailwindcss/theme.css): text-xs =
//   0.75rem, text-sm = 0.875rem, text-base = 1rem, larger steps >= 1.125rem.
//
// Composition with the browser harness: this lock proves nothing about the
// rendered DOM. From source it proves that a size expression cannot compute
// below 12px under the constitutional root/viewport bounds, or that the value
// is a registered token. The Stage A browser harness (computed-style floor
// tests) is the independent runtime proof; the two compose as static gate +
// runtime gate — no arithmetic here is claimed as computed-DOM evidence.
//
// Known limitations (mirrored in the lane report):
// - Bare unknown `text-<word>` utilities are skipped: a per-file scanner
//   cannot see theme-registered custom utilities. Needs a central
//   theme-utility registry in policy (requested from the lead).
// - Whitespace-separated standalone interpolations (`${cond}` between whole
//   class names) are skipped; only boundary-gluing dynamics are flagged.
// - Condition-position identifiers in cn()/clsx() (`cond && 'class'`) are
//   treated as booleans, not class sources.
// - Standalone SVG markup is parsed at the attribute boundary and embedded
//   CSS is delegated to PostCSS; malformed governed attributes fail closed.
// ---------------------------------------------------------------------------

import postcss from 'postcss'
import path from 'node:path'
import ts from 'typescript'

export const extensions = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.css', '.svg']

const TS_EXTENSIONS = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs']

const FLOOR_PX = 12
const ROOT_MIN_PX = 12
const ROOT_MAX_PX = 20
const REFLOW_MIN_VIEWPORT_PX = 320

// Tailwind 4.3.3 default text scale in rem, transcribed from
// node_modules/tailwindcss/theme.css. Only xs/sm sit below 1rem and can fall
// under the 12px floor at the 12px minimum root.
const TEXT_SCALE_REM = {
  xs: 0.75,
  sm: 0.875,
  base: 1,
  lg: 1.125,
  xl: 1.25,
  '2xl': 1.5,
  '3xl': 1.875,
  '4xl': 2.25,
  '5xl': 3,
  '6xl': 3.75,
  '7xl': 4.5,
  '8xl': 6,
  '9xl': 8,
}

// text-<name> utilities that are alignment/decoration/case, never sizes —
// this lane must not confuse them with sizes, and vice versa.
const NON_SIZE_TEXT_UTILITIES = new Set([
  'center', 'left', 'right', 'justify', 'start', 'end',
  'wrap', 'nowrap', 'balance', 'pretty', 'clip', 'ellipsis', 'truncate',
  'underline', 'overline', 'line-through', 'no-underline',
  'uppercase', 'lowercase', 'capitalize', 'normal-case',
  'opaque', 'inherit', 'current', 'transparent',
])

// font-weight utilities (D9 governs weights too, but that is outside this
// lane's mandate — reported to the lead, not silently dropped).
const FONT_WEIGHT_UTILITIES = new Set([
  'thin', 'extralight', 'light', 'normal', 'medium', 'semibold',
  'bold', 'extrabold', 'black',
])

// font-sans / font-serif resolve to the Tailwind default stacks in BOTH live
// builds of this repo (src/styles/globals.css and src/styles/library.css map
// only --font-headline/body/mono), so they are off-token families per §D9.
// font-mono / font-headline / font-body are remapped onto tokens in both
// builds and are not flagged per-usage; the theme-level literals in
// globals.css are theme hygiene for the token lane, not usage violations.
const OFF_TOKEN_FAMILY_UTILITIES = new Set(['sans', 'serif'])
const REMAPPED_FAMILY_UTILITIES = new Set(['mono', 'headline', 'body'])

const CLASS_BUILDERS = new Set(['cn', 'clsx', 'cva', 'twMerge', 'classnames', 'classNames'])

const STYLE_SIZE_PROPERTY_NAMES = new Set(['fontSize', 'font-size'])
const STYLE_FAMILY_PROPERTY_NAMES = new Set(['fontFamily', 'font-family'])
const STYLE_SHORTHAND_PROPERTY_NAMES = new Set(['font'])
const STYLE_ROLE_PROPERTIES = new Map([
  ['fontWeight', { css: 'font-weight', ruleId: 'typography/font-weight-arbitrary' }],
  ['font-weight', { css: 'font-weight', ruleId: 'typography/font-weight-arbitrary' }],
  ['lineHeight', { css: 'line-height', ruleId: 'typography/line-height-arbitrary' }],
  ['line-height', { css: 'line-height', ruleId: 'typography/line-height-arbitrary' }],
  ['letterSpacing', { css: 'letter-spacing', ruleId: 'typography/letter-spacing-arbitrary' }],
  ['letter-spacing', { css: 'letter-spacing', ruleId: 'typography/letter-spacing-arbitrary' }],
])
const ROLE_UTILITY_TOKENS = new Map([
  ['font', { variables: new Map([...FONT_WEIGHT_UTILITIES].map((name) => [name, `--font-weight-${name}`])), ruleId: 'typography/font-weight-arbitrary' }],
  ['leading', { variables: new Map([['normal', '--leading-normal']]), ruleId: 'typography/line-height-arbitrary' }],
  ['tracking', { variables: new Map([['normal', '--tracking-normal']]), ruleId: 'typography/letter-spacing-arbitrary' }],
])

const CSS_FAMILY_KEYWORDS = new Set(['inherit', 'initial', 'unset', 'revert', 'revert-layer'])
const CSS_GENERIC_FAMILY_KEYWORDS = new Set([
  'serif', 'sans-serif', 'monospace', 'cursive', 'fantasy', 'system-ui',
  'ui-serif', 'ui-sans-serif', 'ui-monospace', 'ui-rounded',
])
const CSS_SYSTEM_FONT_KEYWORDS = new Set([
  'caption', 'icon', 'menu', 'message-box', 'small-caption', 'status-bar',
])
const ABSOLUTE_SIZE_KEYWORDS_PX = new Map([
  ['xx-small', 9], ['x-small', 10], ['small', 13], ['medium', 16],
  ['large', 18], ['x-large', 24], ['xx-large', 32], ['xxx-large', 48],
])
const FONT_SHORTHAND_PREFIX_KEYWORDS = new Set([
  'normal', 'italic', 'oblique', 'small-caps', 'bold', 'bolder', 'lighter',
  '100', '200', '300', '400', '500', '600', '700', '800', '900',
])

const VAR_REFERENCE_PATTERN = /^var\(\s*(--[a-zA-Z0-9-]+)\s*(?:,[^)]*)?\)$/
const BARE_CUSTOM_PROPERTY_PATTERN = /^--[a-zA-Z0-9-]+$/

const DYNAMIC_UTILITY_MESSAGE = 'This dynamic class expression can resolve to a governed text-size or family utility the static lock cannot see. Make the utility static or route it through cn()/cva() with literal strings (fail-closed per the enforcement contract).'

// ---------------------------------------------------------------------------
// CSS math expression parsing + conservative interval evaluation.
//
// Interval = { lo: number | null, hi: number | null }; null means "no sound
// bound". The floor verdict needs only a sound lower bound:
//   violation     iff lo !== null && lo < FLOOR_PX
//   proven safe   iff lo !== null && lo >= FLOOR_PX
//   unsupported   otherwise (fail-closed, explicit finding).
// ---------------------------------------------------------------------------

const UNIT_TO_PX = {
  px: 1,
  pt: 4 / 3,
  pc: 16,
  in: 96,
  cm: 96 / 2.54,
  mm: 96 / 25.4,
  q: 96 / 101.6,
}
const ROOT_RELATIVE_UNITS = new Set(['rem'])
// vh/vmin/vmax have no constitutional bound (no minimum height contract); em,
// %, ch, ex and container units are relative to unknown context.
const UNPROVABLE_UNITS = new Set([
  'em', '%', 'ch', 'ex', 'ric', 'lh', 'rlh', 'cap', 'ic',
  'cqi', 'cqb', 'cqh', 'cqw', 'cqvmin', 'cqvmax',
  'vi', 'vb', 'svh', 'svw', 'lvh', 'lvw', 'dvh', 'dvw',
  'vh', 'vmin', 'vmax',
])

function tokenizeExpression(text) {
  const tokens = []
  let index = 0
  while (index < text.length) {
    const character = text[index]
    if (/\s/.test(character)) {
      index += 1
      continue
    }
    // A dash is both the minus operator and the start of a custom-property
    // name (`--type-caption-size`). A word match longer than one character
    // must win; a lone dash is the operator (calc(1rem - 0.5px)).
    const word = /^[a-zA-Z-]+/.exec(text.slice(index))
    if (word && (word[0].length > 1 || !'+-*/(),'.includes(character))) {
      tokens.push({ type: 'word', value: word[0] })
      index += word[0].length
      continue
    }
    if ('+-*/(),'.includes(character)) {
      tokens.push({ type: character })
      index += 1
      continue
    }
    const number = /^\d*\.?\d+(?:[eE][+-]?\d+)?/.exec(text.slice(index))
    if (number) {
      const value = Number.parseFloat(number[0])
      const unit = /^[a-zA-Z%]+/.exec(text.slice(index + number[0].length))
      tokens.push({ type: 'value', value, unit: unit ? unit[0] : '' })
      index += number[0].length + (unit ? unit[0].length : 0)
      continue
    }
    throw new Error(`unexpected character "${character}"`)
  }
  return tokens
}

function parseExpressionTokens(tokens) {
  let position = 0

  function peek() {
    return tokens[position]
  }

  function parseSum() {
    let left = parseProduct()
    while (peek() && (peek().type === '+' || peek().type === '-')) {
      const operator = tokens[position].type
      position += 1
      const right = parseProduct()
      left = { type: 'binary', operator, left, right }
    }
    return left
  }

  function parseProduct() {
    let left = parseUnary()
    while (peek() && (peek().type === '*' || peek().type === '/')) {
      const operator = tokens[position].type
      position += 1
      const right = parseUnary()
      left = { type: 'binary', operator, left, right }
    }
    return left
  }

  function parseUnary() {
    if (peek() && peek().type === '-') {
      position += 1
      return { type: 'negation', operand: parseUnary() }
    }
    if (peek() && peek().type === '+') {
      position += 1
      return parseUnary()
    }
    return parseAtom()
  }

  function parseAtom() {
    const token = peek()
    if (!token) throw new Error('unexpected end of expression')
    if (token.type === '(') {
      position += 1
      const inner = parseSum()
      if (!peek() || peek().type !== ')') throw new Error('missing closing parenthesis')
      position += 1
      return inner
    }
    if (token.type === 'value') {
      position += 1
      return { type: 'dimension', value: token.value, unit: token.unit }
    }
    if (token.type === 'word') {
      const name = token.value
      position += 1
      if (!peek() || peek().type !== '(') throw new Error(`bare word "${name}" is not a length`)
      position += 1
      if (name === 'var') {
        const nameToken = peek()
        if (!nameToken || nameToken.type !== 'word' || !nameToken.value.startsWith('--')) {
          throw new Error('var() requires a custom property name')
        }
        position += 1
        // Optional fallback after the name: var(--x, <anything>). It cannot
        // tighten the verdict (the var contributes an unknown interval), so
        // consume it without evaluating.
        if (peek() && peek().type === ',') {
          let depth = 0
          while (position < tokens.length) {
            const kind = tokens[position].type
            if (kind === '(') depth += 1
            if (kind === ')') {
              if (depth === 0) break
              depth -= 1
            }
            position += 1
          }
        }
        if (!peek() || peek().type !== ')') throw new Error('missing closing parenthesis for var()')
        position += 1
        return { type: 'varRef', name: nameToken.value }
      }
      const args = []
      if (peek() && peek().type !== ')') {
        args.push(parseSum())
        while (peek() && peek().type === ',') {
          position += 1
          args.push(parseSum())
        }
      }
      if (!peek() || peek().type !== ')') throw new Error(`missing closing parenthesis for ${name}()`)
      position += 1
      return { type: 'function', name, args }
    }
    throw new Error(`unexpected token of type ${token.type}`)
  }

  const expression = parseSum()
  if (position !== tokens.length) throw new Error('trailing tokens after expression')
  return expression
}

function printExpression(node) {
  switch (node.type) {
    case 'dimension':
      return `${node.value}${node.unit}`
    case 'varRef':
      return `var(${node.name})`
    case 'negation':
      return `-${printExpression(node.operand)}`
    case 'binary':
      return `${printExpression(node.left)} ${node.operator} ${printExpression(node.right)}`
    case 'function':
      return `${node.name}(${node.args.map(printExpression).join(', ')})`
    default:
      throw new Error(`unprintable node ${node.type}`)
  }
}

function dimensionInterval(value, unit) {
  if (!unit || unit === 'px') return { lo: value, hi: value }
  if (ROOT_RELATIVE_UNITS.has(unit)) {
    // §D1: the root spans 12–20px, so rem must hold the floor across it.
    return { lo: value * ROOT_MIN_PX, hi: value * ROOT_MAX_PX }
  }
  if (unit === 'vw') {
    // §D10: 320px reflow floor → 1vw >= 3.2px; no upper bound.
    return { lo: (value * REFLOW_MIN_VIEWPORT_PX) / 100, hi: Number.POSITIVE_INFINITY }
  }
  if (UNPROVABLE_UNITS.has(unit)) return { lo: null, hi: null }
  if (Object.hasOwn(UNIT_TO_PX, unit)) {
    const px = value * UNIT_TO_PX[unit]
    return { lo: px, hi: px }
  }
  return { lo: null, hi: null }
}

function addIntervals(a, b) {
  return {
    lo: a.lo === null || b.lo === null ? null : a.lo + b.lo,
    hi: a.hi === null || b.hi === null ? null : a.hi + b.hi,
  }
}

function subtractIntervals(a, b) {
  return {
    lo: a.lo === null || b.hi === null ? null : a.lo - b.hi,
    hi: a.hi === null || b.lo === null ? null : a.hi - b.lo,
  }
}

function multiplyIntervals(a, b) {
  const bounds = [a.lo, a.hi, b.lo, b.hi]
  if (bounds.some((bound) => bound === null || !Number.isFinite(bound))) return { lo: null, hi: null }
  const products = [a.lo * b.lo, a.lo * b.hi, a.hi * b.lo, a.hi * b.hi]
  return { lo: Math.min(...products), hi: Math.max(...products) }
}

function divideIntervals(a, b) {
  if (a.lo === null || a.hi === null || b.lo === null || b.hi === null) return { lo: null, hi: null }
  if (b.lo <= 0 && b.hi >= 0) return { lo: null, hi: null } // divisor crosses zero
  const quotients = [a.lo / b.lo, a.lo / b.hi, a.hi / b.lo, a.hi / b.hi]
  return { lo: Math.min(...quotients), hi: Math.max(...quotients) }
}

function maxIntervals(intervals) {
  // max(a, b) >= a and >= b, so any operand's sound lower bound bounds the
  // result from below — unknown operands cannot lower it. The upper bound is
  // only sound when every operand has one.
  const knownLos = intervals.map((interval) => interval.lo).filter((lo) => lo !== null)
  const allHisKnown = intervals.every((interval) => interval.hi !== null)
  return {
    lo: knownLos.length > 0 ? Math.max(...knownLos) : null,
    hi: allHisKnown ? Math.max(...intervals.map((interval) => interval.hi)) : null,
  }
}

function minIntervals(intervals) {
  // min(a, b) <= a and <= b: one unknown operand can push the result below
  // anything known, so both bounds are only sound when all operands are known.
  if (intervals.some((interval) => interval.lo === null || interval.hi === null)) {
    return { lo: null, hi: null }
  }
  return {
    lo: Math.min(...intervals.map((interval) => interval.lo)),
    hi: Math.min(...intervals.map((interval) => interval.hi)),
  }
}

function clampIntervals(args) {
  // clamp(min, value, max) === max(min, min(value, max)).
  return maxIntervals([args[0], minIntervals([args[1], args[2]])])
}

// Evaluates a parsed expression against the floor. var() references cannot be
// resolved to numbers by CSS name from the policy, so they contribute an
// unknown interval; registration is tracked separately.
function evaluateExpression(node, registeredTokens, resolvedTokenValues = new Map()) {
  const state = { unregistered: [] }

  function evaluate(current) {
    switch (current.type) {
      case 'dimension':
        return dimensionInterval(current.value, current.unit)
      case 'varRef':
        if (!registeredTokens.has(current.name)) state.unregistered.push(current.name)
        if (resolvedTokenValues.has(current.name)) {
          const resolved = parseExpressionTokens(tokenizeExpression(String(resolvedTokenValues.get(current.name))))
          return evaluate(resolved)
        }
        return { lo: null, hi: null }
      case 'negation':
        return multiplyIntervals({ lo: -1, hi: -1 }, evaluate(current.operand))
      case 'binary': {
        const left = evaluate(current.left)
        const right = evaluate(current.right)
        if (current.operator === '+') return addIntervals(left, right)
        if (current.operator === '-') return subtractIntervals(left, right)
        if (current.operator === '*') return multiplyIntervals(left, right)
        return divideIntervals(left, right)
      }
      case 'function': {
        if (current.name === 'calc') {
          if (current.args.length !== 1) throw new Error('calc() takes one argument')
          return evaluate(current.args[0])
        }
        if (current.name === 'min') return minIntervals(current.args.map(evaluate))
        if (current.name === 'max') return maxIntervals(current.args.map(evaluate))
        if (current.name === 'clamp') {
          if (current.args.length !== 3) throw new Error('clamp() takes three arguments')
          return clampIntervals(current.args.map(evaluate))
        }
        throw new Error(`unsupported function ${current.name}()`)
      }
      default:
        throw new Error(`unevaluable node ${current.type}`)
    }
  }

  try {
    const interval = evaluate(node)
    if (state.unregistered.length > 0) return { status: 'unregistered' }
    if (interval.lo === null) {
      // A bare registered var is trusted to the token layer plus the browser
      // harness; a registered var inside arithmetic is not (calc(var(--t) - 4px)
      // can fall under the floor and cannot be bounded from policy).
      const isBareRegisteredVar = node.type === 'varRef' && registeredTokens.has(node.name)
      if (isBareRegisteredVar) return { status: 'ok' }
      return { status: 'unsupported' }
    }
    return { status: interval.lo < FLOOR_PX ? 'below-floor' : 'ok', lowerBound: interval.lo }
  } catch {
    return { status: 'unsupported' }
  }
}

function scanSvg({ path, source, registeredTokens, resolvedTokenValues }) {
  const findings = []
  // XML declarations, doctypes and comments are not JSX nodes and cannot
  // carry typography. Mask them while preserving newlines and offsets; the
  // remaining element/attribute structure is parsed by the TypeScript JSX AST.
  let jsxSource = source.replace(/<\?[\s\S]*?\?>|<!DOCTYPE[\s\S]*?>|<!--[\s\S]*?-->/gi, (text) => text.replace(/[^\n]/g, ' '))
  for (const match of source.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/gi)) {
    const cssOffset = match.index + match[0].indexOf(match[1])
    const baseLine = source.slice(0, cssOffset).split('\n').length - 1
    for (const finding of scanCss({ path, source: match[1], registeredTokens, resolvedTokenValues })) {
      findings.push({ ...finding, line: finding.line ? finding.line + baseLine : undefined })
    }
    // CSS braces are JSX expression delimiters. Mask only the already-parsed
    // style body, retaining newlines so AST locations still map to the SVG.
    const masked = match[1].replace(/[^\n]/g, ' ')
    jsxSource = `${jsxSource.slice(0, cssOffset)}${masked}${jsxSource.slice(cssOffset + match[1].length)}`
  }
  const jsxFindings = scanTypeScript({ path, source: `const __svg = (${jsxSource})`, registeredTokens, resolvedTokenValues })
  findings.push(...jsxFindings)
  return findings
}

// ---------------------------------------------------------------------------
// Value-level entry points shared by the TS and CSS scans.
// ---------------------------------------------------------------------------

// Evaluates a font-size value string → { status, printed }.
function evaluateFontSizeValue(text, registeredTokens, resolvedTokenValues = new Map()) {
  const trimmed = text.trim()
  if (['inherit', 'initial', 'unset', 'revert', 'revert-layer'].includes(trimmed)) return { status: 'ok', printed: trimmed }
  if (ABSOLUTE_SIZE_KEYWORDS_PX.has(trimmed)) return { status: ABSOLUTE_SIZE_KEYWORDS_PX.get(trimmed) < FLOOR_PX ? 'below-floor' : 'ok', printed: trimmed }
  if (trimmed === 'smaller' || trimmed === 'larger') return { status: 'unsupported', printed: trimmed }
  const varMatch = VAR_REFERENCE_PATTERN.exec(trimmed)
  if (varMatch) {
    if (!registeredTokens.has(varMatch[1])) return { status: 'unregistered', printed: `var(${varMatch[1]})` }
    const resolved = resolvedTokenValues.get(varMatch[1])
    if (resolved !== undefined) return { ...evaluateFontSizeValue(String(resolved), registeredTokens, new Map()), printed: `var(${varMatch[1]})` }
    return { status: 'ok', printed: `var(${varMatch[1]})` }
  }
  try {
    const ast = parseExpressionTokens(tokenizeExpression(trimmed))
    const verdict = evaluateExpression(ast, registeredTokens, resolvedTokenValues)
    return { status: verdict.status, printed: printExpression(ast) }
  } catch {
    return { status: 'unsupported', printed: collapseWhitespace(trimmed) }
  }
}

// Evaluates a font-family value → { status: 'ok' | 'unregistered' | 'literal', printed }.
function evaluateFontFamilyValue(text, registeredTokens) {
  const trimmed = text.trim()
  const varMatch = VAR_REFERENCE_PATTERN.exec(trimmed)
  if (varMatch) {
    const status = registeredTokens.has(varMatch[1]) ? 'ok' : 'unregistered'
    return { status, printed: `var(${varMatch[1]})` }
  }
  if (CSS_FAMILY_KEYWORDS.has(trimmed)) return { status: 'ok', printed: trimmed }
  const parts = trimmed.split(',').map((part) => collapseWhitespace(part))
  const unregistered = []
  let sawRegisteredVar = false
  for (const part of parts) {
    const partVar = VAR_REFERENCE_PATTERN.exec(part)
    if (partVar) {
      if (registeredTokens.has(partVar[1])) sawRegisteredVar = true
      else unregistered.push(partVar[1])
    }
  }
  if (unregistered.length > 0) return { status: 'unregistered', printed: parts.join(', ') }
  const literalParts = parts.filter((part) => !VAR_REFERENCE_PATTERN.test(part))
  if (sawRegisteredVar) {
    // Trailing generic-keyword fallbacks after a registered token are
    // defensive CSS hygiene; specific literal families alongside it are not.
    const offending = literalParts.filter((part) => !CSS_GENERIC_FAMILY_KEYWORDS.has(part))
    return { status: offending.length === 0 ? 'ok' : 'literal', printed: parts.join(', ') }
  }
  if (literalParts.length === 0) return { status: 'ok', printed: parts.join(', ') }
  return { status: 'literal', printed: parts.join(', ') }
}

// Splits a `font` shorthand into size expression and family list.
// `font: [style|variant|weight]... <size>[/line-height]? <family>#`.
function parseFontShorthand(text) {
  const tokens = collapseWhitespace(text.trim()).split(' ')
  if (tokens.length > 0 && tokens.every((token) => CSS_SYSTEM_FONT_KEYWORDS.has(token))) {
    return { systemKeyword: true }
  }
  let size = null
  let familyStart = -1
  for (let index = 0; index < tokens.length; index += 1) {
    const token = tokens[index]
    if (size === null && familyStart === -1 && FONT_SHORTHAND_PREFIX_KEYWORDS.has(token)) continue
    const sizeMatch = /^[\d.]+[a-z%]*/.exec(token)
    if (size === null && sizeMatch && /^[\d.]/.test(token)) {
      // The /line-height suffix is not division; drop it before evaluation.
      size = token.split('/')[0]
      familyStart = index + 1
      continue
    }
    if (size !== null) break
  }
  if (size === null) return { systemKeyword: false, unsupported: true }
  const family = tokens.slice(familyStart).join(' ')
  return { systemKeyword: false, unsupported: false, size, family }
}

function collapseWhitespace(text) {
  return text.replace(/\s+/g, ' ').trim()
}

// ---------------------------------------------------------------------------
// Tailwind class-token classification.
// ---------------------------------------------------------------------------

// Every classification callback receives a sink:
//   { emit(finding, position?), isRegistered(name), registeredTokens }.
function classifyClassToken(token, sink) {
  const stripped = token.replace(/^!/, '').replace(/!$/, '')
  if (token.endsWith('-') && stripped === token && (token === 'text-' || token === 'font-')) {
    // A bare `text-` / `font-` prefix — always the left half of a glued
    // dynamic value once template/concat handling has decomposed the site.
    sink.emit({
      ruleId: 'typography/unsupported-text-utility',
      syntax: `${token}\${…}`,
      message: DYNAMIC_UTILITY_MESSAGE,
    })
    return
  }
  if (stripped.startsWith('text-')) {
    classifyTextUtility(stripped.slice('text-'.length), stripped, sink)
    return
  }
  if (stripped.startsWith('font-')) {
    classifyFontUtility(stripped.slice('font-'.length), sink)
    return
  }
  if (stripped.startsWith('leading-')) classifyRoleUtility('leading', stripped.slice('leading-'.length), sink)
  if (stripped.startsWith('tracking-')) classifyRoleUtility('tracking', stripped.slice('tracking-'.length), sink)
}

function classifyRoleUtility(prefix, rest, sink) {
  const contract = ROLE_UTILITY_TOKENS.get(prefix)
  const syntax = `${prefix}-${rest}`
  const arbitrary = matchArbitrary(rest)
  if (arbitrary) {
    const value = collapseArbitraryWhitespace(arbitrary.inner)
    const variable = VAR_REFERENCE_PATTERN.exec(value)
    if (variable && sink.isRegistered(variable[1])) return
    sink.emit({
      ruleId: variable ? 'typography/unregistered-typography-token' : contract.ruleId,
      syntax: `${prefix}-${arbitrary.wrapper}${value}${arbitrary.wrapper === '[' ? ']' : ')'}`,
      message: variable ? `${syntax} references an unregistered typography token.` : `${syntax} is an arbitrary D9 typography value; use a registered role token.`,
    })
    return
  }
  const token = contract.variables.get(rest)
  if (token && sink.isRegistered(token)) return
  if (token) sink.emit({ ruleId: contract.ruleId, syntax, message: `${syntax} is not backed by a registered D9 role token.` })
}

function classifyTextUtility(rest, canonicalToken, sink) {
  // v4 line-height modifier (`text-sm/4`): the size part governs.
  const sizeName = rest.split('/')[0]
  if (Object.hasOwn(TEXT_SCALE_REM, sizeName)) {
    const rem = TEXT_SCALE_REM[sizeName]
    if (rem < 1) {
      sink.emit({
        ruleId: 'typography/text-size-below-floor',
        syntax: canonicalToken,
        message: `text-${sizeName} resolves to the Tailwind default ${rem}rem, which computes to ${rem * ROOT_MIN_PX}px at the ${ROOT_MIN_PX}px minimum root (§D1/§D2: no UI text below ${FLOOR_PX}px). Use the ${FLOOR_PX}px caption/label role tokens or a floor-guarded remap registered centrally.`,
      })
    }
    return
  }
  if (NON_SIZE_TEXT_UTILITIES.has(sizeName)) return
  const arbitrary = matchArbitrary(rest)
  if (arbitrary) {
    classifyArbitrarySize(arbitrary, sink)
    return
  }
  // Unknown bare word: a colour utility or a theme-registered custom utility
  // this per-file scanner cannot see (documented limitation, central registry
  // requested). Never guessed at.
}

// This bounded grammar accepts only complete var-to-var fallback chains.
// Other fallback expressions keep their existing classification paths.
function nestedTokenFallbacks(value) {
  const names = []
  let rest = value
  while (true) {
    const leaf = /^var\(\s*(--[a-zA-Z0-9-]+)\s*\)$/.exec(rest)
    if (leaf) return names.length > 0 ? [...names, leaf[1]] : null
    const branch = /^var\(\s*(--[a-zA-Z0-9-]+)\s*,\s*(var\(.*\))\s*\)$/.exec(rest)
    if (!branch) return null
    names.push(branch[1])
    rest = branch[2]
  }
}

function classifyArbitrarySize({ inner, wrapper }, sink) {
  const canonicalInner = collapseArbitraryWhitespace(inner)
  const closing = wrapper === '[' ? ']' : ')'
  const syntax = `text-${wrapper}${canonicalInner}${closing}`
  if (canonicalInner.startsWith('color:')) return // colours lane
  if (canonicalInner.startsWith('length:')) {
    evaluateArbitrarySizeValue(canonicalInner.slice('length:'.length), syntax, sink)
    return
  }
  // The app's dominant dynamic-colour pattern is the untyped
  // `text-[var(--color-…)]`. A registered var in the system's --color-
  // namespace is a colour token — the colours lane owns it, not this lock.
  // The namespace only ever narrows this rule away; it never permits a size.
  const nestedTokens = nestedTokenFallbacks(canonicalInner)
  if (nestedTokens) {
    if (nestedTokens.every((token) => sink.isRegistered(token) && token.startsWith('--color-'))) return
    const hasTypography = nestedTokens.some((token) => sink.isRegistered(token) && (token.startsWith('--type-') || token.startsWith('--font-size-')))
    sink.emit({
      ruleId: hasTypography ? 'typography/unsupported-text-utility' : 'typography/missing-arbitrary-type-hint',
      syntax,
      message: `${syntax} has a custom-property fallback chain whose colour-versus-length kind is not proven. Add an explicit color: or length: hint and resolve unregistered references separately.`,
    })
    return
  }
  // A non-nested fallback must not consume unmatched opening parentheses.
  const untypedVar = /^var\(\s*(--[a-zA-Z0-9-]+)\s*(?:,[^()]*)?\)$/.exec(canonicalInner)
  if (untypedVar) {
    const token = untypedVar[1]
    if (sink.isRegistered(token) && token.startsWith('--color-')) return
    if (sink.isRegistered(token) && (token.startsWith('--type-') || token.startsWith('--font-size-'))) {
      sink.emit({
        ruleId: 'typography/unsupported-text-utility',
        syntax,
        message: `${syntax} refers to a registered typography token without the required length: type hint. Use text-[length:var(${token})] so Tailwind and the static lock agree on its kind.`,
      })
      return
    }
    sink.emit({
      ruleId: 'typography/missing-arbitrary-type-hint',
      syntax,
      message: `${syntax} is a well-formed custom-property utility whose colour-versus-length kind is not proven by canonical policy. Add color: or length: for the intended kind; if the token is unregistered, register or replace it separately during C1 migration.`,
    })
    return
  }
  if (isColourLike(canonicalInner)) return // colours lane
  if (BARE_CUSTOM_PROPERTY_PATTERN.test(canonicalInner)) {
    evaluateArbitrarySizeValue(canonicalInner, syntax, sink)
    return
  }
  if (isLengthLike(canonicalInner)) {
    evaluateArbitrarySizeValue(canonicalInner, syntax, sink)
    return
  }
  sink.emit({
    ruleId: 'typography/unsupported-text-utility',
    syntax,
    message: `The arbitrary utility ${syntax} cannot be classified as a size, a colour, or a token reference by the static lock. Use text-[length:var(--type-…)] for a registered size token, or an explicit colour form for colours.`,
  })
}

function evaluateArbitrarySizeValue(value, syntax, sink) {
  const varMatch = VAR_REFERENCE_PATTERN.exec(value)
  const bareMatch = BARE_CUSTOM_PROPERTY_PATTERN.exec(value)
  if (varMatch || bareMatch) {
    const name = varMatch ? varMatch[1] : value
    if (!sink.isRegistered(name)) {
      sink.emit({
        ruleId: 'typography/unregistered-font-size-token',
        syntax,
        message: `${syntax} references ${name}, which is not a registered typography token (§D9; the migration plan requires zero unregistered arbitrary type values).`,
      })
    }
    return
  }
  try {
    const ast = parseExpressionTokens(tokenizeExpression(value))
    const verdict = evaluateExpression(ast, sink.registeredTokens)
    if (verdict.status === 'unregistered') {
      sink.emit({
        ruleId: 'typography/unregistered-font-size-token',
        syntax,
        message: `${syntax} references an unregistered custom property (§D9 token boundary).`,
      })
      return
    }
    if (verdict.status === 'unsupported') {
      sink.emit({
        ruleId: 'typography/unsupported-text-utility',
        syntax,
        message: `${syntax} uses an expression the static lock cannot bound against the ${FLOOR_PX}px floor (dynamic or unbounded relative units). Use a registered size token or prove it in the browser harness.`,
      })
      return
    }
    // §E1 rejects the arbitrary text-size utility form itself, even when the
    // literal clears the floor.
    sink.emit({
      ruleId: 'typography/arbitrary-text-size',
      syntax,
      message: `${syntax} is an arbitrary text-size utility; §E1/D9 require sizes from the role tokens (text-[length:var(--type-…)]) instead of arbitrary values${verdict.lowerBound !== undefined && verdict.lowerBound < FLOOR_PX ? ` (this one also computes to ${verdict.lowerBound}px at the minimum root, below the ${FLOOR_PX}px floor)` : ''}.`,
    })
  } catch {
    sink.emit({
      ruleId: 'typography/unsupported-text-utility',
      syntax,
      message: `${syntax} is not parseable as a size expression; the lock fails closed rather than guess.`,
    })
  }
}

function classifyFontUtility(rest, sink) {
  const value = rest.split('/')[0]
  if (FONT_WEIGHT_UTILITIES.has(value)) {
    classifyRoleUtility('font', value, sink)
    return
  }
  if (OFF_TOKEN_FAMILY_UTILITIES.has(value)) {
    sink.emit({
      ruleId: 'typography/font-family-off-token',
      syntax: `font-${value}`,
      message: `font-${value} resolves to the Tailwind default family stack, not the Outfit/Inter/JetBrains Mono tokens (§D9). Neither live build remaps it; use a token-backed family utility or var(--type-…-family).`,
    })
    return
  }
  if (REMAPPED_FAMILY_UTILITIES.has(value)) return
  const arbitrary = matchArbitrary(rest)
  if (arbitrary) {
    const canonicalInner = collapseArbitraryWhitespace(arbitrary.inner)
    const closing = arbitrary.wrapper === '[' ? ']' : ')'
    const syntax = `font-${arbitrary.wrapper}${canonicalInner}${closing}`
    if (canonicalInner.startsWith('family-name:')) {
      classifyArbitraryFamily(canonicalInner.slice('family-name:'.length), syntax, sink)
      return
    }
    if (/^['"]/.test(canonicalInner)) {
      classifyArbitraryFamily(canonicalInner, syntax, sink)
      return
    }
    if (/^[\d.]/.test(canonicalInner) || VAR_REFERENCE_PATTERN.test(canonicalInner)) {
      classifyRoleUtility('font', rest, sink)
      return
    }
    sink.emit({
      ruleId: 'typography/unsupported-text-utility',
      syntax,
      message: `${syntax} cannot be classified as a family, a weight, or a token reference by the static lock.`,
    })
  }
  // Unknown bare family-shaped utility (theme-registered) — skipped,
  // documented limitation.
}

function classifyArbitraryFamily(value, syntax, sink) {
  const varMatch = VAR_REFERENCE_PATTERN.exec(value)
  const bareMatch = BARE_CUSTOM_PROPERTY_PATTERN.exec(value)
  if (varMatch || bareMatch) {
    const name = varMatch ? varMatch[1] : value
    if (!sink.isRegistered(name)) {
      sink.emit({
        ruleId: 'typography/font-family-literal',
        syntax,
        message: `${syntax} references ${name}, which is not a registered family token (§D9).`,
      })
    }
    return
  }
  sink.emit({
    ruleId: 'typography/font-family-literal',
    syntax,
    message: `${syntax} is a literal font family; §D9 requires families from the Outfit/Inter/JetBrains Mono token set.`,
  })
}

function matchArbitrary(rest) {
  const bracket = /^\[(.*)\]$/.exec(rest)
  if (bracket) return { inner: bracket[1], wrapper: '[' }
  const paren = /^\((.*)\)$/.exec(rest)
  if (paren) return { inner: paren[1], wrapper: '(' }
  return null
}

function collapseArbitraryWhitespace(inner) {
  return collapseWhitespace(inner).replace(/\s*:\s*/, ':')
}

function isColourLike(value) {
  return /^(#|rgb\(|rgba\(|hsl\(|hsla\(|oklch\(|oklab\(|lab\(|lch\(|color\()/.test(value)
}

function isLengthLike(value) {
  return /^[\d.]/.test(value) || /^(calc|clamp|min|max)\(/.test(value)
}

// ---------------------------------------------------------------------------
// TypeScript / TSX scan.
// ---------------------------------------------------------------------------

function scriptKindFor(path) {
  const lowerPath = path.toLowerCase()
  if (lowerPath.endsWith('.svg')) return ts.ScriptKind.JSX
  if (lowerPath.endsWith('.tsx')) return ts.ScriptKind.TSX
  if (lowerPath.endsWith('.ts') || lowerPath.endsWith('.mts') || lowerPath.endsWith('.cts')) return ts.ScriptKind.TS
  if (lowerPath.endsWith('.jsx')) return ts.ScriptKind.JSX
  return ts.ScriptKind.JS
}

function scanTypeScript({ path, source, registeredTokens, resolvedTokenValues, modules, moduleCache }) {
  const sourceFile = ts.createSourceFile(path, source, ts.ScriptTarget.ES2022, true, scriptKindFor(path))
  const findings = []
  const seen = new Set()
  // className initializers are walked with their own positions; the builder
  // pass must not walk the same call again and duplicate findings.
  const handledCalls = new WeakSet()

  function report(finding) {
    const key = `${finding.ruleId}|${finding.line ?? ''}|${finding.column ?? ''}|${finding.syntax}`
    if (seen.has(key)) return
    seen.add(key)
    findings.push({ path, ...finding })
  }

  const parseDiagnostics = sourceFile.parseDiagnostics
  if (Array.isArray(parseDiagnostics) && parseDiagnostics.length > 0) {
    findings.push({
      ruleId: 'typography/parse-failure',
      path,
      syntax: `${parseDiagnostics.length} TypeScript parse diagnostics`,
      message: `TypeScript could not parse ${path} cleanly (${parseDiagnostics.length} diagnostic(s); first: ${ts.flattenDiagnosticMessageText(parseDiagnostics[0].messageText, ' ')}). The lock fails closed on unparseable governed files.`,
    })
    return findings
  }

  function positionAt(offset) {
    const position = sourceFile.getLineAndCharacterOfPosition(offset)
    return { line: position.line + 1, column: position.character + 1 }
  }

  const bindings = new Map()
  const duplicateBindings = new Set()
  const scannedParameterDefaults = new WeakSet()
  sourceFile.forEachChild((node) => {
    if (!ts.isVariableStatement(node) || !(node.declarationList.flags & ts.NodeFlags.Const)) return
    for (const declaration of node.declarationList.declarations) if (ts.isIdentifier(declaration.name) && declaration.initializer) {
      if (bindings.has(declaration.name.text)) duplicateBindings.add(declaration.name.text)
      else bindings.set(declaration.name.text, declaration.initializer)
    }
  })
  function forwardedClassBoundary(identifier) {
    let fn = identifier.parent
    while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
    if (!fn) return null
    // Provenance guard: a plain same-named declaration anywhere between the
    // sink and this function wins the binding, so the forwarded value is not
    // proven to be the caller-supplied className.
    if (plainDeclarationShadows(identifier, fn)) return null
    let initializer = null
    let declaration = null
    for (const parameter of fn.parameters) {
      if (ts.isIdentifier(parameter.name) && parameter.name.text === identifier.text && identifier.text === 'className') {
        initializer = parameter.initializer; declaration = parameter.name; break
      }
      if (ts.isObjectBindingPattern(parameter.name)) for (const element of parameter.name.elements) {
        if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
        const property = element.propertyName?.getText(sourceFile) ?? element.name.text
        if (property === 'className') { initializer = element.initializer; declaration = element.name; break }
      }
    }
    let sourceParameterName = null
    if (!declaration && fn.body) {
      // Body destructuring — `const { className } = props` and the renamed
      // `const { className: alias } = props` — is the second proven unchanged
      // forwarding shape. Provenance requires the destructured source to be a
      // bare identifier that is a parameter of this same function and the
      // binding to be `const`; helper/store outputs, member objects with
      // fallbacks, `let` bindings and non-className properties keep unsupported.
      const parameterNames = new Set()
      for (const parameter of fn.parameters) if (ts.isIdentifier(parameter.name)) parameterNames.add(parameter.name.text)
      let scope = identifier.parent
      while (scope && scope !== fn) {
        if (ts.isBlock(scope)) {
          const matches = []
          for (const statement of scope.statements) {
            if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
            for (const candidate of statement.declarationList.declarations) {
              if (!ts.isObjectBindingPattern(candidate.name)) continue
              const source = candidate.initializer
              if (!ts.isIdentifier(source) || !parameterNames.has(source.text)) continue
              for (const element of candidate.name.elements) {
                if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
                const property = element.propertyName?.getText(sourceFile) ?? element.name.text
                if (property === 'className') matches.push({ initializer: element.initializer, declaration: element.name, source: source.text })
              }
            }
          }
          // Only the innermost unambiguous destructuring classifies.
          if (matches.length > 1) return null
          if (matches.length === 1) {
            initializer = matches[0].initializer; declaration = matches[0].declaration; sourceParameterName = matches[0].source; break
          }
        }
        scope = scope.parent
      }
    }
    if (!declaration) return null
    let reassigned = false
    function check(node) {
      if (ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === identifier.text
        && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
      // For body destructuring, mutating the source parameter or its
      // className member before the read breaks unchanged provenance.
      if (sourceParameterName) {
        if (ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === sourceParameterName
          && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
        if (ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) && node.left.name.text === 'className'
          && ts.isIdentifier(node.left.expression) && node.left.expression.text === sourceParameterName
          && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
        if (ts.isDeleteExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'className'
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === sourceParameterName) reassigned = true
        if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'assign'
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object'
          && node.arguments.length > 0 && ts.isIdentifier(node.arguments[0]) && node.arguments[0].text === sourceParameterName) reassigned = true
      }
      node.forEachChild(check)
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    let parent = fn.parent
    while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    while (parent && ts.isCallExpression(parent) && isComponentWrapper(parent.expression)) {
      parent = parent.parent
      while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    }
    const ownerName = ts.isFunctionDeclaration(fn) && fn.name ? fn.name.text
      : parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) ? parent.name.text
        : anonymousRegistrationOwner(fn)
    if (!ownerName) return null
    return { symbol: ownerName, name: 'className', initializer, declaration }
  }

  function isComponentWrapper(expression) {
    if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
    return ts.isPropertyAccessExpression(expression) && (expression.name.text === 'forwardRef' || expression.name.text === 'memo')
  }

  function anonymousRegistrationOwner(fn) {
    const properties = []
    let current = fn
    while (current.parent) {
      const parent = current.parent
      if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent)) {
        current = parent
        continue
      }
      if (ts.isPropertyAssignment(parent) && parent.initializer === current) {
        if (ts.isComputedPropertyName(parent.name)) return null
        const name = ts.isIdentifier(parent.name) || ts.isStringLiteral(parent.name) || ts.isNumericLiteral(parent.name) ? parent.name.text : null
        if (!name) return null
        properties.unshift(name)
        current = parent.parent
        continue
      }
      if (ts.isObjectLiteralExpression(parent)) {
        current = parent
        continue
      }
      if (ts.isSpreadAssignment(parent)) return null
      if ((ts.isArrowFunction(parent) || ts.isFunctionExpression(parent)) && parent.body === current) {
        current = parent
        continue
      }
      if (ts.isCallExpression(parent) && parent.arguments.includes(current) && properties.length > 0) {
        const key = parent.arguments[0]
        if (!key || (!ts.isStringLiteral(key) && !ts.isNoSubstitutionTemplateLiteral(key))) return null
        const callee = parent.expression.getText(sourceFile).replace(/\s+/g, '')
        if (!/^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$/.test(callee)) return null
        return `${callee}(${key.text}):${properties.join('.')}`
      }
      return null
    }
    return null
  }

  const moduleContext = { modules, moduleCache }
  const classWalker = createClassWalker({ report, positionAt, sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary, scannedParameterDefaults, moduleContext, handledCalls })
  const styleWalker = createStyleWalker({ report, positionAt, sourceFile, registeredTokens, resolvedTokenValues, moduleContext })
  function visit(node) {
    if (ts.isJsxAttribute(node)) {
      const attributeName = node.name.text
      if (attributeName === 'className' || attributeName === 'class') {
        const initializer = node.initializer
        if (initializer && ts.isJsxExpression(initializer) && initializer.expression) {
          const reportClass = classWalker.classReporterFor(initializer)
          if (ts.isCallExpression(initializer.expression)) markClassBuilderTree(initializer.expression)
          classWalker.walkClassExpression(initializer.expression, reportClass)
        } else if (initializer && ts.isStringLiteral(initializer)) {
          classWalker.classifyClassString(initializer.text, initializer)
        }
      }
      if (attributeName === 'fontSize' || attributeName === 'font-size') styleWalker.walkJsxValue(node, 'fontSize')
      if (attributeName === 'fontFamily' || attributeName === 'font-family') styleWalker.walkJsxValue(node, 'fontFamily')
      if (attributeName === 'style' && path.endsWith('.svg') && node.initializer && ts.isStringLiteral(node.initializer)) {
        const nested = scanCss({ path, source: `.svg-inline { ${node.initializer.text} }`, registeredTokens })
        nested.forEach((finding) => report({ ...finding, line: positionAt(node.getStart(sourceFile)).line, column: positionAt(node.getStart(sourceFile)).column }))
      }
    }
    if (path.endsWith('.svg') && ts.isJsxElement(node) && node.openingElement.tagName.getText(sourceFile) === 'style') {
      const css = node.children.filter(ts.isJsxText).map((child) => child.text).join('')
      if (css.trim()) scanCss({ path, source: css, registeredTokens }).forEach((finding) => report(finding))
    }
    if (ts.isCallExpression(node)) {
      const callee = node.expression
      if (isAuthenticCvaFactoryCall(node)) {
        handledCalls.add(node)
        const reportClass = classWalker.classReporterFor(node)
        node.arguments.forEach((argument) => classWalker.walkClassExpression(argument, reportClass))
      } else if (ts.isIdentifier(callee) && CLASS_BUILDERS.has(callee.text) && !handledCalls.has(node)) {
        markClassBuilderTree(node)
        const reportClass = classWalker.classReporterFor(node)
        node.arguments.forEach((argument) => classWalker.walkClassExpression(argument, reportClass))
      }
    }
    if (ts.isPropertyAssignment(node)) {
      styleWalker.walkStyleProperty(node)
    }
    node.forEachChild(visit)
  }

  function markClassBuilderTree(node) {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_BUILDERS.has(node.expression.text)) handledCalls.add(node)
    node.forEachChild(markClassBuilderTree)
  }

  function isAuthenticCvaFactoryCall(node) {
    return authenticCvaImport(node.expression)
  }

  visit(sourceFile)
  return findings
}

function createClassWalker({ report, positionAt, sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary, scannedParameterDefaults, moduleContext, handledCalls }) {
  const nodeHelpers = createClassNodeHelpers(sourceFile)
  // A sink bound to one source range; token positions refine it.
  function classReporterFor(node) {
    return makeSink(positionAt(node.getStart(sourceFile)))
  }

  function makeSink(base) {
    return {
      emit(finding, position) {
        const at = position ?? base
        report({ ...finding, line: at.line, column: at.column })
      },
      isRegistered: (name) => registeredTokens.has(name),
      registeredTokens,
    }
  }

  function classifyClassString(text, node) {
    classifyInto(text, node, classReporterFor(node))
  }

  function walkClassExpression(node, sink) {
    if (!node) return
    switch (node.kind) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
        classifyInto(node.text, node, sink)
        return
      case ts.SyntaxKind.TemplateExpression:
        nodeHelpers.walkDynamicTemplate(node, sink, walkClassExpression)
        return
      case ts.SyntaxKind.ConditionalExpression:
        walkClassExpression(node.whenTrue, sink)
        walkClassExpression(node.whenFalse, sink)
        return
      case ts.SyntaxKind.BinaryExpression:
        if (node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
          const alternatives = expandStaticClassStrings(node)
          if (alternatives) {
            alternatives.forEach((text) => classifyInto(text, node, sink))
            return
          }
          const staticText = collectStaticTextForBinding(node)
          if (staticText !== null) {
            classifyInto(staticText, node, sink)
            return
          }
        }
        nodeHelpers.walkClassBinary(node, sink, walkClassExpression)
        return
      case ts.SyntaxKind.CallExpression: {
        // Known builders carry class content in their arguments; any other
        // call resolves to a class string the lock cannot see (e.g. a cva
        // variant resolver — its literal base/variants were already scanned
        // at the declaration site; the resolved variant is dynamic).
        const callee = node.expression
        if (ts.isIdentifier(callee) && CLASS_BUILDERS.has(callee.text)) {
          node.arguments.forEach((argument) => walkClassExpression(argument, sink))
          return
        }
        const variantDefinition = cvaDefinitionFor(callee, moduleContext)
        if (variantDefinition) {
          if (!handledCalls.has(variantDefinition)) variantDefinition.arguments.forEach((argument) => walkClassExpression(argument, sink))
          return
        }
        if (ts.isPropertyAccessExpression(callee) && callee.name.text === 'join'
          && node.arguments.length <= 1
          && (!node.arguments[0] || ((ts.isStringLiteral(node.arguments[0]) || ts.isNoSubstitutionTemplateLiteral(node.arguments[0])) && /^\s*$/.test(node.arguments[0].text)))) {
          const joined = localInitializer(callee.expression)
          if (joined && ts.isArrayLiteralExpression(joined)) {
            joined.elements.forEach((element) => walkClassExpression(element, sink))
            return
          }
        }
        if (ts.isPropertyAccessExpression(callee) && callee.name.text === 'trim' && node.arguments.length === 0) {
          walkClassExpression(callee.expression, sink)
          return
        }
        const returns = pureFunctionReturns(node, moduleContext)
        if (returns) {
          returns.forEach((returned) => walkClassExpression(returned, sink))
          return
        }
        emitUnsupported(node, sink)
        return
      }
      case ts.SyntaxKind.ObjectLiteralExpression:
        node.properties.forEach((property) => {
          if (ts.isPropertyAssignment(property)) walkClassExpression(property.initializer, sink)
        })
        return
      case ts.SyntaxKind.ArrayLiteralExpression:
        node.elements.forEach((element) => walkClassExpression(element, sink))
        return
      case ts.SyntaxKind.ParenthesizedExpression:
      case ts.SyntaxKind.AsExpression:
      case ts.SyntaxKind.NonNullExpression:
      case ts.SyntaxKind.SatisfiesExpression:
      case ts.SyntaxKind.TypeAssertionExpression:
        walkClassExpression(node.expression, sink)
        return
      case ts.SyntaxKind.Identifier:
        if (node.text === 'undefined') return
        {
          const boundary = forwardedClassBoundary(node)
          if (boundary) {
            if (boundary.initializer && !scannedParameterDefaults.has(boundary.declaration)) {
              scannedParameterDefaults.add(boundary.declaration)
              walkClassExpression(boundary.initializer, sink)
            }
            sink.emit({
              ruleId: 'typography/extension-boundary', syntax: `${boundary.symbol}#${boundary.name}`,
              message: 'Unchanged caller-supplied className crosses a component extension boundary and remains blocking until exact central review.',
            })
            return
          }
        }
        {
          const lexical = duplicateBindings.has(node.text) || hasMultipleVariableDeclarations(sourceFile, node.text) ? null : findLexicalBinding(node, node.text)
          const binding = lexical?.initializer ?? (bindings.has(node.text) && !duplicateBindings.has(node.text) ? bindings.get(node.text) : null)
          if (binding) {
            if (ts.isBinaryExpression(binding) && binding.operatorToken.kind === ts.SyntaxKind.PlusToken && collectStaticTextForBinding(binding) === null) {
              emitUnsupported(node, sink)
              return
            }
            walkClassExpression(binding, sink)
            return
          }
        }
        {
          const resolved = resolveImportedExpression(node, moduleContext)
          if (resolved) {
            walkClassExpression(resolved, sink)
            return
          }
        }
        emitUnsupported(node, sink)
        return
      case ts.SyntaxKind.PropertyAccessExpression:
      case ts.SyntaxKind.ElementAccessExpression: {
        const candidates = localCandidates(node)
        if (candidates.length > 0) {
          candidates.forEach((candidate) => walkClassExpression(candidate, sink))
          return
        }
        const resolved = resolveImportedExpression(node, moduleContext)
        if (resolved) {
          walkClassExpression(resolved, sink)
          return
        }
        emitUnsupported(node, sink)
        return
      }
      case ts.SyntaxKind.FalseKeyword:
      case ts.SyntaxKind.TrueKeyword:
      case ts.SyntaxKind.NullKeyword:
      case ts.SyntaxKind.NumericLiteral:
        return
      default:
        emitUnsupported(node, sink)
    }
  }

  function emitUnsupported(node, sink) {
    sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
  }

  function localInitializer(node) {
    const expression = unwrapStatic(node)
    if (ts.isIdentifier(expression) && !duplicateBindings.has(expression.text) && !hasMultipleVariableDeclarations(sourceFile, expression.text)) {
      const lexical = findLexicalBinding(expression, expression.text)
      const initializer = lexical?.initializer ?? bindings.get(expression.text)
      if (initializer) return unwrapStatic(initializer)
    }
    return expression
  }

  function localCandidates(node, seen = new Set()) {
    const expression = unwrapStatic(node)
    if (!expression || seen.has(expression)) return []
    seen.add(expression)
    if (ts.isIdentifier(expression)) {
      const resolved = localInitializer(expression)
      return resolved !== expression ? localCandidates(resolved, seen) : []
    }
    if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
      const owners = localCandidates(expression.expression, new Set(seen))
      const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
        : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression)) ? expression.argumentExpression.text : null
      const results = []
      for (const owner of owners) {
        const object = unwrapStatic(owner)
        if (!ts.isObjectLiteralExpression(object)) continue
        if (key !== null) {
          const value = objectProperty(object, key)
          if (value) results.push(value)
        } else {
          for (const property of object.properties) {
            if (ts.isPropertyAssignment(property)) results.push(property.initializer)
            else if (ts.isShorthandPropertyAssignment(property)) results.push(property.name)
          }
        }
      }
      return results.flatMap((candidate) => {
        const nested = localCandidates(candidate, new Set(seen))
        return nested.length > 0 ? nested : [candidate]
      })
    }
    if (ts.isCallExpression(expression) && ts.isPropertyAccessExpression(expression.expression)
      && ts.isIdentifier(expression.expression.expression) && expression.expression.expression.text === 'Object'
      && ['freeze', 'seal', 'preventExtensions'].includes(expression.expression.name.text) && expression.arguments[0]) {
      return localCandidates(expression.arguments[0], seen)
    }
    return [expression]
  }

  function collectStaticTextForBinding(node) {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = collectStaticTextForBinding(node.left); const right = collectStaticTextForBinding(node.right)
      return left !== null && right !== null ? left + right : null
    }
    return null
  }

  function expandStaticClassStrings(node) {
    const expression = unwrapStatic(node)
    if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) return [expression.text]
    if (ts.isConditionalExpression(expression)) {
      const yes = expandStaticClassStrings(expression.whenTrue)
      const no = expandStaticClassStrings(expression.whenFalse)
      return yes && no ? [...yes, ...no] : null
    }
    if (ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = expandStaticClassStrings(expression.left)
      const right = expandStaticClassStrings(expression.right)
      if (!left || !right || left.length * right.length > 256) return null
      return left.flatMap((a) => right.map((b) => a + b))
    }
    return null
  }

  // Classifies whole class tokens from a string/template chunk, refining each
  // finding's position to the token's offset inside the literal.
  function classifyInto(text, node, sink) {
    const lead = node.getStart(sourceFile) + 1
    let searchFrom = 0
    for (const token of text.split(/\s+/).filter(Boolean)) {
      const tokenIndex = text.indexOf(token, searchFrom)
      searchFrom = tokenIndex + token.length
      classifyClassToken(token, {
        emit: (finding) => sink.emit(finding, positionAt(lead + tokenIndex)),
        isRegistered: sink.isRegistered,
        registeredTokens: sink.registeredTokens,
      })
    }
  }
  return { classReporterFor, classifyClassString, walkClassExpression }
}

function createClassNodeHelpers(sourceFile) {
  function walkClassBinary(node, sink, recurse) {
    const operator = node.operatorToken.kind
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken) {
      // The left operand is a boolean guard by clsx convention. Only the
      // resulting class value on the right is typography input.
      recurse(node.right, sink)
      return
    }
    if (operator === ts.SyntaxKind.BarBarToken || operator === ts.SyntaxKind.QuestionQuestionToken) {
      // Either operand can become the resulting class string.
      recurse(node.left, sink)
      recurse(node.right, sink)
      return
    }
    if (operator === ts.SyntaxKind.PlusToken) {
      const staticText = collectStaticText(node)
      if (staticText === null) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
        return
      }
      const partial = staticText.split(/\s+/).filter(Boolean).pop() ?? ''
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (partial.length > 0) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
      }
      return
    }
  }

  function collectStaticText(node) {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = collectStaticText(node.left)
      const right = collectStaticText(node.right)
      if (left !== null && right !== null) return left + right
      if (left !== null) return left
      if (right !== null) return right
    }
    return null
  }

  function walkDynamicTemplate(node, sink, recurse) {
    const staticTexts = [node.head.text, ...node.templateSpans.map((span) => span.literal.text)]
    staticTexts.forEach((text, index) => {
      const isFirst = index === 0
      const isLast = index === staticTexts.length - 1
      const tokens = text.split(/\s+/).filter(Boolean)
      const interior = [...tokens]
      // Tokens glued to an interpolation boundary are partial; the glue check
      // below handles them. Interior tokens classify normally.
      if (!isFirst && /^\S/.test(text) && interior.length > 0) interior.shift()
      if (!isLast && /\S$/.test(text) && interior.length > 0) interior.pop()
      for (const token of interior) classifyClassToken(token, sink)
    })
    node.templateSpans.forEach((span, index) => {
      const before = staticTexts[index]
      const after = staticTexts[index + 1]
      const partial = before.split(/\s+/).filter(Boolean).pop() ?? ''
      // Glue means the interpolation supplies a token fragment rather than
      // whole class names: it continues the last static token (`safe${x}`),
      // begins a token completed by static text (`${x}more`), or sits directly
      // against a neighbouring interpolation with no separating text.
      const gluedBefore = /\S$/.test(before) || (before === '' && index > 0)
      const gluedAfter = /^\S/.test(after) || (after === '' && index < node.templateSpans.length - 1)
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (/\[(?:[^\]]*)?$/.test(partial) || /\((?:[^)]*)?$/.test(partial)) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (gluedBefore || gluedAfter) {
        // The joined token is not statically knowable, so the whole template
        // fails closed. Fragment values are never walked as whole classes.
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
      } else {
        // Whitespace-separated on both sides: the interpolation supplies
        // whole class names and must be inspected — a proven forwarded value
        // yields its exact blocking extension boundary; anything unproven
        // fails closed inside the shared expression walker.
        recurse(span.expression, sink)
      }
    })
  }
  return { walkClassBinary, walkDynamicTemplate }
}

function createStyleWalker({ report, positionAt, sourceFile, registeredTokens, resolvedTokenValues, moduleContext }) {
  const emit = createStyleVerdictEmitter({ report })
  function walkStyleProperty(node) {
    const name = stylePropertyName(node)
    if (!name) return
    const at = positionAt(node.name.getStart(sourceFile))
    if (STYLE_SIZE_PROPERTY_NAMES.has(name)) {
      reportStyleSize(node.initializer, at)
      return
    }
    if (STYLE_FAMILY_PROPERTY_NAMES.has(name)) {
      reportStyleFamily(node.initializer, at)
      return
    }
    if (STYLE_SHORTHAND_PROPERTY_NAMES.has(name)) {
      reportStyleShorthand(node.initializer, at)
      return
    }
    if (STYLE_ROLE_PROPERTIES.has(name)) reportRoleValue(node.initializer, name, at)
  }

  function walkJsxValue(node, name) {
    if (!node.initializer) return
    const at = positionAt(node.name.getStart(sourceFile))
    const value = ts.isJsxExpression(node.initializer) ? node.initializer.expression : node.initializer
    if (!value) return
    if (name === 'fontSize') reportStyleSize(value, at)
    else reportStyleFamily(value, at)
  }

  function reportRoleValue(initializer, name, at) {
    const contract = STYLE_ROLE_PROPERTIES.get(name)
    let value = collapseWhitespace(initializer.getText(sourceFile))
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer) || ts.isNumericLiteral(initializer)) value = initializer.text
    if (['inherit', 'initial', 'unset', 'revert', 'revert-layer', 'normal'].includes(value)) return
    const variable = VAR_REFERENCE_PATTERN.exec(value)
    if (variable && registeredTokens.has(variable[1])) return
    report({
      ruleId: variable ? 'typography/unregistered-typography-token' : contract.ruleId,
      syntax: `${name}: ${value}`,
      message: variable ? `${name} references an unregistered typography token.` : `${name} is an arbitrary D9 typography value; use a registered role token.`,
      ...at,
    })
  }

  function reportStyleSize(initializer, at) {
    const resolved = resolveImportedExpression(initializer, moduleContext)
    if (resolved && resolved !== initializer) return reportStyleSize(resolved, at)
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer)) {
      emit.emitSizeVerdict(evaluateFontSizeValue(initializer.text, registeredTokens, resolvedTokenValues), 'fontSize', at)
      return
    }
    if (ts.isNumericLiteral(initializer)) {
      const value = Number.parseFloat(initializer.text)
      emit.emitSizeVerdict(
        { status: value < FLOOR_PX ? 'below-floor' : 'ok', printed: initializer.text },
        'fontSize',
        at,
      )
      return
    }
    if (ts.isTemplateExpression(initializer)) {
      const skeleton = templateSkeleton(initializer)
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `fontSize: ${skeleton}`,
        message: `fontSize: ${skeleton} is a dynamic expression the static lock cannot bound against the ${FLOOR_PX}px floor (§D2). Bind it to a registered size token or prove it in the browser harness.`,
        ...at,
      })
      return
    }
    report({
      ruleId: 'typography/unsupported-font-size',
      syntax: `fontSize: ${collapseWhitespace(initializer.getText(sourceFile))}`,
      message: `This fontSize value is not a literal the static lock can evaluate against the ${FLOOR_PX}px floor (§D2).`,
      ...at,
    })
  }

  function reportStyleFamily(initializer, at) {
    const resolved = resolveImportedExpression(initializer, moduleContext)
    if (resolved && resolved !== initializer) return reportStyleFamily(resolved, at)
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer)) {
      emit.emitFamilyVerdict(evaluateFontFamilyValue(initializer.text, registeredTokens), 'fontFamily', at)
      return
    }
    report({
      ruleId: 'typography/font-family-literal',
      syntax: `fontFamily: ${collapseWhitespace(initializer.getText(sourceFile))}`,
      message: 'This fontFamily value is not statically evaluable; §D9 requires families to resolve to the token set.',
      ...at,
    })
  }

  function reportStyleShorthand(initializer, at) {
    if (!ts.isStringLiteral(initializer) && !ts.isNoSubstitutionTemplateLiteral(initializer)) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.getText(sourceFile))}`,
        message: `This font shorthand is not a literal the static lock can evaluate against the ${FLOOR_PX}px floor (§D2).`,
        ...at,
      })
      return
    }
    const parsed = parseFontShorthand(initializer.text)
    if (parsed.systemKeyword) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.text)}`,
        message: 'System font keywords carry an implementation-defined size the static lock cannot bound against the 12px floor.',
        ...at,
      })
      return
    }
    if (parsed.unsupported) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.text)}`,
        message: 'This font shorthand could not be split into size and family by the static lock.',
        ...at,
      })
      return
    }
    if (parsed.size) {
      const verdict = evaluateFontSizeValue(parsed.size, registeredTokens)
      if (verdict.status !== 'ok') emit.emitSizeVerdict({ ...verdict, printed: `${verdict.printed} …` }, 'font', at)
    }
    if (parsed.family && parsed.family.trim().length > 0) {
      const verdict = evaluateFontFamilyValue(parsed.family, registeredTokens)
      if (verdict.status !== 'ok') emit.emitFamilyVerdict({ ...verdict, printed: `… ${verdict.printed}` }, 'font', at)
    }
  }

  function templateSkeleton(template) {
    let skeleton = template.head.text
    for (const span of template.templateSpans) {
      skeleton += '${…}' + span.literal.text
    }
    return collapseWhitespace(skeleton)
  }
  return { walkStyleProperty, walkJsxValue }
}

function createStyleVerdictEmitter({ report }) {
  function emitSizeVerdict({ status, printed }, label, at) {
    if (status === 'ok') return
    if (status === 'below-floor') {
      report({
        ruleId: 'typography/font-size-below-floor',
        syntax: `${label}: ${printed}`,
        message: `${label}: ${printed} can compute below the ${FLOOR_PX}px floor under the §D1 root range (${ROOT_MIN_PX}–${ROOT_MAX_PX}px); §D2 forbids any UI text below ${FLOOR_PX}px.`,
        ...at,
      })
      return
    }
    if (status === 'unregistered') {
      report({
        ruleId: 'typography/unregistered-font-size-token',
        syntax: `${label}: ${printed}`,
        message: `${label}: ${printed} references a custom property that is not a registered typography token (§D9).`,
        ...at,
      })
      return
    }
    report({
      ruleId: 'typography/unsupported-font-size',
      syntax: `${label}: ${printed}`,
      message: `${label}: ${printed} cannot be bounded against the ${FLOOR_PX}px floor statically (relative or dynamic units); §D2 needs a registered token, a provable expression, or browser-harness proof.`,
      ...at,
    })
  }

  function emitFamilyVerdict({ status, printed }, label, at) {
    if (status === 'ok') return
    report({
      ruleId: status === 'unregistered' ? 'typography/unregistered-font-size-token' : 'typography/font-family-literal',
      syntax: `${label}: ${printed}`,
      message: status === 'unregistered'
        ? `${label}: ${printed} references an unregistered custom property (§D9 family token boundary).`
        : `${label}: ${printed} is a literal family stack; §D9 requires the Outfit/Inter/JetBrains Mono tokens.`,
      ...at,
    })
  }
  return { emitSizeVerdict, emitFamilyVerdict }
}



function stylePropertyName(node) {
  const name = node.name
  if (ts.isIdentifier(name)) return name.text
  if (ts.isStringLiteral(name)) return name.text
  if (ts.isComputedPropertyName(name) && ts.isBinaryExpression(name.expression) && name.expression.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = name.expression.left; const right = name.expression.right
    if (ts.isStringLiteral(left) && ts.isStringLiteral(right)) return left.text + right.text
  }
  return null
}

// ---------------------------------------------------------------------------
// CSS scan (PostCSS).
// ---------------------------------------------------------------------------

function scanCss({ path, source, registeredTokens, resolvedTokenValues = new Map() }) {
  let root
  try {
    root = postcss.parse(source, { from: path })
  } catch (error) {
    return [{
      ruleId: 'typography/parse-failure',
      path,
      syntax: 'CSS parse failure',
      message: `PostCSS could not parse ${path}: ${error.message}. The lock fails closed on unparseable governed files.`,
    }]
  }

  const findings = []
  root.walkDecls((declaration) => {
    // Custom-property assignments are token-graph declarations (D3 lane),
    // not typography usages.
    if (declaration.prop.startsWith('--')) {
      if (declaration.prop.startsWith('--type-') && registeredTokens.has(declaration.prop)) {
        const verdict = evaluateFontSizeValue(declaration.value, registeredTokens, resolvedTokenValues)
        if (verdict.status !== 'ok') findings.push({
          ruleId: 'typography/token-shadow', path, syntax: `${declaration.prop}: ${verdict.printed}`,
          message: `${declaration.prop} shadows a registered typography token with an unsafe value.`,
          line: declaration.source.start.line, column: declaration.source.start.column,
        })
      }
      return
    }
    const parent = declaration.parent
    if (parent && parent.type === 'atrule' && parent.name === 'font-face') return
    const at = { line: declaration.source.start.line, column: declaration.source.start.column }
    const prop = declaration.prop.toLowerCase()
    if (prop === 'font-size') {
      emitCssVerdict(evaluateFontSizeValue(declaration.value, registeredTokens, resolvedTokenValues), 'font-size', at, path, findings, 'size')
      return
    }
    if (prop === 'font-family') {
      emitCssVerdict(evaluateFontFamilyValue(declaration.value, registeredTokens), 'font-family', at, path, findings, 'family')
      return
    }
    if (prop === 'font') {
      emitCssShorthand(declaration.value, at, path, findings, registeredTokens)
      return
    }
    const role = STYLE_ROLE_PROPERTIES.get(prop)
    if (role) emitCssRole(declaration.value, prop, role, at, path, findings, registeredTokens)
  })
  root.walkAtRules('apply', (rule) => {
    const at = { line: rule.source.start.line, column: rule.source.start.column }
    const sink = { registeredTokens, isRegistered: (name) => registeredTokens.has(name), emit: (finding) => findings.push({ path, ...finding, ...at }) }
    for (const token of rule.params.split(/\s+/).filter(Boolean)) classifyClassToken(token, sink)
  })
  return findings
}

function emitCssRole(value, label, contract, at, path, findings, registeredTokens) {
  const printed = collapseWhitespace(value)
  if (['inherit', 'initial', 'unset', 'revert', 'revert-layer', 'normal'].includes(printed)) return
  const variable = VAR_REFERENCE_PATTERN.exec(printed)
  if (variable && registeredTokens.has(variable[1])) return
  findings.push({
    ruleId: variable ? 'typography/unregistered-typography-token' : contract.ruleId,
    path,
    syntax: `${label}: ${printed}`,
    message: variable ? `${label} references an unregistered typography token.` : `${label} is an arbitrary D9 typography value; use a registered role token.`,
    ...at,
  })
}

function sizeVerdictFields(status, label, printed) {
  if (status === 'below-floor') {
    return {
      ruleId: 'typography/font-size-below-floor',
      message: `${label}: ${printed} can compute below the ${FLOOR_PX}px floor under the §D1 root range (${ROOT_MIN_PX}–${ROOT_MAX_PX}px); §D2 forbids any UI text below ${FLOOR_PX}px.`,
    }
  }
  if (status === 'unregistered') {
    return {
      ruleId: 'typography/unregistered-font-size-token',
      message: `${label}: ${printed} references a custom property that is not a registered typography token (§D9).`,
    }
  }
  return {
    ruleId: 'typography/unsupported-font-size',
    message: `${label}: ${printed} cannot be bounded against the ${FLOOR_PX}px floor statically (relative or dynamic units); §D2 needs a registered token, a provable expression, or browser-harness proof.`,
  }
}

function familyVerdictFields(status, label, printed) {
  if (status === 'unregistered') {
    return {
      ruleId: 'typography/unregistered-font-size-token',
      message: `${label}: ${printed} references an unregistered custom property (§D9 family token boundary).`,
    }
  }
  return {
    ruleId: 'typography/font-family-literal',
    message: `${label}: ${printed} is a literal family stack; §D9 requires the Outfit/Inter/JetBrains Mono tokens.`,
  }
}

function emitCssVerdict({ status, printed }, label, at, path, findings, kind) {
  if (status === 'ok') return
  const fields = kind === 'size' ? sizeVerdictFields(status, label, printed) : familyVerdictFields(status, label, printed)
  findings.push({
    ...fields,
    path,
    syntax: `${label}: ${printed}`,
    line: at.line,
    column: at.column,
  })
}

function emitCssShorthand(value, at, path, findings, registeredTokens) {
  const parsed = parseFontShorthand(value)
  if (parsed.systemKeyword || parsed.unsupported) {
    findings.push({
      ruleId: 'typography/unsupported-font-size',
      path,
      syntax: `font: ${collapseWhitespace(value)}`,
      message: parsed.systemKeyword
        ? 'System font keywords carry an implementation-defined size the static lock cannot bound against the 12px floor.'
        : 'This font shorthand could not be split into size and family by the static lock.',
      line: at.line,
      column: at.column,
    })
    return
  }
  if (parsed.size) {
    const verdict = evaluateFontSizeValue(parsed.size, registeredTokens)
    if (verdict.status !== 'ok') {
      emitCssVerdict({ ...verdict, printed: `${verdict.printed} …` }, 'font', at, path, findings, 'size')
    }
  }
  if (parsed.family && parsed.family.trim().length > 0) {
    const verdict = evaluateFontFamilyValue(parsed.family, registeredTokens)
    if (verdict.status !== 'ok') {
      emitCssVerdict({ ...verdict, printed: `… ${verdict.printed}` }, 'font', at, path, findings, 'family')
    }
  }
}

function governedModulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/')
    ? `src/${specifier.slice(2)}`
    : specifier.startsWith('.') ? path.posix.normalize(path.posix.join(path.posix.dirname(from), specifier)) : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}.mjs`, `${base}.cjs`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

// True when a plain same-named variable declaration (const/let/var, any
// identifier binding) sits in a block lexically between `identifier` and its
// enclosing function — such a declaration owns the sink binding, so a
// forwarded-parameter or body-destructured provenance claim cannot hold.
function plainDeclarationShadows(identifier, fn) {
  let scope = identifier.parent
  while (scope && scope !== fn) {
    if (ts.isBlock(scope)) {
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement)) continue
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === identifier.text) return true
        }
      }
    }
    scope = scope.parent
  }
  return false
}

function findLexicalBinding(identifier, name) {
  let scope = identifier.parent
  while (scope) {
    if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
      const matches = []
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === name) matches.push(declaration)
        }
      }
      if (matches.length > 1) return null
      if (matches.length === 1) return { declaration: matches[0], initializer: matches[0].initializer ?? null }
    }
    if (ts.isFunctionLike(scope)) {
      for (const parameter of scope.parameters) {
        if (ts.isIdentifier(parameter.name) && parameter.name.text === name) return { declaration: parameter, initializer: parameter.initializer ?? null }
      }
    }
    scope = scope.parent
  }
  return null
}

function hasMultipleVariableDeclarations(sourceFile, name) {
  let count = 0
  function visit(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name) count += 1
    if (count < 2) node.forEachChild(visit)
  }
  visit(sourceFile)
  return count > 1
}

function moduleRecord(context, modulePath) {
  if (!context?.modules) return null
  if (context.moduleCache.has(modulePath)) return context.moduleCache.get(modulePath)
  const source = context.modules[modulePath]
  if (typeof source !== 'string') return null
  const sourceFile = ts.createSourceFile(modulePath, source, ts.ScriptTarget.ES2022, true, scriptKindFor(modulePath))
  const record = { sourceFile, exports: new Map(), imports: new Map(), reexports: new Map(), valid: !(sourceFile.parseDiagnostics ?? []).some((item) => item.category === ts.DiagnosticCategory.Error) }
  context.moduleCache.set(modulePath, record)
  for (const statement of sourceFile.statements) {
    const exported = statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)
    if (exported && ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) record.exports.set(declaration.name.text, declaration)
    } else if (exported && ts.isFunctionDeclaration(statement) && statement.name) {
      record.exports.set(statement.name.text, statement)
    } else if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const named = statement.importClause?.namedBindings
      if (named && ts.isNamedImports(named)) for (const element of named.elements) record.imports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    } else if (ts.isExportDeclaration(statement) && statement.exportClause && ts.isNamedExports(statement.exportClause) && statement.moduleSpecifier && ts.isStringLiteral(statement.moduleSpecifier)) {
      for (const element of statement.exportClause.elements) record.reexports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    }
  }
  return record
}

function resolveModuleExport(context, fromPath, specifier, exported, seen) {
  const target = governedModulePath(fromPath, specifier, context?.modules)
  if (!target) return null
  const marker = `${target}#${exported}`
  if (seen.has(marker)) return null
  const nextSeen = new Set(seen).add(marker)
  const record = moduleRecord(context, target)
  if (!record?.valid) return null
  if (record.exports.has(exported)) return record.exports.get(exported)
  const forwarded = record.reexports.get(exported) ?? record.imports.get(exported)
  return forwarded ? resolveModuleExport(context, target, forwarded.specifier, forwarded.imported, nextSeen) : null
}

function importedDeclaration(identifier, context) {
  const sourceFile = identifier.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const named = statement.importClause?.namedBindings
    if (!named || !ts.isNamedImports(named)) continue
    for (const element of named.elements) if (element.name.text === identifier.text) {
      return resolveModuleExport(context, sourceFile.fileName, statement.moduleSpecifier.text, element.propertyName?.text ?? element.name.text, new Set())
    }
  }
  return null
}

function objectProperty(object, key) {
  for (const property of object.properties) {
    if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) continue
    const name = property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) || ts.isNumericLiteral(property.name)) ? property.name.text : null
    if (name === key) return ts.isPropertyAssignment(property) ? property.initializer : property.name
  }
  return null
}

function unwrapStatic(node) {
  let current = node
  while (current && (ts.isParenthesizedExpression(current) || ts.isAsExpression(current) || ts.isSatisfiesExpression(current) || ts.isNonNullExpression(current) || current.kind === ts.SyntaxKind.TypeAssertionExpression)) current = current.expression
  return current
}

function resolveImportedExpression(node, context, seen = new Set()) {
  const expression = unwrapStatic(node)
  if (!expression || !context?.modules) return null
  if (ts.isIdentifier(expression)) {
    const declaration = importedDeclaration(expression, context)
    if (!declaration || seen.has(declaration)) return null
    seen.add(declaration)
    if (!ts.isVariableDeclaration(declaration) || !declaration.initializer) return null
    const initializer = unwrapStatic(declaration.initializer)
    return resolveImportedExpression(initializer, context, seen) ?? initializer
  }
  if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
    const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
      : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression)) ? expression.argumentExpression.text : null
    if (key === null) return null
    const owner = resolveImportedExpression(expression.expression, context, seen)
    const unwrappedOwner = unwrapStatic(owner)
    return unwrappedOwner && ts.isObjectLiteralExpression(unwrappedOwner) ? objectProperty(unwrappedOwner, key) : null
  }
  if (ts.isCallExpression(expression) && ts.isPropertyAccessExpression(expression.expression) && ts.isIdentifier(expression.expression.expression)
    && expression.expression.expression.text === 'Object' && ['freeze', 'seal', 'preventExtensions'].includes(expression.expression.name.text)) {
    return expression.arguments[0] ? resolveImportedExpression(expression.arguments[0], context, seen) ?? unwrapStatic(expression.arguments[0]) : null
  }
  return null
}

function functionDeclarationFor(call, context) {
  const callee = unwrapStatic(call.expression)
  if (!callee || !ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)?.initializer
  const localFunction = unwrapStatic(local)
  if (localFunction && (ts.isArrowFunction(localFunction) || ts.isFunctionExpression(localFunction))) return localFunction
  const imported = importedDeclaration(callee, context)
  return imported && (ts.isFunctionDeclaration(imported) || ts.isArrowFunction(imported) || ts.isFunctionExpression(imported)) ? imported : null
}

function authenticCvaImport(expression) {
  const callee = unwrapStatic(expression)
  if (!callee || !ts.isIdentifier(callee)) return false
  const sourceFile = callee.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)
      || statement.moduleSpecifier.text !== 'class-variance-authority') continue
    const named = statement.importClause?.namedBindings
    if (!named || !ts.isNamedImports(named)) continue
    if (named.elements.some((element) => element.name.text === callee.text && (element.propertyName?.text ?? element.name.text) === 'cva')) return true
  }
  return false
}

function cvaDefinitionFor(expression, context) {
  const callee = unwrapStatic(expression)
  if (!callee || !ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)
  const declaration = local?.declaration ?? importedDeclaration(callee, context)
  if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer) return null
  const initializer = unwrapStatic(declaration.initializer)
  return initializer && ts.isCallExpression(initializer) && authenticCvaImport(initializer.expression) ? initializer : null
}

function pureFunctionReturns(call, context) {
  const declaration = functionDeclarationFor(call, context)
  if (!declaration?.body) return null
  if (!ts.isBlock(declaration.body)) return isPureStaticExpression(declaration.body) ? [declaration.body] : null
  if (declaration.body.statements.length !== 1) return null
  const statement = declaration.body.statements[0]
  if (!ts.isReturnStatement(statement) || !statement.expression || !isPureStaticExpression(statement.expression)) return null
  return [statement.expression]
}

function isPureStaticExpression(node) {
  let pure = true
  function visit(current) {
    if (!pure) return
    if (ts.isCallExpression(current) || ts.isNewExpression(current) || ts.isAwaitExpression(current) || ts.isYieldExpression(current) || ts.isTaggedTemplateExpression(current)
      || (ts.isBinaryExpression(current) && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment)) {
      pure = false
      return
    }
    current.forEachChild(visit)
  }
  visit(node)
  return pure
}

// ---------------------------------------------------------------------------
// Entry point (design-system/enforcement/contract.json scanner API).
// ---------------------------------------------------------------------------

export function scan({ path, source, policy, modules }) {
  if (typeof path !== 'string' || typeof source !== 'string') {
    throw new Error('typography scanner requires string path and source')
  }
  const tokenCssNames = Array.isArray(policy?.tokenCssNames) ? policy.tokenCssNames : []
  const registeredTokens = new Set(tokenCssNames)
  const derivedResolvedTokens = Object.fromEntries(Object.entries(policy?.resolvedTokens ?? {}).map(([id, value]) => [`--${id.replace(/([a-z0-9])([A-Z])/g, '$1-$2').replaceAll('.', '-').toLowerCase()}`, value]))
  const resolvedTokenValues = new Map(Object.entries(policy?.resolvedCssTokens ?? derivedResolvedTokens))
  const context = { path, source, registeredTokens, resolvedTokenValues, modules, moduleCache: new Map() }
  const lowerPath = path.toLowerCase()
  if (lowerPath.endsWith('.css')) return scanCss(context)
  if (lowerPath.endsWith('.svg')) return scanSvg(context)
  const extension = lowerPath.slice(lowerPath.lastIndexOf('.'))
  if (TS_EXTENSIONS.includes(extension)) return scanTypeScript(context)
  throw new Error(`typography scanner does not handle extension: ${path}`)
}
