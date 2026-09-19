#!/usr/bin/env node
// scripts/design-system/registry-verify.mjs
//
// Batch verifier for registry-rule candidates — Stage B closure, wave 4
// (docs/internal/design/design-system-migration-plan.md §5, "Exception
// ledger"). A dry run against the current audit report found 392 unmatched
// `*/extension-boundary` findings (148 unique [path, syntax] sites) that a
// human would otherwise have to read one at a time before they can become
// entries in scripts/design-system/registry-rules.json. This tool does the
// mechanical part: for every finding not already covered by a rule, it
// parses the source file with the TypeScript compiler API, traces the value
// from its declaration to every place it is read, and decides:
//
//   PASS       — the flow is mechanically proven to match one of the
//                approved categories (§5): caller pass-through, user-
//                authored colours, or live layout measurement (plus a
//                third-party-widget PASS only when an already-registered
//                sibling rule proves the same exact site was already
//                repaired and reviewed).
//   NEEDS-READ — the tool cannot prove the flow one way or the other. This
//                is the default whenever anything is ambiguous.
//   REJECT     — the tool found a concrete disqualifier: the value is
//                transformed, sourced from a module-level constant or a
//                non-prop/non-state origin, the file is a test stand-in, or
//                a colour traces to a status/severity/config lookup rather
//                than a user-authored entity field.
//
// This script never writes scripts/design-system/registry-rules.json. It
// only ever reads an audit report + the current rules file and writes dry-
// run evidence (verdicts.json, verdicts.md, draft-rules.json) to --out.
//
// CLI:
//   node scripts/design-system/registry-verify.mjs \
//     --audit <audit-report.json> --rules scripts/design-system/registry-rules.json --out <dir>

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import ts from 'typescript'
import { parseSourceFile } from './codemod-lib.mjs'

// ── syntax parsing ──────────────────────────────────────────────────────

const PAINT_RE = /^(.+)#([^.#<]+)\.([^<]+)<-(.+)$/
const SIMPLE_CHAIN_RE = /^[A-Za-z_$][\w$]*(?:\??\.[A-Za-z_$][\w$]*)*$/

/**
 * Parses a scanner's `syntax` string into a structured shape. The three
 * shapes emitted by the scanners' extension-boundary paths
 * (ts-colors.mjs::emitExtensionBoundary/emitRuntimePaintBoundary and the
 * spacing/typography siblings that follow the same convention):
 *   "Owner#name"            — direct/renamed class-like parameter forward
 *   "Owner#base.key"        — a parameter's own `.className`-shaped member,
 *                             or a nested sub-component's own parameter
 *   "Owner#receiver.prop<-expr" — a runtime paint/measurement boundary
 */
export function parseSyntax(syntax) {
  if (typeof syntax !== 'string' || syntax.length === 0) return { kind: 'unrecognized', reason: 'empty or non-string syntax' }
  const paint = PAINT_RE.exec(syntax)
  if (paint) {
    return { kind: 'paint', owner: paint[1], receiver: paint[2], property: paint[3], exprText: paint[4] }
  }
  const hashIndex = syntax.indexOf('#')
  if (hashIndex < 0) return { kind: 'unrecognized', reason: 'no "#" separator' }
  const owner = syntax.slice(0, hashIndex)
  const rest = syntax.slice(hashIndex + 1)
  if (rest.length === 0) return { kind: 'unrecognized', reason: 'empty name after "#"' }
  const dotIndex = rest.indexOf('.')
  if (dotIndex < 0) return { kind: 'name', owner, name: rest }
  const base = rest.slice(0, dotIndex)
  const key = rest.slice(dotIndex + 1)
  if (key.includes('.')) return { kind: 'unrecognized', reason: 'multi-segment member syntax not supported' }
  return { kind: 'member', owner, base, key }
}

// ── generic TS helpers (adapted from codemod-classname-passthrough.mjs's
//    own proof helpers — kept standalone here since those are unexported) ──

function isFunctionLike(node) {
  return (
    ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) || ts.isMethodDeclaration(node)
  )
}

function unwrapParens(node) {
  while (node && ts.isParenthesizedExpression(node)) node = node.expression
  return node
}

function isComponentWrapperCallee(expression) {
  if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
  if (ts.isPropertyAccessExpression(expression)) return expression.name.text === 'forwardRef' || expression.name.text === 'memo'
  return false
}

/** The stable name a function-like node is bound to (own declaration name,
 * or the const/let identifier it's the initializer of, unwrapping a single
 * forwardRef/memo wrapper call). Null when anonymous. */
function ownerNameOf(fn) {
  if ((ts.isFunctionDeclaration(fn) || ts.isFunctionExpression(fn)) && fn.name) return fn.name.text
  let parent = fn.parent
  while (parent) {
    if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent)) { parent = parent.parent; continue }
    if (ts.isCallExpression(parent) && isComponentWrapperCallee(parent.expression) && parent.arguments.includes(fn)) { parent = parent.parent; continue }
    break
  }
  if (parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text
  return null
}

/** Every function-like node in `sourceFile` whose stable owner name is
 * `ownerName`. */
