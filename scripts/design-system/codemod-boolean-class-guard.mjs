#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), pattern P16 and the
// P13 singletons that are the same shape in disguise (see
// dist/design-system-baseline/cli-lanes/claude-codemods/triage.json).
//
// ── The gap ──────────────────────────────────────────────────────────────
// A class-list sink (`cn(...)`, `clsx(...)`, `className={...}`, `class={...}`)
// contains `<guard> && '<literal-class>'`. The scanners (spacing.mjs,
// typography.mjs, ts-colors.mjs) correctly refuse to assume an opaque `guard`
// is boolean — a non-boolean guard could leak its own falsy value (0, '',
// null) into the class list — so they emit an `unsupported` finding on the
// GUARD itself and fail closed, per COMMON-RULES ("Unsupported findings are
// never baselined, and fail-closed is sacred"). That is correct scanner
// behaviour, not a bug to silence.
//
// Probed (dist/design-system-baseline/cli-lanes/fanout/L4/probe-ternary.mjs,
// probe-clean-comparison.out.json): all three scanners already fully resolve
// the ternary shape `<guard> ? '<literal>' : undefined` (and `: ''`) inside
// cn(), clsx() and a plain className={...}, with a fully opaque `guard` —
// zero `unsupported` findings, one real finding (clean pass or violation) per
// literal. `A && 'lit'` on the same guard/literal instead produces an
// `unsupported` finding on `A` from spacing.mjs and ts-colors.mjs (typography
// already handled `&&` — see probe output). No smaller shape search was
// needed: the very first shape tried already closes on all three.
//
// ── What this codemod actually proves before rewriting ──────────────────
// Swapping `A && 'x'` for `A ? 'x' : undefined` is only INVISIBLE (identical
// rendered class list, on every call) if `A` is truly a JS boolean at
// runtime — not merely "some falsy value" — because:
//   - `cn`/`clsx` (src/lib/utils.ts wraps clsx; clsx's own `r()`/`clsx()`
//     gate every argument on plain truthiness: `e && ...`) drop false,
//     undefined, null, 0 and '' identically — so inside a cn()/clsx() call
//     ANY falsy guard type is safe.
//   - A bare `className={A && 'x'}` (no cn/clsx) is NOT automatically safe:
//     React's DOM property setter for `className` is
//     `setValueForKnownAttribute(node, "class", value)`
//     (node_modules/react-dom/cjs/react-dom-client.development.js,
//     `setProp`'s `case "className"`), and that function's `switch(typeof
//     value)` removes the attribute for `undefined` AND for `boolean` —
//     identically — but NOT for `0` or `''` (those get stringified:
//     `class="0"` / `class=""`). So the rewrite is invisible for a bare
//     className/class sink ONLY when the guard's false branch is provably
//     the strict boolean `false`, never `0`/''`/`null`.
// This codemod therefore only ever rewrites a guard it can prove yields a
// strict JS boolean, independent of sink type — which is a STRONGER
// guarantee than cn/clsx even require, so it is safe for every sink in scope
// (cn, clsx, className, class) without needing sink-specific carve-outs.
//
// A guard is "boolean-provable" via one of:
//   Tier 0 (pure ECMA-262 syntax, true regardless of operand types):
//     - `!x` (UnaryExpression `!` always yields a strict boolean)
//     - a comparison (`===`,`!==`,`==`,`!=`,`<`,`<=`,`>`,`>=`,`in`,`instanceof`)
//     - a boolean literal
//     - `.includes(...)`/`.some(...)`/`.every(...)`/`.startsWith(...)`/
//       `.endsWith(...)`/`.test(...)` — built-ins whose return type is
//       boolean by spec (Array/String.prototype.*, RegExp.prototype.test)
//     - `&&`/`||` of two Tier-0-or-Tier-1-provable operands
//   Tier 1 (traced to ONE local declaration in the same file, re-verified
//   against a real source on every run — never a blind hardcode):
//     - `const [x, setX] = useState(false|true)` / `useState<boolean>(...)`
//     - `const { isDragging } = useDraggable(...)` / `const { isOver } =
//       useDroppable(...)` — TRUSTED_HOOK_RETURNS below, each entry
//       re-verified against dnd-kit's own .d.ts on every run
//     - a function parameter destructured with an explicit generic type
//       annotation matched in TRUSTED_GENERIC_PARAM_PROPS (React Flow's
//       `NodeProps<T>.selected`), re-verified against @xyflow's own .d.ts
//     - `const x = useXStore((s) => <expr>)` where `<expr>` is itself
//       Tier-0-provable, OR is a direct `s.field` read whose `field` is
//       declared `boolean` in that store's own types file (STORE_TYPE_FILES;
//       re-parsed from src/store/chat/types.ts on every run)
//     - `const x = <expr>` where `<expr>` recursively proves boolean
//     - `<obj>.<field>` where `<obj>`'s explicit type annotation resolves to
//       a name imported from the generated wire-type module and that field
//       is declared `boolean` in GENERATED_WIRE_TYPE_FILE (re-parsed on
//       every run — e.g. `entry: LibraryEntry` / `LibraryEntry.is_hidden`)
// Anything else (an unresolved identifier, a property access whose base type
// can't be traced, a call to an unrecognized function, more than one
// candidate declaration) is refused and reported, never guessed.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const CLASS_BUILDER_NAMES = new Set(['cn', 'clsx', 'classes', 'cx', 'classNames'])
const CLASS_ATTRIBUTE_NAMES = new Set(['className', 'class'])
const BOOLEAN_METHOD_NAMES = new Set(['includes', 'some', 'every', 'startsWith', 'endsWith', 'test'])
const COMPARISON_OPERATOR_KINDS = new Set([
  ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken,
  ts.SyntaxKind.ExclamationEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken,
  ts.SyntaxKind.LessThanToken, ts.SyntaxKind.LessThanEqualsToken,
  ts.SyntaxKind.GreaterThanToken, ts.SyntaxKind.GreaterThanEqualsToken,
  ts.SyntaxKind.InKeyword, ts.SyntaxKind.InstanceOfKeyword,
])

