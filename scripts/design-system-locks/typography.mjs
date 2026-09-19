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

// P10 precision fix: a CLASS_BUILDER's OWN definition — `export function
// cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }`
// (src/lib/utils.ts) — is not a live class value to prove. `visit()` walks
// EVERY call to a CLASS_BUILDERS-named function anywhere in the file, not
// just inside a className/cn() call site, so `cn`'s own body — which itself
// calls `twMerge`/`clsx`, both CLASS_BUILDERS members — was being walked as
// though it were a real usage, and its own rest parameter flagged as an
// unresolved dynamic class expression. Every REAL class argument is already
// proven at each actual call site elsewhere; the definition itself only
// repackages/joins whatever was passed in. Narrow and structural (never a
// name allowlist beyond the already-trusted CLASS_BUILDERS set): the
// enclosing function's own resolvable declaration name must itself be a
// CLASS_BUILDER, it must take exactly one parameter (plain or rest), its
// body must be nothing but a single return of a chain of CLASS_BUILDER
// calls, and the identifier under test must be that same parameter forwarded
// unchanged (whole or spread) as a bare argument somewhere in that chain.
// Anything else — extra statements, a differently-named receiver, a
// non-CLASS_BUILDER callee anywhere in the chain — is left exactly as
// unproven as before (falls through to the existing proofs / emitUnsupported).
function classBuilderOwnParameterForward(identifier) {
  let fn = identifier.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn || fn.parameters.length !== 1) return false
  const parameter = fn.parameters[0]
  if (!ts.isIdentifier(parameter.name) || parameter.name.text !== identifier.text) return false
  const declarationName = classBuilderDeclarationName(fn)
  if (!declarationName || !CLASS_BUILDERS.has(declarationName)) return false
  const returned = classBuilderSingleReturnExpression(fn)
  if (!returned) return false
  return classBuilderChainForwardsParameter(returned, parameter.name.text, new Set())
}

// The stable name a function-like node is declared under — a function
// declaration's own name, or the identifier of a `const NAME = (...) => ...`
// / `const NAME = function (...) {...}` it is the initializer of. Anything
// else (a method, an inline callback, an unnamed export default) is not a
// recognizable CLASS_BUILDERS declaration and returns null.
function classBuilderDeclarationName(fn) {
  if (ts.isFunctionDeclaration(fn)) return fn.name ? fn.name.text : null
  const parent = fn.parent
  return parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) && parent.initializer === fn
    ? parent.name.text
    : null
}

// A function-like node's body reduced to its single meaningful expression:
// a block whose only statement is `return <expr>`, or an arrow function's
// direct expression body. Any other shape (multiple statements, no return,
// a non-expression return) is not provably transparent and returns null.
function classBuilderSingleReturnExpression(fn) {
  if (!fn.body) return null
  if (!ts.isBlock(fn.body)) return fn.body
  if (fn.body.statements.length !== 1) return null
  const statement = fn.body.statements[0]
  return ts.isReturnStatement(statement) && statement.expression ? statement.expression : null
}

