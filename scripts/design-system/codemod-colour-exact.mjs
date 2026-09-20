#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure fan-out lane COLOUR-SPLIT
// (dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md), covering
// ONLY the byte-identical, provably-invisible slice of Lane M4's colour
// mapping (dist/design-system-baseline/cli-lanes/c1-prep/M4/mapping.json,
// mapping.md). The split itself is re-derived — not copied from M4's cached
// judgement — against design-system/tokens/colors.json by
// dist/design-system-baseline/cli-lanes/c1-prep/COLOUR-SPLIT/generate-safe-split.mjs
// and recorded in that same directory's safe-split.json/safe-split.md.
//
// Four SAFE (apply) rule families, all data-driven from safe-split.json at
// run time so the lead can re-run this unchanged once NEEDS-DECISION values
// are resolved:
//
//   1. ts-var-substring  — a handful of undefined `var(--name)` references in
//      TS/TSX string literals (JSX style props, Tailwind arbitrary-bracket
//      className tokens) whose replacement is a literal, unambiguous name
//      swap to an already-registered token of the identical resolved value
//      (`var(--forge-gold)` -> `var(--color-accent)`, and the dead
//      `--color-primary-fg` fallback collapse). Two of M4's five
//      ts-colors/undefined-token items; the file-scoped ambiguity elsewhere
//      in that rule (`--color-border-hover`, `--color-text-secondary`) is a
//      real token-catalog gap, not something this script guesses at.
//
//   2. css-theme-block-primitive-swap — globals.css's un-layered `@theme`
//      block duplicates src/styles/tokens.theme.generated.css's own output
//      using raw hex instead of `var(--primitive-color-*)`. Per property,
//      swapping the SAME variable name it declares to `var(--same-name)`
//      would be circular/invalid CSS, so this rule swaps to the differently
//      named primitive alias colors.json's own ref chain already resolves to
//      — verified byte-identical at data-generation time, and re-verified
//      here at run time against the exact hex still present in the file.
//      `--color-cancelled` is explicitly excluded (refused): its literal
//      (#F97316, Blocked-orange) does not match the registered token
//      (#EAB308, amber) — a real status-mismatch bug, not this script's call.
//
//   3. css-var-fc-indirection-unwrap — src/styles/fullcalendar-theme.css
//      declares its own `--fc-*` custom properties, two of which already
//      read `var(--registered-token)` at the declaration; this rule points
//      CONSUMPTION sites straight at the registered token instead of through
//      the FullCalendar-named indirection (never touches the declaration
//      line itself, so it can never introduce a self-reference).
//
//   4. css-var-fallback-drop — a `var(--token, #hexFallback)` site where the
//      fallback is runtime-verified (against the full token table embedded
//      in safe-split.json, itself re-derived from colors.json) to be
//      byte-identical to the token's live value: dropping the dead-but-
//      correct fallback is a no-op today. The SAME generic check correctly
//      REFUSES every one of M4's documented dead/MISMATCHED fallback sites
//      (brand-icon.tsx, MediaActionToolbar.tsx, AgentProfile.tsx,
//      UntrustedChildText.tsx) — this isn't a separate rule, it's the same
//      verification failing closed.
//
// Everything else this script recognizes is an explicit, reasoned REFUSAL,
// never a silent skip that could hide a violation the scanner would still
// flag:
//
//   - alpha-concatenation shape (`` `${expr}HH` `` — a template literal whose
//     only substitution is immediately followed by exactly two hex digits).
//     mapping.md's "Gate-2 finding #1": once the substituted expression
//     becomes a token instead of a raw hex literal, this produces the
//     invalid-CSS string "var(--x)HH". This script cannot prove what `expr`
//     evaluates to, so it always refuses this shape, structurally, never
//     guessing it is (or isn't) already using the TaskNode.tsx-style
//     defensive `toTint()` guard.
//   - governed-data files (safe-split.json `alwaysRefuse.governedDataFiles`)
//     — any hex literal or `var()` reference found there is data (persisted
//     agent colours, file-type registries, diagram themes, document ink),
//     per design-system-definition.md D14/E1, never a token-swap candidate.
//   - Tailwind palette-hue utility classes (`text-red-400`, `bg-amber-500/10`,
//     `bg-white`, …) — mapping.md headline finding #3: Tailwind v4's
//     OKLCH-regenerated default palette no longer numerically matches this
//     repo's brand hexes, so swapping the class is a real, visible colour
//     change, never invisible. Structural rule, not a per-value lookup.
//   - a bare CSS hex declaration (`prop: #hex;`, the whole value) that
//     byte-matches one or more registered tokens but isn't already covered
//     by rule 2/3/4 above: which registered name to use is ambiguous
//     (several tokens can share one hex), so this is refused with the
//     candidate list rather than picked for the reader.
//   - a `--color-<name>: #hex;` line inside globals.css's `@theme` block
//     whose hex has DRIFTED from the exact value this script's table was
//     verified against: refuses rather than guessing at a value it was not
//     proven correct for.
//
// Every other rule from scripts/design-system/codemod-lib.mjs's shared
// contract still applies: dry-run by default (`--apply` writes), idempotent,
// touches only ALLOWED_ROOTS (src/, packages/ui/src/), and reports a
// deterministic summary (totalFilesScanned/totalFilesTouched/totalEdits/
// totalRefusals).
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { relative, resolve, extname, sep } from 'node:path'
import postcss from 'postcss'
import ts from 'typescript'
import {
  ALLOWED_ROOTS, applyEdits, isInAllowedRoot, parseCodemodArgs, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

export const DEFAULT_SAFE_SPLIT_PATH = 'design-system/repair/colour-safe-split.json'

const SKIP_DIR_NAMES = new Set(['node_modules', 'dist', '.git'])
const SCANNED_EXTENSIONS = new Set(['.css', '.ts', '.tsx'])
// Mirrors scripts/design-system-locks/ts-colors.mjs::CANONICAL_GENERATED_TOKEN_PATHS
// (and audit.mjs's copy of the same list) exactly: these three files ARE the
// token definitions, not a usage site — the ts-colors/css-colors scanners
// exempt them semantically, and M4's own dataset contains zero occurrences
// from any of them (verified: `grep`-ing mapping.json's file lists for these
// three paths returns nothing), so this codemod excludes them from scanning
// entirely rather than generating a flood of "ambiguous token name" noise
// against the very files that ARE the token catalog.
const CANONICAL_GENERATED_TOKEN_PATHS = new Set([
  'src/styles/tokens.generated.css',
  'src/styles/tokens.theme.generated.css',
  'src/design-system/tokens.ts',
])
// Test files carry zero occurrences in M4's ledger-derived dataset either
// (same verification) — the real scanners exempt test assertions on token
// values, which are not "chrome" needing a swap. Matched by filename, same
// convention this repo already uses (`*.test.ts(x)`, `*.spec.ts(x)`).
const TEST_FILE_RE = /\.(test|spec)\.[jt]sx?$/i

const HEX_RE = /^#[0-9a-f]{3,8}$/i
const BARE_HEX_TOKEN_RE = /#[0-9a-f]{3,8}\b/gi
const VAR_FALLBACK_RE = /var\(\s*(--[\w-]+)\s*,\s*(#[0-9a-fA-F]{3,8})\s*\)/g
const ALPHA_CONCAT_RE = /^`\$\{[^}]*\}[0-9a-fA-F]{2}`$/
const TAILWIND_PREFIX_ALT = '(?:bg|text|border|ring|fill|stroke|from|via|to|divide|outline|shadow|accent|caret|decoration)'
const TAILWIND_VARIANT_RE = '(?:[\\w-]+:)*'

// ── safe-split.json loading & lookup tables ─────────────────────────────

export function loadSafeSplit(path) {
  const text = readFileSync(path, 'utf8')
  const data = JSON.parse(text)
  if (!data || !data.safe || !data.alwaysRefuse) {
    throw new Error(`safe-split file ${path} is missing "safe"/"alwaysRefuse" sections`)
  }
  return data
}

export function buildTables(safeSplit) {
  const tokenValueByCss = new Map(
    Object.entries(safeSplit.safe.cssVarFallbackDropTokenValues ?? {}).map(([k, v]) => [k, String(v).toLowerCase()]),
  )
  const hexToTokenNames = new Map()
  for (const [name, hex] of tokenValueByCss) {
    const key = hex.toLowerCase()
    if (!hexToTokenNames.has(key)) hexToTokenNames.set(key, [])
    hexToTokenNames.get(key).push(name)
  }
  for (const names of hexToTokenNames.values()) names.sort()

  const tailwindPaletteRe = new RegExp(
    `(?:^|\\s)(${TAILWIND_VARIANT_RE}${TAILWIND_PREFIX_ALT}-(?:${(safeSplit.alwaysRefuse.tailwindPaletteHues ?? []).join('|')})(?:-\\d{2,3})?(?:/\\d{1,3})?)(?=\\s|$)`,
    'gi',
  )

  return {
    tsVarSubstrings: safeSplit.safe.tsVarSubstrings ?? [],
    themeBlockFile: safeSplit.safe.cssThemeBlockPrimitiveSwap?.file ?? null,
    themeBlockProperties: new Map((safeSplit.safe.cssThemeBlockPrimitiveSwap?.properties ?? []).map((p) => [p.prop, p])),
    fcUnwrapFiles: new Set(safeSplit.safe.cssVarIndirectionUnwrap?.files ?? []),
    tokenValueByCss,
    hexToTokenNames,
    governedDataFiles: new Set(safeSplit.alwaysRefuse.governedDataFiles ?? []),
    tailwindPaletteRe,
  }
}

function candidateTokensForHex(tables, hex) {
  return tables.hexToTokenNames.get(hex.toLowerCase()) ?? []
}

/** A fresh RegExp instance per call — VAR_FALLBACK_RE carries the `g` flag, and
 * reusing one shared instance's `lastIndex` across many unrelated `.exec()`
 * calls is a classic stateful-regex bug; this sidesteps it entirely. */
function firstVarFallbackMatch(value) {
  return new RegExp(VAR_FALLBACK_RE.source).exec(value)
}

/** Verifies a `var(--token, #hex)` fallback against the live token table; returns
 * 'apply' (drop the fallback), 'refuse-mismatch' (fallback disagrees with the live
 * value), or 'unknown' (token isn't in the table at all — not this script's scope). */
function classifyFallback(tables, tokenName, hexLiteral) {
  const live = tables.tokenValueByCss.get(tokenName)
  if (live === undefined) return { verdict: 'unknown' }
  if (live === hexLiteral.toLowerCase()) return { verdict: 'apply' }
  return { verdict: 'refuse-mismatch', live }
}

// ── CSS-file path (postcss) ─────────────────────────────────────────────

function fcDeclarationMap(root) {
  const map = new Map()
  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    if (!prop.startsWith('--fc-')) return
    const value = decl.value.trim()
    const match = /^var\(\s*(--[\w-]+)\s*\)$/.exec(value)
    if (match) map.set(prop, `var(${match[1]})`)
  })
  return map
}

export function planCssFile(repoRoot, filePath, tables) {
  const relPath = relative(repoRoot, filePath).split(sep).join('/')
  const text = readFile(filePath)
  let root
  try {
    root = postcss.parse(text, { from: filePath })
  } catch (error) {
    return {
      relPath,
      before: text,
      after: text,
      edits: [],
      refusals: [{
        file: relPath,
        loc: `${relPath}:${error.line ?? 1}:${error.column ?? 1}`,
        prop: null,
        value: null,
        family: 'parse-error',
        reason: `Failed to parse as CSS (${error.message}); refusing rather than guessing at declarations in an unparseable file.`,
      }],
    }
  }

  const isThemeBlockFile = relPath === tables.themeBlockFile
  const isFcUnwrapFile = tables.fcUnwrapFiles.has(relPath)
  const fcMap = isFcUnwrapFile ? fcDeclarationMap(root) : null

  const edits = []
  const refusals = []

  root.walkDecls((decl) => {
    const prop = decl.prop.toLowerCase()
    const value = decl.value
    const loc = `${relPath}:${decl.source?.start?.line ?? '?'}:${decl.source?.start?.column ?? '?'}`

    // Rule 2: theme-block primitive swap (exact file + exact property).
    if (isThemeBlockFile && tables.themeBlockProperties.has(prop)) {
      const entry = tables.themeBlockProperties.get(prop)
      const trimmedLower = value.trim().toLowerCase()
      // Idempotence: a prior run already rewrote this declaration to
      // entry.replacement — that is success, not drift. Recognize it and
      // move on silently (no edit left to make, nothing to refuse either).
      if (!entry.refuse && entry.replacement && trimmedLower === entry.replacement.toLowerCase()) return
      if (trimmedLower !== entry.expectedHex.toLowerCase()) {
        refusals.push({
          file: relPath, loc, prop, value, family: 'css-theme-block-primitive-swap',
          reason: `Expected ${prop} to still be ${entry.expectedHex} (the value this rule was verified against); found ${value} instead — the value has drifted, refusing rather than guessing.`,
        })
        return
      }
      if (entry.refuse) {
        refusals.push({ file: relPath, loc, prop, value, family: 'css-theme-block-primitive-swap', reason: entry.reason })
        return
      }
      edits.push({ loc, prop, before: value, after: entry.replacement })
      decl.value = entry.replacement
      return
    }

    // Rule 3: `--fc-*` consumption-site indirection unwrap (never the
    // declaration line itself — fcMap only maps names whose OWN declaration
    // already reads a registered token, and we skip exactly that line).
    if (isFcUnwrapFile && fcMap && fcMap.size > 0 && !prop.startsWith('--fc-')) {
      let rewritten = value
      let touched = false
      for (const [fcName, replacement] of fcMap) {
        if (rewritten.includes(`var(${fcName})`)) {
          rewritten = rewritten.split(`var(${fcName})`).join(replacement)
          touched = true
        }
      }
      if (touched) {
        edits.push({ loc, prop, before: value, after: rewritten })
        decl.value = rewritten
        return
      }
    }

    // Rule 4: `var(--token, #hex)` dead-fallback drop, generic + runtime-verified.
    const fallbackMatch = firstVarFallbackMatch(value)
    if (fallbackMatch) {
      const [whole, tokenName, hexLiteral] = fallbackMatch
      const verdict = classifyFallback(tables, tokenName, hexLiteral)
      if (verdict.verdict === 'apply') {
        const rewritten = value.split(whole).join(`var(${tokenName})`)
        edits.push({ loc, prop, before: value, after: rewritten })
        decl.value = rewritten
        return
      }
      if (verdict.verdict === 'refuse-mismatch') {
        refusals.push({
          file: relPath, loc, prop, value, family: 'css-var-fallback-drop',
          reason: `Fallback literal ${hexLiteral} does not match ${tokenName}'s live value ${verdict.live} — dropping it would silently change behaviour if ${tokenName} were ever undefined. Refusing rather than guessing which one is right.`,
        })
        return
      }
    }

    // Generic ambiguous-token refusal: a bare `prop: #hex;` whose hex is a
    // byte-identical match for one or more REGISTERED tokens, but isn't
    // already covered by rule 2/3/4 above — which name to use is a real
    // choice this script is not authorized to make.
    const trimmedValue = value.trim()
    if (HEX_RE.test(trimmedValue)) {
      const candidates = candidateTokensForHex(tables, trimmedValue)
      if (candidates.length > 0) {
        refusals.push({
          file: relPath, loc, prop, value,
          family: 'css-ambiguous-token-name',
          reason: `${trimmedValue} is byte-identical to ${candidates.length} registered token(s) (${candidates.join(', ')}) but this declaration isn't part of the theme-block/fc-indirection rules this script knows how to disambiguate — refusing rather than guessing which token name belongs here.`,
        })
      }
    }
  })

  const after = edits.length > 0 ? root.toString() : text
  return { relPath, before: text, after, edits, refusals }
}

// ── .ts/.tsx path (TypeScript AST — string literals + template literals) ──

function locOf(sourceFile, relPath, node) {
  const pos = sourceFile.getLineAndCharacterOfPosition(node.getStart())
  return `${relPath}:${pos.line + 1}:${pos.character + 1}`
}

/** Applies rule 1 (ts-var-substring) to a literal's text; returns the rewritten
 * text and whether anything changed. Order matters: compound fallback forms
 * must be tried before the bare form so a single pass collapses correctly. */
function applyTsVarSubstrings(text, rules) {
  let out = text
  let changed = false
  for (const rule of rules) {
    if (out.includes(rule.find)) {
      out = out.split(rule.find).join(rule.replace)
      changed = true
    }
  }
  return { text: out, changed }
}

/** Rule 4 applied to a literal's text (same verification as the CSS path). */
function applyVarFallbackDrop(text, tables, refusalsOut, ctx) {
  let out = text
  let changed = false
  let match
  const re = new RegExp(VAR_FALLBACK_RE.source, 'g')
  while ((match = re.exec(text)) !== null) {
    const [whole, tokenName, hexLiteral] = match
    const verdict = classifyFallback(tables, tokenName, hexLiteral)
    if (verdict.verdict === 'apply') {
      out = out.split(whole).join(`var(${tokenName})`)
      changed = true
    } else if (verdict.verdict === 'refuse-mismatch') {
      refusalsOut.push({
        file: ctx.relPath, loc: ctx.loc, prop: null, value: whole,
        family: 'ts-var-fallback-drop',
        reason: `Fallback literal ${hexLiteral} does not match ${tokenName}'s live value ${verdict.live} — dropping it would silently change behaviour if ${tokenName} were ever undefined. Refusing rather than guessing which one is right.`,
      })
    }
  }
  return { text: out, changed }
}

function tailwindPaletteRefusals(text, tables, ctx) {
  const out = []
  tables.tailwindPaletteRe.lastIndex = 0
  let match
  const re = new RegExp(tables.tailwindPaletteRe.source, tables.tailwindPaletteRe.flags)
  while ((match = re.exec(text)) !== null) {
    out.push({
      file: ctx.relPath, loc: ctx.loc, prop: null, value: match[1].trim(),
      family: 'tailwind-palette-utility',
      reason: `Tailwind v4 regenerated its default palette in OKLCH — "${match[1].trim()}" no longer resolves to any brand hex this repo owns, so swapping it is a real, visible colour change, never an invisible one (mapping.md headline finding #3). Refusing structurally, not a value lookup.`,
    })
  }
  return out
}

function ambiguousHexRefusals(text, tables, ctx) {
  const out = []
  const re = new RegExp(BARE_HEX_TOKEN_RE.source, 'gi')
  let match
  while ((match = re.exec(text)) !== null) {
    const candidates = candidateTokensForHex(tables, match[0])
    if (candidates.length > 0) {
      out.push({
        file: ctx.relPath, loc: ctx.loc, prop: null, value: match[0],
        family: 'ts-ambiguous-token-name',
        reason: `${match[0]} is byte-identical to ${candidates.length} registered token(s) (${candidates.join(', ')}) but which name belongs at this call site is not mechanically derivable — refusing rather than guessing.`,
      })
    }
  }
  return out
}

/** Governed-data files: never edited, only scanned for hex/var() occurrences so
 * the refusal is explicit rather than a silent (and easy to mistake for
 * "nothing to see here") skip. */
function planGovernedLiteral(text, ctx, refusalsOut) {
  const hexMatches = text.match(new RegExp(BARE_HEX_TOKEN_RE.source, 'gi')) ?? []
  const varMatches = text.match(/var\(--[\w-]+[^)]*\)/g) ?? []
  for (const value of [...hexMatches, ...varMatches]) {
    refusalsOut.push({
      file: ctx.relPath, loc: ctx.loc, prop: null, value,
      family: 'governed-data',
      reason: 'This file is a registered governed-data source (persisted colour, icon/theme/diagram registry, or document ink) per design-system-definition.md D14/E1 — never a token-swap candidate, regardless of whether the literal happens to byte-match a token.',
    })
  }
}