// Hook return properties trusted as boolean — re-verified against the cited
// .d.ts on every run (see verifyTrustedFacts). Never trusted just because a
// local identifier happens to be named `isDragging`/`isOver`: the CALLEE must
// also be imported from the cited package in the file being edited.
const TRUSTED_HOOK_RETURNS = new Map([
  ['useDraggable.isDragging', {
    requiredImportSpecifier: '@dnd-kit/core',
    verifyFile: 'node_modules/@dnd-kit/core/dist/hooks/useDraggable.d.ts',
    verifySnippet: 'isDragging: boolean;',
  }],
  ['useDroppable.isOver', {
    requiredImportSpecifier: '@dnd-kit/core',
    verifyFile: 'node_modules/@dnd-kit/core/dist/hooks/useDroppable.d.ts',
    verifySnippet: 'isOver: boolean;',
  }],
])

// Function-parameter destructure off a generically-typed parameter, trusted
// as boolean — same re-verification discipline as TRUSTED_HOOK_RETURNS.
const TRUSTED_GENERIC_PARAM_PROPS = new Map([
  ['NodeProps.selected', {
    requiredImportSpecifier: '@xyflow/react',
    verifyFile: 'node_modules/@xyflow/system/dist/esm/types/nodes.d.ts',
    verifySnippet: 'selected?: boolean;',
  }],
])

// Zustand-style store hooks whose `(s) => s.field` selector is trusted when
// `field` is declared `boolean` in the cited types file — re-parsed (not
// hardcoded per-field) on every run by computeStoreBooleanFields.
const STORE_TYPE_FILES = new Map([
  ['useChatStore', { requiredImportSpecifier: '@/store/chat', typesFile: 'src/store/chat/types.ts' }],
])

// Application-local (non-third-party) hooks whose destructured return field
// is trusted when it's declared `boolean` somewhere in the cited source file
// — re-parsed (not hardcoded per-field) on every run by
// computeStoreBooleanFields, exactly like STORE_TYPE_FILES. Distinct from
// TRUSTED_HOOK_RETURNS (which is for THIRD-PARTY hooks whose return type is a
// Pick<>/Required<> utility-type shape too complex to re-parse cheaply, so
// that table instead re-verifies one cited string in the package's own
// .d.ts): a same-repo hook's own plain-interface return type can be
// re-derived exactly, the same way a store's state type can.
const LOCAL_HOOK_RETURN_FILES = new Map([
  ['useLibraryFileEditor', { requiredImportSpecifier: './useLibraryFileEditor', typesFile: 'src/components/library/preview/useLibraryFileEditor.ts' }],
])