// True when `expression` — after unwrapping parens/as/satisfies/non-null —
// is either the bare parameter identifier itself, or a call to a
// CLASS_BUILDERS-named function where at least one argument recursively
// forwards it the same way. `seen` guards against a call chain that somehow
// revisits the same node. Deliberately NOT extended to a spread argument
// (`clsx(...inputs)`): the general class-expression walker has no
// SpreadElement case at all (falls to its default emitUnsupported before
// this proof is ever consulted for one), so claiming spread support here
// would be dead code proving something the walker cannot reach — cn()'s
// real shape (src/lib/utils.ts) passes the whole array (`clsx(inputs)`),
// never a spread.
function classBuilderChainForwardsParameter(expression, parameterName, seen) {
  const unwrapped = unwrapStatic(expression)
  if (!unwrapped || seen.has(unwrapped)) return false
  seen.add(unwrapped)
  if (ts.isIdentifier(unwrapped)) return unwrapped.text === parameterName
  if (!ts.isCallExpression(unwrapped) || !ts.isIdentifier(unwrapped.expression) || !CLASS_BUILDERS.has(unwrapped.expression.text)) return false
  return unwrapped.arguments.some((argument) => classBuilderChainForwardsParameter(argument, parameterName, seen))
}

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
function isClassLikeParameterName(name) {
  return name === 'className' || name === 'class'
    || /(?:^|[a-z0-9])(?:ClassName|Class)$/.test(name)
}

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
    // The label always reports the SOURCE contract name (e.g. `className`
    // for a renamed `{ className: chevronClassName }`), never the local
    // alias — the alias is an implementation detail, the source prop name is
    // the stable receiving identity the contract requires.
    let boundaryName = identifier.text
    for (const parameter of fn.parameters) {
      if (ts.isIdentifier(parameter.name) && parameter.name.text === identifier.text && isClassLikeParameterName(identifier.text)) {
        initializer = parameter.initializer; declaration = parameter.name; break
      }
      if (ts.isObjectBindingPattern(parameter.name)) for (const element of parameter.name.elements) {
        if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
        const property = element.propertyName?.getText(sourceFile) ?? element.name.text
        if (isClassLikeParameterName(property)) { initializer = element.initializer; declaration = element.name; boundaryName = property; break }
      }
    }
    let sourceParameterName = null
    let sourceProperty = 'className'
    if (!declaration && fn.body) {
      // Body destructuring — `const { className } = props` and the renamed
      // `const { className: alias } = props` — is the second proven unchanged
      // forwarding shape. Provenance requires the destructured source to be a
      // bare identifier that is a parameter of this same function and the
      // binding to be `const`; helper/store outputs, member objects with
      // fallbacks (incl. `props ?? {}` — pinned unsupported by an existing
      // fixture: "keeps body-destructured forwarding with unproven
      // provenance unsupported"), `let` bindings and non-class-like
      // properties keep unsupported.
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
                // Narrower than the direct-parameter branches above (pinned
                // by an existing fixture): body destructuring only proves
                // literal `className`/`class`, never a class-like alias of a
                // different source property name.
                if (property === 'className' || property === 'class') matches.push({ initializer: element.initializer, declaration: element.name, source: source.text, property })
              }
            }
          }
          // Only the innermost unambiguous destructuring classifies.
          if (matches.length > 1) return null
          if (matches.length === 1) {
            initializer = matches[0].initializer; declaration = matches[0].declaration; sourceParameterName = matches[0].source; sourceProperty = matches[0].property; boundaryName = matches[0].property; break
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
        if (ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) && node.left.name.text === sourceProperty
          && ts.isIdentifier(node.left.expression) && node.left.expression.text === sourceParameterName
          && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
        if (ts.isDeleteExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === sourceProperty
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === sourceParameterName) reassigned = true
        if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'assign'
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object'
          && node.arguments.length > 0 && ts.isIdentifier(node.arguments[0]) && node.arguments[0].text === sourceParameterName) reassigned = true
      }
      node.forEachChild(check)
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    let ownerName = ownerNameFor(fn)
    let renderPropPath = null
    if (!ownerName) {
      // Render-prop shape: `components={{ Chevron: ({ className: alias }) =>
      // ... }}` (react-day-picker's Calendar#Chevron). `fn` here is the
      // property's function VALUE, so ownerNameFor's own climb (which only
      // resolves a named variable declaration or a forwardRef/memo call
      // wrapper) finds no name and returns null — it is not built to look
      // past a JSX-attribute object literal. anonymousRegistrationOwner is
      // no substitute either: its walk requires the object literal to
      // eventually sit inside a CallExpression argument (e.g. `cva('x', {
      // variants: {...} })`), never a JsxAttribute. renderPropOwner is
      // narrowly scoped to exactly this third shape: a function that is the
      // value of a named (non-computed) property in an object literal that
      // is itself the direct value of a JSX attribute. The owner is the
      // nearest NAMED enclosing component (matching parameterMemberBoundary's
      // nearestNamedAncestorOwner), and the render-prop's own property name
      // (e.g. `Chevron`) prefixes the boundary name so the finding names the
      // exact receiving identity, e.g. `Calendar#Chevron.className`.
      const renderProp = renderPropOwner(fn)
      if (renderProp) { ownerName = renderProp.owner; renderPropPath = renderProp.property }
    }
    if (!ownerName) return null
    return { symbol: ownerName, name: renderPropPath ? `${renderPropPath}.${boundaryName}` : boundaryName, initializer, declaration }
  }

  // Resolves the owner for `forwardedClassBoundary` when the forwarding
  // function is a render-prop value inside a JSX attribute's object literal
  // (`components={{ Chevron: (...) => ... }}`) rather than a named variable
  // or a forwardRef/memo-wrapped export. Deliberately narrow: every step of
  // the climb must match exactly, or it returns null and the caller falls
  // back to unsupported — this only ever ADDS a resolvable owner for a shape
  // ownerNameFor/anonymousRegistrationOwner cannot reach, never widens what
  // counts as an unchanged forward (that proof already happened above).
  function renderPropOwner(fn) {
    const propertyAssignment = fn.parent
    if (!propertyAssignment || !ts.isPropertyAssignment(propertyAssignment) || propertyAssignment.initializer !== fn) return null
    if (ts.isComputedPropertyName(propertyAssignment.name)) return null
    const propertyName = ts.isIdentifier(propertyAssignment.name) || ts.isStringLiteral(propertyAssignment.name)
      ? propertyAssignment.name.text : null
    if (!propertyName) return null
    const objectLiteral = propertyAssignment.parent
    if (!objectLiteral || !ts.isObjectLiteralExpression(objectLiteral)) return null
    const jsxExpression = objectLiteral.parent
    if (!jsxExpression || !ts.isJsxExpression(jsxExpression)) return null
    const jsxAttribute = jsxExpression.parent
    if (!jsxAttribute || !ts.isJsxAttribute(jsxAttribute)) return null
    const ownerName = nearestNamedAncestorOwner(jsxAttribute)
    if (!ownerName) return null
    return { owner: ownerName, property: propertyName }
  }

  // Shared by forwardedClassBoundary and parameterMemberBoundary: resolves
  // the stable component/function name a boundary finding's `symbol` names,
  // unwrapping parenthesized/as/satisfies wrappers and forwardRef/memo.
  function ownerNameFor(fn) {
    let parent = fn.parent
    while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    while (parent && ts.isCallExpression(parent) && isComponentWrapper(parent.expression)) {
      parent = parent.parent
      while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    }
    return ts.isFunctionDeclaration(fn) && fn.name ? fn.name.text
      : parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) ? parent.name.text
        : anonymousRegistrationOwner(fn)
  }

  // Capability B (independent review): `X.className` / `X?.className` where
  // X is an unmutated, uniquely-bound, bare (non-destructured) parameter of
  // the nearest enclosing function — e.g. a `.map((item) => <X
  // className={item.className} />)` callback parameter, or a
  // `RetryableState({ iconProps }) { ... iconProps?.className ... }` prop
  // object read directly by member access rather than destructured. Distinct
  // from forwardedClassBoundary (which proves the *identifier itself* is an
  // unchanged forwarded className): here the identifier is a param and only
  // one MEMBER of it is read. Restricted to the literal `className`/`class`
  // key (not the broader class-like-name heuristic) since, unlike a
  // same-named parameter, an arbitrary property name carries no naming
  // signal that it is a CSS class at all.
  function parameterMemberBoundary(node) {
    const key = ts.isPropertyAccessExpression(node) ? node.name.text
      : node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
        ? node.argumentExpression.text : null
    if (key !== 'className' && key !== 'class') return null
    const base = unwrapStatic(node.expression)
    if (!ts.isIdentifier(base)) return null
    if (duplicateBindings.has(base.text) || hasMultipleVariableDeclarations(sourceFile, base.text)) return null
    let fn = node.parent
    while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
    if (!fn) return null
    if (plainDeclarationShadows(base, fn)) return null
    // `base` may be a bare parameter (`function f(item) { ... item.className }`)
    // or itself a destructured element of an object-pattern parameter
    // (`RetryableState({ iconProps }) { ... iconProps?.className ... }`) —
    // either way it is the whole prop object being read, not further
    // destructured.
    const parameter = fn.parameters.find((candidate) => {
      if (ts.isIdentifier(candidate.name) && candidate.name.text === base.text) return true
      if (ts.isObjectBindingPattern(candidate.name)) {
        return candidate.name.elements.some((element) =>
          ts.isBindingElement(element) && !element.dotDotDotToken && ts.isIdentifier(element.name) && element.name.text === base.text)
      }
      return false
    })
    if (!parameter) return null
    let reassigned = false
    function check(current) {
      if (!reassigned) {
        if (ts.isBinaryExpression(current) && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
          if (ts.isIdentifier(current.left) && current.left.text === base.text) reassigned = true
          if (ts.isPropertyAccessExpression(current.left) && current.left.name.text === key
            && ts.isIdentifier(current.left.expression) && current.left.expression.text === base.text) reassigned = true
        }
        if (ts.isDeleteExpression(current) && ts.isPropertyAccessExpression(current.expression) && current.expression.name.text === key
          && ts.isIdentifier(current.expression.expression) && current.expression.expression.text === base.text) reassigned = true
        if (ts.isCallExpression(current) && ts.isPropertyAccessExpression(current.expression) && current.expression.name.text === 'assign'
          && ts.isIdentifier(current.expression.expression) && current.expression.expression.text === 'Object'
          && current.arguments.length > 0 && ts.isIdentifier(current.arguments[0]) && current.arguments[0].text === base.text) reassigned = true
        current.forEachChild(check)
      }
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    // ownerNameFor alone (shared with forwardedClassBoundary, left untouched
    // there) does not resolve an anonymous callback passed directly as a
    // bare call argument — `items.map((item) => ...)`, not registered under
    // a named object property — since anonymousRegistrationOwner requires at
    // least one named property in the chain. That shape is common for this
    // capability (a rendered list item), so fall back to the nearest NAMED
    // enclosing function/component as the stable receiving identity.
    const ownerName = ownerNameFor(fn) ?? nearestNamedAncestorOwner(fn)
    if (!ownerName) return null
    return { symbol: ownerName, name: `${base.text}.${key}` }
  }

  function nearestNamedAncestorOwner(fn) {
    let current = fn.parent
    while (current) {
      if (ts.isFunctionDeclaration(current) && current.name) return current.name.text
      if (ts.isArrowFunction(current) || ts.isFunctionExpression(current)) {
        const name = ownerNameFor(current)
        if (name) return name
      }
      current = current.parent
    }
    return null
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
  const classWalker = createClassWalker({ report, positionAt, sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary, parameterMemberBoundary, scannedParameterDefaults, moduleContext, handledCalls })
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

function createClassWalker({ report, positionAt, sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary, parameterMemberBoundary, scannedParameterDefaults, moduleContext, handledCalls }) {
  const nodeHelpers = createClassNodeHelpers(sourceFile)
  // Finding 3 fix, round 2: declarations currently "in progress" on the
  // active finite-call resolution chain (push before walking a resolved
  // call's returns, pop after) — see finiteCallReturns' declarationMarker
  // doc comment. Scoped to one walker/file, not permanently accumulating:
  // a marker is only ever present while its own subtree is still being
  // walked, so sibling (non-nested) calls to the same helper elsewhere are
  // never wrongly blocked.
  const finiteCallStack = new Set()
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
        const joinerDeclaration = transparentJoinerDeclaration(callee)
        if (joinerDeclaration) {
          node.arguments.forEach((argument) => walkClassExpression(argument, sink))
          return
        }
        // Broader than pureFunctionReturns above (single-statement, fully
        // pure return only): a local/imported/IIFE callee whose body is a
        // finite if-chain and/or switch of return statements (BULK_BUTTON_CLASS
        // style, or BrowserLiveView's `(() => { if (...) return {...}; ...
        // })()` chip config). Tried second so the stricter, longer-proven
        // path above still wins whenever both would apply.
        {
          const finiteReturns = finiteCallReturns(node, moduleContext, finiteCallStack)
          if (finiteReturns) {
            const declaration = calleeDeclaration(node, moduleContext)
            const marker = declarationMarker(declaration)
            finiteCallStack.add(marker)
            try {
              finiteReturns.forEach((returned) => walkClassExpression(returned, sink))
            } finally {
              finiteCallStack.delete(marker)
            }
            return
          }
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
        // P10: this identifier IS the parameter of the CLASS_BUILDER
        // function currently being DEFINED, forwarded unchanged into
        // another CLASS_BUILDER call — the definition site itself, not a
        // live call-site usage. See classBuilderOwnParameterForward.
        if (classBuilderOwnParameterForward(node)) return
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
          // Both the nearest-scope lexical lookup AND the flat top-level
          // `bindings` Map fallback must respect hasMultipleVariableDeclarations:
          // the same name reused ANYWHERE else in the file (e.g. a `let`
          // shadow inside a different, unrelated function) means findLexicalBinding's
          // const-only scope walk could climb past that shadow to the wrong
          // (but still textually valid) outer const — the top-level `bindings`
          // Map fallback must fail closed the same way, not bypass the guard
          // that disabled the lexical path in the first place (previously a
          // silent-pass gap: probed and closed).
          const ambiguousName = duplicateBindings.has(node.text) || hasMultipleVariableDeclarations(sourceFile, node.text)
          const lexical = ambiguousName ? null : findLexicalBinding(node, node.text)
          const binding = lexical?.initializer ?? (!ambiguousName && bindings.has(node.text) ? bindings.get(node.text) : null)
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
        {
          const destructured = duplicateBindings.has(node.text) || hasMultipleVariableDeclarations(sourceFile, node.text)
            ? null : findDestructuredBinding(node, node.text)
          if (destructured) {
            const leaves = classCarryingLeaves(destructured.source)
            if (leaves) {
              // Collect first, emit after (rather than walking each found
              // leaf inline): a missing leaf discovered LATER in the loop
              // can still fail the whole read closed, and nothing should
              // have been emitted for the found leaves in that case.
              const values = []
              let missingLeaf = false
              let unprovable = false
              for (const leaf of leaves) {
                const property = leaf.properties.find((candidate) => propertyKeyName(candidate) === destructured.property)
                if (!property) { missingLeaf = true; continue }
                if (ts.isPropertyAssignment(property)) values.push(property.initializer)
                else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
                else { unprovable = true; break }
              }
              // Same LEAD DECISION absence standard as resolveProvenPropertyAccess:
              // a missing leaf is only a safe no-op when absenceValue proves it
              // (null-prototype, no computed keys/spreads, safe top-level factory).
              if (unprovable || (missingLeaf && !absenceValue(destructured.source, destructured.property))) {
                emitUnsupported(node, sink)
                return
              }
              if (values.length > 0) { values.forEach((value) => walkClassExpression(value, sink)); return }
              if (destructured.defaultExpr) { walkClassExpression(destructured.defaultExpr, sink); return }
              return // proven absent on every branch (absenceValue passed above): no class content, safe no-op
            }
          }
        }
        emitUnsupported(node, sink)
        return
      case ts.SyntaxKind.PropertyAccessExpression:
      case ts.SyntaxKind.ElementAccessExpression: {
        const parameterBoundary = parameterMemberBoundary(node)
        if (parameterBoundary) {
          sink.emit({
            ruleId: 'typography/extension-boundary', syntax: `${parameterBoundary.symbol}#${parameterBoundary.name}`,
            message: 'Unchanged caller-supplied className member crosses a component extension boundary and remains blocking until exact central review.',
          })
          return
        }
        const proven = resolveProvenPropertyAccess(node)
        if (proven) {
          if (proven.unprovable) { emitUnsupported(node, sink); return }
          proven.values.forEach((value) => walkClassExpression(value, sink))
          return
        }
        const importedRecord = importedRecordAllValues(node)
        if (importedRecord) {
          importedRecord.forEach((value) => walkClassExpression(value, sink))
          return
        }
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

  // A same-file top-level `function name(...rest) { return rest(.filter(Boolean))?.join(sep) }`
  // — a transparent variadic class joiner sharing cn()/clsx()'s semantics
  // under a project-local name (ChipListInput.tsx's `classes()`). Detected
  // structurally rather than allowlisted by name: only a single rest
  // parameter, a single statement, and a `.join()` call (optionally preceded
  // by `.filter(Boolean)`) directly on that same rest parameter qualify —
  // anything else (extra statements, a different receiver, additional
  // transforms) is not provably transparent and is left alone.
  function transparentJoinerDeclaration(callee) {
    if (!ts.isIdentifier(callee)) return null
    const fnDecl = sameFileFunctionDeclaration(callee.text, sourceFile)
    if (!fnDecl || fnDecl.parameters.length !== 1) return null
    const parameter = fnDecl.parameters[0]
    if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name)) return null
    const restName = parameter.name.text
    if (fnDecl.body.statements.length !== 1) return null
    const statement = fnDecl.body.statements[0]
    if (!ts.isReturnStatement(statement) || !statement.expression) return null
    const expression = statement.expression
    if (!ts.isCallExpression(expression) || !ts.isPropertyAccessExpression(expression.expression) || expression.expression.name.text !== 'join') return null
    let receiver = expression.expression.expression
    if (ts.isCallExpression(receiver) && ts.isPropertyAccessExpression(receiver.expression) && receiver.expression.name.text === 'filter') {
      receiver = receiver.expression.expression
    }
    return ts.isIdentifier(receiver) && receiver.text === restName ? fnDecl : null
  }

  function isLeafSplittingOwner(node) {
    return ts.isCallExpression(node) || ts.isConditionalExpression(node)
      || (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken))
  }

  // A top-level `const NAME = <expr>` declaration node, resolved the same way
  // the `bindings` Map is populated (scanTypeScript's own top-level scan)
  // but returning the declaration itself, not just its initializer — needed
  // so callers can run the LEAD DECISION binding-safety proof
  // (absenceBindingUsesSafe) against it.
  function topLevelConstDeclaration(name) {
    for (const statement of sourceFile.statements) {
      if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name) && declaration.name.text === name && declaration.initializer) return declaration
      }
    }
    return null
  }

  // The owner expression a property-access base resolves to, restricted to
  // the NEW shapes handled by resolveProvenPropertyAccess/classCarryingLeaves
  // below (a call, a conditional, or a `??`/`||` fallback chain of such) —
  // plain object-literal owners keep going through the pre-existing
  // localCandidates path unchanged, so this never re-decides a case that
  // already worked. Returns `{ expr, declaration }`: `declaration` is the
  // const variable declaration an identifier hop went through (null when the
  // owner IS the call/conditional directly, e.g. no intermediate variable) —
  // LEAD DECISION requires that declaration's binding to be proven safe
  // (absenceBindingUsesSafe) before any conclusion drawn from it is trusted.
  function ownerExpressionFor(expression) {
    const unwrapped = unwrapStatic(expression)
    if (!unwrapped) return null
    if (isLeafSplittingOwner(unwrapped)) return { expr: unwrapped, declaration: null }
    if (ts.isIdentifier(unwrapped) && !duplicateBindings.has(unwrapped.text) && !hasMultipleVariableDeclarations(sourceFile, unwrapped.text)) {
      const lexical = findLexicalBinding(unwrapped, unwrapped.text)
      const initializer = lexical?.initializer ?? (bindings.has(unwrapped.text) && !duplicateBindings.has(unwrapped.text) ? bindings.get(unwrapped.text) : null)
      const resolved = initializer ? unwrapStatic(initializer) : null
      if (!resolved || !isLeafSplittingOwner(resolved)) return null
      const declaration = lexical?.declaration ?? topLevelConstDeclaration(unwrapped.text)
      return { expr: resolved, declaration }
    }
    return null
  }

  // Resolves a bare identifier to a top-level/lexical const-bound, or
  // imported, object literal — a "record" (STATUS_BADGE/PRIORITY_BADGE
  // style). Rejects spreads at the call sites below, not here. The local
  // branch is already guaranteed const (findLexicalBinding/the top-level
  // `bindings` Map only ever collect `const` declarations). The IMPORTED
  // branch is not — moduleRecord's export collection accepts any
  // VariableStatement regardless of const/let/var — so it must additionally
  // require the exporting declaration to be `const` AND never mutated in its
  // own module (absenceBindingUsesSafe): an exported mutable binding can be
  // reassigned to an arbitrary value by another importer and stays
  // unsupported (LEAD DECISION, "never-mutated const literal").
  function resolveRecordObjectLiteral(node) {
    const unwrapped = unwrapStatic(node)
    if (!unwrapped || !ts.isIdentifier(unwrapped)) return null
    if (duplicateBindings.has(unwrapped.text) || hasMultipleVariableDeclarations(sourceFile, unwrapped.text)) return null
    const lexical = findLexicalBinding(unwrapped, unwrapped.text)
    const localValue = lexical?.initializer ?? (bindings.has(unwrapped.text) ? bindings.get(unwrapped.text) : null)
    const local = localValue ? unwrapStatic(localValue) : null
    if (local && ts.isObjectLiteralExpression(local)) {
      // LEAD DECISION (Finding 1 fix, round 2): the LOCAL branch was
      // previously trusted purely because `bindings`/`findLexicalBinding`
      // only ever collect `const` declarations — but const-only blocks
      // *reassignment*, not a later member write (`config.small = '...'`)
      // onto the SAME never-reassigned binding. Require the same
      // never-mutated proof the imported branch below already carries.
      const localDeclaration = lexical?.declaration ?? topLevelConstDeclaration(unwrapped.text)
      // LEAD DECISION (round 3, derived-value escape proof): never-mutated
      // (absenceBindingUsesSafe) is not enough — REC's own record binding
      // being safe says nothing about a NESTED value read off it (`REC.a`)
      // escaping into a container/call/return/reassignment elsewhere in
      // scope and being mutated THROUGH that alias. See
      // derivedValueEscapeSafe's doc comment.
      if (!localDeclaration || !absenceBindingUsesSafe(localDeclaration, false, true) || !derivedValueEscapeSafe(localDeclaration, [local])) return null
      return local
    }
    const importedDecl = importedDeclaration(unwrapped, moduleContext)
    if (importedDecl && ts.isVariableDeclaration(importedDecl) && importedDecl.initializer
      && (importedDecl.parent.flags & ts.NodeFlags.Const) && absenceBindingUsesSafe(importedDecl, false, true)) {
      const imported = unwrapStatic(importedDecl.initializer)
      const importedContainers = imported && ts.isObjectLiteralExpression(imported) ? [imported] : null
      if (importedContainers
        // LEAD DECISION (Finding 2 fix, round 2): never-mutated-in-its-own-module
        // is not enough — a DIFFERENT importer can still mutate the exported
        // record's members from its own `ts.SourceFile`, invisible to the
        // check above. Require the same proof across every governed module.
        // Round 3: exportNeverMutatedByImporters now also runs the
        // derived-value escape proof per importer (containers threaded
        // through); the EXPORTING module's own copy of that same proof
        // still runs here too.
        && exportNeverMutatedByImporters(importedDecl, moduleContext, importedContainers)
        && derivedValueEscapeSafe(importedDecl, importedContainers)) return imported
    }
    return null
  }

  // The finite set of object-literal "leaves" a class-carrying expression can
  // resolve to: the expression itself if it is already an object literal
  // (rejecting spreads — an unproven source of extra properties), every leaf
  // of a (possibly nested) conditional or `??`/`||` fallback chain, every
  // finite return of a call, or a record lookup (RECORD[key] / RECORD.key,
  // local or imported — a literal key selects one property, proven absent is
  // an empty-but-valid result; a dynamic key enumerates every value, mirroring
  // importedRecordAllValues but composable inside a leaf chain, e.g.
  // `PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]`) — each recursively
  // reduced the same way. Returns null if any branch is not provably one of
  // these shapes (fail closed — no partial results).
  function classCarryingLeaves(node, seen = new Set()) {
    const expression = unwrapStatic(node)
    if (!expression) return null
    if (ts.isObjectLiteralExpression(expression)) {
      return expression.properties.some((property) => ts.isSpreadAssignment(property)) ? null : [expression]
    }
    if (ts.isConditionalExpression(expression)) {
      const yes = classCarryingLeaves(expression.whenTrue, seen)
      const no = classCarryingLeaves(expression.whenFalse, seen)
      return yes && no ? [...yes, ...no] : null
    }
    if (ts.isBinaryExpression(expression) && (expression.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || expression.operatorToken.kind === ts.SyntaxKind.BarBarToken)) {
      const left = classCarryingLeaves(expression.left, seen)
      const right = classCarryingLeaves(expression.right, seen)
      return left && right ? [...left, ...right] : null
    }
    if (ts.isCallExpression(expression)) {
      if (seen.has(expression)) return null
      const nextSeen = new Set(seen).add(expression)
      const branches = finiteCallReturns(expression, moduleContext)
      if (!branches) return null
      const leaves = []
      for (const branch of branches) {
        const nested = classCarryingLeaves(branch, nextSeen)
        if (!nested) return null
        leaves.push(...nested)
      }
      return leaves
    }
    if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
      const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
        : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression) || ts.isNumericLiteral(expression.argumentExpression))
          ? expression.argumentExpression.text : null
      const object = resolveRecordObjectLiteral(expression.expression)
      if (!object) return null
      if (key !== null) {
        const property = object.properties.find((candidate) => propertyKeyName(candidate) === key)
        if (!property) return [] // proven absent on this record entry: no class content, safe
        if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
        return classCarryingLeaves(ts.isPropertyAssignment(property) ? property.initializer : property.name, seen)
      }
      if (object.properties.some((property) => ts.isSpreadAssignment(property))) return null
      const leaves = []
      for (const property of object.properties) {
        if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
        const nested = classCarryingLeaves(ts.isPropertyAssignment(property) ? property.initializer : property.name, seen)
        if (!nested) return null
        leaves.push(...nested)
      }
      return leaves
    }
    return null
  }

  // Capability C1/C2 (independent review) applied to property access:
  // `X.prop`/`X?.prop`/`X['prop']` where X (after at most one identifier
  // hop) is a call or conditional expression provably reducible to a finite
  // set of object-literal leaves (classCarryingLeaves). A leaf missing the
  // property contributes nothing (proven absent, safe); a leaf whose
  // matching property is a getter/method/computed key is unprovable and
  // fails the WHOLE access closed (never a silent partial result).
  function resolveProvenPropertyAccess(node) {
    const key = ts.isPropertyAccessExpression(node) ? node.name.text
      : node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
        ? node.argumentExpression.text : null
    if (key === null) return null
    const owner = ownerExpressionFor(node.expression)
    if (!owner) return null
    // LEAD DECISION: an identifier hop's binding must be proven safe (never
    // reassigned/aliased/mutated/escaped anywhere in its enclosing block)
    // BEFORE any leaf — found or missing — is trusted. This gates PRESENT
    // branch enumeration too, not only the absence proof below (a member
    // write to a DIFFERENT property after the call still disqualifies the
    // whole binding, since it proves the object escapes untracked mutation).
    if (owner.declaration && !absenceBindingUsesSafe(owner.declaration, false, true)) return null
    const leaves = classCarryingLeaves(owner.expr)
    if (!leaves) return null
    // Round 3: same derived-value escape proof as resolveRecordObjectLiteral
    // — the identifier hop's binding being never-reassigned/mutated says
    // nothing about a nested (non-primitive) value read off it escaping
    // into a container/call/return/reassignment elsewhere in scope.
    if (owner.declaration && !derivedValueEscapeSafe(owner.declaration, leaves)) return null
    let missingLeaf = false
    const values = []
    for (const leaf of leaves) {
      const property = leaf.properties.find((candidate) => propertyKeyName(candidate) === key)
      if (!property) { missingLeaf = true; continue }
      if (ts.isPropertyAssignment(property)) values.push(property.initializer)
      else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
      else return { unprovable: true }
    }
    // A leaf lacking the key contributes nothing only when its absence is
    // independently proven (absenceValue — the ts-colors null-prototype
    // standard). Otherwise a missing static match could be a later member
    // write, a non-null-prototype object any caller can extend, or an
    // unsafe factory, and the whole read must fail closed (LEAD DECISION).
    if (missingLeaf && !absenceValue(owner.expr, key)) return { unprovable: true }
    return { unprovable: false, values }
  }

  // "Indexing into a finite const record with all values enumerated"
  // (existing localCandidates behavior) generalized to an IMPORTED record:
  // `IMPORTED_RECORD[dynamicKey]` where the static key can't be read but the
  // imported object literal's full, spread-free value set can — every value
  // is a valid candidate (STATUS_BADGE[status] style). A literal string key
  // is left to the existing resolveImportedExpression path untouched.
  function importedRecordAllValues(node) {
    if (!ts.isElementAccessExpression(node)) return null
    const key = node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
      ? node.argumentExpression.text : null
    if (key !== null) return null
    const base = unwrapStatic(node.expression)
    if (!ts.isIdentifier(base)) return null
    const declaration = importedDeclaration(base, moduleContext)
    // LEAD DECISION ("never-mutated const literal"): moduleRecord's export
    // collection does not filter by const/let/var, so this must check it
    // itself — an exported `let`/`var` binding can be reassigned wholesale by
    // another importer, and a mutated-in-its-own-module binding can hold a
    // different shape than its initializer shows; both stay unsupported.
    // Finding 2 fix (round 2): never-mutated-in-its-OWN-module is not
    // enough — a DIFFERENT importer can still mutate the exported record's
    // members from its own file, invisible to absenceBindingUsesSafe above.
    if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer
      || !(declaration.parent.flags & ts.NodeFlags.Const) || !absenceBindingUsesSafe(declaration, false, true)) return null
    const object = unwrapStatic(declaration.initializer)
    if (!ts.isObjectLiteralExpression(object) || object.properties.some((property) => ts.isSpreadAssignment(property))) return null
    // Round 3: derived-value escape proof — a nested (non-primitive)
    // property value enumerated here can still escape into a container/
    // call/return/reassignment elsewhere in scope, either in this file or
    // (threaded through as `containers`) in the exporting module/another
    // importer's own scope.
    if (!exportNeverMutatedByImporters(declaration, moduleContext, [object]) || !derivedValueEscapeSafe(declaration, [object])) return null
    const values = []
    for (const property of object.properties) {
      if (ts.isPropertyAssignment(property)) values.push(property.initializer)
      else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
      else return null
    }
    return values
  }

  // A body-level destructured local — `const { x } = <expr>` — bound to a
  // call/conditional/object-literal `<expr>` (GoalIndicator's `const {
  // className } = describeNonActiveState(state)`). Distinct from bindings/
  // findLexicalBinding, which only track simple identifier declarations, not
  // destructuring patterns. Scoped to the innermost enclosing block, same
  // uniqueness discipline as the rest of this file: more than one qualifying
  // destructure of the same local name in scope is unprovable.
  function findDestructuredBinding(identifier, name) {
    let scope = identifier.parent
    while (scope) {
      if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
        const matches = []
        for (const statement of scope.statements) {
          if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
          for (const declaration of statement.declarationList.declarations) {
            if (!ts.isObjectBindingPattern(declaration.name) || !declaration.initializer) continue
            for (const element of declaration.name.elements) {
              if (!ts.isBindingElement(element) || element.dotDotDotToken || !ts.isIdentifier(element.name) || element.name.text !== name) continue
              const property = element.propertyName?.getText(sourceFile) ?? element.name.text
              matches.push({ property, source: declaration.initializer, defaultExpr: element.initializer ?? null })
            }
          }
        }
        if (matches.length > 1) return null
        if (matches.length === 1) return matches[0]
      }
      scope = scope.parent
    }
    return null
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
      if (duplicateBindings.has(expression.text) || hasMultipleVariableDeclarations(sourceFile, expression.text)) return []
      const lexical = findLexicalBinding(expression, expression.text)
      const initializer = lexical?.initializer ?? (bindings.has(expression.text) ? bindings.get(expression.text) : null)
      if (!initializer) return []
      // LEAD DECISION (Finding 1 fix, round 2, parity with resolveProvenPropertyAccess/
      // resolveRecordObjectLiteral's absenceBindingUsesSafe gate): a same-file
      // binding must be proven never mutated — reassigned, aliased,
      // Object.assign'd, or member-written anywhere in its enclosing scope,
      // including AFTER this read site — before its declaration-time
      // initializer is trusted as the CURRENT value. Without this,
      // `const config = {...}; config.small = 'text-[10px]'` (or
      // `Object.assign(config, {...})`) silently resolved to the STALE
      // pre-mutation literal.
      const declaration = lexical?.declaration ?? topLevelConstDeclaration(expression.text)
      if (!declaration || !absenceBindingUsesSafe(declaration, false, true)) return []
      const resolved = unwrapStatic(initializer)
      if (!resolved) return []
      // Round 3: derived-value escape proof — never-mutated (absenceBindingUsesSafe)
      // proves nobody wrote through THIS binding's own name; it says nothing
      // about a nested (non-primitive) value read off it (`REC.a`) escaping
      // into a container/call/return/reassignment elsewhere in scope and
      // being mutated through THAT alias instead (cross-scanner-false-green
      // "array alias mutation of a nested object"). `containers` must be the
      // FULLY reduced object-literal leaf set `resolved` structurally holds
      // (classCarryingLeaves — the same general ternary/`??`/call/dynamic-
      // index-into-another-record reducer classCarryingLeaves/resolveProvenPropertyAccess
      // already use elsewhere in this file), NOT `resolved` itself: an
      // unresolved intermediate shape (a ConditionalExpression like
      // PolicyBadge's `inert ? {...} : {...}`, or a dynamic-key element
      // access like `POLICY_CONFIGS[policy]`) is not a container
      // derivedMemberTargets can traverse and was wrongly treated as an
      // unresolvable escape (found via the round-3 lane audit comparison:
      // PolicyBadge.tsx `cfg.activeColor`/`cfg.color`, TablePart.tsx
      // `inertProps.className` — real-tree false positives, not real debt).
      const structuralLeaves = classCarryingLeaves(resolved)
      const containers = structuralLeaves && structuralLeaves.length > 0 ? structuralLeaves : [resolved]
      // LEAD DECISION (false green found post-Stage-B: a record DEFINED and
      // EXPORTED in the reading file, `export const M = { a: GOOD }`, then
      // mutated by a DIFFERENT module that imports it — `import { M } from
      // './P'; M.a = BAD` — reached a className with zero findings, for both
      // `M.a` and `M[k]` reads). absenceBindingUsesSafe/derivedValueEscapeSafe
      // above only prove no write reaches `M` from THIS file's own
      // ts.SourceFile; a write from an IMPORTING module is invisible to both.
      // exportNeverMutatedByImporters closes exactly that gap (parity with
      // resolveRecordObjectLiteral's local branch and importedRecordAllValues,
      // and with ts-colors.mjs's knownClassExportUsesSafe): it is a no-op
      // (returns true immediately) for a non-exported declaration, so this
      // only ever ADDS a check for the exported case, never narrows the
      // non-exported one already proven above.
      if (!derivedValueEscapeSafe(declaration, containers) || !exportNeverMutatedByImporters(declaration, moduleContext, containers)) return []
      return localCandidates(resolved, seen)
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
    // Capability A (independent review): a span that LOOKS glued by raw
    // static-text adjacency can still be provably separated when the
    // interpolation is a (possibly nested) conditional whose every branch is
    // either empty or itself supplies the missing whitespace boundary — e.g.
    // `` `cell${isToday ? ' today' : ''}` `` (glued-before, both branches are
    // empty or start with a space) or ChatImage's `` `...${c ? ` ${c}` : ''}` ``
    // (the true branch is a nested template whose OWN head starts with a
    // space; its interior interpolation is resolved by the ordinary
    // recursive walk once reached). Computed up front so both passes below
    // (which token a static text's boundary must/must not trim, and how a
    // span itself classifies) agree.
    const selfSeparating = node.templateSpans.map((span, index) => {
      const before = staticTexts[index]
      const after = staticTexts[index + 1]
      const gluedBeforeRaw = /\S$/.test(before) || (before === '' && index > 0)
      const gluedAfterRaw = /^\S/.test(after) || (after === '' && index < node.templateSpans.length - 1)
      if (!gluedBeforeRaw && !gluedAfterRaw) return false
      const partial = before.split(/\s+/).filter(Boolean).pop() ?? ''
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') return false
      if (/\[(?:[^\]]*)?$/.test(partial) || /\((?:[^)]*)?$/.test(partial)) return false
      return isSelfSeparatingConditional(span.expression, gluedBeforeRaw, gluedAfterRaw)
    })
    staticTexts.forEach((text, index) => {
      const isFirst = index === 0
      const isLast = index === staticTexts.length - 1
      const tokens = text.split(/\s+/).filter(Boolean)
      const interior = [...tokens]
      // Tokens glued to an interpolation boundary are partial; the glue check
      // below handles them. Interior tokens classify normally. A boundary
      // whose adjacent span is self-separating is NOT partial — the static
      // token is already complete on its own (the branch supplies its own
      // separator or is empty), so it must not be trimmed away here.
      if (!isFirst && !selfSeparating[index - 1] && /^\S/.test(text) && interior.length > 0) interior.shift()
      if (!isLast && !selfSeparating[index] && /\S$/.test(text) && interior.length > 0) interior.pop()
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
      } else if (selfSeparating[index]) {
        recurse(span.expression, sink)
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

// Capability A's proof for a template span glued to adjacent static text:
// true only when every leaf of `node` (through nested conditionals) is
// either an empty string/no-substitution-template literal, or a string
// literal / template expression whose own boundary text already supplies
// the missing whitespace on every side the outer static text is glued on.
// A template-expression leaf's INTERIOR interpolations are not inspected
// here — they are resolved by the ordinary recursive template walk once
// this leaf is reached as separated. Any other expression shape (identifier,
// call, member access, non-whitespace-bounded literal) fails the proof.
function isSelfSeparatingConditional(node, gluedBefore, gluedAfter) {
  const expression = unwrapStatic(node)
  if (ts.isConditionalExpression(expression)) {
    return isSelfSeparatingConditional(expression.whenTrue, gluedBefore, gluedAfter)
      && isSelfSeparatingConditional(expression.whenFalse, gluedBefore, gluedAfter)
  }
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
    const text = expression.text
    if (text === '') return true
    if (gluedBefore && !/^\s/.test(text)) return false
    if (gluedAfter && !/\s$/.test(text)) return false
    return true
  }
  if (ts.isTemplateExpression(expression)) {
    const head = expression.head.text
    const tail = expression.templateSpans[expression.templateSpans.length - 1].literal.text
    if (gluedBefore && !/^\s/.test(head)) return false
    if (gluedAfter && !/\s$/.test(tail)) return false
    return true
  }
  return false
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



// P11 precision fix: a computed property key that is itself a string
// literal, just wrapped by a cast/assertion (`['fontSize' as string]: …`,
// `(['fontSize'])`, `!` non-null, `satisfies`), was invisible to this
// dispatcher — `ts.isComputedPropertyName(name)` matched, but neither the
// plain-literal nor the two-literal-PlusToken branch fired for an
// AsExpression-wrapped literal, so `stylePropertyName` returned null and
// `walkStyleProperty` silently skipped the property entirely (a
// `fontSize`/`fontFamily`/`font`/role value written this way was never
// checked against the D2 floor or the D9 token set at all — a bigger gap
// than an over-block, and strictly a coverage GAIN to close, never a
// loosening of fail-closed). `unwrapStatic` is the same cast-stripping this
// file already trusts everywhere else (class expressions, imported
// records); using it here just makes the computed-key reader consistent
// with the rest of the scanner. Only the identifier NAME becomes resolvable
// — the property's VALUE still goes through the exact same reportStyleSize/
// reportStyleFamily/reportRoleValue checks as a plain `fontSize: …` key, so
// a key that resolves to a name typography does not track (e.g. `'color'`)
// is unaffected: it still matches none of the STYLE_*_PROPERTY_NAMES /
// STYLE_ROLE_PROPERTIES sets and stays silently out of scope, exactly as a
// literal `color: …` key already is today.
function stylePropertyName(node) {
  const name = node.name
  if (ts.isIdentifier(name)) return name.text
  if (ts.isStringLiteral(name)) return name.text
  if (ts.isComputedPropertyName(name)) {
    const expression = unwrapStatic(name.expression)
    if (expression && (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression))) return expression.text
    if (expression && ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = unwrapStatic(expression.left); const right = unwrapStatic(expression.right)
      if (left && right && ts.isStringLiteral(left) && ts.isStringLiteral(right)) return left.text + right.text
    }
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

// Only a JS/TS module can contain import/export/require syntax relevant to
// exportNeverMutatedByImporters' cross-module proof below (parity with
// ts-colors.mjs's isJsModulePath) — an asset file (`.svg`, `.css`, …)
// present in the governed `modules` map is irrelevant and must not be
// treated as an (unparseable) importer candidate.
function isGovernedModulePath(modulePath) {
  const lower = modulePath.toLowerCase()
  return lower.endsWith('.ts') || lower.endsWith('.tsx') || lower.endsWith('.js') || lower.endsWith('.jsx')
    || lower.endsWith('.mts') || lower.endsWith('.cts') || lower.endsWith('.mjs') || lower.endsWith('.cjs')
}

// A template-literal dynamic import()/require() argument's HEAD is always
// the literal prefix of whatever string it evaluates to at runtime — a
// substitution can only append characters after it, never rewrite or erase
// them. governedModulePath only ever resolves a specifier that itself
// starts with '@/' or a relative '.' form. If the head cannot possibly grow
// into one of those two forms, no runtime value of the template can EVER be
// a governedModulePath-resolvable specifier at all — proven impossible, not
// guessed — so it can never target `origin`. When the head IS shaped like a
// local specifier, it must additionally share `origin`'s directory (and,
// lacking a trailing '/', `origin`'s basename must share the head's own
// final segment as a prefix), or it is still a provably different target.
// Any other argument shape stays exactly as conservative as before: not
// excluded. Ported verbatim from ts-colors.mjs's dynamicImportProvenNotOrigin
// (parity, Finding 2 fix round 2) — without this, a SINGLE dynamic
// import()/require() anywhere in the ENTIRE governed tree (e.g. a test
// file's `await import('@/store/ui')`, wholly unrelated to the record being
// proven) poisoned exportNeverMutatedByImporters for EVERY exported record
// program-wide, not just ones the dynamic import could plausibly reach —
// found via a real-tree audit run flagging src/components/workspaces/
// taskStatusConfig.ts's STATUS_BADGE (genuinely never mutated anywhere)
// solely because an unrelated test file contained an unrelated dynamic
// import.
function dynamicImportProvenNotOrigin(modulePath, argument, origin) {
  if (!ts.isTemplateExpression(argument)) return false
  const head = argument.head.text
  if (head.startsWith('@/') || head.startsWith('./') || head.startsWith('../')) {
    const prefix = head.startsWith('@/') ? `src/${head.slice(2)}` : path.posix.normalize(path.posix.join(path.posix.dirname(modulePath), head))
    const prefixDir = prefix.endsWith('/') ? prefix.slice(0, -1) : path.posix.dirname(prefix)
    const originDir = path.posix.dirname(origin)
    if (originDir !== prefixDir) return true
    if (prefix.endsWith('/')) return false
    return !path.posix.basename(origin).startsWith(path.posix.basename(prefix))
  }
  if ('@/'.startsWith(head) || './'.startsWith(head) || '../'.startsWith(head)) return false
  return true
}

// LEAD DECISION (Finding 2 fix, round 2 — parity with ts-colors.mjs's
// knownClassExportUsesSafe): an exported record's declaring module cannot
// see a member write made by ANY OTHER module that imports it — `import {
// SIZES } from './sizes'; SIZES.small = 'text-[10px]'` mutates SIZES from a
// different `ts.SourceFile` than the one absenceBindingUsesSafe on the
// EXPORTING declaration can ever walk. Scan every module supplied in the
// governed `modules` context for a static import of this export; each
// static named import must itself pass absenceBindingUsesSafe inside the
// IMPORTING file's own scope. A default/namespace import sharing the same
// import statement, a type-only re-export that is not itself type-only, a
// dynamic `import()`/`require()` PROVABLY reaching this origin module, or a
// missing/unparseable module all fail closed (return false) rather than
// assume safety — a dynamic import PROVABLY reaching some OTHER module is
// not this record's concern (dynamicImportProvenNotOrigin above) and must
// not poison every unrelated exported record in the governed tree.
// `containers`, when supplied, is the record value(s) `declaration` is
// already known (by the caller) to structurally hold — threaded through so
// EACH importer's specifier binding also passes the round-3 derived-value
// escape proof (parity with ts-colors.mjs's knownClassExportUsesSafe, which
// pairs absenceBindingUsesSafe with knownClassDerivedUsesSafe per importer,
// not just on the exporting declaration): a nested value read off a
// never-reassigned import specifier can escape into a container/call/
// return/reassignment in the IMPORTER's own scope exactly as it can in the
// exporting module's scope.
function exportNeverMutatedByImporters(declaration, context, containers = null) {
  const statement = declaration.parent?.parent
  if (!statement || !ts.isVariableStatement(statement)) return false
  if (!statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) return true
  if (!context?.modules || !ts.isIdentifier(declaration.name)) return false
  const origin = declaration.getSourceFile().fileName
  for (const modulePath of Object.keys(context.modules)) {
    if (!isGovernedModulePath(modulePath)) continue
    const record = moduleRecord(context, modulePath)
    if (!record || !record.valid) return false
    for (const item of record.sourceFile.statements) {
      if ((!ts.isImportDeclaration(item) && !ts.isExportDeclaration(item)) || !item.moduleSpecifier || !ts.isStringLiteral(item.moduleSpecifier)) continue
      if (governedModulePath(modulePath, item.moduleSpecifier.text, context.modules) !== origin) continue
      if (ts.isExportDeclaration(item)) {
        if (!item.isTypeOnly) return false
        continue
      }
      const clause = item.importClause
      if (!clause || clause.isTypeOnly) continue
      const named = clause.namedBindings
      if (clause.name || (named && !ts.isNamedImports(named))) return false
      if (named) for (const specifier of named.elements) {
        if (specifier.isTypeOnly || (specifier.propertyName?.text ?? specifier.name.text) !== declaration.name.text) continue
        if (!absenceBindingUsesSafe(specifier, false, true) || (containers && !derivedValueEscapeSafe(specifier, containers))) return false
      }
    }
    let dynamic = false
    const visitDynamic = (node) => {
      if (dynamic) return
      if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword
        || (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
        const argument = node.arguments[0]
        if (!argument) dynamic = true
        else if (ts.isStringLiteralLike(argument)) {
          if (governedModulePath(modulePath, argument.text, context.modules) === origin) dynamic = true
        } else if (!dynamicImportProvenNotOrigin(modulePath, argument, origin)) dynamic = true
        if (dynamic) return
      }
      node.forEachChild(visitDynamic)
    }
    visitDynamic(record.sourceFile)
    if (dynamic) return false
  }
  return true
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
    // LEAD DECISION (Finding 2 fix, round 2): this was the un-guarded
    // fallback path — no const check, no same-module mutation check, no
    // cross-module mutation check — that let `import { SIZES } from
    // './sizes'; SIZES.small = 'text-[10px]'` resolve to the stale
    // exporter-time literal with zero findings. `const`-only is required
    // unconditionally (a `let` export can be reassigned wholesale by its
    // OWN module regardless of member shape).
    if (!ts.isVariableDeclaration(declaration) || !declaration.initializer
      || !(declaration.parent.flags & ts.NodeFlags.Const)) return null
    const initializer = unwrapStatic(declaration.initializer)
    // The never-mutated MEMBER proof (absenceBindingUsesSafe +
    // exportNeverMutatedByImporters) only matters when the resolved value
    // is a RECORD (object/array literal) whose own members could later be
    // written out from under this resolution — that IS Finding 2's exploit
    // shape. For a plain primitive export (a string/template built by
    // concatenation — e.g. LibraryPreviewPane.tsx's `LIBRARY_ICON_BTN`,
    // referenced directly as `className={LIBRARY_ICON_BTN}` — the far MORE
    // common shape), `absenceBindingUsesSafe`'s actual contract ("every
    // reference is a property/element access") is the WRONG question: a
    // `const` primitive cannot be reassigned by the language itself and has
    // no mutable members, so demanding every reference be `.prop`-shaped
    // wrongly rejected the entirely normal, safe pattern of referencing the
    // constant directly — a real regression found via a real-tree audit run
    // (`LIBRARY_ICON_BTN`/`LINK_CLASS`/`priorityBadge.className`-shaped
    // constants newly, wrongly flagged unsupported).
    const isRecordLiteral = initializer && (ts.isObjectLiteralExpression(initializer) || ts.isArrayLiteralExpression(initializer))
    // Round 3: derived-value escape proof, same as resolveRecordObjectLiteral
    // — required alongside the never-mutated proofs above whenever the
    // resolved value is a record whose nested members could carry a live
    // reference out through a container/call/return/reassignment elsewhere
    // in this (exporting) module's scope.
    if (isRecordLiteral && (!absenceBindingUsesSafe(declaration, false, true) || !exportNeverMutatedByImporters(declaration, context, [initializer])
      || !derivedValueEscapeSafe(declaration, [initializer]))) return null
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
// Finite-return call resolution (independent review capabilities C1/C2):
// resolves a call to a local/imported function, or an immediately-invoked
// function expression, whose body is provably a FINITE, enumerable set of
// return expressions — an if-chain of guard returns ending optionally in an
// unconditional return, and/or a single switch statement — with only
// harmless (`const`, `void <expr>`, `throw`) statements otherwise. Used both
// to resolve a whole call used directly as a class value (BULK_BUTTON_CLASS
// style) and, via classCarryingLeaves below, to resolve property reads off a
// call's returned object literal (statusConfig.textClass style). Anything
// outside this shape (mutable state, unenumerable control flow, `let`
// reassignment across branches, opaque helper output) is NOT provable here
// and callers must fail closed.
// ---------------------------------------------------------------------------

// Resolves the single meaningful return/throw in a short statement list
// (a switch clause body or an if-branch body), skipping only harmless
// leading `const` declarations and `void <expr>` statements. Returns
// { ok:true, expr } (expr is null for a bare `return;`/`throw`) when proven,
// { ok:false } otherwise (unrecognized statement — fail closed).
function terminalReturnOutcome(statements) {
  for (const statement of statements) {
    if (ts.isBlock(statement)) return terminalReturnOutcome(statement.statements)
    if (ts.isReturnStatement(statement)) return { ok: true, expr: statement.expression ?? null }
    if (ts.isThrowStatement(statement)) return { ok: true, expr: null }
    if (ts.isVariableStatement(statement) && (statement.declarationList.flags & ts.NodeFlags.Const)) continue
    if (ts.isExpressionStatement(statement) && ts.isVoidExpression(statement.expression)) continue
    return { ok: false }
  }
  return { ok: false }
}

// A fallthrough (empty-statement) case clause contributes nothing of its
// own — the next non-empty clause's outcome covers it — so it is simply
// skipped rather than resolved.
function collectSwitchReturns(switchStatement) {
  const results = []
  for (const clause of switchStatement.caseBlock.clauses) {
    if (clause.statements.length === 0) continue
    const outcome = terminalReturnOutcome(clause.statements)
    if (!outcome.ok) return null
    if (outcome.expr) results.push(outcome.expr)
  }
  return results
}

// Collects every reachable return-value expression from a function body's
// top-level statements: const declarations and `void` statements are
// transparent; an `if (cond) return <expr>` guard (no `else`) contributes
// its outcome and falls through to the next statement; a switch statement
// contributes every clause's outcome; a return statement must be the LAST
// statement (nothing can follow it) and ends collection. No unconditional
// terminal return is a safe, valid outcome — the implicit `undefined` value
// contributes no class content, exactly like a switch with no default.
// Anything else (loops, try/catch, if/else, reassignment) is unprovable.
function collectFiniteReturns(statements) {
  const results = []
  for (let index = 0; index < statements.length; index += 1) {
    const statement = statements[index]
    if (ts.isVariableStatement(statement) && (statement.declarationList.flags & ts.NodeFlags.Const)) continue
    if (ts.isExpressionStatement(statement) && ts.isVoidExpression(statement.expression)) continue
    if (ts.isReturnStatement(statement)) {
      if (index !== statements.length - 1) return null
      if (statement.expression) results.push(statement.expression)
      return results
    }
    if (ts.isIfStatement(statement) && !statement.elseStatement) {
      const body = ts.isBlock(statement.thenStatement) ? statement.thenStatement.statements : [statement.thenStatement]
      const outcome = terminalReturnOutcome(body)
      if (!outcome.ok) return null
      if (outcome.expr) results.push(outcome.expr)
      continue
    }
    if (ts.isSwitchStatement(statement)) {
      const switchResults = collectSwitchReturns(statement)
      if (!switchResults) return null
      results.push(...switchResults)
      continue
    }
    return null
  }
  return results
}

// A same-file top-level `function name(...) {...}` declaration — distinct
// from findLexicalBinding (const bindings + parameters only) and
// importedDeclaration (import statements only), neither of which sees a
// plain function declaration in the SAME file.
function sameFileFunctionDeclaration(name, sourceFile) {
  for (const statement of sourceFile.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name?.text === name && statement.body) return statement
  }
  return null
}

// Resolves a call's callee to a function-like declaration: a local
// const-bound arrow/function expression, a same-file function declaration,
// an imported function, or (unlike functionDeclarationFor, used by the
// stricter single-return pureFunctionReturns above) an immediately-invoked
// function expression (`(() => {...})()`), whose callee is the function
// itself, not an identifier.
function calleeDeclaration(call, context) {
  const callee = unwrapStatic(call.expression)
  if (!callee) return null
  if (ts.isArrowFunction(callee) || ts.isFunctionExpression(callee)) return callee
  if (!ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)?.initializer
  const localFunction = unwrapStatic(local)
  if (localFunction && (ts.isArrowFunction(localFunction) || ts.isFunctionExpression(localFunction))) return localFunction
  const sameFile = sameFileFunctionDeclaration(callee.text, callee.getSourceFile())
  if (sameFile) return sameFile
  const imported = importedDeclaration(callee, context)
  return imported && (ts.isFunctionDeclaration(imported) || ts.isArrowFunction(imported) || ts.isFunctionExpression(imported)) ? imported : null
}

// A stable identity for a callee declaration — filename + source position —
// used by finiteCallReturns' cycle guard below. collectFiniteReturns is
// deterministic per declaration (it depends only on the declaration's body,
// never on which call site triggered resolution), so a self- or mutually-
// recursive helper always re-resolves to the SAME declaration on every hop
// around the cycle; keying the guard on the declaration (not the call site)
// catches that regardless of how many distinct call-expression nodes the
// cycle passes through.
function declarationMarker(declaration) {
  return `${declaration.getSourceFile().fileName}#${declaration.pos}`
}

// Every finite return-value expression a call can produce, or null if the
// callee/body is not provably finite. A non-block (concise) arrow body is
// trivially one expression.
//
// LEAD DECISION (Finding 3 fix, round 2): a self-recursive helper
// (`function cls(n){ return n>0 ? cls(n-1) : 'text-[10px]' }`) or a mutually
// recursive pair has no cycle guard here on its own — this function alone
// cannot loop (it does not call itself), but a caller that walks a returned
// call back into finiteCallReturns again (walkClassExpression's direct
// class-value CallExpression path does exactly this) recurses forever and
// crashes with a RangeError instead of failing closed. `stack` lets a
// caller mark a declaration as "currently being resolved" for the duration
// of walking ITS returns; re-entering finiteCallReturns for the same
// declaration while its marker is still active is a proven cycle and must
// fail closed (null — the same "not provably finite" contract as any other
// unsupported shape), never throw. Defaults to a fresh Set so existing
// callers that do not thread a stack (classCarryingLeaves, which already
// carries its own independent node-identity cycle guard) are unaffected.
function finiteCallReturns(call, context, stack = new Set()) {
  const declaration = calleeDeclaration(call, context)
  if (!declaration?.body) return null
  if (stack.has(declarationMarker(declaration))) return null
  if (!ts.isBlock(declaration.body)) return [declaration.body]
  return collectFiniteReturns(declaration.body.statements)
}

function propertyKeyName(property) {
  return property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) || ts.isNumericLiteral(property.name))
    ? property.name.text : null
}

// ---------------------------------------------------------------------------
// Absence-of-member proof (LEAD DECISION, cross-scanner consistency with
// ts-colors.mjs's absenceValue/absenceFactory/absenceBindingUsesSafe/
// absenceLexicalBinding/absentClassProperty — read there for the reviewed
// original; ported here, not re-derived, so the two lanes hold the SAME
// standard). A member is provably ABSENT only when:
//   - every candidate object literal reachable through the chain below has
//     an explicit `__proto__: null` and no computed keys or spreads
//     (an ordinary object can inherit or later gain the property from
//     application code or a prototype mutation elsewhere — only a null
//     prototype rules that out without assuming module-graph purity);
//   - the call chain that produced those literals, if any, resolves through
//     a safe, non-async, non-generator TOP-LEVEL factory function whose own
//     identifier is used only as a call callee; and
//   - any identifier binding (a `const NAME = <call>` an owner was reached
//     through) is used SAFELY everywhere in its enclosing block: read only
//     via property/element access, never reassigned, deleted, aliased,
//     Object.assign-mutated, incremented, or called as a method receiver —
//     scanning the whole block catches a write AFTER the read site too, not
//     only before it.
// Anything less fails closed: a missing static match is never silently "no
// class content" — it is unprovable and must reach emitUnsupported. This
// same standard gates PRESENT branch enumeration too (not just absence):
// the binding-safety leg above is checked before any leaf, found or
// missing, is trusted.
// ---------------------------------------------------------------------------

function absenceBindingHasName(binding, name) {
  if (ts.isIdentifier(binding)) return binding.text === name
  return (ts.isObjectBindingPattern(binding) || ts.isArrayBindingPattern(binding))
    && binding.elements.some((element) => ts.isBindingElement(element) && absenceBindingHasName(element.name, name))
}

// Resolve lexical declarations without falling through a nearer opaque
// binding. Switch/namespace/class/loop scopes and catch-clause/parameter
// shadows are deliberately outside this proof — ambiguous, not resolved.
function absenceLexicalBinding(identifier) {
  const name = identifier.text
  for (let scope = identifier.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (ts.isFunctionLike(scope) && scope.parameters.some((parameter) => absenceBindingHasName(parameter.name, name))) return null
    if (ts.isCatchClause(scope) && scope.variableDeclaration && absenceBindingHasName(scope.variableDeclaration.name, name)) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (ts.isVariableStatement(statement)) {
        for (const declaration of statement.declarationList.declarations) {
          if (absenceBindingHasName(declaration.name, name)) matches.push(declaration)
        }
      } else if (statement.name && ts.isIdentifier(statement.name) && statement.name.text === name) matches.push(statement)
      else if (ts.isImportDeclaration(statement)) {
        const clause = statement.importClause
        if (clause?.name?.text === name) matches.push(clause)
        const importedBindings = clause?.namedBindings
        if (importedBindings && ts.isNamedImports(importedBindings)) {
          for (const element of importedBindings.elements) if (element.name.text === name) matches.push(element)
        } else if (importedBindings?.name?.text === name) matches.push(importedBindings)
      }
    }
    if (matches.length) return matches.length === 1 ? matches[0] : null
  }
  return null
}

