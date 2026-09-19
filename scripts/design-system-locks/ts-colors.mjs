import { extname, posix } from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'

export const extensions = Object.freeze(['.js', '.jsx', '.ts', '.tsx', '.svg'])

const LOOKUP_MISSING = Symbol('missing')
const LOOKUP_UNBOUND = Symbol('unbound')
const PARAMETER_BINDING = Symbol('parameter-binding')
const IMPORT_BINDING = Symbol('import-binding')
const MODULE_CACHES = new WeakMap()

const CLASS_BUILDERS = new Set([
  'cn',
  'clsx',
  'cva',
  'cx',
  'twMerge',
  'twJoin',
  'classNames',
  'classnames',
])

// Project-local class-composing helper names (not an importable package, so
// not reachable through CLASS_BUILDERS' name-resolution path) that make the
// exact same contract as cn/clsx: every CALL site already gets its own
// arguments independently inspected (inspectClassBuilderCall for a
// CLASS_BUILDERS name, or plain className/cn(...)-argument inspection
// wherever these are invoked), so the value flowing through this function's
// OWN parameter, at its OWN definition site, is never new — it is exactly
// what a validated call site handed it. Treating that parameter as an
// unproven value there is redundant re-validation of an already-covered
// site, not a real gap (P10 review finding). Named explicitly, like
// CLASS_BUILDERS, rather than inferred structurally, so an unrelated
// same-named local helper is never accidentally trusted.
const LOCAL_CLASS_COMPOSERS = new Set(['classes', 'statusDot'])

// Array methods whose result can never contain content absent from their
// receiver — `.join(sep)` stringifies exactly the receiver's own elements;
// `.filter(predicate)` (any predicate) only ever removes elements. Neither
// can manufacture a NEW, unproven value, so recursing into the receiver
// (rather than treating the call itself as an opaque, unresolvable value)
// does not weaken the proof — it is the same array, narrowed or stringified.
const ARRAY_RECEIVER_PRESERVING_METHODS = new Set(['join', 'filter'])

const COLOR_FUNCS = new Set(['rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color'])

const GROUP_FUNCS = new Set([
  'color-mix',
  'light-dark',
  'drop-shadow',
  'linear-gradient',
  'radial-gradient',
  'conic-gradient',
  'repeating-linear-gradient',
  'repeating-radial-gradient',
  'repeating-conic-gradient',
])

const PERMITTED_KEYWORDS = new Set([
  'transparent',
  'currentcolor',
  'inherit',
  'initial',
  'unset',
  'revert',
  'revert-layer',
  'none',
  'auto',
])

const SYSTEM_COLORS = new Set([
  'canvas',
  'canvastext',
  'linktext',
  'visitedtext',
  'activetext',
  'buttonface',
  'buttontext',
  'buttonborder',
  'field',
  'fieldtext',
  'highlight',
  'highlighttext',
  'selecteditem',
  'selecteditemtext',
  'mark',
  'marktext',
  'graytext',
  'accentcolor',
  'accentcolortext',
])

const NAMED_COLORS = new Set(
  `aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue
   blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk
   crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki
   darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen
   darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue
   dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite
   gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki
   lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan
   lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen
   lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen
   linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple
   mediumseagreen mediumslateblue mediumspringgreen mediumturquoise mediumvioletred
   midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab
   orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip
   peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue
   saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue
   slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise
   violet wheat white whitesmoke yellow yellowgreen`.trim().split(/\s+/),
)

const COLOR_PROPERTIES = new Set([
  'color',
  'fill',
  'stroke',
  'background',
  'backgroundcolor',
  'backgroundimage',
  'border',
  'bordercolor',
  'bordertop',
  'borderright',
  'borderbottom',
  'borderleft',
  'bordertopcolor',
  'borderrightcolor',
  'borderbottomcolor',
  'borderleftcolor',
  'outline',
  'outlinecolor',
  'textshadow',
  'boxshadow',
  'shadow',
  'textdecoration',
  'textdecorationcolor',
  'textemphasiscolor',
  'caretcolor',
  'accentcolor',
  'columnrule',
  'columnrulecolor',
  'stopcolor',
  'floodcolor',
  'lightingcolor',
  'scrollbarcolor',
  'filter',
  'backdropfilter',
])

const CANVAS_PAINT_PROPERTIES = new Set(['fillstyle', 'strokestyle', 'shadowcolor'])

const TAILWIND_PREFIXES = [
  'ring-offset',
  'placeholder',
  'decoration',
  'border-t',
  'border-r',
  'border-b',
  'border-l',
  'border-x',
  'border-y',
  'border-s',
  'border-e',
  'outline',
  'divide',
  'stroke',
  'shadow',
  'accent',
  'caret',
  'fill',
  'from',
  'via',
  'to',
  'ring',
  'text',
  'bg',
  'border',
]

const TAILWIND_PALETTES = new Set([
  'slate',
  'gray',
  'zinc',
  'neutral',
  'stone',
  'red',
  'orange',
  'amber',
  'yellow',
  'lime',
  'green',
  'emerald',
  'teal',
  'cyan',
  'sky',
  'blue',
  'indigo',
  'violet',
  'purple',
  'fuchsia',
  'pink',
  'rose',
])

const printer = ts.createPrinter({
  removeComments: true,
  newLine: ts.NewLineKind.LineFeed,
})

export function scan(input = {}) {
  const path = input.path
  const source = input.source
  if (typeof path !== 'string' || path.length === 0) {
    throw new TypeError('path must be a non-empty string')
  }
  if (typeof source !== 'string') {
    throw new TypeError('source must be a string')
  }

  const prepared = prepareSource(path, source)
  const sourceFile = ts.createSourceFile(path, prepared, ts.ScriptTarget.Latest, true, scriptKindFor(path))
  const modules = input.modules
  let moduleCache = new Map()
  if (modules && typeof modules === 'object') {
    moduleCache = MODULE_CACHES.get(modules) ?? moduleCache
    if (!MODULE_CACHES.has(modules)) MODULE_CACHES.set(modules, moduleCache)
  }
  const ctx = {
    path,
    sourceFile,
    tokens: tokenSet(input.policy),
    scopes: [],
    callFrames: [],
    findings: [],
    seen: new Set(),
    modules,
    moduleCache,
    sourceRecords: new Map(),
  }
  ctx.sourceRecords.set(sourceFile, indexModuleRecord(path, sourceFile))

  for (const diagnostic of sourceFile.parseDiagnostics ?? []) {
    if (diagnostic.category === ts.DiagnosticCategory.Error) emitParse(ctx, diagnostic)
  }

  walk(sourceFile, ctx)
  sortFindings(ctx.findings)
  return ctx.findings
}

function prepareSource(filePath, source) {
  if (extname(filePath).toLowerCase() !== '.svg') return source
  return source
    .replace(/^\uFEFF/, '')
    .replace(/^\s*<\?xml\b[^?]*\?>\s*/i, '')
    .replace(/^\s*<!DOCTYPE\s+svg\b[^>]*>\s*/i, '')
}

