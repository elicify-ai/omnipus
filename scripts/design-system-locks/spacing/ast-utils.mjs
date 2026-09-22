#!/usr/bin/env node
// spacing/ast-utils.mjs
//
// Generic, dependency-free AST/reporting primitives (unwrap,
// staticPropertyName, pushFinding, and friends) every other spacing/*
// module builds on. Nothing here imports from a sibling module -- the leaf
// of the dependency graph.

'use strict'

import ts from 'typescript'

export const RULE = {
  offScale: 'spacing/off-scale',
  rootDependent: 'spacing/root-dependent',
  extensionBoundary: 'spacing/extension-boundary',
  missingSafeAreaFallback: 'spacing/missing-safe-area-fallback',
  invalidVar: 'spacing/invalid-var',
  unsupported: 'spacing/unsupported',
  parseError: 'spacing/parse-error',
}

export const UNRESOLVED_EXPR = Symbol('unresolved spacing expression')

export function unwrap(node) {
  let current = node
  while (current) {
    if (ts.isParenthesizedExpression(current) || ts.isAsExpression(current) || ts.isSatisfiesExpression(current) || ts.isNonNullExpression(current)) {
      current = current.expression
      continue
    }
    if (current.kind === ts.SyntaxKind.TypeAssertionExpression) {
      current = current.expression
      continue
    }
    break
  }
  return current
}

// Ported from ts-colors.mjs::propertyNameOf's ComputedPropertyName branch: a
// computed key (`[expr]: value`) that is itself a string literal — only
// wrapped in a type assertion the real codebase actually uses to satisfy a
// CSSProperties-style index signature, e.g. `['--kb-reading-measure' as
// string]: '72ch'` — is exactly as static as an ordinary quoted key once the
// assertion is stripped via `unwrap`. Deliberately does NOT recurse into
// this same function for the unwrapped expression (that would wrongly
// resolve a bare identifier reference, e.g. `[key]: value`, to the
// identifier's own NAME as if it were the key's runtime VALUE — the exact
// distinction ts-colors.mjs's propertyNameOf preserves by checking only for
// a StringLiteral/NoSubstitutionTemplateLiteral directly, never recursing).
// A numeric literal is deliberately excluded too, matching ts-colors.mjs
// exactly, not a proven-safe shape to add on top of the ported capability.
export function staticPropertyName(name) {
  if (!name) return null
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) return name.text
  if (ts.isStringLiteral(name) || ts.isNumericLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name)) return name.text
  if (ts.isComputedPropertyName(name)) {
    const expr = unwrap(name.expression)
    if (expr && (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr))) return expr.text
  }
  return null
}

export function calleeName(expr) {
  const node = unwrap(expr)
  if (!node) return ''
  if (ts.isIdentifier(node)) return node.text
  if (ts.isPropertyAccessExpression(node)) return node.name.text
  return ''
}

export function propertyInit(obj, name) {
  for (const prop of obj.properties) {
    if (!ts.isPropertyAssignment(prop)) continue
    const key = staticPropertyName(prop.name)
    if (key === name) return prop.initializer
  }
  return null
}

export function statementBinding(statements, name) {
  for (const statement of statements) {
    if (!ts.isVariableStatement(statement)) continue
    for (const declaration of statement.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.name.text === name) {
        return { declaration, initializer: declaration.initializer ?? null }
      }
    }
  }
  return null
}

export function findLexicalBinding(identifier, name) {
  let scope = identifier.parent
  while (scope) {
    if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
      const binding = statementBinding(scope.statements, name)
      if (binding) return binding
    }
    if (ts.isFunctionLike(scope)) {
      for (const parameter of scope.parameters) {
        if (ts.isIdentifier(parameter.name) && parameter.name.text === name) {
          return { declaration: parameter, initializer: parameter.initializer ?? null }
        }
      }
    }
    scope = scope.parent
  }
  return null
}

export function locFromTs(node, sourceFile) {
  const start = node.getStart(sourceFile)
  const { line, character } = sourceFile.getLineAndCharacterOfPosition(start)
  return { line: line + 1, column: character + 1 }
}

// Source-occurrence identity, independent of the display location. The class
// path deliberately displays findings at the point of use (useLoc forcing —
// see visitClassBuilderArg), so a pure helper's single literal reported from
// two call sites, or an array's two elements reported from one shared call
// site, both collapse onto identical (line, column) pairs under display-only
// dedupe. originKey instead identifies the actual terminal AST node (its own
// file + start offset): two references that resolve to the SAME literal node
// (repeat consumption via aliases or multiple calls to one pure helper) still
// dedupe to one finding, while two DISTINCT literal nodes (two elements of a
// helper-returned array, two branches of a conditional, ...) — even with
// identical text — are never collapsed. withOrigin tags a loc object without
// disturbing the (line, column) any downstream code reads for display.
export function nodeKey(node) {
  return `${node.getSourceFile().fileName}#${node.getStart()}`
}

