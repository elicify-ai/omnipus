#!/usr/bin/env node
// typography/module-and-purity.mjs
//
// Class-builder alias detection, cross-module import/export resolution
// (governedModulePath and friends), and the "provably never
// mutated/escaped" purity proofs (finite-return, absence-of-member,
// array-mutation, derived-value-escape) shared by the TS/TSX scan. Depends
// only on class-value-rules.mjs (CLASS_BUILDERS, scriptKindFor) — nothing
// here calls into ts-scan.mjs or css-scan.mjs, which is what keeps the
// module graph acyclic.

'use strict'

import ts from 'typescript'
import path from 'node:path'
import { CLASS_BUILDERS, scriptKindFor } from './class-value-rules.mjs'

// P10 precision fix: a CLASS_BUILDER's OWN definition — `export function
// cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }`
// (src/lib/utils.ts) — is not a live class value to prove. `visit()` walks
// EVERY call to a CLASS_BUILDERS-named function anywhere in the file, not
// just inside a className/cn() call site, so `cn`'s own body — which itself
// calls `twMerge`/`clsx`, both CLASS_BUILDERS members — was being walked as
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
// Anything else — extra statements, a differently-named receiver, a
// non-CLASS_BUILDER callee anywhere in the chain — is left exactly as
// unproven as before (falls through to the existing proofs / emitUnsupported).
export function classBuilderOwnParameterForward(identifier) {
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

// The stable name a function-like node is declared under — a function
// declaration's own name, or the identifier of a `const NAME = (...) => ...`
// / `const NAME = function (...) {...}` it is the initializer of. Anything
// else (a method, an inline callback, an unnamed export default) is not a
// recognizable CLASS_BUILDERS declaration and returns null.
export function classBuilderDeclarationName(fn) {
  if (ts.isFunctionDeclaration(fn)) return fn.name ? fn.name.text : null
  const parent = fn.parent
  return parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name) && parent.initializer === fn
    ? parent.name.text
    : null
}

// A function-like node's body reduced to its single meaningful expression:
// a block whose only statement is `return <expr>`, or an arrow function's
// direct expression body. Any other shape (multiple statements, no return,
// a non-expression return) is not provably transparent and returns null.
export function classBuilderSingleReturnExpression(fn) {
  if (!fn.body) return null
  if (!ts.isBlock(fn.body)) return fn.body
  if (fn.body.statements.length !== 1) return null
  const statement = fn.body.statements[0]
  return ts.isReturnStatement(statement) && statement.expression ? statement.expression : null
}

// True when `expression` — after unwrapping parens/as/satisfies/non-null —
// is either the bare parameter identifier itself, or a call to a
// CLASS_BUILDERS-named function where at least one argument recursively
// forwards it the same way. `seen` guards against a call chain that somehow
// revisits the same node. Deliberately NOT extended to a spread argument
// (`clsx(...inputs)`): the general class-expression walker has no
// SpreadElement case at all (falls to its default emitUnsupported before
// this proof is ever consulted for one), so claiming spread support here
// would be dead code proving something the walker cannot reach — cn()'s
// real shape (src/lib/utils.ts) passes the whole array (`clsx(inputs)`),
// never a spread.
export function classBuilderChainForwardsParameter(expression, parameterName, seen) {
  const unwrapped = unwrapStatic(expression)
  if (!unwrapped || seen.has(unwrapped)) return false
  seen.add(unwrapped)
  if (ts.isIdentifier(unwrapped)) return unwrapped.text === parameterName
  if (!ts.isCallExpression(unwrapped) || !ts.isIdentifier(unwrapped.expression) || !CLASS_BUILDERS.has(unwrapped.expression.text)) return false
  return unwrapped.arguments.some((argument) => classBuilderChainForwardsParameter(argument, parameterName, seen))
}

export function governedModulePath(from, specifier, modules) {
  if (!modules || typeof modules !== 'object') return null
  const base = specifier.startsWith('@/')
    ? `src/${specifier.slice(2)}`
    : specifier.startsWith('.') ? path.posix.normalize(path.posix.join(path.posix.dirname(from), specifier)) : null
  if (!base) return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, `${base}.jsx`, `${base}.mjs`, `${base}.cjs`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (Object.hasOwn(modules, candidate)) return candidate
  }
  return null
}

// True when a plain same-named variable declaration (const/let/var, any
// identifier binding) sits in a block lexically between `identifier` and its
// enclosing function — such a declaration owns the sink binding, so a
// forwarded-parameter or body-destructured provenance claim cannot hold.
export function plainDeclarationShadows(identifier, fn) {
  let scope = identifier.parent
  while (scope && scope !== fn) {
    if (ts.isBlock(scope)) {
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement)) continue
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === identifier.text) return true
        }
      }
    }
    scope = scope.parent
  }
  return false
}

export function findLexicalBinding(identifier, name) {
  let scope = identifier.parent
  while (scope) {
    if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
      const matches = []
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === name) matches.push(declaration)
        }
      }
      if (matches.length > 1) return null
      if (matches.length === 1) return { declaration: matches[0], initializer: matches[0].initializer ?? null }
    }
    if (ts.isFunctionLike(scope)) {
      for (const parameter of scope.parameters) {
        if (ts.isIdentifier(parameter.name) && parameter.name.text === name) return { declaration: parameter, initializer: parameter.initializer ?? null }
      }
    }
    scope = scope.parent
  }
  return null
}

export function hasMultipleVariableDeclarations(sourceFile, name) {
  let count = 0
  function visit(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name) count += 1
    if (count < 2) node.forEachChild(visit)
  }
  visit(sourceFile)
  return count > 1
}

export function moduleRecord(context, modulePath) {
  if (!context?.modules) return null
  if (context.moduleCache.has(modulePath)) return context.moduleCache.get(modulePath)
  const source = context.modules[modulePath]
  if (typeof source !== 'string') return null
  const sourceFile = ts.createSourceFile(modulePath, source, ts.ScriptTarget.ES2022, true, scriptKindFor(modulePath))
  const record = { sourceFile, exports: new Map(), imports: new Map(), reexports: new Map(), valid: !(sourceFile.parseDiagnostics ?? []).some((item) => item.category === ts.DiagnosticCategory.Error) }
  context.moduleCache.set(modulePath, record)
  for (const statement of sourceFile.statements) {
    const exported = statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)
    if (exported && ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) record.exports.set(declaration.name.text, declaration)
    } else if (exported && ts.isFunctionDeclaration(statement) && statement.name) {
      record.exports.set(statement.name.text, statement)
    } else if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const named = statement.importClause?.namedBindings
      if (named && ts.isNamedImports(named)) for (const element of named.elements) record.imports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    } else if (ts.isExportDeclaration(statement) && statement.exportClause && ts.isNamedExports(statement.exportClause) && statement.moduleSpecifier && ts.isStringLiteral(statement.moduleSpecifier)) {
      for (const element of statement.exportClause.elements) record.reexports.set(element.name.text, { specifier: statement.moduleSpecifier.text, imported: element.propertyName?.text ?? element.name.text })
    }
  }
  return record
}

export function resolveModuleExport(context, fromPath, specifier, exported, seen) {
  const target = governedModulePath(fromPath, specifier, context?.modules)
  if (!target) return null
  const marker = `${target}#${exported}`
  if (seen.has(marker)) return null
  const nextSeen = new Set(seen).add(marker)
  const record = moduleRecord(context, target)
  if (!record?.valid) return null
  if (record.exports.has(exported)) return record.exports.get(exported)
  const forwarded = record.reexports.get(exported) ?? record.imports.get(exported)
  return forwarded ? resolveModuleExport(context, target, forwarded.specifier, forwarded.imported, nextSeen) : null
}

export function importedDeclaration(identifier, context) {
  const sourceFile = identifier.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const named = statement.importClause?.namedBindings
    if (!named || !ts.isNamedImports(named)) continue
    for (const element of named.elements) if (element.name.text === identifier.text) {
      return resolveModuleExport(context, sourceFile.fileName, statement.moduleSpecifier.text, element.propertyName?.text ?? element.name.text, new Set())
    }
  }
  return null
}

