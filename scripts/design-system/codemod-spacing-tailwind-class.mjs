#!/usr/bin/env node
// Exact-source repair codemod — C1 lane S-SPACE-1 (Stage B closure,
// dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md), M1's
// script #1: "codemod-spacing-tailwind-class.mjs" covers the
// `tailwind-fraction-utility` + `variant-prefixed-tailwind-fraction-utility`
// parser buckets from dist/design-system-baseline/cli-lanes/c1-prep/M1/
// mapping.json — plain and variant-prefixed Tailwind spacing utilities
// (p/px/py/.../m/mx/my/.../gap/gap-x/gap-y/space-x/space-y), 156 distinct
// values / 2,099 ledger items at the time this was written.
//
// The mapping is READ AT RUN TIME (loadMapping/buildTokenTable below), never
// hardcoded: the lead can re-run this unchanged once a currently
// NEEDS-DECISION value (e.g. `py-20`, 70px, "outside the 2px render-alike
// band" per the mapping's own `reason` text) gets a founder ruling and moves
// to NORMALIZED — the next run picks it up with zero code changes, because
// --groups only ever widens/narrows which of the mapping's OWN `group`
// labels this run is allowed to act on.
//
// Default behavior: apply IDENTICAL + NORMALIZED, refuse everything else
// (NEEDS-DECISION, or any group not named by --groups). Refuses, rather than
// guesses, four additional shapes that are real but out of THIS script's
// scope:
//   - a negative utility (`-mt-1`) — pattern `negative-tailwind-fraction-
//     utility`, a sibling lane's script (M1 script #2) because it needs a
//     Tailwind v4 compile probe before batch-applying (M1 finding 4);
//   - a class already carrying a second, conflicting spacing utility in the
//     same class list (same variant-modifier chain + same Tailwind spacing
//     prefix, different value) — rewriting one side of an existing conflict
//     mechanically could not be proven invisible;
//   - a token that is structurally spacing-utility-shaped but absent from
//     the mapping snapshot entirely — refuses rather than guess a token
//     target from source drift since M1 was built;
//   - any class expression this script cannot prove is a static, literal
//     class-list string at all (a dynamic call, a member/element access, an
//     imported/`let`/parameter-bound identifier, ...) — "refuse rather than
//     guess" per COMMON-RULES.md; typically ordinary `className` prop
//     forwarding, never itself a place a spacing literal could live, but
//     unprovable is unprovable regardless of how likely that is.
//
// Class-list contexts handled (COMMON-RULES.md "every place a class string
// lives"): a JSX `className`/`class` attribute (string literal, template
// literal, conditional, `&&`/`||`/`??`, array, object map, or a call to a
// recognized class builder, recursively); a direct or nested argument to a
// recognized class-builder call (`cn`, `clsx`, `classNames`, `classnames`,
// `cva`, `cx`, `tv`, `twMerge`, `twJoin` — trusted only when the name is NOT
// also locally declared in the file, the same "not shadowed" bar every
// other codemod in this directory uses); a `cva()` variants/compoundVariants
// map (plain object-literal recursion — a variant's STRING KEY is scanned
// too, mirroring spacing.mjs::visitClassObject, which is what makes a
// `clsx`/`classnames` boolean-map argument — `clsx({ 'p-4': isActive })` —
// work the same way without a separate code path); and a class-string
// CONSTANT — a bare identifier used as a class value is resolved to its own
// `const NAME = <class-expr>` declaration in the same file (module or block
// scope) and the resolved expression is walked the same way, recursively.
//
// Preserves everything else on the class list verbatim: order, duplicates,
// unrelated utilities, and every variant-prefix chain (`hover:`, `sm:`,
// `max-sm:pointer-coarse:`, `[&:has(*)]:`, `[&_p]:`, ...) — a variant chain
// is split off at its LAST top-level (bracket-depth-0) colon and prepended
// to the resolved bare utility's own replacement text verbatim; never
// reformatted, reordered, or guessed at.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

export const DEFAULT_MAPPING_REL = 'dist/design-system-baseline/cli-lanes/c1-prep/M1/mapping.json'
export const TARGET_PATTERNS = new Set(['tailwind-fraction-utility', 'variant-prefixed-tailwind-fraction-utility'])
export const DEFAULT_ALLOWED_GROUPS = Object.freeze(['IDENTICAL', 'NORMALIZED'])

