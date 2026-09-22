#!/usr/bin/env node
// ts-colors/value-analysis.mjs
//
// Member/array-literal resolution (resolveMemberTargets and friends) and
// the CSS-value/Tailwind-class-token text analysis (analyzeCssValue,
// analyzeClassToken, scanEmbeddedSvg) plus the finding-emission primitives
// (emit and friends). See scope-and-jsx.mjs's header for why this file
// cycles with its three siblings (scanEmbeddedSvg calls back into
// scope-and-jsx.mjs's walk) and why that is safe here.

'use strict'

import ts from 'typescript'
import postcss from 'postcss'
import {
  CLASS_BUILDERS,
  COLOR_FUNCS,
  GROUP_FUNCS,
  LOOKUP_MISSING,
  LOOKUP_UNBOUND,
  NAMED_COLORS,
  PERMITTED_KEYWORDS,
  SYSTEM_COLORS,
  TAILWIND_PALETTES,
  TAILWIND_PREFIXES,
  isFunctionLike,
  isParameterBinding,
  lookup,
  prepareSource,
  printer,
  unwrap,
  walk,
} from './scope-and-jsx.mjs'
import {
  absenceValue,
  isColorPropertyName,
  knownClassExportUsesSafe,
  opaqueObjectMember,
  propertyNameOf,
} from './style-and-purity.mjs'
import {
  ARRAY_MUTATING_METHODS,
  POSITIONAL_UNSTABLE_INITS,
  collectReturns,
  isNullOrUndefinedLiteral,
  resolveIdentInit,
  withCalleeFrame,
} from './binding-resolution.mjs'

// `Object.keys(<record>)[<literal index>]` (CreateAgentWizard.tsx's
// `defaultColorHex = Object.keys(AVATAR_COLORS_BY_NAME)[0]`) — resolves to
// the KEY (not the value) at that literal position, across every
// statically-resolvable candidate record, when every one of that record's
// OWN properties is a plain, literal-keyed, non-spread PropertyAssignment
// in a PROVABLE declaration order — the exact ordering `Object.keys` itself
// iterates in at runtime. A spread or an unprovable computed key ANYWHERE
// in the record could shift what actually lands at this index, so any one
// voids the WHOLE record's proof (never guessed, never partial) — the same
// completeness standard opaqueObjectMember already enforces for a plain
// property read. Only a literal NumericLiteral index and a literal
// `Object.keys(...)` receiver are accepted — the same "provable AST shape
// or fail closed" standard as everywhere else in this file. Returns the
// KEY's own name node (itself a StringLiteral for AVATAR_COLORS_BY_NAME's
// hex-keyed record) so the caller's ordinary literal-value dispatch handles
// it identically to any other resolved target — never a shorthand or
// non-PropertyAssignment member, which carries no independent literal key
// node this function could safely hand back.
export function resolveObjectKeysElementAccess(node, ctx, stack) {
  if (!ts.isElementAccessExpression(node)) return null
  const keyExpr = unwrap(node.argumentExpression)
  if (!ts.isNumericLiteral(keyExpr)) return null
  const receiver = unwrap(node.expression)
  if (!ts.isCallExpression(receiver) || receiver.arguments.length !== 1) return null
  const callee = unwrap(receiver.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ts.isIdentifier(callee.expression)
    || callee.expression.text !== 'Object' || callee.name.text !== 'keys') return null
  const objects = resolveToObjects(receiver.arguments[0], ctx, stack)
  if (!objects.length) return null
  const index = Number(keyExpr.text)
  const names = []
  for (const obj of objects) {
    if (obj.properties.some(opaqueObjectMember)) return null
    if (index < 0 || index >= obj.properties.length) return null
    const prop = obj.properties[index]
    if (!ts.isPropertyAssignment(prop) || propertyNameOf(prop.name) === null) return null
    names.push(prop.name)
  }
  return names
}

export function resolveMember(node, ctx, stack) {
  node = unwrap(node)
  if (ts.isPropertyAccessExpression(node)) {
    const obj = resolveToObject(unwrap(node.expression), ctx, stack)
    if (!obj) return null
    return propertyInit(obj, node.name.text)
  }
  if (ts.isElementAccessExpression(node)) {
    const keyExpr = unwrap(node.argumentExpression)
    if (!(ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr))) return null
    const obj = resolveToObject(unwrap(node.expression), ctx, stack)
    if (!obj) return null
    return propertyInit(obj, keyExpr.text)
  }
  return null
}

