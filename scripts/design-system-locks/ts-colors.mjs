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

// String whitespace-trimming methods (bullet 1, icon-button.tsx's
// `` `${sizes[size]} ${className ?? ''}`.trim() ``) — same "cannot
// manufacture a NEW, unproven value" reasoning as
// ARRAY_RECEIVER_PRESERVING_METHODS, one level narrower: `.trim()` /
// `.trimStart()` / `.trimEnd()` can only ever REMOVE leading/trailing
// whitespace characters from their string receiver, never introduce a new
// character (let alone a new class token) — recursing into the receiver
// template/string is exactly as sound as the existing join/filter handling.
const STRING_TRIM_METHODS = new Set(['trim', 'trimStart', 'trimEnd'])

// Case-changing String methods (generated-token-accessor capability, lane R4
// — dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md bullet (a)): `.toUpperCase()` / `.toLowerCase()` can only
// ever change the CASE of characters already present in the receiver — same
// "cannot manufacture a NEW, unproven value" reasoning as STRING_TRIM_METHODS,
// one level narrower still. Deliberately excludes every OTHER String method
// (`.replace()`, `.concat()`, `.slice()`, …), which CAN introduce content
// absent from the receiver — see generatedTokenAccessorReturnIsGoverned,
// the only place this set is consulted: it is scoped to that one narrow
// recognizer, not wired into the general css-value dispatch (see that
// function's own doc comment for why).
const STRING_CASE_METHODS = new Set(['toUpperCase', 'toLowerCase'])

// Mirrors scripts/design-system-locks/audit.mjs::CANONICAL_GENERATED_TOKEN_PATHS
// EXACTLY — the audit's own authoritative allowlist of canonical generated-
// token output paths (repo-root-relative POSIX paths). Not imported directly:
// audit.mjs pulls in Ajv plus the baseline/ledger JSON schemas purely to run
// the audit CLI, a heavyweight dependency this scanner's hot path has no
// business acquiring just to read one constant, and audit.mjs dynamically
// imports every scanner (including this file) to run it — a static import
// back the other way would work today (audit.mjs finishes its own top-level
// evaluation before it ever dynamically imports a scanner) but ties this
// scanner's loadability to audit.mjs's own unrelated dependencies forever.
// Mirrored instead, with the mirror never silent:
// tests/design-system-locks/ts-colors.test.mjs imports BOTH lists and
// asserts they are identical, so any future drift between the audit's list
// and this one fails CI rather than silently narrowing or widening the
// governed-token-accessor gate below.
export const CANONICAL_GENERATED_TOKEN_PATHS = Object.freeze([
  'src/styles/tokens.generated.css',
  'src/styles/tokens.theme.generated.css',
  'src/design-system/tokens.ts',
])
const CANONICAL_GENERATED_TOKEN_PATH_SET = new Set(CANONICAL_GENERATED_TOKEN_PATHS)

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
    // scanHookStateLaundering's independent, silent scan of a useState
    // initializer/setter argument — never emit ts-colors/unsupported for
    // the opaque/runtime case there (the read-site boundary already covers
    // it); a raw literal still reports normally through analyzeCssValue,
    // which does not consult this flag at all.
    silentUnsupported: false,
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
    const specifier = statement.moduleSpecifier.text
    for (const element of bindings.elements) {
      const imported = element.propertyName?.text ?? element.name.text
      const declaration = resolveModuleDeclaration(ctx, ctx.path, specifier, imported)
      // A specifier ungoverned by governedModulePath (a bare/scoped package
      // like 'vitest' or 'zod', or a local path this scan's module set does
      // not carry) resolves to LOOKUP_UNBOUND — conservative/opaque for
      // every OTHER consumer of bindingValue, unchanged from before. But an
      // external-package IMPORT is still real, provable binding evidence:
      // isNonPaintApiCall/originatesFromNonPaintApi need the raw specifier
      // to tell a genuine `import { z } from 'zod'` apart from a same-named
      // local shadow (`const z = {...}`) — collapsing straight to the bare
      // LOOKUP_UNBOUND symbol erases exactly that evidence. Recording it as
      // an external IMPORT_BINDING and having bindingValue (below) fold it
      // back to LOOKUP_UNBOUND keeps every other call site byte-for-byte
      // identical while giving the paint-API recognizers something to
      // check.
      bind(ctx, element.name.text, declaration === LOOKUP_UNBOUND
        ? { kind: IMPORT_BINDING, external: true, specifier, imported }
        : { kind: IMPORT_BINDING, declaration })
    }
  }
}

function bindingValue(value) {
  if (!value || typeof value !== 'object' || value.kind !== IMPORT_BINDING) return value
  if (value.external) return LOOKUP_UNBOUND
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
    if (isZodShapeArgument(node, ctx)) inspectZodShape(node, ctx)
    else inspectStyleObject(node, ctx, new Set(), false)
  }
}