const KNOWN_BUILDERS = new Set(['cn', 'clsx', 'classNames', 'classnames', 'cva', 'cx', 'tv', 'twMerge', 'twJoin'])
const CLASS_ATTRIBUTE_NAMES = new Set(['className', 'class'])

// Mirrors scripts/design-system-locks/spacing.mjs's own TW_PREFIXES list
// (the real scanner this mapping was built from) — kept identical on
// purpose so "structurally looks like a spacing utility" means the same
// thing here as it did when the ledger was generated. Sorted longest-first
// so e.g. `gap-x-2` matches prefix `gap-x`, never the shorter `gap`.
const TW_SPACING_PREFIXES = [
  'scroll-ms', 'scroll-me', 'scroll-mx', 'scroll-my', 'scroll-mt', 'scroll-mr', 'scroll-mb', 'scroll-ml',
  'scroll-ps', 'scroll-pe', 'scroll-px', 'scroll-py', 'scroll-pt', 'scroll-pr', 'scroll-pb', 'scroll-pl',
  'scroll-m', 'scroll-p',
  'space-x', 'space-y',
  'gap-x', 'gap-y',
  'ms', 'me', 'mx', 'my', 'mt', 'mr', 'mb', 'ml',
  'ps', 'pe', 'px', 'py', 'pt', 'pr', 'pb', 'pl',
  'gap', 'm', 'p',
].sort((a, b) => b.length - a.length)

// ── mapping loading (run-time, never hardcoded) ─────────────────────────

export function loadMapping(repoRoot, mappingPath) {
  const path = mappingPath ? resolve(mappingPath) : resolve(repoRoot, DEFAULT_MAPPING_REL)
  const text = readFile(path)
  let json
  try {
    json = JSON.parse(text)
  } catch (err) {
    throw new Error(`mapping file ${path} is not valid JSON: ${err.message}`, { cause: err })
  }
  if (!json || !Array.isArray(json.rows)) throw new Error(`mapping file ${path} has no "rows" array`)
  return { path, json }
}

/** `ours`: value -> row, restricted to TARGET_PATTERNS (this script's own
 * scope). `any`: value -> row, every pattern in the file — used only to
 * distinguish "a real M1 value that belongs to a sibling script's pattern"
 * (silently out of scope) from "not in the mapping snapshot at all" (a
 * defensive refusal — see classifyToken). */
export function buildTokenTable(mappingJson, mappingPath) {
  const ours = new Map()
  const any = new Map()
  for (const row of mappingJson.rows ?? []) {
    if (typeof row.value !== 'string' || row.value.length === 0) continue
    any.set(row.value, row)
    if (TARGET_PATTERNS.has(row.pattern)) ours.set(row.value, row)
  }
  return { ours, any, mappingPath }
}

export function parseGroupsArg(argv, defaultGroups = DEFAULT_ALLOWED_GROUPS) {
  const idx = argv.findIndex((a) => a === '--groups' || a.startsWith('--groups='))
  if (idx === -1) return [...defaultGroups]
  const raw = argv[idx].startsWith('--groups=') ? argv[idx].slice('--groups='.length) : argv[idx + 1]
  const groups = (raw ?? '').split(',').map((s) => s.trim()).filter(Boolean)
  return groups.length > 0 ? groups : [...defaultGroups]
}

export function parseMappingArg(argv) {
  const idx = argv.findIndex((a) => a === '--mapping' || a.startsWith('--mapping='))
  if (idx === -1) return null
  return argv[idx].startsWith('--mapping=') ? argv[idx].slice('--mapping='.length) : argv[idx + 1]
}

// ── token-structure helpers ──────────────────────────────────────────────

/** Splits a class token at its LAST top-level (bracket-depth-0) colon, so an
 * arbitrary-variant group's own internal colon (`[&:has(*)]:pb-2`) is never
 * mistaken for the modifier/utility boundary, and a stacked modifier chain
 * (`max-sm:pointer-coarse:gap-6`) splits before the utility, not between
 * modifiers. */
export function splitVariantPrefix(token) {
  let depth = 0
  let lastColon = -1
  for (let i = 0; i < token.length; i++) {
    const ch = token[i]
    if (ch === '[') depth += 1
    else if (ch === ']') depth = Math.max(0, depth - 1)
    else if (ch === ':' && depth === 0) lastColon = i
  }
  if (lastColon === -1) return { prefixChain: '', base: token }
  return { prefixChain: token.slice(0, lastColon + 1), base: token.slice(lastColon + 1) }
}