export function resolveMemberTargets(node, ctx, stack, resolution = null, nullAllowed = false) {
  const target = resolution ? null : resolveMember(node, ctx, stack)
  if (target) return [target]
  node = unwrap(node)
  if (!ts.isElementAccessExpression(node) && !ts.isPropertyAccessExpression(node)) return []
  // Graph-visuals capability (bullet 4): `Object.fromEntries(Object.keys(X)
  // .map((key) => [key, <templateValue>]))` — the exact shape
  // `STATUS_VISUALS`/`taskGraph.ts` builds its per-status record with. Every
  // entry shares the SAME syntactic template value (only the KEY varies at
  // runtime), so a read at ANY key — literal ("STATUS_VISUALS.inbox") or
  // dynamic ("STATUS_VISUALS[status]") — resolves to that one template,
  // exactly like this file's existing unprovable-ELEMENT-ACCESS-key
  // fallback below already treats every value of an ordinary record as a
  // candidate regardless of the key requested; recognizing this constructor
  // shape only ADDS resolvability for it, never changes what an unprovable
  // key resolves to on a plain object literal.
  const derivedEntries = resolveObjectFromEntriesRecord(unwrap(node.expression), ctx, stack)
  if (derivedEntries) return derivedEntries
  // CreateAgentWizard.tsx's `Object.keys(AVATAR_COLORS_BY_NAME)[0]` — the
  // module-consts-palette capability (R1): resolves to the KEY at that
  // literal position, never a value this file has any other way to reach
  // (resolveObjectKeysElementAccess's own doc comment covers the full
  // soundness argument).
  const keysIndexEntries = resolveObjectKeysElementAccess(node, ctx, stack)
  if (keysIndexEntries) return keysIndexEntries
  if (ts.isElementAccessExpression(node)) {
    const arrayResolution = { incomplete: false }
    const arrays = resolveToArrays(unwrap(node.expression), ctx, stack, arrayResolution, nullAllowed)
    if (arrays.length) {
      if (resolution && arrayResolution.incomplete) resolution.incomplete = true
      const keyExpr = unwrap(node.argumentExpression)
      if (ts.isNumericLiteral(keyExpr)) {
        const index = Number(keyExpr.text)
        return arrays.map((array) => array.elements[index]).filter(Boolean)
      }
      return arrays.flatMap((array) => [...array.elements].filter((element) => !ts.isOmittedExpression(element)))
    }
  }
  const objects = resolveToObjects(unwrap(node.expression), ctx, stack, resolution, nullAllowed)
  // See resolveDestructuredTargets' matching comment: a computed key that
  // statically resolves to a literal name is not opaque (P11 review
  // finding) — only a spread/method/getter member, or a computed key with
  // no provable literal name, still voids the whole-object completeness
  // proof.
  if (resolution && objects.some(obj => obj.properties.some(opaqueObjectMember))) resolution.incomplete = true
  const member = (obj, key) => {
    const value = propertyInit(obj, key)
    if (resolution && (!value && !absenceValue(obj, key, ctx))) resolution.incomplete = true
    return value
  }
  if (ts.isPropertyAccessExpression(node)) return objects.map(obj => member(obj, node.name.text)).filter(Boolean)
  const keyExpr = unwrap(node.argumentExpression)
  if (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr)) return objects.map(obj => member(obj, keyExpr.text)).filter(Boolean)
  return objects.flatMap((obj) => obj.properties.filter(ts.isPropertyAssignment).map((prop) => prop.initializer))
}

