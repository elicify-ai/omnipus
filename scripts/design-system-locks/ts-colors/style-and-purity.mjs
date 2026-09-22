#!/usr/bin/env node
// ts-colors/style-and-purity.mjs
//
// Style/paint-object and className-expression inspection
// (inspectCssValueExpr, inspectClassExpr and the class-builder/cva family),
// plus the "provably never mutated/escaped" absence and export-purity
// proofs they depend on. See
// scope-and-jsx.mjs's header for why this file cycles with its three
// siblings and why that is safe here.

'use strict'

import ts from 'typescript'
import { extname, posix } from 'node:path'
import {
  ARRAY_ENUMERATION_METHODS,
  ARRAY_NARROWING_METHODS,
  analyzeClassString,
  analyzeCssDeclarations,
  analyzeCssValue,
  classForwardMemberBoundary,
  emitExtensionBoundary,
  emitRuntimePaintBoundary,
  emitUnsupported,
  emitUnverifiedGovernedValue,
  isClassBuilderCall,
  isIgnoredLiteral,
  isSkipIdent,
  resolveCalleeName,
  resolveMemberTargets,
} from './value-analysis.mjs'
import {
  ARRAY_RECEIVER_PRESERVING_METHODS,
  COLOR_PROPERTIES,
  IMPORT_BINDING,
  LOOKUP_MISSING,
  LOOKUP_UNBOUND,
  PARAMETER_BINDING,
  STRING_TRIM_METHODS,
  bindingValue,
  governedModulePath,
  isCanvasPaintName,
  isDirectRuntimeRead,
  isFunctionLike,
  isParameterBinding,
  isProvenNonNullReceiver,
  isRuntimeMeasurementBoundary,
  isStyleReceiver,
  lookup,
  moduleRecord,
  paintBoundary,
  resolveModuleDeclaration,
  setPropertyBoundary,
  unwrap,
} from './scope-and-jsx.mjs'
import {
  POSITIONAL_UNSTABLE_INITS,
  bodyDestructuredClassBoundary,
  destructuredSourceParameterBinding,
  functionReturns,
  hasStableRuntimeParameterRoot,
  hookStateLocal,
  inspectClassObjectKeys,
  inspectLiteralColorArguments,
  inspectLogical,
  inspectTemplate,
  inspectZodChainLiterals,
  isDefinitelyBoolean,
  isDefinitelyNumeric,
  isGeneratedTokenAccessorCall,
  isNumericSerialization,
  isStatusContractColorRead,
  resolveDestructuredTargets,
  resolveIdentInit,
  scanHookStateLaundering,
  tryString,
  withCalleeFrame,
} from './binding-resolution.mjs'

export function inspectSetPropertyCall(call, ctx) {
  if (resolveCalleeName(call.expression, ctx) !== 'setProperty') return
  const callee = unwrap(call.expression)
  const receiver = ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee)
    ? callee.expression
    : null
  if (!isStyleReceiver(receiver)) return
  if (call.arguments.length < 2) return
  const propNode = unwrap(call.arguments[0])
  const valueNode = call.arguments[1]
  const prop = tryString(propNode, ctx, new Set())
  if (prop === null) {
    emitUnsupported(ctx, propNode)
    inspectCssValueExpr(valueNode, ctx, new Set())
    return
  }
  if (prop.startsWith('--') || isColorPropertyName(prop) || isCanvasPaintName(prop)) {
    // Unlike every other paint sink in this file
    // (inspectStyleObject, inspectPaintAssignment, the JSX colour-attribute
    // branch), this call never constructed a boundary at all — so a
    // genuinely runtime custom-property write (a live layout measurement,
    // an adjustable root font size) had no way to ever become anything but
    // permanently-unsupported, even once fully proven unresolvable.
    inspectCssValueExpr(valueNode, ctx, new Set(), setPropertyBoundary(call, prop))
  }
}

export function inspectStyleExpr(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return
  if (ts.isObjectLiteralExpression(node)) {
    inspectStyleObject(node, ctx, stack, true)
    return
  }
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    analyzeCssDeclarations(node.text, ctx, node)
    return
  }
  if (ts.isTemplateExpression(node)) {
    inspectTemplate(node, ctx, 'css-decl', stack)
    return
  }
  if (ts.isIdentifier(node)) {
    if (isSkipIdent(node.text)) return
    const init = resolveIdentInit(node, ctx, stack)
    if (isParameterBinding(init)) {
      // Mirror of inspectClassExpr's forwardClassName → emitExtensionBoundary
      // path: a `style` parameter, forwarded UNCHANGED and passed straight
      // into a `style={…}` JSX attribute, is caller pass-through —
      // the caller's own argument was already validated at ITS call site.
      // Reassigned or anonymous-owner bindings keep the conservative
      // unsupported fallback, same as className.
      if (init.forwardStyle && !init.reassigned && init.ownerName !== 'anonymous') emitExtensionBoundary(ctx, node, init)
      else emitUnsupported(ctx, node)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      emitUnsupported(ctx, node)
      return
    }
    inspectStyleExpr(init, ctx, stack)
    return
  }
  if (ts.isConditionalExpression(node)) {
    inspectStyleExpr(node.whenTrue, ctx, stack)
    inspectStyleExpr(node.whenFalse, ctx, stack)
    return
  }
  if (ts.isCallExpression(node)) {
    // MessageItem.tsx's `style={avatarStyle(isUser, agent?.color)}` —
    // mirrors inspectCssValueExpr's own CallExpression handling (same
    // withCalleeFrame/collectReturns machinery): a LOCAL helper's own
    // returned style object(s) are exactly as reviewable as an inline
    // object literal would be. This never trusts the call's return value
    // as safe text — it recurses into each returned expression through
    // this SAME function, so an opaque/unresolvable branch still fails
    // exactly as closed as it would inline. The callee's own body is
    // independently walked regardless (the blanket object-literal walk in
    // inspectNode covers it whether or not it is ever called), so this
    // only removes a redundant whole-call "unsupported" that duplicated
    // what the callee's own internal boundary/raw findings already report.
    const outcome = withCalleeFrame(node, ctx, stack, (returns) => {
      for (const returned of returns) inspectStyleExpr(returned, ctx, stack)
      return true
    })
    if (outcome !== true) emitUnsupported(ctx, node)
    return
  }
  emitUnsupported(ctx, node)
}

export function inspectStyleObject(obj, ctx, stack, strictBoundary) {
  for (const prop of obj.properties) {
    if (ts.isSpreadAssignment(prop)) {
      if (strictBoundary) inspectSpreadStyle(prop, ctx, stack)
      continue
    }
    if (ts.isShorthandPropertyAssignment(prop)) {
      if (isColorPropertyName(prop.name.text)) inspectCssValueExpr(prop.name, ctx, stack, paintBoundary(prop, 'dom-style', prop.name.text))
      continue
    }
    if (!ts.isPropertyAssignment(prop)) continue
    const key = propertyNameOf(prop.name)
    if (key === null) {
      if (strictBoundary) {
        emitUnsupported(ctx, prop.name)
        inspectCssValueExpr(prop.initializer, ctx, stack)
      }
      continue
    }
    if (isColorPropertyName(key)) inspectCssValueExpr(prop.initializer, ctx, stack, paintBoundary(prop, 'dom-style', key))
  }
}

export function inspectSpreadStyle(prop, ctx, stack) {
  const expr = unwrap(prop.expression)
  if (ts.isObjectLiteralExpression(expr)) {
    inspectStyleObject(expr, ctx, stack, true)
    return
  }
  if (ts.isIdentifier(expr)) {
    const init = resolveIdentInit(expr, ctx, stack)
    if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) {
      const unwrapped = unwrap(init)
      if (ts.isObjectLiteralExpression(unwrapped)) {
        inspectStyleObject(unwrapped, ctx, stack, true)
        return
      }
    }
  }
  emitUnsupported(ctx, prop)
}

export function propertyNameOf(name) {
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) return name.text
  if (ts.isStringLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name) || ts.isNumericLiteral(name)) {
    return name.text
  }
  if (ts.isComputedPropertyName(name)) {
    const expr = unwrap(name.expression)
    if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) return expr.text
  }
  return null
}

