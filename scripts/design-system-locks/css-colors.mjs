// css-colors — syntax-aware CSS raw-color lock for the design-system enforcement
// suite (design-system/enforcement/contract.json).
//
// Detects colors that are not registered design tokens: hex literals, named
// colors (including system colors), and raw color functions (rgb/hsl/oklch/…)
// in any declaration value, including custom property declarations and var()
// fallbacks. In color-valued positions (color properties and color functions
// such as gradients) a var() reference to a name missing from
// policy.tokenCssNames is reported as an unregistered token. Paint embedded in
// data:image/svg+xml url() payloads is decoded (percent or base64) and scanned
// like any other paint; binary image data URIs fail closed as unsupported.
// Registered CSS token references via var() are permitted. Strings, comments,
// and non-data url() contents are never colors. Token-graph checks beyond
// color-position registration (cycles, namespace edges, layer edges) belong to
// the Stage A tokens checker and are composed by the orchestrator.
//
// At-rule CONDITION text (AtRule.params) is scanned too, not just declaration
// values: @supports and @container conditions are grammatically built from
// `<ident-or-custom-prop>: <value>` feature tests (optionally wrapped in
// `not(...)`/`style(...)`/combined with and/or), and that value position is
// analyzed with the exact same walkValueTokens used for declarations — never a
// second, weaker matcher. `selector(...)` arguments are skipped outright (a
// selector, never a color position). Every other at-rule's params — keyframe
// selectors, page selectors, `@theme`/`@utility`/`@property` identifiers,
// `@import`/`@charset` strings, layer names, and so on — is not colour-bearing
// by grammar and is not walked, so it never manufactures unsupported-noise on
// ordinary CSS. Within a walked condition, plain boolean/range feature tests
// (e.g. `(min-width: 640px)`, `(color)`) have no colon-paired value and are
// left alone — no current CSS media/container feature takes a color value —
// while a genuinely malformed condition (unbalanced parentheses, an
// unrecognizable declaration shape) fails closed as css-colors/unsupported.
//
// Fail-closed: PostCSS parse failures become explicit css-colors/parse-error
// findings, unrecognized #-prefixed tokens and undecodable data URIs become
// css-colors/unsupported findings. The scanner never returns empty success for
// input it could not analyze. Findings use the shared scanner API; policy and
// exceptions are the orchestrator's responsibility — nothing is skipped by path.

import postcss from 'postcss'

export const extensions = ['.css']

const RULE_RAW_COLOR = 'css-colors/raw-color'
const RULE_UNREGISTERED = 'css-colors/unregistered-token'
const RULE_UNSUPPORTED = 'css-colors/unsupported'
const RULE_PARSE_ERROR = 'css-colors/parse-error'

const FRAME_SCAN = 'scan'
const FRAME_VAR_NAME = 'var-name'
const FRAME_VAR_ARGS = 'var-args'
const FRAME_COLOR = 'color'

/** CSS-wide and paint keywords that are value plumbing, not palette choices. */
const COLOR_KEYWORD_EXEMPTS = new Set([
  'currentcolor', 'transparent', 'inherit', 'initial', 'unset', 'revert', 'revert-layer', 'none',
])

/** Functions that always produce a color literal (unless relative-color form). */
const ALWAYS_RAW_COLOR_FUNCTIONS = new Set([
  'rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color', 'device-cmyk',
])

/** Functions whose arguments are color positions, for the unregistered-token check. */
const COLOR_CONTEXT_FUNCTIONS = new Set([
  'linear-gradient', 'radial-gradient', 'conic-gradient',
  'repeating-linear-gradient', 'repeating-radial-gradient', 'repeating-conic-gradient',
  'color-mix', 'light-dark',
])

/**
 * At-rules whose condition text is grammatically built from feature tests —
 * `<ident-or-custom-prop>: <value>`, optionally wrapped in not()/style() or
 * combined with and/or — so a color can legitimately appear there. Every other
 * at-rule's params (keyframe selectors, page selectors, `@theme`/`@utility`/
 * `@property` identifiers, `@import`/`@charset` strings, layer names, …) is a
 * name or selector list, never a color position, and is deliberately left
 * unwalked so it can never manufacture unsupported-noise on ordinary CSS.
 */
const CONDITION_AT_RULES = new Set(['supports', 'container', 'media'])

