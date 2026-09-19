#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket D pattern P12
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json).
//
// Pattern: depth-based tree indentation computed with ad hoc pixel arithmetic
// directly inside a JSX `style={{ paddingLeft: ... }}` (or a top-level
// conditional between such an object and `undefined`), e.g.
// `paddingLeft: \`${entry.indent * 12}px\`` or `paddingLeft: indent + 18`.
// scripts/design-system-locks/spacing.mjs's `analyzeStyleValue` only reads a
// NumericLiteral, negative NumericLiteral, StringLiteral or
// NoSubstitutionTemplateLiteral directly at the property site — any other
// expression (an identifier, a binary expression, a template WITH a
// substitution) is unconditionally `spacing/unsupported`, regardless of what
// the expression computes. That is a scanner *capability* gap, not a design
// decision about the pixel values themselves — the lead's ruling (see
// dist/design-system-baseline/cli-lanes/fanout/L5/) is to rewrite the
// EXPRESSION SHAPE only, with byte-identical computed output, so these six
// findings become ordinary (baseline-eligible) spacing debt the scanner can
// actually see, instead of the fail-closed-exempt `spacing/unsupported`
// bucket. Snapping the pixel values themselves to the 4/8px scale is
// out of scope here — that is C1 work once these are real findings.
//
// The trick: move the exact original (runtime, depth-dependent) arithmetic
// expression — byte for byte, unchanged — into a CSS custom property on the
// same inline style object, and replace the `paddingLeft` value with a
// STATIC (no interpolation) `calc(var(--<name>) * 1px)` string. The custom
// property carries the same number the original expression always computed;
// multiplying by the literal `1px` turns that unitless number into a length
// with the same magnitude, so the rendered padding is unchanged for every
// runtime depth (not just a bounded sample) — `<name>` is not one of the
// registered spacing tokens, so the scanner reports `spacing/invalid-var`
// (real, actionable, non-unsupported debt) instead of silently claiming
// success. React's inline-style type (`CSSProperties`) has no index
// signature for custom properties (deliberately, per @types/react), so the
// rewritten object needs an `as import('react').CSSProperties` cast — using
// the inline `import(...)` type form so this codemod never has to add or
// merge an import statement.
//
// Scope (deliberately narrow, re-checked structurally on every run, never a
// frozen file list): a JSX `style={...}` attribute — optionally a top-level
// ternary between an object literal and `undefined`/another object literal,
// mirroring spacing.mjs's own `visitStyleLike` conditional handling — whose
// object literal has a `paddingLeft` property whose value is either (a) a
// "depth-linear arithmetic" expression (identifiers/property-accesses
// combined only with `+`, `-`, `*` and numeric literals, containing at least
// one non-literal atom), or (b) a template literal of the exact shape
// `` `${<depth-linear arithmetic>}px` `` (no other literal text). Anything
// else that still reaches a `paddingLeft` value non-literally (a function
// call, a ternary property value, a spread, a shorthand property, a
// computed property name, an ambiguous template) is refused and reported,
// never silently skipped or guessed at.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  isInAllowedRoot, listSourceFiles, parseCodemodArgs, parseSourceFile,
  readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const TARGET_PROPERTY_NAME = 'paddingLeft'
const CAST_SUFFIX = ` as import('react').CSSProperties`

// ── Shape recognition ───────────────────────────────────────────────────────

function unwrapParens(node) {
  let current = node
  while (current && ts.isParenthesizedExpression(current)) current = current.expression
  return current
}

/** `foo`, `foo.bar`, `foo.bar.baz` — a non-optional, non-computed chain of
 * identifier property reads. Never a call, an element access or an
 * optional-chain step (any of those could have side effects or produce a
 * value this codemod cannot prove is a plain number). */
function isPlainAtom(node) {
  if (ts.isIdentifier(node)) return true
  if (ts.isPropertyAccessExpression(node) && !node.questionDotToken) return isPlainAtom(node.expression)
  return false
}

const LINEAR_OPERATORS = new Set([ts.SyntaxKind.PlusToken, ts.SyntaxKind.MinusToken, ts.SyntaxKind.AsteriskToken])

/**
 * Recognizes "depth-linear arithmetic": any expression built from numeric
 * literals and plain atoms (see isPlainAtom) combined only with +, - and *.
 * Returns { ok, hasAtom } — hasAtom distinguishes a pure-literal expression
 * (already scanner-readable, no rewrite needed) from one that genuinely
 * depends on a runtime identifier (the P12 shape this codemod targets).
 */
