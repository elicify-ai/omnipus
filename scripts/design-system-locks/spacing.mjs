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
  if (ts.isIdentifier(expr) || ts.isPropertyAccessExpression(expr) || ts.isElementAccessExpression(expr)) {
    const boundary = directForwardedParameter(expr, 'className')
    if (boundary) {
      pushFinding(ctx, RULE.extensionBoundary, `${boundary.symbol}#${boundary.name}`, 'Spacing extension boundary; caller-provided className is forwarded unchanged and requires exact central review.', location)
      if (boundary.initializer) visitClassBuilderArg(boundary.initializer, ctx, sourceFile)
      return
    }
    const resolved = resolveExpr(expr, ctx)
    if (resolved === UNRESOLVED_EXPR) {
      if (isCssModuleClassReference(expr, sourceFile)) return
      pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(sourceFile)}`, 'Unsupported spacing expression; dynamic or cyclic class aliases cannot be verified against the D10 scale.', location)
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
    analyzeClassList(expr.text, ctx, location)
    return
  }
  if (ts.isTemplateExpression(expr)) {
    analyzeClassTemplate(expr, ctx, sourceFile, location)
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
    if (!viaAliasResolution && guardClassBuilder(expr.expression, ctx) && builderArgumentsStaticallyGoverned(expr, ctx)) return
    const returns = pureFunctionReturns(expr, ctx)
    if (returns) {
      for (const returned of returns) visitClassBuilderArg(returned, ctx, returned.getSourceFile(), location)
      return
    }
    pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; dynamic or impure class helpers cannot be verified against the D10 scale.', location)
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
  pushFinding(ctx, RULE.unsupported, `className: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; this class expression requires explicit analysis.', location)
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
    pushFinding(ctx, RULE.unsupported, `style: ${expr.getText(expr.getSourceFile())}`, 'Unsupported spacing expression; dynamic or impure style helpers cannot be verified against the D10 scale.', locFromTs(expr, sourceFile))
    return
  }
  if (ts.isConditionalExpression(expr)) {
    visitStyleLike(expr.whenTrue, ctx, sourceFile)
    visitStyleLike(expr.whenFalse, ctx, sourceFile)
    return
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

function resolveExpr(expr, ctx, seen = new Set()) {
  const node = unwrap(expr)
  if (!node) return null
  if (ts.isIdentifier(node)) {
    const binding = findLexicalBinding(node, node.text)
    if (binding && isReassignedWithin(node.getSourceFile(), node.text)) return UNRESOLVED_EXPR
    const imported = binding ? null : importedDeclaration(node, ctx, seen)
    const resolvedBinding = binding ?? imported
    if (!resolvedBinding || !resolvedBinding.initializer) return UNRESOLVED_EXPR
    if (seen.has(resolvedBinding.declaration)) return UNRESOLVED_EXPR
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
    const key = keyExpr && (ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr))
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

function staticPropertyName(name) {
  if (!name) return null
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) return name.text
  if (ts.isStringLiteral(name) || ts.isNumericLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name)) return name.text
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

function formatPx(px) {
  const rounded = Math.round(px * 1000) / 1000
  if (Number.isInteger(rounded)) return `${rounded}px`
  return `${rounded}px`
}

function pushFinding(ctx, ruleId, syntax, message, loc) {
  const line = loc?.line
  // Dedupe is per source occurrence: a revisit of the same occurrence carries
  // an identical (line, column), while two distinct occurrences of one syntax
  // on a single line differ by column and must both count — fingerprint
  // occurrence counts are explicit Stage B requirements. Line and column stay
  // display-only on the finding; the orchestrator fingerprint ignores them.
  const column = Number.isInteger(loc?.column) && loc.column > 0 ? loc.column : undefined
  const exists = ctx.findings.some((finding) => finding.ruleId === ruleId && finding.syntax === syntax
    && finding.line === line && finding.column === column)
  if (exists) return
  const finding = { ruleId, path: ctx.path, syntax, message }
  if (Number.isInteger(line) && line > 0) finding.line = line
  if (column !== undefined) finding.column = column
  ctx.findings.push(finding)
}