// A member is "opaque" for the whole-object completeness proofs above when
// ECMAScript could make its later evaluation silently override a sibling
// property in a way this scanner cannot statically account for: any
// non-PropertyAssignment member (spread, shorthand, method, getter/setter —
// none of these are looked up by propertyInit at all) is always opaque. A
// PropertyAssignment with a computed key is opaque only when the key itself
// cannot be pinned to a static literal name — a computed key that IS a
// static literal (`['color']`, a cast literal, a resolved const) is exactly
// as provable as a plain `color:` key once propertyNameOf resolves it, and
// propertyInit's last-match-wins lookup already applies the same
// left-to-right override order the language uses.
export function opaqueObjectMember(member) {
  // A shorthand property (`{ status, ... }`, equivalent to `{ status:
  // status, ... }`) names itself exactly as unambiguously as a plain
  // `key: value` PropertyAssignment — nothing about it is dynamic or
  // unprovable. Recognized here (bullet 3/4: BoardView's COLUMNS =
  // STATUS_ORDER.map((status) => ({ status, label: ..., headerColor: ...
  // })) uses this shape for its OWN map-parameter key) so it does not void
  // the whole-object completeness proof for an UNRELATED sibling key
  // (`headerColor`) being read — propertyInit is extended to match, so a
  // read that specifically targets the shorthand key itself still resolves
  // too, never silently drops it.
  if (ts.isShorthandPropertyAssignment(member)) return false
  if (!ts.isPropertyAssignment(member)) return true
  return ts.isComputedPropertyName(member.name) && propertyNameOf(member.name) === null
}

export function isColorPropertyName(name) {
  const normalized = name.replace(/-/g, '').toLowerCase()
  return COLOR_PROPERTIES.has(normalized) || normalized.endsWith('color')
}

export function inspectCssValueExpr(node, ctx, stack, boundary = null) {
  node = unwrap(node)
  if (!node || isIgnoredLiteral(node)) return
  if (isDefinitelyNumeric(node, ctx) || isNumericSerialization(node, ctx)) {
    inspectLiteralColorArguments(node, ctx)
    return
  }
  if (ts.isCallExpression(node)) {
    if (isNonPaintApiCall(node, ctx)) return
    // A proven zod chain (`import { z } from 'zod'`) never gets the
    // expect/vi blanket skip above — see isProvenZodChain's doc comment.
    // Instead every literal argument anywhere in the chain (`.default(…)`,
    // but never a `.regex(/…/)` RegExp literal) is inspected directly, so
    // this reports ts-colors/raw-color for real colour data and stays
    // silent only where nothing in the chain is a string/template literal.
    if (isProvenZodChain(node, ctx)) {
      inspectZodChainLiterals(node, ctx)
      return
    }
  }

  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    analyzeCssValue(node.text, ctx, node)
    return
  }
  if (ts.isTemplateExpression(node)) {
    inspectTemplate(node, ctx, 'css', stack, boundary)
    return
  }
  if (ts.isIdentifier(node)) {
    if (isSkipIdent(node.text)) return
    const init = resolveIdentInit(node, ctx, stack)
    if (POSITIONAL_UNSTABLE_INITS.has(init)) {
      emitUnsupported(ctx, node)
      return
    }
    if (isParameterBinding(init)) {
      // Finite-const-array-callback capability (bullet 3): `color` in
      // `AVATAR_COLORS.map((color) => ({ backgroundColor: color, ... }))` IS
      // the array element itself; every element already passed
      // arrayLiteralIsStable's escape/mutation guard at bind time.
      if (init.arrayElements) {
        for (const element of init.arrayElements) inspectCssValueExpr(element, ctx, stack, boundary)
        return
      }
      if (boundary && init.ownerName !== 'anonymous' && !init.reassigned) emitRuntimePaintBoundary(ctx, node, boundary)
      else emitUnsupported(ctx, node)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      const destructured = resolveDestructuredTargets(node, ctx, stack)
      if (destructured) {
        if (!destructured.resolved) {
          // Deliberately NOT a blanket "any boundary" escape (would break
          // the adversarial suite's opaque-destructuring invariant: e.g.
          // `const { color } = fileTypeMeta(...)` must stay unsupported
          // forever, regardless of any boundary, because the SOURCE is an
          // arbitrary unprovable call — that is exactly what "stays
          // unsupported" means). isRuntimeMeasurementBoundary is the one
          // narrow, provable exception: a live layout/geometry measurement
          // surfaced ONLY through
          // `.style.setProperty('--custom-prop', …)` on a property that is
          // demonstrably not a colour name at all (isColorPropertyName
          // false) — the scanner treats every `--*` custom property as a
          // conservative paint sink by construction (it cannot know a
          // consumer won't put a colour in it), but `--app-top` etc. are
          // provably NOT colour-named, unlike the adversarial suite's
          // `color` sink. destructuredSourceParameterBinding is the SECOND,
          // equally narrow exception (R1 user-authored-colour capability):
          // the destructuring SOURCE itself provably resolves to a runtime
          // parameter binding (never an arbitrary call — see that function's
          // own doc comment for why the `fileTypeMeta()` pin stays blocked).
          if (boundary && isRuntimeMeasurementBoundary(boundary)) emitRuntimePaintBoundary(ctx, node, boundary)
          else if (boundary && boundary.owner !== 'anonymous' && destructuredSourceParameterBinding(node, ctx, stack)) emitRuntimePaintBoundary(ctx, node, boundary)
          else emitUnsupported(ctx, node)
          return
        }
        for (const target of destructured.targets) inspectCssValueExpr(target, ctx, stack, boundary)
        return
      }
      // Same "not a blanket escape" reasoning as above, for the plain case:
      // not a destructuring binding at all. isRuntimeMeasurementBoundary
      // covers the non-colour custom-property shape above; hookStateLocal
      // is the OTHER narrow, provable exception for a REAL colour
      // property — `const [selectedColor] = useState(...)` is
      // not an arbitrary opaque call the way `const [color] =
      // fileTypeMeta()` is (the adversarial suite's "array-destructured
      // bindings stay unsupported" case, which must keep failing): useState
      // guarantees the bound identifier holds exactly whatever its OWN
      // setter last wrote, nothing else, ever — PROVIDED every write is
      // visible (hookStateLocal itself voids the proof, returning null, the
      // moment the setter escapes to somewhere this file cannot see every
      // call of). scanHookStateLaundering closes the laundering gap a lead
      // review caught (2026-09-19): a registered exception at THIS read site
      // must never become a free pass for a raw colour literal smuggled in
      // through the initial value or a later setter call — those are
      // independently scanned every time this exact exception is reached,
      // so a raw literal there still reports ts-colors/raw-color.
      const hookState = boundary && boundary.owner !== 'anonymous' && !isRuntimeMeasurementBoundary(boundary) ? hookStateLocal(node) : null
      if (boundary && boundary.owner !== 'anonymous' && (isRuntimeMeasurementBoundary(boundary) || hookState)) {
        if (hookState) scanHookStateLaundering(hookState, ctx)
        emitRuntimePaintBoundary(ctx, node, boundary)
      }
      else emitUnsupported(ctx, node)
      return
    }
    // Marker keyed by this identifier occurrence's own source position, not
    // its bare text: two distinct bindings that happen to share a name
    // (e.g. a parameter forwarded into a nested helper's same-named
    // parameter) are different positions and must not collide. A genuine
    // resolution cycle always revisits the same declaring node (and so the
    // same position) and is still caught (nested-helper parameter-frame
    // propagation fix, glm-colours-collision-lead-probes "nested-frame").
    const marker = `${node.getSourceFile().fileName}#${node.pos}`
    if (stack.has(marker)) emitUnsupported(ctx, node)
    else {
      stack.add(marker)
      inspectCssValueExpr(init, ctx, stack, boundary)
      stack.delete(marker)
    }
    return
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    // A resolution tracker MUST be passed here (parity with inspectClassExpr's
    // member-access branch): without one, resolveMemberTargets's fast
    // single-target path (resolveMember) silently skips the spread/computed
    // key incompleteness check that its own multi-object fallback performs,
    // so a call-derived receiver with an opaque spread/computed key would
    // resolve to whatever explicit property happens to exist and be reported
    // clean instead of unsupported.
    // Null-branch capability (bullet 2): a dispatcher/ternary/record whose
    // candidate set includes a literal null/undefined must not fail the
    // WHOLE read closed when the read itself proves the receiver non-null
    // here (optional chaining, an `as NonNullable<typeof x>` cast, or a
    // lexical `x && …` / `x ? … : …` / `if (x) { … }` guard) — see
    // isProvenNonNullReceiver. Only the null candidate is excluded; every
    // OTHER candidate still has to resolve on its own merits.
    // Status-contract governed-record capability: checked FIRST, exactly
    // like the generated-token-accessor capability's own call-
    // site check below — a matching `statusContract.<key>.resolvedColor`
    // read reports CLEAN and returns here; every other shape (wrong module,
    // a look-alike local `statusContract`, an unresolvable key, a non-colour
    // final property) takes no special action and falls straight through,
    // unchanged, to the resolveMemberTargets handling immediately below.
    if (ts.isPropertyAccessExpression(node) && isStatusContractColorRead(node, ctx)) return
    const nullAllowed = isProvenNonNullReceiver(node)
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, stack, resolution, nullAllowed)
    if (targets.length) {
      for (const target of targets) inspectCssValueExpr(target, ctx, stack, boundary)
      if (resolution.incomplete) emitUnsupported(ctx, node)
    }
    else if (boundary && isDirectRuntimeRead(node) && boundary.owner !== 'anonymous') emitRuntimePaintBoundary(ctx, node, boundary)
    else emitUnsupported(ctx, node)
    return
  }
  if (ts.isConditionalExpression(node)) {
    inspectCssValueExpr(node.whenTrue, ctx, stack, boundary)
    inspectCssValueExpr(node.whenFalse, ctx, stack, boundary)
    return
  }
  if (ts.isCallExpression(node)) {
    // Generated-token-accessor capability — see
    // isGeneratedTokenAccessorCall's own doc comment for the full soundness
    // argument. A matching call reports CLEAN and returns here; every other
    // call (including every FORBIDDEN shape: a non-canonical module, a local
    // object literal or parameter base, an unrecognized String method, a
    // mixed literal return) takes no special action and falls straight
    // through, unchanged, to the withCalleeFrame handling immediately below.
    if (isGeneratedTokenAccessorCall(node, ctx, stack)) return
    const returns = withCalleeFrame(node, ctx, stack, (collected) => {
      for (const returned of collected) inspectCssValueExpr(returned, ctx, stack, boundary)
      return collected
    })
    if (!returns || returns.length === 0) emitUnsupported(ctx, node)
    return
  }
  if (ts.isBinaryExpression(node)) {
    inspectLogical(node, ctx, stack, (value, innerCtx, innerStack) => inspectCssValueExpr(value, innerCtx, innerStack, boundary))
    return
  }
  emitUnsupported(ctx, node)
}