// The committed generated wire-type mirror (Hard Constraint #8: generated
// types are the only legal cross-boundary types) used to prove a property
// access like `entry.is_hidden` boolean when `entry`'s own parameter type
// resolves to a name imported from here (or any /generated/ path).
const GENERATED_WIRE_TYPE_FILE = 'src/lib/api/generated/schemas.ts'
const GENERATED_WIRE_IMPORT_SPECIFIERS = [/^@\/lib\/api$/, /\/generated(\/|$)/]

// ── Trusted-fact re-verification ──────────────────────────────────────────
// Mirrors codemod-dead-textclass-read.mjs's "re-derived from source on every
// run, never hardcoded" discipline for third-party .d.ts facts that can't be
// re-parsed cheaply from a stable AST shape (Pick<>/Required<> utility types)
// the way an application source file can. If a fact's cited file no longer
// contains the cited snippet (e.g. a dependency upgrade changed the type),
// the codemod excludes it and reports why — it never silently keeps trusting
// a fact it can no longer see.
export function verifyTrustedFacts(repoRoot) {
  const broken = []
  const checkTable = (table) => {
    const verified = new Map()
    for (const [key, fact] of table) {
      let ok
      try {
        const text = readFile(resolve(repoRoot, fact.verifyFile))
        ok = text.includes(fact.verifySnippet)
      } catch {
        ok = false
      }
      if (ok) verified.set(key, fact)
      else broken.push({ key, verifyFile: fact.verifyFile, verifySnippet: fact.verifySnippet })
    }
    return verified
  }
  return {
    trustedHookReturns: checkTable(TRUSTED_HOOK_RETURNS),
    trustedGenericParamProps: checkTable(TRUSTED_GENERIC_PARAM_PROPS),
    broken,
  }
}

/** Parses a store's types file and returns the set of property names declared `boolean` anywhere in it (interfaces + object-literal type aliases). */
export function computeStoreBooleanFields(repoRoot, relFile) {
  const filePath = resolve(repoRoot, relFile)
  let text
  try {
    text = readFile(filePath)
  } catch {
    return new Set()
  }
  const source = parseSourceFile(filePath, text)
  return collectBooleanMemberNames(source)
}

/** Parses GENERATED_WIRE_TYPE_FILE and returns Map<typeName, Set<booleanFieldName>> across every `type X = {...}` / `interface X {...}` in it. */
export function computeGeneratedWireBooleanFields(repoRoot) {
  const filePath = resolve(repoRoot, GENERATED_WIRE_TYPE_FILE)
  let text
  try {
    text = readFile(filePath)
  } catch {
    return new Map()
  }
  const source = parseSourceFile(filePath, text)
  const map = new Map()
  const isBooleanTypeNode = (t) => {
    if (!t) return false
    if (t.kind === ts.SyntaxKind.BooleanKeyword) return true
    if (ts.isUnionTypeNode(t)) {
      const hasBoolean = t.types.some((tt) => tt.kind === ts.SyntaxKind.BooleanKeyword)
      const restAreNullish = t.types.every((tt) => tt.kind === ts.SyntaxKind.BooleanKeyword || tt.kind === ts.SyntaxKind.UndefinedKeyword)
      return hasBoolean && restAreNullish
    }
    return false
  }
  const collectMembers = (members, typeName) => {
    const set = new Set()
    for (const m of members) {
      if (ts.isPropertySignature(m) && m.name && ts.isIdentifier(m.name) && isBooleanTypeNode(m.type)) {
        set.add(m.name.text)
      }
    }
    if (set.size > 0) map.set(typeName, set)
  }
  const visit = (node) => {
    if (ts.isTypeAliasDeclaration(node) && ts.isTypeLiteralNode(node.type)) collectMembers(node.type.members, node.name.text)
    if (ts.isInterfaceDeclaration(node)) collectMembers(node.members, node.name.text)
    ts.forEachChild(node, visit)
  }
  visit(source)
  return map
}

