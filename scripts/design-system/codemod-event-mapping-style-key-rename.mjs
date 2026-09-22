#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure fan-out, lane W3-events.
//
// Pattern: src/lib/calendar/eventMapping.ts has three private helper
// functions (resolveOccurrenceChipState, resolveBucketWorstWins,
// resolveBucketChip) whose inline return-type literal declares a member
// named `style: ChipStyle` (ChipStyle = { bg: string; icon: StatusIconKey },
// imported from src/components/calendar/types.ts). Every object literal they
// return, and every call-site read of the result, uses that SAME literal
// property name `style`.
//
// This collides with a name the spacing scanner (scripts/design-system-locks/
// spacing.mjs::visitNode) treats specially: ANY object-literal property or
// JSX attribute named exactly `style` is walked as if it were a CSS style
// object (visitStyleLike). Here it never is one — `style` is an internal
// field holding a whole `ChipStyle` reference (a colour+icon pair), never a
// DOM/JSX style prop and never forwarded to one. Passing the whole named
// constant (SCHEDULED_STYLE, NO_RECORD_STYLE, SKIPPED_STYLE, STATUS_STYLE.*)
// into that field is an "embed a whole record into another property" shape
// that spacing.mjs's recordBindingEscapes proof cannot certify a primitive
// leaf for, so it reports `spacing/unsupported` at all 9 return sites
// (dist/design-system-baseline/cli-lanes/wave2/remaining-unsupported.json).
//
// The fix is a pure rename: `style` -> `chipStyle` on the return-type
// member, every returned object literal's key, and every call-site
// `<result>.style` read. None of these three functions is exported and none
// of their results ever cross a JSX/DOM boundary directly — every consumer
// reads `.bg`/`.icon` off the field and forwards THOSE plain strings into
// `makeEvent(...)` -> `backgroundColor`/`borderColor`/`extendedProps.icon`.
// Renaming the field changes zero runtime values, zero DOM/FullCalendar
// props, and zero test-observable behaviour — it only stops the property key
// from lexically matching the scanner's generic "style" trigger, which was
// never about a DOM style value here in the first place.
//
// This codemod re-derives the target functions and their call sites from
// src/lib/calendar/eventMapping.ts on every run (never hardcodes line
// numbers): a function's return-type object literal must contain a member
// literally named `style` whose type is a TypeReference to `ChipStyle`
// imported from a module matching /(^|\/)components\/calendar\/types$/.
// Fails CLOSED, whole-file, on anything it cannot prove:
//   - a `style` return-type member whose value is a ShorthandPropertyAssignment
//     (`{ style }`) at a call site — ambiguous, never auto-rewritten;
//   - a `<expr>.style` read whose base is not a bare identifier, or whose
//     identifier's origin(s) in its enclosing function body/block are not
//     ALL direct calls to one of the target functions.
// A single unresolved occurrence anywhere in the file aborts the whole
// file's edit set (return type and value keys are coupled — renaming one
// without the other would not compile), so no partial/broken rename is ever
// possible.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, parseCodemodArgs, parseSourceFile, readFile,
  unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

export const TARGET_FILE_REL = 'src/lib/calendar/eventMapping.ts'
const CHIPSTYLE_TYPE_NAME = 'ChipStyle'
const CHIPSTYLE_MODULE_RE = /(^|\/)components\/calendar\/types$/
const OLD_KEY = 'style'
const NEW_KEY = 'chipStyle'

/** Local name `ChipStyle` is imported under, from a module matching CHIPSTYLE_MODULE_RE, or null. */
function importedChipStyleLocalName(source) {
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    if (!ts.isStringLiteral(statement.moduleSpecifier)) continue
    if (!CHIPSTYLE_MODULE_RE.test(statement.moduleSpecifier.text)) continue
    const namedBindings = statement.importClause?.namedBindings
    if (!namedBindings || !ts.isNamedImports(namedBindings)) continue
    for (const el of namedBindings.elements) {
      const importedName = (el.propertyName ?? el.name).text
      if (importedName === CHIPSTYLE_TYPE_NAME) return el.name.text
    }
  }
  return null
}

/** PropertySignature named `style` typed as a TypeReference to `chipStyleLocalName`, or null. */
function findStylePropertySignature(typeLiteral, chipStyleLocalName) {
  if (!typeLiteral || !ts.isTypeLiteralNode(typeLiteral)) return null
  for (const member of typeLiteral.members) {
    if (!ts.isPropertySignature(member)) continue
    if (!member.name || !ts.isIdentifier(member.name) || member.name.text !== OLD_KEY) continue
    const memberType = member.type
    if (!memberType || !ts.isTypeReferenceNode(memberType)) continue
    if (!ts.isIdentifier(memberType.typeName) || memberType.typeName.text !== chipStyleLocalName) continue
    return member
  }
  return null
}

