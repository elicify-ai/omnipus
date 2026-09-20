// Design-system lock scanner — raw controls and low-level imports (Stage B).
//
// Contract: design-system/enforcement/contract.json — export scan({ path, source, policy })
// returning Finding[] synchronously, plus the handled extensions. The policy bag is
// accepted and intentionally unused here: controls rules need no token data, and
// exceptions/baselines are applied centrally by the orchestrator, never in scanners.
//
// Rules (docs/internal/design/design-system-definition.md E1, D5):
//   controls/raw-button              — JSX <button>, element factories, document.createElement
//   controls/raw-dialog              — JSX <dialog>, element factories, document.createElement
//   controls/global-confirm          — window/globalThis/bare/aliased confirm() calls
//   controls/checkbox-as-switch      — Checkbox or input[type=checkbox] with role="switch",
//                                      including spreads and dynamically-valued role/type that
//                                      cannot be statically excluded
//   controls/radix-import            — @radix-ui/* imports, re-exports, type-only imports,
//                                      dynamic import(), require() and import-equals. Exempt
//                                      only for the file that IS the catalog-registered
//                                      "primitive" wrapper for that boundary — see below.
//   controls/shadcn-low-level-import — ui-kit imports of names outside the registered public
//                                      boundary (design-system/catalog.json publicExports and
//                                      publicTypes); namespaces and whole-module reach are
//                                      never the curated surface
//   controls/parse-error             — fail-closed: parse failures are explicit findings
//
// All findings are raw: this scanner never exempts directories or files by path alone. The
// one deliberate exception is controls/radix-import inside a file the catalog itself
// classifies "primitive" (design-system/catalog.json): that classification means the file's
// whole registered job is wrapping the underlying Radix (or Radix-adjacent, e.g.
// @radix-ui/react-slot) package, so its own Radix import(s) are the intended architecture,
// not a violation — the rule exists to stop SCREENS reaching past our components, not to
// stop our components existing (see isPrimitiveWrapperFile below). Every other classification
// (composite, domain, foundations, application) still reports: those modules are meant to be
// built from primitives, not to reach into Radix directly, so being catalogued does not bless
// them, and an uncatalogued file in src/components/ui/ is never blessed at all. The approved
// registered boundary for ui-kit CONSUMERS (controls/shadcn-low-level-import) remains the
// catalog's exact export lists, not a broad allowance of src/components/ui (domain widgets
// live there too and stay reported).
//
// Attribute case policy: the HTML input `type` attribute is ASCII case-insensitive
// (so type="CHECKBOX" is a checkbox), while ARIA role tokens are case-sensitive
// (so role="Switch" is not the switch role). Input role="switch" without any type
// attribute is reported as a homemade switch path (decided policy, fail-closed).
//
// Single-file scope model: sequential re-aliasing of the browser confirm and of the
// React factory globals is tracked, and identifier bindings (imports, declarations,
// parameters, hoisted function and `var` declarations) shadow the globals when in
// scope. Assignments that move a tracked value across a function boundary into an
// outer binding (a closure write) or to an undeclared name (an implicit global)
// cannot be ordered against that binding's uses, so a confirm-valued write is
// fail-closed: the prohibited global reference is flagged where it is captured
// (canonical syntax `window.confirm`), per the final limit review blessing.
// Browser-global confirm references (including tracked aliases) that escape ordinary
// alias/call flow through arguments, object members, returns, or
// `.call`/`.apply`/`.bind` receivers are reported at
// the reference itself. Tracked declarations and assignments retain their existing
// alias-flow behavior so one source occurrence is not counted twice. Chained
// assignments and cross-function captures of factory/document/browser-object globals
// remain outside the current model; confirm-valued closure writes are fail-closed.

import posix from 'node:path/posix'
import ts from 'typescript'

export const extensions = ['.js', '.jsx', '.ts', '.tsx']

const RULES = {
  RAW_BUTTON: 'controls/raw-button',
  RAW_DIALOG: 'controls/raw-dialog',
  GLOBAL_CONFIRM: 'controls/global-confirm',
  CHECKBOX_AS_SWITCH: 'controls/checkbox-as-switch',
  RADIX_IMPORT: 'controls/radix-import',
  SHADCN_LOW_LEVEL: 'controls/shadcn-low-level-import',
  PARSE_ERROR: 'controls/parse-error',
}

const RAW_INTRINSICS = new Map([
  ['button', RULES.RAW_BUTTON],
  ['dialog', RULES.RAW_DIALOG],
])
const FACTORY_NAMES = new Set(['createElement', 'jsx', 'jsxs', 'jsxDEV', '_jsx', '_jsxs', '_jsxDEV'])
const REACT_MODULES = new Set(['react', 'react/jsx-runtime', 'react/jsx-dev-runtime'])
const CONFIRM_OBJECTS = new Set(['window', 'globalThis'])
const BUILTIN_GLOBAL_VALUES = new Map([
  ['confirm', 'confirm'],
  ['window', 'browser-object'],
  ['globalThis', 'browser-object'],
  ['document', 'document'],
  ['React', 'react-ns'],
])
const ALIAS_TARGET = 'src/'
const UI_PREFIX = 'src/components/ui/'
const CHECKBOX_SOURCE = 'src/components/ui/checkbox'
const EXTENSION_PATTERN = /\.(?:tsx|ts|jsx|js|mjs|cjs)$/

// ---------------------------------------------------------------------------
// Scanner entry point
// ---------------------------------------------------------------------------

export function scan({ path: rawPath, source, catalog } = {}) {
  const filePath = normalizeScanPath(rawPath)
  if (typeof source !== 'string' || source.length === 0) {
    throw new Error('controls.mjs: source must be a non-empty string')
  }
  const sourceFile = ts.createSourceFile(filePath, source, ts.ScriptTarget.Latest, true, scriptKindFor(filePath))
  if (sourceFile.parseDiagnostics.length > 0) {
    return [parseErrorFinding(filePath, sourceFile, sourceFile.parseDiagnostics[0])]
  }
  const findings = analyze(filePath, sourceFile, catalog)
  findings.sort(byRuleThenSyntaxThenPosition)
  return findings
}

// Documented path behavior (review controls-review-08): backslashes normalize to
// POSIX so Windows-walked trees fingerprint identically; absolute paths (POSIX root
// or drive letter) and paths escaping the repository root are rejected loudly
// rather than silently mangled into a bogus baseline path.
function normalizeScanPath(rawPath) {
  if (typeof rawPath !== 'string' || rawPath.length === 0) {
    throw new Error('controls.mjs: path must be a non-empty string')
  }
  const forward = rawPath.replace(/\\/g, '/')
  if (forward.startsWith('/') || /^[A-Za-z]:\//.test(forward) || forward.startsWith('../')) {
    throw new Error(`controls.mjs: path must be repository-relative POSIX, got: ${rawPath}`)
  }
  const normalized = posix.normalize(forward)
  return normalized.startsWith('./') ? normalized.slice(2) : normalized
}

