#!/usr/bin/env node
// ts-colors/binding-resolution.mjs
//
// Destructuring/positional-parameter binding resolution, the
// runtime-template-span and hook-state-laundering proofs, and the
// generated-token/status-contract governance checks. See
// scope-and-jsx.mjs's header for why this file cycles with its three
// siblings and why that is safe here.

'use strict'

import ts from 'typescript'
import {
  absenceBindingHasName,
  inspectClassExpr,
  inspectCssValueExpr,
  opaqueObjectMember,
  propertyNameOf,
  zodChainCalls,
} from './style-and-purity.mjs'
import {
  CANONICAL_GENERATED_TOKEN_PATH_SET,
  CLASS_BUILDERS,
  LOCAL_CLASS_COMPOSERS,
  LOOKUP_MISSING,
  LOOKUP_UNBOUND,
  PARAMETER_BINDING,
  STATUS_CONTRACT_BUILDER_NAME,
  STATUS_CONTRACT_EXPORT_NAME,
  STATUS_CONTRACT_MODULE_PATH,
  STRING_CASE_METHODS,
  STRING_TRIM_METHODS,
  bindingValue,
  functionIdentity,
  governedModulePath,
  indexModuleRecord,
  isDirectRuntimeRead,
  isFunctionLike,
  isParameterBinding,
  isProvenNonNullReceiver,
  isRuntimeMeasurementBoundary,
  lookup,
  moduleRecord,
  resolveModuleDeclaration,
  typeHasBooleanProperty,
  unwrap,
} from './scope-and-jsx.mjs'
import {
  analyzeClassString,
  analyzeCssDeclarations,
  analyzeCssText,
  analyzeCssValue,
  emitRuntimePaintBoundary,
  emitUnsupported,
  isSkipIdent,
  propertyInit,
  resolveMemberTargets,
  resolveToObjects,
} from './value-analysis.mjs'

// A `.regex(/pattern/)` argument is a RegExp literal, not a string/template
// literal — inspectLiteralColorArguments only ever reports string/no-sub-
// template-literal arguments (a regex has no way to BE a raw colour value;
// it is a pattern that validates the SHAPE of a later-supplied string), so
// running it over every call in the chain already leaves
// `z.string().regex(/^#[0-9A-Fa-f]{6}$/)` clean with no special-casing.
// `.default('#ff0000')`, by contrast, supplies a literal the app can go on
// to paint — inspectLiteralColorArguments reports it exactly like any other
// raw-colour literal (ts-colors/raw-color), because zod schema DATA is
// real, unlike expect/vi's opaque test fixtures (soundness decision, FIX-S
// evidence).
export function inspectZodChainLiterals(call, ctx) {
  for (const chainCall of zodChainCalls(call)) inspectLiteralColorArguments(chainCall, ctx)
}

export function hasStableRuntimeParameterRoot(node, ctx) {
  node = unwrap(node)
  while (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    if (ts.isElementAccessExpression(node)) {
      const key = unwrap(node.argumentExpression)
      if (!(ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key) || ts.isNumericLiteral(key))) return false
    }
    node = unwrap(node.expression)
  }
  if (!ts.isIdentifier(node)) return false
  const binding = lookup(ctx, node.text)
  return isParameterBinding(binding) && !binding.reassigned && binding.ownerName !== 'anonymous'
}

export function inspectClassObjectKeys(obj, ctx) {
  for (const prop of obj.properties) {
    if (ts.isSpreadAssignment(prop)) {
      emitUnsupported(ctx, prop)
      continue
    }
    const nameNode = prop.name
    if (!nameNode) continue
    if (ts.isIdentifier(nameNode) || ts.isStringLiteral(nameNode) || ts.isNoSubstitutionTemplateLiteral(nameNode)) {
      analyzeClassString(nameNode.text, ctx, nameNode)
    } else if (ts.isComputedPropertyName(nameNode)) {
      inspectClassExpr(nameNode.expression, ctx, new Set())
    }
  }
}

export function inspectLogical(node, ctx, stack, inspect) {
  const kind = node.operatorToken.kind
  if (kind === ts.SyntaxKind.PlusToken && inspect === inspectClassExpr) {
    inspect(node.left, ctx, stack)
    inspect(node.right, ctx, stack)
    return
  }
  if (kind === ts.SyntaxKind.AmpersandAmpersandToken) {
    if (!isDefinitelyBoolean(node.left, ctx)) inspect(node.left, ctx, stack)
    inspect(node.right, ctx, stack)
    return
  }
  if (kind === ts.SyntaxKind.BarBarToken || kind === ts.SyntaxKind.QuestionQuestionToken) {
    inspect(node.left, ctx, stack)
    inspect(node.right, ctx, stack)
    return
  }
  emitUnsupported(ctx, node)
}

export function isDefinitelyBoolean(node, ctx) {
  node = unwrap(node)
  if (!node) return false
  if (node.kind === ts.SyntaxKind.TrueKeyword || node.kind === ts.SyntaxKind.FalseKeyword) return true
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) return true
  if (ts.isBinaryExpression(node)) {
    return [
      ts.SyntaxKind.EqualsEqualsToken,
      ts.SyntaxKind.EqualsEqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsToken,
      ts.SyntaxKind.ExclamationEqualsEqualsToken,
      ts.SyntaxKind.LessThanToken,
      ts.SyntaxKind.LessThanEqualsToken,
      ts.SyntaxKind.GreaterThanToken,
      ts.SyntaxKind.GreaterThanEqualsToken,
      ts.SyntaxKind.InKeyword,
      ts.SyntaxKind.InstanceOfKeyword,
    ].includes(node.operatorToken.kind)
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    const property = ts.isPropertyAccessExpression(node)
      ? node.name.text
      : (() => {
          const key = unwrap(node.argumentExpression)
          return ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key) ? key.text : null
        })()
    const receiver = unwrap(node.expression)
    if (property === null || !ts.isIdentifier(receiver)) return false
    const binding = lookup(ctx, receiver.text)
    if (!isParameterBinding(binding) || binding.reassigned) return false
    let parameter = binding.declaration
    while (parameter && !ts.isParameter(parameter)) parameter = parameter.parent
    const type = parameter?.type
    return Boolean(type && typeHasBooleanProperty(type, property, ctx))
  }
  if (!ts.isIdentifier(node)) return false
  const binding = lookup(ctx, node.text)
  if (!isParameterBinding(binding)) {
    const value = bindingValue(binding)
    return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && !(ts.isIdentifier(value) && value.text === node.text) && isDefinitelyBoolean(value, ctx))
  }
  if (binding.boolean) return true
  let parameter = binding.declaration
  while (parameter && !ts.isParameter(parameter)) parameter = parameter.parent
  const type = parameter?.type
  if (!type) return false
  if (ts.isIdentifier(parameter.name) && parameter.name.text === node.text) return type.kind === ts.SyntaxKind.BooleanKeyword
  if (!ts.isTypeLiteralNode(type)) return false
  return type.members.some((member) => ts.isPropertySignature(member) && propertyNameOf(member.name) === node.text && member.type?.kind === ts.SyntaxKind.BooleanKeyword)
    || new RegExp(`\\b${node.text}\\s*\\??\\s*:\\s*boolean\\b`).test(type.getText())
}