export function inspectClassBuilderCall(call, ctx, stack) {
  const name = resolveCalleeName(call.expression, ctx)
  for (const arg of call.arguments) {
    if (name === 'cva') inspectCvaArg(arg, ctx, stack)
    else inspectClassExpr(arg, ctx, stack)
  }
}

export function inspectCvaArg(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return
  if (
    ts.isStringLiteral(node) ||
    ts.isNoSubstitutionTemplateLiteral(node) ||
    ts.isTemplateExpression(node) ||
    ts.isArrayLiteralExpression(node)
  ) {
    inspectClassExpr(node, ctx, stack)
    return
  }
  if (ts.isObjectLiteralExpression(node)) {
    for (const prop of node.properties) {
      if (ts.isSpreadAssignment(prop)) inspectCvaArg(prop.expression, ctx, stack)
      else if (ts.isPropertyAssignment(prop)) inspectCvaArg(prop.initializer, ctx, stack)
    }
  }
}

export function inspectClassExpr(node, ctx, stack, boundary = null) {
  node = unwrap(node)
  if (!node || isIgnoredLiteral(node)) return
  if (isDefinitelyBoolean(node, ctx)) return

  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    analyzeClassString(node.text, ctx, node)
    return
  }
  if (ts.isTemplateExpression(node)) {
    inspectTemplate(node, ctx, 'class', stack)
    return
  }
  if (ts.isArrayLiteralExpression(node)) {
    for (const element of node.elements) {
      if (ts.isSpreadElement(element)) inspectClassExpr(element.expression, ctx, stack)
      else inspectClassExpr(element, ctx, stack)
    }
    return
  }
  if (ts.isObjectLiteralExpression(node)) {
    inspectClassObjectKeys(node, ctx)
    return
  }
  if (ts.isIdentifier(node)) {
    if (isSkipIdent(node.text)) return
    const init = resolveIdentInit(node, ctx, stack)
    if (POSITIONAL_UNSTABLE_INITS.has(init)) {
      emitUnsupported(ctx, node)
      return
    }
    if (isParameterBinding(init)) {
      // Finite-const-array-callback capability (bullet 3): `color` in
      // `AVATAR_COLORS.map((color) => ...)` IS the array element itself —
      // every element already passed arrayLiteralIsStable's escape/mutation
      // guard at bind time, so each stands in exactly the same evidentiary
      // place as a finite record's property value.
      if (init.arrayElements) {
        for (const element of init.arrayElements) inspectClassExpr(element, ctx, stack)
        return
      }
      if (!init.forwardClassName || init.reassigned || (init.classBoundaryOwner ?? init.ownerName) === 'anonymous') emitUnsupported(ctx, node)
      else emitExtensionBoundary(ctx, node, init)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      const destructured = resolveDestructuredTargets(node, ctx, stack)
      if (destructured) {
        if (!destructured.resolved) {
          // table.tsx's `const { className: containerClassName } =
          // containerProps` (R1 user-authored-colour capability, className-
          // forward variant — see bodyDestructuredClassBoundary's own doc
          // comment).
          const bodyBoundary = bodyDestructuredClassBoundary(node, ctx, stack)
          if (bodyBoundary) { emitExtensionBoundary(ctx, node, bodyBoundary); return }
          emitUnsupported(ctx, node)
          return
        }
        for (const target of destructured.targets) inspectClassExpr(target, ctx, stack)
        return
      }
      emitUnsupported(ctx, node)
      return
    }
    // Marker keyed by this identifier occurrence's own source position, not
    // its bare text: two distinct bindings that happen to share a name
    // (e.g. a parameter forwarded into a nested helper's same-named
    // parameter) are different positions and must not collide. A genuine
    // resolution cycle always revisits the same declaring node (and so the
    // same position) and is still caught (nested-helper parameter-frame
    // propagation fix, glm-colours-collision-lead-probes "nested-frame").
    const marker = `${node.getSourceFile().fileName}#${node.pos}`
    if (stack.has(marker)) emitUnsupported(ctx, node)
    else {
      stack.add(marker)
      inspectClassExpr(init, ctx, stack)
      stack.delete(marker)
    }
    return
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    // Renamed class-like parameter member forward (`item.className`,
    // `badge.className`, `iconProps?.className`) — bullet 1's port of
    // typography's parameterMemberBoundary. Checked BEFORE the general
    // member-resolution fallback below: a bare/uniquely-bound parameter's
    // OWN `.className`/`.class` member, read unchanged, is caller
    // pass-through exactly like the plain-identifier forward above, not a
    // value this scanner can further resolve.
    const memberBoundary = classForwardMemberBoundary(node, ctx, stack)
    if (memberBoundary) {
      emitExtensionBoundary(ctx, node, memberBoundary)
      return
    }
    const nullAllowed = isProvenNonNullReceiver(node)
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, stack, resolution, nullAllowed)
    if (targets.length) {
      for (const target of targets) inspectClassExpr(target, ctx, stack, boundary)
      if (resolution.incomplete || !knownClassReceiverStable(node.expression, ctx)) emitUnsupported(ctx, node)
    } else if (absentClassProperty(node, ctx)) return
    else if (boundary && boundary.owner !== 'anonymous' && hasStableRuntimeParameterRoot(node, ctx)) emitUnverifiedGovernedValue(ctx, node, boundary)
    else emitUnsupported(ctx, node)
    return
  }
  if (ts.isCallExpression(node) && isClassBuilderCall(node, ctx)) {
    inspectClassBuilderCall(node, ctx, stack)
    return
  }
  if (ts.isCallExpression(node) && ts.isIdentifier(unwrap(node.expression))) {
    const factory = resolveIdentInit(unwrap(node.expression), ctx, stack)
    if (ts.isCallExpression(factory) && resolveCalleeName(factory.expression, ctx) === 'cva') {
      inspectClassBuilderCall(factory, ctx, stack)
      return
    }
  }
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(unwrap(node.expression))
    && (ARRAY_RECEIVER_PRESERVING_METHODS.has(unwrap(node.expression).name.text) || STRING_TRIM_METHODS.has(unwrap(node.expression).name.text))) {
    // `.join(sep)` only ever stringifies the array it's called on — never
    // invents new content. `.filter(predicate)` (any predicate, including
    // `Boolean`) can only REMOVE elements from its receiver, never add one:
    // whatever safety proof holds for the receiver array already covers
    // every element that could survive the filter. Recursing into the
    // receiver (exactly like the existing `.join()` handling) lets a
    // locally-defined class-composing helper's own filter/join chain over
    // its OWN parameter (e.g. `parts.filter(Boolean).join(' ')`) resolve
    // through to that parameter — which the composer-forwarding proof above
    // already covers — instead of dead-ending on the unresolvable
    // `.filter(...)` call itself.
    inspectClassExpr(unwrap(node.expression).expression, ctx, stack)
    return
  }
  if (ts.isCallExpression(node)) {
    const returns = withCalleeFrame(node, ctx, stack, (collected) => {
      for (const returned of collected) inspectClassExpr(returned, ctx, stack)
      return collected
    })
    if (!returns || returns.length === 0) emitUnsupported(ctx, node)
    return
  }
  if (ts.isConditionalExpression(node)) {
    inspectClassExpr(node.whenTrue, ctx, stack)
    inspectClassExpr(node.whenFalse, ctx, stack)
    return
  }
  if (ts.isBinaryExpression(node)) {
    inspectLogical(node, ctx, stack, inspectClassExpr)
    return
  }
  emitUnsupported(ctx, node)
}