function scriptKindFor(filePath) {
  if (filePath.endsWith('.tsx')) return ts.ScriptKind.TSX
  if (filePath.endsWith('.ts')) return ts.ScriptKind.TS
  if (filePath.endsWith('.jsx')) return ts.ScriptKind.JSX
  return ts.ScriptKind.JS
}

function parseErrorFinding(filePath, sourceFile, diagnostic) {
  const syntax = flattenDiagnosticMessages(diagnostic.messageText) || 'parse error'
  const position = typeof diagnostic.start === 'number' ? diagnostic.start : sourceFile.getEnd()
  const { line, character } = sourceFile.getLineAndCharacterOfPosition(position)
  return makeFinding(RULES.PARSE_ERROR, filePath, syntax, `${filePath} failed to parse: ${syntax} (fail-closed; infrastructure errors are not baselineable)`, line + 1, character + 1)
}

function flattenDiagnosticMessages(messageText) {
  if (typeof messageText === 'string') return messageText
  const parts = []
  const walkChain = (chain) => {
    if (!chain) return
    parts.push(chain.messageText)
    if (chain.next) chain.next.forEach(walkChain)
  }
  walkChain(messageText)
  return parts.join(' ')
}

function makeFinding(ruleId, filePath, syntax, message, line, column) {
  const result = { ruleId, path: filePath, syntax, message }
  if (Number.isInteger(line) && line > 0) result.line = line
  if (Number.isInteger(column) && column > 0) result.column = column
  return result
}

function byRuleThenSyntaxThenPosition(a, b) {
  return a.ruleId.localeCompare(b.ruleId) || a.syntax.localeCompare(b.syntax) || (a.line ?? 0) - (b.line ?? 0) || (a.column ?? 0) - (b.column ?? 0)
}

// ---------------------------------------------------------------------------
// Analysis driver
// ---------------------------------------------------------------------------

function analyze(filePath, sourceFile, catalog) {
  const context = {
    filePath,
    findings: [],
    importMap: new Map(),
    uiBoundaries: null,
    catalog,
    scopes: [{ names: new Map(), isFunctionScope: true }],
  }
  for (const statement of sourceFile.statements) {
    if (isModuleDeclaration(statement)) handleModuleDeclaration(context, statement)
    // Function declarations hoist to the top of their scope, so their names
    // shadow globals even for call sites textually above the declaration.
    if (ts.isFunctionDeclaration(statement) && statement.name) {
      declareInModuleScope(context, statement.name.text, null)
    }
  }
  // `var` declarations hoist to the module scope the same way, so a call above
  // a later `var confirm = ...` resolves to the local binding, never the global.
  for (const statement of sourceFile.statements) {
    hoistVarDeclarations(context, statement)
  }
  for (const statement of sourceFile.statements) {
    visitNode(context, statement)
  }
  return context.findings
}

function isModuleDeclaration(node) {
  return ts.isImportDeclaration(node) || ts.isExportDeclaration(node) || ts.isImportEqualsDeclaration(node)
}

function visitNode(context, node) {
  if (isModuleDeclaration(node)) return
  if (ts.isVariableDeclaration(node)) return handleVariableDeclaration(context, node)
  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
    handleAssignmentExpression(context, node)
  }
  if (ts.isCallExpression(node)) handleCallExpression(context, node)
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    handleEscapedConfirmReference(context, node)
  }
  if (ts.isIdentifier(node)) handleEscapedConfirmIdentifier(context, node)
  if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) handleJsxElement(context, node)

  if ((ts.isFunctionDeclaration(node) || ts.isClassDeclaration(node)) && node.name) {
    declareName(context, node.name.text, null, false)
  }

  const scopeKind = scopeKindOf(node)
  if (scopeKind) {
    context.scopes.push({ names: new Map(), isFunctionScope: scopeKind === 'function' })
    if (scopeKind === 'function') {
      if (ts.isFunctionExpression(node) && node.name) declareName(context, node.name.text, null, false)
      for (const parameter of node.parameters) declareBindingPattern(context, parameter.name)
      // `var` declarations anywhere in this function (nested blocks and loops
      // included, nested functions excluded) bind for the whole function.
      hoistVarDeclarations(context, node.body)
    }
    if (ts.isBlock(node)) declareHoistedFunctions(context, node)
    if (ts.isCatchClause(node) && node.variableDeclaration) {
      declareBindingPattern(context, node.variableDeclaration.name)
    }
  }

  ts.forEachChild(node, (child) => visitNode(context, child))

  if (scopeKind) context.scopes.pop()
}

// `var` declarations hoist to the nearest function scope regardless of the block
// they are written in, so their names shadow the tracked globals for the whole
// function — including call sites textually above the declaration. Nested
// function bodies stop the walk: their vars belong to those functions. Hoisted
// names carry no value; the declaration's initializer updates the binding when
// it is reached sequentially.
function hoistVarDeclarations(context, node) {
  if (!node || isFunctionLikeBoundary(node)) return
  if (ts.isVariableDeclaration(node) && isVarDeclarationList(node.parent)) {
    declareBindingPatternFunctionScoped(context, node.name)
  }
  ts.forEachChild(node, (child) => hoistVarDeclarations(context, child))
}

function isFunctionLikeBoundary(node) {
  return (
    ts.isFunctionDeclaration(node) ||
    ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) ||
    ts.isMethodDeclaration(node) ||
    ts.isConstructorDeclaration(node) ||
    ts.isGetAccessorDeclaration(node) ||
    ts.isSetAccessorDeclaration(node) ||
    ts.isClassStaticBlockDeclaration(node)
  )
}

function declareBindingPatternFunctionScoped(context, patternName) {
  if (ts.isIdentifier(patternName)) {
    declareName(context, patternName.text, null, true)
    return
  }
  const elements = ts.isObjectBindingPattern(patternName) || ts.isArrayBindingPattern(patternName) ? patternName.elements : []
  for (const element of elements) {
    if (ts.isBindingElement(element)) declareBindingPatternFunctionScoped(context, element.name)
  }
}

function declareHoistedFunctions(context, block) {
  for (const statement of block.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name) {
      declareName(context, statement.name.text, null, false)
    }
  }
}

function scopeKindOf(node) {
  if (
    ts.isFunctionDeclaration(node) ||
    ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) ||
    ts.isMethodDeclaration(node) ||
    ts.isConstructorDeclaration(node) ||
    ts.isGetAccessorDeclaration(node) ||
    ts.isSetAccessorDeclaration(node)
  ) {
    return 'function'
  }
  if (ts.isBlock(node) || ts.isForStatement(node) || ts.isForOfStatement(node) || ts.isForInStatement(node) || ts.isCatchClause(node) || ts.isClassStaticBlockDeclaration(node)) {
    return 'block'
  }
  return null
}

