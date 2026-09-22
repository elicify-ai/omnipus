#!/usr/bin/env node
// Exact-source repair codemod — C1 lane S-TYPO-1 (dist/design-system-baseline/
// cli-lanes/fanout/COMMON-RULES.md fan-out; mapping source: lane M2's
// dist/design-system-baseline/cli-lanes/c1-prep/M2/mapping.json).
//
// Scope: the TWO Tailwind-syntax patterns M2 scoped to this lane —
//   - static utility classes:   `text-xs`, `text-sm`                (311 items)
//   - arbitrary bracket values: `text-[10px]`, `text-[0.7rem]`, …   (236 items,
//     10 distinct values, 2 of which — `text-[13px]`, `text-[1.15rem]` — are
//     M2 NEEDS-DECISION and are refused, never guessed)
// Every other pattern in M2 (CSS `font-size:`/`font-family:` declarations,
// inline `style={{ fontSize }}` objects, the SVG `fontSize="9"` attribute
// case, missing arbitrary-value type hints) belongs to a sibling lane.
//
// ── Mapping is data, not judgment ──────────────────────────────────────────
// The replacement table below is copied verbatim from M2's mapping.json
// (`rows[].replacementToken` for ruleId `typography/text-size-below-floor`
// restricted to `text-xs`/`text-sm`, and `typography/arbitrary-text-size`
// for all 10 rows). This codemod does not compute a mapping — it applies
// M2's decision and REFUSES anything M2 marked NEEDS-DECISION or anything
// not in the table at all, never inventing a token M2 didn't approve.
//
// ── Where a class string can live, and how each is found ───────────────────
// A "class-list site" is one of:
//   1. A JSX `className`/`class` attribute — a bare string literal, or a
//      `{...}` expression.
//   2. A direct call to a recognized class-composing function
//      (`cn`, `clsx`, `classes`, `cx`, `classNames`) anywhere in the file —
//      every argument is walked.
//   3. A `cva(base, { variants: {...}, compoundVariants: [...] })` call —
//      the base string, every variant map's leaf values, and every compound
//      variant's `class`/`className` property.
//   4. A `const <name> = <expr>` declaration whose name contains "class"
//      (case-insensitive) anywhere in the file — the classic
//      `const iconClass = 'text-xs ...'` / `const sizeClasses = {...}` shape.
//   5. A bare identifier reaching one of the above (e.g. `cn(..., textSize)`
//      where `textSize` isn't class-named at all) is followed back to its
//      OWN `const` declaration when that name resolves to exactly one
//      declaration in the file — see `resolveSoleConstInitializer`. Zero or
//      multiple candidates is left alone, not refused (see that function's
//      comment): most identifiers reaching a class-builder call are not a
//      class string at all, so refusing every unresolved one would be noise,
//      not a real ambiguity finding. This is also how a module-level
//      `const PANEL_BASE = 'flex ... text-xs'` interpolated as
//      `` `${PANEL_BASE} border-...` `` (real VideoEmbed.tsx shape) gets
//      found: every template substitution expression is itself walked (see
//      `processTemplateExpression`), so the identifier is reached and
//      resolved there too — and edited exactly once no matter how many of
//      the 5 templates interpolate it, via `ctx.handledLeaves`.
// From each of those roots, `walkClassExpr` recurses through the shapes that
// compose without changing which string ends up in the DOM: parentheses,
// `?:` both branches, `&&`/`||` both operands, a nested recognized
// class-composing call, a hand-rolled `[...].join(sep)` / `[...].filter(...)
// .join(sep)` array-join (real ShellDenyPatternsEditor.tsx shape),
// array-literal elements, and a plain object literal's property VALUES (the
// `{ small: 'text-xs', large: '...' }` variant-map shape — not clsx's
// inverted `{ 'text-xs': cond }` key-as-class shorthand, unused anywhere in
// M2's file lists for this lane). Anything else (a call to an unrecognized
// function, an identifier that doesn't resolve, a property access, a
// spread) has no provable string content here and is silently skipped —
// there is nothing to refuse when there is no literal text to examine.
//
// Every leaf a root can reach is a StringLiteral, a NoSubstitutionTemplate
// Literal, or one piece of a TemplateExpression (head / middle / tail). A
// given leaf node is only ever processed once (`ctx.handledLeaves`), so the
// same string reached via two different root rules (e.g. a `cn()` call that
// is itself the value of a `className` attribute) never gets edited twice —
// this also makes the traversal order-independent and idempotent by
// construction, not by convention.
//
// ── What counts as a match, and the boundary proof ─────────────────────────
// A match is a mapping key appearing as a WHOLE Tailwind utility token: not
// preceded or followed by a word character or a hyphen (so `focus:text-sm`
// matches on `text-sm` — a variant prefix ends in `:`, which is neither — but
// `not-text-xs-real` does not, on either side). This is a plain boundary
// check on the exact source text between the literal's quotes/backticks; it
// never touches, reformats, or reflows anything else in the string.
//
// ── Refusals — never guessed ────────────────────────────────────────────
//   - NEEDS-DECISION match (`text-[13px]`, `text-[1.15rem]`): refused, citing
//     M2's mapping.json — no existing token renders these values.
//   - Computed/ambiguous: a template literal chunk ending in a bare `text-`
//     or `text-[` immediately followed by an interpolation (e.g.
//     `` `text-${size}` ``) — the resulting class cannot be proven statically,
//     so it is refused rather than guessed. A real match whose OWN left or
//     right edge sits exactly at a dynamic boundary (e.g.
//     `` `text-xs${suffix}` ``, no separating space) is refused the same way
//     — the interpolation could merge into the token.
//   - Conflicting text-size token on the same element: two matches (from
//     this lane's own key set, or one this-lane match plus an already-
//     resolved `text-[length:var(--type-*)]`/`text-[var(--type-*)]` token)
//     sharing the same variant-prefix inside the SAME literal chunk. Scoped
//     deliberately to one literal chunk, not the whole `cn()` call or JSX
//     element: proving a conflict across separate string-literal arguments
//     would require re-deriving Tailwind's same-group class-merge rules,
//     which is out of scope for this narrow, two-pattern lane. A prefix
//     group with only ONE distinct value present is never a conflict, so
//     `text-xs` alongside `md:text-sm` (different breakpoints, not a
//     collision) is untouched by this check — see `prefixOf`.
//
// ── `+`-string concatenation is out of scope ────────────────────────────
// `'text-' + size` is not one of the composed shapes `walkClassExpr` walks
// into. There is no literal FROM-key text to find there (the static operand
// alone, `'text-'`, matches nothing in the table), so this is a silent skip,
// not a refusal — consistent with "refuse only what can be partly seen but
// not safely resolved."
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

