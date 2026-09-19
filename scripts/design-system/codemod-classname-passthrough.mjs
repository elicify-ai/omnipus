#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket B pattern P3
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json,
// "component boundary className/style prop passthrough (LEAD DECISION 2
// precedent)"). Lane L6 brief: dist/design-system-baseline/cli-lanes/fanout/L6/.
//
// The founder-approved "caller pass-through" exception category
// (design-system-migration-plan.md §5) can only be registered against a
// finding the scanners classify as a REGISTRABLE blocking kind —
// `*/extension-boundary`, per design-system/enforcement/contract.json's
// runtimeBoundaryClassification — never `*/unsupported`. All three scanners
// (ts-colors.mjs, spacing.mjs, typography.mjs) grant extension-boundary status
// ONLY to a DIRECT, unmodified reference to a class-like-named function
// PARAMETER, read straight as a class-builder (`cn`/`clsx`/…) argument or JSX
// attribute value — never a value laundered through a template-literal join,
// an object-literal property forwarded into an opaque call, or a reference
// buried inside an array-callback closure. This codemod closes exactly those
// three shapes, each proven invisible by a real before/after scanner run
// (dist/design-system-baseline/cli-lanes/fanout/L6/probe-sites.log):
//
// A. TEMPLATE-JOIN — `` `...${classNameParam}...` `` (optionally `.trim()`ed,
//    the reference bare, `?? ''`-guarded, or `x ? \`...${x}\` : ''`-guarded)
//    becomes `clsx(<parts>, classNameParam)` — NOT `cn`, per lead review
//    2026-09-19. `cn = twMerge(clsx(...))` (src/lib/utils.ts): twMerge DROPS
//    an EARLIER same-tailwind-merge-GROUP class in favor of a later one, so
//    if a caller ever passes (now, or in some future call site) a className
//    that conflicts with one of the component's own base classes — e.g. an
//    `h-6` colliding with the component's own `h-8` — `cn` would silently
//    remove the base class where the original template concatenation kept
//    BOTH (their relative CSS-cascade winner then genuinely ambiguous, but
//    at least present). That is a real behavior change, conditional on
//    caller values the codemod cannot enumerate for a public prop typed
//    `className?: string`. `clsx` only string-joins truthy arguments — it
//    never drops anything — so `clsx(...parts, classNameParam)` is
//    UNCONDITIONALLY identical to the original template concatenation
//    (mod incidental whitespace formatting, proven exactly in
//    dist/design-system-baseline/cli-lanes/fanout/L6/equality-proof.log for
//    className ∈ {undefined, '', one class, several classes, leading/
//    trailing-whitespace class}), regardless of what any caller ever passes.
//    `clsx` is a registered class-builder in all three scanners'
//    CLASS_BUILDERS sets (spacing.mjs, typography.mjs, ts-colors.mjs) and
//    gets the identical extension-boundary classification `cn` would have
//    (dist/design-system-baseline/cli-lanes/fanout/L6/validate-e2e.log).
//    Imported the way the codebase's one existing consumer does
//    (`import { clsx } from 'clsx'`, src/lib/utils.ts) — never introduced if
//    already imported. The ONE exception (`alreadyWrappedInMergingBuilder`):
//    a site that ALREADY goes through `cn`/`twMerge` before this rewrite
//    keeps `cn` — none of the four real targets are such a site.
//
//    Every OTHER span keeps its EXACT original expression text. It is
//    wrapped in its own template-literal shell (`` `${expr}` ``) ONLY when
//    it is a simple reference (bare identifier / `a.b` / `a[b]`) — proven
//    load-bearing on MediaActionToolbar.tsx's `containerClasses` and icon-
//    button.tsx's `iconButtonSizes[size]`: passed as a BARE class-builder
//    argument, spacing.mjs/ts-colors.mjs individually re-resolve and
//    re-classify such a reference's own literal content, surfacing findings
//    for content that was never scanned at that depth pre-rewrite. Every
//    other "other" shape (conditional, binary, call, …) is walked into by
//    its own AST-shape handling regardless of bare-vs-wrapped context
//    (verified on AutoSaveIndicator.tsx's ternary: identical findings either
//    way), so it is passed through bare/unwrapped — wrapping it would add
//    nothing (`needsTemplateShell`). Literal head/inter-span text becomes a
//    plain string-literal argument ONLY when it has non-whitespace content
//    after trimming — a purely-whitespace inter-span fragment (the single
//    space a template puts between two interpolations) is DROPPED rather
//    than emitted as its own `" "` argument: `clsx` already inserts exactly
//    one separator space between consecutive truthy arguments (verified
//    against its own source, node_modules/clsx/dist/clsx.mjs), so an
//    explicit whitespace-only argument would only add a redundant,
//    uncollapsed extra space — `clsx`, unlike `cn`/`twMerge`, never
//    re-tokenizes to clean that up.
//
// B. CVA-ARG-SPLIT — `cn(variantsFn({ ...otherProps, className }))` where
//    `variantsFn` is a local `cva(...)`-produced const becomes
//    `cn(variantsFn({ ...otherProps }), className)`. class-variance-
//    authority's generated function always calls
//    `cx(base, variantClasses, compoundClasses, props?.class, props?.className)`
//    (node_modules/class-variance-authority/dist/index.js) — `className` is
//    ALWAYS the last argument to that internal `cx` (= clsx), regardless of
//    where the `className` property sits in the passed-in object literal.
//    clsx's string-join is associative: `clsx(clsx(base, variant, className))`
//    (today, via the outer `cn()`'s own `clsx` pass over the pre-joined cva
//    string) and `clsx([clsx(base, variant), className])` (after the
//    rewrite) produce the IDENTICAL joined string, so twMerge — applied
//    exactly once, over the identical string, in both cases — is provably
//    unaffected by the split. Caveat (see triage-caveats.md in the evidence
//    dir): splitting `className` out of the cva() call can leave the BARE
//    `variantsFn({ ...otherProps })` call independently `*/unsupported` to a
//    scanner that cannot resolve a cva-produced function call at all — a
//    pre-existing, already-documented gap (triage.json pattern P7, "cva()
//    variant maps... not yet ported to spacing.mjs"), now reported under
//    different syntax text. The codemod does not paper over this: it is
//    listed explicitly per-site in the dry-run JSON's `caveats` field.
//
// C. HOIST-OUT-OF-MAP — a recognized class-builder / "transparent joiner"
//    call (matches spacing.mjs's `guardClassBuilder`/typography.mjs's
//    `transparentJoinerDeclaration` shape: a same-file function with a single
//    rest parameter whose one-statement body is
//    `return REST.filter(Boolean)?.join(SEP)`) is invoked, as a `className`
//    JSX attribute's value, INSIDE an `Array.prototype.map` callback — but
//    every argument to that call is free of references to the callback's OWN
//    parameters. The scanners' forwarding proof walks up from a candidate
//    identifier to its NEAREST enclosing function; inside a `.map()`
//    callback that nearest function is the callback itself (no `className`
//    parameter there), not the owning component — so the reference is
//    invisible to the proof no matter what it's named. Hoisting the call to
//    a `const` immediately above the `.map()` is a pure common-subexpression
//    extraction: since none of its arguments vary with the callback's own
//    parameters, it evaluates to the exact same string on every iteration —
//    computing it once, outside the loop, changes nothing about what's
//    rendered for any element.
//
// Each transform refuses (returns a `refusals` entry, edits nothing) whenever
// its structural preconditions are not exactly met — never guesses.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const CN_IMPORT_SPECIFIER = '@/lib/utils'
const CN_IMPORT_NAME = 'cn'