// Absence requires a complete proof; the general member resolver intentionally
// retains partial targets to report known violations alongside unknown values.
export function absentClassProperty(node, ctx) {
  const property = ts.isPropertyAccessExpression(node) ? node.name.text
    : ts.isStringLiteral(unwrap(node.argumentExpression)) ? unwrap(node.argumentExpression).text : null
  const receiver = unwrap(node.expression)
  if (!property || ['__proto__', 'prototype', 'constructor'].includes(property) || !ts.isIdentifier(receiver)) return false
  const declaration = absenceLexicalBinding(receiver)
  if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer
    || !(declaration.parent.flags & ts.NodeFlags.Const)) return false
  if (!absenceBindingUsesSafe(declaration, false)) return false
  return absenceValue(declaration.initializer, property, ctx)
}

// Known initial colour targets do not establish the value after a receiver
// write or escape. Keep their findings, but retain an unsupported sink as well.
export function knownClassReceiverStable(node, ctx, seen = new Set()) {
  node = unwrap(node)
  if (!node || seen.has(node)) return false
  const next = new Set(seen).add(node)
  // A fixed literal value can never carry a mutable reference back to
  // anything — it is trivially receiver-stable on its own, independent of
  // whichever container produced it. Recognizing this base case here (rather
  // than re-deriving it at every call site) lets a ternary/`??` branch made
  // of plain literals (e.g. `cond ? 'a' : 'b'`) prove stable through the SAME
  // recursive conditional/binary handling just below.
  if (ts.isStringLiteralLike(node) || ts.isNumericLiteral(node) || ts.isTemplateExpression(node)
    || [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(node.kind)) return true
  if (ts.isObjectLiteralExpression(node) || ts.isArrayLiteralExpression(node)) return true
  if (ts.isConditionalExpression(node)) return knownClassReceiverStable(node.whenTrue, ctx, next) && knownClassReceiverStable(node.whenFalse, ctx, next)
  if (ts.isBinaryExpression(node) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(node.operatorToken.kind)) {
    return knownClassReceiverStable(node.left, ctx, next) && knownClassReceiverStable(node.right, ctx, next)
  }
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    if (!knownClassReceiverStable(node.expression, ctx, next)) return false
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, new Set(), resolution)
    // A stable container may still hold a borrowed mutable object. Follow the
    // selected values as well as the container before accepting their members.
    const stable = value => ts.isStringLiteralLike(value) || ts.isNumericLiteral(value) || ts.isTemplateExpression(value)
      || [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)
      || knownClassReceiverStable(value, ctx, next)
    return !resolution.incomplete && targets.length > 0 && targets.every(stable)
  }
  if (ts.isCallExpression(node)) {
    const callee = unwrap(node.expression)
    const factory = isFunctionLike(callee) ? callee : absenceFactory(callee, ctx)
    if (!factory || factory.asteriskToken || factory.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword)) return false
    const returns = functionReturns(node, ctx, new Set())
    return Boolean(returns?.length && returns.every(value => knownClassReceiverStable(value, ctx, next)))
  }
  if (!ts.isIdentifier(node)) return false
  // Finite-const-array-callback capability (bullet 3): `o` in
  // `STATUS_OPTIONS.filter(pred).map((o) => cn('text-xs', o.color))` is a
  // function PARAMETER, not a `const` variable declaration — absenceLexicalBinding's
  // own declaration-based proof below can never accept it, even though
  // arrayLiteralIsStable already proved the ARRAY it enumerates is
  // stable at bind time. Each candidate element gets the SAME recursive
  // stability proof any other resolved target does.
  const binding = lookup(ctx, node.text)
  if (isParameterBinding(binding) && binding.arrayElements && !binding.reassigned) {
    return binding.arrayElements.every(element => knownClassReceiverStable(element, ctx, next))
  }
  let declaration = absenceLexicalBinding(node)
  if (!declaration || !absenceBindingUsesSafe(declaration, false, true) || !knownClassDerivedUsesSafe(declaration, ctx)) return false
  if (ts.isImportSpecifier(declaration)) {
    const statement = declaration.parent.parent.parent
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) return false
    declaration = resolveModuleDeclaration(ctx, node.getSourceFile().fileName, statement.moduleSpecifier.text,
      declaration.propertyName?.text ?? declaration.name.text)
    if (!declaration || declaration === LOOKUP_UNBOUND || !absenceBindingUsesSafe(declaration, false, true)
      || !knownClassDerivedUsesSafe(declaration, ctx)) return false
  }
  return ts.isVariableDeclaration(declaration) && Boolean(declaration.parent.flags & ts.NodeFlags.Const)
    && knownClassExportUsesSafe(declaration, ctx)
    && Boolean(declaration.initializer) && knownClassReceiverStable(declaration.initializer, ctx, next)
}

// Only a JS/TS module can contain import/export/require syntax relevant to
// this cross-module proof. An asset file (`.svg`, `.css`, …) parsed as
// TS/JSX always produces parse diagnostics (it is not JS at all), which used
// to make EVERY exported const's receiver-stability proof fail the instant
// the module set contained even one such file — unconditionally, everywhere,
// regardless of relevance (found via `src/assets/logo/omnipus-avatar.svg`
// poisoning `STATUS_BADGE`'s proof in a completely unrelated file). This
// exemption is sound only because this project's build never executes an
// `.svg` or `.css` file as script: `vite.config.ts` registers no svgr-type
// plugin (no transform turns an `.svg` import into executable JS/TS), and
// every SVG is imported as a plain URL. If a plugin like that is ever added,
// this exemption must be revisited — an `.svg`/`.css` file could then carry
// real import/export/mutation code invisible to this cross-module proof.
export function isJsModulePath(path) {
  const ext = extname(path).toLowerCase()
  return ext === '.ts' || ext === '.tsx' || ext === '.js' || ext === '.jsx'
}

