import path from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'

// D10 closed spacing scale. Hairlines are 1px borders, not padding/gap/margin.
const CONSTITUTIONAL_SCALE = [0, 4, 8, 16, 24, 32, 40, 48, 64]
const SCALE_EPSILON = 1e-6
const SCALE_TEXT = '0, 4, 8, 16, 24, 32, 40, 48, and 64px'

const RULE = {
  offScale: 'spacing/off-scale',
  rootDependent: 'spacing/root-dependent',
  extensionBoundary: 'spacing/extension-boundary',
  missingSafeAreaFallback: 'spacing/missing-safe-area-fallback',
  invalidVar: 'spacing/invalid-var',
  unsupported: 'spacing/unsupported',
  parseError: 'spacing/parse-error',
}

export const extensions = ['.css', '.js', '.jsx', '.ts', '.tsx']

const CLASS_BUILDERS = new Set(['cn', 'clsx', 'classNames', 'classnames', 'twMerge', 'twJoin', 'cva', 'cx', 'tv'])

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
function classBuilderOwnParameterForward(identifier) {
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
function classBuilderDeclarationName(fn) {
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
function classBuilderSingleReturnExpression(fn) {
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
function classBuilderChainForwardsParameter(expression, parameterName, seen) {
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
function transparentJoinerDeclaration(callee, sourceFile) {
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

const SPACING_PROPERTIES = new Set([
  'margin', 'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  'margin-block', 'margin-block-start', 'margin-block-end',
  'margin-inline', 'margin-inline-start', 'margin-inline-end',
  'padding', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
  'padding-block', 'padding-block-start', 'padding-block-end',
  'padding-inline', 'padding-inline-start', 'padding-inline-end',
  'gap', 'row-gap', 'column-gap', 'grid-gap', 'grid-row-gap', 'grid-column-gap',
  'scroll-margin', 'scroll-margin-top', 'scroll-margin-right', 'scroll-margin-bottom', 'scroll-margin-left',
  'scroll-margin-block', 'scroll-margin-block-start', 'scroll-margin-block-end',
  'scroll-margin-inline', 'scroll-margin-inline-start', 'scroll-margin-inline-end',
  'scroll-padding', 'scroll-padding-top', 'scroll-padding-right', 'scroll-padding-bottom', 'scroll-padding-left',
  'scroll-padding-block', 'scroll-padding-block-start', 'scroll-padding-block-end',
  'scroll-padding-inline', 'scroll-padding-inline-start', 'scroll-padding-inline-end',
  'border-spacing',
])

const TW_PREFIXES = [
  'scroll-ms', 'scroll-me', 'scroll-mx', 'scroll-my', 'scroll-mt', 'scroll-mr', 'scroll-mb', 'scroll-ml',
  'scroll-ps', 'scroll-pe', 'scroll-px', 'scroll-py', 'scroll-pt', 'scroll-pr', 'scroll-pb', 'scroll-pl',
  'scroll-m', 'scroll-p',
  'space-x', 'space-y',
  'gap-x', 'gap-y',
  'ms', 'me', 'mx', 'my', 'mt', 'mr', 'mb', 'ml',
  'ps', 'pe', 'px', 'py', 'pt', 'pr', 'pb', 'pl',
  'gap', 'm', 'p',
]

const IGNORE_KEYWORDS = new Set(['auto', 'normal', 'none'])
const CSS_WIDE_KEYWORDS = new Set(['inherit', 'initial', 'unset', 'revert', 'revert-layer'])
const SAFE_AREA_ENV_NAMES = new Set([
  'safe-area-inset-top',
  'safe-area-inset-right',
  'safe-area-inset-bottom',
  'safe-area-inset-left',
])
const ABSOLUTE_UNIT_TO_PX = {
  px: 1,
  in: 96,
  cm: 96 / 2.54,
  mm: 96 / 25.4,
  pt: 96 / 72,
  pc: 16,
  q: 96 / 2.54 / 40,
}

export function scan(input = {}) {
  const filePath = input.path
  const source = input.source
  if (typeof filePath !== 'string' || filePath.length === 0) {
    throw new Error('path must be a non-empty string')
  }
  if (typeof source !== 'string') {
    throw new Error('source must be a string')
  }
  if (source.length === 0) return []
  const ctx = createContext(filePath, input.policy, input.modules)
  const ext = path.posix.extname(filePath).toLowerCase()
  if (ext === '.css') return scanCss(source, ctx)
  if (ext === '.ts' || ext === '.tsx' || ext === '.js' || ext === '.jsx') {
    return scanScript(source, ctx, ext)
  }
  pushFinding(ctx, RULE.parseError, '<unparseable>', `Failed to parse source for spacing analysis; unsupported extension ${ext}.`, { line: 1, column: 1 })
  return ctx.findings
}

function createContext(filePath, policy, modules) {
  const resolved = policy && typeof policy === 'object' && policy.resolvedTokens && typeof policy.resolvedTokens === 'object'
    ? policy.resolvedTokens
    : {}
  const names = new Set(Array.isArray(policy?.tokenCssNames) ? policy.tokenCssNames : [])
  return {
    path: filePath,
    tokenCssNames: names,
    scale: new Set(CONSTITUTIONAL_SCALE),
    tokenPx: tokenPxFromPolicy(resolved),
    modules,
    moduleCache: new Map(),
    findings: [],
  }
}

function tokenPxFromPolicy(resolved) {
  const map = new Map()
  for (const [id, value] of Object.entries(resolved)) {
    const css = cssNameFromTokenId(id)
    const px = pixelTokenValue(value)
    if (css && px !== null) map.set(css, px)
  }
  return map
}

function cssNameFromTokenId(id) {
  const scale = /^space\.scale\.(\d+)$/.exec(id)
  if (scale) return `--space-${scale[1]}`
  return `--${String(id).replace(/[A-Z]/g, (ch) => `-${ch.toLowerCase()}`).replace(/\./g, '-')}`
}

function pixelTokenValue(value) {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'string') return null
  const dim = parseDimension(value.trim(), { unitlessIsPx: true })
  if (!dim) return null
  if (dim.number === 0) return 0
  if (dim.unit === 'rem') return null
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) return dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit]
  return null
}

function scanCss(source, ctx) {
  let root
  try {
    root = postcss.parse(source, { from: ctx.path })
  } catch (error) {
    if (error.name === 'CssSyntaxError') {
      pushFinding(
        ctx,
        RULE.parseError,
        '<unparseable>',
        `Failed to parse source for spacing analysis${error.reason ? `: ${error.reason}` : '.'}`,
        { line: error.line, column: error.column },
      )
      return ctx.findings
    }
    throw error
  }
  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    if (!SPACING_PROPERTIES.has(prop)) return
    const syntax = canonicalDecl(prop, decl.value)
    const loc = locFromPostcss(decl)
    analyzeSpacingValue(decl.value, ctx, syntax, loc, { unitlessIsPx: false })
  })
  root.walkAtRules((rule) => {
    if (rule.name.toLowerCase() !== 'apply') return
    analyzeClassList(rule.params, ctx, locFromPostcss(rule))
  })
  return ctx.findings
}

function locFromPostcss(node) {
  return { line: node.source?.start?.line, column: node.source?.start?.column }
}

function scanScript(source, ctx, ext) {
  const scriptKind = ext === '.ts' ? ts.ScriptKind.TS
    : ext === '.tsx' ? ts.ScriptKind.TSX
      : ext === '.jsx' ? ts.ScriptKind.JSX
        : ts.ScriptKind.JS
  const file = ts.createSourceFile(ctx.path, source, ts.ScriptTarget.Latest, true, scriptKind)
  const diagnostics = file.parseDiagnostics ?? []
  for (const diagnostic of diagnostics) {
    if (diagnostic.category !== ts.DiagnosticCategory.Error) continue
    const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')
    const pos = diagnostic.start ?? 0
    const { line, character } = file.getLineAndCharacterOfPosition(pos)
    pushFinding(
      ctx,
      RULE.parseError,
      '<unparseable>',
      `Failed to parse source for spacing analysis: ${message}`,
      { line: line + 1, column: character + 1 },
    )
  }
  visitNode(file, ctx, file)
  return ctx.findings
}

function visitNode(node, ctx, sourceFile) {
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
    // Dispatch by local name first (pre-existing behavior), then authenticate
    // renamed imports: a builder imported as `clsx as formatClasses` has a
    // callee name outside CLASS_BUILDERS, so its definition-site arguments
    // would otherwise never be scanned anywhere (HIGH bypass, frozen
    // 1577aee7). Authentication — not name matching — decides renamed callees.
    if (CLASS_BUILDERS.has(name) || guardClassBuilder(node.expression, ctx)) {
      for (const arg of node.arguments) visitClassBuilderArg(arg, ctx, sourceFile)
    }
  }
  ts.forEachChild(node, (child) => visitNode(child, ctx, sourceFile))
}

function visitClassLike(initializer, ctx, sourceFile) {
  if (!initializer) return
  const expr = unwrap(ts.isJsxExpression(initializer) ? initializer.expression : initializer)
  if (!expr) return
  if (ts.isCallExpression(expr) && (CLASS_BUILDERS.has(calleeName(expr.expression)) || guardClassBuilder(expr.expression, ctx))) return
  visitClassBuilderArg(expr, ctx, sourceFile)
}