// Only a JS/TS module can contain import/export/require syntax relevant to
// exportNeverMutatedByImporters' cross-module proof below (parity with
// ts-colors.mjs's isJsModulePath) — an asset file (`.svg`, `.css`, …)
// present in the governed `modules` map is irrelevant and must not be
// treated as an (unparseable) importer candidate.
export function isGovernedModulePath(modulePath) {
  const lower = modulePath.toLowerCase()
  return lower.endsWith('.ts') || lower.endsWith('.tsx') || lower.endsWith('.js') || lower.endsWith('.jsx')
    || lower.endsWith('.mts') || lower.endsWith('.cts') || lower.endsWith('.mjs') || lower.endsWith('.cjs')
}

// A template-literal dynamic import()/require() argument's HEAD is always
// the literal prefix of whatever string it evaluates to at runtime — a
// substitution can only append characters after it, never rewrite or erase
// them. governedModulePath only ever resolves a specifier that itself
// starts with '@/' or a relative '.' form. If the head cannot possibly grow
// into one of those two forms, no runtime value of the template can EVER be
// a governedModulePath-resolvable specifier at all — proven impossible, not
// guessed — so it can never target `origin`. When the head IS shaped like a
// local specifier, it must additionally share `origin`'s directory (and,
// lacking a trailing '/', `origin`'s basename must share the head's own
// final segment as a prefix), or it is still a provably different target.
// Any other argument shape stays exactly as conservative as before: not
// excluded. Ported verbatim from ts-colors.mjs's dynamicImportProvenNotOrigin
// (parity, Finding 2 fix round 2) — without this, a SINGLE dynamic
// import()/require() anywhere in the ENTIRE governed tree (e.g. a test
// file's `await import('@/store/ui')`, wholly unrelated to the record being
// proven) poisoned exportNeverMutatedByImporters for EVERY exported record
// program-wide, not just ones the dynamic import could plausibly reach —
// found via a real-tree audit run flagging src/components/workspaces/
// taskStatusConfig.ts's STATUS_BADGE (genuinely never mutated anywhere)
// solely because an unrelated test file contained an unrelated dynamic
// import.
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

// LEAD DECISION (Finding 2 fix, round 2 — parity with ts-colors.mjs's
// knownClassExportUsesSafe): an exported record's declaring module cannot
// see a member write made by ANY OTHER module that imports it — `import {
// SIZES } from './sizes'; SIZES.small = 'text-[10px]'` mutates SIZES from a
// different `ts.SourceFile` than the one absenceBindingUsesSafe on the
// EXPORTING declaration can ever walk. Scan every module supplied in the
// governed `modules` context for a static import of this export; each
// static named import must itself pass absenceBindingUsesSafe inside the
// IMPORTING file's own scope. A default/namespace import sharing the same
// import statement, a type-only re-export that is not itself type-only, a
// dynamic `import()`/`require()` PROVABLY reaching this origin module, or a
// missing/unparseable module all fail closed (return false) rather than
// assume safety — a dynamic import PROVABLY reaching some OTHER module is
// not this record's concern (dynamicImportProvenNotOrigin above) and must
// not poison every unrelated exported record in the governed tree.
// `containers`, when supplied, is the record value(s) `declaration` is
// already known (by the caller) to structurally hold — threaded through so
// EACH importer's specifier binding also passes the round-3 derived-value
// escape proof (parity with ts-colors.mjs's knownClassExportUsesSafe, which
// pairs absenceBindingUsesSafe with knownClassDerivedUsesSafe per importer,
// not just on the exporting declaration): a nested value read off a
// never-reassigned import specifier can escape into a container/call/
// return/reassignment in the IMPORTER's own scope exactly as it can in the
// exporting module's scope.
export function exportNeverMutatedByImporters(declaration, context, containers = null, usesSafeCheck = null) {
  const usesSafe = usesSafeCheck ?? ((specifier) => absenceBindingUsesSafe(specifier, false, true))
  const statement = declaration.parent?.parent
  if (!statement || !ts.isVariableStatement(statement)) return false
  if (!statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) return true
  if (!context?.modules || !ts.isIdentifier(declaration.name)) return false
  const origin = declaration.getSourceFile().fileName
  for (const modulePath of Object.keys(context.modules)) {
    if (!isGovernedModulePath(modulePath)) continue
    const record = moduleRecord(context, modulePath)
    if (!record || !record.valid) return false
    for (const item of record.sourceFile.statements) {
      if ((!ts.isImportDeclaration(item) && !ts.isExportDeclaration(item)) || !item.moduleSpecifier || !ts.isStringLiteral(item.moduleSpecifier)) continue
      if (governedModulePath(modulePath, item.moduleSpecifier.text, context.modules) !== origin) continue
      if (ts.isExportDeclaration(item)) {
        if (!item.isTypeOnly) return false
        continue
      }
      const clause = item.importClause
      if (!clause || clause.isTypeOnly) continue
      const named = clause.namedBindings
      if (clause.name || (named && !ts.isNamedImports(named))) return false
      if (named) for (const specifier of named.elements) {
        if (specifier.isTypeOnly || (specifier.propertyName?.text ?? specifier.name.text) !== declaration.name.text) continue
        if (!usesSafe(specifier) || (containers && !derivedValueEscapeSafe(specifier, containers))) return false
      }
    }
    let dynamic = false
    const visitDynamic = (node) => {
      if (dynamic) return
      if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword
        || (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
        const argument = node.arguments[0]
        if (!argument) dynamic = true
        else if (ts.isStringLiteralLike(argument)) {
          if (governedModulePath(modulePath, argument.text, context.modules) === origin) dynamic = true
        } else if (!dynamicImportProvenNotOrigin(modulePath, argument, origin)) dynamic = true
        if (dynamic) return
      }
      node.forEachChild(visitDynamic)
    }
    visitDynamic(record.sourceFile)
    if (dynamic) return false
  }
  return true
}

export function objectProperty(object, key) {
  for (const property of object.properties) {
    if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) continue
    const name = property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) || ts.isNumericLiteral(property.name)) ? property.name.text : null
    if (name === key) return ts.isPropertyAssignment(property) ? property.initializer : property.name
  }
  return null
}

export function unwrapStatic(node) {
  let current = node
  while (current && (ts.isParenthesizedExpression(current) || ts.isAsExpression(current) || ts.isSatisfiesExpression(current) || ts.isNonNullExpression(current) || current.kind === ts.SyntaxKind.TypeAssertionExpression)) current = current.expression
  return current
}