export function resolveToArrays(node, ctx, stack, resolution = null, nullAllowed = false) {
  node = unwrap(node)
  if (ts.isArrayLiteralExpression(node)) return [node]
  if (isNullOrUndefinedLiteral(node)) {
    if (!nullAllowed && resolution) resolution.incomplete = true
    return []
  }
  if (ts.isIdentifier(node)) {
    // Position-keyed, not text-keyed — see inspectClassExpr's identifier
    // marker for the shadowing rationale (nested-helper parameter-frame fix).
    const marker = `${node.getSourceFile().fileName}#array:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const init = resolveIdentInit(node, ctx, next)
      if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToArrays(init, ctx, next, resolution, nullAllowed)
    }
  }
  if (ts.isConditionalExpression(node)) return [...resolveToArrays(node.whenTrue, ctx, stack, resolution, nullAllowed), ...resolveToArrays(node.whenFalse, ctx, stack, resolution, nullAllowed)]
  if (resolution) resolution.incomplete = true
  return []
}

export function resolveToObjects(node, ctx, stack, resolution = null, nullAllowed = false) {
  node = unwrap(node)
  if (ts.isObjectLiteralExpression(node)) return [node]
  // Null-branch capability (bullet 2): a literal null/undefined candidate in
  // a dispatcher/ternary/record contributes NOTHING to the read rather than
  // voiding the whole proof, but ONLY when the caller has already proven the
  // outer read cannot observe it (isProvenNonNullReceiver at the read site);
  // otherwise this is exactly as unprovable as any other missing branch.
  if (isNullOrUndefinedLiteral(node)) {
    if (!nullAllowed && resolution) resolution.incomplete = true
    return []
  }
  if (ts.isIdentifier(node)) {
    // Position-keyed, not text-keyed — see inspectClassExpr's identifier
    // marker for the shadowing rationale (nested-helper parameter-frame fix).
    const marker = `${node.getSourceFile().fileName}#object:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const init = resolveIdentInit(node, ctx, next)
      // Finite-const-array-callback capability (bullet 3): `o` in
      // `STATUS_OPTIONS.filter(pred).map((o) => ({ ..., color: o.color }))`
      // — `o`'s candidate objects are STATUS_OPTIONS' own (conservatively
      // un-filtered) elements, each already an object literal.
      if (isParameterBinding(init) && init.arrayElements && !init.reassigned) {
        return init.arrayElements.flatMap(element => resolveToObjects(element, ctx, next, resolution, nullAllowed))
      }
      if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToObjects(init, ctx, next, resolution, nullAllowed)
    }
  }
  if (ts.isConditionalExpression(node)) return [...resolveToObjects(node.whenTrue, ctx, stack, resolution, nullAllowed), ...resolveToObjects(node.whenFalse, ctx, stack, resolution, nullAllowed)]
  if (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken)) return [...resolveToObjects(node.left, ctx, stack, resolution, nullAllowed), ...resolveToObjects(node.right, ctx, stack, resolution, nullAllowed)]
  if (ts.isCallExpression(node)) {
    const marker = `${node.getSourceFile().fileName}#object-call:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const framed = withCalleeFrame(node, ctx, next, (collected) => ({
        collected,
        resolved: collected.flatMap(value => resolveToObjects(value, ctx, next, resolution, nullAllowed)),
      }))
      if (framed?.collected?.length) return framed.resolved
      // Finite-const-array-.map()-derivation capability (bullet 3/BoardView
      // col.headerColor): `COLUMNS = STATUS_ORDER.map((status) => ({...}))`
      // — COLUMNS is not itself a literal array, so withCalleeFrame's
      // local-FUNCTION-call resolution above never applies to it (its
      // "callee" is a property access, `.map`, not an identifier). The
      // candidate objects are exactly the callback's own return
      // expressions — sound regardless of what the receiver array actually
      // contains, because any FURTHER read off a returned object (e.g.
      // `.headerColor` -> `STATUS_COLORS[status]`) independently re-proves
      // itself through the ordinary resolution chain the moment it is
      // inspected (the unprovable-dynamic-key enumeration fallback above,
      // for instance, never needed `status`'s own value to begin with).
      const derived = resolveDerivedMapElements(node)
      if (derived) return derived.flatMap(value => resolveToObjects(value, ctx, next, resolution, nullAllowed))
    }
  }
  if (ts.isElementAccessExpression(node) || ts.isPropertyAccessExpression(node)) {
    const targets = resolveMemberTargets(node, ctx, stack, resolution, nullAllowed)
    if (targets.length) return targets.flatMap(value => resolveToObjects(value, ctx, stack, resolution, nullAllowed))
  }
  if (resolution) resolution.incomplete = true
  return []
}

export function resolveToObject(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return null
  if (ts.isObjectLiteralExpression(node)) return node
  if (ts.isIdentifier(node)) {
    const init = resolveIdentInit(node, ctx, stack)
    if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToObject(init, ctx, stack)
  }
  return null
}

export function propertyInit(obj, name) {
  // Last match wins, matching real object-literal evaluation order (a later
  // property with the same key — including one written through a literal
  // computed key, `['color']` alongside `color:` — overrides an earlier
  // one). Required for the narrowed computed-key completeness checks below:
  // once a computed key that resolves to a literal name is allowed to
  // participate in the proof at all, this lookup MUST
  // resolve it in the same order the language does, or a later override
  // (safe or unsafe) could silently be missed in either direction.
  let result = null
  for (const prop of obj.properties) {
    if (ts.isShorthandPropertyAssignment(prop)) {
      if (prop.name.text === name) result = prop.name
      continue
    }
    if (!ts.isPropertyAssignment(prop)) continue
    const key = propertyNameOf(prop.name)
    if (key === name) result = prop.initializer
  }
  return result
}

// ── Bullet 3: finite const-array callback elements ─────────────────────────

export const ARRAY_ENUMERATION_METHODS = new Set(['map', 'forEach', 'find', 'some', 'every'])

// `.filter(predicate)` (ANY predicate) can only REMOVE elements from its
// receiver, never add one — the same reasoning ARRAY_RECEIVER_PRESERVING_METHODS
// already applies for a class-composing helper's own `.filter().join()`
// chain. Chaining through it before a `.map()`/`.forEach()`/... enumeration
// (`STATUS_OPTIONS.filter(pred).map((o) => ...)`) still leaves every
// resulting element a genuine member of the ORIGINAL array — a conservative
// superset of the filtered subset, safe to prove over in full.
export const ARRAY_NARROWING_METHODS = new Set(['filter'])

// A never-reassigned, never-mutated, never-unsafely-exported `const` array
// literal — the same escape/mutation guard standard this file already
// applies to a finite record (knownClassExportUsesSafe), plus an array-
// specific mutating-method scan mirroring composerParameterIsMutated's
// (push/pop/splice/...) guard, generalized from a parameter name to a
// top-level declaration name.
export function arrayLiteralIsStable(declaration, ctx) {
  if (!knownClassExportUsesSafe(declaration, ctx)) return false
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return true
  let mutated = false
  const visit = node => {
    if (mutated || !node) return
    if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
      const parent = node.parent
      if (ts.isBinaryExpression(parent) && parent.left === node
        && parent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && parent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { mutated = true; return }
      if (ts.isPropertyAccessExpression(parent) && parent.expression === node
        && ARRAY_MUTATING_METHODS.has(parent.name.text)
        && ts.isCallExpression(parent.parent) && parent.parent.expression === parent) { mutated = true; return }
      if (ts.isElementAccessExpression(parent) && parent.expression === node
        && ts.isBinaryExpression(parent.parent) && parent.parent.left === parent
        && parent.parent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && parent.parent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { mutated = true; return }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return !mutated
}

// Resolves `expr` to the elements of a finite, stable const array literal,
// optionally chained through one or more `.filter(...)` hops (which only
// ever narrow). Returns null for anything else — runtime data (props,
// state, a fetched array) is the user-authored-colour category, handled by
// registration, not resolved here.
export function resolveStableArrayLiteralElements(expr, ctx, stack) {
  expr = unwrap(expr)
  if (ts.isIdentifier(expr)) {
    const marker = `${expr.getSourceFile().fileName}#arraysrc:${expr.pos}`
    if (stack.has(marker)) return null
    const next = new Set(stack).add(marker)
    const init = resolveIdentInit(expr, ctx, next)
    if (POSITIONAL_UNSTABLE_INITS.has(init)) return null
    if (!init || init === LOOKUP_MISSING || init === LOOKUP_UNBOUND) return null
    const resolved = unwrap(init)
    if (ts.isArrayLiteralExpression(resolved)) {
      const declaration = resolved.parent
      if (!ts.isVariableDeclaration(declaration) || declaration.initializer !== resolved) return null
      if (!(declaration.parent.flags & ts.NodeFlags.Const)) return null
      if (resolved.elements.some(element => ts.isSpreadElement(element) || ts.isOmittedExpression(element))) return null
      if (!arrayLiteralIsStable(declaration, ctx)) return null
      return resolved.elements
    }
    // BoardView.tsx's COLUMNS = STATUS_ORDER.map((status) => ({...})): the
    // identifier's OWN initializer is a `.map()`-derivation, not a literal
    // array — recurse into the SAME CallExpression handling below rather
    // than failing closed, so `col` in `COLUMNS.map((col) => ...)` resolves
    // two derivation hops deep exactly like a direct `X.map(cb)` would.
    return resolveStableArrayLiteralElements(resolved, ctx, next)
  }
  if (ts.isCallExpression(expr)) {
    const callee = unwrap(expr.expression)
    if (!ts.isPropertyAccessExpression(callee)) return null
    if (ARRAY_NARROWING_METHODS.has(callee.name.text)) return resolveStableArrayLiteralElements(callee.expression, ctx, stack)
    // A `.map()`-derived array element (bullet 3 / BoardView col.headerColor):
    // no array-literal mutation guard applies here at all — there is no
    // `const X = [...]` literal to mutate in the first place, each element
    // is a FRESH object literal the callback constructs; see
    // resolveDerivedMapElements' own soundness note (mirrored in
    // resolveToObjects' CallExpression branch) for why no further
    // verification of the receiver is needed.
    if (callee.name.text === 'map') return resolveDerivedMapElements(expr)
    return null
  }
  return null
}