function findOwnerFunctions(sourceFile, ownerName) {
  const matches = []
  const visit = (node) => {
    if (isFunctionLike(node) && ownerNameOf(node) === ownerName) matches.push(node)
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return matches
}

function bindingDeclaresName(nameNode, name) {
  if (ts.isIdentifier(nameNode)) return nameNode.text === name
  if (ts.isObjectBindingPattern(nameNode) || ts.isArrayBindingPattern(nameNode)) {
    return nameNode.elements.some((el) => ts.isBindingElement(el) && bindingDeclaresName(el.name, name))
  }
  return false
}

/** A direct parameter of `fn` bound to local name `name` — a bare
 * identifier parameter, a rest parameter, or an object-binding-pattern
 * element (renamed or not; matched by its LOCAL binding name, since that is
 * what the scanner's own `binding.name`/body references use). */
function findExactParameterBinding(fn, name) {
  for (const parameter of fn.parameters) {
    if (ts.isIdentifier(parameter.name) && parameter.name.text === name) {
      return { declNode: parameter, kind: parameter.dotDotDotToken ? 'rest-parameter' : 'parameter' }
    }
    if (ts.isObjectBindingPattern(parameter.name)) {
      for (const element of parameter.name.elements) {
        if (ts.isBindingElement(element) && ts.isIdentifier(element.name) && element.name.text === name) {
          return { declNode: element, kind: 'destructured prop' }
        }
      }
    }
  }
  return null
}

/** `const { name } = someParam` inside `fn`'s body, where `someParam` is
 * itself a direct parameter of `fn` — a body-level destructure of a prop,
 * one hop removed from the parameter list. */
function findBodyDestructureBinding(fn, name) {
  if (!fn.body || !ts.isBlock(fn.body)) return null
  const paramNames = new Set(fn.parameters.filter((p) => ts.isIdentifier(p.name)).map((p) => p.name.text))
  for (const statement of fn.body.statements) {
    if (!ts.isVariableStatement(statement)) continue
    for (const declaration of statement.declarationList.declarations) {
      if (
        ts.isObjectBindingPattern(declaration.name) &&
        declaration.initializer && ts.isIdentifier(declaration.initializer) &&
        paramNames.has(declaration.initializer.text)
      ) {
        for (const element of declaration.name.elements) {
          if (ts.isBindingElement(element) && ts.isIdentifier(element.name) && element.name.text === name) {
            return { declNode: element, kind: `destructured from \`${declaration.initializer.text}\`` }
          }
        }
      }
    }
  }
  return null
}

/** A module-level (top-of-file) `const name = ...` — used to distinguish a
 * genuine "sourced from a module constant" REJECT from a plain "could not
 * find any declaration" NEEDS-READ. */
function findModuleConstDeclaration(sourceFile, name) {
  for (const statement of sourceFile.statements) {
    if (!ts.isVariableStatement(statement)) continue
    for (const declaration of statement.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.name.text === name) return declaration
    }
  }
  return null
}

/** A `const name = ...`/`let name = ...` declared INSIDE `fn`'s own body
 * (not a one-hop destructure of one of fn's parameters — that shape is
 * `findBodyDestructureBinding`'s job) — a non-prop, non-caller-supplied
 * source. Never descends into a nested function-like node, so a same-named
 * local inside a callback does not falsely match the outer binding. */
function findLocalVariableDeclaration(fn, name) {
  if (!fn.body || !ts.isBlock(fn.body)) return null
  let found = null
  const visit = (node) => {
    if (found || !node) return
    if (isFunctionLike(node) && node !== fn) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name) { found = node; return }
    ts.forEachChild(node, visit)
  }
  visit(fn.body)
  return found
}

/** True when `name` is assigned to (=, +=, ++, --, ...) anywhere within
 * `fn`'s body. */
function isReassignedWithin(fn, name) {
  let reassigned = false
  const visit = (node) => {
    if (reassigned || !node) return
    if (node !== fn && isFunctionLike(node)) return
    if (
      ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === name &&
      node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment
    ) { reassigned = true; return }
    if (
      (ts.isPrefixUnaryExpression(node) || ts.isPostfixUnaryExpression(node)) &&
      ts.isIdentifier(node.operand) && node.operand.text === name &&
      (node.operator === ts.SyntaxKind.PlusPlusToken || node.operator === ts.SyntaxKind.MinusMinusToken)
    ) { reassigned = true; return }
    ts.forEachChild(node, visit)
  }
  if (fn.body) visit(fn.body)
  return reassigned
}

/** Every Identifier reference to `name` within `bodyNode`, skipping any
 * declaration-position occurrence and any subtree shadowed by a nested
 * function that redeclares `name` as its own parameter. */
function collectReferences(bodyNode, name) {
  const refs = []
  const visit = (node) => {
    if (!node) return
    if (isFunctionLike(node) && node !== bodyNode) {
      const shadows = node.parameters?.some((p) => bindingDeclaresName(p.name, name))
      if (shadows) return
    }
    if (ts.isIdentifier(node) && node.text === name) {
      const parent = node.parent
      const isDeclPosition = (
        (ts.isParameter(parent) && parent.name === node) ||
        (ts.isBindingElement(parent) && parent.name === node) ||
        // A BindingElement's own `propertyName` (`const { className:
        // containerClassName } = containerProps` — "className" here is only
        // the SOURCE KEY of an unrelated destructure, never bound to or
        // reading our own `className` binding at all) is a key position,
        // not a reference — even though it is textually identical to the
        // name we are tracing. Without this, an unrelated rename-source key
        // elsewhere in the same function body gets misread as a "usage",
        // turning a real direct forward into a false NEEDS-READ.
        (ts.isBindingElement(parent) && parent.propertyName === node) ||
        (ts.isVariableDeclaration(parent) && parent.name === node) ||
        (ts.isPropertyAssignment(parent) && parent.name === node) ||
        (ts.isPropertyAccessExpression(parent) && parent.name === node) ||
        // A JSX attribute's own NAME (`<div className={className} />` — the
        // attribute is literally named "className") is a key position, not
        // a reference to the forwarded value, even though it is textually
        // identical to it in the single most common real shape this tool
        // verifies. Without this, the attribute name node gets misread as a
        // "usage" whose containing expression (JsxAttribute) climb() cannot
        // classify, turning every direct className forward into a false
        // NEEDS-READ.
        (ts.isJsxAttribute(parent) && parent.name === node)
      )
      if (!isDeclPosition) refs.push(node)
    }
    ts.forEachChild(node, visit)
  }
  visit(bodyNode)
  return refs
}

const KNOWN_BUILDER_NAMES = new Set(['cn', 'clsx', 'twMerge', 'twJoin'])
const SAFE_CHAIN_METHODS = new Set(['filter', 'join'])

function isFilterJoinChain(expr, restName) {
  if (!ts.isCallExpression(expr) || !ts.isPropertyAccessExpression(expr.expression) || expr.expression.name.text !== 'join') return false
  let receiver = expr.expression.expression
  if (ts.isCallExpression(receiver) && ts.isPropertyAccessExpression(receiver.expression) && receiver.expression.name.text === 'filter') {
    receiver = receiver.expression.expression
  }
  return ts.isIdentifier(receiver) && receiver.text === restName
}