export function resolveImportedExpression(node, context, seen = new Set()) {
  const expression = unwrapStatic(node)
  if (!expression || !context?.modules) return null
  if (ts.isIdentifier(expression)) {
    const declaration = importedDeclaration(expression, context)
    if (!declaration || seen.has(declaration)) return null
    seen.add(declaration)
    // LEAD DECISION (Finding 2 fix, round 2): this was the un-guarded
    // fallback path — no const check, no same-module mutation check, no
    // cross-module mutation check — that let `import { SIZES } from
    // './sizes'; SIZES.small = 'text-[10px]'` resolve to the stale
    // exporter-time literal with zero findings. `const`-only is required
    // unconditionally (a `let` export can be reassigned wholesale by its
    // OWN module regardless of member shape).
    if (!ts.isVariableDeclaration(declaration) || !declaration.initializer
      || !(declaration.parent.flags & ts.NodeFlags.Const)) return null
    const initializer = unwrapStatic(declaration.initializer)
    // The never-mutated MEMBER proof (absenceBindingUsesSafe +
    // exportNeverMutatedByImporters) only matters when the resolved value
    // is a RECORD (object/array literal) whose own members could later be
    // written out from under this resolution — that IS Finding 2's exploit
    // shape. For a plain primitive export (a string/template built by
    // concatenation — e.g. LibraryPreviewPane.tsx's `LIBRARY_ICON_BTN`,
    // referenced directly as `className={LIBRARY_ICON_BTN}` — the far MORE
    // common shape), `absenceBindingUsesSafe`'s actual contract ("every
    // reference is a property/element access") is the WRONG question: a
    // `const` primitive cannot be reassigned by the language itself and has
    // no mutable members, so demanding every reference be `.prop`-shaped
    // wrongly rejected the entirely normal, safe pattern of referencing the
    // constant directly — a real regression found via a real-tree audit run
    // (`LIBRARY_ICON_BTN`/`LINK_CLASS`/`priorityBadge.className`-shaped
    // constants newly, wrongly flagged unsupported).
    const isRecordLiteral = initializer && (ts.isObjectLiteralExpression(initializer) || ts.isArrayLiteralExpression(initializer))
    // Round 3: derived-value escape proof, same as resolveRecordObjectLiteral
    // — required alongside the never-mutated proofs above whenever the
    // resolved value is a record whose nested members could carry a live
    // reference out through a container/call/return/reassignment elsewhere
    // in this (exporting) module's scope.
    if (isRecordLiteral && (!absenceBindingUsesSafe(declaration, false, true) || !exportNeverMutatedByImporters(declaration, context, [initializer])
      || !derivedValueEscapeSafe(declaration, [initializer]))) return null
    return resolveImportedExpression(initializer, context, seen) ?? initializer
  }
  if (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression)) {
    const key = ts.isPropertyAccessExpression(expression) ? expression.name.text
      : expression.argumentExpression && (ts.isStringLiteral(expression.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(expression.argumentExpression)) ? expression.argumentExpression.text : null
    if (key === null) return null
    const owner = resolveImportedExpression(expression.expression, context, seen)
    const unwrappedOwner = unwrapStatic(owner)
    return unwrappedOwner && ts.isObjectLiteralExpression(unwrappedOwner) ? objectProperty(unwrappedOwner, key) : null
  }
  if (ts.isCallExpression(expression) && ts.isPropertyAccessExpression(expression.expression) && ts.isIdentifier(expression.expression.expression)
    && expression.expression.expression.text === 'Object' && ['freeze', 'seal', 'preventExtensions'].includes(expression.expression.name.text)) {
    return expression.arguments[0] ? resolveImportedExpression(expression.arguments[0], context, seen) ?? unwrapStatic(expression.arguments[0]) : null
  }
  return null
}

export function functionDeclarationFor(call, context) {
  const callee = unwrapStatic(call.expression)
  if (!callee || !ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)?.initializer
  const localFunction = unwrapStatic(local)
  if (localFunction && (ts.isArrowFunction(localFunction) || ts.isFunctionExpression(localFunction))) return localFunction
  const imported = importedDeclaration(callee, context)
  return imported && (ts.isFunctionDeclaration(imported) || ts.isArrowFunction(imported) || ts.isFunctionExpression(imported)) ? imported : null
}

export function authenticCvaImport(expression) {
  const callee = unwrapStatic(expression)
  if (!callee || !ts.isIdentifier(callee)) return false
  const sourceFile = callee.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)
      || statement.moduleSpecifier.text !== 'class-variance-authority') continue
    const named = statement.importClause?.namedBindings
    if (!named || !ts.isNamedImports(named)) continue
    if (named.elements.some((element) => element.name.text === callee.text && (element.propertyName?.text ?? element.name.text) === 'cva')) return true
  }
  return false
}

export function cvaDefinitionFor(expression, context) {
  const callee = unwrapStatic(expression)
  if (!callee || !ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)
  const declaration = local?.declaration ?? importedDeclaration(callee, context)
  if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer) return null
  const initializer = unwrapStatic(declaration.initializer)
  return initializer && ts.isCallExpression(initializer) && authenticCvaImport(initializer.expression) ? initializer : null
}

// FIX-P2 (2026-09-20, confirmed-defect closure): CLASS_BUILDERS names were
// dispatched purely by IDENTIFIER TEXT with no check of what the name is
// actually bound to in the file being scanned — a local `function cn() {
// return 'always-safe' }` (or a clsx/cva/twMerge/classnames/classNames
// look-alike) was trusted exactly as the real utility and could hide
// arbitrary class content behind it. Parity with ts-colors.mjs's
// isBoundTestNamespace/isZodImportBinding (FIX-S): a name with NO competing
// LOCAL declaration anywhere reachable from the call site — the
// overwhelmingly common real shape, `import { cn } from '@/lib/utils'` used
// two lines below, or simply omitted from a narrow test fixture — is
// trusted exactly like a genuine import (nothing contradicts it; imports
// are never tracked as a "local declaration" here and cannot coexist with a
// same-named local declaration in valid TS/JS anyway). Only a name that IS
// locally bound to something else in this same file must additionally prove
// itself a transparent single-parameter forward into a real CLASS_BUILDERS
// chain (cn's own src/lib/utils.ts definition shape, P10) before it is
// trusted; anything else — a different arity, a non-forwarding body, a
// constant return, an arbitrary caller-supplied parameter of the same name
// — falls through to the generic analysis exactly as an unauthenticated
// call already does (never silently trusted, never silently dropped).
export function authenticClassBuilderCallee(identifier, ctx) {
  if (!ts.isIdentifier(identifier)) return false
  if (authenticClassBuilderImport(identifier)) return true
  if (!hasCompetingLocalBinding(identifier, identifier.text, ctx)) return true
  return transparentClassBuilderAlias(identifier, identifier.text, ctx)
}

// A real import authenticates its CLASS_BUILDERS-named local binding: `cva`
// from 'class-variance-authority' (authenticCvaImport, already proven and
// reused verbatim — never a second mechanism), `clsx` from 'clsx' (default
// or named), `classnames`/`classNames` from 'classnames' (default import
// bound under either spelling), `twMerge` (named) from 'tailwind-merge', and
// `cn` (named) from a specifier that structurally resolves to
// src/lib/utils — the repo's own utility. An npm package and this repo's
// single canonical utils module are both authenticated by the IMPORT
// STATEMENT alone, never by resolving and re-reading the target file.
export function authenticClassBuilderImport(identifier) {
  const name = identifier.text
  if (name === 'cva') return authenticCvaImport(identifier)
  const sourceFile = identifier.getSourceFile()
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue
    const clause = statement.importClause
    if (!clause || clause.isTypeOnly) continue
    const specifier = statement.moduleSpecifier.text
    const named = clause.namedBindings && ts.isNamedImports(clause.namedBindings) ? clause.namedBindings : null
    if (name === 'clsx' && specifier === 'clsx') {
      if (clause.name?.text === 'clsx') return true
      if (named?.elements.some((element) => !element.isTypeOnly && element.name.text === 'clsx' && (element.propertyName?.text ?? element.name.text) === 'clsx')) return true
    }
    if ((name === 'classnames' || name === 'classNames') && specifier === 'classnames') {
      if (clause.name?.text === name) return true
      if (named?.elements.some((element) => !element.isTypeOnly && element.name.text === name)) return true
    }
    if (name === 'twMerge' && specifier === 'tailwind-merge') {
      if (named?.elements.some((element) => !element.isTypeOnly && element.name.text === 'twMerge' && (element.propertyName?.text ?? element.name.text) === 'twMerge')) return true
    }
    if (name === 'cn' && classBuilderImportBase(sourceFile.fileName, specifier) === 'src/lib/utils') {
      if (named?.elements.some((element) => !element.isTypeOnly && element.name.text === 'cn' && (element.propertyName?.text ?? element.name.text) === 'cn')) return true
    }
  }
  return false
}

// Structural specifier resolution for the repo's single canonical utils
// module — the same '@/'/relative math governedModulePath uses elsewhere in
// this file, but without requiring the target to be present in the governed
// `modules` snapshot (an npm-package-shaped authentication by import
// statement alone, not a cross-module content read).
export function classBuilderImportBase(fromPath, specifier) {
  if (specifier.startsWith('@/')) return `src/${specifier.slice(2)}`
  if (specifier.startsWith('.')) return path.posix.normalize(path.posix.join(path.posix.dirname(fromPath), specifier))
  return null
}

