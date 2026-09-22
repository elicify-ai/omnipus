// Shared helpers for scripts/design-system/codemod-*.mjs — exact-source
// repair codemods for Stage B closure (design-system-migration-plan.md,
// "Stage B closure — founder decisions, 2026-09-19"). Each codemod:
//   - defaults to dry-run: prints a unified diff + JSON summary, writes nothing;
//   - only applies with --apply;
//   - never edits outside src/ or packages/ui/src/;
//   - refuses (and reports) any file where the target pattern is ambiguous;
//   - is idempotent: a second --apply makes zero further changes.
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs'
import { relative, resolve, extname, sep } from 'node:path'
import ts from 'typescript'

export const ALLOWED_ROOTS = Object.freeze(['src', 'packages/ui/src'])

const SOURCE_EXTENSIONS = new Set(['.ts', '.tsx'])
const SKIP_DIR_NAMES = new Set(['node_modules', 'dist', '.git'])

/** Recursively lists every .ts/.tsx file under the given root-relative directories. */
export function listSourceFiles(repoRoot, roots = ALLOWED_ROOTS) {
  const out = []
  const walk = (dir) => {
    let names
    try { names = readdirSync(dir) } catch { return }
    for (const name of names) {
      if (SKIP_DIR_NAMES.has(name)) continue
      const full = resolve(dir, name)
      const st = statSync(full)
      if (st.isDirectory()) walk(full)
      else if (SOURCE_EXTENSIONS.has(extname(name))) out.push(full)
    }
  }
  for (const root of roots) walk(resolve(repoRoot, root))
  return out
}

/** True when `absPath` is inside one of the codemod's legal edit roots. */
export function isInAllowedRoot(repoRoot, absPath) {
  const rel = relative(repoRoot, absPath)
  if (rel.startsWith('..') || rel === '') return false
  return ALLOWED_ROOTS.some((root) => rel === root || rel.startsWith(root + sep) || rel.startsWith(root + '/'))
}

export function parseSourceFile(filePath, text) {
  return ts.createSourceFile(filePath, text, ts.ScriptTarget.Latest, true, filePath.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
}

/**
 * Applies a set of non-overlapping text edits ({start, end, replacement}) to
 * `text`, splicing from the highest offset down so earlier offsets stay
 * valid. Edits must not overlap — callers are responsible for that; this
 * throws on overlap rather than silently corrupting output.
 */
export function applyEdits(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  let lastStart = Infinity
  for (const edit of sorted) {
    if (edit.end > lastStart) throw new Error(`overlapping codemod edits at ${edit.start}-${edit.end}`)
    result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
    lastStart = edit.start
  }
  return result
}

/** Minimal unified-diff-style renderer (line-based, no external dependency). */
export function unifiedDiff(filePath, before, after) {
  if (before === after) return ''
  const beforeLines = before.split('\n')
  const afterLines = after.split('\n')
  const max = Math.max(beforeLines.length, afterLines.length)
  const lines = [`--- a/${filePath}`, `+++ b/${filePath}`]
  let inHunk = false
  for (let i = 0; i < max; i++) {
    const b = beforeLines[i]
    const a = afterLines[i]
    if (b === a) { inHunk = false; continue }
    if (!inHunk) { lines.push(`@@ line ${i + 1} @@`); inHunk = true }
    if (b !== undefined) lines.push(`-${b}`)
    if (a !== undefined) lines.push(`+${a}`)
  }
  return lines.join('\n') + '\n'
}

/** Parses CLI args shared by every codemod: --apply (default dry-run), --root <repoRoot>. */
export function parseCodemodArgs(argv, defaultRoot) {
  const apply = argv.includes('--apply')
  const rootFlagIndex = argv.indexOf('--root')
  const root = rootFlagIndex >= 0 ? argv[rootFlagIndex + 1] : defaultRoot
  return { apply, root: resolve(root) }
}

export function readFile(path) {
  return readFileSync(path, 'utf8')
}

export function writeFileAtomic(path, content) {
  writeFileSync(path, content, 'utf8')
}

/** Finds the innermost enclosing function-like body (Block) containing `node`, or the SourceFile itself. */
export function enclosingFunctionBody(node) {
  let current = node.parent
  while (current) {
    if (
      (ts.isFunctionDeclaration(current) || ts.isFunctionExpression(current) || ts.isArrowFunction(current) || ts.isMethodDeclaration(current)) &&
      current.body && ts.isBlock(current.body)
    ) {
      return current.body
    }
    current = current.parent
  }
  return node.getSourceFile()
}