function collectBooleanMemberNames(source) {
  const set = new Set()
  const isBooleanTypeNode = (t) => {
    if (!t) return false
    if (t.kind === ts.SyntaxKind.BooleanKeyword) return true
    if (ts.isUnionTypeNode(t)) return t.types.some((tt) => tt.kind === ts.SyntaxKind.BooleanKeyword)
    return false
  }
  const visit = (node) => {
    if ((ts.isPropertySignature(node) || ts.isPropertyDeclaration(node)) && node.name && ts.isIdentifier(node.name) && isBooleanTypeNode(node.type)) {
      set.add(node.name.text)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return set
}

/** Local name -> module specifier, for any named (value or type) import in the file. */
function importSpecifierOf(source, localName) {
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    if (!ts.isStringLiteral(statement.moduleSpecifier)) continue
    const clause = statement.importClause
    const namedBindings = clause && clause.namedBindings
    if (!namedBindings || !ts.isNamedImports(namedBindings)) continue
    for (const el of namedBindings.elements) {
      if (el.name.text === localName) return statement.moduleSpecifier.text
    }
  }
  return undefined
}

/** Local type name -> original exported name it was imported as (handles `import { X as Y }`). */
function importedOriginalName(source, localName) {
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    const clause = statement.importClause
    const namedBindings = clause && clause.namedBindings
    if (!namedBindings || !ts.isNamedImports(namedBindings)) continue
    for (const el of namedBindings.elements) {
      if (el.name.text === localName) return (el.propertyName ?? el.name).text
    }
  }
  return undefined
}

function isGeneratedWireImport(specifier) {
  return GENERATED_WIRE_IMPORT_SPECIFIERS.some((re) => re.test(specifier))
}

// ── Scope resolution (deliberately conservative: refuses on ambiguity) ────
function enclosingFunctionLike(node) {
  let current = node.parent
  while (current) {
    if (ts.isFunctionDeclaration(current) || ts.isFunctionExpression(current) || ts.isArrowFunction(current) || ts.isMethodDeclaration(current)) {
      return current
    }
    current = current.parent
  }
  return null
}

function paramBindingFor(fn, name) {
  if (!fn) return null
  for (const param of fn.parameters) {
    if (ts.isIdentifier(param.name) && param.name.text === name) {
      return { kind: 'param-identifier', typeNode: param.type }
    }
    if (ts.isObjectBindingPattern(param.name)) {
      for (const el of param.name.elements) {
        if (ts.isBindingElement(el) && ts.isIdentifier(el.name) && el.name.text === name) {
          const propertyName = el.propertyName && ts.isIdentifier(el.propertyName) ? el.propertyName.text : name
          return { kind: 'param-object-destructure', propertyName, typeNode: param.type }
        }
      }
    }
  }
  return null
}

function scopeStatementsOf(fn, sourceFile) {
  if (!fn) return sourceFile.statements
  return fn.body && ts.isBlock(fn.body) ? fn.body.statements : []
}

function localVarBindingsFor(statements, name) {
  const matches = []
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && node.initializer) {
      if (ts.isIdentifier(node.name) && node.name.text === name) {
        matches.push({ kind: 'const-identifier', initializer: node.initializer })
      } else if (ts.isObjectBindingPattern(node.name)) {
        for (const el of node.name.elements) {
          if (ts.isBindingElement(el) && ts.isIdentifier(el.name) && el.name.text === name) {
            const propertyName = el.propertyName && ts.isIdentifier(el.propertyName) ? el.propertyName.text : name
            matches.push({ kind: 'const-object-destructure', propertyName, initializer: node.initializer })
          }
        }
      } else if (ts.isArrayBindingPattern(node.name)) {
        node.name.elements.forEach((el, index) => {
          if (ts.isBindingElement(el) && ts.isIdentifier(el.name) && el.name.text === name) {
            matches.push({ kind: 'array-destructure', index, initializer: node.initializer })
          }
        })
      }
    }
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node) || ts.isMethodDeclaration(node)) return
    ts.forEachChild(node, visit)
  }
  for (const stmt of statements) visit(stmt)
  return matches
}

/** Resolves ONE binding for `identifierNode`, climbing outward through enclosing
 * function scopes (param list, then that scope's own non-nested statements) until
 * found. Returns null (unresolved), a binding object, or {kind:'ambiguous'}. */