/** Normalized properties whose var() references are color positions. */
const COLOR_CONTEXT_PROPERTIES = new Set([
  'color', 'accent-color', 'caret-color', 'background', 'background-color',
  'border', 'border-color', 'border-top', 'border-top-color', 'border-right', 'border-right-color',
  'border-bottom', 'border-bottom-color', 'border-left', 'border-left-color',
  'border-block', 'border-block-color', 'border-block-start', 'border-block-start-color',
  'border-block-end', 'border-block-end-color', 'border-inline', 'border-inline-color',
  'border-inline-start', 'border-inline-start-color', 'border-inline-end', 'border-inline-end-color',
  'outline', 'outline-color', 'text-decoration', 'text-decoration-color', 'text-emphasis-color',
  'box-shadow', 'text-shadow', 'column-rule', 'column-rule-color',
  'fill', 'stroke', 'flood-color', 'lighting-color', 'stop-color',
  'text-fill-color', 'tap-highlight-color', 'scrollbar-color',
])

/** SVG presentation attributes whose values are paint colors. */
const SVG_PAINT_ATTRIBUTES = new Set([
  'fill', 'stroke', 'stop-color', 'color', 'flood-color', 'lighting-color',
])

/** Canonical-name folding for aliased legacy functions. */
const FUNCTION_ALIASES = new Map([['rgba', 'rgb'], ['hsla', 'hsl']])

/** Properties whose bare identifiers are names (keyframes, areas, fonts), not colors. */
const IDENT_CONTEXT_PROPERTIES = new Set([
  'animation', 'animation-name', 'animation-timeline', 'transition', 'transition-property',
  'will-change', 'grid-template-areas', 'grid-area', 'grid-column', 'grid-row', 'container-name',
  'counter-increment', 'counter-reset', 'counter-set', 'font-family', 'font',
  'scroll-timeline-name', 'view-timeline-name', 'timeline-scope', 'view-transition-name', 'anchor-name',
])

/** CSS Color 4 extended color keywords plus system colors. */
const NAMED_COLORS = new Set([
  'aliceblue', 'antiquewhite', 'aqua', 'aquamarine', 'azure', 'beige', 'bisque', 'black',
  'blanchedalmond', 'blue', 'blueviolet', 'brown', 'burlywood', 'cadetblue', 'chartreuse',
  'chocolate', 'coral', 'cornflowerblue', 'cornsilk', 'crimson', 'cyan', 'darkblue', 'darkcyan',
  'darkgoldenrod', 'darkgray', 'darkgreen', 'darkgrey', 'darkkhaki', 'darkmagenta',
  'darkolivegreen', 'darkorange', 'darkorchid', 'darkred', 'darksalmon', 'darkseagreen',
  'darkslateblue', 'darkslategray', 'darkslategrey', 'darkturquoise', 'darkviolet', 'deeppink',
  'deepskyblue', 'dimgray', 'dimgrey', 'dodgerblue', 'firebrick', 'floralwhite', 'forestgreen',
  'fuchsia', 'gainsboro', 'ghostwhite', 'gold', 'goldenrod', 'gray', 'green', 'greenyellow',
  'grey', 'honeydew', 'hotpink', 'indianred', 'indigo', 'ivory', 'khaki', 'lavender',
  'lavenderblush', 'lawngreen', 'lemonchiffon', 'lightblue', 'lightcoral', 'lightcyan',
  'lightgoldenrodyellow', 'lightgray', 'lightgreen', 'lightgrey', 'lightpink', 'lightsalmon',
  'lightseagreen', 'lightskyblue', 'lightslategray', 'lightslategrey', 'lightsteelblue',
  'lightyellow', 'lime', 'limegreen', 'linen', 'magenta', 'maroon', 'mediumaquamarine',
  'mediumblue', 'mediumorchid', 'mediumpurple', 'mediumseagreen', 'mediumslateblue',
  'mediumspringgreen', 'mediumturquoise', 'mediumvioletred', 'midnightblue', 'mintcream',
  'mistyrose', 'moccasin', 'navajowhite', 'navy', 'oldlace', 'olive', 'olivedrab', 'orange',
  'orangered', 'orchid', 'palegoldenrod', 'palegreen', 'paleturquoise', 'palevioletred',
  'papayawhip', 'peachpuff', 'peru', 'pink', 'plum', 'powderblue', 'purple', 'rebeccapurple',
  'red', 'rosybrown', 'royalblue', 'saddlebrown', 'salmon', 'sandybrown', 'seagreen', 'seashell',
  'sienna', 'silver', 'skyblue', 'slateblue', 'slategray', 'slategrey', 'snow', 'springgreen',
  'steelblue', 'tan', 'teal', 'thistle', 'tomato', 'turquoise', 'violet', 'wheat', 'white',
  'whitesmoke', 'yellow', 'yellowgreen',
  'canvas', 'canvastext', 'linktext', 'visitedtext', 'activetext', 'buttonface', 'buttontext',
  'buttonborder', 'field', 'fieldtext', 'highlight', 'highlighttext', 'itemtext', 'mark',
  'marktext', 'graytext',
])

