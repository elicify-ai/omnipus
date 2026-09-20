#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure fan-out lane S-SPACE-3
// (dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md), covering
// two of Lane M1's spacing pattern families (mapping.md "Recommended script
// boundaries", script #5 `codemod-spacing-raw-css` + script #6
// `codemod-spacing-safe-area-fallback`, merged into one script per this
// lane's brief):
//
//   1. raw-css-declaration + unregistered-custom-property-calc — spacing
//      values written as a literal CSS declaration text (`padding: 6px 4px`),
//      found in two shapes: real `.css` stylesheets (src/styles/*.css) and
//      inline React `style={{ ... }}` object-literal string values in
//      .ts/.tsx (FullCalendarView.tsx, markdown-shared.tsx — FullCalendar's
//      own JS theming API takes a style object, not a className). Neither
//      shape is reachable by codemod-lib.mjs's existing .ts/.tsx-only,
//      className-shaped walker (mapping.md finding 7), so this script parses
//      CSS text with `postcss` — the same parser scripts/design-system-locks/
//      spacing.mjs and css-colors.mjs already use for the enforcement
//      scanners — never a hand-rolled regex over px/rem literals.
//      `unregistered-custom-property-calc` (the four tree-depth indent
//      multipliers, `calc(var(--…-indent-depth-px) * 1px)`) is NOT a spacing
//      value at all (mapping.md finding 6) — it is always refused, and this
//      script also refuses any *further* occurrence of that exact shape it
//      finds beyond the ones already enumerated in mapping.json (a per-instance
//      multiplier can appear more than once in the same file — Sidebar.tsx
//      has three — while the ledger counts it once; refusing every occurrence
//      found is the fail-closed choice, never silently skipping a repeat).
//
//   2. safe-area-env-fallback — `env(safe-area-inset-*)` used without a
//      registered token fallback, in three surface shapes: a plain CSS/style
//      declaration (`padding-right: env(safe-area-inset-right)`), a CSS
//      declaration that ALREADY wraps env() in `max(<fixed>, env(...))` to
//      enforce a floor even when the device has no notch (globals.css'
//      `[data-app-shell] > div > header`), and a Tailwind arbitrary-bracket
//      className utility (`pb-[env(safe-area-inset-bottom)]`,
//      `pb-[max(0.5rem,env(safe-area-inset-bottom))]`). mapping.md finding 5
//      is explicit that the ledger's own suggested `env(prop, fallback)` form
//      is NOT behaviour-preserving for the two `max()` sites: `max()`'s floor
//      still applies when the inset is exactly 0 (no notch); `env()`'s own
//      fallback only ever fires when the browser doesn't support `env()` at
//      all, which is a different condition. Every safe-area-env-fallback row
//      in mapping.json is (and, as of the 2026-09-20 regeneration, remains)
//      NEEDS-DECISION — picking the actual fallback token per surface is a
//      central-review design decision this script is not authorized to make.
//      This script therefore never rewrites a safe-area site; it always
//      refuses, but distinguishes WHY per site (floor-preserving risk vs.
//      plain missing-fallback) so the refusal itself states the correct next
//      step instead of parroting the ledger's behaviour-changing suggestion.
//
// Every other rule from scripts/design-system/codemod-lib.mjs's shared
// contract still applies: dry-run by default (`--apply` writes), idempotent
// (a value this script already rewrote no longer matches its own OLD-value
// lookup key, so a second run finds nothing left to do), refuses rather than
// guesses, touches only ALLOWED_ROOTS (src/, packages/ui/src/), and reports a
// deterministic summary (totalFilesScanned/totalFilesTouched/totalEdits/
// totalRefusals).
//
// Group gate: mapping.json rows are read AT RUN TIME (never hardcoded) and
// grouped IDENTICAL / NORMALIZED / NEEDS-DECISION by Lane M1. This script
// applies ONLY rows in `--groups` (default `IDENTICAL,NORMALIZED`) and always
// refuses NEEDS-DECISION regardless of that flag — the flag can only ever
// narrow which of the two mechanical groups get applied, never promote a
// NEEDS-DECISION row into an applied one.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { relative, resolve, extname } from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'
import {
  ALLOWED_ROOTS, applyEdits, isInAllowedRoot, parseCodemodArgs, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

export const DEFAULT_MAPPING_PATH = 'dist/design-system-baseline/cli-lanes/c1-prep/M1/mapping.json'

const SKIP_DIR_NAMES = new Set(['node_modules', 'dist', '.git'])
const SCANNED_EXTENSIONS = new Set(['.css', '.ts', '.tsx'])

const RAW_CSS_PATTERNS = new Set(['raw-css-declaration', 'unregistered-custom-property-calc'])
const SAFE_AREA_PATTERN = 'safe-area-env-fallback'
const APPLYABLE_GROUPS_UNIVERSE = new Set(['IDENTICAL', 'NORMALIZED'])
export const DEFAULT_GROUPS = ['IDENTICAL', 'NORMALIZED']

// A per-instance tree-depth multiplier (mapping.md finding 6): the custom
// property name varies per component (`--sidebar-indent-depth-px`,
// `--file-tree-view-indent-depth-px`, …) but the shape — `calc(var(--…) *
// 1px)` — is structural, so this is a shape match, not a literal-value
// lookup, and catches a repeat occurrence the ledger counted only once.
const INDENT_DEPTH_CALC_RE = /^calc\(var\(--[\w-]+\)\s*\*\s*1px\)$/i
const SAFE_AREA_TEXT_RE = /safe-area-inset/i
const SAFE_AREA_FLOOR_RE = /max\(/i

const GENERIC_CALC_REFUSAL_REASON = 'This is a computed per-instance multiplier (calc(var(--…) * 1px)), not a constant spacing value — no single spacing token can replace it; needs either a registered indent-step token multiplied per depth, or an explicit scope exception. Refusing rather than guessing (structural match; this exact custom-property name is not enumerated in mapping.json, but the shape is identical to the ones that are).'

// ── mapping.json loading & lookup tables ────────────────────────────────

export function loadMapping(mappingPath) {
  const text = readFileSync(mappingPath, 'utf8')
  const data = JSON.parse(text)
  if (!data || !Array.isArray(data.rows)) throw new Error(`mapping file ${mappingPath} has no "rows" array`)
  return data.rows
}

/** Parses a "prop: value" declaration TEXT (mapping.json's own row.value/row.replacement
 * shape for this script's two families) via postcss — never a regex over the value. Returns
 * null for text that isn't a real single CSS declaration (e.g. a Tailwind bracket utility, or
 * NEEDS-DECISION prose that starts with a capitalized warning, not a property name). */
export function parseDeclarationText(text) {
  const trimmed = String(text ?? '').trim()
  if (trimmed.length === 0) return null
  let root
  try {
    root = postcss.parse(`a{${trimmed}}`)
  } catch {
    return null
  }
  const rule = root.first
  if (!rule || rule.type !== 'rule') return null
  const decl = rule.nodes.find((node) => node.type === 'decl')
  if (!decl) return null
  return { prop: decl.prop.toLowerCase(), value: decl.value.trim() }
}

export function normalizeDeclValue(value) {
  return String(value).trim().replace(/\s+/g, ' ').toLowerCase()
}

function declarationKey(prop, value) {
  return `${String(prop).toLowerCase()}::${normalizeDeclValue(value)}`
}

/** Builds this script's two lookup structures from mapping.json's `rows`, scoped to its two
 * pattern families — every other row (Tailwind utility classes, arbitrary-bracket px/rem, the
 * hairline family, …) belongs to a sibling lane and is ignored here. */
export function buildTables(rows) {
  const rawCssIndex = new Map()
  const safeAreaEntries = []
  for (const row of rows) {
    if (RAW_CSS_PATTERNS.has(row.pattern)) {
      const parsed = parseDeclarationText(row.value)
      if (!parsed) continue
      rawCssIndex.set(declarationKey(parsed.prop, parsed.value), row)
    } else if (row.pattern === SAFE_AREA_PATTERN) {
      const parsedDecl = parseDeclarationText(row.value)
      safeAreaEntries.push({
        row,
        prop: parsedDecl ? parsedDecl.prop : null,
        value: parsedDecl ? parsedDecl.value : row.value,
        isTailwindUtility: !parsedDecl,
        hasFloor: SAFE_AREA_FLOOR_RE.test(row.value),
      })
    }
  }
  return { rawCssIndex, safeAreaEntries }
}

/** The new value text for an APPLYABLE raw-css row, parsed from row.replacement the same
 * way row.value is parsed — never string-munged. Only ever called for IDENTICAL/NORMALIZED
 * rows; a NEEDS-DECISION row's replacement is prose, not a declaration, and is never applied. */
export function extractReplacementValue(row) {
  const parsed = parseDeclarationText(row.replacement)
  if (!parsed) throw new Error(`row.replacement for "${row.value}" is not a parseable CSS declaration: ${row.replacement}`)
  return parsed.value
}

function classifyRawCssRow(row, applyGroups) {
  if (row.group === 'NEEDS-DECISION') {
    return { verdict: 'refuse', reason: row.reason || 'Needs a design decision before this can be applied mechanically (NEEDS-DECISION in mapping.json).' }
  }
  if (applyGroups.has(row.group)) return { verdict: 'apply' }
  return { verdict: 'refuse', reason: `Row is classified ${row.group} in mapping.json, but this run's --groups (${[...applyGroups].join(', ') || 'none'}) does not include it — rerun with --groups=${row.group} to apply it.` }
}

/** The refusal explanation for a safe-area site. `hasFloor` distinguishes the two behaviour-risk
 * sites (already `max(<fixed>, env(...))`, mapping.md finding 5) from a plain missing-fallback
 * site — the WHY differs, so the reason differs, even though both are always refused. */
function classifySafeAreaSite({ hasFloor, matchedRow }) {
  const base = hasFloor
    ? 'Cannot preserve behaviour exactly: this site already enforces a minimum inset via max(<fixed>, env(safe-area-inset-*)), which still applies on a device with a zero inset (no notch/home-indicator). The ledger\'s literal env(prop, fallback) form is not equivalent — env()\'s own fallback argument only fires when env() itself is unsupported, never merely because the inset value is 0 — so mechanically rewriting to that form would silently drop the floor. If this is ever applied by hand, it must keep the max(<token>, env(...)) shape and a human must choose which --space-N token replaces the fixed operand; this script is not authorized to make that token choice, so it refuses and leaves the file untouched, which keeps the existing floor working exactly as it does today.'
    : 'No fallback token has been chosen for this bare env(safe-area-inset-*) declaration yet. Picking one is a central-review design decision (which --space-N reads as "no inset" on a device with none), not a mechanical snap — refusing rather than inventing a fallback, and leaving the file untouched.'
  if (matchedRow && matchedRow.reason) return `${base} (mapping.json: \`${matchedRow.value}\` — ${matchedRow.reason})`
  return base
}

// ── CSS-file path (postcss) ─────────────────────────────────────────────

/** Walks one .css file's declarations with postcss, mutating in place for applyable matches
 * and collecting refusals for everything else this script's two families recognize. Comments,
 * custom properties, `!important`, vendor prefixes, at-rule nesting and declaration order are
 * all preserved automatically — postcss's AST round-trips everything it doesn't touch, and the
 * only node ever mutated is `decl.value` on a proven, applyable match. */
export function planCssFile(repoRoot, filePath, tables, applyGroups) {
  const relPath = relative(repoRoot, filePath)
  const text = readFile(filePath)
  let root
  try {
    root = postcss.parse(text, { from: filePath })
  } catch (error) {
    return {
      relPath,
      before: text,
      after: text,
      edits: [],
      refusals: [{
        file: relPath,
        loc: `${relPath}:${error.line ?? 1}:${error.column ?? 1}`,
        prop: null,
        value: null,
        family: 'parse-error',
        reason: `Failed to parse as CSS (${error.message}); refusing rather than guessing at declarations in an unparseable file.`,
      }],
    }
  }

  const edits = []
  const refusals = []
  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    const value = decl.value
    const loc = `${relPath}:${decl.source?.start?.line ?? '?'}:${decl.source?.start?.column ?? '?'}`
    const row = tables.rawCssIndex.get(declarationKey(prop, value))
    if (row) {
      const verdict = classifyRawCssRow(row, applyGroups)
      if (verdict.verdict === 'apply') {
        const newValue = extractReplacementValue(row)
        edits.push({ loc, prop, before: value, after: newValue })
        decl.value = newValue
      } else {
        refusals.push({ file: relPath, loc, prop, value, family: row.pattern, reason: verdict.reason })
      }
      return
    }
    if (SAFE_AREA_TEXT_RE.test(value)) {
      const matched = tables.safeAreaEntries.find((entry) => !entry.isTailwindUtility && entry.prop === prop && normalizeDeclValue(entry.value) === normalizeDeclValue(value))
      const hasFloor = matched ? matched.hasFloor : SAFE_AREA_FLOOR_RE.test(value)
      const reason = classifySafeAreaSite({ hasFloor, matchedRow: matched ? matched.row : null })
      refusals.push({ file: relPath, loc, prop, value, family: SAFE_AREA_PATTERN, reason })
    }
  })

  const after = edits.length > 0 ? root.toString() : text
  return { relPath, before: text, after, edits, refusals }
}

