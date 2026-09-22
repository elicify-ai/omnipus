#!/usr/bin/env node
// typography/ts-scan.mjs
//
// The TypeScript/TSX scan (className/style-attribute walking) and the
// style-attribute scan share this file because scanTypeScript's own engine
// constructs both a ClassExpressionEngine and a createStyleWalker() and
// calls into scanCss (css-scan.mjs) for embedded <style> blocks -- pulling
// either piece into a separate file would create either an import cycle or
// a forward-reference cell, neither of which is simpler than one file.
// Depends on class-value-rules.mjs, module-and-purity.mjs and
// css-scan.mjs; nothing in css-scan.mjs or module-and-purity.mjs calls
// back in here, so the module graph stays acyclic.
//
// ClassExpressionEngine and TypeScriptScanEngine hold createClassWalker's
// and scanTypeScript's logic as class methods: a class declaration is not
// itself a function-like node the function-size scanner
// (scripts/tsfunlen.cjs) counts, so each method is measured on its own
// span instead of one closure bundle's -- see each class's own header
// comment.

'use strict'

import ts from 'typescript'
import {
  CLASS_BUILDERS,
  DYNAMIC_UTILITY_MESSAGE,
  STYLE_SIZE_PROPERTY_NAMES,
  STYLE_FAMILY_PROPERTY_NAMES,
  STYLE_SHORTHAND_PROPERTY_NAMES,
  STYLE_ROLE_PROPERTIES,
  FLOOR_PX,
  ROOT_MIN_PX,
  ROOT_MAX_PX,
  VAR_REFERENCE_PATTERN,
  isClassLikeParameterName,
  scriptKindFor,
  evaluateFontSizeValue,
  evaluateFontFamilyValue,
  parseFontShorthand,
  collapseWhitespace,
  classifyClassToken,
} from './class-value-rules.mjs'
import {
  plainDeclarationShadows,
  findLexicalBinding,
  hasMultipleVariableDeclarations,
  unwrapStatic,
  resolveImportedExpression,
  propertyKeyName,
  absenceValue,
  sameFileFunctionDeclaration,
  absenceBindingUsesSafe,
  derivedValueEscapeSafe,
  importedDeclaration,
  exportNeverMutatedByImporters,
  arrayBindingUsesSafe,
  objectProperty,
  classBuilderOwnParameterForward,
  calleeDeclaration,
  declarationMarker,
  finiteCallReturns,
  pureFunctionReturns,
  cvaDefinitionFor,
  authenticClassBuilderCallee,
  authenticCvaImport,
} from './module-and-purity.mjs'
import { scanCss } from './css-scan.mjs'

export function createClassNodeHelpers(sourceFile) {
  function walkClassBinary(node, sink, recurse) {
    const operator = node.operatorToken.kind
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken) {
      // The left operand is a boolean guard by clsx convention. Only the
      // resulting class value on the right is typography input.
      recurse(node.right, sink)
      return
    }
    if (operator === ts.SyntaxKind.BarBarToken || operator === ts.SyntaxKind.QuestionQuestionToken) {
      // Either operand can become the resulting class string.
      recurse(node.left, sink)
      recurse(node.right, sink)
      return
    }
    if (operator === ts.SyntaxKind.PlusToken) {
      const staticText = collectStaticText(node)
      if (staticText === null) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
        return
      }
      const partial = staticText.split(/\s+/).filter(Boolean).pop() ?? ''
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (partial.length > 0) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
      }
      return
    }
  }

  function collectStaticText(node) {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = collectStaticText(node.left)
      const right = collectStaticText(node.right)
      if (left !== null && right !== null) return left + right
      if (left !== null) return left
      if (right !== null) return right
    }
    return null
  }

  function walkDynamicTemplate(node, sink, recurse) {
    const staticTexts = [node.head.text, ...node.templateSpans.map((span) => span.literal.text)]
    // Capability A (independent review): a span that LOOKS glued by raw
    // static-text adjacency can still be provably separated when the
    // interpolation is a (possibly nested) conditional whose every branch is
    // either empty or itself supplies the missing whitespace boundary — e.g.
    // `` `cell${isToday ? ' today' : ''}` `` (glued-before, both branches are
    // empty or start with a space) or ChatImage's `` `...${c ? ` ${c}` : ''}` ``
    // (the true branch is a nested template whose OWN head starts with a
    // space; its interior interpolation is resolved by the ordinary
    // recursive walk once reached). Computed up front so both passes below
    // (which token a static text's boundary must/must not trim, and how a
    // span itself classifies) agree.
    const selfSeparating = node.templateSpans.map((span, index) => {
      const before = staticTexts[index]
      const after = staticTexts[index + 1]
      const gluedBeforeRaw = /\S$/.test(before) || (before === '' && index > 0)
      const gluedAfterRaw = /^\S/.test(after) || (after === '' && index < node.templateSpans.length - 1)
      if (!gluedBeforeRaw && !gluedAfterRaw) return false
      const partial = before.split(/\s+/).filter(Boolean).pop() ?? ''
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') return false
      if (/\[(?:[^\]]*)?$/.test(partial) || /\((?:[^)]*)?$/.test(partial)) return false
      return isSelfSeparatingConditional(span.expression, gluedBeforeRaw, gluedAfterRaw)
    })
    staticTexts.forEach((text, index) => {
      const isFirst = index === 0
      const isLast = index === staticTexts.length - 1
      const tokens = text.split(/\s+/).filter(Boolean)
      const interior = [...tokens]
      // Tokens glued to an interpolation boundary are partial; the glue check
      // below handles them. Interior tokens classify normally. A boundary
      // whose adjacent span is self-separating is NOT partial — the static
      // token is already complete on its own (the branch supplies its own
      // separator or is empty), so it must not be trimmed away here.
      if (!isFirst && !selfSeparating[index - 1] && /^\S/.test(text) && interior.length > 0) interior.shift()
      if (!isLast && !selfSeparating[index] && /\S$/.test(text) && interior.length > 0) interior.pop()
      for (const token of interior) classifyClassToken(token, sink)
    })
    node.templateSpans.forEach((span, index) => {
      const before = staticTexts[index]
      const after = staticTexts[index + 1]
      const partial = before.split(/\s+/).filter(Boolean).pop() ?? ''
      // Glue means the interpolation supplies a token fragment rather than
      // whole class names: it continues the last static token (`safe${x}`),
      // begins a token completed by static text (`${x}more`), or sits directly
      // against a neighbouring interpolation with no separating text.
      const gluedBefore = /\S$/.test(before) || (before === '' && index > 0)
      const gluedAfter = /^\S/.test(after) || (after === '' && index < node.templateSpans.length - 1)
      if (partial === 'text-' || partial === 'font-' || partial === 'leading-' || partial === 'tracking-') {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (/\[(?:[^\]]*)?$/.test(partial) || /\((?:[^)]*)?$/.test(partial)) {
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: `${partial}\${…}`, message: DYNAMIC_UTILITY_MESSAGE })
      } else if (selfSeparating[index]) {
        recurse(span.expression, sink)
      } else if (gluedBefore || gluedAfter) {
        // The joined token is not statically knowable, so the whole template
        // fails closed. Fragment values are never walked as whole classes.
        sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
      } else {
        // Whitespace-separated on both sides: the interpolation supplies
        // whole class names and must be inspected — a proven forwarded value
        // yields its exact blocking extension boundary; anything unproven
        // fails closed inside the shared expression walker.
        recurse(span.expression, sink)
      }
    })
  }
  return { walkClassBinary, walkDynamicTemplate }
}

// Capability A's proof for a template span glued to adjacent static text:
// true only when every leaf of `node` (through nested conditionals) is
// either an empty string/no-substitution-template literal, or a string
// literal / template expression whose own boundary text already supplies
// the missing whitespace on every side the outer static text is glued on.
// A template-expression leaf's INTERIOR interpolations are not inspected
// here — they are resolved by the ordinary recursive template walk once
// this leaf is reached as separated. Any other expression shape (identifier,
// call, member access, non-whitespace-bounded literal) fails the proof.
export function isSelfSeparatingConditional(node, gluedBefore, gluedAfter) {
  const expression = unwrapStatic(node)
  if (ts.isConditionalExpression(expression)) {
    return isSelfSeparatingConditional(expression.whenTrue, gluedBefore, gluedAfter)
      && isSelfSeparatingConditional(expression.whenFalse, gluedBefore, gluedAfter)
  }
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
    const text = expression.text
    if (text === '') return true
    if (gluedBefore && !/^\s/.test(text)) return false
    if (gluedAfter && !/\s$/.test(text)) return false
    return true
  }
  if (ts.isTemplateExpression(expression)) {
    const head = expression.head.text
    const tail = expression.templateSpans[expression.templateSpans.length - 1].literal.text
    if (gluedBefore && !/^\s/.test(head)) return false
    if (gluedAfter && !/\s$/.test(tail)) return false
    return true
  }
  return false
}

