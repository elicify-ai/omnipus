// panelShell.teamSwitchFollows.test.ts — side-panel-shell-spec.md SP-36
// (gate-round-2 finding N2): the TEAM panel follows the workspace switch —
// always.
//
// A NEW file by ruling: the colocated GREEN-side types.policy.test.ts
// (commit 210244c18, not authored by this RED lane) must not be edited by
// this pack, and my earlier §12 #20 pack (panelShell.workspaceSwitch.test.ts)
// covers browser/library/mail/tasks/calendar — team is its one gap.
//
// The pin has two halves, both from the policy contract the spec gives the
// panel registry (US-3, FR-020, §8.1): the POLICY RECORD value
// ('always') and the BEHAVIOUR that value buys — an unscoped Team panel
// (no openedWorkspaceId) still follows the switch, which is exactly what
// distinguishes 'always' from 'when-scoped' (library's value). The guard
// still gates the follow (CRIT-001/FR-013 — a declined beforeLeave cancels).
//
// RED on this pre-GREEN tree: neither module member exists yet
// (types.ts carries no PANEL_POLICIES; workspaceSwitch.ts does not exist),
// so every test fails BLOCKED naming the missing unit — never a skip.

import { describe, expect, it } from 'vitest'

const TYPES_MODULE = '@/components/panel-shell/types'
const SWITCH_MODULE = '@/components/panel-shell/workspaceSwitch'

type SwitchInput = {
  panelId: 'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'
  openedWorkspaceId?: string
  nextWorkspaceId: string
  beforeLeave?: () => Promise<boolean>
}
type Decision =
  | { action: 'stay' }
  | { action: 'follow'; workspaceId: string }
  | { action: 'cancel' }

async function loadPolicy(): Promise<{
  teamSwitchFollows: string
}> {
  const mod = (await import(/* @vite-ignore */ TYPES_MODULE)) as Record<string, unknown>
  const policies = mod.PANEL_POLICIES as
    | Record<string, { switchFollows?: unknown }>
    | undefined
  if (!policies || typeof policies !== 'object' || !policies.team) {
    throw new Error(
      'BLOCKED: panel-shell types carry no PANEL_POLICIES.team record — required by SP-36/FR-020 ' +
        '(the workspace-switch rule reads the panel policy: team follows the switch "always")',
    )
  }
  if (policies.team.switchFollows === undefined) {
    throw new Error(
      'BLOCKED: PANEL_POLICIES.team carries no switchFollows value — required by SP-36/FR-020',
    )
  }
  return { teamSwitchFollows: String(policies.team.switchFollows) }
}

async function loadSwitch(): Promise<{
  resolveWorkspaceSwitch: (input: SwitchInput) => Promise<Decision>
}> {
  const mod = (await import(/* @vite-ignore */ SWITCH_MODULE)) as Record<string, unknown>
  const fn = mod.resolveWorkspaceSwitch
  if (typeof fn !== 'function') {
    throw new Error(
      'BLOCKED: workspaceSwitch.ts exports no resolveWorkspaceSwitch — required by SP-36/FR-020 ' +
        '(team must follow the workspace switch per its policy)',
    )
  }
  return {
    resolveWorkspaceSwitch: fn as (input: SwitchInput) => Promise<Decision>,
  }
}

describe('SP-36 — the Team panel follows the workspace switch (always)', () => {
  it('RED/BLOCKED — the policy record pins team.switchFollows to "always"', async () => {
    const { teamSwitchFollows } = await loadPolicy()
    expect(teamSwitchFollows).toBe('always')
  })

  it('RED/BLOCKED — an UNSCOPED Team panel (no openedWorkspaceId) still follows the switch', async () => {
    await loadPolicy()
    const { resolveWorkspaceSwitch } = await loadSwitch()
    // No openedWorkspaceId: under 'when-scoped' this would STAY; only the
    // team policy's 'always' makes it follow. The exact decision object is
    // the spec shape (FR-020): follow, echoing the next workspace.
    await expect(
      resolveWorkspaceSwitch({ panelId: 'team', nextWorkspaceId: 'ws-b' }),
    ).resolves.toEqual({ action: 'follow', workspaceId: 'ws-b' })
  })

  it('RED/BLOCKED — a declined beforeLeave CANCELS even for the "always" policy (CRIT-001/FR-013)', async () => {
    await loadPolicy()
    const { resolveWorkspaceSwitch } = await loadSwitch()
    await expect(
      resolveWorkspaceSwitch({
        panelId: 'team',
        openedWorkspaceId: 'ws-a',
        nextWorkspaceId: 'ws-b',
        beforeLeave: async () => false,
      }),
    ).resolves.toEqual({ action: 'cancel' })
  })
})