function resolveBinding(identifierNode, sourceFile) {
  const name = identifierNode.text
  let level = enclosingFunctionLike(identifierNode)
  // include the starting point once, then climb
  const visited = new Set()
  while (true) {
    const levelKey = level ?? sourceFile
    if (visited.has(levelKey)) break
    visited.add(levelKey)

    const p = paramBindingFor(level, name)
    if (p) return p

    const matches = localVarBindingsFor(scopeStatementsOf(level, sourceFile), name)
    if (matches.length === 1) return matches[0]
    if (matches.length > 1) return { kind: 'ambiguous' }

    if (!level) break
    level = enclosingFunctionLike(level)
  }
  return null
}

// ── Boolean provability ────────────────────────────────────────────────
function isTier0LogicalOperand(kind) {
  return kind === ts.SyntaxKind.AmpersandAmpersandToken || kind === ts.SyntaxKind.BarBarToken
}

function proveBoolean(node, ctx, depth = 0) {
  if (depth > 16) return { provable: false, reason: 'expression too deep to prove' }
  if (ts.isParenthesizedExpression(node)) return proveBoolean(node.expression, ctx, depth + 1)
  if (node.kind === ts.SyntaxKind.TrueKeyword || node.kind === ts.SyntaxKind.FalseKeyword) {
    return { provable: true, reason: 'boolean literal' }
  }
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) {
    return { provable: true, reason: '`!` always coerces its operand to a strict boolean (ECMA-262), regardless of the operand\'s type' }
  }
  if (ts.isBinaryExpression(node) && COMPARISON_OPERATOR_KINDS.has(node.operatorToken.kind)) {
    return { provable: true, reason: 'comparison operator always yields a strict boolean, regardless of operand types' }
  }
  if (ts.isBinaryExpression(node) && isTier0LogicalOperand(node.operatorToken.kind)) {
    const op = node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken ? '&&' : '||'
    const l = proveBoolean(node.left, ctx, depth + 1)
    if (!l.provable) return { provable: false, reason: `left operand of \`${op}\` not provable: ${l.reason}` }
    const r = proveBoolean(node.right, ctx, depth + 1)
    if (!r.provable) return { provable: false, reason: `right operand of \`${op}\` not provable: ${r.reason}` }
    return { provable: true, reason: `both operands of \`${op}\` independently provably boolean` }
  }
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && BOOLEAN_METHOD_NAMES.has(node.expression.name.text)) {
    const m = node.expression.name.text
    return { provable: true, reason: `\`.${m}(...)\` has a spec-guaranteed boolean return type (Array/String.prototype.${m} or RegExp.prototype.test)` }
  }
  if (ts.isIdentifier(node)) return proveIdentifier(node, ctx, depth)
  if (ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && !node.questionDotToken) {
    return provePropertyAccess(node, ctx)
  }
  return { provable: false, reason: `unrecognized guard shape (${ts.SyntaxKind[node.kind]})` }
}

function proveIdentifier(node, ctx, depth) {
  const binding = resolveBinding(node, ctx.sourceFile)
  if (!binding) return { provable: false, reason: `no local declaration found for \`${node.text}\` in this file` }
  if (binding.kind === 'ambiguous') return { provable: false, reason: `multiple candidate declarations of \`${node.text}\` in scope` }

  if (binding.kind === 'array-destructure') {
    const init = binding.initializer
    if (binding.index === 0 && ts.isCallExpression(init) && ts.isIdentifier(init.expression) && init.expression.text === 'useState') {
      const arg0 = init.arguments[0]
      const literalBoolInit = arg0 && (arg0.kind === ts.SyntaxKind.TrueKeyword || arg0.kind === ts.SyntaxKind.FalseKeyword)
      const typedBoolean = init.typeArguments && init.typeArguments.length === 1 && init.typeArguments[0].kind === ts.SyntaxKind.BooleanKeyword
      if (literalBoolInit || typedBoolean) {
        return { provable: true, reason: `\`useState(${arg0 ? arg0.getText() : ''})\` — TypeScript infers/declares this state (and its setter) as boolean` }
      }
    }
    return { provable: false, reason: `\`${node.text}\` is array-destructured from a source that isn't a recognized boolean useState(...)` }
  }

  if (binding.kind === 'const-object-destructure' || binding.kind === 'param-object-destructure') {
    return proveTrustedDestructure(node.text, binding, ctx)
  }

  if (binding.kind === 'const-identifier') {
    // `const x = useXStore((s) => <expr>)` selector shape.
    const init = binding.initializer
    if (ts.isCallExpression(init) && ts.isIdentifier(init.expression) && STORE_TYPE_FILES.has(init.expression.text) && init.arguments.length === 1) {
      const proved = proveStoreSelector(init, ctx)
      if (proved) return proved
    }
    const inner = proveBoolean(init, ctx, depth + 1)
    if (!inner.provable) {
      return { provable: false, reason: `\`${node.text}\` (= \`${init.getText().slice(0, 60)}\`) not provable: ${inner.reason}` }
    }
    return inner
  }

  if (binding.kind === 'param-identifier') {
    return { provable: false, reason: `\`${node.text}\` is a bare function parameter with no traceable boolean source` }
  }

  return { provable: false, reason: `unrecognized binding kind for \`${node.text}\`` }
}

