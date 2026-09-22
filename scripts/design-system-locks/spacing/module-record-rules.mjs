#!/usr/bin/env node
// spacing/module-record-rules.mjs
//
// Cross-module import/export resolution (governedModulePath and friends)
// and the "provably never mutated/escaped" record-binding purity proofs
// (resolveExpr/recordBindingEscapes/recordExportedUsesSafe and friends).
// Depends on ast-utils.mjs only.

'use strict'

import ts from 'typescript'
import path from 'node:path'
import {
  UNRESOLVED_EXPR,
  findLexicalBinding,
  isAssignmentOperatorKind,
  isConstVariableDeclaration,
  isIncDecOperator,
  isPrimitiveLeafNode,
  isReassignedWithin,
  propertyInit,
  staticPropertyName,
  unwrap,
} from './ast-utils.mjs'

// Rebuilds the static string/numeric key path an occurrence's climbed
// property/element-access chain represents (`REC` -> `REC.a` -> `REC.a.b`
// yields `['a', 'b']`); null on any dynamic/computed segment (fail closed --
// callers treat null exactly like an unresolvable, non-primitive value).
export function chainPropertyPath(occurrence, climbed) {
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

export function structuralValueAtPath(root, path) {
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
export function dynamicTailEmbedIsSafe(occurrence, climbed, rootInitializer) {
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
export function recordDeclarationScope(declaration) {
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
export function recordLiteralRoot(initializer) {
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

export const RECORD_ESCAPE_CACHE = new WeakMap()

// Returns true when `name` escapes or is mutated anywhere in `scope` --
// false only when every occurrence is a provably safe read. `rootInitializer`
// is the object/array literal (or whatever resolveExpr already resolved the
// binding to) used to classify a container-escape's embedded value as a
// primitive leaf or not.
export function recordBindingEscapes(scope, name, rootInitializer, cacheKey) {
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

// Mirrors ts-colors.mjs::knownClassExportUsesSafe and its dynamic-import
// helper (dynamicImportProvenNotOrigin, ported below in full -- see its own
// comment ahead of computeRecordExportedUsesSafe for the template-literal-
// head proof). recordBindingEscapes above only ever walks ONE scope: the
// declaring module (for a same-file read) or the
// reading module (for an already-imported read). Neither walk looks at
// OTHER files in the `modules` context that import the SAME exported record
// and mutate it there -- an `export let`/`export const` record is reachable
// from any module via a named import, a namespace import, or a re-export.
// This function is that check: for an EXPORTED record declaration, walk every
// JS/TS module in `ctx.modules`, and for each one that imports from the
// declaring module, either recurse into recordBindingEscapes for a named
// import's own local binding (reusing the exact same escape/mutation proof
// already used for the direct-importer case), or fail closed outright for a
// namespace import, a re-export, or a dynamic import()/require() that could
// resolve to the origin -- none of those can be traced by this scanner. A
// declaration with no `export` modifier returns true immediately (nothing
// to check); a missing `modules` context on an exported declaration returns
// false (fail closed -- matches ts-colors' own ordering).
export const EXPORTED_RECORD_SAFE_CACHE = new WeakMap()

export function isJsModulePath(modulePath) {
  const ext = path.posix.extname(modulePath).toLowerCase()
  return ext === '.ts' || ext === '.tsx' || ext === '.js' || ext === '.jsx'
}

export function recordExportedUsesSafe(declaration, ctx, rootInitializer) {
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
export function dynamicImportProvenNotOrigin(modulePath, argument, origin) {
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
// error recovery: a broken JSX body preceding a later `import` statement can
// make that import vanish from BOTH the top-level statement list AND a full
// recursive `ts.forEachChild` walk -- the parser drops the tokens instead of
// attaching them to any recoverable node. Trusting the parsed AST of an
// already-broken file to prove it is IRRELEVANT to `origin` would therefore
// be unsound. Poisoning unconditionally on ANY parse error anywhere in the
// tree would instead make a single transiently-broken, wholly unrelated
// file fail every exported record's cross-module proof everywhere.
// Scanning the RAW TEXT instead of the AST is safe in
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
export const PLAIN_STRING_LITERAL_SOURCE = String.raw`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|\`(?:[^\`\\$]|\\.|\$(?!\{))*\``

export const PLAIN_STRING_LITERAL_RE_G = new RegExp(PLAIN_STRING_LITERAL_SOURCE, 'g')

export const DYNAMIC_IMPORT_CALL_RE = /\b(?:import|require)\s*\(\s*/g

export const PLAIN_STRING_LITERAL_ARG_RE = new RegExp(`^(?:${PLAIN_STRING_LITERAL_SOURCE})\\s*[,)]`)

export function unparseableModuleMightTargetOrigin(modulePath, source, origin, modules) {
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

export function computeRecordExportedUsesSafe(declaration, ctx, rootInitializer) {
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

export function resolveExpr(expr, ctx, seen = new Set()) {
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
    // left to the narrower isReassignedWithin gate: authenticatedBuilderAlias
    // (below) independently requires const-ness before trusting such an
    // alias, and multiple existing tests (`keeps a
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

export function governedModulePath(from, specifier, modules) {
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

export function moduleRecord(ctx, modulePath) {
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

export function resolveModuleExport(ctx, fromPath, specifier, exported, seen) {
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

export function importedDeclaration(identifier, ctx, seen) {
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

export function functionDeclarationFor(node, ctx) {
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

export function pureFunctionReturns(call, ctx) {
  const declaration = functionDeclarationFor(call.expression, ctx)
  if (!declaration?.body) return null
  if (!ts.isBlock(declaration.body)) return isPureStaticExpression(declaration.body) ? [declaration.body] : null
  if (declaration.body.statements.length === 1 && ts.isReturnStatement(declaration.body.statements[0]) && declaration.body.statements[0].expression) {
    return isPureStaticExpression(declaration.body.statements[0].expression) ? [declaration.body.statements[0].expression] : null
  }
  return collectGuardedLocalReturns(declaration.body)
}

// C2 gap 2: a locally-scoped "named-branch" class builder --
// ToolPolicyEditor.tsx's `BULK_BUTTON_CLASS(active, policy)` shape:
//   const base = '...'; const variantA = '...'; const variantB = '...'
//   if (!active) return base
//   if (policy === 'allow') return variantA
//   return variantB
// reached generically through the SAME pureFunctionReturns call every other
// opaque class-builder-argument call expression goes through -- not gated on
// a CLASS_BUILDERS name (BULK_BUTTON_CLASS carries none) and, unlike
// collectFiniteIfChainReturns (Item 5) above, NOT restricted to a top-level
// declaration: BULK_BUTTON_CLASS is declared inside the component body, and
// isTopLevelFiniteDispatcherFunction's restriction exists to rule out a
// dispatcher closing over CALLER-local mutable state it cannot see.
//
// That restriction is replaced here, not dropped, by a stricter LEXICAL one:
// every string this proof can ever produce is either a bare string/no-
// substitution-template literal sitting directly in a return statement, or a
// `const` this SAME function declared at the top of its own body with such a
// literal as its initializer. Deliberately narrower than isPureStaticExpression
// (used by the sibling single-statement case above): that check only rules out
// calls/new/await/yield/assignment inside an expression -- it does NOT rule out
// a bare identifier reference at all. A leading-const guard around a branch
// whose OTHER return is a bare reference to an outer, reassigned `let`
// would resolve cleanly with ZERO finding (not even for the reassigned
// off-scale value) once that identifier reaches the file's general
// identifier machinery -- a pre-existing resolveExpr blind spot this
// capability must not gain a route into.
// Restricting both the local consts' initializers and every return expression
// to string/template literal syntax makes that entirely moot: neither can
// EVER contain an identifier reference, so there is nothing to leak outward,
// resolved or not -- `const` also makes runtime reassignment between
// declaration and return impossible (enforced by the language, not this
// analysis).
//
// Fails closed (returns null -> the caller's existing unsupported finding)
// on: any statement that is not {a leading const, an if/return, the final
// return}; a leading declaration that is not `const`, has more than one
// declarator, destructures its name, or whose initializer is anything other
// than a string/no-substitution-template literal; an `if` with an `else`, a
// multi-statement block body, or a body with no return expression (both the
// braced `if (c) { return x }` and unbraced `if (c) return x` single-statement
// forms are accepted); a branch or final return expression that is neither a
// string/template literal nor a reference to one of this function's own
// leading consts; a body with no unconditional trailing return (so every path
// is provably exhaustive by construction, without needing to reason about the
// conditions themselves); a body that is only leading consts with no return
// at all.
export function collectGuardedLocalReturns(body) {
  const statements = body.statements
  const locals = new Map()
  let index = 0
  while (index < statements.length) {
    const statement = statements[index]
    if (!ts.isVariableStatement(statement)) break
    const { declarations } = statement.declarationList
    if (declarations.length !== 1) return null
    const [decl] = declarations
    if (!ts.isIdentifier(decl.name) || !decl.initializer) return null
    if (!isConstVariableDeclaration(decl) || !isLiteralClassString(decl.initializer)) return null
    locals.set(decl.name.text, decl.initializer)
    index += 1
  }
  if (index === statements.length) return null // no branch/trailing return to enumerate
  // Requiring at least one leading const keeps this shape disjoint from the
  // plain "bare if-chain returning literals directly" shape (no leading
  // locals at all) that collectFiniteIfChainReturns (Item 5) already owns
  // through its OWN, deliberately top-level-only proof -- MessageItem.tsx's
  // avatarStyle, including its pinned "a NESTED if-chain function stays
  // unsupported" regression sentinel (spacing-adversarial.test.mjs). Without
  // this, resolving the shape here first (pureFunctionReturns runs before
  // collectFiniteIfChainReturns in visitStyleLike) would silently widen that
  // deliberately-narrower proof to nested declarations too -- exactly the
  // "quietly widen a rule" failure mode this capability must not cause. Zero
  // leading consts steps out of the way entirely and lets that sentinel keep
  // its own unrelated, narrower guarantee.
  if (locals.size === 0) return null
  const resolveReturn = (expr) => {
    if (ts.isIdentifier(expr)) return locals.get(expr.text) ?? null
    return isLiteralClassString(expr) ? expr : null
  }
  const results = []
  for (; index < statements.length - 1; index += 1) {
    const statement = statements[index]
    if (!ts.isIfStatement(statement) || statement.elseStatement) return null
    const thenBody = statement.thenStatement
    const inner = ts.isBlock(thenBody) ? (thenBody.statements.length === 1 ? thenBody.statements[0] : null) : thenBody
    if (!inner || !ts.isReturnStatement(inner) || !inner.expression) return null
    const resolved = resolveReturn(inner.expression)
    if (!resolved) return null
    results.push(resolved)
  }
  const last = statements[statements.length - 1]
  if (!ts.isReturnStatement(last) || !last.expression) return null
  const resolvedLast = resolveReturn(last.expression)
  if (!resolvedLast) return null
  results.push(resolvedLast)
  return results
}

// A string or no-substitution-template literal carries no expressions and
// therefore no identifier references at all -- unlike isPureStaticExpression
// (which only rules out calls/new/await/yield/assignment, and would happily
// accept a template WITH a `${...}` substitution or a bare identifier), this
// is the strict "cannot possibly name anything outside itself" check
// collectGuardedLocalReturns needs so it can trust a local const's value, or
// a bare returned literal, without re-verifying anything downstream.
export function isLiteralClassString(node) {
  return ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)
}

export function isPureStaticExpression(node) {
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

export function isProvenBoolean(node, ctx, seen = new Set()) {
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

export function booleanParameter(identifier) {
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