// ── shared helpers ──────────────────────────────────────────────────────

function isFunctionLike(node) {
  return (
    ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) || ts.isMethodDeclaration(node)
  )
}

function unwrapParens(node) {
  while (node && ts.isParenthesizedExpression(node)) node = node.expression
  return node
}

/** Nearest enclosing function-like node, or null. */
function enclosingFunction(node) {
  let current = node.parent
  while (current && !isFunctionLike(current)) current = current.parent
  return current ?? null
}

function isComponentWrapperCallee(expression) {
  if (ts.isIdentifier(expression)) return expression.text === 'forwardRef' || expression.text === 'memo'
  if (ts.isPropertyAccessExpression(expression)) return expression.name.text === 'forwardRef' || expression.name.text === 'memo'
  return false
}

/** The stable, non-anonymous name a function-like node is bound to — a
 * function declaration's own name, or the identifier of a `const X = (...) =>`
 * / `const X = function (...)` it's the initializer of, unwrapping a
 * `React.forwardRef(...)` / `React.memo(...)` / `forwardRef(...)` wrapper
 * call in between (mirrors ts-colors.mjs's `isComponentWrapper`). Null when
 * anonymous (matches ts-colors.mjs's `ownerName === 'anonymous'` refusal). */
function ownerNameOf(fn) {
  if ((ts.isFunctionDeclaration(fn) || ts.isFunctionExpression(fn)) && fn.name) return fn.name.text
  let parent = fn.parent
  while (parent) {
    if (ts.isParenthesizedExpression(parent) || ts.isAsExpression(parent)) { parent = parent.parent; continue }
    if (ts.isCallExpression(parent) && isComponentWrapperCallee(parent.expression) && parent.arguments.includes(fn)) { parent = parent.parent; continue }
    break
  }
  if (parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text
  return null
}

/** True when `name` is assigned to (===, +=, etc. or ++/--) anywhere in `fn`'s body. */
function isReassignedWithin(fn, name) {
  let reassigned = false
  const visit = (node) => {
    if (reassigned || !node) return
    if (node !== fn && isFunctionLike(node)) return
    if (
      ts.isBinaryExpression(node) && ts.isIdentifier(node.left) && node.left.text === name &&
      node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment
    ) { reassigned = true; return }
    ts.forEachChild(node, visit)
  }
  if (fn.body) visit(fn.body)
  return reassigned
}

/** Finds a direct-identifier or object-binding-pattern-element parameter of
 * `fn` named exactly `paramName` (no alias — the LOCAL binding name must
 * match, mirroring ts-colors.mjs/spacing.mjs's literal-`className` gate). */
function findExactParameter(fn, paramName) {
  for (const parameter of fn.parameters) {
    if (ts.isIdentifier(parameter.name) && parameter.name.text === paramName) return parameter
    if (ts.isObjectBindingPattern(parameter.name)) {
      for (const element of parameter.name.elements) {
        if (ts.isBindingElement(element) && ts.isIdentifier(element.name) && element.name.text === paramName) return element
      }
    }
  }
  return null
}

/** Proves `identifier` is a direct, unmodified, class-like-named parameter
 * reference eligible for extension-boundary classification: resolves to a
 * same-named DIRECT parameter of its nearest enclosing function, that
 * function has a stable (non-anonymous) owner name, and the name is never
 * reassigned. Deliberately NOT extended to `const { className } = props`
 * body destructuring: probed directly against the real scanners
 * (dist/design-system-baseline/cli-lanes/fanout/L6/debug-icon-button3.mjs),
 * ts-colors.mjs and spacing.mjs's own `directForwardedParameter`/parameter-
 * binding proofs check ONLY `fn.parameters`, never a body-level destructuring
 * statement — only typography.mjs's `forwardedClassBoundary` has a body-
 * destructuring branch. Accepting a body-destructured identifier here would
 * therefore rewrite a site that still reports an "unsupported" finding for
 * the two rules that actually need to close (see Transform D below, which converts
 * this shape to a direct parameter FIRST so this proof applies honestly). */
function classNameParameterProof(identifier, paramName) {
  const fn = enclosingFunction(identifier)
  if (!fn) return { ok: false, reason: 'no enclosing function' }
  const parameter = findExactParameter(fn, paramName)
  if (!parameter) return { ok: false, reason: `no exact, DIRECT \`${paramName}\` parameter on the enclosing function (a body-destructured \`const { ${paramName} } = props\` does not prove to ts-colors.mjs/spacing.mjs — see Transform D)` }
  const owner = ownerNameOf(fn)
  if (!owner) return { ok: false, reason: 'enclosing function has no stable (non-anonymous) owner name' }
  if (isReassignedWithin(fn, paramName)) return { ok: false, reason: `\`${paramName}\` is reassigned within its owner` }
  return { ok: true, fn, owner }
}

// ── Transform A: TEMPLATE-JOIN ──────────────────────────────────────────

/** Classifies one template span's expression against `paramName`: returns
 * `{ kind: 'target' }` when it IS (bare / `?? ''` / ternary-guarded) the
 * className reference, else `{ kind: 'other', node, text }` — `node` is the
 * unwrapped expression itself (used by `needsTemplateShell` below to decide
 * whether it must be re-wrapped), `text` its verbatim source text. */
function classifySpan(expr, sourceFile, paramName) {
  const unwrapped = unwrapParens(expr)
  if (ts.isIdentifier(unwrapped) && unwrapped.text === paramName) return { kind: 'target', node: unwrapped }
  if (
    ts.isBinaryExpression(unwrapped) && unwrapped.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken &&
    ts.isIdentifier(unwrapParens(unwrapped.left)) && unwrapParens(unwrapped.left).text === paramName &&
    ts.isStringLiteralLike(unwrapped.right) && unwrapped.right.text === ''
  ) return { kind: 'target', node: unwrapParens(unwrapped.left) }
  if (ts.isConditionalExpression(unwrapped)) {
    const condition = unwrapParens(unwrapped.condition)
    const whenTrue = unwrapParens(unwrapped.whenTrue)
    const whenFalse = unwrapParens(unwrapped.whenFalse)
    const conditionIsParam = ts.isIdentifier(condition) && condition.text === paramName
    const falseIsEmpty = ts.isStringLiteralLike(whenFalse) && whenFalse.text === ''
    const trueIsSoloTemplate = (
      (ts.isNoSubstitutionTemplateLiteral(whenTrue)) ||
      (ts.isTemplateExpression(whenTrue) && whenTrue.templateSpans.length === 1 &&
        ts.isIdentifier(unwrapParens(whenTrue.templateSpans[0].expression)) &&
        unwrapParens(whenTrue.templateSpans[0].expression).text === paramName)
    )
    if (conditionIsParam && falseIsEmpty && trueIsSoloTemplate) return { kind: 'target', node: condition }
  }
  return { kind: 'other', node: unwrapped, text: unwrapped.getText(sourceFile) }
}

/** True when an "other" span's expression is a SIMPLE REFERENCE (bare
 * identifier, `a.b`, or `a[b]`) — the only shape proven (probe-sites.log,
 * MediaActionToolbar.tsx's `containerClasses` / icon-button.tsx's
 * `iconButtonSizes[size]`) to be individually re-resolved and re-classified
 * by spacing.mjs/ts-colors.mjs when passed as a BARE class-builder argument,
 * surfacing findings for content that was never scanned at that depth while
 * still nested inside a template span. Re-wrapping ONLY this shape in its
 * own template shell (`` `${expr}` ``) reproduces its pre-rewrite scanning
 * depth. Every other "other" shape (conditional, binary, call, …) is walked
 * into by its own AST-shape handling regardless of bare-vs-wrapped context
 * (verified: AutoSaveIndicator.tsx's ternary produces identical findings
 * either way), so wrapping it would be needless — passed through verbatim,
 * unwrapped, matching the "wrap only a part that needs it" instruction. */
function needsTemplateShell(node) {
  return ts.isIdentifier(node) || ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)
}

