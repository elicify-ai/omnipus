#!/usr/bin/env node
// Exact-value repair codemod — Stage B closure, C1 lane S-TYPO-3 (design-system
// enforcement ledger, rules typography/font-weight-arbitrary,
// typography/line-height-arbitrary, typography/letter-spacing-arbitrary,
// typography/font-family-off-token, typography/font-family-literal).
// Input, already decided upstream by lane M3:
//   dist/design-system-baseline/cli-lanes/c1-prep/M3/mapping.json + mapping.md
//   + raw-entries-by-rule.json.
//
// Seven script-able shapes (M3 mapping.md "Script-able patterns" table) plus
// one invisible cleanup, none of which invent a mapping M3 did not already
// decide:
//
//   1. Tailwind `font-normal` class token          -> font-[var(--font-weight-regular)]
//   2. Numeric fontWeight (JSX style / raw CSS)     -> var(--font-weight-<name>)
//   3. Tailwind `tracking-normal` class token       -> tracking-[var(--font-letter-spacing-normal)]
//   4. CSS `letter-spacing: 0;`                     -> letter-spacing: var(--font-letter-spacing-normal);
//   5. Exact-match lineHeight/line-height           -> var(--font-line-height-compact|body)
//   6. Tailwind `font-sans` class token (SEPARATE PASS, see below) -> font-body
//   7. Literal `fontFamily` string (exact match)    -> var(--type-code-family|body-family)
//   +  Invisible cleanup: 12 brand-logo SVGs' dead `line-height:1` (no
//      <text>/<tspan> in the file, so the declaration has zero rendered
//      effect) is removed.
//
// Everything M3 filed under NEEDS-DECISION is REFUSED, never guessed: the two
// `fontWeight: <ternary>` sites (AgentListScreen.tsx), `lineHeight`/
// `line-height` values of `1` and `1.65` (no exact token), `tracking-[0.07em]`
// / `tracking-[0.08em]` / `tracking-[0.2em]` / `letter-spacing: 0.04em` (gaps
// in the 3-value letter-spacing scale), and non-logo `line-height: 1` sites
// (brand-icon.tsx, fullcalendar-theme.css). This codemod actively DETECTS
// each of those shapes and reports a refusal for it (not a silent skip), so a
// dry-run report shows the NEEDS-DECISION set explicitly rather than by
// omission.
//
// Font-sans is a SEPARATE, independently-appliable pass (group 6 above): it
// is a real, sanctioned, but VISIBLE rendering change (M3: Tailwind's
// `font-sans` resolves to the OS system font, not Inter, because only
// `font-mono`/`font-headline`/`font-body` are remapped onto tokens in this
// repo — `font-body` is the fix, and it is correct, but five surfaces will
// visibly re-render in Inter). It is gated behind `--font-sans-pass`: with
// that flag, `--apply` writes ONLY the font-sans->font-body edits and
// none of the other (invisible) groups; without it, `--apply` writes every
// other group and leaves font-sans untouched, reported separately as
// `fontSansPass` in the summary so the lead can apply it independently, on
// its own review, in its own commit.
//
// Property-name matching for fontWeight/lineHeight (pattern 2 and 5) is
// restricted to properties provably inside a real JSX `style={{ }}`
// attribute (walks up the parent chain to a JsxAttribute named `style`,
// exactly like scripts/design-system/codemod-type-scale-css.mjs's
// `isWithinJsxStyleAttribute`) — never a same-named field on an unrelated
// object. fontFamily (pattern 7) is deliberately NOT restricted that way:
// two of its three ledger sites (LibraryCodeEditor.tsx's CodeMirror
// `.cm-scroller` theme object, mermaid-renderer.tsx's `themeVariables`
// config) are legitimate font-stack values passed to non-React theming APIs,
// not React `style` props, so structural containment would wrongly refuse
// them. Safety instead comes from matching the FULL, EXACT, multi-segment
// font-stack string M3 already verified (never a bare family name or a
// property-name-only match), which cannot plausibly coincide with an
// unrelated field.
//
// Conventions match scripts/design-system/codemod-dead-textclass-read.mjs and
// scripts/design-system/codemod-type-scale-css.mjs: dry-run by default,
// `--apply` writes, idempotent, refuses ambiguous matches, deterministic
// output, summary object with totalFilesScanned/totalFilesTouched/
// totalEdits/totalRefusals. Touches src/ and packages/ui/src/ (the usual
// allowed roots, via codemod-lib's ALLOWED_ROOTS/isInAllowedRoot) PLUS
// src/assets/ for the SVG cleanup ONLY — every SVG write is additionally
// guarded by `isSvgCleanupAllowedPath`, which requires the path to sit under
// src/assets/ specifically, not just anywhere isInAllowedRoot would accept.
import { readdirSync, statSync } from 'node:fs'
import { extname, relative, resolve, sep } from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'
import {
  ALLOWED_ROOTS, applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const SKIP_DIR_NAMES = new Set(['node_modules', 'dist', '.git'])
const SVG_CLEANUP_ROOT = 'src/assets'
const CLASS_BUILDER_NAMES = new Set(['cn', 'clsx', 'classes', 'cx', 'classNames'])

/** Recursively lists every file with `ext` under the given root-relative directories. */
function listFilesByExt(repoRoot, roots, ext) {
  const out = []
  const walk = (dir) => {
    let names
    try { names = readdirSync(dir) } catch { return }
    for (const name of names) {
      if (SKIP_DIR_NAMES.has(name)) continue
      const full = resolve(dir, name)
      let st
      try { st = statSync(full) } catch { continue }
      if (st.isDirectory()) walk(full)
      else if (extname(name) === ext) out.push(full)
    }
  }
  for (const root of roots) walk(resolve(repoRoot, root))
  return out
}

function listCssFiles(repoRoot, roots = ALLOWED_ROOTS) {
  return listFilesByExt(repoRoot, roots, '.css')
}

function listSvgFiles(repoRoot, roots = [SVG_CLEANUP_ROOT]) {
  return listFilesByExt(repoRoot, roots, '.svg')
}

/** Guard for the one path the SVG cleanup is allowed to write under — a
 * strictly narrower check than isInAllowedRoot (which would accept anything
 * under src/), so a future change to SVG_CLEANUP_ROOT or a bug in the file
 * walker cannot silently widen where this codemod is allowed to write. */
export function isSvgCleanupAllowedPath(repoRoot, absPath) {
  const rel = relative(repoRoot, absPath).split(sep).join('/')
  return rel === SVG_CLEANUP_ROOT || rel.startsWith(SVG_CLEANUP_ROOT + '/')
}

function locOf(source, pos) {
  const lc = source.getLineAndCharacterOfPosition(pos)
  return { line: lc.line + 1, column: lc.character + 1 }
}

// ── Token tables (design-system/tokens/foundations.json, verified by M3) ──

const FONT_WEIGHT_VAR = { 400: '--font-weight-regular', 500: '--font-weight-medium', 600: '--font-weight-semibold', 700: '--font-weight-bold' }
const FONT_FAMILY_LITERAL_MAP = new Map([
  ['"JetBrains Mono", "Fira Code", monospace', 'var(--type-code-family)'],
  ['"Inter", system-ui, sans-serif', 'var(--type-body-family)'],
])

const NEEDS_DECISION = {
  fontWeightTernary: 'M3 NEEDS-DECISION: a ConditionalExpression is captured as raw source text by the scanner\'s value-shape check and can never match a var() reference no matter what its two arms contain — the sound fix is structural (move the condition into className as two static Tailwind arms), not a literal substitution. Refusing rather than guessing.',
  lineHeightOne: 'M3 NEEDS-DECISION: no registered lineHeight token equals 1 (nearest is font.lineHeight.tight at 1.1, a 10% increase, not identical) — refusing rather than mis-mapping a decorative/icon glyph line-height onto a role token meant for display/heading text.',
  lineHeightOneSixFive: 'M3 NEEDS-DECISION: no registered lineHeight token equals 1.65 (nearest is font.lineHeight.body at 1.6, a real ~3% visible tightening) — a Redesign-Risk-tier change per D9, not an invisible one. Refusing rather than rounding without approval.',
  trackingGap007: 'M3 NEEDS-DECISION: 0.07em is far outside the 3-value letter-spacing scale (wide=0.025em) — a token-scale gap, not a rounding question. Refusing rather than guessing a new token name the lead has not registered.',
  trackingGap008: 'M3 NEEDS-DECISION: 0.08em is far outside the 3-value letter-spacing scale (wide=0.025em) — clusters with the 0.07em group as a possible new "widest" scale step, but that is a token-design call, not a mechanical mapping. Refusing rather than guessing.',
  trackingGap02: 'M3 NEEDS-DECISION: 0.2em is 8x the widest registered token (wide=0.025em) — most likely a one-off hero/display treatment, not a role-text tracking value. Refusing rather than guessing.',
  leadingOneSixFive: 'M3 NEEDS-DECISION: no registered lineHeight token equals 1.65 (nearest is font.lineHeight.body at 1.6, a real ~3% visible tightening) — a Redesign-Risk-tier change per D9. Refusing rather than rounding without approval.',
  letterSpacingGap004: 'M3 NEEDS-DECISION: 0.04em sits between normal (0em) and wide (0.025em) and matches neither — a token-scale gap. Refusing rather than guessing.',
  lineHeightOneCss: 'M3 NEEDS-DECISION: no registered lineHeight token equals 1 (nearest is font.lineHeight.tight at 1.1, not identical) — refusing rather than mis-mapping non-logo chrome onto a role token it was never meant for.',
}

const REFUSE_CLASS_TOKENS = new Map([
  ['tracking-[0.07em]', NEEDS_DECISION.trackingGap007],
  ['tracking-[0.08em]', NEEDS_DECISION.trackingGap008],
  ['tracking-[0.2em]', NEEDS_DECISION.trackingGap02],
  ['leading-[1.65]', NEEDS_DECISION.leadingOneSixFive],
])

const APPLY_CLASS_TOKENS = new Map([
  ['font-normal', 'font-[var(--font-weight-regular)]'],
])
const TRACKING_NORMAL_TOKEN = 'tracking-[var(--font-letter-spacing-normal)]'
const FONT_SANS_TOKEN = 'font-body'

// ── Class-token spans: JSX className/class attributes and cn()/clsx()-style
// class-builder calls, anywhere in the file (module scope included, so an
// exported `cn(...)` constant like DATE_TRIGGER_CLASSNAME is covered). ──

function literalSpan(node) {
  return { node, text: node.text, offset: node.getStart() + 1 }
}

function collectFromExpression(expr, spans) {
  if (!expr) return
  if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) {
    spans.push(literalSpan(expr))
  } else if (ts.isTemplateExpression(expr)) {
    spans.push(literalSpan(expr.head))
    for (const s of expr.templateSpans) spans.push(literalSpan(s.literal))
  }
  // Any other expression shape (ternary, identifier, call other than a
  // recognized class builder, …) is out of scope for every pattern this
  // codemod owns — no site in the M3 mapping needs it, so it is left
  // unmatched rather than guessed at.
}

