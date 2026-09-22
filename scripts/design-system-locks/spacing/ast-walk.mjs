#!/usr/bin/env node
// spacing/ast-walk.mjs
//
// The TS/JSX AST walk (visitNode and everything it dispatches to --
// class-builder detection, dispatcher-record resolution, style-object
// walking). Depends on ast-utils.mjs, value-rules.mjs and
// module-record-rules.mjs; nothing in those three calls back in here, so
// the module graph stays acyclic.

'use strict'

import ts from 'typescript'
import {
  RULE,
  UNRESOLVED_EXPR,
  calleeName,
  findLexicalBinding,
  findNamedBinding,
  findParameterBinding,
  isConcatOrLogic,
  isConstVariableDeclaration,
  isCssModuleClassReference,
  isPrimitiveLeafNode,
  isReassignedWithin,
  locFromTs,
  nearestNamedAncestorOwner,
  pushFinding,
  receivingSymbol,
  staticPropertyName,
  unwrap,
  withOrigin,
} from './ast-utils.mjs'
import {
  governedModulePath,
  importedDeclaration,
  isProvenBoolean,
  moduleRecord,
  pureFunctionReturns,
  recordDeclarationScope,
  resolveExpr,
  resolveModuleExport,
} from './module-record-rules.mjs'
import {
  SPACING_PROPERTIES,
  analyzeClassList,
  analyzeClassTemplate,
  analyzeStyleValue,
  kebab,
  scanInlineStyle,
} from './value-rules.mjs'

export const CLASS_BUILDERS = new Set(['cn', 'clsx', 'classNames', 'classnames', 'twMerge', 'twJoin', 'cva', 'cx', 'tv'])

// P10 port (typography.mjs::classBuilderOwnParameterForward) -- a
// CLASS_BUILDER's OWN definition (`export function cn(...inputs:
// ClassValue[]) { return twMerge(clsx(inputs)) }`, src/lib/utils.ts) is not
// a live class value to prove: visitNode/visitClassBuilderArg walk EVERY
// call to a CLASS_BUILDERS-named function anywhere in the file, not just
// inside a className/cn() call site, so `cn`'s own body -- which itself
// calls `twMerge`/`clsx`, both CLASS_BUILDERS members -- was being walked as
// though it were a real usage, and its own rest parameter flagged as an
// unresolved dynamic class expression. Every REAL class argument is already
// proven at each actual call site elsewhere; the definition itself only
// repackages/joins whatever was passed in. Narrow and structural (never a
// name allowlist beyond the already-trusted CLASS_BUILDERS set): the
// enclosing function's own resolvable declaration name must itself be a
// CLASS_BUILDER, it must take exactly one parameter (plain or rest), its
// body must be nothing but a single return of a chain of CLASS_BUILDER
// calls, and the identifier under test must be that same parameter forwarded
// unchanged (whole or spread) as a bare argument somewhere in that chain.
// Anything else -- extra statements, a differently-named receiver, a
// non-CLASS_BUILDER callee anywhere in the chain -- is left exactly as
// unproven as before (falls through to the existing proofs / unsupported).
export function classBuilderOwnParameterForward(identifier) {
  let fn = identifier.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn || fn.parameters.length !== 1) return false
  const parameter = fn.parameters[0]
  if (!ts.isIdentifier(parameter.name) || parameter.name.text !== identifier.text) return false
  const declarationName = classBuilderDeclarationName(fn)
  if (!declarationName || !CLASS_BUILDERS.has(declarationName)) return false
  const returned = classBuilderSingleReturnExpression(fn)
  if (!returned) return false
  return classBuilderChainForwardsParameter(returned, parameter.name.text, new Set())
}

// The stable name a function-like node is declared under -- a function
// declaration's own name, or the identifier of a `const NAME = (...) => ...`
// / `const NAME = function (...) {...}` it is the initializer of. Anything
// else (a method, an inline callback, an unnamed export default) is not a
// recognizable CLASS_BUILDERS declaration and returns null.
export function classBuilderDeclarationName(fn) {
  if (ts.isFunctionDeclaration(fn)) return fn.name ? fn.name.text : null
  const parent = fn.parent
  return parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) && parent.initializer === fn
    ? parent.name.text
    : null
}

// A function-like node's body reduced to its single meaningful expression: a
// block whose only statement is `return <expr>`, or an arrow function's
// direct expression body. Any other shape (multiple statements, no return, a
// non-expression return) is not provably transparent and returns null.
export function classBuilderSingleReturnExpression(fn) {
  if (!fn.body) return null
  if (!ts.isBlock(fn.body)) return fn.body
  if (fn.body.statements.length !== 1) return null
  const statement = fn.body.statements[0]
  return ts.isReturnStatement(statement) && statement.expression ? statement.expression : null
}

// True when `expression` -- after unwrapping parens/as/satisfies/non-null --
// is either the bare parameter identifier itself, or a call to a
// CLASS_BUILDERS-named function where at least one argument recursively
// forwards it the same way. `seen` guards against a call chain that somehow
// revisits the same node. Deliberately NOT extended to a spread argument
// (`clsx(...inputs)`): cn()'s real shape (src/lib/utils.ts) passes the whole
// array (`clsx(inputs)`), never a spread.
export function classBuilderChainForwardsParameter(expression, parameterName, seen) {
  const unwrapped = unwrap(expression)
  if (!unwrapped || seen.has(unwrapped)) return false
  seen.add(unwrapped)
  if (ts.isIdentifier(unwrapped)) return unwrapped.text === parameterName
  if (!ts.isCallExpression(unwrapped) || !ts.isIdentifier(unwrapped.expression) || !CLASS_BUILDERS.has(unwrapped.expression.text)) return false
  return unwrapped.arguments.some((argument) => classBuilderChainForwardsParameter(argument, parameterName, seen))
}

// Item 2 port (typography.mjs::transparentJoinerDeclaration, ChipListInput.tsx
// lead-assigned follow-up): a same-file, TOP-LEVEL `function name(...rest) {
// return rest.filter(Boolean).join(sep) }` (the `.filter(Boolean)` prefix is
// optional) -- a transparent variadic class joiner sharing cn()/clsx()'s own
// semantics under a project-local name. Detected structurally, never
// allowlisted by name: only a single rest parameter, a single statement, and
// a `.join()` call directly on that same rest parameter (optionally preceded
// by `.filter(Boolean)` on it) qualify -- extra statements, a differently-
// named receiver, or an additional transform in between all leave the call
// exactly as unproven as before (falls through to the pre-existing proofs /
// unsupported). Restricted to a top-level FunctionDeclaration (matching
// typography.mjs's own sameFileFunctionDeclaration) -- a nested or
// const-bound joiner is not this proof's target and stays unsupported.
export function transparentJoinerDeclaration(callee, sourceFile) {
  if (!ts.isIdentifier(callee)) return null
  let fnDecl = null
  for (const statement of sourceFile.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name?.text === callee.text && statement.body) { fnDecl = statement; break }
  }
  if (!fnDecl || fnDecl.parameters.length !== 1) return null
  const parameter = fnDecl.parameters[0]
  if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name)) return null
  const restName = parameter.name.text
  if (fnDecl.body.statements.length !== 1) return null
  const statement = fnDecl.body.statements[0]
  if (!ts.isReturnStatement(statement) || !statement.expression) return null
  const expression = statement.expression
  if (!ts.isCallExpression(expression) || !ts.isPropertyAccessExpression(expression.expression) || expression.expression.name.text !== 'join') return null
  let receiver = expression.expression.expression
  if (ts.isCallExpression(receiver) && ts.isPropertyAccessExpression(receiver.expression) && receiver.expression.name.text === 'filter') {
    receiver = receiver.expression.expression
  }
  return ts.isIdentifier(receiver) && receiver.text === restName ? fnDecl : null
}

export function visitNode(node, ctx, sourceFile) {
  if (ts.isJsxAttribute(node)) {
    const name = node.name.getText()
    if (name === 'className' || name === 'class') visitClassLike(node.initializer, ctx, sourceFile)
    if (name === 'style') visitStyleLike(node.initializer, ctx, sourceFile)
  } else if (ts.isPropertyAssignment(node)) {
    const name = staticPropertyName(node.name)
    if (name === 'className' || name === 'class') visitClassLike(node.initializer, ctx, sourceFile)
    if (name === 'style') visitStyleLike(node.initializer, ctx, sourceFile)
  } else if (ts.isCallExpression(node)) {
    const name = calleeName(node.expression)
    // FIX-P1 finding 1: a bare CLASS_BUILDERS name is trusted ONLY when there
    // is no local declaration anywhere in the file to collide with it
    // (hasLocalNameCollision) -- an unresolvable bare name can only ever be
    // the real ambient builder at runtime (anything else throws
    // ReferenceError), so trusting it is sound. guardClassBuilder separately
    // authenticates a renamed import: a builder imported as `clsx as
    // formatClasses` has a callee name outside CLASS_BUILDERS, so its
    // definition-site arguments would otherwise never be scanned anywhere
    // (HIGH bypass, frozen 1577aee7). What must NOT dispatch here is a LOCAL
    // function merely SHARING a CLASS_BUILDERS name (e.g. `function cx(...a)
    // { return 'p-[7px]' }`): its arguments are not its class content, its
    // return value is, and that is exactly what the generic
    // pureFunctionReturns path below (reached via visitClassBuilderArg)
    // analyzes -- dispatching it here made that path unreachable and hid the
    // real returned literal (lead-verified, see FIX-P1 finding 1 evidence).
    // hasLocalNameCollision is exactly what closes that gap: a same-file
    // FunctionDeclaration/const/etc. named `cx` collides, so the bare-name
    // trust is withdrawn and only guardClassBuilder's real-import
    // authentication can still dispatch it.
    if ((CLASS_BUILDERS.has(name) && !hasLocalNameCollision(sourceFile, name)) || guardClassBuilder(node.expression, ctx)) {
      for (const arg of node.arguments) visitClassBuilderArg(arg, ctx, sourceFile)
    }
  }
  ts.forEachChild(node, (child) => visitNode(child, ctx, sourceFile))
}

export function visitClassLike(initializer, ctx, sourceFile) {
  if (!initializer) return
  const expr = unwrap(ts.isJsxExpression(initializer) ? initializer.expression : initializer)
  if (!expr) return
  // FIX-P1 finding 1: same collision-gated bare-name rule as visitNode's
  // dispatch (see its own header comment) -- a same-file local declaration
  // sharing the CLASS_BUILDERS name withdraws the bare-name trust and falls
  // through to visitClassBuilderArg's generic proofs instead of a no-op.
  if (ts.isCallExpression(expr)) {
    const name = calleeName(expr.expression)
    if ((CLASS_BUILDERS.has(name) && !hasLocalNameCollision(sourceFile, name)) || guardClassBuilder(expr.expression, ctx)) return
  }
  visitClassBuilderArg(expr, ctx, sourceFile)
}