// ── .ts/.tsx path (TypeScript AST — inline style-object strings + className safe-area) ──

function unwrapExpr(node) {
  let current = node
  while (current) {
    if (ts.isParenthesizedExpression(current)) { current = current.expression; continue }
    if (ts.isAsExpression(current) || ts.isSatisfiesExpression(current) || ts.isNonNullExpression(current)) { current = current.expression; continue }
    if (ts.isTypeAssertionExpression && ts.isTypeAssertionExpression(current)) { current = current.expression; continue }
    break
  }
  return current
}

/** Every object literal a `style={...}` expression could evaluate to, following `??`/`||`/`&&`/
 * ternary branches (SearchModal.tsx: `row.depth > 0 ? { ... } as CSSProperties : undefined`). */
function collectStyleObjectLiterals(expr, out) {
  const node = unwrapExpr(expr)
  if (!node) return
  if (ts.isObjectLiteralExpression(node)) { out.push(node); return }
  if (ts.isConditionalExpression(node)) {
    collectStyleObjectLiterals(node.whenTrue, out)
    collectStyleObjectLiterals(node.whenFalse, out)
    return
  }
  if (ts.isBinaryExpression(node) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken, ts.SyntaxKind.AmpersandAmpersandToken].includes(node.operatorToken.kind)) {
    collectStyleObjectLiterals(node.left, out)
    collectStyleObjectLiterals(node.right, out)
  }
}