function proveStoreSelector(callExpr, ctx) {
  const hookName = callExpr.expression.text
  const storeCfg = STORE_TYPE_FILES.get(hookName)
  const importSpec = importSpecifierOf(ctx.sourceFile, hookName)
  if (importSpec !== storeCfg.requiredImportSpecifier) return null // not really the trusted hook — fall through to generic proof (will refuse)
  const selector = callExpr.arguments[0]
  if (!ts.isArrowFunction(selector) || selector.parameters.length !== 1 || !ts.isIdentifier(selector.parameters[0].name)) return null
  const body = ts.isBlock(selector.body) ? null : selector.body
  if (!body) return null
  const tier0 = proveBoolean(body, ctx, 0)
  if (tier0.provable) return { provable: true, reason: `\`${hookName}\` selector body is itself boolean-provable: ${tier0.reason}` }
  const paramName = selector.parameters[0].name.text
  if (ts.isPropertyAccessExpression(body) && ts.isIdentifier(body.expression) && body.expression.text === paramName) {
    const field = body.name.text
    const boolFields = ctx.storeBooleanFields.get(hookName)
    if (boolFields && boolFields.has(field)) {
      return { provable: true, reason: `\`${hookName}((${paramName}) => ${paramName}.${field})\` — \`${field}\` is declared \`boolean\` in ${storeCfg.typesFile}` }
    }
  }
  return null
}

function proveTrustedDestructure(localName, binding, ctx) {
  if (binding.kind === 'const-object-destructure') {
    const init = binding.initializer
    if (ts.isCallExpression(init) && ts.isIdentifier(init.expression)) {
      const hookName = init.expression.text
      const key = `${hookName}.${binding.propertyName}`
      const fact = ctx.trustedHookReturns.get(key)
      if (fact) {
        const importSpec = importSpecifierOf(ctx.sourceFile, hookName)
        if (importSpec === fact.requiredImportSpecifier) {
          return { provable: true, reason: `destructured \`${binding.propertyName}\` off \`${hookName}(...)\` (imported from ${fact.requiredImportSpecifier}), verified boolean in ${fact.verifyFile}` }
        }
        return { provable: false, reason: `\`${hookName}\` in this file isn't imported from ${fact.requiredImportSpecifier} — refusing to trust a same-named local function` }
      }
      const localCfg = LOCAL_HOOK_RETURN_FILES.get(hookName)
      if (localCfg) {
        const importSpec = importSpecifierOf(ctx.sourceFile, hookName)
        if (importSpec !== localCfg.requiredImportSpecifier) {
          return { provable: false, reason: `\`${hookName}\` in this file isn't imported from ${localCfg.requiredImportSpecifier} — refusing to trust a same-named local function` }
        }
        const boolFields = ctx.localHookBooleanFields.get(hookName)
        if (boolFields && boolFields.has(binding.propertyName)) {
          return { provable: true, reason: `destructured \`${binding.propertyName}\` off \`${hookName}(...)\` (imported from ${localCfg.requiredImportSpecifier}) — \`${binding.propertyName}\` is declared \`boolean\` in ${localCfg.typesFile}` }
        }
        return { provable: false, reason: `\`${binding.propertyName}\` isn't declared \`boolean\` in ${localCfg.typesFile}` }
      }
    }
    return { provable: false, reason: `\`${localName}\` is object-destructured from an unrecognized call (\`${init.getText().slice(0, 60)}\`)` }
  }
  if (binding.kind === 'param-object-destructure') {
    const typeNode = binding.typeNode
    if (typeNode && ts.isTypeReferenceNode(typeNode) && ts.isIdentifier(typeNode.typeName)) {
      const typeName = typeNode.typeName.text
      const key = `${typeName}.${binding.propertyName}`
      const fact = ctx.trustedGenericParamProps.get(key)
      if (fact) {
        const importSpec = importSpecifierOf(ctx.sourceFile, typeName)
        if (importSpec === fact.requiredImportSpecifier) {
          return { provable: true, reason: `destructured \`${binding.propertyName}\` off a parameter typed \`${typeNode.getText()}\` (imported from ${fact.requiredImportSpecifier}), verified boolean in ${fact.verifyFile}` }
        }
        return { provable: false, reason: `\`${typeName}\` in this file isn't imported from ${fact.requiredImportSpecifier} — refusing to trust a same-named local type` }
      }
    }
    return { provable: false, reason: `\`${localName}\` is a function-parameter destructure with an untraced/untrusted source type` }
  }
  return { provable: false, reason: 'unrecognized destructure kind' }
}