export function visitClassBuilderArg(node, ctx, sourceFile, useLoc = null, viaAliasResolution = false) {
  const expr = unwrap(node)
  if (!expr) return
  const location = useLoc ?? locFromTs(expr, sourceFile)
  if (ts.isIdentifier(expr) && expr.text === 'undefined') {
    // Absence proof mirrors the style path (hasUndefinedShadowOrDynamicScope):
    // a file that binds the name undefined, or has dynamic scopes, must not
    // treat the operand as absent. A lexically bound shadow then resolves and
    // reports its real value; an unprovable one blocks below.
    if (!hasUndefinedShadowOrDynamicScope(expr.getSourceFile())) return
  } else if (isProvenBoolean(expr, ctx)) return
  // P10: this identifier IS the parameter of the CLASS_BUILDER function
  // currently being DEFINED, forwarded unchanged into another CLASS_BUILDER
  // call -- the definition site itself, not a live call-site usage.
  if (ts.isIdentifier(expr) && classBuilderOwnParameterForward(expr)) return
  if (ts.isIdentifier(expr) || ts.isPropertyAccessExpression(expr) || ts.isElementAccessExpression(expr)) {
    const boundary = directForwardedParameter(expr, 'className') ?? forwardedClassLikeBoundary(expr)
      ?? (ts.isIdentifier(expr) ? bodyDestructuredClassBoundary(expr) : null)
    if (boundary) {
      // FIX-P1 finding 2: a boundary exists to protect against an UNKNOWN
      // caller's dynamic value. When the receiving component/function is
      // never exported from this module and every one of its call sites
      // resolvable in THIS SAME FILE supplies the class-like prop as a
      // static string literal, there is no unknown caller left -- the
      // literal(s) are ordinary spacing values, inspected directly instead
      // of hidden behind a boundary marker. Narrow and sound (see
      // sameFileLiteralClassProp's own header comment): a rename, a
      // non-literal call site, zero call sites, or any export path all keep
      // the existing conservative boundary exactly as before.
      const sameFileLiterals = sameFileLiteralClassProp(expr, sourceFile)
      if (sameFileLiterals) {
        for (const literal of sameFileLiterals) visitClassBuilderArg(literal, ctx, sourceFile, location)
        return
      }
      pushFinding(ctx, RULE.extensionBoundary, `${boundary.symbol}#${boundary.name}`, 'Spacing extension boundary; caller-provided className is forwarded unchanged and requires exact central review.', withOrigin(location, expr))
      if (boundary.initializer) visitClassBuilderArg(boundary.initializer, ctx, sourceFile)
      return
    }
    if (ts.isPropertyAccessExpression(expr) || ts.isElementAccessExpression(expr)) {
      const memberBoundary = parameterMemberBoundary(expr)
      if (memberBoundary) {
        pushFinding(ctx, RULE.extensionBoundary, `${memberBoundary.symbol}#${memberBoundary.name}`, 'Spacing extension boundary; caller-provided className is forwarded unchanged and requires exact central review.', withOrigin(location, expr))
        return
      }
    }
    const dispatcher = resolveDispatcherMember(expr, ctx)
      ?? resolveDestructuredDispatcherMember(expr, ctx)
      ?? resolveRecordChainMember(expr, ctx)
      ?? ((ts.isPropertyAccessExpression(expr) || ts.isElementAccessExpression(expr)) ? resolveArrayCallbackMember(expr, ctx) : null)
    if (dispatcher) {
      if (dispatcher.allAbsent) return
      for (const value of dispatcher.values) visitClassBuilderArg(value, ctx, value.getSourceFile(), location)
      return
    }
    const resolved = resolveExpr(expr, ctx)
    if (resolved === UNRESOLVED_EXPR) {
      if (isCssModuleClassReference(expr, sourceFile)) return
      const embeddingCall = chainHasDynamicElementAccess(expr) ? directClassBuilderCallEmbed(expr, ctx) : null
      if (embeddingCall) {
        pushFinding(ctx, RULE.unsupported, `className: ${embeddingCall.getText(sourceFile)}`, 'Unsupported spacing expression; a dynamic-key record read embedded directly in a class-builder call could not be proven safe.', withOrigin(location, embeddingCall))
        return
      }
      pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(sourceFile)}`, 'Unsupported spacing expression; dynamic or cyclic class aliases cannot be verified against the D10 scale.', withOrigin(location, expr))
      return
    }
    if (ts.isIdentifier(expr) && authenticatedBuilderAlias(expr, resolved, ctx)) return
    // An alias-resolved call is only proven for immutable identifier bindings
    // (authenticatedBuilderAlias above); mutable and member aliases must keep
    // the conservative call-branch treatment, so the delegation is flagged.
    if (resolved && resolved !== expr) visitClassBuilderArg(resolved, ctx, resolved.getSourceFile(), location, true)
    return
  }
  if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) {
    analyzeClassList(expr.text, ctx, withOrigin(location, expr))
    return
  }
  if (ts.isTemplateExpression(expr)) {
    analyzeClassTemplate(expr, ctx, sourceFile, withOrigin(location, expr))
    return
  }
  if (ts.isArrayLiteralExpression(expr)) {
    for (const element of expr.elements) visitClassBuilderArg(element, ctx, sourceFile, useLoc)
    return
  }
  if (ts.isObjectLiteralExpression(expr)) {
    visitClassObject(expr, ctx, sourceFile)
    return
  }
  if (ts.isConditionalExpression(expr)) {
    visitClassBuilderArg(expr.whenTrue, ctx, sourceFile, useLoc)
    visitClassBuilderArg(expr.whenFalse, ctx, sourceFile, useLoc)
    return
  }
  if (ts.isCallExpression(expr)) {
    const joined = literalClassArrayJoin(expr)
    if (joined) {
      for (const element of joined.elements) visitClassBuilderArg(element, ctx, sourceFile, useLoc)
      return
    }
    // Item 2 port (typography.mjs::transparentJoinerDeclaration): a same-file
    // top-level `function name(...rest) { return rest(.filter(Boolean))?.join(sep) }`
    // -- a transparent variadic class joiner sharing cn()/clsx()'s semantics
    // under a project-local name (ChipListInput.tsx's `classes()`). Proven
    // structurally (see transparentJoinerDeclaration below), never trusted by
    // name -- CLASS_BUILDERS stays a closed, authenticated set.
    const joinerDeclaration = transparentJoinerDeclaration(expr.expression, expr.getSourceFile())
    if (joinerDeclaration) {
      for (const argument of expr.arguments) visitClassBuilderArg(argument, ctx, sourceFile, useLoc)
      return
    }
    // Item 4 port (ts-colors.mjs::inspectClassExpr's cva-factory call-site
    // resolution): `badgeVariants({ variant })` calling a const bound to
    // `cva('base', { variants: {...} })` cannot be precisely resolved to the
    // exact string ONE specific variant selection produces -- but every
    // string the call could EVER return is already a member of the cva
    // definition's own config, checked once at the DEFINITION site
    // (visitClassObject, reached via visitNode's ordinary CLASS_BUILDERS
    // dispatch on the `cva(...)` call itself). Re-inspecting that SAME
    // definition at each call site is therefore sound -- it can never MISS a
    // violation the call could produce -- without needing to trace which
    // variant branch this specific call selects.
    const cvaDefinition = cvaFactoryDefinition(expr.expression, ctx)
    if (cvaDefinition) {
      for (const argument of cvaDefinition.arguments) visitClassBuilderArg(argument, ctx, argument.getSourceFile())
      return
    }
    if (!viaAliasResolution && guardClassBuilder(expr.expression, ctx) && builderArgumentsStaticallyGoverned(expr, ctx)) return
    const returns = pureFunctionReturns(expr, ctx)
    if (returns) {
      for (const returned of returns) visitClassBuilderArg(returned, ctx, returned.getSourceFile(), location)
      return
    }
    pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; dynamic or impure class helpers cannot be verified against the D10 scale.', withOrigin(location, expr))
    return
  }
  if (ts.isBinaryExpression(expr) && isConcatOrLogic(expr.operatorToken.kind)) {
    if (expr.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken
      && (isProvenBoolean(expr.left, ctx) || wholeClassGuardContext(expr, ctx))) {
      visitClassBuilderArg(expr.right, ctx, sourceFile, useLoc)
      return
    }
    visitClassBuilderArg(expr.left, ctx, sourceFile, useLoc)
    visitClassBuilderArg(expr.right, ctx, sourceFile, useLoc)
    return
  }
  if ([ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword, ts.SyntaxKind.OmittedExpression].includes(expr.kind) || ts.isNumericLiteral(expr)) return
  pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; this class expression requires explicit analysis.', withOrigin(location, expr))
}

// An authenticated builder call in a non-top-level position (conditional
// branch, nullish operand, nested argument) carries no unsupported marker of
// its own when a dry run proves every argument statically governed: the
// definition dispatch (visitNode) analyzes those arguments exactly once. Any
// unresolvable argument — including the wrapper's own rest parameter — keeps
// the marker, so the class-builder-infrastructure findings stay blocking.
// The dry run shares the module cache but discards its findings.
export function builderArgumentsStaticallyGoverned(call, ctx) {
  const shadow = { ...ctx, findings: [] }
  for (const argument of call.arguments) visitClassBuilderArg(argument, shadow, call.getSourceFile())
  return !shadow.findings.some((finding) => finding.ruleId === RULE.unsupported || finding.ruleId === RULE.parseError)
}

// A falsy && left operand cannot itself be a spacing class. This does not
// establish a class fragment or the input semantics of an arbitrary helper.
export function wholeClassGuardContext(expression, ctx) {
  let node = expression
  while (node.parent) {
    const parent = node.parent
    if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent)
      || ts.isTypeAssertionExpression(parent) || ts.isNonNullExpression(parent)
      || ts.isSatisfiesExpression(parent) || ts.isJsxExpression(parent)) { node = parent; continue }
    if (ts.isBinaryExpression(parent) && [ts.SyntaxKind.AmpersandAmpersandToken,
      ts.SyntaxKind.BarBarToken, ts.SyntaxKind.QuestionQuestionToken].includes(parent.operatorToken.kind)) { node = parent; continue }
    if (ts.isConditionalExpression(parent) && (parent.whenTrue === node || parent.whenFalse === node)) { node = parent; continue }
    if (ts.isJsxAttribute(parent) || ts.isPropertyAssignment(parent)) {
      return ['className', 'class'].includes(staticPropertyName(parent.name))
    }
    if (ts.isCallExpression(parent) && parent.arguments.includes(node)) return guardClassBuilder(parent.expression, ctx)
    return false
  }
  return false
}

// guardImport is pure per (source file, identifier text): the shadow walk,
// reassignment scan and import match list depend only on those. Memoize per
// SourceFile object so the builder dispatch can probe every call expression
// without re-walking the file; each scan parses fresh SourceFile objects, so
// entries never outlive their scan.
export const importGuardMemo = new WeakMap()

export function guardImport(identifier) {
  if (!identifier || !ts.isIdentifier(identifier)) return null
  const source = identifier.getSourceFile()
  let perFile = importGuardMemo.get(source)
  if (!perFile) {
    perFile = new Map()
    importGuardMemo.set(source, perFile)
  }
  if (perFile.has(identifier.text)) return perFile.get(identifier.text)
  const result = computeGuardImport(identifier, source)
  perFile.set(identifier.text, result)
  return result
}

// Whole-file shadow rejection is deliberately conservative: an unrelated
// binding with the same name cannot accidentally authenticate a local helper.
// Factored out of computeGuardImport (FIX-P1 finding 1) so the CLASS_BUILDERS
// bare-name dispatch fallback below can reuse the exact same collision test:
// a name with NO local declaration anywhere in the file and no dynamic scope
// can only ever resolve, at runtime, to whatever real ambient/global binding
// it is -- for a project source file that is either a genuine import (already
// separately authenticated by guardClassBuilder) or a ReferenceError, never a
// same-file lookalike. A name WITH a local declaration is exactly the
// lookalike shape the finding closes (`function cx(...) { return 'p-[7px]' }`)
// and must fall through to the generic proofs instead of the bare-name trust.
export const nameCollisionMemo = new WeakMap()

export function hasLocalNameCollision(source, name) {
  let perFile = nameCollisionMemo.get(source)
  if (!perFile) { perFile = new Map(); nameCollisionMemo.set(source, perFile) }
  if (perFile.has(name)) return perFile.get(name)
  let unsafe = false
  const visit = node => {
    if ((ts.isVariableDeclaration(node) || ts.isParameter(node) || ts.isFunctionDeclaration(node)
      || ts.isFunctionExpression(node) || ts.isClassDeclaration(node) || ts.isClassExpression(node)
      || ts.isEnumDeclaration(node) || ts.isModuleDeclaration(node) || ts.isImportEqualsDeclaration(node))
      && node.name && findNamedBinding(node.name, name)) unsafe = true
    if (ts.isWithStatement(node) || (ts.isCallExpression(node) && calleeName(node.expression) === 'eval')) unsafe = true
    ts.forEachChild(node, visit)
  }
  visit(source)
  const result = unsafe || isReassignedWithin(source, name)
  perFile.set(name, result)
  return result
}

export function computeGuardImport(identifier, source) {
  const matches = []
  if (hasLocalNameCollision(source, identifier.text)) return null
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier) || statement.importClause?.isTypeOnly) continue
    const clause = statement.importClause
    if (clause?.name?.text === identifier.text) matches.push({ specifier: statement.moduleSpecifier.text, imported: 'default' })
    const bindings = clause?.namedBindings
    if (bindings && ts.isNamedImports(bindings)) for (const element of bindings.elements) {
      if (!element.isTypeOnly && element.name.text === identifier.text) matches.push({ specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    }
  }
  return matches.length === 1 ? matches[0] : null
}

// Item 4 helpers. cva is authenticated separately from guardClassBuilder
// above: that function verifies clsx/classnames/tailwind-merge/a locally-
// defined cn() wrapper specifically, none of which share cva's "config
// object -> call-site-selected subset" shape, so it has no cva branch at
// all -- matched here by literal CLASS_BUILDERS name (the pre-existing,
// pre-trusted dispatch every other builder already uses) or by a verified
// import from 'class-variance-authority', covering a renamed import
// (`import { cva as variants } from 'class-variance-authority'`) the same
// way guardClassBuilder covers a renamed clsx/cn.
// Deliberately NOT trusted by bare CLASS_BUILDERS name-matching the way
// every OTHER builder call is elsewhere in this file (visitNode's own
// top-level dispatch, guardClassBuilder's clsx/classnames/tailwind-merge
// branches): those all treat the CALL's OWN arguments, at THAT call site,
// as the class content to check -- safe regardless of what the named
// function actually does at runtime, since nothing is inferred about ANY
// OTHER call. cvaFactoryDefinition instead assumes that calling the
// resolved factory AGAIN LATER, with DIFFERENT runtime arguments, can only
// ever produce content already present in the ORIGINAL cva(...) definition
// -- an assumption that holds for the real cva (its whole contract), but
// is unsound for an unrelated function merely NAMED `cva` (verified by a
// mutation fixture: a bare-name-only local `function cva() { return () =>
// RAW_VALUE }` would walk zero arguments at its own call site and silently
// report NOTHING, hiding whatever RAW_VALUE actually contains -- a false
// green, not a false red). Requiring a verified import from the real
// 'class-variance-authority' package closes that gap.
export function isCvaCallee(callee) {
  const unwrapped = unwrap(callee)
  if (!unwrapped) return false
  const imported = guardImport(unwrapped)
  return imported?.specifier === 'class-variance-authority' && imported.imported === 'cva'
}

// Resolves `callee` (a call's own expression, e.g. `badgeVariants` in
// `badgeVariants({ variant })`) to the `cva(...)` CallExpression it is
// declared equal to, when `callee` is an unambiguous `const` binding (local
// declaration or import) whose initializer is itself an authenticated cva
// call -- otherwise null, leaving the call exactly as unresolved as before.
export function cvaFactoryDefinition(callee, ctx) {
  if (!ts.isIdentifier(callee)) return null
  const binding = findLexicalBinding(callee, callee.text)
  const imported = binding ? null : importedDeclaration(callee, ctx, new Set())
  const resolvedBinding = binding ?? imported
  if (!resolvedBinding || !resolvedBinding.initializer || !ts.isVariableDeclaration(resolvedBinding.declaration) || !isConstVariableDeclaration(resolvedBinding.declaration)) return null
  const initializer = unwrap(resolvedBinding.initializer)
  if (!initializer || !ts.isCallExpression(initializer) || !isCvaCallee(initializer.expression)) return null
  return initializer
}

export function guardClassBuilder(expression, ctx) {
  const callee = unwrap(expression)
  const imported = guardImport(callee)
  if (imported) {
    if (imported.specifier === 'clsx' && ['default', 'clsx'].includes(imported.imported)) return true
    if (imported.specifier === 'classnames' && imported.imported === 'default') return true
    if (imported.specifier === 'tailwind-merge' && ['twMerge', 'twJoin'].includes(imported.imported)) return true
    // cva/tv are deliberately NOT authenticated here even when genuinely
    // imported: unlike clsx/classnames/twMerge/twJoin/cn, a bare cva/tv call's
    // OWN arguments are not unconditionally "the resolved class content" --
    // cva returns a variant-selector FUNCTION, not a class string, so a
    // renamed cva call used directly as a value (`const badge = variants(...);
    // <span className={badge}/>`) must stay unsupported (pinned by "keeps a
    // renamed cva factory blocking in alias and direct positions"). The
    // UNRENAMED case is covered by the CLASS_BUILDERS bare-name fallback
    // below (a real import never collides with hasLocalNameCollision, since
    // an ImportDeclaration is not one of the collision node kinds), and every
    // renamed cva/tv call site is separately authenticated by the narrower,
    // real-shape-aware isCvaCallee/cvaFactoryDefinition path.
    if (imported.imported !== 'cn') return false
    return verifyCnWrapperShape(resolveModuleExport(ctx, callee.getSourceFile().fileName, imported.specifier, imported.imported, new Set()))
  }
  // No import at all: authenticate a cn() wrapper declared AND used within
  // THIS SAME FILE (src/lib/utils.ts's own real shape, mirrored locally in a
  // single-module fixture that never imports it from anywhere) by the
  // identical structural verification below, applied to the local
  // declaration instead of a resolved cross-module export. Restricted to the
  // literal name 'cn' -- same precision scope as the imported branch above;
  // a differently-named local wrapper is not this proof's target. Uses
  // soleLocalDeclaration, NOT functionDeclarationFor -- functionDeclarationFor
  // falls back to resolving a same-named IMPORT when no local declaration is
  // in scope, which would silently re-authenticate the exact shadow-import
  // case guardImport's own whole-file shadow rejection exists to block
  // ("keeps the alias unsupported when the consumer shadows the cn binding").
  if (!ts.isIdentifier(callee) || callee.text !== 'cn') return false
  return verifyCnWrapperShape(soleLocalDeclaration(callee.getSourceFile(), 'cn'))
}

// The single, unambiguous FunctionDeclaration binding `name` at module scope
// -- same whole-file shadow-rejection philosophy as guardImport/
// computeGuardImport's own "an unrelated binding with the same name cannot
// accidentally authenticate a local helper", applied to a purely local
// (non-imported) declaration instead of an import specifier. ANY other node
// binding `name` anywhere in the file (a parameter, a `var`/`let`/`const`, a
// second same-named function, a class, an enum, a dynamic scope) makes it
// unsafe to trust, and so does a whole-file reassignment of it.
export function soleLocalDeclaration(source, name) {
  const matches = []
  let unsafe = false
  const visit = node => {
    if (ts.isFunctionDeclaration(node) && node.name?.text === name) matches.push(node)
    else if ((ts.isVariableDeclaration(node) || ts.isParameter(node) || ts.isFunctionExpression(node)
      || ts.isClassDeclaration(node) || ts.isClassExpression(node) || ts.isEnumDeclaration(node)
      || ts.isModuleDeclaration(node) || ts.isImportEqualsDeclaration(node))
      && node.name && findNamedBinding(node.name, name)) unsafe = true
    if (ts.isWithStatement(node) || (ts.isCallExpression(node) && calleeName(node.expression) === 'eval')) unsafe = true
    ts.forEachChild(node, visit)
  }
  visit(source)
  if (unsafe || matches.length !== 1 || isReassignedWithin(source, name)) return null
  return matches[0]
}

// The exact structural shape src/lib/utils.ts's real cn() wrapper has: a
// single rest parameter, a single statement, `return
// mergeBuilder(clsxBuilder(inputs))`, both builders genuinely imported (or,
// for mergeBuilder, extendTailwindMerge-derived), and the parameter forwarded
// unchanged. Shared by both the imported-cn path and the local-declaration
// path above so a lookalike cannot pass either route without the real shape.
export function verifyCnWrapperShape(declaration) {
  if (!declaration || !ts.isFunctionDeclaration(declaration) || !declaration.body || declaration.asteriskToken
    || declaration.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword)
    || declaration.parameters.length !== 1 || declaration.body.statements.length !== 1
    || !guardWrapperBindingStable(declaration)) return false
  const parameter = declaration.parameters[0]
  const statement = declaration.body.statements[0]
  if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name) || !ts.isReturnStatement(statement) || !statement.expression) return false
  const outer = unwrap(statement.expression)
  if (!ts.isCallExpression(outer) || outer.arguments.length !== 1 || !guardMergeBuilder(outer.expression)) return false
  const inner = unwrap(outer.arguments[0])
  if (!ts.isCallExpression(inner) || inner.arguments.length !== 1) return false
  const innerImport = guardImport(unwrap(inner.expression))
  const input = unwrap(inner.arguments[0])
  return innerImport?.specifier === 'clsx' && ['default', 'clsx'].includes(innerImport.imported)
    && ts.isIdentifier(input) && input.text === parameter.name.text
}