// `fn` is a callback whose FIRST parameter is bound at bindParams time —
// resolves the finite, stable const-array elements it enumerates when `fn`
// is passed directly as the sole relevant argument of `<array>.map(...)` /
// `.forEach(...)` / `.find(...)` / `.some(...)` / `.every(...)`, chained
// through any number of `.filter(...)` hops first.
export function resolveEnumerableArrayElements(fn, ctx) {
  const call = fn.parent
  if (!call || !ts.isCallExpression(call) || call.arguments[0] !== fn) return null
  const callee = unwrap(call.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ARRAY_ENUMERATION_METHODS.has(callee.name.text)) return null
  return resolveStableArrayLiteralElements(callee.expression, ctx, new Set())
}

// Graph-visuals derivation (bullet 4 / BoardView col.headerColor): `node` is
// `<expr>.map(callback)` — resolves to the callback's own RETURN
// expressions, regardless of what `<expr>` itself is. This is sound with no
// further verification of `<expr>` needed: any read later extracted off one
// of these returned objects (e.g. `.headerColor` -> `STATUS_COLORS[status]`)
// independently re-proves itself through the ordinary resolution chain the
// moment IT is inspected — an unresolvable nested read (e.g. a genuine
// runtime `.map()` element's OWN parameter used directly) fails exactly as
// closed as it would anywhere else in this file, because that parameter is
// simply out of scope by the time a SEPARATE read site inspects it.
export function resolveDerivedMapElements(node) {
  const callee = unwrap(node.expression)
  if (!ts.isPropertyAccessExpression(callee) || callee.name.text !== 'map') return null
  const callback = node.arguments[0]
  if (!callback || !isFunctionLike(callback) || !callback.body) return null
  return collectReturns(callback)
}

// Graph-visuals capability (bullet 4): recognizes `Object.fromEntries(
// Object.keys(<source>).map((key) => [key, <templateValue>]))` — the exact
// idiom `taskGraph.ts`'s STATUS_VISUALS uses to project one finite record
// (STATUS_COLORS) into another, keyed identically. `<source>`'s own
// identity does not matter (see resolveMemberTargets' call site comment);
// only the STRUCTURAL shape is checked, and only a single, unambiguous
// 2-element-tuple return per callback qualifies — anything else (multiple
// differently-shaped returns, a spread/computed tuple) returns null and the
// ordinary (unresolvable) path stays in charge.
export function resolveObjectFromEntriesRecord(node, ctx, stack) {
  node = unwrap(node)
  if (ts.isIdentifier(node)) {
    // The receiver is usually an IDENTIFIER bound to the fromEntries call
    // (`STATUS_VISUALS = Object.fromEntries(...)`), not the call itself —
    // resolve through it the same way every other member-access receiver in
    // this file does, one hop at a time (so `STATUS_VISUALS[status]` and,
    // transitively, `statusVisual(status)`'s OWN `STATUS_VISUALS[...] ??
    // STATUS_VISUALS.inbox` return both reach this recognizer).
    const marker = `${node.getSourceFile().fileName}#fromentries:${node.pos}`
    if (stack.has(marker)) return null
    const next = new Set(stack).add(marker)
    const init = resolveIdentInit(node, ctx, next)
    if (!init || init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || POSITIONAL_UNSTABLE_INITS.has(init)) return null
    return resolveObjectFromEntriesRecord(init, ctx, next)
  }
  if (!ts.isCallExpression(node)) return null
  const callee = unwrap(node.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ts.isIdentifier(callee.expression)
    || callee.expression.text !== 'Object' || callee.name.text !== 'fromEntries') return null
  const arg = node.arguments[0] ? unwrap(node.arguments[0]) : null
  if (!arg || !ts.isCallExpression(arg)) return null
  const mapCallee = unwrap(arg.expression)
  if (!ts.isPropertyAccessExpression(mapCallee) || mapCallee.name.text !== 'map') return null
  const keysReceiver = unwrap(mapCallee.expression)
  if (!ts.isCallExpression(keysReceiver)) return null
  const keysCallee = unwrap(keysReceiver.expression)
  if (!ts.isPropertyAccessExpression(keysCallee) || !ts.isIdentifier(keysCallee.expression)
    || keysCallee.expression.text !== 'Object' || keysCallee.name.text !== 'keys') return null
  const entryCallback = arg.arguments[0]
  if (!entryCallback || !isFunctionLike(entryCallback) || !entryCallback.body) return null
  const returns = collectReturns(entryCallback)
  if (!returns.length) return null
  const values = []
  for (const ret of returns) {
    const tuple = unwrap(ret)
    if (!ts.isArrayLiteralExpression(tuple) || tuple.elements.length < 2
      || tuple.elements.some(element => ts.isSpreadElement(element) || ts.isOmittedExpression(element))) return null
    values.push(tuple.elements[1])
  }
  return values
}