export function createStyleWalker({ report, positionAt, sourceFile, registeredTokens, resolvedTokenValues, moduleContext }) {
  const emit = createStyleVerdictEmitter({ report })
  function walkStyleProperty(node) {
    const name = stylePropertyName(node)
    if (!name) return
    const at = positionAt(node.name.getStart(sourceFile))
    if (STYLE_SIZE_PROPERTY_NAMES.has(name)) {
      reportStyleSize(node.initializer, at)
      return
    }
    if (STYLE_FAMILY_PROPERTY_NAMES.has(name)) {
      reportStyleFamily(node.initializer, at)
      return
    }
    if (STYLE_SHORTHAND_PROPERTY_NAMES.has(name)) {
      reportStyleShorthand(node.initializer, at)
      return
    }
    if (STYLE_ROLE_PROPERTIES.has(name)) reportRoleValue(node.initializer, name, at)
  }

  function walkJsxValue(node, name) {
    if (!node.initializer) return
    const at = positionAt(node.name.getStart(sourceFile))
    const value = ts.isJsxExpression(node.initializer) ? node.initializer.expression : node.initializer
    if (!value) return
    if (name === 'fontSize') reportStyleSize(value, at)
    else reportStyleFamily(value, at)
  }

  function reportRoleValue(initializer, name, at) {
    const contract = STYLE_ROLE_PROPERTIES.get(name)
    let value = collapseWhitespace(initializer.getText(sourceFile))
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer) || ts.isNumericLiteral(initializer)) value = initializer.text
    if (['inherit', 'initial', 'unset', 'revert', 'revert-layer', 'normal'].includes(value)) return
    const variable = VAR_REFERENCE_PATTERN.exec(value)
    if (variable && registeredTokens.has(variable[1])) return
    report({
      ruleId: variable ? 'typography/unregistered-typography-token' : contract.ruleId,
      syntax: `${name}: ${value}`,
      message: variable ? `${name} references an unregistered typography token.` : `${name} is an arbitrary D9 typography value; use a registered role token.`,
      ...at,
    })
  }

  function reportStyleSize(initializer, at) {
    const resolved = resolveImportedExpression(initializer, moduleContext)
    if (resolved && resolved !== initializer) return reportStyleSize(resolved, at)
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer)) {
      emit.emitSizeVerdict(evaluateFontSizeValue(initializer.text, registeredTokens, resolvedTokenValues), 'fontSize', at)
      return
    }
    if (ts.isNumericLiteral(initializer)) {
      const value = Number.parseFloat(initializer.text)
      emit.emitSizeVerdict(
        { status: value < FLOOR_PX ? 'below-floor' : 'ok', printed: initializer.text },
        'fontSize',
        at,
      )
      return
    }
    if (ts.isTemplateExpression(initializer)) {
      const skeleton = templateSkeleton(initializer)
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `fontSize: ${skeleton}`,
        message: `fontSize: ${skeleton} is a dynamic expression the static lock cannot bound against the ${FLOOR_PX}px floor (§D2). Bind it to a registered size token or prove it in the browser harness.`,
        ...at,
      })
      return
    }
    report({
      ruleId: 'typography/unsupported-font-size',
      syntax: `fontSize: ${collapseWhitespace(initializer.getText(sourceFile))}`,
      message: `This fontSize value is not a literal the static lock can evaluate against the ${FLOOR_PX}px floor (§D2).`,
      ...at,
    })
  }

  function reportStyleFamily(initializer, at) {
    const resolved = resolveImportedExpression(initializer, moduleContext)
    if (resolved && resolved !== initializer) return reportStyleFamily(resolved, at)
    if (ts.isStringLiteral(initializer) || ts.isNoSubstitutionTemplateLiteral(initializer)) {
      emit.emitFamilyVerdict(evaluateFontFamilyValue(initializer.text, registeredTokens), 'fontFamily', at)
      return
    }
    report({
      ruleId: 'typography/font-family-literal',
      syntax: `fontFamily: ${collapseWhitespace(initializer.getText(sourceFile))}`,
      message: 'This fontFamily value is not statically evaluable; §D9 requires families to resolve to the token set.',
      ...at,
    })
  }

  function reportStyleShorthand(initializer, at) {
    if (!ts.isStringLiteral(initializer) && !ts.isNoSubstitutionTemplateLiteral(initializer)) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.getText(sourceFile))}`,
        message: `This font shorthand is not a literal the static lock can evaluate against the ${FLOOR_PX}px floor (§D2).`,
        ...at,
      })
      return
    }
    const parsed = parseFontShorthand(initializer.text)
    if (parsed.systemKeyword) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.text)}`,
        message: 'System font keywords carry an implementation-defined size the static lock cannot bound against the 12px floor.',
        ...at,
      })
      return
    }
    if (parsed.unsupported) {
      report({
        ruleId: 'typography/unsupported-font-size',
        syntax: `font: ${collapseWhitespace(initializer.text)}`,
        message: 'This font shorthand could not be split into size and family by the static lock.',
        ...at,
      })
      return
    }
    if (parsed.size) {
      const verdict = evaluateFontSizeValue(parsed.size, registeredTokens)
      if (verdict.status !== 'ok') emit.emitSizeVerdict({ ...verdict, printed: `${verdict.printed} …` }, 'font', at)
    }
    if (parsed.family && parsed.family.trim().length > 0) {
      const verdict = evaluateFontFamilyValue(parsed.family, registeredTokens)
      if (verdict.status !== 'ok') emit.emitFamilyVerdict({ ...verdict, printed: `… ${verdict.printed}` }, 'font', at)
    }
  }

  function templateSkeleton(template) {
    let skeleton = template.head.text
    for (const span of template.templateSpans) {
      skeleton += '${…}' + span.literal.text
    }
    return collapseWhitespace(skeleton)
  }
  return { walkStyleProperty, walkJsxValue }
}

export function createStyleVerdictEmitter({ report }) {
  function emitSizeVerdict({ status, printed }, label, at) {
    if (status === 'ok') return
    if (status === 'below-floor') {
      report({
        ruleId: 'typography/font-size-below-floor',
        syntax: `${label}: ${printed}`,
        message: `${label}: ${printed} can compute below the ${FLOOR_PX}px floor under the §D1 root range (${ROOT_MIN_PX}–${ROOT_MAX_PX}px); §D2 forbids any UI text below ${FLOOR_PX}px.`,
        ...at,
      })
      return
    }
    if (status === 'unregistered') {
      report({
        ruleId: 'typography/unregistered-font-size-token',
        syntax: `${label}: ${printed}`,
        message: `${label}: ${printed} references a custom property that is not a registered typography token (§D9).`,
        ...at,
      })
      return
    }
    if (status === 'runtime-preference-boundary') {
      report({
        ruleId: 'typography/extension-boundary',
        syntax: `${label}: ${printed}`,
        message: `${label}: ${printed} resolves the runtime user font-size preference (--user-font-size, §D1) against otherwise fully registered floor/ceiling tokens; this is a centrally-reviewed extension boundary, not an unregistered token, and stays blocking until exact central review.`,
        ...at,
      })
      return
    }
    report({
      ruleId: 'typography/unsupported-font-size',
      syntax: `${label}: ${printed}`,
      message: `${label}: ${printed} cannot be bounded against the ${FLOOR_PX}px floor statically (relative or dynamic units); §D2 needs a registered token, a provable expression, or browser-harness proof.`,
      ...at,
    })
  }

  function emitFamilyVerdict({ status, printed }, label, at) {
    if (status === 'ok') return
    report({
      ruleId: status === 'unregistered' ? 'typography/unregistered-font-size-token' : 'typography/font-family-literal',
      syntax: `${label}: ${printed}`,
      message: status === 'unregistered'
        ? `${label}: ${printed} references an unregistered custom property (§D9 family token boundary).`
        : `${label}: ${printed} is a literal family stack; §D9 requires the Outfit/Inter/JetBrains Mono tokens.`,
      ...at,
    })
  }
  return { emitSizeVerdict, emitFamilyVerdict }
}

// P11 precision fix: a computed property key that is itself a string
// literal, just wrapped by a cast/assertion (`['fontSize' as string]: …`,
// `(['fontSize'])`, `!` non-null, `satisfies`), was invisible to this
// dispatcher — `ts.isComputedPropertyName(name)` matched, but neither the
// plain-literal nor the two-literal-PlusToken branch fired for an
// AsExpression-wrapped literal, so `stylePropertyName` returned null and
// `walkStyleProperty` silently skipped the property entirely (a
// `fontSize`/`fontFamily`/`font`/role value written this way was never
// checked against the D2 floor or the D9 token set at all — a bigger gap
// than an over-block, and strictly a coverage GAIN to close, never a
// loosening of fail-closed). `unwrapStatic` is the same cast-stripping this
// file already trusts everywhere else (class expressions, imported
// records); using it here just makes the computed-key reader consistent
// with the rest of the scanner. Only the identifier NAME becomes resolvable
// — the property's VALUE still goes through the exact same reportStyleSize/
// reportStyleFamily/reportRoleValue checks as a plain `fontSize: …` key, so
// a key that resolves to a name typography does not track (e.g. `'color'`)
// is unaffected: it still matches none of the STYLE_*_PROPERTY_NAMES /
// STYLE_ROLE_PROPERTIES sets and stays silently out of scope, exactly as a
// literal `color: …` key already is today.
export function stylePropertyName(node) {
  const name = node.name
  if (ts.isIdentifier(name)) return name.text
  if (ts.isStringLiteral(name)) return name.text
  if (ts.isComputedPropertyName(name)) {
    const expression = unwrapStatic(name.expression)
    if (expression && (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression))) return expression.text
    if (expression && ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = unwrapStatic(expression.left); const right = unwrapStatic(expression.right)
      if (left && right && ts.isStringLiteral(left) && ts.isStringLiteral(right)) return left.text + right.text
    }
  }
  return null
}

// ClassExpressionEngine holds createClassWalker's ~20 mutually-recursive
// closures as methods on one instance — constructor params become instance
// fields, sibling closures become sibling methods called via this.<name> —
// because a class declaration is not itself a function-like node the
// function-size scanner counts, so each method is measured on its own span
// instead of the whole bundle's. createClassWalker below is the public
// entry point.
class ClassExpressionEngine {
  constructor({ report, positionAt, sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary, parameterMemberBoundary, scannedParameterDefaults, moduleContext, handledCalls }) {
    this.report = report
    this.positionAt = positionAt
    this.sourceFile = sourceFile
    this.registeredTokens = registeredTokens
    this.bindings = bindings
    this.duplicateBindings = duplicateBindings
    this.forwardedClassBoundary = forwardedClassBoundary
    this.parameterMemberBoundary = parameterMemberBoundary
    this.scannedParameterDefaults = scannedParameterDefaults
    this.moduleContext = moduleContext
    this.handledCalls = handledCalls
    this.nodeHelpers = createClassNodeHelpers(sourceFile)
    // Finding 3 fix, round 2: declarations currently "in progress" on the
    // active finite-call resolution chain (push before walking a resolved
    // call's returns, pop after) — see finiteCallReturns' declarationMarker
    // doc comment. Scoped to one walker/file, not permanently accumulating:
    // a marker is only ever present while its own subtree is still being
    // walked, so sibling (non-nested) calls to the same helper elsewhere are
    // never wrongly blocked.
    this.finiteCallStack = new Set()
  }

  // A sink bound to one source range; token positions refine it.
  classReporterFor(node) {
    return this.makeSink(this.positionAt(node.getStart(this.sourceFile)))
  }

  makeSink(base) {
    return {
      emit: (finding, position) => {
        const at = position ?? base
        this.report({ ...finding, line: at.line, column: at.column })
      },
      isRegistered: (name) => this.registeredTokens.has(name),
      registeredTokens: this.registeredTokens,
    }
  }

  classifyClassString(text, node) {
    this.classifyInto(text, node, this.classReporterFor(node))
  }