function planStringLiteral(node, sourceFile, ctx, tables) {
  const originalText = node.text
  const loc = locOf(sourceFile, ctx.relPath, node)
  const literalCtx = { relPath: ctx.relPath, loc }

  if (ctx.isGoverned) {
    planGovernedLiteral(originalText, literalCtx, ctx.refusals)
    return
  }

  ctx.refusals.push(...tailwindPaletteRefusals(originalText, tables, literalCtx))

  const step1 = applyTsVarSubstrings(originalText, tables.tsVarSubstrings)
  const step2 = applyVarFallbackDrop(step1.text, tables, ctx.refusals, literalCtx)
  ctx.refusals.push(...ambiguousHexRefusals(originalText, tables, literalCtx))

  const finalText = step2.text
  if (step1.changed || step2.changed) {
    const quote = node.getText()[0]
    const isTemplate = quote === '`'
    ctx.edits.push({
      start: node.getStart(), end: node.getEnd(),
      replacement: isTemplate ? `\`${finalText}\`` : `${quote}${finalText}${quote}`,
      loc, prop: null, before: originalText, after: finalText,
    })
  }
}

/** Alpha-concatenation shape: a template EXPRESSION (not a plain string) whose
 * raw text is exactly `` `${...}HH` `` — one substitution, immediately followed
 * by two hex digits and nothing else. Always refused, never touched. */