// Whether `name` is bound to ANYTHING in a scope reachable from `identifier`
// — a same-file top-level function declaration, a lexically-visible
// const/parameter, or the flat top-level `bindings` map. An IMPORT is
// deliberately not tracked here (imports are authenticated separately by
// authenticClassBuilderImport and cannot coexist with a same-named local
// declaration in valid TS/JS), so this answers exactly the question the
// look-alike defect turns on: "is there a LOCAL declaration that could be
// shadowing the trusted name". An ambiguous (duplicate) same-file
// declaration counts as competing too — never blanket-trusted merely
// because no SINGLE lexical match could be resolved.
export function hasCompetingLocalBinding(identifier, name, { sourceFile, bindings, duplicateBindings }) {
  if (sameFileFunctionDeclaration(name, sourceFile)) return true
  if (duplicateBindings.has(name) || hasMultipleVariableDeclarations(sourceFile, name)) return true
  if (findLexicalBinding(identifier, name)) return true
  if (bindings.has(name)) return true
  return false
}

// The function-like node locally bound to `name`, when one exists — a
// same-file function declaration, or a lexically-visible const bound to an
// arrow/function expression. Returns null for anything else bound to `name`
// (a plain value, a class, a parameter with no function initializer) —
// those are look-alikes with no forwarding proof available at all, and
// transparentClassBuilderAlias below correctly returns false for them.
export function classBuilderLocalFunctionFor(identifier, name, { sourceFile, bindings, duplicateBindings }) {
  const fnDecl = sameFileFunctionDeclaration(name, sourceFile)
  if (fnDecl) return fnDecl
  if (duplicateBindings.has(name) || hasMultipleVariableDeclarations(sourceFile, name)) return null
  const lexical = findLexicalBinding(identifier, name)
  const initializer = lexical?.initializer ?? (bindings.has(name) ? bindings.get(name) : null)
  const candidate = initializer ? unwrapStatic(initializer) : null
  return candidate && (ts.isArrowFunction(candidate) || ts.isFunctionExpression(candidate)) ? candidate : null
}

// A local declaration for a CLASS_BUILDERS name is trusted ONLY when it is
// provably the SAME transparent single-parameter forward
// classBuilderOwnParameterForward already recognizes at cn()'s own
// definition site (P10) — reusing that exact structural proof rather than
// inventing a second mechanism. Anything else (wrong arity, extra
// statements, a non-forwarding return, a name mismatch) is precisely the
// "local look-alike" this fix must not trust, and falls through to the
// generic analysis instead.
export function transparentClassBuilderAlias(identifier, name, ctx) {
  const fn = classBuilderLocalFunctionFor(identifier, name, ctx)
  if (!fn || fn.parameters.length !== 1) return false
  const parameter = fn.parameters[0]
  if (!ts.isIdentifier(parameter.name)) return false
  if (classBuilderDeclarationName(fn) !== name) return false
  const returned = classBuilderSingleReturnExpression(fn)
  if (!returned) return false
  return classBuilderChainForwardsParameter(returned, parameter.name.text, new Set())
}

export function pureFunctionReturns(call, context) {
  const declaration = functionDeclarationFor(call, context)
  if (!declaration?.body) return null
  if (!ts.isBlock(declaration.body)) return isPureStaticExpression(declaration.body) ? [declaration.body] : null
  if (declaration.body.statements.length !== 1) return null
  const statement = declaration.body.statements[0]
  if (!ts.isReturnStatement(statement) || !statement.expression || !isPureStaticExpression(statement.expression)) return null
  return [statement.expression]
}

export function isPureStaticExpression(node) {
  let pure = true
  function visit(current) {
    if (!pure) return
    if (ts.isCallExpression(current) || ts.isNewExpression(current) || ts.isAwaitExpression(current) || ts.isYieldExpression(current) || ts.isTaggedTemplateExpression(current)
      || (ts.isBinaryExpression(current) && current.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && current.operatorToken.kind <= ts.SyntaxKind.LastAssignment)) {
      pure = false
      return
    }
    current.forEachChild(visit)
  }
  visit(node)
  return pure
}

// ---------------------------------------------------------------------------
// Finite-return call resolution (independent review capabilities C1/C2):
// resolves a call to a local/imported function, or an immediately-invoked
// function expression, whose body is provably a FINITE, enumerable set of
// return expressions — an if-chain of guard returns ending optionally in an
// unconditional return, and/or a single switch statement — with only
// harmless (`const`, `void <expr>`, `throw`) statements otherwise. Used both
// to resolve a whole call used directly as a class value (BULK_BUTTON_CLASS
// style) and, via classCarryingLeaves below, to resolve property reads off a
// call's returned object literal (statusConfig.textClass style). Anything
// outside this shape (mutable state, unenumerable control flow, `let`
// reassignment across branches, opaque helper output) is NOT provable here
// and callers must fail closed.
// ---------------------------------------------------------------------------

// Resolves the single meaningful return/throw in a short statement list
// (a switch clause body or an if-branch body), skipping only harmless
// leading `const` declarations and `void <expr>` statements. Returns
// { ok:true, expr } (expr is null for a bare `return;`/`throw`) when proven,
// { ok:false } otherwise (unrecognized statement — fail closed).
export function terminalReturnOutcome(statements) {
  for (const statement of statements) {
    if (ts.isBlock(statement)) return terminalReturnOutcome(statement.statements)
    if (ts.isReturnStatement(statement)) return { ok: true, expr: statement.expression ?? null }
    if (ts.isThrowStatement(statement)) return { ok: true, expr: null }
    if (ts.isVariableStatement(statement) && (statement.declarationList.flags & ts.NodeFlags.Const)) continue
    if (ts.isExpressionStatement(statement) && ts.isVoidExpression(statement.expression)) continue
    return { ok: false }
  }
  return { ok: false }
}

// A fallthrough (empty-statement) case clause contributes nothing of its
// own — the next non-empty clause's outcome covers it — so it is simply
// skipped rather than resolved.
export function collectSwitchReturns(switchStatement) {
  const results = []
  for (const clause of switchStatement.caseBlock.clauses) {
    if (clause.statements.length === 0) continue
    const outcome = terminalReturnOutcome(clause.statements)
    if (!outcome.ok) return null
    if (outcome.expr) results.push(outcome.expr)
  }
  return results
}

// Collects every reachable return-value expression from a function body's
// top-level statements: const declarations and `void` statements are
// transparent; an `if (cond) return <expr>` guard (no `else`) contributes
// its outcome and falls through to the next statement; a switch statement
// contributes every clause's outcome; a return statement must be the LAST
// statement (nothing can follow it) and ends collection. No unconditional
// terminal return is a safe, valid outcome — the implicit `undefined` value
// contributes no class content, exactly like a switch with no default.
// Anything else (loops, try/catch, if/else, reassignment) is unprovable.
export function collectFiniteReturns(statements) {
  const results = []
  for (let index = 0; index < statements.length; index += 1) {
    const statement = statements[index]
    if (ts.isVariableStatement(statement) && (statement.declarationList.flags & ts.NodeFlags.Const)) continue
    if (ts.isExpressionStatement(statement) && ts.isVoidExpression(statement.expression)) continue
    if (ts.isReturnStatement(statement)) {
      if (index !== statements.length - 1) return null
      if (statement.expression) results.push(statement.expression)
      return results
    }
    if (ts.isIfStatement(statement) && !statement.elseStatement) {
      const body = ts.isBlock(statement.thenStatement) ? statement.thenStatement.statements : [statement.thenStatement]
      const outcome = terminalReturnOutcome(body)
      if (!outcome.ok) return null
      if (outcome.expr) results.push(outcome.expr)
      continue
    }
    if (ts.isSwitchStatement(statement)) {
      const switchResults = collectSwitchReturns(statement)
      if (!switchResults) return null
      results.push(...switchResults)
      continue
    }
    return null
  }
  return results
}