/** camelCase (or an already-kebab custom property) JS style key -> CSS property name. */
function cssPropFromStyleKey(key) {
  if (key.startsWith('--')) return key.toLowerCase()
  return key.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)
}

function locOf(ctx, node) {
  const pos = ctx.sourceFile.getLineAndCharacterOfPosition(node.getStart())
  return `${ctx.relPath}:${pos.line + 1}:${pos.character + 1}`
}

/** Shared classification for one (prop, value) declaration site found in EITHER a real CSS
 * declaration or a JS style-object string value — the matching/refusal logic is identical; only
 * how the edit is applied back to source text differs (postcss decl.value vs. a text splice). */
function classifyDeclarationSite(prop, value, tables, applyGroups) {
  const row = tables.rawCssIndex.get(declarationKey(prop, value))
  if (row) {
    const verdict = classifyRawCssRow(row, applyGroups)
    if (verdict.verdict === 'apply') {
      return { kind: 'apply', newValue: extractReplacementValue(row), family: row.pattern }
    }
    return { kind: 'refuse', family: row.pattern, reason: verdict.reason }
  }
  if (INDENT_DEPTH_CALC_RE.test(value.trim())) {
    const matched = [...tables.rawCssIndex.values()].find((candidate) => {
      if (candidate.pattern !== 'unregistered-custom-property-calc') return false
      const parsed = parseDeclarationText(candidate.value)
      return parsed && normalizeDeclValue(parsed.value) === normalizeDeclValue(value)
    })
    return { kind: 'refuse', family: 'unregistered-custom-property-calc', reason: matched ? matched.reason : GENERIC_CALC_REFUSAL_REASON }
  }
  if (SAFE_AREA_TEXT_RE.test(value)) {
    const matched = tables.safeAreaEntries.find((entry) => !entry.isTailwindUtility && entry.prop === prop && normalizeDeclValue(entry.value) === normalizeDeclValue(value))
    const hasFloor = matched ? matched.hasFloor : SAFE_AREA_FLOOR_RE.test(value)
    return { kind: 'refuse', family: SAFE_AREA_PATTERN, reason: classifySafeAreaSite({ hasFloor, matchedRow: matched ? matched.row : null }) }
  }
  return null
}