export function inspectTemplate(node, ctx, mode, stack, boundary = null) {
  const parts = [node.head.text]
  let unresolved = false
  for (const span of node.templateSpans) {
    const text = tryString(span.expression, ctx, stack)
    if (text === null) {
      // Narrow escape hatch (Task 2 capability), deliberately NOT a mirror of
      // class mode's per-span fan-out below: a css-value template can
      // assemble a compound functional value (e.g. linear-gradient(...)), so
      // an unverified RUNTIME value (parameter, opaque call, extension
      // boundary) must never be spliced into it AS SAFE — that would be
      // genuine CSS-syntax-injection surface, unlike a whitespace-separated
      // Tailwind class list. Only a destructured member whose every resolved
      // source object is provably fixed at build time (a plain string
      // literal in every branch of a local/imported pure factory — the
      // fileTypeMeta shape: `const { color } = fileTypeMeta(...)`) counts as
      // "the base value is proven"; a bare `${value}` reaching a
      // parameter/boundary keeps the unconditional numeric-proof requirement
      // below untouched (named numeric proof adversarial suite). css-decl
      // and css-text also stay untouched: a template there can span
      // multiple declarations or property names, which this literal-only
      // proof does not model. runtimeBoundarySpan below is not a THIRD
      // "treat as safe" escape — it stays exactly as blocking as unresolved
      // (a real, registrable extension-boundary finding is emitted for the
      // span instead of a whole-template unsupported); it never contributes
      // its unproven text to `combined` (still pushes '' below), so it
      // cannot become an unscanned splice into whatever analyzeCssValue
      // eventually sees.
      const literalTargets = mode === 'css' ? literalDestructuredTemplateTargets(span.expression, ctx, stack) : null
      const arrayTargets = mode === 'css' && !literalTargets ? arrayElementTemplateTargets(span.expression, ctx, stack) : null
      const runtimeBoundarySpan = mode === 'css' ? runtimeBoundaryTemplateSpan(span.expression, ctx, stack, boundary) : null
      if (mode === 'class') {
        const expression = unwrap(span.expression)
        const binding = ts.isIdentifier(expression) ? resolveIdentInit(expression, ctx, stack) : null
        if (isParameterBinding(binding) && binding.forwardClassName) emitUnsupported(ctx, expression)
        else inspectClassExpr(span.expression, ctx, stack)
      }
      else if (literalTargets) {
        for (const target of literalTargets) analyzeCssValue(target.text, ctx, target)
      }
      else if (arrayTargets) {
        for (const target of arrayTargets) analyzeCssValue(target.text, ctx, target)
      }
      else if (runtimeBoundarySpan) {
        emitRuntimePaintBoundary(ctx, runtimeBoundarySpan, boundary)
      }
      // R1 user-authored-colour tint capability: widened css-mode template
      // span resolution for shapes the three narrow escape hatches above do
      // not cover — a `??`/`||` mix of a runtime member read and a
      // finite/const-resolvable branch (RollupBadge's `(agent?.color ??
      // STATUS_COLORS[item.status])`), a plain local reached through a
      // resolvable chain (PlansFilterBand's `displayColor`), and a call to a
      // local/imported pure helper whose return(s) resolve under the SAME
      // call-frame parameter substitution resolveIdentInit already performs
      // (TaskCard's taskDisplayColor(task), WorkspaceGraphTab's
      // planDisplayColor(activePlan), TaskNode's toTint(avatarColor)). See
      // resolveRuntimeTemplateSpanValue's own doc comment for the full
      // soundness argument — every leaf is independently emitted through the
      // exact same analyzeCssValue/emitRuntimePaintBoundary/emitUnsupported
      // paths this file already uses everywhere else, never spliced as
      // trusted text.
      else if (mode === 'css' && resolveRuntimeTemplateSpanValue(span.expression, ctx, stack, boundary)) {
        // handled — the resolver already emitted whatever findings apply.
      }
      else if (!isDefinitelyNumeric(span.expression, ctx)) unresolved = true
      parts.push('')
    } else {
      parts.push(text)
    }
    parts.push(span.literal.text)
  }
  if (unresolved) emitUnsupported(ctx, node)
  const combined = parts.join('')
  if (mode === 'class') analyzeClassString(combined, ctx, node)
  else if (mode === 'css-decl') analyzeCssDeclarations(combined, ctx, node)
  else if (mode === 'css-text') analyzeCssText(combined, ctx, node)
  else analyzeCssValue(combined, ctx, node)
}

export function isDefinitelyNumeric(node, ctx) {
  node = unwrap(node)
  if (!node) return false
  if (ts.isNumericLiteral(node)) return true
  if (ts.isBinaryExpression(node)) {
    return [ts.SyntaxKind.PlusToken, ts.SyntaxKind.MinusToken, ts.SyntaxKind.AsteriskToken, ts.SyntaxKind.SlashToken, ts.SyntaxKind.PercentToken, ts.SyntaxKind.AsteriskAsteriskToken].includes(node.operatorToken.kind)
      && isDefinitelyNumeric(node.left, ctx)
      && isDefinitelyNumeric(node.right, ctx)
  }
  if (ts.isPrefixUnaryExpression(node) && (node.operator === ts.SyntaxKind.PlusToken || node.operator === ts.SyntaxKind.MinusToken)) return isDefinitelyNumeric(node.operand, ctx)
  if (ts.isCallExpression(node)) {
    const callee = unwrap(node.expression)
    if (ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && callee.expression.text === 'Math') return true
    if (ts.isIdentifier(callee)) {
      const declaration = resolveIdentInit(callee, ctx, new Set())
      if (declaration && declaration !== LOOKUP_MISSING && declaration !== LOOKUP_UNBOUND && isFunctionLike(declaration)) {
        return declaration.type?.kind === ts.SyntaxKind.NumberKeyword
      }
    }
    return false
  }
  if (!ts.isIdentifier(node)) return false
  const binding = lookup(ctx, node.text)
  if (isParameterBinding(binding)) {
    let parameter = binding.declaration
    while (parameter && !ts.isParameter(parameter)) parameter = parameter.parent
    if (binding.reassigned) return false
    if (parameter?.type?.kind === ts.SyntaxKind.NumberKeyword) return true
    if (parameter?.type && ts.isTypeReferenceNode(parameter.type)) {
      const element = binding.declaration
      if (!ts.isBindingElement(element) || element.dotDotDotToken || element.parent !== parameter.name || !ts.isObjectBindingPattern(parameter.name)) return false
      const property = element.propertyName ? propertyNameOf(element.propertyName) : node.text
      return namedNumericProperty(parameter.type, property, parameter, new Set())
    }
    return Boolean(parameter?.type && new RegExp(`\\b${node.text}\\s*\\??\\s*:\\s*number\\b`).test(parameter.type.getText()))
  }
  const value = bindingValue(binding)
  return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && !(ts.isIdentifier(value) && value.text === node.text) && isDefinitelyNumeric(value, ctx))
}

// Only unique local declarations with required number fields establish this proof.
// Imported, merged, optional and union types remain unresolved deliberately.
export function namedNumericProperty(type, property, origin, seen) {
  if (ts.isTypeLiteralNode(type)) {
    const members = type.members.filter(member => ts.isPropertySignature(member) && propertyNameOf(member.name) === property)
    return members.length === 1 && !members[0].questionToken && members[0].type?.kind === ts.SyntaxKind.NumberKeyword
  }
  if (!ts.isTypeReferenceNode(type) || !ts.isIdentifier(type.typeName) || type.typeArguments?.length) return false
  const name = type.typeName.text
  if (seen.has(name)) return false
  const source = origin.getSourceFile()
  for (let scope = origin.parent; scope && scope !== source; scope = scope.parent) {
    // Proof is module-local: nested lexical scopes need their own complete
    // resolver, including class names, namespace merging and switch clauses.
    if (ts.isBlock(scope) || ts.isCaseBlock(scope)) return false
    if (ts.isModuleBlock(scope) || ts.isModuleDeclaration(scope)) return false
    if (ts.isClassDeclaration(scope) || ts.isClassExpression(scope)) return false
    if (scope.typeParameters?.some(parameter => parameter.name.text === name)) return false
  }
  const declarations = source.statements.filter(statement =>
    (ts.isTypeAliasDeclaration(statement) || ts.isInterfaceDeclaration(statement)) && statement.name.text === name)
  if (declarations.length !== 1) return false
  const declaration = declarations[0]
  if (declaration.typeParameters?.length) return false
  const next = new Set(seen).add(name)
  if (ts.isTypeAliasDeclaration(declaration)) return namedNumericProperty(declaration.type, property, origin, next)
  if (declaration.heritageClauses?.length) return false
  const members = declaration.members.filter(member => ts.isPropertySignature(member) && propertyNameOf(member.name) === property)
  return members.length === 1 && !members[0].questionToken && members[0].type?.kind === ts.SyntaxKind.NumberKeyword
}

export function isNumericSerialization(node, ctx) {
  node = unwrap(node)
  if (!ts.isCallExpression(node) || node.arguments.length !== 1) return false
  const callee = unwrap(node.expression)
  return ts.isIdentifier(callee) && callee.text === 'String' && isDefinitelyNumeric(node.arguments[0], ctx)
}

export function inspectLiteralColorArguments(node, ctx) {
  node = unwrap(node)
  if (!ts.isCallExpression(node)) return
  for (const argument of node.arguments) {
    const value = unwrap(argument)
    if (ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)) analyzeCssValue(value.text, ctx, value)
  }
}

export function tryString(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return null
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
  if (ts.isIdentifier(node)) {
    const init = resolveIdentInit(node, ctx, stack)
    if (isParameterBinding(init)) return null
    if (POSITIONAL_UNSTABLE_INITS.has(init)) return null
    if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return tryString(init, ctx, stack)
  }
  return null
}

export function resolveIdentInit(ident, ctx, stack) {
  const name = ident.text
  if (stack.has(name)) return LOOKUP_UNBOUND
  stack.add(name)
  // Positional lexical identity first: the binding that owns this identifier
  // by source position. The name-keyed sources below (walk-time scope stack,
  // module declaration/import maps) describe other positions — the caller's
  // scopes at call time, or the module top level — so they may only serve an
  // identifier with no positional binding at all. A positional binding that
  // cannot be valued must block, never fall through to those stale sources
  // (lexical binding collision family V1–V4).
  let init = positionalInit(ident, ctx, stack)
  if (init === null) {
    init = lookup(ctx, name)
    if (init === LOOKUP_MISSING) {
      const record = ctx.sourceRecords.get(ident.getSourceFile())
      const declaration = record?.declarations.get(name)
      if (declaration) init = ts.isVariableDeclaration(declaration) ? declaration.initializer ?? LOOKUP_UNBOUND : declaration
      else if (record?.imports.has(name)) {
        const imported = record.imports.get(name)
        const resolved = resolveModuleDeclaration(ctx, record.path, imported.specifier, imported.imported, stack)
        init = ts.isVariableDeclaration(resolved) ? resolved.initializer ?? LOOKUP_UNBOUND : resolved
      }
    }
    init = bindingValue(init)
  }
  stack.delete(name)
  return init
}