// ---------------------------------------------------------------------------
// Scopes and bindings
//
// A binding maps a local name to one of the semantic values below, or to null
// for "locally bound but none of the tracked globals" (the shadowing case):
//   confirm        — evaluates to the browser confirm function
//   browser-object — evaluates to window/globalThis
//   document       — evaluates to the DOM document
//   dom-factory    — evaluates to document.createElement
//   react-ns       — evaluates to a React module namespace
//   react-factory  — evaluates to createElement/jsx/jsxs/jsxDEV from React
// ---------------------------------------------------------------------------

function declareName(context, name, value, isVar) {
  const target = isVar ? nearestFunctionScope(context) : context.scopes[context.scopes.length - 1]
  target.names.set(name, { value })
}

function nearestFunctionScope(context) {
  for (let index = context.scopes.length - 1; index >= 0; index -= 1) {
    if (context.scopes[index].isFunctionScope) return context.scopes[index]
  }
  return context.scopes[0]
}

function declareBindingPattern(context, patternName) {
  if (ts.isIdentifier(patternName)) {
    declareName(context, patternName.text, null, false)
    return
  }
  const elements = ts.isObjectBindingPattern(patternName) || ts.isArrayBindingPattern(patternName) ? patternName.elements : []
  for (const element of elements) {
    if (ts.isBindingElement(element)) declareBindingPattern(context, element.name)
  }
}

function lookupName(context, name) {
  for (let index = context.scopes.length - 1; index >= 0; index -= 1) {
    const scope = context.scopes[index]
    if (scope.names.has(name)) return scope.names.get(name)
  }
  return null
}

// Scope-stack index of the binding a name resolves to (-1 when unbound), used
// by assignment tracking to tell a sequential same-function write from a
// closure write into an outer scope.
function findBindingScopeIndex(context, name) {
  for (let index = context.scopes.length - 1; index >= 0; index -= 1) {
    if (context.scopes[index].names.has(name)) return index
  }
  return -1
}

function innermostFunctionScopeIndex(context) {
  for (let index = context.scopes.length - 1; index >= 0; index -= 1) {
    if (context.scopes[index].isFunctionScope) return index
  }
  return 0
}

function declareInModuleScope(context, name, value) {
  context.scopes[0].names.set(name, { value })
}

// ---------------------------------------------------------------------------
// Variable declarations: sequential alias tracking for the tracked globals
// ---------------------------------------------------------------------------

function handleVariableDeclaration(context, node) {
  const isVar = isVarDeclarationList(node.parent)
  const initValue = valueOfExpression(context, node.initializer)
  if (ts.isIdentifier(node.name)) {
    declareName(context, node.name.text, initValue, isVar)
  } else if (ts.isObjectBindingPattern(node.name)) {
    for (const element of node.name.elements) {
      if (!ts.isBindingElement(element)) continue
      declareBoundElement(context, element, destructuredValue(initValue, element), isVar)
    }
  }
  if (node.initializer) visitNode(context, node.initializer)
}

function destructuredValue(initValue, element) {
  return memberValueFromSource(initValue, elementPropertySourceName(element))
}

function memberValueFromSource(initValue, sourceName) {
  if (initValue === 'browser-object' && sourceName === 'confirm') return 'confirm'
  if (initValue === 'react-ns' && FACTORY_NAMES.has(sourceName)) return 'react-factory'
  if (initValue === 'document' && sourceName === 'createElement') return 'dom-factory'
  return null
}

function elementPropertySourceName(element) {
  if (element.propertyName && ts.isIdentifier(element.propertyName)) return element.propertyName.text
  return ts.isIdentifier(element.name) ? element.name.text : ''
}

function declareBoundElement(context, element, value, isVar) {
  if (ts.isIdentifier(element.name)) {
    declareName(context, element.name.text, value, isVar)
  } else {
    declareBindingPattern(context, element.name)
  }
}

function isVarDeclarationList(list) {
  return !!list && ts.isVariableDeclarationList(list) && (list.flags & ts.NodeFlags.Let) === 0 && (list.flags & ts.NodeFlags.Const) === 0
}

// ---------------------------------------------------------------------------
// Plain `=` assignments: sequential alias flow inside one function, fail-closed
// capture flag across function boundaries
//
// A write that stays inside the current function updates the binding in place
// (same sequential semantics as a declaration, including clearing on
// reassignment to an untracked value). A write to a binding in an outer scope
// (a closure write) or to an undeclared name (an implicit global) can be
// observed by code this single-file pass cannot order — call sites earlier in
// the file, other functions, later modules — so a confirm-valued write is
// flagged at the capture itself (final limit review: "flag the prohibited
// global-confirm reference where captured").
// ---------------------------------------------------------------------------

function handleAssignmentExpression(context, node) {
  for (const target of assignmentTargets(context, node)) {
    applyAssignmentTarget(context, target, node)
  }
}

function assignmentTargets(context, node) {
  if (ts.isIdentifier(node.left)) {
    return [{ name: node.left.text, value: valueOfExpression(context, node.right) }]
  }
  if (ts.isObjectLiteralExpression(node.left)) {
    const initValue = valueOfExpression(context, node.right)
    const targets = []
    for (const property of node.left.properties) {
      const sourceName = assignmentPropertySourceName(property)
      if (!sourceName) continue
      const targetName = ts.isPropertyAssignment(property) && ts.isIdentifier(property.initializer)
        ? property.initializer.text
        : ts.isShorthandPropertyAssignment(property)
          ? property.name.text
          : null
      if (targetName) targets.push({ name: targetName, value: memberValueFromSource(initValue, sourceName) })
    }
    return targets
  }
  return []
}

function assignmentPropertySourceName(property) {
  if (ts.isPropertyAssignment(property)) {
    return ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) ? property.name.text : ''
  }
  if (ts.isShorthandPropertyAssignment(property)) return property.name.text
  return ''
}

function applyAssignmentTarget(context, target, node) {
  const bindingIndex = findBindingScopeIndex(context, target.name)
  if (bindingIndex === -1 || bindingIndex < innermostFunctionScopeIndex(context)) {
    if (target.value === 'confirm') reportConfirmCapture(context, node, target.name)
    return
  }
  context.scopes[bindingIndex].names.set(target.name, { value: target.value })
}