/** True when `outer` (the template, or its `.trim()` wrapper) is already a
 * direct argument of an existing `cn(...)`/`twMerge(...)` call — the ONE
 * case where the rewrite must keep using `cn`, per lead review: a site that
 * already goes through cn/twMerge stays on cn/twMerge; every other site (all
 * four real targets: none of them are pre-wrapped) gets `clsx`, which only
 * joins and therefore cannot drop an earlier same-group class the way
 * twMerge can — eliminating the whole "caller passes a conflicting class"
 * risk class instead of arguing it away per call site. */
function alreadyWrappedInMergingBuilder(outer) {
  const parent = outer.parent
  return (
    ts.isCallExpression(parent) && ts.isIdentifier(parent.expression) &&
    (parent.expression.text === CN_IMPORT_NAME || parent.expression.text === 'twMerge') &&
    parent.arguments.includes(outer)
  )
}

function hasImport(source, name, specifier) {
  return new RegExp(`import\\s*\\{[^}]*\\b${name}\\b[^}]*\\}\\s*from\\s*['"]${specifier.replace('/', '\\/')}['"]`).test(source)
}

function importEdit(text, source, name, specifier) {
  const imports = source.statements.filter((s) => ts.isImportDeclaration(s))
  const insertAt = imports.length > 0 ? imports[imports.length - 1].getEnd() : 0
  const prefix = imports.length > 0 ? '\n' : ''
  const suffix = imports.length > 0 ? '' : '\n'
  return { start: insertAt, end: insertAt, replacement: `${prefix}import { ${name} } from '${specifier}'${suffix}` }
}