/** Classifies a variant-stripped base utility as spacing-shaped or not.
 * Returns `{ prefix, rest, negative }` or `null`. A prefix match with a
 * keyword suffix (`mx-auto`, `p-full`, ...) is deliberately NOT spacing-
 * shaped — the real scanner (spacing.mjs IGNORE_KEYWORDS) never produces a
 * ledger finding for those either, so treating them as ambiguous here would
 * be pure noise, not a real gap. */
export function parseSpacingUtility(base) {
  const negative = base.startsWith('-')
  const unsigned = negative ? base.slice(1) : base
  for (const prefix of TW_SPACING_PREFIXES) {
    if (!unsigned.startsWith(prefix + '-')) continue
    const rest = unsigned.slice(prefix.length + 1)
    if (rest.length === 0) continue
    // `p-0`/`gap-0`/... is never root-dependent (0 * anything is always
    // exactly 0px, already the constitutional scale's own --space-0) and
    // never appears in the M1 ledger at all — the real scanner
    // (spacing.mjs) never flags it as a violation in the first place, so
    // treating it as spacing-shaped-but-unmapped here would be pure noise:
    // 69 real-repo refusals for a value that was never wrong, confirmed
    // during this script's own preview run.
    if (/^0(\.0+)?$/.test(rest)) return null
    if (/^\d+(\.\d+)?$/.test(rest) || rest === 'px') return { prefix, rest, negative }
    if (/^\[.+\]$/.test(rest)) {
      // An arbitrary-bracket value whose content is ALREADY a `var(--...)`
      // token reference is this script's own output (or another C1 script's)
      // — already migrated, not a target — never flagged. This is what
      // keeps a second run over already-applied output idempotent: without
      // it, `gap-[var(--space-2)]` would itself look "spacing-shaped but
      // unmapped" and get a spurious defensive refusal on re-scan.
      if (/^var\(--/.test(rest.slice(1, -1))) return null
      return { prefix, rest, negative }
    }
    return null
  }
  return null
}

/**
 * Classifies one whitespace-delimited class token against the mapping.
 * `{ kind: 'apply', replacement, row }` | `{ kind: 'refuse', category, reason }` | `{ kind: 'skip' }`.
 */
export function classifyToken(token, table, allowedGroups) {
  const direct = table.ours.get(token)
  if (direct) return classifyRow(direct, allowedGroups)

  const { prefixChain, base } = splitVariantPrefix(token)
  if (prefixChain) {
    const baseRow = table.ours.get(base)
    if (baseRow) return classifyRow({ ...baseRow, value: token, replacement: `${prefixChain}${baseRow.replacement}` }, allowedGroups)
  }

  const spacing = parseSpacingUtility(base)
  if (!spacing) return { kind: 'skip' }
  if (spacing.negative) {
    return {
      kind: 'refuse',
      category: 'negative-utility',
      reason: `\`${token}\` is a negative spacing utility — owned by the sibling negative-utility lane (M1 script #2, codemod-spacing-tailwind-negative.mjs), which must confirm Tailwind v4 compiles a negated \`var()\` arbitrary value before batch-applying (M1 finding 4).`,
    }
  }
  if (table.any.has(token) || table.any.has(base)) return { kind: 'skip' }
  return {
    kind: 'refuse',
    category: 'unmapped-spacing-shaped-token',
    reason: `\`${token}\` structurally resembles a Tailwind spacing utility but is not present in the M1 mapping snapshot (${table.mappingPath ?? DEFAULT_MAPPING_REL}) — refusing rather than guessing its token target.`,
  }
}

function classifyRow(row, allowedGroups) {
  if (!allowedGroups.includes(row.group)) {
    return {
      kind: 'refuse',
      category: 'group-not-applied',
      reason: row.reason && row.reason.length > 0
        ? row.reason
        : `\`${row.value}\` is classified ${row.group}, which is not in the applied groups (${allowedGroups.join(', ')}).`,
    }
  }
  return { kind: 'apply', replacement: row.replacement, row }
}

/**
 * Classifies a flat list of `{ text, start, end }` tokens (already
 * positioned, either relative to one literal or absolute across a whole
 * template), adding a conflict pass: any two spacing-shaped tokens sharing
 * the same variant-prefix chain AND the same Tailwind spacing prefix, but
 * with a different value, mark every one of THIS script's own candidates
 * (apply / group-not-applied) among them as refused instead — conflict
 * takes priority over an otherwise-clean apply or an otherwise-refusable
 * NEEDS-DECISION classification, since the ambiguity is more fundamental
 * than either. A `negative-utility` or `unmapped-spacing-shaped-token`
 * refusal is left as-is (already refused, for its own, unrelated reason).
 */
export function classifyTokens(tokens, table, allowedGroups) {
  const groups = new Map()
  for (const t of tokens) {
    const { prefixChain, base } = splitVariantPrefix(t.text)
    const spacing = parseSpacingUtility(base)
    if (!spacing || spacing.negative) continue
    const key = `${prefixChain}\u0000${spacing.prefix}`
    if (!groups.has(key)) groups.set(key, new Set())
    groups.get(key).add(base)
  }
  const conflictKeys = new Set([...groups.entries()].filter(([, bases]) => bases.size > 1).map(([k]) => k))

  return tokens.map((t) => {
    const { prefixChain, base } = splitVariantPrefix(t.text)
    const spacing = parseSpacingUtility(base)
    const inConflict = spacing && !spacing.negative && conflictKeys.has(`${prefixChain}\u0000${spacing.prefix}`)
    let classification = classifyToken(t.text, table, allowedGroups)
    if (inConflict && (classification.kind === 'apply' || (classification.kind === 'refuse' && classification.category === 'group-not-applied'))) {
      classification = {
        kind: 'refuse',
        category: 'conflicting-spacing-token',
        reason: `\`${t.text}\` shares its spacing property with another, differing utility in the same class list (same modifier chain + \`${spacing.prefix}\` Tailwind prefix, different value) — refusing rather than guess which one is meant to win.`,
      }
    }
    return { ...t, classification }
  })
}

function tokenizeLiteral(text, baseOffset) {
  return [...text.matchAll(/\S+/g)].map((m) => ({ text: m[0], start: baseOffset + m.index, end: baseOffset + m.index + m[0].length }))
}

/** Tokenizes a TemplateExpression's own literal segments (head + each
 * span's trailing literal), EXCLUDING any token that touches an
 * interpolation boundary without intervening whitespace — such a token is
 * fused with dynamic content and its true rendered text cannot be proven,
 * so it is silently left untouched (neither applied nor refused) rather
 * than risk splitting a runtime-composed class in half. */
export function collectTemplateTokens(templateNode, sourceFile) {
  const segments = [{
    text: templateNode.head.text,
    start: templateNode.head.getStart(sourceFile) + 1,
    openLeft: false,
    openRight: templateNode.templateSpans.length > 0,
  }]
  templateNode.templateSpans.forEach((span, i) => {
    segments.push({
      text: span.literal.text,
      start: span.literal.getStart(sourceFile) + 1,
      openLeft: true,
      openRight: i !== templateNode.templateSpans.length - 1,
    })
  })

  const tokens = []
  for (const seg of segments) {
    const matches = [...seg.text.matchAll(/\S+/g)]
    matches.forEach((m, i) => {
      const isFirst = i === 0
      const isLast = i === matches.length - 1
      const leftOpen = isFirst && seg.openLeft && m.index === 0
      const rightOpen = isLast && seg.openRight && m.index + m[0].length === seg.text.length
      if (leftOpen || rightOpen) return
      tokens.push({ text: m[0], start: seg.start + m.index, end: seg.start + m.index + m[0].length })
    })
  }
  return tokens
}

function staticPropertyName(name) {
  if (!name) return null
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) return name.text
  if (ts.isStringLiteral(name) || ts.isNumericLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name)) return name.text
  return null
}