// The statically-known semantic value of an expression, or null. Direct
// member/element access on a resolved global object, the unshadowed builtin
// identifiers themselves, require('react')-style module values, and identifiers
// already tracked as aliases (sequential re-aliasing only).
function valueOfExpression(context, expression) {
  if (!expression) return null
  const unwrapped = unwrapTypeWrapper(expression)
  if (ts.isStringLiteral(unwrapped) && RAW_INTRINSICS.has(unwrapped.text)) return `raw-${unwrapped.text}`
  if (ts.isConditionalExpression(unwrapped)) {
    const whenTrue = valueOfExpression(context, unwrapped.whenTrue)
    const whenFalse = valueOfExpression(context, unwrapped.whenFalse)
    const raw = new Set([whenTrue, whenFalse].filter((value) => value === 'raw-button' || value === 'raw-dialog'))
    if (raw.size === 2) return 'raw-button-or-dialog'
    if (raw.size === 1) return [...raw][0]
    return whenTrue === whenFalse ? whenTrue : null
  }
  if (ts.isPropertyAccessExpression(unwrapped) || ts.isElementAccessExpression(unwrapped)) {
    const name = memberName(unwrapped)
    if (name === 'confirm' && resolvesToBrowserObject(context, unwrapped.expression)) return 'confirm'
    if (name === 'document' && resolvesToBrowserObject(context, unwrapped.expression)) return 'document'
    if (name === 'createElement' && resolvesToDocument(context, unwrapped.expression)) return 'dom-factory'
    if (name && FACTORY_NAMES.has(name) && resolvesToReactNamespace(context, unwrapped.expression)) return 'react-factory'
    return null
  }
  if (ts.isIdentifier(unwrapped)) {
    const builtin = BUILTIN_GLOBAL_VALUES.get(unwrapped.text)
    if (builtin) {
      const binding = lookupName(context, unwrapped.text)
      return binding === null || binding.value === builtin ? builtin : null
    }
    return lookupName(context, unwrapped.text)?.value ?? null
  }
  if (isRequireCall(context, unwrapped)) {
    const specifier = stringModuleArgument(unwrapped)
    if (specifier && REACT_MODULES.has(specifier)) return 'react-ns'
  }
  return null
}

function memberName(expression) {
  if (ts.isPropertyAccessExpression(expression)) return expression.name.text
  if (ts.isElementAccessExpression(expression) && ts.isStringLiteral(expression.argumentExpression)) return expression.argumentExpression.text
  return null
}

// Resolution predicates: an expression "resolves to" a global only when the
// name is unbound (true global reference) or provably an alias of it. A local
// binding with no tracked value (parameter, unrelated import, plain local)
// shadows the global and blocks detection — review controls-review-03/04.
//
// TypeScript wrappers around the object of a member access or a call target
// are a runtime no-op (parens `(x)`, non-null `x!`) or erased entirely at
// compile time (`x as T`, `x satisfies T`, the angle-bracket `<T>x` form), so
// the emitted code is identical to the unwrapped expression. Every resolver
// and call-target check below unwraps through `unwrapTypeWrapper` first —
// otherwise `(window).confirm(...)`, `(window as any).confirm(...)`,
// `window!.confirm(...)`, `(window satisfies Window).confirm(...)`,
// `(React as any).createElement(...)` and `(document as any).createElement(...)`
// all read as "object is not a bare identifier" and silently pass review-08's
// fixture family through unreported (found in the FIX-P4 defect report).
function isTypeWrapperNode(node) {
  return (
    ts.isParenthesizedExpression(node)
    || ts.isAsExpression(node)
    || ts.isSatisfiesExpression(node)
    || ts.isNonNullExpression(node)
    || ts.isTypeAssertionExpression(node)
  )
}

function unwrapTypeWrapper(expression) {
  let current = expression
  while (current && isTypeWrapperNode(current)) {
    current = current.expression
  }
  return current
}

// The inverse walk: starting from a reference, climb back OUT through any
// wrapper nodes that hold it as their `.expression`, to find the nearest
// semantically meaningful parent and the node (the reference itself, or the
// outermost wrapper around it) that sits directly under that parent. The
// escape-exclusion checks below key off of the same call-target / initializer
// / tracked-assignment shapes already checked for the bare form; a wrapper
// must not change what those checks see, or a wrapped reference that is
// legitimately tracked elsewhere (`(window.confirm)(...)`, `const c =
// (window.confirm)`) gets counted as escaping AND as its normal alias/call
// finding — one source occurrence reported twice.
function effectiveParentContext(node) {
  let current = node
  let parent = current.parent
  while (parent && isTypeWrapperNode(parent) && parent.expression === current) {
    current = parent
    parent = current.parent
  }
  return { parent, effectiveNode: current }
}

function resolvesToBrowserObject(context, expression) {
  const unwrapped = unwrapTypeWrapper(expression)
  if (!ts.isIdentifier(unwrapped)) return false
  if (CONFIRM_OBJECTS.has(unwrapped.text)) {
    const binding = lookupName(context, unwrapped.text)
    return binding === null || binding.value === 'browser-object'
  }
  return lookupName(context, unwrapped.text)?.value === 'browser-object'
}

function resolvesToDocument(context, expression) {
  const unwrapped = unwrapTypeWrapper(expression)
  if (ts.isIdentifier(unwrapped)) {
    if (unwrapped.text === 'document') {
      const binding = lookupName(context, 'document')
      return binding === null || binding.value === 'document'
    }
    return lookupName(context, unwrapped.text)?.value === 'document'
  }
  if (ts.isPropertyAccessExpression(unwrapped) && unwrapped.name.text === 'document') {
    return resolvesToBrowserObject(context, unwrapped.expression)
  }
  return false
}

function resolvesToReactNamespace(context, expression) {
  const unwrapped = unwrapTypeWrapper(expression)
  if (!ts.isIdentifier(unwrapped)) return false
  if (unwrapped.text === 'React') {
    const binding = lookupName(context, 'React')
    return binding === null || binding.value === 'react-ns'
  }
  return lookupName(context, unwrapped.text)?.value === 'react-ns'
}

// ---------------------------------------------------------------------------
// Call expressions: global confirm, element factories, dynamic module reach
// ---------------------------------------------------------------------------