// Function declarations are assignable live exports. The initial return shape
// is insufficient if the defining module can write or expose that binding.
// Reject ambiguous same-name references conservatively, including nested ones.
export function guardWrapperBindingStable(declaration) {
  if (!declaration.name || !ts.isIdentifier(declaration.name)) return false
  let safe = true
  const visit = node => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === declaration.name.text && node !== declaration.name) {
      if (!ts.isCallExpression(node.parent) || node.parent.expression !== node) { safe = false; return }
    }
    ts.forEachChild(node, visit)
  }
  visit(declaration.getSourceFile())
  return safe
}

export function guardMergeBuilder(expression) {
  const callee = unwrap(expression)
  const imported = guardImport(callee)
  if (imported?.specifier === 'tailwind-merge' && ['twMerge', 'twJoin'].includes(imported.imported)) return true
  if (!callee || !ts.isIdentifier(callee)) return false
  const binding = findLexicalBinding(callee, callee.text)
  if (!binding || !ts.isVariableDeclaration(binding.declaration) || !(binding.declaration.parent.flags & ts.NodeFlags.Const)
    || isReassignedWithin(callee.getSourceFile(), callee.text)) return false
  const initializer = unwrap(binding.initializer)
  if (!initializer || !ts.isCallExpression(initializer)) return false
  const factory = guardImport(unwrap(initializer.expression))
  return factory?.specifier === 'tailwind-merge' && factory.imported === 'extendTailwindMerge'
}

// A const identifier (local declaration or const import binding) whose
// initializer is an authenticated class-builder call governs that call at its
// definition site: visitNode scans every builder call in the defining file, so
// the consuming sink adds no evidence by re-walking the arguments, and
// re-walking would double-report each literal at the consuming path. Only an
// immutable binding may clear a sink through its initializer — resolveExpr
// cannot see function-local reassignment, so let/var aliases and parameters
// keep their blocking finding. Member and element reads are excluded: object
// properties are writable and belong to the finite-config classification.
export function authenticatedBuilderAlias(identifier, resolved, ctx) {
  if (!resolved || !ts.isCallExpression(resolved) || !guardClassBuilder(resolved.expression, ctx)) return false
  const binding = findLexicalBinding(identifier, identifier.text)
  if (binding) return isConstVariableDeclaration(binding.declaration)
  const imported = importedDeclaration(identifier, ctx, new Set())
  return !!imported && isConstVariableDeclaration(imported.declaration)
}

export function literalClassArrayJoin(expr) {
  if (!ts.isPropertyAccessExpression(expr.expression) || expr.expression.name.text !== 'join') return null
  const array = unwrap(expr.expression.expression)
  const separator = expr.arguments.length === 1 ? unwrap(expr.arguments[0]) : null
  if (!array || !ts.isArrayLiteralExpression(array) || array.elements.some(ts.isSpreadElement)) return null
  if (!separator || !ts.isStringLiteral(separator) || !/^[ \t\n\r\f]+$/.test(separator.text)) return null
  return array
}

export function directForwardedParameter(expr, name) {
  if (!ts.isIdentifier(expr) || expr.text !== name) return null
  let owner = expr.parent
  while (owner && !ts.isFunctionLike(owner)) owner = owner.parent
  if (!owner) return null
  const parameter = findParameterBinding(owner.parameters, expr.text)
  if (!parameter || isReassignedWithin(owner, expr.text)) return null
  const symbol = receivingSymbol(owner)
  if (!symbol) return null
  return { symbol, name: expr.text, initializer: parameter.initializer ?? null }
}

// Item 1 port (typography.mjs::isClassLikeParameterName + forwardedClassBoundary,
// lead-assigned wave-3 follow-up to the CAP-D1/D2 narrowing above). A
// parameter/property name carries a whole CSS class value, not just the
// literal `className`/`class` (react-day-picker's `Chevron: ({ className:
// chevronClassName })`, sheet.tsx's `widthClass`, smart-select's
// `triggerClassName`, dialog.tsx's `overlayClassName`, table.tsx's
// `containerClassName`, ...). Matched structurally (camelCase `...Class`/
// `...ClassName` suffix) so directForwardedParameter's existing proof below
// covers every such prop without a per-component allowlist. This only ever
// WIDENS which identifiers are eligible for the existing unchanged-forwarding
// proof (still gated by the same owner/reassignment checks); a false match
// degrades at worst to an extra blocking extension-boundary finding, never a
// silent pass.
export function isClassLikeParameterName(name) {
  return name === 'className' || name === 'class'
    || /(?:^|[a-z0-9])(?:ClassName|Class)$/.test(name)
}

// Generalizes directForwardedParameter('className') to any class-like-named
// parameter (isClassLikeParameterName above), including one destructured
// out of a JSX render-prop's own inline object literal (`components={{
// Chevron: ({ className: chevronClassName }) => <Icon
// className={chevronClassName}/> }}`, calendar.tsx's real shape). Unlike
// typography.mjs's forwardedClassBoundary, this does not remap a renamed
// destructured element back to its SOURCE property name ('className') --
// every real target of this port is already a class-like-named identifier
// at its own read site (chevronClassName/triggerClassName/widthClass/...),
// so the local name IS the exact receiving identity to report; adding the
// source-name remap would be unexercised, unproven complexity, not a real
// capability gap. receivingSymbol already resolves the enclosing owner for
// BOTH a plain variable-declared component AND a render-prop's own inline
// PropertyAssignment (`Chevron: (...) => ...` climbs straight to
// `ts.isPropertyAssignment(current)`, returning 'Chevron') -- no separate
// render-prop-owner climb is needed.
export function forwardedClassLikeBoundary(expr) {
  if (!ts.isIdentifier(expr) || !isClassLikeParameterName(expr.text)) return null
  let owner = expr.parent
  while (owner && !ts.isFunctionLike(owner)) owner = owner.parent
  if (!owner) return null
  const parameter = findParameterBinding(owner.parameters, expr.text)
  if (!parameter || isReassignedWithin(owner, expr.text)) return null
  const symbol = receivingSymbol(owner)
  if (!symbol) return null
  return { symbol, name: expr.text, initializer: parameter.initializer ?? null }
}