// A same-file top-level `function name(...) {...}` declaration — distinct
// from findLexicalBinding (const bindings + parameters only) and
// importedDeclaration (import statements only), neither of which sees a
// plain function declaration in the SAME file.
export function sameFileFunctionDeclaration(name, sourceFile) {
  for (const statement of sourceFile.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name?.text === name && statement.body) return statement
  }
  return null
}

// Resolves a call's callee to a function-like declaration: a local
// const-bound arrow/function expression, a same-file function declaration,
// an imported function, or (unlike functionDeclarationFor, used by the
// stricter single-return pureFunctionReturns above) an immediately-invoked
// function expression (`(() => {...})()`), whose callee is the function
// itself, not an identifier.
export function calleeDeclaration(call, context) {
  const callee = unwrapStatic(call.expression)
  if (!callee) return null
  if (ts.isArrowFunction(callee) || ts.isFunctionExpression(callee)) return callee
  if (!ts.isIdentifier(callee)) return null
  const local = findLexicalBinding(callee, callee.text)?.initializer
  const localFunction = unwrapStatic(local)
  if (localFunction && (ts.isArrowFunction(localFunction) || ts.isFunctionExpression(localFunction))) return localFunction
  const sameFile = sameFileFunctionDeclaration(callee.text, callee.getSourceFile())
  if (sameFile) return sameFile
  const imported = importedDeclaration(callee, context)
  return imported && (ts.isFunctionDeclaration(imported) || ts.isArrowFunction(imported) || ts.isFunctionExpression(imported)) ? imported : null
}

// A stable identity for a callee declaration — filename + source position —
// used by finiteCallReturns' cycle guard below. collectFiniteReturns is
// deterministic per declaration (it depends only on the declaration's body,
// never on which call site triggered resolution), so a self- or mutually-
// recursive helper always re-resolves to the SAME declaration on every hop
// around the cycle; keying the guard on the declaration (not the call site)
// catches that regardless of how many distinct call-expression nodes the
// cycle passes through.
export function declarationMarker(declaration) {
  return `${declaration.getSourceFile().fileName}#${declaration.pos}`
}

// Every finite return-value expression a call can produce, or null if the
// callee/body is not provably finite. A non-block (concise) arrow body is
// trivially one expression.
//
// LEAD DECISION (Finding 3 fix, round 2): a self-recursive helper
// (`function cls(n){ return n>0 ? cls(n-1) : 'text-[10px]' }`) or a mutually
// recursive pair has no cycle guard here on its own — this function alone
// cannot loop (it does not call itself), but a caller that walks a returned
// call back into finiteCallReturns again (walkClassExpression's direct
// class-value CallExpression path does exactly this) recurses forever and
// crashes with a RangeError instead of failing closed. `stack` lets a
// caller mark a declaration as "currently being resolved" for the duration
// of walking ITS returns; re-entering finiteCallReturns for the same
// declaration while its marker is still active is a proven cycle and must
// fail closed (null — the same "not provably finite" contract as any other
// unsupported shape), never throw. Defaults to a fresh Set so existing
// callers that do not thread a stack (classCarryingLeaves, which already
// carries its own independent node-identity cycle guard) are unaffected.
export function finiteCallReturns(call, context, stack = new Set()) {
  const declaration = calleeDeclaration(call, context)
  if (!declaration?.body) return null
  if (stack.has(declarationMarker(declaration))) return null
  if (!ts.isBlock(declaration.body)) return [declaration.body]
  return collectFiniteReturns(declaration.body.statements)
}

export function propertyKeyName(property) {
  return property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) || ts.isNumericLiteral(property.name))
    ? property.name.text : null
}

// ---------------------------------------------------------------------------
// Absence-of-member proof (LEAD DECISION, cross-scanner consistency with
// ts-colors.mjs's absenceValue/absenceFactory/absenceBindingUsesSafe/
// absenceLexicalBinding/absentClassProperty — read there for the reviewed
// original; ported here, not re-derived, so the two lanes hold the SAME
// standard). A member is provably ABSENT only when:
//   - every candidate object literal reachable through the chain below has
//     an explicit `__proto__: null` and no computed keys or spreads
//     (an ordinary object can inherit or later gain the property from
//     application code or a prototype mutation elsewhere — only a null
//     prototype rules that out without assuming module-graph purity);
//   - the call chain that produced those literals, if any, resolves through
//     a safe, non-async, non-generator TOP-LEVEL factory function whose own
//     identifier is used only as a call callee; and
//   - any identifier binding (a `const NAME = <call>` an owner was reached
//     through) is used SAFELY everywhere in its enclosing block: read only
//     via property/element access, never reassigned, deleted, aliased,
//     Object.assign-mutated, incremented, or called as a method receiver —
//     scanning the whole block catches a write AFTER the read site too, not
//     only before it.
// Anything less fails closed: a missing static match is never silently "no
// class content" — it is unprovable and must reach emitUnsupported. This
// same standard gates PRESENT branch enumeration too (not just absence):
// the binding-safety leg above is checked before any leaf, found or
// missing, is trusted.
// ---------------------------------------------------------------------------

export function absenceBindingHasName(binding, name) {
  if (ts.isIdentifier(binding)) return binding.text === name
  return (ts.isObjectBindingPattern(binding) || ts.isArrayBindingPattern(binding))
    && binding.elements.some((element) => ts.isBindingElement(element) && absenceBindingHasName(element.name, name))
}

// Resolve lexical declarations without falling through a nearer opaque
// binding. Switch/namespace/class/loop scopes and catch-clause/parameter
// shadows are deliberately outside this proof — ambiguous, not resolved.
export function absenceLexicalBinding(identifier) {
  const name = identifier.text
  for (let scope = identifier.parent; scope; scope = scope.parent) {
    if (ts.isCaseBlock(scope) || ts.isModuleBlock(scope) || ts.isClassDeclaration(scope)
      || ts.isClassExpression(scope) || ts.isForStatement(scope) || ts.isForInStatement(scope)
      || ts.isForOfStatement(scope)) return null
    if (ts.isFunctionLike(scope) && scope.parameters.some((parameter) => absenceBindingHasName(parameter.name, name))) return null
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
        const importedBindings = clause?.namedBindings
        if (importedBindings && ts.isNamedImports(importedBindings)) {
          for (const element of importedBindings.elements) if (element.name.text === name) matches.push(element)
        } else if (importedBindings?.name?.text === name) matches.push(importedBindings)
      }
    }
    if (matches.length) return matches.length === 1 ? matches[0] : null
  }
  return null
}

// A resolved member value consumed purely as a JSX element's own tag name
// (`<Icon/>`, `<driveChip.Icon/>`) is a terminal render read: React only
// invokes/constructs the referenced value, it is never handed anything that
// could reach back and mutate the stable record that produced it. Ported
// from ts-colors.mjs's isJsxTagName (parity, round 2) — without this an
// unrelated sibling property rendered as a component tag blanket-blocks
// every OTHER property read off the SAME record purely because it isn't
// itself a further `.member` read.
export function isJsxTagName(expression) {
  const parent = expression.parent
  return Boolean(parent) && (ts.isJsxOpeningElement(parent) || ts.isJsxSelfClosingElement(parent) || ts.isJsxClosingElement(parent)) && parent.tagName === expression
}

