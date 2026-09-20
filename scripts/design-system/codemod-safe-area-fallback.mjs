#!/usr/bin/env node
// scripts/design-system/codemod-safe-area-fallback.mjs
//
// C1 repair for spacing/missing-safe-area-fallback.
//
// The spacing lock (scripts/design-system-locks/spacing.mjs::
// analyzeEnvironmentSpacing) requires every safe-area env() to carry an
// explicit closed-scale fallback as its SECOND argument -- env(name, fallback)
// -- not a max() wrapper. A bare env(safe-area-inset-bottom) is a finding
// because on a browser without safe-area support the whole declaration is
// invalid and the padding silently vanishes.
//
// This script inserts exactly one fallback, var(--space-0), and changes
// nothing else. That choice is deliberate rather than convenient: --space-0 is
// 0px, which is precisely what a supporting browser already computes when the
// inset is zero, so the rendered result is unchanged everywhere the current
// code works, and is 0 instead of "declaration dropped" everywhere it does
// not. Any other fallback would be a design decision about how much padding a
// non-notched device deserves, which is not this script's to make.
//
// It deliberately does NOT touch the surrounding expression. Several sites
// read max(0.5rem, env(safe-area-inset-bottom)); the 0.5rem is root-font
// dependent and belongs to the separate spacing/root-dependent family, whose
// disposition is a founder decision (rem scales with a user-adjusted root
// font, a px token does not). Rewriting it here would smuggle that decision
// into an unrelated batch.
//
// Refuses -- reports and edits nothing -- when an env() already has more than
// one argument, when the name is not a safe-area inset, or when the text is
// not in an allowed root.
//
// CLI:
//   node scripts/design-system/codemod-safe-area-fallback.mjs            # preview
//   node scripts/design-system/codemod-safe-area-fallback.mjs --apply    # write

import { existsSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { ALLOWED_ROOTS, applyEdits, parseCodemodArgs, readFile, unifiedDiff, writeFileAtomic } from './codemod-lib.mjs'

export const FALLBACK = 'var(--space-0)'

const SAFE_AREA_NAMES = new Set([
  'safe-area-inset-top',
  'safe-area-inset-right',
  'safe-area-inset-bottom',
  'safe-area-inset-left',
])

const EXTENSIONS = new Set(['.css', '.js', '.jsx', '.ts', '.tsx'])

/**
 * Collect every source file under the allowed roots. Walked here rather than
 * via codemod-lib's listSourceFiles because this repair also has to reach
 * .css, which that helper's TypeScript-oriented list does not carry.
 */
export function collectFiles(repoRoot, roots = ALLOWED_ROOTS) {
  const out = []
  for (const root of roots) {
    const absRoot = path.join(repoRoot, root)
    if (!existsSync(absRoot)) continue
    const stack = [absRoot]
    while (stack.length) {
      const current = stack.pop()
      for (const entry of readdirSync(current)) {
        const abs = path.join(current, entry)
        if (statSync(abs).isDirectory()) {
          if (entry !== 'node_modules') stack.push(abs)
          continue
        }
        if (EXTENSIONS.has(path.extname(abs))) out.push(abs)
      }
    }
  }
  return out.sort()
}

/**
 * Find the matching close paren for the `env(` whose open paren is at
 * openIndex, counting nesting. Returns -1 when unbalanced, which is a refusal
 * rather than a guess.
 */
function matchingParen(text, openIndex) {
  let depth = 0
  for (let i = openIndex; i < text.length; i += 1) {
    const ch = text[i]
    if (ch === '(') depth += 1
    else if (ch === ')') {
      depth -= 1
      if (depth === 0) return i
    }
  }
  return -1
}

/**
 * Split on top-level commas only, so env(x, max(a, b)) reads as two arguments.
 */
function splitTopLevel(text) {
  const parts = []
  let depth = 0
  let start = 0
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i]
    if (ch === '(') depth += 1
    else if (ch === ')') depth -= 1
    else if (ch === ',' && depth === 0) {
      parts.push(text.slice(start, i))
      start = i + 1
    }
  }
  parts.push(text.slice(start))
  return parts
}

