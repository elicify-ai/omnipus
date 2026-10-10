/**
 * noLegacyIconImports.test.ts — static guard for the deleted legacy icon
 * system (founder 2026-10-10: "Delete the old Phosphor per-agent icon
 * system: IconRenderer, src/lib/agentIcons.ts … No deprecated code left
 * behind").
 *
 * A merge from an old branch can resurrect a deleted module as an ordinary,
 * conflict-free import. This guard re-runs the deletion search on every test
 * run: it must find ZERO files under src/ importing the deleted modules.
 *
 * The search is proven live before it is trusted: the same walker, with the
 * same matching, is first asserted to FIND known positives (the AgentMark
 * import in AgentCard.tsx, and any import of a module that exists). A search
 * that cannot find what exists cannot be believed when it finds nothing.
 */
import { describe, expect, it } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const SRC_ROOT = join(process.cwd(), 'src')

/** Every .ts/.tsx file under src/, as repo-relative paths. */
function walkSourceFiles(dir: string): string[] {
  const found: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      found.push(...walkSourceFiles(full))
    } else if (/\.(ts|tsx)$/.test(entry)) {
      found.push(full)
    }
  }
  return found
}

/** Files whose source text imports the given module specifier (any import form). */
function filesImporting(files: string[], specifier: string): string[] {
  const needle = new RegExp(
    `from\\s+['"]${specifier.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}['"]` +
    `|import\\s+\\(\\s*['"]${specifier.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}['"]\\s*\\)` +
    `|import\\s+['"]${specifier.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}['"]`,
  )
  return files.filter((file) => needle.test(readFileSync(file, 'utf8')))
}

describe('static guard — the legacy per-agent icon system stays deleted', () => {
  const files = walkSourceFiles(SRC_ROOT)

  it('the search is live: it reads the whole src/ tree', () => {
    // A walker that silently read 0 files would "pass" everything below.
    expect(files.length).toBeGreaterThan(500)
  })

  it('the search is live: it finds a known real import (AgentMark in AgentCard.tsx)', () => {
    const hits = filesImporting(files, '@/components/agents/AgentMark')
    const agentCard = hits.find((file) => file.endsWith(join('src', 'components', 'agents', 'AgentCard.tsx')))
    expect(agentCard, 'AgentCard.tsx must appear among AgentMark importers').toBeDefined()
    expect(hits.length).toBeGreaterThan(5)
  })

  it('the search is live: it finds imports of a module that exists (@/lib/utils)', () => {
    expect(filesImporting(files, '@/lib/utils').length).toBeGreaterThan(10)
  })

  it('no file imports the deleted IconRenderer', () => {
    const hits = filesImporting(files, '@/components/shared/IconRenderer')
    expect(
      hits.map((file) => relative(process.cwd(), file).split(sep).join('/')),
      'IconRenderer was deleted (founder 2026-10-10) — nothing may import it',
    ).toEqual([])
  })

  it('no file imports the deleted agentIcons module', () => {
    const hits = filesImporting(files, '@/lib/agentIcons')
    expect(
      hits.map((file) => relative(process.cwd(), file).split(sep).join('/')),
      'src/lib/agentIcons.ts was deleted (founder 2026-10-10) — nothing may import it',
    ).toEqual([])
  })
})