export function isClassBuilderCall(node, ctx) {
  return CLASS_BUILDERS.has(resolveCalleeName(node.expression, ctx))
}

export function resolveCalleeName(expr, ctx) {
  expr = unwrap(expr)
  if (ts.isIdentifier(expr)) {
    const init = lookup(ctx, expr.text)
    if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) {
      const unwrapped = unwrap(init)
      if (unwrapped && ts.isIdentifier(unwrapped) && unwrapped.text !== expr.text) {
        return resolveCalleeName(unwrapped, ctx)
      }
    }
    return expr.text
  }
  if (ts.isPropertyAccessExpression(expr)) return expr.name.text
  if (ts.isElementAccessExpression(expr)) {
    const arg = unwrap(expr.argumentExpression)
    if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) return arg.text
  }
  return ''
}

export function isIgnoredLiteral(node) {
  return (
    node.kind === ts.SyntaxKind.NullKeyword ||
    node.kind === ts.SyntaxKind.TrueKeyword ||
    node.kind === ts.SyntaxKind.FalseKeyword ||
    ts.isNumericLiteral(node)
  )
}

export function isSkipIdent(name) {
  return name === 'undefined' || name === 'NaN' || name === 'Infinity'
}

export function analyzeCssDeclarations(text, ctx, node) {
  const occurrence = { next: 0, prefix: 'css-decl' }
  for (const chunk of splitTopLevel(text, ';')) {
    const trimmed = chunk.trim()
    if (!trimmed) continue
    const colon = indexOfTopLevel(trimmed, ':')
    const value = colon === -1 ? trimmed : trimmed.slice(colon + 1)
    analyzeCssValue(value, ctx, node, occurrence)
  }
}

export function analyzeCssText(text, ctx, node) {
  let root
  try {
    root = postcss.parse(text)
  } catch {
    emitUnsupported(ctx, node)
    return
  }
  root.walkDecls((decl) => {
    const offset = decl.source?.start?.offset ?? 0
    analyzeCssValue(decl.value, ctx, node, { next: 0, prefix: `css:${offset}` })
  })
}

export function stripMatchingQuotes(text) {
  const trimmed = text.trim()
  if (
    trimmed.length >= 2
    && ((trimmed[0] === '"' && trimmed.endsWith('"')) || (trimmed[0] === "'" && trimmed.endsWith("'")))
  ) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

export function decodePercentStrict(text) {
  let out = ''
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index]
    if (char !== '%') {
      out += char
      continue
    }
    const hex = text.slice(index + 1, index + 3)
    if (!/^[0-9a-fA-F]{2}$/.test(hex)) return null
    out += String.fromCharCode(parseInt(hex, 16))
    index += 2
  }
  return out
}

export function decodeBase64Strict(text) {
  const compact = text.replace(/\s+/g, '')
  if (compact.length === 0 || compact.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(compact)) return null
  try {
    const decoded = Buffer.from(compact, 'base64')
    if (decoded.toString('base64').replace(/=+$/, '') !== compact.replace(/=+$/, '')) return null
    return decoded.toString('utf8')
  } catch {
    return null
  }
}

export function analyzeUrlFunction(fn, ctx, node) {
  const url = stripMatchingQuotes(fn.inner)
  if (!/^data:/i.test(url)) return
  analyzeDataUri(url, ctx, node)
}

export function analyzeDataUri(url, ctx, node) {
  const comma = url.indexOf(',')
  if (comma === -1) {
    emitUnsupported(ctx, node)
    return
  }
  const meta = url.slice(5, comma).trim().toLowerCase().replace(/\s+/g, '')
  const payload = url.slice(comma + 1)
  const mime = meta.split(';')[0] || ''
  if (mime !== 'image/svg+xml') {
    if (mime.startsWith('image/')) emitUnsupported(ctx, node)
    return
  }
  let markup
  try {
    markup = /(?:^|;)base64$/.test(meta) ? decodeBase64Strict(payload) : decodePercentStrict(payload)
  } catch {
    emitUnsupported(ctx, node)
    return
  }
  if (markup == null || !markup.includes('<')) {
    emitUnsupported(ctx, node)
    return
  }
  scanEmbeddedSvg(markup, ctx, node)
}