// A template-literal dynamic import()/require() argument's HEAD is always
// the literal prefix of whatever string it evaluates to at runtime — a
// substitution can only append characters after it, never rewrite or erase
// them (TemplateExpression semantics: head + eval(span1) + text + …).
// governedModulePath only ever resolves a specifier that itself starts with
// '@/' or a relative '.' form. If the head cannot possibly grow into one of
// those two forms (it already diverges from both — e.g. an npm package name
// interpolation like `@codemirror/legacy-modes/mode/${m}`), no runtime value
// of the template can EVER be a governedModulePath-resolvable specifier at
// all — proven impossible, not guessed — so it can never target `origin`.
// When the head IS shaped like a local specifier, it must additionally share
// `origin`'s directory (and, lacking a trailing '/', `origin`'s basename
// must share the head's own final segment as a prefix), or it is still a
// provably different target. Any other argument shape (bare identifier,
// call, spread, or an ambiguous short head that could still complete into
// '@/'/'.' once interpolation appends more text) stays exactly as
// conservative as before: not excluded.
export function dynamicImportProvenNotOrigin(modulePath, argument, origin) {
  if (!ts.isTemplateExpression(argument)) return false
  const head = argument.head.text
  if (head.startsWith('@/') || head.startsWith('./') || head.startsWith('../')) {
    const prefix = head.startsWith('@/') ? `src/${head.slice(2)}` : posix.normalize(posix.join(posix.dirname(modulePath), head))
    const prefixDir = prefix.endsWith('/') ? prefix.slice(0, -1) : posix.dirname(prefix)
    const originDir = posix.dirname(origin)
    if (originDir !== prefixDir) return true
    if (prefix.endsWith('/')) return false
    return !posix.basename(origin).startsWith(posix.basename(prefix))
  }
  if ('@/'.startsWith(head) || './'.startsWith(head) || '../'.startsWith(head)) return false
  return true
}

// Exported objects can be mutated through a different importer. Check all
// supplied modules; namespace/re-export/dynamic access stays unresolved rather
// than guessing which exported object it might expose.
export function knownClassExportUsesSafe(declaration, ctx) {
  const statement = declaration.parent.parent
  if (!statement.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.ExportKeyword)) return true
  if (!ctx.modules || !ts.isIdentifier(declaration.name)) return false
  const origin = declaration.getSourceFile().fileName
  for (const modulePath of Object.keys(ctx.modules)) {
    if (!isJsModulePath(modulePath)) continue
    const record = moduleRecord(ctx, modulePath)
    if (!record || record.sf.parseDiagnostics.length) return false
    for (const item of record.sf.statements) {
      if ((!ts.isImportDeclaration(item) && !ts.isExportDeclaration(item)) || !item.moduleSpecifier
        || !ts.isStringLiteral(item.moduleSpecifier)) continue
      if (governedModulePath(modulePath, item.moduleSpecifier.text, ctx.modules) !== origin) continue
      if (ts.isExportDeclaration(item)) {
        if (!item.isTypeOnly) return false
        continue
      }
      const clause = item.importClause
      if (!clause || clause.isTypeOnly) continue
      const bindings = clause.namedBindings
      if (clause.name || (bindings && !ts.isNamedImports(bindings))) return false
      if (bindings) for (const specifier of bindings.elements) {
        if (specifier.isTypeOnly || (specifier.propertyName?.text ?? specifier.name.text) !== declaration.name.text) continue
        if (!absenceBindingUsesSafe(specifier, false, true) || !knownClassDerivedUsesSafe(specifier, ctx)) return false
      }
    }
    let dynamic = false
    const visit = node => {
      if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword
        || (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
        const argument = node.arguments[0]
        if (!argument) dynamic = true
        else if (ts.isStringLiteralLike(argument)) {
          if (governedModulePath(modulePath, argument.text, ctx.modules) === origin) dynamic = true
        } else if (!dynamicImportProvenNotOrigin(modulePath, argument, origin)) dynamic = true
      }
      if (!dynamic) ts.forEachChild(node, visit)
    }
    visit(record.sf)
    if (dynamic) return false
  }
  return true
}

// A resolved member value consumed purely as a JSX element's own tag name
// (`<Icon/>`, `<driveChip.Icon/>`) is a terminal render read: React only
// invokes/constructs the referenced value, it is never handed anything that
// could reach back and mutate the stable record that produced it. Without
// recognizing this, an UNRELATED sibling property rendered as a component
// (e.g. `cfg.icon`) blanket-blocks every OTHER property drawn from the very
// same finite record (e.g. `cfg.activeColor`) purely because it isn't itself
// a further `.member` read.
export function isJsxTagName(expression) {
  const parent = expression.parent
  return Boolean(parent) && (ts.isJsxOpeningElement(parent) || ts.isJsxSelfClosingElement(parent) || ts.isJsxClosingElement(parent)) && parent.tagName === expression
}

// `Object.keys/isFrozen/getOwnPropertyNames(X)` are well-known, spec-pure
// static reads that can neither mutate `X` nor hand a mutable reference to
// `X`'s OWN nested values back to the caller: `keys`/`getOwnPropertyNames`
// return a brand-new array of STRINGS, and `isFrozen` returns a boolean. A
// terminal read this way is exactly as safe as a JSX-tag render —
// recognizing it stops e.g. `Object.keys(PRIORITY_BADGE)` (read once, for
// its domain of keys) from blanket-blocking every OTHER, unrelated read of
// the same record. Being passed the receiver at all is the complete proof:
// nothing about how the call's own return value is later used can turn this
// back into a mutable escape, so these are safe unconditionally.
export const UNCONDITIONALLY_READONLY_STATIC_METHODS = new Set(['keys', 'isFrozen', 'getOwnPropertyNames'])

// `Object.values`/`Object.entries` also return a brand-new array — but its
// elements (for `entries`, each pair's second element) are `X`'s OWN nested
// property VALUES, handed out live: mutating an element mutates `X` itself.
// Folding these in with `keys` and exempting just the argument occurrence
// would be unsound — it proves nothing about what the caller does with the
// returned array afterward (store it, iterate
// it with `forEach`/`for...of`/`.map`/spread/`Array.from`, mutate an
// element). This scanner does not attempt to trace every way such a
// live-reference array can be consumed, so per the "when in doubt, treat it
// as an escape" rule, passing the receiver to either of these is itself
// treated as an escape of its nested values — there is no safe default here
// the way there is for `keys`.
export const NESTED_REFERENCE_RETURNING_STATIC_METHODS = new Set(['values', 'entries'])

// `Object.freeze(X)` returns the SAME reference passed in — not a copy — and
// only sets `X`'s own (shallow) mutability flag: nested objects reachable
// through `X` stay fully mutable after the call. Treating the argument
// occurrence as safe is sound ONLY when nobody captures or chains off the
// call's return value (a bare `Object.freeze(X)` statement); the instant the
// return value is assigned, chained, or otherwise consumed, it is exactly as
// live an alias of `X` as `X` itself and must not be exempted here.
export function objectFreezeCallArgumentDiscardsReturn(callExpr) {
  // Walk out through any wrapping parens (`;(Object.freeze(X))`,
  // `(Object.freeze(X));`) — these are the exact same discarded-return
  // shape as the bare statement, just with redundant parens; only the
  // paren wrapper sits between the call and the statement (W3 review
  // finding). Do not also unwrap `void`/`if (...)  Object.freeze(X)` here —
  // those are separate, unreviewed shapes this fix does not claim to cover.
  let node = callExpr
  while (ts.isParenthesizedExpression(node.parent)) node = node.parent
  return ts.isExpressionStatement(node.parent)
}

export function objectStaticCallArgumentMethod(node) {
  const parent = node.parent
  if (!ts.isCallExpression(parent) || parent.expression === node || !parent.arguments.includes(node)) return null
  const callee = unwrap(parent.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ts.isIdentifier(callee.expression) || callee.expression.text !== 'Object') return null
  return { method: callee.name.text, call: parent }
}