/** True when `name` is a same-file function (declaration or `const name =
 * (...) => ...`) whose whole body is exactly `REST(.filter(...))?.join(...)`
 * on its own single rest parameter — the "transparent joiner" shape
 * (`cn`/`clsx`-equivalent local helper; mirrors codemod-lib.mjs's own
 * `isTransparentJoinerCallee`, kept standalone since that helper isn't
 * exported). */
function isLocalTransparentJoiner(name, sourceFile) {
  for (const statement of sourceFile.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name?.text === name) {
      if (statement.parameters.length !== 1 || !statement.body) continue
      const parameter = statement.parameters[0]
      if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name)) continue
      if (statement.body.statements.length !== 1) continue
      const ret = statement.body.statements[0]
      if (!ts.isReturnStatement(ret) || !ret.expression) continue
      if (isFilterJoinChain(ret.expression, parameter.name.text)) return true
    }
    if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        if (!ts.isIdentifier(declaration.name) || declaration.name.text !== name || !declaration.initializer) continue
        const fn = declaration.initializer
        if (!ts.isArrowFunction(fn) && !ts.isFunctionExpression(fn)) continue
        if (fn.parameters.length !== 1) continue
        const parameter = fn.parameters[0]
        if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name)) continue
        const restName = parameter.name.text
        let bodyExpr
        if (ts.isBlock(fn.body)) {
          if (fn.body.statements.length !== 1) continue
          const ret = fn.body.statements[0]
          if (!ts.isReturnStatement(ret) || !ret.expression) continue
          bodyExpr = ret.expression
        } else {
          bodyExpr = fn.body
        }
        if (isFilterJoinChain(bodyExpr, restName)) return true
      }
    }
  }
  return false
}

function isKnownBuilder(name, sourceFile) {
  if (!name) return false
  if (KNOWN_BUILDER_NAMES.has(name)) return true
  return isLocalTransparentJoiner(name, sourceFile)
}

function calleeNameOf(expression) {
  return ts.isIdentifier(expression) ? expression.text : null
}

function isTrivialFallback(node) {
  const unwrapped = unwrapParens(node)
  if (ts.isStringLiteralLike(unwrapped)) return unwrapped.text === ''
  if (ts.isIdentifier(unwrapped)) return unwrapped.text === 'undefined'
  return unwrapped.kind === ts.SyntaxKind.NullKeyword
}

function isLiteralLikeExpression(node) {
  const unwrapped = unwrapParens(node)
  return ts.isStringLiteralLike(unwrapped)
}

/**
 * Walks UP from `node` (an identifier reference, or a composite expression
 * one hop up from it) through every shape proven invisible for a class-like
 * value forward: a direct JSX attribute / return / arrow body (terminal
 * "sink"), a known class-merge builder call or transparent-joiner method
 * chain, a template literal whose other spans are literal-only, a `??`/`||`
 * trivial-fallback guard, a ternary guard, a spread/array-literal wrapper,
 * or — when the composite lands in a `const NEW = ...` — bubbles out as an
 * "alias" for the caller to keep tracing from NEW's own declaration scope.
 * Anything else is reported "unknown" (never treated as safe).
 */
function climb(node, sourceFile, hopBudget) {
  if (hopBudget <= 0) return { kind: 'unknown', reason: 'forwarding chain exceeds the traceable hop budget' }
  let current = node
  while (current.parent && ts.isParenthesizedExpression(current.parent)) current = current.parent
  const parent = current.parent
  if (!parent) return { kind: 'unknown', reason: 'no parent expression (unexpected)' }

  if (ts.isJsxExpression(parent) && parent.expression === current && parent.parent && ts.isJsxAttribute(parent.parent)) {
    return { kind: 'sink', node: parent.parent, description: `JSX attribute \`${parent.parent.name.getText(sourceFile)}\`` }
  }
  if (ts.isReturnStatement(parent) && parent.expression === current) {
    return { kind: 'sink', node: parent, description: 'return statement' }
  }
  if (ts.isArrowFunction(parent) && parent.body === current) {
    return { kind: 'sink', node: parent, description: 'arrow function return value' }
  }
  if (ts.isVariableDeclaration(parent) && parent.initializer === current && ts.isIdentifier(parent.name)) {
    return { kind: 'alias', name: parent.name.text, declNode: parent }
  }
  if (ts.isSpreadElement(parent) && parent.expression === current) {
    return climb(parent, sourceFile, hopBudget - 1)
  }
  if (ts.isArrayLiteralExpression(parent) && parent.elements.includes(current)) {
    return climb(parent, sourceFile, hopBudget - 1)
  }
  if (ts.isPropertyAssignment(parent) && parent.initializer === current) {
    return climb(parent.parent, sourceFile, hopBudget - 1)
  }
  if (ts.isShorthandPropertyAssignment(parent)) {
    return climb(parent.parent, sourceFile, hopBudget - 1)
  }
  if (ts.isCallExpression(parent) && parent.arguments.includes(current)) {
    const calleeName = calleeNameOf(parent.expression)
    if (isKnownBuilder(calleeName, sourceFile)) return climb(parent, sourceFile, hopBudget - 1)
    return { kind: 'unknown', reason: `passed as an argument to \`${calleeName ?? parent.expression.getText(sourceFile)}\`, which is not a recognized class-merge builder` }
  }
  if (ts.isPropertyAccessExpression(parent) && parent.expression === current) {
    const methodName = parent.name.text
    if (!SAFE_CHAIN_METHODS.has(methodName)) return { kind: 'transform', reason: `\`.${methodName}(...)\` is called on the value` }
    const call = parent.parent
    if (!ts.isCallExpression(call) || call.expression !== parent) return { kind: 'unknown', reason: `\`.${methodName}\` is read without being called` }
    return climb(call, sourceFile, hopBudget - 1)
  }
  if (ts.isTemplateSpan(parent) && parent.expression === current) {
    const template = parent.parent
    const othersLiteral = template.templateSpans.every((span) => span === parent || isLiteralLikeExpression(span.expression))
    if (!othersLiteral) return { kind: 'unknown', reason: 'template literal has another non-literal interpolation' }
    return climb(template, sourceFile, hopBudget - 1)
  }
  if (ts.isBinaryExpression(parent) && parent.left === current && (parent.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || parent.operatorToken.kind === ts.SyntaxKind.BarBarToken)) {
    const operator = parent.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken ? '??' : '||'
    if (!isTrivialFallback(parent.right)) return { kind: 'unknown', reason: `\`${operator}\` fallback is not a trivial empty value` }
    return climb(parent, sourceFile, hopBudget - 1)
  }
  if (ts.isConditionalExpression(parent)) {
    if (parent.condition === current) return { kind: 'neutral' }
    const isTrueBranch = parent.whenTrue === current
    const isFalseBranch = parent.whenFalse === current
    if (isTrueBranch || isFalseBranch) {
      const sibling = isTrueBranch ? parent.whenFalse : parent.whenTrue
      if (!isTrivialFallback(sibling)) return { kind: 'unknown', reason: 'conditional expression has a non-trivial sibling branch (a possible conditional replacement)' }
      return climb(parent, sourceFile, hopBudget - 1)
    }
  }
  return { kind: 'unknown', reason: `unrecognized containing expression (${ts.SyntaxKind[parent.kind]})` }
}