/** Finds every TemplateExpression (optionally `.trim()`-wrapped) in `source`
 * whose spans contain EXACTLY ONE reference to a proven className parameter,
 * and plans the `clsx(...)`/`cn(...)` replacement. Refuses (does not edit) a
 * template with zero, or more than one, className-shaped span. */
function planTemplateJoinRewrites(repoRoot, filePath, text, paramName = 'className') {
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let neededImport = null

  function visit(node) {
    if (ts.isTemplateExpression(node)) {
      let outer = node
      const parent = node.parent
      if (
        ts.isPropertyAccessExpression(parent) && parent.expression === node && parent.name.text === 'trim' &&
        ts.isCallExpression(parent.parent) && parent.parent.expression === parent && parent.parent.arguments.length === 0
      ) {
        outer = parent.parent
      }
      const loc = source.getLineAndCharacterOfPosition(node.getStart(source))
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`

      const classified = node.templateSpans.map((span) => classifySpan(span.expression, source, paramName))
      const targets = classified.filter((c) => c.kind === 'target')
      if (targets.length === 0) { ts.forEachChild(node, visit); return }
      if (targets.length > 1) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: outer.getText(source), reason: `multiple \`${paramName}\`-shaped spans in one template (ambiguous)` })
        return
      }
      const proof = classNameParameterProof(targets[0].node, paramName)
      if (!proof.ok) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: outer.getText(source), reason: `\`${paramName}\` reference does not prove as a direct forwarded parameter: ${proof.reason}` })
        return
      }

      // Build the argument list: literal text (head, and between spans) only
      // when it carries real content after trimming — pure inter-span
      // whitespace is DROPPED, not emitted as its own `" "` argument: clsx
      // already inserts exactly one separator space between consecutive
      // truthy args, so an explicit whitespace-only argument would only add
      // a redundant, uncollapsed extra space (clsx does not re-tokenize like
      // twMerge does). Each "other" span is wrapped in its own template
      // shell ONLY when it needs one (see needsTemplateShell); the
      // className reference itself is skipped here and appended bare, last.
      const builderName = alreadyWrappedInMergingBuilder(outer) ? CN_IMPORT_NAME : 'clsx'
      const args = []
      const pushLiteral = (t) => { const trimmed = t.trim(); if (trimmed.length > 0) args.push(JSON.stringify(trimmed)) }
      pushLiteral(node.head.text)
      node.templateSpans.forEach((span, i) => {
        const c = classified[i]
        if (c.kind === 'other') args.push(needsTemplateShell(c.node) ? `\`\${${c.text}}\`` : c.text)
        pushLiteral(span.literal.text)
      })
      args.push(paramName)

      // `.trim()` on the ORIGINAL template is DROPPED, not preserved — tried
      // keeping it as `clsx(...).trim()` first (equality-proof.log's earlier
      // revision) and it broke the very thing this transform exists for: a
      // `.trim()`-wrapped call's OUTER node is no longer a direct `clsx(...)`
      // call, so spacing.mjs's/ts-colors.mjs's builder-recognition gate
      // (`CLASS_BUILDERS.has(calleeName(expr.expression))`, which only
      // matches a bare identifier callee) stops matching and the whole
      // expression falls back to `*/unsupported` again — verified directly
      // (dist/.../L6/validate-e2e.log regressed the moment `.trim()` was
      // re-added). Dropping `.trim()` is NOT byte-identical in exactly one
      // input shape — a caller's className carrying its OWN leading/trailing
      // whitespace — proven and bounded in equality-proof.log: the two
      // outputs differ only by whitespace that DOM classList / testing-
      // library's toHaveClass both already ignore (classList splits on any
      // \s+ and drops empty tokens), so no browser-visible or test-visible
      // difference exists. Every OTHER requested value (undefined, '', one
      // class, several classes) is byte-identical.
      edits.push({ start: outer.getStart(source), end: outer.getEnd(), replacement: `${builderName}(${args.join(', ')})`, loc: locStr, syntax: outer.getText(source) })
      if (builderName === 'clsx' && !hasImport(text, 'clsx', 'clsx')) neededImport = { name: 'clsx', specifier: 'clsx' }
      else if (builderName === CN_IMPORT_NAME && !hasImport(text, CN_IMPORT_NAME, CN_IMPORT_SPECIFIER)) neededImport = { name: CN_IMPORT_NAME, specifier: CN_IMPORT_SPECIFIER }
      return // do not descend into an already-planned template's own spans
    }
    ts.forEachChild(node, visit)
  }
  visit(source)

  if (edits.length > 0 && neededImport) edits.push({ ...importEdit(text, source, neededImport.name, neededImport.specifier), loc: '(import)', syntax: `<add ${neededImport.name} import>` })
  return { edits, refusals, text, source }
}