function unwrap(node) {
  while (node && (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isNonNullExpression(node) || ts.isSatisfiesExpression(node))) {
    node = node.expression
  }
  return node
}

// ── builder-call authentication ─────────────────────────────────────────

const localDeclMemo = new WeakMap()

/** Every top-level name this file itself declares (function/class/const/let/
 * var) — a bare call to a KNOWN_BUILDERS name is trusted as a real class
 * builder only when it is NOT one of these, mirroring spacing.mjs's own
 * "authenticate, don't trust by name alone" posture (guardClassBuilder) at
 * low cost: a locally-declared lookalike falls through to the generic
 * "unrecognized call" refusal instead of being walked as though its
 * arguments were the class content. */
function isLocallyDeclared(sourceFile, name) {
  let set = localDeclMemo.get(sourceFile)
  if (!set) {
    set = new Set()
    const visit = (node) => {
      if ((ts.isFunctionDeclaration(node) || ts.isClassDeclaration(node)) && node.name && ts.isIdentifier(node.name)) set.add(node.name.text)
      if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) set.add(node.name.text)
      ts.forEachChild(node, visit)
    }
    visit(sourceFile)
    localDeclMemo.set(sourceFile, set)
  }
  return set.has(name)
}

function isKnownBuilderCall(node, sourceFile) {
  return ts.isCallExpression(node) && ts.isIdentifier(node.expression) &&
    KNOWN_BUILDERS.has(node.expression.text) && !isLocallyDeclared(sourceFile, node.expression.text)
}