/**
 * The CSS Color Module Level 4 system color keywords (lowercase). These
 * resolve to the user's OS palette and are the only legitimate use of a
 * "raw color" keyword — but ONLY inside a `@media (forced-colors: active)`
 * block (see isInForcedColorsActiveMedia). The same keyword anywhere else
 * (including bare, or inside `@media (forced-colors: none)` / unconditional
 * CSS) remains raw-color debt: nothing about the keyword itself is exempt,
 * only its use in the one context where its OS-palette behavior is the
 * point. `AccentColor`/`AccentColorText` are included for completeness even
 * though they are not yet used anywhere in the codebase.
 */
const SYSTEM_COLOR_KEYWORDS = new Set([
  'canvas', 'canvastext', 'linktext', 'visitedtext', 'activetext',
  'buttonface', 'buttontext', 'buttonborder', 'field', 'fieldtext',
  'highlight', 'highlighttext', 'selecteditem', 'selecteditemtext',
  'mark', 'marktext', 'graytext', 'accentcolor', 'accentcolortext',
])

const HEX_LENGTHS = new Set([3, 4, 6, 8])

/** Splits a declaration value into offset-carrying tokens. Offsets are 0-based character indexes into the value string. */
function tokenizeValue(value) {
  const tokens = []
  let start = -1
  const pushWord = (end) => {
    if (start !== -1) tokens.push({ kind: 'word', text: value.slice(start, end), start, end })
    start = -1
  }
  for (let i = 0; i < value.length; i += 1) {
    const char = value[i]
    if (char === '/' && value[i + 1] === '*') {
      pushWord(i)
      const close = value.indexOf('*/', i + 2)
      const end = close === -1 ? value.length : close + 2
      tokens.push({ kind: 'comment', text: value.slice(i, end), start: i, end })
      i = end - 1
      continue
    }
    if (char === '"' || char === "'") {
      pushWord(i)
      let j = i + 1
      while (j < value.length && value[j] !== char) j += value[j] === '\\' ? 2 : 1
      const end = Math.min(j + 1, value.length)
      tokens.push({ kind: 'string', text: value.slice(i, end), start: i, end })
      i = end - 1
      continue
    }
    if (isWordChar(char, value[i + 1])) {
      if (start === -1) start = i
      if (char === '\\') i += 1
      continue
    }
    pushWord(i)
    if (char === '(' || char === ')' || char === ',') {
      tokens.push({ kind: char === '(' ? 'lparen' : char === ')' ? 'rparen' : 'comma', text: char, start: i, end: i + 1 })
    } else if (!/\s/.test(char)) {
      tokens.push({ kind: 'operator', text: char, start: i, end: i + 1 })
    }
  }
  pushWord(value.length)
  return tokens
}

function isWordChar(char, next) {
  if (/[A-Za-z0-9_.%+-]/.test(char) || char.charCodeAt(0) >= 0x80) return true
  if (char === '\\') return true
  return char === '#' && next !== undefined && (/[A-Za-z0-9_\-.%+-]/.test(next) || next.charCodeAt(0) >= 0x80)
}

/** Prints tokens back to a canonical form: lowercase identifiers, commas attached, single spaces elsewhere. */
function canonicalJoin(tokens) {
  let out = ''
  for (const token of tokens) {
    if (token.kind === 'comma') {
      out += ','
      continue
    }
    if (token.kind === 'comment') continue
    const needsSpace = out.length > 0 && !out.endsWith('(') && !out.endsWith(',')
    const text = token.kind === 'word' ? token.text.toLowerCase() : token.text
    out += (needsSpace ? ' ' : '') + text
  }
  return out
}

function matchingParenIndex(tokens, openIndex) {
  let depth = 0
  for (let i = openIndex; i < tokens.length; i += 1) {
    if (tokens[i].kind === 'lparen') depth += 1
    if (tokens[i].kind === 'rparen') {
      depth -= 1
      if (depth === 0) return i
    }
  }
  return -1
}

/** Lowercases a property and strips a vendor prefix so denylist entries match prefixed forms. */
function normalizeProperty(prop) {
  return prop.toLowerCase().replace(/^-(?:webkit|moz|ms|o)-/, '')
}

function isIdentContextProperty(prop) {
  return IDENT_CONTEXT_PROPERTIES.has(normalizeProperty(prop))
}

function isColorContextProperty(prop) {
  return COLOR_CONTEXT_PROPERTIES.has(normalizeProperty(prop))
}

function isRelativeColorForm(tokens, lparenIndex) {
  const first = tokens[lparenIndex + 1]
  return first?.kind === 'word' && first.text.toLowerCase() === 'from'
}

/**
 * Walks one value string (a declaration value or an SVG paint attribute value)
 * and reports raw colors, unregistered color-position var() references, and
 * unsupported tokens through `report(ruleId, syntax, token)`.
 */