export function isReadonlyObjectStaticCallArgument(node) {
  const call = objectStaticCallArgumentMethod(node)
  if (!call) return false
  if (UNCONDITIONALLY_READONLY_STATIC_METHODS.has(call.method)) return true
  if (call.method === 'freeze') return objectFreezeCallArgumentDiscardsReturn(call.call)
  // `values`/`entries` and any unrecognized static method both fall through
  // to `false` here — an escape, per the "when in doubt" rule above.
  if (NESTED_REFERENCE_RETURNING_STATIC_METHODS.has(call.method)) return false
  return false
}

// A ternary/`??` composed entirely of literal leaves (e.g. `cond ? 'a' : 'b'`)
// is just as immutable and escape-free as a single literal — unlike an
// object/array literal, which CAN still be captured by a separate alias and
// mutated elsewhere (that risk is exactly what `knownClassDerivedUsesSafe`'s
// alias-escape walk below must keep catching, so this stays deliberately
// narrower than `knownClassReceiverStable` and never treats a container
// literal or an identifier chain as a safe leaf on its own).
export function primitiveLeaf(value) {
  value = unwrap(value)
  if (!value) return false
  if (ts.isStringLiteralLike(value) || ts.isNumericLiteral(value) || ts.isTemplateExpression(value)
    || [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)) return true
  if (ts.isConditionalExpression(value)) return primitiveLeaf(value.whenTrue) && primitiveLeaf(value.whenFalse)
  if (ts.isBinaryExpression(value) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(value.operatorToken.kind)) {
    return primitiveLeaf(value.left) && primitiveLeaf(value.right)
  }
  return false
}