/** True for a call to a LOCAL `const NAME = cva(...)` factory (e.g.
 * `buttonVariants({ variant })`) — its possible string content was already
 * fully walked at the `cva(...)` DEFINITION site (reached independently,
 * since `cva` is itself a KNOWN_BUILDERS name), so re-visiting the call SITE
 * would either double-report the same literals or, worse, hit the
 * unresolvable `{ variant }` argument and refuse it for no reason. Silently
 * skipped, not refused — mirrors spacing.mjs::cvaFactoryDefinition's
 * "re-inspect the definition, not the call site" strategy, simplified: no
 * cross-file import resolution, only the same file's own top-level consts. */
function isCvaFactoryCallSite(node, sourceFile) {
  if (!ts.isCallExpression(node) || !ts.isIdentifier(node.expression)) return false
  const name = node.expression.text
  for (const statement of sourceFile.statements) {
    if (!ts.isVariableStatement(statement)) continue
    for (const decl of statement.declarationList.declarations) {
      if (!ts.isIdentifier(decl.name) || decl.name.text !== name || !decl.initializer) continue
      const init = unwrap(decl.initializer)
      if (ts.isCallExpression(init) && ts.isIdentifier(init.expression) && init.expression.text === 'cva') return true
    }
  }
  return false
}

/** Resolves a bare identifier used as a class value back to a `const NAME =
 * <expr>` declaration in an enclosing block or the module scope of the SAME
 * file (innermost scope wins, respecting shadowing) — the "class-string
 * constant" case. Refuses (does not guess) an import, a `let`/`var`
 * binding, a destructured/computed name, or no declaration found at all. */
function resolveConstStringInitializer(identifier) {
  const name = identifier.text
  let scope = identifier.parent
  while (scope) {
    if (ts.isBlock(scope) || ts.isSourceFile(scope)) {
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement)) continue
        if (!(statement.declarationList.flags & ts.NodeFlags.Const)) continue
        for (const decl of statement.declarationList.declarations) {
          if (ts.isIdentifier(decl.name) && decl.name.text === name) {
            if (!decl.initializer) return { ok: false, reason: '`const` declaration has no initializer' }
            return { ok: true, node: decl.initializer }
          }
        }
      }
    }
    scope = scope.parent
  }
  return { ok: false, reason: 'no local `const` declaration found in this file (an import, a `let`/`var` binding, a destructured name, or a parameter cannot be proven a static class-list string)' }
}

// ── AST walk ─────────────────────────────────────────────────────────────

function locStr(ctx, node) {
  const loc = ctx.sourceFile.getLineAndCharacterOfPosition(node.getStart(ctx.sourceFile))
  return `${relative(ctx.repoRoot, ctx.filePath)}:${loc.line + 1}:${loc.character + 1}`
}

function applyClassification(t, node, ctx) {
  if (t.classification.kind === 'apply') {
    ctx.edits.push({ start: t.start, end: t.end, replacement: t.classification.replacement, loc: locStr(ctx, node), syntax: t.text })
  } else if (t.classification.kind === 'refuse') {
    ctx.refusals.push({ file: relative(ctx.repoRoot, ctx.filePath), loc: locStr(ctx, node), syntax: t.text, category: t.classification.category, reason: t.classification.reason })
  }
}

function handleStringLikeLiteral(node, ctx) {
  const inner = node.getText(ctx.sourceFile).slice(1, -1)
  const baseOffset = node.getStart(ctx.sourceFile) + 1
  const tokens = tokenizeLiteral(inner, baseOffset)
  for (const t of classifyTokens(tokens, ctx.table, ctx.allowedGroups)) applyClassification(t, node, ctx)
}