function walkValueTokens({ prop, value, identContext, colorDepth, ctx, messages, report, forcedColorsActive = false }) {
  const tokens = tokenizeValue(value)
  const frames = []
  let depth = colorDepth
  const emit = (ruleId, syntax, token) => report(ruleId, syntax, token, messages(ruleId, syntax))
  for (let i = 0; i < tokens.length; i += 1) {
    const token = tokens[i]
    if (token.kind !== 'word') {
      if (token.kind === 'rparen' && frames.pop() === FRAME_COLOR) depth -= 1
      continue
    }
    if (tokens[i + 1]?.kind === 'lparen') {
      const name = token.text.toLowerCase()
      const closeIndex = matchingParenIndex(tokens, i + 1)
      if (name === 'url' && closeIndex !== -1) {
        analyzeUrlContent(value.slice(tokens[i + 1].end, tokens[closeIndex].start), ctx, prop, emit, token)
        i = closeIndex
        continue
      }
      if (ALWAYS_RAW_COLOR_FUNCTIONS.has(name) && closeIndex !== -1 && !isRelativeColorForm(tokens, i + 1)) {
        const canonicalName = FUNCTION_ALIASES.get(name) ?? name
        emit(
          RULE_RAW_COLOR,
          `${canonicalName}(${canonicalJoin(tokens.slice(i + 2, closeIndex))})`,
          token,
        )
        i = closeIndex
        continue
      }
      if (closeIndex === -1 || name === 'var' || !COLOR_CONTEXT_FUNCTIONS.has(name)) {
        frames.push(closeIndex !== -1 && name === 'var' ? FRAME_VAR_NAME : FRAME_SCAN)
        continue
      }
      frames.push(FRAME_COLOR)
      depth += 1
      continue
    }
    const frame = frames[frames.length - 1]
    if (frame === FRAME_VAR_NAME) {
      if (depth > 0 && !ctx.tokenSet.has(token.text)) {
        emit(RULE_UNREGISTERED, `var(${token.text})`, token)
      }
      frames[frames.length - 1] = FRAME_VAR_ARGS
      continue
    }
    checkWordToken(token, identContext, emit, forcedColorsActive)
  }
}

function checkWordToken(token, identContext, report, forcedColorsActive = false) {
  const text = token.text
  if (text.startsWith('#')) {
    if (/^#[0-9a-fA-F]+$/.test(text) && HEX_LENGTHS.has(text.length - 1)) {
      report(RULE_RAW_COLOR, text.toLowerCase(), token)
    } else {
      report(RULE_UNSUPPORTED, text, token)
    }
    return
  }
  if (text.startsWith('--')) return
  const lower = text.toLowerCase()
  if (COLOR_KEYWORD_EXEMPTS.has(lower)) return
  if (!NAMED_COLORS.has(lower) || identContext) return
  if (forcedColorsActive && SYSTEM_COLOR_KEYWORDS.has(lower)) return
  report(RULE_RAW_COLOR, lower, token)
}

/**
 * Walks an at-rule's condition text (AtRule.params) for @supports, @container
 * and @media, whose grammar is built from feature tests that pair a bare
 * identifier or custom-property name with a value via ':' — the exact shape
 * a declaration has. `emitAt(ruleId, syntax, offset, message)` reports at an
 * offset into `params`; callers translate that to a file line/column.
 */
function walkAtRuleCondition(params, ctx, emitAt, forcedColorsActive = false) {
  const tokens = tokenizeValue(params)
  walkConditionTokens(tokens, 0, tokens.length, params, ctx, emitAt, forcedColorsActive)
}

/** Scans a flat run of condition tokens for parenthesized feature groups. */
function walkConditionTokens(tokens, from, to, fullText, ctx, emitAt, forcedColorsActive) {
  const unbalanced = (syntax, offset) => emitAt(
    RULE_UNSUPPORTED,
    syntax,
    offset,
    `Unbalanced parentheses in at-rule condition (${syntax}); the condition cannot be analyzed for embedded colors. Fail-closed report, not a pass.`,
  )
  for (let i = from; i < to; i += 1) {
    const token = tokens[i]
    if (token.kind === 'rparen') {
      unbalanced(`unbalanced parentheses in condition: ${truncate(fullText, 60)}`, token.start)
      return
    }
    if (token.kind === 'lparen') {
      const close = matchingParenIndex(tokens, i)
      if (close === -1 || close >= to) {
        unbalanced(`unbalanced parentheses in condition: ${truncate(fullText, 60)}`, token.start)
        return
      }
      walkConditionGroup(tokens, i + 1, close, fullText, ctx, emitAt, forcedColorsActive)
      i = close
      continue
    }
    if (token.kind === 'word' && tokens[i + 1]?.kind === 'lparen') {
      const lower = token.text.toLowerCase()
      const close = matchingParenIndex(tokens, i + 1)
      if (close === -1 || close >= to) {
        unbalanced(`unbalanced parentheses in condition: ${truncate(fullText, 60)}`, token.start)
        return
      }
      // `selector(...)` takes a CSS selector, never a color position — every other
      // word-then-'(' adjacency here (style(), not(), and/or combinators, or a
      // container-name/media-type immediately preceding its condition — whitespace
      // is invisible to the tokenizer, so these are indistinguishable from a
      // "function call" by token shape alone) is safe to recurse into: the group
      // walk below only ever reports a genuine single-identifier ':' declaration,
      // so recursing never manufactures a finding from ordinary combinator text.
      if (lower !== 'selector') walkConditionGroup(tokens, i + 2, close, fullText, ctx, emitAt, forcedColorsActive)
      i = close
      continue
    }
  }
}