function handleCallExpression(context, node) {
  // The call target itself can be wrapped the same way as a member-access
  // object (`(confirm)('sure?')`, `(window.confirm)('sure?')`); unwrap once,
  // up front, so every branch below sees the real callee shape.
  const callee = unwrapTypeWrapper(node.expression)
  if (callee.kind === ts.SyntaxKind.ImportKeyword) {
    reportWholeModuleReach(context, node, stringModuleArgument(node), (specifier) => `import("${specifier}")`)
    return
  }
  if (ts.isIdentifier(callee)) {
    if (callee.text === 'require' && lookupName(context, 'require') === null) {
      reportWholeModuleReach(context, node, stringModuleArgument(node), (specifier) => `require("${specifier}")`)
      return
    }
    const binding = lookupName(context, callee.text)
    if (callee.text === 'confirm' && binding === null) {
      reportConfirmCall(context, node, 'the unshadowed browser global')
    } else if (binding?.value === 'confirm') {
      reportConfirmCall(context, node, `a tracked alias "${callee.text}" of the browser global`)
    } else {
      // Bare factory names are governed when unbound (compiler-emitted/global)
      // or provably an alias of a React factory or document.createElement.
      const factoryValue = FACTORY_NAMES.has(callee.text) && binding === null ? 'react-factory' : binding?.value
      if (factoryValue === 'react-factory') reportFactoryCall(context, node)
      else if (factoryValue === 'dom-factory') reportDomCreateElementCall(context, node)
    }
    return
  }
  if (ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee)) {
    const name = memberName(callee)
    // A computed key that isn't a string literal (`window['con' + 'firm']`)
    // cannot be proven to be anything in particular — including "not
    // confirm/createElement/a React factory". Once the object side is
    // provably one of the tracked globals, fail closed and report exactly as
    // if the (unknowable) key had matched, rather than staying silent.
    const computedNonLiteral = ts.isElementAccessExpression(callee) && name === null
    if ((name === 'confirm' || computedNonLiteral) && resolvesToBrowserObject(context, callee.expression)) {
      reportConfirmCall(
        context,
        node,
        computedNonLiteral
          ? `a computed member of ${displayName(callee.expression)} that cannot be statically resolved (fail-closed: the key must be a literal to rule out confirm)`
          : `the ${displayName(callee.expression)} member`,
      )
      return
    }
    if ((name === 'createElement' || computedNonLiteral) && resolvesToDocument(context, callee.expression)) {
      reportDomCreateElementCall(context, node)
      return
    }
    if (((name && FACTORY_NAMES.has(name)) || computedNonLiteral) && resolvesToReactNamespace(context, callee.expression)) {
      reportFactoryCall(context, node)
    }
  }
}

function handleEscapedConfirmReference(context, node) {
  if (memberName(node) !== 'confirm' || !resolvesToBrowserObject(context, node.expression)) return
  const { parent, effectiveNode } = effectiveParentContext(node)
  if (ts.isCallExpression(parent) && parent.expression === effectiveNode) return
  if (ts.isVariableDeclaration(parent) && parent.initializer === effectiveNode) return
  if (
    ts.isBinaryExpression(parent)
    && parent.operatorToken.kind === ts.SyntaxKind.EqualsToken
    && parent.right === effectiveNode
    && (ts.isIdentifier(parent.left) || ts.isObjectLiteralExpression(parent.left))
  ) return
  context.findings.push(
    makeFinding(RULES.GLOBAL_CONFIRM, context.filePath, 'window.confirm', 'Browser confirm function reference escapes the tracked direct-call and local-alias flow; use the ConfirmDialog composite (design-system-definition.md D5) or register an exception.', ...positionOf(node))
  )
}

function handleEscapedConfirmIdentifier(context, node) {
  if (valueOfExpression(context, node) !== 'confirm') return
  const parent = node.parent
  const { parent: effectiveParent, effectiveNode } = effectiveParentContext(node)
  if (ts.isCallExpression(effectiveParent) && effectiveParent.expression === effectiveNode) return
  if (ts.isVariableDeclaration(parent) && parent.name === node) return
  if (ts.isVariableDeclaration(effectiveParent) && effectiveParent.initializer === effectiveNode) return
  if (ts.isParameter(parent) && parent.name === node) return
  if (ts.isBindingElement(parent) && (parent.name === node || parent.propertyName === node)) return
  if (ts.isPropertyAccessExpression(parent) && parent.name === node) return
  if (ts.isPropertyAssignment(parent) && parent.name === node && parent.initializer !== node) return
  if ('name' in parent && parent.name === node && !ts.isShorthandPropertyAssignment(parent)) return
  if (ts.isBinaryExpression(parent) && parent.left === node) return
  if (
    ts.isBinaryExpression(effectiveParent)
    && effectiveParent.operatorToken.kind === ts.SyntaxKind.EqualsToken
    && effectiveParent.right === effectiveNode
    && (ts.isIdentifier(effectiveParent.left) || ts.isObjectLiteralExpression(effectiveParent.left))
  ) return
  context.findings.push(
    makeFinding(RULES.GLOBAL_CONFIRM, context.filePath, 'window.confirm', 'Tracked browser confirm reference escapes the direct-call and local-alias flow; use the ConfirmDialog composite (design-system-definition.md D5) or register an exception.', ...positionOf(node))
  )
}

function displayName(expression) {
  return ts.isIdentifier(expression) ? expression.text : 'browser global'
}

function isRequireCall(context, expression) {
  if (!expression || !ts.isCallExpression(expression)) return false
  const callee = unwrapTypeWrapper(expression.expression)
  return ts.isIdentifier(callee) && callee.text === 'require' && lookupName(context, 'require') === null
}

function stringModuleArgument(call) {
  const argument = call.arguments[0]
  return ts.isStringLiteral(argument) ? argument.text : null
}

function reportConfirmCall(context, node, via) {
  const callee = unwrapTypeWrapper(node.expression)
  const syntax = ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee) ? 'window.confirm(...)' : 'confirm(...)'
  context.findings.push(
    makeFinding(RULES.GLOBAL_CONFIRM, context.filePath, syntax, `Browser confirm() reached via ${via}; use the ConfirmDialog composite (design-system-definition.md D5) or register an exception.`, ...positionOf(node))
  )
}

// Canonical syntax is the captured global reference itself (`window.confirm`),
// normalized away from bare-alias/element-access spellings, and distinct from
// the call syntaxes so occurrence accounting stays one-finding-per-reference.
function reportConfirmCapture(context, node, targetName) {
  context.findings.push(
    makeFinding(RULES.GLOBAL_CONFIRM, context.filePath, 'window.confirm', `Browser confirm() reference captured into "${targetName}", which outlives the assigning function (closure or implicit-global write); its later uses cannot be bounded in this file. Use the ConfirmDialog composite (design-system-definition.md D5) or register an exception.`, ...positionOf(node))
  )
}

function reportFactoryCall(context, node) {
  const tag = factoryTagLiteral(node)
  if (!tag || !RAW_INTRINSICS.has(tag)) return
  context.findings.push(
    makeFinding(RULES[tag === 'button' ? 'RAW_BUTTON' : 'RAW_DIALOG'], context.filePath, `createElement("${tag}")`, `Raw <${tag}> element constructed via a React element factory; use the Button or Dialog primitive (design-system-definition.md D5) or register an exception.`, ...positionOf(node))
  )
}

function reportDomCreateElementCall(context, node) {
  const tag = factoryTagLiteral(node)
  if (!tag || !RAW_INTRINSICS.has(tag)) return
  context.findings.push(
    makeFinding(RULES[tag === 'button' ? 'RAW_BUTTON' : 'RAW_DIALOG'], context.filePath, `document.createElement("${tag}")`, `Raw <${tag}> element constructed via document.createElement; use the Button or Dialog primitive (design-system-definition.md D5) or register an exception.`, ...positionOf(node))
  )
}