// A resolved member value consumed purely as a JSX element's own tag name
// (`<Icon/>`, `<driveChip.Icon/>`) is a terminal render read: React only
// invokes/constructs the referenced value, it is never handed anything that
// could reach back and mutate the stable record that produced it. Ported
// from ts-colors.mjs's isJsxTagName (parity, round 2) — without this an
// unrelated sibling property rendered as a component tag blanket-blocks
// every OTHER property read off the SAME record purely because it isn't
// itself a further `.member` read.
function isJsxTagName(expression) {
  const parent = expression.parent
  return Boolean(parent) && (ts.isJsxOpeningElement(parent) || ts.isJsxSelfClosingElement(parent) || ts.isJsxClosingElement(parent)) && parent.tagName === expression
}

// `Object.keys/freeze/isFrozen/getOwnPropertyNames(X)` are well-known,
// spec-pure static reads that can never hand a MUTABLE reference to one of
// `X`'s nested values back to the caller: `keys`/`getOwnPropertyNames`
// return a fresh array of plain (immutable) strings, and `freeze`/
// `isFrozen` only touch `X`'s own mutability flag. Ported from
// ts-colors.mjs's isReadonlyObjectStaticCallArgument/
// READONLY_OBJECT_STATIC_METHODS (parity, round 2) — found missing via a
// real-tree audit run: `ListView.tsx`'s `Object.keys(PRIORITY_BADGE)` (read
// once, for its domain of keys) wrongly disqualified EVERY other,
// unrelated `.prop`/`[key]` read of the same never-mutated imported record
// (`PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]`) once
// exportNeverMutatedByImporters started scanning importer usage.
//
// DELIBERATELY NARROWER than ts-colors.mjs's set here: ts-colors also
// exempts `values`/`entries` because its `knownClassDerivedUsesSafe`
// separately, recursively re-proves that EVERY value extracted that way is
// itself never escaped/mutated (own doc comment: "already covered by the
// SAME per-property proof used elsewhere in this file"). That recursive
// derived-value proof is not ported here — exempting `values`/`entries`
// WITHOUT it would silently trust `Object.values(REC).forEach(v => { v.cls
// = 'text-[10px]' })`, which hands back LIVE references to REC's own nested
// objects and mutates them in place (caught red-handed by
// tests/design-system-locks/cross-scanner-false-green.test.mjs's "Object.values
// mutation of a nested const record" / "Object.entries mutation ..." /
// "imported record mutated via Object.values by the importer" cases — all
// three zero-finding bypasses while `values`/`entries` were exempted here).
// `keys`/`freeze`/`isFrozen`/`getOwnPropertyNames` have no such hole: none
// of them ever return a reference to a nested object at all.
const READONLY_OBJECT_STATIC_METHODS = new Set(['keys', 'freeze', 'isFrozen', 'getOwnPropertyNames'])