function refuse(node, ctx, category, reason) {
  ctx.refusals.push({ file: relative(ctx.repoRoot, ctx.filePath), loc: locStr(ctx, node), syntax: node.getText(ctx.sourceFile), category, reason })
}

/** Recursively resolves and edits one class-bearing expression. Refuses
 * (never guesses) any shape it cannot structurally prove is a static
 * class-list string.
 *
 * Guards on `ctx.visitedNodes` (a WeakSet of AST node objects, not text) so
 * the SAME node is never processed twice — real collision: a top-level
 * `const resolvedClassName = cn('p-2 gap-4', ...)` is reached BOTH directly
 * by `visitTopLevel`'s own generic walk (which finds the `cn(...)` call and
 * iterates its arguments) AND, independently, whenever `resolvedClassName`
 * is later used as a class value (`<div className={resolvedClassName} />`
 * resolves the identifier back to the SAME `cn(...)` call node and walks
 * INTO it again). Without this guard the second path re-walks the same
 * string-literal argument nodes and emits duplicate, position-identical
 * edits — `codemod-lib.mjs::applyEdits` correctly throws on that overlap
 * (found on the real repo: `src/components/workspaces/team/
 * AddAgentPicker.tsx`'s `resolvedClassName`). Guarding by node IDENTITY,
 * not text, never suppresses two textually-identical-but-distinct
 * occurrences (`cn('p-2', 'p-2')` is two real AST nodes, both still walked).
 */
function visitClassExpr(rawNode, ctx) {
  const expr = unwrap(rawNode)
  if (!expr) return
  if (ctx.visitedNodes.has(expr)) return
  ctx.visitedNodes.add(expr)

  if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) { handleStringLikeLiteral(expr, ctx); return }

  if (ts.isTemplateExpression(expr)) {
    const tokens = collectTemplateTokens(expr, ctx.sourceFile)
    for (const t of classifyTokens(tokens, ctx.table, ctx.allowedGroups)) applyClassification(t, expr, ctx)
    return
  }

  if (ts.isConditionalExpression(expr)) { visitClassExpr(expr.whenTrue, ctx); visitClassExpr(expr.whenFalse, ctx); return }

  if (ts.isBinaryExpression(expr) && expr.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) {
    // `guard && 'class'` — the LEFT side is a boolean condition, never a
    // class value itself (mirrors spacing.mjs's own && handling); only the
    // RIGHT side can ever contribute a class token.
    visitClassExpr(expr.right, ctx)
    return
  }
  if (
    ts.isBinaryExpression(expr) &&
    [ts.SyntaxKind.BarBarToken, ts.SyntaxKind.QuestionQuestionToken].includes(expr.operatorToken.kind)
  ) {
    // `value || 'fallback'` / `value ?? 'fallback'` — BOTH sides are
    // genuine class-value alternatives.
    visitClassExpr(expr.left, ctx)
    visitClassExpr(expr.right, ctx)
    return
  }

  if (ts.isArrayLiteralExpression(expr)) {
    for (const el of expr.elements) {
      if (ts.isSpreadElement(el)) { refuse(el, ctx, 'unprovable-class-string', 'a spread element in a class-list array cannot be proven a static class-list string.'); continue }
      visitClassExpr(el, ctx)
    }
    return
  }

  if (ts.isObjectLiteralExpression(expr)) {
    for (const prop of expr.properties) {
      if (ts.isPropertyAssignment(prop)) {
        // The KEY is scanned too (spacing.mjs::visitClassObject convention)
        // — this is what makes a clsx/classnames BOOLEAN-MAP argument
        // (`clsx({ 'p-4': isActive })`, key = class, value = condition) work
        // via the exact same code path as a cva variants map (key = variant
        // name, value = class): whichever position actually holds a class
        // token gets scanned; the other is harmlessly filtered out by
        // classifyToken's own structural check (an ordinary variant name
        // like `primary` never looks spacing-shaped).
        if (ts.isStringLiteral(prop.name) || ts.isNoSubstitutionTemplateLiteral(prop.name)) handleStringLikeLiteral(prop.name, ctx)
        visitClassExpr(prop.initializer, ctx)
      } else if (ts.isShorthandPropertyAssignment(prop)) {
        visitClassExpr(prop.name, ctx)
      } else {
        refuse(prop, ctx, 'unprovable-class-string', `unrecognized object-literal member shape (${ts.SyntaxKind[prop.kind]}) inside a class map.`)
      }
    }
    return
  }

  if (ts.isCallExpression(expr)) {
    if (isKnownBuilderCall(expr, ctx.sourceFile)) { for (const arg of expr.arguments) visitClassExpr(arg, ctx); return }
    if (isCvaFactoryCallSite(expr, ctx.sourceFile)) return // already covered at its cva() definition site
    refuse(expr, ctx, 'unprovable-class-string', 'a call to an unrecognized (or locally-shadowed) function cannot be proven a static class-list string.')
    return
  }

  if (ts.isIdentifier(expr)) {
    if (expr.text === 'undefined') return
    const resolved = resolveConstStringInitializer(expr)
    if (resolved.ok) { visitClassExpr(resolved.node, ctx); return } // visitedNodes (above) dedupes a multiply-referenced constant
    refuse(expr, ctx, 'unprovable-class-string', `cannot prove \`${expr.text}\` is a static class-list string: ${resolved.reason}`)
    return
  }

  if (ts.isPropertyAccessExpression(expr) || ts.isElementAccessExpression(expr)) {
    refuse(expr, ctx, 'unprovable-class-string', 'a member/element access cannot be proven a static class-list string without resolving its receiver, which this script does not attempt.')
    return
  }

  if (
    [ts.SyntaxKind.TrueKeyword, ts.SyntaxKind.FalseKeyword, ts.SyntaxKind.NullKeyword, ts.SyntaxKind.OmittedExpression].includes(expr.kind) ||
    ts.isNumericLiteral(expr)
  ) return

  refuse(expr, ctx, 'unprovable-class-string', `unrecognized class expression shape (${ts.SyntaxKind[expr.kind]}) — cannot prove it is a static class-list string.`)
}