// `Object.keys/freeze/isFrozen/getOwnPropertyNames(X)` are well-known,
// spec-pure static reads that can never hand a MUTABLE reference to one of
// `X`'s nested values back to the caller: `keys`/`getOwnPropertyNames`
// return a fresh array of plain (immutable) strings, and `freeze`/
// `isFrozen` only touch `X`'s own mutability flag. Ported from
// ts-colors.mjs's isReadonlyObjectStaticCallArgument/
// READONLY_OBJECT_STATIC_METHODS (parity, round 2) — found missing via a
// real-tree audit run: `ListView.tsx`'s `Object.keys(PRIORITY_BADGE)` (read
// once, for its domain of keys) wrongly disqualified EVERY other,
// unrelated `.prop`/`[key]` read of the same never-mutated imported record
// (`PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]`) once
// exportNeverMutatedByImporters started scanning importer usage.
//
// DELIBERATELY NARROWER than ts-colors.mjs's set here: ts-colors also
// exempts `values`/`entries` because its `knownClassDerivedUsesSafe`
// separately, recursively re-proves that EVERY value extracted that way is
// itself never escaped/mutated (own doc comment: "already covered by the
// SAME per-property proof used elsewhere in this file"). That recursive
// derived-value proof is not ported here — exempting `values`/`entries`
// WITHOUT it would silently trust `Object.values(REC).forEach(v => { v.cls
// = 'text-[10px]' })`, which hands back LIVE references to REC's own nested
// objects and mutates them in place (caught red-handed by
// tests/design-system-locks/cross-scanner-false-green.test.mjs's "Object.values
// mutation of a nested const record" / "Object.entries mutation ..." /
// "imported record mutated via Object.values by the importer" cases — all
// three zero-finding bypasses while `values`/`entries` were exempted here).
// `keys`/`freeze`/`isFrozen`/`getOwnPropertyNames` have no such hole: none
// of them ever return a reference to a nested object at all.
export const READONLY_OBJECT_STATIC_METHODS = new Set(['keys', 'freeze', 'isFrozen', 'getOwnPropertyNames'])

export function isReadonlyObjectStaticCallArgument(node) {
  const parent = node.parent
  if (!ts.isCallExpression(parent) || parent.expression === node || !parent.arguments.includes(node)) return false
  const callee = unwrapStatic(parent.expression)
  return ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && callee.expression.text === 'Object'
    && READONLY_OBJECT_STATIC_METHODS.has(callee.name.text)
}

// A simple, non-rest, non-default, non-nested object-destructuring read of a
// stable receiver (`const { Icon } = config`) is safe when every extracted
// local is itself only ever used safely (recursing through this same
// property-or-terminal-JSX-tag proof) — the same "read-only, never a whole
// mutable escape" guarantee absenceBindingUsesSafe already proves for a
// plain member-access alias, just entered through a binding pattern instead
// of a `const x = y.z` initializer. Ported from ts-colors.mjs's
// destructuredPatternUsesSafe (CAP-E2, parity) — without this, a
// destructured extraction consumed only as a JSX tag (GoalPillTray's
// `const { Icon } = config` feeding `<Icon/>`) is neither a further
// `.member` read nor a JSX tag name itself, so it blanket-blocked every
// OTHER, unrelated property read off the SAME record (`config.accentClass`)
// purely because destructuring wasn't a recognized safe use at all.
export function destructuredPatternUsesSafe(pattern) {
  if (!ts.isObjectBindingPattern(pattern)) return false
  for (const element of pattern.elements) {
    if (element.dotDotDotToken || !ts.isIdentifier(element.name) || element.initializer) return false
    if (!absenceBindingUsesSafe(element, false, true)) return false
  }
  return true
}

// ---------------------------------------------------------------------------
// Array never-mutated proof (TaskDetailPanel.tsx's `STATUS_OPTIONS.filter(
// (o) => ...).map((o) => ({ ..., className: cn('text-xs', o.color) }))`).
//
// absenceBindingUsesSafe above treats ANY direct method call on the binding
// as suspect — sound for a RECORD (a bare object literal calling a method on
// itself is unusual), but wrong for an ARRAY: `.filter()/.map()/.find()` are
// the ordinary, constant way to read one, and treating every such call as
// unproven would make this proof fail on nearly every real array. This is a
// dedicated, Array.prototype-aware counterpart, not a patch to the shared
// proof (zero risk to every existing record-shaped caller).
//
// Two independent risks, both must be ruled out for every reference to the
// array binding, across its own declaring module AND every importer
// (exportNeverMutatedByImporters call sites below thread this in via its
// usesSafeCheck parameter):
//   1. A call to a genuinely mutating method (push/pop/shift/unshift/
//      splice/sort/reverse/fill/copyWithin — ts-colors.mjs's
//      ARRAY_MUTATING_METHODS documents the same split) or a direct index/
//      length assignment.
//   2. A callback-taking method (filter/map/flatMap/find/findIndex/
//      findLast/findLastIndex/some/every/forEach) whose callback WRITES
//      through its own element parameter (`o.color = 'x'`) — every element
//      it receives is the SAME live object the array holds, not a copy.
//   3. A method that can hand back one of the array's ORIGINAL element
//      objects by reference (find/findLast/at/slice/concat, or a plain
//      `arr[i]` read) escaping into a binding this proof no longer tracks
//      (a `const`/`let`, a call argument, a container literal) rather than
//      being read-and-discarded in the same expression — mirrors
//      derivedValueEscapeSafe's "must be aliased into a re-provable const,
//      or it is unsafe" discipline, but inverted: an array read has nothing
//      further to prove as long as it is NEVER aliased at all.
export const ARRAY_MUTATING_METHODS = new Set(['push', 'pop', 'shift', 'unshift', 'splice', 'sort', 'reverse', 'fill', 'copyWithin'])

// Never mutates, never returns/leaks an original element reference —
// findIndex/findLastIndex return an index (number), not the element.
export const ARRAY_NON_EXTRACTING_SAFE_METHODS = new Set(['some', 'every', 'includes', 'indexOf', 'lastIndexOf', 'findIndex', 'findLastIndex', 'join', 'forEach'])

// Returns (or can return) one or more of the array's ORIGINAL element
// objects by reference — safe only when never aliased (see risk 3 above).
export const ARRAY_EXTRACTING_SAFE_METHODS = new Set(['filter', 'map', 'flatMap', 'slice', 'concat', 'find', 'findLast', 'at'])

export const ARRAY_CALLBACK_TAKING_METHODS = new Set(['filter', 'map', 'flatMap', 'find', 'findIndex', 'findLast', 'findLastIndex', 'some', 'every', 'forEach'])

