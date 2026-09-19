#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket A pattern P1
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json).
//
// Pattern: `src/lib/toolStatusConfig.tsx`'s two exported status-config
// factories, `getToolBadgeStatusConfig` and `getSpanStatusDot`, each declare
// an optional `textClass?: string` field on their return type but NEVER
// assign it in any branch — every returned object literal omits the key, so
// `.textClass` on a value produced ONLY by calls to these two functions is
// always exactly `undefined`. `cn('...', X.textClass)` where X is such a
// value is therefore byte-for-byte identical to `cn('...')` at every call —
// `cn`/`clsx` treat `undefined` arguments as no-ops. Removing the dead
// argument changes nothing about the rendered DOM.
//
// This codemod re-derives "never assigns textClass" from
// src/lib/toolStatusConfig.tsx itself on every run (it does not hardcode the
// fact) — if a future edit adds `textClass` to either function, the codemod
// stops treating that function as safe and closes zero (or fewer) findings,
// rather than silently miscompiling code.
//
// It targets the SOURCE PATTERN, not a frozen file list: it scans every
// .ts/.tsx file under src/ and packages/ui/src/ for `<ident>.textClass`
// reads, and only edits a site when it can prove EVERY possible origin of
// `<ident>` at that point is one of:
//   - a call to a function proven safe above, or
//   - an object literal that itself omits `textClass`.
// A `??`/`||`/ternary chain is followed into both operands; anything else
// (a property access, an unproven function call, a bare identifier from
// elsewhere) makes the codemod refuse that occurrence and report why.
// It only removes the argument when it is a DIRECT argument to a recognized
// class-composing call (`cn`, `clsx`, `classes`) — never elsewhere.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, enclosingFunctionBody, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const SHARED_MODULE_REL = 'src/lib/toolStatusConfig.tsx'
const TARGET_FUNCTION_NAMES = ['getToolBadgeStatusConfig', 'getSpanStatusDot']
const CLASS_BUILDER_NAMES = new Set(['cn', 'clsx', 'classes', 'cx', 'classNames'])
const PROPERTY_NAME = 'textClass'

/** Parses src/lib/toolStatusConfig.tsx and returns the subset of
 * TARGET_FUNCTION_NAMES whose every returned object literal omits `textClass`. */
export function computeSafeFunctionNames(repoRoot) {
  const filePath = resolve(repoRoot, SHARED_MODULE_REL)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const safe = new Set()
  const checked = new Set()

  const objectLiteralOmitsTextClass = (obj) =>
    !obj.properties.some((p) => {
      const name = p.name && ts.isIdentifier(p.name) ? p.name.text : undefined
      return name === PROPERTY_NAME
    })

  const visit = (node) => {
    if (ts.isFunctionDeclaration(node) && node.name && TARGET_FUNCTION_NAMES.includes(node.name.text)) {
      const fnName = node.name.text
      checked.add(fnName)
      let neverAssigns = true
      const visitBody = (n) => {
        if (ts.isObjectLiteralExpression(n)) {
          if (!objectLiteralOmitsTextClass(n)) neverAssigns = false
        }
        ts.forEachChild(n, visitBody)
      }
      if (node.body) visitBody(node.body)
      if (neverAssigns) safe.add(fnName)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)

  return { safe, checked, filePath }
}

function resolvesToSharedModule(specifier) {
  return /(^|\/)lib\/toolStatusConfig$/.test(specifier)
}

/** Local identifier names bound (via import) to a safe factory function in this file. */
function collectSafeLocalImportNames(source, safeFunctionNames) {
  const names = new Set()
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    if (!ts.isStringLiteral(statement.moduleSpecifier)) continue
    if (!resolvesToSharedModule(statement.moduleSpecifier.text)) continue
    const clause = statement.importClause
    const namedBindings = clause && clause.namedBindings
    if (!namedBindings || !ts.isNamedImports(namedBindings)) continue
    for (const el of namedBindings.elements) {
      const importedName = (el.propertyName ?? el.name).text
      if (safeFunctionNames.has(importedName)) names.add(el.name.text)
    }
  }
  return names
}

function calleeName(expr) {
  return ts.isIdentifier(expr) ? expr.text : undefined
}