  walkClassExpression(node, sink) {
    if (!node) return
    switch (node.kind) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
        this.classifyInto(node.text, node, sink)
        return
      case ts.SyntaxKind.TemplateExpression:
        this.nodeHelpers.walkDynamicTemplate(node, sink, this.walkClassExpression.bind(this))
        return
      case ts.SyntaxKind.ConditionalExpression:
        this.walkClassExpression(node.whenTrue, sink)
        this.walkClassExpression(node.whenFalse, sink)
        return
      case ts.SyntaxKind.BinaryExpression:
        if (node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
          const alternatives = this.expandStaticClassStrings(node)
          if (alternatives) {
            alternatives.forEach((text) => this.classifyInto(text, node, sink))
            return
          }
          const staticText = this.collectStaticTextForBinding(node)
          if (staticText !== null) {
            this.classifyInto(staticText, node, sink)
            return
          }
        }
        this.nodeHelpers.walkClassBinary(node, sink, this.walkClassExpression.bind(this))
        return
      case ts.SyntaxKind.CallExpression: {
        // Known builders carry class content in their arguments; any other
        // call resolves to a class string the lock cannot see (e.g. a cva
        // variant resolver — its literal base/variants were already scanned
        // at the declaration site; the resolved variant is dynamic).
        const callee = node.expression
        if (ts.isIdentifier(callee) && CLASS_BUILDERS.has(callee.text)
          && authenticClassBuilderCallee(callee, { sourceFile: this.sourceFile, bindings: this.bindings, duplicateBindings: this.duplicateBindings })) {
          node.arguments.forEach((argument) => this.walkClassExpression(argument, sink))
          return
        }
        const variantDefinition = cvaDefinitionFor(callee, this.moduleContext)
        if (variantDefinition) {
          if (!this.handledCalls.has(variantDefinition)) variantDefinition.arguments.forEach((argument) => this.walkClassExpression(argument, sink))
          return
        }
        if (ts.isPropertyAccessExpression(callee) && callee.name.text === 'join'
          && node.arguments.length <= 1
          && (!node.arguments[0] || ((ts.isStringLiteral(node.arguments[0]) || ts.isNoSubstitutionTemplateLiteral(node.arguments[0])) && /^\s*$/.test(node.arguments[0].text)))) {
          const joined = this.localInitializer(callee.expression)
          if (joined && ts.isArrayLiteralExpression(joined)) {
            joined.elements.forEach((element) => this.walkClassExpression(element, sink))
            return
          }
        }
        if (ts.isPropertyAccessExpression(callee) && callee.name.text === 'trim' && node.arguments.length === 0) {
          this.walkClassExpression(callee.expression, sink)
          return
        }
        const returns = pureFunctionReturns(node, this.moduleContext)
        if (returns) {
          returns.forEach((returned) => this.walkClassExpression(returned, sink))
          return
        }
        const joinerDeclaration = this.transparentJoinerDeclaration(callee)
        if (joinerDeclaration) {
          node.arguments.forEach((argument) => this.walkClassExpression(argument, sink))
          return
        }
        // Broader than pureFunctionReturns above (single-statement, fully
        // pure return only): a local/imported/IIFE callee whose body is a
        // finite if-chain and/or switch of return statements (BULK_BUTTON_CLASS
        // style, or BrowserLiveView's `(() => { if (...) return {...}; ...
        // })()` chip config). Tried second so the stricter, longer-proven
        // path above still wins whenever both would apply.
        {
          const finiteReturns = finiteCallReturns(node, this.moduleContext, this.finiteCallStack)
          if (finiteReturns) {
            const declaration = calleeDeclaration(node, this.moduleContext)
            const marker = declarationMarker(declaration)
            this.finiteCallStack.add(marker)
            try {
              finiteReturns.forEach((returned) => this.walkClassExpression(returned, sink))
            } finally {
              this.finiteCallStack.delete(marker)
            }
            return
          }
        }
        this.emitUnsupported(node, sink)
        return
      }
      case ts.SyntaxKind.ObjectLiteralExpression:
        node.properties.forEach((property) => {
          if (ts.isPropertyAssignment(property)) { this.walkClassExpression(property.initializer, sink); return }
          if (ts.isShorthandPropertyAssignment(property)) { this.walkClassExpression(property.name, sink); return }
          if (ts.isSpreadAssignment(property)) {
            // FIX-P2 (2026-09-20, confirmed-defect closure): a spread member
            // used to be dropped here with no recursion and no unsupported
            // finding at all — every OTHER object-literal path in this file
            // detects a spread and fails closed (classCarryingLeaves,
            // importedRecordAllValues, arrayElementPropertyValues,
            // derivedMemberTargets); this was the one silent exception,
            // reachable from inside every class-builder call and every cva
            // variants map (`clsx({ ...extra, visible: 'text-[9px]' })`
            // reported only `visible`; the spread-reached value vanished).
            // A spread whose source resolves to a provably stable (never
            // reassigned/mutated/escaped — resolveRecordObjectLiteral's own
            // absenceBindingUsesSafe/derivedValueEscapeSafe/
            // exportNeverMutatedByImporters proofs) local or imported const
            // object literal is walked exactly like an inline object
            // literal, recursively — so a further nested spread/shorthand
            // inside IT is proven the same way, or itself fails closed.
            // Anything else fails closed here instead of vanishing.
            const resolved = this.resolveRecordObjectLiteral(property.expression)
            if (resolved) { this.walkClassExpression(resolved, sink); return }
            this.emitUnsupported(property, sink)
            return
          }
          // Any other object-literal member kind (a method, a get/set
          // accessor) is not a class-content shape this lock can prove —
          // fail closed rather than silently drop it, matching every other
          // property kind above.
          this.emitUnsupported(property, sink)
        })
        return
      case ts.SyntaxKind.ArrayLiteralExpression:
        node.elements.forEach((element) => this.walkClassExpression(element, sink))
        return
      case ts.SyntaxKind.ParenthesizedExpression:
      case ts.SyntaxKind.AsExpression:
      case ts.SyntaxKind.NonNullExpression:
      case ts.SyntaxKind.SatisfiesExpression:
      case ts.SyntaxKind.TypeAssertionExpression:
        this.walkClassExpression(node.expression, sink)
        return
      case ts.SyntaxKind.Identifier:
        return this.walkClassIdentifier(node, sink)
      case ts.SyntaxKind.PropertyAccessExpression:
      case ts.SyntaxKind.ElementAccessExpression: {
        const parameterBoundary = this.parameterMemberBoundary(node)
        if (parameterBoundary) {
          sink.emit({
            ruleId: 'typography/extension-boundary', syntax: `${parameterBoundary.symbol}#${parameterBoundary.name}`,
            message: 'Unchanged caller-supplied className member crosses a component extension boundary and remains blocking until exact central review.',
          })
          return
        }
        const proven = this.resolveProvenPropertyAccess(node)
        if (proven) {
          if (proven.unprovable) { this.emitUnsupported(node, sink); return }
          proven.values.forEach((value) => this.walkClassExpression(value, sink))
          return
        }
        const importedRecord = this.importedRecordAllValues(node)
        if (importedRecord) {
          importedRecord.forEach((value) => this.walkClassExpression(value, sink))
          return
        }
        const candidates = this.localCandidates(node)
        if (candidates.length > 0) {
          candidates.forEach((candidate) => this.walkClassExpression(candidate, sink))
          return
        }
        const resolved = resolveImportedExpression(node, this.moduleContext)
        if (resolved) {
          this.walkClassExpression(resolved, sink)
          return
        }
        const arrayCallback = this.resolveArrayCallbackPropertyAccess(node)
        if (arrayCallback) {
          if (arrayCallback.unprovable) { this.emitUnsupported(node, sink); return }
          arrayCallback.values.forEach((value) => this.walkClassExpression(value, sink))
          return
        }
        this.emitUnsupported(node, sink)
        return
      }
      case ts.SyntaxKind.FalseKeyword:
      case ts.SyntaxKind.TrueKeyword:
      case ts.SyntaxKind.NullKeyword:
      case ts.SyntaxKind.NumericLiteral:
        return
      default:
        this.emitUnsupported(node, sink)
    }
  }

  // Handles walkClassExpression's Identifier case -- self-contained,
  // returns on every path, no fallthrough.
  walkClassIdentifier(node, sink) {
        if (node.text === 'undefined') return
        // P10: this identifier IS the parameter of the CLASS_BUILDER
        // function currently being DEFINED, forwarded unchanged into
        // another CLASS_BUILDER call — the definition site itself, not a
        // live call-site usage. See classBuilderOwnParameterForward.
        if (classBuilderOwnParameterForward(node)) return
        {
          const boundary = this.forwardedClassBoundary(node)
          if (boundary) {
            if (boundary.initializer && !this.scannedParameterDefaults.has(boundary.declaration)) {
              this.scannedParameterDefaults.add(boundary.declaration)
              this.walkClassExpression(boundary.initializer, sink)
            }
            sink.emit({
              ruleId: 'typography/extension-boundary', syntax: `${boundary.symbol}#${boundary.name}`,
              message: 'Unchanged caller-supplied className crosses a component extension boundary and remains blocking until exact central review.',
            })
            return
          }
        }
        {
          // Both the nearest-scope lexical lookup AND the flat top-level
          // `bindings` Map fallback must respect hasMultipleVariableDeclarations:
          // the same name reused ANYWHERE else in the file (e.g. a `let`
          // shadow inside a different, unrelated function) means findLexicalBinding's
          // const-only scope walk could climb past that shadow to the wrong
          // (but still textually valid) outer const — the top-level `bindings`
          // Map fallback must fail closed the same way, not bypass the guard
          // that disabled the lexical path in the first place (previously a
          // silent-pass gap: probed and closed).
          const ambiguousName = this.duplicateBindings.has(node.text) || hasMultipleVariableDeclarations(this.sourceFile, node.text)
          const lexical = ambiguousName ? null : findLexicalBinding(node, node.text)
          const binding = lexical?.initializer ?? (!ambiguousName && this.bindings.has(node.text) ? this.bindings.get(node.text) : null)
          if (binding) {
            if (ts.isBinaryExpression(binding) && binding.operatorToken.kind === ts.SyntaxKind.PlusToken && this.collectStaticTextForBinding(binding) === null) {
              this.emitUnsupported(node, sink)
              return
            }
            this.walkClassExpression(binding, sink)
            return
          }
        }
        {
          const resolved = resolveImportedExpression(node, this.moduleContext)
          if (resolved) {
            this.walkClassExpression(resolved, sink)
            return
          }
        }
        {
          const destructured = this.duplicateBindings.has(node.text) || hasMultipleVariableDeclarations(this.sourceFile, node.text)
            ? null : this.findDestructuredBinding(node, node.text)
          if (destructured) {
            const leaves = this.classCarryingLeaves(destructured.source)
            if (leaves) {
              // Collect first, emit after (rather than walking each found
              // leaf inline): a missing leaf discovered LATER in the loop
              // can still fail the whole read closed, and nothing should
              // have been emitted for the found leaves in that case.
              const values = []
              let missingLeaf = false
              let unprovable = false
              for (const leaf of leaves) {
                const property = leaf.properties.find((candidate) => propertyKeyName(candidate) === destructured.property)
                if (!property) { missingLeaf = true; continue }
                if (ts.isPropertyAssignment(property)) values.push(property.initializer)
                else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
                else { unprovable = true; break }
              }
              // Same LEAD DECISION absence standard as resolveProvenPropertyAccess:
              // a missing leaf is only a safe no-op when absenceValue proves it
              // (null-prototype, no computed keys/spreads, safe top-level factory).
              if (unprovable || (missingLeaf && !absenceValue(destructured.source, destructured.property))) {
                this.emitUnsupported(node, sink)
                return
              }
              if (values.length > 0) { values.forEach((value) => this.walkClassExpression(value, sink)); return }
              if (destructured.defaultExpr) { this.walkClassExpression(destructured.defaultExpr, sink); return }
              return // proven absent on every branch (absenceValue passed above): no class content, safe no-op
            }
          }
        }
        this.emitUnsupported(node, sink)
        return
  }

  emitUnsupported(node, sink) {
    sink.emit({ ruleId: 'typography/unsupported-text-utility', syntax: node.getText(this.sourceFile), message: DYNAMIC_UTILITY_MESSAGE })
  }

  // A same-file top-level `function name(...rest) { return rest(.filter(Boolean))?.join(sep) }`
  // — a transparent variadic class joiner sharing cn()/clsx()'s semantics
  // under a project-local name (ChipListInput.tsx's `classes()`). Detected
  // structurally rather than allowlisted by name: only a single rest
  // parameter, a single statement, and a `.join()` call (optionally preceded
  // by `.filter(Boolean)`) directly on that same rest parameter qualify —
  // anything else (extra statements, a different receiver, additional
  // transforms) is not provably transparent and is left alone.
  transparentJoinerDeclaration(callee) {
    if (!ts.isIdentifier(callee)) return null
    const fnDecl = sameFileFunctionDeclaration(callee.text, this.sourceFile)
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

  isLeafSplittingOwner(node) {
    return ts.isCallExpression(node) || ts.isConditionalExpression(node)
      || (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken))
  }

  // A top-level `const NAME = <expr>` declaration node, resolved the same way
  // the `bindings` Map is populated (scanTypeScript's own top-level scan)
  // but returning the declaration itself, not just its initializer — needed
  // so callers can run the LEAD DECISION binding-safety proof
  // (absenceBindingUsesSafe) against it.
  topLevelConstDeclaration(name) {
    for (const statement of this.sourceFile.statements) {
      if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
      for (const declaration of statement.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name) && declaration.name.text === name && declaration.initializer) return declaration
      }
    }
    return null
  }

  // The owner expression a property-access base resolves to, restricted to
  // the NEW shapes handled by resolveProvenPropertyAccess/classCarryingLeaves
  // below (a call, a conditional, or a `??`/`||` fallback chain of such) —
  // plain object-literal owners keep going through the pre-existing
  // localCandidates path unchanged, so this never re-decides a case that
  // already worked. Returns `{ expr, declaration }`: `declaration` is the
  // const variable declaration an identifier hop went through (null when the
  // owner IS the call/conditional directly, e.g. no intermediate variable) —
  // LEAD DECISION requires that declaration's binding to be proven safe
  // (absenceBindingUsesSafe) before any conclusion drawn from it is trusted.
  ownerExpressionFor(expression) {
    const unwrapped = unwrapStatic(expression)
    if (!unwrapped) return null
    if (this.isLeafSplittingOwner(unwrapped)) return { expr: unwrapped, declaration: null }
    if (ts.isIdentifier(unwrapped) && !this.duplicateBindings.has(unwrapped.text) && !hasMultipleVariableDeclarations(this.sourceFile, unwrapped.text)) {
      const lexical = findLexicalBinding(unwrapped, unwrapped.text)
      const initializer = lexical?.initializer ?? (this.bindings.has(unwrapped.text) && !this.duplicateBindings.has(unwrapped.text) ? this.bindings.get(unwrapped.text) : null)
      const resolved = initializer ? unwrapStatic(initializer) : null
      if (!resolved || !this.isLeafSplittingOwner(resolved)) return null
      const declaration = lexical?.declaration ?? this.topLevelConstDeclaration(unwrapped.text)
      return { expr: resolved, declaration }
    }
    return null
  }

  // Resolves a bare identifier to a top-level/lexical const-bound, or
  // imported, object literal — a "record" (STATUS_BADGE/PRIORITY_BADGE
  // style). Rejects spreads at the call sites below, not here. The local
  // branch is already guaranteed const (findLexicalBinding/the top-level
  // `bindings` Map only ever collect `const` declarations). The IMPORTED
  // branch is not — moduleRecord's export collection accepts any
  // VariableStatement regardless of const/let/var — so it must additionally
  // require the exporting declaration to be `const` AND never mutated in its
  // own module (absenceBindingUsesSafe): an exported mutable binding can be
  // reassigned to an arbitrary value by another importer and stays
  // unsupported (LEAD DECISION, "never-mutated const literal").
  resolveRecordObjectLiteral(node) {
    const unwrapped = unwrapStatic(node)
    if (!unwrapped || !ts.isIdentifier(unwrapped)) return null
    if (this.duplicateBindings.has(unwrapped.text) || hasMultipleVariableDeclarations(this.sourceFile, unwrapped.text)) return null
    const lexical = findLexicalBinding(unwrapped, unwrapped.text)
    const localValue = lexical?.initializer ?? (this.bindings.has(unwrapped.text) ? this.bindings.get(unwrapped.text) : null)
    const local = localValue ? unwrapStatic(localValue) : null
    if (local && ts.isObjectLiteralExpression(local)) {
      // LEAD DECISION (Finding 1 fix, round 2): the LOCAL branch was
      // previously trusted purely because `bindings`/`findLexicalBinding`
      // only ever collect `const` declarations — but const-only blocks
      // *reassignment*, not a later member write (`config.small = '...'`)
      // onto the SAME never-reassigned binding. Require the same
      // never-mutated proof the imported branch below already carries.
      const localDeclaration = lexical?.declaration ?? this.topLevelConstDeclaration(unwrapped.text)
      // LEAD DECISION (round 3, derived-value escape proof): never-mutated
      // (absenceBindingUsesSafe) is not enough — REC's own record binding
      // being safe says nothing about a NESTED value read off it (`REC.a`)
      // escaping into a container/call/return/reassignment elsewhere in
      // scope and being mutated THROUGH that alias. See
      // derivedValueEscapeSafe's doc comment.
      if (!localDeclaration || !absenceBindingUsesSafe(localDeclaration, false, true) || !derivedValueEscapeSafe(localDeclaration, [local])) return null
      return local
    }
    const importedDecl = importedDeclaration(unwrapped, this.moduleContext)
    if (importedDecl && ts.isVariableDeclaration(importedDecl) && importedDecl.initializer
      && (importedDecl.parent.flags & ts.NodeFlags.Const) && absenceBindingUsesSafe(importedDecl, false, true)) {
      const imported = unwrapStatic(importedDecl.initializer)
      const importedContainers = imported && ts.isObjectLiteralExpression(imported) ? [imported] : null
      if (importedContainers
        // LEAD DECISION (Finding 2 fix, round 2): never-mutated-in-its-own-module
        // is not enough — a DIFFERENT importer can still mutate the exported
        // record's members from its own `ts.SourceFile`, invisible to the
        // check above. Require the same proof across every governed module.
        // Round 3: exportNeverMutatedByImporters now also runs the
        // derived-value escape proof per importer (containers threaded
        // through); the EXPORTING module's own copy of that same proof
        // still runs here too.
        && exportNeverMutatedByImporters(importedDecl, this.moduleContext, importedContainers)
        && derivedValueEscapeSafe(importedDecl, importedContainers)) return imported
    }
    return null
  }

  // The finite set of object-literal "leaves" a class-carrying expression can
  // resolve to: the expression itself if it is already an object literal
  // (rejecting spreads — an unproven source of extra properties), every leaf
  // of a (possibly nested) conditional or `??`/`||` fallback chain, every
  // finite return of a call, or a record lookup (RECORD[key] / RECORD.key,
  // local or imported — a literal key selects one property, proven absent is
  // an empty-but-valid result; a dynamic key enumerates every value, mirroring
  // importedRecordAllValues but composable inside a leaf chain, e.g.
  // `PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]`) — each recursively
  // reduced the same way. Returns null if any branch is not provably one of
  // these shapes (fail closed — no partial results).
  classCarryingLeaves(node, seen = new Set()) {
    const expression = unwrapStatic(node)
    if (!expression) return null
    if (ts.isObjectLiteralExpression(expression)) {
      return expression.properties.some((property) => ts.isSpreadAssignment(property)) ? null : [expression]
    }
    if (ts.isConditionalExpression(expression)) {
      const yes = this.classCarryingLeaves(expression.whenTrue, seen)
      const no = this.classCarryingLeaves(expression.whenFalse, seen)
      return yes && no ? [...yes, ...no] : null
    }
    if (ts.isBinaryExpression(expression) && (expression.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || expression.operatorToken.kind === ts.SyntaxKind.BarBarToken)) {
      const left = this.classCarryingLeaves(expression.left, seen)
      const right = this.classCarryingLeaves(expression.right, seen)
      return left && right ? [...left, ...right] : null
    }
    if (ts.isCallExpression(expression)) {
      if (seen.has(expression)) return null
      const nextSeen = new Set(seen).add(expression)
      const branches = finiteCallReturns(expression, this.moduleContext)
      if (!branches) return null
      const leaves = []
      for (const branch of branches) {
        const nested = this.classCarryingLeaves(branch, nextSeen)
        if (!nested) return null
        leaves.push(...nested)
      }
      return leaves
    }
    if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
      const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
        : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression) || ts.isNumericLiteral(expression.argumentExpression))
          ? expression.argumentExpression.text : null
      const object = this.resolveRecordObjectLiteral(expression.expression)
      if (!object) return null
      // FIX-P2 (2026-09-20, confirmed-defect closure): this spread check
      // used to run only below, gating the key===null (enumerate-everything)
      // branch — the key!==null branch looked a single static key up with
      // `.find()` and, on a miss, returned [] ("proven absent"), even when
      // the object had a spread that could genuinely carry that key.
      // Hoisted above both branches: a spread anywhere in the object means
      // neither branch can trust what IS or ISN'T on it, matching
      // derivedMemberTargets' identical ordering elsewhere in this file.
      if (object.properties.some((property) => ts.isSpreadAssignment(property))) return null
      if (key !== null) {
        const property = object.properties.find((candidate) => propertyKeyName(candidate) === key)
        if (!property) return [] // proven absent on this record entry: no class content, safe
        if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
        return this.classCarryingLeaves(ts.isPropertyAssignment(property) ? property.initializer : property.name, seen)
      }
      const leaves = []
      for (const property of object.properties) {
        if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
        const nested = this.classCarryingLeaves(ts.isPropertyAssignment(property) ? property.initializer : property.name, seen)
        if (!nested) return null
        leaves.push(...nested)
      }
      return leaves
    }
    return null
  }

  // Capability C1/C2 (independent review) applied to property access:
  // `X.prop`/`X?.prop`/`X['prop']` where X (after at most one identifier
  // hop) is a call or conditional expression provably reducible to a finite
  // set of object-literal leaves (classCarryingLeaves). A leaf missing the
  // property contributes nothing (proven absent, safe); a leaf whose
  // matching property is a getter/method/computed key is unprovable and
  // fails the WHOLE access closed (never a silent partial result).
  resolveProvenPropertyAccess(node) {
    const key = ts.isPropertyAccessExpression(node) ? node.name.text
      : node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
        ? node.argumentExpression.text : null
    if (key === null) return null
    const owner = this.ownerExpressionFor(node.expression)
    if (!owner) return null
    // LEAD DECISION: an identifier hop's binding must be proven safe (never
    // reassigned/aliased/mutated/escaped anywhere in its enclosing block)
    // BEFORE any leaf — found or missing — is trusted. This gates PRESENT
    // branch enumeration too, not only the absence proof below (a member
    // write to a DIFFERENT property after the call still disqualifies the
    // whole binding, since it proves the object escapes untracked mutation).
    if (owner.declaration && !absenceBindingUsesSafe(owner.declaration, false, true)) return null
    const leaves = this.classCarryingLeaves(owner.expr)
    if (!leaves) return null
    // Round 3: same derived-value escape proof as resolveRecordObjectLiteral
    // — the identifier hop's binding being never-reassigned/mutated says
    // nothing about a nested (non-primitive) value read off it escaping
    // into a container/call/return/reassignment elsewhere in scope.
    if (owner.declaration && !derivedValueEscapeSafe(owner.declaration, leaves)) return null
    let missingLeaf = false
    const values = []
    for (const leaf of leaves) {
      const property = leaf.properties.find((candidate) => propertyKeyName(candidate) === key)
      if (!property) { missingLeaf = true; continue }
      if (ts.isPropertyAssignment(property)) values.push(property.initializer)
      else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
      else return { unprovable: true }
    }
    // A leaf lacking the key contributes nothing only when its absence is
    // independently proven (absenceValue — the ts-colors null-prototype
    // standard). Otherwise a missing static match could be a later member
    // write, a non-null-prototype object any caller can extend, or an
    // unsafe factory, and the whole read must fail closed (LEAD DECISION).
    if (missingLeaf && !absenceValue(owner.expr, key)) return { unprovable: true }
    return { unprovable: false, values }
  }

  // "Indexing into a finite const record with all values enumerated"
  // (existing localCandidates behavior) generalized to an IMPORTED record:
  // `IMPORTED_RECORD[dynamicKey]` where the static key can't be read but the
  // imported object literal's full, spread-free value set can — every value
  // is a valid candidate (STATUS_BADGE[status] style). A literal string key
  // is left to the existing resolveImportedExpression path untouched.
  importedRecordAllValues(node) {
    if (!ts.isElementAccessExpression(node)) return null
    const key = node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
      ? node.argumentExpression.text : null
    if (key !== null) return null
    const base = unwrapStatic(node.expression)
    if (!ts.isIdentifier(base)) return null
    const declaration = importedDeclaration(base, this.moduleContext)
    // LEAD DECISION ("never-mutated const literal"): moduleRecord's export
    // collection does not filter by const/let/var, so this must check it
    // itself — an exported `let`/`var` binding can be reassigned wholesale by
    // another importer, and a mutated-in-its-own-module binding can hold a
    // different shape than its initializer shows; both stay unsupported.
    // Finding 2 fix (round 2): never-mutated-in-its-OWN-module is not
    // enough — a DIFFERENT importer can still mutate the exported record's
    // members from its own file, invisible to absenceBindingUsesSafe above.
    if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer
      || !(declaration.parent.flags & ts.NodeFlags.Const) || !absenceBindingUsesSafe(declaration, false, true)) return null
    const object = unwrapStatic(declaration.initializer)
    if (!ts.isObjectLiteralExpression(object) || object.properties.some((property) => ts.isSpreadAssignment(property))) return null
    // Round 3: derived-value escape proof — a nested (non-primitive)
    // property value enumerated here can still escape into a container/
    // call/return/reassignment elsewhere in scope, either in this file or
    // (threaded through as `containers`) in the exporting module/another
    // importer's own scope.
    if (!exportNeverMutatedByImporters(declaration, this.moduleContext, [object]) || !derivedValueEscapeSafe(declaration, [object])) return null
    const values = []
    for (const property of object.properties) {
      if (ts.isPropertyAssignment(property)) values.push(property.initializer)
      else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
      else return null
    }
    return values
  }

  // Resolves the finite array of object-literal elements a class-carrying
  // `X.map((item) => ... item.prop ...)` maps over — `X` itself, or `X`
  // after unwrapping a leading `.filter(predicate)`/`.slice(...)` (neither
  // changes an element's own shape, only which/how many elements survive) —
  // to a top-level/lexical const-bound, or imported, ARRAY literal (the
  // array-shaped counterpart of resolveRecordObjectLiteral above; same
  // never-mutated/no-spread/derived-value-escape safety discipline, ported
  // for TaskDetailPanel.tsx's `STATUS_OPTIONS.filter(...).map((o) => ({
  // ..., className: cn('text-xs', o.color) }))`). Returns null (fail closed)
  // for anything not provably one of these shapes.
  resolveArrayLiteral(node) {
    const unwrapped = unwrapStatic(node)
    if (!unwrapped) return null
    if (ts.isCallExpression(unwrapped) && ts.isPropertyAccessExpression(unwrapped.expression)
      && (unwrapped.expression.name.text === 'filter' || unwrapped.expression.name.text === 'slice')) {
      return this.resolveArrayLiteral(unwrapped.expression.expression)
    }
    if (!ts.isIdentifier(unwrapped)) return null
    if (this.duplicateBindings.has(unwrapped.text) || hasMultipleVariableDeclarations(this.sourceFile, unwrapped.text)) return null
    const lexical = findLexicalBinding(unwrapped, unwrapped.text)
    const localValue = lexical?.initializer ?? (this.bindings.has(unwrapped.text) ? this.bindings.get(unwrapped.text) : null)
    const local = localValue ? unwrapStatic(localValue) : null
    if (local && ts.isArrayLiteralExpression(local)) {
      const localDeclaration = lexical?.declaration ?? this.topLevelConstDeclaration(unwrapped.text)
      // Array-specific never-mutated proof (see arrayBindingUsesSafe's own
      // doc comment) — NOT absenceBindingUsesSafe/derivedValueEscapeSafe,
      // which are correct for a RECORD but fail on ordinary array reads
      // (.filter()/.map()/.find()).
      if (!localDeclaration || !arrayBindingUsesSafe(localDeclaration)) return null
      return local
    }
    const importedDecl = importedDeclaration(unwrapped, this.moduleContext)
    if (importedDecl && ts.isVariableDeclaration(importedDecl) && importedDecl.initializer
      && (importedDecl.parent.flags & ts.NodeFlags.Const) && arrayBindingUsesSafe(importedDecl)) {
      const imported = unwrapStatic(importedDecl.initializer)
      if (imported && ts.isArrayLiteralExpression(imported)
        && exportNeverMutatedByImporters(importedDecl, this.moduleContext, null, arrayBindingUsesSafe)) return imported
    }
    return null
  }

  // Every value `key` can hold across a finite, never-mutated const array's
  // elements — rejecting a spread anywhere (the array itself or an element)
  // and any element that is not a plain object literal, the same
  // fail-closed discipline as resolveRecordObjectLiteral's record leaves. A
  // missing key on an element is safe only when absenceValue proves it
  // (LEAD DECISION parity with resolveProvenPropertyAccess).
  arrayElementPropertyValues(arrayLiteral, key) {
    if (arrayLiteral.elements.some((element) => ts.isSpreadElement(element))) return null
    const values = []
    for (const element of arrayLiteral.elements) {
      const item = unwrapStatic(element)
      if (!item || !ts.isObjectLiteralExpression(item) || item.properties.some((property) => ts.isSpreadAssignment(property))) return null
      const property = item.properties.find((candidate) => propertyKeyName(candidate) === key)
      if (!property) {
        if (!absenceValue(item, key)) return null
        continue
      }
      if (ts.isPropertyAssignment(property)) values.push(property.initializer)
      else if (ts.isShorthandPropertyAssignment(property)) values.push(property.name)
      else return null
    }
    return values
  }

  // `item.prop` (or `item['prop']`) read off the SOLE parameter of an arrow
  // function that is itself, structurally, the callback argument of a
  // `.map(...)` call whose receiver resolves via resolveArrayLiteral —
  // TaskDetailPanel.tsx's `o.color` inside `STATUS_OPTIONS.filter(...).map((o)
  // => ({ ..., className: cn('text-xs', o.color) }))`. Narrow and
  // structural, mirroring forwardedClassBoundary's own shape/reassignment
  // discipline: the identifier must be the callback's own parameter (not a
  // same-named shadow), the callback must be the literal first argument of
  // the `.map()` call it is nested in, and the parameter must never be
  // reassigned in the callback body before this read.
  resolveArrayCallbackPropertyAccess(node) {
    const key = ts.isPropertyAccessExpression(node) ? node.name.text
      : node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
        ? node.argumentExpression.text : null
    if (key === null) return null
    const base = unwrapStatic(node.expression)
    if (!base || !ts.isIdentifier(base)) return null
    let fn = base.parent
    while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
    if (!fn || fn.parameters.length !== 1) return null
    const parameter = fn.parameters[0]
    if (!ts.isIdentifier(parameter.name) || parameter.name.text !== base.text) return null
    if (plainDeclarationShadows(base, fn)) return null
    const callExpression = fn.parent
    if (!callExpression || !ts.isCallExpression(callExpression) || callExpression.arguments[0] !== fn) return null
    if (!ts.isPropertyAccessExpression(callExpression.expression) || callExpression.expression.name.text !== 'map') return null
    let reassigned = false
    function check(current) {
      if (ts.isBinaryExpression(current) && ts.isIdentifier(current.left) && current.left.text === base.text
        && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
      current.forEachChild(check)
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    const array = this.resolveArrayLiteral(callExpression.expression.expression)
    if (!array) return null
    const values = this.arrayElementPropertyValues(array, key)
    if (!values) return { unprovable: true }
    return { unprovable: false, values }
  }

  // A body-level destructured local — `const { x } = <expr>` — bound to a
  // call/conditional/object-literal `<expr>` (GoalIndicator's `const {
  // className } = describeNonActiveState(state)`). Distinct from bindings/
  // findLexicalBinding, which only track simple identifier declarations, not
  // destructuring patterns. Scoped to the innermost enclosing block, same
  // uniqueness discipline as the rest of this file: more than one qualifying
  // destructure of the same local name in scope is unprovable.
  findDestructuredBinding(identifier, name) {
    let scope = identifier.parent
    while (scope) {
      if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
        const matches = []
        for (const statement of scope.statements) {
          if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
          for (const declaration of statement.declarationList.declarations) {
            if (!ts.isObjectBindingPattern(declaration.name) || !declaration.initializer) continue
            for (const element of declaration.name.elements) {
              if (!ts.isBindingElement(element) || element.dotDotDotToken || !ts.isIdentifier(element.name) || element.name.text !== name) continue
              const property = element.propertyName?.getText(this.sourceFile) ?? element.name.text
              matches.push({ property, source: declaration.initializer, defaultExpr: element.initializer ?? null })
            }
          }
        }
        if (matches.length > 1) return null
        if (matches.length === 1) return matches[0]
      }
      scope = scope.parent
    }
    return null
  }

  localInitializer(node) {
    const expression = unwrapStatic(node)
    if (ts.isIdentifier(expression) && !this.duplicateBindings.has(expression.text) && !hasMultipleVariableDeclarations(this.sourceFile, expression.text)) {
      const lexical = findLexicalBinding(expression, expression.text)
      const initializer = lexical?.initializer ?? this.bindings.get(expression.text)
      if (initializer) return unwrapStatic(initializer)
    }
    return expression
  }

  localCandidates(node, seen = new Set()) {
    const expression = unwrapStatic(node)
    if (!expression || seen.has(expression)) return []
    seen.add(expression)
    if (ts.isIdentifier(expression)) {
      if (this.duplicateBindings.has(expression.text) || hasMultipleVariableDeclarations(this.sourceFile, expression.text)) return []
      const lexical = findLexicalBinding(expression, expression.text)
      const initializer = lexical?.initializer ?? (this.bindings.has(expression.text) ? this.bindings.get(expression.text) : null)
      if (!initializer) return []
      // LEAD DECISION (Finding 1 fix, round 2, parity with resolveProvenPropertyAccess/
      // resolveRecordObjectLiteral's absenceBindingUsesSafe gate): a same-file
      // binding must be proven never mutated — reassigned, aliased,
      // Object.assign'd, or member-written anywhere in its enclosing scope,
      // including AFTER this read site — before its declaration-time
      // initializer is trusted as the CURRENT value. Without this,
      // `const config = {...}; config.small = 'text-[10px]'` (or
      // `Object.assign(config, {...})`) silently resolved to the STALE
      // pre-mutation literal.
      const declaration = lexical?.declaration ?? this.topLevelConstDeclaration(expression.text)
      if (!declaration || !absenceBindingUsesSafe(declaration, false, true)) return []
      const resolved = unwrapStatic(initializer)
      if (!resolved) return []
      // Round 3: derived-value escape proof — never-mutated (absenceBindingUsesSafe)
      // proves nobody wrote through THIS binding's own name; it says nothing
      // about a nested (non-primitive) value read off it (`REC.a`) escaping
      // into a container/call/return/reassignment elsewhere in scope and
      // being mutated through THAT alias instead (cross-scanner-false-green
      // "array alias mutation of a nested object"). `containers` must be the
      // FULLY reduced object-literal leaf set `resolved` structurally holds
      // (classCarryingLeaves — the same general ternary/`??`/call/dynamic-
      // index-into-another-record reducer classCarryingLeaves/resolveProvenPropertyAccess
      // already use elsewhere in this file), NOT `resolved` itself: an
      // unresolved intermediate shape (a ConditionalExpression like
      // PolicyBadge's `inert ? {...} : {...}`, or a dynamic-key element
      // access like `POLICY_CONFIGS[policy]`) is not a container
      // derivedMemberTargets can traverse and was wrongly treated as an
      // unresolvable escape (found via the round-3 lane audit comparison:
      // PolicyBadge.tsx `cfg.activeColor`/`cfg.color`, TablePart.tsx
      // `inertProps.className` — real-tree false positives, not real debt).
      const structuralLeaves = this.classCarryingLeaves(resolved)
      const containers = structuralLeaves && structuralLeaves.length > 0 ? structuralLeaves : [resolved]
      // LEAD DECISION (false green found post-Stage-B: a record DEFINED and
      // EXPORTED in the reading file, `export const M = { a: GOOD }`, then
      // mutated by a DIFFERENT module that imports it — `import { M } from
      // './P'; M.a = BAD` — reached a className with zero findings, for both
      // `M.a` and `M[k]` reads). absenceBindingUsesSafe/derivedValueEscapeSafe
      // above only prove no write reaches `M` from THIS file's own
      // ts.SourceFile; a write from an IMPORTING module is invisible to both.
      // exportNeverMutatedByImporters closes exactly that gap (parity with
      // resolveRecordObjectLiteral's local branch and importedRecordAllValues,
      // and with ts-colors.mjs's knownClassExportUsesSafe): it is a no-op
      // (returns true immediately) for a non-exported declaration, so this
      // only ever ADDS a check for the exported case, never narrows the
      // non-exported one already proven above.
      if (!derivedValueEscapeSafe(declaration, containers) || !exportNeverMutatedByImporters(declaration, this.moduleContext, containers)) return []
      return this.localCandidates(resolved, seen)
    }
    if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
      const owners = this.localCandidates(expression.expression, new Set(seen))
      const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
        : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression)) ? expression.argumentExpression.text : null
      const results = []
      for (const owner of owners) {
        const object = unwrapStatic(owner)
        if (!ts.isObjectLiteralExpression(object)) continue
        // FIX-P2 (2026-09-20, confirmed-defect closure): a spread member
        // used to be silently skipped by both branches below. For the
        // key===null (enumerate-everything) branch that let an owner with a
        // spread PLUS other explicit properties contribute a non-empty,
        // apparently-complete `results` list while whatever the spread
        // carried never appeared anywhere — the caller
        // (walkClassExpression's PropertyAccessExpression case) treats
        // `candidates.length > 0` as fully resolved and returns, never
        // falling through to fail closed. Skipping the WHOLE owner here
        // (never partially enumerating it) forces that fallback instead,
        // matching classCarryingLeaves'/derivedMemberTargets' identical
        // spread-anywhere-fails-closed rule.
        if (object.properties.some((property) => ts.isSpreadAssignment(property))) continue
        if (key !== null) {
          const value = objectProperty(object, key)
          if (value) results.push(value)
        } else {
          for (const property of object.properties) {
            if (ts.isPropertyAssignment(property)) results.push(property.initializer)
            else if (ts.isShorthandPropertyAssignment(property)) results.push(property.name)
          }
        }
      }
      return results.flatMap((candidate) => {
        const nested = this.localCandidates(candidate, new Set(seen))
        return nested.length > 0 ? nested : [candidate]
      })
    }
    if (ts.isCallExpression(expression) && ts.isPropertyAccessExpression(expression.expression)
      && ts.isIdentifier(expression.expression.expression) && expression.expression.expression.text === 'Object'
      && ['freeze', 'seal', 'preventExtensions'].includes(expression.expression.name.text) && expression.arguments[0]) {
      return this.localCandidates(expression.arguments[0], seen)
    }
    return [expression]
  }

  collectStaticTextForBinding(node) {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = this.collectStaticTextForBinding(node.left); const right = this.collectStaticTextForBinding(node.right)
      return left !== null && right !== null ? left + right : null
    }
    return null
  }

  expandStaticClassStrings(node) {
    const expression = unwrapStatic(node)
    if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) return [expression.text]
    if (ts.isConditionalExpression(expression)) {
      const yes = this.expandStaticClassStrings(expression.whenTrue)
      const no = this.expandStaticClassStrings(expression.whenFalse)
      return yes && no ? [...yes, ...no] : null
    }
    if (ts.isBinaryExpression(expression) && expression.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = this.expandStaticClassStrings(expression.left)
      const right = this.expandStaticClassStrings(expression.right)
      if (!left || !right || left.length * right.length > 256) return null
      return left.flatMap((a) => right.map((b) => a + b))
    }
    return null
  }

  // Classifies whole class tokens from a string/template chunk, refining each
  // finding's position to the token's offset inside the literal.
  classifyInto(text, node, sink) {
    const lead = node.getStart(this.sourceFile) + 1
    let searchFrom = 0
    for (const token of text.split(/\s+/).filter(Boolean)) {
      const tokenIndex = text.indexOf(token, searchFrom)
      searchFrom = tokenIndex + token.length
      classifyClassToken(token, {
        emit: (finding) => sink.emit(finding, this.positionAt(lead + tokenIndex)),
        isRegistered: sink.isRegistered,
        registeredTokens: sink.registeredTokens,
      })
    }
  }
}