// True when `callback` (the method call's first argument) never writes
// through its own first parameter — the exact same reassignment/member-write
// discipline forwardedClassBoundary's `check()` already applies to a
// forwarded className parameter, here guarding an array element instead.
export function arrayCallbackParameterNeverWritten(callback) {
  if (!callback || !ts.isFunctionLike(callback) || callback.parameters.length === 0) return true
  const parameter = callback.parameters[0]
  if (!ts.isIdentifier(parameter.name)) return true // a destructured element param reads only, never mutates the receiver by member-write
  const name = parameter.name.text
  let mutated = false
  function visit(node) {
    if (mutated || !node) return
    if (ts.isIdentifier(node) && node.text === name && node !== parameter.name) {
      const parent = node.parent
      if (ts.isBinaryExpression(parent) && parent.left === node
        && parent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && parent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { mutated = true; return }
      if (ts.isDeleteExpression(parent) || ts.isPrefixUnaryExpression(parent) || ts.isPostfixUnaryExpression(parent)) { mutated = true; return }
      if ((ts.isPropertyAccessExpression(parent) || ts.isElementAccessExpression(parent)) && parent.expression === node) {
        const grandparent = parent.parent
        if (ts.isBinaryExpression(grandparent) && grandparent.left === parent
          && grandparent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && grandparent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { mutated = true; return }
        if (ts.isDeleteExpression(grandparent)) { mutated = true; return }
      }
      if (ts.isCallExpression(parent) && parent.expression === node) { mutated = true; return } // calling the element itself — unprovable
    }
    node.forEachChild(visit)
  }
  if (callback.body) visit(callback.body)
  return !mutated
}

// True when `node` (a call/element-access that can return one of the
// array's original elements) is read-and-discarded in the same expression —
// never assigned to a `const`/`let`, handed to an arbitrary call as an
// argument, or folded into a container literal — climbing past `??`/`||`/
// ternary branches and parens first, since none of those persist a value
// anywhere on their own.
export function arrayResultConsumedImmediately(node) {
  let current = node
  while (current.parent && (
    (ts.isBinaryExpression(current.parent) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(current.parent.operatorToken.kind) && current.parent.left === current)
    || (ts.isConditionalExpression(current.parent) && (current.parent.whenTrue === current || current.parent.whenFalse === current))
    || ts.isParenthesizedExpression(current.parent)
  )) current = current.parent
  const consumer = current.parent
  if (!consumer) return true
  if (ts.isVariableDeclaration(consumer) && consumer.initializer === current) return false
  if (ts.isBinaryExpression(consumer) && consumer.left === current
    && consumer.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && consumer.operatorToken.kind <= ts.SyntaxKind.LastAssignment) return false
  if (ts.isCallExpression(consumer) && consumer.arguments.includes(current)) return false
  if (ts.isPropertyAssignment(consumer) || ts.isShorthandPropertyAssignment(consumer) || ts.isSpreadAssignment(consumer) || ts.isSpreadElement(consumer)) return false
  if (ts.isArrayLiteralExpression(consumer) || ts.isObjectLiteralExpression(consumer)) return false
  return true
}

// The array-aware counterpart of absenceBindingUsesSafe: every reference to
// `declaration`'s name in its enclosing scope must be one of the proven-safe
// shapes above. Unrecognized syntax (destructuring, spread, `for...of`, a
// detached method reference never called, calling the array itself) is not
// chased further and fails closed, same posture as every other proof in
// this file.
export function arrayBindingUsesSafe(declaration) {
  if (!declaration.name || !ts.isIdentifier(declaration.name)) return false
  const name = declaration.name.text
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  function visit(node) {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === name && node !== declaration.name) {
      const parent = node.parent
      if (ts.isImportSpecifier(parent) && parent === declaration) return
      if (isJsxTagName(node)) return
      if (ts.isPropertyAccessExpression(parent) && parent.expression === node) {
        const method = parent.name.text
        if (method === 'length') {
          const grandparent = parent.parent
          if (ts.isBinaryExpression(grandparent) && grandparent.left === parent
            && grandparent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && grandparent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { safe = false; return }
          return
        }
        const call = parent.parent
        if (!ts.isCallExpression(call) || call.expression !== parent) { safe = false; return }
        if (ARRAY_MUTATING_METHODS.has(method)) { safe = false; return }
        const callback = ARRAY_CALLBACK_TAKING_METHODS.has(method) ? call.arguments[0] : null
        if (callback && !arrayCallbackParameterNeverWritten(callback)) { safe = false; return }
        if (ARRAY_NON_EXTRACTING_SAFE_METHODS.has(method)) return
        if (ARRAY_EXTRACTING_SAFE_METHODS.has(method)) { if (!arrayResultConsumedImmediately(call)) { safe = false; return } return }
        safe = false
        return
      }
      if (ts.isElementAccessExpression(parent) && parent.expression === node) {
        const grandparent = parent.parent
        if (ts.isBinaryExpression(grandparent) && grandparent.left === parent
          && grandparent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && grandparent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) { safe = false; return }
        if (!arrayResultConsumedImmediately(parent)) { safe = false; return }
        return
      }
      safe = false
    }
    node.forEachChild(visit)
  }
  visit(scope)
  return safe
}

// A receiver may only be read through properties; a factory may only be
// called. Whole-object references, aliases, writes, deletes, increments and
// calls through its members escape the proof. Scanning the enclosing block
// also catches writes AFTER the sink, not just before it.
export function absenceBindingUsesSafe(declaration, factory, indexedReads = false) {
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
      if (factory) {
        if (!ts.isCallExpression(parent) || parent.expression !== node) { safe = false; return }
      } else {
        if (isJsxTagName(node)) return
        if (ts.isVariableDeclaration(parent) && parent.initializer === node && destructuredPatternUsesSafe(parent.name)) return
        if (isReadonlyObjectStaticCallArgument(node)) return
        if ((!ts.isPropertyAccessExpression(parent) && !ts.isElementAccessExpression(parent)) || parent.expression !== node) { safe = false; return }
        const argument = ts.isElementAccessExpression(parent) && parent.argumentExpression ? unwrapStatic(parent.argumentExpression) : null
        const property = ts.isPropertyAccessExpression(parent) ? parent.name.text
          : argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument)) ? argument.text : null
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
    node.forEachChild(visit)
  }
  visit(scope)
  return safe
}

// ---------------------------------------------------------------------------
// Derived-value escape proof (round 3). Ported from ts-colors.mjs's
// knownClassDerivedUsesSafe/primitiveLeaf/resolveMemberTargets — the leg
// absenceBindingUsesSafe above deliberately does NOT cover: a nested
// (non-primitive) value read off an already-trusted record is exactly as
// mutable, through the SAME record binding, as a direct member write.
// `const list = [REC.a]; list[0].cls = 'text-[10px]'` hands out a LIVE
// reference to REC's own nested `{ cls }` object the instant `REC.a` is
// evaluated — nothing about a property-write proof scanning for
// assignment/delete/call syntax on the RECEIVER traces where that reference
// is handed off to (cross-scanner-false-green.test.mjs "array alias
// mutation of a nested object", the one remaining shared-suite failure this
// round closes).
//
// A fixed literal value can never carry a mutable reference back to
// anything (parity with ts-colors' primitiveLeaf base case) — a
// ternary/`??`/`||` chain composed entirely of such leaves is exactly as
// immutable. Deliberately narrower than absenceValue/classCarryingLeaves:
// an object/array literal is NOT a safe leaf here, unlike those proofs —
// it can still be captured by a separate alias and mutated elsewhere, which
// is exactly the escape this proof exists to catch.
export function primitiveLeaf(value) {
  value = unwrapStatic(value)
  if (!value) return false
  if (ts.isStringLiteralLike(value) || ts.isNumericLiteral(value) || ts.isTemplateExpression(value)
    || [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword].includes(value.kind)) return true
  if (ts.isConditionalExpression(value)) return primitiveLeaf(value.whenTrue) && primitiveLeaf(value.whenFalse)
  if (ts.isBinaryExpression(value) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(value.operatorToken.kind)) {
    return primitiveLeaf(value.left) && primitiveLeaf(value.right)
  }
  return false
}

// The static string/numeric key a single property/element access step
// selects, or null when the key is dynamic/computed (unresolvable at that
// step — derivedMemberTargets below enumerates every value in that case,
// mirroring ts-colors' resolveMemberTargets dynamic-key fallback).
export function memberAccessKey(member) {
  if (ts.isPropertyAccessExpression(member)) return member.name.text
  const argument = member.argumentExpression ? unwrapStatic(member.argumentExpression) : null
  return argument && (ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument) || ts.isNumericLiteral(argument)) ? argument.text : null
}