function scriptKindFor(filePath) {
  switch (extname(filePath).toLowerCase()) {
    case '.ts':
      return ts.ScriptKind.TS
    case '.tsx':
      return ts.ScriptKind.TSX
    case '.js':
      return ts.ScriptKind.JS
    case '.jsx':
      return ts.ScriptKind.JSX
    case '.svg':
      return ts.ScriptKind.JSX
    default:
      return ts.ScriptKind.TSX
  }
}

function tokenSet(policy) {
  const names = policy?.tokenCssNames
  const set = new Set()
  if (!Array.isArray(names)) return set
  for (const name of names) {
    if (typeof name !== 'string') continue
    const trimmed = name.trim()
    if (!trimmed) continue
    set.add(trimmed.startsWith('--') ? trimmed : `--${trimmed}`)
  }
  return set
}

function walk(node, ctx) {
  const scoped = ts.isBlock(node) || ts.isModuleBlock(node) || ts.isSourceFile(node)
  if (scoped) pushScope(ctx)
  if (ts.isSourceFile(node)) bindImports(ctx, node)

  if (isFunctionLike(node)) {
    pushScope(ctx)
    bindParams(ctx, node.parameters, node)
    inspectParameterDefaults(ctx, node.parameters)
    if (node.body) walk(node.body, ctx)
    popScope(ctx)
    if (scoped) popScope(ctx)
    return
  }

  if (ts.isCatchClause(node)) {
    pushScope(ctx)
    if (node.variableDeclaration) bindPattern(ctx, node.variableDeclaration.name, LOOKUP_UNBOUND)
    if (node.block) walk(node.block, ctx)
    popScope(ctx)
    if (scoped) popScope(ctx)
    return
  }

  if (ts.isVariableDeclaration(node)) bindPattern(ctx, node.name, node.initializer ?? LOOKUP_UNBOUND)

  inspectNode(node, ctx)
  ts.forEachChild(node, (child) => walk(child, ctx))
  if (scoped) popScope(ctx)
}

function governedModulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/') ? `src/${specifier.slice(2)}` : specifier.startsWith('.') ? posix.normalize(posix.join(posix.dirname(from), specifier)) : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

function indexModuleRecord(path, sf) {
  const record = { path, sf, declarations: new Map(), imports: new Map() }
  for (const statement of sf.statements) {
    if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) record.declarations.set(declaration.name.text, declaration)
    } else if (ts.isFunctionDeclaration(statement) && statement.name) record.declarations.set(statement.name.text, statement)
    else if ((ts.isInterfaceDeclaration(statement) || ts.isTypeAliasDeclaration(statement)) && statement.name) record.declarations.set(statement.name.text, statement)
    else if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const bindings = statement.importClause?.namedBindings
      if (!bindings || !ts.isNamedImports(bindings)) continue
      for (const element of bindings.elements) record.imports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    }
  }
  return record
}

function moduleRecord(ctx, path) {
  if (ctx.moduleCache.has(path)) {
    const record = ctx.moduleCache.get(path)
    ctx.sourceRecords.set(record.sf, record)
    return record
  }
  const source = ctx.modules?.[path]
  if (typeof source !== 'string') return null
  const sf = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, scriptKindFor(path))
  const record = indexModuleRecord(path, sf)
  ctx.moduleCache.set(path, record)
  ctx.sourceRecords.set(sf, record)
  return record
}

function resolveModuleDeclaration(ctx, fromPath, specifier, imported, seen = new Set()) {
  const path = governedModulePath(fromPath, specifier, ctx.modules)
  if (!path) return LOOKUP_UNBOUND
  const marker = `${path}#${imported}`
  if (seen.has(marker)) return LOOKUP_UNBOUND
  const record = moduleRecord(ctx, path)
  if (!record) return LOOKUP_UNBOUND
  const declaration = record.declarations.get(imported)
  if (declaration) return declaration
  const forwarded = record.imports.get(imported)
  if (!forwarded) return LOOKUP_UNBOUND
  return resolveModuleDeclaration(ctx, path, forwarded.specifier, forwarded.imported, new Set(seen).add(marker))
}

function bindImports(ctx, sf) {
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const bindings = statement.importClause?.namedBindings
    if (!bindings || !ts.isNamedImports(bindings)) continue
    for (const element of bindings.elements) {
      const declaration = resolveModuleDeclaration(ctx, ctx.path, statement.moduleSpecifier.text, element.propertyName?.text ?? element.name.text)
      bind(ctx, element.name.text, declaration === LOOKUP_UNBOUND ? LOOKUP_UNBOUND : { kind: IMPORT_BINDING, declaration })
    }
  }
}

function bindingValue(value) {
  if (!value || typeof value !== 'object' || value.kind !== IMPORT_BINDING) return value
  const declaration = value.declaration
  if (ts.isVariableDeclaration(declaration)) return declaration.initializer ?? LOOKUP_UNBOUND
  return declaration
}

function inspectNode(node, ctx) {
  if (ts.isCallExpression(node) && isClassBuilderCall(node, ctx)) {
    inspectClassBuilderCall(node, ctx, new Set())
    return
  }
  if (ts.isCallExpression(node)) inspectSetPropertyCall(node, ctx)
  if (ts.isJsxAttribute(node)) {
    inspectJsxAttribute(node, ctx)
    return
  }
  if (isHtmlStyleTag(node)) {
    inspectStyleElement(node, ctx)
    return
  }
  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
    markParameterReassignment(node, ctx)
    inspectPaintAssignment(node, ctx)
  }
  if (ts.isObjectLiteralExpression(node) && !isNonPaintApiObjectArgument(node, ctx)) {
    inspectStyleObject(node, ctx, new Set(), false)
  }
}

// The blanket "any color-shaped object literal, anywhere" walk above exists
// to catch a style object built separately from the JSX/DOM site that later
// applies it (an indirect construction inspectStyleExpr's own JSX-triggered
// walk cannot see). It has no way to tell that apart from an object literal
// that is never applied to anything — most visibly, a fixture object handed
// directly to a known non-paint API: a jest-dom/testing-library assertion
// (`expect(el).toHaveStyle({ color: … })`, matching the file's existing
// `expect`/`z` non-paint recognition below) or a Vitest mock return value
// (`vi.spyOn(...).mockReturnValue({ stroke: () => {} })` — `stroke` there is
// a Canvas 2D method name, not the SVG colour property it happens to share a
// name with). Neither can ever reach a real render; skip only the object
// literal that is itself (or is inside a single array wrapper of) a direct
// argument to such a call (P13 review finding) — this does not touch the
// object literal's OWN nested values, spreads, or any other object literal
// found elsewhere in the walk.
function isNonPaintApiObjectArgument(node, ctx) {
  let expr = node
  // Walk out through a cast/paren/non-null wrapper directly on the object
  // literal itself (`{…} as unknown as CanvasRenderingContext2D`, a common
  // vitest `mockReturnValue(...)` shape) and, separately, through a single
  // array-literal wrapper (`mockReturnValue([{…}])`) — neither changes
  // which call the object literal is ultimately an argument to.
  while (expr.parent && (ts.isParenthesizedExpression(expr.parent) || ts.isAsExpression(expr.parent)
    || ts.isSatisfiesExpression(expr.parent) || ts.isNonNullExpression(expr.parent))) expr = expr.parent
  let parent = expr.parent
  if (parent && ts.isArrayLiteralExpression(parent)) {
    expr = parent
    while (expr.parent && (ts.isParenthesizedExpression(expr.parent) || ts.isAsExpression(expr.parent)
      || ts.isSatisfiesExpression(expr.parent) || ts.isNonNullExpression(expr.parent))) expr = expr.parent
    parent = expr.parent
  }
  return Boolean(parent) && ts.isCallExpression(parent) && parent.arguments.includes(expr) && isNonPaintApiCall(parent, ctx)
}

