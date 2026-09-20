#!/usr/bin/env node
// Exact-value repair codemod — Stage B closure, C1 lane S-SPACE-2
// (dist/design-system-baseline/cli-lanes/fanout/S-SPACE-2/). Input, decided
// upstream by lane M1: dist/design-system-baseline/cli-lanes/c1-prep/M1/
// mapping.json + mapping.md. This script owns exactly three of M1's pattern
// families (a sibling lane owns plain Tailwind spacing utilities, another
// owns raw CSS):
//
//   1. ARBITRARY BRACKET  (`arbitrary-bracket-px`, `arbitrary-bracket-rem`)
//      e.g. `ml-[3px]` -> `ml-[var(--space-1)]` — excludes any row whose
//      token is `--border-width-hairline` (that is family 2, below).
//   2. HAIRLINE  (`tailwind-px-suffix-utility`, the 1px subset of
//      `arbitrary-bracket-px`, and `negative-1px-utility (verify: no
//      negative hairline)`) — every value that renders exactly 1px maps to
//      the registered `--border-width-hairline` token
//      (design-system/tokens/foundations.json `border.width.hairline`,
//      value `1px`), NEVER the 4/8 spacing scale: confirmed both by the
//      token file itself and by docs/internal/design/design-system-
//      definition.md D10 ("Hairlines and 1px borders stay 1px") and its
//      D3 gate list ("a spacing value off the 4px/8px scale, except
//      hairlines and 1px borders"). A hairline is never negated — see
//      `-mb-px` below.
//   3. NEGATIVE MARGIN  (`negative-tailwind-fraction-utility`) — M1 finding
//      4: nobody had confirmed Tailwind v4 compiles a negated ARBITRARY
//      `var()` value (`-mt-[var(--space-1)]`) as opposed to a negated
//      built-in scale step (`-mt-1`, long supported). This codemod does not
//      trust the mapping's own "VERIFY FIRST" prose or guess an answer: on
//      every run it live-compiles a handful of representative negative
//      candidates through THIS REPO'S OWN installed `tailwindcss` package
//      (`probeNegativeArbitraryVarSupport`, re-derived every run, never
//      hardcoded — same "prove don't assume" convention as
//      codemod-dead-textclass-read.mjs's computeSafeFunctionNames) and only
//      unlocks the family when every one of them compiles to the expected
//      `calc(var(--space-N) * -1)` form. Recorded proof from an identical
//      standalone probe against this repo's tailwindcss@4.3.3 lives at
//      dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-2/probe/
//      tw-negative-probe.out — confirmed: Tailwind v4 DOES compile a
//      negated arbitrary var() margin/space-between utility correctly, so
//      this codemod emits the direct `-<prop>-[var(<token>)]` form (not the
//      `calc()` fallback the mapping's prose considered) once the family
//      is unlocked. If the live probe ever fails in a future Tailwind
//      version, the codemod refuses the WHOLE family — see M1 finding 4;
//      a wrong negative margin is a visible layout break, so this is a
//      refuse-by-default family until proven otherwise on every run.
//
// Every applied replacement is read from M1's own mapping.json at runtime
// (never a hardcoded value table) — the ONE exception is the negative
// family's actual class text, which is derived mechanically from the row's
// `value` + `token` fields once the family is unlocked, because the
// mapping's own `replacement` field for that family is deliberately
// decision-prose ("VERIFY FIRST — proposed ..."), not a literal class.
//
// Scope of what counts as an editable site: any StringLiteral or
// NoSubstitutionTemplateLiteral (`ts.isStringLiteralLike`), OR a literal
// head/middle/tail chunk of a `TemplateExpression` (e.g. the real repo's
// `` `-mb-px whitespace-nowrap ... ${...}` `` in BasePreview.tsx), matched
// as a whole, whitespace/true-edge-bounded token. A candidate touching a
// TemplateExpression's OWN interpolation boundary (`${`/`}`) is refused,
// not guessed at — the same shape design-system-locks/spacing.mjs's own
// `flagIncompleteSpacing`/`completeClasses` refuse for exactly this
// reason (an interpolated value's runtime content is unknown). Group
// filtering: only a row whose `group` is in
// `--groups` (default `IDENTICAL,NORMALIZED`) is ever applied; every
// NEEDS-DECISION row, and every row whose own mapping-provided replacement
// is decision prose rather than a bare utility class, is refused with the
// mapping's own reason text.
import { createRequire } from 'node:module'
import { dirname, relative, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

export const DEFAULT_MAPPING_PATH_REL = 'dist/design-system-baseline/cli-lanes/c1-prep/M1/mapping.json'

export const OWNED_PATTERNS = new Set([
  'arbitrary-bracket-px',
  'arbitrary-bracket-rem',
  'tailwind-px-suffix-utility',
  'negative-tailwind-fraction-utility',
  'negative-1px-utility (verify: no negative hairline)',
])

const HAIRLINE_TOKEN = '--border-width-hairline'
const VALID_GROUPS = new Set(['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION'])
export const DEFAULT_ALLOWED_GROUPS = new Set(['IDENTICAL', 'NORMALIZED'])

// ── mapping.json loading (read at runtime, never hardcoded) ──────────────

/** Loads M1's `{ rows: [...] }` mapping.json shape and returns just the
 * rows array. Throws loudly (never falls back to an empty/guessed set) if
 * the file is missing or shaped unexpectedly — see COMMON-RULES "refuse
 * rather than guess". */
export function loadMappingRows(mappingPath) {
  const raw = readFile(mappingPath)
  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch (err) {
    throw new Error(`mapping file ${mappingPath} is not valid JSON: ${err.message}`, { cause: err })
  }
  if (!parsed || !Array.isArray(parsed.rows)) {
    throw new Error(`mapping file ${mappingPath} does not have the expected { rows: [...] } shape`)
  }
  return parsed.rows
}

// ── family classification ─────────────────────────────────────────────────

/** Classifies one mapping row into this lane's three families, or `null`
 * for a pattern this lane does not own (a sibling lane's row would never
 * reach here in practice, since callers pre-filter by OWNED_PATTERNS, but
 * this stays defensive rather than assuming). */
export function classifyFamily(row) {
  if (row.pattern === 'tailwind-px-suffix-utility') return 'hairline'
  if (row.pattern.startsWith('negative-1px-utility')) return 'hairline'
  if (row.pattern === 'arbitrary-bracket-px' && row.token === HAIRLINE_TOKEN) return 'hairline'
  if (row.pattern === 'negative-tailwind-fraction-utility') return 'negative'
  if (row.pattern === 'arbitrary-bracket-px' || row.pattern === 'arbitrary-bracket-rem') return 'arbitrary'
  return null
}

/** True when `replacement` is a mechanical, single bare utility-class token
 * (no spaces, no decision prose) safe to apply verbatim — false for every
 * "VERIFY FIRST — ...", "NOT RECOMMENDED — ...", "NO SOUND TOKEN MAPPING —
 * ..." free-text cell M1 writes for a value it could not mechanically
 * resolve. */
function isBareUtilityClassReplacement(replacement) {
  return (
    typeof replacement === 'string' &&
    replacement.length > 0 &&
    !replacement.includes(' ') &&
    /^[A-Za-z0-9:_[\]().*%/+-]+$/.test(replacement)
  )
}

// A negative Tailwind utility's own shape: a leading `-`, a property
// segment (letters and internal hyphens, e.g. `mx`, `m`, `mt`, `space-x`),
// then a trailing numeric Tailwind spacing-scale suffix (`1`, `0.5`, `2`).
const NEGATIVE_VALUE_SHAPE = /^-([a-z]+(?:-[a-z]+)*)-(\d+(?:\.\d+)?)$/

/** Mechanically derives the negated-arbitrary-value replacement for a
 * negative-family row from its OWN `value` + `token` fields — never from
 * the mapping's decision-prose `replacement` cell. Returns null when
 * `value` does not match the expected negative-utility shape (refused by
 * the caller rather than guessed). */
export function deriveNegativeReplacement(value, token) {
  const match = NEGATIVE_VALUE_SHAPE.exec(value)
  if (!match) return null
  const [, prop] = match
  return `-${prop}-[var(${token})]`
}

/** Builds the value -> action table this codemod applies, straight from
 * M1's mapping rows (re-read every run) plus the live negative-family
 * compile-probe result. Returns a `Map<value, action>`, where `action` is
 * either `{ action: 'apply', replacement, family, group, pattern }` or
 * `{ action: 'refuse', reason, family, group, pattern }`. Pure — takes no
 * dependency on the filesystem beyond the already-loaded `rows`. */
export function buildValueActions(rows, { allowedGroups = DEFAULT_ALLOWED_GROUPS, negativeSupported, negativeReason } = {}) {
  for (const group of allowedGroups) {
    if (!VALID_GROUPS.has(group)) throw new Error(`unknown mapping group "${group}" (expected one of ${[...VALID_GROUPS].join(', ')})`)
  }
  const actions = new Map()
  for (const row of rows) {
    if (!OWNED_PATTERNS.has(row.pattern)) continue
    const family = classifyFamily(row)
    if (!family) continue
    const inAllowedGroup = allowedGroups.has(row.group)
    const base = { family, group: row.group, pattern: row.pattern }

    // A hairline is never negated, in any group, regardless of --groups —
    // this is a hard invariant (D10), not a group-eligibility decision.
    if (family === 'hairline' && /do not negate a hairline/i.test(row.replacement ?? '')) {
      actions.set(row.value, {
        ...base,
        action: 'refuse',
        reason: `${row.reason} Hairlines are never negated (D10 hairline/1px-border exception) — keep \`${row.value}\` as a literal 1px.`,
      })
      continue
    }

    if (family === 'negative') {
      if (!negativeSupported) {
        actions.set(row.value, {
          ...base,
          action: 'refuse',
          reason: `Tailwind v4 negated-arbitrary-var() compile probe did not confirm support (${negativeReason}); refusing the whole negative-margin family per M1 finding 4 rather than emit an unverified layout change.`,
        })
        continue
      }
      if (!inAllowedGroup) {
        actions.set(row.value, { ...base, action: 'refuse', reason: row.reason || `\`${row.value}\` is classified ${row.group}, outside the applied groups (${[...allowedGroups].join(', ')}).` })
        continue
      }
      const replacement = deriveNegativeReplacement(row.value, row.token)
      if (!replacement) {
        actions.set(row.value, { ...base, action: 'refuse', reason: `could not mechanically derive a negative-arbitrary-value replacement from \`${row.value}\` (unexpected shape) — refusing rather than guess.` })
        continue
      }
      actions.set(row.value, { ...base, action: 'apply', replacement })
      continue
    }

    // arbitrary / hairline (non-hairline-negation) families: trust the
    // mapping's own replacement verbatim, but only when it is itself a
    // bare mechanical class token and its group is one of the applied ones.
    if (inAllowedGroup && isBareUtilityClassReplacement(row.replacement)) {
      actions.set(row.value, { ...base, action: 'apply', replacement: row.replacement })
    } else {
      actions.set(row.value, { ...base, action: 'refuse', reason: row.reason || `\`${row.value}\` is classified ${row.group}; its mapping replacement is not a mechanical bare-class token.` })
    }
  }
  return actions
}

// ── live Tailwind negative-arbitrary-var() compile probe ─────────────────

/** Live-compiles a handful of representative `-<prop>-[var(--space-N)]`
 * candidates through the REPO'S OWN installed `tailwindcss` package (never
 * a bundled/hardcoded copy) and checks each compiles to the expected
 * negated `calc(var(...) * -1)` form. Re-derived on every call — this
 * never hardcodes "Tailwind v4 supports this" as a fact; if a future
 * Tailwind upgrade changes the behavior, the next run's probe result
 * changes with it. See file header and M1 finding 4. */
export async function probeNegativeArbitraryVarSupport(repoRoot) {
  let requireFromRoot
  try {
    requireFromRoot = createRequire(pathToFileURL(resolve(repoRoot, 'package.json')).href)
  } catch (err) {
    return { supported: false, reason: `could not resolve ${repoRoot}/package.json to load this repo's own tailwindcss package: ${err.message}` }
  }

  let compile
  let twRoot
  try {
    // `tailwindcss`'s package.json "exports" map resolves the bare
    // specifier to its CJS entry (dist/lib.js) under the "require"
    // condition — loaded with `require()` itself (not a dynamic `import()`
    // of that same CJS file), since Node's CJS/ESM interop for a dynamic
    // import of a non-`.mjs` CJS file only reconstructs NAMED exports via
    // static `cjs-module-lexer` analysis, which is not guaranteed for a
    // built/bundled file — confirmed against this repo's tailwindcss@4.3.3
    // (dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-2/probe/
    // tw-negative-probe.out uses the same `require()` approach and works).
    ;({ compile } = requireFromRoot('tailwindcss'))
    twRoot = dirname(requireFromRoot.resolve('tailwindcss/package.json'))
  } catch (err) {
    return { supported: false, reason: `could not load this repo's own "tailwindcss" package: ${err.message}` }
  }
  if (typeof compile !== 'function') {
    return { supported: false, reason: `this repo's "tailwindcss" package does not export a "compile" function (unexpected package shape) — refusing rather than guess` }
  }

  async function loadStylesheet(id, base) {
    let resolved
    if (id === 'tailwindcss') resolved = resolve(twRoot, 'index.css')
    else if (id.startsWith('.')) resolved = resolve(base, id)
    else resolved = requireFromRoot.resolve(id, { paths: [base] })
    const content = readFile(resolved)
    return { base: dirname(resolved), content }
  }

  const css = '@import "tailwindcss";\n:root {\n  --space-0-5: 2px;\n  --space-1: 4px;\n  --space-2: 8px;\n}\n'
  const CHECKS = [
    { candidate: '-mt-[var(--space-1)]', expect: /margin-top:\s*calc\(var\(--space-1\)\s*\*\s*-1\)/ },
    { candidate: '-m-[var(--space-1)]', expect: /margin:\s*calc\(var\(--space-1\)\s*\*\s*-1\)/ },
    { candidate: '-ml-[var(--space-2)]', expect: /margin-left:\s*calc\(var\(--space-2\)\s*\*\s*-1\)/ },
    { candidate: '-space-x-[var(--space-2)]', expect: /margin-inline-start:\s*calc\(calc\(var\(--space-2\)\s*\*\s*-1\)/ },
  ]

  let build
  try {
    ;({ build } = await compile(css, { base: repoRoot, loadStylesheet, onDependency() {} }))
  } catch (err) {
    return { supported: false, reason: `tailwindcss compile() failed: ${err.message}` }
  }

  let output
  try {
    output = build(CHECKS.map((c) => c.candidate))
  } catch (err) {
    return { supported: false, reason: `tailwindcss build() failed on negative-arbitrary-var candidates: ${err.message}` }
  }

  for (const { candidate, expect } of CHECKS) {
    if (!expect.test(output)) {
      return { supported: false, reason: `\`${candidate}\` did not compile to the expected negated calc() form — this repo's tailwindcss does not (or no longer) reliably support negated arbitrary var() values`, output }
    }
  }
  return { supported: true, reason: "verified live against this repo's own tailwindcss package: every negated-arbitrary-var() candidate compiled to the expected calc(var(...) * -1) form", output }
}

// ── file-level edit planning ──────────────────────────────────────────────

function locOf(source, pos, filePath) {
  const lc = source.getLineAndCharacterOfPosition(pos)
  return `${filePath}:${lc.line + 1}:${lc.character + 1}`
}

function isWhitespace(ch) {
  return /\s/.test(ch)
}

/** Scans one literal text fragment (the interior of a plain string literal,
 * or one head/middle/tail chunk of a template literal) for every
 * occurrence of every owned value, applying or refusing per `valueActions`.
 * `leftSafe`/`rightSafe` say whether the fragment's OWN edges are a true
 * string boundary (plain string literal; the very start/end of a template
 * literal) or sit against an interpolation (`${`/`}`) whose runtime
 * content is unknown — a match touching an unsafe edge is refused, never
 * guessed at, matching design-system-locks/spacing.mjs's own
 * `flagIncompleteSpacing`/`completeClasses` treatment of the same shape. A
 * match touching a SAFE edge, or bounded by literal whitespace anywhere
 * inside the fragment, is a genuine whole-token match. */
function scanFragment({ raw, fragStart, leftSafe, rightSafe, valueActions, edits, refusals, source, relPath }) {
  for (const [value, action] of valueActions) {
    let idx = raw.indexOf(value)
    while (idx !== -1) {
      const beforeChar = idx === 0 ? undefined : raw[idx - 1]
      const afterIdx = idx + value.length
      const afterChar = afterIdx >= raw.length ? undefined : raw[afterIdx]
      const beforeOk = beforeChar !== undefined ? isWhitespace(beforeChar) : leftSafe
      const afterOk = afterChar !== undefined ? isWhitespace(afterChar) : rightSafe
      const absStart = fragStart + idx
      const loc = locOf(source, absStart, relPath)
      if (beforeOk && afterOk) {
        if (action.action === 'apply') {
          edits.push({ start: absStart, end: absStart + value.length, replacement: action.replacement, loc, syntax: value, family: action.family })
        } else {
          refusals.push({ loc, syntax: value, reason: action.reason, family: action.family })
        }
      } else {
        refusals.push({
          loc,
          syntax: value,
          family: action.family,
          reason: `\`${value}\` found as a substring but is not bounded by whitespace (or a true string/template edge) on both sides — refusing rather than assume a coincidental or glued-together match, or a class glued to an interpolated value whose runtime content is unknown.`,
        })
      }
      idx = raw.indexOf(value, idx + value.length)
    }
  }
}

/** Plans edits/refusals for one file's already-read `text` against
 * `valueActions` — pure (no filesystem access), so tests can exercise it
 * directly with synthetic source text instead of real files. Walks every
 * `StringLiteral`/`NoSubstitutionTemplateLiteral` (matches
 * codemod-type-scale-css.mjs's pattern 4 scope) AND every literal chunk of
 * a `TemplateExpression` (head/middle/tail — e.g. `` `-mb-px ${x}` ``),
 * finding every whitespace/true-edge-BOUNDED occurrence of the exact token
 * text. A same-text match that is NOT boundary-bounded (glued to other
 * non-whitespace content, or to an interpolation whose value is unknown)
 * is refused, not silently skipped or guessed at. */
export function planEditsForText(filePath, text, valueActions, repoRoot = filePath) {
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  const relPath = relative(repoRoot, filePath)

  const visit = (node) => {
    if (ts.isStringLiteralLike(node)) {
      const start = node.getStart(source) + 1
      const raw = text.slice(start, node.getEnd() - 1)
      scanFragment({ raw, fragStart: start, leftSafe: true, rightSafe: true, valueActions, edits, refusals, source, relPath })
    } else if (ts.isTemplateExpression(node)) {
      const headStart = node.head.getStart(source) + 1
      const headEnd = node.head.getEnd() - 2 // exclude the leading backtick and trailing `${`
      scanFragment({ raw: text.slice(headStart, headEnd), fragStart: headStart, leftSafe: true, rightSafe: false, valueActions, edits, refusals, source, relPath })
      node.templateSpans.forEach((span, index) => {
        const isLastSpan = index === node.templateSpans.length - 1
        const litStart = span.literal.getStart(source) + 1
        const litEnd = isLastSpan ? span.literal.getEnd() - 1 : span.literal.getEnd() - 2
        scanFragment({ raw: text.slice(litStart, litEnd), fragStart: litStart, leftSafe: false, rightSafe: isLastSpan, valueActions, edits, refusals, source, relPath })
      })
      // Fall through to forEachChild below — a template's own interpolated
      // expressions (e.g. a ternary's own plain string-literal branches)
      // may contain further, independently-scannable string literals.
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { edits, refusals }
}

/** Reads `filePath` from disk and delegates to `planEditsForText`. */
export function planFileEdits(repoRoot, filePath, valueActions) {
  const text = readFile(filePath)
  const { edits, refusals } = planEditsForText(filePath, text, valueActions, repoRoot)
  return { edits, refusals, text }
}

// ── orchestration ──────────────────────────────────────────────────────────

export async function runCodemod({ repoRoot, apply, mappingPath, allowedGroups = DEFAULT_ALLOWED_GROUPS, negativeProbeResult }) {
  const resolvedMappingPath = mappingPath ?? resolve(repoRoot, DEFAULT_MAPPING_PATH_REL)
  const rows = loadMappingRows(resolvedMappingPath)
  const probe = negativeProbeResult ?? await probeNegativeArbitraryVarSupport(repoRoot)
  const valueActions = buildValueActions(rows, { allowedGroups, negativeSupported: probe.supported, negativeReason: probe.reason })

  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0

  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath, valueActions)
    if (edits.length === 0 && refusals.length === 0) continue
    const after = edits.length > 0 ? applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement }))) : text
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax, replacement: e.replacement, family: e.family })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  const byFamily = { arbitrary: { edits: 0, refusals: 0 }, hairline: { edits: 0, refusals: 0 }, negative: { edits: 0, refusals: 0 } }
  for (const f of fileResults) {
    for (const e of f.edits) byFamily[e.family].edits += 1
    for (const r of f.refusals) byFamily[r.family].refusals += 1
  }

  return {
    mappingPath: resolvedMappingPath,
    appliedGroups: [...allowedGroups],
    negativeProbe: { supported: probe.supported, reason: probe.reason },
    valueActions: [...valueActions.entries()].map(([value, action]) => ({ value, ...action })),
    byFamily,
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: Boolean(apply),
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────

/** Reads `--flag value` or `--flag=value`, matching the sibling S-SPACE-1
 * lane's parseGroupsArg/parseMappingArg convention. */
function readFlag(argv, flag) {
  const idx = argv.findIndex((a) => a === flag || a.startsWith(`${flag}=`))
  if (idx === -1) return undefined
  return argv[idx].startsWith(`${flag}=`) ? argv[idx].slice(flag.length + 1) : argv[idx + 1]
}

function parseArgs(argv, defaultRoot) {
  const apply = argv.includes('--apply')
  const root = resolve(readFlag(argv, '--root') ?? defaultRoot)
  const groupsRaw = readFlag(argv, '--groups')
  const allowedGroups = groupsRaw
    ? new Set(groupsRaw.split(',').map((s) => s.trim()).filter(Boolean))
    : DEFAULT_ALLOWED_GROUPS
  const mappingRaw = readFlag(argv, '--mapping')
  const mappingPath = mappingRaw ? resolve(mappingRaw) : undefined
  return { apply, root, allowedGroups, mappingPath }
}

const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root, allowedGroups, mappingPath } = parseArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = await runCodemod({ repoRoot: root, apply, allowedGroups, mappingPath })
  console.error(`Negative-margin family: ${result.negativeProbe.supported ? 'UNLOCKED' : 'REFUSED'} — ${result.negativeProbe.reason}`)
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED [${r.family}] ${f.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    mappingPath: result.mappingPath,
    appliedGroups: result.appliedGroups,
    negativeProbe: result.negativeProbe,
    byFamily: result.byFamily,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
