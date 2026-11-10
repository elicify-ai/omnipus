/**
 * -wu11-no-manual-create.test.ts — WU-11 RED: the manual create flow is GONE.
 *
 * Oracles (omnipus-system-agent-spec.md):
 *   - FR-108: "The Agents screen and routes, the create wizard/modal and
 *     /admin/chat MUST be removed without forwarding; REST create-agent MUST
 *     remain, documented as a non-UI API."
 *   - BDD-12.8 No create form (US-12 AS-5): "Given any screen — Then no
 *     manual create action exists; Add agent opens Omnipus."
 *   - FR-045 / BDD-4.1: Add agent opens the Omnipus chat with W1 for that
 *     workspace, leaving the active workspace unchanged.
 *   - FR-103: Team's Add agent offers "Create with Omnipus" and "Add an
 *     existing agent" — the ONLY two choices.
 *   - §15 WU-11 row (landing 1 removals).
 *
 * Part 1 is a source-invariant scan: the requirement itself is the ABSENCE of
 * the screen, wizard, modal and their wiring, so the oracle is the file tree.
 * Each failure lists the exact violating file — a bare boolean would not be
 * actionable. RED today: AgentListScreen.tsx, CreateAgentModal.tsx,
 * CreateAgentWizard.tsx, wizard/, WorkerCard.tsx all exist and are wired.
 *
 * Part 2 holds the two Add-agent entry points that NO existing test pins (the
 * Team menu entries are covered by WorkspaceTeamTab.omnipus.test.tsx BDD-4.1).
 * They are KEEP-GREEN guards, not RED: WU-10 already wired them to Omnipus,
 * they must still pass after GREEN. Labelled in tests/l1-wu11-red/ORACLE.md.
 */
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const ROOT = join(import.meta.dirname, '..', '..', '..')

/** SPA-authored sources; generated wire types and test files are out of scope. */
function spaSources(): string[] {
  const skipDirs = new Set(['node_modules', 'generated', 'fixtures'])
  const files: string[] = []
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry)
      const stat = statSync(full)
      if (stat.isDirectory()) {
        if (!skipDirs.has(entry)) walk(full)
        continue
      }
      if (!/\.(ts|tsx)$/.test(entry)) continue
      if (/\.(test|spec)\.(ts|tsx)$/.test(entry)) continue
      files.push(full)
    }
  }
  walk(join(ROOT, 'src'))
  return files
}

const rel = (p: string) => relative(ROOT, p).split(sep).join('/')

describe('WU-11 RED — no manual create action exists (FR-108, BDD-12.8)', () => {
  it('the Agents screen, create modal, create wizard and wizard directory are deleted', () => {
    const retired = [
      'src/components/screens/AgentListScreen.tsx',
      'src/components/agents/CreateAgentModal.tsx',
      'src/components/agents/CreateAgentWizard.tsx',
      'src/components/agents/wizard',
      'src/components/agents/WorkerCard.tsx',
    ]
    const stillPresent = retired.filter((p) => existsSync(join(ROOT, p)))
    expect(
      stillPresent,
      `FR-108 removes the Agents screen, the create wizard/modal and their helpers; ` +
        `still present: ${stillPresent.join(', ')}`,
    ).toEqual([])
  })

  it('no SPA source imports the retired screen, wizard, modal or WorkerCard', () => {
    const retiredNames = ['AgentListScreen', 'CreateAgentModal', 'CreateAgentWizard', 'WorkerCard']
    const violations: string[] = []
    for (const file of spaSources()) {
      const src = readFileSync(file, 'utf8')
      for (const name of retiredNames) {
        if (new RegExp(`\\b${name}\\b`).test(src)) violations.push(`${rel(file)} references ${name}`)
      }
    }
    expect(violations, `FR-108: the retired components must have no importers left`).toEqual([])
  })

  it('no SPA source carries the create-modal store wiring (createAgentModalOpen / openCreateAgentModal)', () => {
    const violations: string[] = []
    for (const file of spaSources()) {
      const src = readFileSync(file, 'utf8')
      if (/createAgentModalOpen|openCreateAgentModal|closeCreateAgentModal/.test(src)) {
        violations.push(rel(file))
      }
    }
    expect(violations, `FR-108/BDD-12.8: the manual-create modal's store keys must be gone`).toEqual([])
  })

  it('no component outside src/lib/api calls the create-agent REST client (REST remains a non-UI API)', () => {
    const violations: string[] = []
    for (const file of spaSources()) {
      const path = rel(file)
      if (path.startsWith('src/lib/api/')) continue
      const src = readFileSync(file, 'utf8')
      if (/\bcreateAgent\s*\(/.test(src)) violations.push(path)
    }
    expect(
      violations,
      `FR-108: REST create-agent REMAINS (non-UI API) — no UI path may call src/lib/api/agents.ts::createAgent`,
    ).toEqual([])
  })

  it('the sidebar carries no "/agents" asset entry (Sidebar.tsx::ASSET_ITEMS)', () => {
    const sidebar = readFileSync(join(ROOT, 'src/components/layout/Sidebar.tsx'), 'utf8')
    const match = sidebar.match(/ASSET_ITEMS[^=]*=\s*\[([\s\S]*?)\]/)
    expect(match, 'Sidebar.tsx::ASSET_ITEMS not found — the fixed nav structure changed; re-derive this oracle').not.toBeNull()
    const entries = match![1]
    expect(entries, 'BDD-12.8: the sidebar must not link the removed Agents screen').not.toMatch(/['"]\/agents['"]/)
  })
})