export function scanEmbeddedSvg(markup, ctx, originNode) {
  if ((ctx.embedDepth ?? 0) >= 2) {
    emitUnsupported(ctx, originNode)
    return
  }
  const prepared = prepareSource('embedded.svg', markup)
  const svgFile = ts.createSourceFile(ctx.path, prepared, ts.ScriptTarget.Latest, true, ts.ScriptKind.JSX)
  const nested = {
    path: ctx.path,
    sourceFile: svgFile,
    tokens: ctx.tokens,
    scopes: [new Map()],
    findings: ctx.findings,
    seen: ctx.seen,
    embedDepth: (ctx.embedDepth ?? 0) + 1,
  }
  const before = ctx.findings.length
  walk(svgFile, nested)
  const parseErrors = (svgFile.parseDiagnostics ?? []).filter(
    (diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error,
  )
  if (ctx.findings.length === before && parseErrors.length > 0) emitUnsupported(ctx, originNode)
}

export function analyzeClassString(text, ctx, node) {
  for (const token of splitClassTokens(text)) analyzeClassToken(token, ctx, node)
}

export function analyzeClassToken(token, ctx, node) {
  const utility = stripImportant(lastVariantSegment(token))
  if (!utility) return

  if (utility.startsWith('[') && utility.endsWith(']')) {
    const inner = utility.slice(1, -1)
    const colon = inner.indexOf(':')
    if (colon > 0) {
      const prop = inner.slice(0, colon)
      const value = inner.slice(colon + 1)
      if (isColorPropertyName(prop)) analyzeCssValue(decodeTwArbitrary(value), ctx, node)
    }
    return
  }

  if (isTailwindPaletteUtility(utility)) {
    emitRaw(ctx, node, token, `Default Tailwind palette utility "${token}" is not a design-system token.`)
    return
  }

  const arbitrary = prefixedArbitrary(utility)
  if (arbitrary !== null) analyzeCssValue(decodeTwArbitrary(arbitrary), ctx, node)
}

export function splitClassTokens(text) {
  const tokens = []
  let current = ''
  let depth = 0
  for (const char of text) {
    if (char === '[') depth += 1
    else if (char === ']' && depth > 0) depth -= 1
    if (/\s/.test(char) && depth === 0) {
      if (current) tokens.push(current)
      current = ''
      continue
    }
    current += char
  }
  if (current) tokens.push(current)
  return tokens
}

export function lastVariantSegment(token) {
  let current = ''
  let depth = 0
  let last = token
  for (const char of token) {
    if (char === '[') depth += 1
    else if (char === ']' && depth > 0) depth -= 1
    if (char === ':' && depth === 0) {
      last = ''
      current = ''
      continue
    }
    current += char
    last = current
  }
  return last || token
}

export function stripImportant(utility) {
  let value = utility
  if (value.startsWith('!')) value = value.slice(1)
  if (value.endsWith('!')) value = value.slice(0, -1)
  return value
}

export function isTailwindPaletteUtility(utility) {
  for (const prefix of TAILWIND_PREFIXES) {
    if (!utility.startsWith(`${prefix}-`)) continue
    const rest = utility.slice(prefix.length + 1)
    const [paletteAndShade] = rest.split('/')
    if (paletteAndShade === 'black' || paletteAndShade === 'white') return true
    const match = paletteAndShade.match(/^([a-z]+)-(\d{2,3})$/)
    if (match && TAILWIND_PALETTES.has(match[1])) return true
  }
  return false
}

export function prefixedArbitrary(utility) {
  for (const prefix of TAILWIND_PREFIXES) {
    if (utility.startsWith(`${prefix}-[`) && utility.endsWith(']')) {
      return utility.slice(prefix.length + 2, -1)
    }
  }
  return null
}

export function decodeTwArbitrary(value) {
  return value.replace(/_/g, ' ')
}

export function analyzeCssValue(text, ctx, node, occurrence = { next: 0, prefix: 'css' }) {
  const input = text.trim()
  if (!input) return
  let index = 0
  const length = input.length

  while (index < length) {
    while (index < length && /\s/.test(input[index])) index += 1
    if (index >= length) break

    if (startsWithWord(input, index, 'url') && input[index + 3] === '(') {
      const fn = readFunction(input, index)
      analyzeUrlFunction(fn, ctx, node)
      index = fn.end
      continue
    }

    if (startsWithWord(input, index, 'var') && input[index + 3] === '(') {
      const fn = readFunction(input, index)
      analyzeVar(fn.inner, ctx, node, occurrence)
      index = fn.end
      continue
    }

    const ident = readIdent(input, index)
    if (ident && input[index + ident.length] === '(') {
      const fn = readFunction(input, index)
      handleCssFunction(fn, ctx, node, occurrence)
      index = fn.end
      continue
    }

    if (input[index] === '#') {
      const hex = readHex(input, index)
      if (hex) {
        emitRaw(ctx, node, hex.toLowerCase(), undefined, `${occurrence.prefix}:${occurrence.next++}`)
        index += hex.length
        continue
      }
    }

    if (ident) {
      handleCssIdent(ident, ctx, node, occurrence)
      index += ident.length
      continue
    }

    if (input[index] === '"' || input[index] === "'") {
      index = skipQuoted(input, index)
      continue
    }

    index += 1
  }
}

export function handleCssFunction(fn, ctx, node, occurrence) {
  const name = fn.name.toLowerCase()
  if (COLOR_FUNCS.has(name)) {
    if (hasNumericColorChannels(fn.inner)) emitRaw(ctx, node, canonicalizeFunction(fn.full), undefined, `${occurrence.prefix}:${occurrence.next++}`)
    analyzeCssValue(fn.inner, ctx, node, occurrence)
    return
  }
  if (GROUP_FUNCS.has(name) || name.includes('gradient')) {
    analyzeCssValue(fn.inner, ctx, node, occurrence)
  }
}

export function handleCssIdent(ident, ctx, node, occurrence) {
  const lower = ident.toLowerCase()
  if (PERMITTED_KEYWORDS.has(lower) || SYSTEM_COLORS.has(lower)) return
  if (NAMED_COLORS.has(lower)) emitRaw(ctx, node, lower, undefined, `${occurrence.prefix}:${occurrence.next++}`)
}

export function analyzeVar(inner, ctx, node, occurrence) {
  const trimmed = inner.trim()
  const comma = indexOfTopLevel(trimmed, ',')
  const name = (comma === -1 ? trimmed : trimmed.slice(0, comma)).trim()
  const fallback = comma === -1 ? '' : trimmed.slice(comma + 1).trim()
  if (!name.startsWith('--')) {
    emitUnsupported(ctx, node)
  } else if (!ctx.tokens.has(name)) {
    emitUndefined(ctx, node, `var(${name})`, `${occurrence.prefix}:${occurrence.next++}`)
  }
  if (fallback) analyzeCssValue(fallback, ctx, node, occurrence)
}

export function hasNumericColorChannels(inner) {
  let args = splitTopLevel(inner, ',')
  if (args.length === 1) args = splitTopLevel(inner, ' \t\n\r')
  return args.some((arg) => {
    const trimmed = arg.trim()
    if (!trimmed || /^var\(/i.test(trimmed)) return false
    return /^-?\d/.test(trimmed)
  })
}

export function canonicalizeFunction(full) {
  return full.replace(/\s+/g, ' ').trim().replace(/^[A-Za-z-]+/, (name) => name.toLowerCase())
}

export function startsWithWord(text, index, word) {
  if (text.slice(index, index + word.length).toLowerCase() !== word) return false
  const next = text[index + word.length]
  return !next || !/[A-Za-z0-9_-]/.test(next)
}

export function readIdent(text, index) {
  if (index >= text.length) return null
  const first = text[index]
  if (first === '-' && text[index + 1] === '-') {
    let end = index + 2
    while (end < text.length && /[A-Za-z0-9_-]/.test(text[end])) end += 1
    return text.slice(index, end)
  }
  if (!/[A-Za-z_]/.test(first)) return null
  let end = index + 1
  while (end < text.length && /[A-Za-z0-9_-]/.test(text[end])) end += 1
  return text.slice(index, end)
}

export function readHex(text, index) {
  if (text[index] !== '#') return null
  let end = index + 1
  while (end < text.length && /[0-9a-fA-F]/.test(text[end])) end += 1
  const digits = end - index - 1
  if (digits === 3 || digits === 4 || digits === 6 || digits === 8) return text.slice(index, end)
  return null
}

export function readFunction(text, start) {
  let index = start
  while (index < text.length && /[A-Za-z0-9_-]/.test(text[index])) index += 1
  const name = text.slice(start, index)
  if (text[index] !== '(') return { name, inner: '', full: name, end: index }
  const close = skipBalanced(text, index)
  return {
    name,
    inner: text.slice(index + 1, close - 1),
    full: text.slice(start, close),
    end: close,
  }
}

export function skipBalanced(text, openIndex) {
  let depth = 0
  for (let index = openIndex; index < text.length; index += 1) {
    const char = text[index]
    if (char === '"' || char === "'") {
      index = skipQuoted(text, index) - 1
      continue
    }
    if (char === '(') depth += 1
    else if (char === ')') {
      depth -= 1
      if (depth === 0) return index + 1
    }
  }
  return text.length
}

export function skipQuoted(text, start) {
  const quote = text[start]
  let index = start + 1
  while (index < text.length) {
    if (text[index] === '\\') {
      index += 2
      continue
    }
    if (text[index] === quote) return index + 1
    index += 1
  }
  return text.length
}

export function splitTopLevel(text, separators) {
  const parts = []
  let depth = 0
  let start = 0
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index]
    if (char === '"' || char === "'") {
      index = skipQuoted(text, index) - 1
      continue
    }
    if (char === '(') depth += 1
    else if (char === ')') depth = Math.max(0, depth - 1)
    else if (depth === 0 && separators.includes(char)) {
      parts.push(text.slice(start, index))
      start = index + 1
    }
  }
  parts.push(text.slice(start))
  return parts
}