// Positional lexical binding: the nearest binding that owns an identifier by
// source position, independent of when resolution runs. Returns null when the
// walk crosses a scope deliberately outside positional proof (case/class/loop/
// namespace/catch boundaries, ambiguous matches) — the legacy name-keyed paths
// then stay in charge. A found binding is definitive: it is valued here or it
// blocks. A use ahead of the declaration (temporal dead zone, var hoisting)
// resolves to the initializer of unreachable code, which over-reports rather
// than opening a hole.
export function positionalInit(ident, ctx, stack) {
  const name = ident.text
  for (let scope = ident.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (isFunctionLike(scope) && scope.parameters.some(parameter => absenceBindingHasName(parameter.name, name))) {
      return positionalParameterInit(scope, name, ctx)
    }
    if (ts.isCatchClause(scope) && scope.variableDeclaration && absenceBindingHasName(scope.variableDeclaration.name, name)) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (ts.isVariableStatement(statement)) {
        for (const declaration of statement.declarationList.declarations) {
          if (absenceBindingHasName(declaration.name, name)) matches.push(declaration)
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
    if (matches.length) {
      if (matches.length !== 1) return null
      return declarationInit(matches[0], ident, ctx, stack)
    }
  }
  return null
}

// Value a positionally found declaring node. Variable declarations give their
// initializer; when the name is written in any shape anywhere in the declaring
// block the initializer node is additionally recorded as unstable — member
// navigation still yields its partial literal targets (known violations beside
// unknown values), while whole-value sinks block on it. Pattern bindings and
// defaulted/namespace imports have no provable whole value here and block.
// Named imports resolve through the module graph, and function/interface
// declarations keep term identity, mirroring the legacy module-map semantics.
export const POSITIONAL_UNSTABLE_INITS = new WeakSet()

export function declarationInit(declaration, ident, ctx, stack) {
  if (ts.isVariableDeclaration(declaration)) {
    if (!ts.isIdentifier(declaration.name)) return LOOKUP_UNBOUND
    const init = declaration.initializer ?? LOOKUP_UNBOUND
    if (init !== LOOKUP_UNBOUND && !wholeValueWritesAbsent(declaration)) POSITIONAL_UNSTABLE_INITS.add(init)
    return init
  }
  if (ts.isImportSpecifier(declaration)) {
    const statement = declaration.parent.parent.parent
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) return LOOKUP_UNBOUND
    const record = ctx.sourceRecords.get(ident.getSourceFile())
    const resolved = resolveModuleDeclaration(ctx, record?.path ?? ctx.path, statement.moduleSpecifier.text,
      declaration.propertyName?.text ?? declaration.name.text, stack)
    return ts.isVariableDeclaration(resolved) ? resolved.initializer ?? LOOKUP_UNBOUND : resolved
  }
  if (ts.isImportClause(declaration) || ts.isNamespaceImport(declaration)) return null
  return declaration
}

// Object-destructured local bindings (`const { color } = factory()`) are
// never valued by positionalInit/declarationInit above — a pattern
// declaration.name always returns LOOKUP_UNBOUND there (declarationInit's
// `!ts.isIdentifier(declaration.name)` guard) because that path's contract is
// one scalar continuation node, and a destructured member's value depends on
// which property of which resolved SOURCE object, not a single node. This is
// proven separately, as a finite fan-out over every statically-resolvable
// source object — resolveToObjects already covers literal objects, `as
// const` records, and calls to local/imported pure factory functions with
// multiple return branches (worklist pattern: `const { color } = fileTypeMeta(...)`
// reaching a style paint sink). Called by class/css-value identifier
// resolution only AFTER the ordinary LOOKUP_UNBOUND path has already failed,
// so it changes nothing about existing name-keyed, parameter, or plain
// single-identifier resolution.
export function destructuredBindingElement(ident) {
  const name = ident.text
  for (let scope = ident.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (isFunctionLike(scope) && scope.parameters.some(parameter => absenceBindingHasName(parameter.name, name))) return null
    if (ts.isCatchClause(scope) && scope.variableDeclaration && absenceBindingHasName(scope.variableDeclaration.name, name)) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (!ts.isVariableStatement(statement)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (absenceBindingHasName(declaration.name, name)) matches.push(declaration)
      }
    }
    if (matches.length) {
      if (matches.length !== 1) return null
      const declaration = matches[0]
      if (ts.isIdentifier(declaration.name)) return null
      return destructuredElementOf(declaration, declaration.name, name)
    }
  }
  return null
}

// The one narrow, provable exception for an ARRAY-destructured local:
// `const [x] = useState(...)` / `React.useState(...)`.
// Array patterns are otherwise never proven here at all — destructuredElementOf
// deliberately refuses them ("index is not a stable key": a sibling helper
// swapping return order would silently rebind every existing consumer, and
// the adversarial suite's "array-destructured bindings stay unsupported"
// case exists specifically to keep an ARBITRARY `const [x] = someCall()`
// blocked forever). useState is not arbitrary: it is a fixed, single-purpose
// React API whose contract guarantees the bound identifier holds exactly
// whatever value its OWN paired setter last wrote — never anything from
// another source — so this checks the exact literal callee shape, nothing
// broader (no aliasing through an intermediate variable, no custom hook
// wrapping useState, no `React.useState` behind a renamed import — all of
// those stay unresolved, matching how narrowly composerForwardParameter and
// forwardClassName are drawn elsewhere in this file).
// Returns a descriptor — { declaration, initializer, setterCalls, owner } —
// only when EVERY write this state could ever receive is visible and
// accounted for; returns null otherwise (not a useState local at all, OR a
// useState local whose setter escapes — see setterUsage below). The
// descriptor's setterCalls (every direct `setX(...)` call site within the
// owning component) and initializer are what scanHookStateLaundering scans
// afterwards (2026-09-19 lead review: a registered read-site exception must
// never become a free pass for a raw colour literal smuggled in through
// EITHER the initial value or a later setter call — closing that gap is
// THIS function's escape proof plus scanHookStateLaundering's independent
// scan of exactly the two places a literal could hide).
export function hookStateLocal(ident) {
  const name = ident.text
  for (let scope = ident.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (isFunctionLike(scope) && scope.parameters.some(parameter => absenceBindingHasName(parameter.name, name))) return null
    if (ts.isCatchClause(scope) && scope.variableDeclaration && absenceBindingHasName(scope.variableDeclaration.name, name)) return null
    if (!ts.isBlock(scope) && !ts.isSourceFile(scope)) continue
    const matches = []
    for (const statement of scope.statements) {
      if (!ts.isVariableStatement(statement)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (absenceBindingHasName(declaration.name, name)) matches.push(declaration)
      }
    }
    if (!matches.length) continue
    if (matches.length !== 1) return null
    const declaration = matches[0]
    const pattern = declaration.name
    if (!ts.isArrayBindingPattern(pattern)) return null
    const bound = pattern.elements.some(element =>
      ts.isBindingElement(element) && !element.dotDotDotToken && !element.initializer
      && ts.isIdentifier(element.name) && element.name.text === name)
    if (!bound) return null
    const initializer = unwrap(declaration.initializer)
    if (!initializer || !ts.isCallExpression(initializer)) return null
    const callee = unwrap(initializer.expression)
    const isUseState = (ts.isIdentifier(callee) && callee.text === 'useState')
      || (ts.isPropertyAccessExpression(callee) && callee.name.text === 'useState')
    if (!isUseState) return null
    let owner = declaration.parent
    while (owner && !isFunctionLike(owner)) owner = owner.parent
    if (!owner) return null
    // The setter is always array position 1 (array destructuring has no
    // renaming concept — position IS the binding). Anything other than a
    // plain, simple identifier there (missing entirely, a rest element, a
    // default, a nested pattern — none of these are realistic React code)
    // means no NAME ever reaches anywhere this file could call it from, so
    // there is nothing to scan and nothing that could escape either.
    const setterElement = pattern.elements[1]
    const setterIsNamed = setterElement && ts.isBindingElement(setterElement)
      && !setterElement.dotDotDotToken && !setterElement.initializer && ts.isIdentifier(setterElement.name)
    if (!setterIsNamed) return { declaration, initializer, setterCalls: [], owner }
    const usage = setterUsage(owner, setterElement.name.text, setterElement.name)
    if (usage === null) return null // the setter escapes — void the whole proof, not just one argument
    return { declaration, initializer, setterCalls: usage, owner }
  }
  return null
}

// Walks the owning component for every reference to a state setter's name,
// classifying each as either a direct call (`setX(...)`, collected so
// scanHookStateLaundering can inspect its argument) or an ESCAPE — passed to
// another function/component, returned, assigned to another variable,
// spread, or anything else that is not literally the callee of a call
// expression. An escape means a write could originate somewhere this file
// never sees, so the caller must void the entire useState exception (return
// null), not merely skip counting that one reference — this is the fix for
// the exact gap the lead's bcc-setter-probe and a hypothetical
// `onSave={setColor}` prop-forward would otherwise both fall through.
// `declaredNameNode` (the setter's OWN binding-element identifier) is
// excluded from the walk so the declaration site itself is never mistaken
// for a use.
export function setterUsage(owner, setterName, declaredNameNode) {
  const calls = []
  let escaped = false
  const visit = (node) => {
    if (!node) return
    if (node !== declaredNameNode && ts.isIdentifier(node) && node.text === setterName) {
      const parent = node.parent
      if (parent && ts.isCallExpression(parent) && unwrap(parent.expression) === node) calls.push(parent)
      else escaped = true
    }
    ts.forEachChild(node, visit)
  }
  visit(owner.body ?? owner)
  return escaped ? null : calls
}