function isFunctionLike(node) {
  return (
    ts.isFunctionDeclaration(node) ||
    ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) ||
    ts.isMethodDeclaration(node) ||
    ts.isConstructorDeclaration(node) ||
    ts.isGetAccessorDeclaration(node) ||
    ts.isSetAccessorDeclaration(node)
  )
}

function pushScope(ctx) {
  ctx.scopes.push(new Map())
}

function popScope(ctx) {
  ctx.scopes.pop()
}

function bind(ctx, name, init) {
  const scope = ctx.scopes[ctx.scopes.length - 1]
  if (scope) scope.set(name, init)
}

function bindParams(ctx, parameters, owner) {
  const ownerName = functionIdentity(owner)
  for (const parameter of parameters) {
    // composerForwardParameter is only ever true for the TOP-LEVEL parameter
    // identifier itself (it requires ts.isIdentifier(parameter.name)), never
    // propagated into a destructured sub-pattern — an unrelated same-named
    // field of a destructured options object is never swept in.
    bindParameterPattern(ctx, parameter.name, parameter, ownerName, composerForwardParameter(parameter, owner))
  }
}

function bindParameterPattern(ctx, name, declaration, ownerName, composerForward = false) {
  if (ts.isIdentifier(name)) {
    bind(ctx, name.text, { kind: PARAMETER_BINDING, name: name.text, ownerName, declaration, reassigned: false, boolean: parameterIsBoolean(name.text, declaration, ctx), forwardClassName: name.text === 'className' || composerForward })
    return
  }
  if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
    for (const element of name.elements) {
      if (ts.isBindingElement(element)) bindParameterPattern(ctx, element.name, element, ownerName)
    }
  }
}

function parameterIsBoolean(name, declaration, ctx) {
  if (ts.isBindingElement(declaration) && declaration.initializer && (declaration.initializer.kind === ts.SyntaxKind.TrueKeyword || declaration.initializer.kind === ts.SyntaxKind.FalseKeyword)) return true
  let parameter = declaration
  while (parameter && !ts.isParameter(parameter)) parameter = parameter.parent
  const type = parameter?.type
  if (type) {
    if (ts.isIdentifier(parameter.name) && parameter.name.text === name) return type.kind === ts.SyntaxKind.BooleanKeyword
    if (typeHasBooleanProperty(type, name, ctx)) return true
  }
  const fn = parameter?.parent
  const call = fn?.parent
  const contextual = call && ts.isCallExpression(call) && call.arguments.includes(fn) ? call.typeArguments?.[1] : null
  return Boolean(contextual && typeHasBooleanProperty(contextual, name, ctx))
}

function typeHasBooleanProperty(type, name, ctx, seen = new Set()) {
  type = unwrap(type)
  if (!type) return false
  if (ts.isTypeLiteralNode(type)) return type.members.some((member) => ts.isPropertySignature(member) && propertyNameOf(member.name) === name && member.type?.kind === ts.SyntaxKind.BooleanKeyword)
  if (ts.isIntersectionTypeNode(type) || ts.isUnionTypeNode(type)) return type.types.some((part) => typeHasBooleanProperty(part, name, ctx, seen))
  if (!ts.isTypeReferenceNode(type) || !ts.isIdentifier(type.typeName) || seen.has(type.typeName.text)) return false
  seen.add(type.typeName.text)
  const declaration = ctx.sourceRecords.get(type.getSourceFile())?.declarations.get(type.typeName.text)
  if (!declaration) return false
  if (ts.isInterfaceDeclaration(declaration)) return declaration.members.some((member) => ts.isPropertySignature(member) && propertyNameOf(member.name) === name && member.type?.kind === ts.SyntaxKind.BooleanKeyword)
  return ts.isTypeAliasDeclaration(declaration) && typeHasBooleanProperty(declaration.type, name, ctx, seen)
}

function functionIdentity(node) {
  if (node.name && ts.isIdentifier(node.name)) return node.name.text
  if (ts.isMethodDeclaration(node) && node.name) return propertyNameOf(node.name) ?? 'anonymous'
  let parent = node.parent
  while (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent)) {
    parent = parent.parent
  }
  while (ts.isCallExpression(parent) && isComponentWrapper(parent.expression)) {
    parent = parent.parent
    while (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent) || ts.isSatisfiesExpression(parent)) {
      parent = parent.parent
    }
  }
  if (ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text
  if (ts.isPropertyAssignment(parent)) return propertyNameOf(parent.name) ?? 'anonymous'
  if (ts.isExportAssignment(parent)) return 'default'
  return 'anonymous'
}

function receivingSymbol(node) {
  let current = node
  while (current && !isFunctionLike(current)) current = current.parent
  return current ? functionIdentity(current) : 'anonymous'
}

function paintBoundary(node, receiver, property) {
  return { owner: receivingSymbol(node), receiver, property }
}

function assignmentBoundary(node) {
  const left = unwrap(node.left)
  let property = 'unknown'
  if (ts.isPropertyAccessExpression(left)) property = left.name.text
  else if (ts.isElementAccessExpression(left)) {
    const key = unwrap(left.argumentExpression)
    if (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key)) property = key.text
  }
  return paintBoundary(node, isCanvasPaintName(property) ? 'canvas-paint' : 'dom-style', property)
}

function isDirectRuntimeRead(node) {
  node = unwrap(node)
  if (ts.isIdentifier(node)) return true
  if (ts.isPropertyAccessExpression(node)) return isDirectRuntimeRead(node.expression)
  if (ts.isElementAccessExpression(node)) {
    const key = unwrap(node.argumentExpression)
    return (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key) || ts.isNumericLiteral(key)) && isDirectRuntimeRead(node.expression)
  }
  return false
}

function isComponentWrapper(expression) {
  expression = unwrap(expression)
  if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
  if (ts.isPropertyAccessExpression(expression)) {
    return expression.name.text === 'forwardRef' || expression.name.text === 'memo'
  }
  return false
}