/**
 * Mark every index that sits inside a comment. Both CSS and TS/JS are covered:
 * block comments for either, plus line comments for TS/JS. A codemod must not
 * rewrite prose it does not understand -- globals.css documents the very class
 * this script edits, and silently rewriting that sentence would be the script
 * editing its own documentation. Such sites are reported as refusals so a
 * human updates the wording deliberately.
 */
export function commentMask(text) {
  const mask = new Uint8Array(text.length)
  let i = 0
  while (i < text.length) {
    const two = text.slice(i, i + 2)
    if (two === '/*') {
      const end = text.indexOf('*/', i + 2)
      const stop = end === -1 ? text.length : end + 2
      mask.fill(1, i, stop)
      i = stop
      continue
    }
    if (two === '//') {
      let end = text.indexOf('\n', i)
      if (end === -1) end = text.length
      mask.fill(1, i, end)
      i = end
      continue
    }
    i += 1
  }
  return mask
}

/**
 * Plan the edits for one file's text. Pure: never touches disk.
 */
export function planFile(relPath, text) {
  const edits = []
  const refusals = []
  const mask = commentMask(text)
  const pattern = /\benv\s*\(/g
  let match
  while ((match = pattern.exec(text)) !== null) {
    if (mask[match.index]) {
      const line = text.slice(0, match.index).split('\n').length
      refusals.push({ index: match.index, reason: `line ${line}: env() inside a comment -- prose is not rewritten mechanically; update the wording by hand if it is now stale` })
      continue
    }
    const openIndex = text.indexOf('(', match.index)
    const closeIndex = matchingParen(text, openIndex)
    if (closeIndex === -1) {
      refusals.push({ index: match.index, reason: 'unbalanced env() parentheses -- refusing rather than guessing where it ends' })
      continue
    }
    const inner = text.slice(openIndex + 1, closeIndex)
    const args = splitTopLevel(inner)
    const name = args[0]?.trim().toLowerCase()
    if (!SAFE_AREA_NAMES.has(name)) continue
    if (args.length !== 1) continue
    edits.push({ start: closeIndex, end: closeIndex, replacement: `,${FALLBACK}` })
  }
  return { file: relPath, edits, refusals, editCount: edits.length }
}

/**
 * Run the codemod over a repo root. Returns a summary; writes only when
 * apply is true.
 */
export function runCodemod({ repoRoot, apply = false, roots = ALLOWED_ROOTS, emitDiff = () => {} } = {}) {
  const files = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const abs of collectFiles(repoRoot, roots)) {
    const relPath = path.relative(repoRoot, abs).split(path.sep).join('/')
    const before = readFile(abs)
    const plan = planFile(relPath, before)
    if (plan.editCount === 0 && plan.refusals.length === 0) continue
    totalEdits += plan.editCount
    totalRefusals += plan.refusals.length
    for (const refusal of plan.refusals) emitDiff(`REFUSED ${relPath} -- ${refusal.reason}`)
    if (plan.editCount > 0) {
      const after = applyEdits(before, plan.edits)
      emitDiff(unifiedDiff(relPath, before, after))
      if (apply) writeFileAtomic(abs, after)
      files.push({ file: relPath, editCount: plan.editCount, refusals: plan.refusals })
    } else {
      files.push({ file: relPath, editCount: 0, refusals: plan.refusals })
    }
  }
  return {
    mode: apply ? 'apply' : 'dry-run',
    totalFilesTouched: files.filter((f) => f.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files,
  }
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  const scriptDir = path.dirname(fileURLToPath(import.meta.url))
  const defaultRoot = path.resolve(scriptDir, '..', '..')
  const args = parseCodemodArgs(process.argv.slice(2), defaultRoot)
  const summary = runCodemod({ repoRoot: args.repoRoot ?? defaultRoot, apply: args.apply === true, emitDiff: (line) => console.log(line) })
  const { files, ...counts } = summary
  console.log(JSON.stringify(counts, null, 2))
}