export function withOrigin(loc, node) {
  return { ...loc, originKey: nodeKey(node) }
}

export function formatPx(px) {
  const rounded = Math.round(px * 1000) / 1000
  if (Number.isInteger(rounded)) return `${rounded}px`
  return `${rounded}px`
}

export function pushFinding(ctx, ruleId, syntax, message, loc) {
  const line = loc?.line
  // Dedupe is per source occurrence: a revisit of the same occurrence must
  // collapse, while two distinct occurrences of one syntax must both count —
  // fingerprint occurrence counts are explicit Stage B requirements. Line and
  // column stay display-only on the finding; the orchestrator fingerprint
  // ignores them.
  //
  // Callers that carry an originKey (see withOrigin/nodeKey) identify the
  // occurrence by the actual terminal AST node reached, independent of the
  // display location. This matters because the class-builder path reports
  // some findings at the point of use rather than the node's own position
  // (see "reports imported spacing at the consumer use site" and pure-helper
  // resolution): without originKey, two DISTINCT array elements returned by
  // one pure helper call would display at the identical forced call-site
  // location and silently dedupe to one (undercount); with it, they carry
  // distinct node identities and both count, while two references that
  // resolve to the SAME literal (repeat consumption via aliasing, or two call
  // sites of one single-value pure helper) still collapse to one. Callers
  // without an originKey dedupe by (line, column) alone.
  const column = Number.isInteger(loc?.column) && loc.column > 0 ? loc.column : undefined
  const originKey = typeof loc?.originKey === 'string' ? loc.originKey : undefined
  const exists = ctx.findings.some((finding) => finding.ruleId === ruleId && finding.syntax === syntax
    && (originKey !== undefined || finding.__originKey !== undefined
      ? finding.__originKey === originKey
      : finding.line === line && finding.column === column))
  if (exists) return
  const finding = { ruleId, path: ctx.path, syntax, message }
  if (Number.isInteger(line) && line > 0) finding.line = line
  if (column !== undefined) finding.column = column
  if (originKey !== undefined) Object.defineProperty(finding, '__originKey', { value: originKey, enumerable: false })
  ctx.findings.push(finding)
}

export function isConcatOrLogic(kind) {
  return (
    kind === ts.SyntaxKind.PlusToken
    || kind === ts.SyntaxKind.AmpersandAmpersandToken
    || kind === ts.SyntaxKind.BarBarToken
    || kind === ts.SyntaxKind.QuestionQuestionToken
  )
}

// record-binding escape/mutation guard for resolveExpr's identifier branch.
//
// Must catch a property write (`SIZES.small = 'p-[7px]'`),
// Object.assign/Reflect.set/Object.defineProperty onto the binding, or any
// other escape of the binding itself -- not just a bare `name = ...`
// reassignment. Must also walk the FULL scope rather than stop at the
// first function-like boundary: for a module-scope `const SIZES = {...}`,
// the scope IS the SourceFile, so a mutation written inside ANY function in
// the file (the common case: a component body) has to be visible too.
// Enforced by cross-scanner-false-green.test.mjs.
//
// recordBindingEscapes below is a direct structural sibling of
// dispatcherBindingUsesSafe (same file) and ts-colors.mjs's
// absenceBindingUsesSafe: it walks the FULL scope (real recursive descent,
// no function-boundary stop) looking for every occurrence of the binding's
// name, climbs each occurrence's containing property/element-access chain,
// and classifies the terminal use as either an ordinary read (safe), a
// direct mutation (assignment/delete/++/--/for-of-or-in target -- unsafe),
// or a container escape: the chain gets embedded in an array/object
// literal, a spread, a shorthand property, or passed as a bare call
// argument to ANY function.
//
// The container-escape branch is deliberately STRICTER than
// ts-colors.mjs::absenceBindingUsesSafe/knownClassDerivedUsesSafe, which
// both whitelist Object.keys/values/entries/freeze/isFrozen/
// getOwnPropertyNames as safe call arguments. That whitelist is exactly the
// nested-reference escape hole the same shared suite proved against the
// colour scanner: `Object.values(REC).forEach(v => { v.cls = '...' })`
// receives LIVE references to REC's own nested objects, so trusting
// Object.values/entries as "read-only" lets a later write through the
// returned references mutate REC without ever naming REC on the left of an
// assignment. No call name is safe to whitelist for a container-typed
// argument here -- only a chain that structurally resolves (via
// structuralValueAtPath, walking the SAME literal resolveExpr would have
// trusted) to a primitive leaf (string/number/boolean/null/template) is
// allowed to be embedded or passed, because a primitive cannot carry a
// mutable reference back into the record.
export function isAssignmentOperatorKind(kind) {
  return kind >= ts.SyntaxKind.FirstAssignment && kind <= ts.SyntaxKind.LastAssignment
}