function collectClassSpans(source) {
  const spans = []
  const visit = (node) => {
    if (ts.isJsxAttribute(node) && ts.isIdentifier(node.name) && (node.name.text === 'className' || node.name.text === 'class')) {
      const init = node.initializer
      if (init) {
        if (ts.isStringLiteral(init)) spans.push(literalSpan(init))
        else if (ts.isJsxExpression(init)) collectFromExpression(init.expression, spans)
      }
    } else if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_BUILDER_NAMES.has(node.expression.text)) {
      for (const arg of node.arguments) collectFromExpression(arg, spans)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return spans
}

/** Whitespace-delimited tokens within `text`, each with its offset into `text`. */
function tokenizeWithOffsets(text) {
  const tokens = []
  const re = /\S+/g
  let m
  while ((m = re.exec(text))) tokens.push({ value: m[0], start: m.index, end: m.index + m[0].length })
  return tokens
}

function planClassTokenEdits(repoRoot, filePath, source) {
  const relPath = relative(repoRoot, filePath)
  const mainEdits = []
  const fontSansEdits = []
  const refusals = []
  const spans = collectClassSpans(source)
  for (const span of spans) {
    for (const tok of tokenizeWithOffsets(span.text)) {
      const absStart = span.offset + tok.start
      const absEnd = span.offset + tok.end
      const loc = locOf(source, absStart)
      const locStr = `${relPath}:${loc.line}:${loc.column}`
      if (APPLY_CLASS_TOKENS.has(tok.value)) {
        mainEdits.push({ start: absStart, end: absEnd, replacement: APPLY_CLASS_TOKENS.get(tok.value), loc: locStr, syntax: tok.value })
      } else if (tok.value === 'tracking-normal') {
        mainEdits.push({ start: absStart, end: absEnd, replacement: TRACKING_NORMAL_TOKEN, loc: locStr, syntax: tok.value })
      } else if (tok.value === 'font-sans') {
        fontSansEdits.push({ start: absStart, end: absEnd, replacement: FONT_SANS_TOKEN, loc: locStr, syntax: tok.value })
      } else if (REFUSE_CLASS_TOKENS.has(tok.value)) {
        refusals.push({ file: relPath, loc: locStr, syntax: tok.value, reason: REFUSE_CLASS_TOKENS.get(tok.value) })
      }
    }
  }
  return { mainEdits, fontSansEdits, refusals }
}

// ── Style-object property assignments (fontWeight, lineHeight, fontFamily) ─

/** True when `node` is nested (at any depth) inside a JSX `style={...}` attribute. */
function isWithinJsxStyleAttribute(node) {
  let current = node.parent
  while (current) {
    if (ts.isJsxAttribute(current) && ts.isIdentifier(current.name) && current.name.text === 'style') return true
    current = current.parent
  }
  return false
}

function literalTextOf(node) {
  if (ts.isNumericLiteral(node) || ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
  return null
}

function planStyleObjectEdits(repoRoot, filePath, source) {
  const relPath = relative(repoRoot, filePath)
  const edits = []
  const refusals = []
  const visit = (node) => {
    if (ts.isPropertyAssignment(node) && ts.isIdentifier(node.name)) {
      const propName = node.name.text
      const init = node.initializer
      const loc = locOf(source, node.getStart())
      const locStr = `${relPath}:${loc.line}:${loc.column}`

      if (propName === 'fontWeight' && isWithinJsxStyleAttribute(node)) {
        if (ts.isNumericLiteral(init) && init.text in FONT_WEIGHT_VAR) {
          edits.push({ start: init.getStart(), end: init.getEnd(), replacement: `'var(${FONT_WEIGHT_VAR[init.text]})'`, loc: locStr, syntax: `fontWeight: ${init.text}` })
        } else if (ts.isConditionalExpression(init)) {
          refusals.push({ file: relPath, loc: locStr, syntax: node.getText(), reason: NEEDS_DECISION.fontWeightTernary })
        }
      } else if (propName === 'lineHeight' && isWithinJsxStyleAttribute(node)) {
        const val = literalTextOf(init)
        if (val === '1.4') {
          edits.push({ start: init.getStart(), end: init.getEnd(), replacement: `'var(--font-line-height-compact)'`, loc: locStr, syntax: `lineHeight: ${init.getText()}` })
        } else if (val === '1') {
          refusals.push({ file: relPath, loc: locStr, syntax: `lineHeight: ${init.getText()}`, reason: NEEDS_DECISION.lineHeightOne })
        } else if (val === '1.65') {
          refusals.push({ file: relPath, loc: locStr, syntax: `lineHeight: ${init.getText()}`, reason: NEEDS_DECISION.lineHeightOneSixFive })
        }
      } else if (propName === 'fontFamily') {
        if (ts.isStringLiteral(init) && FONT_FAMILY_LITERAL_MAP.has(init.text)) {
          edits.push({ start: init.getStart(), end: init.getEnd(), replacement: `'${FONT_FAMILY_LITERAL_MAP.get(init.text)}'`, loc: locStr, syntax: `fontFamily: ${init.getText()}` })
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals }
}

// ── TS/TSX file driver ─────────────────────────────────────────────────────

export function planTsFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const { mainEdits: classMain, fontSansEdits, refusals: classRefusals } = planClassTokenEdits(repoRoot, filePath, source)
  const { edits: styleEdits, refusals: styleRefusals } = planStyleObjectEdits(repoRoot, filePath, source)
  return {
    mainEdits: [...classMain, ...styleEdits],
    fontSansEdits,
    refusals: [...classRefusals, ...styleRefusals],
    text,
    source,
  }
}

// ── Plain CSS declarations (font-weight, letter-spacing, line-height) ─────

export function planCssFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const relPath = relative(repoRoot, filePath)
  let root
  try {
    root = postcss.parse(text, { from: filePath })
  } catch (err) {
    return {
      edits: [],
      refusals: [{ file: relPath, loc: `${relPath}:1:1`, syntax: '(unparseable CSS)', reason: `PostCSS could not parse this file (${err.message}); refusing rather than editing blind.` }],
      text,
      after: text,
    }
  }
  const edits = []
  const refusals = []
  let changed = false
  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    const value = decl.value.trim()
    const start = decl.source?.start
    const loc = start ? `${relPath}:${start.line}:${start.column}` : `${relPath}:?:?`

    if (prop === 'font-weight' && value in FONT_WEIGHT_VAR) {
      const replacement = `var(${FONT_WEIGHT_VAR[value]})`
      edits.push({ loc, syntax: `font-weight: ${value}`, replacement: `font-weight: ${replacement}` })
      decl.value = replacement
      changed = true
      return
    }
    if (prop === 'letter-spacing') {
      if (value === '0') {
        edits.push({ loc, syntax: 'letter-spacing: 0', replacement: 'letter-spacing: var(--font-letter-spacing-normal)' })
        decl.value = 'var(--font-letter-spacing-normal)'
        changed = true
        return
      }
      if (value === '0.04em') {
        refusals.push({ file: relPath, loc, syntax: `letter-spacing: ${value}`, reason: NEEDS_DECISION.letterSpacingGap004 })
      }
      return
    }
    if (prop === 'line-height') {
      if (value === '1.6') {
        edits.push({ loc, syntax: 'line-height: 1.6', replacement: 'line-height: var(--font-line-height-body)' })
        decl.value = 'var(--font-line-height-body)'
        changed = true
        return
      }
      if (value === '1') {
        refusals.push({ file: relPath, loc, syntax: 'line-height: 1', reason: NEEDS_DECISION.lineHeightOneCss })
      }
    }
  })
  return { edits, refusals, text, after: changed ? root.toString() : text }
}

// ── SVG invisible cleanup: dead `line-height:1` in a `style=""` attribute ──
// with no <text>/<tspan> anywhere in the file. Refuses (never removes) when
// a <text>/<tspan> is present — the no-text fact is re-derived from the
// FILE'S OWN CONTENT on every run, never hardcoded to a frozen file list.

const LINE_HEIGHT_ONE_DECL_RE = /^line-height\s*:\s*1$/
const HAS_TEXT_ELEMENT_RE = /<text[\s>]|<tspan[\s>]/i

export function planSvgLineHeightCleanup(repoRoot, filePath) {
  const text = readFile(filePath)
  const relPath = relative(repoRoot, filePath)
  const edits = []
  const refusals = []
  if (!isSvgCleanupAllowedPath(repoRoot, filePath)) {
    return { edits, refusals, text, applied: false, guardRejected: true }
  }
  const hasTextEl = HAS_TEXT_ELEMENT_RE.test(text)
  const styleAttrRe = /style="([^"]*)"/g
  let m
  while ((m = styleAttrRe.exec(text))) {
    const styleValue = m[1]
    const decls = styleValue.split(';').map((d) => d.trim())
    const idx = decls.findIndex((d) => LINE_HEIGHT_ONE_DECL_RE.test(d))
    if (idx === -1) continue
    if (hasTextEl) {
      refusals.push({ file: relPath, loc: `${relPath}:1:${m.index + 1}`, syntax: m[0], reason: 'refused: this SVG contains a <text>/<tspan> element, so its line-height:1 is not proven inert — verified per file, not assumed from the fixture list.' })
      continue
    }
    const remaining = decls.filter((_, i) => i !== idx).filter(Boolean)
    const newStyleValue = remaining.join(';')
    const valueStart = m.index + 'style="'.length
    const valueEnd = valueStart + styleValue.length
    edits.push({ start: valueStart, end: valueEnd, replacement: newStyleValue, loc: `${relPath}:1:${valueStart + 1}`, syntax: m[0] })
  }
  return { edits, refusals, text }
}