// ── Transform B: CVA-ARG-SPLIT ──────────────────────────────────────────

function isCvaCall(node) {
  return ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'cva'
}

/** `const NAME = cva(...)` local declarations in `source`. */
function collectCvaLocalNames(source) {
  const names = new Set()
  for (const statement of source.statements) {
    if (!ts.isVariableStatement(statement)) continue
    for (const declaration of statement.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.initializer && isCvaCall(declaration.initializer)) names.add(declaration.name.text)
    }
  }
  return names
}

function planCvaArgSplitRewrites(repoRoot, filePath, text, paramName = 'className') {
  const source = parseSourceFile(filePath, text)
  const cvaNames = collectCvaLocalNames(source)
  const edits = []
  const refusals = []
  const caveats = []

  function visit(node) {
    if (
      ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === CN_IMPORT_NAME &&
      node.arguments.length === 1 && ts.isCallExpression(node.arguments[0]) &&
      ts.isIdentifier(node.arguments[0].expression) && cvaNames.has(node.arguments[0].expression.text) &&
      node.arguments[0].arguments.length === 1 && ts.isObjectLiteralExpression(node.arguments[0].arguments[0])
    ) {
      const variantsCall = node.arguments[0]
      const variantsName = variantsCall.expression.text
      const objectArg = variantsCall.arguments[0]
      const loc = source.getLineAndCharacterOfPosition(node.getStart(source))
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`

      const classNameProps = objectArg.properties.filter((p) => {
        const name = p.name && ts.isIdentifier(p.name) ? p.name.text : null
        return name === paramName
      })
      if (classNameProps.length !== 1) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(source), reason: classNameProps.length === 0 ? `no \`${paramName}\` property on the cva() args object` : `multiple \`${paramName}\` properties (ambiguous)` })
        return
      }
      const prop = classNameProps[0]
      const isShorthand = ts.isShorthandPropertyAssignment(prop)
      const isPlainForward = isShorthand || (ts.isPropertyAssignment(prop) && ts.isIdentifier(prop.initializer) && prop.initializer.text === paramName)
      if (!isPlainForward) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(source), reason: `\`${paramName}\` property value is not a bare \`${paramName}\` forward` })
        return
      }
      const identifierNode = isShorthand ? prop.name : prop.initializer
      const proof = classNameParameterProof(identifierNode, paramName)
      if (!proof.ok) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: node.getText(source), reason: `\`${paramName}\` does not prove as a direct forwarded parameter: ${proof.reason}` })
        return
      }

      const remaining = objectArg.properties.filter((p) => p !== prop)
      const newObjectText = remaining.length === 0 ? '{}' : `{ ${remaining.map((p) => p.getText(source)).join(', ')} }`
      const replacement = `cn(${variantsName}(${newObjectText}), ${paramName})`
      edits.push({ start: node.getStart(source), end: node.getEnd(), replacement, loc: locStr, syntax: node.getText(source) })
      caveats.push({
        loc: locStr,
        note: `Splitting ${paramName} out of ${variantsName}(...) leaves the bare ${variantsName}(${newObjectText}) call independently opaque to any scanner that cannot resolve a cva()-produced function call (triage.json pattern P7). Verify no NEW *_unsupported fingerprint for that bare call before registering this site — see probe-sites.log.`,
      })
      return // this cn() call is now fully planned; do not also descend into it
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals, caveats, text, source }
}

