#!/usr/bin/env node
// typography/class-value-rules.mjs
//
// The dependency-free leaf layer: CSS length/font-size math, Tailwind
// class-token classification, and the constant tables both the TS/TSX and
// CSS scans read. Nothing here imports from any sibling typography/*
// module — that acyclic-by-construction property is what lets both
// ts-scan.mjs and css-scan.mjs depend on this file without a cycle.
// Every export below is used by at least one sibling module; see
// ../typography.mjs for the public entry point.

'use strict'

import ts from 'typescript'

export const TS_EXTENSIONS = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs']

export const FLOOR_PX = 12

export const ROOT_MIN_PX = 12

export const ROOT_MAX_PX = 20

export const REFLOW_MIN_VIEWPORT_PX = 320

// Tailwind 4.3.3 default text scale in rem, transcribed from
// node_modules/tailwindcss/theme.css. Only xs/sm sit below 1rem and can fall
// under the 12px floor at the 12px minimum root.
export const TEXT_SCALE_REM = {
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
export const NON_SIZE_TEXT_UTILITIES = new Set([
  'center', 'left', 'right', 'justify', 'start', 'end',
  'wrap', 'nowrap', 'balance', 'pretty', 'clip', 'ellipsis', 'truncate',
  'underline', 'overline', 'line-through', 'no-underline',
  'uppercase', 'lowercase', 'capitalize', 'normal-case',
  'opaque', 'inherit', 'current', 'transparent',
])

// font-weight utilities (D9 governs weights too, but that is outside this
// lane's mandate — reported to the lead, not silently dropped).
export const FONT_WEIGHT_UTILITIES = new Set([
  'thin', 'extralight', 'light', 'normal', 'medium', 'semibold',
  'bold', 'extrabold', 'black',
])

// font-sans / font-serif resolve to the Tailwind default stacks in BOTH live
// builds of this repo (src/styles/globals.css and src/styles/library.css map
// only --font-headline/body/mono), so they are off-token families per §D9.
// font-mono / font-headline / font-body are remapped onto tokens in both
// builds and are not flagged per-usage; the theme-level literals in
// globals.css are theme hygiene for the token lane, not usage violations.
export const OFF_TOKEN_FAMILY_UTILITIES = new Set(['sans', 'serif'])

export const REMAPPED_FAMILY_UTILITIES = new Set(['mono', 'headline', 'body'])

export const CLASS_BUILDERS = new Set(['cn', 'clsx', 'cva', 'twMerge', 'classnames', 'classNames'])

// A parameter/property name carries a whole CSS class value, not just the
// literal `className`/`class` (react-day-picker's `Chevron: ({ className:
// chevronClassName })`, sheet.tsx's `widthClass`, smart-select's
// `triggerClassName`, ...). Matched structurally (camelCase `...Class`/
// `...ClassName` suffix) so the extension-boundary/forwarding proofs below
// cover every such prop without a per-component allowlist. This only ever
// WIDENS which identifiers are eligible for the existing unchanged-forwarding
// proof (still gated by the same reassignment/shadow/ownership checks); a
// false match degrades at worst to an extra blocking extension-boundary
// finding, never a silent pass.
export function isClassLikeParameterName(name) {
  return name === 'className' || name === 'class'
    || /(?:^|[a-z0-9])(?:ClassName|Class)$/.test(name)
}

export const STYLE_SIZE_PROPERTY_NAMES = new Set(['fontSize', 'font-size'])

export const STYLE_FAMILY_PROPERTY_NAMES = new Set(['fontFamily', 'font-family'])

export const STYLE_SHORTHAND_PROPERTY_NAMES = new Set(['font'])

export const STYLE_ROLE_PROPERTIES = new Map([
  ['fontWeight', { css: 'font-weight', ruleId: 'typography/font-weight-arbitrary' }],
  ['font-weight', { css: 'font-weight', ruleId: 'typography/font-weight-arbitrary' }],
  ['lineHeight', { css: 'line-height', ruleId: 'typography/line-height-arbitrary' }],
  ['line-height', { css: 'line-height', ruleId: 'typography/line-height-arbitrary' }],
  ['letterSpacing', { css: 'letter-spacing', ruleId: 'typography/letter-spacing-arbitrary' }],
  ['letter-spacing', { css: 'letter-spacing', ruleId: 'typography/letter-spacing-arbitrary' }],
])

export const ROLE_UTILITY_TOKENS = new Map([
  ['font', { variables: new Map([...FONT_WEIGHT_UTILITIES].map((name) => [name, `--font-weight-${name}`])), ruleId: 'typography/font-weight-arbitrary' }],
  ['leading', { variables: new Map([['normal', '--leading-normal']]), ruleId: 'typography/line-height-arbitrary' }],
  ['tracking', { variables: new Map([['normal', '--tracking-normal']]), ruleId: 'typography/letter-spacing-arbitrary' }],
])

// CSS-wide value keywords: value plumbing, not a size/family choice of their
// own — each one defers entirely to a value computed elsewhere (the parent's
// cascaded value, the property's own initial value, …), so it can never pin
// an off-scale magic number the way a literal (`13px`, `'Arial'`) can. This
// is the typography-lock analogue of css-colors.mjs's COLOR_KEYWORD_EXEMPTS,
// which treats the identical keyword set (plus currentcolor/transparent) the
// same way for color. Used both for raw CSS/style values (evaluateFontSizeValue,
// evaluateFontFamilyValue, role-utility CSS values) and, via
// evaluateArbitrarySizeValue, for the Tailwind `text-[length:inherit]` arbitrary
// form — see the comment at that call site for why the scanner cannot (and,
// following the color-lock precedent, does not try to) verify that a governed
// parent exists at every call site.
export const CSS_WIDE_KEYWORDS = new Set(['inherit', 'initial', 'unset', 'revert', 'revert-layer'])

export const CSS_GENERIC_FAMILY_KEYWORDS = new Set([
  'serif', 'sans-serif', 'monospace', 'cursive', 'fantasy', 'system-ui',
  'ui-serif', 'ui-sans-serif', 'ui-monospace', 'ui-rounded',
])

export const CSS_SYSTEM_FONT_KEYWORDS = new Set([
  'caption', 'icon', 'menu', 'message-box', 'small-caption', 'status-bar',
])

export const ABSOLUTE_SIZE_KEYWORDS_PX = new Map([
  ['xx-small', 9], ['x-small', 10], ['small', 13], ['medium', 16],
  ['large', 18], ['x-large', 24], ['xx-large', 32], ['xxx-large', 48],
])

export const FONT_SHORTHAND_PREFIX_KEYWORDS = new Set([
  'normal', 'italic', 'oblique', 'small-caps', 'bold', 'bolder', 'lighter',
  '100', '200', '300', '400', '500', '600', '700', '800', '900',
])

export const VAR_REFERENCE_PATTERN = /^var\(\s*(--[a-zA-Z0-9-]+)\s*(?:,[^)]*)?\)$/