export function indexOfTopLevel(text, separator) {
  let depth = 0
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index]
    if (char === '"' || char === "'") {
      index = skipQuoted(text, index) - 1
      continue
    }
    if (char === '(') depth += 1
    else if (char === ')') depth = Math.max(0, depth - 1)
    else if (depth === 0 && char === separator) return index
  }
  return -1
}

export function emitRaw(ctx, node, syntax, message, occurrenceKey) {
  emit(ctx, node, {
    ruleId: 'ts-colors/raw-color',
    syntax,
    message: message ?? `Raw colour "${syntax}" is not a design-system token. Use var(--token) from the registered token set.`,
  }, occurrenceKey)
}

export function emitUndefined(ctx, node, syntax, occurrenceKey) {
  emit(ctx, node, {
    ruleId: 'ts-colors/undefined-token',
    syntax,
    message: `CSS variable "${syntax}" is not in the registered token set.`,
  }, occurrenceKey)
}

export function emitUnsupported(ctx, node) {
  // See scanHookStateLaundering: while silenced, an opaque/runtime argument
  // adds nothing extra (the read-site boundary already covers it) — but a
  // raw literal still reaches analyzeCssValue/emit('raw-color', ...)
  // unconditionally beforehand, since this function is never on that path.
  if (ctx.silentUnsupported) return
  emit(ctx, node, {
    ruleId: 'ts-colors/unsupported',
    syntax: printUnsupported(ctx.sourceFile, node),
    message: 'Unresolved dynamic colour expression; scanners must not treat this as safe.',
  })
}

export function emitExtensionBoundary(ctx, node, binding) {
  // classBoundaryOwner/classBoundaryName stand in for ownerName/name ONLY
  // when set (the class-like-parameter-forward and member-forward
  // capabilities) — every other existing caller (plain className forward,
  // style forward) passes a binding with neither field set and keeps
  // reporting ownerName/name exactly as before.
  const ownerName = binding.classBoundaryOwner ?? binding.ownerName
  const name = binding.classBoundaryName ?? binding.name
  const start = binding.declaration.getStart(ctx.sourceFile, false)
  const declaration = ctx.sourceFile.getLineAndCharacterOfPosition(start)
  emit(ctx, node, {
    ruleId: 'ts-colors/extension-boundary',
    syntax: `${ownerName}#${name}`,
    message: `Unchanged ${name} parameter forwarded from its declaration at ${declaration.line + 1}:${declaration.character + 1}; this remains blocking until its exact extension boundary is centrally reviewed.`,
  })
}