/**
 * Traces a single binding (`declNode` bound to `name` inside `homeFn`) end
 * to end: every reference either reaches a proven terminal sink, is a
 * neutral (non-flow) use such as a ternary's own condition, or bubbles out
 * as a new alias binding that gets queued and traced in turn (bounded by a
 * global hop budget so a pathological chain reports NEEDS-READ instead of
 * looping). Returns `{ ok, sinks, reason }` — `ok` is false the moment any
 * reference classifies as `transform` or `unknown`; `sinks` collects every
 * terminal sink discovered before that.
 */
function traceBinding(homeFn, name, sourceFile, totalHopBudget = { n: 24 }) {
  const sinks = []
  const queue = [{ fn: homeFn, name }]
  const visitedAliases = new Set()
  while (queue.length > 0) {
    if (totalHopBudget.n <= 0) return { ok: false, disqualifying: false, sinks, reason: 'value is forwarded through too many aliases to trace conclusively' }
    const { fn, name: currentName } = queue.shift()
    const scopeKey = `${fn.pos}:${fn.end}:${currentName}`
    if (visitedAliases.has(scopeKey)) continue
    visitedAliases.add(scopeKey)
    const bodyNode = fn.body ?? fn
    const refs = collectReferences(bodyNode, currentName)
    if (refs.length === 0) return { ok: false, disqualifying: true, sinks, reason: `\`${currentName}\` is declared but never referenced` }
    for (const ref of refs) {
      totalHopBudget.n -= 1
      const outcome = climb(ref, sourceFile, 8)
      if (outcome.kind === 'sink') {
        const pos = sourceFile.getLineAndCharacterOfPosition(outcome.node.getStart(sourceFile, false))
        sinks.push({ line: pos.line + 1, column: pos.character + 1, description: outcome.description })
      } else if (outcome.kind === 'neutral') {
        continue
      } else if (outcome.kind === 'alias') {
        let enclosing = outcome.declNode.parent
        while (enclosing && !isFunctionLike(enclosing) && !ts.isSourceFile(enclosing)) enclosing = enclosing.parent
        const nextFn = enclosing && isFunctionLike(enclosing) ? enclosing : fn
        queue.push({ fn: nextFn, name: outcome.name })
      } else if (outcome.kind === 'transform') {
        return { ok: false, disqualifying: true, sinks, reason: outcome.reason }
      } else {
        return { ok: false, disqualifying: false, sinks, reason: outcome.reason }
      }
    }
  }
  if (sinks.length === 0) return { ok: false, disqualifying: false, sinks, reason: 'no terminal sink (JSX attribute / return / arrow body) was reached' }
  return { ok: true, disqualifying: false, sinks, reason: null }
}

// ── member-kind binding resolution ──────────────────────────────────────