function factoryTagLiteral(node) {
  const argument = node.arguments[0]
  return ts.isStringLiteral(argument) ? argument.text : null
}

function positionOf(node) {
  const sourceFile = node.getSourceFile()
  const { line, character } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile))
  return [line + 1, character + 1]
}

// ---------------------------------------------------------------------------
// JSX elements
// ---------------------------------------------------------------------------

function handleJsxElement(context, node) {
  const tag = node.tagName
  if (ts.isPropertyAccessExpression(tag)) {
    // Member tags resolve like identifiers: only the Checkbox primitive name is
    // governed; neutral member tags (SwitchPrimitives.Root) stay allowed and
    // Radix-backed members fail through the import rule instead (review-09).
    if (tag.name.text === 'Checkbox') handleCheckboxComponent(context, node)
    return
  }
  if (!ts.isIdentifier(tag)) return
  if (RAW_INTRINSICS.has(tag.text)) {
    context.findings.push(
      makeFinding(RAW_INTRINSICS.get(tag.text), context.filePath, `<${tag.text}>`, `Raw <${tag.text}> element in scanned code; use the named primitive (design-system-definition.md D5) or register an exact exception for an approved implementation path.`, ...positionOf(node))
    )
    return
  }
  if (tag.text === 'input') {
    handleCheckboxInput(context, node)
    return
  }
  if (isCheckboxTag(context, tag.text)) {
    handleCheckboxComponent(context, node)
    return
  }
  const alias = lookupName(context, tag.text)?.value
  if (alias === 'raw-button' || alias === 'raw-button-or-dialog') {
    reportAliasedIntrinsic(context, node, 'button')
  }
  if (alias === 'raw-dialog' || alias === 'raw-button-or-dialog') {
    reportAliasedIntrinsic(context, node, 'dialog')
  }
}

function reportAliasedIntrinsic(context, node, tag) {
  context.findings.push(
    makeFinding(RAW_INTRINSICS.get(tag), context.filePath, `<${tag}>`, `Raw <${tag}> element reached through a static JSX alias; use the named primitive (design-system-definition.md D5) or register an exact exception for an approved implementation path.`, ...positionOf(node))
  )
}

function isCheckboxTag(context, tagText) {
  if (tagText === 'Checkbox') return true
  const imported = context.importMap.get(tagText)
  return imported === CHECKBOX_SOURCE
}

function handleCheckboxComponent(context, node) {
  if (hasSpreadAttribute(node)) {
    reportUnresolvedCheckbox(context, node, '<Checkbox {...props}>', 'Checkbox with a spread attribute can carry role="switch" and cannot be statically excluded; pass a literal role or use the Switch primitive (design-system-definition.md D5).')
    return
  }
  const role = jsxAttribute(node, 'role')
  if (!role) return
  if (role.kind === 'literal') {
    if (role.value !== 'switch') return
    context.findings.push(
      makeFinding(RULES.CHECKBOX_AS_SWITCH, context.filePath, '<Checkbox role="switch">', 'Checkbox rendered with role="switch"; use the Switch primitive for boolean preferences (design-system-definition.md D5).', ...positionOf(node))
    )
    return
  }
  reportUnresolvedCheckbox(context, node, '<Checkbox role={expr}>', 'Checkbox with a dynamic role attribute cannot be statically excluded from the switch masquerade; use a literal role or the Switch primitive (design-system-definition.md D5).')
}

// Fail-closed decision table for <input> (type × role):
//   role literal non-switch                     → allowed (not the switch role)
//   type literal non-checkbox                   → allowed (checkbox impossible; a
//                                                  switch role on a non-checkbox
//                                                  type is not checkbox-as-switch)
//   type literal checkbox + role literal switch → confirmed finding
//   no type            + role literal switch     → finding (decided policy: an
//                                                  untyped input presenting as a
//                                                  switch is a homemade switch path)
//   type expression    + role literal switch     → unresolved (dynamic type)
//   type literal checkbox + role expression     → unresolved (dynamic role)
//   type expression    + role expression         → unresolved (dynamic type+role)
//   no type            + role expression         → unresolved (dynamic role)
//   no role (any type state)                     → allowed (plain input/checkbox)
//   spread + any checkbox/switch/dynamic indicator → unresolved (spread reach)
// Case policy: type compares ASCII case-insensitively (HTML attribute), role
// compares case-sensitively (ARIA token).
function handleCheckboxInput(context, node) {
  const role = jsxAttribute(node, 'role')
  const type = jsxAttribute(node, 'type')
  if (role?.kind === 'literal' && role.value !== 'switch') return
  if (type?.kind === 'literal' && type.value.toLowerCase() !== 'checkbox') return
  const typeIsCheckbox = type?.kind === 'literal'
  const roleIsSwitch = role?.kind === 'literal'
  const dynamicIndicator = type?.kind === 'expr' || role?.kind === 'expr'
  if (hasSpreadAttribute(node) && (typeIsCheckbox || roleIsSwitch || dynamicIndicator)) {
    reportUnresolvedCheckbox(context, node, '<input {...props}>', 'input with a spread attribute alongside checkbox/switch/dynamic indicators can carry role="switch" or type="checkbox" and cannot be statically excluded; use the Switch primitive (design-system-definition.md D5).')
    return
  }
  if (roleIsSwitch) {
    if (!type || typeIsCheckbox) {
      context.findings.push(
        makeFinding(
          RULES.CHECKBOX_AS_SWITCH,
          context.filePath,
          typeIsCheckbox ? '<input type="checkbox" role="switch">' : '<input role="switch">',
          typeIsCheckbox
            ? 'Checkbox input rendered with role="switch"; use the Switch primitive for boolean preferences (design-system-definition.md D5).'
            : 'input with role="switch" and no type attribute; an untyped input presenting as a switch is a homemade switch path, use the Switch primitive (design-system-definition.md D5).',
          ...positionOf(node),
        )
      )
      return
    }
    reportUnresolvedCheckbox(context, node, '<input type={expr} role="switch">', 'input with role="switch" and a dynamic type attribute cannot be statically excluded from the checkbox masquerade; use a literal type or the Switch primitive (design-system-definition.md D5).')
    return
  }
  if (type?.kind === 'expr' && role?.kind === 'expr') {
    reportUnresolvedCheckbox(context, node, '<input type={expr} role={expr}>', 'input with dynamic type and role attributes cannot be statically excluded from the switch masquerade; use literal attributes or the Switch primitive (design-system-definition.md D5).')
    return
  }
  if (typeIsCheckbox && role?.kind === 'expr') {
    reportUnresolvedCheckbox(context, node, '<input type="checkbox" role={expr}>', 'Checkbox input with a dynamic role attribute cannot be statically excluded from the switch masquerade; use a literal role or the Switch primitive (design-system-definition.md D5).')
    return
  }
  if (!type && role?.kind === 'expr') {
    reportUnresolvedCheckbox(context, node, '<input role={expr}>', 'input with a dynamic role attribute and no type attribute cannot be statically excluded from the homemade switch path; use a literal role or the Switch primitive (design-system-definition.md D5).')
  }
}