function visitTopLevel(node, ctx) {
  if (ts.isJsxAttribute(node)) {
    const name = node.name.getText(ctx.sourceFile)
    if (CLASS_ATTRIBUTE_NAMES.has(name) && node.initializer) {
      const init = ts.isJsxExpression(node.initializer) ? node.initializer.expression : node.initializer
      if (init) visitClassExpr(init, ctx)
      return
    }
  } else if (ts.isPropertyAssignment(node)) {
    const name = staticPropertyName(node.name)
    if (CLASS_ATTRIBUTE_NAMES.has(name)) { visitClassExpr(node.initializer, ctx); return }
  } else if (ts.isCallExpression(node) && isKnownBuilderCall(node, ctx.sourceFile)) {
    for (const arg of node.arguments) visitClassExpr(arg, ctx)
    return
  }
  ts.forEachChild(node, (child) => visitTopLevel(child, ctx))
}

// ── driver ───────────────────────────────────────────────────────────────

/** Processes one file: returns `{ edits, refusals }` — never mutates. */
export function planFileEdits(repoRoot, filePath, table, allowedGroups) {
  const text = readFile(filePath)
  const sourceFile = parseSourceFile(filePath, text)
  const ctx = { repoRoot, filePath, sourceFile, table, allowedGroups, edits: [], refusals: [], visitedNodes: new WeakSet() }
  visitTopLevel(sourceFile, ctx)
  return { edits: ctx.edits, refusals: ctx.refusals, text, sourceFile }
}

export function runCodemod({ repoRoot, apply, mappingPath, allowedGroups = DEFAULT_ALLOWED_GROUPS }) {
  const { path: resolvedMappingPath, json } = loadMapping(repoRoot, mappingPath)
  const table = buildTokenTable(json, resolvedMappingPath)
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))

  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  const refusalsByCategory = {}

  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath, table, allowedGroups)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax, replacement: e.replacement })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    for (const r of refusals) refusalsByCategory[r.category] = (refusalsByCategory[r.category] ?? 0) + 1
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    mappingPath: resolvedMappingPath,
    appliedGroups: allowedGroups,
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    refusalsByCategory,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const argv = process.argv.slice(2)
  const { apply, root } = parseCodemodArgs(argv, resolve(new URL('../..', import.meta.url).pathname))
  const allowedGroups = parseGroupsArg(argv)
  const mappingPath = parseMappingArg(argv)
  const result = runCodemod({ repoRoot: root, apply, mappingPath, allowedGroups })
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED [${r.category}] ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    mappingPath: result.mappingPath,
    appliedGroups: result.appliedGroups,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
    refusalsByCategory: result.refusalsByCategory,
  }, null, 2))
  process.exit(0)
}