function inspectParameterDefaults(ctx, parameters) {
  for (const parameter of parameters) {
    if (parameter.initializer && bindingContainsClassName(parameter.name)) {
      inspectClassExpr(parameter.initializer, ctx, new Set())
    }
    inspectBindingDefaults(ctx, parameter.name)
  }
}

function inspectBindingDefaults(ctx, name) {
  if (!ts.isObjectBindingPattern(name) && !ts.isArrayBindingPattern(name)) return
  for (const element of name.elements) {
    if (!ts.isBindingElement(element)) continue
    if (element.initializer && bindingContainsClassName(element.name)) {
      inspectClassExpr(element.initializer, ctx, new Set())
    }
    inspectBindingDefaults(ctx, element.name)
  }
}

function bindingContainsClassName(name) {
  if (ts.isIdentifier(name)) return name.text === 'className'
  if (!ts.isObjectBindingPattern(name) && !ts.isArrayBindingPattern(name)) return false
  return name.elements.some((element) => ts.isBindingElement(element) && bindingContainsClassName(element.name))
}

function isParameterBinding(value) {
  return Boolean(value && typeof value === 'object' && value.kind === PARAMETER_BINDING)
}

function markParameterReassignment(node, ctx) {
  const target = unwrap(node.left)
  if (!ts.isIdentifier(target)) return
  const binding = lookup(ctx, target.text)
  if (!isParameterBinding(binding)) return
  binding.reassigned = true
  inspectClassExpr(node.right, ctx, new Set())
}

function bindPattern(ctx, name, init) {
  if (ts.isIdentifier(name)) {
    bind(ctx, name.text, init)
    return
  }
  if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
    for (const element of name.elements) {
      if (ts.isBindingElement(element)) bindPattern(ctx, element.name, LOOKUP_UNBOUND)
    }
  }
}

function lookup(ctx, name) {
  for (let index = ctx.scopes.length - 1; index >= 0; index -= 1) {
    if (ctx.scopes[index].has(name)) return ctx.scopes[index].get(name)
  }
  return LOOKUP_MISSING
}

function unwrap(node) {
  while (node) {
    if (
      ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) ||
      ts.isSatisfiesExpression(node) ||
      ts.isNonNullExpression(node) ||
      (typeof ts.isTypeAssertionExpression === 'function' && ts.isTypeAssertionExpression(node))
    ) {
      node = node.expression
      continue
    }
    break
  }
  return node
}

function inspectJsxAttribute(attr, ctx) {
  const name = jsxAttrName(attr.name)
  if (!attr.initializer) return
  let value = attr.initializer
  if (ts.isJsxExpression(value)) value = value.expression
  if (!value) return

  if (name === 'class' || name === 'className') {
    inspectClassExpr(value, ctx, new Set(), paintBoundary(attr, 'className', name))
    return
  }
  if (name === 'style') {
    inspectStyleExpr(value, ctx, new Set())
    return
  }
  if (isJsxColorAttr(name)) inspectCssValueExpr(value, ctx, new Set(), paintBoundary(attr, 'svg-attribute', name))
}

function jsxAttrName(name) {
  if (ts.isIdentifier(name)) return name.text
  if (typeof ts.isJsxNamespacedName === 'function' && ts.isJsxNamespacedName(name)) {
    return `${name.namespace.text}-${name.name.text}`
  }
  return ''
}

function isJsxColorAttr(name) {
  const normalized = name.toLowerCase().replace(/:/g, '-')
  if (normalized === 'fill' || normalized === 'stroke' || normalized === 'color') return true
  const compact = normalized.replace(/-/g, '')
  return compact === 'stopcolor' || compact === 'floodcolor' || compact === 'lightingcolor'
}

function isHtmlStyleTag(node) {
  const tag = ts.isJsxElement(node)
    ? node.openingElement.tagName
    : ts.isJsxSelfClosingElement(node)
      ? node.tagName
      : null
  return Boolean(tag && ts.isIdentifier(tag) && tag.text === 'style')
}

function inspectStyleElement(node, ctx) {
  const opening = ts.isJsxElement(node) ? node.openingElement : node
  for (const attr of opening.attributes.properties) {
    if (!ts.isJsxAttribute(attr)) continue
    if (jsxAttrName(attr.name) !== 'dangerouslySetInnerHTML') continue
    let value = attr.initializer
    if (value && ts.isJsxExpression(value)) value = value.expression
    if (!value) continue
    const obj = unwrap(value)
    if (ts.isObjectLiteralExpression(obj)) {
      for (const prop of obj.properties) {
        if (!ts.isPropertyAssignment(prop)) continue
        if (propertyNameOf(prop.name) === '__html') inspectCssTextExpr(prop.initializer, ctx, new Set())
      }
    } else {
      emitUnsupported(ctx, obj)
    }
  }
  if (!ts.isJsxElement(node)) return
  for (const child of node.children) {
    if (ts.isJsxExpression(child) && child.expression) inspectCssTextExpr(child.expression, ctx, new Set())
    else if (ts.isJsxText(child) && child.text.trim()) analyzeCssText(child.text, ctx, child)
  }
}

function inspectCssTextExpr(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    analyzeCssText(node.text, ctx, node)
    return
  }
  if (ts.isTemplateExpression(node)) {
    inspectTemplate(node, ctx, 'css-text', stack)
    return
  }
  if (ts.isIdentifier(node)) {
    if (isSkipIdent(node.text)) return
    const init = resolveIdentInit(node, ctx, stack)
    if (isParameterBinding(init)) {
      emitUnsupported(ctx, node)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      emitUnsupported(ctx, node)
      return
    }
    inspectCssTextExpr(init, ctx, stack)
    return
  }
  emitUnsupported(ctx, node)
}

function isCanvasPaintName(name) {
  return CANVAS_PAINT_PROPERTIES.has(name.replace(/-/g, '').toLowerCase())
}

function isStyleReceiver(node) {
  node = unwrap(node)
  if (!node) return false
  if (ts.isIdentifier(node)) return node.text === 'style'
  if (ts.isPropertyAccessExpression(node)) return node.name.text === 'style'
  if (ts.isElementAccessExpression(node)) {
    const key = unwrap(node.argumentExpression)
    return (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key)) && key.text === 'style'
  }
  return false
}

function classifyPaintTarget(left) {
  left = unwrap(left)
  if (ts.isPropertyAccessExpression(left)) {
    const name = left.name.text
    if (isCanvasPaintName(name)) return 'css-value'
    if (name === 'cssText' && isStyleReceiver(left.expression)) return 'css-text'
    if (isColorPropertyName(name) && isStyleReceiver(left.expression)) return 'css-value'
    return null
  }
  if (ts.isElementAccessExpression(left)) {
    const keyExpr = unwrap(left.argumentExpression)
    if (!(ts.isStringLiteral(keyExpr) || ts.isNoSubstitutionTemplateLiteral(keyExpr))) {
      return isStyleReceiver(left.expression) ? 'unsupported' : null
    }
    const key = keyExpr.text
    if (isCanvasPaintName(key)) return 'css-value'
    if (key === 'cssText' && isStyleReceiver(left.expression)) return 'css-text'
    if (isColorPropertyName(key) && isStyleReceiver(left.expression)) return 'css-value'
    return null
  }
  return null
}