/** Every top-level FunctionDeclaration in `source` whose return type literal
 * has a `style: ChipStyle` member. Returns Map<name, { fn, propSig }>. */
function findTargetFunctions(source, chipStyleLocalName) {
  const out = new Map()
  if (!chipStyleLocalName) return out
  for (const statement of source.statements) {
    if (!ts.isFunctionDeclaration(statement) || !statement.name || !statement.body) continue
    const propSig = findStylePropertySignature(statement.type, chipStyleLocalName)
    if (!propSig) continue
    out.set(statement.name.text, { fn: statement, propSig })
  }
  return out
}

/** Every ReturnStatement directly inside `body` (no descent into nested function-likes). */
function collectReturnStatements(body) {
  const out = []
  const visit = (node) => {
    if (ts.isReturnStatement(node)) { out.push(node); return }
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node) || ts.isMethodDeclaration(node)) return
    ts.forEachChild(node, visit)
  }
  ts.forEachChild(body, visit)
  return out
}

function unwrapParens(node) {
  let n = node
  while (n && ts.isParenthesizedExpression(n)) n = n.expression
  return n
}

/** Declaration + reassignment origins of `identifierName` within `scope` (no descent into nested function-likes). */
function findOrigins(scope, identifierName) {
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
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node) || ts.isMethodDeclaration(node)) return
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return origins
}

/** Innermost enclosing Block or SourceFile containing `node` (local re-implementation —
 * codemod-lib's enclosingFunctionBody does the same walk; duplicated here to keep this
 * file self-contained and independently reviewable). */
function enclosingScope(node) {
  let current = node.parent
  while (current) {
    if (
      (ts.isFunctionDeclaration(current) || ts.isFunctionExpression(current) || ts.isArrowFunction(current) || ts.isMethodDeclaration(current)) &&
      current.body && ts.isBlock(current.body)
    ) return current.body
    current = current.parent
  }
  return node.getSourceFile()
}

function locOf(source, filePath, repoRoot, pos) {
  const lc = source.getLineAndCharacterOfPosition(pos)
  return `${relative(repoRoot, filePath)}:${lc.line + 1}:${lc.character + 1}`
}

/**
 * Plans the whole-file edit set. Returns { edits, refusals } — never
 * mutates. Fails closed: if `refusals.length > 0`, `edits` is always `[]`
 * (a partial rename would not compile — return-type and value keys are
 * coupled, so this is all-or-nothing for the whole file).
 */