// ── M2's decision, verbatim (mapping.json rows, restricted to this lane's
// two ruleIds) ───────────────────────────────────────────────────────────
export const MAPPED = new Map([
  // typography/text-size-below-floor
  ['text-xs', 'text-[length:var(--type-utility-xs-size)]'],
  ['text-sm', 'text-[length:var(--type-body-compact-size)]'],
  // typography/arbitrary-text-size
  ['text-[10px]', 'text-[length:var(--type-caption-size)]'],
  ['text-[11px]', 'text-[length:var(--type-caption-size)]'],
  ['text-[9px]', 'text-[length:var(--type-caption-size)]'],
  ['text-[12px]', 'text-[length:var(--type-caption-size)]'],
  ['text-[0.7rem]', 'text-[length:var(--type-caption-size)]'],
  ['text-[14px]', 'text-[length:var(--type-body-size)]'],
  ['text-[7px]', 'text-[length:var(--type-caption-size)]'],
  ['text-[8px]', 'text-[length:var(--type-caption-size)]'],
])

// typography/arbitrary-text-size rows M2 left open — no token renders these
// values; refuse rather than snap to the nearest one.
export const NEEDS_DECISION = new Set(['text-[13px]', 'text-[1.15rem]'])

const ALL_KEYS = [...MAPPED.keys(), ...NEEDS_DECISION]

const CLASS_BUILDER_NAMES = new Set(['cn', 'clsx', 'classes', 'cx', 'classNames'])