// ── Orchestration ──────────────────────────────────────────────────────────

export function runCodemod({ repoRoot, apply, fontSansPass = false }) {
  const tsFiles = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const cssFiles = listCssFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const svgFiles = listSvgFiles(repoRoot).filter((f) => isSvgCleanupAllowedPath(repoRoot, f))

  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  let fontSansTotalEdits = 0
  let fontSansFilesTouched = 0

  for (const filePath of tsFiles) {
    const relPath = relative(repoRoot, filePath)
    const { mainEdits, fontSansEdits, refusals, text } = planTsFileEdits(repoRoot, filePath)
    if (mainEdits.length === 0 && fontSansEdits.length === 0 && refusals.length === 0) continue

    // This invocation's PRIMARY edits: the main (invisible) group normally,
    // or the font-sans group when running the separate font-sans pass. The
    // OTHER group is always computed too, purely for informational
    // reporting (never applied by this invocation).
    const primaryEdits = fontSansPass ? fontSansEdits : mainEdits
    const otherEdits = fontSansPass ? mainEdits : fontSansEdits
    const after = primaryEdits.length > 0 ? applyEdits(text, primaryEdits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement }))) : text
    const diff = primaryEdits.length > 0 ? unifiedDiff(relPath, text, after) : ''
    const otherAfter = otherEdits.length > 0 ? applyEdits(text, otherEdits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement }))) : text
    const otherDiff = otherEdits.length > 0 ? unifiedDiff(relPath, text, otherAfter) : ''

    fileResults.push({
      file: relPath,
      kind: 'ts',
      editCount: primaryEdits.length,
      refusalCount: refusals.length,
      edits: primaryEdits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
      fontSans: fontSansPass
        ? { editCount: primaryEdits.length, edits: primaryEdits.map((e) => ({ loc: e.loc, syntax: e.syntax })), diff }
        : { editCount: otherEdits.length, edits: otherEdits.map((e) => ({ loc: e.loc, syntax: e.syntax })), diff: otherDiff },
    })
    totalEdits += primaryEdits.length
    totalRefusals += refusals.length
    fontSansTotalEdits += fontSansEdits.length
    if (fontSansEdits.length > 0) fontSansFilesTouched += 1

    if (apply && primaryEdits.length > 0) writeFileAtomic(filePath, after)
  }

  // CSS has no font-sans-shaped pattern (that group is Tailwind-class-only,
  // TS/TSX). A --font-sans-pass invocation therefore never scans or writes
  // CSS at all — its summary is scoped to the one pass it applies.
  for (const filePath of cssFiles) {
    if (fontSansPass) continue
    const relPath = relative(repoRoot, filePath)
    const { edits, refusals, text, after } = planCssFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    const diff = after !== text ? unifiedDiff(relPath, text, after) : ''
    fileResults.push({ file: relPath, kind: 'css', editCount: edits.length, refusalCount: refusals.length, edits, refusals, diff, fontSans: { editCount: 0, edits: [], diff: '' } })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && after !== text) writeFileAtomic(filePath, after)
  }

  for (const filePath of svgFiles) {
    if (fontSansPass) continue // font-sans pass never touches SVGs
    const relPath = relative(repoRoot, filePath)
    const { edits, refusals, text, guardRejected } = planSvgLineHeightCleanup(repoRoot, filePath)
    if (guardRejected) continue
    if (edits.length === 0 && refusals.length === 0) continue
    const after = edits.length > 0 ? applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement }))) : text
    const diff = edits.length > 0 ? unifiedDiff(relPath, text, after) : ''
    fileResults.push({ file: relPath, kind: 'svg', editCount: edits.length, refusalCount: refusals.length, edits, refusals, diff, fontSans: { editCount: 0, edits: [], diff: '' } })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) {
      if (!isSvgCleanupAllowedPath(repoRoot, filePath)) throw new Error(`refusing to write outside ${SVG_CLEANUP_ROOT}: ${relPath}`)
      writeFileAtomic(filePath, after)
    }
  }

  return {
    totalFilesScanned: fontSansPass ? tsFiles.length : tsFiles.length + cssFiles.length + svgFiles.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    fontSansPass: {
      applied: apply && fontSansPass,
      totalEdits: fontSansTotalEdits,
      totalFilesTouched: fontSansFilesTouched,
      note: fontSansPass
        ? 'this invocation applied the font-sans -> font-body pass (a real, sanctioned, but VISIBLE change — see file header).'
        : 'font-sans -> font-body is intentionally not applied by this invocation; re-run with --font-sans-pass --apply to apply it independently.',
    },
    files: fileResults,
    applied: apply,
    mode: fontSansPass ? 'font-sans-pass' : 'main-pass',
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const argv = process.argv.slice(2)
  const fontSansPass = argv.includes('--font-sans-pass')
  const { apply, root } = parseCodemodArgs(argv, resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply, fontSansPass })
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
    if (!fontSansPass && f.fontSans && f.fontSans.editCount > 0) {
      console.log(`FONT-SANS PASS AVAILABLE ${f.file}: ${f.fontSans.editCount} font-sans -> font-body edit(s) not applied (re-run with --font-sans-pass --apply)`)
    }
  }
  console.log(JSON.stringify({
    mode: result.mode,
    applied: result.applied,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
    fontSansPass: result.fontSansPass,
  }, null, 2))
  process.exit(0)
}