function createClassWalker(ctx) {
  return new ClassExpressionEngine(ctx)
}

// TypeScriptScanEngine holds scanTypeScript's own closures
// (report/positionAt/forwardedClassBoundary/... through visit) as methods,
// for the same reason as ClassExpressionEngine above: a class declaration
// is not a function-like node the size scanner counts, so the methods are
// measured individually rather than as one large function.
class TypeScriptScanEngine {
  constructor({ path, sourceFile, findings, seen, handledCalls, bindings, duplicateBindings, scannedParameterDefaults, registeredTokens, resolvedTokenValues, moduleContext }) {
    this.path = path
    this.sourceFile = sourceFile
    this.findings = findings
    this.seen = seen
    this.handledCalls = handledCalls
    this.bindings = bindings
    this.duplicateBindings = duplicateBindings
    this.scannedParameterDefaults = scannedParameterDefaults
    this.registeredTokens = registeredTokens
    this.resolvedTokenValues = resolvedTokenValues
    this.moduleContext = moduleContext
    // Set by scanTypeScript once createClassWalker/createStyleWalker have
    // been constructed with this engine's own bound report/positionAt/
    // forwardedClassBoundary/parameterMemberBoundary methods -- unset only
    // during that brief construction window, always set before visit()
    // (or markClassBuilderTree/isAuthenticCvaFactoryCall, both reachable
    // only from within visit()) is ever called.
    this.classWalker = null
    this.styleWalker = null
  }