// Independently scans the two places a raw colour literal could be
// laundered through an already-registered useState exception: the
// useState(...) call's own initial-value argument, and every argument ever
// passed to the paired setter within the owning component (setterCalls is
// already proven complete — hookStateLocal returns null instead whenever
// the setter escapes). Silences ts-colors/unsupported for the duration (via
// ctx.silentUnsupported) — an opaque or runtime argument here (a parameter,
// `agent.color`, a callback parameter) adds nothing extra; the read-site
// boundary already covers it. A functional update's own return value(s)
// (`setC(prev => '#f00')`) are scanned the same way a plain argument is.
export function scanHookStateLaundering(state, ctx) {
  const wasSilent = ctx.silentUnsupported
  ctx.silentUnsupported = true
  try {
    scanStateValueArgument(state.initializer.arguments[0], ctx)
    for (const call of state.setterCalls) scanStateValueArgument(call.arguments[0], ctx)
  } finally {
    ctx.silentUnsupported = wasSilent
  }
}

export function scanStateValueArgument(argument, ctx) {
  if (!argument) return
  const node = unwrap(argument)
  if (node && (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) && node.body) {
    for (const returned of collectReturns(node)) scanStateValueArgument(returned, ctx)
    return
  }
  inspectCssValueExpr(argument, ctx, new Set())
}

// Only a simple, unrenamed-or-renamed, non-rest, non-default, non-nested
// element is a stable proof target. A rest collection, a default fallback
// value, or a nested sub-pattern each carry aggregation/fallback semantics
// this proof does not model, so they stay unsupported (never guessed at).
export function destructuredElementOf(declaration, pattern, name) {
  if (!ts.isObjectBindingPattern(pattern)) return null // array patterns: index is not a stable key
  for (const element of pattern.elements) {
    if (!ts.isBindingElement(element)) continue
    if (element.dotDotDotToken) { if (absenceBindingHasName(element.name, name)) return null; continue }
    if (!ts.isIdentifier(element.name)) { if (absenceBindingHasName(element.name, name)) return null; continue }
    if (element.name.text !== name) continue
    if (element.initializer) return null
    const key = element.propertyName ? propertyNameOf(element.propertyName) : element.name.text
    if (key === null) return null
    if (destructuredElementReassigned(declaration, element)) return null
    return { declaration, key }
  }
  return null
}

// Same write-detection shape as wholeValueWritesAbsent, applied to the
// destructured local name itself: a downstream reassignment of the binding
// would silently replace the proven value with something unproven, so it
// must void the proof rather than resolve against stale data.
export function destructuredElementReassigned(declaration, element) {
  const name = element.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return true
  let unsafe = false
  const visit = node => {
    if (unsafe) return
    if (ts.isIdentifier(node) && node.text === name && node !== element.name) {
      let expression = node
      while (expression.parent && (ts.isParenthesizedExpression(expression.parent)
        || ts.isAsExpression(expression.parent) || ts.isNonNullExpression(expression.parent))) expression = expression.parent
      const use = expression.parent
      if ((ts.isBinaryExpression(use) && use.left === expression && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
        || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
        || ts.isDeleteExpression(use)
        || (ts.isPrefixUnaryExpression(use) && (use.operator === ts.SyntaxKind.PlusPlusToken || use.operator === ts.SyntaxKind.MinusMinusToken))
        || (ts.isPostfixUnaryExpression(use) && (use.operator === ts.SyntaxKind.PlusPlusToken || use.operator === ts.SyntaxKind.MinusMinusToken))) { unsafe = true; return }
    }
    if (!unsafe) ts.forEachChild(node, visit)
  }
  visit(scope)
  return unsafe
}

// Resolve a destructured identifier to the finite set of property-value
// expressions across every statically-provable source object. Returns null
// when this identifier is not a destructuring case at all (callers fall
// through to their existing LOOKUP_UNBOUND handling unchanged); returns
// { resolved: false } when it IS a destructuring case but cannot be proven
// (unresolvable source, incomplete member coverage, or the property is
// missing from at least one candidate branch — never guessed, never
// defaulted) — callers must still block. { resolved: true, targets } is only
// returned when the named property is present in every resolved candidate.
export function resolveDestructuredTargets(ident, ctx, stack) {
  const found = destructuredBindingElement(ident)
  if (!found) return null
  const { declaration, key } = found
  const resolution = { incomplete: false }
  const objects = resolveToObjects(unwrap(declaration.initializer), ctx, stack, resolution)
  // Same incompleteness rule resolveMemberTargets enforces for ordinary
  // member access: a spread, or a computed key whose name cannot be
  // statically pinned down, on ANY candidate object means ECMAScript could
  // resolve the target property from an opaque runtime value (a later
  // spread, or unresolvable computed key, overrides an earlier explicit
  // property), so the whole destructuring proof must be voided, not
  // silently first-match-won. A computed key that IS statically a literal
  // (`['color']`, `[SOME_CONST]` resolved to a literal, or the same wrapped
  // in a cast) carries no such opacity — propertyNameOf already resolves it
  // to a plain name, and propertyInit's last-match-wins lookup below
  // resolves it in the same left-to-right override order the language
  // uses, so it does not need to blank the whole proof.
  if (objects.some(obj => obj.properties.some(opaqueObjectMember))) resolution.incomplete = true
  if (resolution.incomplete || objects.length === 0) return { resolved: false }
  const targets = []
  for (const obj of objects) {
    const value = propertyInit(obj, key)
    if (!value) return { resolved: false }
    targets.push(value)
  }
  return { resolved: true, targets }
}

// R1 user-authored-colour capability: TaskNode.tsx's `const { agentColor }
// = data` — a BODY-level destructuring of a name straight off a function's
// OWN parameter (`data` in `function TaskNodeComponent({ data, selected })`)
// is destructuredBindingElement's exact "resolved, but the source is not a
// finite const object" case — resolveDestructuredTargets correctly reports
// `{ resolved: false }` for it (objects.length === 0), because `data` is
// genuine runtime prop data, not a finite record. That is precisely the
// user-authored-colour shape (a persisted field read off runtime entity
// data), not a gap: read the SAME way as `item.agentColor` or `agent?.color`
// already resolve via isDirectRuntimeRead + a runtime paint boundary — this
// only recognizes the destructured-LOCAL-NAME form of that identical read.
// Requires the destructuring SOURCE to be a bare identifier that resolves to
// a PROVEN, non-reassigned, non-anonymous-owner parameter binding — an
// arbitrary opaque call (`const { color } = fileTypeMeta(...)`, the
// adversarial suite's pinned "must stay unsupported forever" case) resolves
// its source to something OTHER than a parameter binding here (LOOKUP_UNBOUND,
// or a call expression), so this never touches that proof.
export function destructuredSourceParameterBinding(ident, ctx, stack) {
  const found = destructuredBindingElement(ident)
  if (!found) return null
  const source = unwrap(found.declaration.initializer)
  if (!source || !ts.isIdentifier(source)) return null
  const binding = resolveIdentInit(source, ctx, new Set(stack))
  if (!isParameterBinding(binding) || binding.reassigned || binding.ownerName === 'anonymous') return null
  return binding
}

// R1 user-authored-colour capability, className-forward variant: table.tsx's
// `const { className: containerClassName, ... } = containerProps`, where
// `containerProps` is itself Table's own top-level destructured parameter
// element. Ported from typography.mjs's forwardedClassBoundary body-
// destructuring branch — same "a name bound directly in the function's OWN
// parameter list carries the identical unchanged-caller-value guarantee"
// contract, entered through destructuredBindingElement instead of a
// hand-rolled scope walk. Reports the SOURCE parameter + property path
// (`containerProps.className`), never the local alias and never the
// enclosing function's own bare name — `Table#className` already names a
// DIFFERENT boundary (the table's own direct className parameter) and must
// not collide with this one (lead directive, 2026-09-20).
export function bodyDestructuredClassBoundary(ident, ctx, stack) {
  const found = destructuredBindingElement(ident)
  if (!found || (found.key !== 'className' && found.key !== 'class')) return null
  const source = unwrap(found.declaration.initializer)
  if (!source || !ts.isIdentifier(source)) return null
  const binding = resolveIdentInit(source, ctx, new Set(stack))
  if (!isParameterBinding(binding) || binding.reassigned || binding.ownerName === 'anonymous') return null
  return { ownerName: binding.ownerName, name: `${source.text}.${found.key}`, declaration: binding.declaration }
}

// Only a bare string literal (or no-substitution template) is fixed at
// build time and therefore safe to splice into a larger css-value template
// with no runtime-injection surface — see inspectTemplate's css-mode branch.
export function isLiteralCssValue(node) {
  return ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)
}

// css-value template escape hatch for a destructured identifier span (Task 2
// capability): returns the finite set of literal target nodes only when the
// span is a bare identifier resolving, via resolveDestructuredTargets, to
// values that are ALL plain string literals — never a parameter, call, or
// any other runtime-shaped value. Returns null otherwise (not this case at
// all, or resolved to something not provably literal), so the caller falls
// back to the pre-existing numeric-proof/unresolved behavior unchanged.
export function literalDestructuredTemplateTargets(expression, ctx, stack) {
  const node = unwrap(expression)
  if (!ts.isIdentifier(node)) return null
  const destructured = resolveDestructuredTargets(node, ctx, stack)
  if (!destructured?.resolved || !destructured.targets.every(isLiteralCssValue)) return null
  return destructured.targets
}