export const BARE_CUSTOM_PROPERTY_PATTERN = /^--[a-zA-Z0-9-]+$/

// Runtime, JS-written custom properties that are not, and will never be,
// registered design tokens — §D1's root font-size control. The write site
// (ProfileSection.tsx's accessibility font-size slider,
// document.documentElement.style.setProperty('--user-font-size', …)) is a
// founder-approved permanent category, mirrored here on the read side: it
// contributes an unbounded interval (its numeric value is user-controlled,
// not statically known) but must NOT be treated as an unregistered *token* —
// it was never meant to be one. See evaluateExpression's clamp/min/max
// handling and the RUNTIME_FONT_SIZE_PREFERENCE verdict status below, which
// mirrors spacing.mjs::analyzeEnvironmentSpacing's safe-area env() carve-out:
// still a blocking, centrally-reviewed extension-boundary finding, never a
// silent pass.
export const RUNTIME_FONT_SIZE_PREFERENCE_VARS = new Set(['--user-font-size'])

export const DYNAMIC_UTILITY_MESSAGE = 'This dynamic class expression can resolve to a governed text-size or family utility the static lock cannot see. Make the utility static or route it through cn()/cva() with literal strings (fail-closed per the enforcement contract).'

// ---------------------------------------------------------------------------
// CSS math expression parsing + conservative interval evaluation.
//
// Interval = { lo: number | null, hi: number | null }; null means "no sound
// bound". The floor verdict needs only a sound lower bound:
//   violation     iff lo !== null && lo < FLOOR_PX
//   proven safe   iff lo !== null && lo >= FLOOR_PX
//   unsupported   otherwise (fail-closed, explicit finding).
// ---------------------------------------------------------------------------