// An already-resolved type-scale token this lane (or a prior manual fix)
// could have introduced — used only to detect a pre-existing conflict, never
// itself rewritten.
const EXISTING_TOKEN_RE = /\btext-\[(?:length:)?var\(--type-[\w-]+\)\]/g

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function buildTokenRegex(keys) {
  const escaped = [...keys].sort((a, b) => b.length - a.length).map(escapeRegExp)
  return new RegExp(`(?<![\\w-])(${escaped.join('|')})(?![\\w-])`, 'g')
}

const TOKEN_RE = buildTokenRegex(ALL_KEYS)

/** The run of non-whitespace characters immediately before `matchStart` in
 * `raw` — a Tailwind variant-prefix chain (`sm:`, `focus:hover:`, `!`, …) or
 * `''` when the match is unprefixed. Used only to GROUP matches that would
 * collide on the same element (same prefix); never emitted into an edit. */
function prefixOf(raw, matchStart) {
  let i = matchStart
  while (i > 0 && !/\s/.test(raw[i - 1])) i--
  return raw.slice(i, matchStart)
}

function unwrapParens(node) {
  while (node && ts.isParenthesizedExpression(node)) node = node.expression
  return node
}

function isLogicalOperand(kind) {
  return kind === ts.SyntaxKind.AmpersandAmpersandToken || kind === ts.SyntaxKind.BarBarToken
}

function locOf(ctx, absOffset) {
  const lc = ctx.source.getLineAndCharacterOfPosition(absOffset)
  return `${relative(ctx.repoRoot, ctx.filePath)}:${lc.line + 1}:${lc.character + 1}`
}

function relFile(ctx) {
  return relative(ctx.repoRoot, ctx.filePath)
}

/**
 * Finds every FROM-key match and every already-resolved existing-token
 * occurrence in `raw`, groups them by `prefixOf`, and for each group either:
 *   - refuses every FROM-key match in the group, when the group holds more
 *     than one distinct value (a real match conflicting with another real
 *     match, or with an already-resolved token) — "conflicting text-size
 *     token on the same element";
 *   - refuses a single NEEDS-DECISION match;
 *   - edits a single mapped match.
 * A match whose own edge sits exactly at a dynamic boundary
 * (`hasPrecedingDynamic`/`hasFollowingDynamic`) is refused individually,
 * before grouping, and does not participate in conflict grouping.
 */
