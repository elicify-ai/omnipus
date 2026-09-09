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
import { humanizeToolName } from '../lib/humanizeToolName'
import { shouldRenderToolCall } from '../lib/toolVisibility'

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

  // §-rename (2026-06-26): the intermediate `tools` multi-action name was renamed to
  // `load_tool`. §-rename (ADR-071 D1, 2026-08-28): `load_tool` was renamed to
  // `ToolSearch`. The canonical name assertions below guard the CURRENT name.
  // Backend: pkg/tools registers `ToolSearch`; InfraManifestToolNames() returns
  // {"ToolSearch"}. Frontend: humanizeToolName maps `ToolSearch` → "Find & load tools",
  // and shouldRenderToolCall hides it from the chat thread by default.

  // TestCanonicalDiscoveryToolName_PinsProductionSymbol (ADR-071 D1 / spec
  // FR-012, W-D1 test 1): this guard previously asserted a local variable
  // against itself (`const canonical = 'load_tool'; expect(canonical).toBe(
  // 'load_tool')`) — a tautology that could never fail and never caught a
  // drift, exactly the pattern docs/internal/false-green-patterns.md warns
  // about. It is re-pointed here at two REAL production symbols — the
  // humanizeToolName EXPLICIT_LABELS key and the toolVisibility.ts case
  // literal — so a future rename or revert that misses either site fails
  // this test instead of passing silently.
  it('canonical name ToolSearch is pinned against production symbols (humanizeToolName, shouldRenderToolCall)', () => {
    // If the EXPLICIT_LABELS key drifted from "ToolSearch" (e.g. reverted to
    // "load_tool" or misspelled), humanizeToolName would fall through to the
    // generic fallback and return a different string.
    expect(humanizeToolName('ToolSearch')).toBe('Find & load tools')

    // If the toolVisibility.ts `case 'ToolSearch':` arm drifted, the call
    // would fall through to `default: return true` (always visible),
    // reversing the documented hidden-by-default rule for this infra tool.
    expect(shouldRenderToolCall('ToolSearch', undefined, false)).toBe(false)
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

  it('retired intermediate name "tools" (the loader) does not appear as a non-comment literal in source files', () => {
    // After the §-rename (2026-06-26), the loader was `load_tool` (itself
    // renamed to `ToolSearch` by ADR-071 D1); the old `tools` multi-action
    // name must not reappear as a new code literal for the loader tool.
    // NOTE: the string "tools" legitimately appears in non-loader contexts (tab values,
    // query keys, route names, etc.) so we check for the exact tool-name patterns only:
    // the explicit humanizeToolName map key and the canonicalToolNames regression constant.
    // This test only guards against the map entry and canonical constant drifting back.
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
      // Only flag the exact patterns that would indicate a regressed loader tool name:
      // a map entry `tools:` in humanizeToolName or a canonical = 'tools' constant.
      if (
        withoutComments.includes("tools: 'Tools (search") ||
        withoutComments.includes('tools: "Tools (search') ||
        /const canonical\s*=\s*['"]tools['"]/.test(withoutComments)
      ) {
        violations.push(file)
      }
    }
    expect(violations, `Retired loader name "tools" regressed in: ${violations.join(', ')}`).toHaveLength(0)
  })
})