export const UNIT_TO_PX = {
  px: 1,
  pt: 4 / 3,
  pc: 16,
  in: 96,
  cm: 96 / 2.54,
  mm: 96 / 25.4,
  q: 96 / 101.6,
}

export const ROOT_RELATIVE_UNITS = new Set(['rem'])

// vh/vmin/vmax have no constitutional bound (no minimum height contract); em,
// %, ch, ex and container units are relative to unknown context.
export const UNPROVABLE_UNITS = new Set([
  'em', '%', 'ch', 'ex', 'ric', 'lh', 'rlh', 'cap', 'ic',
  'cqi', 'cqb', 'cqh', 'cqw', 'cqvmin', 'cqvmax',
  'vi', 'vb', 'svh', 'svw', 'lvh', 'lvw', 'dvh', 'dvw',
  'vh', 'vmin', 'vmax',
])

export function tokenizeExpression(text) {
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

export function parseExpressionTokens(tokens) {
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

export function printExpression(node) {
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

export function dimensionInterval(value, unit) {
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

export function addIntervals(a, b) {
  return {
    lo: a.lo === null || b.lo === null ? null : a.lo + b.lo,
    hi: a.hi === null || b.hi === null ? null : a.hi + b.hi,
  }
}

export function subtractIntervals(a, b) {
  return {
    lo: a.lo === null || b.hi === null ? null : a.lo - b.hi,
    hi: a.hi === null || b.lo === null ? null : a.hi - b.lo,
  }
}

export function multiplyIntervals(a, b) {
  const bounds = [a.lo, a.hi, b.lo, b.hi]
  if (bounds.some((bound) => bound === null || !Number.isFinite(bound))) return { lo: null, hi: null }
  const products = [a.lo * b.lo, a.lo * b.hi, a.hi * b.lo, a.hi * b.hi]
  return { lo: Math.min(...products), hi: Math.max(...products) }
}

export function divideIntervals(a, b) {
  if (a.lo === null || a.hi === null || b.lo === null || b.hi === null) return { lo: null, hi: null }
  if (b.lo <= 0 && b.hi >= 0) return { lo: null, hi: null } // divisor crosses zero
  const quotients = [a.lo / b.lo, a.lo / b.hi, a.hi / b.lo, a.hi / b.hi]
  return { lo: Math.min(...quotients), hi: Math.max(...quotients) }
}

export function maxIntervals(intervals) {
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

export function minIntervals(intervals) {
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

export function clampIntervals(args) {
  // clamp(min, value, max) === max(min, min(value, max)).
  return maxIntervals([args[0], minIntervals([args[1], args[2]])])
}

// Evaluates a parsed expression against the floor. var() references cannot be
// resolved to numbers by CSS name from the policy, so they contribute an
// unknown interval; registration is tracked separately.
export function evaluateExpression(node, registeredTokens, resolvedTokenValues = new Map()) {
  // usedRuntimePreference/hasRawDimensionLiteral back the
  // RUNTIME_FONT_SIZE_PREFERENCE_VARS carve-out below: a runtime preference
  // var (currently only --user-font-size) never counts as an unregistered
  // token, but the carve-out only fires when every OTHER leaf in the
  // expression is itself a registered token — a raw dimension literal
  // (`12px` typed directly in source, not reached by resolving a registered
  // var's value) still forces the ordinary unregistered/unsupported path, so
  // `clamp(12px, var(--user-font-size), 20px)` stays a finding exactly as it
  // did before this carve-out existed.
  const state = { unregistered: [], usedRuntimePreference: false, hasRawDimensionLiteral: false }

  function evaluate(current, viaResolvedVar = false) {
    switch (current.type) {
      case 'dimension':
        if (!viaResolvedVar) state.hasRawDimensionLiteral = true
        return dimensionInterval(current.value, current.unit)
      case 'varRef':
        if (RUNTIME_FONT_SIZE_PREFERENCE_VARS.has(current.name)) {
          state.usedRuntimePreference = true
          return { lo: null, hi: null }
        }
        if (!registeredTokens.has(current.name)) state.unregistered.push(current.name)
        if (resolvedTokenValues.has(current.name)) {
          const resolved = parseExpressionTokens(tokenizeExpression(String(resolvedTokenValues.get(current.name))))
          return evaluate(resolved, true)
        }
        return { lo: null, hi: null }
      case 'negation':
        return multiplyIntervals({ lo: -1, hi: -1 }, evaluate(current.operand, viaResolvedVar))
      case 'binary': {
        const left = evaluate(current.left, viaResolvedVar)
        const right = evaluate(current.right, viaResolvedVar)
        if (current.operator === '+') return addIntervals(left, right)
        if (current.operator === '-') return subtractIntervals(left, right)
        if (current.operator === '*') return multiplyIntervals(left, right)
        return divideIntervals(left, right)
      }
      case 'function': {
        if (current.name === 'calc') {
          if (current.args.length !== 1) throw new Error('calc() takes one argument')
          return evaluate(current.args[0], viaResolvedVar)
        }
        if (current.name === 'min') return minIntervals(current.args.map((arg) => evaluate(arg, viaResolvedVar)))
        if (current.name === 'max') return maxIntervals(current.args.map((arg) => evaluate(arg, viaResolvedVar)))
        if (current.name === 'clamp') {
          if (current.args.length !== 3) throw new Error('clamp() takes three arguments')
          return clampIntervals(current.args.map((arg) => evaluate(arg, viaResolvedVar)))
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
    if (interval.lo !== null && interval.lo < FLOOR_PX) return { status: 'below-floor', lowerBound: interval.lo }
    if (state.usedRuntimePreference) {
      // Only every OTHER leaf being a registered token earns the carve-out;
      // a raw literal bound (clamp(12px, var(--user-font-size), 20px)) is
      // not "fully tokenized" and stays blocking debt, same as before.
      if (state.hasRawDimensionLiteral) return { status: 'unregistered' }
      return { status: 'runtime-preference-boundary', lowerBound: interval.lo ?? undefined }
    }
    if (interval.lo === null) {
      // A bare registered var is trusted to the token layer plus the browser
      // harness; a registered var inside arithmetic is not (calc(var(--t) - 4px)
      // can fall under the floor and cannot be bounded from policy).
      const isBareRegisteredVar = node.type === 'varRef' && registeredTokens.has(node.name)
      if (isBareRegisteredVar) return { status: 'ok' }
      return { status: 'unsupported' }
    }
    return { status: 'ok', lowerBound: interval.lo }
  } catch {
    return { status: 'unsupported' }
  }
}

// ---------------------------------------------------------------------------
// Value-level entry points shared by the TS and CSS scans.
// ---------------------------------------------------------------------------

// Evaluates a font-size value string → { status, printed }.
export function evaluateFontSizeValue(text, registeredTokens, resolvedTokenValues = new Map()) {
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
export function evaluateFontFamilyValue(text, registeredTokens) {
  const trimmed = text.trim()
  const varMatch = VAR_REFERENCE_PATTERN.exec(trimmed)
  if (varMatch) {
    const status = registeredTokens.has(varMatch[1]) ? 'ok' : 'unregistered'
    return { status, printed: `var(${varMatch[1]})` }
  }
  if (CSS_WIDE_KEYWORDS.has(trimmed)) return { status: 'ok', printed: trimmed }
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
export function parseFontShorthand(text) {
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

export function collapseWhitespace(text) {
  return text.replace(/\s+/g, ' ').trim()
}

// ---------------------------------------------------------------------------
// Tailwind class-token classification.
// ---------------------------------------------------------------------------

// Every classification callback receives a sink:
//   { emit(finding, position?), isRegistered(name), registeredTokens }.
export function classifyClassToken(token, sink) {
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

export function classifyRoleUtility(prefix, rest, sink) {
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

export function classifyTextUtility(rest, canonicalToken, sink) {
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
export function nestedTokenFallbacks(value) {
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

export function classifyArbitrarySize({ inner, wrapper }, sink) {
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

export function evaluateArbitrarySizeValue(value, syntax, sink) {
  // `text-[length:inherit]` (and initial/unset/revert/revert-layer): a
  // CSS-wide keyword is value plumbing, never a magic number — it names no
  // size of its own, only "take whatever is already computed elsewhere",
  // which cannot drift off the D9/D10 scale because it carries no scale
  // position to drift from. Tokenizing/parsing it as a length expression
  // would throw (parseAtom requires a bare word to be a function call), so
  // this must short-circuit before that attempt, exactly like
  // evaluateFontSizeValue already does for the identical keyword set on the
  // raw-CSS `font-size:` declaration path (§D1/§D2) — this closes the one
  // place that path and the Tailwind arbitrary-utility path disagreed.
  // Only the `length:`-hinted call site (classifyArbitrarySize's
  // `text-[length:…]` branch) can ever reach this function with a bare
  // keyword: the other two call sites gate on BARE_CUSTOM_PROPERTY_PATTERN
  // (`--…`) and isLengthLike (`\d`/calc|clamp|min|max), neither of which
  // "inherit" et al. satisfy, so an untyped `text-[inherit]` never reaches
  // this branch and still falls through to its own unsupported-text-utility
  // finding unchanged.
  //
  // Whether a "governed parent" actually exists at the DOM node this class
  // ends up on is NOT something this static, per-declaration scanner can see
  // — it has no render-tree/DOM model, only the literal text of one class
  // value. The identical question already exists for `color: currentColor`
  // in css-colors.mjs's COLOR_KEYWORD_EXEMPTS and is answered the same way
  // there: accepted unconditionally, not gated on a provably-governed
  // ancestor, because CSS-wide keywords carry no value of their own to
  // register in the first place — there is nothing here for a central
  // reviewer to approve. An off-scale literal like `text-[13px]` remains
  // fully reportable; only the zero-information keyword form is exempt.
  if (CSS_WIDE_KEYWORDS.has(value.trim())) return
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

export function classifyFontUtility(rest, sink) {
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

export function classifyArbitraryFamily(value, syntax, sink) {
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

export function matchArbitrary(rest) {
  const bracket = /^\[(.*)\]$/.exec(rest)
  if (bracket) return { inner: bracket[1], wrapper: '[' }
  const paren = /^\((.*)\)$/.exec(rest)
  if (paren) return { inner: paren[1], wrapper: '(' }
  return null
}

export function collapseArbitraryWhitespace(inner) {
  return collapseWhitespace(inner).replace(/\s*:\s*/, ':')
}

export function isColourLike(value) {
  return /^(#|rgb\(|rgba\(|hsl\(|hsla\(|oklch\(|oklab\(|lab\(|lch\(|color\()/.test(value)
}

export function isLengthLike(value) {
  return /^[\d.]/.test(value) || /^(calc|clamp|min|max)\(/.test(value)
}

// ---------------------------------------------------------------------------
// TypeScript / TSX scan.
// ---------------------------------------------------------------------------

export function scriptKindFor(path) {
  const lowerPath = path.toLowerCase()
  if (lowerPath.endsWith('.svg')) return ts.ScriptKind.JSX
  if (lowerPath.endsWith('.tsx')) return ts.ScriptKind.TSX
  if (lowerPath.endsWith('.ts') || lowerPath.endsWith('.mts') || lowerPath.endsWith('.cts')) return ts.ScriptKind.TS
  if (lowerPath.endsWith('.jsx')) return ts.ScriptKind.JSX
  return ts.ScriptKind.JS
}