function planStyleObjectLiteral(objectLiteral, ctx) {
  for (const property of objectLiteral.properties) {
    if (!ts.isPropertyAssignment(property)) continue
    let keyText = null
    if (ts.isIdentifier(property.name)) keyText = property.name.text
    else if (ts.isStringLiteral(property.name)) keyText = property.name.text
    if (keyText === null) continue
    const valueNode = unwrapExpr(property.initializer)
    if (!valueNode || !(ts.isStringLiteral(valueNode) || ts.isNoSubstitutionTemplateLiteral(valueNode))) continue
    const cssProp = cssPropFromStyleKey(keyText)
    const valueText = valueNode.text
    const verdict = classifyDeclarationSite(cssProp, valueText, ctx.tables, ctx.applyGroups)
    if (!verdict) continue
    const loc = locOf(ctx, valueNode)
    if (verdict.kind === 'apply') {
      const quote = valueNode.getText()[0]
      ctx.edits.push({
        start: valueNode.getStart(), end: valueNode.getEnd(), replacement: `${quote}${verdict.newValue}${quote}`,
        loc, prop: cssProp, before: valueText, after: verdict.newValue,
      })
    } else {
      ctx.refusals.push({ file: ctx.relPath, loc, prop: cssProp, value: valueText, family: verdict.family, reason: verdict.reason })
    }
  }
}