function classifyLinearTerm(node) {
  const n = unwrapParens(node)
  if (!n) return { ok: false }
  if (ts.isNumericLiteral(n)) return { ok: true, hasAtom: false }
  if (ts.isPrefixUnaryExpression(n) && n.operator === ts.SyntaxKind.MinusToken && ts.isNumericLiteral(n.operand)) {
    return { ok: true, hasAtom: false }
  }
  if (isPlainAtom(n)) return { ok: true, hasAtom: true }
  if (ts.isBinaryExpression(n) && LINEAR_OPERATORS.has(n.operatorToken.kind)) {
    const left = classifyLinearTerm(n.left)
    if (!left.ok) return { ok: false }
    const right = classifyLinearTerm(n.right)
    if (!right.ok) return { ok: false }
    return { ok: true, hasAtom: left.hasAtom || right.hasAtom }
  }
  return { ok: false }
}

function isDepthLinearArithmetic(node) {
  const result = classifyLinearTerm(node)
  return result.ok && result.hasAtom
}

/**
 * Classifies one `paddingLeft` value expression.
 *   - { kind: 'skip' }: already a literal the scanner reads directly — no
 *     rewrite needed (also the idempotency case: what a prior run left behind).
 *   - { kind: 'candidate', exprNode }: the P12 shape; exprNode is the exact
 *     runtime arithmetic sub-expression to relocate into the custom property.
 *   - { kind: 'refuse', reason }: a non-literal paddingLeft value this
 *     codemod cannot safely prove byte-identical for — reported, never edited.
 */
function classifyPaddingLeftValue(valueExpr) {
  const v = unwrapParens(valueExpr)
  if (ts.isNumericLiteral(v) || ts.isStringLiteral(v) || ts.isNoSubstitutionTemplateLiteral(v)) {
    return { kind: 'skip' }
  }
  if (ts.isTemplateExpression(v)) {
    if (v.templateSpans.length !== 1) {
      return { kind: 'refuse', reason: `template literal has ${v.templateSpans.length} substitutions, expected exactly 1` }
    }
    const span = v.templateSpans[0]
    if (v.head.text !== '') {
      return { kind: 'refuse', reason: `template literal has leading text ${JSON.stringify(v.head.text)} before the substitution` }
    }
    if (span.literal.text !== 'px') {
      return { kind: 'refuse', reason: `template literal trailing text is ${JSON.stringify(span.literal.text)}, expected exactly "px"` }
    }
    if (!isDepthLinearArithmetic(span.expression)) {
      return { kind: 'refuse', reason: 'template substitution is not depth-linear arithmetic (identifiers/property-accesses combined only with +, -, * and numeric literals)' }
    }
    return { kind: 'candidate', exprNode: span.expression }
  }
  if (isDepthLinearArithmetic(v)) {
    return { kind: 'candidate', exprNode: v }
  }
  return { kind: 'refuse', reason: `unrecognized paddingLeft value shape (${ts.SyntaxKind[v.kind]})` }
}

// ── Style-object discovery ──────────────────────────────────────────────────

/** Every ObjectLiteralExpression reachable from a `style={...}` value by
 * unwrapping parens and top-level ternaries — mirrors spacing.mjs's own
 * `visitStyleLike` conditional handling (`x ? {...} : undefined`), so this
 * codemod finds exactly the objects the scanner itself would visit. Anything
 * else reachable that way (an identifier, undefined, a call) is left alone —
 * it is not a literal this codemod can safely rewrite in place. */
function collectStyleObjectLiterals(styleValueExpr, out = []) {
  const node = unwrapParens(styleValueExpr)
  if (!node) return out
  if (ts.isConditionalExpression(node)) {
    collectStyleObjectLiterals(node.whenTrue, out)
    collectStyleObjectLiterals(node.whenFalse, out)
    return out
  }
  if (ts.isAsExpression(node) || ts.isSatisfiesExpression(node)) {
    collectStyleObjectLiterals(node.expression, out)
    return out
  }
  if (ts.isObjectLiteralExpression(node)) out.push(node)
  return out
}

function styleValueExprFromNode(node) {
  if (ts.isJsxAttribute(node) && node.name.getText() === 'style') {
    if (node.initializer && ts.isJsxExpression(node.initializer) && node.initializer.expression) {
      return node.initializer.expression
    }
    return null
  }
  if (ts.isPropertyAssignment(node)) {
    const name = node.name
    const isStyleKey = (ts.isIdentifier(name) && name.text === 'style') || (ts.isStringLiteral(name) && name.text === 'style')
    if (isStyleKey) return node.initializer
    return null
  }
  return null
}