// css-mode template escape hatch for the finite-const-array-callback
// capability (bullet 3): `` `0 0 0 2px var(--color-primary), 0 0 0 4px
// ${color}` `` in AgentFormFields.tsx's AvatarColorPicker — `tryString`
// cannot give ONE static string for an array-element parameter (there are
// several candidate elements, by design), so this mirrors
// literalDestructuredTemplateTargets' exact contract for that different
// (array, not destructured-record) provenance: only literal string targets
// qualify, everything else falls through to the ordinary unresolved path.
export function arrayElementTemplateTargets(expression, ctx, stack) {
  const node = unwrap(expression)
  if (!ts.isIdentifier(node)) return null
  const init = resolveIdentInit(node, ctx, stack)
  if (!isParameterBinding(init) || !init.arrayElements || init.reassigned) return null
  const targets = init.arrayElements.map(unwrap)
  return targets.every(isLiteralCssValue) ? targets : null
}

// css-mode template escape hatch for a plain LOCAL identifier span —
// deliberately NEVER a parameter or an opaque call: this mirrors
// inspectCssValueExpr's own unresolved-identifier boundary check (same
// file, same rule), applied to a template SPAN instead of a bare value.
// Returns the span's own identifier node — for the caller to register as
// an exact, reviewable extension boundary — only when EVERY other avenue
// this file has for proving the span is exhausted: it is a bare identifier
// (never a nested expression — the "AST-normalized expression" identity
// requirement stays exact, one finding per site), it is not a parameter (a
// parameter/opaque call inside a template stays governed by the comment
// above), and it does not resolve (LOOKUP_UNBOUND/MISSING, or a
// destructuring proof that could not complete) — with a KNOWN paint-sink
// boundary available. Returns null for anything else (no boundary, a
// parameter, an opaque call, or a value this file CAN still resolve).
export function runtimeBoundaryTemplateSpan(expression, ctx, stack, boundary) {
  if (!boundary) return null
  const node = unwrap(expression)
  if (!node || !ts.isIdentifier(node) || isSkipIdent(node.text)) return null
  // Same two narrow, provable exceptions as inspectCssValueExpr's own
  // unresolved-identifier boundary check — never a blanket "any boundary"
  // escape (see isRuntimeMeasurementBoundary's and hookStateLocal's own
  // comments for why: the adversarial suite's opaque-call/opaque-array
  // destructuring cases must stay unsupported even inside a template span).
  if (boundary.owner === 'anonymous') return null
  const hookState = isRuntimeMeasurementBoundary(boundary) ? null : hookStateLocal(node)
  if (!isRuntimeMeasurementBoundary(boundary) && !hookState) return null
  // Same laundering close as inspectCssValueExpr's own hookStateLocal branch
  // (2026-09-19 lead review): the exact same exception, reached through a
  // template span instead of a bare value, must not let a raw literal
  // smuggled through the initial value or a setter call go unreported.
  if (hookState) scanHookStateLaundering(hookState, ctx)
  const init = resolveIdentInit(node, ctx, stack)
  if (isParameterBinding(init) || POSITIONAL_UNSTABLE_INITS.has(init)) return null
  if (init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND && init) return null
  const destructured = resolveDestructuredTargets(node, ctx, stack)
  if (destructured?.resolved) return null
  return node
}

// R1 user-authored-colour tint capability: a css-mode template-span resolver
// that mirrors inspectCssValueExpr's OWN dispatch (BinaryExpression ??/||,
// ConditionalExpression, Identifier resolution incl. resolveIdentInit's
// call-frame parameter substitution, PropertyAccess/ElementAccess via
// resolveMemberTargets + the SAME isDirectRuntimeRead boundary fallback,
// CallExpression via withCalleeFrame/collectReturns) but for the "prove or
// decline, never splice unproven text" contract a template SPAN requires —
// see inspectTemplate's own comment on why a compound css-value template
// must never trust an unresolved value as safe text. Every leaf this
// reaches is independently EMITTED (a literal through analyzeCssValue at
// its own node, a runtime-proven read through emitRuntimePaintBoundary, a
// nested template through inspectTemplate itself) as a side effect; the
// caller only ever splices an empty placeholder for the span, exactly like
// runtimeBoundaryTemplateSpan's existing contract. Returns true only when
// EVERY reachable leaf was independently accounted for this way; any leaf
// this cannot prove returns false, leaving inspectTemplate's pre-existing
// unresolved/emitUnsupported fallback in full, unchanged control — this is
// strictly additive coverage, never a relaxation of an existing pinned
// shape (a const-rooted, non-runtime value still resolves as raw-color/
// token exactly as it does outside a template; an opaque call with no
// local declaration, or a parameter with no boundary, still returns false
// here exactly as it fails everywhere else in this file).
export function resolveRuntimeTemplateSpanValue(expression, ctx, stack, boundary) {
  const node = unwrap(expression)
  if (!node) return false
  if (isLiteralCssValue(node)) { analyzeCssValue(node.text, ctx, node); return true }
  if (ts.isTemplateExpression(node)) { inspectTemplate(node, ctx, 'css', stack, boundary); return true }
  if (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken)) {
    const left = resolveRuntimeTemplateSpanValue(node.left, ctx, stack, boundary)
    const right = resolveRuntimeTemplateSpanValue(node.right, ctx, stack, boundary)
    return left && right
  }
  if (ts.isConditionalExpression(node)) {
    const whenTrue = resolveRuntimeTemplateSpanValue(node.whenTrue, ctx, stack, boundary)
    const whenFalse = resolveRuntimeTemplateSpanValue(node.whenFalse, ctx, stack, boundary)
    return whenTrue && whenFalse
  }
  if (ts.isIdentifier(node)) {
    if (isSkipIdent(node.text)) return false
    const marker = `${node.getSourceFile().fileName}#tplspanval:${node.pos}`
    if (stack.has(marker)) return false
    const nextStack = new Set(stack).add(marker)
    const init = resolveIdentInit(node, ctx, nextStack)
    if (isParameterBinding(init)) {
      // Deliberately NEVER resolved here (regression fix, mutation-verified):
      // a BARE top-level parameter spliced directly into a template must
      // stay exactly as blocking as the pre-existing pinned adversarial
      // contract requires ("a template alpha suffix on an unproven
      // (non-destructured) value stays unsupported, never spliced"; "an
      // unproven-type numeric template is unaffected by the
      // destructured-literal escape hatch") — a plain parameter carries no
      // independent provenance signal here the way a MEMBER read
      // (`agent.color`), a `??`/`||` mix, or a call return does. The one
      // capability this file's own real targets need — the finite-const-
      // array-callback element (AVATAR_COLORS.map((color) => ...) reached
      // inside its template) — is already fully owned by the pre-existing
      // arrayElementTemplateTargets escape hatch (checked earlier in
      // inspectTemplate), so this branch never needs to re-resolve it.
      return false
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      const destructured = resolveDestructuredTargets(node, ctx, stack)
      if (destructured) {
        if (!destructured.resolved) {
          if (boundary && isRuntimeMeasurementBoundary(boundary)) { emitRuntimePaintBoundary(ctx, node, boundary); return true }
          if (boundary && boundary.owner !== 'anonymous' && destructuredSourceParameterBinding(node, ctx, stack)) { emitRuntimePaintBoundary(ctx, node, boundary); return true }
          return false
        }
        // literalDestructuredTemplateTargets (checked earlier in
        // inspectTemplate, unchanged) already owns the "every resolved
        // candidate is a plain literal" case for a destructured span — this
        // point is reached ONLY when that already failed, i.e. at least one
        // candidate branch is NOT provably literal. Pinned adversarial
        // contract ("a template alpha suffix on a destructured non-literal
        // value stays unsupported"): that shape stays wholly unresolved here
        // too, never a partial per-branch emission — recursing per-target
        // through this resolver would leak a raw-color finding for the ONE
        // literal branch while the compound span as a whole is still
        // unproven, which is exactly the partial-credit splice this file's
        // template handling refuses everywhere else.
        return false
      }
      if (!boundary || boundary.owner === 'anonymous') return false
      const hookState = isRuntimeMeasurementBoundary(boundary) ? null : hookStateLocal(node)
      if (isRuntimeMeasurementBoundary(boundary) || hookState) {
        if (hookState) scanHookStateLaundering(hookState, ctx)
        emitRuntimePaintBoundary(ctx, node, boundary)
        return true
      }
      return false
    }
    return resolveRuntimeTemplateSpanValue(init, ctx, nextStack, boundary)
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    // Status-contract governed-record capability: checked FIRST, mirroring
    // inspectCssValueExpr's own call-site check exactly —
    // every real consumer of `statusContract.<key>.resolvedColor` reaches
    // THIS resolver (not inspectCssValueExpr directly) whenever the read
    // sits inside a template span (`` `${color}1a` ``, the alpha-tint idiom
    // every applied consumer uses), because inspectTemplate's `tryString`
    // cannot fold a PropertyAccessExpression into a literal and falls to
    // this parallel dispatch instead. A matching read is fully accounted
    // for (returns true, no finding) with no further action needed here;
    // every other shape falls through unchanged to the existing
    // resolveMemberTargets/emitRuntimePaintBoundary handling below.
    if (ts.isPropertyAccessExpression(node) && isStatusContractColorRead(node, ctx)) return true
    const nullAllowed = isProvenNonNullReceiver(node)
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, stack, resolution, nullAllowed)
    if (targets.length) {
      if (resolution.incomplete) return false
      return targets.every((target) => resolveRuntimeTemplateSpanValue(target, ctx, stack, boundary))
    }
    if (boundary && isDirectRuntimeRead(node) && boundary.owner !== 'anonymous') { emitRuntimePaintBoundary(ctx, node, boundary); return true }
    return false
  }
  if (ts.isCallExpression(node)) {
    const outcome = withCalleeFrame(node, ctx, stack, (returns) => returns.every((returned) => resolveRuntimeTemplateSpanValue(returned, ctx, stack, boundary)))
    return outcome === true
  }
  return false
}