// A property read may expose a nested configuration object. Follow const
// selections and reject their escapes too; primitive class values cannot carry
// a mutable reference back to the configuration.
export function knownClassDerivedUsesSafe(declaration, ctx, seen = new Set()) {
  if (!declaration.name || !ts.isIdentifier(declaration.name) || seen.has(declaration)) return false
  const next = new Set(seen).add(declaration)
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  // The JsxExpression carve-out below is sound ONLY for a binding read
  // exactly once in scope — a "single-use const record". With a SIBLING
  // read elsewhere (even one
  // that is itself JsxExpression-terminal), an unresolvable branch anywhere
  // in the binding's structure still voids the WHOLE binding's proof for
  // every property, not just the one that happens to be unresolvable — see
  // the adversarial "a sibling whose ternary has one opaque literal branch
  // still blocks" case this single-use gate exists to keep blocking.
  let occurrences = 0
  const countOccurrences = node => {
    if (ts.isIdentifier(node) && node.text === declaration.name.text && node !== declaration.name) occurrences += 1
    ts.forEachChild(node, countOccurrences)
  }
  countOccurrences(scope)
  const singleUse = occurrences === 1
  let safe = true
  const visit = node => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === declaration.name.text && node !== declaration.name) {
      let member = node.parent
      if ((ts.isPropertyAccessExpression(member) || ts.isElementAccessExpression(member)) && member.expression === node) {
        while ((ts.isPropertyAccessExpression(member.parent) || ts.isElementAccessExpression(member.parent)) && member.parent.expression === member) member = member.parent
        // R1 user-authored-colour capability: a terminal `.length` read, or
        // a terminal array-enumeration/narrowing CALL (`.map()`, `.filter()`,
        // `.forEach()`, `.find()`, `.some()`, `.every()`) directly on this
        // occurrence, is exactly as safe as the `.join()`/`.filter()` receiver
        // recursion this file already grants class-composing helpers
        // (ARRAY_RECEIVER_PRESERVING_METHODS) and exactly the shape
        // resolveStableArrayLiteralElements' own CallExpression branch
        // already resolves through (AgentFormFields.tsx's
        // `AVATAR_COLORS.map((color) => ...)`) — `.length` returns a
        // primitive number that can never carry a mutable reference back to
        // the array, and none of these methods can manufacture a NEW,
        // unproven element or hand out a mutable alias of the receiver
        // itself. Recognizing them here only WIDENS what counts as a safe
        // terminal read of THIS occurrence (mirrors the JsxExpression/
        // VariableDeclaration-alias terminal cases below); every other
        // occurrence of the same exported name still needs its own proof.
        if (ts.isPropertyAccessExpression(member) && member.name.text === 'length') return
        if (ts.isPropertyAccessExpression(member) && (ARRAY_ENUMERATION_METHODS.has(member.name.text) || ARRAY_NARROWING_METHODS.has(member.name.text))
          && ts.isCallExpression(member.parent) && member.parent.expression === member) return
        const resolution = { incomplete: false }
        const targets = resolveMemberTargets(member, ctx, new Set(), resolution)
        // A sibling property's inability to be fully ENUMERATED (e.g. an
        // optional field omitted on some branches, blocked by the
        // deliberately-unrelaxed null-prototype absence rule) is a
        // value-completeness concern already independently enforced at that
        // property's own emission site — it is not evidence THIS receiver
        // could be mutated, so `resolution.incomplete` must not gate the
        // escape check below (that would blanket-block an unrelated, fully
        // resolvable sibling property purely because ANOTHER property
        // couldn't prove absence). `targets.length === 0` stays a trigger:
        // `[].every(primitive)` is vacuously true, so a totally unresolvable
        // occurrence must still be treated as potentially escaping. This
        // uses `primitiveLeaf`, NOT `knownClassReceiverStable` — the latter
        // also accepts a bare object/array literal as "stable", which is
        // right for TRAVERSING into one but wrong here: that literal can
        // still be captured by a separate alias and mutated elsewhere, which
        // is exactly the escape the alias-check below exists to catch.
        const primitive = value => primitiveLeaf(value)
        if ((targets.length === 0 || !targets.every(primitive)) && !isJsxTagName(member)) {
          let derived = member
          while ((ts.isBinaryExpression(derived.parent) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(derived.parent.operatorToken.kind))
            || (ts.isConditionalExpression(derived.parent) && (derived.parent.whenTrue === derived || derived.parent.whenFalse === derived))) derived = derived.parent
          const alias = derived.parent
          // A read consumed directly by a JsxExpression (`{cfg.color}` as a
          // JSX attribute value or child) is a terminal, read-only render
          // consumption — no NEW alias is created here for something else to
          // capture and later mutate, so there is nothing for the escape walk
          // below to catch. This is distinct from every other non-alias
          // consumer (a call argument, a return statement, …): those CAN
          // hand the value to code that stores or mutates it, so they must
          // keep failing the proof. Only widen this one, narrowly-provable
          // carve-out — do not broaden to "any non-alias use is safe",
          // which would defeat the walk entirely.
          if (singleUse && ts.isJsxExpression(alias)) { /* terminal read; no escape */ }
          else if (!ts.isVariableDeclaration(alias) || alias.initializer !== derived
            || !(alias.parent.flags & ts.NodeFlags.Const) || !absenceBindingUsesSafe(alias, false, true)
            || !knownClassDerivedUsesSafe(alias, ctx, next)) { safe = false; return }
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return safe
}

export function absenceBindingHasName(binding, name) {
  if (ts.isIdentifier(binding)) return binding.text === name
  return (ts.isObjectBindingPattern(binding) || ts.isArrayBindingPattern(binding))
    && binding.elements.some(element => ts.isBindingElement(element) && absenceBindingHasName(element.name, name))
}

// Resolve lexical declarations without falling through a nearer opaque binding.
// Switch, namespace, class and loop scopes are deliberately outside this proof.
export function absenceLexicalBinding(identifier) {
  const name = identifier.text
  for (let scope = identifier.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (isFunctionLike(scope) && scope.parameters.some(parameter => absenceBindingHasName(parameter.name, name))) return null
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
    if (matches.length) return matches.length === 1 ? matches[0] : null
  }
  return null
}

// A simple, non-rest, non-default, non-nested object-destructuring read of a
// stable receiver (`const { Icon } = cfg`) is safe when every extracted
// local is itself only ever used safely (recursing through this same
// property-or-terminal-JSX-tag proof) — the same "read-only, never a whole
// mutable escape" guarantee this function already proves for a plain
// member-access alias, just entered through a binding pattern instead of a
// `const x = y.z` initializer.
export function destructuredPatternUsesSafe(pattern) {
  if (!ts.isObjectBindingPattern(pattern)) return false
  for (const element of pattern.elements) {
    if (element.dotDotDotToken || !ts.isIdentifier(element.name) || element.initializer) return false
    if (!absenceBindingUsesSafe(element, false, true)) return false
  }
  return true
}

// A receiver may only be read through properties; a factory may only be called.
// Whole-object references, aliases, writes and calls through its members escape
// the proof. Scanning the enclosing scope also catches writes after the sink.
export function absenceBindingUsesSafe(declaration, factory, indexedReads = false) {
  if (!declaration.name || !ts.isIdentifier(declaration.name)) return false
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  const visit = node => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
      const parent = node.parent
      if (ts.isImportSpecifier(parent) && parent === declaration) return
      if (factory) {
        if (!ts.isCallExpression(parent) || parent.expression !== node) { safe = false; return }
      } else {
        if (isJsxTagName(node)) return
        if (ts.isVariableDeclaration(parent) && parent.initializer === node && destructuredPatternUsesSafe(parent.name)) return
        if (isReadonlyObjectStaticCallArgument(node)) return
        // R1 user-authored-colour capability: `for (const x of ARRAY)` reads
        // ARRAY as a plain iterable — sequential, read-only element access,
        // exactly as safe as `.forEach()`/`.map()` (which this function's
        // sibling widening in knownClassDerivedUsesSafe already grants): it
        // can neither mutate the receiver nor hand out a reference that
        // could. Not a property/element read at all, so it would otherwise
        // fall through this function's ordinary "must be a property/element
        // read" requirement below and mark the whole export unsafe
        // (AgentFormFields.test.tsx's `for (const color of AVATAR_COLORS)`).
        if (ts.isForOfStatement(parent) && parent.expression === node) return
        if ((!ts.isPropertyAccessExpression(parent) && !ts.isElementAccessExpression(parent)) || parent.expression !== node) { safe = false; return }
        const property = ts.isPropertyAccessExpression(parent) ? parent.name.text
          : ts.isStringLiteral(unwrap(parent.argumentExpression)) ? unwrap(parent.argumentExpression).text : null
        if ((!property && !indexedReads) || ['__proto__', 'prototype', 'constructor'].includes(property)) { safe = false; return }
        let expression = parent
        while (expression.parent && (ts.isPropertyAccessExpression(expression.parent)
          || ts.isElementAccessExpression(expression.parent) || ts.isParenthesizedExpression(expression.parent)
          || ts.isAsExpression(expression.parent) || ts.isNonNullExpression(expression.parent)
          || ts.isObjectLiteralExpression(expression.parent) || ts.isArrayLiteralExpression(expression.parent)
          || ts.isPropertyAssignment(expression.parent) || ts.isSpreadElement(expression.parent))) expression = expression.parent
        const use = expression.parent
        // R1 user-authored-colour capability: a terminal array-enumeration/
        // narrowing CALL (`.map()`, `.filter()`, `.forEach()`, `.find()`,
        // `.some()`, `.every()`) directly on THIS property read is exactly
        // as safe as every other terminal read this function already grants
        // (a plain property/element read, above) — none of these methods
        // can manufacture a NEW, unproven element or hand out a mutable
        // alias of the receiver itself (same reasoning as
        // knownClassDerivedUsesSafe's matching widening, and the reasoning
        // resolveStableArrayLiteralElements' own CallExpression branch
        // already relies on to resolve AVATAR_COLORS.map((color) => ...)).
        // Gated on THIS exact property name (`.map`/etc, checked above),
        // never on "any call" — an arbitrary call through this member would
        // still fail closed exactly as before.
        const isEnumerationCall = ts.isCallExpression(use) && use.expression === expression
          && property && (ARRAY_ENUMERATION_METHODS.has(property) || ARRAY_NARROWING_METHODS.has(property))
        if ((ts.isBinaryExpression(use) && use.left === expression && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
          || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
          || ts.isDeleteExpression(use) || ts.isPrefixUnaryExpression(use) || ts.isPostfixUnaryExpression(use)
          || (ts.isCallExpression(use) && use.expression === expression && !isEnumerationCall)) { safe = false; return }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return safe
}

export function absenceFactory(callee, ctx) {
  if (!ts.isIdentifier(callee)) return null
  let declaration = absenceLexicalBinding(callee)
  if (!declaration || !absenceBindingUsesSafe(declaration, true)) return null
  if (ts.isImportSpecifier(declaration)) {
    const statement = declaration.parent.parent.parent
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) return null
    const modulePath = governedModulePath(callee.getSourceFile().fileName, statement.moduleSpecifier.text, ctx.modules)
    const record = modulePath && moduleRecord(ctx, modulePath)
    if (!record || record.sf.parseDiagnostics.length) return null
    const imported = declaration.propertyName?.text ?? declaration.name.text
    const matches = record.sf.statements.filter(statement => statement.name?.text === imported)
    if (matches.length !== 1) return null
    declaration = matches[0]
    if (!declaration.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.ExportKeyword)
      || !absenceBindingUsesSafe(declaration, true)) return null
  }
  if (!ts.isFunctionDeclaration(declaration) || !ts.isSourceFile(declaration.parent)
    || !declaration.body || declaration.asteriskToken
    || declaration.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword)) return null
  return declaration
}

export function absenceValue(node, property, ctx) {
  node = unwrap(node)
  if (ts.isObjectLiteralExpression(node)) {
    // An ordinary object can inherit this property from arbitrary application
    // or imported prototype mutations. Only an explicit null prototype proves
    // absence without assuming that the surrounding module graph is pure.
    const nullPrototype = node.properties.some(member => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name) && propertyNameOf(member.name) === '__proto__'
      && unwrap(member.initializer).kind === ts.SyntaxKind.NullKeyword)
    return nullPrototype && node.properties.every(member => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name)
      && propertyNameOf(member.name) !== property
      && (propertyNameOf(member.name) !== '__proto__' || unwrap(member.initializer).kind === ts.SyntaxKind.NullKeyword))
  }
  if (ts.isConditionalExpression(node)) {
    return absenceValue(node.whenTrue, property, ctx) && absenceValue(node.whenFalse, property, ctx)
  }
  if (!ts.isCallExpression(node)) return false
  const declaration = absenceFactory(unwrap(node.expression), ctx)
  if (!declaration) return false
  const returns = []
  const visit = child => {
    if (isFunctionLike(child) && child !== declaration) return
    if (ts.isReturnStatement(child)) returns.push(child.expression)
    else ts.forEachChild(child, visit)
  }
  visit(declaration.body)
  // Returned calls/aliases are opaque, so recursive functions never recurse
  // through this proof. Direct conditional literals cover every returned arm.
  const fresh = value => {
    value = value && unwrap(value)
    if (!value) return false
    if (ts.isConditionalExpression(value)) return fresh(value.whenTrue) && fresh(value.whenFalse)
    return ts.isObjectLiteralExpression(value) && absenceValue(value, property, ctx)
  }
  return returns.length > 0 && returns.every(fresh)
}

// `expect`/`vi`/`z` are never trusted by BARE NAME alone — a same-named
// local (`const z = { object: (o) => o }`) or a same-named shadowing
// parameter would hide colours exactly as effectively as the real import.
// isBoundTestNamespace below requires PROOF: the identifier is
// either never locally bound at all (LOOKUP_MISSING — the legitimate
// vitest-globals case, since this repo's vite.config.ts sets `test.globals
// = true` and ~650 real *.test.tsx files use `expect`/`vi` with no import
// line at all) or explicitly imported from the real package. A local
// declaration/shadow always binds to something other than LOOKUP_MISSING or
// a real external IMPORT_BINDING, so it can never satisfy this proof.
export function isBoundTestNamespace(name, ctx) {
  const binding = lookup(ctx, name)
  if (binding === LOOKUP_MISSING) return true
  return Boolean(binding) && typeof binding === 'object' && binding.kind === IMPORT_BINDING
    && binding.external && (binding.specifier === 'vitest' || binding.specifier.startsWith('@vitest/'))
}