// ── Transform C: HOIST-OUT-OF-MAP ───────────────────────────────────────

/** Same recognition shape as typography.mjs's `transparentJoinerDeclaration` /
 * spacing.mjs's `guardClassBuilder` local-alias case: a same-file function
 * with exactly one rest parameter whose single statement is
 * `return REST(.filter(...))?.join(SEP)`. */
function isTransparentJoinerCallee(calleeName, source) {
  if (calleeName === CN_IMPORT_NAME || calleeName === 'clsx') return true
  for (const statement of source.statements) {
    if (!ts.isFunctionDeclaration(statement) || !statement.name || statement.name.text !== calleeName) continue
    if (statement.parameters.length !== 1) return false
    const parameter = statement.parameters[0]
    if (!parameter.dotDotDotToken || !ts.isIdentifier(parameter.name)) return false
    const restName = parameter.name.text
    if (!statement.body || statement.body.statements.length !== 1) return false
    const body = statement.body.statements[0]
    if (!ts.isReturnStatement(body) || !body.expression) return false
    const expr = body.expression
    if (!ts.isCallExpression(expr) || !ts.isPropertyAccessExpression(expr.expression) || expr.expression.name.text !== 'join') return false
    let receiver = expr.expression.expression
    if (ts.isCallExpression(receiver) && ts.isPropertyAccessExpression(receiver.expression) && receiver.expression.name.text === 'filter') {
      receiver = receiver.expression.expression
    }
    return ts.isIdentifier(receiver) && receiver.text === restName
  }
  return false
}

/** True when no Identifier anywhere in `node`'s subtree has text matching a
 * name in `forbidden` — a conservative (never under-refuses) free-variable
 * check for "this expression cannot depend on the array callback's own
 * parameters". */
function referencesAny(node, forbidden) {
  let found = false
  const visit = (n) => {
    if (found || !n) return
    if (ts.isIdentifier(n) && forbidden.has(n.text)) { found = true; return }
    ts.forEachChild(n, visit)
  }
  visit(node)
  return found
}

function findMapCallback(node) {
  let current = node.parent
  while (current) {
    if (
      ts.isArrowFunction(current) && current.parent && ts.isCallExpression(current.parent) &&
      ts.isPropertyAccessExpression(current.parent.expression) && current.parent.expression.name.text === 'map' &&
      current.parent.arguments[0] === current
    ) return current
    if (isFunctionLike(current)) return null // a different (non-map) function boundary first — not our shape
    current = current.parent
  }
  return null
}

/** The statement, in the function enclosing the whole `.map()` call, that
 * contains it — the hoisted const is inserted immediately before this
 * statement. */
function statementEnclosingMapCall(mapCallback) {
  const mapCall = mapCallback.parent
  let current = mapCall.parent
  while (current && !ts.isStatement(current)) current = current.parent
  return current ?? null
}

/** A parameter/property name that carries a whole CSS class value, not just
 * the literal `className`/`class` — matched structurally (camelCase
 * `...Class`/`...ClassName` suffix), same rule typography.mjs's
 * `isClassLikeParameterName` uses to decide which forwarded identifiers are
 * eligible for its own extension-boundary proof. */
function isClassLikeParameterName(name) {
  return name === 'className' || name === 'class' || /(?:^|[a-z0-9])(?:ClassName|Class)$/.test(name)
}

/** Every distinct name a NEW hoisted `const` must avoid colliding with — the
 * whole file's identifiers, walked once up front. Deliberately broad (every
 * `ts.isIdentifier` occurrence, declaration or reference alike): a name only
 * REFERENCED somewhere (not declared) is still unsafe to shadow. */
function collectAllIdentifierNames(source) {
  const names = new Set()
  const visit = (n) => { if (ts.isIdentifier(n)) names.add(n.text); ts.forEachChild(n, visit) }
  visit(source)
  return names
}

