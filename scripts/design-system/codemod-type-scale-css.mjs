#!/usr/bin/env node
// Exact-value repair codemod — Stage B closure, C1 lane S-TYPO-2 (design-system
// enforcement ledger, rules typography/font-size-below-floor,
// typography/unregistered-font-size-token, typography/missing-arbitrary-type-hint).
// Input, already decided upstream by lane M2:
//   dist/design-system-baseline/cli-lanes/c1-prep/M2/mapping.json + mapping.md
//   + impact-and-patterns.md.
//
// This script owns the NON-TAILWIND text-size/family shapes only. A sibling
// lane owns the Tailwind static/arbitrary utility classes (`text-xs`,
// `text-[10px]`, …) — this codemod never touches a className string except
// the one narrow "missing arbitrary type hint" shape below, and it never
// touches a bare Tailwind size utility.
//
// Four pattern shapes, each applying M2's per-value table (never inventing a
// mapping M2 did not already decide):
//
//   1. Plain CSS `font-size:`/`font-family:` declarations (any .css file
//      under the allowed roots) — parsed with the SAME postcss AST the
//      css-colors lock uses (scripts/design-system-locks/css-colors.mjs), so
//      declaration order, custom properties, !important, comments and vendor
//      prefixes are reproduced byte-for-byte for every declaration this
//      script does not touch, and preserved automatically for the ones it
//      does (only `decl.value` is reassigned; postcss's raw-value cache is
//      invalidated by that assignment — see postcss/lib/stringifier.js
//      `rawValue()` — so the stringifier never echoes stale source text).
//   2. JSX inline `style={{ fontSize: '…' }}` / `style={{ fontFamily: '…' }}`
//      object properties, matched on the TypeScript AST and restricted to
//      properties that are provably inside a JSX `style=` attribute (walks
//      up from the PropertyAssignment to a JsxAttribute named `style`) —
//      never a same-named field on an unrelated object.
//   3. The SVG presentation attribute `fontSize="9"` in ChartPart.tsx (and
//      any other file with the identical literal shape) — REFUSED, never
//      rewritten. See "SVG presentation-attribute decision" below.
//   4. The `missing-arbitrary-type-hint` Tailwind-adjacent shape
//      (`text-[var(--x)]` lacking a `color:`/`length:` hint) — matched as an
//      exact substring inside a string-literal-like node (covers both a
//      plain JS string literal and a JSX attribute string value, which share
//      the same `StringLiteral` AST node shape). Only the THREE rows M2's
//      table actually lists for this rule are recognized; nothing else
//      matching `text-[var(--…)]` is touched.
//
// SVG presentation-attribute decision (M2 NEEDS-DECISION row, ChartPart.tsx
// `fontSize="9"` × 6 literal occurrences behind one ledger fingerprint):
// REFUSED, not rewritten to `fontSize={'var(--type-caption-size)'}` or any
// other var()-based form. `<text fontSize="9">` is a raw SVG presentation
// attribute (React writes it to the DOM as the `font-size` attribute, not
// through the CSS `style` property), and presentation-attribute VALUES are
// parsed by the SVG attribute grammar, not the CSS value grammar a
// stylesheet or a `style` attribute gets — custom-property (`var()`)
// resolution is a CSS cascade behaviour, and WebKit/Safari has a
// long-standing, still-current gap where `var()` inside an SVG presentation
// attribute does not resolve (it resolves reliably only via the `style`
// attribute or a stylesheet rule), while Chromium and Firefox do resolve it.
// Omnipus explicitly supports macOS end users, where Safari is a mainstream
// browser choice, so this is not a corner case to wave through. This
// codemod cannot execute a real cross-browser rendering check from inside
// this harness, so per the lane brief ("if it is not provably safe,
// REFUSE") it refuses rather than guess between `var()` and a hardcoded
// `12` — both remain open in M2's NEEDS-DECISION list pending the lead's
// call on which fallback to script.
//
// Conventions match scripts/design-system/codemod-dead-textclass-read.mjs:
// dry-run by default, `--apply` writes, idempotent, refuses ambiguous
// matches, touches only src/ and packages/ui/src/, never guesses.
import { readdirSync, statSync } from 'node:fs'
import { extname, relative, resolve } from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'
import {
  ALLOWED_ROOTS, applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const SKIP_DIR_NAMES = new Set(['node_modules', 'dist', '.git'])

/** Recursively lists every .css file under the given root-relative directories. */
function listCssFiles(repoRoot, roots = ALLOWED_ROOTS) {
  const out = []
  const walk = (dir) => {
    let names
    try { names = readdirSync(dir) } catch { return }
    for (const name of names) {
      if (SKIP_DIR_NAMES.has(name)) continue
      const full = resolve(dir, name)
      const st = statSync(full)
      if (st.isDirectory()) walk(full)
      else if (extname(name) === '.css') out.push(full)
    }
  }
  for (const root of roots) walk(resolve(repoRoot, root))
  return out
}

function locOf(source, pos) {
  const lc = source.getLineAndCharacterOfPosition(pos)
  return { line: lc.line + 1, column: lc.character + 1 }
}

// A `var(--name)` or `var(--name, <fallback>)` reference — the fallback (if
// any) is dropped on replacement because every M2-mapped replacement token
// already carries its own complete fallback chain (see mapping.json notes
// for `--font-body`/`--font-headline`/`--font-mono`/`--font-outfit`).
const VAR_TOKEN_PATTERN = /^var\(\s*(--[a-zA-Z0-9-]+)\s*(?:,[\s\S]*)?\)$/

// ── Pattern 1: plain CSS `font-size:`/`font-family:` declarations ─────────

const CSS_FONT_SIZE_REPLACEMENTS = new Map([
  ['0.65rem', 'var(--type-caption-size)'],
  ['0.6rem', 'var(--type-caption-size)'],
  ['0.75rem', 'var(--type-utility-xs-size)'],
  ['0.7rem', 'var(--type-caption-size)'],
  ['0.85rem', 'var(--type-caption-size)'],
  ['0.875rem', 'var(--type-body-compact-size)'],
  ['9px', 'var(--type-caption-size)'],
])

const CSS_FONT_FAMILY_TOKEN_REPLACEMENTS = new Map([
  ['--font-body', 'var(--font-family-body)'],
  ['--font-headline', 'var(--font-family-heading)'],
  ['--font-mono', 'var(--font-family-mono)'],
])

const CLAMP_REFUSAL_REASON = 'M2 NEEDS-DECISION: this root font-size clamp() composes literal/primitive values inline; no single registered semantic token exists for "the product root font-size clamp" yet, and D9 does not pre-approve inventing one. Refusing rather than choosing a token name the lead has not registered.'

function planCssFileEdits(repoRoot, filePath) {
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
    if (prop !== 'font-size' && prop !== 'font-family') return
    const value = decl.value.trim()
    const start = decl.source?.start
    const loc = start ? `${relPath}:${start.line}:${start.column}` : `${relPath}:?:?`

    if (prop === 'font-size') {
      if (CSS_FONT_SIZE_REPLACEMENTS.has(value)) {
        const replacement = CSS_FONT_SIZE_REPLACEMENTS.get(value)
        edits.push({ loc, syntax: `font-size: ${value}`, replacement: `font-size: ${replacement}` })
        decl.value = replacement
        changed = true
        return
      }
      if (/^clamp\(/i.test(value)) {
        refusals.push({ file: relPath, loc, syntax: `font-size: ${value}`, reason: CLAMP_REFUSAL_REASON })
      }
      return
    }

    // font-family
    const match = VAR_TOKEN_PATTERN.exec(value)
    if (match && CSS_FONT_FAMILY_TOKEN_REPLACEMENTS.has(match[1])) {
      const replacement = CSS_FONT_FAMILY_TOKEN_REPLACEMENTS.get(match[1])
      edits.push({ loc, syntax: `font-family: ${value}`, replacement: `font-family: ${replacement}` })
      decl.value = replacement
      changed = true
    }
  })
  return { edits, refusals, text, after: changed ? root.toString() : text }
}