/** className/class string literals never get edited by this script (every safe-area site is
 * always refused) — only scanned for a Tailwind arbitrary-bracket safe-area utility token so the
 * refusal reporting is complete across all three safe-area surface shapes (mapping.md's plain
 * declaration / max()-floor declaration / Tailwind bracket utility). */
function planClassNameLiteral(literalNode, ctx) {
  const text = literalNode.text
  if (!SAFE_AREA_TEXT_RE.test(text)) return
  for (const token of text.split(/\s+/).filter(Boolean)) {
    if (!SAFE_AREA_TEXT_RE.test(token)) continue
    const matched = ctx.tables.safeAreaEntries.find((entry) => entry.isTailwindUtility && entry.row.value === token)
    const hasFloor = matched ? matched.hasFloor : SAFE_AREA_FLOOR_RE.test(token)
    const reason = classifySafeAreaSite({ hasFloor, matchedRow: matched ? matched.row : null })
    ctx.refusals.push({ file: ctx.relPath, loc: locOf(ctx, literalNode), prop: null, value: token, family: SAFE_AREA_PATTERN, reason })
  }
}

export function planTsFile(repoRoot, filePath, tables, applyGroups) {
  const relPath = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const sourceFile = ts.createSourceFile(filePath, text, ts.ScriptTarget.Latest, true, filePath.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const ctx = { sourceFile, relPath, tables, applyGroups, edits: [], refusals: [] }

  const visitStyleAttributeLike = (initializer) => {
    const objects = []
    collectStyleObjectLiterals(initializer, objects)
    for (const obj of objects) planStyleObjectLiteral(obj, ctx)
  }

  const visit = (node) => {
    if (ts.isJsxAttribute(node)) {
      const name = node.name.getText()
      if (name === 'style' && node.initializer && ts.isJsxExpression(node.initializer) && node.initializer.expression) {
        visitStyleAttributeLike(node.initializer.expression)
      }
      if ((name === 'className' || name === 'class') && node.initializer) {
        if (ts.isStringLiteral(node.initializer)) {
          planClassNameLiteral(node.initializer, ctx)
        } else if (ts.isJsxExpression(node.initializer) && node.initializer.expression) {
          const expr = unwrapExpr(node.initializer.expression)
          if (expr && (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr))) planClassNameLiteral(expr, ctx)
        }
      }
    } else if (ts.isPropertyAssignment(node)) {
      const name = ts.isIdentifier(node.name) ? node.name.text : (ts.isStringLiteral(node.name) ? node.name.text : null)
      if (name === 'style') visitStyleAttributeLike(node.initializer)
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)

  const after = ctx.edits.length > 0
    ? applyEdits(text, ctx.edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    : text
  return { relPath, before: text, after, edits: ctx.edits, refusals: ctx.refusals }
}

// ── file discovery ───────────────────────────────────────────────────────

function listCandidateFiles(repoRoot) {
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
      else if (SCANNED_EXTENSIONS.has(extname(name))) out.push(full)
    }
  }
  for (const root of ALLOWED_ROOTS) walk(resolve(repoRoot, root))
  return out
}