// zod's `z` has no ambient-global form (unlike vitest's globals) — every
// legitimate use is `import { z } from 'zod'` (or a `zod/*` subpath), so
// LOOKUP_MISSING never counts as proof here the way it does for
// isBoundTestNamespace.
export function isZodImportBinding(binding) {
  return Boolean(binding) && typeof binding === 'object' && binding.kind === IMPORT_BINDING
    && binding.external && (binding.specifier === 'zod' || binding.specifier.startsWith('zod/'))
}

// A call/identifier chain originates from zod when it is EITHER rooted
// directly at a proven `z`
// import (including a renamed one, `import { z as zz } from 'zod'` —
// isZodImportBinding checks the BINDING, not the literal text `z`) OR, at
// any point along its property-access/call spine, hands off to a plain
// local variable/const whose OWN initializer recursively satisfies the
// same proof — e.g. `const VaultFilterNode = z.lazy(() => z.object({...}));
// ... VaultFilterNode.optional()`. This does NOT recurse into the alias's
// own ARGUMENTS (only through `.expression` hops), so it never re-proves
// the alias's own literal safety — that is unnecessary: the alias's
// initializer is a normal statement in the file, independently walked and
// inspected (via the ordinary tree walk / isZodShapeArgument below) at its
// OWN definition site. This function only answers "is this identifier
// eligible for the narrow literal-scan below", never "is this whole call
// opaque, skip it" — trusting the whole call is unsound for a DIRECT `z`
// root, and exactly as unsound one alias hop away.
// `seen` guards a self-referential/mutually-aliased pair from recursing
// forever (VaultFilterNode's own definition legitimately references
// `VaultFilterNode` again inside `z.array(VaultFilterNode)`, but that
// reference lives in a CALL ARGUMENT this function never descends into, so
// the guard is a defensive backstop, not something the real shape relies
// on to terminate).
export function zodChainRoot(node, ctx, seen) {
  node = unwrap(node)
  if (!node) return false
  if (ts.isCallExpression(node)) return zodChainRoot(node.expression, ctx, seen)
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) return zodChainRoot(node.expression, ctx, seen)
  if (!ts.isIdentifier(node)) return false
  if (seen.has(node.text)) return false
  seen.add(node.text)
  const binding = lookup(ctx, node.text)
  if (isZodImportBinding(binding)) return true
  if (!binding || binding === LOOKUP_MISSING || binding === LOOKUP_UNBOUND) return false
  // A DIFFERENT import (governed-local or another external package) and a
  // function parameter are never assumed to secretly BE zod — only a
  // plain local variable/const's own initializer (the raw AST node
  // `lookup`/`bind` store directly for that case, per bindPattern) is
  // eligible to keep chasing.
  if (typeof binding === 'object' && (binding.kind === IMPORT_BINDING || binding.kind === PARAMETER_BINDING)) return false
  return zodChainRoot(binding, ctx, seen)
}

// The zod builder/combinator methods whose argument is a SHAPE object (its
// keys are schema field names, its values are schemas or literal defaults)
// rather than a style/CSS object — even though a field can happen to be
// named `color` or `filter`, exactly like a real CSS property. Gates
// isZodShapeArgument below.
export const ZOD_SHAPE_METHODS = new Set(['object', 'strictObject', 'extend', 'merge', 'partial', 'pick', 'omit'])

export function isNonPaintApiCall(call, ctx) {
  let expression = unwrap(call.expression)
  while (ts.isPropertyAccessExpression(expression)) {
    expression = unwrap(expression.expression)
    if (ts.isCallExpression(expression)) expression = unwrap(expression.expression)
  }
  const root = ts.isIdentifier(expression) ? expression.text : ''
  // `vi` is Vitest's test-utility namespace (vi.fn/vi.spyOn/vi.mock/…) — it
  // is only ever importable inside a test file, never bundled into runtime
  // application code, so a value that is the argument to (or return/
  // implementation of) a `vi.*` call can never reach a real render; it is
  // fixture/mock data standing in for a real API's shape (e.g.
  // `vi.spyOn(Canvas...).mockReturnValue({ stroke: () => {} })`, where
  // `stroke` is the Canvas 2D method name, not an SVG colour property).
  // `expect` (including `expect.stringMatching(...)`, folded into this same
  // root check rather than a separate `names.includes('stringMatching')`
  // branch) is the matching jest-dom/testing-library assertion namespace.
  //
  // `z` (zod) is deliberately NOT given this same blanket "the whole call
  // is opaque, skip it" trust: a zod schema's literal arguments
  // (`.default('#ff0000')`) are
  // real application data, unlike `expect`/`vi`'s test-only fixtures, so a
  // zod-rooted call gets narrow literal-argument scanning
  // (isProvenZodChain / inspectZodChainLiterals in inspectCssValueExpr)
  // instead of a blanket skip here.
  if ((root === 'expect' || root === 'vi') && isBoundTestNamespace(root, ctx)) return true
  if (!root || !ctx) return false
  const binding = lookup(ctx, root)
  const value = bindingValue(binding)
  return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && originatesFromNonPaintApi(value, ctx, new Set([root])))
}

export function originatesFromNonPaintApi(node, ctx, seen) {
  node = unwrap(node)
  if (!node) return false
  if (ts.isCallExpression(node)) return originatesFromNonPaintApi(node.expression, ctx, seen)
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) return originatesFromNonPaintApi(node.expression, ctx, seen)
  if (!ts.isIdentifier(node)) return false
  // Alias-chasing (`const helper = vi; helper.fn(...)`) stays trusted for
  // `expect`/`vi` — see isBoundTestNamespace: still binding-proven, not
  // name-only. `z` is deliberately excluded here too (not just from the
  // direct check above): an ALIASED zod value (`const S = z; S.object(...)`)
  // would otherwise get the SAME blanket "whole call is opaque" trust this
  // function grants expect/vi, undoing the narrow literal-scanning decision
  // above. Losing alias-chasing for zod is intentionally conservative —
  // worst case an aliased zod chain reports ts-colors/unsupported instead
  // of being precisely scanned, never a silent pass.
  if ((node.text === 'expect' || node.text === 'vi') && isBoundTestNamespace(node.text, ctx)) return true
  if (seen.has(node.text)) return false
  seen.add(node.text)
  const value = bindingValue(lookup(ctx, node.text))
  return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && originatesFromNonPaintApi(value, ctx, seen))
}

// Walks the property-access/call spine of a zod chain (`z.string().regex(p)
// .optional()` → [.optional(), .regex(p), .string()]), stopping at the
// root identifier. Used to scan every call's OWN direct arguments — never
// an object literal argument (those are already, independently, walked and
// inspected by inspectStyleObject through the ordinary tree walk; this
// function's job is only the literal-arguments-of-a-call-chain shape a
// normal object-literal walk cannot see, e.g. `.default('#ff0000')`).
export function zodChainCalls(call) {
  const calls = []
  let node = unwrap(call)
  while (node) {
    if (ts.isCallExpression(node)) {
      calls.push(node)
      node = unwrap(node.expression)
      continue
    }
    if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
      node = unwrap(node.expression)
      continue
    }
    break
  }
  return calls
}

// A call/identifier chain rooted at a PROVEN zod import — directly
// (`z....`) or through a plain local alias (zodChainRoot) — eligible for
// the narrow literal-argument scan below instead of the generic
// "unresolved dynamic colour expression" fallback every other unrecognized
// call gets. Being "eligible" here does not mean "safe" — it means every
// literal argument in the chain (stopping at any alias boundary) gets
// INSPECTED (inspectZodChainLiterals), which is what actually decides
// raw-color vs. clean.
export function isProvenZodChain(node, ctx) {
  return zodChainRoot(node, ctx, new Set())
}