function reportUnresolvedCheckbox(context, node, syntax, message) {
  context.findings.push(makeFinding(RULES.CHECKBOX_AS_SWITCH, context.filePath, syntax, message, ...positionOf(node)))
}

function hasSpreadAttribute(node) {
  return node.attributes.properties.some((property) => ts.isJsxSpreadAttribute(property))
}

function jsxAttribute(node, name) {
  for (const attribute of node.attributes.properties) {
    if (!ts.isJsxAttribute(attribute) || attribute.name.text !== name) continue
    const initializer = attribute.initializer
    if (!initializer) return { kind: 'expr' }
    if (ts.isStringLiteral(initializer)) return { kind: 'literal', value: initializer.text }
    if (ts.isJsxExpression(initializer) && initializer.expression) {
      if (ts.isStringLiteral(initializer.expression)) return { kind: 'literal', value: initializer.expression.text }
      return { kind: 'expr' }
    }
    return { kind: 'expr' }
  }
  return null
}

// ---------------------------------------------------------------------------
// Import, re-export, and other module-reach declarations
// ---------------------------------------------------------------------------

function handleModuleDeclaration(context, node) {
  if (ts.isImportEqualsDeclaration(node)) {
    handleImportEqualsDeclaration(context, node)
    return
  }
  const specifier = node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier) ? node.moduleSpecifier.text : null
  if (!specifier) return
  const isImport = ts.isImportDeclaration(node)
  const parts = collectDeclarationParts(node, isImport)
  const resolved = resolveSpecifier(specifier, context.filePath)
  if (isImport) registerImportedNames(context, parts, resolved, specifier)
  if (specifier.startsWith('@radix-ui/')) {
    if (isPrimitiveWrapperFile(context)) return
    context.findings.push(
      makeFinding(RULES.RADIX_IMPORT, context.filePath, renderDeclaration(parts, specifier), `Low-level Radix import "${specifier}"; consume the registered wrapper primitive instead, or register an exact exception for an approved implementation path (design-system-definition.md D5).`, ...positionOf(node))
    )
    return
  }
  if (!resolved || !resolved.startsWith(UI_PREFIX)) return
  reportUiKitImport(context, node, parts, resolved)
}

// import X = require('...') binds a whole module namespace like a namespace
// import and is governed as one.
function handleImportEqualsDeclaration(context, node) {
  const reference = node.moduleReference
  if (!ts.isExternalModuleReference(reference) || !ts.isStringLiteral(reference.expression)) return
  const specifier = reference.expression.text
  if (REACT_MODULES.has(specifier) && !node.isTypeOnly) {
    declareInModuleScope(context, node.name.text, 'react-ns')
  }
  reportWholeModuleReach(context, node, specifier, (spec) => `import ${node.name.text} = require("${spec}")`)
}

// Dynamic import(), require(), and import-equals have no named clause, so any
// ui-kit reach through them is whole-module (namespace-equivalent) reach and is
// never the curated surface; Radix reach is absolute.
function reportWholeModuleReach(context, node, specifier, renderSyntax) {
  if (!specifier) return
  if (specifier.startsWith('@radix-ui/')) {
    if (isPrimitiveWrapperFile(context)) return
    context.findings.push(
      makeFinding(RULES.RADIX_IMPORT, context.filePath, renderSyntax(specifier), `Low-level Radix dependency "${specifier}" introduced via dynamic module reach; consume the registered wrapper primitive instead, or register an exact exception for an approved implementation path (design-system-definition.md D5).`, ...positionOf(node))
    )
    return
  }
  const resolved = resolveSpecifier(specifier, context.filePath)
  if (!resolved || !resolved.startsWith(UI_PREFIX)) return
  const resolvedSource = stripExtension(resolved)
  const detail = lookupUiEntry(context, resolvedSource)
    ? `Whole-module reach over ui-kit source "${resolvedSource}" (namespace-equivalent); import the public export from design-system/catalog.json or register an exact exception.`
    : `ui-kit module "${resolvedSource}" is not registered in design-system/catalog.json; importing it needs a registered boundary or an exception.`
  context.findings.push(makeFinding(RULES.SHADCN_LOW_LEVEL, context.filePath, renderSyntax(resolvedSource), detail, ...positionOf(node)))
}

function collectDeclarationParts(node, isImport) {
  const parts = { kind: isImport ? 'import' : 'export', typeOnly: false, defaultName: null, named: [], namespace: null, hasClause: false }
  if (isImport) {
    const clause = node.importClause
    if (!clause) return parts
    parts.hasClause = true
    parts.typeOnly = !!clause.isTypeOnly
    if (clause.name) parts.defaultName = clause.name.text
    collectNamedBindings(parts, clause.namedBindings)
    return parts
  }
  parts.typeOnly = !!node.isTypeOnly
  const clause = node.exportClause
  if (!clause) return parts
  parts.hasClause = true
  if (ts.isNamespaceExport(clause)) {
    parts.namespace = clause.name.text
    return parts
  }
  for (const element of clause.elements) {
    const exportedName = element.propertyName ? element.propertyName.text : element.name.text
    parts.named.push(namedSpecifier(exportedName, element.name.text, element.isTypeOnly, parts.typeOnly))
  }
  return parts
}

function collectNamedBindings(parts, namedBindings) {
  if (!namedBindings) return
  if (ts.isNamespaceImport(namedBindings)) {
    parts.namespace = namedBindings.name.text
    return
  }
  for (const element of namedBindings.elements) {
    const exportedName = element.propertyName ? element.propertyName.text : element.name.text
    parts.named.push(namedSpecifier(exportedName, element.name.text, element.isTypeOnly, parts.typeOnly))
  }
}

// `typeOnly` marks a type position for catalog governance (clause-level or the
// element's own inline `type` marker); `inlineTypeOnly` drives rendering only,
// so a type-only clause does not double-print the marker per element.
function namedSpecifier(exportedName, localName, elementIsTypeOnly, clauseIsTypeOnly) {
  return {
    exportedName,
    localName,
    typeOnly: clauseIsTypeOnly || !!elementIsTypeOnly,
    inlineTypeOnly: !!elementIsTypeOnly && !clauseIsTypeOnly,
  }
}