  report(finding) {
    const key = `${finding.ruleId}|${finding.line ?? ''}|${finding.column ?? ''}|${finding.syntax}`
    if (this.seen.has(key)) return
    this.seen.add(key)
    this.findings.push({ path: this.path, ...finding })
  }

  positionAt(offset) {
    const position = this.sourceFile.getLineAndCharacterOfPosition(offset)
    return { line: position.line + 1, column: position.character + 1 }
  }

  forwardedClassBoundary(identifier) {
    let fn = identifier.parent
    while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
    if (!fn) return null
    // Provenance guard: a plain same-named declaration anywhere between the
    // sink and this function wins the binding, so the forwarded value is not
    // proven to be the caller-supplied className.
    if (plainDeclarationShadows(identifier, fn)) return null
    let initializer = null
    let declaration = null
    // The label always reports the SOURCE contract name (e.g. `className`
    // for a renamed `{ className: chevronClassName }`), never the local
    // alias — the alias is an implementation detail, the source prop name is
    // the stable receiving identity the contract requires.
    let boundaryName = identifier.text
    for (const parameter of fn.parameters) {
      if (ts.isIdentifier(parameter.name) && parameter.name.text === identifier.text && isClassLikeParameterName(identifier.text)) {
        initializer = parameter.initializer; declaration = parameter.name; break
      }
      if (ts.isObjectBindingPattern(parameter.name)) for (const element of parameter.name.elements) {
        if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
        const property = element.propertyName?.getText(this.sourceFile) ?? element.name.text
        if (isClassLikeParameterName(property)) { initializer = element.initializer; declaration = element.name; boundaryName = property; break }
      }
    }
    let sourceParameterName = null
    let sourceProperty = 'className'
    if (!declaration && fn.body) {
      // Body destructuring — `const { className } = props` and the renamed
      // `const { className: alias } = props` — is the second proven unchanged
      // forwarding shape. Provenance requires the destructured source to be a
      // bare identifier that is a parameter of this same function and the
      // binding to be `const`; helper/store outputs, member objects with
      // fallbacks (incl. `props ?? {}` — pinned unsupported by an existing
      // fixture: "keeps body-destructured forwarding with unproven
      // provenance unsupported"), `let` bindings and non-class-like
      // properties keep unsupported.
      // A name bound directly in the function's OWN parameter list — either
      // a plain parameter (`function f(props)`) or an element destructured
      // right there (`({ containerProps }, ref) => ...`, table.tsx's Table)
      // — carries the identical unchanged-caller-value guarantee either way;
      // only a REST element (`...rest`) is excluded, since it is a freshly
      // assembled object of whatever properties remain, not itself a single
      // named caller-supplied value.
      const parameterNames = new Set()
      // Tracked separately from the union `parameterNames` above (which only
      // decides ELIGIBILITY as a source): a source reached through a
      // destructured element of the function's own parameter list
      // (`containerProps`, table.tsx's Table) is a NAMED sub-object, not the
      // whole-props catch-all a plain identifier parameter (`props`) is — the
      // finding below prefixes the boundary name with it so a body-
      // destructured forward through a named sub-object never collides with
      // the SAME function's own direct className boundary (`Table#className`
      // vs `Table#containerProps.className`).
      const destructuredParameterNames = new Set()
      for (const parameter of fn.parameters) {
        if (ts.isIdentifier(parameter.name)) parameterNames.add(parameter.name.text)
        else if (ts.isObjectBindingPattern(parameter.name)) {
          for (const element of parameter.name.elements) {
            if (ts.isBindingElement(element) && !element.dotDotDotToken && ts.isIdentifier(element.name)) {
              parameterNames.add(element.name.text)
              destructuredParameterNames.add(element.name.text)
            }
          }
        }
      }
      let scope = identifier.parent
      while (scope && scope !== fn) {
        if (ts.isBlock(scope)) {
          const matches = []
          for (const statement of scope.statements) {
            if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
            for (const candidate of statement.declarationList.declarations) {
              if (!ts.isObjectBindingPattern(candidate.name)) continue
              const source = candidate.initializer
              if (!ts.isIdentifier(source) || !parameterNames.has(source.text)) continue
              for (const element of candidate.name.elements) {
                if (!ts.isBindingElement(element) || !ts.isIdentifier(element.name) || element.name.text !== identifier.text) continue
                const property = element.propertyName?.getText(this.sourceFile) ?? element.name.text
                // Narrower than the direct-parameter branches above (pinned
                // by an existing fixture): body destructuring only proves
                // literal `className`/`class`, never a class-like alias of a
                // different source property name.
                if (property === 'className' || property === 'class') matches.push({ initializer: element.initializer, declaration: element.name, source: source.text, property })
              }
            }
          }
          // Only the innermost unambiguous destructuring classifies.
          if (matches.length > 1) return null
          if (matches.length === 1) {
            initializer = matches[0].initializer; declaration = matches[0].declaration; sourceParameterName = matches[0].source; sourceProperty = matches[0].property
            // A source reached through a destructured element of the
            // function's OWN parameter list (containerProps) is prefixed so
            // it never collides with the same function's direct className
            // boundary; a plain identifier parameter (props) keeps the bare
            // property name, matching every existing fixture.
            boundaryName = destructuredParameterNames.has(matches[0].source) ? `${matches[0].source}.${matches[0].property}` : matches[0].property
            break
          }
        }
        scope = scope.parent
      }
    }
    if (!declaration) return null
    let reassigned = false
    function check(node) {
      if (ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === identifier.text
        && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
      // For body destructuring, mutating the source parameter or its
      // className member before the read breaks unchanged provenance.
      if (sourceParameterName) {
        if (ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === sourceParameterName
          && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
        if (ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) && node.left.name.text === sourceProperty
          && ts.isIdentifier(node.left.expression) && node.left.expression.text === sourceParameterName
          && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) reassigned = true
        if (ts.isDeleteExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === sourceProperty
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === sourceParameterName) reassigned = true
        if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'assign'
          && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object'
          && node.arguments.length > 0 && ts.isIdentifier(node.arguments[0]) && node.arguments[0].text === sourceParameterName) reassigned = true
      }
      node.forEachChild(check)
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    let ownerName = this.ownerNameFor(fn)
    let renderPropPath = null
    if (!ownerName) {
      // Render-prop shape: `components={{ Chevron: ({ className: alias }) =>
      // ... }}` (react-day-picker's Calendar#Chevron). `fn` here is the
      // property's function VALUE, so ownerNameFor's own climb (which only
      // resolves a named variable declaration or a forwardRef/memo call
      // wrapper) finds no name and returns null — it is not built to look
      // past a JSX-attribute object literal. anonymousRegistrationOwner is
      // no substitute either: its walk requires the object literal to
      // eventually sit inside a CallExpression argument (e.g. `cva('x', {
      // variants: {...} })`), never a JsxAttribute. renderPropOwner is
      // narrowly scoped to exactly this third shape: a function that is the
      // value of a named (non-computed) property in an object literal that
      // is itself the direct value of a JSX attribute. The owner is the
      // nearest NAMED enclosing component (matching parameterMemberBoundary's
      // nearestNamedAncestorOwner), and the render-prop's own property name
      // (e.g. `Chevron`) prefixes the boundary name so the finding names the
      // exact receiving identity, e.g. `Calendar#Chevron.className`.
      const renderProp = this.renderPropOwner(fn)
      if (renderProp) { ownerName = renderProp.owner; renderPropPath = renderProp.property }
    }
    if (!ownerName) return null
    return { symbol: ownerName, name: renderPropPath ? `${renderPropPath}.${boundaryName}` : boundaryName, initializer, declaration }
  }

  // Resolves the owner for `forwardedClassBoundary` when the forwarding
  // function is a render-prop value inside a JSX attribute's object literal
  // (`components={{ Chevron: (...) => ... }}`) rather than a named variable
  // or a forwardRef/memo-wrapped export. Deliberately narrow: every step of
  // the climb must match exactly, or it returns null and the caller falls
  // back to unsupported — this only ever ADDS a resolvable owner for a shape
  // ownerNameFor/anonymousRegistrationOwner cannot reach, never widens what
  // counts as an unchanged forward (that proof already happened above).
  renderPropOwner(fn) {
    const propertyAssignment = fn.parent
    if (!propertyAssignment || !ts.isPropertyAssignment(propertyAssignment) || propertyAssignment.initializer !== fn) return null
    if (ts.isComputedPropertyName(propertyAssignment.name)) return null
    const propertyName = ts.isIdentifier(propertyAssignment.name) || ts.isStringLiteral(propertyAssignment.name)
      ? propertyAssignment.name.text : null
    if (!propertyName) return null
    const objectLiteral = propertyAssignment.parent
    if (!objectLiteral || !ts.isObjectLiteralExpression(objectLiteral)) return null
    const jsxExpression = objectLiteral.parent
    if (!jsxExpression || !ts.isJsxExpression(jsxExpression)) return null
    const jsxAttribute = jsxExpression.parent
    if (!jsxAttribute || !ts.isJsxAttribute(jsxAttribute)) return null
    const ownerName = this.nearestNamedAncestorOwner(jsxAttribute)
    if (!ownerName) return null
    return { owner: ownerName, property: propertyName }
  }

  // Shared by forwardedClassBoundary and parameterMemberBoundary: resolves
  // the stable component/function name a boundary finding's `symbol` names,
  // unwrapping parenthesized/as/satisfies wrappers and forwardRef/memo.
  ownerNameFor(fn) {
    let parent = fn.parent
    while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    while (parent && ts.isCallExpression(parent) && this.isComponentWrapper(parent.expression)) {
      parent = parent.parent
      while (parent && (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent))) parent = parent.parent
    }
    return ts.isFunctionDeclaration(fn) && fn.name ? fn.name.text
      : parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) ? parent.name.text
        : this.anonymousRegistrationOwner(fn)
  }

  // Capability B (independent review): `X.className` / `X?.className` where
  // X is an unmutated, uniquely-bound, bare (non-destructured) parameter of
  // the nearest enclosing function — e.g. a `.map((item) => <X
  // className={item.className} />)` callback parameter, or a
  // `RetryableState({ iconProps }) { ... iconProps?.className ... }` prop
  // object read directly by member access rather than destructured. Distinct
  // from forwardedClassBoundary (which proves the *identifier itself* is an
  // unchanged forwarded className): here the identifier is a param and only
  // one MEMBER of it is read. Restricted to the literal `className`/`class`
  // key (not the broader class-like-name heuristic) since, unlike a
  // same-named parameter, an arbitrary property name carries no naming
  // signal that it is a CSS class at all.
  parameterMemberBoundary(node) {
    const key = ts.isPropertyAccessExpression(node) ? node.name.text
      : node.argumentExpression && (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression))
        ? node.argumentExpression.text : null
    if (key !== 'className' && key !== 'class') return null
    const base = unwrapStatic(node.expression)
    if (!ts.isIdentifier(base)) return null
    if (this.duplicateBindings.has(base.text) || hasMultipleVariableDeclarations(this.sourceFile, base.text)) return null
    let fn = node.parent
    while (fn && !ts.isFunctionLike(fn)) fn = fn.parent
    if (!fn) return null
    if (plainDeclarationShadows(base, fn)) return null
    // `base` may be a bare parameter (`function f(item) { ... item.className }`)
    // or itself a destructured element of an object-pattern parameter
    // (`RetryableState({ iconProps }) { ... iconProps?.className ... }`) —
    // either way it is the whole prop object being read, not further
    // destructured.
    const parameter = fn.parameters.find((candidate) => {
      if (ts.isIdentifier(candidate.name) && candidate.name.text === base.text) return true
      if (ts.isObjectBindingPattern(candidate.name)) {
        return candidate.name.elements.some((element) =>
          ts.isBindingElement(element) && !element.dotDotDotToken && ts.isIdentifier(element.name) && element.name.text === base.text)
      }
      return false
    })
    if (!parameter) return null
    let reassigned = false
    function check(current) {
      if (!reassigned) {
        if (ts.isBinaryExpression(current) && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
          if (ts.isIdentifier(current.left) && current.left.text === base.text) reassigned = true
          if (ts.isPropertyAccessExpression(current.left) && current.left.name.text === key
            && ts.isIdentifier(current.left.expression) && current.left.expression.text === base.text) reassigned = true
        }
        if (ts.isDeleteExpression(current) && ts.isPropertyAccessExpression(current.expression) && current.expression.name.text === key
          && ts.isIdentifier(current.expression.expression) && current.expression.expression.text === base.text) reassigned = true
        if (ts.isCallExpression(current) && ts.isPropertyAccessExpression(current.expression) && current.expression.name.text === 'assign'
          && ts.isIdentifier(current.expression.expression) && current.expression.expression.text === 'Object'
          && current.arguments.length > 0 && ts.isIdentifier(current.arguments[0]) && current.arguments[0].text === base.text) reassigned = true
        current.forEachChild(check)
      }
    }
    if (fn.body) check(fn.body)
    if (reassigned) return null
    // ownerNameFor alone (shared with forwardedClassBoundary, left untouched
    // there) does not resolve an anonymous callback passed directly as a
    // bare call argument — `items.map((item) => ...)`, not registered under
    // a named object property — since anonymousRegistrationOwner requires at
    // least one named property in the chain. That shape is common for this
    // capability (a rendered list item), so fall back to the nearest NAMED
    // enclosing function/component as the stable receiving identity.
    const ownerName = this.ownerNameFor(fn) ?? this.nearestNamedAncestorOwner(fn)
    if (!ownerName) return null
    return { symbol: ownerName, name: `${base.text}.${key}` }
  }

  nearestNamedAncestorOwner(fn) {
    let current = fn.parent
    while (current) {
      if (ts.isFunctionDeclaration(current) && current.name) return current.name.text
      if (ts.isArrowFunction(current) || ts.isFunctionExpression(current)) {
        const name = this.ownerNameFor(current)
        if (name) return name
      }
      current = current.parent
    }
    return null
  }

  isComponentWrapper(expression) {
    if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
    return ts.isPropertyAccessExpression(expression) && (expression.name.text === 'forwardRef' || expression.name.text === 'memo')
  }

  anonymousRegistrationOwner(fn) {
    const properties = []
    let current = fn
    while (current.parent) {
      const parent = current.parent
      if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent)) {
        current = parent
        continue
      }
      if (ts.isPropertyAssignment(parent) && parent.initializer === current) {
        if (ts.isComputedPropertyName(parent.name)) return null
        const name = ts.isIdentifier(parent.name) || ts.isStringLiteral(parent.name) || ts.isNumericLiteral(parent.name) ? parent.name.text : null
        if (!name) return null
        properties.unshift(name)
        current = parent.parent
        continue
      }
      if (ts.isObjectLiteralExpression(parent)) {
        current = parent
        continue
      }
      if (ts.isSpreadAssignment(parent)) return null
      if ((ts.isArrowFunction(parent) || ts.isFunctionExpression(parent)) && parent.body === current) {
        current = parent
        continue
      }
      if (ts.isCallExpression(parent) && parent.arguments.includes(current) && properties.length > 0) {
        const key = parent.arguments[0]
        if (!key || (!ts.isStringLiteral(key) && !ts.isNoSubstitutionTemplateLiteral(key))) return null
        const callee = parent.expression.getText(this.sourceFile).replace(/\s+/g, '')
        if (!/^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$/.test(callee)) return null
        return `${callee}(${key.text}):${properties.join('.')}`
      }
      return null
    }
    return null
  }

  visit(node) {
    if (ts.isJsxAttribute(node)) {
      const attributeName = node.name.text
      if (attributeName === 'className' || attributeName === 'class') {
        const initializer = node.initializer
        if (initializer && ts.isJsxExpression(initializer) && initializer.expression) {
          const reportClass = this.classWalker.classReporterFor(initializer)
          if (ts.isCallExpression(initializer.expression)) this.markClassBuilderTree(initializer.expression)
          this.classWalker.walkClassExpression(initializer.expression, reportClass)
        } else if (initializer && ts.isStringLiteral(initializer)) {
          this.classWalker.classifyClassString(initializer.text, initializer)
        }
      }
      if (attributeName === 'fontSize' || attributeName === 'font-size') this.styleWalker.walkJsxValue(node, 'fontSize')
      if (attributeName === 'fontFamily' || attributeName === 'font-family') this.styleWalker.walkJsxValue(node, 'fontFamily')
      if (attributeName === 'style' && this.path.endsWith('.svg') && node.initializer && ts.isStringLiteral(node.initializer)) {
        const nested = scanCss({ path: this.path, source: `.svg-inline { ${node.initializer.text} }`, registeredTokens: this.registeredTokens })
        nested.forEach((finding) => this.report({ ...finding, line: this.positionAt(node.getStart(this.sourceFile)).line, column: this.positionAt(node.getStart(this.sourceFile)).column }))
      }
    }
    if (this.path.endsWith('.svg') && ts.isJsxElement(node) && node.openingElement.tagName.getText(this.sourceFile) === 'style') {
      const css = node.children.filter(ts.isJsxText).map((child) => child.text).join('')
      if (css.trim()) scanCss({ path: this.path, source: css, registeredTokens: this.registeredTokens }).forEach((finding) => this.report(finding))
    }
    if (ts.isCallExpression(node)) {
      const callee = node.expression
      if (this.isAuthenticCvaFactoryCall(node)) {
        this.handledCalls.add(node)
        const reportClass = this.classWalker.classReporterFor(node)
        node.arguments.forEach((argument) => this.classWalker.walkClassExpression(argument, reportClass))
      } else if (ts.isIdentifier(callee) && CLASS_BUILDERS.has(callee.text) && !this.handledCalls.has(node)
        && authenticClassBuilderCallee(callee, { sourceFile: this.sourceFile, bindings: this.bindings, duplicateBindings: this.duplicateBindings })) {
        this.markClassBuilderTree(node)
        const reportClass = this.classWalker.classReporterFor(node)
        node.arguments.forEach((argument) => this.classWalker.walkClassExpression(argument, reportClass))
      }
    }
    if (ts.isPropertyAssignment(node)) {
      this.styleWalker.walkStyleProperty(node)
    }
    node.forEachChild(this.visit.bind(this))
  }

  markClassBuilderTree(node) {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_BUILDERS.has(node.expression.text)) this.handledCalls.add(node)
    node.forEachChild(this.markClassBuilderTree.bind(this))
  }

  isAuthenticCvaFactoryCall(node) {
    return authenticCvaImport(node.expression)
  }
}