// Item 1 port (typography.mjs::forwardedClassBoundary's body-destructuring
// branch): `const { className: alias } = containerProps` where
// `containerProps` is itself a NAMED element of the enclosing function's own
// parameter list (table.tsx's Table: `({ className, containerProps = {},
// ...props }, ref) => { const { className: containerClassName } =
// containerProps; ... }`). Distinct from forwardedClassLikeBoundary (which
// proves a DIRECTLY destructured parameter unchanged): here the caller-
// supplied value passes through TWO destructuring hops. The SOURCE property
// name is always `className`/`class`, never the broader class-like-name
// heuristic -- an arbitrary property name on a body destructure carries no
// naming signal it is ever a real caller-forwarded value; the direct-
// parameter branch's heuristic already covers named forwards at the OUTER
// hop. A REST element (`...rest`) is excluded from the eligible parameter
// names -- it is a freshly assembled object of whatever remains, not itself a
// single named caller-supplied value -- and only the INNERMOST unambiguous
// destructure of a name qualifies, matching every other body-destructuring
// proof convention. The finding names the receiving identity prefixed with
// the source parameter name WHEN that source was itself reached through a
// destructured element of the function's parameter list (`containerProps`,
// not the whole-props catch-all) -- `Table#containerProps.className`, never
// colliding with the SAME function's own direct className boundary
// (`Table#className`); a source bound as a plain identifier parameter
// (`function Chip(props) { const { className } = props }`) keeps the bare
// `Chip#className` form, matching typography.mjs's identical naming rule.
export function bodyDestructuredClassBoundary(identifier) {
  let fn = identifier.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn || !fn.body) return null
  const plainParameterNames = new Set()
  const destructuredParameterNames = new Set()
  for (const parameter of fn.parameters) {
    if (ts.isIdentifier(parameter.name)) plainParameterNames.add(parameter.name.text)
    else if (ts.isObjectBindingPattern(parameter.name)) {
      for (const element of parameter.name.elements) {
        if (ts.isBindingElement(element) && !element.dotDotDotToken && ts.isIdentifier(element.name)) destructuredParameterNames.add(element.name.text)
      }
    }
  }
  let match = null
  for (let scope = identifier.parent; scope && scope !== fn; scope = scope.parent) {
    if (!ts.isBlock(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
      for (const candidate of statement.declarationList.declarations) {
        if (!ts.isObjectBindingPattern(candidate.name)) continue
        const source = candidate.initializer
        if (!ts.isIdentifier(source) || !(plainParameterNames.has(source.text) || destructuredParameterNames.has(source.text))) continue
        for (const element of candidate.name.elements) {
          if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
          const property = element.propertyName ? staticPropertyName(element.propertyName) : element.name.text
          if (property === 'className' || property === 'class') matches.push({ initializer: element.initializer ?? null, source: source.text, property })
        }
      }
    }
    if (matches.length > 1) return null
    if (matches.length === 1) { match = matches[0]; break }
  }
  if (!match) return null
  if (isReassignedWithin(fn, identifier.text) || isReassignedWithin(fn, match.source)) return null
  // Mutating the source parameter's own className member before the read
  // breaks unchanged provenance -- mirrors typography.mjs's own
  // sourceParameterName reassignment guard, deliberately NOT stopping at
  // nested function-like nodes (a mutation performed inside a closure over
  // the source parameter is exactly as real as one written directly here).
  let mutated = false
  const check = (node) => {
    if (mutated) return
    if (ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) && node.left.name.text === match.property
      && ts.isIdentifier(node.left.expression) && node.left.expression.text === match.source
      && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { mutated = true; return }
    if (ts.isDeleteExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === match.property
      && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === match.source) { mutated = true; return }
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'assign'
      && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object'
      && node.arguments.length > 0 && ts.isIdentifier(node.arguments[0]) && node.arguments[0].text === match.source) { mutated = true; return }
    ts.forEachChild(node, check)
  }
  check(fn.body)
  if (mutated) return null
  const symbol = receivingSymbol(fn)
  if (!symbol) return null
  const boundaryName = destructuredParameterNames.has(match.source) ? `${match.source}.${match.property}` : match.property
  return { symbol, name: boundaryName, initializer: match.initializer }
}

// A MODULE-PRIVATE component/function's class-like prop, read back
// unchanged inside its own body, only needs the conservative
// extension-boundary treatment because an UNKNOWN outside caller might pass
// a dynamic value.
// When the receiver is never exported from this module (no `export`
// modifier, no `export default`, no `export { name }`) and EVERY call site
// resolvable in THIS SAME FILE supplies the prop as a static string literal,
// there is no unknown caller left to protect against -- the literal(s) are
// ordinary spacing values. Deliberately narrow, matching only the exact
// shape this recovers soundly:
//   - the read-site identifier must be a PLAIN parameter, or a destructured
//     element whose local name is UNRENAMED from its source property --
//     otherwise the outward-facing prop name a caller writes could differ
//     from `expr.text`, and the call-site search below would be checking the
//     wrong key (a renamed shape, e.g. `{ className: chevronClassName }`,
//     falls through to the ordinary boundary exactly as before);
//   - the receiver must be declared directly at module top level (a
//     FunctionDeclaration, or a single-declarator top-level `const X = (...)
//     => ...`) -- a nested/locally-scoped same-named function is left
//     exactly as unproven as before, since a whole-module call-site scan
//     would not be scoped correctly to it;
//   - at least one call site must be found, and every one of them (a JSX
//     element/self-closing element or a plain call expression naming the
//     receiver) must supply the prop as a literal -- a single missing or
//     dynamic value at any call site aborts the whole recovery.
// This only ever RECOVERS a proof (substituting literals for a boundary
// marker); it can never suppress a finding a non-literal or exported shape
// would have produced, and it never touches the two-hop
// bodyDestructuredClassBoundary or parameterMemberBoundary shapes below,
// which stay exactly as conservative as before.
export function sameFileLiteralClassProp(expr, sourceFile) {
  if (!ts.isIdentifier(expr)) return null
  let fn = expr.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn) return null
  const parameter = findParameterBinding(fn.parameters, expr.text)
  if (!parameter) return null
  if ('propertyName' in parameter && parameter.propertyName
    && (!ts.isIdentifier(parameter.propertyName) || parameter.propertyName.text !== expr.text)) return null
  if (isReassignedWithin(fn, expr.text)) return null
  const statement = topLevelComponentStatement(fn, sourceFile)
  if (!statement) return null
  const symbol = receivingSymbol(fn)
  if (!symbol || isExportedComponentName(statement, symbol, sourceFile)) return null
  const sites = moduleCallSitesFor(symbol, sourceFile, fn)
  if (sites.length === 0) return null
  const literals = []
  for (const site of sites) {
    const literal = callSitePropLiteral(site, expr.text)
    if (!literal) return null
    literals.push(literal)
  }
  return literals
}

// The top-level statement declaring `fn`, restricted to the two shapes a
// same-file call-site scan can be soundly scoped to: a top-level
// FunctionDeclaration, or a top-level `const NAME = (...) => ...` /
// `const NAME = function (...) {...}` with exactly one declarator (so `NAME`
// is unambiguous). Anything nested inside another function/block -- or a
// multi-declarator `const a = ..., b = ...` statement -- returns null; the
// whole-module call-site scan below is only correctly scoped for a name bound
// directly at module top level.
export function topLevelComponentStatement(fn, sourceFile) {
  if (ts.isFunctionDeclaration(fn)) return sourceFile.statements.includes(fn) ? fn : null
  if (ts.isArrowFunction(fn) || ts.isFunctionExpression(fn)) {
    const declarator = fn.parent
    if (!declarator || !ts.isVariableDeclaration(declarator) || declarator.initializer !== fn) return null
    const list = declarator.parent
    if (!list || !ts.isVariableDeclarationList(list) || list.declarations.length !== 1) return null
    const statement = list.parent
    return statement && ts.isVariableStatement(statement) && sourceFile.statements.includes(statement) ? statement : null
  }
  return null
}

// True when `symbol` is reachable from outside this module: the declaring
// statement itself carries `export`/`export default`, or a separate
// `export { symbol }` (optionally `export { local as symbol }`, matched by
// LOCAL name since that is what a same-module reference would use) or
// `export default symbol` re-exports it.
export function isExportedComponentName(statement, symbol, sourceFile) {
  if (statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword || modifier.kind === ts.SyntaxKind.DefaultKeyword)) return true
  for (const other of sourceFile.statements) {
    if (ts.isExportAssignment(other) && ts.isIdentifier(other.expression) && other.expression.text === symbol) return true
    if (ts.isExportDeclaration(other) && other.exportClause && ts.isNamedExports(other.exportClause)) {
      for (const element of other.exportClause.elements) {
        if ((element.propertyName?.text ?? element.name.text) === symbol) return true
      }
    }
  }
  return false
}

// Every same-file call site of `symbol`: a JSX element/self-closing element
// tagged with the bare identifier `symbol`, or a plain call expression whose
// callee is the bare identifier `symbol` -- excluding `fn` itself (its own
// declaration is not a call site). A tag/callee reached through a property
// access (`<Foo.Inner/>`) is not matched; that under-counts call sites
// (missing one only makes `sites.length` smaller or zero) rather than
// over-counting, so it can only fall back to the existing boundary, never
// wrongly recover one.
export function moduleCallSitesFor(symbol, sourceFile, fn) {
  const sites = []
  const visit = (node) => {
    if (node !== fn) {
      if ((ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) && ts.isIdentifier(node.tagName) && node.tagName.text === symbol) {
        sites.push({ kind: 'jsx', node })
      } else if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === symbol) {
        sites.push({ kind: 'call', node })
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return sites
}

// The static string literal a single call site supplies for `propName`, or
// null when the prop is absent, shorthand (no initializer), or any non-
// literal expression -- absence/dynamism is never assumed safe.
export function callSitePropLiteral(site, propName) {
  if (site.kind === 'jsx') {
    const attribute = site.node.attributes.properties.find((property) => ts.isJsxAttribute(property) && staticPropertyName(property.name) === propName)
    if (!attribute || !attribute.initializer) return null
    if (ts.isStringLiteral(attribute.initializer)) return attribute.initializer
    if (ts.isJsxExpression(attribute.initializer) && attribute.initializer.expression) {
      const inner = unwrap(attribute.initializer.expression)
      if (inner && (ts.isStringLiteral(inner) || ts.isNoSubstitutionTemplateLiteral(inner))) return inner
    }
    return null
  }
  const firstArgument = site.node.arguments[0] && unwrap(site.node.arguments[0])
  if (!firstArgument || !ts.isObjectLiteralExpression(firstArgument)) return null
  const property = firstArgument.properties.find((candidate) => ts.isPropertyAssignment(candidate) && staticPropertyName(candidate.name) === propName)
  if (!property) return null
  const value = unwrap(property.initializer)
  return value && (ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)) ? value : null
}

// Item 1 port (typography.mjs::parameterMemberBoundary, Capability B): `X.
// className` / `X?.className` where X is an unmutated, bare (non-
// destructured) parameter of the nearest enclosing function -- e.g. a
// `.map((item) => <X className={item.className} />)` callback parameter
// (smart-select.tsx's real shape). Distinct from forwardedClassLikeBoundary
// (which proves the identifier ITSELF is an unchanged forwarded className):
// here the identifier is a param and only ONE MEMBER of it is read.
// Restricted to the literal `className`/`class` key (not the broader
// class-like-name heuristic): unlike a same-named parameter, an arbitrary
// property name carries no naming signal that it is a CSS class at all.
export function parameterMemberBoundary(node) {
  const key = ts.isPropertyAccessExpression(node) ? node.name.text
    : (() => { const argument = node.argumentExpression && unwrap(node.argumentExpression)
      return argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument)) ? argument.text : null })()
  if (key !== 'className' && key !== 'class') return null
  const base = unwrap(node.expression)
  if (!ts.isIdentifier(base)) return null
  let fn = node.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn) return null
  const parameter = findParameterBinding(fn.parameters, base.text)
  if (!parameter || isReassignedWithin(fn, base.text)) return null
  // receivingSymbol alone does not resolve an anonymous callback passed
  // directly as a bare call argument (`items.map((item) => ...)`, not
  // registered under a named variable/property) -- fall back to the nearest
  // NAMED enclosing function/component as the stable receiving identity.
  const ownerName = receivingSymbol(fn) ?? nearestNamedAncestorOwner(fn)
  if (!ownerName) return null
  return { symbol: ownerName, name: `${base.text}.${key}` }
}

// finite-dispatcher member resolution: `identifier.prop` where identifier is
// a single const binding, declared inside a function, whose initializer
// resolves (through ternary chains and calls to "finite dispatcher"
// functions — see collectFiniteDispatcherReturns) to a closed set of object
// literals. Two provable outcomes:
//   - ABSENCE: every literal omits `prop` — the read is PROVEN to always be
//     `undefined`. An unshadowed `undefined` already contributes nothing to
//     a class-builder argument (see the identifier-`undefined` branch
//     above); this extends the same absence proof to a member read whose
//     value is provably always undefined rather than the literal identifier
//     `undefined` itself.
//   - PRESENCE: every literal either omits `prop` (contributes nothing, same
//     as the absence case) or sets it to a plain initializer expression —
//     each present initializer is then visited exactly like an array
//     literal's elements (own node identity via withOrigin at the eventual
//     leaf, so distinct declared values are never conflated, and repeat
//     consumption of the identical literal collapses as usual).
// Any branch this cannot classify (a spread, a computed key, a call to a
// non-dispatcher function, a genuinely dynamic source, a getter/setter) or
// that classifies to a value we cannot statically enumerate this way aborts
// the whole proof and falls through to the ordinary unsupported path — this
// never trusts a partial/best-effort subset of branches.
//
// resolveDispatcherMember requires, mirroring
// ts-colors.mjs::absenceValue/absenceBindingUsesSafe:
//   - the binding is `const` (never `let`/`var`/a parameter);
//   - the binding is declared inside a function, and every reassignment,
//     member write, Object.assign, delete, spread-into or other escape of
//     that binding ANYWHERE IN THE ENCLOSING FUNCTION (found the same way
//     directForwardedParameter finds its owner — not the whole source
//     file, and not stopping at nested closures the way isReassignedWithin
//     does, since a mutation inside a nested callback is exactly as real as
//     one at the top of the function) is treated as unprovable;
//   - ABSENCE additionally requires every absent-classified branch's object
//     literal to carry an explicit `__proto__: null` — an ordinary literal
//     can inherit the property from prototype mutations elsewhere, so
//     absence without a null prototype would assume a pure module graph,
//     which the contract forbids. PRESENCE values do not need this: an own
//     data property always shadows the prototype on a plain read.
export function resolveDispatcherMember(expr, ctx) {
  if (!ts.isPropertyAccessExpression(expr)) return null
  const propertyName = staticPropertyName(expr.name)
  if (propertyName === null || ['__proto__', 'prototype', 'constructor'].includes(propertyName)) return null
  const target = unwrap(expr.expression)
  if (!target) return null
  let source = target
  if (ts.isIdentifier(target)) {
    const binding = findLexicalBinding(target, target.text)
    if (!binding || !binding.initializer || !ts.isVariableDeclaration(binding.declaration) || !isConstVariableDeclaration(binding.declaration)) return null
    let owner = binding.declaration.parent
    while (owner && !ts.isFunctionLike(owner)) owner = owner.parent
    if (!owner || !dispatcherBindingUsesSafe(owner, binding.declaration.name, target.text)) return null
    source = binding.initializer
  } else if (!ts.isCallExpression(target) && !ts.isConditionalExpression(target)) {
    return null
  }
  const objects = collectDispatchedObjectLiterals(source, ctx, new Set())
  if (!objects || objects.length === 0) return null
  const values = []
  for (const obj of objects) {
    const classified = classifyDispatcherProperty(obj, propertyName)
    if (classified.kind === 'unknown') return null
    if (classified.kind === 'present') { values.push(classified.initializer); continue }
    // classified.kind === 'absent': only provable without a null prototype
    // to guard the literal against an injected property (see LEAD DECISION).
    if (!hasNullPrototypeLiteral(obj)) return null
  }
  return { allAbsent: values.length === 0, values }
}