/** Finds a local `interface Name {...}` or `type Name = {...}` declaration in `sourceFile`. */
function findLocalTypeDeclaration(sourceFile, name) {
  let found
  const visit = (node) => {
    if (found) return
    if ((ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node)) && node.name.text === name) {
      found = node
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return found
}

/** The type annotation for `binding.propertyName` when `binding` destructures a
 * function parameter typed by a LOCAL props interface/type-literal in the same
 * file (e.g. `{ entry }: LibraryEntryRowProps` where `interface
 * LibraryEntryRowProps { entry: LibraryEntry; ... }`) — one extra hop beyond
 * the parameter's own (outer) type, which TRUSTED_GENERIC_PARAM_PROPS uses
 * directly for third-party generics like `NodeProps<T>.selected`. */
function resolveDestructuredParamPropertyType(binding, ctx) {
  const outerType = binding.typeNode
  if (!outerType) return undefined
  let members
  if (ts.isTypeLiteralNode(outerType)) {
    members = outerType.members
  } else if (ts.isTypeReferenceNode(outerType) && ts.isIdentifier(outerType.typeName)) {
    const decl = findLocalTypeDeclaration(ctx.sourceFile, outerType.typeName.text)
    if (decl) {
      members = ts.isInterfaceDeclaration(decl)
        ? decl.members
        : (ts.isTypeAliasDeclaration(decl) && ts.isTypeLiteralNode(decl.type) ? decl.type.members : undefined)
    }
  }
  if (!members) return undefined
  for (const m of members) {
    if (ts.isPropertySignature(m) && m.name && ts.isIdentifier(m.name) && m.name.text === binding.propertyName) {
      return m.type
    }
  }
  return undefined
}

function provePropertyAccess(node, ctx) {
  const objName = node.expression.text
  const propName = node.name.text
  const binding = resolveBinding(node.expression, ctx.sourceFile)
  let typeNode
  if (binding && binding.kind === 'param-identifier') {
    typeNode = binding.typeNode
  } else if (binding && binding.kind === 'param-object-destructure') {
    typeNode = resolveDestructuredParamPropertyType(binding, ctx)
  }
  if (!typeNode) return { provable: false, reason: `could not resolve an explicit type annotation for \`${objName}\`` }
  if (!ts.isTypeReferenceNode(typeNode) || !ts.isIdentifier(typeNode.typeName)) {
    return { provable: false, reason: `\`${objName}\`'s type annotation isn't a simple named type reference` }
  }
  const typeName = typeNode.typeName.text
  const importSpec = importSpecifierOf(ctx.sourceFile, typeName)
  if (!importSpec || !isGeneratedWireImport(importSpec)) {
    return { provable: false, reason: `\`${typeName}\` isn't imported from a generated wire-type module in this file` }
  }
  const originalName = importedOriginalName(ctx.sourceFile, typeName) ?? typeName
  const boolFields = ctx.generatedWireBooleanFields.get(originalName)
  if (!boolFields || !boolFields.has(propName)) {
    return { provable: false, reason: `\`${originalName}.${propName}\` isn't declared \`boolean\` in ${GENERATED_WIRE_TYPE_FILE}` }
  }
  return { provable: true, reason: `\`${objName}\` is typed \`${originalName}\` (generated wire type), whose \`${propName}\` field is declared \`boolean\`` }
}

// ── Sink matching + edit planning ─────────────────────────────────────────
function calleeName(expr) {
  return ts.isIdentifier(expr) ? expr.text : undefined
}

function isClassListSink(node) {
  const parent = node.parent
  if (ts.isCallExpression(parent) && parent.arguments.includes(node) && CLASS_BUILDER_NAMES.has(calleeName(parent.expression) ?? '')) {
    return true
  }
  if (ts.isJsxExpression(parent) && parent.expression === node) {
    const attr = parent.parent
    if (ts.isJsxAttribute(attr) && ts.isIdentifier(attr.name) && CLASS_ATTRIBUTE_NAMES.has(attr.name.text)) return true
  }
  return false
}

function isStringishLiteral(node) {
  return ts.isStringLiteral(node) || node.kind === ts.SyntaxKind.NoSubstitutionTemplateLiteral
}

export function planFileEdits(repoRoot, filePath, facts) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const ctx = {
    sourceFile: source,
    trustedHookReturns: facts.trustedHookReturns,
    trustedGenericParamProps: facts.trustedGenericParamProps,
    storeBooleanFields: facts.storeBooleanFields,
    localHookBooleanFields: facts.localHookBooleanFields,
    generatedWireBooleanFields: facts.generatedWireBooleanFields,
  }
  const edits = []
  const refusals = []

  const visit = (node) => {
    if (
      ts.isBinaryExpression(node) &&
      node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken &&
      isStringishLiteral(node.right) &&
      // must be the OUTERMOST `&&` of its chain — not itself the left/right of another `&&`/`||`
      !(ts.isBinaryExpression(node.parent) && isTier0LogicalOperand(node.parent.operatorToken.kind)) &&
      isClassListSink(node)
    ) {
      const loc = source.getLineAndCharacterOfPosition(node.getStart())
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
      const proof = proveBoolean(node.left, ctx, 0)
      if (!proof.provable) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(), reason: proof.reason })
      } else {
        const guardText = text.slice(node.left.getStart(), node.left.getEnd())
        const literalText = text.slice(node.right.getStart(), node.right.getEnd())
        const replacement = `${guardText} ? ${literalText} : undefined`
        edits.push({ start: node.getStart(), end: node.getEnd(), replacement, loc: locStr, syntax: node.getText(), proof: proof.reason })
      }
      // Whether proven or refused, this node's own guard subtree was already
      // fully classified above — do not also visit into node.left looking
      // for a nested, smaller `&&`-with-literal (there isn't one: proveBoolean
      // already walked it for boolean-provability, and a literal can only be
      // the tail of one chain). Still visit node.right (a literal — no-op)
      // and continue past this statement for siblings.
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals, text, source }
}