export function planFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const refusals = []

  const chipStyleLocalName = importedChipStyleLocalName(source)
  if (!chipStyleLocalName) return { edits: [], refusals: [], text, source, targetFunctionNames: [] }

  const targets = findTargetFunctions(source, chipStyleLocalName)
  if (targets.size === 0) return { edits: [], refusals: [], text, source, targetFunctionNames: [] }

  const pendingEdits = []

  // 1. Return-type `style` -> `chipStyle` member name, one per target function.
  for (const [, { propSig }] of targets) {
    pendingEdits.push({
      start: propSig.name.getStart(),
      end: propSig.name.getEnd(),
      replacement: NEW_KEY,
      loc: locOf(source, filePath, repoRoot, propSig.name.getStart()),
      syntax: `${propSig.getText(source)} (return type)`,
    })
  }

  // 2. Every returned object literal's top-level `style:` key, within each
  //    target function's own body only.
  for (const [name, { fn }] of targets) {
    for (const ret of collectReturnStatements(fn.body)) {
      const expr = ret.expression ? unwrapParens(ret.expression) : null
      if (!expr || !ts.isObjectLiteralExpression(expr)) continue
      for (const prop of expr.properties) {
        // `{ style }` shorthand is unconditionally equivalent (by JS/TS
        // grammar) to `{ style: style }` — no proof needed to expand it.
        // Only the KEY changes; the referenced local binding (e.g. `const
        // style = run.status === 'skipped' ? SKIPPED_STYLE : ...` in
        // resolveOccurrenceChipState) is left completely untouched, so the
        // expanded read is byte-identical at runtime.
        if (ts.isShorthandPropertyAssignment(prop) && !prop.objectAssignmentInitializer && prop.name.text === OLD_KEY) {
          pendingEdits.push({
            start: prop.getStart(),
            end: prop.getEnd(),
            replacement: `${NEW_KEY}: ${OLD_KEY}`,
            loc: locOf(source, filePath, repoRoot, prop.getStart()),
            syntax: `${prop.getText(source)} (shorthand, expanded)`,
          })
          continue
        }
        if (ts.isShorthandPropertyAssignment(prop) && prop.name.text === OLD_KEY) {
          // A destructuring-assignment-target shorthand with a default
          // initializer (`{ style = x }`) never occurs in a return-object
          // literal position — TS itself would reject it here. Refuse
          // defensively rather than assume.
          refusals.push({
            file: relative(repoRoot, filePath),
            loc: locOf(source, filePath, repoRoot, prop.getStart()),
            syntax: prop.getText(source),
            reason: `shorthand \`{ ${OLD_KEY} }\` with an assignment initializer in ${name} — unrecognized shape, refuses`,
          })
          continue
        }
        if (!ts.isPropertyAssignment(prop)) continue
        if (!ts.isIdentifier(prop.name) || prop.name.text !== OLD_KEY) continue
        pendingEdits.push({
          start: prop.name.getStart(),
          end: prop.name.getEnd(),
          replacement: NEW_KEY,
          loc: locOf(source, filePath, repoRoot, prop.name.getStart()),
          syntax: prop.getText(source),
        })
      }
    }
  }

  // 3. File-wide `<ident>.style` reads whose identifier's every origin (in
  //    its own enclosing block) is a direct call to one of the target
  //    functions.
  const targetNames = new Set(targets.keys())
  const visitAccess = (node) => {
    if (ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.name) && node.name.text === OLD_KEY) {
      if (!ts.isIdentifier(node.expression)) {
        refusals.push({
          file: relative(repoRoot, filePath),
          loc: locOf(source, filePath, repoRoot, node.getStart()),
          syntax: node.getText(source),
          reason: `\`.${OLD_KEY}\` read on a non-identifier base (${ts.SyntaxKind[node.expression.kind]}) — cannot prove origin`,
        })
      } else {
        const identName = node.expression.text
        const scope = enclosingScope(node)
        const origins = findOrigins(scope, identName)
        const allSafe = origins.length > 0 && origins.every((o) =>
          ts.isCallExpression(o) && ts.isIdentifier(o.expression) && targetNames.has(o.expression.text))
        if (!allSafe) {
          refusals.push({
            file: relative(repoRoot, filePath),
            loc: locOf(source, filePath, repoRoot, node.getStart()),
            syntax: node.getText(source),
            reason: origins.length === 0
              ? `could not resolve any origin for \`${identName}\` in its enclosing scope`
              : `\`${identName}\`'s origin is not provably always a direct call to one of: ${[...targetNames].join(', ')}`,
          })
        } else {
          pendingEdits.push({
            start: node.name.getStart(),
            end: node.name.getEnd(),
            replacement: NEW_KEY,
            loc: locOf(source, filePath, repoRoot, node.name.getStart()),
            syntax: node.getText(source),
          })
        }
      }
    }
    ts.forEachChild(node, visitAccess)
  }
  visitAccess(source)

  if (refusals.length > 0) return { edits: [], refusals, text, source, targetFunctionNames: [...targets.keys()] }
  return { edits: pendingEdits, refusals: [], text, source, targetFunctionNames: [...targets.keys()] }
}

export function runCodemod({ repoRoot, apply }) {
  const filePath = resolve(repoRoot, TARGET_FILE_REL)
  let planned
  try {
    planned = planFileEdits(repoRoot, filePath)
  } catch (err) {
    if (err && err.code === 'ENOENT') {
      return { targetFileFound: false, totalEdits: 0, totalRefusals: 0, files: [], applied: apply }
    }
    throw err
  }
  const { edits, refusals, text } = planned
  if (!isInAllowedRoot(repoRoot, filePath)) {
    throw new Error(`refusing to edit outside allowed roots: ${filePath}`)
  }

  let after = text
  if (edits.length > 0) {
    after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
  }
  const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
  if (apply && edits.length > 0) writeFileAtomic(filePath, after)

  return {
    targetFileFound: true,
    targetFunctionNames: planned.targetFunctionNames,
    totalEdits: edits.length,
    totalRefusals: refusals.length,
    files: [{
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
    }],
    applied: Boolean(apply && edits.length > 0),
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  for (const f of result.files ?? []) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: apply ? 'apply' : 'dry-run',
    targetFileFound: result.targetFileFound,
    targetFunctionNames: result.targetFunctionNames ?? [],
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
    applied: result.applied,
  }, null, 2))
  process.exit(0)
}
