#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket C pattern P8
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json,
// lane L7b fanout brief).
//
// Pattern P8: a finite-branch IIFE, switch or ternary returning object
// literals where every branch sets a real literal, which spacing.mjs /
// typography.mjs / ts-colors.mjs cannot all prove from the ORIGINAL shape —
// each scanner's finite-dispatcher proof has a different, narrower AST shape
// it can follow (documented per-site below). This codemod closes the 12 of
// 13 P8 findings that have an INVISIBLE fix (identical rendered DOM, class
// list and behaviour) reachable by reshaping the SOURCE, never the scanners.
// One finding — TablePart.tsx's `inertProps.className` at the ts-colors
// layer — is closed as a side effect of the same site's spacing rewrite;
// see planTablePartInertProps's note.
//
// Design (COMMON-RULES, dist/design-system-baseline/cli-lanes/fanout/
// COMMON-RULES.md): "the smallest INVISIBLE rewrite all three [scanners]
// can prove" per site — never a scanner change. This is FIVE bespoke site
// recipes, not one generic pattern-matcher: P8's five sites (BrowserLiveView
// IIFE, GoalIndicator/GoalPillTray destructuring escapes, FileWriteConfirm's
// mixed-value ternary, TablePart's template-literal member read) each need a
// structurally different rewrite, so — like the reference implementation
// (codemod-dead-textclass-read.mjs, which is pinned to two named functions
// in one shared module) — each recipe RE-VERIFIES its exact expected AST
// shape from the CURRENT source on every run and refuses (never
// force-applies) the instant that shape has changed. A recipe that finds its
// OWN already-rewritten output instead reports a clean no-op (idempotent);
// anything else refuses with a reason.
import { resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

// ── Shared AST helpers ──────────────────────────────────────────────────────

function findFirst(source, predicate) {
  let found = null
  const visit = (node) => {
    if (found) return
    if (predicate(node)) { found = node; return }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return found
}

function findAll(source, predicate) {
  const out = []
  const visit = (node) => {
    if (predicate(node)) out.push(node)
    ts.forEachChild(node, visit)
  }
  visit(source)
  return out
}

/** A simple (no rename/default/rest/nested) object-destructuring pattern. */
function isSimpleObjectBindingPattern(pattern) {
  return ts.isObjectBindingPattern(pattern) && pattern.elements.every((el) =>
    !el.dotDotDotToken && !el.initializer && !el.propertyName && ts.isIdentifier(el.name))
}

/**
 * Every bare reference to `name` within `scope`, EXCLUDING `declarationNode`
 * itself. Returns `null` (ambiguous — caller must refuse) the instant it
 * finds: a nested re-declaration of `name` (parameter, variable, catch
 * binding, import) anywhere in `scope`, OR a reference used as an object-
 * literal shorthand property (`{ name }` — `{ temp.name }` is not valid
 * shorthand syntax) or as a destructuring target. A JSX tag-name reference
 * (`<name .../>` / `</name>`) is reported separately (`jsxTagRefs`) since it
 * rewrites differently (`<temp.name>`, still one legal JSX member-expression
 * tag) from an ordinary expression reference (`plainRefs`, rewrites to
 * `temp.name`).
 *
 * `declarationNode` is the ORIGINAL destructuring `VariableDeclaration` (its
 * own `name` is an ObjectBindingPattern that structurally "declares" every
 * name being searched for — it must never itself count as a shadow).
 * `bindingIdentifierNodes` is the set of the destructuring pattern's own
 * per-element `Identifier` name nodes (e.g. the `testId` in `{ testId }`) —
 * these are the declaration sites being replaced, not reads, so the "plain
 * identifier" pass below must skip them too.
 */
function collectSafeReferences(scope, name, declarationNode, bindingIdentifierNodes) {
  const bindingIdentifiers = new Set(bindingIdentifierNodes)
  const plainRefs = []
  const jsxTagRefs = []
  let ambiguous = false
  const visit = (node) => {
    if (ambiguous) return
    if (
      (ts.isParameter(node) && bindingHasName(node.name, name)) ||
      (ts.isVariableDeclaration(node) && node !== declarationNode && bindingHasName(node.name, name)) ||
      (ts.isCatchClause(node) && node.variableDeclaration && bindingHasName(node.variableDeclaration.name, name)) ||
      (ts.isImportSpecifier(node) && node.name.text === name)
    ) { ambiguous = true; return }
    if (ts.isBindingElement(node) && ts.isIdentifier(node.name) && node.name.text === name && !bindingIdentifiers.has(node.name)) {
      ambiguous = true; return
    }
    if (ts.isIdentifier(node) && node.text === name && !bindingIdentifiers.has(node)) {
      const parent = node.parent
      // A "name" role — a JSX attribute name, an object/interface member
      // key, a label — is a different identifier that merely happens to
      // share this one's spelling (`<p className="...">`'s `className`
      // attribute NAME is not a reference to our destructured `className`
      // variable). These are not references at all; skip without flagging
      // ambiguous and keep descending in case the identifier's own
      // children (e.g. a computed name) need visiting.
      if (
        (ts.isJsxAttribute(parent) && parent.name === node) ||
        ((ts.isPropertyAssignment(parent) || ts.isPropertySignature(parent) || ts.isMethodDeclaration(parent) || ts.isMethodSignature(parent)) && parent.name === node) ||
        (ts.isLabeledStatement(parent) && parent.label === node)
      ) {
        ts.forEachChild(node, visit)
        return
      }
      if (ts.isShorthandPropertyAssignment(parent) && parent.name === node) { ambiguous = true; return }
      if ((ts.isJsxOpeningElement(parent) || ts.isJsxClosingElement(parent) || ts.isJsxSelfClosingElement(parent)) && parent.tagName === node) {
        jsxTagRefs.push(node)
      } else {
        plainRefs.push(node)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  if (ambiguous) return null
  return { plainRefs, jsxTagRefs }
}

function bindingHasName(name, text) {
  if (ts.isIdentifier(name)) return name.text === text
  return (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name))
    && name.elements.some((el) => ts.isBindingElement(el) && bindingHasName(el.name, text))
}

/** Rewrites a proven-safe destructuring declaration (`const { a, b } = X`)
 * into a single-binding declaration (`const <newBinderName> = X`) plus a
 * `.prop`-access edit for every reference this file already verified is
 * safe. `declEnd`/`declStart` bound the whole `VariableStatement` being
 * replaced (including trailing semicolon/newline handling is left to the
 * caller so this stays reusable for both the "reuse an existing identifier"
 * and "introduce a new one" shapes). */
function destructureToPropertyAccessEdits({ declarationStatementStart, declarationStatementEnd, replacementStatementText, refsByName, newBinderName }) {
  const edits = [{ start: declarationStatementStart, end: declarationStatementEnd, replacement: replacementStatementText }]
  for (const [name, refs] of Object.entries(refsByName)) {
    for (const node of refs.plainRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: `${newBinderName}.${name}` })
    for (const node of refs.jsxTagRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: `${newBinderName}.${name}` })
  }
  return edits
}

function refuse(reason) { return { ok: false, reason } }
function ok(edits, notes) { return { ok: true, edits, notes: notes ?? [] } }
function noop(notes) { return { ok: true, edits: [], alreadyApplied: true, notes: notes ?? [] } }

// ── Site 1 — GoalIndicator.tsx ──────────────────────────────────────────────
// `{goalStatus.state !== 'active' && (() => { const { testId, text,
// className } = describeNonActiveState(goalStatus.state); return (<>...)
// })()}` — `describeNonActiveState` is a top-level, exhaustive-switch
// dispatcher (already scanner-provable in isolation: see
// spacing.mjs::resolveDispatcherMember / typography.mjs::finiteCallReturns),
// but the render callback reads its result through DESTRUCTURING
// (`{ className }`), never a property access — none of the three scanners'
// finite-dispatcher proofs follow a destructuring pattern's element back to
// the call, only `<ident>.prop`. Bridging that gap needs no data-flow
// change: replace the destructuring with a single named binding and turn
// every one of the three destructured names' uses into `.prop` reads.
export function planGoalIndicatorNonActiveState(source) {
  const CALLEE = 'describeNonActiveState'
  const PROP_NAMES = ['testId', 'text', 'className']
  const decl = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) &&
    isSimpleObjectBindingPattern(node.name) &&
    node.initializer && ts.isCallExpression(node.initializer) &&
    ts.isIdentifier(node.initializer.expression) && node.initializer.expression.text === CALLEE)

  if (decl) {
    const names = decl.name.elements.map((el) => el.name.text)
    if (names.length !== PROP_NAMES.length || !PROP_NAMES.every((n) => names.includes(n))) {
      return refuse(`found a destructuring of ${CALLEE}(...) but its bound names (${names.join(', ')}) no longer match the expected (${PROP_NAMES.join(', ')}) — refusing rather than guess`)
    }
    const statement = decl.parent.parent // VariableDeclarationList -> VariableStatement
    if (!ts.isVariableStatement(statement)) return refuse('destructuring declaration is not a direct VariableStatement — unexpected shape')
    // Scope this to the immediately enclosing arrow function body (the IIFE
    // render callback), not the whole component — a same-named identifier
    // anywhere else in GoalIndicator is a different binding.
    let scope = decl.parent.parent.parent
    while (scope && !(ts.isArrowFunction(scope) || ts.isFunctionExpression(scope) || ts.isFunctionDeclaration(scope) || ts.isMethodDeclaration(scope)) ) scope = scope.parent
    if (!scope || !scope.body || !ts.isBlock(scope.body)) return refuse('could not find the enclosing function body to scope reference-safety checks')

    const refsByName = {}
    for (const el of decl.name.elements) {
      const propName = el.name.text
      const found = collectSafeReferences(scope.body, propName, decl, [el.name])
      if (!found) return refuse(`a reference to destructured name \`${propName}\` is ambiguous (shadowed, or used as object-literal shorthand) — refusing`)
      refsByName[propName] = found
    }

    const newBinderName = 'nonActiveState'
    // Refuse rather than collide if that name is already bound in scope.
    const collision = findFirst(scope.body, (node) =>
      (ts.isVariableDeclaration(node) && bindingHasName(node.name, newBinderName) && node !== decl) ||
      (ts.isParameter(node) && bindingHasName(node.name, newBinderName)))
    if (collision) return refuse(`synthesized binder name \`${newBinderName}\` already used in this scope — refusing rather than collide`)

    const initText = decl.initializer.getText()
    const replacementStatementText = `const ${newBinderName} = ${initText}`
    const edits = destructureToPropertyAccessEdits({
      declarationStatementStart: statement.getStart(),
      declarationStatementEnd: statement.getEnd(),
      replacementStatementText,
      refsByName,
      newBinderName,
    })
    return ok(edits, [`${CALLEE}(...) destructuring replaced with \`${newBinderName}\` + property access (spacing/typography/ts-colors finite-dispatcher proofs all require \`<ident>.prop\`, never a destructuring element)`])
  }

  // Idempotency: our own prior output already present?
  const already = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === 'nonActiveState' &&
    node.initializer && ts.isCallExpression(node.initializer) && ts.isIdentifier(node.initializer.expression) && node.initializer.expression.text === CALLEE)
  if (already) return noop([`already rewritten (const nonActiveState = ${CALLEE}(...) present)`])

  return refuse(`could not find \`const { ${PROP_NAMES.join(', ')} } = ${CALLEE}(...)\` — site shape has changed`)
}