/** Classifies one candidate origin expression as safe/unsafe, flattening ??/||/ternary/parens. */
function classifyOrigin(node, safeLocalNames) {
  if (ts.isParenthesizedExpression(node)) return classifyOrigin(node.expression, safeLocalNames)
  if (ts.isConditionalExpression(node)) {
    return [...classifyOrigin(node.whenTrue, safeLocalNames), ...classifyOrigin(node.whenFalse, safeLocalNames)]
  }
  if (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken)) {
    return [...classifyOrigin(node.left, safeLocalNames), ...classifyOrigin(node.right, safeLocalNames)]
  }
  if (ts.isCallExpression(node)) {
    const name = calleeName(node.expression)
    if (name && safeLocalNames.has(name)) return [{ safe: true }]
    return [{ safe: false, reason: `call to unproven function \`${name ?? node.expression.getText()}\`` }]
  }
  if (ts.isObjectLiteralExpression(node)) {
    const hasTextClass = node.properties.some((p) => p.name && ts.isIdentifier(p.name) && p.name.text === PROPERTY_NAME)
    if (hasTextClass) return [{ safe: false, reason: 'object literal explicitly sets textClass' }]
    return [{ safe: true }]
  }
  return [{ safe: false, reason: `unrecognized origin shape (${ts.SyntaxKind[node.kind]})` }]
}

/** Finds every assignment origin for `identifierName` within `scopeBody` (declaration initializer + `x = ...` reassignments). */
function findOrigins(scopeBody, identifierName) {
  const origins = []
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === identifierName && node.initializer) {
      origins.push(node.initializer)
    }
    if (
      ts.isBinaryExpression(node) &&
      node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
      ts.isIdentifier(node.left) &&
      node.left.text === identifierName
    ) {
      origins.push(node.right)
    }
    // Do not descend into nested function-like scopes — a same-named
    // identifier there is a different binding, not a reassignment of ours.
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node) || ts.isMethodDeclaration(node)) return
    ts.forEachChild(node, visit)
  }
  visit(scopeBody)
  return origins
}

/** Processes one file: returns { edits, refusals } — never mutates. */
export function planFileEdits(repoRoot, filePath, safeFunctionNames) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const safeLocalNames = collectSafeLocalImportNames(source, safeFunctionNames)
  const edits = []
  const refusals = []
  if (safeLocalNames.size === 0) return { edits, refusals } // file doesn't import either safe factory — nothing to check

  const visit = (node) => {
    if (
      ts.isPropertyAccessExpression(node) &&
      node.name.text === PROPERTY_NAME &&
      ts.isIdentifier(node.expression)
    ) {
      const identifierName = node.expression.text
      const scopeBody = enclosingFunctionBody(node)
      const origins = findOrigins(scopeBody, identifierName)
      const loc = source.getLineAndCharacterOfPosition(node.getStart())
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`

      if (origins.length === 0) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(), reason: 'could not resolve any declaration/assignment for the base identifier in this scope' })
        return
      }
      const classifications = origins.flatMap((o) => classifyOrigin(o, safeLocalNames))
      const unsafe = classifications.filter((c) => !c.safe)
      if (unsafe.length > 0) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(), reason: unsafe.map((u) => u.reason).join('; ') })
        return
      }

      // Proven dead. Only auto-remove when used as a direct argument to a
      // recognized class-composing call — otherwise refuse (too different a
      // context to prove invisibility mechanically).
      const parent = node.parent
      if (!(ts.isCallExpression(parent) && parent.arguments.includes(node) && CLASS_BUILDER_NAMES.has(calleeName(parent.expression) ?? ''))) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(), reason: `proven dead but not a direct argument to a recognized class-composing call (found inside: ${ts.SyntaxKind[parent.kind]})` })
        return
      }

      const args = parent.arguments
      const index = args.indexOf(node)
      let start
      let end = node.getEnd()
      if (args.length === 1) {
        // cn(X.textClass) -> cn()
        start = node.getStart()
      } else if (index === args.length - 1) {
        // last of several -> remove the preceding ", "
        const prev = args[index - 1]
        start = prev.getEnd()
      } else {
        // not last -> remove this arg and the following ", "
        start = node.getStart()
        const next = args[index + 1]
        end = next.getStart()
      }
      edits.push({ start, end, replacement: '', loc: locStr, syntax: node.getText() })
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals, text, source }
}

export function runCodemod({ repoRoot, apply }) {
  const { safe: safeFunctionNames, checked } = computeSafeFunctionNames(repoRoot)
  const notSafe = TARGET_FUNCTION_NAMES.filter((n) => !safeFunctionNames.has(n))
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))

  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath, safeFunctionNames)
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
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    safeFunctionNames: [...safeFunctionNames],
    unsafeFunctionNames: notSafe,
    checkedFunctionNames: [...checked],
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
  if (result.unsafeFunctionNames.length > 0) {
    console.error(`WARNING: ${result.unsafeFunctionNames.join(', ')} now assign textClass in at least one branch — codemod treats them as unsafe and will not close findings on their consumers.`)
  }
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    safeFunctionNames: result.safeFunctionNames,
    unsafeFunctionNames: result.unsafeFunctionNames,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