function processLiteralContent({ raw, rawStart, node, hasPrecedingDynamic, hasFollowingDynamic }, ctx) {
  if (ctx.handledLeaves.has(node)) return
  ctx.handledLeaves.add(node)

  if (hasFollowingDynamic && /\btext-\[?$/.test(raw)) {
    ctx.refusals.push({
      file: relFile(ctx),
      loc: locOf(ctx, rawStart + raw.length),
      syntax: `${raw.slice(Math.max(0, raw.length - 24))}\${…}`,
      reason: 'computed text-size-shaped class segment: a `text-`/`text-[` prefix is immediately followed by an interpolation — the resulting class cannot be proven statically',
    })
  }

  const candidateGroups = new Map()
  const existingByPrefix = new Map()

  let m
  TOKEN_RE.lastIndex = 0
  while ((m = TOKEN_RE.exec(raw))) {
    const key = m[1]
    const start = m.index
    const end = start + key.length
    if (start === 0 && hasPrecedingDynamic) {
      ctx.refusals.push({ file: relFile(ctx), loc: locOf(ctx, rawStart + start), syntax: key, reason: 'match sits at the start of a dynamic-adjacent template/concatenation segment — cannot prove its left boundary statically' })
      continue
    }
    if (end === raw.length && hasFollowingDynamic) {
      ctx.refusals.push({ file: relFile(ctx), loc: locOf(ctx, rawStart + start), syntax: key, reason: 'match sits at the end of a dynamic-adjacent template/concatenation segment — cannot prove its right boundary statically' })
      continue
    }
    const prefix = prefixOf(raw, start)
    if (!candidateGroups.has(prefix)) candidateGroups.set(prefix, [])
    candidateGroups.get(prefix).push({ key, start, end })
  }

  let e
  EXISTING_TOKEN_RE.lastIndex = 0
  while ((e = EXISTING_TOKEN_RE.exec(raw))) {
    const prefix = prefixOf(raw, e.index)
    if (!existingByPrefix.has(prefix)) existingByPrefix.set(prefix, [])
    existingByPrefix.get(prefix).push(e[0])
  }

  for (const [prefix, group] of candidateGroups) {
    const distinctKeys = new Set(group.map((g) => g.key))
    const existingHere = existingByPrefix.get(prefix) ?? []
    const isConflict = distinctKeys.size > 1 || (distinctKeys.size === 1 && existingHere.length > 0)
    if (isConflict) {
      const otherValues = new Set([...distinctKeys, ...existingHere])
      for (const g of group) {
        ctx.refusals.push({
          file: relFile(ctx),
          loc: locOf(ctx, rawStart + g.start),
          syntax: g.key,
          reason: `conflicting text-size utilities on the same class string (prefix \`${prefix || '(none)'}\`): ${[...otherValues].join(', ')}`,
        })
      }
      continue
    }
    for (const g of group) {
      if (NEEDS_DECISION.has(g.key)) {
        ctx.refusals.push({
          file: relFile(ctx),
          loc: locOf(ctx, rawStart + g.start),
          syntax: g.key,
          reason: `\`${g.key}\` is NEEDS-DECISION in M2's mapping (dist/design-system-baseline/cli-lanes/c1-prep/M2/mapping.json) — no existing token renders this value; requires a founder/lead decision before this codemod can rewrite it`,
        })
        continue
      }
      const replacement = MAPPED.get(g.key)
      ctx.edits.push({ start: rawStart + g.start, end: rawStart + g.end, replacement, loc: locOf(ctx, rawStart + g.start), syntax: g.key })
    }
  }
}

function processPlainLiteral(node, ctx) {
  const rawStart = node.getStart(ctx.source) + 1
  const rawEnd = node.getEnd() - 1
  const raw = ctx.text.slice(rawStart, rawEnd)
  processLiteralContent({ raw, rawStart, node, hasPrecedingDynamic: false, hasFollowingDynamic: false }, ctx)
}

function processTemplatePiece(literalNode, hasPrecedingDynamic, hasFollowingDynamic, ctx) {
  const isTailLike = literalNode.kind === ts.SyntaxKind.TemplateTail
  const rawStart = literalNode.getStart(ctx.source) + 1
  const rawEnd = isTailLike ? literalNode.getEnd() - 1 : literalNode.getEnd() - 2
  const raw = ctx.text.slice(rawStart, rawEnd)
  processLiteralContent({ raw, rawStart, node: literalNode, hasPrecedingDynamic, hasFollowingDynamic }, ctx)
}

function processTemplateExpression(node, ctx) {
  const spans = node.templateSpans
  processTemplatePiece(node.head, false, spans.length > 0, ctx)
  spans.forEach((span, i) => {
    const isLastSpan = i === spans.length - 1
    processTemplatePiece(span.literal, true, !isLastSpan, ctx)
    // Also walk the substitution expression itself — the real VideoEmbed.tsx
    // shape (`` `${PANEL_BASE} border-...` ``, PANEL_BASE a module-level
    // `const`): the literal chunks around a span never include the
    // substitution's own text, so without this the identifier/conditional/
    // nested-call INSIDE `${...}` would be invisible here even though its
    // resolved shape is fully composable. This walks the SAME node kinds
    // walkClassExpr already proves elsewhere (identifier resolution, `?:`,
    // nested class-builder calls, …); it does not change what a literal
    // chunk touching the boundary is allowed to do (that stays governed by
    // hasPrecedingDynamic/hasFollowingDynamic in processLiteralContent).
    walkClassExpr(span.expression, ctx)
  })
}

/** Recurses through the shapes proven not to change which string reaches the
 * DOM: parens, both `?:` branches, both `&&`/`||` operands, a nested
 * recognized class-builder call, and array-literal elements. Anything else
 * has no provable string content here and is silently skipped. */
export function walkClassExpr(expr, ctx) {
  if (!expr) return
  const node = unwrapParens(expr)
  if (ts.isStringLiteral(node) || node.kind === ts.SyntaxKind.NoSubstitutionTemplateLiteral) {
    processPlainLiteral(node, ctx)
    return
  }
  if (ts.isTemplateExpression(node)) {
    processTemplateExpression(node, ctx)
    return
  }
  if (ts.isConditionalExpression(node)) {
    walkClassExpr(node.whenTrue, ctx)
    walkClassExpr(node.whenFalse, ctx)
    return
  }
  if (ts.isBinaryExpression(node) && isLogicalOperand(node.operatorToken.kind)) {
    walkClassExpr(node.left, ctx)
    walkClassExpr(node.right, ctx)
    return
  }
  if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_BUILDER_NAMES.has(node.expression.text)) {
    for (const arg of node.arguments) walkClassExpr(arg, ctx)
    return
  }
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'join') {
    // The hand-rolled `[...].join(' ')` class-list shape (real
    // ShellDenyPatternsEditor.tsx: `className={[...].join(' ')}`) — and its
    // `[...].filter(Boolean).join(' ')` variant. `.filter` never changes
    // WHICH surviving element holds a given piece of source text (it only
    // drops falsy entries; none of this lane's FROM keys are falsy), so
    // walking the pre-filter elements proves the exact same source text
    // `.join` will actually emit them from.
    let receiver = node.expression.expression
    if (
      ts.isCallExpression(receiver) && ts.isPropertyAccessExpression(receiver.expression) &&
      receiver.expression.name.text === 'filter'
    ) {
      receiver = receiver.expression.expression
    }
    const arrayNode = unwrapParens(receiver)
    if (ts.isArrayLiteralExpression(arrayNode)) {
      for (const el of arrayNode.elements) walkClassExpr(el, ctx)
    }
    return
  }
  if (ts.isArrayLiteralExpression(node)) {
    for (const el of node.elements) walkClassExpr(el, ctx)
    return
  }
  if (ts.isObjectLiteralExpression(node)) {
    // The "variant map" shape a class-like-named constant commonly takes
    // (`const sizeClasses = { small: 'text-xs', large: 'text-base' }`):
    // walk every plain (non-computed, non-shorthand, non-spread) property's
    // VALUE. This deliberately does NOT support clsx's inverted
    // `{ 'text-xs': condition }` shorthand (class text as the KEY, a
    // boolean as the value) — unused anywhere in this lane's real targets
    // (M2's file lists), so there is no evidence to build and prove a
    // second, opposite-direction reader against.
    for (const prop of node.properties) {
      if (ts.isPropertyAssignment(prop)) walkClassExpr(prop.initializer, ctx)
    }
    return
  }
  if (ts.isIdentifier(node)) {
    // A bare identifier argument to a class-composing call — the real
    // `const textSize = density === 'compact' ? 'text-[10px]' : 'text-xs'`
    // shape (UntrustedChildText.tsx), forwarded as `cn(..., textSize, ...)`
    // without "class" anywhere in its name, so the class-like-name heuristic
    // alone would miss it. Only followed when the name resolves to EXACTLY
    // ONE `const <name> = <expr>` declaration anywhere in the file — zero or
    // multiple candidates is the overwhelmingly common case for an
    // identifier here (a `className` prop forward, a design-system helper
    // result, an unrelated local) and is silently left alone, not refused:
    // refusing every unresolved identifier reaching a class-builder call
    // would flag countless sites with no FROM-key text anywhere in reach.
    // `ctx.resolvingIdentifiers` guards a same-named self/mutual-cycle
    // (`const a = a`) from recursing forever.
    if (ctx.resolvingIdentifiers.has(node.text)) return
    const initializer = resolveSoleConstInitializer(ctx.source, node.text)
    if (!initializer) return
    ctx.resolvingIdentifiers.add(node.text)
    walkClassExpr(initializer, ctx)
    ctx.resolvingIdentifiers.delete(node.text)
    return
  }
  // Unrecognized shape (unknown call, property access, spread, …): no
  // literal text is visible here, so there is nothing to find and nothing
  // to refuse.
}