// A parameter is owned by its function; its runtime value comes from the
// active caller/callee frame's call argument, or the parameter default, or —
// when no frame is being resolved — the walk-time binding of that same
// parameter (in-place inspection). Anything else blocks: parameters never
// fall through to module maps (V4).
// An array-mutating method call whose RECEIVER is the composer's own rest
// parameter (`inputs.push('text-[10px]')`) injects content that never
// passed through any call site's argument inspection — the forward-
// parameter trust below must not cover it.
export const ARRAY_MUTATING_METHODS = new Set(['push', 'pop', 'shift', 'unshift', 'splice', 'sort', 'reverse', 'fill', 'copyWithin'])

export function composerParameterIsMutated(owner, paramName) {
  let mutated = false
  const visit = node => {
    if (mutated || !node) return
    if (ts.isIdentifier(node) && node.text === paramName) {
      const parent = node.parent
      if (ts.isPropertyAccessExpression(parent) && parent.expression === node
        && ARRAY_MUTATING_METHODS.has(parent.name.text)
        && ts.isCallExpression(parent.parent) && parent.parent.expression === parent) { mutated = true; return }
      if (ts.isElementAccessExpression(parent) && parent.expression === node
        && ts.isBinaryExpression(parent.parent) && parent.parent.left === parent
        && parent.parent.operatorToken.kind === ts.SyntaxKind.EqualsToken) { mutated = true; return }
    }
    ts.forEachChild(node, visit)
  }
  if (owner.body) visit(owner.body)
  return mutated
}

// A rest/destructured parameter of a recognized class-composing function
// (cn/clsx/… or the project-local classes/statusDot) is, structurally,
// exactly the same forward-parameter contract as one literally named
// `className` — every call site's own arguments are independently
// inspected wherever THAT call is written, so the composed value reaching
// this parameter is never new — UNLESS the composer's own body mutates the
// parameter in place before forwarding it (an array-mutating method call, or
// a direct element assignment), which injects content no call site's
// argument inspection ever saw (P10 forbidden control — a composer body
// that pushes an unvalidated literal onto its rest parameter must keep
// blocking, not be swept into the same trust as a pure forward). Computed
// statelessly from the parameter/owner AST alone (not the walk-time scope
// binding — see the call site below for why the latter is unavailable
// here) so it holds regardless of whether this parameter is reached through
// an active callee frame or the in-place walk.
export function composerForwardParameter(parameter, owner) {
  if (!ts.isIdentifier(parameter.name)) return false
  const ownerName = functionIdentity(owner)
  if (!CLASS_BUILDERS.has(ownerName) && !LOCAL_CLASS_COMPOSERS.has(ownerName)) return false
  if (!(Boolean(parameter.dotDotDotToken) || owner.parameters.length === 1)) return false
  return !composerParameterIsMutated(owner, parameter.name.text)
}

export function positionalParameterInit(owner, name, ctx) {
  let parameter = null
  let index = -1
  let bindable = true
  for (let position = 0; position < owner.parameters.length; position += 1) {
    const candidate = owner.parameters[position]
    if (!absenceBindingHasName(candidate.name, name)) continue
    if (!ts.isIdentifier(candidate.name) || candidate.dotDotDotToken) bindable = false
    parameter = candidate
    index = position
    break
  }
  if (!parameter) return LOOKUP_UNBOUND
  for (let depth = ctx.callFrames.length - 1; depth >= 0; depth -= 1) {
    const frame = ctx.callFrames[depth]
    if (frame.declaration !== owner) continue
    // A pattern or rest parameter cannot bind to a single call argument —
    // UNLESS it is a recognized composer's own forward parameter, whose
    // call-site arguments are validated at every call site regardless of
    // how many collapse into this one rest slot; surface that as a
    // parameter binding here (rather than the walk-time scope lookup,
    // which is no longer on the scope stack while tracing a callee's body
    // from a DIFFERENT function's call site) so the existing
    // forwardClassName → extension-boundary path applies uniformly.
    if (!bindable) {
      if (composerForwardParameter(parameter, owner)) {
        return { kind: PARAMETER_BINDING, name, ownerName: functionIdentity(owner), declaration: parameter, reassigned: false, boolean: false, forwardClassName: true, forwardStyle: false }
      }
      return LOOKUP_UNBOUND
    }
    const argument = frame.call.arguments[index]
    if (argument) return argument
    return parameter.initializer ?? LOOKUP_UNBOUND
  }
  // Without an active frame the walk-time binding of this same parameter
  // stays in charge (in-place JSX/className classification).
  const binding = lookup(ctx, name)
  return isParameterBinding(binding) ? binding : LOOKUP_UNBOUND
}

export const WHOLE_VALUE_WRITE_CACHE = new WeakMap()

export function wholeValueWritesAbsent(declaration) {
  if (WHOLE_VALUE_WRITE_CACHE.has(declaration)) return WHOLE_VALUE_WRITE_CACHE.get(declaration)
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  let safe = true
  if (scope) {
    const visit = node => {
      if (!safe) return
      if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
        let expression = node
        while (expression.parent && (ts.isPropertyAccessExpression(expression.parent)
          || ts.isElementAccessExpression(expression.parent) || ts.isParenthesizedExpression(expression.parent)
          || ts.isAsExpression(expression.parent) || ts.isNonNullExpression(expression.parent)
          || ts.isArrayLiteralExpression(expression.parent) || ts.isObjectLiteralExpression(expression.parent))) expression = expression.parent
        const use = expression.parent
        if ((ts.isBinaryExpression(use) && use.left === expression && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
          || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
          || ts.isDeleteExpression(use)
          || (ts.isPrefixUnaryExpression(use) && (use.operator === ts.SyntaxKind.PlusPlusToken || use.operator === ts.SyntaxKind.MinusMinusToken))
          || (ts.isPostfixUnaryExpression(use) && (use.operator === ts.SyntaxKind.PlusPlusToken || use.operator === ts.SyntaxKind.MinusMinusToken))) { safe = false; return }
      }
      if (safe) ts.forEachChild(node, visit)
    }
    visit(scope)
  }
  WHOLE_VALUE_WRITE_CACHE.set(declaration, safe)
  return safe
}

export function calleeDeclaration(call, ctx, stack) {
  const callee = unwrap(call.expression)
  return isFunctionLike(callee) ? callee : ts.isIdentifier(callee) ? resolveIdentInit(callee, ctx, stack) : null
}

export function isLocalFunction(declaration) {
  return Boolean(declaration && declaration !== LOOKUP_UNBOUND
    && (ts.isFunctionDeclaration(declaration) || ts.isFunctionExpression(declaration) || ts.isArrowFunction(declaration)) && declaration.body)
}

export function collectReturns(declaration) {
  const returns = []
  const visit = (node) => {
    if (isFunctionLike(node) && node !== declaration) return
    if (ts.isReturnStatement(node) && node.expression) returns.push(node.expression)
    else ts.forEachChild(node, visit)
  }
  if (ts.isBlock(declaration.body)) visit(declaration.body)
  else returns.push(declaration.body)
  return returns
}

export function functionReturns(call, ctx, stack) {
  const declaration = calleeDeclaration(call, ctx, stack)
  if (!isLocalFunction(declaration)) return null
  // Position-keyed on the resolved declaration (calleeDeclaration already
  // performs proper scope-based identifier resolution) rather than the
  // callee identifier's bare text — two distinct declarations that happen to
  // share a name (e.g. a shadowed same-named local function) must not
  // collide on this cycle-breaking marker. Keying on the declaration (not
  // the call site) is intentional here, unlike the sibling object/array/call
  // markers: this marker exists to break recursion through a shared callee,
  // which every call site into that same declaration must detect alike.
  const marker = `${declaration.getSourceFile().fileName}#${declaration.pos}`
  if (stack.has(marker)) return []
  stack.add(marker)
  const returns = collectReturns(declaration)
  stack.delete(marker)
  return returns
}

// Resolve a local call's returns under a caller/callee frame so positional
// parameter resolution can bind call arguments (V4). `body` receives the
// collected return expressions; its return value propagates. Null (opaque
// callee) and [] (recursion marker) keep the legacy call-site contracts.
export function withCalleeFrame(call, ctx, stack, body) {
  const declaration = calleeDeclaration(call, ctx, stack)
  if (!isLocalFunction(declaration)) return null
  // Position-keyed on the resolved declaration — see functionReturns' marker
  // comment for why this must be the declaration, not the callee text or the
  // call site.
  const marker = `${declaration.getSourceFile().fileName}#${declaration.pos}`
  if (stack.has(marker)) return []
  stack.add(marker)
  ctx.callFrames.push({ declaration, call })
  try {
    const returns = collectReturns(declaration)
    return returns.length ? body(returns) : []
  } finally {
    ctx.callFrames.pop()
    stack.delete(marker)
  }
}