function visitClassBuilderArg(node, ctx, sourceFile, useLoc = null, viaAliasResolution = false) {
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
function builderArgumentsStaticallyGoverned(call, ctx) {
  const shadow = { ...ctx, findings: [] }
  for (const argument of call.arguments) visitClassBuilderArg(argument, shadow, call.getSourceFile())
  return !shadow.findings.some((finding) => finding.ruleId === RULE.unsupported || finding.ruleId === RULE.parseError)
}

// A falsy && left operand cannot itself be a spacing class. This does not
// establish a class fragment or the input semantics of an arbitrary helper.
function wholeClassGuardContext(expression, ctx) {
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
const importGuardMemo = new WeakMap()

function guardImport(identifier) {
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
function computeGuardImport(identifier, source) {
  const matches = []
  let unsafe = false
  const visit = node => {
    if ((ts.isVariableDeclaration(node) || ts.isParameter(node) || ts.isFunctionDeclaration(node)
      || ts.isFunctionExpression(node) || ts.isClassDeclaration(node) || ts.isClassExpression(node)
      || ts.isEnumDeclaration(node) || ts.isModuleDeclaration(node) || ts.isImportEqualsDeclaration(node))
      && node.name && findNamedBinding(node.name, identifier.text)) unsafe = true
    if (ts.isWithStatement(node) || (ts.isCallExpression(node) && calleeName(node.expression) === 'eval')) unsafe = true
    ts.forEachChild(node, visit)
  }
  visit(source)
  if (unsafe || isReassignedWithin(source, identifier.text)) return null
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
function isCvaCallee(callee) {
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
function cvaFactoryDefinition(callee, ctx) {
  if (!ts.isIdentifier(callee)) return null
  const binding = findLexicalBinding(callee, callee.text)
  const imported = binding ? null : importedDeclaration(callee, ctx, new Set())
  const resolvedBinding = binding ?? imported
  if (!resolvedBinding || !resolvedBinding.initializer || !ts.isVariableDeclaration(resolvedBinding.declaration) || !isConstVariableDeclaration(resolvedBinding.declaration)) return null
  const initializer = unwrap(resolvedBinding.initializer)
  if (!initializer || !ts.isCallExpression(initializer) || !isCvaCallee(initializer.expression)) return null
  return initializer
}

function guardClassBuilder(expression, ctx) {
  const callee = unwrap(expression)
  const imported = guardImport(callee)
  if (!imported) return false
  if (imported.specifier === 'clsx' && ['default', 'clsx'].includes(imported.imported)) return true
  if (imported.specifier === 'classnames' && imported.imported === 'default') return true
  if (imported.specifier === 'tailwind-merge' && ['twMerge', 'twJoin'].includes(imported.imported)) return true
  if (imported.imported !== 'cn') return false
  const declaration = resolveModuleExport(ctx, callee.getSourceFile().fileName, imported.specifier, imported.imported, new Set())
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
function guardWrapperBindingStable(declaration) {
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

function guardMergeBuilder(expression) {
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
function authenticatedBuilderAlias(identifier, resolved, ctx) {
  if (!resolved || !ts.isCallExpression(resolved) || !guardClassBuilder(resolved.expression, ctx)) return false
  const binding = findLexicalBinding(identifier, identifier.text)
  if (binding) return isConstVariableDeclaration(binding.declaration)
  const imported = importedDeclaration(identifier, ctx, new Set())
  return !!imported && isConstVariableDeclaration(imported.declaration)
}

function isConstVariableDeclaration(declaration) {
  return ts.isVariableDeclaration(declaration) && !!(declaration.parent.flags & ts.NodeFlags.Const)
}

function literalClassArrayJoin(expr) {
  if (!ts.isPropertyAccessExpression(expr.expression) || expr.expression.name.text !== 'join') return null
  const array = unwrap(expr.expression.expression)
  const separator = expr.arguments.length === 1 ? unwrap(expr.arguments[0]) : null
  if (!array || !ts.isArrayLiteralExpression(array) || array.elements.some(ts.isSpreadElement)) return null
  if (!separator || !ts.isStringLiteral(separator) || !/^[ \t\n\r\f]+$/.test(separator.text)) return null
  return array
}

function directForwardedParameter(expr, name) {
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
function isClassLikeParameterName(name) {
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
function forwardedClassLikeBoundary(expr) {
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
function bodyDestructuredClassBoundary(identifier) {
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
function parameterMemberBoundary(node) {
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

function nearestNamedAncestorOwner(fn) {
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

function findParameterBinding(parameters, name) {
  for (const parameter of parameters) {
    const binding = findNamedBinding(parameter.name, name)
    if (binding) return binding
  }
  return null
}

function findNamedBinding(nameNode, name) {
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

function receivingSymbol(owner) {
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

function isReassignedWithin(owner, name) {
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

function isCssModuleClassReference(expr, sourceFile) {
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
// LEAD DECISION (frozen review of bb1fd56b2 found two BLOCKING false
// greens: a property write/Object.assign/delete/alias/reassignment on the
// bound variable after the dispatcher call was invisible, at ANY scope,
// because the only guard (isReassignedWithin) either never inspected member
// writes at all or was scoped to the whole SourceFile — which stops
// descending the instant it meets the first function-like node, i.e. it is
// a near no-op for code inside any component). The fix below requires,
// mirroring ts-colors.mjs::absenceValue/absenceBindingUsesSafe:
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
function resolveDispatcherMember(expr, ctx) {
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
function classifyDispatcherProperty(obj, name) {
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
function hasNullPrototypeLiteral(obj) {
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
function dispatcherBindingUsesSafe(owner, declarationName, name) {
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
function destructuredBindingPatternUsesSafe(pattern) {
  if (!ts.isObjectBindingPattern(pattern)) return false
  return pattern.elements.every((element) => ts.isBindingElement(element)
    && !element.dotDotDotToken && !element.initializer && ts.isIdentifier(element.name))
}

// Resolves `node` to the closed set of object literals it could evaluate to,
// following conditional (ternary) chains and calls into finite-dispatcher
// functions. Returns null the instant any branch cannot be classified this
// way — the caller (isProvenAbsentDispatcherMember) requires every branch to
// be accounted for, never a partial/best-effort subset.
function collectDispatchedObjectLiterals(node, ctx, seen) {
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
function collectFiniteDispatcherReturns(call, ctx, seen) {
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
function isTopLevelFiniteDispatcherFunction(declaration) {
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
function collectFiniteIfChainReturns(call, ctx) {
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

// --- Dispatcher-FUNCTION binding safety (round 3: guard parity with
// ts-colors.mjs::absenceFactory, closing the gap the independent re-review
// found — dist/design-system-baseline/cli-lanes/claude-spacing-rereview/
// review.md, finding 1). Everything above this point proves the shape of the
// switch statement; everything below proves the CALLEE NAME used to reach it
// can be trusted at all — a `function` declaration creates a mutable,
// reassignable binding (unlike a `const` arrow/function-expression), so
// `getConfig = altGetConfig` anywhere in the module made the two rounds of
// fixes before this one resolve `allAbsent: true` against the ORIGINAL,
// never-executed declaration while the REAL, reassigned function always
// returned an off-scale class. Deliberately kept separate from
// functionDeclarationFor (shared with the unrelated style-helper proof,
// pureFunctionReturns) so this stricter proof can never change that
// consumer's behavior — see the "not found" GitNexus impact check + grep
// caller sweep recorded in this round's evidence (parity.md).

function dispatcherFunctionBindingHasName(name, text) {
  if (ts.isIdentifier(name)) return name.text === text
  return (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name))
    && name.elements.some((element) => ts.isBindingElement(element) && dispatcherFunctionBindingHasName(element.name, text))
}

// Sentinel: more than one declaration for the callee's name was found in a
// single lexical scope — an AST-level duplicate the type checker would
// reject, but not something a pure-parse proof may silently resolve via a
// "first match wins" walk (a stray second declaration must never smuggle a
// different function behind a name the switch-shape proof already trusts).
const DISPATCHER_FUNCTION_AMBIGUOUS = Symbol('dispatcher function binding ambiguous')

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
function dispatcherFunctionLexicalBinding(identifier) {
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
// spread, etc. — fails the proof. This is what was missing: two prior fixes
// each guarded the dispatcher-RESULT binding (`cfg`, via
// dispatcherBindingUsesSafe above) but never the dispatcher FUNCTION's own
// name.
function dispatcherFunctionFactoryUsesSafe(declaration) {
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
// ONLY as `name(...)` anywhere it is visible (dispatcherFunctionFactoryUsesSafe
// — this is the guard the independent re-review found missing), and — for an
// imported dispatcher — the SAME two proofs re-applied to the EXPORTING
// module's own top-level declaration (the local import specifier is checked
// first via the shared pre-check above, the module is then loaded and its
// export re-checked import-side), gated on the module parsing clean, on
// exactly one matching top-level function declaration existing in it, and on
// that declaration actually carrying an `export` modifier.
function dispatcherFunctionDeclarationFor(node, ctx) {
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
const CLAUSE_NEVER_RETURNS = Symbol('clause never returns a value')

// A case/default clause's statement list, or — for the common exhaustiveness-
// guard shape (`default: { const _x: never = status; return {...} }` or
// `default: { const _x: never = status; throw new Error(...) }`) — a single
// wrapping block's statement list. Only VariableStatement and
// ExpressionStatement are allowed before the final return/throw (matches the
// `const _exhaustive: never = status; void _exhaustive` pattern); anything
// else (loops, nested control flow) is not this shape and aborts.
function clauseReturnExpression(clause) {
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
const NULL_CANDIDATE = Symbol('spacing-null-candidate')

function isNullOrUndefinedLiteral(expr) {
  if (expr.kind === ts.SyntaxKind.NullKeyword) return true
  return ts.isIdentifier(expr) && expr.text === 'undefined'
}

function memberAccessIsNullGuarded(expr) {
  return Boolean(expr.questionDotToken)
}

function resolveRecordChain(node, ctx, seen) {
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

function resolveRecordChainIdentifier(identifier, ctx, seen) {
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
function resolveRecordChainMember(expr, ctx) {
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
function chainHasDynamicElementAccess(node) {
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

function directClassBuilderCallEmbed(node, ctx) {
  const parent = node.parent
  if (!parent || !ts.isCallExpression(parent) || !parent.arguments.includes(node)) return null
  if (!CLASS_BUILDERS.has(calleeName(parent.expression)) && !guardClassBuilder(parent.expression, ctx)) return null
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
function destructuredDispatcherElement(identifier) {
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

function resolveDestructuredDispatcherMember(expr, ctx) {
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
function resolveArrayCallbackMember(node, ctx) {
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
function resolveSafeArrayLiteral(node, ctx) {
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
function arrayElementPropertyValues(arrayLiteral, key) {
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

function visitClassObject(expr, ctx, sourceFile) {
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
function hasUndefinedShadowOrDynamicScope(sourceFile) {
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

function visitStyleLike(initializer, ctx, sourceFile) {
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

function visitStyleObject(obj, ctx, sourceFile) {
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

function analyzeStyleValue(propName, valueExpr, ctx, loc) {
  if (ts.isNumericLiteral(valueExpr)) {
    const syntax = `${propName}: ${valueExpr.text}px`
    analyzeSpacingValue(`${valueExpr.text}px`, ctx, syntax, loc, { unitlessIsPx: true })
    return
  }
  if (ts.isPrefixUnaryExpression(valueExpr) && valueExpr.operator === ts.SyntaxKind.MinusToken && ts.isNumericLiteral(valueExpr.operand)) {
    const syntax = `${propName}: -${valueExpr.operand.text}px`
    analyzeSpacingValue(`-${valueExpr.operand.text}px`, ctx, syntax, loc, { unitlessIsPx: true })
    return
  }
  if (ts.isStringLiteral(valueExpr) || ts.isNoSubstitutionTemplateLiteral(valueExpr)) {
    analyzeSpacingValue(valueExpr.text, ctx, canonicalDecl(propName, valueExpr.text), loc, { unitlessIsPx: false })
    return
  }
  pushFinding(ctx, RULE.unsupported, `${propName}: {expr}`, 'Unsupported spacing expression; dynamic spacing values cannot be verified against the D10 scale.', loc)
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
function localAssignedStyleObject(identifier) {
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

function scanInlineStyle(text, ctx, loc) {
  for (const chunk of splitTopLevel(text, ';')) {
    if (!chunk) continue
    const colon = chunk.indexOf(':')
    if (colon === -1) continue
    const prop = kebab(chunk.slice(0, colon).trim())
    if (!SPACING_PROPERTIES.has(prop)) continue
    const value = chunk.slice(colon + 1)
    analyzeSpacingValue(value, ctx, canonicalDecl(prop, value), loc, { unitlessIsPx: false })
  }
}

function analyzeClassTemplate(expr, ctx, sourceFile, useLoc = null) {
  const loc = useLoc ?? locFromTs(expr, sourceFile)
  const syntax = canonicalTemplate(expr)
  analyzeClassList(completeClasses(expr.head.text, 'end'), ctx, loc)
  flagIncompleteSpacing(expr.head.text, 'end', ctx, loc, syntax)
  for (const [index, span] of expr.templateSpans.entries()) {
    const edge = index === expr.templateSpans.length - 1 ? 'start' : 'both'
    analyzeClassList(completeClasses(span.literal.text, edge === 'start' ? 'start' : 'both'), ctx, loc)
    flagIncompleteSpacing(span.literal.text, edge, ctx, loc, syntax)
  }
}

function canonicalTemplate(expr) {
  let text = expr.head.text
  for (const span of expr.templateSpans) text += `\${...}${span.literal.text}`
  return `\`${text}\``
}

function completeClasses(fragment, edge) {
  const tokens = splitClassList(fragment)
  if (tokens.length === 0) return ''
  let start = 0
  let end = tokens.length
  if ((edge === 'end' || edge === 'both') && !/\s$/.test(fragment)) end -= 1
  if ((edge === 'start' || edge === 'both') && !/^\s/.test(fragment) && start < end) start += 1
  return tokens.slice(start, end).join(' ')
}

function flagIncompleteSpacing(fragment, edge, ctx, loc, syntax) {
  const tokens = splitClassList(fragment)
  if (tokens.length === 0) return
  if ((edge === 'end' || edge === 'both') && !/\s$/.test(fragment) && looksLikeSpacingFragment(tokens[tokens.length - 1])) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; interpolated Tailwind spacing cannot be verified against the D10 scale.', loc)
    return
  }
  if ((edge === 'start' || edge === 'both') && !/^\s/.test(fragment) && looksLikeSpacingFragment(tokens[0])) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; interpolated Tailwind spacing cannot be verified against the D10 scale.', loc)
  }
}

function looksLikeSpacingFragment(token) {
  const stripped = token.replace(/^!/, '').replace(/!$/, '')
  const utility = splitVariants(stripped).utility
  const core = utility.startsWith('-') ? utility.slice(1) : utility
  if (!core) return false
  if (parseSpacingUtility(core)) return true
  return TW_PREFIXES.some((prefix) => prefix.startsWith(core) || core.startsWith(`${prefix}-`) || core === prefix)
}

function analyzeClassList(value, ctx, loc) {
  for (const cls of splitClassList(value)) analyzeUtilityClass(cls, ctx, loc)
}

function analyzeUtilityClass(cls, ctx, loc) {
  const { utility } = splitVariants(cls)
  const importantFree = utility.replace(/^!/, '').replace(/!$/, '')
  const negative = importantFree.startsWith('-')
  const core = negative ? importantFree.slice(1) : importantFree
  const parsed = parseSpacingUtility(core)
  if (!parsed) return
  const suffix = parsed.suffix
  if (!suffix || suffix === 'reverse') return
  if (suffix === 'auto') return
  if (suffix === 'full' || suffix.includes('/')) {
    pushFinding(ctx, RULE.unsupported, cls, `Unsupported spacing expression; ${cls} is percentage spacing, not a closed D10 pixel step.`, loc)
    return
  }
  if (suffix.startsWith('[') && suffix.endsWith(']')) {
    const inner = decodeArbitrary(suffix.slice(1, -1))
    analyzeSpacingValue(inner, ctx, cls, loc, { unitlessIsPx: false })
    return
  }
  if (suffix.startsWith('(') && suffix.endsWith(')')) {
    analyzeSpacingValue(`var(${suffix.slice(1, -1).trim()})`, ctx, cls, loc, { unitlessIsPx: false })
    return
  }
  if (suffix === 'px') {
    reportOffScale(ctx, cls, 1, loc, '1px')
    return
  }
  if (suffix === '0') return
  if (/^\d+(?:\.\d+)?$/.test(suffix)) {
    if (Number(suffix) === 0) return
    pushFinding(ctx, RULE.rootDependent, cls, `Root-dependent spacing; Tailwind rem utility ${cls} changes with the user root (D1/D10 require pixel-stable spacing).`, loc)
    return
  }
  pushFinding(ctx, RULE.unsupported, cls, `Unsupported spacing expression; named utility ${cls} is not a closed-scale token or static length.`, loc)
}

function decodeArbitrary(inner) {
  const decoded = inner.replaceAll('_', ' ')
  return decoded.replace(/^(?:length|spacing):/, '')
}

function parseSpacingUtility(core) {
  for (const prefix of TW_PREFIXES) {
    if (core.startsWith(`${prefix}-`)) return { prefix, suffix: core.slice(prefix.length + 1) }
  }
  return null
}

function splitClassList(value) {
  const out = []
  let current = ''
  let square = 0
  let paren = 0
  for (const char of String(value)) {
    if (char === '[') square += 1
    else if (char === ']' && square > 0) square -= 1
    else if (char === '(') paren += 1
    else if (char === ')' && paren > 0) paren -= 1
    if (/\s/.test(char) && square === 0 && paren === 0) {
      if (current) out.push(current)
      current = ''
    } else {
      current += char
    }
  }
  if (current) out.push(current)
  return out
}

function splitVariants(cls) {
  const parts = []
  let current = ''
  let square = 0
  let paren = 0
  for (const char of cls) {
    if (char === '[') square += 1
    else if (char === ']' && square > 0) square -= 1
    else if (char === '(') paren += 1
    else if (char === ')' && paren > 0) paren -= 1
    if (char === ':' && square === 0 && paren === 0) {
      parts.push(current)
      current = ''
    } else {
      current += char
    }
  }
  if (current) parts.push(current)
  const utility = parts.pop() || ''
  return { variants: parts, utility }
}

function analyzeSpacingValue(rawValue, ctx, syntax, loc, options) {
  const value = String(rawValue).trim()
  if (!value) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; empty spacing values cannot be verified against the D10 scale.', loc)
    return
  }
  for (const part of splitSpaceSeparated(value)) {
    analyzeSpacingTerm(part, ctx, syntax, loc, options)
  }
}

function analyzeSpacingTerm(term, ctx, syntax, loc, options) {
  const trimmed = term.trim()
  if (!trimmed) return
  const lower = trimmed.toLowerCase()
  if (IGNORE_KEYWORDS.has(lower)) return
  if (CSS_WIDE_KEYWORDS.has(lower)) {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; keyword ${trimmed} cannot be verified against the D10 scale.`, loc)
    return
  }
  if (options.allowUnitlessNumber && isUnitlessNumber(trimmed)) return
  const fn = parseFunctionCall(trimmed)
  if (fn) {
    classifyFunction(fn, ctx, syntax, loc, options)
    return
  }
  const dim = parseDimension(trimmed, options)
  if (dim) {
    classifyDimension(dim, ctx, syntax, loc)
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${trimmed} cannot be verified against the D10 scale.`, loc)
}

function classifyDimension(dim, ctx, syntax, loc) {
  if (dim.unit === '%' ) {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; percentage ${dim.raw} is not a closed D10 pixel step.`, loc)
    return
  }
  if (dim.unit === 'fr') {
    pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${dim.raw} is not closed-scale spacing (fr is grid-track geometry).`, loc)
    return
  }
  if (dim.unit === 'rem') {
    if (dim.number === 0) return
    pushFinding(ctx, RULE.rootDependent, syntax, `Root-dependent spacing; rem spacing ${dim.raw} changes with the user root (D1/D10 require pixel-stable spacing).`, loc)
    return
  }
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) {
    const px = dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit]
    if (isOnScale(Math.abs(px), ctx.scale)) return
    reportOffScale(ctx, syntax, px, loc, dim.raw)
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; unit ${dim.unit} cannot be normalized to the D10 pixel scale.`, loc)
}

function classifyFunction(fn, ctx, syntax, loc, options) {
  if (fn.name === 'var') {
    analyzeVar(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'calc') {
    analyzeCalc(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'env') {
    analyzeEnvironmentSpacing(fn.args, ctx, syntax, loc)
    return
  }
  if (fn.name === 'min' || fn.name === 'max' || fn.name === 'clamp') {
    for (const arg of splitCommaSeparated(fn.args)) {
      analyzeSpacingTerm(arg, ctx, syntax, loc, { ...options, allowUnitlessNumber: false })
    }
    return
  }
  pushFinding(ctx, RULE.unsupported, syntax, `Unsupported spacing expression; ${fn.name}() cannot be verified against the D10 scale.`, loc)
}

function analyzeEnvironmentSpacing(args, ctx, syntax, loc) {
  const parts = splitCommaSeparated(args)
  const name = parts[0]?.trim().toLowerCase()
  if (SAFE_AREA_ENV_NAMES.has(name) && parts.length === 1) {
    pushFinding(ctx, RULE.missingSafeAreaFallback, syntax, `Safe-area spacing ${name} is missing the required explicit closed-scale fallback.`, loc)
    return
  }
  if (!SAFE_AREA_ENV_NAMES.has(name) || parts.length !== 2) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; safe-area env() spacing requires one explicit closed-scale fallback.', loc)
    return
  }
  const before = ctx.findings.length
  analyzeSpacingTerm(parts[1], ctx, syntax, loc, { unitlessIsPx: false })
  if (ctx.findings.length === before) {
    pushFinding(ctx, RULE.extensionBoundary, syntax, `Spacing extension boundary; runtime ${name} uses a validated closed-scale fallback and requires exact central review.`, loc)
  }
}

function analyzeVar(args, ctx, syntax, loc) {
  const parts = splitCommaSeparated(args)
  const name = parts[0] ? parts[0].trim() : ''
  if (!name.startsWith('--')) {
    pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; var() is missing a custom property name.', loc)
    return
  }
  if (!ctx.tokenCssNames.has(name)) {
    pushFinding(ctx, RULE.invalidVar, syntax, `Unknown spacing custom property ${name}; it is not a registered token.`, loc)
    return
  }
  if (!isSpacingTokenName(name)) {
    pushFinding(ctx, RULE.unsupported, syntax, `Custom property ${name} is registered but is not a D10 spacing token.`, loc)
  }
  if (parts[1]) analyzeSpacingTerm(parts[1], ctx, syntax, loc, { unitlessIsPx: false })
}

function analyzeCalc(args, ctx, syntax, loc) {
  const result = evaluateCalcExpr(args, ctx)
  if (result.kind === 'invalid-var') {
    pushFinding(ctx, RULE.invalidVar, syntax, `Unknown spacing custom property ${result.name}; it is not a registered token.`, loc)
    return
  }
  if (result.kind === 'px') {
    if (isOnScale(Math.abs(result.value), ctx.scale)) return
    reportOffScale(ctx, syntax, result.value, loc, formatPx(result.value))
    return
  }
  if (result.kind === 'number' && result.value === 0) return
  pushFinding(ctx, RULE.unsupported, syntax, 'Unsupported spacing expression; calc() result is not a closed D10 pixel length.', loc)
}

function evaluateCalcExpr(input, ctx) {
  const parser = { s: String(input), i: 0, ctx }
  const result = parseCalcAdd(parser)
  skipCalcWs(parser)
  if (result.kind === 'invalid-var' || result.kind === 'unsupported') return result
  if (parser.i !== parser.s.length) return { kind: 'unsupported' }
  return result
}

function skipCalcWs(parser) {
  while (parser.i < parser.s.length && /\s/.test(parser.s[parser.i])) parser.i += 1
}

function parseCalcAdd(parser) {
  let left = parseCalcMul(parser)
  if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  for (;;) {
    skipCalcWs(parser)
    const op = parser.s[parser.i]
    if (op !== '+' && op !== '-') break
    parser.i += 1
    const right = parseCalcMul(parser)
    if (right.kind === 'unsupported' || right.kind === 'invalid-var') return right
    left = combineCalc(left, right, op)
    if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  }
  return left
}

function parseCalcMul(parser) {
  let left = parseCalcUnary(parser)
  if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  for (;;) {
    skipCalcWs(parser)
    const op = parser.s[parser.i]
    if (op !== '*' && op !== '/') break
    parser.i += 1
    const right = parseCalcUnary(parser)
    if (right.kind === 'unsupported' || right.kind === 'invalid-var') return right
    left = combineCalc(left, right, op)
    if (left.kind === 'unsupported' || left.kind === 'invalid-var') return left
  }
  return left
}

function parseCalcUnary(parser) {
  skipCalcWs(parser)
  if (parser.s[parser.i] === '+' || parser.s[parser.i] === '-') {
    const op = parser.s[parser.i]
    parser.i += 1
    const inner = parseCalcUnary(parser)
    if (op === '-') return negateCalc(inner)
    return inner
  }
  return parseCalcPrimary(parser)
}

function parseCalcPrimary(parser) {
  skipCalcWs(parser)
  if (parser.s[parser.i] === '(') {
    parser.i += 1
    const inner = parseCalcAdd(parser)
    skipCalcWs(parser)
    if (parser.s[parser.i] !== ')') return { kind: 'unsupported' }
    parser.i += 1
    return inner
  }
  if (parser.i < parser.s.length && /[A-Za-z_]/.test(parser.s[parser.i])) {
    const nameStart = parser.i
    while (parser.i < parser.s.length && /[\w-]/.test(parser.s[parser.i])) parser.i += 1
    const name = parser.s.slice(nameStart, parser.i)
    skipCalcWs(parser)
    const args = readBalancedArgs(parser)
    if (args === null) return { kind: 'unsupported' }
    return evalNamedCalcFn(name.toLowerCase(), args, parser.ctx)
  }
  const dim = readCalcDimension(parser)
  if (!dim) return { kind: 'unsupported' }
  if (dim.number === 0) return { kind: 'px', value: 0 }
  if (!dim.unit) return { kind: 'number', value: dim.number }
  if (Object.hasOwn(ABSOLUTE_UNIT_TO_PX, dim.unit)) {
    return { kind: 'px', value: dim.number * ABSOLUTE_UNIT_TO_PX[dim.unit] }
  }
  return { kind: 'unsupported' }
}

function readBalancedArgs(parser) {
  if (parser.s[parser.i] !== '(') return null
  let depth = 0
  const start = parser.i
  for (; parser.i < parser.s.length; parser.i += 1) {
    const char = parser.s[parser.i]
    if (char === '(') depth += 1
    else if (char === ')') {
      depth -= 1
      if (depth === 0) {
        const inner = parser.s.slice(start + 1, parser.i)
        parser.i += 1
        return inner
      }
    }
  }
  return null
}

function readCalcDimension(parser) {
  skipCalcWs(parser)
  const start = parser.i
  if (parser.i >= parser.s.length || !/[\d.]/.test(parser.s[parser.i])) return null
  while (parser.i < parser.s.length && /[\d.]/.test(parser.s[parser.i])) parser.i += 1
  const number = Number(parser.s.slice(start, parser.i))
  if (!Number.isFinite(number)) {
    parser.i = start
    return null
  }
  const unitStart = parser.i
  while (parser.i < parser.s.length && /[A-Za-z%]/.test(parser.s[parser.i])) parser.i += 1
  return { number, unit: parser.s.slice(unitStart, parser.i).toLowerCase() }
}

function evalNamedCalcFn(name, args, ctx) {
  if (name === 'calc') return evaluateCalcExpr(args, ctx)
  if (name !== 'var') return { kind: 'unsupported' }
  const parts = splitCommaSeparated(args)
  const token = parts[0] ? parts[0].trim() : ''
  if (!token.startsWith('--')) return { kind: 'unsupported' }
  if (!ctx.tokenCssNames.has(token)) return { kind: 'invalid-var', name: token }
  if (!isSpacingTokenName(token)) return { kind: 'unsupported' }
  if (!ctx.tokenPx.has(token)) return { kind: 'unsupported' }
  return { kind: 'px', value: ctx.tokenPx.get(token) }
}

function combineCalc(left, right, op) {
  if (op === '+' || op === '-') {
    const sign = op === '-' ? -1 : 1
    if (left.kind === 'px' && right.kind === 'px') return { kind: 'px', value: left.value + sign * right.value }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value + sign * right.value }
    return { kind: 'unsupported' }
  }
  if (op === '*') {
    if (left.kind === 'px' && right.kind === 'number') return { kind: 'px', value: left.value * right.value }
    if (left.kind === 'number' && right.kind === 'px') return { kind: 'px', value: left.value * right.value }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value * right.value }
    return { kind: 'unsupported' }
  }
  if (op === '/') {
    if (!right.value) return { kind: 'unsupported' }
    if (left.kind === 'px' && right.kind === 'number') return { kind: 'px', value: left.value / right.value }
    if (left.kind === 'px' && right.kind === 'px') return { kind: 'number', value: left.value / right.value }
    if (left.kind === 'number' && right.kind === 'number') return { kind: 'number', value: left.value / right.value }
    return { kind: 'unsupported' }
  }
  return { kind: 'unsupported' }
}

function negateCalc(value) {
  if (value.kind === 'px' || value.kind === 'number') return { kind: value.kind, value: -value.value }
  return value
}

function reportOffScale(ctx, syntax, px, loc) {
  const abs = Math.abs(px)
  pushFinding(
    ctx,
    RULE.offScale,
    syntax,
    `Off-scale spacing ${formatPx(abs)}; D10 allows ${SCALE_TEXT} (hairlines are 1px borders only).`,
    loc,
  )
}

function parseFunctionCall(term) {
  const trimmed = term.trim()
  const open = trimmed.indexOf('(')
  if (open <= 0 || !trimmed.endsWith(')')) return null
  const name = trimmed.slice(0, open).trim()
  if (!/^[A-Za-z_][\w-]*$/.test(name)) return null
  const inner = trimmed.slice(open + 1, -1)
  if (!isBalanced(trimmed.slice(open))) return null
  return { name: name.toLowerCase(), args: inner }
}

function parseDimension(term, options) {
  const trimmed = term.trim()
  if (trimmed === '0') return { number: 0, unit: 'px', raw: '0' }
  const match = /^([+-]?(?:\d*\.\d+|\d+))([A-Za-z%]+)?$/.exec(trimmed)
  if (!match) return null
  const number = Number(match[1])
  if (!Number.isFinite(number)) return null
  let unit = (match[2] || '').toLowerCase()
  if (!unit) {
    if (number === 0) unit = 'px'
    else if (options.unitlessIsPx) unit = 'px'
    else return null
  }
  return { number, unit, raw: trimmed }
}

function isOnScale(px, scale) {
  for (const step of scale) {
    if (Math.abs(px - step) < SCALE_EPSILON) return true
  }
  return false
}

function isUnitlessNumber(term) {
  return /^[+-]?(?:\d*\.\d+|\d+)$/.test(term.trim())
}

function isSpacingTokenName(name) {
  return name.startsWith('--space-') || (name.startsWith('--density-') && name.includes('gap'))
}

function isBalanced(text) {
  let depth = 0
  for (const char of text) {
    if (char === '(') depth += 1
    if (char === ')') {
      depth -= 1
      if (depth < 0) return false
    }
  }
  return depth === 0
}

function splitSpaceSeparated(value) {
  return splitTopLevel(value, ' ').filter(Boolean)
}

function splitCommaSeparated(value) {
  return splitTopLevel(value, ',').filter(Boolean)
}

function splitTopLevel(value, separator) {
  const out = []
  let current = ''
  let depth = 0
  for (const char of String(value)) {
    if (char === '(') depth += 1
    else if (char === ')') depth = Math.max(0, depth - 1)
    if (char === separator && depth === 0) {
      const piece = current.trim()
      if (piece) out.push(piece)
      current = ''
    } else {
      current += char
    }
  }
  const piece = current.trim()
  if (piece) out.push(piece)
  return out
}

function canonicalDecl(prop, value) {
  return `${prop}: ${normalizeCssValue(value)}`
}

function normalizeCssValue(value) {
  return String(value)
    .trim()
    .replace(/\s+/g, ' ')
    .replace(/\s*!\s*important$/i, '')
    .replace(/\(\s+/g, '(')
    .replace(/\s+\)/g, ')')
    .replace(/\s+,/g, ',')
    .replace(/,\s+/g, ', ')
    .trim()
}

function kebab(name) {
  return name.replace(/^[A-Z]/, (char) => char.toLowerCase()).replace(/[A-Z]/g, (char) => `-${char.toLowerCase()}`)
}

function isConcatOrLogic(kind) {
  return (
    kind === ts.SyntaxKind.PlusToken
    || kind === ts.SyntaxKind.AmpersandAmpersandToken
    || kind === ts.SyntaxKind.BarBarToken
    || kind === ts.SyntaxKind.QuestionQuestionToken
  )
}

const UNRESOLVED_EXPR = Symbol('unresolved spacing expression')

// record-binding escape/mutation guard for resolveExpr's identifier branch.
//
// LEAD DECISION (round 4, cross-scanner-false-green.test.mjs, committed
// ee0551dc3): the prior guard here (`isReassignedWithin(node.getSourceFile(),
// name)`) had two independent holes proven by that shared suite:
//   1. It only matched a bare `name = ...` reassignment (`ts.isIdentifier
//      (node.left)`) -- a property write (`SIZES.small = 'p-[7px]'`),
//      Object.assign/Reflect.set/Object.defineProperty onto the binding, or
//      any other escape of the binding itself was completely invisible.
//   2. Its walk (`isReassignedWithin`) stops descending the INSTANT it meets
//      any function-like node that is not the scope it started from -- for
//      a module-scope `const SIZES = {...}`, the scope IS the SourceFile,
//      so a mutation written inside ANY function in the file (the common
//      case: a component body) was never visited at all.
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
function isAssignmentOperatorKind(kind) {
  return kind >= ts.SyntaxKind.FirstAssignment && kind <= ts.SyntaxKind.LastAssignment
}

function isIncDecOperator(operator) {
  return operator === ts.SyntaxKind.PlusPlusToken || operator === ts.SyntaxKind.MinusMinusToken
}

function isPrimitiveLeafNode(node) {
  const value = unwrap(node)
  if (!value) return false
  if (ts.isStringLiteralLike(value) || ts.isNumericLiteral(value)) return true
  if (ts.isNoSubstitutionTemplateLiteral(value) || ts.isTemplateExpression(value)) return true
  return [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)
}

// Rebuilds the static string/numeric key path an occurrence's climbed
// property/element-access chain represents (`REC` -> `REC.a` -> `REC.a.b`
// yields `['a', 'b']`); null on any dynamic/computed segment (fail closed --
// callers treat null exactly like an unresolvable, non-primitive value).
function chainPropertyPath(occurrence, climbed) {
  const path = []
  let current = occurrence
  while (current !== climbed) {
    const parent = current.parent
    if (ts.isPropertyAccessExpression(parent) && parent.expression === current) {
      path.push(parent.name.text)
      current = parent
      continue
    }
    if (ts.isElementAccessExpression(parent) && parent.expression === current) {
      const keyExpr = unwrap(parent.argumentExpression)
      const key = keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr) || ts.isNumericLiteral(keyExpr))
        ? keyExpr.text
        : null
      if (key === null) return null
      path.push(key)
      current = parent
      continue
    }
    if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isNonNullExpression(parent)) {
      current = parent
      continue
    }
    return null
  }
  return path
}

function structuralValueAtPath(root, path) {
  let current = unwrap(root)
  for (const key of path) {
    if (!current) return null
    if (ts.isObjectLiteralExpression(current)) {
      const init = propertyInit(current, key)
      current = init ? unwrap(init) : null
      continue
    }
    if (ts.isArrayLiteralExpression(current)) {
      const index = Number(key)
      if (!Number.isSafeInteger(index) || index < 0 || index >= current.elements.length) return null
      const element = current.elements[index]
      current = element && !ts.isOmittedExpression(element) ? unwrap(element) : null
      continue
    }
    return null
  }
  return current
}

// Companion to chainPropertyPath/structuralValueAtPath for
// recordBindingEscapes's container-escape check, closing a gap the dynamic-
// key fan-out capability (resolveRecordChain, above) exposed: the MOST
// COMMON real shape for a dynamic-key record read is `cn(RECORD[dynamicKey])`
// -- passed straight into a class-builder call as a bare argument, which
// recordBindingEscapes's embeds check requires to structurally prove a
// PRIMITIVE at one exact path. chainPropertyPath cannot express a dynamic
// key at all (it has no static string to push), so every such embed
// unconditionally failed the primitive proof and blocked resolution, even
// though the record itself is exactly as safe as any other primitive-only
// record. This does not touch chainPropertyPath/structuralValueAtPath's own
// contract or loosen the primitive-leaf requirement anywhere -- it is a
// SEPARATE, narrower proof tried only as a fallback: every segment up to the
// dynamic hop must still be a fully static path, and that hop must be a
// dynamic ElementAccessExpression -- when so, the embedded value is SOME
// property of the object literal the static prefix resolves to.
//
// Item 3b widening (TaskDetailPanel.tsx's `PRIORITY_CONFIG[p]?.color`): the
// dynamic hop need not be the FINAL one any more -- a chain of further
// STATIC hops (`.color`) between it and `climbed` is allowed, collected as
// `tailPath`. When `tailPath` is empty (the original, still-supported shape)
// this requires EVERY one of the dynamically-indexed object's own properties
// to independently be a primitive leaf, exactly as before. When `tailPath` is
// non-empty, each of THOSE properties is itself an object one hop closer to
// the actual embedded value -- so instead the proof requires every branch's
// value AT `tailPath` (not the branch object itself) to be a primitive leaf;
// this is narrower where `tailPath` is empty and identical there, never a
// looser check on the SAME shape. A second dynamic hop inside the tail
// (chainPropertyPath returns null) or any non-static tail shape still fails
// closed, matching every other proof in this file.
function dynamicTailEmbedIsSafe(occurrence, climbed, rootInitializer) {
  const path = []
  let current = occurrence
  while (current !== climbed) {
    const parent = current.parent
    if (ts.isPropertyAccessExpression(parent) && parent.expression === current) {
      path.push(parent.name.text)
      current = parent
      continue
    }
    if (ts.isElementAccessExpression(parent) && parent.expression === current) {
      const keyExpr = unwrap(parent.argumentExpression)
      const key = keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr) || ts.isNumericLiteral(keyExpr))
        ? keyExpr.text
        : null
      if (key === null) {
        const tailPath = chainPropertyPath(parent, climbed)
        if (tailPath === null) return false // a second dynamic hop, or a non-static tail shape, is not provable this way
        const target = structuralValueAtPath(rootInitializer, path)
        if (!target || !ts.isObjectLiteralExpression(target)) return false
        return target.properties.every((prop) => {
          let branchValue
          if (ts.isShorthandPropertyAssignment(prop)) branchValue = prop.name
          else if (ts.isPropertyAssignment(prop) && !ts.isComputedPropertyName(prop.name)) branchValue = prop.initializer
          else return false
          if (tailPath.length === 0) return isPrimitiveLeafNode(branchValue)
          const narrowed = structuralValueAtPath(branchValue, tailPath)
          return narrowed ? isPrimitiveLeafNode(narrowed) : false
        })
      }
      path.push(key)
      current = parent
      continue
    }
    if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isNonNullExpression(parent)) {
      current = parent
      continue
    }
    return false
  }
  return false // reached climbed via a fully-static path -- chainPropertyPath already covers that case; nothing new to prove here
}

// Nearest enclosing Block/SourceFile a binding's mutations could occur in --
// same shape as the declaration-scope walks already used throughout this
// file (e.g. wholeValueWritesAbsent's sibling in ts-colors.mjs). A parameter
// has no enclosing block of its own; its owning function's body is the
// correct scope.
function recordDeclarationScope(declaration) {
  if (ts.isParameter(declaration)) {
    const owner = declaration.parent
    return owner && ts.isFunctionLike(owner) && owner.body ? owner.body : null
  }
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  return scope
}

// A binding only counts as "record-shaped" -- and so only gets the stricter
// recordBindingEscapes treatment -- when it structurally resolves to a
// plain object or array literal, through any number of
// Object.freeze/seal/preventExtensions wraps (mirroring resolveExpr's own
// unwrap of that call shape below). Anything else (a CallExpression alias
// such as `const TRIGGER = cn(...)`, a template, a conditional, ...) is
// deliberately left to the narrower, pre-existing isReassignedWithin gate.
function recordLiteralRoot(initializer) {
  let current = unwrap(initializer)
  while (
    current
    && ts.isCallExpression(current)
    && ts.isPropertyAccessExpression(current.expression)
    && ts.isIdentifier(current.expression.expression)
    && current.expression.expression.text === 'Object'
    && ['freeze', 'seal', 'preventExtensions'].includes(current.expression.name.text)
    && current.arguments[0]
  ) current = unwrap(current.arguments[0])
  return current && (ts.isObjectLiteralExpression(current) || ts.isArrayLiteralExpression(current)) ? current : null
}

const RECORD_ESCAPE_CACHE = new WeakMap()

// Returns true when `name` escapes or is mutated anywhere in `scope` --
// false only when every occurrence is a provably safe read. `rootInitializer`
// is the object/array literal (or whatever resolveExpr already resolved the
// binding to) used to classify a container-escape's embedded value as a
// primitive leaf or not.
function recordBindingEscapes(scope, name, rootInitializer, cacheKey) {
  if (!scope) return true
  if (cacheKey) {
    let byScope = RECORD_ESCAPE_CACHE.get(cacheKey)
    if (byScope?.has(scope)) return byScope.get(scope)
  }
  let unsafe = false
  const visit = (node) => {
    if (unsafe) return
    if (ts.isIdentifier(node) && node.text === name) {
      let climbed = node
      while (climbed.parent && (
        (ts.isPropertyAccessExpression(climbed.parent) && climbed.parent.expression === climbed)
        || (ts.isElementAccessExpression(climbed.parent) && climbed.parent.expression === climbed)
        || ts.isParenthesizedExpression(climbed.parent)
        || ts.isAsExpression(climbed.parent)
        || ts.isNonNullExpression(climbed.parent)
      )) climbed = climbed.parent
      const use = climbed.parent
      if (!use) { unsafe = true; return }
      if (ts.isBinaryExpression(use) && use.left === climbed && isAssignmentOperatorKind(use.operatorToken.kind)) { unsafe = true; return }
      if ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === climbed) { unsafe = true; return }
      if (ts.isDeleteExpression(use)) { unsafe = true; return }
      if ((ts.isPrefixUnaryExpression(use) || ts.isPostfixUnaryExpression(use)) && isIncDecOperator(use.operator)) { unsafe = true; return }
      const embeds = ts.isArrayLiteralExpression(use)
        || (ts.isPropertyAssignment(use) && use.initializer === climbed)
        || ts.isShorthandPropertyAssignment(use)
        || ts.isSpreadElement(use)
        || ts.isSpreadAssignment(use)
        || (ts.isCallExpression(use) && use.arguments.includes(climbed))
      if (embeds) {
        const path = chainPropertyPath(node, climbed)
        const value = path && rootInitializer ? structuralValueAtPath(rootInitializer, path) : null
        if ((!value || !isPrimitiveLeafNode(value)) && !dynamicTailEmbedIsSafe(node, climbed, rootInitializer)) { unsafe = true; return }
      }
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  if (cacheKey) {
    let byScope = RECORD_ESCAPE_CACHE.get(cacheKey)
    if (!byScope) { byScope = new Map(); RECORD_ESCAPE_CACHE.set(cacheKey, byScope) }
    byScope.set(scope, unsafe)
  }
  return unsafe
}

// SP-FALSE-GREEN fix (lead review of SP-RECOVER): ported from
// ts-colors.mjs::knownClassExportUsesSafe and its dynamic-import helper
// (dynamicImportProvenNotOrigin, now ported below in full -- see its own
// comment ahead of computeRecordExportedUsesSafe for the template-literal-
// head proof). recordBindingEscapes above only ever walks ONE scope: the
// declaring module (for a same-file read) or the
// reading module (for an already-imported read). Neither walk looks at
// OTHER files in the `modules` context that import the SAME exported record
// and mutate it there -- an `export let`/`export const` record is reachable
// from any module via a named import, a namespace import, or a re-export.
// This is the missing check: for an EXPORTED record declaration, walk every
// JS/TS module in `ctx.modules`, and for each one that imports from the
// declaring module, either recurse into recordBindingEscapes for a named
// import's own local binding (reusing the exact same escape/mutation proof
// already used for the direct-importer case), or fail closed outright for a
// namespace import, a re-export, or a dynamic import()/require() that could
// resolve to the origin -- none of those can be traced by this scanner. A
// declaration with no `export` modifier returns true immediately (nothing
// to check); a missing `modules` context on an exported declaration returns
// false (fail closed -- matches ts-colors' own ordering).
const EXPORTED_RECORD_SAFE_CACHE = new WeakMap()

function isJsModulePath(modulePath) {
  const ext = path.posix.extname(modulePath).toLowerCase()
  return ext === '.ts' || ext === '.tsx' || ext === '.js' || ext === '.jsx'
}

function recordExportedUsesSafe(declaration, ctx, rootInitializer) {
  if (!ts.isVariableDeclaration(declaration) || !ts.isIdentifier(declaration.name)) return true
  const statement = declaration.parent?.parent
  if (!statement || !ts.isVariableStatement(statement) || !statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) return true
  if (EXPORTED_RECORD_SAFE_CACHE.has(declaration)) return EXPORTED_RECORD_SAFE_CACHE.get(declaration)
  const result = computeRecordExportedUsesSafe(declaration, ctx, rootInitializer)
  EXPORTED_RECORD_SAFE_CACHE.set(declaration, result)
  return result
}

// CAP-D2 port (ts-colors.mjs::dynamicImportProvenNotOrigin) -- a template-
// literal dynamic import()/require() argument's HEAD is always the literal
// prefix of whatever string it evaluates to at runtime (TemplateExpression
// semantics: head + eval(span1) + text + ...) -- a substitution can only
// APPEND characters after it, never rewrite or erase what is already there.
// governedModulePath only ever resolves a specifier that itself starts with
// '@/' or a relative '.' form. If the head cannot possibly grow into one of
// those two forms (it already diverges from both -- e.g. an npm package name
// interpolation like `@codemirror/legacy-modes/mode/${m}`), no runtime value
// of the template can EVER be a governedModulePath-resolvable specifier at
// all -- proven impossible, not guessed -- so it can never target `origin`.
// When the head IS shaped like a local specifier, it must additionally share
// `origin`'s directory (and, lacking a trailing '/', `origin`'s basename
// must share the head's own final segment as a prefix), or it is still a
// provably different target. Any other argument shape (bare identifier,
// call, spread, or an ambiguous short head that could still complete into
// '@/'/'.' once interpolation appends more text) stays exactly as
// conservative as before: not excluded.
function dynamicImportProvenNotOrigin(modulePath, argument, origin) {
  if (!ts.isTemplateExpression(argument)) return false
  const head = argument.head.text
  if (head.startsWith('@/') || head.startsWith('./') || head.startsWith('../')) {
    const prefix = head.startsWith('@/') ? `src/${head.slice(2)}` : path.posix.normalize(path.posix.join(path.posix.dirname(modulePath), head))
    const prefixDir = prefix.endsWith('/') ? prefix.slice(0, -1) : path.posix.dirname(prefix)
    const originDir = path.posix.dirname(origin)
    if (originDir !== prefixDir) return true
    if (prefix.endsWith('/')) return false
    return !path.posix.basename(origin).startsWith(path.posix.basename(prefix))
  }
  if ('@/'.startsWith(head) || './'.startsWith(head) || '../'.startsWith(head)) return false
  return true
}

// CAP-D1 fix (spacing-specific -- ts-colors.mjs has not itself narrowed this
// case; its own knownClassExportUsesSafe still fails closed unconditionally
// on ANY parse-error JS/TS module in the modules context). A JS/TS module
// that fails to parse (moduleRecord's `valid` is false) may still contain
// literal import/export/dynamic-import syntax the TS parser discarded during
// error recovery: verified empirically that a broken JSX body preceding a
// later `import` statement can make that import vanish from BOTH the
// top-level statement list AND a full recursive `ts.forEachChild` walk --
// the parser drops the tokens instead of attaching them to any recoverable
// node. Trusting the parsed AST of an already-broken file to prove it is
// IRRELEVANT to `origin` would therefore be unsound. Unconditionally
// poisoning on ANY parse error anywhere in the tree is exactly the bug this
// closes: with the lead applying live codemods elsewhere in src/, a single
// transiently-broken, wholly unrelated file was enough to make every
// exported record's cross-module proof fail everywhere (STATUS_BADGE.inbox,
// MODE_CHIP_CLASS[m]). Scanning the RAW TEXT instead of the AST is safe in
// the direction that matters: it can only ever find MORE candidate
// specifiers than the file actually contains (a stray quoted string that
// happens to look like a path), never fewer -- so it stays fail-closed. A
// module is judged irrelevant only when NEITHER a plain quoted/no-
// -substitution-backtick string ANYWHERE in its text governs to `origin`,
// NOR an import()/require() call exists whose argument isn't itself a single
// plain literal of that same shape immediately following the open paren
// (anything else -- an identifier, a template with `${`, a call, nothing at
// all -- stays ambiguous and fails closed, deliberately NOT attempting the
// template-head proof above on already-broken text).
const PLAIN_STRING_LITERAL_SOURCE = String.raw`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|\`(?:[^\`\\$]|\\.|\$(?!\{))*\``
const PLAIN_STRING_LITERAL_RE_G = new RegExp(PLAIN_STRING_LITERAL_SOURCE, 'g')
const DYNAMIC_IMPORT_CALL_RE = /\b(?:import|require)\s*\(\s*/g
const PLAIN_STRING_LITERAL_ARG_RE = new RegExp(`^(?:${PLAIN_STRING_LITERAL_SOURCE})\\s*[,)]`)

function unparseableModuleMightTargetOrigin(modulePath, source, origin, modules) {
  PLAIN_STRING_LITERAL_RE_G.lastIndex = 0
  let match
  while ((match = PLAIN_STRING_LITERAL_RE_G.exec(source))) {
    if (governedModulePath(modulePath, match[0].slice(1, -1), modules) === origin) return true
  }
  DYNAMIC_IMPORT_CALL_RE.lastIndex = 0
  while ((match = DYNAMIC_IMPORT_CALL_RE.exec(source))) {
    const rest = source.slice(match.index + match[0].length)
    if (!PLAIN_STRING_LITERAL_ARG_RE.test(rest)) return true // ambiguous argument shape -- fail closed
  }
  return false
}

function computeRecordExportedUsesSafe(declaration, ctx, rootInitializer) {
  if (!ctx.modules) return false
  const origin = declaration.getSourceFile().fileName
  const exportedName = declaration.name.text
  for (const modulePath of Object.keys(ctx.modules)) {
    if (!isJsModulePath(modulePath)) continue
    const record = moduleRecord(ctx, modulePath)
    if (!record) return false
    if (!record.valid) {
      if (unparseableModuleMightTargetOrigin(modulePath, ctx.modules[modulePath], origin, ctx.modules)) return false
      continue
    }
    for (const item of record.sourceFile.statements) {
      if ((!ts.isImportDeclaration(item) && !ts.isExportDeclaration(item)) || !item.moduleSpecifier || !ts.isStringLiteral(item.moduleSpecifier)) continue
      if (governedModulePath(modulePath, item.moduleSpecifier.text, ctx.modules) !== origin) continue
      if (ts.isExportDeclaration(item)) {
        // A re-export (`export { M } from './P'`/`export * from './P'`)
        // hands the record to WHATEVER re-imports it from here, arbitrarily
        // far downstream -- untraceable, so it fails closed unconditionally
        // (a type-only re-export carries no runtime value to mutate).
        if (!item.isTypeOnly) return false
        continue
      }
      const clause = item.importClause
      if (!clause || clause.isTypeOnly) continue
      const bindings = clause.namedBindings
      // A default import binding on this statement, or a namespace import
      // (`* as NS`) instead of named imports, cannot be traced by name at
      // all -- fail closed exactly like ts-colors' own check.
      if (clause.name || (bindings && !ts.isNamedImports(bindings))) return false
      if (bindings) for (const specifier of bindings.elements) {
        if (specifier.isTypeOnly || (specifier.propertyName?.text ?? specifier.name.text) !== exportedName) continue
        if (recordBindingEscapes(record.sourceFile, specifier.name.text, rootInitializer, specifier)) return false
      }
    }
    let dynamic = false
    const visitDynamic = (node) => {
      if (dynamic) return
      if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword
        || (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
        const argument = node.arguments[0]
        const literal = argument ? unwrap(argument) : null
        if (!argument) dynamic = true
        else if (literal && (ts.isStringLiteral(literal) || ts.isNoSubstitutionTemplateLiteral(literal))) {
          if (governedModulePath(modulePath, literal.text, ctx.modules) === origin) dynamic = true
        } else if (!literal || !dynamicImportProvenNotOrigin(modulePath, literal, origin)) dynamic = true
        // any other non-literal dynamic import()/require() argument stays conservatively treated as possibly-origin
      }
      if (!dynamic) ts.forEachChild(node, visitDynamic)
    }
    visitDynamic(record.sourceFile)
    if (dynamic) return false
  }
  return true
}

function resolveExpr(expr, ctx, seen = new Set()) {
  const node = unwrap(expr)
  if (!node) return null
  if (ts.isIdentifier(node)) {
    const binding = findLexicalBinding(node, node.text)
    const imported = binding ? null : importedDeclaration(node, ctx, seen)
    const resolvedBinding = binding ?? imported
    if (!resolvedBinding || !resolvedBinding.initializer) return UNRESOLVED_EXPR
    if (seen.has(resolvedBinding.declaration)) return UNRESOLVED_EXPR
    // The stricter record-binding escape guard (recordBindingEscapes) only
    // applies when the binding is actually record-shaped (a plain object or
    // array literal, optionally Object.freeze/seal/preventExtensions-
    // wrapped) -- that shape is what the container-escape/primitive-leaf
    // classification needs to mean anything. A CallExpression-initialized
    // alias (`const TRIGGER = cn(...)`, `let btn = cn(...)`) is deliberately
    // left to the ORIGINAL, narrower isReassignedWithin gate: authenticated-
    // BuilderAlias (below, unchanged) independently requires const-ness
    // before trusting such an alias, and multiple existing tests (`keeps a
    // reassigned alias unsupported`, `reports definition debt once while a
    // reassigned renamed alias stays blocking at the sink`, `reports
    // aliased-call debt exactly once at the definition site (date-picker
    // shape)`) depend on a call-expression alias resolving THROUGH to its
    // definition site rather than collapsing to a generic unsupported here.
    const literalRoot = recordLiteralRoot(resolvedBinding.initializer)
    if (literalRoot && binding) {
      const scope = recordDeclarationScope(resolvedBinding.declaration)
      if (recordBindingEscapes(scope, node.text, literalRoot, resolvedBinding.declaration)) return UNRESOLVED_EXPR
      // Same-file declaration and read: the walk above only ever sees THIS
      // module. If the declaration is itself exported, a DIFFERENT module in
      // `ctx.modules` can still import and mutate it without ever touching
      // this file at all (SP-FALSE-GREEN fix) -- check every importer too.
      if (!recordExportedUsesSafe(resolvedBinding.declaration, ctx, literalRoot)) return UNRESOLVED_EXPR
    } else if (literalRoot && imported) {
      // The LOCAL import specifier's own binding, scoped to the whole
      // IMPORTING module -- `SIZES.small = '...'`/`Object.values(SIZES)...`
      // written anywhere in the consumer file after the import, not just
      // near the read site (import bindings are module-scoped, not
      // block-scoped, so the walk root is the whole SourceFile).
      const importerScope = node.getSourceFile()
      if (recordBindingEscapes(importerScope, node.text, literalRoot, resolvedBinding.declaration)) return UNRESOLVED_EXPR
      // The EXPORTING module's own declaration, scoped to ITS whole module --
      // a helper inside the exporting file that mutates its own export
      // (never touching the importer's local name at all) must be exactly
      // as disqualifying.
      const exportingDeclaration = resolvedBinding.declaration
      if (ts.isVariableDeclaration(exportingDeclaration) && ts.isIdentifier(exportingDeclaration.name)) {
        const exportingScope = exportingDeclaration.getSourceFile()
        if (recordBindingEscapes(exportingScope, exportingDeclaration.name.text, literalRoot, exportingDeclaration)) return UNRESOLVED_EXPR
      }
      // A THIRD module (neither this reader nor the declaring module) can
      // also import and mutate the same export (SP-FALSE-GREEN fix).
      if (!recordExportedUsesSafe(resolvedBinding.declaration, ctx, literalRoot)) return UNRESOLVED_EXPR
    } else if (binding && isReassignedWithin(node.getSourceFile(), node.text)) {
      return UNRESOLVED_EXPR
    }
    seen.add(resolvedBinding.declaration)
    return resolveExpr(resolvedBinding.initializer, ctx, seen)
  }
  if (ts.isPropertyAccessExpression(node)) {
    const obj = resolveExpr(node.expression, ctx, seen)
    if (obj === UNRESOLVED_EXPR) return UNRESOLVED_EXPR
    if (obj && ts.isObjectLiteralExpression(obj)) {
      const init = propertyInit(obj, node.name.text)
      return init ? resolveExpr(init, ctx, seen) : UNRESOLVED_EXPR
    }
    return UNRESOLVED_EXPR
  }
  if (ts.isElementAccessExpression(node)) {
    const keyExpr = unwrap(node.argumentExpression)
    // A numeric literal key (`RECORD[3]`) is a static key on an object
    // literal exactly as much as a string key is; Record<number, string> is
    // the common shape for a finite class/style lookup table (see
    // resolveClosedRecordValues below for the dynamic-key sibling of this).
    const key = keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr) || ts.isNumericLiteral(keyExpr))
      ? keyExpr.text
      : null
    const obj = resolveExpr(node.expression, ctx, seen)
    if (obj === UNRESOLVED_EXPR) return UNRESOLVED_EXPR
    if (keyExpr && ts.isNumericLiteral(keyExpr) && obj && ts.isArrayLiteralExpression(obj)) {
      const index = Number(keyExpr.text)
      if (!Number.isSafeInteger(index) || index < 0 || index >= obj.elements.length) return UNRESOLVED_EXPR
      const element = obj.elements[index]
      return element && !ts.isOmittedExpression(element) ? resolveExpr(element, ctx, seen) : UNRESOLVED_EXPR
    }
    if (key === null) return UNRESOLVED_EXPR
    if (obj && ts.isObjectLiteralExpression(obj)) {
      const init = propertyInit(obj, key)
      return init ? resolveExpr(init, ctx, seen) : UNRESOLVED_EXPR
    }
    return UNRESOLVED_EXPR
  }
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'Object') {
    const method = node.expression.name.text
    if (['freeze', 'seal', 'preventExtensions'].includes(method) && node.arguments[0]) {
      return resolveExpr(node.arguments[0], ctx, seen)
    }
  }
  return node
}

function governedModulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/')
    ? `src/${specifier.slice(2)}`
    : specifier.startsWith('.') ? path.posix.normalize(path.posix.join(path.posix.dirname(from), specifier)) : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

function moduleRecord(ctx, modulePath) {
  if (ctx.moduleCache.has(modulePath)) return ctx.moduleCache.get(modulePath)
  const source = ctx.modules?.[modulePath]
  if (typeof source !== 'string') return null
  const ext = path.posix.extname(modulePath)
  const kind = ext === '.tsx' ? ts.ScriptKind.TSX : ext === '.jsx' ? ts.ScriptKind.JSX : ext === '.js' ? ts.ScriptKind.JS : ts.ScriptKind.TS
  const sourceFile = ts.createSourceFile(modulePath, source, ts.ScriptTarget.Latest, true, kind)
  const record = { sourceFile, exports: new Map(), imports: new Map(), valid: !(sourceFile.parseDiagnostics ?? []).some((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error) }
  ctx.moduleCache.set(modulePath, record)
  for (const statement of sourceFile.statements) {
    if (ts.isVariableStatement(statement) && statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) record.exports.set(declaration.name.text, declaration)
    } else if (ts.isFunctionDeclaration(statement) && statement.name && statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) {
      record.exports.set(statement.name.text, statement)
    } else if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const bindings = statement.importClause?.namedBindings
      if (!bindings || !ts.isNamedImports(bindings)) continue
      for (const element of bindings.elements) record.imports.set(element.name.text, {
        specifier: statement.moduleSpecifier.text,
        imported: element.propertyName?.text ?? element.name.text,
      })
    }
  }
  return record
}

function resolveModuleExport(ctx, fromPath, specifier, exported, seen) {
  const targetPath = governedModulePath(fromPath, specifier, ctx.modules)
  if (!targetPath) return null
  const marker = `${targetPath}#${exported}`
  if (seen.has(marker)) return null
  const record = moduleRecord(ctx, targetPath)
  if (!record?.valid) return null
  const declaration = record.exports.get(exported)
  if (declaration) return declaration
  const forwarded = record.imports.get(exported)
  if (!forwarded) return null
  const nextSeen = new Set(seen)
  nextSeen.add(marker)
  return resolveModuleExport(ctx, targetPath, forwarded.specifier, forwarded.imported, nextSeen)
}

function importedDeclaration(identifier, ctx, seen) {
  const sourceFile = identifier.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const bindings = statement.importClause?.namedBindings
    if (!bindings || !ts.isNamedImports(bindings)) continue
    for (const element of bindings.elements) {
      if (element.name.text !== identifier.text) continue
      const declaration = resolveModuleExport(ctx, sourceFile.fileName, statement.moduleSpecifier.text, element.propertyName?.text ?? element.name.text, seen)
      if (!declaration) return null
      if (ts.isVariableDeclaration(declaration)) return { declaration, initializer: declaration.initializer ?? null }
      return null
    }
  }
  return null
}

function functionDeclarationFor(node, ctx) {
  const callee = unwrap(node)
  if (!callee || !ts.isIdentifier(callee)) return null
  let scope = callee.parent
  while (scope) {
    if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
      for (const statement of scope.statements) {
        if (ts.isFunctionDeclaration(statement) && statement.name?.text === callee.text) return statement
        if (!ts.isVariableStatement(statement)) continue
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === callee.text) {
            const initializer = unwrap(declaration.initializer)
            if (initializer && (ts.isArrowFunction(initializer) || ts.isFunctionExpression(initializer))) return initializer
          }
        }
      }
    }
    scope = scope.parent
  }
  const sourceFile = callee.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const bindings = statement.importClause?.namedBindings
    if (!bindings || !ts.isNamedImports(bindings)) continue
    for (const element of bindings.elements) {
      if (element.name.text !== callee.text) continue
      const declaration = resolveModuleExport(ctx, sourceFile.fileName, statement.moduleSpecifier.text, element.propertyName?.text ?? element.name.text, new Set())
      return declaration && (ts.isFunctionDeclaration(declaration) || ts.isArrowFunction(declaration) || ts.isFunctionExpression(declaration)) ? declaration : null
    }
  }
  return null
}

function pureFunctionReturns(call, ctx) {
  const declaration = functionDeclarationFor(call.expression, ctx)
  if (!declaration?.body) return null
  if (!ts.isBlock(declaration.body)) return isPureStaticExpression(declaration.body) ? [declaration.body] : null
  if (declaration.body.statements.length !== 1 || !ts.isReturnStatement(declaration.body.statements[0]) || !declaration.body.statements[0].expression) return null
  return isPureStaticExpression(declaration.body.statements[0].expression) ? [declaration.body.statements[0].expression] : null
}

function isPureStaticExpression(node) {
  let pure = true
  const visit = (current) => {
    if (!pure) return
    if (ts.isCallExpression(current) || ts.isNewExpression(current) || ts.isAwaitExpression(current)
      || ts.isYieldExpression(current) || ts.isTaggedTemplateExpression(current)
      || (ts.isBinaryExpression(current) && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment
        && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment)) {
      pure = false
      return
    }
    ts.forEachChild(current, visit)
  }
  visit(node)
  return pure
}

function isProvenBoolean(node, ctx, seen = new Set()) {
  const expr = unwrap(node)
  if (!expr) return false
  if (expr.kind === ts.SyntaxKind.TrueKeyword || expr.kind === ts.SyntaxKind.FalseKeyword) return true
  if (ts.isPrefixUnaryExpression(expr) && expr.operator === ts.SyntaxKind.ExclamationToken) return true
  if (ts.isIdentifier(expr)) {
    if (booleanParameter(expr)) return true
    const resolved = resolveExpr(expr, ctx, seen)
    return resolved !== UNRESOLVED_EXPR && resolved !== expr && isProvenBoolean(resolved, ctx, seen)
  }
  if (ts.isBinaryExpression(expr)) {
    return [ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken,
      ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.LessThanToken, ts.SyntaxKind.LessThanEqualsToken,
      ts.SyntaxKind.GreaterThanToken, ts.SyntaxKind.GreaterThanEqualsToken, ts.SyntaxKind.InKeyword,
      ts.SyntaxKind.InstanceOfKeyword].includes(expr.operatorToken.kind)
  }
  return false
}

function booleanParameter(identifier) {
  let owner = identifier.parent
  while (owner && !ts.isFunctionLike(owner)) owner = owner.parent
  if (!owner) return false
  for (const parameter of owner.parameters) {
    if (ts.isIdentifier(parameter.name) && parameter.name.text === identifier.text) return parameter.type?.kind === ts.SyntaxKind.BooleanKeyword
    if (!ts.isObjectBindingPattern(parameter.name) || !parameter.type || !ts.isTypeLiteralNode(parameter.type)) continue
    const bound = parameter.name.elements.some((element) => ts.isBindingElement(element) && ts.isIdentifier(element.name) && element.name.text === identifier.text)
    if (!bound) continue
    return parameter.type.members.some((member) => ts.isPropertySignature(member)
      && staticPropertyName(member.name) === identifier.text && member.type?.kind === ts.SyntaxKind.BooleanKeyword)
  }
  return false
}

function findLexicalBinding(identifier, name) {
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

function statementBinding(statements, name) {
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

function propertyInit(obj, name) {
  for (const prop of obj.properties) {
    if (!ts.isPropertyAssignment(prop)) continue
    const key = staticPropertyName(prop.name)
    if (key === name) return prop.initializer
  }
  return null
}

function calleeName(expr) {
  const node = unwrap(expr)
  if (!node) return ''
  if (ts.isIdentifier(node)) return node.text
  if (ts.isPropertyAccessExpression(node)) return node.name.text
  return ''
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
function staticPropertyName(name) {
  if (!name) return null
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) return name.text
  if (ts.isStringLiteral(name) || ts.isNumericLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name)) return name.text
  if (ts.isComputedPropertyName(name)) {
    const expr = unwrap(name.expression)
    if (expr && (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr))) return expr.text
  }
  return null
}

function unwrap(node) {
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

function locFromTs(node, sourceFile) {
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
function nodeKey(node) {
  return `${node.getSourceFile().fileName}#${node.getStart()}`
}

function withOrigin(loc, node) {
  return { ...loc, originKey: nodeKey(node) }
}

function formatPx(px) {
  const rounded = Math.round(px * 1000) / 1000
  if (Number.isInteger(rounded)) return `${rounded}px`
  return `${rounded}px`
}

function pushFinding(ctx, ruleId, syntax, message, loc) {
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
  // without an originKey keep the prior (line, column) behavior unchanged.
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