/** The initializer of the ONE `const <name> = <expr>` declaration matching
 * `name` anywhere in `source` — null when there are zero or more than one
 * (ambiguous; see the identifier-resolution comment in `walkClassExpr`). */
function resolveSoleConstInitializer(source, name) {
  const found = []
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === name && node.initializer) {
      found.push(node.initializer)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return found.length === 1 ? found[0] : null
}

function isJsxClassAttribute(node) {
  return ts.isJsxAttribute(node) && ts.isIdentifier(node.name) && (node.name.text === 'className' || node.name.text === 'class')
}

function handleJsxClassAttribute(node, ctx) {
  const init = node.initializer
  if (!init) return
  if (ts.isStringLiteral(init)) { walkClassExpr(init, ctx); return }
  if (ts.isJsxExpression(init) && init.expression) walkClassExpr(init.expression, ctx)
}

function isClassBuilderCallNode(node) {
  return ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_BUILDER_NAMES.has(node.expression.text)
}

function handleClassBuilderCall(node, ctx) {
  for (const arg of node.arguments) walkClassExpr(arg, ctx)
}

function isCvaCallNode(node) {
  return ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'cva'
}

/** `cva(base, { variants: { group: { key: <classExpr> } }, compoundVariants:
 * [{ ..., class|className: <classExpr> }] })` — walks the base string, every
 * variant map leaf, and every compound variant's class property. */