export function scanTypeScript({ path, source, registeredTokens, resolvedTokenValues, modules, moduleCache }) {
  const sourceFile = ts.createSourceFile(path, source, ts.ScriptTarget.ES2022, true, scriptKindFor(path))
  const findings = []
  const seen = new Set()
  // className initializers are walked with their own positions; the builder
  // pass must not walk the same call again and duplicate findings.
  const handledCalls = new WeakSet()


  const parseDiagnostics = sourceFile.parseDiagnostics
  if (Array.isArray(parseDiagnostics) && parseDiagnostics.length > 0) {
    findings.push({
      ruleId: 'typography/parse-failure',
      path,
      syntax: `${parseDiagnostics.length} TypeScript parse diagnostics`,
      message: `TypeScript could not parse ${path} cleanly (${parseDiagnostics.length} diagnostic(s); first: ${ts.flattenDiagnosticMessageText(parseDiagnostics[0].messageText, ' ')}). The lock fails closed on unparseable governed files.`,
    })
    return findings
  }

  const bindings = new Map()
  const duplicateBindings = new Set()
  const scannedParameterDefaults = new WeakSet()
  sourceFile.forEachChild((node) => {
    if (!ts.isVariableStatement(node) || !(node.declarationList.flags & ts.NodeFlags.Const)) return
    for (const declaration of node.declarationList.declarations) if (ts.isIdentifier(declaration.name) && declaration.initializer) {
      if (bindings.has(declaration.name.text)) duplicateBindings.add(declaration.name.text)
      else bindings.set(declaration.name.text, declaration.initializer)
    }
  })

  const moduleContext = { modules, moduleCache }
  const engine = new TypeScriptScanEngine({ path, sourceFile, findings, seen, handledCalls, bindings, duplicateBindings, scannedParameterDefaults, registeredTokens, resolvedTokenValues, moduleContext })
  engine.classWalker = createClassWalker({ report: engine.report.bind(engine), positionAt: engine.positionAt.bind(engine), sourceFile, registeredTokens, bindings, duplicateBindings, forwardedClassBoundary: engine.forwardedClassBoundary.bind(engine), parameterMemberBoundary: engine.parameterMemberBoundary.bind(engine), scannedParameterDefaults, moduleContext, handledCalls })
  engine.styleWalker = createStyleWalker({ report: engine.report.bind(engine), positionAt: engine.positionAt.bind(engine), sourceFile, registeredTokens, resolvedTokenValues, moduleContext })
  engine.visit(sourceFile)
  return engine.findings
}