// Renamed class-like parameter MEMBER forward (bullet 1): `item.className`,
// `badge.className`, `iconProps?.className` — a bare/uniquely-bound
// parameter's OWN `.className`/`.class` member, read unchanged. Distinct
// from the plain-identifier forward above (which proves the IDENTIFIER
// itself is an unchanged forwarded value): here only ONE MEMBER of the
// parameter is read, so only the literal `className`/`class` key qualifies
// — unlike a same-named parameter, an arbitrary property name carries no
// naming signal that it is a CSS class at all (typography's
// parameterMemberBoundary contract, ported verbatim).
export function classForwardMemberBoundary(node, ctx) {
  const key = ts.isPropertyAccessExpression(node) ? node.name.text : elementAccessStringKey(node)
  if (key !== 'className' && key !== 'class') return null
  const base = unwrap(node.expression)
  if (!ts.isIdentifier(base)) return null
  const binding = lookup(ctx, base.text)
  if (!isParameterBinding(binding) || binding.reassigned) return null
  // Two provably-unchanged shapes qualify, matching typography's own
  // parameterMemberBoundary contract exactly: (1) a parameter of an
  // ANONYMOUS enclosing function (an array-callback element:
  // `items.map((item) => ... item.className ...)`) — "ownerNameFor alone...
  // does not resolve an anonymous callback"; (2) a NAMED component/function's
  // own DESTRUCTURED parameter FIELD (`iconProps` in a named
  // `RetryableState({ icon, iconProps, ... }) {...}`, WhatsAppPairingBody.tsx
  // — R1 lead addendum 2026-09-20, matching typography's classification of
  // this exact site as `RetryableState#iconProps.className`) — proven by
  // `ts.isBindingElement(binding.declaration)`: `iconProps` is itself ONE
  // NAMED field of a larger destructuring pattern, the identical contract a
  // destructured render-prop parameter or a `.map()` element already carries
  // elsewhere in this file. This does NOT widen to a NAMED function's WHOLE,
  // undestructured parameter (`props` in `Box = (props) => <div
  // className={props.className} />`) — `binding.declaration` there is the
  // bare `ts.ParameterDeclaration` itself, never a BindingElement, so it
  // stays excluded and keeps routing through the existing
  // hasStableRuntimeParameterRoot -> unverified-governed-value path exactly
  // as the pinned `Box#className<-props.className` fixture requires: an
  // arbitrary whole-props read carries no comparable "this one field is
  // meant to forward" signal that a dedicated, separately-named destructured
  // field does.
  if (binding.ownerName !== 'anonymous' && !ts.isBindingElement(binding.declaration)) return null
  const ownerName = binding.ownerName === 'anonymous' ? binding.memberOwnerName : binding.ownerName
  if (!ownerName || ownerName === 'anonymous') return null
  return { ownerName, name: `${base.text}.${key}`, declaration: binding.declaration }
}

export function elementAccessStringKey(node) {
  if (!ts.isElementAccessExpression(node)) return null
  const key = unwrap(node.argumentExpression)
  return key && (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key)) ? key.text : null
}

export function emitRuntimePaintBoundary(ctx, node, boundary) {
  const expression = printUnsupported(ctx.sourceFile, node)
  emit(ctx, node, {
    ruleId: 'ts-colors/extension-boundary',
    syntax: `${boundary.owner}#${boundary.receiver}.${boundary.property}<-${expression}`,
    message: `Runtime paint input for ${boundary.owner} ${boundary.receiver}.${boundary.property} remains blocking until this exact expression boundary is centrally reviewed.`,
  })
}

export function emitUnverifiedGovernedValue(ctx, node, boundary) {
  const expression = printUnsupported(ctx.sourceFile, node)
  emit(ctx, node, {
    ruleId: 'ts-colors/unverified-governed-value',
    syntax: `${boundary.owner}#${boundary.receiver}<-${expression}`,
    message: `The value reaching ${boundary.owner} ${boundary.receiver} is not statically proven to use registered design-system tokens.`,
  })
}

export function emitParse(ctx, diagnostic) {
  const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')
  const start = Math.min(diagnostic.start ?? 0, Math.max(0, ctx.sourceFile.end - 1))
  const pos = ctx.sourceFile.getLineAndCharacterOfPosition(start)
  emit(ctx, null, {
    ruleId: 'ts-colors/parse-error',
    syntax: message,
    message: `TypeScript parse error: ${message}`,
    line: pos.line + 1,
    column: pos.character + 1,
  })
}

export function emit(ctx, node, partial, occurrenceKey = '') {
  let line = partial.line
  let column = partial.column
  if (node && (line === undefined || column === undefined)) {
    const start = node.getStart(ctx.sourceFile, false)
    const pos = ctx.sourceFile.getLineAndCharacterOfPosition(start)
    line = pos.line + 1
    column = pos.character + 1
  }
  const finding = {
    ruleId: partial.ruleId,
    path: ctx.path,
    syntax: partial.syntax,
    message: partial.message,
  }
  if (Number.isInteger(line) && line > 0) finding.line = line
  if (Number.isInteger(column) && column > 0) finding.column = column
  const key = `${finding.ruleId}|${finding.syntax}|${finding.line ?? ''}|${finding.column ?? ''}|${occurrenceKey}`
  if (ctx.seen.has(key)) return
  ctx.seen.add(key)
  ctx.findings.push(finding)
}

export function printUnsupported(sourceFile, node) {
  if (ts.isSpreadAssignment(node) || ts.isSpreadElement(node)) {
    return `...${printCanonical(sourceFile, node.expression)}`
  }
  return printCanonical(sourceFile, node)
}

export function printCanonical(sourceFile, node) {
  return printer.printNode(ts.EmitHint.Unspecified, node, sourceFile).replace(/\s+/g, ' ').trim()
}

export function sortFindings(findings) {
  findings.sort((left, right) => {
    if (left.ruleId === 'ts-colors/parse-error' && right.ruleId !== 'ts-colors/parse-error') return -1
    if (right.ruleId === 'ts-colors/parse-error' && left.ruleId === 'ts-colors/parse-error') return 0
    if (right.ruleId === 'ts-colors/parse-error') return 1
    return (
      (left.line ?? 0) - (right.line ?? 0) ||
      (left.column ?? 0) - (right.column ?? 0) ||
      left.ruleId.localeCompare(right.ruleId) ||
      left.syntax.localeCompare(right.syntax)
    )
  })
}