// ── Site 2 — GoalPillTray.tsx ───────────────────────────────────────────────
// Two independent sub-fixes on the same `const config = describePillState(
// frame.state)` binding:
//   (a) `const { Icon } = config` destructures `Icon` out, which — same gap
//       as GoalIndicator above — poisons the WHOLE `config` binding for
//       spacing.mjs's dispatcherBindingUsesSafe / ts-colors.mjs's
//       knownClassDerivedUsesSafe (both require every use of `config`
//       anywhere in scope to be a plain `.prop` read; a destructuring
//       pattern is not one). Fix: drop the destructuring, use
//       `<config.Icon .../>` directly — JSX supports a member-expression
//       tag name.
//   (b) `config.pulse && 'animate-pulse'` — `pulse?: boolean` is typed
//       boolean in the same-file `PillStateConfig` interface, but
//       ts-colors.mjs's `isDefinitelyBoolean` only recognizes a boolean-typed
//       FUNCTION PARAMETER, never a local `const` bound to a call's return
//       value, so it walks `config.pulse` itself looking for class content
//       and fails to resolve it. `config.pulse === true && '...'` is
//       byte-identical in behaviour for every value `pulse?: boolean` can
//       hold (`true`, `false`, `undefined`) and IS one of
//       isDefinitelyBoolean's recognized shapes (an `===` comparison).
export function planGoalPillTrayIconAndPulse(source, text) {
  const notes = []
  const edits = []

  // (a) Icon destructuring.
  const iconDecl = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && isSimpleObjectBindingPattern(node.name) &&
    node.name.elements.length === 1 && node.name.elements[0].name.text === 'Icon' &&
    node.initializer && ts.isIdentifier(node.initializer) && node.initializer.text === 'config')
  let iconAlreadyDone = false
  if (iconDecl) {
    const statement = iconDecl.parent.parent
    if (!ts.isVariableStatement(statement)) return refuse('Icon destructuring declaration is not a direct VariableStatement — unexpected shape')
    let scope = iconDecl.parent.parent.parent
    while (scope && !(ts.isArrowFunction(scope) || ts.isFunctionExpression(scope) || ts.isFunctionDeclaration(scope) || ts.isMethodDeclaration(scope))) scope = scope.parent
    if (!scope || !scope.body || !ts.isBlock(scope.body)) return refuse('could not find the enclosing function body for the Icon destructuring')
    const refs = collectSafeReferences(scope.body, 'Icon', iconDecl, [iconDecl.name.elements[0].name])
    if (!refs) return refuse('a reference to destructured `Icon` is ambiguous (shadowed, or object-literal shorthand) — refusing')
    if (refs.plainRefs.length > 0) return refuse(`\`Icon\` is used as a plain (non-JSX-tag) expression at ${refs.plainRefs.length} site(s) — this recipe only rewrites the JSX-tag-name usage it was built for; refusing rather than guess at the others`)
    if (refs.jsxTagRefs.length === 0) return refuse('found the Icon destructuring but no `<Icon .../>` JSX-tag usage to rewrite — refusing')
    // Removes the statement AND its trailing newline/indentation so we
    // don't leave a blank line behind.
    edits.push(...statementRemovalEdit(statement, text))
    for (const node of refs.jsxTagRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: 'config.Icon' })
    notes.push('`const { Icon } = config` removed; `<Icon>` -> `<config.Icon>` (destructuring off `config` poisoned the whole binding for spacing.mjs/ts-colors.mjs\'s dispatcher-safety proofs)')
  } else {
    const already = !findFirst(source, (node) => ts.isJsxOpeningElement(node) && ts.isIdentifier(node.tagName) && node.tagName.text === 'Icon')
      && findFirst(source, (node) => (ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) && ts.isPropertyAccessExpression(node.tagName) && ts.isIdentifier(node.tagName.expression) && node.tagName.expression.text === 'config' && node.tagName.name.text === 'Icon')
    if (already) iconAlreadyDone = true
    else return refuse('could not find `const { Icon } = config` and no `<config.Icon>` already present — site shape has changed')
  }

  // (b) pulse boolean guard.
  const pulseAnd = findFirst(source, (node) =>
    ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken &&
    ts.isPropertyAccessExpression(node.left) && ts.isIdentifier(node.left.expression) &&
    node.left.expression.text === 'config' && node.left.name.text === 'pulse')
  let pulseAlreadyDone = false
  if (pulseAnd) {
    edits.push({ start: pulseAnd.left.getStart(), end: pulseAnd.left.getEnd(), replacement: `${pulseAnd.left.getText()} === true` })
    notes.push('`config.pulse && X` -> `config.pulse === true && X` (ts-colors.mjs::isDefinitelyBoolean only recognizes an explicit boolean comparison off a non-parameter receiver, not a bare typed-boolean property read)')
  } else {
    const already = findFirst(source, (node) =>
      ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsEqualsEqualsToken &&
      ts.isPropertyAccessExpression(node.left) && ts.isIdentifier(node.left.expression) && node.left.expression.text === 'config' && node.left.name.text === 'pulse' &&
      node.right.kind === ts.SyntaxKind.TrueKeyword)
    if (already) pulseAlreadyDone = true
    else return refuse('could not find `config.pulse && ...` and no `config.pulse === true` already present — site shape has changed')
  }

  if (iconAlreadyDone && pulseAlreadyDone) return noop(['both Icon-destructuring and pulse-guard fixes already present'])
  return ok(edits, notes)
}