function isReadonlyObjectStaticCallArgument(node) {
  const parent = node.parent
  if (!ts.isCallExpression(parent) || parent.expression === node || !parent.arguments.includes(node)) return false
  const callee = unwrapStatic(parent.expression)
  return ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && callee.expression.text === 'Object'
    && READONLY_OBJECT_STATIC_METHODS.has(callee.name.text)
}

// A simple, non-rest, non-default, non-nested object-destructuring read of a
// stable receiver (`const { Icon } = config`) is safe when every extracted
// local is itself only ever used safely (recursing through this same
// property-or-terminal-JSX-tag proof) — the same "read-only, never a whole
// mutable escape" guarantee absenceBindingUsesSafe already proves for a
// plain member-access alias, just entered through a binding pattern instead
// of a `const x = y.z` initializer. Ported from ts-colors.mjs's
// destructuredPatternUsesSafe (CAP-E2, parity) — without this, a
// destructured extraction consumed only as a JSX tag (GoalPillTray's
// `const { Icon } = config` feeding `<Icon/>`) is neither a further
// `.member` read nor a JSX tag name itself, so it blanket-blocked every
// OTHER, unrelated property read off the SAME record (`config.accentClass`)
// purely because destructuring wasn't a recognized safe use at all.
function destructuredPatternUsesSafe(pattern) {
  if (!ts.isObjectBindingPattern(pattern)) return false
  for (const element of pattern.elements) {
    if (element.dotDotDotToken || !ts.isIdentifier(element.name) || element.initializer) return false
    if (!absenceBindingUsesSafe(element, false, true)) return false
  }
  return true
}