// ── Generated-token-accessor capability ──────────────────────────────────────
//
// Closes the last ts-colors/unsupported finding in the tree:
// src/design-system/status.ts's `generatedColor()`, whose sole return
// (`value.toUpperCase()`) is unresolvable by the ordinary interprocedural
// walk — `.toUpperCase()`'s callee isn't a local declaration, so
// withCalleeFrame collects zero returns and inspectCssValueExpr's generic
// CallExpression branch falls to emitUnsupported. Root cause and the shape
// this recognizes are documented in full in
// dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md; this implementation differs from that spec in two ways:
//   1. A matching call site reports CLEAN, never ts-colors/extension-
//      boundary — extension-boundary is reserved for RUNTIME ENTITY DATA (a
//      prop, a user-chosen persisted field) that still needs central,
//      per-call-site review (see the R1 design note, same fanout dir). A
//      value read from the project's OWN generated-token output, through an
//      accessor whose only content-shaping step is a case change, is not
//      that: nothing this call can evaluate to is anything other than what
//      the token generator itself produced for that key. It is the same
//      trust class as a `var(--token)` CSS reference, which this scanner
//      already treats as clean (see COMMON-RULES' colour fairness control).
//      An extension-boundary marker here would misfile a governed-pipeline
//      read as an ad hoc runtime value needing case-by-case sign-off.
//   2. No structural throw-guard match is required (the spec's condition 3).
//      The classification here is "is this call site definitionally reading
//      the token module's own generated content", not "is this value
//      provably a valid colour" — a throw-guard's presence or absence
//      changes the latter, not the former. Requiring one would add AST-
//      matching surface without closing any additional required case.
//
// Implemented as ONE atomic structural match at the CALL SITE
// (isGeneratedTokenAccessorCall), not as a composition of "recurse through
// .toUpperCase()" plus "recurse through the element access" as two
// independent, generally-wired capabilities: wiring `.toUpperCase()`/
// `.trim()` as a general receiver-preserving case in inspectCssValueExpr
// (mirroring inspectClassExpr's existing STRING_TRIM_METHODS handling)
// makes `generatedValues[id]` reachable as a plain ElementAccessExpression
// with a non-literal key — which resolveMemberTargets already handles, via
// its pre-existing "unprovable key → every value of the resolved object is
// a candidate" fallback (the same fallback the R1 STATUS_VISUALS test at
// ts-colors.test.mjs exercises deliberately). With only that general
// change, a NON-canonical, purely local finite object (`const LOCAL =
// {'color.a': '#112233'}; LOCAL[id]`) does NOT stay unsupported once
// reachable — it resolves to ts-colors/raw-color at each property, same as
// the canonical case would. That is sound in general (a module-const/
// finite-palette value must always resolve, never become an exception —
// R1 design note bullet 1) but it defeats the FORBIDDEN requirement that
// non-canonical/local/parameter shapes stay unsupported. Matching the
// WHOLE call site atomically avoids the conflict: every non-governed shape
// (wrong module, local object literal, bare parameter, an unrecognized
// String method, a mixed literal return) falls straight through to the
// pre-existing withCalleeFrame/collectReturns handling immediately below,
// which for `<x>.toUpperCase()` is emitUnsupported when `<x>` doesn't
// resolve any other way. This is a pure ADDITION: every existing return
// path is reached exactly as before unless ALL of a matching function's
// return expressions independently prove governed.
export function isGeneratedTokenAccessorCall(call, ctx, stack) {
  const declaration = calleeDeclaration(call, ctx, stack)
  if (!isLocalFunction(declaration)) return false
  const returns = collectReturns(declaration)
  if (!returns.length) return false
  return returns.every((expr) => generatedTokenAccessorReturnIsGoverned(expr, declaration, ctx))
}

// A governed return is either the bare identifier bound to a governed
// element-access read (see isGovernedTokenIdentifier), or that same
// identifier wrapped in exactly one content-preserving String method call
// (STRING_TRIM_METHODS / STRING_CASE_METHODS — never any other method,
// which could introduce content the token pipeline never produced).
export function generatedTokenAccessorReturnIsGoverned(expr, declaration, ctx) {
  let target = unwrap(expr)
  if (ts.isCallExpression(target) && target.arguments.length === 0) {
    const callee = unwrap(target.expression)
    if (ts.isPropertyAccessExpression(callee) && (STRING_TRIM_METHODS.has(callee.name.text) || STRING_CASE_METHODS.has(callee.name.text))) {
      target = unwrap(callee.expression)
    }
  }
  return ts.isIdentifier(target) && isGovernedTokenIdentifier(target, declaration, ctx)
}

// True when `identNode` names a `const` declared, in the SAME function body
// as `declaration`, directly by an element access (`values[id]`, any key —
// literal or dynamic, it does not matter which) whose BASE resolves — via
// resolveGeneratedTokenModulePath below — to an export of a module on
// CANONICAL_GENERATED_TOKEN_PATHS.
export function isGovernedTokenIdentifier(identNode, declaration, ctx) {
  const local = findLocalConstDeclaration(declaration.body, identNode.text)
  if (!local || !local.initializer) return false
  const initializer = unwrap(local.initializer)
  if (!ts.isElementAccessExpression(initializer)) return false
  const base = unwrap(initializer.expression)
  if (!ts.isIdentifier(base)) return false
  const modulePath = resolveGeneratedTokenModulePath(base, ctx)
  return modulePath !== null && CANONICAL_GENERATED_TOKEN_PATH_SET.has(modulePath)
}

// Finds the `const <name> = …` VariableDeclaration textually inside `body`
// (a function Block), never descending into a nested function's own scope —
// a same-named local inside a closure the outer function merely defines,
// but never itself reads before returning, must not be mistaken for this
// function's own binding.
export function findLocalConstDeclaration(body, name) {
  if (!body || !ts.isBlock(body)) return null
  let found = null
  const visit = (node) => {
    if (found || isFunctionLike(node)) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name
      && node.parent && ts.isVariableDeclarationList(node.parent) && (node.parent.flags & ts.NodeFlags.Const)) {
      found = node
      return
    }
    ts.forEachChild(node, visit)
  }
  ts.forEachChild(body, visit)
  return found
}

// Resolves `ident` to a canonical-token-module path when it is either (a) a
// named import, or (b) a `const` alias — through an `as`/type-assertion cast
// or plain reassignment-free binding, any number of hops, each hop still
// required to be `const` — of one. Anything else (a parameter, a `let`, an
// object/array literal, an opaque call) returns null: the ONLY two shapes
// that can carry this proof are "this name IS the import" and "this name IS
// a const alias of a name that, recursively, resolves the same way."
export function resolveGeneratedTokenModulePath(ident, ctx, seen = new Set()) {
  const sourceFile = ident.getSourceFile()
  const marker = `${sourceFile.fileName}#${ident.text}`
  if (seen.has(marker)) return null
  seen.add(marker)
  const record = ctx.sourceRecords.get(sourceFile) ?? indexModuleRecord(sourceFile.fileName, sourceFile)
  const name = ident.text
  const imported = record.imports.get(name)
  if (imported) return governedModulePath(record.path, imported.specifier, ctx.modules)
  const declaration = record.declarations.get(name)
  if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer
    && declaration.parent && ts.isVariableDeclarationList(declaration.parent) && (declaration.parent.flags & ts.NodeFlags.Const)) {
    const target = unwrap(declaration.initializer)
    if (ts.isIdentifier(target)) return resolveGeneratedTokenModulePath(target, ctx, seen)
  }
  return null
}