/** Removes a whole statement plus its own leading indentation and trailing
 * newline, so deleting it doesn't leave a blank line behind. */
function statementRemovalEdit(statement, text) {
  let start = statement.getStart()
  let lineStart = start
  while (lineStart > 0 && text[lineStart - 1] !== '\n') lineStart -= 1
  if (text.slice(lineStart, start).trim() === '') start = lineStart
  let end = statement.getEnd()
  if (text[end] === '\n') end += 1
  return [{ start, end, replacement: '' }]
}

// ── Site 3 — FileWriteConfirm.tsx ───────────────────────────────────────────
// `const statusConfig = isRefusal && !isRunning ? {..., textClass: '...'} :
// getToolBadgeStatusConfig(...)` — `getToolBadgeStatusConfig` NEVER assigns
// `textClass` in any branch (P1's own proof, codemod-dead-textclass-read.mjs)
// but the OTHER ternary branch here DOES, so `.textClass` is not globally
// dead (P1 cannot close it) and IS a real, sometimes-present value (P8's
// job). Proving that mix needs every `getToolBadgeStatusConfig` branch to
// carry `__proto__: null` (spacing.mjs's absence-with-null-prototype rule) —
// which is a shared module four OTHER components also call, not this file's
// to touch. Splitting `textClass` out of the ternary into its own
// literal-vs-undefined value sidesteps the shared module entirely: the
// value is ALWAYS either the one warning literal or `undefined`, provable
// directly with no member-resolution machinery at all.
export function planFileWriteConfirmTextClass(source, text) {
  const CONDITION_TEXT = 'isRefusal && !isRunning'
  const TEXT_CLASS_LITERAL = "'text-[var(--color-warning)]'"
  const EXPECTED_WHEN_TRUE_KEYS = new Set(['indicator', 'label', 'textClass'])
  const decl = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === 'statusConfig' &&
    node.initializer && ts.isConditionalExpression(node.initializer) &&
    node.initializer.condition.getText() === CONDITION_TEXT &&
    ts.isObjectLiteralExpression(node.initializer.whenTrue) &&
    node.initializer.whenTrue.properties.every((p) => p.name && ts.isIdentifier(p.name) && EXPECTED_WHEN_TRUE_KEYS.has(p.name.text) && ts.isPropertyAssignment(p)) &&
    node.initializer.whenTrue.properties.length === EXPECTED_WHEN_TRUE_KEYS.size &&
    node.initializer.whenTrue.properties.some((p) => ts.isIdentifier(p.name) && p.name.text === 'textClass' && p.initializer.getText() === TEXT_CLASS_LITERAL))

  if (decl) {
    const statement = decl.parent.parent
    if (!ts.isVariableStatement(statement)) return refuse('statusConfig declaration is not a direct VariableStatement')
    const textClassRefs = findAll(source, (node) =>
      ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'statusConfig' && node.name.text === 'textClass')
    if (textClassRefs.length === 0) return refuse('found the statusConfig ternary but no `statusConfig.textClass` reads to rewrite')

    const whenTrue = decl.initializer.whenTrue
    const textClassProp = whenTrue.properties.find((p) => p.name && ts.isIdentifier(p.name) && p.name.text === 'textClass')
    const otherProps = whenTrue.properties.filter((p) => p !== textClassProp)
    const newWhenTrueText = `{\n        ${otherProps.map((p) => p.getText()).join(',\n        ')},\n      }`
    const whenFalseText = decl.initializer.whenFalse.getText()

    const edits = []
    edits.push(...statementRemovalEdit(statement, text).map((e) => ({ ...e, replacement: '' })))
    const indent = leadingIndent(statement, text)
    const replacement =
      `${indent}const isRefusalDisplay = ${CONDITION_TEXT}\n` +
      `${indent}const statusConfig = isRefusalDisplay\n` +
      `${indent}  ? ${newWhenTrueText}\n` +
      `${indent}  : ${whenFalseText}\n` +
      `${indent}// Split out of \`statusConfig\` so the scanner-provable value is a plain\n` +
      `${indent}// literal-vs-undefined ternary: \`getToolBadgeStatusConfig\` never assigns\n` +
      `${indent}// \`textClass\` in any branch (see codemod-dead-textclass-read.mjs's P1\n` +
      `${indent}// proof of that same fact), so folding it into \`statusConfig.textClass\`\n` +
      `${indent}// made the design-system scanners unable to prove the read — they cannot\n` +
      `${indent}// see across the shared factory's switch without also being able to\n` +
      `${indent}// prove EVERY one of its returns carries a null prototype, which this\n` +
      `${indent}// file has no business asserting about a function four other components\n` +
      `${indent}// also call. Computing it locally sidesteps that: \`textClass\` is either\n` +
      `${indent}// the literal warning class or \`undefined\`, never anything else.\n` +
      `${indent}const textClass = isRefusalDisplay ? ${TEXT_CLASS_LITERAL} : undefined\n`
    edits[0].replacement = replacement
    for (const node of textClassRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: 'textClass' })
    return ok(edits, ['`statusConfig.textClass` split into an independent `textClass` literal-vs-undefined const; `statusConfig` no longer carries `textClass` at all'])
  }

  const already = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === 'textClass' &&
    node.initializer && ts.isConditionalExpression(node.initializer) && node.initializer.whenTrue.getText() === TEXT_CLASS_LITERAL)
  if (already) return noop(['already rewritten (independent `const textClass = ... ? ' + TEXT_CLASS_LITERAL + ' : undefined` present)'])
  return refuse('could not find the expected `statusConfig` ternary shape — site has changed')
}