// Classifies how `name` behaves in one candidate object-literal branch:
// 'absent' (provably omitted — safe to treat as undefined and contribute
// nothing), 'present' (an ordinary PropertyAssignment or shorthand with a
// concrete initializer node), or 'unknown' (a spread, method, getter/setter,
// or computed key that could shadow the name — this branch can never
// contribute to either proof, so the caller must abort entirely).
export function classifyDispatcherProperty(obj, name) {
  let initializer = null
  for (const prop of obj.properties) {
    if (ts.isShorthandPropertyAssignment(prop)) {
      if (prop.name.text === name) initializer = prop.name
      continue
    }
    if (!ts.isPropertyAssignment(prop)) return { kind: 'unknown' }
    const key = staticPropertyName(prop.name)
    if (key === null) return { kind: 'unknown' }
    if (key === name) initializer = prop.initializer
  }
  return initializer ? { kind: 'present', initializer } : { kind: 'absent' }
}

// True only when every declared `__proto__` property on this literal is an
// explicit `null` initializer, and at least one such property exists.
// Mirrors ts-colors.mjs::absenceValue's nullPrototype computation.
export function hasNullPrototypeLiteral(obj) {
  const protoProps = obj.properties.filter((prop) => ts.isPropertyAssignment(prop)
    && !ts.isComputedPropertyName(prop.name) && staticPropertyName(prop.name) === '__proto__')
  return protoProps.length > 0 && protoProps.every((prop) => unwrap(prop.initializer)?.kind === ts.SyntaxKind.NullKeyword)
}

// A dispatcher-result binding may only be read through a property or
// element access; any other use of the identifier anywhere in `owner`
// (reassignment, a property/element write including compound assignment,
// `delete`, `++`/`--`, being passed as a call argument such as
// `Object.assign(cfg, ...)`, being spread, or being aliased via another
// declaration such as `const x = cfg`) is treated as an escape and fails
// the proof. Deliberately does NOT stop descending at nested function-like
// nodes (unlike isReassignedWithin) — a mutation performed inside a nested
// callback closed over the binding is exactly as real as one written
// directly in the enclosing function body. Mirrors, and is scoped like,
// ts-colors.mjs::absenceBindingUsesSafe(declaration, false).
export function dispatcherBindingUsesSafe(owner, declarationName, name) {
  if (!owner.body) return false
  let safe = true
  const visit = (node) => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === name && node !== declarationName) {
      const parent = node.parent
      // Ported from ts-colors.mjs::absenceBindingUsesSafe's own destructuring
      // exemption (CAP-E2, colour pass-2): `const { Icon } = cfg` only ever
      // READS cfg's own properties out into fresh bindings -- destructuring
      // can never mutate cfg or hand out a live reference back into it, so it
      // is exactly as safe as a `.member` read, not an escape. Without this,
      // one unrelated sibling property consumed by destructuring (`Icon`,
      // never itself a className) blanket-blocked every OTHER property drawn
      // from the SAME dispatcher-result binding (`config.accentClass`,
      // `config.pulse`). Scoped to a simple, non-rest, non-default pattern —
      // mirrors destructuredPatternUsesSafe, but does not need to recurse
      // into how each extracted local is later used: unlike a `.member`
      // alias (which still holds a path back to cfg), a destructured local
      // is a copied value with no path back to the receiver at all.
      if (ts.isVariableDeclaration(parent) && parent.initializer === node && destructuredBindingPatternUsesSafe(parent.name)) return
      if ((!ts.isPropertyAccessExpression(parent) && !ts.isElementAccessExpression(parent)) || parent.expression !== node) { safe = false; return }
      const property = ts.isPropertyAccessExpression(parent) ? parent.name.text : null
      if (!property || ['__proto__', 'prototype', 'constructor'].includes(property)) { safe = false; return }
      let expression = parent
      while (expression.parent && (
        ts.isPropertyAccessExpression(expression.parent) || ts.isElementAccessExpression(expression.parent)
        || ts.isParenthesizedExpression(expression.parent) || ts.isAsExpression(expression.parent)
        || ts.isNonNullExpression(expression.parent) || ts.isObjectLiteralExpression(expression.parent)
        || ts.isArrayLiteralExpression(expression.parent) || ts.isPropertyAssignment(expression.parent)
        || ts.isSpreadAssignment(expression.parent) || ts.isSpreadElement(expression.parent)
      )) expression = expression.parent
      const use = expression.parent
      if ((ts.isBinaryExpression(use) && use.left === expression
          && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
        || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
        || ts.isDeleteExpression(use) || ts.isPrefixUnaryExpression(use) || ts.isPostfixUnaryExpression(use)
        || (ts.isCallExpression(use) && use.expression === expression)) { safe = false; return }
    }
    ts.forEachChild(node, visit)
  }
  visit(owner.body)
  return safe
}

// Ported from ts-colors.mjs::destructuredPatternUsesSafe (CAP-E2). Only a
// simple (non-rest, non-default, non-nested, unrenamed-or-renamed) object
// binding pattern qualifies — a rest element could capture an unbounded,
// unclassified slice of the receiver, a default value introduces its own
// (unrelated) expression to prove, and a nested pattern is out of scope for
// this narrow exemption.
export function destructuredBindingPatternUsesSafe(pattern) {
  if (!ts.isObjectBindingPattern(pattern)) return false
  return pattern.elements.every((element) => ts.isBindingElement(element)
    && !element.dotDotDotToken && !element.initializer && ts.isIdentifier(element.name))
}

// Resolves `node` to the closed set of object literals it could evaluate to,
// following conditional (ternary) chains and calls into finite-dispatcher
// functions. Returns null the instant any branch cannot be classified this
// way — the caller (isProvenAbsentDispatcherMember) requires every branch to
// be accounted for, never a partial/best-effort subset.
export function collectDispatchedObjectLiterals(node, ctx, seen) {
  const expr = unwrap(node)
  if (!expr) return null
  if (ts.isObjectLiteralExpression(expr)) return [expr]
  if (ts.isConditionalExpression(expr)) {
    const whenTrue = collectDispatchedObjectLiterals(expr.whenTrue, ctx, seen)
    if (!whenTrue) return null
    const whenFalse = collectDispatchedObjectLiterals(expr.whenFalse, ctx, seen)
    if (!whenFalse) return null
    return [...whenTrue, ...whenFalse]
  }
  if (ts.isCallExpression(expr)) return collectFiniteDispatcherReturns(expr, ctx, seen)
  return null
}

// A "finite dispatcher" function: a block body of zero or more plain
// variable statements followed by exactly one switch statement as the final
// statement, every case/default clause containing exactly one return
// statement. Each clause's returned expression is resolved the same way
// (ternary chains, nested dispatcher calls), so a switch clause that itself
// delegates to another finite dispatcher is supported too. `seen` guards
// against a genuine call cycle (A dispatches into B which dispatches back
// into A) without penalizing the same function being called from two
// independent sibling branches — each recursive descent gets its own copy
// rather than mutating a shared set, so siblings never see each other's
// visited functions. Per LEAD DECISION, the function itself must be a safe,
// non-async, non-generator, top-level declaration (see
// isTopLevelFiniteDispatcherFunction) — an async/generator call does not
// return the object at all (a Promise/Generator instead), and a dispatcher
// nested inside the very component being analyzed could close over local
// mutable state the proof cannot see.
export function collectFiniteDispatcherReturns(call, ctx, seen) {
  const declaration = dispatcherFunctionDeclarationFor(call.expression, ctx)
  if (!declaration || !declaration.body || !ts.isBlock(declaration.body)) return null
  if (!isTopLevelFiniteDispatcherFunction(declaration)) return null
  if (seen.has(declaration)) return null
  const nextSeen = new Set(seen)
  nextSeen.add(declaration)
  const statements = declaration.body.statements
  const last = statements[statements.length - 1]
  if (!last || !ts.isSwitchStatement(last)) return null
  for (let i = 0; i < statements.length - 1; i += 1) {
    if (!ts.isVariableStatement(statements[i])) return null
  }
  const results = []
  for (const clause of last.caseBlock.clauses) {
    const returned = clauseReturnExpression(clause)
    if (returned === null) return null
    if (returned === CLAUSE_NEVER_RETURNS) continue // e.g. an exhaustiveness-guard `default: throw ...` — contributes no value
    const branch = collectDispatchedObjectLiterals(returned, ctx, nextSeen)
    if (!branch) return null
    results.push(...branch)
  }
  if (results.length === 0) return null
  return results
}

// Safe: not a generator, not async (both return a wrapper object, not the
// literal, at the call site). Top-level: a plain `function` declaration or a
// `const`-bound arrow/function-expression, either way declared directly at
// module scope — never nested inside another function, where it could close
// over caller-local mutable state this proof does not (and cannot cheaply)
// account for.
export function isTopLevelFiniteDispatcherFunction(declaration) {
  if (declaration.asteriskToken || declaration.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword)) return false
  if (ts.isFunctionDeclaration(declaration)) return ts.isSourceFile(declaration.parent)
  if (ts.isArrowFunction(declaration) || ts.isFunctionExpression(declaration)) {
    const variable = declaration.parent
    return ts.isVariableDeclaration(variable) && isConstVariableDeclaration(variable)
      && ts.isVariableDeclarationList(variable.parent) && ts.isVariableStatement(variable.parent.parent)
      && ts.isSourceFile(variable.parent.parent.parent)
  }
  return false
}

// Item 5: a plain top-level `function name(...) { if (a) { return {...} } if
// (b) { return {...} } ... return {...} }` if-chain dispatcher -- the style-
// helper counterpart of collectFiniteDispatcherReturns's own switch-based
// proof above (MessageItem.tsx's avatarStyle: `if (isUser) { return {...} }
// if (agentColor) { return {...} } return {...}`). Reuses
// dispatcherFunctionDeclarationFor/isTopLevelFiniteDispatcherFunction
// unchanged -- same callee-binding-safety and top-level/non-async/non-
// generator discipline, only the recognized BODY SHAPE differs. Deliberately
// narrower than a general control-flow prover: every statement but the last
// must be a single-statement `if (<cond>) { return <expr> }` block with no
// `else` and no further nesting, and the LAST statement must itself be a bare
// `return <expr>`. Any other statement shape (an assignment, a loop, an
// else-branch, a multi-statement if-body, a condition with side effects) is
// not provably exhaustive this way and returns null -- callers fall through
// to the ordinary unsupported path, never a silent pass.
export function collectFiniteIfChainReturns(call, ctx) {
  const declaration = dispatcherFunctionDeclarationFor(call.expression, ctx)
  if (!declaration || !declaration.body || !ts.isBlock(declaration.body)) return null
  if (!isTopLevelFiniteDispatcherFunction(declaration)) return null
  const statements = declaration.body.statements
  if (statements.length === 0) return null
  const results = []
  for (let i = 0; i < statements.length - 1; i += 1) {
    const statement = statements[i]
    if (!ts.isIfStatement(statement) || statement.elseStatement) return null
    const body = statement.thenStatement
    if (!ts.isBlock(body) || body.statements.length !== 1) return null
    const inner = body.statements[0]
    if (!ts.isReturnStatement(inner) || !inner.expression) return null
    results.push(inner.expression)
  }
  const last = statements[statements.length - 1]
  if (!ts.isReturnStatement(last) || !last.expression) return null
  results.push(last.expression)
  return results
}