function kebabFileSlug(filePath) {
  const base = filePath.replace(/^.*[\\/]/, '').replace(/\.[jt]sx?$/, '')
  return base
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .replace(/[^A-Za-z0-9]+/g, '-')
    .toLowerCase()
    .replace(/^-+|-+$/g, '')
}

// ── Planning ─────────────────────────────────────────────────────────────

/** Processes one file: returns { edits, refusals } — never mutates. */
export function planFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  const cssVarName = `--${kebabFileSlug(filePath)}-indent-depth-px`

  const locOf = (node) => {
    const loc = source.getLineAndCharacterOfPosition(node.getStart())
    return `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
  }

  const planObject = (obj) => {
    const candidates = []
    for (const prop of obj.properties) {
      if (ts.isSpreadAssignment(prop)) continue
      const name = prop.name && (ts.isIdentifier(prop.name) || ts.isStringLiteral(prop.name)) ? prop.name.text : null
      if (name !== TARGET_PROPERTY_NAME) continue
      if (ts.isShorthandPropertyAssignment(prop)) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locOf(prop), syntax: prop.getText(), reason: 'shorthand paddingLeft property; cannot safely rewrite in place' })
        continue
      }
      if (!ts.isPropertyAssignment(prop)) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locOf(prop), syntax: prop.getText(), reason: `unrecognized property kind (${ts.SyntaxKind[prop.kind]})` })
        continue
      }
      // prop.name is guaranteed Identifier|StringLiteral here — `name` above
      // only resolves to TARGET_PROPERTY_NAME for those two node kinds, so a
      // computed property name (`[expr]: ...`) never reaches this point; it
      // fell through the `name !== TARGET_PROPERTY_NAME` check above instead.
      const classified = classifyPaddingLeftValue(prop.initializer)
      if (classified.kind === 'skip') continue
      if (classified.kind === 'refuse') {
        refusals.push({ file: relative(repoRoot, filePath), loc: locOf(prop), syntax: prop.getText(), reason: classified.reason })
        continue
      }
      candidates.push({ prop, exprNode: classified.exprNode })
    }
    if (candidates.length === 0) return

    const collision = obj.properties.some((p) => {
      const n = p.name && (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name)) ? p.name.text : null
      return n === cssVarName
    })
    if (collision) {
      refusals.push({ file: relative(repoRoot, filePath), loc: locOf(obj), syntax: obj.getText(), reason: `generated custom property ${cssVarName} already exists on this object` })
      return
    }
    if (obj.parent && ts.isAsExpression(obj.parent)) {
      // Already cast (a prior run, or hand-authored) — do not double-cast.
      // Still emit the property edits; the existing cast covers them too.
    }

    for (const { prop, exprNode } of candidates) {
      const exprText = exprNode.getText(source)
      const replacement = `'${cssVarName}': ${exprText}, ${TARGET_PROPERTY_NAME}: 'calc(var(${cssVarName}) * 1px)'`
      edits.push({ start: prop.getStart(), end: prop.getEnd(), replacement, loc: locOf(prop), syntax: prop.getText() })
    }
    if (!(obj.parent && ts.isAsExpression(obj.parent))) {
      edits.push({ start: obj.getEnd(), end: obj.getEnd(), replacement: CAST_SUFFIX, loc: locOf(obj), syntax: '(object literal end)' })
    }
  }

  const visit = (node) => {
    const styleValue = styleValueExprFromNode(node)
    if (styleValue) {
      for (const obj of collectStyleObjectLiterals(styleValue)) planObject(obj)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)

  return { edits, refusals, text, source }
}

/** Applies non-overlapping {start, end, replacement} edits, splicing from the
 * highest offset down. Local (not codemod-lib's applyEdits) because this
 * codemod deliberately allows a property-replace edit and a same-file,
 * later-offset cast-insertion edit to coexist — codemod-lib's version throws
 * on any edit whose end exceeds the previous edit's start, which a
 * zero-width insertion immediately after a replaced range can trip; sorting
 * by start descending and splicing is still safe as long as ranges do not
 * overlap, which planFileEdits guarantees (cast insertion is always at
 * obj.getEnd(), strictly after every property it was derived from). */
function applyPlannedEdits(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start || b.end - a.end)
  let result = text
  for (const edit of sorted) {
    result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
  }
  return result
}

export function runCodemod({ repoRoot, apply }) {
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0

  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    const after = edits.length > 0 ? applyPlannedEdits(text, edits) : text
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
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
    totalFilesScanned: files.length,
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