function leadingIndent(node, text) {
  let start = node.getStart()
  let lineStart = start
  while (lineStart > 0 && text[lineStart - 1] !== '\n') lineStart -= 1
  return text.slice(lineStart, start)
}

// ── Site 4 — TablePart.tsx ──────────────────────────────────────────────────
// `const inertProps = inert ? { className: 'cursor-default', onClick: ... }
// : { className: '' }`, read back as `inertProps.className` INSIDE A RAW
// TEMPLATE LITERAL (`` `...${inertProps.className}` ``, not a cn()/clsx()
// call argument). spacing.mjs already proves this fine either way (its
// dispatcher-member proof covers a plain ternary of object literals
// directly); ts-colors.mjs and typography.mjs's TEMPLATE-LITERAL walkers
// use a narrower single-target resolver for a member read
// (ts-colors.mjs::resolveMember/resolveToObject, which does not follow a
// ConditionalExpression at all) than their cn()-argument walkers use — so
// the exact same value is unprovable in a template literal but provable as
// a cn() argument. Pulling `className`/`onClick` out of the object-literal
// ternary into their OWN plain ternaries (still `inert ? <literal> :
// <literal>`, just not wrapped in an object) removes the member-access step
// entirely, which both scanners' basic identifier-bound-to-ternary proof
// already covers regardless of context.
export function planTablePartInertProps(source, text) {
  const decl = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === 'inertProps' &&
    node.initializer && ts.isConditionalExpression(node.initializer) &&
    ts.isObjectLiteralExpression(node.initializer.whenTrue) &&
    ts.isObjectLiteralExpression(node.initializer.whenFalse))

  if (decl) {
    const cond = decl.initializer.condition
    const whenTrue = decl.initializer.whenTrue
    const whenFalse = decl.initializer.whenFalse
    const classNameTrue = whenTrue.properties.find((p) => p.name && ts.isIdentifier(p.name) && p.name.text === 'className')
    const classNameFalse = whenFalse.properties.find((p) => p.name && ts.isIdentifier(p.name) && p.name.text === 'className')
    const onClickTrue = whenTrue.properties.find((p) => p.name && ts.isIdentifier(p.name) && p.name.text === 'onClick')
    if (!classNameTrue || !classNameFalse || !ts.isPropertyAssignment(classNameTrue) || !ts.isPropertyAssignment(classNameFalse)) {
      return refuse('inertProps ternary branches do not both declare a plain `className` property — refusing')
    }
    if (whenFalse.properties.length !== 1) return refuse('the `false` branch of the inertProps ternary has more than just `className` — refusing rather than guess how to split it')

    const classNameRefs = findAll(source, (node) =>
      ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'inertProps' && node.name.text === 'className')
    const onClickRefs = findAll(source, (node) =>
      ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'inertProps' && node.name.text === 'onClick')
    if (classNameRefs.length === 0) return refuse('found the inertProps ternary but no `inertProps.className` reads')

    const statement = decl.parent.parent
    if (!ts.isVariableStatement(statement)) return refuse('inertProps declaration is not a direct VariableStatement')
    const indent = leadingIndent(statement, text)
    const condText = cond.getText()
    let replacement = `${indent}const inertClassName = ${condText} ? ${classNameTrue.initializer.getText()} : ${classNameFalse.initializer.getText()}\n`
    if (onClickTrue) {
      replacement += `${indent}const inertOnClick = ${condText} ? ${onClickTrue.initializer.getText()} : undefined\n`
    }
    const edits = statementRemovalEdit(statement, text)
    edits[0].replacement = replacement
    for (const node of classNameRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: 'inertClassName' })
    for (const node of onClickRefs) edits.push({ start: node.getStart(), end: node.getEnd(), replacement: 'inertOnClick' })
    return ok(edits, [
      '`inertProps` ternary-of-objects split into independent `inertClassName`/`inertOnClick` ternaries-of-literals — removes the member-access-off-a-ternary step neither ts-colors.mjs nor typography.mjs\'s template-literal walker follows (closes both scanners\' unsupported findings at this site; spacing.mjs was already clean before and after).',
    ])
  }

  const already = findFirst(source, (node) =>
    ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === 'inertClassName')
  if (already) return noop(['already rewritten (`inertClassName`/`inertOnClick` present)'])
  return refuse('could not find the expected `inertProps` ternary-of-objects shape — site has changed')
}