// --- Dispatcher-FUNCTION binding safety: guard parity with
// ts-colors.mjs::absenceFactory. Everything above this point proves the
// shape of the switch statement; everything below proves the CALLEE NAME
// used to reach it can be trusted at all — a `function` declaration
// creates a mutable, reassignable binding (unlike a `const` arrow/
// function-expression), so `getConfig = altGetConfig` anywhere in the
// module can make a callee-name-only proof resolve against the ORIGINAL,
// never-executed declaration while the REAL, reassigned function returns a
// different result. Deliberately kept separate from functionDeclarationFor
// (shared with the unrelated style-helper proof, pureFunctionReturns) so
// this stricter proof can never change that consumer's behavior.

export function dispatcherFunctionBindingHasName(name, text) {
  if (ts.isIdentifier(name)) return name.text === text
  return (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name))
    && name.elements.some((element) => ts.isBindingElement(element) && dispatcherFunctionBindingHasName(element.name, text))
}

// Sentinel: more than one declaration for the callee's name was found in a
// single lexical scope — an AST-level duplicate the type checker would
// reject, but not something a pure-parse proof may silently resolve via a
// "first match wins" walk (a stray second declaration must never smuggle a
// different function behind a name the switch-shape proof already trusts).
export const DISPATCHER_FUNCTION_AMBIGUOUS = Symbol('dispatcher function binding ambiguous')

// Mirrors ts-colors.mjs::absenceLexicalBinding: resolves the nearest lexical
// declaration for `identifier`'s name without falling through a nearer
// opaque scope (module/class/for-loop bodies, or a same-named function
// parameter/catch binding, are deliberately outside this proof). A
// CaseBlock is handled separately, just below the loop's own disqualifying
// checks, rather than as a blanket wall like the reference: spacing.mjs's
// finite-dispatcher feature explicitly supports one switch-case RETURNING a
// call to another dispatcher (collectFiniteDispatcherReturns's own nested-
// dispatch support, and clauseReturnExpression's bare-leading-statement
// shape) — a callee identifier living inside a case clause's return
// statement is completely ordinary here, not opaque, so treating CaseBlock
// as an unconditional wall (as the reference does, since ts-colors.mjs has
// no switch-based dispatcher-calls-dispatcher shape at all) would wrongly
// reject the "follows one dispatcher delegating to another" capability.
// Only a BARE (non-block-wrapped) declaration sharing the name directly
// under a sibling case clause is genuinely hazardous — switch clauses
// without their own `{ }` share one lexical scope, so such a declaration
// could shadow the real target — and that alone still disqualifies.
export function dispatcherFunctionLexicalBinding(identifier) {
  const name = identifier.text
  for (let scope = identifier.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope)) {
      for (const clause of scope.clauses) {
        for (const statement of clause.statements) {
          if (ts.isVariableStatement(statement)) {
            for (const declaration of statement.declarationList.declarations) {
              if (dispatcherFunctionBindingHasName(declaration.name, name)) return null
            }
          } else if (statement.name && ts.isIdentifier(statement.name) && statement.name.text === name) return null
        }
      }
      continue
    }
    if (ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (ts.isFunctionLike(scope) && scope.parameters.some((parameter) => dispatcherFunctionBindingHasName(parameter.name, name))) return null
    if (ts.isCatchClause(scope) && scope.variableDeclaration && dispatcherFunctionBindingHasName(scope.variableDeclaration.name, name)) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (ts.isVariableStatement(statement)) {
        for (const declaration of statement.declarationList.declarations) {
          if (dispatcherFunctionBindingHasName(declaration.name, name)) matches.push(declaration)
        }
      } else if (statement.name && ts.isIdentifier(statement.name) && statement.name.text === name) matches.push(statement)
      else if (ts.isImportDeclaration(statement)) {
        const clause = statement.importClause
        if (clause?.name?.text === name) matches.push(clause)
        const bindings = clause?.namedBindings
        if (bindings && ts.isNamedImports(bindings)) {
          for (const element of bindings.elements) if (element.name.text === name) matches.push(element)
        } else if (bindings?.name?.text === name) matches.push(bindings)
      }
    }
    if (matches.length) return matches.length === 1 ? matches[0] : DISPATCHER_FUNCTION_AMBIGUOUS
  }
  return null
}

// Mirrors ts-colors.mjs::absenceBindingUsesSafe(declaration, true) (factory
// mode): the dispatcher FUNCTION's own binding may only ever appear as the
// callee of a call expression (`name(...)`) anywhere it is lexically
// visible in `declaration`'s enclosing block/module scope. Any other
// occurrence — reassignment, a bare reference, a property read/write on the
// function object, being passed as a value, aliased into another binding,
// spread, etc. — fails the proof. Distinct from dispatcherBindingUsesSafe
// above, which guards the dispatcher-RESULT binding (`cfg`) — this guards
// the dispatcher FUNCTION's own name.
export function dispatcherFunctionFactoryUsesSafe(declaration) {
  if (!declaration.name || !ts.isIdentifier(declaration.name)) return false
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  const visit = (node) => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
      const parent = node.parent
      if (ts.isImportSpecifier(parent) && parent === declaration) return
      if (!ts.isCallExpression(parent) || parent.expression !== node) { safe = false; return }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return safe
}

// Resolves a finite-dispatcher call's callee to its own declaration under
// the same proof strength ts-colors.mjs::absenceFactory demands of a colour
// dispatcher's callee: an unambiguous lexical resolution
// (dispatcherFunctionLexicalBinding), the resolved binding's own name used
// ONLY as `name(...)` anywhere it is visible (dispatcherFunctionFactoryUsesSafe),
// and — for an imported dispatcher — the SAME two proofs re-applied to the EXPORTING
// module's own top-level declaration (the local import specifier is checked
// first via the shared pre-check above, the module is then loaded and its
// export re-checked import-side), gated on the module parsing clean, on
// exactly one matching top-level function declaration existing in it, and on
// that declaration actually carrying an `export` modifier.
export function dispatcherFunctionDeclarationFor(node, ctx) {
  const callee = unwrap(node)
  if (!callee || !ts.isIdentifier(callee)) return null
  let declaration = dispatcherFunctionLexicalBinding(callee)
  if (!declaration || declaration === DISPATCHER_FUNCTION_AMBIGUOUS) return null
  if (!dispatcherFunctionFactoryUsesSafe(declaration)) return null
  if (ts.isImportSpecifier(declaration)) {
    const statement = declaration.parent.parent.parent
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) return null
    const modulePath = governedModulePath(callee.getSourceFile().fileName, statement.moduleSpecifier.text, ctx.modules)
    const record = modulePath && moduleRecord(ctx, modulePath)
    if (!record || (record.sourceFile.parseDiagnostics ?? []).length) return null
    const imported = declaration.propertyName?.text ?? declaration.name.text
    const matches = record.sourceFile.statements.filter((statement) => ts.isFunctionDeclaration(statement) && statement.name?.text === imported)
    if (matches.length !== 1) return null
    declaration = matches[0]
    if (!declaration.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)
      || !dispatcherFunctionFactoryUsesSafe(declaration)) return null
    return declaration
  }
  if (ts.isFunctionDeclaration(declaration)) return ts.isSourceFile(declaration.parent) ? declaration : null
  if (ts.isVariableDeclaration(declaration)) {
    if (!isConstVariableDeclaration(declaration) || !ts.isVariableDeclarationList(declaration.parent)
      || !ts.isVariableStatement(declaration.parent.parent) || !ts.isSourceFile(declaration.parent.parent.parent)) return null
    const initializer = unwrap(declaration.initializer)
    if (initializer && (ts.isArrowFunction(initializer) || ts.isFunctionExpression(initializer))) return initializer
  }
  return null
}

// Sentinel: a clause that provably never returns a value (an
// exhaustiveness-guard `default: throw new Error(...)`) is not a value
// source at all, so it is skipped rather than aborting the whole dispatcher
// proof — distinct from `null`, which means "this shape is not recognized,
// abort".
export const CLAUSE_NEVER_RETURNS = Symbol('clause never returns a value')

// A case/default clause's statement list, or — for the common exhaustiveness-
// guard shape (`default: { const _x: never = status; return {...} }` or
// `default: { const _x: never = status; throw new Error(...) }`) — a single
// wrapping block's statement list. Only VariableStatement and
// ExpressionStatement are allowed before the final return/throw (matches the
// `const _exhaustive: never = status; void _exhaustive` pattern); anything
// else (loops, nested control flow) is not this shape and aborts.
export function clauseReturnExpression(clause) {
  let statements = clause.statements
  if (statements.length === 1 && ts.isBlock(statements[0])) statements = statements[0].statements
  const last = statements[statements.length - 1]
  if (!last) return null
  const leadingStatementsAreSafe = () => {
    for (let i = 0; i < statements.length - 1; i += 1) {
      if (!ts.isVariableStatement(statements[i]) && !ts.isExpressionStatement(statements[i])) return false
    }
    return true
  }
  if (ts.isThrowStatement(last)) return leadingStatementsAreSafe() ? CLAUSE_NEVER_RETURNS : null
  if (!ts.isReturnStatement(last) || !last.expression) return null
  return leadingStatementsAreSafe() ? last.expression : null
}

// --- Capability port: dynamic-key record fan-out + ??/|| record-value
// chaining. Purely additive alongside resolveDispatcherMember/
// collectDispatchedObjectLiterals/dispatcherBindingUsesSafe above -- none of
// those three is modified by anything below, so every existing switch/
// ternary/IIFE-callee dispatcher test keeps exercising exactly the code it
// always has. Ported capabilities:
//   - ts-colors.mjs::resolveMemberTargets's ElementAccessExpression branch:
//     a non-literal (dynamic) key against a directly-resolvable record
//     cannot be narrowed to one property, so every one of the record's own
//     property values becomes a candidate branch -- the P7 triage's named
//     capability, closing the recurring `RECORD[dynamicKey]` shape
//     (STATUS_BADGE[run.status], MODE_CHIP_CLASS[m], TONE_ICON_CLASS[copy.
//     tone], sizeClasses[size], sideVariants[side], PRIORITY_CLASS[priority],
//     PRIORITY_CONFIG[p], ...).
//   - ts-colors.mjs::resolveToObjects's BinaryExpression (??/||) branch:
//     either operand could be the live record-shaped value, so both
//     contribute -- closes the recurring `const badge = PRIORITY_BADGE[a] ??
//     PRIORITY_BADGE[b]` shape (ListView.tsx/TaskCard.tsx/
//     CreateTaskSlideOver.tsx/TaskRunsList.tsx/TaskRunStatusField.tsx).
// Neither ported capability is a wholesale rewrite of resolveExpr or the
// dispatcher machinery: this is a SEPARATE, additive traversal that only
// ever augments what those already prove, reusing their exact building
// blocks (classifyDispatcherProperty, hasNullPrototypeLiteral,
// dispatcherBindingUsesSafe, resolveExpr, isPrimitiveLeafNode) rather than
// re-deriving any escape proof of its own.
//
// Escape safety (two tiers, matching whichever of this file's two existing
// record-safety guards actually applies to the binding in question):
//   - a MODULE-level (SourceFile-scoped) record identifier defers entirely
//     to resolveExpr's own identifier branch -- round 4's recordBindingEscapes,
//     the stricter, whole-module/whole-import-chain escape walk this file
//     already uses for every other record read at that scope. Nothing here
//     re-implements or loosens that guard; it is only ever consulted.
//   - a FUNCTION-scoped alias bound to this chain's own result (`const badge
//     = PRIORITY_BADGE[a] ?? PRIORITY_BADGE[b]`) is safe unconditionally
//     when every one of its resolved candidates is a primitive leaf (a
//     `const` primitive binding cannot itself carry a mutable reference back
//     into the source record, however it is later read); when a candidate is
//     still object-shaped (e.g. PRIORITY_BADGE's own `{ label, className }`
//     branch, still reachable and mutable), the alias's own later uses are
//     proven exactly like any other dispatcher-result binding, via
//     dispatcherBindingUsesSafe (round 3), reused unmodified.
// Returns null (never []) the instant any branch cannot be classified --
// never a partial/best-effort subset, matching every other finite-branch
// proof in this file.
// Item 3 (lead-assigned wave-3 follow-up): a candidate set containing a
// PROVEN null/undefined branch (`cond ? {...} : null`, `base ?? undefined`)
// must not poison the WHOLE union the way an unclassifiable receiver
// correctly does elsewhere in this proof -- a null/undefined branch is a
// REAL, valid outcome of a conditional/nullish source, and a member read
// that is itself optional-chained (`x?.prop`) already handles that outcome
// at the read site: reading THROUGH `?.` can never actually reach the null
// branch at runtime, so it contributes nothing to the read's own resolved
// value set. An UNGUARDED read of the same chain (`x.prop`, no `?.`) could
// actually throw or read off `null`/`undefined` at runtime for that branch
// -- unprovable, stays unsupported exactly as before.
//
// Scoped to `?.` only, NOT the `x && x.prop` truthiness-guard form the task
// also names: dispatcherBindingUsesSafe (this file's escape-safety proof for
// a function-scoped dispatcher binding, immediately below in the call chain)
// unconditionally treats ANY bare, non-`.member`-immediately-following
// occurrence of the binding as an escape -- by design, for the general
// mutation-safety proof, not specifically about null handling -- and a bare
// `cfg` as the left operand of `&&` is EXACTLY such an occurrence. Every
// `x && x.prop` fixture tried during this port hit that gate first and
// stayed unsupported for an UNRELATED, pre-existing reason, never reaching
// this null-candidate logic at all. Teaching dispatcherBindingUsesSafe a
// truthiness-guard exemption (paralleling its existing destructuring
// exemption) is a real, separate capability with its own soundness argument
// (it would need to correlate the `&&` LEFT operand with the SAME binding's
// `.member` access on the RIGHT, not just recognize any bare `&&` operand)
// -- shipping it unverified here would be unproven complexity, not a proven
// fix, so it is deliberately left out of this pass.
export const NULL_CANDIDATE = Symbol('spacing-null-candidate')