function inspectPaintAssignment(node, ctx) {
  const kind = classifyPaintTarget(node.left)
  if (kind === 'css-value') inspectCssValueExpr(node.right, ctx, new Set(), assignmentBoundary(node))
  else if (kind === 'css-text') inspectCssTextExpr(node.right, ctx, new Set())
  else if (kind === 'unsupported') {
    emitUnsupported(ctx, node.left)
    inspectCssValueExpr(node.right, ctx, new Set())
  }
}

function inspectSetPropertyCall(call, ctx) {
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
    inspectCssValueExpr(valueNode, ctx, new Set())
  }
}

function inspectStyleExpr(node, ctx, stack) {
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
      emitUnsupported(ctx, node)
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
  emitUnsupported(ctx, node)
}

function inspectStyleObject(obj, ctx, stack, strictBoundary) {
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

function inspectSpreadStyle(prop, ctx, stack) {
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

function propertyNameOf(name) {
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
// left-to-right override order the language uses (P11 review finding).
function opaqueObjectMember(member) {
  if (!ts.isPropertyAssignment(member)) return true
  return ts.isComputedPropertyName(member.name) && propertyNameOf(member.name) === null
}

function isColorPropertyName(name) {
  const normalized = name.replace(/-/g, '').toLowerCase()
  return COLOR_PROPERTIES.has(normalized) || normalized.endsWith('color')
}

function inspectCssValueExpr(node, ctx, stack, boundary = null) {
  node = unwrap(node)
  if (!node || isIgnoredLiteral(node)) return
  if (isDefinitelyNumeric(node, ctx) || isNumericSerialization(node, ctx)) {
    inspectLiteralColorArguments(node, ctx)
    return
  }
  if (ts.isCallExpression(node) && isNonPaintApiCall(node, ctx)) return

  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    analyzeCssValue(node.text, ctx, node)
    return
  }
  if (ts.isTemplateExpression(node)) {
    inspectTemplate(node, ctx, 'css', stack)
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
      if (boundary && init.ownerName !== 'anonymous' && !init.reassigned) emitRuntimePaintBoundary(ctx, node, boundary)
      else emitUnsupported(ctx, node)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      const destructured = resolveDestructuredTargets(node, ctx, stack)
      if (destructured) {
        if (!destructured.resolved) { emitUnsupported(ctx, node); return }
        for (const target of destructured.targets) inspectCssValueExpr(target, ctx, stack, boundary)
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
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, stack, resolution)
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

function inspectClassBuilderCall(call, ctx, stack) {
  const name = resolveCalleeName(call.expression, ctx)
  for (const arg of call.arguments) {
    if (name === 'cva') inspectCvaArg(arg, ctx, stack)
    else inspectClassExpr(arg, ctx, stack)
  }
}

function inspectCvaArg(node, ctx, stack) {
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

function inspectClassExpr(node, ctx, stack, boundary = null) {
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
      if (!init.forwardClassName || init.reassigned || init.ownerName === 'anonymous') emitUnsupported(ctx, node)
      else emitExtensionBoundary(ctx, node, init)
      return
    }
    if (init === LOOKUP_MISSING || init === LOOKUP_UNBOUND || !init) {
      const destructured = resolveDestructuredTargets(node, ctx, stack)
      if (destructured) {
        if (!destructured.resolved) { emitUnsupported(ctx, node); return }
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
    const resolution = { incomplete: false }
    const targets = resolveMemberTargets(node, ctx, stack, resolution)
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
    && ARRAY_RECEIVER_PRESERVING_METHODS.has(unwrap(node.expression).name.text)) {
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
    // `.filter(...)` call itself (P10 review finding).
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
function absentClassProperty(node, ctx) {
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
function knownClassReceiverStable(node, ctx, seen = new Set()) {
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
function isJsModulePath(path) {
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
function dynamicImportProvenNotOrigin(modulePath, argument, origin) {
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
function knownClassExportUsesSafe(declaration, ctx) {
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
function isJsxTagName(expression) {
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
const UNCONDITIONALLY_READONLY_STATIC_METHODS = new Set(['keys', 'isFrozen', 'getOwnPropertyNames'])

// `Object.values`/`Object.entries` also return a brand-new array — but its
// elements (for `entries`, each pair's second element) are `X`'s OWN nested
// property VALUES, handed out live: mutating an element mutates `X` itself.
// Exempting the argument occurrence and stopping there (as this file used to
// do, folding these in with `keys`) is unsound — it proves nothing about
// what the caller does with the returned array afterward (store it, iterate
// it with `forEach`/`for...of`/`.map`/spread/`Array.from`, mutate an
// element). This scanner does not attempt to trace every way such a
// live-reference array can be consumed, so per the "when in doubt, treat it
// as an escape" rule, passing the receiver to either of these is itself
// treated as an escape of its nested values — there is no safe default here
// the way there is for `keys`.
const NESTED_REFERENCE_RETURNING_STATIC_METHODS = new Set(['values', 'entries'])

// `Object.freeze(X)` returns the SAME reference passed in — not a copy — and
// only sets `X`'s own (shallow) mutability flag: nested objects reachable
// through `X` stay fully mutable after the call. Treating the argument
// occurrence as safe is sound ONLY when nobody captures or chains off the
// call's return value (a bare `Object.freeze(X)` statement); the instant the
// return value is assigned, chained, or otherwise consumed, it is exactly as
// live an alias of `X` as `X` itself and must not be exempted here.
function objectFreezeCallArgumentDiscardsReturn(callExpr) {
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

function objectStaticCallArgumentMethod(node) {
  const parent = node.parent
  if (!ts.isCallExpression(parent) || parent.expression === node || !parent.arguments.includes(node)) return null
  const callee = unwrap(parent.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ts.isIdentifier(callee.expression) || callee.expression.text !== 'Object') return null
  return { method: callee.name.text, call: parent }
}

function isReadonlyObjectStaticCallArgument(node) {
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
function primitiveLeaf(value) {
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
function knownClassDerivedUsesSafe(declaration, ctx, seen = new Set()) {
  if (!declaration.name || !ts.isIdentifier(declaration.name) || seen.has(declaration)) return false
  const next = new Set(seen).add(declaration)
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  // The JsxExpression carve-out below (W1 review finding) is sound ONLY for
  // a binding read exactly once in scope — the review's own probe frames it
  // as a "single-use const record". With a SIBLING read elsewhere (even one
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
          // carve-out (W1 review finding) — do not broaden to "any non-alias
          // use is safe", which would defeat the walk entirely.
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

function absenceBindingHasName(binding, name) {
  if (ts.isIdentifier(binding)) return binding.text === name
  return (ts.isObjectBindingPattern(binding) || ts.isArrayBindingPattern(binding))
    && binding.elements.some(element => ts.isBindingElement(element) && absenceBindingHasName(element.name, name))
}

// Resolve lexical declarations without falling through a nearer opaque binding.
// Switch, namespace, class and loop scopes are deliberately outside this proof.
function absenceLexicalBinding(identifier) {
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
function destructuredPatternUsesSafe(pattern) {
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
function absenceBindingUsesSafe(declaration, factory, indexedReads = false) {
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
        if ((ts.isBinaryExpression(use) && use.left === expression && use.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && use.operatorToken.kind <= ts.SyntaxKind.LastAssignment)
          || ((ts.isForOfStatement(use) || ts.isForInStatement(use)) && use.initializer === expression)
          || ts.isDeleteExpression(use) || ts.isPrefixUnaryExpression(use) || ts.isPostfixUnaryExpression(use)
          || (ts.isCallExpression(use) && use.expression === expression)) { safe = false; return }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(scope)
  return safe
}

function absenceFactory(callee, ctx) {
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

function absenceValue(node, property, ctx) {
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

function isNonPaintApiCall(call, ctx) {
  let expression = unwrap(call.expression)
  const names = []
  while (ts.isPropertyAccessExpression(expression)) {
    names.push(expression.name.text)
    expression = unwrap(expression.expression)
    if (ts.isCallExpression(expression)) expression = unwrap(expression.expression)
  }
  const root = ts.isIdentifier(expression) ? expression.text : ''
  // `vi` is Vitest's test-utility namespace (vi.fn/vi.spyOn/vi.mock/…) — it
  // is only ever importable inside a test file, never bundled into runtime
  // application code, so a value that is the argument to (or return/
  // implementation of) a `vi.*` call can never reach a real render; it is
  // fixture/mock data standing in for a real API's shape (P13 review
  // finding — e.g. `vi.spyOn(Canvas...).mockReturnValue({ stroke: () => {} })`,
  // where `stroke` is the Canvas 2D method name, not an SVG colour property).
  if (root === 'z' || root === 'expect' || root === 'vi' || names.includes('stringMatching')) return true
  if (!root || !ctx) return false
  const binding = lookup(ctx, root)
  const value = bindingValue(binding)
  return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && originatesFromNonPaintApi(value, ctx, new Set([root])))
}

function originatesFromNonPaintApi(node, ctx, seen) {
  node = unwrap(node)
  if (!node) return false
  if (ts.isCallExpression(node)) return originatesFromNonPaintApi(node.expression, ctx, seen)
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) return originatesFromNonPaintApi(node.expression, ctx, seen)
  if (!ts.isIdentifier(node)) return false
  if (node.text === 'z' || node.text === 'expect' || node.text === 'vi') return true
  if (seen.has(node.text)) return false
  seen.add(node.text)
  const value = bindingValue(lookup(ctx, node.text))
  return Boolean(value && value !== LOOKUP_MISSING && value !== LOOKUP_UNBOUND && originatesFromNonPaintApi(value, ctx, seen))
}

function hasStableRuntimeParameterRoot(node, ctx) {
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

function inspectClassObjectKeys(obj, ctx) {
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

function inspectLogical(node, ctx, stack, inspect) {
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

function isDefinitelyBoolean(node, ctx) {
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

function inspectTemplate(node, ctx, mode, stack) {
  const parts = [node.head.text]
  let unresolved = false
  for (const span of node.templateSpans) {
    const text = tryString(span.expression, ctx, stack)
    if (text === null) {
      // Narrow escape hatch (Task 2 capability), deliberately NOT a mirror of
      // class mode's per-span fan-out below: a css-value template can
      // assemble a compound functional value (e.g. linear-gradient(...)), so
      // an unverified RUNTIME value (parameter, opaque call, extension
      // boundary) must never be spliced into it — that would be genuine
      // CSS-syntax-injection surface, unlike a whitespace-separated Tailwind
      // class list. Only a destructured member whose every resolved source
      // object is provably fixed at build time (a plain string literal in
      // every branch of a local/imported pure factory — the fileTypeMeta
      // shape: `const { color } = fileTypeMeta(...)`) counts as "the base
      // value is proven"; a bare `${value}` reaching a parameter/boundary
      // keeps the unconditional numeric-proof requirement below untouched
      // (named numeric proof adversarial suite). css-decl and css-text also
      // stay untouched: a template there can span multiple declarations or
      // property names, which this literal-only proof does not model.
      const literalTargets = mode === 'css' ? literalDestructuredTemplateTargets(span.expression, ctx, stack) : null
      if (mode === 'class') {
        const expression = unwrap(span.expression)
        const binding = ts.isIdentifier(expression) ? resolveIdentInit(expression, ctx, stack) : null
        if (isParameterBinding(binding) && binding.forwardClassName) emitUnsupported(ctx, expression)
        else inspectClassExpr(span.expression, ctx, stack)
      }
      else if (literalTargets) {
        for (const target of literalTargets) analyzeCssValue(target.text, ctx, target)
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

function isDefinitelyNumeric(node, ctx) {
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
function namedNumericProperty(type, property, origin, seen) {
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

function isNumericSerialization(node, ctx) {
  node = unwrap(node)
  if (!ts.isCallExpression(node) || node.arguments.length !== 1) return false
  const callee = unwrap(node.expression)
  return ts.isIdentifier(callee) && callee.text === 'String' && isDefinitelyNumeric(node.arguments[0], ctx)
}

function inspectLiteralColorArguments(node, ctx) {
  node = unwrap(node)
  if (!ts.isCallExpression(node)) return
  for (const argument of node.arguments) {
    const value = unwrap(argument)
    if (ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)) analyzeCssValue(value.text, ctx, value)
  }
}

function tryString(node, ctx, stack) {
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

function resolveIdentInit(ident, ctx, stack) {
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
function positionalInit(ident, ctx, stack) {
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
const POSITIONAL_UNSTABLE_INITS = new WeakSet()

function declarationInit(declaration, ident, ctx, stack) {
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
function destructuredBindingElement(ident) {
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

// Only a simple, unrenamed-or-renamed, non-rest, non-default, non-nested
// element is a stable proof target. A rest collection, a default fallback
// value, or a nested sub-pattern each carry aggregation/fallback semantics
// this proof does not model, so they stay unsupported (never guessed at).
function destructuredElementOf(declaration, pattern, name) {
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
function destructuredElementReassigned(declaration, element) {
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
function resolveDestructuredTargets(ident, ctx, stack) {
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
  // uses, so it does not need to blank the whole proof (P11 review finding).
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

// Only a bare string literal (or no-substitution template) is fixed at
// build time and therefore safe to splice into a larger css-value template
// with no runtime-injection surface — see inspectTemplate's css-mode branch.
function isLiteralCssValue(node) {
  return ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)
}

// css-value template escape hatch for a destructured identifier span (Task 2
// capability): returns the finite set of literal target nodes only when the
// span is a bare identifier resolving, via resolveDestructuredTargets, to
// values that are ALL plain string literals — never a parameter, call, or
// any other runtime-shaped value. Returns null otherwise (not this case at
// all, or resolved to something not provably literal), so the caller falls
// back to the pre-existing numeric-proof/unresolved behavior unchanged.
function literalDestructuredTemplateTargets(expression, ctx, stack) {
  const node = unwrap(expression)
  if (!ts.isIdentifier(node)) return null
  const destructured = resolveDestructuredTargets(node, ctx, stack)
  if (!destructured?.resolved || !destructured.targets.every(isLiteralCssValue)) return null
  return destructured.targets
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
const ARRAY_MUTATING_METHODS = new Set(['push', 'pop', 'shift', 'unshift', 'splice', 'sort', 'reverse', 'fill', 'copyWithin'])

function composerParameterIsMutated(owner, paramName) {
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
// an active callee frame or the in-place walk (P10 review finding).
function composerForwardParameter(parameter, owner) {
  if (!ts.isIdentifier(parameter.name)) return false
  const ownerName = functionIdentity(owner)
  if (!CLASS_BUILDERS.has(ownerName) && !LOCAL_CLASS_COMPOSERS.has(ownerName)) return false
  if (!(Boolean(parameter.dotDotDotToken) || owner.parameters.length === 1)) return false
  return !composerParameterIsMutated(owner, parameter.name.text)
}

function positionalParameterInit(owner, name, ctx) {
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
        return { kind: PARAMETER_BINDING, name, ownerName: functionIdentity(owner), declaration: parameter, reassigned: false, boolean: false, forwardClassName: true }
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

const WHOLE_VALUE_WRITE_CACHE = new WeakMap()

function wholeValueWritesAbsent(declaration) {
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

function calleeDeclaration(call, ctx, stack) {
  const callee = unwrap(call.expression)
  return isFunctionLike(callee) ? callee : ts.isIdentifier(callee) ? resolveIdentInit(callee, ctx, stack) : null
}

function isLocalFunction(declaration) {
  return Boolean(declaration && declaration !== LOOKUP_UNBOUND
    && (ts.isFunctionDeclaration(declaration) || ts.isFunctionExpression(declaration) || ts.isArrowFunction(declaration)) && declaration.body)
}

function collectReturns(declaration) {
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

function functionReturns(call, ctx, stack) {
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
function withCalleeFrame(call, ctx, stack, body) {
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

function resolveMember(node, ctx, stack) {
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

function resolveMemberTargets(node, ctx, stack, resolution = null) {
  const target = resolution ? null : resolveMember(node, ctx, stack)
  if (target) return [target]
  node = unwrap(node)
  if (!ts.isElementAccessExpression(node) && !ts.isPropertyAccessExpression(node)) return []
  if (ts.isElementAccessExpression(node)) {
    const arrayResolution = { incomplete: false }
    const arrays = resolveToArrays(unwrap(node.expression), ctx, stack, arrayResolution)
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
  const objects = resolveToObjects(unwrap(node.expression), ctx, stack, resolution)
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

function resolveToArrays(node, ctx, stack, resolution = null) {
  node = unwrap(node)
  if (ts.isArrayLiteralExpression(node)) return [node]
  if (ts.isIdentifier(node)) {
    // Position-keyed, not text-keyed — see inspectClassExpr's identifier
    // marker for the shadowing rationale (nested-helper parameter-frame fix).
    const marker = `${node.getSourceFile().fileName}#array:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const init = resolveIdentInit(node, ctx, next)
      if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToArrays(init, ctx, next, resolution)
    }
  }
  if (ts.isConditionalExpression(node)) return [...resolveToArrays(node.whenTrue, ctx, stack, resolution), ...resolveToArrays(node.whenFalse, ctx, stack, resolution)]
  if (resolution) resolution.incomplete = true
  return []
}

function resolveToObjects(node, ctx, stack, resolution = null) {
  node = unwrap(node)
  if (ts.isObjectLiteralExpression(node)) return [node]
  if (ts.isIdentifier(node)) {
    // Position-keyed, not text-keyed — see inspectClassExpr's identifier
    // marker for the shadowing rationale (nested-helper parameter-frame fix).
    const marker = `${node.getSourceFile().fileName}#object:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const init = resolveIdentInit(node, ctx, next)
      if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToObjects(init, ctx, next, resolution)
    }
  }
  if (ts.isConditionalExpression(node)) return [...resolveToObjects(node.whenTrue, ctx, stack, resolution), ...resolveToObjects(node.whenFalse, ctx, stack, resolution)]
  if (ts.isBinaryExpression(node) && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken)) return [...resolveToObjects(node.left, ctx, stack, resolution), ...resolveToObjects(node.right, ctx, stack, resolution)]
  if (ts.isCallExpression(node)) {
    const marker = `${node.getSourceFile().fileName}#object-call:${node.pos}`
    if (!stack.has(marker)) {
      const next = new Set(stack).add(marker)
      const framed = withCalleeFrame(node, ctx, next, (collected) => ({
        collected,
        resolved: collected.flatMap(value => resolveToObjects(value, ctx, next, resolution)),
      }))
      if (framed?.collected?.length) return framed.resolved
    }
  }
  if (ts.isElementAccessExpression(node) || ts.isPropertyAccessExpression(node)) {
    const targets = resolveMemberTargets(node, ctx, stack, resolution)
    if (targets.length) return targets.flatMap(value => resolveToObjects(value, ctx, stack, resolution))
  }
  if (resolution) resolution.incomplete = true
  return []
}

function resolveToObject(node, ctx, stack) {
  node = unwrap(node)
  if (!node) return null
  if (ts.isObjectLiteralExpression(node)) return node
  if (ts.isIdentifier(node)) {
    const init = resolveIdentInit(node, ctx, stack)
    if (init && init !== LOOKUP_MISSING && init !== LOOKUP_UNBOUND) return resolveToObject(init, ctx, stack)
  }
  return null
}

function propertyInit(obj, name) {
  // Last match wins, matching real object-literal evaluation order (a later
  // property with the same key — including one written through a literal
  // computed key, `['color']` alongside `color:` — overrides an earlier
  // one). Required for the narrowed computed-key completeness checks below
  // (P11 review finding): once a computed key that resolves to a literal
  // name is allowed to participate in the proof at all, this lookup MUST
  // resolve it in the same order the language does, or a later override
  // (safe or unsafe) could silently be missed in either direction.
  let result = null
  for (const prop of obj.properties) {
    if (!ts.isPropertyAssignment(prop)) continue
    const key = propertyNameOf(prop.name)
    if (key === name) result = prop.initializer
  }
  return result
}

function isClassBuilderCall(node, ctx) {
  return CLASS_BUILDERS.has(resolveCalleeName(node.expression, ctx))
}

function resolveCalleeName(expr, ctx) {
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

function isIgnoredLiteral(node) {
  return (
    node.kind === ts.SyntaxKind.NullKeyword ||
    node.kind === ts.SyntaxKind.TrueKeyword ||
    node.kind === ts.SyntaxKind.FalseKeyword ||
    ts.isNumericLiteral(node)
  )
}

function isSkipIdent(name) {
  return name === 'undefined' || name === 'NaN' || name === 'Infinity'
}

function analyzeCssDeclarations(text, ctx, node) {
  const occurrence = { next: 0, prefix: 'css-decl' }
  for (const chunk of splitTopLevel(text, ';')) {
    const trimmed = chunk.trim()
    if (!trimmed) continue
    const colon = indexOfTopLevel(trimmed, ':')
    const value = colon === -1 ? trimmed : trimmed.slice(colon + 1)
    analyzeCssValue(value, ctx, node, occurrence)
  }
}

function analyzeCssText(text, ctx, node) {
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

function stripMatchingQuotes(text) {
  const trimmed = text.trim()
  if (
    trimmed.length >= 2
    && ((trimmed[0] === '"' && trimmed.endsWith('"')) || (trimmed[0] === "'" && trimmed.endsWith("'")))
  ) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

function decodePercentStrict(text) {
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

function decodeBase64Strict(text) {
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

function analyzeUrlFunction(fn, ctx, node) {
  const url = stripMatchingQuotes(fn.inner)
  if (!/^data:/i.test(url)) return
  analyzeDataUri(url, ctx, node)
}

function analyzeDataUri(url, ctx, node) {
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

function scanEmbeddedSvg(markup, ctx, originNode) {
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

function analyzeClassString(text, ctx, node) {
  for (const token of splitClassTokens(text)) analyzeClassToken(token, ctx, node)
}

function analyzeClassToken(token, ctx, node) {
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

function splitClassTokens(text) {
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

function lastVariantSegment(token) {
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

function stripImportant(utility) {
  let value = utility
  if (value.startsWith('!')) value = value.slice(1)
  if (value.endsWith('!')) value = value.slice(0, -1)
  return value
}

function isTailwindPaletteUtility(utility) {
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

function prefixedArbitrary(utility) {
  for (const prefix of TAILWIND_PREFIXES) {
    if (utility.startsWith(`${prefix}-[`) && utility.endsWith(']')) {
      return utility.slice(prefix.length + 2, -1)
    }
  }
  return null
}

function decodeTwArbitrary(value) {
  return value.replace(/_/g, ' ')
}

function analyzeCssValue(text, ctx, node, occurrence = { next: 0, prefix: 'css' }) {
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

function handleCssFunction(fn, ctx, node, occurrence) {
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

function handleCssIdent(ident, ctx, node, occurrence) {
  const lower = ident.toLowerCase()
  if (PERMITTED_KEYWORDS.has(lower) || SYSTEM_COLORS.has(lower)) return
  if (NAMED_COLORS.has(lower)) emitRaw(ctx, node, lower, undefined, `${occurrence.prefix}:${occurrence.next++}`)
}

function analyzeVar(inner, ctx, node, occurrence) {
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

function hasNumericColorChannels(inner) {
  let args = splitTopLevel(inner, ',')
  if (args.length === 1) args = splitTopLevel(inner, ' \t\n\r')
  return args.some((arg) => {
    const trimmed = arg.trim()
    if (!trimmed || /^var\(/i.test(trimmed)) return false
    return /^-?\d/.test(trimmed)
  })
}

function canonicalizeFunction(full) {
  return full.replace(/\s+/g, ' ').trim().replace(/^[A-Za-z-]+/, (name) => name.toLowerCase())
}

function startsWithWord(text, index, word) {
  if (text.slice(index, index + word.length).toLowerCase() !== word) return false
  const next = text[index + word.length]
  return !next || !/[A-Za-z0-9_-]/.test(next)
}

function readIdent(text, index) {
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

function readHex(text, index) {
  if (text[index] !== '#') return null
  let end = index + 1
  while (end < text.length && /[0-9a-fA-F]/.test(text[end])) end += 1
  const digits = end - index - 1
  if (digits === 3 || digits === 4 || digits === 6 || digits === 8) return text.slice(index, end)
  return null
}

function readFunction(text, start) {
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

function skipBalanced(text, openIndex) {
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

function skipQuoted(text, start) {
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

function splitTopLevel(text, separators) {
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

function indexOfTopLevel(text, separator) {
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

function emitRaw(ctx, node, syntax, message, occurrenceKey) {
  emit(ctx, node, {
    ruleId: 'ts-colors/raw-color',
    syntax,
    message: message ?? `Raw colour "${syntax}" is not a design-system token. Use var(--token) from the registered token set.`,
  }, occurrenceKey)
}

function emitUndefined(ctx, node, syntax, occurrenceKey) {
  emit(ctx, node, {
    ruleId: 'ts-colors/undefined-token',
    syntax,
    message: `CSS variable "${syntax}" is not in the registered token set.`,
  }, occurrenceKey)
}

function emitUnsupported(ctx, node) {
  emit(ctx, node, {
    ruleId: 'ts-colors/unsupported',
    syntax: printUnsupported(ctx.sourceFile, node),
    message: 'Unresolved dynamic colour expression; scanners must not treat this as safe.',
  })
}

function emitExtensionBoundary(ctx, node, binding) {
  const start = binding.declaration.getStart(ctx.sourceFile, false)
  const declaration = ctx.sourceFile.getLineAndCharacterOfPosition(start)
  emit(ctx, node, {
    ruleId: 'ts-colors/extension-boundary',
    syntax: `${binding.ownerName}#${binding.name}`,
    message: `Unchanged className parameter forwarded from its declaration at ${declaration.line + 1}:${declaration.character + 1}; this remains blocking until its exact extension boundary is centrally reviewed.`,
  })
}

function emitRuntimePaintBoundary(ctx, node, boundary) {
  const expression = printUnsupported(ctx.sourceFile, node)
  emit(ctx, node, {
    ruleId: 'ts-colors/extension-boundary',
    syntax: `${boundary.owner}#${boundary.receiver}.${boundary.property}<-${expression}`,
    message: `Runtime paint input for ${boundary.owner} ${boundary.receiver}.${boundary.property} remains blocking until this exact expression boundary is centrally reviewed.`,
  })
}

function emitUnverifiedGovernedValue(ctx, node, boundary) {
  const expression = printUnsupported(ctx.sourceFile, node)
  emit(ctx, node, {
    ruleId: 'ts-colors/unverified-governed-value',
    syntax: `${boundary.owner}#${boundary.receiver}<-${expression}`,
    message: `The value reaching ${boundary.owner} ${boundary.receiver} is not statically proven to use registered design-system tokens.`,
  })
}

function emitParse(ctx, diagnostic) {
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

function emit(ctx, node, partial, occurrenceKey = '') {
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

function printUnsupported(sourceFile, node) {
  if (ts.isSpreadAssignment(node) || ts.isSpreadElement(node)) {
    return `...${printCanonical(sourceFile, node.expression)}`
  }
  return printCanonical(sourceFile, node)
}

function printCanonical(sourceFile, node) {
  return printer.printNode(ts.EmitHint.Unspecified, node, sourceFile).replace(/\s+/g, ' ').trim()
}

function sortFindings(findings) {
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