// ── Pattern 2: JSX inline `style={{ fontSize/fontFamily: '…' }}` ──────────

const JSX_STYLE_FONT_SIZE_REPLACEMENTS = new Map([
  ['0.65rem', 'var(--type-caption-size)'],
  ['0.75rem', 'var(--type-utility-xs-size)'],
  ['0.7rem', 'var(--type-caption-size)'],
  ['0.875rem', 'var(--type-body-compact-size)'],
  ['11px', 'var(--type-code-size)'],
])

const JSX_STYLE_FONT_FAMILY_TOKEN_REPLACEMENTS = new Map([
  ['--font-body', 'var(--font-family-body)'],
  ['--font-outfit', 'var(--font-family-heading)'],
])

/** True when `node` is nested (at any depth) inside a JSX `style={...}` attribute. */
function isWithinJsxStyleAttribute(node) {
  let current = node.parent
  while (current) {
    if (ts.isJsxAttribute(current) && ts.isIdentifier(current.name) && current.name.text === 'style') return true
    current = current.parent
  }
  return false
}

function planStyleObjectEdits(repoRoot, filePath, text, source) {
  const relPath = relative(repoRoot, filePath)
  const edits = []
  const visit = (node) => {
    if (
      ts.isPropertyAssignment(node) &&
      ts.isIdentifier(node.name) &&
      (node.name.text === 'fontSize' || node.name.text === 'fontFamily') &&
      ts.isStringLiteralLike(node.initializer) &&
      isWithinJsxStyleAttribute(node)
    ) {
      const raw = node.initializer.text
      let replacement = null
      if (node.name.text === 'fontSize' && JSX_STYLE_FONT_SIZE_REPLACEMENTS.has(raw)) {
        replacement = JSX_STYLE_FONT_SIZE_REPLACEMENTS.get(raw)
      } else if (node.name.text === 'fontFamily') {
        const match = VAR_TOKEN_PATTERN.exec(raw.trim())
        if (match && JSX_STYLE_FONT_FAMILY_TOKEN_REPLACEMENTS.has(match[1])) {
          replacement = JSX_STYLE_FONT_FAMILY_TOKEN_REPLACEMENTS.get(match[1])
        }
      }
      if (replacement !== null) {
        const start = node.initializer.getStart()
        const end = node.initializer.getEnd()
        const quote = text[start]
        const loc = locOf(source, start)
        edits.push({
          start,
          end,
          replacement: `${quote}${replacement}${quote}`,
          loc: `${relPath}:${loc.line}:${loc.column}`,
          syntax: `${node.name.text}: ${text.slice(start, end)}`,
        })
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return edits
}

// ── Pattern 3: SVG `fontSize="9"` presentation attribute — REFUSED ────────

const SVG_FONT_SIZE_REFUSAL_REASON = 'M2 NEEDS-DECISION: SVG presentation-attribute values follow the SVG attribute grammar, not the CSS value grammar, and WebKit/Safari has a documented gap where var() does not resolve there (only via a style attribute or stylesheet rule) while Chromium/Firefox do resolve it. Omnipus supports macOS end users, where Safari is mainstream, so var() here is not provably safe from this harness. Refusing rather than guessing between a var() reference and a hardcoded 12 — both remain open in M2 pending the lead\'s choice.'

function planSvgAttributeRefusals(repoRoot, filePath, source) {
  const relPath = relative(repoRoot, filePath)
  const refusals = []
  const visit = (node) => {
    if (
      ts.isJsxAttribute(node) &&
      ts.isIdentifier(node.name) &&
      node.name.text === 'fontSize' &&
      node.initializer &&
      ts.isStringLiteralLike(node.initializer) &&
      node.initializer.text === '9'
    ) {
      const loc = locOf(source, node.getStart())
      refusals.push({ file: relPath, loc: `${relPath}:${loc.line}:${loc.column}`, syntax: 'fontSize="9"', reason: SVG_FONT_SIZE_REFUSAL_REASON })
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return refusals
}

// ── Pattern 4: missing arbitrary type hint (`text-[var(--x)]`) ────────────
// Only the three rows M2's mapping.json actually lists for
// typography/missing-arbitrary-type-hint. Matched as an exact substring
// inside any string-literal-like AST node (covers a plain JS string literal
// and a JSX attribute string value alike, since TypeScript represents both
// as the same StringLiteral node kind).

const HINT_REPLACEMENTS = [
  { find: 'text-[var(--badge-error-foreground)]', replace: 'text-[color:var(--badge-error-foreground)]' },
]

const HINT_REFUSALS = [
  {
    find: 'text-[var(--color-primary-fg,var(--color-secondary))]',
    reason: 'M2 NEEDS-DECISION: --color-primary-fg is not a registered colour token (its fallback, --color-secondary, is). A type hint cannot close this finding without first resolving which registered token this should reference — refusing rather than guessing.',
  },
  {
    find: 'text-[var(--color-text-secondary)]',
    reason: 'M2 NEEDS-DECISION: --color-text-secondary is not a registered colour token (checked against design-system/tokens/colors.json; candidates are --color-muted or --color-secondary). A type hint alone never approves an unknown token — refusing rather than guessing.',
  },
]

function planMissingHintEdits(repoRoot, filePath, text, source) {
  const relPath = relative(repoRoot, filePath)
  const edits = []
  const refusals = []
  const visit = (node) => {
    if (ts.isStringLiteralLike(node)) {
      const raw = text.slice(node.getStart(), node.getEnd())
      for (const { find, replace } of HINT_REPLACEMENTS) {
        let idx = raw.indexOf(find)
        while (idx !== -1) {
          const absStart = node.getStart() + idx
          const loc = locOf(source, absStart)
          edits.push({ start: absStart, end: absStart + find.length, replacement: replace, loc: `${relPath}:${loc.line}:${loc.column}`, syntax: find })
          idx = raw.indexOf(find, idx + find.length)
        }
      }
      for (const { find, reason } of HINT_REFUSALS) {
        let idx = raw.indexOf(find)
        while (idx !== -1) {
          const absStart = node.getStart() + idx
          const loc = locOf(source, absStart)
          refusals.push({ file: relPath, loc: `${relPath}:${loc.line}:${loc.column}`, syntax: find, reason })
          idx = raw.indexOf(find, idx + find.length)
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals }
}

// ── TS/TSX file driver (patterns 2, 3, 4) ──────────────────────────────────

export function planTsxFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const styleEdits = planStyleObjectEdits(repoRoot, filePath, text, source)
  const svgRefusals = planSvgAttributeRefusals(repoRoot, filePath, source)
  const { edits: hintEdits, refusals: hintRefusals } = planMissingHintEdits(repoRoot, filePath, text, source)
  return {
    edits: [...styleEdits, ...hintEdits],
    refusals: [...svgRefusals, ...hintRefusals],
    text,
    source,
  }
}

export { planCssFileEdits, listCssFiles }

// ── Orchestration ──────────────────────────────────────────────────────────

export function runCodemod({ repoRoot, apply }) {
  const cssFiles = listCssFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const tsFiles = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))

  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0

  for (const filePath of cssFiles) {
    const { edits, refusals, text, after } = planCssFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    const diff = after !== text ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({ file: relative(repoRoot, filePath), kind: 'css', editCount: edits.length, refusalCount: refusals.length, edits, refusals, diff })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && after !== text) writeFileAtomic(filePath, after)
  }

  for (const filePath of tsFiles) {
    const { edits, refusals, text } = planTsxFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      kind: 'tsx',
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    totalFilesScanned: cssFiles.length + tsFiles.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