export function isNullOrUndefinedLiteral(expr) {
  if (expr.kind === ts.SyntaxKind.NullKeyword) return true
  return ts.isIdentifier(expr) && expr.text === 'undefined'
}

export function memberAccessIsNullGuarded(expr) {
  return Boolean(expr.questionDotToken)
}

export function resolveRecordChain(node, ctx, seen) {
  const expr = unwrap(node)
  if (!expr) return null
  if (isNullOrUndefinedLiteral(expr)) return [NULL_CANDIDATE]
  if (ts.isObjectLiteralExpression(expr)) return [expr]
  if (ts.isConditionalExpression(expr)) {
    const whenTrue = resolveRecordChain(expr.whenTrue, ctx, seen)
    if (!whenTrue) return null
    const whenFalse = resolveRecordChain(expr.whenFalse, ctx, seen)
    if (!whenFalse) return null
    return [...whenTrue, ...whenFalse]
  }
  if (ts.isBinaryExpression(expr) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(expr.operatorToken.kind)) {
    const left = resolveRecordChain(expr.left, ctx, seen)
    if (!left) return null
    const right = resolveRecordChain(expr.right, ctx, seen)
    if (!right) return null
    return [...left, ...right]
  }
  if (ts.isCallExpression(expr)) return collectFiniteDispatcherReturns(expr, ctx, seen)
  if (ts.isIdentifier(expr)) return resolveRecordChainIdentifier(expr, ctx, seen)
  if (ts.isElementAccessExpression(expr) || ts.isPropertyAccessExpression(expr)) {
    const propertyName = ts.isPropertyAccessExpression(expr) ? staticPropertyName(expr.name) : null
    if (propertyName !== null && ['__proto__', 'prototype', 'constructor'].includes(propertyName)) return null
    const keyExpr = ts.isElementAccessExpression(expr) ? unwrap(expr.argumentExpression) : null
    const staticKey = propertyName ?? ((keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr) || ts.isNumericLiteral(keyExpr))) ? keyExpr.text : null)
    const receivers = resolveRecordChain(expr.expression, ctx, seen)
    if (!receivers) return null
    const nullGuarded = memberAccessIsNullGuarded(expr)
    const results = []
    for (const receiver of receivers) {
      if (receiver === NULL_CANDIDATE) {
        if (!nullGuarded) return null
        continue // guarded (?. or `x && x.prop`): the null branch is never actually read, contributes nothing
      }
      if (!ts.isObjectLiteralExpression(receiver)) return null
      if (staticKey !== null) {
        const classified = classifyDispatcherProperty(receiver, staticKey)
        if (classified.kind === 'unknown') return null
        if (classified.kind === 'present') { results.push(classified.initializer); continue }
        if (!hasNullPrototypeLiteral(receiver)) return null
        continue
      }
      // Dynamic key: fan out over every enumerable branch -- the ported
      // capability itself. A spread/method/getter/computed-key branch could
      // shadow an arbitrary key, so it aborts the whole proof rather than
      // being silently skipped.
      for (const prop of receiver.properties) {
        if (ts.isShorthandPropertyAssignment(prop)) { results.push(prop.name); continue }
        if (!ts.isPropertyAssignment(prop) || ts.isComputedPropertyName(prop.name)) return null
        results.push(prop.initializer)
      }
    }
    return results.length ? results : null
  }
  return null
}

export function resolveRecordChainIdentifier(identifier, ctx, seen) {
  const binding = findLexicalBinding(identifier, identifier.text)
  if (binding && binding.initializer && ts.isVariableDeclaration(binding.declaration) && isConstVariableDeclaration(binding.declaration)) {
    if (seen.has(binding.declaration)) return null // cycle guard, keyed on the declaration (not the occurrence) — see resolveRecordChain's own header comment
    let owner = binding.declaration.parent
    while (owner && !ts.isFunctionLike(owner)) owner = owner.parent
    if (owner) {
      const next = new Set(seen); next.add(binding.declaration)
      const candidates = resolveRecordChain(binding.initializer, ctx, next)
      if (!candidates) return null
      if (candidates.every((value) => isPrimitiveLeafNode(value))) return candidates
      if (!dispatcherBindingUsesSafe(owner, binding.declaration.name, identifier.text)) return null
      return candidates
    }
  }
  // Module-level (SourceFile-scoped) or cross-module record: defer entirely
  // to resolveExpr's own identifier branch (round 4's recordBindingEscapes).
  const resolved = resolveExpr(identifier, ctx, new Set())
  if (resolved === UNRESOLVED_EXPR || !resolved || resolved === identifier) return null
  if (ts.isObjectLiteralExpression(resolved)) return [resolved]
  return null
}

// Top-level entry point, wired alongside resolveDispatcherMember in
// visitClassBuilderArg: `expr` may be a bare identifier (`badgeClass`, once
// its own chain resolves to nothing but primitive leaves), or a `.prop`/
// `[key]` access on top of one (`badge.className`, `TONE_ICON_CLASS[copy.
// tone]`, `PRIORITY_CONFIG[p]?.color`).
export function resolveRecordChainMember(expr, ctx) {
  const values = resolveRecordChain(expr, ctx, new Set())
  if (!values || values.length === 0) return null
  // NULL_CANDIDATE (item 3) is only ever a meaningful INTERMEDIATE value,
  // consumed and filtered by the ElementAccess/PropertyAccess branch's own
  // guarded-member-read loop before it can reach this top level -- the only
  // way one survives to here is `expr` itself resolving bare to null/
  // undefined (or a conditional/`??` union of ONLY null/undefined) with no
  // subsequent member access ever narrowing it. That is not a class-builder
  // value to recurse into at all; every real AST node in `values` still
  // gets its own visitClassBuilderArg(value.getSourceFile(), ...) call right
  // after this returns, which would crash on the bare Symbol otherwise.
  if (values.includes(NULL_CANDIDATE)) return null
  return { allAbsent: false, values }
}

// SP-RECOVER completion (not present in the lost session's own log — its
// last probe of this exact shape errored on a concurrent `npm ci` corrupting
// node_modules before it ever got a real result back; see the recovery
// evidence dir for the transcript). Neither resolveRecordChain's own
// dynamic-key fan-out nor recordBindingEscapes/dynamicTailEmbedIsSafe (both
// faithfully recovered from the session log) push a finding themselves --
// they only ever return null/UNRESOLVED_EXPR and let visitClassBuilderArg's
// generic UNRESOLVED_EXPR fallback report `expr.getText()`, i.e. the bare
// `sizeClasses[size]` fragment. The orphaned tests in this port's own diff
// (spacing.test.mjs's "dynamic-key record read passed as a bare
// class-builder argument" describe block) instead expect the finding
// attributed to the WHOLE class-builder call
// (`cn('base', sizeClasses[size])`), mirroring the one PRE-EXISTING
// precedent in this file for call-level attribution (the reassigned
// call-expression alias case, "still drills into a reassigned `let`
// call-expression alias..." in spacing-adversarial.test.mjs, which resolves
// THROUGH to `cn(...)`'s own text via recursion). Scoped narrowly to avoid
// touching the many EXISTING passing tests that embed a *pure* property
// chain with no dynamic element access directly in a class-builder call and
// expect the bare chain's own text (`config.textClass`, not the call) --
// chainHasDynamicElementAccess requires an actual non-literal `[key]` hop
// somewhere in the chain, which none of those cases have.
export function chainHasDynamicElementAccess(node) {
  let current = unwrap(node)
  while (current) {
    if (ts.isPropertyAccessExpression(current)) { current = unwrap(current.expression); continue }
    if (ts.isElementAccessExpression(current)) {
      const keyExpr = unwrap(current.argumentExpression)
      const staticKey = keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr) || ts.isNumericLiteral(keyExpr))
      if (!staticKey) return true
      current = unwrap(current.expression)
      continue
    }
    return false
  }
  return false
}

export function directClassBuilderCallEmbed(node, ctx) {
  const parent = node.parent
  if (!parent || !ts.isCallExpression(parent) || !parent.arguments.includes(node)) return null
  // FIX-P1 finding 1: same collision-gated bare-name rule as visitNode/
  // visitClassLike (see visitNode's own header comment).
  const name = calleeName(parent.expression)
  const sourceFile = parent.getSourceFile()
  if (!(CLASS_BUILDERS.has(name) && !hasLocalNameCollision(sourceFile, name)) && !guardClassBuilder(parent.expression, ctx)) return null
  return parent
}

// --- Capability port: destructured-member resolution through a provably-
// finite dispatcher call (ts-colors.mjs's CAP-A: "destructured-member
// resolution through a provably-finite object source", e.g. `const { color }
// = fileTypeMeta(...)`). `resolveDispatcherMember` above only fires for a
// PropertyAccessExpression (`x.prop`); `const { className } =
// describeNonActiveState(status)` never reaches it at all, because the READ
// SITE is the bare local `className`, not a further `.prop` access — the
// destructuring already performed the member extraction at the DECLARATION.
// `destructuredDispatcherElement` finds that declaration (mirroring
// findLexicalBinding's own nearest-enclosing-block walk, restricted to a
// simple — non-rest, non-default, non-nested — object-binding element) and
// `resolveDestructuredDispatcherMember` classifies the extracted property
// across the SAME finite-dispatcher call resolution
// (collectDispatchedObjectLiterals, reused unmodified) resolveDispatcherMember
// already uses for the property-access shape. No additional escape guard is
// needed for the destructured local itself: unlike a `.member` alias (which
// still holds a live path back to the receiver), destructuring COPIES a
// value out — there is no path back into the dispatcher's return object for
// anything done to the local afterward, and the local's own binding is
// `const` (enforced below), so it cannot be reassigned either.
export function destructuredDispatcherElement(identifier) {
  const name = identifier.text
  for (let scope = identifier.parent; scope; scope = scope.parent) {
    if (ts.isFunctionLike(scope) && scope.parameters.some((parameter) => findNamedBinding(parameter.name, name))) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    for (const statement of scope.statements) {
      if (!ts.isVariableStatement(statement)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (!isConstVariableDeclaration(declaration) || !declaration.initializer || !ts.isObjectBindingPattern(declaration.name)) continue
        for (const element of declaration.name.elements) {
          if (!ts.isBindingElement(element) || element.dotDotDotToken || element.initializer || !ts.isIdentifier(element.name) || element.name.text !== name) continue
          return { element, source: declaration.initializer }
        }
      }
    }
  }
  return null
}

export function resolveDestructuredDispatcherMember(expr, ctx) {
  if (!ts.isIdentifier(expr)) return null
  const found = destructuredDispatcherElement(expr)
  if (!found) return null
  const propertyName = found.element.propertyName ? staticPropertyName(found.element.propertyName) : found.element.name.text
  if (propertyName === null || ['__proto__', 'prototype', 'constructor'].includes(propertyName)) return null
  const objects = collectDispatchedObjectLiterals(found.source, ctx, new Set())
  if (!objects || objects.length === 0) return null
  const values = []
  for (const obj of objects) {
    const classified = classifyDispatcherProperty(obj, propertyName)
    if (classified.kind === 'unknown') return null
    if (classified.kind === 'present') { values.push(classified.initializer); continue }
    if (!hasNullPrototypeLiteral(obj)) return null
  }
  return { allAbsent: values.length === 0, values }
}

// Item 3a port (typography.mjs::resolveArrayCallbackPropertyAccess +
// resolveArrayLiteral + arrayElementPropertyValues): `item.prop` (or
// `item['prop']`) read off the SOLE parameter of a `.map(...)` callback whose
// receiver resolves -- after unwrapping any leading `.filter()`/`.slice()`
// (neither changes an element's own shape) -- to a finite, never-mutated
// const array literal, local or imported (TaskDetailPanel.tsx's
// `STATUS_OPTIONS.filter(...).map((o) => ({ ..., className: cn('text-xs',
// o.color) }))`). Reuses the SAME record-shaped mutation-safety proof
// (recordLiteralRoot/recordBindingEscapes/recordExportedUsesSafe, via
// resolveExpr's own identifier branch) this file already trusts for a direct
// property/element read off a record binding -- this only adds the missing
// MIDDLE step, iterating a proven-safe array's elements through a callback
// parameter, never loosens that proof. Narrow and structural, mirroring
// forwardedClassLikeBoundary's own shape/reassignment discipline: the
// identifier must be the callback's own SOLE parameter (not a same-named
// shadow reached through a nested closure -- `fn.parent` must be the `.map()`
// call itself), and the parameter must never be reassigned in the callback
// body before this read.
export function resolveArrayCallbackMember(node, ctx) {
  const key = ts.isPropertyAccessExpression(node) ? staticPropertyName(node.name)
    : (() => { const argument = node.argumentExpression && unwrap(node.argumentExpression)
      return argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument)) ? argument.text : null })()
  if (key === null || ['__proto__', 'prototype', 'constructor'].includes(key)) return null
  const base = unwrap(node.expression)
  if (!base || !ts.isIdentifier(base)) return null
  let fn = base.parent
  while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
  if (!fn || fn.parameters.length !== 1) return null
  const parameter = fn.parameters[0]
  if (!ts.isIdentifier(parameter.name) || parameter.name.text !== base.text) return null
  if (isReassignedWithin(fn, base.text)) return null
  const callExpression = fn.parent
  if (!callExpression || !ts.isCallExpression(callExpression) || callExpression.arguments[0] !== fn) return null
  if (!ts.isPropertyAccessExpression(callExpression.expression) || callExpression.expression.name.text !== 'map') return null
  const array = resolveSafeArrayLiteral(callExpression.expression.expression, ctx)
  if (!array) return null
  return arrayElementPropertyValues(array, key)
}