// A receiver may only be read through properties; a factory may only be
// called. Whole-object references, aliases, writes, deletes, increments and
// calls through its members escape the proof. Scanning the enclosing block
// also catches writes AFTER the sink, not just before it.
function absenceBindingUsesSafe(declaration, factory, indexedReads = false) {
  if (!declaration.name || !ts.isIdentifier(declaration.name)) return false
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  const visit = (node) => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
      const parent = node.parent
      if (ts.isImportSpecifier(parent) && parent === declaration) return
      if (factory) {
        if (!ts.isCallExpression(parent) || parent.expression !== node) { safe = false; return }
      } else {
        if (isJsxTagName(node)) return
        if (ts.isVariableDeclaration(parent) && parent.initializer === node && destructuredPatternUsesSafe(parent.name)) return
        if (isReadonlyObjectStaticCallArgument(node)) return
        if ((!ts.isPropertyAccessExpression(parent) && !ts.isElementAccessExpression(parent)) || parent.expression !== node) { safe = false; return }
        const argument = ts.isElementAccessExpression(parent) && parent.argumentExpression ? unwrapStatic(parent.argumentExpression) : null
        const property = ts.isPropertyAccessExpression(parent) ? parent.name.text
          : argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument)) ? argument.text : null
        if ((!property && !indexedReads) || ['__proto__', 'prototype', 'constructor'].includes(property)) { safe = false; return }
        let expression = parent
        while (expression.parent && (ts.isPropertyAccessExpression(expression.parent)
          || ts.isElementAccessExpression(expression.parent) || ts.isParenthesizedExpression(expression.parent)
          || ts.isAsExpression(expression.parent) || ts.isNonNullExpression(expression.parent)
          || ts.isObjectLiteralExpression(expression.parent) || ts.isArrayLiteralExpression(expression.parent)
          || ts.isPropertyAssignment(expression.parent) || ts.isSpreadElement(expression.parent))) expression = expression.parent
        const use = expression.parent
        if ((ts.isBinaryExpression(use) && use.left === expression && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
          || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
          || ts.isDeleteExpression(use) || ts.isPrefixUnaryExpression(use) || ts.isPostfixUnaryExpression(use)
          || (ts.isCallExpression(use) && use.expression === expression)) { safe = false; return }
      }
    }
    node.forEachChild(visit)
  }
  visit(scope)
  return safe
}