// ── orchestration ────────────────────────────────────────────────────────

export function runCodemod({ repoRoot, apply = false, groups = DEFAULT_GROUPS, mappingPath }) {
  const resolvedMappingPath = mappingPath ?? resolve(repoRoot, DEFAULT_MAPPING_PATH)
  const rows = loadMapping(resolvedMappingPath)
  const tables = buildTables(rows)
  const applyGroups = new Set((groups ?? []).filter((g) => APPLYABLE_GROUPS_UNIVERSE.has(g)))

  const files = listCandidateFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0

  for (const filePath of files) {
    const result = extname(filePath) === '.css'
      ? planCssFile(repoRoot, filePath, tables, applyGroups)
      : planTsFile(repoRoot, filePath, tables, applyGroups)
    if (result.edits.length === 0 && result.refusals.length === 0) continue
    const diff = result.edits.length > 0 ? unifiedDiff(result.relPath, result.before, result.after) : ''
    fileResults.push({
      file: result.relPath,
      editCount: result.edits.length,
      refusalCount: result.refusals.length,
      edits: result.edits.map((e) => ({ loc: e.loc, prop: e.prop, before: e.before, after: e.after })),
      refusals: result.refusals,
      diff,
    })
    totalEdits += result.edits.length
    totalRefusals += result.refusals.length
    if (apply && result.edits.length > 0) writeFileAtomic(filePath, result.after)
  }

  return {
    mappingPath: resolvedMappingPath,
    appliedGroups: [...applyGroups],
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────

function readFlag(argv, name) {
  const eqPrefix = `--${name}=`
  const eqEntry = argv.find((a) => a.startsWith(eqPrefix))
  if (eqEntry) return eqEntry.slice(eqPrefix.length)
  const index = argv.indexOf(`--${name}`)
  if (index >= 0 && index + 1 < argv.length) return argv[index + 1]
  return undefined
}

const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const argv = process.argv.slice(2)
  const { apply, root } = parseCodemodArgs(argv, resolve(new URL('../..', import.meta.url).pathname))
  const groupsFlag = readFlag(argv, 'groups')
  const groups = groupsFlag ? groupsFlag.split(',').map((s) => s.trim()).filter(Boolean) : DEFAULT_GROUPS
  const mappingFlag = readFlag(argv, 'mapping')
  const mappingPath = mappingFlag ? resolve(mappingFlag) : undefined

  const result = runCodemod({ repoRoot: root, apply, groups, mappingPath })

  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) {
      console.log(`REFUSED ${r.loc} \`${r.prop ?? '(className)'}: ${r.value}\` [${r.family}] — ${r.reason}`)
    }
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    mappingPath: result.mappingPath,
    appliedGroups: result.appliedGroups,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