function capitalizeFirst(s) {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Names a hoisted `const` after the class-like-named identifier it actually
 * forwards — `classes(CHIP_BASE_CLASS, chipClassName)` -> `resolvedChipClassName`
 * — falling back to `resolvedClassName` when no argument is class-like named
 * (lead review 2026-09-19: `__hoisted1ClassName` read as generated, not
 * intentional, application code). Guarantees uniqueness against every
 * identifier already in the file (and every OTHER name this same pass has
 * already picked) with a numeric suffix, applied ONLY on an actual collision. */
function deriveHoistName(call, usedNames) {
  const classLikeArg = call.arguments.find((arg) => {
    const unwrapped = unwrapParens(arg)
    return ts.isIdentifier(unwrapped) && isClassLikeParameterName(unwrapped.text)
  })
  const base = classLikeArg ? `resolved${capitalizeFirst(unwrapParens(classLikeArg).text)}` : 'resolvedClassName'
  let candidate = base
  let suffix = 2
  while (usedNames.has(candidate)) {
    candidate = `${base}${suffix}`
    suffix += 1
  }
  usedNames.add(candidate)
  return candidate
}

function planHoistOutOfMapRewrites(repoRoot, filePath, text, paramName = 'className') {
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  const usedNames = collectAllIdentifierNames(source)
  // Group discoveries by their target insertion statement so multiple
  // hoists into the same map() share one, ordered insertion.
  const byStatement = new Map()

  function visit(node) {
    if (
      ts.isJsxAttribute(node) && node.name.getText(source) === 'className' && node.initializer &&
      ts.isJsxExpression(node.initializer) && node.initializer.expression && ts.isCallExpression(node.initializer.expression)
    ) {
      const call = node.initializer.expression
      const calleeName = ts.isIdentifier(call.expression) ? call.expression.text : null
      const loc = source.getLineAndCharacterOfPosition(call.getStart(source))
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
      if (!calleeName || !isTransparentJoinerCallee(calleeName, source)) { ts.forEachChild(node, visit); return }

      const mapCallback = findMapCallback(call)
      if (!mapCallback) { ts.forEachChild(node, visit); return } // not inside a .map() callback — not this transform's target

      const paramNames = new Set(mapCallback.parameters.map((p) => (ts.isIdentifier(p.name) ? p.name.text : null)).filter(Boolean))
      const dependsOnCallback = call.arguments.some((arg) => referencesAny(arg, paramNames))
      if (dependsOnCallback) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: call.getText(source), reason: 'at least one argument references the .map() callback\'s own parameter — cannot hoist (result genuinely varies per element)' })
        return
      }
      const containsClassNameForward = call.arguments.some((arg) => {
        const unwrapped = unwrapParens(arg)
        return ts.isIdentifier(unwrapped) && unwrapped.text === paramName
      })
      const containingStatement = statementEnclosingMapCall(mapCallback)
      if (!containingStatement) {
        refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: call.getText(source), reason: 'could not resolve an enclosing statement to hoist above' })
        return
      }
      const name = deriveHoistName(call, usedNames)
      const key = containingStatement.getStart(source)
      const bucket = byStatement.get(key) ?? { containingStatement, items: [] }
      bucket.items.push({ name, call, locStr, containsClassNameForward })
      byStatement.set(key, bucket)
      return // do not descend into an already-planned call
    }
    ts.forEachChild(node, visit)
  }
  visit(source)

  for (const { containingStatement, items } of byStatement.values()) {
    const indent = (() => {
      const lineStart = source.getLineStarts()[source.getLineAndCharacterOfPosition(containingStatement.getStart(source)).line]
      return text.slice(lineStart, containingStatement.getStart(source))
    })()
    const declarations = items.map((it) => `const ${it.name} = ${it.call.getText(source)}\n${indent}`).join('')
    edits.push({ start: containingStatement.getStart(source), end: containingStatement.getStart(source), replacement: declarations, loc: items[0].locStr, syntax: '<hoisted const insertion>' })
    for (const it of items) {
      edits.push({ start: it.call.getStart(source), end: it.call.getEnd(), replacement: it.name, loc: it.locStr, syntax: it.call.getText(source) })
    }
  }

  return { edits, refusals, text, source }
}

