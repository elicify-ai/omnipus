#!/usr/bin/env node
// ts-colors/scope-and-jsx.mjs
//
// The module scan entry (walk), lexical scope/binding tracking
// (pushScope/bind/bindParams/bindPattern/lookup), and the JSX-attribute/
// inline-style inspection family. Part of a mutually recursive quartet with
// style-and-purity.mjs,
// binding-resolution.mjs and value-analysis.mjs (walk ultimately reaches
// scanEmbeddedSvg in value-analysis.mjs, which calls back into walk) --
// safe as a circular ESM import here because every module in the group is
// function-declarations-only with no top-level side effects: every
// binding is live and resolved by the time any function actually runs.
// Verified empirically by the full ts-colors test suite, not just
// asserted in this comment.

'use strict'

import ts from 'typescript'
import { extname, posix } from 'node:path'
import {
  analyzeCssText,
  emitUnsupported,
  isClassBuilderCall,
  isSkipIdent,
  resolveEnumerableArrayElements,
} from './value-analysis.mjs'
import {
  ZOD_SHAPE_METHODS,
  inspectClassBuilderCall,
  inspectClassExpr,
  inspectCssValueExpr,
  inspectSetPropertyCall,
  inspectStyleExpr,
  inspectStyleObject,
  isColorPropertyName,
  isNonPaintApiCall,
  propertyNameOf,
  zodChainRoot,
} from './style-and-purity.mjs'
import {
  composerForwardParameter,
  inspectTemplate,
  inspectZodChainLiterals,
  resolveIdentInit,
} from './binding-resolution.mjs'

export const LOOKUP_MISSING = Symbol('missing')

export const LOOKUP_UNBOUND = Symbol('unbound')

export const PARAMETER_BINDING = Symbol('parameter-binding')

export const IMPORT_BINDING = Symbol('import-binding')

export const MODULE_CACHES = new WeakMap()