// The blanket "any color-shaped object literal, anywhere" walk above exists
// to catch a style object built separately from the JSX/DOM site that later
// applies it (an indirect construction inspectStyleExpr's own JSX-triggered
// walk cannot see). It has no way to tell that apart from an object literal
// that is never applied to anything — most visibly, a fixture object handed
// directly to a known non-paint API: a jest-dom/testing-library assertion
// (`expect(el).toHaveStyle({ color: … })`, matching the file's existing
// `expect` non-paint recognition below) or a Vitest mock return value
// (`vi.spyOn(...).mockReturnValue({ stroke: () => {} })` — `stroke` there is
// a Canvas 2D method name, not the SVG colour property it happens to share a
// name with). Neither can ever reach a real render; skip only the object
// literal that is itself (or is inside a single array wrapper of) a direct
// argument to such a call (P13 review finding) — this does not touch the
// object literal's OWN nested values, spreads, or any other object literal
// found elsewhere in the walk.
//
// `z.object({...})` deliberately does NOT get this same whole-object skip
// (FIX-S, 2026-09-20): unlike an expect/vi fixture, a zod schema's object
// literal is real application data — `z.object({ color: z.string()
// .default('#ff0000') })` must still have its `.default(...)` literal
// inspected. isNonPaintApiCall no longer recognizes a bare `z`-rooted call
// here at all, so this object literal is walked normally UNLESS
// isZodShapeArgument (below) recognizes it as a zod SHAPE object, whose
// keys are schema field names, not CSS properties.
function isNonPaintApiObjectArgument(node, ctx) {
  const call = enclosingCallArgument(node)
  return Boolean(call) && isNonPaintApiCall(call, ctx)
}

// Shared by isNonPaintApiObjectArgument and isZodShapeArgument: walks an
// object literal out through a cast/paren/non-null wrapper directly on it
// (`{…} as unknown as CanvasRenderingContext2D`, a common vitest
// `mockReturnValue(...)` shape) and, separately, through a single
// array-literal wrapper (`mockReturnValue([{…}])`), then returns the
// enclosing CallExpression if the (possibly-wrapped) node is one of its
// direct arguments — neither wrapper changes which call it is ultimately
// an argument to.
function enclosingCallArgument(node) {
  let expr = node
  while (expr.parent && (ts.isParenthesizedExpression(expr.parent) || ts.isAsExpression(expr.parent)
    || ts.isSatisfiesExpression(expr.parent) || ts.isNonNullExpression(expr.parent))) expr = expr.parent
  let parent = expr.parent
  if (parent && ts.isArrayLiteralExpression(parent)) {
    expr = parent
    while (expr.parent && (ts.isParenthesizedExpression(expr.parent) || ts.isAsExpression(expr.parent)
      || ts.isSatisfiesExpression(expr.parent) || ts.isNonNullExpression(expr.parent))) expr = expr.parent
    parent = expr.parent
  }
  return parent && ts.isCallExpression(parent) && parent.arguments.includes(expr) ? parent : null
}

// FIX-S round 2 (2026-09-20, lead addendum) — the object literal passed
// directly to a PROVEN zod shape-building method (object/strictObject/
// extend/merge/partial/pick/omit — ZOD_SHAPE_METHODS) is a SCHEMA SHAPE,
// not a style object: its keys are schema field names that can happen to
// share a name with a CSS colour property (`filter`, `color`, …) without
// meaning the CSS property at all, and its values are schemas or literal
// defaults, not CSS values. Root cause of the `VaultFilterNode.optional()`
// → ts-colors/unsupported regression this closes: `filter:` matched
// COLOR_PROPERTIES and got walked as a DOM style value. inspectZodShape
// (below) replaces inspectStyleObject for exactly this object literal.
function isZodShapeArgument(node, ctx) {
  const call = enclosingCallArgument(node)
  if (!call) return false
  const callee = unwrap(call.expression)
  if (!ts.isPropertyAccessExpression(callee) || !ZOD_SHAPE_METHODS.has(callee.name.text)) return false
  return zodChainRoot(call, ctx, new Set())
}

// For each property of a proven zod shape object: (a)/(b) a value that
// ITSELF proves zod-rooted (a direct chain like `z.string().default(...)`,
// OR a bare/called reference to a locally aliased schema constant like
// `VaultFilterNode` / `VaultFilterNode.optional()` — zodChainRoot treats
// both uniformly) gets the narrow literal-argument scan
// (inspectZodChainLiterals): its own literals are reported, an alias's
// literals are left to its own definition site, never re-proven or
// silently trusted here. (c) anything else — a direct string/template
// literal used as a value, a parameter, an unresolvable/non-zod
// identifier, a non-zod call — stays on the SAME conservative path a real
// style property would have taken (inspectCssValueExpr): a literal colour
// still reports ts-colors/raw-color, an unresolvable reference still
// reports ts-colors/unsupported (fail-closed, unchanged from before this
// shape/style split).
function inspectZodShape(obj, ctx) {
  for (const prop of obj.properties) {
    if (ts.isSpreadAssignment(prop)) continue
    let value
    let keyNode
    if (ts.isShorthandPropertyAssignment(prop)) {
      value = prop.name
      keyNode = prop.name
    } else if (ts.isPropertyAssignment(prop)) {
      value = prop.initializer
      keyNode = prop.name
    } else {
      continue
    }
    const key = propertyNameOf(keyNode)
    if (key === null) continue
    const unwrapped = unwrap(value)
    if (unwrapped && zodChainRoot(unwrapped, ctx, new Set())) {
      inspectZodChainLiterals(unwrapped, ctx)
      continue
    }
    if (isColorPropertyName(key)) inspectCssValueExpr(value, ctx, new Set(), paintBoundary(prop, 'dom-style', key))
  }
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
  // Computed ONCE per enclosing function (identical for every parameter of
  // `owner`), not per parameter: classOwner resolves the render-prop shape
  // (W3-colour port of typography's renderPropOwner) so a class-like
  // parameter forwarded through e.g. `classNames={{ Chevron: ({ className:
  // chevronClassName }) => ... }}` reports the ENCLOSING named component
  // ("Calendar"), not the render-prop's own property name ("Chevron"), with
  // the property folded into the boundary NAME instead (`Chevron.className`)
  // — see classForwardOwner. arrayElements is the W3-colour finite-const-
  // array-callback capability (bullet 3): when `owner` is itself the
  // callback argument of `<constArray>.map(...)` (optionally chained
  // through `.filter(...)`, which only ever narrows, never invents), every
  // element of that const array stands in the same evidentiary place as a
  // finite record's property values for the callback's OWN first
  // parameter — this is what lets `AVATAR_COLORS.map((color) => ...)`
  // resolve `color` to one of AVATAR_COLORS' own literal hex strings.
  const classOwner = classForwardOwner(owner)
  const memberOwnerName = classOwner && classOwner.ownerName !== 'anonymous' ? classOwner.ownerName : nearestNamedAncestorOwner(owner)
  const arrayElements = resolveEnumerableArrayElements(owner, ctx)
  parameters.forEach((parameter, index) => {
    // composerForwardParameter is only ever true for the TOP-LEVEL parameter
    // identifier itself (it requires ts.isIdentifier(parameter.name)), never
    // propagated into a destructured sub-pattern — an unrelated same-named
    // field of a destructured options object is never swept in.
    bindParameterPattern(ctx, parameter.name, parameter, ownerName, composerForwardParameter(parameter, owner), null, classOwner, memberOwnerName, index === 0 ? arrayElements : null)
  })
}