// Import declarations bind their local names in module scope (a re-export does
// not). React-family modules additionally contribute the factory globals'
// values; every import shadows the builtin globals regardless of source.
function registerImportedNames(context, parts, resolved, specifier) {
  const reactFamily = !parts.typeOnly && REACT_MODULES.has(specifier)
  const bind = (localName, value) => {
    declareInModuleScope(context, localName, value)
    context.importMap.set(localName, resolved ?? null)
  }
  if (parts.defaultName) bind(parts.defaultName, reactFamily ? 'react-ns' : null)
  if (parts.namespace) bind(parts.namespace, reactFamily ? 'react-ns' : null)
  for (const named of parts.named) {
    bind(named.localName, reactFamily && FACTORY_NAMES.has(named.exportedName) && !named.typeOnly ? 'react-factory' : null)
  }
}

function reportUiKitImport(context, node, parts, resolved) {
  const resolvedSource = stripExtension(resolved)
  const entry = lookupUiEntry(context, resolvedSource)
  if (!parts.hasClause) {
    if (parts.kind === 'import' && entry) return
    if (parts.kind === 'export' && entry && entry.allValueExportsPublic) return
  }
  const offending = offendingNames(parts, entry)
  if (offending.length === 0) return
  const detail = entry
    ? `Import of non-public ui-kit name(s) ${offending.join(', ')} from "${resolvedSource}"; use the public export from design-system/catalog.json or register an exact exception.`
    : `ui-kit module "${resolvedSource}" is not registered in design-system/catalog.json; importing it needs a registered boundary or an exception.`
  context.findings.push(makeFinding(RULES.SHADCN_LOW_LEVEL, context.filePath, renderDeclaration(parts, resolvedSource), detail, ...positionOf(node)))
}

function offendingNames(parts, entry) {
  if (parts.namespace) return [parts.namespace]
  if (!parts.hasClause) return ['*']
  const names = []
  if (parts.defaultName) names.push(parts.defaultName)
  for (const named of parts.named) {
    const allowed = named.typeOnly
      ? !!entry && (entry.publicTypes.has(named.exportedName) || entry.publicNames.has(named.exportedName))
      : !!entry && entry.publicNames.has(named.exportedName)
    if (!allowed) names.push(named.exportedName)
  }
  return names
}

// Canonical declaration text: normalized spacing, named specifiers sorted by
// exported name, double-quoted specifier, and a clause-level `type` marker when
// the whole clause is type-only (element-level markers render per element). For
// ui-kit imports the specifier is the resolved catalog source so path-form churn
// does not create fingerprints.
function renderDeclaration(parts, specifier) {
  const quoted = `"${specifier}"`
  if (parts.kind === 'export') {
    if (!parts.hasClause) return `export * from ${quoted}`
    if (parts.namespace) return `export * as ${parts.namespace} from ${quoted}`
    return `export ${renderTypeMarker(parts.typeOnly)}${renderNamed(parts.named)} from ${quoted}`
  }
  if (!parts.hasClause) return `import ${quoted}`
  if (parts.namespace) {
    return parts.defaultName
      ? `import ${parts.defaultName}, ${renderTypeMarker(parts.typeOnly)}* as ${parts.namespace} from ${quoted}`
      : `import ${renderTypeMarker(parts.typeOnly)}* as ${parts.namespace} from ${quoted}`
  }
  const named = renderNamed(parts.named)
  if (parts.defaultName) return named ? `import ${parts.defaultName}, ${named} from ${quoted}` : `import ${renderTypeMarker(parts.typeOnly)}${parts.defaultName} from ${quoted}`
  return `import ${renderTypeMarker(parts.typeOnly)}${named} from ${quoted}`
}

function renderTypeMarker(typeOnly) {
  return typeOnly ? 'type ' : ''
}

function renderNamed(named) {
  if (named.length === 0) return ''
  const rendered = [...named]
    .sort((first, second) => first.exportedName.localeCompare(second.exportedName) || first.localName.localeCompare(second.localName))
    .map((item) => `${renderTypeMarker(item.inlineTypeOnly)}${item.exportedName === item.localName ? item.exportedName : `${item.exportedName} as ${item.localName}`}`)
  return `{ ${rendered.join(', ')} }`
}

// ---------------------------------------------------------------------------
// Specifier resolution and catalog boundary
// ---------------------------------------------------------------------------

function resolveSpecifier(specifier, importerPath) {
  if (specifier.startsWith('@/')) return normalizeRepositoryPath(ALIAS_TARGET + specifier.slice(2))
  if (specifier.startsWith('./') || specifier.startsWith('../')) {
    return normalizeRepositoryPath(posix.join(posix.dirname(importerPath), specifier))
  }
  return null
}

function normalizeRepositoryPath(value) {
  const normalized = posix.normalize(value)
  return normalized.startsWith('./') ? normalized.slice(2) : normalized
}

function stripExtension(value) {
  return value.replace(EXTENSION_PATTERN, '')
}

// controls/radix-import exemption: true only when the scanned file is itself a
// design-system/catalog.json entry classified "primitive" — the registered
// sanctioned-wrapper boundary (see the doc comment at the top of this file).
// No catalog supplied (older/partial callers, or fixtures that never exercise
// a ui-kit import) is not proof of primitive-hood, so it fails closed to
// "not exempt" rather than throwing: the finding still reports, exactly as it
// did before this exemption existed. This mirrors the fail-closed posture
// used everywhere else in this scanner (parse errors, unresolved checkbox
// shapes, escaped confirm captures).
function isPrimitiveWrapperFile(context) {
  if (!context.catalog || !Array.isArray(context.catalog.entries)) return false
  context.primitiveWrapperSources ??= loadPrimitiveWrapperSources(context.catalog)
  return context.primitiveWrapperSources.has(stripExtension(context.filePath))
}

function loadPrimitiveWrapperSources(catalog) {
  return new Set(
    catalog.entries
      .filter((entry) => entry.classification === 'primitive')
      .map((entry) => stripExtension(entry.source)),
  )
}

function lookupUiEntry(context, resolvedSource) {
  context.uiBoundaries ??= loadPublicBoundaries(context.catalog)
  const direct = context.uiBoundaries.get(resolvedSource)
  if (direct) return direct
  if (resolvedSource.endsWith('/index')) return context.uiBoundaries.get(resolvedSource.slice(0, -'/index'.length))
  return undefined
}

function loadPublicBoundaries(catalog) {
  if (!catalog || !Array.isArray(catalog.entries)) throw new Error('controls.mjs: catalog context is required for ui-kit import classification')
  return new Map(
    catalog.entries.map((entry) => {
      const publicNames = new Set(entry.publicExports ?? [])
      const publicTypes = new Set(entry.publicTypes ?? [])
      const exports = entry.exports ?? []
      // export * re-exports every export (values and types); it is the curated
      // surface only when every export is public in one of the two lists.
      const allValueExportsPublic = exports.every((name) => publicNames.has(name) || publicTypes.has(name))
      return [stripExtension(entry.source), { publicNames, publicTypes, allValueExportsPublic }]
    }),
  )
}