// ── Site 5 — BrowserLiveView.tsx ────────────────────────────────────────────
// `const driveChip = (() => { if (visualState === 'agent-working') return
// {...}; ... if (visualDriveMode === 'disconnected') return {...}; ...
// })()` — an if-chain IIFE. typography.mjs's finiteCallReturns already
// follows an IIFE with if-chain guards directly (no rewrite needed there),
// but spacing.mjs's dispatcher (resolveDispatcherMember /
// collectFiniteDispatcherReturns) only ever follows a CALL to a NAMED,
// top-level, switch-shaped function — never an inline IIFE, and never an
// if-chain. Pulling this out into a named top-level function with a
// `switch` closes spacing.mjs — but a first attempt that kept the original
// TWO-LEVEL branching (an outer switch on `visualState` whose default case
// CALLS a second named dispatcher for the `visualDriveMode` sub-branches)
// broke ts-colors.mjs instead: its lexical-binding resolver
// (absenceLexicalBinding) treats a switch's CaseBlock as an opaque wall for
// ANY identifier lookup inside a case — including a plain function-call
// reference that has nothing to do with a declaration inside the switch —
// so a case that itself CALLS another top-level dispatcher can never be
// resolved by ts-colors.mjs (spacing.mjs has dedicated, documented support
// for exactly this shape; ts-colors.mjs does not). The fix that satisfies
// all three: collapse `visualState` + `visualDriveMode` into ONE finite
// composite key (`driveChipKeyFor`, itself a plain if-chain — never
// referenced FROM inside a switch case) so `driveChipConfig` stays a
// SINGLE flat switch with every clause returning a literal object directly,
// no call anywhere inside any case.
//
// This recipe matches the ORIGINAL IIFE by exact source text (not a partial
// AST shape) — the rewrite is a bespoke restructuring unique to this one
// component, and a text-exact match means ANY future edit to the chip logic
// (even a single label change) makes this recipe refuse instead of silently
// reproducing stale behaviour.
const DRIVE_CHIP_ANCHOR_BEFORE =
  `/** Presentation state; only annotation and connectivity gate input. */\ntype DriveMode = 'annotating' | 'agent-working' | 'you-driving' | 'disconnected' | 'other-driving' | 'idle'\n`

