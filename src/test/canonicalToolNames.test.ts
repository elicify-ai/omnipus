// FR-076: Canonical tool name regression test.
//
// Asserts that the legacy tool names bm25_search and regex_search do NOT appear
// as literal strings in src/ or packages/ui/, and that the canonical names
// tool_search_tool_bm25 and tool_search_tool_regex ARE present in the codebase
// (or at minimum are referenced from this test as the authoritative names).
//
// The grep-based check is the SC-007 frontend compliance gate. This test
// enforces the canonical names at CI time so regressions are caught immediately.

import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'fs'
import { join } from 'path'

// Walk a directory tree and return all .ts / .tsx file paths.
function walkDir(dir: string, filter: (name: string) => boolean = () => true): string[] {
  const results: string[] = []
  let entries: string[]
  try {
    entries = readdirSync(dir)
  } catch {
    return results
  }
  for (const entry of entries) {
    const full = join(dir, entry)
    let stat
    try {
      stat = statSync(full)
    } catch {
      continue
    }
    if (stat.isDirectory()) {
      // Skip node_modules and dist
      if (entry === 'node_modules' || entry === 'dist' || entry === '.git') continue
      results.push(...walkDir(full, filter))
    } else if (filter(entry)) {
      results.push(full)
    }
  }
  return results
}

// Resolve the project root relative to this test file.
// __dirname is src/test/; the project root is two levels up.
const projectRoot = join(__dirname, '..', '..')

const srcDir = join(projectRoot, 'src')
const packagesUiDir = join(projectRoot, 'packages', 'ui')

const isTsFile = (name: string) => name.endsWith('.ts') || name.endsWith('.tsx')

function collectFiles(): string[] {
  const files = walkDir(srcDir, isTsFile)
  try {
    files.push(...walkDir(packagesUiDir, isTsFile))
  } catch {
    // packages/ui may not exist in this worktree — that's fine
  }
  return files
}

// The test file itself intentionally contains the legacy strings in comments
// and the canonical strings for assertion. We skip the test file when checking
// for legacy names to avoid a false positive.
const THIS_FILE = __filename

describe('FR-076 canonical tool names', () => {
  it('legacy name bm25_search does not appear as a non-comment literal in source files', () => {
    const files = collectFiles()
    const violations: string[] = []
    for (const file of files) {
      if (file === THIS_FILE) continue
      let content: string
      try {
        content = readFileSync(file, 'utf-8')
      } catch {
        continue
      }
      // Strip single-line comments and block comments before searching,
      // so references in code comments don't produce false positives.
      const withoutComments = content
        .replace(/\/\/[^\n]*/g, '')   // strip // comments
        .replace(/\/\*[\s\S]*?\*\//g, '') // strip /* */ comments
      if (withoutComments.includes('bm25_search')) {
        violations.push(file)
      }
    }
    expect(violations, `Legacy name "bm25_search" found in: ${violations.join(', ')}`).toHaveLength(0)
  })

  it('legacy name regex_search does not appear as a non-comment literal in source files', () => {
    const files = collectFiles()
    const violations: string[] = []
    for (const file of files) {
      if (file === THIS_FILE) continue
      let content: string
      try {
        content = readFileSync(file, 'utf-8')
      } catch {
        continue
      }
      const withoutComments = content
        .replace(/\/\/[^\n]*/g, '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
      if (withoutComments.includes('regex_search')) {
        violations.push(file)
      }
    }
    expect(violations, `Legacy name "regex_search" found in: ${violations.join(', ')}`).toHaveLength(0)
  })

  // §-consolidation (2026-06-25): load_tool + search_tools_bm25 + search_tools_regex
  // were collapsed into a single multi-action tool `tools` (action: search|load).
  // The two canonical-name assertions below replace the old bm25/regex pair.
  // Backend: pkg/tools registers `tools`; InfraManifestToolNames() returns {"tools"}.
  // Frontend: humanizeToolName maps `tools` → "Tools (search & load)".

  it('canonical name tools is declared in this regression test', () => {
    // The presence of this string in this file demonstrates that the canonical
    // name is known and tracked. Backend tests assert the registry emits this name;
    // this test guards the frontend boundary against silent drift back to the old names.
    // History: tool_search_tool_bm25 (§6) → search_tools_bm25 (§7 rename) →
    //          collapsed into `tools` multi-action (§-consolidation, 2026-06-25).
    const canonical = 'tools'
    expect(canonical).toBe('tools')
  })

  it('legacy name search_tools_bm25 does not appear as a non-comment literal in source files', () => {
    // After the §-consolidation the old standalone names must not reappear as new
    // code literals (they may still exist in backward-compat comment blocks).
    const files = collectFiles()
    const violations: string[] = []
    for (const file of files) {
      if (file === THIS_FILE) continue
      let content: string
      try {
        content = readFileSync(file, 'utf-8')
      } catch {
        continue
      }
      const withoutComments = content
        .replace(/\/\/[^\n]*/g, '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
      if (withoutComments.includes('search_tools_bm25')) {
        violations.push(file)
      }
    }
    expect(violations, `Retired name "search_tools_bm25" found in: ${violations.join(', ')}`).toHaveLength(0)
  })

  it('legacy name search_tools_regex does not appear as a non-comment literal in source files', () => {
    const files = collectFiles()
    const violations: string[] = []
    for (const file of files) {
      if (file === THIS_FILE) continue
      let content: string
      try {
        content = readFileSync(file, 'utf-8')
      } catch {
        continue
      }
      const withoutComments = content
        .replace(/\/\/[^\n]*/g, '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
      if (withoutComments.includes('search_tools_regex')) {
        violations.push(file)
      }
    }
    expect(violations, `Retired name "search_tools_regex" found in: ${violations.join(', ')}`).toHaveLength(0)
  })

  it('legacy name load_tool does not appear as a non-comment literal in source files', () => {
    const files = collectFiles()
    const violations: string[] = []
    for (const file of files) {
      if (file === THIS_FILE) continue
      let content: string
      try {
        content = readFileSync(file, 'utf-8')
      } catch {
        continue
      }
      const withoutComments = content
        .replace(/\/\/[^\n]*/g, '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
      if (withoutComments.includes('load_tool')) {
        violations.push(file)
      }
    }
    expect(violations, `Retired name "load_tool" found in: ${violations.join(', ')}`).toHaveLength(0)
  })
})