// ---------------------------------------------------------------------------
// Derived-value escape proof (round 3). Ported from ts-colors.mjs's
// knownClassDerivedUsesSafe/primitiveLeaf/resolveMemberTargets — the leg
// absenceBindingUsesSafe above deliberately does NOT cover: a nested
// (non-primitive) value read off an already-trusted record is exactly as
// mutable, through the SAME record binding, as a direct member write.
// `const list = [REC.a]; list[0].cls = 'text-[10px]'` hands out a LIVE
// reference to REC's own nested `{ cls }` object the instant `REC.a` is
// evaluated — nothing about a property-write proof scanning for
// assignment/delete/call syntax on the RECEIVER traces where that reference
// is handed off to (cross-scanner-false-green.test.mjs "array alias
// mutation of a nested object", the one remaining shared-suite failure this
// round closes).
//
// A fixed literal value can never carry a mutable reference back to
// anything (parity with ts-colors' primitiveLeaf base case) — a
// ternary/`??`/`||` chain composed entirely of such leaves is exactly as
// immutable. Deliberately narrower than absenceValue/classCarryingLeaves:
// an object/array literal is NOT a safe leaf here, unlike those proofs —
// it can still be captured by a separate alias and mutated elsewhere, which
// is exactly the escape this proof exists to catch.
function primitiveLeaf(value) {
  value = unwrapStatic(value)
  if (!value) return false
  if (ts.isStringLiteralLike(value) || ts.isNumericLiteral(value) || ts.isTemplateExpression(value)
    || [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)) return true
  if (ts.isConditionalExpression(value)) return primitiveLeaf(value.whenTrue) && primitiveLeaf(value.whenFalse)
  if (ts.isBinaryExpression(value) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(value.operatorToken.kind)) {
    return primitiveLeaf(value.left) && primitiveLeaf(value.right)
  }
  return false
}