/** Scans the content of one parenthesized condition group for a `prop: value` feature test. */
function walkConditionGroup(tokens, from, to, fullText, ctx, emitAt, forcedColorsActive) {
  if (from >= to) return
  let colonIdx = -1
  for (let k = from; k < to; k += 1) {
    if (tokens[k].kind === 'operator' && tokens[k].text === ':') {
      colonIdx = k
      break
    }
    if (tokens[k].kind === 'lparen') break
  }
  if (colonIdx === -1) {
    // No colon at this level: either a nested condition (recurse) or a boolean/range
    // feature test (`(color)`, `(width >= 400px)`) — no current CSS media or
    // container feature takes a color value, so that case is left clean, not noise.
    const hasNested = tokens.slice(from, to)
      .some((t) => t.kind === 'lparen' || (t.kind === 'word' && ['and', 'or', 'not'].includes(t.text.toLowerCase())))
    if (hasNested) walkConditionTokens(tokens, from, to, fullText, ctx, emitAt, forcedColorsActive)
    return
  }
  const propTokens = tokens.slice(from, colonIdx).filter((t) => t.kind === 'word')
  if (propTokens.length !== 1) {
    const syntax = `unrecognized condition shape: ${truncate(fullText.slice(tokens[from].start, tokens[to - 1].end), 60)}`
    emitAt(
      RULE_UNSUPPORTED,
      syntax,
      tokens[from].start,
      `Unrecognized at-rule condition shape (${syntax}); cannot verify whether it carries a color. Fail-closed report, not a pass.`,
    )
    return
  }
  const prop = propTokens[0].text
  const valueStart = tokens[colonIdx + 1]?.start
  const valueEnd = tokens[to - 1]?.end
  if (valueStart === undefined || valueEnd === undefined || valueStart >= valueEnd) return
  const value = fullText.slice(valueStart, valueEnd)
  walkValueTokens({
    prop,
    value,
    identContext: isIdentContextProperty(prop),
    colorDepth: isColorContextProperty(prop) ? 1 : 0,
    ctx,
    forcedColorsActive,
    messages: atRuleConditionMessages(prop, value),
    report: (ruleId, syntax, token, message) => emitAt(ruleId, syntax, valueStart + (token?.start ?? 0), message),
  })
}

function atRuleConditionMessages(prop, value) {
  const trunc = truncate(value, 60)
  return (ruleId, syntax) => {
    if (ruleId === RULE_RAW_COLOR) {
      return `Raw color ${syntax} in at-rule condition "${prop}" (value: ${trunc}); reference a registered CSS token via var(--…) or register an exception.`
    }
    if (ruleId === RULE_UNREGISTERED) {
      return `Unregistered CSS token reference ${syntax} in at-rule color condition "${prop}" (value: ${trunc}); reference a registered token from the token graph or register it centrally.`
    }
    if (ruleId === RULE_UNSUPPORTED) {
      return `Unrecognized #-prefixed token ${syntax} in at-rule condition "${prop}"; not a valid hex length (3, 4, 6, or 8 digits). Fail-closed report, not a pass.`
    }
    return `Data URI in at-rule condition "${prop}" could not be decoded (${syntax}); embedded paint cannot be verified — fail-closed report, not a pass.`
  }
}