export const CLASS_BUILDERS = new Set([
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
// site, not a real gap. Named explicitly, like
// CLASS_BUILDERS, rather than inferred structurally, so an unrelated
// same-named local helper is never accidentally trusted.
export const LOCAL_CLASS_COMPOSERS = new Set(['classes', 'statusDot'])

// Array methods whose result can never contain content absent from their
// receiver — `.join(sep)` stringifies exactly the receiver's own elements;
// `.filter(predicate)` (any predicate) only ever removes elements. Neither
// can manufacture a NEW, unproven value, so recursing into the receiver
// (rather than treating the call itself as an opaque, unresolvable value)
// does not weaken the proof — it is the same array, narrowed or stringified.
export const ARRAY_RECEIVER_PRESERVING_METHODS = new Set(['join', 'filter'])

// String whitespace-trimming methods (bullet 1, icon-button.tsx's
// `` `${sizes[size]} ${className ?? ''}`.trim() ``) — same "cannot
// manufacture a NEW, unproven value" reasoning as
// ARRAY_RECEIVER_PRESERVING_METHODS, one level narrower: `.trim()` /
// `.trimStart()` / `.trimEnd()` can only ever REMOVE leading/trailing
// whitespace characters from their string receiver, never introduce a new
// character (let alone a new class token) — recursing into the receiver
// template/string is exactly as sound as the existing join/filter handling.
export const STRING_TRIM_METHODS = new Set(['trim', 'trimStart', 'trimEnd'])

// Case-changing String methods (generated-token-accessor capability — see
// dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md bullet (a)): `.toUpperCase()` / `.toLowerCase()` can only
// ever change the CASE of characters already present in the receiver — same
// "cannot manufacture a NEW, unproven value" reasoning as STRING_TRIM_METHODS,
// one level narrower still. Deliberately excludes every OTHER String method
// (`.replace()`, `.concat()`, `.slice()`, …), which CAN introduce content
// absent from the receiver — see generatedTokenAccessorReturnIsGoverned,
// the only place this set is consulted: it is scoped to that one narrow
// recognizer, not wired into the general css-value dispatch (see that
// function's own doc comment for why).
export const STRING_CASE_METHODS = new Set(['toUpperCase', 'toLowerCase'])

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

export const CANONICAL_GENERATED_TOKEN_PATH_SET = new Set(CANONICAL_GENERATED_TOKEN_PATHS)

// Status-contract governed-record capability — see the
// isStatusContractColorRead doc comment below for the full design.
// `src/design-system/status.ts`'s own `statusContract` export and its
// `status(...)` builder function name, mirrored here exactly (never derived
// or guessed) so a differently-named look-alike export/builder can never
// satisfy this capability.
export const STATUS_CONTRACT_MODULE_PATH = 'src/design-system/status.ts'

export const STATUS_CONTRACT_EXPORT_NAME = 'statusContract'

export const STATUS_CONTRACT_BUILDER_NAME = 'status'

export const COLOR_FUNCS = new Set(['rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color'])

export const GROUP_FUNCS = new Set([
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

export const PERMITTED_KEYWORDS = new Set([
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

export const SYSTEM_COLORS = new Set([
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

export const NAMED_COLORS = new Set(
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

export const COLOR_PROPERTIES = new Set([
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

export const CANVAS_PAINT_PROPERTIES = new Set(['fillstyle', 'strokestyle', 'shadowcolor'])

export const TAILWIND_PREFIXES = [
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

export const TAILWIND_PALETTES = new Set([
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

export const printer = ts.createPrinter({
  removeComments: true,
  newLine: ts.NewLineKind.LineFeed,
})

export function prepareSource(filePath, source) {
  if (extname(filePath).toLowerCase() !== '.svg') return source
  return source
    .replace(/^\uFEFF/, '')
    .replace(/^\s*<\?xml\b[^?]*\?>\s*/i, '')
    .replace(/^\s*<!DOCTYPE\s+svg\b[^>]*>\s*/i, '')
}

export function scriptKindFor(filePath) {
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

export function tokenSet(policy) {
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

export function walk(node, ctx) {
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

export function governedModulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/') ? `src/${specifier.slice(2)}` : specifier.startsWith('.') ? posix.normalize(posix.join(posix.dirname(from), specifier)) : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

export function indexModuleRecord(path, sf) {
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

export function moduleRecord(ctx, path) {
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

export function resolveModuleDeclaration(ctx, fromPath, specifier, imported, seen = new Set()) {
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

export function bindImports(ctx, sf) {
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

export function bindingValue(value) {
  if (!value || typeof value !== 'object' || value.kind !== IMPORT_BINDING) return value
  if (value.external) return LOOKUP_UNBOUND
  const declaration = value.declaration
  if (ts.isVariableDeclaration(declaration)) return declaration.initializer ?? LOOKUP_UNBOUND
  return declaration
}

export function inspectNode(node, ctx) {
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
// argument to such a call — this does not touch the object literal's OWN
// nested values, spreads, or any other object literal found elsewhere in
// the walk.
//
// `z.object({...})` deliberately does NOT get this same whole-object skip:
// unlike an expect/vi fixture, a zod schema's object literal is real
// application data — `z.object({ color: z.string().default('#ff0000') })`
// must still have its `.default(...)` literal inspected. isNonPaintApiCall
// does not recognize a bare `z`-rooted call here at all, so this object
// literal is walked normally UNLESS isZodShapeArgument (below) recognizes
// it as a zod SHAPE object, whose keys are schema field names, not CSS
// properties.
export function isNonPaintApiObjectArgument(node, ctx) {
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
export function enclosingCallArgument(node) {
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
export function isZodShapeArgument(node, ctx) {
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
export function inspectZodShape(obj, ctx) {
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

export function isFunctionLike(node) {
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

export function pushScope(ctx) {
  ctx.scopes.push(new Map())
}

export function popScope(ctx) {
  ctx.scopes.pop()
}

export function bind(ctx, name, init) {
  const scope = ctx.scopes[ctx.scopes.length - 1]
  if (scope) scope.set(name, init)
}

export function bindParams(ctx, parameters, owner) {
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

export function bindParameterPattern(ctx, name, declaration, ownerName, composerForward = false, boundaryNameOverride = null, classOwner = null, memberOwnerName = null, arrayElements = null) {
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
    // parameter: a top-level or destructured parameter
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

export function parameterIsBoolean(name, declaration, ctx) {
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

export function typeHasBooleanProperty(type, name, ctx, seen = new Set()) {
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

export function functionIdentity(node) {
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
// `...ClassName` suffix), matching the committed typography scanner's
// isClassLikeParameterName exactly so both scanners cover the exact same
// forwarding shapes. This only ever WIDENS which identifiers are eligible
// for the existing unchanged-forwarding proof (still gated by the same
// reassignment/owner checks below); a false match degrades at worst to an
// extra blocking extension-boundary finding, never a silent pass.
export function isClassLikeParameterName(name) {
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
export function renderPropAssignment(fn) {
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
export function nearestNamedAncestorOwner(node) {
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
export function classForwardOwner(fn) {
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
export function receivingSymbol(node) {
  let current = node
  while (current && !isFunctionLike(current)) current = current.parent
  if (!current) return 'anonymous'
  const identity = functionIdentity(current)
  if (identity !== 'anonymous') return identity
  return nearestNamedAncestorOwner(current) ?? 'anonymous'
}

export function paintBoundary(node, receiver, property) {
  return { owner: receivingSymbol(node), receiver, property }
}

// React hook whose sole first argument is a callback the hook itself
// invokes later (an effect body) rather than something the CALLER'S render
// output depends on synchronously. Deliberately narrow: only the three
// hooks that share this exact "runs a side-effecting callback" contract —
// broadening to e.g. useMemo/useCallback would misattribute a value the
// render path actually depends on.
export const REACT_EFFECT_HOOKS = new Set(['useEffect', 'useLayoutEffect', 'useInsertionEffect'])

// True only when `fn` is passed as the literal first argument of a call to
// one of REACT_EFFECT_HOOKS — the exact shape `useEffect(() => {…})` /
// `useEffect(function () {…}, deps)` / `React.useEffect(() => {…})`.
// Anything indirect (the callback held in a variable first, a custom hook
// wrapping one of these) is not provable from the AST alone and returns
// null, same as any other unprovable shape in this file.
export function directHookCallbackName(fn) {
  const call = fn.parent
  if (!call || !ts.isCallExpression(call) || call.arguments[0] !== fn) return null
  const callee = unwrap(call.expression)
  if (ts.isIdentifier(callee) && REACT_EFFECT_HOOKS.has(callee.text)) return callee.text
  if (ts.isPropertyAccessExpression(callee) && REACT_EFFECT_HOOKS.has(callee.name.text)) return callee.name.text
  return null
}

// A `.style.setProperty(...)` call's owner identity.
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
export function setPropertyOwnerIdentity(call) {
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

export function setPropertyBoundary(call, prop) {
  return { owner: setPropertyOwnerIdentity(call), receiver: 'dom-style', property: prop }
}

// The one narrow, provable escape for an otherwise-unresolvable value
// reaching a `.style.setProperty(...)` sink: a custom property (`--*`)
// that is demonstrably NOT a recognized colour name.
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
export function isRuntimeMeasurementBoundary(boundary) {
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
export function isNonNullableCast(expr, identifierText) {
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
export function conditionNarrowsIdentifier(expr, name) {
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
export function isIdentifierGuarded(node, name) {
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
export function isProvenNonNullReceiver(node) {
  if (node.questionDotToken) return true
  const base = unwrap(node.expression)
  if (!ts.isIdentifier(base)) return false
  if (isNonNullableCast(node.expression, base.text)) return true
  return isIdentifierGuarded(node, base.text)
}

export function assignmentBoundary(node) {
  const left = unwrap(node.left)
  let property = 'unknown'
  if (ts.isPropertyAccessExpression(left)) property = left.name.text
  else if (ts.isElementAccessExpression(left)) {
    const key = unwrap(left.argumentExpression)
    if (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key)) property = key.text
  }
  return paintBoundary(node, isCanvasPaintName(property) ? 'canvas-paint' : 'dom-style', property)
}

export function isDirectRuntimeRead(node) {
  node = unwrap(node)
  if (ts.isIdentifier(node)) return true
  if (ts.isPropertyAccessExpression(node)) return isDirectRuntimeRead(node.expression)
  if (ts.isElementAccessExpression(node)) {
    const key = unwrap(node.argumentExpression)
    return (ts.isStringLiteral(key) || ts.isNoSubstitutionTemplateLiteral(key) || ts.isNumericLiteral(key)) && isDirectRuntimeRead(node.expression)
  }
  return false
}

export function isComponentWrapper(expression) {
  expression = unwrap(expression)
  if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
  if (ts.isPropertyAccessExpression(expression)) {
    return expression.name.text === 'forwardRef' || expression.name.text === 'memo'
  }
  return false
}

export function inspectParameterDefaults(ctx, parameters) {
  for (const parameter of parameters) {
    if (parameter.initializer && bindingContainsClassName(parameter.name)) {
      inspectClassExpr(parameter.initializer, ctx, new Set())
    }
    inspectBindingDefaults(ctx, parameter.name)
  }
}

export function inspectBindingDefaults(ctx, name) {
  if (!ts.isObjectBindingPattern(name) && !ts.isArrayBindingPattern(name)) return
  for (const element of name.elements) {
    if (!ts.isBindingElement(element)) continue
    if (element.initializer && bindingContainsClassName(element.name)) {
      inspectClassExpr(element.initializer, ctx, new Set())
    }
    inspectBindingDefaults(ctx, element.name)
  }
}

export function bindingContainsClassName(name) {
  if (ts.isIdentifier(name)) return name.text === 'className'
  if (!ts.isObjectBindingPattern(name) && !ts.isArrayBindingPattern(name)) return false
  return name.elements.some((element) => ts.isBindingElement(element) && bindingContainsClassName(element.name))
}

export function isParameterBinding(value) {
  return Boolean(value && typeof value === 'object' && value.kind === PARAMETER_BINDING)
}

export function markParameterReassignment(node, ctx) {
  const target = unwrap(node.left)
  if (!ts.isIdentifier(target)) return
  const binding = lookup(ctx, target.text)
  if (!isParameterBinding(binding)) return
  binding.reassigned = true
  inspectClassExpr(node.right, ctx, new Set())
}

export function bindPattern(ctx, name, init) {
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

export function lookup(ctx, name) {
  for (let index = ctx.scopes.length - 1; index >= 0; index -= 1) {
    if (ctx.scopes[index].has(name)) return ctx.scopes[index].get(name)
  }
  return LOOKUP_MISSING
}

export function unwrap(node) {
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

export function inspectJsxAttribute(attr, ctx) {
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

export function jsxAttrName(name) {
  if (ts.isIdentifier(name)) return name.text
  if (typeof ts.isJsxNamespacedName === 'function' && ts.isJsxNamespacedName(name)) {
    return `${name.namespace.text}-${name.name.text}`
  }
  return ''
}

export function isJsxColorAttr(name) {
  const normalized = name.toLowerCase().replace(/:/g, '-')
  if (normalized === 'fill' || normalized === 'stroke' || normalized === 'color') return true
  const compact = normalized.replace(/-/g, '')
  return compact === 'stopcolor' || compact === 'floodcolor' || compact === 'lightingcolor'
}

export function isHtmlStyleTag(node) {
  const tag = ts.isJsxElement(node)
    ? node.openingElement.tagName
    : ts.isJsxSelfClosingElement(node)
      ? node.tagName
      : null
  return Boolean(tag && ts.isIdentifier(tag) && tag.text === 'style')
}

export function inspectStyleElement(node, ctx) {
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

export function inspectCssTextExpr(node, ctx, stack) {
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

export function isCanvasPaintName(name) {
  return CANVAS_PAINT_PROPERTIES.has(name.replace(/-/g, '').toLowerCase())
}

export function isStyleReceiver(node) {
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

export function classifyPaintTarget(left) {
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

export function inspectPaintAssignment(node, ctx) {
  const kind = classifyPaintTarget(node.left)
  if (kind === 'css-value') inspectCssValueExpr(node.right, ctx, new Set(), assignmentBoundary(node))
  else if (kind === 'css-text') inspectCssTextExpr(node.right, ctx, new Set())
  else if (kind === 'unsupported') {
    emitUnsupported(ctx, node.left)
    inspectCssValueExpr(node.right, ctx, new Set())
  }
}