function handleCva(node, ctx) {
  const [base, config] = node.arguments
  if (base) walkClassExpr(base, ctx)
  if (!config || !ts.isObjectLiteralExpression(config)) return
  for (const prop of config.properties) {
    if (!ts.isPropertyAssignment(prop) || !ts.isIdentifier(prop.name)) continue
    if (prop.name.text === 'variants' && ts.isObjectLiteralExpression(prop.initializer)) {
      for (const groupProp of prop.initializer.properties) {
        if (!ts.isPropertyAssignment(groupProp) || !ts.isObjectLiteralExpression(groupProp.initializer)) continue
        for (const valueProp of groupProp.initializer.properties) {
          if (ts.isPropertyAssignment(valueProp)) walkClassExpr(valueProp.initializer, ctx)
        }
      }
    } else if (prop.name.text === 'compoundVariants' && ts.isArrayLiteralExpression(prop.initializer)) {
      for (const el of prop.initializer.elements) {
        if (!ts.isObjectLiteralExpression(el)) continue
        for (const p of el.properties) {
          if (ts.isPropertyAssignment(p) && ts.isIdentifier(p.name) && (p.name.text === 'class' || p.name.text === 'className')) {
            walkClassExpr(p.initializer, ctx)
          }
        }
      }
    }
  }
}

function isClassLikeVariableDeclaration(node) {
  return ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && /class/i.test(node.name.text) && Boolean(node.initializer)
}

function handleClassLikeVariableDeclaration(node, ctx) {
  walkClassExpr(node.initializer, ctx)
}

/** Processes one file: returns { edits, refusals } — never mutates. Every
 * root rule below runs unconditionally on every matching node (none of them
 * skip descending into children), and `ctx.handledLeaves` de-duplicates any
 * leaf reached by more than one route — see file header. */
export function planFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const ctx = { repoRoot, filePath, text, source, edits: [], refusals: [], handledLeaves: new Set(), resolvingIdentifiers: new Set() }

  const visit = (node) => {
    if (isJsxClassAttribute(node)) handleJsxClassAttribute(node, ctx)
    else if (isCvaCallNode(node)) handleCva(node, ctx)
    else if (isClassBuilderCallNode(node)) handleClassBuilderCall(node, ctx)
    else if (isClassLikeVariableDeclaration(node)) handleClassLikeVariableDeclaration(node, ctx)
    ts.forEachChild(node, visit)
  }
  visit(source)

  return { edits: ctx.edits, refusals: ctx.refusals, text }
}

export function runCodemod({ repoRoot, apply }) {
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