// ── Status-contract governed-record capability ───────────────────────────────
//
// Closes the ts-colors/unsupported regression that surfaces the moment
// hand-written status hexes are replaced with reads of the design
// system's own governed record: `import { statusContract } from
// '@/design-system/status'; STATUS_COLORS = { inbox: statusContract.inbox
// .resolvedColor, ... }`. `statusContract` (src/design-system/status.ts) is
// `Object.freeze({ inbox: status('inbox', ...), next: status('next', ...),
// ... })`, where `status()`'s own `resolvedColor` field is bound to
// `generatedColor(...)` — a call this file ALREADY proves governed via the
// generated-token-accessor capability above (isGeneratedTokenAccessorCall /
// generatedTokenAccessorReturnIsGoverned / resolveGeneratedTokenModulePath).
// This capability re-verifies that exact chain structurally, reusing those
// three functions rather than writing a second colour-provenance proof, and
// adds nothing beyond walking TWO extra, fixed hops (statusContract's own
// object literal, then status()'s own return object literal) to reach the
// same already-trusted call:
//   1. The read must be a plain, non-computed `<base>.<statusKey>.<finalProp>`
//      PropertyAccessExpression chain — never `statusContract['inbox']` or
//      any ElementAccessExpression at either hop.
//   2. `<base>` must resolve — via resolveGeneratedTokenModulePath's own
//      import/const-alias walk, unchanged — to a NAMED import of
//      `statusContract` (checked by IMPORT NAME, not module path alone, so
//      a renamed import of some OTHER export from that module never
//      qualifies) from STATUS_CONTRACT_MODULE_PATH. A same-named LOCAL
//      object literal (`const statusContract = {...}`) is never trusted:
//      it has no import to resolve, so this fails closed exactly like the
//      generated-token-accessor capability's own module-path check does for
//      a look-alike local finite object (see that capability's doc comment,
//      "Probed directly").
//   3. `<statusKey>` must be an actual property of statusContract's own
//      object-literal initializer (after unwrapping the ONE Object.freeze
//      wrapper — never any other call), whose value is a call to the
//      module-local `status(...)` builder — never a hard-coded assumption
//      that every property of a record named `statusContract` is safe.
//   4. `<finalProp>` must resolve, inside THAT status() call's own single
//      return object literal (again unwrapping exactly one Object.freeze),
//      to a value that is EITHER a bare identifier bound (by a local `const`
//      in status()'s own body) to a further call this file independently
//      proves governed via isGeneratedTokenAccessorCall — reusing that
//      capability exactly as it already exists, not a copy of it — OR a
//      bare identifier bound directly to a governed `<tokens>[id]` element
//      access (isGovernedTokenIdentifier's own shape, reused the same way).
//      `resolvedColor` (bound to `generatedColor(...)`) satisfies this;
//      `label`/`nonColorCue` (bound to a function PARAMETER, no local const
//      at all) and `tokens`/`contrast` (bound to `compositeOver(...)` calls
//      that do real colour MATH, not a governed accessor) do not — so this
//      is a genuine structural proof of "this member is provably a governed
//      colour", never a hard-coded trust of the name "resolvedColor" alone.
// Any failure at any step falls through UNCHANGED to the pre-existing
// resolveMemberTargets/emitUnsupported handling at this call site — a wrong
// key, an unresolvable base, a differently-named final property, or a
// non-colour member (bullet 4) keeps today's conservative behaviour.
//
// Deliberately does NOT reach through calleeDeclaration/resolveIdentInit
// (which key off the LIVE scope stack, `ctx.scopes` — correct for the file
// currently being walked, but wrong for status.ts's own top-level
// declarations when reached this way from an arbitrary consumer file, where
// a same-named local binding in the CONSUMER could otherwise be
// misattributed). Every lookup here is purely structural — module-record
// declarations/imports, keyed by source file — exactly like
// resolveGeneratedTokenModulePath and isGovernedTokenIdentifier already are.
export function isStatusContractColorRead(node, ctx) {
  const finalKey = node.name.text
  const mid = unwrap(node.expression)
  if (!ts.isPropertyAccessExpression(mid)) return false
  const statusKey = mid.name.text
  const base = unwrap(mid.expression)
  if (!ts.isIdentifier(base)) return false
  if (!resolvesToStatusContractImport(base, ctx)) return false
  return statusContractMemberIsGovernedColor(statusKey, finalKey, ctx)
}

// Mirrors resolveGeneratedTokenModulePath's own import/const-alias walk
// exactly, with one addition: the FIRST hop must be a named import whose
// EXPORTED name (not just local alias) is literally `statusContract` —
// resolveGeneratedTokenModulePath alone only proves the MODULE, which would
// wrongly accept a renamed import of some other export from that same file.
export function resolvesToStatusContractImport(ident, ctx, seen = new Set()) {
  const sourceFile = ident.getSourceFile()
  const marker = `${sourceFile.fileName}#${ident.text}`
  if (seen.has(marker)) return false
  seen.add(marker)
  const record = ctx.sourceRecords.get(sourceFile) ?? indexModuleRecord(sourceFile.fileName, sourceFile)
  const name = ident.text
  const imported = record.imports.get(name)
  if (imported) {
    return imported.imported === STATUS_CONTRACT_EXPORT_NAME
      && governedModulePath(record.path, imported.specifier, ctx.modules) === STATUS_CONTRACT_MODULE_PATH
  }
  const declaration = record.declarations.get(name)
  if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer
    && declaration.parent && ts.isVariableDeclarationList(declaration.parent) && (declaration.parent.flags & ts.NodeFlags.Const)) {
    const target = unwrap(declaration.initializer)
    if (ts.isIdentifier(target)) return resolvesToStatusContractImport(target, ctx, seen)
  }
  return false
}

// Strips exactly one `Object.freeze(...)` wrapper (status.ts wraps both
// `statusContract` itself and each status() return value this way) — this
// file's ordinary `unwrap` deliberately does not do this generally (it would
// let ANY frozen record resolve as a plain object literal everywhere,
// widening every OTHER capability in this file that consults `unwrap`); this
// capability alone needs it, so it strips it locally instead.
export function unwrapObjectFreezeCall(node) {
  const core = unwrap(node)
  if (ts.isCallExpression(core) && ts.isPropertyAccessExpression(core.expression)
    && ts.isIdentifier(core.expression.expression) && core.expression.expression.text === 'Object'
    && core.expression.name.text === 'freeze' && core.arguments.length === 1) {
    return unwrap(core.arguments[0])
  }
  return core
}

// Bullets 3-4 of isStatusContractColorRead's doc comment: resolves
// STATUS_CONTRACT_MODULE_PATH's own `statusContract` and `status(...)`
// declarations directly from its module record (never via ctx.scopes) and
// proves `<statusKey>.<finalKey>` governed via the existing generated-token-
// accessor machinery.
export function statusContractMemberIsGovernedColor(statusKey, finalKey, ctx) {
  const record = moduleRecord(ctx, STATUS_CONTRACT_MODULE_PATH)
  if (!record) return false
  const contractDeclaration = record.declarations.get(STATUS_CONTRACT_EXPORT_NAME)
  if (!contractDeclaration || !ts.isVariableDeclaration(contractDeclaration) || !contractDeclaration.initializer) return false
  const contractObject = unwrapObjectFreezeCall(contractDeclaration.initializer)
  if (!contractObject || !ts.isObjectLiteralExpression(contractObject) || contractObject.properties.some(opaqueObjectMember)) return false
  const entryValue = propertyInit(contractObject, statusKey)
  if (!entryValue) return false
  const call = unwrap(entryValue)
  if (!ts.isCallExpression(call)) return false
  const callee = unwrap(call.expression)
  if (!ts.isIdentifier(callee) || callee.text !== STATUS_CONTRACT_BUILDER_NAME) return false
  const builderDeclaration = record.declarations.get(STATUS_CONTRACT_BUILDER_NAME)
  if (!builderDeclaration || !ts.isFunctionDeclaration(builderDeclaration) || !builderDeclaration.body) return false
  const returns = collectReturns(builderDeclaration)
  if (!returns.length) return false
  return returns.every((expr) => statusBuilderReturnIsGovernedColor(expr, finalKey, builderDeclaration, ctx))
}

export function statusBuilderReturnIsGovernedColor(returnExpr, finalKey, builderDeclaration, ctx) {
  const returnObject = unwrapObjectFreezeCall(returnExpr)
  if (!returnObject || !ts.isObjectLiteralExpression(returnObject) || returnObject.properties.some(opaqueObjectMember)) return false
  const value = propertyInit(returnObject, finalKey)
  if (!value) return false
  return statusBuilderValueIsGovernedColor(value, builderDeclaration, ctx)
}

// `value` is a member of status()'s own return object literal (e.g. the bare
// identifier `color` for `resolvedColor: color`). Governed only when it
// resolves — through a local `const` declared directly in the SAME status()
// body (never a function PARAMETER like `label`/`nonColorCue`, which
// findLocalConstDeclaration structurally cannot find) — to a call this file
// already proves governed via isGeneratedTokenAccessorCall, or directly to a
// governed `<tokens>[id]` element access via isGovernedTokenIdentifier's own
// shape. Reuses both capabilities exactly as written; adds no new colour-
// provenance proof of its own.
export function statusBuilderValueIsGovernedColor(value, builderDeclaration, ctx) {
  const target = unwrap(value)
  if (!ts.isIdentifier(target)) return false
  if (isGovernedTokenIdentifier(target, builderDeclaration, ctx)) return true
  const local = findLocalConstDeclaration(builderDeclaration.body, target.text)
  if (!local || !local.initializer) return false
  const initializer = unwrap(local.initializer)
  if (!ts.isCallExpression(initializer)) return false
  const callee = unwrap(initializer.expression)
  if (!ts.isIdentifier(callee)) return false
  const calleeDeclaration = ctx.sourceRecords.get(builderDeclaration.getSourceFile())?.declarations.get(callee.text)
  if (!calleeDeclaration || !ts.isFunctionDeclaration(calleeDeclaration) || !calleeDeclaration.body) return false
  const returns = collectReturns(calleeDeclaration)
  if (!returns.length) return false
  return returns.every((expr) => generatedTokenAccessorReturnIsGoverned(expr, calleeDeclaration, ctx))
}

// True for the literal `null` keyword or the `undefined` identifier — the
// two shapes a dispatcher/ternary/record candidate set can legitimately
// contribute NOTHING to a class/colour read (bullet 2, null branches), never
// any other unprovable value.
export function isNullOrUndefinedLiteral(node) {
  return node.kind === ts.SyntaxKind.NullKeyword || (ts.isIdentifier(node) && node.text === 'undefined')
}