// The static string/numeric key a single property/element access step
// selects, or null when the key is dynamic/computed (unresolvable at that
// step — derivedMemberTargets below enumerates every value in that case,
// mirroring ts-colors' resolveMemberTargets dynamic-key fallback).
function memberAccessKey(member) {
  if (ts.isPropertyAccessExpression(member)) return member.name.text
  const argument = member.argumentExpression ? unwrapStatic(member.argumentExpression) : null
  return argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument) || ts.isNumericLiteral(argument)) ? argument.text : null
}

// Structural (safety-independent) resolution of ONE static-or-dynamic
// property/element key step against a set of candidate container value
// nodes, returning the resolved target node(s). A spread anywhere in a
// candidate container, an omitted array element, or a non-container
// candidate reached mid-chain all fail closed to null — always treated by
// the caller as "not proven primitive" (requires capture), never as
// absence. A dynamic key (key === null) enumerates every value the
// container holds, the same live-reference risk Object.values/entries carry
// (own doc comment on NESTED_REFERENCE_RETURNING_STATIC_METHODS in
// ts-colors.mjs) — and the same reason a real dynamic-key record read like
// `STATUS_BADGE[status].dot` must still resolve safely when its OWN further
// static key (`dot`) is primitive on every branch.
function derivedMemberTargets(containers, key) {
  const next = []
  for (const container of containers) {
    const resolved = unwrapStatic(container)
    if (!resolved) return null
    if (ts.isObjectLiteralExpression(resolved)) {
      if (resolved.properties.some((property) => ts.isSpreadAssignment(property))) return null
      if (key === null) {
        for (const property of resolved.properties) {
          if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
          next.push(ts.isPropertyAssignment(property) ? property.initializer : property.name)
        }
        continue
      }
      const value = objectProperty(resolved, key)
      if (value) { next.push(value); continue }
      // Proven-absent standard, parity with absenceValue elsewhere in this
      // file (the LEAD DECISION ts-colors null-prototype standard): a
      // property missing from a plain object literal is provably nothing
      // to escape ONLY when the literal itself proves a null prototype.
      if (absenceValue(resolved, key)) continue
      // CAP-G (ported from ts-colors.mjs's resolution.incomplete /
      // knownClassDerivedUsesSafe): an unprovable absence on THIS ONE
      // container — a property genuinely omitted on some branch of a
      // finite union, but without the null-prototype proof above — is a
      // value-completeness concern already independently enforced at that
      // property's OWN emission site (resolveProvenPropertyAccess's
      // missingLeaf/absenceValue gate; still correctly unsupported there,
      // unaffected by this change). It is not evidence this receiver could
      // be mutated, so it must not discard whatever OTHER containers in the
      // same union already proved primitive and blanket-block an unrelated,
      // fully-resolvable sibling property purely because this OTHER
      // property couldn't prove absence on every branch (GoalPillTray's
      // `config.pulse`, omitted on most of describePillState's switch
      // branches with no `__proto__: null`, used to block the
      // fully-resolvable sibling `config.accentClass`). Skip this
      // container's contribution — same as the proven-absent branch above —
      // rather than failing the whole chain closed; a genuinely
      // non-container/spread/computed shape below still fails closed
      // unchanged.
      continue
    }
    if (ts.isArrayLiteralExpression(resolved)) {
      if (resolved.elements.some((element) => ts.isSpreadElement(element))) return null
      if (key !== null && /^\d+$/.test(key)) {
        const element = resolved.elements[Number(key)]
        if (element && !ts.isOmittedExpression(element)) next.push(element)
        else if (element) return null // an omitted array hole is unresolved, not proven-empty
        continue // an out-of-bounds index on a literal array is provably nothing
      }
      for (const element of resolved.elements) {
        if (ts.isOmittedExpression(element)) return null
        next.push(element)
      }
      continue
    }
    return null
  }
  return next
}