function planTemplateExpression(node, sourceFile, ctx) {
  if (ctx.isGoverned) return // governed files get their own generic scan already
  const raw = node.getText()
  if (!ALPHA_CONCAT_RE.test(raw)) return
  ctx.refusals.push({
    file: ctx.relPath, loc: locOf(sourceFile, ctx.relPath, node), prop: null, value: raw,
    family: 'alpha-concatenation',
    reason: 'A token is a CSS custom-property STRING, not a colour value — string-concatenating a hex alpha suffix onto it produces the invalid CSS literal "var(--x)HH" the moment the source becomes a token (mapping.md "Gate-2 finding #1"). This script cannot prove what the substituted expression evaluates to, so it always refuses this shape; TaskNode.tsx\'s toTint() guard (or reading the registered *-background token directly) is the correct fix, applied by a human.',
  })
}

export function planTsFile(repoRoot, filePath, tables) {
  const relPath = relative(repoRoot, filePath).split(sep).join('/')
  const text = readFile(filePath)
  const sourceFile = ts.createSourceFile(filePath, text, ts.ScriptTarget.Latest, true, filePath.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const isGoverned = tables.governedDataFiles.has(relPath)
  const ctx = { relPath, isGoverned, edits: [], refusals: [] }

  const visit = (node) => {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      planStringLiteral(node, sourceFile, ctx, tables)
    } else if (ts.isTemplateExpression(node)) {
      planTemplateExpression(node, sourceFile, ctx)
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)

  const after = ctx.edits.length > 0
    ? applyEdits(text, ctx.edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    : text
  return { relPath, before: text, after, edits: ctx.edits, refusals: ctx.refusals }
}

// ── file discovery ───────────────────────────────────────────────────────

function listCandidateFiles(repoRoot) {
  const out = []
  const walk = (dir) => {
    let names
    try { names = readdirSync(dir) } catch { return }
    for (const name of names) {
      if (SKIP_DIR_NAMES.has(name)) continue
      const full = resolve(dir, name)
      let st
      try { st = statSync(full) } catch { continue }
      if (st.isDirectory()) { walk(full); continue }
      if (!SCANNED_EXTENSIONS.has(extname(name))) continue
      if (TEST_FILE_RE.test(name)) continue
      const relPosix = relative(repoRoot, full).split(sep).join('/')
      if (CANONICAL_GENERATED_TOKEN_PATHS.has(relPosix)) continue
      out.push(full)
    }
  }
  for (const root of ALLOWED_ROOTS) walk(resolve(repoRoot, root))
  return out
}

// ── orchestration ────────────────────────────────────────────────────────

export function runCodemod({ repoRoot, apply = false, safeSplitPath }) {
  const resolvedPath = safeSplitPath ?? resolve(repoRoot, DEFAULT_SAFE_SPLIT_PATH)
  const safeSplit = loadSafeSplit(resolvedPath)
  const tables = buildTables(safeSplit)

  const files = listCandidateFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0

  for (const filePath of files) {
    const result = extname(filePath) === '.css'
      ? planCssFile(repoRoot, filePath, tables)
      : planTsFile(repoRoot, filePath, tables)
    if (result.edits.length === 0 && result.refusals.length === 0) continue
    const diff = result.edits.length > 0 ? unifiedDiff(result.relPath, result.before, result.after) : ''
    fileResults.push({
      file: result.relPath,
      editCount: result.edits.length,
      refusalCount: result.refusals.length,
      edits: result.edits.map((e) => ({ loc: e.loc, prop: e.prop, before: e.before, after: e.after })),
      refusals: result.refusals,
      diff,
    })
    totalEdits += result.edits.length
    totalRefusals += result.refusals.length
    if (apply && result.edits.length > 0) writeFileAtomic(filePath, result.after)
  }

  return {
    safeSplitPath: resolvedPath,
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────

function readFlag(argv, name) {
  const eqPrefix = `--${name}=`
  const eqEntry = argv.find((a) => a.startsWith(eqPrefix))
  if (eqEntry) return eqEntry.slice(eqPrefix.length)
  const index = argv.indexOf(`--${name}`)
  if (index >= 0 && index + 1 < argv.length) return argv[index + 1]
  return undefined
}

const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const argv = process.argv.slice(2)
  const { apply, root } = parseCodemodArgs(argv, resolve(new URL('../..', import.meta.url).pathname))
  const safeSplitFlag = readFlag(argv, 'safe-split')
  const safeSplitPath = safeSplitFlag ? resolve(safeSplitFlag) : undefined

  const result = runCodemod({ repoRoot: root, apply, safeSplitPath })

  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) {
      console.log(`REFUSED ${r.loc} \`${r.prop ?? '(literal)'}: ${r.value}\` [${r.family}] — ${r.reason}`)
    }
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    safeSplitPath: result.safeSplitPath,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