/** Strips one matching pair of surrounding quotes, if present. */
function stripMatchingQuotes(text) {
  const trimmed = text.trim()
  if (trimmed.length >= 2
    && ((trimmed[0] === '"' && trimmed.endsWith('"')) || (trimmed[0] === "'" && trimmed.endsWith("'")))) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

/** Percent-decodes a data URI payload; returns null on any invalid escape (strict). */
function decodePercentStrict(text) {
  let out = ''
  for (let i = 0; i < text.length; i += 1) {
    const char = text[i]
    if (char !== '%') {
      out += char
      continue
    }
    const hex = text.slice(i + 1, i + 3)
    if (!/^[0-9a-fA-F]{2}$/.test(hex)) return null
    out += String.fromCharCode(parseInt(hex, 16))
    i += 2
  }
  return out
}

/** Base64-decodes strictly (alphabet, padding, round-trip); returns null on any defect. */
function decodeBase64Strict(text) {
  const compact = text.replace(/\s+/g, '')
  if (compact.length === 0 || compact.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(compact)) return null
  const decoded = Buffer.from(compact, 'base64')
  if (decoded.toString('base64').replace(/=+$/, '') !== compact.replace(/=+$/, '')) return null
  return decoded.toString('utf8')
}

/**
 * Analyzes raw url() content (quoted or not). External references are clean at
 * this scanner boundary; data:image/svg+xml payloads are decoded and scanned,
 * other image data URIs fail closed because their paint cannot be verified.
 */
function analyzeUrlContent(rawContent, ctx, prop, report, urlToken) {
  const url = stripMatchingQuotes(rawContent)
  if (!/^data:/i.test(url)) return
  const commaIndex = url.indexOf(',')
  if (commaIndex === -1) {
    report(RULE_UNSUPPORTED, `data URI without payload: ${truncate(url, 40)}`, urlToken)
    return
  }
  const meta = url.slice(5, commaIndex).toLowerCase()
  const mime = meta.split(';')[0]
  if (mime !== 'image/svg+xml') {
    if (mime.startsWith('image/')) {
      report(RULE_UNSUPPORTED, `data URI ${mime}: binary image paint cannot be verified`, urlToken)
    }
    return
  }
  const markup = meta.endsWith(';base64') ? decodeBase64Strict(url.slice(commaIndex + 1)) : decodePercentStrict(url.slice(commaIndex + 1))
  if (markup === null || !markup.includes('<')) {
    report(RULE_UNSUPPORTED, `undecodable data URI ${truncate(meta, 30)} payload`, urlToken)
    return
  }
  scanSvgPaint(markup, ctx, report, urlToken)
}

/** Scans decoded SVG markup for paint attributes and style declarations, reporting through the url() position. */
function scanSvgPaint(markup, ctx, report, urlToken) {
  const parsed = eachSvgPaintDeclaration(markup, (attr, value) => {
    if (!value) return
    walkValueTokens({
      prop: attr,
      value,
      identContext: false,
      colorDepth: 1,
      ctx,
      messages: svgPaintMessages(attr),
      report: (ruleId, syntax, _token, message) => report(ruleId, syntax, urlToken, message),
    })
  })
  if (!parsed) {
    report(RULE_UNSUPPORTED, 'malformed data-URI SVG', urlToken)
  }
}

/** Visits paint declarations in document order and returns false when structural parsing is incomplete. */
function eachSvgPaintDeclaration(markup, visit) {
  const length = markup.length
  let i = 0
  while (i < length) {
    const open = markup.indexOf('<', i)
    if (open === -1) return true
    if (markup.startsWith('!--', open + 1)) {
      const end = markup.indexOf('-->', open)
      if (end === -1) return false
      i = end + 3
      continue
    }
    if (markup[open + 1] === '?' || markup[open + 1] === '!') {
      const end = markup.indexOf('>', open)
      if (end === -1) return false
      i = end + 1
      continue
    }
    let close = open + 1
    let quote = null
    while (close < length) {
      const char = markup[close]
      if (quote) {
        if (char === quote) quote = null
      } else if (char === '"' || char === "'") {
        quote = char
      } else if (char === '>') {
        break
      }
      close += 1
    }
    if (close >= length) return false
    if (!parseTagAttributes(markup.slice(open + 1, close), visit)) return false
    i = close + 1
  }
  return true
}

/** Parses one tag body, visiting paint attributes and returning false for an unterminated quoted value. */
function parseTagAttributes(tagBody, visit) {
  const length = tagBody.length
  let k = 0
  while (k < length && !/[\s/]/.test(tagBody[k])) k += 1
  while (k < length) {
    while (k < length && /[\s/]/.test(tagBody[k])) k += 1
    if (k >= length) break
    const nameStart = k
    while (k < length && !/[\s/=]/.test(tagBody[k])) k += 1
    const name = tagBody.slice(nameStart, k).toLowerCase()
    while (k < length && /\s/.test(tagBody[k])) k += 1
    if (tagBody[k] !== '=') {
      continue
    }
    k += 1
    while (k < length && /\s/.test(tagBody[k])) k += 1
    const quote = tagBody[k]
    let value
    if (quote === '"' || quote === "'") {
      const end = tagBody.indexOf(quote, k + 1)
      if (end === -1) return false
      value = tagBody.slice(k + 1, end)
      k = end + 1
    } else {
      const valueStart = k
      while (k < length && !/\s/.test(tagBody[k])) k += 1
      value = tagBody.slice(valueStart, k)
    }
    if (SVG_PAINT_ATTRIBUTES.has(name)) visit(name, value)
    else if (name === 'style') eachStylePaintDeclaration(value, visit)
  }
  return true
}

/** Splits a style attribute into declarations and visits the paint-valued ones. */
function eachStylePaintDeclaration(styleText, visit) {
  for (const chunk of styleText.split(';')) {
    const colon = chunk.indexOf(':')
    if (colon === -1) continue
    const prop = chunk.slice(0, colon).trim().toLowerCase()
    if (SVG_PAINT_ATTRIBUTES.has(prop)) visit(prop, chunk.slice(colon + 1).trim())
  }
}

function declarationMessages(prop, value) {
  const trunc = truncate(value, 60)
  return (ruleId, syntax) => {
    if (ruleId === RULE_RAW_COLOR) {
      return `Raw color ${syntax} in declaration "${prop}" (value: ${trunc}); reference a registered CSS token via var(--…) or register an exception.`
    }
    if (ruleId === RULE_UNREGISTERED) {
      return `Unregistered CSS token reference ${syntax} in color declaration "${prop}" (value: ${trunc}); reference a registered token from the token graph or register it centrally.`
    }
    if (ruleId === RULE_UNSUPPORTED) {
      if (syntax === 'malformed data-URI SVG') {
        return `Malformed decoded SVG in declaration "${prop}"; embedded paint cannot be verified — fail-closed report, not a pass.`
      }
      return `Unrecognized #-prefixed token ${syntax} in declaration "${prop}"; not a valid hex length (3, 4, 6, or 8 digits). Fail-closed report, not a pass.`
    }
    return `Data URI in declaration "${prop}" could not be decoded (${syntax}); embedded paint cannot be verified — fail-closed report, not a pass.`
  }
}

function svgPaintMessages(attr) {
  return (ruleId, syntax) => {
    if (ruleId === RULE_RAW_COLOR) {
      return `Raw color ${syntax} in embedded data-URI SVG paint attribute "${attr}"; reference a registered CSS token or register an exception for the asset.`
    }
    if (ruleId === RULE_UNREGISTERED) {
      return `Unregistered CSS token reference ${syntax} in embedded data-URI SVG paint attribute "${attr}"; reference a registered token from the token graph or register it centrally.`
    }
    return `Unrecognized color ${syntax} in embedded data-URI SVG paint attribute "${attr}"; not a valid hex length (3, 4, 6, or 8 digits). Fail-closed report, not a pass.`
  }
}

/** Converts a fixed prefix leading up to an offset into a 1-based display line/column. */
function positionFromPrefix(start, prefix) {
  if (!start) return { line: 1, column: 1 }
  const lastNewline = prefix.lastIndexOf('\n')
  if (lastNewline === -1) return { line: start.line, column: start.column + prefix.length }
  let newlines = 0
  for (let i = 0; i < prefix.length; i += 1) if (prefix[i] === '\n') newlines += 1
  return { line: start.line + newlines, column: prefix.length - lastNewline }
}

/** Maps a 0-based offset in decl.value to 1-based display line/column in the file. */
function makePositionMapper(decl) {
  const start = decl.source?.start
  const between = decl.raws.between ?? ': '
  return (offset) => positionFromPrefix(start, decl.prop + between + decl.value.slice(0, offset))
}

/** Maps a 0-based offset in atRule.params to 1-based display line/column in the file. */
function makeAtRulePositionMapper(atRule) {
  const start = atRule.source?.start
  const afterName = atRule.raws.afterName ?? (atRule.params ? ' ' : '')
  const prefix = `@${atRule.name}${afterName}`
  return (offset) => positionFromPrefix(start, prefix + atRule.params.slice(0, offset))
}

function truncate(text, limit) {
  return text.length > limit ? `${text.slice(0, limit)}…` : text
}

/**
 * Detects whether an @media condition text requires forced-colors: active —
 * i.e. the feature test `(forced-colors: active)` appears at some
 * conjunctive/disjunctive position that is not wrapped in a not(...)
 * negation. `@media (forced-colors: active)`, `screen and (forced-colors:
 * active)`, and `(forced-colors: active) or (color)` all qualify;
 * `@media (forced-colors: none)`, `@media not (forced-colors: active)`, and
 * an unrelated `@media (prefers-contrast: more)` do not. This mirrors
 * walkConditionTokens/walkConditionGroup's own recursive descent through
 * parens and and/or/not/style() combinators, but answers a yes/no structural
 * question instead of reporting colors.
 */
function hasActiveForcedColorsCondition(params) {
  const tokens = tokenizeValue(params)
  return scanForForcedColorsActive(tokens, 0, tokens.length, false)
}

function scanForForcedColorsActive(tokens, from, to, negated) {
  for (let i = from; i < to; i += 1) {
    const token = tokens[i]
    if (token.kind === 'lparen') {
      const close = matchingParenIndex(tokens, i)
      if (close === -1) return false
      if (!negated && isForcedColorsActiveFeature(tokens, i + 1, close)) return true
      if (scanForForcedColorsActive(tokens, i + 1, close, negated)) return true
      i = close
      continue
    }
    if (token.kind === 'word' && tokens[i + 1]?.kind === 'lparen') {
      const lower = token.text.toLowerCase()
      const close = matchingParenIndex(tokens, i + 1)
      if (close === -1) return false
      if (lower === 'selector') {
        i = close
        continue
      }
      const childNegated = negated || lower === 'not'
      if (!childNegated && isForcedColorsActiveFeature(tokens, i + 2, close)) return true
      if (scanForForcedColorsActive(tokens, i + 2, close, childNegated)) return true
      i = close
      continue
    }
  }
  return false
}

/** True when tokens[from..to) is exactly the feature test `forced-colors : active`. */
function isForcedColorsActiveFeature(tokens, from, to) {
  const inner = tokens.slice(from, to).filter((t) => t.kind !== 'comment')
  if (inner.length !== 3) return false
  const [propTok, colonTok, valTok] = inner
  if (propTok.kind !== 'word' || colonTok.kind !== 'operator' || colonTok.text !== ':' || valTok.kind !== 'word') {
    return false
  }
  return propTok.text.toLowerCase() === 'forced-colors' && valTok.text.toLowerCase() === 'active'
}

/**
 * True when `node` sits inside an ancestor `@media` at-rule whose condition
 * requires forced-colors: active (see hasActiveForcedColorsCondition). A
 * declaration/condition under any such ancestor can only ever run when
 * forced-colors is active — nested at-rules AND their conditions together,
 * so any one qualifying ancestor is sufficient, regardless of what other
 * conditions also apply.
 */
function isInForcedColorsActiveMedia(node) {
  let parent = node.parent
  while (parent) {
    if (parent.type === 'atrule' && normalizeProperty(parent.name) === 'media'
      && hasActiveForcedColorsCondition(parent.params ?? '')) {
      return true
    }
    parent = parent.parent
  }
  return false
}

function requireScanArgs(path, source, policy) {
  if (typeof path !== 'string' || path.length === 0) throw new Error('css-colors: scan requires a non-empty path')
  if (typeof source !== 'string') throw new Error('css-colors: scan requires source to be a string')
  if (policy === null || typeof policy !== 'object') throw new Error('css-colors: policy is required')
  const { tokenCssNames } = policy
  const valid = Array.isArray(tokenCssNames)
    && tokenCssNames.length > 0
    && tokenCssNames.every((name) => typeof name === 'string' && name.length > 0)
  if (!valid) {
    throw new Error('css-colors: policy.tokenCssNames must be a non-empty array of registered CSS token names')
  }
}

export function scan({ path, source, policy }) {
  requireScanArgs(path, source, policy)
  let root
  try {
    root = postcss.parse(source, { from: path })
  } catch (error) {
    if (error instanceof postcss.CssSyntaxError) {
      return [{
        ruleId: RULE_PARSE_ERROR,
        path,
        syntax: `parse error: ${error.reason ?? 'unknown parser failure'}`,
        message: `css-colors could not parse this stylesheet (${error.message}). Fail-closed: fix the syntax error or extend the scanner.`,
        line: error.line ?? 1,
        column: error.column ?? 1,
      }]
    }
    throw error
  }
  const ctx = { tokenSet: new Set(policy.tokenCssNames) }
  const findings = []
  root.walk((node) => {
    if (node.type === 'decl') {
      const decl = node
      const position = makePositionMapper(decl)
      walkValueTokens({
        prop: decl.prop,
        value: decl.value,
        identContext: isIdentContextProperty(decl.prop),
        colorDepth: isColorContextProperty(decl.prop) ? 1 : 0,
        ctx,
        forcedColorsActive: isInForcedColorsActiveMedia(decl),
        messages: declarationMessages(decl.prop, decl.value),
        report: (ruleId, syntax, token, message) => {
          const at = position(token?.start ?? 0)
          findings.push({ ruleId, path, syntax, message, line: at.line, column: at.column })
        },
      })
      return
    }
    if (node.type === 'atrule') {
      const atRule = node
      const name = normalizeProperty(atRule.name)
      if (!CONDITION_AT_RULES.has(name)) return
      const params = atRule.params ?? ''
      if (params.trim() === '') return
      const position = makeAtRulePositionMapper(atRule)
      walkAtRuleCondition(params, ctx, (ruleId, syntax, offset, message) => {
        const at = position(offset)
        findings.push({ ruleId, path, syntax, message, line: at.line, column: at.column })
      }, isInForcedColorsActiveMedia(atRule))
    }
  })
  return findings
}