const DRIVE_CHIP_IIFE_ORIGINAL = `// ── ADR-040 D6 — header chip config (icon + text label + colour), derived
  // from \`visualState\`. Words + icon back up the colour for accessibility
  // (never colour alone). The 'idle' bucket further distinguishes connection
  // lifecycle (connecting/reconnecting) from a genuinely idle, ready-to-drive
  // frame — the old corner pill's connecting/disconnected states still need
  // SOME visible home now that the pill itself is gone.
  const driveChip = (() => {
    if (visualState === 'agent-working') {
      return { label: \`\${agentDisplayName} is browsing…\`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
    }
    if (visualState === 'you-driving') {
      return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
    }
    if (visualState === 'annotating') {
      return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
    }
    if (visualState === 'error') {
      return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
    }
    // 'idle' visualState — visualDriveMode further distinguishes
    // disconnected/other-driving/genuinely-idle, reading the SAME display
    // source of truth \`visualState\` itself derives from, instead of
    // re-deriving \`!connected\`/\`controlledByOther\` here too.
    if (visualDriveMode === 'disconnected') {
      return {
        label: statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
        Icon: SpinnerGap,
        textClass: 'text-[var(--color-muted)]',
        dotClass: 'bg-[var(--color-muted)]',
        pulse: false,
      }
    }
    if (visualDriveMode === 'other-driving') {
      // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
      // keyboard and omnibox all still work while someone else is also active
      // (operator directive, 2026-08-03). The old label read "Someone else is
      // driving", which told the user their input would be ignored — and it
      // was, because the client and server both gated on the lock. Both gates
      // are gone; the chip now just says who else is here.
      return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    }
    return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  })()`