// Resolves `node` -- itself, or after unwrapping a leading `.filter(...)`/
// `.slice(...)` (neither changes an element's own shape, only which/how many
// survive) -- to a finite array literal, reusing resolveExpr's own identifier
// resolution (and so its record-shaped mutation-safety proof) unchanged.
// Returns null (fail closed) for anything not provably this shape.
export function resolveSafeArrayLiteral(node, ctx) {
  const unwrapped = unwrap(node)
  if (!unwrapped) return null
  if (ts.isCallExpression(unwrapped) && ts.isPropertyAccessExpression(unwrapped.expression)
    && (unwrapped.expression.name.text === 'filter' || unwrapped.expression.name.text === 'slice')) {
    return resolveSafeArrayLiteral(unwrapped.expression.expression, ctx)
  }
  if (!ts.isIdentifier(unwrapped)) return null
  const resolved = resolveExpr(unwrapped, ctx)
  if (resolved === UNRESOLVED_EXPR || !resolved) return null
  return ts.isArrayLiteralExpression(resolved) ? resolved : null
}

// Every value `key` can hold across a finite array literal's elements --
// rejecting a spread anywhere (the array itself or an element) and any
// element that is not a plain object literal, the same fail-closed
// discipline as classifyDispatcherProperty's own record leaves (reused here
// unmodified). Returns { allAbsent, values }, or null when any element
// cannot be classified -- the caller falls through to the ordinary
// unsupported path, never a silent pass.
export function arrayElementPropertyValues(arrayLiteral, key) {
  if (arrayLiteral.elements.some((element) => ts.isSpreadElement(element))) return null
  const values = []
  for (const element of arrayLiteral.elements) {
    const item = unwrap(element)
    if (!item || !ts.isObjectLiteralExpression(item) || item.properties.some((property) => ts.isSpreadAssignment(property))) return null
    const classified = classifyDispatcherProperty(item, key)
    if (classified.kind === 'unknown') return null
    if (classified.kind === 'present') { values.push(classified.initializer); continue }
    if (!hasNullPrototypeLiteral(item)) return null
  }
  return { allAbsent: values.length === 0, values }
}

export function visitClassObject(expr, ctx, sourceFile) {
  for (const prop of expr.properties) {
    if (ts.isSpreadAssignment(prop)) {
      pushFinding(ctx, RULE.unsupported, '{...}', 'Unsupported spacing expression; spread class lists cannot be verified against the D10 scale.', locFromTs(prop, sourceFile))
      continue
    }
    if (ts.isPropertyAssignment(prop) || ts.isShorthandPropertyAssignment(prop)) {
      const key = staticPropertyName(prop.name)
      if (key) analyzeClassList(key, ctx, locFromTs(prop, sourceFile))
      if (ts.isPropertyAssignment(prop)) visitClassBuilderArg(prop.initializer, ctx, sourceFile)
    }
  }
}

// Conservative whole-file proof: even an unrelated binding named undefined
// prevents treating that spelling as absent style. Never infer absence for an
// alias, and reject dynamic lexical scopes rather than assuming their contents.
export function hasUndefinedShadowOrDynamicScope(sourceFile) {
  let unsafe = false
  const bindsUndefined = (name) => {
    if (!name) return false
    if (ts.isIdentifier(name)) return name.text === 'undefined'
    if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
      return name.elements.some((element) => ts.isBindingElement(element) && bindsUndefined(element.name))
    }
    return false
  }
  const visit = (node) => {
    if (unsafe) return
    if (ts.isWithStatement(node)) { unsafe = true; return }
    if (ts.isCallExpression(node)) {
      const callee = unwrap(node.expression)
      if (callee && ts.isIdentifier(callee) && callee.text === 'eval') { unsafe = true; return }
    }
    if ((ts.isVariableDeclaration(node) || ts.isParameter(node)
      || ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node)
      || ts.isClassDeclaration(node) || ts.isClassExpression(node)
      || ts.isEnumDeclaration(node) || ts.isModuleDeclaration(node)
      || ts.isImportClause(node) || ts.isImportSpecifier(node)
      || ts.isNamespaceImport(node) || ts.isImportEqualsDeclaration(node)) && bindsUndefined(node.name)) {
      unsafe = true
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return unsafe
}

export function visitStyleLike(initializer, ctx, sourceFile) {
  if (!initializer) return
  const expr = unwrap(ts.isJsxExpression(initializer) ? initializer.expression : initializer)
  if (!expr) return
  if (ts.isIdentifier(expr) && expr.text === 'undefined') {
    if (!hasUndefinedShadowOrDynamicScope(expr.getSourceFile())) return
    pushFinding(ctx, RULE.unsupported, 'style: undefined', 'Unsupported spacing expression; shadowed undefined or dynamic lexical scope cannot prove absent style.', locFromTs(expr, sourceFile))
    return
  }
  const boundary = directForwardedParameter(expr, 'style')
  if (boundary) {
    pushFinding(ctx, RULE.extensionBoundary, `${boundary.symbol}#${boundary.name}`, 'Spacing extension boundary; caller-provided style is forwarded unchanged and requires exact central review.', locFromTs(expr, sourceFile))
    if (boundary.initializer) visitStyleLike(boundary.initializer, ctx, sourceFile)
    return
  }
  if (ts.isCallExpression(expr)) {
    const returns = pureFunctionReturns(expr, ctx)
    if (returns) {
      for (const returned of returns) visitStyleLike(returned, ctx, returned.getSourceFile())
      return
    }
    // Item 5 port (finite if-chain dispatcher, style-helper counterpart of
    // this file's own switch-based finite dispatcher): MessageItem.tsx's
    // `avatarStyle(isUser, agent?.color)`.
    const ifChainReturns = collectFiniteIfChainReturns(expr, ctx)
    if (ifChainReturns) {
      for (const returned of ifChainReturns) visitStyleLike(returned, ctx, returned.getSourceFile())
      return
    }
    pushFinding(ctx, RULE.unsupported, `style: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; dynamic or impure style helpers cannot be verified against the D10 scale.', locFromTs(expr, sourceFile))
    return
  }
  if (ts.isConditionalExpression(expr)) {
    visitStyleLike(expr.whenTrue, ctx, sourceFile)
    visitStyleLike(expr.whenFalse, ctx, sourceFile)
    return
  }
  // Item 6: a locally-declared, never-escaping style accumulator (see
  // localAssignedStyleObject below) read back whole (KbMarkdownImage.tsx's
  // `style={Object.keys(style).length > 0 ? style : undefined}`).
  if (ts.isIdentifier(expr)) {
    const assigned = localAssignedStyleObject(expr)
    if (assigned) {
      for (const { propertyName, valueExprs } of assigned) {
        if (!SPACING_PROPERTIES.has(propertyName)) continue
        for (const valueExpr of valueExprs) analyzeStyleValue(propertyName, unwrap(valueExpr), ctx, locFromTs(valueExpr, sourceFile))
      }
      return
    }
  }
  const resolved = resolveExpr(expr, ctx)
  if (resolved === UNRESOLVED_EXPR) {
    pushFinding(ctx, RULE.unsupported, `style: ${expr.getText(sourceFile)}`, 'Unsupported spacing expression; dynamic or cyclic style aliases cannot be verified against the D10 scale.', locFromTs(expr, sourceFile))
    return
  }
  const value = resolved ?? expr
  if (ts.isObjectLiteralExpression(value)) {
    visitStyleObject(value, ctx, sourceFile)
    return
  }
  if (ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)) {
    scanInlineStyle(value.text, ctx, locFromTs(value, sourceFile))
  }
}

export function visitStyleObject(obj, ctx, sourceFile) {
  for (const prop of obj.properties) {
    const loc = locFromTs(prop, sourceFile)
    if (ts.isSpreadAssignment(prop)) {
      pushFinding(ctx, RULE.unsupported, '{...}', 'Unsupported spacing expression; spread style properties cannot be verified against the D10 scale.', loc)
      continue
    }
    if (ts.isShorthandPropertyAssignment(prop)) {
      const name = staticPropertyName(prop.name)
      const propName = name ? kebab(name) : null
      if (propName && SPACING_PROPERTIES.has(propName)) {
        pushFinding(ctx, RULE.unsupported, `${propName}: {expr}`, 'Unsupported spacing expression; dynamic spacing values cannot be verified against the D10 scale.', loc)
      }
      continue
    }
    if (!ts.isPropertyAssignment(prop)) continue
    const rawName = staticPropertyName(prop.name)
    if (rawName === null) {
      pushFinding(ctx, RULE.unsupported, '[computed]', 'Unsupported spacing expression; computed style property names cannot be verified against the D10 scale.', loc)
      continue
    }
    const propName = kebab(rawName)
    if (!SPACING_PROPERTIES.has(propName)) continue
    analyzeStyleValue(propName, unwrap(prop.initializer), ctx, loc)
  }
}

// Item 6: a locally-declared, never-escaping style accumulator --
// `const NAME: Record<string, string | number> = {}` followed by a sequence
// of `NAME.prop = value` assignment statements in the SAME declaring scope
// (KbMarkdownImage.tsx's D-40/D-134 width/height box), read back whole at the
// render site through a presence check (`Object.keys(NAME).length > 0 ?
// NAME : undefined`). Deliberately narrow: the declaration's own initializer
// must be a literal EMPTY object (nothing pre-existing to merge/shadow), and
// EVERY other occurrence of the identifier anywhere in its declaring scope
// must be either a bare `NAME.prop = value` assignment or the object's own
// key list being read (read-only, transient `Object.keys/values/entries
// (NAME)`, never a value derived FROM an enumerated key) -- a compound
// assignment, `delete`, spread, reassignment of the whole binding, or being
// passed anywhere else aborts the whole proof and returns null (fail
// closed), exactly like every other escape guard in this file. This
// Object.keys/values/entries allowance is intentionally separate from, and
// does not touch or loosen, recordBindingEscapes's own refusal of that same
// call shape for a dispatcher/record VALUE lookup (ListView.tsx's
// PRIORITY_ORDER) -- a presence/key-count check never derives a further
// lookup from the enumerated keys, so it carries none of that shape's risk.
// Each property's possible values are the UNION of every assignment found (a
// later conditional branch can overwrite an earlier one at runtime;
// collecting both is the SOUND over-approximation -- it can never miss a
// value the property could actually hold, only ever consider one it can't).
export function localAssignedStyleObject(identifier) {
  const binding = findLexicalBinding(identifier, identifier.text)
  if (!binding || !ts.isVariableDeclaration(binding.declaration) || !isConstVariableDeclaration(binding.declaration)) return null
  const initializer = binding.initializer
  if (!initializer || !ts.isObjectLiteralExpression(initializer) || initializer.properties.length > 0) return null
  const scope = recordDeclarationScope(binding.declaration)
  if (!scope) return null
  const byProperty = new Map()
  let unsafe = false
  const visit = (node) => {
    if (unsafe) return
    if (ts.isIdentifier(node) && node.text === identifier.text && node !== binding.declaration.name) {
      const parent = node.parent
      // A JSX attribute's own NAME (`style={...}`'s `style`) or an object
      // property's own KEY (`{ style: ... }`'s `style`) is an unrelated
      // syntactic label that happens to share this identifier's text -- not
      // a reference to the variable at all -- and must not be treated as a
      // use, safe or otherwise.
      if ((ts.isJsxAttribute(parent) && parent.name === node)
        || (ts.isPropertyAssignment(parent) && parent.name === node && !ts.isComputedPropertyName(parent.name))
        || (ts.isBindingElement(parent) && parent.propertyName === node)) return
      if (ts.isPropertyAccessExpression(parent) && parent.expression === node
        && ts.isBinaryExpression(parent.parent) && parent.parent.left === parent
        && parent.parent.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
        const propertyName = kebab(parent.name.text)
        if (!byProperty.has(propertyName)) byProperty.set(propertyName, [])
        byProperty.get(propertyName).push(parent.parent.right)
        return
      }
      if (ts.isCallExpression(parent) && parent.arguments.length === 1 && parent.arguments[0] === node
        && ts.isPropertyAccessExpression(parent.expression) && ts.isIdentifier(parent.expression.expression)
        && parent.expression.expression.text === 'Object'
        && ['keys', 'values', 'entries'].includes(parent.expression.name.text)) return
      if (node === identifier) return // the read site itself
      unsafe = true
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  if (unsafe) return null
  const result = []
  for (const [propertyName, valueExprs] of byProperty) result.push({ propertyName, valueExprs })
  return result
}