function bindParameterPattern(ctx, name, declaration, ownerName, composerForward = false, boundaryNameOverride = null, classOwner = null, memberOwnerName = null, arrayElements = null) {
  if (ts.isIdentifier(name)) {
    // classLikeName: the SOURCE contract name a class-like forward reports
    // (W3-colour port of typography's forwardedClassBoundary) — for a
    // top-level parameter this is just its own name (`triggerClassName`,
    // `widthClass`, ...); for a destructured rename (`{ className:
    // chevronClassName }`) the caller passes the SOURCE property name
    // (`className`) down as boundaryNameOverride, never the local alias —
    // the alias is an implementation detail, the source prop name is the
    // stable receiving identity the contract requires.
    const classLikeName = boundaryNameOverride ?? (isClassLikeParameterName(name.text) ? name.text : null)
    const forwardClassName = Boolean(classLikeName) || composerForward
    const boundaryLabel = classLikeName ?? name.text
    // forwardStyle mirrors forwardClassName's exact contract for a `style`
    // parameter (P4 review finding): a top-level or destructured parameter
    // literally named `style` is, by React/DOM convention, a CSSProperties
    // value the caller already owns — forwarded UNCHANGED into a `style={…}`
    // JSX attribute, never read or merged here. There is no composer
    // equivalent for style (composerForward is a className/cn-specific
    // concept — a rest/spread style composer would need its own contract,
    // not this one), so unlike forwardClassName this is never OR'd with
    // composerForward.
    bind(ctx, name.text, {
      kind: PARAMETER_BINDING,
      name: name.text,
      ownerName,
      declaration,
      reassigned: false,
      boolean: parameterIsBoolean(name.text, declaration, ctx),
      forwardClassName,
      forwardStyle: name.text === 'style',
      // classBoundaryOwner/classBoundaryName stand in for ownerName/name ONLY
      // on the extension-boundary emission path (emitExtensionBoundary),
      // leaving every OTHER consumer of ownerName/name (style forwarding,
      // the plain-className runtime-paint-boundary check) byte-for-byte
      // unchanged — this never widens what those other capabilities accept.
      classBoundaryOwner: forwardClassName ? (classOwner?.ownerName ?? ownerName) : null,
      classBoundaryName: forwardClassName ? (classOwner?.pathPrefix ? `${classOwner.pathPrefix}.${boundaryLabel}` : boundaryLabel) : null,
      // memberOwnerName backs classForwardMemberBoundary (`item.className`,
      // `iconProps?.className`): the nearest reviewable owner for a MEMBER
      // read off this exact parameter, regardless of whether the parameter's
      // OWN name is class-like (typography's parameterMemberBoundary
      // contract — ownerNameFor(fn) ?? nearestNamedAncestorOwner(fn)).
      memberOwnerName,
      arrayElements,
    })
    return
  }
  if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
    for (const element of name.elements) {
      if (!ts.isBindingElement(element)) continue
      let nextBoundaryName = null
      if (ts.isIdentifier(element.name)) {
        const property = element.propertyName ? propertyNameOf(element.propertyName) : element.name.text
        if (property && isClassLikeParameterName(property)) nextBoundaryName = property
      }
      // arrayElements is deliberately NOT propagated into a destructured
      // sub-pattern: it identifies "this bare identifier IS the whole
      // callback-array element" (`.map((color) => ...)`), which is false
      // for `.map(({ color }) => ...)` — there `color` is a PROPERTY read
      // off the element, a different (unhandled) shape, not the element
      // itself.
      bindParameterPattern(ctx, element.name, element, ownerName, false, nextBoundaryName, classOwner, memberOwnerName, null)
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

// A parameter/property name carries a whole CSS class value, not just the
// literal `className`/`class` (react-day-picker's `Chevron: ({ className:
// chevronClassName })`, sheet.tsx's `widthClass`, smart-select's
// `triggerClassName`, ...). Matched structurally (camelCase `...Class`/
// `...ClassName` suffix), ported verbatim from the committed typography
// scanner's isClassLikeParameterName so both scanners cover the exact same
// forwarding shapes. This only ever WIDENS which identifiers are eligible
// for the existing unchanged-forwarding proof (still gated by the same
// reassignment/owner checks below); a false match degrades at worst to an
// extra blocking extension-boundary finding, never a silent pass.
function isClassLikeParameterName(name) {
  return name === 'className' || name === 'class'
    || /(?:^|[a-z0-9])(?:ClassName|Class)$/.test(name)
}

// Detects the render-prop shape typography's renderPropOwner exists for:
// `fn` is the value of a NAMED (non-computed) property inside an object
// literal that is itself the direct expression of a JSX attribute
// (`classNames={{ Chevron: ({ className: chevronClassName }) => ... }}`).
// functionIdentity resolves THIS shape to the property name alone
// ("Chevron") — not a stable, independently reviewable owner — so a
// class-like parameter forwarded through such a value needs the ENCLOSING
// named component instead, with the render-prop's own property name kept as
// a path prefix on the boundary name.
function renderPropAssignment(fn) {
  const propertyAssignment = fn.parent
  if (!propertyAssignment || !ts.isPropertyAssignment(propertyAssignment) || propertyAssignment.initializer !== fn) return null
  if (ts.isComputedPropertyName(propertyAssignment.name)) return null
  const propertyName = propertyNameOf(propertyAssignment.name)
  if (!propertyName) return null
  const objectLiteral = propertyAssignment.parent
  if (!objectLiteral || !ts.isObjectLiteralExpression(objectLiteral)) return null
  const jsxExpression = objectLiteral.parent
  if (!jsxExpression || !ts.isJsxExpression(jsxExpression)) return null
  const jsxAttribute = jsxExpression.parent
  if (!jsxAttribute || !ts.isJsxAttribute(jsxAttribute)) return null
  return { jsxAttribute, propertyName }
}

// Climbs from `node` looking for the nearest ENCLOSING function-like ancestor
// with a resolvable (non-'anonymous') functionIdentity — the stable owner a
// render-prop value or an anonymous array-callback (`.map((item) => ...)`)
// falls back to when it has no name of its own (typography's
// parameterMemberBoundary: `ownerNameFor(fn) ?? nearestNamedAncestorOwner(fn)`).
function nearestNamedAncestorOwner(node) {
  let current = node.parent
  while (current) {
    if (isFunctionLike(current)) {
      const identity = functionIdentity(current)
      if (identity !== 'anonymous') return identity
    }
    current = current.parent
  }
  return null
}

// Resolves the class-forwarding owner for `fn` — the enclosing function a
// parameter of `fn` forwards a class-like value FROM, for emitExtensionBoundary
// purposes. Render-prop shapes are checked FIRST and override
// functionIdentity's own (too-narrow) PropertyAssignment resolution: without
// this, `Chevron: ({ className: chevronClassName }) => ...` would report
// owner "Chevron" (the render-prop's own key) instead of the enclosing
// "Calendar" component, diverging from typography's committed contract.
// Every other shape defers to functionIdentity unchanged — this never
// widens what functionIdentity already resolves elsewhere in this file
// (setPropertyOwnerIdentity, paintBoundary, ...), only ADDS the one render-
// prop override for THIS capability's own callers.
function classForwardOwner(fn) {
  const renderProp = renderPropAssignment(fn)
  if (renderProp) {
    const owner = nearestNamedAncestorOwner(renderProp.jsxAttribute)
    return { ownerName: owner ?? 'anonymous', pathPrefix: owner ? renderProp.propertyName : null }
  }
  return { ownerName: functionIdentity(fn), pathPrefix: null }
}

// R1 user-authored-colour capability: the immediate enclosing function is
// often itself anonymous even when a real, reviewable owner exists just
// outside it — a `.map()` render callback (`chatAgents.map((agent) => (...
// style={{backgroundColor: agent.color}} ...))`) or a `useMemo(() => {...
// color: selectedColor ...}, deps)` callback are both bodies that execute
// synchronously INSIDE their enclosing named component's own render, not
// independent owners of their own. `nearestNamedAncestorOwner` already
// exists for exactly this climb (parameter/render-prop ownership below) —
// reusing it here only WIDENS which paint-boundary reads get a real,
// reviewable owner name instead of falling back to the unregistrable
// `ts-colors/unsupported`; it never changes what is required to prove the
// VALUE itself is a runtime read (isDirectRuntimeRead/hookStateLocal/etc.
// still gate every emission site unchanged) — it only supplies a name for
// an owner that already, demonstrably, exists.
function receivingSymbol(node) {
  let current = node
  while (current && !isFunctionLike(current)) current = current.parent
  if (!current) return 'anonymous'
  const identity = functionIdentity(current)
  if (identity !== 'anonymous') return identity
  return nearestNamedAncestorOwner(current) ?? 'anonymous'
}

function paintBoundary(node, receiver, property) {
  return { owner: receivingSymbol(node), receiver, property }
}

// React hook whose sole first argument is a callback the hook itself
// invokes later (an effect body) rather than something the CALLER'S render
// output depends on synchronously. Deliberately narrow: only the three
// hooks that share this exact "runs a side-effecting callback" contract —
// broadening to e.g. useMemo/useCallback would misattribute a value the
// render path actually depends on.
const REACT_EFFECT_HOOKS = new Set(['useEffect', 'useLayoutEffect', 'useInsertionEffect'])

// True only when `fn` is passed as the literal first argument of a call to
// one of REACT_EFFECT_HOOKS — the exact shape `useEffect(() => {…})` /
// `useEffect(function () {…}, deps)` / `React.useEffect(() => {…})`.
// Anything indirect (the callback held in a variable first, a custom hook
// wrapping one of these) is not provable from the AST alone and returns
// null, same as any other unprovable shape in this file.
function directHookCallbackName(fn) {
  const call = fn.parent
  if (!call || !ts.isCallExpression(call) || call.arguments[0] !== fn) return null
  const callee = unwrap(call.expression)
  if (ts.isIdentifier(callee) && REACT_EFFECT_HOOKS.has(callee.text)) return callee.text
  if (ts.isPropertyAccessExpression(callee) && REACT_EFFECT_HOOKS.has(callee.name.text)) return callee.name.text
  return null
}

// A `.style.setProperty(...)` call's owner identity (P18/P6 review finding).
// functionIdentity already gives a stable, non-anonymous identity to any
// NAMED enclosing function — a local helper like an AppShell `applyMetrics`
// resolves through it unchanged, no different from any other paint sink.
// The one shape functionIdentity legitimately refuses is a truly anonymous
// function — most commonly a React effect callback with no name of its own.
// Only in that specific, provable case, derive a stable identity by
// combining the nearest NAMED enclosing function with the effect hook that
// scheduled it (`<namedAncestor>#<hookName>`, e.g. "ProfileSection#useEffect").
// Any other shape — anonymous with no hook wrapper, or no named ancestor to
// anchor to at all — returns 'anonymous', unchanged from today: the caller
// must keep treating the write as unsupported.
function setPropertyOwnerIdentity(call) {
  let node = call
  while (node && !isFunctionLike(node)) node = node.parent
  if (!node) return 'anonymous'
  const identity = functionIdentity(node)
  if (identity !== 'anonymous') return identity
  const hookName = directHookCallbackName(node)
  if (!hookName) return 'anonymous'
  let outer = node.parent
  while (outer && !isFunctionLike(outer)) outer = outer.parent
  if (!outer) return 'anonymous'
  const outerIdentity = functionIdentity(outer)
  return outerIdentity === 'anonymous' ? 'anonymous' : `${outerIdentity}#${hookName}`
}

function setPropertyBoundary(call, prop) {
  return { owner: setPropertyOwnerIdentity(call), receiver: 'dom-style', property: prop }
}

// The one narrow, provable escape for an otherwise-unresolvable value
// reaching a `.style.setProperty(...)` sink (P18/P6 review finding): a
// custom property (`--*`) that is demonstrably NOT a recognized colour name.
// isColorPropertyName gates every OTHER paint sink in this file before a
// boundary is even constructed (inspectStyleObject only builds one for a
// colour-named key) — setProperty is the one sink that treats EVERY custom
// property as conservatively paint-relevant, because it cannot know a
// consumer won't put a colour in it. A property whose own name proves it is
// not a colour (`--app-top`, `--user-font-size`) is a live
// measurement/adjustable-value carrier, not colour-enforcement debt; a
// property that DOES read as a colour (`--accent-color`) gets no exception
// here and keeps the ordinary unsupported/opaque-call treatment. Requires
// `owner` to be resolved too (never 'anonymous') — an anonymous owner still
// has no stable, reviewable identity to register.
function isRuntimeMeasurementBoundary(boundary) {
  return boundary.owner !== 'anonymous' && boundary.receiver === 'dom-style'
    && boundary.property.startsWith('--') && !isColorPropertyName(boundary.property)
}

// ── Bullet 2: null branches ─────────────────────────────────────────────

// True when `expr` (the RAW, not-yet-unwrapped receiver of a member/element
// access) is a `(x as NonNullable<typeof x>)` cast on the identifier named
// `identifierText` — LibraryEntryRow.tsx's exact idiom for proving a
// value non-null after an earlier conditional already established it.
// Checked on the raw expression (before this file's ordinary `unwrap()`,
// which strips AsExpression and would erase the signal) so the type
// annotation itself is still visible.
function isNonNullableCast(expr, identifierText) {
  let e = expr
  while (ts.isParenthesizedExpression(e)) e = e.expression
  if (!ts.isAsExpression(e)) return false
  const type = e.type
  if (!type || !ts.isTypeReferenceNode(type) || !ts.isIdentifier(type.typeName) || type.typeName.text !== 'NonNullable') return false
  const arg = type.typeArguments?.[0]
  if (!arg || !ts.isTypeQueryNode(arg)) return false
  return ts.isIdentifier(arg.exprName) && arg.exprName.text === identifierText
}

// True when `expr` narrows `name` to non-null/non-undefined — a bare
// truthy check on the identifier itself, or on ANY conjunct of an `&&`
// chain (`data && cfg` narrows `cfg` exactly as `cfg` alone would).
// Deliberately narrow to a PLAIN identifier conjunct — no `!= null` /
// `typeof` refinements — matching this scanner's existing "provable AST
// shape or fail closed" standard elsewhere in the file.
function conditionNarrowsIdentifier(expr, name) {
  expr = unwrap(expr)
  if (ts.isIdentifier(expr)) return expr.text === name
  if (ts.isBinaryExpression(expr) && expr.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) {
    return conditionNarrowsIdentifier(expr.left, name) || conditionNarrowsIdentifier(expr.right, name)
  }
  return false
}

// True when `node` sits lexically inside the truthy branch of a `name &&
// …` / `name ? … : …` / `if (name) { … }` guard — walked one direct
// parent-child hop at a time from `node` up to the source file root, so it
// finds a guard at ANY enclosing depth (e.g. a JSX `{strength && (
// <div>...<p>{strength.color}</p>...</div>)}` wrapper several levels above
// the actual read).
function isIdentifierGuarded(node, name) {
  let current = node
  let parent = node.parent
  while (parent) {
    if (ts.isBinaryExpression(parent) && parent.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken
      && parent.right === current && conditionNarrowsIdentifier(parent.left, name)) return true
    if (ts.isConditionalExpression(parent) && parent.whenTrue === current && conditionNarrowsIdentifier(parent.condition, name)) return true
    if (ts.isIfStatement(parent) && parent.thenStatement === current && conditionNarrowsIdentifier(parent.expression, name)) return true
    current = parent
    parent = parent.parent
  }
  return false
}

// True when a PropertyAccessExpression/ElementAccessExpression's receiver
// is PROVEN non-null/non-undefined at this exact read site — optional
// chaining on the access itself, a `NonNullable<typeof x>` cast, or a
// lexical `x &&` / `x ?` / `if (x)` guard enclosing it. This is the ONLY
// thing that permits resolveToObjects/resolveToArrays to drop a literal
// null/undefined candidate without voiding the whole read — see bullet 2.
function isProvenNonNullReceiver(node) {
  if (node.questionDotToken) return true
  const base = unwrap(node.expression)
  if (!ts.isIdentifier(base)) return false
  if (isNonNullableCast(node.expression, base.text)) return true
  return isIdentifierGuarded(node, base.text)
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
    // P18/P6 review finding: unlike every other paint sink in this file
    // (inspectStyleObject, inspectPaintAssignment, the JSX colour-attribute
    // branch), this call never constructed a boundary at all — so a
    // genuinely runtime custom-property write (a live layout measurement,
    // an adjustable root font size) had no way to ever become anything but
    // permanently-unsupported, even once fully proven unresolvable.
    inspectCssValueExpr(valueNode, ctx, new Set(), setPropertyBoundary(call, prop))
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
      // Mirror of inspectClassExpr's forwardClassName → emitExtensionBoundary
      // path (P4 review finding): a `style` parameter, UNCHANGED and passed
      // straight into a `style={…}` JSX attribute, is caller pass-through —
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
  // A shorthand property (`{ status, ... }`, equivalent to `{ status:
  // status, ... }`) names itself exactly as unambiguously as a plain
  // `key: value` PropertyAssignment — nothing about it is dynamic or
  // unprovable. Recognized here (bullet 3/4: BoardView's COLUMNS =
  // STATUS_ORDER.map((status) => ({ status, label: ..., headerColor: ...
  // })) uses this shape for its OWN map-parameter key) so it no longer
  // voids the whole-object completeness proof for an UNRELATED sibling key
  // (`headerColor`) being read — propertyInit is extended to match, so a
  // read that specifically targets the shorthand key itself still resolves
  // too, never silently drops it.
  if (ts.isShorthandPropertyAssignment(member)) return false
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
  if (ts.isCallExpression(node)) {
    if (isNonPaintApiCall(node, ctx)) return
    // FIX-S: a proven zod chain (`import { z } from 'zod'`) never gets the
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
          // narrow, provable exception (P18 review finding): a live
          // layout/geometry measurement surfaced ONLY through
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
      // covers P18/P6's non-colour custom-property shape; hookStateLocal is
      // the OTHER narrow, provable exception (P17/P6 review finding) for a
      // REAL colour property — `const [selectedColor] = useState(...)` is
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
    // Generated-token-accessor capability (lane R4) — see
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

// FIX-S (2026-09-20, confirmed-defect closure): `expect`/`vi`/`z` used to be
// trusted by BARE NAME, with no check of what the name is actually bound to
// — a same-named local (`const z = { object: (o) => o }`) or a same-named
// shadowing parameter hid colours exactly as effectively as the real
// import. isBoundTestNamespace below requires PROOF: the identifier is
// either never locally bound at all (LOOKUP_MISSING — the legitimate
// vitest-globals case, since this repo's vite.config.ts sets `test.globals
// = true` and ~650 real *.test.tsx files use `expect`/`vi` with no import
// line at all) or explicitly imported from the real package. A local
// declaration/shadow always binds to something other than LOOKUP_MISSING or
// a real external IMPORT_BINDING, so it can never satisfy this proof.
function isBoundTestNamespace(name, ctx) {
  const binding = lookup(ctx, name)
  if (binding === LOOKUP_MISSING) return true
  return Boolean(binding) && typeof binding === 'object' && binding.kind === IMPORT_BINDING
    && binding.external && (binding.specifier === 'vitest' || binding.specifier.startsWith('@vitest/'))
}

// zod's `z` has no ambient-global form (unlike vitest's globals) — every
// legitimate use is `import { z } from 'zod'` (or a `zod/*` subpath), so
// LOOKUP_MISSING never counts as proof here the way it does for
// isBoundTestNamespace.
function isZodImportBinding(binding) {
  return Boolean(binding) && typeof binding === 'object' && binding.kind === IMPORT_BINDING
    && binding.external && (binding.specifier === 'zod' || binding.specifier.startsWith('zod/'))
}

// FIX-S round 2 (2026-09-20, lead addendum): a call/identifier chain
// originates from zod when it is EITHER rooted directly at a proven `z`
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
// opaque, skip it" — that blanket trust was the FIX-S round 1 defect for a
// DIRECT `z` root, and would be exactly as unsound one alias hop away.
// `seen` guards a self-referential/mutually-aliased pair from recursing
// forever (VaultFilterNode's own definition legitimately references
// `VaultFilterNode` again inside `z.array(VaultFilterNode)`, but that
// reference lives in a CALL ARGUMENT this function never descends into, so
// the guard is a defensive backstop, not something the real shape relies
// on to terminate).
function zodChainRoot(node, ctx, seen) {
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
const ZOD_SHAPE_METHODS = new Set(['object', 'strictObject', 'extend', 'merge', 'partial', 'pick', 'omit'])

function isNonPaintApiCall(call, ctx) {
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
  // fixture/mock data standing in for a real API's shape (P13 review
  // finding — e.g. `vi.spyOn(Canvas...).mockReturnValue({ stroke: () => {} })`,
  // where `stroke` is the Canvas 2D method name, not an SVG colour property).
  // `expect` (including `expect.stringMatching(...)`, folded into this same
  // root check — see FIX-S evidence for why the old separate
  // `names.includes('stringMatching')` branch was unproven and redundant)
  // is the matching jest-dom/testing-library assertion namespace.
  //
  // `z` (zod) is deliberately NOT given this same blanket "the whole call
  // is opaque, skip it" trust — see FIX-S evidence for the soundness
  // argument. A zod schema's literal arguments (`.default('#ff0000')`) are
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

function originatesFromNonPaintApi(node, ctx, seen) {
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
function zodChainCalls(call) {
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
function isProvenZodChain(node, ctx) {
  return zodChainRoot(node, ctx, new Set())
}

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
function inspectZodChainLiterals(call, ctx) {
  for (const chainCall of zodChainCalls(call)) inspectLiteralColorArguments(chainCall, ctx)
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

function inspectTemplate(node, ctx, mode, stack, boundary = null) {
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

// The one narrow, provable exception for an ARRAY-destructured local (P17/P6
// review finding): `const [x] = useState(...)` / `React.useState(...)`.
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
function hookStateLocal(ident) {
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
function setterUsage(owner, setterName, declaredNameNode) {
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
function scanHookStateLaundering(state, ctx) {
  const wasSilent = ctx.silentUnsupported
  ctx.silentUnsupported = true
  try {
    scanStateValueArgument(state.initializer.arguments[0], ctx)
    for (const call of state.setterCalls) scanStateValueArgument(call.arguments[0], ctx)
  } finally {
    ctx.silentUnsupported = wasSilent
  }
}

function scanStateValueArgument(argument, ctx) {
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
function destructuredSourceParameterBinding(ident, ctx, stack) {
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
function bodyDestructuredClassBoundary(ident, ctx, stack) {
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

// css-mode template escape hatch for the finite-const-array-callback
// capability (bullet 3): `` `0 0 0 2px var(--color-primary), 0 0 0 4px
// ${color}` `` in AgentFormFields.tsx's AvatarColorPicker — `tryString`
// cannot give ONE static string for an array-element parameter (there are
// several candidate elements, by design), so this mirrors
// literalDestructuredTemplateTargets' exact contract for that different
// (array, not destructured-record) provenance: only literal string targets
// qualify, everything else falls through to the ordinary unresolved path.
function arrayElementTemplateTargets(expression, ctx, stack) {
  const node = unwrap(expression)
  if (!ts.isIdentifier(node)) return null
  const init = resolveIdentInit(node, ctx, stack)
  if (!isParameterBinding(init) || !init.arrayElements || init.reassigned) return null
  const targets = init.arrayElements.map(unwrap)
  return targets.every(isLiteralCssValue) ? targets : null
}

// css-mode template escape hatch for a plain LOCAL identifier span (P6
// review finding) — deliberately NEVER a parameter or an opaque call: this
// mirrors inspectCssValueExpr's own unresolved-identifier boundary check
// (same file, same rule), applied to a template SPAN instead of a bare
// value. Returns the span's own identifier node — for the caller to
// register as an exact, reviewable extension boundary — only when EVERY
// other avenue this file has for proving the span is exhausted: it is a
// bare identifier (never a nested expression — the "AST-normalized
// expression" identity requirement stays exact, one finding per site), it
// is not a parameter (a parameter/opaque call inside a template stays
// governed by the comment above, unchanged), and it does not resolve
// (LOOKUP_UNBOUND/MISSING, or a destructuring proof that could not
// complete) — with a KNOWN paint-sink boundary available. Returns null for
// anything else (no boundary, a parameter, an opaque call, or a value this
// file CAN still resolve), leaving the pre-existing behaviour untouched.
function runtimeBoundaryTemplateSpan(expression, ctx, stack, boundary) {
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
function resolveRuntimeTemplateSpanValue(expression, ctx, stack, boundary) {
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

// ── Generated-token-accessor capability (lane R4, 2026-09-20) ───────────────
//
// Closes the last ts-colors/unsupported finding in the tree:
// src/design-system/status.ts's `generatedColor()`, whose sole return
// (`value.toUpperCase()`) is unresolvable by the ordinary interprocedural
// walk — `.toUpperCase()`'s callee isn't a local declaration, so
// withCalleeFrame collects zero returns and inspectCssValueExpr's generic
// CallExpression branch falls to emitUnsupported. Root cause and the shape
// this recognizes are documented in full in
// dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md; this implementation refines that spec in two ways the
// R4 lane brief (2026-09-20) makes binding:
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
// Deliberately implemented as ONE atomic structural match at the CALL SITE
// (isGeneratedTokenAccessorCall), not as a composition of "recurse through
// .toUpperCase()" plus "recurse through the element access" as two
// independent, generally-wired capabilities. That composition was tried
// first and rejected: wiring `.toUpperCase()`/.trim()` as a general
// receiver-preserving case in inspectCssValueExpr (mirroring
// inspectClassExpr's existing STRING_TRIM_METHODS handling exactly) makes
// `generatedValues[id]` reachable as a plain ElementAccessExpression with a
// non-literal key — which resolveMemberTargets already handles today, via
// its pre-existing "unprovable key → every value of the resolved object is
// a candidate" fallback (the same fallback the R1 STATUS_VISUALS test at
// ts-colors.test.mjs exercises deliberately). Probed directly
// (dist/design-system-baseline/cli-lanes/fanout/R4/probes/baseline-probe.log):
// with only that general change, a NON-canonical, purely local finite
// object (`const LOCAL = {'color.a': '#112233'}; LOCAL[id]`) does NOT stay
// unsupported once reachable — it resolves to ts-colors/raw-color at each
// property, same as the canonical case would. That is sound in general (a
// module-const/finite-palette value must always resolve, never become an
// exception — R1 design note bullet 1) but it defeats this lane's own
// FORBIDDEN requirement that the non-canonical/local/parameter shapes stay
// unsupported. Matching the WHOLE call site atomically avoids the conflict
// entirely: every non-governed shape (wrong module, local object literal,
// bare parameter, an unrecognized String method, a mixed literal return)
// takes NO special action here and falls straight through, byte-for-byte
// unchanged, to the pre-existing withCalleeFrame/collectReturns handling
// immediately below — which for `<x>.toUpperCase()` was, is, and remains
// emitUnsupported when `<x>` doesn't resolve any other way. This is a pure
// ADDITION: every existing return path is reached exactly as before unless
// ALL of a matching function's return expressions independently prove
// governed.
function isGeneratedTokenAccessorCall(call, ctx, stack) {
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
function generatedTokenAccessorReturnIsGoverned(expr, declaration, ctx) {
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
function isGovernedTokenIdentifier(identNode, declaration, ctx) {
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
function findLocalConstDeclaration(body, name) {
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
function resolveGeneratedTokenModulePath(ident, ctx, seen = new Set()) {
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

// True for the literal `null` keyword or the `undefined` identifier — the
// two shapes a dispatcher/ternary/record candidate set can legitimately
// contribute NOTHING to a class/colour read (bullet 2, null branches), never
// any other unprovable value.
function isNullOrUndefinedLiteral(node) {
  return node.kind === ts.SyntaxKind.NullKeyword || (ts.isIdentifier(node) && node.text === 'undefined')
}

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
function resolveObjectKeysElementAccess(node, ctx, stack) {
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

function resolveMemberTargets(node, ctx, stack, resolution = null, nullAllowed = false) {
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

function resolveToArrays(node, ctx, stack, resolution = null, nullAllowed = false) {
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

function resolveToObjects(node, ctx, stack, resolution = null, nullAllowed = false) {
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

const ARRAY_ENUMERATION_METHODS = new Set(['map', 'forEach', 'find', 'some', 'every'])
// `.filter(predicate)` (ANY predicate) can only REMOVE elements from its
// receiver, never add one — the same reasoning ARRAY_RECEIVER_PRESERVING_METHODS
// already applies for a class-composing helper's own `.filter().join()`
// chain. Chaining through it before a `.map()`/`.forEach()`/... enumeration
// (`STATUS_OPTIONS.filter(pred).map((o) => ...)`) still leaves every
// resulting element a genuine member of the ORIGINAL array — a conservative
// superset of the filtered subset, safe to prove over in full.
const ARRAY_NARROWING_METHODS = new Set(['filter'])

// A never-reassigned, never-mutated, never-unsafely-exported `const` array
// literal — the same escape/mutation guard standard this file already
// applies to a finite record (knownClassExportUsesSafe), plus an array-
// specific mutating-method scan mirroring composerParameterIsMutated's
// (push/pop/splice/...) guard, generalized from a parameter name to a
// top-level declaration name.
function arrayLiteralIsStable(declaration, ctx) {
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
function resolveStableArrayLiteralElements(expr, ctx, stack) {
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
function resolveEnumerableArrayElements(fn, ctx) {
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
function resolveDerivedMapElements(node) {
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
function resolveObjectFromEntriesRecord(node, ctx, stack) {
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

function emitExtensionBoundary(ctx, node, binding) {
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
function classForwardMemberBoundary(node, ctx) {
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

function elementAccessStringKey(node) {
  if (!ts.isElementAccessExpression(node)) return null
  const key = unwrap(node.argumentExpression)
  return key && (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key)) ? key.text : null
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