const DRIVE_CHIP_TOP_LEVEL_DECLARATIONS = `
// ── ADR-040 D6 — the finite key \`driveChipConfig\` switches on below.
// \`visualState\` alone decides 4 of the 7 chip states directly; the 5th
// (\`'idle'\`) needs \`visualDriveMode\` too (connecting/reconnecting vs.
// someone else driving vs. genuinely idle) — this collapses both into ONE
// discriminant so \`driveChipConfig\` can stay a single flat switch instead
// of delegating to a second dispatcher function from inside a \`case\`. That
// avoids a real gap: ts-colors.mjs's lexical-binding resolver
// (absenceLexicalBinding) treats a \`switch\`'s CaseBlock as an opaque wall
// for identifier lookups — by design, since it has no
// case-delegates-to-another-dispatcher support the way spacing.mjs's
// resolveDispatcherMember does — so a case clause that itself CALLS another
// top-level function can never be proven by ts-colors.mjs, only by
// spacing.mjs. A single switch with only literal-object-returning clauses
// has no such call for either scanner to trip on.
type DriveChipKey = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle-disconnected' | 'idle-other-driving' | 'idle-default'

function driveChipKeyFor(visualState: VisualState, visualDriveMode: DriveMode): DriveChipKey {
  if (visualState !== 'idle') return visualState
  if (visualDriveMode === 'disconnected') return 'idle-disconnected'
  if (visualDriveMode === 'other-driving') return 'idle-other-driving'
  return 'idle-default'
}

// ── ADR-040 D6 — header chip config (icon + text label + colour), derived
// from \`visualState\`/\`visualDriveMode\` via driveChipKeyFor above. Words +
// icon back up the colour for accessibility (never colour alone).
//
// Top-level named function (not the inline IIFE this replaces) with a
// single \`switch\` on the finite \`DriveChipKey\` union, every clause
// returning a plain object literal — behaviourally identical to the
// original if-chain (each key maps to exactly the same returned literal
// the removed inline branch returned for that same visualState/
// visualDriveMode combination). This shape is required, not stylistic:
// none of the three design-system scanners can resolve a member read
// (\`driveChip.textClass\`, \`driveChip.dotClass\`) off a \`const\` bound to an
// inline IIFE's if-chain — spacing.mjs's finite-dispatcher proof
// (resolveDispatcherMember/collectFiniteDispatcherReturns) only follows a
// CALL to a top-level, switch-shaped function whose clauses return object
// literals directly (or delegate via a call OUTSIDE any case block); and
// ts-colors.mjs's calleeDeclaration/absenceFactory path needs that same
// named-top-level-call shape, with every clause a literal (see
// driveChipKeyFor's comment for why a two-function, call-from-inside-a-
// case version breaks ts-colors.mjs specifically).
function driveChipConfig(visualState: VisualState, visualDriveMode: DriveMode, statusState: LiveStatus, agentDisplayName: string) {
  switch (driveChipKeyFor(visualState, visualDriveMode)) {
    case 'agent-working':
      return { label: \`\${agentDisplayName} is browsing…\`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
    case 'you-driving':
      return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
    case 'annotating':
      return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
    case 'error':
      return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
    case 'idle-disconnected':
      return {
        label: statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
        Icon: SpinnerGap,
        textClass: 'text-[var(--color-muted)]',
        dotClass: 'bg-[var(--color-muted)]',
        pulse: false,
      }
    case 'idle-other-driving':
      // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
      // keyboard and omnibox all still work while someone else is also active
      // (operator directive, 2026-08-03). The old label read "Someone else is
      // driving", which told the user their input would be ignored — and it
      // was, because the client and server both gated on the lock. Both gates
      // are gone; the chip now just says who else is here.
      return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    default:
      return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  }
}
`