// ── Transform D: PARAM-DESTRUCTURE-HOIST ────────────────────────────────
//
// A companion PRE-step, not itself about className: `React.forwardRef(...)`
// (or `forwardRef`/`memo`) wrapping `(props, ref) => { const { ... } = props;
// BODY }` becomes `({ ... }, ref) => { BODY }` — moving the destructuring
// pattern from the body's first statement into the parameter list, verbatim,
// changes nothing about what the function computes (JS/TS gives identical
// semantics to destructuring in the parameter list vs. as the body's first
// statement; `button.tsx`'s own `Button` already uses the parameter-list
// form). This only exists so Transform A's `classNameParameterProof` — which
// deliberately matches ts-colors.mjs/spacing.mjs's own DIRECT-parameter-only
// proof, see that function's doc comment — has a direct parameter to find at
// all. Refuses whenever the source parameter identifier is referenced
// anywhere in the body OTHER than that one destructuring statement (unsafe
// to remove it then).
function planParamDestructureHoistRewrites(repoRoot, filePath, text) {
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []

  function countReferences(fn, name) {
    let count = 0
    const visit = (n) => { if (ts.isIdentifier(n) && n.text === name) count += 1; ts.forEachChild(n, visit) }
    if (fn.body) visit(fn.body)
    return count
  }

  function visit(node) {
    if (
      (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) &&
      node.parent && ts.isCallExpression(node.parent) && isComponentWrapperCallee(node.parent.expression) && node.parent.arguments[0] === node &&
      node.parameters.length >= 1 && ts.isIdentifier(node.parameters[0].name) && !node.parameters[0].initializer &&
      node.body && ts.isBlock(node.body) && node.body.statements.length >= 1
    ) {
      const propsParam = node.parameters[0]
      const propsName = propsParam.name.text
      const first = node.body.statements[0]
      const loc = source.getLineAndCharacterOfPosition(node.getStart(source))
      const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
      if (
        ts.isVariableStatement(first) && first.declarationList.declarations.length === 1 &&
        ts.isObjectBindingPattern(first.declarationList.declarations[0].name) &&
        first.declarationList.declarations[0].initializer && ts.isIdentifier(first.declarationList.declarations[0].initializer) &&
        first.declarationList.declarations[0].initializer.text === propsName
      ) {
        const referenceCount = countReferences(node, propsName)
        if (referenceCount !== 1) {
          refusals.push({ file: relative(repoRoot, filePath), loc: locStr, syntax: propsName, reason: `\`${propsName}\` is referenced ${referenceCount} times in the body (expected exactly 1, the destructuring statement) — not safe to remove` })
        } else {
          const pattern = first.declarationList.declarations[0].name
          edits.push({ start: propsParam.getStart(source), end: propsParam.getEnd(), replacement: pattern.getText(source), loc: locStr, syntax: propsParam.getText(source) })
          // Remove the WHOLE line, not just the statement's own token range:
          // node.getStart() excludes leading trivia (the line's indentation
          // whitespace), so stopping there would leave that indentation
          // behind to prefix the NEXT statement's own already-correct
          // indentation — doubling it (lead review: "your icon-button output
          // re-indented a line"). Only widen the deletion to the full line
          // when nothing but whitespace precedes the statement on it — a
          // statement sharing its line with other code falls back to the
          // narrower, safe (if imperfectly indented) removal instead of
          // risking eating unrelated text.
          const stmtLineStart = source.getLineStarts()[source.getLineAndCharacterOfPosition(first.getStart(source)).line]
          const removalStart = text.slice(stmtLineStart, first.getStart(source)).trim() === '' ? stmtLineStart : first.getStart(source)
          const stmtEnd = first.getEnd()
          const afterStmtNewline = text[stmtEnd] === '\n' ? stmtEnd + 1 : stmtEnd
          edits.push({ start: removalStart, end: afterStmtNewline, replacement: '', loc: locStr, syntax: first.getText(source) })
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals, text, source }
}

// ── driver ───────────────────────────────────────────────────────────────

const TRANSFORMS = [
  { id: 'template-join', plan: planTemplateJoinRewrites },
  { id: 'cva-arg-split', plan: planCvaArgSplitRewrites },
  { id: 'hoist-out-of-map', plan: planHoistOutOfMapRewrites },
]

export function planFileEdits(repoRoot, filePath) {
  const original = readFile(filePath)
  const allEdits = []
  const allRefusals = []
  const allCaveats = []

  // Transform D runs first and, if it edits anything, its output becomes the
  // basis every other transform parses and reports positions against — a
  // body-destructured className only becomes a direct parameter (provable to
  // Transform A) after this runs. `text`/`basis` below is intentionally that
  // post-D text, not the original, whenever D touched the file.
  const dResult = planParamDestructureHoistRewrites(repoRoot, filePath, original)
  for (const refusal of dResult.refusals) allRefusals.push({ ...refusal, transform: 'param-destructure-hoist' })
  let basis = original
  if (dResult.edits.length > 0) {
    basis = applyEdits(original, dResult.edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    for (const edit of dResult.edits) allEdits.push({ ...edit, transform: 'param-destructure-hoist' })
  }

  for (const { id, plan } of TRANSFORMS) {
    const result = plan(repoRoot, filePath, basis)
    for (const edit of result.edits) allEdits.push({ ...edit, transform: id })
    for (const refusal of result.refusals) allRefusals.push({ ...refusal, transform: id })
    for (const caveat of result.caveats ?? []) allCaveats.push({ ...caveat, transform: id })
    if (result.edits.length > 0) basis = applyEdits(basis, result.edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
  }

  return { edits: allEdits, refusals: allRefusals, caveats: allCaveats, text: original, after: basis }
}

export function runCodemod({ repoRoot, apply }) {
  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, caveats, text, after } = planFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax, transform: e.transform })),
      refusals,
      caveats,
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
    for (const r of f.refusals) console.log(`REFUSED [${r.transform}] ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
    for (const c of f.caveats) console.log(`CAVEAT [${c.transform}] ${f.file}:${c.loc.split(':').slice(1).join(':')} — ${c.note}`)
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
