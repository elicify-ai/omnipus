#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket C pattern P9
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json,
// L13 probe dist/design-system-baseline/cli-lanes/fanout/L13/).
//
// PATTERN: `(COND ? TPL_A : TPL_B) + SUFFIX` — a finite ternary between two
// class-string leaves (a string literal, a no-substitution template literal,
// or a template literal WITH substitutions, e.g. one that embeds a
// cross-module re-exported string constant via `${IMPORTED_CONST}`), string-
// concatenated with a trailing SUFFIX expression of any shape. Found live at
// src/components/library/preview/BasePreview.tsx's `containerClass`:
//
//   const containerClass =
//     (variant === 'inline'
//       ? `flex ${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]`
//       : 'flex h-full min-h-0 flex-col') + (embed ? ' group' : '')
//
// WHY THE LOCK SEES THIS AS UNSUPPORTED (typography.mjs, not touched by this
// lane — scanner capability ports are a separate lane's job): the top-level
// node is a BinaryExpression(+) . typography.mjs's class-string walker
// (walkClassExpression / walkClassBinary / expandStaticClassStrings /
// collectStaticText / collectStaticTextForBinding) can prove a `+` node
// static ONLY when EVERY leaf on both sides is a plain string/no-substitution
// -template literal — none of those helpers descend into a
// ConditionalExpression whose leaf is a TemplateExpression carrying a `${…}`
// substitution, so the entire `+` node (and therefore anything that reads
// its LHS's cross-module `${INLINE_PREVIEW_BOX_CLASS}`) fails closed as
// `typography/unsupported-text-utility`, even though the SAME cross-module
// identifier resolves cleanly everywhere it is referenced WITHOUT this `+`
// wrapper (e.g. `LIBRARY_ICON_BTN`, `LINK_CLASS`/`UNVERIFIED_LINK_CLASS` —
// see the L13 probe: those sites already scan clean, this shape does not).
//
// THE REWRITE (algebraic, not a scanner change): `(A ? X : Y) + S` is
// value-identical to `A ? (X-with-S-appended) : (Y-with-S-appended)` for ANY
// expression S, with NO purity requirement on S — the ternary already
// evaluates exactly one of X/Y at runtime; folding S into EACH branch as a
// template substitution still evaluates S exactly once per execution (only
// the taken branch's copy ever runs), and in the SAME left-to-right order
// (test, then the chosen branch's own sub-expressions, then S) that the
// original `+` produced. Folding S in as `${S}` rather than re-emitting `+ S`
// removes the top-level BinaryExpression entirely, so the lock's ordinary
// ConditionalExpression/TemplateExpression walkers apply and can resolve
// every substitution independently — including a cross-module const.
//
// SCOPE / REFUSALS: only matches `(COND ? LEAF_A : LEAF_B) + SUFFIX` where
// LEAF_A/LEAF_B (after unwrapping parens) are each a StringLiteral,
// NoSubstitutionTemplateLiteral, or TemplateExpression — nothing else is a
// provable "class leaf" this codemod can textually re-fold. SUFFIX may be
// any expression (see purity argument above — it is embedded verbatim,
// unevaluated by the codemod itself). Refuses when a leaf's static text
// contains a backtick, a backslash, or a literal `${` sequence — folding
// those into a NEW template literal without a real escaping pass could
// change what the new source parses as, and this codemod does no escaping.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

function unwrapParens(node) {
  let current = node
  while (current && ts.isParenthesizedExpression(current)) current = current.expression
  return current
}

function isTemplateLeaf(node) {
  return ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateExpression(node)
}

const UNSAFE_TEXT_RE = /[`\\]|\$\{/

/** Raw text chunks a template leaf is built from — head/tails for a
 * TemplateExpression, or the single decoded string for a plain literal. */
function leafTextChunks(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [node.text]
  return [node.head.text, ...node.templateSpans.map((span) => span.literal.text)]
}

function leafHasUnsafeText(node) {
  return leafTextChunks(node).some((chunk) => UNSAFE_TEXT_RE.test(chunk))
}

/** { head, spans: [{ exprText, tailText }] } — the ordered
 * head/substitution/tail decomposition of a template leaf. */
function templatePieces(node, sourceFile) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return { head: node.text, spans: [] }
  return {
    head: node.head.text,
    spans: node.templateSpans.map((span) => ({ exprText: span.expression.getText(sourceFile), tailText: span.literal.text })),
  }
}

function renderTemplate(piece) {
  const parts = ['`', piece.head]
  for (const span of piece.spans) parts.push('${', span.exprText, '}', span.tailText)
  parts.push('`')
  return parts.join('')
}

/** Processes one file: returns { edits, refusals } — never mutates. */
export function planFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []

  const visit = (node) => {
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = unwrapParens(node.left)
      const loc = source.getLineAndCharacterOfPosition(node.getStart())
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
      if (ts.isConditionalExpression(left)) {
        const whenTrue = unwrapParens(left.whenTrue)
        const whenFalse = unwrapParens(left.whenFalse)
        if (!isTemplateLeaf(whenTrue) || !isTemplateLeaf(whenFalse)) {
          refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(source), reason: 'left ternary branch is not a plain string/template-literal class leaf' })
        } else if (leafHasUnsafeText(whenTrue) || leafHasUnsafeText(whenFalse)) {
          refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(source), reason: 'a ternary branch contains a backtick, backslash, or literal ${ — unsafe to re-fold into a new template literal without an escaping pass' })
        } else {
          const suffixText = node.right.getText(source)
          const truePieces = templatePieces(whenTrue, source)
          const falsePieces = templatePieces(whenFalse, source)
          const trueTemplate = renderTemplate({ head: truePieces.head, spans: [...truePieces.spans, { exprText: suffixText, tailText: '' }] })
          const falseTemplate = renderTemplate({ head: falsePieces.head, spans: [...falsePieces.spans, { exprText: suffixText, tailText: '' }] })
          const replacement = `${left.condition.getText(source)} ? ${trueTemplate} : ${falseTemplate}`
          edits.push({ start: node.getStart(), end: node.getEnd(), replacement, loc: locStr, syntax: node.getText(source) })
          return // do not descend into an edited node's children
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals, text, source }
}

export function runCodemod({ repoRoot, apply }) {
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) {
      after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    }
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
      after,
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