const DRIVE_CHIP_CALL_REPLACEMENT =
  `// ── ADR-040 D6 — header chip config; see driveChipConfig's top-level\n` +
  `  // definition (module scope, above) for why this is a named function call\n` +
  `  // and not an inline IIFE — none of the three design-system scanners can\n` +
  `  // resolve a member read off a \`const\` bound to an inline IIFE's if-chain.\n` +
  `  const driveChip = driveChipConfig(visualState, visualDriveMode, statusState, agentDisplayName)`

export function planBrowserLiveViewDriveChip(source, text) {
  const anchorIdx = text.indexOf(DRIVE_CHIP_ANCHOR_BEFORE)
  const iifeIdx = text.indexOf(DRIVE_CHIP_IIFE_ORIGINAL)

  if (iifeIdx === -1) {
    if (text.includes('function driveChipConfig(') && text.includes('function driveChipKeyFor(')) {
      return noop(['already rewritten (driveChipKeyFor/driveChipConfig top-level functions present)'])
    }
    return refuse('could not find the exact original driveChip IIFE text — site has changed; refusing rather than guess at a partial match')
  }
  if (anchorIdx === -1) return refuse('could not find the DriveMode type-declaration anchor to insert the new top-level functions after')

  const edits = [
    { start: anchorIdx + DRIVE_CHIP_ANCHOR_BEFORE.length, end: anchorIdx + DRIVE_CHIP_ANCHOR_BEFORE.length, replacement: DRIVE_CHIP_TOP_LEVEL_DECLARATIONS },
    { start: iifeIdx, end: iifeIdx + DRIVE_CHIP_IIFE_ORIGINAL.length, replacement: DRIVE_CHIP_CALL_REPLACEMENT },
  ]
  return ok(edits, [
    'inline IIFE if-chain replaced with a call to a new top-level `driveChipConfig`, itself switching on a new composite `driveChipKeyFor(visualState, visualDriveMode)` key so every switch clause returns a literal object directly (no case-internal call, which ts-colors.mjs cannot resolve) — closes spacing.mjs\'s two `driveChip.textClass`/`driveChip.dotClass` unsupported findings; ts-colors.mjs and typography.mjs were already clean on this site before and after.',
  ])
}

// ── Site registry + driver ──────────────────────────────────────────────────

export const SITES = [
  { id: 'goal-indicator-non-active-state', file: 'src/components/chat/GoalIndicator.tsx', plan: planGoalIndicatorNonActiveState },
  { id: 'goal-pill-tray-icon-and-pulse', file: 'src/components/chat/GoalPillTray.tsx', plan: planGoalPillTrayIconAndPulse },
  { id: 'file-write-confirm-text-class', file: 'src/components/chat/tools/FileWriteConfirm.tsx', plan: planFileWriteConfirmTextClass },
  { id: 'table-part-inert-props', file: 'src/components/library/preview/viewparts/TablePart.tsx', plan: planTablePartInertProps },
  { id: 'browser-live-view-drive-chip', file: 'src/components/browser/BrowserLiveView.tsx', plan: planBrowserLiveViewDriveChip },
]

export function planSite(repoRoot, site) {
  const filePath = resolve(repoRoot, site.file)
  if (!isInAllowedRoot(repoRoot, filePath)) return { ...site, ok: false, reason: `${site.file} is outside the allowed edit roots` }
  let text
  try {
    text = readFile(filePath)
  } catch (err) {
    return { ...site, ok: false, reason: `could not read ${site.file}: ${err.message}` }
  }
  const source = parseSourceFile(filePath, text)
  const result = site.plan(source, text)
  return { ...site, filePath, text, ...result }
}

export function runCodemod({ repoRoot, apply }) {
  const results = []
  for (const site of SITES) {
    const planned = planSite(repoRoot, site)
    if (!planned.ok) { results.push(planned); continue }
    const after = planned.edits.length > 0 ? applyEdits(planned.text, planned.edits) : planned.text
    const diff = planned.edits.length > 0 ? unifiedDiff(site.file, planned.text, after) : ''
    if (apply && planned.edits.length > 0) writeFileAtomic(planned.filePath, after)
    results.push({ ...planned, after, diff })
  }
  return {
    applied: apply,
    totalSites: SITES.length,
    totalApplied: results.filter((r) => r.ok && r.edits && r.edits.length > 0).length,
    totalAlreadyApplied: results.filter((r) => r.ok && r.alreadyApplied).length,
    totalRefused: results.filter((r) => !r.ok).length,
    results,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  for (const r of result.results) {
    if (!r.ok) { console.log(`REFUSED ${r.id} (${r.file}) — ${r.reason}`); continue }
    if (r.alreadyApplied) { console.log(`NOOP ${r.id} (${r.file}) — ${r.notes.join('; ')}`); continue }
    if (r.diff) console.log(r.diff)
    for (const note of r.notes) console.log(`  # ${note}`)
  }
  console.log(`\n${result.totalApplied} site(s) ${apply ? 'applied' : 'would apply'}, ${result.totalAlreadyApplied} already applied, ${result.totalRefused} refused, of ${result.totalSites} total.`)
}