// Structural (safety-independent) resolution of ONE static-or-dynamic
// property/element key step against a set of candidate container value
// nodes, returning the resolved target node(s). A spread anywhere in a
// candidate container, an omitted array element, or a non-container
// candidate reached mid-chain all fail closed to null — always treated by
// the caller as "not proven primitive" (requires capture), never as
// absence. A dynamic key (key === null) enumerates every value the
// container holds, the same live-reference risk Object.values/entries carry
// (own doc comment on NESTED_REFERENCE_RETURNING_STATIC_METHODS in
// ts-colors.mjs) — and the same reason a real dynamic-key record read like
// `STATUS_BADGE[status].dot` must still resolve safely when its OWN further
// static key (`dot`) is primitive on every branch.
export function derivedMemberTargets(containers, key) {
  const next = []
  for (const container of containers) {
    const resolved = unwrapStatic(container)
    if (!resolved) return null
    if (ts.isObjectLiteralExpression(resolved)) {
      if (resolved.properties.some((property) => ts.isSpreadAssignment(property))) return null
      if (key === null) {
        for (const property of resolved.properties) {
          if (!ts.isPropertyAssignment(property) && !ts.isShorthandPropertyAssignment(property)) return null
          next.push(ts.isPropertyAssignment(property) ? property.initializer : property.name)
        }
        continue
      }
      const value = objectProperty(resolved, key)
      if (value) { next.push(value); continue }
      // Proven-absent standard, parity with absenceValue elsewhere in this
      // file (the LEAD DECISION ts-colors null-prototype standard): a
      // property missing from a plain object literal is provably nothing
      // to escape ONLY when the literal itself proves a null prototype.
      if (absenceValue(resolved, key)) continue
      // CAP-G (ported from ts-colors.mjs's resolution.incomplete /
      // knownClassDerivedUsesSafe): an unprovable absence on THIS ONE
      // container — a property genuinely omitted on some branch of a
      // finite union, but without the null-prototype proof above — is a
      // value-completeness concern already independently enforced at that
      // property's OWN emission site (resolveProvenPropertyAccess's
      // missingLeaf/absenceValue gate; still correctly unsupported there,
      // unaffected by this change). It is not evidence this receiver could
      // be mutated, so it must not discard whatever OTHER containers in the
      // same union already proved primitive and blanket-block an unrelated,
      // fully-resolvable sibling property purely because this OTHER
      // property couldn't prove absence on every branch (GoalPillTray's
      // `config.pulse`, omitted on most of describePillState's switch
      // branches with no `__proto__: null`, used to block the
      // fully-resolvable sibling `config.accentClass`). Skip this
      // container's contribution — same as the proven-absent branch above —
      // rather than failing the whole chain closed; a genuinely
      // non-container/spread/computed shape below still fails closed
      // unchanged.
      continue
    }
    if (ts.isArrayLiteralExpression(resolved)) {
      if (resolved.elements.some((element) => ts.isSpreadElement(element))) return null
      if (key !== null && /^\d+$/.test(key)) {
        const element = resolved.elements[Number(key)]
        if (element && !ts.isOmittedExpression(element)) next.push(element)
        else if (element) return null // an omitted array hole is unresolved, not proven-empty
        continue // an out-of-bounds index on a literal array is provably nothing
      }
      for (const element of resolved.elements) {
        if (ts.isOmittedExpression(element)) return null
        next.push(element)
      }
      continue
    }
    return null
  }
  return next
}

// EVERY occurrence of `declaration`'s name used in a property/element-access
// chain whose resolved target(s) (traced structurally from `containers` —
// the record value(s) this declaration currently, provably, holds) are not
// ALL primitive leaves must be captured — immediately, with nothing else in
// between but a `??`/ternary fallback wrapper — by a fresh `const` variable
// declaration whose OWN downstream uses recursively pass this same proof.
// Placed into an array/object/Map/Set literal, passed as a call argument,
// returned, assigned to an existing binding, or spread are all NOT that
// shape (the climb below stops at the first non-property/element-access
// parent, and the capture check that follows requires that parent to be
// exactly a fresh `const` VariableDeclaration initializer) and fail closed.
// Consumed purely as a JSX tag name is the one terminal-render exception
// (isJsxTagName), matching the JSX-tag carve-out absenceBindingUsesSafe
// already grants member reads.
//
// `targets === null` (derivedMemberTargets hit something structurally
// unresolvable) always requires capture — conservative, "when in doubt,
// treat it as an escape". `targets.length === 0`, by contrast, is ONLY ever
// produced by derivedMemberTargets when every step that found nothing was
// INDEPENDENTLY proven absent (the same null-prototype standard
// absenceValue already enforces elsewhere in this file) — there is
// genuinely nothing there to escape, so this does NOT require capture.
// This is a deliberate, narrower reading than ts-colors.mjs's
// knownClassDerivedUsesSafe (whose own resolveMemberTargets can return an
// empty array for reasons OTHER than proven absence, so it must keep
// `targets.length === 0` as a trigger) — needed here because typography's
// resolveProvenPropertyAccess funnels its OWN already-proven-absent reads
// (missingLeaf && absenceValue, e.g. "a helper whose branches ALL prove the
// read property absent resolves as a safe no-op") through this same
// `owner.declaration` gate; without this, a genuinely absent property was
// wrongly relabelled unsupported by the derived-value escape proof itself.
export function derivedValueEscapeSafe(declaration, containers, seen = new Set()) {
  if (!declaration.name || !ts.isIdentifier(declaration.name) || seen.has(declaration)) return false
  const next = new Set(seen).add(declaration)
  let scope = declaration.parent
  while (scope && !ts.isBlock(scope) && !ts.isSourceFile(scope)) scope = scope.parent
  if (!scope) return false
  let safe = true
  const visit = (node) => {
    if (!safe) return
    if (ts.isIdentifier(node) && node.text === declaration.name.text && node !== declaration.name) {
      let member = node.parent
      if ((ts.isPropertyAccessExpression(member) || ts.isElementAccessExpression(member)) && member.expression === node) {
        const keys = [memberAccessKey(member)]
        while ((ts.isPropertyAccessExpression(member.parent) || ts.isElementAccessExpression(member.parent)) && member.parent.expression === member) {
          member = member.parent
          keys.push(memberAccessKey(member))
        }
        let targets = containers
        for (const key of keys) targets = targets ? derivedMemberTargets(targets, key) : null
        if ((!targets || (targets.length > 0 && !targets.every((value) => primitiveLeaf(value)))) && !isJsxTagName(member)) {
          let derived = member
          while ((ts.isBinaryExpression(derived.parent) && [ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(derived.parent.operatorToken.kind))
            || (ts.isConditionalExpression(derived.parent) && (derived.parent.whenTrue === derived || derived.parent.whenFalse === derived))) derived = derived.parent
          const alias = derived.parent
          if (!ts.isVariableDeclaration(alias) || alias.initializer !== derived
            || !(alias.parent.flags & ts.NodeFlags.Const) || !absenceBindingUsesSafe(alias, false, true)
            || !targets || !derivedValueEscapeSafe(alias, targets, next)) { safe = false; return }
        }
      }
    }
    node.forEachChild(visit)
  }
  visit(scope)
  return safe
}

export function absenceFactory(callee) {
  if (!ts.isIdentifier(callee)) return null
  const declaration = absenceLexicalBinding(callee)
  if (!declaration || !absenceBindingUsesSafe(declaration, true)) return null
  if (!ts.isFunctionDeclaration(declaration) || !ts.isSourceFile(declaration.parent)
    || !declaration.body || declaration.asteriskToken
    || declaration.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword)) return null
  return declaration
}

export function absenceValue(node, property) {
  node = unwrapStatic(node)
  if (ts.isObjectLiteralExpression(node)) {
    const nullPrototype = node.properties.some((member) => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name) && propertyKeyName(member) === '__proto__'
      && unwrapStatic(member.initializer).kind === ts.SyntaxKind.NullKeyword)
    return nullPrototype && node.properties.every((member) => ts.isPropertyAssignment(member)
      && !ts.isComputedPropertyName(member.name)
      && propertyKeyName(member) !== property
      && (propertyKeyName(member) !== '__proto__' || unwrapStatic(member.initializer).kind === ts.SyntaxKind.NullKeyword))
  }
  if (ts.isConditionalExpression(node)) {
    return absenceValue(node.whenTrue, property) && absenceValue(node.whenFalse, property)
  }
  if (!ts.isCallExpression(node)) return false
  const declaration = absenceFactory(unwrapStatic(node.expression))
  if (!declaration) return false
  const returns = []
  const visit = (child) => {
    if (ts.isFunctionLike(child) && child !== declaration) return
    if (ts.isReturnStatement(child)) returns.push(child.expression)
    else child.forEachChild(visit)
  }
  visit(declaration.body)
  // Returned calls/aliases are opaque, so recursive functions never recurse
  // through this proof. Direct conditional literals cover every returned arm.
  const fresh = (value) => {
    value = value && unwrapStatic(value)
    if (!value) return false
    if (ts.isConditionalExpression(value)) return fresh(value.whenTrue) && fresh(value.whenFalse)
    return ts.isObjectLiteralExpression(value) && absenceValue(value, property)
  }
  return returns.length > 0 && returns.every(fresh)
}