// EVERY occurrence of `declaration`'s name used in a property/element-access
// chain whose resolved target(s) (traced structurally from `containers` —
// the record value(s) this declaration currently, provably, holds) are not
// ALL primitive leaves must be captured — immediately, with nothing else in
// between but a `??`/ternary fallback wrapper — by a fresh `const` variable
// declaration whose OWN downstream uses recursively pass this same proof.
// Placed into an array/object/Map/Set literal, passed as a call argument,
// returned, assigned to an existing binding, or spread are all NOT that
// shape (the climb below stops at the first non-property/element-access
// parent, and the capture check that follows requires that parent to be
// exactly a fresh `const` VariableDeclaration initializer) and fail closed.
// Consumed purely as a JSX tag name is the one terminal-render exception
// (isJsxTagName), matching the JSX-tag carve-out absenceBindingUsesSafe
// already grants member reads.
//
// `targets === null` (derivedMemberTargets hit something structurally
// unresolvable) always requires capture — conservative, "when in doubt,
// treat it as an escape". `targets.length === 0`, by contrast, is ONLY ever
// produced by derivedMemberTargets when every step that found nothing was
// INDEPENDENTLY proven absent (the same null-prototype standard
// absenceValue already enforces elsewhere in this file) — there is
// genuinely nothing there to escape, so this does NOT require capture.
// This is a deliberate, narrower reading than ts-colors.mjs's
// knownClassDerivedUsesSafe (whose own resolveMemberTargets can return an
// empty array for reasons OTHER than proven absence, so it must keep
// `targets.length === 0` as a trigger) — needed here because typography's
// resolveProvenPropertyAccess funnels its OWN already-proven-absent reads
// (missingLeaf && absenceValue, e.g. "a helper whose branches ALL prove the
// read property absent resolves as a safe no-op") through this same
// `owner.declaration` gate; without this, a genuinely absent property was
// wrongly relabelled unsupported by the derived-value escape proof itself.
function derivedValueEscapeSafe(declaration, containers, seen = new Set()) {
  if (!declaration.name || !ts.isIdentifier(declaration.name) || seen.has(declaration)) return false
  const next = new Set(seen).add(declaration)
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  const visit = (node) => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === declaration.name.text && node !== declaration.name) {
      let member = node.parent
      if ((ts.isPropertyAccessExpression(member) || ts.isElementAccessExpression(member)) && member.expression === node) {
        const keys = [memberAccessKey(member)]
        while ((ts.isPropertyAccessExpression(member.parent) || ts.isElementAccessExpression(member.parent)) && member.parent.expression === member) {
          member = member.parent
          keys.push(memberAccessKey(member))
        }
        let targets = containers
        for (const key of keys) targets = targets ? derivedMemberTargets(targets, key) : null
        if ((!targets || (targets.length > 0 && !targets.every((value) => primitiveLeaf(value)))) && !isJsxTagName(member)) {
          let derived = member
          while ((ts.isBinaryExpression(derived.parent) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(derived.parent.operatorToken.kind))
            || (ts.isConditionalExpression(derived.parent) && (derived.parent.whenTrue === derived || derived.parent.whenFalse === derived))) derived = derived.parent
          const alias = derived.parent
          if (!ts.isVariableDeclaration(alias) || alias.initializer !== derived
            || !(alias.parent.flags & ts.NodeFlags.Const) || !absenceBindingUsesSafe(alias, false, true)
            || !targets || !derivedValueEscapeSafe(alias, targets, next)) { safe = false; return }
        }
      }
    }
    node.forEachChild(visit)
  }
  visit(scope)
  return safe
}

function absenceFactory(callee) {
  if (!ts.isIdentifier(callee)) return null
  const declaration = absenceLexicalBinding(callee)
  if (!declaration || !absenceBindingUsesSafe(declaration, true)) return null
  if (!ts.isFunctionDeclaration(declaration) || !ts.isSourceFile(declaration.parent)
    || !declaration.body || declaration.asteriskToken
    || declaration.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword)) return null
  return declaration
}

function absenceValue(node, property) {
  node = unwrapStatic(node)
  if (ts.isObjectLiteralExpression(node)) {
    const nullPrototype = node.properties.some((member) => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name) && propertyKeyName(member) === '__proto__'
      && unwrapStatic(member.initializer).kind === ts.SyntaxKind.NullKeyword)
    return nullPrototype && node.properties.every((member) => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name)
      && propertyKeyName(member) !== property
      && (propertyKeyName(member) !== '__proto__' || unwrapStatic(member.initializer).kind === ts.SyntaxKind.NullKeyword))
  }
  if (ts.isConditionalExpression(node)) {
    return absenceValue(node.whenTrue, property) && absenceValue(node.whenFalse, property)
  }
  if (!ts.isCallExpression(node)) return false
  const declaration = absenceFactory(unwrapStatic(node.expression))
  if (!declaration) return false
  const returns = []
  const visit = (child) => {
    if (ts.isFunctionLike(child) && child !== declaration) return
    if (ts.isReturnStatement(child)) returns.push(child.expression)
    else child.forEachChild(visit)
  }
  visit(declaration.body)
  // Returned calls/aliases are opaque, so recursive functions never recurse
  // through this proof. Direct conditional literals cover every returned arm.
  const fresh = (value) => {
    value = value && unwrapStatic(value)
    if (!value) return false
    if (ts.isConditionalExpression(value)) return fresh(value.whenTrue) && fresh(value.whenFalse)
    return ts.isObjectLiteralExpression(value) && absenceValue(value, property)
  }
  return returns.length > 0 && returns.every(fresh)
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