export function isIncDecOperator(operator) {
  return operator === ts.SyntaxKind.PlusPlusToken || operator === ts.SyntaxKind.MinusMinusToken
}

export function isPrimitiveLeafNode(node) {
  const value = unwrap(node)
  if (!value) return false
  if (ts.isStringLiteralLike(value) || ts.isNumericLiteral(value)) return true
  if (ts.isNoSubstitutionTemplateLiteral(value) || ts.isTemplateExpression(value)) return true
  return [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)
}

export function isConstVariableDeclaration(declaration) {
  return ts.isVariableDeclaration(declaration) && !!(declaration.parent.flags & ts.NodeFlags.Const)
}

export function isReassignedWithin(owner, name) {
  let reassigned = false
  const visit = (node) => {
    if (reassigned) return
    if (node !== owner && ts.isFunctionLike(node)) return
    if (ts.isBinaryExpression(node)
      && ts.isIdentifier(node.left)
      && node.left.text === name
      && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment
      && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
      reassigned = true
      return
    }
    if ((ts.isPrefixUnaryExpression(node) || ts.isPostfixUnaryExpression(node))
      && ts.isIdentifier(node.operand)
      && node.operand.text === name
      && (node.operator === ts.SyntaxKind.PlusPlusToken || node.operator === ts.SyntaxKind.MinusMinusToken)) {
      reassigned = true
      return
    }
    ts.forEachChild(node, visit)
  }
  if (ts.isSourceFile(owner)) visit(owner)
  else if (owner.body) visit(owner.body)
  return reassigned
}

export function findNamedBinding(nameNode, name) {
  if (ts.isIdentifier(nameNode)) return nameNode.text === name ? { initializer: null } : null
  if (!ts.isObjectBindingPattern(nameNode) && !ts.isArrayBindingPattern(nameNode)) return null
  for (const element of nameNode.elements) {
    if (!ts.isBindingElement(element)) continue
    if (ts.isIdentifier(element.name) && element.name.text === name) return element
    const nested = findNamedBinding(element.name, name)
    if (nested) return nested
  }
  return null
}

export function findParameterBinding(parameters, name) {
  for (const parameter of parameters) {
    const binding = findNamedBinding(parameter.name, name)
    if (binding) return binding
  }
  return null
}

export function receivingSymbol(owner) {
  if ((ts.isFunctionDeclaration(owner) || ts.isFunctionExpression(owner) || ts.isMethodDeclaration(owner)) && owner.name) {
    return staticPropertyName(owner.name)
  }
  let current = owner.parent
  while (current) {
    if (ts.isVariableDeclaration(current) && ts.isIdentifier(current.name)) return current.name.text
    if (ts.isPropertyAssignment(current)) return staticPropertyName(current.name)
    if (ts.isFunctionLike(current) || ts.isStatement(current) || ts.isSourceFile(current)) return null
    current = current.parent
  }
  return null
}

export function nearestNamedAncestorOwner(fn) {
  let current = fn.parent
  while (current) {
    if (ts.isFunctionDeclaration(current) && current.name) return current.name.text
    if (ts.isArrowFunction(current) || ts.isFunctionExpression(current)) {
      const name = receivingSymbol(current)
      if (name) return name
    }
    current = current.parent
  }
  return null
}

export function isCssModuleClassReference(expr, sourceFile) {
  if (!ts.isPropertyAccessExpression(expr) || !ts.isIdentifier(expr.expression)) return false
  const bindingName = expr.expression.text
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    if (!/\.module\.css$/i.test(statement.moduleSpecifier.text)) continue
    const clause = statement.importClause
    if (clause?.name?.text === bindingName) return true
    if (clause?.namedBindings && ts.isNamespaceImport(clause.namedBindings) && clause.namedBindings.name.text === bindingName) return true
  }
  return false
}