function findPropertyFunctionsNamed(fn, baseName) {
  const matches = []
  const visit = (node) => {
    if (!node) return
    if ((ts.isPropertyAssignment(node) || ts.isShorthandPropertyAssignment(node)) && node.name && node.name.getText().replace(/['"]/g, '') === baseName) {
      const value = ts.isPropertyAssignment(node) ? node.initializer : node.name
      if (value && (ts.isArrowFunction(value) || ts.isFunctionExpression(value))) matches.push(value)
    }
    if (ts.isMethodDeclaration(node) && node.name && node.name.getText() === baseName) matches.push(node)
    ts.forEachChild(node, visit)
  }
  if (fn.body) visit(fn.body)
  return matches
}

function findParamLocalNameForProperty(fn, propertyKey) {
  if (fn.parameters.length === 0 || !ts.isObjectBindingPattern(fn.parameters[0].name)) return null
  for (const element of fn.parameters[0].name.elements) {
    if (!ts.isBindingElement(element)) continue
    const propName = element.propertyName ? element.propertyName.getText().replace(/['"]/g, '') : (ts.isIdentifier(element.name) ? element.name.text : null)
    if (propName === propertyKey && ts.isIdentifier(element.name)) return { localName: element.name.text, declNode: element }
  }
  return null
}

function findAnonymousCallbacksWithParam(fn, baseName) {
  const matches = []
  const visit = (node) => {
    if (!node) return
    if ((ts.isArrowFunction(node) || ts.isFunctionExpression(node)) && ownerNameOf(node) === null) {
      const first = node.parameters[0]
      if (first && ts.isIdentifier(first.name) && first.name.text === baseName) matches.push(node)
    }
    ts.forEachChild(node, visit)
  }
  if (fn.body) visit(fn.body)
  return matches
}

// ── site verification ───────────────────────────────────────────────────

const TEST_FILE_RE = /(\.(test|spec|stories)\.[tj]sx?$)|(\/__tests__\/)|(\/tests\/)|(\/test-utils\/)|(\.mock\.[tj]sx?$)/i
export { TEST_FILE_RE }

const CATEGORY_OWNER = {
  'caller pass-through': 'caller pass-through exception registry',
  'user-authored colours': 'user-authored colours exception registry',
  'live layout measurement': 'live layout measurement exception registry',
  'user-adjustable root font': 'user-adjustable root font exception registry',
  'third-party widget values': 'third-party integration boundary exception registry',
}

function locPrefix(path, line, column) {
  return `${path}:${line}:${column}`
}

function declarationLoc(sourceFile, declNode, path) {
  const pos = sourceFile.getLineAndCharacterOfPosition(declNode.getStart(sourceFile, false))
  return { line: pos.line + 1, column: pos.character + 1, loc: locPrefix(path, pos.line + 1, pos.character + 1) }
}

/** Resolves a name-kind (or reduced member-kind) binding: direct parameter,
 * rest parameter, destructured prop, or one-hop body destructure of a
 * parameter. Returns `{ declNode, kind }` or a `{ reject | needsRead }`
 * verdict short-circuit. */
function resolveNameBinding(fn, name) {
  const direct = findExactParameterBinding(fn, name)
  if (direct) return { ok: true, ...direct }
  const bodyDestructure = findBodyDestructureBinding(fn, name)
  if (bodyDestructure) return { ok: true, ...bodyDestructure }
  return { ok: false }
}

function classifyPassthroughLike(fn, name, sourceFile, path, ownerLabel, declKindNoun) {
  const binding = resolveNameBinding(fn, name)
  if (!binding.ok) {
    const moduleConst = findModuleConstDeclaration(sourceFile, name)
    if (moduleConst) {
      const loc = declarationLoc(sourceFile, moduleConst, path)
      return { verdict: 'REJECT', reason: `\`${name}\` resolves to a module-level constant at ${loc.loc}, not a caller-supplied prop of ${ownerLabel}.` }
    }
    const localVar = findLocalVariableDeclaration(fn, name)
    if (localVar) {
      const loc = declarationLoc(sourceFile, localVar, path)
      return { verdict: 'REJECT', reason: `\`${name}\` is a local variable declared at ${loc.loc} inside ${ownerLabel}, not a caller-supplied prop — a non-prop source and not registrable as caller pass-through.` }
    }
    return { verdict: 'NEEDS-READ', reason: `could not find a declaration binding \`${name}\` as a direct parameter, rest parameter, or one-hop body-destructured prop of ${ownerLabel}.` }
  }
  if (isReassignedWithin(fn, name)) {
    const loc = declarationLoc(sourceFile, binding.declNode, path)
    return { verdict: 'REJECT', reason: `\`${name}\` (declared at ${loc.loc}) is reassigned within ${ownerLabel} — not a pure forward.` }
  }
  const declLoc = declarationLoc(sourceFile, binding.declNode, path)
  const trace = traceBinding(fn, name, sourceFile)
  if (!trace.ok) {
    return {
      verdict: trace.disqualifying ? 'REJECT' : 'NEEDS-READ',
      reason: `${ownerLabel}'s ${binding.kind ?? declKindNoun} \`${name}\` (declared at ${declLoc.loc}) — ${trace.reason}.`,
      declLoc,
      sinks: trace.sinks,
    }
  }
  return { verdict: 'PASS', declLoc, sinks: trace.sinks, bindingKind: binding.kind ?? declKindNoun }
}

function verifyNameKind(parsed, sourceFile, path, siblingInfo) {
  const owners = findOwnerFunctions(sourceFile, parsed.owner)
  if (owners.length === 0) return { verdict: 'NEEDS-READ', reason: `could not locate a function/component declaration named \`${parsed.owner}\` in ${path}.` }
  if (owners.length > 1) return { verdict: 'NEEDS-READ', reason: `\`${parsed.owner}\` resolves to ${owners.length} function-like declarations in ${path} — ambiguous owner.` }
  const fn = owners[0]
  const result = classifyPassthroughLike(fn, parsed.name, sourceFile, path, `\`${parsed.owner}\``, 'parameter')
  return finalizePassthroughResult(result, parsed, path, siblingInfo)
}

function verifyMemberKind(parsed, sourceFile, path, siblingInfo) {
  const owners = findOwnerFunctions(sourceFile, parsed.owner)
  if (owners.length === 0) return { verdict: 'NEEDS-READ', reason: `could not locate a function/component declaration named \`${parsed.owner}\` in ${path}.` }
  if (owners.length > 1) return { verdict: 'NEEDS-READ', reason: `\`${parsed.owner}\` resolves to ${owners.length} function-like declarations in ${path} — ambiguous owner.` }
  const ownerFn = owners[0]

  const subOwners = findPropertyFunctionsNamed(ownerFn, parsed.base)
  if (subOwners.length === 1) {
    const inner = subOwners[0]
    const found = findParamLocalNameForProperty(inner, parsed.key)
    if (found) {
      const result = classifyPassthroughLike(inner, found.localName, sourceFile, path, `\`${parsed.owner}\`'s \`${parsed.base}\` sub-component`, 'destructured prop')
      return finalizePassthroughResult(result, parsed, path, siblingInfo)
    }
  }

  const callbacks = findAnonymousCallbacksWithParam(ownerFn, parsed.base)
  if (callbacks.length === 1) {
    const callbackFn = callbacks[0]
    const bodyNode = callbackFn.body
    const refs = []
    const visit = (node) => {
      if (!node) return
      if (isFunctionLike(node) && node !== callbackFn) {
        if (node.parameters?.some((p) => bindingDeclaresName(p.name, parsed.base))) return
      }
      if (ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === parsed.base && node.name.text === parsed.key) {
        refs.push(node)
      }
      ts.forEachChild(node, visit)
    }
    visit(bodyNode)
    if (refs.length === 0) return { verdict: 'NEEDS-READ', reason: `no \`${parsed.base}.${parsed.key}\` access found inside the matching anonymous callback in ${path}.` }
    const declPos = sourceFile.getLineAndCharacterOfPosition(callbackFn.parameters[0].getStart(sourceFile, false))
    const declLoc = { line: declPos.line + 1, column: declPos.character + 1, loc: locPrefix(path, declPos.line + 1, declPos.character + 1) }
    const sinks = []
    for (const ref of refs) {
      const outcome = climb(ref, sourceFile, 8)
      if (outcome.kind === 'sink') {
        const pos = sourceFile.getLineAndCharacterOfPosition(outcome.node.getStart(sourceFile, false))
        sinks.push({ line: pos.line + 1, column: pos.character + 1, description: outcome.description })
      } else if (outcome.kind === 'neutral') {
        continue
      } else if (outcome.kind === 'transform') {
        return finalizePassthroughResult({ verdict: 'REJECT', reason: `\`${parsed.base}.${parsed.key}\` (declared at ${declLoc.loc}) — ${outcome.reason}.` }, parsed, path, siblingInfo)
      } else {
        return finalizePassthroughResult({ verdict: 'NEEDS-READ', reason: `\`${parsed.base}.${parsed.key}\` (declared at ${declLoc.loc}) — ${outcome.reason}.`, declLoc, sinks }, parsed, path, siblingInfo)
      }
    }
    if (sinks.length === 0) return finalizePassthroughResult({ verdict: 'NEEDS-READ', reason: `\`${parsed.base}.${parsed.key}\` (declared at ${declLoc.loc}) never reaches a proven terminal sink.`, declLoc, sinks }, parsed, path, siblingInfo)
    return finalizePassthroughResult({ verdict: 'PASS', declLoc, sinks, bindingKind: 'callback parameter member' }, parsed, path, siblingInfo)
  }

  return { verdict: 'NEEDS-READ', reason: `\`${parsed.base}.${parsed.key}\` did not resolve to exactly one sub-component parameter or one anonymous-callback parameter member inside \`${parsed.owner}\` in ${path} (${subOwners.length} sub-component match(es), ${callbacks.length} callback match(es)).` }
}

function finalizePassthroughResult(result, parsed, path, siblingInfo) {
  if (result.verdict !== 'PASS') return result
  const category = siblingInfo?.category ?? 'caller pass-through'
  const owner = CATEGORY_OWNER[category] ?? CATEGORY_OWNER['caller pass-through']
  const sinkText = result.sinks.map((s) => `${locPrefix(path, s.line, s.column)} (${s.description})`).join('; ')
  const siblingNote = siblingInfo ? ` Sibling rule already registers this exact (path, syntax) under ruleId "${siblingInfo.ruleId}" with category "${siblingInfo.category}"; this rule extends the same review to the sibling ruleId this scanner reports.` : ''
  const reason = `Category: ${category} — migration plan §5. Mechanically verified by registry-verify.mjs: ${parsed.owner}'s own ${result.bindingKind ?? 'parameter'} \`${parsed.kind === 'member' ? `${parsed.base}.${parsed.key}` : parsed.name}\` is declared at ${result.declLoc.loc} and flows unmodified to ${result.sinks.length} sink(s) — ${sinkText} — with no transformation, conditional replacement, or non-forwarding read in between.${siblingNote}`
  return { verdict: 'PASS', category, owner, reason, evidence: [{ ...result.declLoc, description: 'declaration' }, ...result.sinks.map((s) => ({ ...s, description: s.description }))] }
}

// ── paint-kind verification ─────────────────────────────────────────────

const PAINT_PROPS = new Set(['backgroundColor', 'color', 'borderColor', 'fill', 'stroke'])
const BLOCKLIST_TOKEN_RE = /^(config|cfg|status|severity|variant|state|kind|theme)$/i
const MEASUREMENT_RE = /visualViewport|innerHeight|innerWidth|getBoundingClientRect|clientHeight|clientWidth|scrollY|scrollX|offsetHeight|offsetWidth|matchMedia|devicePixelRatio/

function resolveRootBinding(sourceFile, fn, rootToken) {
  const direct = findExactParameterBinding(fn, rootToken)
  if (direct) return { origin: 'parameter', declNode: direct.declNode, initializerText: null }
  const bodyDestructure = findBodyDestructureBinding(fn, rootToken)
  if (bodyDestructure) return { origin: 'destructured prop', declNode: bodyDestructure.declNode, initializerText: null }
  if (fn.body && ts.isBlock(fn.body)) {
    for (const statement of fn.body.statements) {
      if (!ts.isVariableStatement(statement)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name) && declaration.name.text === rootToken) {
          return { origin: 'local variable', declNode: declaration, initializerText: declaration.initializer ? declaration.initializer.getText(sourceFile) : '' }
        }
      }
    }
  }
  return null
}

function verifyPaintKind(parsed, sourceFile, path, siblingInfo) {
  const owners = findOwnerFunctions(sourceFile, parsed.owner)
  if (owners.length !== 1) return { verdict: 'NEEDS-READ', reason: `\`${parsed.owner}\` resolves to ${owners.length} function-like declaration(s) in ${path} — cannot uniquely locate the paint boundary's owner.` }
  const fn = owners[0]

  if (!SIMPLE_CHAIN_RE.test(parsed.exprText)) {
    return { verdict: 'NEEDS-READ', reason: `the source expression \`${parsed.exprText}\` is not a simple property-access chain (a call, template, or other computed expression) — needs a read to confirm provenance.` }
  }
  const tokens = parsed.exprText.split(/\?\./).flatMap((seg) => seg.split('.'))
  const rootToken = tokens[0]
  const chainTokens = tokens.slice(1)
  const lastToken = chainTokens.length > 0 ? chainTokens[chainTokens.length - 1] : null

  if (BLOCKLIST_TOKEN_RE.test(rootToken) || chainTokens.some((t) => BLOCKLIST_TOKEN_RE.test(t))) {
    return { verdict: 'REJECT', reason: `\`${parsed.exprText}\` traces through a status/severity/config-shaped identifier, not a user-authored entity colour field (status/control chrome is never registrable under the user-authored-colours category).` }
  }

  const rootBinding = resolveRootBinding(sourceFile, fn, rootToken)
  if (!rootBinding) return { verdict: 'NEEDS-READ', reason: `could not resolve root identifier \`${rootToken}\` of \`${parsed.exprText}\` within \`${parsed.owner}\`.` }
  const declLoc = declarationLoc(sourceFile, rootBinding.declNode, path)

  if (parsed.property.startsWith('--')) {
    const candidateText = rootBinding.initializerText ?? ''
    if (MEASUREMENT_RE.test(candidateText)) {
      const category = 'live layout measurement'
      const reason = `Category: ${category} — migration plan §5 founder decision 2026-09-19 ("A value measured from the browser at run time... is written to a CSS custom property"). Mechanically verified: \`${parsed.owner}\` writes CSS custom property \`${parsed.property}\` from \`${parsed.exprText}\`, whose root \`${rootToken}\` is a ${rootBinding.origin} declared at ${declLoc.loc} with an initializer that reads a browser measurement API (\`${candidateText}\`).`
      return { verdict: 'PASS', category, owner: CATEGORY_OWNER[category], reason, evidence: [{ ...declLoc, description: `${rootBinding.origin} declaration; measurement initializer: ${candidateText}` }] }
    }
    return { verdict: 'NEEDS-READ', reason: `\`${parsed.owner}\` writes CSS custom property \`${parsed.property}\` from \`${parsed.exprText}\`; root \`${rootToken}\` (${rootBinding.origin}, declared at ${declLoc.loc}) does not visibly trace to a browser measurement API — needs a read to classify as live layout measurement vs. another category.` }
  }

  if (!PAINT_PROPS.has(parsed.property)) {
    return { verdict: 'NEEDS-READ', reason: `receiver property \`${parsed.property}\` on \`${parsed.receiver}\` is not a recognized paint property (backgroundColor/color/borderColor/fill/stroke) — the boundary shape needs a read.` }
  }

  const rootIsCallerBound = rootBinding.origin === 'parameter' || rootBinding.origin === 'destructured prop'
  if (lastToken && lastToken.toLowerCase() === 'color' && rootIsCallerBound) {
    const category = siblingInfo?.category ?? 'user-authored colours'
    const owner = CATEGORY_OWNER[category] ?? CATEGORY_OWNER['user-authored colours']
    const siblingNote = siblingInfo ? ` Sibling rule already registers this exact (path, syntax) under ruleId "${siblingInfo.ruleId}" with category "${siblingInfo.category}".` : ''
    const reason = `Category: ${category} — migration plan §5 permanent governed registry 1. Mechanically verified: \`${parsed.owner}\` writes \`${parsed.receiver}.${parsed.property}\` directly from \`${parsed.exprText}\`, whose root \`${rootToken}\` is a ${rootBinding.origin} declared at ${declLoc.loc}, with the final property \`${lastToken}\` read unchanged (no status/config lookup, no transformation).${siblingNote}`
    return { verdict: 'PASS', category, owner, reason, evidence: [{ ...declLoc, description: `${rootBinding.origin} declaration` }] }
  }
  if (!lastToken && rootBinding.origin !== 'parameter' && rootBinding.origin !== 'destructured prop' && /color/i.test(rootToken)) {
    return { verdict: 'NEEDS-READ', reason: `\`${rootToken}\` (bare identifier, ${rootBinding.origin}, declared at ${declLoc.loc}) needs a read to confirm it is genuinely user-chosen and not a status-derived local.` }
  }
  if (!lastToken && rootIsCallerBound && /color/i.test(rootToken)) {
    const category = siblingInfo?.category ?? 'user-authored colours'
    const owner = CATEGORY_OWNER[category] ?? CATEGORY_OWNER['user-authored colours']
    const reason = `Category: ${category} — migration plan §5 permanent governed registry 1. Mechanically verified: \`${parsed.owner}\` writes \`${parsed.receiver}.${parsed.property}\` directly from bare identifier \`${parsed.exprText}\`, a ${rootBinding.origin} declared at ${declLoc.loc} whose own name identifies it as a colour value, forwarded unchanged.`
    return { verdict: 'PASS', category, owner, reason, evidence: [{ ...declLoc, description: `${rootBinding.origin} declaration` }] }
  }
  return { verdict: 'NEEDS-READ', reason: `\`${parsed.exprText}\` reaches \`${parsed.receiver}.${parsed.property}\` but does not match a mechanically provable user-authored-colour shape (root \`${rootToken}\` is a ${rootBinding.origin}; final token \`${lastToken ?? '(none)'}\`) — needs a read.` }
}

// ── top-level per-finding verification ──────────────────────────────────

/**
 * Verifies one finding. `sourceCache` maps repo-relative path -> { sourceFile, text } | null (null = read/parse failed).
 * `siblingMap` maps "path|syntax" -> { ruleId, category, owner } for an
 * ALREADY-REGISTERED rule at the same exact (path, syntax) under a
 * different ruleId (used to carry an already-reviewed third-party-widget
 * classification across sibling scanner findings, matching the precedent
 * already in registry-rules.json).
 */
export function verifyFinding({ ruleId, path, syntax, count }, { sourceCache, siblingMap }) {
  const base = { ruleId, path, syntax, count: count ?? null }
  if (TEST_FILE_RE.test(path)) {
    return { ...base, verdict: 'REJECT', category: null, reason: 'test stand-in file — test-only components are never an exception category, however unprovable their forwarding looks.', evidence: [] }
  }

  const cached = sourceCache.get(path)
  if (cached === null) return { ...base, verdict: 'NEEDS-READ', category: null, reason: `source file ${path} could not be read or parsed.`, evidence: [] }

  const { sourceFile } = cached
  const parsed = parseSyntax(syntax)
  const siblingInfo = siblingMap.get(`${path}|${syntax}`)

  let result
  try {
    if (parsed.kind === 'unrecognized') {
      result = { verdict: 'NEEDS-READ', reason: `syntax string does not match a recognized shape: ${parsed.reason}.` }
    } else if (parsed.kind === 'name') {
      result = verifyNameKind(parsed, sourceFile, path, siblingInfo)
    } else if (parsed.kind === 'member') {
      result = verifyMemberKind(parsed, sourceFile, path, siblingInfo)
    } else if (parsed.kind === 'paint') {
      result = verifyPaintKind(parsed, sourceFile, path, siblingInfo)
    } else {
      result = { verdict: 'NEEDS-READ', reason: 'unhandled syntax kind.' }
    }
  } catch (error) {
    result = { verdict: 'NEEDS-READ', reason: `analysis threw: ${error && error.message ? error.message : String(error)}` }
  }

  return {
    ...base,
    verdict: result.verdict,
    category: result.category ?? null,
    reason: result.reason,
    evidence: result.evidence ?? [],
    draftRule: result.verdict === 'PASS' ? { ruleId, path, syntax, category: result.category, owner: result.owner, reason: result.reason } : null,
  }
}

// ── batch driver ─────────────────────────────────────────────────────────

function tripleKey(ruleId, path, syntax) {
  return `${ruleId}\u0000${path}\u0000${syntax}`
}

/** Builds the sibling-category lookup: for every rule already in
 * registry-rules.json, "path|syntax" -> { ruleId, category, owner } (first
 * one wins on the rare chance of more than one). */
export function buildSiblingMap(rulesDoc) {
  const map = new Map()
  for (const rule of rulesDoc?.rules ?? []) {
    const key = `${rule.path}|${rule.syntax}`
    if (!map.has(key)) map.set(key, { ruleId: rule.ruleId, category: rule.category, owner: rule.owner })
  }
  return map
}

/**
 * Pure batch function: given a parsed audit report, a parsed rules doc, and
 * a `readSource(path) -> string | null` loader, returns every uncovered
 * extension-boundary finding's verdict (ruleId ending "/extension-
 * boundary"). Kept side-effect free (no fs writes) so tests can call it
 * directly with in-memory sources.
 */
export function buildVerdicts({ auditReport, rulesDoc, readSource }) {
  const covered = new Set((rulesDoc?.rules ?? []).map((r) => tripleKey(r.ruleId, r.path, r.syntax)))
  const siblingMap = buildSiblingMap(rulesDoc)
  const fingerprints = Array.isArray(auditReport?.debt?.fingerprints) ? auditReport.debt.fingerprints : []
  const boundaryFindings = fingerprints.filter((f) => typeof f.ruleId === 'string' && f.ruleId.endsWith('/extension-boundary'))
  const uncovered = boundaryFindings.filter((f) => !covered.has(tripleKey(f.ruleId, f.path, f.syntax)))

  const sourceCache = new Map()
  for (const finding of uncovered) {
    if (sourceCache.has(finding.path)) continue
    let text
    try { text = readSource(finding.path) } catch { text = null }
    if (text === null) { sourceCache.set(finding.path, null); continue }
    try {
      const sourceFile = parseSourceFile(finding.path, text)
      sourceCache.set(finding.path, { sourceFile, text })
    } catch {
      sourceCache.set(finding.path, null)
    }
  }

  const verdicts = uncovered.map((finding) => verifyFinding(finding, { sourceCache, siblingMap }))
  return { verdicts, totalBoundaryFindings: boundaryFindings.length, totalUncovered: uncovered.length }
}

export function summarizeCounts(verdicts) {
  const counts = { PASS: 0, 'NEEDS-READ': 0, REJECT: 0 }
  const byCategory = {}
  for (const v of verdicts) {
    counts[v.verdict] = (counts[v.verdict] ?? 0) + 1
    const catKey = `${v.verdict}:${v.category ?? '(none)'}`
    byCategory[catKey] = (byCategory[catKey] ?? 0) + 1
  }
  return { counts, byCategory }
}

export function renderMarkdown(verdicts) {
  const order = ['PASS', 'NEEDS-READ', 'REJECT']
  const lines = ['# registry-verify verdicts', '']
  for (const verdict of order) {
    const rows = verdicts.filter((v) => v.verdict === verdict)
    lines.push(`## ${verdict} (${rows.length})`, '')
    const byCategory = new Map()
    for (const row of rows) {
      const key = row.category ?? '(uncategorized)'
      if (!byCategory.has(key)) byCategory.set(key, [])
      byCategory.get(key).push(row)
    }
    for (const [category, rowsInCategory] of [...byCategory.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
      lines.push(`### ${category} (${rowsInCategory.length})`, '', '| ruleId | path | syntax | reason |', '|---|---|---|---|')
      for (const row of rowsInCategory.sort((a, b) => (a.path + a.syntax).localeCompare(b.path + b.syntax))) {
        const reason = (row.reason ?? '').replace(/\|/g, '\\|').replace(/\n/g, ' ')
        lines.push(`| ${row.ruleId} | ${row.path} | \`${row.syntax}\` | ${reason} |`)
      }
      lines.push('')
    }
  }
  return lines.join('\n')
}

export function loadJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function parseArgs(argv) {
  const args = { audit: null, rules: null, out: null }
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--audit') args.audit = argv[++i]
    else if (argv[i] === '--rules') args.rules = argv[++i]
    else if (argv[i] === '--out') args.out = argv[++i]
  }
  return args
}

function main() {
  const { audit, rules, out } = parseArgs(process.argv.slice(2))
  if (!audit || !rules || !out) {
    console.error('usage: node registry-verify.mjs --audit <audit-report.json> --rules <registry-rules.json> --out <dir>')
    process.exitCode = 2
    return
  }
  const auditReport = loadJson(audit)
  const rulesDoc = loadJson(rules)
  const readSource = (path) => {
    try { return readFileSync(path, 'utf8') } catch { return null }
  }
  const { verdicts, totalBoundaryFindings, totalUncovered } = buildVerdicts({ auditReport, rulesDoc, readSource })
  const { counts, byCategory } = summarizeCounts(verdicts)
  const draftRules = verdicts.filter((v) => v.draftRule).map((v) => v.draftRule)

  mkdirSync(out, { recursive: true })
  writeFileSync(`${out}/verdicts.json`, `${JSON.stringify(verdicts, null, 2)}\n`)
  writeFileSync(`${out}/verdicts.md`, renderMarkdown(verdicts))
  writeFileSync(`${out}/draft-rules.json`, `${JSON.stringify({ version: 1, rules: draftRules }, null, 2)}\n`)

  console.error(`registry-verify: ${totalBoundaryFindings} total extension-boundary findings, ${totalUncovered} uncovered; PASS=${counts.PASS} NEEDS-READ=${counts['NEEDS-READ']} REJECT=${counts.REJECT}`)
  console.error(JSON.stringify(byCategory, null, 2))
}

const isMainModule = process.argv[1] && dirname(process.argv[1]) && import.meta.url === `file://${process.argv[1]}`
if (isMainModule) main()