export function runCodemod({ repoRoot, apply }) {
  const { trustedHookReturns, trustedGenericParamProps, broken } = verifyTrustedFacts(repoRoot)
  const storeBooleanFields = new Map()
  for (const [hookName, cfg] of STORE_TYPE_FILES) {
    storeBooleanFields.set(hookName, computeStoreBooleanFields(repoRoot, cfg.typesFile))
  }
  const localHookBooleanFields = new Map()
  for (const [hookName, cfg] of LOCAL_HOOK_RETURN_FILES) {
    localHookBooleanFields.set(hookName, computeStoreBooleanFields(repoRoot, cfg.typesFile))
  }
  const generatedWireBooleanFields = computeGeneratedWireBooleanFields(repoRoot)
  const facts = { trustedHookReturns, trustedGenericParamProps, storeBooleanFields, localHookBooleanFields, generatedWireBooleanFields }

  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath, facts)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax, proof: e.proof })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    brokenTrustedFacts: broken,
    storeBooleanFieldCounts: Object.fromEntries([...storeBooleanFields].map(([k, v]) => [k, v.size])),
    generatedWireTypeCount: generatedWireBooleanFields.size,
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  if (result.brokenTrustedFacts.length > 0) {
    console.error(`WARNING: ${result.brokenTrustedFacts.length} trusted fact(s) failed re-verification and were excluded: ${result.brokenTrustedFacts.map((b) => b.key).join(', ')}`)
  }
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    brokenTrustedFacts: result.brokenTrustedFacts,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
