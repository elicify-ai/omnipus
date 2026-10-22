/**
 * -wu11-ava-admin-removed.test.tsx — WU-11 RED: no Ava, no Admin, anywhere in
 * the SPA's agent surfaces.
 *
 * Oracle — FR-008 (omnipus-system-agent-spec.md): "Ava and Admin seeds,
 * prompts, skills, identity branches and UI MUST be removed; a guard script
 * MUST fail CI if `ava` or `admin` reappear as agent ids in production
 * code…" (the guard script itself is the backend lane's; this file is the
 * SPA half of the same FR, per the §15 WU-11 row).
 *
 * Behavioral (RED today):
 *   - AddAgentPicker offers `ava` today — only `admin` is filtered
 *     (AddAgentPicker.tsx). FR-008: neither may ever be selectable.
 *   - workspaceRoster's adminDefault renders an Admin row for the default
 *     workspace from admin_main_session_id (workspaceRoster.ts::adminDefault).
 *     FR-008: no Admin UI — not even the "validated default-workspace main".
 *
 * Static (RED today) — the seven branch sites the frontend impact map names.
 * The scan is deliberately FILE-SCOPED to those sites (never a repo-wide
 * 'admin' grep: the login/onboarding username placeholders and task-fixture
 * owner names are the HUMAN administrator, not the retired Admin agent, and
 * stay). If one of the listed files is deleted outright, absence passes —
 * removal is the goal, and the deletion is the stronger form of it.
 */
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = join(import.meta.dirname, '..', '..', '..')

// ── Behavioral: the picker ───────────────────────────────────────────────────
import { AddAgentPicker } from '@/components/workspaces/team/AddAgentPicker'
import type { Agent, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { workspaceMains } from '@/components/layout/sidebar/workspaceRoster'

function retiredAgent(id: string): Agent {
  return makeAgent({ id, name: id === 'ava' ? 'Ava' : 'Admin', type: 'core' })
}

describe('WU-11 RED — Ava/Admin are never selectable (FR-008)', () => {
  it('AddAgentPicker offers neither Ava nor Admin even when the agents cache still carries them', async () => {
    const user = userEvent.setup()
    const members: Workspace[] = []
    render(
      <AddAgentPicker
        agents={[retiredAgent('ava'), retiredAgent('admin')]}
        memberIds={new Set<string>()}
        onAdd={() => {}}
        workspaces={members}
      />,
    )
    await user.click(screen.getByTestId('team-add-agent'))
    // Today: `ava` survives every filter (only `a.id !== 'admin'` runs) and
    // renders as a clickable candidate — the RED.
    expect(screen.queryByTestId('team-add-agent-option-ava')).toBeNull()
    expect(screen.queryByTestId('team-add-agent-option-admin')).toBeNull()
    expect(screen.queryByText('Ava')).toBeNull()
    expect(screen.queryByText('Admin')).toBeNull()
  })

  it('the default-workspace sidebar roster renders no Admin row from admin_main_session_id', () => {
    const base = {
      id: 'default',
      name: 'Default',
      status: 'active' as const,
      pinned: false,
      pin_order: 0,
      task_count: 0,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      revision: '0'.repeat(64),
      is_default: true,
    }
    const workspace = {
      ...base,
      core_team: [],
      admin_main_session_id: 'main-admin-1',
    }
    const mains = workspaceMains(
      workspace,
      [retiredAgent('admin')],
      [{ id: 'main-admin-1', agent_id: 'admin', title: 'Admin', type: 'main', workspace_id: 'default', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 0 }],
      'fresh',
      [],
    )
    const allRows = [...mains.visible, ...mains.hidden, ...mains.result.rows]
    expect(
      allRows.filter((row) => row.agentId === 'admin'),
      'FR-008: no Admin UI — workspaceRoster.ts::adminDefault must stop projecting an Admin row',
    ).toEqual([])
  })
})

// ── Static: the seven branch sites ───────────────────────────────────────────
// Each site carries at least one `ava`/`admin` agent-id literal today; the
// scan lists every surviving literal, so the RED names the exact branches.
const BRANCH_SITES = [
  'src/components/layout/sidebar/workspaceRoster.ts',
  'src/components/layout/sidebar/AgentRowMenu.tsx',
  'src/lib/nav/eligibleMains.ts',
  'src/hooks/useChatAgents.ts',
  'src/components/workspaces/team/teamGraphModel.ts',
  'src/components/workspaces/team/AddAgentPicker.tsx',
  'src/lib/agentKind.ts',
  'src/routes/_app/admin.chat.tsx',
] as const

describe('WU-11 RED — the Ava/Admin identity branches are deleted (FR-008)', () => {
  it('no retired-agent branch site carries an `ava` or `admin` agent-id literal', () => {
    const violations: string[] = []
    for (const site of BRANCH_SITES) {
      const full = join(ROOT, site)
      if (!existsSync(full)) continue // deleted outright — the stronger form of removal
      const src = readFileSync(full, 'utf8')
      const lines = src.split('\n')
      lines.forEach((line, i) => {
        if (/['"`]ava['"`]|['"`]admin['"`]/.test(line)) {
          violations.push(`${site}:${i + 1}: ${line.trim()}`)
        }
      })
    }
    expect(
      violations,
      'FR-008: Ava and Admin UI and identity branches MUST be removed — these lines still branch on the retired agent ids',
    ).toEqual([])
  })
})

afterEach(() => {
  cleanup()
})
